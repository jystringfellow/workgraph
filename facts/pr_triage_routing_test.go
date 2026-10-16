package facts

import "testing"

func TestPRRoutingClassificationFollowsRuleOrder(t *testing.T) {
	t.Skip("TBD: a table of items covers each rule and records the matching rule id; nit: should this be cached? is question, not routine_pattern")
}

func TestPRRoutingFlagsUnclassifiedHumanComments(t *testing.T) {
	t.Skip("TBD: a trusted human comment matching no rule is flag with rule unclassified")
}

func TestPRRoutingHoldsUntrustedActors(t *testing.T) {
	t.Skip("TBD: a CONTRIBUTOR comment is hold and never appears in a packet, whatever its text")
}

func TestPRRoutingTextCannotRaiseAnItemOutOfHold(t *testing.T) {
	t.Skip("TBD: a held item whose body matches routine_patterns stays hold")
}

func TestPRRoutingFlagsHumanReplyAfterAgentReply(t *testing.T) {
	t.Skip("TBD: a human comment after an agent reply is flag with rule human_after_agent and is dispatched until max_agent_replies_per_thread, then hold")
}

func TestPRRoutingShadowModeLaunchesNothing(t *testing.T) {
	t.Skip("TBD: shadow mode records item_classified, attention_raised, and would_dispatch and the fake runner log is empty")
}

func TestPRRoutingDispatchRequiresAgentReplyMarker(t *testing.T) {
	t.Skip("TBD: pr route enable with an empty agent_reply_marker fails and leaves mode unchanged")
}

func TestPRRoutingBatchesItemsPerPRAfterSettle(t *testing.T) {
	t.Skip("TBD: three comments on one PR within settle produce one run with all three items; comments on two PRs produce two runs")
}

func TestPRRoutingSupersedesStaleItems(t *testing.T) {
	t.Skip("TBD: a ci item for an old head and a comment on a resolved thread get outcome superseded and are not in the packet")
}

func TestPRRoutingUserActivityHoldsDispatch(t *testing.T) {
	t.Skip("TBD: a recent author_replied with responder author delays the batch until user_active_hold expires")
}

func TestPRRoutingBudgetsHoldDispatch(t *testing.T) {
	t.Skip("TBD: exceeding max_runs_per_pr_per_day launches nothing and raises budget attention")
}

func TestPRRoutingCIAttemptCapHolds(t *testing.T) {
	t.Skip("TBD: after two runs whose pushed head failed checks, the next checks_failed is hold with rule ci_attempt_cap")
}

func TestPRRoutingOnlyBotThreadsMayBeResolved(t *testing.T) {
	t.Skip("TBD: packet items have may_resolve true only for bot comments; a human thread resolved by identity_login during a run records resolved_human_thread and raises attention without pausing")
}

func TestPRRoutingAttentionSnoozeHidesUntilExpiry(t *testing.T) {
	t.Skip("TBD: pr attention snooze --for 1d hides the item from pr attention list until the snooze ends, then it reappears if still open")
}

func TestPRRoutingRunsInDetachedWorktreeOutsideClone(t *testing.T) {
	t.Skip("TBD: the fake runner cwd is a detached worktree at the observed head outside the local clone, and the clone's branch and status are unchanged")
}

func TestPRRoutingPacketKeepsUntrustedTextDelimited(t *testing.T) {
	t.Skip("TBD: prompt.md contains no comment body text and packet.json keeps bodies under untrusted")
}

func TestPRRoutingRecordsValidatedOutcomes(t *testing.T) {
	t.Skip("TBD: a scripted result records one item_handled per item, ignores unknown ids, and marks missing items not_handled")
}

func TestPRRoutingFailedRunsRaiseAttention(t *testing.T) {
	t.Skip("TBD: non-zero exit, timeout, or invalid result.json records run_finished failed or timed_out and raises run_failed attention for every item")
}

func TestPRRoutingForcePushGuardPausesRouting(t *testing.T) {
	t.Skip("TBD: a fake runner that rewrites the remote branch causes guard_violated and paused, and the next batch does not launch")
}

func TestPRRoutingVerificationReadsLaterWatchEvents(t *testing.T) {
	t.Skip("TBD: a replied outcome is unverified until an agent author_replied in that thread arrives; fixed is verified when the observed head descends from pushed_head")
}

func TestPRRoutingAttentionClearsWhenOwnerReplies(t *testing.T) {
	t.Skip("TBD: an author_replied with responder author in the thread removes the item from pr attention list")
}

func TestPRRoutingRunsAreAISessions(t *testing.T) {
	t.Skip("TBD: ai sessions lists the run with launched_by.kind pr_routing")
}

func TestPRRoutingRepositoryPolicyOnlyTightens(t *testing.T) {
	t.Skip("TBD: default-branch and PR-head hold_paths both hold matching comments; a PR head deleting a default-branch rule does not release it; max_runs_per_day 999 is ignored and reported by doctor; a run editing the policy file records policy_edited and pauses")
}

func TestPRRoutingSimulateWritesNothing(t *testing.T) {
	t.Skip("TBD: simulate over stored events prints decisions and the event count is unchanged")
}

func TestPRRoutingClassificationNeedsNoLLMOrGitHub(t *testing.T) {
	t.Skip("TBD: with no runner, no gh, and no LLM profile configured, classification and simulate succeed")
}
