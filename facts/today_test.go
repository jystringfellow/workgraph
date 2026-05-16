package facts

import "testing"

func TestTodayReturnsEventsFromCurrentDay(t *testing.T) {
	t.Skip("TBD: today command returns events from the current local day")
}

func TestTodayGroupsEventsIntoSessions(t *testing.T) {
	t.Skip("TBD: today command groups events into time-based sessions")
}

func TestTodayGroupsSessionsByProject(t *testing.T) {
	t.Skip("TBD: today command groups sessions by inferred project")
}

func TestTodayShowsUnfinishedWorkWhenKnown(t *testing.T) {
	t.Skip("TBD: today command shows unfinished work when tasks or TODOs are known")
}

func TestTodayOutputIncludesExpectedSections(t *testing.T) {
	t.Skip("TBD: today output includes Today, Projects, and Sessions sections when data exists")
}

func TestTodayShowsEmptyStateWhenNoEventsExist(t *testing.T) {
	t.Skip("TBD: today output says no activity has been captured when no events exist today")
}

func TestTodayOutputIsPlainTextWithoutLLM(t *testing.T) {
	t.Skip("TBD: today output is deterministic plain text and does not require an LLM")
}
