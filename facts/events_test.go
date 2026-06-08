package facts

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEventsTodayFiltersByType(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	dbPath := filepath.Join(homeDir, "workgraph.db")
	insertLLMEvent(t, dbPath, "notion-1", "notion.page_updated", now, "workgraph", "Updated launch plan", `{"title":"Updated launch plan","content_preview":"Launch notes"}`)
	insertLLMEvent(t, dbPath, "slack-1", "slack.message", now, "help", "Can you review this?", `{"channel_name":"help","text":"Can you review this?"}`)

	output, err := runworkgraph(t, repoRoot, "events", "today",
		"--home", homeDir,
		"--type", "notion.page_updated",
	)
	if err != nil {
		t.Fatalf("workgraph events today failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"Events today",
		"Type: notion.page_updated",
		"notion.page_updated Updated launch plan",
		"notion-1",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected events output to include %q, got:\n%s", expected, output)
		}
	}
	if strings.Contains(string(output), "slack.message") {
		t.Fatalf("expected type filter to exclude Slack event, got:\n%s", output)
	}
}
