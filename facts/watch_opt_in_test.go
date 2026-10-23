package facts

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestStartWithoutWatchDirsReportsFileWatchingOff(t *testing.T) {
	fakeUserHomeWithDirs(t, "Desktop", "Documents", "Downloads", "Code")
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:         homeDir,
		DatabasePath:    initResult.DatabasePath,
		MaxWatchEntries: 1,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	defer capture.Close()

	if len(capture.Status.WatchDirs) != 0 {
		t.Fatalf("expected no watch roots, got %q", capture.Status.WatchDirs)
	}
	if len(capture.Status.RegisteredWatchDirs) != 0 || capture.Status.WatchCount != 0 {
		t.Fatalf("expected no registered watches, got %d %q", capture.Status.WatchCount, capture.Status.RegisteredWatchDirs)
	}
	if capture.Status.WatchLimitReached {
		t.Fatalf("expected no watch limit when file watching is off")
	}
	for _, expected := range []string{"File watching: off", "workgraph settings add-watch"} {
		if !strings.Contains(capture.Status.Message, expected) {
			t.Fatalf("expected start message to include %q, got:\n%s", expected, capture.Status.Message)
		}
	}
	for _, unexpected := range []string{"Watch limit reached", "Watching:"} {
		if strings.Contains(capture.Status.Message, unexpected) {
			t.Fatalf("expected start message not to include %q, got:\n%s", unexpected, capture.Status.Message)
		}
	}
}

func TestStartIgnoresAutomaticWatchRootsFromOlderSettings(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	automaticDir := filepath.Join(tempDir, "Code")
	userAddedDir := filepath.Join(tempDir, "notes")
	repoDir := filepath.Join(automaticDir, "Cupcake")
	for _, dir := range []string{repoDir, userAddedDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	commitSHA := createGitCommit(t, repoDir, "Add cupcake API")

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	writeInitSettings(t, initResult.SettingsPath, initSettingsFile{
		WatchDirs:             []string{userAddedDir, automaticDir},
		ConservativeWatchDirs: []string{automaticDir},
		IgnorePaths:           []string{homeDir},
		IgnoreNames:           []string{".git"},
	})
	before, err := os.ReadFile(initResult.SettingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	defer capture.Close()

	if !sameStrings(capture.Status.WatchDirs, []string{userAddedDir}) {
		t.Fatalf("expected only the user-added root to be watched, got %q", capture.Status.WatchDirs)
	}
	for _, registered := range capture.Status.RegisteredWatchDirs {
		if registered == automaticDir || strings.HasPrefix(registered, automaticDir+string(filepath.Separator)) {
			t.Fatalf("expected automatic root not to be watched, got registered %q", registered)
		}
	}

	result, err := workgraph.CaptureGitCommits(workgraph.GitCaptureConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
	})
	if err != nil {
		t.Fatalf("git capture failed: %v", err)
	}
	if !sameStrings(result.Repositories, []string{repoDir}) {
		t.Fatalf("expected git capture to keep scanning the automatic root, got %q", result.Repositories)
	}
	if gitCommitEventCount(t, initResult.DatabasePath, commitSHA) != 1 {
		t.Fatalf("expected commit %s to be captured", commitSHA)
	}

	settings, err := workgraph.GetSettings(workgraph.SettingsGetConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("settings get failed: %v", err)
	}
	for _, expected := range []string{"Watch directories: 1 (user config)", "Repository scan directories: 1 (user config)"} {
		if !strings.Contains(settings.Message, expected) {
			t.Fatalf("expected settings output to include %q, got:\n%s", expected, settings.Message)
		}
	}

	after, err := os.ReadFile(initResult.SettingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("expected settings file to be unchanged, got:\n%s", after)
	}
}

func TestGitCaptureScansRepoDirsWithoutWatchingThem(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	codeDir := filepath.Join(tempDir, "Code")
	repoDir := filepath.Join(codeDir, "Cupcake")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	commitSHA := createGitCommit(t, repoDir, "Add cupcake API")

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	writeInitSettings(t, initResult.SettingsPath, initSettingsFile{
		WatchDirs:   []string{},
		RepoDirs:    []string{codeDir, filepath.Join(tempDir, "missing")},
		IgnorePaths: []string{homeDir},
		IgnoreNames: []string{".git"},
	})

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	defer capture.Close()
	if len(capture.Status.RegisteredWatchDirs) != 0 {
		t.Fatalf("expected repository scan dirs not to be watched, got %q", capture.Status.RegisteredWatchDirs)
	}

	result, err := workgraph.CaptureGitCommits(workgraph.GitCaptureConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
	})
	if err != nil {
		t.Fatalf("git capture failed: %v", err)
	}
	if !sameStrings(result.Repositories, []string{repoDir}) {
		t.Fatalf("expected git capture to find repositories under repo_dirs, got %q", result.Repositories)
	}
	if gitCommitEventCount(t, initResult.DatabasePath, commitSHA) != 1 {
		t.Fatalf("expected commit %s to be captured", commitSHA)
	}
}
