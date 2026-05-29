package facts

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

	for _, path := range []string{homeDir, dbPath, memoryDir, filepath.Join(memoryDir, "projects")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected init to create %q: %v", path, err)
		}
	}
}

func TestInitCommandCreatesAndReportsConfigPath(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	configPath := filepath.Join(homeDir, "config.json")

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

	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected init to create config %q: %v", configPath, err)
	}
	if !strings.Contains(string(output), configPath) {
		t.Fatalf("expected init output to include config path %q, got:\n%s", configPath, output)
	}
	if !strings.Contains(string(output), "Config: "+configPath) {
		t.Fatalf("expected init output to label config path, got:\n%s", output)
	}
}

func TestInitCommandForceRefreshesConfig(t *testing.T) {
	tempDir := t.TempDir()
	userHome := fakeUserHomeWithDirs(t, "Desktop", "Documents", "Downloads")
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	configPath := filepath.Join(homeDir, "config.json")
	oldConfig := cliInitConfigFile{
		WatchDirs:   []string{filepath.Join(tempDir, "old-watch")},
		IgnorePaths: []string{filepath.Join(tempDir, "old-ignore")},
		IgnoreNames: []string{".git", "node_modules"},
	}

	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("create workgraph home: %v", err)
	}
	writeCLIInitConfig(t, configPath, oldConfig)

	repoRoot := repoRoot(t)
	cmd := exec.Command(
		"go",
		"run",
		"./cmd/workgraph",
		"init",
		"--force",
		"--home",
		homeDir,
		"--memory",
		memoryDir,
	)
	cmd.Dir = repoRoot

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workgraph init --force failed: %v\n%s", err, output)
	}

	workgraphHome, err := filepath.Abs(homeDir)
	if err != nil {
		t.Fatalf("resolve workgraph home: %v", err)
	}

	expected := cliInitConfigFile{
		WatchDirs:   []string{filepath.Join(userHome, "Desktop"), filepath.Join(userHome, "Documents"), filepath.Join(userHome, "Downloads")},
		IgnorePaths: []string{workgraphHome},
		IgnoreNames: []string{".git", "node_modules", "DerivedData", ".noindex", "xcuserdata", "bin", "obj", "dist", "build", "target", ".build", ".gradle"},
	}
	config := readCLIInitConfig(t, configPath)
	if !reflect.DeepEqual(config, expected) {
		t.Fatalf("expected force init to refresh config to %#v, got %#v", expected, config)
	}
	if !strings.Contains(string(output), "Config: "+configPath) {
		t.Fatalf("expected init output to label config path, got:\n%s", output)
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

type cliInitConfigFile struct {
	WatchDirs   []string `json:"watch_dirs"`
	IgnorePaths []string `json:"ignore_paths"`
	IgnoreNames []string `json:"ignore_names"`
}

func readCLIInitConfig(t *testing.T, path string) cliInitConfigFile {
	t.Helper()

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var config cliInitConfigFile
	if err := json.Unmarshal(contents, &config); err != nil {
		t.Fatalf("parse config: %v", err)
	}

	return config
}

func writeCLIInitConfig(t *testing.T, path string, config cliInitConfigFile) {
	t.Helper()

	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("encode config: %v", err)
	}

	if err := os.WriteFile(path, append(contents, '\n'), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
