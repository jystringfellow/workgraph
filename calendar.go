package workgraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DefaultGoogleCalendarClientID is the OAuth client id used for Google Calendar PKCE OAuth.
var DefaultGoogleCalendarClientID = "249970569298-d3ba0fnmoc720pq1coacr9s1ss2rhp5s.apps.googleusercontent.com"

// DefaultGoogleCalendarRedirectURI is the local OAuth callback URL for Google Calendar.
const DefaultGoogleCalendarRedirectURI = "http://localhost:2728/calendar/google/callback"

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

// CalendarConnectConfig controls calendar provider OAuth setup.
type CalendarConnectConfig struct {
	HomeDir       string
	Provider      string
	ClientID      string
	ClientSecret  string
	RedirectURI   string
	Code          string
	State         string
	ExpectedState string
	CalendarIDs   []string
	AuthBaseURL   string
	TokenURL      string
	APIBaseURL    string
	HTTPClient    *http.Client
	OpenBrowser   func(string) error
}

// CalendarConnectResult describes calendar provider OAuth setup.
type CalendarConnectResult struct {
	ConfigPath       string
	AuthorizationURL string
	State            string
	Configured       bool
	Message          string
}

type calendarConnectorConfig struct {
	Google *googleCalendarConnectorConfig `json:"google,omitempty"`
}

type googleCalendarConnectorConfig struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	TokenType    string   `json:"token_type,omitempty"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	CalendarIDs  []string `json:"calendar_ids"`
	APIBaseURL   string   `json:"api_base_url,omitempty"`
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

type googleOAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
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

	config.HomeDir = status.HomeDir
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

// ConnectCalendar prepares or completes calendar provider OAuth setup.
func ConnectCalendar(config CalendarConnectConfig) (CalendarConnectResult, error) {
	if strings.ToLower(config.Provider) != "google" {
		return CalendarConnectResult{}, fmt.Errorf("unsupported calendar provider %q", config.Provider)
	}
	return connectGoogleCalendar(config)
}

// ConnectCalendarWithBrowser completes calendar provider OAuth with a local callback and PKCE.
func ConnectCalendarWithBrowser(ctx context.Context, config CalendarConnectConfig) (CalendarConnectResult, error) {
	if strings.ToLower(config.Provider) != "google" {
		return CalendarConnectResult{}, fmt.Errorf("unsupported calendar provider %q", config.Provider)
	}
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return CalendarConnectResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return CalendarConnectResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "workgraph.db")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CalendarConnectResult{}, fmt.Errorf("%w: run workgraph init", ErrNotInitialized)
		}
		return CalendarConnectResult{}, fmt.Errorf("check database: %w", err)
	}
	config.ClientID = resolveGoogleCalendarClientID(config.ClientID)
	if config.ClientID == "" {
		return CalendarConnectResult{}, errors.New("google calendar client id is required for browser connect")
	}
	if config.RedirectURI == "" {
		config.RedirectURI = DefaultGoogleCalendarRedirectURI
	}
	parsedRedirect, err := url.Parse(config.RedirectURI)
	if err != nil {
		return CalendarConnectResult{}, fmt.Errorf("parse google calendar redirect URI: %w", err)
	}
	if !isLocalHTTPRedirect(parsedRedirect) {
		return CalendarConnectResult{}, errors.New("google calendar redirect URI must be an http localhost URL")
	}

	listener, err := net.Listen("tcp", parsedRedirect.Host)
	if err != nil {
		return CalendarConnectResult{}, fmt.Errorf("start google calendar oauth callback: %w", err)
	}
	defer listener.Close()

	state, err := randomURLToken(24)
	if err != nil {
		return CalendarConnectResult{}, fmt.Errorf("create oauth state: %w", err)
	}
	verifier, err := randomURLToken(48)
	if err != nil {
		return CalendarConnectResult{}, fmt.Errorf("create oauth verifier: %w", err)
	}
	config.State = state
	authURL := googleCalendarAuthorizationURLWithPKCE(config, state, slackPKCEChallenge(verifier))
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	server := &http.Server{
		Handler: googleCalendarOAuthCallbackHandler(state, codeCh, errCh),
	}
	go func() {
		err := server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	defer server.Shutdown(context.Background())

	openBrowser := config.OpenBrowser
	if openBrowser == nil {
		openBrowser = openURLInBrowser
	}
	if err := openBrowser(authURL); err != nil {
		return CalendarConnectResult{}, err
	}

	var code string
	select {
	case <-ctx.Done():
		return CalendarConnectResult{}, ctx.Err()
	case err := <-errCh:
		return CalendarConnectResult{}, err
	case code = <-codeCh:
	}

	config.HomeDir = homeDir
	config.Code = code
	config.ExpectedState = state
	config.ClientSecret = ""
	token, err := exchangeGoogleCalendarOAuthCodeWithPKCE(config, verifier)
	if err != nil {
		return CalendarConnectResult{}, err
	}
	result, err := storeGoogleCalendarConnection(homeDir, config, token)
	if err != nil {
		return CalendarConnectResult{}, err
	}
	result.AuthorizationURL = authURL
	result.State = state
	return result, nil
}

func connectGoogleCalendar(config CalendarConnectConfig) (CalendarConnectResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return CalendarConnectResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return CalendarConnectResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "workgraph.db")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CalendarConnectResult{}, fmt.Errorf("%w: run workgraph init", ErrNotInitialized)
		}
		return CalendarConnectResult{}, fmt.Errorf("check database: %w", err)
	}
	config.ClientID = resolveGoogleCalendarClientID(config.ClientID)
	if config.ClientID == "" {
		return CalendarConnectResult{}, errors.New("google calendar client id is required")
	}
	if config.RedirectURI == "" {
		config.RedirectURI = DefaultGoogleCalendarRedirectURI
	}

	state := config.State
	if state == "" {
		generated, err := newEventID()
		if err != nil {
			return CalendarConnectResult{}, fmt.Errorf("create oauth state: %w", err)
		}
		state = generated
	}
	result := CalendarConnectResult{
		ConfigPath:       calendarConfigPath(homeDir),
		AuthorizationURL: googleCalendarAuthorizationURL(config, state),
		State:            state,
	}
	if config.Code == "" {
		result.Message = strings.Join([]string{
			"Google Calendar OAuth authorization URL",
			result.AuthorizationURL,
			"State: " + result.State,
			"After Google redirects back with a code, rerun calendar connect google with --code and --state.",
		}, "\n")
		return result, nil
	}
	if config.ExpectedState != "" && config.State != "" && config.State != config.ExpectedState {
		return CalendarConnectResult{}, errors.New("google calendar oauth state did not match")
	}

	token, err := exchangeGoogleCalendarOAuthCode(config)
	if err != nil {
		return CalendarConnectResult{}, err
	}
	return storeGoogleCalendarConnection(homeDir, config, token)
}

func googleCalendarAuthorizationURL(config CalendarConnectConfig, state string) string {
	return googleCalendarAuthorizationURLWithPKCE(config, state, "")
}

func googleCalendarAuthorizationURLWithPKCE(config CalendarConnectConfig, state string, challenge string) string {
	baseURL := config.AuthBaseURL
	if baseURL == "" {
		baseURL = "https://accounts.google.com/o/oauth2/v2/auth"
	}
	values := url.Values{}
	values.Set("client_id", config.ClientID)
	values.Set("redirect_uri", config.RedirectURI)
	values.Set("response_type", "code")
	values.Set("scope", strings.Join(googleCalendarScopes(), " "))
	values.Set("state", state)
	values.Set("access_type", "offline")
	values.Set("include_granted_scopes", "true")
	if challenge != "" {
		values.Set("code_challenge", challenge)
		values.Set("code_challenge_method", "S256")
	}
	return baseURL + "?" + values.Encode()
}

func exchangeGoogleCalendarOAuthCode(config CalendarConnectConfig) (googleOAuthTokenResponse, error) {
	return exchangeGoogleCalendarOAuthCodeWithVerifier(config, "")
}

func exchangeGoogleCalendarOAuthCodeWithPKCE(config CalendarConnectConfig, verifier string) (googleOAuthTokenResponse, error) {
	return exchangeGoogleCalendarOAuthCodeWithVerifier(config, verifier)
}

func exchangeGoogleCalendarOAuthCodeWithVerifier(config CalendarConnectConfig, verifier string) (googleOAuthTokenResponse, error) {
	tokenURL := config.TokenURL
	if tokenURL == "" {
		tokenURL = "https://oauth2.googleapis.com/token"
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", config.Code)
	form.Set("client_id", config.ClientID)
	form.Set("redirect_uri", config.RedirectURI)
	if config.ClientSecret != "" {
		form.Set("client_secret", config.ClientSecret)
	}
	if verifier != "" {
		form.Set("code_verifier", verifier)
	}

	request, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("build Google Calendar token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("exchange Google Calendar OAuth code: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("read Google Calendar OAuth response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return googleOAuthTokenResponse{}, fmt.Errorf("exchange Google Calendar OAuth code: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var token googleOAuthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("parse Google Calendar OAuth response: %w", err)
	}
	return token, nil
}

func storeGoogleCalendarConnection(homeDir string, config CalendarConnectConfig, token googleOAuthTokenResponse) (CalendarConnectResult, error) {
	if token.AccessToken == "" {
		return CalendarConnectResult{}, errors.New("google calendar oauth response did not include an access token")
	}
	configPath := calendarConfigPath(homeDir)
	stored := calendarConnectorConfig{
		Google: &googleCalendarConnectorConfig{
			AccessToken:  token.AccessToken,
			RefreshToken: token.RefreshToken,
			TokenType:    token.TokenType,
			ExpiresAt:    googleCalendarTokenExpiresAt(token),
			Scopes:       strings.Fields(token.Scope),
			CalendarIDs:  googleCalendarIDs(config.CalendarIDs),
			APIBaseURL:   config.APIBaseURL,
		},
	}
	if err := writeCalendarConnectorConfig(configPath, stored); err != nil {
		return CalendarConnectResult{}, err
	}
	return CalendarConnectResult{
		ConfigPath: configPath,
		Configured: true,
		Message:    googleCalendarConnectedMessage(configPath, stored.Google),
	}, nil
}

func googleCalendarScopes() []string {
	return []string{
		"https://www.googleapis.com/auth/calendar.calendarlist.readonly",
		"https://www.googleapis.com/auth/calendar.freebusy",
		"https://www.googleapis.com/auth/calendar.calendars.readonly",
		"https://www.googleapis.com/auth/calendar.events.owned.readonly",
		"https://www.googleapis.com/auth/calendar.events.readonly",
	}
}

func googleCalendarIDs(calendarIDs []string) []string {
	result := []string{}
	for _, calendarID := range calendarIDs {
		if strings.TrimSpace(calendarID) == "" {
			continue
		}
		result = append(result, calendarID)
	}
	if len(result) == 0 {
		return []string{"primary"}
	}
	return result
}

func googleCalendarTokenExpiresAt(token googleOAuthTokenResponse) string {
	if token.ExpiresIn <= 0 {
		return ""
	}
	return time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339Nano)
}

func googleCalendarConnectedMessage(configPath string, config *googleCalendarConnectorConfig) string {
	return strings.Join([]string{
		"Google Calendar connected",
		"Config: " + configPath,
		"Calendars: " + strings.Join(config.CalendarIDs, ", "),
	}, "\n")
}

func resolveGoogleCalendarClientID(clientID string) string {
	if clientID != "" {
		return clientID
	}
	return DefaultGoogleCalendarClientID
}

func googleCalendarOAuthCallbackHandler(expectedState string, codeCh chan<- string, errCh chan<- error) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if googleErr := query.Get("error"); googleErr != "" {
			http.Error(response, "Google Calendar authorization failed.", http.StatusBadRequest)
			errCh <- fmt.Errorf("google calendar oauth: %s", googleErr)
			return
		}
		if query.Get("state") != expectedState {
			http.Error(response, "Google Calendar authorization state did not match.", http.StatusBadRequest)
			errCh <- errors.New("google calendar oauth state did not match")
			return
		}
		code := query.Get("code")
		if code == "" {
			http.Error(response, "Google Calendar authorization did not include a code.", http.StatusBadRequest)
			errCh <- errors.New("google calendar oauth code missing")
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(response, "<!doctype html><title>workgraph Calendar Authorization Received</title><p>%s</p>", html.EscapeString("Google Calendar authorization received. Return to workgraph to confirm the connection completed."))
		codeCh <- code
	})
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
	token := config.Token
	calendarID := config.CalendarID
	if token == "" {
		stored, err := readCalendarConnectorConfig(config.HomeDir)
		if err == nil && stored.Google != nil {
			token = stored.Google.AccessToken
			if calendarID == "" && len(stored.Google.CalendarIDs) > 0 {
				calendarID = stored.Google.CalendarIDs[0]
			}
			if config.APIBaseURL == "" {
				config.APIBaseURL = stored.Google.APIBaseURL
			}
		}
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("calendar token is required")
	}
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
	request.Header.Set("Authorization", "Bearer "+token)

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

func readCalendarConnectorConfig(homeDir string) (calendarConnectorConfig, error) {
	path := calendarConfigPath(homeDir)
	contents, err := os.ReadFile(path)
	if err != nil {
		return calendarConnectorConfig{}, err
	}
	var config calendarConnectorConfig
	if err := json.Unmarshal(contents, &config); err != nil {
		return calendarConnectorConfig{}, fmt.Errorf("parse calendar config: %w", err)
	}
	return config, nil
}

func writeCalendarConnectorConfig(path string, config calendarConnectorConfig) error {
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode calendar config: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		return fmt.Errorf("write calendar config: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure calendar config: %w", err)
	}
	return nil
}

func calendarConfigPath(homeDir string) string {
	return filepath.Join(homeDir, "calendar.json")
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
