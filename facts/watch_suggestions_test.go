package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestSuggestsWatchRootFromUnwatchedGitActivity(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	codeDir := filepath.Join(tempDir, "Code")
	repoDir := filepath.Join(codeDir, "Cupcake")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	commitSHA := createGitCommit(t, repoDir, "Add cupcake API")

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	writeInitSettings(t, initResult.SettingsPath, initSettingsFile{
		WatchDirs:   []string{},
		RepoDirs:    []string{codeDir},
		IgnorePaths: []string{homeDir},
		IgnoreNames: []string{".git"},
	})
	before := readInitSettings(t, initResult.SettingsPath)

	if _, err := workgraph.CaptureGitCommits(workgraph.GitCaptureConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
	}); err != nil {
		t.Fatalf("git capture failed: %v", err)
	}

	suggestion := waitForSuggestion(t, initResult.DatabasePath, "watch_root", repoDir)
	if suggestion.Status != "proposed" || suggestion.Confidence != "high" || suggestion.Lane != "baseline" {
		t.Fatalf("expected proposed high-confidence baseline watch_root suggestion, got %#v", suggestion)
	}
	if suggestion.PatternKey != repoDir {
		t.Fatalf("expected pattern_key %q, got %q", repoDir, suggestion.PatternKey)
	}
	if !strings.Contains(strings.ToLower(suggestion.Reason), "git") || !strings.Contains(suggestion.EvidenceJSON, "git.commit:"+commitSHA) {
		t.Fatalf("expected reason and evidence to explain git activity, got reason %q evidence %q", suggestion.Reason, suggestion.EvidenceJSON)
	}
	if !strings.Contains(suggestion.EvidenceJSON, `"git"`) {
		t.Fatalf("expected evidence to record git source, got %s", suggestion.EvidenceJSON)
	}

	after := readInitSettings(t, initResult.SettingsPath)
	if !reflect.DeepEqual(before.WatchDirs, after.WatchDirs) {
		t.Fatalf("expected watch suggestion not to mutate watch_dirs without approval, before %#v after %#v", before.WatchDirs, after.WatchDirs)
	}
}

func TestApprovingWatchRootSuggestionAddsDirectoryToConfig(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	existingDir := filepath.Join(tempDir, "notes")
	suggestedDir := filepath.Join(tempDir, "Code", "Cupcake")
	for _, dir := range []string{existingDir, suggestedDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create dir %q: %v", dir, err)
		}
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if _, err := workgraph.AddWatchDir(workgraph.SettingsWatchConfig{
		HomeDir: homeDir,
		Path:    existingDir,
	}); err != nil {
		t.Fatalf("add existing watch dir: %v", err)
	}

	suggestion, err := workgraph.UpsertSuggestion(workgraph.SuggestionUpsert{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		Type:         "watch_root",
		PatternKey:   suggestedDir,
		Title:        "Watch Cupcake",
		Reason:       "Git activity was captured in unwatched local repository.",
		Confidence:   "high",
		Lane:         "baseline",
		EvidenceJSON: `{"source":"git","sources":["git"],"path":"` + filepath.ToSlash(suggestedDir) + `","paths":["` + filepath.ToSlash(suggestedDir) + `"],"event_ids":["git.commit:abc123"]}`,
	})
	if err != nil {
		t.Fatalf("create watch_root suggestion: %v", err)
	}

	output := runWorkgraphCommand(t, nil, "suggestions", "approve", suggestion.ID, "--home", homeDir, "--database", initResult.DatabasePath)
	if !strings.Contains(output, "Suggestion approved") || !strings.Contains(output, suggestion.ID) {
		t.Fatalf("expected approve output, got:\n%s", output)
	}

	config := readInitSettings(t, initResult.SettingsPath)
	expectedWatchDirs := []string{suggestedDir, existingDir}
	if !reflect.DeepEqual(config.WatchDirs, expectedWatchDirs) {
		t.Fatalf("expected approved watch_root prepended to watch_dirs %#v, got %#v", expectedWatchDirs, config.WatchDirs)
	}
	assertSuggestionStatus(t, initResult.DatabasePath, suggestion.ID, "approved")
	assertSuggestionFeedback(t, initResult.DatabasePath, suggestion.ID, "accepted")
}

func TestDuplicateWatchRootSuggestionsAreCoalesced(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	codeDir := filepath.Join(tempDir, "Code")
	repoDir := filepath.Join(codeDir, "Cupcake")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	firstSHA := createGitCommit(t, repoDir, "Add cupcake API")

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	writeInitSettings(t, initResult.SettingsPath, initSettingsFile{
		WatchDirs:   []string{},
		RepoDirs:    []string{codeDir},
		IgnorePaths: []string{homeDir},
		IgnoreNames: []string{".git"},
	})

	if _, err := workgraph.CaptureGitCommits(workgraph.GitCaptureConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
	}); err != nil {
		t.Fatalf("first git capture failed: %v", err)
	}
	firstSuggestion := waitForSuggestion(t, initResult.DatabasePath, "watch_root", repoDir)

	secondSHA := commitGitFile(t, repoDir, "handler.go", "package main\n", "Add cupcake handler")
	if _, err := workgraph.CaptureGitCommits(workgraph.GitCaptureConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
	}); err != nil {
		t.Fatalf("second git capture failed: %v", err)
	}
	secondSuggestion := waitForSuggestion(t, initResult.DatabasePath, "watch_root", repoDir)

	if secondSuggestion.ID != firstSuggestion.ID {
		t.Fatalf("expected coalesced suggestion to keep id %q, got %q", firstSuggestion.ID, secondSuggestion.ID)
	}

	db := openIgnoreSuggestionDB(t, initResult.DatabasePath)
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM suggestions WHERE type = ? AND pattern_key = ?`, "watch_root", repoDir).Scan(&count); err != nil {
		t.Fatalf("count watch_root suggestions: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one coalesced watch_root suggestion, got %d", count)
	}

	var evidence struct {
		EventIDs []string `json:"event_ids"`
	}
	if err := json.Unmarshal([]byte(secondSuggestion.EvidenceJSON), &evidence); err != nil {
		t.Fatalf("parse suggestion evidence: %v", err)
	}
	if !containsString(evidence.EventIDs, "git.commit:"+firstSHA) || !containsString(evidence.EventIDs, "git.commit:"+secondSHA) {
		t.Fatalf("expected coalesced evidence to include both commits, got %#v", evidence.EventIDs)
	}
}
