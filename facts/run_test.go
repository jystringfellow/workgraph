package facts

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
	_ "github.com/mattn/go-sqlite3"
)

func TestRunStartsEventCaptureAfterInit(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
		PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	defer capture.Close()

	if !strings.Contains(capture.Status.Message, "capture is running") {
		t.Fatalf("expected capture running message, got %q", capture.Status.Message)
	}
	if len(capture.Status.WatchDirs) != 1 || capture.Status.WatchDirs[0] != watchDir {
		t.Fatalf("expected run to watch %q, got %#v", watchDir, capture.Status.WatchDirs)
	}
}

func TestRunRefusesBeforeInit(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")

	_, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:   homeDir,
		WatchDirs: []string{t.TempDir()},
	})
	if err == nil {
		t.Fatalf("expected run to fail before init")
	}
	if !errors.Is(err, workgraph.ErrNotInitialized) {
		t.Fatalf("expected ErrNotInitialized, got %v", err)
	}
	if !strings.Contains(err.Error(), "workgraph init") {
		t.Fatalf("expected init guidance, got %q", err.Error())
	}
}

func TestRunCapturesFileActivity(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
		PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	target := filepath.Join(watchDir, "notes.md")
	if err := os.WriteFile(target, []byte("first"), 0o644); err != nil {
		t.Fatalf("create watched file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", target)

	if err := os.WriteFile(target, []byte("second"), 0o644); err != nil {
		t.Fatalf("modify watched file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "modified", target)

	if err := os.Remove(target); err != nil {
		t.Fatalf("delete watched file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "deleted", target)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunReportsCapturedFileEvents(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	target := filepath.Join(watchDir, "visible.md")
	if err := os.WriteFile(target, []byte("show me"), 0o644); err != nil {
		t.Fatalf("create watched file: %v", err)
	}

	event := waitForReportedEvent(t, capture.Events, "created", target)
	if event.Type != "file.created" {
		t.Fatalf("expected file.created event, got %q", event.Type)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunCapturesRapidFileLifecycleWithoutLosingTheFile(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	target := filepath.Join(watchDir, "quick.md")
	if err := os.WriteFile(target, []byte("first"), 0o644); err != nil {
		t.Fatalf("create watched file: %v", err)
	}
	if err := os.WriteFile(target, []byte("second"), 0o644); err != nil {
		t.Fatalf("modify watched file: %v", err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatalf("delete watched file: %v", err)
	}

	waitForEvent(t, initResult.DatabasePath, "created", target)
	waitForEvent(t, initResult.DatabasePath, "deleted", target)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunPreservesWrittenEventsWhenStopped(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
		PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	target := filepath.Join(watchDir, "kept.md")
	if err := os.WriteFile(target, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("create watched file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", target)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	assertEventExists(t, initResult.DatabasePath, "created", target)
}

func waitForEvent(t *testing.T, dbPath, operation, path string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if eventExists(t, dbPath, operation, path) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s event for %q", operation, path)
}

func assertEventExists(t *testing.T, dbPath, operation, path string) {
	t.Helper()

	if !eventExists(t, dbPath, operation, path) {
		t.Fatalf("expected %s event for %q to be preserved", operation, path)
	}
}

func eventExists(t *testing.T, dbPath, operation, path string) bool {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var count int
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM events
		WHERE source = 'file'
			AND type = ?
			AND json_extract(payload_json, '$.operation') = ?
			AND json_extract(payload_json, '$.path') = ?
	`, "file."+operation, operation, path).Scan(&count)
	if err != nil {
		t.Fatalf("query event: %v", err)
	}

	return count > 0
}

func waitForReportedEvent(t *testing.T, events <-chan workgraph.CapturedEvent, operation, path string) workgraph.CapturedEvent {
	t.Helper()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Operation == operation && event.Path == path {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for reported %s event for %q", operation, path)
		}
	}
}
