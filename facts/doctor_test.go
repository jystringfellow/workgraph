package facts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorReportsLocalReadiness(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	missingWatchDir := filepath.Join(tempDir, "missing")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}

	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	writeInitConfig(t, filepath.Join(homeDir, "config.json"), initConfigFile{
		WatchDirs:   []string{watchDir, missingWatchDir},
		IgnorePaths: []string{homeDir},
		IgnoreNames: []string{".git"},
	})
	if err := os.WriteFile(filepath.Join(homeDir, "slack.json"), []byte(`{"access_token":"slack-secret","channels":["C123"],"user_scopes":[]}`), 0o600); err != nil {
		t.Fatalf("write slack config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "llm.json"), []byte(`{
  "default_profile": "main",
  "profiles": {
    "main": {
      "provider": "openai-compatible",
      "base_url": "https://llm.example.test/v1",
      "model": "test-model",
      "api_key_env": "WORKGRAPH_TEST_LLM_KEY"
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write llm config: %v", err)
	}

	output, err := runworkgraph(t, repoRoot, "doctor", "--home", homeDir)
	if err != nil {
		t.Fatalf("workgraph doctor failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"workgraph doctor",
		"Database: ok",
		"Daemon: not running",
		"Watch roots: 2 configured",
		"- " + watchDir + ": ok",
		"- " + missingWatchDir + ": missing",
		"- slack: token present",
		"LLM: default profile main",
		"API key env WORKGRAPH_TEST_LLM_KEY: missing",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected doctor output to include %q, got:\n%s", expected, output)
		}
	}
	if strings.Contains(string(output), "slack-secret") {
		t.Fatalf("doctor output exposed token:\n%s", output)
	}
}
