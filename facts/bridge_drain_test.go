package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
