package workgraph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	CaptureMode         string
	Connected           bool
	Enabled             bool
	Interval            time.Duration
	SetupState          string
	LastValidated       string
	LastValidationError string
	LastPoll            string
	LastSuccess         string
	LastIngest          string
	LastError           string
	NextPoll            string
	ConsecutiveFailures int
	CaptureRequestID    string
	CaptureStatus       string
	CaptureWorker       string
	CaptureLeaseExpires string
	CaptureAvailableAt  string
}

// ConnectorModeConfig controls how a connector captures provider data.
type ConnectorModeConfig struct {
	HomeDir string
	ID      string
	Mode    string
}

// ConnectorBridgeConfig controls one approved bridged connector setup.
type ConnectorBridgeConfig struct {
	HomeDir      string
	ID           string
	Interval     time.Duration
	BridgeParams json.RawMessage
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

// ConnectorValidateConfig controls connector setup validation.
type ConnectorValidateConfig struct {
	HomeDir       string
	ID            string
	GitHubCommand string
}

// ConnectorValidateResult describes connector setup validation.
type ConnectorValidateResult struct {
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

// ConnectorDoctorResult describes connector setup health findings.
type ConnectorDoctorResult struct {
	HomeDir  string
	Findings []ConnectorHealthFinding
	Message  string
}

// ConnectorUpgradeResult describes local connector runtime migrations.
type ConnectorUpgradeResult struct {
	HomeDir string
	Changes []string
	Message string
}

// ConnectorHealthFinding describes one connector setup issue or status.
type ConnectorHealthFinding struct {
	ID      string
	Status  string
	Details string
}

type connectorRuntimeFile struct {
	Connectors map[string]connectorRuntimeEntry `json:"connectors,omitempty"`
}

type connectorRuntimeEntry struct {
	Enabled             *bool           `json:"enabled,omitempty"`
	CaptureMode         string          `json:"capture_mode,omitempty"`
	BridgeParams        json.RawMessage `json:"bridge_params,omitempty"`
	Interval            string          `json:"interval,omitempty"`
	SetupState          string          `json:"setup_state,omitempty"`
	LastValidated       string          `json:"last_validated_at,omitempty"`
	LastValidationError string          `json:"last_validation_error,omitempty"`
	LastPoll            string          `json:"last_poll_at,omitempty"`
	LastSuccess         string          `json:"last_success_at,omitempty"`
	LastIngest          string          `json:"last_ingest_at,omitempty"`
	LastError           string          `json:"last_error,omitempty"`
	NextPoll            string          `json:"next_poll_at,omitempty"`
	ConsecutiveFailures int             `json:"consecutive_failures,omitempty"`
}

var connectorPollStateMu sync.Mutex

// ConnectBridgedConnector enables a provider connector without requiring local credentials.
func ConnectBridgedConnector(config ConnectorModeConfig) (ConnectorConnectResult, error) {
	config.Mode = "bridged"
	result, err := SetConnectorMode(config)
	if err != nil {
		return ConnectorConnectResult{}, err
	}
	return ConnectorConnectResult{
		HomeDir: result.HomeDir,
		ID:      result.ID,
		Message: strings.Join([]string{
			fmt.Sprintf("Connector %s connected in bridged mode", result.ID),
			"Status: awaiting first ingest",
			"workgraph will not store provider credentials or call the provider directly.",
			"Config: " + connectorRuntimePath(result.HomeDir),
		}, "\n"),
	}, nil
}

// ConfigureBridgedConnector records approved non-secret scope and cadence.
func ConfigureBridgedConnector(config ConnectorBridgeConfig) (ConnectorConnectResult, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return ConnectorConnectResult{}, err
	}
	id, err := normalizeConnectorID(config.ID)
	if err != nil {
		return ConnectorConnectResult{}, err
	}
	params, err := validatedBridgeParams(id, config.BridgeParams)
	if err != nil {
		return ConnectorConnectResult{}, err
	}
	if config.Interval < 0 {
		return ConnectorConnectResult{}, fmt.Errorf("connector interval must be positive")
	}
	if err := enforceConnectorManagedSettings(id); err != nil {
		return ConnectorConnectResult{}, err
	}
	connectorPollStateMu.Lock()
	defer connectorPollStateMu.Unlock()
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return ConnectorConnectResult{}, err
	}
	entry := state.entry(id)
	scopeChanged := entry.CaptureMode == "bridged" && !bytes.Equal(bytes.TrimSpace(entry.BridgeParams), bytes.TrimSpace(params))
	if scopeChanged {
		if err := cancelActiveCaptureRequests(homeDir, id, time.Now()); err != nil {
			return ConnectorConnectResult{}, err
		}
	}
	enabled := true
	entry.Enabled = &enabled
	entry.CaptureMode = "bridged"
	entry.BridgeParams = params
	entry.SetupState = "ready"
	entry.LastValidationError = ""
	entry.LastError = ""
	entry.NextPoll = ""
	entry.ConsecutiveFailures = 0
	if config.Interval > 0 {
		entry.Interval = config.Interval.String()
	}
	state.Connectors[id] = entry
	if err := writeConnectorRuntimeFile(homeDir, state); err != nil {
		return ConnectorConnectResult{}, err
	}
	message := fmt.Sprintf("Connector %s configured in bridged mode\nStatus: awaiting first ingest\nConfig: %s", id, connectorRuntimePath(homeDir))
	if scopeChanged {
		message += "\nPrevious active request: cancelled after scope change"
	}
	return ConnectorConnectResult{
		HomeDir: homeDir,
		ID:      id,
		Message: message,
	}, nil
}

func validatedBridgeParams(connectorID string, raw json.RawMessage) (json.RawMessage, error) {
	if err := validateBridgeableConnector(connectorID); err != nil {
		return nil, err
	}
	params, err := canonicalBridgeParams(raw)
	if err != nil {
		return nil, fmt.Errorf("bridge parameters must be a JSON object: %w", err)
	}
	if err := rejectBridgeSecrets(params); err != nil {
		return nil, err
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(params, &values); err != nil {
		return nil, fmt.Errorf("decode bridge parameters: %w", err)
	}
	requireStrings := func(keys ...string) bool {
		for _, key := range keys {
			var items []string
			if err := json.Unmarshal(values[key], &items); err == nil {
				for _, item := range items {
					if strings.TrimSpace(item) != "" {
						return true
					}
				}
			}
		}
		return false
	}
	requireString := func(key string) bool {
		var value string
		return json.Unmarshal(values[key], &value) == nil && strings.TrimSpace(value) != ""
	}
	stringValue := func(key string) string {
		var value string
		_ = json.Unmarshal(values[key], &value)
		return strings.TrimSpace(value)
	}
	participantScope := func(allowedIncludes ...string) bool {
		if stringValue("scope") != "participant" || !requireString("identity") {
			return false
		}
		var includes []string
		if json.Unmarshal(values["include"], &includes) != nil || len(includes) == 0 {
			return false
		}
		allowed := map[string]bool{}
		for _, include := range allowedIncludes {
			allowed[include] = true
		}
		for _, include := range includes {
			if !allowed[strings.TrimSpace(include)] {
				return false
			}
		}
		return true
	}
	requirePositiveInt := func(key string, allowZero bool) bool {
		var value int
		if json.Unmarshal(values[key], &value) != nil {
			return false
		}
		if allowZero {
			return value >= 0
		}
		return value > 0
	}
	paramsChanged := false
	switch connectorID {
	case "github":
		if !requireStrings("repositories") {
			return nil, fmt.Errorf("bridged github requires a non-empty repositories array")
		}
	case "slack":
		includeDMs := false
		if rawValue, found := values["include_dms"]; found {
			if err := json.Unmarshal(rawValue, &includeDMs); err != nil {
				return nil, fmt.Errorf("bridged slack include_dms must be a boolean")
			}
		}
		if !requireStrings("channels") && !includeDMs && !participantScope("authored", "mentions", "thread_participation") {
			return nil, fmt.Errorf("bridged slack requires channels, include_dms true, or participant scope with identity and approved include values")
		}
	case "slack.lists":
		if !requireStrings("lists") {
			return nil, fmt.Errorf("bridged slack.lists requires a non-empty lists array")
		}
		doneColumn := stringValue("done_column")
		if _, present := values["done_column"]; present && doneColumn == "" {
			return nil, fmt.Errorf("bridged slack.lists done_column must be a non-empty string")
		}
		if doneColumn == "" {
			values["done_column"] = json.RawMessage(`"Done"`)
			paramsChanged = true
		}
		var rowKeyCandidates [][]string
		if rawCandidates, present := values["row_key_candidates"]; present {
			if err := json.Unmarshal(rawCandidates, &rowKeyCandidates); err != nil || len(rowKeyCandidates) == 0 {
				return nil, fmt.Errorf("bridged slack.lists row_key_candidates must be a non-empty array of non-empty string arrays")
			}
			for candidateIndex, candidate := range rowKeyCandidates {
				if len(candidate) == 0 {
					return nil, fmt.Errorf("bridged slack.lists row_key_candidates must be a non-empty array of non-empty string arrays")
				}
				for columnIndex, column := range candidate {
					column = strings.TrimSpace(column)
					if column == "" {
						return nil, fmt.Errorf("bridged slack.lists row_key_candidates must be a non-empty array of non-empty string arrays")
					}
					rowKeyCandidates[candidateIndex][columnIndex] = column
				}
			}
			encoded, _ := json.Marshal(rowKeyCandidates)
			values["row_key_candidates"] = encoded
			paramsChanged = true
		} else {
			values["row_key_candidates"] = json.RawMessage(`[["Related Message"],["Title","Cycle"],["Title"]]`)
			paramsChanged = true
		}
	case "mail.google", "mail.microsoft":
		if !requireStrings("mailboxes", "folders") || !requirePositiveInt("preview_limit", false) {
			return nil, fmt.Errorf("bridged %s requires non-empty mailboxes or folders and positive preview_limit", connectorID)
		}
	case "calendar.google", "calendar.microsoft":
		if !requireStrings("calendars") || !requirePositiveInt("past_days", true) || !requirePositiveInt("future_days", false) {
			return nil, fmt.Errorf("bridged %s requires non-empty calendars, non-negative past_days, and positive future_days", connectorID)
		}
	case "azure.boards":
		projectScope := requireString("project") && requireString("area_path")
		participant := participantScope("authored", "assigned")
		if !requireString("organization") || (!projectScope && !participant) {
			return nil, fmt.Errorf("bridged azure.boards requires organization plus project and area_path, or participant scope with identity and approved include values")
		}
	}
	if paramsChanged {
		encoded, err := json.Marshal(values)
		if err != nil {
			return nil, fmt.Errorf("encode bridge parameters: %w", err)
		}
		params = json.RawMessage(encoded)
	}
	return params, nil
}

func validateBridgeableConnector(connectorID string) error {
	switch connectorID {
	case "git":
		return fmt.Errorf("connector git only supports direct capture")
	case "notion":
		return fmt.Errorf("connector notion only supports direct capture; use workgraph notion connect or workgraph notion connect-token")
	default:
		return nil
	}
}

func bridgeConfigurationIssue(connectorID string, params json.RawMessage) (string, string) {
	if err := validateBridgeableConnector(connectorID); err != nil {
		return "unsupported bridge", err.Error()
	}
	if _, err := validatedBridgeParams(connectorID, params); err != nil {
		return "needs scope", err.Error() + "; rerun workgraph connectors connect with --mode bridged and --params-json"
	}
	return "", ""
}

func rejectBridgeSecrets(params json.RawMessage) error {
	var value any
	if err := json.Unmarshal(params, &value); err != nil {
		return fmt.Errorf("bridge parameters must be valid JSON: %w", err)
	}
	var inspect func(any) error
	inspect = func(current any) error {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
				if strings.Contains(normalized, "token") || strings.Contains(normalized, "secret") || strings.Contains(normalized, "password") || normalized == "authorization" || normalized == "apikey" {
					return fmt.Errorf("bridge parameter %q may contain a secret", key)
				}
				if err := inspect(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range typed {
				if err := inspect(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return inspect(value)
}

// SetConnectorMode selects direct or bridged capture for one connector.
func SetConnectorMode(config ConnectorModeConfig) (ConnectorUpdateResult, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return ConnectorUpdateResult{}, err
	}
	id, err := normalizeConnectorID(config.ID)
	if err != nil {
		return ConnectorUpdateResult{}, err
	}
	mode := strings.ToLower(strings.TrimSpace(config.Mode))
	if mode != "direct" && mode != "bridged" {
		return ConnectorUpdateResult{}, fmt.Errorf("capture mode must be direct or bridged")
	}
	if mode == "bridged" {
		if err := validateBridgeableConnector(id); err != nil {
			return ConnectorUpdateResult{}, err
		}
	}
	if err := enforceConnectorManagedSettings(id); err != nil {
		return ConnectorUpdateResult{}, err
	}

	connectorPollStateMu.Lock()
	defer connectorPollStateMu.Unlock()
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return ConnectorUpdateResult{}, err
	}
	entry := state.entry(id)
	if mode == "bridged" {
		if _, err := validatedBridgeParams(id, entry.BridgeParams); err != nil {
			return ConnectorUpdateResult{}, err
		}
	}
	entry.CaptureMode = mode
	if mode == "bridged" {
		enabled := true
		entry.Enabled = &enabled
		entry.SetupState = "ready"
		entry.LastValidationError = ""
		entry.LastError = ""
		entry.NextPoll = ""
		entry.ConsecutiveFailures = 0
	}
	if mode == "direct" {
		if err := cancelActiveCaptureRequests(homeDir, id, time.Now()); err != nil {
			return ConnectorUpdateResult{}, err
		}
	}
	state.Connectors[id] = entry
	if err := writeConnectorRuntimeFile(homeDir, state); err != nil {
		return ConnectorUpdateResult{}, err
	}
	return ConnectorUpdateResult{
		HomeDir: homeDir,
		ID:      id,
		Message: fmt.Sprintf("Connector %s capture mode: %s\nConfig: %s", id, mode, connectorRuntimePath(homeDir)),
	}, nil
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

// DoctorConnectors reports local connector setup health and upgrade hints.
func DoctorConnectors(config ConnectorListConfig) (ConnectorDoctorResult, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return ConnectorDoctorResult{}, err
	}
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return ConnectorDoctorResult{}, err
	}
	result := ConnectorDoctorResult{
		HomeDir:  homeDir,
		Findings: connectorHealthFindings(homeDir, state),
	}
	result.Message = connectorDoctorMessage(result)
	return result, nil
}

// UpgradeConnectors reconciles legacy local connector runtime state.
func UpgradeConnectors(config ConnectorListConfig) (ConnectorUpgradeResult, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return ConnectorUpgradeResult{}, err
	}
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return ConnectorUpgradeResult{}, err
	}
	changes := upgradeConnectorRuntimeState(homeDir, &state)
	if len(changes) > 0 {
		if err := writeConnectorRuntimeFile(homeDir, state); err != nil {
			return ConnectorUpgradeResult{}, err
		}
	}
	result := ConnectorUpgradeResult{
		HomeDir: homeDir,
		Changes: changes,
	}
	result.Message = connectorUpgradeMessage(result)
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
	if config.Enabled {
		if err := enforceConnectorManagedSettings(id); err != nil {
			return ConnectorUpdateResult{}, err
		}
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
	result, err := ValidateConnector(ConnectorValidateConfig{
		HomeDir:       config.HomeDir,
		ID:            "github",
		GitHubCommand: config.GitHubCommand,
	})
	if err != nil {
		return ConnectorConnectResult{}, err
	}
	return ConnectorConnectResult{
		HomeDir: result.HomeDir,
		ID:      result.ID,
		Message: connectorConnectMessage(result.HomeDir, result.ID),
	}, nil
}

func ValidateConnector(config ConnectorValidateConfig) (ConnectorValidateResult, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return ConnectorValidateResult{}, err
	}
	id, err := normalizeConnectorID(config.ID)
	if err != nil {
		return ConnectorValidateResult{}, err
	}
	if err := enforceConnectorManagedSettings(id); err != nil {
		return ConnectorValidateResult{}, err
	}
	switch id {
	case "github":
		return validateGitHubConnector(homeDir, config)
	case "notion":
		return validateNotionConnector(homeDir)
	default:
		return ConnectorValidateResult{}, fmt.Errorf("validation is not implemented for connector %s", id)
	}
}

func validateGitHubConnector(homeDir string, config ConnectorValidateConfig) (ConnectorValidateResult, error) {
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
			return ConnectorValidateResult{}, recordErr
		}
		return ConnectorValidateResult{}, fmt.Errorf("validate GitHub CLI authentication: %s", details)
	}
	if _, err := connectRuntimeConnector(homeDir, "github", ""); err != nil {
		return ConnectorValidateResult{}, err
	}
	return ConnectorValidateResult{
		HomeDir: homeDir,
		ID:      "github",
		Message: fmt.Sprintf("Connector github validation passed\nConfig: %s", connectorRuntimePath(homeDir)),
	}, nil
}

func validateNotionConnector(homeDir string) (ConnectorValidateResult, error) {
	stored, err := readNotionConnectorConfig(homeDir)
	if err != nil {
		details := "notion is not connected"
		if !os.IsNotExist(err) {
			details = err.Error()
		}
		if recordErr := recordConnectorValidationError(homeDir, "notion", time.Now(), details); recordErr != nil {
			return ConnectorValidateResult{}, recordErr
		}
		return ConnectorValidateResult{}, fmt.Errorf("validate Notion connection: %s", details)
	}
	if err := validateNotionToken(stored.AccessToken, resolveNotionAPIBaseURL(stored.APIBaseURL), nil); err != nil {
		if recordErr := recordConnectorValidationError(homeDir, "notion", time.Now(), err.Error()); recordErr != nil {
			return ConnectorValidateResult{}, recordErr
		}
		return ConnectorValidateResult{}, err
	}
	if _, err := connectRuntimeConnector(homeDir, "notion", ""); err != nil {
		return ConnectorValidateResult{}, err
	}
	return ConnectorValidateResult{
		HomeDir: homeDir,
		ID:      "notion",
		Message: fmt.Sprintf("Connector notion validation passed\nConfig: %s", connectorRuntimePath(homeDir)),
	}, nil
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
		if connectorCaptureMode(state, id) == "bridged" {
			emitted, emitErr := EmitBridgedCaptureRequest(CaptureRequestEmitConfig{
				HomeDir:      homeDir,
				DatabasePath: dbPath,
				ConnectorID:  id,
			})
			if emitErr != nil {
				pollResult.Status = "error"
				pollResult.Error = emitErr.Error()
			} else if emitted.Coalesced {
				pollResult.Status = "pending (coalesced), request " + emitted.Request.ID
			} else {
				pollResult.Status = "pending, request " + emitted.Request.ID
			}
			result.Results = append(result.Results, pollResult)
			continue
		}
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
	id, err = normalizeConnectorID(id)
	if err != nil {
		return ConnectorConnectResult{}, err
	}
	if err := enforceConnectorManagedSettings(id); err != nil {
		return ConnectorConnectResult{}, err
	}
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return ConnectorConnectResult{}, err
	}
	entry := state.entry(id)
	enabled := true
	entry.Enabled = &enabled
	entry.CaptureMode = "direct"
	entry.SetupState = "ready"
	entry.LastValidated = time.Now().UTC().Format(time.RFC3339)
	entry.LastValidationError = ""
	entry.LastPoll = ""
	entry.LastSuccess = ""
	entry.LastError = ""
	entry.NextPoll = ""
	entry.ConsecutiveFailures = 0
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

func clearRuntimeConnector(homeDir string, id string) error {
	homeDir, err := connectorHomeDir(homeDir)
	if err != nil {
		return err
	}
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return err
	}
	entry := state.entry(id)
	entry.SetupState = ""
	entry.LastValidated = ""
	entry.LastValidationError = ""
	entry.LastPoll = ""
	entry.LastSuccess = ""
	entry.LastError = ""
	entry.NextPoll = ""
	entry.ConsecutiveFailures = 0
	state.Connectors[id] = entry
	return writeConnectorRuntimeFile(homeDir, state)
}

func pollConnectorIDs(homeDir string, state connectorRuntimeFile, requested string) ([]string, error) {
	if strings.TrimSpace(requested) != "" {
		id, err := normalizeConnectorID(requested)
		if err != nil {
			return nil, err
		}
		if err := enforceConnectorManagedSettings(id); err != nil {
			return nil, err
		}
		if !connectorConnected(homeDir, state, id) {
			return nil, fmt.Errorf("connector %s is not connected", id)
		}
		if !connectorEnabled(state, id) {
			return nil, fmt.Errorf("connector %s is disabled", id)
		}
		if connectorCaptureMode(state, id) == "bridged" {
			if _, details := bridgeConfigurationIssue(id, state.entry(id).BridgeParams); details != "" {
				return nil, fmt.Errorf("%s", details)
			}
		}
		if !connectorReadyForPolling(state, id) {
			return nil, fmt.Errorf("connector %s is not ready", id)
		}
		return []string{id}, nil
	}
	return monitoredConnectorIDs(homeDir, state)
}

func pollConnectorOnce(homeDir string, databasePath string, id string) error {
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return err
	}
	if connectorCaptureMode(state, id) == "bridged" {
		return fmt.Errorf("connector %s is bridged; feed it via capture ingest", id)
	}
	ctx := context.Background()
	capture := &RunCapture{
		homeDir:      homeDir,
		databasePath: databasePath,
		watchDirs:    []string{},
		events:       make(chan CapturedEvent, 128),
	}
	var pollErr error
	switch id {
	case "git":
		capture.gitEnabled = true
		pollErr = capture.captureGitCommits(ctx)
	case "github":
		capture.githubEnabled = true
		pollErr = capture.captureGitHubEvents(ctx)
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
		pollErr = capture.captureSlackEvents(ctx)
	case "slack.lists":
		config, err := readSlackConnectorConfig(homeDir)
		if err != nil {
			return err
		}
		capture.slackEnabled = true
		capture.slackToken = config.AccessToken
		capture.slackListIDs = append([]string(nil), config.ListIDs...)
		capture.slackAPIBaseURL = config.APIBaseURL
		pollErr = capture.captureSlackListItems(ctx)
	case "calendar.google":
		pollErr = capture.captureCalendarEvents(ctx, "google")
	case "calendar.microsoft":
		pollErr = capture.captureCalendarEvents(ctx, "microsoft")
	case "mail.google":
		pollErr = capture.captureMailMessages(ctx, "google")
	case "mail.microsoft":
		pollErr = capture.captureMailMessages(ctx, "microsoft")
	case "notion":
		capture.notionEnabled = true
		pollErr = capture.captureNotionEvents(ctx)
	case "azure.boards":
		capture.azureBoardsEnabled = true
		pollErr = capture.captureAzureBoardsEvents(ctx)
	default:
		return fmt.Errorf("unsupported connector %s", id)
	}
	when := time.Now()
	if pollErr != nil {
		_ = recordConnectorPollError(homeDir, id, when, pollErr)
		return pollErr
	}
	return recordConnectorPollSuccess(homeDir, id, when)
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
	activeRequests := map[string]CaptureRequest{}
	if requests, err := ListCaptureRequests(CaptureRequestListConfig{HomeDir: homeDir}); err == nil {
		for _, request := range requests {
			if request.Status == "pending" || request.Status == "claimed" {
				activeRequests[request.ConnectorID] = request
			}
		}
	}
	for _, id := range ids {
		connected := connectorConnected(homeDir, state, id)
		entry := state.entry(id)
		request := activeRequests[id]
		captureMode := connectorCaptureMode(state, id)
		setupState := connectorSetupState(id, connected, entry)
		lastValidationError := entry.LastValidationError
		if captureMode == "bridged" {
			if issue, details := bridgeConfigurationIssue(id, entry.BridgeParams); issue != "" {
				setupState = issue
				lastValidationError = details
			}
		}
		statuses = append(statuses, ConnectorStatus{
			ID:                  id,
			CaptureMode:         captureMode,
			Connected:           connected,
			Enabled:             connectorEnabled(state, id),
			Interval:            connectorInterval(state, id, defaultConnectorInterval(id)),
			SetupState:          setupState,
			LastValidated:       entry.LastValidated,
			LastValidationError: lastValidationError,
			LastPoll:            entry.LastPoll,
			LastSuccess:         entry.LastSuccess,
			LastIngest:          entry.LastIngest,
			LastError:           entry.LastError,
			NextPoll:            connectorNextPoll(entry, defaultConnectorInterval(id)),
			ConsecutiveFailures: entry.ConsecutiveFailures,
			CaptureRequestID:    request.ID,
			CaptureStatus:       request.Status,
			CaptureWorker:       request.ClaimedBy,
			CaptureLeaseExpires: request.LeaseExpiresAt,
			CaptureAvailableAt:  request.AvailableAt,
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
	var pollErr error
	if strings.TrimSpace(lastError) != "" {
		pollErr = errors.New(strings.TrimSpace(lastError))
	}
	return recordConnectorPollAttempt(homeDir, id, when, time.Time{}, -1, pollErr)
}

func recordConnectorPollAttempt(homeDir string, id string, when time.Time, nextPoll time.Time, failures int, pollErr error) error {
	connectorPollStateMu.Lock()
	defer connectorPollStateMu.Unlock()

	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return err
	}
	entry := state.entry(id)
	entry.LastPoll = when.UTC().Format(time.RFC3339)
	if pollErr == nil {
		entry.LastSuccess = entry.LastPoll
		entry.LastError = ""
		entry.ConsecutiveFailures = 0
	} else {
		entry.LastError = strings.TrimSpace(pollErr.Error())
		if failures >= 0 {
			entry.ConsecutiveFailures = failures
		} else {
			entry.ConsecutiveFailures++
		}
	}
	if nextPoll.IsZero() {
		entry.NextPoll = ""
	} else {
		entry.NextPoll = nextPoll.UTC().Format(time.RFC3339)
	}
	if pollErr != nil && connectorAuthFailure(pollErr.Error()) {
		entry.SetupState = "error"
		entry.LastValidated = entry.LastPoll
		entry.LastValidationError = "last poll failed with invalid credentials; reconnect " + id
		entry.NextPoll = ""
	}
	state.Connectors[id] = entry
	return writeConnectorRuntimeFile(homeDir, state)
}

func connectorConnected(homeDir string, state connectorRuntimeFile, id string) bool {
	if id != "git" && connectorCaptureMode(state, id) == "bridged" {
		return true
	}
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

func connectorCaptureMode(state connectorRuntimeFile, id string) string {
	mode := strings.ToLower(strings.TrimSpace(state.entry(id).CaptureMode))
	if mode == "bridged" {
		return mode
	}
	return "direct"
}

func recordConnectorIngest(homeDir string, id string, when time.Time) error {
	connectorPollStateMu.Lock()
	defer connectorPollStateMu.Unlock()

	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return err
	}
	entry := state.entry(id)
	entry.LastIngest = when.UTC().Format(time.RFC3339)
	state.Connectors[id] = entry
	return writeConnectorRuntimeFile(homeDir, state)
}

func recordConnectorCaptureCompletion(homeDir string, id string, when time.Time) error {
	connectorPollStateMu.Lock()
	defer connectorPollStateMu.Unlock()

	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return err
	}
	entry := state.entry(id)
	completedAt := when.UTC().Format(time.RFC3339)
	entry.LastIngest = completedAt
	entry.LastPoll = completedAt
	entry.LastSuccess = completedAt
	entry.LastError = ""
	entry.ConsecutiveFailures = 0
	entry.NextPoll = when.UTC().Add(connectorInterval(state, id, defaultConnectorInterval(id))).Format(time.RFC3339)
	state.Connectors[id] = entry
	return writeConnectorRuntimeFile(homeDir, state)
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
	if err := os.Chmod(connectorRuntimePath(homeDir), 0o600); err != nil {
		return fmt.Errorf("secure connector settings: %w", err)
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

func connectorSetupState(id string, connected bool, entry connectorRuntimeEntry) string {
	state := strings.TrimSpace(entry.SetupState)
	if state != "" {
		return state
	}
	if !connected {
		return "not connected"
	}
	if id == "github" {
		return "not validated"
	}
	return "ready"
}

func connectorReadyForPolling(state connectorRuntimeFile, id string) bool {
	entry := state.entry(id)
	if connectorCaptureMode(state, id) == "bridged" {
		if issue, _ := bridgeConfigurationIssue(id, entry.BridgeParams); issue != "" {
			return false
		}
	}
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
	if strings.TrimSpace(entry.NextPoll) != "" {
		return entry.NextPoll
	}
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

func connectorHealthFindings(homeDir string, state connectorRuntimeFile) []ConnectorHealthFinding {
	statuses := connectorStatuses(homeDir, state)
	findings := make([]ConnectorHealthFinding, 0, len(statuses))
	for _, status := range statuses {
		entry := state.entry(status.ID)
		if status.CaptureMode == "bridged" {
			if issue, details := bridgeConfigurationIssue(status.ID, entry.BridgeParams); issue != "" {
				findings = append(findings, ConnectorHealthFinding{
					ID:      status.ID,
					Status:  issue,
					Details: details,
				})
				continue
			}
		}
		switch {
		case connectorAuthFailure(entry.LastError):
			findings = append(findings, ConnectorHealthFinding{
				ID:      status.ID,
				Status:  "needs reconnect",
				Details: "last poll failed with invalid credentials",
			})
		case status.ID == "github" && strings.TrimSpace(entry.SetupState) == "":
			findings = append(findings, ConnectorHealthFinding{
				ID:      status.ID,
				Status:  "not validated",
				Details: "run workgraph github connect",
			})
		case status.Connected && strings.TrimSpace(entry.SetupState) == "" && status.ID != "git":
			findings = append(findings, ConnectorHealthFinding{
				ID:      status.ID,
				Status:  "needs upgrade",
				Details: "setup state missing for existing local config",
			})
		case status.SetupState == "error":
			details := strings.TrimSpace(status.LastValidationError)
			if details == "" {
				details = strings.TrimSpace(status.LastError)
			}
			findings = append(findings, ConnectorHealthFinding{
				ID:      status.ID,
				Status:  "error",
				Details: details,
			})
		}
	}
	sort.SliceStable(findings, func(i, j int) bool {
		return findings[i].ID < findings[j].ID
	})
	return findings
}

func upgradeConnectorRuntimeState(homeDir string, state *connectorRuntimeFile) []string {
	statuses := connectorStatuses(homeDir, *state)
	changes := []string{}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, status := range statuses {
		entry := state.entry(status.ID)
		if connectorAuthFailure(entry.LastError) {
			if entry.SetupState != "error" || !strings.Contains(entry.LastValidationError, "invalid credentials") {
				entry.SetupState = "error"
				entry.LastValidated = now
				entry.LastValidationError = "last poll failed with invalid credentials; reconnect " + status.ID
				state.Connectors[status.ID] = entry
				changes = append(changes, fmt.Sprintf("- %s: marked error; reconnect required", status.ID))
			}
			continue
		}
		if !status.Connected || status.ID == "github" {
			continue
		}
		if strings.TrimSpace(entry.SetupState) == "" {
			entry.SetupState = "ready"
			if strings.TrimSpace(entry.LastValidated) == "" {
				entry.LastValidated = now
			}
			state.Connectors[status.ID] = entry
			changes = append(changes, fmt.Sprintf("- %s: marked ready from existing local config", status.ID))
		}
	}
	sort.Strings(changes)
	return changes
}

func connectorAuthFailure(lastError string) bool {
	normalized := strings.ToLower(lastError)
	return strings.Contains(normalized, "status 401") ||
		strings.Contains(normalized, "unauthenticated") ||
		strings.Contains(normalized, "invalid credentials")
}

func connectorListMessage(result ConnectorListResult) string {
	lines := []string{"Connectors"}
	statuses := append([]ConnectorStatus(nil), result.Connectors...)
	sort.SliceStable(statuses, func(i, j int) bool {
		return statuses[i].ID < statuses[j].ID
	})
	for _, status := range statuses {
		label := status.ID
		if status.CaptureMode == "bridged" {
			label += " (bridged)"
		}
		connected := "not connected"
		if status.Connected {
			connected = "connected"
		}
		enabled := "disabled"
		if status.Enabled {
			enabled = "enabled"
		}
		line := fmt.Sprintf("- %s: %s, %s, interval %s", label, connected, enabled, status.Interval)
		if status.LastPoll != "" {
			line += ", last poll " + status.LastPoll
		}
		if status.LastError != "" {
			line += ", last error " + status.LastError
		}
		if status.NextPoll != "" {
			line += ", next poll " + status.NextPoll
		}
		if status.CaptureStatus != "" {
			line += ", capture " + status.CaptureStatus
			if status.CaptureWorker != "" {
				line += ", worker " + status.CaptureWorker
			}
			if status.CaptureLeaseExpires != "" {
				line += ", lease expires " + status.CaptureLeaseExpires
			}
		}
		lines = append(lines, line)
	}
	lines = append(lines, "Config: "+connectorRuntimePath(result.HomeDir))
	return strings.Join(lines, "\n")
}

func connectorDoctorMessage(result ConnectorDoctorResult) string {
	lines := []string{"Connector health"}
	if len(result.Findings) == 0 {
		lines = append(lines, "No connector setup issues found.")
	} else {
		for _, finding := range result.Findings {
			line := fmt.Sprintf("- %s: %s", finding.ID, finding.Status)
			if finding.Details != "" {
				line += ", " + finding.Details
			}
			lines = append(lines, line)
		}
		lines = append(lines, "Run: workgraph connectors upgrade")
	}
	lines = append(lines, "Config: "+connectorRuntimePath(result.HomeDir))
	return strings.Join(lines, "\n")
}

func connectorUpgradeMessage(result ConnectorUpgradeResult) string {
	lines := []string{"Connector settings upgraded"}
	if len(result.Changes) == 0 {
		lines = append(lines, "No connector settings needed changes.")
	} else {
		lines = append(lines, result.Changes...)
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
		label := status.ID
		if status.CaptureMode == "bridged" {
			label += " (bridged)"
		}
		polling := "polling disabled"
		if status.Enabled {
			polling = "polling enabled"
		}
		if status.SetupState != "ready" || !status.Connected {
			polling = "polling not ready"
		}
		line := fmt.Sprintf("- %s: setup %s, %s, interval %s", label, status.SetupState, polling, status.Interval)
		if status.LastValidated != "" {
			line += ", last validated " + status.LastValidated
		}
		if status.LastValidationError != "" {
			line += ", validation error " + status.LastValidationError
		}
		if status.LastPoll != "" {
			line += ", last poll " + status.LastPoll
		}
		if status.LastIngest != "" {
			line += ", last ingest " + status.LastIngest
		}
		if status.LastError != "" {
			line += ", last error " + status.LastError
		}
		if status.NextPoll != "" {
			line += ", next poll " + status.NextPoll
		}
		if status.CaptureStatus != "" {
			line += ", capture " + status.CaptureStatus
			if status.CaptureWorker != "" {
				line += ", worker " + status.CaptureWorker
			}
			if status.CaptureLeaseExpires != "" {
				line += ", lease expires " + status.CaptureLeaseExpires
			}
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
