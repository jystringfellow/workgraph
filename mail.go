package workgraph

import (
	"context"
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
)

// DefaultGoogleMailClientID is the OAuth client id used for Google Mail PKCE OAuth.
var DefaultGoogleMailClientID = DefaultGoogleCalendarClientID

// DefaultGoogleMailRedirectURI is used by manual Google Mail OAuth flows.
const DefaultGoogleMailRedirectURI = "http://127.0.0.1:2727/mail/google/callback"

// DefaultGoogleMailTokenURL is the workgraph OAuth token relay for Google Mail.
var DefaultGoogleMailTokenURL = DefaultGoogleCalendarTokenURL

// MailConnectConfig controls mail provider OAuth setup.
type MailConnectConfig struct {
	HomeDir       string
	Provider      string
	ClientID      string
	RedirectURI   string
	Code          string
	CodeVerifier  string
	State         string
	ExpectedState string
	AuthBaseURL   string
	TokenURL      string
	APIBaseURL    string
	HTTPClient    *http.Client
	OpenBrowser   func(string) error
}

// MailConnectResult describes mail provider OAuth setup.
type MailConnectResult struct {
	ConfigPath       string
	AuthorizationURL string
	State            string
	Configured       bool
	Message          string
}

type mailConnectorConfig struct {
	Google *googleMailConnectorConfig `json:"google,omitempty"`
}

type googleMailConnectorConfig struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	TokenType    string   `json:"token_type,omitempty"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	APIBaseURL   string   `json:"api_base_url,omitempty"`
	ClientID     string   `json:"client_id,omitempty"`
	TokenURL     string   `json:"token_url,omitempty"`
}

// ConnectMail prepares or completes mail provider OAuth setup.
func ConnectMail(config MailConnectConfig) (MailConnectResult, error) {
	switch strings.ToLower(config.Provider) {
	case "google":
		return connectGoogleMail(config)
	default:
		return MailConnectResult{}, fmt.Errorf("unsupported mail provider %q", config.Provider)
	}
}

// ConnectMailWithBrowser completes mail provider OAuth with a local callback and PKCE.
func ConnectMailWithBrowser(ctx context.Context, config MailConnectConfig) (MailConnectResult, error) {
	provider := strings.ToLower(config.Provider)
	if provider != "google" {
		return MailConnectResult{}, fmt.Errorf("unsupported mail provider %q", config.Provider)
	}
	homeDir, err := mailHomeDir(config.HomeDir)
	if err != nil {
		return MailConnectResult{}, err
	}
	if connected, err := mailProviderConnected(homeDir, provider); err != nil {
		return MailConnectResult{}, err
	} else if connected {
		return mailAlreadyConnectedResult(homeDir, provider), nil
	}

	config.ClientID = resolveGoogleMailClientID(config.ClientID)
	if config.ClientID == "" {
		return MailConnectResult{}, errors.New("google mail client id is required for browser connect")
	}

	listenAddress := "127.0.0.1:0"
	redirectPath := ""
	if config.RedirectURI != "" {
		parsedRedirect, err := url.Parse(config.RedirectURI)
		if err != nil {
			return MailConnectResult{}, fmt.Errorf("parse google mail redirect URI: %w", err)
		}
		if !isLocalHTTPRedirect(parsedRedirect) {
			return MailConnectResult{}, errors.New("google mail redirect URI must be an http localhost URL")
		}
		listenAddress = parsedRedirect.Host
		redirectPath = parsedRedirect.EscapedPath()
	}

	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return MailConnectResult{}, fmt.Errorf("start google mail oauth callback: %w", err)
	}
	defer listener.Close()
	if config.RedirectURI == "" || strings.HasSuffix(listenAddress, ":0") {
		config.RedirectURI = "http://" + listener.Addr().String() + redirectPath
	}

	state, err := randomURLToken(24)
	if err != nil {
		return MailConnectResult{}, fmt.Errorf("create oauth state: %w", err)
	}
	verifier, err := randomURLToken(48)
	if err != nil {
		return MailConnectResult{}, fmt.Errorf("create oauth verifier: %w", err)
	}
	config.State = state
	authURL := googleMailAuthorizationURLWithPKCE(config, state, slackPKCEChallenge(verifier))

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	server := &http.Server{
		Handler: calendarOAuthCallbackHandler(mailProviderDisplayName(provider), state, codeCh, errCh),
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
		return MailConnectResult{}, err
	}

	var code string
	select {
	case <-ctx.Done():
		return MailConnectResult{}, ctx.Err()
	case err := <-errCh:
		return MailConnectResult{}, err
	case code = <-codeCh:
	}

	config.HomeDir = homeDir
	config.Code = code
	config.ExpectedState = state
	token, err := exchangeGoogleMailOAuthCodeWithPKCE(config, verifier)
	if err != nil {
		return MailConnectResult{}, err
	}
	result, err := storeGoogleMailConnection(homeDir, config, token)
	if err != nil {
		return MailConnectResult{}, err
	}
	result.AuthorizationURL = authURL
	result.State = state
	return result, nil
}

func connectGoogleMail(config MailConnectConfig) (MailConnectResult, error) {
	homeDir, err := mailHomeDir(config.HomeDir)
	if err != nil {
		return MailConnectResult{}, err
	}
	if config.Code == "" {
		if connected, err := mailProviderConnected(homeDir, "google"); err != nil {
			return MailConnectResult{}, err
		} else if connected {
			return mailAlreadyConnectedResult(homeDir, "google"), nil
		}
	}
	config.ClientID = resolveGoogleMailClientID(config.ClientID)
	if config.ClientID == "" {
		return MailConnectResult{}, errors.New("google mail client id is required")
	}
	if config.RedirectURI == "" {
		config.RedirectURI = DefaultGoogleMailRedirectURI
	}

	state := config.State
	if state == "" {
		generated, err := newEventID()
		if err != nil {
			return MailConnectResult{}, fmt.Errorf("create oauth state: %w", err)
		}
		state = generated
	}
	verifier := config.CodeVerifier
	if verifier == "" {
		generated, err := randomURLToken(48)
		if err != nil {
			return MailConnectResult{}, fmt.Errorf("create oauth verifier: %w", err)
		}
		verifier = generated
	}
	result := MailConnectResult{
		ConfigPath:       mailConfigPath(homeDir),
		AuthorizationURL: googleMailAuthorizationURLWithPKCE(config, state, slackPKCEChallenge(verifier)),
		State:            state,
	}
	if config.Code == "" {
		result.Message = strings.Join([]string{
			"Google Mail OAuth authorization URL",
			result.AuthorizationURL,
			"State: " + result.State,
			"Code verifier: " + verifier,
			"After Google redirects back with a code, rerun mail connect google with --code, --state, and --code-verifier.",
		}, "\n")
		return result, nil
	}
	if config.ExpectedState != "" && config.State != "" && config.State != config.ExpectedState {
		return MailConnectResult{}, errors.New("google mail oauth state did not match")
	}

	token, err := exchangeGoogleMailOAuthCodeWithPKCE(config, verifier)
	if err != nil {
		return MailConnectResult{}, err
	}
	return storeGoogleMailConnection(homeDir, config, token)
}

func googleMailAuthorizationURLWithPKCE(config MailConnectConfig, state string, challenge string) string {
	baseURL := config.AuthBaseURL
	if baseURL == "" {
		baseURL = "https://accounts.google.com/o/oauth2/v2/auth"
	}
	values := url.Values{}
	values.Set("client_id", config.ClientID)
	values.Set("redirect_uri", config.RedirectURI)
	values.Set("response_type", "code")
	values.Set("scope", strings.Join(googleMailScopes(), " "))
	values.Set("state", state)
	values.Set("access_type", "offline")
	values.Set("include_granted_scopes", "true")
	if challenge != "" {
		values.Set("code_challenge", challenge)
		values.Set("code_challenge_method", "S256")
	}
	return baseURL + "?" + values.Encode()
}

func exchangeGoogleMailOAuthCodeWithPKCE(config MailConnectConfig, verifier string) (googleOAuthTokenResponse, error) {
	tokenURL := resolveGoogleMailTokenURL(config.TokenURL)
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", config.Code)
	form.Set("client_id", config.ClientID)
	form.Set("redirect_uri", config.RedirectURI)
	form.Set("code_verifier", verifier)

	request, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("build Google Mail token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("exchange Google Mail OAuth code: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("read Google Mail OAuth response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return googleOAuthTokenResponse{}, fmt.Errorf("exchange Google Mail OAuth code: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var token googleOAuthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("parse Google Mail OAuth response: %w", err)
	}
	return token, nil
}

func storeGoogleMailConnection(homeDir string, config MailConnectConfig, token googleOAuthTokenResponse) (MailConnectResult, error) {
	if token.AccessToken == "" {
		return MailConnectResult{}, errors.New("google mail oauth response did not include an access token")
	}
	configPath := mailConfigPath(homeDir)
	stored, err := readOrEmptyMailConnectorConfig(homeDir)
	if err != nil {
		return MailConnectResult{}, err
	}
	stored.Google = &googleMailConnectorConfig{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		ExpiresAt:    googleCalendarTokenExpiresAt(token),
		Scopes:       strings.Fields(token.Scope),
		APIBaseURL:   config.APIBaseURL,
		ClientID:     config.ClientID,
		TokenURL:     resolveGoogleMailTokenURL(config.TokenURL),
	}
	if err := writeMailConnectorConfig(configPath, stored); err != nil {
		return MailConnectResult{}, err
	}
	return MailConnectResult{
		ConfigPath: configPath,
		Configured: true,
		Message: strings.Join([]string{
			"Google Mail connected",
			"Config: " + configPath,
		}, "\n"),
	}, nil
}

func googleMailScopes() []string {
	return []string{
		"https://www.googleapis.com/auth/gmail.readonly",
	}
}

func resolveGoogleMailClientID(clientID string) string {
	if clientID != "" {
		return clientID
	}
	return DefaultGoogleMailClientID
}

func resolveGoogleMailTokenURL(tokenURL string) string {
	if tokenURL != "" {
		return tokenURL
	}
	return DefaultGoogleMailTokenURL
}

func mailProviderConnected(homeDir string, provider string) (bool, error) {
	stored, err := readMailConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	switch strings.ToLower(provider) {
	case "google":
		return stored.Google != nil, nil
	default:
		return false, nil
	}
}

func mailAlreadyConnectedResult(homeDir string, provider string) MailConnectResult {
	configPath := mailConfigPath(homeDir)
	return MailConnectResult{
		ConfigPath: configPath,
		Configured: true,
		Message: strings.Join([]string{
			mailProviderDisplayName(provider) + " is already connected",
			"Config: " + configPath,
		}, "\n"),
	}
}

func mailProviderDisplayName(provider string) string {
	switch strings.ToLower(provider) {
	case "google":
		return "Google Mail"
	default:
		return "Mail"
	}
}

func readMailConnectorConfig(homeDir string) (mailConnectorConfig, error) {
	path := mailConfigPath(homeDir)
	contents, err := os.ReadFile(path)
	if err != nil {
		return mailConnectorConfig{}, err
	}
	var config mailConnectorConfig
	if err := json.Unmarshal(contents, &config); err != nil {
		return mailConnectorConfig{}, fmt.Errorf("parse mail config: %w", err)
	}
	return config, nil
}

func readOrEmptyMailConnectorConfig(homeDir string) (mailConnectorConfig, error) {
	config, err := readMailConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return mailConnectorConfig{}, nil
		}
		return mailConnectorConfig{}, err
	}
	return config, nil
}

func writeMailConnectorConfig(path string, config mailConnectorConfig) error {
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode mail config: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		return fmt.Errorf("write mail config: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure mail config: %w", err)
	}
	return nil
}

func mailHomeDir(homeDir string) (string, error) {
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

func mailConfigPath(homeDir string) string {
	return filepath.Join(homeDir, "mail.json")
}
