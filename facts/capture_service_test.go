package facts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestCaptureServiceWorkerTracksSpacedHomeAndRejectsDuplicates(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("user services require macOS or Linux")
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, ".config"))
	home := filepath.Join(root, "work graph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"__capture-worker", "--home", home, "--database", initialized.DatabasePath, "--watch", t.TempDir()}
	worker := exec.Command(workgraphFactsBinary, args...)
	if err := worker.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Process.Signal(syscall.SIGTERM); _ = worker.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "daemon.pid")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	status, err := workgraph.DaemonStatusForHome(home)
	if err != nil || !status.Running || status.PID != worker.Process.Pid {
		t.Fatalf("worker status: %+v %v", status, err)
	}
	output, err := runWorkgraphCommandWithTimeout(nil, 2*time.Second, args...)
	if err == nil || !strings.Contains(output, "already locked") {
		t.Fatalf("duplicate worker: %v %s", err, output)
	}
	output, err = runWorkgraphCommandAllowError(nil, "service", "install", "--home", home)
	if err == nil || !strings.Contains(output, "already running") {
		t.Fatalf("service took over manual capture: %v %s", err, output)
	}
}

func TestCaptureServiceInstallStatusAndUninstall(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("user services require macOS or Linux")
	}
	root := t.TempDir()
	home := filepath.Join(root, "work graph % state")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: home}); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"launchctl", "systemctl"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HOME/calls\"\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, ".config"))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := runworkgraph(t, repoRoot(t), "service", "install", "--home", home)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, output)
	}
	pattern := filepath.Join(root, "Library", "LaunchAgents", "com.workgraph.capture.*.plist")
	if runtime.GOOS == "linux" {
		pattern = filepath.Join(root, ".config", "systemd", "user", "workgraph-capture-*.service")
	}
	paths, err := filepath.Glob(pattern)
	if err != nil || len(paths) != 1 {
		t.Fatalf("service definition missing: %v %v", paths, err)
	}
	definition, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"__capture-worker", "--home"} {
		if !strings.Contains(string(definition), expected) {
			t.Fatalf("definition missing %s: %s", expected, definition)
		}
	}
	if runtime.GOOS == "darwin" {
		for _, key := range []string{"RunAtLoad", "KeepAlive", "ThrottleInterval"} {
			if !strings.Contains(string(definition), key) {
				t.Fatal(key)
			}
		}
	} else if !strings.Contains(string(definition), "Restart=on-failure") || !strings.Contains(string(definition), "WantedBy=default.target") {
		t.Fatal(string(definition))
	}
	output, err = runworkgraph(t, repoRoot(t), "service", "status", "--home", home)
	if err != nil || !strings.Contains(string(output), paths[0]) {
		t.Fatalf("status: %v %s", err, output)
	}
	output, err = runworkgraph(t, repoRoot(t), "stop", "--home", home)
	if err != nil {
		t.Fatalf("stop installed service: %v %s", err, output)
	}
	output, err = runworkgraph(t, repoRoot(t), "service", "install", "--home", home)
	if err != nil {
		t.Fatalf("reinstall: %v %s", err, output)
	}
	command := "launchctl"
	if runtime.GOOS == "linux" {
		command = "systemctl"
	}
	commandPath := filepath.Join(bin, command)
	original, err := os.ReadFile(commandPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\necho supervisor-unavailable >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	output, err = runworkgraph(t, repoRoot(t), "service", "uninstall", "--home", home)
	if err == nil || !strings.Contains(string(output), "supervisor-unavailable") {
		t.Fatalf("uninstall hid failure: %v %s", err, output)
	}
	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatalf("failed unload removed definition: %v", err)
	}
	if err := os.WriteFile(commandPath, original, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		output, err = runworkgraph(t, repoRoot(t), "service", "uninstall", "--home", home)
		if err != nil {
			t.Fatalf("uninstall: %v %s", err, output)
		}
	}
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Fatalf("definition remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "workgraph.db")); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(filepath.Join(root, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "disable") {
		t.Fatalf("service was not disabled: %s", calls)
	}
}
