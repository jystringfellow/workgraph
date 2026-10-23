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
	resumeHandoffLookback      = 7 * 24 * time.Hour
	resumeHandoffSessionGap    = 30 * time.Minute
	resumeHandoffEventLimit    = 3
	resumeSinceEventLimit      = 5
	resumeCheckInFormat        = "2006-01-02T15:04:05.000000000Z"
	resumeProjectListWindow    = 14 * 24 * time.Hour
	resumeProjectListLimit     = 10
)

const resumeCheckInSchema = `CREATE TABLE IF NOT EXISTS resume_checkins (
	scope TEXT PRIMARY KEY,
	checked_at TEXT NOT NULL
)`

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

type ResumeResult struct {
	NextItems    []NextItem
	Project      string
	Projects     []ResumeProject
	MoreProjects int
	LastWork     []ResumeEvent
	WorkOlder    bool
	AsOf         time.Time
	CheckIn      time.Time
	SinceLast    []ResumeEvent
	SinceCount   int
	Updates      []ResumeEvent
	UpdateCount  int
	Events       []ResumeEvent
	Files        []string
	GitHub       []ResumeEvent
	Memory       *MemoryDoc
	MemoryPath   string
	Workstream   string
	Members      []string
	EventGroups  map[string]string
	Omitted      int
	Message      string
}

type ResumeProject struct {
	Name       string
	EventCount int
	LastActive time.Time
}

type ResumeEvent struct {
	ID          string
	Source      string
	Type        string
	Timestamp   time.Time
	Project     string
	Path        string
	Summary     string
	Payload     string
	CapturedAt  time.Time
	Involvement []string
}

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
	result := ResumeResult{Project: project, AsOf: now}
	nextWarning := ""
	bridgeWarning := ""
	if homeDir, err := resolveHomeDir(config.HomeDir); err == nil {
		bridgeWarning = bridgeStallWarning(homeDir, dbPath, now)
	}
	if !config.AllProjects && !config.DebugRelevance {
		next, nextErr := Next(NextConfig{HomeDir: config.HomeDir, DatabasePath: dbPath, Project: project, Now: now})
		if nextErr != nil {
			nextWarning = "\n\n" + nextUnavailableMessage(nextErr)
		} else {
			result.NextItems = next.Items
		}
	}
	if project == "" {
		if config.AllProjects {
			result.Projects = resumableProjects(events)
		} else {
			result.Projects = recentResumableProjects(resumableRelevantProjects(events, config.GitEmails, config.GitHubLogins, now), now)
			result.MoreProjects = len(resumableProjects(events)) - len(result.Projects)
			result.LastWork, result.WorkOlder = resumeLastWork(events, config.GitEmails, now)
		}
		if config.DebugRelevance {
			result.Message = resumeRelevanceMessage(resumeRelevanceDecisions(events, config.GitEmails, config.GitHubLogins, now), location)
			return result, nil
		}
		if !config.AllProjects {
			result.CheckIn, err = loadResumeCheckIn(db)
			if err != nil {
				return ResumeResult{}, err
			}
			if !result.CheckIn.IsZero() {
				result.SinceLast, result.SinceCount = resumePersonalWorkSince(events, config.GitEmails, result.CheckIn, now)
				result.Updates, result.UpdateCount = resumeRelevantUpdatesSince(events, config.GitEmails, result.CheckIn, now)
			}
		}
		result.Message = resumeProjectsMessage(result.Projects, result.MoreProjects, location)
		if !config.AllProjects {
			prefix := resumeLastWorkMessage(result.LastWork, result.WorkOlder, location)
			if len(normalizedEmailSet(config.GitEmails)) == 0 && hasGitCommitEvent(events) {
				prefix += "\nGit identity unknown: counting every captured commit as yours. Set git user.email to filter other authors."
			}
			if waiting := nextWaitingMessage(result.NextItems, 5); waiting != "" {
				prefix += "\n\n" + waiting
			}
			if !result.CheckIn.IsZero() {
				prefix += "\n\n" + resumeSinceMessage(result, location)
				activity, err := resumeProjectActivityMessage(db, config, events, result.CheckIn, now)
				if err != nil {
					return ResumeResult{}, err
				}
				prefix += "\n\n" + activity
			}
			if decisions := resumeDecisionsMessage(result.NextItems); decisions != "" {
				prefix += "\n\n" + decisions
			}
			result.Message = prefix + "\n\n" + result.Message
		}
		result.Message += memoryDiscoveryHints(db, events, MemoryDoctorConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, MemoryDir: config.MemoryDir})
		result.Message += nextWarning + bridgeWarning
		return result, nil
	}
	project = canonicalResumeProjectName(project, events)
	result.Project = project

	memoryDir, err := resolveMemoryDir(config.MemoryDir)
	if err != nil {
		return ResumeResult{}, err
	}
	selection, err := selectResumeProjectEvents(db, config, events, project, memoryDir, now)
	if err != nil {
		return ResumeResult{}, err
	}
	projectEvents := selection.Events
	result.Workstream, result.Members, result.EventGroups = selection.Workstream, selection.Projects, selection.Groups
	result.GitHub = resumeOpenGitHubWork(projectEvents)
	result.Events, result.Omitted = limitResumeEvents(projectEvents, resumeActivityLimit(config.MaxEvents))
	result.Files = resumeRelevantFiles(result.Events)
	if result.Workstream == "" {
		result.Memory, result.MemoryPath, err = loadProjectMemory(memoryDir, config.Project)
		if err != nil {
			return ResumeResult{}, err
		}
	}
	result.Message = resumeProjectMessage(result, location)
	result.Message += memoryDiscoveryHints(db, events, MemoryDoctorConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, MemoryDir: config.MemoryDir, Project: config.Project})
	result.Message += nextWarning + bridgeWarning
	return result, nil
}

func loadResumeCheckIn(db *sql.DB) (time.Time, error) {
	var exists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'resume_checkins'`).Scan(&exists); err != nil {
		return time.Time{}, fmt.Errorf("check resume check-in storage: %w", err)
	}
	if exists == 0 {
		return time.Time{}, nil
	}
	var raw string
	err := db.QueryRow(`SELECT checked_at FROM resume_checkins WHERE scope = 'overview'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("read resume check-in: %w", err)
	}
	checkedAt, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse resume check-in: %w", err)
	}
	return checkedAt, nil
}

func RecordResumeCheckIn(config ResumeConfig, at time.Time) error {
	if at.IsZero() {
		return errors.New("resume check-in time is required")
	}
	dbPath, err := resumeDatabasePath(config)
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if _, err := db.Exec(resumeCheckInSchema); err != nil {
		return fmt.Errorf("create resume check-in storage: %w", err)
	}
	if _, err := db.Exec(`INSERT INTO resume_checkins (scope, checked_at) VALUES ('overview', ?)
		ON CONFLICT(scope) DO UPDATE SET checked_at = excluded.checked_at
		WHERE excluded.checked_at > resume_checkins.checked_at`, at.UTC().Format(resumeCheckInFormat)); err != nil {
		return fmt.Errorf("record resume check-in: %w", err)
	}
	return nil
}

func resumePersonalWorkSince(events []ResumeEvent, gitEmails []string, previous time.Time, now time.Time) ([]ResumeEvent, int) {
	var recent []ResumeEvent
	count := 0
	for _, event := range events {
		if !resumePersonalWork(event, gitEmails) || !resumeEventSince(event, previous, now) {
			continue
		}
		count++
		if len(recent) < resumeSinceEventLimit {
			recent = append(recent, event)
		}
	}
	return recent, count
}

func resumeRelevantUpdatesSince(events []ResumeEvent, gitEmails []string, previous time.Time, now time.Time) ([]ResumeEvent, int) {
	var recent []ResumeEvent
	count := 0
	for _, event := range events {
		if !resumeRelevantUpdate(event, gitEmails) || !resumeEventSince(event, previous, now) {
			continue
		}
		count++
		if len(recent) < resumeSinceEventLimit {
			recent = append(recent, event)
		}
	}
	return recent, count
}

func resumeRelevantUpdate(event ResumeEvent, gitEmails []string) bool {
	if strings.HasPrefix(event.Type, "github.pull_request.") && !nextNeedsUserTransition(event.Type, event.Payload) {
		return false
	}
	return !isTransientResumePath(event.Path) && len(event.Involvement) > 0 && !resumePersonalWork(event, gitEmails)
}

func resumeEventSince(event ResumeEvent, previous, now time.Time) bool {
	if event.Timestamp.After(now) || event.CapturedAt.After(now) {
		return false
	}
	return event.Timestamp.After(previous) || event.CapturedAt.After(previous)
}

func resumeProjectActivityMessage(db *sql.DB, config ResumeConfig, events []ResumeEvent, previous, now time.Time) (string, error) {
	activity, err := loadProjectActivity(db, config.MemoryDir, now, config.GitEmails)
	if err != nil {
		return "", err
	}
	for _, event := range events {
		if !resumeEventSince(event, previous, now) {
			continue
		}
		if resumePersonalWork(event, config.GitEmails) || resumeRelevantUpdate(event, config.GitEmails) || activity.linkedToProject(event.ID) {
			activity.addEvent(event.ID, event.Project)
		}
	}
	prs, err := agentPullRequestsSince(db, previous, now)
	if err != nil {
		return "", err
	}
	for _, pr := range prs {
		activity.addAgentPullRequest(pr)
	}
	lines := append([]string{"Activity since last check-in by project"}, activity.lines()...)
	if len(lines) == 1 {
		lines = append(lines, "No new activity captured.")
	}
	return strings.Join(lines, "\n"), nil
}

func resumeDecisionsMessage(items []NextItem) string {
	decisions := len(nextCategorizeItems(items))
	if decisions == 0 {
		return ""
	}
	return fmt.Sprintf("Categorize: %d decisions remaining (workgraph projects review)", decisions)
}

func resumeSinceMessage(result ResumeResult, location *time.Location) string {
	lines := []string{
		"Previous check-in: " + result.CheckIn.In(location).Format("2006-01-02 15:04"),
		"Personal work since last check-in",
	}
	lines = appendResumeSinceEvents(lines, result.SinceLast, result.SinceCount, result.CheckIn, location, "No new personal work captured.")
	lines = append(lines, "", "Relevant updates since last check-in")
	lines = appendResumeSinceEvents(lines, result.Updates, result.UpdateCount, result.CheckIn, location, "No new relevant updates captured.")
	return strings.Join(lines, "\n")
}

func appendResumeSinceEvents(lines []string, events []ResumeEvent, count int, previous time.Time, location *time.Location, emptyMessage string) []string {
	if count == 0 {
		return append(lines, emptyMessage)
	}
	lines = append(lines, pluralize(count, "event"))
	for _, event := range events {
		line := fmt.Sprintf("- %s %s %s %s", event.Timestamp.In(location).Format("2006-01-02 15:04"), event.Project, event.Type, resumeEventLabel(event))
		if !event.Timestamp.After(previous) && event.CapturedAt.After(previous) {
			line += " (captured since check-in)"
		}
		if len(event.Involvement) > 0 {
			line += " [" + strings.Join(event.Involvement, ", ") + "]"
		}
		lines = append(lines, line)
	}
	if count > len(events) {
		lines = append(lines, fmt.Sprintf("... and %s", pluralizeOlderEvents(count-len(events))))
	}
	return lines
}

func resumeLastWork(events []ResumeEvent, gitEmails []string, now time.Time) ([]ResumeEvent, bool) {
	var session []ResumeEvent
	for _, event := range events {
		if event.Timestamp.After(now) || !resumePersonalWork(event, gitEmails) {
			continue
		}
		if len(session) > 0 {
			previous := session[len(session)-1]
			if event.Project != session[0].Project || previous.Timestamp.Sub(event.Timestamp) > resumeHandoffSessionGap {
				break
			}
		}
		session = append(session, event)
		if len(session) == resumeHandoffEventLimit {
			break
		}
	}
	if len(session) == 0 {
		return nil, false
	}
	return session, session[0].Timestamp.Before(now.Add(-resumeHandoffLookback))
}

func resumePersonalWork(event ResumeEvent, gitEmails []string) bool {
	if event.Project == "" || isTransientResumePath(event.Path) {
		return false
	}
	switch {
	case event.Type == "git.commit":
		return gitCommitAuthoredByLocalIdentity(event.Payload, normalizedEmailSet(gitEmails))
	case event.Type == "file.modified" || event.Type == "file.deleted":
		return event.Path != "" && !broadResumeProjectName(event.Project) && !personalResumeProjectName(event.Project)
	default:
		return false
	}
}

func resumeLastWorkMessage(events []ResumeEvent, older bool, location *time.Location) string {
	lines := []string{"Where you left off"}
	if len(events) == 0 {
		return strings.Join(append(lines, "No personal work session found in captured events."), "\n")
	}
	latest := events[0]
	heading := fmt.Sprintf("- %s: last active %s", latest.Project, latest.Timestamp.In(location).Format("2006-01-02 15:04"))
	if older {
		heading += " (older than a week)"
	}
	lines = append(lines, heading)
	for _, event := range events {
		lines = append(lines, fmt.Sprintf("  - %s %s %s", event.Timestamp.In(location).Format("15:04"), event.Type, resumeEventLabel(event)))
	}
	lines = append(lines, "Run: workgraph resume "+latest.Project)
	return strings.Join(lines, "\n")
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
	var involvementColumnCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('events') WHERE name = 'involvement_json'`).Scan(&involvementColumnCount); err != nil {
		return nil, fmt.Errorf("inspect event schema: %w", err)
	}
	query := `SELECT id, source, type, timestamp, project, summary, payload_json, created_at, involvement_json FROM events WHERE ` + notControlQueryEvent
	if involvementColumnCount == 0 {
		query = `SELECT id, source, type, timestamp, project, summary, payload_json, created_at, NULL FROM events WHERE ` + notControlQueryEvent
	}
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var events []ResumeEvent
	for rows.Next() {
		var stored storedTodayEvent
		var capturedAtRaw string
		if err := rows.Scan(&stored.ID, &stored.Source, &stored.Type, &stored.Timestamp, &stored.Project, &stored.Summary, &stored.PayloadJSON, &capturedAtRaw, &stored.Involvement); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}

		timestamp, err := time.Parse(time.RFC3339Nano, stored.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("parse event timestamp %q: %w", stored.ID, err)
		}
		capturedAt, err := time.Parse(time.RFC3339Nano, capturedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse event capture time %q: %w", stored.ID, err)
		}

		event := ResumeEvent{
			ID:         stored.ID,
			Source:     stored.Source,
			Type:       stored.Type,
			Timestamp:  timestamp.In(location),
			Path:       eventPath(stored.PayloadJSON),
			Payload:    stored.PayloadJSON,
			CapturedAt: capturedAt.In(location),
		}
		if stored.Involvement.Valid {
			if err := json.Unmarshal([]byte(stored.Involvement.String), &event.Involvement); err != nil {
				return nil, fmt.Errorf("parse event involvement %q: %w", stored.ID, err)
			}
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
	for i := range events {
		if resolved := slackResumeCanonicalProject(events[i], aliases); resolved != "" {
			events[i].Project = resolved
		}
	}
}

func slackResumeProjectAliases(events []ResumeEvent) map[string]slackChannelNames {
	aliases := map[string]slackChannelNames{}
	for _, event := range events {
		if !strings.HasPrefix(event.Type, "slack.") {
			continue
		}
		addSlackChannelAlias(aliases, event.Payload)
	}
	return aliases
}

type slackChannelNames struct {
	Channel    string
	Restricted string
}

func (names slackChannelNames) For(payload string) string {
	if SlackPrivacySource(payload) == "slack" {
		return names.Channel
	}
	if names.Restricted != "" {
		return names.Restricted
	}
	return names.Channel
}

func addSlackChannelAlias(aliases map[string]slackChannelNames, payload string) {
	channelID, _ := slackResumeChannelIdentity(payload)
	container := slackPayloadContainer(payload)
	if channelID == "" || container == "" {
		return
	}
	names := aliases[channelID]
	if SlackPrivacySource(payload) == "slack" {
		names.Channel = container
	} else {
		names.Restricted = container
	}
	aliases[channelID] = names
}

const slackChannelAliasQuery = `SELECT payload_json FROM events INDEXED BY idx_events_slack_channel_id
	WHERE source = 'slack' AND json_extract(payload_json, '$.channel_id') = ?
	ORDER BY timestamp ASC, id ASC`

func loadSlackChannelAlias(db *sql.DB, channelID string) (slackChannelNames, error) {
	rows, err := db.Query(slackChannelAliasQuery, channelID)
	if err != nil {
		return slackChannelNames{}, fmt.Errorf("query slack channel alias: %w", err)
	}
	defer rows.Close()
	aliases := map[string]slackChannelNames{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return slackChannelNames{}, fmt.Errorf("scan slack channel alias: %w", err)
		}
		addSlackChannelAlias(aliases, payload)
	}
	if err := rows.Err(); err != nil {
		return slackChannelNames{}, fmt.Errorf("query slack channel alias: %w", err)
	}
	return aliases[channelID], nil
}

func slackResumeCanonicalProject(event ResumeEvent, aliases map[string]slackChannelNames) string {
	if !strings.HasPrefix(event.Type, "slack.") {
		return ""
	}
	if container := slackPayloadContainer(event.Payload); container != "" {
		return container
	}
	channelID, _ := slackResumeChannelIdentity(event.Payload)
	if channelID == "" {
		return ""
	}
	if name := aliases[channelID].For(event.Payload); name != "" {
		return name
	}
	if container := canonicalSlackContainer(event.Project, channelID, slackPayloadConversationKind(event.Payload)); container != "" {
		return container
	}
	return slackConversationFallbackContainer(event.Payload, event.Project)
}

func slackResumeChannelIdentity(payload string) (string, string) {
	var parsed struct {
		ChannelID   string `json:"channel_id"`
		ChannelName string `json:"channel_name"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return "", ""
	}
	return strings.TrimSpace(parsed.ChannelID), slackContainerName(parsed.ChannelName)
}

func slackContainerName(name string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "#"))
}

func slackPayloadContainer(payload string) string {
	channelID, channelName := slackResumeChannelIdentity(payload)
	return canonicalSlackContainer(channelName, channelID, slackPayloadConversationKind(payload))
}

func unresolvedRawSlackProject(event ResumeEvent) bool {
	channelID, _ := slackResumeChannelIdentity(event.Payload)
	if channelID == "" || slackPayloadContainer(event.Payload) != "" {
		return false
	}
	return slackIDOnlyContainer(event.Project, channelID)
}

func resumableProjects(events []ResumeEvent) []ResumeProject {
	return resumableProjectsFromEvents(events)
}

func recentResumableProjects(projects []ResumeProject, now time.Time) []ResumeProject {
	var recent []ResumeProject
	for _, project := range projects {
		if project.LastActive.Before(now.Add(-resumeProjectListWindow)) {
			continue
		}
		recent = append(recent, project)
		if len(recent) == resumeProjectListLimit {
			break
		}
	}
	return recent
}

func hasGitCommitEvent(events []ResumeEvent) bool {
	for _, event := range events {
		if event.Type == "git.commit" {
			return true
		}
	}
	return false
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
	limitResumeRelevanceDecisions(decisionsByProject, resumableRelevantProjects(events, gitEmails, githubLogins, now), now)
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

func limitResumeRelevanceDecisions(decisions map[string]resumeRelevanceDecision, relevant []ResumeProject, now time.Time) {
	selected := map[string]bool{}
	for _, project := range recentResumableProjects(relevant, now) {
		selected[project.Name] = true
	}
	for _, project := range relevant {
		if selected[project.Name] {
			continue
		}
		decision := decisions[project.Name]
		decision.Shown = false
		decision.Reason = fmt.Sprintf("beyond the %d most recent containers", resumeProjectListLimit)
		if project.LastActive.Before(now.Add(-resumeProjectListWindow)) {
			decision.Reason = "no user-work evidence in the last 14 days"
		}
		decisions[project.Name] = decision
	}
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
		emails := normalizedEmailSet(gitEmails)
		switch {
		case len(emails) == 0:
			return true, "git commit with unknown local identity"
		case gitCommitAuthoredByLocalIdentity(event.Payload, emails):
			return true, "git commit authored by local identity"
		case gitCommitAuthorEmail(event.Payload) == "":
			return false, "git commit missing author email"
		default:
			return false, "git commit authored by another identity"
		}
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

func resumeProjectsMessage(projects []ResumeProject, more int, location *time.Location) string {
	lines := []string{"Resumable projects"}
	if len(projects) == 0 && more > 0 {
		lines = append(lines, "No personal project activity in the last 14 days.", fmt.Sprintf("%d more: workgraph resume --all", more))
		return strings.Join(lines, "\n")
	}
	if len(projects) == 0 {
		lines = append(lines, "No resumable projects found.", "Run workgraph start to capture activity.")
		return strings.Join(lines, "\n")
	}

	for _, project := range projects {
		lines = append(lines, fmt.Sprintf("- %s: %s, last active %s", project.Name, pluralize(project.EventCount, "event"), project.LastActive.In(location).Format("2006-01-02 15:04")))
	}
	if more > 0 {
		lines = append(lines, fmt.Sprintf("%d more: workgraph resume --all", more))
	}
	lines = append(lines, "", "Run: workgraph resume <project>")
	return strings.Join(lines, "\n")
}

func resumeRelevanceMessage(decisions []resumeRelevanceDecision, location *time.Location) string {
	lines := []string{"Resume relevance"}
	if len(decisions) == 0 {
		lines = append(lines, "No containers with captured events found.")
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

type resumeProjectSelection struct {
	Events     []ResumeEvent
	Workstream string
	Projects   []string
	Groups     map[string]string
}

func selectResumeProjectEvents(db *sql.DB, config ResumeConfig, events []ResumeEvent, project, memoryDir string, now time.Time) (resumeProjectSelection, error) {
	activity, err := loadProjectActivity(db, config.MemoryDir, now, config.GitEmails)
	if err != nil {
		return resumeProjectSelection{}, err
	}
	selection := resumeProjectSelection{Groups: map[string]string{}}
	slugs := []string{projectSlug(config.Project)}
	if members := resumeWorkstreamProjects(activity.catalog, project); len(members) > 0 {
		selection.Workstream, selection.Projects, slugs = project, members, members
	}
	selected := map[string]bool{}
	named := map[string]string{project: project}
	if selection.Workstream != "" {
		named = map[string]string{}
	}
	mapped := map[string]string{}
	for _, slug := range slugs {
		selected[slug] = true
		if selection.Workstream != "" {
			named[slug] = slug
		}
		path, _ := projectMemoryPath(memoryDir, slug)
		sources, err := memoryProjectSources(db, path)
		if err != nil {
			return resumeProjectSelection{}, err
		}
		for _, source := range sources {
			if mapped[source] == "" {
				mapped[source] = slug
			}
		}
	}
	for _, event := range events {
		if isTransientResumePath(event.Path) {
			continue
		}
		group := ""
		link, linked := activity.links[activity.eventUnits[event.ID]]
		switch {
		case named[event.Project] != "":
			group = named[event.Project]
		case linked:
			if link.Target == "project" && selected[link.Project] {
				group = link.Project
			}
		default:
			group = mapped[event.Project]
		}
		if group == "" {
			continue
		}
		selection.Groups[event.ID] = group
		selection.Events = append(selection.Events, event)
	}
	return selection, nil
}

func resumeWorkstreamProjects(catalog ProjectCatalog, name string) []string {
	if catalogHasProject(catalog, projectSlug(name)) {
		return nil
	}
	var members []string
	for _, entry := range catalog.Projects {
		if entry.Workstream == name {
			members = append(members, entry.Slug)
		}
	}
	sort.Strings(members)
	return members
}

func resumeWorkstreamActivityLines(result ResumeResult, location *time.Location) []string {
	var order []string
	byGroup := map[string][]ResumeEvent{}
	for _, event := range result.Events {
		group := result.EventGroups[event.ID]
		if _, ok := byGroup[group]; !ok {
			order = append(order, group)
		}
		byGroup[group] = append(byGroup[group], event)
	}
	var lines []string
	for _, group := range order {
		label := group
		if group != result.Project && group != result.Workstream {
			label += " project"
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", label, pluralize(len(byGroup[group]), "event")))
		for _, event := range byGroup[group] {
			lines = append(lines, fmt.Sprintf("  - %s %s %s", event.Timestamp.In(location).Format("2006-01-02 15:04"), event.Type, resumeEventLabel(event)))
		}
	}
	return lines
}

func resumeProjectMessage(result ResumeResult, location *time.Location) string {
	lines := []string{"Resume " + result.Project}
	if result.Workstream != "" {
		lines = []string{"Resume " + result.Workstream + " workstream", "Projects: " + strings.Join(result.Members, ", ")}
	}
	if len(result.Events) == 0 {
		lines = append(lines,
			fmt.Sprintf("No recent activity found for %s.", result.Project),
			"Check the project or container name, or run workgraph start to capture activity.",
		)
		if result.Memory != nil {
			lines = append(lines, "", "Project memory", result.Memory.Content)
		}
		if waiting := nextWaitingMessage(result.NextItems, 0); waiting != "" {
			lines = append(lines, "", waiting)
		}
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "", "Recent activity")
	if result.Workstream != "" {
		lines = append(lines, resumeWorkstreamActivityLines(result, location)...)
	} else {
		for _, event := range result.Events {
			lines = append(lines, fmt.Sprintf("- %s %s %s", event.Timestamp.In(location).Format("2006-01-02 15:04"), event.Type, resumeEventLabel(event)))
		}
	}
	if result.Omitted > 0 {
		lines = append(lines, fmt.Sprintf("... and %s", pluralizeOlderEvents(result.Omitted)))
	}

	if waiting := nextWaitingMessage(result.NextItems, 0); waiting != "" {
		lines = append(lines, "", waiting)
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
