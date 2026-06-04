package facts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCIRunsFullGoSuiteOnPullRequestsToMain(t *testing.T) {
	workflowPath := filepath.Join(repoRoot(t), ".github/workflows/ci.yaml")
	contents, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	workflow := string(contents)

	for _, expected := range []string{
		"pull_request:",
		"main",
		"actions/checkout",
		"actions/setup-go",
		"go-version-file: go.mod",
		"go test ./...",
	} {
		if !strings.Contains(workflow, expected) {
			t.Fatalf("expected CI workflow to include %q, got:\n%s", expected, workflow)
		}
	}
}
