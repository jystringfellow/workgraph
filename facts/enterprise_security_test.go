package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlackCompliancePageIsITReadable(t *testing.T) {
	path := filepath.Join(repoRoot(t), "public", "slack-compliance.html")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Slack compliance page: %v", err)
	}
	page := string(contents)
	for _, expected := range []string{
		"Slack and Enterprise Compliance",
		"local-first",
		"channels:history",
		"groups:history",
		"im:history",
		"mpim:history",
		"does not request Slack write scopes",
		"~/.workgraph/workgraph.db",
		"~/.workgraph/slack.json",
		"managed-settings.json",
		"connectors.slack.include_dms",
		"hosted LLM providers",
		"Network destinations",
		"Cloudflare Workers",
		"does not provide a trust center",
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("expected Slack compliance page to include %q", expected)
		}
	}
	if strings.Contains(page, "guaranteed compliant") {
		t.Fatalf("expected compliance page not to overstate compliance")
	}
}

func TestManagedSettingsDeploymentGuideAndPolicyExample(t *testing.T) {
	root := repoRoot(t)
	guidePath := filepath.Join(root, "docs", "security", "managed-settings.md")
	guideContents, err := os.ReadFile(guidePath)
	if err != nil {
		t.Fatalf("read managed settings guide: %v", err)
	}
	guide := string(guideContents)
	for _, expected := range []string{
		"# Managed Settings Deployment",
		"/Library/Application Support/workgraph/managed-settings.json",
		"%ProgramData%\\workgraph\\managed-settings.json",
		"/etc/workgraph/managed-settings.json",
		"enterprise-managed-settings.recommended.json",
		"bedrock-inference-profiles.managed-settings.example.json",
		"workgraph settings get --format json",
		"hosted LLM providers",
		"Bedrock inference profiles",
		"llm.allowed_providers",
		"llm.outbound_filter.sensitive_patterns",
		"llm.openai_compatible.allowed_models",
		"llm.openai_compatible.require_model_probe",
		"llm.bedrock.allowed_model_arns",
		"llm.bedrock.allowed_inference_profile_scopes",
		"Slack DM capture",
		"endpoint management",
		"does not prevent a user from running unrelated software",
	} {
		if !strings.Contains(guide, expected) {
			t.Fatalf("expected managed settings guide to include %q", expected)
		}
	}

	policyPath := filepath.Join(root, "docs", "security", "enterprise-managed-settings.recommended.json")
	policyContents, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatalf("read recommended managed settings policy: %v", err)
	}
	if strings.Contains(string(policyContents), "token") || strings.Contains(string(policyContents), "secret") {
		t.Fatalf("expected recommended managed settings policy not to contain token or secret fields")
	}

	var policy struct {
		Version int `json:"version"`
		LLM     struct {
			HostedEnabled struct {
				Value  bool `json:"value"`
				Locked bool `json:"locked"`
			} `json:"hosted_enabled"`
			AllowedBaseURLs struct {
				Value  []string `json:"value"`
				Locked bool     `json:"locked"`
			} `json:"allowed_base_urls"`
			AllowedProviders struct {
				Value  []string `json:"value"`
				Locked bool     `json:"locked"`
			} `json:"allowed_providers"`
			OutboundFilter struct {
				SensitivePatterns struct {
					Value  []string `json:"value"`
					Locked bool     `json:"locked"`
				} `json:"sensitive_patterns"`
			} `json:"outbound_filter"`
			OpenAICompatible struct {
				AllowedModels struct {
					Value  []string `json:"value"`
					Locked bool     `json:"locked"`
				} `json:"allowed_models"`
				RequireModelProbe struct {
					Value  bool `json:"value"`
					Locked bool `json:"locked"`
				} `json:"require_model_probe"`
			} `json:"openai_compatible"`
		} `json:"llm"`
		Connectors struct {
			Slack struct {
				IncludeDMs struct {
					Value  bool `json:"value"`
					Locked bool `json:"locked"`
				} `json:"include_dms"`
			} `json:"slack"`
		} `json:"connectors"`
	}
	if err := json.Unmarshal(policyContents, &policy); err != nil {
		t.Fatalf("recommended managed settings policy must be valid JSON: %v", err)
	}
	if policy.Version != 1 {
		t.Fatalf("expected managed settings policy version 1, got %d", policy.Version)
	}
	if policy.LLM.HostedEnabled.Value || !policy.LLM.HostedEnabled.Locked {
		t.Fatalf("expected recommended policy to lock hosted LLM providers off")
	}
	if len(policy.LLM.AllowedBaseURLs.Value) != 1 || policy.LLM.AllowedBaseURLs.Value[0] != "http://localhost:11434/v1" || !policy.LLM.AllowedBaseURLs.Locked {
		t.Fatalf("expected recommended policy to lock LLM base URLs to local endpoint, got %+v", policy.LLM.AllowedBaseURLs)
	}
	if len(policy.LLM.AllowedProviders.Value) != 1 || policy.LLM.AllowedProviders.Value[0] != "openai-compatible" || !policy.LLM.AllowedProviders.Locked {
		t.Fatalf("expected recommended policy to lock allowed providers to openai-compatible, got %+v", policy.LLM.AllowedProviders)
	}
	if len(policy.LLM.OutboundFilter.SensitivePatterns.Value) != 1 || policy.LLM.OutboundFilter.SensitivePatterns.Value[0] != "PROJECT-[0-9]{4}-SECRET" || !policy.LLM.OutboundFilter.SensitivePatterns.Locked {
		t.Fatalf("expected recommended policy to lock outbound LLM sensitive patterns, got %+v", policy.LLM.OutboundFilter.SensitivePatterns)
	}
	if len(policy.LLM.OpenAICompatible.AllowedModels.Value) != 1 || policy.LLM.OpenAICompatible.AllowedModels.Value[0] != "llama3.1:8b-instruct-q4_K_M" || !policy.LLM.OpenAICompatible.AllowedModels.Locked {
		t.Fatalf("expected recommended policy to lock OpenAI-compatible allowed models, got %+v", policy.LLM.OpenAICompatible.AllowedModels)
	}
	if !policy.LLM.OpenAICompatible.RequireModelProbe.Value || !policy.LLM.OpenAICompatible.RequireModelProbe.Locked {
		t.Fatalf("expected recommended policy to require OpenAI-compatible model probing, got %+v", policy.LLM.OpenAICompatible.RequireModelProbe)
	}
	if policy.Connectors.Slack.IncludeDMs.Value || !policy.Connectors.Slack.IncludeDMs.Locked {
		t.Fatalf("expected recommended policy to lock Slack DM capture off")
	}

	bedrockPolicyPath := filepath.Join(root, "docs", "security", "bedrock-inference-profiles.managed-settings.example.json")
	bedrockPolicyContents, err := os.ReadFile(bedrockPolicyPath)
	if err != nil {
		t.Fatalf("read Bedrock managed settings policy: %v", err)
	}
	var bedrockPolicy struct {
		Version int `json:"version"`
		LLM     struct {
			AllowedProviders struct {
				Value  []string `json:"value"`
				Locked bool     `json:"locked"`
			} `json:"allowed_providers"`
			Bedrock struct {
				AllowedInferenceProfileScopes struct {
					Value []struct {
						AccountID string `json:"account_id"`
						Region    string `json:"region"`
					} `json:"value"`
					Locked bool `json:"locked"`
				} `json:"allowed_inference_profile_scopes"`
			} `json:"bedrock"`
		} `json:"llm"`
	}
	if err := json.Unmarshal(bedrockPolicyContents, &bedrockPolicy); err != nil {
		t.Fatalf("Bedrock managed settings policy must be valid JSON: %v", err)
	}
	if bedrockPolicy.Version != 1 {
		t.Fatalf("expected Bedrock managed settings policy version 1, got %d", bedrockPolicy.Version)
	}
	if len(bedrockPolicy.LLM.AllowedProviders.Value) != 1 || bedrockPolicy.LLM.AllowedProviders.Value[0] != "bedrock" || !bedrockPolicy.LLM.AllowedProviders.Locked {
		t.Fatalf("expected Bedrock policy to lock allowed providers to bedrock, got %+v", bedrockPolicy.LLM.AllowedProviders)
	}
	if len(bedrockPolicy.LLM.Bedrock.AllowedInferenceProfileScopes.Value) != 1 ||
		bedrockPolicy.LLM.Bedrock.AllowedInferenceProfileScopes.Value[0].AccountID != "123456789012" ||
		bedrockPolicy.LLM.Bedrock.AllowedInferenceProfileScopes.Value[0].Region != "us-west-2" ||
		!bedrockPolicy.LLM.Bedrock.AllowedInferenceProfileScopes.Locked {
		t.Fatalf("expected Bedrock policy to lock inference profile scopes, got %+v", bedrockPolicy.LLM.Bedrock.AllowedInferenceProfileScopes)
	}
}

func TestConnectorCredentialHardeningGuideInventoriesLocalSecrets(t *testing.T) {
	path := filepath.Join(repoRoot(t), "docs", "security", "connector-credentials.md")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read connector credential hardening guide: %v", err)
	}
	guide := string(contents)
	for _, expected := range []string{
		"# Connector Credential Hardening",
		"~/.workgraph/slack.json",
		"~/.workgraph/calendar.json",
		"~/.workgraph/mail.json",
		"~/.workgraph/notion.json",
		"~/.workgraph/azure-boards.json",
		"~/.workgraph/llm.json",
		"~/.workgraph/connectors.json",
		"0700",
		"0600",
		"POSIX connector credential file permission hardening",
		"Windows connector credential ACL design and CI readiness",
		"Windows connector credential ACL hardening",
		"Windows connector credential ACL implementation verified by Windows CI",
		"access tokens",
		"refresh tokens",
		"workgraph settings get --format json",
		"workgraph connectors doctor",
		"disconnect",
		"does not print connector credentials",
		"OS credential store",
		"Windows Credential Manager",
		"SQLite encryption keys",
	} {
		if !strings.Contains(guide, expected) {
			t.Fatalf("expected connector credential guide to include %q", expected)
		}
	}
}
