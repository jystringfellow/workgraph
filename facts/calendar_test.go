package facts

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestCalendarCaptureStoresNormalizedEvent(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	eventsPath := filepath.Join(tempDir, "calendar-events.json")
	if err := os.WriteFile(eventsPath, []byte(`[
  {
    "provider": "google",
    "calendar_id": "primary",
    "event_id": "evt-123",
    "title": "Cupcake API planning",
    "start": "2026-05-20T15:00:00Z",
    "end": "2026-05-20T15:30:00Z",
    "location": "Conference Room 2",
    "meeting_url": "https://meet.google.com/cup-cake-api",
    "organizer": "Ada Lovelace",
    "attendees": ["Grace Hopper", "Stringfellow"],
    "status": "confirmed",
    "project": "Cupcake API"
  }
]`), 0o644); err != nil {
		t.Fatalf("write calendar events: %v", err)
	}

	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	output, err := runworkgraph(t, repoRoot, "calendar", "capture", "--home", homeDir, "--events-file", eventsPath)
	if err != nil {
		t.Fatalf("workgraph calendar capture failed: %v\n%s", err, output)
	}

	event := calendarEvent(t, filepath.Join(homeDir, "workgraph.db"), "google", "primary", "evt-123")
	if event.Timestamp != "2026-05-20T15:00:00Z" {
		t.Fatalf("expected event timestamp to use calendar start, got %q", event.Timestamp)
	}
	if event.Project != "Cupcake API" {
		t.Fatalf("expected explicit project %q, got %q", "Cupcake API", event.Project)
	}
	if event.Actor != "Ada Lovelace" {
		t.Fatalf("expected organizer actor, got %q", event.Actor)
	}
	if event.Summary != "Cupcake API planning" {
		t.Fatalf("expected title summary, got %q", event.Summary)
	}
	for _, expected := range []string{
		`"provider":"google"`,
		`"calendar_id":"primary"`,
		`"event_id":"evt-123"`,
		`"title":"Cupcake API planning"`,
		`"start":"2026-05-20T15:00:00Z"`,
		`"end":"2026-05-20T15:30:00Z"`,
		`"location":"Conference Room 2"`,
		`"meeting_url":"https://meet.google.com/cup-cake-api"`,
		`"organizer":"Ada Lovelace"`,
		`"attendees":["Grace Hopper","Stringfellow"]`,
		`"status":"confirmed"`,
	} {
		if !strings.Contains(event.PayloadJSON, expected) {
			t.Fatalf("expected payload to include %s, got %s", expected, event.PayloadJSON)
		}
	}
	if !strings.Contains(string(output), "Calendar capture complete") {
		t.Fatalf("expected capture summary, got:\n%s", output)
	}
}

func TestCalendarCaptureKeepsOneEventPerProviderCalendarID(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	eventsPath := filepath.Join(tempDir, "calendar-events.json")
	if err := os.WriteFile(eventsPath, []byte(`[
  {
    "provider": "microsoft",
    "calendar_id": "work",
    "event_id": "evt-456",
    "title": "Sprint planning",
    "start": "2026-05-20T16:00:00Z",
    "end": "2026-05-20T17:00:00Z",
    "organizer": "Grace Hopper",
    "status": "confirmed"
  }
]`), 0o644); err != nil {
		t.Fatalf("write calendar events: %v", err)
	}

	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	for i := 0; i < 2; i++ {
		if output, err := runworkgraph(t, repoRoot, "calendar", "capture", "--home", homeDir, "--events-file", eventsPath); err != nil {
			t.Fatalf("workgraph calendar capture failed: %v\n%s", err, output)
		}
	}

	if count := calendarEventCount(t, filepath.Join(homeDir, "workgraph.db")); count != 1 {
		t.Fatalf("expected recapture to keep one calendar event, got %d", count)
	}
}

type storedCalendarEvent struct {
	Timestamp   string
	Project     string
	Actor       string
	Summary     string
	PayloadJSON string
}

func calendarEvent(t *testing.T, dbPath, provider, calendarID, eventID string) storedCalendarEvent {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var event storedCalendarEvent
	err = db.QueryRow(`
		SELECT timestamp, COALESCE(project, ''), COALESCE(actor, ''), COALESCE(summary, ''), payload_json
		FROM events
		WHERE source = 'calendar'
			AND type = 'calendar.event'
			AND json_extract(payload_json, '$.provider') = ?
			AND json_extract(payload_json, '$.calendar_id') = ?
			AND json_extract(payload_json, '$.event_id') = ?
	`, provider, calendarID, eventID).Scan(&event.Timestamp, &event.Project, &event.Actor, &event.Summary, &event.PayloadJSON)
	if err != nil {
		t.Fatalf("fetch calendar event: %v", err)
	}
	return event
}

func calendarEventCount(t *testing.T, dbPath string) int {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE source = 'calendar'`).Scan(&count); err != nil {
		t.Fatalf("count calendar events: %v", err)
	}
	return count
}
