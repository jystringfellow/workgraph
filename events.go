package workgraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type EventsTodayConfig struct {
	HomeDir      string
	DatabasePath string
	Type         string
	Project      string
	Actor        string
	Involvement  string
	Query        string
	Limit        int
	Format       string
}

type EventsTodayResult struct {
	Date        string
	Type        string
	Project     string
	Actor       string
	Involvement string
	Query       string
	Format      string
	Events      []TodayEvent
	Message     string
}

type EventsQueryConfig struct {
	HomeDir      string
	DatabasePath string
	Since        time.Time
	Until        time.Time
	Source       string
	Type         string
	Project      string
	Actor        string
	Involvement  string
	Query        string
	Limit        int
	Format       string
}

type EventsQueryResult struct {
	Since       time.Time
	Until       time.Time
	Source      string
	Type        string
	Project     string
	Actor       string
	Involvement string
	Query       string
	Format      string
	Events      []TodayEvent
	Message     string
}

type jsonEventRecord struct {
	ID          string   `json:"id"`
	Source      string   `json:"source"`
	Type        string   `json:"type"`
	Timestamp   string   `json:"timestamp"`
	Project     string   `json:"project,omitempty"`
	Actor       string   `json:"actor,omitempty"`
	Involvement []string `json:"involvement"`
	Path        string   `json:"path,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Label       string   `json:"label"`
}

func EventsToday(config EventsTodayConfig) (EventsTodayResult, error) {
	format, err := normalizeEventsFormat(config.Format)
	if err != nil {
		return EventsTodayResult{}, err
	}
	today, err := Today(TodayConfig{
		HomeDir:            config.HomeDir,
		DatabasePath:       config.DatabasePath,
		Actor:              config.Actor,
		Involvement:        config.Involvement,
		IncludeAllEvidence: true,
	})
	if err != nil {
		return EventsTodayResult{}, err
	}
	eventType := strings.TrimSpace(config.Type)
	project := strings.TrimSpace(config.Project)
	query := strings.TrimSpace(config.Query)
	events := make([]TodayEvent, 0, len(today.Events))
	for _, event := range today.Events {
		if !eventTypeMatches(event.Type, eventType) {
			continue
		}
		if project != "" && event.Project != project {
			continue
		}
		if !eventQueryMatches(event, query) {
			continue
		}
		events = append(events, event)
	}
	if config.Limit > 0 && len(events) > config.Limit {
		events = events[len(events)-config.Limit:]
	}
	result := EventsTodayResult{
		Date:        today.Date,
		Type:        eventType,
		Project:     project,
		Actor:       strings.TrimSpace(config.Actor),
		Involvement: strings.TrimSpace(config.Involvement),
		Query:       query,
		Format:      format,
		Events:      events,
	}
	location := time.Now().Location()
	if format == "json" {
		message, err := eventsTodayJSONMessage(result, location)
		if err != nil {
			return EventsTodayResult{}, err
		}
		result.Message = message
	} else {
		result.Message = eventsTodayMessage(result, location)
	}
	return result, nil
}

func QueryEvents(config EventsQueryConfig) (EventsQueryResult, error) {
	format, err := normalizeEventsFormat(config.Format)
	if err != nil {
		return EventsQueryResult{}, err
	}
	if !config.Since.IsZero() && !config.Until.IsZero() && config.Until.Before(config.Since) {
		return EventsQueryResult{}, fmt.Errorf("until timestamp must not be before since timestamp")
	}

	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return EventsQueryResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return EventsQueryResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}

	dbPath := config.DatabasePath
	if dbPath == "" {
		dbPath = filepath.Join(homeDir, "workgraph.db")
	}
	dbPath, err = filepath.Abs(dbPath)
	if err != nil {
		return EventsQueryResult{}, fmt.Errorf("resolve database path: %w", err)
	}

	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return EventsQueryResult{}, fmt.Errorf("%w: run workgraph init", ErrNotInitialized)
		}
		return EventsQueryResult{}, fmt.Errorf("check database: %w", err)
	}

	db, err := openDatabase(dbPath)
	if err != nil {
		return EventsQueryResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return EventsQueryResult{}, fmt.Errorf("open database: %w", err)
	}

	location := time.Now().Location()
	if !config.Since.IsZero() {
		location = config.Since.Location()
	} else if !config.Until.IsZero() {
		location = config.Until.Location()
	}

	source := strings.TrimSpace(config.Source)
	eventType := strings.TrimSpace(config.Type)
	project := strings.TrimSpace(config.Project)
	actor := strings.TrimSpace(config.Actor)
	involvement := strings.TrimSpace(config.Involvement)
	queryText := strings.TrimSpace(config.Query)

	sqlQuery := `SELECT id, source, type, timestamp, project, actor, involvement_json, summary, payload_json
		FROM events WHERE ` + notControlQueryEvent
	var args []any

	if !config.Since.IsZero() {
		sqlQuery += ` AND timestamp >= ?`
		args = append(args, config.Since.UTC().Add(-24*time.Hour).Format("2006-01-02"))
	}
	if !config.Until.IsZero() {
		sqlQuery += ` AND timestamp < ?`
		args = append(args, config.Until.UTC().Add(48*time.Hour).Format("2006-01-02"))
	}
	if source != "" {
		sqlQuery += ` AND source = ?`
		args = append(args, source)
	}
	if eventType != "" {
		if prefix, wildcard := strings.CutSuffix(eventType, ".*"); wildcard {
			sqlQuery += ` AND type >= ? AND type < ?`
			args = append(args, prefix+".", prefix+"/")
		} else {
			sqlQuery += ` AND type = ?`
			args = append(args, eventType)
		}
	}
	if project != "" {
		sqlQuery += ` AND project = ?`
		args = append(args, project)
	}
	if actor != "" {
		sqlQuery += ` AND actor = ?`
		args = append(args, actor)
	}
	if involvement != "" {
		if !validInvolvement(involvement) {
			return EventsQueryResult{}, fmt.Errorf("unknown involvement %q", involvement)
		}
		sqlQuery += ` AND EXISTS (SELECT 1 FROM json_each(events.involvement_json) WHERE value = ?)`
		args = append(args, involvement)
	}
	sqlQuery += ` ORDER BY timestamp ASC, id ASC`

	repeatedListRevisions, err := repeatedSlackListRevisionIDs(db)
	if err != nil {
		return EventsQueryResult{}, err
	}
	rows, err := db.Query(sqlQuery, args...)
	if err != nil {
		return EventsQueryResult{}, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	events := make([]TodayEvent, 0)
	for rows.Next() {
		var stored storedTodayEvent
		if err := rows.Scan(&stored.ID, &stored.Source, &stored.Type, &stored.Timestamp, &stored.Project, &stored.Actor, &stored.Involvement, &stored.Summary, &stored.PayloadJSON); err != nil {
			return EventsQueryResult{}, fmt.Errorf("scan event: %w", err)
		}
		if repeatedListRevisions[stored.ID] {
			continue
		}

		timestamp, err := time.Parse(time.RFC3339Nano, stored.Timestamp)
		if err != nil {
			return EventsQueryResult{}, fmt.Errorf("parse event timestamp %q: %w", stored.ID, err)
		}
		if !config.Since.IsZero() && timestamp.Before(config.Since) {
			continue
		}
		if !config.Until.IsZero() && !timestamp.Before(config.Until) {
			continue
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
		if stored.Actor.Valid {
			event.Actor = stored.Actor.String
		}
		if stored.Involvement.Valid {
			if err := json.Unmarshal([]byte(stored.Involvement.String), &event.Involvement); err != nil {
				return EventsQueryResult{}, fmt.Errorf("parse event involvement %q: %w", stored.ID, err)
			}
		}
		if stored.Summary.Valid {
			event.Summary = stored.Summary.String
		}
		if !eventTypeMatches(event.Type, eventType) {
			continue
		}
		if !eventQueryMatches(event, queryText) {
			continue
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return EventsQueryResult{}, fmt.Errorf("query events: %w", err)
	}

	sort.Slice(events, func(i, j int) bool {
		if !events[i].Timestamp.Equal(events[j].Timestamp) {
			return events[i].Timestamp.Before(events[j].Timestamp)
		}
		return events[i].ID < events[j].ID
	})

	if config.Limit > 0 && len(events) > config.Limit {
		events = events[len(events)-config.Limit:]
	}

	result := EventsQueryResult{
		Since:       config.Since,
		Until:       config.Until,
		Source:      source,
		Type:        eventType,
		Project:     project,
		Actor:       actor,
		Involvement: involvement,
		Query:       queryText,
		Format:      format,
		Events:      events,
	}
	if format == "json" {
		message, err := eventsQueryJSONMessage(result, location)
		if err != nil {
			return EventsQueryResult{}, err
		}
		result.Message = message
	} else {
		result.Message = eventsQueryMessage(result, location)
	}
	return result, nil
}

func normalizeEventsFormat(format string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(format))
	switch trimmed {
	case "", "text":
		return "text", nil
	case "json":
		return "json", nil
	default:
		return "", fmt.Errorf("unknown format %q", format)
	}
}

func eventTypeMatches(eventType string, filter string) bool {
	if filter == "" {
		return true
	}
	if prefix, wildcard := strings.CutSuffix(filter, ".*"); wildcard {
		return strings.HasPrefix(eventType, prefix+".")
	}
	return eventType == filter
}

func eventQueryMatches(event TodayEvent, query string) bool {
	if query == "" {
		return true
	}
	needle := strings.ToLower(query)
	for _, candidate := range []string{
		event.Summary,
		event.ID,
		event.Type,
		event.Project,
		event.Actor,
		event.Path,
		eventLabel(event),
	} {
		if candidate != "" && strings.Contains(strings.ToLower(candidate), needle) {
			return true
		}
	}
	return false
}

func untrustedCommentLine(event TodayEvent) string {
	var payload struct {
		ActorKind string `json:"actor_kind"`
		Untrusted struct {
			Author string `json:"author"`
			Body   string `json:"body"`
		} `json:"untrusted"`
	}
	if event.Type != githubPREventReviewComment || json.Unmarshal([]byte(event.Payload), &payload) != nil || payload.Untrusted.Body == "" {
		return ""
	}
	firstLine, _, _ := strings.Cut(strings.TrimSpace(payload.Untrusted.Body), "\n")
	runes := []rune(strings.TrimSpace(firstLine))
	if len(runes) > todayEventLabelMaxRunes {
		runes = append(runes[:todayEventLabelMaxRunes-1], '…')
	}
	return fmt.Sprintf("  untrusted comment (%s, %s): %q", payload.Untrusted.Author, payload.ActorKind, string(runes))
}

func eventsTodayMessage(result EventsTodayResult, location *time.Location) string {
	lines := []string{
		"Events today",
		fmt.Sprintf("%s: %s", result.Date, pluralize(len(result.Events), "event")),
	}
	if result.Type != "" {
		lines = append(lines, "Type: "+result.Type)
	}
	if result.Project != "" {
		lines = append(lines, "Project: "+result.Project)
	}
	if result.Actor != "" {
		lines = append(lines, "Actor: "+result.Actor)
	}
	if result.Involvement != "" {
		lines = append(lines, "Involvement: "+result.Involvement)
	}
	if result.Query != "" {
		lines = append(lines, "Query: "+result.Query)
	}
	if len(result.Events) == 0 {
		lines = append(lines, "No matching events captured today.")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, renderEventDetailLines(result.Events, location, false)...)
	return strings.Join(lines, "\n")
}

func eventsQueryMessage(result EventsQueryResult, location *time.Location) string {
	lines := []string{
		"Events",
		pluralize(len(result.Events), "event"),
	}
	if !result.Since.IsZero() {
		lines = append(lines, "Since: "+result.Since.In(location).Format(time.RFC3339))
	}
	if !result.Until.IsZero() {
		lines = append(lines, "Until: "+result.Until.In(location).Format(time.RFC3339))
	}
	if result.Source != "" {
		lines = append(lines, "Source: "+result.Source)
	}
	if result.Type != "" {
		lines = append(lines, "Type: "+result.Type)
	}
	if result.Project != "" {
		lines = append(lines, "Project: "+result.Project)
	}
	if result.Actor != "" {
		lines = append(lines, "Actor: "+result.Actor)
	}
	if result.Involvement != "" {
		lines = append(lines, "Involvement: "+result.Involvement)
	}
	if result.Query != "" {
		lines = append(lines, "Query: "+result.Query)
	}
	if len(result.Events) == 0 {
		lines = append(lines, "No matching events captured.")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, renderEventDetailLines(result.Events, location, true)...)
	return strings.Join(lines, "\n")
}

func renderEventDetailLines(events []TodayEvent, location *time.Location, includeDate bool) []string {
	timeFormat := "15:04"
	if includeDate {
		timeFormat = "2006-01-02 15:04"
	}
	var lines []string
	for _, event := range events {
		lines = append(lines, fmt.Sprintf("- %s %s %s", event.Timestamp.In(location).Format(timeFormat), event.Type, eventLabel(event)))
		lines = append(lines, "  id: "+event.ID)
		if event.Project != "" {
			lines = append(lines, "  project: "+event.Project)
		}
		if event.Actor != "" {
			lines = append(lines, "  actor: "+event.Actor)
		}
		switch {
		case event.Involvement == nil:
			lines = append(lines, "  involvement: unclassified")
		case len(event.Involvement) == 0:
			lines = append(lines, "  involvement: none")
		default:
			lines = append(lines, "  involvement: "+strings.Join(event.Involvement, ", "))
		}
		if event.Path != "" {
			lines = append(lines, "  path: "+event.Path)
		}
		if line := untrustedCommentLine(event); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func toJSONEventRecords(events []TodayEvent, location *time.Location) []jsonEventRecord {
	records := make([]jsonEventRecord, 0, len(events))
	for _, event := range events {
		records = append(records, jsonEventRecord{
			ID:          event.ID,
			Source:      event.Source,
			Type:        event.Type,
			Timestamp:   event.Timestamp.In(location).Format(time.RFC3339),
			Project:     event.Project,
			Actor:       event.Actor,
			Involvement: event.Involvement,
			Path:        event.Path,
			Summary:     event.Summary,
			Label:       eventLabel(event),
		})
	}
	return records
}

func eventsTodayJSONMessage(result EventsTodayResult, location *time.Location) (string, error) {
	payload := struct {
		Date        string            `json:"date"`
		Type        string            `json:"type,omitempty"`
		Project     string            `json:"project,omitempty"`
		Actor       string            `json:"actor,omitempty"`
		Involvement string            `json:"involvement,omitempty"`
		Query       string            `json:"query,omitempty"`
		Events      []jsonEventRecord `json:"events"`
	}{
		Date:        result.Date,
		Type:        result.Type,
		Project:     result.Project,
		Actor:       result.Actor,
		Involvement: result.Involvement,
		Query:       result.Query,
		Events:      toJSONEventRecords(result.Events, location),
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode events today json: %w", err)
	}
	return string(encoded), nil
}

func eventsQueryJSONMessage(result EventsQueryResult, location *time.Location) (string, error) {
	var since, until string
	if !result.Since.IsZero() {
		since = result.Since.In(location).Format(time.RFC3339)
	}
	if !result.Until.IsZero() {
		until = result.Until.In(location).Format(time.RFC3339)
	}
	payload := struct {
		Since       string            `json:"since,omitempty"`
		Until       string            `json:"until,omitempty"`
		Source      string            `json:"source,omitempty"`
		Type        string            `json:"type,omitempty"`
		Project     string            `json:"project,omitempty"`
		Actor       string            `json:"actor,omitempty"`
		Involvement string            `json:"involvement,omitempty"`
		Query       string            `json:"query,omitempty"`
		Events      []jsonEventRecord `json:"events"`
	}{
		Since:       since,
		Until:       until,
		Source:      result.Source,
		Type:        result.Type,
		Project:     result.Project,
		Actor:       result.Actor,
		Involvement: result.Involvement,
		Query:       result.Query,
		Events:      toJSONEventRecords(result.Events, location),
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode events query json: %w", err)
	}
	return string(encoded), nil
}
