package facts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCommandAddWatchDefaultsToCurrentDirectory(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	projectDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}

	repoRoot := repoRoot(t)
	workgraphBinary := filepath.Join(tempDir, "workgraph")
	buildCmd := exec.Command("go", "build", "-o", workgraphBinary, "./cmd/workgraph")
	buildCmd.Dir = repoRoot
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build workgraph failed: %v\n%s", err, output)
	}

	initCmd := exec.Command("go", "run", "./cmd/workgraph", "init", "--home", homeDir)
	initCmd.Dir = repoRoot
	if output, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	cmd := exec.Command(workgraphBinary, "config", "add-watch", "--home", homeDir)
	cmd.Dir = projectDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workgraph config add-watch failed: %v\n%s", err, output)
	}

	config := readCLIInitConfig(t, filepath.Join(homeDir, "config.json"))
	resolvedProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		t.Fatalf("resolve project dir: %v", err)
	}
	if config.WatchDirs[0] != resolvedProjectDir {
		t.Fatalf("expected current dir %q to be first watch dir, got %q", resolvedProjectDir, config.WatchDirs)
	}
	if !strings.Contains(string(output), "Added watch directory: "+projectDir) {
		t.Fatalf("expected output to report added watch dir, got:\n%s", output)
	}
}
