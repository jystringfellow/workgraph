package facts

import (
	"database/sql"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
	_ "github.com/mattn/go-sqlite3"
)

func TestTodayReturnsEventsFromCurrentDay(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Date(2026, 5, 17, 15, 30, 0, 0, time.FixedZone("test-local", -7*60*60))
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "today-readme",
		Type:      "file.modified",
		Timestamp: now.Add(-2 * time.Hour),
		Project:   "workgraph",
		Payload:   `{"path":"/tmp/workgraph/README.md","operation":"modified"}`,
	})
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "yesterday-notes",
		Type:      "file.modified",
		Timestamp: now.AddDate(0, 0, -1),
		Project:   "workgraph",
		Payload:   `{"path":"/tmp/workgraph/yesterday.md","operation":"modified"}`,
	})

	today, err := workgraph.Today(workgraph.TodayConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Now:          now,
	})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}

	if !strings.Contains(today.Message, "README.md") {
		t.Fatalf("expected today's event in output, got:\n%s", today.Message)
	}
	if strings.Contains(today.Message, "yesterday.md") {
		t.Fatalf("expected previous-day event to be omitted, got:\n%s", today.Message)
	}
}

func TestTodayGroupsEventsIntoSessions(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Date(2026, 5, 17, 17, 0, 0, 0, time.Local)
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "morning-1",
		Type:      "file.created",
		Timestamp: time.Date(2026, 5, 17, 9, 0, 0, 0, time.Local),
		Project:   "alpha",
		Payload:   `{"path":"/tmp/alpha/plan.md","operation":"created"}`,
	})
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "morning-2",
		Type:      "file.modified",
		Timestamp: time.Date(2026, 5, 17, 9, 20, 0, 0, time.Local),
		Project:   "alpha",
		Payload:   `{"path":"/tmp/alpha/plan.md","operation":"modified"}`,
	})
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "afternoon-1",
		Type:      "file.modified",
		Timestamp: time.Date(2026, 5, 17, 12, 30, 0, 0, time.Local),
		Project:   "alpha",
		Payload:   `{"path":"/tmp/alpha/notes.md","operation":"modified"}`,
	})

	today, err := workgraph.Today(workgraph.TodayConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Now:          now,
	})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}

	if !strings.Contains(today.Message, "Sessions") {
		t.Fatalf("expected Sessions section, got:\n%s", today.Message)
	}
	if !strings.Contains(today.Message, "09:00-09:20 alpha (2 events)") {
		t.Fatalf("expected nearby events to share one session, got:\n%s", today.Message)
	}
	if !strings.Contains(today.Message, "12:30 alpha (1 event)") {
		t.Fatalf("expected distant event to start a new session, got:\n%s", today.Message)
	}
}

func TestTodayGroupsSessionsByProject(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Date(2026, 5, 17, 18, 0, 0, 0, time.Local)
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "alpha-event",
		Type:      "file.modified",
		Timestamp: time.Date(2026, 5, 17, 10, 0, 0, 0, time.Local),
		Project:   "alpha",
		Payload:   `{"path":"/tmp/alpha/a.md","operation":"modified"}`,
	})
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "beta-event",
		Type:      "file.created",
		Timestamp: time.Date(2026, 5, 17, 11, 0, 0, 0, time.Local),
		Project:   "beta",
		Payload:   `{"path":"/tmp/beta/b.md","operation":"created"}`,
	})

	today, err := workgraph.Today(workgraph.TodayConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Now:          now,
	})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}

	alphaIndex := strings.Index(today.Message, "- alpha: 1 event")
	betaIndex := strings.Index(today.Message, "- beta: 1 event")
	if alphaIndex == -1 || betaIndex == -1 {
		t.Fatalf("expected project event counts, got:\n%s", today.Message)
	}
	if alphaIndex > betaIndex {
		t.Fatalf("expected projects to be listed deterministically, got:\n%s", today.Message)
	}
}

func TestTodayShowsUnfinishedWorkWhenKnown(t *testing.T) {
	t.Skip("TBD: today command shows unfinished work when tasks or TODOs are known")
}

func TestTodayOutputIncludesExpectedSections(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Now()
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "section-event",
		Type:      "file.modified",
		Timestamp: now.Add(-time.Hour),
		Project:   "workgraph",
		Payload:   `{"path":"/tmp/workgraph/today.md","operation":"modified"}`,
	})

	cmd := exec.Command(
		"go",
		"run",
		"./cmd/workgraph",
		"today",
		"--home",
		homeDir,
		"--database",
		result.DatabasePath,
	)
	cmd.Dir = repoRoot(t)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workgraph today failed: %v\n%s", err, output)
	}

	for _, expected := range []string{"Today", "Projects", "Sessions", "workgraph", "today.md"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected today output to include %q, got:\n%s", expected, output)
		}
	}
}

func TestTodayShowsEmptyStateWhenNoEventsExist(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	today, err := workgraph.Today(workgraph.TodayConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Now:          time.Date(2026, 5, 17, 9, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}

	if !strings.Contains(today.Message, "Today") {
		t.Fatalf("expected Today section, got:\n%s", today.Message)
	}
	if !strings.Contains(today.Message, "No activity has been captured today.") {
		t.Fatalf("expected empty state, got:\n%s", today.Message)
	}
	if strings.Contains(today.Message, "Projects") || strings.Contains(today.Message, "Sessions") {
		t.Fatalf("expected empty state to omit data sections, got:\n%s", today.Message)
	}
}

func TestTodayOutputIsPlainTextWithoutLLM(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Date(2026, 5, 17, 14, 0, 0, 0, time.Local)
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "deterministic-event",
		Type:      "file.modified",
		Timestamp: now.Add(-time.Hour),
		Project:   "workgraph",
		Payload:   `{"path":"/tmp/workgraph/plain.md","operation":"modified"}`,
	})

	config := workgraph.TodayConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Now:          now,
	}
	first, err := workgraph.Today(config)
	if err != nil {
		t.Fatalf("first today failed: %v", err)
	}
	second, err := workgraph.Today(config)
	if err != nil {
		t.Fatalf("second today failed: %v", err)
	}

	if first.Message != second.Message {
		t.Fatalf("expected deterministic output, got:\nfirst:\n%s\nsecond:\n%s", first.Message, second.Message)
	}
	if strings.Contains(first.Message, "```") || strings.Contains(first.Message, "<") {
		t.Fatalf("expected simple plain text output, got:\n%s", first.Message)
	}
}

type storedEvent struct {
	ID        string
	Type      string
	Timestamp time.Time
	Project   string
	Payload   string
}

func insertEvent(t *testing.T, dbPath string, event storedEvent) {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`INSERT INTO events
		(id, source, type, timestamp, payload_json, project, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		"file",
		event.Type,
		event.Timestamp.Format(time.RFC3339Nano),
		event.Payload,
		event.Project,
		event.Timestamp.Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("insert event %q: %v", event.ID, err)
	}
}
