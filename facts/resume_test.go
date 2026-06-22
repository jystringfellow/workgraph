package facts

import (
	"os"
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

func TestResumeProjectListHidesClonedRepoHistoryUnlessAllIsRequested(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Date(2026, 6, 22, 9, 0, 0, 0, time.Local)
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "cloned-history",
		Source:    "git",
		Type:      "git.commit",
		Timestamp: now.Add(-5 * time.Minute),
		Project:   "PlaidQuickstartBlazor",
		Payload:   `{"repo_path":"/tmp/Code/PlaidQuickstartBlazor","commit":"1111111111111111","branch":"main","subject":"Initial commit","author_name":"Other Dev","author_email":"other@example.test"}`,
		Summary:   "Initial commit",
	})
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "my-work",
		Source:    "git",
		Type:      "git.commit",
		Timestamp: now.Add(-10 * time.Minute),
		Project:   "workgraph",
		Payload:   `{"repo_path":"/tmp/Code/workgraph","commit":"2222222222222222","branch":"main","subject":"Improve resume relevance","author_name":"Stringfellow","author_email":"me@example.test"}`,
		Summary:   "Improve resume relevance",
	})

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Now:          now,
		GitEmails:    []string{"me@example.test"},
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if strings.Contains(resume.Message, "PlaidQuickstartBlazor") {
		t.Fatalf("expected cloned third-party git history to be hidden from default resume, got:\n%s", resume.Message)
	}
	if !strings.Contains(resume.Message, "workgraph") {
		t.Fatalf("expected user-authored git work to remain resumable, got:\n%s", resume.Message)
	}

	all, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Now:          now,
		GitEmails:    []string{"me@example.test"},
		AllProjects:  true,
	})
	if err != nil {
		t.Fatalf("resume --all failed: %v", err)
	}
	if !strings.Contains(all.Message, "PlaidQuickstartBlazor") || !strings.Contains(all.Message, "workgraph") {
		t.Fatalf("expected resume --all to include weak and strong projects, got:\n%s", all.Message)
	}
}

func TestResumeProjectListRequiresGitHubUserEvidenceWhenLoginIsKnown(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Date(2026, 6, 22, 10, 0, 0, 0, time.Local)
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "third-party-issue",
		Source:    "github",
		Type:      "github.issue",
		Timestamp: now.Add(-5 * time.Minute),
		Project:   "Sparkle",
		Payload:   `{"repository":"sparkle-project/Sparkle","number":1,"state":"open","actor":"someone-else","title":"External issue"}`,
		Summary:   "External issue",
	})
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "my-issue",
		Source:    "github",
		Type:      "github.issue",
		Timestamp: now.Add(-10 * time.Minute),
		Project:   "workgraph",
		Payload:   `{"repository":"jystringfellow/workgraph","number":2,"state":"open","actor":"jystringfellow","title":"My issue"}`,
		Summary:   "My issue",
	})

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Now:          now,
		GitHubLogins: []string{"jystringfellow"},
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if strings.Contains(resume.Message, "Sparkle") {
		t.Fatalf("expected third-party GitHub issue project to be hidden, got:\n%s", resume.Message)
	}
	if !strings.Contains(resume.Message, "workgraph") {
		t.Fatalf("expected user-authored GitHub issue project to remain resumable, got:\n%s", resume.Message)
	}
}

func TestResumeProjectListKeepsSlackConversationsAndDemotesBroadFolders(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Date(2026, 6, 22, 11, 0, 0, 0, time.Local)
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "download-churn",
		Source:    "file",
		Type:      "file.created",
		Timestamp: now.Add(-5 * time.Minute),
		Project:   "Downloads",
		Payload:   `{"path":"/Users/stringfellow/Downloads/Unconfirmed.crdownload","operation":"created"}`,
	})
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "slack-dm",
		Source:    "slack",
		Type:      "slack.message",
		Timestamp: now.Add(-10 * time.Minute),
		Project:   "DM: Lila",
		Payload:   `{"channel_id":"D123","user_name":"lila","text":"Can you pick this up?","ts":"1782126000.000000"}`,
		Summary:   "Can you pick this up?",
	})

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Now:          now,
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if strings.Contains(resume.Message, "Downloads") {
		t.Fatalf("expected broad Downloads file churn to be hidden, got:\n%s", resume.Message)
	}
	if !strings.Contains(resume.Message, "DM: Lila") {
		t.Fatalf("expected Slack conversation to remain resumable, got:\n%s", resume.Message)
	}
}

func TestResumeCanonicalizesOlderSlackChannelIDsWhenResolvedNamesExist(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Date(2026, 6, 22, 11, 30, 0, 0, time.Local)
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "old-raw-slack-message",
		Source:    "slack",
		Type:      "slack.message",
		Timestamp: now.Add(-5 * time.Minute),
		Project:   "D123",
		Payload:   `{"channel_id":"D123","user_name":"lila","text":"Earlier raw id message","ts":"1782126000.000000"}`,
		Summary:   "Earlier raw id message",
	})
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "new-resolved-slack-message",
		Source:    "slack",
		Type:      "slack.message",
		Timestamp: now.Add(-10 * time.Minute),
		Project:   "DM: Lila",
		Payload:   `{"channel_id":"D123","channel_name":"DM: Lila","user_name":"lila","text":"Resolved message","ts":"1782126010.000000"}`,
		Summary:   "Resolved message",
	})

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Now:          now,
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if strings.Contains(resume.Message, "D123") {
		t.Fatalf("expected raw Slack channel id to be hidden when resolved name exists, got:\n%s", resume.Message)
	}
	if !strings.Contains(resume.Message, "- DM: Lila: 2 events") {
		t.Fatalf("expected Slack events to be merged under resolved conversation name, got:\n%s", resume.Message)
	}

	rawResume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Project:      "D123",
		Now:          now,
	})
	if err != nil {
		t.Fatalf("resume raw Slack id failed: %v", err)
	}
	if !strings.Contains(rawResume.Message, "Resume DM: Lila") || !strings.Contains(rawResume.Message, "Earlier raw id message") {
		t.Fatalf("expected exact raw Slack id resume to resolve to named conversation, got:\n%s", rawResume.Message)
	}
}

func TestResumeProjectListHidesStaleUserAuthoredGitWorkUnlessAllIsRequested(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.Local)
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "stale-user-work",
		Source:    "git",
		Type:      "git.commit",
		Timestamp: now.AddDate(-2, 0, 0),
		Project:   "HelloWorld",
		Payload:   `{"repo_path":"/tmp/Code/HelloWorld","commit":"3333333333333333","branch":"main","subject":"Hello world","author_name":"Stringfellow","author_email":"me@example.test"}`,
		Summary:   "Hello world",
	})
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "recent-user-work",
		Source:    "git",
		Type:      "git.commit",
		Timestamp: now.Add(-time.Hour),
		Project:   "workgraph",
		Payload:   `{"repo_path":"/tmp/Code/workgraph","commit":"4444444444444444","branch":"main","subject":"Recent work","author_name":"Stringfellow","author_email":"me@example.test"}`,
		Summary:   "Recent work",
	})

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Now:          now,
		GitEmails:    []string{"me@example.test"},
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if strings.Contains(resume.Message, "HelloWorld") {
		t.Fatalf("expected stale user-authored git work to be hidden from default resume, got:\n%s", resume.Message)
	}
	if !strings.Contains(resume.Message, "workgraph") {
		t.Fatalf("expected recent user-authored git work to remain resumable, got:\n%s", resume.Message)
	}

	all, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Now:          now,
		GitEmails:    []string{"me@example.test"},
		AllProjects:  true,
	})
	if err != nil {
		t.Fatalf("resume --all failed: %v", err)
	}
	if !strings.Contains(all.Message, "HelloWorld") || !strings.Contains(all.Message, "workgraph") {
		t.Fatalf("expected resume --all to include stale and recent projects, got:\n%s", all.Message)
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

func TestResumeOmitsTransientFileEvidenceBeforeCappingActivity(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Date(2026, 5, 21, 16, 0, 0, 0, time.Local)
	for index := 0; index < 10; index++ {
		insertEvent(t, result.DatabasePath, storedEvent{
			ID:        "fractile-transient-" + string(rune('a'+index)),
			Type:      "file.created",
			Timestamp: now.Add(-time.Duration(index) * time.Minute),
			Project:   "FracTile",
			Payload:   `{"path":"/tmp/FracTile/.dat.nosync101E8.QvsH8z","operation":"created"}`,
		})
	}
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "fractile-readme",
		Type:      "file.modified",
		Timestamp: now.Add(-11 * time.Minute),
		Project:   "FracTile",
		Payload:   `{"path":"/tmp/FracTile/README.md","operation":"modified"}`,
	})

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		Project:      "FracTile",
		Now:          now,
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}

	if !strings.Contains(resume.Message, "README.md") {
		t.Fatalf("expected durable edit to survive transient activity, got:\n%s", resume.Message)
	}
	if strings.Contains(resume.Message, ".dat.nosync") {
		t.Fatalf("expected transient local paths to be omitted, got:\n%s", resume.Message)
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

func TestResumeShowsKnownOpenGitHubWorkOutsideRecentActivityCap(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := time.Date(2026, 5, 21, 16, 0, 0, 0, time.Local)
	for index := 0; index < 10; index++ {
		insertEvent(t, result.DatabasePath, storedEvent{
			ID:        "cupcake-recent-file-" + string(rune('a'+index)),
			Type:      "file.modified",
			Timestamp: now.Add(-time.Duration(index) * time.Minute),
			Project:   "Cupcake",
			Payload:   `{"path":"/tmp/Cupcake/file.go","operation":"modified"}`,
			Summary:   "Recent file event",
		})
	}
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "cupcake-open-pr",
		Source:    "github",
		Type:      "github.pull_request",
		Timestamp: now.Add(-time.Hour),
		Project:   "Cupcake",
		Payload:   `{"repository":"jystringfellow/Cupcake","number":42,"state":"open","title":"Add cupcake API"}`,
		Summary:   "Add cupcake API",
	})
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "cupcake-open-issue",
		Source:    "github",
		Type:      "github.issue",
		Timestamp: now.Add(-2 * time.Hour),
		Project:   "Cupcake",
		Payload:   `{"repository":"jystringfellow/Cupcake","number":12,"state":"open","title":"Bug in cupcake frosting"}`,
		Summary:   "Bug in cupcake frosting",
	})
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "cupcake-merged-pr",
		Source:    "github",
		Type:      "github.pull_request",
		Timestamp: now.Add(-3 * time.Hour),
		Project:   "Cupcake",
		Payload:   `{"repository":"jystringfellow/Cupcake","number":7,"state":"merged","title":"Ship old cupcake work"}`,
		Summary:   "Ship old cupcake work",
	})
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "other-open-issue",
		Source:    "github",
		Type:      "github.issue",
		Timestamp: now.Add(-4 * time.Hour),
		Project:   "workgraph",
		Payload:   `{"repository":"jystringfellow/workgraph","number":9,"state":"open","title":"Other project work"}`,
		Summary:   "Other project work",
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

	if !strings.Contains(resume.Message, "Open GitHub work") {
		t.Fatalf("expected open GitHub work section, got:\n%s", resume.Message)
	}
	if !strings.Contains(resume.Message, "github.pull_request Add cupcake API (#42 open)") {
		t.Fatalf("expected open PR outside activity cap, got:\n%s", resume.Message)
	}
	if !strings.Contains(resume.Message, "github.issue Bug in cupcake frosting (#12 open)") {
		t.Fatalf("expected open issue outside activity cap, got:\n%s", resume.Message)
	}
	if strings.Contains(resume.Message, "Ship old cupcake work") || strings.Contains(resume.Message, "Other project work") {
		t.Fatalf("expected closed and other-project GitHub work to be omitted, got:\n%s", resume.Message)
	}
}

func TestResumeIncludesProjectMemoryWhenPresent(t *testing.T) {
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
	if err := os.WriteFile(memoryPath, []byte("# Cupcake\n\n## Current priorities\n- Finish auth first.\n"), 0o644); err != nil {
		t.Fatalf("write project memory: %v", err)
	}
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "cupcake-project-memory",
		Type:      "file.modified",
		Timestamp: time.Date(2026, 5, 22, 12, 0, 0, 0, time.Local),
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
	for _, expected := range []string{"Recent activity", "Project memory", "Current priorities", "Finish auth first."} {
		if !strings.Contains(resume.Message, expected) {
			t.Fatalf("expected resume to include %q, got:\n%s", expected, resume.Message)
		}
	}
}

func TestResumeIncludesProjectMemoryWithoutCapturedActivity(t *testing.T) {
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
	if err := os.WriteFile(memoryPath, []byte("# Cupcake\n\n## Context\nMemory-first project.\n"), 0o644); err != nil {
		t.Fatalf("write project memory: %v", err)
	}

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		MemoryDir:    memoryDir,
		Project:      "Cupcake",
		Now:          time.Date(2026, 5, 22, 12, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}

	for _, expected := range []string{
		"No recent activity found for Cupcake.",
		"Project memory",
		"Memory-first project.",
	} {
		if !strings.Contains(resume.Message, expected) {
			t.Fatalf("expected resume to include %q, got:\n%s", expected, resume.Message)
		}
	}
}

func TestResumePointsAtMissingProjectMemory(t *testing.T) {
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
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "cupcake-no-project-memory",
		Type:      "file.modified",
		Timestamp: time.Date(2026, 5, 22, 12, 0, 0, 0, time.Local),
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
	expectedPath := filepath.Join(memoryDir, "projects", "cupcake.md")
	if !strings.Contains(resume.Message, "Add project memory: "+expectedPath) {
		t.Fatalf("expected resume to point at %q, got:\n%s", expectedPath, resume.Message)
	}
}

func TestResumeDoesNotReadProjectMemoryOutsideMemoryRepo(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(memoryDir, "Outside.md"), []byte("Should stay outside project memory."), 0o644); err != nil {
		t.Fatalf("write outside memory: %v", err)
	}
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "unsafe-project-memory",
		Type:      "file.modified",
		Timestamp: time.Date(2026, 5, 22, 12, 0, 0, 0, time.Local),
		Project:   "../Outside",
		Payload:   `{"path":"/tmp/Outside/api.go","operation":"modified"}`,
	})

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		MemoryDir:    memoryDir,
		Project:      "../Outside",
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if strings.Contains(resume.Message, "Should stay outside project memory.") {
		t.Fatalf("expected resume not to read outside project memory, got:\n%s", resume.Message)
	}
}

func TestResumeUsesLowerKebabProjectMemoryPath(t *testing.T) {
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
	memoryPath := filepath.Join(memoryDir, "projects", "cupcake-api.md")
	if err := os.WriteFile(memoryPath, []byte("Kebab memory path."), 0o644); err != nil {
		t.Fatalf("write project memory: %v", err)
	}
	insertEvent(t, result.DatabasePath, storedEvent{
		ID:        "kebab-project-memory",
		Type:      "file.modified",
		Timestamp: time.Date(2026, 5, 22, 12, 0, 0, 0, time.Local),
		Project:   "Cupcake API",
		Payload:   `{"path":"/tmp/Cupcake/api.go","operation":"modified"}`,
	})

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: result.DatabasePath,
		MemoryDir:    memoryDir,
		Project:      "Cupcake API",
	})
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if !strings.Contains(resume.Message, "Kebab memory path.") {
		t.Fatalf("expected resume to load %q, got:\n%s", memoryPath, resume.Message)
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
	if !strings.Contains(resume.Message, "Check the project name or run workgraph start") {
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
