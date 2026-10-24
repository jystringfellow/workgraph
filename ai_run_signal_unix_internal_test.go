//go:build unix

package workgraph

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAIRunForwardsSignalReceivedBeforeChildStarts(t *testing.T) {
	t.Setenv("WORKGRAPH_AI_SESSION_ID", "")
	os.Unsetenv("WORKGRAPH_AI_SESSION_ID")
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := Init(InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init workgraph: %v", err)
	}
	queued := make(chan struct{})
	aiRunSignalQueued = func() { close(queued) }
	aiRunBeforeChildStart = func() {
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			t.Errorf("signal wrapper before child start: %v", err)
			return
		}
		select {
		case <-queued:
		case <-time.After(10 * time.Second):
			t.Errorf("SIGTERM was not queued before the child started")
		}
	}
	t.Cleanup(func() {
		aiRunBeforeChildStart = nil
		aiRunSignalQueued = nil
	})

	markers := t.TempDir()
	var stderr strings.Builder
	result, err := RunAISession(AIRunConfig{
		HomeDir:      homeDir,
		DatabasePath: initialized.DatabasePath,
		WorkingDir:   markers,
		Command:      []string{"sh", "-c", `while :; do sleep 0.05; done`},
		Stderr:       &stderr,
	})
	if err != nil {
		t.Fatalf("wrapped session failed: %v\n%s", err, stderr.String())
	}
	if result.ExitCode != 143 {
		t.Fatalf("expected the queued SIGTERM to reach the child and yield 143, got %d\n%s", result.ExitCode, stderr.String())
	}
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open event database: %v", err)
	}
	defer db.Close()
	var summary string
	if err := db.QueryRow(`SELECT summary FROM events WHERE type = 'ai.session_ended'`).Scan(&summary); err != nil {
		t.Fatalf("read end event: %v", err)
	}
	if summary != "AI session ended (signal SIGTERM)" {
		t.Fatalf("unexpected end summary: %q", summary)
	}
}
