package workgraph

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const memoryProjectSourcesSchema = `CREATE TABLE IF NOT EXISTS memory_project_sources (
 memory_doc_path TEXT NOT NULL,
 captured_project TEXT NOT NULL,
 PRIMARY KEY (memory_doc_path, captured_project)
)`

type MemoryProjectSourcesConfig struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
	Project      string
	Workstream   string
	Sources      []string
	Unlink       bool
}

func UpdateMemoryProjectSources(config MemoryProjectSourcesConfig) (string, error) {
	if len(config.Sources) == 0 {
		return "", fmt.Errorf("at least one --source is required")
	}
	key, target, err := memoryProjectSourceKey(config)
	if err != nil {
		return "", err
	}
	dbPath, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return "", err
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return "", err
	}
	defer db.Close()
	if !config.Unlink {
		if err := requireMemoryContainers(db, config.Sources); err != nil {
			return "", err
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(memoryProjectSourcesSchema); err != nil {
		return "", err
	}
	for _, source := range config.Sources {
		if strings.TrimSpace(source) == "" {
			return "", fmt.Errorf("container cannot be empty")
		}
		if config.Unlink {
			if _, err := tx.Exec(`DELETE FROM memory_project_sources WHERE memory_doc_path = ? AND captured_project = ?`, key, source); err != nil {
				return "", err
			}
			continue
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO memory_project_sources (memory_doc_path, captured_project) VALUES (?, ?)`, key, source); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	action := "Linked"
	if config.Unlink {
		action = "Unlinked"
	}
	return fmt.Sprintf("%s containers for %s: %s\nAuthored memory and captured events were preserved.", action, target, strings.Join(config.Sources, ", ")), nil
}

func requireMemoryContainers(db *sql.DB, sources []string) error {
	var resolved map[string]int
	for _, source := range sources {
		if strings.TrimSpace(source) == "" {
			return fmt.Errorf("container cannot be empty")
		}
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE project = ?)`, source).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		if resolved == nil {
			events, err := loadResumeEvents(db, time.Local)
			if err != nil {
				return err
			}
			resolved, _ = memoryContainerActivity(events)
		}
		if resolved[source] == 0 {
			return fmt.Errorf("container %q was not found; inspect workgraph today or resume --all", source)
		}
	}
	return nil
}

func memoryProjectSourceKey(config MemoryProjectSourcesConfig) (string, string, error) {
	if config.Workstream != "" {
		if config.Project != "" {
			return "", "", fmt.Errorf("a container maps to either a workstream or a project, not both")
		}
		if !validWorkstreamSlug(config.Workstream) {
			return "", "", fmt.Errorf("workstream %q is not a lowercase kebab-case slug", config.Workstream)
		}
		return workstreamSourcePrefix + config.Workstream, "workstream " + config.Workstream, nil
	}
	memoryDir, err := resolveMemoryDir(config.MemoryDir)
	if err != nil {
		return "", "", err
	}
	doc, path, err := loadProjectMemory(memoryDir, config.Project)
	if err != nil {
		return "", "", err
	}
	if doc == nil {
		return "", "", fmt.Errorf("project memory does not exist; run workgraph memory init %q first", config.Project)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	return path, config.Project, nil
}

func memoryProjectSources(db *sql.DB, memoryPath string) ([]string, error) {
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'memory_project_sources')`).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	path, err := filepath.Abs(memoryPath)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT captured_project FROM memory_project_sources WHERE memory_doc_path = ? ORDER BY captured_project`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []string
	for rows.Next() {
		var source string
		if err := rows.Scan(&source); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

func mappedMemoryEvents(db *sql.DB, events []ResumeEvent, project, memoryPath string) ([]ResumeEvent, error) {
	sources, err := memoryProjectSources(db, memoryPath)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{project: true}
	for _, source := range sources {
		allowed[source] = true
	}
	var result []ResumeEvent
	for _, event := range events {
		if allowed[event.Project] && !isTransientResumePath(event.Path) {
			result = append(result, event)
		}
	}
	return result, nil
}
