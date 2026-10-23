package facts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

const reviewerParams = `{"roles":["reviewer"]}`

func teamResponse(t *testing.T) string {
	return watchJSON(t, map[string]any{"data": map[string]any{"viewer": map[string]any{"organizations": map[string]any{
		"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{map[string]any{"login": "acme", "teams": map[string]any{"totalCount": 1, "nodes": []any{map[string]any{"slug": "platform"}}}}},
	}}}})
}

func reviewerHarness(t *testing.T, params string) *prWatchHarness {
	t.Helper()
	h := newUnconnectedPRWatchHarness(t)
	h.gh.Queue(t, probeResponse(t))
	if !strings.Contains(params, `"review_requests":"direct"`) {
		h.gh.Queue(t, teamResponse(t))
	}
	if _, err := workgraph.ConnectGitHubPRWatch(workgraph.ConnectorConnectConfig{HomeDir: h.homeDir, GitHubCommand: h.gh.Path, ParamsJSON: json.RawMessage(params)}); err != nil {
		t.Fatal(err)
	}
	return h
}

func requestedPR(id string, requests ...string) map[string]any {
	node := watchPR(func(pr *prFixture) { pr.ID = id }).node()
	node["author"] = watchActor("alice", false)
	nodes := []any{}
	for _, request := range requests {
		reviewer := watchActor(request, false)
		if org, slug, ok := strings.Cut(request, "/"); ok {
			reviewer = map[string]any{"__typename": "Team", "slug": slug, "organization": map[string]any{"login": org}}
		}
		nodes = append(nodes, map[string]any{"requestedReviewer": reviewer})
	}
	node["reviewRequests"] = map[string]any{"totalCount": len(nodes), "nodes": nodes}
	return node
}

func reviewerStatus(t *testing.T, nodes ...map[string]any) string {
	return watchJSON(t, map[string]any{"data": map[string]any{"rateLimit": watchRateLimit(), "search": map[string]any{"issueCount": len(nodes), "pageInfo": map[string]any{"hasNextPage": false}, "nodes": nodes}}})
}

func setViewerReview(node map[string]any, id, state string, submittedAt any) {
	node["viewerLatestReview"] = map[string]any{"nodes": []any{map[string]any{"id": id, "state": state, "submittedAt": submittedAt, "commit": map[string]any{"oid": watchHeadSHA}}}}
}

func TestGitHubPRWatchDirectAndTeamReviewRequestsEnterReviewerSet(t *testing.T) {
	h := reviewerHarness(t, reviewerParams)
	h.mustPoll(watchBase, reviewerStatus(t, requestedPR("direct", watchIdentity), requestedPR("team", "acme/platform"), requestedPR("unknown", "acme/other")))
	if h.observationCount() != 3 {
		t.Fatalf("expected three reviewer observations, got %d", h.observationCount())
	}
	events := h.eventsOfType("github.pull_request.observed")
	if len(events) != 3 {
		t.Fatalf("expected three observed events, got %v", eventTypes(h.events()))
	}
	for _, event := range events {
		if event.Involvement != `["review_requested"]` || payloadString(event.Payload, "role") != "reviewer" {
			t.Fatalf("wrong involvement: %+v", event)
		}
		request := payloadValue(event.Payload, "triage", "request")
		switch payloadString(event.Payload, "node_id") {
		case "direct":
			if payloadValue(request, "direct") != true {
				t.Fatalf("missing direct: %s", event.Raw)
			}
		case "team":
			if !strings.Contains(event.Raw, "acme/platform") {
				t.Fatalf("missing cached team: %s", event.Raw)
			}
		case "unknown":
			if payloadValue(request, "unresolved_team") != true {
				t.Fatalf("missing unresolved team: %s", event.Raw)
			}
		}
	}
	inputs := h.gh.GraphQLInputs(t)
	if len(inputs) != 3 || !strings.Contains(inputs[1], "userLogins") || !strings.Contains(inputs[2], "review-requested:@me") || strings.Contains(inputs[2], "timelineItems") {
		t.Fatalf("unexpected query sequence: %v", inputs)
	}
	if !strings.Contains(inputs[2], "DISMISSED") || strings.Contains(inputs[2], "PENDING") {
		t.Fatalf("wrong submitted review filter: %s", inputs[2])
	}
	h.mustPoll(watchBase.Add(5*time.Minute), reviewerStatus(t, requestedPR("direct", watchIdentity), requestedPR("team", "acme/platform"), requestedPR("unknown", "acme/other")))
	if len(h.events()) != 3 || len(h.gh.GraphQLInputs(t)) != 4 {
		t.Fatal("unchanged poll emitted events or refreshed teams too soon")
	}
	t.Run("direct only", func(t *testing.T) {
		h := reviewerHarness(t, `{"roles":["reviewer"],"review_requests":"direct"}`)
		h.mustPoll(watchBase, reviewerStatus(t, requestedPR("direct", watchIdentity), requestedPR("team", "acme/platform")))
		if h.observationCount() != 1 {
			t.Fatal("team-only PR must be absent in direct mode")
		}
		if inputs := h.gh.GraphQLInputs(t); len(inputs) != 2 || !strings.Contains(inputs[1], "user-review-requested:@me") {
			t.Fatalf("wrong direct query: %v", inputs)
		}
	})
}

func TestGitHubPRWatchFirstReviewerPollIsBacklogAndLaterRequestsAreTransitions(t *testing.T) {
	t.Run("scope change preserves open item", func(t *testing.T) {
		h := reviewerHarness(t, reviewerParams)
		status := reviewerStatus(t, requestedPR(watchPRNodeID, watchIdentity))
		h.mustPoll(watchBase, status)
		item, ok := nextItemsByReason(t, h, watchBase.Add(time.Minute))["review_requested"]
		if !ok {
			t.Fatal("captured request missing from next")
		}
		h.gh.Queue(t, probeResponse(t))
		if _, err := workgraph.ConnectGitHubPRWatch(workgraph.ConnectorConnectConfig{
			HomeDir: h.homeDir, GitHubCommand: h.gh.Path,
			ParamsJSON: json.RawMessage(`{"roles":["reviewer"],"review_requests":"direct"}`),
		}); err != nil {
			t.Fatal(err)
		}
		h.mustPoll(watchBase.Add(5*time.Minute), status)
		if len(h.eventsOfType("github.pull_request.review_requested")) != 0 {
			t.Error("scope change replayed an existing review request")
		}
		if got := nextItemsByReason(t, h, watchBase.Add(6*time.Minute))["review_requested"]; got.ID != item.ID {
			t.Errorf("scope change replaced open item %q with %q", item.ID, got.ID)
		}
	})
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{true: "empty first poll", false: "backlog"}[empty], func(t *testing.T) {
			h := reviewerHarness(t, reviewerParams)
			first := reviewerStatus(t, requestedPR("backlog", watchIdentity))
			if empty {
				first = reviewerStatus(t)
			}
			h.mustPoll(watchBase, first)
			if len(h.eventsOfType("github.pull_request.review_requested")) != 0 {
				t.Fatal("first poll replayed backlog")
			}
			h.mustPoll(watchBase.Add(5*time.Minute), reviewerStatus(t, requestedPR("backlog", watchIdentity), requestedPR("new", watchIdentity)))
			expected := 1
			if empty {
				expected = 2
			}
			if len(h.eventsOfType("github.pull_request.observed")) != 2 || len(h.eventsOfType("github.pull_request.review_requested")) != expected {
				t.Fatalf("wrong transitions: %v", eventTypes(h.events()))
			}
		})
	}
}

func TestGitHubPRWatchReviewRequestsCloseAtOnce(t *testing.T) {
	for _, state := range []string{"APPROVED", "COMMENTED", "CHANGES_REQUESTED", "PENDING", "null", "withdrawn"} {
		t.Run(state, func(t *testing.T) {
			h := reviewerHarness(t, reviewerParams)
			node := requestedPR(watchPRNodeID, watchIdentity)
			h.mustPoll(watchBase, reviewerStatus(t, node))
			if len(nextItemsByReason(t, h, watchBase.Add(time.Minute))) != 1 {
				t.Fatal("captured request missing from next")
			}
			if state == "PENDING" || state == "null" {
				reviewState := state
				if state == "null" {
					reviewState = "APPROVED"
				}
				setViewerReview(node, "draft", reviewState, nil)
				h.mustPoll(watchBase.Add(2*time.Minute), reviewerStatus(t, node))
				if len(h.events()) != 1 {
					t.Fatal("unsubmitted review emitted an event")
				}
			}
			leaving := requestedPR(watchPRNodeID)
			if state == "withdrawn" {
				leaving = requestedPR(watchPRNodeID, "bob", "acme/not-my-team")
			}
			reviewed := state == "APPROVED" || state == "COMMENTED" || state == "CHANGES_REQUESTED"
			if reviewed {
				setViewerReview(leaving, "review-1", state, watchTime(3*time.Minute))
			}
			h.mustPoll(watchBase.Add(5*time.Minute), reviewerStatus(t), followUpResponse(t, leaving))
			removed := h.eventsOfType("github.pull_request.review_request_removed")
			reason := "withdrawn"
			submissions := 0
			if reviewed {
				reason = "reviewed"
				submissions = 1
			}
			if len(removed) != 1 || payloadString(removed[0].Payload, "triage", "reason") != reason || h.observationCount() != 0 || len(h.eventsOfType("github.pull_request.review_submitted")) != submissions {
				t.Fatalf("request did not close correctly: %v", h.events())
			}
			if len(nextItemsByReason(t, h, watchBase.Add(6*time.Minute))) != 0 {
				t.Fatal("removed request remains in next")
			}
			h.mustPoll(watchBase.Add(10*time.Minute), reviewerStatus(t, node))
			if len(h.eventsOfType("github.pull_request.review_requested")) != 1 || len(h.eventsOfType("github.pull_request.observed")) != 1 {
				t.Fatalf("re-request failed: %v", eventTypes(h.events()))
			}
		})
	}
}

func TestGitHubPRWatchIgnoredTeamsAreAbsent(t *testing.T) {
	h := reviewerHarness(t, `{"roles":["reviewer"],"ignore_teams":["acme/platform","acme/unknown"]}`)
	h.mustPoll(watchBase, reviewerStatus(t, requestedPR("ignored", "acme/platform"), requestedPR("uncached", "acme/unknown"), requestedPR("direct", watchIdentity, "acme/platform")))
	if h.observationCount() != 1 || len(h.events()) != 1 || payloadString(h.events()[0].Payload, "node_id") != "direct" {
		t.Fatalf("ignored team request leaked: %v", h.events())
	}
}

func TestGitHubPRWatchReviewerCaptureFeedsNext(t *testing.T) {
	h := reviewerHarness(t, reviewerParams)
	h.mustPoll(watchBase, reviewerStatus(t, requestedPR(watchPRNodeID, "acme/platform")))
	items := nextItemsByReason(t, h, watchBase.Add(time.Minute))
	item, ok := items["review_requested"]
	if !ok || item.Role != "reviewer" || item.OpenedBy != "github.pull_request.observed:"+watchPRNodeID+":reviewer" {
		t.Fatalf("unexpected next items: %#v", items)
	}
	output, err := h.cli("next", "--home", h.homeDir)
	if err != nil || !strings.Contains(output, "via acme/platform") {
		t.Fatalf("next did not label captured request: %s %v", output, err)
	}
}

func TestGitHubPRWatchTeamCacheRefreshAndWarnings(t *testing.T) {
	for _, failure := range []struct {
		name        string
		response    string
		stderr      string
		code        int
		rateLimited bool
		storedReset bool
	}{
		{name: "unavailable", response: `{"data":{"viewer":null},"errors":[{"type":"FORBIDDEN","message":"read:org unavailable"}]}`},
		{name: "graphql rate limit", response: `{"data":null,"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`, rateLimited: true},
		{name: "stored reset", response: `{"data":null,"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`, code: 1, rateLimited: true, storedReset: true},
		{name: "http rate limit", stderr: "HTTP 429: secondary rate limit", code: 1, rateLimited: true},
	} {
		t.Run(failure.name, func(t *testing.T) {
			h := reviewerHarness(t, reviewerParams)
			node := requestedPR(watchPRNodeID, "acme/platform")
			h.mustPoll(watchBase, reviewerStatus(t, node))
			before := h.observationForRole(watchPRNodeID, "reviewer")
			runtimePath := filepath.Join(h.homeDir, "connectors.json")
			readState := func() (map[string]any, map[string]any) {
				t.Helper()
				raw, err := os.ReadFile(runtimePath)
				if err != nil {
					t.Fatal(err)
				}
				var state map[string]any
				if err := json.Unmarshal(raw, &state); err != nil {
					t.Fatal(err)
				}
				return state, state["connectors"].(map[string]any)["github.pr_watch"].(map[string]any)
			}
			state, entry := readState()
			cache := entry["github_pr_teams"].(map[string]any)
			cache["refreshed_at"] = watchTime(-time.Hour)
			if failure.storedReset {
				entry["rate_limit_reset_at"] = watchTime(20 * time.Minute)
			} else {
				delete(entry, "rate_limit_reset_at")
			}
			if err := os.WriteFile(runtimePath, []byte(watchJSON(t, state)), 0600); err != nil {
				t.Fatal(err)
			}

			h.gh.QueueResult(t, failure.response, failure.stderr, failure.code)
			if !failure.rateLimited {
				h.gh.Queue(t, reviewerStatus(t, node))
			}
			now := watchBase.Add(5 * time.Minute)
			_, err := h.poll(now)
			retryAt := now.Add(time.Minute)
			if failure.rateLimited {
				var retry *workgraph.ConnectorPollRetryAfter
				if !errors.As(err, &retry) {
					t.Fatalf("expected retry-after from team query, got %v", err)
				}
				retryAt = now.Add(retry.RetryAfter)
				if failure.storedReset && !retryAt.Equal(watchBase.Add(20*time.Minute)) {
					t.Fatalf("retry ignored stored reset: %s", retryAt)
				}
				if !failure.storedReset && retry.RetryAfter != time.Minute {
					t.Fatalf("expected a one-minute retry without a reset, got %s", retry.RetryAfter)
				}
				deadline, err := workgraph.ConnectorPollNotBefore(h.homeDir, "github.pr_watch")
				if err != nil || !deadline.Equal(retryAt) {
					t.Fatalf("missing retry deadline: %s %v", deadline, err)
				}
				calls := len(h.gh.GraphQLInputs(t))
				_, err = h.poll(now.Add(time.Second))
				var deferred *workgraph.ConnectorPollDeferred
				if !errors.As(err, &deferred) || !deferred.NotBefore.Equal(retryAt) || len(h.gh.GraphQLInputs(t)) != calls {
					t.Fatalf("poll before deadline was not deferred without queries: %v", err)
				}
				if !reflect.DeepEqual(before, h.observationForRole(watchPRNodeID, "reviewer")) {
					t.Fatal("rate limit changed observation")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			_, entry = readState()
			retained := entry["github_pr_teams"].(map[string]any)
			if !reflect.DeepEqual(retained["teams"], cache["teams"]) || retained["refreshed_at"] != cache["refreshed_at"] {
				t.Fatalf("failed refresh replaced good cache: %#v", retained)
			}
			request := h.observationForRole(watchPRNodeID, "reviewer")["review_request"]
			if payloadValue(request, "unresolved_team") == true || !reflect.DeepEqual(payloadValue(request, "teams"), []any{"acme/platform"}) {
				t.Fatalf("cached attribution lost: %#v", request)
			}
			status, err := workgraph.ListConnectors(workgraph.ConnectorListConfig{HomeDir: h.homeDir})
			if err != nil || !strings.Contains(status.Message, "team membership unavailable") {
				t.Fatalf("missing team warning: %+v %v", status, err)
			}

			calls := len(h.gh.GraphQLInputs(t))
			h.mustPoll(retryAt, teamResponse(t), reviewerStatus(t, node))
			if len(h.gh.GraphQLInputs(t)) != calls+2 {
				t.Fatal("next eligible poll did not retry membership")
			}
			_, entry = readState()
			if entry["github_pr_teams"].(map[string]any)["refreshed_at"] != retryAt.Format(time.RFC3339Nano) {
				t.Fatal("successful refresh time did not advance")
			}
			status, err = workgraph.ListConnectors(workgraph.ConnectorListConfig{HomeDir: h.homeDir})
			if err != nil || strings.Contains(status.Message, "team membership unavailable") {
				t.Fatalf("warning did not clear: %+v %v", status, err)
			}
			h.mustPoll(retryAt.Add(time.Minute), reviewerStatus(t, node))
			if len(h.gh.GraphQLInputs(t)) != calls+3 {
				t.Fatal("successful membership refreshed before one hour")
			}
			if len(h.events()) != 1 {
				t.Fatal("label refresh emitted a request transition")
			}
		})
	}
}

func TestGitHubPRWatchReviewerLifecycle(t *testing.T) {
	for _, state := range []string{"MERGED", "CLOSED", "archived", "missing", "lag"} {
		t.Run(state, func(t *testing.T) {
			h := reviewerHarness(t, reviewerParams)
			h.mustPoll(watchBase, reviewerStatus(t, requestedPR(watchPRNodeID, watchIdentity)))
			follow := followUpNode(state, func(node map[string]any) {
				node["mergedAt"], node["closedAt"] = watchTime(time.Minute), watchTime(time.Minute)
			})
			if state == "archived" || state == "lag" {
				follow = requestedPR(watchPRNodeID, watchIdentity)
				follow["repository"] = map[string]any{"isArchived": state == "archived"}
			}
			var value any = follow
			polls := 1
			if state == "missing" {
				value = nil
				polls = 6
			}
			if state == "lag" {
				polls = 6
			}
			for i := 1; i <= polls; i++ {
				h.mustPoll(watchBase.Add(time.Duration(i)*5*time.Minute), reviewerStatus(t), followUpResponse(t, value))
				if i < polls && h.observationCount() != 1 {
					t.Fatal("missing PR disappeared before six polls")
				}
			}
			expected := "unwatched"
			if state == "MERGED" || state == "CLOSED" {
				expected = strings.ToLower(state)
			}
			events := h.eventsOfType("github.pull_request." + expected)
			if len(events) != 1 || h.observationCount() != 0 {
				t.Fatalf("wrong lifecycle: %v", eventTypes(h.events()))
			}
			for _, input := range h.gh.GraphQLInputs(t) {
				if strings.Contains(input, "timelineItems") {
					t.Fatal("reviewer fetched details")
				}
			}
		})
	}
}

func TestGitHubPRWatchFailedReviewerKeepsPriorState(t *testing.T) {
	for _, failure := range []string{
		`{"data":{"search":{"issueCount":2,"nodes":[]}}}`,
		`{"errors":[{"type":"FORBIDDEN","message":"SAML enforcement"}]}`,
	} {
		h := reviewerHarness(t, `{"roles":["author","reviewer"]}`)
		h.mustPoll(watchBase, statusResponse(t), reviewerStatus(t, requestedPR(watchPRNodeID, watchIdentity)))
		h.gh.Queue(t, statusResponse(t, watchPR(func(pr *prFixture) { pr.ID = "author-pr" })))
		h.gh.Queue(t, detailResponse(t, detailFixture{ID: "author-pr"}))
		h.gh.Queue(t, failure)
		if _, err := h.poll(watchBase.Add(5 * time.Minute)); err == nil {
			t.Fatal("expected reviewer failure")
		}
		if h.observationCount() != 2 || len(h.events()) != 2 {
			t.Fatal("failed reviewer changed state or prevented author capture")
		}
		if countRows(t, h.dbPath, `SELECT COUNT(*) FROM github_pr_observations WHERE role='reviewer' AND observed_at=?`, watchTime(0)) != 1 {
			t.Fatal("failed reviewer observation changed")
		}
	}
}

func TestGitHubPRWatchTruncatedReviewerRequestsDoNotWithdraw(t *testing.T) {
	h := reviewerHarness(t, `{"roles":["reviewer"],"review_requests":"direct"}`)
	h.mustPoll(watchBase, reviewerStatus(t, requestedPR(watchPRNodeID, watchIdentity)))
	partial := requestedPR(watchPRNodeID, "someone-else")
	partial["reviewRequests"].(map[string]any)["totalCount"] = 51
	h.mustPoll(watchBase.Add(5*time.Minute), reviewerStatus(t), followUpResponse(t, partial))
	if h.observationCount() != 1 || len(h.events()) != 1 {
		t.Fatal("truncated requests falsely withdrew the viewer request")
	}
	h.mustPoll(watchBase.Add(10*time.Minute), reviewerStatus(t, partial))
	if h.observationCount() != 1 || len(h.events()) != 1 {
		t.Fatal("truncated status falsely withdrew the viewer request")
	}
}

func TestGitHubPRWatchTeamMembershipFailureAtConnectIsWarning(t *testing.T) {
	h := newUnconnectedPRWatchHarness(t)
	h.gh.Queue(t, probeResponse(t))
	h.gh.Queue(t, `{"data":{"viewer":null},"errors":[{"type":"FORBIDDEN","message":"missing read:org"}]}`)
	if _, err := workgraph.ConnectGitHubPRWatch(workgraph.ConnectorConnectConfig{HomeDir: h.homeDir, GitHubCommand: h.gh.Path, ParamsJSON: json.RawMessage(reviewerParams)}); err != nil {
		t.Fatalf("membership failure blocked connection: %v", err)
	}
	h.mustPoll(watchBase, `{"data":{"viewer":null},"errors":[{"type":"FORBIDDEN","message":"missing read:org"}]}`, reviewerStatus(t, requestedPR(watchPRNodeID, "acme/platform")))
	if len(h.events()) != 1 || payloadValue(h.events()[0].Payload, "triage", "request", "unresolved_team") != true {
		t.Fatalf("missing unresolved request: %v", h.events())
	}
	status, err := workgraph.ListConnectors(workgraph.ConnectorListConfig{HomeDir: h.homeDir})
	if err != nil || !strings.Contains(status.Message, "team membership unavailable") {
		t.Fatalf("membership failure not reported: %+v %v", status, err)
	}
}

func nextTextLines(t *testing.T, h *prWatchHarness, args ...string) []string {
	t.Helper()
	output, err := h.cli(append([]string{"next", "--home", h.homeDir}, args...)...)
	if err != nil {
		t.Fatalf("next failed: %s %v", output, err)
	}
	return strings.Split(output, "\n")
}

func linesContaining(lines []string, value string) []string {
	matches := []string{}
	for _, line := range lines {
		if strings.Contains(line, value) {
			matches = append(matches, line)
		}
	}
	return matches
}

func TestGitHubPRWatchBotPRInBothRolesRendersOnceOnYourBehalf(t *testing.T) {
	h := reviewerHarness(t, `{"roles":["author","reviewer"]}`)
	copilot := watchActor("copilot-swe-agent", true)
	authored := watchPR().node()
	authored["author"] = copilot
	requested := requestedPR(watchPRNodeID, watchIdentity)
	requested["author"] = copilot
	question := commentFixture{ID: "PRRC_q", Author: "alice", Body: "Why this default?", CreatedAt: watchTime(-time.Hour)}
	h.mustPoll(watchBase, reviewerStatus(t, authored),
		detailResponse(t, detailFixture{ID: watchPRNodeID, Threads: []threadFixture{{ID: "PRRT_one", Path: "settings.go", Line: 18, Comments: []commentFixture{question}}}}),
		reviewerStatus(t, requested))
	for _, role := range []string{"author", "reviewer"} {
		observation := h.observationForRole(watchPRNodeID, role)
		if observation == nil || observation["author_login"] != "copilot-swe-agent" || observation["author_kind"] != "bot" {
			t.Fatalf("%s observation did not record the bot author: %#v", role, observation)
		}
	}
	result, err := workgraph.Next(workgraph.NextConfig{HomeDir: h.homeDir, DatabasePath: h.dbPath, Now: watchBase.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, item := range result.Items {
		ids[item.ID] = true
	}
	if len(result.Items) != 2 || len(ids) != 2 {
		t.Fatalf("expected the reply and the review request as separate items, got %#v", result.Items)
	}
	lines := nextTextLines(t, h)
	headers := linesContaining(lines, "acme/widget#42")
	if len(headers) != 1 || !strings.HasPrefix(strings.TrimSpace(headers[0]), "acme/widget#42 Add settings helper") || !strings.Contains(headers[0], "copilot-swe-agent, on your behalf") {
		t.Fatalf("expected one header for the PR opened on the user's behalf, got %q", lines)
	}
	if strings.Contains(headers[0], "[bot]") {
		t.Fatalf("a PR opened on the user's behalf is not a bot review request: %q", headers[0])
	}
	text := strings.Join(lines, "\n")
	header := strings.Index(text, headers[0])
	for _, reason := range []string{"reply requested on settings.go:18", "review requested"} {
		if at := strings.Index(text, reason); at < header {
			t.Fatalf("expected %q under the PR header, got %q", reason, text)
		}
	}
	for id := range ids {
		if strings.Contains(text, id) {
			t.Fatalf("text output leaked item id %q: %q", id, text)
		}
	}
}

func TestGitHubPRWatchBotReviewRequestsAreTaggedAndIgnorable(t *testing.T) {
	dependabot := func() map[string]any {
		node := requestedPR("PR_dependabot", "acme/platform")
		node["author"] = watchActor("dependabot", true)
		node["number"], node["title"] = 7, "Bump lodash"
		node["repository"] = map[string]any{"nameWithOwner": "acme/api"}
		node["url"] = "https://github.com/acme/api/pull/7"
		return node
	}
	human := func() map[string]any {
		node := requestedPR("PR_human", watchIdentity)
		node["repository"] = map[string]any{"nameWithOwner": "acme/web"}
		node["url"] = "https://github.com/acme/web/pull/42"
		return node
	}

	h := reviewerHarness(t, reviewerParams)
	h.mustPoll(watchBase, reviewerStatus(t, dependabot(), human()))
	lines := nextTextLines(t, h)
	bot := linesContaining(lines, "acme/api#7")
	if len(bot) != 1 || !strings.Contains(bot[0], "Bump lodash") || !strings.Contains(bot[0], "[bot]") {
		t.Fatalf("expected the bot review request tagged bot, got %q", lines)
	}
	person := linesContaining(lines, "acme/web#42")
	if len(person) != 1 || strings.Contains(person[0], "[bot]") || strings.Contains(person[0], "on your behalf") {
		t.Fatalf("expected the human review request untagged, got %q", lines)
	}

	for _, ignore := range []string{`"ignore_authors":["bots"]`, `"ignore_authors":["app/dependabot"]`, `"ignore_repositories":["acme/api"]`} {
		t.Run(ignore, func(t *testing.T) {
			h := reviewerHarness(t, `{"roles":["reviewer"],`+ignore+`}`)
			h.mustPoll(watchBase, reviewerStatus(t, dependabot(), human()))
			if h.observationCount() != 1 || h.observationForRole("PR_human", "reviewer") == nil {
				t.Fatalf("expected only the human review request to be watched, got %d observations", h.observationCount())
			}
			lines := nextTextLines(t, h)
			if len(linesContaining(lines, "acme/api#7")) != 0 || len(linesContaining(lines, "acme/web#42")) != 1 {
				t.Fatalf("ignored review request reached next: %q", lines)
			}
			params := watchBridgeParams(t, h.homeDir)
			key, _, _ := strings.Cut(strings.Trim(ignore, `"`), `"`)
			if params[key] == nil {
				t.Fatalf("expected %s persisted in bridge params, got %#v", key, params)
			}
		})
	}
}

func TestGitHubPRWatchIgnoredRepositoriesApplyToBothRoles(t *testing.T) {
	h := reviewerHarness(t, `{"roles":["author","reviewer"],"ignore_repositories":["acme/api"],"ignore_authors":["octo"]}`)
	ignoredAuthored := watchPR(func(pr *prFixture) { pr.ID, pr.Repo, pr.Number = "PR_ignored_author", "acme/api", 3 })
	kept := watchPR()
	ignoredRequest := requestedPR("PR_ignored_review", watchIdentity)
	ignoredRequest["repository"] = map[string]any{"nameWithOwner": "acme/api"}
	h.mustPoll(watchBase, statusResponse(t, ignoredAuthored, kept),
		detailResponse(t, detailFixture{ID: watchPRNodeID}),
		reviewerStatus(t, ignoredRequest))
	if h.observationCount() != 1 || h.observationForRole(watchPRNodeID, "author") == nil {
		t.Fatalf("expected only the authored PR outside ignored repositories, got %d observations", h.observationCount())
	}

	for _, params := range []string{`{"ignore_repositories":["acme"]}`, `{"ignore_repositories":["acme/api/extra"]}`, `{"ignore_authors":[""]}`} {
		h := newUnconnectedPRWatchHarness(t)
		h.gh.Queue(t, probeResponse(t))
		if _, err := workgraph.ConnectGitHubPRWatch(workgraph.ConnectorConnectConfig{HomeDir: h.homeDir, GitHubCommand: h.gh.Path, ParamsJSON: json.RawMessage(params)}); err == nil {
			t.Fatalf("expected %s to be rejected", params)
		}
	}
}

func TestGitHubPRWatchTruncatedReviewRequestsStayListed(t *testing.T) {
	located := func(node map[string]any, repository string, number int) map[string]any {
		node["number"] = number
		node["repository"] = map[string]any{"nameWithOwner": repository}
		node["url"] = fmt.Sprintf("https://github.com/%s/pull/%d", repository, number)
		return node
	}
	truncated := located(requestedPR("PR_truncated", "acme/platform"), "acme/api", 7)
	truncated["reviewRequests"].(map[string]any)["totalCount"] = 51
	complete := located(requestedPR("PR_complete", "acme/platform"), "acme/web", 8)

	h := reviewerHarness(t, reviewerParams)
	h.mustPoll(watchBase, reviewerStatus(t, truncated, complete))
	request := h.observationForRole("PR_truncated", "reviewer")["review_request"].(map[string]any)
	if request["incomplete"] != true || request["direct"] != false {
		t.Fatalf("expected the truncated request recorded as incomplete, got %#v", request)
	}
	if complete := h.observationForRole("PR_complete", "reviewer")["review_request"].(map[string]any); complete["incomplete"] != nil {
		t.Fatalf("expected the complete request without incomplete, got %#v", complete)
	}
	output, err := h.cli("next", "--home", h.homeDir)
	if err != nil {
		t.Fatalf("next failed: %s %v", output, err)
	}
	listed := strings.Index(output, "\n  acme/api#7 ")
	summary := strings.Index(output, "\nTeam review requests via acme/platform: 1 PR across 1 repo\n")
	if listed < 0 || summary < 0 || listed > summary || strings.Index(output[summary:], "acme/api#7") >= 0 {
		t.Fatalf("expected the truncated request listed under Waiting on you and only the complete one summarized: %s", output)
	}
	today, err := workgraph.Today(workgraph.TodayConfig{HomeDir: h.homeDir, Now: watchBase.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(today.Message, "Waiting on you: 1 items + 1 team review request (workgraph next)") {
		t.Fatalf("today: %s", today.Message)
	}
}
