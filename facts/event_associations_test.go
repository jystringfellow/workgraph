package facts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestFileEventInfersProjectFromNearestGitRoot(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "Code")
	repoDir := filepath.Join(watchDir, "Cupcake")
	sourceDir := filepath.Join(repoDir, "Cupcake.API")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatalf("create git metadata dir: %v", err)
	}
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("create source dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	target := filepath.Join(sourceDir, "OrdersController.cs")
	if err := os.WriteFile(target, []byte("controller"), 0o644); err != nil {
		t.Fatalf("create source file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", target)

	project := projectForEvent(t, initResult.DatabasePath, "created", target)
	if project != "Cupcake" {
		t.Fatalf("expected project %q from nearest git root, got %q", "Cupcake", project)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestFileEventFallsBackToConfiguredWatchRoot(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "Downloads")
	notesDir := filepath.Join(watchDir, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("create notes dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	target := filepath.Join(notesDir, "scratch.md")
	if err := os.WriteFile(target, []byte("notes"), 0o644); err != nil {
		t.Fatalf("create note: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", target)

	project := projectForEvent(t, initResult.DatabasePath, "created", target)
	if project != "Downloads" {
		t.Fatalf("expected project %q from configured watch root, got %q", "Downloads", project)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestFileEventPreservesArtifactPath(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "Code")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir: homeDir, DatabasePath: initResult.DatabasePath, WatchDirs: []string{watchDir},
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() { done <- capture.Run(ctx) }()

	target := filepath.Join(watchDir, "notes.md")
	if err := os.WriteFile(target, []byte("notes"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", target)

	db, err := sql.Open("sqlite3", initResult.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var storedPath string
	if err := db.QueryRow(`SELECT json_extract(payload_json, '$.path') FROM events WHERE source = 'file' AND type = 'file.created' AND json_extract(payload_json, '$.path') = ?`, target).Scan(&storedPath); err != nil {
		t.Fatalf("query artifact path: %v", err)
	}
	if storedPath != target {
		t.Fatalf("expected artifact path %q, got %q", target, storedPath)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestAssociatedSessionsUseProjectAndTime(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	base := time.Date(2026, 7, 16, 9, 0, 0, 0, time.Local)
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "file:1", Source: "file", Type: "file.modified", Timestamp: base, Project: "workgraph", Payload: `{"path":"README.md"}`})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "git:1", Source: "git", Type: "git.commit", Timestamp: base.Add(20 * time.Minute), Project: "workgraph", Payload: `{"commit":"abcdef0123456789"}`})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "git:2", Source: "git", Type: "git.commit", Timestamp: base.Add(51 * time.Minute), Project: "workgraph", Payload: `{"commit":"1234567890abcdef"}`})

	result, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: base.Add(time.Hour)})
	if err != nil {
		t.Fatalf("today failed: %v", err)
	}
	if len(result.Sessions) != 2 || len(result.Sessions[0].Events) != 2 || len(result.Sessions[1].Events) != 1 {
		t.Fatalf("expected deterministic 2+1 sessions, got %#v", result.Sessions)
	}
}

func TestAssociationExactURLIsStrongExplainableAndCanonical(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	githubID := "github.pull_request:acme/widget:42"
	slackID := "slack.message:C1:1"
	insertAssociationEvent(t, dbPath, associationStoredEvent{
		ID: githubID, Source: "github", Type: "github.pull_request", Timestamp: base,
		Project: "widget", Summary: "Improve login redirect handling",
		Payload: `{"repository":"acme/widget","number":42,"url":"HTTPS://github.com/acme/widget/pull/42/?b=2&a=1#discussion","title":"Improve login redirect handling"}`,
	})
	insertAssociationEvent(t, dbPath, associationStoredEvent{
		ID: slackID, Source: "slack", Type: "slack.message", Timestamp: base.Add(4 * time.Minute),
		Project: "widget", Summary: "Review https://github.com/acme/widget/pull/42?a=1&b=2",
		Payload: `{"text":"Review https://github.com/acme/widget/pull/42?a=1&b=2","permalink":"https://slack.example/C1/1"}`,
	})

	first, err := workgraph.ExplainEventAssociations(workgraph.AssociationExplainConfig{HomeDir: homeDir, DatabasePath: dbPath, EventID: githubID})
	if err != nil {
		t.Fatalf("explain association: %v", err)
	}
	second, err := workgraph.ExplainEventAssociations(workgraph.AssociationExplainConfig{HomeDir: homeDir, DatabasePath: dbPath, EventID: slackID})
	if err != nil {
		t.Fatalf("explain reversed association: %v", err)
	}
	if len(first.Candidates) != 1 || len(second.Candidates) != 1 {
		t.Fatalf("expected one candidate from either direction, got %d and %d", len(first.Candidates), len(second.Candidates))
	}
	a, b := first.Candidates[0], second.Candidates[0]
	if a.Score != 100 || a.Confidence != "high" || a.Status != "proposed" {
		t.Fatalf("expected proposed high score 100, got %#v", a)
	}
	if a.SuggestionID != b.SuggestionID || a.PatternKey != b.PatternKey {
		t.Fatalf("expected stable pair identity, got %#v and %#v", a, b)
	}
	wantIDs := []string{githubID, slackID}
	if strings.Join(a.EventIDs, "\n") != strings.Join(wantIDs, "\n") {
		t.Fatalf("expected canonical ids %v, got %v", wantIDs, a.EventIDs)
	}
	if !containsAssociationText(a.Reasons, "identical normalized URL https://github.com/acme/widget/pull/42?a=1&b=2") {
		t.Fatalf("expected normalized URL reason, got %v", a.Reasons)
	}

	var evidence struct {
		EventIDs       []string `json:"event_ids"`
		Score          int      `json:"score"`
		Confidence     string   `json:"confidence"`
		Reasons        []string `json:"reasons"`
		MatchedSignals []string `json:"matched_signals"`
	}
	if err := json.Unmarshal([]byte(a.EvidenceJSON), &evidence); err != nil {
		t.Fatalf("decode evidence: %v", err)
	}
	if evidence.Score != 100 || evidence.Confidence != "high" || len(evidence.Reasons) == 0 || len(evidence.MatchedSignals) == 0 {
		t.Fatalf("expected scored explainable evidence, got %#v", evidence)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var count int
	var createdAt, updatedAt string
	if err := db.QueryRow(`SELECT COUNT(*), MIN(created_at), MIN(updated_at) FROM suggestions WHERE type = 'association'`).Scan(&count, &createdAt, &updatedAt); err != nil {
		t.Fatalf("count association suggestions: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one coalesced association suggestion, got %d", count)
	}
	wantDerivedAt := base.Add(4 * time.Minute).Format(time.RFC3339Nano)
	if createdAt != wantDerivedAt || updatedAt != wantDerivedAt {
		t.Fatalf("expected deterministic derived timestamps %q, got created=%q updated=%q", wantDerivedAt, createdAt, updatedAt)
	}
}

func TestAssociationExtractsRepositoryCommitAndBranchSignals(t *testing.T) {
	tests := []struct {
		name       string
		left       string
		right      string
		wantScore  int
		wantReason string
	}{
		{
			name:       "repository plus issue number",
			left:       `{"repository":"acme/widget","number":42}`,
			right:      `{"repository":"ACME/WIDGET","number":42}`,
			wantScore:  90,
			wantReason: "same repository and issue or pull-request number acme/widget#42",
		},
		{
			name:       "full and abbreviated commit SHA",
			left:       `{"commit":"abcdef0123456789abcdef0123456789abcdef01"}`,
			right:      `{"headSha":"abcdef012345"}`,
			wantScore:  85,
			wantReason: "matching commit SHA abcdef012345",
		},
		{
			name:       "repository plus branch",
			left:       `{"repository":"acme/widget","branch":"refs/heads/Feature/Login"}`,
			right:      `{"repository":"acme/widget","headRefName":"feature/login"}`,
			wantScore:  65,
			wantReason: "same repository and branch acme/widget@feature/login",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			homeDir, dbPath := initAssociationStore(t)
			base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
			insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "left", Source: "git", Type: "git.commit", Timestamp: base, Payload: test.left})
			insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "right", Source: "github", Type: "github.pull_request", Timestamp: base.Add(3 * time.Hour), Payload: test.right})
			result, err := workgraph.ExplainEventAssociations(workgraph.AssociationExplainConfig{HomeDir: homeDir, DatabasePath: dbPath, EventID: "left"})
			if err != nil {
				t.Fatalf("explain association: %v", err)
			}
			if len(result.Candidates) != 1 || result.Candidates[0].Score != test.wantScore || !containsAssociationText(result.Candidates[0].Reasons, test.wantReason) {
				t.Fatalf("expected score %d and reason %q, got %#v", test.wantScore, test.wantReason, result.Candidates)
			}
		})
	}
}

func TestAssociationScoringAndOrderingAreDeterministic(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	targetID := "mail:target"
	url := "https://github.com/acme/widget/issues/77"
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: targetID, Source: "mail", Type: "mail.message", Timestamp: base, Payload: fmt.Sprintf(`{"body_text":%q}`, url)})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:later", Source: "github", Type: "github.issue", Timestamp: base.Add(5 * time.Minute), Payload: fmt.Sprintf(`{"url":%q}`, url)})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:earlier-z", Source: "slack", Type: "slack.message", Timestamp: base.Add(-10 * time.Minute), Payload: fmt.Sprintf(`{"text":%q}`, url)})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "calendar:earlier-a", Source: "calendar", Type: "calendar.event", Timestamp: base.Add(-10 * time.Minute), Payload: fmt.Sprintf(`{"meeting_url":%q}`, url)})

	result, err := workgraph.ExplainEventAssociations(workgraph.AssociationExplainConfig{HomeDir: homeDir, DatabasePath: dbPath, EventID: targetID})
	if err != nil {
		t.Fatalf("explain association: %v", err)
	}
	want := []string{"github:later", "calendar:earlier-a", "slack:earlier-z"}
	if len(result.Candidates) != len(want) {
		t.Fatalf("expected %d candidates, got %#v", len(want), result.Candidates)
	}
	for i, candidate := range result.Candidates {
		if candidate.RelatedEventID != want[i] || candidate.Score != 100 {
			t.Fatalf("candidate %d: expected %q score 100, got %#v", i, want[i], candidate)
		}
	}
}

func TestAssociationFuzzyScoreIsExplicit(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "mail:fuzzy", Source: "mail", Type: "mail.message", Timestamp: base, Project: "widget", Summary: "Investigate login redirect timeout"})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:fuzzy", Source: "slack", Type: "slack.message", Timestamp: base.Add(10 * time.Minute), Project: "Widget", Summary: "Login redirect timeout investigation"})

	result, err := workgraph.ExplainEventAssociations(workgraph.AssociationExplainConfig{HomeDir: homeDir, DatabasePath: dbPath, EventID: "mail:fuzzy"})
	if err != nil {
		t.Fatalf("explain association: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("expected one fuzzy candidate, got %#v", result.Candidates)
	}
	candidate := result.Candidates[0]
	if candidate.Score != 70 || candidate.Confidence != "medium" {
		t.Fatalf("expected deterministic fuzzy score 70 medium, got %#v", candidate)
	}
	for _, expected := range []string{"normalized title-token overlap", "same project widget", "timestamps within 15 minutes"} {
		if !containsAssociationText(candidate.Reasons, expected) {
			t.Fatalf("expected reason containing %q, got %v", expected, candidate.Reasons)
		}
	}
}

func TestAssociationExcludesSameSource(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	url := "https://github.com/acme/widget/pull/9"
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:1", Source: "slack", Type: "slack.message", Timestamp: base, Payload: fmt.Sprintf(`{"text":%q}`, url)})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:2", Source: "slack", Type: "slack.thread_reply", Timestamp: base.Add(time.Minute), Payload: fmt.Sprintf(`{"text":%q}`, url)})
	assertNoAssociationCandidates(t, homeDir, dbPath, "slack:1")
}

func TestAssociationDoesNotMatchIssueNumberAcrossRepositories(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:a", Source: "github", Type: "github.issue", Timestamp: base, Payload: `{"repository":"acme/alpha","number":42,"title":"Status update"}`})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "mail:b", Source: "mail", Type: "mail.message", Timestamp: base.Add(time.Minute), Payload: `{"repository":"acme/beta","number":42,"subject":"Status update"}`})
	assertNoAssociationCandidates(t, homeDir, dbPath, "github:a")
}

func TestAssociationRejectsTimestampOnlyAndGenericTitles(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "calendar:generic", Source: "calendar", Type: "calendar.event", Timestamp: base, Project: "work", Summary: "Project status update", Payload: `{"title":"Project status update"}`})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "mail:generic", Source: "mail", Type: "mail.message", Timestamp: base.Add(time.Minute), Project: "work", Summary: "Work status update", Payload: `{"subject":"Work status update"}`})
	assertNoAssociationCandidates(t, homeDir, dbPath, "calendar:generic")
}

func TestAssociationDismissalSurvivesReevaluation(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	url := "https://github.com/acme/widget/pull/12"
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:dismiss", Source: "github", Type: "github.pull_request", Timestamp: base, Payload: fmt.Sprintf(`{"url":%q}`, url)})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:dismiss", Source: "slack", Type: "slack.message", Timestamp: base.Add(time.Minute), Payload: fmt.Sprintf(`{"text":%q}`, url)})
	first, err := workgraph.ExplainEventAssociations(workgraph.AssociationExplainConfig{HomeDir: homeDir, DatabasePath: dbPath, EventID: "github:dismiss"})
	if err != nil || len(first.Candidates) != 1 {
		t.Fatalf("produce association: result=%#v err=%v", first, err)
	}
	if _, err := workgraph.DismissSuggestion(workgraph.SuggestionStatusUpdate{HomeDir: homeDir, DatabasePath: dbPath, ID: first.Candidates[0].SuggestionID, ReasonCode: "not_related"}); err != nil {
		t.Fatalf("dismiss association: %v", err)
	}
	second, err := workgraph.ExplainEventAssociations(workgraph.AssociationExplainConfig{HomeDir: homeDir, DatabasePath: dbPath, EventID: "slack:dismiss"})
	if err != nil {
		t.Fatalf("reevaluate association: %v", err)
	}
	if len(second.Candidates) != 1 || second.Candidates[0].Status != "dismissed" {
		t.Fatalf("expected dismissed lifecycle to survive, got %#v", second.Candidates)
	}
}

func TestAssociationApprovalIsLifecycleOnly(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	url := "https://github.com/acme/widget/issues/5"
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:approve", Source: "github", Type: "github.issue", Timestamp: base, Payload: fmt.Sprintf(`{"url":%q}`, url)})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "mail:approve", Source: "mail", Type: "mail.message", Timestamp: base.Add(time.Minute), Payload: fmt.Sprintf(`{"body_text":%q}`, url)})
	result, err := workgraph.ExplainEventAssociations(workgraph.AssociationExplainConfig{HomeDir: homeDir, DatabasePath: dbPath, EventID: "github:approve"})
	if err != nil || len(result.Candidates) != 1 {
		t.Fatalf("produce association: result=%#v err=%v", result, err)
	}
	approved, err := workgraph.ApproveSuggestion(workgraph.SuggestionStatusUpdate{HomeDir: homeDir, DatabasePath: dbPath, ID: result.Candidates[0].SuggestionID})
	if err != nil {
		t.Fatalf("approve association: %v", err)
	}
	if approved.Status != "approved" {
		t.Fatalf("expected approved lifecycle, got %#v", approved)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var events int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 2 {
		t.Fatalf("approval must not mutate raw events, got %d", events)
	}
}

func TestAssociationExplainCommandAndInsufficientEvidence(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	url := "https://github.com/acme/widget/pull/99"
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "github:cli", Source: "github", Type: "github.pull_request", Timestamp: base, Payload: fmt.Sprintf(`{"url":%q}`, url)})
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "slack:cli", Source: "slack", Type: "slack.message", Timestamp: base.Add(time.Minute), Payload: fmt.Sprintf(`{"text":%q}`, url)})

	output, err := runworkgraph(t, repoRoot(t), "associations", "explain", "github:cli", "--home", homeDir, "--database", dbPath)
	if err != nil {
		t.Fatalf("association explain command: %v\n%s", err, output)
	}
	for _, expected := range []string{"Event: github:cli", "Related associations", "slack:cli", "score: 100 (high)", "state: proposed", "cited events: github:cli, slack:cli", "identical normalized URL"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected CLI output to include %q, got:\n%s", expected, output)
		}
	}

	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "file:alone", Source: "file", Type: "file.modified", Timestamp: base, Payload: `{"path":"README.md"}`})
	output, err = runworkgraph(t, repoRoot(t), "associations", "explain", "file:alone", "--home", homeDir, "--database", dbPath)
	if err != nil {
		t.Fatalf("insufficient association explain command: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "No related events met the 60-point evidence threshold") {
		t.Fatalf("expected honest insufficient-evidence output, got:\n%s", output)
	}

	output, err = runworkgraph(t, repoRoot(t), "associations", "explain", "missing", "--home", homeDir, "--database", dbPath)
	if err == nil || !strings.Contains(string(output), `event "missing" not found`) {
		t.Fatalf("expected not-found error, err=%v output=%s", err, output)
	}
}

func TestAssociationCandidateSelectionIsBounded(t *testing.T) {
	homeDir, dbPath := initAssociationStore(t)
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	targetURL := "https://github.com/acme/widget/issues/123"
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "target", Source: "github", Type: "github.issue", Timestamp: base, Payload: fmt.Sprintf(`{"url":%q}`, targetURL)})
	for i := 0; i < 200; i++ {
		insertAssociationEvent(t, dbPath, associationStoredEvent{ID: fmt.Sprintf("near:%03d", i), Source: "slack", Type: "slack.message", Timestamp: base.Add(time.Duration(i+1) * time.Minute), Payload: fmt.Sprintf(`{"text":"unrelated detail %03d"}`, i)})
	}
	insertAssociationEvent(t, dbPath, associationStoredEvent{ID: "far:exact", Source: "mail", Type: "mail.message", Timestamp: base.Add(6 * 24 * time.Hour), Payload: fmt.Sprintf(`{"body_text":%q}`, targetURL)})

	result, err := workgraph.ExplainEventAssociations(workgraph.AssociationExplainConfig{HomeDir: homeDir, DatabasePath: dbPath, EventID: "target"})
	if err != nil {
		t.Fatalf("explain bounded candidates: %v", err)
	}
	if result.CandidatesConsidered != 200 {
		t.Fatalf("expected exactly 200 considered candidates, got %d", result.CandidatesConsidered)
	}
	if len(result.Candidates) != 0 {
		t.Fatalf("expected farther exact match to remain outside bounded set, got %#v", result.Candidates)
	}
}

type associationStoredEvent struct {
	ID        string
	Source    string
	Type      string
	Timestamp time.Time
	Project   string
	Summary   string
	Payload   string
}

func initAssociationStore(t *testing.T) (string, string) {
	t.Helper()
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	return homeDir, result.DatabasePath
}

func insertAssociationEvent(t *testing.T, dbPath string, event associationStoredEvent) {
	t.Helper()
	if event.Payload == "" {
		event.Payload = `{}`
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, summary, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.Source, event.Type, event.Timestamp.UTC().Format(time.RFC3339Nano), event.Payload, emptyAssociationString(event.Project), emptyAssociationString(event.Summary), event.Timestamp.UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("insert event %q: %v", event.ID, err)
	}
}

func emptyAssociationString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func assertNoAssociationCandidates(t *testing.T, homeDir, dbPath, eventID string) {
	t.Helper()
	result, err := workgraph.ExplainEventAssociations(workgraph.AssociationExplainConfig{HomeDir: homeDir, DatabasePath: dbPath, EventID: eventID})
	if err != nil {
		t.Fatalf("explain association: %v", err)
	}
	if len(result.Candidates) != 0 {
		t.Fatalf("expected no association candidates, got %#v", result.Candidates)
	}
}

func containsAssociationText(values []string, substring string) bool {
	for _, value := range values {
		if strings.Contains(value, substring) {
			return true
		}
	}
	return false
}

func projectForEvent(t *testing.T, dbPath, operation, path string) string {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var project string
	err = db.QueryRow(`
		SELECT project
		FROM events
		WHERE source = 'file'
			AND type = ?
			AND json_extract(payload_json, '$.operation') = ?
			AND json_extract(payload_json, '$.path') = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, "file."+operation, operation, path).Scan(&project)
	if err != nil {
		t.Fatalf("query event project: %v", err)
	}

	return project
}
