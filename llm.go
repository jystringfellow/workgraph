package workgraph

import (
	"bufio"
	"bytes"
	"context"
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

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
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
	HomeDir    string
	DryRun     bool
	HTTPClient *http.Client
	Stream     func(string) error
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
	Stream   bool                      `json:"stream,omitempty"`
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

type openAICompatibleStreamResponse struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
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
	responseText, err := callLLMProfile(config.HTTPClient, profile, []openAICompatibleMessage{
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
			"Model: " + llmProfileModelLabel(profile),
			"Destination: " + llmProfileDestination(profile),
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
	messages := []openAICompatibleMessage{
		{Role: "system", Content: "You write concise workgraph daily work summaries from captured local work evidence."},
		{Role: "user", Content: strings.Join([]string{
			"Prompt:",
			prompt,
			"",
			"Context:",
			context,
		}, "\n")},
	}
	var responseText string
	if config.Stream != nil {
		if err := config.Stream(strings.Join([]string{
			"LLM summarize today streaming",
			"Profile: " + profileName,
			"Provider: " + profile.Provider,
			"Model: " + llmProfileModelLabel(profile),
			"Destination: " + llmProfileDestination(profile),
			"",
			"Thinking...",
			"",
			"Summary:",
		}, "\n") + "\n"); err != nil {
			return LLMResult{}, err
		}
		responseText, err = callLLMProfileStream(config.HTTPClient, profile, messages, config.Stream)
	} else {
		responseText, err = callLLMProfile(config.HTTPClient, profile, messages)
	}
	if err != nil {
		return LLMResult{}, err
	}
	if config.Stream != nil {
		if err := config.Stream("\n"); err != nil {
			return LLMResult{}, err
		}
		return LLMResult{ConfigPath: llmConfigPath(homeDir)}, nil
	}
	return LLMResult{
		ConfigPath: llmConfigPath(homeDir),
		Message: strings.Join([]string{
			"LLM summarize today complete",
			"Profile: " + profileName,
			"Provider: " + profile.Provider,
			"Model: " + llmProfileModelLabel(profile),
			"Destination: " + llmProfileDestination(profile),
			"",
			"Summary:",
			responseText,
		}, "\n"),
	}, nil
}

func callLLMProfile(client *http.Client, profile llmProfile, messages []openAICompatibleMessage) (string, error) {
	switch profile.Provider {
	case "openai-compatible":
		return callOpenAICompatible(client, profile, messages)
	case "bedrock":
		return callBedrockConverse(client, profile, messages)
	default:
		return "", fmt.Errorf("llm calls do not support provider %q yet", profile.Provider)
	}
}

func callLLMProfileStream(client *http.Client, profile llmProfile, messages []openAICompatibleMessage, stream func(string) error) (string, error) {
	switch profile.Provider {
	case "openai-compatible":
		return callOpenAICompatibleStream(client, profile, messages, stream)
	case "bedrock":
		return callBedrockConverseStream(client, profile, messages, stream)
	default:
		response, err := callLLMProfile(client, profile, messages)
		if err != nil {
			return "", err
		}
		if stream != nil && response != "" {
			if err := stream(response); err != nil {
				return "", err
			}
		}
		return response, nil
	}
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

func callOpenAICompatibleStream(client *http.Client, profile llmProfile, messages []openAICompatibleMessage, stream func(string) error) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	body, err := json.Marshal(openAICompatibleRequest{
		Model:    profile.Model,
		Messages: messages,
		Stream:   true,
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
	request.Header.Set("Accept", "text/event-stream")
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
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		responseBody, err := io.ReadAll(response.Body)
		if err != nil {
			return "", fmt.Errorf("read OpenAI-compatible response: %w", err)
		}
		return "", fmt.Errorf("call OpenAI-compatible profile: status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var builder strings.Builder
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var parsed openAICompatibleStreamResponse
		if err := json.Unmarshal([]byte(data), &parsed); err != nil {
			return "", fmt.Errorf("parse OpenAI-compatible stream response: %w", err)
		}
		for _, choice := range parsed.Choices {
			chunk := choice.Delta.Content
			if chunk == "" {
				continue
			}
			builder.WriteString(chunk)
			if stream != nil {
				if err := stream(chunk); err != nil {
					return "", err
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read OpenAI-compatible stream response: %w", err)
	}
	if strings.TrimSpace(builder.String()) == "" {
		return "", errors.New("OpenAI-compatible stream response did not include assistant text")
	}
	return strings.TrimSpace(builder.String()), nil
}

func callBedrockConverse(client *http.Client, profile llmProfile, messages []openAICompatibleMessage) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	loadOptions := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(profile.Region),
	}
	if profile.AWSProfile != "" {
		loadOptions = append(loadOptions, awsconfig.WithSharedConfigProfile(profile.AWSProfile))
	}
	if client != nil {
		loadOptions = append(loadOptions, awsconfig.WithHTTPClient(client))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return "", fmt.Errorf("load Bedrock AWS config: %w", err)
	}
	if profile.BaseURL != "" {
		cfg.BaseEndpoint = aws.String(profile.BaseURL)
	}

	bedrock := bedrockruntime.NewFromConfig(cfg)
	output, err := bedrock.Converse(ctx, &bedrockruntime.ConverseInput{
		ModelId: aws.String(profile.ModelARN),
		Messages: []types.Message{
			{
				Role: types.ConversationRoleUser,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberText{Value: bedrockPromptText(messages)},
				},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("call Bedrock profile: %w", err)
	}
	message, ok := output.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return "", errors.New("Bedrock response did not include a message")
	}
	for _, block := range message.Value.Content {
		text, ok := block.(*types.ContentBlockMemberText)
		if ok && strings.TrimSpace(text.Value) != "" {
			return strings.TrimSpace(text.Value), nil
		}
	}
	return "", errors.New("Bedrock response did not include assistant text")
}

func callBedrockConverseStream(client *http.Client, profile llmProfile, messages []openAICompatibleMessage, stream func(string) error) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg, err := bedrockAWSConfig(ctx, client, profile)
	if err != nil {
		return "", err
	}
	bedrock := bedrockruntime.NewFromConfig(cfg)
	output, err := bedrock.ConverseStream(ctx, &bedrockruntime.ConverseStreamInput{
		ModelId: aws.String(profile.ModelARN),
		Messages: []types.Message{
			{
				Role: types.ConversationRoleUser,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberText{Value: bedrockPromptText(messages)},
				},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("call Bedrock profile: %w", err)
	}
	eventStream := output.GetStream()
	defer eventStream.Close()

	var builder strings.Builder
	for event := range eventStream.Events() {
		delta, ok := event.(*types.ConverseStreamOutputMemberContentBlockDelta)
		if !ok {
			continue
		}
		text, ok := delta.Value.Delta.(*types.ContentBlockDeltaMemberText)
		if !ok || text.Value == "" {
			continue
		}
		builder.WriteString(text.Value)
		if stream != nil {
			if err := stream(text.Value); err != nil {
				return "", err
			}
		}
	}
	if err := eventStream.Err(); err != nil {
		return "", fmt.Errorf("read Bedrock stream response: %w", err)
	}
	if strings.TrimSpace(builder.String()) == "" {
		return "", errors.New("Bedrock stream response did not include assistant text")
	}
	return strings.TrimSpace(builder.String()), nil
}

func bedrockAWSConfig(ctx context.Context, client *http.Client, profile llmProfile) (aws.Config, error) {
	loadOptions := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(profile.Region),
	}
	if profile.AWSProfile != "" {
		loadOptions = append(loadOptions, awsconfig.WithSharedConfigProfile(profile.AWSProfile))
	}
	if client != nil {
		loadOptions = append(loadOptions, awsconfig.WithHTTPClient(client))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load Bedrock AWS config: %w", err)
	}
	if profile.BaseURL != "" {
		cfg.BaseEndpoint = aws.String(profile.BaseURL)
	}
	return cfg, nil
}

func bedrockPromptText(messages []openAICompatibleMessage) string {
	var parts []string
	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		role := strings.TrimSpace(message.Role)
		if role == "" {
			parts = append(parts, content)
			continue
		}
		parts = append(parts, strings.ToUpper(role[:1])+role[1:]+":\n"+content)
	}
	return strings.Join(parts, "\n\n")
}

func llmTodayContextAndPrompt(homeDir string) (string, string, error) {
	today, err := Today(TodayConfig{HomeDir: homeDir})
	if err != nil {
		return "", "", err
	}
	prompt := strings.Join([]string{
		"Summarize today's captured work context for a work journal.",
		"Group activity into a few coherent work threads rather than restating the timeline.",
		"Prefer outcomes, decisions, merged work, active projects, and open loops.",
		"Use the file change summaries as evidence, not as individual tasks.",
		"Ignore transient editor scratch files and repeated save churn unless the affected area is important.",
		"Use only the provided context.",
	}, "\n")
	return llmTodayContext(today, time.Now().Location()), prompt, nil
}

func llmTodayContext(today TodayResult, location *time.Location) string {
	lines := []string{
		"Today",
		fmt.Sprintf("%s: %s", today.Date, pluralize(len(today.Events), "event")),
	}
	if len(today.Events) == 0 {
		lines = append(lines, "No activity has been captured today.")
		return strings.Join(lines, "\n")
	}
	projects := todayProjectCounts(today.Events)
	if len(projects) > 0 {
		lines = append(lines, "", "Projects")
		for _, project := range projects {
			lines = append(lines, fmt.Sprintf("- %s: %s", project.Name, pluralize(project.Count, "event")))
		}
	}
	lines = append(lines, "", "Sessions")
	for _, session := range today.Sessions {
		lines = append(lines, llmTodaySessionLines(session, location)...)
	}
	return strings.Join(lines, "\n")
}

func llmTodaySessionLines(session TodaySession, location *time.Location) []string {
	lines := []string{fmt.Sprintf("- %s %s (%s)", sessionRange(session, location), projectLabel(session.Project), pluralize(len(session.Events), "event"))}
	fileEvents := make([]TodayEvent, 0)
	for _, event := range session.Events {
		if strings.HasPrefix(event.Type, "file.") {
			fileEvents = append(fileEvents, event)
			continue
		}
		lines = append(lines, fmt.Sprintf("  - %s %s %s", event.Timestamp.In(location).Format("15:04"), event.Type, eventLabel(event)))
		lines = append(lines, llmEventDetailLines(event)...)
	}
	if len(fileEvents) > 0 {
		lines = append(lines, llmFileChangeSummaryLines(fileEvents)...)
	}
	return lines
}

func llmEventDetailLines(event TodayEvent) []string {
	var payload struct {
		URL            string `json:"url"`
		Permalink      string `json:"permalink"`
		ContentPreview string `json:"content_preview"`
	}
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		return nil
	}
	var lines []string
	link := payload.URL
	if link == "" {
		link = payload.Permalink
	}
	if link != "" {
		lines = append(lines, "    - link: "+link)
	}
	if strings.TrimSpace(payload.ContentPreview) != "" {
		lines = append(lines, "    - content preview:")
		for _, line := range strings.Split(capLLMDetail(payload.ContentPreview, 1200), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				lines = append(lines, "      "+line)
			}
		}
	}
	return lines
}

func capLLMDetail(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit]) + "\n..."
}

func llmFileChangeSummaryLines(events []TodayEvent) []string {
	operations := map[string]int{}
	files := map[string]bool{}
	dirs := map[string]int{}
	for _, event := range events {
		operation := strings.TrimPrefix(event.Type, "file.")
		if operation == "" {
			operation = "changed"
		}
		operations[operation]++
		if event.Path != "" {
			files[event.Path] = true
			dirs[filepath.Dir(event.Path)]++
		}
	}
	lines := []string{fmt.Sprintf("  - file changes: %s across %s", pluralize(len(events), "event"), pluralize(len(files), "file"))}
	for _, operation := range sortedCountKeys(operations) {
		lines = append(lines, fmt.Sprintf("    - %s: %d", operation, operations[operation]))
	}
	for _, dir := range topCountKeys(dirs, 5) {
		lines = append(lines, fmt.Sprintf("    - touched %s (%s)", dir, pluralize(dirs[dir], "event")))
	}
	return lines
}

func sortedCountKeys(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func topCountKeys(counts map[string]int, limit int) []string {
	keys := sortedCountKeys(counts)
	sort.SliceStable(keys, func(i, j int) bool {
		if counts[keys[i]] == counts[keys[j]] {
			return keys[i] < keys[j]
		}
		return counts[keys[i]] > counts[keys[j]]
	})
	if len(keys) > limit {
		return keys[:limit]
	}
	return keys
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
