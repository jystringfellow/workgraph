package facts

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestEffectivenessReviewComputesStableLocalMetrics(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.FixedZone("PDT", -7*60*60))
	db := openSQLite(t, initResult.DatabasePath)

	insertReviewSuggestion(t, db, "accepted-fast", now.Add(-2*time.Hour), now.Add(-2*time.Hour+2*time.Minute), "accepted", "")
	insertReviewSuggestion(t, db, "accepted-slow", now.Add(-3*time.Hour), now.Add(-3*time.Hour+10*time.Minute), "accepted", "")
	insertReviewSuggestion(t, db, "dismissed", now.Add(-4*time.Hour), now.Add(-time.Hour), "dismissed", "not_relevant")
	insertReviewSuggestion(t, db, "snoozed", now.Add(-5*time.Hour), now.Add(-30*time.Minute), "snoozed", "later")
	insertReviewSuggestion(t, db, "outside", now.Add(-10*24*time.Hour), now.Add(-9*24*time.Hour), "dismissed", "not_relevant")

	connectorState := `{"connectors":{"git":{"enabled":true,"interval":"1h","setup_state":"ready","last_poll_at":"2026-07-15T18:50:00Z","last_success_at":"2026-07-15T18:30:00Z","last_error":"poll timeout","consecutive_failures":1}}}`
	if err := os.WriteFile(filepath.Join(homeDir, "connectors.json"), []byte(connectorState), 0o600); err != nil {
		t.Fatalf("write connector state: %v", err)
	}

	result, err := workgraph.EffectivenessReview(workgraph.EffectivenessReviewConfig{
		HomeDir: homeDir, DatabasePath: initResult.DatabasePath, Since: "7d", Format: "text", Now: now,
	})
	if err != nil {
		t.Fatalf("review failed: %v", err)
	}
	if result.Acceptance.Count != 2 || result.Acceptance.Denominator != 4 || result.Acceptance.RatePercent == nil || *result.Acceptance.RatePercent != 50 {
		t.Fatalf("unexpected acceptance metric: %#v", result.Acceptance)
	}
	if result.Dismissal.Count != 1 || result.Dismissal.RatePercent == nil || *result.Dismissal.RatePercent != 25 {
		t.Fatalf("unexpected dismissal metric: %#v", result.Dismissal)
	}
	if result.Snooze.Count != 1 || result.Snooze.RatePercent == nil || *result.Snooze.RatePercent != 25 {
		t.Fatalf("unexpected snooze metric: %#v", result.Snooze)
	}
	if len(result.DismissalReasons) != 1 || result.DismissalReasons[0].Code != "not_relevant" || result.DismissalReasons[0].Count != 1 {
		t.Fatalf("unexpected dismissal reasons: %#v", result.DismissalReasons)
	}
	if result.TimeToUseful.SampleSize != 2 || result.TimeToUseful.MedianSeconds == nil || *result.TimeToUseful.MedianSeconds != 360 {
		t.Fatalf("unexpected time-to-useful metric: %#v", result.TimeToUseful)
	}
	git := reviewConnectorByID(t, result.Connectors, "git")
	if git.Freshness != "fresh" || !git.Degraded || git.AgeSeconds == nil || *git.AgeSeconds != 1800 {
		t.Fatalf("unexpected git freshness: %#v", git)
	}
	for _, expected := range []string{"50.00%", "25.00%", "not_relevant: 1", "360 seconds", "git", "fresh", "degraded"} {
		if !strings.Contains(result.Message, expected) {
			t.Fatalf("expected text review to contain %q, got:\n%s", expected, result.Message)
		}
	}

	jsonResult, err := workgraph.EffectivenessReview(workgraph.EffectivenessReviewConfig{
		HomeDir: homeDir, DatabasePath: initResult.DatabasePath, Since: "7d", Format: "json", Now: now,
	})
	if err != nil {
		t.Fatalf("json review failed: %v", err)
	}
	var payload struct {
		Acceptance workgraph.ReviewRateMetric       `json:"acceptance_rate"`
		TimeUseful workgraph.ReviewDurationMetric   `json:"time_to_useful_suggestion"`
		Connectors []workgraph.ReviewConnectorState `json:"connectors"`
	}
	if err := json.Unmarshal([]byte(jsonResult.Message), &payload); err != nil {
		t.Fatalf("parse review JSON: %v\n%s", err, jsonResult.Message)
	}
	if payload.Acceptance.Count != result.Acceptance.Count || payload.Acceptance.Denominator != result.Acceptance.Denominator || payload.TimeUseful.MedianSeconds == nil || *payload.TimeUseful.MedianSeconds != *result.TimeToUseful.MedianSeconds {
		t.Fatalf("text and JSON results differ: payload=%#v result=%#v", payload, result)
	}
}

func TestEffectivenessReviewUsesDeterministicCurrentWeekAndHonestEmptyMetrics(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	location := time.FixedZone("PDT", -7*60*60)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, location)

	result, err := workgraph.EffectivenessReview(workgraph.EffectivenessReviewConfig{
		HomeDir: homeDir, DatabasePath: initResult.DatabasePath, Format: "json", Now: now,
	})
	if err != nil {
		t.Fatalf("review failed: %v", err)
	}
	if !result.Window.Start.Equal(time.Date(2026, 7, 13, 0, 0, 0, 0, location)) || !result.Window.End.Equal(now) {
		t.Fatalf("unexpected current-week window: %#v", result.Window)
	}
	if result.Acceptance.Status != "insufficient_data" || result.Acceptance.RatePercent != nil || result.TimeToUseful.Status != "insufficient_data" || result.TimeToUseful.MedianSeconds != nil {
		t.Fatalf("expected honest insufficient data metrics, got acceptance=%#v useful=%#v", result.Acceptance, result.TimeToUseful)
	}
	if !strings.Contains(result.Message, `"status": "insufficient_data"`) || !strings.Contains(result.Message, `"rate_percent": null`) || !strings.Contains(result.Message, `"median_seconds": null`) {
		t.Fatalf("expected explicit null metrics in JSON, got:\n%s", result.Message)
	}
}

func TestEffectivenessReviewUsesInclusiveStartExclusiveEndRollingWindows(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	now := time.Date(2026, 7, 15, 19, 0, 0, 0, time.UTC)
	start := now.Add(-168 * time.Hour)
	db := openSQLite(t, initResult.DatabasePath)
	insertReviewSuggestion(t, db, "at-start", start.Add(-time.Hour), start, "accepted", "")
	insertReviewSuggestion(t, db, "at-end", now.Add(-time.Hour), now, "dismissed", "not_relevant")

	result, err := workgraph.EffectivenessReview(workgraph.EffectivenessReviewConfig{
		HomeDir: homeDir, DatabasePath: initResult.DatabasePath, Since: "7d", Format: "text", Now: now,
	})
	if err != nil {
		t.Fatalf("7d review failed: %v", err)
	}
	if !result.Window.Start.Equal(start) || result.Window.End.Sub(result.Window.Start) != 168*time.Hour || result.Acceptance.Count != 1 || result.Acceptance.Denominator != 1 || result.Dismissal.Count != 0 {
		t.Fatalf("unexpected 7d boundary result: window=%#v acceptance=%#v dismissal=%#v", result.Window, result.Acceptance, result.Dismissal)
	}

	thirtyDays, err := workgraph.EffectivenessReview(workgraph.EffectivenessReviewConfig{
		HomeDir: homeDir, DatabasePath: initResult.DatabasePath, Since: "30d", Format: "text", Now: now,
	})
	if err != nil {
		t.Fatalf("30d review failed: %v", err)
	}
	if thirtyDays.Window.End.Sub(thirtyDays.Window.Start) != 720*time.Hour {
		t.Fatalf("expected exact 720-hour window, got %#v", thirtyDays.Window)
	}
}

func TestReviewCLIAcceptsThirtyDaysAndRejectsUnknownWindows(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	output := runWorkgraphCommand(t, nil, "review", "--home", homeDir, "--database", initResult.DatabasePath, "--since", "30d", "--format", "json")
	if !strings.Contains(output, `"kind": "30d"`) {
		t.Fatalf("expected 30d JSON review, got:\n%s", output)
	}
	output, err = runWorkgraphCommandAllowError(nil, "review", "--home", homeDir, "--database", initResult.DatabasePath, "--since", "90d")
	if err == nil || !strings.Contains(output, `unsupported review window "90d"`) {
		t.Fatalf("expected unknown window error, err=%v output=%s", err, output)
	}
}

func insertReviewSuggestion(t *testing.T, db *sql.DB, id string, created time.Time, feedbackAt time.Time, action string, reason string) {
	t.Helper()
	createdAt := created.UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO suggestions (id, type, pattern_key, title, reason, confidence, lane, status, evidence_json, created_at, updated_at) VALUES (?, 'what_next', ?, ?, 'Review fixture', 'medium', 'baseline', 'proposed', '{}', ?, ?)`, id, id, id, createdAt, createdAt); err != nil {
		t.Fatalf("insert review suggestion: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO suggestion_feedback (id, suggestion_id, action, reason_code, created_at) VALUES (?, ?, ?, NULLIF(?, ''), ?)`, "feedback-"+id, id, action, reason, feedbackAt.UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert review feedback: %v", err)
	}
}

func reviewConnectorByID(t *testing.T, connectors []workgraph.ReviewConnectorState, id string) workgraph.ReviewConnectorState {
	t.Helper()
	for _, connector := range connectors {
		if connector.ID == id {
			return connector
		}
	}
	t.Fatalf("connector %q not found in %#v", id, connectors)
	return workgraph.ReviewConnectorState{}
}
