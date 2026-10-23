package workgraph

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestLLMAssociateCadenceRunsHourlyAndRepeatsTheDailyRun(t *testing.T) {
	start := time.Date(2026, 10, 21, 14, 0, 0, 0, time.Local)
	cadence := newLLMAssociateCadence(start)
	if !cadence.due(start) || cadence.dailyRemaining != 0 {
		t.Fatalf("expected an hourly run at start without a daily run, got %+v", cadence)
	}
	cadence.record(start, LLMAssociateScheduleResult{Eligible: true, Created: true}, nil)
	if cadence.due(start.Add(59 * time.Minute)) {
		t.Fatalf("expected no run within the hour")
	}
	if !cadence.due(start.Add(time.Hour)) {
		t.Fatalf("expected the next hourly run")
	}

	daily := time.Date(2026, 10, 22, 5, 0, 0, 0, time.Local)
	for created := 0; created < llmAssociateDailyRequests; created++ {
		if !cadence.due(daily) {
			t.Fatalf("expected daily run %d to be due", created+1)
		}
		cadence.record(daily, LLMAssociateScheduleResult{Eligible: true}, nil)
		if !cadence.due(daily) {
			t.Fatalf("expected a coalesced daily check to keep the run open")
		}
		cadence.record(daily, LLMAssociateScheduleResult{Eligible: true, Created: true}, nil)
	}
	if cadence.due(daily.Add(time.Minute)) {
		t.Fatalf("expected the daily run to stop after %d requests", llmAssociateDailyRequests)
	}

	next := time.Date(2026, 10, 23, 5, 30, 0, 0, time.Local)
	if !cadence.due(next) {
		t.Fatalf("expected the next day's run")
	}
	cadence.record(next, LLMAssociateScheduleResult{}, nil)
	if cadence.dailyRemaining != 0 {
		t.Fatalf("expected the daily run to stop when nothing is eligible, got %d", cadence.dailyRemaining)
	}
	later := time.Date(2026, 10, 24, 6, 0, 0, 0, time.Local)
	cadence.due(later)
	cadence.record(later, LLMAssociateScheduleResult{}, errors.New("model unavailable"))
	if cadence.dailyRemaining != 0 {
		t.Fatalf("expected a failed check to end the daily run")
	}
}

func TestLLMTaskClaimHonorsTheTaskAllowedWhenAuthorized(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	home := filepath.Join(root, "state")
	initialized, err := Init(InitConfig{HomeDir: home, MemoryDir: filepath.Join(root, "memory")})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO capture_requests (id, connector_id, kind, since, until, params_json, status, attempts, available_at, created_at)
		VALUES ('verify-request', ?, ?, ?, ?, '{"task":"associate-verify","unit_ids":[]}', 'pending', 0, ?, ?)`,
		llmAssociateConnectorID, captureRequestKindLLMTask, now, now, now, now); err != nil {
		t.Fatal(err)
	}

	claimed, err := claimCaptureRequests(CaptureRequestClaimConfig{HomeDir: home, ConnectorID: llmAssociateConnectorID, Kind: captureRequestKindLLMTask, Worker: "codex-drain-race", llmTasks: []string{""}})
	if err != nil || len(claimed) != 0 {
		t.Fatalf("a worker authorized only for associate tasks claimed a verify request: %+v %v", claimed, err)
	}
	var status string
	var attempts int
	if err := db.QueryRow(`SELECT status, attempts FROM capture_requests WHERE id = 'verify-request'`).Scan(&status, &attempts); err != nil || status != "pending" || attempts != 0 {
		t.Fatalf("expected the verify request untouched, got %s %d %v", status, attempts, err)
	}
	claimed, err = claimCaptureRequests(CaptureRequestClaimConfig{HomeDir: home, ConnectorID: llmAssociateConnectorID, Kind: captureRequestKindLLMTask, Worker: "claude-code-drain-race", llmTasks: []string{llmTaskAssociateVerify}})
	if err != nil || len(claimed) != 1 || claimed[0].Request.ID != "verify-request" {
		t.Fatalf("expected the worker authorized for verify tasks to claim it, got %+v %v", claimed, err)
	}
}
