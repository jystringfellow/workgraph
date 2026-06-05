package facts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
	_ "github.com/mattn/go-sqlite3"
)

func TestCalendarCaptureStoresNormalizedEvent(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	eventsPath := filepath.Join(tempDir, "calendar-events.json")
	if err := os.WriteFile(eventsPath, []byte(`[
  {
    "provider": "google",
    "calendar_id": "primary",
    "event_id": "evt-123",
    "title": "Cupcake API planning",
    "start": "2026-05-20T15:00:00Z",
    "end": "2026-05-20T15:30:00Z",
    "location": "Conference Room 2",
    "meeting_url": "https://meet.google.com/cup-cake-api",
    "organizer": "Ada Lovelace",
    "attendees": ["Grace Hopper", "Stringfellow"],
    "status": "confirmed",
    "project": "Cupcake API"
  }
]`), 0o644); err != nil {
		t.Fatalf("write calendar events: %v", err)
	}

	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	output, err := runworkgraph(t, repoRoot, "calendar", "capture", "--home", homeDir, "--events-file", eventsPath)
	if err != nil {
		t.Fatalf("workgraph calendar capture failed: %v\n%s", err, output)
	}

	event := calendarEvent(t, filepath.Join(homeDir, "workgraph.db"), "google", "primary", "evt-123")
	if event.Timestamp != "2026-05-20T15:00:00Z" {
		t.Fatalf("expected event timestamp to use calendar start, got %q", event.Timestamp)
	}
	if event.Project != "Cupcake API" {
		t.Fatalf("expected explicit project %q, got %q", "Cupcake API", event.Project)
	}
	if event.Actor != "Ada Lovelace" {
		t.Fatalf("expected organizer actor, got %q", event.Actor)
	}
	if event.Summary != "Cupcake API planning" {
		t.Fatalf("expected title summary, got %q", event.Summary)
	}
	for _, expected := range []string{
		`"provider":"google"`,
		`"calendar_id":"primary"`,
		`"event_id":"evt-123"`,
		`"title":"Cupcake API planning"`,
		`"start":"2026-05-20T15:00:00Z"`,
		`"end":"2026-05-20T15:30:00Z"`,
		`"location":"Conference Room 2"`,
		`"meeting_url":"https://meet.google.com/cup-cake-api"`,
		`"organizer":"Ada Lovelace"`,
		`"attendees":["Grace Hopper","Stringfellow"]`,
		`"status":"confirmed"`,
	} {
		if !strings.Contains(event.PayloadJSON, expected) {
			t.Fatalf("expected payload to include %s, got %s", expected, event.PayloadJSON)
		}
	}
	if !strings.Contains(string(output), "Calendar capture complete") {
		t.Fatalf("expected capture summary, got:\n%s", output)
	}
}

func TestCalendarCaptureKeepsOneEventPerProviderCalendarID(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	eventsPath := filepath.Join(tempDir, "calendar-events.json")
	if err := os.WriteFile(eventsPath, []byte(`[
  {
    "provider": "microsoft",
    "calendar_id": "work",
    "event_id": "evt-456",
    "title": "Sprint planning",
    "start": "2026-05-20T16:00:00Z",
    "end": "2026-05-20T17:00:00Z",
    "organizer": "Grace Hopper",
    "status": "confirmed"
  }
]`), 0o644); err != nil {
		t.Fatalf("write calendar events: %v", err)
	}

	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	for i := 0; i < 2; i++ {
		if output, err := runworkgraph(t, repoRoot, "calendar", "capture", "--home", homeDir, "--events-file", eventsPath); err != nil {
			t.Fatalf("workgraph calendar capture failed: %v\n%s", err, output)
		}
	}

	if count := calendarEventCount(t, filepath.Join(homeDir, "workgraph.db")); count != 1 {
		t.Fatalf("expected recapture to keep one calendar event, got %d", count)
	}
}

func TestCalendarCaptureMapsGoogleCalendarEvents(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	var gotPath string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		gotPath = request.URL.Path
		gotAuth = request.Header.Get("Authorization")
		if request.URL.Query().Get("singleEvents") != "true" {
			t.Fatalf("expected singleEvents=true, got %q", request.URL.RawQuery)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "items": [
    {
      "id": "google-event-1",
      "summary": "Roadmap review",
      "start": {"dateTime": "2026-05-21T09:00:00-07:00"},
      "end": {"dateTime": "2026-05-21T09:45:00-07:00"},
      "location": "Board Room",
      "hangoutLink": "https://meet.google.com/road-map",
      "organizer": {"displayName": "Ada Lovelace", "email": "ada@example.test"},
      "attendees": [
        {"displayName": "Grace Hopper", "email": "grace@example.test"},
        {"email": "stringfellow@example.test"}
      ],
      "status": "confirmed"
    }
  ]
}`))
	}))
	defer server.Close()

	output, err := runworkgraph(t, repoRoot, "calendar", "capture",
		"--home", homeDir,
		"--provider", "google",
		"--calendar-id", "primary",
		"--token", "test-token",
		"--calendar-api-base", server.URL,
	)
	if err != nil {
		t.Fatalf("workgraph calendar capture failed: %v\n%s", err, output)
	}
	if gotPath != "/calendar/v3/calendars/primary/events" {
		t.Fatalf("expected Google events endpoint, got %q", gotPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("expected bearer token, got %q", gotAuth)
	}

	event := calendarEvent(t, filepath.Join(homeDir, "workgraph.db"), "google", "primary", "google-event-1")
	if event.Timestamp != "2026-05-21T16:00:00Z" {
		t.Fatalf("expected UTC start timestamp, got %q", event.Timestamp)
	}
	if event.Actor != "Ada Lovelace" {
		t.Fatalf("expected organizer display name, got %q", event.Actor)
	}
	if event.Summary != "Roadmap review" {
		t.Fatalf("expected Google summary as title, got %q", event.Summary)
	}
	for _, expected := range []string{
		`"provider":"google"`,
		`"calendar_id":"primary"`,
		`"event_id":"google-event-1"`,
		`"title":"Roadmap review"`,
		`"start":"2026-05-21T16:00:00Z"`,
		`"end":"2026-05-21T16:45:00Z"`,
		`"location":"Board Room"`,
		`"meeting_url":"https://meet.google.com/road-map"`,
		`"organizer":"Ada Lovelace"`,
		`"attendees":["Grace Hopper","stringfellow@example.test"]`,
		`"status":"confirmed"`,
	} {
		if !strings.Contains(event.PayloadJSON, expected) {
			t.Fatalf("expected payload to include %s, got %s", expected, event.PayloadJSON)
		}
	}
	if !strings.Contains(string(output), "Events stored: 1") {
		t.Fatalf("expected capture summary, got:\n%s", output)
	}
}

func TestGoogleCalendarCaptureRefreshesExpiredStoredToken(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	var tokenRequestForm url.Values
	var captureAuth string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			if err := request.ParseForm(); err != nil {
				t.Fatalf("parse token request form: %v", err)
			}
			tokenRequestForm = request.Form
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{
  "access_token": "fresh-access-token",
  "token_type": "Bearer",
  "expires_in": 3600,
  "scope": "https://www.googleapis.com/auth/calendar.events.readonly"
}`))
		case "/calendar/v3/calendars/primary/events":
			captureAuth = request.Header.Get("Authorization")
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"items":[{"id":"refreshed-event","summary":"Refreshed token event","start":{"dateTime":"2026-05-23T10:00:00Z"},"end":{"dateTime":"2026-05-23T10:30:00Z"},"organizer":{"displayName":"Ada Lovelace"},"status":"confirmed"}]}`))
		default:
			t.Fatalf("unexpected calendar server path %s", request.URL.Path)
		}
	}))
	defer server.Close()

	configPath := filepath.Join(homeDir, "calendar.json")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`{
  "google": {
    "access_token": "expired-access-token",
    "refresh_token": "stored-refresh-token",
    "token_type": "Bearer",
    "expires_at": "2026-01-01T00:00:00Z",
    "scopes": ["https://www.googleapis.com/auth/calendar.events.readonly"],
    "calendar_ids": ["primary"],
    "api_base_url": %q,
    "client_id": "client-id",
    "token_url": %q
  }
}
`, server.URL, server.URL+"/token")), 0o600); err != nil {
		t.Fatalf("write calendar config: %v", err)
	}

	output, err := runworkgraph(t, repoRoot, "calendar", "capture",
		"--home", homeDir,
		"--provider", "google",
	)
	if err != nil {
		t.Fatalf("workgraph calendar capture failed: %v\n%s", err, output)
	}
	if tokenRequestForm.Get("grant_type") != "refresh_token" {
		t.Fatalf("expected refresh token grant, got %#v", tokenRequestForm)
	}
	if tokenRequestForm.Get("refresh_token") != "stored-refresh-token" {
		t.Fatalf("expected stored refresh token in token request, got %#v", tokenRequestForm)
	}
	if tokenRequestForm.Get("client_id") != "client-id" {
		t.Fatalf("expected client id in token request, got %#v", tokenRequestForm)
	}
	if _, ok := tokenRequestForm["client_secret"]; ok {
		t.Fatalf("expected no client_secret field in calendar refresh token request, got %#v", tokenRequestForm["client_secret"])
	}
	if captureAuth != "Bearer fresh-access-token" {
		t.Fatalf("expected capture to use refreshed access token, got %q", captureAuth)
	}

	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read refreshed calendar config: %v", err)
	}
	if !strings.Contains(string(contents), `"access_token": "fresh-access-token"`) {
		t.Fatalf("expected refreshed access token stored, got:\n%s", contents)
	}
	event := calendarEvent(t, filepath.Join(homeDir, "workgraph.db"), "google", "primary", "refreshed-event")
	if event.Summary != "Refreshed token event" {
		t.Fatalf("expected refreshed token event capture, got %#v", event)
	}
}

func TestGoogleCalendarConnectOAuthStoresConnectorConfigAfterCodeExchange(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "calendar", "connect", "google",
		"--home", homeDir,
		"--no-browser",
		"--client-id", "client-id",
		"--redirect-uri", "http://127.0.0.1:2727/calendar/google/callback",
		"--state", "fixed-state",
	)
	if err != nil {
		t.Fatalf("workgraph calendar connect URL failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Google Calendar OAuth authorization URL") {
		t.Fatalf("expected authorization guidance, got:\n%s", output)
	}
	authorizationURL := calendarAuthorizationURL(t, string(output))
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatalf("parse authorization url: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "accounts.google.com" || parsed.Path != "/o/oauth2/v2/auth" {
		t.Fatalf("expected Google OAuth authorize URL, got %s", authorizationURL)
	}
	query := parsed.Query()
	if query.Get("client_id") != "client-id" {
		t.Fatalf("expected client id in authorization URL, got %q", query.Get("client_id"))
	}
	if query.Get("redirect_uri") != "http://127.0.0.1:2727/calendar/google/callback" {
		t.Fatalf("expected redirect URI in authorization URL, got %q", query.Get("redirect_uri"))
	}
	if query.Get("state") != "fixed-state" {
		t.Fatalf("expected state in authorization URL, got %q", query.Get("state"))
	}
	if query.Get("response_type") != "code" {
		t.Fatalf("expected authorization code response type, got %q", query.Get("response_type"))
	}
	if query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("expected PKCE challenge in authorization URL, got %q / %q", query.Get("code_challenge"), query.Get("code_challenge_method"))
	}
	if query.Get("access_type") != "offline" {
		t.Fatalf("expected offline access for refresh token, got %q", query.Get("access_type"))
	}
	for _, expectedScope := range []string{
		"https://www.googleapis.com/auth/calendar.calendarlist.readonly",
		"https://www.googleapis.com/auth/calendar.freebusy",
		"https://www.googleapis.com/auth/calendar.calendars.readonly",
		"https://www.googleapis.com/auth/calendar.events.owned.readonly",
		"https://www.googleapis.com/auth/calendar.events.readonly",
	} {
		if !strings.Contains(query.Get("scope"), expectedScope) {
			t.Fatalf("expected scope %q, got %q", expectedScope, query.Get("scope"))
		}
	}
	if _, err := os.Stat(filepath.Join(homeDir, "calendar.json")); !os.IsNotExist(err) {
		t.Fatalf("expected calendar config not to be written before code exchange, stat err: %v", err)
	}

	var tokenRequestForm url.Values
	var captureAuth string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			if err := request.ParseForm(); err != nil {
				t.Fatalf("parse token request form: %v", err)
			}
			tokenRequestForm = request.Form
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{
  "access_token": "access-token",
  "refresh_token": "refresh-token",
  "token_type": "Bearer",
  "expires_in": 3600,
  "scope": "https://www.googleapis.com/auth/calendar.calendarlist.readonly https://www.googleapis.com/auth/calendar.freebusy https://www.googleapis.com/auth/calendar.calendars.readonly https://www.googleapis.com/auth/calendar.events.owned.readonly https://www.googleapis.com/auth/calendar.events.readonly"
}`))
		case "/calendar/v3/calendars/primary/events":
			captureAuth = request.Header.Get("Authorization")
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"items":[{"id":"connected-event","summary":"Connected calendar event","start":{"dateTime":"2026-05-22T10:00:00Z"},"end":{"dateTime":"2026-05-22T10:30:00Z"},"organizer":{"displayName":"Ada Lovelace"},"status":"confirmed"}]}`))
		default:
			t.Fatalf("unexpected calendar server path %s", request.URL.Path)
		}
	}))
	defer server.Close()

	output, err = runworkgraph(t, repoRoot, "calendar", "connect", "google",
		"--home", homeDir,
		"--client-id", "client-id",
		"--redirect-uri", "http://127.0.0.1:2727/calendar/google/callback",
		"--code", "oauth-code",
		"--code-verifier", "manual-code-verifier",
		"--state", "fixed-state",
		"--expected-state", "fixed-state",
		"--calendar-id", "primary",
		"--calendar-token-url", server.URL+"/token",
		"--calendar-api-base", server.URL,
	)
	if err != nil {
		t.Fatalf("workgraph calendar connect exchange failed: %v\n%s", err, output)
	}
	if tokenRequestForm.Get("grant_type") != "authorization_code" {
		t.Fatalf("expected authorization_code grant, got %q", tokenRequestForm.Get("grant_type"))
	}
	if tokenRequestForm.Get("code") != "oauth-code" {
		t.Fatalf("expected oauth code in token request, got %q", tokenRequestForm.Get("code"))
	}
	if tokenRequestForm.Get("client_id") != "client-id" || tokenRequestForm.Get("code_verifier") != "manual-code-verifier" {
		t.Fatalf("expected PKCE client id and verifier in token request, got %#v", tokenRequestForm)
	}
	if _, ok := tokenRequestForm["client_secret"]; ok {
		t.Fatalf("expected no client_secret field in calendar token request, got %#v", tokenRequestForm["client_secret"])
	}
	if !strings.Contains(string(output), "Google Calendar connected") {
		t.Fatalf("expected connected message, got:\n%s", output)
	}

	configPath := filepath.Join(homeDir, "calendar.json")
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("expected calendar config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected calendar config permissions 0600, got %v", got)
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read calendar config: %v", err)
	}
	var stored struct {
		Google struct {
			AccessToken  string   `json:"access_token"`
			RefreshToken string   `json:"refresh_token"`
			TokenType    string   `json:"token_type"`
			Scopes       []string `json:"scopes"`
			CalendarIDs  []string `json:"calendar_ids"`
			APIBaseURL   string   `json:"api_base_url"`
		} `json:"google"`
	}
	if err := json.Unmarshal(contents, &stored); err != nil {
		t.Fatalf("parse calendar config: %v", err)
	}
	if stored.Google.AccessToken != "access-token" || stored.Google.RefreshToken != "refresh-token" || stored.Google.TokenType != "Bearer" {
		t.Fatalf("expected stored tokens, got %#v", stored.Google)
	}
	if len(stored.Google.CalendarIDs) != 1 || stored.Google.CalendarIDs[0] != "primary" {
		t.Fatalf("expected stored calendar id, got %#v", stored.Google.CalendarIDs)
	}
	if stored.Google.APIBaseURL != server.URL {
		t.Fatalf("expected stored API base URL, got %q", stored.Google.APIBaseURL)
	}
	if len(stored.Google.Scopes) != 5 ||
		stored.Google.Scopes[0] != "https://www.googleapis.com/auth/calendar.calendarlist.readonly" ||
		stored.Google.Scopes[1] != "https://www.googleapis.com/auth/calendar.freebusy" ||
		stored.Google.Scopes[2] != "https://www.googleapis.com/auth/calendar.calendars.readonly" ||
		stored.Google.Scopes[3] != "https://www.googleapis.com/auth/calendar.events.owned.readonly" ||
		stored.Google.Scopes[4] != "https://www.googleapis.com/auth/calendar.events.readonly" {
		t.Fatalf("expected stored scopes, got %#v", stored.Google.Scopes)
	}

	output, err = runworkgraph(t, repoRoot, "calendar", "capture",
		"--home", homeDir,
		"--provider", "google",
	)
	if err != nil {
		t.Fatalf("workgraph calendar capture from stored config failed: %v\n%s", err, output)
	}
	if captureAuth != "Bearer access-token" {
		t.Fatalf("expected capture to use stored access token, got %q", captureAuth)
	}
	event := calendarEvent(t, filepath.Join(homeDir, "workgraph.db"), "google", "primary", "connected-event")
	if event.Summary != "Connected calendar event" {
		t.Fatalf("expected connected calendar event capture, got %#v", event)
	}
}

func TestMicrosoftCalendarConnectOAuthUsesPKCEAndStoresConnectorConfig(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "calendar", "connect", "microsoft",
		"--home", homeDir,
		"--no-browser",
		"--client-id", "client-id",
		"--redirect-uri", "http://localhost:2727/calendar/microsoft/callback",
		"--state", "fixed-state",
	)
	if err != nil {
		t.Fatalf("workgraph calendar connect URL failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Microsoft Calendar OAuth authorization URL") {
		t.Fatalf("expected Microsoft authorization guidance, got:\n%s", output)
	}
	authorizationURL := calendarAuthorizationURL(t, string(output))
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatalf("parse authorization url: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "login.microsoftonline.com" || parsed.Path != "/common/oauth2/v2.0/authorize" {
		t.Fatalf("expected Microsoft OAuth authorize URL, got %s", authorizationURL)
	}
	query := parsed.Query()
	if query.Get("client_id") != "client-id" {
		t.Fatalf("expected client id in authorization URL, got %q", query.Get("client_id"))
	}
	if query.Get("redirect_uri") != "http://localhost:2727/calendar/microsoft/callback" {
		t.Fatalf("expected redirect URI in authorization URL, got %q", query.Get("redirect_uri"))
	}
	if query.Get("state") != "fixed-state" {
		t.Fatalf("expected state in authorization URL, got %q", query.Get("state"))
	}
	if query.Get("response_type") != "code" {
		t.Fatalf("expected authorization code response type, got %q", query.Get("response_type"))
	}
	if query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("expected PKCE challenge in authorization URL, got %q / %q", query.Get("code_challenge"), query.Get("code_challenge_method"))
	}
	for _, expectedScope := range []string{
		"openid",
		"profile",
		"email",
		"offline_access",
		"https://graph.microsoft.com/Calendars.Read",
		"https://graph.microsoft.com/Calendars.Read.Shared",
	} {
		if !strings.Contains(query.Get("scope"), expectedScope) {
			t.Fatalf("expected scope %q, got %q", expectedScope, query.Get("scope"))
		}
	}
	if strings.Contains(query.Get("scope"), "visualstudio.com") {
		t.Fatalf("expected Microsoft Calendar OAuth not to request Azure DevOps scopes, got %q", query.Get("scope"))
	}
	if _, err := os.Stat(filepath.Join(homeDir, "calendar.json")); !os.IsNotExist(err) {
		t.Fatalf("expected calendar config not to be written before code exchange, stat err: %v", err)
	}

	var tokenRequestForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/token" {
			t.Fatalf("unexpected token server path %s", request.URL.Path)
		}
		if err := request.ParseForm(); err != nil {
			t.Fatalf("parse token request form: %v", err)
		}
		tokenRequestForm = request.Form
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "access_token": "microsoft-access-token",
  "refresh_token": "microsoft-refresh-token",
  "token_type": "Bearer",
  "expires_in": 3600,
  "scope": "openid profile email offline_access https://graph.microsoft.com/Calendars.Read https://graph.microsoft.com/Calendars.Read.Shared"
}`))
	}))
	defer server.Close()

	output, err = runworkgraph(t, repoRoot, "calendar", "connect", "microsoft",
		"--home", homeDir,
		"--client-id", "client-id",
		"--redirect-uri", "http://localhost:2727/calendar/microsoft/callback",
		"--code", "oauth-code",
		"--code-verifier", "manual-code-verifier",
		"--state", "fixed-state",
		"--expected-state", "fixed-state",
		"--calendar-id", "primary",
		"--calendar-token-url", server.URL+"/token",
		"--calendar-api-base", "https://graph.microsoft.test",
	)
	if err != nil {
		t.Fatalf("workgraph calendar connect exchange failed: %v\n%s", err, output)
	}
	if tokenRequestForm.Get("grant_type") != "authorization_code" {
		t.Fatalf("expected authorization_code grant, got %q", tokenRequestForm.Get("grant_type"))
	}
	if tokenRequestForm.Get("code") != "oauth-code" {
		t.Fatalf("expected oauth code in token request, got %q", tokenRequestForm.Get("code"))
	}
	if tokenRequestForm.Get("client_id") != "client-id" || tokenRequestForm.Get("code_verifier") != "manual-code-verifier" {
		t.Fatalf("expected PKCE client id and verifier in token request, got %#v", tokenRequestForm)
	}
	if tokenRequestForm.Get("redirect_uri") != "http://localhost:2727/calendar/microsoft/callback" {
		t.Fatalf("expected redirect URI in token request, got %#v", tokenRequestForm)
	}
	if _, ok := tokenRequestForm["client_secret"]; ok {
		t.Fatalf("expected no client_secret field in Microsoft calendar token request, got %#v", tokenRequestForm["client_secret"])
	}
	if !strings.Contains(string(output), "Microsoft Calendar connected") {
		t.Fatalf("expected connected message, got:\n%s", output)
	}

	contents, err := os.ReadFile(filepath.Join(homeDir, "calendar.json"))
	if err != nil {
		t.Fatalf("read calendar config: %v", err)
	}
	var stored struct {
		Microsoft struct {
			AccessToken  string   `json:"access_token"`
			RefreshToken string   `json:"refresh_token"`
			TokenType    string   `json:"token_type"`
			Scopes       []string `json:"scopes"`
			CalendarIDs  []string `json:"calendar_ids"`
			APIBaseURL   string   `json:"api_base_url"`
			ClientID     string   `json:"client_id"`
			TokenURL     string   `json:"token_url"`
		} `json:"microsoft"`
	}
	if err := json.Unmarshal(contents, &stored); err != nil {
		t.Fatalf("parse calendar config: %v", err)
	}
	if stored.Microsoft.AccessToken != "microsoft-access-token" || stored.Microsoft.RefreshToken != "microsoft-refresh-token" || stored.Microsoft.TokenType != "Bearer" {
		t.Fatalf("expected stored Microsoft tokens, got %#v", stored.Microsoft)
	}
	if len(stored.Microsoft.CalendarIDs) != 1 || stored.Microsoft.CalendarIDs[0] != "primary" {
		t.Fatalf("expected stored calendar id, got %#v", stored.Microsoft.CalendarIDs)
	}
	if stored.Microsoft.APIBaseURL != "https://graph.microsoft.test" {
		t.Fatalf("expected stored API base URL, got %q", stored.Microsoft.APIBaseURL)
	}
	if stored.Microsoft.ClientID != "client-id" || stored.Microsoft.TokenURL != server.URL+"/token" {
		t.Fatalf("expected stored Microsoft OAuth metadata, got %#v", stored.Microsoft)
	}
}

func TestMicrosoftCalendarBrowserConnectUsesPKCEAndStoresConnectorConfig(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	var tokenRequestForm url.Values
	tokenServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/token" {
			t.Fatalf("expected token endpoint, got %s", request.URL.Path)
		}
		if err := request.ParseForm(); err != nil {
			t.Fatalf("parse token request form: %v", err)
		}
		tokenRequestForm = request.Form
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "access_token": "microsoft-browser-access-token",
  "refresh_token": "microsoft-browser-refresh-token",
  "token_type": "Bearer",
  "expires_in": 3600,
  "scope": "openid profile email offline_access https://graph.microsoft.com/Calendars.Read https://graph.microsoft.com/Calendars.Read.Shared"
}`))
	}))
	defer tokenServer.Close()

	openBrowser := func(authorizationURL string) error {
		parsed, err := url.Parse(authorizationURL)
		if err != nil {
			t.Fatalf("parse authorization url: %v", err)
		}
		query := parsed.Query()
		redirectURI := query.Get("redirect_uri")
		if !strings.HasPrefix(redirectURI, "http://127.0.0.1:") {
			t.Fatalf("expected loopback redirect URI, got %q", redirectURI)
		}
		redirect, err := url.Parse(redirectURI)
		if err != nil {
			t.Fatalf("parse redirect URI: %v", err)
		}
		if redirect.Path != "/calendar/microsoft/callback" {
			t.Fatalf("expected Microsoft callback path, got %q", redirect.Path)
		}
		if query.Get("client_id") != workgraph.DefaultMicrosoftCalendarClientID {
			t.Fatalf("expected default Microsoft client id, got %q", query.Get("client_id"))
		}
		if query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
			t.Fatalf("expected PKCE challenge in authorization URL, got %q / %q", query.Get("code_challenge"), query.Get("code_challenge_method"))
		}
		if strings.Contains(query.Get("scope"), "visualstudio.com") {
			t.Fatalf("expected Microsoft Calendar browser OAuth not to request Azure DevOps scopes, got %q", query.Get("scope"))
		}
		go func() {
			time.Sleep(20 * time.Millisecond)
			callback, err := url.Parse(redirectURI)
			if err != nil {
				t.Errorf("parse callback URL: %v", err)
				return
			}
			values := callback.Query()
			values.Set("code", "microsoft-browser-oauth-code")
			values.Set("state", query.Get("state"))
			callback.RawQuery = values.Encode()
			_, _ = http.Get(callback.String())
		}()
		return nil
	}

	result, err := workgraph.ConnectCalendarWithBrowser(context.Background(), workgraph.CalendarConnectConfig{
		HomeDir:     homeDir,
		Provider:    "microsoft",
		RedirectURI: "http://127.0.0.1:0/calendar/microsoft/callback",
		TokenURL:    tokenServer.URL + "/token",
		OpenBrowser: openBrowser,
	})
	if err != nil {
		t.Fatalf("browser calendar connect failed: %v", err)
	}
	if !result.Configured {
		t.Fatalf("expected browser connect to configure Microsoft Calendar")
	}
	if tokenRequestForm.Get("code") != "microsoft-browser-oauth-code" {
		t.Fatalf("expected browser oauth code, got %q", tokenRequestForm.Get("code"))
	}
	if tokenRequestForm.Get("code_verifier") == "" {
		t.Fatalf("expected PKCE code verifier in token request")
	}
	if _, ok := tokenRequestForm["client_secret"]; ok {
		t.Fatalf("expected no client_secret field for PKCE browser connect, got %#v", tokenRequestForm["client_secret"])
	}

	contents, err := os.ReadFile(filepath.Join(homeDir, "calendar.json"))
	if err != nil {
		t.Fatalf("read calendar config: %v", err)
	}
	if !strings.Contains(string(contents), `"microsoft"`) || !strings.Contains(string(contents), `"access_token": "microsoft-browser-access-token"`) {
		t.Fatalf("expected Microsoft browser access token in config, got %s", contents)
	}
}

func TestGoogleCalendarBrowserConnectUsesPKCEAndStoresConnectorConfig(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	var tokenRequestForm url.Values
	tokenServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/token" {
			t.Fatalf("expected token endpoint, got %s", request.URL.Path)
		}
		if err := request.ParseForm(); err != nil {
			t.Fatalf("parse token request form: %v", err)
		}
		tokenRequestForm = request.Form
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "access_token": "browser-access-token",
  "refresh_token": "browser-refresh-token",
  "token_type": "Bearer",
  "expires_in": 3600,
  "scope": "https://www.googleapis.com/auth/calendar.calendarlist.readonly https://www.googleapis.com/auth/calendar.freebusy https://www.googleapis.com/auth/calendar.calendars.readonly https://www.googleapis.com/auth/calendar.events.owned.readonly https://www.googleapis.com/auth/calendar.events.readonly"
}`))
	}))
	defer tokenServer.Close()

	openBrowser := func(authorizationURL string) error {
		parsed, err := url.Parse(authorizationURL)
		if err != nil {
			t.Fatalf("parse authorization url: %v", err)
		}
		query := parsed.Query()
		redirectURI := query.Get("redirect_uri")
		if !strings.HasPrefix(redirectURI, "http://127.0.0.1:") {
			t.Fatalf("expected dynamic loopback redirect URI, got %q", redirectURI)
		}
		redirect, err := url.Parse(redirectURI)
		if err != nil {
			t.Fatalf("parse redirect URI: %v", err)
		}
		if redirect.Path != "" {
			t.Fatalf("expected root loopback redirect path, got %q", redirect.Path)
		}
		if query.Get("client_id") != workgraph.DefaultGoogleCalendarClientID {
			t.Fatalf("expected default client id, got %q", query.Get("client_id"))
		}
		if query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
			t.Fatalf("expected PKCE challenge in authorization URL, got %q / %q", query.Get("code_challenge"), query.Get("code_challenge_method"))
		}
		go func() {
			time.Sleep(20 * time.Millisecond)
			callback, err := url.Parse(redirectURI)
			if err != nil {
				t.Errorf("parse callback URL: %v", err)
				return
			}
			values := callback.Query()
			values.Set("code", "browser-oauth-code")
			values.Set("state", query.Get("state"))
			callback.RawQuery = values.Encode()
			_, _ = http.Get(callback.String())
		}()
		return nil
	}

	result, err := workgraph.ConnectCalendarWithBrowser(context.Background(), workgraph.CalendarConnectConfig{
		HomeDir:     homeDir,
		Provider:    "google",
		TokenURL:    tokenServer.URL + "/token",
		OpenBrowser: openBrowser,
	})
	if err != nil {
		t.Fatalf("browser calendar connect failed: %v", err)
	}
	if !result.Configured {
		t.Fatalf("expected browser connect to configure Google Calendar")
	}
	if tokenRequestForm.Get("code") != "browser-oauth-code" {
		t.Fatalf("expected browser oauth code, got %q", tokenRequestForm.Get("code"))
	}
	if tokenRequestForm.Get("code_verifier") == "" {
		t.Fatalf("expected PKCE code verifier in token request")
	}
	if _, ok := tokenRequestForm["client_secret"]; ok {
		t.Fatalf("expected no client_secret field for PKCE browser connect, got %#v", tokenRequestForm["client_secret"])
	}

	contents, err := os.ReadFile(filepath.Join(homeDir, "calendar.json"))
	if err != nil {
		t.Fatalf("read calendar config: %v", err)
	}
	if !strings.Contains(string(contents), `"access_token": "browser-access-token"`) {
		t.Fatalf("expected browser access token in config, got %s", contents)
	}
}

func TestGoogleCalendarDisconnectRevokesTokenAndRemovesConnectorConfig(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	configPath := filepath.Join(homeDir, "calendar.json")
	if err := os.WriteFile(configPath, []byte(`{
  "google": {
    "access_token": "google-access-token",
    "refresh_token": "google-refresh-token",
    "token_type": "Bearer",
    "calendar_ids": ["primary"]
  }
}
`), 0o600); err != nil {
		t.Fatalf("write calendar config: %v", err)
	}

	var revokedToken string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/revoke" {
			t.Fatalf("unexpected Google revoke path %s", request.URL.Path)
		}
		if request.Method != http.MethodPost {
			t.Fatalf("expected POST revoke request, got %s", request.Method)
		}
		if err := request.ParseForm(); err != nil {
			t.Fatalf("parse revoke form: %v", err)
		}
		revokedToken = request.Form.Get("token")
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	output, err := runworkgraph(t, repoRoot, "calendar", "disconnect", "google",
		"--home", homeDir,
		"--calendar-revoke-url", server.URL+"/revoke",
	)
	if err != nil {
		t.Fatalf("workgraph calendar disconnect failed: %v\n%s", err, output)
	}
	if revokedToken != "google-refresh-token" {
		t.Fatalf("expected disconnect to revoke refresh token, got %q", revokedToken)
	}
	if !strings.Contains(string(output), "Google Calendar disconnected") {
		t.Fatalf("expected disconnect message, got:\n%s", output)
	}
	if !strings.Contains(string(output), "Google Calendar token revoked") {
		t.Fatalf("expected revoke confirmation, got:\n%s", output)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("expected calendar config removed, stat err: %v", err)
	}
}

func TestGoogleCalendarUsesWorkgraphTokenRelayByDefault(t *testing.T) {
	if workgraph.DefaultGoogleCalendarTokenURL != "https://workgraph-google-oauth-token.jystringfellow.workers.dev/calendar/google/token" {
		t.Fatalf("expected default Google Calendar token URL to use workgraph relay, got %q", workgraph.DefaultGoogleCalendarTokenURL)
	}
}

func TestGoogleCalendarTokenRelayDocumentsLocalDevSecrets(t *testing.T) {
	repoRoot := repoRoot(t)

	gitignore, err := os.ReadFile(filepath.Join(repoRoot, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(gitignore), ".dev.vars") {
		t.Fatalf("expected .gitignore to ignore local Cloudflare .dev.vars secrets, got:\n%s", gitignore)
	}

	readme, err := os.ReadFile(filepath.Join(repoRoot, "workers/google-oauth-token/README.md"))
	if err != nil {
		t.Fatalf("read token relay README: %v", err)
	}
	for _, expected := range []string{
		".dev.vars",
		".dev.vars.example",
		"GOOGLE_CLIENT_SECRET",
		"wrangler dev",
		"wrangler secret put GOOGLE_CLIENT_SECRET",
	} {
		if !strings.Contains(string(readme), expected) {
			t.Fatalf("expected token relay README to document %q, got:\n%s", expected, readme)
		}
	}

	example, err := os.ReadFile(filepath.Join(repoRoot, "workers/google-oauth-token/.dev.vars.example"))
	if err != nil {
		t.Fatalf("read token relay .dev.vars example: %v", err)
	}
	if !strings.Contains(string(example), "GOOGLE_CLIENT_SECRET=") {
		t.Fatalf("expected .dev.vars example to include GOOGLE_CLIENT_SECRET, got:\n%s", example)
	}
}

func TestGoogleCalendarTokenRelayAllowsRefreshTokenGrant(t *testing.T) {
	repoRoot := repoRoot(t)
	source, err := os.ReadFile(filepath.Join(repoRoot, "workers/google-oauth-token/src/index.ts"))
	if err != nil {
		t.Fatalf("read token relay source: %v", err)
	}
	for _, expected := range []string{
		"authorization_code",
		"refresh_token",
		"client_secret",
		"GOOGLE_CLIENT_SECRET",
	} {
		if !strings.Contains(string(source), expected) {
			t.Fatalf("expected token relay source to include %q, got:\n%s", expected, source)
		}
	}
	if strings.Contains(string(source), "console.log") {
		t.Fatalf("expected token relay not to log OAuth request data, got:\n%s", source)
	}
}

func TestMicrosoftCalendarPublisherDomainVerificationFile(t *testing.T) {
	path := filepath.Join(repoRoot(t), "public/.well-known/microsoft-identity-association.json")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Microsoft identity association file: %v", err)
	}

	var association struct {
		AssociatedApplications []struct {
			ApplicationID string `json:"applicationId"`
		} `json:"associatedApplications"`
	}
	if err := json.Unmarshal(contents, &association); err != nil {
		t.Fatalf("parse Microsoft identity association file: %v", err)
	}
	if len(association.AssociatedApplications) != 1 {
		t.Fatalf("expected one associated Microsoft application, got %#v", association.AssociatedApplications)
	}
	if got := association.AssociatedApplications[0].ApplicationID; got != "413dce76-e10c-4a57-84b4-89f6b66ab265" {
		t.Fatalf("expected workgraph Microsoft application id, got %q", got)
	}
}

type storedCalendarEvent struct {
	Timestamp   string
	Project     string
	Actor       string
	Summary     string
	PayloadJSON string
}

func calendarAuthorizationURL(t *testing.T, output string) string {
	t.Helper()

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "https://") {
			return line
		}
	}
	t.Fatalf("expected authorization URL in output:\n%s", output)
	return ""
}

func calendarEvent(t *testing.T, dbPath, provider, calendarID, eventID string) storedCalendarEvent {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var event storedCalendarEvent
	err = db.QueryRow(`
		SELECT timestamp, COALESCE(project, ''), COALESCE(actor, ''), COALESCE(summary, ''), payload_json
		FROM events
		WHERE source = 'calendar'
			AND type = 'calendar.event'
			AND json_extract(payload_json, '$.provider') = ?
			AND json_extract(payload_json, '$.calendar_id') = ?
			AND json_extract(payload_json, '$.event_id') = ?
	`, provider, calendarID, eventID).Scan(&event.Timestamp, &event.Project, &event.Actor, &event.Summary, &event.PayloadJSON)
	if err != nil {
		t.Fatalf("fetch calendar event: %v", err)
	}
	return event
}

func calendarEventCount(t *testing.T, dbPath string) int {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE source = 'calendar'`).Scan(&count); err != nil {
		t.Fatalf("count calendar events: %v", err)
	}
	return count
}
