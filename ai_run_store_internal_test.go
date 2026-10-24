package workgraph

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

type aiRunOpenTracker struct {
	mu     sync.Mutex
	opened []*sql.DB
}

func (tracker *aiRunOpenTracker) open(databasePath string, busyTimeout time.Duration) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", aiRunDatabaseDSN(databasePath, busyTimeout))
	if err == nil {
		tracker.mu.Lock()
		tracker.opened = append(tracker.opened, db)
		tracker.mu.Unlock()
	}
	return db, err
}

func (tracker *aiRunOpenTracker) openCount() int {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return len(tracker.opened)
}

func (tracker *aiRunOpenTracker) requireAllClosed(t *testing.T) {
	t.Helper()
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	for index, db := range tracker.opened {
		if open := db.Stats().OpenConnections; open != 0 {
			t.Fatalf("database handle %d has %d open connections", index, open)
		}
		if err := db.Ping(); err == nil || err.Error() != "sql: database is closed" {
			t.Fatalf("database handle %d was not closed: %v", index, err)
		}
	}
}

func TestAIRunClosesEveryDatabaseOpenBeforeAgentIdles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper process")
	}
	t.Setenv("WORKGRAPH_AI_SESSION_ID", "")
	os.Unsetenv("WORKGRAPH_AI_SESSION_ID")
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := Init(InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	tracker := &aiRunOpenTracker{}
	previousOpen := aiRunOpenDatabase
	aiRunOpenDatabase = tracker.open
	t.Cleanup(func() { aiRunOpenDatabase = previousOpen })

	markers := t.TempDir()
	ready := filepath.Join(markers, "agent-ready")
	release := filepath.Join(markers, "agent-release")
	type runOutcome struct {
		result AIRunResult
		err    error
	}
	finished := make(chan runOutcome, 1)
	go func() {
		result, err := RunAISession(AIRunConfig{
			HomeDir:      homeDir,
			DatabasePath: initialized.DatabasePath,
			WorkingDir:   markers,
			Command:      []string{"sh", "-c", `touch "$1"; while [ ! -f "$2" ]; do sleep 0.05; done`, "workgraph-ai-test", ready, release},
		})
		finished <- runOutcome{result: result, err: err}
	}()
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o644) })

	deadline := time.Now().Add(10 * time.Second)
	for !aiRunTrackerFactStartRecorded(t, initialized.DatabasePath) || !aiRunTrackerFactFileExists(ready) {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the running fake agent and its AI session start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if tracker.openCount() == 0 {
		t.Fatal("expected the session start to use a tracked database open")
	}
	tracker.requireAllClosed(t)
	idleOpens := tracker.openCount()

	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatalf("release fake agent: %v", err)
	}
	outcome := <-finished
	if outcome.err != nil || outcome.result.ExitCode != 0 {
		t.Fatalf("wrapped session failed: %+v %v", outcome.result, outcome.err)
	}
	if tracker.openCount() <= idleOpens {
		t.Fatalf("expected the session end to open the database again, opens stayed at %d", idleOpens)
	}
	tracker.requireAllClosed(t)
}

func aiRunTrackerFactFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func aiRunTrackerFactStartRecorded(t *testing.T, databasePath string) bool {
	t.Helper()
	db, err := sql.Open("sqlite3", databasePath+"?_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	var id string
	err = db.QueryRow(`SELECT id FROM events WHERE type = 'ai.session_started'`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		t.Fatalf("read AI session start: %v", err)
	}
	return true
}

func TestAIRunDatabaseOpenNeverCreatesAMissingDatabase(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "space # dir", "workgraph.db")
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		t.Fatalf("create database directory: %v", err)
	}
	err := withAIRunDatabase(databasePath, time.Now().Add(10*time.Second), func(aiRunConnection) error { return nil })
	if err == nil || !isAIRunDatabaseUnavailable(err) {
		t.Fatalf("expected a retryable unavailable-database error, got %v", err)
	}
	if _, statErr := os.Stat(databasePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("short open created a missing database: %v", statErr)
	}

	if _, err := Init(InitConfig{HomeDir: filepath.Dir(databasePath)}); err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	var count int
	err = withAIRunDatabase(databasePath, time.Now().Add(10*time.Second), func(db aiRunConnection) error {
		return db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count)
	})
	if err != nil {
		t.Fatalf("open existing database through its file URI: %v", err)
	}
}

func holdAIRunTestExclusiveLock(t *testing.T, databasePath string) {
	t.Helper()
	release := takeAIRunTestExclusiveLock(t, databasePath)
	t.Cleanup(release)
}

func takeAIRunTestExclusiveLock(t *testing.T, databasePath string) func() {
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
	var once sync.Once
	return func() {
		once.Do(func() {
			_, _ = db.Exec(`COMMIT`)
			db.Close()
		})
	}
}

func initAIRunTestDatabase(t *testing.T) string {
	t.Helper()
	initialized, err := Init(InitConfig{HomeDir: filepath.Join(t.TempDir(), ".workgraph")})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	return initialized.DatabasePath
}

func countAIRunTestEvents(db aiRunConnection) error {
	var count int
	return db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count)
}

func TestAIRunDatabaseRetryGivesUpWithinItsDeadline(t *testing.T) {
	databasePath := initAIRunTestDatabase(t)
	holdAIRunTestExclusiveLock(t, databasePath)

	budget := 1500 * time.Millisecond
	began := time.Now()
	err := retryAIRunDatabase(databasePath, began.Add(budget), countAIRunTestEvents)
	elapsed := time.Since(began)
	if err == nil || !isAIRunDatabaseUnavailable(err) {
		t.Fatalf("expected an unavailable-database error, got %v", err)
	}
	if elapsed < budget-aiRunLockWaitSlice || elapsed > budget+time.Second {
		t.Fatalf("retry ran %v; expected it to keep retrying until its %v deadline and stop there", elapsed, budget)
	}
}

func TestAIRunStartRetryAfterChildExitGivesUpWithinItsBudget(t *testing.T) {
	databasePath := initAIRunTestDatabase(t)
	holdAIRunTestExclusiveLock(t, databasePath)
	previousBudget := aiRunRetryBudget
	aiRunRetryBudget = 1500 * time.Millisecond
	t.Cleanup(func() { aiRunRetryBudget = previousBudget })

	done := make(chan struct{})
	exitedAt := time.Now()
	close(done)
	start := persistAIRunStart(databasePath, countAIRunTestEvents, aiRunChildExit{done: done, at: &exitedAt})
	elapsed := time.Since(exitedAt)
	if start.err == nil || !isAIRunDatabaseUnavailable(start.err) || !start.childExited {
		t.Fatalf("expected an unavailable start after child exit, got %+v", start)
	}
	if elapsed < aiRunRetryBudget-aiRunLockWaitSlice || elapsed > aiRunRetryBudget+time.Second {
		t.Fatalf("start retry ran %v after child exit; expected it to retry until its %v window ended and stop there", elapsed, aiRunRetryBudget)
	}
}

func TestAIRunDatabaseDeadlineCoversLocksTakenAfterConnecting(t *testing.T) {
	databasePath := initAIRunTestDatabase(t)
	budget := 3 * time.Second
	began := time.Now()
	aiRunAfterConnect = func() {
		time.Sleep(2 * time.Second)
		t.Cleanup(takeAIRunTestExclusiveLock(t, databasePath))
	}
	t.Cleanup(func() { aiRunAfterConnect = nil })

	err := withAIRunDatabase(databasePath, began.Add(budget), countAIRunTestEvents)
	elapsed := time.Since(began)
	if err == nil || !isAIRunDatabaseUnavailable(err) {
		t.Fatalf("expected an unavailable-database error, got %v", err)
	}
	if elapsed > budget+time.Second {
		t.Fatalf("statement waited %v against a %v deadline after connecting", elapsed, budget)
	}
}

func TestAIRunStartWindowCountsFromChildExitDuringABlockedAttempt(t *testing.T) {
	databasePath := initAIRunTestDatabase(t)
	holdAIRunTestExclusiveLock(t, databasePath)
	previousBudget := aiRunRetryBudget
	aiRunRetryBudget = 1500 * time.Millisecond
	t.Cleanup(func() { aiRunRetryBudget = previousBudget })

	done := make(chan struct{})
	var exitedAt time.Time
	go func() {
		time.Sleep(aiRunLockWaitSlice / 2)
		exitedAt = time.Now()
		close(done)
	}()
	start := persistAIRunStart(databasePath, countAIRunTestEvents, aiRunChildExit{done: done, at: &exitedAt})
	<-done
	sinceExit := time.Since(exitedAt)
	if start.err == nil || !isAIRunDatabaseUnavailable(start.err) || !start.childExited {
		t.Fatalf("expected an unavailable start after child exit, got %+v", start)
	}
	if sinceExit > aiRunRetryBudget+time.Second {
		t.Fatalf("start retry ran %v after the child exited mid-attempt with a %v window", sinceExit, aiRunRetryBudget)
	}
}
