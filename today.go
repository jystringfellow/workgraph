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
	todaySessionGap         = 30 * time.Minute
	todayEventLabelMaxRunes = 160

	// todayAssociationTargetLimit bounds how many of today's most recent
	// events are evaluated as association targets, matching the requirement
	// that today's association context is a small supplement, not a broad
	// re-scan of stored evidence.
	todayAssociationTargetLimit = 50
	// todayAssociationRenderLimit bounds how many coalesced association
	// pairs are rendered in the compact today section.
	todayAssociationRenderLimit = 5
	// todayAssociationMinimumScore restricts today's association context to
	// the high-confidence baseline tier only (score 80 through 100).
	todayAssociationMinimumScore = 80
)

// TodayConfig controls the local-day activity view.
type TodayConfig struct {
	HomeDir      string
	DatabasePath string
	Now          time.Time
}

// TodayResult describes today's activity in deterministic plain text.
type TodayResult struct {
	Date         string
	Events       []TodayEvent
	Sessions     []TodaySession
	Associations []TodayAssociation
	Message      string
}

// TodayEvent is one stored event included in the local-day activity view.
type TodayEvent struct {
	ID        string
	Source    string
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

// TodayAssociation is a compact, high-confidence deterministic baseline
// association surfaced alongside today's raw events and sessions. It
// supplements that primary evidence and never regroups or replaces it.
type TodayAssociation struct {
	EventIDs     []string
	PatternKey   string
	SuggestionID string
	Score        int
	Confidence   string
	Status       string
	Reason       string
}

type storedTodayEvent struct {
	ID          string
	Source      string
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
	associations, err := loadTodayAssociations(db, events, now.UTC())
	if err != nil {
		return TodayResult{}, err
	}
	result.Associations = associations
	result.Message = todayMessage(result, location)

	return result, nil
}

func loadTodayEvents(db *sql.DB, now time.Time) ([]TodayEvent, error) {
	location := now.Location()
	dayStart := time.Date(now.In(location).Year(), now.In(location).Month(), now.In(location).Day(), 0, 0, 0, 0, location)
	dayEnd := dayStart.AddDate(0, 0, 1)

	rows, err := db.Query(
		`SELECT id, source, type, timestamp, project, summary, payload_json FROM events WHERE timestamp >= ? AND timestamp < ? ORDER BY timestamp ASC, id ASC`,
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
		if err := rows.Scan(&stored.ID, &stored.Source, &stored.Type, &stored.Timestamp, &stored.Project, &stored.Summary, &stored.PayloadJSON); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}

		timestamp, err := time.Parse(time.RFC3339Nano, stored.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("parse event timestamp %q: %w", stored.ID, err)
		}

		event := TodayEvent{
			ID:        stored.ID,
			Source:    stored.Source,
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

// loadTodayAssociations evaluates a small, bounded set of high-confidence
// deterministic baseline associations to supplement today's raw events and
// sessions. It reuses the same candidate window and scoring evaluator as
// `workgraph associations explain`, but only reads existing suggestion
// lifecycle state; it never coalesces or writes new association suggestion
// rows. This keeps repeated `today` invocations free of uncontrolled writes.
//
// Only stored snoozed suggestions are refreshed via expireSnoozedSuggestions,
// matching the read-path behavior already used by ListSuggestions and
// ExplainEventAssociations.
func loadTodayAssociations(db *sql.DB, events []TodayEvent, now time.Time) ([]TodayAssociation, error) {
	if len(events) == 0 {
		return nil, nil
	}
	if err := expireSnoozedSuggestions(db, now); err != nil {
		return nil, err
	}

	targets := events
	if len(targets) > todayAssociationTargetLimit {
		targets = targets[len(targets)-todayAssociationTargetLimit:]
	}

	type candidateAssociation struct {
		association TodayAssociation
		recency     time.Time
	}
	found := map[string]candidateAssociation{}

	for _, event := range targets {
		target := AssociationEvent{
			ID:        event.ID,
			Source:    event.Source,
			Type:      event.Type,
			Timestamp: event.Timestamp.UTC(),
			Project:   event.Project,
			Summary:   event.Summary,
			Payload:   event.Payload,
		}
		candidates, err := loadAssociationCandidates(db, target)
		if err != nil {
			return nil, err
		}
		for _, candidateEvent := range candidates {
			candidate, ok, err := evaluateAssociationPair(target, candidateEvent)
			if err != nil {
				return nil, err
			}
			if !ok || candidate.Score < todayAssociationMinimumScore {
				continue
			}
			if _, exists := found[candidate.PatternKey]; exists {
				continue
			}

			status, err := todayAssociationLifecycleStatus(db, candidate.PatternKey)
			if err != nil {
				return nil, err
			}
			if todayAssociationStatusHidden(status) {
				continue
			}
			suppressed, err := associationPatternSuppressed(db, candidate.PatternKey, now)
			if err != nil {
				return nil, err
			}
			if suppressed {
				continue
			}

			recency := target.Timestamp
			if candidateEvent.Timestamp.After(recency) {
				recency = candidateEvent.Timestamp
			}
			reason := ""
			if len(candidate.Reasons) > 0 {
				reason = candidate.Reasons[0]
			}
			found[candidate.PatternKey] = candidateAssociation{
				association: TodayAssociation{
					EventIDs:     candidate.EventIDs,
					PatternKey:   candidate.PatternKey,
					SuggestionID: candidate.SuggestionID,
					Score:        candidate.Score,
					Confidence:   candidate.Confidence,
					Status:       status,
					Reason:       reason,
				},
				recency: recency,
			}
		}
	}

	if len(found) == 0 {
		return nil, nil
	}

	ranked := make([]candidateAssociation, 0, len(found))
	for _, entry := range found {
		ranked = append(ranked, entry)
	}
	sort.Slice(ranked, func(i, j int) bool {
		left, right := ranked[i], ranked[j]
		if left.association.Score != right.association.Score {
			return left.association.Score > right.association.Score
		}
		if !left.recency.Equal(right.recency) {
			return left.recency.After(right.recency)
		}
		return left.association.PatternKey < right.association.PatternKey
	})
	if len(ranked) > todayAssociationRenderLimit {
		ranked = ranked[:todayAssociationRenderLimit]
	}

	associations := make([]TodayAssociation, len(ranked))
	for i, entry := range ranked {
		associations[i] = entry.association
	}
	return associations, nil
}

// todayAssociationLifecycleStatus reports the current suggestion lifecycle
// state for a candidate association pattern. A pattern with no stored
// suggestion row yet (never inspected through `associations explain`)
// defaults to "proposed" without writing anything.
func todayAssociationLifecycleStatus(db *sql.DB, patternKey string) (string, error) {
	suggestion, err := readSuggestionByPattern(db, "association", patternKey)
	if errors.Is(err, sql.ErrNoRows) {
		return "proposed", nil
	}
	if err != nil {
		return "", err
	}
	return suggestion.Status, nil
}

// todayAssociationStatusHidden hides dismissed and snoozed associations.
// Proposed, reviewed, approved, and acted associations remain visible.
func todayAssociationStatusHidden(status string) bool {
	switch status {
	case "dismissed", "snoozed":
		return true
	default:
		return false
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
			lines = append(lines, fmt.Sprintf("  - %s %s %s", event.Timestamp.In(location).Format("15:04"), event.Type, todayEventLabel(event)))
		}
	}

	if len(result.Associations) > 0 {
		lines = append(lines, "", "Associations")
		for _, association := range result.Associations {
			lines = append(lines, fmt.Sprintf("- %s", strings.Join(association.EventIDs, ", ")))
			lines = append(lines, fmt.Sprintf("  score: %d (%s)", association.Score, association.Confidence))
			lines = append(lines, "  state: "+association.Status)
			lines = append(lines, "  reason: "+association.Reason)
		}
	}

	lines = append(lines, "", "Details: workgraph events today")

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

func todayEventLabel(event TodayEvent) string {
	label := strings.Join(strings.Fields(eventLabel(event)), " ")
	if label == "" {
		label = event.ID
	}
	runes := []rune(label)
	if len(runes) <= todayEventLabelMaxRunes {
		return label
	}
	return strings.TrimSpace(string(runes[:todayEventLabelMaxRunes-1])) + "…"
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
