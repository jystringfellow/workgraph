package workgraph

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// CalendarCaptureConfig controls normalized calendar event ingestion.
type CalendarCaptureConfig struct {
	HomeDir      string
	DatabasePath string
	WatchDirs    []string
	EventsFile   string
}

// CalendarCaptureResult describes a calendar capture run.
type CalendarCaptureResult struct {
	HomeDir      string
	DatabasePath string
	EventsStored int
	Message      string
}

type calendarExportEvent struct {
	Provider   string   `json:"provider"`
	CalendarID string   `json:"calendar_id"`
	EventID    string   `json:"event_id"`
	Title      string   `json:"title"`
	Start      string   `json:"start"`
	End        string   `json:"end"`
	AllDay     bool     `json:"all_day,omitempty"`
	Location   string   `json:"location,omitempty"`
	MeetingURL string   `json:"meeting_url,omitempty"`
	Organizer  string   `json:"organizer,omitempty"`
	Attendees  []string `json:"attendees,omitempty"`
	Status     string   `json:"status,omitempty"`
	Project    string   `json:"project,omitempty"`
}

type calendarEventPayload struct {
	Provider   string   `json:"provider"`
	CalendarID string   `json:"calendar_id"`
	EventID    string   `json:"event_id"`
	Title      string   `json:"title"`
	Start      string   `json:"start"`
	End        string   `json:"end"`
	AllDay     bool     `json:"all_day,omitempty"`
	Location   string   `json:"location,omitempty"`
	MeetingURL string   `json:"meeting_url,omitempty"`
	Organizer  string   `json:"organizer,omitempty"`
	Attendees  []string `json:"attendees,omitempty"`
	Status     string   `json:"status,omitempty"`
}

// CaptureCalendarEvents stores normalized calendar events from a local export file.
func CaptureCalendarEvents(config CalendarCaptureConfig) (CalendarCaptureResult, error) {
	status, err := prepareRunStatus(RunConfig{
		HomeDir:      config.HomeDir,
		DatabasePath: config.DatabasePath,
		WatchDirs:    config.WatchDirs,
	})
	if err != nil {
		return CalendarCaptureResult{}, err
	}
	if config.EventsFile == "" {
		return CalendarCaptureResult{}, errors.New("events file is required")
	}

	eventsFile, err := filepath.Abs(config.EventsFile)
	if err != nil {
		return CalendarCaptureResult{}, fmt.Errorf("resolve events file: %w", err)
	}
	contents, err := os.ReadFile(eventsFile)
	if err != nil {
		return CalendarCaptureResult{}, fmt.Errorf("read events file: %w", err)
	}
	var exported []calendarExportEvent
	if err := json.Unmarshal(contents, &exported); err != nil {
		return CalendarCaptureResult{}, fmt.Errorf("parse events file: %w", err)
	}

	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return CalendarCaptureResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return CalendarCaptureResult{}, fmt.Errorf("open database: %w", err)
	}

	stored := 0
	for _, event := range exported {
		inserted, err := storeCalendarEvent(db, event)
		if err != nil {
			return CalendarCaptureResult{}, err
		}
		if inserted {
			stored++
		}
	}

	result := CalendarCaptureResult{
		HomeDir:      status.HomeDir,
		DatabasePath: status.DatabasePath,
		EventsStored: stored,
	}
	result.Message = calendarCaptureMessage(result)
	return result, nil
}

func storeCalendarEvent(db *sql.DB, event calendarExportEvent) (bool, error) {
	if strings.TrimSpace(event.Provider) == "" {
		return false, errors.New("calendar event provider is required")
	}
	if strings.TrimSpace(event.CalendarID) == "" {
		return false, errors.New("calendar event calendar_id is required")
	}
	if strings.TrimSpace(event.EventID) == "" {
		return false, errors.New("calendar event event_id is required")
	}
	if strings.TrimSpace(event.Start) == "" {
		return false, errors.New("calendar event start is required")
	}

	start, err := time.Parse(time.RFC3339Nano, event.Start)
	if err != nil {
		return false, fmt.Errorf("parse calendar event start: %w", err)
	}
	if event.End != "" {
		if _, err := time.Parse(time.RFC3339Nano, event.End); err != nil {
			return false, fmt.Errorf("parse calendar event end: %w", err)
		}
	}

	payload, err := json.Marshal(calendarEventPayload{
		Provider:   event.Provider,
		CalendarID: event.CalendarID,
		EventID:    event.EventID,
		Title:      event.Title,
		Start:      event.Start,
		End:        event.End,
		AllDay:     event.AllDay,
		Location:   event.Location,
		MeetingURL: event.MeetingURL,
		Organizer:  event.Organizer,
		Attendees:  append([]string(nil), event.Attendees...),
		Status:     event.Status,
	})
	if err != nil {
		return false, fmt.Errorf("encode calendar event: %w", err)
	}

	result, err := db.Exec(`INSERT INTO events
		(id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			timestamp = excluded.timestamp,
			payload_json = excluded.payload_json,
			project = excluded.project,
			actor = excluded.actor,
			summary = excluded.summary
		WHERE excluded.timestamp != events.timestamp
			OR excluded.payload_json != events.payload_json
			OR COALESCE(excluded.project, '') != COALESCE(events.project, '')
			OR COALESCE(excluded.actor, '') != COALESCE(events.actor, '')
			OR COALESCE(excluded.summary, '') != COALESCE(events.summary, '')`,
		calendarEventID(event),
		"calendar",
		"calendar.event",
		start.UTC().Format(time.RFC3339Nano),
		string(payload),
		emptyStringAsNull(event.Project),
		emptyStringAsNull(event.Organizer),
		emptyStringAsNull(event.Title),
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return false, fmt.Errorf("store calendar event: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store calendar event: %w", err)
	}
	return rows > 0, nil
}

func calendarEventID(event calendarExportEvent) string {
	return fmt.Sprintf("calendar.event:%s:%s:%s", event.Provider, event.CalendarID, event.EventID)
}

func emptyStringAsNull(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func calendarCaptureMessage(result CalendarCaptureResult) string {
	lines := []string{
		"Calendar capture complete",
		"Home: " + result.HomeDir,
		"Database: " + result.DatabasePath,
		fmt.Sprintf("Events stored: %d", result.EventsStored),
	}
	return strings.Join(lines, "\n")
}
