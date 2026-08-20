package facts

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestVersionReportsInspectableBuildIdentity(t *testing.T) {
	output := strings.TrimSpace(runWorkgraphCommand(t, nil, "version"))
	lines := strings.Split(output, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected three version lines, got %d:\n%s", len(lines), output)
	}
	if !regexp.MustCompile(`^workgraph (dev|v[0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.+-]*)$`).MatchString(lines[0]) {
		t.Fatalf("unexpected version line: %q", lines[0])
	}
	if !regexp.MustCompile(`^commit: (unknown|[0-9a-f]{40})$`).MatchString(lines[1]) {
		t.Fatalf("unexpected commit line: %q", lines[1])
	}
	if lines[2] != "built: unknown" && !regexp.MustCompile(`^built: \d{4}-\d{2}-\d{2}T`).MatchString(lines[2]) {
		t.Fatalf("unexpected build-time line: %q", lines[2])
	}
	if strings.Contains(output, repoRoot(t)) {
		t.Fatalf("version output exposed a local checkout path:\n%s", output)
	}
}

func TestInstallDocsAddGoBinToPathAndExplainExplicitUpgrades(t *testing.T) {
	for _, relativePath := range []string{"README.md", filepath.Join("docs", "commands.md")} {
		contents, err := os.ReadFile(filepath.Join(repoRoot(t), relativePath))
		if err != nil {
			t.Fatalf("read %s: %v", relativePath, err)
		}
		document := string(contents)
		for _, expected := range []string{
			`export PATH="$(go env GOPATH)/bin:$PATH"`,
			"command -v workgraph",
			"go install github.com/jystringfellow/workgraph/cmd/workgraph@latest",
			"workgraph version",
		} {
			if !strings.Contains(document, expected) {
				t.Fatalf("%s is missing %q", relativePath, expected)
			}
		}
		if strings.Contains(document, "```sh\n$(go env GOPATH)/bin\n```") {
			t.Fatalf("%s still presents the Go binary directory as a command", relativePath)
		}
	}
}

func TestReleaseWorkflowBuildsNativeArchivesAndChecksums(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yaml"))
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	workflow := string(contents)
	for _, expected := range []string{
		"push:",
		"tags:",
		"contents: write",
		"go vet ./...",
		"go test ./...",
		"ubuntu-22.04",
		"ubuntu-22.04-arm",
		"macos-15-intel",
		"macos-15",
		"MACOSX_DEPLOYMENT_TARGET",
		"windows-latest",
		"main.version",
		"main.commit",
		"main.buildDate",
		"sha256sum",
		"checksums.txt",
		"gh release create",
		"workgraph.rb",
		"HOMEBREW_TAP_TOKEN",
	} {
		if !strings.Contains(workflow, expected) {
			t.Fatalf("release workflow is missing %q:\n%s", expected, workflow)
		}
	}
	if strings.Contains(workflow, "curl") && strings.Contains(workflow, "| sh") {
		t.Fatalf("release workflow includes a network-to-shell installation path:\n%s", workflow)
	}
}

func TestHomebrewFormulaTemplatePinsEverySupportedArchive(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(repoRoot(t), "packaging", "homebrew", "workgraph.rb.tmpl"))
	if err != nil {
		t.Fatalf("read Homebrew formula template: %v", err)
	}
	formula := string(contents)
	for _, expected := range []string{
		"{{VERSION}}",
		"{{DARWIN_ARM64_SHA256}}",
		"{{DARWIN_AMD64_SHA256}}",
		"{{LINUX_ARM64_SHA256}}",
		"{{LINUX_AMD64_SHA256}}",
		`bin.install "workgraph"`,
		`shell_output("#{bin}/workgraph version")`,
	} {
		if !strings.Contains(formula, expected) {
			t.Fatalf("Homebrew formula template is missing %q:\n%s", expected, formula)
		}
	}
}
