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

const (
	defaultResumeActivityLimit = 10
	defaultResumeFreshness     = 180 * 24 * time.Hour
)

// ResumeConfig controls the resumable work view.
type ResumeConfig struct {
	HomeDir        string
	DatabasePath   string
	MemoryDir      string
	Project        string
	Now            time.Time
	MaxEvents      int
	AllProjects    bool
	GitEmails      []string
	GitHubLogins   []string
	DebugRelevance bool
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

	project := strings.TrimSpace(config.Project)
	result := ResumeResult{Project: project}
	if project == "" {
		if config.AllProjects {
			result.Projects = resumableProjects(events)
		} else {
			result.Projects = resumableRelevantProjects(events, config.GitEmails, config.GitHubLogins, now)
		}
		if config.DebugRelevance {
			result.Message = resumeRelevanceMessage(resumeRelevanceDecisions(events, config.GitEmails, config.GitHubLogins, now), location)
			return result, nil
		}
		result.Message = resumeProjectsMessage(result.Projects, location)
		return result, nil
	}
	project = canonicalResumeProjectName(project, events)
	result.Project = project

	projectEvents := resumeProjectEvents(events, project)
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

func canonicalResumeProjectName(project string, events []ResumeEvent) string {
	project = strings.TrimSpace(project)
	for _, event := range events {
		if event.Project == project {
			return project
		}
		if !strings.HasPrefix(event.Type, "slack.") {
			continue
		}
		channelID, _ := slackResumeChannelIdentity(event.Payload)
		if channelID == project && event.Project != "" {
			return event.Project
		}
	}
	return project
}

func resumeDatabasePath(config ResumeConfig) (string, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return "", err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return "", fmt.Errorf("resolve workgraph home: %w", err)
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
	canonicalizeSlackResumeProjects(events)

	sort.Slice(events, func(i, j int) bool {
		if events[i].Timestamp.Equal(events[j].Timestamp) {
			return events[i].ID < events[j].ID
		}
		return events[i].Timestamp.After(events[j].Timestamp)
	})
	return events, nil
}

func canonicalizeSlackResumeProjects(events []ResumeEvent) {
	aliases := slackResumeProjectAliases(events)
	if len(aliases) == 0 {
		return
	}
	for i := range events {
		if resolved := slackResumeCanonicalProject(events[i], aliases); resolved != "" {
			events[i].Project = resolved
		}
	}
}

func slackResumeProjectAliases(events []ResumeEvent) map[string]string {
	aliases := map[string]string{}
	for _, event := range events {
		if !strings.HasPrefix(event.Type, "slack.") {
			continue
		}
		channelID, channelName := slackResumeChannelIdentity(event.Payload)
		if channelID == "" || channelName == "" || channelName == channelID {
			continue
		}
		aliases[channelID] = channelName
	}
	return aliases
}

func slackResumeCanonicalProject(event ResumeEvent, aliases map[string]string) string {
	if !strings.HasPrefix(event.Type, "slack.") {
		return ""
	}
	channelID, channelName := slackResumeChannelIdentity(event.Payload)
	if channelName != "" && channelName != channelID {
		return channelName
	}
	if channelID != "" {
		return aliases[channelID]
	}
	return ""
}

func slackResumeChannelIdentity(payload string) (string, string) {
	var parsed struct {
		ChannelID   string `json:"channel_id"`
		ChannelName string `json:"channel_name"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return "", ""
	}
	return strings.TrimSpace(parsed.ChannelID), strings.TrimSpace(parsed.ChannelName)
}

func unresolvedRawSlackProject(event ResumeEvent) bool {
	channelID, channelName := slackResumeChannelIdentity(event.Payload)
	if channelID == "" {
		return false
	}
	if channelName != "" && channelName != channelID {
		return false
	}
	return strings.TrimSpace(event.Project) == channelID
}

func resumableProjects(events []ResumeEvent) []ResumeProject {
	return resumableProjectsFromEvents(events)
}

func resumableRelevantProjects(events []ResumeEvent, gitEmails []string, githubLogins []string, now time.Time) []ResumeProject {
	var relevant []ResumeEvent
	for _, event := range events {
		if shown, _ := resumeEventRelevance(event, gitEmails, githubLogins, now); shown {
			relevant = append(relevant, event)
		}
	}
	return resumableProjectsFromEvents(relevant)
}

type resumeRelevanceDecision struct {
	Project    string
	Shown      bool
	EventCount int
	LastActive time.Time
	Reason     string
}

func resumeRelevanceDecisions(events []ResumeEvent, gitEmails []string, githubLogins []string, now time.Time) []resumeRelevanceDecision {
	decisionsByProject := map[string]resumeRelevanceDecision{}
	for _, event := range events {
		if event.Project == "" {
			continue
		}
		shown, reason := resumeEventRelevance(event, gitEmails, githubLogins, now)
		decision := decisionsByProject[event.Project]
		if decision.Project == "" {
			decision.Project = event.Project
			decision.LastActive = event.Timestamp
			decision.Reason = reason
		}
		decision.EventCount++
		if event.Timestamp.After(decision.LastActive) {
			decision.LastActive = event.Timestamp
		}
		if shown && !decision.Shown {
			decision.Shown = true
			decision.Reason = reason
		}
		decisionsByProject[event.Project] = decision
	}
	decisions := make([]resumeRelevanceDecision, 0, len(decisionsByProject))
	for _, decision := range decisionsByProject {
		decisions = append(decisions, decision)
	}
	sort.Slice(decisions, func(i, j int) bool {
		if decisions[i].LastActive.Equal(decisions[j].LastActive) {
			return decisions[i].Project < decisions[j].Project
		}
		return decisions[i].LastActive.After(decisions[j].LastActive)
	})
	return decisions
}

func resumableProjectsFromEvents(events []ResumeEvent) []ResumeProject {
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

func resumeEventIsStale(event ResumeEvent, now time.Time) bool {
	if now.IsZero() {
		return false
	}
	return event.Timestamp.Before(now.Add(-defaultResumeFreshness))
}

func resumeEventHasUserWorkEvidence(event ResumeEvent, gitEmails []string, githubLogins []string) bool {
	shown, _ := resumeEventRelevance(event, gitEmails, githubLogins, time.Time{})
	return shown
}

func resumeEventRelevance(event ResumeEvent, gitEmails []string, githubLogins []string, now time.Time) (bool, string) {
	if event.Project == "" {
		return false, "missing project"
	}
	if resumeEventIsStale(event, now) {
		return false, "stale evidence"
	}
	switch event.Type {
	case "git.commit":
		authorEmail := gitCommitAuthorEmail(event.Payload)
		if authorEmail == "" {
			if len(normalizedEmailSet(gitEmails)) == 0 {
				return true, "git commit with unknown local identity"
			}
			return false, "git commit missing author email"
		}
		emails := normalizedEmailSet(gitEmails)
		if len(emails) == 0 || emails[strings.ToLower(strings.TrimSpace(authorEmail))] {
			return true, "git commit authored by local identity"
		}
		return false, "git commit authored by another identity"
	case "github.pull_request", "github.issue":
		actor := githubEventActor(event.Payload)
		logins := normalizedStringSet(githubLogins)
		if len(logins) == 0 || logins[strings.ToLower(strings.TrimSpace(actor))] {
			return true, "GitHub actor matches local identity"
		}
		return false, "GitHub actor does not match local identity"
	default:
		if strings.HasPrefix(event.Type, "slack.") {
			if unresolvedRawSlackProject(event) {
				return false, "raw Slack id without resolved name"
			}
			return true, "slack conversation"
		}
		if broadResumeProjectName(event.Project) {
			return false, "broad folder file churn"
		}
		if personalResumeProjectName(event.Project) {
			return false, "home folder file churn"
		}
		if event.Path != "" {
			return true, "file activity"
		}
		return false, "no user-work evidence"
	}
}

func gitCommitAuthorEmail(payload string) string {
	var parsed struct {
		AuthorEmail string `json:"author_email"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.AuthorEmail)
}

func normalizedEmailSet(emails []string) map[string]bool {
	return normalizedStringSet(emails)
}

func normalizedStringSet(values []string) map[string]bool {
	set := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			set[value] = true
		}
	}
	return set
}

func githubEventActor(payload string) string {
	var parsed struct {
		Actor string `json:"actor"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Actor)
}

func broadResumeProjectName(project string) bool {
	switch strings.ToLower(strings.TrimSpace(project)) {
	case "desktop", "documents", "downloads", "projects", "code", "developer", "work", "repos", "source":
		return true
	default:
		return false
	}
}

func personalResumeProjectName(project string) bool {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(project), filepath.Base(homeDir))
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
		lines = append(lines, "No resumable projects found.", "Run workgraph start to capture activity.")
		return strings.Join(lines, "\n")
	}

	for _, project := range projects {
		lines = append(lines, fmt.Sprintf("- %s: %s, last active %s", project.Name, pluralize(project.EventCount, "event"), project.LastActive.In(location).Format("2006-01-02 15:04")))
	}
	lines = append(lines, "", "Run: workgraph resume <project>")
	return strings.Join(lines, "\n")
}

func resumeRelevanceMessage(decisions []resumeRelevanceDecision, location *time.Location) string {
	lines := []string{"Resume relevance"}
	if len(decisions) == 0 {
		lines = append(lines, "No projects with captured events found.")
		return strings.Join(lines, "\n")
	}
	for _, decision := range decisions {
		state := "hidden"
		if decision.Shown {
			state = "shown"
		}
		lines = append(lines, fmt.Sprintf("- %s %s: %s (%s, last active %s)", state, decision.Project, decision.Reason, pluralize(decision.EventCount, "event"), decision.LastActive.In(location).Format("2006-01-02 15:04")))
	}
	return strings.Join(lines, "\n")
}

func resumeProjectMessage(result ResumeResult, location *time.Location) string {
	lines := []string{"Resume " + result.Project}
	if len(result.Events) == 0 {
		lines = append(lines,
			fmt.Sprintf("No recent activity found for %s.", result.Project),
			"Check the project name or run workgraph start to capture activity.",
		)
		if result.Memory != nil {
			lines = append(lines, "", "Project memory", result.Memory.Content)
		}
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
