package facts

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestClaudePluginInstallMergesLeastPrivilegeDrainPermissions(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), ".claude-enterprise"))
	claudeConfigPath := filepath.Join(filepath.Dir(homeDir), ".claude.json")
	if err := os.WriteFile(claudeConfigPath, []byte(`{"theme":"dark","projects":{"/keep":{"hasTrustDialogAccepted":false}}}`), 0o600); err != nil {
		t.Fatalf("write existing Claude user config: %v", err)
	}
	settingsDir := filepath.Join(homeDir, ".claude")
	if err := os.MkdirAll(settingsDir, 0o700); err != nil {
		t.Fatalf("create Claude settings directory: %v", err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"theme":"dark","permissions":{"allow":["Read(./notes/**)"]}}`), 0o600); err != nil {
		t.Fatalf("write existing Claude settings: %v", err)
	}
	fixtureDir := t.TempDir()
	clientPath := filepath.Join(fixtureDir, "claude")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake Claude: %v", err)
	}
	if output, err := runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", filepath.Join(fixtureDir, "installed"), "--no-launchd"); err != nil {
		t.Fatalf("install Claude plugin: %v\n%s", err, output)
	}

	contents, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read merged Claude settings: %v", err)
	}
	var settings struct {
		Theme       string `json:"theme"`
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(contents, &settings); err != nil {
		t.Fatalf("parse merged Claude settings: %v", err)
	}
	if settings.Theme != "dark" {
		t.Fatalf("plugin install replaced unrelated settings: %s", contents)
	}
	allowed := strings.Join(settings.Permissions.Allow, "\n")
	for _, expected := range []string{
		"Read(./notes/**)",
		"mcp__plugin_workgraph_workgraph__capture_requests_list",
		"mcp__plugin_workgraph_workgraph__capture_requests_claim",
		"mcp__plugin_workgraph_workgraph__capture_request_renew",
		"mcp__plugin_workgraph_workgraph__capture_ingest",
		"mcp__plugin_workgraph_workgraph__capture_request_fail",
		"mcp__plugin_workgraph_workgraph__capture_watermark",
		"mcp__plugin_workgraph_workgraph__connector_status",
		"mcp__plugin_workgraph_workgraph__connector_required_tools",
		"mcp__plugin_workgraph_workgraph__bridge_worker_heartbeat",
	} {
		if !strings.Contains(allowed, expected) {
			t.Fatalf("Claude permissions omitted %q:\n%s", expected, contents)
		}
	}
	for _, forbidden := range []string{"mcp__plugin_workgraph_workgraph__*", "connector_bridge_configure", "connector_bridge_disconnect", "Bash"} {
		if strings.Contains(allowed, forbidden) {
			t.Fatalf("Claude permissions were too broad (%q):\n%s", forbidden, contents)
		}
	}

	configContents, err := os.ReadFile(claudeConfigPath)
	if err != nil {
		t.Fatalf("read Claude user config: %v", err)
	}
	var userConfig struct {
		Theme    string `json:"theme"`
		Projects map[string]struct {
			Trusted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(configContents, &userConfig); err != nil {
		t.Fatalf("parse Claude user config: %v", err)
	}
	if userConfig.Theme != "dark" || userConfig.Projects["/keep"].Trusted {
		t.Fatalf("plugin install replaced unrelated Claude user config: %s", configContents)
	}
	if !userConfig.Projects[homeDir].Trusted {
		t.Fatalf("plugin install did not trust workgraph home %q: %s", homeDir, configContents)
	}
}

func TestClaudePluginInstallAddsOnlyExplicitProviderTools(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	clientPath := filepath.Join(fixtureDir, "claude")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake Claude: %v", err)
	}
	tools := []string{
		"mcp__claude_ai_Slack__slack_read_channel",
		"mcp__azure-devops__wit_query",
	}
	args := []string{"plugin", "install", "--home", homeDir, "--client", "claude-code",
		"--client-command", clientPath, "--install-root", filepath.Join(fixtureDir, "installed"), "--no-launchd"}
	for _, tool := range tools {
		args = append(args, "--allow-provider-tool", tool)
	}
	output, err := runworkgraph(t, repoRoot(t), args...)
	if err != nil {
		t.Fatalf("install Claude plugin with provider tools: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Provider tools: 2 explicitly allowed") {
		t.Fatalf("install did not report provider tool count:\n%s", output)
	}
	contents, err := os.ReadFile(filepath.Join(homeDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read worker settings: %v", err)
	}
	for _, tool := range tools {
		if !strings.Contains(string(contents), tool) {
			t.Fatalf("worker settings omitted %q:\n%s", tool, contents)
		}
	}
	output, err = runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir, "--client", "claude-code",
		"--client-command", clientPath, "--install-root", filepath.Join(fixtureDir, "installed"), "--no-launchd")
	if err != nil || !strings.Contains(string(output), "Provider tools: 2 explicitly allowed") {
		t.Fatalf("reinstall did not preserve provider permissions: %v\n%s", err, output)
	}

	output, err = runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir, "--client", "claude-code",
		"--client-command", clientPath, "--install-root", filepath.Join(fixtureDir, "wildcard"), "--no-launchd",
		"--allow-provider-tool", "mcp__claude_ai_Slack__*")
	if err == nil || !strings.Contains(string(output), "exact") {
		t.Fatalf("expected wildcard provider permission rejection, got err=%v:\n%s", err, output)
	}
}

func TestBridgeWorkerModelPersistsAcrossReinstallAndCanBeCleared(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	logPath := filepath.Join(fixtureDir, "codex.log")
	clientPath := filepath.Join(fixtureDir, "codex")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+logPath+"\"\n"), 0o700); err != nil {
		t.Fatalf("write fake Codex: %v", err)
	}
	installRoot := filepath.Join(fixtureDir, "installed")
	model := "gpt-bridge-pinned"
	install := func(extra ...string) []byte {
		t.Helper()
		args := []string{"plugin", "install", "--home", homeDir, "--client", "codex", "--client-command", clientPath, "--install-root", installRoot, "--no-launchd"}
		args = append(args, extra...)
		output, err := runworkgraph(t, repoRoot(t), args...)
		if err != nil {
			t.Fatalf("install Codex plugin: %v\n%s", err, output)
		}
		return output
	}

	if output := install("--model", model); !strings.Contains(string(output), "Model: "+model) {
		t.Fatalf("install did not report pinned model:\n%s", output)
	}
	configPath := filepath.Join(homeDir, "bridge", "workers.json")
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read worker model config: %v", err)
	}
	if !strings.Contains(string(contents), `"codex"`) || !strings.Contains(string(contents), `"model": "`+model+`"`) {
		t.Fatalf("worker model config omitted selection:\n%s", contents)
	}
	if info, err := os.Stat(configPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("expected private worker config, info=%v error=%v", info, err)
	}

	if output := install(); !strings.Contains(string(output), "Model: "+model) {
		t.Fatalf("reinstall did not preserve pinned model:\n%s", output)
	}
	doctor, err := runworkgraph(t, repoRoot(t), "plugin", "doctor", "--home", homeDir,
		"--client", "codex", "--client-command", clientPath, "--install-root", installRoot)
	if err != nil || !strings.Contains(string(doctor), "Model: "+model) {
		t.Fatalf("doctor did not report pinned model: %v\n%s", err, doctor)
	}

	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit request: %v\n%s", err, output)
	}
	_ = os.Remove(logPath)
	if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "codex", "--client-command", clientPath); err != nil {
		t.Fatalf("drain with pinned model: %v\n%s", err, output)
	}
	drainArgs, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read Codex drain arguments: %v", err)
	}
	if !strings.Contains(string(drainArgs), "--model "+model) {
		t.Fatalf("drain omitted pinned model:\n%s", drainArgs)
	}

	if output := install("--clear-model"); !strings.Contains(string(output), "Model: client default") {
		t.Fatalf("clear did not report client default:\n%s", output)
	}
	_ = os.Remove(logPath)
	if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "codex", "--client-command", clientPath); err != nil {
		t.Fatalf("drain with default model: %v\n%s", err, output)
	}
	drainArgs, err = os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read default Codex drain arguments: %v", err)
	}
	if strings.Contains(string(drainArgs), "--model") {
		t.Fatalf("cleared model still reached drain:\n%s", drainArgs)
	}

	conflict, err := runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir,
		"--client", "codex", "--client-command", clientPath, "--install-root", installRoot,
		"--no-launchd", "--model", model, "--clear-model")
	if err == nil || !strings.Contains(string(conflict), "cannot be used together") {
		t.Fatalf("expected conflicting model flags to fail, got err=%v:\n%s", err, conflict)
	}
}

func TestSlackListsBridgeContractAllowsContentHashWithoutClaimingDeletion(t *testing.T) {
	root := repoRoot(t)
	skillPaths := []string{
		filepath.Join(root, ".agents", "skills", "workgraph-bridge", "SKILL.md"),
		filepath.Join(root, "integrations", "claude-code", "plugins", "workgraph", "skills", "workgraph-bridge", "SKILL.md"),
		filepath.Join(root, "integrations", "codex", "plugins", "workgraph", "skills", "workgraph-bridge", "SKILL.md"),
	}
	var canonicalSkill string
	for _, path := range skillPaths {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read bridge skill %s: %v", path, err)
		}
		if canonicalSkill == "" {
			canonicalSkill = string(contents)
		} else if string(contents) != canonicalSkill {
			t.Fatalf("packaged bridge skill drifted from canonical copy: %s", path)
		}
		for _, expected := range []string{
			"complete_snapshot", "slack_read_file", `response_format: "detailed"`,
			"resource read needed", "routing context", "connector_required_tools",
		} {
			if !strings.Contains(string(contents), expected) {
				t.Fatalf("bridge skill %s omitted %q", path, expected)
			}
		}
	}
	paths := []string{
		filepath.Join(root, ".agents", "skills", "workgraph-bridge", "references", "event-contracts.md"),
		filepath.Join(root, "integrations", "claude-code", "plugins", "workgraph", "skills", "workgraph-bridge", "references", "event-contracts.md"),
		filepath.Join(root, "integrations", "codex", "plugins", "workgraph", "skills", "workgraph-bridge", "references", "event-contracts.md"),
	}
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read Slack Lists bridge contract %s: %v", path, err)
		}
		for _, expected := range []string{"<revision-or-content-hash>", "canonical JSON", "A missing row is not a deletion", "complete_snapshot", "slack_read_file"} {
			if !strings.Contains(string(contents), expected) {
				t.Fatalf("Slack Lists bridge contract %s omitted %q", path, expected)
			}
		}
	}
}

func TestBridgeInstallPackagesCodexAndClaudeCodeIdempotently(t *testing.T) {
	for _, client := range []string{"codex", "claude-code"} {
		t.Run(client, func(t *testing.T) {
			homeDir := initBridgedCaptureHome(t)
			fixtureDir := t.TempDir()
			logPath := filepath.Join(fixtureDir, "client.log")
			clientPath := filepath.Join(fixtureDir, "client")
			script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + logPath + "\"\n"
			if err := os.WriteFile(clientPath, []byte(script), 0o700); err != nil {
				t.Fatalf("write fake client: %v", err)
			}
			installRoot := filepath.Join(fixtureDir, "installed")
			args := []string{"bridge", "install", "--home", homeDir, "--client", client,
				"--client-command", clientPath, "--install-root", installRoot, "--no-launchd"}
			for attempt := 0; attempt < 2; attempt++ {
				if output, err := runworkgraph(t, repoRoot(t), args...); err != nil {
					t.Fatalf("install %s attempt %d: %v\n%s", client, attempt+1, err, output)
				}
			}
			manifest := ".codex-plugin/plugin.json"
			if client == "claude-code" {
				manifest = ".claude-plugin/plugin.json"
			}
			if _, err := os.Stat(filepath.Join(installRoot, "plugins", "workgraph", manifest)); err != nil {
				t.Fatalf("installed %s manifest: %v", client, err)
			}
			for _, skill := range []string{"workgraph-bridge", "workgraph-memory", "workgraph-ai-checkpoint"} {
				if _, err := os.Stat(filepath.Join(installRoot, "plugins", "workgraph", "skills", skill, "SKILL.md")); err != nil {
					t.Fatalf("installed %s skill %s: %v", client, skill, err)
				}
			}
			logContents, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("read client registration log: %v", err)
			}
			logText := string(logContents)
			for _, expected := range []string{"plugin marketplace add", "workgraph@workgraph"} {
				if !strings.Contains(logText, expected) {
					t.Fatalf("%s registration omitted %q:\n%s", client, expected, logText)
				}
			}
		})
	}
}

func TestBridgeDrainLaunchesClientOnlyForActiveDaemonWork(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	logPath := filepath.Join(fixtureDir, "drain.log")
	clientPath := filepath.Join(fixtureDir, "codex")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+logPath+"\"\n"), 0o700); err != nil {
		t.Fatalf("write fake Codex: %v", err)
	}
	output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "codex", "--client-command", clientPath)
	if err != nil || !strings.Contains(string(output), "No active") {
		t.Fatalf("idle bridge drain: %v\n%s", err, output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("idle drain launched Codex: %v", err)
	}
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit request: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "codex", "--client-command", clientPath); err != nil {
		t.Fatalf("active bridge drain: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read drain invocation: %v", err)
	}
	for _, expected := range []string{"exec", "--ephemeral", "workgraph-bridge", "List pending requests before claiming"} {
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("drain invocation omitted %q:\n%s", expected, contents)
		}
	}
}

func TestClaudeBridgeDrainUsesNonInteractivePermissionMode(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	logPath := filepath.Join(fixtureDir, "drain.log")
	clientPath := filepath.Join(fixtureDir, "claude")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \""+logPath+"\"\n"), 0o700); err != nil {
		t.Fatalf("write fake Claude: %v", err)
	}
	if output, err := runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", filepath.Join(fixtureDir, "installed"),
		"--no-launchd", "--model", "claude-bridge-pinned",
		"--allow-provider-tool", "mcp__claude_ai_Slack__slack_list_user_channels"); err != nil {
		t.Fatalf("install Claude bridge permissions: %v\n%s", err, output)
	}
	_ = os.Remove(logPath)
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit request: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "claude-code", "--client-command", clientPath); err != nil {
		t.Fatalf("drain with Claude: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read Claude invocation: %v", err)
	}
	for _, expected := range []string{
		"-p", "--permission-mode dontAsk",
		"--model claude-bridge-pinned",
		"List pending requests before claiming", "connector_required_tools",
		"fetch and identity requirements returned by the registry",
		"leave the request pending", "Never fall back to the CLI",
	} {
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("Claude drain invocation omitted %q:\n%s", expected, contents)
		}
	}
	if strings.Contains(string(contents), "--settings") {
		t.Fatalf("Claude drain invocation overrode settings and hid provider MCP servers:\n%s", contents)
	}
}

func TestClaudeBridgeDrainLeavesRequestsPendingWithoutProviderPermissions(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	logPath := filepath.Join(fixtureDir, "claude.log")
	clientPath := filepath.Join(fixtureDir, "claude")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+logPath+"\"\n"), 0o700); err != nil {
		t.Fatalf("write fake Claude: %v", err)
	}
	if output, err := runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", filepath.Join(fixtureDir, "installed"), "--no-launchd"); err != nil {
		t.Fatalf("install Claude plugin: %v\n%s", err, output)
	}
	_ = os.Remove(logPath)
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit request: %v\n%s", err, output)
	}
	output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "claude-code", "--client-command", clientPath)
	if err != nil {
		t.Fatalf("skip incapable Claude drain: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "remain pending") {
		t.Fatalf("expected pending capability warning, got:\n%s", output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("incapable drain launched Claude: %v", err)
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	var status string
	if err := db.QueryRow(`SELECT status FROM capture_requests WHERE connector_id = 'slack'`).Scan(&status); err != nil {
		t.Fatalf("read pending request: %v", err)
	}
	if status != "pending" {
		t.Fatalf("expected request to remain pending, got %q", status)
	}
}

func TestClaudeBridgeDrainFailsLoudlyWhenWorkspaceTrustIsMissing(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	logPath := filepath.Join(fixtureDir, "claude.log")
	clientPath := filepath.Join(fixtureDir, "claude")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+logPath+"\"\n"), 0o700); err != nil {
		t.Fatalf("write fake Claude: %v", err)
	}
	if output, err := runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", filepath.Join(fixtureDir, "installed"),
		"--no-launchd", "--allow-provider-tool", "mcp__claude_ai_Slack__slack_list_user_channels"); err != nil {
		t.Fatalf("install Claude plugin: %v\n%s", err, output)
	}
	_ = os.Remove(logPath)
	if err := os.WriteFile(filepath.Join(filepath.Dir(homeDir), ".claude.json"), []byte(`{"projects":{}}`), 0o600); err != nil {
		t.Fatalf("remove Claude workspace trust: %v", err)
	}
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit request: %v\n%s", err, output)
	}
	output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "claude-code", "--client-command", clientPath)
	if err == nil || !strings.Contains(string(output), "workspace is not trusted") || !strings.Contains(string(output), "rerun workgraph plugin install") {
		t.Fatalf("expected explicit workspace trust error, got err=%v:\n%s", err, output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("untrusted drain launched Claude: %v", err)
	}
}

func TestBridgeInstallReloadsExistingLaunchAgentBeforeBootstrap(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd reload is macOS-specific")
	}
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	userHome := filepath.Join(fixtureDir, "user")
	if err := os.MkdirAll(userHome, 0o700); err != nil {
		t.Fatalf("create fake user home: %v", err)
	}
	t.Setenv("HOME", userHome)
	claudeConfigDir := filepath.Join(userHome, ".claude-enterprise")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfigDir)
	t.Setenv("ACCESS_TOKEN", "must-not-enter-launch-agent")
	logPath := filepath.Join(fixtureDir, "launchctl.log")
	binDir := filepath.Join(fixtureDir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatalf("create fake bin: %v", err)
	}
	launchctlPath := filepath.Join(binDir, "launchctl")
	if err := os.WriteFile(launchctlPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+logPath+"\"\n"), 0o700); err != nil {
		t.Fatalf("write fake launchctl: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	clientPath := filepath.Join(fixtureDir, "claude")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake Claude: %v", err)
	}
	installRoot := filepath.Join(fixtureDir, "installed")
	if output, err := runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", installRoot,
		"--model", "launchd-pinned"); err != nil {
		t.Fatalf("install with launchd reload: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read launchctl log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(contents)), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "bootout gui/") || !strings.HasPrefix(lines[1], "bootstrap gui/") {
		t.Fatalf("expected bootout before bootstrap, got:\n%s", contents)
	}
	plistPath := filepath.Join(userHome, "Library", "LaunchAgents", "com.workgraph.bridge.claude.code.plist")
	plist, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("read bridge launch agent: %v", err)
	}
	for _, expected := range []string{
		"<key>EnvironmentVariables</key>",
		"<key>HOME</key><string>" + userHome + "</string>",
		"<key>PATH</key><string>",
		filepath.Dir(clientPath),
		"<key>CLAUDE_CONFIG_DIR</key><string>" + claudeConfigDir + "</string>",
	} {
		if !strings.Contains(string(plist), expected) {
			t.Fatalf("launch agent omitted %q:\n%s", expected, plist)
		}
	}
	for _, forbidden := range []string{"ACCESS_TOKEN", "must-not-enter-launch-agent", "launchd-pinned", "--model"} {
		if strings.Contains(string(plist), forbidden) {
			t.Fatalf("launch agent copied generated configuration %q:\n%s", forbidden, plist)
		}
	}
	doctor, err := runworkgraph(t, repoRoot(t), "plugin", "doctor", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", installRoot)
	if err != nil {
		t.Fatalf("doctor installed launch agent: %v\n%s", err, doctor)
	}
	if !strings.Contains(string(doctor), "Worker environment: ready") || !strings.Contains(string(doctor), "Model: launchd-pinned") {
		t.Fatalf("doctor did not verify launch environment:\n%s", doctor)
	}
}

func TestBridgeInstallReportsPartialStateWhenLaunchAgentReloadFails(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd reload is macOS-specific")
	}
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	userHome := filepath.Join(fixtureDir, "user")
	binDir := filepath.Join(fixtureDir, "bin")
	if err := os.MkdirAll(userHome, 0o700); err != nil {
		t.Fatalf("create fake user home: %v", err)
	}
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatalf("create fake bin: %v", err)
	}
	t.Setenv("HOME", userHome)
	launchctlPath := filepath.Join(binDir, "launchctl")
	if err := os.WriteFile(launchctlPath, []byte("#!/bin/sh\nif [ \"$1\" = bootstrap ]; then echo reload-failed >&2; exit 1; fi\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write failing launchctl: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	clientPath := filepath.Join(fixtureDir, "claude")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake Claude: %v", err)
	}
	output, err := runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", filepath.Join(fixtureDir, "installed"))
	if err == nil || !strings.Contains(string(output), "plugin files and settings were updated") {
		t.Fatalf("expected explicit partial-install error, got err=%v:\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(homeDir, ".claude", "settings.json")); err != nil {
		t.Fatalf("expected settings to remain after partial install: %v", err)
	}
}

func TestBridgeDoctorVerifiesInstalledPackageAndLocalMCPWithoutProvider(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	clientPath := filepath.Join(fixtureDir, "claude")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake Claude: %v", err)
	}
	installRoot := filepath.Join(fixtureDir, "installed")
	if output, err := runworkgraph(t, repoRoot(t), "bridge", "install", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", installRoot, "--no-launchd"); err != nil {
		t.Fatalf("install bridge: %v\n%s", err, output)
	}
	output, err := runworkgraph(t, repoRoot(t), "bridge", "doctor", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", installRoot)
	if err != nil {
		t.Fatalf("doctor bridge: %v\n%s", err, output)
	}
	for _, expected := range []string{"Package: ready", "MCP: ready", "Permissions: ready", "Round trip: ready", "Client: ready", "Worker: not installed"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("bridge doctor omitted %q:\n%s", expected, output)
		}
	}
}

func TestReferenceWorkersDrainFakeProviderRequestEndToEnd(t *testing.T) {
	for _, client := range []string{"codex", "claude-code"} {
		t.Run(client, func(t *testing.T) {
			homeDir := initBridgedCaptureHome(t)
			if client == "claude-code" {
				settingsDir := filepath.Join(homeDir, ".claude")
				if err := os.MkdirAll(settingsDir, 0o700); err != nil {
					t.Fatalf("create Claude worker settings: %v", err)
				}
				if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(`{"permissions":{"allow":["mcp__fake_provider__read"]}}`), 0o600); err != nil {
					t.Fatalf("write Claude worker settings: %v", err)
				}
				trust, err := json.Marshal(map[string]any{"projects": map[string]any{homeDir: map[string]any{"hasTrustDialogAccepted": true}}})
				if err != nil {
					t.Fatalf("encode Claude workspace trust: %v", err)
				}
				if err := os.WriteFile(filepath.Join(filepath.Dir(homeDir), ".claude.json"), trust, 0o600); err != nil {
					t.Fatalf("write Claude workspace trust: %v", err)
				}
			}
			connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
			if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
				t.Fatalf("emit request: %v\n%s", err, output)
			}
			fixtureDir := t.TempDir()
			clientPath := filepath.Join(fixtureDir, "client")
			script := `#!/bin/sh
claim_file="${TMPDIR:-/tmp}/workgraph-reference-worker-$$.json"
claim_output="$($WORKGRAPH_EXECUTABLE capture requests --claim --home "$WORKGRAPH_HOME" --connector slack --max 1 --worker reference-fact --claim-file "$claim_file")" || exit 1
request_id="$(printf '%s\n' "$claim_output" | sed -n 's/^Request: //p')"
printf '%s\n' '{"type":"slack.message","timestamp":"2026-09-13T12:00:00Z","external_id":"C0DEMO123:reference-worker","summary":"Reference worker event","payload":{"channel":"C0DEMO123","text":"fake approved provider result"}}' | "$WORKGRAPH_EXECUTABLE" capture ingest --home "$WORKGRAPH_HOME" --request "$request_id" --claim-file "$claim_file" --json -
rm -f "$claim_file"
`
			if err := os.WriteFile(clientPath, []byte(script), 0o700); err != nil {
				t.Fatalf("write fake reference client: %v", err)
			}
			if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", client, "--client-command", clientPath); err != nil {
				t.Fatalf("drain fake provider request: %v\n%s", err, output)
			}
			db, err := sql.Open("sqlite3", filepath.Join(homeDir, "workgraph.db"))
			if err != nil {
				t.Fatalf("open event store: %v", err)
			}
			defer db.Close()
			var events, completed int
			if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE source = 'slack' AND summary = 'Reference worker event'`).Scan(&events); err != nil {
				t.Fatalf("count reference events: %v", err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM capture_requests WHERE connector_id = 'slack' AND status = 'completed'`).Scan(&completed); err != nil {
				t.Fatalf("count completed requests: %v", err)
			}
			if events != 1 || completed != 1 {
				t.Fatalf("expected one event and completed request, got events=%d completed=%d", events, completed)
			}
		})
	}
}
