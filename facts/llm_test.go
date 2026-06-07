package facts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLLMAddListAndUseProfiles(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "llm", "add", "local-gemma",
		"--home", homeDir,
		"--provider", "openai-compatible",
		"--base-url", "http://localhost:11434/v1",
		"--model", "gemma-4-12b",
	)
	if err != nil {
		t.Fatalf("workgraph llm add failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "LLM profile added") || !strings.Contains(string(output), "local-gemma") {
		t.Fatalf("expected add confirmation, got:\n%s", output)
	}

	configPath := filepath.Join(homeDir, "llm.json")
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("expected llm config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected llm config permissions 0600, got %v", got)
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read llm config: %v", err)
	}
	var stored struct {
		DefaultProfile string `json:"default_profile"`
		Profiles       map[string]struct {
			Provider   string `json:"provider"`
			BaseURL    string `json:"base_url"`
			Model      string `json:"model"`
			APIKey     string `json:"api_key,omitempty"`
			AWSProfile string `json:"aws_profile"`
			Region     string `json:"region"`
			ModelARN   string `json:"model_arn"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(contents, &stored); err != nil {
		t.Fatalf("parse llm config: %v", err)
	}
	profile, ok := stored.Profiles["local-gemma"]
	if !ok {
		t.Fatalf("expected local-gemma profile, got %#v", stored.Profiles)
	}
	if profile.Provider != "openai-compatible" || profile.BaseURL != "http://localhost:11434/v1" || profile.Model != "gemma-4-12b" {
		t.Fatalf("expected stored local profile, got %#v", profile)
	}
	if profile.APIKey != "" {
		t.Fatalf("expected local profile not to store an API key, got %q", profile.APIKey)
	}

	output, err = runworkgraph(t, repoRoot, "llm", "add", "typo-profile",
		"--home", homeDir,
		"--provider", "openai-compaitble",
		"--base-url", "http://localhost:11434/v1",
		"--model", "gemma-4-12b",
	)
	if err == nil {
		t.Fatalf("expected invalid provider typo to fail, got output:\n%s", output)
	}
	if !strings.Contains(string(output), `unsupported llm provider "openai-compaitble"`) {
		t.Fatalf("expected unsupported provider error, got:\n%s", output)
	}

	output, err = runworkgraph(t, repoRoot, "llm", "add", "bedrock-work",
		"--home", homeDir,
		"--provider", "bedrock",
		"--aws-profile", "work",
		"--region", "us-east-1",
		"--model-arn", "arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.anthropic.claude-3-5-sonnet-20241022-v2:0",
	)
	if err != nil {
		t.Fatalf("workgraph llm add bedrock failed: %v\n%s", err, output)
	}
	contents, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read llm config after bedrock add: %v", err)
	}
	if err := json.Unmarshal(contents, &stored); err != nil {
		t.Fatalf("parse llm config after bedrock add: %v", err)
	}
	bedrockProfile, ok := stored.Profiles["bedrock-work"]
	if !ok {
		t.Fatalf("expected bedrock-work profile, got %#v", stored.Profiles)
	}
	if bedrockProfile.Provider != "bedrock" || bedrockProfile.AWSProfile != "work" || bedrockProfile.Region != "us-east-1" {
		t.Fatalf("expected stored Bedrock account metadata, got %#v", bedrockProfile)
	}
	if bedrockProfile.ModelARN != "arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.anthropic.claude-3-5-sonnet-20241022-v2:0" {
		t.Fatalf("expected stored Bedrock model ARN, got %#v", bedrockProfile)
	}

	output, err = runworkgraph(t, repoRoot, "llm", "list", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph llm list failed: %v\n%s", err, output)
	}
	for _, expected := range []string{"local-gemma", "openai-compatible", "gemma-4-12b"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected list output to include %q, got:\n%s", expected, output)
		}
	}

	output, err = runworkgraph(t, repoRoot, "llm", "use", "local-gemma", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph llm use failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Default LLM profile: local-gemma") {
		t.Fatalf("expected default profile message, got:\n%s", output)
	}

	output, err = runworkgraph(t, repoRoot, "llm", "use", "local-gemma", "--for", "summarize", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph llm use for task failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "LLM profile for summarize: local-gemma") {
		t.Fatalf("expected task profile message, got:\n%s", output)
	}

	output, err = runworkgraph(t, repoRoot, "llm", "use", "bedrock-work", "--for", "associate", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph llm use for association failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "LLM profile for associate: bedrock-work") {
		t.Fatalf("expected association profile message, got:\n%s", output)
	}

	output, err = runworkgraph(t, repoRoot, "llm", "remove", "bedrock-work", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph llm remove failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "LLM profile removed: bedrock-work") {
		t.Fatalf("expected remove confirmation, got:\n%s", output)
	}
	contents, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read llm config after remove: %v", err)
	}
	var routed struct {
		TaskProfiles map[string]string `json:"task_profiles"`
		Profiles     map[string]struct {
			Provider string `json:"provider"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(contents, &routed); err != nil {
		t.Fatalf("parse llm config after remove: %v", err)
	}
	if _, ok := routed.Profiles["bedrock-work"]; ok {
		t.Fatalf("expected removed profile to be absent, got %#v", routed.Profiles)
	}
	if routed.TaskProfiles["associate"] != "" {
		t.Fatalf("expected associate route removed with profile, got %#v", routed.TaskProfiles)
	}
}

func TestLLMTestUsesOpenAICompatibleProfile(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	var gotPath string
	var gotModel string
	var gotMessages []map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		gotPath = request.URL.Path
		var body struct {
			Model    string              `json:"model"`
			Messages []map[string]string `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode llm test request: %v", err)
		}
		gotModel = body.Model
		gotMessages = body.Messages
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "content": "llm test ok"
      }
    }
  ]
}`))
	}))
	defer server.Close()

	if output, err := runworkgraph(t, repoRoot, "llm", "add", "local-test",
		"--home", homeDir,
		"--provider", "openai-compatible",
		"--base-url", server.URL+"/v1",
		"--model", "test-model",
	); err != nil {
		t.Fatalf("workgraph llm add failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "llm", "test",
		"--home", homeDir,
		"--profile", "local-test",
	)
	if err != nil {
		t.Fatalf("workgraph llm test failed: %v\n%s", err, output)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("expected OpenAI-compatible chat completions path, got %q", gotPath)
	}
	if gotModel != "test-model" {
		t.Fatalf("expected configured model, got %q", gotModel)
	}
	if len(gotMessages) == 0 {
		t.Fatalf("expected minimal test messages, got %#v", gotMessages)
	}
	for _, message := range gotMessages {
		for _, forbidden := range []string{"Slack", "Notion", "GitHub", "calendar", "mail", "memory"} {
			if strings.Contains(message["content"], forbidden) {
				t.Fatalf("expected llm test not to send connector or memory content, got %#v", gotMessages)
			}
		}
	}
	for _, expected := range []string{"local-test", "openai-compatible", "test-model", server.URL + "/v1", "llm test ok"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected test output to include %q, got:\n%s", expected, output)
		}
	}
}

func TestLLMSummarizeTodayDryRunDoesNotCallProvider(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	called := false
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		called = true
		http.Error(response, "dry run should not call provider", http.StatusInternalServerError)
	}))
	defer server.Close()

	if output, err := runworkgraph(t, repoRoot, "llm", "add", "local-summary",
		"--home", homeDir,
		"--provider", "openai-compatible",
		"--base-url", server.URL+"/v1",
		"--model", "summary-model",
	); err != nil {
		t.Fatalf("workgraph llm add failed: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot, "llm", "use", "local-summary",
		"--home", homeDir,
		"--for", "summarize",
	); err != nil {
		t.Fatalf("workgraph llm use summarize failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "llm", "summarize", "today",
		"--home", homeDir,
		"--dry-run",
	)
	if err != nil {
		t.Fatalf("workgraph llm summarize today dry-run failed: %v\n%s", err, output)
	}
	if called {
		t.Fatalf("expected dry-run not to call provider")
	}
	for _, expected := range []string{
		"LLM summarize today dry run",
		"Profile: local-summary",
		"Provider: openai-compatible",
		"Model: summary-model",
		server.URL + "/v1",
		"Prompt:",
		"Context:",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected dry-run output to include %q, got:\n%s", expected, output)
		}
	}
	if _, err := os.Stat(filepath.Join(homeDir, "memory")); err == nil {
		t.Fatalf("expected dry-run not to create or write memory directory")
	}
}
