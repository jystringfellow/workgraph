package facts

import (
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

func TestSuggestionScanFindsGeneratedDirectoryPressureWithoutChangingSettings(t *testing.T) {
	homeDir, databasePath, _, releasePath := initializedSuggestionScanTree(t, 16)

	output := runWorkgraphCommand(t, nil, "suggestions", "scan", "--home", homeDir, "--database", databasePath, "--type", "ignore")
	for _, expected := range []string{
		"Suggestion scan complete",
		"Type: ignore",
		"Scan truncated: no",
		"ignore_path",
		releasePath,
		"Run 'workgraph suggestions show <id>' to inspect evidence.",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected suggestion scan output to include %q, got:\n%s", expected, output)
		}
	}

	listed, err := workgraph.ListSuggestions(workgraph.SuggestionListConfig{
		HomeDir:      homeDir,
		DatabasePath: databasePath,
		Status:       "proposed",
	})
	if err != nil {
		t.Fatalf("list scanned suggestions: %v", err)
	}
	if len(listed.Suggestions) != 1 {
		t.Fatalf("expected one proposed scan suggestion, got %+v", listed.Suggestions)
	}
	suggestion := listed.Suggestions[0]
	if suggestion.Type != "ignore_path" || suggestion.PatternKey != releasePath || suggestion.Lane != "baseline" {
		t.Fatalf("expected deterministic ignore_path suggestion for %q, got %+v", releasePath, suggestion)
	}
	for _, expected := range []string{"filesystem_scan", "generated directory name: release", "directory pressure:"} {
		if !strings.Contains(suggestion.EvidenceJSON, expected) {
			t.Fatalf("expected scan evidence to include %q, got %s", expected, suggestion.EvidenceJSON)
		}
	}

	settings := readCLIInitSettings(t, filepath.Join(homeDir, "settings.json"))
	if containsString(settings.IgnorePaths, releasePath) {
		t.Fatalf("scan must not apply its proposed ignore path, got %#v", settings.IgnorePaths)
	}
}

func TestSuggestionScanUsesRecentEventsForRepeatedGeneratedNames(t *testing.T) {
	userHome := fakeUserHomeWithDirs(t, "Code")
	watchRoot := filepath.Join(userHome, "Code")
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, MemoryDir: filepath.Join(t.TempDir(), "memory")})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	var eventPaths []string
	for i := 0; i < 3; i++ {
		project := filepath.Join(watchRoot, fmt.Sprintf("project-%d", i))
		if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
			t.Fatalf("create project marker: %v", err)
		}
		cache := filepath.Join(project, ".cache")
		if err := os.MkdirAll(cache, 0o755); err != nil {
			t.Fatalf("create cache: %v", err)
		}
		for j := 0; j < 3; j++ {
			eventPaths = append(eventPaths, filepath.Join(cache, fmt.Sprintf("entry-%d", j)))
		}
	}
	insertSuggestionScanEvents(t, result.DatabasePath, eventPaths, time.Now().UTC())

	scan, err := workgraph.ScanSuggestions(workgraph.SuggestionScanConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Type:         "ignore",
	})
	if err != nil {
		t.Fatalf("scan suggestions: %v", err)
	}

	var found *workgraph.Suggestion
	for i := range scan.Suggestions {
		if scan.Suggestions[i].Type == "ignore_name" && scan.Suggestions[i].PatternKey == ".cache" {
			found = &scan.Suggestions[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected repeated .cache event evidence to produce ignore_name suggestion, got %+v", scan.Suggestions)
	}
	for _, expected := range []string{"recent_file_events", "9 captured file events", "3 candidate paths"} {
		if !strings.Contains(found.EvidenceJSON, expected) && !strings.Contains(found.Reason, expected) {
			t.Fatalf("expected repeated-name suggestion to include %q, got reason=%q evidence=%s", expected, found.Reason, found.EvidenceJSON)
		}
	}
}

func TestSuggestionScanIsBoundedAndRespectsExistingIgnores(t *testing.T) {
	homeDir, databasePath, _, releasePath := initializedSuggestionScanTree(t, 20)

	bounded, err := workgraph.ScanSuggestions(workgraph.SuggestionScanConfig{
		HomeDir:        homeDir,
		DatabasePath:   databasePath,
		Type:           "ignore",
		MaxDirectories: 5,
	})
	if err != nil {
		t.Fatalf("bounded suggestion scan: %v", err)
	}
	if !bounded.Truncated || bounded.DirectoriesInspected != 5 {
		t.Fatalf("expected scan to stop at five directories, got %+v", bounded)
	}

	secondHome, secondDatabase, _, secondRelease := initializedSuggestionScanTree(t, 20)
	if _, err := workgraph.AddIgnorePath(workgraph.SettingsIgnoreConfig{HomeDir: secondHome, Path: secondRelease}); err != nil {
		t.Fatalf("add existing ignore: %v", err)
	}
	ignored, err := workgraph.ScanSuggestions(workgraph.SuggestionScanConfig{
		HomeDir:      secondHome,
		DatabasePath: secondDatabase,
		Type:         "ignore",
	})
	if err != nil {
		t.Fatalf("scan with existing ignore: %v", err)
	}
	for _, suggestion := range ignored.Suggestions {
		if suggestion.PatternKey == secondRelease {
			t.Fatalf("expected existing ignore %q not to be suggested, got %+v", secondRelease, ignored.Suggestions)
		}
	}
	_ = releasePath
}

func TestSuggestionScanRejectsUnsupportedTypeWithoutWriting(t *testing.T) {
	homeDir, databasePath, _, _ := initializedSuggestionScanTree(t, 16)
	output, err := runWorkgraphCommandAllowError(nil, "suggestions", "scan", "--home", homeDir, "--database", databasePath, "--type", "semantic")
	if err == nil {
		t.Fatalf("expected unsupported scan type to fail, got:\n%s", output)
	}
	if !strings.Contains(output, `unsupported suggestion scan type "semantic"`) {
		t.Fatalf("expected unsupported type error, got:\n%s", output)
	}
	listed, err := workgraph.ListSuggestions(workgraph.SuggestionListConfig{HomeDir: homeDir, DatabasePath: databasePath})
	if err != nil {
		t.Fatalf("list suggestions: %v", err)
	}
	if len(listed.Suggestions) != 0 {
		t.Fatalf("unsupported scan must not store suggestions, got %+v", listed.Suggestions)
	}
}

func initializedSuggestionScanTree(t *testing.T, childDirectories int) (string, string, string, string) {
	t.Helper()
	userHome := fakeUserHomeWithDirs(t, "Code")
	watchRoot := filepath.Join(userHome, "Code")
	project := filepath.Join(watchRoot, "FracTile")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatalf("create project marker: %v", err)
	}
	releasePath := filepath.Join(project, "release")
	for i := 0; i < childDirectories; i++ {
		path := filepath.Join(releasePath, fmt.Sprintf("dir-%02d", i))
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("create release directory: %v", err)
		}
	}

	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, MemoryDir: filepath.Join(t.TempDir(), "memory")})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	return homeDir, result.DatabasePath, watchRoot, releasePath
}

func insertSuggestionScanEvents(t *testing.T, databasePath string, paths []string, timestamp time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	for i, path := range paths {
		payload, err := json.Marshal(map[string]string{"path": path, "operation": "modified"})
		if err != nil {
			t.Fatalf("encode event payload: %v", err)
		}
		instant := timestamp.Add(time.Duration(i) * time.Millisecond).Format(time.RFC3339Nano)
		if _, err := db.Exec(`INSERT INTO events
			(id, source, type, timestamp, payload_json, project, created_at)
			VALUES (?, 'file', 'file.modified', ?, ?, 'scan-test', ?)`, fmt.Sprintf("scan-event-%02d", i), instant, string(payload), instant); err != nil {
			t.Fatalf("insert scan event: %v", err)
		}
	}
}
