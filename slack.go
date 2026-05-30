package workgraph

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DefaultSlackClientID is set by official workgraph builds for Slack PKCE OAuth.
// Local development builds can pass a client id explicitly.
var DefaultSlackClientID = "11231908244708.11230550498913"

// DefaultSlackRedirectURI is the public HTTPS relay URL used for Slack PKCE OAuth.
// Official builds should set this to a workgraph-controlled HTTPS endpoint.
var DefaultSlackRedirectURI = "https://workgraph.pages.dev/slack/callback"

// DefaultSlackLocalCallbackURI is where the HTTPS relay returns the browser.
const DefaultSlackLocalCallbackURI = "http://localhost:2727/slack/callback"

// SlackCaptureConfig controls Slack event ingestion.
type SlackCaptureConfig struct {
	HomeDir      string
	DatabasePath string
	WatchDirs    []string
	EventsFile   string
}

// SlackCaptureResult describes a Slack capture run.
type SlackCaptureResult struct {
	HomeDir      string
	DatabasePath string
	EventsStored int
	Message      string
}

// SlackAPICaptureConfig controls read-only Slack API polling.
type SlackAPICaptureConfig struct {
	HomeDir       string
	DatabasePath  string
	Token         string
	Channels      []string
	IncludeDMs    bool
	SelfUserID    string
	APIBaseURL    string
	HTTPClient    *http.Client
	Cursors       map[string]string
	ThreadCursors map[string]string
}

// SlackAPICaptureResult describes a Slack API capture run.
type SlackAPICaptureResult struct {
	EventsStored  int
	Cursors       map[string]string
	ThreadCursors map[string]string
}

type SlackChannel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Private bool   `json:"is_private"`
	IsIM    bool   `json:"is_im"`
	IsMPIM  bool   `json:"is_mpim"`
	User    string `json:"user"`
}

// SlackConnectConfig controls Slack OAuth setup.
type SlackConnectConfig struct {
	HomeDir          string
	ClientID         string
	ClientSecret     string
	RedirectURI      string
	LocalCallbackURI string
	Code             string
	State            string
	ExpectedState    string
	Channels         []string
	IncludeDMs       bool
	Scopes           []string
	APIBaseURL       string
	HTTPClient       *http.Client
	OpenBrowser      func(string) error
}

// SlackConnectResult describes Slack OAuth setup.
type SlackConnectResult struct {
	ConfigPath       string
	AuthorizationURL string
	State            string
	Configured       bool
	DaemonRestarted  bool
	Message          string
}

type SlackDisconnectConfig struct {
	HomeDir    string
	APIBaseURL string
	HTTPClient *http.Client
}

type SlackDisconnectResult struct {
	ConfigPath      string
	Revoked         bool
	DaemonRestarted bool
	Message         string
}

type slackConnectorConfig struct {
	AccessToken  string   `json:"access_token"`
	AuthedUserID string   `json:"authed_user_id,omitempty"`
	TeamID       string   `json:"team_id,omitempty"`
	TeamName     string   `json:"team_name,omitempty"`
	BotUserID    string   `json:"bot_user_id,omitempty"`
	Channels     []string `json:"channels"`
	IncludeDMs   bool     `json:"include_dms,omitempty"`
	UserScopes   []string `json:"user_scopes"`
	APIBaseURL   string   `json:"api_base_url,omitempty"`
}

type slackExportEvent struct {
	Kind           string         `json:"kind"`
	ChannelID      string         `json:"channel_id"`
	ChannelName    string         `json:"channel_name"`
	Project        string         `json:"project,omitempty"`
	User           string         `json:"user"`
	UserName       string         `json:"user_name,omitempty"`
	UserIsSelf     bool           `json:"user_is_self,omitempty"`
	Text           string         `json:"text"`
	NormalizedText string         `json:"normalized_text,omitempty"`
	Mentions       []slackMention `json:"mentions,omitempty"`
	TS             string         `json:"ts"`
	ThreadTS       string         `json:"thread_ts,omitempty"`
	Permalink      string         `json:"permalink"`
	Timestamp      string         `json:"timestamp"`
}

type slackEventPayload struct {
	ChannelID      string         `json:"channel_id"`
	ChannelName    string         `json:"channel_name"`
	User           string         `json:"user"`
	UserName       string         `json:"user_name,omitempty"`
	UserIsSelf     bool           `json:"user_is_self,omitempty"`
	Text           string         `json:"text"`
	NormalizedText string         `json:"normalized_text,omitempty"`
	Mentions       []slackMention `json:"mentions,omitempty"`
	TS             string         `json:"ts"`
	ThreadTS       string         `json:"thread_ts,omitempty"`
	Permalink      string         `json:"permalink"`
}

type slackMention struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	IsSelf bool   `json:"is_self,omitempty"`
}

type slackAPIResponse struct {
	OK       bool              `json:"ok"`
	Error    string            `json:"error"`
	Messages []slackAPIMessage `json:"messages"`
}

type slackAPIMessage struct {
	Type       string `json:"type"`
	User       string `json:"user"`
	Text       string `json:"text"`
	TS         string `json:"ts"`
	ThreadTS   string `json:"thread_ts"`
	Permalink  string `json:"permalink"`
	ReplyCount int    `json:"reply_count"`
}

type slackConversationsListResponse struct {
	OK       bool           `json:"ok"`
	Error    string         `json:"error"`
	Channels []SlackChannel `json:"channels"`
}

type slackConversationInfoResponse struct {
	OK      bool         `json:"ok"`
	Error   string       `json:"error"`
	Channel SlackChannel `json:"channel"`
}

type slackConversationMembersResponse struct {
	OK      bool     `json:"ok"`
	Error   string   `json:"error"`
	Members []string `json:"members"`
}

type slackUserInfoResponse struct {
	OK    bool      `json:"ok"`
	Error string    `json:"error"`
	User  slackUser `json:"user"`
}

type slackAuthTestResponse struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error"`
	UserID string `json:"user_id"`
}

type slackUser struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	RealName string `json:"real_name"`
	Profile  struct {
		DisplayName string `json:"display_name"`
		RealName    string `json:"real_name"`
	} `json:"profile"`
}

type slackOAuthAccessResponse struct {
	OK          bool   `json:"ok"`
	Error       string `json:"error"`
	AccessToken string `json:"access_token"`
	BotUserID   string `json:"bot_user_id"`
	AuthedUser  struct {
		ID          string `json:"id"`
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
	} `json:"authed_user"`
	Team struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"team"`
}

type slackRevokeResponse struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Revoked bool   `json:"revoked"`
}

// CaptureSlackEvents stores Slack events from a local export file.
func CaptureSlackEvents(config SlackCaptureConfig) (SlackCaptureResult, error) {
	status, err := prepareRunStatus(RunConfig{
		HomeDir:      config.HomeDir,
		DatabasePath: config.DatabasePath,
		WatchDirs:    config.WatchDirs,
	})
	if err != nil {
		return SlackCaptureResult{}, err
	}
	if config.EventsFile == "" {
		return SlackCaptureResult{}, errors.New("events file is required")
	}

	eventsFile, err := filepath.Abs(config.EventsFile)
	if err != nil {
		return SlackCaptureResult{}, fmt.Errorf("resolve events file: %w", err)
	}
	contents, err := os.ReadFile(eventsFile)
	if err != nil {
		return SlackCaptureResult{}, fmt.Errorf("read events file: %w", err)
	}
	var exported []slackExportEvent
	if err := json.Unmarshal(contents, &exported); err != nil {
		return SlackCaptureResult{}, fmt.Errorf("parse events file: %w", err)
	}

	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return SlackCaptureResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return SlackCaptureResult{}, fmt.Errorf("open database: %w", err)
	}

	stored := 0
	for _, event := range exported {
		inserted, err := storeSlackEvent(db, event)
		if err != nil {
			return SlackCaptureResult{}, err
		}
		if inserted {
			stored++
		}
	}

	result := SlackCaptureResult{
		HomeDir:      status.HomeDir,
		DatabasePath: status.DatabasePath,
		EventsStored: stored,
	}
	result.Message = slackCaptureMessage(result)
	return result, nil
}

// ConnectSlack prepares or completes Slack OAuth setup.
func ConnectSlack(config SlackConnectConfig) (SlackConnectResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return SlackConnectResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return SlackConnectResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "workgraph.db")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SlackConnectResult{}, fmt.Errorf("%w: run workgraph init", ErrNotInitialized)
		}
		return SlackConnectResult{}, fmt.Errorf("check database: %w", err)
	}
	config.ClientID = resolveSlackClientID(config.ClientID)
	if config.ClientID == "" {
		return SlackConnectResult{}, errors.New("slack client id is required")
	}
	if config.ClientSecret == "" && config.Code != "" {
		return SlackConnectResult{}, errors.New("slack client secret is required")
	}
	if config.RedirectURI == "" {
		return SlackConnectResult{}, errors.New("slack redirect URI is required")
	}

	state := config.State
	if state == "" {
		generated, err := newEventID()
		if err != nil {
			return SlackConnectResult{}, fmt.Errorf("create oauth state: %w", err)
		}
		state = generated
	}
	result := SlackConnectResult{
		ConfigPath:       slackConfigPath(homeDir),
		AuthorizationURL: slackAuthorizationURL(config.ClientID, config.RedirectURI, state, slackScopes(config.Scopes, config.IncludeDMs), ""),
		State:            state,
	}
	if config.Code == "" {
		result.Message = strings.Join([]string{
			"Slack OAuth authorization URL",
			result.AuthorizationURL,
			"State: " + result.State,
			"After Slack redirects back with a code, rerun slack connect with --code and --state.",
		}, "\n")
		return result, nil
	}
	if config.ExpectedState != "" && config.State != "" && config.State != config.ExpectedState {
		return SlackConnectResult{}, errors.New("slack oauth state did not match")
	}

	token, err := exchangeSlackOAuthCode(config)
	if err != nil {
		return SlackConnectResult{}, err
	}
	accessToken := slackOAuthAccessToken(token)
	if accessToken == "" {
		return SlackConnectResult{}, errors.New("slack oauth response did not include an access token")
	}

	stored := slackConnectorConfig{
		AccessToken:  accessToken,
		AuthedUserID: token.AuthedUser.ID,
		TeamID:       token.Team.ID,
		TeamName:     token.Team.Name,
		BotUserID:    token.BotUserID,
		Channels:     append([]string(nil), config.Channels...),
		IncludeDMs:   config.IncludeDMs && slackHasDMScopes(slackOAuthUserScopes(token)),
		UserScopes:   slackOAuthUserScopes(token),
		APIBaseURL:   config.APIBaseURL,
	}
	if err := writeSlackConnectorConfig(result.ConfigPath, stored); err != nil {
		return SlackConnectResult{}, err
	}
	daemonRestarted, err := restartDaemonAfterSlackUpdate(homeDir)
	if err != nil {
		return SlackConnectResult{}, err
	}

	result.Configured = true
	result.DaemonRestarted = daemonRestarted
	result.Message = slackConnectedMessage(result.ConfigPath, stored, config.IncludeDMs, daemonRestarted)
	return result, nil
}

// ConnectSlackWithBrowser completes Slack OAuth with a local callback and PKCE.
func ConnectSlackWithBrowser(ctx context.Context, config SlackConnectConfig) (SlackConnectResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return SlackConnectResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return SlackConnectResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "workgraph.db")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SlackConnectResult{}, fmt.Errorf("%w: run workgraph init", ErrNotInitialized)
		}
		return SlackConnectResult{}, fmt.Errorf("check database: %w", err)
	}
	config.ClientID = resolveSlackClientID(config.ClientID)
	if config.ClientID == "" {
		return SlackConnectResult{}, errors.New("slack client id is required for browser connect")
	}

	redirectURI := config.RedirectURI
	if redirectURI == "" {
		redirectURI = DefaultSlackRedirectURI
	}
	parsedRedirect, err := url.Parse(redirectURI)
	if err != nil {
		return SlackConnectResult{}, fmt.Errorf("parse slack redirect URI: %w", err)
	}
	if parsedRedirect.Scheme != "https" && !isLocalHTTPRedirect(parsedRedirect) {
		return SlackConnectResult{}, errors.New("slack redirect URI must be https or a localhost http development URL")
	}
	localCallbackURI := config.LocalCallbackURI
	if localCallbackURI == "" {
		localCallbackURI = DefaultSlackLocalCallbackURI
	}
	parsedLocalCallback, err := url.Parse(localCallbackURI)
	if err != nil {
		return SlackConnectResult{}, fmt.Errorf("parse slack local callback URI: %w", err)
	}
	if !isLocalHTTPRedirect(parsedLocalCallback) {
		return SlackConnectResult{}, errors.New("slack local callback URI must be an http localhost URL")
	}

	listener, err := net.Listen("tcp", parsedLocalCallback.Host)
	if err != nil {
		return SlackConnectResult{}, fmt.Errorf("start slack oauth callback: %w", err)
	}
	defer listener.Close()

	state, err := randomURLToken(24)
	if err != nil {
		return SlackConnectResult{}, fmt.Errorf("create oauth state: %w", err)
	}
	verifier, err := randomURLToken(48)
	if err != nil {
		return SlackConnectResult{}, fmt.Errorf("create oauth verifier: %w", err)
	}
	authURL := slackAuthorizationURL(config.ClientID, redirectURI, state, slackScopes(config.Scopes, config.IncludeDMs), slackPKCEChallenge(verifier))
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	server := &http.Server{
		Handler: slackOAuthCallbackHandler(state, codeCh, errCh),
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
		return SlackConnectResult{}, err
	}

	var code string
	select {
	case <-ctx.Done():
		return SlackConnectResult{}, ctx.Err()
	case err := <-errCh:
		return SlackConnectResult{}, err
	case code = <-codeCh:
	}

	config.HomeDir = homeDir
	config.RedirectURI = redirectURI
	config.Code = code
	config.State = state
	config.ExpectedState = state
	config.ClientSecret = ""
	token, err := exchangeSlackOAuthCodeWithPKCE(config, verifier)
	if err != nil {
		return SlackConnectResult{}, err
	}
	accessToken := slackOAuthAccessToken(token)
	if accessToken == "" {
		return SlackConnectResult{}, errors.New("slack oauth response did not include an access token")
	}
	stored := slackConnectorConfig{
		AccessToken:  accessToken,
		AuthedUserID: token.AuthedUser.ID,
		TeamID:       token.Team.ID,
		TeamName:     token.Team.Name,
		BotUserID:    token.BotUserID,
		Channels:     append([]string(nil), config.Channels...),
		IncludeDMs:   config.IncludeDMs && slackHasDMScopes(slackOAuthUserScopes(token)),
		UserScopes:   slackOAuthUserScopes(token),
		APIBaseURL:   config.APIBaseURL,
	}
	configPath := slackConfigPath(homeDir)
	if err := writeSlackConnectorConfig(configPath, stored); err != nil {
		return SlackConnectResult{}, err
	}
	daemonRestarted, err := restartDaemonAfterSlackUpdate(homeDir)
	if err != nil {
		return SlackConnectResult{}, err
	}
	return SlackConnectResult{
		ConfigPath:       configPath,
		AuthorizationURL: authURL,
		State:            state,
		Configured:       true,
		DaemonRestarted:  daemonRestarted,
		Message:          slackConnectedMessage(configPath, stored, config.IncludeDMs, daemonRestarted),
	}, nil
}

func DisconnectSlack(config SlackDisconnectConfig) (SlackDisconnectResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return SlackDisconnectResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return SlackDisconnectResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	stored, err := readSlackConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SlackDisconnectResult{}, errors.New("Slack is not connected")
		}
		return SlackDisconnectResult{}, err
	}
	configPath := slackConfigPath(homeDir)
	revoked, err := revokeSlackToken(config, stored.AccessToken)
	if err != nil {
		return SlackDisconnectResult{}, err
	}
	if err := os.Remove(configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return SlackDisconnectResult{}, fmt.Errorf("remove slack config: %w", err)
	}
	daemonRestarted, err := restartDaemonAfterSlackUpdate(homeDir)
	if err != nil {
		return SlackDisconnectResult{}, err
	}
	lines := []string{
		"Slack disconnected",
		"Config removed: " + configPath,
	}
	if revoked {
		lines = append(lines, "Slack token revoked")
	}
	if daemonRestarted {
		lines = append(lines, "Background capture restarted to apply Slack settings.")
	}
	return SlackDisconnectResult{
		ConfigPath:      configPath,
		Revoked:         revoked,
		DaemonRestarted: daemonRestarted,
		Message:         strings.Join(lines, "\n"),
	}, nil
}

// CaptureSlackFromAPI stores Slack messages from configured or discovered channels.
func CaptureSlackFromAPI(config SlackAPICaptureConfig) (SlackAPICaptureResult, error) {
	if config.Token == "" {
		return SlackAPICaptureResult{}, errors.New("slack token is required")
	}

	status, err := prepareRunStatus(RunConfig{
		HomeDir:      config.HomeDir,
		DatabasePath: config.DatabasePath,
	})
	if err != nil {
		return SlackAPICaptureResult{}, err
	}

	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return SlackAPICaptureResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return SlackAPICaptureResult{}, fmt.Errorf("open database: %w", err)
	}

	cursors := map[string]string{}
	for channel, cursor := range config.Cursors {
		cursors[channel] = cursor
	}
	threadCursors := map[string]string{}
	for threadKey, cursor := range config.ThreadCursors {
		threadCursors[threadKey] = cursor
	}

	channels := explicitSlackChannels(config.Channels)
	if len(channels) == 0 {
		channels, err = discoverSlackConversations(config)
		if err != nil {
			return SlackAPICaptureResult{}, err
		}
	} else {
		channels, err = fetchSlackConversationInfoForChannels(config, channels)
		if err != nil {
			return SlackAPICaptureResult{}, err
		}
	}
	userNames := map[string]string{}
	if err := resolveSlackConversationNames(config, channels, userNames); err != nil {
		return SlackAPICaptureResult{}, err
	}
	channelNames := slackChannelNameCache(channels)
	selfUserID := config.SelfUserID
	if selfUserID == "" {
		selfUserID = fetchSlackSelfUserID(config)
	}

	stored := 0
	checkedThreads := map[string]bool{}
	for _, channel := range channels {
		messages, err := fetchSlackHistory(config, channel.ID, cursors[channel.ID])
		if err != nil {
			return SlackAPICaptureResult{}, err
		}
		newest := cursors[channel.ID]
		for _, message := range messages {
			event := slackEventFromAPIMessage(channel.ID, channel.Name, message, "")
			event.UserName = fetchSlackUserName(config, message.User, userNames)
			event.UserIsSelf = slackUserIsSelf(message.User, selfUserID)
			event.NormalizedText, event.Mentions = normalizeSlackText(config, message.Text, userNames, channelNames, selfUserID)
			inserted, err := storeSlackEvent(db, event)
			if err != nil {
				return SlackAPICaptureResult{}, err
			}
			if inserted {
				stored++
			}
			if slackTSAfter(event.TS, newest) {
				newest = event.TS
			}
			if message.ReplyCount <= 0 {
				continue
			}
			threadKey := slackThreadCursorKey(channel.ID, message.TS)
			if _, ok := threadCursors[threadKey]; !ok {
				threadCursors[threadKey] = message.TS
			}
			replyStored, latestReply, err := captureSlackThreadReplies(db, config, channel, message.TS, threadCursors[threadKey], userNames, selfUserID)
			if err != nil {
				return SlackAPICaptureResult{}, err
			}
			stored += replyStored
			threadCursors[threadKey] = latestReply
			if slackTSAfter(latestReply, newest) {
				newest = latestReply
			}
			checkedThreads[threadKey] = true
		}
		cursors[channel.ID] = newest
	}
	for _, channel := range channels {
		for threadKey, cursor := range threadCursors {
			if checkedThreads[threadKey] {
				continue
			}
			threadChannelID, threadTS, ok := parseSlackThreadCursorKey(threadKey)
			if !ok || threadChannelID != channel.ID {
				continue
			}
			replyStored, latestReply, err := captureSlackThreadReplies(db, config, channel, threadTS, cursor, userNames, selfUserID)
			if err != nil {
				return SlackAPICaptureResult{}, err
			}
			stored += replyStored
			threadCursors[threadKey] = latestReply
			if slackTSAfter(latestReply, cursors[channel.ID]) {
				cursors[channel.ID] = latestReply
			}
		}
	}

	return SlackAPICaptureResult{
		EventsStored:  stored,
		Cursors:       cursors,
		ThreadCursors: threadCursors,
	}, nil
}

func readSlackConnectorConfig(homeDir string) (slackConnectorConfig, error) {
	path := slackConfigPath(homeDir)
	contents, err := os.ReadFile(path)
	if err != nil {
		return slackConnectorConfig{}, err
	}
	var config slackConnectorConfig
	if err := json.Unmarshal(contents, &config); err != nil {
		return slackConnectorConfig{}, fmt.Errorf("parse slack config: %w", err)
	}
	return config, nil
}

func writeSlackConnectorConfig(path string, config slackConnectorConfig) error {
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode slack config: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		return fmt.Errorf("write slack config: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure slack config: %w", err)
	}
	return nil
}

func slackConfigPath(homeDir string) string {
	return filepath.Join(homeDir, "slack.json")
}

func slackConnectedMessage(configPath string, config slackConnectorConfig, requestedDMs bool, daemonRestarted bool) string {
	lines := []string{
		"Slack connected",
		"Config: " + configPath,
		slackCollectionModeLine(config),
	}
	if requestedDMs {
		lines = append(lines, "To remove Slack DM access later, run workgraph slack disconnect, then reconnect without --include-dms.")
	} else {
		lines = append(lines, "To include DMs later, run workgraph slack disconnect, then reconnect with --include-dms.")
	}
	if daemonRestarted {
		lines = append(lines, "Background capture restarted to apply Slack settings.")
	}
	return strings.Join(lines, "\n")
}

func restartDaemonAfterSlackUpdate(homeDir string) (bool, error) {
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

func slackCollectionModeLine(config slackConnectorConfig) string {
	mode := "Collection: auto-discover visible public and private channels"
	if len(config.Channels) > 0 {
		mode = fmt.Sprintf("Collection: %d explicit channel(s)", len(config.Channels))
	}
	if config.IncludeDMs {
		mode += " plus opted-in DMs and group DMs"
	}
	return mode
}

func storeSlackEvent(db *sql.DB, event slackExportEvent) (bool, error) {
	eventType := slackEventType(event.Kind)
	if eventType == "" {
		return false, nil
	}

	payload, err := json.Marshal(slackEventPayload{
		ChannelID:      event.ChannelID,
		ChannelName:    event.ChannelName,
		User:           event.User,
		UserName:       event.UserName,
		UserIsSelf:     event.UserIsSelf,
		Text:           event.Text,
		NormalizedText: event.NormalizedText,
		Mentions:       event.Mentions,
		TS:             event.TS,
		ThreadTS:       event.ThreadTS,
		Permalink:      event.Permalink,
	})
	if err != nil {
		return false, fmt.Errorf("encode slack event: %w", err)
	}

	timestamp := time.Now().UTC()
	if event.Timestamp != "" {
		parsed, err := time.Parse(time.RFC3339Nano, event.Timestamp)
		if err != nil {
			return false, fmt.Errorf("parse slack event timestamp: %w", err)
		}
		timestamp = parsed
	}

	result, err := db.Exec(`INSERT OR IGNORE INTO events
		(id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		fmt.Sprintf("%s:%s:%s", eventType, event.ChannelID, event.TS),
		"slack",
		eventType,
		timestamp.UTC().Format(time.RFC3339Nano),
		string(payload),
		inferSlackProject(event),
		slackActor(event),
		slackSummary(event),
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return false, fmt.Errorf("store slack event: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store slack event: %w", err)
	}
	return rows > 0, nil
}

func slackEventType(kind string) string {
	switch kind {
	case "message":
		return "slack.message"
	case "thread_reply":
		return "slack.thread_reply"
	default:
		return ""
	}
}

func inferSlackProject(event slackExportEvent) string {
	if event.Project != "" {
		return event.Project
	}
	if event.ChannelName != "" {
		return strings.TrimPrefix(event.ChannelName, "#")
	}
	return event.ChannelID
}

func slackActor(event slackExportEvent) string {
	if event.UserIsSelf {
		if event.UserName != "" {
			return event.UserName + " (you)"
		}
		return "You"
	}
	if event.UserName != "" {
		return event.UserName
	}
	return event.User
}

func slackSummary(event slackExportEvent) string {
	if event.NormalizedText != "" {
		return event.NormalizedText
	}
	return event.Text
}

func slackCaptureMessage(result SlackCaptureResult) string {
	lines := []string{
		"Slack capture complete",
		"Home: " + result.HomeDir,
		"Database: " + result.DatabasePath,
		fmt.Sprintf("Events stored: %d", result.EventsStored),
	}
	return strings.Join(lines, "\n")
}

func fetchSlackHistory(config SlackAPICaptureConfig, channel string, oldest string) ([]slackAPIMessage, error) {
	values := url.Values{}
	values.Set("channel", channel)
	values.Set("limit", "50")
	if oldest != "" {
		values.Set("oldest", oldest)
		values.Set("inclusive", "false")
	}
	return fetchSlackMessages(config, "conversations.history", values)
}

func discoverSlackConversations(config SlackAPICaptureConfig) ([]SlackChannel, error) {
	values := url.Values{}
	values.Set("types", strings.Join(slackConversationTypes(config.IncludeDMs), ","))
	values.Set("exclude_archived", "true")
	values.Set("limit", "200")
	baseURL := config.APIBaseURL
	if baseURL == "" {
		baseURL = "https://slack.com/api"
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/conversations.list?" + values.Encode()
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create slack channel discovery request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+config.Token)

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("discover slack channels: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("discover slack channels: status %s", response.Status)
	}

	var apiResponse slackConversationsListResponse
	if err := json.NewDecoder(response.Body).Decode(&apiResponse); err != nil {
		return nil, fmt.Errorf("parse slack channels response: %w", err)
	}
	if !apiResponse.OK {
		if apiResponse.Error == "" {
			apiResponse.Error = "unknown_error"
		}
		return nil, fmt.Errorf("slack api conversations.list: %s", apiResponse.Error)
	}
	return apiResponse.Channels, nil
}

func fetchSlackConversationInfoForChannels(config SlackAPICaptureConfig, channels []SlackChannel) ([]SlackChannel, error) {
	resolved := make([]SlackChannel, 0, len(channels))
	for _, channel := range channels {
		if channel.ID == "" || channel.Name != "" {
			resolved = append(resolved, channel)
			continue
		}
		info, err := fetchSlackConversationInfo(config, channel.ID)
		if err != nil {
			resolved = append(resolved, channel)
			continue
		}
		if info.ID == "" {
			info.ID = channel.ID
		}
		resolved = append(resolved, info)
	}
	return resolved, nil
}

func fetchSlackConversationInfo(config SlackAPICaptureConfig, channelID string) (SlackChannel, error) {
	values := url.Values{}
	values.Set("channel", channelID)
	var apiResponse slackConversationInfoResponse
	if err := fetchSlackAPIJSON(config, "conversations.info", values, &apiResponse); err != nil {
		return SlackChannel{}, err
	}
	if !apiResponse.OK {
		if apiResponse.Error == "" {
			apiResponse.Error = "unknown_error"
		}
		return SlackChannel{}, fmt.Errorf("slack api conversations.info: %s", apiResponse.Error)
	}
	return apiResponse.Channel, nil
}

func resolveSlackConversationNames(config SlackAPICaptureConfig, channels []SlackChannel, userNames map[string]string) error {
	for i := range channels {
		name, err := slackConversationDisplayName(config, channels[i], userNames)
		if err != nil {
			return err
		}
		if name != "" {
			channels[i].Name = name
		}
	}
	return nil
}

func slackConversationDisplayName(config SlackAPICaptureConfig, channel SlackChannel, userNames map[string]string) (string, error) {
	if channel.IsIM && channel.User != "" {
		userName := fetchSlackUserName(config, channel.User, userNames)
		if userName != "" {
			return "DM: " + userName, nil
		}
	}
	if channel.IsMPIM {
		memberNames := fetchSlackConversationMemberNames(config, channel.ID, userNames)
		if len(memberNames) > 0 {
			return "Group DM: " + strings.Join(memberNames, ", "), nil
		}
	}
	if channel.Name != "" {
		return strings.TrimPrefix(channel.Name, "#"), nil
	}
	return channel.ID, nil
}

func fetchSlackConversationMemberNames(config SlackAPICaptureConfig, channelID string, userNames map[string]string) []string {
	values := url.Values{}
	values.Set("channel", channelID)
	var apiResponse slackConversationMembersResponse
	if err := fetchSlackAPIJSON(config, "conversations.members", values, &apiResponse); err != nil {
		return nil
	}
	if !apiResponse.OK {
		return nil
	}
	names := make([]string, 0, len(apiResponse.Members))
	for _, memberID := range apiResponse.Members {
		name := fetchSlackUserName(config, memberID, userNames)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func slackChannelNameCache(channels []SlackChannel) map[string]string {
	names := map[string]string{}
	for _, channel := range channels {
		if channel.ID == "" || channel.Name == "" {
			continue
		}
		names[channel.ID] = strings.TrimPrefix(channel.Name, "#")
	}
	return names
}

func normalizeSlackText(config SlackAPICaptureConfig, text string, userNames map[string]string, channelNames map[string]string, selfUserID string) (string, []slackMention) {
	if text == "" {
		return "", nil
	}
	var normalized strings.Builder
	mentions := []slackMention{}
	for i := 0; i < len(text); {
		if text[i] != '<' {
			normalized.WriteByte(text[i])
			i++
			continue
		}
		end := strings.IndexByte(text[i:], '>')
		if end < 0 {
			normalized.WriteByte(text[i])
			i++
			continue
		}
		end += i
		token := text[i+1 : end]
		replacement, mention, ok := resolveSlackMentionToken(config, token, userNames, channelNames, selfUserID)
		if !ok {
			normalized.WriteString(text[i : end+1])
			i = end + 1
			continue
		}
		normalized.WriteString(replacement)
		mentions = append(mentions, mention)
		i = end + 1
	}
	if len(mentions) == 0 {
		return text, nil
	}
	return normalized.String(), mentions
}

func resolveSlackMentionToken(config SlackAPICaptureConfig, token string, userNames map[string]string, channelNames map[string]string, selfUserID string) (string, slackMention, bool) {
	if strings.HasPrefix(token, "@") {
		id, fallback := splitSlackMentionIDAndLabel(strings.TrimPrefix(token, "@"))
		name := fetchSlackUserName(config, id, userNames)
		if name == "" {
			name = fallback
		}
		if name == "" {
			name = id
		}
		isSelf := slackUserIsSelf(id, selfUserID)
		replacement := "@" + name
		if isSelf {
			replacement += " (you)"
		}
		return replacement, slackMention{Type: "user", ID: id, Name: name, IsSelf: isSelf}, true
	}
	if strings.HasPrefix(token, "#") {
		id, fallback := splitSlackMentionIDAndLabel(strings.TrimPrefix(token, "#"))
		name := slackChannelMentionName(config, id, fallback, channelNames)
		if name == "" {
			name = id
		}
		return "#" + strings.TrimPrefix(name, "#"), slackMention{Type: "channel", ID: id, Name: strings.TrimPrefix(name, "#")}, true
	}
	return "", slackMention{}, false
}

func splitSlackMentionIDAndLabel(token string) (string, string) {
	parts := strings.SplitN(token, "|", 2)
	id := strings.TrimSpace(parts[0])
	if caret := strings.IndexByte(id, '^'); caret >= 0 {
		id = id[caret+1:]
	}
	label := ""
	if len(parts) == 2 {
		label = strings.TrimSpace(parts[1])
	}
	return id, label
}

func slackChannelMentionName(config SlackAPICaptureConfig, channelID string, fallback string, channelNames map[string]string) string {
	if channelNames != nil {
		if name := channelNames[channelID]; name != "" {
			return name
		}
	}
	if fallback != "" {
		return fallback
	}
	info, err := fetchSlackConversationInfo(config, channelID)
	if err != nil {
		return ""
	}
	name := strings.TrimPrefix(info.Name, "#")
	if name != "" && channelNames != nil {
		channelNames[channelID] = name
	}
	return name
}

func slackUserIsSelf(userID string, selfUserID string) bool {
	return userID != "" && selfUserID != "" && userID == selfUserID
}

func explicitSlackChannels(ids []string) []SlackChannel {
	channels := make([]SlackChannel, 0, len(ids))
	for _, id := range ids {
		channels = append(channels, SlackChannel{ID: id})
	}
	return channels
}

func fetchSlackReplies(config SlackAPICaptureConfig, channel string, ts string) ([]slackAPIMessage, error) {
	values := url.Values{}
	values.Set("channel", channel)
	values.Set("ts", ts)
	values.Set("limit", "50")
	return fetchSlackMessages(config, "conversations.replies", values)
}

func captureSlackThreadReplies(db *sql.DB, config SlackAPICaptureConfig, channel SlackChannel, threadTS string, newest string, userNames map[string]string, selfUserID string) (int, string, error) {
	replies, err := fetchSlackReplies(config, channel.ID, threadTS)
	if err != nil {
		return 0, newest, err
	}
	stored := 0
	for _, reply := range replies {
		if reply.TS == threadTS {
			continue
		}
		if !slackTSAfter(reply.TS, newest) {
			continue
		}
		replyEvent := slackEventFromAPIMessage(channel.ID, channel.Name, reply, threadTS)
		replyEvent.UserName = fetchSlackUserName(config, reply.User, userNames)
		replyEvent.UserIsSelf = slackUserIsSelf(reply.User, selfUserID)
		replyEvent.NormalizedText, replyEvent.Mentions = normalizeSlackText(config, reply.Text, userNames, nil, selfUserID)
		inserted, err := storeSlackEvent(db, replyEvent)
		if err != nil {
			return stored, newest, err
		}
		if inserted {
			stored++
		}
		if slackTSAfter(replyEvent.TS, newest) {
			newest = replyEvent.TS
		}
	}
	return stored, newest, nil
}

func slackThreadCursorKey(channelID string, threadTS string) string {
	return channelID + "\x00" + threadTS
}

func parseSlackThreadCursorKey(key string) (string, string, bool) {
	parts := strings.SplitN(key, "\x00", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func fetchSlackUserName(config SlackAPICaptureConfig, userID string, userNames map[string]string) string {
	if userID == "" {
		return ""
	}
	if name, ok := userNames[userID]; ok {
		return name
	}
	values := url.Values{}
	values.Set("user", userID)
	var apiResponse slackUserInfoResponse
	if err := fetchSlackAPIJSON(config, "users.info", values, &apiResponse); err != nil {
		return ""
	}
	if !apiResponse.OK {
		return ""
	}
	name := slackUserDisplayName(apiResponse.User)
	userNames[userID] = name
	return name
}

func fetchSlackSelfUserID(config SlackAPICaptureConfig) string {
	var apiResponse slackAuthTestResponse
	if err := fetchSlackAPIJSON(config, "auth.test", url.Values{}, &apiResponse); err != nil {
		return ""
	}
	if !apiResponse.OK {
		return ""
	}
	return apiResponse.UserID
}

func slackUserDisplayName(user slackUser) string {
	for _, candidate := range []string{
		user.Profile.DisplayName,
		user.Profile.RealName,
		user.RealName,
		user.Name,
		user.ID,
	} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	return ""
}

func fetchSlackAPIJSON(config SlackAPICaptureConfig, method string, values url.Values, target any) error {
	baseURL := config.APIBaseURL
	if baseURL == "" {
		baseURL = "https://slack.com/api"
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/" + method + "?" + values.Encode()
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create slack request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+config.Token)

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("fetch slack %s: %w", method, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("fetch slack %s: status %s", method, response.Status)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("parse slack %s response: %w", method, err)
	}
	return nil
}

func fetchSlackMessages(config SlackAPICaptureConfig, method string, values url.Values) ([]slackAPIMessage, error) {
	baseURL := config.APIBaseURL
	if baseURL == "" {
		baseURL = "https://slack.com/api"
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/" + method + "?" + values.Encode()
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create slack request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+config.Token)

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch slack messages: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch slack messages: status %s", response.Status)
	}

	var apiResponse slackAPIResponse
	if err := json.NewDecoder(response.Body).Decode(&apiResponse); err != nil {
		return nil, fmt.Errorf("parse slack response: %w", err)
	}
	if !apiResponse.OK {
		if apiResponse.Error == "" {
			apiResponse.Error = "unknown_error"
		}
		return nil, fmt.Errorf("slack api %s: %s", method, apiResponse.Error)
	}
	return apiResponse.Messages, nil
}

func revokeSlackToken(config SlackDisconnectConfig, token string) (bool, error) {
	if token == "" {
		return false, errors.New("slack access token is missing")
	}
	baseURL := config.APIBaseURL
	if baseURL == "" {
		baseURL = "https://slack.com/api"
	}
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/auth.revoke", nil)
	if err != nil {
		return false, fmt.Errorf("create slack revoke request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return false, fmt.Errorf("revoke slack token: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return false, fmt.Errorf("revoke slack token: status %s: %s", response.Status, strings.TrimSpace(string(body)))
	}

	var apiResponse slackRevokeResponse
	if err := json.NewDecoder(response.Body).Decode(&apiResponse); err != nil {
		return false, fmt.Errorf("parse slack revoke response: %w", err)
	}
	if !apiResponse.OK {
		if apiResponse.Error == "" {
			apiResponse.Error = "unknown_error"
		}
		return false, fmt.Errorf("slack auth.revoke: %s", apiResponse.Error)
	}
	return apiResponse.Revoked, nil
}

func exchangeSlackOAuthCode(config SlackConnectConfig) (slackOAuthAccessResponse, error) {
	return exchangeSlackOAuthCodeForm(config, "")
}

func exchangeSlackOAuthCodeWithPKCE(config SlackConnectConfig, verifier string) (slackOAuthAccessResponse, error) {
	return exchangeSlackOAuthCodeForm(config, verifier)
}

func exchangeSlackOAuthCodeForm(config SlackConnectConfig, verifier string) (slackOAuthAccessResponse, error) {
	baseURL := config.APIBaseURL
	if baseURL == "" {
		baseURL = "https://slack.com/api"
	}
	values := url.Values{}
	values.Set("code", config.Code)
	values.Set("redirect_uri", config.RedirectURI)
	if verifier != "" {
		values.Set("client_id", config.ClientID)
		values.Set("code_verifier", verifier)
	}
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/oauth.v2.access", strings.NewReader(values.Encode()))
	if err != nil {
		return slackOAuthAccessResponse{}, fmt.Errorf("create slack oauth request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if verifier == "" {
		request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(config.ClientID+":"+config.ClientSecret)))
	}

	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return slackOAuthAccessResponse{}, fmt.Errorf("exchange slack oauth code: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return slackOAuthAccessResponse{}, fmt.Errorf("exchange slack oauth code: status %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var token slackOAuthAccessResponse
	if err := json.NewDecoder(response.Body).Decode(&token); err != nil {
		return slackOAuthAccessResponse{}, fmt.Errorf("parse slack oauth response: %w", err)
	}
	if !token.OK {
		if token.Error == "" {
			token.Error = "unknown_error"
		}
		return slackOAuthAccessResponse{}, fmt.Errorf("slack oauth: %s", token.Error)
	}
	return token, nil
}

func slackAuthorizationURL(clientID, redirectURI, state string, scopes []string, pkceChallenge string) string {
	values := url.Values{}
	values.Set("client_id", clientID)
	values.Set("redirect_uri", redirectURI)
	values.Set("user_scope", strings.Join(scopes, ","))
	values.Set("state", state)
	if pkceChallenge != "" {
		values.Set("code_challenge", pkceChallenge)
		values.Set("code_challenge_method", "S256")
	}
	return "https://slack.com/oauth/v2/authorize?" + values.Encode()
}

func slackScopes(configured []string, includeDMs bool) []string {
	if len(configured) > 0 {
		return append([]string(nil), configured...)
	}
	scopes := []string{"channels:history", "channels:read", "groups:history", "groups:read", "users:read", "team:read"}
	if includeDMs {
		scopes = append(scopes, "im:history", "im:read", "mpim:history", "mpim:read")
	}
	return scopes
}

func slackOAuthAccessToken(response slackOAuthAccessResponse) string {
	if response.AuthedUser.AccessToken != "" {
		return response.AuthedUser.AccessToken
	}
	return response.AccessToken
}

func slackOAuthUserScopes(response slackOAuthAccessResponse) []string {
	return splitSlackScopes(response.AuthedUser.Scope)
}

func splitSlackScopes(scopes string) []string {
	if strings.TrimSpace(scopes) == "" {
		return nil
	}
	parts := strings.Split(scopes, ",")
	result := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		scope := strings.TrimSpace(part)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		result = append(result, scope)
	}
	return result
}

func slackHasDMScopes(scopes []string) bool {
	granted := map[string]bool{}
	for _, scope := range scopes {
		granted[scope] = true
	}
	for _, required := range []string{"im:read", "im:history", "mpim:read", "mpim:history"} {
		if !granted[required] {
			return false
		}
	}
	return true
}

func slackConversationTypes(includeDMs bool) []string {
	types := []string{"public_channel", "private_channel"}
	if includeDMs {
		types = append(types, "im", "mpim")
	}
	return types
}

func resolveSlackClientID(clientID string) string {
	if clientID != "" {
		return clientID
	}
	return DefaultSlackClientID
}

func isLocalHTTPRedirect(parsed *url.URL) bool {
	if parsed.Scheme != "http" {
		return false
	}
	host := parsed.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func randomURLToken(size int) (string, error) {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func slackPKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func slackOAuthCallbackHandler(expectedState string, codeCh chan<- string, errCh chan<- error) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if slackErr := query.Get("error"); slackErr != "" {
			http.Error(response, "Slack authorization failed.", http.StatusBadRequest)
			errCh <- fmt.Errorf("slack oauth: %s", slackErr)
			return
		}
		if query.Get("state") != expectedState {
			http.Error(response, "Slack authorization state did not match.", http.StatusBadRequest)
			errCh <- errors.New("slack oauth state did not match")
			return
		}
		code := query.Get("code")
		if code == "" {
			http.Error(response, "Slack authorization did not include a code.", http.StatusBadRequest)
			errCh <- errors.New("slack oauth code missing")
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(response, "<!doctype html><title>workgraph Slack Connected</title><p>%s</p>", html.EscapeString("Slack connected. You can close this window and return to workgraph."))
		codeCh <- code
	})
}

func openURLInBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

func slackEventFromAPIMessage(channelID string, channelName string, message slackAPIMessage, parentThreadTS string) slackExportEvent {
	threadTS := message.ThreadTS
	if threadTS == "" {
		threadTS = parentThreadTS
	}
	kind := "message"
	if threadTS != "" && threadTS != message.TS {
		kind = "thread_reply"
	}
	return slackExportEvent{
		Kind:        kind,
		ChannelID:   channelID,
		ChannelName: channelName,
		User:        message.User,
		Text:        message.Text,
		TS:          message.TS,
		ThreadTS:    threadTS,
		Permalink:   message.Permalink,
		Timestamp:   slackTimestampToRFC3339(message.TS),
	}
}

func slackTimestampToRFC3339(ts string) string {
	seconds, err := strconv.ParseFloat(ts, 64)
	if err != nil {
		return ""
	}
	nanos := int64(seconds * float64(time.Second))
	return time.Unix(0, nanos).UTC().Format(time.RFC3339Nano)
}

func slackTSAfter(candidate, current string) bool {
	if current == "" {
		return candidate != ""
	}
	candidateValue, candidateErr := strconv.ParseFloat(candidate, 64)
	currentValue, currentErr := strconv.ParseFloat(current, 64)
	if candidateErr != nil || currentErr != nil {
		return candidate > current
	}
	return candidateValue > currentValue
}
