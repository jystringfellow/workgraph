package workgraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var managedSettingsPathOverrideForTest string

type managedSettingsFile struct {
	Version int                `json:"version"`
	LLM     managedLLMSettings `json:"llm"`
}

type managedLLMSettings struct {
	HostedEnabled  managedBoolSetting        `json:"hosted_enabled"`
	AllowedBaseURL managedStringSliceSetting `json:"allowed_base_urls"`
}

type managedBoolSetting struct {
	Value  *bool `json:"value"`
	Locked bool  `json:"locked"`
}

type managedStringSliceSetting struct {
	Value  []string `json:"value"`
	Locked bool     `json:"locked"`
}

func readManagedSettings() (managedSettingsFile, string, bool, error) {
	path := defaultManagedSettingsPath()
	if path == "" {
		return managedSettingsFile{}, "", false, nil
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return managedSettingsFile{}, path, false, nil
		}
		return managedSettingsFile{}, path, false, fmt.Errorf("read managed settings: %w", err)
	}
	var settings managedSettingsFile
	if err := json.Unmarshal(contents, &settings); err != nil {
		return managedSettingsFile{}, path, false, fmt.Errorf("parse managed settings: %w", err)
	}
	return settings, path, true, nil
}

// SetManagedSettingsPathForTest redirects managed settings lookup for facts.
func SetManagedSettingsPathForTest(path string) func() {
	previous := managedSettingsPathOverrideForTest
	managedSettingsPathOverrideForTest = path
	return func() {
		managedSettingsPathOverrideForTest = previous
	}
}

func defaultManagedSettingsPath() string {
	if managedSettingsPathOverrideForTest != "" {
		return managedSettingsPathOverrideForTest
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(string(filepath.Separator), "Library", "Application Support", "workgraph", "managed-settings.json")
	case "windows":
		if programData := strings.TrimSpace(os.Getenv("ProgramData")); programData != "" {
			return filepath.Join(programData, "workgraph", "managed-settings.json")
		}
		return ""
	default:
		return filepath.Join(string(filepath.Separator), "etc", "workgraph", "managed-settings.json")
	}
}

func enforceLLMManagedSettings(profile llmProfile) error {
	settings, _, present, err := readManagedSettings()
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	if len(settings.LLM.AllowedBaseURL.Value) > 0 && profile.Provider == "openai-compatible" {
		if !baseURLAllowed(profile.BaseURL, settings.LLM.AllowedBaseURL.Value) {
			return fmt.Errorf("llm destination %q is not allowed by managed settings", profile.BaseURL)
		}
		return nil
	}
	if settings.LLM.HostedEnabled.Value != nil && !*settings.LLM.HostedEnabled.Value {
		if profile.Provider != "openai-compatible" {
			return errors.New("hosted LLM providers are disabled by managed settings")
		}
		if !isLocalLLMBaseURL(profile.BaseURL) {
			return errors.New("hosted LLM providers are disabled by managed settings")
		}
	}
	return nil
}

func baseURLAllowed(baseURL string, allowed []string) bool {
	normalized := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	for _, candidate := range allowed {
		if normalized == strings.TrimRight(strings.TrimSpace(candidate), "/") {
			return true
		}
	}
	return false
}

func isLocalLLMBaseURL(baseURL string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
