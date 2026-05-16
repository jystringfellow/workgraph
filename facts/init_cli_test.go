package facts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCommandCreatesConfiguredPathsAndReportsThem(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")

	repoRoot := repoRoot(t)
	cmd := exec.Command(
		"go",
		"run",
		"./cmd/workgraph",
		"init",
		"--home",
		homeDir,
		"--memory",
		memoryDir,
	)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	dbPath := filepath.Join(homeDir, "workgraph.db")
	for _, expected := range []string{homeDir, dbPath, memoryDir} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected init output to include %q, got:\n%s", expected, output)
		}
	}

	for _, path := range []string{homeDir, dbPath, memoryDir} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected init to create %q: %v", path, err)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	return filepath.Dir(wd)
}
