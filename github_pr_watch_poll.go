package workgraph

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	githubPRWatchDetailRefresh    = 30 * time.Minute
	githubPRWatchTimelineOverlap  = 10 * time.Minute
	githubPRWatchMaxTimelinePages = 10
	githubPRWatchMaxMissingPolls  = 6
	githubPRWatchDiffHunkMaxRunes = 4000
	githubPRWatchBacklogThreads   = 20
	githubPRWatchFailedChecksMax  = 20
)

type githubPRWatchPoll struct {
	teams          *githubPRTeamCache
	ctx            context.Context
	gh             string
	db             *sql.DB
	params         githubPRWatchParams
	now            time.Time
	observedAt     string
	remoteProjects map[string]string
	rateLimit      *githubGraphQLRateLimit
	leaving        map[string]bool
	hints          githubPRWatchHints
	homeDir        string
	batchSize      int
	timedOut       bool
	pageSize       int
	searchTimedOut bool
	deferred       *ConnectorPollDeferred
}

type githubPRWatchChanges struct {
	reviewerPolled bool
	events         []githubPRWatchEvent
	upserts        []githubPRObservation
	deletes        []githubPRObservation
}

type githubPRWatchEvent struct {
	ID        string
	Type      string
	Timestamp string
	Actor     string
	Summary   string
	Project   string
	Payload   githubPREventPayload
}

type githubPREventPayload struct {
	Role              string             `json:"role"`
	Repository        string             `json:"repository"`
	Number            int                `json:"number"`
	NodeID            string             `json:"node_id"`
	URL               string             `json:"url"`
	Title             string             `json:"title"`
	Branch            string             `json:"branch"`
	BaseRef           string             `json:"base_ref"`
	HeadSHA           string             `json:"head_sha"`
	HeadRepository    string             `json:"head_repository,omitempty"`
	IsCrossRepository bool               `json:"is_cross_repository"`
	ActorKind         string             `json:"actor_kind,omitempty"`
	ActorAssociation  string             `json:"actor_association,omitempty"`
	State             githubPREventState `json:"state"`
	Triage            map[string]any     `json:"triage"`
	ObservedAt        string             `json:"observed_at"`
	BaselineAt        string             `json:"baseline_at,omitempty"`
	Baseline          *githubPRBaseline  `json:"baseline,omitempty"`
	Untrusted         map[string]any     `json:"untrusted,omitempty"`
}

type githubPREventState struct {
	ReviewDecision      string `json:"review_decision"`
	ChecksState         string `json:"checks_state"`
	UnresolvedThreads   *int   `json:"unresolved_threads"`
	ThreadsAwaitingUser *int   `json:"threads_awaiting_user"`
	Mergeable           string `json:"mergeable"`
	IsDraft             bool   `json:"is_draft"`
	Ready               *bool  `json:"ready"`
}

type githubPRDetail struct {
	node     githubPRDetailNode
	timeline []githubPRTimelineItem
	since    string
	cursor   string
	complete bool
}

type githubPRThreadView struct {
	thread   githubPRThread
	comments []githubPRCommentView
}

type githubPRCommentView struct {
	comment   githubPRThreadComment
	responder string
}

type githubPRChecks struct {
	state       string
	runs        []githubPRCheckRun
	fingerprint string
	completedAt string
	failed      []map[string]any
	carried     bool
}

func CaptureGitHubPRWatch(config GitHubPRWatchCaptureConfig) (GitHubPRWatchCaptureResult, error) {
	status, err := prepareRunStatus(RunConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, WatchDirs: config.WatchDirs})
	if err != nil {
		return GitHubPRWatchCaptureResult{}, err
	}
	if homeDatabase := filepath.Join(status.HomeDir, "workgraph.db"); status.DatabasePath != homeDatabase {
		return GitHubPRWatchCaptureResult{}, fmt.Errorf("connector %s captures only into the home database %s, not %s", githubPRWatchConnectorID, homeDatabase, status.DatabasePath)
	}
	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return GitHubPRWatchCaptureResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := createSchema(db); err != nil {
		return GitHubPRWatchCaptureResult{}, fmt.Errorf("prepare database schema: %w", err)
	}
	ctx := config.Context
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now
	if config.Now != nil {
		now = config.Now
	}
	remoteProjects := githubRemoteProjects(ctx, status.WatchDirs, status.HomeDir, status.DatabasePath, status.IgnorePaths, status.IgnoreNames)

	result := GitHubPRWatchCaptureResult{HomeDir: status.HomeDir, DatabasePath: status.DatabasePath}
	var hints githubPRWatchHints
	for attempt := 0; ; attempt++ {
		state, err := readConnectorRuntimeFile(status.HomeDir)
		if err != nil {
			return result, err
		}
		raw := state.entry(githubPRWatchConnectorID).BridgeParams
		if len(raw) == 0 {
			return result, fmt.Errorf("connector %s is not connected; run workgraph github prs connect", githubPRWatchConnectorID)
		}
		params, err := parseGitHubPRWatchParams(raw)
		if err != nil {
			return result, err
		}
		_, scopeHash, err := params.encode()
		if err != nil {
			return result, err
		}
		if err := reconcileGitHubPRWatchFence(db, state); err != nil {
			return result, err
		}
		fence, err := readGitHubPRWatchFence(db)
		if err != nil {
			return result, err
		}
		if !fence.Enabled {
			return result, fmt.Errorf("connector %s is disabled", githubPRWatchConnectorID)
		}
		fence.ScopeHash = scopeHash
		pollStart := now().UTC()
		if deferred := githubPRWatchDeferral(state.entry(githubPRWatchConnectorID), pollStart); deferred != nil {
			if err := extendConnectorNotBefore(status.HomeDir, githubPRWatchConnectorID, deferred.NotBefore); err != nil {
				return result, err
			}
			return result, deferred
		}
		if attempt == 0 && config.takeHints != nil {
			if hints, err = config.takeHints(db); err != nil {
				return result, err
			}
		}
		poll := &githubPRWatchPoll{
			ctx: ctx, gh: githubCommandOrDefault(config.GitHubCommand), db: db, params: params,
			now: pollStart, remoteProjects: remoteProjects, hints: hints,
			homeDir: status.HomeDir, batchSize: githubPRWatchStoredBatchSize(state.entry(githubPRWatchConnectorID)),
			pageSize: githubPRWatchStoredSearchPageSize(state.entry(githubPRWatchConnectorID)),
		}
		poll.observedAt = poll.now.Format(time.RFC3339Nano)
		var changes *githubPRWatchChanges
		var roleErr error
		poll.teams, roleErr = refreshGitHubPRTeams(ctx, status.HomeDir, poll.gh, params, pollStart, false)
		if roleErr == nil {
			changes, roleErr = poll.run()
		}
		if err := recordGitHubPRWatchBudget(status.HomeDir, poll.rateLimit); err != nil {
			return result, err
		}
		if poll.deferred != nil {
			if err := extendConnectorNotBefore(status.HomeDir, githubPRWatchConnectorID, poll.deferred.NotBefore); err != nil {
				return result, err
			}
			if roleErr == nil && changes == nil {
				return result, poll.deferred
			}
		}
		var retryAfter *ConnectorPollRetryAfter
		if errors.As(roleErr, &retryAfter) {
			if resetAt := githubPRWatchResetAt(poll.rateLimit, state.entry(githubPRWatchConnectorID)); resetAt.After(pollStart) {
				retryAfter.RetryAfter = resetAt.Sub(pollStart)
			}
			if err := extendConnectorNotBefore(status.HomeDir, githubPRWatchConnectorID, pollStart.Add(retryAfter.RetryAfter)); err != nil {
				return result, err
			}
			return result, retryAfter
		}
		if changes == nil {
			return result, roleErr
		}
		stored, err := commitGitHubPRWatchChanges(db, fence, *changes)
		if errors.Is(err, errGitHubPRWatchFenced) {
			current, readErr := readGitHubPRWatchFence(db)
			if readErr == nil && attempt == 0 && current.Enabled && current.ScopeHash == fence.ScopeHash {
				continue
			}
		}
		if err != nil {
			return result, err
		}
		if !poll.timedOut && poll.batchSize < githubPRWatchBatchSize {
			if err := updateGitHubPRWatchState(status.HomeDir, func(entry *connectorRuntimeEntry) { entry.QueryBatchSize = poll.batchSize + 1 }); err != nil {
				return result, err
			}
		}
		if !poll.searchTimedOut && poll.pageSize < githubPRWatchSearchPageSize {
			grown := min(githubPRWatchSearchPageSize, poll.pageSize+githubPRWatchSearchPageGrowth)
			if err := updateGitHubPRWatchState(status.HomeDir, func(entry *connectorRuntimeEntry) { entry.SearchPageSize = grown }); err != nil {
				return result, err
			}
		}
		result.EventsStored = stored
		result.Message = strings.Join([]string{
			"GitHub PR watch capture complete",
			"Home: " + result.HomeDir,
			"Database: " + result.DatabasePath,
			fmt.Sprintf("Events stored: %d", stored),
		}, "\n")
		if roleErr == nil && poll.deferred != nil {
			return result, poll.deferred
		}
		return result, roleErr
	}
}

func githubPRWatchDeferral(entry connectorRuntimeEntry, now time.Time) *ConnectorPollDeferred {
	if notBefore, err := connectorNotBefore(entry); err == nil && notBefore.After(now) {
		return &ConnectorPollDeferred{Reason: "rate limited until " + notBefore.UTC().Format(time.RFC3339), NotBefore: notBefore}
	}
	if entry.RateLimitRemaining == nil || *entry.RateLimitRemaining >= githubPRWatchMinRemaining {
		return nil
	}
	resetAt, err := time.Parse(time.RFC3339Nano, entry.RateLimitResetAt)
	if err != nil || !resetAt.After(now) {
		return nil
	}
	return &ConnectorPollDeferred{Reason: "rate limited until " + resetAt.UTC().Format(time.RFC3339), NotBefore: resetAt}
}

func githubPRWatchResetAt(budget *githubGraphQLRateLimit, entry connectorRuntimeEntry) time.Time {
	resetAt := entry.RateLimitResetAt
	if budget != nil {
		resetAt = budget.ResetAt
	}
	parsed, err := time.Parse(time.RFC3339Nano, resetAt)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func recordGitHubPRWatchBudget(homeDir string, budget *githubGraphQLRateLimit) error {
	if budget == nil {
		return nil
	}
	connectorPollStateMu.Lock()
	defer connectorPollStateMu.Unlock()
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return err
	}
	entry := state.entry(githubPRWatchConnectorID)
	remaining := *budget.Remaining
	entry.RateLimitRemaining = &remaining
	entry.RateLimitResetAt = budget.ResetAt
	state.Connectors[githubPRWatchConnectorID] = entry
	return writeConnectorRuntimeFile(homeDir, state)
}

func extendConnectorNotBefore(homeDir string, id string, deadline time.Time) error {
	connectorPollStateMu.Lock()
	defer connectorPollStateMu.Unlock()
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return err
	}
	entry := state.entry(id)
	if current, err := connectorNotBefore(entry); err == nil && !deadline.After(current) {
		return nil
	}
	entry.NotBefore = deadline.UTC().Format(time.RFC3339Nano)
	state.Connectors[id] = entry
	return writeConnectorRuntimeFile(homeDir, state)
}

func (poll *githubPRWatchPoll) run() (*githubPRWatchChanges, error) {
	var combined *githubPRWatchChanges
	var failures []error
	for _, role := range poll.params.Roles {
		var changes githubPRWatchChanges
		var err error
		if role == githubPRRoleReviewer {
			changes, err = poll.runReviewer()
		} else {
			changes, err = poll.runAuthor()
		}
		var deferred *ConnectorPollDeferred
		if errors.As(err, &deferred) {
			poll.deferred = deferred
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s role: %w", role, err))
			continue
		}
		if combined == nil {
			combined = &githubPRWatchChanges{}
		}
		combined.reviewerPolled = combined.reviewerPolled || changes.reviewerPolled
		combined.events = append(combined.events, changes.events...)
		combined.upserts = append(combined.upserts, changes.upserts...)
		combined.deletes = append(combined.deletes, changes.deletes...)
	}
	return combined, errors.Join(failures...)
}

func (poll *githubPRWatchPoll) runAuthor() (githubPRWatchChanges, error) {
	previous, err := loadGitHubPRObservations(poll.db, githubPRRoleAuthor)
	if err != nil {
		return githubPRWatchChanges{}, err
	}
	nodes, err := poll.search(githubPRRoleAuthor)
	if err != nil {
		return githubPRWatchChanges{}, err
	}
	current := map[string]bool{}
	detailIDs := []string{}
	mergeIDs := []string{}
	for _, node := range nodes {
		current[node.ID] = true
		if poll.needsDetail(previous[node.ID], node) {
			detailIDs = append(detailIDs, node.ID)
		}
		if poll.needsMergeState(previous[node.ID], node) {
			mergeIDs = append(mergeIDs, node.ID)
		}
	}
	leaving := []*githubPRObservation{}
	leavingIDs := []string{}
	for nodeID, observation := range previous {
		if !current[nodeID] {
			leaving = append(leaving, observation)
			leavingIDs = append(leavingIDs, nodeID)
		}
	}
	sort.Strings(leavingIDs)
	poll.leaving = map[string]bool{}
	for _, id := range leavingIDs {
		poll.leaving[id] = true
	}
	sort.Slice(leaving, func(i, j int) bool { return leaving[i].NodeID < leaving[j].NodeID })

	mergeStates, err := poll.fetchMergeStates(mergeIDs)
	if err != nil {
		return githubPRWatchChanges{}, err
	}
	details, err := poll.fetchDetails(append(detailIDs, leavingIDs...), previous)
	if err != nil {
		return githubPRWatchChanges{}, err
	}
	followUps, err := poll.fetchFollowUps(leavingIDs)
	if err != nil {
		return githubPRWatchChanges{}, err
	}

	changes := githubPRWatchChanges{}
	for _, node := range nodes {
		if err := poll.observeAuthor(&changes, previous[node.ID], node, details[node.ID], mergeStates[node.ID]); err != nil {
			return githubPRWatchChanges{}, err
		}
	}
	for _, observation := range leaving {
		poll.resolveLeaving(&changes, *observation, details[observation.NodeID], followUps[observation.NodeID])
	}
	return changes, nil
}

func (poll *githubPRWatchPoll) graphQL(document string, variables map[string]any, out any) (githubGraphQLErrors, error) {
	var data json.RawMessage
	graphQLErrors, err := runGitHubGraphQL(poll.ctx, poll.gh, document, variables, &data)
	if err != nil || len(data) == 0 {
		return graphQLErrors, err
	}
	var budget struct {
		RateLimit *githubGraphQLRateLimit `json:"rateLimit"`
	}
	if json.Unmarshal(data, &budget) == nil && budget.RateLimit != nil && budget.RateLimit.Remaining != nil {
		poll.rateLimit = budget.RateLimit
	}
	if err := json.Unmarshal(data, out); err != nil {
		return nil, fmt.Errorf("parse gh api graphql data: %w", err)
	}
	return graphQLErrors, nil
}

func (poll *githubPRWatchPoll) search(role string) ([]githubPRStatusNode, error) {
	qualifier := "author"
	if role == githubPRRoleReviewer {
		qualifier = "review-requested"
		if poll.params.ReviewRequests == "direct" {
			qualifier = "user-review-requested"
		}
	}
	query := fmt.Sprintf("is:pr is:open %s:%s archived:false sort:updated-desc", qualifier, poll.params.Identity)
	document := githubPRWatchAuthorStatusQuery
	if role == githubPRRoleReviewer {
		document = githubPRWatchReviewerStatusQuery
	}
	var nodes []githubPRStatusNode
	var after any
	issueCount := 0
	for {
		var data githubPRStatusData
		variables := map[string]any{"q": query, "first": poll.pageSize, "after": after}
		if role == githubPRRoleReviewer {
			variables["login"] = poll.params.IdentityLogin
		}
		graphQLErrors, err := poll.graphQL(document, variables, &data)
		if errors.Is(err, errGitHubGraphQLTimeout) && poll.pageSize > githubPRWatchMinSearchPageSize {
			if err := poll.shrinkSearchPages(); err != nil {
				return nil, err
			}
			continue
		}
		if errors.Is(err, errGitHubGraphQLTimeout) {
			poll.searchTimedOut = true
			notBefore := poll.now.Add(githubPRWatchDefaultRetryAfter)
			return nil, &ConnectorPollDeferred{Reason: err.Error() + "; retrying after " + notBefore.Format(time.RFC3339), NotBefore: notBefore}
		}
		if err != nil {
			return nil, err
		}
		if len(graphQLErrors) > 0 {
			return nil, fmt.Errorf("search incomplete: %w", graphQLErrors)
		}
		issueCount = data.Search.IssueCount
		if limit := poll.params.MaxOpenPRs[role]; issueCount > limit {
			return nil, fmt.Errorf("%d open pull requests exceed max_open_prs.%s %d", issueCount, role, limit)
		}
		nodes = append(nodes, data.Search.Nodes...)
		if !data.Search.PageInfo.HasNextPage {
			break
		}
		after = data.Search.PageInfo.EndCursor
	}
	if issueCount != len(nodes) {
		return nil, fmt.Errorf("search incomplete: issueCount %d but received %d pull requests", issueCount, len(nodes))
	}
	pullRequests := nodes[:0]
	for _, node := range nodes {
		if node.ID != "" && !poll.params.ignoresPullRequest(role, node) {
			pullRequests = append(pullRequests, node)
		}
	}
	return pullRequests, nil
}

func (poll *githubPRWatchPoll) needsDetail(previous *githubPRObservation, node githubPRStatusNode) bool {
	if previous == nil || previous.UpdatedAt != node.UpdatedAt || previous.TimelineCursor != "" {
		return true
	}
	fetchedAt, err := time.Parse(time.RFC3339Nano, previous.DetailFetchedAt)
	if err != nil {
		return true
	}
	age := poll.now.Sub(fetchedAt)
	return age > githubPRWatchDetailRefresh || poll.hints.forcesDetail(previous.URL, age)
}

func (poll *githubPRWatchPoll) needsMergeState(previous *githubPRObservation, node githubPRStatusNode) bool {
	if previous == nil || previous.HeadSHA != node.HeadRefOid || previous.UpdatedAt != node.UpdatedAt ||
		previous.ChecksRollup != node.Commits.rollupKey() || previous.Mergeable == "UNKNOWN" || previous.MergeStateStatus == "UNKNOWN" {
		return true
	}
	fetchedAt, err := time.Parse(time.RFC3339Nano, previous.MergeFetchedAt)
	if err != nil {
		return true
	}
	age := poll.now.Sub(fetchedAt)
	return age > githubPRWatchDetailRefresh || poll.hints.forcesDetail(previous.URL, age)
}

func (poll *githubPRWatchPoll) shrinkBatches(failedSize int) error {
	poll.timedOut = true
	poll.batchSize = max(1, failedSize/2)
	return updateGitHubPRWatchState(poll.homeDir, func(entry *connectorRuntimeEntry) { entry.QueryBatchSize = poll.batchSize })
}

func (poll *githubPRWatchPoll) shrinkSearchPages() error {
	poll.searchTimedOut = true
	poll.pageSize = max(githubPRWatchMinSearchPageSize, poll.pageSize/2)
	return updateGitHubPRWatchState(poll.homeDir, func(entry *connectorRuntimeEntry) { entry.SearchPageSize = poll.pageSize })
}

func githubPRWatchStoredBatchSize(entry connectorRuntimeEntry) int {
	if entry.QueryBatchSize > 0 && entry.QueryBatchSize < githubPRWatchBatchSize {
		return entry.QueryBatchSize
	}
	return githubPRWatchBatchSize
}

func githubPRWatchStoredSearchPageSize(entry connectorRuntimeEntry) int {
	if entry.SearchPageSize >= githubPRWatchMinSearchPageSize && entry.SearchPageSize < githubPRWatchSearchPageSize {
		return entry.SearchPageSize
	}
	return githubPRWatchSearchPageSize
}

func updateGitHubPRWatchState(homeDir string, update func(*connectorRuntimeEntry)) error {
	connectorPollStateMu.Lock()
	defer connectorPollStateMu.Unlock()
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return err
	}
	entry := state.entry(githubPRWatchConnectorID)
	update(&entry)
	state.Connectors[githubPRWatchConnectorID] = entry
	return writeConnectorRuntimeFile(homeDir, state)
}

func (poll *githubPRWatchPoll) fetchMergeStates(ids []string) (map[string]*githubPRMergeNode, error) {
	results := map[string]*githubPRMergeNode{}
	queue := append([]string(nil), ids...)
	for len(queue) > 0 {
		size := min(len(queue), poll.batchSize)
		batch := queue[:size]
		variables := map[string]any{}
		for index, id := range batch {
			variables[fmt.Sprintf("id%d", index)] = id
		}
		var data map[string]json.RawMessage
		graphQLErrors, err := poll.graphQL(githubPRWatchMergeQuery(len(batch)), variables, &data)
		if errors.Is(err, errGitHubGraphQLTimeout) && size > 1 {
			if err := poll.shrinkBatches(size); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if len(graphQLErrors) > 0 {
			return nil, fmt.Errorf("merge state incomplete: %w", graphQLErrors)
		}
		queue = queue[size:]
		for index, id := range batch {
			raw := data[fmt.Sprintf("pr%d", index)]
			if len(raw) == 0 || string(raw) == "null" {
				continue
			}
			var node githubPRMergeNode
			if err := json.Unmarshal(raw, &node); err != nil {
				return nil, fmt.Errorf("parse pull request merge state: %w", err)
			}
			results[id] = &node
		}
	}
	return results, nil
}

func (poll *githubPRWatchPoll) fetchDetails(ids []string, previous map[string]*githubPRObservation) (map[string]*githubPRDetail, error) {
	type pendingPage struct {
		id    string
		since string
		after string
		pages int
	}
	queue := []pendingPage{}
	for _, id := range ids {
		page := pendingPage{id: id}
		if observation := previous[id]; observation != nil {
			page.since = observation.TimelineSince
			page.after = observation.TimelineCursor
		}
		queue = append(queue, page)
	}
	results := map[string]*githubPRDetail{}
	for len(queue) > 0 {
		size := min(len(queue), poll.batchSize)
		batch := queue[:size]
		variables := map[string]any{}
		for index, page := range batch {
			variables[fmt.Sprintf("id%d", index)] = page.id
			variables[fmt.Sprintf("since%d", index)] = graphQLNullable(page.since)
			variables[fmt.Sprintf("after%d", index)] = graphQLNullable(page.after)
		}
		var data map[string]json.RawMessage
		graphQLErrors, err := poll.graphQL(githubPRWatchDetailQuery(len(batch)), variables, &data)
		if errors.Is(err, errGitHubGraphQLTimeout) && size > 1 {
			if err := poll.shrinkBatches(size); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		queue = queue[size:]
		leavingAlias := func(field any) bool {
			alias, _ := field.(string)
			var index int
			if _, scanErr := fmt.Sscanf(alias, "pr%d", &index); scanErr != nil || index < 0 || index >= len(batch) {
				return false
			}
			return poll.leaving[batch[index].id]
		}
		if len(graphQLErrors) > 0 && !graphQLErrors.onlyInaccessibleNodes(leavingAlias) {
			return nil, fmt.Errorf("detail incomplete: %w", graphQLErrors)
		}
		for index, page := range batch {
			raw := data[fmt.Sprintf("pr%d", index)]
			if len(raw) == 0 || string(raw) == "null" {
				continue
			}
			var node githubPRDetailNode
			if err := json.Unmarshal(raw, &node); err != nil {
				return nil, fmt.Errorf("parse pull request detail: %w", err)
			}
			detail := results[page.id]
			if detail == nil {
				detail = &githubPRDetail{node: node, since: page.since}
				results[page.id] = detail
			}
			detail.timeline = append(detail.timeline, node.TimelineItems.Nodes...)
			pageInfo := node.TimelineItems.PageInfo
			switch {
			case !pageInfo.HasNextPage:
				detail.complete = true
			case page.pages+1 >= githubPRWatchMaxTimelinePages:
				detail.cursor = pageInfo.EndCursor
			default:
				queue = append(queue, pendingPage{id: page.id, since: page.since, after: pageInfo.EndCursor, pages: page.pages + 1})
			}
		}
	}
	return results, nil
}

func (poll *githubPRWatchPoll) fetchFollowUps(ids []string) (map[string]*githubPRFollowUpNode, error) {
	results := map[string]*githubPRFollowUpNode{}
	if len(ids) == 0 {
		return results, nil
	}
	var data struct {
		Nodes []*githubPRFollowUpNode `json:"nodes"`
	}
	graphQLErrors, err := poll.graphQL(githubPRWatchFollowUpQuery, map[string]any{"ids": ids}, &data)
	if err != nil {
		return nil, err
	}
	if len(graphQLErrors) > 0 && !graphQLErrors.onlyInaccessibleNodes(func(field any) bool { return field == "nodes" }) {
		return nil, fmt.Errorf("follow-up incomplete: %w", graphQLErrors)
	}
	for index, id := range ids {
		if index < len(data.Nodes) && data.Nodes[index] != nil && data.Nodes[index].ID != "" {
			results[id] = data.Nodes[index]
		}
	}
	return results, nil
}

func (poll *githubPRWatchPoll) observeAuthor(changes *githubPRWatchChanges, previous *githubPRObservation, node githubPRStatusNode, detail *githubPRDetail, mergeState *githubPRMergeNode) error {
	rollupKey := node.Commits.rollupKey()
	var checks githubPRChecks
	mergeFetchedAt := ""
	if mergeState != nil && mergeState.HeadRefOid != node.HeadRefOid {
		mergeState = nil
	}
	switch {
	case mergeState != nil:
		node.ReviewDecision = mergeState.ReviewDecision
		node.Mergeable, node.MergeStateStatus, node.Commits = mergeState.Mergeable, mergeState.MergeStateStatus, mergeState.Commits
		checks = githubPRChecksFromStatus(node)
		mergeFetchedAt = poll.observedAt
	case previous != nil:
		node.ReviewDecision = previous.ReviewDecision
		node.Mergeable, node.MergeStateStatus = previous.Mergeable, previous.MergeStateStatus
		checks = githubPRChecks{state: previous.ChecksState, runs: previous.CheckRuns, carried: true}
		mergeFetchedAt = previous.MergeFetchedAt
	default:
		node.Mergeable, node.MergeStateStatus = "UNKNOWN", "UNKNOWN"
		checks = githubPRChecks{state: "unknown", carried: true}
	}
	current := githubPRObservationFromStatus(node)
	current.ObservedAt = poll.observedAt
	current.VerifiedAt = poll.observedAt
	current.Access = "ok"
	current.ChecksRollup = rollupKey
	current.MergeFetchedAt = mergeFetchedAt
	current.ChecksState = checks.state
	current.CheckRuns = checks.runs
	runSet := current.HeadSHA + ":" + checks.fingerprint
	terminal := !checks.carried && (checks.state == "passed" || checks.state == "failed")

	hasStoredObserved := false
	if previous != nil {
		current.BaselineAt = previous.BaselineAt
		current.LastKnown = previous.LastKnown
		for _, reported := range previous.ReportedRunSets {
			if strings.HasPrefix(reported, current.HeadSHA+":") {
				current.ReportedRunSets = append(current.ReportedRunSets, reported)
			}
		}
		current.OpinionatedReviews = previous.OpinionatedReviews
		current.Threads = previous.Threads
		current.Conversation = previous.Conversation
		current.TimelineSince = previous.TimelineSince
		current.TimelineCursor = previous.TimelineCursor
		current.DetailFetchedAt = previous.DetailFetchedAt
	} else {
		baselineAt, found, err := poll.storedObservedBaseline(node.ID, githubPRRoleAuthor)
		if err != nil {
			return err
		}
		hasStoredObserved = found
		current.BaselineAt = poll.observedAt
		if found {
			current.BaselineAt = baselineAt
			if current.LastKnown, err = poll.storedLastKnown(node.ID, githubPRRoleAuthor); err != nil {
				return err
			}
		}
	}
	views := poll.applyDetail(&current, detail)
	var reasons []string
	current.Ready, reasons = githubPRReadiness(current)

	if previous == nil {
		if !hasStoredObserved {
			changes.events = append(changes.events, poll.observedEvent(current, node, checks, views))
		}
		closedAt, err := poll.priorClosedAt(node.ID)
		if err != nil {
			return err
		}
		if closedAt != "" {
			changes.events = append(changes.events, poll.event(current, githubPREventReopened, closedAt, "", poll.observedAt,
				fmt.Sprintf("PR #%d reopened", current.Number), map[string]any{"category": "lifecycle", "needs_user": false}))
		}
		if !hasStoredObserved {
			if terminal {
				current.ReportedRunSets = append(current.ReportedRunSets, runSet)
			}
			if current.Ready != "unknown" {
				current.LastKnown.Ready = current.Ready
			}
			if current.Mergeable != "UNKNOWN" && current.Mergeable != "" {
				current.LastKnown.Mergeable = current.Mergeable
			}
			changes.upserts = append(changes.upserts, current)
			return nil
		}
	}

	if detail != nil {
		changes.events = append(changes.events, poll.feedbackEvents(current, detail, views)...)
	}
	if terminal && !slices.Contains(current.ReportedRunSets, runSet) {
		changes.events = append(changes.events, poll.checksEvent(current, checks))
		current.ReportedRunSets = append(current.ReportedRunSets, runSet)
	}
	discriminator := current.HeadSHA + ":" + poll.observedAt
	if current.Ready == "true" && current.LastKnown.Ready != "true" {
		changes.events = append(changes.events, poll.event(current, githubPREventReadyToMerge, discriminator, "", poll.observedAt,
			fmt.Sprintf("PR #%d ready to merge", current.Number), map[string]any{"category": "merge", "needs_user": true}))
	}
	if current.Ready == "false" && current.LastKnown.Ready == "true" {
		changes.events = append(changes.events, poll.event(current, githubPREventReadinessLost, discriminator, "", poll.observedAt,
			fmt.Sprintf("PR #%d no longer ready to merge (%s)", current.Number, strings.Join(reasons, ", ")),
			map[string]any{"category": "merge", "needs_user": false, "reasons": reasons}))
	}
	if current.Ready != "unknown" {
		current.LastKnown.Ready = current.Ready
	}
	if current.Mergeable == "CONFLICTING" && current.LastKnown.Mergeable != "CONFLICTING" {
		changes.events = append(changes.events, poll.event(current, githubPREventMergeConflict, discriminator, "", poll.observedAt,
			fmt.Sprintf("PR #%d has merge conflicts", current.Number), map[string]any{"category": "merge", "needs_user": true}))
	}
	if current.Mergeable != "UNKNOWN" && current.Mergeable != "" {
		current.LastKnown.Mergeable = current.Mergeable
	}
	changes.upserts = append(changes.upserts, current)
	return nil
}

func (poll *githubPRWatchPoll) resolveLeaving(changes *githubPRWatchChanges, observation githubPRObservation, detail *githubPRDetail, followUp *githubPRFollowUpNode) {
	if detail != nil {
		views := poll.applyDetail(&observation, detail)
		changes.events = append(changes.events, poll.feedbackEvents(observation, detail, views)...)
	}
	observation.ObservedAt = poll.observedAt
	lifecycle := map[string]any{"category": "lifecycle", "needs_user": false}
	unwatched := func(exit string) {
		triage := map[string]any{"category": "lifecycle", "needs_user": false, "exit": exit}
		changes.events = append(changes.events, poll.event(observation, githubPREventUnwatched, observation.Role+":"+poll.observedAt, "", poll.observedAt,
			fmt.Sprintf("PR #%d unwatched (%s)", observation.Number, exit), triage))
		changes.deletes = append(changes.deletes, observation)
	}
	switch {
	case followUp == nil:
		observation.Access = "unreachable"
		observation.MissingPolls++
		if observation.MissingPolls >= githubPRWatchMaxMissingPolls {
			unwatched("unreachable")
			return
		}
		changes.upserts = append(changes.upserts, observation)
	case followUp.Merged || followUp.State == "MERGED":
		changes.events = append(changes.events, poll.event(observation, githubPREventMerged, followUp.MergedAt, followUp.MergedBy.login(), followUp.MergedAt,
			fmt.Sprintf("PR #%d merged", observation.Number), lifecycle))
		changes.deletes = append(changes.deletes, observation)
	case followUp.State == "CLOSED":
		changes.events = append(changes.events, poll.event(observation, githubPREventClosed, followUp.ClosedAt, "", followUp.ClosedAt,
			fmt.Sprintf("PR #%d closed", observation.Number), lifecycle))
		changes.deletes = append(changes.deletes, observation)
	case followUp.Repo.IsArchived:
		unwatched("out_of_scope")
	default:
		observation.Access = "ok"
		observation.MissingPolls++
		if observation.MissingPolls >= githubPRWatchMaxMissingPolls {
			unwatched("left_search")
			return
		}
		changes.upserts = append(changes.upserts, observation)
	}
}

func githubPRObservationFromStatus(node githubPRStatusNode) githubPRObservation {
	observation := githubPRObservation{
		Role:              githubPRRoleAuthor,
		Repository:        node.Repository.NameWithOwner,
		Number:            node.Number,
		NodeID:            node.ID,
		URL:               node.URL,
		Title:             node.Title,
		AuthorLogin:       node.Author.login(),
		AuthorKind:        node.Author.kind(),
		IsDraft:           node.IsDraft,
		State:             node.State,
		CreatedAt:         node.CreatedAt,
		UpdatedAt:         node.UpdatedAt,
		HeadRef:           node.HeadRefName,
		HeadSHA:           node.HeadRefOid,
		BaseRef:           node.BaseRefName,
		IsCrossRepository: node.IsCrossRepository,
		ReviewDecision:    node.ReviewDecision,
		Mergeable:         node.Mergeable,
		MergeStateStatus:  node.MergeStateStatus,
	}
	if node.HeadRepository != nil {
		observation.HeadRepository = node.HeadRepository.NameWithOwner
	}
	if observation.ReviewDecision == "" {
		observation.ReviewDecision = "none"
	}
	return observation
}

func githubPRChecksFromStatus(node githubPRStatusNode) githubPRChecks {
	if len(node.Commits.Nodes) == 0 || node.Commits.Nodes[0].Commit.StatusCheckRollup == nil {
		return githubPRChecks{state: "none"}
	}
	rollup := node.Commits.Nodes[0].Commit.StatusCheckRollup
	if rollup.Contexts.TotalCount > len(rollup.Contexts.Nodes) {
		return githubPRChecks{state: "unknown"}
	}
	checks := githubPRChecks{}
	pending := false
	failed := false
	keys := []string{}
	for _, context := range rollup.Contexts.Nodes {
		var run githubPRCheckRun
		var terminal, failing bool
		var key string
		if context.Typename == "StatusContext" {
			run = githubPRCheckRun{ID: context.Context, Name: context.Context, Status: context.State, Conclusion: context.State, CompletedAt: context.CreatedAt, DetailsURL: context.TargetURL}
			terminal = context.State != "PENDING" && context.State != "EXPECTED"
			failing = context.State == "FAILURE" || context.State == "ERROR"
			key = context.Context + "@" + context.CreatedAt
		} else {
			run = githubPRCheckRun{ID: context.ID, Name: context.Name, Status: context.Status, Conclusion: context.Conclusion, CompletedAt: context.CompletedAt, DetailsURL: context.DetailsURL}
			terminal = context.Status == "COMPLETED"
			failing = terminal && githubCheckConclusionFailed(context.Conclusion)
			key = context.ID + "@" + context.CompletedAt
		}
		checks.runs = append(checks.runs, run)
		if !terminal {
			pending = true
			continue
		}
		keys = append(keys, key)
		if run.CompletedAt > checks.completedAt {
			checks.completedAt = run.CompletedAt
		}
		if failing {
			failed = true
			checks.failed = append(checks.failed, map[string]any{"name": run.Name, "conclusion": run.Conclusion, "details_url": run.DetailsURL})
		}
	}
	sort.Strings(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	checks.fingerprint = hex.EncodeToString(sum[:])[:12]
	sort.Slice(checks.failed, func(i, j int) bool { return checks.failed[i]["name"].(string) < checks.failed[j]["name"].(string) })
	if len(checks.failed) > githubPRWatchFailedChecksMax {
		checks.failed = checks.failed[:githubPRWatchFailedChecksMax]
	}
	switch {
	case pending:
		checks.state = "pending"
	case rollup.State == "SUCCESS":
		checks.state = "passed"
	case rollup.State == "FAILURE" || rollup.State == "ERROR" || failed:
		checks.state = "failed"
	default:
		checks.state = "passed"
	}
	return checks
}

func githubCheckConclusionFailed(conclusion string) bool {
	switch conclusion {
	case "FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
		return true
	default:
		return false
	}
}

func (poll *githubPRWatchPoll) applyDetail(observation *githubPRObservation, detail *githubPRDetail) []githubPRThreadView {
	if detail == nil {
		return nil
	}
	reviews := detail.node.LatestOpinionatedReviews
	if reviews.TotalCount > len(reviews.Nodes) {
		observation.OpinionatedReviews = nil
	} else {
		opinionated := []githubPROpinionatedReview{}
		for _, review := range reviews.Nodes {
			if poll.params.ignoresActor(review.Author.login()) || review.SubmittedAt == "" {
				continue
			}
			opinionated = append(opinionated, githubPROpinionatedReview{
				ReviewID: review.ID, Reviewer: review.Author.login(), ActorKind: review.Author.kind(), State: review.State, SubmittedAt: review.SubmittedAt,
			})
		}
		observation.OpinionatedReviews = &opinionated
	}

	views := []githubPRThreadView{}
	threads := []githubPRThread{}
	for _, node := range detail.node.ReviewThreads.Nodes {
		view := poll.threadView(node)
		views = append(views, view)
		threads = append(threads, view.thread)
	}
	if detail.node.ReviewThreads.TotalCount > len(detail.node.ReviewThreads.Nodes) {
		observation.Threads = nil
	} else {
		observation.Threads = &threads
	}

	conversation := observation.Conversation
	conversation.Unknown = false
	for _, item := range detail.timeline {
		if item.Typename != "IssueComment" || poll.params.ignoresActor(item.Author.login()) {
			continue
		}
		if poll.responder(item.Author.login(), item.Body) != "other" {
			if githubTimeAfter(item.CreatedAt, conversation.LastUserCommentAt) {
				conversation.LastUserCommentAt = item.CreatedAt
			}
			continue
		}
		if item.Author.kind() == "user" && githubTimeAfter(item.CreatedAt, conversation.LastHumanCommentAt) {
			conversation.LastHumanCommentID = item.ID
			conversation.LastHumanCommentAt = item.CreatedAt
		}
	}
	if !detail.complete {
		conversation.Unknown = true
	}
	observation.Conversation = conversation
	observation.TimelineCursor = detail.cursor
	observation.TimelineSince = detail.since
	if detail.complete {
		observation.TimelineSince = poll.now.Add(-githubPRWatchTimelineOverlap).Format(time.RFC3339)
	}
	observation.DetailFetchedAt = poll.observedAt
	return views
}

func (poll *githubPRWatchPoll) threadView(node githubPRThreadNode) githubPRThreadView {
	line := 0
	if node.Line != nil {
		line = *node.Line
	} else if node.OrigLine != nil {
		line = *node.OrigLine
	}
	view := githubPRThreadView{thread: githubPRThread{
		ThreadID: node.ID, Path: node.Path, Line: line, IsResolved: node.IsResolved, IsOutdated: node.IsOutdated,
	}}
	for _, comment := range node.Comments.Nodes {
		if comment.State == "PENDING" || poll.params.ignoresActor(comment.Author.login()) {
			continue
		}
		responder := poll.responder(comment.Author.login(), comment.Body)
		if responder == "agent" {
			view.thread.AgentReplies++
		}
		view.comments = append(view.comments, githubPRCommentView{comment: comment, responder: responder})
	}
	view.thread.Depth = len(view.comments)
	if len(view.comments) > 0 {
		last := view.comments[len(view.comments)-1]
		view.thread.LastCommentID = last.comment.ID
		view.thread.LastCommentAt = last.comment.CreatedAt
		view.thread.LastResponder = last.responder
	}
	switch {
	case node.Comments.TotalCount > len(node.Comments.Nodes):
		view.thread.Unknown = true
	case node.IsResolved || len(view.comments) == 0:
	case view.thread.LastResponder == "other":
		view.thread.Awaiting = "user"
	default:
		view.thread.Awaiting = "other"
	}
	return view
}

func (poll *githubPRWatchPoll) responder(login string, body string) string {
	if login == "" || !strings.EqualFold(login, poll.params.IdentityLogin) {
		return "other"
	}
	marker := strings.TrimSpace(poll.params.AgentReplyMarker)
	if marker != "" && lastNonEmptyLine(normalizeUntrustedText(body)) == marker {
		return "agent"
	}
	return "author"
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(text, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			return line
		}
	}
	return ""
}

func githubPRReadiness(observation githubPRObservation) (string, []string) {
	reasons := []string{}
	unknown := false
	if observation.State != "OPEN" || observation.IsDraft {
		reasons = append(reasons, "draft")
	}
	switch observation.ReviewDecision {
	case "APPROVED":
	case "none":
		if observation.OpinionatedReviews == nil {
			unknown = true
			break
		}
		approved, changesRequested := false, false
		for _, review := range *observation.OpinionatedReviews {
			approved = approved || review.State == "APPROVED"
			changesRequested = changesRequested || review.State == "CHANGES_REQUESTED"
		}
		if !approved || changesRequested {
			reasons = append(reasons, "approval")
		}
	default:
		reasons = append(reasons, "approval")
	}
	switch observation.ChecksState {
	case "passed", "none":
	case "unknown":
		unknown = true
	default:
		reasons = append(reasons, "checks")
	}
	if observation.Threads == nil {
		unknown = true
	} else {
		unresolved := 0
		for _, thread := range *observation.Threads {
			if thread.Unknown {
				unknown = true
			}
			if !thread.IsResolved && !thread.IsOutdated {
				unresolved++
			}
		}
		if unresolved > 0 {
			reasons = append(reasons, "threads")
		}
	}
	switch observation.Mergeable {
	case "MERGEABLE":
	case "CONFLICTING":
		reasons = append(reasons, "conflicts")
	default:
		unknown = true
	}
	switch observation.MergeStateStatus {
	case "CLEAN", "HAS_HOOKS":
	case "UNKNOWN", "":
		unknown = true
	default:
		reasons = append(reasons, "merge_state")
	}
	switch {
	case len(reasons) > 0:
		return "false", reasons
	case unknown:
		return "unknown", reasons
	default:
		return "true", reasons
	}
}

func (poll *githubPRWatchPoll) feedbackEvents(observation githubPRObservation, detail *githubPRDetail, views []githubPRThreadView) []githubPRWatchEvent {
	baseline, err := time.Parse(time.RFC3339Nano, observation.BaselineAt)
	if err != nil {
		return nil
	}
	after := func(value string) bool {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		return err == nil && parsed.After(baseline)
	}
	events := []githubPRWatchEvent{}
	for _, view := range views {
		previousResponder := any(nil)
		for index, comment := range view.comments {
			if after(comment.comment.CreatedAt) {
				events = append(events, poll.threadCommentEvent(observation, view, index, comment, previousResponder))
			}
			previousResponder = comment.responder
		}
	}
	conversationAwaitsUser := !observation.Conversation.Unknown && githubTimeAfter(observation.Conversation.LastHumanCommentAt, observation.Conversation.LastUserCommentAt)
	for _, item := range detail.timeline {
		if poll.params.ignoresActor(item.Author.login()) {
			continue
		}
		switch item.Typename {
		case "IssueComment":
			if after(item.CreatedAt) {
				events = append(events, poll.issueCommentEvent(observation, item, conversationAwaitsUser))
			}
		case "PullRequestReview":
			if after(item.SubmittedAt) {
				if event, ok := poll.reviewEvent(observation, item); ok {
					events = append(events, event)
				}
			}
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Timestamp < events[j].Timestamp })
	return events
}

func (poll *githubPRWatchPoll) threadCommentEvent(observation githubPRObservation, view githubPRThreadView, index int, comment githubPRCommentView, previousResponder any) githubPRWatchEvent {
	location := fmt.Sprintf("%s:%d", view.thread.Path, view.thread.Line)
	var replyTo any
	if comment.comment.ReplyTo != nil {
		replyTo = comment.comment.ReplyTo.ID
	}
	triage := map[string]any{
		"thread_id": view.thread.ThreadID, "path": view.thread.Path, "line": view.thread.Line, "is_outdated": view.thread.IsOutdated,
		"reply_to": replyTo, "thread_depth": index + 1, "previous_responder": previousResponder,
		"agent_replies_in_thread": view.thread.AgentReplies, "has_suggestion": hasSuggestionBlock(comment.comment.Body),
	}
	login := comment.comment.Author.login()
	var event githubPRWatchEvent
	if comment.responder == "other" {
		triage["category"] = "review_feedback"
		triage["needs_user"] = view.thread.Awaiting == "user"
		event = poll.event(observation, githubPREventReviewComment, comment.comment.ID, login, comment.comment.CreatedAt,
			fmt.Sprintf("PR #%d review comment from %s on %s", observation.Number, login, location), triage)
		event.Payload.Untrusted = poll.untrusted("github.review_thread_comment", login, comment.comment.URL, comment.comment.Body, comment.comment.DiffHunk)
	} else {
		triage["category"] = "user_reply"
		triage["needs_user"] = false
		triage["responder"] = comment.responder
		event = poll.event(observation, githubPREventAuthorReplied, comment.comment.ID, login, comment.comment.CreatedAt,
			fmt.Sprintf("PR #%d %s reply on %s", observation.Number, comment.responder, location), triage)
	}
	event.Payload.ActorKind = comment.comment.Author.kind()
	event.Payload.ActorAssociation = comment.comment.AuthorAssociation
	return event
}

func (poll *githubPRWatchPoll) issueCommentEvent(observation githubPRObservation, item githubPRTimelineItem, conversationAwaitsUser bool) githubPRWatchEvent {
	login := item.Author.login()
	responder := poll.responder(login, item.Body)
	triage := map[string]any{"thread_id": nil, "has_suggestion": hasSuggestionBlock(item.Body)}
	var event githubPRWatchEvent
	if responder == "other" {
		triage["category"] = "review_feedback"
		triage["needs_user"] = conversationAwaitsUser && item.Author.kind() == "user"
		event = poll.event(observation, githubPREventReviewComment, item.ID, login, item.CreatedAt,
			fmt.Sprintf("PR #%d comment from %s", observation.Number, login), triage)
		event.Payload.Untrusted = poll.untrusted("github.issue_comment", login, item.URL, item.Body, "")
	} else {
		triage["category"] = "user_reply"
		triage["needs_user"] = false
		triage["responder"] = responder
		event = poll.event(observation, githubPREventAuthorReplied, item.ID, login, item.CreatedAt,
			fmt.Sprintf("PR #%d %s reply in conversation", observation.Number, responder), triage)
	}
	event.Payload.ActorKind = item.Author.kind()
	event.Payload.ActorAssociation = item.AuthorAssociation
	return event
}

func (poll *githubPRWatchPoll) reviewEvent(observation githubPRObservation, item githubPRTimelineItem) (githubPRWatchEvent, bool) {
	login := item.Author.login()
	responder := poll.responder(login, item.Body)
	triage := map[string]any{"category": "review_feedback", "needs_user": false, "has_suggestion": hasSuggestionBlock(item.Body)}
	var event githubPRWatchEvent
	switch {
	case item.State == "CHANGES_REQUESTED" && responder == "other":
		triage["needs_user"] = true
		event = poll.event(observation, githubPREventChangesRequested, item.ID, login, item.SubmittedAt,
			fmt.Sprintf("PR #%d changes requested by %s", observation.Number, login), triage)
	case item.State == "APPROVED" && responder == "other":
		event = poll.event(observation, githubPREventApproved, item.ID, login, item.SubmittedAt,
			fmt.Sprintf("PR #%d approved by %s", observation.Number, login), triage)
	case item.State == "COMMENTED" && strings.TrimSpace(item.Body) != "" && responder == "other":
		event = poll.event(observation, githubPREventReviewComment, item.ID, login, item.SubmittedAt,
			fmt.Sprintf("PR #%d review from %s", observation.Number, login), triage)
	case item.State == "COMMENTED" && strings.TrimSpace(item.Body) != "":
		triage["category"] = "user_reply"
		triage["responder"] = responder
		event = poll.event(observation, githubPREventAuthorReplied, item.ID, login, item.SubmittedAt,
			fmt.Sprintf("PR #%d %s review reply", observation.Number, responder), triage)
		event.Payload.ActorKind = item.Author.kind()
		event.Payload.ActorAssociation = item.AuthorAssociation
		return event, true
	default:
		return githubPRWatchEvent{}, false
	}
	if strings.TrimSpace(item.Body) != "" {
		event.Payload.Untrusted = poll.untrusted("github.review_body", login, item.URL, item.Body, "")
	}
	event.Payload.ActorKind = item.Author.kind()
	event.Payload.ActorAssociation = item.AuthorAssociation
	return event, true
}

func (poll *githubPRWatchPoll) checksEvent(observation githubPRObservation, checks githubPRChecks) githubPRWatchEvent {
	short := observation.HeadSHA
	if len(short) > 7 {
		short = short[:7]
	}
	triage := map[string]any{"category": "ci", "needs_user": checks.state == "failed", "failed_checks": checks.failed}
	if checks.failed == nil {
		triage["failed_checks"] = []map[string]any{}
	}
	discriminator := observation.HeadSHA + ":" + checks.fingerprint
	timestamp := checks.completedAt
	if timestamp == "" {
		timestamp = poll.observedAt
	}
	if checks.state == "failed" {
		names := []string{}
		for _, failed := range checks.failed {
			names = append(names, failed["name"].(string))
		}
		return poll.event(observation, githubPREventChecksFailed, discriminator, "", timestamp,
			fmt.Sprintf("PR #%d checks failed on %s (%s)", observation.Number, short, strings.Join(names, ", ")), triage)
	}
	return poll.event(observation, githubPREventChecksPassed, discriminator, "", timestamp,
		fmt.Sprintf("PR #%d checks passed on %s", observation.Number, short), triage)
}

func (poll *githubPRWatchPoll) observedEvent(observation githubPRObservation, node githubPRStatusNode, checks githubPRChecks, views []githubPRThreadView) githubPRWatchEvent {
	baseline := githubPRBaseline{ReviewIDs: []string{}, Threads: []githubPRBaselineThread{}, HeadSHA: observation.HeadSHA}
	if checks.state == "passed" || checks.state == "failed" {
		baseline.RunFingerprint = checks.fingerprint
	}
	if observation.OpinionatedReviews != nil {
		for _, review := range *observation.OpinionatedReviews {
			if review.State == "CHANGES_REQUESTED" {
				baseline.ReviewIDs = append(baseline.ReviewIDs, review.ReviewID)
			}
		}
	}
	baseline.Conversation.LastHumanCommentID = observation.Conversation.LastHumanCommentID
	backlog := []map[string]any{}
	for _, view := range views {
		thread := view.thread
		if thread.Awaiting != "user" || thread.IsOutdated || thread.Unknown {
			continue
		}
		baseline.Threads = append(baseline.Threads, githubPRBaselineThread{ThreadID: thread.ThreadID, LastCommentID: thread.LastCommentID})
		if len(backlog) < githubPRWatchBacklogThreads {
			last := view.comments[len(view.comments)-1].comment
			entry := poll.untrusted("github.review_thread_comment", last.Author.login(), last.URL, last.Body, "")
			delete(entry, "trust")
			delete(entry, "origin")
			entry["thread_id"] = thread.ThreadID
			entry["path"] = thread.Path
			entry["line"] = thread.Line
			backlog = append(backlog, entry)
		}
	}
	conversationAwaitsUser := githubTimeAfter(observation.Conversation.LastHumanCommentAt, observation.Conversation.LastUserCommentAt)
	needsUser := len(baseline.Threads) > 0 || conversationAwaitsUser || len(baseline.ReviewIDs) > 0 ||
		checks.state == "failed" || observation.Mergeable == "CONFLICTING" || observation.Ready == "true"
	event := poll.event(observation, githubPREventObserved, observation.Role, observation.AuthorLogin, poll.observedAt,
		fmt.Sprintf("PR #%d observed (%s)", observation.Number, observation.Role), map[string]any{"category": "lifecycle", "needs_user": needsUser})
	event.Payload.ActorKind = node.Author.kind()
	event.Payload.BaselineAt = observation.BaselineAt
	event.Payload.Baseline = &baseline
	event.Payload.Untrusted = map[string]any{"trust": "untrusted", "origin": "github.review_thread_comment", "threads": backlog}
	return event
}

func (poll *githubPRWatchPoll) event(observation githubPRObservation, eventType string, discriminator string, actor string, timestamp string, summary string, triage map[string]any) githubPRWatchEvent {
	id := eventType + ":" + observation.NodeID
	if discriminator != "" {
		id += ":" + discriminator
	}
	return githubPRWatchEvent{
		ID:        id,
		Type:      eventType,
		Timestamp: githubNormalizedTime(timestamp, poll.observedAt),
		Actor:     actor,
		Summary:   summary,
		Project:   inferGitHubProject(poll.db, githubExportEvent{Repository: observation.Repository, Commit: observation.HeadSHA}, poll.remoteProjects),
		Payload: githubPREventPayload{
			Role:              observation.Role,
			Repository:        observation.Repository,
			Number:            observation.Number,
			NodeID:            observation.NodeID,
			URL:               observation.URL,
			Title:             observation.Title,
			Branch:            observation.HeadRef,
			BaseRef:           observation.BaseRef,
			HeadSHA:           observation.HeadSHA,
			HeadRepository:    observation.HeadRepository,
			IsCrossRepository: observation.IsCrossRepository,
			State:             githubPRStateSummary(observation),
			Triage:            triage,
			ObservedAt:        poll.observedAt,
		},
	}
}

func githubPRStateSummary(observation githubPRObservation) githubPREventState {
	state := githubPREventState{
		ReviewDecision: observation.ReviewDecision,
		ChecksState:    observation.ChecksState,
		Mergeable:      observation.Mergeable,
		IsDraft:        observation.IsDraft,
	}
	if observation.Threads != nil {
		unresolved, awaiting := 0, 0
		for _, thread := range *observation.Threads {
			if !thread.IsResolved && !thread.IsOutdated {
				unresolved++
			}
			if thread.Awaiting == "user" && !thread.IsOutdated {
				awaiting++
			}
		}
		state.UnresolvedThreads = &unresolved
		state.ThreadsAwaitingUser = &awaiting
	}
	if observation.Ready != "unknown" && observation.Ready != "" {
		ready := observation.Ready == "true"
		state.Ready = &ready
	}
	return state
}

func (poll *githubPRWatchPoll) untrusted(origin string, author string, url string, body string, diffHunk string) map[string]any {
	untrusted := map[string]any{"trust": "untrusted", "origin": origin, "author": author, "comment_url": url}
	maxRunes := *poll.params.CommentBodyMaxRunes
	if maxRunes == 0 {
		return untrusted
	}
	normalized := normalizeUntrustedText(body)
	runes := []rune(normalized)
	sum := sha256.Sum256([]byte(normalized))
	untrusted["body"] = string(runes[:min(len(runes), maxRunes)])
	untrusted["body_truncated"] = len(runes) > maxRunes
	untrusted["body_runes"] = len(runes)
	untrusted["body_sha256"] = hex.EncodeToString(sum[:])
	if diffHunk != "" {
		hunk := []rune(normalizeUntrustedText(diffHunk))
		untrusted["diff_hunk"] = string(hunk[:min(len(hunk), githubPRWatchDiffHunkMaxRunes)])
	}
	return untrusted
}

var githubANSIEscapePattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b[@-_]`)

func normalizeUntrustedText(text string) string {
	text = githubANSIEscapePattern.ReplaceAllString(text, "")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			return -1
		case (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069):
			return -1
		case r == 0x200b || r == 0x200c || r == 0x200d || r == 0x2060 || r == 0xfeff:
			return -1
		}
		return r
	}, text)
}

func hasSuggestionBlock(body string) bool {
	for _, line := range strings.Split(normalizeUntrustedText(body), "\n") {
		if line == "```suggestion" {
			return true
		}
	}
	return false
}

func githubTimeAfter(candidate string, reference string) bool {
	candidateTime, err := time.Parse(time.RFC3339Nano, candidate)
	if err != nil {
		return false
	}
	referenceTime, err := time.Parse(time.RFC3339Nano, reference)
	if err != nil {
		return true
	}
	return candidateTime.After(referenceTime)
}

func githubNormalizedTime(value string, fallback string) string {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return fallback
	}
	return parsed.UTC().Format(time.RFC3339Nano)
}

func graphQLNullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (poll *githubPRWatchPoll) storedObservedBaseline(nodeID string, role string) (string, bool, error) {
	var baselineAt sql.NullString
	err := poll.db.QueryRow(`SELECT json_extract(payload_json, '$.baseline_at') FROM events WHERE id = ?`,
		githubPREventObserved+":"+nodeID+":"+role).Scan(&baselineAt)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read stored observed event: %w", err)
	}
	return baselineAt.String, baselineAt.String != "", nil
}

func (poll *githubPRWatchPoll) storedLastKnown(nodeID string, role string) (githubPRLastKnown, error) {
	rows, err := poll.db.Query(`SELECT type, json_extract(payload_json, '$.state.mergeable'), json_extract(payload_json, '$.state.ready')
		FROM events
		WHERE type IN (?, ?, ?, ?) AND json_extract(payload_json, '$.node_id') = ? AND json_extract(payload_json, '$.role') = ?
		ORDER BY timestamp, id`,
		githubPREventObserved, githubPREventMergeConflict, githubPREventReadyToMerge, githubPREventReadinessLost, nodeID, role)
	if err != nil {
		return githubPRLastKnown{}, fmt.Errorf("read stored readiness evidence: %w", err)
	}
	defer rows.Close()
	lastKnown := githubPRLastKnown{}
	for rows.Next() {
		var eventType string
		var mergeable sql.NullString
		var ready sql.NullBool
		if err := rows.Scan(&eventType, &mergeable, &ready); err != nil {
			return githubPRLastKnown{}, fmt.Errorf("scan stored readiness evidence: %w", err)
		}
		switch eventType {
		case githubPREventObserved:
			if mergeable.String != "" && mergeable.String != "UNKNOWN" {
				lastKnown.Mergeable = mergeable.String
			}
			if ready.Valid {
				lastKnown.Ready = strconv.FormatBool(ready.Bool)
			}
		case githubPREventMergeConflict:
			lastKnown.Mergeable = "CONFLICTING"
		case githubPREventReadyToMerge:
			lastKnown.Ready = "true"
		case githubPREventReadinessLost:
			lastKnown.Ready = "false"
		}
	}
	return lastKnown, rows.Err()
}

func (poll *githubPRWatchPoll) priorClosedAt(nodeID string) (string, error) {
	prefix := githubPREventClosed + ":" + nodeID + ":"
	var id string
	err := poll.db.QueryRow(`SELECT id FROM events WHERE type = ? AND substr(id, 1, ?) = ? ORDER BY timestamp DESC LIMIT 1`,
		githubPREventClosed, len(prefix), prefix).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read prior closed event: %w", err)
	}
	return strings.TrimPrefix(id, prefix), nil
}

func loadGitHubPRObservations(db *sql.DB, role string) (map[string]*githubPRObservation, error) {
	rows, err := db.Query(`SELECT node_id, observation_json FROM github_pr_observations WHERE connector_id = ? AND role = ?`, githubPRWatchConnectorID, role)
	if err != nil {
		return nil, fmt.Errorf("load github.pr_watch observations: %w", err)
	}
	defer rows.Close()
	observations := map[string]*githubPRObservation{}
	for rows.Next() {
		var nodeID, raw string
		if err := rows.Scan(&nodeID, &raw); err != nil {
			return nil, fmt.Errorf("scan github.pr_watch observation: %w", err)
		}
		var observation githubPRObservation
		if err := json.Unmarshal([]byte(raw), &observation); err != nil {
			return nil, fmt.Errorf("parse github.pr_watch observation %s: %w", nodeID, err)
		}
		observations[nodeID] = &observation
	}
	return observations, rows.Err()
}

func commitGitHubPRWatchChanges(db *sql.DB, fence githubPRWatchFence, changes githubPRWatchChanges) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin github.pr_watch commit: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE github_pr_watch_state SET poll_seq = poll_seq + 1
		WHERE connector_id = ? AND enabled = 1 AND scope_hash = ? AND poll_seq = ?`, githubPRWatchConnectorID, fence.ScopeHash, fence.PollSeq)
	if err != nil {
		return 0, fmt.Errorf("fence github.pr_watch commit: %w", err)
	}
	if updated, err := result.RowsAffected(); err != nil || updated != 1 {
		return 0, errGitHubPRWatchFenced
	}

	stored := 0
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	for _, event := range changes.events {
		payload, err := json.Marshal(event.Payload)
		if err != nil {
			return 0, fmt.Errorf("encode github.pr_watch event: %w", err)
		}
		involvement, err := involvementJSON([]string{githubPRInvolvement(event.Payload.Role)})
		if err != nil {
			return 0, err
		}
		inserted, err := tx.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, involvement_json, summary, created_at)
			VALUES (?, 'github', ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
			event.ID, event.Type, event.Timestamp, string(payload), event.Project, event.Actor, involvement, event.Summary, createdAt)
		if err != nil {
			return 0, fmt.Errorf("store github.pr_watch event: %w", err)
		}
		if rows, err := inserted.RowsAffected(); err == nil && rows > 0 {
			stored++
		}
	}
	for _, observation := range changes.upserts {
		encoded, err := json.Marshal(observation)
		if err != nil {
			return 0, fmt.Errorf("encode github.pr_watch observation: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO github_pr_observations (connector_id, node_id, role, repository, number, observation_json, observed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(connector_id, node_id, role) DO UPDATE SET repository = excluded.repository, number = excluded.number,
				observation_json = excluded.observation_json, observed_at = excluded.observed_at`,
			githubPRWatchConnectorID, observation.NodeID, observation.Role, observation.Repository, observation.Number, string(encoded), observation.ObservedAt); err != nil {
			return 0, fmt.Errorf("store github.pr_watch observation: %w", err)
		}
		if observation.Access == "ok" && observation.MissingPolls == 0 {
			if _, err := tx.Exec(`UPDATE events SET payload_json = json_set(payload_json, '$.branch', ?, '$.commit', ?)
				WHERE id = ? AND type = 'github.pull_request'`,
				observation.HeadRef, observation.HeadSHA, fmt.Sprintf("github.pull_request:%s:%d", observation.Repository, observation.Number)); err != nil {
				return 0, fmt.Errorf("enrich github pull request snapshot: %w", err)
			}
		}
	}
	for _, observation := range changes.deletes {
		if _, err := tx.Exec(`DELETE FROM github_pr_observations WHERE connector_id = ? AND node_id = ? AND role = ?`,
			githubPRWatchConnectorID, observation.NodeID, observation.Role); err != nil {
			return 0, fmt.Errorf("delete github.pr_watch observation: %w", err)
		}
	}
	if changes.reviewerPolled {
		if _, err := tx.Exec(`UPDATE github_pr_watch_state SET reviewer_polled = 1 WHERE connector_id = ?`, githubPRWatchConnectorID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit github.pr_watch poll: %w", err)
	}
	return stored, nil
}

func githubPRInvolvement(role string) string {
	if role == githubPRRoleReviewer {
		return "review_requested"
	}
	return "authored"
}
