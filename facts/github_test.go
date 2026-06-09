package facts

import (
	"context"
	"database/sql"
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
	if _, err := runworkgraph(t, repoRoot, "config", "add-watch", "--home", homeDir, codeDir); err != nil {
		t.Fatalf("workgraph config add-watch failed: %v", err)
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
	if _, err := runworkgraph(t, repoRoot, "config", "add-watch", "--home", homeDir, codeDir); err != nil {
		t.Fatalf("workgraph config add-watch failed: %v", err)
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

func TestRunBoundsGitHubRepositoryQueriesPerPoll(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	codeDir := filepath.Join(tempDir, "Code")
	for i := 0; i < 30; i++ {
		repoName := "Repo" + strconv.Itoa(i)
		repoDir := filepath.Join(codeDir, repoName)
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatalf("create repo dir: %v", err)
		}
		createGitCommit(t, repoDir, "Initial commit")
		runGit(t, repoDir, "remote", "add", "origin", "https://github.com/jystringfellow/"+repoName+".git")
	}
	ghPath := writeFakeGH(t, tempDir, 5000)

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

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

	time.Sleep(60 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	logContents, err := os.ReadFile(filepath.Join(tempDir, "gh.log"))
	if err != nil {
		t.Fatalf("read gh log: %v", err)
	}
	prQueries := uniquePRQueryRepos(string(logContents))
	if len(prQueries) > 25 {
		t.Fatalf("expected at most 25 repositories queried, got %d: %v\n%s", len(prQueries), prQueries, logContents)
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
  printf '[{"number":42,"url":"https://github.com/jystringfellow/Cupcake/pull/42","state":"open","author":{"login":"octocat"},"title":"Add cupcake API","headRefName":"feature/cupcake-api","headSha":"abcdef1234567890","updatedAt":"2026-05-20T14:30:00Z"}]'
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

func fmtInt(value int) string {
	return strconv.Itoa(value)
}

func uniquePRQueryRepos(log string) map[string]bool {
	repos := map[string]bool{}
	for _, line := range strings.Split(log, "\n") {
		if !strings.HasPrefix(line, "search prs ") {
			continue
		}
		fields := strings.Fields(line)
		for i, field := range fields {
			if field == "--repo" && i+1 < len(fields) {
				repos[fields[i+1]] = true
			}
		}
	}
	return repos
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
