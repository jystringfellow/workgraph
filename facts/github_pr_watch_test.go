package facts

import "testing"

func TestGitHubPRWatchRateLimitsSetOneDeadline(t *testing.T) {
	t.Skip("TBD: a stored low budget defers the poll and a RATE_LIMITED error with Retry-After 120 records a failure; observations are unchanged and neither a hint nor github prs capture polls before the deadline")
}

func TestConnectorPollerDeferralDoesNotCountAsFailure(t *testing.T) {
	t.Skip("TBD: a deferred poll keeps consecutive_failures unchanged and sets the not-before deadline; a connector returning neither outcome keeps today's schedule")
}

func TestGitHubPRWatchResumeRelevantUpdatesIncludeOnlyNeedsUserTransitions(t *testing.T) {
	t.Skip("TBD: after a resume check-in, a review_comment awaiting the user and a checks_failed appear under Relevant updates since last check-in; a needs_user observed, checks_passed, and author_replied do not")
}

func TestGitHubPRWatchBacklogItemsCiteTheObservedEvent(t *testing.T) {
	t.Skip("TBD: on first sight a PR with failed checks, changes requested, a conflict, and an awaiting thread opens four items whose opened_by is the observed event and since is baseline_at; after an observation reset the item ids and opened_by are unchanged")
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
