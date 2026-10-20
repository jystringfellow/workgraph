package facts

import "testing"

func TestNextListsEachReasonInDocumentedOrder(t *testing.T) {
	t.Skip("TBD: fixtures for an awaiting thread, a review request, a re-review, changes requested, failed checks, a conflict, a ready PR, a bot conversation comment, and an agent-answered thread list exactly seven grouped items in order without invoking gh")
}

func TestNextJSONItemsFollowTheV1Schema(t *testing.T) {
	t.Skip("TBD: for one item of each reason, the item has schema workgraph.next_item.v1, a stable id, opened_by, evidence ids, the documented detail fields, and pull_request.head_repository including a cross-repository PR and a deleted fork as null, with no local path or token; untrusted text appears only with --include-untrusted in the untrusted field; text output never contains comment text")
}

func TestNextDoneAndSnoozeHideOnlyThatItemID(t *testing.T) {
	t.Skip("TBD: done hides a thread item until a new reviewer comment lists it under a new id; a snoozed item returns when the snooze ends; both append next events and invoke no provider")
}

func TestNextReportsStaleness(t *testing.T) {
	t.Skip("TBD: with the source in setup error or its last successful poll older than three intervals next warns and items are stale; an unreachable PR's items are stale with access unreachable while other PRs' items are fresh; unknown dimensions show as unknown")
}

func TestNextWatchEmitsTheInitialSetThenDifferences(t *testing.T) {
	t.Skip("TBD: next --watch --json emits each open item with initial true then synced; a poll commit that opens one item and resolves another emits one opened and one closed reason resolved within two seconds; with polling stopped a snooze expiry emits opened for the same id, passing three intervals emits changed with stale true, and a disconnect emits closed reason out_of_scope")
}

func TestNextResumeAndTodayShowTheSameItems(t *testing.T) {
	t.Skip("TBD: the default resume overview lists the first five items in next order with a more line; resume <project> lists that project's items; today prints only the count line and still counts only personal activity")
}

func TestNextExplainCitesTheRuleAndEvidence(t *testing.T) {
	t.Skip("TBD: next explain <id> prints the reason, the holding condition, and every evidence event id")
}

func TestNextMCPToolReturnsTheSameItemsReadOnly(t *testing.T) {
	t.Skip("TBD: the next_items MCP tool returns the same ids as next --json and has no write behavior")
}

func TestNextCloseReasonsDoNotOverclaim(t *testing.T) {
	t.Skip("TBD: a PR dropped after six missing polls closes its items with left_search, an unreachable one with unreachable, and a withdrawn review request with out_of_scope; none closes with resolved")
}
