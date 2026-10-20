package facts

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

const (
	watchPRNodeID  = "PR_kwDOwidget42"
	watchHeadSHA   = "1111111111111111111111111111111111111111"
	watchIdentity  = "octo"
	watchUpdatedAt = "2026-10-16T10:00:00Z"
)

var watchBase = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

type prFixture struct {
	ID         string
	Number     int
	Repo       string
	Title      string
	Draft      bool
	UpdatedAt  string
	HeadRef    string
	HeadSHA    string
	Decision   string
	Mergeable  string
	MergeState string
	Checks     []checkFixture
	ChecksOver bool
}

type checkFixture struct {
	ID          string
	Name        string
	Status      string
	Conclusion  string
	CompletedAt string
}

type commentFixture struct {
	ID          string
	Author      string
	Bot         bool
	Body        string
	CreatedAt   string
	State       string
	Association string
	ReplyTo     string
}

type reviewFixture struct {
	ID          string
	Author      string
	State       string
	Body        string
	SubmittedAt string
}

type threadFixture struct {
	ID            string
	Path          string
	Line          int
	Resolved      bool
	Outdated      bool
	Comments      []commentFixture
	CommentsTotal int
}

type detailFixture struct {
	ID          string
	Reviews     []reviewFixture
	Timeline    []map[string]any
	HasNextPage bool
	EndCursor   string
	Threads     []threadFixture
}

func watchPR(mutate ...func(*prFixture)) prFixture {
	pr := prFixture{
		ID:         watchPRNodeID,
		Number:     42,
		Repo:       "acme/widget",
		Title:      "Add settings helper",
		UpdatedAt:  watchUpdatedAt,
		HeadRef:    "feature/settings-helper",
		HeadSHA:    watchHeadSHA,
		Decision:   "REVIEW_REQUIRED",
		Mergeable:  "MERGEABLE",
		MergeState: "BLOCKED",
	}
	for _, change := range mutate {
		change(&pr)
	}
	return pr
}

func watchActor(login string, bot bool) map[string]any {
	typename := "User"
	if bot {
		typename = "Bot"
	}
	return map[string]any{"__typename": typename, "login": login}
}

func (pr prFixture) node() map[string]any {
	var rollup any
	if len(pr.Checks) > 0 {
		state := "SUCCESS"
		contexts := []any{}
		for _, check := range pr.Checks {
			if check.Status != "COMPLETED" {
				state = "PENDING"
			} else if check.Conclusion == "FAILURE" && state != "PENDING" {
				state = "FAILURE"
			}
			contexts = append(contexts, map[string]any{
				"__typename": "CheckRun", "id": check.ID, "name": check.Name, "status": check.Status,
				"conclusion": nullable(check.Conclusion), "completedAt": nullable(check.CompletedAt),
				"detailsUrl": "https://github.com/acme/widget/runs/" + check.ID,
			})
		}
		total := len(contexts)
		if pr.ChecksOver {
			total = 101
		}
		rollup = map[string]any{"state": state, "contexts": map[string]any{"totalCount": total, "nodes": contexts}}
	}
	return map[string]any{
		"id": pr.ID, "number": pr.Number, "url": fmt.Sprintf("https://github.com/%s/pull/%d", pr.Repo, pr.Number),
		"title": pr.Title, "isDraft": pr.Draft, "state": "OPEN", "createdAt": "2026-10-15T09:00:00Z", "updatedAt": pr.UpdatedAt,
		"author": watchActor(watchIdentity, false), "repository": map[string]any{"nameWithOwner": pr.Repo},
		"headRepository": map[string]any{"nameWithOwner": pr.Repo}, "isCrossRepository": false,
		"headRefName": pr.HeadRef, "headRefOid": pr.HeadSHA, "baseRefName": "main",
		"reviewDecision": nullable(pr.Decision), "mergeable": pr.Mergeable, "mergeStateStatus": pr.MergeState,
		"reviewRequests":     map[string]any{"totalCount": 0, "nodes": []any{}},
		"viewerLatestReview": map[string]any{"nodes": []any{}},
		"commits": map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{
			"oid": pr.HeadSHA, "statusCheckRollup": rollup,
		}}}},
	}
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func watchJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return string(encoded)
}

func watchRateLimit() map[string]any {
	return map[string]any{"cost": 1, "remaining": 4900, "resetAt": "2026-10-16T13:00:00Z"}
}

func statusPage(t *testing.T, issueCount int, prs ...prFixture) string {
	t.Helper()
	nodes := []any{}
	for _, pr := range prs {
		nodes = append(nodes, pr.node())
	}
	return watchJSON(t, map[string]any{"data": map[string]any{
		"viewer":    map[string]any{"login": watchIdentity},
		"rateLimit": watchRateLimit(),
		"search": map[string]any{
			"issueCount": issueCount,
			"pageInfo":   map[string]any{"hasNextPage": false, "endCursor": nil},
			"nodes":      nodes,
		},
	}})
}

func statusResponse(t *testing.T, prs ...prFixture) string {
	t.Helper()
	return statusPage(t, len(prs), prs...)
}

func issueComment(comment commentFixture) map[string]any {
	return map[string]any{
		"__typename": "IssueComment", "id": comment.ID, "body": comment.Body, "createdAt": comment.CreatedAt,
		"url":               "https://github.com/acme/widget/pull/42#issuecomment-" + comment.ID,
		"authorAssociation": associationOrDefault(comment.Association), "author": watchActor(comment.Author, comment.Bot),
	}
}

func reviewItem(review reviewFixture) map[string]any {
	return map[string]any{
		"__typename": "PullRequestReview", "id": review.ID, "state": review.State, "body": review.Body,
		"submittedAt": review.SubmittedAt, "url": "https://github.com/acme/widget/pull/42#pullrequestreview-" + review.ID,
		"authorAssociation": "MEMBER", "commit": map[string]any{"oid": watchHeadSHA}, "author": watchActor(review.Author, false),
	}
}

func associationOrDefault(value string) string {
	if value == "" {
		return "MEMBER"
	}
	return value
}

func (detail detailFixture) node() any {
	if detail.ID == "" {
		return nil
	}
	reviews := []any{}
	for _, review := range detail.Reviews {
		reviews = append(reviews, map[string]any{
			"id": review.ID, "state": review.State, "submittedAt": review.SubmittedAt, "author": watchActor(review.Author, false),
		})
	}
	threads := []any{}
	for _, thread := range detail.Threads {
		comments := []any{}
		for _, comment := range thread.Comments {
			state := comment.State
			if state == "" {
				state = "SUBMITTED"
			}
			var replyTo any
			if comment.ReplyTo != "" {
				replyTo = map[string]any{"id": comment.ReplyTo}
			}
			comments = append(comments, map[string]any{
				"id": comment.ID, "state": state, "body": comment.Body, "createdAt": comment.CreatedAt,
				"url":      "https://github.com/acme/widget/pull/42#discussion_" + comment.ID,
				"diffHunk": "@@ -12,7 +12,7 @@ func loadSettings()", "replyTo": replyTo,
				"authorAssociation": associationOrDefault(comment.Association), "author": watchActor(comment.Author, comment.Bot),
			})
		}
		total := thread.CommentsTotal
		if total == 0 {
			total = len(comments)
		}
		threads = append(threads, map[string]any{
			"id": thread.ID, "isResolved": thread.Resolved, "isOutdated": thread.Outdated, "path": thread.Path,
			"line": thread.Line, "originalLine": thread.Line, "resolvedBy": nil,
			"comments": map[string]any{"totalCount": total, "nodes": comments},
		})
	}
	timeline := []any{}
	for _, item := range detail.Timeline {
		timeline = append(timeline, item)
	}
	return map[string]any{
		"id":                       detail.ID,
		"latestOpinionatedReviews": map[string]any{"totalCount": len(reviews), "nodes": reviews},
		"timelineItems": map[string]any{
			"pageInfo": map[string]any{"hasNextPage": detail.HasNextPage, "endCursor": nullable(detail.EndCursor)},
			"nodes":    timeline,
		},
		"reviewThreads": map[string]any{"totalCount": len(threads), "nodes": threads},
	}
}

func detailResponse(t *testing.T, details ...detailFixture) string {
	t.Helper()
	data := map[string]any{"rateLimit": watchRateLimit()}
	for index, detail := range details {
		data[fmt.Sprintf("pr%d", index)] = detail.node()
	}
	return watchJSON(t, map[string]any{"data": data})
}

func followUpResponse(t *testing.T, nodes ...any) string {
	t.Helper()
	return watchJSON(t, map[string]any{"data": map[string]any{"rateLimit": watchRateLimit(), "nodes": nodes}})
}

func followUpNode(state string, mutate func(map[string]any)) map[string]any {
	node := map[string]any{
		"id": watchPRNodeID, "state": state, "merged": state == "MERGED", "mergedAt": nil, "closedAt": nil,
		"mergedBy": nil, "repository": map[string]any{"isArchived": false},
	}
	if mutate != nil {
		mutate(node)
	}
	return node
}

func probeResponse(t *testing.T) string {
	t.Helper()
	return watchJSON(t, map[string]any{"data": map[string]any{
		"viewer": map[string]any{"login": watchIdentity}, "rateLimit": map[string]any{"remaining": 4999, "resetAt": "2026-10-16T13:00:00Z"},
	}})
}

func watchTime(offset time.Duration) string {
	return watchBase.Add(offset).UTC().Format(time.RFC3339)
}

type prWatchHarness struct {
	t        *testing.T
	tempDir  string
	homeDir  string
	dbPath   string
	watchDir string
	gh       *fakeGHGraphQL
}

func newPRWatchHarness(t *testing.T, paramsJSON string) *prWatchHarness {
	t.Helper()
	harness := newUnconnectedPRWatchHarness(t)
	harness.connect(paramsJSON)
	return harness
}

func newUnconnectedPRWatchHarness(t *testing.T) *prWatchHarness {
	t.Helper()
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	watchDir := filepath.Join(tempDir, "Code")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}
	isolateWatchSettings(t, homeDir, watchDir)
	return &prWatchHarness{t: t, tempDir: tempDir, homeDir: homeDir, dbPath: result.DatabasePath, watchDir: watchDir, gh: writeFakeGHGraphQL(t, tempDir)}
}

func isolateWatchSettings(t *testing.T, homeDir, watchDir string) {
	t.Helper()
	path := filepath.Join(homeDir, "settings.json")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(contents, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	settings["watch_dirs"] = []string{watchDir}
	settings["conservative_watch_dirs"] = []string{}
	if err := os.WriteFile(path, []byte(watchJSON(t, settings)), 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
}

func (harness *prWatchHarness) connect(paramsJSON string) {
	harness.t.Helper()
	harness.gh.Queue(harness.t, probeResponse(harness.t))
	if _, err := workgraph.ConnectGitHubPRWatch(workgraph.ConnectorConnectConfig{
		HomeDir:       harness.homeDir,
		GitHubCommand: harness.gh.Path,
		ParamsJSON:    json.RawMessage(paramsJSON),
	}); err != nil {
		harness.t.Fatalf("connect github.pr_watch: %v", err)
	}
}

func (harness *prWatchHarness) poll(now time.Time) (workgraph.GitHubPRWatchCaptureResult, error) {
	harness.t.Helper()
	return workgraph.CaptureGitHubPRWatch(workgraph.GitHubPRWatchCaptureConfig{
		HomeDir:       harness.homeDir,
		DatabasePath:  harness.dbPath,
		WatchDirs:     []string{harness.watchDir},
		GitHubCommand: harness.gh.Path,
		Now:           func() time.Time { return now },
	})
}

func (harness *prWatchHarness) mustPoll(now time.Time, responses ...string) {
	harness.t.Helper()
	for _, response := range responses {
		harness.gh.Queue(harness.t, response)
	}
	if _, err := harness.poll(now); err != nil {
		harness.t.Fatalf("poll github.pr_watch at %s: %v", now.Format(time.RFC3339), err)
	}
}

func (harness *prWatchHarness) cli(args ...string) (string, error) {
	harness.t.Helper()
	output, err := runworkgraph(harness.t, repoRoot(harness.t), args...)
	return string(output), err
}

type prWatchEvent struct {
	ID          string
	Type        string
	Timestamp   string
	Actor       string
	Summary     string
	Project     string
	Involvement string
	Raw         string
	Payload     map[string]any
}

func (harness *prWatchHarness) events() []prWatchEvent {
	harness.t.Helper()
	return watchEvents(harness.t, harness.dbPath)
}

func watchEvents(t *testing.T, dbPath string) []prWatchEvent {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, type, timestamp, COALESCE(actor, ''), COALESCE(summary, ''), COALESCE(project, ''),
		COALESCE(involvement_json, ''), payload_json
		FROM events WHERE type GLOB 'github.pull_request.*' ORDER BY timestamp, id`)
	if err != nil {
		t.Fatalf("query watch events: %v", err)
	}
	defer rows.Close()
	var events []prWatchEvent
	for rows.Next() {
		var event prWatchEvent
		if err := rows.Scan(&event.ID, &event.Type, &event.Timestamp, &event.Actor, &event.Summary, &event.Project, &event.Involvement, &event.Raw); err != nil {
			t.Fatalf("scan watch event: %v", err)
		}
		if err := json.Unmarshal([]byte(event.Raw), &event.Payload); err != nil {
			t.Fatalf("parse watch event payload: %v", err)
		}
		events = append(events, event)
	}
	return events
}

func (harness *prWatchHarness) eventsOfType(eventType string) []prWatchEvent {
	harness.t.Helper()
	var matched []prWatchEvent
	for _, event := range harness.events() {
		if event.Type == eventType {
			matched = append(matched, event)
		}
	}
	return matched
}

func (harness *prWatchHarness) observation(nodeID string) map[string]any {
	harness.t.Helper()
	return harness.observationForRole(nodeID, "author")
}

func (harness *prWatchHarness) observationForRole(nodeID, role string) map[string]any {
	harness.t.Helper()
	db, err := sql.Open("sqlite3", harness.dbPath)
	if err != nil {
		harness.t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var raw string
	err = db.QueryRow(`SELECT observation_json FROM github_pr_observations WHERE connector_id = 'github.pr_watch' AND node_id = ? AND role = ?`, nodeID, role).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		harness.t.Fatalf("read observation: %v", err)
	}
	var observation map[string]any
	if err := json.Unmarshal([]byte(raw), &observation); err != nil {
		harness.t.Fatalf("parse observation: %v", err)
	}
	return observation
}

func (harness *prWatchHarness) observationCount() int {
	harness.t.Helper()
	return countRows(harness.t, harness.dbPath, `SELECT COUNT(*) FROM github_pr_observations`)
}

func (harness *prWatchHarness) exec(query string, args ...any) {
	harness.t.Helper()
	db, err := sql.Open("sqlite3", harness.dbPath)
	if err != nil {
		harness.t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		harness.t.Fatalf("exec %q: %v", query, err)
	}
}

func countRows(t *testing.T, dbPath string, query string, args ...any) int {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count rows %q: %v", query, err)
	}
	return count
}

type watchFenceRow struct {
	Enabled   int
	ScopeHash string
	PollSeq   int
}

func (harness *prWatchHarness) fence() watchFenceRow {
	harness.t.Helper()
	db, err := sql.Open("sqlite3", harness.dbPath)
	if err != nil {
		harness.t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var row watchFenceRow
	err = db.QueryRow(`SELECT enabled, scope_hash, poll_seq FROM github_pr_watch_state WHERE connector_id = 'github.pr_watch'`).Scan(&row.Enabled, &row.ScopeHash, &row.PollSeq)
	if err != nil && err != sql.ErrNoRows {
		harness.t.Fatalf("read fence row: %v", err)
	}
	return row
}

func payloadValue(value any, path ...string) any {
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	return value
}

func payloadString(value any, path ...string) string {
	text, _ := payloadValue(value, path...).(string)
	return text
}

func eventTypes(events []prWatchEvent) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, strings.TrimPrefix(event.Type, "github.pull_request."))
	}
	return types
}
