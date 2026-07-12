package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestInitRepairsSensitiveLocalStateToUserOnlyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes are not a Windows security boundary")
	}
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("initialize workgraph: %v", err)
	}
	for _, path := range []string{result.DatabasePath, result.SettingsPath} {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatalf("broaden permissions for %s: %v", path, err)
		}
	}

	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("repair workgraph initialization: %v", err)
	}
	for _, path := range []string{result.DatabasePath, result.SettingsPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("expected %s permissions repaired to 0600, got %v", path, got)
		}
	}
}

func TestMachineReadableSecurityReportIsHonestAndDoesNotExposeSecrets(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	secrets := []string{
		"xoxp-playlist-security-review-secret",
		"captured Playlist acquisition plan",
	}
	slackMode := os.FileMode(0o600)
	if runtime.GOOS != "windows" {
		slackMode = 0o644
	}
	if err := os.WriteFile(filepath.Join(homeDir, "slack.json"), []byte(`{
  "access_token": "xoxp-playlist-security-review-secret",
  "api_base_url": "https://slack.com/api"
}`), slackMode); err != nil {
		t.Fatalf("write fake Slack credentials: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "captured-fixture.txt"), []byte(secrets[1]), 0o600); err != nil {
		t.Fatalf("write captured-data fixture: %v", err)
	}

	output, err := runworkgraph(t, repoRoot, "security", "report", "--home", homeDir, "--format", "json")
	if err != nil {
		t.Fatalf("workgraph security report failed: %v\n%s", err, output)
	}
	for _, secret := range secrets {
		if strings.Contains(string(output), secret) {
			t.Fatalf("security report exposed sensitive value %q:\n%s", secret, output)
		}
	}

	var report struct {
		Version  int    `json:"version"`
		Status   string `json:"status"`
		Platform string `json:"platform"`
		Home     struct {
			Path     string `json:"path"`
			Exists   bool   `json:"exists"`
			UserOnly bool   `json:"user_only"`
		} `json:"home"`
		Database struct {
			Path       string `json:"path"`
			Exists     bool   `json:"exists"`
			UserOnly   bool   `json:"user_only"`
			Encryption string `json:"encryption"`
		} `json:"database"`
		LocalFiles []struct {
			ID       string `json:"id"`
			Exists   bool   `json:"exists"`
			UserOnly bool   `json:"user_only"`
		} `json:"local_files"`
		ManagedSettings struct {
			Path   string `json:"path"`
			Active bool   `json:"active"`
		} `json:"managed_settings"`
		CredentialStorage struct {
			ConnectorSecrets  string `json:"connector_secrets"`
			OSCredentialStore bool   `json:"os_credential_store"`
		} `json:"credential_storage"`
		Network struct {
			ConfiguredDestinationCount int `json:"configured_destination_count"`
		} `json:"network"`
		Findings []struct {
			Code        string `json:"code"`
			Severity    string `json:"severity"`
			Description string `json:"description"`
			Remediation string `json:"remediation"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("parse security report JSON: %v\n%s", err, output)
	}
	if report.Version != 1 || report.Status != "attention_required" || report.Platform != runtime.GOOS {
		t.Fatalf("unexpected report identity: %+v", report)
	}
	if report.Home.Path != homeDir || !report.Home.Exists || (runtime.GOOS != "windows" && !report.Home.UserOnly) {
		t.Fatalf("unexpected home security state: %+v", report.Home)
	}
	if report.Database.Path != filepath.Join(homeDir, "workgraph.db") || !report.Database.Exists || (runtime.GOOS != "windows" && !report.Database.UserOnly) || report.Database.Encryption != "not_enabled" {
		t.Fatalf("unexpected database security state: %+v", report.Database)
	}
	if report.ManagedSettings.Path == "" {
		t.Fatalf("expected fixed managed settings path: %+v", report.ManagedSettings)
	}
	if report.CredentialStorage.ConnectorSecrets != "local_files" || report.CredentialStorage.OSCredentialStore {
		t.Fatalf("unexpected connector credential storage state: %+v", report.CredentialStorage)
	}
	if report.Network.ConfiguredDestinationCount != 1 {
		t.Fatalf("expected one configured network destination, got %+v", report.Network)
	}
	wantedFindings := map[string]bool{
		"sqlite_not_encrypted":          false,
		"connector_secrets_file_backed": false,
	}
	if runtime.GOOS != "windows" {
		wantedFindings["local_file_permissions_too_broad"] = false
	}
	for _, finding := range report.Findings {
		if _, ok := wantedFindings[finding.Code]; ok {
			wantedFindings[finding.Code] = finding.Severity != "" && finding.Description != "" && finding.Remediation != ""
		}
	}
	for code, complete := range wantedFindings {
		if !complete {
			t.Fatalf("expected complete finding %q, got %+v", code, report.Findings)
		}
	}
	for _, file := range report.LocalFiles {
		if file.ID == "slack_credentials" {
			if !file.Exists || (runtime.GOOS != "windows" && file.UserOnly) {
				t.Fatalf("expected report to identify broadened Slack credential permissions, got %+v", file)
			}
			return
		}
	}
	t.Fatalf("expected report to inventory Slack credentials: %+v", report.LocalFiles)
}

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
		"connectors.allowed_ids",
		"connectors.disabled_ids",
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
			AllowedIDs struct {
				Value  []string `json:"value"`
				Locked bool     `json:"locked"`
			} `json:"allowed_ids"`
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
	if len(policy.Connectors.AllowedIDs.Value) == 0 || !policy.Connectors.AllowedIDs.Locked {
		t.Fatalf("expected recommended policy to lock connector allowed IDs, got %+v", policy.Connectors.AllowedIDs)
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

func TestEndpointSecurityReviewGuideStatesControlsAndKnownGaps(t *testing.T) {
	path := filepath.Join(repoRoot(t), "docs", "security", "endpoint-security.md")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read endpoint security review guide: %v", err)
	}
	guide := string(contents)
	for _, expected := range []string{
		"# Endpoint Security Review",
		"workgraph security report --format json",
		"read-only",
		"does not contact provider APIs",
		"connector tokens",
		"0700",
		"0600",
		"SQLite encryption",
		"OS credential store",
		"full-disk encryption",
		"managed settings",
		"hosted LLM",
		"Slack direct-message capture",
		"network destinations",
		"not a compliance attestation",
	} {
		if !strings.Contains(guide, expected) {
			t.Fatalf("expected endpoint security guide to include %q", expected)
		}
	}
}
