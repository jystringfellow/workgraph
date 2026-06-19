package facts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsCommandAddWatchDefaultsToCurrentDirectory(t *testing.T) {
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

	cmd := exec.Command(workgraphBinary, "settings", "add-watch", "--home", homeDir)
	cmd.Dir = projectDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workgraph settings add-watch failed: %v\n%s", err, output)
	}

	config := readCLIInitSettings(t, filepath.Join(homeDir, "settings.json"))
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

func TestSettingsGetReportsManagedSettingsWithoutSecrets(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	managedPath := filepath.Join(tempDir, "managed-settings.json")
	if err := os.WriteFile(managedPath, []byte(`{
  "version": 1,
  "secret_note": "do-not-print",
  "llm": {
    "hosted_enabled": {
      "value": false,
      "locked": true
    },
    "allowed_base_urls": {
      "value": ["http://localhost:11434/v1"],
      "locked": true
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write managed settings: %v", err)
	}

	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot, "llm", "add", "hosted",
		"--home", homeDir,
		"--provider", "bedrock",
		"--region", "us-east-1",
		"--model-arn", "arn:aws:bedrock:us-east-1:123456789012:foundation-model/example",
	); err != nil {
		t.Fatalf("workgraph llm add failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "settings", "get",
		"--home", homeDir,
		"--managed-settings", managedPath,
	)
	if err != nil {
		t.Fatalf("workgraph settings get failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"Effective workgraph settings",
		"Managed settings: " + managedPath,
		"LLM hosted providers: disabled (managed settings locked)",
		"LLM allowed base URLs: http://localhost:11434/v1 (managed settings locked)",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected settings output to include %q, got:\n%s", expected, output)
		}
	}
	if strings.Contains(string(output), "do-not-print") {
		t.Fatalf("expected settings output not to expose ignored managed secret fields, got:\n%s", output)
	}
}

func TestSettingsDoctorReportsInvalidSettings(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "settings.json"), []byte(`{"watch_dirs": [`), 0o644); err != nil {
		t.Fatalf("write invalid settings: %v", err)
	}

	output, err := runworkgraph(t, repoRoot, "settings", "doctor", "--home", homeDir)
	if err == nil {
		t.Fatalf("expected settings doctor to fail for invalid settings, got:\n%s", output)
	}
	for _, expected := range []string{
		"workgraph settings doctor",
		"Settings: invalid",
		"parse settings",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected settings doctor output to include %q, got:\n%s", expected, output)
		}
	}
}
