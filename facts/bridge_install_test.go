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

func TestSlackListsBridgeContractAllowsContentHashWithoutClaimingDeletion(t *testing.T) {
	root := repoRoot(t)
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
		for _, expected := range []string{"<revision-or-content-hash>", "canonical JSON", "A missing row is not a deletion"} {
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
		"--no-launchd", "--allow-provider-tool", "mcp__claude_ai_Slack__slack_list_user_channels"); err != nil {
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
		"-p", "--permission-mode dontAsk", "--settings " + filepath.Join(homeDir, ".claude", "settings.json"),
		"List pending requests before claiming", "leave the request pending", "Never fall back to the CLI",
	} {
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("Claude drain invocation omitted %q:\n%s", expected, contents)
		}
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
	if output, err := runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", filepath.Join(fixtureDir, "installed")); err != nil {
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
