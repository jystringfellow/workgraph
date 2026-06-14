package workgraph

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ConnectorListConfig controls connector status listing.
type ConnectorListConfig struct {
	HomeDir string
}

// ConnectorListResult describes configured connector polling.
type ConnectorListResult struct {
	HomeDir    string
	Connectors []ConnectorStatus
	Message    string
}

// ConnectorStatus describes one connector's polling state.
type ConnectorStatus struct {
	ID                  string
	Connected           bool
	Enabled             bool
	Interval            time.Duration
	SetupState          string
	LastValidated       string
	LastValidationError string
	LastPoll            string
	LastError           string
	NextPoll            string
}

// ConnectorUpdateConfig controls connector polling updates.
type ConnectorUpdateConfig struct {
	HomeDir  string
	ID       string
	Enabled  bool
	Interval time.Duration
}

// ConnectorUpdateResult describes a connector polling update.
type ConnectorUpdateResult struct {
	HomeDir string
	ID      string
	Message string
}

// ConnectorConnectConfig controls local connector setup.
type ConnectorConnectConfig struct {
	HomeDir       string
	ID            string
	GitHubCommand string
}

// ConnectorConnectResult describes local connector setup.
type ConnectorConnectResult struct {
	HomeDir string
	ID      string
	Message string
}

// ConnectorPollConfig controls one-shot connector polling.
type ConnectorPollConfig struct {
	HomeDir      string
	DatabasePath string
	ID           string
	Once         bool
}

// ConnectorPollResult describes a one-shot connector polling run.
type ConnectorPollResult struct {
	HomeDir string
	Results []ConnectorPollConnectorResult
	Message string
}

// ConnectorPollConnectorResult describes one connector's one-shot poll result.
type ConnectorPollConnectorResult struct {
	ID     string
	Status string
	Error  string
}

type connectorRuntimeFile struct {
	Connectors map[string]connectorRuntimeEntry `json:"connectors,omitempty"`
}

type connectorRuntimeEntry struct {
	Enabled             *bool  `json:"enabled,omitempty"`
	Interval            string `json:"interval,omitempty"`
	SetupState          string `json:"setup_state,omitempty"`
	LastValidated       string `json:"last_validated_at,omitempty"`
	LastValidationError string `json:"last_validation_error,omitempty"`
	LastPoll            string `json:"last_poll_at,omitempty"`
	LastError           string `json:"last_error,omitempty"`
}

// ListConnectors reports known connector polling state.
func ListConnectors(config ConnectorListConfig) (ConnectorListResult, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return ConnectorListResult{}, err
	}
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return ConnectorListResult{}, err
	}
	statuses := connectorStatuses(homeDir, state)
	result := ConnectorListResult{
		HomeDir:    homeDir,
		Connectors: statuses,
	}
	result.Message = connectorListMessage(result)
	return result, nil
}

// StatusConnectors reports setup and polling state for known connectors.
func StatusConnectors(config ConnectorListConfig) (ConnectorListResult, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return ConnectorListResult{}, err
	}
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return ConnectorListResult{}, err
	}
	result := ConnectorListResult{
		HomeDir:    homeDir,
		Connectors: connectorStatuses(homeDir, state),
	}
	result.Message = connectorStatusMessage(result)
	return result, nil
}

// SetConnectorEnabled changes connector polling without disconnecting credentials.
func SetConnectorEnabled(config ConnectorUpdateConfig) (ConnectorUpdateResult, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return ConnectorUpdateResult{}, err
	}
	id, err := normalizeConnectorID(config.ID)
	if err != nil {
		return ConnectorUpdateResult{}, err
	}
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return ConnectorUpdateResult{}, err
	}
	entry := state.entry(id)
	enabled := config.Enabled
	entry.Enabled = &enabled
	state.Connectors[id] = entry
	if err := writeConnectorRuntimeFile(homeDir, state); err != nil {
		return ConnectorUpdateResult{}, err
	}
	word := "disabled"
	if enabled {
		word = "enabled"
	}
	return ConnectorUpdateResult{
		HomeDir: homeDir,
		ID:      id,
		Message: fmt.Sprintf("Connector %s %s\nConfig: %s", id, word, connectorRuntimePath(homeDir)),
	}, nil
}

// SetConnectorInterval changes connector polling interval without disconnecting credentials.
func SetConnectorInterval(config ConnectorUpdateConfig) (ConnectorUpdateResult, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return ConnectorUpdateResult{}, err
	}
	id, err := normalizeConnectorID(config.ID)
	if err != nil {
		return ConnectorUpdateResult{}, err
	}
	if config.Interval <= 0 {
		return ConnectorUpdateResult{}, fmt.Errorf("connector interval must be positive")
	}
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return ConnectorUpdateResult{}, err
	}
	entry := state.entry(id)
	entry.Interval = config.Interval.String()
	state.Connectors[id] = entry
	if err := writeConnectorRuntimeFile(homeDir, state); err != nil {
		return ConnectorUpdateResult{}, err
	}
	return ConnectorUpdateResult{
		HomeDir: homeDir,
		ID:      id,
		Message: fmt.Sprintf("Connector %s interval: %s\nConfig: %s", id, config.Interval, connectorRuntimePath(homeDir)),
	}, nil
}

// ConnectGit enables local git capture in the shared connector runtime.
func ConnectGit(config ConnectorConnectConfig) (ConnectorConnectResult, error) {
	return connectRuntimeConnector(config.HomeDir, "git", "")
}

// ConnectGitHub validates the GitHub CLI and enables GitHub polling.
func ConnectGitHub(config ConnectorConnectConfig) (ConnectorConnectResult, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return ConnectorConnectResult{}, err
	}
	gh := strings.TrimSpace(config.GitHubCommand)
	if gh == "" {
		gh = "gh"
	}
	if output, err := exec.Command(gh, "auth", "status").CombinedOutput(); err != nil {
		details := strings.TrimSpace(string(output))
		if details == "" {
			details = err.Error()
		}
		if recordErr := recordConnectorValidationError(homeDir, "github", time.Now(), details); recordErr != nil {
			return ConnectorConnectResult{}, recordErr
		}
		return ConnectorConnectResult{}, fmt.Errorf("validate GitHub CLI authentication: %s", details)
	}
	return connectRuntimeConnector(homeDir, "github", "")
}

func PollConnectors(config ConnectorPollConfig) (ConnectorPollResult, error) {
	if !config.Once {
		return ConnectorPollResult{}, fmt.Errorf("connector polling currently requires --once")
	}
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return ConnectorPollResult{}, err
	}
	dbPath := strings.TrimSpace(config.DatabasePath)
	if dbPath == "" {
		dbPath = filepath.Join(homeDir, "workgraph.db")
	}
	dbPath, err = filepath.Abs(dbPath)
	if err != nil {
		return ConnectorPollResult{}, fmt.Errorf("resolve database path: %w", err)
	}

	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return ConnectorPollResult{}, err
	}
	ids, err := pollConnectorIDs(homeDir, state, config.ID)
	if err != nil {
		return ConnectorPollResult{}, err
	}

	result := ConnectorPollResult{HomeDir: homeDir}
	for _, id := range ids {
		pollResult := ConnectorPollConnectorResult{ID: id, Status: "ok"}
		if err := pollConnectorOnce(homeDir, dbPath, id); err != nil {
			pollResult.Status = "error"
			pollResult.Error = err.Error()
		}
		result.Results = append(result.Results, pollResult)
	}
	result.Message = connectorPollMessage(result)
	for _, item := range result.Results {
		if item.Status == "error" {
			return result, fmt.Errorf("poll connector %s: %s", item.ID, item.Error)
		}
	}
	return result, nil
}

func connectRuntimeConnector(homeDir string, id string, interval string) (ConnectorConnectResult, error) {
	homeDir, err := connectorHomeDir(homeDir)
	if err != nil {
		return ConnectorConnectResult{}, err
	}
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return ConnectorConnectResult{}, err
	}
	entry := state.entry(id)
	enabled := true
	entry.Enabled = &enabled
	entry.SetupState = "ready"
	entry.LastValidated = time.Now().UTC().Format(time.RFC3339)
	entry.LastValidationError = ""
	if interval != "" {
		entry.Interval = interval
	}
	state.Connectors[id] = entry
	if err := writeConnectorRuntimeFile(homeDir, state); err != nil {
		return ConnectorConnectResult{}, err
	}
	return ConnectorConnectResult{
		HomeDir: homeDir,
		ID:      id,
		Message: connectorConnectMessage(homeDir, id),
	}, nil
}

func pollConnectorIDs(homeDir string, state connectorRuntimeFile, requested string) ([]string, error) {
	if strings.TrimSpace(requested) != "" {
		id, err := normalizeConnectorID(requested)
		if err != nil {
			return nil, err
		}
		if !connectorConnected(homeDir, id) {
			return nil, fmt.Errorf("connector %s is not connected", id)
		}
		if !connectorEnabled(state, id) {
			return nil, fmt.Errorf("connector %s is disabled", id)
		}
		if !connectorReadyForPolling(state, id) {
			return nil, fmt.Errorf("connector %s is not ready", id)
		}
		return []string{id}, nil
	}
	return monitoredConnectorIDs(homeDir, state), nil
}

func pollConnectorOnce(homeDir string, databasePath string, id string) error {
	capture := &RunCapture{
		homeDir:      homeDir,
		databasePath: databasePath,
		watchDirs:    []string{},
		events:       make(chan CapturedEvent, 128),
	}
	switch id {
	case "git":
		capture.gitEnabled = true
		return capture.captureGitCommits()
	case "github":
		capture.githubEnabled = true
		return capture.captureGitHubEvents()
	case "slack":
		config, err := readSlackConnectorConfig(homeDir)
		if err != nil {
			return err
		}
		capture.slackEnabled = true
		capture.slackToken = config.AccessToken
		capture.slackChannels = append([]string(nil), config.Channels...)
		capture.slackIncludeDMs = config.IncludeDMs
		capture.slackSelfUserID = config.AuthedUserID
		capture.slackAPIBaseURL = config.APIBaseURL
		capture.slackCursors = map[string]string{}
		capture.slackThreadCursors = map[string]string{}
		return capture.captureSlackEvents()
	case "slack.lists":
		config, err := readSlackConnectorConfig(homeDir)
		if err != nil {
			return err
		}
		capture.slackEnabled = true
		capture.slackToken = config.AccessToken
		capture.slackListIDs = append([]string(nil), config.ListIDs...)
		capture.slackAPIBaseURL = config.APIBaseURL
		return capture.captureSlackListItems()
	case "calendar.google":
		capture.calendarProviders = []string{"google"}
		return capture.captureCalendarEvents()
	case "calendar.microsoft":
		capture.calendarProviders = []string{"microsoft"}
		return capture.captureCalendarEvents()
	case "mail.google":
		capture.mailProviders = []string{"google"}
		return capture.captureMailMessages()
	case "mail.microsoft":
		capture.mailProviders = []string{"microsoft"}
		return capture.captureMailMessages()
	case "notion":
		capture.notionEnabled = true
		return capture.captureNotionEvents()
	case "azure.boards":
		capture.azureBoardsEnabled = true
		return capture.captureAzureBoardsEvents()
	default:
		return fmt.Errorf("unsupported connector %s", id)
	}
}

func connectorStatuses(homeDir string, state connectorRuntimeFile) []ConnectorStatus {
	ids := []string{
		"git",
		"github",
		"slack",
		"slack.lists",
		"calendar.google",
		"calendar.microsoft",
		"mail.google",
		"mail.microsoft",
		"notion",
		"azure.boards",
	}
	statuses := make([]ConnectorStatus, 0, len(ids))
	for _, id := range ids {
		connected := connectorConnected(homeDir, id)
		entry := state.entry(id)
		statuses = append(statuses, ConnectorStatus{
			ID:                  id,
			Connected:           connected,
			Enabled:             connectorEnabled(state, id),
			Interval:            connectorInterval(state, id, defaultConnectorInterval(id)),
			SetupState:          connectorSetupState(entry),
			LastValidated:       entry.LastValidated,
			LastValidationError: entry.LastValidationError,
			LastPoll:            entry.LastPoll,
			LastError:           entry.LastError,
			NextPoll:            connectorNextPoll(entry, defaultConnectorInterval(id)),
		})
	}
	return statuses
}

func recordConnectorValidationError(homeDir string, id string, when time.Time, validationError string) error {
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return err
	}
	entry := state.entry(id)
	entry.SetupState = "error"
	entry.LastValidated = when.UTC().Format(time.RFC3339)
	entry.LastValidationError = strings.TrimSpace(validationError)
	state.Connectors[id] = entry
	return writeConnectorRuntimeFile(homeDir, state)
}

func recordConnectorPollSuccess(homeDir string, id string, when time.Time) error {
	return recordConnectorPollResult(homeDir, id, when, "")
}

func recordConnectorPollError(homeDir string, id string, when time.Time, pollErr error) error {
	message := ""
	if pollErr != nil {
		message = pollErr.Error()
	}
	return recordConnectorPollResult(homeDir, id, when, message)
}

func recordConnectorPollResult(homeDir string, id string, when time.Time, lastError string) error {
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return err
	}
	entry := state.entry(id)
	entry.LastPoll = when.UTC().Format(time.RFC3339)
	entry.LastError = strings.TrimSpace(lastError)
	state.Connectors[id] = entry
	return writeConnectorRuntimeFile(homeDir, state)
}

func connectorConnected(homeDir string, id string) bool {
	switch id {
	case "git":
		return true
	case "github":
		return true
	case "slack":
		config, err := readSlackConnectorConfig(homeDir)
		return err == nil && strings.TrimSpace(config.AccessToken) != ""
	case "slack.lists":
		config, err := readSlackConnectorConfig(homeDir)
		return err == nil && strings.TrimSpace(config.AccessToken) != "" && len(config.ListIDs) > 0
	case "calendar.google":
		config, err := readCalendarConnectorConfig(homeDir)
		return err == nil && config.Google != nil && strings.TrimSpace(config.Google.AccessToken) != ""
	case "calendar.microsoft":
		config, err := readCalendarConnectorConfig(homeDir)
		return err == nil && config.Microsoft != nil && strings.TrimSpace(config.Microsoft.AccessToken) != ""
	case "mail.google":
		config, err := readMailConnectorConfig(homeDir)
		return err == nil && config.Google != nil && strings.TrimSpace(config.Google.AccessToken) != ""
	case "mail.microsoft":
		config, err := readMailConnectorConfig(homeDir)
		return err == nil && config.Microsoft != nil && strings.TrimSpace(config.Microsoft.AccessToken) != ""
	case "notion":
		return notionConnectorConnected(homeDir)
	case "azure.boards":
		return azureBoardsConnectorConnected(homeDir)
	default:
		return false
	}
}

func connectorHomeDir(homeDir string) (string, error) {
	resolved, err := resolveHomeDir(homeDir)
	if err != nil {
		return "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve workgraph home: %w", err)
	}
	if _, err := os.Stat(filepath.Join(resolved, "workgraph.db")); err != nil {
		return "", fmt.Errorf("%w: run workgraph init", ErrNotInitialized)
	}
	return resolved, nil
}

func normalizeConnectorID(id string) (string, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	switch id {
	case "git", "github", "slack", "slack.lists", "calendar.google", "calendar.microsoft", "mail.google", "mail.microsoft", "notion", "azure.boards":
		return id, nil
	case "calendar":
		return "", fmt.Errorf("connector %q is ambiguous: use calendar.google or calendar.microsoft", id)
	case "mail":
		return "", fmt.Errorf("connector %q is ambiguous: use mail.google or mail.microsoft", id)
	default:
		return "", fmt.Errorf("unknown connector %q", id)
	}
}

func readConnectorRuntimeFile(homeDir string) (connectorRuntimeFile, error) {
	path := connectorRuntimePath(homeDir)
	contents, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return connectorRuntimeFile{Connectors: map[string]connectorRuntimeEntry{}}, nil
		}
		return connectorRuntimeFile{}, fmt.Errorf("read connector settings: %w", err)
	}
	var state connectorRuntimeFile
	if err := json.Unmarshal(contents, &state); err != nil {
		return connectorRuntimeFile{}, fmt.Errorf("parse connector settings: %w", err)
	}
	if state.Connectors == nil {
		state.Connectors = map[string]connectorRuntimeEntry{}
	}
	return state, nil
}

func writeConnectorRuntimeFile(homeDir string, state connectorRuntimeFile) error {
	if state.Connectors == nil {
		state.Connectors = map[string]connectorRuntimeEntry{}
	}
	contents, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode connector settings: %w", err)
	}
	if err := os.WriteFile(connectorRuntimePath(homeDir), append(contents, '\n'), 0o600); err != nil {
		return fmt.Errorf("write connector settings: %w", err)
	}
	return nil
}

func connectorRuntimePath(homeDir string) string {
	return filepath.Join(homeDir, "connectors.json")
}

func (state connectorRuntimeFile) entry(id string) connectorRuntimeEntry {
	if state.Connectors == nil {
		state.Connectors = map[string]connectorRuntimeEntry{}
	}
	return state.Connectors[id]
}

func connectorEnabled(state connectorRuntimeFile, id string) bool {
	entry := state.Connectors[id]
	if entry.Enabled == nil {
		return true
	}
	return *entry.Enabled
}

func connectorSetupState(entry connectorRuntimeEntry) string {
	state := strings.TrimSpace(entry.SetupState)
	if state == "" {
		return "unknown"
	}
	return state
}

func connectorReadyForPolling(state connectorRuntimeFile, id string) bool {
	entry := state.entry(id)
	if strings.TrimSpace(entry.SetupState) == "error" || strings.TrimSpace(entry.SetupState) == "draft" {
		return false
	}
	if id == "github" {
		return strings.TrimSpace(entry.SetupState) == "ready"
	}
	return true
}

func connectorInterval(state connectorRuntimeFile, id string, fallback time.Duration) time.Duration {
	entry := state.Connectors[id]
	if entry.Interval == "" {
		return fallback
	}
	interval, err := time.ParseDuration(entry.Interval)
	if err != nil || interval <= 0 {
		return fallback
	}
	return interval
}

func connectorNextPoll(entry connectorRuntimeEntry, fallback time.Duration) string {
	if strings.TrimSpace(entry.LastPoll) == "" {
		return ""
	}
	lastPoll, err := time.Parse(time.RFC3339, entry.LastPoll)
	if err != nil {
		return ""
	}
	interval := fallback
	if entry.Interval != "" {
		if parsed, err := time.ParseDuration(entry.Interval); err == nil && parsed > 0 {
			interval = parsed
		}
	}
	return lastPoll.Add(interval).UTC().Format(time.RFC3339)
}

func defaultConnectorInterval(id string) time.Duration {
	switch id {
	case "git":
		return gitPollInterval(0)
	case "github":
		return githubPollInterval(0)
	case "slack":
		return slackPollInterval(0)
	case "slack.lists":
		return slackListPollInterval(0)
	case "calendar.google", "calendar.microsoft":
		return calendarPollInterval(0)
	case "mail.google", "mail.microsoft":
		return mailPollInterval(0)
	case "notion":
		return notionPollInterval(0)
	case "azure.boards":
		return azureBoardsPollInterval(0)
	default:
		return 0
	}
}

func connectorListMessage(result ConnectorListResult) string {
	lines := []string{"Connectors"}
	statuses := append([]ConnectorStatus(nil), result.Connectors...)
	sort.SliceStable(statuses, func(i, j int) bool {
		return statuses[i].ID < statuses[j].ID
	})
	for _, status := range statuses {
		connected := "not connected"
		if status.Connected {
			connected = "connected"
		}
		enabled := "disabled"
		if status.Enabled {
			enabled = "enabled"
		}
		line := fmt.Sprintf("- %s: %s, %s, interval %s", status.ID, connected, enabled, status.Interval)
		if status.LastPoll != "" {
			line += ", last poll " + status.LastPoll
		}
		if status.LastError != "" {
			line += ", last error " + status.LastError
		}
		if status.NextPoll != "" {
			line += ", next poll " + status.NextPoll
		}
		lines = append(lines, line)
	}
	lines = append(lines, "Config: "+connectorRuntimePath(result.HomeDir))
	return strings.Join(lines, "\n")
}

func connectorStatusMessage(result ConnectorListResult) string {
	lines := []string{"Connector status"}
	statuses := append([]ConnectorStatus(nil), result.Connectors...)
	sort.SliceStable(statuses, func(i, j int) bool {
		return statuses[i].ID < statuses[j].ID
	})
	for _, status := range statuses {
		polling := "polling disabled"
		if status.Enabled {
			polling = "polling enabled"
		}
		if status.SetupState == "error" || status.SetupState == "draft" {
			polling = "polling not ready"
		}
		line := fmt.Sprintf("- %s: setup %s, %s, interval %s", status.ID, status.SetupState, polling, status.Interval)
		if status.LastValidated != "" {
			line += ", last validated " + status.LastValidated
		}
		if status.LastValidationError != "" {
			line += ", validation error " + status.LastValidationError
		}
		if status.LastPoll != "" {
			line += ", last poll " + status.LastPoll
		}
		if status.LastError != "" {
			line += ", last error " + status.LastError
		}
		if status.NextPoll != "" {
			line += ", next poll " + status.NextPoll
		}
		lines = append(lines, line)
	}
	lines = append(lines, "Config: "+connectorRuntimePath(result.HomeDir))
	return strings.Join(lines, "\n")
}

func connectorPollMessage(result ConnectorPollResult) string {
	lines := []string{"Connector poll complete"}
	results := append([]ConnectorPollConnectorResult(nil), result.Results...)
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].ID < results[j].ID
	})
	if len(results) == 0 {
		lines = append(lines, "No ready enabled connectors to poll.")
	}
	for _, result := range results {
		line := fmt.Sprintf("- %s: %s", result.ID, result.Status)
		if result.Error != "" {
			line += ", error " + result.Error
		}
		lines = append(lines, line)
	}
	lines = append(lines, "Config: "+connectorRuntimePath(result.HomeDir))
	return strings.Join(lines, "\n")
}

func connectorConnectMessage(homeDir string, id string) string {
	name := id
	if id == "git" {
		name = "Git"
	}
	if id == "github" {
		name = "GitHub"
	}
	return strings.Join([]string{
		fmt.Sprintf("%s connected", name),
		fmt.Sprintf("Polling enabled for connector %s.", id),
		fmt.Sprintf("Disable: workgraph connectors disable %s", id),
		fmt.Sprintf("Interval: workgraph connectors interval %s <duration>", id),
		"Config: " + connectorRuntimePath(homeDir),
	}, "\n")
}
