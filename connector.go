package workgraph

import (
	"encoding/json"
	"fmt"
	"os"
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
	ID        string
	Connected bool
	Enabled   bool
	Interval  time.Duration
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

type connectorRuntimeFile struct {
	Connectors map[string]connectorRuntimeEntry `json:"connectors,omitempty"`
}

type connectorRuntimeEntry struct {
	Enabled  *bool  `json:"enabled,omitempty"`
	Interval string `json:"interval,omitempty"`
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

func connectorStatuses(homeDir string, state connectorRuntimeFile) []ConnectorStatus {
	ids := []string{
		"git",
		"github",
		"slack",
		"calendar.google",
		"calendar.microsoft",
		"mail.google",
		"mail.microsoft",
		"notion",
	}
	statuses := make([]ConnectorStatus, 0, len(ids))
	for _, id := range ids {
		connected := connectorConnected(homeDir, id)
		statuses = append(statuses, ConnectorStatus{
			ID:        id,
			Connected: connected,
			Enabled:   connectorEnabled(state, id),
			Interval:  connectorInterval(state, id, defaultConnectorInterval(id)),
		})
	}
	return statuses
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
	case "git", "github", "slack", "calendar.google", "calendar.microsoft", "mail.google", "mail.microsoft", "notion":
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

func defaultConnectorInterval(id string) time.Duration {
	switch id {
	case "git":
		return gitPollInterval(0)
	case "github":
		return githubPollInterval(0)
	case "slack":
		return slackPollInterval(0)
	case "calendar.google", "calendar.microsoft":
		return calendarPollInterval(0)
	case "mail.google", "mail.microsoft":
		return mailPollInterval(0)
	case "notion":
		return notionPollInterval(0)
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
		lines = append(lines, fmt.Sprintf("- %s: %s, %s, interval %s", status.ID, connected, enabled, status.Interval))
	}
	lines = append(lines, "Config: "+connectorRuntimePath(result.HomeDir))
	return strings.Join(lines, "\n")
}
