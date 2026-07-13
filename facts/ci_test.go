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
		"ubuntu-latest",
		"macos-latest",
		"go vet ./...",
		"go test ./...",
	} {
		if !strings.Contains(workflow, expected) {
			t.Fatalf("expected CI workflow to include %q, got:\n%s", expected, workflow)
		}
	}
	if strings.Contains(workflow, "continue-on-error") {
		t.Fatalf("expected vet and test failures to fail CI, got:\n%s", workflow)
	}
}

func TestCommandFactsReusePrebuiltCLIWithoutGoOnPath(t *testing.T) {
	repoRoot := repoRoot(t)
	t.Setenv("PATH", t.TempDir())

	firstRoot := t.TempDir()
	if output, err := runworkgraph(t, repoRoot, "init",
		"--home", filepath.Join(firstRoot, ".workgraph"),
		"--memory", filepath.Join(firstRoot, "memory")); err != nil {
		t.Fatalf("prebuilt CLI command failed without go on PATH: %v\n%s", err, output)
	}

	secondRoot := t.TempDir()
	if output, err := runWorkgraphCommandAllowError(nil, "init",
		"--home", filepath.Join(secondRoot, ".workgraph"),
		"--memory", filepath.Join(secondRoot, "memory")); err != nil {
		t.Fatalf("prebuilt daemon CLI command failed without go on PATH: %v\n%s", err, output)
	}
}
