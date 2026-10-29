package facts

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

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
		"mcp__plugin_workgraph_workgraph__llm_task_submit",
	} {
		if !strings.Contains(allowed, expected) {
			t.Fatalf("Claude permissions omitted %q:\n%s", expected, contents)
		}
	}
	for _, forbidden := range []string{"mcp__plugin_workgraph_workgraph__*", "mcp__plugin_workgraph_workgraph__next_items", "connector_bridge_configure", "connector_bridge_disconnect", "Bash"} {
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

func TestBridgeWorkerConfigDirPersistsAndControlsClientAccountContext(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	configDir := filepath.Join(fixtureDir, "codex-work")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("create Codex config directory: %v", err)
	}
	ambientDir := filepath.Join(fixtureDir, "codex-personal")
	t.Setenv("CODEX_HOME", ambientDir)
	environmentPath := filepath.Join(fixtureDir, "codex.environment")
	clientPath := filepath.Join(fixtureDir, "codex")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nprintf '%s' \"${CODEX_HOME:-missing}\" > \""+environmentPath+"\"\n"), 0o700); err != nil {
		t.Fatalf("write fake Codex: %v", err)
	}
	installRoot := filepath.Join(fixtureDir, "installed")
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

	if output := install("--config-dir", configDir); !strings.Contains(string(output), "Config dir: "+configDir) {
		t.Fatalf("install did not report config-dir binding:\n%s", output)
	}
	contents, err := os.ReadFile(filepath.Join(homeDir, "bridge", "workers.json"))
	if err != nil {
		t.Fatalf("read worker config: %v", err)
	}
	if !strings.Contains(string(contents), `"config_dir": "`+configDir+`"`) {
		t.Fatalf("worker config omitted config-dir binding:\n%s", contents)
	}
	if output := install(); !strings.Contains(string(output), "Config dir: "+configDir) {
		t.Fatalf("reinstall did not preserve config-dir binding:\n%s", output)
	}
	doctor, err := runworkgraph(t, repoRoot(t), "plugin", "doctor", "--home", homeDir,
		"--client", "codex", "--client-command", clientPath, "--install-root", installRoot)
	if err != nil || !strings.Contains(string(doctor), "Config dir: "+configDir) {
		t.Fatalf("doctor did not verify config-dir binding: %v\n%s", err, doctor)
	}

	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit request: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "codex", "--client-command", clientPath); err != nil {
		t.Fatalf("drain with config-dir binding: %v\n%s", err, output)
	}
	environment, err := os.ReadFile(environmentPath)
	if err != nil {
		t.Fatalf("read Codex environment: %v", err)
	}
	if string(environment) != configDir {
		t.Fatalf("expected stored config dir %q, got %q", configDir, environment)
	}

	if output := install("--clear-config-dir"); !strings.Contains(string(output), "Config dir: ambient default") {
		t.Fatalf("clear did not report ambient default:\n%s", output)
	}
}

func TestSlackListsBridgeContractAllowsContentHashWithoutClaimingDeletion(t *testing.T) {
	root := repoRoot(t)
	skillPaths := []string{
		filepath.Join(root, ".agents", "skills", "workgraph-bridge", "SKILL.md"),
		filepath.Join(root, "integrations", "claude-code", "plugins", "workgraph", "skills", "workgraph-bridge", "SKILL.md"),
		filepath.Join(root, "integrations", "codex", "plugins", "workgraph", "skills", "workgraph-bridge", "SKILL.md"),
		filepath.Join(root, "integrations", "copilot", "plugins", "workgraph", "skills", "workgraph-bridge", "SKILL.md"),
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
			"resource read needed", "routing context", "connector_required_tools", "snapshot_continues",
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
		filepath.Join(root, "integrations", "copilot", "plugins", "workgraph", "skills", "workgraph-bridge", "references", "event-contracts.md"),
	}
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read Slack Lists bridge contract %s: %v", path, err)
		}
		for _, expected := range []string{"<revision-or-content-hash>", "canonical JSON", "snapshot_continues", "Multi-line values", "A missing row is not a deletion", "complete_snapshot", "slack_read_file",
			"`slack.thread_reply`", "`channel_id`", "`channel_name`", "`conversation_type`", "`DM: <user name>`", "`Group DM: <names>`", "`DM (<channel_id>)`", "Slack conversation info"} {
			if !strings.Contains(string(contents), expected) {
				t.Fatalf("Slack Lists bridge contract %s omitted %q", path, expected)
			}
		}
	}
}

func TestBridgedCaptureOperatorGuideCoversSetupSupportAndRecovery(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "bridged-capture.md"))
	if err != nil {
		t.Fatalf("read bridged-capture operator guide: %v", err)
	}
	guide := string(contents)
	for _, expected := range []string{
		"workgraph plugin install --client codex",
		"workgraph connectors connect <connector> --mode bridged",
		"never receives the provider token",
		"notion` | **no**",
		"notion.activity` | **only**",
		"workgraph capture requests --list",
		"workgraph capture requests --cancel <request-id>",
		"capture_request_fail",
		"specs/bridged-capture.md",
		".agents/skills/workgraph-bridge/SKILL.md",
	} {
		if !strings.Contains(guide, expected) {
			t.Fatalf("operator guide omitted %q", expected)
		}
	}
}

func TestBridgeInstallPackagesEveryClientIdempotently(t *testing.T) {
	for _, client := range []string{"codex", "claude-code", "copilot"} {
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
			if client == "copilot" {
				manifest = "plugin.json"
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
	for _, expected := range []string{"exec", "--ephemeral", "workgraph-bridge", "List pending requests before claiming", "Claim with worker name codex-drain-"} {
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
	for _, args := range [][]string{
		{"llm", "add", "claude-bridge", "--home", homeDir, "--provider", "ai-client", "--client", "claude-code", "--model", "haiku", "--execution", "bridge"},
		{"llm", "use", "claude-bridge", "--home", homeDir, "--for", "associate"},
	} {
		if output, err := runworkgraph(t, repoRoot(t), args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, output)
		}
	}
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO capture_requests (id, connector_id, kind, since, until, params_json, status, attempts, available_at, created_at)
		VALUES ('llm-task-fact', 'llm.associate', 'llm_task', ?, ?, '{"unit_ids":["u1"]}', 'pending', 1, ?, ?)`, future, future, future, future); err != nil {
		t.Fatalf("insert llm_task request: %v", err)
	}
	if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "claude-code", "--client-command", clientPath); err != nil {
		t.Fatalf("drain while the task is in backoff: %v\n%s", err, output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("drain launched Claude for a task in backoff: %v", err)
	}
	if _, err := db.Exec(`UPDATE capture_requests SET status = 'claimed', claimed_by = 'claude-code-drain-other', claim_token = 'other', lease_expires_at = ?, available_at = '2026-10-21T00:00:00Z' WHERE id = 'llm-task-fact'`, future); err != nil {
		t.Fatal(err)
	}
	if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "claude-code", "--client-command", clientPath); err != nil {
		t.Fatalf("drain while another worker holds the task: %v\n%s", err, output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("drain launched Claude for a task another worker holds: %v", err)
	}
	if _, err := db.Exec(`UPDATE capture_requests SET status = 'pending', claimed_by = NULL, claim_token = NULL, lease_expires_at = NULL WHERE id = 'llm-task-fact'`); err != nil {
		t.Fatal(err)
	}
	if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "claude-code", "--client-command", clientPath); err != nil {
		t.Fatalf("drain categorization work without provider tools: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected Claude to run categorization work that needs no provider tools: %v", err)
	}
	for _, expected := range []string{"--model haiku", "kind llm_task", "llm_task_submit", "Claim at most one request"} {
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("categorization drain omitted %q:\n%s", expected, contents)
		}
	}
	if strings.Contains(string(contents), "connector_required_tools") {
		t.Fatalf("categorization drain asked for provider preflight:\n%s", contents)
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
	if err := os.MkdirAll(claudeConfigDir, 0o700); err != nil {
		t.Fatalf("create Claude config directory: %v", err)
	}
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
		"--model", "launchd-pinned", "--config-dir", claudeConfigDir); err != nil {
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
	} {
		if !strings.Contains(string(plist), expected) {
			t.Fatalf("launch agent omitted %q:\n%s", expected, plist)
		}
	}
	for _, forbidden := range []string{"ACCESS_TOKEN", "must-not-enter-launch-agent", "CLAUDE_CONFIG_DIR", claudeConfigDir, "launchd-pinned", "--model"} {
		if strings.Contains(string(plist), forbidden) {
			t.Fatalf("launch agent copied generated configuration %q:\n%s", forbidden, plist)
		}
	}
	doctor, err := runworkgraph(t, repoRoot(t), "plugin", "doctor", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", installRoot)
	if err != nil {
		t.Fatalf("doctor installed launch agent: %v\n%s", err, doctor)
	}
	if !strings.Contains(string(doctor), "Worker environment: ready") || !strings.Contains(string(doctor), "Model: launchd-pinned") || !strings.Contains(string(doctor), "Config dir: "+claudeConfigDir) {
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

	staleHeartbeat := time.Now().UTC().Add(-19*24*time.Hour - time.Hour).Format(time.RFC3339Nano)
	if err := os.MkdirAll(filepath.Join(homeDir, "bridge"), 0o700); err != nil {
		t.Fatalf("create bridge state directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "bridge", "claude-code.heartbeat"), []byte(staleHeartbeat+"\n"), 0o600); err != nil {
		t.Fatalf("write stale heartbeat: %v", err)
	}
	output, err = runworkgraph(t, repoRoot(t), "bridge", "doctor", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", installRoot)
	if err != nil {
		t.Fatalf("doctor bridge with stale heartbeat: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "Round trip: ready") || !strings.Contains(string(output), "Round trip: stale (last heartbeat 19d ago)") {
		t.Fatalf("expected stale heartbeat to make the round trip stale:\n%s", output)
	}
}

func TestBridgeDrainTimeoutReturnsClaimedRequestForRetry(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	clientPath := filepath.Join(fixtureDir, "codex")
	otherClaimFile := filepath.Join(fixtureDir, "other-claim.json")
	script := "#!/bin/sh\n" +
		"\"$WORKGRAPH_EXECUTABLE\" capture requests --home \"$WORKGRAPH_HOME\" --claim --connector github --max 1 --worker other-worker --claim-file \"" + otherClaimFile + "\" >/dev/null || exit 4\n" +
		"\"$WORKGRAPH_EXECUTABLE\" capture requests --home \"$WORKGRAPH_HOME\" --claim --connector slack --max 1 --worker \"$WORKGRAPH_BRIDGE_WORKER\" --claim-file \"" + filepath.Join(fixtureDir, "claim.json") + "\" >/dev/null || exit 3\n" +
		"echo 'calling provider tool'\n" +
		"exec sleep 30\n"
	if err := os.WriteFile(clientPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write hanging fake Codex: %v", err)
	}
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	connectBridgedConnector(t, repoRoot(t), homeDir, "github")
	for _, connector := range []string{"slack", "github"} {
		if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", connector); err != nil {
			t.Fatalf("emit %s request: %v\n%s", connector, err, output)
		}
	}
	started := time.Now()
	output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "codex", "--client-command", clientPath, "--timeout", "2s")
	if err == nil || !strings.Contains(string(output), "timed out after 2s") {
		t.Fatalf("expected bounded drain timeout, got err=%v output=%s", err, output)
	}
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Fatalf("expected drain to stop near its timeout, took %s", elapsed)
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	var status, lastError string
	var claimedBy sql.NullString
	var attempts int
	if err := db.QueryRow(`SELECT status, last_error, claimed_by, attempts FROM capture_requests WHERE connector_id = 'slack'`).Scan(&status, &lastError, &claimedBy, &attempts); err != nil {
		t.Fatalf("read timed out request: %v", err)
	}
	if status != "pending" || claimedBy.Valid || attempts != 1 || lastError != "bridge drain timed out after 2s" {
		t.Fatalf("expected timed out claim back to pending, got status=%q claimed_by=%v attempts=%d error=%q", status, claimedBy, attempts, lastError)
	}
	var otherClaim struct {
		Token string `json:"claim_token"`
	}
	contents, err := os.ReadFile(otherClaimFile)
	if err != nil || json.Unmarshal(contents, &otherClaim) != nil || otherClaim.Token == "" {
		t.Fatalf("read other worker's claim file: %v\n%s", err, contents)
	}
	var otherStatus, otherWorker, otherToken string
	if err := db.QueryRow(`SELECT status, claimed_by, claim_token FROM capture_requests WHERE connector_id = 'github'`).Scan(&otherStatus, &otherWorker, &otherToken); err != nil {
		t.Fatalf("read other worker's request: %v", err)
	}
	if otherStatus != "claimed" || otherWorker != "other-worker" || otherToken != otherClaim.Token {
		t.Fatalf("expected drain timeout to leave another worker's claim intact, got status=%q worker=%q", otherStatus, otherWorker)
	}
	statusOutput, err := runworkgraph(t, repoRoot(t), "connectors", "status", "--home", homeDir)
	if err != nil {
		t.Fatalf("connector status: %v\n%s", err, statusOutput)
	}
	if !strings.Contains(string(statusOutput), "last error bridge drain timed out after 2s") {
		t.Fatalf("expected connector status to show the drain timeout:\n%s", statusOutput)
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
				allow := []string{"mcp__fake_provider__read"}
				for _, tool := range []string{"capture_requests_list", "capture_requests_claim", "capture_request_renew", "capture_ingest", "capture_request_fail", "capture_watermark", "connector_status", "connector_required_tools", "bridge_worker_heartbeat", "llm_task_submit"} {
					allow = append(allow, "mcp__plugin_workgraph_workgraph__"+tool)
				}
				settings, err := json.Marshal(map[string]any{"permissions": map[string]any{"allow": allow}})
				if err != nil {
					t.Fatalf("encode Claude worker settings: %v", err)
				}
				if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), settings, 0o600); err != nil {
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
printf '%s\n' '{"type":"slack.message","timestamp":"2026-09-13T12:00:00Z","external_id":"C0DEMO123:reference-worker","summary":"Reference worker event","payload":{"channel_id":"C0DEMO123","channel_name":"demo-changes","conversation_type":"channel","text":"fake approved provider result"}}' | "$WORKGRAPH_EXECUTABLE" capture ingest --home "$WORKGRAPH_HOME" --request "$request_id" --claim-file "$claim_file" --json -
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

func TestCopilotPluginInstallPersistsOnlyExplicitProviderTools(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	clientPath := filepath.Join(fixtureDir, "copilot")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake Copilot: %v", err)
	}
	installRoot := filepath.Join(fixtureDir, "installed")
	install := func(extra ...string) ([]byte, error) {
		t.Helper()
		args := []string{"plugin", "install", "--home", homeDir, "--client", "copilot",
			"--client-command", clientPath, "--install-root", installRoot, "--no-launchd"}
		return runworkgraph(t, repoRoot(t), append(args, extra...)...)
	}
	output, err := install("--allow-provider-tool", "slack(slack_read_channel)", "--allow-provider-tool", "azure-devops(wit_query)", "--allow-provider-tool", "slack(slack_read_channel)")
	if err != nil || !strings.Contains(string(output), "Provider tools: 2 explicitly allowed") {
		t.Fatalf("install Copilot plugin with provider tools: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(filepath.Join(homeDir, "bridge", "workers.json"))
	if err != nil {
		t.Fatalf("read worker config: %v", err)
	}
	var config struct {
		Workers map[string]struct {
			ProviderTools []string `json:"provider_tools"`
		} `json:"workers"`
	}
	if err := json.Unmarshal(contents, &config); err != nil {
		t.Fatalf("parse worker config: %v", err)
	}
	if got := strings.Join(config.Workers["copilot"].ProviderTools, ","); got != "azure-devops(wit_query),slack(slack_read_channel)" {
		t.Fatalf("worker config stored provider tools %q:\n%s", got, contents)
	}
	if output, err := install("--allow-provider-tool", "slack(slack_list_user_channels)"); err != nil || !strings.Contains(string(output), "Provider tools: 3 explicitly allowed") {
		t.Fatalf("reinstall did not add to the preserved provider tools: %v\n%s", err, output)
	}
	if output, err := install("--model", "copilot-pinned"); err != nil || !strings.Contains(string(output), "Provider tools: 3 explicitly allowed") {
		t.Fatalf("reinstall did not preserve provider tools: %v\n%s", err, output)
	}
	doctor, err := runworkgraph(t, repoRoot(t), "plugin", "doctor", "--home", homeDir,
		"--client", "copilot", "--client-command", clientPath, "--install-root", installRoot)
	if err != nil || !strings.Contains(string(doctor), "Provider tools: 3 explicitly allowed") || !strings.Contains(string(doctor), "Model: copilot-pinned") {
		t.Fatalf("doctor did not report Copilot worker settings: %v\n%s", err, doctor)
	}
	for _, rejected := range []string{"slack(*)", "slack", "workgraph(capture_ingest)", "shell(rm)", "mcp__claude_ai_Slack__slack_read_channel"} {
		if output, err := install("--allow-provider-tool", rejected); err == nil {
			t.Fatalf("expected Copilot provider tool %q to be rejected:\n%s", rejected, output)
		}
	}
	if output, err := runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir, "--client", "codex",
		"--client-command", clientPath, "--install-root", filepath.Join(fixtureDir, "codex"), "--no-launchd",
		"--allow-provider-tool", "slack(slack_read_channel)"); err == nil {
		t.Fatalf("expected Codex provider tools to stay unsupported:\n%s", output)
	}
}

func TestCopilotBridgeDrainAllowsOnlyWorkgraphDrainToolsAndExplicitProviderTools(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	logPath := filepath.Join(fixtureDir, "drain.log")
	environmentPath := filepath.Join(fixtureDir, "drain.env")
	clientPath := filepath.Join(fixtureDir, "copilot")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \""+logPath+"\"\nprintf '%s|%s' \"${COPILOT_ALLOW_ALL-unset}\" \"${COPILOT_CUSTOM_INSTRUCTIONS_DIRS-unset}\" > \""+environmentPath+"\"\n"), 0o700); err != nil {
		t.Fatalf("write fake Copilot: %v", err)
	}
	t.Setenv("COPILOT_ALLOW_ALL", "true")
	t.Setenv("COPILOT_CUSTOM_INSTRUCTIONS_DIRS", fixtureDir)
	if output, err := runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir,
		"--client", "copilot", "--client-command", clientPath, "--install-root", filepath.Join(fixtureDir, "installed"),
		"--no-launchd", "--model", "copilot-bridge-pinned",
		"--allow-provider-tool", "slack(slack_list_user_channels)"); err != nil {
		t.Fatalf("install Copilot plugin: %v\n%s", err, output)
	}
	_ = os.Remove(logPath)
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit request: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "copilot", "--client-command", clientPath); err != nil {
		t.Fatalf("drain with Copilot: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read Copilot invocation: %v", err)
	}
	arguments := strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n")
	for _, expected := range []string{
		"-s", "--no-ask-user", "--sync-session=off", "--model=copilot-bridge-pinned",
		"--allow-tool=workgraph(capture_requests_claim)", "--allow-tool=workgraph(capture_ingest)",
		"--allow-tool=workgraph(connector_required_tools)", "--allow-tool=workgraph(llm_task_submit)",
		"--allow-tool=slack(slack_list_user_channels)", "-p",
		"--no-custom-instructions", "--disallow-temp-dir",
		"--deny-tool=shell", "--deny-tool=write", "--deny-tool=url", "--deny-tool=memory",
		"--available-tools=skill", "--available-tools=workgraph-capture_requests_claim",
		"--available-tools=workgraph-llm_task_submit", "--available-tools=slack-slack_list_user_channels",
	} {
		if !slices.Contains(arguments, expected) {
			t.Fatalf("Copilot drain invocation omitted %q:\n%s", expected, contents)
		}
	}
	for _, argument := range arguments {
		tool, found := strings.CutPrefix(argument, "--available-tools=")
		if found && tool != "skill" && tool != "slack-slack_list_user_channels" && !strings.HasPrefix(tool, "workgraph-") {
			t.Fatalf("Copilot drain exposed unapproved tool %q:\n%s", tool, contents)
		}
	}
	environment, err := os.ReadFile(environmentPath)
	if err != nil || string(environment) != "unset|unset" {
		t.Fatalf("Copilot drain inherited permission or instruction overrides %q: %v", environment, err)
	}
	prompt := arguments[len(arguments)-1]
	if arguments[len(arguments)-2] != "-p" || !strings.Contains(prompt, "List pending requests before claiming") || !strings.Contains(prompt, "Claim with worker name copilot-drain-") {
		t.Fatalf("Copilot drain prompt did not follow -p:\n%s", contents)
	}
	for _, argument := range arguments {
		if argument == "--allow-all-tools" || argument == "--allow-all" || argument == "--yolo" || argument == "--allow-tool=workgraph" || strings.HasPrefix(argument, "--allow-tool=shell") || strings.HasPrefix(argument, "--allow-tool=write") {
			t.Fatalf("Copilot drain granted broad permission %q:\n%s", argument, contents)
		}
	}
}

func TestCopilotBridgeDrainLeavesCaptureRequestsPendingWithoutProviderTools(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	logPath := filepath.Join(fixtureDir, "copilot.log")
	clientPath := filepath.Join(fixtureDir, "copilot")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+logPath+"\"\n"), 0o700); err != nil {
		t.Fatalf("write fake Copilot: %v", err)
	}
	if output, err := runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir,
		"--client", "copilot", "--client-command", clientPath, "--install-root", filepath.Join(fixtureDir, "installed"), "--no-launchd"); err != nil {
		t.Fatalf("install Copilot plugin: %v\n%s", err, output)
	}
	_ = os.Remove(logPath)
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit request: %v\n%s", err, output)
	}
	output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "copilot", "--client-command", clientPath)
	if err != nil || !strings.Contains(string(output), "No Copilot provider tools are explicitly allowed") {
		t.Fatalf("expected pending capability warning: %v\n%s", err, output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("incapable drain launched Copilot: %v", err)
	}
	var status string
	if err := openBridgedCaptureDatabase(t, homeDir).QueryRow(`SELECT status FROM capture_requests WHERE connector_id = 'slack'`).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("expected request to remain pending, got %q err=%v", status, err)
	}
}

func TestCopilotPluginReinstallRepointsTheMarketplaceAtTheCurrentInstallRoot(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	statePath := filepath.Join(fixtureDir, "marketplace")
	clientPath := filepath.Join(fixtureDir, "copilot")
	script := `#!/bin/sh
case "$1 $2 $3" in
  "plugin marketplace add")
    if [ -f "` + statePath + `" ]; then echo 'Marketplace "workgraph" already registered' >&2; exit 1; fi
    printf '%s' "$4" > "` + statePath + `" ;;
  "plugin marketplace remove")
    if [ ! -f "` + statePath + `" ]; then echo 'Marketplace "workgraph" is not registered' >&2; exit 1; fi
    rm -f "` + statePath + `" ;;
esac
exit 0
`
	if err := os.WriteFile(clientPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake Copilot: %v", err)
	}
	for _, installRoot := range []string{filepath.Join(fixtureDir, "first"), filepath.Join(fixtureDir, "second"), filepath.Join(fixtureDir, "second")} {
		if output, err := runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir, "--client", "copilot",
			"--client-command", clientPath, "--install-root", installRoot, "--no-launchd"); err != nil {
			t.Fatalf("install Copilot plugin at %s: %v\n%s", installRoot, err, output)
		}
		registered, err := os.ReadFile(statePath)
		if err != nil || string(registered) != installRoot {
			t.Fatalf("Copilot marketplace points at %q, want %q: %v", registered, installRoot, err)
		}
	}
}

func TestCopilotBridgeDrainRejectsHandEditedProviderToolsWithoutLaunching(t *testing.T) {
	for _, tool := range []string{"shell(rm)", "workgraph(capture_ingest)", "slack"} {
		t.Run(tool, func(t *testing.T) {
			homeDir := initBridgedCaptureHome(t)
			fixtureDir := t.TempDir()
			markerPath := filepath.Join(fixtureDir, "launched")
			clientPath := filepath.Join(fixtureDir, "copilot")
			if err := os.WriteFile(clientPath, []byte("#!/bin/sh\ncase \"$1\" in plugin) exit 0 ;; esac\ntouch \""+markerPath+"\"\n"), 0o700); err != nil {
				t.Fatalf("write fake Copilot: %v", err)
			}
			if output, err := runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir,
				"--client", "copilot", "--client-command", clientPath, "--install-root", filepath.Join(fixtureDir, "installed"),
				"--no-launchd", "--allow-provider-tool", "slack(slack_list_user_channels)"); err != nil {
				t.Fatalf("install Copilot plugin: %v\n%s", err, output)
			}
			configPath := filepath.Join(homeDir, "bridge", "workers.json")
			if err := os.WriteFile(configPath, []byte(`{"workers":{"copilot":{"provider_tools":["`+tool+`"]}}}`), 0o600); err != nil {
				t.Fatalf("edit worker config: %v", err)
			}
			connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
			if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
				t.Fatalf("emit request: %v\n%s", err, output)
			}
			output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "copilot", "--client-command", clientPath)
			if err == nil || !strings.Contains(string(output), "--clear-provider-tools") {
				t.Fatalf("drain accepted stored provider tool %q: %v\n%s", tool, err, output)
			}
			if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
				t.Fatalf("drain launched Copilot with stored provider tool %q: %v", tool, err)
			}
		})
	}
}

func TestCopilotPluginInstallClearProviderToolsReplacesTheStoredSet(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	clientPath := filepath.Join(fixtureDir, "copilot")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake Copilot: %v", err)
	}
	install := func(extra ...string) string {
		t.Helper()
		args := append([]string{"plugin", "install", "--home", homeDir, "--client", "copilot", "--client-command", clientPath,
			"--install-root", filepath.Join(fixtureDir, "installed"), "--no-launchd"}, extra...)
		output, err := runworkgraph(t, repoRoot(t), args...)
		if err != nil {
			t.Fatalf("install Copilot plugin %v: %v\n%s", extra, err, output)
		}
		return string(output)
	}
	install("--allow-provider-tool", "slack(slack_list_user_channels)", "--allow-provider-tool", "slack(slack_read_channel)")
	if output := install("--clear-provider-tools", "--allow-provider-tool", "azure-devops(wit_query)"); !strings.Contains(output, "Provider tools: 1") {
		t.Fatalf("replacing provider tools did not report one tool:\n%s", output)
	}
	contents, err := os.ReadFile(filepath.Join(homeDir, "bridge", "workers.json"))
	if err != nil || strings.Contains(string(contents), "slack(") || !strings.Contains(string(contents), "azure-devops(wit_query)") {
		t.Fatalf("worker config did not replace provider tools: %v\n%s", err, contents)
	}
	if output := install("--clear-provider-tools"); !strings.Contains(output, "Provider tools: 0") {
		t.Fatalf("clearing provider tools did not report zero tools:\n%s", output)
	}
	if contents, err := os.ReadFile(filepath.Join(homeDir, "bridge", "workers.json")); err == nil && strings.Contains(string(contents), "provider_tools") {
		t.Fatalf("worker config kept provider tools after clearing:\n%s", contents)
	}
	output, err := runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir, "--client", "codex", "--client-command", clientPath,
		"--install-root", filepath.Join(fixtureDir, "codex"), "--no-launchd", "--clear-provider-tools")
	if err == nil || !strings.Contains(string(output), "--clear-provider-tools is supported only for copilot") {
		t.Fatalf("codex accepted --clear-provider-tools: %v\n%s", err, output)
	}
}
