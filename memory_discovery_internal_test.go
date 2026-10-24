package workgraph

import "testing"

func TestMemoryNameSimilarityExplainsOnlySupportedMatches(t *testing.T) {
	for _, test := range []struct {
		name, source string
		score        int
	}{
		{"HPO Harness", "Helix.Platform.Operations", 2},
		{"HPO Harness", "HelixPlatformOperations", 2},
		{"Acme Skills", "acme-skills", 3},
		{"Acme Workstream", "acme-service", 1},
		{"My API", "Other API", 0},
		{"Work Project", "Work Service", 0},
		{"HPO Harness", "Unrelated.Repository", 0},
	} {
		score, reason := memoryNameSimilarity(test.name, test.source)
		if score != test.score || score > 0 && reason == "" {
			t.Fatalf("%q / %q: %d %q", test.name, test.source, score, reason)
		}
	}
}
