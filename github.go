package workgraph

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const maxGitHubReposPerPoll = 25

// GitHubCaptureConfig controls GitHub event ingestion.
type GitHubCaptureConfig struct {
	HomeDir       string
	DatabasePath  string
	WatchDirs     []string
	EventsFile    string
	GitHubCommand string
}

// GitHubCaptureResult describes a GitHub capture run.
type GitHubCaptureResult struct {
	HomeDir      string
	DatabasePath string
	EventsStored int
	Message      string
}

type githubExportEvent struct {
	Kind       string `json:"kind"`
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
	State      string `json:"state"`
	Actor      string `json:"actor"`
	Title      string `json:"title"`
	Branch     string `json:"branch,omitempty"`
	Commit     string `json:"commit,omitempty"`
	UpdatedAt  string `json:"updated_at"`
}

type githubEventPayload struct {
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
	State      string `json:"state"`
	Actor      string `json:"actor"`
	Title      string `json:"title"`
	Branch     string `json:"branch,omitempty"`
	Commit     string `json:"commit,omitempty"`
}

type githubRateLimit struct {
	Resources struct {
		Core struct {
			Remaining int `json:"remaining"`
		} `json:"core"`
	} `json:"resources"`
}

type githubSearchItem struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Title       string `json:"title"`
	HeadRefName string `json:"headRefName"`
	HeadSHA     string `json:"headSha"`
	UpdatedAt   string `json:"updatedAt"`
}

// CaptureGitHubEvents stores GitHub events from a local export file.
func CaptureGitHubEvents(config GitHubCaptureConfig) (GitHubCaptureResult, error) {
	status, err := prepareRunStatus(RunConfig{
		HomeDir:      config.HomeDir,
		DatabasePath: config.DatabasePath,
		WatchDirs:    config.WatchDirs,
	})
	if err != nil {
		return GitHubCaptureResult{}, err
	}
	if config.EventsFile == "" {
		return GitHubCaptureResult{}, errors.New("events file is required")
	}

	eventsFile, err := filepath.Abs(config.EventsFile)
	if err != nil {
		return GitHubCaptureResult{}, fmt.Errorf("resolve events file: %w", err)
	}
	contents, err := os.ReadFile(eventsFile)
	if err != nil {
		return GitHubCaptureResult{}, fmt.Errorf("read events file: %w", err)
	}
	var exported []githubExportEvent
	if err := json.Unmarshal(contents, &exported); err != nil {
		return GitHubCaptureResult{}, fmt.Errorf("parse events file: %w", err)
	}

	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return GitHubCaptureResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return GitHubCaptureResult{}, fmt.Errorf("open database: %w", err)
	}

	remoteProjects := githubRemoteProjects(status.WatchDirs, status.HomeDir, status.DatabasePath, status.IgnorePaths, status.IgnoreNames)
	stored := 0
	for _, event := range exported {
		inserted, err := storeGitHubEvent(db, event, inferGitHubProject(db, event, remoteProjects))
		if err != nil {
			return GitHubCaptureResult{}, err
		}
		if inserted {
			stored++
		}
	}

	result := GitHubCaptureResult{
		HomeDir:      status.HomeDir,
		DatabasePath: status.DatabasePath,
		EventsStored: stored,
	}
	result.Message = githubCaptureMessage(result)
	return result, nil
}

// CaptureGitHubFromGH stores GitHub events discovered through the GitHub CLI.
func CaptureGitHubFromGH(config GitHubCaptureConfig) (GitHubCaptureResult, error) {
	status, err := prepareRunStatus(RunConfig{
		HomeDir:      config.HomeDir,
		DatabasePath: config.DatabasePath,
		WatchDirs:    config.WatchDirs,
	})
	if err != nil {
		return GitHubCaptureResult{}, err
	}

	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return GitHubCaptureResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return GitHubCaptureResult{}, fmt.Errorf("open database: %w", err)
	}

	gh := config.GitHubCommand
	if gh == "" {
		gh = "gh"
	}
	if !githubRateLimitAllowsPolling(gh) {
		return GitHubCaptureResult{HomeDir: status.HomeDir, DatabasePath: status.DatabasePath}, nil
	}

	remoteProjects := githubRemoteProjects(status.WatchDirs, status.HomeDir, status.DatabasePath, status.IgnorePaths, status.IgnoreNames)
	stored := 0
	for i, remote := range githubRemoteProjectEntries(status.WatchDirs, status.HomeDir, status.DatabasePath, status.IgnorePaths, status.IgnoreNames) {
		if i >= maxGitHubReposPerPoll {
			break
		}
		events := githubEventsFromGH(gh, remote.Repository)
		for _, event := range events {
			inserted, err := storeGitHubEvent(db, event, inferGitHubProject(db, event, remoteProjects))
			if err != nil {
				return GitHubCaptureResult{}, err
			}
			if inserted {
				stored++
			}
		}
	}

	result := GitHubCaptureResult{
		HomeDir:      status.HomeDir,
		DatabasePath: status.DatabasePath,
		EventsStored: stored,
	}
	result.Message = githubCaptureMessage(result)
	return result, nil
}

func githubRateLimitAllowsPolling(gh string) bool {
	output, err := exec.Command(gh, "api", "rate_limit").Output()
	if err != nil {
		return false
	}
	var limit githubRateLimit
	if err := json.Unmarshal(output, &limit); err != nil {
		return false
	}
	return limit.Resources.Core.Remaining >= 100
}

func githubEventsFromGH(gh string, repository string) []githubExportEvent {
	var events []githubExportEvent
	events = append(events, githubPullRequestsFromGH(gh, repository)...)
	events = append(events, githubIssuesFromGH(gh, repository)...)
	return events
}

func githubPullRequestsFromGH(gh string, repository string) []githubExportEvent {
	output, err := exec.Command(gh, "search", "prs", "--repo", repository, "--json", "number,url,state,author,title,headRefName,headSha,updatedAt", "--limit", "20").Output()
	if err != nil {
		return nil
	}
	var items []githubSearchItem
	if err := json.Unmarshal(output, &items); err != nil {
		return nil
	}
	events := make([]githubExportEvent, 0, len(items))
	for _, item := range items {
		events = append(events, githubExportEvent{
			Kind:       "pull_request",
			Repository: repository,
			Number:     item.Number,
			URL:        item.URL,
			State:      strings.ToLower(item.State),
			Actor:      item.Author.Login,
			Title:      item.Title,
			Branch:     item.HeadRefName,
			Commit:     item.HeadSHA,
			UpdatedAt:  item.UpdatedAt,
		})
	}
	return events
}

func githubIssuesFromGH(gh string, repository string) []githubExportEvent {
	output, err := exec.Command(gh, "search", "issues", "--repo", repository, "--json", "number,url,state,author,title,updatedAt", "--limit", "20").Output()
	if err != nil {
		return nil
	}
	var items []githubSearchItem
	if err := json.Unmarshal(output, &items); err != nil {
		return nil
	}
	events := make([]githubExportEvent, 0, len(items))
	for _, item := range items {
		events = append(events, githubExportEvent{
			Kind:       "issue",
			Repository: repository,
			Number:     item.Number,
			URL:        item.URL,
			State:      strings.ToLower(item.State),
			Actor:      item.Author.Login,
			Title:      item.Title,
			UpdatedAt:  item.UpdatedAt,
		})
	}
	return events
}

func storeGitHubEvent(db *sql.DB, event githubExportEvent, project string) (bool, error) {
	eventType := githubEventType(event.Kind)
	if eventType == "" {
		return false, nil
	}

	payload, err := json.Marshal(githubEventPayload{
		Repository: event.Repository,
		Number:     event.Number,
		URL:        event.URL,
		State:      event.State,
		Actor:      event.Actor,
		Title:      event.Title,
		Branch:     event.Branch,
		Commit:     event.Commit,
	})
	if err != nil {
		return false, fmt.Errorf("encode github event: %w", err)
	}

	timestamp := time.Now().UTC()
	if event.UpdatedAt != "" {
		parsed, err := time.Parse(time.RFC3339Nano, event.UpdatedAt)
		if err != nil {
			return false, fmt.Errorf("parse github event timestamp: %w", err)
		}
		timestamp = parsed
	}

	result, err := db.Exec(`INSERT OR IGNORE INTO events
		(id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		fmt.Sprintf("%s:%s:%d", eventType, event.Repository, event.Number),
		"github",
		eventType,
		timestamp.UTC().Format(time.RFC3339Nano),
		string(payload),
		project,
		event.Actor,
		event.Title,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return false, fmt.Errorf("store github event: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store github event: %w", err)
	}
	return rows > 0, nil
}

func githubEventType(kind string) string {
	switch kind {
	case "pull_request":
		return "github.pull_request"
	case "issue":
		return "github.issue"
	default:
		return ""
	}
}

func inferGitHubProject(db *sql.DB, event githubExportEvent, remoteProjects map[string]string) string {
	if project := remoteProjects[strings.ToLower(event.Repository)]; project != "" {
		return project
	}
	if event.Commit != "" {
		if project := projectForCommit(db, event.Commit); project != "" {
			return project
		}
	}
	parts := strings.Split(event.Repository, "/")
	if len(parts) == 2 && parts[1] != "" {
		return parts[1]
	}
	return event.Repository
}

func projectForCommit(db *sql.DB, commit string) string {
	var project string
	err := db.QueryRow(`
		SELECT project
		FROM events
		WHERE source = 'git'
			AND type = 'git.commit'
			AND json_extract(payload_json, '$.commit') = ?
		LIMIT 1
	`, commit).Scan(&project)
	if err != nil {
		return ""
	}
	return project
}

func githubRemoteProjects(watchDirs []string, homeDir, dbPath string, ignorePaths []string, ignoreNames []string) map[string]string {
	projects := map[string]string{}
	for _, entry := range githubRemoteProjectEntries(watchDirs, homeDir, dbPath, ignorePaths, ignoreNames) {
		projects[strings.ToLower(entry.Repository)] = entry.Project
	}
	return projects
}

type githubRemoteProjectEntry struct {
	Repository string
	Project    string
}

func githubRemoteProjectEntries(watchDirs []string, homeDir, dbPath string, ignorePaths []string, ignoreNames []string) []githubRemoteProjectEntry {
	repos, err := findGitRepositories(watchDirs, homeDir, dbPath, ignorePaths, ignoreNames)
	if err != nil {
		return nil
	}

	var entries []githubRemoteProjectEntry
	for _, repo := range repos {
		output, err := exec.Command("git", "-C", repo, "remote", "get-url", "origin").Output()
		if err != nil {
			continue
		}
		remote := normalizeGitHubRepository(strings.TrimSpace(string(output)))
		if remote == "" {
			continue
		}
		entries = append(entries, githubRemoteProjectEntry{
			Repository: remote,
			Project:    filepath.Base(repo),
		})
	}
	return entries
}

func normalizeGitHubRepository(remote string) string {
	remote = strings.TrimSuffix(remote, ".git")
	if strings.HasPrefix(remote, "git@github.com:") {
		return strings.TrimPrefix(remote, "git@github.com:")
	}
	parsed, err := url.Parse(remote)
	if err != nil {
		return ""
	}
	if parsed.Host != "github.com" {
		return ""
	}
	return strings.TrimPrefix(parsed.Path, "/")
}

func githubCaptureMessage(result GitHubCaptureResult) string {
	lines := []string{
		"GitHub capture complete",
		"Home: " + result.HomeDir,
		"Database: " + result.DatabasePath,
		fmt.Sprintf("Events stored: %d", result.EventsStored),
	}
	return strings.Join(lines, "\n")
}
