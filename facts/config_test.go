package facts

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestInitCreatesConfigWithSaneDefaultWatchRoots(t *testing.T) {
	userHome := fakeUserHomeWithDirs(t, "Desktop", "Documents", "Downloads", "Developer")
	homeDir := filepath.Join(t.TempDir(), ".workgraph")

	_, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	config := readInitConfig(t, filepath.Join(homeDir, "config.json"))
	expectedWatchDirs := []string{
		filepath.Join(userHome, "Desktop"),
		filepath.Join(userHome, "Documents"),
		filepath.Join(userHome, "Downloads"),
		filepath.Join(userHome, "Developer"),
	}
	if !reflect.DeepEqual(config.WatchDirs, expectedWatchDirs) {
		t.Fatalf("expected config watch dirs %q, got %q", expectedWatchDirs, config.WatchDirs)
	}
	if !reflect.DeepEqual(config.ConservativeWatchDirs, expectedWatchDirs) {
		t.Fatalf("expected default watch dirs to be conservative %q, got %q", expectedWatchDirs, config.ConservativeWatchDirs)
	}
	for _, ignoredName := range []string{"xcuserdata", "bin", "obj", "dist", "build", "target", ".build", ".gradle"} {
		if !containsString(config.IgnoreNames, ignoredName) {
			t.Fatalf("expected default ignore names to include generated output %q, got %q", ignoredName, config.IgnoreNames)
		}
	}
}

func TestConfigPersistsResolvedAbsoluteHomePath(t *testing.T) {
	fakeUserHomeWithDirs(t, "Desktop", "Documents")
	homeDir := filepath.Join(t.TempDir(), ".workgraph")

	_, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	config := readInitConfig(t, filepath.Join(homeDir, "config.json"))
	if len(config.WatchDirs) == 0 {
		t.Fatalf("expected watch dirs, got none")
	}
	for _, watchDir := range config.WatchDirs {
		if !filepath.IsAbs(watchDir) {
			t.Fatalf("expected absolute watch dir, got %q", watchDir)
		}
		if strings.Contains(watchDir, "$HOME") || strings.Contains(watchDir, "~") {
			t.Fatalf("expected resolved watch dir, got %q", watchDir)
		}
	}
}

func TestConfigIgnoresworkgraphHomeByDefault(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")

	_, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	config := readInitConfig(t, filepath.Join(homeDir, "config.json"))
	workgraphHome, err := filepath.Abs(homeDir)
	if err != nil {
		t.Fatalf("resolve workgraph home: %v", err)
	}

	if !reflect.DeepEqual(config.IgnorePaths, []string{workgraphHome}) {
		t.Fatalf("expected ignore paths %q, got %q", []string{workgraphHome}, config.IgnorePaths)
	}
}

func TestConfigSupportsIgnoredPathsAndNames(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	ignoredPath := filepath.Join(watchDir, "private")
	ignoredNameDir := filepath.Join(watchDir, "node_modules")
	if err := os.MkdirAll(ignoredPath, 0o755); err != nil {
		t.Fatalf("create ignored path: %v", err)
	}
	if err := os.MkdirAll(ignoredNameDir, 0o755); err != nil {
		t.Fatalf("create ignored name dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	writeInitConfig(t, initResult.ConfigPath, initConfigFile{
		WatchDirs:   []string{watchDir},
		IgnorePaths: []string{ignoredPath},
		IgnoreNames: []string{"node_modules"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	visible := filepath.Join(watchDir, "visible.md")
	if err := os.WriteFile(visible, []byte("visible"), 0o644); err != nil {
		t.Fatalf("create visible file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", visible)

	ignoredByPath := filepath.Join(ignoredPath, "secret.md")
	if err := os.WriteFile(ignoredByPath, []byte("secret"), 0o644); err != nil {
		t.Fatalf("create file under ignored path: %v", err)
	}
	assertNoEvent(t, initResult.DatabasePath, "created", ignoredByPath)

	ignoredByName := filepath.Join(ignoredNameDir, "package.json")
	if err := os.WriteFile(ignoredByName, []byte("{}"), 0o644); err != nil {
		t.Fatalf("create file under ignored name: %v", err)
	}
	assertNoEvent(t, initResult.DatabasePath, "created", ignoredByName)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunUsesConfiguredWatchDirsWhenNoWatchFlagIsProvided(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "configured")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	writeInitConfig(t, initResult.ConfigPath, initConfigFile{
		WatchDirs:   []string{watchDir},
		IgnorePaths: []string{homeDir},
		IgnoreNames: []string{".git", "node_modules"},
	})

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	defer capture.Close()

	if !reflect.DeepEqual(capture.Status.WatchDirs, []string{watchDir}) {
		t.Fatalf("expected run to watch configured dirs %q, got %q", []string{watchDir}, capture.Status.WatchDirs)
	}
}

func TestWatchFlagsOverrideConfiguredWatchRoots(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	configuredDir := filepath.Join(tempDir, "configured")
	overrideDir := filepath.Join(tempDir, "override")
	ignoredDir := filepath.Join(overrideDir, "private")
	for _, dir := range []string{configuredDir, ignoredDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create dir %q: %v", dir, err)
		}
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	writeInitConfig(t, initResult.ConfigPath, initConfigFile{
		WatchDirs:   []string{configuredDir},
		IgnorePaths: []string{ignoredDir},
		IgnoreNames: []string{".git", "node_modules"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{overrideDir},
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	if !reflect.DeepEqual(capture.Status.WatchDirs, []string{overrideDir}) {
		t.Fatalf("expected run to watch override dirs %q, got %q", []string{overrideDir}, capture.Status.WatchDirs)
	}

	visible := filepath.Join(overrideDir, "visible.md")
	if err := os.WriteFile(visible, []byte("visible"), 0o644); err != nil {
		t.Fatalf("create visible file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", visible)

	ignored := filepath.Join(ignoredDir, "secret.md")
	if err := os.WriteFile(ignored, []byte("secret"), 0o644); err != nil {
		t.Fatalf("create ignored file: %v", err)
	}
	assertNoEvent(t, initResult.DatabasePath, "created", ignored)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestConfigAddWatchPrependsResolvedDirectory(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	projectDir := filepath.Join(tempDir, "external-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	before := readInitConfig(t, initResult.ConfigPath)

	result, err := workgraph.AddWatchDir(workgraph.ConfigWatchConfig{
		HomeDir: homeDir,
		Path:    projectDir,
	})
	if err != nil {
		t.Fatalf("add watch dir failed: %v", err)
	}

	config := readInitConfig(t, initResult.ConfigPath)
	projectDir, err = filepath.Abs(projectDir)
	if err != nil {
		t.Fatalf("resolve project dir: %v", err)
	}
	expectedWatchDirs := append([]string{projectDir}, before.WatchDirs...)
	if !reflect.DeepEqual(config.WatchDirs, expectedWatchDirs) {
		t.Fatalf("expected watch dirs %q, got %q", expectedWatchDirs, config.WatchDirs)
	}
	if containsString(config.ConservativeWatchDirs, projectDir) {
		t.Fatalf("expected added watch dir %q to be explicit, got conservative dirs %q", projectDir, config.ConservativeWatchDirs)
	}
	if !reflect.DeepEqual(config.IgnorePaths, before.IgnorePaths) {
		t.Fatalf("expected ignore paths to be preserved")
	}
	if !reflect.DeepEqual(config.IgnoreNames, before.IgnoreNames) {
		t.Fatalf("expected ignore names to be preserved")
	}
	if result.ConfigPath != initResult.ConfigPath {
		t.Fatalf("expected result config path %q, got %q", initResult.ConfigPath, result.ConfigPath)
	}
	if result.AddedPath != projectDir {
		t.Fatalf("expected added path %q, got %q", projectDir, result.AddedPath)
	}
	if !strings.Contains(result.Message, "Added watch directory: "+projectDir) {
		t.Fatalf("expected add message to include path, got %q", result.Message)
	}
}

func TestConfigAddWatchDefaultsToCurrentDirectory(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	projectDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir project: %v", err)
	}
	defer os.Chdir(originalDir)

	_, err = workgraph.AddWatchDir(workgraph.ConfigWatchConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("add watch dir failed: %v", err)
	}

	config := readInitConfig(t, initResult.ConfigPath)
	resolvedProjectDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve project dir: %v", err)
	}
	if config.WatchDirs[0] != resolvedProjectDir {
		t.Fatalf("expected current directory %q to be first watch dir, got %q", resolvedProjectDir, config.WatchDirs)
	}
}

func TestConfigAddWatchIsIdempotent(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	projectDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	for i := 0; i < 2; i++ {
		_, err := workgraph.AddWatchDir(workgraph.ConfigWatchConfig{
			HomeDir: homeDir,
			Path:    projectDir,
		})
		if err != nil {
			t.Fatalf("add watch dir failed: %v", err)
		}
	}

	config := readInitConfig(t, initResult.ConfigPath)
	count := 0
	for _, watchDir := range config.WatchDirs {
		if watchDir == projectDir {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected project dir to appear once, got %d in %q", count, config.WatchDirs)
	}
}

func TestConfigAddedWatchDirIsUsedBeforeHomeBudget(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	largeHome := filepath.Join(tempDir, "home")
	externalProject := filepath.Join(tempDir, "external")
	largeHomeChild := filepath.Join(largeHome, "Desktop")
	if err := os.MkdirAll(largeHomeChild, 0o755); err != nil {
		t.Fatalf("create large home dir: %v", err)
	}
	if err := os.MkdirAll(externalProject, 0o755); err != nil {
		t.Fatalf("create external project: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	writeInitConfig(t, initResult.ConfigPath, initConfigFile{
		WatchDirs:   []string{largeHome},
		IgnorePaths: []string{homeDir},
		IgnoreNames: []string{".git", "node_modules"},
	})

	_, err = workgraph.AddWatchDir(workgraph.ConfigWatchConfig{
		HomeDir: homeDir,
		Path:    externalProject,
	})
	if err != nil {
		t.Fatalf("add watch dir failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:         homeDir,
		DatabasePath:    initResult.DatabasePath,
		MaxWatchEntries: 1,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	target := filepath.Join(externalProject, "NewFile.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("create external project file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", target)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func assertNoEvent(t *testing.T, dbPath, operation, path string) {
	t.Helper()

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if eventExists(t, dbPath, operation, path) {
			t.Fatalf("expected no %s event for ignored path %q", operation, path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
