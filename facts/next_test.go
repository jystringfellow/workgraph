package facts

import "testing"

func TestNextListsEachReasonInDocumentedOrder(t *testing.T) {
	t.Skip("TBD: fixtures for an awaiting thread, a review request, a re-review, changes requested, failed checks, a conflict, a ready PR, a bot conversation comment, and an agent-answered thread list exactly seven grouped items in order without invoking gh")
}

func TestNextJSONItemsFollowTheV1Schema(t *testing.T) {
	t.Skip("TBD: each item has schema workgraph.next_item.v1, a stable id, opened_by, and evidence ids, and no local path or token; untrusted text appears only with --include-untrusted and only in the untrusted field; text output never contains comment text")
}

func TestNextDoneAndSnoozeHideOnlyThatItemID(t *testing.T) {
	t.Skip("TBD: done hides a thread item until a new reviewer comment lists it under a new id; a snoozed item returns when the snooze ends; both append next events and invoke no provider")
}

func TestNextReportsStaleness(t *testing.T) {
	t.Skip("TBD: with the source in setup error or its last successful poll older than three intervals, next prints a warning, items have freshness.stale true, and unknown dimensions show as unknown")
}

func TestNextWatchEmitsTheInitialSetThenDifferences(t *testing.T) {
	t.Skip("TBD: next --watch --json emits each open item with initial true then synced; after a poll commit that opens one item and resolves another it emits exactly one opened and one closed with reason resolved within two seconds")
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
