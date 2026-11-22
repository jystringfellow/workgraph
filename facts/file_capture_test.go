package facts

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestFileWatcherRecordsFileChangeEvent(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	_, done, stop := startIgnoreSuggestionCapture(t, homeDir, initResult.DatabasePath, watchDir)
	defer stopIgnoreSuggestionCapture(t, stop, done)

	target := filepath.Join(watchDir, "plan.md")
	if err := os.WriteFile(target, []byte("initial"), 0o644); err != nil {
		t.Fatalf("write watched file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", target)

	if err := os.WriteFile(target, []byte("updated"), 0o644); err != nil {
		t.Fatalf("modify watched file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "modified", target)

	assertEventExists(t, initResult.DatabasePath, "created", target)
	assertEventExists(t, initResult.DatabasePath, "modified", target)
}

func TestFileWatcherIgnoresworkgraphInternalFiles(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("create home dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	_, done, stop := startIgnoreSuggestionCapture(t, homeDir, initResult.DatabasePath, tempDir)
	defer stopIgnoreSuggestionCapture(t, stop, done)

	internalFile := filepath.Join(homeDir, "internal-state.json")
	if err := os.WriteFile(internalFile, []byte(`{"internal":true}`), 0o600); err != nil {
		t.Fatalf("write internal file: %v", err)
	}

	visibleFile := filepath.Join(tempDir, "notes.md")
	if err := os.WriteFile(visibleFile, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write visible file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", visibleFile)
	assertNoRunEvent(t, initResult.DatabasePath, "created", internalFile)

	db, err := sql.Open("sqlite3", initResult.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT json_extract(payload_json, '$.path') FROM events WHERE source = 'file'`)
	if err != nil {
		t.Fatalf("query file events: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var recordedPath string
		if err := rows.Scan(&recordedPath); err != nil {
			t.Fatalf("scan event path: %v", err)
		}
		if recordedPath == homeDir || strings.HasPrefix(recordedPath, homeDir+string(filepath.Separator)) {
			t.Fatalf("expected internal workgraph file to be ignored, got event for %q", recordedPath)
		}
		if strings.HasPrefix(recordedPath, initResult.DatabasePath) {
			t.Fatalf("expected database file to be ignored, got event for %q", recordedPath)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate file events: %v", err)
	}
}

func TestFileEventIncludesPathAndOperation(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	_, done, stop := startIgnoreSuggestionCapture(t, homeDir, initResult.DatabasePath, watchDir)
	defer stopIgnoreSuggestionCapture(t, stop, done)

	target := filepath.Join(watchDir, "document.md")
	if err := os.WriteFile(target, []byte("v1"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", target)

	if err := os.WriteFile(target, []byte("v2"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "modified", target)

	if err := os.Remove(target); err != nil {
		t.Fatalf("delete file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "deleted", target)

	db, err := sql.Open("sqlite3", initResult.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	for _, op := range []string{"created", "modified", "deleted"} {
		var payloadJSON string
		err := db.QueryRow(`
			SELECT payload_json
			FROM events
			WHERE source = 'file'
				AND type = ?
				AND json_extract(payload_json, '$.path') = ?
				AND json_extract(payload_json, '$.operation') = ?
			LIMIT 1
		`, "file."+op, target, op).Scan(&payloadJSON)
		if err != nil {
			t.Fatalf("expected file.%s event with path and operation, got: %v", op, err)
		}

		var payload struct {
			Path      string `json:"path"`
			Operation string `json:"operation"`
		}
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload for %s: %v", op, err)
		}
		if payload.Path != target || payload.Operation != op {
			t.Fatalf("expected payload path=%q operation=%q, got %#v", target, op, payload)
		}
	}
}

func TestFileEventInfersProjectFromGitRepoOrFolder(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	workspaceDir := filepath.Join(tempDir, "workspace")
	gitRepoDir := filepath.Join(workspaceDir, "Cupcake", "pkg")
	plainFolderDir := filepath.Join(tempDir, "Notes")
	if err := os.MkdirAll(filepath.Join(workspaceDir, "Cupcake", ".git"), 0o755); err != nil {
		t.Fatalf("create .git dir: %v", err)
	}
	if err := os.MkdirAll(gitRepoDir, 0o755); err != nil {
		t.Fatalf("create repo subdir: %v", err)
	}
	if err := os.MkdirAll(plainFolderDir, 0o755); err != nil {
		t.Fatalf("create plain folder: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{workspaceDir, plainFolderDir},
		PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start capture: %v", err)
	}
	done := make(chan error, 1)
	ctx, stop := context.WithCancel(context.Background())
	go func() {
		done <- capture.Run(ctx)
	}()
	defer stopIgnoreSuggestionCapture(t, stop, done)

	repoFile := filepath.Join(gitRepoDir, "server.go")
	if err := os.WriteFile(repoFile, []byte("package pkg\n"), 0o644); err != nil {
		t.Fatalf("write repo file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", repoFile)

	folderFile := filepath.Join(plainFolderDir, "todo.md")
	if err := os.WriteFile(folderFile, []byte("# Todo\n"), 0o644); err != nil {
		t.Fatalf("write folder file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", folderFile)

	db, err := sql.Open("sqlite3", initResult.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var repoProject string
	if err := db.QueryRow(`SELECT project FROM events WHERE source = 'file' AND json_extract(payload_json, '$.path') = ? LIMIT 1`, repoFile).Scan(&repoProject); err != nil {
		t.Fatalf("query repo file project: %v", err)
	}
	if repoProject != "Cupcake" {
		t.Fatalf("expected git repo project %q, got %q", "Cupcake", repoProject)
	}

	var folderProject string
	if err := db.QueryRow(`SELECT project FROM events WHERE source = 'file' AND json_extract(payload_json, '$.path') = ? LIMIT 1`, folderFile).Scan(&folderProject); err != nil {
		t.Fatalf("query folder file project: %v", err)
	}
	if folderProject != "Notes" {
		t.Fatalf("expected folder project %q, got %q", "Notes", folderProject)
	}
}
