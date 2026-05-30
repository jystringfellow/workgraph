package facts

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestStartStartsBackgroundCaptureWithConfiguredWatchDirs(t *testing.T) {
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
	writeInitConfig(t, initResult.ConfigPath, initConfigFile{
		WatchDirs:   []string{watchDir},
		IgnorePaths: []string{homeDir},
		IgnoreNames: []string{".git", "node_modules"},
	})

	output := runWorkgraphCommand(t, nil, "start", "--home", homeDir, "--database", initResult.DatabasePath)
	defer runWorkgraphCommand(t, nil, "stop", "--home", homeDir)

	if !strings.Contains(output, "started") {
		t.Fatalf("expected capture start output, got:\n%s", output)
	}
	if !strings.Contains(output, watchDir) {
		t.Fatalf("expected start output to include configured watch dir %q, got:\n%s", watchDir, output)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "daemon.pid")); err != nil {
		t.Fatalf("expected capture pid under workgraph home: %v", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "daemon.log")); err != nil {
		t.Fatalf("expected capture log under workgraph home: %v", err)
	}
}

func TestStartUsesDefaultSaneWatchRootsAfterInit(t *testing.T) {
	userHome := fakeUserHomeWithDirs(t, "Desktop", "Documents")
	env := []string{"HOME=" + userHome, "USERPROFILE=" + userHome}
	homeDir := filepath.Join(userHome, ".workgraph")
	dbPath := filepath.Join(homeDir, "workgraph.db")

	runWorkgraphCommand(t, env, "init")
	output := runWorkgraphCommand(t, env, "start", "--home", homeDir)
	defer runWorkgraphCommand(t, env, "stop", "--home", homeDir)

	for _, expected := range []string{filepath.Join(userHome, "Desktop"), filepath.Join(userHome, "Documents")} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected capture to watch default directory %q, got:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "Watching: "+userHome+"\n") {
		t.Fatalf("expected capture not to watch broad user home %q, got:\n%s", userHome, output)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected init to create database %q: %v", dbPath, err)
	}
}

func TestStatusReportsRunningCaptureState(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	ignoredDir := filepath.Join(watchDir, "private")
	if err := os.MkdirAll(ignoredDir, 0o755); err != nil {
		t.Fatalf("create ignored dir: %v", err)
	}

	initResult, err := workgraph.Init(workgraph.InitConfig{
		HomeDir: homeDir,
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	writeInitConfig(t, initResult.ConfigPath, initConfigFile{
		WatchDirs:   []string{watchDir},
		IgnorePaths: []string{ignoredDir},
		IgnoreNames: []string{"node_modules"},
	})

	runWorkgraphCommand(t, nil, "start", "--home", homeDir, "--database", initResult.DatabasePath)
	defer runWorkgraphCommand(t, nil, "stop", "--home", homeDir)

	output := runWorkgraphCommand(t, nil, "status", "--home", homeDir)
	for _, expected := range []string{"running", "PID:", watchDir, ignoredDir, "node_modules"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected capture status to include %q, got:\n%s", expected, output)
		}
	}
}

func TestStopStopsBackgroundCapture(t *testing.T) {
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
	writeInitConfig(t, initResult.ConfigPath, initConfigFile{
		WatchDirs:   []string{watchDir},
		IgnorePaths: []string{homeDir},
		IgnoreNames: []string{".git", "node_modules"},
	})

	runWorkgraphCommand(t, nil, "start", "--home", homeDir, "--database", initResult.DatabasePath)

	target := filepath.Join(watchDir, "kept.md")
	if err := os.WriteFile(target, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("create watched file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", target)

	output := runWorkgraphCommand(t, nil, "stop", "--home", homeDir)
	if !strings.Contains(output, "stopped") {
		t.Fatalf("expected stop output, got:\n%s", output)
	}
	assertEventExists(t, initResult.DatabasePath, "created", target)
	assertCaptureNotRunning(t, homeDir)
}

func TestTopLevelStartRefusesBeforeInit(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")

	output, err := runWorkgraphCommandAllowError(nil, "start", "--home", homeDir)
	if err == nil {
		runWorkgraphCommand(t, nil, "stop", "--home", homeDir)
		t.Fatalf("expected capture start to fail before init")
	}
	if !strings.Contains(output, "workgraph init") {
		t.Fatalf("expected init guidance, got:\n%s", output)
	}
}

func TestTopLevelRunIsNotACommand(t *testing.T) {
	output, err := runWorkgraphCommandAllowError(nil, "run")
	if err == nil {
		t.Fatalf("expected run command to be rejected")
	}
	if !strings.Contains(output, "unknown command: run") {
		t.Fatalf("expected unknown command output for run, got:\n%s", output)
	}
}

func TestBackgroundCaptureDoesNotRecordIgnoredPaths(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	watchDir := filepath.Join(tempDir, "project")
	ignoredPath := filepath.Join(watchDir, "private")
	ignoredNameDir := filepath.Join(watchDir, "node_modules")
	for _, dir := range []string{ignoredPath, ignoredNameDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create ignored dir %q: %v", dir, err)
		}
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

	runWorkgraphCommand(t, nil, "start", "--home", homeDir, "--database", initResult.DatabasePath)
	defer runWorkgraphCommand(t, nil, "stop", "--home", homeDir)

	visible := filepath.Join(watchDir, "visible.md")
	if err := os.WriteFile(visible, []byte("visible"), 0o644); err != nil {
		t.Fatalf("create visible file: %v", err)
	}
	waitForEvent(t, initResult.DatabasePath, "created", visible)

	ignoredByPath := filepath.Join(ignoredPath, "secret.md")
	if err := os.WriteFile(ignoredByPath, []byte("secret"), 0o644); err != nil {
		t.Fatalf("create ignored path file: %v", err)
	}
	assertNoEvent(t, initResult.DatabasePath, "created", ignoredByPath)

	ignoredByName := filepath.Join(ignoredNameDir, "package.json")
	if err := os.WriteFile(ignoredByName, []byte("{}"), 0o644); err != nil {
		t.Fatalf("create ignored name file: %v", err)
	}
	assertNoEvent(t, initResult.DatabasePath, "created", ignoredByName)
}

func TestStartForegroundKeepsCaptureAttached(t *testing.T) {
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

	output, err := runWorkgraphBinaryWithTimeout(t, nil, 5*time.Second, "start", "--foreground", "--home", homeDir, "--database", initResult.DatabasePath, "--watch", watchDir)
	if err == nil {
		t.Fatalf("expected foreground run to remain attached until interrupted")
	}
	if !strings.Contains(output, "capture is running") {
		t.Fatalf("expected foreground run output, got:\n%s", output)
	}
}

func runWorkgraphCommand(t *testing.T, env []string, args ...string) string {
	t.Helper()

	output, err := runWorkgraphCommandAllowError(env, args...)
	if err != nil {
		t.Fatalf("workgraph %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}

	return output
}

func runWorkgraphCommandAllowError(env []string, args ...string) (string, error) {
	return runWorkgraphCommandWithTimeout(env, 30*time.Second, args...)
}

func runWorkgraphCommandWithTimeout(env []string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmdArgs := append([]string{"run", "./cmd/workgraph"}, args...)
	cmd := exec.CommandContext(ctx, "go", cmdArgs...)
	cmd.Dir = repoRootForDaemon()
	cmd.Env = daemonCommandEnv(env)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func runWorkgraphBinaryWithTimeout(t *testing.T, env []string, timeout time.Duration, args ...string) (string, error) {
	t.Helper()

	binPath := filepath.Join(t.TempDir(), "workgraph")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/workgraph")
	build.Dir = repoRootForDaemon()
	build.Env = daemonCommandEnv(env)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build workgraph: %v\n%s", err, output)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Dir = repoRootForDaemon()
	cmd.Env = daemonCommandEnv(env)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func daemonCommandEnv(env []string) []string {
	commandEnv := append([]string{}, os.Environ()...)
	if os.Getenv("GOMODCACHE") == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			commandEnv = append(commandEnv, "GOMODCACHE="+filepath.Join(userHome, "go", "pkg", "mod"))
		}
	}
	return append(commandEnv, env...)
}

func repoRootForDaemon() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return filepath.Dir(wd)
}

func assertCaptureNotRunning(t *testing.T, homeDir string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	pidPath := filepath.Join(homeDir, "daemon.pid")
	for time.Now().Before(deadline) {
		if _, err := os.Stat(pidPath); os.IsNotExist(err) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("expected daemon pid file to be removed")
}
