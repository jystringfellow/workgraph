package facts

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestSlackCaptureStoresMessageEvent(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	eventsPath := filepath.Join(tempDir, "slack-events.json")
	if err := os.WriteFile(eventsPath, []byte(`[
  {
    "kind": "message",
    "channel_id": "C123",
    "channel_name": "cupcake-api",
    "user": "U456",
    "text": "Ship the auth flow before beta.",
    "ts": "1716215400.000100",
    "permalink": "https://example.slack.com/archives/C123/p1716215400000100",
    "timestamp": "2026-05-20T14:30:00Z"
  }
]`), 0o644); err != nil {
		t.Fatalf("write slack events: %v", err)
	}

	repoRoot := repoRoot(t)
	if output, err := runWorkGraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	output, err := runWorkGraph(t, repoRoot, "slack", "capture", "--home", homeDir, "--events-file", eventsPath)
	if err != nil {
		t.Fatalf("workgraph slack capture failed: %v\n%s", err, output)
	}

	event := slackEvent(t, filepath.Join(homeDir, "workgraph.db"), "slack.message", "C123", "1716215400.000100")
	if event.Project != "cupcake-api" {
		t.Fatalf("expected channel fallback project %q, got %q", "cupcake-api", event.Project)
	}
	if event.Actor != "U456" {
		t.Fatalf("expected actor U456, got %q", event.Actor)
	}
	if event.Summary != "Ship the auth flow before beta." {
		t.Fatalf("expected message summary, got %q", event.Summary)
	}
	for _, expected := range []string{
		`"channel_id":"C123"`,
		`"channel_name":"cupcake-api"`,
		`"user":"U456"`,
		`"text":"Ship the auth flow before beta."`,
		`"ts":"1716215400.000100"`,
		`"permalink":"https://example.slack.com/archives/C123/p1716215400000100"`,
	} {
		if !strings.Contains(event.PayloadJSON, expected) {
			t.Fatalf("expected payload to include %s, got %s", expected, event.PayloadJSON)
		}
	}
	if !strings.Contains(string(output), "Slack capture complete") {
		t.Fatalf("expected capture summary, got:\n%s", output)
	}
}

func TestSlackCaptureStoresThreadReplyWithoutDuplicateEvent(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	eventsPath := filepath.Join(tempDir, "slack-events.json")
	if err := os.WriteFile(eventsPath, []byte(`[
  {
    "kind": "thread_reply",
    "channel_id": "C123",
    "channel_name": "cupcake-api",
    "project": "Cupcake API",
    "user": "U789",
    "text": "I confirmed the migration window.",
    "ts": "1716215500.000200",
    "thread_ts": "1716215400.000100",
    "permalink": "https://example.slack.com/archives/C123/p1716215500000200",
    "timestamp": "2026-05-20T14:31:40Z"
  }
]`), 0o644); err != nil {
		t.Fatalf("write slack events: %v", err)
	}

	repoRoot := repoRoot(t)
	if output, err := runWorkGraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	for i := 0; i < 2; i++ {
		if output, err := runWorkGraph(t, repoRoot, "slack", "capture", "--home", homeDir, "--events-file", eventsPath); err != nil {
			t.Fatalf("workgraph slack capture failed: %v\n%s", err, output)
		}
	}

	event := slackEvent(t, filepath.Join(homeDir, "workgraph.db"), "slack.thread_reply", "C123", "1716215500.000200")
	if event.Project != "Cupcake API" {
		t.Fatalf("expected explicit project %q, got %q", "Cupcake API", event.Project)
	}
	if event.Actor != "U789" {
		t.Fatalf("expected actor U789, got %q", event.Actor)
	}
	if !strings.Contains(event.PayloadJSON, `"thread_ts":"1716215400.000100"`) {
		t.Fatalf("expected thread timestamp in payload, got %s", event.PayloadJSON)
	}
	if count := slackEventCount(t, filepath.Join(homeDir, "workgraph.db")); count != 1 {
		t.Fatalf("expected recapture to keep one Slack event, got %d", count)
	}
}

type storedSlackEvent struct {
	Project     string
	Actor       string
	Summary     string
	PayloadJSON string
}

func slackEvent(t *testing.T, dbPath, eventType, channelID, ts string) storedSlackEvent {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var event storedSlackEvent
	err = db.QueryRow(`
		SELECT project, actor, summary, payload_json
		FROM events
		WHERE source = 'slack'
			AND type = ?
			AND json_extract(payload_json, '$.channel_id') = ?
			AND json_extract(payload_json, '$.ts') = ?
	`, eventType, channelID, ts).Scan(&event.Project, &event.Actor, &event.Summary, &event.PayloadJSON)
	if err != nil {
		t.Fatalf("query slack event: %v", err)
	}
	return event
}

func slackEventCount(t *testing.T, dbPath string) int {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE source = 'slack'`).Scan(&count); err != nil {
		t.Fatalf("query slack event count: %v", err)
	}
	return count
}
