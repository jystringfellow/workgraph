package facts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

const slackListDriftParams = `{"lists":["FDRIFT"],"list_options":{"FDRIFT":{"state":{"column":"Done","done_values":["true"]},"row_key_candidates":[["Related Message"],["Title"]]}}}`

func configureSlackListDriftBridge(t *testing.T) string {
	t.Helper()
	homeDir := initBridgedCaptureHome(t)
	if _, err := workgraph.ConfigureBridgedConnector(workgraph.ConnectorBridgeConfig{
		HomeDir: homeDir, ID: "slack.lists", BridgeParams: json.RawMessage(slackListDriftParams),
	}); err != nil {
		t.Fatalf("configure Slack Lists bridge: %v", err)
	}
	return homeDir
}

func claimSlackListSnapshot(t *testing.T, homeDir string, at time.Time) (string, string) {
	t.Helper()
	emitted, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{
		HomeDir: homeDir, ConnectorID: "slack.lists", Now: at,
	})
	if err != nil {
		t.Fatalf("emit Slack Lists snapshot: %v", err)
	}
	claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{
		HomeDir: homeDir, ConnectorID: "slack.lists", Worker: "facts", Max: 1, Now: at, Lease: 10 * time.Minute,
	})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim Slack Lists snapshot: claims=%d error=%v", len(claims), err)
	}
	return emitted.Request.ID, claims[0].ClaimToken
}

func ingestSlackListDriftRow(t *testing.T, homeDir string, at time.Time, details string, priority string, garbledHeader bool) workgraph.BridgedIngestResult {
	t.Helper()
	requestID, token := claimSlackListSnapshot(t, homeDir, at)
	fields := map[string]any{
		"Title":           "Action items from partner sync",
		"Done":            "true",
		"Priority":        priority,
		"Details":         details,
		"Related Message": "https://example.slack.com/archives/C0DRIFT/p1",
		"Due Date":        "",
	}
	if garbledHeader {
		fields["&quot;Due Date&quot;"] = ""
	}
	encoded, err := json.Marshal([]map[string]any{{"type": "slack.list_item", "payload": map[string]any{"list_id": "FDRIFT", "fields": fields}}})
	if err != nil {
		t.Fatalf("encode Slack List row: %v", err)
	}
	result, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{
		HomeDir: homeDir, RequestID: requestID, ClaimToken: token, Input: bytes.NewReader(encoded),
	})
	if err != nil {
		t.Fatalf("ingest Slack List row: %v", err)
	}
	return result
}

func countSlackListEvents(t *testing.T, homeDir string) int {
	t.Helper()
	db := openBridgedCaptureDatabase(t, homeDir)
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'slack.list_item'`).Scan(&count); err != nil {
		t.Fatalf("count Slack List events: %v", err)
	}
	return count
}

func TestSlackListSnapshotIgnoresMultilineTranscriptionDrift(t *testing.T) {
	homeDir := configureSlackListDriftBridge(t)
	now := time.Now().UTC().Add(-3 * time.Minute)
	original := "*Sync*\n• Copilot pilots\n• Champions group\n\nSent a follow-up."
	drifted := "*Sync*\n• Copilot pilots\nChampions group\n\t\n\nSent a follow-up."

	first := ingestSlackListDriftRow(t, homeDir, now, original, "Today", false)
	second := ingestSlackListDriftRow(t, homeDir, now.Add(time.Minute), drifted, " Today ", true)
	third := ingestSlackListDriftRow(t, homeDir, now.Add(2*time.Minute), original, "Later", false)

	if first.EventsInserted != 1 || second.EventsInserted != 0 || second.EventsDuplicate != 1 || third.EventsInserted != 1 {
		t.Fatalf("expected drift to deduplicate and a Priority change to revise: first=%#v second=%#v third=%#v", first, second, third)
	}
	if count := countSlackListEvents(t, homeDir); count != 2 {
		t.Fatalf("expected two stored revisions, got %d", count)
	}
}

func TestSlackListSnapshotDedupesAgainstStoredRevisionsAndTodayHidesRepeats(t *testing.T) {
	homeDir := configureSlackListDriftBridge(t)
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	db := openBridgedCaptureDatabase(t, homeDir)
	store := func(id string, at time.Time, details string) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"capture_semantics": "complete_snapshot",
			"content_hash":      "legacy-" + id,
			"list_id":           "FDRIFT",
			"row_key":           slackListRowKey(map[string]string{"Related Message": "https://example.slack.com/archives/C0DRIFT/p1"}),
			"done":              true,
			"observed_at":       at.UTC().Format(time.RFC3339Nano),
			"fields":            map[string]any{"Title": "Action items from partner sync", "Done": "true", "Priority": "Today", "Details": details, "Related Message": "https://example.slack.com/archives/C0DRIFT/p1"},
		})
		if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, summary, created_at)
			VALUES (?, 'slack', 'slack.list_item', ?, ?, 'slack-list:FDRIFT', 'Action items from partner sync', ?)`,
			id, at.UTC().Format(time.RFC3339Nano), string(payload), at.UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("store legacy Slack List revision: %v", err)
		}
	}
	store("legacy-a", dayStart.Add(time.Hour), "Body\n• one")
	store("legacy-b", dayStart.Add(2*time.Hour), "Body\none\n")
	store("legacy-c", dayStart.Add(3*time.Hour), "Body\n• one\n• two")

	today, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, Now: dayStart.Add(4 * time.Hour)})
	if err != nil {
		t.Fatalf("today: %v", err)
	}
	var listItems []string
	for _, event := range today.Events {
		if event.Type == "slack.list_item" {
			listItems = append(listItems, event.ID)
		}
	}
	if len(listItems) != 1 || listItems[0] != "legacy-a" {
		t.Fatalf("expected today to show only the first unchanged revision, got %#v", listItems)
	}

	result := ingestSlackListDriftRow(t, homeDir, time.Now().UTC().Add(-time.Minute), "Body\n- one\n- two", "Today", false)
	if result.EventsInserted != 0 || result.EventsDuplicate != 1 {
		t.Fatalf("expected unchanged row to deduplicate against the stored revision: %#v", result)
	}
}

func slackListRowKey(columns map[string]string) string {
	encoded, _ := json.Marshal(columns)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:16])
}

func callBridgeMCPIngest(t *testing.T, homeDir string, arguments map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(arguments)
	if err != nil {
		t.Fatalf("encode ingest arguments: %v", err)
	}
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "capture_ingest", "arguments": json.RawMessage(encoded)},
	})
	if err != nil {
		t.Fatalf("encode MCP request: %v", err)
	}
	command := exec.Command(workgraphFactsBinary, "bridge", "mcp", "--home", homeDir)
	command.Dir = repoRoot(t)
	command.Stdin = strings.NewReader(string(request) + "\n")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("call capture_ingest over MCP: %v", err)
	}
	var response map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output), &response); err != nil {
		t.Fatalf("decode MCP response: %v\n%s", err, output)
	}
	return response["result"].(map[string]any)
}

func readSlackListRequest(t *testing.T, homeDir string, requestID string) (string, string) {
	t.Helper()
	db := openBridgedCaptureDatabase(t, homeDir)
	var status, lease string
	if err := db.QueryRow(`SELECT status, COALESCE(lease_expires_at, '') FROM capture_requests WHERE id = ?`, requestID).Scan(&status, &lease); err != nil {
		t.Fatalf("read capture request: %v", err)
	}
	return status, lease
}

func TestBridgeMCPIngestsChunkedSlackListCSVUnderOneClaim(t *testing.T) {
	homeDir := configureSlackListDriftBridge(t)
	now := time.Now().UTC().Add(-time.Minute)
	requestID, token := claimSlackListSnapshot(t, homeDir, now)
	nearExpiry := time.Now().UTC().Add(time.Minute)
	if _, err := openBridgedCaptureDatabase(t, homeDir).Exec(`UPDATE capture_requests SET lease_expires_at = ? WHERE id = ?`,
		nearExpiry.Format(time.RFC3339Nano), requestID); err != nil {
		t.Fatalf("shorten claim lease: %v", err)
	}

	csvSnapshot := "Title,Done,Details\r\n\"Review design\",false,\"Line one\nline two\"\r\nSchedule meeting,true,\r\n"
	split := strings.Index(csvSnapshot, "line two")
	chunk := func(fragment string, continues bool) map[string]any {
		arguments := map[string]any{"request_id": requestID, "claim_token": token, "list_id": "FDRIFT", "snapshot_csv": fragment}
		if continues {
			arguments["snapshot_continues"] = true
		}
		return callBridgeMCPIngest(t, homeDir, arguments)
	}

	staged := chunk(csvSnapshot[:split], true)
	if staged["isError"] == true {
		t.Fatalf("stage first chunk: %#v", staged)
	}
	status, leaseAfter := readSlackListRequest(t, homeDir, requestID)
	renewedUntil, err := time.Parse(time.RFC3339Nano, leaseAfter)
	if status != "claimed" || err != nil || !renewedUntil.After(nearExpiry.Add(3*time.Minute)) {
		t.Fatalf("expected staged chunk to keep and renew the claim: status=%s lease=%s err=%v", status, leaseAfter, err)
	}
	if count := countSlackListEvents(t, homeDir); count != 0 {
		t.Fatalf("expected staged chunk to store no events, got %d", count)
	}

	final := chunk(csvSnapshot[split:], false)
	if final["isError"] == true {
		t.Fatalf("ingest final chunk: %#v", final)
	}
	if status, _ := readSlackListRequest(t, homeDir, requestID); status != "completed" {
		t.Fatalf("expected final chunk to complete the request, got %s", status)
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	var details string
	if err := db.QueryRow(`SELECT json_extract(payload_json, '$.fields.Details') FROM events WHERE summary = 'Review design'`).Scan(&details); err != nil {
		t.Fatalf("read chunked row: %v", err)
	}
	if details != "Line one\nline two" || countSlackListEvents(t, homeDir) != 2 {
		t.Fatalf("expected both rows with the split quoted field rejoined, details=%q", details)
	}
	var staging int
	if err := db.QueryRow(`SELECT COUNT(*) FROM slack_list_snapshot_chunks WHERE request_id = ?`, requestID).Scan(&staging); err != nil || staging != 0 {
		t.Fatalf("expected completion to clear staged chunks: count=%d err=%v", staging, err)
	}
}

func TestBridgeMCPDiscardsSlackListChunksFromStaleClaim(t *testing.T) {
	homeDir := configureSlackListDriftBridge(t)
	now := time.Now().UTC().Add(-time.Minute)
	requestID, staleToken := claimSlackListSnapshot(t, homeDir, now)
	stale := callBridgeMCPIngest(t, homeDir, map[string]any{
		"request_id": requestID, "claim_token": staleToken, "list_id": "FDRIFT",
		"snapshot_csv": "Title,Done\r\nStale row,false\r\n", "snapshot_continues": true,
	})
	if stale["isError"] == true {
		t.Fatalf("stage stale chunk: %#v", stale)
	}
	if err := workgraph.FailCaptureRequest(workgraph.CaptureRequestCapabilityConfig{
		HomeDir: homeDir, RequestID: requestID, ClaimToken: staleToken, Error: "worker restarted", Now: now,
	}); err != nil {
		t.Fatalf("fail stale claim: %v", err)
	}
	reclaimAt := now.Add(6 * time.Minute)
	claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{
		HomeDir: homeDir, ConnectorID: "slack.lists", Worker: "facts", Max: 1, Now: reclaimAt, Lease: 10 * time.Minute,
	})
	if err != nil || len(claims) != 1 || claims[0].Request.ID != requestID {
		t.Fatalf("reclaim Slack Lists snapshot: claims=%#v error=%v", claims, err)
	}
	final := callBridgeMCPIngest(t, homeDir, map[string]any{
		"request_id": requestID, "claim_token": claims[0].ClaimToken, "list_id": "FDRIFT",
		"snapshot_csv": "Title,Done\r\nFresh row,true\r\n",
	})
	if final["isError"] == true {
		t.Fatalf("ingest fresh snapshot: %#v", final)
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	var summaries string
	if err := db.QueryRow(`SELECT group_concat(summary) FROM events WHERE type = 'slack.list_item'`).Scan(&summaries); err != nil {
		t.Fatalf("read Slack List events: %v", err)
	}
	if summaries != "Fresh row" {
		t.Fatalf("expected only the fresh claim's rows, got %q", summaries)
	}
}

func TestBridgeMCPRejectedFinalSlackListChunkDiscardsStagedFragments(t *testing.T) {
	homeDir := configureSlackListDriftBridge(t)
	requestID, token := claimSlackListSnapshot(t, homeDir, time.Now().UTC().Add(-time.Minute))
	send := func(fragment string, continues bool) map[string]any {
		arguments := map[string]any{"request_id": requestID, "claim_token": token, "list_id": "FDRIFT", "snapshot_csv": fragment}
		if continues {
			arguments["snapshot_continues"] = true
		}
		return callBridgeMCPIngest(t, homeDir, arguments)
	}
	if staged := send("Title,Done\r\nSame row,false\r\n", true); staged["isError"] == true {
		t.Fatalf("stage chunk: %#v", staged)
	}
	rejected := send("Same row,true\r\n", false)
	encoded, _ := json.Marshal(rejected)
	if rejected["isError"] != true || !strings.Contains(string(encoded), "duplicate Slack List snapshot row key") || !strings.Contains(string(encoded), "resend the snapshot from its first fragment") {
		t.Fatalf("expected duplicate row keys across chunks to reject the snapshot with resend guidance: %s", encoded)
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	var staging int
	if err := db.QueryRow(`SELECT COUNT(*) FROM slack_list_snapshot_chunks WHERE request_id = ?`, requestID).Scan(&staging); err != nil || staging != 0 {
		t.Fatalf("expected rejected final chunk to discard staged fragments: count=%d err=%v", staging, err)
	}
	if status, _ := readSlackListRequest(t, homeDir, requestID); status != "claimed" || countSlackListEvents(t, homeDir) != 0 {
		t.Fatalf("expected the claim to stay open with no stored rows, status=%s", status)
	}
	if retried := send("Title,Done\r\nSame row,true\r\n", false); retried["isError"] == true {
		t.Fatalf("expected a full resend under the same claim to succeed: %#v", retried)
	}
}

func TestSlackListSnapshotRecordsReturnToEarlierStateAndDedupesReplays(t *testing.T) {
	homeDir := configureSlackListDriftBridge(t)
	now := time.Now().UTC().Add(-7 * time.Minute)
	ingest := func(at time.Time, done string) workgraph.BridgedIngestResult {
		t.Helper()
		requestID, token := claimSlackListSnapshot(t, homeDir, at)
		input := `[{"type":"slack.list_item","payload":{"list_id":"FDRIFT","fields":{"Title":"Round trip","Done":"` + done + `"}}}]`
		result, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{
			HomeDir: homeDir, RequestID: requestID, ClaimToken: token, Input: strings.NewReader(input),
		})
		if err != nil {
			t.Fatalf("ingest Slack List row: %v", err)
		}
		return result
	}
	open := ingest(now, "false")
	done := ingest(now.Add(time.Minute), "true")
	reopened := ingest(now.Add(2*time.Minute), "false")
	replay := ingest(now.Add(3*time.Minute), "false")
	doneAgain := ingest(now.Add(4*time.Minute), "true")
	reopenedAgain := ingest(now.Add(5*time.Minute), "false")
	replayAgain := ingest(now.Add(6*time.Minute), "false")
	if open.EventsInserted != 1 || done.EventsInserted != 1 || reopened.EventsInserted != 1 || replay.EventsDuplicate != 1 ||
		doneAgain.EventsInserted != 1 || reopenedAgain.EventsInserted != 1 || replayAgain.EventsDuplicate != 1 {
		t.Fatalf("expected every transition across two cycles stored and replays deduplicated: %#v %#v %#v %#v %#v %#v %#v",
			open, done, reopened, replay, doneAgain, reopenedAgain, replayAgain)
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	var latestDone bool
	if err := db.QueryRow(`SELECT json_extract(payload_json, '$.done') FROM events WHERE type = 'slack.list_item' ORDER BY timestamp DESC LIMIT 1`).Scan(&latestDone); err != nil {
		t.Fatalf("read latest revision: %v", err)
	}
	if latestDone || countSlackListEvents(t, homeDir) != 5 {
		t.Fatalf("expected five revisions ending open, latestDone=%v", latestDone)
	}
}

func TestSlackListSnapshotDedupesAlternatingRenderingsOfAnUnchangedItem(t *testing.T) {
	homeDir := configureSlackListDriftBridge(t)
	now := time.Now().UTC().Add(-6 * time.Minute)
	ingest := func(at time.Time, cycle string, done string) workgraph.BridgedIngestResult {
		t.Helper()
		requestID, token := claimSlackListSnapshot(t, homeDir, at)
		input := `[{"type":"slack.list_item","payload":{"list_id":"FDRIFT","fields":{"Title":"Review backlog","Cycle":"` + cycle + `","Done":"` + done + `","Related Message":"https://example.slack.com/archives/C0FLAP/p7"}}}]`
		result, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{
			HomeDir: homeDir, RequestID: requestID, ClaimToken: token, Input: strings.NewReader(input),
		})
		if err != nil {
			t.Fatalf("ingest Slack List row: %v", err)
		}
		return result
	}

	first := ingest(now, "Sprint 8", "true")
	unchanged := ingest(now.Add(time.Minute), "Sprint 8", "true")
	if first.EventsInserted != 1 || unchanged.EventsInserted != 0 || unchanged.EventsDuplicate != 1 {
		t.Fatalf("expected an unchanged re-capture to store nothing: first=%#v unchanged=%#v", first, unchanged)
	}

	changed := ingest(now.Add(2*time.Minute), "Sprint 7", "true")
	if changed.EventsInserted != 1 {
		t.Fatalf("expected a changed item to store one revision: %#v", changed)
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	rows, err := db.Query(`SELECT id, json_extract(payload_json, '$.list_id'), json_extract(payload_json, '$.row_key'), json_extract(payload_json, '$.fields.Cycle')
		FROM events WHERE type = 'slack.list_item' ORDER BY timestamp`)
	if err != nil {
		t.Fatalf("read Slack List revisions: %v", err)
	}
	type revision struct{ id, listID, rowKey, cycle string }
	var revisions []revision
	for rows.Next() {
		var stored revision
		if err := rows.Scan(&stored.id, &stored.listID, &stored.rowKey, &stored.cycle); err != nil {
			t.Fatalf("scan Slack List revision: %v", err)
		}
		revisions = append(revisions, stored)
	}
	rows.Close()
	wantRowKey := slackListRowKey(map[string]string{"Related Message": "https://example.slack.com/archives/C0FLAP/p7"})
	if len(revisions) != 2 || revisions[0].id == revisions[1].id || revisions[1].cycle != "Sprint 7" {
		t.Fatalf("expected the change stored as a second, distinct revision: %#v", revisions)
	}
	for _, stored := range revisions {
		if stored.listID != "FDRIFT" || stored.rowKey != wantRowKey {
			t.Fatalf("expected every revision to keep the item's List id and row key: %#v", revisions)
		}
	}

	flapBack := ingest(now.Add(3*time.Minute), "Sprint 8", "true")
	flapAgain := ingest(now.Add(4*time.Minute), "Sprint 7", "true")
	if flapBack.EventsInserted != 0 || flapBack.EventsDuplicate != 1 || flapAgain.EventsInserted != 0 || flapAgain.EventsDuplicate != 1 {
		t.Fatalf("expected re-captures alternating between already-stored renderings to deduplicate: back=%#v again=%#v", flapBack, flapAgain)
	}
	reopened := ingest(now.Add(5*time.Minute), "Sprint 8", "false")
	if reopened.EventsInserted != 1 {
		t.Fatalf("expected a state change to store a new revision: %#v", reopened)
	}
	if count := countSlackListEvents(t, homeDir); count != 3 {
		t.Fatalf("expected three stored revisions, got %d", count)
	}
}

func TestTodayHidesStoredSlackListRevisionsThatAlternateWithoutAStateChange(t *testing.T) {
	homeDir := configureSlackListDriftBridge(t)
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	db := openBridgedCaptureDatabase(t, homeDir)
	relatedMessage := "https://example.slack.com/archives/C0FLAP/p9"
	store := func(id string, at time.Time, cycle string, done bool) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"capture_semantics": "complete_snapshot",
			"content_hash":      "stored-" + id,
			"list_id":           "FDRIFT",
			"row_key":           slackListRowKey(map[string]string{"Related Message": relatedMessage}),
			"done":              done,
			"observed_at":       at.UTC().Format(time.RFC3339Nano),
			"fields":            map[string]any{"Title": "Review backlog", "Cycle": cycle, "Done": strconv.FormatBool(done), "Related Message": relatedMessage},
		})
		if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, summary, created_at)
			VALUES (?, 'slack', 'slack.list_item', ?, ?, 'slack-list:FDRIFT', 'Review backlog', ?)`,
			id, at.UTC().Format(time.RFC3339Nano), string(payload), at.UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("store Slack List revision: %v", err)
		}
	}
	store("render-a", dayStart.Add(1*time.Hour), "Sprint 8", true)
	store("render-b", dayStart.Add(2*time.Hour), "Sprint 7", true)
	store("render-a-again", dayStart.Add(3*time.Hour), "Sprint 8", true)
	store("render-b-again", dayStart.Add(4*time.Hour), "Sprint 7", true)
	store("reopened", dayStart.Add(5*time.Hour), "Sprint 8", false)

	today, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, Now: dayStart.Add(6 * time.Hour)})
	if err != nil {
		t.Fatalf("today: %v", err)
	}
	var listItems []string
	for _, event := range today.Events {
		if event.Type == "slack.list_item" {
			listItems = append(listItems, event.ID)
		}
	}
	if strings.Join(listItems, ",") != "render-a,render-b,reopened" {
		t.Fatalf("expected today to hide alternating repeats but keep the change and the state change, got %#v", listItems)
	}
}

func TestTodayAndResumeKeepAReopeningOrderedByCaptureTimeWhenTimestampsTie(t *testing.T) {
	homeDir := configureSlackListDriftBridge(t)
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	observedAt := dayStart.Add(time.Hour).UTC().Format(time.RFC3339Nano)
	db := openBridgedCaptureDatabase(t, homeDir)
	relatedMessage := "https://example.slack.com/archives/C0TIE/p3"
	store := func(id string, capturedAt time.Time, done bool) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"capture_semantics": "complete_snapshot",
			"content_hash":      "stored-" + id,
			"list_id":           "FDRIFT",
			"row_key":           slackListRowKey(map[string]string{"Related Message": relatedMessage}),
			"done":              done,
			"observed_at":       observedAt,
			"fields":            map[string]any{"Title": "Tied revision", "Done": strconv.FormatBool(done), "Related Message": relatedMessage},
		})
		if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, summary, created_at)
			VALUES (?, 'slack', 'slack.list_item', ?, ?, 'slack-list:FDRIFT', 'Tied revision', ?)`,
			id, observedAt, string(payload), capturedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("store Slack List revision: %v", err)
		}
	}
	store("tie-a-open", dayStart.Add(time.Hour+time.Minute), false)
	store("tie-c-done", dayStart.Add(time.Hour+2*time.Minute), true)
	store("tie-b-reopened", dayStart.Add(time.Hour+3*time.Minute), false)

	today, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, Now: dayStart.Add(2 * time.Hour)})
	if err != nil {
		t.Fatalf("today: %v", err)
	}
	todayIDs := map[string]bool{}
	for _, event := range today.Events {
		todayIDs[event.ID] = true
	}
	if !todayIDs["tie-a-open"] || !todayIDs["tie-c-done"] || !todayIDs["tie-b-reopened"] {
		t.Fatalf("expected today to keep open, done, and the reopening ordered by capture time, got %#v", todayIDs)
	}

	resume, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: homeDir, Project: "slack-list:FDRIFT", Now: dayStart.Add(2 * time.Hour)})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	resumeIDs := map[string]bool{}
	for _, event := range resume.Events {
		resumeIDs[event.ID] = true
	}
	if !resumeIDs["tie-a-open"] || !resumeIDs["tie-c-done"] || !resumeIDs["tie-b-reopened"] {
		t.Fatalf("expected resume to keep open, done, and the reopening ordered by capture time, got %#v", resumeIDs)
	}
}

func TestSlackListLatestRevisionUsesTimestampInstantNotText(t *testing.T) {
	homeDir := configureSlackListDriftBridge(t)
	db := openBridgedCaptureDatabase(t, homeDir)
	second := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	rowKey := slackListRowKey(map[string]string{"Title": "Precision row"})
	store := func(id string, timestamp string, priority string, done bool) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"capture_semantics": "complete_snapshot", "content_hash": id, "list_id": "FDRIFT", "row_key": rowKey,
			"done": done, "observed_at": timestamp,
			"fields": map[string]any{"Title": "Precision row", "Done": strconv.FormatBool(done), "Priority": priority},
		})
		if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, summary, created_at)
			VALUES (?, 'slack', 'slack.list_item', ?, ?, 'slack-list:FDRIFT', 'Precision row', ?)`, id, timestamp, string(payload), timestamp); err != nil {
			t.Fatalf("store revision: %v", err)
		}
	}
	base := second.Format("2006-01-02T15:04:05")
	store("whole-second", base+"Z", "Low", true)
	store("tenth", base+".1Z", "Medium", false)
	store("hundredth", base+".11Z", "High", false)

	requestID, token := claimSlackListSnapshot(t, homeDir, time.Now().UTC().Add(-time.Minute))
	input := `[{"type":"slack.list_item","payload":{"list_id":"FDRIFT","fields":{"Title":"Precision row","Done":"false","Priority":"High"}}}]`
	result, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{
		HomeDir: homeDir, RequestID: requestID, ClaimToken: token, Input: strings.NewReader(input),
	})
	if err != nil {
		t.Fatalf("ingest Slack List row: %v", err)
	}
	if result.EventsInserted != 0 || result.EventsDuplicate != 1 {
		t.Fatalf("expected the row to match the latest-by-instant revision (.11Z): %#v", result)
	}
}

func countSlackListChunks(t *testing.T, homeDir string) int {
	t.Helper()
	var count int
	if err := openBridgedCaptureDatabase(t, homeDir).QueryRow(`SELECT COUNT(*) FROM slack_list_snapshot_chunks`).Scan(&count); err != nil {
		t.Fatalf("count staged chunks: %v", err)
	}
	return count
}

func TestSlackListStagedChunksEndWithTheirClaim(t *testing.T) {
	homeDir := configureSlackListDriftBridge(t)
	stage := func(requestID string, token string) {
		t.Helper()
		staged := callBridgeMCPIngest(t, homeDir, map[string]any{
			"request_id": requestID, "claim_token": token, "list_id": "FDRIFT",
			"snapshot_csv": "Title,Done\r\nStaged row,false\r\n", "snapshot_continues": true,
		})
		if staged["isError"] == true || countSlackListChunks(t, homeDir) != 1 {
			t.Fatalf("stage chunk: %#v", staged)
		}
	}

	now := time.Now().UTC().Add(-5 * time.Minute)
	requestID, token := claimSlackListSnapshot(t, homeDir, now)
	stage(requestID, token)
	if err := workgraph.FailCaptureRequest(workgraph.CaptureRequestCapabilityConfig{
		HomeDir: homeDir, RequestID: requestID, ClaimToken: token, Error: "provider error", Now: now,
	}); err != nil {
		t.Fatalf("fail claim: %v", err)
	}
	if count := countSlackListChunks(t, homeDir); count != 0 {
		t.Fatalf("expected failure to discard staged chunks, got %d", count)
	}

	claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{
		HomeDir: homeDir, ConnectorID: "slack.lists", Worker: "facts", Max: 1, Now: now.Add(6 * time.Minute), Lease: 10 * time.Minute,
	})
	if err != nil || len(claims) != 1 {
		t.Fatalf("reclaim: claims=%d err=%v", len(claims), err)
	}
	stage(requestID, claims[0].ClaimToken)
	input := `[{"type":"slack.list_item","payload":{"list_id":"FDRIFT","fields":{"Title":"Compatibility row","Done":"false"}}}]`
	if _, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{
		HomeDir: homeDir, RequestID: requestID, ClaimToken: claims[0].ClaimToken, Input: strings.NewReader(input),
	}); err != nil {
		t.Fatalf("complete through compatibility path: %v", err)
	}
	if count := countSlackListChunks(t, homeDir); count != 0 {
		t.Fatalf("expected compatibility-path completion to discard staged chunks, got %d", count)
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	if _, err := db.Exec(`INSERT INTO slack_list_snapshot_chunks (request_id, claim_token, sequence, fragment, created_at)
		VALUES ('abandoned', 'gone', 0, 'Title', ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert abandoned chunk: %v", err)
	}
	if _, err := workgraph.ListCaptureRequests(workgraph.CaptureRequestListConfig{HomeDir: homeDir}); err != nil {
		t.Fatalf("open store: %v", err)
	}
	if count := countSlackListChunks(t, homeDir); count != 0 {
		t.Fatalf("expected store open to discard abandoned chunks, got %d", count)
	}
}
