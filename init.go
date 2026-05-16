package workgraph

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// InitConfig controls where WorkGraph stores local state.
type InitConfig struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
}

// InitResult describes the local paths initialized by WorkGraph.
type InitResult struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
	Message      string
}

// Init creates the local WorkGraph home and SQLite database.
func Init(config InitConfig) (InitResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return InitResult{}, err
	}

	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return InitResult{}, fmt.Errorf("create WorkGraph home: %w", err)
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

func initMessage(result InitResult) string {
	lines := []string{
		"WorkGraph initialized",
		"Home: " + result.HomeDir,
		"Database: " + result.DatabasePath,
		"Memory: " + result.MemoryDir,
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
