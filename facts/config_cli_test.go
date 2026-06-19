package facts

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	workgraph "github.com/jystringfellow/workgraph"
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
  },
  "connectors": {
    "slack": {
      "include_dms": {
        "value": false,
        "locked": true
      }
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write managed settings: %v", err)
	}
	restoreManagedSettings := workgraph.SetManagedSettingsPathForTest(managedPath)
	defer restoreManagedSettings()

	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("workgraph init failed: %v", err)
	}
	result, err := workgraph.GetSettings(workgraph.SettingsGetConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("workgraph settings get failed: %v", err)
	}
	for _, expected := range []string{
		"Effective workgraph settings",
		"Managed settings: " + managedPath,
		"LLM hosted providers: disabled (managed settings locked)",
		"LLM allowed base URLs: http://localhost:11434/v1 (managed settings locked)",
		"Slack DM capture: disabled (managed settings locked)",
	} {
		if !strings.Contains(result.Message, expected) {
			t.Fatalf("expected settings output to include %q, got:\n%s", expected, result.Message)
		}
	}
	if strings.Contains(result.Message, "do-not-print") {
		t.Fatalf("expected settings output not to expose ignored managed secret fields, got:\n%s", result.Message)
	}
}

func TestSettingsGetJSONReportsManagedSettingsWithoutSecrets(t *testing.T) {
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
  },
  "connectors": {
    "slack": {
      "include_dms": {
        "value": false,
        "locked": true
      }
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write managed settings: %v", err)
	}
	restoreManagedSettings := workgraph.SetManagedSettingsPathForTest(managedPath)
	defer restoreManagedSettings()

	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("workgraph init failed: %v", err)
	}
	result, err := workgraph.GetSettings(workgraph.SettingsGetConfig{
		HomeDir: homeDir,
		Format:  "json",
	})
	if err != nil {
		t.Fatalf("workgraph settings get --format json failed: %v", err)
	}
	if strings.Contains(result.Message, "do-not-print") {
		t.Fatalf("expected JSON settings output not to expose ignored managed secret fields, got:\n%s", result.Message)
	}

	var payload struct {
		Settings struct {
			Path string `json:"path"`
		} `json:"settings"`
		ManagedSettings struct {
			Active bool   `json:"active"`
			Path   string `json:"path"`
		} `json:"managed_settings"`
		LLM struct {
			HostedEnabled struct {
				Value  bool   `json:"value"`
				Locked bool   `json:"locked"`
				Source string `json:"source"`
			} `json:"hosted_enabled"`
			AllowedBaseURLs struct {
				Value  []string `json:"value"`
				Locked bool     `json:"locked"`
				Source string   `json:"source"`
			} `json:"allowed_base_urls"`
		} `json:"llm"`
		Connectors struct {
			Slack struct {
				IncludeDMs struct {
					Value  bool   `json:"value"`
					Locked bool   `json:"locked"`
					Source string `json:"source"`
				} `json:"include_dms"`
			} `json:"slack"`
		} `json:"connectors"`
	}
	if err := json.Unmarshal([]byte(result.Message), &payload); err != nil {
		t.Fatalf("expected valid JSON settings output: %v\n%s", err, result.Message)
	}
	if payload.Settings.Path != filepath.Join(homeDir, "settings.json") {
		t.Fatalf("expected settings path in JSON output, got %q", payload.Settings.Path)
	}
	if !payload.ManagedSettings.Active || payload.ManagedSettings.Path != managedPath {
		t.Fatalf("expected active managed settings path %q, got %+v", managedPath, payload.ManagedSettings)
	}
	if payload.LLM.HostedEnabled.Value || !payload.LLM.HostedEnabled.Locked || payload.LLM.HostedEnabled.Source != "managed" {
		t.Fatalf("expected hosted LLM disabled from managed settings, got %+v", payload.LLM.HostedEnabled)
	}
	if len(payload.LLM.AllowedBaseURLs.Value) != 1 || payload.LLM.AllowedBaseURLs.Value[0] != "http://localhost:11434/v1" || !payload.LLM.AllowedBaseURLs.Locked || payload.LLM.AllowedBaseURLs.Source != "managed" {
		t.Fatalf("expected managed allowed LLM base URLs, got %+v", payload.LLM.AllowedBaseURLs)
	}
	if payload.Connectors.Slack.IncludeDMs.Value || !payload.Connectors.Slack.IncludeDMs.Locked || payload.Connectors.Slack.IncludeDMs.Source != "managed" {
		t.Fatalf("expected Slack DM capture disabled from managed settings, got %+v", payload.Connectors.Slack.IncludeDMs)
	}
}

func TestSettingsGetCommandSupportsJSONFormat(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "settings", "get", "--home", homeDir, "--format", "json")
	if err != nil {
		t.Fatalf("workgraph settings get --format json failed: %v\n%s", err, output)
	}

	var payload struct {
		Settings struct {
			Path          string `json:"path"`
			WatchDirCount int    `json:"watch_directory_count"`
		} `json:"settings"`
		ManagedSettings struct {
			Active bool   `json:"active"`
			Path   string `json:"path"`
		} `json:"managed_settings"`
		Connectors struct {
			Slack struct {
				IncludeDMs struct {
					Source string `json:"source"`
				} `json:"include_dms"`
			} `json:"slack"`
		} `json:"connectors"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		t.Fatalf("expected CLI to print valid JSON settings output: %v\n%s", err, output)
	}
	if payload.Settings.Path != filepath.Join(homeDir, "settings.json") {
		t.Fatalf("expected settings path in CLI JSON output, got %q", payload.Settings.Path)
	}
	if payload.Settings.WatchDirCount == 0 {
		t.Fatalf("expected initialized watch directories in CLI JSON output, got %+v", payload.Settings)
	}
	if payload.ManagedSettings.Active {
		t.Fatalf("expected no active managed settings in CLI JSON output, got %+v", payload.ManagedSettings)
	}
	if payload.Connectors.Slack.IncludeDMs.Source != "user_config" {
		t.Fatalf("expected Slack DM setting source to default to user_config, got %+v", payload.Connectors.Slack.IncludeDMs)
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
