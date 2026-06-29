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

const todaySessionGap = 30 * time.Minute

// TodayConfig controls the local-day activity view.
type TodayConfig struct {
	HomeDir      string
	DatabasePath string
	Now          time.Time
}

// TodayResult describes today's activity in deterministic plain text.
type TodayResult struct {
	Date     string
	Events   []TodayEvent
	Sessions []TodaySession
	Message  string
}

// TodayEvent is one stored event included in the local-day activity view.
type TodayEvent struct {
	ID        string
	Type      string
	Timestamp time.Time
	Project   string
	Path      string
	Summary   string
	Payload   string
}

// TodaySession is a time-based grouping inferred from today's events.
type TodaySession struct {
	StartedAt time.Time
	EndedAt   time.Time
	Project   string
	Events    []TodayEvent
}

type storedTodayEvent struct {
	ID          string
	Type        string
	Timestamp   string
	Project     sql.NullString
	Summary     sql.NullString
	PayloadJSON string
}

// Today returns captured work from the current local day.
func Today(config TodayConfig) (TodayResult, error) {
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	location := now.Location()

	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return TodayResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return TodayResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}

	dbPath := config.DatabasePath
	if dbPath == "" {
		dbPath = filepath.Join(homeDir, "workgraph.db")
	}
	dbPath, err = filepath.Abs(dbPath)
	if err != nil {
		return TodayResult{}, fmt.Errorf("resolve database path: %w", err)
	}

	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return TodayResult{}, fmt.Errorf("%w: run workgraph init", ErrNotInitialized)
		}
		return TodayResult{}, fmt.Errorf("check database: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return TodayResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return TodayResult{}, fmt.Errorf("open database: %w", err)
	}

	events, err := loadTodayEvents(db, now)
	if err != nil {
		return TodayResult{}, err
	}

	result := TodayResult{
		Date:   now.In(location).Format("2006-01-02"),
		Events: events,
	}
	result.Sessions = groupTodaySessions(events)
	result.Message = todayMessage(result, location)

	return result, nil
}

func loadTodayEvents(db *sql.DB, now time.Time) ([]TodayEvent, error) {
	location := now.Location()
	dayStart := time.Date(now.In(location).Year(), now.In(location).Month(), now.In(location).Day(), 0, 0, 0, 0, location)
	dayEnd := dayStart.AddDate(0, 0, 1)

	rows, err := db.Query(
		`SELECT id, type, timestamp, project, summary, payload_json FROM events WHERE timestamp >= ? AND timestamp < ? ORDER BY timestamp ASC, id ASC`,
		dayStart.UTC().Format(time.RFC3339),
		dayEnd.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var events []TodayEvent
	for rows.Next() {
		var stored storedTodayEvent
		if err := rows.Scan(&stored.ID, &stored.Type, &stored.Timestamp, &stored.Project, &stored.Summary, &stored.PayloadJSON); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}

		timestamp, err := time.Parse(time.RFC3339Nano, stored.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("parse event timestamp %q: %w", stored.ID, err)
		}

		event := TodayEvent{
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

	return events, nil
}

func eventPath(payloadJSON string) string {
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return ""
	}
	return payload.Path
}

func groupTodaySessions(events []TodayEvent) []TodaySession {
	var sessions []TodaySession
	for _, event := range events {
		if len(sessions) == 0 {
			sessions = append(sessions, newTodaySession(event))
			continue
		}

		last := &sessions[len(sessions)-1]
		if last.Project == event.Project && event.Timestamp.Sub(last.EndedAt) <= todaySessionGap {
			last.EndedAt = event.Timestamp
			last.Events = append(last.Events, event)
			continue
		}

		sessions = append(sessions, newTodaySession(event))
	}
	return sessions
}

func newTodaySession(event TodayEvent) TodaySession {
	return TodaySession{
		StartedAt: event.Timestamp,
		EndedAt:   event.Timestamp,
		Project:   event.Project,
		Events:    []TodayEvent{event},
	}
}

func todayMessage(result TodayResult, location *time.Location) string {
	lines := []string{
		"Today",
		fmt.Sprintf("%s: %s", result.Date, pluralize(len(result.Events), "event")),
	}

	if len(result.Events) == 0 {
		lines = append(lines, "No activity has been captured today.")
		return strings.Join(lines, "\n")
	}

	projects := todayProjectCounts(result.Events)
	if len(projects) > 0 {
		lines = append(lines, "", "Projects")
		for _, project := range projects {
			lines = append(lines, fmt.Sprintf("- %s: %s", project.Name, pluralize(project.Count, "event")))
		}
	}

	lines = append(lines, "", "Sessions")
	for _, session := range result.Sessions {
		lines = append(lines, fmt.Sprintf("- %s %s (%s)", sessionRange(session, location), projectLabel(session.Project), pluralize(len(session.Events), "event")))
		for _, event := range session.Events {
			lines = append(lines, fmt.Sprintf("  - %s %s %s", event.Timestamp.In(location).Format("15:04"), event.Type, eventLabel(event)))
		}
	}

	return strings.Join(lines, "\n")
}

type todayProjectCount struct {
	Name  string
	Count int
}

func todayProjectCounts(events []TodayEvent) []todayProjectCount {
	counts := map[string]int{}
	for _, event := range events {
		if event.Project == "" {
			continue
		}
		counts[event.Project]++
	}

	projects := make([]todayProjectCount, 0, len(counts))
	for project, count := range counts {
		projects = append(projects, todayProjectCount{Name: project, Count: count})
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].Count == projects[j].Count {
			return projects[i].Name < projects[j].Name
		}
		return projects[i].Count > projects[j].Count
	})

	return projects
}

func sessionRange(session TodaySession, location *time.Location) string {
	start := session.StartedAt.In(location).Format("15:04")
	end := session.EndedAt.In(location).Format("15:04")
	if start == end {
		return start
	}
	return start + "-" + end
}

func projectLabel(project string) string {
	if project == "" {
		return "unknown project"
	}
	return project
}

func eventLabel(event TodayEvent) string {
	if event.Type == "git.commit" {
		return gitCommitEventLabel(event)
	}
	if event.Type == "github.pull_request" || event.Type == "github.issue" {
		return githubEventLabel(event)
	}
	if event.Summary != "" {
		return event.Summary
	}
	if event.Path != "" {
		return event.Path
	}
	return event.ID
}

func gitCommitEventLabel(event TodayEvent) string {
	var payload struct {
		Commit  string `json:"commit"`
		Branch  string `json:"branch"`
		Subject string `json:"subject"`
	}
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		return eventLabelWithoutGitDecoration(event)
	}

	subject := payload.Subject
	if subject == "" {
		subject = event.Summary
	}
	if subject == "" {
		subject = event.ID
	}

	shortCommit := payload.Commit
	if len(shortCommit) > 7 {
		shortCommit = shortCommit[:7]
	}
	if payload.Branch == "" && shortCommit == "" {
		return subject
	}
	if payload.Branch == "" {
		return fmt.Sprintf("%s (%s)", subject, shortCommit)
	}
	if shortCommit == "" {
		return fmt.Sprintf("%s (%s)", subject, payload.Branch)
	}
	return fmt.Sprintf("%s (%s %s)", subject, payload.Branch, shortCommit)
}

func githubEventLabel(event TodayEvent) string {
	var payload struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		return eventLabelWithoutGitDecoration(event)
	}

	title := payload.Title
	if title == "" {
		title = event.Summary
	}
	if title == "" {
		title = event.ID
	}

	if payload.Number == 0 && payload.State == "" {
		return title
	}
	if payload.Number == 0 {
		return fmt.Sprintf("%s (%s)", title, payload.State)
	}
	if payload.State == "" {
		return fmt.Sprintf("%s (#%d)", title, payload.Number)
	}
	return fmt.Sprintf("%s (#%d %s)", title, payload.Number, payload.State)
}

func eventLabelWithoutGitDecoration(event TodayEvent) string {
	if event.Summary != "" {
		return event.Summary
	}
	if event.Path != "" {
		return event.Path
	}
	return event.ID
}

func pluralize(count int, singular string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, singular)
	}
	return fmt.Sprintf("%d %ss", count, singular)
}
