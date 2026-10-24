//go:build darwin || linux

package facts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestStartWaitsWhileAnotherProcessUpgradesTheDatabase(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	released := holdDatabaseMigration(t, initResult.DatabasePath, 4*time.Second)

	started := time.Now()
	output, err := runWorkgraphCommandWithTimeout(nil, 60*time.Second, "start", "--home", homeDir, "--database", initResult.DatabasePath)
	elapsed := time.Since(started)
	defer runWorkgraphCommand(t, nil, "stop", "--home", homeDir)
	<-released

	if err != nil {
		t.Fatalf("expected start to wait through the upgrade, got %v:\n%s", err, output)
	}
	upgrading := strings.Index(output, "Upgrading database…")
	ready := strings.Index(output, "workgraph capture started")
	if upgrading < 0 || ready < upgrading {
		t.Fatalf("expected upgrade progress before the ready message, got:\n%s", output)
	}
	if strings.Contains(output, "did not become ready") {
		t.Fatalf("expected no readiness failure, got:\n%s", output)
	}
	if elapsed < 3*time.Second {
		t.Fatalf("expected start to report ready only after the upgrade finished, returned after %s:\n%s", elapsed, output)
	}
	pid := captureDaemonPID(t, homeDir)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("expected capture worker %d to be running: %v", pid, err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "daemon.starting.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected startup progress file to be removed once ready, got %v", err)
	}
}

func TestStatusReportsCaptureStartingDuringDatabaseUpgrade(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	released := holdDatabaseMigration(t, initResult.DatabasePath, 4*time.Second)

	startDone := make(chan string, 1)
	go func() {
		output, _ := runWorkgraphCommandWithTimeout(nil, 60*time.Second, "start", "--home", homeDir, "--database", initResult.DatabasePath)
		startDone <- output
	}()
	defer runWorkgraphCommand(t, nil, "stop", "--home", homeDir)

	deadline := time.Now().Add(3 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		status = runWorkgraphCommand(t, nil, "status", "--home", homeDir)
		if strings.Contains(status, "starting (upgrading database)") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(status, "workgraph capture is starting (upgrading database)") {
		t.Fatalf("expected status to report the upgrade in progress, got:\n%s", status)
	}
	<-released
	if output := <-startDone; !strings.Contains(output, "workgraph capture started") {
		t.Fatalf("expected start to finish after the upgrade, got:\n%s", output)
	}
}

func TestStopStopsWorkerWaitingForDatabaseUpgrade(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	holdDatabaseMigration(t, initResult.DatabasePath, 6*time.Second)

	startDone := make(chan string, 1)
	go func() {
		output, _ := runWorkgraphCommandWithTimeout(nil, 60*time.Second, "start", "--home", homeDir, "--database", initResult.DatabasePath)
		startDone <- output
	}()
	var startup struct {
		PID int `json:"pid"`
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if contents, err := os.ReadFile(filepath.Join(homeDir, "daemon.starting.json")); err == nil && json.Unmarshal(contents, &startup) == nil && startup.PID > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if startup.PID == 0 {
		t.Fatal("expected the starting worker to record its startup progress")
	}

	stop := runWorkgraphCommand(t, nil, "stop", "--home", homeDir)
	if !strings.Contains(stop, "workgraph capture stopped") {
		t.Fatalf("expected stop to stop the starting worker, got:\n%s", stop)
	}
	waitForProcessExit(t, startup.PID)
	if output := <-startDone; strings.Contains(output, "workgraph capture started") {
		t.Fatalf("expected start not to report a stopped worker as started, got:\n%s", output)
	}
	status := runWorkgraphCommand(t, nil, "status", "--home", homeDir)
	if !strings.Contains(status, "not running") {
		t.Fatalf("expected capture to be stopped, got:\n%s", status)
	}
}

func TestStatusReportsDatabaseUpgradeWhileCaptureRuns(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	runWorkgraphCommand(t, nil, "start", "--home", homeDir, "--database", initResult.DatabasePath)
	defer runWorkgraphCommand(t, nil, "stop", "--home", homeDir)
	holdDatabaseMigration(t, initResult.DatabasePath, 2*time.Second)

	status := runWorkgraphCommand(t, nil, "status", "--home", homeDir)
	for _, expected := range []string{"workgraph capture is running", "Database upgrade in progress"} {
		if !strings.Contains(status, expected) {
			t.Fatalf("expected status to include %q, got:\n%s", expected, status)
		}
	}
}

func TestStartFailsPromptlyWhenWorkerExitsDuringStartup(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	captureLock, err := os.OpenFile(filepath.Join(homeDir, "capture.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open capture lock: %v", err)
	}
	defer captureLock.Close()
	if err := syscall.Flock(int(captureLock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("hold capture lock: %v", err)
	}

	started := time.Now()
	output, err := runWorkgraphCommandWithTimeout(nil, 30*time.Second, "start", "--home", homeDir, "--database", initResult.DatabasePath)
	elapsed := time.Since(started)

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("expected start to fail with exit code 1, got %v:\n%s", err, output)
	}
	for _, expected := range []string{"workgraph start: daemon did not become ready", "capture worker exited during startup", "daemon.log"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected start failure to include %q, got:\n%s", expected, output)
		}
	}
	if elapsed >= 1500*time.Millisecond {
		t.Fatalf("expected start to fail promptly after the worker exited, took %s", elapsed)
	}
}

func TestShortCommandsWaitForAnotherProcessDatabaseUpgrade(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	dbPath := filepath.Join(homeDir, "workgraph.db")
	hold := 7 * time.Second
	started := time.Now()
	released := holdDatabaseMigration(t, dbPath, hold)

	today, err := runworkgraph(t, repoRoot(t), "today", "--home", homeDir)
	if err != nil {
		t.Fatalf("expected today to read during the upgrade, got %v:\n%s", err, today)
	}
	if elapsed := time.Since(started); elapsed > hold-3*time.Second {
		t.Fatalf("expected today to read without waiting for the upgrade to release the database, took %s", elapsed)
	}
	if !strings.Contains(string(today), "Database upgrade in progress") {
		t.Fatalf("expected today to report the upgrade in progress, got:\n%s", today)
	}
	poll, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack")
	if err != nil {
		t.Fatalf("expected connector poll to wait for the upgrade, got %v:\n%s", err, poll)
	}
	if elapsed := time.Since(started); elapsed < hold-time.Second {
		t.Fatalf("expected the connector poll to finish only after the upgrade released the database, took %s", elapsed)
	}
	<-released
	if strings.Contains(string(poll), "database is locked") {
		t.Fatalf("expected no lock error, got:\n%s", poll)
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	var requests int
	if err := db.QueryRow(`SELECT COUNT(*) FROM capture_requests WHERE connector_id = 'slack'`).Scan(&requests); err != nil {
		t.Fatalf("count capture requests: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected the poll to emit one slack capture request after waiting, got %d", requests)
	}
}

func TestConnectorPollRetriesBriefWriteContention(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	db := openBridgedCaptureDatabase(t, homeDir)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("open connection: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("begin write transaction: %v", err)
	}
	committed := make(chan error, 1)
	go func() {
		time.Sleep(time.Second)
		_, err := conn.ExecContext(context.Background(), `COMMIT`)
		committed <- err
	}()

	poll, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack")
	if err := <-committed; err != nil {
		t.Fatalf("commit write transaction: %v", err)
	}
	if err != nil || strings.Contains(string(poll), "database is locked") {
		t.Fatalf("expected the poll to retry brief write contention, got %v:\n%s", err, poll)
	}
}

func TestNewAndUpgradedDatabasesUseWriteAheadLogging(t *testing.T) {
	root := t.TempDir()
	freshHome := filepath.Join(root, "fresh")
	fresh, err := workgraph.Init(workgraph.InitConfig{HomeDir: freshHome, MemoryDir: filepath.Join(root, "fresh-memory")})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if mode := journalMode(t, fresh.DatabasePath); mode != "wal" {
		t.Fatalf("expected a new database in WAL mode, got %q", mode)
	}

	upgradedHome := filepath.Join(root, "upgraded")
	if err := os.MkdirAll(upgradedHome, 0o700); err != nil {
		t.Fatal(err)
	}
	upgradedPath := filepath.Join(upgradedHome, "workgraph.db")
	legacy, err := sql.Open("sqlite3", upgradedPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`PRAGMA journal_mode = DELETE`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE events (id TEXT PRIMARY KEY, source TEXT NOT NULL, type TEXT NOT NULL, timestamp TEXT NOT NULL, payload_json TEXT NOT NULL, project TEXT, actor TEXT, summary TEXT, created_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	if mode := journalMode(t, upgradedPath); mode != "delete" {
		t.Fatalf("expected the legacy fixture in rollback-journal mode, got %q", mode)
	}

	if output, err := runworkgraph(t, repoRoot(t), "init", "--home", upgradedHome); err != nil {
		t.Fatalf("reopen legacy database: %v\n%s", err, output)
	}
	if mode := journalMode(t, upgradedPath); mode != "wal" {
		t.Fatalf("expected the upgraded database in WAL mode, got %q", mode)
	}
	doctor, err := runworkgraph(t, repoRoot(t), "doctor", "--home", upgradedHome)
	if err != nil {
		t.Fatalf("workgraph doctor failed: %v\n%s", err, doctor)
	}
	if !strings.Contains(string(doctor), "Database: ok (journal mode: wal)") {
		t.Fatalf("expected doctor to report WAL journal mode, got:\n%s", doctor)
	}

	readOnly, err := sql.Open("sqlite3", "file:"+upgradedPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	var tables int
	if err := readOnly.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'events'`).Scan(&tables); err != nil || tables != 1 {
		t.Fatalf("expected read-only access to the WAL database, got %d tables, err %v", tables, err)
	}
}

func TestBackgroundCaptureIgnoresDatabaseSidecarFiles(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(watchDir, "events.db")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, DatabasePath: dbPath}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	runWorkgraphCommand(t, nil, "start", "--home", homeDir, "--database", dbPath, "--watch", watchDir)
	defer runWorkgraphCommand(t, nil, "stop", "--home", homeDir)

	for index := range 3 {
		visible := filepath.Join(watchDir, "visible-"+string(rune('a'+index))+".md")
		if err := os.WriteFile(visible, []byte("visible"), 0o644); err != nil {
			t.Fatal(err)
		}
		waitForEvent(t, dbPath, "created", visible)
	}
	time.Sleep(300 * time.Millisecond)

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var sidecarEvents int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE source = 'file' AND json_extract(payload_json, '$.path') LIKE ?`, dbPath+"%").Scan(&sidecarEvents); err != nil {
		t.Fatal(err)
	}
	if sidecarEvents != 0 {
		t.Fatalf("expected no file events for the database or its sidecar files, got %d", sidecarEvents)
	}
}

func holdDatabaseMigration(t *testing.T, dbPath string, hold time.Duration) <-chan struct{} {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dbPath)
	if err != nil {
		t.Fatalf("resolve database path: %v", err)
	}
	lock, err := os.OpenFile(resolved+"-migration.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open migration lock: %v", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("hold migration lock: %v", err)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("open migration connection: %v", err)
	}
	if _, err := conn.ExecContext(context.Background(), `BEGIN EXCLUSIVE`); err != nil {
		t.Fatalf("begin migration transaction: %v", err)
	}
	if _, err := conn.ExecContext(context.Background(), `CREATE TABLE fact_migration_marker (id INTEGER)`); err != nil {
		t.Fatalf("write during migration: %v", err)
	}
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(hold)
		_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		_ = conn.Close()
		_ = db.Close()
		_ = lock.Close()
	}()
	t.Cleanup(func() { <-released })
	return released
}

func journalMode(t *testing.T, dbPath string) string {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	return mode
}
