package facts

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestGitHubPRWatchConnectValidatesGHAndStaysOptIn(t *testing.T) {
	t.Skip("TBD: github prs connect validates gh auth status and a viewer probe, marks github.pr_watch ready, and github connect alone leaves it disabled")
}

func TestGitHubPRWatchFirstSightEmitsOneObservedEventPerPR(t *testing.T) {
	t.Skip("TBD: first poll emits exactly one observed event per open authored PR with full state, baseline ids, and the awaiting-user backlog, and no other transitions")
}

func TestGitHubPRWatchObservationResetNeitherDuplicatesNorLoses(t *testing.T) {
	t.Skip("TBD: after deleting observations, a comment created meanwhile emits one review_comment, comments before baseline_at emit nothing, and no second observed is stored")
}

func TestGitHubPRWatchUnchangedStateProducesNoNewEvent(t *testing.T) {
	t.Skip("TBD: polling an identical observation twice leaves the transition event count unchanged and advances observed_at and verified_at")
}

func TestGitHubPRWatchChecksWaitForTheWholeRunSet(t *testing.T) {
	t.Skip("TBD: one failed and one pending context emits nothing; when the pending context finishes exactly one checks_failed is emitted; a new head that goes pending emits nothing")
}

func TestGitHubPRWatchCheckEventsAreKeyedByExecution(t *testing.T) {
	t.Skip("TBD: re-polling the same terminal runs emits nothing; a re-run with new run ids, or the same id and a new completedAt, that finishes failed emits a second checks_failed")
}

func TestGitHubPRWatchNewReviewsAndCommentsEmitByProviderID(t *testing.T) {
	t.Skip("TBD: each new review and comment id emits one event oldest first with actor_kind, actor_association, and has_suggestion; three new comments in one thread emit three events")
}

func TestGitHubPRWatchTimelinePaginationCatchesUp(t *testing.T) {
	t.Skip("TBD: 150 conversation comments emit 150 events in one poll; a 1500-comment backlog emits 1000 with timeline_cursor stored and the dimension unknown, then the remaining 500 on the next poll with no other change")
}

func TestGitHubPRWatchRemovesIgnoredActorsBeforeStateAndCapturesBots(t *testing.T) {
	t.Skip("TBD: a comment and a CHANGES_REQUESTED review by an ignore_actors login emit nothing, do not make the thread await the user, and open no item; a Bot comment emits review_comment with actor_kind bot; an identity_login comment emits author_replied")
}

func TestGitHubPRWatchAttributesRepliesByMarkerAndPredecessor(t *testing.T) {
	t.Skip("TBD: a marker-ending identity_login comment emits author_replied responder agent and the following reviewer comment has previous_responder agent; another account ending in the marker is a review_comment")
}

func TestGitHubPRWatchIgnoresPendingDraftComments(t *testing.T) {
	t.Skip("TBD: a PENDING comment by the user on a thread awaiting the user emits nothing and the thread still awaits the user; after the review is submitted author_replied is emitted and the thread awaits other")
}

func TestGitHubPRWatchReadinessAndConflictsUseTheLastKnownValue(t *testing.T) {
	t.Skip("TBD: ready emits ready_to_merge; a new unresolved thread emits readiness_lost reasons threads; resolving emits a second ready_to_merge on the same SHA; BLOCKED or BEHIND is not ready; after first sight with mergeable UNKNOWN a later CONFLICTING emits merge_conflict and a later ready state emits ready_to_merge; unknown values do not transition")
}

func TestGitHubPRWatchLeavingTheOpenSetIsResolvedByFollowUpQuery(t *testing.T) {
	t.Skip("TBD: a PR merged right after a new comment emits the review_comment and merged; closed emits closed; archived emits unwatched out_of_scope; a PR still matching search is kept six polls then emits unwatched left_search; a FORBIDDEN PR's items are stale while others are fresh and after six polls it emits unwatched unreachable; a reopened PR emits reopened")
}

func TestGitHubPRWatchRateLimitsSetOneDeadline(t *testing.T) {
	t.Skip("TBD: a stored low budget defers the poll and a RATE_LIMITED error with Retry-After 120 records a failure; observations are unchanged and neither a hint nor github prs capture polls before the deadline")
}

func TestGitHubPRWatchAuthExpiryStopsOnlyThisConnector(t *testing.T) {
	t.Skip("TBD: gh HTTP 401 Bad credentials marks github.pr_watch setup error and stops its poller while file capture and other connectors continue")
}

func TestGitHubPRWatchNeverPersistsAToken(t *testing.T) {
	t.Skip("TBD: with a token sentinel from fake gh auth token and GH_TOKEN, no home file or database row contains it and gh auth token is never invoked")
}

func TestConnectorPollerDeferralDoesNotCountAsFailure(t *testing.T) {
	for _, failures := range []int{0, 2} {
		t.Run(fmt.Sprintf("previous failures %d", failures), func(t *testing.T) {
			homeDir := filepath.Join(t.TempDir(), ".workgraph")
			if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(homeDir, "notion.json"), []byte(`{"access_token":"test-token"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			const lastSuccess = "2026-06-10T09:00:00Z"
			if err := os.WriteFile(filepath.Join(homeDir, "connectors.json"), []byte(fmt.Sprintf(
				`{"connectors":{"git":{"enabled":false},"notion":{"enabled":true,"setup_state":"ready","interval":"1h","consecutive_failures":%d,"last_success_at":%q}}}`,
				failures, lastSuccess)), 0o600); err != nil {
				t.Fatal(err)
			}
			starts := make(chan time.Time, 8)
			deadlines := make(chan time.Time, 1)
			attempts := 0
			client := &http.Client{Transport: prWatchPollTransport(func(request *http.Request) (*http.Response, error) {
				started := time.Now()
				starts <- started
				attempts++
				if attempts == 1 {
					deadline := started.Add(time.Second)
					deadlines <- deadline
					return nil, &workgraph.ConnectorPollDeferred{Reason: "budget low", NotBefore: deadline}
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"results":[],"has_more":false}`)),
					Request:    request,
				}, nil
			})}
			capture, err := workgraph.StartRun(workgraph.RunConfig{
				HomeDir: homeDir, NotionHTTPClient: client,
				ConnectorRetryInitial: 10 * time.Millisecond, ConnectorRetryMax: 20 * time.Millisecond,
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- capture.Run(ctx) }()
			defer func() {
				cancel()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			select {
			case <-starts:
			case <-time.After(3 * time.Second):
				t.Fatal("connector did not poll immediately")
			}
			deadline := <-deadlines
			var deferred workgraph.ConnectorStatus
			for until := time.Now().Add(500 * time.Millisecond); time.Now().Before(until); {
				result, err := workgraph.StatusConnectors(workgraph.ConnectorListConfig{HomeDir: homeDir})
				if err == nil {
					for _, status := range result.Connectors {
						if status.ID == "notion" && status.LastPoll != "" {
							deferred = status
						}
					}
					if deferred.LastPoll != "" {
						if !strings.Contains(result.Message, "deferred budget low") {
							t.Fatalf("status must show deferral reason: %s", result.Message)
						}
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
			}
			if deferred.LastPoll == "" || deferred.ConsecutiveFailures != failures || deferred.LastError != "" || deferred.LastSuccess != lastSuccess {
				t.Fatalf("deferral must preserve failure count and last success without recording an error: %+v", deferred)
			}
			notBefore, err := workgraph.ConnectorPollNotBefore(homeDir, "notion")
			if err != nil || !notBefore.Equal(deadline) || deferred.NotBefore != deadline.UTC().Format(time.RFC3339Nano) {
				t.Fatalf("expected persisted deadline %s, got %s and status %q (%v)", deadline, notBefore, deferred.NotBefore, err)
			}
			next, err := time.Parse(time.RFC3339Nano, deferred.NextPoll)
			if err != nil || !next.Equal(deadline) {
				t.Fatalf("expected resume at %s, got %q (%v)", deadline, deferred.NextPoll, err)
			}
			select {
			case started := <-starts:
				if started.Before(deadline) {
					t.Fatalf("poll started before deadline: %s < %s", started, deadline)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("deferred connector did not resume at its deadline")
			}
			select {
			case <-starts:
				t.Fatal("successful connector did not return to its normal one-hour interval")
			case <-time.After(100 * time.Millisecond):
			}
		})
	}
}

type prWatchPollTransport func(*http.Request) (*http.Response, error)

func (transport prWatchPollTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestGitHubPRWatchStoresCommentTextWithUntrustedFlag(t *testing.T) {
	t.Skip("TBD: comment body is stored only under payload.untrusted with controls stripped, newlines and suggestion blocks preserved, body_truncated and body_sha256, absent from summary, event label, and attention item; comment_body_max_runes 0 stores no body text")
}

func TestGitHubPRWatchIncompleteRoleFailsWithoutChanges(t *testing.T) {
	t.Skip("TBD: a role with GraphQL errors, issueCount above received nodes, or a count over its ceiling commits no events and no closed events, while a complete role in the same poll commits")
}

func TestGitHubPRWatchInvokesOnlyReadOnlyGraphQLAndTypeWildcardFilters(t *testing.T) {
	t.Skip("TBD: no gh api graphql invocation contains mutation; events today --type github.pull_request.* lists transitions but not snapshots or issues")
}

func TestGitHubPRWatchSnapshotEnrichmentIsOrderIndependent(t *testing.T) {
	t.Skip("TBD: watch-then-participant and participant-then-watch both leave one snapshot row with branch, commit, the original matched_scopes, and the participant timestamp")
}

func TestGitHubPRWatchAssociatesFromObservedEventsOnly(t *testing.T) {
	t.Skip("TBD: head_sha on an observed event associates with a git.commit; transition events are not candidates; untrusted URLs create no association")
}

func TestGitHubPRWatchOnlyTheUsersOwnActionsArePersonalActivity(t *testing.T) {
	t.Skip("TBD: with observed, review_comment, checks_failed, ready_to_merge, an agent author_replied, and an author author_replied stored today, today counts only the author reply; events today lists all of them")
}

func TestGitHubPRWatchResumeRelevantUpdatesIncludeOnlyNeedsUserTransitions(t *testing.T) {
	f := newNextFixture(t)
	f.exec(t, `DELETE FROM events`)
	if err := workgraph.RecordResumeCheckIn(workgraph.ResumeConfig{HomeDir: f.home}, f.now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		typ       string
		needsUser bool
	}{
		{"review_comment", true}, {"checks_failed", true}, {"observed", true}, {"checks_passed", true}, {"author_replied", true}, {"review_comment", false}, {"changes_requested", false}, {"merge_conflict", false}, {"ready_to_merge", false}, {"review_requested", false},
	}
	for i, entry := range cases {
		at := f.now.Add(time.Duration(i-20) * time.Minute).Format(time.RFC3339Nano)
		f.event(t, entry.typ+at, entry.typ, at, map[string]any{"node_id": "PR0", "role": "author", "triage": map[string]any{"needs_user": entry.needsUser}})
	}
	resume, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: f.home, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, event := range resume.Updates {
		got = append(got, event.Type)
	}
	if !reflect.DeepEqual(got, []string{"github.pull_request.checks_failed", "github.pull_request.review_comment"}) || resume.UpdateCount != 2 {
		t.Fatalf("relevant updates: %v (%d)", got, resume.UpdateCount)
	}
}

func TestGitHubPRWatchPollsAreFenced(t *testing.T) {
	t.Skip("TBD: a github prs capture and a daemon poll that see the same new comment store one review_comment; a poll in flight during github prs disconnect, connectors disable github.pr_watch, or a scope change commits nothing and leaves no observations")
}

func TestGitHubPRWatchFencingRecoversFromAPartialWrite(t *testing.T) {
	t.Skip("TBD: when connectors.json shows the watch disabled but github_pr_watch_state is enabled, the next daemon start disables it, deletes observations, and increments poll_seq")
}

func TestGitHubPRWatchLongThreadsAreUnknown(t *testing.T) {
	t.Skip("TBD: a thread with 101 comments marks thread values unknown and opens no reply item from carried state")
}

func TestGitHubPRWatchBacklogItemsCiteTheObservedEvent(t *testing.T) {
	f := newNextFixture(t)
	f.exec(t, `DELETE FROM events`)
	f.exec(t, `DELETE FROM github_pr_observations`)
	o := f.observations[0]
	o["opinionated_reviews"] = f.observations[2]["opinionated_reviews"]
	o["checks_state"] = "failed"
	o["check_runs"] = f.observations[3]["check_runs"]
	o["mergeable"] = "CONFLICTING"
	baselineAt := f.now.Add(-time.Hour).Format(time.RFC3339Nano)
	o["baseline_at"] = baselineAt
	baseline := map[string]any{
		"review_ids": []string{"R1"},
		"threads":    []any{map[string]any{"thread_id": "T1", "last_comment_id": "C1"}},
		"head_sha":   "abc123", "run_fingerprint": "fixturehash1",
	}
	id := "github.pull_request.observed:PR0:author"
	f.event(t, id, "observed", baselineAt, map[string]any{"node_id": "PR0", "role": "author", "baseline_at": baselineAt, "baseline": baseline, "state": map[string]any{"checks_state": "failed", "mergeable": "CONFLICTING"}, "triage": map[string]any{"needs_user": true}, "untrusted": map[string]any{"threads": []any{map[string]any{"thread_id": "T1", "last_comment_id": "C1", "body": "BASELINE PRIVATE", "comment_url": "https://github.com/acme/demo/pull/1#discussion_r1"}}}})
	f.save(t, o)
	items := f.items(t, "--include-untrusted")
	if len(items) != 4 {
		t.Fatalf("backlog: %+v", items)
	}
	for _, item := range items {
		if item.OpenedBy != id || item.Since.Format(time.RFC3339Nano) != baselineAt || !reflect.DeepEqual(item.Evidence, []string{id}) {
			t.Fatalf("opening evidence: %+v", item)
		}
	}
	if items[0].Untrusted["body"] != "BASELINE PRIVATE" {
		t.Fatalf("baseline body: %v", items[0].Untrusted)
	}
	f.exec(t, `DELETE FROM github_pr_observations`)
	o["observed_at"] = f.now.Add(time.Minute).Format(time.RFC3339Nano)
	o["baseline_at"] = o["observed_at"]
	f.save(t, o)
	reset := f.items(t, "--include-untrusted")
	if !reflect.DeepEqual(items, reset) {
		t.Fatalf("reset changed items:\nbefore %+v\nafter %+v", items, reset)
	}
}

func TestGitHubPRWatchDirectAndTeamReviewRequestsEnterReviewerSet(t *testing.T) {
	t.Skip("TBD: with direct_and_team a direct request and a cached-team request are watched in the reviewer role labeled direct and via org/team; an uncached team is via team (unresolved); with direct the team-only PR is absent; no detail query runs for reviewer-role PRs")
}

func TestGitHubPRWatchFirstReviewerPollIsBacklogAndLaterRequestsAreTransitions(t *testing.T) {
	t.Skip("TBD: the first reviewer poll emits only observed per PR; a request appearing on a later poll emits observed and review_requested")
}

func TestGitHubPRWatchReviewRequestsCloseAtOnce(t *testing.T) {
	t.Skip("TBD: a submitted review emits review_submitted and review_request_removed reason reviewed and the item closes out_of_scope; a request withdrawn before any review emits reason withdrawn; a PENDING draft review emits nothing; a re-request emits a new review_requested")
}

func TestGitHubPRWatchIgnoredTeamsAreAbsent(t *testing.T) {
	t.Skip("TBD: a PR requested only through an ignore_teams team is absent from the reviewer set")
}

func TestGitHubPRWatchPushHintWakesThePoller(t *testing.T) {
	t.Skip("TBD: with a 5m interval a hint 30s after a poll triggers a poll at the next 2s check and a second hint during that poll triggers exactly one follow-up; a hint within 10s of the previous poll start waits until 10s; a hint naming a PR whose thread was resolved without an updatedAt change forces its detail query and closes the reply item; a hint before the not-before deadline waits for it")
}
