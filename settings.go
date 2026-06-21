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
	Format  string
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

	if config.Format == "json" {
		return settingsGetJSON(settingsPath, managedPath, managedPresent, localSettings, managed)
	}
	if config.Format != "" && config.Format != "text" {
		return SettingsGetResult{}, fmt.Errorf("unsupported settings format %q", config.Format)
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
	if len(managed.LLM.AllowedProvider.Value) > 0 {
		lines = append(lines, "LLM allowed providers: "+strings.Join(managed.LLM.AllowedProvider.Value, ", ")+" ("+managedSettingSource(managed.LLM.AllowedProvider.Locked)+")")
	}
	if len(managed.LLM.OpenAICompatible.AllowedModels.Value) > 0 {
		lines = append(lines, "OpenAI-compatible allowed models: "+strings.Join(managed.LLM.OpenAICompatible.AllowedModels.Value, ", ")+" ("+managedSettingSource(managed.LLM.OpenAICompatible.AllowedModels.Locked)+")")
	}
	if len(managed.LLM.OutboundFilter.SensitivePatterns.Value) > 0 {
		lines = append(lines, fmt.Sprintf("Outbound LLM sensitive patterns: %d (%s)", len(managed.LLM.OutboundFilter.SensitivePatterns.Value), managedSettingSource(managed.LLM.OutboundFilter.SensitivePatterns.Locked)))
	}
	if managed.LLM.OpenAICompatible.RequireModelProbe.Value != nil {
		state := "disabled"
		if *managed.LLM.OpenAICompatible.RequireModelProbe.Value {
			state = "enabled"
		}
		lines = append(lines, "OpenAI-compatible model probe required: "+state+" ("+managedSettingSource(managed.LLM.OpenAICompatible.RequireModelProbe.Locked)+")")
	}
	if len(managed.LLM.Bedrock.AllowedModelARNs.Value) > 0 {
		lines = append(lines, "Bedrock allowed model ARNs: "+strings.Join(managed.LLM.Bedrock.AllowedModelARNs.Value, ", ")+" ("+managedSettingSource(managed.LLM.Bedrock.AllowedModelARNs.Locked)+")")
	}
	if len(managed.LLM.Bedrock.AllowedInferenceProfileScopes.Value) > 0 {
		lines = append(lines, "Bedrock allowed inference profile scopes: "+bedrockInferenceProfileScopeText(managed.LLM.Bedrock.AllowedInferenceProfileScopes.Value)+" ("+managedSettingSource(managed.LLM.Bedrock.AllowedInferenceProfileScopes.Locked)+")")
	}
	if len(managed.Connectors.AllowedIDs.Value) > 0 {
		lines = append(lines, "Connector allowed IDs: "+strings.Join(managed.Connectors.AllowedIDs.Value, ", ")+" ("+managedSettingSource(managed.Connectors.AllowedIDs.Locked)+")")
	}
	if len(managed.Connectors.DisabledIDs.Value) > 0 {
		lines = append(lines, "Connector disabled IDs: "+strings.Join(managed.Connectors.DisabledIDs.Value, ", ")+" ("+managedSettingSource(managed.Connectors.DisabledIDs.Locked)+")")
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

type settingsGetJSONPayload struct {
	Settings        settingsJSONInfo          `json:"settings"`
	ManagedSettings managedSettingsJSONInfo   `json:"managed_settings"`
	LLM             llmSettingsJSONInfo       `json:"llm"`
	Connectors      connectorSettingsJSONInfo `json:"connectors"`
}

type settingsJSONInfo struct {
	Path            string `json:"path"`
	WatchDirCount   int    `json:"watch_directory_count"`
	IgnorePathCount int    `json:"ignore_path_count"`
	IgnoreNameCount int    `json:"ignore_name_count"`
	Source          string `json:"source"`
}

type managedSettingsJSONInfo struct {
	Active bool   `json:"active"`
	Path   string `json:"path"`
}

type llmSettingsJSONInfo struct {
	HostedEnabled    managedBoolJSONInfo              `json:"hosted_enabled"`
	AllowedBaseURL   managedStringSliceJSONInfo       `json:"allowed_base_urls"`
	AllowedProvider  managedStringSliceJSONInfo       `json:"allowed_providers"`
	OutboundFilter   outboundFilterSettingsJSONInfo   `json:"outbound_filter"`
	OpenAICompatible openAICompatibleSettingsJSONInfo `json:"openai_compatible"`
	Bedrock          bedrockSettingsJSONInfo          `json:"bedrock"`
}

type outboundFilterSettingsJSONInfo struct {
	SensitivePatterns managedStringSliceJSONInfo `json:"sensitive_patterns"`
}

type openAICompatibleSettingsJSONInfo struct {
	AllowedModels     managedStringSliceJSONInfo `json:"allowed_models"`
	RequireModelProbe managedBoolJSONInfo        `json:"require_model_probe"`
}

type bedrockSettingsJSONInfo struct {
	AllowedModelARNs              managedStringSliceJSONInfo                  `json:"allowed_model_arns"`
	AllowedInferenceProfileScopes managedBedrockInferenceProfileScopeJSONInfo `json:"allowed_inference_profile_scopes"`
}

type managedBedrockInferenceProfileScopeJSONInfo struct {
	Value  []managedBedrockInferenceProfileScope `json:"value"`
	Locked bool                                  `json:"locked"`
	Source string                                `json:"source"`
}

type connectorSettingsJSONInfo struct {
	AllowedIDs  managedStringSliceJSONInfo `json:"allowed_ids"`
	DisabledIDs managedStringSliceJSONInfo `json:"disabled_ids"`
	Slack       slackSettingsJSONInfo      `json:"slack"`
}

type slackSettingsJSONInfo struct {
	IncludeDMs managedBoolJSONInfo `json:"include_dms"`
}

type managedBoolJSONInfo struct {
	Value  *bool  `json:"value"`
	Locked bool   `json:"locked"`
	Source string `json:"source"`
}

type managedStringSliceJSONInfo struct {
	Value  []string `json:"value"`
	Locked bool     `json:"locked"`
	Source string   `json:"source"`
}

func settingsGetJSON(settingsPath, managedPath string, managedPresent bool, localSettings settingsFile, managed managedSettingsFile) (SettingsGetResult, error) {
	payload := settingsGetJSONPayload{
		Settings: settingsJSONInfo{
			Path:            settingsPath,
			WatchDirCount:   len(localSettings.WatchDirs),
			IgnorePathCount: len(localSettings.IgnorePaths),
			IgnoreNameCount: len(localSettings.IgnoreNames),
			Source:          "user_config",
		},
		ManagedSettings: managedSettingsJSONInfo{
			Active: managedPresent,
			Path:   managedPath,
		},
		LLM: llmSettingsJSONInfo{
			HostedEnabled:   boolManagedJSON(managed.LLM.HostedEnabled),
			AllowedBaseURL:  stringSliceManagedJSON(managed.LLM.AllowedBaseURL),
			AllowedProvider: stringSliceManagedJSON(managed.LLM.AllowedProvider),
			OutboundFilter: outboundFilterSettingsJSONInfo{
				SensitivePatterns: stringSliceManagedJSON(managed.LLM.OutboundFilter.SensitivePatterns),
			},
			OpenAICompatible: openAICompatibleSettingsJSONInfo{
				AllowedModels:     stringSliceManagedJSON(managed.LLM.OpenAICompatible.AllowedModels),
				RequireModelProbe: boolManagedJSON(managed.LLM.OpenAICompatible.RequireModelProbe),
			},
			Bedrock: bedrockSettingsJSONInfo{
				AllowedModelARNs:              stringSliceManagedJSON(managed.LLM.Bedrock.AllowedModelARNs),
				AllowedInferenceProfileScopes: bedrockInferenceProfileScopesManagedJSON(managed.LLM.Bedrock.AllowedInferenceProfileScopes),
			},
		},
		Connectors: connectorSettingsJSONInfo{
			AllowedIDs:  stringSliceManagedJSON(managed.Connectors.AllowedIDs),
			DisabledIDs: stringSliceManagedJSON(managed.Connectors.DisabledIDs),
			Slack: slackSettingsJSONInfo{
				IncludeDMs: boolManagedJSON(managed.Connectors.Slack.IncludeDMs),
			},
		},
	}
	contents, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return SettingsGetResult{}, fmt.Errorf("encode settings JSON: %w", err)
	}
	return SettingsGetResult{
		SettingsPath:        settingsPath,
		ManagedSettingsPath: managedPath,
		Message:             string(contents),
	}, nil
}

func boolManagedJSON(setting managedBoolSetting) managedBoolJSONInfo {
	source := "user_config"
	if setting.Value != nil {
		source = "managed"
	}
	return managedBoolJSONInfo{
		Value:  setting.Value,
		Locked: setting.Locked,
		Source: source,
	}
}

func stringSliceManagedJSON(setting managedStringSliceSetting) managedStringSliceJSONInfo {
	source := "user_config"
	if len(setting.Value) > 0 {
		source = "managed"
	}
	value := append([]string(nil), setting.Value...)
	return managedStringSliceJSONInfo{
		Value:  value,
		Locked: setting.Locked,
		Source: source,
	}
}

func bedrockInferenceProfileScopesManagedJSON(setting managedBedrockInferenceProfileScopeSetting) managedBedrockInferenceProfileScopeJSONInfo {
	source := "user_config"
	if len(setting.Value) > 0 {
		source = "managed"
	}
	value := append([]managedBedrockInferenceProfileScope(nil), setting.Value...)
	return managedBedrockInferenceProfileScopeJSONInfo{
		Value:  value,
		Locked: setting.Locked,
		Source: source,
	}
}

func bedrockInferenceProfileScopeText(scopes []managedBedrockInferenceProfileScope) string {
	labels := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		labels = append(labels, strings.TrimSpace(scope.AccountID)+" "+strings.TrimSpace(scope.Region))
	}
	return strings.Join(labels, ", ")
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
