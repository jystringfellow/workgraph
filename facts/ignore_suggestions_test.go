package facts

import "testing"

func TestSuggestsIgnorePathFromNoisyTrackedActivity(t *testing.T) {
	t.Skip("TBD: high event volume under one generated-looking path creates a pending ignore-path suggestion without changing config")
}

func TestSuggestsIgnoreNameFromRecurringGeneratedBasename(t *testing.T) {
	t.Skip("TBD: high event volume under repeated generated basenames creates a pending ignore-name suggestion without changing config")
}

func TestApprovingIgnorePathSuggestionAddsPathToConfig(t *testing.T) {
	t.Skip("TBD: approving an ignore-path suggestion appends the suggested path to ignore_paths")
}

func TestApprovingIgnoreNameSuggestionAddsNameToConfig(t *testing.T) {
	t.Skip("TBD: approving an ignore-name suggestion appends the suggested basename to ignore_names")
}

func TestDuplicateIgnoreSuggestionsAreCoalesced(t *testing.T) {
	t.Skip("TBD: repeated noisy activity for the same path or name updates one pending suggestion instead of creating duplicates")
}
