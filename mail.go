package workgraph

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	stdmail "net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultGoogleMailClientID is the OAuth client id used for Google Mail PKCE OAuth.
var DefaultGoogleMailClientID = DefaultGoogleCalendarClientID

// DefaultGoogleMailRedirectURI is used by manual Google Mail OAuth flows.
const DefaultGoogleMailRedirectURI = "http://127.0.0.1:2727/mail/google/callback"

// DefaultGoogleMailTokenURL is the workgraph OAuth token relay for Google Mail.
var DefaultGoogleMailTokenURL = DefaultGoogleCalendarTokenURL

// DefaultGoogleMailRevokeURL is Google's OAuth token revocation endpoint.
var DefaultGoogleMailRevokeURL = DefaultGoogleCalendarRevokeURL

// DefaultMicrosoftMailClientID is the Entra application id used for Microsoft Mail PKCE OAuth.
var DefaultMicrosoftMailClientID = DefaultMicrosoftCalendarClientID

// DefaultMicrosoftMailRedirectURI is used by Microsoft Mail OAuth flows.
const DefaultMicrosoftMailRedirectURI = "http://localhost:2727/mail/microsoft/callback"

// DefaultMicrosoftMailTokenURL is Microsoft identity platform's v2 token endpoint.
var DefaultMicrosoftMailTokenURL = DefaultMicrosoftCalendarTokenURL

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

// MailDisconnectConfig controls mail provider disconnect behavior.
type MailDisconnectConfig struct {
	HomeDir    string
	Provider   string
	RevokeURL  string
	HTTPClient *http.Client
}

// MailDisconnectResult describes mail provider disconnect behavior.
type MailDisconnectResult struct {
	ConfigPath string
	Revoked    bool
	Message    string
}

// MailCaptureConfig controls normalized mail message ingestion.
type MailCaptureConfig struct {
	HomeDir      string
	DatabasePath string
	Provider     string
	MailboxID    string
	Token        string
	ClientID     string
	TokenURL     string
	APIBaseURL   string
	HTTPClient   *http.Client
}

// MailCaptureResult describes a mail capture run.
type MailCaptureResult struct {
	HomeDir        string
	DatabasePath   string
	MessagesStored int
	Message        string
}

type mailConnectorConfig struct {
	Google    *googleMailConnectorConfig    `json:"google,omitempty"`
	Microsoft *microsoftMailConnectorConfig `json:"microsoft,omitempty"`
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

type microsoftMailConnectorConfig struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	TokenType    string   `json:"token_type,omitempty"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	APIBaseURL   string   `json:"api_base_url,omitempty"`
	ClientID     string   `json:"client_id,omitempty"`
	TokenURL     string   `json:"token_url,omitempty"`
}

type mailExportMessage struct {
	Provider  string   `json:"provider"`
	MailboxID string   `json:"mailbox_id"`
	MessageID string   `json:"message_id"`
	ThreadID  string   `json:"thread_id,omitempty"`
	Subject   string   `json:"subject,omitempty"`
	Timestamp string   `json:"timestamp"`
	From      string   `json:"from,omitempty"`
	To        []string `json:"to,omitempty"`
	CC        []string `json:"cc,omitempty"`
	Snippet   string   `json:"snippet,omitempty"`
	BodyText  string   `json:"body_text,omitempty"`
	BodyHTML  string   `json:"body_html,omitempty"`
	Project   string   `json:"project,omitempty"`
}

type mailMessagePayload struct {
	Provider  string   `json:"provider"`
	MailboxID string   `json:"mailbox_id"`
	MessageID string   `json:"message_id"`
	ThreadID  string   `json:"thread_id,omitempty"`
	Subject   string   `json:"subject,omitempty"`
	Timestamp string   `json:"timestamp"`
	From      string   `json:"from,omitempty"`
	To        []string `json:"to,omitempty"`
	CC        []string `json:"cc,omitempty"`
	Snippet   string   `json:"snippet,omitempty"`
	BodyText  string   `json:"body_text,omitempty"`
	BodyHTML  string   `json:"body_html,omitempty"`
}

type gmailListMessagesResponse struct {
	Messages []gmailListedMessage `json:"messages"`
}

type gmailListedMessage struct {
	ID       string `json:"id"`
	ThreadID string `json:"threadId"`
}

type gmailMessage struct {
	ID           string       `json:"id"`
	ThreadID     string       `json:"threadId"`
	InternalDate string       `json:"internalDate"`
	Snippet      string       `json:"snippet"`
	Payload      gmailPayload `json:"payload"`
}

type gmailPayload struct {
	MimeType string         `json:"mimeType"`
	Headers  []gmailHeader  `json:"headers"`
	Body     gmailBody      `json:"body"`
	Parts    []gmailPayload `json:"parts"`
}

type gmailHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type gmailBody struct {
	Data string `json:"data"`
}

type microsoftMailMessagesResponse struct {
	Value []microsoftMailMessage `json:"value"`
}

type microsoftMailMessage struct {
	ID               string                   `json:"id"`
	ConversationID   string                   `json:"conversationId"`
	Subject          string                   `json:"subject"`
	ReceivedDateTime string                   `json:"receivedDateTime"`
	SentDateTime     string                   `json:"sentDateTime"`
	BodyPreview      string                   `json:"bodyPreview"`
	From             microsoftMailRecipient   `json:"from"`
	ToRecipients     []microsoftMailRecipient `json:"toRecipients"`
	CCRecipients     []microsoftMailRecipient `json:"ccRecipients"`
	Body             microsoftMailMessageBody `json:"body"`
}

type microsoftMailRecipient struct {
	EmailAddress microsoftMailEmailAddress `json:"emailAddress"`
}

type microsoftMailEmailAddress struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type microsoftMailMessageBody struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}

// ConnectMail prepares or completes mail provider OAuth setup.
func ConnectMail(config MailConnectConfig) (MailConnectResult, error) {
	switch strings.ToLower(config.Provider) {
	case "google":
		return connectGoogleMail(config)
	case "microsoft":
		return connectMicrosoftMail(config)
	default:
		return MailConnectResult{}, fmt.Errorf("unsupported mail provider %q", config.Provider)
	}
}

// DisconnectMail revokes provider OAuth access when possible and removes local connector settings.
func DisconnectMail(config MailDisconnectConfig) (MailDisconnectResult, error) {
	switch strings.ToLower(config.Provider) {
	case "google":
		return disconnectGoogleMail(config)
	case "microsoft":
		return disconnectMicrosoftMail(config)
	default:
		return MailDisconnectResult{}, fmt.Errorf("unsupported mail provider %q", config.Provider)
	}
}

// CaptureMailMessages stores normalized mail messages from a provider API.
func CaptureMailMessages(config MailCaptureConfig) (MailCaptureResult, error) {
	status, err := prepareRunStatus(RunConfig{
		HomeDir:      config.HomeDir,
		DatabasePath: config.DatabasePath,
	})
	if err != nil {
		return MailCaptureResult{}, err
	}

	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return MailCaptureResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return MailCaptureResult{}, fmt.Errorf("open database: %w", err)
	}

	config.HomeDir = status.HomeDir
	messages, err := mailMessages(config)
	if err != nil {
		return MailCaptureResult{}, err
	}

	stored := 0
	for _, message := range messages {
		inserted, err := storeMailMessage(db, message)
		if err != nil {
			return MailCaptureResult{}, err
		}
		if inserted {
			stored++
		}
	}

	result := MailCaptureResult{
		HomeDir:        status.HomeDir,
		DatabasePath:   status.DatabasePath,
		MessagesStored: stored,
	}
	result.Message = mailCaptureMessage(result)
	return result, nil
}

// ConnectMailWithBrowser completes mail provider OAuth with a local callback and PKCE.
func ConnectMailWithBrowser(ctx context.Context, config MailConnectConfig) (MailConnectResult, error) {
	provider := strings.ToLower(config.Provider)
	if provider != "google" && provider != "microsoft" {
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

	switch provider {
	case "google":
		config.ClientID = resolveGoogleMailClientID(config.ClientID)
	case "microsoft":
		config.ClientID = resolveMicrosoftMailClientID(config.ClientID)
		if config.RedirectURI == "" {
			config.RedirectURI = DefaultMicrosoftMailRedirectURI
		}
	}
	if config.ClientID == "" {
		return MailConnectResult{}, fmt.Errorf("%s mail client id is required for browser connect", provider)
	}

	listenAddress := "127.0.0.1:0"
	redirectPath := ""
	if config.RedirectURI != "" {
		parsedRedirect, err := url.Parse(config.RedirectURI)
		if err != nil {
			return MailConnectResult{}, fmt.Errorf("parse %s mail redirect URI: %w", provider, err)
		}
		if !isLocalHTTPRedirect(parsedRedirect) {
			return MailConnectResult{}, fmt.Errorf("%s mail redirect URI must be an http localhost URL", provider)
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
	var authURL string
	switch provider {
	case "google":
		authURL = googleMailAuthorizationURLWithPKCE(config, state, slackPKCEChallenge(verifier))
	case "microsoft":
		authURL = microsoftMailAuthorizationURLWithPKCE(config, state, slackPKCEChallenge(verifier))
	}

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
	var result MailConnectResult
	switch provider {
	case "google":
		token, err := exchangeGoogleMailOAuthCodeWithPKCE(config, verifier)
		if err != nil {
			return MailConnectResult{}, err
		}
		result, err = storeGoogleMailConnection(homeDir, config, token)
		if err != nil {
			return MailConnectResult{}, err
		}
	case "microsoft":
		token, err := exchangeMicrosoftMailOAuthCodeWithPKCE(config, verifier)
		if err != nil {
			return MailConnectResult{}, err
		}
		result, err = storeMicrosoftMailConnection(homeDir, config, token)
		if err != nil {
			return MailConnectResult{}, err
		}
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

func connectMicrosoftMail(config MailConnectConfig) (MailConnectResult, error) {
	homeDir, err := mailHomeDir(config.HomeDir)
	if err != nil {
		return MailConnectResult{}, err
	}
	if config.Code == "" {
		if connected, err := mailProviderConnected(homeDir, "microsoft"); err != nil {
			return MailConnectResult{}, err
		} else if connected {
			return mailAlreadyConnectedResult(homeDir, "microsoft"), nil
		}
	}
	config.ClientID = resolveMicrosoftMailClientID(config.ClientID)
	if config.ClientID == "" {
		return MailConnectResult{}, errors.New("microsoft mail client id is required")
	}
	if config.RedirectURI == "" {
		config.RedirectURI = DefaultMicrosoftMailRedirectURI
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
		AuthorizationURL: microsoftMailAuthorizationURLWithPKCE(config, state, slackPKCEChallenge(verifier)),
		State:            state,
	}
	if config.Code == "" {
		result.Message = strings.Join([]string{
			"Microsoft Mail OAuth authorization URL",
			result.AuthorizationURL,
			"State: " + result.State,
			"Code verifier: " + verifier,
			"After Microsoft redirects back with a code, rerun mail connect microsoft with --code, --state, and --code-verifier.",
		}, "\n")
		return result, nil
	}
	if config.ExpectedState != "" && config.State != "" && config.State != config.ExpectedState {
		return MailConnectResult{}, errors.New("microsoft mail oauth state did not match")
	}

	token, err := exchangeMicrosoftMailOAuthCodeWithPKCE(config, verifier)
	if err != nil {
		return MailConnectResult{}, err
	}
	return storeMicrosoftMailConnection(homeDir, config, token)
}

func disconnectGoogleMail(config MailDisconnectConfig) (MailDisconnectResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return MailDisconnectResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return MailDisconnectResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	stored, err := readMailConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return mailAlreadyDisconnectedResult(homeDir, "google"), nil
		}
		return MailDisconnectResult{}, err
	}
	if stored.Google == nil {
		return mailAlreadyDisconnectedResult(homeDir, "google"), nil
	}

	configPath := mailConfigPath(homeDir)
	revoked, err := revokeGoogleMailToken(config, stored.Google)
	if err != nil {
		return MailDisconnectResult{}, err
	}
	stored.Google = nil
	if err := writeOrRemoveMailConnectorConfig(configPath, stored); err != nil {
		return MailDisconnectResult{}, err
	}

	lines := []string{
		"Google Mail disconnected",
	}
	if stored.Microsoft == nil {
		lines = append(lines, "Config removed: "+configPath)
	} else {
		lines = append(lines, "Config updated: "+configPath)
	}
	if revoked {
		lines = append(lines, "Google Mail token revoked")
	}
	return MailDisconnectResult{
		ConfigPath: configPath,
		Revoked:    revoked,
		Message:    strings.Join(lines, "\n"),
	}, nil
}

func disconnectMicrosoftMail(config MailDisconnectConfig) (MailDisconnectResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return MailDisconnectResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return MailDisconnectResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	stored, err := readMailConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return mailAlreadyDisconnectedResult(homeDir, "microsoft"), nil
		}
		return MailDisconnectResult{}, err
	}
	if stored.Microsoft == nil {
		return mailAlreadyDisconnectedResult(homeDir, "microsoft"), nil
	}

	configPath := mailConfigPath(homeDir)
	stored.Microsoft = nil
	if err := writeOrRemoveMailConnectorConfig(configPath, stored); err != nil {
		return MailDisconnectResult{}, err
	}

	lines := []string{
		"Microsoft Mail disconnected",
		"Microsoft Mail credentials removed locally",
		"To revoke Microsoft consent, remove workgraph from your Microsoft account or tenant app consent settings.",
	}
	if stored.Google == nil {
		lines = append(lines, "Config removed: "+configPath)
	} else {
		lines = append(lines, "Config updated: "+configPath)
	}
	return MailDisconnectResult{
		ConfigPath: configPath,
		Message:    strings.Join(lines, "\n"),
	}, nil
}

func revokeGoogleMailToken(config MailDisconnectConfig, stored *googleMailConnectorConfig) (bool, error) {
	token := stored.RefreshToken
	if token == "" {
		token = stored.AccessToken
	}
	if token == "" {
		return false, nil
	}
	revokeURL := config.RevokeURL
	if revokeURL == "" {
		revokeURL = DefaultGoogleMailRevokeURL
	}
	form := url.Values{}
	form.Set("token", token)
	request, err := http.NewRequest(http.MethodPost, revokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return false, fmt.Errorf("build Google Mail revoke request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return false, fmt.Errorf("revoke Google Mail token: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return false, fmt.Errorf("read Google Mail revoke response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, fmt.Errorf("revoke Google Mail token: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return true, nil
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

func microsoftMailAuthorizationURLWithPKCE(config MailConnectConfig, state string, challenge string) string {
	baseURL := config.AuthBaseURL
	if baseURL == "" {
		baseURL = "https://login.microsoftonline.com/common/oauth2/v2.0/authorize"
	}
	values := url.Values{}
	values.Set("client_id", config.ClientID)
	values.Set("redirect_uri", config.RedirectURI)
	values.Set("response_type", "code")
	values.Set("scope", strings.Join(microsoftMailScopes(), " "))
	values.Set("state", state)
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

func exchangeMicrosoftMailOAuthCodeWithPKCE(config MailConnectConfig, verifier string) (googleOAuthTokenResponse, error) {
	tokenURL := resolveMicrosoftMailTokenURL(config.TokenURL)
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", config.Code)
	form.Set("client_id", config.ClientID)
	form.Set("redirect_uri", config.RedirectURI)
	form.Set("code_verifier", verifier)

	request, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("build Microsoft Mail token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("exchange Microsoft Mail OAuth code: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("read Microsoft Mail OAuth response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return googleOAuthTokenResponse{}, fmt.Errorf("exchange Microsoft Mail OAuth code: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var token googleOAuthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("parse Microsoft Mail OAuth response: %w", err)
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

func storeMicrosoftMailConnection(homeDir string, config MailConnectConfig, token googleOAuthTokenResponse) (MailConnectResult, error) {
	if token.AccessToken == "" {
		return MailConnectResult{}, errors.New("microsoft mail oauth response did not include an access token")
	}
	configPath := mailConfigPath(homeDir)
	stored, err := readOrEmptyMailConnectorConfig(homeDir)
	if err != nil {
		return MailConnectResult{}, err
	}
	stored.Microsoft = &microsoftMailConnectorConfig{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		ExpiresAt:    googleCalendarTokenExpiresAt(token),
		Scopes:       strings.Fields(token.Scope),
		APIBaseURL:   config.APIBaseURL,
		ClientID:     config.ClientID,
		TokenURL:     resolveMicrosoftMailTokenURL(config.TokenURL),
	}
	if err := writeMailConnectorConfig(configPath, stored); err != nil {
		return MailConnectResult{}, err
	}
	return MailConnectResult{
		ConfigPath: configPath,
		Configured: true,
		Message: strings.Join([]string{
			"Microsoft Mail connected",
			"Config: " + configPath,
		}, "\n"),
	}, nil
}

func mailMessages(config MailCaptureConfig) ([]mailExportMessage, error) {
	switch strings.ToLower(config.Provider) {
	case "google":
		return mailMessagesFromGoogle(config)
	case "microsoft":
		return mailMessagesFromMicrosoft(config)
	case "":
		return nil, errors.New("mail provider is required")
	default:
		return nil, fmt.Errorf("unsupported mail provider %q", config.Provider)
	}
}

func mailMessagesFromGoogle(config MailCaptureConfig) ([]mailExportMessage, error) {
	token := config.Token
	if token == "" {
		stored, err := readMailConnectorConfig(config.HomeDir)
		if err == nil && stored.Google != nil {
			googleConfig := stored.Google
			token = googleConfig.AccessToken
			if config.APIBaseURL == "" {
				config.APIBaseURL = googleConfig.APIBaseURL
			}
		}
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("mail token is required")
	}
	mailboxID := config.MailboxID
	if mailboxID == "" {
		mailboxID = "me"
	}

	baseURL := config.APIBaseURL
	if baseURL == "" {
		baseURL = "https://gmail.googleapis.com"
	}
	listEndpoint, err := url.JoinPath(baseURL, "gmail/v1/users", mailboxID, "messages")
	if err != nil {
		return nil, fmt.Errorf("build Gmail messages URL: %w", err)
	}
	listed, err := requestGmailMessageList(config, listEndpoint, token)
	if err != nil {
		return nil, err
	}

	messages := make([]mailExportMessage, 0, len(listed.Messages))
	for _, listedMessage := range listed.Messages {
		if listedMessage.ID == "" {
			continue
		}
		messageEndpoint, err := url.JoinPath(baseURL, "gmail/v1/users", mailboxID, "messages", listedMessage.ID)
		if err != nil {
			return nil, fmt.Errorf("build Gmail message URL: %w", err)
		}
		message, err := requestGmailMessage(config, messageEndpoint, token)
		if err != nil {
			return nil, err
		}
		normalized, err := gmailExportMessage(mailboxID, message)
		if err != nil {
			return nil, err
		}
		messages = append(messages, normalized)
	}
	return messages, nil
}

func mailMessagesFromMicrosoft(config MailCaptureConfig) ([]mailExportMessage, error) {
	token := config.Token
	if token == "" {
		stored, err := readMailConnectorConfig(config.HomeDir)
		if err == nil && stored.Microsoft != nil {
			microsoftConfig := stored.Microsoft
			token = microsoftConfig.AccessToken
			if config.APIBaseURL == "" {
				config.APIBaseURL = microsoftConfig.APIBaseURL
			}
		}
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("mail token is required")
	}
	mailboxID := config.MailboxID
	if mailboxID == "" {
		mailboxID = "me"
	}

	baseURL := config.APIBaseURL
	if baseURL == "" {
		baseURL = "https://graph.microsoft.com"
	}
	endpoint, err := microsoftMailMessagesEndpoint(baseURL, mailboxID)
	if err != nil {
		return nil, err
	}
	graphMessages, err := requestMicrosoftMailMessages(config, endpoint, token)
	if err != nil {
		return nil, err
	}

	messages := make([]mailExportMessage, 0, len(graphMessages.Value))
	for _, graphMessage := range graphMessages.Value {
		normalized, err := microsoftMailExportMessage(mailboxID, graphMessage)
		if err != nil {
			return nil, err
		}
		messages = append(messages, normalized)
	}
	return messages, nil
}

func microsoftMailMessagesEndpoint(baseURL string, mailboxID string) (string, error) {
	if mailboxID == "" || mailboxID == "me" {
		endpoint, err := url.JoinPath(baseURL, "v1.0", "me", "messages")
		if err != nil {
			return "", fmt.Errorf("build Microsoft Mail messages URL: %w", err)
		}
		return endpoint, nil
	}
	endpoint, err := url.JoinPath(baseURL, "v1.0", "users", mailboxID, "messages")
	if err != nil {
		return "", fmt.Errorf("build Microsoft Mail messages URL: %w", err)
	}
	return endpoint, nil
}

func requestMicrosoftMailMessages(config MailCaptureConfig, endpoint string, token string) (microsoftMailMessagesResponse, error) {
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return microsoftMailMessagesResponse{}, fmt.Errorf("build Microsoft Mail request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return microsoftMailMessagesResponse{}, fmt.Errorf("request Microsoft Mail messages: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return microsoftMailMessagesResponse{}, fmt.Errorf("read Microsoft Mail response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return microsoftMailMessagesResponse{}, fmt.Errorf("request Microsoft Mail messages: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var messages microsoftMailMessagesResponse
	if err := json.Unmarshal(body, &messages); err != nil {
		return microsoftMailMessagesResponse{}, fmt.Errorf("parse Microsoft Mail response: %w", err)
	}
	return messages, nil
}

func microsoftMailExportMessage(mailboxID string, message microsoftMailMessage) (mailExportMessage, error) {
	if message.ID == "" {
		return mailExportMessage{}, errors.New("microsoft mail message id is required")
	}
	timestamp := message.ReceivedDateTime
	if timestamp == "" {
		timestamp = message.SentDateTime
	}
	if timestamp == "" {
		return mailExportMessage{}, errors.New("receivedDateTime or sentDateTime is required")
	}
	parsed, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return mailExportMessage{}, fmt.Errorf("parse Microsoft Mail timestamp %q: %w", message.ID, err)
	}

	exported := mailExportMessage{
		Provider:  "microsoft",
		MailboxID: mailboxID,
		MessageID: message.ID,
		ThreadID:  message.ConversationID,
		Subject:   message.Subject,
		Timestamp: parsed.UTC().Format(time.RFC3339Nano),
		From:      microsoftMailAddress(message.From.EmailAddress),
		To:        microsoftMailAddresses(message.ToRecipients),
		CC:        microsoftMailAddresses(message.CCRecipients),
		Snippet:   message.BodyPreview,
	}
	switch strings.ToLower(message.Body.ContentType) {
	case "html":
		exported.BodyHTML = message.Body.Content
	default:
		exported.BodyText = message.Body.Content
	}
	return exported, nil
}

func microsoftMailAddresses(recipients []microsoftMailRecipient) []string {
	result := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		if address := microsoftMailAddress(recipient.EmailAddress); address != "" {
			result = append(result, address)
		}
	}
	return result
}

func microsoftMailAddress(address microsoftMailEmailAddress) string {
	if address.Name != "" && address.Address != "" {
		return address.Name + " <" + address.Address + ">"
	}
	if address.Address != "" {
		return address.Address
	}
	return address.Name
}

func requestGmailMessageList(config MailCaptureConfig, endpoint string, token string) (gmailListMessagesResponse, error) {
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return gmailListMessagesResponse{}, fmt.Errorf("build Gmail list request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return gmailListMessagesResponse{}, fmt.Errorf("request Gmail messages: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return gmailListMessagesResponse{}, fmt.Errorf("read Gmail messages response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return gmailListMessagesResponse{}, fmt.Errorf("request Gmail messages: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var listed gmailListMessagesResponse
	if err := json.Unmarshal(body, &listed); err != nil {
		return gmailListMessagesResponse{}, fmt.Errorf("parse Gmail messages response: %w", err)
	}
	return listed, nil
}

func requestGmailMessage(config MailCaptureConfig, endpoint string, token string) (gmailMessage, error) {
	requestURL, err := url.Parse(endpoint)
	if err != nil {
		return gmailMessage{}, fmt.Errorf("build Gmail message URL: %w", err)
	}
	query := requestURL.Query()
	query.Set("format", "full")
	requestURL.RawQuery = query.Encode()

	request, err := http.NewRequest(http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return gmailMessage{}, fmt.Errorf("build Gmail message request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return gmailMessage{}, fmt.Errorf("request Gmail message: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return gmailMessage{}, fmt.Errorf("read Gmail message response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return gmailMessage{}, fmt.Errorf("request Gmail message: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var message gmailMessage
	if err := json.Unmarshal(body, &message); err != nil {
		return gmailMessage{}, fmt.Errorf("parse Gmail message response: %w", err)
	}
	return message, nil
}

func gmailExportMessage(mailboxID string, message gmailMessage) (mailExportMessage, error) {
	if message.ID == "" {
		return mailExportMessage{}, errors.New("gmail message id is required")
	}
	headers := gmailHeaders(message.Payload.Headers)
	timestamp, err := gmailMessageTimestamp(headers["date"], message.InternalDate)
	if err != nil {
		return mailExportMessage{}, fmt.Errorf("parse Gmail message timestamp %q: %w", message.ID, err)
	}
	bodyText, bodyHTML := gmailMessageBodies(message.Payload)
	return mailExportMessage{
		Provider:  "google",
		MailboxID: mailboxID,
		MessageID: message.ID,
		ThreadID:  message.ThreadID,
		Subject:   headers["subject"],
		Timestamp: timestamp,
		From:      headers["from"],
		To:        mailAddressList(headers["to"]),
		CC:        mailAddressList(headers["cc"]),
		Snippet:   message.Snippet,
		BodyText:  bodyText,
		BodyHTML:  bodyHTML,
	}, nil
}

func gmailHeaders(headers []gmailHeader) map[string]string {
	result := map[string]string{}
	for _, header := range headers {
		name := strings.ToLower(strings.TrimSpace(header.Name))
		if name == "" {
			continue
		}
		if _, exists := result[name]; !exists {
			result[name] = header.Value
		}
	}
	return result
}

func gmailMessageTimestamp(dateHeader string, internalDate string) (string, error) {
	if strings.TrimSpace(dateHeader) != "" {
		parsed, err := stdmail.ParseDate(dateHeader)
		if err == nil {
			return parsed.UTC().Format(time.RFC3339Nano), nil
		}
	}
	if strings.TrimSpace(internalDate) == "" {
		return "", errors.New("date header or internalDate is required")
	}
	millis, err := parseInt64(internalDate)
	if err != nil {
		return "", err
	}
	return time.Unix(0, millis*int64(time.Millisecond)).UTC().Format(time.RFC3339Nano), nil
}

func gmailMessageBodies(payload gmailPayload) (string, string) {
	var textBody string
	var htmlBody string
	var walk func(gmailPayload)
	walk = func(part gmailPayload) {
		if textBody != "" && htmlBody != "" {
			return
		}
		if part.Body.Data != "" {
			decoded := decodeGmailBody(part.Body.Data)
			switch strings.ToLower(part.MimeType) {
			case "text/plain":
				if textBody == "" {
					textBody = decoded
				}
			case "text/html":
				if htmlBody == "" {
					htmlBody = decoded
				}
			}
		}
		for _, child := range part.Parts {
			walk(child)
		}
	}
	walk(payload)
	return textBody, htmlBody
}

func decodeGmailBody(data string) string {
	decoded, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil {
		decoded, err = base64.URLEncoding.DecodeString(data)
	}
	if err != nil {
		return ""
	}
	return string(decoded)
}

func mailAddressList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	addresses, err := stdmail.ParseAddressList(value)
	if err != nil {
		return []string{value}
	}
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if address.Name != "" {
			result = append(result, address.Name+" <"+address.Address+">")
			continue
		}
		result = append(result, address.Address)
	}
	return result
}

func storeMailMessage(db *sql.DB, message mailExportMessage) (bool, error) {
	if strings.TrimSpace(message.Provider) == "" {
		return false, errors.New("mail message provider is required")
	}
	if strings.TrimSpace(message.MailboxID) == "" {
		return false, errors.New("mail message mailbox_id is required")
	}
	if strings.TrimSpace(message.MessageID) == "" {
		return false, errors.New("mail message message_id is required")
	}
	if strings.TrimSpace(message.Timestamp) == "" {
		return false, errors.New("mail message timestamp is required")
	}

	timestamp, err := time.Parse(time.RFC3339Nano, message.Timestamp)
	if err != nil {
		return false, fmt.Errorf("parse mail message timestamp: %w", err)
	}

	payload, err := json.Marshal(mailMessagePayload{
		Provider:  message.Provider,
		MailboxID: message.MailboxID,
		MessageID: message.MessageID,
		ThreadID:  message.ThreadID,
		Subject:   message.Subject,
		Timestamp: message.Timestamp,
		From:      message.From,
		To:        append([]string(nil), message.To...),
		CC:        append([]string(nil), message.CC...),
		Snippet:   message.Snippet,
		BodyText:  message.BodyText,
		BodyHTML:  message.BodyHTML,
	})
	if err != nil {
		return false, fmt.Errorf("encode mail message: %w", err)
	}

	result, err := db.Exec(`INSERT INTO events
		(id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		mailMessageID(message),
		"mail",
		"mail.message",
		timestamp.UTC().Format(time.RFC3339Nano),
		string(payload),
		emptyStringAsNull(message.Project),
		emptyStringAsNull(message.From),
		emptyStringAsNull(message.Subject),
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return false, fmt.Errorf("store mail message: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store mail message: %w", err)
	}
	return rows > 0, nil
}

func mailMessageID(message mailExportMessage) string {
	return fmt.Sprintf("mail.message:%s:%s:%s", message.Provider, message.MailboxID, message.MessageID)
}

func parseInt64(value string) (int64, error) {
	return strconv.ParseInt(value, 10, 64)
}

func googleMailScopes() []string {
	return []string{
		"https://www.googleapis.com/auth/gmail.readonly",
	}
}

func microsoftMailScopes() []string {
	return []string{
		"openid",
		"profile",
		"email",
		"offline_access",
		"https://graph.microsoft.com/Mail.Read",
		"https://graph.microsoft.com/Mail.Read.Shared",
	}
}

func resolveGoogleMailClientID(clientID string) string {
	if clientID != "" {
		return clientID
	}
	return DefaultGoogleMailClientID
}

func resolveMicrosoftMailClientID(clientID string) string {
	if clientID != "" {
		return clientID
	}
	return DefaultMicrosoftMailClientID
}

func resolveGoogleMailTokenURL(tokenURL string) string {
	if tokenURL != "" {
		return tokenURL
	}
	return DefaultGoogleMailTokenURL
}

func resolveMicrosoftMailTokenURL(tokenURL string) string {
	if tokenURL != "" {
		return tokenURL
	}
	return DefaultMicrosoftMailTokenURL
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
	case "microsoft":
		return stored.Microsoft != nil, nil
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

func mailAlreadyDisconnectedResult(homeDir string, provider string) MailDisconnectResult {
	configPath := mailConfigPath(homeDir)
	return MailDisconnectResult{
		ConfigPath: configPath,
		Message: strings.Join([]string{
			mailProviderDisplayName(provider) + " is not connected",
			"No local Mail connector settings changed.",
		}, "\n"),
	}
}

func mailProviderDisplayName(provider string) string {
	switch strings.ToLower(provider) {
	case "google":
		return "Google Mail"
	case "microsoft":
		return "Microsoft Mail"
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

func writeOrRemoveMailConnectorConfig(path string, config mailConnectorConfig) error {
	if config.Google == nil && config.Microsoft == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove mail config: %w", err)
		}
		return nil
	}
	return writeMailConnectorConfig(path, config)
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

func mailCaptureMessage(result MailCaptureResult) string {
	lines := []string{
		"Mail capture complete",
		"Home: " + result.HomeDir,
		"Database: " + result.DatabasePath,
		fmt.Sprintf("Messages stored: %d", result.MessagesStored),
	}
	return strings.Join(lines, "\n")
}
