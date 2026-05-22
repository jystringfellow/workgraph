package facts

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

// Domain entity facts cover WorkGraph concepts before storage.

func TestEventRequiresIDSourceTypeTimestampPayload(t *testing.T) {
	t.Skip("TBD: event requires id, source, type, timestamp, and payload_json")
}

func TestEventCanIncludeProjectActorAndSummary(t *testing.T) {
	t.Skip("TBD: event may include project, actor, and summary metadata")
}

func TestEventPayloadMustBeValidJSON(t *testing.T) {
	t.Skip("TBD: event payload_json must be valid JSON")
}

func TestEventTimestampUsesRFC3339(t *testing.T) {
	t.Skip("TBD: event timestamp uses RFC3339")
}

func TestSessionRequiresIDStartedAtEndedAt(t *testing.T) {
	t.Skip("TBD: session requires id, started_at, and ended_at")
}

func TestDomainSessionStartMustBeBeforeOrEqualEnd(t *testing.T) {
	t.Skip("TBD: session started_at must be before or equal to ended_at")
}

func TestMemoryDocRequiresIDPathKindContentUpdatedAt(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir:   homeDir,
		MemoryDir: memoryDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	memoryPath := filepath.Join(memoryDir, "projects", "cupcake.md")
	if err := os.WriteFile(memoryPath, []byte("# Cupcake\n\nPrioritize auth.\n"), 0o644); err != nil {
		t.Fatalf("write project memory: %v", err)
	}
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "cupcake-memory",
		Type:      "file.modified",
		Timestamp: time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
		Project:   "Cupcake",
		Payload:   `{"path":"/tmp/Cupcake/api.go","operation":"modified"}`,
	})

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		MemoryDir:    memoryDir,
		Project:      "Cupcake",
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if resume.Memory == nil {
		t.Fatalf("expected project memory doc")
	}
	if resume.Memory.ID == "" {
		t.Fatalf("expected memory doc id")
	}
	if resume.Memory.Path != memoryPath {
		t.Fatalf("expected memory doc path %q, got %q", memoryPath, resume.Memory.Path)
	}
	if resume.Memory.Kind != "markdown" {
		t.Fatalf("expected markdown memory kind, got %q", resume.Memory.Kind)
	}
	if resume.Memory.Content != "# Cupcake\n\nPrioritize auth.\n" {
		t.Fatalf("expected memory content, got %q", resume.Memory.Content)
	}
	if resume.Memory.UpdatedAt.IsZero() {
		t.Fatalf("expected memory doc updated timestamp")
	}
}

func TestMemoryDocUpdatedAtUsesRFC3339(t *testing.T) {
	t.Skip("TBD: memory doc updated_at uses RFC3339")
}
