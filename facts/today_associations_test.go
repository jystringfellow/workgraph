package facts

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
	_ "github.com/mattn/go-sqlite3"
)

// TestTodayShowsHighConfidenceAssociation covers requirement: one
// high-confidence cross-source association rendered in `today`.
func TestTodayShowsHighConfidenceAssociation(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	now := time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)
	url := "https://github.com/acme/widget/pull/9"
	insertAssociationEvent(t, dbPath, associationStoredEvent{
		ID: "github:high", Source: "github", Type: "github.pull_request",
		Timestamp: now.Add(-2 * time.Hour), Payload: fmt.Sprintf(`{"url":%q}`, url),
	})
	insertAssociationEvent(t, dbPath, associationStoredEvent{
		ID: "slack:high", Source: "slack", Type: "slack.message",
		Timestamp: now.Add(-90 * time.Minute), Payload: fmt.Sprintf(`{"text":%q}`, url),
	})

	result, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}
	for _, expected := range []string{"Associations", "github:high, slack:high", "score: 100 (high)", "state: proposed", "identical normalized URL"} {
		if !strings.Contains(result.Message, expected) {
			t.Fatalf("expected today output to include %q, got:\n%s", expected, result.Message)
		}
	}
}

// TestTodayExcludesMediumConfidenceAssociation covers requirement: a
// medium-confidence association is excluded from today's compact section.
func TestTodayExcludesMediumConfidenceAssociation(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	now := time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)
	insertAssociationEvent(t, dbPath, associationStoredEvent{
		ID: "mail:medium", Source: "mail", Type: "mail.message",
		Timestamp: now.Add(-3 * time.Hour), Project: "widget", Summary: "Investigate login redirect timeout",
	})
	insertAssociationEvent(t, dbPath, associationStoredEvent{
		ID: "slack:medium", Source: "slack", Type: "slack.message",
		Timestamp: now.Add(-170 * time.Minute), Project: "Widget", Summary: "Login redirect timeout investigation",
	})

	result, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}
	if strings.Contains(result.Message, "Associations") {
		t.Fatalf("expected medium-confidence association to be excluded, got:\n%s", result.Message)
	}
}

// TestTodayCoalescesCanonicalPairWhenBothEventsAreToday covers requirement:
// the same association renders exactly once even though both cited events
// occurred today and each could independently surface it as a target.
func TestTodayCoalescesCanonicalPairWhenBothEventsAreToday(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	now := time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)
	url := "https://github.com/acme/widget/issues/42"
	insertAssociationEvent(t, dbPath, associationStoredEvent{
		ID: "github:coalesce", Source: "github", Type: "github.issue",
		Timestamp: now.Add(-3 * time.Hour), Payload: fmt.Sprintf(`{"url":%q}`, url),
	})
	insertAssociationEvent(t, dbPath, associationStoredEvent{
		ID: "mail:coalesce", Source: "mail", Type: "mail.message",
		Timestamp: now.Add(-1 * time.Hour), Payload: fmt.Sprintf(`{"body_text":%q}`, url),
	})

	result, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}
	if count := strings.Count(result.Message, "github:coalesce, mail:coalesce"); count != 1 {
		t.Fatalf("expected canonical pair to render exactly once, got %d occurrences in:\n%s", count, result.Message)
	}
	if len(result.Associations) != 1 {
		t.Fatalf("expected exactly one coalesced association, got %#v", result.Associations)
	}
}

// TestTodayAssociationOrderingIsDeterministic covers requirement:
// deterministic score descending, then most recent cited timestamp
// descending, tie-break ordering.
func TestTodayAssociationOrderingIsDeterministic(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	now := time.Date(2026, 7, 20, 18, 0, 0, 0, time.UTC)

	// Pair A: exact URL match, score 100, most recent cited timestamp is latest.
	urlA := "https://github.com/acme/alpha/pull/1"
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:a", Source: "github", Type: "github.pull_request", Timestamp: now.Add(-6 * time.Hour), Payload: fmt.Sprintf(`{"url":%q}`, urlA)})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:a", Source: "slack", Type: "slack.message", Timestamp: now.Add(-30 * time.Minute), Payload: fmt.Sprintf(`{"text":%q}`, urlA)})

	// Pair B: exact URL match, score 100, most recent cited timestamp earlier than pair A.
	urlB := "https://github.com/acme/beta/pull/2"
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:b", Source: "github", Type: "github.pull_request", Timestamp: now.Add(-6 * time.Hour), Payload: fmt.Sprintf(`{"url":%q}`, urlB)})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:b", Source: "slack", Type: "slack.message", Timestamp: now.Add(-5 * time.Hour), Payload: fmt.Sprintf(`{"text":%q}`, urlB)})

	// Pair C: repository + issue number match, score 90 (medium time gap keeps it below 100).
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:c", Source: "github", Type: "github.issue", Timestamp: now.Add(-6 * time.Hour), Payload: `{"repository":"acme/gamma","number":7}`})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "mail:c", Source: "mail", Type: "mail.message", Timestamp: now.Add(-3 * time.Hour), Payload: `{"repository":"acme/gamma","number":7}`})

	result, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}
	if len(result.Associations) != 3 {
		t.Fatalf("expected three associations, got %#v", result.Associations)
	}
	if result.Associations[0].Score != 100 || result.Associations[1].Score != 100 || result.Associations[2].Score != 90 {
		t.Fatalf("expected scores [100 100 90], got %#v", result.Associations)
	}
	indexA := strings.Index(result.Message, "github:a, slack:a")
	indexB := strings.Index(result.Message, "github:b, slack:b")
	indexC := strings.Index(result.Message, "github:c, mail:c")
	if indexA == -1 || indexB == -1 || indexC == -1 {
		t.Fatalf("expected all three pairs rendered, got:\n%s", result.Message)
	}
	if !(indexA < indexB && indexB < indexC) {
		t.Fatalf("expected order A (latest, score 100), B (earlier, score 100), C (score 90); got:\n%s", result.Message)
	}
}

// TestTodayExcludesDismissedAssociation covers requirement: a dismissed
// association is excluded even though it still meets the score threshold.
func TestTodayExcludesDismissedAssociation(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	now := time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)
	url := "https://github.com/acme/widget/pull/77"
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:dismiss", Source: "github", Type: "github.pull_request", Timestamp: now.Add(-2 * time.Hour), Payload: fmt.Sprintf(`{"url":%q}`, url)})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:dismiss", Source: "slack", Type: "slack.message", Timestamp: now.Add(-1 * time.Hour), Payload: fmt.Sprintf(`{"text":%q}`, url)})

	explain, err := workgraph.ExplainEventAssociations(workgraph.AssociationExplainConfig{HomeDir: homeDir, DatabasePath: dbPath, EventID: "github:dismiss"})
	if err != nil || len(explain.Candidates) != 1 {
		t.Fatalf("produce association: result=%#v err=%v", explain, err)
	}
	if _, err := workgraph.DismissSuggestion(workgraph.SuggestionStatusUpdate{HomeDir: homeDir, DatabasePath: dbPath, ID: explain.Candidates[0].SuggestionID, ReasonCode: "not_related"}); err != nil {
		t.Fatalf("dismiss association: %v", err)
	}

	result, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}
	if strings.Contains(result.Message, "Associations") {
		t.Fatalf("expected dismissed association to be hidden, got:\n%s", result.Message)
	}
}

// TestTodayExcludesSnoozedOrSuppressedAssociation covers requirement:
// snoozed and explicitly suppressed associations are excluded.
func TestTodayExcludesSnoozedOrSuppressedAssociation(t *testing.T) {
	t.Run("snoozed", func(t *testing.T) {
		homeDir, dbPath := initAssociationStore(t)
		now := time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)
		url := "https://github.com/acme/widget/pull/88"
		insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:snooze", Source: "github", Type: "github.pull_request", Timestamp: now.Add(-2 * time.Hour), Payload: fmt.Sprintf(`{"url":%q}`, url)})
		insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:snooze", Source: "slack", Type: "slack.message", Timestamp: now.Add(-1 * time.Hour), Payload: fmt.Sprintf(`{"text":%q}`, url)})

		explain, err := workgraph.ExplainEventAssociations(workgraph.AssociationExplainConfig{HomeDir: homeDir, DatabasePath: dbPath, EventID: "github:snooze"})
		if err != nil || len(explain.Candidates) != 1 {
			t.Fatalf("produce association: result=%#v err=%v", explain, err)
		}
		until := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
		if _, err := workgraph.SnoozeSuggestion(workgraph.SuggestionSnoozeUpdate{HomeDir: homeDir, DatabasePath: dbPath, ID: explain.Candidates[0].SuggestionID, UntilAt: until, ReasonCode: "later"}); err != nil {
			t.Fatalf("snooze association: %v", err)
		}

		result, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now})
		if err != nil {
			t.Fatalf("today failed: %v", err)
		}
		if strings.Contains(result.Message, "Associations") {
			t.Fatalf("expected snoozed association to be hidden, got:\n%s", result.Message)
		}
	})

	t.Run("suppressed", func(t *testing.T) {
		homeDir, dbPath := initAssociationStore(t)
		now := time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)
		url := "https://github.com/acme/widget/pull/89"
		insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:suppress", Source: "github", Type: "github.pull_request", Timestamp: now.Add(-2 * time.Hour), Payload: fmt.Sprintf(`{"url":%q}`, url)})
		insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:suppress", Source: "slack", Type: "slack.message", Timestamp: now.Add(-1 * time.Hour), Payload: fmt.Sprintf(`{"text":%q}`, url)})

		ids := []string{"github:suppress", "slack:suppress"}
		patternKey := "pair:" + mustJSONArray(ids)
		if _, err := workgraph.AddSuggestionSuppression(workgraph.SuggestionSuppressionChange{HomeDir: homeDir, DatabasePath: dbPath, Type: "association", PatternKey: patternKey, Reason: "not_useful"}); err != nil {
			t.Fatalf("suppress association pattern: %v", err)
		}

		result, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now})
		if err != nil {
			t.Fatalf("today failed: %v", err)
		}
		if strings.Contains(result.Message, "Associations") {
			t.Fatalf("expected suppressed association to be hidden, got:\n%s", result.Message)
		}
	})
}

// TestTodayShowsApprovedAndActedAssociations covers requirement: approved
// and acted associations remain visible with their lifecycle state shown.
func TestTodayShowsApprovedAndActedAssociations(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	now := time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)
	url := "https://github.com/acme/widget/issues/55"
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:approve", Source: "github", Type: "github.issue", Timestamp: now.Add(-2 * time.Hour), Payload: fmt.Sprintf(`{"url":%q}`, url)})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "mail:approve", Source: "mail", Type: "mail.message", Timestamp: now.Add(-1 * time.Hour), Payload: fmt.Sprintf(`{"body_text":%q}`, url)})

	explain, err := workgraph.ExplainEventAssociations(workgraph.AssociationExplainConfig{HomeDir: homeDir, DatabasePath: dbPath, EventID: "github:approve"})
	if err != nil || len(explain.Candidates) != 1 {
		t.Fatalf("produce association: result=%#v err=%v", explain, err)
	}
	suggestionID := explain.Candidates[0].SuggestionID
	if _, err := workgraph.ApproveSuggestion(workgraph.SuggestionStatusUpdate{HomeDir: homeDir, DatabasePath: dbPath, ID: suggestionID}); err != nil {
		t.Fatalf("approve association: %v", err)
	}

	approvedResult, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}
	if !strings.Contains(approvedResult.Message, "state: approved") {
		t.Fatalf("expected approved association to remain visible, got:\n%s", approvedResult.Message)
	}

	if _, err := workgraph.CompleteSuggestion(workgraph.SuggestionStatusUpdate{HomeDir: homeDir, DatabasePath: dbPath, ID: suggestionID, ReasonCode: "done"}); err != nil {
		t.Fatalf("complete association: %v", err)
	}
	actedResult, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}
	if !strings.Contains(actedResult.Message, "state: acted") {
		t.Fatalf("expected acted association to remain visible, got:\n%s", actedResult.Message)
	}
}

// TestTodayAssociationIncludesRelatedEventOutsideToday covers requirements:
// a related event outside today but inside the seven-day window is
// permitted, while it is not itself listed among today's raw events.
func TestTodayAssociationIncludesRelatedEventOutsideToday(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	now := time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)
	url := "https://github.com/acme/widget/pull/33"
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:today-only", Source: "github", Type: "github.pull_request", Timestamp: now.Add(-1 * time.Hour), Payload: fmt.Sprintf(`{"url":%q}`, url)})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:three-days-ago", Source: "slack", Type: "slack.message", Timestamp: now.AddDate(0, 0, -3), Payload: fmt.Sprintf(`{"text":%q}`, url)})

	result, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}
	if !strings.Contains(result.Message, "github:today-only, slack:three-days-ago") {
		t.Fatalf("expected association citing an event outside today but inside the window, got:\n%s", result.Message)
	}
	if strings.Contains(result.Message, "slack:three-days-ago") && strings.Contains(result.Message, "Sessions") {
		sessionsIndex := strings.Index(result.Message, "Sessions")
		associationsIndex := strings.Index(result.Message, "Associations")
		if associationsIndex == -1 {
			t.Fatalf("expected Associations section, got:\n%s", result.Message)
		}
		sessionsSection := result.Message[sessionsIndex:associationsIndex]
		if strings.Contains(sessionsSection, "slack:three-days-ago") {
			t.Fatalf("expected out-of-day event to stay out of raw Sessions section, got:\n%s", result.Message)
		}
	}
}

// TestTodayNoAssociationHeadingForInsufficientEvidence covers requirement:
// no Associations heading appears when no pair meets the evidence threshold.
func TestTodayNoAssociationHeadingForInsufficientEvidence(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	now := time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "file:unrelated", Source: "file", Type: "file.modified", Timestamp: now.Add(-2 * time.Hour), Project: "alpha", Payload: `{"path":"README.md"}`})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:unrelated", Source: "slack", Type: "slack.message", Timestamp: now.Add(-1 * time.Hour), Project: "beta", Payload: `{"text":"completely unrelated chatter about lunch plans"}`})

	result, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}
	if strings.Contains(result.Message, "Associations") {
		t.Fatalf("expected no Associations heading for insufficient evidence, got:\n%s", result.Message)
	}
}

// TestTodayNoAssociationHeadingOnEmptyDay covers requirement: no Associations
// heading appears when there is no activity captured today.
func TestTodayNoAssociationHeadingOnEmptyDay(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	now := time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)

	result, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}
	if strings.Contains(result.Message, "Associations") {
		t.Fatalf("expected no Associations heading on an empty day, got:\n%s", result.Message)
	}
	if !strings.Contains(result.Message, "No activity has been captured today.") {
		t.Fatalf("expected empty-day message preserved, got:\n%s", result.Message)
	}
}

// TestTodayAssociationsPreserveProjectsSessionsAndRawEventLines covers
// requirement: association context is purely additive and never replaces or
// regroups existing Projects, Sessions, or raw event lines.
func TestTodayAssociationsPreserveProjectsSessionsAndRawEventLines(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	now := time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)
	url := "https://github.com/acme/widget/pull/64"
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:preserve", Source: "github", Type: "github.pull_request", Timestamp: now.Add(-2 * time.Hour), Project: "widget", Payload: fmt.Sprintf(`{"url":%q,"title":"Ship widget release","number":64,"state":"open"}`, url)})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:preserve", Source: "slack", Type: "slack.message", Timestamp: now.Add(-1 * time.Hour), Project: "widget", Payload: fmt.Sprintf(`{"text":%q}`, url)})

	result, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}
	for _, expected := range []string{"Projects", "- widget: 2 events", "Sessions", "Ship widget release", "Associations"} {
		if !strings.Contains(result.Message, expected) {
			t.Fatalf("expected today output to preserve %q, got:\n%s", expected, result.Message)
		}
	}
	if len(result.Events) != 2 || len(result.Sessions) == 0 {
		t.Fatalf("expected raw events and sessions preserved, got events=%d sessions=%d", len(result.Events), len(result.Sessions))
	}
}

// TestTodayAssociationTargetEvaluationIsCappedAtFifty covers requirement:
// evaluate at most the 50 most recent events from today as association
// targets. A qualifying pair whose members are both older than the 50 most
// recent events must not surface, while a qualifying pair within the cap
// still does.
func TestTodayAssociationTargetEvaluationIsCappedAtFifty(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	now := time.Date(2026, 7, 20, 20, 0, 0, 0, time.UTC)
	dayStart := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)

	// Oldest-of-the-day qualifying pair: excluded once 60 more recent filler
	// events push both members out of the top 50 most-recent targets.
	excludedURL := "https://github.com/acme/oldest/pull/1"
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:oldest", Source: "github", Type: "github.pull_request", Timestamp: dayStart.Add(1 * time.Minute), Payload: fmt.Sprintf(`{"url":%q}`, excludedURL)})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:oldest", Source: "slack", Type: "slack.message", Timestamp: dayStart.Add(2 * time.Minute), Payload: fmt.Sprintf(`{"text":%q}`, excludedURL)})

	for i := 0; i < 60; i++ {
		insertAssociationEvent(t, dbPath, associationStoredEvent{
			ID: fmt.Sprintf("file:filler-%02d", i), Source: "file", Type: "file.modified",
			Timestamp: dayStart.Add(10*time.Minute + time.Duration(i)*time.Minute),
			Payload:   fmt.Sprintf(`{"path":"filler-%02d.md"}`, i),
		})
	}

	// Recent qualifying pair: included because both members are within the
	// most recent 50 evaluated targets.
	includedURL := "https://github.com/acme/recent/pull/2"
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:recent", Source: "github", Type: "github.pull_request", Timestamp: now.Add(-20 * time.Minute), Payload: fmt.Sprintf(`{"url":%q}`, includedURL)})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:recent", Source: "slack", Type: "slack.message", Timestamp: now.Add(-10 * time.Minute), Payload: fmt.Sprintf(`{"text":%q}`, includedURL)})

	result, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}
	if !strings.Contains(result.Message, "github:recent, slack:recent") {
		t.Fatalf("expected in-cap association to be rendered, got:\n%s", result.Message)
	}
	if strings.Contains(result.Message, "github:oldest, slack:oldest") {
		t.Fatalf("expected out-of-cap association to be excluded by the 50-target evaluation cap, got:\n%s", result.Message)
	}
	if len(result.Associations) != 1 {
		t.Fatalf("expected exactly one association within the target cap, got %#v", result.Associations)
	}
}

// TestTodayAssociationRenderIsCappedAtFive covers requirement: render at
// most 5 association pairs even when more qualify.
func TestTodayAssociationRenderIsCappedAtFive(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	now := time.Date(2026, 7, 20, 18, 0, 0, 0, time.UTC)

	for i := 0; i < 6; i++ {
		url := fmt.Sprintf("https://github.com/acme/cap%d/pull/%d", i, i)
		// Space out the "most recent cited event" per pair so ranking is
		// unambiguous: pair i's recency is earlier than pair i+1's.
		recent := now.Add(-time.Duration(60-i*5) * time.Minute)
		older := recent.Add(-30 * time.Minute)
		insertAssociationEvent(t, dbPath, associationStoredEvent{ID: fmt.Sprintf("github:cap%d", i), Source: "github", Type: "github.pull_request", Timestamp: older, Payload: fmt.Sprintf(`{"url":%q}`, url)})
		insertAssociationEvent(t, dbPath, associationStoredEvent{ID: fmt.Sprintf("slack:cap%d", i), Source: "slack", Type: "slack.message", Timestamp: recent, Payload: fmt.Sprintf(`{"text":%q}`, url)})
	}

	result, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}
	if len(result.Associations) != 5 {
		t.Fatalf("expected exactly 5 rendered associations, got %d: %#v", len(result.Associations), result.Associations)
	}
	// Pair 0 has the earliest recency of all six pairs, so it must be the one
	// dropped by the 5-pair render cap.
	if strings.Contains(result.Message, "github:cap0, slack:cap0") {
		t.Fatalf("expected least-recent pair to be dropped by the render cap, got:\n%s", result.Message)
	}
	for i := 1; i < 6; i++ {
		expected := fmt.Sprintf("github:cap%d, slack:cap%d", i, i)
		if !strings.Contains(result.Message, expected) {
			t.Fatalf("expected pair %d to be rendered, got:\n%s", i, result.Message)
		}
	}
}

// TestTodayAssociationDoesNotMutateRawEvents covers requirement: computing
// association context never rewrites or deletes raw events.
func TestTodayAssociationDoesNotMutateRawEvents(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	now := time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)
	url := "https://github.com/acme/widget/pull/71"
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:immutable", Source: "github", Type: "github.pull_request", Timestamp: now.Add(-2 * time.Hour), Payload: fmt.Sprintf(`{"url":%q}`, url)})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:immutable", Source: "slack", Type: "slack.message", Timestamp: now.Add(-1 * time.Hour), Payload: fmt.Sprintf(`{"text":%q}`, url)})

	before := snapshotEventRows(t, dbPath)
	if _, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now}); err != nil {
		t.Fatalf("today failed: %v", err)
	}
	after := snapshotEventRows(t, dbPath)
	if before != after {
		t.Fatalf("expected raw event rows to remain unchanged:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestTodayAssociationCLIOutput covers requirement: association context is
// exercised through the `workgraph today` CLI command, with no LLM or
// network dependency required.
func TestTodayAssociationCLIOutput(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	now := time.Now().UTC()
	url := "https://github.com/acme/widget/pull/91"
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:cli-today", Source: "github", Type: "github.pull_request", Timestamp: now.Add(-10 * time.Minute), Payload: fmt.Sprintf(`{"url":%q}`, url)})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:cli-today", Source: "slack", Type: "slack.message", Timestamp: now.Add(-5 * time.Minute), Payload: fmt.Sprintf(`{"text":%q}`, url)})

	output, err := runworkgraph(t, repoRoot(t), "today", "--home", homeDir, "--database", dbPath)
	if err != nil {
		t.Fatalf("today command: %v\n%s", err, output)
	}
	for _, expected := range []string{"Associations", "github:cli-today, slack:cli-today", "score: 100 (high)"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected CLI output to include %q, got:\n%s", expected, output)
		}
	}
}

func snapshotEventRows(t *testing.T, dbPath string) string {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, source, type, timestamp, project, actor, summary, payload_json, created_at FROM events ORDER BY id ASC`)
	if err != nil {
		t.Fatalf("query events snapshot: %v", err)
	}
	defer rows.Close()
	var builder strings.Builder
	for rows.Next() {
		var id, source, eventType, timestamp, payload, createdAt string
		var project, actor, summary sql.NullString
		if err := rows.Scan(&id, &source, &eventType, &timestamp, &project, &actor, &summary, &payload, &createdAt); err != nil {
			t.Fatalf("scan event snapshot: %v", err)
		}
		fmt.Fprintf(&builder, "%s|%s|%s|%s|%s|%s|%s|%s|%s\n", id, source, eventType, timestamp, project.String, actor.String, summary.String, payload, createdAt)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate event snapshot: %v", err)
	}
	return builder.String()
}

func mustJSONArray(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = strconv.Quote(value)
	}
	return "[" + strings.Join(quoted, ",") + "]"
}
