package facts

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
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

func TestSlackCaptureStoresMessageEvent(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	eventsPath := filepath.Join(tempDir, "slack-events.json")
	if err := os.WriteFile(eventsPath, []byte(`[
  {
    "kind": "message",
    "channel_id": "C123",
    "channel_name": "cupcake-api",
    "user": "U456",
    "text": "Ship the auth flow before beta.",
    "ts": "1716215400.000100",
    "permalink": "https://example.slack.com/archives/C123/p1716215400000100",
    "timestamp": "2026-05-20T14:30:00Z"
  }
]`), 0o644); err != nil {
		t.Fatalf("write slack events: %v", err)
	}

	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	output, err := runworkgraph(t, repoRoot, "slack", "capture", "--home", homeDir, "--events-file", eventsPath)
	if err != nil {
		t.Fatalf("workgraph slack capture failed: %v\n%s", err, output)
	}

	event := slackEvent(t, filepath.Join(homeDir, "workgraph.db"), "slack.message", "C123", "1716215400.000100")
	if event.Project != "cupcake-api" {
		t.Fatalf("expected channel fallback project %q, got %q", "cupcake-api", event.Project)
	}
	if event.Actor != "U456" {
		t.Fatalf("expected actor U456, got %q", event.Actor)
	}
	if event.Summary != "Ship the auth flow before beta." {
		t.Fatalf("expected message summary, got %q", event.Summary)
	}
	for _, expected := range []string{
		`"channel_id":"C123"`,
		`"channel_name":"cupcake-api"`,
		`"user":"U456"`,
		`"text":"Ship the auth flow before beta."`,
		`"ts":"1716215400.000100"`,
		`"permalink":"https://example.slack.com/archives/C123/p1716215400000100"`,
	} {
		if !strings.Contains(event.PayloadJSON, expected) {
			t.Fatalf("expected payload to include %s, got %s", expected, event.PayloadJSON)
		}
	}
	if !strings.Contains(string(output), "Slack capture complete") {
		t.Fatalf("expected capture summary, got:\n%s", output)
	}
}

func TestSlackCaptureStoresThreadReplyWithoutDuplicateEvent(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	eventsPath := filepath.Join(tempDir, "slack-events.json")
	if err := os.WriteFile(eventsPath, []byte(`[
  {
    "kind": "thread_reply",
    "channel_id": "C123",
    "channel_name": "cupcake-api",
    "project": "Cupcake API",
    "user": "U789",
    "text": "I confirmed the migration window.",
    "ts": "1716215500.000200",
    "thread_ts": "1716215400.000100",
    "permalink": "https://example.slack.com/archives/C123/p1716215500000200",
    "timestamp": "2026-05-20T14:31:40Z"
  }
]`), 0o644); err != nil {
		t.Fatalf("write slack events: %v", err)
	}

	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	for i := 0; i < 2; i++ {
		if output, err := runworkgraph(t, repoRoot, "slack", "capture", "--home", homeDir, "--events-file", eventsPath); err != nil {
			t.Fatalf("workgraph slack capture failed: %v\n%s", err, output)
		}
	}

	event := slackEvent(t, filepath.Join(homeDir, "workgraph.db"), "slack.thread_reply", "C123", "1716215500.000200")
	if event.Project != "Cupcake API" {
		t.Fatalf("expected explicit project %q, got %q", "Cupcake API", event.Project)
	}
	if event.Actor != "U789" {
		t.Fatalf("expected actor U789, got %q", event.Actor)
	}
	if !strings.Contains(event.PayloadJSON, `"thread_ts":"1716215400.000100"`) {
		t.Fatalf("expected thread timestamp in payload, got %s", event.PayloadJSON)
	}
	if count := slackEventCount(t, filepath.Join(homeDir, "workgraph.db")); count != 1 {
		t.Fatalf("expected recapture to keep one Slack event, got %d", count)
	}
}

func TestRunCollectsConfiguredSlackMessagesAndThreadReplies(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	transport := &fakeSlackTransport{}
	client := &http.Client{Transport: transport}
	transport.handle = func(request *http.Request) string {
		if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("expected bearer token, got %q", got)
		}
		if request.URL.Query().Get("channel") != "C123" {
			t.Fatalf("expected configured channel C123, got %q", request.URL.Query().Get("channel"))
		}
		switch request.URL.Path {
		case "/api/conversations.history":
			transport.historyRequests++
			return `{"ok":true,"messages":[{"type":"message","user":"U456","text":"We decided to ship auth first.","ts":"1716215400.000100","permalink":"https://example.slack.com/archives/C123/p1716215400000100","reply_count":1}]}`
		case "/api/conversations.replies":
			transport.repliesRequests++
			if request.URL.Query().Get("ts") != "1716215400.000100" {
				t.Fatalf("expected thread ts, got %q", request.URL.Query().Get("ts"))
			}
			return `{"ok":true,"messages":[{"type":"message","user":"U456","text":"We decided to ship auth first.","ts":"1716215400.000100"},{"type":"message","user":"U789","text":"I noted that in the release plan.","ts":"1716215500.000200","thread_ts":"1716215400.000100","permalink":"https://example.slack.com/archives/C123/p1716215500000200"}]}`
		default:
			t.Fatalf("unexpected Slack API path %s", request.URL.Path)
		}
		return `{"ok":false,"error":"unexpected_request"}`
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:           homeDir,
		DatabasePath:      initResult.DatabasePath,
		WatchDirs:         []string{tempDir},
		SlackToken:        "test-token",
		SlackChannels:     []string{"C123"},
		SlackAPIBaseURL:   "https://slack.test/api",
		SlackHTTPClient:   client,
		SlackPollInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	waitForSlackEvent(t, initResult.DatabasePath, "slack.message", "C123", "1716215400.000100")
	waitForSlackEvent(t, initResult.DatabasePath, "slack.thread_reply", "C123", "1716215500.000200")

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if transport.historyRequests == 0 {
		t.Fatalf("expected Slack history to be requested")
	}
	if transport.repliesRequests == 0 {
		t.Fatalf("expected Slack thread replies to be requested")
	}
	if count := slackEventCount(t, initResult.DatabasePath); count != 2 {
		t.Fatalf("expected message and thread reply events, got %d", count)
	}
}

func TestRunDiscoversSlackChannelsWhenNoneAreConfigured(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	transport := &fakeSlackTransport{}
	client := &http.Client{Transport: transport}
	transport.handle = func(request *http.Request) string {
		if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("expected bearer token, got %q", got)
		}
		switch request.URL.Path {
		case "/api/conversations.list":
			transport.listRequests++
			if request.URL.Query().Get("types") != "public_channel,private_channel" {
				t.Fatalf("expected public and private channel discovery, got %q", request.URL.Query().Get("types"))
			}
			return `{"ok":true,"channels":[{"id":"C123","name":"cupcake-api","is_private":false},{"id":"G456","name":"exec-planning","is_private":true}]}`
		case "/api/conversations.history":
			transport.historyRequests++
			switch request.URL.Query().Get("channel") {
			case "C123":
				return `{"ok":true,"messages":[{"type":"message","user":"U456","text":"Public channel context.","ts":"1716215700.000400"}]}`
			case "G456":
				return `{"ok":true,"messages":[{"type":"message","user":"U789","text":"Private channel context.","ts":"1716215800.000500"}]}`
			default:
				t.Fatalf("unexpected discovered channel %q", request.URL.Query().Get("channel"))
			}
		default:
			t.Fatalf("unexpected Slack API path %s", request.URL.Path)
		}
		return `{"ok":false,"error":"unexpected_request"}`
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:           homeDir,
		DatabasePath:      initResult.DatabasePath,
		WatchDirs:         []string{tempDir},
		SlackToken:        "test-token",
		SlackAPIBaseURL:   "https://slack.test/api",
		SlackHTTPClient:   client,
		SlackPollInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	waitForSlackEventOrRunError(t, initResult.DatabasePath, "slack.message", "C123", "1716215700.000400", done, transport)
	waitForSlackEvent(t, initResult.DatabasePath, "slack.message", "G456", "1716215800.000500")

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if transport.listRequests == 0 {
		t.Fatalf("expected Slack channel discovery")
	}
	if transport.historyRequests < 2 {
		t.Fatalf("expected history for discovered channels, got %d", transport.historyRequests)
	}

	publicEvent := slackEvent(t, initResult.DatabasePath, "slack.message", "C123", "1716215700.000400")
	if publicEvent.Project != "cupcake-api" {
		t.Fatalf("expected discovered public channel project, got %q", publicEvent.Project)
	}
	privateEvent := slackEvent(t, initResult.DatabasePath, "slack.message", "G456", "1716215800.000500")
	if privateEvent.Project != "exec-planning" {
		t.Fatalf("expected discovered private channel project, got %q", privateEvent.Project)
	}
}

func TestRunDiscoversSlackDMsOnlyWhenOptedIn(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	transport := &fakeSlackTransport{}
	client := &http.Client{Transport: transport}
	transport.handle = func(request *http.Request) string {
		if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("expected bearer token, got %q", got)
		}
		switch request.URL.Path {
		case "/api/conversations.list":
			transport.listRequests++
			if request.URL.Query().Get("types") != "public_channel,private_channel,im,mpim" {
				t.Fatalf("expected opt-in DM discovery, got %q", request.URL.Query().Get("types"))
			}
			return `{"ok":true,"channels":[{"id":"D123","name":"","is_private":true},{"id":"GDM456","name":"release-dm","is_private":true}]}`
		case "/api/conversations.history":
			transport.historyRequests++
			switch request.URL.Query().Get("channel") {
			case "D123":
				return `{"ok":true,"messages":[{"type":"message","user":"U456","text":"One to one context.","ts":"1716215900.000600"}]}`
			case "GDM456":
				return `{"ok":true,"messages":[{"type":"message","user":"U789","text":"Group DM context.","ts":"1716216000.000700"}]}`
			default:
				t.Fatalf("unexpected discovered DM %q", request.URL.Query().Get("channel"))
			}
		default:
			t.Fatalf("unexpected Slack API path %s", request.URL.Path)
		}
		return `{"ok":false,"error":"unexpected_request"}`
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:           homeDir,
		DatabasePath:      initResult.DatabasePath,
		WatchDirs:         []string{tempDir},
		SlackToken:        "test-token",
		SlackIncludeDMs:   true,
		SlackAPIBaseURL:   "https://slack.test/api",
		SlackHTTPClient:   client,
		SlackPollInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	waitForSlackEventOrRunError(t, initResult.DatabasePath, "slack.message", "D123", "1716215900.000600", done, transport)
	waitForSlackEvent(t, initResult.DatabasePath, "slack.message", "GDM456", "1716216000.000700")

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if transport.listRequests == 0 {
		t.Fatalf("expected Slack DM discovery")
	}
	if transport.historyRequests < 2 {
		t.Fatalf("expected history for discovered DMs, got %d", transport.historyRequests)
	}
}

func TestSlackConnectPrintsOAuthURLWithoutStoringToken(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	result, err := workgraph.ConnectSlack(workgraph.SlackConnectConfig{
		HomeDir:      homeDir,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURI:  workgraph.DefaultSlackRedirectURI,
		Channels:     []string{"C123"},
		State:        "fixed-state",
	})
	if err != nil {
		t.Fatalf("slack connect failed: %v", err)
	}
	if result.Configured {
		t.Fatalf("expected URL generation not to complete configuration")
	}
	if !strings.Contains(result.Message, "Slack OAuth authorization URL") {
		t.Fatalf("expected authorization guidance, got:\n%s", result.Message)
	}
	parsed, err := url.Parse(result.AuthorizationURL)
	if err != nil {
		t.Fatalf("parse authorization url: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "slack.com" || parsed.Path != "/oauth/v2/authorize" {
		t.Fatalf("expected Slack OAuth v2 authorize URL, got %s", result.AuthorizationURL)
	}
	query := parsed.Query()
	if query.Get("client_id") != "client-id" {
		t.Fatalf("expected client id in authorization URL, got %q", query.Get("client_id"))
	}
	if query.Get("redirect_uri") != workgraph.DefaultSlackRedirectURI {
		t.Fatalf("expected redirect URI in authorization URL, got %q", query.Get("redirect_uri"))
	}
	if query.Get("state") != "fixed-state" {
		t.Fatalf("expected state in authorization URL, got %q", query.Get("state"))
	}
	if query.Get("scope") != "" {
		t.Fatalf("expected no bot scopes in authorization URL, got %q", query.Get("scope"))
	}
	if !strings.Contains(query.Get("user_scope"), "channels:history") {
		t.Fatalf("expected user history scope in authorization URL, got %q", query.Get("user_scope"))
	}
	if _, err := os.Stat(filepath.Join(homeDir, "slack.json")); !os.IsNotExist(err) {
		t.Fatalf("expected slack config not to be written before code exchange, stat err: %v", err)
	}
}

func TestSlackConnectRequestsDMScopeOnlyWhenOptedIn(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	withoutDMs, err := workgraph.ConnectSlack(workgraph.SlackConnectConfig{
		HomeDir:      homeDir,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURI:  workgraph.DefaultSlackRedirectURI,
		State:        "fixed-state",
	})
	if err != nil {
		t.Fatalf("slack connect without DMs failed: %v", err)
	}
	withoutScopes := slackOAuthScopes(t, withoutDMs.AuthorizationURL)
	for _, disallowed := range []string{"im:history", "im:read", "mpim:history", "mpim:read"} {
		if strings.Contains(withoutScopes, disallowed) {
			t.Fatalf("expected DM scope %q not to be requested by default, got %q", disallowed, withoutScopes)
		}
	}

	withDMs, err := workgraph.ConnectSlack(workgraph.SlackConnectConfig{
		HomeDir:      homeDir,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURI:  workgraph.DefaultSlackRedirectURI,
		State:        "fixed-state",
		IncludeDMs:   true,
	})
	if err != nil {
		t.Fatalf("slack connect with DMs failed: %v", err)
	}
	withScopes := slackOAuthScopes(t, withDMs.AuthorizationURL)
	for _, expected := range []string{"im:history", "im:read", "mpim:history", "mpim:read"} {
		if !strings.Contains(withScopes, expected) {
			t.Fatalf("expected DM scope %q when opted in, got %q", expected, withScopes)
		}
	}
}

func TestSlackConnectDMEnabledMessageExplainsDisconnectBeforeRemovingDMScopes(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	transport := &fakeSlackTransport{}
	client := &http.Client{Transport: transport}
	transport.handle = func(request *http.Request) string {
		return `{"ok":true,"authed_user":{"id":"U123","access_token":"xoxp-installed","scope":"channels:history,channels:read,groups:history,groups:read,im:history,im:read,mpim:history,mpim:read"},"team":{"id":"T123","name":"Cupcake Labs"}}`
	}

	result, err := workgraph.ConnectSlack(workgraph.SlackConnectConfig{
		HomeDir:       homeDir,
		ClientID:      "client-id",
		ClientSecret:  "client-secret",
		RedirectURI:   workgraph.DefaultSlackRedirectURI,
		Code:          "oauth-code",
		State:         "fixed-state",
		ExpectedState: "fixed-state",
		IncludeDMs:    true,
		APIBaseURL:    "https://slack.test/api",
		HTTPClient:    client,
	})
	if err != nil {
		t.Fatalf("slack connect failed: %v", err)
	}
	if !strings.Contains(result.Message, "To remove Slack DM access later, run workgraph slack disconnect, then reconnect without --include-dms.") {
		t.Fatalf("expected disconnect guidance for DM-enabled connect, got:\n%s", result.Message)
	}
}

func TestSlackConnectWithoutDMsMessageExplainsReconnectPath(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	transport := &fakeSlackTransport{}
	client := &http.Client{Transport: transport}
	transport.handle = func(request *http.Request) string {
		return `{"ok":true,"authed_user":{"id":"U123","access_token":"xoxp-installed","scope":"channels:history,channels:read,groups:history,groups:read"},"team":{"id":"T123","name":"Cupcake Labs"}}`
	}

	result, err := workgraph.ConnectSlack(workgraph.SlackConnectConfig{
		HomeDir:       homeDir,
		ClientID:      "client-id",
		ClientSecret:  "client-secret",
		RedirectURI:   workgraph.DefaultSlackRedirectURI,
		Code:          "oauth-code",
		State:         "fixed-state",
		ExpectedState: "fixed-state",
		APIBaseURL:    "https://slack.test/api",
		HTTPClient:    client,
	})
	if err != nil {
		t.Fatalf("slack connect failed: %v", err)
	}
	if !strings.Contains(result.Message, "To include DMs later, run workgraph slack disconnect, then reconnect with --include-dms.") {
		t.Fatalf("expected reconnect guidance for channel-only connect, got:\n%s", result.Message)
	}
}

func TestSlackDisconnectRevokesTokenRemovesConfigAndRestartsDaemon(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	writeInitConfig(t, initResult.ConfigPath, initConfigFile{
		WatchDirs:   []string{watchDir},
		IgnorePaths: []string{homeDir},
		IgnoreNames: []string{".git", "node_modules"},
	})
	configPath := filepath.Join(homeDir, "slack.json")
	if err := os.WriteFile(configPath, []byte(`{
  "access_token": "xoxp-installed",
  "channels": [],
  "include_dms": true,
  "user_scopes": ["channels:history", "channels:read", "groups:history", "groups:read", "im:history", "im:read", "mpim:history", "mpim:read"],
  "api_base_url": "https://slack.test/api"
}
`), 0o600); err != nil {
		t.Fatalf("write slack config: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/auth.revoke" {
			t.Fatalf("unexpected Slack API path %s", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer xoxp-installed" {
			t.Fatalf("expected revoke bearer token, got %q", got)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":true,"revoked":true}`))
	}))
	defer server.Close()

	runWorkgraphCommand(t, nil, "run", "--home", homeDir, "--database", initResult.DatabasePath)
	defer runWorkgraphCommand(t, nil, "stop", "--home", homeDir)
	beforePID := readDaemonPID(t, homeDir)

	output := runWorkgraphCommand(t, nil, "slack", "disconnect", "--home", homeDir, "--slack-api-base", server.URL)
	if !strings.Contains(output, "Slack disconnected") {
		t.Fatalf("expected disconnect message, got:\n%s", output)
	}
	if !strings.Contains(output, "Slack token revoked") {
		t.Fatalf("expected revoke confirmation, got:\n%s", output)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("expected slack config removed, stat err: %v", err)
	}
	afterPID := readDaemonPID(t, homeDir)
	if afterPID == beforePID {
		t.Fatalf("expected daemon pid to change after Slack disconnect restart, still %s", afterPID)
	}
}

func TestSlackConnectExchangesCodeAndStoresConnectorConfig(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	transport := &fakeSlackTransport{}
	client := &http.Client{Transport: transport}
	transport.handle = func(request *http.Request) string {
		if request.URL.Path != "/api/oauth.v2.access" {
			t.Fatalf("unexpected Slack API path %s", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); !strings.HasPrefix(got, "Basic ") {
			t.Fatalf("expected OAuth exchange to use basic auth, got %q", got)
		}
		if err := request.ParseForm(); err != nil {
			t.Fatalf("parse OAuth form: %v", err)
		}
		if request.Form.Get("code") != "oauth-code" {
			t.Fatalf("expected code form value, got %q", request.Form.Get("code"))
		}
		if request.Form.Get("redirect_uri") != workgraph.DefaultSlackRedirectURI {
			t.Fatalf("expected redirect URI form value, got %q", request.Form.Get("redirect_uri"))
		}
		return `{"ok":true,"authed_user":{"id":"U123","access_token":"xoxp-installed","scope":"channels:history,channels:read,groups:history,groups:read"},"team":{"id":"T123","name":"Cupcake Labs"}}`
	}

	result, err := workgraph.ConnectSlack(workgraph.SlackConnectConfig{
		HomeDir:       homeDir,
		ClientID:      "client-id",
		ClientSecret:  "client-secret",
		RedirectURI:   workgraph.DefaultSlackRedirectURI,
		Code:          "oauth-code",
		State:         "fixed-state",
		ExpectedState: "fixed-state",
		Channels:      []string{"C123", "C456"},
		APIBaseURL:    "https://slack.test/api",
		HTTPClient:    client,
	})
	if err != nil {
		t.Fatalf("slack connect exchange failed: %v", err)
	}
	if !result.Configured {
		t.Fatalf("expected connect to complete configuration")
	}
	if !strings.Contains(result.Message, "Slack connected") {
		t.Fatalf("expected connected message, got:\n%s", result.Message)
	}
	if !strings.Contains(result.Message, "Collection: 2 explicit channel(s)") {
		t.Fatalf("expected connected message to describe explicit channel collection, got:\n%s", result.Message)
	}

	configPath := filepath.Join(homeDir, "slack.json")
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("expected slack config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected user-only slack config permissions, got %v", info.Mode().Perm())
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read slack config: %v", err)
	}
	var stored struct {
		AccessToken string   `json:"access_token"`
		TeamID      string   `json:"team_id"`
		TeamName    string   `json:"team_name"`
		Channels    []string `json:"channels"`
		UserScopes  []string `json:"user_scopes"`
		APIBaseURL  string   `json:"api_base_url"`
	}
	if err := json.Unmarshal(contents, &stored); err != nil {
		t.Fatalf("parse slack config: %v", err)
	}
	if stored.AccessToken != "xoxp-installed" || stored.TeamID != "T123" || stored.TeamName != "Cupcake Labs" {
		t.Fatalf("unexpected stored Slack config: %#v", stored)
	}
	if strings.Join(stored.Channels, ",") != "C123,C456" {
		t.Fatalf("expected stored channels, got %#v", stored.Channels)
	}
	if strings.Join(stored.UserScopes, ",") != "channels:history,channels:read,groups:history,groups:read" {
		t.Fatalf("expected stored OAuth user scopes, got %#v", stored.UserScopes)
	}
	if stored.APIBaseURL != "https://slack.test/api" {
		t.Fatalf("expected API base URL, got %q", stored.APIBaseURL)
	}
}

func TestSlackConnectReportsAutoDiscoveryInsteadOfZeroChannels(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	transport := &fakeSlackTransport{}
	client := &http.Client{Transport: transport}
	transport.handle = func(request *http.Request) string {
		return `{"ok":true,"authed_user":{"id":"U123","access_token":"xoxp-installed"},"team":{"id":"T123","name":"Cupcake Labs"}}`
	}

	result, err := workgraph.ConnectSlack(workgraph.SlackConnectConfig{
		HomeDir:       homeDir,
		ClientID:      "client-id",
		ClientSecret:  "client-secret",
		RedirectURI:   workgraph.DefaultSlackRedirectURI,
		Code:          "oauth-code",
		State:         "fixed-state",
		ExpectedState: "fixed-state",
		APIBaseURL:    "https://slack.test/api",
		HTTPClient:    client,
	})
	if err != nil {
		t.Fatalf("slack connect exchange failed: %v", err)
	}
	if strings.Contains(result.Message, "Channels: 0") {
		t.Fatalf("expected connected message not to report zero channels, got:\n%s", result.Message)
	}
	if !strings.Contains(result.Message, "Collection: auto-discover visible public and private channels") {
		t.Fatalf("expected auto-discovery collection message, got:\n%s", result.Message)
	}
}

func TestRunUsesStoredSlackConnectorConfig(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	configPath := filepath.Join(homeDir, "slack.json")
	if err := os.WriteFile(configPath, []byte(`{
  "access_token": "xoxb-stored",
  "channels": ["CSTORED"],
  "api_base_url": "https://slack.test/api"
}
`), 0o600); err != nil {
		t.Fatalf("write stored slack config: %v", err)
	}

	transport := &fakeSlackTransport{}
	client := &http.Client{Transport: transport}
	transport.handle = func(request *http.Request) string {
		if got := request.Header.Get("Authorization"); got != "Bearer xoxb-stored" {
			t.Fatalf("expected stored bearer token, got %q", got)
		}
		if request.URL.Query().Get("channel") != "CSTORED" {
			t.Fatalf("expected stored channel, got %q", request.URL.Query().Get("channel"))
		}
		if request.URL.Path != "/api/conversations.history" {
			t.Fatalf("unexpected Slack API path %s", request.URL.Path)
		}
		return `{"ok":true,"messages":[{"type":"message","user":"U456","text":"Stored config works.","ts":"1716215600.000300"}]}`
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:           homeDir,
		DatabasePath:      initResult.DatabasePath,
		WatchDirs:         []string{tempDir},
		SlackHTTPClient:   client,
		SlackPollInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	waitForSlackEvent(t, initResult.DatabasePath, "slack.message", "CSTORED", "1716215600.000300")
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestSlackRelayContractMatchesDefaultRedirect(t *testing.T) {
	if workgraph.DefaultSlackRedirectURI != "https://workgraph.pages.dev/slack/callback" {
		t.Fatalf("expected Cloudflare Pages Slack redirect, got %q", workgraph.DefaultSlackRedirectURI)
	}

	repoRoot := repoRoot(t)
	relayPath := filepath.Join(repoRoot, "public", "slack", "callback", "index.html")
	contents, err := os.ReadFile(relayPath)
	if err != nil {
		t.Fatalf("read Slack relay page: %v", err)
	}
	for _, expected := range []string{
		"http://localhost:2727/slack/callback",
		"window.location.search",
		"window.location.hash",
		"window.location.replace",
	} {
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("expected Slack relay page to include %q, got:\n%s", expected, contents)
		}
	}
}

type fakeSlackTransport struct {
	handle          func(*http.Request) string
	listRequests    int
	historyRequests int
	repliesRequests int
}

func (transport *fakeSlackTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body := transport.handle(request)
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}, nil
}

func readDaemonPID(t *testing.T, homeDir string) string {
	t.Helper()

	contents, err := os.ReadFile(filepath.Join(homeDir, "daemon.pid"))
	if err != nil {
		t.Fatalf("read daemon pid: %v", err)
	}
	return strings.TrimSpace(string(contents))
}

type storedSlackEvent struct {
	Project     string
	Actor       string
	Summary     string
	PayloadJSON string
}

func waitForSlackEvent(t *testing.T, dbPath, eventType, channelID, ts string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if slackEventExists(t, dbPath, eventType, channelID, ts) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for slack event %s %s %s", eventType, channelID, ts)
}

func waitForSlackEventOrRunError(t *testing.T, dbPath, eventType, channelID, ts string, done <-chan error, transport *fakeSlackTransport) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("run returned before slack event: %v", err)
		default:
		}
		if slackEventExists(t, dbPath, eventType, channelID, ts) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for slack event %s %s %s; list=%d history=%d replies=%d", eventType, channelID, ts, transport.listRequests, transport.historyRequests, transport.repliesRequests)
}

func slackEventExists(t *testing.T, dbPath, eventType, channelID, ts string) bool {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var count int
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM events
		WHERE source = 'slack'
			AND type = ?
			AND json_extract(payload_json, '$.channel_id') = ?
			AND json_extract(payload_json, '$.ts') = ?
	`, eventType, channelID, ts).Scan(&count)
	if err != nil {
		t.Fatalf("query slack event count: %v", err)
	}
	return count > 0
}

func slackOAuthScopes(t *testing.T, authorizationURL string) string {
	t.Helper()

	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatalf("parse authorization url: %v", err)
	}
	return parsed.Query().Get("user_scope")
}

func slackEvent(t *testing.T, dbPath, eventType, channelID, ts string) storedSlackEvent {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var event storedSlackEvent
	err = db.QueryRow(`
		SELECT project, actor, summary, payload_json
		FROM events
		WHERE source = 'slack'
			AND type = ?
			AND json_extract(payload_json, '$.channel_id') = ?
			AND json_extract(payload_json, '$.ts') = ?
	`, eventType, channelID, ts).Scan(&event.Project, &event.Actor, &event.Summary, &event.PayloadJSON)
	if err != nil {
		t.Fatalf("query slack event: %v", err)
	}
	return event
}

func slackEventCount(t *testing.T, dbPath string) int {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE source = 'slack'`).Scan(&count); err != nil {
		t.Fatalf("query slack event count: %v", err)
	}
	return count
}
