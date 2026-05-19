package facts

import "testing"

func TestSuggestsWatchRootFromUnwatchedGitActivity(t *testing.T) {
	t.Skip("TBD: git activity in an unwatched local repository creates a pending watch-root suggestion without changing config")
}

func TestApprovingWatchRootSuggestionAddsDirectoryToConfig(t *testing.T) {
	t.Skip("TBD: approving a watch-root suggestion prepends the suggested directory to watch_dirs")
}

func TestDuplicateWatchRootSuggestionsAreCoalesced(t *testing.T) {
	t.Skip("TBD: repeated external activity for the same directory updates one pending suggestion instead of creating duplicates")
}
