package facts

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestTodayCLIPreviewsCheckpointsWhileEventsTodayKeepsTheStoredLabel(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatal(err)
	}
	insertEvent(t, result.DatabasePath, storedEvent{ID: "checkpoint", Type: "ai.session_checkpointed", Timestamp: time.Now().UTC(), Project: "workgraph", Payload: `{"agent_stated":{"goal":"Private checkpoint context","next_actions":["Write facts"]}}`, Summary: "AI session checkpointed"})
	today, err := runworkgraph(t, repoRoot(t), "today", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph today failed: %v\n%s", err, today)
	}
	if !strings.Contains(string(today), "ai.session_checkpointed AI checkpoint: Private checkpoint context; next: Write facts") || strings.Contains(string(today), "AI session checkpointed") {
		t.Fatalf("expected checkpoint preview in default today output, got:\n%s", today)
	}
	events, err := runworkgraph(t, repoRoot(t), "events", "today", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph events today failed: %v\n%s", err, events)
	}
	if !strings.Contains(string(events), "ai.session_checkpointed AI session checkpointed") || strings.Contains(string(events), "Private checkpoint context") {
		t.Fatalf("expected stored checkpoint label in events today, got:\n%s", events)
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
