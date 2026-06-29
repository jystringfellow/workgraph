package workgraph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// InitConfig controls where workgraph stores local state.
type InitConfig struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
	Force        bool
}

// InitResult describes the local paths initialized by workgraph.
type InitResult struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
	SettingsPath string
	Message      string
}

type settingsFile struct {
	WatchDirs             []string `json:"watch_dirs"`
	ConservativeWatchDirs []string `json:"conservative_watch_dirs,omitempty"`
	IgnorePaths           []string `json:"ignore_paths"`
	IgnoreNames           []string `json:"ignore_names"`
}

// Init creates the local workgraph home, settings, SQLite database, and memory repo.
func Init(config InitConfig) (InitResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return InitResult{}, err
	}

	if err := ensureWorkgraphHomeDir(homeDir); err != nil {
		return InitResult{}, fmt.Errorf("create workgraph home: %w", err)
	}

	settingsPath := filepath.Join(homeDir, "settings.json")
	if err := createDefaultSettings(settingsPath, homeDir, config.Force); err != nil {
		return InitResult{}, fmt.Errorf("create settings: %w", err)
	}

	dbPath := config.DatabasePath
	if dbPath == "" {
		dbPath = filepath.Join(homeDir, "workgraph.db")
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return InitResult{}, fmt.Errorf("create database directory: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return InitResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return InitResult{}, fmt.Errorf("initialize database: %w", err)
	}

	if err := createSchema(db); err != nil {
		return InitResult{}, fmt.Errorf("create database schema: %w", err)
	}

	memoryDir, err := resolveMemoryDir(config.MemoryDir)
	if err != nil {
		return InitResult{}, err
	}
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		return InitResult{}, fmt.Errorf("create memory repo: %w", err)
	}
	if err := os.MkdirAll(projectMemoryDir(memoryDir), 0o755); err != nil {
		return InitResult{}, fmt.Errorf("create project memory directory: %w", err)
	}

	result := InitResult{
		HomeDir:      homeDir,
		DatabasePath: dbPath,
		MemoryDir:    memoryDir,
		SettingsPath: settingsPath,
	}
	result.Message = initMessage(result)

	return result, nil
}

func ensureWorkgraphHomeDir(homeDir string) error {
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		return err
	}
	return os.Chmod(homeDir, 0o700)
}

func resolveHomeDir(homeDir string) (string, error) {
	if homeDir != "" {
		return homeDir, nil
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find user home: %w", err)
	}

	return filepath.Join(userHome, ".workgraph"), nil
}

func resolveMemoryDir(memoryDir string) (string, error) {
	if memoryDir != "" {
		return memoryDir, nil
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find user home: %w", err)
	}

	return filepath.Join(userHome, "workgraph-memory"), nil
}

func createDefaultSettings(settingsPath string, homeDir string, force bool) error {
	workgraphHome, err := filepath.Abs(homeDir)
	if err != nil {
		return fmt.Errorf("resolve workgraph home: %w", err)
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("find user home: %w", err)
	}
	userHome, err = filepath.Abs(userHome)
	if err != nil {
		return fmt.Errorf("resolve user home: %w", err)
	}

	watchDirs, err := defaultWatchDirs(userHome)
	if err != nil {
		return err
	}

	config := settingsFile{
		WatchDirs:             watchDirs,
		ConservativeWatchDirs: append([]string(nil), watchDirs...),
		IgnorePaths:           []string{workgraphHome},
		IgnoreNames:           defaultIgnoreNames(),
	}

	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode default settings: %w", err)
	}
	contents = append(contents, '\n')

	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}

	file, err := os.OpenFile(settingsPath, flags, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	if _, err := file.Write(contents); err != nil {
		return err
	}

	return nil
}

func defaultIgnoreNames() []string {
	return []string{".git", "node_modules", "DerivedData", ".noindex", "xcuserdata", "bin", "obj", "dist", "build", "target", ".build", ".gradle"}
}

func defaultWatchDirs(userHome string) ([]string, error) {
	candidates := []string{
		"Desktop",
		"Documents",
		"Downloads",
		"Code",
		"Projects",
		"Developer",
		"Work",
		"source",
		"repos",
	}

	watchDirs := []string{}
	for _, name := range candidates {
		path := filepath.Join(userHome, name)
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("check default watch directory %q: %w", path, err)
		}
		if !info.IsDir() {
			continue
		}
		path, err = filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve default watch directory: %w", err)
		}
		watchDirs = append(watchDirs, path)
	}

	if len(watchDirs) == 0 {
		return []string{userHome}, nil
	}
	return watchDirs, nil
}

func initMessage(result InitResult) string {
	lines := []string{
		"workgraph initialized",
		"Home: " + result.HomeDir,
		"Database: " + result.DatabasePath,
		"Memory: " + result.MemoryDir,
		"Project memory: " + projectMemoryDir(result.MemoryDir),
		"Settings: " + result.SettingsPath,
	}
	if runtime.GOOS == "darwin" {
		lines = append(lines,
			"",
			"macOS note: workgraph watches common folders such as Desktop, Documents, and Downloads by default. macOS may prompt for access to protected folders.",
			"To avoid repeated prompts, grant Full Disk Access once to your terminal app or installed workgraph binary in System Settings > Privacy & Security > Full Disk Access.",
		)
	}

	return strings.Join(lines, "\n")
}

func createSchema(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS events (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			type TEXT NOT NULL,
			timestamp TEXT NOT NULL,
			payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
			project TEXT,
			actor TEXT,
			summary TEXT,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			started_at TEXT NOT NULL,
			ended_at TEXT NOT NULL,
			project TEXT,
			summary TEXT,
			CHECK (started_at <= ended_at)
		);`,
		`CREATE TABLE IF NOT EXISTS memory_docs (
			id TEXT PRIMARY KEY,
			path TEXT NOT NULL UNIQUE,
			kind TEXT NOT NULL,
			content TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS memory_links (
			id TEXT PRIMARY KEY,
			memory_doc_path TEXT NOT NULL,
			event_id TEXT NOT NULL,
			relation TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE (memory_doc_path, event_id, relation)
		);`,
		`CREATE TABLE IF NOT EXISTS notion_index (
			notion_id TEXT PRIMARY KEY,
			object_type TEXT NOT NULL,
			title TEXT,
			url TEXT,
			parent_json TEXT CHECK (parent_json IS NULL OR json_valid(parent_json)),
			properties_json TEXT CHECK (properties_json IS NULL OR json_valid(properties_json)),
			content_preview TEXT,
			content_synced_at TEXT,
			created_time TEXT,
			created_by TEXT,
			last_edited_time TEXT,
			last_edited_by TEXT,
			source TEXT NOT NULL,
			first_seen_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			last_synced_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS suggestions (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			pattern_key TEXT,
			title TEXT NOT NULL,
			reason TEXT NOT NULL,
			confidence TEXT NOT NULL CHECK (confidence IN ('high', 'medium', 'low')),
			lane TEXT NOT NULL CHECK (lane IN ('baseline', 'semantic', 'manual')),
			status TEXT NOT NULL CHECK (status IN ('proposed', 'reviewed', 'approved', 'dismissed', 'snoozed', 'acted')),
			evidence_json TEXT NOT NULL CHECK (json_valid(evidence_json)),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			resolved_at TEXT,
			UNIQUE (type, pattern_key)
		);`,
		`CREATE TABLE IF NOT EXISTS suggestion_feedback (
			id TEXT PRIMARY KEY,
			suggestion_id TEXT NOT NULL,
			action TEXT NOT NULL CHECK (action IN ('reviewed', 'accepted', 'approved', 'dismissed', 'snoozed', 'completed', 'undone')),
			reason_code TEXT,
			note TEXT,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS suggestion_suppressions (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			pattern_key TEXT NOT NULL,
			reason TEXT,
			until_at TEXT,
			created_at TEXT NOT NULL,
			UNIQUE (type, pattern_key)
		);`,
	}

	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}

	if err := ensureColumn(db, "notion_index", "content_preview", "TEXT"); err != nil {
		return err
	}
	if err := ensureColumn(db, "notion_index", "content_synced_at", "TEXT"); err != nil {
		return err
	}

	indices := []struct{ name, table, column string }{
		{"idx_events_timestamp", "events", "timestamp"},
		{"idx_events_project", "events", "project"},
		{"idx_events_source", "events", "source"},
		{"idx_events_type", "events", "type"},
	}
	for _, idx := range indices {
		if err := ensureIndex(db, idx.name, idx.table, idx.column); err != nil {
			return err
		}
	}

	return nil
}

func ensureIndex(db *sql.DB, name, table, column string) error {
	_, err := db.Exec("CREATE INDEX IF NOT EXISTS " + name + " ON " + table + " (" + column + ")")
	return err
}

func ensureColumn(db *sql.DB, table string, column string, definition string) error {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + definition)
	return err
}
