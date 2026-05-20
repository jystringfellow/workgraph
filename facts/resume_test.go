package facts

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestResumeListsRecentProjectsWhenProjectIsOmitted(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Date(2026, 5, 20, 16, 0, 0, 0, time.Local)
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "workgraph-old",
		Type:      "file.modified",
		Timestamp: now.Add(-2 * time.Hour),
		Project:   "workgraph",
		Payload:   `{"path":"/tmp/workgraph/today.go","operation":"modified"}`,
	})
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "cupcake-recent",
		Type:      "github.pull_request",
		Timestamp: now.Add(-10 * time.Minute),
		Project:   "Cupcake",
		Payload:   `{"repository":"jystringfellow/Cupcake","number":42,"state":"open","title":"Add cupcake API"}`,
		Summary:   "Add cupcake API",
	})

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Now:          now,
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}

	cupcakeIndex := strings.Index(resume.Message, "- Cupcake: 1 event")
	workgraphIndex := strings.Index(resume.Message, "- workgraph: 1 event")
	if !strings.Contains(resume.Message, "Resumable projects") {
		t.Fatalf("expected resumable projects section, got:\n%s", resume.Message)
	}
	if cupcakeIndex == -1 || workgraphIndex == -1 {
		t.Fatalf("expected project event counts, got:\n%s", resume.Message)
	}
	if cupcakeIndex > workgraphIndex {
		t.Fatalf("expected most recently active project first, got:\n%s", resume.Message)
	}
	if !strings.Contains(resume.Message, "Run: workgraph resume <project>") {
		t.Fatalf("expected resume hint, got:\n%s", resume.Message)
	}
}

func TestResumeReturnsRecentEventsForProject(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Date(2026, 5, 20, 16, 0, 0, 0, time.Local)
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "cupcake-old",
		Type:      "file.modified",
		Timestamp: now.Add(-2 * time.Hour),
		Project:   "Cupcake",
		Payload:   `{"path":"/tmp/Cupcake/README.md","operation":"modified"}`,
	})
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "other-project",
		Type:      "file.modified",
		Timestamp: now.Add(-time.Hour),
		Project:   "workgraph",
		Payload:   `{"path":"/tmp/workgraph/today.go","operation":"modified"}`,
	})
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "cupcake-recent",
		Type:      "git.commit",
		Timestamp: now.Add(-10 * time.Minute),
		Project:   "Cupcake",
		Payload:   `{"commit":"abcdef1234567890","branch":"main","subject":"Add cupcake API"}`,
		Summary:   "Add cupcake API",
	})

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Project:      "Cupcake",
		Now:          now,
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}

	recentIndex := strings.Index(resume.Message, "git.commit Add cupcake API (main abcdef1)")
	oldIndex := strings.Index(resume.Message, "README.md")
	if !strings.Contains(resume.Message, "Resume Cupcake") {
		t.Fatalf("expected project resume heading, got:\n%s", resume.Message)
	}
	if !strings.Contains(resume.Message, "Recent activity") {
		t.Fatalf("expected recent activity section, got:\n%s", resume.Message)
	}
	if recentIndex == -1 || oldIndex == -1 {
		t.Fatalf("expected Cupcake events in output, got:\n%s", resume.Message)
	}
	if recentIndex > oldIndex {
		t.Fatalf("expected recent activity first, got:\n%s", resume.Message)
	}
	if strings.Contains(resume.Message, "today.go") {
		t.Fatalf("expected other project events to be omitted, got:\n%s", resume.Message)
	}
}

func TestResumeCapsRecentActivityWithOlderEventCount(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Date(2026, 5, 20, 16, 0, 0, 0, time.Local)
	for index := 0; index < 12; index++ {
		insertEvent(t, result.DatabasePath, storedEvent{
			ID:        "cupcake-event-" + string(rune('a'+index)),
			Type:      "file.modified",
			Timestamp: now.Add(-time.Duration(index) * time.Minute),
			Project:   "Cupcake",
			Payload:   `{"path":"/tmp/Cupcake/file.go","operation":"modified"}`,
			Summary:   "Event " + string(rune('A'+index)),
		})
	}

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Project:      "Cupcake",
		Now:          now,
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}

	if !strings.Contains(resume.Message, "Event A") || !strings.Contains(resume.Message, "Event J") {
		t.Fatalf("expected 10 most recent events, got:\n%s", resume.Message)
	}
	if strings.Contains(resume.Message, "Event K") || strings.Contains(resume.Message, "Event L") {
		t.Fatalf("expected older events to be omitted, got:\n%s", resume.Message)
	}
	if !strings.Contains(resume.Message, "... and 2 older events") {
		t.Fatalf("expected omitted older event count, got:\n%s", resume.Message)
	}
}

func TestResumeIncludesRelevantFiles(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Date(2026, 5, 20, 16, 0, 0, 0, time.Local)
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "cupcake-file",
		Type:      "file.modified",
		Timestamp: now.Add(-10 * time.Minute),
		Project:   "Cupcake",
		Payload:   `{"path":"/tmp/Cupcake/api.go","operation":"modified"}`,
	})

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Project:      "Cupcake",
		Now:          now,
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}

	if !strings.Contains(resume.Message, "Relevant files") {
		t.Fatalf("expected relevant files section, got:\n%s", resume.Message)
	}
	if !strings.Contains(resume.Message, "- /tmp/Cupcake/api.go") {
		t.Fatalf("expected touched file path, got:\n%s", resume.Message)
	}
}

func TestResumeIncludesPeopleWhenKnown(t *testing.T) {
	t.Skip("TBD: deferred until people/entity links are active")
}

func TestResumeSuggestsNextStepWhenEnoughContextExists(t *testing.T) {
	t.Skip("TBD: resume <project> suggests a next step when enough context exists")
}

func TestResumeOutputIncludesExpectedSections(t *testing.T) {
	t.Skip("TBD: resume output includes Recent activity, Relevant files, and Suggested next step sections")
}

func TestResumeShowsMissingProjectState(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Project:      "MissingProject",
		Now:          time.Date(2026, 5, 20, 16, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}

	if !strings.Contains(resume.Message, "No recent activity found for MissingProject.") {
		t.Fatalf("expected missing project state, got:\n%s", resume.Message)
	}
	if !strings.Contains(resume.Message, "Check the project name or run workgraph run") {
		t.Fatalf("expected capture suggestion, got:\n%s", resume.Message)
	}
}

func TestResumeLabelsUncertainNextSteps(t *testing.T) {
	t.Skip("TBD: resume labels uncertain next steps as suggestions")
}

func TestCLIResumeListsRecentProjects(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Now()
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "cli-resume-project",
		Type:      "file.modified",
		Timestamp: now.Add(-time.Hour),
		Project:   "Cupcake",
		Payload:   `{"path":"/tmp/Cupcake/api.go","operation":"modified"}`,
	})

	cmd := exec.Command(
		"go",
		"run",
		"./cmd/workgraph",
		"resume",
		"--home",
		homeDir,
		"--database",
		result.DatabasePath,
	)
	cmd.Dir = repoRoot(t)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workgraph resume failed: %v\n%s", err, output)
	}

	if !strings.Contains(string(output), "Resumable projects") || !strings.Contains(string(output), "Cupcake") {
		t.Fatalf("expected CLI resume project list, got:\n%s", output)
	}
}
