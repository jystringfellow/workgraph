package facts

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestFileEventInfersProjectFromNearestGitRoot(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "Code")
	repoDir := filepath.Join(watchDir, "Cupcake")
	sourceDir := filepath.Join(repoDir, "Cupcake.API")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatalf("create git metadata dir: %v", err)
	}
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("create source dir: %v", err)
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

	target := filepath.Join(sourceDir, "OrdersController.cs")
	if err := os.WriteFile(target, []byte("controller"), 0o644); err != nil {
		t.Fatalf("create source file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", target)

	project := projectForEvent(t, initResult.DatabasePath, "created", target)
	if project != "Cupcake" {
		t.Fatalf("expected project %q from nearest git root, got %q", "Cupcake", project)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestFileEventFallsBackToConfiguredWatchRoot(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "Downloads")
	notesDir := filepath.Join(watchDir, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("create notes dir: %v", err)
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

	target := filepath.Join(notesDir, "scratch.md")
	if err := os.WriteFile(target, []byte("notes"), 0o644); err != nil {
		t.Fatalf("create note: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", target)

	project := projectForEvent(t, initResult.DatabasePath, "created", target)
	if project != "Downloads" {
		t.Fatalf("expected project %q from configured watch root, got %q", "Downloads", project)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestFileEventPreservesArtifactPath(t *testing.T) {
	t.Skip("TBD: file event payload preserves the changed file path as artifact identity")
}

func TestAssociatedSessionsUseProjectAndTime(t *testing.T) {
	t.Skip("TBD: event sessions group nearby events from the same project deterministically")
}

func projectForEvent(t *testing.T, dbPath, operation, path string) string {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var project string
	err = db.QueryRow(`
		SELECT project
		FROM events
		WHERE source = 'file'
			AND type = ?
			AND json_extract(payload_json, '$.operation') = ?
			AND json_extract(payload_json, '$.path') = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, "file."+operation, operation, path).Scan(&project)
	if err != nil {
		t.Fatalf("query event project: %v", err)
	}

	return project
}
