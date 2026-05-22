package workgraph

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const defaultResumeActivityLimit = 10

// ResumeConfig controls the resumable work view.
type ResumeConfig struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
	Project      string
	Now          time.Time
	MaxEvents    int
}

// ResumeResult describes resumable work in deterministic plain text.
type ResumeResult struct {
	Project    string
	Projects   []ResumeProject
	Events     []ResumeEvent
	Files      []string
	GitHub     []ResumeEvent
	Memory     *MemoryDoc
	MemoryPath string
	Omitted    int
	Message    string
}

// ResumeProject is a project with captured activity.
type ResumeProject struct {
	Name       string
	EventCount int
	LastActive time.Time
}

// ResumeEvent is one stored event included in a resume view.
type ResumeEvent struct {
	ID        string
	Type      string
	Timestamp time.Time
	Project   string
	Path      string
	Summary   string
	Payload   string
}

// Resume returns recent project context from captured events.
func Resume(config ResumeConfig) (ResumeResult, error) {
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	location := now.Location()

	dbPath, err := resumeDatabasePath(config)
	if err != nil {
		return ResumeResult{}, err
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return ResumeResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return ResumeResult{}, fmt.Errorf("open database: %w", err)
	}

	events, err := loadResumeEvents(db, location)
	if err != nil {
		return ResumeResult{}, err
	}

	result := ResumeResult{Project: config.Project}
	if config.Project == "" {
		result.Projects = resumableProjects(events)
		result.Message = resumeProjectsMessage(result.Projects, location)
		return result, nil
	}

	projectEvents := resumeProjectEvents(events, config.Project)
	result.GitHub = resumeOpenGitHubWork(projectEvents)
	result.Events, result.Omitted = limitResumeEvents(projectEvents, resumeActivityLimit(config.MaxEvents))
	result.Files = resumeRelevantFiles(result.Events)
	memoryDir, err := resolveMemoryDir(config.MemoryDir)
	if err != nil {
		return ResumeResult{}, err
	}
	result.Memory, result.MemoryPath, err = loadProjectMemory(memoryDir, config.Project)
	if err != nil {
		return ResumeResult{}, err
	}
	result.Message = resumeProjectMessage(result, location)
	return result, nil
}

func resumeDatabasePath(config ResumeConfig) (string, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return "", err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return "", fmt.Errorf("resolve WorkGraph home: %w", err)
	}

	dbPath := config.DatabasePath
	if dbPath == "" {
		dbPath = filepath.Join(homeDir, "workgraph.db")
	}
	dbPath, err = filepath.Abs(dbPath)
	if err != nil {
		return "", fmt.Errorf("resolve database path: %w", err)
	}

	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: run workgraph init", ErrNotInitialized)
		}
		return "", fmt.Errorf("check database: %w", err)
	}
	return dbPath, nil
}

func loadResumeEvents(db *sql.DB, location *time.Location) ([]ResumeEvent, error) {
	rows, err := db.Query(`SELECT id, type, timestamp, project, summary, payload_json FROM events`)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var events []ResumeEvent
	for rows.Next() {
		var stored storedTodayEvent
		if err := rows.Scan(&stored.ID, &stored.Type, &stored.Timestamp, &stored.Project, &stored.Summary, &stored.PayloadJSON); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}

		timestamp, err := time.Parse(time.RFC3339Nano, stored.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("parse event timestamp %q: %w", stored.ID, err)
		}

		event := ResumeEvent{
			ID:        stored.ID,
			Type:      stored.Type,
			Timestamp: timestamp.In(location),
			Path:      eventPath(stored.PayloadJSON),
			Payload:   stored.PayloadJSON,
		}
		if stored.Project.Valid {
			event.Project = stored.Project.String
		}
		if stored.Summary.Valid {
			event.Summary = stored.Summary.String
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}

	sort.Slice(events, func(i, j int) bool {
		if events[i].Timestamp.Equal(events[j].Timestamp) {
			return events[i].ID < events[j].ID
		}
		return events[i].Timestamp.After(events[j].Timestamp)
	})
	return events, nil
}

func resumableProjects(events []ResumeEvent) []ResumeProject {
	projectsByName := map[string]ResumeProject{}
	for _, event := range events {
		if event.Project == "" {
			continue
		}
		project := projectsByName[event.Project]
		if project.Name == "" {
			project.Name = event.Project
			project.LastActive = event.Timestamp
		}
		project.EventCount++
		if event.Timestamp.After(project.LastActive) {
			project.LastActive = event.Timestamp
		}
		projectsByName[event.Project] = project
	}

	projects := make([]ResumeProject, 0, len(projectsByName))
	for _, project := range projectsByName {
		projects = append(projects, project)
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].LastActive.Equal(projects[j].LastActive) {
			return projects[i].Name < projects[j].Name
		}
		return projects[i].LastActive.After(projects[j].LastActive)
	})
	return projects
}

func resumeProjectEvents(events []ResumeEvent, project string) []ResumeEvent {
	var filtered []ResumeEvent
	for _, event := range events {
		if event.Project != project {
			continue
		}
		if isTransientResumePath(event.Path) {
			continue
		}
		filtered = append(filtered, event)
	}
	return filtered
}

func isTransientResumePath(path string) bool {
	if path == "" {
		return false
	}
	if isTransientEditorPath(path) {
		return true
	}
	return strings.HasPrefix(filepath.Base(path), ".dat.nosync")
}

func resumeActivityLimit(configured int) int {
	if configured > 0 {
		return configured
	}
	return defaultResumeActivityLimit
}

func limitResumeEvents(events []ResumeEvent, limit int) ([]ResumeEvent, int) {
	if limit <= 0 || len(events) <= limit {
		return events, 0
	}
	return events[:limit], len(events) - limit
}

func resumeRelevantFiles(events []ResumeEvent) []string {
	seen := map[string]bool{}
	var files []string
	for _, event := range events {
		if event.Path == "" || seen[event.Path] {
			continue
		}
		seen[event.Path] = true
		files = append(files, event.Path)
	}
	return files
}

func resumeOpenGitHubWork(events []ResumeEvent) []ResumeEvent {
	var work []ResumeEvent
	for _, event := range events {
		if event.Type != "github.pull_request" && event.Type != "github.issue" {
			continue
		}

		var payload struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			continue
		}
		if payload.State != "open" {
			continue
		}
		work = append(work, event)
	}
	return work
}

func resumeProjectsMessage(projects []ResumeProject, location *time.Location) string {
	lines := []string{"Resumable projects"}
	if len(projects) == 0 {
		lines = append(lines, "No resumable projects found.", "Run workgraph run to capture activity.")
		return strings.Join(lines, "\n")
	}

	for _, project := range projects {
		lines = append(lines, fmt.Sprintf("- %s: %s, last active %s", project.Name, pluralize(project.EventCount, "event"), project.LastActive.In(location).Format("2006-01-02 15:04")))
	}
	lines = append(lines, "", "Run: workgraph resume <project>")
	return strings.Join(lines, "\n")
}

func resumeProjectMessage(result ResumeResult, location *time.Location) string {
	lines := []string{"Resume " + result.Project}
	if len(result.Events) == 0 {
		lines = append(lines,
			fmt.Sprintf("No recent activity found for %s.", result.Project),
			"Check the project name or run workgraph run to capture activity.",
		)
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "", "Recent activity")
	for _, event := range result.Events {
		lines = append(lines, fmt.Sprintf("- %s %s %s", event.Timestamp.In(location).Format("2006-01-02 15:04"), event.Type, resumeEventLabel(event)))
	}
	if result.Omitted > 0 {
		lines = append(lines, fmt.Sprintf("... and %s", pluralizeOlderEvents(result.Omitted)))
	}

	if len(result.Files) > 0 {
		lines = append(lines, "", "Relevant files")
		for _, file := range result.Files {
			lines = append(lines, "- "+file)
		}
	}

	if len(result.GitHub) > 0 {
		lines = append(lines, "", "Open GitHub work")
		for _, event := range result.GitHub {
			lines = append(lines, fmt.Sprintf("- %s %s", event.Type, resumeEventLabel(event)))
		}
	}

	if result.Memory != nil {
		lines = append(lines, "", "Project memory", result.Memory.Content)
	} else if result.MemoryPath != "" {
		lines = append(lines, "", "Add project memory: "+result.MemoryPath)
	}

	return strings.Join(lines, "\n")
}

func resumeEventLabel(event ResumeEvent) string {
	return eventLabel(TodayEvent{
		ID:        event.ID,
		Type:      event.Type,
		Timestamp: event.Timestamp,
		Project:   event.Project,
		Path:      event.Path,
		Summary:   event.Summary,
		Payload:   event.Payload,
	})
}

func pluralizeOlderEvents(count int) string {
	if count == 1 {
		return "1 older event"
	}
	return fmt.Sprintf("%d older events", count)
}
