package workgraph

import (
	"context"
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
)

// DefaultNotionClientID is the OAuth client id used for the workgraph Notion public connection.
var DefaultNotionClientID = "378d872b-594c-8110-b4c0-0037422697b3"

// DefaultNotionRedirectURI is the local redirect URI registered for the workgraph Notion public connection.
const DefaultNotionRedirectURI = "http://localhost:2727/notion/callback"

// DefaultNotionTokenURL is the workgraph OAuth token relay for Notion.
var DefaultNotionTokenURL = "https://workgraph-notion-oauth-token.jystringfellow.workers.dev/notion/token"

// DefaultNotionAPIBaseURL is Notion's API base URL.
var DefaultNotionAPIBaseURL = "https://api.notion.com"

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

// NotionDisconnectConfig controls Notion disconnect behavior.
type NotionDisconnectConfig struct {
	HomeDir string
}

// NotionDisconnectResult describes Notion disconnect behavior.
type NotionDisconnectResult struct {
	ConfigPath string
	Message    string
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

// ConnectNotion prepares or completes Notion OAuth setup.
func ConnectNotion(config NotionConnectConfig) (NotionConnectResult, error) {
	homeDir, err := resolveNotionHomeDir(config.HomeDir)
	if err != nil {
		return NotionConnectResult{}, err
	}
	if config.Code == "" {
		if connected, err := notionConnected(homeDir); err != nil {
			return NotionConnectResult{}, err
		} else if connected {
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

// ConnectNotionWithBrowser completes Notion OAuth with a local callback.
func ConnectNotionWithBrowser(ctx context.Context, config NotionConnectConfig) (NotionConnectResult, error) {
	homeDir, err := resolveNotionHomeDir(config.HomeDir)
	if err != nil {
		return NotionConnectResult{}, err
	}
	if connected, err := notionConnected(homeDir); err != nil {
		return NotionConnectResult{}, err
	} else if connected {
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
	return NotionDisconnectResult{
		ConfigPath: configPath,
		Message: strings.Join([]string{
			"Notion disconnected",
			"Config removed: " + configPath,
			"To fully revoke access, remove workgraph from your Notion workspace connection settings.",
		}, "\n"),
	}, nil
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
