package workgraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const (
	defaultGitHubBootstrapLookback = 168 * time.Hour
	githubCaptureOverlap           = 5 * time.Minute
	githubSearchResultCap          = 1000
	githubMinBisectWindow          = time.Minute
)

type GitHubCaptureConfig struct {
	HomeDir       string
	DatabasePath  string
	WatchDirs     []string
	EventsFile    string
	GitHubCommand string
	Context       context.Context
}

type GitHubCaptureResult struct {
	HomeDir      string
	DatabasePath string
	EventsStored int
	Message      string
}

type githubExportEvent struct {
	Kind          string   `json:"kind"`
	Repository    string   `json:"repository"`
	Number        int      `json:"number"`
	URL           string   `json:"url"`
	State         string   `json:"state"`
	Actor         string   `json:"actor"`
	Title         string   `json:"title"`
	Branch        string   `json:"branch,omitempty"`
	Commit        string   `json:"commit,omitempty"`
	UpdatedAt     string   `json:"updated_at"`
	MatchedScopes []string `json:"matched_scopes,omitempty"`
}

type githubCaptureParams struct {
	Scope              string   `json:"scope"`
	Identity           string   `json:"identity"`
	Include            []string `json:"include"`
	AlwaysRepositories []string `json:"always_repositories"`
	BootstrapLookback  string   `json:"bootstrap_lookback"`
}

func parseGitHubCaptureParams(raw json.RawMessage) (githubCaptureParams, error) {
	var params githubCaptureParams
	if len(bytesTrimSpace(raw)) == 0 {
		return githubCaptureParams{}, fmt.Errorf("github connector has no capture scope configured; reconnect with workgraph github connect")
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return githubCaptureParams{}, fmt.Errorf("parse github capture params: %w", err)
	}
	if params.Scope != "participant" || strings.TrimSpace(params.Identity) == "" || len(params.Include) == 0 {
		return githubCaptureParams{}, fmt.Errorf("github connector scope is not configured for participant capture; reconnect with workgraph github connect")
	}
	return params, nil
}

func bytesTrimSpace(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}

func githubBootstrapLookback(params githubCaptureParams) time.Duration {
	duration, err := time.ParseDuration(strings.TrimSpace(params.BootstrapLookback))
	if err != nil || duration <= 0 {
		return defaultGitHubBootstrapLookback
	}
	return duration
}

type githubEventPayload struct {
	Repository    string   `json:"repository"`
	Number        int      `json:"number"`
	URL           string   `json:"url"`
	State         string   `json:"state"`
	Actor         string   `json:"actor"`
	Title         string   `json:"title"`
	Branch        string   `json:"branch,omitempty"`
	Commit        string   `json:"commit,omitempty"`
	MatchedScopes []string `json:"matched_scopes,omitempty"`
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
	Title      string `json:"title"`
	UpdatedAt  string `json:"updatedAt"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
}

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

	remoteProjects := githubRemoteProjects(context.Background(), status.WatchDirs, status.HomeDir, status.DatabasePath, status.IgnorePaths, status.IgnoreNames)
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
	if err := createSchema(db); err != nil {
		return GitHubCaptureResult{}, fmt.Errorf("prepare database schema: %w", err)
	}

	gh := config.GitHubCommand
	if gh == "" {
		gh = "gh"
	}
	ctx := config.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if !githubRateLimitAllowsPolling(ctx, gh) {
		if ctx.Err() != nil {
			return GitHubCaptureResult{}, ctx.Err()
		}
		return GitHubCaptureResult{HomeDir: status.HomeDir, DatabasePath: status.DatabasePath}, nil
	}

	state, err := readConnectorRuntimeFile(status.HomeDir)
	if err != nil {
		return GitHubCaptureResult{}, err
	}
	params, err := parseGitHubCaptureParams(state.entry("github").BridgeParams)
	if err != nil {
		return GitHubCaptureResult{}, err
	}

	until := time.Now().UTC()
	cursor, hasCursor, err := readGitHubCaptureCursor(db)
	if err != nil {
		return GitHubCaptureResult{}, err
	}
	since := until.Add(-githubBootstrapLookback(params))
	if hasCursor {
		since = cursor.Add(-githubCaptureOverlap)
	}
	if !since.Before(until) {
		return GitHubCaptureResult{HomeDir: status.HomeDir, DatabasePath: status.DatabasePath}, nil
	}

	events, err := githubParticipantEvents(ctx, gh, params, since, until)
	if err != nil {
		return GitHubCaptureResult{}, err
	}
	if ctx.Err() != nil {
		return GitHubCaptureResult{}, ctx.Err()
	}

	remoteProjects := githubRemoteProjects(ctx, status.WatchDirs, status.HomeDir, status.DatabasePath, status.IgnorePaths, status.IgnoreNames)

	tx, err := db.Begin()
	if err != nil {
		return GitHubCaptureResult{}, fmt.Errorf("begin github capture: %w", err)
	}
	defer tx.Rollback()
	stored := 0
	for _, event := range events {
		inserted, err := storeGitHubEvent(tx, event, inferGitHubProject(tx, event, remoteProjects))
		if err != nil {
			return GitHubCaptureResult{}, err
		}
		if inserted {
			stored++
		}
	}
	if _, err := tx.Exec(`INSERT INTO capture_cursors (connector_id, completed_through, updated_at)
		VALUES ('github', ?, ?)
		ON CONFLICT(connector_id) DO UPDATE SET completed_through = excluded.completed_through, updated_at = excluded.updated_at`,
		until.Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return GitHubCaptureResult{}, fmt.Errorf("advance github capture cursor: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return GitHubCaptureResult{}, fmt.Errorf("commit github capture: %w", err)
	}

	result := GitHubCaptureResult{
		HomeDir:      status.HomeDir,
		DatabasePath: status.DatabasePath,
		EventsStored: stored,
	}
	result.Message = githubCaptureMessage(result)
	return result, nil
}

func readGitHubCaptureCursor(db *sql.DB) (time.Time, bool, error) {
	var value string
	err := db.QueryRow(`SELECT completed_through FROM capture_cursors WHERE connector_id = 'github'`).Scan(&value)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read github capture cursor: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse github capture cursor: %w", err)
	}
	return parsed, true, nil
}

func githubRateLimitAllowsPolling(ctx context.Context, gh string) bool {
	output, err := exec.CommandContext(ctx, gh, "api", "rate_limit").Output()
	if err != nil {
		return false
	}
	var limit githubRateLimit
	if err := json.Unmarshal(output, &limit); err != nil {
		return false
	}
	return limit.Resources.Core.Remaining >= 100
}

func githubParticipantEvents(ctx context.Context, gh string, params githubCaptureParams, since, until time.Time) ([]githubExportEvent, error) {
	include := map[string]bool{}
	for _, value := range params.Include {
		include[strings.TrimSpace(value)] = true
	}
	var groups [][]githubExportEvent
	if include["involves"] {
		prs, err := githubSearchWindow(ctx, gh, "pull_request", []string{"--involves", params.Identity}, since, until, "involves")
		if err != nil {
			return nil, err
		}
		issues, err := githubSearchWindow(ctx, gh, "issue", []string{"--involves", params.Identity}, since, until, "involves")
		if err != nil {
			return nil, err
		}
		groups = append(groups, prs, issues)
	}
	if include["review_requested"] {
		prs, err := githubSearchWindow(ctx, gh, "pull_request", []string{"--review-requested", params.Identity}, since, until, "review_requested")
		if err != nil {
			return nil, err
		}
		groups = append(groups, prs)
	}
	if len(params.AlwaysRepositories) > 0 {
		var repoArgs []string
		for _, repository := range params.AlwaysRepositories {
			repoArgs = append(repoArgs, "--repo", repository)
		}
		prs, err := githubSearchWindow(ctx, gh, "pull_request", repoArgs, since, until, "always_repositories")
		if err != nil {
			return nil, err
		}
		issues, err := githubSearchWindow(ctx, gh, "issue", repoArgs, since, until, "always_repositories")
		if err != nil {
			return nil, err
		}
		groups = append(groups, prs, issues)
	}
	return mergeGitHubEvents(groups...), nil
}

func githubSearchWindow(ctx context.Context, gh string, kind string, baseArgs []string, since, until time.Time, matchedScope string) ([]githubExportEvent, error) {
	if !since.Before(until) {
		return nil, nil
	}
	searchArgs := []string{"search", "prs"}
	jsonFields := "number,url,state,author,title,updatedAt,repository"
	if kind == "issue" {
		searchArgs = []string{"search", "issues"}
	}
	args := append(append([]string{}, searchArgs...), baseArgs...)
	args = append(args,
		"--updated", since.UTC().Format(time.RFC3339)+".."+until.UTC().Format(time.RFC3339),
		"--sort", "updated",
		"--order", "asc",
		"--json", jsonFields,
		"--limit", strconv.Itoa(githubSearchResultCap),
	)
	output, err := exec.CommandContext(ctx, gh, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	var items []githubSearchItem
	if err := json.Unmarshal(output, &items); err != nil {
		return nil, fmt.Errorf("parse gh %s output: %w", strings.Join(args, " "), err)
	}
	if len(items) >= githubSearchResultCap {
		window := until.Sub(since)
		if window <= githubMinBisectWindow {
			return nil, fmt.Errorf("github search %s saturated the %d result cap for %s..%s and cannot be split further", kind, githubSearchResultCap, since.Format(time.RFC3339), until.Format(time.RFC3339))
		}
		mid := since.Add(window / 2)
		first, err := githubSearchWindow(ctx, gh, kind, baseArgs, since, mid, matchedScope)
		if err != nil {
			return nil, err
		}
		second, err := githubSearchWindow(ctx, gh, kind, baseArgs, mid, until, matchedScope)
		if err != nil {
			return nil, err
		}
		return append(first, second...), nil
	}
	events := make([]githubExportEvent, 0, len(items))
	for _, item := range items {
		events = append(events, githubExportEvent{
			Kind:          kind,
			Repository:    item.Repository.NameWithOwner,
			Number:        item.Number,
			URL:           item.URL,
			State:         strings.ToLower(item.State),
			Actor:         item.Author.Login,
			Title:         item.Title,
			UpdatedAt:     item.UpdatedAt,
			MatchedScopes: []string{matchedScope},
		})
	}
	return events, nil
}

func mergeGitHubEvents(groups ...[]githubExportEvent) []githubExportEvent {
	index := map[string]int{}
	var merged []githubExportEvent
	for _, group := range groups {
		for _, event := range group {
			key := fmt.Sprintf("%s:%s:%d", event.Kind, strings.ToLower(event.Repository), event.Number)
			if i, ok := index[key]; ok {
				merged[i] = mergeGitHubEvent(merged[i], event)
				continue
			}
			index[key] = len(merged)
			merged = append(merged, event)
		}
	}
	return merged
}

func mergeGitHubEvent(existing, incoming githubExportEvent) githubExportEvent {
	winner := existing
	if githubEventIsNewer(incoming, existing) {
		winner = incoming
	}
	scopes := map[string]bool{}
	for _, scope := range existing.MatchedScopes {
		scopes[scope] = true
	}
	for _, scope := range incoming.MatchedScopes {
		scopes[scope] = true
	}
	merged := make([]string, 0, len(scopes))
	for scope := range scopes {
		merged = append(merged, scope)
	}
	sort.Strings(merged)
	winner.MatchedScopes = merged
	return winner
}

func githubEventIsNewer(candidate, current githubExportEvent) bool {
	candidateTime, candidateErr := time.Parse(time.RFC3339Nano, candidate.UpdatedAt)
	currentTime, currentErr := time.Parse(time.RFC3339Nano, current.UpdatedAt)
	if candidateErr != nil || currentErr != nil {
		return candidate.UpdatedAt > current.UpdatedAt
	}
	return candidateTime.After(currentTime)
}

type githubExecQueryRower interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func storeGitHubEvent(db githubExecQueryRower, event githubExportEvent, project string) (bool, error) {
	eventType := githubEventType(event.Kind)
	if eventType == "" {
		return false, nil
	}

	payload, err := json.Marshal(githubEventPayload{
		Repository:    event.Repository,
		Number:        event.Number,
		URL:           event.URL,
		State:         event.State,
		Actor:         event.Actor,
		Title:         event.Title,
		Branch:        event.Branch,
		Commit:        event.Commit,
		MatchedScopes: event.MatchedScopes,
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

	result, err := db.Exec(`INSERT INTO events
		(id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			timestamp = excluded.timestamp,
			payload_json = excluded.payload_json,
			project = excluded.project,
			actor = excluded.actor,
			summary = excluded.summary
		WHERE excluded.timestamp > events.timestamp`,
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

func inferGitHubProject(db githubExecQueryRower, event githubExportEvent, remoteProjects map[string]string) string {
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

func projectForCommit(db githubExecQueryRower, commit string) string {
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

func githubRemoteProjects(ctx context.Context, watchDirs []string, homeDir, dbPath string, ignorePaths []string, ignoreNames []string) map[string]string {
	projects := map[string]string{}
	for _, entry := range githubRemoteProjectEntries(ctx, watchDirs, homeDir, dbPath, ignorePaths, ignoreNames) {
		projects[strings.ToLower(entry.Repository)] = entry.Project
	}
	return projects
}

type githubRemoteProjectEntry struct {
	Repository string
	Project    string
}

func githubRemoteProjectEntries(ctx context.Context, watchDirs []string, homeDir, dbPath string, ignorePaths []string, ignoreNames []string) []githubRemoteProjectEntry {
	repos, err := findGitRepositories(watchDirs, homeDir, dbPath, ignorePaths, ignoreNames)
	if err != nil {
		return nil
	}

	var entries []githubRemoteProjectEntry
	for _, repo := range repos {
		output, err := exec.CommandContext(ctx, "git", "-C", repo, "remote", "get-url", "origin").Output()
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
