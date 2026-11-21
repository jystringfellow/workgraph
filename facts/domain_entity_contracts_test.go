package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestEventRequiresIDSourceTypeTimestampPayload(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir:   homeDir,
		MemoryDir: filepath.Join(tempDir, "workgraph-memory"),
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	eventTime := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	db := openIgnoreSuggestionDB(t, result.DatabasePath)
	defer db.Close()

	_, err = db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"evt-required-ok",
		"file",
		"file.modified",
		eventTime.Format(time.RFC3339),
		`{"path":"README.md","operation":"modified"}`,
		eventTime.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("expected event with required fields to succeed: %v", err)
	}

	missingCases := []struct {
		name string
		args []any
	}{
		{
			name: "duplicate id",
			args: []any{"evt-required-ok", "file", "file.modified", "2026-05-25T12:00:00Z", `{"path":"README.md"}`, "2026-05-25T12:00:00Z"},
		},
		{
			name: "missing source",
			args: []any{"evt-no-source", nil, "file.modified", "2026-05-25T12:00:00Z", `{"path":"README.md"}`, "2026-05-25T12:00:00Z"},
		},
		{
			name: "missing type",
			args: []any{"evt-no-type", "file", nil, "2026-05-25T12:00:00Z", `{"path":"README.md"}`, "2026-05-25T12:00:00Z"},
		},
		{
			name: "missing timestamp",
			args: []any{"evt-no-ts", "file", "file.modified", nil, `{"path":"README.md"}`, "2026-05-25T12:00:00Z"},
		},
		{
			name: "missing payload_json",
			args: []any{"evt-no-payload", "file", "file.modified", "2026-05-25T12:00:00Z", nil, "2026-05-25T12:00:00Z"},
		},
	}

	for _, tc := range missingCases {
		_, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, tc.args...)
		if err == nil {
			t.Fatalf("expected %s to be rejected", tc.name)
		}
	}

	today, err := workgraph.Today(workgraph.TodayConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Now:          eventTime,
	})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}
	if len(today.Events) != 1 {
		t.Fatalf("expected 1 today event, got %d", len(today.Events))
	}
	ev := today.Events[0]
	if ev.ID == "" || ev.Source == "" || ev.Type == "" || ev.Timestamp.IsZero() || ev.Payload == "" {
		t.Fatalf("expected event domain fields id, source, type, timestamp, payload, got %#v", ev)
	}
}

func TestEventCanIncludeProjectActorAndSummary(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir:   homeDir,
		MemoryDir: filepath.Join(tempDir, "workgraph-memory"),
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	eventTime := time.Date(2026, 5, 25, 14, 30, 0, 0, time.UTC)
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "evt-optional-metadata",
		Source:    "git",
		Type:      "git.commit",
		Timestamp: eventTime,
		Project:   "Cupcake",
		Actor:     "dev@example.test",
		Summary:   "Implement auth middleware",
		Payload:   `{"repo_path":"/tmp/Cupcake","commit":"abc123","subject":"Implement auth middleware"}`,
	})

	today, err := workgraph.Today(workgraph.TodayConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Now:          eventTime,
	})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}
	if len(today.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(today.Events))
	}
	got := today.Events[0]
	if got.Project != "Cupcake" || got.Actor != "dev@example.test" || got.Summary != "Implement auth middleware" {
		t.Fatalf("expected project, actor, and summary metadata preserved, got %#v", got)
	}
}

func TestEventPayloadMustBeValidJSON(t *testing.T) {
	db := openContractDatabase(t)

	_, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"evt-invalid-payload",
		"file",
		"file.modified",
		"2026-05-25T12:00:00Z",
		`{invalid-json`,
		"2026-05-25T12:00:00Z",
	)
	if err == nil {
		t.Fatalf("expected invalid JSON payload to be rejected")
	}

	validPayload := `{"path":"/tmp/Cupcake/README.md","operation":"modified"}`
	_, err = db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"evt-valid-payload",
		"file",
		"file.modified",
		"2026-05-25T12:00:00Z",
		validPayload,
		"2026-05-25T12:00:00Z",
	)
	if err != nil {
		t.Fatalf("expected valid JSON payload to succeed: %v", err)
	}

	var storedPayload string
	if err := db.QueryRow(`SELECT payload_json FROM events WHERE id = ?`, "evt-valid-payload").Scan(&storedPayload); err != nil {
		t.Fatalf("read stored payload: %v", err)
	}
	if !json.Valid([]byte(storedPayload)) {
		t.Fatalf("expected stored payload_json to be valid JSON, got %q", storedPayload)
	}
}

func TestEventTimestampUsesRFC3339(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoDir := filepath.Join(tempDir, "Code", "Cupcake")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	commitSHA := createGitCommit(t, repoDir, "Add RFC3339 timestamp test")

	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if _, err := workgraph.AddWatchDir(workgraph.SettingsWatchConfig{HomeDir: homeDir, Path: repoDir}); err != nil {
		t.Fatalf("add watch dir: %v", err)
	}
	if _, err := workgraph.CaptureGitCommits(workgraph.GitCaptureConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
	}); err != nil {
		t.Fatalf("capture git commits: %v", err)
	}

	db := openIgnoreSuggestionDB(t, result.DatabasePath)
	defer db.Close()

	var timestampText, createdAtText string
	if err := db.QueryRow(`SELECT timestamp, created_at FROM events WHERE id = ?`, "git.commit:"+commitSHA).Scan(&timestampText, &createdAtText); err != nil {
		t.Fatalf("query event timestamp: %v", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, timestampText); err != nil {
		t.Fatalf("expected event timestamp %q to parse as RFC3339: %v", timestampText, err)
	}
	if _, err := time.Parse(time.RFC3339Nano, createdAtText); err != nil {
		t.Fatalf("expected event created_at %q to parse as RFC3339: %v", createdAtText, err)
	}
}

func TestSessionRequiresIDStartedAtEndedAt(t *testing.T) {
	db := openContractDatabase(t)

	_, err := db.Exec(`INSERT INTO sessions (id, started_at, ended_at) VALUES (?, ?, ?)`,
		"session-required-ok",
		"2026-05-25T10:00:00Z",
		"2026-05-25T10:15:00Z",
	)
	if err != nil {
		t.Fatalf("expected session with id, started_at, and ended_at (without summary) to succeed: %v", err)
	}

	missingCases := []struct {
		name string
		args []any
	}{
		{name: "duplicate id", args: []any{"session-required-ok", "2026-05-25T10:00:00Z", "2026-05-25T10:15:00Z"}},
		{name: "missing started_at", args: []any{"session-no-start", nil, "2026-05-25T10:15:00Z"}},
		{name: "missing ended_at", args: []any{"session-no-end", "2026-05-25T10:00:00Z", nil}},
	}
	for _, tc := range missingCases {
		_, err := db.Exec(`INSERT INTO sessions (id, started_at, ended_at) VALUES (?, ?, ?)`, tc.args...)
		if err == nil {
			t.Fatalf("expected %s to be rejected", tc.name)
		}
	}
}

func TestDomainSessionStartMustBeBeforeOrEqualEnd(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir:   homeDir,
		MemoryDir: filepath.Join(tempDir, "workgraph-memory"),
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	first := time.Date(2026, 5, 25, 9, 0, 0, 0, time.UTC)
	second := time.Date(2026, 5, 25, 9, 10, 0, 0, time.UTC)
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "session-evt-1",
		Type:      "file.modified",
		Timestamp: first,
		Project:   "Cupcake",
		Payload:   `{"path":"/tmp/Cupcake/a.go","operation":"modified"}`,
	})
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "session-evt-2",
		Type:      "file.modified",
		Timestamp: second,
		Project:   "Cupcake",
		Payload:   `{"path":"/tmp/Cupcake/b.go","operation":"modified"}`,
	})

	today, err := workgraph.Today(workgraph.TodayConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Now:          second,
	})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}
	if len(today.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(today.Sessions))
	}
	session := today.Sessions[0]
	if session.StartedAt.After(session.EndedAt) {
		t.Fatalf("expected session started_at (%s) <= ended_at (%s)", session.StartedAt, session.EndedAt)
	}
	if !session.StartedAt.Equal(first) || !session.EndedAt.Equal(second) {
		t.Fatalf("expected session bounds [%s, %s], got [%s, %s]", first, second, session.StartedAt, session.EndedAt)
	}

	db := openIgnoreSuggestionDB(t, result.DatabasePath)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO sessions (id, started_at, ended_at) VALUES (?, ?, ?)`,
		"equal-bounds",
		first.Format(time.RFC3339),
		first.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("expected session with equal started_at and ended_at to succeed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, started_at, ended_at) VALUES (?, ?, ?)`,
		"inverted-bounds",
		second.Format(time.RFC3339),
		first.Format(time.RFC3339),
	); err == nil {
		t.Fatalf("expected session with started_at > ended_at to be rejected")
	}
}

func TestMemoryDocRequiresIDPathKindContentUpdatedAt(t *testing.T) {
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

	memoryPath := filepath.Join(memoryDir, "projects", "cupcake.md")
	if err := os.WriteFile(memoryPath, []byte("# Cupcake\n\nPrioritize auth.\n"), 0o644); err != nil {
		t.Fatalf("write project memory: %v", err)
	}
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "cupcake-memory",
		Type:      "file.modified",
		Timestamp: time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
		Project:   "Cupcake",
		Payload:   `{"path":"/tmp/Cupcake/api.go","operation":"modified"}`,
	})

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		MemoryDir:    memoryDir,
		Project:      "Cupcake",
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if resume.Memory == nil {
		t.Fatalf("expected project memory doc")
	}
	if resume.Memory.ID == "" {
		t.Fatalf("expected memory doc id")
	}
	if resume.Memory.Path != memoryPath {
		t.Fatalf("expected memory doc path %q, got %q", memoryPath, resume.Memory.Path)
	}
	if resume.Memory.Kind != "markdown" {
		t.Fatalf("expected markdown memory kind, got %q", resume.Memory.Kind)
	}
	if resume.Memory.Content != "# Cupcake\n\nPrioritize auth.\n" {
		t.Fatalf("expected memory content, got %q", resume.Memory.Content)
	}
	if resume.Memory.UpdatedAt.IsZero() {
		t.Fatalf("expected memory doc updated timestamp")
	}
}

func TestMemoryDocUpdatedAtUsesRFC3339(t *testing.T) {
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

	memoryPath := filepath.Join(memoryDir, "projects", "cupcake.md")
	if err := os.WriteFile(memoryPath, []byte("# Cupcake\n"), 0o644); err != nil {
		t.Fatalf("write project memory: %v", err)
	}
	expectedModTime := time.Date(2026, 5, 25, 16, 45, 30, 0, time.UTC)
	if err := os.Chtimes(memoryPath, expectedModTime, expectedModTime); err != nil {
		t.Fatalf("chtimes project memory: %v", err)
	}

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		MemoryDir:    memoryDir,
		Project:      "Cupcake",
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if resume.Memory == nil {
		t.Fatalf("expected loaded memory doc")
	}

	formatted := resume.Memory.UpdatedAt.UTC().Format(time.RFC3339)
	db := openIgnoreSuggestionDB(t, result.DatabasePath)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO memory_docs (id, path, kind, content, updated_at) VALUES (?, ?, ?, ?, ?)`,
		resume.Memory.ID,
		resume.Memory.Path,
		resume.Memory.Kind,
		resume.Memory.Content,
		formatted,
	); err != nil {
		t.Fatalf("insert memory_doc: %v", err)
	}

	var storedUpdatedAt string
	if err := db.QueryRow(`SELECT updated_at FROM memory_docs WHERE id = ?`, resume.Memory.ID).Scan(&storedUpdatedAt); err != nil {
		t.Fatalf("read memory_doc updated_at: %v", err)
	}
	parsed, err := time.Parse(time.RFC3339, storedUpdatedAt)
	if err != nil {
		t.Fatalf("expected memory_doc updated_at %q to be valid RFC3339: %v", storedUpdatedAt, err)
	}
	if !parsed.Equal(expectedModTime) {
		t.Fatalf("expected parsed updated_at %s to equal %s", parsed, expectedModTime)
	}
}
