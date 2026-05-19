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

// InitConfig controls where WorkGraph stores local state.
type InitConfig struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
	Force        bool
}

// InitResult describes the local paths initialized by WorkGraph.
type InitResult struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
	ConfigPath   string
	Message      string
}

type configFile struct {
	WatchDirs             []string `json:"watch_dirs"`
	ConservativeWatchDirs []string `json:"conservative_watch_dirs,omitempty"`
	IgnorePaths           []string `json:"ignore_paths"`
	IgnoreNames           []string `json:"ignore_names"`
}

// Init creates the local WorkGraph home, config, SQLite database, and memory repo.
func Init(config InitConfig) (InitResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return InitResult{}, err
	}

	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return InitResult{}, fmt.Errorf("create WorkGraph home: %w", err)
	}

	configPath := filepath.Join(homeDir, "config.json")
	if err := createDefaultConfig(configPath, homeDir, config.Force); err != nil {
		return InitResult{}, fmt.Errorf("create config: %w", err)
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

	result := InitResult{
		HomeDir:      homeDir,
		DatabasePath: dbPath,
		MemoryDir:    memoryDir,
		ConfigPath:   configPath,
	}
	result.Message = initMessage(result)

	return result, nil
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

func createDefaultConfig(configPath string, homeDir string, force bool) error {
	workgraphHome, err := filepath.Abs(homeDir)
	if err != nil {
		return fmt.Errorf("resolve WorkGraph home: %w", err)
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

	config := configFile{
		WatchDirs:             watchDirs,
		ConservativeWatchDirs: append([]string(nil), watchDirs...),
		IgnorePaths:           []string{workgraphHome},
		IgnoreNames:           defaultIgnoreNames(),
	}

	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode default config: %w", err)
	}
	contents = append(contents, '\n')

	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}

	file, err := os.OpenFile(configPath, flags, 0o644)
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
	return []string{".git", "node_modules", "DerivedData", ".noindex"}
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
		"WorkGraph initialized",
		"Home: " + result.HomeDir,
		"Database: " + result.DatabasePath,
		"Memory: " + result.MemoryDir,
		"Config: " + result.ConfigPath,
	}
	if runtime.GOOS == "darwin" {
		lines = append(lines,
			"",
			"macOS note: WorkGraph watches common folders such as Desktop, Documents, and Downloads by default. macOS may prompt for access to protected folders.",
			"To avoid repeated prompts, grant Full Disk Access once to your terminal app or installed WorkGraph binary in System Settings > Privacy & Security > Full Disk Access.",
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
	}

	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}

	return nil
}
