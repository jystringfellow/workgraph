package facts

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
	_ "github.com/mattn/go-sqlite3"
)

func TestRunStartsEventCaptureAfterInit(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
		PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	defer capture.Close()

	if !strings.Contains(capture.Status.Message, "capture is running") {
		t.Fatalf("expected capture running message, got %q", capture.Status.Message)
	}
	if len(capture.Status.WatchDirs) != 1 || capture.Status.WatchDirs[0] != watchDir {
		t.Fatalf("expected run to watch %q, got %#v", watchDir, capture.Status.WatchDirs)
	}
}

func TestRunRefusesBeforeInit(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")

	_, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:   homeDir,
		WatchDirs: []string{t.TempDir()},
	})
	if err == nil {
		t.Fatalf("expected run to fail before init")
	}
	if !errors.Is(err, workgraph.ErrNotInitialized) {
		t.Fatalf("expected ErrNotInitialized, got %v", err)
	}
	if !strings.Contains(err.Error(), "workgraph init") {
		t.Fatalf("expected init guidance, got %q", err.Error())
	}
}

func TestRunCapturesFileActivity(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
		PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	target := filepath.Join(watchDir, "notes.md")
	if err := os.WriteFile(target, []byte("first"), 0o644); err != nil {
		t.Fatalf("create watched file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", target)

	if err := os.WriteFile(target, []byte("second"), 0o644); err != nil {
		t.Fatalf("modify watched file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "modified", target)

	if err := os.Remove(target); err != nil {
		t.Fatalf("delete watched file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "deleted", target)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunReportsCapturedFileEvents(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	target := filepath.Join(watchDir, "visible.md")
	if err := os.WriteFile(target, []byte("show me"), 0o644); err != nil {
		t.Fatalf("create watched file: %v", err)
	}

	event := waitForReportedEvent(t, capture.Events, "created", target)
	if event.Type != "file.created" {
		t.Fatalf("expected file.created event, got %q", event.Type)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunCapturesRapidFileLifecycleWithoutLosingTheFile(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	target := filepath.Join(watchDir, "quick.md")
	if err := os.WriteFile(target, []byte("first"), 0o644); err != nil {
		t.Fatalf("create watched file: %v", err)
	}
	if err := os.WriteFile(target, []byte("second"), 0o644); err != nil {
		t.Fatalf("modify watched file: %v", err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatalf("delete watched file: %v", err)
	}

	waitForEvent(t, initResult.DatabasePath, "created", target)
	waitForEvent(t, initResult.DatabasePath, "deleted", target)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunCoalescesEditorSafeSaveIntoModification(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}
	target := filepath.Join(watchDir, "notes.txt")
	if err := os.WriteFile(target, []byte("before"), 0o644); err != nil {
		t.Fatalf("create existing document: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	scratch := target + ".sb-4f50fe81-TXpPoa"
	if err := os.WriteFile(scratch, []byte("after"), 0o644); err != nil {
		t.Fatalf("create editor scratch file: %v", err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove original document: %v", err)
	}
	if err := os.WriteFile(target, []byte("after"), 0o644); err != nil {
		t.Fatalf("replace original document: %v", err)
	}
	if err := os.Remove(scratch); err != nil {
		t.Fatalf("remove editor scratch file: %v", err)
	}

	waitForEvent(t, initResult.DatabasePath, "modified", target)
	assertNoRunEvent(t, initResult.DatabasePath, "created", target)
	assertNoRunEvent(t, initResult.DatabasePath, "deleted", target)
	assertNoRunEvent(t, initResult.DatabasePath, "created", scratch)
	assertNoRunEvent(t, initResult.DatabasePath, "deleted", scratch)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunIgnoresEditorSafeSaveScratchFiles(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	scratch := filepath.Join(watchDir, "notes.txt.sb-4f50fe81-TXpPoa")
	if err := os.WriteFile(scratch, []byte("scratch"), 0o644); err != nil {
		t.Fatalf("create editor scratch file: %v", err)
	}
	if err := os.Remove(scratch); err != nil {
		t.Fatalf("remove editor scratch file: %v", err)
	}

	assertNoRunEvent(t, initResult.DatabasePath, "created", scratch)
	assertNoRunEvent(t, initResult.DatabasePath, "deleted", scratch)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunPreservesWrittenEventsWhenStopped(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
		PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	target := filepath.Join(watchDir, "kept.md")
	if err := os.WriteFile(target, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("create watched file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", target)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	assertEventExists(t, initResult.DatabasePath, "created", target)
}

func TestRunSkipsInaccessibleWatchSubtrees(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based inaccessible directory setup is not portable to Windows")
	}

	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	inaccessibleDir := filepath.Join(watchDir, "blocked")
	if err := os.MkdirAll(inaccessibleDir, 0o755); err != nil {
		t.Fatalf("create inaccessible dir: %v", err)
	}
	if err := os.Chmod(inaccessibleDir, 0); err != nil {
		t.Fatalf("make directory inaccessible: %v", err)
	}
	defer os.Chmod(inaccessibleDir, 0o755)

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
	})
	if err != nil {
		t.Fatalf("run start should skip inaccessible subtrees, got: %v", err)
	}
	defer capture.Close()

	if len(capture.Status.WatchDirs) != 1 || capture.Status.WatchDirs[0] != watchDir {
		t.Fatalf("expected run to keep watching %q, got %#v", watchDir, capture.Status.WatchDirs)
	}
}

func TestRunSkipsUnsupportedSpecialFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket setup is not portable to Windows")
	}

	tempDir, err := os.MkdirTemp("/private/tmp", "workgraph-special-")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	fifoPath := filepath.Join(watchDir, "app.fifo")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("create watch dir: %v", err)
	}
	if err := syscall.Mkfifo(fifoPath, 0o644); err != nil {
		t.Fatalf("create fifo: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
	})
	if err != nil {
		t.Fatalf("run start should skip unsupported special files, got: %v", err)
	}
	defer capture.Close()
}

func TestRunSkipsGeneratedAppleIndexDirectories(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	generatedDir := filepath.Join(watchDir, "DerivedData", "Example", "Index.noindex", "DataStore")
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		t.Fatalf("create generated dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
	})
	if err != nil {
		t.Fatalf("run start should skip generated Apple index directories, got: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	visible := filepath.Join(watchDir, "notes.md")
	if err := os.WriteFile(visible, []byte("visible"), 0o644); err != nil {
		t.Fatalf("create visible file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", visible)

	generated := filepath.Join(generatedDir, "Unit.pcm")
	if err := os.WriteFile(generated, []byte("generated"), 0o644); err != nil {
		t.Fatalf("create generated file: %v", err)
	}
	assertNoRunEvent(t, initResult.DatabasePath, "created", generated)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunStopsAddingWatchersAtResourceBudget(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	for _, dir := range []string{
		watchDir,
		filepath.Join(watchDir, "one"),
		filepath.Join(watchDir, "two"),
		filepath.Join(watchDir, "three"),
	} {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:         homeDir,
		DatabasePath:    initResult.DatabasePath,
		WatchDirs:       []string{watchDir},
		MaxWatchEntries: 1,
	})
	if err != nil {
		t.Fatalf("run start should keep capture alive after watch budget is reached, got: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	if !capture.Status.WatchLimitReached {
		t.Fatalf("expected watch limit to be reported as reached")
	}
	if capture.Status.WatchCount != 1 {
		t.Fatalf("expected one registered watcher, got %d", capture.Status.WatchCount)
	}
	if capture.Status.WatchLimit != 1 {
		t.Fatalf("expected watch limit 1, got %d", capture.Status.WatchLimit)
	}
	if !strings.Contains(capture.Status.Message, "Watch limit reached") {
		t.Fatalf("expected run message to report watch limit, got %q", capture.Status.Message)
	}

	visible := filepath.Join(watchDir, "visible.md")
	if err := os.WriteFile(visible, []byte("visible"), 0o644); err != nil {
		t.Fatalf("create visible file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", visible)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunRegistersConfiguredRootsBeforeDescending(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	firstRoot := filepath.Join(tempDir, "Documents")
	secondRoot := filepath.Join(tempDir, "Downloads")
	if err := os.MkdirAll(filepath.Join(firstRoot, "large", "nested"), 0o755); err != nil {
		t.Fatalf("create first root tree: %v", err)
	}
	if err := os.MkdirAll(secondRoot, 0o755); err != nil {
		t.Fatalf("create second root: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:         homeDir,
		DatabasePath:    initResult.DatabasePath,
		WatchDirs:       []string{firstRoot, secondRoot},
		MaxWatchEntries: 2,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	expectedRegistered := []string{firstRoot, secondRoot}
	if !sameStrings(capture.Status.RegisteredWatchDirs, expectedRegistered) {
		t.Fatalf("expected configured roots to be registered before descendants as %q, got %q", expectedRegistered, capture.Status.RegisteredWatchDirs)
	}

	target := filepath.Join(secondRoot, "visible.md")
	if err := os.WriteFile(target, []byte("visible"), 0o644); err != nil {
		t.Fatalf("create file in second root: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", target)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunReportsRegisteredAndUnwatchedDirectoriesAtResourceBudget(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	firstChild := filepath.Join(watchDir, "alpha")
	unwatchedChild := filepath.Join(watchDir, "bravo")
	for _, dir := range []string{watchDir, firstChild, unwatchedChild} {
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

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:         homeDir,
		DatabasePath:    initResult.DatabasePath,
		WatchDirs:       []string{watchDir},
		MaxWatchEntries: 2,
	})
	if err != nil {
		t.Fatalf("run start should keep capture alive after watch budget is reached, got: %v", err)
	}
	defer capture.Close()

	expectedRegistered := []string{watchDir, firstChild}
	if !sameStrings(capture.Status.RegisteredWatchDirs, expectedRegistered) {
		t.Fatalf("expected registered watch dirs %q, got %q", expectedRegistered, capture.Status.RegisteredWatchDirs)
	}
	if capture.Status.WatchLimitPath != unwatchedChild {
		t.Fatalf("expected first unwatched directory %q, got %q", unwatchedChild, capture.Status.WatchLimitPath)
	}
	if strings.Contains(capture.Status.Message, "Registered watch directories:") {
		t.Fatalf("expected run message not to include registered directory sample, got %q", capture.Status.Message)
	}
	if !strings.Contains(capture.Status.Message, "Watching: 1 configured directory") {
		t.Fatalf("expected run message to summarize configured directories, got %q", capture.Status.Message)
	}
	if !strings.Contains(capture.Status.Message, "Last watched directory: "+firstChild) {
		t.Fatalf("expected run message to include last watched directory, got %q", capture.Status.Message)
	}
	if !strings.Contains(capture.Status.Message, "Next unwatched directory: "+unwatchedChild) {
		t.Fatalf("expected run message to include first unwatched directory, got %q", capture.Status.Message)
	}
	if !strings.Contains(capture.Status.Message, "Prioritize important directories with workgraph config add-watch.") {
		t.Fatalf("expected run message to include priority directory guidance, got %q", capture.Status.Message)
	}
}

func TestRunPrioritizesUserFacingDirectoriesBeforeHiddenCachesAtWatchBudget(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "home")
	desktopDir := filepath.Join(watchDir, "Desktop")
	cacheDir := filepath.Join(watchDir, ".cache")
	cacheChild := filepath.Join(cacheDir, "runtime")
	if err := os.MkdirAll(desktopDir, 0o755); err != nil {
		t.Fatalf("create desktop dir: %v", err)
	}
	if err := os.MkdirAll(cacheChild, 0o755); err != nil {
		t.Fatalf("create cache dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:         homeDir,
		DatabasePath:    initResult.DatabasePath,
		WatchDirs:       []string{watchDir},
		MaxWatchEntries: 2,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	expectedRegistered := []string{watchDir, desktopDir}
	if !sameStrings(capture.Status.RegisteredWatchDirs, expectedRegistered) {
		t.Fatalf("expected user-facing directories to be registered first as %q, got %q", expectedRegistered, capture.Status.RegisteredWatchDirs)
	}

	desktopFile := filepath.Join(desktopDir, "testing-watch.txt")
	if err := os.WriteFile(desktopFile, []byte("visible"), 0o644); err != nil {
		t.Fatalf("create desktop file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", desktopFile)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunSkipsImplicitTopLevelHiddenDirectories(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "home")
	visibleDir := filepath.Join(watchDir, "Desktop")
	hiddenDir := filepath.Join(watchDir, ".cache")
	if err := os.MkdirAll(visibleDir, 0o755); err != nil {
		t.Fatalf("create visible dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(hiddenDir, "runtime"), 0o755); err != nil {
		t.Fatalf("create hidden dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{watchDir},
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	defer capture.Close()

	if containsString(capture.Status.RegisteredWatchDirs, hiddenDir) {
		t.Fatalf("expected implicit top-level hidden dir %q not to be watched, got %q", hiddenDir, capture.Status.RegisteredWatchDirs)
	}
	if !containsString(capture.Status.RegisteredWatchDirs, visibleDir) {
		t.Fatalf("expected visible dir %q to be watched, got %q", visibleDir, capture.Status.RegisteredWatchDirs)
	}
}

func TestRunExplicitWatchRootOverridesTopLevelHiddenSkip(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	hiddenDir := filepath.Join(tempDir, "home", ".config")
	if err := os.MkdirAll(hiddenDir, 0o755); err != nil {
		t.Fatalf("create hidden dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{hiddenDir},
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	go func() {
		done <- capture.Run(ctx)
	}()

	if !containsString(capture.Status.RegisteredWatchDirs, hiddenDir) {
		t.Fatalf("expected explicit hidden watch root %q to be watched, got %q", hiddenDir, capture.Status.RegisteredWatchDirs)
	}

	target := filepath.Join(hiddenDir, "settings.json")
	if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
		t.Fatalf("create hidden root file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", target)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}

func TestRunConservativeRootStopsAtFolderOnlyChildren(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	documentsDir := filepath.Join(tempDir, "home", "Documents")
	appLibraryDir := filepath.Join(documentsDir, "Native Instruments")
	deepLibraryDir := filepath.Join(appLibraryDir, "Kontakt", "presets")
	if err := os.MkdirAll(deepLibraryDir, 0o755); err != nil {
		t.Fatalf("create app library tree: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	writeInitConfig(t, initResult.ConfigPath, initConfigFile{
		WatchDirs:             []string{documentsDir},
		ConservativeWatchDirs: []string{documentsDir},
		IgnorePaths:           []string{homeDir},
		IgnoreNames:           []string{".git", "node_modules"},
	})

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	defer capture.Close()

	if !containsString(capture.Status.RegisteredWatchDirs, documentsDir) {
		t.Fatalf("expected conservative root %q to be watched, got %q", documentsDir, capture.Status.RegisteredWatchDirs)
	}
	if !containsString(capture.Status.RegisteredWatchDirs, appLibraryDir) {
		t.Fatalf("expected immediate child %q to be watched, got %q", appLibraryDir, capture.Status.RegisteredWatchDirs)
	}
	if containsString(capture.Status.RegisteredWatchDirs, deepLibraryDir) {
		t.Fatalf("expected folder-only app library descendants not to be watched, got %q", capture.Status.RegisteredWatchDirs)
	}
}

func TestRunConservativeRootRecursesIntoWorkLikeChild(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	documentsDir := filepath.Join(tempDir, "home", "Documents")
	clientDir := filepath.Join(documentsDir, "Client")
	assetsDir := filepath.Join(clientDir, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatalf("create client tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clientDir, "brief.md"), []byte("notes"), 0o644); err != nil {
		t.Fatalf("create client document: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	writeInitConfig(t, initResult.ConfigPath, initConfigFile{
		WatchDirs:             []string{documentsDir},
		ConservativeWatchDirs: []string{documentsDir},
		IgnorePaths:           []string{homeDir},
		IgnoreNames:           []string{".git", "node_modules"},
	})

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	defer capture.Close()

	if !containsString(capture.Status.RegisteredWatchDirs, assetsDir) {
		t.Fatalf("expected work-like child descendants to be watched, got %q", capture.Status.RegisteredWatchDirs)
	}
}

func TestRunExplicitRootRecursesIntoFolderOnlyChildren(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	explicitDir := filepath.Join(tempDir, "Native Instruments")
	deepDir := filepath.Join(explicitDir, "Kontakt", "presets")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		t.Fatalf("create explicit tree: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{explicitDir},
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	defer capture.Close()

	if !containsString(capture.Status.RegisteredWatchDirs, deepDir) {
		t.Fatalf("expected explicit folder-only descendants to be watched, got %q", capture.Status.RegisteredWatchDirs)
	}
}

func TestRunExplicitCodeRootSkipsGeneratedBuildOutput(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	codeDir := filepath.Join(tempDir, "Code")
	sourceDir := filepath.Join(codeDir, "Cupcake", "Cupcake.API", "Controllers")
	buildDir := filepath.Join(codeDir, "Cupcake", "Cupcake.Tests", "bin", "Debug", "netcoreapp3.1")
	xcodeUserDataDir := filepath.Join(codeDir, "MacsyZones", "MacsyZones.xcodeproj", "project.xcworkspace", "xcuserdata")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("create source tree: %v", err)
	}
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("create build output tree: %v", err)
	}
	if err := os.MkdirAll(xcodeUserDataDir, 0o755); err != nil {
		t.Fatalf("create Xcode user state tree: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{codeDir},
	})
	if err != nil {
		t.Fatalf("run start failed: %v", err)
	}
	defer capture.Close()

	if !containsString(capture.Status.RegisteredWatchDirs, sourceDir) {
		t.Fatalf("expected source directory %q to be watched, got %q", sourceDir, capture.Status.RegisteredWatchDirs)
	}
	if containsString(capture.Status.RegisteredWatchDirs, buildDir) {
		t.Fatalf("expected generated build output %q not to be watched, got %q", buildDir, capture.Status.RegisteredWatchDirs)
	}
	if containsString(capture.Status.RegisteredWatchDirs, xcodeUserDataDir) {
		t.Fatalf("expected Xcode user state %q not to be watched, got %q", xcodeUserDataDir, capture.Status.RegisteredWatchDirs)
	}
}

func waitForEvent(t *testing.T, dbPath, operation, path string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if eventExists(t, dbPath, operation, path) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s event for %q", operation, path)
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func assertEventExists(t *testing.T, dbPath, operation, path string) {
	t.Helper()

	if !eventExists(t, dbPath, operation, path) {
		t.Fatalf("expected %s event for %q to be preserved", operation, path)
	}
}

func assertNoRunEvent(t *testing.T, dbPath, operation, path string) {
	t.Helper()

	deadline := time.Now().Add(350 * time.Millisecond)
	for time.Now().Before(deadline) {
		if eventExists(t, dbPath, operation, path) {
			t.Fatalf("expected no %s event for %q", operation, path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func eventExists(t *testing.T, dbPath, operation, path string) bool {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var count int
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM events
		WHERE source = 'file'
			AND type = ?
			AND json_extract(payload_json, '$.operation') = ?
			AND json_extract(payload_json, '$.path') = ?
	`, "file."+operation, operation, path).Scan(&count)
	if err != nil {
		t.Fatalf("query event: %v", err)
	}

	return count > 0
}

func waitForReportedEvent(t *testing.T, events <-chan workgraph.CapturedEvent, operation, path string) workgraph.CapturedEvent {
	t.Helper()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Operation == operation && event.Path == path {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for reported %s event for %q", operation, path)
		}
	}
}
