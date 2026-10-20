package workgraph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const githubPRWatchProbeQuery = `query { viewer { login } rateLimit { remaining resetAt } }`

const githubPRWatchAuthorStatusQuery = `query($q: String!, $first: Int!, $after: String) {
  viewer { login }
  rateLimit { cost remaining resetAt }
  search(type: ISSUE, query: $q, first: $first, after: $after) {
    issueCount
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on PullRequest {
        id number url title isDraft state createdAt updatedAt
        author { __typename login }
        repository { nameWithOwner }
        headRepository { nameWithOwner }
        isCrossRepository
        headRefName headRefOid baseRefName
        commits(last: 1) {
          nodes { commit { oid statusCheckRollup { state contexts { totalCount } } } }
        }
      }
    }
  }
}`

const githubPRWatchReviewerStatusQuery = `query($q: String!, $login: String!, $first: Int!, $after: String) {
  viewer { login }
  rateLimit { cost remaining resetAt }
  search(type: ISSUE, query: $q, first: $first, after: $after) {
    issueCount
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on PullRequest {
        id number url title isDraft state createdAt updatedAt
        author { __typename login }
        repository { nameWithOwner }
        headRepository { nameWithOwner }
        isCrossRepository
        headRefName headRefOid baseRefName
        reviewRequests(first: 50) {
          totalCount
          nodes { requestedReviewer {
            __typename
            ... on User { login }
            ... on Team { slug organization { login } }
          } }
        }
        viewerLatestReview: reviews(author: $login, last: 1,
            states: [APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED]) {
          nodes { id state submittedAt commit { oid } }
        }
        commits(last: 1) {
          nodes { commit { oid statusCheckRollup { state contexts { totalCount } } } }
        }
      }
    }
  }
}`

const githubPRWatchMergeAlias = `
  prN: node(id: $idN) {
    ... on PullRequest {
      id headRefOid reviewDecision mergeable mergeStateStatus
      commits(last: 1) {
        nodes { commit {
          oid
          statusCheckRollup {
            state
            contexts(first: 100) {
              totalCount
              nodes {
                __typename
                ... on CheckRun { id name status conclusion completedAt detailsUrl }
                ... on StatusContext { context state createdAt targetUrl }
              }
            }
          }
        } }
      }
    }
  }`

const githubPRWatchDetailAlias = `
  prN: node(id: $idN) {
    ... on PullRequest {
      id
      latestOpinionatedReviews(first: 20) {
        totalCount
        nodes { id state submittedAt author { __typename login } }
      }
      timelineItems(since: $sinceN, after: $afterN, first: 100,
                    itemTypes: [ISSUE_COMMENT, PULL_REQUEST_REVIEW]) {
        pageInfo { hasNextPage endCursor }
        nodes {
          __typename
          ... on IssueComment {
            id body createdAt url authorAssociation author { __typename login }
          }
          ... on PullRequestReview {
            id state body submittedAt url authorAssociation commit { oid }
            author { __typename login }
          }
        }
      }
      reviewThreads(first: 100) {
        totalCount
        nodes {
          id isResolved isOutdated path line originalLine resolvedBy { login }
          comments(first: 100) {
            totalCount
            nodes {
              id state body createdAt url diffHunk replyTo { id }
              authorAssociation author { __typename login }
            }
          }
        }
      }
    }
  }`

const githubPRWatchFollowUpQuery = `query($ids: [ID!]!) {
  rateLimit { cost remaining resetAt }
  nodes(ids: $ids) {
    ... on PullRequest {
      id state merged mergedAt closedAt mergedBy { login } repository { isArchived }
    }
  }
}`

const githubPRWatchTeamsQuery = `query($login: String!, $after: String) {
  viewer { organizations(first: 100, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes { login teams(first: 100, userLogins: [$login]) {
      totalCount nodes { slug }
    } }
  } }
}`

const githubPRWatchReviewerFollowUpQuery = `query($ids: [ID!]!, $login: String!) {
  rateLimit { cost remaining resetAt }
  nodes(ids: $ids) {
    ... on PullRequest {
      id state merged mergedAt closedAt mergedBy { login } repository { isArchived }
      reviewRequests(first: 50) {
        totalCount
        nodes { requestedReviewer {
          __typename
          ... on User { login }
          ... on Team { slug organization { login } }
        } }
      }
      viewerLatestReview: reviews(author: $login, last: 1,
          states: [APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED]) {
        nodes { id state submittedAt commit { oid } }
      }
    }
  }
}`

const githubPRWatchBatchSize = 10

const (
	githubPRWatchSearchPageSize    = 20
	githubPRWatchMinSearchPageSize = 5
	githubPRWatchSearchPageGrowth  = 5
)

var errGitHubGraphQLTimeout = errors.New("GitHub timed out on the query")

func githubPRWatchMergeQuery(count int) string {
	params := []string{}
	aliases := []string{}
	for index := 0; index < count; index++ {
		suffix := fmt.Sprint(index)
		params = append(params, "$id"+suffix+": ID!")
		aliases = append(aliases, strings.NewReplacer("prN", "pr"+suffix, "$idN", "$id"+suffix).Replace(githubPRWatchMergeAlias))
	}
	return "query(" + strings.Join(params, ", ") + ") {\n  rateLimit { cost remaining resetAt }" + strings.Join(aliases, "") + "\n}"
}

func githubPRWatchDetailQuery(count int) string {
	params := []string{}
	aliases := []string{}
	for index := 0; index < count; index++ {
		suffix := fmt.Sprint(index)
		params = append(params, fmt.Sprintf("$id%[1]s: ID!, $since%[1]s: DateTime, $after%[1]s: String", suffix))
		alias := strings.NewReplacer("prN", "pr"+suffix, "$idN", "$id"+suffix, "$sinceN", "$since"+suffix, "$afterN", "$after"+suffix).Replace(githubPRWatchDetailAlias)
		aliases = append(aliases, alias)
	}
	return "query(" + strings.Join(params, ", ") + ") {\n  rateLimit { cost remaining resetAt }" + strings.Join(aliases, "") + "\n}"
}

type githubGraphQLError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Path    []any  `json:"path"`
}

type githubGraphQLResponse struct {
	Data   json.RawMessage      `json:"data"`
	Errors []githubGraphQLError `json:"errors"`
}

type githubGraphQLErrors []githubGraphQLError

func (errs githubGraphQLErrors) Error() string {
	messages := make([]string, 0, len(errs))
	for _, graphQLErr := range errs {
		label := graphQLErr.Type
		if label == "" {
			label = "ERROR"
		}
		messages = append(messages, label+": "+graphQLErr.Message)
	}
	return "github graphql errors: " + strings.Join(messages, "; ")
}

func (errs githubGraphQLErrors) onlyInaccessibleNodes(allowed func(field any) bool) bool {
	for _, graphQLErr := range errs {
		if (graphQLErr.Type != "NOT_FOUND" && graphQLErr.Type != "FORBIDDEN") || len(graphQLErr.Path) == 0 || !allowed(graphQLErr.Path[0]) {
			return false
		}
	}
	return true
}

func runGitHubGraphQL(ctx context.Context, gh string, document string, variables map[string]any, out any) (githubGraphQLErrors, error) {
	body, err := json.Marshal(map[string]any{"query": document, "variables": variables})
	if err != nil {
		return nil, fmt.Errorf("encode github graphql request: %w", err)
	}
	input, err := os.CreateTemp("", "workgraph-graphql-*.json")
	if err != nil {
		return nil, fmt.Errorf("create github graphql input: %w", err)
	}
	defer os.Remove(input.Name())
	if _, err := input.Write(body); err != nil {
		input.Close()
		return nil, fmt.Errorf("write github graphql input: %w", err)
	}
	if err := input.Close(); err != nil {
		return nil, fmt.Errorf("write github graphql input: %w", err)
	}

	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(ctx, gh, "api", "graphql", "--input", input.Name())
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	var response githubGraphQLResponse
	decodeErr := json.Unmarshal(stdout.Bytes(), &response)
	if runErr != nil && (decodeErr != nil || len(response.Errors) == 0) {
		return nil, githubGHCommandError(stderr.String(), runErr)
	}
	if decodeErr != nil {
		return nil, fmt.Errorf("parse gh api graphql response: %w", decodeErr)
	}
	for _, graphQLErr := range response.Errors {
		if graphQLErr.Type == "RATE_LIMITED" {
			return nil, githubRateLimitError(fmt.Errorf("github graphql rate limited: %s", graphQLErr.Message))
		}
	}
	if len(response.Data) > 0 && string(response.Data) != "null" && out != nil {
		if err := json.Unmarshal(response.Data, out); err != nil {
			return nil, fmt.Errorf("parse gh api graphql data: %w", err)
		}
	}
	return githubGraphQLErrors(response.Errors), nil
}

func githubGHCommandError(stderr string, runErr error) error {
	details := strings.TrimSpace(stderr)
	if details == "" {
		details = runErr.Error()
	}
	for _, status := range []string{"HTTP 502", "HTTP 504"} {
		if strings.Contains(strings.ToUpper(details), status) {
			return fmt.Errorf("%w (%s)", errGitHubGraphQLTimeout, status)
		}
	}
	if githubGHRateLimited(details) {
		return githubRateLimitError(fmt.Errorf("gh api graphql: %s", details))
	}
	if githubGHAuthFailure(details) {
		return fmt.Errorf("gh api graphql: %s; GitHub CLI authentication failed: run gh auth login then workgraph connectors validate %s", details, githubPRWatchConnectorID)
	}
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		return fmt.Errorf("gh api graphql: %w", runErr)
	}
	return fmt.Errorf("gh api graphql: %s", details)
}

func githubGHRateLimited(details string) bool {
	normalized := strings.ToLower(details)
	for _, marker := range []string{"http 403", "http 429", "rate limit exceeded", "secondary rate limit"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func githubRateLimitError(err error) error {
	return &ConnectorPollRetryAfter{Err: err, RetryAfter: githubPRWatchDefaultRetryAfter}
}

func githubGHAuthFailure(details string) bool {
	normalized := strings.ToLower(details)
	return strings.Contains(normalized, "http 401") || strings.Contains(normalized, "bad credentials") || strings.Contains(normalized, "gh auth login")
}

type githubGraphQLRateLimit struct {
	Remaining *int   `json:"remaining"`
	ResetAt   string `json:"resetAt"`
}

type githubPageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type githubActorNode struct {
	Typename string `json:"__typename"`
	Login    string `json:"login"`
}

type githubPRStatusData struct {
	Search struct {
		IssueCount int                  `json:"issueCount"`
		PageInfo   githubPageInfo       `json:"pageInfo"`
		Nodes      []githubPRStatusNode `json:"nodes"`
	} `json:"search"`
}

type githubPRStatusNode struct {
	githubPRReviewerStatus
	ID         string           `json:"id"`
	Number     int              `json:"number"`
	URL        string           `json:"url"`
	Title      string           `json:"title"`
	IsDraft    bool             `json:"isDraft"`
	State      string           `json:"state"`
	CreatedAt  string           `json:"createdAt"`
	UpdatedAt  string           `json:"updatedAt"`
	Author     *githubActorNode `json:"author"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	HeadRepository *struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"headRepository"`
	IsCrossRepository bool                      `json:"isCrossRepository"`
	HeadRefName       string                    `json:"headRefName"`
	HeadRefOid        string                    `json:"headRefOid"`
	BaseRefName       string                    `json:"baseRefName"`
	ReviewDecision    string                    `json:"reviewDecision"`
	Mergeable         string                    `json:"mergeable"`
	MergeStateStatus  string                    `json:"mergeStateStatus"`
	Commits           githubPRCommitsConnection `json:"commits"`
}

type githubPRMergeNode struct {
	ID               string                    `json:"id"`
	HeadRefOid       string                    `json:"headRefOid"`
	ReviewDecision   string                    `json:"reviewDecision"`
	Mergeable        string                    `json:"mergeable"`
	MergeStateStatus string                    `json:"mergeStateStatus"`
	Commits          githubPRCommitsConnection `json:"commits"`
}

type githubPRCommitsConnection struct {
	Nodes []struct {
		Commit struct {
			Oid               string `json:"oid"`
			StatusCheckRollup *struct {
				State    string `json:"state"`
				Contexts struct {
					TotalCount int                    `json:"totalCount"`
					Nodes      []githubPRCheckContext `json:"nodes"`
				} `json:"contexts"`
			} `json:"statusCheckRollup"`
		} `json:"commit"`
	} `json:"nodes"`
}

func (connection githubPRCommitsConnection) rollupKey() string {
	if len(connection.Nodes) == 0 || connection.Nodes[0].Commit.StatusCheckRollup == nil {
		return "none"
	}
	rollup := connection.Nodes[0].Commit.StatusCheckRollup
	return fmt.Sprintf("%s:%d", rollup.State, rollup.Contexts.TotalCount)
}

type githubPRReviewerStatus struct {
	ReviewRequests struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			RequestedReviewer *struct {
				Typename     string `json:"__typename"`
				Login        string `json:"login"`
				Slug         string `json:"slug"`
				Organization struct {
					Login string `json:"login"`
				} `json:"organization"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	ViewerLatestReview struct {
		Nodes []struct {
			ID          string `json:"id"`
			State       string `json:"state"`
			SubmittedAt string `json:"submittedAt"`
			Commit      *struct {
				OID string `json:"oid"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"viewerLatestReview"`
}

func (status githubPRReviewerStatus) viewerReview() *githubPRViewerReview {
	for _, review := range status.ViewerLatestReview.Nodes {
		if review.State == "PENDING" || review.SubmittedAt == "" {
			continue
		}
		result := &githubPRViewerReview{ID: review.ID, State: review.State, SubmittedAt: review.SubmittedAt}
		if review.Commit != nil {
			result.Commit = review.Commit.OID
		}
		return result
	}
	return nil
}

type githubPRCheckContext struct {
	Typename    string `json:"__typename"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	CompletedAt string `json:"completedAt"`
	DetailsURL  string `json:"detailsUrl"`
	Context     string `json:"context"`
	State       string `json:"state"`
	CreatedAt   string `json:"createdAt"`
	TargetURL   string `json:"targetUrl"`
}

type githubPRDetailNode struct {
	ID                       string `json:"id"`
	LatestOpinionatedReviews struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			ID          string           `json:"id"`
			State       string           `json:"state"`
			SubmittedAt string           `json:"submittedAt"`
			Author      *githubActorNode `json:"author"`
		} `json:"nodes"`
	} `json:"latestOpinionatedReviews"`
	TimelineItems struct {
		PageInfo githubPageInfo         `json:"pageInfo"`
		Nodes    []githubPRTimelineItem `json:"nodes"`
	} `json:"timelineItems"`
	ReviewThreads struct {
		TotalCount int                  `json:"totalCount"`
		Nodes      []githubPRThreadNode `json:"nodes"`
	} `json:"reviewThreads"`
}

type githubPRTimelineItem struct {
	Typename          string           `json:"__typename"`
	ID                string           `json:"id"`
	State             string           `json:"state"`
	Body              string           `json:"body"`
	CreatedAt         string           `json:"createdAt"`
	SubmittedAt       string           `json:"submittedAt"`
	URL               string           `json:"url"`
	AuthorAssociation string           `json:"authorAssociation"`
	Author            *githubActorNode `json:"author"`
}

type githubPRThreadNode struct {
	ID         string `json:"id"`
	IsResolved bool   `json:"isResolved"`
	IsOutdated bool   `json:"isOutdated"`
	Path       string `json:"path"`
	Line       *int   `json:"line"`
	OrigLine   *int   `json:"originalLine"`
	Comments   struct {
		TotalCount int                     `json:"totalCount"`
		Nodes      []githubPRThreadComment `json:"nodes"`
	} `json:"comments"`
}

type githubPRThreadComment struct {
	ID                string           `json:"id"`
	State             string           `json:"state"`
	Body              string           `json:"body"`
	CreatedAt         string           `json:"createdAt"`
	URL               string           `json:"url"`
	DiffHunk          string           `json:"diffHunk"`
	AuthorAssociation string           `json:"authorAssociation"`
	Author            *githubActorNode `json:"author"`
	ReplyTo           *struct {
		ID string `json:"id"`
	} `json:"replyTo"`
}

type githubPRFollowUpNode struct {
	githubPRReviewerStatus
	ID       string           `json:"id"`
	State    string           `json:"state"`
	Merged   bool             `json:"merged"`
	MergedAt string           `json:"mergedAt"`
	ClosedAt string           `json:"closedAt"`
	MergedBy *githubActorNode `json:"mergedBy"`
	Repo     struct {
		IsArchived bool `json:"isArchived"`
	} `json:"repository"`
}

func (actor *githubActorNode) login() string {
	if actor == nil {
		return ""
	}
	return actor.Login
}

func (actor *githubActorNode) kind() string {
	if actor == nil {
		return "user"
	}
	if actor.Typename == "Bot" || strings.HasSuffix(strings.ToLower(actor.Login), "[bot]") {
		return "bot"
	}
	return "user"
}
