package workgraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStartDaemonDoesNotLaunchWhenProcessDiscoveryFails(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := Init(InitConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("initialize workgraph: %v", err)
	}

	original := listDaemonProcesses
	listDaemonProcesses = func() ([]daemonProcess, error) {
		return nil, errors.New("process inspection unavailable")
	}
	t.Cleanup(func() { listDaemonProcesses = original })

	_, err := StartDaemon(DaemonConfig{HomeDir: homeDir, WatchDirs: []string{t.TempDir()}})
	if err == nil || !strings.Contains(err.Error(), "process inspection unavailable") {
		t.Fatalf("expected process discovery error, got %v", err)
	}
}

func TestWaitForDaemonReadyWaitsForMatchingPIDFile(t *testing.T) {
	homeDir := t.TempDir()
	status := DaemonStatus{
		Running:      true,
		PID:          os.Getpid(),
		HomeDir:      homeDir,
		DatabasePath: filepath.Join(homeDir, "workgraph.db"),
	}
	contents, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("encode daemon state: %v", err)
	}
	if err := os.WriteFile(daemonStatePath(homeDir), contents, 0o600); err != nil {
		t.Fatalf("write daemon state: %v", err)
	}

	ready := make(chan error, 1)
	go func() {
		_, err := waitForDaemonReady(daemonReadyWait{homeDir: homeDir})
		ready <- err
	}()
	select {
	case err := <-ready:
		t.Fatalf("readiness returned before daemon PID file existed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := os.WriteFile(daemonPIDPath(homeDir), []byte("different-pid\n"), 0o600); err != nil {
		t.Fatalf("write mismatched daemon PID: %v", err)
	}
	select {
	case err := <-ready:
		t.Fatalf("readiness returned for mismatched daemon PID file: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := os.WriteFile(daemonPIDPath(homeDir), []byte(fmt.Sprintf("%d\n", status.PID)), 0o600); err != nil {
		t.Fatalf("write matching daemon PID: %v", err)
	}
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("readiness failed after matching daemon PID was written: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("readiness did not observe matching daemon PID file")
	}
}

func TestMatchingCaptureWorkerProcessesForHomeAndDatabase(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	dbPath := filepath.Join(homeDir, "workgraph.db")
	otherHome := filepath.Join(t.TempDir(), ".workgraph")
	otherDB := filepath.Join(otherHome, "workgraph.db")

	processes := []daemonProcess{
		{
			PID:     101,
			Command: "/tmp/go-build/workgraph __capture-worker --home " + homeDir + " --database " + dbPath,
		},
		{
			PID:     102,
			Command: "/tmp/go-build/workgraph __capture-worker --home " + homeDir,
		},
		{
			PID:     103,
			Command: "/tmp/go-build/workgraph __capture-worker --database " + dbPath,
		},
		{
			PID:     201,
			Command: "/tmp/go-build/workgraph __capture-worker --home " + otherHome + " --database " + otherDB,
		},
		{
			PID:     202,
			Command: "/tmp/go-build/workgraph start --home " + homeDir,
		},
	}

	matches := matchingCaptureWorkerProcesses(homeDir, dbPath, processes)
	got := []int{}
	for _, process := range matches {
		got = append(got, process.PID)
	}
	want := []int{101, 102, 103}
	if len(got) != len(want) {
		t.Fatalf("expected matching worker PIDs %#v, got %#v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected matching worker PIDs %#v, got %#v", want, got)
		}
	}
}

func TestWaitForDaemonReadyReportsStillStartingWhileWorkerUpgradesDatabase(t *testing.T) {
	homeDir := t.TempDir()
	databasePath := filepath.Join(homeDir, "workgraph.db")
	if err := os.WriteFile(databasePath, nil, 0o600); err != nil {
		t.Fatalf("create database file: %v", err)
	}
	release, err := lockDatabaseMigration(databasePath)
	if err != nil {
		t.Fatalf("hold migration lock: %v", err)
	}
	defer release()
	endStartup := beginDaemonStartup(homeDir, databasePath)
	defer endStartup()

	originalMaxWait, originalReportAfter := daemonStartupMaxWait, daemonUpgradeReportAfter
	daemonStartupMaxWait, daemonUpgradeReportAfter = 300*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { daemonStartupMaxWait, daemonUpgradeReportAfter = originalMaxWait, originalReportAfter })

	var progress strings.Builder
	_, err = waitForDaemonReady(daemonReadyWait{homeDir: homeDir, databasePath: databasePath, progress: &progress})
	var stillStarting *DaemonStillStartingError
	if !errors.As(err, &stillStarting) {
		t.Fatalf("expected a still-starting outcome, got %v", err)
	}
	if !stillStarting.UpgradingDatabase || stillStarting.PID != os.Getpid() {
		t.Fatalf("expected the live worker to be reported as upgrading, got %+v", stillStarting)
	}
	if got, want := err.Error(), "workgraph capture is still starting (upgrading database); check workgraph status"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
	if strings.Contains(err.Error(), "did not become ready") || strings.Contains(err.Error(), "fail") {
		t.Fatalf("expected the outcome not to claim start failed, got %q", err.Error())
	}
	if progress.String() != "Upgrading database…\n" {
		t.Fatalf("expected one upgrade progress line, got %q", progress.String())
	}
}
