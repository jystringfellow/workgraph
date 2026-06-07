package facts

import (
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

	_ "github.com/mattn/go-sqlite3"
)

func TestNotionTokenRelayDocumentsSecretsAndLocalDev(t *testing.T) {
	repoRoot := repoRoot(t)

	readme, err := os.ReadFile(filepath.Join(repoRoot, "workers/notion-oauth-token/README.md"))
	if err != nil {
		t.Fatalf("read Notion token relay README: %v", err)
	}
	for _, expected := range []string{
		".dev.vars",
		".dev.vars.example",
		"NOTION_CLIENT_SECRET",
		"wrangler dev",
		"wrangler secret put NOTION_CLIENT_SECRET",
		"https://workgraph-notion-oauth-token.jystringfellow.workers.dev/notion/token",
	} {
		if !strings.Contains(string(readme), expected) {
			t.Fatalf("expected Notion token relay README to document %q, got:\n%s", expected, readme)
		}
	}

	example, err := os.ReadFile(filepath.Join(repoRoot, "workers/notion-oauth-token/.dev.vars.example"))
	if err != nil {
		t.Fatalf("read Notion token relay .dev.vars example: %v", err)
	}
	if !strings.Contains(string(example), "NOTION_CLIENT_SECRET=") {
		t.Fatalf("expected .dev.vars example to include NOTION_CLIENT_SECRET, got:\n%s", example)
	}
}

func TestNotionCaptureStoresSharedPagesAndDatabases(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	var requests int
	var gotAuth string
	var gotVersion string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if request.Method != http.MethodPost || request.URL.Path != "/v1/search" {
			t.Fatalf("expected Notion search request, got %s %s", request.Method, request.URL.Path)
		}
		gotAuth = request.Header.Get("Authorization")
		gotVersion = request.Header.Get("Notion-Version")
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "object": "list",
  "results": [
    {
      "object": "page",
      "id": "page-1",
      "created_time": "2026-06-07T15:00:00.000Z",
      "last_edited_time": "2026-06-07T16:00:00.000Z",
      "url": "https://www.notion.so/page-1",
      "parent": {"type": "workspace", "workspace": true},
      "properties": {
        "title": {
          "id": "title",
          "type": "title",
          "title": [{"plain_text": "Launch plan"}]
        }
      }
    },
    {
      "object": "database",
      "id": "database-1",
      "created_time": "2026-06-07T14:00:00.000Z",
      "last_edited_time": "2026-06-07T14:30:00.000Z",
      "url": "https://www.notion.so/database-1",
      "title": [{"plain_text": "Project docs"}]
    }
  ],
  "next_cursor": null,
  "has_more": false
}`))
	}))
	defer server.Close()

	output, err := runworkgraph(t, repoRoot, "notion", "capture",
		"--home", homeDir,
		"--token", "notion-token",
		"--notion-api-base", server.URL,
	)
	if err != nil {
		t.Fatalf("workgraph notion capture failed: %v\n%s", err, output)
	}
	if requests != 1 {
		t.Fatalf("expected one search request, got %d", requests)
	}
	if gotAuth != "Bearer notion-token" {
		t.Fatalf("expected bearer auth, got %q", gotAuth)
	}
	if gotVersion == "" {
		t.Fatalf("expected Notion-Version header")
	}
	if !strings.Contains(string(output), "Notion capture complete") || !strings.Contains(string(output), "Events stored: 2") {
		t.Fatalf("expected Notion capture summary, got:\n%s", output)
	}

	page := notionEvent(t, filepath.Join(homeDir, "workgraph.db"), "notion.page", "page-1")
	if page.Timestamp != "2026-06-07T16:00:00Z" {
		t.Fatalf("expected page timestamp from last_edited_time, got %q", page.Timestamp)
	}
	if page.Summary != "Launch plan" {
		t.Fatalf("expected page title summary, got %q", page.Summary)
	}
	for _, expected := range []string{
		`"object":"page"`,
		`"id":"page-1"`,
		`"title":"Launch plan"`,
		`"url":"https://www.notion.so/page-1"`,
	} {
		if !strings.Contains(page.PayloadJSON, expected) {
			t.Fatalf("expected page payload to include %s, got %s", expected, page.PayloadJSON)
		}
	}

	database := notionEvent(t, filepath.Join(homeDir, "workgraph.db"), "notion.database", "database-1")
	if database.Summary != "Project docs" {
		t.Fatalf("expected database title summary, got %q", database.Summary)
	}
	if !strings.Contains(database.PayloadJSON, `"object":"database"`) {
		t.Fatalf("expected database payload, got %s", database.PayloadJSON)
	}
}

func TestNotionConnectOAuthStoresConnectorConfig(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "notion", "connect",
		"--home", homeDir,
		"--no-browser",
		"--redirect-uri", "http://localhost:2727/notion/callback",
		"--state", "fixed-state",
	)
	if err != nil {
		t.Fatalf("workgraph notion connect URL failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Notion OAuth authorization URL") {
		t.Fatalf("expected authorization guidance, got:\n%s", output)
	}
	authorizationURL := notionAuthorizationURL(t, string(output))
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatalf("parse authorization url: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "api.notion.com" || parsed.Path != "/v1/oauth/authorize" {
		t.Fatalf("expected Notion OAuth authorize URL, got %s", authorizationURL)
	}
	query := parsed.Query()
	if query.Get("client_id") != "378d872b-594c-8110-b4c0-0037422697b3" {
		t.Fatalf("expected default Notion client id in authorization URL, got %q", query.Get("client_id"))
	}
	if query.Get("redirect_uri") != "http://localhost:2727/notion/callback" {
		t.Fatalf("expected redirect URI in authorization URL, got %q", query.Get("redirect_uri"))
	}
	if query.Get("response_type") != "code" || query.Get("owner") != "user" {
		t.Fatalf("expected Notion code/user authorization URL, got response_type=%q owner=%q", query.Get("response_type"), query.Get("owner"))
	}
	if query.Get("state") != "fixed-state" {
		t.Fatalf("expected state in authorization URL, got %q", query.Get("state"))
	}
	if query.Get("code_challenge") != "" || query.Get("code_challenge_method") != "" {
		t.Fatalf("expected no PKCE for Notion authorization URL, got %q / %q", query.Get("code_challenge"), query.Get("code_challenge_method"))
	}
	if _, err := os.Stat(filepath.Join(homeDir, "notion.json")); !os.IsNotExist(err) {
		t.Fatalf("expected notion config not to be written before code exchange, stat err: %v", err)
	}

	var tokenRequestForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/notion/token" {
			t.Fatalf("unexpected token server path %s", request.URL.Path)
		}
		if err := request.ParseForm(); err != nil {
			t.Fatalf("parse token request form: %v", err)
		}
		tokenRequestForm = request.Form
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "access_token": "notion-access-token",
  "refresh_token": "notion-refresh-token",
  "token_type": "bearer",
  "expires_in": 3600,
  "workspace_id": "workspace-id",
  "workspace_name": "Workspace",
  "bot_id": "bot-id"
}`))
	}))
	defer server.Close()

	output, err = runworkgraph(t, repoRoot, "notion", "connect",
		"--home", homeDir,
		"--redirect-uri", "http://localhost:2727/notion/callback",
		"--code", "oauth-code",
		"--state", "fixed-state",
		"--expected-state", "fixed-state",
		"--notion-token-url", server.URL+"/notion/token",
		"--notion-api-base", "https://api.notion.test",
	)
	if err != nil {
		t.Fatalf("workgraph notion connect exchange failed: %v\n%s", err, output)
	}
	if tokenRequestForm.Get("grant_type") != "authorization_code" {
		t.Fatalf("expected authorization_code grant, got %q", tokenRequestForm.Get("grant_type"))
	}
	if tokenRequestForm.Get("code") != "oauth-code" {
		t.Fatalf("expected oauth code in token request, got %q", tokenRequestForm.Get("code"))
	}
	if tokenRequestForm.Get("client_id") != "378d872b-594c-8110-b4c0-0037422697b3" {
		t.Fatalf("expected default Notion client id in token request, got %#v", tokenRequestForm)
	}
	if tokenRequestForm.Get("redirect_uri") != "http://localhost:2727/notion/callback" {
		t.Fatalf("expected redirect URI in token request, got %#v", tokenRequestForm)
	}
	if _, ok := tokenRequestForm["code_verifier"]; ok {
		t.Fatalf("expected no PKCE verifier in Notion token request, got %#v", tokenRequestForm["code_verifier"])
	}
	if _, ok := tokenRequestForm["client_secret"]; ok {
		t.Fatalf("expected no client_secret field in Notion token request, got %#v", tokenRequestForm["client_secret"])
	}
	if !strings.Contains(string(output), "Notion connected") {
		t.Fatalf("expected connected message, got:\n%s", output)
	}

	configPath := filepath.Join(homeDir, "notion.json")
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("expected notion config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected notion config permissions 0600, got %v", got)
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read notion config: %v", err)
	}
	var stored struct {
		AccessToken   string `json:"access_token"`
		RefreshToken  string `json:"refresh_token"`
		TokenType     string `json:"token_type"`
		ExpiresAt     string `json:"expires_at"`
		WorkspaceID   string `json:"workspace_id"`
		WorkspaceName string `json:"workspace_name"`
		BotID         string `json:"bot_id"`
		APIBaseURL    string `json:"api_base_url"`
		ClientID      string `json:"client_id"`
		TokenURL      string `json:"token_url"`
	}
	if err := json.Unmarshal(contents, &stored); err != nil {
		t.Fatalf("parse notion config: %v", err)
	}
	if stored.AccessToken != "notion-access-token" || stored.RefreshToken != "notion-refresh-token" || stored.TokenType != "bearer" {
		t.Fatalf("expected stored Notion tokens, got %#v", stored)
	}
	if stored.WorkspaceID != "workspace-id" || stored.WorkspaceName != "Workspace" || stored.BotID != "bot-id" {
		t.Fatalf("expected stored Notion workspace metadata, got %#v", stored)
	}
	if stored.ExpiresAt == "" {
		t.Fatalf("expected stored Notion token expiry, got %#v", stored)
	}
	if stored.APIBaseURL != "https://api.notion.test" {
		t.Fatalf("expected stored API base URL, got %q", stored.APIBaseURL)
	}
	if stored.ClientID != "378d872b-594c-8110-b4c0-0037422697b3" || stored.TokenURL != server.URL+"/notion/token" {
		t.Fatalf("expected stored OAuth metadata, got %#v", stored)
	}

	output, err = runworkgraph(t, repoRoot, "notion", "connect",
		"--home", homeDir,
	)
	if err != nil {
		t.Fatalf("expected already connected Notion connect to succeed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Notion is already connected") {
		t.Fatalf("expected already connected message, got:\n%s", output)
	}

	output, err = runworkgraph(t, repoRoot, "notion", "disconnect",
		"--home", homeDir,
	)
	if err != nil {
		t.Fatalf("workgraph notion disconnect failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Notion disconnected") || !strings.Contains(string(output), "Notion workspace connection settings") {
		t.Fatalf("expected Notion disconnect guidance, got:\n%s", output)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("expected notion config removed after disconnect, stat err: %v", err)
	}

	output, err = runworkgraph(t, repoRoot, "notion", "disconnect",
		"--home", homeDir,
	)
	if err != nil {
		t.Fatalf("expected already disconnected Notion disconnect to succeed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Notion is not connected") {
		t.Fatalf("expected already disconnected message, got:\n%s", output)
	}
}

type storedNotionEvent struct {
	Timestamp   string
	Summary     string
	PayloadJSON string
}

func notionEvent(t *testing.T, dbPath, eventType, objectID string) storedNotionEvent {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	id := fmt.Sprintf("%s:%s", eventType, objectID)
	var event storedNotionEvent
	err = db.QueryRow(`SELECT timestamp, summary, payload_json FROM events WHERE id = ?`, id).Scan(&event.Timestamp, &event.Summary, &event.PayloadJSON)
	if err != nil {
		t.Fatalf("read Notion event %s: %v", id, err)
	}
	return event
}

func notionAuthorizationURL(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			return line
		}
	}
	t.Fatalf("expected authorization URL in output:\n%s", output)
	return ""
}

func TestNotionTokenRelayExchangesAndRefreshesTokens(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(repoRoot(t), "workers/notion-oauth-token/src/index.ts"))
	if err != nil {
		t.Fatalf("read Notion token relay source: %v", err)
	}
	for _, expected := range []string{
		"378d872b-594c-8110-b4c0-0037422697b3",
		"/notion/token",
		"authorization_code",
		"refresh_token",
		"NOTION_CLIENT_SECRET",
		"Authorization",
		"Basic ",
		"application/json",
		"https://api.notion.com/v1/oauth/token",
	} {
		if !strings.Contains(string(source), expected) {
			t.Fatalf("expected Notion token relay source to include %q, got:\n%s", expected, source)
		}
	}
	if strings.Contains(string(source), "console.log") {
		t.Fatalf("expected Notion token relay not to log OAuth request data, got:\n%s", source)
	}
}
