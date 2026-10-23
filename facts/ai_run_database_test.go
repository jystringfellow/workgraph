package facts

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
	_ "github.com/mattn/go-sqlite3"
)

type aiRunDatabaseFact struct {
	databasePath string
	ready        string
	release      string
	command      *exec.Cmd
	output       *lockedFactBuffer
	exited       chan struct{}
	waitErr      error
}

type lockedFactBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *lockedFactBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *lockedFactBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func newAIRunDatabaseFact(t *testing.T) *aiRunDatabaseFact {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell helper process")
	}
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	markers := t.TempDir()
	fact := &aiRunDatabaseFact{
		databasePath: initialized.DatabasePath,
		ready:        filepath.Join(markers, "agent-ready"),
		release:      filepath.Join(markers, "agent-release"),
		output:       &lockedFactBuffer{},
		exited:       make(chan struct{}),
	}
	fact.command = exec.Command(workgraphFactsBinary,
		"ai", "run", "--home", homeDir, "--database", initialized.DatabasePath,
		"--", "sh", "-c", `touch "$1"; while [ ! -f "$2" ]; do sleep 0.05; done`, "workgraph-ai-test", fact.ready, fact.release,
	)
	fact.command.Dir = repoRootForDaemon()
	fact.command.Env = daemonCommandEnv(nil)
	fact.command.Stdout = fact.output
	fact.command.Stderr = fact.output
	return fact
}

func (fact *aiRunDatabaseFact) start(t *testing.T) {
	t.Helper()
	if err := fact.command.Start(); err != nil {
		t.Fatalf("start wrapped fake agent: %v", err)
	}
	go func() {
		fact.waitErr = fact.command.Wait()
		close(fact.exited)
	}()
	t.Cleanup(func() {
		_ = os.WriteFile(fact.release, nil, 0o644)
		select {
		case <-fact.exited:
		case <-time.After(30 * time.Second):
			_ = fact.command.Process.Kill()
			<-fact.exited
		}
	})
	fact.waitForFile(t, fact.ready)
}

func startAIRunDatabaseFact(t *testing.T) *aiRunDatabaseFact {
	t.Helper()
	fact := newAIRunDatabaseFact(t)
	fact.start(t)
	return fact
}

func (fact *aiRunDatabaseFact) waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-fact.exited:
			t.Fatalf("wrapper exited before %s appeared: %v\n%s", filepath.Base(path), fact.waitErr, fact.output.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for %s\n%s", filepath.Base(path), fact.output.String())
}

func (fact *aiRunDatabaseFact) requireRunning(t *testing.T) {
	t.Helper()
	select {
	case <-fact.exited:
		t.Fatalf("wrapper exited early: %v\n%s", fact.waitErr, fact.output.String())
	default:
	}
}

func (fact *aiRunDatabaseFact) releaseAndWait(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(fact.release, nil, 0o644); err != nil {
		t.Fatalf("release fake agent: %v", err)
	}
	fact.waitForExit(t)
}

func (fact *aiRunDatabaseFact) waitForExit(t *testing.T) {
	t.Helper()
	select {
	case <-fact.exited:
	case <-time.After(30 * time.Second):
		t.Fatalf("wrapper did not exit\n%s", fact.output.String())
	}
}

func countAIFactEvents(t *testing.T, databasePath string, eventType string) int {
	t.Helper()
	db, err := sql.Open("sqlite3", databasePath+"?_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = ?`, eventType).Scan(&count); err != nil {
		t.Fatalf("count %s events: %v", eventType, err)
	}
	return count
}

func holdAIFactExclusiveLock(t *testing.T, databasePath string) func() {
	t.Helper()
	db, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`BEGIN EXCLUSIVE`); err != nil {
		db.Close()
		t.Fatalf("take exclusive database lock: %v", err)
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		if _, err := db.Exec(`COMMIT`); err != nil {
			t.Errorf("release exclusive database lock: %v", err)
		}
		db.Close()
	}
	t.Cleanup(release)
	return release
}

func TestAIRunHoldsNoDatabaseHandleWhileAgentIsIdle(t *testing.T) {
	fact := startAIRunDatabaseFact(t)
	waitForAIFactSession(t, fact.databasePath)

	movedPath := fact.databasePath + ".moved"
	if err := os.Rename(fact.databasePath, movedPath); err != nil {
		t.Fatalf("move database aside while agent is idle: %v", err)
	}
	contents, err := os.ReadFile(movedPath)
	if err != nil {
		t.Fatalf("read moved database: %v", err)
	}
	if err := os.WriteFile(fact.databasePath, contents, 0o600); err != nil {
		t.Fatalf("restore database copy at original path: %v", err)
	}

	fact.requireRunning(t)
	fact.releaseAndWait(t)
	if fact.waitErr != nil {
		t.Fatalf("wrapper failed: %v\n%s", fact.waitErr, fact.output.String())
	}
	if count := countAIFactEvents(t, fact.databasePath, "ai.session_ended"); count != 1 {
		t.Fatalf("expected end event in the database at the configured path, got %d\n%s", count, fact.output.String())
	}
	if count := countAIFactEvents(t, movedPath, "ai.session_ended"); count != 0 {
		t.Fatalf("end event was written through a handle held since session start")
	}
}

func TestAIRunKeepsAgentRunningWhileDatabaseIsLockedAtStart(t *testing.T) {
	fact := newAIRunDatabaseFact(t)
	releaseLock := holdAIFactExclusiveLock(t, fact.databasePath)
	fact.start(t)

	time.Sleep(7 * time.Second)
	fact.requireRunning(t)
	releaseLock()

	waitForAIFactSessionWithin(t, fact.databasePath, 15*time.Second)
	fact.requireRunning(t)
	fact.releaseAndWait(t)
	if fact.waitErr != nil {
		t.Fatalf("wrapper failed after the lock cleared: %v\n%s", fact.waitErr, fact.output.String())
	}
	if count := countAIFactEvents(t, fact.databasePath, "ai.session_ended"); count != 1 {
		t.Fatalf("expected one end event, got %d\n%s", count, fact.output.String())
	}
	if !strings.Contains(fact.output.String(), "AI session start was recorded late") {
		t.Fatalf("expected delayed start to be reported after the agent exited, got:\n%s", fact.output.String())
	}
}

func TestAIRunRetriesEndRecordWhileDatabaseIsLocked(t *testing.T) {
	fact := startAIRunDatabaseFact(t)
	waitForAIFactSession(t, fact.databasePath)

	releaseLock := holdAIFactExclusiveLock(t, fact.databasePath)
	if err := os.WriteFile(fact.release, nil, 0o644); err != nil {
		t.Fatalf("release fake agent: %v", err)
	}
	time.Sleep(7 * time.Second)
	fact.requireRunning(t)
	releaseLock()
	fact.waitForExit(t)

	if fact.waitErr != nil {
		t.Fatalf("wrapper failed: %v\n%s", fact.waitErr, fact.output.String())
	}
	if count := countAIFactEvents(t, fact.databasePath, "ai.session_ended"); count != 1 {
		t.Fatalf("expected the end event to land once the lock cleared, got %d\n%s", count, fact.output.String())
	}
	if strings.Contains(fact.output.String(), "persist AI session end") {
		t.Fatalf("end event reported as failed although it landed:\n%s", fact.output.String())
	}
}

func TestAIRunRecordsEndWhenWrapperIsSignaled(t *testing.T) {
	fact := startAIRunDatabaseFact(t)
	waitForAIFactSession(t, fact.databasePath)

	if err := fact.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal wrapper: %v", err)
	}
	fact.waitForExit(t)

	var exitErr *exec.ExitError
	if !errors.As(fact.waitErr, &exitErr) || exitErr.ExitCode() != 143 {
		t.Fatalf("expected wrapper to return the forwarded SIGTERM outcome 143, got %v\n%s", fact.waitErr, fact.output.String())
	}
	db, err := sql.Open("sqlite3", fact.databasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	var summary string
	if err := db.QueryRow(`SELECT summary FROM events WHERE type = 'ai.session_ended'`).Scan(&summary); err != nil {
		t.Fatalf("read end event after wrapper signal: %v\n%s", err, fact.output.String())
	}
	if summary != "AI session ended (signal SIGTERM)" {
		t.Fatalf("unexpected end summary after wrapper signal: %q", summary)
	}
}

func waitForAIFactSessionWithin(t *testing.T, databasePath string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if countAIFactEvents(t, databasePath, "ai.session_started") == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for AI session start")
}
