package facts

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
	_ "github.com/mattn/go-sqlite3"
)

func TestAIRunRecordsOneLocalChildLifetimeWithoutArguments(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}

	output, err := runWorkgraphCommandAllowError([]string{"AI_FACT_SECRET=must-not-be-stored"},
		"ai", "run",
		"--home", homeDir,
		"--database", initialized.DatabasePath,
		"--", workgraphFactsBinary, "doctor", "--home", homeDir,
	)
	if err != nil {
		t.Fatalf("run wrapped child: %v\n%s", err, output)
	}

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT type, COALESCE(actor, ''), summary, payload_json FROM events WHERE source = 'ai' ORDER BY timestamp, id`)
	if err != nil {
		t.Fatalf("query AI events: %v", err)
	}
	defer rows.Close()

	type storedEvent struct {
		typeName string
		actor    string
		summary  string
		payload  string
	}
	var events []storedEvent
	for rows.Next() {
		var event storedEvent
		if err := rows.Scan(&event.typeName, &event.actor, &event.summary, &event.payload); err != nil {
			t.Fatalf("scan AI event: %v", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate AI events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected start and end events, got %#v", events)
	}
	if events[0].typeName != "ai.session_started" || events[1].typeName != "ai.session_ended" {
		t.Fatalf("expected start then end events, got %#v", events)
	}
	if events[0].actor != "" || events[1].actor != "" {
		t.Fatalf("expected empty AI event actors, got %#v", events)
	}
	if events[0].summary != "AI session started (workgraph)" || events[1].summary != "AI session ended (exit 0)" {
		t.Fatalf("unexpected deterministic summaries: %#v", events)
	}

	var started struct {
		SchemaVersion int    `json:"schema_version"`
		SessionID     string `json:"session_id"`
		Tool          string `json:"tool"`
		ToolPath      string `json:"tool_path"`
		PID           int    `json:"pid"`
		Observed      struct {
			CWD          string   `json:"cwd"`
			WorktreeRoot string   `json:"worktree_root"`
			GitCommonDir string   `json:"git_common_dir"`
			DirtyPaths   []string `json:"dirty_paths"`
		} `json:"observed"`
	}
	if err := json.Unmarshal([]byte(events[0].payload), &started); err != nil {
		t.Fatalf("decode start payload: %v", err)
	}
	if started.SchemaVersion != 1 || started.SessionID == "" || started.Tool != "workgraph" || filepath.Base(started.ToolPath) != "workgraph" || !filepath.IsAbs(started.ToolPath) || started.PID <= 0 {
		t.Fatalf("unexpected start payload: %#v", started)
	}
	if started.Observed.CWD == "" || started.Observed.WorktreeRoot == "" || started.Observed.GitCommonDir == "" {
		t.Fatalf("expected observed Git identity, got %#v", started.Observed)
	}

	var ended struct {
		SessionID string `json:"session_id"`
		Outcome   struct {
			Kind     string `json:"kind"`
			ExitCode int    `json:"exit_code"`
		} `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(events[1].payload), &ended); err != nil {
		t.Fatalf("decode end payload: %v", err)
	}
	if ended.SessionID != started.SessionID || ended.Outcome.Kind != "exited" || ended.Outcome.ExitCode != 0 {
		t.Fatalf("unexpected end payload: %#v", ended)
	}

	allPayloads := events[0].payload + events[1].payload
	if strings.Contains(allPayloads, `"doctor"`) || strings.Contains(allPayloads, "must-not-be-stored") {
		t.Fatalf("AI lifecycle payload persisted child arguments: %s", allPayloads)
	}
}

func TestAICheckpointStrictInputContract(t *testing.T) {
	oversized := bytes.Repeat([]byte(" "), 65537)
	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{name: "oversized", input: oversized, want: "exceeds 65,536 bytes"},
		{name: "invalid UTF-8", input: []byte{'{', '"', 'g', 'o', 'a', 'l', '"', ':', '"', 0xff, '"', '}'}, want: "valid UTF-8"},
		{name: "trailing JSON", input: []byte(`{"goal":"valid"}{"goal":"second"}`), want: "trailing JSON data"},
		{name: "unknown observed field", input: []byte(`{"branch":"main"}`), want: `field "branch" is unknown`},
		{name: "null", input: []byte(`{"goal":null}`), want: `field "goal" may not be null`},
		{name: "empty", input: []byte(`{"blockers":[]}`), want: "meaningful text"},
		{name: "control", input: []byte(`{"goal":"bad\u001bterminal"}`), want: "control character"},
		{name: "credential", input: []byte(`{"goal":"sk-abcdefghijklmnopqrstuvwxyz123456"}`), want: "openai-token credential pattern"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := workgraph.CheckpointAISession(workgraph.AICheckpointConfig{
				SessionID: "missing-session", Input: bytes.NewReader(test.input),
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q validation error, got %v", test.want, err)
			}
			if strings.Contains(err.Error(), "abcdefghijklmnopqrstuvwxyz123456") {
				t.Fatalf("validation error echoed credential material: %v", err)
			}
		})
	}
}

func TestAICheckpointRejectsDuplicateFieldsWithoutPersistence(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	checkpoint := `{"goal":"first value","goal":"second value"}`
	command := exec.Command(workgraphFactsBinary,
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath,
		"--", workgraphFactsBinary, "ai", "checkpoint", "--stdin",
	)
	command.Dir = repoRootForDaemon()
	command.Env = daemonCommandEnv(nil)
	command.Stdin = strings.NewReader(checkpoint)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("expected duplicate checkpoint field to fail, got:\n%s", output)
	}
	if !strings.Contains(string(output), `field "goal" is duplicated`) || strings.Contains(string(output), "first value") || strings.Contains(string(output), "second value") {
		t.Fatalf("expected secret-free duplicate-field error, got:\n%s", output)
	}
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_checkpointed'`).Scan(&count); err != nil {
		t.Fatalf("count checkpoint events: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no checkpoint event, got %d", count)
	}
}

func TestAICheckpointRejectsStorageThatConflictsWithInjectedSession(t *testing.T) {
	primaryHome := filepath.Join(t.TempDir(), "primary")
	primary, err := workgraph.Init(workgraph.InitConfig{HomeDir: primaryHome})
	if err != nil {
		t.Fatalf("init primary workgraph: %v", err)
	}
	secondaryHome := filepath.Join(t.TempDir(), "secondary")
	secondary, err := workgraph.Init(workgraph.InitConfig{HomeDir: secondaryHome})
	if err != nil {
		t.Fatalf("init secondary workgraph: %v", err)
	}
	command := exec.Command(workgraphFactsBinary,
		"ai", "run", "--home", primaryHome, "--database", primary.DatabasePath,
		"--", workgraphFactsBinary, "ai", "checkpoint", "--database", secondary.DatabasePath, "--stdin",
	)
	command.Dir = repoRootForDaemon()
	command.Env = daemonCommandEnv(nil)
	command.Stdin = strings.NewReader(`{"goal":"Must stay in the primary store"}`)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("expected conflicting checkpoint storage to fail, got:\n%s", output)
	}
	if !strings.Contains(string(output), "explicit database disagrees with WORKGRAPH_AI_DATABASE") {
		t.Fatalf("expected storage disagreement error, got:\n%s", output)
	}
	db, err := sql.Open("sqlite3", secondary.DatabasePath)
	if err != nil {
		t.Fatalf("open secondary database: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE source = 'ai'`).Scan(&count); err != nil {
		t.Fatalf("count secondary AI events: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected secondary database to remain unchanged, got %d AI events", count)
	}
}

func TestAIStatusDerivationUsesConservativeProcessEvidence(t *testing.T) {
	tests := []struct {
		name      string
		inspector aiFactProcessInspector
		ended     bool
		want      string
	}{
		{name: "ended precedence", inspector: aiFactProcessInspector{err: errors.New("must not inspect")}, ended: true, want: "ended"},
		{name: "boot changed", inspector: aiFactProcessInspector{boot: "boot-b", process: workgraph.AIProcessInspection{Exists: true, StartIdentity: "start-a"}}, want: "interrupted"},
		{name: "pid absent", inspector: aiFactProcessInspector{boot: "boot-a", process: workgraph.AIProcessInspection{Exists: false}}, want: "interrupted"},
		{name: "identity matches", inspector: aiFactProcessInspector{boot: "boot-a", process: workgraph.AIProcessInspection{Exists: true, StartIdentity: "start-a"}}, want: "running"},
		{name: "pid reused", inspector: aiFactProcessInspector{boot: "boot-a", process: workgraph.AIProcessInspection{Exists: true, StartIdentity: "start-b"}}, want: "interrupted"},
		{name: "inspection unavailable", inspector: aiFactProcessInspector{boot: "boot-a", err: errors.New("permission denied")}, want: "unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			homeDir := filepath.Join(t.TempDir(), ".workgraph")
			initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
			if err != nil {
				t.Fatalf("init workgraph: %v", err)
			}
			insertAIFactSession(t, initialized.DatabasePath, test.ended)
			result, err := workgraph.ListAISessions(workgraph.AISessionsConfig{
				HomeDir: homeDir, DatabasePath: initialized.DatabasePath, ProcessInspector: test.inspector, Location: time.UTC,
			})
			if err != nil {
				t.Fatalf("list AI sessions: %v", err)
			}
			if len(result.Sessions) != 1 || result.Sessions[0].Status != test.want {
				t.Fatalf("expected status %q, got %#v", test.want, result.Sessions)
			}
		})
	}
}

func TestAIRunPreservesSignalExitStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix signal outcome")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	output, err := runWorkgraphCommandAllowError(nil,
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath,
		"--", "sh", "-c", "kill -TERM $$",
	)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 143 {
		t.Fatalf("expected wrapper exit 143, got %v\n%s", err, output)
	}
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	var summary string
	if err := db.QueryRow(`SELECT summary FROM events WHERE type = 'ai.session_ended'`).Scan(&summary); err != nil {
		t.Fatalf("read end event: %v", err)
	}
	if summary != "AI session ended (signal SIGTERM)" {
		t.Fatalf("unexpected signal summary: %q", summary)
	}
}

func TestAINonGitSessionKeepsEmptyGitIdentity(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	command := exec.Command(workgraphFactsBinary,
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath,
		"--", workgraphFactsBinary, "doctor", "--home", homeDir,
	)
	command.Dir = workingDir
	command.Env = daemonCommandEnv(nil)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run non-Git AI session: %v\n%s", err, output)
	}
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	var project, payloadText string
	if err := db.QueryRow(`SELECT COALESCE(project, ''), payload_json FROM events WHERE type = 'ai.session_started'`).Scan(&project, &payloadText); err != nil {
		t.Fatalf("read non-Git start: %v", err)
	}
	var payload struct {
		Observed workgraph.AIObservedState `json:"observed"`
	}
	if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
		t.Fatalf("decode non-Git start: %v", err)
	}
	canonicalWorkingDir, err := filepath.EvalSymlinks(workingDir)
	if err != nil {
		t.Fatalf("canonicalize non-Git working directory: %v", err)
	}
	if project != "" || payload.Observed.CWD != canonicalWorkingDir || payload.Observed.WorktreeRoot != "" || payload.Observed.GitCommonDir != "" || payload.Observed.Branch != "" || payload.Observed.Head != "" || len(payload.Observed.DirtyPaths) != 0 {
		t.Fatalf("unexpected non-Git identity: project=%q observed=%#v", project, payload.Observed)
	}
}

func TestAIRunRejectsNestedSessionBeforeLaunchingChild(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	output, err := runWorkgraphCommandAllowError([]string{"WORKGRAPH_AI_SESSION_ID=outer-session"},
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath,
		"--", workgraphFactsBinary, "doctor", "--home", homeDir,
	)
	if err == nil || !strings.Contains(output, "nested AI sessions are not supported") {
		t.Fatalf("expected nested session rejection, got %v\n%s", err, output)
	}
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE source = 'ai'`).Scan(&count); err != nil {
		t.Fatalf("count AI events: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no nested events, got %d", count)
	}
}

func TestAIRunAbortsBeforeChildWhenLaunchObservationFails(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	childHome := filepath.Join(t.TempDir(), "child-must-not-run")
	emptyPath := t.TempDir()
	output, err := runWorkgraphCommandAllowError([]string{"PATH=" + emptyPath},
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath,
		"--", workgraphFactsBinary, "init", "--home", childHome,
	)
	if err == nil || !strings.Contains(output, "collect AI launch observation") {
		t.Fatalf("expected launch observation failure, got %v\n%s", err, output)
	}
	if _, err := os.Stat(childHome); !os.IsNotExist(err) {
		t.Fatalf("expected child not to run, stat error: %v", err)
	}
}

func TestAIRunTerminatesChildWhenStartPersistenceFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper process")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	installAIInsertFailure(t, initialized.DatabasePath, "ai.session_started")
	marker := filepath.Join(t.TempDir(), "child-finished")
	output, err := runWorkgraphCommandAllowError(nil,
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath,
		"--", "sh", "-c", "sleep 2; touch \"$1\"", "workgraph-ai-test", marker,
	)
	if err == nil || !strings.Contains(output, "persist AI session start") {
		t.Fatalf("expected start persistence failure, got %v\n%s", err, output)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("expected failed start to terminate child, stat error: %v", err)
	}
}

func TestAIRunEndPersistenceFailurePreservesChildExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper process")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	installAIInsertFailure(t, initialized.DatabasePath, "ai.session_ended")
	output, err := runWorkgraphCommandAllowError(nil,
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath,
		"--", "sh", "-c", "exit 7",
	)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("expected child exit 7 despite end persistence failure, got %v\n%s", err, output)
	}
	if !strings.Contains(output, "persist AI session end") {
		t.Fatalf("expected visible end persistence error, got:\n%s", output)
	}
}

func TestAICheckpointRejectsAnotherGitCheckout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper process")
	}
	firstCheckout := initAIFactGitRepo(t)
	secondCheckout := initAIFactGitRepo(t)
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	wrapper := exec.Command(workgraphFactsBinary,
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath,
		"--", "sh", "-c", "sleep 1",
	)
	wrapper.Dir = firstCheckout
	wrapper.Env = daemonCommandEnv(nil)
	var wrapperOutput bytes.Buffer
	wrapper.Stdout = &wrapperOutput
	wrapper.Stderr = &wrapperOutput
	if err := wrapper.Start(); err != nil {
		t.Fatalf("start wrapped session: %v", err)
	}
	sessionID := waitForAIFactSession(t, initialized.DatabasePath)
	_, checkpointErr := workgraph.CheckpointAISession(workgraph.AICheckpointConfig{
		HomeDir: homeDir, DatabasePath: initialized.DatabasePath, SessionID: sessionID,
		WorkingDir: secondCheckout, Input: strings.NewReader(`{"goal":"Wrong checkout"}`),
	})
	if checkpointErr == nil || !strings.Contains(checkpointErr.Error(), "checkout differs") {
		t.Fatalf("expected checkout binding rejection, got %v", checkpointErr)
	}
	if err := wrapper.Wait(); err != nil {
		t.Fatalf("wait for wrapped session: %v\n%s", err, wrapperOutput.String())
	}
}

func TestAIObservedDirtyPathsAreBoundedAndDeterministic(t *testing.T) {
	checkout := initAIFactGitRepo(t)
	for index := 500; index >= 0; index-- {
		name := filepath.Join(checkout, "dirty", fmt.Sprintf("file-%03d.txt", index))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatalf("create dirty directory: %v", err)
		}
		if err := os.WriteFile(name, []byte("dirty"), 0o644); err != nil {
			t.Fatalf("write dirty file: %v", err)
		}
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	command := exec.Command(workgraphFactsBinary,
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath,
		"--", workgraphFactsBinary, "doctor", "--home", homeDir,
	)
	command.Dir = checkout
	command.Env = daemonCommandEnv(nil)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("record dirty observation: %v\n%s", err, output)
	}
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	var payloadText string
	if err := db.QueryRow(`SELECT payload_json FROM events WHERE type = 'ai.session_started'`).Scan(&payloadText); err != nil {
		t.Fatalf("read start event: %v", err)
	}
	var payload struct {
		Observed workgraph.AIObservedState `json:"observed"`
	}
	if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
		t.Fatalf("decode start event: %v", err)
	}
	if payload.Observed.DirtyPathCount != 501 || len(payload.Observed.DirtyPaths) != 500 || !payload.Observed.DirtyPathsTruncated || !sort.StringsAreSorted(payload.Observed.DirtyPaths) {
		t.Fatalf("unexpected bounded dirty paths: count=%d stored=%d truncated=%v", payload.Observed.DirtyPathCount, len(payload.Observed.DirtyPaths), payload.Observed.DirtyPathsTruncated)
	}
}

type aiFactProcessInspector struct {
	boot    string
	process workgraph.AIProcessInspection
	err     error
}

func (inspector aiFactProcessInspector) CurrentBootIdentity() (string, error) {
	return inspector.boot, nil
}

func (inspector aiFactProcessInspector) InspectProcess(int) (workgraph.AIProcessInspection, error) {
	return inspector.process, inspector.err
}

func insertAIFactSession(t *testing.T, databasePath string, ended bool) {
	t.Helper()
	db, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	timestamp := "2026-08-03T12:00:00Z"
	started := `{"schema_version":1,"session_id":"00000000-0000-4000-8000-000000000001","tool":"codex","pid":42,"boot_identity":"boot-a","process_start_identity":"start-a","observed":{"observed_at":"2026-08-03T12:00:00Z","cwd":"/tmp/project","worktree_root":"","git_common_dir":"","branch":"","head":"","dirty_paths":[],"dirty_path_count":0,"dirty_paths_truncated":false}}`
	if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at) VALUES (?, 'ai', 'ai.session_started', ?, ?, '', '', 'AI session started (codex)', ?)`, "ai.session_started:00000000-0000-4000-8000-000000000001", timestamp, started, timestamp); err != nil {
		t.Fatalf("insert start event: %v", err)
	}
	if ended {
		endedPayload := `{"schema_version":1,"session_id":"00000000-0000-4000-8000-000000000001","outcome":{"kind":"exited","exit_code":0},"observation_status":"unavailable"}`
		if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at) VALUES (?, 'ai', 'ai.session_ended', ?, ?, '', '', 'AI session ended (exit 0)', ?)`, "ai.session_ended:00000000-0000-4000-8000-000000000001", "2026-08-03T12:01:00Z", endedPayload, "2026-08-03T12:01:00Z"); err != nil {
			t.Fatalf("insert end event: %v", err)
		}
	}
}

func installAIInsertFailure(t *testing.T, databasePath string, eventType string) {
	t.Helper()
	db, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TRIGGER fail_ai_event BEFORE INSERT ON events
		WHEN NEW.type = '` + eventType + `' BEGIN SELECT RAISE(ABORT, 'injected AI event failure'); END`)
	if err != nil {
		t.Fatalf("install AI event failure: %v", err)
	}
}

func initAIFactGitRepo(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", repository},
		{"-C", repository, "config", "user.email", "facts@example.invalid"},
		{"-C", repository, "config", "user.name", "Workgraph Facts"},
	} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("prepare Git repository with %v: %v\n%s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("facts\n"), 0o644); err != nil {
		t.Fatalf("write repository file: %v", err)
	}
	for _, args := range [][]string{{"-C", repository, "add", "README.md"}, {"-C", repository, "commit", "-qm", "initial"}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("commit Git repository with %v: %v\n%s", args, err, output)
		}
	}
	canonical, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatalf("canonicalize Git repository: %v", err)
	}
	return canonical
}

func waitForAIFactSession(t *testing.T, databasePath string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		db, err := sql.Open("sqlite3", databasePath)
		if err != nil {
			t.Fatalf("open event database: %v", err)
		}
		var sessionID string
		err = db.QueryRow(`SELECT json_extract(payload_json, '$.session_id') FROM events WHERE type = 'ai.session_started'`).Scan(&sessionID)
		db.Close()
		if err == nil {
			return sessionID
		}
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("read started session: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for AI session start")
	return ""
}

func TestAICheckpointStoresValidatedAgentContextAndObservedState(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	checkpoint := `{"goal":"Add durable context","completed":["Defined the contract"],"next_actions":["Implement checkpoints"],"blockers":[],"decisions":["Keep data local"]}`
	command := exec.Command(workgraphFactsBinary,
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath,
		"--", workgraphFactsBinary, "ai", "checkpoint", "--stdin",
	)
	command.Dir = repoRootForDaemon()
	command.Env = daemonCommandEnv(nil)
	command.Stdin = strings.NewReader(checkpoint)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run cooperative checkpoint child: %v\n%s", err, output)
	}

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	var eventID string
	var payloadText string
	if err := db.QueryRow(`SELECT id, payload_json FROM events WHERE type = 'ai.session_checkpointed'`).Scan(&eventID, &payloadText); err != nil {
		t.Fatalf("read checkpoint event: %v", err)
	}
	var payload struct {
		SchemaVersion int    `json:"schema_version"`
		SessionID     string `json:"session_id"`
		Observed      struct {
			WorktreeRoot string   `json:"worktree_root"`
			DirtyPaths   []string `json:"dirty_paths"`
		} `json:"observed"`
		AgentStated struct {
			Goal        string   `json:"goal"`
			Completed   []string `json:"completed"`
			NextActions []string `json:"next_actions"`
			Blockers    []string `json:"blockers"`
			Decisions   []string `json:"decisions"`
		} `json:"agent_stated"`
	}
	if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
		t.Fatalf("decode checkpoint payload: %v", err)
	}
	if payload.SchemaVersion != 1 || payload.AgentStated.Goal != "Add durable context" || len(payload.AgentStated.Completed) != 1 || len(payload.AgentStated.NextActions) != 1 || len(payload.AgentStated.Decisions) != 1 {
		t.Fatalf("unexpected agent checkpoint: %#v", payload.AgentStated)
	}
	receipt := "AI checkpoint recorded\nSession: " + payload.SessionID + "\nEvent: " + eventID + "\n"
	if string(output) != receipt {
		t.Fatalf("expected secret-free checkpoint receipt %q, got:\n%s", receipt, output)
	}
	if payload.Observed.WorktreeRoot != repoRootForDaemon() {
		t.Fatalf("expected worktree root %q, got %#v", repoRootForDaemon(), payload.Observed)
	}
	if !sort.StringsAreSorted(payload.Observed.DirtyPaths) {
		t.Fatalf("expected sorted dirty paths, got %#v", payload.Observed.DirtyPaths)
	}
	for _, path := range payload.Observed.DirtyPaths {
		if filepath.IsAbs(path) {
			t.Fatalf("expected worktree-relative dirty path, got %q", path)
		}
	}
}

func TestAISessionsListsEndedSessionsFromEvents(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	runWorkgraphCommand(t, nil,
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath,
		"--", workgraphFactsBinary, "doctor", "--home", homeDir,
	)

	output := runWorkgraphCommand(t, nil, "ai", "sessions", "--home", homeDir, "--database", initialized.DatabasePath)
	for _, expected := range []string{"AI sessions", "workgraph", "ended", "project: workgraph", "started:", "checkpoint: -", "latest event:"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected sessions output to contain %q, got:\n%s", expected, output)
		}
	}
}

func TestAIArchiveAndUnarchivePreserveSessionEvidence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	toolPath := writeAIFactExecutable(t, workingDir, "codex", "exit 0")
	sessionID := "00000000-0000-4000-8000-000000000090"
	insertAINativeFactSession(t, initialized.DatabasePath, sessionID, "codex", toolPath, "019c547f-d3a6-733d-8471-a0a043354c90", "", workingDir, "2026-08-03T12:00:00Z", true)

	archiveOutput, err := runWorkgraphCommandAllowError(nil,
		"ai", "archive", "--home", homeDir, "--database", initialized.DatabasePath, sessionID,
	)
	if err != nil || !strings.Contains(archiveOutput, "AI session archived\nSession: "+sessionID+"\nEvent: ai.session_archived:"+sessionID+":") {
		t.Fatalf("archive AI session: %v\n%s", err, archiveOutput)
	}
	defaultOutput := runWorkgraphCommand(t, nil, "ai", "sessions", "--home", homeDir, "--database", initialized.DatabasePath)
	if strings.Contains(defaultOutput, sessionID) || defaultOutput != "No AI sessions matched.\n" {
		t.Fatalf("archived session remained in default list:\n%s", defaultOutput)
	}
	allOutput := runWorkgraphCommand(t, nil, "ai", "sessions", "--all", "--home", homeDir, "--database", initialized.DatabasePath)
	if !strings.Contains(allOutput, sessionID) || !strings.Contains(allOutput, "archived: yes") {
		t.Fatalf("archived session missing from all list:\n%s", allOutput)
	}
	showOutput := runWorkgraphCommand(t, nil, "ai", "show", "--home", homeDir, "--database", initialized.DatabasePath, sessionID)
	if !strings.Contains(showOutput, "Archived: yes") {
		t.Fatalf("show did not render archive state:\n%s", showOutput)
	}
	if resumeOutput, err := runWorkgraphCommandAllowError(nil, "ai", "resume", "--home", homeDir, "--database", initialized.DatabasePath, sessionID); err != nil {
		t.Fatalf("archived session was not resumable: %v\n%s", err, resumeOutput)
	}

	repeatOutput := runWorkgraphCommand(t, nil, "ai", "archive", "--home", homeDir, "--database", initialized.DatabasePath, sessionID)
	if repeatOutput != "AI session already archived\nSession: "+sessionID+"\n" {
		t.Fatalf("unexpected idempotent archive receipt:\n%s", repeatOutput)
	}

	unarchiveOutput := runWorkgraphCommand(t, nil, "ai", "unarchive", "--home", homeDir, "--database", initialized.DatabasePath, sessionID)
	if !strings.Contains(unarchiveOutput, "AI session unarchived\nSession: "+sessionID+"\nEvent: ai.session_unarchived:"+sessionID+":") {
		t.Fatalf("unexpected unarchive receipt:\n%s", unarchiveOutput)
	}
	restoredOutput := runWorkgraphCommand(t, nil, "ai", "sessions", "--home", homeDir, "--database", initialized.DatabasePath)
	if !strings.Contains(restoredOutput, sessionID) || !strings.Contains(restoredOutput, "archived: no") {
		t.Fatalf("unarchived session missing from default list:\n%s", restoredOutput)
	}

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	var archiveCount, unarchiveCount, lifecycleCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_archived' AND json_extract(payload_json, '$.session_id') = ?`, sessionID).Scan(&archiveCount); err != nil {
		t.Fatalf("count archive events: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_unarchived' AND json_extract(payload_json, '$.session_id') = ?`, sessionID).Scan(&unarchiveCount); err != nil {
		t.Fatalf("count unarchive events: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type IN ('ai.session_started', 'ai.session_ended') AND json_extract(payload_json, '$.session_id') = ?`, sessionID).Scan(&lifecycleCount); err != nil {
		t.Fatalf("count preserved lifecycle events: %v", err)
	}
	if archiveCount != 1 || unarchiveCount != 1 || lifecycleCount != 2 {
		t.Fatalf("unexpected archive history: archive=%d unarchive=%d lifecycle=%d", archiveCount, unarchiveCount, lifecycleCount)
	}
	var archivePayload string
	if err := db.QueryRow(`SELECT payload_json FROM events WHERE type = 'ai.session_archived' AND json_extract(payload_json, '$.session_id') = ?`, sessionID).Scan(&archivePayload); err != nil {
		t.Fatalf("read archive payload: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(archivePayload), &payload); err != nil || len(payload) != 2 || payload["session_id"] != sessionID || payload["schema_version"] != float64(1) {
		t.Fatalf("unexpected archive payload: %v %#v", err, payload)
	}
}

func TestAISessionsFiltersStatusAndLimitsNewestMatches(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	toolPath := filepath.Join(workingDir, "codex")
	endedIDs := []string{
		"00000000-0000-4000-8000-000000000101",
		"00000000-0000-4000-8000-000000000102",
		"00000000-0000-4000-8000-000000000103",
	}
	for index, sessionID := range endedIDs {
		startedAt := fmt.Sprintf("2026-08-03T%02d:00:00Z", 12+index)
		insertAINativeFactSession(t, initialized.DatabasePath, sessionID, "codex", toolPath, "", "", workingDir, startedAt, true)
	}
	nonEndedID := "00000000-0000-4000-8000-000000000104"
	insertAINativeFactSession(t, initialized.DatabasePath, nonEndedID, "codex", toolPath, "", "", workingDir, "2026-08-03T15:00:00Z", false)
	runWorkgraphCommand(t, nil, "ai", "archive", "--home", homeDir, "--database", initialized.DatabasePath, endedIDs[2])

	defaultOutput := runWorkgraphCommand(t, nil,
		"ai", "sessions", "--status", "ended", "--limit", "2", "--home", homeDir, "--database", initialized.DatabasePath,
	)
	if strings.Contains(defaultOutput, endedIDs[2]) || strings.Contains(defaultOutput, nonEndedID) || !strings.Contains(defaultOutput, endedIDs[1]) || !strings.Contains(defaultOutput, endedIDs[0]) {
		t.Fatalf("default status and limit filter returned wrong sessions:\n%s", defaultOutput)
	}
	if strings.Index(defaultOutput, endedIDs[1]) > strings.Index(defaultOutput, endedIDs[0]) {
		t.Fatalf("limited sessions were not newest first:\n%s", defaultOutput)
	}

	allOutput := runWorkgraphCommand(t, nil,
		"ai", "sessions", "--all", "--status", "ended", "--limit", "2", "--home", homeDir, "--database", initialized.DatabasePath,
	)
	if !strings.Contains(allOutput, endedIDs[2]) || !strings.Contains(allOutput, endedIDs[1]) || strings.Contains(allOutput, endedIDs[0]) || strings.Contains(allOutput, nonEndedID) {
		t.Fatalf("all status and limit filter returned wrong sessions:\n%s", allOutput)
	}

	for _, test := range []struct {
		args     []string
		expected string
	}{
		{args: []string{"--status", "stale"}, expected: `unsupported AI session status "stale"`},
		{args: []string{"--limit", "-1"}, expected: "AI session limit must not be negative"},
	} {
		args := append([]string{"ai", "sessions", "--home", homeDir, "--database", initialized.DatabasePath}, test.args...)
		output, err := runWorkgraphCommandAllowError(nil, args...)
		if err == nil || !strings.Contains(output, test.expected) {
			t.Fatalf("expected sessions validation error %q, got %v\n%s", test.expected, err, output)
		}
	}
}

func TestAISessionsListsOnlyArchivedAndDisclosesLimit(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	toolPath := filepath.Join(workingDir, "codex")
	ids := []string{
		"00000000-0000-4000-8000-000000000111",
		"00000000-0000-4000-8000-000000000112",
		"00000000-0000-4000-8000-000000000113",
	}
	for index, sessionID := range ids {
		insertAINativeFactSession(t, initialized.DatabasePath, sessionID, "codex", toolPath, "", "", workingDir, fmt.Sprintf("2026-08-03T%02d:00:00Z", 12+index), true)
	}
	runWorkgraphCommand(t, nil, "ai", "archive", "--home", homeDir, "--database", initialized.DatabasePath, ids[0], ids[1])

	output := runWorkgraphCommand(t, nil,
		"ai", "sessions", "--archived", "--limit", "1", "--home", homeDir, "--database", initialized.DatabasePath,
	)
	if !strings.Contains(output, "Showing 1 of 2 matching sessions") || !strings.Contains(output, ids[0]) || strings.Contains(output, ids[1]) || strings.Contains(output, ids[2]) {
		t.Fatalf("archived-only limited list was not disclosed deterministically:\n%s", output)
	}
	conflictOutput, err := runWorkgraphCommandAllowError(nil,
		"ai", "sessions", "--all", "--archived", "--home", homeDir, "--database", initialized.DatabasePath,
	)
	if err == nil || !strings.Contains(conflictOutput, "--all and --archived cannot be combined") {
		t.Fatalf("expected archived visibility conflict, got %v\n%s", err, conflictOutput)
	}
}

func TestAIArchiveMultipleExplicitSessionsAtomically(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	toolPath := filepath.Join(workingDir, "codex")
	firstID := "00000000-0000-4000-8000-000000000121"
	secondID := "00000000-0000-4000-8000-000000000122"
	insertAINativeFactSession(t, initialized.DatabasePath, firstID, "codex", toolPath, "", "", workingDir, "2026-08-03T12:00:00Z", true)
	insertAINativeFactSession(t, initialized.DatabasePath, secondID, "codex", toolPath, "", "", workingDir, "2026-08-03T13:00:00Z", true)

	output := runWorkgraphCommand(t, nil,
		"ai", "archive", "--home", homeDir, "--database", initialized.DatabasePath, firstID, secondID, firstID,
	)
	for _, expected := range []string{"AI sessions archived", "Matched: 2", "Archived: 2", "Already archived: 0", firstID, secondID} {
		if !strings.Contains(output, expected) {
			t.Fatalf("multi-archive output missing %q:\n%s", expected, output)
		}
	}
	if strings.Index(output, firstID) > strings.Index(output, secondID) {
		t.Fatalf("explicit IDs did not retain first-appearance order:\n%s", output)
	}

	repeatOutput := runWorkgraphCommand(t, nil,
		"ai", "archive", "--home", homeDir, "--database", initialized.DatabasePath, firstID, secondID,
	)
	if !strings.Contains(repeatOutput, "Archived: 0") || !strings.Contains(repeatOutput, "Already archived: 2") {
		t.Fatalf("multi-archive was not idempotent:\n%s", repeatOutput)
	}

	unarchiveOutput := runWorkgraphCommand(t, nil,
		"ai", "unarchive", "--home", homeDir, "--database", initialized.DatabasePath, firstID, secondID,
	)
	if !strings.Contains(unarchiveOutput, "AI sessions unarchived") || !strings.Contains(unarchiveOutput, "Unarchived: 2") {
		t.Fatalf("multi-unarchive failed:\n%s", unarchiveOutput)
	}

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	for _, sessionID := range []string{firstID, secondID} {
		var archiveCount, unarchiveCount int
		if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_archived' AND json_extract(payload_json, '$.session_id') = ?`, sessionID).Scan(&archiveCount); err != nil {
			t.Fatalf("count archive events for %s: %v", sessionID, err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_unarchived' AND json_extract(payload_json, '$.session_id') = ?`, sessionID).Scan(&unarchiveCount); err != nil {
			t.Fatalf("count unarchive events for %s: %v", sessionID, err)
		}
		if archiveCount != 1 || unarchiveCount != 1 {
			t.Fatalf("non-idempotent transitions for %s: archive=%d unarchive=%d", sessionID, archiveCount, unarchiveCount)
		}
	}
}

func TestAIArchiveExplicitBatchRollsBackOnValidationOrPersistenceFailure(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	toolPath := filepath.Join(workingDir, "codex")
	firstID := "00000000-0000-4000-8000-000000000131"
	secondID := "00000000-0000-4000-8000-000000000132"
	missingID := "00000000-0000-4000-8000-000000000139"
	insertAINativeFactSession(t, initialized.DatabasePath, firstID, "codex", toolPath, "", "", workingDir, "2026-08-03T12:00:00Z", true)
	insertAINativeFactSession(t, initialized.DatabasePath, secondID, "codex", toolPath, "", "", workingDir, "2026-08-03T13:00:00Z", true)

	missingOutput, err := runWorkgraphCommandAllowError(nil,
		"ai", "archive", "--home", homeDir, "--database", initialized.DatabasePath, firstID, missingID,
	)
	if err == nil || !strings.Contains(missingOutput, `AI session "`+missingID+`" was not found`) {
		t.Fatalf("expected whole-batch missing-session error, got %v\n%s", err, missingOutput)
	}

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_second_bulk_archive BEFORE INSERT ON events
		WHEN NEW.type = 'ai.session_archived' AND json_extract(NEW.payload_json, '$.session_id') = '` + secondID + `'
		BEGIN SELECT RAISE(ABORT, 'injected bulk archive failure'); END`); err != nil {
		db.Close()
		t.Fatalf("install bulk archive failure: %v", err)
	}
	db.Close()

	failureOutput, err := runWorkgraphCommandAllowError(nil,
		"ai", "archive", "--home", homeDir, "--database", initialized.DatabasePath, firstID, secondID,
	)
	if err == nil || !strings.Contains(failureOutput, "injected bulk archive failure") {
		t.Fatalf("expected transactional bulk failure, got %v\n%s", err, failureOutput)
	}
	db, err = sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("reopen event database: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_archived' AND json_extract(payload_json, '$.session_id') IN (?, ?)`, firstID, secondID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("bulk archive did not roll back: count=%d err=%v", count, err)
	}
}

func TestAIArchiveSelectorRequiresPreviewOrConfirmation(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	toolPath := filepath.Join(workingDir, "codex")
	oldID := "00000000-0000-4000-8000-000000000141"
	newID := "00000000-0000-4000-8000-000000000142"
	nonEndedID := "00000000-0000-4000-8000-000000000143"
	alreadyArchivedID := "00000000-0000-4000-8000-000000000144"
	insertAINativeFactSession(t, initialized.DatabasePath, oldID, "codex", toolPath, "", "", workingDir, "2026-07-01T12:00:00Z", true)
	insertAINativeFactSession(t, initialized.DatabasePath, newID, "codex", toolPath, "", "", workingDir, "2026-08-02T12:00:00Z", true)
	insertAINativeFactSession(t, initialized.DatabasePath, nonEndedID, "codex", toolPath, "", "", workingDir, "2026-07-01T13:00:00Z", false)
	insertAINativeFactSession(t, initialized.DatabasePath, alreadyArchivedID, "codex", toolPath, "", "", workingDir, "2026-07-01T14:00:00Z", true)
	runWorkgraphCommand(t, nil, "ai", "archive", "--home", homeDir, "--database", initialized.DatabasePath, alreadyArchivedID)

	baseArgs := []string{"ai", "archive", "--home", homeDir, "--database", initialized.DatabasePath, "--status", "ended", "--before", "2026-08-01"}
	previewOutput := runWorkgraphCommand(t, nil, append(baseArgs, "--dry-run")...)
	for _, expected := range []string{"AI archive preview", "Matched: 1", oldID, "codex", "ended", "No events written."} {
		if !strings.Contains(previewOutput, expected) {
			t.Fatalf("archive preview missing %q:\n%s", expected, previewOutput)
		}
	}
	for _, excluded := range []string{newID, nonEndedID, alreadyArchivedID, workingDir} {
		if strings.Contains(previewOutput, excluded) {
			t.Fatalf("archive preview exposed or selected %q:\n%s", excluded, previewOutput)
		}
	}

	approvalOutput, err := runWorkgraphCommandAllowError(nil, baseArgs...)
	if err == nil || !strings.Contains(approvalOutput, "matched 1 sessions") || !strings.Contains(approvalOutput, "--dry-run") || !strings.Contains(approvalOutput, "--yes") {
		t.Fatalf("selector did not require approval: %v\n%s", err, approvalOutput)
	}

	confirmedOutput := runWorkgraphCommand(t, nil, append(baseArgs, "--yes")...)
	if !strings.Contains(confirmedOutput, "AI sessions archived") || !strings.Contains(confirmedOutput, "Archived: 1") || !strings.Contains(confirmedOutput, oldID) {
		t.Fatalf("confirmed archive failed:\n%s", confirmedOutput)
	}

	for _, test := range []struct {
		args     []string
		expected string
	}{
		{args: []string{"--all", "--status", "ended", "--dry-run"}, expected: "--all cannot be combined with --status or --before"},
		{args: []string{"--before", "2026-08-01", "--dry-run"}, expected: "--before requires --status"},
		{args: []string{"--status", "ended", "--before", "not-a-date", "--dry-run"}, expected: "invalid AI archive cutoff"},
		{args: []string{"--all", "--dry-run", "--yes"}, expected: "--dry-run and --yes cannot be combined"},
	} {
		args := append([]string{"ai", "archive", "--home", homeDir, "--database", initialized.DatabasePath}, test.args...)
		output, err := runWorkgraphCommandAllowError(nil, args...)
		if err == nil || !strings.Contains(output, test.expected) {
			t.Fatalf("expected selector validation %q, got %v\n%s", test.expected, err, output)
		}
	}
}

func TestAIShowSeparatesStoredObservationFromAgentCheckpoint(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	checkpoint := `{"goal":"Resume implementation","current_state":"Checkpoint storage works","next_actions":["Render deterministic context"],"blockers":[]}`
	command := exec.Command(workgraphFactsBinary,
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath,
		"--", workgraphFactsBinary, "ai", "checkpoint", "--stdin",
	)
	command.Dir = repoRootForDaemon()
	command.Env = daemonCommandEnv(nil)
	command.Stdin = strings.NewReader(checkpoint)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("record checkpoint: %v\n%s", err, output)
	}

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	var sessionID string
	if err := db.QueryRow(`SELECT json_extract(payload_json, '$.session_id') FROM events WHERE type = 'ai.session_started'`).Scan(&sessionID); err != nil {
		db.Close()
		t.Fatalf("read session id: %v", err)
	}
	db.Close()

	output := runWorkgraphCommand(t, nil, "ai", "show", "--home", homeDir, "--database", initialized.DatabasePath, sessionID)
	for _, expected := range []string{
		"AI session " + sessionID,
		"Status: ended",
		"Observed repository state",
		"Agent-stated checkpoint",
		"Goal\nResume implementation",
		"Current state\nCheckpoint storage works",
		"Next actions\n- Render deterministic context",
		"Blockers\n- None",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected show output to contain %q, got:\n%s", expected, output)
		}
	}
}

func TestAIShowHandlesNoCheckpointAndNewerSchemaSafely(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	insertAIFactSession(t, initialized.DatabasePath, false)
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	unsupported := `{"schema_version":2,"session_id":"00000000-0000-4000-8000-000000000001","agent_stated":{"goal":"must not render"}}`
	_, err = db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at) VALUES (?, 'ai', 'ai.session_checkpointed', ?, ?, '', '', 'AI session checkpointed', ?)`,
		"ai.session_checkpointed:00000000-0000-4000-8000-000000000001:future", "2026-08-03T12:01:00Z", unsupported, "2026-08-03T12:01:00Z")
	db.Close()
	if err != nil {
		t.Fatalf("insert unsupported checkpoint: %v", err)
	}
	output := runWorkgraphCommand(t, nil, "ai", "show", "--home", homeDir, "--database", initialized.DatabasePath, "00000000-0000-4000-8000-000000000001")
	if !strings.Contains(output, "No agent-stated checkpoint recorded.") || !strings.Contains(output, "unsupported schema version 2") || strings.Contains(output, "must not render") {
		t.Fatalf("expected safe unsupported-schema degradation, got:\n%s", output)
	}
}

func TestAICodexNativeBindingPersistsOnlyAllowlistedMetadataAndLineage(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	priorID := "00000000-0000-4000-8000-000000000010"
	currentID := "00000000-0000-4000-8000-000000000011"
	nativeID := "019c547f-d3a6-733d-8471-a0a043354c6b"
	insertAINativeFactSession(t, initialized.DatabasePath, priorID, "codex", "/usr/local/bin/codex", nativeID, "", workingDir, "2026-08-03T12:00:00Z", true)
	insertAINativeFactSession(t, initialized.DatabasePath, currentID, "codex", "/usr/local/bin/codex", "", "", workingDir, "2026-08-03T13:00:00Z", false)

	callback := `{"session_id":"` + nativeID + `","cwd":"` + workingDir + `","hook_event_name":"SessionStart","source":"resume","transcript_path":"/private/secret-transcript.jsonl","model":"secret-model","prompt":"must-not-persist"}`
	command := exec.Command(workgraphFactsBinary, "__ai-native-session", "--tool", "codex")
	command.Env = daemonCommandEnv([]string{
		"WORKGRAPH_AI_SESSION_ID=" + currentID,
		"WORKGRAPH_AI_HOME=" + homeDir,
		"WORKGRAPH_AI_DATABASE=" + initialized.DatabasePath,
	})
	command.Stdin = strings.NewReader(callback)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("bind Codex native session: %v\n%s", err, output)
	}
	if len(output) != 0 {
		t.Fatalf("expected silent native binding hook, got:\n%s", output)
	}
	retry := exec.Command(workgraphFactsBinary, "__ai-native-session", "--tool", "codex")
	retry.Env = command.Env
	endCallback := `{"session_id":"` + nativeID + `","cwd":"` + workingDir + `","hook_event_name":"SessionEnd","reason":"other","transcript_path":"/private/other-secret-transcript.jsonl"}`
	retry.Stdin = strings.NewReader(endCallback)
	if retryOutput, err := retry.CombinedOutput(); err != nil || len(retryOutput) != 0 {
		t.Fatalf("retry idempotent native binding: %v\n%s", err, retryOutput)
	}

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	var payloadText string
	if err := db.QueryRow(`SELECT payload_json FROM events WHERE type = 'ai.session_native_bound' AND json_extract(payload_json, '$.session_id') = ?`, currentID).Scan(&payloadText); err != nil {
		t.Fatalf("read native binding event: %v", err)
	}
	for _, forbidden := range []string{"secret-transcript", "secret-model", "must-not-persist", "transcript_path", "model", "prompt"} {
		if strings.Contains(payloadText, forbidden) {
			t.Fatalf("native binding persisted forbidden callback data %q: %s", forbidden, payloadText)
		}
	}
	var payload struct {
		SessionID            string `json:"session_id"`
		Tool                 string `json:"tool"`
		NativeSessionID      string `json:"native_session_id"`
		Source               string `json:"source"`
		PredecessorSessionID string `json:"predecessor_session_id"`
	}
	if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
		t.Fatalf("decode native binding: %v", err)
	}
	if payload.SessionID != currentID || payload.Tool != "codex" || payload.NativeSessionID != nativeID || payload.Source != "resume" || payload.PredecessorSessionID != priorID {
		t.Fatalf("unexpected native binding: %#v", payload)
	}
	var bindingCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_native_bound' AND json_extract(payload_json, '$.session_id') = ?`, currentID).Scan(&bindingCount); err != nil || bindingCount != 1 {
		t.Fatalf("expected one idempotent native binding, count=%d err=%v", bindingCount, err)
	}
	showOutput := runWorkgraphCommand(t, nil, "ai", "show", "--home", homeDir, "--database", initialized.DatabasePath, currentID)
	for _, expected := range []string{"Native session: " + nativeID, "Predecessor: " + priorID} {
		if !strings.Contains(showOutput, expected) {
			t.Fatalf("expected show output to contain %q, got:\n%s", expected, showOutput)
		}
	}
}

func TestAIRunInjectsCodexBindingHook(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	fixtureDir := t.TempDir()
	argsPath := filepath.Join(fixtureDir, "args.txt")
	codexPath := writeAIFactExecutable(t, fixtureDir, "codex", `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_ARGS"`)
	output, err := runWorkgraphCommandAllowError([]string{"WORKGRAPH_AI_TEST_ARGS=" + argsPath},
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath, "--", codexPath,
	)
	if err != nil {
		t.Fatalf("run Codex hook fixture: %v\n%s", err, output)
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read injected Codex args: %v", err)
	}
	args := string(argsBytes)
	for _, expected := range []string{"hooks.SessionStart", "hooks.SessionEnd", "startup|resume|clear", "__ai-native-session", "--tool codex"} {
		if !strings.Contains(args, expected) {
			t.Fatalf("injected Codex args are missing %q:\n%s", expected, args)
		}
	}
	if strings.Contains(args, "transcript_path") {
		t.Fatalf("injected hook referenced transcript data:\n%s", args)
	}
}

func TestAIResumeLaunchesNativeCodexAndLinksNewLifetime(t *testing.T) {
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
	codexPath := writeAIFactExecutable(t, workingDir, "codex", `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_ARGS"`)
	priorID := "00000000-0000-4000-8000-000000000020"
	nativeID := "019c547f-d3a6-733d-8471-a0a043354c6c"
	insertAINativeFactSession(t, initialized.DatabasePath, priorID, "codex", codexPath, nativeID, "", workingDir, "2026-08-03T12:00:00Z", true)

	output, err := runWorkgraphCommandAllowError([]string{"WORKGRAPH_AI_TEST_ARGS=" + argsPath},
		"ai", "resume", "--home", homeDir, "--database", initialized.DatabasePath, priorID,
	)
	if err != nil {
		t.Fatalf("resume native Codex session: %v\n%s", err, output)
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read native resume args: %v", err)
	}
	if !strings.Contains(string(argsBytes), "resume\n"+nativeID+"\n") {
		t.Fatalf("expected Codex native resume argv, got:\n%s", argsBytes)
	}

	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	var newID, payloadText string
	if err := db.QueryRow(`SELECT json_extract(payload_json, '$.session_id'), payload_json FROM events WHERE type = 'ai.session_started' AND json_extract(payload_json, '$.session_id') <> ? ORDER BY timestamp DESC LIMIT 1`, priorID).Scan(&newID, &payloadText); err != nil {
		t.Fatalf("read resumed workgraph lifetime: %v", err)
	}
	if newID == priorID || !strings.Contains(payloadText, `"predecessor_session_id":"`+priorID+`"`) || !strings.Contains(payloadText, `"native_session_id":"`+nativeID+`"`) {
		t.Fatalf("resumed lifetime was not linked: id=%q payload=%s", newID, payloadText)
	}
}

func TestAIResumeExplainsPresentSessionEnvironmentWithoutLaunching(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	markerPath := filepath.Join(workingDir, "launched")
	codexPath := writeAIFactExecutable(t, workingDir, "codex", `touch "$WORKGRAPH_AI_TEST_MARKER"`)
	sessionID := "00000000-0000-4000-8000-000000000021"
	insertAINativeFactSession(t, initialized.DatabasePath, sessionID, "codex", codexPath, "019c547f-d3a6-733d-8471-a0a043354c6e", "", workingDir, "2026-08-03T12:00:00Z", true)

	output, err := runWorkgraphCommandAllowError([]string{
		"WORKGRAPH_AI_SESSION_ID=stale-session",
		"WORKGRAPH_AI_TEST_MARKER=" + markerPath,
	}, "ai", "resume", "--home", homeDir, "--database", initialized.DatabasePath, sessionID)
	if err == nil || !strings.Contains(output, "run resume outside the wrapped agent") || !strings.Contains(output, "unset it if it is stale") {
		t.Fatalf("expected actionable session environment error, got %v\n%s", err, output)
	}
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("resume with a present session environment launched child: %v", err)
	}
}

func TestAIRunLinksExplicitCodexResumeToKnownNativeSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	codexPath := writeAIFactExecutable(t, workingDir, "codex", "exit 0")
	priorID := "00000000-0000-4000-8000-000000000030"
	nativeID := "019c547f-d3a6-733d-8471-a0a043354c6d"
	insertAINativeFactSession(t, initialized.DatabasePath, priorID, "codex", codexPath, nativeID, "", workingDir, "2026-08-03T12:00:00Z", true)

	output, err := runWorkgraphCommandAllowError(nil,
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath, "--", codexPath, "resume", nativeID,
	)
	if err != nil {
		t.Fatalf("run explicit Codex resume: %v\n%s", err, output)
	}
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	var payloadText string
	if err := db.QueryRow(`SELECT payload_json FROM events WHERE type = 'ai.session_started' AND json_extract(payload_json, '$.session_id') <> ? ORDER BY timestamp DESC LIMIT 1`, priorID).Scan(&payloadText); err != nil {
		t.Fatalf("read explicit resumed lifetime: %v", err)
	}
	if !strings.Contains(payloadText, `"predecessor_session_id":"`+priorID+`"`) || !strings.Contains(payloadText, `"native_session_id":"`+nativeID+`"`) {
		t.Fatalf("explicit Codex resume was not linked: %s", payloadText)
	}
}

func TestAIRunInjectsClaudeBindingHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	fixtureDir := t.TempDir()
	argsPath := filepath.Join(fixtureDir, "args.txt")
	claudePath := writeAIFactExecutable(t, fixtureDir, "claude", `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_ARGS"`)
	output, err := runWorkgraphCommandAllowError([]string{"WORKGRAPH_AI_TEST_ARGS=" + argsPath},
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath, "--", claudePath,
	)
	if err != nil {
		t.Fatalf("run Claude hook fixture: %v\n%s", err, output)
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read injected Claude args: %v", err)
	}
	args := string(argsBytes)
	for _, expected := range []string{"--settings", "SessionStart", "SessionEnd", "startup|resume|clear|compact|fork", "__ai-native-session", "--tool claude"} {
		if !strings.Contains(args, expected) {
			t.Fatalf("injected Claude args are missing %q:\n%s", expected, args)
		}
	}
	if strings.Contains(args, "transcript_path") {
		t.Fatalf("injected Claude hook referenced transcript data:\n%s", args)
	}
}

func TestAINativeLifecycleCallbacksSupportClaudeAndOpenCode(t *testing.T) {
	for _, tool := range []string{"claude", "opencode"} {
		t.Run(tool, func(t *testing.T) {
			homeDir := filepath.Join(t.TempDir(), ".workgraph")
			initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
			if err != nil {
				t.Fatalf("init workgraph: %v", err)
			}
			workingDir := t.TempDir()
			sessionID := "00000000-0000-4000-8000-000000000041"
			nativeID := tool + "-native-id"
			insertAINativeFactSession(t, initialized.DatabasePath, sessionID, tool, "/usr/local/bin/"+tool, "", "", workingDir, "2026-08-03T12:00:00Z", false)

			callback := `{"session_id":"` + nativeID + `","cwd":"` + workingDir + `","hook_event_name":"SessionStart","source":"startup","transcript_path":"/private/secret.jsonl","prompt":"must-not-persist"}`
			command := exec.Command(workgraphFactsBinary, "__ai-native-session", "--tool", tool)
			command.Env = append(os.Environ(), "WORKGRAPH_AI_HOME="+homeDir, "WORKGRAPH_AI_DATABASE="+initialized.DatabasePath, "WORKGRAPH_AI_SESSION_ID="+sessionID)
			command.Stdin = strings.NewReader(callback)
			if output, err := command.CombinedOutput(); err != nil || len(output) != 0 {
				t.Fatalf("bind %s native session: %v\n%s", tool, err, output)
			}

			db, err := sql.Open("sqlite3", initialized.DatabasePath)
			if err != nil {
				t.Fatalf("open event database: %v", err)
			}
			defer db.Close()
			var payloadText string
			if err := db.QueryRow(`SELECT payload_json FROM events WHERE type = 'ai.session_native_bound' AND json_extract(payload_json, '$.session_id') = ?`, sessionID).Scan(&payloadText); err != nil {
				t.Fatalf("read native binding: %v", err)
			}
			if !strings.Contains(payloadText, `"native_session_id":"`+nativeID+`"`) || strings.Contains(payloadText, "secret.jsonl") || strings.Contains(payloadText, "must-not-persist") {
				t.Fatalf("unexpected allowlisted native binding: %s", payloadText)
			}
		})
	}
}

func TestAIRunAssignsExactCopilotSessionID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	fixtureDir := t.TempDir()
	argsPath := filepath.Join(fixtureDir, "args.txt")
	copilotPath := writeAIFactExecutable(t, fixtureDir, "copilot", `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_ARGS"`)
	output, err := runWorkgraphCommandAllowError([]string{"WORKGRAPH_AI_TEST_ARGS=" + argsPath},
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath, "--", copilotPath,
	)
	if err != nil {
		t.Fatalf("run Copilot fixture: %v\n%s", err, output)
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read Copilot args: %v", err)
	}
	argument := strings.TrimSpace(string(argsBytes))
	if !strings.HasPrefix(argument, "--session-id=") {
		t.Fatalf("expected exact Copilot session assignment, got %q", argument)
	}
	nativeID := strings.TrimPrefix(argument, "--session-id=")
	if len(nativeID) != 36 || strings.Count(nativeID, "-") != 4 {
		t.Fatalf("expected generated Copilot UUID, got %q", nativeID)
	}
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	var storedID string
	if err := db.QueryRow(`SELECT json_extract(payload_json, '$.native_session_id') FROM events WHERE type = 'ai.session_started'`).Scan(&storedID); err != nil {
		t.Fatalf("read stored Copilot id: %v", err)
	}
	if storedID != nativeID {
		t.Fatalf("stored Copilot id %q does not match assigned id %q", storedID, nativeID)
	}
}

func TestAIRunInjectsOpenCodeBindingPluginWithoutChangingProjectConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	fixtureDir := t.TempDir()
	configPath := filepath.Join(fixtureDir, "config.txt")
	hookPath := filepath.Join(fixtureDir, "hook.txt")
	opencodePath := writeAIFactExecutable(t, fixtureDir, "opencode", `printf '%s' "$OPENCODE_CONFIG_CONTENT" > "$WORKGRAPH_AI_TEST_CONFIG"
printf '%s' "$WORKGRAPH_AI_HOOK_ARGV" > "$WORKGRAPH_AI_TEST_HOOK"`)
	output, err := runWorkgraphCommandAllowError([]string{
		"OPENCODE_CONFIG_CONTENT={\"model\":\"test/model\"}",
		"WORKGRAPH_AI_TEST_CONFIG=" + configPath,
		"WORKGRAPH_AI_TEST_HOOK=" + hookPath,
	}, "ai", "run", "--home", homeDir, "--database", initialized.DatabasePath, "--", opencodePath)
	if err != nil {
		t.Fatalf("run OpenCode fixture: %v\n%s", err, output)
	}
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read OpenCode inline config: %v", err)
	}
	var config map[string]any
	if err := json.Unmarshal(configBytes, &config); err != nil {
		t.Fatalf("decode OpenCode inline config: %v\n%s", err, configBytes)
	}
	if config["model"] != "test/model" {
		t.Fatalf("existing OpenCode config was not preserved: %#v", config)
	}
	plugins, ok := config["plugin"].([]any)
	if !ok || len(plugins) == 0 {
		t.Fatalf("OpenCode binding plugin was not injected: %#v", config)
	}
	pluginPath, ok := plugins[len(plugins)-1].(string)
	if !ok {
		t.Fatalf("OpenCode plugin path is not a string: %#v", plugins)
	}
	pluginBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read generated OpenCode plugin: %v", err)
	}
	plugin := string(pluginBytes)
	for _, expected := range []string{"session.created", "session.updated", "sessionID", "WORKGRAPH_AI_HOOK_ARGV"} {
		if !strings.Contains(plugin, expected) {
			t.Fatalf("OpenCode plugin is missing %q:\n%s", expected, plugin)
		}
	}
	for _, forbidden := range []string{"message.updated", "prompt", "session.diff"} {
		if strings.Contains(plugin, forbidden) {
			t.Fatalf("OpenCode plugin referenced forbidden data %q:\n%s", forbidden, plugin)
		}
	}
	hookBytes, err := os.ReadFile(hookPath)
	if err != nil || !strings.Contains(string(hookBytes), "__ai-native-session") || !strings.Contains(string(hookBytes), "opencode") {
		t.Fatalf("OpenCode hook argv unavailable: %v %s", err, hookBytes)
	}
	if _, err := os.Stat(filepath.Join(fixtureDir, "opencode.json")); !os.IsNotExist(err) {
		t.Fatalf("workgraph unexpectedly changed project OpenCode config: %v", err)
	}
}

func TestAIResumeSupportsVerifiedNativeAdapters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for index, test := range []struct {
		tool         string
		nativeID     string
		expectedArgs string
	}{
		{tool: "claude", nativeID: "11111111-1111-4111-8111-111111111111", expectedArgs: "--resume\n11111111-1111-4111-8111-111111111111\n"},
		{tool: "copilot", nativeID: "22222222-2222-4222-8222-222222222222", expectedArgs: "--resume=22222222-2222-4222-8222-222222222222\n"},
		{tool: "opencode", nativeID: "ses_opencode_native", expectedArgs: "--session\nses_opencode_native\n"},
	} {
		t.Run(test.tool, func(t *testing.T) {
			homeDir := filepath.Join(t.TempDir(), ".workgraph")
			initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
			if err != nil {
				t.Fatalf("init workgraph: %v", err)
			}
			workingDir := t.TempDir()
			argsPath := filepath.Join(workingDir, "args.txt")
			toolPath := writeAIFactExecutable(t, workingDir, test.tool, `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_ARGS"`)
			sessionID := fmt.Sprintf("00000000-0000-4000-8000-%012d", 50+index)
			insertAINativeFactSession(t, initialized.DatabasePath, sessionID, test.tool, toolPath, test.nativeID, "", workingDir, "2026-08-03T12:00:00Z", true)

			output, err := runWorkgraphCommandAllowError([]string{"WORKGRAPH_AI_TEST_ARGS=" + argsPath},
				"ai", "resume", "--home", homeDir, "--database", initialized.DatabasePath, sessionID,
			)
			if err != nil {
				t.Fatalf("resume %s session: %v\n%s", test.tool, err, output)
			}
			argsBytes, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatalf("read %s resume args: %v", test.tool, err)
			}
			if !strings.Contains(string(argsBytes), test.expectedArgs) {
				t.Fatalf("expected %s native resume argv %q, got:\n%s", test.tool, test.expectedArgs, argsBytes)
			}
		})
	}
}

func TestAIRunLinksExplicitVerifiedNativeResumes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for index, test := range []struct {
		tool     string
		nativeID string
		args     []string
	}{
		{tool: "claude", nativeID: "31111111-1111-4111-8111-111111111111", args: []string{"--resume", "31111111-1111-4111-8111-111111111111"}},
		{tool: "copilot", nativeID: "32222222-2222-4222-8222-222222222222", args: []string{"--resume=32222222-2222-4222-8222-222222222222"}},
		{tool: "opencode", nativeID: "ses_explicit_opencode", args: []string{"--session", "ses_explicit_opencode"}},
	} {
		t.Run(test.tool, func(t *testing.T) {
			homeDir := filepath.Join(t.TempDir(), ".workgraph")
			initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
			if err != nil {
				t.Fatalf("init workgraph: %v", err)
			}
			workingDir := t.TempDir()
			toolPath := writeAIFactExecutable(t, workingDir, test.tool, "exit 0")
			priorID := fmt.Sprintf("00000000-0000-4000-8000-%012d", 70+index)
			insertAINativeFactSession(t, initialized.DatabasePath, priorID, test.tool, toolPath, test.nativeID, "", workingDir, "2026-08-03T12:00:00Z", true)

			commandArgs := []string{"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath, "--", toolPath}
			commandArgs = append(commandArgs, test.args...)
			if output, err := runWorkgraphCommandAllowError(nil, commandArgs...); err != nil {
				t.Fatalf("run explicit %s resume: %v\n%s", test.tool, err, output)
			}
			db, err := sql.Open("sqlite3", initialized.DatabasePath)
			if err != nil {
				t.Fatalf("open event database: %v", err)
			}
			defer db.Close()
			var payloadText string
			if err := db.QueryRow(`SELECT payload_json FROM events WHERE type = 'ai.session_started' AND json_extract(payload_json, '$.session_id') <> ? ORDER BY timestamp DESC LIMIT 1`, priorID).Scan(&payloadText); err != nil {
				t.Fatalf("read explicit %s resumed lifetime: %v", test.tool, err)
			}
			if !strings.Contains(payloadText, `"native_session_id":"`+test.nativeID+`"`) || !strings.Contains(payloadText, `"predecessor_session_id":"`+priorID+`"`) {
				t.Fatalf("explicit %s resume was not linked: %s", test.tool, payloadText)
			}
		})
	}
}

func TestAIRunDoesNotInventCopilotIdentityForAmbiguousResume(t *testing.T) {
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
	copilotPath := writeAIFactExecutable(t, workingDir, "copilot", `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_ARGS"`)
	if output, err := runWorkgraphCommandAllowError([]string{"WORKGRAPH_AI_TEST_ARGS=" + argsPath},
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath, "--", copilotPath, "--resume=short-prefix",
	); err != nil {
		t.Fatalf("run ambiguous Copilot resume: %v\n%s", err, output)
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read Copilot args: %v", err)
	}
	if strings.Contains(string(argsBytes), "--session-id") {
		t.Fatalf("workgraph overrode ambiguous Copilot selection:\n%s", argsBytes)
	}
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	var nativeID any
	if err := db.QueryRow(`SELECT json_extract(payload_json, '$.native_session_id') FROM events WHERE type = 'ai.session_started'`).Scan(&nativeID); err != nil {
		t.Fatalf("read ambiguous Copilot start: %v", err)
	}
	if nativeID != nil {
		t.Fatalf("workgraph invented exact Copilot identity: %#v", nativeID)
	}
}

func TestAIRunRejectsAdapterConfigConflictsBeforeLaunch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for _, test := range []struct {
		tool     string
		args     []string
		env      []string
		expected string
	}{
		{tool: "codex", args: []string{"-c", `hooks.SessionStart=[]`}, expected: "Codex hooks.SessionStart cannot be combined"},
		{tool: "codex", args: []string{"--config", `hooks.SessionEnd=[]`}, expected: "Codex hooks.SessionEnd cannot be combined"},
		{tool: "codex", args: []string{`--config=hooks.SessionStart=[]`}, expected: "Codex hooks.SessionStart cannot be combined"},
		{tool: "codex", args: []string{`-c=hooks.SessionEnd=[]`}, expected: "Codex hooks.SessionEnd cannot be combined"},
		{tool: "claude", args: []string{"--settings", `{}`}, expected: "Claude --settings cannot be combined"},
		{tool: "opencode", env: []string{"OPENCODE_CONFIG_CONTENT=not-json"}, expected: "OPENCODE_CONFIG_CONTENT must be one valid JSON object"},
	} {
		t.Run(test.tool, func(t *testing.T) {
			homeDir := filepath.Join(t.TempDir(), ".workgraph")
			initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
			if err != nil {
				t.Fatalf("init workgraph: %v", err)
			}
			workingDir := t.TempDir()
			markerPath := filepath.Join(workingDir, "launched")
			toolPath := writeAIFactExecutable(t, workingDir, test.tool, `touch "$WORKGRAPH_AI_TEST_MARKER"`)
			environment := append([]string{}, test.env...)
			environment = append(environment, "WORKGRAPH_AI_TEST_MARKER="+markerPath)
			commandArgs := []string{"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath, "--", toolPath}
			commandArgs = append(commandArgs, test.args...)
			output, err := runWorkgraphCommandAllowError(environment, commandArgs...)
			if err == nil || !strings.Contains(output, test.expected) {
				t.Fatalf("expected adapter configuration rejection, got %v\n%s", err, output)
			}
			if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
				t.Fatalf("invalid adapter configuration launched child: %v", err)
			}
			db, err := sql.Open("sqlite3", initialized.DatabasePath)
			if err != nil {
				t.Fatalf("open event database: %v", err)
			}
			defer db.Close()
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'ai.session_started'`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("invalid adapter configuration stored start event: count=%d err=%v", count, err)
			}
		})
	}
}

func TestAIRunAllowsUnrelatedCodexConfigOverrides(t *testing.T) {
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
	codexPath := writeAIFactExecutable(t, workingDir, "codex", `printf '%s\n' "$@" > "$WORKGRAPH_AI_TEST_ARGS"`)
	output, err := runWorkgraphCommandAllowError([]string{"WORKGRAPH_AI_TEST_ARGS=" + argsPath},
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath,
		"--", codexPath, "-c", `model="gpt-5.6"`,
	)
	if err != nil {
		t.Fatalf("run Codex with unrelated config override: %v\n%s", err, output)
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read Codex args: %v", err)
	}
	if !strings.Contains(string(argsBytes), `model="gpt-5.6"`) || !strings.Contains(string(argsBytes), "hooks.SessionStart") {
		t.Fatalf("unrelated Codex config or workgraph hook missing:\n%s", argsBytes)
	}
}

func TestAIResumeRefusesUnsupportedNativeAdapterWithoutLaunching(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	workingDir := t.TempDir()
	markerPath := filepath.Join(workingDir, "launched")
	aiderPath := writeAIFactExecutable(t, workingDir, "aider", `touch "$WORKGRAPH_AI_TEST_MARKER"`)
	sessionID := "00000000-0000-4000-8000-000000000040"
	insertAINativeFactSession(t, initialized.DatabasePath, sessionID, "aider", aiderPath, "aider-native-id", "", workingDir, "2026-08-03T12:00:00Z", true)

	output, err := runWorkgraphCommandAllowError([]string{"WORKGRAPH_AI_TEST_MARKER=" + markerPath},
		"ai", "resume", "--home", homeDir, "--database", initialized.DatabasePath, sessionID,
	)
	if err == nil || !strings.Contains(output, `AI tool "aider" has no verified native adapter`) {
		t.Fatalf("expected unsupported native adapter error, got %v\n%s", err, output)
	}
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("unsupported adapter unexpectedly launched a process: %v", err)
	}
}

func insertAINativeFactSession(t *testing.T, databasePath, sessionID, tool, toolPath, nativeID, predecessorID, cwd, startedAt string, ended bool) {
	t.Helper()
	startPayload := map[string]any{
		"schema_version":         1,
		"session_id":             sessionID,
		"tool":                   tool,
		"tool_path":              toolPath,
		"pid":                    42,
		"boot_identity":          "boot-a",
		"process_start_identity": "start-a",
		"observed": map[string]any{
			"observed_at": startedAt, "cwd": cwd, "worktree_root": "", "git_common_dir": "", "branch": "", "head": "",
			"dirty_paths": []string{}, "dirty_path_count": 0, "dirty_paths_truncated": false,
		},
	}
	if nativeID != "" {
		startPayload["native_session_id"] = nativeID
	}
	if predecessorID != "" {
		startPayload["predecessor_session_id"] = predecessorID
	}
	encoded, err := json.Marshal(startPayload)
	if err != nil {
		t.Fatalf("encode native fact start: %v", err)
	}
	db, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at) VALUES (?, 'ai', 'ai.session_started', ?, ?, '', '', ?, ?)`, "ai.session_started:"+sessionID, startedAt, string(encoded), "AI session started ("+tool+")", startedAt); err != nil {
		t.Fatalf("insert native fact start: %v", err)
	}
	if ended {
		startedTime, err := time.Parse(time.RFC3339, startedAt)
		if err != nil {
			t.Fatalf("parse native fact time: %v", err)
		}
		endedAt := startedTime.Add(time.Minute).Format(time.RFC3339)
		endedPayload := `{"schema_version":1,"session_id":"` + sessionID + `","outcome":{"kind":"exited","exit_code":0},"observation_status":"unavailable"}`
		if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at) VALUES (?, 'ai', 'ai.session_ended', ?, ?, '', '', 'AI session ended (exit 0)', ?)`, "ai.session_ended:"+sessionID, endedAt, endedPayload, endedAt); err != nil {
			t.Fatalf("insert native fact end: %v", err)
		}
	}
}

func writeAIFactExecutable(t *testing.T, directory, name, body string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	contents := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write AI executable fixture: %v", err)
	}
	return path
}
