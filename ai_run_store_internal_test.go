package workgraph

import (
	"context"
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

func takeAIRunTestWriteLock(db aiRunConnection) error {
	if _, err := db.Exec(`BEGIN IMMEDIATE`); err != nil {
		return err
	}
	_, err := db.Exec(`ROLLBACK`)
	return err
}

const aiRunTestWatchdog = 10 * time.Second

type aiRunTestAttempt struct {
	deadline  time.Time
	startedAt time.Time
	lockWait  time.Duration
	refused   bool
}

type aiRunAttemptRecorder struct {
	mu       sync.Mutex
	attempts []aiRunTestAttempt
	opens    []time.Time
}

func recordAIRunAttempts(t *testing.T) *aiRunAttemptRecorder {
	recorder := &aiRunAttemptRecorder{}
	previousOpen := aiRunOpenDatabase
	aiRunOpenDatabase = func(databasePath string, busyTimeout time.Duration) (*sql.DB, error) {
		recorder.mu.Lock()
		recorder.opens = append(recorder.opens, time.Now())
		recorder.mu.Unlock()
		return previousOpen(databasePath, busyTimeout)
	}
	t.Cleanup(func() { aiRunOpenDatabase = previousOpen })
	return recorder
}

func (recorder *aiRunAttemptRecorder) takeWriteLock(connection aiRunConnection) error {
	attempt := aiRunTestAttempt{deadline: connection.deadline, startedAt: time.Now()}
	err := takeAIRunTestWriteLock(connection)
	attempt.refused = errors.Is(err, context.DeadlineExceeded)
	var millis int64
	if scanErr := connection.conn.QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&millis); scanErr == nil {
		attempt.lockWait = time.Duration(millis) * time.Millisecond
	}
	recorder.mu.Lock()
	recorder.attempts = append(recorder.attempts, attempt)
	recorder.mu.Unlock()
	return err
}

func (recorder *aiRunAttemptRecorder) recorded() []aiRunTestAttempt {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]aiRunTestAttempt(nil), recorder.attempts...)
}

func (recorder *aiRunAttemptRecorder) opensFrom(at time.Time) int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	count := 0
	for _, opened := range recorder.opens {
		if !opened.Before(at) {
			count++
		}
	}
	return count
}

func requireAIRunRetriesStopAt(t *testing.T, recorder *aiRunAttemptRecorder, attempts []aiRunTestAttempt, windowEnd time.Time) {
	t.Helper()
	if len(attempts) == 0 {
		t.Fatalf("expected attempts within the window, got none")
	}
	for index, attempt := range attempts {
		if !attempt.deadline.Equal(windowEnd) {
			t.Fatalf("attempt %d ran against deadline %s, want the window end %s", index, attempt.deadline, windowEnd)
		}
		if !attempt.refused && attempt.lockWait > aiRunLockWaitSlice {
			t.Fatalf("attempt %d waited up to %v for the lock, longer than one %v slice", index, attempt.lockWait, aiRunLockWaitSlice)
		}
	}
	if late := recorder.opensFrom(windowEnd); late > 1 {
		t.Fatalf("expected retries to stop at the window end, but %d attempts opened the database after it", late)
	}
}

func TestAIRunDatabaseRetryGivesUpWithinItsDeadline(t *testing.T) {
	databasePath := initAIRunTestDatabase(t)
	holdAIRunTestExclusiveLock(t, databasePath)

	budget := 1500 * time.Millisecond
	began := time.Now()
	deadline := began.Add(budget)
	recorder := recordAIRunAttempts(t)
	err := retryAIRunDatabase(databasePath, deadline, recorder.takeWriteLock)
	elapsed := time.Since(began)
	if err == nil || !isAIRunDatabaseUnavailable(err) {
		t.Fatalf("expected an unavailable-database error, got %v", err)
	}
	if elapsed < budget-aiRunLockWaitSlice || elapsed > budget+aiRunTestWatchdog {
		t.Fatalf("retry ran %v; expected it to keep retrying until its %v deadline", elapsed, budget)
	}
	requireAIRunRetriesStopAt(t, recorder, recorder.recorded(), deadline)
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
	recorder := recordAIRunAttempts(t)
	start := persistAIRunStart(databasePath, recorder.takeWriteLock, aiRunChildExit{done: done, at: &exitedAt})
	elapsed := time.Since(exitedAt)
	if start.err == nil || !isAIRunDatabaseUnavailable(start.err) || !start.childExited {
		t.Fatalf("expected an unavailable start after child exit, got %+v", start)
	}
	if elapsed < aiRunRetryBudget-aiRunLockWaitSlice || elapsed > aiRunRetryBudget+aiRunTestWatchdog {
		t.Fatalf("start retry ran %v after child exit; expected it to retry until its %v window ended", elapsed, aiRunRetryBudget)
	}
	requireAIRunRetriesStopAt(t, recorder, recorder.recorded(), exitedAt.Add(aiRunRetryBudget))
}

func TestAIRunDatabaseDeadlineCoversLocksTakenAfterConnecting(t *testing.T) {
	databasePath := initAIRunTestDatabase(t)
	deadline := time.Now().Add(2 * aiRunLockWaitSlice)
	var lockedAt time.Time
	aiRunAfterConnect = func() {
		time.Sleep(time.Until(deadline) - aiRunLockWaitSlice/2)
		t.Cleanup(takeAIRunTestExclusiveLock(t, databasePath))
		lockedAt = time.Now()
	}
	t.Cleanup(func() { aiRunAfterConnect = nil })

	recorder := recordAIRunAttempts(t)
	err := withAIRunDatabase(databasePath, deadline, recorder.takeWriteLock)
	if err == nil || !isAIRunDatabaseUnavailable(err) {
		t.Fatalf("expected an unavailable-database error, got %v", err)
	}
	if returnedAt := time.Now(); returnedAt.After(deadline.Add(aiRunTestWatchdog)) {
		t.Fatalf("statement returned %v after its deadline", returnedAt.Sub(deadline))
	}
	attempts := recorder.recorded()
	if len(attempts) != 1 {
		t.Fatalf("expected one attempt, got %d", len(attempts))
	}
	if attempt := attempts[0]; !attempt.refused && attempt.lockWait > deadline.Sub(lockedAt)+time.Millisecond {
		t.Fatalf("statement could wait %v for a lock taken %v before its deadline", attempt.lockWait, deadline.Sub(lockedAt))
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
	recorder := recordAIRunAttempts(t)
	start := persistAIRunStart(databasePath, recorder.takeWriteLock, aiRunChildExit{done: done, at: &exitedAt})
	<-done
	sinceExit := time.Since(exitedAt)
	if start.err == nil || !isAIRunDatabaseUnavailable(start.err) || !start.childExited {
		t.Fatalf("expected an unavailable start after child exit, got %+v", start)
	}
	if sinceExit > aiRunRetryBudget+aiRunTestWatchdog {
		t.Fatalf("start retry ran %v after the child exited mid-attempt with a %v window", sinceExit, aiRunRetryBudget)
	}
	windowEnd := exitedAt.Add(aiRunRetryBudget)
	var afterExit []aiRunTestAttempt
	staleWindows := 0
	for _, attempt := range recorder.recorded() {
		if attempt.startedAt.Before(exitedAt) {
			continue
		}
		if attempt.deadline.Equal(windowEnd) {
			afterExit = append(afterExit, attempt)
		} else {
			staleWindows++
		}
	}
	if staleWindows > 1 {
		t.Fatalf("expected attempts after the child exit to use the window from the exit, but %d used an earlier window", staleWindows)
	}
	requireAIRunRetriesStopAt(t, recorder, afterExit, windowEnd)
}
