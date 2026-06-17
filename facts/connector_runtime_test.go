package facts

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
	_ "github.com/mattn/go-sqlite3"
)

func TestRunPollsConnectedCalendarMailAndNotion(t *testing.T) {
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

	calendarServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/calendar/v3/calendars/primary/events" {
			t.Fatalf("unexpected calendar path %s", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer calendar-token" {
			t.Fatalf("expected calendar bearer token, got %q", got)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"items":[{"id":"calendar-runtime-event","summary":"Runtime calendar event","start":{"dateTime":"2026-06-07T09:00:00Z"},"end":{"dateTime":"2026-06-07T09:30:00Z"},"organizer":{"displayName":"Ada Lovelace"},"status":"confirmed"}]}`))
	}))
	defer calendarServer.Close()
	if err := os.WriteFile(filepath.Join(homeDir, "calendar.json"), []byte(fmt.Sprintf(`{
  "google": {
    "access_token": "calendar-token",
    "calendar_ids": ["primary"],
    "api_base_url": %q
  }
}
`, calendarServer.URL)), 0o600); err != nil {
		t.Fatalf("write calendar config: %v", err)
	}

	mailServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer mail-token" {
			t.Fatalf("expected mail bearer token, got %q", got)
		}
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/gmail/v1/users/me/messages":
			_, _ = response.Write([]byte(`{"messages":[{"id":"runtime-mail-message","threadId":"runtime-thread"}]}`))
		case "/gmail/v1/users/me/messages/runtime-mail-message":
			_, _ = response.Write([]byte(`{
  "id": "runtime-mail-message",
  "threadId": "runtime-thread",
  "internalDate": "1780848000000",
  "snippet": "Runtime mail snippet",
  "payload": {
    "headers": [
      {"name": "Subject", "value": "Runtime mail message"},
      {"name": "From", "value": "Ada Lovelace <ada@example.test>"},
      {"name": "To", "value": "Stringfellow <stringfellow@example.test>"},
      {"name": "Date", "value": "Sun, 07 Jun 2026 09:00:00 +0000"}
    ],
    "body": {"data": "UnVudGltZSBib2R5"}
  }
}`))
		default:
			t.Fatalf("unexpected mail path %s", request.URL.Path)
		}
	}))
	defer mailServer.Close()
	if err := os.WriteFile(filepath.Join(homeDir, "mail.json"), []byte(fmt.Sprintf(`{
  "google": {
    "access_token": "mail-token",
    "api_base_url": %q
  }
}
`, mailServer.URL)), 0o600); err != nil {
		t.Fatalf("write mail config: %v", err)
	}

	notionServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/search" {
			t.Fatalf("unexpected notion path %s", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer notion-token" {
			t.Fatalf("expected notion bearer token, got %q", got)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"object":"list","results":[{"object":"page","id":"runtime-notion-page","created_time":"2026-06-07T08:00:00.000Z","last_edited_time":"2026-06-07T09:00:00.000Z","url":"https://notion.test/runtime","properties":{"title":{"type":"title","title":[{"plain_text":"Runtime Notion page"}]}}}],"has_more":false}`))
	}))
	defer notionServer.Close()
	if err := os.WriteFile(filepath.Join(homeDir, "notion.json"), []byte(fmt.Sprintf(`{
  "access_token": "notion-token",
  "api_base_url": %q
}
`, notionServer.URL)), 0o600); err != nil {
		t.Fatalf("write notion config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:              homeDir,
		DatabasePath:         initResult.DatabasePath,
		WatchDirs:            []string{watchDir},
		CalendarPollInterval: 10 * time.Millisecond,
		MailPollInterval:     10 * time.Millisecond,
		NotionPollInterval:   10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	if !strings.Contains(capture.Status.Message, "Monitoring:") ||
		!strings.Contains(capture.Status.Message, "calendar.google") ||
		!strings.Contains(capture.Status.Message, "mail.google") ||
		!strings.Contains(capture.Status.Message, "notion") {
		t.Fatalf("expected start message to report monitored connectors, got:\n%s", capture.Status.Message)
	}
	done := make(chan error, 1)
	go func() {
		done <- capture.Run(ctx)
	}()

	waitForEventTypeSummary(t, initResult.DatabasePath, "calendar.event", "Runtime calendar event")
	waitForEventTypeSummary(t, initResult.DatabasePath, "mail.message", "Runtime mail message")
	waitForEventTypeSummary(t, initResult.DatabasePath, "notion.page", "Runtime Notion page")

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("capture run failed: %v", err)
	}
}

func TestRunSkipsConnectorWithSetupError(t *testing.T) {
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

	var requests int
	notionServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		http.Error(response, "notion should not be polled", http.StatusInternalServerError)
	}))
	defer notionServer.Close()
	if err := os.WriteFile(filepath.Join(homeDir, "notion.json"), []byte(fmt.Sprintf(`{
  "access_token": "notion-token",
  "api_base_url": %q
}
`, notionServer.URL)), 0o600); err != nil {
		t.Fatalf("write notion config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "connectors.json"), []byte(`{
  "connectors": {
    "notion": {
      "enabled": true,
      "setup_state": "error",
      "last_validation_error": "reconnect notion"
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write connector runtime config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:            homeDir,
		DatabasePath:       initResult.DatabasePath,
		WatchDirs:          []string{watchDir},
		NotionPollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	if strings.Contains(capture.Status.Message, "notion") {
		t.Fatalf("expected setup-error Notion not to be monitored, got:\n%s", capture.Status.Message)
	}
	done := make(chan error, 1)
	go func() {
		done <- capture.Run(ctx)
	}()
	time.Sleep(40 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("capture run failed: %v", err)
	}
	if requests != 0 {
		t.Fatalf("expected setup-error Notion not to be polled, got %d requests", requests)
	}
}

func TestRunPollsConnectedSlackListsAndAzureBoards(t *testing.T) {
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

	slackServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/slackLists.items.list" {
			t.Fatalf("unexpected Slack Lists path %s", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer slack-token" {
			t.Fatalf("expected Slack bearer token, got %q", got)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":true,"items":[{"id":"runtime-list-item","list_id":"LTODO","updated_timestamp":"1780938000.000000","updated_by":"U123","fields":[{"key":"task","text":"Runtime Slack List task","value":"Runtime Slack List task"}]}]}`))
	}))
	defer slackServer.Close()
	if err := os.WriteFile(filepath.Join(homeDir, "slack.json"), []byte(fmt.Sprintf(`{
  "access_token": "slack-token",
  "channels": [],
  "list_ids": ["LTODO"],
  "api_base_url": %q
}
`, slackServer.URL+"/api")), 0o600); err != nil {
		t.Fatalf("write slack config: %v", err)
	}

	azureServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer azure-token" {
			t.Fatalf("expected Azure bearer token, got %q", got)
		}
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/acme/Work/_apis/wit/wiql":
			_, _ = response.Write([]byte(`{"workItems":[{"id":456}]}`))
		case "/acme/Work/_apis/wit/workitemsbatch":
			_, _ = response.Write([]byte(`{"value":[{"id":456,"url":"https://dev.azure.com/acme/Work/_workitems/edit/456","fields":{"System.Title":"Runtime Azure Boards item","System.State":"Active","System.WorkItemType":"Task","System.AssignedTo":{"displayName":"Craig Stringfellow"},"System.ChangedDate":"2026-06-08T13:00:00Z","System.AreaPath":"Work\\Platform"}}]}`))
		default:
			t.Fatalf("unexpected Azure Boards path %s", request.URL.Path)
		}
	}))
	defer azureServer.Close()
	if err := os.WriteFile(filepath.Join(homeDir, "azure-boards.json"), []byte(fmt.Sprintf(`{
  "access_token": "azure-token",
  "organization": "acme",
  "project": "Work",
  "team": "Platform",
  "area_paths": ["Work\\Platform"],
  "api_base_url": %q
}
`, azureServer.URL)), 0o600); err != nil {
		t.Fatalf("write azure boards config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:                 homeDir,
		DatabasePath:            initResult.DatabasePath,
		WatchDirs:               []string{watchDir},
		SlackListPollInterval:   10 * time.Millisecond,
		AzureBoardsPollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	if !strings.Contains(capture.Status.Message, "slack.lists") ||
		!strings.Contains(capture.Status.Message, "azure.boards") {
		t.Fatalf("expected start message to report Slack Lists and Azure Boards, got:\n%s", capture.Status.Message)
	}
	done := make(chan error, 1)
	go func() {
		done <- capture.Run(ctx)
	}()

	waitForEventTypeSummary(t, initResult.DatabasePath, "slack.list_item", "Runtime Slack List task")
	waitForEventTypeSummary(t, initResult.DatabasePath, "azure_boards.work_item", "Runtime Azure Boards item")

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("capture run failed: %v", err)
	}
}

func TestConnectorsListAndUpdatePollingSettings(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "notion.json"), []byte(`{"access_token":"notion-token"}`), 0o600); err != nil {
		t.Fatalf("write notion config: %v", err)
	}

	output, err := runworkgraph(t, repoRoot, "connectors", "list", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph connectors list failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "- notion: connected, enabled") {
		t.Fatalf("expected connected enabled Notion connector, got:\n%s", output)
	}

	output, err = runworkgraph(t, repoRoot, "connectors", "disable", "--home", homeDir, "notion")
	if err != nil {
		t.Fatalf("workgraph connectors disable failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Connector notion disabled") {
		t.Fatalf("expected disable output, got:\n%s", output)
	}

	output, err = runworkgraph(t, repoRoot, "connectors", "interval", "--home", homeDir, "notion", "30m")
	if err != nil {
		t.Fatalf("workgraph connectors interval failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Connector notion interval: 30m0s") {
		t.Fatalf("expected interval output, got:\n%s", output)
	}

	output, err = runworkgraph(t, repoRoot, "connectors", "list", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph connectors list failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "- notion: connected, disabled, interval 30m0s") {
		t.Fatalf("expected disabled Notion connector with interval, got:\n%s", output)
	}
}

func TestConnectorsListShowsPollDetails(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "connectors.json"), []byte(`{
  "connectors": {
    "notion": {
      "enabled": true,
      "interval": "30m",
      "last_poll_at": "2026-06-09T10:00:00Z",
      "last_error": "rate limited"
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write connector runtime config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "notion.json"), []byte(`{"access_token":"notion-token"}`), 0o600); err != nil {
		t.Fatalf("write notion config: %v", err)
	}

	output, err := runworkgraph(t, repoRoot, "connectors", "list", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph connectors list failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"- notion: connected, enabled, interval 30m0s",
		"last poll 2026-06-09T10:00:00Z",
		"last error rate limited",
		"next poll 2026-06-09T10:30:00Z",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected connector poll detail %q, got:\n%s", expected, output)
		}
	}
}

func TestConnectorsStatusShowsSetupAndPollState(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "connectors.json"), []byte(`{
  "connectors": {
    "github": {
      "enabled": true,
      "interval": "5m",
      "setup_state": "ready",
      "last_validated_at": "2026-06-10T09:00:00Z",
      "last_poll_at": "2026-06-10T09:05:00Z"
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write connector runtime config: %v", err)
	}

	output, err := runworkgraph(t, repoRoot, "connectors", "status", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph connectors status failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"Connector status",
		"- github: setup ready",
		"last validated 2026-06-10T09:00:00Z",
		"polling enabled",
		"last poll 2026-06-10T09:05:00Z",
		"next poll 2026-06-10T09:10:00Z",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected connector status detail %q, got:\n%s", expected, output)
		}
	}
}

func TestConnectorsStatusDerivesClearSetupState(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "notion.json"), []byte(`{"access_token":"notion-token"}`), 0o600); err != nil {
		t.Fatalf("write notion config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "calendar.json"), []byte(`{
  "microsoft": {
    "access_token": "microsoft-calendar-token",
    "calendar_ids": ["primary"]
  }
}`), 0o600); err != nil {
		t.Fatalf("write calendar config: %v", err)
	}

	output, err := runworkgraph(t, repoRoot, "connectors", "status", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph connectors status failed: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "setup unknown") {
		t.Fatalf("expected status not to show setup unknown, got:\n%s", output)
	}
	for _, expected := range []string{
		"- calendar.microsoft: setup not supported, polling not ready",
		"- notion: setup ready",
		"- azure.boards: setup not connected, polling not ready",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected connector status detail %q, got:\n%s", expected, output)
		}
	}
}

func TestConnectorsDoctorReportsLegacySetupStateAndAuthErrors(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "notion.json"), []byte(`{"access_token":"notion-token"}`), 0o600); err != nil {
		t.Fatalf("write notion config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "calendar.json"), []byte(`{
  "microsoft": {
    "access_token": "microsoft-calendar-token",
    "calendar_ids": ["primary"]
  }
}`), 0o600); err != nil {
		t.Fatalf("write calendar config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "connectors.json"), []byte(`{
  "connectors": {
    "mail.google": {
      "last_poll_at": "2026-06-17T11:33:50Z",
      "last_error": "request Gmail messages: status 401: Invalid Credentials"
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write connector runtime config: %v", err)
	}

	output, err := runworkgraph(t, repoRoot, "connectors", "doctor", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph connectors doctor failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"Connector health",
		"- calendar.microsoft: not supported, polling is not implemented yet",
		"- github: not validated, run workgraph github connect",
		"- notion: needs upgrade, setup state missing for existing local config",
		"- mail.google: needs reconnect, last poll failed with invalid credentials",
		"Run: workgraph connectors upgrade",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected connector doctor detail %q, got:\n%s", expected, output)
		}
	}
}

func TestConnectorsUpgradeNormalizesLegacySetupState(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "notion.json"), []byte(`{"access_token":"notion-token"}`), 0o600); err != nil {
		t.Fatalf("write notion config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "connectors.json"), []byte(`{
  "connectors": {
    "mail.google": {
      "last_poll_at": "2026-06-17T11:33:50Z",
      "last_error": "request Gmail messages: status 401: Invalid Credentials"
    },
    "notion": {
      "enabled": false,
      "interval": "30m"
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write connector runtime config: %v", err)
	}

	output, err := runworkgraph(t, repoRoot, "connectors", "upgrade", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph connectors upgrade failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"Connector settings upgraded",
		"- mail.google: marked error; reconnect required",
		"- notion: marked ready from existing local config",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected connector upgrade detail %q, got:\n%s", expected, output)
		}
	}

	statusOutput, statusErr := runworkgraph(t, repoRoot, "connectors", "status", "--home", homeDir)
	if statusErr != nil {
		t.Fatalf("workgraph connectors status failed: %v\n%s", statusErr, statusOutput)
	}
	for _, expected := range []string{
		"- mail.google: setup error, polling not ready",
		"validation error last poll failed with invalid credentials; reconnect mail.google",
		"- notion: setup ready, polling disabled, interval 30m0s",
	} {
		if !strings.Contains(string(statusOutput), expected) {
			t.Fatalf("expected connector status detail %q, got:\n%s", expected, statusOutput)
		}
	}
}

func TestConnectorsPollOnceCapturesReadyConnector(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	notionServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/search" {
			t.Fatalf("unexpected notion path %s", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer notion-token" {
			t.Fatalf("expected notion bearer token, got %q", got)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"object":"list","results":[{"object":"page","id":"poll-once-notion-page","created_time":"2026-06-07T08:00:00.000Z","last_edited_time":"2026-06-07T09:00:00.000Z","url":"https://notion.test/poll-once","properties":{"title":{"type":"title","title":[{"plain_text":"Poll once Notion page"}]}}}],"has_more":false}`))
	}))
	defer notionServer.Close()
	if err := os.WriteFile(filepath.Join(homeDir, "notion.json"), []byte(fmt.Sprintf(`{
  "access_token": "notion-token",
  "api_base_url": %q
}
`, notionServer.URL)), 0o600); err != nil {
		t.Fatalf("write notion config: %v", err)
	}

	output, err := runworkgraph(t, repoRoot, "connectors", "poll", "--once", "--connector", "notion", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph connectors poll failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Connector poll complete") || !strings.Contains(string(output), "notion: ok") {
		t.Fatalf("expected poll output, got:\n%s", output)
	}
	waitForEventTypeSummary(t, filepath.Join(homeDir, "workgraph.db"), "notion.page", "Poll once Notion page")

	statusOutput, err := runworkgraph(t, repoRoot, "connectors", "status", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph connectors status failed: %v\n%s", err, statusOutput)
	}
	if !strings.Contains(string(statusOutput), "- notion:") || !strings.Contains(string(statusOutput), "last poll ") {
		t.Fatalf("expected status to include Notion last poll, got:\n%s", statusOutput)
	}
}

func waitForEventTypeSummary(t *testing.T, dbPath, eventType, summary string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if eventWithSummaryExists(t, dbPath, eventType, summary) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s summary %q", eventType, summary)
}

func eventWithSummaryExists(t *testing.T, dbPath, eventType, summary string) bool {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = ? AND COALESCE(summary, '') LIKE ?`, eventType, "%"+summary+"%").Scan(&count); err != nil {
		t.Fatalf("query events: %v", err)
	}
	return count > 0
}
