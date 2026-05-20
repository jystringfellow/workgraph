package workgraph

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// GitCaptureConfig controls local git commit capture.
type GitCaptureConfig struct {
	HomeDir      string
	DatabasePath string
	WatchDirs    []string
	MaxCommits   int
}

// GitCaptureResult describes a local git capture run.
type GitCaptureResult struct {
	HomeDir       string
	DatabasePath  string
	Repositories  []string
	CommitsStored int
	Events        []CapturedEvent
	Message       string
}

type gitCommitPayload struct {
	RepoPath    string `json:"repo_path"`
	Commit      string `json:"commit"`
	Branch      string `json:"branch,omitempty"`
	Subject     string `json:"subject"`
	AuthorName  string `json:"author_name,omitempty"`
	AuthorEmail string `json:"author_email,omitempty"`
}

type gitCommit struct {
	SHA         string
	UnixTime    int64
	AuthorName  string
	AuthorEmail string
	Subject     string
}

// CaptureGitCommits scans configured watch roots and stores local git commits.
func CaptureGitCommits(config GitCaptureConfig) (GitCaptureResult, error) {
	status, err := prepareGitCaptureStatus(config)
	if err != nil {
		return GitCaptureResult{}, err
	}

	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return GitCaptureResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return GitCaptureResult{}, fmt.Errorf("open database: %w", err)
	}

	repos, err := findGitRepositories(status.WatchDirs, status.HomeDir, status.DatabasePath, status.IgnorePaths, status.IgnoreNames)
	if err != nil {
		return GitCaptureResult{}, err
	}

	maxCommits := config.MaxCommits
	if maxCommits <= 0 {
		maxCommits = 50
	}

	stored := 0
	var events []CapturedEvent
	for _, repo := range repos {
		branch := gitBranch(repo)
		commits, err := gitRecentCommits(repo, maxCommits)
		if err != nil {
			continue
		}
		for _, commit := range commits {
			inserted, event, err := storeGitCommit(db, repo, branch, commit)
			if err != nil {
				return GitCaptureResult{}, err
			}
			if inserted {
				stored++
				events = append(events, event)
			}
		}
	}

	result := GitCaptureResult{
		HomeDir:       status.HomeDir,
		DatabasePath:  status.DatabasePath,
		Repositories:  repos,
		CommitsStored: stored,
		Events:        events,
	}
	result.Message = gitCaptureMessage(result)
	return result, nil
}

func prepareGitCaptureStatus(config GitCaptureConfig) (RunStatus, error) {
	status, err := prepareRunStatus(RunConfig{
		HomeDir:      config.HomeDir,
		DatabasePath: config.DatabasePath,
		WatchDirs:    config.WatchDirs,
	})
	if err != nil {
		return RunStatus{}, err
	}
	return status, nil
}

func findGitRepositories(watchDirs []string, homeDir, dbPath string, ignorePaths []string, ignoreNames []string) ([]string, error) {
	seen := map[string]bool{}
	var repos []string
	for _, watchDir := range watchDirs {
		err := filepath.WalkDir(watchDir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				if isPermissionError(err) || isUnsupportedSpecialFileError(err) {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.IsDir() {
				return nil
			}
			if entry.Name() == ".git" {
				repoPath := filepath.Dir(path)
				if !seen[repoPath] {
					seen[repoPath] = true
					repos = append(repos, repoPath)
				}
				return filepath.SkipDir
			}
			if shouldIgnorePath(path, homeDir, dbPath, ignorePaths, ignoreNames) {
				return filepath.SkipDir
			}
			return nil
		})
		if err != nil && !errors.Is(err, filepath.SkipDir) {
			return nil, fmt.Errorf("scan git repositories under %q: %w", watchDir, err)
		}
	}

	sort.Strings(repos)
	return repos, nil
}

func gitBranch(repoPath string) string {
	output, err := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func gitRecentCommits(repoPath string, maxCommits int) ([]gitCommit, error) {
	format := "%H%x1f%ct%x1f%an%x1f%ae%x1f%s%x1e"
	output, err := exec.Command("git", "-C", repoPath, "log", "-n", strconv.Itoa(maxCommits), "--format="+format).Output()
	if err != nil {
		return nil, err
	}

	records := strings.Split(string(output), "\x1e")
	commits := make([]gitCommit, 0, len(records))
	for _, record := range records {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		fields := strings.Split(record, "\x1f")
		if len(fields) != 5 {
			continue
		}
		unixTime, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		commits = append(commits, gitCommit{
			SHA:         fields[0],
			UnixTime:    unixTime,
			AuthorName:  fields[2],
			AuthorEmail: fields[3],
			Subject:     fields[4],
		})
	}
	return commits, nil
}

func storeGitCommit(db *sql.DB, repoPath, branch string, commit gitCommit) (bool, CapturedEvent, error) {
	payload, err := json.Marshal(gitCommitPayload{
		RepoPath:    repoPath,
		Commit:      commit.SHA,
		Branch:      branch,
		Subject:     commit.Subject,
		AuthorName:  commit.AuthorName,
		AuthorEmail: commit.AuthorEmail,
	})
	if err != nil {
		return false, CapturedEvent{}, fmt.Errorf("encode git commit event: %w", err)
	}
	project := filepath.Base(repoPath)

	result, err := db.Exec(`INSERT OR IGNORE INTO events
		(id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"git.commit:"+commit.SHA,
		"git",
		"git.commit",
		time.Unix(commit.UnixTime, 0).UTC().Format(time.RFC3339Nano),
		string(payload),
		project,
		commit.AuthorEmail,
		commit.Subject,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return false, CapturedEvent{}, fmt.Errorf("store git commit event: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, CapturedEvent{}, fmt.Errorf("store git commit event: %w", err)
	}
	event := CapturedEvent{
		Type:    "git.commit",
		Project: project,
		Summary: commit.Subject,
	}
	return rows > 0, event, nil
}

func gitCaptureMessage(result GitCaptureResult) string {
	lines := []string{
		"Git capture complete",
		"Home: " + result.HomeDir,
		"Database: " + result.DatabasePath,
		fmt.Sprintf("Repositories: %d", len(result.Repositories)),
		fmt.Sprintf("Commits stored: %d", result.CommitsStored),
	}
	return strings.Join(lines, "\n")
}
