package workgraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SettingsWatchConfig controls updates to settings watch roots.
type SettingsWatchConfig struct {
	HomeDir string
	Path    string
}

// SettingsWatchResult describes a watch root added to the settings.
type SettingsWatchResult struct {
	SettingsPath string
	AddedPath    string
	WatchDirs    []string
	Message      string
}

// SettingsIgnoreConfig controls updates to settings ignore rules.
type SettingsIgnoreConfig struct {
	HomeDir string
	Path    string
	Name    string
}

// SettingsGetConfig controls effective settings reporting.
type SettingsGetConfig struct {
	HomeDir string
}

// SettingsGetResult describes effective settings visible to users/admins.
type SettingsGetResult struct {
	SettingsPath        string
	ManagedSettingsPath string
	Message             string
}

// SettingsDoctorConfig controls settings validation.
type SettingsDoctorConfig struct {
	HomeDir string
}

// SettingsDoctorResult describes settings validation output.
type SettingsDoctorResult struct {
	SettingsPath        string
	ManagedSettingsPath string
	Message             string
	OK                  bool
}

// GetSettings reports effective local settings without exposing secrets.
func GetSettings(config SettingsGetConfig) (SettingsGetResult, error) {
	homeDir, err := resolvedSettingsHome(config.HomeDir)
	if err != nil {
		return SettingsGetResult{}, err
	}
	settingsPath := filepath.Join(homeDir, "settings.json")
	localSettings, err := readSettings(settingsPath)
	if err != nil {
		return SettingsGetResult{}, err
	}
	managed, managedPath, managedPresent, err := readManagedSettings()
	if err != nil {
		return SettingsGetResult{}, err
	}

	lines := []string{
		"Effective workgraph settings",
		"Settings: " + settingsPath,
	}
	if managedPresent {
		lines = append(lines, "Managed settings: "+managedPath)
	} else if managedPath != "" {
		lines = append(lines, "Managed settings: not found at "+managedPath)
	} else {
		lines = append(lines, "Managed settings: none")
	}
	lines = append(lines,
		fmt.Sprintf("Watch directories: %d (user config)", len(localSettings.WatchDirs)),
		fmt.Sprintf("Ignore paths: %d (user config)", len(localSettings.IgnorePaths)),
		fmt.Sprintf("Ignore names: %d (user config)", len(localSettings.IgnoreNames)),
	)
	if managed.LLM.HostedEnabled.Value != nil {
		state := "enabled"
		if !*managed.LLM.HostedEnabled.Value {
			state = "disabled"
		}
		lines = append(lines, "LLM hosted providers: "+state+" ("+managedSettingSource(managed.LLM.HostedEnabled.Locked)+")")
	} else {
		lines = append(lines, "LLM hosted providers: user configured")
	}
	if len(managed.LLM.AllowedBaseURL.Value) > 0 {
		lines = append(lines, "LLM allowed base URLs: "+strings.Join(managed.LLM.AllowedBaseURL.Value, ", ")+" ("+managedSettingSource(managed.LLM.AllowedBaseURL.Locked)+")")
	}
	if managed.Connectors.Slack.IncludeDMs.Value != nil {
		state := "enabled"
		if !*managed.Connectors.Slack.IncludeDMs.Value {
			state = "disabled"
		}
		lines = append(lines, "Slack DM capture: "+state+" ("+managedSettingSource(managed.Connectors.Slack.IncludeDMs.Locked)+")")
	}

	return SettingsGetResult{
		SettingsPath:        settingsPath,
		ManagedSettingsPath: managedPath,
		Message:             strings.Join(lines, "\n"),
	}, nil
}

// DoctorSettings validates local and managed settings without printing secrets.
func DoctorSettings(config SettingsDoctorConfig) (SettingsDoctorResult, error) {
	homeDir, err := resolvedSettingsHome(config.HomeDir)
	if err != nil {
		return SettingsDoctorResult{}, err
	}
	settingsPath := filepath.Join(homeDir, "settings.json")
	lines := []string{"workgraph settings doctor"}
	ok := true
	if _, err := readSettings(settingsPath); err != nil {
		ok = false
		lines = append(lines, "Settings: invalid", "Settings path: "+settingsPath, "Settings error: "+err.Error())
	} else {
		lines = append(lines, "Settings: ok", "Settings path: "+settingsPath)
	}

	_, managedPath, managedPresent, err := readManagedSettings()
	if err != nil {
		ok = false
		lines = append(lines, "Managed settings: invalid", "Managed settings path: "+managedPath, "Managed settings error: "+err.Error())
	} else if managedPresent {
		lines = append(lines, "Managed settings: ok", "Managed settings path: "+managedPath)
	} else if managedPath != "" {
		lines = append(lines, "Managed settings: not found", "Managed settings path: "+managedPath)
	} else {
		lines = append(lines, "Managed settings: none")
	}

	result := SettingsDoctorResult{
		SettingsPath:        settingsPath,
		ManagedSettingsPath: managedPath,
		Message:             strings.Join(lines, "\n"),
		OK:                  ok,
	}
	if !ok {
		return result, errors.New("settings validation failed")
	}
	return result, nil
}

func managedSettingSource(locked bool) string {
	if locked {
		return "managed settings locked"
	}
	return "managed settings default"
}

// AddWatchDir prepends a resolved watch directory to workgraph settings.
func AddWatchDir(config SettingsWatchConfig) (SettingsWatchResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return SettingsWatchResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return SettingsWatchResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}

	settingsPath := filepath.Join(homeDir, "settings.json")
	localSettings, err := readSettings(settingsPath)
	if err != nil {
		return SettingsWatchResult{}, err
	}

	watchDir := config.Path
	if watchDir == "" {
		watchDir = "."
	}
	watchDir, err = filepath.Abs(watchDir)
	if err != nil {
		return SettingsWatchResult{}, fmt.Errorf("resolve watch directory: %w", err)
	}

	info, err := os.Stat(watchDir)
	if err != nil {
		return SettingsWatchResult{}, fmt.Errorf("watch directory %q: %w", watchDir, err)
	}
	if !info.IsDir() {
		return SettingsWatchResult{}, fmt.Errorf("watch path %q is not a directory", watchDir)
	}

	localSettings.WatchDirs = prependUniquePath(watchDir, localSettings.WatchDirs)
	localSettings.ConservativeWatchDirs = removePath(watchDir, localSettings.ConservativeWatchDirs)
	if err := writeSettings(settingsPath, localSettings); err != nil {
		return SettingsWatchResult{}, err
	}

	result := SettingsWatchResult{
		SettingsPath: settingsPath,
		AddedPath:    watchDir,
		WatchDirs:    append([]string(nil), localSettings.WatchDirs...),
	}
	result.Message = strings.Join([]string{
		"workgraph settings updated",
		"Settings: " + result.SettingsPath,
		"Added watch directory: " + result.AddedPath,
	}, "\n")

	return result, nil
}

func prependUniquePath(path string, paths []string) []string {
	result := []string{path}
	for _, existing := range paths {
		if existing == path {
			continue
		}
		result = append(result, existing)
	}
	return result
}

func removePath(path string, paths []string) []string {
	result := []string{}
	for _, existing := range paths {
		if existing == path {
			continue
		}
		result = append(result, existing)
	}
	return result
}

func addIgnorePath(config SettingsIgnoreConfig) (string, error) {
	homeDir, err := resolvedSettingsHome(config.HomeDir)
	if err != nil {
		return "", err
	}
	settingsPath := filepath.Join(homeDir, "settings.json")
	localSettings, err := readSettings(settingsPath)
	if err != nil {
		return "", err
	}

	ignorePath := strings.TrimSpace(config.Path)
	if ignorePath == "" {
		return "", fmt.Errorf("ignore path is required")
	}
	ignorePath, err = filepath.Abs(ignorePath)
	if err != nil {
		return "", fmt.Errorf("resolve ignore path: %w", err)
	}
	localSettings.IgnorePaths = appendUniqueString(localSettings.IgnorePaths, ignorePath)
	if err := writeSettings(settingsPath, localSettings); err != nil {
		return "", err
	}
	return ignorePath, nil
}

func addIgnoreName(config SettingsIgnoreConfig) (string, error) {
	homeDir, err := resolvedSettingsHome(config.HomeDir)
	if err != nil {
		return "", err
	}
	settingsPath := filepath.Join(homeDir, "settings.json")
	localSettings, err := readSettings(settingsPath)
	if err != nil {
		return "", err
	}

	ignoreName := strings.TrimSpace(config.Name)
	if ignoreName == "" {
		return "", fmt.Errorf("ignore name is required")
	}
	localSettings.IgnoreNames = appendUniqueString(localSettings.IgnoreNames, ignoreName)
	if err := writeSettings(settingsPath, localSettings); err != nil {
		return "", err
	}
	return ignoreName, nil
}

func resolvedSettingsHome(homeDir string) (string, error) {
	homeDir, err := resolveHomeDir(homeDir)
	if err != nil {
		return "", err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return "", fmt.Errorf("resolve workgraph home: %w", err)
	}
	return homeDir, nil
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return append([]string(nil), values...)
		}
	}
	result := append([]string(nil), values...)
	return append(result, value)
}

func writeSettings(settingsPath string, config settingsFile) error {
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	contents = append(contents, '\n')

	if err := os.WriteFile(settingsPath, contents, 0o644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}
