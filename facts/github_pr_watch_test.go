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
