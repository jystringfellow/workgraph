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

type PluginInstallConfig struct {
	HomeDir        string
	Client         string
	ClientCommand  string
	InstallRoot    string
	SkipLaunchd    bool
	ProviderTools  []string
	Model          string
	ClearModel     bool
	ConfigDir      string
	ClearConfigDir bool
}

type PluginInstallResult struct {
	Client      string
	InstallRoot string
	Version     string
	Model       string
	ConfigDir   string
	Message     string
}

type PluginDoctorConfig struct {
	HomeDir       string
	Client        string
	ClientCommand string
	InstallRoot   string
}

type BridgeInstallConfig = PluginInstallConfig
type BridgeInstallResult = PluginInstallResult
type BridgeDoctorConfig = PluginDoctorConfig

func InstallPlugin(config PluginInstallConfig) (PluginInstallResult, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return PluginInstallResult{}, err
	}
	client := strings.ToLower(strings.TrimSpace(config.Client))
	if client != "codex" && client != "claude-code" {
		return PluginInstallResult{}, fmt.Errorf("plugin client must be codex or claude-code")
	}
	if strings.TrimSpace(config.Model) != "" && config.ClearModel {
		return PluginInstallResult{}, fmt.Errorf("--model and --clear-model cannot be used together")
	}
	if strings.TrimSpace(config.ConfigDir) != "" && config.ClearConfigDir {
		return PluginInstallResult{}, fmt.Errorf("--config-dir and --clear-config-dir cannot be used together")
	}
	providerTools, err := validateClaudeProviderTools(client, config.ProviderTools)
	if err != nil {
		return PluginInstallResult{}, err
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
	workerSettings, err := configureBridgeWorkerSettings(homeDir, client, config.Model, config.ClearModel, config.ConfigDir, config.ClearConfigDir)
	if err != nil {
		return PluginInstallResult{}, err
	}
	if err := registerAgentPlugin(client, commandName, installRoot, signedInClientBinding{ConfigDir: workerSettings.ConfigDir}); err != nil {
		return PluginInstallResult{}, err
	}
	providerToolCount := 0
	if client == "claude-code" {
		providerToolCount, err = installClaudeBridgePermissions(homeDir, providerTools)
		if err != nil {
			return PluginInstallResult{}, err
		}
		if err := installClaudeBridgeTrust(homeDir); err != nil {
			return PluginInstallResult{}, err
		}
	}
	launchStatus := "launchd setup skipped"
	if !config.SkipLaunchd {
		if err := installBridgeLaunchAgent(client, commandName, executable, homeDir); err != nil {
			return PluginInstallResult{}, fmt.Errorf("plugin files and settings were updated but launchd worker reload failed: %w", err)
		}
		launchStatus = "launchd worker installed"
	}
	clientLabel := "Codex"
	if client == "claude-code" {
		clientLabel = "Claude Code"
	}
	result := PluginInstallResult{Client: client, InstallRoot: installRoot, Version: pluginVersion, Model: workerSettings.Model, ConfigDir: workerSettings.ConfigDir}
	lines := []string{
		"workgraph plugin installed",
		"Client: " + client,
		"Package: " + installRoot,
		"Version: " + pluginVersion,
		"Skills: 3",
		"MCP: workgraph",
		"Model: " + bridgeWorkerModelLabel(workerSettings.Model),
		"Config dir: " + signedInClientBindingLabel(signedInClientBinding{ConfigDir: workerSettings.ConfigDir}),
	}
	if client == "claude-code" {
		lines = append(lines, "Permissions: unattended workgraph MCP drain only")
		lines = append(lines, "Workspace trust: ready")
		lines = append(lines, fmt.Sprintf("Provider tools: %d explicitly allowed", providerToolCount))
	}
	lines = append(lines,
		"Worker: "+launchStatus,
		"Daemon: after an executable upgrade, run workgraph stop && workgraph start.",
		"Next: start a new "+clientLabel+" session to load the plugin and MCP server.",
	)
	result.Message = strings.Join(lines, "\n")
	return result, nil
}

var claudeBridgeDrainPermissions = []string{
	"mcp__plugin_workgraph_workgraph__capture_requests_list",
	"mcp__plugin_workgraph_workgraph__capture_requests_claim",
	"mcp__plugin_workgraph_workgraph__capture_request_renew",
	"mcp__plugin_workgraph_workgraph__capture_ingest",
	"mcp__plugin_workgraph_workgraph__capture_request_fail",
	"mcp__plugin_workgraph_workgraph__capture_watermark",
	"mcp__plugin_workgraph_workgraph__connector_status",
	"mcp__plugin_workgraph_workgraph__connector_required_tools",
	"mcp__plugin_workgraph_workgraph__bridge_worker_heartbeat",
}

func validateClaudeProviderTools(client string, tools []string) ([]string, error) {
	if len(tools) > 0 && client != "claude-code" {
		return nil, fmt.Errorf("--allow-provider-tool is supported only for claude-code")
	}
	validated := make([]string, 0, len(tools))
	seen := map[string]bool{}
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if !strings.HasPrefix(tool, "mcp__") || strings.Contains(tool, "*") {
			return nil, fmt.Errorf("provider tool permissions must use an exact MCP tool name")
		}
		if strings.HasPrefix(tool, "mcp__plugin_workgraph_workgraph__") {
			return nil, fmt.Errorf("workgraph MCP permissions are managed by the installer")
		}
		if !seen[tool] {
			validated = append(validated, tool)
			seen[tool] = true
		}
	}
	return validated, nil
}

func installClaudeBridgePermissions(homeDir string, providerTools []string) (int, error) {
	settingsDir := filepath.Join(homeDir, ".claude")
	if err := os.MkdirAll(settingsDir, 0o700); err != nil {
		return 0, fmt.Errorf("create Claude bridge settings directory: %w", err)
	}
	if err := os.Chmod(settingsDir, 0o700); err != nil {
		return 0, fmt.Errorf("secure Claude bridge settings directory: %w", err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	document := map[string]any{}
	if contents, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(contents, &document); err != nil {
			return 0, fmt.Errorf("parse Claude bridge settings: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return 0, fmt.Errorf("read Claude bridge settings: %w", err)
	}
	permissions := map[string]any{}
	if existing, found := document["permissions"]; found {
		var ok bool
		permissions, ok = existing.(map[string]any)
		if !ok {
			return 0, fmt.Errorf("Claude bridge settings permissions must be an object")
		}
	}
	allow := []any{}
	if existing, found := permissions["allow"]; found {
		var ok bool
		allow, ok = existing.([]any)
		if !ok {
			return 0, fmt.Errorf("Claude bridge settings permissions.allow must be an array")
		}
	}
	seen := map[string]bool{}
	for _, raw := range allow {
		value, ok := raw.(string)
		if !ok {
			return 0, fmt.Errorf("Claude bridge settings permissions.allow entries must be strings")
		}
		seen[value] = true
	}
	for _, permission := range claudeBridgeDrainPermissions {
		if !seen[permission] {
			allow = append(allow, permission)
			seen[permission] = true
		}
	}
	for _, permission := range providerTools {
		if !seen[permission] {
			allow = append(allow, permission)
			seen[permission] = true
		}
	}
	permissions["allow"] = allow
	document["permissions"] = permissions
	if err := writeJSONFile(settingsPath, document); err != nil {
		return 0, fmt.Errorf("write Claude bridge settings: %w", err)
	}
	if err := os.Chmod(settingsPath, 0o600); err != nil {
		return 0, fmt.Errorf("secure Claude bridge settings: %w", err)
	}
	return countClaudeProviderPermissions(seen), nil
}

func installClaudeBridgeTrust(homeDir string) error {
	configPath, err := claudeUserConfigPath()
	if err != nil {
		return err
	}
	document := map[string]any{}
	if contents, err := os.ReadFile(configPath); err == nil {
		if err := json.Unmarshal(contents, &document); err != nil {
			return fmt.Errorf("parse Claude user config: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read Claude user config: %w", err)
	}
	projects := map[string]any{}
	if existing, found := document["projects"]; found {
		var ok bool
		projects, ok = existing.(map[string]any)
		if !ok {
			return fmt.Errorf("Claude user config projects must be an object")
		}
	}
	project := map[string]any{}
	if existing, found := projects[homeDir]; found {
		var ok bool
		project, ok = existing.(map[string]any)
		if !ok {
			return fmt.Errorf("Claude user config project %q must be an object", homeDir)
		}
	}
	project["hasTrustDialogAccepted"] = true
	projects[homeDir] = project
	document["projects"] = projects
	if err := writeJSONFile(configPath, document); err != nil {
		return fmt.Errorf("write Claude user config: %w", err)
	}
	return nil
}

func claudeUserConfigPath() (string, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home for Claude workspace trust: %w", err)
	}
	userHome, err = filepath.Abs(userHome)
	if err != nil {
		return "", fmt.Errorf("resolve Claude user config directory: %w", err)
	}
	return filepath.Join(userHome, ".claude.json"), nil
}

func verifyClaudeBridgeTrust(homeDir string) error {
	configPath, err := claudeUserConfigPath()
	if err != nil {
		return err
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var document struct {
		Projects map[string]struct {
			Trusted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(contents, &document); err != nil {
		return err
	}
	if !document.Projects[homeDir].Trusted {
		return fmt.Errorf("workgraph home %q is not trusted", homeDir)
	}
	return nil
}

func countClaudeProviderPermissions(allowed map[string]bool) int {
	workgraph := map[string]bool{}
	for _, permission := range claudeBridgeDrainPermissions {
		workgraph[permission] = true
	}
	count := 0
	for permission := range allowed {
		if strings.HasPrefix(permission, "mcp__") && !workgraph[permission] {
			count++
		}
	}
	return count
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

func registerAgentPlugin(client string, commandName string, installRoot string, binding signedInClientBinding) error {
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
		command := exec.Command(commandName, args...)
		command.Env = applySignedInClientBinding(os.Environ(), client, binding)
		output, err := command.CombinedOutput()
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
	environment := bridgeLaunchEnvironment(userHome, clientPath, workgraphExecutable)
	environmentXML := ""
	for _, name := range []string{"HOME", "PATH"} {
		if value := environment[name]; value != "" {
			environmentXML += "\n    <key>" + name + "</key><string>" + html.EscapeString(value) + "</string>"
		}
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array>%s
  </array>
  <key>EnvironmentVariables</key><dict>%s
  </dict>
  <key>RunAtLoad</key><true/>
  <key>StartInterval</key><integer>60</integer>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, html.EscapeString(label), arguments, environmentXML,
		html.EscapeString(filepath.Join(logDir, client+".out.log")),
		html.EscapeString(filepath.Join(logDir, client+".err.log")))
	if err := os.WriteFile(plistPath, []byte(plist), 0o600); err != nil {
		return fmt.Errorf("write bridge launch agent: %w", err)
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain+"/"+label).Run()
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

func bridgeLaunchEnvironment(userHome string, clientPath string, workgraphExecutable string) map[string]string {
	directories := []string{filepath.Dir(clientPath), filepath.Dir(workgraphExecutable)}
	directories = append(directories, filepath.SplitList(os.Getenv("PATH"))...)
	directories = append(directories, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin")
	seen := map[string]bool{}
	path := make([]string, 0, len(directories))
	for _, directory := range directories {
		directory = strings.TrimSpace(directory)
		if directory == "" || seen[directory] {
			continue
		}
		seen[directory] = true
		path = append(path, directory)
	}
	environment := map[string]string{
		"HOME": userHome,
		"PATH": strings.Join(path, string(os.PathListSeparator)),
	}
	return environment
}

func verifyBridgeLaunchEnvironment(plistPath string, userHome string, clientPath string) error {
	contents, err := os.ReadFile(plistPath)
	if err != nil {
		return err
	}
	text := string(contents)
	for _, expected := range []string{
		"<key>EnvironmentVariables</key>",
		"<key>HOME</key><string>" + html.EscapeString(userHome) + "</string>",
		"<key>PATH</key><string>",
		html.EscapeString(filepath.Dir(clientPath)),
	} {
		if !strings.Contains(text, expected) {
			return fmt.Errorf("launch agent omitted %s", expected)
		}
	}
	return nil
}

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
	if client != "codex" && client != "claude-code" {
		return "", fmt.Errorf("bridge client must be codex or claude-code")
	}
	workerSettings, err := bridgeWorkerSettingsFor(homeDir, client)
	if err != nil {
		return "", err
	}
	commandName := strings.TrimSpace(clientCommand)
	if commandName == "" {
		commandName = client
		if client == "claude-code" {
			commandName = "claude"
		}
	}
	prompt := "Use the workgraph-bridge skill and local workgraph MCP tools. List pending requests before claiming. For each candidate connector, call connector_required_tools and prove that every operation in the fetch and identity requirements returned by the registry is present and authorized with a harmless read-only discovery call. Claim at most one request, filtered to a connector whose complete registry preflight succeeded. Follow the request capture_semantics: fetch only the bounded approved scope for bounded_events, or the exhaustive configured current state for complete_snapshot. If provider capability is absent or denied, leave the request pending and do not report a connector failure. Never fall back to the CLI. Report failures that occur after a successful capability preflight through capture_request_fail."
	var args []string
	switch client {
	case "codex":
		args = []string{"exec", "--ephemeral", "--skip-git-repo-check"}
		if workerSettings.Model != "" {
			args = append(args, "--model", workerSettings.Model)
		}
		args = append(args, prompt)
	case "claude-code":
		settingsPath := filepath.Join(homeDir, ".claude", "settings.json")
		if _, err := os.Stat(settingsPath); err != nil {
			return "", fmt.Errorf("Claude bridge settings are unavailable; rerun workgraph plugin install --client claude-code: %w", err)
		}
		if err := verifyClaudeBridgeTrust(homeDir); err != nil {
			return "", fmt.Errorf("Claude bridge workspace is not trusted; rerun workgraph plugin install --client claude-code: %w", err)
		}
		providerToolCount, err := claudeProviderPermissionCount(homeDir)
		if err != nil {
			return "", fmt.Errorf("inspect Claude provider permissions: %w", err)
		}
		if providerToolCount == 0 {
			recordBridgeDrainHeartbeat(homeDir, client)
			return "No Claude provider tools are explicitly allowed; active capture requests remain pending.", nil
		}
		args = []string{"-p", "--permission-mode", "dontAsk"}
		if workerSettings.Model != "" {
			args = append(args, "--model", workerSettings.Model)
		}
		args = append(args, prompt)
	}
	command := exec.Command(commandName, args...)
	command.Dir = homeDir
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve workgraph executable: %w", err)
	}
	command.Env = applySignedInClientBinding(os.Environ(), client, signedInClientBinding{ConfigDir: workerSettings.ConfigDir})
	command.Env = append(command.Env, "WORKGRAPH_HOME="+homeDir, "WORKGRAPH_EXECUTABLE="+executable)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("drain bridge with %s: %s", client, strings.TrimSpace(string(output)))
	}
	recordBridgeDrainHeartbeat(homeDir, client)
	return strings.TrimSpace(string(output)), nil
}

func recordBridgeDrainHeartbeat(homeDir string, client string) {
	_, _ = RecordBridgeWorkerHeartbeat(homeDir, "", client)
	stamp := filepath.Join(homeDir, "bridge", client+".heartbeat")
	if err := os.MkdirAll(filepath.Dir(stamp), 0o700); err == nil {
		_ = os.WriteFile(stamp, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o600)
	}
}

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
	clientPath, err := exec.LookPath(commandName)
	if err != nil {
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
	permissionStatus := "client-managed"
	if client == "claude-code" {
		if err := verifyClaudeBridgePermissions(homeDir); err != nil {
			return "", fmt.Errorf("verify Claude bridge permissions: %w", err)
		}
		if err := verifyClaudeBridgeTrust(homeDir); err != nil {
			return "", fmt.Errorf("verify Claude bridge workspace trust: %w", err)
		}
		permissionStatus = "ready"
	}
	providerToolCount := 0
	if client == "claude-code" {
		providerToolCount, err = claudeProviderPermissionCount(homeDir)
		if err != nil {
			return "", fmt.Errorf("inspect Claude provider permissions: %w", err)
		}
	}
	workerSettings, err := bridgeWorkerSettingsFor(homeDir, client)
	if err != nil {
		return "", err
	}
	if err := verifySignedInClientBinding(signedInClientBinding{ConfigDir: workerSettings.ConfigDir}); err != nil {
		return "", fmt.Errorf("verify client config directory: %w", err)
	}
	worker := "not installed"
	workerEnvironment := "not installed"
	marker := filepath.Join(homeDir, "bridge", client+".launch-agent")
	if contents, err := os.ReadFile(marker); err == nil {
		worker = "installed"
		plistPath := strings.TrimSpace(string(contents))
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("verify bridge worker environment: %w", err)
		}
		if err := verifyBridgeLaunchEnvironment(plistPath, userHome, clientPath); err != nil {
			return "", fmt.Errorf("verify bridge worker environment: %w; rerun workgraph plugin install --client %s", err, client)
		}
		workerEnvironment = "ready"
	}
	heartbeat := "never"
	if contents, err := os.ReadFile(filepath.Join(homeDir, "bridge", client+".heartbeat")); err == nil {
		heartbeat = strings.TrimSpace(string(contents))
	}
	lines := []string{
		"workgraph plugin doctor",
		"Client: ready",
		"Package: ready",
		"Version: " + pluginVersion,
		"Skills: 3/3 ready",
		"MCP: ready",
		"Permissions: " + permissionStatus,
		"Model: " + bridgeWorkerModelLabel(workerSettings.Model),
		"Config dir: " + signedInClientBindingLabel(signedInClientBinding{ConfigDir: workerSettings.ConfigDir}),
	}
	if client == "claude-code" {
		lines = append(lines, "Workspace trust: ready")
		lines = append(lines, fmt.Sprintf("Provider tools: %d explicitly allowed", providerToolCount))
	}
	lines = append(lines,
		"Round trip: ready",
		"Worker: "+worker,
		"Worker environment: "+workerEnvironment,
		"Last heartbeat: "+heartbeat,
	)
	return strings.Join(lines, "\n"), nil
}

func claudeProviderPermissionCount(homeDir string) (int, error) {
	contents, err := os.ReadFile(filepath.Join(homeDir, ".claude", "settings.json"))
	if err != nil {
		return 0, err
	}
	var document struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(contents, &document); err != nil {
		return 0, err
	}
	allowed := map[string]bool{}
	for _, permission := range document.Permissions.Allow {
		allowed[permission] = true
	}
	return countClaudeProviderPermissions(allowed), nil
}

func verifyClaudeBridgePermissions(homeDir string) error {
	contents, err := os.ReadFile(filepath.Join(homeDir, ".claude", "settings.json"))
	if err != nil {
		return err
	}
	var document struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(contents, &document); err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, permission := range document.Permissions.Allow {
		allowed[permission] = true
	}
	for _, permission := range claudeBridgeDrainPermissions {
		if !allowed[permission] {
			return fmt.Errorf("missing %s", permission)
		}
	}
	return nil
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
	if _, err := ConfigureBridgedConnector(ConnectorBridgeConfig{HomeDir: homeDir, ID: "slack", BridgeParams: json.RawMessage(`{"channels":["C0DOCTOR"],"include_dms":false}`)}); err != nil {
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
	if _, err := IngestBridgedCapture(BridgedIngestConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath, RequestID: emitted.Request.ID, ClaimToken: claimed[0].ClaimToken, Input: strings.NewReader(`{"events":[],"empty_proof":{"exhaustive_query":true}}`)}); err != nil {
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
