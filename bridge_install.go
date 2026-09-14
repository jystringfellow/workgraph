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

//go:embed all:integrations/codex all:integrations/claude-code all:.agents/skills/workgraph-bridge all:.agents/skills/workgraph-memory all:.agents/skills/workgraph-ai-checkpoint
var agentPluginAssets embed.FS

// PluginInstallConfig controls one client plugin installation.
type PluginInstallConfig struct {
	HomeDir       string
	Client        string
	ClientCommand string
	InstallRoot   string
	SkipLaunchd   bool
}

// PluginInstallResult describes an installed client plugin package.
type PluginInstallResult struct {
	Client      string
	InstallRoot string
	Version     string
	Message     string
}

// PluginDoctorConfig controls provider-free client plugin diagnostics.
type PluginDoctorConfig struct {
	HomeDir       string
	Client        string
	ClientCommand string
	InstallRoot   string
}

// Compatibility aliases retain the public bridge install API.
type BridgeInstallConfig = PluginInstallConfig
type BridgeInstallResult = PluginInstallResult
type BridgeDoctorConfig = PluginDoctorConfig

// InstallPlugin installs and registers one reference client package idempotently.
func InstallPlugin(config PluginInstallConfig) (PluginInstallResult, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return PluginInstallResult{}, err
	}
	client := strings.ToLower(strings.TrimSpace(config.Client))
	if client != "codex" && client != "claude-code" {
		return PluginInstallResult{}, fmt.Errorf("plugin client must be codex or claude-code")
	}
	commandName := strings.TrimSpace(config.ClientCommand)
	if commandName == "" {
		commandName = client
		if client == "claude-code" {
			commandName = "claude"
		}
	}
	if _, err := exec.LookPath(commandName); err != nil {
		return PluginInstallResult{}, fmt.Errorf("find %s client: %w", client, err)
	}
	installRoot := strings.TrimSpace(config.InstallRoot)
	if installRoot == "" {
		installRoot = filepath.Join(homeDir, "bridge", "integrations", client)
	}
	installRoot, err = filepath.Abs(installRoot)
	if err != nil {
		return PluginInstallResult{}, fmt.Errorf("resolve plugin install root: %w", err)
	}
	if err := copyAgentPlugin(client, installRoot); err != nil {
		return PluginInstallResult{}, err
	}
	executable, err := os.Executable()
	if err != nil {
		return PluginInstallResult{}, fmt.Errorf("resolve workgraph executable: %w", err)
	}
	if err := writeInstalledPluginMCP(client, installRoot, executable, homeDir); err != nil {
		return PluginInstallResult{}, err
	}
	pluginVersion, err := stampInstalledPluginVersion(client, installRoot, time.Now().UTC())
	if err != nil {
		return PluginInstallResult{}, err
	}
	if err := registerAgentPlugin(client, commandName, installRoot); err != nil {
		return PluginInstallResult{}, err
	}
	launchStatus := "launchd setup skipped"
	if !config.SkipLaunchd {
		if err := installBridgeLaunchAgent(client, commandName, executable, homeDir); err != nil {
			return PluginInstallResult{}, err
		}
		launchStatus = "launchd worker installed"
	}
	clientLabel := "Codex"
	if client == "claude-code" {
		clientLabel = "Claude Code"
	}
	result := PluginInstallResult{Client: client, InstallRoot: installRoot, Version: pluginVersion}
	result.Message = strings.Join([]string{
		"workgraph plugin installed",
		"Client: " + client,
		"Package: " + installRoot,
		"Version: " + pluginVersion,
		"Skills: 3",
		"MCP: workgraph",
		"Worker: " + launchStatus,
		"Next: start a new " + clientLabel + " session to load the plugin.",
	}, "\n")
	return result, nil
}

func stampInstalledPluginVersion(client string, installRoot string, installedAt time.Time) (string, error) {
	versionOwner := "codex"
	manifest := filepath.Join(installRoot, "plugins", "workgraph", ".codex-plugin", "plugin.json")
	if client == "claude-code" {
		versionOwner = "claude"
		manifest = filepath.Join(installRoot, "plugins", "workgraph", ".claude-plugin", "plugin.json")
	}
	version := "0.2.0+" + versionOwner + ".local-" + installedAt.UTC().Format("20060102-150405.000000000")
	if err := setJSONVersion(manifest, version); err != nil {
		return "", fmt.Errorf("stamp plugin manifest: %w", err)
	}
	if client == "claude-code" {
		marketplace := filepath.Join(installRoot, ".claude-plugin", "marketplace.json")
		if err := setClaudeMarketplacePluginVersion(marketplace, version); err != nil {
			return "", fmt.Errorf("stamp Claude Code marketplace: %w", err)
		}
	}
	return version, nil
}

func setJSONVersion(path string, version string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document map[string]any
	if err := json.Unmarshal(contents, &document); err != nil {
		return err
	}
	document["version"] = version
	return writeJSONFile(path, document)
}

func setClaudeMarketplacePluginVersion(path string, version string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document map[string]any
	if err := json.Unmarshal(contents, &document); err != nil {
		return err
	}
	plugins, ok := document["plugins"].([]any)
	if !ok {
		return fmt.Errorf("plugins list is missing")
	}
	found := false
	for _, value := range plugins {
		plugin, ok := value.(map[string]any)
		if !ok || plugin["name"] != "workgraph" {
			continue
		}
		plugin["version"] = version
		found = true
	}
	if !found {
		return fmt.Errorf("workgraph plugin entry is missing")
	}
	return writeJSONFile(path, document)
}

func writeJSONFile(path string, document any) error {
	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(contents, '\n'), 0o600); err != nil {
		return err
	}
	return nil
}

// InstallBridge is a compatibility wrapper around InstallPlugin.
func InstallBridge(config BridgeInstallConfig) (BridgeInstallResult, error) {
	return InstallPlugin(config)
}

func copyAgentPlugin(client string, destination string) error {
	prefix := "integrations/" + client
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return fmt.Errorf("create plugin package: %w", err)
	}
	if err := copyEmbeddedPluginTree(prefix, destination); err != nil {
		return err
	}
	for _, skill := range []string{"workgraph-bridge", "workgraph-memory", "workgraph-ai-checkpoint"} {
		skillDestination := filepath.Join(destination, "plugins", "workgraph", "skills", skill)
		if err := copyEmbeddedPluginTree(".agents/skills/"+skill, skillDestination); err != nil {
			return err
		}
	}
	return nil
}

func copyEmbeddedPluginTree(prefix string, destination string) error {
	return fs.WalkDir(agentPluginAssets, prefix, func(path string, entry fs.DirEntry, walkErr error) error {
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
		contents, err := agentPluginAssets.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, contents, 0o600); err != nil {
			return fmt.Errorf("install bridge asset %s: %w", relative, err)
		}
		return nil
	})
}

func writeInstalledPluginMCP(client string, installRoot string, executable string, homeDir string) error {
	server := map[string]any{"command": executable, "args": []string{"bridge", "mcp", "--home", homeDir}}
	var document any = map[string]any{"workgraph": server}
	if client == "codex" {
		document = map[string]any{"mcpServers": map[string]any{"workgraph": server}}
	}
	contents, _ := json.MarshalIndent(document, "", "  ")
	path := filepath.Join(installRoot, "plugins", "workgraph", ".mcp.json")
	if err := os.WriteFile(path, append(contents, '\n'), 0o600); err != nil {
		return fmt.Errorf("write installed bridge MCP config: %w", err)
	}
	return nil
}

func registerAgentPlugin(client string, commandName string, installRoot string) error {
	commands := [][]string{}
	if client == "codex" {
		commands = [][]string{{"plugin", "marketplace", "add", installRoot}, {"plugin", "add", "workgraph@workgraph"}}
	} else {
		commands = [][]string{
			{"plugin", "marketplace", "add", "--scope", "user", installRoot},
			{"plugin", "install", "--scope", "user", "workgraph@workgraph"},
			{"plugin", "update", "--scope", "user", "workgraph@workgraph"},
		}
	}
	for _, args := range commands {
		output, err := exec.Command(commandName, args...).CombinedOutput()
		if err != nil {
			details := strings.ToLower(strings.TrimSpace(string(output)))
			if !strings.Contains(details, "already") && !strings.Contains(details, "exist") {
				return fmt.Errorf("register %s workgraph plugin: %s", client, strings.TrimSpace(string(output)))
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

// DoctorPlugin verifies a package, skills, client binary, local MCP, and worker marker without provider access.
func DoctorPlugin(config PluginDoctorConfig) (string, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return "", err
	}
	client := strings.ToLower(strings.TrimSpace(config.Client))
	if client != "codex" && client != "claude-code" {
		return "", fmt.Errorf("plugin client must be codex or claude-code")
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
	pluginRoot := filepath.Join(installRoot, "plugins", "workgraph")
	manifest := filepath.Join(pluginRoot, ".codex-plugin", "plugin.json")
	if client == "claude-code" {
		manifest = filepath.Join(pluginRoot, ".claude-plugin", "plugin.json")
	}
	if _, err := os.Stat(manifest); err != nil {
		return "", fmt.Errorf("workgraph plugin is not installed: %w", err)
	}
	pluginVersion, err := readPluginVersion(manifest)
	if err != nil {
		return "", fmt.Errorf("read workgraph plugin version: %w", err)
	}
	for _, required := range []string{
		filepath.Join("skills", "workgraph-bridge", "SKILL.md"),
		filepath.Join("skills", "workgraph-bridge", "references", "event-contracts.md"),
		filepath.Join("skills", "workgraph-memory", "SKILL.md"),
		filepath.Join("skills", "workgraph-ai-checkpoint", "SKILL.md"),
	} {
		if _, err := os.Stat(filepath.Join(pluginRoot, required)); err != nil {
			return "", fmt.Errorf("required plugin capability %s is not installed: %w", required, err)
		}
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
		"workgraph plugin doctor",
		"Client: ready",
		"Package: ready",
		"Version: " + pluginVersion,
		"Skills: 3/3 ready",
		"MCP: ready",
		"Round trip: ready",
		"Worker: " + worker,
		"Last heartbeat: " + heartbeat,
	}, "\n"), nil
}

func readPluginVersion(manifest string) (string, error) {
	contents, err := os.ReadFile(manifest)
	if err != nil {
		return "", err
	}
	var document struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(contents, &document); err != nil {
		return "", err
	}
	if document.Name != "workgraph" {
		return "", fmt.Errorf("manifest names %q instead of workgraph", document.Name)
	}
	if strings.TrimSpace(document.Version) == "" {
		return "", fmt.Errorf("manifest version is empty")
	}
	return document.Version, nil
}

// DoctorBridge is a compatibility wrapper around DoctorPlugin.
func DoctorBridge(config BridgeDoctorConfig) (string, error) {
	return DoctorPlugin(config)
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
