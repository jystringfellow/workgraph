package workgraph

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

//go:embed all:integrations/codex all:integrations/claude-code all:.agents/skills/workgraph-bridge
var bridgeIntegrationAssets embed.FS

// BridgeInstallConfig controls one client integration installation.
type BridgeInstallConfig struct {
	HomeDir       string
	Client        string
	ClientCommand string
	InstallRoot   string
	SkipLaunchd   bool
}

// BridgeInstallResult describes an installed client bridge package.
type BridgeInstallResult struct {
	Client      string
	InstallRoot string
	Message     string
}

// BridgeDoctorConfig controls provider-free reference integration diagnostics.
type BridgeDoctorConfig struct {
	HomeDir       string
	Client        string
	ClientCommand string
	InstallRoot   string
}

// InstallBridge installs and registers one reference client package idempotently.
func InstallBridge(config BridgeInstallConfig) (BridgeInstallResult, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return BridgeInstallResult{}, err
	}
	client := strings.ToLower(strings.TrimSpace(config.Client))
	if client != "codex" && client != "claude-code" {
		return BridgeInstallResult{}, fmt.Errorf("bridge client must be codex or claude-code")
	}
	commandName := strings.TrimSpace(config.ClientCommand)
	if commandName == "" {
		commandName = client
		if client == "claude-code" {
			commandName = "claude"
		}
	}
	if _, err := exec.LookPath(commandName); err != nil {
		return BridgeInstallResult{}, fmt.Errorf("find %s client: %w", client, err)
	}
	installRoot := strings.TrimSpace(config.InstallRoot)
	if installRoot == "" {
		installRoot = filepath.Join(homeDir, "bridge", "integrations", client)
	}
	installRoot, err = filepath.Abs(installRoot)
	if err != nil {
		return BridgeInstallResult{}, fmt.Errorf("resolve bridge install root: %w", err)
	}
	if err := copyBridgeIntegration(client, installRoot); err != nil {
		return BridgeInstallResult{}, err
	}
	executable, err := os.Executable()
	if err != nil {
		return BridgeInstallResult{}, fmt.Errorf("resolve workgraph executable: %w", err)
	}
	if err := writeInstalledBridgeMCP(client, installRoot, executable, homeDir); err != nil {
		return BridgeInstallResult{}, err
	}
	if err := registerBridgePlugin(client, commandName, installRoot); err != nil {
		return BridgeInstallResult{}, err
	}
	launchStatus := "launchd setup skipped"
	if !config.SkipLaunchd {
		if err := installBridgeLaunchAgent(client, commandName, executable, homeDir); err != nil {
			return BridgeInstallResult{}, err
		}
		launchStatus = "launchd worker installed"
	}
	result := BridgeInstallResult{Client: client, InstallRoot: installRoot}
	result.Message = strings.Join([]string{
		"workgraph bridge installed",
		"Client: " + client,
		"Package: " + installRoot,
		"MCP: workgraph",
		"Worker: " + launchStatus,
	}, "\n")
	return result, nil
}

func copyBridgeIntegration(client string, destination string) error {
	prefix := "integrations/" + client
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return fmt.Errorf("create bridge package: %w", err)
	}
	if err := copyEmbeddedBridgeTree(prefix, destination); err != nil {
		return err
	}
	skillDestination := filepath.Join(destination, "plugins", "workgraph-bridge", "skills", "workgraph-bridge")
	return copyEmbeddedBridgeTree(".agents/skills/workgraph-bridge", skillDestination)
}

func copyEmbeddedBridgeTree(prefix string, destination string) error {
	return fs.WalkDir(bridgeIntegrationAssets, prefix, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(prefix, path)
		if err != nil || relative == "." {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		contents, err := bridgeIntegrationAssets.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, contents, 0o600); err != nil {
			return fmt.Errorf("install bridge asset %s: %w", relative, err)
		}
		return nil
	})
}

func writeInstalledBridgeMCP(client string, installRoot string, executable string, homeDir string) error {
	server := map[string]any{"command": executable, "args": []string{"bridge", "mcp", "--home", homeDir}}
	var document any = map[string]any{"workgraph": server}
	if client == "codex" {
		document = map[string]any{"mcpServers": map[string]any{"workgraph": server}}
	}
	contents, _ := json.MarshalIndent(document, "", "  ")
	path := filepath.Join(installRoot, "plugins", "workgraph-bridge", ".mcp.json")
	if err := os.WriteFile(path, append(contents, '\n'), 0o600); err != nil {
		return fmt.Errorf("write installed bridge MCP config: %w", err)
	}
	return nil
}

func registerBridgePlugin(client string, commandName string, installRoot string) error {
	commands := [][]string{}
	if client == "codex" {
		commands = [][]string{{"plugin", "marketplace", "add", installRoot}, {"plugin", "add", "workgraph-bridge@workgraph"}}
	} else {
		commands = [][]string{{"plugin", "marketplace", "add", "--scope", "user", installRoot}, {"plugin", "install", "--scope", "user", "workgraph-bridge@workgraph"}}
	}
	for _, args := range commands {
		output, err := exec.Command(commandName, args...).CombinedOutput()
		if err != nil {
			details := strings.ToLower(strings.TrimSpace(string(output)))
			if !strings.Contains(details, "already") && !strings.Contains(details, "exist") && !strings.Contains(details, "installed") {
				return fmt.Errorf("register %s bridge plugin: %s", client, strings.TrimSpace(string(output)))
			}
		}
	}
	return nil
}

func installBridgeLaunchAgent(client string, clientCommand string, workgraphExecutable string, homeDir string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("automatic bridge workers currently require macOS")
	}
	clientPath, err := exec.LookPath(clientCommand)
	if err != nil {
		return fmt.Errorf("resolve %s executable: %w", client, err)
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve user home: %w", err)
	}
	launchDir := filepath.Join(userHome, "Library", "LaunchAgents")
	logDir := filepath.Join(homeDir, "bridge", "logs")
	if err := os.MkdirAll(launchDir, 0o700); err != nil {
		return fmt.Errorf("create LaunchAgents directory: %w", err)
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return fmt.Errorf("create bridge log directory: %w", err)
	}
	label := "com.workgraph.bridge." + strings.ReplaceAll(client, "-", ".")
	plistPath := filepath.Join(launchDir, label+".plist")
	values := []string{workgraphExecutable, "bridge", "drain", "--home", homeDir, "--client", client, "--client-command", clientPath}
	arguments := ""
	for _, value := range values {
		arguments += "\n      <string>" + html.EscapeString(value) + "</string>"
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array>%s
  </array>
  <key>RunAtLoad</key><true/>
  <key>StartInterval</key><integer>60</integer>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, html.EscapeString(label), arguments,
		html.EscapeString(filepath.Join(logDir, client+".out.log")),
		html.EscapeString(filepath.Join(logDir, client+".err.log")))
	if err := os.WriteFile(plistPath, []byte(plist), 0o600); err != nil {
		return fmt.Errorf("write bridge launch agent: %w", err)
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	output, err := exec.Command("launchctl", "bootstrap", domain, plistPath).CombinedOutput()
	if err != nil && !strings.Contains(strings.ToLower(string(output)), "already") && !strings.Contains(strings.ToLower(string(output)), "exists") {
		return fmt.Errorf("load bridge launch agent: %s", strings.TrimSpace(string(output)))
	}
	_ = exec.Command("launchctl", "enable", domain+"/"+label).Run()
	_ = exec.Command("launchctl", "kickstart", domain+"/"+label).Run()
	marker := filepath.Join(homeDir, "bridge", client+".launch-agent")
	if err := os.WriteFile(marker, []byte(plistPath+"\n"), 0o600); err != nil {
		return fmt.Errorf("record bridge launch agent: %w", err)
	}
	return nil
}

// DrainBridge launches a signed-in reference client only when bridged work is active.
func DrainBridge(homeDir string, client string, clientCommand string) (string, error) {
	homeDir, err := connectorHomeDir(homeDir)
	if err != nil {
		return "", err
	}
	requests, err := ListCaptureRequests(CaptureRequestListConfig{HomeDir: homeDir})
	if err != nil {
		return "", err
	}
	active := false
	for _, request := range requests {
		if request.Status == "pending" || request.Status == "claimed" {
			active = true
			break
		}
	}
	if !active {
		return "No active bridged capture requests.", nil
	}
	client = strings.ToLower(strings.TrimSpace(client))
	commandName := strings.TrimSpace(clientCommand)
	if commandName == "" {
		commandName = client
		if client == "claude-code" {
			commandName = "claude"
		}
	}
	prompt := "Use the workgraph-bridge skill and local workgraph MCP tools to drain all currently available capture requests. Fetch only the bounded approved scopes. Report failures through capture_request_fail."
	var args []string
	switch client {
	case "codex":
		args = []string{"exec", "--ephemeral", "--skip-git-repo-check", prompt}
	case "claude-code":
		args = []string{"-p", prompt}
	default:
		return "", fmt.Errorf("bridge client must be codex or claude-code")
	}
	command := exec.Command(commandName, args...)
	command.Dir = homeDir
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve workgraph executable: %w", err)
	}
	command.Env = append(os.Environ(), "WORKGRAPH_HOME="+homeDir, "WORKGRAPH_EXECUTABLE="+executable)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("drain bridge with %s: %s", client, strings.TrimSpace(string(output)))
	}
	_, _ = RecordBridgeWorkerHeartbeat(homeDir, "", client)
	stamp := filepath.Join(homeDir, "bridge", client+".heartbeat")
	if err := os.MkdirAll(filepath.Dir(stamp), 0o700); err == nil {
		_ = os.WriteFile(stamp, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o600)
	}
	return strings.TrimSpace(string(output)), nil
}

// DoctorBridge verifies a package, client binary, local MCP, and worker marker without provider access.
func DoctorBridge(config BridgeDoctorConfig) (string, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return "", err
	}
	client := strings.ToLower(strings.TrimSpace(config.Client))
	if client != "codex" && client != "claude-code" {
		return "", fmt.Errorf("bridge client must be codex or claude-code")
	}
	commandName := strings.TrimSpace(config.ClientCommand)
	if commandName == "" {
		commandName = client
		if client == "claude-code" {
			commandName = "claude"
		}
	}
	if _, err := exec.LookPath(commandName); err != nil {
		return "", fmt.Errorf("find %s client: %w", client, err)
	}
	installRoot := strings.TrimSpace(config.InstallRoot)
	if installRoot == "" {
		installRoot = filepath.Join(homeDir, "bridge", "integrations", client)
	}
	manifest := filepath.Join(installRoot, "plugins", "workgraph-bridge", ".codex-plugin", "plugin.json")
	if client == "claude-code" {
		manifest = filepath.Join(installRoot, "plugins", "workgraph-bridge", ".claude-plugin", "plugin.json")
	}
	if _, err := os.Stat(manifest); err != nil {
		return "", fmt.Errorf("bridge package is not installed: %w", err)
	}
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{}}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\",\"params\":{}}\n")
	var output bytes.Buffer
	if err := ServeBridgeMCP(BridgeMCPConfig{HomeDir: homeDir, Input: input, Output: &output}); err != nil {
		return "", fmt.Errorf("verify local bridge MCP: %w", err)
	}
	if !strings.Contains(output.String(), "connector_bridge_configure") {
		return "", fmt.Errorf("local bridge MCP did not advertise bridge tools")
	}
	if err := verifyBridgeLifecycleRoundTrip(); err != nil {
		return "", fmt.Errorf("verify bridge lifecycle round trip: %w", err)
	}
	worker := "not installed"
	marker := filepath.Join(homeDir, "bridge", client+".launch-agent")
	if _, err := os.Stat(marker); err == nil {
		worker = "installed"
	}
	heartbeat := "never"
	if contents, err := os.ReadFile(filepath.Join(homeDir, "bridge", client+".heartbeat")); err == nil {
		heartbeat = strings.TrimSpace(string(contents))
	}
	return strings.Join([]string{
		"workgraph bridge doctor",
		"Client: ready",
		"Package: ready",
		"MCP: ready",
		"Round trip: ready",
		"Worker: " + worker,
		"Last heartbeat: " + heartbeat,
	}, "\n"), nil
}

func verifyBridgeLifecycleRoundTrip() error {
	tempDir, err := os.MkdirTemp("", "workgraph-bridge-doctor-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	homeDir := filepath.Join(tempDir, ".workgraph")
	initialized, err := Init(InitConfig{HomeDir: homeDir, MemoryDir: filepath.Join(tempDir, "memory")})
	if err != nil {
		return err
	}
	if _, err := ConnectBridgedConnector(ConnectorModeConfig{HomeDir: homeDir, ID: "slack"}); err != nil {
		return err
	}
	emitted, err := EmitBridgedCaptureRequest(CaptureRequestEmitConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath, ConnectorID: "slack"})
	if err != nil {
		return err
	}
	claimed, err := ClaimCaptureRequests(CaptureRequestClaimConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath, ConnectorID: "slack", Worker: "bridge-doctor", Max: 1})
	if err != nil || len(claimed) != 1 {
		return fmt.Errorf("claim disposable request: claims=%d error=%v", len(claimed), err)
	}
	if _, err := IngestBridgedCapture(BridgedIngestConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath, RequestID: emitted.Request.ID, ClaimToken: claimed[0].ClaimToken, Input: strings.NewReader("[]")}); err != nil {
		return err
	}
	watermark, err := CaptureWatermark(CaptureRequestListConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath, ConnectorID: "slack"})
	if err != nil {
		return err
	}
	if watermark != emitted.Request.Until {
		return fmt.Errorf("cursor %q did not reach request bound %q", watermark, emitted.Request.Until)
	}
	return nil
}
