package facts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestMemoryDiscoveryOffersAndConfirmsAcronymMapping(t *testing.T) {
	root := t.TempDir()
	home, memory := filepath.Join(root, "state"), filepath.Join(root, "memory")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: home, MemoryDir: memory})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := workgraph.InitProjectMemory(workgraph.ProjectMemoryInitConfig{HomeDir: home, MemoryDir: memory, Project: "MMO Harness"})
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(doc.Path)
	if err != nil {
		t.Fatal(err)
	}
	source := "Mindbody.Modernization.Orchestration"
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "mmo-event", Type: "file.modified", Timestamp: time.Now(), Project: source, Payload: `{"path":"/tmp/work/notes.md"}`})
	for _, command := range [][]string{{"memory", "doctor"}, {"memory", "suggest"}, {"resume"}, {"today"}} {
		args := append(append([]string{}, command...), "--home", home, "--memory", memory)
		if command[0] != "today" {
			args = append(args, "MMO Harness")
		}
		output, err := runworkgraph(t, repoRoot(t), args...)
		if err != nil {
			t.Fatalf("%v: %v %s", command, err, output)
		}
		for _, expected := range []string{source, "acronym", "memory link"} {
			if !strings.Contains(string(output), expected) {
				t.Fatalf("%v omitted %s: %s", command, expected, output)
			}
		}
	}
	args := []string{"memory", "doctor", "--home", home, "--memory", memory, "--interactive", "MMO Harness"}
	if output, err := runworkgraphInput(t, repoRoot(t), "", args...); err != nil {
		t.Fatalf("EOF: %v %s", err, output)
	}
	output, err := runworkgraphInput(t, repoRoot(t), "no\n", args...)
	if err != nil {
		t.Fatalf("decline: %v %s", err, output)
	}
	suggestions, err := workgraph.SuggestMemoryUpdates(workgraph.MemorySuggestConfig{HomeDir: home, MemoryDir: memory, Project: "MMO Harness"})
	if err != nil || len(suggestions.Suggestions) != 0 {
		t.Fatal("declined mapping changed evidence")
	}
	output, err = runworkgraphInput(t, repoRoot(t), "yes\n", args...)
	if err != nil {
		t.Fatalf("accept: %v %s", err, output)
	}
	suggestions, err = workgraph.SuggestMemoryUpdates(workgraph.MemorySuggestConfig{HomeDir: home, MemoryDir: memory, Project: "MMO Harness"})
	if err != nil || len(suggestions.Suggestions) != 1 {
		t.Fatalf("accepted mapping missing: %+v %v", suggestions, err)
	}
	contents, err := os.ReadFile(doc.Path)
	if err != nil || string(contents) != string(original) {
		t.Fatal("discovery rewrote memory")
	}
	output, err = runworkgraph(t, repoRoot(t), "memory", "doctor", "--home", home, "--memory", memory, "MMO Harness")
	if err != nil || strings.Contains(string(output), "acronym") {
		t.Fatalf("already linked candidate offered again: %v %s", err, output)
	}
}

func TestMemoryDiscoveryUsesTitleWithoutChangingFilenameIdentity(t *testing.T) {
	root := t.TempDir()
	home, memory := filepath.Join(root, "state"), filepath.Join(root, "memory")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: home, MemoryDir: memory})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := workgraph.InitProjectMemory(workgraph.ProjectMemoryInitConfig{HomeDir: home, MemoryDir: memory, Project: "delivery"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doc.Path, []byte("# MMO Harness\n\nAuthored context.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "repo", Type: "file.modified", Timestamp: time.Now(), Project: "Mindbody.Modernization.Orchestration", Payload: `{}`})
	output, err := runworkgraphInput(t, repoRoot(t), "yes\n", "memory", "doctor", "--home", home, "--memory", memory, "--interactive")
	if err != nil || !strings.Contains(string(output), "'delivery'") {
		t.Fatalf("title discovery: %v %s", err, output)
	}
	result, err := workgraph.SuggestMemoryUpdates(workgraph.MemorySuggestConfig{HomeDir: home, MemoryDir: memory, Project: "delivery"})
	if err != nil || len(result.Suggestions) != 1 {
		t.Fatalf("linked wrong document: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(memory, "projects", "mmo-harness.md")); !os.IsNotExist(err) {
		t.Fatal("created document from display title")
	}
}
