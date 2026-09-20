package facts

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
	_ "github.com/mattn/go-sqlite3"
)

func TestTodayUsesExplicitInvolvementWithoutDroppingLegacyEvidence(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	db, err := sql.Open("sqlite3", result.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, event := range []struct {
		id, summary string
		involvement any
	}{
		{id: "direct", summary: "Review requested", involvement: `["review_requested"]`},
		{id: "ambient", summary: "Ambient channel chatter", involvement: `[]`},
		{id: "legacy", summary: "Legacy evidence", involvement: nil},
	} {
		if _, err := db.Exec(`INSERT INTO events
			(id, source, type, timestamp, payload_json, summary, involvement_json, created_at)
			VALUES (?, 'slack', 'slack.message', ?, '{}', ?, ?, ?)`,
			event.id, now, event.summary, event.involvement, now); err != nil {
			t.Fatalf("insert %s: %v", event.id, err)
		}
	}

	todayOutput, err := runworkgraph(t, repoRoot(t), "today", "--home", homeDir)
	if err != nil {
		t.Fatalf("today failed: %v\n%s", err, todayOutput)
	}
	if !strings.Contains(string(todayOutput), "Review requested") || !strings.Contains(string(todayOutput), "Legacy evidence") {
		t.Fatalf("expected direct and legacy evidence in today:\n%s", todayOutput)
	}
	if strings.Contains(string(todayOutput), "Ambient channel chatter") {
		t.Fatalf("expected explicitly ambient evidence hidden from today:\n%s", todayOutput)
	}

	filtered, err := runworkgraph(t, repoRoot(t), "today", "--home", homeDir, "--involvement", "review_requested")
	if err != nil {
		t.Fatalf("today involvement filter failed: %v\n%s", err, filtered)
	}
	if !strings.Contains(string(filtered), "Review requested") || strings.Contains(string(filtered), "Legacy evidence") {
		t.Fatalf("expected exact involvement filter:\n%s", filtered)
	}

	details, err := runworkgraph(t, repoRoot(t), "events", "today", "--home", homeDir)
	if err != nil {
		t.Fatalf("events today failed: %v\n%s", err, details)
	}
	for _, summary := range []string{"Review requested", "Ambient channel chatter", "Legacy evidence"} {
		if !strings.Contains(string(details), summary) {
			t.Fatalf("expected %q in complete evidence view:\n%s", summary, details)
		}
	}
	for _, classification := range []string{"involvement: review_requested", "involvement: none", "involvement: unclassified"} {
		if !strings.Contains(string(details), classification) {
			t.Fatalf("expected %q in complete evidence view:\n%s", classification, details)
		}
	}
}
