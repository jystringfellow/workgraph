package facts

import "testing"

func TestGitHubAuthoredPRsConnectValidatesGHAndStaysOptIn(t *testing.T) {
	t.Skip("TBD: github prs connect validates gh auth status and a viewer probe, marks github.authored_prs ready, and github connect alone leaves it disabled")
}

func TestGitHubAuthoredPRsFirstSightEmitsOneObservedEventPerPR(t *testing.T) {
	t.Skip("TBD: first poll emits exactly one observed event per open PR with full state, baseline_at, and the awaiting-author backlog, and no other transitions")
}

func TestGitHubAuthoredPRsObservationResetNeitherDuplicatesNorLoses(t *testing.T) {
	t.Skip("TBD: after deleting observations, a comment created meanwhile emits one review_comment, comments before baseline_at emit nothing, and no second observed is stored")
}

func TestGitHubAuthoredPRsSameSHAAndStateProducesNoNewEvent(t *testing.T) {
	t.Skip("TBD: polling an identical observation twice leaves the transition event count unchanged and only advances observed_at")
}

func TestGitHubAuthoredPRsChecksWaitForTheWholeRunSet(t *testing.T) {
	t.Skip("TBD: one failed and one pending context emits nothing; when the pending context finishes exactly one checks_failed is emitted; a new head that goes pending emits nothing")
}

func TestGitHubAuthoredPRsCheckEventsAreKeyedByRunIdentity(t *testing.T) {
	t.Skip("TBD: re-polling the same terminal runs emits nothing; a re-run with new run ids that finishes failed emits a second checks_failed for the SHA")
}

func TestGitHubAuthoredPRsNewReviewsAndCommentsEmitByProviderID(t *testing.T) {
	t.Skip("TBD: each new review and comment id emits one event oldest first with actor_kind, actor_association, and has_suggestion; three new comments in one thread emit three events")
}

func TestGitHubAuthoredPRsTimelinePaginationIsLossless(t *testing.T) {
	t.Skip("TBD: 150 conversation comments between polls emit 150 events across two pages; past the page limit timeline_since is unchanged, the dimension is unknown, and the next complete fetch emits the rest")
}

func TestGitHubAuthoredPRsIgnoresConfiguredActorsAndCapturesBotsAndAuthorReplies(t *testing.T) {
	t.Skip("TBD: ignore_actors comments emit nothing; a Bot comment emits review_comment with actor_kind bot; an identity_login comment emits author_replied and flips the thread to awaiting reviewer")
}

func TestGitHubAuthoredPRsAttributesRepliesByMarkerAndPredecessor(t *testing.T) {
	t.Skip("TBD: a marker-ending identity_login comment emits author_replied responder agent; the next reviewer comment in the same poll has previous_responder agent and a second has reviewer; another account ending in the marker is a review_comment")
}

func TestGitHubAuthoredPRsReadinessIsLostAndRegained(t *testing.T) {
	t.Skip("TBD: ready emits ready_to_merge; a new unresolved thread emits readiness_lost with reasons threads; resolving emits a second ready_to_merge on the same SHA; mergeable UNKNOWN and more than 20 opinionated reviews do not transition")
}

func TestGitHubAuthoredPRsLeavingTheOpenSetIsResolvedByFollowUpQuery(t *testing.T) {
	t.Skip("TBD: merged and closed emit terminal events and drop the observation; OPEN keeps it; archived drops silently; FORBIDDEN is reported unreachable; a reopened PR emits reopened")
}

func TestGitHubAuthoredPRsRateLimitedPollDefersAndPreservesObservations(t *testing.T) {
	t.Skip("TBD: a stored GraphQL remaining below 200 before resetAt defers the poll with no query, no events, byte-identical observations, rate limited until status, and unchanged consecutive_failures")
}

func TestGitHubAuthoredPRsSecondaryRateLimitBacksOffWithoutLosingState(t *testing.T) {
	t.Skip("TBD: HTTP 403 secondary rate limit or RATE_LIMITED errors record a failure with retry-after, schedule next_poll_at at or after Retry-After, and leave observations unchanged")
}

func TestGitHubAuthoredPRsAuthExpiryStopsOnlyThisConnector(t *testing.T) {
	t.Skip("TBD: gh HTTP 401 Bad credentials marks github.authored_prs setup error and stops its poller while file capture and other connectors continue")
}

func TestGitHubAuthoredPRsNeverPersistsAToken(t *testing.T) {
	t.Skip("TBD: with a token sentinel from fake gh auth token and GH_TOKEN, no home file or database row contains it and gh auth token is never invoked")
}

func TestGitHubAuthoredPRsStoresCommentTextWithUntrustedFlag(t *testing.T) {
	t.Skip("TBD: comment body is stored only under payload.untrusted with controls stripped, newlines and suggestion blocks preserved, body_truncated and body_sha256, absent from summary and today label; comment_body_max_runes 0 stores no body text")
}

func TestGitHubAuthoredPRsIncompleteSnapshotFailsWithoutChanges(t *testing.T) {
	t.Skip("TBD: GraphQL errors or issueCount above received nodes fail the poll with no events and no closed events for missing PRs")
}

func TestConnectorPollerDeferralDoesNotCountAsFailure(t *testing.T) {
	t.Skip("TBD: a deferred poll keeps consecutive_failures unchanged and schedules next_poll_at at the not-before time; a failure with retry-after waits at least that long; other connectors keep today's schedule")
}

func TestGitHubAuthoredPRsInvokeOnlyReadOnlyGraphQLAndTypeWildcardFilters(t *testing.T) {
	t.Skip("TBD: no gh api graphql invocation contains mutation; events today --type github.pull_request.* lists transitions but not snapshots or issues")
}

func TestGitHubAuthoredPRsEnrichmentSurvivesParticipantPolls(t *testing.T) {
	t.Skip("TBD: an existing github.pull_request row gains branch and commit keeping matched_scopes and timestamp; a newer github poll keeps them; no row is created otherwise; either connector order gives the same row")
}

func TestGitHubAuthoredPRsAssociateFromObservedEventsOnly(t *testing.T) {
	t.Skip("TBD: head_sha on an observed event associates with a git.commit; transition events are not candidates; untrusted URLs create no association; event ids are unchanged across a repository rename")
}

func TestGitHubAuthoredPRsWaitingOnYouListsEachReason(t *testing.T) {
	t.Skip("TBD: fixtures for failed checks, changes requested, an awaiting thread, a human conversation comment, a bot coverage comment, a conflict, and a ready PR list exactly six items in order without the bot comment and without invoking gh, identically in today, resume, and github prs waiting")
}

func TestGitHubAuthoredPRsAgentAnswersLeaveWaitingOnYou(t *testing.T) {
	t.Skip("TBD: a thread whose last comment is a marker agent reply is not listed; a reviewer reply after it is")
}

func TestGitHubAuthoredPRsWaitingOnYouReportsStaleness(t *testing.T) {
	t.Skip("TBD: with setup error or the last successful poll older than three intervals, today and github prs waiting print a warning and unknown dimensions show as unknown")
}
