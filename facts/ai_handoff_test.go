package facts

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

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
printf '%s\n%s\n%s' "$WORKGRAPH_AI_SESSION_ID" "$WORKGRAPH_AI_HOME" "$WORKGRAPH_AI_DATABASE" > "$WORKGRAPH_AI_TEST_CONTEXT"
`+aiHandoffDestinationRecorder)
			sourceID := fmt.Sprintf("00000000-0000-4000-8000-%012d", 300+index)
			insertAINativeFactSession(t, initialized.DatabasePath, sourceID, test.sourceTool, "/usr/local/bin/"+test.sourceTool, test.sourceTool+"-native", "", workingDir, "2026-08-03T12:00:00Z", true)
			insertAIHandoffCheckpoint(t, initialized.DatabasePath, sourceID, workingDir)
			injectForbiddenSourceFields(t, initialized.DatabasePath, sourceID)
			sourceHistory := readAISessionHistory(t, initialized.DatabasePath, sourceID)

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
			argumentLines := strings.Split(strings.TrimSuffix(arguments, "\n"), "\n")
			for _, argument := range argumentLines {
				if strings.Contains(argument, test.sourceTool+"-native") || argument == "resume" || strings.HasPrefix(argument, "--resume") {
					t.Fatalf("handoff selected the source native session %q:\n%s", argument, arguments)
				}
			}
			if test.promptFlag != "" && (len(argumentLines) < 2 || argumentLines[len(argumentLines)-2] != test.promptFlag) {
				t.Fatalf("continuation context did not follow %s:\n%s", test.promptFlag, arguments)
			}
			resolvedWorkingDir, err := filepath.EvalSymlinks(workingDir)
			if err != nil {
				t.Fatalf("resolve working directory: %v", err)
			}
			if launchDir := readAIFactFile(t, workingDir, "launch-dir.txt"); launchDir != resolvedWorkingDir {
				t.Fatalf("destination launched in %q, want the source's observed cwd %q", launchDir, resolvedWorkingDir)
			}
			var delivered map[string]any
			if err := json.Unmarshal([]byte(readAIFactFile(t, workingDir, "context.json")), &delivered); err != nil {
				t.Fatalf("decode delivered context: %v", err)
			}
			expected := map[string]any{
				"instruction":       "Continue from the structured Workgraph handoff context. Verify repository state before changing files.",
				"source_session_id": sourceID,
				"checkpoint": map[string]any{
					"goal": "Ship the handoff feature", "current_state": "Implementation ready",
					"completed": []any{"Added facts"}, "next_actions": []any{"Run focused facts"},
					"blockers": []any{}, "decisions": []any{"Reuse session events"},
				},
				"repository": map[string]any{
					"observed_at": "2026-08-03T12:00:30Z", "cwd": resolvedWorkingDir, "worktree_root": resolvedWorkingDir,
					"branch": "main", "head": "0123456789abcdef", "dirty_paths": []any{"changed.go"},
					"dirty_path_count": float64(1), "dirty_paths_truncated": false,
				},
			}
			if !reflect.DeepEqual(delivered, expected) {
				t.Fatalf("delivered context is not exactly the structured continuation:\n got %#v\nwant %#v", delivered, expected)
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
			if after := readAISessionHistory(t, initialized.DatabasePath, sourceID); after != sourceHistory {
				t.Fatalf("successful handoff changed source history:\nbefore %s\nafter  %s", sourceHistory, after)
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
	storageDir := filepath.Join(t.TempDir(), "custom work graph")
	homeDir := filepath.Join(storageDir, "home")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, DatabasePath: filepath.Join(storageDir, "data's", "events.db")})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	launchedPath := filepath.Join(workingDir, "launched.txt")
	writeAIFactExecutable(t, workingDir, "codex", `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_LAUNCHED"`)
	sourceID := "00000000-0000-4000-8000-000000000321"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", false)
	environment := daemonCommandEnv([]string{
		"PATH=" + filepath.Dir(workgraphFactsBinary) + string(os.PathListSeparator) + workingDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"WORKGRAPH_AI_TEST_LAUNCHED=" + launchedPath,
	})

	command := exec.Command(workgraphFactsBinary,
		"ai", "handoff", "codex", "--from", sourceID, "--stdin", "--home", homeDir, "--database", initialized.DatabasePath,
	)
	command.Dir = workingDir
	command.Env = environment
	detachFromControllingTerminal(command)
	command.Stdin = strings.NewReader(`{"goal":"Recorded before terminal check"}`)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "no controlling terminal") || !strings.Contains(string(output), "checkpoint was saved") || !strings.Contains(string(output), "without --stdin") {
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

	parts := strings.Split(string(output), "`")
	if len(parts) != 3 || !strings.HasPrefix(parts[1], "workgraph ai handoff codex --from "+sourceID) {
		t.Fatalf("missing-terminal failure did not suggest one handoff command:\n%s", output)
	}
	retry := exec.Command("/bin/sh", "-c", parts[1])
	retry.Dir = workingDir
	retry.Env = environment
	if retryOutput, err := retry.CombinedOutput(); err != nil {
		t.Fatalf("suggested retry %q failed: %v\n%s", parts[1], err, retryOutput)
	}
	launchedArgs, err := os.ReadFile(launchedPath)
	if err != nil || !strings.Contains(string(launchedArgs), "Recorded before terminal check") {
		t.Fatalf("suggested retry did not hand off the recorded checkpoint: %v\n%s", err, launchedArgs)
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
	const terminalUnavailable = "workgraph-fact-terminal-unavailable"
	wrapper := filepath.Join(t.TempDir(), "in-terminal.sh")
	contents := "#!/bin/sh\n" +
		"if ! { exec 3<>/dev/tty; } 2>/dev/null || ! [ -t 3 ]; then echo " + terminalUnavailable + "; exit 98; fi\n" +
		"exec 3>&-\n" +
		"cd " + quote(dir) + " || exit 97\n" +
		"exec " + strings.Join(quoted, " ") + " < " + quote(stdinPath) + "\n"
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
	if strings.Contains(string(output), "openpty") || strings.Contains(string(output), terminalUnavailable) {
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

	t.Setenv("PATH", workingDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WORKGRAPH_AI_TEST_ARGS", argsPath)
	result, err := workgraph.HandoffAISession(workgraph.AIHandoffConfig{
		HomeDir: homeDir, DatabasePath: initialized.DatabasePath, SourceSessionID: sourceID, Destination: "codex",
	})
	if err != nil {
		t.Fatalf("handoff escaped checkpoint: %v", err)
	}
	if !result.ReusedCheckpoint || result.CheckpointEventID != checkpoint.EventID {
		t.Fatalf("handoff did not identify reused checkpoint %s: %#v", checkpoint.EventID, result)
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
}

func TestAIHandoffRejectsSensitiveStoredCheckpointWithoutLeakingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for _, field := range []string{"goal", "branch", "cwd", "worktree_root", "dirty_paths"} {
		t.Run(field, func(t *testing.T) {
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
			if field == "goal" {
				payload["agent_stated"].(map[string]any)[field] = credential
			} else {
				value := filepath.Join(workingDir, credential)
				if err := os.MkdirAll(value, 0o755); err != nil {
					t.Fatal(err)
				}
				observed := payload["observed"].(map[string]any)
				if field == "dirty_paths" {
					paths := make([]string, 500)
					for i := range paths {
						paths[i] = strings.Repeat("safe-path-", 20)
					}
					observed[field] = append(paths, value)
				} else {
					observed[field] = value
				}
			}
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
			history := readAISessionHistory(t, initialized.DatabasePath, sourceID)

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
			if after := readAISessionHistory(t, initialized.DatabasePath, sourceID); after != history {
				t.Fatal("rejection changed source history")
			}
		})
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
		{name: "conflicting injected home", env: []string{"WORKGRAPH_AI_HOME=" + otherHome}, args: []string{"codex", "--from", sourceID}, want: "explicit home disagrees"},
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
			if test.name == "unsupported destination" {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
					t.Fatalf("unsupported destination must exit 2: %v\n%s", err, output)
				}
			}
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
	if err == nil || !strings.Contains(output, "start AI tool") || !strings.Contains(output, "checkpoint was saved") || !strings.Contains(output, "without --stdin") || !strings.Contains(output, "ai handoff codex --from "+sourceID) {
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
			assertAIHandoffRejectsUnsafeEvidence(t, "ai.session_checkpointed", test.version, aiUnsafeEvidenceFields, "latest checkpoint is unsupported or invalid")
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
					assertAIHandoffRejectsUnsafeEvidence(t, eventType, test.version, aiUnsafeEvidenceFields, "latest repository observation is unsupported or invalid")
				})
			}
		})
	}
}

var aiMistypedEvidenceCases = []struct {
	name      string
	eventType string
	fields    string
	want      string
}{
	{name: "start pid", eventType: "ai.session_started", fields: `"tool":"codex","pid":"not-a-number","observed":{"cwd":"must-not-be-used"}`, want: "latest repository observation is unsupported or invalid"},
	{name: "start observation", eventType: "ai.session_started", fields: `"tool":"codex","pid":42,"observed":{"cwd":["must-not-be-used"]}`, want: "latest repository observation is unsupported or invalid"},
	{name: "checkpoint agent stated", eventType: "ai.session_checkpointed", fields: `"agent_stated":["must not be handed off"],"observed":{"cwd":"must-not-be-used"}`, want: "latest checkpoint is unsupported or invalid"},
	{name: "checkpoint observation", eventType: "ai.session_checkpointed", fields: `"agent_stated":{"goal":"must not be handed off"},"observed":{"dirty_path_count":"many"}`, want: "latest checkpoint is unsupported or invalid"},
	{name: "end outcome", eventType: "ai.session_ended", fields: `"outcome":"exited","observation_status":"captured","observed":{"cwd":"must-not-be-used"}`, want: "latest repository observation is unsupported or invalid"},
}

func TestAIHandoffRejectsOlderEvidenceWhenNewerSupportedPayloadIsMistyped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for _, test := range aiMistypedEvidenceCases {
		t.Run(test.name, func(t *testing.T) {
			assertAIHandoffRejectsUnsafeEvidence(t, test.eventType, "1", test.fields, test.want)
		})
	}
}

const aiUnsafeEvidenceFields = `"agent_stated":{"goal":"must not be handed off"},"observation_status":"captured","observed":{"cwd":"must-not-be-used"}`

func assertAIHandoffRejectsUnsafeEvidence(t *testing.T, eventType, schemaVersion, fields, want string) {
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
	unsupported := `{"schema_version":` + schemaVersion + `,"session_id":"` + sourceID + `",` + fields + `}`
	if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES ('opaque-newer-evidence', 'ai', ?, '2026-08-03T12:02:00Z', ?, '', '', 'Newer AI evidence', '2026-08-03T12:02:00Z')`,
		eventType, unsupported); err != nil {
		t.Fatalf("insert unsupported or invalid evidence: %v", err)
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

const aiHandoffDestinationRecorder = `for last do :; done
printf '%s' "$last" > context.json
pwd -P > launch-dir.txt`

func readAIFactFile(t *testing.T, directory, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return strings.TrimSuffix(string(contents), "\n")
}

func readAISessionHistory(t *testing.T, databasePath, sessionID string) string {
	t.Helper()
	db, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var history string
	if err := db.QueryRow(`SELECT json_group_array(json_object(
		'id', id, 'source', source, 'type', type, 'timestamp', timestamp, 'payload_json', payload_json,
		'project', project, 'actor', actor, 'summary', summary, 'created_at', created_at))
		FROM (SELECT * FROM events WHERE json_extract(payload_json, '$.session_id') = ? ORDER BY id)`, sessionID).Scan(&history); err != nil {
		t.Fatalf("read session history: %v", err)
	}
	return history
}

func insertAIHandoffCheckpoint(t *testing.T, databasePath, sessionID, workingDir string) {
	t.Helper()
	insertAIHandoffCheckpointWith(t, databasePath, sessionID, workingDir, []string{"changed.go"}, map[string]any{
		"goal": "Ship the handoff feature", "current_state": "Implementation ready",
		"completed": []string{"Added facts"}, "next_actions": []string{"Run focused facts"},
		"blockers": []string{}, "decisions": []string{"Reuse session events"},
	})
}

func insertAIHandoffCheckpointWith(t *testing.T, databasePath, sessionID, workingDir string, dirtyPaths []string, agentStated map[string]any) {
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
			"branch": "main", "head": "0123456789abcdef", "dirty_paths": dirtyPaths,
			"dirty_path_count": len(dirtyPaths), "dirty_paths_truncated": false,
		},
		"agent_stated": agentStated,
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

func TestAIResumeRefusesLauncherRepointedAfterRun(t *testing.T) {
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
	impostorPath := filepath.Join(workingDir, "impostor.txt")
	binDir, launcher := writeVersionedClaudeFixture(t, `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_ARGS"`)
	environment := []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"WORKGRAPH_AI_TEST_ARGS=" + argsPath,
		"WORKGRAPH_AI_TEST_IMPOSTOR=" + impostorPath,
	}
	nativeID := "51111111-1111-4111-8111-111111111111"
	run := exec.Command(workgraphFactsBinary, "ai", "run", "--home", homeDir, "--database", initialized.DatabasePath, "--", "claude", "--resume", nativeID)
	run.Dir = workingDir
	run.Env = daemonCommandEnv(environment)
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("run versioned claude: %v\n%s", err, output)
	}
	sessionID := latestAIStartedSessionID(t, initialized.DatabasePath)

	if output, err := runWorkgraphCommandAllowError(environment, "ai", "resume", "--home", homeDir, "--database", initialized.DatabasePath, sessionID); err != nil {
		t.Fatalf("resume through unchanged launcher: %v\n%s", err, output)
	}
	if argsBytes, err := os.ReadFile(argsPath); err != nil || !strings.Contains(string(argsBytes), "--resume\n"+nativeID+"\n") {
		t.Fatalf("unchanged launcher did not resume natively: %v\n%s", err, argsBytes)
	}

	impostor := writeAIFactExecutable(t, t.TempDir(), "unrelated-tool", `echo launched > "$WORKGRAPH_AI_TEST_IMPOSTOR"`)
	if err := os.Remove(launcher); err != nil {
		t.Fatalf("remove launcher: %v", err)
	}
	if err := os.Symlink(impostor, launcher); err != nil {
		t.Fatalf("repoint launcher: %v", err)
	}
	startsBefore := countAIStartedSessions(t, initialized.DatabasePath)
	output, err := runWorkgraphCommandAllowError(environment, "ai", "resume", "--home", homeDir, "--database", initialized.DatabasePath, sessionID)
	if err == nil || !strings.Contains(output, "no longer resolves to the recorded executable") {
		t.Fatalf("expected repointed launcher refusal, got %v\n%s", err, output)
	}
	if _, err := os.Stat(impostorPath); !os.IsNotExist(err) {
		t.Fatalf("repointed launcher target was executed: %v", err)
	}
	if startsAfter := countAIStartedSessions(t, initialized.DatabasePath); startsAfter != startsBefore {
		t.Fatalf("refused resume recorded a new session, starts %d -> %d", startsBefore, startsAfter)
	}
}

func countAIStartedSessions(t *testing.T, databasePath string) int {
	t.Helper()
	db, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var starts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_started'`).Scan(&starts); err != nil {
		t.Fatalf("count started sessions: %v", err)
	}
	return starts
}

func TestAIHandoffReturnsDestinationExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	writeAIFactExecutable(t, workingDir, "codex", "exit 7")
	sourceID := "00000000-0000-4000-8000-000000000360"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", true)
	insertAIHandoffCheckpoint(t, initialized.DatabasePath, sourceID, workingDir)

	output, err := runWorkgraphCommandAllowError([]string{
		"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}, "ai", "handoff", "codex", "--from", sourceID, "--home", homeDir, "--database", initialized.DatabasePath)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("handoff did not return the destination exit code 7: %v\n%s", err, output)
	}

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var outcomeKind string
	var exitCode int
	if err := db.QueryRow(`SELECT json_extract(payload_json, '$.outcome.kind'), json_extract(payload_json, '$.outcome.exit_code') FROM events
		WHERE type = 'ai.session_ended' AND json_extract(payload_json, '$.session_id') <> ?`, sourceID).Scan(&outcomeKind, &exitCode); err != nil {
		t.Fatalf("read destination end: %v", err)
	}
	if outcomeKind != "exited" || exitCode != 7 {
		t.Fatalf("destination end recorded %s/%d, want exited/7", outcomeKind, exitCode)
	}
}

func TestAIHandoffTrimsDirtyPathsToFitTheCommandLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	writeAIFactExecutable(t, workingDir, "codex", aiHandoffDestinationRecorder)
	sourceID := "00000000-0000-4000-8000-000000000361"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", true)
	dirtyPaths := make([]string, 500)
	for index := range dirtyPaths {
		dirtyPaths[index] = fmt.Sprintf("src/%03d/\"quoted\"\\%s.go", index, strings.Repeat("p", 80))
	}
	insertAIHandoffCheckpointWith(t, initialized.DatabasePath, sourceID, workingDir, dirtyPaths, map[string]any{"goal": "Fit the command line"})
	sourceHistory := readAISessionHistory(t, initialized.DatabasePath, sourceID)

	output, err := runWorkgraphCommandAllowError([]string{
		"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}, "ai", "handoff", "codex", "--from", sourceID, "--home", homeDir, "--database", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("handoff with many dirty paths: %v\n%s", err, output)
	}
	encoded := readAIFactFile(t, workingDir, "context.json")
	if cost := aiWindowsArgumentCost(encoded); cost > 30*1024 {
		t.Fatalf("continuation context costs %d bytes on the command line, over 30720", cost)
	}
	var delivered struct {
		Checkpoint map[string]any `json:"checkpoint"`
		Repository struct {
			DirtyPaths          []string `json:"dirty_paths"`
			DirtyPathCount      int      `json:"dirty_path_count"`
			DirtyPathsTruncated bool     `json:"dirty_paths_truncated"`
		} `json:"repository"`
	}
	if err := json.Unmarshal([]byte(encoded), &delivered); err != nil {
		t.Fatalf("decode delivered context: %v", err)
	}
	repository := delivered.Repository
	if !repository.DirtyPathsTruncated || repository.DirtyPathCount != 500 || len(repository.DirtyPaths) == 0 || len(repository.DirtyPaths) >= 500 ||
		!reflect.DeepEqual(repository.DirtyPaths, dirtyPaths[:len(repository.DirtyPaths)]) || delivered.Checkpoint["goal"] != "Fit the command line" {
		t.Fatalf("dirty paths were not trimmed to a marked leading subset: truncated=%v count=%d kept=%d checkpoint=%v",
			repository.DirtyPathsTruncated, repository.DirtyPathCount, len(repository.DirtyPaths), delivered.Checkpoint)
	}
	if after := readAISessionHistory(t, initialized.DatabasePath, sourceID); after != sourceHistory {
		t.Fatal("trimming the continuation context changed source history")
	}
}

func TestAIHandoffRejectsCheckpointTooLargeForTheCommandLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	largeCheckpoint := map[string]any{
		"goal":          strings.Repeat("Keep the goal readable. ", 650),
		"current_state": strings.Repeat("The state is long. ", 800),
		"completed":     []string{strings.Repeat("Done step. ", 300)},
	}
	encodedCheckpoint, err := json.Marshal(largeCheckpoint)
	if err != nil {
		t.Fatalf("encode large checkpoint: %v", err)
	}
	for _, test := range []struct {
		name  string
		stdin bool
	}{
		{name: "stored checkpoint"},
		{name: "stdin checkpoint", stdin: true},
	} {
		t.Run(test.name, func(t *testing.T) {
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
			writeAIFactExecutable(t, workingDir, "codex", `touch "$WORKGRAPH_AI_TEST_LAUNCHED"`)
			sourceID := "00000000-0000-4000-8000-000000000362"
			insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", !test.stdin)
			if !test.stdin {
				insertAIHandoffCheckpointWith(t, initialized.DatabasePath, sourceID, workingDir, []string{"changed.go"}, largeCheckpoint)
			}
			sourceHistory := readAISessionHistory(t, initialized.DatabasePath, sourceID)

			args := []string{"ai", "handoff", "codex", "--from", sourceID, "--home", homeDir, "--database", initialized.DatabasePath}
			if test.stdin {
				args = append(args, "--stdin")
			}
			command := exec.Command(workgraphFactsBinary, args...)
			command.Dir = workingDir
			command.Env = daemonCommandEnv([]string{
				"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH"),
				"WORKGRAPH_AI_TEST_LAUNCHED=" + launchedPath,
			})
			detachFromControllingTerminal(command)
			command.Stdin = strings.NewReader(string(encodedCheckpoint))
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "bytes left on the destination command line") || !strings.Contains(string(output), "shorter checkpoint") {
				t.Fatalf("expected actionable context size rejection, got %v\n%s", err, output)
			}
			if _, err := os.Stat(launchedPath); !os.IsNotExist(err) {
				t.Fatalf("oversized context launched the destination: %v", err)
			}
			if test.stdin {
				for _, want := range []string{"checkpoint was saved", "without --stdin", "ai handoff codex --from " + sourceID, "--home " + homeDir, "--database " + initialized.DatabasePath} {
					if !strings.Contains(string(output), want) {
						t.Errorf("context rejection omitted %q: %s", want, output)
					}
				}
			}

			db, err := sql.Open("sqlite3", initialized.DatabasePath)
			if err != nil {
				t.Fatalf("open database: %v", err)
			}
			defer db.Close()
			var starts, checkpoints int
			if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_started'`).Scan(&starts); err != nil || starts != 1 {
				t.Fatalf("oversized context created destination lifecycle, starts=%d err=%v", starts, err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_checkpointed' AND json_extract(payload_json, '$.session_id') = ?`, sourceID).Scan(&checkpoints); err != nil || checkpoints != 1 {
				t.Fatalf("source checkpoint count = %d, want the one checkpoint kept, err=%v", checkpoints, err)
			}
			if after := readAISessionHistory(t, initialized.DatabasePath, sourceID); !test.stdin && after != sourceHistory {
				t.Fatal("rejected stored-checkpoint handoff changed source history")
			}
		})
	}
}

func TestAIHandoffStdinRefusesWhenALaterDatedCheckpointExists(t *testing.T) {
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
	writeAIFactExecutable(t, workingDir, "codex", `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_LAUNCHED"`)
	sourceID := "00000000-0000-4000-8000-000000000363"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", false)
	insertAIHandoffCheckpoint(t, initialized.DatabasePath, sourceID, workingDir)
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE events SET timestamp = '2099-01-01T00:00:00Z' WHERE id = ?`, "ai.session_checkpointed:"+sourceID+":handoff"); err != nil {
		t.Fatalf("future-date existing checkpoint: %v", err)
	}

	command := exec.Command(workgraphFactsBinary,
		"ai", "handoff", "codex", "--from", sourceID, "--stdin", "--home", homeDir, "--database", initialized.DatabasePath,
	)
	command.Dir = workingDir
	command.Env = daemonCommandEnv([]string{
		"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"WORKGRAPH_AI_TEST_LAUNCHED=" + launchedPath,
	})
	detachFromControllingTerminal(command)
	command.Stdin = strings.NewReader(`{"goal":"Freshly recorded goal"}`)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "checkpoint dated after the one --stdin just recorded") {
		t.Fatalf("expected later-dated checkpoint refusal, got %v\n%s", err, output)
	}
	if _, err := os.Stat(launchedPath); !os.IsNotExist(err) {
		t.Fatalf("destination launched with a checkpoint other than the one recorded: %v", err)
	}
	var checkpoints, starts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_checkpointed' AND json_extract(payload_json, '$.session_id') = ?`, sourceID).Scan(&checkpoints); err != nil || checkpoints != 2 {
		t.Fatalf("fresh checkpoint was not kept beside the existing one, count=%d err=%v", checkpoints, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_started'`).Scan(&starts); err != nil || starts != 1 {
		t.Fatalf("refused handoff created destination lifecycle, starts=%d err=%v", starts, err)
	}
}

func TestAIHandoffBudgetsContextAgainstTheFullWindowsCommandLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workgraphDir := longAIFactDirectory(t, 450)
	workgraphBinary, err := os.ReadFile(workgraphFactsBinary)
	if err != nil {
		t.Fatalf("read workgraph binary: %v", err)
	}
	workgraphPath := filepath.Join(workgraphDir, "workgraph")
	if err := os.WriteFile(workgraphPath, workgraphBinary, 0o755); err != nil {
		t.Fatalf("copy workgraph binary: %v", err)
	}
	toolDir := longAIFactDirectory(t, 450)
	codexPath := writeAIFactExecutable(t, toolDir, "codex", `printf '%s\0' "$@" > "$WORKGRAPH_AI_TEST_ARGS"`)
	workingDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	argsPath := filepath.Join(workingDir, "args.bin")
	sourceID := "00000000-0000-4000-8000-000000000364"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", true)
	dirtyPaths := make([]string, 500)
	for index := range dirtyPaths {
		dirtyPaths[index] = fmt.Sprintf("src/%03d/\"quoted\"\\%s.go", index, strings.Repeat("p", 80))
	}
	insertAIHandoffCheckpointWith(t, initialized.DatabasePath, sourceID, workingDir, dirtyPaths, map[string]any{"goal": "Fit the Windows command line"})

	command := exec.Command(workgraphPath, "ai", "handoff", "codex", "--from", sourceID, "--home", homeDir, "--database", initialized.DatabasePath)
	command.Env = daemonCommandEnv([]string{
		"PATH=" + toolDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"WORKGRAPH_AI_TEST_ARGS=" + argsPath,
	})
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("handoff with long executable paths: %v\n%s", err, output)
	}
	recorded, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read destination arguments: %v", err)
	}
	arguments := strings.Split(strings.TrimSuffix(string(recorded), "\x00"), "\x00")
	if !strings.Contains(strings.Join(arguments, "\n"), workgraphPath) {
		t.Fatalf("destination adapter arguments did not reference the long workgraph path:\n%s", strings.Join(arguments, "\n"))
	}
	if cost := aiWindowsCommandLineCost(append([]string{codexPath}, arguments...)); cost > 32767 {
		t.Fatalf("prepared destination command line costs %d UTF-16 units including NUL, over 32767", cost)
	}
	var delivered struct {
		Repository struct {
			DirtyPaths          []string `json:"dirty_paths"`
			DirtyPathsTruncated bool     `json:"dirty_paths_truncated"`
		} `json:"repository"`
	}
	if err := json.Unmarshal([]byte(arguments[len(arguments)-1]), &delivered); err != nil {
		t.Fatalf("decode delivered context: %v", err)
	}
	if !delivered.Repository.DirtyPathsTruncated || len(delivered.Repository.DirtyPaths) == 0 {
		t.Fatalf("expected a trimmed but nonempty dirty path list, truncated=%v kept=%d", delivered.Repository.DirtyPathsTruncated, len(delivered.Repository.DirtyPaths))
	}
}

func TestAIHandoffTruncationAlwaysDropsADirtyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	writeAIFactExecutable(t, workingDir, "codex", aiHandoffDestinationRecorder)
	handoff := func(sourceID, currentState string) map[string]any {
		t.Helper()
		insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", true)
		insertAIHandoffCheckpointWith(t, initialized.DatabasePath, sourceID, workingDir, []string{"changed.go"}, map[string]any{
			"goal": strings.Repeat("Reach the exact boundary. ", 600), "current_state": currentState,
		})
		output, err := runWorkgraphCommandAllowError([]string{
			"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		}, "ai", "handoff", "codex", "--from", sourceID, "--home", homeDir, "--database", initialized.DatabasePath)
		if err != nil {
			t.Fatalf("handoff: %v\n%s", err, output)
		}
		encoded := readAIFactFile(t, workingDir, "context.json")
		var delivered map[string]any
		if err := json.Unmarshal([]byte(encoded), &delivered); err != nil {
			t.Fatalf("decode delivered context: %v", err)
		}
		delivered["cost"] = aiWindowsArgumentCost(encoded)
		return delivered
	}

	calibration := handoff("00000000-0000-4000-8000-000000000365", "s")
	padding := 30*1024 + 1 - calibration["cost"].(int)
	if padding <= 0 {
		t.Fatalf("calibration context already costs %d", calibration["cost"])
	}
	boundary := handoff("00000000-0000-4000-8000-000000000366", strings.Repeat("s", padding+1))
	repository := boundary["repository"].(map[string]any)
	if repository["dirty_paths_truncated"] != true || len(repository["dirty_paths"].([]any)) != 0 || repository["dirty_path_count"] != float64(1) {
		t.Fatalf("a context one unit over the limit was marked truncated without dropping a path: %v", repository)
	}
}

func longAIFactDirectory(t *testing.T, length int) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temporary directory: %v", err)
	}
	for len(directory) < length {
		directory = filepath.Join(directory, strings.Repeat("d", min(60, max(1, length-len(directory)-1))))
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create long directory: %v", err)
	}
	return directory
}

func aiWindowsArgumentCost(argument string) int {
	return len(argument) + strings.Count(argument, `"`) + strings.Count(argument, `\`) + 2
}

func aiWindowsCommandLineCost(arguments []string) int {
	cost := len(arguments)
	for _, argument := range arguments {
		cost += aiWindowsArgumentCost(argument)
	}
	return cost
}

func TestAIHandoffAndCheckpointAcceptStandardFlagSpellings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for _, subcommand := range []string{"handoff", "checkpoint"} {
		for _, prefix := range []string{"-", "--"} {
			for _, equals := range []bool{false, true} {
				for _, flagsFirst := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/equals=%v/flagsFirst=%v", subcommand, prefix, equals, flagsFirst), func(t *testing.T) {
						homeDir := filepath.Join(t.TempDir(), ".workgraph")
						initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
						if err != nil {
							t.Fatal(err)
						}
						workingDir, err := filepath.EvalSymlinks(t.TempDir())
						if err != nil {
							t.Fatal(err)
						}
						writeAIFactExecutable(t, workingDir, "codex", "exit 0")
						sourceID := "00000000-0000-4000-8000-000000000370"
						insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", false)
						insertAIHandoffCheckpoint(t, initialized.DatabasePath, sourceID, workingDir)
						var options []string
						addOption := func(name, value string) {
							if equals {
								options = append(options, prefix+name+"="+value)
							} else {
								options = append(options, prefix+name, value)
							}
						}
						addOption("home", homeDir)
						addOption("database", initialized.DatabasePath)
						positional := sourceID
						if subcommand == "handoff" {
							positional = "codex"
							addOption("from", sourceID)
						} else {
							options = append(options, prefix+"stdin")
						}
						args := []string{"ai", subcommand}
						if flagsFirst {
							args = append(append(args, options...), positional)
						} else {
							args = append(append(args, positional), options...)
						}
						command := exec.Command(workgraphFactsBinary, args...)
						command.Dir = workingDir
						command.Env = daemonCommandEnv([]string{"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH")})
						command.Stdin = strings.NewReader(`{"goal":"Continue the selected session"}`)
						if output, err := command.CombinedOutput(); err != nil {
							t.Fatalf("standard flags failed: %v\n%s", err, output)
						}
						if subcommand == "handoff" && countAIStartedSessions(t, initialized.DatabasePath) != 2 {
							t.Fatal("handoff did not launch destination")
						}
					})
				}
			}
		}
	}
}

func TestAIHandoffRejectsUnorderableSessionEvidence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for _, eventType := range []string{"ai.session_started", "ai.session_checkpointed", "ai.session_ended", "ai.session_native_bound", "ai.session_archived", "ai.future_event"} {
		for _, stdin := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stdin=%v", eventType, stdin), func(t *testing.T) {
				homeDir := filepath.Join(t.TempDir(), ".workgraph")
				initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
				if err != nil {
					t.Fatal(err)
				}
				workingDir, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				writeAIFactExecutable(t, workingDir, "codex", `touch "$0.launched"`)
				sourceID := "00000000-0000-4000-8000-000000000371"
				insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", false)
				insertAIHandoffCheckpoint(t, initialized.DatabasePath, sourceID, workingDir)
				db, err := sql.Open("sqlite3", initialized.DatabasePath)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				payload := `{"schema_version":1,"session_id":"` + sourceID + `","agent_stated":{"goal":"Unorderable checkpoint"}}`
				if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at)
				VALUES ('unorderable', 'ai', ?, 'not-a-timestamp', ?, '', '', 'Unorderable evidence', '2026-08-03T12:02:00Z')`, eventType, payload); err != nil {
					t.Fatal(err)
				}
				history := readAISessionHistory(t, initialized.DatabasePath, sourceID)
				startsBefore := countAIStartedSessions(t, initialized.DatabasePath)
				args := []string{"ai", "handoff", "codex", "--from", sourceID, "--home", homeDir, "--database", initialized.DatabasePath}
				if stdin {
					args = append(args, "--stdin")
				}
				command := exec.Command(workgraphFactsBinary, args...)
				command.Dir = workingDir
				command.Env = daemonCommandEnv([]string{"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH")})
				command.Stdin = strings.NewReader(`{"goal":"Must not append"}`)
				detachFromControllingTerminal(command)
				output, err := command.CombinedOutput()
				if err == nil || !strings.Contains(string(output), "unorderable") || !strings.Contains(string(output), "timestamp") {
					t.Errorf("expected unorderable evidence rejection: %v\n%s", err, output)
				}
				if after := readAISessionHistory(t, initialized.DatabasePath, sourceID); after != history {
					t.Error("unorderable evidence allowed source changes")
				}
				if countAIStartedSessions(t, initialized.DatabasePath) != startsBefore {
					t.Error("unorderable evidence created destination lifecycle")
				}
				if _, err := os.Stat(filepath.Join(workingDir, "codex.launched")); !os.IsNotExist(err) {
					t.Errorf("destination launched: %v", err)
				}
				listed, err := workgraph.ListAISessions(workgraph.AISessionsConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath})
				if err != nil || len(listed.Warnings) == 0 {
					t.Errorf("unorderable evidence lost integrity warning: %#v %v", listed, err)
				}
			})
		}
	}
}

func TestAIHandoffDistinguishesNotionTokensFromRepositoryNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for _, field := range []string{"branch", "cwd", "worktree_root", "dirty_paths"} {
		for _, test := range []struct {
			name, value string
			reject      bool
		}{
			{name: "ordinary", value: "secret_rotation_for_service_accounts"},
			{name: "ordinary_file", value: "secret_management_helpers_test.go"},
			{name: "legacy_token", value: "secret_" + strings.Repeat("a1B", 14) + "x", reject: true},
			{name: "token", value: "ntn_" + strings.Repeat("b2C", 14) + "y", reject: true},
		} {
			t.Run(field+"/"+test.name, func(t *testing.T) {
				homeDir := filepath.Join(t.TempDir(), ".workgraph")
				initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
				if err != nil {
					t.Fatal(err)
				}
				workingDir := t.TempDir()
				writeAIFactExecutable(t, workingDir, "codex", aiHandoffDestinationRecorder)
				sourceID := "00000000-0000-4000-8000-000000000372"
				insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", false)
				insertAIHandoffCheckpoint(t, initialized.DatabasePath, sourceID, workingDir)
				value := filepath.Join(workingDir, test.value)
				if err := os.MkdirAll(value, 0o755); err != nil {
					t.Fatal(err)
				}
				if field == "branch" {
					value = "feat/" + test.value
				}
				if field == "dirty_paths" {
					value = "internal/" + test.value
				}
				db, err := sql.Open("sqlite3", initialized.DatabasePath)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				jsonPath := "$.observed." + field
				if field == "dirty_paths" {
					jsonPath += "[0]"
				}
				if _, err := db.Exec(`UPDATE events SET payload_json = json_set(payload_json, ?, ?) WHERE id = ?`, jsonPath, value, "ai.session_checkpointed:"+sourceID+":handoff"); err != nil {
					t.Fatal(err)
				}
				output, err := runWorkgraphCommandAllowError([]string{"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH")}, "ai", "handoff", "codex", "--from", sourceID, "--home", homeDir, "--database", initialized.DatabasePath)
				if test.reject {
					if err == nil || !strings.Contains(output, "notion-token credential pattern") {
						t.Fatalf("expected token rejection: %v %s", err, output)
					}
					if strings.Contains(output, test.value) {
						t.Fatal("rejection leaked token")
					}
					if countAIStartedSessions(t, initialized.DatabasePath) != 1 {
						t.Fatal("token launched destination")
					}
					return
				}
				if err != nil {
					t.Fatalf("ordinary repository name blocked handoff: %v %s", err, output)
				}
				launchDir := workingDir
				if field == "cwd" {
					launchDir = value
				}
				if context := readAIFactFile(t, launchDir, "context.json"); !strings.Contains(context, value) {
					t.Fatalf("repository name missing from context: %s", context)
				}
			})
		}
	}
}

func TestAIHandoffUsesLatestCheckpointWhateverItsInitiator(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	runWorkgraphCommand(t, nil, "settings", "enable-agent-checkpoints", "--home", homeDir)
	workingDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	argsPath := filepath.Join(workingDir, "args.txt")
	policyPath := filepath.Join(workingDir, "policy.txt")
	writeAIFactExecutable(t, workingDir, "codex", `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_ARGS"
printf '%s' "${WORKGRAPH_AI_CHECKPOINT_POLICY-unset}" > "$WORKGRAPH_AI_TEST_POLICY"`)
	sourceID := "00000000-0000-4000-8000-000000000323"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", false)
	if _, err := workgraph.CheckpointAISession(workgraph.AICheckpointConfig{
		HomeDir: homeDir, DatabasePath: initialized.DatabasePath, SessionID: sourceID,
		WorkingDir: workingDir, Input: strings.NewReader(`{"goal":"User requested state"}`),
	}); err != nil {
		t.Fatalf("record user checkpoint: %v", err)
	}
	t.Setenv("WORKGRAPH_AI_CHECKPOINT_POLICY", "agent")
	agentCheckpoint, err := workgraph.CheckpointAISession(workgraph.AICheckpointConfig{
		HomeDir: homeDir, DatabasePath: initialized.DatabasePath, SessionID: sourceID,
		WorkingDir: workingDir, AgentInitiated: true, Input: strings.NewReader(`{"goal":"Agent recorded state"}`),
	})
	if err != nil {
		t.Fatalf("record agent-initiated checkpoint: %v", err)
	}
	os.Unsetenv("WORKGRAPH_AI_CHECKPOINT_POLICY")

	t.Setenv("PATH", workingDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WORKGRAPH_AI_TEST_ARGS", argsPath)
	t.Setenv("WORKGRAPH_AI_TEST_POLICY", policyPath)
	result, err := workgraph.HandoffAISession(workgraph.AIHandoffConfig{
		HomeDir: homeDir, DatabasePath: initialized.DatabasePath, SourceSessionID: sourceID, Destination: "codex",
	})
	if err != nil {
		t.Fatalf("handoff agent-initiated checkpoint: %v", err)
	}
	if !result.ReusedCheckpoint || result.CheckpointEventID != agentCheckpoint.EventID {
		t.Fatalf("handoff did not reuse the latest agent-initiated checkpoint %s: %#v", agentCheckpoint.EventID, result)
	}
	if arguments := readAIFactFile(t, workingDir, "args.txt"); !strings.Contains(arguments, "Agent recorded state") || strings.Contains(arguments, "User requested state") || strings.Contains(arguments, "initiated_by") {
		t.Fatalf("destination did not receive only the latest checkpoint text:\n%s", arguments)
	}
	if policy := readAIFactFile(t, workingDir, "policy.txt"); policy != "agent" {
		t.Fatalf("handoff destination checkpoint policy = %q, want agent", policy)
	}
}
