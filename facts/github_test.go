package facts

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestGitHubCaptureStoresPullRequestEvent(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	eventsPath := filepath.Join(tempDir, "github-events.json")
	if err := os.WriteFile(eventsPath, []byte(`[
  {
    "kind": "pull_request",
    "repository": "jystringfellow/Cupcake",
    "number": 42,
    "url": "https://github.com/jystringfellow/Cupcake/pull/42",
    "state": "open",
    "actor": "octocat",
    "title": "Add cupcake API",
    "branch": "feature/cupcake-api",
    "commit": "abcdef1234567890",
    "updated_at": "2026-05-20T14:30:00Z"
  }
]`), 0o644); err != nil {
		t.Fatalf("write github events: %v", err)
	}

	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	output, err := runworkgraph(t, repoRoot, "github", "capture", "--home", homeDir, "--events-file", eventsPath)
	if err != nil {
		t.Fatalf("workgraph github capture failed: %v\n%s", err, output)
	}

	event := githubEvent(t, filepath.Join(homeDir, "workgraph.db"), "github.pull_request", "jystringfellow/Cupcake", 42)
	if event.Project != "Cupcake" {
		t.Fatalf("expected fallback project %q, got %q", "Cupcake", event.Project)
	}
	if event.Actor != "octocat" {
		t.Fatalf("expected actor octocat, got %q", event.Actor)
	}
	if event.Summary != "Add cupcake API" {
		t.Fatalf("expected summary title, got %q", event.Summary)
	}
	for _, expected := range []string{
		`"repository":"jystringfellow/Cupcake"`,
		`"number":42`,
		`"url":"https://github.com/jystringfellow/Cupcake/pull/42"`,
		`"state":"open"`,
		`"actor":"octocat"`,
		`"title":"Add cupcake API"`,
		`"branch":"feature/cupcake-api"`,
		`"commit":"abcdef1234567890"`,
	} {
		if !strings.Contains(event.PayloadJSON, expected) {
			t.Fatalf("expected payload to include %s, got %s", expected, event.PayloadJSON)
		}
	}
	if !strings.Contains(string(output), "GitHub capture complete") {
		t.Fatalf("expected capture summary, got:\n%s", output)
	}
}

func TestGitHubConnectValidatesGHAndEnablesSharedConnectorPolling(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	ghPath := writeFakeGH(t, tempDir, 5000)
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "github", "connect", "--home", homeDir, "--gh", ghPath)
	if err != nil {
		t.Fatalf("workgraph github connect failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "GitHub connected") {
		t.Fatalf("expected github connect output, got:\n%s", output)
	}
	if !strings.Contains(string(output), "workgraph connectors disable github") {
		t.Fatalf("expected disable guidance, got:\n%s", output)
	}
	if !strings.Contains(string(output), "workgraph connectors interval github") {
		t.Fatalf("expected interval guidance, got:\n%s", output)
	}

	logContents, err := os.ReadFile(filepath.Join(tempDir, "gh.log"))
	if err != nil {
		t.Fatalf("read gh log: %v", err)
	}
	if !strings.Contains(string(logContents), "auth status") {
		t.Fatalf("expected github connect to validate gh auth status, got log:\n%s", logContents)
	}

	output, err = runworkgraph(t, repoRoot, "connectors", "list", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph connectors list failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "- github: connected, enabled") {
		t.Fatalf("expected enabled github connector, got:\n%s", output)
	}

	output, err = runworkgraph(t, repoRoot, "connectors", "status", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph connectors status failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"- github: setup ready",
		"last validated",
		"polling enabled",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected github connector status to include %q, got:\n%s", expected, output)
		}
	}
}

func TestGitHubConnectFailureRecordsValidationError(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	ghPath := writeFakeGHAuthFailure(t, tempDir)
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "github", "connect", "--home", homeDir, "--gh", ghPath)
	if err == nil {
		t.Fatalf("expected workgraph github connect to fail, got:\n%s", output)
	}
	if !strings.Contains(string(output), "missing authentication") {
		t.Fatalf("expected auth failure output, got:\n%s", output)
	}

	output, err = runworkgraph(t, repoRoot, "connectors", "status", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph connectors status failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"- github: setup error",
		"last validated",
		"validation error missing authentication",
		"polling not ready",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected github connector status to include %q, got:\n%s", expected, output)
		}
	}
}

func TestConnectorsValidateGitHubUpdatesSetupState(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	ghPath := writeFakeGH(t, tempDir, 5000)
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "connectors", "validate", "--home", homeDir, "--gh", ghPath, "github")
	if err != nil {
		t.Fatalf("workgraph connectors validate failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Connector github validation passed") {
		t.Fatalf("expected validation success output, got:\n%s", output)
	}

	output, err = runworkgraph(t, repoRoot, "connectors", "status", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph connectors status failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"- github: setup ready",
		"last validated",
		"polling enabled",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected github connector status to include %q, got:\n%s", expected, output)
		}
	}
}

func TestConnectorsValidateGitHubFailureRecordsValidationError(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	ghPath := writeFakeGHAuthFailure(t, tempDir)
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "connectors", "validate", "--home", homeDir, "--gh", ghPath, "github")
	if err == nil {
		t.Fatalf("expected connectors validate to fail, got:\n%s", output)
	}
	if !strings.Contains(string(output), "missing authentication") {
		t.Fatalf("expected auth failure output, got:\n%s", output)
	}

	output, err = runworkgraph(t, repoRoot, "connectors", "status", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph connectors status failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"- github: setup error",
		"validation error missing authentication",
		"polling not ready",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected github connector status to include %q, got:\n%s", expected, output)
		}
	}
}

func TestGitHubCaptureLinksProjectByLocalRemote(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	codeDir := filepath.Join(tempDir, "Code")
	repoDir := filepath.Join(codeDir, "Cupcake")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	createGitCommit(t, repoDir, "Add cupcake API")
	runGit(t, repoDir, "remote", "add", "origin", "https://github.com/jystringfellow/Cupcake.git")

	eventsPath := filepath.Join(tempDir, "github-events.json")
	if err := os.WriteFile(eventsPath, []byte(`[
  {
    "kind": "pull_request",
    "repository": "jystringfellow/Cupcake",
    "number": 42,
    "url": "https://github.com/jystringfellow/Cupcake/pull/42",
    "state": "open",
    "actor": "octocat",
    "title": "Add cupcake API",
    "updated_at": "2026-05-20T14:30:00Z"
  }
]`), 0o644); err != nil {
		t.Fatalf("write github events: %v", err)
	}

	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	if _, err := runworkgraph(t, repoRoot, "settings", "add-watch", "--home", homeDir, codeDir); err != nil {
		t.Fatalf("workgraph settings add-watch failed: %v", err)
	}
	if output, err := runworkgraph(t, repoRoot, "github", "capture", "--home", homeDir, "--events-file", eventsPath); err != nil {
		t.Fatalf("workgraph github capture failed: %v\n%s", err, output)
	}

	event := githubEvent(t, filepath.Join(homeDir, "workgraph.db"), "github.pull_request", "jystringfellow/Cupcake", 42)
	if event.Project != "Cupcake" {
		t.Fatalf("expected local remote project %q, got %q", "Cupcake", event.Project)
	}
}

func TestGitHubCaptureLinksProjectByCommitSHA(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	codeDir := filepath.Join(tempDir, "Code")
	repoDir := filepath.Join(codeDir, "Cupcake")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	commitSHA := createGitCommit(t, repoDir, "Add cupcake API")

	eventsPath := filepath.Join(tempDir, "github-events.json")
	if err := os.WriteFile(eventsPath, []byte(`[
  {
    "kind": "pull_request",
    "repository": "jystringfellow/not-the-local-name",
    "number": 7,
    "url": "https://github.com/jystringfellow/not-the-local-name/pull/7",
    "state": "merged",
    "actor": "octocat",
    "title": "Add cupcake API",
    "commit": "`+commitSHA+`",
    "updated_at": "2026-05-20T14:30:00Z"
  }
]`), 0o644); err != nil {
		t.Fatalf("write github events: %v", err)
	}

	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	if _, err := runworkgraph(t, repoRoot, "settings", "add-watch", "--home", homeDir, codeDir); err != nil {
		t.Fatalf("workgraph settings add-watch failed: %v", err)
	}
	if output, err := runworkgraph(t, repoRoot, "git", "capture", "--home", homeDir); err != nil {
		t.Fatalf("workgraph git capture failed: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot, "github", "capture", "--home", homeDir, "--events-file", eventsPath); err != nil {
		t.Fatalf("workgraph github capture failed: %v\n%s", err, output)
	}

	event := githubEvent(t, filepath.Join(homeDir, "workgraph.db"), "github.pull_request", "jystringfellow/not-the-local-name", 7)
	if event.Project != "Cupcake" {
		t.Fatalf("expected commit-linked project %q, got %q", "Cupcake", event.Project)
	}
}

func TestGitHubCaptureStoresIssueEvent(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	eventsPath := filepath.Join(tempDir, "github-events.json")
	if err := os.WriteFile(eventsPath, []byte(`[
  {
    "kind": "issue",
    "repository": "jystringfellow/Cupcake",
    "number": 12,
    "url": "https://github.com/jystringfellow/Cupcake/issues/12",
    "state": "open",
    "actor": "octocat",
    "title": "Bug in cupcake frosting",
    "updated_at": "2026-05-20T14:45:00Z"
  }
]`), 0o644); err != nil {
		t.Fatalf("write github events: %v", err)
	}

	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot, "github", "capture", "--home", homeDir, "--events-file", eventsPath); err != nil {
		t.Fatalf("workgraph github capture failed: %v\n%s", err, output)
	}

	event := githubEvent(t, filepath.Join(homeDir, "workgraph.db"), "github.issue", "jystringfellow/Cupcake", 12)
	if event.Project != "Cupcake" {
		t.Fatalf("expected fallback project %q, got %q", "Cupcake", event.Project)
	}
	if event.Actor != "octocat" {
		t.Fatalf("expected actor octocat, got %q", event.Actor)
	}
	if event.Summary != "Bug in cupcake frosting" {
		t.Fatalf("expected issue title summary, got %q", event.Summary)
	}
	for _, expected := range []string{
		`"repository":"jystringfellow/Cupcake"`,
		`"number":12`,
		`"url":"https://github.com/jystringfellow/Cupcake/issues/12"`,
		`"state":"open"`,
		`"actor":"octocat"`,
		`"title":"Bug in cupcake frosting"`,
	} {
		if !strings.Contains(event.PayloadJSON, expected) {
			t.Fatalf("expected payload to include %s, got %s", expected, event.PayloadJSON)
		}
	}
}

func TestGitHubCaptureRefreshesNewerWorkStateWithoutDuplicateEvent(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	eventsPath := filepath.Join(tempDir, "github-events.json")
	if err := os.WriteFile(eventsPath, []byte(`[
  {
    "kind": "issue",
    "repository": "jystringfellow/Cupcake",
    "number": 12,
    "url": "https://github.com/jystringfellow/Cupcake/issues/12",
    "state": "open",
    "actor": "octocat",
    "title": "Bug in cupcake frosting",
    "updated_at": "2026-05-20T14:45:00Z"
  }
]`), 0o644); err != nil {
		t.Fatalf("write open github events: %v", err)
	}

	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot, "github", "capture", "--home", homeDir, "--events-file", eventsPath); err != nil {
		t.Fatalf("workgraph github capture open state failed: %v\n%s", err, output)
	}

	if err := os.WriteFile(eventsPath, []byte(`[
  {
    "kind": "issue",
    "repository": "jystringfellow/Cupcake",
    "number": 12,
    "url": "https://github.com/jystringfellow/Cupcake/issues/12",
    "state": "closed",
    "actor": "dev-user",
    "title": "Fix cupcake frosting",
    "updated_at": "2026-05-21T09:00:00Z"
  }
]`), 0o644); err != nil {
		t.Fatalf("write closed github events: %v", err)
	}
	if output, err := runworkgraph(t, repoRoot, "github", "capture", "--home", homeDir, "--events-file", eventsPath); err != nil {
		t.Fatalf("workgraph github capture closed state failed: %v\n%s", err, output)
	}

	databasePath := filepath.Join(homeDir, "workgraph.db")
	event := githubEvent(t, databasePath, "github.issue", "jystringfellow/Cupcake", 12)
	for _, expected := range []string{
		`"state":"closed"`,
		`"actor":"dev-user"`,
		`"title":"Fix cupcake frosting"`,
	} {
		if !strings.Contains(event.PayloadJSON, expected) {
			t.Fatalf("expected refreshed payload to include %s, got %s", expected, event.PayloadJSON)
		}
	}
	if event.Actor != "dev-user" || event.Summary != "Fix cupcake frosting" {
		t.Fatalf("expected refreshed actor and summary, got actor %q summary %q", event.Actor, event.Summary)
	}
	if count := githubEventCount(t, databasePath); count != 1 {
		t.Fatalf("expected refreshed GitHub work to keep one event, got %d", count)
	}

	if err := os.WriteFile(eventsPath, []byte(`[
  {
    "kind": "issue",
    "repository": "jystringfellow/Cupcake",
    "number": 12,
    "url": "https://github.com/jystringfellow/Cupcake/issues/12",
    "state": "open",
    "actor": "octocat",
    "title": "Bug in cupcake frosting",
    "updated_at": "2026-05-20T14:45:00Z"
  }
]`), 0o644); err != nil {
		t.Fatalf("write stale github events: %v", err)
	}
	if output, err := runworkgraph(t, repoRoot, "github", "capture", "--home", homeDir, "--events-file", eventsPath); err != nil {
		t.Fatalf("workgraph github capture stale state failed: %v\n%s", err, output)
	}

	stillClosed := githubEvent(t, databasePath, "github.issue", "jystringfellow/Cupcake", 12)
	if !strings.Contains(stillClosed.PayloadJSON, `"state":"closed"`) {
		t.Fatalf("expected stale GitHub state not to reopen work, got %s", stillClosed.PayloadJSON)
	}

	resume, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      homeDir,
		DatabasePath: databasePath,
		Project:      "Cupcake",
		Now:          time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("resume refreshed GitHub state: %v", err)
	}
	if strings.Contains(resume.Message, "Open GitHub work") {
		t.Fatalf("expected refreshed closed GitHub work not to stay open in resume, got:\n%s", resume.Message)
	}
}

func TestGitHubPollRunsThreeBaseParticipantSearchesWithSingleUpdatedRange(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	ghPath := writeFakeGH(t, tempDir, 5000)
	connectGitHubForTest(t, homeDir, ghPath)
	dbPath := filepath.Join(homeDir, "workgraph.db")

	if _, err := workgraph.CaptureGitHubFromGH(workgraph.GitHubCaptureConfig{
		HomeDir: homeDir, DatabasePath: dbPath, GitHubCommand: ghPath,
	}); err != nil {
		t.Fatalf("github capture failed: %v", err)
	}

	logContents := readGHLog(t, tempDir)
	lines := searchInvocationLines(logContents)
	if len(lines) != 3 {
		t.Fatalf("expected exactly 3 base searches, got %d:\n%s", len(lines), logContents)
	}
	involvesPRs := countGHInvocations(logContents, "search prs ", "--involves")
	reviewRequested := countGHInvocations(logContents, "search prs ", "--review-requested")
	involvesIssues := countGHInvocations(logContents, "search issues ", "--involves")
	if involvesPRs != 1 || reviewRequested != 1 || involvesIssues != 1 {
		t.Fatalf("expected one PR-involves, one PR-review-requested, and one issue-involves search, got %d/%d/%d:\n%s", involvesPRs, reviewRequested, involvesIssues, logContents)
	}
	for _, line := range lines {
		if strings.Count(line, "--updated") != 1 {
			t.Fatalf("expected exactly one --updated range per search (scalar flag), got line:\n%s", line)
		}
	}
}

func TestGitHubPollRequestsOnlySupportedJSONFields(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	ghPath := writeFakeGH(t, tempDir, 5000)
	connectGitHubForTest(t, homeDir, ghPath)
	dbPath := filepath.Join(homeDir, "workgraph.db")

	if _, err := workgraph.CaptureGitHubFromGH(workgraph.GitHubCaptureConfig{
		HomeDir: homeDir, DatabasePath: dbPath, GitHubCommand: ghPath,
	}); err != nil {
		t.Fatalf("github capture failed: %v", err)
	}

	logContents := readGHLog(t, tempDir)
	if strings.Contains(logContents, "headRefName") || strings.Contains(logContents, "headSha") {
		t.Fatalf("gh search must not request unsupported headRefName/headSha fields:\n%s", logContents)
	}
	for _, line := range searchInvocationLines(logContents) {
		if !strings.Contains(line, "repository") {
			t.Fatalf("expected gh search --json to request repository, got line:\n%s", line)
		}
	}
}

func TestGitHubAlwaysRepositoriesAddsAtMostTwoBatchedSearches(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	ghPath := writeFakeGH(t, tempDir, 5000)
	params := `{"scope":"participant","identity":"@me","include":["involves","review_requested"],"always_repositories":["jystringfellow/Alpha","jystringfellow/Bravo"],"bootstrap_lookback":"168h"}`
	connectGitHubForTestWithParams(t, homeDir, ghPath, params)
	dbPath := filepath.Join(homeDir, "workgraph.db")

	if _, err := workgraph.CaptureGitHubFromGH(workgraph.GitHubCaptureConfig{
		HomeDir: homeDir, DatabasePath: dbPath, GitHubCommand: ghPath,
	}); err != nil {
		t.Fatalf("github capture failed: %v", err)
	}

	logContents := readGHLog(t, tempDir)
	repoBatched := 0
	for _, line := range searchInvocationLines(logContents) {
		if strings.Contains(line, "--repo jystringfellow/Alpha") && strings.Contains(line, "--repo jystringfellow/Bravo") {
			repoBatched++
		}
	}
	if repoBatched != 2 {
		t.Fatalf("expected exactly 2 batched always_repositories searches (one PR, one issue), got %d:\n%s", repoBatched, logContents)
	}
	event := githubEvent(t, dbPath, "github.pull_request", "jystringfellow/Alpha", 99)
	if event.Actor != "octocat" {
		t.Fatalf("expected always-watched repository activity to be stored, got %+v", event)
	}
}

func TestGitHubPollMergesOverlappingQueryResultsWithProvenance(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	ghPath := writeFakeGHOverlapping(t, tempDir, 5000)
	connectGitHubForTest(t, homeDir, ghPath)
	dbPath := filepath.Join(homeDir, "workgraph.db")

	if _, err := workgraph.CaptureGitHubFromGH(workgraph.GitHubCaptureConfig{
		HomeDir: homeDir, DatabasePath: dbPath, GitHubCommand: ghPath,
	}); err != nil {
		t.Fatalf("github capture failed: %v", err)
	}

	if count := githubEventCount(t, dbPath); count != 1 {
		t.Fatalf("expected overlapping results to merge into one event, got %d", count)
	}
	event := githubEvent(t, dbPath, "github.pull_request", "jystringfellow/Cupcake", 42)
	if !strings.Contains(event.PayloadJSON, `"matched_scopes":["involves","review_requested"]`) {
		t.Fatalf("expected merged provenance for both matched scopes, got %s", event.PayloadJSON)
	}
}

func TestGitHubPollFailurePreservesPreviousCursor(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	ghPath := writeFakeGH(t, tempDir, 5000)
	connectGitHubForTest(t, homeDir, ghPath)
	dbPath := filepath.Join(homeDir, "workgraph.db")

	if _, err := workgraph.CaptureGitHubFromGH(workgraph.GitHubCaptureConfig{
		HomeDir: homeDir, DatabasePath: dbPath, GitHubCommand: ghPath,
	}); err != nil {
		t.Fatalf("initial github capture failed: %v", err)
	}
	watermark, err := workgraph.CaptureWatermark(workgraph.CaptureRequestListConfig{HomeDir: homeDir, DatabasePath: dbPath, ConnectorID: "github"})
	if err != nil {
		t.Fatalf("read watermark: %v", err)
	}
	if watermark == "" {
		t.Fatalf("expected a cursor after successful capture")
	}

	failGH := writeFakeGHMalformedSearch(t, tempDir)
	if _, err := workgraph.CaptureGitHubFromGH(workgraph.GitHubCaptureConfig{
		HomeDir: homeDir, DatabasePath: dbPath, GitHubCommand: failGH,
	}); err == nil {
		t.Fatalf("expected malformed search output to fail the poll")
	}

	after, err := workgraph.CaptureWatermark(workgraph.CaptureRequestListConfig{HomeDir: homeDir, DatabasePath: dbPath, ConnectorID: "github"})
	if err != nil {
		t.Fatalf("read watermark after failure: %v", err)
	}
	if after != watermark {
		t.Fatalf("expected cursor to be preserved after failed poll, got %q want %q", after, watermark)
	}
}

func TestGitHubPollUsesBootstrapLookbackThenCursorMinusOverlap(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	ghPath := writeFakeGH(t, tempDir, 5000)
	params := `{"scope":"participant","identity":"@me","include":["involves"],"always_repositories":[],"bootstrap_lookback":"2h"}`
	connectGitHubForTestWithParams(t, homeDir, ghPath, params)
	dbPath := filepath.Join(homeDir, "workgraph.db")

	if _, err := workgraph.CaptureGitHubFromGH(workgraph.GitHubCaptureConfig{
		HomeDir: homeDir, DatabasePath: dbPath, GitHubCommand: ghPath,
	}); err != nil {
		t.Fatalf("first github capture failed: %v", err)
	}
	firstRanges := extractUpdatedRanges(t, readGHLog(t, tempDir))
	if len(firstRanges) == 0 {
		t.Fatalf("expected at least one --updated range in first capture")
	}
	firstWindow := firstRanges[0].until.Sub(firstRanges[0].since)
	if firstWindow < 110*time.Minute || firstWindow > 130*time.Minute {
		t.Fatalf("expected first capture to use the ~2h bootstrap lookback, got window %s", firstWindow)
	}

	if err := os.WriteFile(filepath.Join(tempDir, "gh.log"), nil, 0o644); err != nil {
		t.Fatalf("reset gh log: %v", err)
	}
	if _, err := workgraph.CaptureGitHubFromGH(workgraph.GitHubCaptureConfig{
		HomeDir: homeDir, DatabasePath: dbPath, GitHubCommand: ghPath,
	}); err != nil {
		t.Fatalf("second github capture failed: %v", err)
	}
	secondRanges := extractUpdatedRanges(t, readGHLog(t, tempDir))
	if len(secondRanges) == 0 {
		t.Fatalf("expected at least one --updated range in second capture")
	}
	expectedSince := firstRanges[0].until.Add(-5 * time.Minute)
	if diff := secondRanges[0].since.Sub(expectedSince); diff < -time.Second || diff > time.Second {
		t.Fatalf("expected second capture since to be first until minus 5m overlap, got %s want %s", secondRanges[0].since, expectedSince)
	}
}

func TestGitHubCaptureRequiresReconnectionForLegacyRepositoryOnlyScope(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "connectors.json"), []byte(`{
  "connectors": {
    "github": {
      "enabled": true,
      "capture_mode": "direct",
      "bridge_params": {"repositories":["jystringfellow/Cupcake"]}
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write legacy connector state: %v", err)
	}
	ghPath := writeFakeGH(t, tempDir, 5000)
	dbPath := filepath.Join(homeDir, "workgraph.db")

	_, err := workgraph.CaptureGitHubFromGH(workgraph.GitHubCaptureConfig{
		HomeDir: homeDir, DatabasePath: dbPath, GitHubCommand: ghPath,
	})
	if err == nil {
		t.Fatalf("expected legacy repository-only scope to require explicit reconnection")
	}
	if !strings.Contains(err.Error(), "reconnect") {
		t.Fatalf("expected reconnection guidance, got: %v", err)
	}
}

func TestGitHubBridgedCaptureRequestCarriesParticipantParams(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	params := `{"scope":"participant","identity":"@me","include":["involves","review_requested"],"always_repositories":["jystringfellow/Alpha"],"bootstrap_lookback":"48h"}`
	if _, err := workgraph.ConfigureBridgedConnector(workgraph.ConnectorBridgeConfig{
		HomeDir: homeDir, ID: "github", BridgeParams: json.RawMessage(params),
	}); err != nil {
		t.Fatalf("configure bridged github: %v", err)
	}
	result, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: "github"})
	if err != nil {
		t.Fatalf("emit bridged capture request: %v", err)
	}
	for _, expected := range []string{`"scope":"participant"`, `"identity":"@me"`, `"always_repositories":["jystringfellow/Alpha"]`} {
		if !strings.Contains(string(result.Request.Params), expected) {
			t.Fatalf("expected bridged request params to include %s, got %s", expected, result.Request.Params)
		}
	}
}

func readGHLog(t *testing.T, dir string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(dir, "gh.log"))
	if err != nil {
		t.Fatalf("read gh log: %v", err)
	}
	return string(contents)
}

func searchInvocationLines(log string) []string {
	var lines []string
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "search prs ") || strings.HasPrefix(line, "search issues ") {
			lines = append(lines, line)
		}
	}
	return lines
}

type githubUpdatedRange struct {
	since time.Time
	until time.Time
}

func extractUpdatedRanges(t *testing.T, log string) []githubUpdatedRange {
	t.Helper()
	var ranges []githubUpdatedRange
	for _, line := range searchInvocationLines(log) {
		fields := strings.Fields(line)
		for i, field := range fields {
			if field == "--updated" && i+1 < len(fields) {
				parts := strings.SplitN(fields[i+1], "..", 2)
				if len(parts) != 2 {
					continue
				}
				since, err := time.Parse(time.RFC3339, parts[0])
				if err != nil {
					continue
				}
				until, err := time.Parse(time.RFC3339, parts[1])
				if err != nil {
					continue
				}
				ranges = append(ranges, githubUpdatedRange{since: since, until: until})
			}
		}
	}
	return ranges
}

func TestRunCapturesGitHubPullRequestsThroughGHCLI(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	codeDir := filepath.Join(tempDir, "Code")
	repoDir := filepath.Join(codeDir, "Cupcake")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	createGitCommit(t, repoDir, "Add cupcake API")
	runGit(t, repoDir, "remote", "add", "origin", "https://github.com/jystringfellow/Cupcake.git")
	ghPath := writeFakeGH(t, tempDir, 5000)

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	connectGitHubForTest(t, homeDir, ghPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:            homeDir,
		DatabasePath:       initResult.DatabasePath,
		WatchDirs:          []string{codeDir},
		GitHubPollInterval: 20 * time.Millisecond,
		GitHubCommand:      ghPath,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()


	waitForGitHubEvent(t, initResult.DatabasePath, "github.pull_request", "jystringfellow/Cupcake", 42)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunSkipsGitHubPollingWhenRateLimitIsLow(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	codeDir := filepath.Join(tempDir, "Code")
	repoDir := filepath.Join(codeDir, "Cupcake")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	createGitCommit(t, repoDir, "Add cupcake API")
	runGit(t, repoDir, "remote", "add", "origin", "https://github.com/jystringfellow/Cupcake.git")
	ghPath := writeFakeGH(t, tempDir, 10)

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	connectGitHubForTest(t, homeDir, ghPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:            homeDir,
		DatabasePath:       initResult.DatabasePath,
		WatchDirs:          []string{codeDir},
		GitHubPollInterval: 20 * time.Millisecond,
		GitHubCommand:      ghPath,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	time.Sleep(120 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if count := githubEventCount(t, initResult.DatabasePath); count != 0 {
		t.Fatalf("expected no github events while rate limited, got %d", count)
	}
	logContents, err := os.ReadFile(filepath.Join(tempDir, "gh.log"))
	if err != nil {
		t.Fatalf("read gh log: %v", err)
	}
	if strings.Contains(string(logContents), "search prs") || strings.Contains(string(logContents), "search issues") {
		t.Fatalf("expected low rate limit to skip repository activity queries, got log:\n%s", logContents)
	}
}

type storedGitHubEvent struct {
	Project     string
	Actor       string
	Summary     string
	PayloadJSON string
}

func writeFakeGH(t *testing.T, dir string, remaining int) string {
	t.Helper()

	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> "` + filepath.Join(dir, "gh.log") + `"
if [ "$1" = "api" ] && [ "$2" = "rate_limit" ]; then
  printf '{"resources":{"core":{"remaining":` + fmtInt(remaining) + `}}}'
  exit 0
fi
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then
  printf 'github.com\n  Logged in\n'
  exit 0
fi
if [ "$1" = "search" ] && [ "$2" = "prs" ]; then
  case "$*" in
    *--review-requested*)
      printf '[]'
      ;;
    *--repo*)
      printf '[{"number":99,"url":"https://github.com/jystringfellow/Alpha/pull/99","state":"open","author":{"login":"octocat"},"title":"Always-watched work","updatedAt":"2026-05-20T14:30:00Z","repository":{"nameWithOwner":"jystringfellow/Alpha"}}]'
      ;;
    *)
      printf '[{"number":42,"url":"https://github.com/jystringfellow/Cupcake/pull/42","state":"open","author":{"login":"octocat"},"title":"Add cupcake API","updatedAt":"2026-05-20T14:30:00Z","repository":{"nameWithOwner":"jystringfellow/Cupcake"}}]'
      ;;
  esac
  exit 0
fi
if [ "$1" = "search" ] && [ "$2" = "issues" ]; then
  printf '[]'
  exit 0
fi
printf 'unexpected gh args: %s\n' "$*" >&2
exit 1
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gh.log"), nil, 0o644); err != nil {
		t.Fatalf("write gh log: %v", err)
	}
	return path
}

// writeFakeGHOverlapping returns the same PR from both the involves and
// review-requested searches so merge behavior can be verified.
func writeFakeGHOverlapping(t *testing.T, dir string, remaining int) string {
	t.Helper()
	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> "` + filepath.Join(dir, "gh.log") + `"
if [ "$1" = "api" ] && [ "$2" = "rate_limit" ]; then
  printf '{"resources":{"core":{"remaining":` + fmtInt(remaining) + `}}}'
  exit 0
fi
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then
  printf 'github.com\n  Logged in\n'
  exit 0
fi
if [ "$1" = "search" ] && [ "$2" = "prs" ]; then
  printf '[{"number":42,"url":"https://github.com/jystringfellow/Cupcake/pull/42","state":"open","author":{"login":"octocat"},"title":"Add cupcake API","updatedAt":"2026-05-20T14:30:00Z","repository":{"nameWithOwner":"jystringfellow/Cupcake"}}]'
  exit 0
fi
if [ "$1" = "search" ] && [ "$2" = "issues" ]; then
  printf '[]'
  exit 0
fi
printf 'unexpected gh args: %s\n' "$*" >&2
exit 1
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh overlapping: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gh.log"), nil, 0o644); err != nil {
		t.Fatalf("write gh log: %v", err)
	}
	return path
}

// writeFakeGHMalformedSearch fails auth/rate-limit checks but returns invalid
// JSON for a search query so poll failures can be verified.
func writeFakeGHMalformedSearch(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "gh-fail")
	script := `#!/bin/sh
echo "$@" >> "` + filepath.Join(dir, "gh.log") + `"
if [ "$1" = "api" ] && [ "$2" = "rate_limit" ]; then
  printf '{"resources":{"core":{"remaining":5000}}}'
  exit 0
fi
if [ "$1" = "search" ]; then
  printf 'not-json'
  exit 0
fi
printf 'unexpected gh args: %s\n' "$*" >&2
exit 1
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh malformed search: %v", err)
	}
	return path
}

func countGHInvocations(log string, prefix string, flag string) int {
	count := 0
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, prefix) && strings.Contains(line, flag) {
			count++
		}
	}
	return count
}

func connectGitHubForTest(t *testing.T, homeDir string, ghPath string) {
	t.Helper()
	connectGitHubForTestWithParams(t, homeDir, ghPath, "")
}

func connectGitHubForTestWithParams(t *testing.T, homeDir string, ghPath string, paramsJSON string) {
	t.Helper()

	if _, err := workgraph.ConnectGitHub(workgraph.ConnectorConnectConfig{
		HomeDir:       homeDir,
		GitHubCommand: ghPath,
		ParamsJSON:    json.RawMessage(paramsJSON),
	}); err != nil {
		t.Fatalf("connect github for test: %v", err)
	}
}

func writeFakeGHAuthFailure(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, "gh")
	script := `#!/bin/sh
echo "$@" >> "` + filepath.Join(dir, "gh.log") + `"
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then
  printf 'missing authentication\n' >&2
  exit 1
fi
printf 'unexpected gh args: %s\n' "$*" >&2
exit 1
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh auth failure: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gh.log"), nil, 0o644); err != nil {
		t.Fatalf("write gh log: %v", err)
	}
	return path
}

func fmtInt(value int) string {
	return strconv.Itoa(value)
}


func waitForGitHubEvent(t *testing.T, dbPath, eventType, repository string, number int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if githubEventExists(t, dbPath, eventType, repository, number) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for github event %s %s#%d", eventType, repository, number)
}

func githubEventExists(t *testing.T, dbPath, eventType, repository string, number int) bool {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var count int
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM events
		WHERE source = 'github'
			AND type = ?
			AND json_extract(payload_json, '$.repository') = ?
			AND json_extract(payload_json, '$.number') = ?
	`, eventType, repository, number).Scan(&count)
	if err != nil {
		t.Fatalf("query github event count: %v", err)
	}
	return count > 0
}

func githubEventCount(t *testing.T, dbPath string) int {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE source = 'github'`).Scan(&count); err != nil {
		t.Fatalf("query github event count: %v", err)
	}
	return count
}

func githubEvent(t *testing.T, dbPath, eventType, repository string, number int) storedGitHubEvent {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var event storedGitHubEvent
	err = db.QueryRow(`
		SELECT project, actor, summary, payload_json
		FROM events
		WHERE source = 'github'
			AND type = ?
			AND json_extract(payload_json, '$.repository') = ?
			AND json_extract(payload_json, '$.number') = ?
	`, eventType, repository, number).Scan(&event.Project, &event.Actor, &event.Summary, &event.PayloadJSON)
	if err != nil {
		t.Fatalf("query github event: %v", err)
	}
	return event
}
