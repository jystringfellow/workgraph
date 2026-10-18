package facts

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestAIHandoffLaunchesVerifiedDestinationsWithLineageAndPrivateContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for index, test := range []struct {
		sourceTool  string
		destination string
		promptFlag  string
	}{
		{sourceTool: "claude", destination: "codex"},
		{sourceTool: "codex", destination: "claude"},
		{sourceTool: "claude", destination: "copilot", promptFlag: "--interactive"},
		{sourceTool: "codex", destination: "opencode", promptFlag: "--prompt"},
		{sourceTool: "claude", destination: "claude"},
	} {
		t.Run(test.sourceTool+"_to_"+test.destination, func(t *testing.T) {
			homeDir := filepath.Join(t.TempDir(), ".workgraph")
			initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
			if err != nil {
				t.Fatalf("init workgraph: %v", err)
			}
			workingDir := t.TempDir()
			argsPath := filepath.Join(workingDir, "args.txt")
			contextPath := filepath.Join(workingDir, "environment.txt")
			writeAIFactExecutable(t, workingDir, test.destination, `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_ARGS"
printf '%s\n%s\n%s' "$WORKGRAPH_AI_SESSION_ID" "$WORKGRAPH_AI_HOME" "$WORKGRAPH_AI_DATABASE" > "$WORKGRAPH_AI_TEST_CONTEXT"`)
			sourceID := fmt.Sprintf("00000000-0000-4000-8000-%012d", 300+index)
			insertAINativeFactSession(t, initialized.DatabasePath, sourceID, test.sourceTool, "/usr/local/bin/"+test.sourceTool, test.sourceTool+"-native", "", workingDir, "2026-08-03T12:00:00Z", true)
			insertAIHandoffCheckpoint(t, initialized.DatabasePath, sourceID, workingDir)
			injectForbiddenSourceFields(t, initialized.DatabasePath, sourceID)

			environment := []string{
				"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH"),
				"WORKGRAPH_AI_TEST_ARGS=" + argsPath,
				"WORKGRAPH_AI_TEST_CONTEXT=" + contextPath,
				"AI_HANDOFF_ENV_SECRET=must-not-be-transported",
			}
			output, err := runWorkgraphCommandAllowError(environment,
				"ai", "handoff", test.destination, "--from", sourceID,
				"--home", homeDir, "--database", initialized.DatabasePath,
			)
			if err != nil {
				t.Fatalf("handoff to %s: %v\n%s", test.destination, err, output)
			}
			argsBytes, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatalf("read destination args: %v", err)
			}
			arguments := string(argsBytes)
			for _, expected := range []string{"Continue from the structured Workgraph handoff context", "Ship the handoff feature", "Run focused facts", "main", "changed.go"} {
				if !strings.Contains(arguments, expected) {
					t.Fatalf("destination context omitted %q:\n%s", expected, arguments)
				}
			}
			if test.promptFlag != "" && !strings.Contains(arguments, test.promptFlag) {
				t.Fatalf("%s handoff omitted interactive prompt flag %q:\n%s", test.destination, test.promptFlag, arguments)
			}
			for _, forbidden := range []string{"source-transcript-secret", "source-prompt-secret", "source-environment-secret", "source-arguments-secret", "source-diff-secret", "must-not-be-transported"} {
				if strings.Contains(arguments, forbidden) {
					t.Fatalf("handoff transported forbidden %q:\n%s", forbidden, arguments)
				}
			}

			db, err := sql.Open("sqlite3", initialized.DatabasePath)
			if err != nil {
				t.Fatalf("open database: %v", err)
			}
			defer db.Close()
			var destinationID, payload string
			if err := db.QueryRow(`SELECT json_extract(payload_json, '$.session_id'), payload_json FROM events
				WHERE type = 'ai.session_started' AND json_extract(payload_json, '$.session_id') <> ? ORDER BY timestamp DESC LIMIT 1`, sourceID).Scan(&destinationID, &payload); err != nil {
				t.Fatalf("read destination session: %v", err)
			}
			if destinationID == sourceID || !strings.Contains(payload, `"predecessor_session_id":"`+sourceID+`"`) {
				t.Fatalf("destination lineage missing: %s", payload)
			}
			for _, forbidden := range []string{"Ship the handoff feature", "source-transcript-secret", "must-not-be-transported"} {
				if strings.Contains(payload, forbidden) {
					t.Fatalf("destination lifecycle persisted continuation data %q: %s", forbidden, payload)
				}
			}
			childContext, err := os.ReadFile(contextPath)
			if err != nil {
				t.Fatalf("read child context: %v", err)
			}
			parts := strings.Split(string(childContext), "\n")
			if len(parts) != 3 || parts[0] != destinationID || parts[1] != homeDir || parts[2] != initialized.DatabasePath {
				t.Fatalf("destination did not receive its own storage identity: %q", childContext)
			}
		})
	}
}

func TestAIHandoffCapturesCheckpointInsideWrappedSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	workingDir, err = filepath.EvalSymlinks(workingDir)
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	argsPath := filepath.Join(workingDir, "args.txt")
	stdinKindPath := filepath.Join(workingDir, "stdin-kind.txt")
	writeAIFactExecutable(t, workingDir, "codex", `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_ARGS"
if [ -t 0 ]; then echo terminal > "$WORKGRAPH_AI_TEST_STDIN_KIND"; else echo not-terminal > "$WORKGRAPH_AI_TEST_STDIN_KIND"; fi`)
	sourceID := "00000000-0000-4000-8000-000000000320"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", false)
	checkpointPath := filepath.Join(t.TempDir(), "checkpoint.json")
	if err := os.WriteFile(checkpointPath, []byte(`{"goal":"Fresh handoff goal","current_state":"Ready to switch","next_actions":["Continue in Codex"]}`), 0o600); err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}

	output, err := runWorkgraphInAIFactTerminal(t, workingDir, checkpointPath, []string{
		"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"WORKGRAPH_AI_SESSION_ID=" + sourceID,
		"WORKGRAPH_AI_HOME=" + homeDir,
		"WORKGRAPH_AI_DATABASE=" + initialized.DatabasePath,
		"WORKGRAPH_AI_TEST_ARGS=" + argsPath,
		"WORKGRAPH_AI_TEST_STDIN_KIND=" + stdinKindPath,
	}, "ai", "handoff", "codex", "--stdin", "--home", homeDir, "--database", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("nested handoff: %v\n%s", err, output)
	}

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var checkpoints int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_checkpointed' AND json_extract(payload_json, '$.session_id') = ?`, sourceID).Scan(&checkpoints); err != nil || checkpoints != 1 {
		t.Fatalf("expected one source checkpoint, count=%d err=%v", checkpoints, err)
	}
	var destinationPayload string
	if err := db.QueryRow(`SELECT payload_json FROM events WHERE type = 'ai.session_started' AND json_extract(payload_json, '$.session_id') <> ?`, sourceID).Scan(&destinationPayload); err != nil {
		t.Fatalf("read destination start: %v", err)
	}
	if !strings.Contains(destinationPayload, `"predecessor_session_id":"`+sourceID+`"`) {
		t.Fatalf("nested handoff did not link destination: %s", destinationPayload)
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil || !strings.Contains(string(argsBytes), "Fresh handoff goal") {
		t.Fatalf("fresh checkpoint not delivered: %v\n%s", err, argsBytes)
	}
	stdinKind, err := os.ReadFile(stdinKindPath)
	if err != nil || strings.TrimSpace(string(stdinKind)) != "terminal" {
		t.Fatalf("destination stdin was not the controlling terminal: %v %q", err, stdinKind)
	}
}

func TestAIHandoffStdinWithoutTerminalKeepsCheckpointAndDoesNotLaunch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	launchedPath := filepath.Join(workingDir, "launched.txt")
	writeAIFactExecutable(t, workingDir, "codex", `cat > "$WORKGRAPH_AI_TEST_LAUNCHED"`)
	sourceID := "00000000-0000-4000-8000-000000000321"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", false)

	command := exec.Command(workgraphFactsBinary,
		"ai", "handoff", "codex", "--from", sourceID, "--stdin", "--home", homeDir, "--database", initialized.DatabasePath,
	)
	command.Dir = workingDir
	command.Env = daemonCommandEnv([]string{
		"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"WORKGRAPH_AI_TEST_LAUNCHED=" + launchedPath,
	})
	detachFromControllingTerminal(command)
	command.Stdin = strings.NewReader(`{"goal":"Recorded before terminal check"}`)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "no controlling terminal") || !strings.Contains(string(output), "ai handoff codex --from "+sourceID) {
		t.Fatalf("expected actionable missing-terminal failure, got %v\n%s", err, output)
	}
	if _, err := os.Stat(launchedPath); !os.IsNotExist(err) {
		t.Fatalf("destination launched without a terminal: %v", err)
	}

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var checkpoints, starts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_checkpointed' AND json_extract(payload_json, '$.session_id') = ?`, sourceID).Scan(&checkpoints); err != nil || checkpoints != 1 {
		t.Fatalf("validated checkpoint was not kept on the source, count=%d err=%v", checkpoints, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_started'`).Scan(&starts); err != nil || starts != 1 {
		t.Fatalf("missing terminal created destination lifecycle, starts=%d err=%v", starts, err)
	}
}

func runWorkgraphInAIFactTerminal(t *testing.T, dir, stdinPath string, env []string, args ...string) (string, error) {
	t.Helper()
	scriptPath, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script is unavailable to allocate a terminal")
	}
	quote := func(value string) string {
		return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
	}
	quoted := []string{quote(workgraphFactsBinary)}
	for _, argument := range args {
		quoted = append(quoted, quote(argument))
	}
	wrapper := filepath.Join(t.TempDir(), "in-terminal.sh")
	contents := "#!/bin/sh\ncd " + quote(dir) + " || exit 97\nexec " + strings.Join(quoted, " ") + " < " + quote(stdinPath) + "\n"
	if err := os.WriteFile(wrapper, []byte(contents), 0o755); err != nil {
		t.Fatalf("write terminal wrapper: %v", err)
	}
	var command *exec.Cmd
	if runtime.GOOS == "linux" {
		command = exec.Command(scriptPath, "-qec", wrapper, "/dev/null")
	} else {
		command = exec.Command(scriptPath, "-q", "/dev/null", wrapper)
	}
	command.Env = daemonCommandEnv(env)
	output, err := command.CombinedOutput()
	if strings.Contains(string(output), "openpty") {
		t.Skipf("terminal allocation is unavailable: %s", output)
	}
	return string(output), err
}

func TestAIHandoffReusesValidCheckpointWithJSONEscapedCharacters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	workingDir, err = filepath.EvalSymlinks(workingDir)
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	argsPath := filepath.Join(workingDir, "args.txt")
	writeAIFactExecutable(t, workingDir, "codex", `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_ARGS"`)
	sourceID := "00000000-0000-4000-8000-000000000322"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", false)
	goal := strings.Repeat("<", 16*1024)
	checkpoint, err := workgraph.CheckpointAISession(workgraph.AICheckpointConfig{
		HomeDir: homeDir, DatabasePath: initialized.DatabasePath, SessionID: sourceID,
		WorkingDir: workingDir, Input: strings.NewReader(`{"goal":"` + goal + `"}`),
	})
	if err != nil {
		t.Fatalf("record valid escaped checkpoint: %v", err)
	}

	output, err := runWorkgraphCommandAllowError([]string{
		"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"WORKGRAPH_AI_TEST_ARGS=" + argsPath,
	}, "ai", "handoff", "codex", "--from", sourceID, "--home", homeDir, "--database", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("handoff escaped checkpoint: %v\n%s", err, output)
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read destination context: %v", err)
	}
	if !strings.Contains(string(argsBytes), goal) {
		t.Fatal("destination context unnecessarily expanded JSON-safe checkpoint text")
	}
	var context struct {
		Checkpoint map[string]any `json:"checkpoint"`
	}
	arguments := strings.Split(strings.TrimSpace(string(argsBytes)), "\n")
	if err := json.Unmarshal([]byte(arguments[len(arguments)-1]), &context); err != nil {
		t.Fatalf("decode destination context: %v", err)
	}
	if context.Checkpoint["goal"] != goal {
		t.Fatal("destination did not receive the complete escaped checkpoint")
	}
	if checkpoint.EventID == "" {
		t.Fatal("checkpoint append did not return an event id")
	}
}

func TestAIHandoffRejectsSensitiveStoredCheckpointWithoutLeakingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	writeAIFactExecutable(t, workingDir, "codex", "exit 0")
	sourceID := "00000000-0000-4000-8000-000000000323"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", true)
	insertAIHandoffCheckpoint(t, initialized.DatabasePath, sourceID, workingDir)
	credential := "sk-abcdefghijklmnopqrstuvwxyz123456"

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	var payloadText string
	checkpointID := "ai.session_checkpointed:" + sourceID + ":handoff"
	if err := db.QueryRow(`SELECT payload_json FROM events WHERE id = ?`, checkpointID).Scan(&payloadText); err != nil {
		db.Close()
		t.Fatalf("read checkpoint: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
		db.Close()
		t.Fatalf("decode checkpoint: %v", err)
	}
	payload["agent_stated"].(map[string]any)["goal"] = credential
	encoded, err := json.Marshal(payload)
	if err != nil {
		db.Close()
		t.Fatalf("encode sensitive checkpoint: %v", err)
	}
	if _, err := db.Exec(`UPDATE events SET payload_json = ? WHERE id = ?`, string(encoded), checkpointID); err != nil {
		db.Close()
		t.Fatalf("update sensitive checkpoint: %v", err)
	}
	db.Close()

	output, err := runWorkgraphCommandAllowError([]string{
		"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}, "ai", "handoff", "codex", "--from", sourceID, "--home", homeDir, "--database", initialized.DatabasePath)
	if err == nil || !strings.Contains(output, "openai-token credential pattern") {
		t.Fatalf("expected sensitive stored checkpoint rejection, got %v\n%s", err, output)
	}
	if strings.Contains(output, credential) {
		t.Fatal("sensitive checkpoint rejection leaked submitted content")
	}

	db, err = sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer db.Close()
	var starts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_started'`).Scan(&starts); err != nil || starts != 1 {
		t.Fatalf("sensitive checkpoint launched a destination, starts=%d err=%v", starts, err)
	}
}

func TestAIHandoffLineageRemainsInspectableAfterNativeBinding(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	writeAIFactExecutable(t, workingDir, "codex", "exit 0")
	sourceID := "00000000-0000-4000-8000-000000000325"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", true)
	insertAIHandoffCheckpoint(t, initialized.DatabasePath, sourceID, workingDir)

	output, err := runWorkgraphCommandAllowError([]string{
		"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}, "ai", "handoff", "codex", "--from", sourceID, "--home", homeDir, "--database", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("handoff to codex: %v\n%s", err, output)
	}

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var destinationID string
	if err := db.QueryRow(`SELECT json_extract(payload_json, '$.session_id') FROM events
		WHERE type = 'ai.session_started' AND json_extract(payload_json, '$.session_id') <> ?`, sourceID).Scan(&destinationID); err != nil {
		t.Fatalf("read destination session: %v", err)
	}
	conflictingPredecessorID := "00000000-0000-4000-8000-000000000999"
	insertAINativeFactSession(t, initialized.DatabasePath, conflictingPredecessorID, "codex", "/usr/local/bin/codex", "codex-destination-native", "", workingDir, "2026-08-03T12:03:00Z", true)
	bound, err := workgraph.BindAINativeSession(workgraph.AINativeSessionConfig{
		HomeDir: homeDir, DatabasePath: initialized.DatabasePath, SessionID: destinationID, Tool: "codex",
		Input: strings.NewReader(`{"session_id":"codex-destination-native","cwd":"` + workingDir + `","hook_event_name":"SessionStart","source":"startup"}`),
	})
	if err != nil {
		t.Fatalf("bind destination native session: %v", err)
	}
	var nativePayload string
	if err := db.QueryRow(`SELECT payload_json FROM events WHERE id = ?`, bound.EventID).Scan(&nativePayload); err != nil {
		t.Fatalf("read native binding: %v", err)
	}
	if !strings.Contains(nativePayload, `"predecessor_session_id":"`+conflictingPredecessorID+`"`) {
		t.Fatalf("native callback did not suggest the conflicting predecessor: %s", nativePayload)
	}

	shown, err := workgraph.ShowAISession(workgraph.AIShowConfig{
		HomeDir: homeDir, DatabasePath: initialized.DatabasePath, SessionID: destinationID,
	})
	if err != nil {
		t.Fatalf("show destination session: %v", err)
	}
	if shown.Session.PredecessorSessionID != sourceID || !strings.Contains(shown.Message, "Predecessor: "+sourceID) {
		t.Fatalf("native binding erased handoff lineage:\n%s", shown.Message)
	}
}

func TestAIHandoffFailuresDoNotCreateDestinationLineage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	sourceID := "00000000-0000-4000-8000-000000000330"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", true)
	insertAIHandoffCheckpoint(t, initialized.DatabasePath, sourceID, workingDir)
	noCheckpointID := "00000000-0000-4000-8000-000000000331"
	insertAINativeFactSession(t, initialized.DatabasePath, noCheckpointID, "claude", "/usr/local/bin/claude", "claude-native-empty", "", workingDir, "2026-08-03T12:02:00Z", true)
	otherHome := filepath.Join(t.TempDir(), ".workgraph")
	other, err := workgraph.Init(workgraph.InitConfig{HomeDir: otherHome})
	if err != nil {
		t.Fatalf("init other workgraph: %v", err)
	}

	tests := []struct {
		name string
		env  []string
		args []string
		want string
	}{
		{name: "unsupported destination", args: []string{"aider", "--from", sourceID}, want: `destination "aider" is unsupported`},
		{name: "missing source", args: []string{"codex", "--from", "missing"}, want: `AI session "missing" was not found`},
		{name: "missing checkpoint", args: []string{"codex", "--from", noCheckpointID}, want: "source session has no structured checkpoint"},
		{name: "conflicting injected source", env: []string{"WORKGRAPH_AI_SESSION_ID=another"}, args: []string{"codex", "--from", sourceID}, want: "explicit source session disagrees"},
		{name: "conflicting injected database", env: []string{"WORKGRAPH_AI_DATABASE=" + other.DatabasePath}, args: []string{"codex", "--from", sourceID}, want: "explicit database disagrees"},
	}
	invalidToolPath := filepath.Join(workingDir, "codex")
	if err := os.WriteFile(invalidToolPath, []byte("not an executable format"), 0o755); err != nil {
		t.Fatalf("write invalid destination: %v", err)
	}
	output, err := runWorkgraphCommandAllowError([]string{"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH")},
		"ai", "handoff", "codex", "--from", sourceID, "--home", homeDir, "--database", initialized.DatabasePath)
	if err == nil || !strings.Contains(output, "start AI tool") {
		t.Fatalf("expected destination start failure, got %v\n%s", err, output)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"ai", "handoff"}, test.args...)
			args = append(args, "--home", homeDir, "--database", initialized.DatabasePath)
			environment := append([]string{"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH")}, test.env...)
			output, err := runWorkgraphCommandAllowError(environment, args...)
			if err == nil || !strings.Contains(output, test.want) {
				t.Fatalf("expected %q, got %v\n%s", test.want, err, output)
			}
		})
	}

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var starts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_started'`).Scan(&starts); err != nil || starts != 2 {
		t.Fatalf("failed handoffs created a destination, starts=%d err=%v", starts, err)
	}
}

func TestAIHandoffFreshCheckpointSurvivesDestinationLaunchFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	workingDir, err = filepath.EvalSymlinks(workingDir)
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workingDir, "codex"), []byte("not an executable format"), 0o755); err != nil {
		t.Fatalf("write invalid destination: %v", err)
	}
	sourceID := "00000000-0000-4000-8000-000000000333"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", false)

	checkpointPath := filepath.Join(t.TempDir(), "checkpoint.json")
	if err := os.WriteFile(checkpointPath, []byte(`{"goal":"Checkpoint survives launch failure"}`), 0o600); err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}
	output, err := runWorkgraphInAIFactTerminal(t, workingDir, checkpointPath,
		[]string{"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH")},
		"ai", "handoff", "codex", "--from", sourceID, "--stdin", "--home", homeDir, "--database", initialized.DatabasePath,
	)
	if err == nil || !strings.Contains(output, "start AI tool") {
		t.Fatalf("expected destination launch failure, got %v\n%s", err, output)
	}

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var checkpoints, starts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_checkpointed' AND json_extract(payload_json, '$.session_id') = ?`, sourceID).Scan(&checkpoints); err != nil || checkpoints != 1 {
		t.Fatalf("fresh checkpoint was not preserved, count=%d err=%v", checkpoints, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_started'`).Scan(&starts); err != nil || starts != 1 {
		t.Fatalf("launch failure created destination lifecycle, starts=%d err=%v", starts, err)
	}
}

func TestAIHandoffRejectsAnOlderCheckpointWhenTheLatestIsUnsupported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for _, test := range []struct {
		name    string
		version string
	}{
		{name: "unsupported integer", version: `2`},
		{name: "string version", version: `"1"`},
		{name: "boolean version", version: `true`},
		{name: "object version", version: `{}`},
		{name: "array version", version: `[]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertAIHandoffRejectsUnsafeEvidence(t, "ai.session_checkpointed", test.version, "latest checkpoint is unsupported or invalid")
		})
	}
}

func TestAIHandoffRejectsOlderObservationWhenNewerEvidenceIsUnsupportedOrInvalid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for _, eventType := range []string{"ai.session_started", "ai.session_ended"} {
		t.Run(eventType, func(t *testing.T) {
			for _, test := range []struct {
				name    string
				version string
			}{
				{name: "unsupported integer", version: `2`},
				{name: "string version", version: `"1"`},
				{name: "boolean version", version: `true`},
				{name: "object version", version: `{}`},
				{name: "array version", version: `[]`},
			} {
				t.Run(test.name, func(t *testing.T) {
					assertAIHandoffRejectsUnsafeEvidence(t, eventType, test.version, "latest repository observation is unsupported or invalid")
				})
			}
		})
	}
}

func assertAIHandoffRejectsUnsafeEvidence(t *testing.T, eventType, schemaVersion, want string) {
	t.Helper()
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	writeAIFactExecutable(t, workingDir, "codex", `touch "$0.launched"`)
	sourceID := "00000000-0000-4000-8000-000000000335"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", true)
	insertAIHandoffCheckpoint(t, initialized.DatabasePath, sourceID, workingDir)

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	unsupported := `{"schema_version":` + schemaVersion + `,"session_id":"` + sourceID + `","agent_stated":{"goal":"must not be handed off"},"observation_status":"captured","observed":{"cwd":"must-not-be-used"}}`
	if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES ('opaque-newer-evidence', 'ai', ?, '2026-08-03T12:02:00Z', ?, '', '', 'Newer AI evidence', '2026-08-03T12:02:00Z')`,
		eventType, unsupported); err != nil {
		t.Fatalf("insert unsupported or invalid evidence: %v", err)
	}
	listed, err := workgraph.ListAISessions(workgraph.AISessionsConfig{
		HomeDir: homeDir, DatabasePath: initialized.DatabasePath,
	})
	if err != nil {
		t.Fatalf("list sessions with invalid newer evidence: %v", err)
	}
	if len(listed.Sessions) != 1 || listed.Known != 1 {
		t.Fatalf("session listing reconstructed identity or lost the source: %#v", listed.Sessions)
	}
	source := listed.Sessions[0]
	if source.SessionID != sourceID || source.Tool != "claude" || source.NativeSessionID != "claude-native" || source.Status != "ended" ||
		source.LatestCheckpointAt == nil || source.LatestCheckpointAt.UTC().Format(time.RFC3339) != "2026-08-03T12:00:30Z" || len(listed.Warnings) == 0 {
		t.Fatalf("session listing lost valid source context or integrity warnings: %#v", listed)
	}
	if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES ('ai.session_checkpointed:00000000-0000-4000-8000-000000000999:invalid', 'ai', 'ai.session_checkpointed',
		'2026-08-03T12:03:00Z', '{"schema_version":"invalid"}', '', '', 'Unassociated AI evidence', '2026-08-03T12:03:00Z')`); err != nil {
		t.Fatalf("insert unassociated invalid envelope: %v", err)
	}
	listed, err = workgraph.ListAISessions(workgraph.AISessionsConfig{
		HomeDir: homeDir, DatabasePath: initialized.DatabasePath,
	})
	if err != nil || len(listed.Sessions) != 1 || listed.Known != 1 || listed.Sessions[0].SessionID != sourceID {
		t.Fatalf("unassociated invalid envelope changed session identities: sessions=%#v err=%v", listed.Sessions, err)
	}
	const eventSnapshotQuery = `SELECT json_group_array(json_object(
		'id', id, 'source', source, 'type', type, 'timestamp', timestamp, 'payload_json', payload_json,
		'project', project, 'actor', actor, 'summary', summary, 'created_at', created_at))
		FROM (SELECT * FROM events WHERE source = 'ai' ORDER BY id)`
	var before string
	if err := db.QueryRow(eventSnapshotQuery).Scan(&before); err != nil {
		t.Fatalf("read source event history before handoff: %v", err)
	}

	output, err := runWorkgraphCommandAllowError([]string{
		"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}, "ai", "handoff", "codex", "--from", sourceID, "--home", homeDir, "--database", initialized.DatabasePath)
	if err == nil || !strings.Contains(output, want) {
		t.Errorf("expected unsafe newer evidence rejection containing %q, got %v\n%s", want, err, output)
	}
	if _, err := os.Stat(filepath.Join(workingDir, "codex.launched")); !os.IsNotExist(err) {
		t.Errorf("unsafe newer evidence launched the destination: stat error=%v", err)
	}
	var destinations int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE source = 'ai' AND
		(json_extract(payload_json, '$.session_id') <> ? OR json_extract(payload_json, '$.predecessor_session_id') IS NOT NULL)`, sourceID).Scan(&destinations); err != nil || destinations != 0 {
		t.Errorf("unsafe evidence created destination lifecycle or lineage, count=%d err=%v", destinations, err)
	}
	var after string
	if err := db.QueryRow(eventSnapshotQuery).Scan(&after); err != nil {
		t.Fatalf("read source event history after handoff: %v", err)
	}
	if after != before {
		t.Error("rejected handoff changed source history or appended destination events")
	}
}

func TestAIHandoffDoesNotAppendCheckpointToEndedSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	writeAIFactExecutable(t, workingDir, "codex", "exit 0")
	sourceID := "00000000-0000-4000-8000-000000000340"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", true)
	insertAIHandoffCheckpoint(t, initialized.DatabasePath, sourceID, workingDir)

	command := exec.Command(workgraphFactsBinary,
		"ai", "handoff", "codex", "--from", sourceID, "--stdin", "--home", homeDir, "--database", initialized.DatabasePath,
	)
	command.Env = daemonCommandEnv([]string{"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH")})
	command.Stdin = strings.NewReader(`{"goal":"must not append"}`)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "source AI session has ended; omit --stdin") {
		t.Fatalf("expected ended-source checkpoint refusal, got %v\n%s", err, output)
	}
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var checkpoints, starts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_checkpointed' AND json_extract(payload_json, '$.session_id') = ?`, sourceID).Scan(&checkpoints); err != nil || checkpoints != 1 {
		t.Fatalf("ended source checkpoint count changed, count=%d err=%v", checkpoints, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_started'`).Scan(&starts); err != nil || starts != 1 {
		t.Fatalf("ended-source failure launched destination, starts=%d err=%v", starts, err)
	}
}

func TestAIHandoffFreshCheckpointPreservesInvokingDirectoryBinding(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	sessionDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve session directory: %v", err)
	}
	writeAIFactExecutable(t, sessionDir, "codex", "exit 0")
	sourceID := "00000000-0000-4000-8000-000000000350"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", sessionDir, "2026-08-03T12:00:00Z", false)

	command := exec.Command(workgraphFactsBinary,
		"ai", "handoff", "codex", "--from", sourceID, "--stdin", "--home", homeDir, "--database", initialized.DatabasePath,
	)
	command.Dir = t.TempDir()
	command.Env = daemonCommandEnv([]string{"PATH=" + sessionDir + string(os.PathListSeparator) + os.Getenv("PATH")})
	command.Stdin = strings.NewReader(`{"goal":"must remain checkout-bound"}`)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "checkpoint directory is outside the non-Git session directory") {
		t.Fatalf("expected invoking-directory rejection, got %v\n%s", err, output)
	}

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var checkpoints, starts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_checkpointed' AND json_extract(payload_json, '$.session_id') = ?`, sourceID).Scan(&checkpoints); err != nil || checkpoints != 0 {
		t.Fatalf("outside-directory handoff appended checkpoint, count=%d err=%v", checkpoints, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_started'`).Scan(&starts); err != nil || starts != 1 {
		t.Fatalf("outside-directory handoff launched destination, starts=%d err=%v", starts, err)
	}
}

func insertAIHandoffCheckpoint(t *testing.T, databasePath, sessionID, workingDir string) {
	t.Helper()
	resolvedWorkingDir, err := filepath.EvalSymlinks(workingDir)
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	workingDir = resolvedWorkingDir
	db, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	payload := map[string]any{
		"schema_version": 1,
		"session_id":     sessionID,
		"observed": map[string]any{
			"observed_at": "2026-08-03T12:00:30Z", "cwd": workingDir,
			"worktree_root": workingDir, "git_common_dir": filepath.Join(workingDir, ".git"),
			"branch": "main", "head": "0123456789abcdef", "dirty_paths": []string{"changed.go"},
			"dirty_path_count": 1, "dirty_paths_truncated": false,
		},
		"agent_stated": map[string]any{
			"goal": "Ship the handoff feature", "current_state": "Implementation ready",
			"completed": []string{"Added facts"}, "next_actions": []string{"Run focused facts"},
			"blockers": []string{}, "decisions": []string{"Reuse session events"},
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode checkpoint: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES (?, 'ai', 'ai.session_checkpointed', '2026-08-03T12:00:30Z', ?, '', '', 'AI session checkpointed', '2026-08-03T12:00:30Z')`,
		"ai.session_checkpointed:"+sessionID+":handoff", string(encoded)); err != nil {
		t.Fatalf("insert checkpoint: %v", err)
	}
}

func injectForbiddenSourceFields(t *testing.T, databasePath, sessionID string) {
	t.Helper()
	db, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var payloadText string
	if err := db.QueryRow(`SELECT payload_json FROM events WHERE id = ?`, "ai.session_started:"+sessionID).Scan(&payloadText); err != nil {
		t.Fatalf("read source payload: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
		t.Fatalf("decode source payload: %v", err)
	}
	payload["transcript"] = "source-transcript-secret"
	payload["prompt"] = "source-prompt-secret"
	payload["environment"] = "source-environment-secret"
	payload["arguments"] = "source-arguments-secret"
	payload["diff"] = "source-diff-secret"
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode source payload: %v", err)
	}
	if _, err := db.Exec(`UPDATE events SET payload_json = ? WHERE id = ?`, string(encoded), "ai.session_started:"+sessionID); err != nil {
		t.Fatalf("update source payload: %v", err)
	}
}

func writeVersionedClaudeFixture(t *testing.T, body string) (string, string) {
	t.Helper()
	binDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve bin directory: %v", err)
	}
	versionsDir := filepath.Join(binDir, "versions")
	if err := os.Mkdir(versionsDir, 0o755); err != nil {
		t.Fatalf("create versions directory: %v", err)
	}
	versioned := writeAIFactExecutable(t, versionsDir, "2.1.302", body)
	launcher := filepath.Join(binDir, "claude")
	if err := os.Symlink(versioned, launcher); err != nil {
		t.Fatalf("link claude launcher: %v", err)
	}
	return binDir, launcher
}

func readAIStartedTool(t *testing.T, databasePath, sessionID string) (string, string) {
	t.Helper()
	db, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var tool, toolPath string
	if err := db.QueryRow(`SELECT json_extract(payload_json, '$.tool'), COALESCE(json_extract(payload_json, '$.tool_path'), '') FROM events
		WHERE type = 'ai.session_started' AND json_extract(payload_json, '$.session_id') = ?`, sessionID).Scan(&tool, &toolPath); err != nil {
		t.Fatalf("read started tool for %s: %v", sessionID, err)
	}
	return tool, toolPath
}

func latestAIStartedSessionID(t *testing.T, databasePath string) string {
	t.Helper()
	db, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var sessionID string
	if err := db.QueryRow(`SELECT json_extract(payload_json, '$.session_id') FROM events
		WHERE type = 'ai.session_started' ORDER BY timestamp DESC, rowid DESC LIMIT 1`).Scan(&sessionID); err != nil {
		t.Fatalf("read latest started session: %v", err)
	}
	return sessionID
}

func TestAIHandoffIdentifiesClaudeLaunchedThroughVersionedSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	argsPath := filepath.Join(workingDir, "args.txt")
	binDir, launcher := writeVersionedClaudeFixture(t, `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_ARGS"`)
	writeAIFactExecutable(t, binDir, "codex", `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_ARGS"`)
	environment := daemonCommandEnv([]string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"WORKGRAPH_AI_TEST_ARGS=" + argsPath,
	})

	run := exec.Command(workgraphFactsBinary, "ai", "run", "--home", homeDir, "--database", initialized.DatabasePath, "--", "claude")
	run.Dir = workingDir
	run.Env = environment
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("run versioned claude: %v\n%s", err, output)
	}
	sourceID := latestAIStartedSessionID(t, initialized.DatabasePath)
	if tool, toolPath := readAIStartedTool(t, initialized.DatabasePath, sourceID); tool != "claude" || toolPath != launcher {
		t.Fatalf("versioned claude recorded as tool=%q tool_path=%q, want claude at %q", tool, toolPath, launcher)
	}
	insertAIHandoffCheckpoint(t, initialized.DatabasePath, sourceID, workingDir)

	for _, destination := range []string{"codex", "claude"} {
		if err := os.Remove(argsPath); err != nil && !os.IsNotExist(err) {
			t.Fatalf("reset destination args: %v", err)
		}
		handoff := exec.Command(workgraphFactsBinary, "ai", "handoff", destination, "--from", sourceID, "--home", homeDir, "--database", initialized.DatabasePath)
		handoff.Dir = workingDir
		handoff.Env = environment
		if output, err := handoff.CombinedOutput(); err != nil {
			t.Fatalf("handoff from versioned claude to %s: %v\n%s", destination, err, output)
		}
		destinationID := latestAIStartedSessionID(t, initialized.DatabasePath)
		if tool, _ := readAIStartedTool(t, initialized.DatabasePath, destinationID); destinationID == sourceID || tool != destination {
			t.Fatalf("handoff to %s recorded destination %s as tool %q", destination, destinationID, tool)
		}
		argsBytes, err := os.ReadFile(argsPath)
		if err != nil {
			t.Fatalf("read %s destination args: %v", destination, err)
		}
		if !strings.Contains(string(argsBytes), "Ship the handoff feature") {
			t.Fatalf("%s destination did not receive continuation context:\n%s", destination, argsBytes)
		}
		if destination == "claude" && !strings.Contains(string(argsBytes), "--settings\n") {
			t.Fatalf("versioned claude destination skipped native lifecycle settings:\n%s", argsBytes)
		}
	}
}

func TestAIResumeLaunchesClaudeRecordedThroughVersionedSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	argsPath := filepath.Join(workingDir, "args.txt")
	_, launcher := writeVersionedClaudeFixture(t, `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_ARGS"`)
	nativeID := "41111111-1111-4111-8111-111111111111"
	sessionID := "00000000-0000-4000-8000-000000000360"
	insertAINativeFactSession(t, initialized.DatabasePath, sessionID, "claude", launcher, nativeID, "", workingDir, "2026-08-03T12:00:00Z", true)

	output, err := runWorkgraphCommandAllowError([]string{"WORKGRAPH_AI_TEST_ARGS=" + argsPath},
		"ai", "resume", "--home", homeDir, "--database", initialized.DatabasePath, sessionID,
	)
	if err != nil {
		t.Fatalf("resume versioned claude: %v\n%s", err, output)
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil || !strings.Contains(string(argsBytes), "--resume\n"+nativeID+"\n") {
		t.Fatalf("versioned claude did not receive native resume argv: %v\n%s", err, argsBytes)
	}
	resumedID := latestAIStartedSessionID(t, initialized.DatabasePath)
	if tool, toolPath := readAIStartedTool(t, initialized.DatabasePath, resumedID); resumedID == sessionID || tool != "claude" || toolPath != launcher {
		t.Fatalf("resumed versioned claude recorded tool=%q tool_path=%q", tool, toolPath)
	}
}

func TestAIHandoffAcceptsCheckpointedSourceFromAnyWrappedTool(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	argsPath := filepath.Join(workingDir, "args.txt")
	writeAIFactExecutable(t, workingDir, "codex", `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_ARGS"`)
	sourceID := "00000000-0000-4000-8000-000000000332"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "aider", "/usr/local/bin/aider", "", "", workingDir, "2026-08-03T12:04:00Z", true)
	insertAIHandoffCheckpoint(t, initialized.DatabasePath, sourceID, workingDir)

	output, err := runWorkgraphCommandAllowError([]string{
		"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"WORKGRAPH_AI_TEST_ARGS=" + argsPath,
	}, "ai", "handoff", "codex", "--from", sourceID, "--home", homeDir, "--database", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("handoff from aider source: %v\n%s", err, output)
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil || !strings.Contains(string(argsBytes), "Ship the handoff feature") {
		t.Fatalf("aider source checkpoint not delivered: %v\n%s", err, argsBytes)
	}
}
