package facts

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	workgraph "github.com/jystringfellow/workgraph"
)

// First implementation slice: replace the home and database placeholders with
// executable assertions, verify they fail, then implement only enough init
// behavior to pass them before expanding the slice.

func TestInitCreatesWorkGraphHome(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")

	_, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	info, err := os.Stat(homeDir)
	if err != nil {
		t.Fatalf("expected WorkGraph home to exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected WorkGraph home to be a directory")
	}
}

func TestInitCreatesSQLiteDatabase(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")

	_, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	dbPath := filepath.Join(homeDir, "workgraph.db")
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("expected SQLite database to exist: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("expected SQLite database path to be a file")
	}

	header := make([]byte, len("SQLite format 3\x00"))
	dbFile, err := os.Open(dbPath)
	if err != nil {
		t.Fatalf("expected SQLite database to be readable: %v", err)
	}
	defer dbFile.Close()

	if _, err := dbFile.Read(header); err != nil {
		t.Fatalf("expected SQLite database header to be readable: %v", err)
	}
	if !bytes.Equal(header, []byte("SQLite format 3\x00")) {
		t.Fatalf("expected SQLite database header, got %q", header)
	}
}

func TestInitCreatesMemoryRepo(t *testing.T) {
	tempDir := t.TempDir()
	memoryDir := filepath.Join(tempDir, "workgraph-memory")

	_, err := workgraph.Init(workgraph.InitConfig{
		HomeDir:   filepath.Join(tempDir, ".workgraph"),
		MemoryDir: memoryDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	info, err := os.Stat(memoryDir)
	if err != nil {
		t.Fatalf("expected memory repo to exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected memory repo to be a directory")
	}
}

func TestInitCreatesDefaultConfig(t *testing.T) {
	tempDir := t.TempDir()
	userHome := fakeUserHomeWithDirs(t, "Desktop", "Documents", "Downloads", "Code")
	homeDir := filepath.Join(tempDir, ".workgraph")

	_, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	configPath := filepath.Join(homeDir, "config.json")
	config := readInitConfig(t, configPath)

	workgraphHome, err := filepath.Abs(homeDir)
	if err != nil {
		t.Fatalf("resolve WorkGraph home: %v", err)
	}
	expectedWatchDirs := []string{
		filepath.Join(userHome, "Desktop"),
		filepath.Join(userHome, "Documents"),
		filepath.Join(userHome, "Downloads"),
		filepath.Join(userHome, "Code"),
	}

	if !reflect.DeepEqual(config.WatchDirs, expectedWatchDirs) {
		t.Fatalf("expected default watch dirs %q, got %q", expectedWatchDirs, config.WatchDirs)
	}
	if !reflect.DeepEqual(config.IgnorePaths, []string{workgraphHome}) {
		t.Fatalf("expected default ignore paths %q, got %q", []string{workgraphHome}, config.IgnorePaths)
	}
	if !reflect.DeepEqual(config.IgnoreNames, []string{".git", "node_modules", "DerivedData", ".noindex"}) {
		t.Fatalf("expected default ignore names, got %q", config.IgnoreNames)
	}
}

func TestInitDefaultConfigWatchesCommonUserFolders(t *testing.T) {
	userHome := fakeUserHomeWithDirs(t, "Desktop", "Documents", "Downloads", "Projects")
	homeDir := filepath.Join(t.TempDir(), ".workgraph")

	_, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	config := readInitConfig(t, filepath.Join(homeDir, "config.json"))
	expectedWatchDirs := []string{
		filepath.Join(userHome, "Desktop"),
		filepath.Join(userHome, "Documents"),
		filepath.Join(userHome, "Downloads"),
		filepath.Join(userHome, "Projects"),
	}

	if !reflect.DeepEqual(config.WatchDirs, expectedWatchDirs) {
		t.Fatalf("expected default watch dirs %q, got %q", expectedWatchDirs, config.WatchDirs)
	}
	for _, watchDir := range config.WatchDirs {
		if !filepath.IsAbs(watchDir) {
			t.Fatalf("expected watch dir to be absolute, got %q", watchDir)
		}
		if strings.Contains(watchDir, "$HOME") || strings.Contains(watchDir, "~") {
			t.Fatalf("expected resolved watch dir, got %q", watchDir)
		}
	}
}

func TestInitDefaultConfigIgnoresWorkGraphHome(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")

	_, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	config := readInitConfig(t, filepath.Join(homeDir, "config.json"))
	workgraphHome, err := filepath.Abs(homeDir)
	if err != nil {
		t.Fatalf("resolve WorkGraph home: %v", err)
	}

	if !reflect.DeepEqual(config.IgnorePaths, []string{workgraphHome}) {
		t.Fatalf("expected default ignore paths %q, got %q", []string{workgraphHome}, config.IgnorePaths)
	}
	if !filepath.IsAbs(config.IgnorePaths[0]) {
		t.Fatalf("expected ignore path to be absolute, got %q", config.IgnorePaths[0])
	}
}

func TestInitIsIdempotent(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")

	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir:   homeDir,
		MemoryDir: memoryDir,
	})
	if err != nil {
		t.Fatalf("initial init failed: %v", err)
	}

	db, err := sql.Open("sqlite3", result.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"event-1",
		"file",
		"file.modified",
		"2026-05-16T12:00:00Z",
		`{"path":"README.md"}`,
		"2026-05-16T12:00:01Z",
	)
	if err != nil {
		t.Fatalf("insert existing event: %v", err)
	}

	memoryPath := filepath.Join(memoryDir, "note.md")
	if err := os.WriteFile(memoryPath, []byte("existing memory"), 0o644); err != nil {
		t.Fatalf("write existing memory file: %v", err)
	}

	_, err = workgraph.Init(workgraph.InitConfig{
		HomeDir:   homeDir,
		MemoryDir: memoryDir,
	})
	if err != nil {
		t.Fatalf("second init failed: %v", err)
	}

	var eventCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE id = ?`, "event-1").Scan(&eventCount); err != nil {
		t.Fatalf("query existing event: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("expected existing event to be preserved, got count %d", eventCount)
	}

	memory, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatalf("read existing memory file: %v", err)
	}
	if string(memory) != "existing memory" {
		t.Fatalf("expected existing memory file to be preserved, got %q", memory)
	}
}

func TestInitPreservesExistingConfig(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	configPath := filepath.Join(homeDir, "config.json")
	existing := initConfigFile{
		WatchDirs:   []string{filepath.Join(tempDir, "watched")},
		IgnorePaths: []string{filepath.Join(tempDir, "private")},
		IgnoreNames: []string{".git", "node_modules", "dist"},
	}

	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("create WorkGraph home: %v", err)
	}
	writeInitConfig(t, configPath, existing)

	_, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	config := readInitConfig(t, configPath)
	if !reflect.DeepEqual(config, existing) {
		t.Fatalf("expected existing config to be preserved, got %#v", config)
	}
}

func TestInitForceOverwritesExistingConfigWithDefaults(t *testing.T) {
	tempDir := t.TempDir()
	userHome := fakeUserHomeWithDirs(t, "Desktop", "Documents")
	homeDir := filepath.Join(tempDir, ".workgraph")
	configPath := filepath.Join(homeDir, "config.json")
	oldConfig := initConfigFile{
		WatchDirs:   []string{filepath.Join(tempDir, "old-watch")},
		IgnorePaths: []string{filepath.Join(tempDir, "old-ignore")},
		IgnoreNames: []string{".git", "node_modules"},
	}

	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("create WorkGraph home: %v", err)
	}
	writeInitConfig(t, configPath, oldConfig)

	_, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
		Force:   true,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	config := readInitConfig(t, configPath)
	workgraphHome, err := filepath.Abs(homeDir)
	if err != nil {
		t.Fatalf("resolve WorkGraph home: %v", err)
	}

	expected := initConfigFile{
		WatchDirs:   []string{filepath.Join(userHome, "Desktop"), filepath.Join(userHome, "Documents")},
		IgnorePaths: []string{workgraphHome},
		IgnoreNames: []string{".git", "node_modules", "DerivedData", ".noindex"},
	}
	if !reflect.DeepEqual(config, expected) {
		t.Fatalf("expected force init to refresh config to %#v, got %#v", expected, config)
	}
}

func TestInitReportsInitializedPaths(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")

	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir:   homeDir,
		MemoryDir: memoryDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	if result.HomeDir != homeDir {
		t.Fatalf("expected home path %q, got %q", homeDir, result.HomeDir)
	}
	if result.DatabasePath != filepath.Join(homeDir, "workgraph.db") {
		t.Fatalf("expected database path under home, got %q", result.DatabasePath)
	}
	if result.MemoryDir != memoryDir {
		t.Fatalf("expected memory path %q, got %q", memoryDir, result.MemoryDir)
	}
	if !strings.Contains(result.Message, result.HomeDir) {
		t.Fatalf("expected result message to include home path, got %q", result.Message)
	}
	if !strings.Contains(result.Message, result.DatabasePath) {
		t.Fatalf("expected result message to include database path, got %q", result.Message)
	}
	if !strings.Contains(result.Message, result.MemoryDir) {
		t.Fatalf("expected result message to include memory path, got %q", result.Message)
	}
}

func TestInitReportsConfigPath(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	configPath := filepath.Join(homeDir, "config.json")

	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	if result.ConfigPath != configPath {
		t.Fatalf("expected config path %q, got %q", configPath, result.ConfigPath)
	}
	if !strings.Contains(result.Message, configPath) {
		t.Fatalf("expected result message to include config path %q, got %q", configPath, result.Message)
	}
	if !strings.Contains(result.Message, "Config: "+configPath) {
		t.Fatalf("expected result message to label config path, got %q", result.Message)
	}
}

func TestInitOnMacOSSuggestsFullDiskAccess(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific privacy guidance")
	}

	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: filepath.Join(t.TempDir(), ".workgraph"),
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	for _, expected := range []string{"macOS", "Full Disk Access", "Documents", "Desktop", "Downloads"} {
		if !strings.Contains(result.Message, expected) {
			t.Fatalf("expected init message to include %q, got %q", expected, result.Message)
		}
	}
}

type initConfigFile struct {
	WatchDirs   []string `json:"watch_dirs"`
	IgnorePaths []string `json:"ignore_paths"`
	IgnoreNames []string `json:"ignore_names"`
}

func readInitConfig(t *testing.T, path string) initConfigFile {
	t.Helper()

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var config initConfigFile
	if err := json.Unmarshal(contents, &config); err != nil {
		t.Fatalf("parse config: %v", err)
	}

	return config
}

func writeInitConfig(t *testing.T, path string, config initConfigFile) {
	t.Helper()

	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("encode config: %v", err)
	}

	if err := os.WriteFile(path, append(contents, '\n'), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func fakeUserHomeWithDirs(t *testing.T, names ...string) string {
	t.Helper()

	originalHome, _ := os.UserHomeDir()
	userHome := filepath.Join(t.TempDir(), "user-home")
	if err := os.MkdirAll(userHome, 0o755); err != nil {
		t.Fatalf("create fake user home: %v", err)
	}
	for _, name := range names {
		if err := os.MkdirAll(filepath.Join(userHome, name), 0o755); err != nil {
			t.Fatalf("create fake user home dir %q: %v", name, err)
		}
	}
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	if os.Getenv("GOMODCACHE") == "" && originalHome != "" {
		t.Setenv("GOMODCACHE", filepath.Join(originalHome, "go", "pkg", "mod"))
	}

	resolved, err := filepath.Abs(userHome)
	if err != nil {
		t.Fatalf("resolve fake user home: %v", err)
	}
	return resolved
}
