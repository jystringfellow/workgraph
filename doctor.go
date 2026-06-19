package workgraph

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DoctorConfig controls local readiness diagnostics.
type DoctorConfig struct {
	HomeDir string
}

// DoctorResult describes local readiness diagnostics.
type DoctorResult struct {
	HomeDir string
	Message string
}

// Doctor reports local workgraph readiness without contacting provider APIs.
func Doctor(config DoctorConfig) (DoctorResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return DoctorResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return DoctorResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}

	lines := []string{
		"workgraph doctor",
		"Home: " + homeDir,
		doctorDatabaseLine(homeDir),
		doctorDaemonLine(homeDir),
	}
	watchLines, err := doctorWatchLines(homeDir)
	if err != nil {
		return DoctorResult{}, err
	}
	lines = append(lines, watchLines...)
	lines = append(lines, "OAuth connectors:")
	lines = append(lines, doctorOAuthConnectorLines(homeDir)...)
	llmLines, err := doctorLLMLines(homeDir)
	if err != nil {
		return DoctorResult{}, err
	}
	lines = append(lines, llmLines...)

	return DoctorResult{HomeDir: homeDir, Message: strings.Join(lines, "\n")}, nil
}

func doctorDatabaseLine(homeDir string) string {
	dbPath := filepath.Join(homeDir, "workgraph.db")
	if info, err := os.Stat(dbPath); err == nil && !info.IsDir() {
		return "Database: ok"
	}
	return "Database: missing - run workgraph init"
}

func doctorDaemonLine(homeDir string) string {
	status, err := DaemonStatusForHome(homeDir)
	if err != nil {
		return "Daemon: unknown - " + err.Error()
	}
	if status.Running {
		return fmt.Sprintf("Daemon: running PID %d", status.PID)
	}
	return "Daemon: not running"
}

func doctorWatchLines(homeDir string) ([]string, error) {
	config, err := readSettings(filepath.Join(homeDir, "settings.json"))
	if err != nil {
		return nil, err
	}
	lines := []string{fmt.Sprintf("Watch roots: %d configured", len(config.WatchDirs))}
	for _, watchDir := range config.WatchDirs {
		status := "ok"
		if info, err := os.Stat(watchDir); err != nil || !info.IsDir() {
			status = "missing"
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", watchDir, status))
	}
	return lines, nil
}

func doctorOAuthConnectorLines(homeDir string) []string {
	statuses := map[string]string{
		"slack":              doctorSlackOAuthStatus(homeDir),
		"calendar.google":    doctorCalendarOAuthStatus(homeDir, "google"),
		"calendar.microsoft": doctorCalendarOAuthStatus(homeDir, "microsoft"),
		"mail.google":        doctorMailOAuthStatus(homeDir, "google"),
		"mail.microsoft":     doctorMailOAuthStatus(homeDir, "microsoft"),
		"notion":             doctorNotionOAuthStatus(homeDir),
		"azure.boards":       doctorAzureBoardsOAuthStatus(homeDir),
	}
	keys := make([]string, 0, len(statuses))
	for key := range statuses {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("- %s: %s", key, statuses[key]))
	}
	return lines
}

func doctorSlackOAuthStatus(homeDir string) string {
	config, err := readSlackConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "not configured"
		}
		return "config error: " + err.Error()
	}
	return tokenPresence(config.AccessToken)
}

func doctorCalendarOAuthStatus(homeDir string, provider string) string {
	config, err := readCalendarConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "not configured"
		}
		return "config error: " + err.Error()
	}
	switch provider {
	case "google":
		if config.Google == nil {
			return "not configured"
		}
		return tokenPresence(config.Google.AccessToken)
	case "microsoft":
		if config.Microsoft == nil {
			return "not configured"
		}
		return tokenPresence(config.Microsoft.AccessToken)
	default:
		return "not configured"
	}
}

func doctorMailOAuthStatus(homeDir string, provider string) string {
	config, err := readMailConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "not configured"
		}
		return "config error: " + err.Error()
	}
	switch provider {
	case "google":
		if config.Google == nil {
			return "not configured"
		}
		return tokenPresence(config.Google.AccessToken)
	case "microsoft":
		if config.Microsoft == nil {
			return "not configured"
		}
		return tokenPresence(config.Microsoft.AccessToken)
	default:
		return "not configured"
	}
}

func doctorNotionOAuthStatus(homeDir string) string {
	config, err := readNotionConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "not configured"
		}
		return "config error: " + err.Error()
	}
	return tokenPresence(config.AccessToken)
}

func doctorAzureBoardsOAuthStatus(homeDir string) string {
	config, err := readAzureBoardsConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "not configured"
		}
		return "config error: " + err.Error()
	}
	if strings.TrimSpace(config.Organization) == "" || strings.TrimSpace(config.Project) == "" {
		return "missing organization or project"
	}
	return tokenPresence(config.AccessToken)
}

func tokenPresence(token string) string {
	if strings.TrimSpace(token) == "" {
		return "token missing"
	}
	return "token present"
}

func doctorLLMLines(homeDir string) ([]string, error) {
	config, err := readOrEmptyLLMConnectorConfig(homeDir)
	if err != nil {
		return nil, err
	}
	if len(config.Profiles) == 0 {
		return []string{"LLM: no profiles configured"}, nil
	}
	if strings.TrimSpace(config.DefaultProfile) == "" {
		return []string{"LLM: profiles configured, no default profile"}, nil
	}
	profile, ok := config.Profiles[config.DefaultProfile]
	if !ok {
		return []string{"LLM: default profile missing: " + config.DefaultProfile}, nil
	}
	lines := []string{
		"LLM: default profile " + config.DefaultProfile,
		"LLM provider: " + profile.Provider,
		"LLM model: " + llmProfileModelLabel(profile),
	}
	if profile.APIKeyEnv != "" {
		envStatus := "present"
		if _, ok := os.LookupEnv(profile.APIKeyEnv); !ok {
			envStatus = "missing"
		}
		lines = append(lines, fmt.Sprintf("API key env %s: %s", profile.APIKeyEnv, envStatus))
	}
	if profile.Provider == "bedrock" {
		lines = append(lines, "AWS region: "+profile.Region)
	}
	return lines, nil
}
