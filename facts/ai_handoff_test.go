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
	writeAIFactExecutable(t, workingDir, "codex", `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_ARGS"`)
	sourceID := "00000000-0000-4000-8000-000000000320"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", false)

	command := exec.Command(workgraphFactsBinary,
		"ai", "handoff", "codex", "--stdin", "--home", homeDir, "--database", initialized.DatabasePath,
	)
	command.Dir = workingDir
	command.Env = daemonCommandEnv([]string{
		"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"WORKGRAPH_AI_SESSION_ID=" + sourceID,
		"WORKGRAPH_AI_HOME=" + homeDir,
		"WORKGRAPH_AI_DATABASE=" + initialized.DatabasePath,
		"WORKGRAPH_AI_TEST_ARGS=" + argsPath,
	})
	command.Stdin = strings.NewReader(`{"goal":"Fresh handoff goal","current_state":"Ready to switch","next_actions":["Continue in Codex"]}`)
	output, err := command.CombinedOutput()
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
	var destinationID, destinationStartedAtText string
	if err := db.QueryRow(`SELECT json_extract(payload_json, '$.session_id'), timestamp FROM events
		WHERE type = 'ai.session_started' AND json_extract(payload_json, '$.session_id') <> ?`, sourceID).Scan(&destinationID, &destinationStartedAtText); err != nil {
		t.Fatalf("read destination session: %v", err)
	}
	destinationStartedAt, err := time.Parse(time.RFC3339Nano, destinationStartedAtText)
	if err != nil {
		t.Fatalf("parse destination start time: %v", err)
	}
	nativeBoundAt := destinationStartedAt.Add(time.Nanosecond).Format(time.RFC3339Nano)
	nativePayload, err := json.Marshal(map[string]any{
		"schema_version":    1,
		"session_id":        destinationID,
		"tool":              "codex",
		"native_session_id": "codex-destination-native",
		"source":            "startup",
	})
	if err != nil {
		t.Fatalf("encode native binding: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES (?, 'ai', 'ai.session_native_bound', ?, ?, '', '', 'AI session native identity bound (codex)', ?)`,
		"ai.session_native_bound:"+destinationID+":handoff", nativeBoundAt, string(nativePayload), nativeBoundAt); err != nil {
		t.Fatalf("insert native binding: %v", err)
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
	unsupportedSourceID := "00000000-0000-4000-8000-000000000332"
	insertAINativeFactSession(t, initialized.DatabasePath, unsupportedSourceID, "aider", "/usr/local/bin/aider", "aider-native", "", workingDir, "2026-08-03T12:04:00Z", true)
	insertAIHandoffCheckpoint(t, initialized.DatabasePath, unsupportedSourceID, workingDir)
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
		{name: "unsupported source", args: []string{"codex", "--from", unsupportedSourceID}, want: `source AI tool "aider" is unsupported`},
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
			output, err := runWorkgraphCommandAllowError(test.env, args...)
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
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_started'`).Scan(&starts); err != nil || starts != 3 {
		t.Fatalf("failed handoffs created a destination, starts=%d err=%v", starts, err)
	}
}

func TestAIHandoffRejectsAnOlderCheckpointWhenTheLatestIsUnsupported(t *testing.T) {
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
	sourceID := "00000000-0000-4000-8000-000000000335"
	insertAINativeFactSession(t, initialized.DatabasePath, sourceID, "claude", "/usr/local/bin/claude", "claude-native", "", workingDir, "2026-08-03T12:00:00Z", true)
	insertAIHandoffCheckpoint(t, initialized.DatabasePath, sourceID, workingDir)

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	unsupported := `{"schema_version":2,"session_id":"` + sourceID + `","agent_stated":{"goal":"must not be handed off"}}`
	if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES (?, 'ai', 'ai.session_checkpointed', '2026-08-03T12:00:45Z', ?, '', '', 'AI session checkpointed', '2026-08-03T12:00:45Z')`,
		"ai.session_checkpointed:"+sourceID+":unsupported", unsupported); err != nil {
		db.Close()
		t.Fatalf("insert unsupported checkpoint: %v", err)
	}
	db.Close()

	output, err := runWorkgraphCommandAllowError([]string{
		"PATH=" + workingDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}, "ai", "handoff", "codex", "--from", sourceID, "--home", homeDir, "--database", initialized.DatabasePath)
	if err == nil || !strings.Contains(output, "latest checkpoint is unsupported or invalid") {
		t.Fatalf("expected unsafe stored checkpoint to fail, got %v\n%s", err, output)
	}

	db, err = sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer db.Close()
	var starts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_started'`).Scan(&starts); err != nil || starts != 1 {
		t.Fatalf("unsupported checkpoint launched a destination, starts=%d err=%v", starts, err)
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
