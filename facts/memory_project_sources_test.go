package facts

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestMemoryProjectSourcesFlowThroughEvidence(t *testing.T) {
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
	for i, project := range []string{"Mindbody.Modernization.Orchestration", "mmo-ui", "unrelated"} {
		insertEvent(t, initialized.DatabasePath, storedEvent{ID: project, Type: "file.modified", Timestamp: time.Now().Add(time.Duration(i) * time.Second), Project: project, Payload: `{"path":"/tmp/work/notes.md"}`, Summary: project})
	}
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP TABLE memory_project_sources`); err != nil {
		t.Fatal(err)
	}
	before, err := workgraph.SuggestMemoryUpdates(workgraph.MemorySuggestConfig{HomeDir: home, MemoryDir: memory, Project: "MMO Harness"})
	if err != nil || len(before.Suggestions) != 0 {
		t.Fatalf("legacy database read: %+v %v", before, err)
	}
	var tableCount int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'memory_project_sources'`).Scan(&tableCount); err != nil || tableCount != 0 {
		t.Fatal("read query migrated legacy database")
	}
	args := []string{"--home", home, "--memory", memory}
	linkArgs := append([]string{"memory", "link"}, args...)
	linkArgs = append(linkArgs, "--source", "Mindbody.Modernization.Orchestration", "--source", "mmo-ui", "MMO Harness")
	for i := 0; i < 2; i++ {
		output, err := runworkgraph(t, repoRoot(t), linkArgs...)
		if err != nil {
			t.Fatalf("link: %v %s", err, output)
		}
	}
	contents, err := os.ReadFile(doc.Path)
	if err != nil || string(contents) != string(original) {
		t.Fatal("link rewrote authored memory")
	}
	suggestions, err := workgraph.SuggestMemoryUpdates(workgraph.MemorySuggestConfig{HomeDir: home, MemoryDir: memory, Project: "MMO Harness"})
	if err != nil || len(suggestions.Suggestions) != 2 {
		t.Fatalf("mapped suggestions: %+v %v", suggestions, err)
	}
	resumed, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: home, MemoryDir: memory, Project: "MMO Harness"})
	if err != nil || len(resumed.Events) != 2 || resumed.Memory == nil {
		t.Fatalf("mapped resume: %+v %v", resumed, err)
	}
	for _, event := range resumed.Events {
		if event.Project == "MMO Harness" {
			t.Fatal("rewrote captured identity")
		}
	}
	links, err := workgraph.ListMemoryLinks(workgraph.MemoryLinksConfig{HomeDir: home, MemoryDir: memory, Project: "MMO Harness"})
	if err != nil || len(links.Links) != 0 || !strings.Contains(links.Message, "mmo-ui") {
		t.Fatalf("mapping visibility: %+v %v", links, err)
	}
	_, err = workgraph.PromoteMemory(workgraph.MemoryPromoteConfig{HomeDir: home, MemoryDir: memory, Project: "MMO Harness", EvidenceID: "mmo-ui", Text: "UI supports this workstream."})
	if err != nil {
		t.Fatal(err)
	}
	unlink := append([]string{"memory", "unlink"}, args...)
	unlink = append(unlink, "--source", "mmo-ui", "MMO Harness")
	output, err := runworkgraph(t, repoRoot(t), unlink...)
	if err != nil {
		t.Fatalf("unlink: %v %s", err, output)
	}
	suggestions, err = workgraph.SuggestMemoryUpdates(workgraph.MemorySuggestConfig{HomeDir: home, MemoryDir: memory, Project: "MMO Harness"})
	if err != nil || len(suggestions.Suggestions) != 1 {
		t.Fatalf("unlink not reflected: %+v %v", suggestions, err)
	}
	links, err = workgraph.ListMemoryLinks(workgraph.MemoryLinksConfig{HomeDir: home, MemoryDir: memory, Project: "MMO Harness"})
	if err != nil || len(links.Links) != 1 {
		t.Fatalf("unlink lost promoted evidence: %+v %v", links, err)
	}
	bad := append([]string{"memory", "link"}, args...)
	bad = append(bad, "--source", "unrelated", "--source", "does-not-exist", "MMO Harness")
	if output, err := runworkgraph(t, repoRoot(t), bad...); err == nil {
		t.Fatalf("accepted unknown source: %s", output)
	}
	suggestions, err = workgraph.SuggestMemoryUpdates(workgraph.MemorySuggestConfig{HomeDir: home, MemoryDir: memory, Project: "MMO Harness"})
	if err != nil || len(suggestions.Suggestions) != 1 {
		t.Fatal("partially applied invalid source list")
	}
}
