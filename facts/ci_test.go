package facts

import (
	"errors"
	"os"
	"os/exec"
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
		"run: scripts/verify",
	} {
		if !strings.Contains(workflow, expected) {
			t.Fatalf("expected CI workflow to include %q, got:\n%s", expected, workflow)
		}
	}
	if strings.Contains(workflow, "continue-on-error") {
		t.Fatalf("expected verification failures to fail CI, got:\n%s", workflow)
	}

	verify, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts/verify"))
	if err != nil {
		t.Fatalf("read scripts/verify: %v", err)
	}
	for _, expected := range []string{"gofmt -l .", "go vet ./...", "scripts/go-test-failures ./..."} {
		if !strings.Contains(string(verify), expected) {
			t.Fatalf("expected scripts/verify to include %q, got:\n%s", expected, verify)
		}
	}
}

func TestReleaseWorkflowVerifiesTaggedSourceWithScriptsVerify(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(repoRoot(t), ".github/workflows/release.yaml"))
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	workflow := string(contents)
	verifyStart := strings.Index(workflow, "  verify:\n")
	buildStart := strings.Index(workflow, "  build:\n")
	if verifyStart < 0 || buildStart <= verifyStart {
		t.Fatalf("expected verify job before build job in release workflow")
	}
	verify := workflow[verifyStart:buildStart]
	for _, expected := range []string{
		"run: scripts/verify",
		"actions/checkout",
		"actions/setup-go",
		"go-version-file: go.mod",
	} {
		if !strings.Contains(verify, expected) {
			t.Fatalf("expected release verify job to include %q, got:\n%s", expected, verify)
		}
	}
	for _, unexpected := range []string{"go vet ./...", "go test ./...", "continue-on-error"} {
		if strings.Contains(verify, unexpected) {
			t.Fatalf("expected release verify job to omit %q, got:\n%s", unexpected, verify)
		}
	}
	if !strings.Contains(workflow[buildStart:], "needs: [validate, verify]") {
		t.Fatalf("expected build job to depend on verification, got:\n%s", workflow[buildStart:])
	}
}

func TestGoTestFailuresReportsOnlyFailingTests(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("scripts/go-test-failures filters same-package output only when jq is installed")
	}
	module := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.test/failures\n\ngo 1.21\n",
		"x_test.go": `package failures

import (
	"log"
	"testing"
)

func TestPassesNoisily(t *testing.T) { log.Printf("noise from a passing test") }

func TestFails(t *testing.T) { t.Fatalf("want 2, got %d", 1) }
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(module, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command(filepath.Join(repoRoot(t), "scripts/go-test-failures"), "./...")
	cmd.Dir = module
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("expected a non-zero exit, got err=%v\n%s", err, out)
	}
	for _, expected := range []string{"--- FAIL: TestFails", "want 2, got 1"} {
		if !strings.Contains(string(out), expected) {
			t.Fatalf("expected output to include %q, got:\n%s", expected, out)
		}
	}
	for _, unexpected := range []string{"TestPassesNoisily", "noise from a passing test"} {
		if strings.Contains(string(out), unexpected) {
			t.Fatalf("expected output to leave out %q, got:\n%s", unexpected, out)
		}
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
