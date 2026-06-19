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
		"workgraph settings get --format json",
		"hosted LLM providers",
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
	if policy.Connectors.Slack.IncludeDMs.Value || !policy.Connectors.Slack.IncludeDMs.Locked {
		t.Fatalf("expected recommended policy to lock Slack DM capture off")
	}
}
