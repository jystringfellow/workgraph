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

// DefaultGoogleCalendarRedirectURI is used by manual Google Calendar OAuth flows.
// Browser OAuth binds a random loopback port and builds the redirect dynamically.
const DefaultGoogleCalendarRedirectURI = "http://127.0.0.1:2727"

// DefaultGoogleCalendarTokenURL is the workgraph OAuth token relay for Google Calendar.
var DefaultGoogleCalendarTokenURL = "https://workgraph-google-oauth-token.jystringfellow.workers.dev/calendar/google/token"

// DefaultGoogleCalendarRevokeURL is Google's OAuth token revocation endpoint.
var DefaultGoogleCalendarRevokeURL = "https://oauth2.googleapis.com/revoke"

// DefaultMicrosoftCalendarClientID is the Entra application id used for Microsoft Calendar PKCE OAuth.
var DefaultMicrosoftCalendarClientID = "413dce76-e10c-4a57-84b4-89f6b66ab265"

// DefaultMicrosoftCalendarRedirectURI is used by Microsoft Calendar OAuth flows.
const DefaultMicrosoftCalendarRedirectURI = "http://localhost:2727/calendar/microsoft/callback"

// DefaultMicrosoftCalendarTokenURL is Microsoft identity platform's v2 token endpoint.
var DefaultMicrosoftCalendarTokenURL = "https://login.microsoftonline.com/common/oauth2/v2.0/token"

// CalendarCaptureConfig controls normalized calendar event ingestion.
type CalendarCaptureConfig struct {
	HomeDir      string
	DatabasePath string
	WatchDirs    []string
	EventsFile   string
	Provider     string
	CalendarID   string
	Token        string
	ClientID     string
	TokenURL     string
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
	RedirectURI   string
	Code          string
	CodeVerifier  string
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

// CalendarDisconnectConfig controls calendar provider disconnect behavior.
type CalendarDisconnectConfig struct {
	HomeDir    string
	Provider   string
	RevokeURL  string
	HTTPClient *http.Client
}

// CalendarDisconnectResult describes calendar provider disconnect behavior.
type CalendarDisconnectResult struct {
	ConfigPath string
	Revoked    bool
	Message    string
}

type calendarConnectorConfig struct {
	Google    *googleCalendarConnectorConfig    `json:"google,omitempty"`
	Microsoft *microsoftCalendarConnectorConfig `json:"microsoft,omitempty"`
}

type googleCalendarConnectorConfig struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	TokenType    string   `json:"token_type,omitempty"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	CalendarIDs  []string `json:"calendar_ids"`
	APIBaseURL   string   `json:"api_base_url,omitempty"`
	ClientID     string   `json:"client_id,omitempty"`
	TokenURL     string   `json:"token_url,omitempty"`
}

type microsoftCalendarConnectorConfig struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	TokenType    string   `json:"token_type,omitempty"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	CalendarIDs  []string `json:"calendar_ids"`
	APIBaseURL   string   `json:"api_base_url,omitempty"`
	ClientID     string   `json:"client_id,omitempty"`
	TokenURL     string   `json:"token_url,omitempty"`
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
	switch strings.ToLower(config.Provider) {
	case "google":
		return connectGoogleCalendar(config)
	case "microsoft":
		return connectMicrosoftCalendar(config)
	default:
		return CalendarConnectResult{}, fmt.Errorf("unsupported calendar provider %q", config.Provider)
	}
}

// DisconnectCalendar revokes provider OAuth access when possible and removes local connector settings.
func DisconnectCalendar(config CalendarDisconnectConfig) (CalendarDisconnectResult, error) {
	switch strings.ToLower(config.Provider) {
	case "google":
		return disconnectGoogleCalendar(config)
	case "microsoft":
		return disconnectMicrosoftCalendar(config)
	default:
		return CalendarDisconnectResult{}, fmt.Errorf("unsupported calendar provider %q", config.Provider)
	}
}

// ConnectCalendarWithBrowser completes calendar provider OAuth with a local callback and PKCE.
func ConnectCalendarWithBrowser(ctx context.Context, config CalendarConnectConfig) (CalendarConnectResult, error) {
	provider := strings.ToLower(config.Provider)
	if provider != "google" && provider != "microsoft" {
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
	if connected, err := calendarProviderConnected(homeDir, provider); err != nil {
		return CalendarConnectResult{}, err
	} else if connected {
		return calendarAlreadyConnectedResult(homeDir, provider), nil
	}
	switch provider {
	case "google":
		config.ClientID = resolveGoogleCalendarClientID(config.ClientID)
	case "microsoft":
		config.ClientID = resolveMicrosoftCalendarClientID(config.ClientID)
	}
	if config.ClientID == "" {
		return CalendarConnectResult{}, fmt.Errorf("%s calendar client id is required for browser connect", provider)
	}
	listenAddress := "127.0.0.1:0"
	redirectPath := ""
	if config.RedirectURI == "" && provider == "microsoft" {
		config.RedirectURI = DefaultMicrosoftCalendarRedirectURI
	}
	if config.RedirectURI != "" {
		parsedRedirect, err := url.Parse(config.RedirectURI)
		if err != nil {
			return CalendarConnectResult{}, fmt.Errorf("parse %s calendar redirect URI: %w", provider, err)
		}
		if !isLocalHTTPRedirect(parsedRedirect) {
			return CalendarConnectResult{}, fmt.Errorf("%s calendar redirect URI must be an http localhost URL", provider)
		}
		listenAddress = parsedRedirect.Host
		redirectPath = parsedRedirect.EscapedPath()
	}

	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return CalendarConnectResult{}, fmt.Errorf("start %s calendar oauth callback: %w", provider, err)
	}
	defer listener.Close()
	if config.RedirectURI == "" || strings.HasSuffix(listenAddress, ":0") {
		config.RedirectURI = "http://" + listener.Addr().String() + redirectPath
	}

	state, err := randomURLToken(24)
	if err != nil {
		return CalendarConnectResult{}, fmt.Errorf("create oauth state: %w", err)
	}
	verifier, err := randomURLToken(48)
	if err != nil {
		return CalendarConnectResult{}, fmt.Errorf("create oauth verifier: %w", err)
	}
	config.State = state
	var authURL string
	switch provider {
	case "google":
		authURL = googleCalendarAuthorizationURLWithPKCE(config, state, slackPKCEChallenge(verifier))
	case "microsoft":
		authURL = microsoftCalendarAuthorizationURLWithPKCE(config, state, slackPKCEChallenge(verifier))
	}
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	server := &http.Server{
		Handler: calendarOAuthCallbackHandler(calendarProviderDisplayName(provider), state, codeCh, errCh),
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
	var result CalendarConnectResult
	switch provider {
	case "google":
		token, err := exchangeGoogleCalendarOAuthCodeWithPKCE(config, verifier)
		if err != nil {
			return CalendarConnectResult{}, err
		}
		result, err = storeGoogleCalendarConnection(homeDir, config, token)
		if err != nil {
			return CalendarConnectResult{}, err
		}
	case "microsoft":
		token, err := exchangeMicrosoftCalendarOAuthCodeWithPKCE(config, verifier)
		if err != nil {
			return CalendarConnectResult{}, err
		}
		result, err = storeMicrosoftCalendarConnection(homeDir, config, token)
		if err != nil {
			return CalendarConnectResult{}, err
		}
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
	if config.Code == "" {
		if connected, err := calendarProviderConnected(homeDir, "google"); err != nil {
			return CalendarConnectResult{}, err
		} else if connected {
			return calendarAlreadyConnectedResult(homeDir, "google"), nil
		}
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
	verifier := config.CodeVerifier
	if verifier == "" {
		generated, err := randomURLToken(48)
		if err != nil {
			return CalendarConnectResult{}, fmt.Errorf("create oauth verifier: %w", err)
		}
		verifier = generated
	}
	result := CalendarConnectResult{
		ConfigPath:       calendarConfigPath(homeDir),
		AuthorizationURL: googleCalendarAuthorizationURLWithPKCE(config, state, slackPKCEChallenge(verifier)),
		State:            state,
	}
	if config.Code == "" {
		result.Message = strings.Join([]string{
			"Google Calendar OAuth authorization URL",
			result.AuthorizationURL,
			"State: " + result.State,
			"Code verifier: " + verifier,
			"After Google redirects back with a code, rerun calendar connect google with --code, --state, and --code-verifier.",
		}, "\n")
		return result, nil
	}
	if config.ExpectedState != "" && config.State != "" && config.State != config.ExpectedState {
		return CalendarConnectResult{}, errors.New("google calendar oauth state did not match")
	}

	token, err := exchangeGoogleCalendarOAuthCodeWithPKCE(config, verifier)
	if err != nil {
		return CalendarConnectResult{}, err
	}
	return storeGoogleCalendarConnection(homeDir, config, token)
}

func connectMicrosoftCalendar(config CalendarConnectConfig) (CalendarConnectResult, error) {
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
	if config.Code == "" {
		if connected, err := calendarProviderConnected(homeDir, "microsoft"); err != nil {
			return CalendarConnectResult{}, err
		} else if connected {
			return calendarAlreadyConnectedResult(homeDir, "microsoft"), nil
		}
	}
	config.ClientID = resolveMicrosoftCalendarClientID(config.ClientID)
	if config.ClientID == "" {
		return CalendarConnectResult{}, errors.New("microsoft calendar client id is required")
	}
	if config.RedirectURI == "" {
		config.RedirectURI = DefaultMicrosoftCalendarRedirectURI
	}

	state := config.State
	if state == "" {
		generated, err := newEventID()
		if err != nil {
			return CalendarConnectResult{}, fmt.Errorf("create oauth state: %w", err)
		}
		state = generated
	}
	verifier := config.CodeVerifier
	if verifier == "" {
		generated, err := randomURLToken(48)
		if err != nil {
			return CalendarConnectResult{}, fmt.Errorf("create oauth verifier: %w", err)
		}
		verifier = generated
	}
	result := CalendarConnectResult{
		ConfigPath:       calendarConfigPath(homeDir),
		AuthorizationURL: microsoftCalendarAuthorizationURLWithPKCE(config, state, slackPKCEChallenge(verifier)),
		State:            state,
	}
	if config.Code == "" {
		result.Message = strings.Join([]string{
			"Microsoft Calendar OAuth authorization URL",
			result.AuthorizationURL,
			"State: " + result.State,
			"Code verifier: " + verifier,
			"After Microsoft redirects back with a code, rerun calendar connect microsoft with --code, --state, and --code-verifier.",
		}, "\n")
		return result, nil
	}
	if config.ExpectedState != "" && config.State != "" && config.State != config.ExpectedState {
		return CalendarConnectResult{}, errors.New("microsoft calendar oauth state did not match")
	}

	token, err := exchangeMicrosoftCalendarOAuthCodeWithPKCE(config, verifier)
	if err != nil {
		return CalendarConnectResult{}, err
	}
	return storeMicrosoftCalendarConnection(homeDir, config, token)
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

func microsoftCalendarAuthorizationURLWithPKCE(config CalendarConnectConfig, state string, challenge string) string {
	baseURL := config.AuthBaseURL
	if baseURL == "" {
		baseURL = "https://login.microsoftonline.com/common/oauth2/v2.0/authorize"
	}
	values := url.Values{}
	values.Set("client_id", config.ClientID)
	values.Set("redirect_uri", config.RedirectURI)
	values.Set("response_type", "code")
	values.Set("scope", strings.Join(microsoftCalendarScopes(), " "))
	values.Set("state", state)
	if challenge != "" {
		values.Set("code_challenge", challenge)
		values.Set("code_challenge_method", "S256")
	}
	return baseURL + "?" + values.Encode()
}

func disconnectGoogleCalendar(config CalendarDisconnectConfig) (CalendarDisconnectResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return CalendarDisconnectResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return CalendarDisconnectResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	stored, err := readCalendarConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return calendarAlreadyDisconnectedResult(homeDir, "google"), nil
		}
		return CalendarDisconnectResult{}, err
	}
	if stored.Google == nil {
		return calendarAlreadyDisconnectedResult(homeDir, "google"), nil
	}

	configPath := calendarConfigPath(homeDir)
	revoked, err := revokeGoogleCalendarToken(config, stored.Google)
	if err != nil {
		return CalendarDisconnectResult{}, err
	}
	stored.Google = nil
	if err := writeOrRemoveCalendarConnectorConfig(configPath, stored); err != nil {
		return CalendarDisconnectResult{}, err
	}

	lines := []string{
		"Google Calendar disconnected",
	}
	if stored.Microsoft == nil {
		lines = append(lines, "Config removed: "+configPath)
	} else {
		lines = append(lines, "Config updated: "+configPath)
	}
	if revoked {
		lines = append(lines, "Google Calendar token revoked")
	}
	return CalendarDisconnectResult{
		ConfigPath: configPath,
		Revoked:    revoked,
		Message:    strings.Join(lines, "\n"),
	}, nil
}

func disconnectMicrosoftCalendar(config CalendarDisconnectConfig) (CalendarDisconnectResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return CalendarDisconnectResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return CalendarDisconnectResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	stored, err := readCalendarConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return calendarAlreadyDisconnectedResult(homeDir, "microsoft"), nil
		}
		return CalendarDisconnectResult{}, err
	}
	if stored.Microsoft == nil {
		return calendarAlreadyDisconnectedResult(homeDir, "microsoft"), nil
	}

	configPath := calendarConfigPath(homeDir)
	stored.Microsoft = nil
	if err := writeOrRemoveCalendarConnectorConfig(configPath, stored); err != nil {
		return CalendarDisconnectResult{}, err
	}

	lines := []string{
		"Microsoft Calendar disconnected",
		"Microsoft Calendar credentials removed locally",
		"To revoke Microsoft consent, remove workgraph from your Microsoft account or tenant app consent settings.",
	}
	if stored.Google == nil {
		lines = append(lines, "Config removed: "+configPath)
	} else {
		lines = append(lines, "Config updated: "+configPath)
	}
	return CalendarDisconnectResult{
		ConfigPath: configPath,
		Message:    strings.Join(lines, "\n"),
	}, nil
}

func revokeGoogleCalendarToken(config CalendarDisconnectConfig, stored *googleCalendarConnectorConfig) (bool, error) {
	token := stored.RefreshToken
	if token == "" {
		token = stored.AccessToken
	}
	if token == "" {
		return false, nil
	}
	revokeURL := config.RevokeURL
	if revokeURL == "" {
		revokeURL = DefaultGoogleCalendarRevokeURL
	}
	form := url.Values{}
	form.Set("token", token)
	request, err := http.NewRequest(http.MethodPost, revokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return false, fmt.Errorf("build Google Calendar revoke request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return false, fmt.Errorf("revoke Google Calendar token: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return false, fmt.Errorf("read Google Calendar revoke response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, fmt.Errorf("revoke Google Calendar token: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return true, nil
}

func exchangeGoogleCalendarOAuthCodeWithPKCE(config CalendarConnectConfig, verifier string) (googleOAuthTokenResponse, error) {
	tokenURL := resolveGoogleCalendarTokenURL(config.TokenURL)
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", config.Code)
	form.Set("client_id", config.ClientID)
	form.Set("redirect_uri", config.RedirectURI)
	form.Set("code_verifier", verifier)

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

func exchangeMicrosoftCalendarOAuthCodeWithPKCE(config CalendarConnectConfig, verifier string) (googleOAuthTokenResponse, error) {
	tokenURL := resolveMicrosoftCalendarTokenURL(config.TokenURL)
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", config.Code)
	form.Set("client_id", config.ClientID)
	form.Set("redirect_uri", config.RedirectURI)
	form.Set("code_verifier", verifier)

	request, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("build Microsoft Calendar token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("exchange Microsoft Calendar OAuth code: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("read Microsoft Calendar OAuth response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return googleOAuthTokenResponse{}, fmt.Errorf("exchange Microsoft Calendar OAuth code: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var token googleOAuthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("parse Microsoft Calendar OAuth response: %w", err)
	}
	return token, nil
}

func refreshGoogleCalendarAccessToken(config CalendarCaptureConfig, stored *googleCalendarConnectorConfig) (*googleCalendarConnectorConfig, error) {
	if stored.RefreshToken == "" {
		return nil, errors.New("google calendar refresh token is required")
	}
	clientID := config.ClientID
	if clientID == "" {
		clientID = stored.ClientID
	}
	clientID = resolveGoogleCalendarClientID(clientID)
	if clientID == "" {
		return nil, errors.New("google calendar client id is required for token refresh")
	}
	tokenURL := config.TokenURL
	if tokenURL == "" {
		tokenURL = stored.TokenURL
	}
	tokenURL = resolveGoogleCalendarTokenURL(tokenURL)

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", stored.RefreshToken)
	form.Set("client_id", clientID)

	request, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build Google Calendar refresh request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("refresh Google Calendar token: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read Google Calendar refresh response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("refresh Google Calendar token: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var token googleOAuthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("parse Google Calendar refresh response: %w", err)
	}
	if token.AccessToken == "" {
		return nil, errors.New("google calendar refresh response did not include an access token")
	}

	refreshed := *stored
	refreshed.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		refreshed.RefreshToken = token.RefreshToken
	}
	if token.TokenType != "" {
		refreshed.TokenType = token.TokenType
	}
	if token.Scope != "" {
		refreshed.Scopes = strings.Fields(token.Scope)
	}
	refreshed.ExpiresAt = googleCalendarTokenExpiresAt(token)
	refreshed.ClientID = clientID
	refreshed.TokenURL = tokenURL
	return &refreshed, nil
}

func storeGoogleCalendarConnection(homeDir string, config CalendarConnectConfig, token googleOAuthTokenResponse) (CalendarConnectResult, error) {
	if token.AccessToken == "" {
		return CalendarConnectResult{}, errors.New("google calendar oauth response did not include an access token")
	}
	configPath := calendarConfigPath(homeDir)
	stored, err := readOrEmptyCalendarConnectorConfig(homeDir)
	if err != nil {
		return CalendarConnectResult{}, err
	}
	stored.Google = &googleCalendarConnectorConfig{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		ExpiresAt:    googleCalendarTokenExpiresAt(token),
		Scopes:       strings.Fields(token.Scope),
		CalendarIDs:  googleCalendarIDs(config.CalendarIDs),
		APIBaseURL:   config.APIBaseURL,
		ClientID:     config.ClientID,
		TokenURL:     resolveGoogleCalendarTokenURL(config.TokenURL),
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

func storeMicrosoftCalendarConnection(homeDir string, config CalendarConnectConfig, token googleOAuthTokenResponse) (CalendarConnectResult, error) {
	if token.AccessToken == "" {
		return CalendarConnectResult{}, errors.New("microsoft calendar oauth response did not include an access token")
	}
	configPath := calendarConfigPath(homeDir)
	stored, err := readOrEmptyCalendarConnectorConfig(homeDir)
	if err != nil {
		return CalendarConnectResult{}, err
	}
	stored.Microsoft = &microsoftCalendarConnectorConfig{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		ExpiresAt:    googleCalendarTokenExpiresAt(token),
		Scopes:       strings.Fields(token.Scope),
		CalendarIDs:  googleCalendarIDs(config.CalendarIDs),
		APIBaseURL:   config.APIBaseURL,
		ClientID:     config.ClientID,
		TokenURL:     resolveMicrosoftCalendarTokenURL(config.TokenURL),
	}
	if err := writeCalendarConnectorConfig(configPath, stored); err != nil {
		return CalendarConnectResult{}, err
	}
	return CalendarConnectResult{
		ConfigPath: configPath,
		Configured: true,
		Message:    microsoftCalendarConnectedMessage(configPath, stored.Microsoft),
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

func microsoftCalendarScopes() []string {
	return []string{
		"openid",
		"profile",
		"email",
		"offline_access",
		"https://graph.microsoft.com/Calendars.Read",
		"https://graph.microsoft.com/Calendars.Read.Shared",
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

func googleCalendarTokenNeedsRefresh(config *googleCalendarConnectorConfig) (bool, error) {
	if config.ExpiresAt == "" {
		return false, nil
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, config.ExpiresAt)
	if err != nil {
		return false, fmt.Errorf("parse Google Calendar token expiry: %w", err)
	}
	return time.Until(expiresAt) <= 5*time.Minute, nil
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

func microsoftCalendarConnectedMessage(configPath string, config *microsoftCalendarConnectorConfig) string {
	return strings.Join([]string{
		"Microsoft Calendar connected",
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

func resolveMicrosoftCalendarClientID(clientID string) string {
	if clientID != "" {
		return clientID
	}
	return DefaultMicrosoftCalendarClientID
}

func resolveGoogleCalendarTokenURL(tokenURL string) string {
	if tokenURL != "" {
		return tokenURL
	}
	return DefaultGoogleCalendarTokenURL
}

func resolveMicrosoftCalendarTokenURL(tokenURL string) string {
	if tokenURL != "" {
		return tokenURL
	}
	return DefaultMicrosoftCalendarTokenURL
}

func calendarOAuthCallbackHandler(providerName string, expectedState string, codeCh chan<- string, errCh chan<- error) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if googleErr := query.Get("error"); googleErr != "" {
			http.Error(response, providerName+" authorization failed.", http.StatusBadRequest)
			errCh <- fmt.Errorf("%s oauth: %s", strings.ToLower(providerName), googleErr)
			return
		}
		if query.Get("state") != expectedState {
			http.Error(response, providerName+" authorization state did not match.", http.StatusBadRequest)
			errCh <- fmt.Errorf("%s oauth state did not match", strings.ToLower(providerName))
			return
		}
		code := query.Get("code")
		if code == "" {
			http.Error(response, providerName+" authorization did not include a code.", http.StatusBadRequest)
			errCh <- fmt.Errorf("%s oauth code missing", strings.ToLower(providerName))
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(response, "<!doctype html><title>%s</title><p>%s</p>", html.EscapeString("workgraph "+providerName+" Authorization Received"), html.EscapeString(providerName+" authorization received. Return to workgraph to confirm the connection completed."))
		codeCh <- code
	})
}

func calendarProviderDisplayName(provider string) string {
	switch strings.ToLower(provider) {
	case "google":
		return "Google Calendar"
	case "microsoft":
		return "Microsoft Calendar"
	default:
		return "Calendar"
	}
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
			googleConfig := stored.Google
			needsRefresh, err := googleCalendarTokenNeedsRefresh(googleConfig)
			if err != nil {
				return nil, err
			}
			if needsRefresh {
				refreshed, err := refreshGoogleCalendarAccessToken(config, googleConfig)
				if err != nil {
					return nil, err
				}
				stored.Google = refreshed
				if err := writeCalendarConnectorConfig(calendarConfigPath(config.HomeDir), stored); err != nil {
					return nil, err
				}
				googleConfig = refreshed
			}
			token = googleConfig.AccessToken
			if calendarID == "" && len(stored.Google.CalendarIDs) > 0 {
				calendarID = stored.Google.CalendarIDs[0]
			}
			if config.APIBaseURL == "" {
				config.APIBaseURL = googleConfig.APIBaseURL
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

func readOrEmptyCalendarConnectorConfig(homeDir string) (calendarConnectorConfig, error) {
	config, err := readCalendarConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return calendarConnectorConfig{}, nil
		}
		return calendarConnectorConfig{}, err
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

func writeOrRemoveCalendarConnectorConfig(path string, config calendarConnectorConfig) error {
	if config.Google == nil && config.Microsoft == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove calendar config: %w", err)
		}
		return nil
	}
	return writeCalendarConnectorConfig(path, config)
}

func calendarProviderConnected(homeDir string, provider string) (bool, error) {
	stored, err := readCalendarConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	switch strings.ToLower(provider) {
	case "google":
		return stored.Google != nil, nil
	case "microsoft":
		return stored.Microsoft != nil, nil
	default:
		return false, nil
	}
}

func calendarAlreadyConnectedResult(homeDir string, provider string) CalendarConnectResult {
	configPath := calendarConfigPath(homeDir)
	return CalendarConnectResult{
		ConfigPath: configPath,
		Configured: true,
		Message: strings.Join([]string{
			calendarProviderDisplayName(provider) + " is already connected",
			"Config: " + configPath,
		}, "\n"),
	}
}

func calendarAlreadyDisconnectedResult(homeDir string, provider string) CalendarDisconnectResult {
	configPath := calendarConfigPath(homeDir)
	return CalendarDisconnectResult{
		ConfigPath: configPath,
		Message: strings.Join([]string{
			calendarProviderDisplayName(provider) + " is not connected",
			"No local Calendar connector settings changed.",
		}, "\n"),
	}
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
