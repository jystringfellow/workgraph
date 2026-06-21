package facts

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
	_ "github.com/mattn/go-sqlite3"
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

	settingsPath := filepath.Join(homeDir, "llm.json")
	info, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("expected llm config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected llm config permissions 0600, got %v", got)
	}
	contents, err := os.ReadFile(settingsPath)
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
	contents, err = os.ReadFile(settingsPath)
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
	contents, err = os.ReadFile(settingsPath)
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

func TestLLMDoctorVerifiesOpenAICompatibleModelList(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		gotPath = request.URL.Path
		if request.URL.Path != "/v1/models" {
			http.Error(response, "unexpected path", http.StatusNotFound)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"data":[{"id":"approved-local-model"},{"id":"other-model"}]}`))
	}))
	defer server.Close()

	if output, err := runworkgraph(t, repoRoot, "llm", "add", "local-approved",
		"--home", homeDir,
		"--provider", "openai-compatible",
		"--base-url", server.URL+"/v1",
		"--model", "approved-local-model",
		"--api-key-env", "WORKGRAPH_LLM_KEY",
	); err != nil {
		t.Fatalf("workgraph llm add failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "llm", "doctor", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph llm doctor failed: %v\n%s", err, output)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("expected OpenAI-compatible models probe, got %q", gotPath)
	}
	for _, expected := range []string{
		"LLM doctor",
		"local-approved: openai-compatible approved-local-model",
		"destination: " + server.URL + "/v1",
		"managed policy: ok",
		"model probe: ok - model approved-local-model is advertised",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected llm doctor output to include %q, got:\n%s", expected, output)
		}
	}
	if strings.Contains(string(output), "WORKGRAPH_LLM_KEY") {
		t.Fatalf("expected llm doctor not to expose API key env names, got:\n%s", output)
	}
}

func TestManagedSettingsRequireOpenAICompatibleModelProbe(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	managedPath := filepath.Join(tempDir, "managed-settings.json")
	if err := os.WriteFile(managedPath, []byte(`{
  "version": 1,
  "llm": {
    "allowed_providers": {
      "value": ["openai-compatible"],
      "locked": true
    },
    "openai_compatible": {
      "allowed_models": {
        "value": ["approved-local-model"],
        "locked": true
      },
      "require_model_probe": {
        "value": true,
        "locked": true
      }
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write managed settings: %v", err)
	}
	restoreManagedSettings := workgraph.SetManagedSettingsPathForTest(managedPath)
	defer restoreManagedSettings()

	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("workgraph init failed: %v", err)
	}
	chatCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/models":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"data":[{"id":"different-model"}]}`))
		case "/v1/chat/completions":
			chatCalled = true
			http.Error(response, "chat should be blocked before provider call", http.StatusInternalServerError)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{
		HomeDir:  homeDir,
		Name:     "blocked-local",
		Provider: "openai-compatible",
		BaseURL:  server.URL + "/v1",
		Model:    "approved-local-model",
	}); err != nil {
		t.Fatalf("workgraph llm add failed: %v", err)
	}
	_, err := workgraph.TestLLMProfile(workgraph.LLMTestConfig{
		HomeDir:    homeDir,
		Profile:    "blocked-local",
		HTTPClient: server.Client(),
	})
	if err == nil {
		t.Fatalf("expected strict model probe to block unadvertised model")
	}
	if !strings.Contains(err.Error(), `OpenAI-compatible model "approved-local-model" is not advertised`) {
		t.Fatalf("expected strict model probe error, got: %v", err)
	}
	if chatCalled {
		t.Fatalf("expected strict model probe to block before chat completion call")
	}
}

func TestManagedSettingsDisableHostedLLMProviders(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	managedPath := filepath.Join(tempDir, "managed-settings.json")
	if err := os.WriteFile(managedPath, []byte(`{
  "version": 1,
  "llm": {
    "hosted_enabled": {
      "value": false,
      "locked": true
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write managed settings: %v", err)
	}
	restoreManagedSettings := workgraph.SetManagedSettingsPathForTest(managedPath)
	defer restoreManagedSettings()

	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("workgraph init failed: %v", err)
	}
	if _, err := workgraph.EnableHostedLLM(workgraph.LLMHostedConfig{HomeDir: homeDir}); err == nil {
		t.Fatalf("expected managed settings to block hosted LLM enable")
	} else if !strings.Contains(err.Error(), "hosted LLM providers are disabled by managed settings") {
		t.Fatalf("expected managed hosted disable error, got: %v", err)
	}
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{
		HomeDir:  homeDir,
		Name:     "bedrock-hosted",
		Provider: "bedrock",
		Region:   "us-east-1",
		ModelARN: "arn:aws:bedrock:us-east-1:123456789012:foundation-model/example",
	}); err != nil {
		t.Fatalf("workgraph llm add bedrock failed: %v", err)
	}

	_, err := workgraph.TestLLMProfile(workgraph.LLMTestConfig{
		HomeDir: homeDir,
		Profile: "bedrock-hosted",
	})
	if err == nil {
		t.Fatalf("expected managed settings to block hosted LLM test")
	}
	if !strings.Contains(err.Error(), "hosted LLM providers are disabled by managed settings") {
		t.Fatalf("expected managed settings error, got: %v", err)
	}
}

func TestHostedLLMRequiresExplicitUserOptIn(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")

	chatCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		chatCalled = true
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "output": {
    "message": {
      "role": "assistant",
      "content": [{"text": "hosted bedrock ok"}]
    }
  },
  "stopReason": "end_turn"
}`))
	}))
	defer server.Close()

	if output, err := runworkgraph(t, repoRoot, "llm", "add", "hosted-bedrock",
		"--home", homeDir,
		"--provider", "bedrock",
		"--region", "us-east-1",
		"--model-arn", "arn:aws:bedrock:us-east-1:123456789012:inference-profile/example",
		"--base-url", server.URL,
	); err != nil {
		t.Fatalf("workgraph llm add hosted bedrock failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "llm", "test",
		"--home", homeDir,
		"--profile", "hosted-bedrock",
	)
	if err == nil {
		t.Fatalf("expected hosted LLM test to require explicit opt-in, got:\n%s", output)
	}
	if !strings.Contains(string(output), "hosted LLM use is not enabled") || !strings.Contains(string(output), "workgraph llm hosted enable") {
		t.Fatalf("expected hosted opt-in error, got:\n%s", output)
	}
	if chatCalled {
		t.Fatalf("expected hosted opt-in to block before provider call")
	}

	output, err = runworkgraph(t, repoRoot, "llm", "hosted", "status", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph llm hosted status failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Hosted LLM use: disabled") {
		t.Fatalf("expected disabled hosted status, got:\n%s", output)
	}

	output, err = runworkgraph(t, repoRoot, "llm", "hosted", "enable", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph llm hosted enable failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Hosted LLM use enabled") || !strings.Contains(string(output), "captured work context") {
		t.Fatalf("expected hosted enable warning, got:\n%s", output)
	}

	output, err = runworkgraph(t, repoRoot, "llm", "test",
		"--home", homeDir,
		"--profile", "hosted-bedrock",
	)
	if err != nil {
		t.Fatalf("expected explicit hosted opt-in to allow approved Bedrock call: %v\n%s", err, output)
	}
	if !chatCalled {
		t.Fatalf("expected provider call after hosted opt-in")
	}
	if !strings.Contains(string(output), "hosted bedrock ok") {
		t.Fatalf("expected hosted provider response, got:\n%s", output)
	}

	contents, err := os.ReadFile(filepath.Join(homeDir, "llm.json"))
	if err != nil {
		t.Fatalf("read llm config: %v", err)
	}
	var stored struct {
		HostedLLM struct {
			Enabled        bool   `json:"enabled"`
			AcknowledgedAt string `json:"acknowledged_at"`
		} `json:"hosted_llm"`
	}
	if err := json.Unmarshal(contents, &stored); err != nil {
		t.Fatalf("parse llm config: %v", err)
	}
	if !stored.HostedLLM.Enabled || stored.HostedLLM.AcknowledgedAt == "" {
		t.Fatalf("expected hosted opt-in state to be stored, got %+v", stored.HostedLLM)
	}
}

func TestLocalLLMDoesNotRequireHostedOptIn(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	called := false
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		called = true
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "content": "local ok"
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
		"--model", "local-model",
	); err != nil {
		t.Fatalf("workgraph llm add local failed: %v\n%s", err, output)
	}
	output, err := runworkgraph(t, repoRoot, "llm", "test",
		"--home", homeDir,
		"--profile", "local-test",
	)
	if err != nil {
		t.Fatalf("expected local OpenAI-compatible profile not to require hosted opt-in: %v\n%s", err, output)
	}
	if !called {
		t.Fatalf("expected local provider call")
	}
	if !strings.Contains(string(output), "local ok") {
		t.Fatalf("expected local response, got:\n%s", output)
	}
}

func TestManagedSettingsAllowOnlyApprovedOpenAICompatibleModels(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	managedPath := filepath.Join(tempDir, "managed-settings.json")
	if err := os.WriteFile(managedPath, []byte(`{
  "version": 1,
  "llm": {
    "allowed_providers": {
      "value": ["openai-compatible"],
      "locked": true
    },
    "allowed_base_urls": {
      "value": ["http://localhost:11434/v1"],
      "locked": true
    },
    "openai_compatible": {
      "allowed_models": {
        "value": ["llama3.1:8b-instruct-q4_K_M"],
        "locked": true
      }
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write managed settings: %v", err)
	}
	restoreManagedSettings := workgraph.SetManagedSettingsPathForTest(managedPath)
	defer restoreManagedSettings()

	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("workgraph init failed: %v", err)
	}
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{
		HomeDir:  homeDir,
		Name:     "blocked-local",
		Provider: "openai-compatible",
		BaseURL:  "http://localhost:11434/v1",
		Model:    "unapproved-model",
	}); err != nil {
		t.Fatalf("workgraph llm add blocked local failed: %v", err)
	}
	_, err := workgraph.TestLLMProfile(workgraph.LLMTestConfig{
		HomeDir: homeDir,
		Profile: "blocked-local",
	})
	if err == nil {
		t.Fatalf("expected managed settings to block unapproved OpenAI-compatible model")
	}
	if !strings.Contains(err.Error(), `OpenAI-compatible model "unapproved-model" is not allowed by managed settings`) {
		t.Fatalf("expected model allowlist error, got: %v", err)
	}

	var gotModel string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode OpenAI-compatible request: %v", err)
		}
		gotModel = body.Model
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "content": "approved local model ok"
      }
    }
  ]
}`))
	}))
	defer server.Close()
	allowedBaseURL := server.URL + "/v1"
	if err := os.WriteFile(managedPath, []byte(`{
  "version": 1,
  "llm": {
    "allowed_providers": {
      "value": ["openai-compatible"],
      "locked": true
    },
    "allowed_base_urls": {
      "value": ["`+allowedBaseURL+`"],
      "locked": true
    },
    "openai_compatible": {
      "allowed_models": {
        "value": ["llama3.1:8b-instruct-q4_K_M"],
        "locked": true
      }
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("rewrite managed settings: %v", err)
	}
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{
		HomeDir:  homeDir,
		Name:     "approved-local",
		Provider: "openai-compatible",
		BaseURL:  allowedBaseURL,
		Model:    "llama3.1:8b-instruct-q4_K_M",
	}); err != nil {
		t.Fatalf("workgraph llm add approved local failed: %v", err)
	}
	result, err := workgraph.TestLLMProfile(workgraph.LLMTestConfig{
		HomeDir: homeDir,
		Profile: "approved-local",
	})
	if err != nil {
		t.Fatalf("expected approved OpenAI-compatible model to pass managed settings: %v", err)
	}
	if gotModel != "llama3.1:8b-instruct-q4_K_M" {
		t.Fatalf("expected approved model in request, got %q", gotModel)
	}
	if !strings.Contains(result.Message, "approved local model ok") {
		t.Fatalf("expected approved local model response, got:\n%s", result.Message)
	}
}

func TestManagedSettingsAllowOnlyApprovedBedrockInferenceProfiles(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	managedPath := filepath.Join(tempDir, "managed-settings.json")
	allowedARN := "arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.anthropic.claude-3-5-sonnet-20241022-v2:0"
	blockedARN := "arn:aws:bedrock:us-east-1:123456789012:foundation-model/anthropic.claude-3-5-sonnet-20241022-v2:0"
	if err := os.WriteFile(managedPath, []byte(`{
  "version": 1,
  "llm": {
    "allowed_providers": {
      "value": ["bedrock"],
      "locked": true
    },
    "bedrock": {
      "allowed_model_arns": {
        "value": ["`+allowedARN+`"],
        "locked": true
      }
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write managed settings: %v", err)
	}
	restoreManagedSettings := workgraph.SetManagedSettingsPathForTest(managedPath)
	defer restoreManagedSettings()

	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("workgraph init failed: %v", err)
	}
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{
		HomeDir:  homeDir,
		Name:     "local",
		Provider: "openai-compatible",
		BaseURL:  "http://localhost:11434/v1",
		Model:    "local-model",
	}); err != nil {
		t.Fatalf("workgraph llm add local failed: %v", err)
	}
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{
		HomeDir:  homeDir,
		Name:     "blocked-bedrock",
		Provider: "bedrock",
		Region:   "us-east-1",
		ModelARN: blockedARN,
	}); err != nil {
		t.Fatalf("workgraph llm add blocked bedrock failed: %v", err)
	}

	_, err := workgraph.TestLLMProfile(workgraph.LLMTestConfig{
		HomeDir: homeDir,
		Profile: "local",
	})
	if err == nil {
		t.Fatalf("expected managed settings to block non-Bedrock provider")
	}
	if !strings.Contains(err.Error(), `llm provider "openai-compatible" is not allowed by managed settings`) {
		t.Fatalf("expected provider allowlist error, got: %v", err)
	}

	_, err = workgraph.TestLLMProfile(workgraph.LLMTestConfig{
		HomeDir: homeDir,
		Profile: "blocked-bedrock",
	})
	if err == nil {
		t.Fatalf("expected managed settings to block unapproved Bedrock model ARN")
	}
	if !strings.Contains(err.Error(), "Bedrock model ARN is not allowed by managed settings") {
		t.Fatalf("expected Bedrock allowlist error, got: %v", err)
	}

	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_SESSION_TOKEN", "test-session-token")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "output": {
    "message": {
      "role": "assistant",
      "content": [{"text": "approved bedrock ok"}]
    }
  },
  "stopReason": "end_turn"
}`))
	}))
	defer server.Close()

	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{
		HomeDir:  homeDir,
		Name:     "approved-bedrock",
		Provider: "bedrock",
		Region:   "us-east-1",
		ModelARN: allowedARN,
		BaseURL:  server.URL,
	}); err != nil {
		t.Fatalf("workgraph llm add approved bedrock failed: %v", err)
	}
	if _, err := workgraph.EnableHostedLLM(workgraph.LLMHostedConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("workgraph llm hosted enable failed: %v", err)
	}
	result, err := workgraph.TestLLMProfile(workgraph.LLMTestConfig{
		HomeDir: homeDir,
		Profile: "approved-bedrock",
	})
	if err != nil {
		t.Fatalf("expected approved Bedrock inference profile to pass managed settings: %v", err)
	}
	if !strings.Contains(result.Message, "approved bedrock ok") {
		t.Fatalf("expected approved Bedrock response, got:\n%s", result.Message)
	}
}

func TestManagedSettingsAllowBedrockInferenceProfileScope(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	managedPath := filepath.Join(tempDir, "managed-settings.json")
	allowedARN := "arn:aws:bedrock:us-west-2:123456789012:inference-profile/us.anthropic.claude-3-5-sonnet-20241022-v2:0"
	wrongRegionARN := "arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.anthropic.claude-3-5-sonnet-20241022-v2:0"
	wrongAccountARN := "arn:aws:bedrock:us-west-2:999999999999:inference-profile/us.anthropic.claude-3-5-sonnet-20241022-v2:0"
	foundationModelARN := "arn:aws:bedrock:us-west-2:123456789012:foundation-model/anthropic.claude-3-5-sonnet-20241022-v2:0"
	if err := os.WriteFile(managedPath, []byte(`{
  "version": 1,
  "llm": {
    "allowed_providers": {
      "value": ["bedrock"],
      "locked": true
    },
    "bedrock": {
      "allowed_inference_profile_scopes": {
        "value": [
          {
            "account_id": "123456789012",
            "region": "us-west-2"
          }
        ],
        "locked": true
      }
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write managed settings: %v", err)
	}
	restoreManagedSettings := workgraph.SetManagedSettingsPathForTest(managedPath)
	defer restoreManagedSettings()

	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("workgraph init failed: %v", err)
	}
	for name, modelARN := range map[string]string{
		"wrong-region":     wrongRegionARN,
		"wrong-account":    wrongAccountARN,
		"foundation-model": foundationModelARN,
	} {
		if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{
			HomeDir:  homeDir,
			Name:     name,
			Provider: "bedrock",
			Region:   "us-west-2",
			ModelARN: modelARN,
		}); err != nil {
			t.Fatalf("workgraph llm add %s failed: %v", name, err)
		}
		_, err := workgraph.TestLLMProfile(workgraph.LLMTestConfig{
			HomeDir: homeDir,
			Profile: name,
		})
		if err == nil {
			t.Fatalf("expected managed settings to block %s", name)
		}
		if !strings.Contains(err.Error(), "Bedrock model ARN is not allowed by managed settings") {
			t.Fatalf("expected Bedrock scope allowlist error for %s, got: %v", name, err)
		}
	}

	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_SESSION_TOKEN", "test-session-token")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "output": {
    "message": {
      "role": "assistant",
      "content": [{"text": "scoped bedrock ok"}]
    }
  },
  "stopReason": "end_turn"
}`))
	}))
	defer server.Close()

	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{
		HomeDir:  homeDir,
		Name:     "scoped-bedrock",
		Provider: "bedrock",
		Region:   "us-west-2",
		ModelARN: allowedARN,
		BaseURL:  server.URL,
	}); err != nil {
		t.Fatalf("workgraph llm add scoped bedrock failed: %v", err)
	}
	if _, err := workgraph.EnableHostedLLM(workgraph.LLMHostedConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("workgraph llm hosted enable failed: %v", err)
	}
	result, err := workgraph.TestLLMProfile(workgraph.LLMTestConfig{
		HomeDir: homeDir,
		Profile: "scoped-bedrock",
	})
	if err != nil {
		t.Fatalf("expected scoped Bedrock inference profile to pass managed settings: %v", err)
	}
	if !strings.Contains(result.Message, "scoped bedrock ok") {
		t.Fatalf("expected scoped Bedrock response, got:\n%s", result.Message)
	}
}

func TestLLMTestUsesBedrockInferenceProfileARN(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_SESSION_TOKEN", "test-session-token")

	modelARN := "arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.anthropic.claude-3-5-sonnet-20241022-v2:0"
	var gotPath string
	var gotAuthorization string
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		gotPath = request.URL.EscapedPath()
		gotAuthorization = request.Header.Get("Authorization")
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read bedrock request: %v", err)
		}
		gotBody = string(body)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "output": {
    "message": {
      "role": "assistant",
      "content": [{"text": "bedrock test ok"}]
    }
  },
  "stopReason": "end_turn"
}`))
	}))
	defer server.Close()

	if output, err := runworkgraph(t, repoRoot, "llm", "add", "bedrock-test",
		"--home", homeDir,
		"--provider", "bedrock",
		"--region", "us-east-1",
		"--model-arn", modelARN,
		"--base-url", server.URL,
	); err != nil {
		t.Fatalf("workgraph llm add bedrock failed: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot, "llm", "hosted", "enable", "--home", homeDir); err != nil {
		t.Fatalf("workgraph llm hosted enable failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "llm", "test",
		"--home", homeDir,
		"--profile", "bedrock-test",
	)
	if err != nil {
		t.Fatalf("workgraph llm test bedrock failed: %v\n%s", err, output)
	}
	if !strings.Contains(gotPath, "/model/") || !strings.HasSuffix(gotPath, "/converse") {
		t.Fatalf("expected Bedrock Converse model path, got %q", gotPath)
	}
	if !strings.Contains(gotPath, "inference-profile") {
		t.Fatalf("expected inference profile ARN in escaped path, got %q", gotPath)
	}
	if !strings.Contains(gotAuthorization, "AWS4-HMAC-SHA256") {
		t.Fatalf("expected signed Bedrock request, got authorization %q", gotAuthorization)
	}
	if !strings.Contains(gotBody, "Reply with a short confirmation") {
		t.Fatalf("expected llm test prompt in Bedrock request, got %s", gotBody)
	}
	for _, expected := range []string{
		"LLM test complete",
		"Profile: bedrock-test",
		"Provider: bedrock",
		modelARN,
		"bedrock://us-east-1/" + modelARN,
		"bedrock test ok",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected bedrock test output to include %q, got:\n%s", expected, output)
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

func TestLLMSummarizeTodayDryRunCollapsesFileChurn(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
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

	dbPath := filepath.Join(homeDir, "workgraph.db")
	start := time.Now().UTC().Add(-5 * time.Minute)
	insertLLMEvent(t, dbPath, "file-1", "file.modified", start.Format(time.RFC3339Nano), "workgraph", "", `{"path":"/repo/workgraph/run.go","operation":"modified"}`)
	insertLLMEvent(t, dbPath, "file-2", "file.modified", start.Add(time.Minute).Format(time.RFC3339Nano), "workgraph", "", `{"path":"/repo/workgraph/run.go","operation":"modified"}`)
	insertLLMEvent(t, dbPath, "file-3", "file.modified", start.Add(2*time.Minute).Format(time.RFC3339Nano), "workgraph", "", `{"path":"/repo/workgraph/facts/llm_test.go","operation":"modified"}`)
	insertLLMEvent(t, dbPath, "file-4", "file.created", start.Add(3*time.Minute).Format(time.RFC3339Nano), "workgraph", "", `{"path":"/repo/workgraph/connector.go","operation":"created"}`)
	insertLLMEvent(t, dbPath, "commit-1", "git.commit", start.Add(4*time.Minute).Format(time.RFC3339Nano), "workgraph", "feat: improve connector runtime", `{"commit":"abcdef123456","branch":"main","subject":"feat: improve connector runtime"}`)

	output, err := runworkgraph(t, repoRoot, "llm", "summarize", "today",
		"--home", homeDir,
		"--dry-run",
	)
	if err != nil {
		t.Fatalf("workgraph llm summarize today dry-run failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Use the file change summaries as evidence, not as individual tasks.") {
		t.Fatalf("expected improved prompt guidance, got:\n%s", output)
	}
	if !strings.Contains(string(output), "file changes: 4 events across 3 files") {
		t.Fatalf("expected file churn summary, got:\n%s", output)
	}
	if !strings.Contains(string(output), "modified: 3") || !strings.Contains(string(output), "created: 1") {
		t.Fatalf("expected file operation counts, got:\n%s", output)
	}
	if strings.Contains(string(output), "file.modified /repo/workgraph/run.go") {
		t.Fatalf("expected dry-run context to collapse repeated file events, got:\n%s", output)
	}
	if !strings.Contains(string(output), "git.commit feat: improve connector runtime") {
		t.Fatalf("expected non-file work evidence to remain, got:\n%s", output)
	}
}

func TestLLMSummarizeTodayDryRunIncludesNotionContentPreview(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
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

	now := time.Now().UTC().Format(time.RFC3339Nano)
	insertLLMEvent(t, filepath.Join(homeDir, "workgraph.db"), "notion-preview-1", "notion.page_updated", now, "workgraph", "Updated launch plan", `{"title":"Updated launch plan","content_preview":"## Launch checklist\nAdded beta rollout notes.\n- [ ] Confirm Slack Lists scope."}`)

	output, err := runworkgraph(t, repoRoot, "llm", "summarize", "today",
		"--home", homeDir,
		"--dry-run",
	)
	if err != nil {
		t.Fatalf("workgraph llm summarize today dry-run failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"notion.page_updated Updated launch plan",
		"content preview:",
		"## Launch checklist",
		"Added beta rollout notes.",
		"- [ ] Confirm Slack Lists scope.",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected summary context to include %q, got:\n%s", expected, output)
		}
	}
}

func TestLLMSummarizeTodayCallsOpenAICompatibleProfile(t *testing.T) {
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
			Stream   bool                `json:"stream"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode summarize request: %v", err)
		}
		gotModel = body.Model
		gotMessages = body.Messages
		if !body.Stream {
			t.Fatalf("expected streaming chat completion request, got %#v", body)
		}
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = response.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"You advanced workgraph \"}}]}\n\n"))
		_, _ = response.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"connector runtime.\"}}]}\n\n"))
		_, _ = response.Write([]byte("data: [DONE]\n\n"))
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
	insertLLMEvent(t, filepath.Join(homeDir, "workgraph.db"), "commit-1", "git.commit", time.Now().UTC().Format(time.RFC3339Nano), "workgraph", "feat: improve connector runtime", `{"commit":"abcdef123456","branch":"main","subject":"feat: improve connector runtime"}`)

	output, err := runworkgraph(t, repoRoot, "llm", "summarize", "today",
		"--home", homeDir,
	)
	if err != nil {
		t.Fatalf("workgraph llm summarize today failed: %v\n%s", err, output)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("expected OpenAI-compatible chat completions path, got %q", gotPath)
	}
	if gotModel != "summary-model" {
		t.Fatalf("expected configured model, got %q", gotModel)
	}
	if len(gotMessages) != 2 {
		t.Fatalf("expected system and user messages, got %#v", gotMessages)
	}
	if gotMessages[0]["role"] != "system" || !strings.Contains(gotMessages[0]["content"], "workgraph daily work summaries") {
		t.Fatalf("expected summarize system prompt, got %#v", gotMessages)
	}
	if gotMessages[1]["role"] != "user" ||
		!strings.Contains(gotMessages[1]["content"], "Prompt:") ||
		!strings.Contains(gotMessages[1]["content"], "Context:") ||
		!strings.Contains(gotMessages[1]["content"], "git.commit feat: improve connector runtime") {
		t.Fatalf("expected prompt and context in user message, got %#v", gotMessages)
	}
	for _, expected := range []string{
		"LLM summarize today streaming",
		"Profile: local-summary",
		"Provider: openai-compatible",
		"Model: summary-model",
		"Thinking...",
		"Summary:",
		"You advanced workgraph connector runtime.",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected summary output to include %q, got:\n%s", expected, output)
		}
	}
}

func TestLLMSummarizeTodayCallsBedrockProfile(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")

	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read bedrock summarize request: %v", err)
		}
		gotBody = string(body)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "output": {
    "message": {
      "role": "assistant",
      "content": [{"text": "Bedrock summarized the workgraph day."}]
    }
  },
  "stopReason": "end_turn"
}`))
	}))
	defer server.Close()

	modelARN := "arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.anthropic.claude-3-5-sonnet-20241022-v2:0"
	if output, err := runworkgraph(t, repoRoot, "llm", "add", "bedrock-summary",
		"--home", homeDir,
		"--provider", "bedrock",
		"--region", "us-east-1",
		"--model-arn", modelARN,
		"--base-url", server.URL,
	); err != nil {
		t.Fatalf("workgraph llm add bedrock failed: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot, "llm", "use", "bedrock-summary",
		"--home", homeDir,
		"--for", "summarize",
	); err != nil {
		t.Fatalf("workgraph llm use bedrock summarize failed: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot, "llm", "hosted", "enable", "--home", homeDir); err != nil {
		t.Fatalf("workgraph llm hosted enable failed: %v\n%s", err, output)
	}
	insertLLMEvent(t, filepath.Join(homeDir, "workgraph.db"), "notion-1", "notion.page_updated", time.Now().UTC().Format(time.RFC3339Nano), "workgraph", "Updated launch plan", `{"title":"Updated launch plan","content_preview":"## Launch checklist\nAdded beta rollout notes."}`)

	output, err := runworkgraph(t, repoRoot, "llm", "summarize", "today",
		"--home", homeDir,
		"--no-stream",
	)
	if err != nil {
		t.Fatalf("workgraph llm summarize today bedrock failed: %v\n%s", err, output)
	}
	if !strings.Contains(gotBody, "Summarize today's captured work context") ||
		!strings.Contains(gotBody, "notion.page_updated Updated launch plan") {
		t.Fatalf("expected Bedrock summarize request to include prompt and context, got %s", gotBody)
	}
	for _, expected := range []string{
		"LLM summarize today complete",
		"Profile: bedrock-summary",
		"Provider: bedrock",
		"Model: " + modelARN,
		"Summary:",
		"Bedrock summarized the workgraph day.",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected Bedrock summary output to include %q, got:\n%s", expected, output)
		}
	}
}

func TestHostedLLMSummarizeFiltersSecretsBeforeProviderCall(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	managedPath := filepath.Join(tempDir, "managed-settings.json")
	if err := os.WriteFile(managedPath, []byte(`{
  "version": 1,
  "llm": {
    "outbound_filter": {
      "sensitive_patterns": {
        "value": ["PROJECT-[0-9]{4}-SECRET"],
        "locked": true
      }
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write managed settings: %v", err)
	}
	restoreManagedSettings := workgraph.SetManagedSettingsPathForTest(managedPath)
	defer restoreManagedSettings()

	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")

	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read bedrock request: %v", err)
		}
		gotBody = string(body)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "output": {
    "message": {
      "role": "assistant",
      "content": [{"text": "filtered summary ok"}]
    }
  },
  "stopReason": "end_turn"
}`))
	}))
	defer server.Close()

	modelARN := "arn:aws:bedrock:us-east-1:123456789012:inference-profile/example"
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{
		HomeDir:  homeDir,
		Name:     "hosted-summary",
		Provider: "bedrock",
		Region:   "us-east-1",
		ModelARN: modelARN,
		BaseURL:  server.URL,
	}); err != nil {
		t.Fatalf("workgraph llm add bedrock failed: %v", err)
	}
	if _, err := workgraph.UseLLMProfile(workgraph.LLMUseProfileConfig{
		HomeDir: homeDir,
		Name:    "hosted-summary",
		Task:    "summarize",
	}); err != nil {
		t.Fatalf("workgraph llm use hosted summarize failed: %v", err)
	}
	if _, err := workgraph.EnableHostedLLM(workgraph.LLMHostedConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("workgraph llm hosted enable failed: %v", err)
	}
	insertLLMEvent(t, filepath.Join(homeDir, "workgraph.db"), "secret-1", "notion.page_updated", time.Now().UTC().Format(time.RFC3339Nano), "workgraph", "Updated incident notes", `{"title":"Updated incident notes","content_preview":"Token ghp_abcdefghijklmnopqrstuvwxyz1234567890ABCD and AWS key AKIAIOSFODNN7EXAMPLE plus PROJECT-1234-SECRET and Notion secret_a1b2c3d4e5f6g7h8i9j0 plus ntn_abcdefghijklmnopqrstuvwxyz123456"}`)

	result, err := workgraph.SummarizeTodayWithLLM(workgraph.LLMSummarizeTodayConfig{
		HomeDir:    homeDir,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("workgraph llm summarize today failed: %v", err)
	}
	for _, forbidden := range []string{
		"ghp_abcdefghijklmnopqrstuvwxyz1234567890ABCD",
		"AKIAIOSFODNN7EXAMPLE",
		"PROJECT-1234-SECRET",
		"secret_a1b2c3d4e5f6g7h8i9j0",
		"ntn_abcdefghijklmnopqrstuvwxyz123456",
	} {
		if strings.Contains(gotBody, forbidden) {
			t.Fatalf("expected hosted request body to redact %q, got %s", forbidden, gotBody)
		}
	}
	for _, expected := range []string{
		"[REDACTED:github-token]",
		"[REDACTED:aws-access-key]",
		"[REDACTED:managed-pattern]",
		"[REDACTED:notion-token]",
	} {
		if !strings.Contains(gotBody, expected) {
			t.Fatalf("expected hosted request body to include %q, got %s", expected, gotBody)
		}
	}
	if !strings.Contains(result.Message, "Outbound filter: 5 redactions applied") {
		t.Fatalf("expected outbound filter redaction count in output, got:\n%s", result.Message)
	}
}

func insertLLMEvent(t *testing.T, dbPath, id, eventType, timestamp, project, summary, payload string) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, summary, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		strings.Split(eventType, ".")[0],
		eventType,
		timestamp,
		payload,
		emptyNull(project),
		emptyNull(summary),
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("insert event %s: %v", id, err)
	}
}

func emptyNull(value string) any {
	if value == "" {
		return nil
	}
	return value
}
