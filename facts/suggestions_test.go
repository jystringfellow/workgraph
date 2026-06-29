package facts

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestSuggestionStorageCoalescesByTypeAndPatternKey(t *testing.T) {
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: filepath.Join(t.TempDir(), ".workgraph")})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	first, err := workgraph.UpsertSuggestion(workgraph.SuggestionUpsert{
		DatabasePath: result.DatabasePath,
		Type:         "ignore_path",
		PatternKey:   "/repo/build",
		Title:        "Ignore noisy build output",
		Reason:       "30 file events appeared under /repo/build.",
		Confidence:   "high",
		Lane:         "baseline",
		EvidenceJSON: `{"event_ids":["event-1"],"paths":["/repo/build/a.o"]}`,
	})
	if err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	second, err := workgraph.UpsertSuggestion(workgraph.SuggestionUpsert{
		DatabasePath: result.DatabasePath,
		Type:         "ignore_path",
		PatternKey:   "/repo/build",
		Title:        "Ignore noisy build output",
		Reason:       "42 file events appeared under /repo/build.",
		Confidence:   "high",
		Lane:         "baseline",
		EvidenceJSON: `{"event_ids":["event-1","event-2"],"paths":["/repo/build/a.o","/repo/build/b.o"]}`,
	})
	if err != nil {
		t.Fatalf("coalesce suggestion: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected coalesced suggestion id %q, got %q", first.ID, second.ID)
	}

	db := openSQLite(t, result.DatabasePath)
	var count int
	var reason string
	var evidence string
	if err := db.QueryRow(`SELECT COUNT(*), reason, evidence_json FROM suggestions WHERE type = ? AND pattern_key = ?`, "ignore_path", "/repo/build").Scan(&count, &reason, &evidence); err != nil {
		t.Fatalf("read suggestion: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one coalesced suggestion, got %d", count)
	}
	if reason != "42 file events appeared under /repo/build." || !strings.Contains(evidence, "event-2") {
		t.Fatalf("expected latest reason and evidence, got reason %q evidence %q", reason, evidence)
	}
}

func TestSuggestionLifecycleFeedbackAndSuppressionAreStored(t *testing.T) {
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: filepath.Join(t.TempDir(), ".workgraph")})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	suggestion, err := workgraph.UpsertSuggestion(workgraph.SuggestionUpsert{
		DatabasePath: result.DatabasePath,
		Type:         "watch_root",
		PatternKey:   "/repo",
		Title:        "Watch repo",
		Reason:       "Git activity was captured outside configured watch roots.",
		Confidence:   "medium",
		Lane:         "baseline",
		EvidenceJSON: `{"paths":["/repo/.git/HEAD"]}`,
	})
	if err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	if err := workgraph.UpdateSuggestionStatus(workgraph.SuggestionStatusUpdate{
		DatabasePath: result.DatabasePath,
		ID:           suggestion.ID,
		Status:       "dismissed",
		ReasonCode:   "not_my_project",
		FeedbackNote: "This repo is temporary.",
	}); err != nil {
		t.Fatalf("dismiss suggestion: %v", err)
	}
	if _, err := workgraph.AddSuggestionSuppression(workgraph.SuggestionSuppressionChange{
		DatabasePath: result.DatabasePath,
		Type:         "watch_root",
		PatternKey:   "/repo",
		Reason:       "not_my_project",
	}); err != nil {
		t.Fatalf("add suppression: %v", err)
	}

	db := openSQLite(t, result.DatabasePath)
	var status string
	var feedbackAction string
	var reasonCode string
	var suppressionCount int
	if err := db.QueryRow(`SELECT status FROM suggestions WHERE id = ?`, suggestion.ID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if err := db.QueryRow(`SELECT action, reason_code FROM suggestion_feedback WHERE suggestion_id = ?`, suggestion.ID).Scan(&feedbackAction, &reasonCode); err != nil {
		t.Fatalf("read feedback: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM suggestion_suppressions WHERE type = ? AND pattern_key = ?`, "watch_root", "/repo").Scan(&suppressionCount); err != nil {
		t.Fatalf("read suppression: %v", err)
	}
	if status != "dismissed" || feedbackAction != "dismissed" || reasonCode != "not_my_project" || suppressionCount != 1 {
		t.Fatalf("expected dismissed suggestion with feedback and suppression, got status=%q action=%q reason=%q suppressions=%d", status, feedbackAction, reasonCode, suppressionCount)
	}

	if err := workgraph.RemoveSuggestionSuppression(workgraph.SuggestionSuppressionChange{
		DatabasePath: result.DatabasePath,
		Type:         "watch_root",
		PatternKey:   "/repo",
	}); err != nil {
		t.Fatalf("remove suppression: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM suggestion_suppressions WHERE type = ? AND pattern_key = ?`, "watch_root", "/repo").Scan(&suppressionCount); err != nil {
		t.Fatalf("read removed suppression: %v", err)
	}
	if suppressionCount != 0 {
		t.Fatalf("expected suppression to be reversible, got %d rows", suppressionCount)
	}
}

func TestSuggestionsCLIListsAndDismissesSuggestions(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	suggestion, err := workgraph.UpsertSuggestion(workgraph.SuggestionUpsert{
		DatabasePath: result.DatabasePath,
		Type:         "ignore_name",
		PatternKey:   "node_modules",
		Title:        "Ignore node_modules",
		Reason:       "Repeated generated dependency events were captured.",
		Confidence:   "high",
		Lane:         "baseline",
		EvidenceJSON: `{"paths":["/repo/node_modules/pkg/index.js"]}`,
	})
	if err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	output := runWorkgraphCommand(t, nil, "suggestions", "list", "--home", homeDir, "--database", result.DatabasePath)
	for _, expected := range []string{"Suggestions", suggestion.ID, "ignore_name", "proposed", "Ignore node_modules"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected suggestions list output to contain %q, got:\n%s", expected, output)
		}
	}

	output = runWorkgraphCommand(t, nil, "suggestions", "dismiss", suggestion.ID, "--home", homeDir, "--database", result.DatabasePath, "--reason", "noisy")
	if !strings.Contains(output, "Suggestion dismissed") || !strings.Contains(output, suggestion.ID) {
		t.Fatalf("expected dismiss output, got:\n%s", output)
	}

	output = runWorkgraphCommand(t, nil, "suggestions", "list", "--home", homeDir, "--database", result.DatabasePath)
	if !strings.Contains(output, "dismissed") {
		t.Fatalf("expected dismissed suggestion in list output, got:\n%s", output)
	}
}

func TestSnoozedSuggestionResurfacesAfterExpiryWindow(t *testing.T) {
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: filepath.Join(t.TempDir(), ".workgraph")})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	suggestion, err := workgraph.UpsertSuggestion(workgraph.SuggestionUpsert{
		DatabasePath: result.DatabasePath,
		Type:         "ignore_path",
		PatternKey:   "/repo/dist",
		Title:        "Ignore dist output",
		Reason:       "Many build events under /repo/dist.",
		Confidence:   "high",
		Lane:         "baseline",
		EvidenceJSON: `{"event_ids":["e1"],"paths":["/repo/dist/main.js"]}`,
	})
	if err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	// Snooze the suggestion.
	if err := workgraph.UpdateSuggestionStatus(workgraph.SuggestionStatusUpdate{
		DatabasePath: result.DatabasePath,
		ID:           suggestion.ID,
		Status:       "snoozed",
	}); err != nil {
		t.Fatalf("snooze suggestion: %v", err)
	}

	// Add a suppression with an until_at already in the past.
	pastTime := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	if _, err := workgraph.AddSuggestionSuppression(workgraph.SuggestionSuppressionChange{
		DatabasePath: result.DatabasePath,
		Type:         "ignore_path",
		PatternKey:   "/repo/dist",
		Reason:       "snoozed",
		UntilAt:      pastTime,
	}); err != nil {
		t.Fatalf("add suppression: %v", err)
	}

	// Listing should expire the snooze and return the suggestion as proposed.
	listed, err := workgraph.ListSuggestions(workgraph.SuggestionListConfig{
		DatabasePath: result.DatabasePath,
	})
	if err != nil {
		t.Fatalf("list suggestions: %v", err)
	}

	var found *workgraph.Suggestion
	for i := range listed.Suggestions {
		if listed.Suggestions[i].ID == suggestion.ID {
			found = &listed.Suggestions[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected suggestion to reappear after snooze expiry, not found in list")
	}
	if found.Status != "proposed" {
		t.Fatalf("expected suggestion status to be proposed after snooze expiry, got %q", found.Status)
	}
}

func TestSnoozedSuggestionStaysHiddenBeforeExpiryWindow(t *testing.T) {
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: filepath.Join(t.TempDir(), ".workgraph")})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	suggestion, err := workgraph.UpsertSuggestion(workgraph.SuggestionUpsert{
		DatabasePath: result.DatabasePath,
		Type:         "ignore_path",
		PatternKey:   "/repo/.cache",
		Title:        "Ignore cache",
		Reason:       "Many cache events.",
		Confidence:   "medium",
		Lane:         "baseline",
		EvidenceJSON: `{"event_ids":["e2"],"paths":["/repo/.cache/v1"]}`,
	})
	if err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	if err := workgraph.UpdateSuggestionStatus(workgraph.SuggestionStatusUpdate{
		DatabasePath: result.DatabasePath,
		ID:           suggestion.ID,
		Status:       "snoozed",
	}); err != nil {
		t.Fatalf("snooze suggestion: %v", err)
	}

	// Add a suppression with an until_at in the future.
	futureTime := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	if _, err := workgraph.AddSuggestionSuppression(workgraph.SuggestionSuppressionChange{
		DatabasePath: result.DatabasePath,
		Type:         "ignore_path",
		PatternKey:   "/repo/.cache",
		Reason:       "snoozed",
		UntilAt:      futureTime,
	}); err != nil {
		t.Fatalf("add suppression: %v", err)
	}

	listed, err := workgraph.ListSuggestions(workgraph.SuggestionListConfig{
		DatabasePath: result.DatabasePath,
	})
	if err != nil {
		t.Fatalf("list suggestions: %v", err)
	}

	for _, s := range listed.Suggestions {
		if s.ID == suggestion.ID && s.Status == "proposed" {
			t.Fatalf("expected snoozed suggestion to remain snoozed before expiry, got proposed")
		}
	}
}

func TestSuggestionsListShowsEvidenceSummary(t *testing.T) {
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: filepath.Join(t.TempDir(), ".workgraph")})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	suggestion, err := workgraph.UpsertSuggestion(workgraph.SuggestionUpsert{
		DatabasePath: result.DatabasePath,
		Type:         "ignore_path",
		PatternKey:   "/repo/build",
		Title:        "Ignore noisy build output",
		Reason:       "Many build events.",
		Confidence:   "high",
		Lane:         "baseline",
		EvidenceJSON: `{"event_ids":["e1","e2","e3"],"paths":["/repo/build/a.o","/repo/build/b.o"]}`,
	})
	if err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	listed, err := workgraph.ListSuggestions(workgraph.SuggestionListConfig{DatabasePath: result.DatabasePath})
	if err != nil {
		t.Fatalf("list suggestions: %v", err)
	}

	if !strings.Contains(listed.Message, "3 events") {
		t.Fatalf("expected list output to include event count in evidence, got:\n%s", listed.Message)
	}
	if !strings.Contains(listed.Message, "2 paths") {
		t.Fatalf("expected list output to include path count in evidence, got:\n%s", listed.Message)
	}
	_ = suggestion
}

func TestSuggestionsShowIncludesFullEvidence(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	suggestion, err := workgraph.UpsertSuggestion(workgraph.SuggestionUpsert{
		DatabasePath: result.DatabasePath,
		Type:         "ignore_path",
		PatternKey:   "/repo/dist",
		Title:        "Ignore dist output",
		Reason:       "Many dist events.",
		Confidence:   "high",
		Lane:         "baseline",
		EvidenceJSON: `{"event_ids":["evt-abc","evt-def"],"paths":["/repo/dist/main.js","/repo/dist/vendor.js"]}`,
	})
	if err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	shown, err := workgraph.ShowSuggestion(workgraph.SuggestionShowConfig{
		DatabasePath: result.DatabasePath,
		ID:           suggestion.ID,
	})
	if err != nil {
		t.Fatalf("show suggestion: %v", err)
	}

	for _, expected := range []string{
		suggestion.ID, "ignore_path", "/repo/dist", "proposed", "high",
		"evt-abc", "evt-def",
		"/repo/dist/main.js", "/repo/dist/vendor.js",
	} {
		if !strings.Contains(shown.Message, expected) {
			t.Fatalf("expected show output to contain %q, got:\n%s", expected, shown.Message)
		}
	}
}

func TestSuggestionsShowCLIRendersEvidence(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	suggestion, err := workgraph.UpsertSuggestion(workgraph.SuggestionUpsert{
		DatabasePath: result.DatabasePath,
		Type:         "watch_root",
		PatternKey:   "/repo",
		Title:        "Watch new repo",
		Reason:       "Git activity outside watch roots.",
		Confidence:   "medium",
		Lane:         "baseline",
		EvidenceJSON: `{"event_ids":["git-1"],"paths":["/repo/.git/HEAD"]}`,
	})
	if err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	output := runWorkgraphCommand(t, nil, "suggestions", "show", suggestion.ID, "--home", homeDir, "--database", result.DatabasePath)
	for _, expected := range []string{suggestion.ID, "watch_root", "/repo", "git-1", "/repo/.git/HEAD"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected CLI show output to contain %q, got:\n%s", expected, output)
		}
	}
}

func TestSuggestionsShowReturnsErrorForUnknownID(t *testing.T) {
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: filepath.Join(t.TempDir(), ".workgraph")})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	_, err = workgraph.ShowSuggestion(workgraph.SuggestionShowConfig{
		DatabasePath: result.DatabasePath,
		ID:           "sug_doesnotexist",
	})
	if err == nil {
		t.Fatalf("expected error for unknown suggestion id, got nil")
	}
}

func openSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close database: %v", err)
		}
	})
	return db
}
