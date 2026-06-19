package facts

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestSuggestsIgnorePathFromNoisyTrackedActivity(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	noisyDir := filepath.Join(watchDir, "GeneratedCache")
	if err := os.MkdirAll(noisyDir, 0o755); err != nil {
		t.Fatalf("create noisy dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	before := readInitSettings(t, filepath.Join(homeDir, "settings.json"))

	_, done, stop := startIgnoreSuggestionCapture(t, homeDir, initResult.DatabasePath, watchDir)
	defer stopIgnoreSuggestionCapture(t, stop, done)

	for i := 0; i < 8; i++ {
		path := filepath.Join(noisyDir, "asset-"+string(rune('a'+i))+".tmp")
		if err := os.WriteFile(path, []byte("noise"), 0o644); err != nil {
			t.Fatalf("write noisy file: %v", err)
		}
		waitForEvent(t, initResult.DatabasePath, "created", path)
	}

	suggestion := waitForSuggestion(t, initResult.DatabasePath, "ignore_path", noisyDir)
	if suggestion.Status != "proposed" || suggestion.Confidence != "high" || suggestion.Lane != "baseline" {
		t.Fatalf("expected proposed high-confidence baseline suggestion, got %#v", suggestion)
	}
	if !strings.Contains(suggestion.Reason, "file events") || !strings.Contains(suggestion.EvidenceJSON, "event_ids") {
		t.Fatalf("expected reason and evidence to explain noisy activity, got reason %q evidence %q", suggestion.Reason, suggestion.EvidenceJSON)
	}

	after := readInitSettings(t, filepath.Join(homeDir, "settings.json"))
	if !reflect.DeepEqual(before.IgnorePaths, after.IgnorePaths) || !reflect.DeepEqual(before.IgnoreNames, after.IgnoreNames) {
		t.Fatalf("expected suggestion not to mutate config, before %#v after %#v", before, after)
	}
}

func TestSuggestsIgnoreNameFromRecurringGeneratedBasename(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	generatedName := "CacheData"
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}
	for i := 0; i < 3; i++ {
		noisyDir := filepath.Join(watchDir, "module-"+string(rune('a'+i)), generatedName)
		if err := os.MkdirAll(noisyDir, 0o755); err != nil {
			t.Fatalf("create repeated generated dir: %v", err)
		}
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	before := readInitSettings(t, filepath.Join(homeDir, "settings.json"))

	_, done, stop := startIgnoreSuggestionCapture(t, homeDir, initResult.DatabasePath, watchDir)
	defer stopIgnoreSuggestionCapture(t, stop, done)

	for i := 0; i < 3; i++ {
		noisyDir := filepath.Join(watchDir, "module-"+string(rune('a'+i)), generatedName)
		for j := 0; j < 3; j++ {
			path := filepath.Join(noisyDir, "item-"+string(rune('a'+j))+".tmp")
			if err := os.WriteFile(path, []byte("noise"), 0o644); err != nil {
				t.Fatalf("write noisy file: %v", err)
			}
			waitForEvent(t, initResult.DatabasePath, "created", path)
		}
	}

	suggestion := waitForSuggestion(t, initResult.DatabasePath, "ignore_name", generatedName)
	if suggestion.Status != "proposed" || suggestion.Confidence != "high" || suggestion.Lane != "baseline" {
		t.Fatalf("expected proposed high-confidence baseline suggestion, got %#v", suggestion)
	}
	if !strings.Contains(suggestion.Reason, "repeated") || !strings.Contains(suggestion.EvidenceJSON, "paths") {
		t.Fatalf("expected reason and evidence to explain recurring basename, got reason %q evidence %q", suggestion.Reason, suggestion.EvidenceJSON)
	}

	after := readInitSettings(t, filepath.Join(homeDir, "settings.json"))
	if !reflect.DeepEqual(before.IgnorePaths, after.IgnorePaths) || !reflect.DeepEqual(before.IgnoreNames, after.IgnoreNames) {
		t.Fatalf("expected suggestion not to mutate config, before %#v after %#v", before, after)
	}
}

func TestApprovingIgnorePathSuggestionAddsPathToConfig(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	ignorePath := filepath.Join(tempDir, "project", "GeneratedCache")
	if err := os.MkdirAll(ignorePath, 0o755); err != nil {
		t.Fatalf("create ignored path: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	suggestion, err := workgraph.UpsertSuggestion(workgraph.SuggestionUpsert{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		Type:         "ignore_path",
		PatternKey:   ignorePath,
		Title:        "Ignore noisy generated path",
		Reason:       "8 file events were captured under a generated-looking path.",
		Confidence:   "high",
		Lane:         "baseline",
		EvidenceJSON: `{"event_ids":["event-1"],"paths":["` + filepath.ToSlash(filepath.Join(ignorePath, "asset.tmp")) + `"]}`,
	})
	if err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	approved, err := workgraph.ApproveSuggestion(workgraph.SuggestionStatusUpdate{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		ID:           suggestion.ID,
	})
	if err != nil {
		t.Fatalf("approve suggestion: %v", err)
	}

	config := readInitSettings(t, filepath.Join(homeDir, "settings.json"))
	if !containsString(config.IgnorePaths, ignorePath) {
		t.Fatalf("expected approved ignore path in config, got %#v", config.IgnorePaths)
	}
	if approved.Status != "approved" {
		t.Fatalf("expected suggestion approved, got %#v", approved)
	}
	assertSuggestionFeedback(t, initResult.DatabasePath, suggestion.ID, "accepted")
}

func TestApprovingIgnoreNameSuggestionAddsNameToConfig(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	ignoreName := "CacheData"

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	suggestion, err := workgraph.UpsertSuggestion(workgraph.SuggestionUpsert{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		Type:         "ignore_name",
		PatternKey:   ignoreName,
		Title:        "Ignore recurring generated name",
		Reason:       "9 file events were captured under a repeated generated-looking basename.",
		Confidence:   "high",
		Lane:         "baseline",
		EvidenceJSON: `{"event_ids":["event-1"],"paths":["/tmp/project/module-a/CacheData/item.tmp"]}`,
	})
	if err != nil {
		t.Fatalf("create suggestion: %v", err)
	}

	output := runWorkgraphCommand(t, nil, "suggestions", "approve", suggestion.ID, "--home", homeDir, "--database", initResult.DatabasePath)
	if !strings.Contains(output, "Suggestion approved") || !strings.Contains(output, suggestion.ID) {
		t.Fatalf("expected approve output, got:\n%s", output)
	}

	config := readInitSettings(t, filepath.Join(homeDir, "settings.json"))
	if !containsString(config.IgnoreNames, ignoreName) {
		t.Fatalf("expected approved ignore name in config, got %#v", config.IgnoreNames)
	}
	assertSuggestionStatus(t, initResult.DatabasePath, suggestion.ID, "approved")
	assertSuggestionFeedback(t, initResult.DatabasePath, suggestion.ID, "accepted")
}

func TestDuplicateIgnoreSuggestionsAreCoalesced(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	noisyDir := filepath.Join(watchDir, "GeneratedCache")
	if err := os.MkdirAll(noisyDir, 0o755); err != nil {
		t.Fatalf("create noisy dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	_, done, stop := startIgnoreSuggestionCapture(t, homeDir, initResult.DatabasePath, watchDir)
	defer stopIgnoreSuggestionCapture(t, stop, done)

	for i := 0; i < 12; i++ {
		path := filepath.Join(noisyDir, "asset-"+string(rune('a'+i))+".tmp")
		if err := os.WriteFile(path, []byte("noise"), 0o644); err != nil {
			t.Fatalf("write noisy file: %v", err)
		}
		waitForEvent(t, initResult.DatabasePath, "created", path)
	}

	suggestion := waitForSuggestion(t, initResult.DatabasePath, "ignore_path", noisyDir)
	db := openIgnoreSuggestionDB(t, initResult.DatabasePath)
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM suggestions WHERE type = ? AND pattern_key = ?`, "ignore_path", noisyDir).Scan(&count); err != nil {
		t.Fatalf("count coalesced suggestions: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected duplicate noisy activity to coalesce into one suggestion, got %d", count)
	}

	var evidence struct {
		EventIDs []string `json:"event_ids"`
	}
	if err := json.Unmarshal([]byte(suggestion.EvidenceJSON), &evidence); err != nil {
		t.Fatalf("parse suggestion evidence: %v", err)
	}
	if len(evidence.EventIDs) < 8 {
		t.Fatalf("expected coalesced suggestion evidence to be updated, got %#v", evidence.EventIDs)
	}
}

func TestIgnoreSuggestionsUseRecentEventWindow(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	noisyDir := filepath.Join(watchDir, "GeneratedCache")
	if err := os.MkdirAll(noisyDir, 0o755); err != nil {
		t.Fatalf("create noisy dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	oldTimestamp := time.Now().Add(-30 * time.Minute).UTC()
	for i := 0; i < 7; i++ {
		insertFileEventAt(t, initResult.DatabasePath, "old-noise-"+string(rune('a'+i)), oldTimestamp, filepath.Join(noisyDir, "old-"+string(rune('a'+i))+".tmp"))
	}

	_, done, stop := startIgnoreSuggestionCapture(t, homeDir, initResult.DatabasePath, watchDir)
	defer stopIgnoreSuggestionCapture(t, stop, done)

	recentPath := filepath.Join(noisyDir, "recent.tmp")
	if err := os.WriteFile(recentPath, []byte("recent"), 0o644); err != nil {
		t.Fatalf("write recent file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", recentPath)

	assertNoSuggestion(t, initResult.DatabasePath, "ignore_path", noisyDir)
}

func startIgnoreSuggestionCapture(t *testing.T, homeDir string, databasePath string, watchDir string) (*workgraph.RunCapture, chan error, context.CancelFunc) {
	t.Helper()

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: databasePath,
		WatchDirs:    []string{watchDir},
		PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start capture: %v", err)
	}

	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		done <- capture.Run(ctx)
	}()
	return capture, done, cancel
}

func stopIgnoreSuggestionCapture(t *testing.T, stop context.CancelFunc, done chan error) {
	t.Helper()

	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("capture run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for capture to stop")
	}
}

func assertNoSuggestion(t *testing.T, databasePath string, suggestionType string, patternKey string) {
	t.Helper()

	db := openIgnoreSuggestionDB(t, databasePath)
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM suggestions WHERE type = ? AND pattern_key = ?`, suggestionType, patternKey).Scan(&count); err != nil {
		t.Fatalf("count suggestions: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no %s suggestion for %q, got %d", suggestionType, patternKey, count)
	}
}

func assertSuggestionStatus(t *testing.T, databasePath string, suggestionID string, expected string) {
	t.Helper()

	db := openIgnoreSuggestionDB(t, databasePath)
	defer db.Close()
	var status string
	if err := db.QueryRow(`SELECT status FROM suggestions WHERE id = ?`, suggestionID).Scan(&status); err != nil {
		t.Fatalf("read suggestion status: %v", err)
	}
	if status != expected {
		t.Fatalf("expected suggestion status %q, got %q", expected, status)
	}
}

func assertSuggestionFeedback(t *testing.T, databasePath string, suggestionID string, expectedAction string) {
	t.Helper()

	db := openIgnoreSuggestionDB(t, databasePath)
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM suggestion_feedback WHERE suggestion_id = ? AND action = ?`, suggestionID, expectedAction).Scan(&count); err != nil {
		t.Fatalf("count suggestion feedback: %v", err)
	}
	if count == 0 {
		t.Fatalf("expected suggestion feedback action %q for %q", expectedAction, suggestionID)
	}
}

func insertFileEventAt(t *testing.T, databasePath string, id string, timestamp time.Time, path string) {
	t.Helper()

	db := openIgnoreSuggestionDB(t, databasePath)
	defer db.Close()
	payload, err := json.Marshal(map[string]string{"path": path, "operation": "created"})
	if err != nil {
		t.Fatalf("encode file event payload: %v", err)
	}
	_, err = db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, created_at)
		VALUES (?, 'file', 'file.created', ?, ?, ?)`,
		id,
		timestamp.Format(time.RFC3339Nano),
		string(payload),
		timestamp.Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("insert file event: %v", err)
	}
}

func waitForSuggestion(t *testing.T, databasePath string, suggestionType string, patternKey string) workgraph.Suggestion {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		db := openIgnoreSuggestionDB(t, databasePath)
		var suggestion workgraph.Suggestion
		err := db.QueryRow(`SELECT id, type, COALESCE(pattern_key, ''), title, reason, confidence, lane, status, evidence_json, created_at, updated_at, COALESCE(resolved_at, '')
			FROM suggestions
			WHERE type = ? AND pattern_key = ?`, suggestionType, patternKey).Scan(
			&suggestion.ID,
			&suggestion.Type,
			&suggestion.PatternKey,
			&suggestion.Title,
			&suggestion.Reason,
			&suggestion.Confidence,
			&suggestion.Lane,
			&suggestion.Status,
			&suggestion.EvidenceJSON,
			&suggestion.CreatedAt,
			&suggestion.UpdatedAt,
			&suggestion.ResolvedAt,
		)
		db.Close()
		if err == nil {
			return suggestion
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s suggestion %q", suggestionType, patternKey)
	return workgraph.Suggestion{}
}

func openIgnoreSuggestionDB(t *testing.T, databasePath string) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	return db
}
