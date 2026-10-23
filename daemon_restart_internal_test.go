package workgraph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDaemonRestartIgnoresWatchRootsFromLegacyDaemonState(t *testing.T) {
	var status DaemonStatus
	if err := json.Unmarshal([]byte(`{
  "running": true,
  "pid": 4242,
  "home_dir": "/home/craig/.workgraph",
  "database_path": "/home/craig/.workgraph/workgraph.db",
  "watch_dirs": ["/home/craig/Desktop", "/home/craig/Documents", "/home/craig/Code"]
}`), &status); err != nil {
		t.Fatalf("decode legacy daemon state: %v", err)
	}

	config := daemonRestartConfig(status)
	if len(config.WatchDirs) != 0 {
		t.Fatalf("expected restart to resolve watch roots from settings, got overrides %q", config.WatchDirs)
	}
	if config.HomeDir != status.HomeDir || config.DatabasePath != status.DatabasePath {
		t.Fatalf("expected restart to keep home and database, got %+v", config)
	}
}

func TestDaemonRestartKeepsCommandLineWatchOverrides(t *testing.T) {
	overrides := []string{"/home/craig/project"}
	status := daemonStatusFromRun(RunStatus{
		HomeDir:      "/home/craig/.workgraph",
		DatabasePath: "/home/craig/.workgraph/workgraph.db",
		WatchDirs:    []string{"/home/craig/project"},
	}, 4242, true)

	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("encode daemon state: %v", err)
	}
	var decoded DaemonStatus
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode daemon state: %v", err)
	}

	config := daemonRestartConfig(decoded)
	if !reflect.DeepEqual(config.WatchDirs, overrides) {
		t.Fatalf("expected restart to keep --watch overrides %q, got %q", overrides, config.WatchDirs)
	}
}

func TestDaemonRestartDoesNotTurnSettingsRootsIntoOverrides(t *testing.T) {
	status := daemonStatusFromRun(RunStatus{
		HomeDir:      "/home/craig/.workgraph",
		DatabasePath: "/home/craig/.workgraph/workgraph.db",
		WatchDirs:    []string{"/home/craig/notes"},
	}, 4242, false)

	if config := daemonRestartConfig(status); len(config.WatchDirs) != 0 {
		t.Fatalf("expected settings-derived roots not to become overrides, got %q", config.WatchDirs)
	}
}

func TestDaemonRestartKeepsRelativeWatchOverrideAfterWorkingDirectoryChanges(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	if _, err := Init(InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("initialize workgraph: %v", err)
	}
	projectDir := t.TempDir()
	otherDir := t.TempDir()
	t.Chdir(projectDir)
	resolvedProject, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve project dir: %v", err)
	}

	runStatus, err := prepareRunStatus(RunConfig{HomeDir: homeDir, WatchDirs: []string{"."}})
	if err != nil {
		t.Fatalf("prepare run status: %v", err)
	}
	encoded, err := json.Marshal(daemonStatusFromRun(runStatus, 4242, true))
	if err != nil {
		t.Fatalf("encode daemon state: %v", err)
	}

	t.Chdir(otherDir)
	var decoded DaemonStatus
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode daemon state: %v", err)
	}
	config := daemonRestartConfig(decoded)
	if !reflect.DeepEqual(config.WatchDirs, []string{resolvedProject}) {
		t.Fatalf("expected restart to keep absolute override %q, got %q", resolvedProject, config.WatchDirs)
	}
}
