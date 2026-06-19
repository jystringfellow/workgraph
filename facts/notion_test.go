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

func TestNotionCaptureIndexesObjectsAndStoresChangedByMeActivity(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "notion.json"), []byte(`{
  "access_token": "notion-token",
  "owner": {"type": "user", "user": {"id": "user-me"}}
}`), 0o600); err != nil {
		t.Fatalf("write notion config: %v", err)
	}
	dbPath := filepath.Join(homeDir, "workgraph.db")
	seedNotionIndex(t, dbPath, "page-1", "page", "Old title", "2026-06-07T15:00:00Z", "user-me")
	seedNotionIndex(t, dbPath, "page-2", "page", "Other old title", "2026-06-07T15:00:00Z", "user-me")

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/search":
			_, _ = response.Write([]byte(`{
  "object": "list",
  "results": [
    {
      "object": "page",
      "id": "page-1",
      "created_time": "2026-06-07T14:00:00.000Z",
      "created_by": {"object": "user", "id": "user-me"},
      "last_edited_time": "2026-06-07T16:30:00.000Z",
      "last_edited_by": {"object": "user", "id": "user-me"},
      "url": "https://www.notion.so/page-1",
      "parent": {"type": "workspace", "workspace": true},
      "properties": {
        "title": {
          "id": "title",
          "type": "title",
          "title": [{"plain_text": "Updated launch plan"}]
        },
        "Status": {"id": "status", "type": "status", "status": {"name": "In progress"}}
      }
    },
    {
      "object": "page",
      "id": "page-2",
      "created_time": "2026-06-07T14:00:00.000Z",
      "created_by": {"object": "user", "id": "user-me"},
      "last_edited_time": "2026-06-07T16:45:00.000Z",
      "last_edited_by": {"object": "user", "id": "user-someone-else"},
      "url": "https://www.notion.so/page-2",
      "properties": {
        "title": {
          "id": "title",
          "type": "title",
          "title": [{"plain_text": "Someone else's update"}]
        }
      }
    }
  ],
  "next_cursor": null,
  "has_more": false
}`))
		case request.Method == http.MethodGet && request.URL.Path == "/v1/blocks/page-1/children":
			_, _ = response.Write([]byte(`{"object":"list","results":[],"next_cursor":null,"has_more":false}`))
		default:
			t.Fatalf("unexpected Notion request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	output, err := runworkgraph(t, repoRoot, "notion", "capture",
		"--home", homeDir,
		"--notion-api-base", server.URL,
	)
	if err != nil {
		t.Fatalf("workgraph notion capture failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Events stored: 3") {
		t.Fatalf("expected metadata and changed-by-me activity events, got:\n%s", output)
	}
	activity := notionEvent(t, dbPath, "notion.page_updated", "page-1:2026-06-07T16:30:00Z")
	if activity.Timestamp != "2026-06-07T16:30:00Z" {
		t.Fatalf("expected activity timestamp from last_edited_time, got %q", activity.Timestamp)
	}
	if activity.Summary != "Updated launch plan" {
		t.Fatalf("expected activity summary from title, got %q", activity.Summary)
	}
	for _, expected := range []string{
		`"object":"page"`,
		`"id":"page-1"`,
		`"last_edited_by":"user-me"`,
		`"properties"`,
		`"Status"`,
		`"In progress"`,
	} {
		if !strings.Contains(activity.PayloadJSON, expected) {
			t.Fatalf("expected activity payload to include %s, got %s", expected, activity.PayloadJSON)
		}
	}

	index := notionIndexRow(t, dbPath, "page-1")
	if index.Title != "Updated launch plan" || index.LastEditedTime != "2026-06-07T16:30:00Z" || index.LastEditedBy != "user-me" {
		t.Fatalf("expected updated notion index, got %#v", index)
	}
	if !strings.Contains(index.PropertiesJSON, `"Status"`) || !strings.Contains(index.PropertiesJSON, `"In progress"`) {
		t.Fatalf("expected properties snapshot in index, got %s", index.PropertiesJSON)
	}
	otherIndex := notionIndexRow(t, dbPath, "page-2")
	if otherIndex.Title != "Someone else's update" || otherIndex.LastEditedBy != "user-someone-else" {
		t.Fatalf("expected other-user page to be indexed, got %#v", otherIndex)
	}
	if notionEventExists(t, dbPath, "notion.page_updated", "page-2:2026-06-07T16:45:00Z") {
		t.Fatalf("expected other-user page update not to create personal activity event")
	}
}

func TestNotionCaptureFetchesPreviewForChangedByMePages(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "notion.json"), []byte(`{
  "access_token": "notion-token",
  "owner": {"type": "user", "user": {"id": "user-me"}}
}`), 0o600); err != nil {
		t.Fatalf("write notion config: %v", err)
	}
	dbPath := filepath.Join(homeDir, "workgraph.db")
	seedNotionIndex(t, dbPath, "page-1", "page", "Old launch plan", "2026-06-07T15:00:00Z", "user-me")
	seedNotionIndex(t, dbPath, "page-2", "page", "Other old title", "2026-06-07T15:00:00Z", "user-me")

	blockRequests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/search":
			_, _ = response.Write([]byte(`{
  "object": "list",
  "results": [
    {
      "object": "page",
      "id": "page-1",
      "created_time": "2026-06-07T14:00:00.000Z",
      "created_by": {"object": "user", "id": "user-me"},
      "last_edited_time": "2026-06-07T16:30:00.000Z",
      "last_edited_by": {"object": "user", "id": "user-me"},
      "url": "https://www.notion.so/page-1",
      "properties": {
        "title": {
          "id": "title",
          "type": "title",
          "title": [{"plain_text": "Updated launch plan"}]
        }
      }
    },
    {
      "object": "page",
      "id": "page-2",
      "created_time": "2026-06-07T14:00:00.000Z",
      "created_by": {"object": "user", "id": "user-me"},
      "last_edited_time": "2026-06-07T16:45:00.000Z",
      "last_edited_by": {"object": "user", "id": "user-someone-else"},
      "url": "https://www.notion.so/page-2",
      "properties": {
        "title": {
          "id": "title",
          "type": "title",
          "title": [{"plain_text": "Someone else's update"}]
        }
      }
    }
  ],
  "next_cursor": null,
  "has_more": false
}`))
		case request.Method == http.MethodGet && request.URL.Path == "/v1/blocks/page-1/children":
			blockRequests["page-1"]++
			_, _ = response.Write([]byte(`{
  "object": "list",
  "results": [
    {
      "object": "block",
      "id": "block-1",
      "type": "heading_2",
      "heading_2": {"rich_text": [{"plain_text": "Launch checklist"}]}
    },
    {
      "object": "block",
      "id": "block-2",
      "type": "paragraph",
      "paragraph": {"rich_text": [{"plain_text": "Added beta rollout notes and owner follow-ups."}]}
    },
    {
      "object": "block",
      "id": "block-3",
      "type": "to_do",
      "to_do": {"checked": false, "rich_text": [{"plain_text": "Confirm Notion polling preview."}]}
    }
  ],
  "next_cursor": null,
  "has_more": false
}`))
		case request.Method == http.MethodGet && request.URL.Path == "/v1/blocks/page-2/children":
			blockRequests["page-2"]++
			http.Error(response, "page-2 blocks should not be fetched", http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected Notion request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	output, err := runworkgraph(t, repoRoot, "notion", "capture",
		"--home", homeDir,
		"--notion-api-base", server.URL,
	)
	if err != nil {
		t.Fatalf("workgraph notion capture failed: %v\n%s", err, output)
	}
	if blockRequests["page-1"] != 1 {
		t.Fatalf("expected changed-by-me page blocks to be fetched once, got %#v", blockRequests)
	}
	if blockRequests["page-2"] != 0 {
		t.Fatalf("expected other-user page blocks not to be fetched, got %#v", blockRequests)
	}

	activity := notionEvent(t, dbPath, "notion.page_updated", "page-1:2026-06-07T16:30:00Z")
	for _, expected := range []string{
		`"content_preview"`,
		`## Launch checklist`,
		`Added beta rollout notes and owner follow-ups.`,
		`- [ ] Confirm Notion polling preview.`,
	} {
		if !strings.Contains(activity.PayloadJSON, expected) {
			t.Fatalf("expected activity payload to include %s, got %s", expected, activity.PayloadJSON)
		}
	}

	index := notionIndexRow(t, dbPath, "page-1")
	if !strings.Contains(index.ContentPreview, "Launch checklist") || !strings.Contains(index.ContentPreview, "Confirm Notion polling preview") {
		t.Fatalf("expected content preview in notion index, got %#v", index)
	}
}

func TestNotionIndexListAndShowInspectStoredPages(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	dbPath := filepath.Join(homeDir, "workgraph.db")
	seedNotionIndex(t, dbPath, "page-1", "page", "Updated launch plan", "2026-06-08T15:00:00Z", "user-me")
	setNotionIndexPreview(t, dbPath, "page-1", "## Launch checklist\nAdded beta rollout notes.")

	output, err := runworkgraph(t, repoRoot, "notion", "index", "list",
		"--home", homeDir,
	)
	if err != nil {
		t.Fatalf("workgraph notion index list failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"Notion index",
		"page-1",
		"page",
		"Updated launch plan",
		"2026-06-08T15:00:00Z",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected notion index list output to include %q, got:\n%s", expected, output)
		}
	}

	output, err = runworkgraph(t, repoRoot, "notion", "index", "show", "page-1",
		"--home", homeDir,
	)
	if err != nil {
		t.Fatalf("workgraph notion index show failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"Notion index item",
		"ID: page-1",
		"Type: page",
		"Title: Updated launch plan",
		"Content preview:",
		"## Launch checklist",
		"Added beta rollout notes.",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected notion index show output to include %q, got:\n%s", expected, output)
		}
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

	settingsPath := filepath.Join(homeDir, "notion.json")
	info, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("expected notion config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected notion config permissions 0600, got %v", got)
	}
	contents, err := os.ReadFile(settingsPath)
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
	if err := os.WriteFile(filepath.Join(homeDir, "connectors.json"), []byte(`{
  "connectors": {
    "notion": {
      "setup_state": "error",
      "last_validation_error": "search Notion: status 401: invalid token",
      "last_poll_at": "2026-06-17T11:33:50Z",
      "last_error": "search Notion: status 401: invalid token"
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write connector runtime config: %v", err)
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
	statusOutput, statusErr := runworkgraph(t, repoRoot, "connectors", "status", "--home", homeDir)
	if statusErr != nil {
		t.Fatalf("workgraph connectors status failed: %v\n%s", statusErr, statusOutput)
	}
	if !strings.Contains(string(statusOutput), "- notion: setup ready, polling enabled") {
		t.Fatalf("expected already connected Notion to repair runtime setup state, got:\n%s", statusOutput)
	}
	if strings.Contains(string(statusOutput), "invalid token") || strings.Contains(string(statusOutput), "validation error") {
		t.Fatalf("expected already connected Notion to clear stale errors, got:\n%s", statusOutput)
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
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Fatalf("expected notion config removed after disconnect, stat err: %v", err)
	}
	statusOutput, statusErr = runworkgraph(t, repoRoot, "connectors", "status", "--home", homeDir)
	if statusErr != nil {
		t.Fatalf("workgraph connectors status failed: %v\n%s", statusErr, statusOutput)
	}
	if !strings.Contains(string(statusOutput), "- notion: setup not connected, polling not ready") {
		t.Fatalf("expected Notion disconnect to clear runtime setup state, got:\n%s", statusOutput)
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

func TestNotionConnectTokenStoresConfigAndEnablesRuntimePolling(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	var searchRequests int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		searchRequests++
		if request.Method != http.MethodPost || request.URL.Path != "/v1/search" {
			t.Fatalf("expected validation search request, got %s %s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer notion-token" {
			t.Fatalf("expected bearer token, got %q", got)
		}
		if got := request.Header.Get("Notion-Version"); got == "" {
			t.Fatalf("expected Notion-Version header")
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"object":"list","results":[],"next_cursor":null,"has_more":false}`))
	}))
	defer server.Close()

	output, err := runworkgraph(t, repoRoot, "notion", "connect-token",
		"--home", homeDir,
		"--token", "notion-token",
		"--notion-api-base", server.URL,
	)
	if err != nil {
		t.Fatalf("workgraph notion connect-token failed: %v\n%s", err, output)
	}
	if searchRequests != 1 {
		t.Fatalf("expected one validation search request, got %d", searchRequests)
	}
	if !strings.Contains(string(output), "Notion connected") || !strings.Contains(string(output), "notion connected") {
		t.Fatalf("expected token connection and runtime handoff output, got:\n%s", output)
	}

	contents, err := os.ReadFile(filepath.Join(homeDir, "notion.json"))
	if err != nil {
		t.Fatalf("read notion config: %v", err)
	}
	var stored struct {
		AccessToken string `json:"access_token"`
		APIBaseURL  string `json:"api_base_url"`
		TokenType   string `json:"token_type"`
		ClientID    string `json:"client_id"`
		TokenURL    string `json:"token_url"`
	}
	if err := json.Unmarshal(contents, &stored); err != nil {
		t.Fatalf("parse notion config: %v", err)
	}
	if stored.AccessToken != "notion-token" || stored.APIBaseURL != server.URL {
		t.Fatalf("expected stored manual-token config, got %#v", stored)
	}
	if stored.TokenType != "" || stored.ClientID != "" || stored.TokenURL != "" {
		t.Fatalf("expected manual-token config not to store OAuth metadata, got %#v", stored)
	}

	runtimeContents, err := os.ReadFile(filepath.Join(homeDir, "connectors.json"))
	if err != nil {
		t.Fatalf("read connector runtime config: %v", err)
	}
	for _, expected := range []string{
		`"notion"`,
		`"enabled": true`,
		`"setup_state": "ready"`,
		`"last_validated_at"`,
	} {
		if !strings.Contains(string(runtimeContents), expected) {
			t.Fatalf("expected runtime config to include %q, got:\n%s", expected, runtimeContents)
		}
	}

	output, err = runworkgraph(t, repoRoot, "connectors", "status", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph connectors status failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"- notion: setup ready",
		"polling enabled",
		"last validated ",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected connector status detail %q, got:\n%s", expected, output)
		}
	}
}

func TestNotionConnectTokenValidationFailureDoesNotStoreReadyState(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "notion", "connect-token",
		"--home", homeDir,
	)
	if err == nil {
		t.Fatalf("expected missing token to fail, got:\n%s", output)
	}
	if _, statErr := os.Stat(filepath.Join(homeDir, "notion.json")); !os.IsNotExist(statErr) {
		t.Fatalf("expected missing token not to store notion config, stat err: %v", statErr)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Error(response, `{"message":"invalid token"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	output, err = runworkgraph(t, repoRoot, "notion", "connect-token",
		"--home", homeDir,
		"--token", "bad-token",
		"--notion-api-base", server.URL,
	)
	if err == nil {
		t.Fatalf("expected invalid token to fail, got:\n%s", output)
	}
	if _, statErr := os.Stat(filepath.Join(homeDir, "notion.json")); !os.IsNotExist(statErr) {
		t.Fatalf("expected invalid token not to store notion config, stat err: %v", statErr)
	}
	statusOutput, statusErr := runworkgraph(t, repoRoot, "connectors", "status", "--home", homeDir)
	if statusErr != nil {
		t.Fatalf("workgraph connectors status failed: %v\n%s", statusErr, statusOutput)
	}
	if strings.Contains(string(statusOutput), "- notion: setup ready") {
		t.Fatalf("expected invalid token not to store ready state, got:\n%s", statusOutput)
	}
	if !strings.Contains(string(statusOutput), "- notion: setup error") || !strings.Contains(string(statusOutput), "validation error") {
		t.Fatalf("expected validation failure to be visible, got:\n%s", statusOutput)
	}
}

func TestNotionConnectorValidateUsesStoredToken(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	var searchRequests int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		searchRequests++
		if request.Method != http.MethodPost || request.URL.Path != "/v1/search" {
			t.Fatalf("expected validation search request, got %s %s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer notion-token" {
			t.Fatalf("expected bearer token, got %q", got)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"object":"list","results":[],"next_cursor":null,"has_more":false}`))
	}))
	defer server.Close()

	if err := os.WriteFile(filepath.Join(homeDir, "notion.json"), []byte(fmt.Sprintf(`{
  "access_token": "notion-token",
  "api_base_url": %q
}`, server.URL)), 0o600); err != nil {
		t.Fatalf("write notion config: %v", err)
	}

	output, err := runworkgraph(t, repoRoot, "connectors", "validate", "--home", homeDir, "notion")
	if err != nil {
		t.Fatalf("workgraph connectors validate notion failed: %v\n%s", err, output)
	}
	if searchRequests != 1 {
		t.Fatalf("expected one validation search request, got %d", searchRequests)
	}
	if !strings.Contains(string(output), "Connector notion validation passed") {
		t.Fatalf("expected validation passed output, got:\n%s", output)
	}

	statusOutput, statusErr := runworkgraph(t, repoRoot, "connectors", "status", "--home", homeDir)
	if statusErr != nil {
		t.Fatalf("workgraph connectors status failed: %v\n%s", statusErr, statusOutput)
	}
	if !strings.Contains(string(statusOutput), "- notion: setup ready") || !strings.Contains(string(statusOutput), "polling enabled") {
		t.Fatalf("expected validation to mark Notion ready, got:\n%s", statusOutput)
	}
}

type storedNotionEvent struct {
	Timestamp   string
	Summary     string
	PayloadJSON string
}

type storedNotionIndex struct {
	Title          string
	LastEditedTime string
	LastEditedBy   string
	PropertiesJSON string
	ContentPreview string
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

func seedNotionIndex(t *testing.T, dbPath, notionID, objectType, title, lastEditedTime, lastEditedBy string) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	now := "2026-06-07T15:00:00Z"
	_, err = db.Exec(`INSERT INTO notion_index (
			notion_id, object_type, title, url, parent_json, properties_json,
			created_time, created_by, last_edited_time, last_edited_by,
			source, first_seen_at, last_seen_at, last_synced_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		notionID,
		objectType,
		title,
		"",
		nil,
		`{}`,
		lastEditedTime,
		lastEditedBy,
		lastEditedTime,
		lastEditedBy,
		"search",
		now,
		now,
		now,
	)
	if err != nil {
		t.Fatalf("seed notion index: %v", err)
	}
}

func notionIndexRow(t *testing.T, dbPath, notionID string) storedNotionIndex {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var row storedNotionIndex
	err = db.QueryRow(`SELECT title, last_edited_time, last_edited_by, COALESCE(properties_json, ''), COALESCE(content_preview, '') FROM notion_index WHERE notion_id = ?`, notionID).Scan(&row.Title, &row.LastEditedTime, &row.LastEditedBy, &row.PropertiesJSON, &row.ContentPreview)
	if err != nil {
		t.Fatalf("read notion index %s: %v", notionID, err)
	}
	return row
}

func setNotionIndexPreview(t *testing.T, dbPath, notionID, preview string) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`UPDATE notion_index SET content_preview = ?, content_synced_at = ? WHERE notion_id = ?`, preview, "2026-06-08T15:01:00Z", notionID)
	if err != nil {
		t.Fatalf("set notion index preview: %v", err)
	}
}

func notionEventExists(t *testing.T, dbPath, eventType, objectID string) bool {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var count int
	id := fmt.Sprintf("%s:%s", eventType, objectID)
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE id = ?`, id).Scan(&count); err != nil {
		t.Fatalf("count notion event %s: %v", id, err)
	}
	return count > 0
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
