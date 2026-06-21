package workgraph

import (
	"bytes"
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

// DefaultNotionClientID is the OAuth client id used for the workgraph Notion public connection.
var DefaultNotionClientID = "378d872b-594c-8110-b4c0-0037422697b3"

// DefaultNotionRedirectURI is the local redirect URI registered for the workgraph Notion public connection.
const DefaultNotionRedirectURI = "http://localhost:2727/notion/callback"

// DefaultNotionTokenURL is the workgraph OAuth token relay for Notion.
var DefaultNotionTokenURL = "https://workgraph-notion-oauth-token.jystringfellow.workers.dev/notion/token"

// DefaultNotionAPIBaseURL is Notion's API base URL.
var DefaultNotionAPIBaseURL = "https://api.notion.com"

// DefaultNotionAPIVersion is pinned to the API shape where search returns pages and databases.
const DefaultNotionAPIVersion = "2022-06-28"

// NotionCaptureConfig controls Notion page and database metadata capture.
type NotionCaptureConfig struct {
	HomeDir      string
	DatabasePath string
	Token        string
	APIBaseURL   string
	HTTPClient   *http.Client
}

// NotionCaptureResult describes a Notion capture run.
type NotionCaptureResult struct {
	HomeDir      string
	DatabasePath string
	EventsStored int
	Message      string
}

// NotionConnectConfig controls Notion OAuth setup.
type NotionConnectConfig struct {
	HomeDir       string
	ClientID      string
	RedirectURI   string
	Code          string
	State         string
	ExpectedState string
	AuthBaseURL   string
	TokenURL      string
	APIBaseURL    string
	HTTPClient    *http.Client
	OpenBrowser   func(string) error
}

// NotionConnectResult describes Notion OAuth setup.
type NotionConnectResult struct {
	ConfigPath       string
	AuthorizationURL string
	State            string
	Configured       bool
	Message          string
}

// NotionConnectTokenConfig controls local Notion internal-integration token setup.
type NotionConnectTokenConfig struct {
	HomeDir    string
	Token      string
	APIBaseURL string
	HTTPClient *http.Client
}

// NotionDisconnectConfig controls Notion disconnect behavior.
type NotionDisconnectConfig struct {
	HomeDir string
}

// NotionDisconnectResult describes Notion disconnect behavior.
type NotionDisconnectResult struct {
	ConfigPath string
	Message    string
}

type NotionIndexListConfig struct {
	HomeDir      string
	DatabasePath string
	Limit        int
}

type NotionIndexShowConfig struct {
	HomeDir      string
	DatabasePath string
	ID           string
}

type NotionIndexResult struct {
	Items   []NotionIndexItem
	Item    NotionIndexItem
	Message string
}

type NotionIndexItem struct {
	ID              string
	ObjectType      string
	Title           string
	URL             string
	LastEditedTime  string
	LastEditedBy    string
	ContentPreview  string
	ContentSyncedAt string
}

type notionConnectorConfig struct {
	AccessToken          string          `json:"access_token"`
	RefreshToken         string          `json:"refresh_token,omitempty"`
	TokenType            string          `json:"token_type,omitempty"`
	ExpiresAt            string          `json:"expires_at,omitempty"`
	WorkspaceID          string          `json:"workspace_id,omitempty"`
	WorkspaceName        string          `json:"workspace_name,omitempty"`
	WorkspaceIcon        string          `json:"workspace_icon,omitempty"`
	BotID                string          `json:"bot_id,omitempty"`
	Owner                json.RawMessage `json:"owner,omitempty"`
	DuplicatedTemplateID string          `json:"duplicated_template_id,omitempty"`
	APIBaseURL           string          `json:"api_base_url,omitempty"`
	ClientID             string          `json:"client_id,omitempty"`
	TokenURL             string          `json:"token_url,omitempty"`
}

type notionOAuthTokenResponse struct {
	AccessToken          string          `json:"access_token"`
	RefreshToken         string          `json:"refresh_token"`
	TokenType            string          `json:"token_type"`
	ExpiresIn            int             `json:"expires_in"`
	WorkspaceID          string          `json:"workspace_id"`
	WorkspaceName        string          `json:"workspace_name"`
	WorkspaceIcon        string          `json:"workspace_icon"`
	BotID                string          `json:"bot_id"`
	Owner                json.RawMessage `json:"owner"`
	DuplicatedTemplateID string          `json:"duplicated_template_id"`
}

type notionSearchResponse struct {
	Results    []notionSearchResult `json:"results"`
	NextCursor string               `json:"next_cursor"`
	HasMore    bool                 `json:"has_more"`
}

type notionBlockChildrenResponse struct {
	Results    []notionBlock `json:"results"`
	NextCursor string        `json:"next_cursor"`
	HasMore    bool          `json:"has_more"`
}

type notionSearchResult struct {
	Object         string                     `json:"object"`
	ID             string                     `json:"id"`
	CreatedTime    string                     `json:"created_time"`
	CreatedBy      notionUserRef              `json:"created_by"`
	LastEditedTime string                     `json:"last_edited_time"`
	LastEditedBy   notionUserRef              `json:"last_edited_by"`
	URL            string                     `json:"url"`
	Properties     map[string]json.RawMessage `json:"properties,omitempty"`
	Title          []notionRichText           `json:"title,omitempty"`
	Parent         json.RawMessage            `json:"parent,omitempty"`
}

type notionUserRef struct {
	Object string `json:"object"`
	ID     string `json:"id"`
}

type notionProperty struct {
	Type  string           `json:"type"`
	Title []notionRichText `json:"title,omitempty"`
}

type notionRichText struct {
	PlainText string `json:"plain_text"`
}

type notionBlock struct {
	Object           string          `json:"object"`
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Paragraph        notionTextBlock `json:"paragraph"`
	Heading1         notionTextBlock `json:"heading_1"`
	Heading2         notionTextBlock `json:"heading_2"`
	Heading3         notionTextBlock `json:"heading_3"`
	BulletedListItem notionTextBlock `json:"bulleted_list_item"`
	NumberedListItem notionTextBlock `json:"numbered_list_item"`
	ToDo             notionTextBlock `json:"to_do"`
	Toggle           notionTextBlock `json:"toggle"`
	Quote            notionTextBlock `json:"quote"`
	Callout          notionTextBlock `json:"callout"`
	ChildPage        struct {
		Title string `json:"title"`
	} `json:"child_page"`
	ChildDatabase struct {
		Title string `json:"title"`
	} `json:"child_database"`
}

type notionTextBlock struct {
	RichText []notionRichText `json:"rich_text"`
	Checked  bool             `json:"checked"`
}

type notionEventPayload struct {
	Object         string          `json:"object"`
	ID             string          `json:"id"`
	Title          string          `json:"title,omitempty"`
	URL            string          `json:"url,omitempty"`
	CreatedTime    string          `json:"created_time,omitempty"`
	CreatedBy      string          `json:"created_by,omitempty"`
	LastEditedTime string          `json:"last_edited_time,omitempty"`
	LastEditedBy   string          `json:"last_edited_by,omitempty"`
	Parent         json.RawMessage `json:"parent,omitempty"`
	Properties     any             `json:"properties,omitempty"`
	ContentPreview string          `json:"content_preview,omitempty"`
}

// CaptureNotion stores metadata for shared Notion pages and databases.
func CaptureNotion(config NotionCaptureConfig) (NotionCaptureResult, error) {
	status, err := prepareRunStatus(RunConfig{
		HomeDir:      config.HomeDir,
		DatabasePath: config.DatabasePath,
	})
	if err != nil {
		return NotionCaptureResult{}, err
	}
	config.HomeDir = status.HomeDir
	if config.DatabasePath == "" {
		config.DatabasePath = status.DatabasePath
	}
	currentUserID := ""
	if config.Token == "" {
		stored, err := readNotionConnectorConfig(status.HomeDir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return NotionCaptureResult{}, errors.New("notion token is required: run workgraph notion connect or pass --token")
			}
			return NotionCaptureResult{}, err
		}
		config.Token = stored.AccessToken
		if config.APIBaseURL == "" {
			config.APIBaseURL = stored.APIBaseURL
		}
		currentUserID = notionOwnerUserID(stored.Owner)
	}
	if config.Token == "" {
		return NotionCaptureResult{}, errors.New("notion token is required")
	}

	objects, err := notionSearchAll(config)
	if err != nil {
		return NotionCaptureResult{}, err
	}

	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return NotionCaptureResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return NotionCaptureResult{}, fmt.Errorf("open database: %w", err)
	}
	if err := createSchema(db); err != nil {
		return NotionCaptureResult{}, fmt.Errorf("create database schema: %w", err)
	}

	stored := 0
	for _, object := range objects {
		if object.Object != "page" && object.Object != "database" {
			continue
		}
		inserted, err := storeNotionEvent(db, object)
		if err != nil {
			return NotionCaptureResult{}, err
		}
		if inserted {
			stored++
		}
		indexed, err := updateNotionIndexAndStoreActivity(db, object, currentUserID, config)
		if err != nil {
			return NotionCaptureResult{}, err
		}
		if indexed {
			stored++
		}
	}

	result := NotionCaptureResult{
		HomeDir:      status.HomeDir,
		DatabasePath: status.DatabasePath,
		EventsStored: stored,
	}
	result.Message = notionCaptureMessage(result)
	return result, nil
}

// ConnectNotion prepares or completes Notion OAuth setup.
func ConnectNotion(config NotionConnectConfig) (NotionConnectResult, error) {
	homeDir, err := resolveNotionHomeDir(config.HomeDir)
	if err != nil {
		return NotionConnectResult{}, err
	}
	if err := enforceConnectorManagedSettings("notion"); err != nil {
		return NotionConnectResult{}, err
	}
	if config.Code == "" {
		if connected, err := notionConnected(homeDir); err != nil {
			return NotionConnectResult{}, err
		} else if connected {
			if _, err := connectRuntimeConnector(homeDir, "notion", ""); err != nil {
				return NotionConnectResult{}, err
			}
			return notionAlreadyConnectedResult(homeDir), nil
		}
	}
	config.ClientID = resolveNotionClientID(config.ClientID)
	if config.ClientID == "" {
		return NotionConnectResult{}, errors.New("notion client id is required")
	}
	if config.RedirectURI == "" {
		config.RedirectURI = DefaultNotionRedirectURI
	}

	state := config.State
	if state == "" {
		generated, err := newEventID()
		if err != nil {
			return NotionConnectResult{}, fmt.Errorf("create oauth state: %w", err)
		}
		state = generated
	}
	result := NotionConnectResult{
		ConfigPath:       notionConfigPath(homeDir),
		AuthorizationURL: notionAuthorizationURL(config, state),
		State:            state,
	}
	if config.Code == "" {
		result.Message = strings.Join([]string{
			"Notion OAuth authorization URL",
			result.AuthorizationURL,
			"State: " + result.State,
			"After Notion redirects back with a code, rerun notion connect with --code and --state.",
		}, "\n")
		return result, nil
	}
	if config.ExpectedState != "" && config.State != "" && config.State != config.ExpectedState {
		return NotionConnectResult{}, errors.New("notion oauth state did not match")
	}

	token, err := exchangeNotionOAuthCode(config)
	if err != nil {
		return NotionConnectResult{}, err
	}
	return storeNotionConnection(homeDir, config, token)
}

// ConnectNotionWithToken validates and stores a local Notion integration token.
func ConnectNotionWithToken(config NotionConnectTokenConfig) (NotionConnectResult, error) {
	homeDir, err := resolveNotionHomeDir(config.HomeDir)
	if err != nil {
		return NotionConnectResult{}, err
	}
	if err := enforceConnectorManagedSettings("notion"); err != nil {
		return NotionConnectResult{}, err
	}
	token := strings.TrimSpace(config.Token)
	if token == "" {
		_ = recordConnectorValidationError(homeDir, "notion", time.Now(), "notion token is required")
		return NotionConnectResult{}, errors.New("notion token is required")
	}
	apiBaseURL := resolveNotionAPIBaseURL(config.APIBaseURL)
	if err := validateNotionToken(token, apiBaseURL, config.HTTPClient); err != nil {
		_ = recordConnectorValidationError(homeDir, "notion", time.Now(), err.Error())
		return NotionConnectResult{}, err
	}
	configPath := notionConfigPath(homeDir)
	stored := notionConnectorConfig{
		AccessToken: token,
		APIBaseURL:  apiBaseURL,
	}
	if err := writeNotionConnectorConfig(configPath, stored); err != nil {
		return NotionConnectResult{}, err
	}
	runtimeResult, err := connectRuntimeConnector(homeDir, "notion", "")
	if err != nil {
		return NotionConnectResult{}, err
	}
	return NotionConnectResult{
		ConfigPath: configPath,
		Configured: true,
		Message: strings.Join([]string{
			"Notion connected",
			"Manual token stored locally. Use a Notion internal integration token with the narrowest workspace access practical.",
			"Config: " + configPath,
			runtimeResult.Message,
		}, "\n"),
	}, nil
}

// ConnectNotionWithBrowser completes Notion OAuth with a local callback.
func ConnectNotionWithBrowser(ctx context.Context, config NotionConnectConfig) (NotionConnectResult, error) {
	homeDir, err := resolveNotionHomeDir(config.HomeDir)
	if err != nil {
		return NotionConnectResult{}, err
	}
	if err := enforceConnectorManagedSettings("notion"); err != nil {
		return NotionConnectResult{}, err
	}
	if connected, err := notionConnected(homeDir); err != nil {
		return NotionConnectResult{}, err
	} else if connected {
		if _, err := connectRuntimeConnector(homeDir, "notion", ""); err != nil {
			return NotionConnectResult{}, err
		}
		return notionAlreadyConnectedResult(homeDir), nil
	}
	config.ClientID = resolveNotionClientID(config.ClientID)
	if config.ClientID == "" {
		return NotionConnectResult{}, errors.New("notion client id is required for browser connect")
	}
	if config.RedirectURI == "" {
		config.RedirectURI = DefaultNotionRedirectURI
	}

	parsedRedirect, err := url.Parse(config.RedirectURI)
	if err != nil {
		return NotionConnectResult{}, fmt.Errorf("parse notion redirect URI: %w", err)
	}
	if !isLocalHTTPRedirect(parsedRedirect) {
		return NotionConnectResult{}, errors.New("notion redirect URI must be an http localhost URL")
	}
	listener, err := net.Listen("tcp", parsedRedirect.Host)
	if err != nil {
		return NotionConnectResult{}, fmt.Errorf("start notion oauth callback: %w", err)
	}
	defer listener.Close()

	state, err := randomURLToken(24)
	if err != nil {
		return NotionConnectResult{}, fmt.Errorf("create oauth state: %w", err)
	}
	config.State = state
	authURL := notionAuthorizationURL(config, state)
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	server := &http.Server{
		Handler: notionOAuthCallbackHandler(state, codeCh, errCh),
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
		return NotionConnectResult{}, err
	}

	var code string
	select {
	case <-ctx.Done():
		return NotionConnectResult{}, ctx.Err()
	case err := <-errCh:
		return NotionConnectResult{}, err
	case code = <-codeCh:
	}

	config.HomeDir = homeDir
	config.Code = code
	config.ExpectedState = state
	token, err := exchangeNotionOAuthCode(config)
	if err != nil {
		return NotionConnectResult{}, err
	}
	result, err := storeNotionConnection(homeDir, config, token)
	if err != nil {
		return NotionConnectResult{}, err
	}
	result.AuthorizationURL = authURL
	result.State = state
	return result, nil
}

// DisconnectNotion removes local Notion connector settings.
func DisconnectNotion(config NotionDisconnectConfig) (NotionDisconnectResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return NotionDisconnectResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return NotionDisconnectResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	configPath := notionConfigPath(homeDir)
	if _, err := os.Stat(configPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return notionAlreadyDisconnectedResult(homeDir), nil
		}
		return NotionDisconnectResult{}, fmt.Errorf("check notion config: %w", err)
	}
	if err := os.Remove(configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return NotionDisconnectResult{}, fmt.Errorf("remove notion config: %w", err)
	}
	if err := clearRuntimeConnector(homeDir, "notion"); err != nil {
		return NotionDisconnectResult{}, err
	}
	return NotionDisconnectResult{
		ConfigPath: configPath,
		Message: strings.Join([]string{
			"Notion disconnected",
			"Config removed: " + configPath,
			"To fully revoke access, remove workgraph from your Notion workspace connection settings.",
		}, "\n"),
	}, nil
}

func ListNotionIndex(config NotionIndexListConfig) (NotionIndexResult, error) {
	db, err := openNotionIndexDatabase(config.HomeDir, config.DatabasePath)
	if err != nil {
		return NotionIndexResult{}, err
	}
	defer db.Close()
	limit := config.Limit
	if limit <= 0 {
		limit = 25
	}
	rows, err := db.Query(`SELECT notion_id, object_type, COALESCE(title, ''), COALESCE(url, ''), COALESCE(last_edited_time, ''), COALESCE(last_edited_by, ''), COALESCE(content_preview, ''), COALESCE(content_synced_at, '')
		FROM notion_index
		ORDER BY COALESCE(last_edited_time, last_seen_at) DESC, notion_id
		LIMIT ?`, limit)
	if err != nil {
		return NotionIndexResult{}, fmt.Errorf("query Notion index: %w", err)
	}
	defer rows.Close()
	var items []NotionIndexItem
	for rows.Next() {
		var item NotionIndexItem
		if err := rows.Scan(&item.ID, &item.ObjectType, &item.Title, &item.URL, &item.LastEditedTime, &item.LastEditedBy, &item.ContentPreview, &item.ContentSyncedAt); err != nil {
			return NotionIndexResult{}, fmt.Errorf("scan Notion index: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return NotionIndexResult{}, fmt.Errorf("query Notion index: %w", err)
	}
	result := NotionIndexResult{Items: items}
	result.Message = notionIndexListMessage(items)
	return result, nil
}

func ShowNotionIndex(config NotionIndexShowConfig) (NotionIndexResult, error) {
	id := strings.TrimSpace(config.ID)
	if id == "" {
		return NotionIndexResult{}, errors.New("notion index id is required")
	}
	db, err := openNotionIndexDatabase(config.HomeDir, config.DatabasePath)
	if err != nil {
		return NotionIndexResult{}, err
	}
	defer db.Close()
	var item NotionIndexItem
	err = db.QueryRow(`SELECT notion_id, object_type, COALESCE(title, ''), COALESCE(url, ''), COALESCE(last_edited_time, ''), COALESCE(last_edited_by, ''), COALESCE(content_preview, ''), COALESCE(content_synced_at, '')
		FROM notion_index
		WHERE notion_id = ?`, id).Scan(&item.ID, &item.ObjectType, &item.Title, &item.URL, &item.LastEditedTime, &item.LastEditedBy, &item.ContentPreview, &item.ContentSyncedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NotionIndexResult{}, fmt.Errorf("notion index item %q not found", id)
		}
		return NotionIndexResult{}, fmt.Errorf("read Notion index item: %w", err)
	}
	result := NotionIndexResult{Item: item}
	result.Message = notionIndexShowMessage(item)
	return result, nil
}

func notionSearchAll(config NotionCaptureConfig) ([]notionSearchResult, error) {
	baseURL := resolveNotionAPIBaseURL(config.APIBaseURL)
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	var results []notionSearchResult
	cursor := ""
	for {
		body := map[string]any{"page_size": 100}
		if cursor != "" {
			body["start_cursor"] = cursor
		}
		requestBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode Notion search request: %w", err)
		}
		request, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/search", bytes.NewReader(requestBody))
		if err != nil {
			return nil, fmt.Errorf("build Notion search request: %w", err)
		}
		request.Header.Set("Authorization", "Bearer "+config.Token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Notion-Version", DefaultNotionAPIVersion)

		response, err := client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("search Notion: %w", err)
		}
		responseBody, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read Notion search response: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close Notion search response: %w", closeErr)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("search Notion: status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
		}
		var parsed notionSearchResponse
		if err := json.Unmarshal(responseBody, &parsed); err != nil {
			return nil, fmt.Errorf("parse Notion search response: %w", err)
		}
		results = append(results, parsed.Results...)
		if !parsed.HasMore || parsed.NextCursor == "" {
			break
		}
		cursor = parsed.NextCursor
	}
	return results, nil
}

func validateNotionToken(token string, apiBaseURL string, client *http.Client) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("notion token is required")
	}
	body := bytes.NewReader([]byte(`{"page_size":1}`))
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(apiBaseURL, "/")+"/v1/search", body)
	if err != nil {
		return fmt.Errorf("build Notion validation request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Notion-Version", DefaultNotionAPIVersion)
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("validate Notion token: %w", err)
	}
	responseBody, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		return fmt.Errorf("read Notion validation response: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close Notion validation response: %w", closeErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("validate Notion token: status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var parsed notionSearchResponse
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return fmt.Errorf("parse Notion validation response: %w", err)
	}
	return nil
}

func notionPageContentPreview(config NotionCaptureConfig, pageID string) (string, error) {
	baseURL := resolveNotionAPIBaseURL(config.APIBaseURL)
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	var blocks []notionBlock
	cursor := ""
	for {
		requestURL := strings.TrimRight(baseURL, "/") + "/v1/blocks/" + url.PathEscape(pageID) + "/children?page_size=100"
		if cursor != "" {
			requestURL += "&start_cursor=" + url.QueryEscape(cursor)
		}
		request, err := http.NewRequest(http.MethodGet, requestURL, nil)
		if err != nil {
			return "", fmt.Errorf("build Notion block request: %w", err)
		}
		request.Header.Set("Authorization", "Bearer "+config.Token)
		request.Header.Set("Notion-Version", DefaultNotionAPIVersion)

		response, err := client.Do(request)
		if err != nil {
			return "", fmt.Errorf("fetch Notion page content: %w", err)
		}
		responseBody, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil {
			return "", fmt.Errorf("read Notion block response: %w", readErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close Notion block response: %w", closeErr)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return "", fmt.Errorf("fetch Notion page content: status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
		}
		var parsed notionBlockChildrenResponse
		if err := json.Unmarshal(responseBody, &parsed); err != nil {
			return "", fmt.Errorf("parse Notion block response: %w", err)
		}
		blocks = append(blocks, parsed.Results...)
		if !parsed.HasMore || parsed.NextCursor == "" {
			break
		}
		cursor = parsed.NextCursor
	}
	return notionContentPreview(blocks), nil
}

func notionContentPreview(blocks []notionBlock) string {
	var lines []string
	for _, block := range blocks {
		line := notionBlockPreviewLine(block)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return capNotionContentPreview(strings.Join(lines, "\n"), 4000)
}

func notionBlockPreviewLine(block notionBlock) string {
	switch block.Type {
	case "paragraph":
		return notionPlainText(block.Paragraph.RichText)
	case "heading_1":
		return prefixNotionPreview("# ", notionPlainText(block.Heading1.RichText))
	case "heading_2":
		return prefixNotionPreview("## ", notionPlainText(block.Heading2.RichText))
	case "heading_3":
		return prefixNotionPreview("### ", notionPlainText(block.Heading3.RichText))
	case "bulleted_list_item":
		return prefixNotionPreview("- ", notionPlainText(block.BulletedListItem.RichText))
	case "numbered_list_item":
		return prefixNotionPreview("1. ", notionPlainText(block.NumberedListItem.RichText))
	case "to_do":
		marker := "- [ ] "
		if block.ToDo.Checked {
			marker = "- [x] "
		}
		return prefixNotionPreview(marker, notionPlainText(block.ToDo.RichText))
	case "toggle":
		return prefixNotionPreview("Toggle: ", notionPlainText(block.Toggle.RichText))
	case "quote":
		return prefixNotionPreview("> ", notionPlainText(block.Quote.RichText))
	case "callout":
		return notionPlainText(block.Callout.RichText)
	case "child_page":
		return prefixNotionPreview("Page: ", block.ChildPage.Title)
	case "child_database":
		return prefixNotionPreview("Database: ", block.ChildDatabase.Title)
	default:
		return ""
	}
}

func prefixNotionPreview(prefix string, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return prefix + text
}

func capNotionContentPreview(preview string, limit int) string {
	preview = strings.TrimSpace(preview)
	if limit <= 0 || len(preview) <= limit {
		return preview
	}
	return strings.TrimSpace(preview[:limit]) + "\n..."
}

func storeNotionEvent(db *sql.DB, object notionSearchResult) (bool, error) {
	title := notionTitle(object)
	timestamp := object.LastEditedTime
	if timestamp == "" {
		timestamp = object.CreatedTime
	}
	parsedTime, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return false, fmt.Errorf("parse Notion timestamp %q: %w", object.ID, err)
	}
	eventType := "notion." + object.Object
	payload := notionEventPayload{
		Object:         object.Object,
		ID:             object.ID,
		Title:          title,
		URL:            object.URL,
		CreatedTime:    object.CreatedTime,
		CreatedBy:      object.CreatedBy.ID,
		LastEditedTime: object.LastEditedTime,
		LastEditedBy:   object.LastEditedBy.ID,
		Parent:         object.Parent,
		Properties:     object.Properties,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("encode Notion event payload: %w", err)
	}
	result, err := db.Exec(`INSERT INTO events (
			id, source, type, timestamp, payload_json, project, actor, summary, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		notionEventID(eventType, object.ID),
		"notion",
		eventType,
		parsedTime.UTC().Format(time.RFC3339Nano),
		string(payloadJSON),
		emptyStringAsNull(""),
		emptyStringAsNull(""),
		emptyStringAsNull(title),
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return false, fmt.Errorf("store Notion event: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store Notion event: %w", err)
	}
	return rows > 0, nil
}

func updateNotionIndexAndStoreActivity(db *sql.DB, object notionSearchResult, currentUserID string, config NotionCaptureConfig) (bool, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	title := notionTitle(object)
	propertiesJSON, err := json.Marshal(object.Properties)
	if err != nil {
		return false, fmt.Errorf("encode Notion properties snapshot: %w", err)
	}
	parentJSON := string(object.Parent)
	if parentJSON == "" {
		parentJSON = "{}"
	}
	createdTime, err := normalizeNotionTimestamp(object.CreatedTime)
	if err != nil {
		return false, err
	}
	lastEditedTime, err := normalizeNotionTimestamp(object.LastEditedTime)
	if err != nil {
		return false, err
	}

	previous, existed, err := readNotionIndexEntry(db, object.ID)
	if err != nil {
		return false, err
	}
	contentPreview := ""
	contentSyncedAt := ""
	shouldStoreActivity := existed && currentUserID != "" && object.LastEditedBy.ID == currentUserID && previous.LastEditedTime != lastEditedTime
	if shouldStoreActivity && object.Object == "page" {
		contentPreview, err = notionPageContentPreview(config, object.ID)
		if err != nil {
			return false, err
		}
		contentSyncedAt = now
	}
	_, err = db.Exec(`INSERT INTO notion_index (
			notion_id, object_type, title, url, parent_json, properties_json,
			content_preview, content_synced_at, created_time, created_by, last_edited_time, last_edited_by,
			source, first_seen_at, last_seen_at, last_synced_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(notion_id) DO UPDATE SET
			object_type = excluded.object_type,
			title = excluded.title,
			url = excluded.url,
			parent_json = excluded.parent_json,
			properties_json = excluded.properties_json,
			content_preview = COALESCE(excluded.content_preview, notion_index.content_preview),
			content_synced_at = COALESCE(excluded.content_synced_at, notion_index.content_synced_at),
			created_time = excluded.created_time,
			created_by = excluded.created_by,
			last_edited_time = excluded.last_edited_time,
			last_edited_by = excluded.last_edited_by,
			source = excluded.source,
			last_seen_at = excluded.last_seen_at,
			last_synced_at = excluded.last_synced_at`,
		object.ID,
		object.Object,
		emptyStringAsNull(title),
		emptyStringAsNull(object.URL),
		parentJSON,
		string(propertiesJSON),
		emptyStringAsNull(contentPreview),
		emptyStringAsNull(contentSyncedAt),
		emptyStringAsNull(createdTime),
		emptyStringAsNull(object.CreatedBy.ID),
		emptyStringAsNull(lastEditedTime),
		emptyStringAsNull(object.LastEditedBy.ID),
		"search",
		now,
		now,
		now,
	)
	if err != nil {
		return false, fmt.Errorf("store Notion index: %w", err)
	}
	if !shouldStoreActivity {
		return false, nil
	}
	return storeNotionActivityEvent(db, object, title, createdTime, lastEditedTime, contentPreview)
}

type notionIndexEntry struct {
	LastEditedTime string
}

func readNotionIndexEntry(db *sql.DB, notionID string) (notionIndexEntry, bool, error) {
	var entry notionIndexEntry
	err := db.QueryRow(`SELECT COALESCE(last_edited_time, '') FROM notion_index WHERE notion_id = ?`, notionID).Scan(&entry.LastEditedTime)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return notionIndexEntry{}, false, nil
		}
		return notionIndexEntry{}, false, fmt.Errorf("read Notion index: %w", err)
	}
	return entry, true, nil
}

func storeNotionActivityEvent(db *sql.DB, object notionSearchResult, title string, createdTime string, lastEditedTime string, contentPreview string) (bool, error) {
	eventType := "notion." + object.Object + "_updated"
	payload := notionEventPayload{
		Object:         object.Object,
		ID:             object.ID,
		Title:          title,
		URL:            object.URL,
		CreatedTime:    createdTime,
		CreatedBy:      object.CreatedBy.ID,
		LastEditedTime: lastEditedTime,
		LastEditedBy:   object.LastEditedBy.ID,
		Parent:         object.Parent,
		Properties:     object.Properties,
		ContentPreview: contentPreview,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("encode Notion activity payload: %w", err)
	}
	_, err = db.Exec(`INSERT INTO events (
			id, source, type, timestamp, payload_json, project, actor, summary, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		notionEventID(eventType, object.ID+":"+lastEditedTime),
		"notion",
		eventType,
		lastEditedTime,
		string(payloadJSON),
		emptyStringAsNull(""),
		emptyStringAsNull(object.LastEditedBy.ID),
		emptyStringAsNull(title),
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return false, fmt.Errorf("store Notion activity event: %w", err)
	}
	return true, nil
}

func notionTitle(object notionSearchResult) string {
	if object.Object == "database" {
		return notionPlainText(object.Title)
	}
	for _, property := range object.Properties {
		var parsed notionProperty
		if err := json.Unmarshal(property, &parsed); err != nil {
			continue
		}
		if parsed.Type == "title" {
			if title := notionPlainText(parsed.Title); title != "" {
				return title
			}
		}
	}
	return ""
}

func normalizeNotionTimestamp(timestamp string) (string, error) {
	if timestamp == "" {
		return "", nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return "", fmt.Errorf("parse Notion timestamp %q: %w", timestamp, err)
	}
	return parsed.UTC().Format(time.RFC3339Nano), nil
}

func notionOwnerUserID(owner json.RawMessage) string {
	if len(owner) == 0 {
		return ""
	}
	var parsed struct {
		Type string `json:"type"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(owner, &parsed); err != nil {
		return ""
	}
	if parsed.Type == "user" {
		return parsed.User.ID
	}
	return ""
}

func notionPlainText(parts []notionRichText) string {
	var values []string
	for _, part := range parts {
		if part.PlainText != "" {
			values = append(values, part.PlainText)
		}
	}
	return strings.TrimSpace(strings.Join(values, ""))
}

func notionEventID(eventType string, objectID string) string {
	return fmt.Sprintf("%s:%s", eventType, objectID)
}

func notionCaptureMessage(result NotionCaptureResult) string {
	return strings.Join([]string{
		"Notion capture complete",
		"Home: " + result.HomeDir,
		"Database: " + result.DatabasePath,
		fmt.Sprintf("Events stored: %d", result.EventsStored),
	}, "\n")
}

func openNotionIndexDatabase(homeDir string, databasePath string) (*sql.DB, error) {
	status, err := prepareRunStatus(RunConfig{
		HomeDir:      homeDir,
		DatabasePath: databasePath,
	})
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := createSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("create database schema: %w", err)
	}
	return db, nil
}

func notionIndexListMessage(items []NotionIndexItem) string {
	lines := []string{"Notion index", fmt.Sprintf("%s indexed", pluralize(len(items), "item"))}
	if len(items) == 0 {
		lines = append(lines, "No Notion objects indexed yet.")
		return strings.Join(lines, "\n")
	}
	for _, item := range items {
		title := item.Title
		if title == "" {
			title = "(untitled)"
		}
		lines = append(lines, fmt.Sprintf("- %s %s %s", item.ID, item.ObjectType, title))
		if item.LastEditedTime != "" {
			lines = append(lines, "  last edited: "+item.LastEditedTime)
		}
	}
	return strings.Join(lines, "\n")
}

func notionIndexShowMessage(item NotionIndexItem) string {
	lines := []string{
		"Notion index item",
		"ID: " + item.ID,
		"Type: " + item.ObjectType,
	}
	if item.Title != "" {
		lines = append(lines, "Title: "+item.Title)
	}
	if item.URL != "" {
		lines = append(lines, "URL: "+item.URL)
	}
	if item.LastEditedTime != "" {
		lines = append(lines, "Last edited: "+item.LastEditedTime)
	}
	if item.LastEditedBy != "" {
		lines = append(lines, "Last edited by: "+item.LastEditedBy)
	}
	if item.ContentSyncedAt != "" {
		lines = append(lines, "Content synced: "+item.ContentSyncedAt)
	}
	if strings.TrimSpace(item.ContentPreview) != "" {
		lines = append(lines, "", "Content preview:")
		lines = append(lines, item.ContentPreview)
	}
	return strings.Join(lines, "\n")
}

func notionAuthorizationURL(config NotionConnectConfig, state string) string {
	baseURL := config.AuthBaseURL
	if baseURL == "" {
		baseURL = "https://api.notion.com/v1/oauth/authorize"
	}
	values := url.Values{}
	values.Set("client_id", config.ClientID)
	values.Set("redirect_uri", config.RedirectURI)
	values.Set("response_type", "code")
	values.Set("owner", "user")
	values.Set("state", state)
	return baseURL + "?" + values.Encode()
}

func exchangeNotionOAuthCode(config NotionConnectConfig) (notionOAuthTokenResponse, error) {
	tokenURL := resolveNotionTokenURL(config.TokenURL)
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", config.Code)
	form.Set("client_id", config.ClientID)
	form.Set("redirect_uri", config.RedirectURI)

	request, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return notionOAuthTokenResponse{}, fmt.Errorf("build Notion token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return notionOAuthTokenResponse{}, fmt.Errorf("exchange Notion OAuth code: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return notionOAuthTokenResponse{}, fmt.Errorf("read Notion OAuth response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return notionOAuthTokenResponse{}, fmt.Errorf("exchange Notion OAuth code: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var token notionOAuthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return notionOAuthTokenResponse{}, fmt.Errorf("parse Notion OAuth response: %w", err)
	}
	return token, nil
}

func storeNotionConnection(homeDir string, config NotionConnectConfig, token notionOAuthTokenResponse) (NotionConnectResult, error) {
	if token.AccessToken == "" {
		return NotionConnectResult{}, errors.New("notion oauth response did not include an access token")
	}
	if err := enforceConnectorManagedSettings("notion"); err != nil {
		return NotionConnectResult{}, err
	}
	configPath := notionConfigPath(homeDir)
	stored := notionConnectorConfig{
		AccessToken:          token.AccessToken,
		RefreshToken:         token.RefreshToken,
		TokenType:            token.TokenType,
		ExpiresAt:            notionTokenExpiresAt(token),
		WorkspaceID:          token.WorkspaceID,
		WorkspaceName:        token.WorkspaceName,
		WorkspaceIcon:        token.WorkspaceIcon,
		BotID:                token.BotID,
		Owner:                token.Owner,
		DuplicatedTemplateID: token.DuplicatedTemplateID,
		APIBaseURL:           resolveNotionAPIBaseURL(config.APIBaseURL),
		ClientID:             resolveNotionClientID(config.ClientID),
		TokenURL:             resolveNotionTokenURL(config.TokenURL),
	}
	if err := writeNotionConnectorConfig(configPath, stored); err != nil {
		return NotionConnectResult{}, err
	}
	if _, err := connectRuntimeConnector(homeDir, "notion", ""); err != nil {
		return NotionConnectResult{}, err
	}
	return NotionConnectResult{
		ConfigPath: configPath,
		Configured: true,
		Message: strings.Join([]string{
			"Notion connected",
			"Config: " + configPath,
		}, "\n"),
	}, nil
}

func notionOAuthCallbackHandler(expectedState string, codeCh chan<- string, errCh chan<- error) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if notionErr := query.Get("error"); notionErr != "" {
			http.Error(response, "Notion authorization failed.", http.StatusBadRequest)
			errCh <- fmt.Errorf("notion oauth: %s", notionErr)
			return
		}
		if query.Get("state") != expectedState {
			http.Error(response, "Notion authorization state did not match.", http.StatusBadRequest)
			errCh <- errors.New("notion oauth state did not match")
			return
		}
		code := query.Get("code")
		if code == "" {
			http.Error(response, "Notion authorization did not include a code.", http.StatusBadRequest)
			errCh <- errors.New("notion oauth code missing")
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(response, "<!doctype html><title>workgraph Notion Authorization Received</title><p>%s</p>", html.EscapeString("Notion authorization received. Return to workgraph to confirm the connection completed."))
		codeCh <- code
	})
}

func resolveNotionHomeDir(homeDir string) (string, error) {
	resolved, err := resolveHomeDir(homeDir)
	if err != nil {
		return "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve workgraph home: %w", err)
	}
	if _, err := os.Stat(filepath.Join(resolved, "workgraph.db")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: run workgraph init", ErrNotInitialized)
		}
		return "", fmt.Errorf("check database: %w", err)
	}
	return resolved, nil
}

func readNotionConnectorConfig(homeDir string) (notionConnectorConfig, error) {
	path := notionConfigPath(homeDir)
	contents, err := os.ReadFile(path)
	if err != nil {
		return notionConnectorConfig{}, err
	}
	var config notionConnectorConfig
	if err := json.Unmarshal(contents, &config); err != nil {
		return notionConnectorConfig{}, fmt.Errorf("parse notion config: %w", err)
	}
	return config, nil
}

func writeNotionConnectorConfig(path string, config notionConnectorConfig) error {
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode notion config: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		return fmt.Errorf("write notion config: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure notion config: %w", err)
	}
	return nil
}

func notionConnected(homeDir string) (bool, error) {
	_, err := readNotionConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func notionAlreadyConnectedResult(homeDir string) NotionConnectResult {
	configPath := notionConfigPath(homeDir)
	return NotionConnectResult{
		ConfigPath: configPath,
		Configured: true,
		Message: strings.Join([]string{
			"Notion is already connected",
			"Config: " + configPath,
		}, "\n"),
	}
}

func notionAlreadyDisconnectedResult(homeDir string) NotionDisconnectResult {
	configPath := notionConfigPath(homeDir)
	return NotionDisconnectResult{
		ConfigPath: configPath,
		Message: strings.Join([]string{
			"Notion is not connected",
			"No local Notion connector settings changed.",
		}, "\n"),
	}
}

func notionConfigPath(homeDir string) string {
	return filepath.Join(homeDir, "notion.json")
}

func resolveNotionClientID(clientID string) string {
	if clientID != "" {
		return clientID
	}
	return DefaultNotionClientID
}

func resolveNotionTokenURL(tokenURL string) string {
	if tokenURL != "" {
		return tokenURL
	}
	return DefaultNotionTokenURL
}

func resolveNotionAPIBaseURL(apiBaseURL string) string {
	if apiBaseURL != "" {
		return apiBaseURL
	}
	return DefaultNotionAPIBaseURL
}

func notionTokenExpiresAt(token notionOAuthTokenResponse) string {
	if token.ExpiresIn <= 0 {
		return ""
	}
	return time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
}
