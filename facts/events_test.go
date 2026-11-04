package facts

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestEventsTodayKeepsCheckpointContentOutOfTheLabel(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	insertEvent(t, result.DatabasePath, storedEvent{ID: "checkpoint", Type: "ai.session_checkpointed", Timestamp: now, Project: "workgraph", Payload: `{"agent_stated":{"goal":"Private checkpoint context"}}`, Summary: "AI session checkpointed"})
	output, err := runworkgraph(t, repoRoot(t), "events", "today", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph events today failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "ai.session_checkpointed AI session checkpointed") {
		t.Fatalf("expected generic checkpoint label, got:\n%s", output)
	}
	if strings.Contains(string(output), "Private checkpoint context") {
		t.Fatalf("expected checkpoint content to remain out of events today label, got:\n%s", output)
	}
}

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
