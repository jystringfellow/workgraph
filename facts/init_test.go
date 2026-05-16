package facts

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	workgraph "github.com/jystringfellow/workgraph"
)

// First implementation slice: replace the home and database placeholders with
// executable assertions, verify they fail, then implement only enough init
// behavior to pass them before expanding the slice.

func TestInitCreatesWorkGraphHome(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")

	_, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	info, err := os.Stat(homeDir)
	if err != nil {
		t.Fatalf("expected WorkGraph home to exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected WorkGraph home to be a directory")
	}
}

func TestInitCreatesSQLiteDatabase(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")

	_, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	dbPath := filepath.Join(homeDir, "workgraph.db")
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("expected SQLite database to exist: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("expected SQLite database path to be a file")
	}

	header := make([]byte, len("SQLite format 3\x00"))
	dbFile, err := os.Open(dbPath)
	if err != nil {
		t.Fatalf("expected SQLite database to be readable: %v", err)
	}
	defer dbFile.Close()

	if _, err := dbFile.Read(header); err != nil {
		t.Fatalf("expected SQLite database header to be readable: %v", err)
	}
	if !bytes.Equal(header, []byte("SQLite format 3\x00")) {
		t.Fatalf("expected SQLite database header, got %q", header)
	}
}

func TestInitCreatesMemoryRepo(t *testing.T) {
	tempDir := t.TempDir()
	memoryDir := filepath.Join(tempDir, "workgraph-memory")

	_, err := workgraph.Init(workgraph.InitConfig{
		HomeDir:   filepath.Join(tempDir, ".workgraph"),
		MemoryDir: memoryDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	info, err := os.Stat(memoryDir)
	if err != nil {
		t.Fatalf("expected memory repo to exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected memory repo to be a directory")
	}
}

func TestInitIsIdempotent(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")

	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir:   homeDir,
		MemoryDir: memoryDir,
	})
	if err != nil {
		t.Fatalf("initial init failed: %v", err)
	}

	db, err := sql.Open("sqlite3", result.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"event-1",
		"file",
		"file.modified",
		"2026-05-16T12:00:00Z",
		`{"path":"README.md"}`,
		"2026-05-16T12:00:01Z",
	)
	if err != nil {
		t.Fatalf("insert existing event: %v", err)
	}

	memoryPath := filepath.Join(memoryDir, "note.md")
	if err := os.WriteFile(memoryPath, []byte("existing memory"), 0o644); err != nil {
		t.Fatalf("write existing memory file: %v", err)
	}

	_, err = workgraph.Init(workgraph.InitConfig{
		HomeDir:   homeDir,
		MemoryDir: memoryDir,
	})
	if err != nil {
		t.Fatalf("second init failed: %v", err)
	}

	var eventCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE id = ?`, "event-1").Scan(&eventCount); err != nil {
		t.Fatalf("query existing event: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("expected existing event to be preserved, got count %d", eventCount)
	}

	memory, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatalf("read existing memory file: %v", err)
	}
	if string(memory) != "existing memory" {
		t.Fatalf("expected existing memory file to be preserved, got %q", memory)
	}
}

func TestInitReportsInitializedPaths(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")

	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir:   homeDir,
		MemoryDir: memoryDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	if result.HomeDir != homeDir {
		t.Fatalf("expected home path %q, got %q", homeDir, result.HomeDir)
	}
	if result.DatabasePath != filepath.Join(homeDir, "workgraph.db") {
		t.Fatalf("expected database path under home, got %q", result.DatabasePath)
	}
	if result.MemoryDir != memoryDir {
		t.Fatalf("expected memory path %q, got %q", memoryDir, result.MemoryDir)
	}
	if !strings.Contains(result.Message, result.HomeDir) {
		t.Fatalf("expected result message to include home path, got %q", result.Message)
	}
	if !strings.Contains(result.Message, result.DatabasePath) {
		t.Fatalf("expected result message to include database path, got %q", result.Message)
	}
	if !strings.Contains(result.Message, result.MemoryDir) {
		t.Fatalf("expected result message to include memory path, got %q", result.Message)
	}
}
