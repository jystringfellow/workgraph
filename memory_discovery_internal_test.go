package workgraph

import "testing"

func TestMemoryNameSimilarityExplainsOnlySupportedMatches(t *testing.T) {
	for _, test := range []struct {
		name, source string
		score        int
	}{
		{"MMO Harness", "Mindbody.Modernization.Orchestration", 2},
		{"MMO Harness", "MindbodyModernizationOrchestration", 2},
		{"Playlist Skills", "playlist-skills", 3},
		{"Playlist Workstream", "playlist-service", 1},
		{"My API", "Other API", 0},
		{"Work Project", "Work Service", 0},
		{"MMO Harness", "Unrelated.Repository", 0},
	} {
		score, reason := memoryNameSimilarity(test.name, test.source)
		if score != test.score || score > 0 && reason == "" {
			t.Fatalf("%q / %q: %d %q", test.name, test.source, score, reason)
		}
	}
}
