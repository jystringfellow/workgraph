package facts

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestAzureBoardsConnectOAuthUsesMicrosoftPKCEAndStoresConnectorConfig(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "azure", "boards", "connect",
		"--home", homeDir,
		"--no-browser",
		"--client-id", "azure-client-id",
		"--redirect-uri", "http://localhost:2727/azure/boards/callback",
		"--state", "fixed-state",
		"--organization", "acme",
		"--project", "Work",
		"--team", "Platform",
		"--area-path", `Work\Platform`,
	)
	if err != nil {
		t.Fatalf("workgraph azure boards connect URL failed: %v\n%s", err, output)
	}
	text := string(output)
	if !strings.Contains(text, "Azure Boards OAuth authorization URL") {
		t.Fatalf("expected authorization URL output, got:\n%s", output)
	}
	authURL := firstLineWithPrefix(text, "https://")
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	if parsed.Host != "login.microsoftonline.com" {
		t.Fatalf("expected Microsoft OAuth host, got %q", parsed.Host)
	}
	if parsed.Path != "/common/oauth2/v2.0/authorize" {
		t.Fatalf("expected Microsoft OAuth authorize path, got %q", parsed.Path)
	}
	query := parsed.Query()
	for key, expected := range map[string]string{
		"client_id":             "azure-client-id",
		"redirect_uri":          "http://localhost:2727/azure/boards/callback",
		"response_type":         "code",
		"state":                 "fixed-state",
		"code_challenge_method": "S256",
	} {
		if got := query.Get(key); got != expected {
			t.Fatalf("expected authorization query %s=%q, got %q in %s", key, expected, got, parsed.RawQuery)
		}
	}
	scope := query.Get("scope")
	for _, expectedScope := range []string{"offline_access", "499b84ac-1321-427f-aa17-267ca6975798/.default"} {
		if !strings.Contains(scope, expectedScope) {
			t.Fatalf("expected scope %q in %q", expectedScope, scope)
		}
	}
	if _, err := os.Stat(filepath.Join(homeDir, "azure-boards.json")); !os.IsNotExist(err) {
		t.Fatalf("expected connect without code not to store config yet, stat err=%v", err)
	}

	var tokenForm url.Values
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected token POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse token form: %v", err)
		}
		tokenForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"azure-access","refresh_token":"azure-refresh","token_type":"Bearer","expires_in":3600,"scope":"offline_access"}`))
	}))
	defer tokenServer.Close()

	output, err = runworkgraph(t, repoRoot, "azure", "boards", "connect",
		"--home", homeDir,
		"--no-browser",
		"--client-id", "azure-client-id",
		"--redirect-uri", "http://localhost:2727/azure/boards/callback",
		"--code", "returned-code",
		"--code-verifier", "pkce-verifier",
		"--state", "fixed-state",
		"--expected-state", "fixed-state",
		"--organization", "acme",
		"--project", "Work",
		"--team", "Platform",
		"--area-path", `Work\Platform`,
		"--azure-token-url", tokenServer.URL,
		"--azure-api-base", "https://dev.azure.test",
	)
	if err != nil {
		t.Fatalf("workgraph azure boards connect token failed: %v\n%s", err, output)
	}
	for key, expected := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "returned-code",
		"client_id":     "azure-client-id",
		"redirect_uri":  "http://localhost:2727/azure/boards/callback",
		"code_verifier": "pkce-verifier",
	} {
		if got := tokenForm.Get(key); got != expected {
			t.Fatalf("expected token form %s=%q, got %q", key, expected, got)
		}
	}

	contents, err := os.ReadFile(filepath.Join(homeDir, "azure-boards.json"))
	if err != nil {
		t.Fatalf("read azure boards config: %v", err)
	}
	var stored map[string]any
	if err := json.Unmarshal(contents, &stored); err != nil {
		t.Fatalf("parse azure boards config: %v", err)
	}
	for key, expected := range map[string]string{
		"access_token": "azure-access",
		"organization": "acme",
		"project":      "Work",
		"team":         "Platform",
		"api_base_url": "https://dev.azure.test",
	} {
		if got, _ := stored[key].(string); got != expected {
			t.Fatalf("expected stored %s=%q, got %q in %s", key, expected, got, contents)
		}
	}
	statusOutput, statusErr := runworkgraph(t, repoRoot, "connectors", "status", "--home", homeDir)
	if statusErr != nil {
		t.Fatalf("workgraph connectors status failed: %v\n%s", statusErr, statusOutput)
	}
	if !strings.Contains(string(statusOutput), "- azure.boards: setup ready, polling enabled") {
		t.Fatalf("expected Azure Boards connect to mark runtime setup ready, got:\n%s", statusOutput)
	}

	if err := os.WriteFile(filepath.Join(homeDir, "connectors.json"), []byte(`{
  "connectors": {
    "azure.boards": {
      "setup_state": "error",
      "last_validation_error": "last poll failed with invalid credentials; reconnect azure.boards",
      "last_poll_at": "2026-06-17T11:33:50Z",
      "last_error": "request Azure Boards WIQL: status 401: InvalidAuthenticationToken"
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write connector runtime config: %v", err)
	}
	output, err = runworkgraph(t, repoRoot, "azure", "boards", "connect", "--home", homeDir)
	if err != nil {
		t.Fatalf("expected already connected Azure Boards connect to succeed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Azure Boards is already connected") {
		t.Fatalf("expected already connected Azure Boards message, got:\n%s", output)
	}
	statusOutput, statusErr = runworkgraph(t, repoRoot, "connectors", "status", "--home", homeDir)
	if statusErr != nil {
		t.Fatalf("workgraph connectors status failed: %v\n%s", statusErr, statusOutput)
	}
	if !strings.Contains(string(statusOutput), "- azure.boards: setup ready, polling enabled") {
		t.Fatalf("expected already connected Azure Boards to repair runtime setup state, got:\n%s", statusOutput)
	}
	if strings.Contains(string(statusOutput), "InvalidAuthenticationToken") || strings.Contains(string(statusOutput), "validation error") {
		t.Fatalf("expected already connected Azure Boards to clear stale errors, got:\n%s", statusOutput)
	}

	output, err = runworkgraph(t, repoRoot, "azure", "boards", "disconnect", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph azure boards disconnect failed: %v\n%s", err, output)
	}
	statusOutput, statusErr = runworkgraph(t, repoRoot, "connectors", "status", "--home", homeDir)
	if statusErr != nil {
		t.Fatalf("workgraph connectors status failed: %v\n%s", statusErr, statusOutput)
	}
	if !strings.Contains(string(statusOutput), "- azure.boards: setup not connected, polling not ready") {
		t.Fatalf("expected Azure Boards disconnect to clear runtime setup state, got:\n%s", statusOutput)
	}
}

func TestAzureBoardsCaptureStoresWorkItems(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	var wiqlBody string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer azure-token" {
			t.Fatalf("expected bearer token, got %q", got)
		}
		switch r.URL.Path {
		case "/acme/Work/_apis/wit/wiql":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read WIQL body: %v", err)
			}
			wiqlBody = string(body)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"workItems":[{"id":123}]}`))
		case "/acme/Work/_apis/wit/workitemsbatch":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"value":[{"id":123,"url":"https://dev.azure.com/acme/Work/_workitems/edit/123","fields":{"System.Title":"Ship workgraph at work","System.State":"Active","System.WorkItemType":"User Story","System.AssignedTo":{"displayName":"Craig Stringfellow"},"System.ChangedDate":"2026-06-08T12:00:00Z","System.Tags":"workgraph;ai","System.IterationPath":"Work\\Sprint 1","System.AreaPath":"Work\\Platform","Microsoft.VSTS.Common.Priority":1}}]}`))
		default:
			t.Fatalf("unexpected Azure Boards path %s", r.URL.Path)
		}
	}))
	defer api.Close()

	output, err := runworkgraph(t, repoRoot, "azure", "boards", "capture",
		"--home", homeDir,
		"--token", "azure-token",
		"--organization", "acme",
		"--project", "Work",
		"--team", "Platform",
		"--area-path", `Work\Platform`,
		"--area-path", `Work\DevEx`,
		"--azure-api-base", api.URL,
	)
	if err != nil {
		t.Fatalf("workgraph azure boards capture failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Events stored: 1") {
		t.Fatalf("expected stored count, got:\n%s", output)
	}
	if !strings.Contains(wiqlBody, `[System.AreaPath] UNDER 'Work\\Platform' OR [System.AreaPath] UNDER 'Work\\DevEx'`) {
		t.Fatalf("expected area paths to be ORed in WIQL, got %s", wiqlBody)
	}

	event := azureBoardsEvent(t, filepath.Join(homeDir, "workgraph.db"), 123)
	if event.Project != "Work" {
		t.Fatalf("expected project Work, got %q", event.Project)
	}
	if event.Actor != "Craig Stringfellow" {
		t.Fatalf("expected actor from assigned identity, got %q", event.Actor)
	}
	if event.Summary != "Ship workgraph at work" {
		t.Fatalf("expected work item title summary, got %q", event.Summary)
	}
	for _, expected := range []string{
		`"organization":"acme"`,
		`"project":"Work"`,
		`"team":"Platform"`,
		`"System.Title":"Ship workgraph at work"`,
		`"System.Tags":"workgraph;ai"`,
		`"url":"https://dev.azure.com/acme/Work/_workitems/edit/123"`,
	} {
		if !strings.Contains(event.PayloadJSON, expected) {
			t.Fatalf("expected payload to include %s, got %s", expected, event.PayloadJSON)
		}
	}
}

func firstLineWithPrefix(text string, prefix string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

func azureBoardsEvent(t *testing.T, dbPath string, id int) storedSlackEvent {
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
		WHERE source = 'azure_boards'
			AND type = 'azure_boards.work_item'
			AND id = ?
	`, fmt.Sprintf("azure_boards.work_item:%d", id)).Scan(&event.Project, &event.Actor, &event.Summary, &event.PayloadJSON)
	if err != nil {
		t.Fatalf("query azure boards event %d: %v", id, err)
	}
	return event
}
