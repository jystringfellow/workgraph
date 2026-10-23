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

func TestRepoDirsPresenceSurvivesEverySettingsWrite(t *testing.T) {
	mutators := map[string]func(homeDir, tempDir string) error{
		"add-watch": func(homeDir, tempDir string) error {
			_, err := workgraph.AddWatchDir(workgraph.SettingsWatchConfig{HomeDir: homeDir, Path: filepath.Join(tempDir, "notes")})
			return err
		},
		"add-ignore-path": func(homeDir, tempDir string) error {
			_, err := workgraph.AddIgnorePath(workgraph.SettingsIgnoreConfig{HomeDir: homeDir, Path: filepath.Join(tempDir, "private")})
			return err
		},
		"remove-ignore-path": func(homeDir, tempDir string) error {
			_, err := workgraph.RemoveIgnorePath(workgraph.SettingsIgnoreConfig{HomeDir: homeDir, Path: homeDir})
			return err
		},
		"add-ignore-name": func(homeDir, tempDir string) error {
			_, err := workgraph.AddIgnoreName(workgraph.SettingsIgnoreConfig{HomeDir: homeDir, Name: ".cache"})
			return err
		},
		"remove-ignore-name": func(homeDir, tempDir string) error {
			_, err := workgraph.RemoveIgnoreName(workgraph.SettingsIgnoreConfig{HomeDir: homeDir, Name: ".git"})
			return err
		},
	}
	repoDirsValues := []struct {
		name           string
		field          string
		scansAutomatic bool
		keepsEmpty     bool
	}{
		{name: "absent", field: "", scansAutomatic: true},
		{name: "null", field: `"repo_dirs": null,`, scansAutomatic: true},
		{name: "empty", field: `"repo_dirs": [],`, keepsEmpty: true},
	}

	for _, repoDirs := range repoDirsValues {
		for mutatorName, mutate := range mutators {
			t.Run(repoDirs.name+"/"+mutatorName, func(t *testing.T) {
				tempDir := t.TempDir()
				homeDir := filepath.Join(tempDir, ".workgraph")
				automaticDir := filepath.Join(tempDir, "Code")
				repoDir := filepath.Join(automaticDir, "Cupcake")
				for _, dir := range []string{repoDir, filepath.Join(tempDir, "notes")} {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatalf("create %s: %v", dir, err)
					}
				}
				createGitCommit(t, repoDir, "Add cupcake API")

				initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
				if err != nil {
					t.Fatalf("init failed: %v", err)
				}
				if err := os.WriteFile(initResult.SettingsPath, []byte(`{
  "watch_dirs": ["`+automaticDir+`"],
  "conservative_watch_dirs": ["`+automaticDir+`"],
  `+repoDirs.field+`
  "ignore_paths": ["`+homeDir+`"],
  "ignore_names": [".git"]
}
`), 0o600); err != nil {
					t.Fatalf("write settings: %v", err)
				}

				if err := mutate(homeDir, tempDir); err != nil {
					t.Fatalf("%s failed: %v", mutatorName, err)
				}

				contents, err := os.ReadFile(initResult.SettingsPath)
				if err != nil {
					t.Fatalf("read settings: %v", err)
				}
				if repoDirs.keepsEmpty && !strings.Contains(string(contents), `"repo_dirs": []`) {
					t.Fatalf("expected explicitly empty repo_dirs to survive %s, got:\n%s", mutatorName, contents)
				}

				result, err := workgraph.CaptureGitCommits(workgraph.GitCaptureConfig{
					HomeDir:      homeDir,
					DatabasePath: initResult.DatabasePath,
				})
				if err != nil {
					t.Fatalf("git capture failed: %v", err)
				}
				scanned := containsString(result.Repositories, repoDir)
				if scanned != repoDirs.scansAutomatic {
					t.Fatalf("expected automatic root scanned=%v after %s, got repositories %q", repoDirs.scansAutomatic, mutatorName, result.Repositories)
				}
			})
		}
	}
}

func TestAddWatchOnAutomaticRootWatchesItAgain(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	automaticDir := filepath.Join(tempDir, "Documents")
	if err := os.MkdirAll(automaticDir, 0o755); err != nil {
		t.Fatalf("create automatic dir: %v", err)
	}
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	writeInitSettings(t, initResult.SettingsPath, initSettingsFile{
		WatchDirs:             []string{automaticDir},
		ConservativeWatchDirs: []string{automaticDir},
		IgnorePaths:           []string{homeDir},
		IgnoreNames:           []string{".git"},
	})

	if _, err := workgraph.AddWatchDir(workgraph.SettingsWatchConfig{HomeDir: homeDir, Path: automaticDir}); err != nil {
		t.Fatalf("add watch dir: %v", err)
	}

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	defer capture.Close()
	if !sameStrings(capture.Status.WatchDirs, []string{automaticDir}) {
		t.Fatalf("expected add-watch to make the automatic root user-added, got %q", capture.Status.WatchDirs)
	}
}

func TestGitHubCaptureLinksProjectThroughRepoDirsOnly(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	codeDir := filepath.Join(tempDir, "Code")
	repoDir := filepath.Join(codeDir, "Cupcake")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	createGitCommit(t, repoDir, "Add cupcake API")
	runGit(t, repoDir, "remote", "add", "origin", "https://github.com/jystringfellow/Cupcake.git")

	eventsPath := filepath.Join(tempDir, "github-events.json")
	if err := os.WriteFile(eventsPath, []byte(`[
  {
    "kind": "pull_request",
    "repository": "jystringfellow/Cupcake",
    "number": 42,
    "url": "https://github.com/jystringfellow/Cupcake/pull/42",
    "state": "open",
    "actor": "octocat",
    "title": "Add cupcake API",
    "updated_at": "2026-05-20T14:30:00Z"
  }
]`), 0o644); err != nil {
		t.Fatalf("write github events: %v", err)
	}

	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	writeInitSettings(t, filepath.Join(homeDir, "settings.json"), initSettingsFile{
		WatchDirs:   []string{},
		RepoDirs:    []string{codeDir},
		IgnorePaths: []string{homeDir},
		IgnoreNames: []string{".git"},
	})
	if output, err := runworkgraph(t, repoRoot, "github", "capture", "--home", homeDir, "--events-file", eventsPath); err != nil {
		t.Fatalf("workgraph github capture failed: %v\n%s", err, output)
	}

	event := githubEvent(t, filepath.Join(homeDir, "workgraph.db"), "github.pull_request", "jystringfellow/Cupcake", 42)
	if event.Project != "Cupcake" {
		t.Fatalf("expected repo_dirs remote project %q, got %q", "Cupcake", event.Project)
	}
}
