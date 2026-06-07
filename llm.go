package workgraph

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const llmTaskSummarize = "summarize"

type LLMAddProfileConfig struct {
	HomeDir    string
	Name       string
	Provider   string
	BaseURL    string
	Model      string
	APIKeyEnv  string
	AWSProfile string
	Region     string
	ModelARN   string
}

type LLMListConfig struct {
	HomeDir string
}

type LLMRemoveProfileConfig struct {
	HomeDir string
	Name    string
}

type LLMUseProfileConfig struct {
	HomeDir string
	Name    string
	Task    string
}

type LLMTestConfig struct {
	HomeDir    string
	Profile    string
	HTTPClient *http.Client
}

type LLMSummarizeTodayConfig struct {
	HomeDir string
	DryRun  bool
}

type LLMResult struct {
	ConfigPath string
	Message    string
}

type llmConnectorConfig struct {
	DefaultProfile string                `json:"default_profile,omitempty"`
	TaskProfiles   map[string]string     `json:"task_profiles,omitempty"`
	Profiles       map[string]llmProfile `json:"profiles"`
}

type llmProfile struct {
	Provider   string `json:"provider"`
	BaseURL    string `json:"base_url,omitempty"`
	Model      string `json:"model,omitempty"`
	APIKeyEnv  string `json:"api_key_env,omitempty"`
	AWSProfile string `json:"aws_profile,omitempty"`
	Region     string `json:"region,omitempty"`
	ModelARN   string `json:"model_arn,omitempty"`
}

type openAICompatibleRequest struct {
	Model    string                    `json:"model"`
	Messages []openAICompatibleMessage `json:"messages"`
}

type openAICompatibleMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAICompatibleResponse struct {
	Choices []struct {
		Message openAICompatibleMessage `json:"message"`
	} `json:"choices"`
}

func AddLLMProfile(config LLMAddProfileConfig) (LLMResult, error) {
	homeDir, err := resolveLLMHomeDir(config.HomeDir)
	if err != nil {
		return LLMResult{}, err
	}
	if strings.TrimSpace(config.Name) == "" {
		return LLMResult{}, errors.New("llm profile name is required")
	}
	profile := llmProfile{
		Provider:   strings.TrimSpace(config.Provider),
		BaseURL:    strings.TrimRight(strings.TrimSpace(config.BaseURL), "/"),
		Model:      strings.TrimSpace(config.Model),
		APIKeyEnv:  strings.TrimSpace(config.APIKeyEnv),
		AWSProfile: strings.TrimSpace(config.AWSProfile),
		Region:     strings.TrimSpace(config.Region),
		ModelARN:   strings.TrimSpace(config.ModelARN),
	}
	if err := validateLLMProfile(profile); err != nil {
		return LLMResult{}, err
	}

	stored, err := readOrEmptyLLMConnectorConfig(homeDir)
	if err != nil {
		return LLMResult{}, err
	}
	if stored.Profiles == nil {
		stored.Profiles = map[string]llmProfile{}
	}
	stored.Profiles[config.Name] = profile
	configPath := llmConfigPath(homeDir)
	if err := writeLLMConnectorConfig(configPath, stored); err != nil {
		return LLMResult{}, err
	}
	return LLMResult{
		ConfigPath: configPath,
		Message: strings.Join([]string{
			"LLM profile added: " + config.Name,
			"Provider: " + profile.Provider,
			"Model: " + llmProfileModelLabel(profile),
			"Config: " + configPath,
		}, "\n"),
	}, nil
}

func ListLLMProfiles(config LLMListConfig) (LLMResult, error) {
	homeDir, err := resolveLLMHomeDir(config.HomeDir)
	if err != nil {
		return LLMResult{}, err
	}
	stored, err := readOrEmptyLLMConnectorConfig(homeDir)
	if err != nil {
		return LLMResult{}, err
	}
	configPath := llmConfigPath(homeDir)
	lines := []string{"LLM profiles"}
	if len(stored.Profiles) == 0 {
		lines = append(lines, "No LLM profiles configured.", "Config: "+configPath)
		return LLMResult{ConfigPath: configPath, Message: strings.Join(lines, "\n")}, nil
	}
	names := make([]string, 0, len(stored.Profiles))
	for name := range stored.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		profile := stored.Profiles[name]
		marker := ""
		if name == stored.DefaultProfile {
			marker = " default"
		}
		lines = append(lines, fmt.Sprintf("- %s: %s %s%s", name, profile.Provider, llmProfileModelLabel(profile), marker))
	}
	if len(stored.TaskProfiles) > 0 {
		lines = append(lines, "", "Task profiles")
		tasks := make([]string, 0, len(stored.TaskProfiles))
		for task := range stored.TaskProfiles {
			tasks = append(tasks, task)
		}
		sort.Strings(tasks)
		for _, task := range tasks {
			lines = append(lines, fmt.Sprintf("- %s: %s", task, stored.TaskProfiles[task]))
		}
	}
	lines = append(lines, "Config: "+configPath)
	return LLMResult{ConfigPath: configPath, Message: strings.Join(lines, "\n")}, nil
}

func RemoveLLMProfile(config LLMRemoveProfileConfig) (LLMResult, error) {
	homeDir, err := resolveLLMHomeDir(config.HomeDir)
	if err != nil {
		return LLMResult{}, err
	}
	name := strings.TrimSpace(config.Name)
	if name == "" {
		return LLMResult{}, errors.New("llm profile name is required")
	}
	stored, err := readLLMConnectorConfig(homeDir)
	if err != nil {
		return LLMResult{}, err
	}
	if _, ok := stored.Profiles[name]; !ok {
		return LLMResult{}, fmt.Errorf("llm profile %q is not configured", name)
	}
	delete(stored.Profiles, name)
	if stored.DefaultProfile == name {
		stored.DefaultProfile = ""
	}
	for task, profileName := range stored.TaskProfiles {
		if profileName == name {
			delete(stored.TaskProfiles, task)
		}
	}
	if len(stored.TaskProfiles) == 0 {
		stored.TaskProfiles = nil
	}
	configPath := llmConfigPath(homeDir)
	if err := writeLLMConnectorConfig(configPath, stored); err != nil {
		return LLMResult{}, err
	}
	return LLMResult{
		ConfigPath: configPath,
		Message: strings.Join([]string{
			"LLM profile removed: " + name,
			"Config: " + configPath,
		}, "\n"),
	}, nil
}

func UseLLMProfile(config LLMUseProfileConfig) (LLMResult, error) {
	homeDir, err := resolveLLMHomeDir(config.HomeDir)
	if err != nil {
		return LLMResult{}, err
	}
	stored, err := readLLMConnectorConfig(homeDir)
	if err != nil {
		return LLMResult{}, err
	}
	if _, ok := stored.Profiles[config.Name]; !ok {
		return LLMResult{}, fmt.Errorf("llm profile %q is not configured", config.Name)
	}
	configPath := llmConfigPath(homeDir)
	task := strings.TrimSpace(config.Task)
	if task == "" {
		stored.DefaultProfile = config.Name
		if err := writeLLMConnectorConfig(configPath, stored); err != nil {
			return LLMResult{}, err
		}
		return LLMResult{
			ConfigPath: configPath,
			Message: strings.Join([]string{
				"Default LLM profile: " + config.Name,
				"Config: " + configPath,
			}, "\n"),
		}, nil
	}
	if stored.TaskProfiles == nil {
		stored.TaskProfiles = map[string]string{}
	}
	stored.TaskProfiles[task] = config.Name
	if err := writeLLMConnectorConfig(configPath, stored); err != nil {
		return LLMResult{}, err
	}
	return LLMResult{
		ConfigPath: configPath,
		Message: strings.Join([]string{
			"LLM profile for " + task + ": " + config.Name,
			"Config: " + configPath,
		}, "\n"),
	}, nil
}

func TestLLMProfile(config LLMTestConfig) (LLMResult, error) {
	homeDir, err := resolveLLMHomeDir(config.HomeDir)
	if err != nil {
		return LLMResult{}, err
	}
	stored, err := readLLMConnectorConfig(homeDir)
	if err != nil {
		return LLMResult{}, err
	}
	profileName, profile, err := resolveLLMProfile(stored, config.Profile, "")
	if err != nil {
		return LLMResult{}, err
	}
	if profile.Provider != "openai-compatible" {
		return LLMResult{}, fmt.Errorf("llm test only supports openai-compatible profiles for now, got %q", profile.Provider)
	}
	responseText, err := callOpenAICompatible(config.HTTPClient, profile, []openAICompatibleMessage{
		{Role: "system", Content: "You are testing a local language model connection."},
		{Role: "user", Content: "Reply with a short confirmation that the model connection works."},
	})
	if err != nil {
		return LLMResult{}, err
	}
	return LLMResult{
		ConfigPath: llmConfigPath(homeDir),
		Message: strings.Join([]string{
			"LLM test complete",
			"Profile: " + profileName,
			"Provider: " + profile.Provider,
			"Model: " + profile.Model,
			"Destination: " + profile.BaseURL,
			"Response: " + responseText,
		}, "\n"),
	}, nil
}

func SummarizeTodayWithLLM(config LLMSummarizeTodayConfig) (LLMResult, error) {
	homeDir, err := resolveLLMHomeDir(config.HomeDir)
	if err != nil {
		return LLMResult{}, err
	}
	stored, err := readLLMConnectorConfig(homeDir)
	if err != nil {
		return LLMResult{}, err
	}
	profileName, profile, err := resolveLLMProfile(stored, "", llmTaskSummarize)
	if err != nil {
		return LLMResult{}, err
	}
	context, prompt, err := llmTodayContextAndPrompt(homeDir)
	if err != nil {
		return LLMResult{}, err
	}
	if config.DryRun {
		return LLMResult{
			ConfigPath: llmConfigPath(homeDir),
			Message: strings.Join([]string{
				"LLM summarize today dry run",
				"Profile: " + profileName,
				"Provider: " + profile.Provider,
				"Model: " + llmProfileModelLabel(profile),
				"Destination: " + llmProfileDestination(profile),
				"",
				"Prompt:",
				prompt,
				"",
				"Context:",
				context,
			}, "\n"),
		}, nil
	}
	return LLMResult{}, errors.New("llm summarize today is only implemented for --dry-run")
}

func callOpenAICompatible(client *http.Client, profile llmProfile, messages []openAICompatibleMessage) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	body, err := json.Marshal(openAICompatibleRequest{
		Model:    profile.Model,
		Messages: messages,
	})
	if err != nil {
		return "", fmt.Errorf("encode OpenAI-compatible request: %w", err)
	}
	requestURL := strings.TrimRight(profile.BaseURL, "/") + "/chat/completions"
	request, err := http.NewRequest(http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build OpenAI-compatible request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if profile.APIKeyEnv != "" {
		if key := os.Getenv(profile.APIKeyEnv); key != "" {
			request.Header.Set("Authorization", "Bearer "+key)
		}
	}

	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("call OpenAI-compatible profile: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return "", fmt.Errorf("read OpenAI-compatible response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("call OpenAI-compatible profile: status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var parsed openAICompatibleResponse
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return "", fmt.Errorf("parse OpenAI-compatible response: %w", err)
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return "", errors.New("OpenAI-compatible response did not include assistant text")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

func llmTodayContextAndPrompt(homeDir string) (string, string, error) {
	today, err := Today(TodayConfig{HomeDir: homeDir})
	if err != nil {
		return "", "", err
	}
	prompt := strings.Join([]string{
		"Summarize today's captured work context.",
		"Group related activity when possible.",
		"Call out notable projects and open loops.",
		"Use only the provided context.",
	}, "\n")
	return today.Message, prompt, nil
}

func resolveLLMProfile(config llmConnectorConfig, requestedProfile string, task string) (string, llmProfile, error) {
	profileName := strings.TrimSpace(requestedProfile)
	if profileName == "" && task != "" && config.TaskProfiles != nil {
		profileName = config.TaskProfiles[task]
	}
	if profileName == "" {
		profileName = config.DefaultProfile
	}
	if profileName == "" {
		return "", llmProfile{}, errors.New("no llm profile selected")
	}
	profile, ok := config.Profiles[profileName]
	if !ok {
		return "", llmProfile{}, fmt.Errorf("llm profile %q is not configured", profileName)
	}
	return profileName, profile, nil
}

func validateLLMProfile(profile llmProfile) error {
	switch profile.Provider {
	case "":
		return errors.New("llm provider is required")
	case "openai-compatible":
		if profile.BaseURL == "" {
			return errors.New("openai-compatible llm base URL is required")
		}
		if profile.Model == "" {
			return errors.New("openai-compatible llm model is required")
		}
	case "bedrock":
		if profile.Region == "" {
			return errors.New("bedrock llm region is required")
		}
		if profile.ModelARN == "" {
			return errors.New("bedrock llm model ARN is required")
		}
	case "openai", "anthropic", "google", "azure":
		if profile.Model == "" && profile.ModelARN == "" {
			return fmt.Errorf("%s llm model is required", profile.Provider)
		}
	default:
		return fmt.Errorf("unsupported llm provider %q", profile.Provider)
	}
	return nil
}

func readLLMConnectorConfig(homeDir string) (llmConnectorConfig, error) {
	path := llmConfigPath(homeDir)
	contents, err := os.ReadFile(path)
	if err != nil {
		return llmConnectorConfig{}, err
	}
	var config llmConnectorConfig
	if err := json.Unmarshal(contents, &config); err != nil {
		return llmConnectorConfig{}, fmt.Errorf("parse llm config: %w", err)
	}
	if config.Profiles == nil {
		config.Profiles = map[string]llmProfile{}
	}
	return config, nil
}

func readOrEmptyLLMConnectorConfig(homeDir string) (llmConnectorConfig, error) {
	config, err := readLLMConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return llmConnectorConfig{Profiles: map[string]llmProfile{}}, nil
		}
		return llmConnectorConfig{}, err
	}
	return config, nil
}

func writeLLMConnectorConfig(path string, config llmConnectorConfig) error {
	if config.Profiles == nil {
		config.Profiles = map[string]llmProfile{}
	}
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode llm config: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		return fmt.Errorf("write llm config: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure llm config: %w", err)
	}
	return nil
}

func resolveLLMHomeDir(homeDir string) (string, error) {
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

func llmConfigPath(homeDir string) string {
	return filepath.Join(homeDir, "llm.json")
}

func llmProfileModelLabel(profile llmProfile) string {
	if profile.Model != "" {
		return profile.Model
	}
	return profile.ModelARN
}

func llmProfileDestination(profile llmProfile) string {
	switch profile.Provider {
	case "openai-compatible":
		return profile.BaseURL
	case "bedrock":
		return "bedrock://" + profile.Region + "/" + profile.ModelARN
	default:
		return profile.Provider
	}
}
