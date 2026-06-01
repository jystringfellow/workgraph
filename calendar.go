package workgraph

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	Provider     string
	CalendarID   string
	Token        string
	APIBaseURL   string
	HTTPClient   *http.Client
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

type googleCalendarEventsResponse struct {
	Items []googleCalendarEvent `json:"items"`
}

type googleCalendarEvent struct {
	ID             string                 `json:"id"`
	Summary        string                 `json:"summary"`
	Start          googleCalendarDateTime `json:"start"`
	End            googleCalendarDateTime `json:"end"`
	Location       string                 `json:"location"`
	HangoutLink    string                 `json:"hangoutLink"`
	ConferenceData googleConferenceData   `json:"conferenceData"`
	Organizer      googleCalendarPerson   `json:"organizer"`
	Attendees      []googleCalendarPerson `json:"attendees"`
	Status         string                 `json:"status"`
}

type googleCalendarDateTime struct {
	DateTime string `json:"dateTime"`
	Date     string `json:"date"`
}

type googleConferenceData struct {
	EntryPoints []googleConferenceEntryPoint `json:"entryPoints"`
}

type googleConferenceEntryPoint struct {
	EntryPointType string `json:"entryPointType"`
	URI            string `json:"uri"`
}

type googleCalendarPerson struct {
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

// CaptureCalendarEvents stores normalized calendar events from a local export file or provider API.
func CaptureCalendarEvents(config CalendarCaptureConfig) (CalendarCaptureResult, error) {
	status, err := prepareRunStatus(RunConfig{
		HomeDir:      config.HomeDir,
		DatabasePath: config.DatabasePath,
		WatchDirs:    config.WatchDirs,
	})
	if err != nil {
		return CalendarCaptureResult{}, err
	}

	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return CalendarCaptureResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return CalendarCaptureResult{}, fmt.Errorf("open database: %w", err)
	}

	exported, err := calendarEvents(config)
	if err != nil {
		return CalendarCaptureResult{}, err
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

func calendarEvents(config CalendarCaptureConfig) ([]calendarExportEvent, error) {
	if config.EventsFile != "" {
		return calendarEventsFromFile(config.EventsFile)
	}

	switch strings.ToLower(config.Provider) {
	case "google":
		return calendarEventsFromGoogle(config)
	case "":
		return nil, errors.New("events file or provider is required")
	default:
		return nil, fmt.Errorf("unsupported calendar provider %q", config.Provider)
	}
}

func calendarEventsFromFile(eventsFile string) ([]calendarExportEvent, error) {
	eventsFile, err := filepath.Abs(eventsFile)
	if err != nil {
		return nil, fmt.Errorf("resolve events file: %w", err)
	}
	contents, err := os.ReadFile(eventsFile)
	if err != nil {
		return nil, fmt.Errorf("read events file: %w", err)
	}
	var exported []calendarExportEvent
	if err := json.Unmarshal(contents, &exported); err != nil {
		return nil, fmt.Errorf("parse events file: %w", err)
	}
	return exported, nil
}

func calendarEventsFromGoogle(config CalendarCaptureConfig) ([]calendarExportEvent, error) {
	if strings.TrimSpace(config.Token) == "" {
		return nil, errors.New("calendar token is required")
	}
	calendarID := config.CalendarID
	if calendarID == "" {
		calendarID = "primary"
	}

	baseURL := config.APIBaseURL
	if baseURL == "" {
		baseURL = "https://www.googleapis.com"
	}
	endpoint, err := url.JoinPath(baseURL, "calendar/v3/calendars", calendarID, "events")
	if err != nil {
		return nil, fmt.Errorf("build Google Calendar events URL: %w", err)
	}
	requestURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("build Google Calendar events URL: %w", err)
	}
	query := requestURL.Query()
	query.Set("singleEvents", "true")
	query.Set("orderBy", "startTime")
	requestURL.RawQuery = query.Encode()

	request, err := http.NewRequest(http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build Google Calendar request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+config.Token)

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request Google Calendar events: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read Google Calendar response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("request Google Calendar events: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var apiResponse googleCalendarEventsResponse
	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return nil, fmt.Errorf("parse Google Calendar response: %w", err)
	}

	events := make([]calendarExportEvent, 0, len(apiResponse.Items))
	for _, event := range apiResponse.Items {
		normalized, err := googleCalendarExportEvent(calendarID, event)
		if err != nil {
			return nil, err
		}
		events = append(events, normalized)
	}
	return events, nil
}

func googleCalendarExportEvent(calendarID string, event googleCalendarEvent) (calendarExportEvent, error) {
	start, allDay, err := googleCalendarTimestamp(event.Start)
	if err != nil {
		return calendarExportEvent{}, fmt.Errorf("parse Google Calendar event start %q: %w", event.ID, err)
	}
	end, _, err := googleCalendarTimestamp(event.End)
	if err != nil {
		return calendarExportEvent{}, fmt.Errorf("parse Google Calendar event end %q: %w", event.ID, err)
	}

	return calendarExportEvent{
		Provider:   "google",
		CalendarID: calendarID,
		EventID:    event.ID,
		Title:      event.Summary,
		Start:      start,
		End:        end,
		AllDay:     allDay,
		Location:   event.Location,
		MeetingURL: googleCalendarMeetingURL(event),
		Organizer:  googleCalendarPersonName(event.Organizer),
		Attendees:  googleCalendarAttendees(event.Attendees),
		Status:     event.Status,
	}, nil
}

func googleCalendarTimestamp(value googleCalendarDateTime) (string, bool, error) {
	if value.DateTime != "" {
		parsed, err := time.Parse(time.RFC3339Nano, value.DateTime)
		if err != nil {
			return "", false, err
		}
		return parsed.UTC().Format(time.RFC3339Nano), false, nil
	}
	if value.Date != "" {
		parsed, err := time.Parse("2006-01-02", value.Date)
		if err != nil {
			return "", true, err
		}
		return parsed.UTC().Format(time.RFC3339Nano), true, nil
	}
	return "", false, errors.New("dateTime or date is required")
}

func googleCalendarMeetingURL(event googleCalendarEvent) string {
	if event.HangoutLink != "" {
		return event.HangoutLink
	}
	for _, entryPoint := range event.ConferenceData.EntryPoints {
		if entryPoint.EntryPointType == "video" && entryPoint.URI != "" {
			return entryPoint.URI
		}
	}
	return ""
}

func googleCalendarAttendees(attendees []googleCalendarPerson) []string {
	result := make([]string, 0, len(attendees))
	for _, attendee := range attendees {
		if name := googleCalendarPersonName(attendee); name != "" {
			result = append(result, name)
		}
	}
	return result
}

func googleCalendarPersonName(person googleCalendarPerson) string {
	if person.DisplayName != "" {
		return person.DisplayName
	}
	return person.Email
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
