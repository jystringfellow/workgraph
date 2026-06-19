package workgraph

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var DefaultAzureBoardsClientID = DefaultMicrosoftCalendarClientID

const DefaultAzureBoardsRedirectURI = "http://localhost:2727/azure/boards/callback"

var DefaultAzureBoardsTokenURL = DefaultMicrosoftCalendarTokenURL

type AzureBoardsConnectConfig struct {
	HomeDir       string
	ClientID      string
	RedirectURI   string
	Code          string
	CodeVerifier  string
	State         string
	ExpectedState string
	Organization  string
	Project       string
	Team          string
	AreaPaths     []string
	WIQL          string
	AuthBaseURL   string
	TokenURL      string
	APIBaseURL    string
	HTTPClient    *http.Client
	OpenBrowser   func(string) error
}

type AzureBoardsCaptureConfig struct {
	HomeDir      string
	DatabasePath string
	Token        string
	Organization string
	Project      string
	Team         string
	AreaPaths    []string
	WIQL         string
	APIBaseURL   string
	HTTPClient   *http.Client
}

type AzureBoardsResult struct {
	ConfigPath       string
	HomeDir          string
	DatabasePath     string
	EventsStored     int
	Message          string
	AuthorizationURL string
	State            string
	Configured       bool
	DaemonRestarted  bool
}

type azureBoardsConnectorConfig struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	TokenType    string   `json:"token_type,omitempty"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	Organization string   `json:"organization"`
	Project      string   `json:"project"`
	Team         string   `json:"team,omitempty"`
	AreaPaths    []string `json:"area_paths,omitempty"`
	WIQL         string   `json:"wiql,omitempty"`
	APIBaseURL   string   `json:"api_base_url,omitempty"`
	ClientID     string   `json:"client_id,omitempty"`
	TokenURL     string   `json:"token_url,omitempty"`
}

type azureBoardsWIQLResponse struct {
	WorkItems []struct {
		ID int `json:"id"`
	} `json:"workItems"`
}

type azureBoardsBatchResponse struct {
	Value []azureBoardsWorkItem `json:"value"`
}

type azureBoardsWorkItem struct {
	ID     int            `json:"id"`
	URL    string         `json:"url"`
	Fields map[string]any `json:"fields"`
	Links  map[string]any `json:"_links,omitempty"`
}

func ConnectAzureBoards(config AzureBoardsConnectConfig) (AzureBoardsResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return AzureBoardsResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return AzureBoardsResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "workgraph.db")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return AzureBoardsResult{}, fmt.Errorf("%w: run workgraph init", ErrNotInitialized)
		}
		return AzureBoardsResult{}, fmt.Errorf("check database: %w", err)
	}
	if config.Code == "" {
		if connected, err := azureBoardsConnected(homeDir); err != nil {
			return AzureBoardsResult{}, err
		} else if connected {
			if _, err := connectRuntimeConnector(homeDir, "azure.boards", ""); err != nil {
				return AzureBoardsResult{}, err
			}
			return AzureBoardsResult{ConfigPath: azureBoardsConfigPath(homeDir), Configured: true, Message: "Azure Boards is already connected\nConfig: " + azureBoardsConfigPath(homeDir)}, nil
		}
	}
	if strings.TrimSpace(config.Organization) == "" {
		return AzureBoardsResult{}, errors.New("azure boards organization is required")
	}
	if strings.TrimSpace(config.Project) == "" {
		return AzureBoardsResult{}, errors.New("azure boards project is required")
	}
	config.ClientID = resolveAzureBoardsClientID(config.ClientID)
	if config.RedirectURI == "" {
		config.RedirectURI = DefaultAzureBoardsRedirectURI
	}
	state := config.State
	if state == "" {
		generated, err := newEventID()
		if err != nil {
			return AzureBoardsResult{}, fmt.Errorf("create oauth state: %w", err)
		}
		state = generated
	}
	verifier := config.CodeVerifier
	if verifier == "" {
		generated, err := randomURLToken(48)
		if err != nil {
			return AzureBoardsResult{}, fmt.Errorf("create oauth verifier: %w", err)
		}
		verifier = generated
	}
	result := AzureBoardsResult{ConfigPath: azureBoardsConfigPath(homeDir), AuthorizationURL: azureBoardsAuthorizationURL(config, state, slackPKCEChallenge(verifier)), State: state}
	if config.Code == "" {
		result.Message = strings.Join([]string{
			"Azure Boards OAuth authorization URL",
			result.AuthorizationURL,
			"State: " + result.State,
			"Code verifier: " + verifier,
			"After Microsoft redirects back with a code, rerun azure boards connect with --code, --state, and --code-verifier.",
		}, "\n")
		return result, nil
	}
	if config.ExpectedState != "" && config.State != "" && config.State != config.ExpectedState {
		return AzureBoardsResult{}, errors.New("azure boards oauth state did not match")
	}
	token, err := exchangeAzureBoardsOAuthCode(config, verifier)
	if err != nil {
		return AzureBoardsResult{}, err
	}
	return storeAzureBoardsConnection(homeDir, config, token)
}

func ConnectAzureBoardsWithBrowser(ctx context.Context, config AzureBoardsConnectConfig) (AzureBoardsResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return AzureBoardsResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return AzureBoardsResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	if connected, err := azureBoardsConnected(homeDir); err != nil {
		return AzureBoardsResult{}, err
	} else if connected {
		if _, err := connectRuntimeConnector(homeDir, "azure.boards", ""); err != nil {
			return AzureBoardsResult{}, err
		}
		return AzureBoardsResult{ConfigPath: azureBoardsConfigPath(homeDir), Configured: true, Message: "Azure Boards is already connected\nConfig: " + azureBoardsConfigPath(homeDir)}, nil
	}
	config.HomeDir = homeDir
	if config.RedirectURI == "" {
		config.RedirectURI = DefaultAzureBoardsRedirectURI
	}
	parsed, err := url.Parse(config.RedirectURI)
	if err != nil {
		return AzureBoardsResult{}, fmt.Errorf("parse azure boards redirect URI: %w", err)
	}
	if !isLocalHTTPRedirect(parsed) {
		return AzureBoardsResult{}, errors.New("azure boards redirect URI must be an http localhost URL")
	}
	listener, err := net.Listen("tcp", parsed.Host)
	if err != nil {
		return AzureBoardsResult{}, fmt.Errorf("start azure boards oauth callback: %w", err)
	}
	defer listener.Close()
	state, err := randomURLToken(24)
	if err != nil {
		return AzureBoardsResult{}, err
	}
	verifier, err := randomURLToken(48)
	if err != nil {
		return AzureBoardsResult{}, err
	}
	config.State = state
	config.CodeVerifier = verifier
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	server := &http.Server{Handler: calendarOAuthCallbackHandler("Azure Boards", state, codeCh, errCh)}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	defer server.Shutdown(context.Background())
	authURL := azureBoardsAuthorizationURL(config, state, slackPKCEChallenge(verifier))
	openBrowser := config.OpenBrowser
	if openBrowser == nil {
		openBrowser = openURLInBrowser
	}
	if err := openBrowser(authURL); err != nil {
		return AzureBoardsResult{}, err
	}
	select {
	case <-ctx.Done():
		return AzureBoardsResult{}, ctx.Err()
	case err := <-errCh:
		return AzureBoardsResult{}, err
	case code := <-codeCh:
		config.Code = code
		config.ExpectedState = state
		return ConnectAzureBoards(config)
	}
}

func CaptureAzureBoards(config AzureBoardsCaptureConfig) (AzureBoardsResult, error) {
	status, err := prepareRunStatus(RunConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return AzureBoardsResult{}, err
	}
	config.HomeDir = status.HomeDir
	if config.DatabasePath == "" {
		config.DatabasePath = status.DatabasePath
	}
	if config.Token == "" {
		stored, err := readAzureBoardsConnectorConfig(status.HomeDir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return AzureBoardsResult{}, errors.New("azure boards token is required: run workgraph azure boards connect or pass --token")
			}
			return AzureBoardsResult{}, err
		}
		config.Token = stored.AccessToken
		config.Organization = firstNonEmpty(config.Organization, stored.Organization)
		config.Project = firstNonEmpty(config.Project, stored.Project)
		config.Team = firstNonEmpty(config.Team, stored.Team)
		if len(config.AreaPaths) == 0 {
			config.AreaPaths = append([]string(nil), stored.AreaPaths...)
		}
		config.WIQL = firstNonEmpty(config.WIQL, stored.WIQL)
		config.APIBaseURL = firstNonEmpty(config.APIBaseURL, stored.APIBaseURL)
	}
	if config.Token == "" || config.Organization == "" || config.Project == "" {
		return AzureBoardsResult{}, errors.New("azure boards token, organization, and project are required")
	}
	ids, err := azureBoardsQueryIDs(config)
	if err != nil {
		return AzureBoardsResult{}, err
	}
	items, err := azureBoardsFetchWorkItems(config, ids)
	if err != nil {
		return AzureBoardsResult{}, err
	}
	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return AzureBoardsResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	stored := 0
	for _, item := range items {
		inserted, err := storeAzureBoardsWorkItem(db, config, item)
		if err != nil {
			return AzureBoardsResult{}, err
		}
		if inserted {
			stored++
		}
	}
	result := AzureBoardsResult{HomeDir: status.HomeDir, DatabasePath: status.DatabasePath, EventsStored: stored}
	result.Message = strings.Join([]string{"Azure Boards capture complete", "Home: " + status.HomeDir, "Database: " + status.DatabasePath, fmt.Sprintf("Events stored: %d", stored)}, "\n")
	return result, nil
}

func DisconnectAzureBoards(config AzureBoardsCaptureConfig) (AzureBoardsResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return AzureBoardsResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return AzureBoardsResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	path := azureBoardsConfigPath(homeDir)
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return AzureBoardsResult{}, errors.New("Azure Boards is not connected")
		}
		return AzureBoardsResult{}, fmt.Errorf("remove azure boards config: %w", err)
	}
	if err := clearRuntimeConnector(homeDir, "azure.boards"); err != nil {
		return AzureBoardsResult{}, err
	}
	daemonRestarted, err := restartDaemonAfterAzureBoardsUpdate(homeDir)
	if err != nil {
		return AzureBoardsResult{}, err
	}
	lines := []string{"Azure Boards disconnected", "Config removed: " + path}
	if daemonRestarted {
		lines = append(lines, "Background capture restarted to apply Azure Boards settings.")
	}
	return AzureBoardsResult{ConfigPath: path, DaemonRestarted: daemonRestarted, Message: strings.Join(lines, "\n")}, nil
}

func azureBoardsAuthorizationURL(config AzureBoardsConnectConfig, state, challenge string) string {
	baseURL := config.AuthBaseURL
	if baseURL == "" {
		baseURL = "https://login.microsoftonline.com/common/oauth2/v2.0/authorize"
	}
	values := url.Values{}
	values.Set("client_id", config.ClientID)
	values.Set("redirect_uri", config.RedirectURI)
	values.Set("response_type", "code")
	values.Set("scope", strings.Join(azureBoardsScopes(), " "))
	values.Set("state", state)
	if challenge != "" {
		values.Set("code_challenge", challenge)
		values.Set("code_challenge_method", "S256")
	}
	return baseURL + "?" + values.Encode()
}

func exchangeAzureBoardsOAuthCode(config AzureBoardsConnectConfig, verifier string) (googleOAuthTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", config.Code)
	form.Set("client_id", config.ClientID)
	form.Set("redirect_uri", config.RedirectURI)
	form.Set("code_verifier", verifier)
	request, err := http.NewRequest(http.MethodPost, resolveAzureBoardsTokenURL(config.TokenURL), strings.NewReader(form.Encode()))
	if err != nil {
		return googleOAuthTokenResponse{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("exchange Azure Boards OAuth code: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return googleOAuthTokenResponse{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return googleOAuthTokenResponse{}, fmt.Errorf("exchange Azure Boards OAuth code: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var token googleOAuthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return googleOAuthTokenResponse{}, err
	}
	return token, nil
}

func storeAzureBoardsConnection(homeDir string, config AzureBoardsConnectConfig, token googleOAuthTokenResponse) (AzureBoardsResult, error) {
	if token.AccessToken == "" {
		return AzureBoardsResult{}, errors.New("azure boards oauth response did not include an access token")
	}
	stored := azureBoardsConnectorConfig{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, TokenType: token.TokenType, ExpiresAt: googleCalendarTokenExpiresAt(token), Scopes: strings.Fields(token.Scope), Organization: config.Organization, Project: config.Project, Team: config.Team, AreaPaths: config.AreaPaths, WIQL: config.WIQL, APIBaseURL: config.APIBaseURL, ClientID: config.ClientID, TokenURL: resolveAzureBoardsTokenURL(config.TokenURL)}
	path := azureBoardsConfigPath(homeDir)
	if err := writeJSON0600(path, stored); err != nil {
		return AzureBoardsResult{}, err
	}
	if _, err := connectRuntimeConnector(homeDir, "azure.boards", ""); err != nil {
		return AzureBoardsResult{}, err
	}
	daemonRestarted, err := restartDaemonAfterAzureBoardsUpdate(homeDir)
	if err != nil {
		return AzureBoardsResult{}, err
	}
	lines := []string{"Azure Boards connected", "Config: " + path}
	if daemonRestarted {
		lines = append(lines, "Background capture restarted to apply Azure Boards settings.")
	}
	return AzureBoardsResult{ConfigPath: path, Configured: true, DaemonRestarted: daemonRestarted, Message: strings.Join(lines, "\n")}, nil
}

func restartDaemonAfterAzureBoardsUpdate(homeDir string) (bool, error) {
	status, err := DaemonStatusForHome(homeDir)
	if err != nil {
		return false, err
	}
	if !status.Running {
		return false, nil
	}
	config := DaemonConfig{
		HomeDir:      status.HomeDir,
		DatabasePath: status.DatabasePath,
		WatchDirs:    append([]string(nil), status.WatchDirs...),
	}
	if _, err := StopDaemon(config); err != nil {
		return false, err
	}
	if _, err := StartDaemon(config); err != nil {
		return false, err
	}
	return true, nil
}

func azureBoardsQueryIDs(config AzureBoardsCaptureConfig) ([]int, error) {
	query := config.WIQL
	if strings.TrimSpace(query) == "" {
		query = defaultAzureBoardsWIQL(config)
	}
	body, _ := json.Marshal(map[string]string{"query": query})
	request, err := http.NewRequest(http.MethodPost, azureBoardsAPIBase(config)+"/"+url.PathEscape(config.Organization)+"/"+url.PathEscape(config.Project)+"/_apis/wit/wiql?api-version=7.1", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	azureBoardsAuthorize(request, config.Token)
	request.Header.Set("Content-Type", "application/json")
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query Azure Boards WIQL: %w", err)
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("query Azure Boards WIQL: status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var parsed azureBoardsWIQLResponse
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(parsed.WorkItems))
	for _, item := range parsed.WorkItems {
		ids = append(ids, item.ID)
	}
	return ids, nil
}

func azureBoardsFetchWorkItems(config AzureBoardsCaptureConfig, ids []int) ([]azureBoardsWorkItem, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	body, _ := json.Marshal(map[string]any{"ids": ids, "fields": []string{"System.Id", "System.Title", "System.State", "System.WorkItemType", "System.AssignedTo", "System.ChangedDate", "System.Tags", "System.IterationPath", "System.AreaPath", "Microsoft.VSTS.Common.Priority"}})
	request, err := http.NewRequest(http.MethodPost, azureBoardsAPIBase(config)+"/"+url.PathEscape(config.Organization)+"/"+url.PathEscape(config.Project)+"/_apis/wit/workitemsbatch?api-version=7.1", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	azureBoardsAuthorize(request, config.Token)
	request.Header.Set("Content-Type", "application/json")
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch Azure Boards work items: %w", err)
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch Azure Boards work items: status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var parsed azureBoardsBatchResponse
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return nil, err
	}
	return parsed.Value, nil
}

func storeAzureBoardsWorkItem(db *sql.DB, config AzureBoardsCaptureConfig, item azureBoardsWorkItem) (bool, error) {
	fieldsJSON, _ := json.Marshal(item.Fields)
	title := azureFieldString(item.Fields, "System.Title")
	changed := azureFieldString(item.Fields, "System.ChangedDate")
	if changed == "" {
		changed = time.Now().UTC().Format(time.RFC3339Nano)
	}
	payload, _ := json.Marshal(map[string]any{"id": item.ID, "url": item.URL, "organization": config.Organization, "project": config.Project, "team": config.Team, "area_paths": config.AreaPaths, "fields": json.RawMessage(fieldsJSON)})
	result, err := db.Exec(`INSERT OR IGNORE INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, fmt.Sprintf("azure_boards.work_item:%d", item.ID), "azure_boards", "azure_boards.work_item", normalizeAzureTime(changed), string(payload), config.Project, azureIdentityName(item.Fields["System.AssignedTo"]), title, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	rows, _ := result.RowsAffected()
	return rows > 0, nil
}

func defaultAzureBoardsWIQL(config AzureBoardsCaptureConfig) string {
	clauses := []string{"[System.TeamProject] = @Project", "[System.AssignedTo] = @Me", "[System.State] <> 'Closed'", "[System.State] <> 'Removed'"}
	areaClauses := []string{}
	for _, area := range config.AreaPaths {
		if strings.TrimSpace(area) != "" {
			areaClauses = append(areaClauses, fmt.Sprintf("[System.AreaPath] UNDER '%s'", strings.ReplaceAll(area, "'", "''")))
		}
	}
	if len(areaClauses) > 0 {
		clauses = append(clauses, "("+strings.Join(areaClauses, " OR ")+")")
	}
	return "SELECT [System.Id] FROM WorkItems WHERE " + strings.Join(clauses, " AND ") + " ORDER BY [System.ChangedDate] DESC"
}

func azureBoardsScopes() []string {
	return []string{"openid", "profile", "email", "offline_access", "499b84ac-1321-427f-aa17-267ca6975798/.default"}
}
func azureBoardsConfigPath(homeDir string) string { return filepath.Join(homeDir, "azure-boards.json") }
func resolveAzureBoardsClientID(id string) string {
	if id != "" {
		return id
	}
	return DefaultAzureBoardsClientID
}
func resolveAzureBoardsTokenURL(v string) string {
	if v != "" {
		return v
	}
	return DefaultAzureBoardsTokenURL
}
func azureBoardsAPIBase(config AzureBoardsCaptureConfig) string {
	if config.APIBaseURL != "" {
		return strings.TrimRight(config.APIBaseURL, "/")
	}
	return "https://dev.azure.com"
}
func azureBoardsConnected(homeDir string) (bool, error) {
	c, err := readAzureBoardsConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return c.AccessToken != "", nil
}
func readAzureBoardsConnectorConfig(homeDir string) (azureBoardsConnectorConfig, error) {
	var c azureBoardsConnectorConfig
	b, err := os.ReadFile(azureBoardsConfigPath(homeDir))
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	return c, err
}
func writeJSON0600(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
func azureBoardsAuthorize(r *http.Request, token string) {
	r.Header.Set("Authorization", "Bearer "+token)
}
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
func azureFieldString(fields map[string]any, key string) string {
	if v, ok := fields[key].(string); ok {
		return v
	}
	return ""
}
func normalizeAzureTime(value string) string {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Now().UTC().Format(time.RFC3339Nano)
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func azureIdentityName(value any) string {
	if m, ok := value.(map[string]any); ok {
		if n, ok := m["displayName"].(string); ok {
			return n
		}
	}
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
