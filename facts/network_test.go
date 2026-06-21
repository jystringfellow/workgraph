package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNetworkDestinationsReportsConfiguredEndpointsWithoutSecrets(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	writeNetworkFixtureConfigs(t, homeDir)

	output, err := runworkgraph(t, repoRoot, "network", "destinations", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph network destinations failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"Network destinations",
		"slack api: https://slack.test/api",
		"calendar.google api: https://calendar.google.test",
		"calendar.google token: https://oauth.workgraph.test/calendar/google/token",
		"calendar.microsoft api: https://graph.microsoft.test",
		"mail.google api: https://gmail.test",
		"mail.microsoft token: https://login.microsoft.test/token",
		"notion api: https://notion.test",
		"azure.boards api: https://dev.azure.test",
		"llm.openai-compatible profile local: http://localhost:11434/v1",
		"llm.bedrock profile bedrock: bedrock://us-west-2/arn:aws:bedrock:us-west-2:123456789012:inference-profile/example",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected network destinations output to include %q, got:\n%s", expected, output)
		}
	}
	for _, forbidden := range []string{
		"slack-secret",
		"calendar-access",
		"calendar-refresh",
		"mail-access",
		"notion-secret",
		"azure-secret",
		"WORKGRAPH_LLM_KEY",
	} {
		if strings.Contains(string(output), forbidden) {
			t.Fatalf("network destinations output exposed secret %q:\n%s", forbidden, output)
		}
	}
}

func TestNetworkDestinationsJSONReportsConfiguredEndpointsWithoutSecrets(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	writeNetworkFixtureConfigs(t, homeDir)

	output, err := runworkgraph(t, repoRoot, "network", "destinations", "--home", homeDir, "--format", "json")
	if err != nil {
		t.Fatalf("workgraph network destinations --format json failed: %v\n%s", err, output)
	}
	var payload struct {
		Destinations []struct {
			ID          string `json:"id"`
			Connector   string `json:"connector"`
			Kind        string `json:"kind"`
			URL         string `json:"url"`
			Configured  bool   `json:"configured"`
			Description string `json:"description"`
		} `json:"destinations"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		t.Fatalf("expected valid network destinations JSON: %v\n%s", err, output)
	}
	byID := map[string]string{}
	for _, destination := range payload.Destinations {
		byID[destination.ID] = destination.URL
		if !destination.Configured {
			t.Fatalf("expected reported destinations to be configured, got %+v", destination)
		}
	}
	for id, expectedURL := range map[string]string{
		"slack.api":                   "https://slack.test/api",
		"calendar.google.token":       "https://oauth.workgraph.test/calendar/google/token",
		"mail.microsoft.api":          "https://graph.mail.test",
		"notion.token":                "https://oauth.workgraph.test/notion/token",
		"azure.boards.api":            "https://dev.azure.test",
		"llm.openai-compatible.local": "http://localhost:11434/v1",
		"llm.bedrock.bedrock":         "bedrock://us-west-2/arn:aws:bedrock:us-west-2:123456789012:inference-profile/example",
	} {
		if byID[id] != expectedURL {
			t.Fatalf("expected destination %s=%q, got %q in %#v", id, expectedURL, byID[id], byID)
		}
	}
	for _, forbidden := range []string{"slack-secret", "calendar-access", "mail-access", "notion-secret", "azure-secret", "WORKGRAPH_LLM_KEY"} {
		if strings.Contains(string(output), forbidden) {
			t.Fatalf("network destinations JSON exposed secret %q:\n%s", forbidden, output)
		}
	}
}

func writeNetworkFixtureConfigs(t *testing.T, homeDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(homeDir, "slack.json"), []byte(`{
  "access_token": "slack-secret",
  "api_base_url": "https://slack.test/api"
}`), 0o600); err != nil {
		t.Fatalf("write slack config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "calendar.json"), []byte(`{
  "google": {
    "access_token": "calendar-access",
    "refresh_token": "calendar-refresh",
    "api_base_url": "https://calendar.google.test",
    "token_url": "https://oauth.workgraph.test/calendar/google/token"
  },
  "microsoft": {
    "access_token": "microsoft-calendar-access",
    "api_base_url": "https://graph.microsoft.test",
    "token_url": "https://login.microsoft.test/token"
  }
}`), 0o600); err != nil {
		t.Fatalf("write calendar config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "mail.json"), []byte(`{
  "google": {
    "access_token": "mail-access",
    "api_base_url": "https://gmail.test",
    "token_url": "https://oauth.workgraph.test/mail/google/token"
  },
  "microsoft": {
    "access_token": "microsoft-mail-access",
    "api_base_url": "https://graph.mail.test",
    "token_url": "https://login.microsoft.test/token"
  }
}`), 0o600); err != nil {
		t.Fatalf("write mail config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "notion.json"), []byte(`{
  "access_token": "notion-secret",
  "api_base_url": "https://notion.test",
  "token_url": "https://oauth.workgraph.test/notion/token"
}`), 0o600); err != nil {
		t.Fatalf("write notion config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "azure-boards.json"), []byte(`{
  "access_token": "azure-secret",
  "api_base_url": "https://dev.azure.test",
  "token_url": "https://login.microsoft.test/token"
}`), 0o600); err != nil {
		t.Fatalf("write azure boards config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "llm.json"), []byte(`{
  "profiles": {
    "local": {
      "provider": "openai-compatible",
      "base_url": "http://localhost:11434/v1",
      "model": "local-model",
      "api_key_env": "WORKGRAPH_LLM_KEY"
    },
    "bedrock": {
      "provider": "bedrock",
      "region": "us-west-2",
      "model_arn": "arn:aws:bedrock:us-west-2:123456789012:inference-profile/example"
    }
  }
}`), 0o600); err != nil {
		t.Fatalf("write llm config: %v", err)
	}
}
