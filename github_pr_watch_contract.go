package workgraph

import "time"

const githubPRWatchConnectorID = "github.pr_watch"

const (
	githubPRRoleAuthor   = "author"
	githubPRRoleReviewer = "reviewer"
)

const (
	githubPREventObserved             = "github.pull_request.observed"
	githubPREventReopened             = "github.pull_request.reopened"
	githubPREventReviewComment        = "github.pull_request.review_comment"
	githubPREventAuthorReplied        = "github.pull_request.author_replied"
	githubPREventChangesRequested     = "github.pull_request.changes_requested"
	githubPREventApproved             = "github.pull_request.approved"
	githubPREventChecksFailed         = "github.pull_request.checks_failed"
	githubPREventChecksPassed         = "github.pull_request.checks_passed"
	githubPREventReadyToMerge         = "github.pull_request.ready_to_merge"
	githubPREventReadinessLost        = "github.pull_request.readiness_lost"
	githubPREventMergeConflict        = "github.pull_request.merge_conflict"
	githubPREventReviewRequested      = "github.pull_request.review_requested"
	githubPREventReviewRequestRemoved = "github.pull_request.review_request_removed"
	githubPREventReviewSubmitted      = "github.pull_request.review_submitted"
	githubPREventMerged               = "github.pull_request.merged"
	githubPREventClosed               = "github.pull_request.closed"
	githubPREventUnwatched            = "github.pull_request.unwatched"
)

const (
	nextReasonReply            = "reply"
	nextReasonReviewRequested  = "review_requested"
	nextReasonChangesRequested = "changes_requested"
	nextReasonChecksFailed     = "checks_failed"
	nextReasonConflicts        = "conflicts"
	nextReasonReadyToMerge     = "ready_to_merge"
)

const (
	nextCloseResolved    = "resolved"
	nextCloseDone        = "done"
	nextCloseSnoozed     = "snoozed"
	nextCloseMerged      = "merged"
	nextCloseClosed      = "closed"
	nextCloseOutOfScope  = "out_of_scope"
	nextCloseLeftSearch  = "left_search"
	nextCloseUnreachable = "unreachable"
)

const githubPRObservationsSchema = `CREATE TABLE IF NOT EXISTS github_pr_observations (
	connector_id TEXT NOT NULL,
	node_id TEXT NOT NULL,
	role TEXT NOT NULL,
	repository TEXT NOT NULL,
	number INTEGER NOT NULL,
	observation_json TEXT NOT NULL CHECK (json_valid(observation_json)),
	observed_at TEXT NOT NULL,
	PRIMARY KEY (connector_id, node_id, role)
)`

const githubPRWatchStateSchema = `CREATE TABLE IF NOT EXISTS github_pr_watch_state (
	connector_id TEXT PRIMARY KEY,
	enabled INTEGER NOT NULL DEFAULT 0,
	scope_hash TEXT NOT NULL DEFAULT '',
	poll_seq INTEGER NOT NULL DEFAULT 0
)`

const connectorPollHintsSchema = `CREATE TABLE IF NOT EXISTS connector_poll_hints (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	connector_id TEXT NOT NULL,
	received_at TEXT NOT NULL,
	pr_url TEXT NOT NULL DEFAULT ''
)`

type githubPRObservation struct {
	Role              string `json:"role"`
	Repository        string `json:"repository"`
	Number            int    `json:"number"`
	NodeID            string `json:"node_id"`
	URL               string `json:"url"`
	Title             string `json:"title"`
	AuthorLogin       string `json:"author_login"`
	IsDraft           bool   `json:"is_draft"`
	State             string `json:"state"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
	HeadRef           string `json:"head_ref"`
	HeadSHA           string `json:"head_sha"`
	BaseRef           string `json:"base_ref"`
	HeadRepository    string `json:"head_repository,omitempty"`
	IsCrossRepository bool   `json:"is_cross_repository"`
	BaselineAt        string `json:"baseline_at"`

	ReviewDecision     string                       `json:"review_decision,omitempty"`
	OpinionatedReviews *[]githubPROpinionatedReview `json:"opinionated_reviews,omitempty"`
	ChecksState        string                       `json:"checks_state,omitempty"`
	CheckRuns          []githubPRCheckRun           `json:"check_runs,omitempty"`
	Threads            *[]githubPRThread            `json:"threads,omitempty"`
	Conversation       githubPRConversation         `json:"conversation"`
	Mergeable          string                       `json:"mergeable,omitempty"`
	MergeStateStatus   string                       `json:"merge_state_status,omitempty"`
	Ready              string                       `json:"ready,omitempty"`
	LastKnown          githubPRLastKnown            `json:"last_known"`
	ReportedRunSets    []string                     `json:"reported_run_sets,omitempty"`
	TimelineSince      string                       `json:"timeline_since,omitempty"`
	TimelineCursor     string                       `json:"timeline_cursor,omitempty"`
	DetailFetchedAt    string                       `json:"detail_fetched_at,omitempty"`

	ReviewRequest *githubPRReviewRequest `json:"review_request,omitempty"`
	ViewerReview  *githubPRViewerReview  `json:"viewer_review,omitempty"`

	ObservedAt   string `json:"observed_at"`
	VerifiedAt   string `json:"verified_at"`
	MissingPolls int    `json:"missing_polls"`
	Access       string `json:"access"`
}

type githubPROpinionatedReview struct {
	ReviewID    string `json:"review_id"`
	Reviewer    string `json:"reviewer"`
	ActorKind   string `json:"actor_kind"`
	State       string `json:"state"`
	SubmittedAt string `json:"submitted_at"`
}

type githubPRCheckRun struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	CompletedAt string `json:"completed_at"`
	DetailsURL  string `json:"details_url"`
}

type githubPRThread struct {
	ThreadID      string `json:"thread_id"`
	Path          string `json:"path"`
	Line          int    `json:"line"`
	IsResolved    bool   `json:"is_resolved"`
	IsOutdated    bool   `json:"is_outdated"`
	Depth         int    `json:"depth"`
	LastCommentID string `json:"last_comment_id"`
	LastCommentAt string `json:"last_comment_at"`
	LastResponder string `json:"last_responder"`
	AgentReplies  int    `json:"agent_replies"`
	Awaiting      string `json:"awaiting,omitempty"`
	Unknown       bool   `json:"unknown,omitempty"`
}

type githubPRConversation struct {
	LastHumanCommentID string `json:"last_human_comment_id,omitempty"`
	LastHumanCommentAt string `json:"last_human_comment_at,omitempty"`
	LastUserCommentAt  string `json:"last_user_comment_at,omitempty"`
	Unknown            bool   `json:"unknown,omitempty"`
}

type githubPRLastKnown struct {
	Ready     string `json:"ready,omitempty"`
	Mergeable string `json:"mergeable,omitempty"`
}

type githubPRBaseline struct {
	ChangesRequestedReviewIDs []string                 `json:"changes_requested_review_ids"`
	AwaitingThreads           []githubPRBaselineThread `json:"awaiting_threads"`
	ConversationCommentID     string                   `json:"conversation_comment_id,omitempty"`
	HeadSHA                   string                   `json:"head_sha"`
	RunFingerprint            string                   `json:"run_fingerprint,omitempty"`
}

type githubPRBaselineThread struct {
	ThreadID      string `json:"thread_id"`
	LastCommentID string `json:"last_comment_id"`
}

type githubPRReviewRequest struct {
	Direct         bool     `json:"direct"`
	Teams          []string `json:"teams"`
	UnresolvedTeam bool     `json:"unresolved_team"`
}

type githubPRViewerReview struct {
	ID          string `json:"id"`
	State       string `json:"state"`
	SubmittedAt string `json:"submitted_at"`
	Commit      string `json:"commit"`
}

type NextItem struct {
	Schema      string            `json:"schema"`
	ID          string            `json:"id"`
	Source      string            `json:"source"`
	Reason      string            `json:"reason"`
	Group       string            `json:"group"`
	Role        string            `json:"role"`
	Project     string            `json:"project"`
	Since       time.Time         `json:"since"`
	OpenedBy    string            `json:"opened_by"`
	Evidence    []string          `json:"evidence"`
	Subject     NextItemSubject   `json:"subject"`
	PullRequest NextItemPR        `json:"pull_request"`
	Actor       *NextItemActor    `json:"actor,omitempty"`
	Detail      map[string]any    `json:"detail"`
	Triage      map[string]any    `json:"triage"`
	Freshness   NextItemFreshness `json:"freshness"`
	Untrusted   map[string]any    `json:"untrusted,omitempty"`
}

type NextItemSubject struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	URL  string `json:"url"`
	Path string `json:"path,omitempty"`
	Line int    `json:"line,omitempty"`
}

type NextItemPR struct {
	Repository        string  `json:"repository"`
	Number            int     `json:"number"`
	NodeID            string  `json:"node_id"`
	URL               string  `json:"url"`
	Title             string  `json:"title"`
	Branch            string  `json:"branch"`
	BaseRef           string  `json:"base_ref"`
	HeadSHA           string  `json:"head_sha"`
	HeadRepository    *string `json:"head_repository"`
	IsDraft           bool    `json:"is_draft"`
	IsCrossRepository bool    `json:"is_cross_repository"`
}

type NextItemActor struct {
	Login       string `json:"login"`
	Kind        string `json:"kind"`
	Association string `json:"association,omitempty"`
}

type NextItemFreshness struct {
	AsOf   time.Time `json:"as_of"`
	Stale  bool      `json:"stale"`
	Access string    `json:"access"`
}

const nextItemSchemaV1 = "workgraph.next_item.v1"
