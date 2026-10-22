package facts

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestBridgeMCPRejectsControlQueryEventsAndCompletesWithEmptyProof(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	connectBridgedConnector(t, repoRoot(t), homeDir, "calendar.microsoft")
	now := time.Now().UTC().Add(-time.Minute)
	emitted, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: "calendar.microsoft", Now: now})
	if err != nil {
		t.Fatalf("emit calendar request: %v", err)
	}
	claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{
		HomeDir: homeDir, ConnectorID: "calendar.microsoft", Worker: "facts", Max: 1, Now: now, Lease: 10 * time.Minute,
	})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim calendar request: claims=%d error=%v", len(claims), err)
	}
	call := func(id int, arguments map[string]any) string {
		arguments["request_id"] = emitted.Request.ID
		arguments["claim_token"] = claims[0].ClaimToken
		encoded, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{"name": "capture_ingest", "arguments": arguments},
		})
		if err != nil {
			t.Fatalf("encode MCP call: %v", err)
		}
		return string(encoded)
	}
	controlTyped := []map[string]any{{
		"type": "calendar.microsoft.control_query", "timestamp": now.Format(time.RFC3339),
		"summary": "Proven-empty window control query", "payload": map[string]any{"control_query": true, "result_count": 0},
	}}
	controlFlagged := []map[string]any{{
		"type": "calendar.microsoft.event", "timestamp": now.Format(time.RFC3339),
		"external_id": "calendar.microsoft.event:00000000-0000-4000-8000-000000000000",
		"summary":     "Control query: no events in window",
		"payload":     map[string]any{"control_query": true, "proof": "wide control returned 7 events, none in window"},
	}}
	requests := []string{
		call(1, map[string]any{"events": controlTyped}),
		call(2, map[string]any{"events": controlFlagged}),
		call(3, map[string]any{"empty_proof": map[string]any{"control_query_passed": true}}),
	}
	command := exec.Command(workgraphFactsBinary, "bridge", "mcp", "--home", homeDir)
	command.Dir = repoRoot(t)
	command.Stdin = strings.NewReader(strings.Join(requests, "\n") + "\n")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("run local bridge MCP: %v\n%s", err, output)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected three MCP responses, got:\n%s", output)
	}
	for index, line := range lines[:2] {
		for _, expected := range []string{`"isError":true`, "event 1", "control", "empty_proof"} {
			if !strings.Contains(line, expected) {
				t.Fatalf("expected control-query submission %d to be rejected mentioning %q, got:\n%s", index+1, expected, line)
			}
		}
	}
	if strings.Contains(lines[2], `"isError":true`) || !strings.Contains(lines[2], `"RequestCompleted":true`) {
		t.Fatalf("expected empty_proof submission to complete the request, got:\n%s", lines[2])
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	var events int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE source = 'calendar.microsoft'`).Scan(&events); err != nil {
		t.Fatalf("count calendar events: %v", err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM capture_requests WHERE id = ?`, emitted.Request.ID).Scan(&status); err != nil {
		t.Fatalf("read request status: %v", err)
	}
	if events != 0 || status != "completed" {
		t.Fatalf("expected completed request with no stored events, got events=%d status=%q", events, status)
	}
}

func TestBridgedIngestDerivesSummaryFromProviderTitle(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	for _, connector := range []string{"mail.microsoft", "calendar.microsoft", "azure.boards", "notion.activity"} {
		connectBridgedConnector(t, repoRoot(t), homeDir, connector)
	}
	batches := []struct {
		source string
		input  string
	}{
		{"mail.microsoft", `[{"type":"mail.microsoft.message","timestamp":"2026-10-22T15:00:00Z","external_id":"mail-1","payload":{"subject":" Quarterly planning ","sender":"a@example.com"}}]`},
		{"calendar.microsoft", `[{"type":"calendar.microsoft.event","timestamp":"2026-10-22T16:00:00Z","external_id":"cal-1","payload":{"subject":"Squad review","start":{"dateTime":"2026-10-22T09:00:00"}}}]`},
		{"azure.boards", `[{"type":"azure.boards.workitem","timestamp":"2026-10-22T17:00:00Z","external_id":"wi-1:3","payload":{"id":1,"title":"Migrate settings page","state":"Active"}}]`},
		{"notion.activity", `[{"type":"notion.page_updated","timestamp":"2026-10-22T18:00:00Z","external_id":"page-1:2026-10-22T18:00:00Z","payload":{"title":"Launch plan","url":"https://notion.so/page-1"}}]`},
		{"mail.microsoft", `[{"type":"mail.microsoft.message","timestamp":"2026-10-22T19:00:00Z","external_id":"mail-2","summary":"Worker summary","payload":{"subject":"Ignored subject"}}]`},
	}
	for _, batch := range batches {
		if _, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{HomeDir: homeDir, Source: batch.source, Input: strings.NewReader(batch.input)}); err != nil {
			t.Fatalf("ingest %s: %v", batch.source, err)
		}
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	rows, err := db.Query(`SELECT type, COALESCE(summary, '') FROM events ORDER BY timestamp`)
	if err != nil {
		t.Fatalf("read summaries: %v", err)
	}
	defer rows.Close()
	var summaries []string
	for rows.Next() {
		var eventType, summary string
		if err := rows.Scan(&eventType, &summary); err != nil {
			t.Fatalf("scan summary: %v", err)
		}
		summaries = append(summaries, eventType+"="+summary)
	}
	expected := []string{
		"mail.microsoft.message=Quarterly planning",
		"calendar.microsoft.event=Squad review",
		"azure.boards.workitem=Migrate settings page",
		"notion.page_updated=Launch plan",
		"mail.microsoft.message=Worker summary",
	}
	if strings.Join(summaries, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("expected provider-title summaries:\n%s\ngot:\n%s", strings.Join(expected, "\n"), strings.Join(summaries, "\n"))
	}
}

func TestStoreOpenRemovesControlQueryEventsAndBackfillsBridgedSummaries(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	dbPath := filepath.Join(homeDir, "workgraph.db")
	now := time.Now().UTC().Add(-time.Hour)
	stored := []storedEvent{
		{ID: "typed-control", Source: "azure.boards", Type: "azure.boards.control_query", Timestamp: now, Payload: `{"query":"wiql","result_count":0}`, Summary: "Proven-empty window control query"},
		{ID: "flagged-control", Source: "mail.microsoft", Type: "mail.microsoft.message", Timestamp: now, Payload: `{"control_query":true,"proof":"inbox search returned 0"}`, Summary: "Control query: no messages in window"},
		{ID: "object-control", Source: "calendar.microsoft", Type: "calendar.microsoft.event", Timestamp: now, Payload: `{"control_query":{"result_count":7}}`},
		{ID: "real-mail", Source: "mail.microsoft", Type: "mail.microsoft.message", Timestamp: now, Payload: `{"subject":"MMO for Legacy Reports","control_query":false}`},
		{ID: "real-calendar", Source: "calendar.microsoft", Type: "calendar.microsoft.event", Timestamp: now, Payload: `{"subject":"Focus"}`},
		{ID: "real-notion", Source: "notion.activity", Type: "notion.page_updated", Timestamp: now, Payload: `{"title":"GenAI Guild FAQ"}`},
		{ID: "real-workitem", Source: "azure.boards", Type: "azure.boards.workitem", Timestamp: now, Payload: `{"title":"Migrate phrasing settings"}`},
		{ID: "summarized-mail", Source: "mail.microsoft", Type: "mail.microsoft.message", Timestamp: now, Payload: `{"subject":"Other"}`, Summary: "Kept summary"},
	}
	for _, event := range stored {
		insertEvent(t, dbPath, event)
	}
	if output, err := runworkgraph(t, repoRoot(t), "init", "--home", homeDir); err != nil {
		t.Fatalf("reopen store: %v\n%s", err, output)
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	rows, err := db.Query(`SELECT id, COALESCE(summary, '') FROM events ORDER BY id`)
	if err != nil {
		t.Fatalf("read repaired events: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var id, summary string
		if err := rows.Scan(&id, &summary); err != nil {
			t.Fatalf("scan repaired event: %v", err)
		}
		got[id] = summary
	}
	expected := map[string]string{
		"real-mail":       "MMO for Legacy Reports",
		"real-calendar":   "Focus",
		"real-notion":     "GenAI Guild FAQ",
		"real-workitem":   "Migrate phrasing settings",
		"summarized-mail": "Kept summary",
	}
	if len(got) != len(expected) {
		t.Fatalf("expected control-query events removed and real events kept, got %#v", got)
	}
	for id, summary := range expected {
		if got[id] != summary {
			t.Fatalf("expected %s summary %q, got %#v", id, summary, got)
		}
	}

	today, err := runworkgraph(t, repoRoot(t), "today", "--home", homeDir)
	if err != nil {
		t.Fatalf("run today: %v\n%s", err, today)
	}
	if strings.Contains(strings.ToLower(string(today)), "control query") {
		t.Fatalf("expected today to omit control-query events, got:\n%s", today)
	}
}
