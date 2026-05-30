package workgraph

import (
	"path/filepath"
	"testing"
)

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
