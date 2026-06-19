package facts

import (
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
