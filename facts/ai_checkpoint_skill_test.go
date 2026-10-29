package facts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledAICheckpointSkillProvidesExplicitPrivateWorkflow(t *testing.T) {
	root := repoRootForDaemon()
	skillPath := filepath.Join(root, ".agents", "skills", "workgraph-ai-checkpoint", "SKILL.md")
	skillBytes, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read bundled AI checkpoint skill: %v", err)
	}
	skill := string(skillBytes)
	for _, expected := range []string{
		"name: workgraph-ai-checkpoint",
		"WORKGRAPH_AI_SESSION_ID",
		"workgraph ai checkpoint --stdin",
		"goal",
		"current_state",
		"completed",
		"next_actions",
		"blockers",
		"decisions",
		"credentials",
		"transcript",
		"observed state",
		"explicit",
		"WORKGRAPH_AI_CHECKPOINT_POLICY=agent",
		"--agent-initiated",
	} {
		if !strings.Contains(skill, expected) {
			t.Fatalf("AI checkpoint skill is missing %q:\n%s", expected, skill)
		}
	}
	if len(skillBytes) > 4096 {
		t.Fatalf("AI checkpoint skill is %d bytes; keep it within 4096 because it loads into every session that uses it", len(skillBytes))
	}
	for _, client := range []string{"claude-code", "codex", "copilot"} {
		packaged, err := os.ReadFile(filepath.Join(root, "integrations", client, "plugins", "workgraph", "skills", "workgraph-ai-checkpoint", "SKILL.md"))
		if err != nil {
			t.Fatalf("read %s packaged AI checkpoint skill: %v", client, err)
		}
		if string(packaged) != skill {
			t.Fatalf("%s packaged AI checkpoint skill differs from the canonical skill", client)
		}
	}

	metadataPath := filepath.Join(root, ".agents", "skills", "workgraph-ai-checkpoint", "agents", "openai.yaml")
	metadataBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("read AI checkpoint skill metadata: %v", err)
	}
	metadata := string(metadataBytes)
	for _, expected := range []string{
		`display_name: "workgraph AI Checkpoint"`,
		`short_description: "Record durable AI session restart context"`,
		`$workgraph-ai-checkpoint`,
	} {
		if !strings.Contains(metadata, expected) {
			t.Fatalf("AI checkpoint skill metadata is missing %q:\n%s", expected, metadata)
		}
	}

	readmeBytes, err := os.ReadFile(filepath.Join(root, ".agents", "README.md"))
	if err != nil {
		t.Fatalf("read agent README: %v", err)
	}
	if !strings.Contains(string(readmeBytes), "skills/workgraph-ai-checkpoint/") {
		t.Fatalf("agent README does not advertise the bundled AI checkpoint skill")
	}
}
