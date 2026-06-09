package facts

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestGitCaptureStoresLocalCommitEvent(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	codeDir := filepath.Join(tempDir, "Code")
	repoDir := filepath.Join(codeDir, "Cupcake")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	commitSHA := createGitCommit(t, repoDir, "Add cupcake API")

	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	if _, err := runworkgraph(t, repoRoot, "config", "add-watch", "--home", homeDir, codeDir); err != nil {
		t.Fatalf("workgraph config add-watch failed: %v", err)
	}

	output, err := runworkgraph(t, repoRoot, "git", "capture", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph git capture failed: %v\n%s", err, output)
	}

	dbPath := filepath.Join(homeDir, "workgraph.db")
	event := gitCommitEvent(t, dbPath, commitSHA)
	if event.Project != "Cupcake" {
		t.Fatalf("expected project %q, got %q", "Cupcake", event.Project)
	}
	if event.Actor != "dev@example.test" {
		t.Fatalf("expected actor email, got %q", event.Actor)
	}
	if event.Summary != "Add cupcake API" {
		t.Fatalf("expected summary subject, got %q", event.Summary)
	}
	for _, expected := range []string{
		`"repo_path":"` + repoDir + `"`,
		`"commit":"` + commitSHA + `"`,
		`"branch":"main"`,
		`"subject":"Add cupcake API"`,
		`"author_name":"Dev User"`,
		`"author_email":"dev@example.test"`,
	} {
		if !strings.Contains(event.PayloadJSON, expected) {
			t.Fatalf("expected payload to include %s, got %s", expected, event.PayloadJSON)
		}
	}
	if !strings.Contains(string(output), "Git capture complete") {
		t.Fatalf("expected capture summary, got:\n%s", output)
	}
}

func TestGitConnectEnablesSharedConnectorPolling(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "git", "connect", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph git connect failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Git connected") {
		t.Fatalf("expected git connect output, got:\n%s", output)
	}
	if !strings.Contains(string(output), "workgraph connectors disable git") {
		t.Fatalf("expected disable guidance, got:\n%s", output)
	}
	if !strings.Contains(string(output), "workgraph connectors interval git") {
		t.Fatalf("expected interval guidance, got:\n%s", output)
	}

	output, err = runworkgraph(t, repoRoot, "connectors", "list", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph connectors list failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "- git: connected, enabled") {
		t.Fatalf("expected enabled git connector, got:\n%s", output)
	}
}

func TestGitCaptureDoesNotDuplicateCommitEvents(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	codeDir := filepath.Join(tempDir, "Code")
	repoDir := filepath.Join(codeDir, "Cupcake")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	commitSHA := createGitCommit(t, repoDir, "Add cupcake API")

	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	if _, err := runworkgraph(t, repoRoot, "config", "add-watch", "--home", homeDir, codeDir); err != nil {
		t.Fatalf("workgraph config add-watch failed: %v", err)
	}

	if output, err := runworkgraph(t, repoRoot, "git", "capture", "--home", homeDir); err != nil {
		t.Fatalf("first git capture failed: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot, "git", "capture", "--home", homeDir); err != nil {
		t.Fatalf("second git capture failed: %v\n%s", err, output)
	}

	count := gitCommitEventCount(t, filepath.Join(homeDir, "workgraph.db"), commitSHA)
	if count != 1 {
		t.Fatalf("expected one git commit event for %s, got %d", commitSHA, count)
	}
}

func TestRunCapturesGitCommitsWhileActive(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	codeDir := filepath.Join(tempDir, "Code")
	repoDir := filepath.Join(codeDir, "Cupcake")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	initGitRepo(t, repoDir)

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
		HomeDir:         homeDir,
		DatabasePath:    initResult.DatabasePath,
		WatchDirs:       []string{codeDir},
		GitPollInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	commitSHA := commitGitFile(t, repoDir, "README.md", "# Cupcake\n", "Add cupcake API")
	waitForGitCommitEvent(t, initResult.DatabasePath, commitSHA)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunReportsGitCommitsWhileForegroundCaptureIsActive(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	codeDir := filepath.Join(tempDir, "Code")
	repoDir := filepath.Join(codeDir, "Cupcake")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	initGitRepo(t, repoDir)

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
		HomeDir:         homeDir,
		DatabasePath:    initResult.DatabasePath,
		WatchDirs:       []string{codeDir},
		GitPollInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	commitGitFile(t, repoDir, "README.md", "# Cupcake\n", "Add cupcake API")
	event := waitForCapturedEvent(t, capture.Events, "git.commit", "Add cupcake API")
	if event.Project != "Cupcake" {
		t.Fatalf("expected captured git project Cupcake, got %q", event.Project)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

type storedGitCommitEvent struct {
	Project     string
	Actor       string
	Summary     string
	PayloadJSON string
}

func runworkgraph(t *testing.T, repoRoot string, args ...string) ([]byte, error) {
	t.Helper()

	cmd := exec.Command("go", append([]string{"run", "./cmd/workgraph"}, args...)...)
	cmd.Dir = repoRoot
	return cmd.CombinedOutput()
}

func createGitCommit(t *testing.T, repoDir, subject string) string {
	t.Helper()

	initGitRepo(t, repoDir)
	return commitGitFile(t, repoDir, "README.md", "# Cupcake\n", subject)
}

func initGitRepo(t *testing.T, repoDir string) {
	t.Helper()

	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir, "config", "user.name", "Dev User")
	runGit(t, repoDir, "config", "user.email", "dev@example.test")
}

func commitGitFile(t *testing.T, repoDir, name, contents, subject string) string {
	t.Helper()

	if err := os.WriteFile(filepath.Join(repoDir, name), []byte(contents), 0o644); err != nil {
		t.Fatalf("write committed file: %v", err)
	}
	runGit(t, repoDir, "add", name)
	runGitWithEnv(t, repoDir, []string{
		"GIT_AUTHOR_DATE=2026-05-20T10:00:00-07:00",
		"GIT_COMMITTER_DATE=2026-05-20T10:00:00-07:00",
	}, "commit", "-m", subject)
	return strings.TrimSpace(string(runGit(t, repoDir, "rev-parse", "HEAD")))
}

func waitForGitCommitEvent(t *testing.T, dbPath, commitSHA string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if gitCommitEventCount(t, dbPath, commitSHA) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for git commit event %s", commitSHA)
}

func waitForCapturedEvent(t *testing.T, events <-chan workgraph.CapturedEvent, eventType, summary string) workgraph.CapturedEvent {
	t.Helper()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Type == eventType && event.Summary == summary {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for captured %s event %q", eventType, summary)
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	return runGitWithEnv(t, dir, nil, args...)
}

func runGitWithEnv(t *testing.T, dir string, env []string, args ...string) []byte {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return output
}

func gitCommitEvent(t *testing.T, dbPath, commitSHA string) storedGitCommitEvent {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var event storedGitCommitEvent
	err = db.QueryRow(`
		SELECT project, actor, summary, payload_json
		FROM events
		WHERE source = 'git'
			AND type = 'git.commit'
			AND json_extract(payload_json, '$.commit') = ?
	`, commitSHA).Scan(&event.Project, &event.Actor, &event.Summary, &event.PayloadJSON)
	if err != nil {
		t.Fatalf("query git commit event: %v", err)
	}
	return event
}

func gitCommitEventCount(t *testing.T, dbPath, commitSHA string) int {
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
		WHERE source = 'git'
			AND type = 'git.commit'
			AND json_extract(payload_json, '$.commit') = ?
	`, commitSHA).Scan(&count)
	if err != nil {
		t.Fatalf("query git commit count: %v", err)
	}
	return count
}
