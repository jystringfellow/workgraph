package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func installFactClaudeBridge(t *testing.T, homeDir string) (string, string, string) {
	t.Helper()
	fixtureDir := t.TempDir()
	logPath := filepath.Join(fixtureDir, "claude.log")
	clientPath := filepath.Join(fixtureDir, "claude")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \""+logPath+"\"\n"), 0o700); err != nil {
		t.Fatalf("write fake Claude: %v", err)
	}
	installRoot := filepath.Join(fixtureDir, "installed")
	if output, err := runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", installRoot,
		"--no-launchd", "--model", "claude-bridge-pinned",
		"--allow-provider-tool", "mcp__claude_ai_Slack__slack_list_user_channels"); err != nil {
		t.Fatalf("install Claude bridge: %v\n%s", err, output)
	}
	_ = os.Remove(logPath)
	return clientPath, logPath, installRoot
}

func drainFactClaudeBridge(t *testing.T, homeDir string, clientPath string, logPath string) string {
	t.Helper()
	_ = os.Remove(logPath)
	if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "claude-code", "--client-command", clientPath); err != nil {
		t.Fatalf("drain with Claude: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected drain to launch Claude: %v", err)
	}
	return string(contents)
}

func TestBridgeDrainAlternatesCategorizationWithClaimableCapture(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	clientPath, logPath, _ := installFactClaudeBridge(t, homeDir)
	for _, args := range [][]string{
		{"llm", "add", "claude-bridge", "--home", homeDir, "--provider", "ai-client", "--client", "claude-code", "--model", "haiku", "--execution", "bridge"},
		{"llm", "use", "claude-bridge", "--home", homeDir, "--for", "associate"},
	} {
		if output, err := runworkgraph(t, repoRoot(t), args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, output)
		}
	}
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit capture request: %v\n%s", err, output)
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO capture_requests (id, connector_id, kind, since, until, params_json, status, attempts, available_at, created_at)
		VALUES ('llm-task-fair', 'llm.associate', 'llm_task', ?, ?, '{"unit_ids":["u1"]}', 'pending', 0, ?, ?)`, now, now, now, now); err != nil {
		t.Fatalf("insert llm_task request: %v", err)
	}

	sessions := []string{}
	for range 4 {
		sessions = append(sessions, drainFactClaudeBridge(t, homeDir, clientPath, logPath))
	}
	kinds := make([]string, len(sessions))
	for index, session := range sessions {
		switch {
		case strings.Contains(session, "kind llm_task") && strings.Contains(session, "--model haiku"):
			kinds[index] = "llm_task"
		case strings.Contains(session, "connector_required_tools") && strings.Contains(session, "--model claude-bridge-pinned"):
			kinds[index] = "capture"
		default:
			t.Fatalf("drain %d ran an unexpected session:\n%s", index, session)
		}
	}
	for index := 1; index < len(kinds); index++ {
		if kinds[index] == kinds[index-1] {
			t.Fatalf("expected categorization and capture to alternate while both are claimable, got %v", kinds)
		}
	}
	if kinds[0] != "llm_task" {
		t.Fatalf("expected the first contended drain to give categorization its turn, got %v", kinds)
	}

	if _, err := db.Exec(`DELETE FROM capture_requests WHERE id = 'llm-task-fair'`); err != nil {
		t.Fatalf("remove llm_task request: %v", err)
	}
	for range 2 {
		if session := drainFactClaudeBridge(t, homeDir, clientPath, logPath); !strings.Contains(session, "connector_required_tools") {
			t.Fatalf("expected capture to run every drain when categorization is idle:\n%s", session)
		}
	}
}

func TestClaudeBridgeDrainRefusesToClaimWithoutWorkgraphPermissions(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	clientPath, logPath, installRoot := installFactClaudeBridge(t, homeDir)
	settingsPath := filepath.Join(homeDir, ".claude", "settings.json")
	contents, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read Claude bridge settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(contents, &settings); err != nil {
		t.Fatalf("parse Claude bridge settings: %v", err)
	}
	permissions := settings["permissions"].(map[string]any)
	kept := []any{}
	for _, permission := range permissions["allow"].([]any) {
		if permission != "mcp__plugin_workgraph_workgraph__llm_task_submit" {
			kept = append(kept, permission)
		}
	}
	permissions["allow"] = kept
	stale, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, stale, 0o600); err != nil {
		t.Fatalf("write stale Claude bridge settings: %v", err)
	}

	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit capture request: %v\n%s", err, output)
	}
	output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "claude-code", "--client-command", clientPath)
	if err == nil || !strings.Contains(string(output), "mcp__plugin_workgraph_workgraph__llm_task_submit") || !strings.Contains(string(output), "rerun workgraph plugin install --client claude-code") {
		t.Fatalf("expected drain to refuse with the missing permission and reinstall hint, got err=%v:\n%s", err, output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("drain launched Claude without the workgraph permissions it needs: %v", err)
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	var status string
	var attempts int
	if err := db.QueryRow(`SELECT status, attempts FROM capture_requests WHERE connector_id = 'slack'`).Scan(&status, &attempts); err != nil {
		t.Fatalf("read capture request: %v", err)
	}
	if status != "pending" || attempts != 0 {
		t.Fatalf("expected the request to stay pending without consuming attempts, got status=%q attempts=%d", status, attempts)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "bridge", "claude-code.heartbeat")); err != nil {
		t.Fatalf("expected a blocked drain to record its heartbeat: %v", err)
	}

	for _, command := range []string{"list", "status"} {
		statusOutput, err := runworkgraph(t, repoRoot(t), "connectors", command, "--home", homeDir)
		if err != nil {
			t.Fatalf("connectors %s: %v\n%s", command, err, statusOutput)
		}
		for _, expected := range []string{"bridge drain blocked (claude-code)", "llm_task_submit", "rerun workgraph plugin install --client claude-code"} {
			if !strings.Contains(string(statusOutput), expected) {
				t.Fatalf("connectors %s omitted %q:\n%s", command, expected, statusOutput)
			}
		}
	}
	doctor, err := runworkgraph(t, repoRoot(t), "plugin", "doctor", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", installRoot)
	if err == nil || !strings.Contains(string(doctor), "llm_task_submit") || !strings.Contains(string(doctor), "rerun workgraph plugin install --client claude-code") {
		t.Fatalf("expected plugin doctor to name the missing permission and reinstall hint, got err=%v:\n%s", err, doctor)
	}

	if output, err := runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", installRoot, "--no-launchd"); err != nil {
		t.Fatalf("reinstall Claude bridge: %v\n%s", err, output)
	}
	drainFactClaudeBridge(t, homeDir, clientPath, logPath)
	statusOutput, err := runworkgraph(t, repoRoot(t), "connectors", "status", "--home", homeDir)
	if err != nil {
		t.Fatalf("connectors status after reinstall: %v\n%s", err, statusOutput)
	}
	if strings.Contains(string(statusOutput), "bridge drain blocked") {
		t.Fatalf("expected reinstall and a passing drain to clear the blocked state:\n%s", statusOutput)
	}
}

func TestClaudeBridgeDrainAcceptsServerWideWorkgraphPermissionRules(t *testing.T) {
	for _, rule := range []string{"mcp__plugin_workgraph_workgraph__*", "mcp__plugin_workgraph_workgraph"} {
		t.Run(rule, func(t *testing.T) {
			homeDir := initBridgedCaptureHome(t)
			clientPath, logPath, _ := installFactClaudeBridge(t, homeDir)
			settings, err := json.Marshal(map[string]any{"permissions": map[string]any{"allow": []string{rule, "mcp__claude_ai_Slack__slack_list_user_channels"}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(homeDir, ".claude", "settings.json"), settings, 0o600); err != nil {
				t.Fatalf("write server-wide Claude bridge settings: %v", err)
			}
			connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
			if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
				t.Fatalf("emit capture request: %v\n%s", err, output)
			}
			if session := drainFactClaudeBridge(t, homeDir, clientPath, logPath); !strings.Contains(session, "connector_required_tools") {
				t.Fatalf("expected a server-wide workgraph rule to allow a capture drain:\n%s", session)
			}
			if _, err := os.Stat(filepath.Join(homeDir, "bridge", "claude-code.blocked")); !os.IsNotExist(err) {
				t.Fatalf("expected no blocked drain state for a server-wide workgraph rule: %v", err)
			}
		})
	}
}

func TestBridgeLoginSignsTheWorkerConfigDirBackIn(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	configDir := filepath.Join(fixtureDir, "claude-enterprise")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	signedOut := filepath.Join(fixtureDir, "signed-out")
	loginLog := filepath.Join(fixtureDir, "login.log")
	clientPath := filepath.Join(fixtureDir, "claude")
	script := "#!/bin/sh\n" +
		"if [ \"$1 $2\" = \"auth login\" ]; then printf '%s %s\\n' \"$*\" \"$CLAUDE_CONFIG_DIR\" > \"" + loginLog + "\"; rm -f \"" + signedOut + "\"; exit 0; fi\n" +
		"if [ \"$1\" = \"-p\" ] && [ -f \"" + signedOut + "\" ]; then echo 'Failed to authenticate: OAuth session expired and could not be refreshed' >&2; exit 1; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(clientPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake Claude: %v", err)
	}
	if output, err := runworkgraph(t, repoRoot(t), "plugin", "install", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", filepath.Join(fixtureDir, "installed"),
		"--no-launchd", "--config-dir", configDir,
		"--allow-provider-tool", "mcp__claude_ai_Slack__slack_list_user_channels"); err != nil {
		t.Fatalf("install Claude bridge: %v\n%s", err, output)
	}
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit capture request: %v\n%s", err, output)
	}
	if err := os.WriteFile(signedOut, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "claude-code", "--client-command", clientPath); err == nil {
		t.Fatalf("expected an expired sign-in to fail the drain:\n%s", output)
	}
	signedOutDetail := "bridge signed out (claude-code, " + configDir + "): Failed to authenticate: OAuth session expired and could not be refreshed; run workgraph bridge login --client claude-code"
	for _, command := range []string{"list", "status"} {
		output, err := runworkgraph(t, repoRoot(t), "connectors", command, "--home", homeDir)
		if err != nil {
			t.Fatalf("connectors %s: %v\n%s", command, err, output)
		}
		if !strings.Contains(string(output), signedOutDetail) {
			t.Fatalf("connectors %s omitted the signed-out bridge %q:\n%s", command, signedOutDetail, output)
		}
	}
	doctor, err := runworkgraph(t, repoRoot(t), "plugin", "doctor", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", filepath.Join(fixtureDir, "installed"))
	if err != nil {
		t.Fatalf("plugin doctor: %v\n%s", err, doctor)
	}
	if !strings.Contains(string(doctor), "Sign-in: expired at last drain; run workgraph bridge login --client claude-code") {
		t.Fatalf("plugin doctor omitted the expired sign-in:\n%s", doctor)
	}
	warning := regexp.MustCompile(`Bridge signed out: slack \(\d+m\); run workgraph bridge login --client claude-code`)
	today, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, MemoryDir: filepath.Join(t.TempDir(), "memory")})
	if err != nil {
		t.Fatalf("today: %v", err)
	}
	if len(warning.FindAllString(today.Message, -1)) != 1 {
		t.Fatalf("today omitted one signed-out bridge warning %q:\n%s", warning, today.Message)
	}
	resume, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: homeDir, MemoryDir: filepath.Join(t.TempDir(), "memory")})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(warning.FindAllString(resume.Message, -1)) != 1 {
		t.Fatalf("resume omitted one signed-out bridge warning %q:\n%s", warning, resume.Message)
	}

	login, err := runworkgraph(t, repoRoot(t), "bridge", "login", "--home", homeDir, "--client", "claude-code", "--client-command", clientPath)
	if err != nil {
		t.Fatalf("bridge login: %v\n%s", err, login)
	}
	loginArgs, err := os.ReadFile(loginLog)
	if err != nil {
		t.Fatalf("expected bridge login to run Claude's sign-in: %v", err)
	}
	if strings.TrimSpace(string(loginArgs)) != "auth login "+configDir {
		t.Fatalf("expected claude auth login with the worker config dir, got %q", loginArgs)
	}
	for _, expected := range []string{configDir, "workgraph bridge drain --client claude-code"} {
		if !strings.Contains(string(login), expected) {
			t.Fatalf("bridge login output omitted %q:\n%s", expected, login)
		}
	}
	statusOutput, err := runworkgraph(t, repoRoot(t), "connectors", "status", "--home", homeDir)
	if err != nil {
		t.Fatalf("connectors status after login: %v\n%s", err, statusOutput)
	}
	if strings.Contains(string(statusOutput), "bridge signed out") {
		t.Fatalf("expected bridge login to clear the signed-out state:\n%s", statusOutput)
	}

	if output, err := runworkgraph(t, repoRoot(t), "bridge", "login", "--home", homeDir, "--client", "agy"); err == nil {
		t.Fatalf("expected bridge login to reject Antigravity:\n%s", output)
	}
}

func TestBridgeLoginSignsTheFailedCategorizationConfigDirBackIn(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	workerDir := filepath.Join(fixtureDir, "claude-worker")
	profileDir := filepath.Join(fixtureDir, "claude-profile")
	for _, dir := range []string{workerDir, profileDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	loginLog := filepath.Join(fixtureDir, "login.log")
	clientPath := filepath.Join(fixtureDir, "claude")
	script := "#!/bin/sh\n" +
		"if [ \"$1 $2\" = \"auth login\" ]; then printf '%s\\n' \"$CLAUDE_CONFIG_DIR\" > \"" + loginLog + "\"; exit 0; fi\n" +
		"if [ \"$1\" = \"-p\" ] && [ \"$CLAUDE_CONFIG_DIR\" = \"" + profileDir + "\" ]; then echo 'Failed to authenticate: OAuth session expired and could not be refreshed' >&2; exit 1; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(clientPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake Claude: %v", err)
	}
	for _, args := range [][]string{
		{"plugin", "install", "--home", homeDir, "--client", "claude-code", "--client-command", clientPath,
			"--install-root", filepath.Join(fixtureDir, "installed"), "--no-launchd", "--config-dir", workerDir,
			"--allow-provider-tool", "mcp__claude_ai_Slack__slack_list_user_channels"},
		{"llm", "add", "claude-bridge", "--home", homeDir, "--provider", "ai-client", "--client", "claude-code",
			"--model", "haiku", "--execution", "bridge", "--config-dir", profileDir},
		{"llm", "use", "claude-bridge", "--home", homeDir, "--for", "associate"},
	} {
		if output, err := runworkgraph(t, repoRoot(t), args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, output)
		}
	}
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit capture request: %v\n%s", err, output)
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO capture_requests (id, connector_id, kind, since, until, params_json, status, attempts, available_at, created_at)
		VALUES ('llm-task-signed-out', 'llm.associate', 'llm_task', ?, ?, '{"unit_ids":["u1"]}', 'pending', 0, ?, ?)`, now, now, now, now); err != nil {
		t.Fatalf("insert llm_task request: %v", err)
	}

	if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "claude-code", "--client-command", clientPath); err == nil {
		t.Fatalf("expected the categorization account's expired sign-in to fail the drain:\n%s", output)
	}
	status, err := runworkgraph(t, repoRoot(t), "connectors", "status", "--home", homeDir)
	if err != nil {
		t.Fatalf("connectors status: %v\n%s", err, status)
	}
	if !strings.Contains(string(status), "bridge signed out (claude-code, "+profileDir+")") {
		t.Fatalf("expected status to name the categorization profile's config dir:\n%s", status)
	}
	if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "claude-code", "--client-command", clientPath); err != nil {
		t.Fatalf("expected the worker account's capture drain to succeed: %v\n%s", err, output)
	}
	status, err = runworkgraph(t, repoRoot(t), "connectors", "status", "--home", homeDir)
	if err != nil {
		t.Fatalf("connectors status after capture drain: %v\n%s", err, status)
	}
	if !strings.Contains(string(status), "bridge signed out (claude-code, "+profileDir+")") {
		t.Fatalf("expected the worker account's successful drain to keep the profile account signed out:\n%s", status)
	}

	login, err := runworkgraph(t, repoRoot(t), "bridge", "login", "--home", homeDir, "--client", "claude-code", "--client-command", clientPath)
	if err != nil {
		t.Fatalf("bridge login: %v\n%s", err, login)
	}
	signedIn, err := os.ReadFile(loginLog)
	if err != nil {
		t.Fatalf("expected bridge login to run Claude's sign-in: %v", err)
	}
	if strings.TrimSpace(string(signedIn)) != profileDir {
		t.Fatalf("expected bridge login to sign in the config dir that failed, got %q", signedIn)
	}
}
