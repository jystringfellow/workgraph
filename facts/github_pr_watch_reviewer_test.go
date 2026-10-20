package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	h := reviewerHarness(t, reviewerParams)
	runtimePath := filepath.Join(h.homeDir, "connectors.json")
	raw, err := os.ReadFile(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	var runtime map[string]any
	if err := json.Unmarshal(raw, &runtime); err != nil {
		t.Fatal(err)
	}
	cache := runtime["connectors"].(map[string]any)["github.pr_watch"].(map[string]any)["github_pr_teams"].(map[string]any)
	if !strings.Contains(watchJSON(t, cache), "acme/platform") {
		t.Fatalf("team cache missing from connector state: %s", raw)
	}
	cache["refreshed_at"] = watchTime(-time.Hour)
	if err := os.WriteFile(runtimePath, []byte(watchJSON(t, runtime)), 0600); err != nil {
		t.Fatal(err)
	}
	failure := `{"data":{"viewer":null},"errors":[{"type":"FORBIDDEN","message":"read:org unavailable"}]}`
	h.mustPoll(watchBase, failure, reviewerStatus(t, requestedPR(watchPRNodeID, "acme/platform")))
	if event := h.events()[0]; payloadValue(event.Payload, "triage", "request", "unresolved_team") != true {
		t.Fatalf("failed membership did not degrade to unresolved: %s", event.Raw)
	}
	status, err := workgraph.ListConnectors(workgraph.ConnectorListConfig{HomeDir: h.homeDir})
	if err != nil || !strings.Contains(status.Message, "team membership unavailable") {
		t.Fatalf("missing team warning: %+v %v", status, err)
	}
	h.mustPoll(watchBase.Add(59*time.Minute), reviewerStatus(t, requestedPR(watchPRNodeID, "acme/platform")))
	if len(h.gh.GraphQLInputs(t)) != 5 {
		t.Fatal("failed membership refreshed more than hourly")
	}
	h.mustPoll(watchBase.Add(time.Hour), teamResponse(t), reviewerStatus(t, requestedPR(watchPRNodeID, "acme/platform")))
	if len(h.gh.GraphQLInputs(t)) != 7 {
		t.Fatal("membership did not refresh after one hour")
	}
	status, err = workgraph.ListConnectors(workgraph.ConnectorListConfig{HomeDir: h.homeDir})
	if err != nil || strings.Contains(status.Message, "team membership unavailable") {
		t.Fatalf("warning did not clear: %+v %v", status, err)
	}
	if len(h.events()) != 1 {
		t.Fatal("label refresh emitted a request transition")
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
	h.mustPoll(watchBase, reviewerStatus(t, requestedPR(watchPRNodeID, "acme/platform")))
	if len(h.events()) != 1 || payloadValue(h.events()[0].Payload, "triage", "request", "unresolved_team") != true {
		t.Fatalf("missing unresolved request: %v", h.events())
	}
	status, err := workgraph.ListConnectors(workgraph.ConnectorListConfig{HomeDir: h.homeDir})
	if err != nil || !strings.Contains(status.Message, "team membership unavailable") {
		t.Fatalf("membership failure not reported: %+v %v", status, err)
	}
}
