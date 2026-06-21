package workgraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var managedSettingsPathOverrideForTest string

type managedSettingsFile struct {
	Version    int                      `json:"version"`
	LLM        managedLLMSettings       `json:"llm"`
	Connectors managedConnectorSettings `json:"connectors"`
}

type managedLLMSettings struct {
	HostedEnabled    managedBoolSetting              `json:"hosted_enabled"`
	AllowedBaseURL   managedStringSliceSetting       `json:"allowed_base_urls"`
	AllowedProvider  managedStringSliceSetting       `json:"allowed_providers"`
	OutboundFilter   managedOutboundFilterSettings   `json:"outbound_filter"`
	OpenAICompatible managedOpenAICompatibleSettings `json:"openai_compatible"`
	Bedrock          managedBedrockSettings          `json:"bedrock"`
}

type managedOutboundFilterSettings struct {
	SensitivePatterns managedStringSliceSetting `json:"sensitive_patterns"`
}

type managedOpenAICompatibleSettings struct {
	AllowedModels     managedStringSliceSetting `json:"allowed_models"`
	RequireModelProbe managedBoolSetting        `json:"require_model_probe"`
}

type managedBedrockSettings struct {
	AllowedModelARNs              managedStringSliceSetting                  `json:"allowed_model_arns"`
	AllowedInferenceProfileScopes managedBedrockInferenceProfileScopeSetting `json:"allowed_inference_profile_scopes"`
}

type managedBedrockInferenceProfileScopeSetting struct {
	Value  []managedBedrockInferenceProfileScope `json:"value"`
	Locked bool                                  `json:"locked"`
}

type managedBedrockInferenceProfileScope struct {
	AccountID string `json:"account_id"`
	Region    string `json:"region"`
}

type managedConnectorSettings struct {
	AllowedIDs  managedStringSliceSetting `json:"allowed_ids"`
	DisabledIDs managedStringSliceSetting `json:"disabled_ids"`
	Slack       managedSlackSettings      `json:"slack"`
}

type managedSlackSettings struct {
	IncludeDMs managedBoolSetting `json:"include_dms"`
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

func enforceLLMManagedSettings(profile llmProfile, client *http.Client) error {
	settings, _, present, err := readManagedSettings()
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	if len(settings.LLM.AllowedProvider.Value) > 0 && !stringAllowedFold(profile.Provider, settings.LLM.AllowedProvider.Value) {
		return fmt.Errorf("llm provider %q is not allowed by managed settings", profile.Provider)
	}
	if len(settings.LLM.AllowedBaseURL.Value) > 0 && profile.Provider == "openai-compatible" {
		if !baseURLAllowed(profile.BaseURL, settings.LLM.AllowedBaseURL.Value) {
			return fmt.Errorf("llm destination %q is not allowed by managed settings", profile.BaseURL)
		}
	}
	if len(settings.LLM.OpenAICompatible.AllowedModels.Value) > 0 && profile.Provider == "openai-compatible" {
		if !stringAllowed(profile.Model, settings.LLM.OpenAICompatible.AllowedModels.Value) {
			return fmt.Errorf("OpenAI-compatible model %q is not allowed by managed settings", profile.Model)
		}
	}
	if managedBoolLockedTrue(settings.LLM.OpenAICompatible.RequireModelProbe) && profile.Provider == "openai-compatible" {
		if err := requireOpenAICompatibleModelProbe(client, profile); err != nil {
			return err
		}
	}
	if bedrockModelARNRestricted(settings.LLM.Bedrock) && profile.Provider == "bedrock" {
		if !bedrockModelARNAllowed(profile.ModelARN, settings.LLM.Bedrock) {
			return fmt.Errorf("Bedrock model ARN is not allowed by managed settings: %s", profile.ModelARN)
		}
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

func enforceSlackDMManagedSettings(includeDMs bool) error {
	if !includeDMs {
		return nil
	}
	settings, _, present, err := readManagedSettings()
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	if managedBoolLockedFalse(settings.Connectors.Slack.IncludeDMs) {
		return errors.New("Slack DM capture is disabled by managed settings")
	}
	return nil
}

func enforceConnectorManagedSettings(id string) error {
	id, err := normalizeConnectorID(id)
	if err != nil {
		return err
	}
	settings, _, present, err := readManagedSettings()
	if err != nil {
		return err
	}
	return connectorManagedPolicyError(settings, present, id)
}

func connectorManagedPolicyError(settings managedSettingsFile, present bool, id string) error {
	allowed, reason := connectorAllowedByManagedPolicy(settings, present, id)
	if allowed {
		return nil
	}
	return fmt.Errorf("connector %s %s by managed settings", id, reason)
}

func connectorAllowedByManagedPolicy(settings managedSettingsFile, present bool, id string) (bool, string) {
	if !present {
		return true, ""
	}
	if stringAllowedFold(id, settings.Connectors.DisabledIDs.Value) {
		return false, "is disabled"
	}
	if len(settings.Connectors.AllowedIDs.Value) > 0 && !stringAllowedFold(id, settings.Connectors.AllowedIDs.Value) {
		return false, "is not allowed"
	}
	return true, ""
}

func managedBoolLockedFalse(setting managedBoolSetting) bool {
	return setting.Value != nil && setting.Locked && !*setting.Value
}

func managedBoolLockedTrue(setting managedBoolSetting) bool {
	return setting.Value != nil && setting.Locked && *setting.Value
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

func stringAllowed(value string, allowed []string) bool {
	normalized := strings.TrimSpace(value)
	for _, candidate := range allowed {
		if normalized == strings.TrimSpace(candidate) {
			return true
		}
	}
	return false
}

func stringAllowedFold(value string, allowed []string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range allowed {
		if normalized == strings.ToLower(strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

func bedrockModelARNRestricted(settings managedBedrockSettings) bool {
	return len(settings.AllowedModelARNs.Value) > 0 || len(settings.AllowedInferenceProfileScopes.Value) > 0
}

func bedrockModelARNAllowed(modelARN string, settings managedBedrockSettings) bool {
	if stringAllowed(modelARN, settings.AllowedModelARNs.Value) {
		return true
	}
	for _, scope := range settings.AllowedInferenceProfileScopes.Value {
		if bedrockInferenceProfileARNInScope(modelARN, scope) {
			return true
		}
	}
	return false
}

func bedrockInferenceProfileARNInScope(modelARN string, scope managedBedrockInferenceProfileScope) bool {
	parsed := parseAWSARN(modelARN)
	if parsed.Service != "bedrock" {
		return false
	}
	if parsed.Region != strings.TrimSpace(scope.Region) || parsed.AccountID != strings.TrimSpace(scope.AccountID) {
		return false
	}
	return strings.HasPrefix(parsed.Resource, "inference-profile/")
}

type awsARNParts struct {
	Partition string
	Service   string
	Region    string
	AccountID string
	Resource  string
}

func parseAWSARN(value string) awsARNParts {
	parts := strings.SplitN(strings.TrimSpace(value), ":", 6)
	if len(parts) != 6 || parts[0] != "arn" {
		return awsARNParts{}
	}
	return awsARNParts{
		Partition: parts[1],
		Service:   parts[2],
		Region:    parts[3],
		AccountID: parts[4],
		Resource:  parts[5],
	}
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
