package facts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
	_ "github.com/mattn/go-sqlite3"
)

func TestBridgedConnectorSetupNeedsNoProviderCredentials(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)

	output, err := runworkgraph(t, repoRoot, "connectors", "connect", "--home", homeDir, "--mode", "bridged",
		"--params-json", bridgeParamsForFact("slack"), "slack")
	if err != nil {
		t.Fatalf("connect bridged Slack: %v\n%s", err, output)
	}
	for _, expected := range []string{"slack", "bridged", "awaiting first ingest"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected setup output to contain %q, got:\n%s", expected, output)
		}
	}

	contents, err := os.ReadFile(filepath.Join(homeDir, "connectors.json"))
	if err != nil {
		t.Fatalf("read connectors config: %v", err)
	}
	var state struct {
		Connectors map[string]struct {
			Enabled     *bool  `json:"enabled"`
			CaptureMode string `json:"capture_mode"`
			SetupState  string `json:"setup_state"`
		} `json:"connectors"`
	}
	if err := json.Unmarshal(contents, &state); err != nil {
		t.Fatalf("parse connectors config: %v", err)
	}
	slack := state.Connectors["slack"]
	if slack.Enabled == nil || !*slack.Enabled || slack.CaptureMode != "bridged" || slack.SetupState != "ready" {
		t.Fatalf("expected enabled ready bridged Slack state, got %#v", slack)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "slack.json")); !os.IsNotExist(err) {
		t.Fatalf("expected bridged setup not to create Slack credentials, stat error %v", err)
	}

	for _, connector := range []string{"git", "notion"} {
		output, err = runworkgraph(t, repoRoot, "connectors", "mode", "--home", homeDir, connector, "bridged")
		if err == nil || !strings.Contains(string(output), connector+" only supports direct capture") {
			t.Fatalf("expected direct-only %s mode error, got err=%v:\n%s", connector, err, output)
		}
		output, err = runworkgraph(t, repoRoot, "connectors", "connect", "--home", homeDir, "--mode", "bridged", "--params-json", bridgeParamsForFact(connector), connector)
		if err == nil || !strings.Contains(string(output), connector+" only supports direct capture") {
			t.Fatalf("expected bridged %s connection to be rejected, got err=%v:\n%s", connector, err, output)
		}
	}
}

func TestBridgedConnectorRequiresScopeAndSurfacesItOnCLIClaim(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)

	output, err := runworkgraph(t, repoRoot, "connectors", "connect", "--home", homeDir, "--mode", "bridged", "slack")
	if err == nil {
		t.Fatalf("expected unscoped bridged Slack setup to fail, got:\n%s", output)
	}
	if !strings.Contains(string(output), "requires") || !strings.Contains(string(output), "channels") {
		t.Fatalf("expected actionable Slack scope error, got:\n%s", output)
	}

	params := `{"include_dms":false,"channels":["C0DEMO123"]}`
	output, err = runworkgraph(t, repoRoot, "connectors", "connect", "--home", homeDir, "--mode", "bridged", "--params-json", params, "slack")
	if err != nil {
		t.Fatalf("connect scoped bridged Slack: %v\n%s", err, output)
	}
	if output, err = runworkgraph(t, repoRoot, "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit scoped Slack request: %v\n%s", err, output)
	}

	claimFile := filepath.Join(t.TempDir(), "slack.claim.json")
	output, err = runworkgraph(t, repoRoot, "capture", "requests", "--claim", "--home", homeDir,
		"--connector", "slack", "--max", "1", "--worker", "facts", "--claim-file", claimFile)
	if err != nil {
		t.Fatalf("claim scoped Slack request: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `Params: {"channels":["C0DEMO123"],"include_dms":false}`) {
		t.Fatalf("claim omitted canonical non-secret parameters:\n%s", output)
	}
	claimContents, err := os.ReadFile(claimFile)
	if err != nil {
		t.Fatalf("read claim capability: %v", err)
	}
	if strings.Contains(string(claimContents), "channels") {
		t.Fatalf("claim capability should not duplicate scope: %s", claimContents)
	}
}

func TestSlackListBridgeDeclaresCompleteSnapshotSemantics(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	params := `{"lists":["F0DEMO123"],"done_column":"Done","row_key_candidates":[["Related Message"],["Title","Cycle"]]}`
	if output, err := runworkgraph(t, repoRoot, "connectors", "connect", "--home", homeDir,
		"--mode", "bridged", "--params-json", params, "slack.lists"); err != nil {
		t.Fatalf("connect bridged Slack Lists snapshot: %v\n%s", err, output)
	}
	emitted, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{
		HomeDir: homeDir, ConnectorID: "slack.lists", Now: time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("emit Slack Lists snapshot: %v", err)
	}
	if emitted.Request.CaptureSemantics != "complete_snapshot" {
		t.Fatalf("expected complete_snapshot request, got %#v", emitted.Request)
	}
	if !strings.Contains(string(emitted.Request.Params), `"done_column":"Done"`) ||
		!strings.Contains(string(emitted.Request.Params), `"row_key_candidates":[["Related Message"],["Title","Cycle"]]`) {
		t.Fatalf("snapshot request omitted normalization config: %s", emitted.Request.Params)
	}
	requests, err := workgraph.ListCaptureRequests(workgraph.CaptureRequestListConfig{HomeDir: homeDir, ConnectorID: "slack.lists"})
	if err != nil || len(requests) != 1 || requests[0].CaptureSemantics != "complete_snapshot" {
		t.Fatalf("listed snapshot semantics: requests=%#v error=%v", requests, err)
	}
}

func TestSlackListSnapshotNormalizesDoneAndContentHashRevisions(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	params := `{"lists":["F0DEMO123"],"done_column":"Done","row_key_candidates":[["Related Message"],["Title","Cycle"]]}`
	if output, err := runworkgraph(t, repoRoot, "connectors", "connect", "--home", homeDir,
		"--mode", "bridged", "--params-json", params, "slack.lists"); err != nil {
		t.Fatalf("connect bridged Slack Lists snapshot: %v\n%s", err, output)
	}
	now := time.Now().UTC().Add(-time.Minute)
	ingestSnapshot := func(at time.Time, done string) workgraph.BridgedIngestResult {
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
		input := `[{"type":"slack.list_item","summary":"Review the design","payload":{"list_id":"F0DEMO123","fields":{"Title":"Review the design","Cycle":"Today","Done":"` + done + `","Related Message":"https://example.slack.com/archives/C0DEMO/p1789"}}}]`
		result, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{
			HomeDir: homeDir, RequestID: emitted.Request.ID, ClaimToken: claims[0].ClaimToken, Input: strings.NewReader(input),
		})
		if err != nil {
			t.Fatalf("ingest Slack Lists snapshot: %v", err)
		}
		return result
	}

	first := ingestSnapshot(now, "FALSE")
	second := ingestSnapshot(now.Add(time.Minute), "FALSE")
	third := ingestSnapshot(now.Add(2*time.Minute), "TRUE")
	if first.EventsInserted != 1 || first.WeakDedupe != 0 || second.EventsDuplicate != 1 || third.EventsInserted != 1 {
		t.Fatalf("unexpected snapshot revisions: first=%#v second=%#v third=%#v", first, second, third)
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	rows, err := db.Query(`SELECT timestamp, payload_json FROM events WHERE type = 'slack.list_item' ORDER BY timestamp`)
	if err != nil {
		t.Fatalf("read Slack Lists snapshot events: %v", err)
	}
	defer rows.Close()
	var payloads []map[string]any
	var timestamps []string
	for rows.Next() {
		var timestamp, payload string
		if err := rows.Scan(&timestamp, &payload); err != nil {
			t.Fatalf("scan Slack Lists snapshot event: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
			t.Fatalf("decode Slack Lists snapshot payload: %v", err)
		}
		timestamps = append(timestamps, timestamp)
		payloads = append(payloads, decoded)
	}
	if len(payloads) != 2 || payloads[0]["done"] != false || payloads[1]["done"] != true {
		t.Fatalf("expected open and done revisions, got %#v", payloads)
	}
	for index, payload := range payloads {
		for _, key := range []string{"row_key", "content_hash", "observed_at", "capture_semantics"} {
			if strings.TrimSpace(payload[key].(string)) == "" {
				t.Fatalf("snapshot payload %d omitted %s: %#v", index, key, payload)
			}
		}
	}
	if timestamps[0] != now.Format(time.RFC3339Nano) || timestamps[1] != now.Add(2*time.Minute).Format(time.RFC3339Nano) {
		t.Fatalf("expected request observation timestamps, got %#v", timestamps)
	}
}

func TestSlackListSnapshotAppliesPerListInterpretationAndKeepsUnknownState(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	params := `{"lists":["FTEAM","FPERSONAL"],"done_column":"Done","list_options":{"FTEAM":{"state":{"column":"Status","done_values":["Complete","Done"]},"interest_columns":["Task","Priority"],"row_key_candidates":[["Task","Owner"]]},"FPERSONAL":{"interest_columns":["Title"],"row_key_candidates":[["Title"]]}}}`
	if _, err := workgraph.ConfigureBridgedConnector(workgraph.ConnectorBridgeConfig{
		HomeDir: homeDir, ID: "slack.lists", BridgeParams: json.RawMessage(params),
	}); err != nil {
		t.Fatalf("connect interpreted Slack Lists snapshot: %v", err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	emitted, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: "slack.lists", Now: now})
	if err != nil {
		t.Fatalf("emit snapshot: %v", err)
	}
	claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{HomeDir: homeDir, ConnectorID: "slack.lists", Worker: "facts", Max: 1, Now: now, Lease: 10 * time.Minute})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim snapshot: claims=%d error=%v", len(claims), err)
	}
	input := `[
		{"type":"slack.list_item","payload":{"list_id":"FTEAM","fields":{"Task":"Ship the bridge","Owner":"Craig","Status":"Complete","Priority":"High","Private Notes":"preserve me"}}},
		{"type":"slack.list_item","payload":{"list_id":"FPERSONAL","fields":{"Title":"Schedule design review","Done":"TRUE","Notes":"also preserve me"}}}
	]`
	result, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{HomeDir: homeDir, RequestID: emitted.Request.ID, ClaimToken: claims[0].ClaimToken, Input: strings.NewReader(input)})
	if err != nil {
		t.Fatalf("ingest interpreted snapshot: %v", err)
	}
	if result.EventsInserted != 2 {
		t.Fatalf("expected completed and unknown-state rows to remain captured, got %#v", result)
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	rows, err := db.Query(`SELECT payload_json FROM events WHERE type = 'slack.list_item' ORDER BY project`)
	if err != nil {
		t.Fatalf("read interpreted events: %v", err)
	}
	defer rows.Close()
	payloads := map[string]map[string]any{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan interpreted event: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("decode interpreted event: %v", err)
		}
		payloads[payload["list_id"].(string)] = payload
	}
	team := payloads["FTEAM"]
	if team["done"] != true {
		t.Fatalf("expected FTEAM configured state to be done, got %#v", team)
	}
	interest, ok := team["interest_fields"].(map[string]any)
	if !ok || interest["Task"] != "Ship the bridge" || interest["Priority"] != "High" || len(interest) != 2 {
		t.Fatalf("expected FTEAM interest projection, got %#v", team["interest_fields"])
	}
	if _, found := payloads["FPERSONAL"]["done"]; found {
		t.Fatalf("expected unconfigured FPERSONAL state to remain unknown, got %#v", payloads["FPERSONAL"])
	}
	if !strings.Contains(fmt.Sprint(team["fields"]), "preserve me") || !strings.Contains(fmt.Sprint(payloads["FPERSONAL"]["fields"]), "also preserve me") {
		t.Fatalf("expected complete raw fields, got %#v", payloads)
	}
}

func TestSlackListSnapshotRejectsAmbiguousOrOutOfScopeRowsAtomically(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	params := json.RawMessage(`{"lists":["F0DEMO123"],"row_key_candidates":[["Title"]]}`)
	if _, err := workgraph.ConfigureBridgedConnector(workgraph.ConnectorBridgeConfig{
		HomeDir: homeDir, ID: "slack.lists", BridgeParams: params,
	}); err != nil {
		t.Fatalf("connect Slack Lists snapshot: %v", err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	for name, input := range map[string]string{
		"duplicate key": `[{"type":"slack.list_item","payload":{"list_id":"F0DEMO123","fields":{"Title":"Same","Done":"false"}}},{"type":"slack.list_item","payload":{"list_id":"F0DEMO123","fields":{"Title":"Same","Done":"true"}}}]`,
		"out of scope":  `[{"type":"slack.list_item","payload":{"list_id":"F0OTHER","fields":{"Title":"Other","Done":"false"}}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			emitted, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: "slack.lists", Now: now})
			if err != nil {
				t.Fatalf("emit snapshot: %v", err)
			}
			claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{HomeDir: homeDir, ConnectorID: "slack.lists", Worker: "facts", Max: 1, Now: now, Lease: 10 * time.Minute})
			if err != nil || len(claims) != 1 {
				t.Fatalf("claim snapshot: claims=%d error=%v", len(claims), err)
			}
			_, err = workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{HomeDir: homeDir, RequestID: emitted.Request.ID, ClaimToken: claims[0].ClaimToken, Input: strings.NewReader(input)})
			if err == nil {
				t.Fatal("expected invalid snapshot to fail")
			}
			db := openBridgedCaptureDatabase(t, homeDir)
			var events int
			if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'slack.list_item'`).Scan(&events); err != nil {
				t.Fatalf("count snapshot events: %v", err)
			}
			if events != 0 {
				t.Fatalf("invalid snapshot stored %d partial event(s)", events)
			}
			if err := workgraph.FailCaptureRequest(workgraph.CaptureRequestCapabilityConfig{HomeDir: homeDir, RequestID: emitted.Request.ID, ClaimToken: claims[0].ClaimToken, Error: "invalid snapshot", Now: now.Add(time.Second)}); err != nil {
				t.Fatalf("release invalid snapshot request: %v", err)
			}
			now = now.Add(time.Minute)
		})
	}
}

func TestChangingBridgeScopeCancelsAndReplacesActiveRequest(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "slack")
	if output, err := runworkgraph(t, repoRoot, "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit original scoped request: %v\n%s", err, output)
	}

	replacement := `{"channels":["C0REPLACED"],"include_dms":false}`
	if output, err := runworkgraph(t, repoRoot, "connectors", "connect", "--home", homeDir, "--mode", "bridged",
		"--params-json", replacement, "slack"); err != nil {
		t.Fatalf("replace Slack scope: %v\n%s", err, output)
	} else if !strings.Contains(string(output), "cancelled after scope change") {
		t.Fatalf("scope replacement did not report cancellation:\n%s", output)
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	var cancelled int
	if err := db.QueryRow(`SELECT COUNT(*) FROM capture_requests WHERE connector_id = 'slack' AND status = 'cancelled'`).Scan(&cancelled); err != nil {
		t.Fatalf("count cancelled old-scope requests: %v", err)
	}
	if cancelled != 1 {
		t.Fatalf("expected old-scope request cancellation, got %d", cancelled)
	}
	if output, err := runworkgraph(t, repoRoot, "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit replacement scoped request: %v\n%s", err, output)
	}
	var params string
	if err := db.QueryRow(`SELECT params_json FROM capture_requests WHERE connector_id = 'slack' AND status = 'pending'`).Scan(&params); err != nil {
		t.Fatalf("read replacement request params: %v", err)
	}
	if params != replacement {
		t.Fatalf("expected replacement scope %s, got %s", replacement, params)
	}
}

func TestBridgedConnectorRejectsMalformedAndSecretScope(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)

	for _, params := range []string{`{"channels":`, `{"channels":["C0DEMO123"],"access_token":"secret"}`} {
		output, err := runworkgraph(t, repoRoot, "connectors", "connect", "--home", homeDir,
			"--mode", "bridged", "--params-json", params, "slack")
		if err == nil {
			t.Fatalf("expected params %q to fail, got:\n%s", params, output)
		}
	}
	contents, err := os.ReadFile(filepath.Join(homeDir, "connectors.json"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read connector state: %v", err)
	}
	if strings.Contains(string(contents), `"slack"`) {
		t.Fatalf("invalid scope changed connector state: %s", contents)
	}
}

func TestConnectorDoctorExplainsLegacyUnscopedBridge(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	if err := os.WriteFile(filepath.Join(homeDir, "connectors.json"), []byte(`{
  "connectors": {
    "azure.boards": {
      "enabled": true,
      "capture_mode": "bridged",
      "setup_state": "ready"
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write legacy bridge state: %v", err)
	}
	output, err := runworkgraph(t, repoRoot(t), "connectors", "doctor", "--home", homeDir)
	if err != nil {
		t.Fatalf("doctor legacy bridge: %v\n%s", err, output)
	}
	for _, expected := range []string{"azure.boards", "needs scope", "--params-json"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("connector doctor omitted %q:\n%s", expected, output)
		}
	}
	statusOutput, err := runworkgraph(t, repoRoot(t), "connectors", "status", "--home", homeDir)
	if err != nil {
		t.Fatalf("status legacy bridge: %v\n%s", err, statusOutput)
	}
	for _, expected := range []string{"azure.boards (bridged)", "setup needs scope", "polling not ready"} {
		if !strings.Contains(string(statusOutput), expected) {
			t.Fatalf("connector status omitted %q:\n%s", expected, statusOutput)
		}
	}
}

func TestConnectorStatusAndDoctorRejectLegacyBridgedNotion(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	if err := os.WriteFile(filepath.Join(homeDir, "connectors.json"), []byte(`{
  "connectors": {
    "notion": {
      "enabled": true,
      "capture_mode": "bridged",
      "setup_state": "ready",
      "bridge_params": {"roots":["demo-root"],"preview_limit":500}
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write legacy bridged Notion state: %v", err)
	}
	for _, command := range []string{"status", "doctor"} {
		output, err := runworkgraph(t, repoRoot(t), "connectors", command, "--home", homeDir)
		if err != nil {
			t.Fatalf("%s legacy bridged Notion: %v\n%s", command, err, output)
		}
		for _, expected := range []string{"notion", "unsupported bridge", "notion connect-token"} {
			if !strings.Contains(string(output), expected) {
				t.Fatalf("connector %s omitted %q:\n%s", command, expected, output)
			}
		}
	}
}

func TestBridgedCaptureSchemaIsDurable(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	db := openBridgedCaptureDatabase(t, homeDir)

	for _, table := range []string{"capture_requests", "capture_cursors"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("expected %s table: %v", table, err)
		}
	}

	requestColumns := bridgedTableColumns(t, db, "capture_requests")
	for _, column := range []string{
		"id", "connector_id", "since", "until", "params_json", "status", "attempts",
		"available_at", "last_error", "created_at", "claim_token", "claimed_by",
		"claimed_at", "lease_expires_at", "completed_at", "cancelled_at",
	} {
		if !requestColumns[column] {
			t.Fatalf("expected capture_requests.%s, got %#v", column, requestColumns)
		}
	}

	cursorColumns := bridgedTableColumns(t, db, "capture_cursors")
	for _, column := range []string{"connector_id", "completed_through", "updated_at"} {
		if !cursorColumns[column] {
			t.Fatalf("expected capture_cursors.%s, got %#v", column, cursorColumns)
		}
	}
}

func TestManualBridgedIngestDeduplicatesSlackEvents(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "slack")

	input := strings.Join([]string{
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:54.844Z","external_id":"C0DEMO123:1789300854.844369","project":"demo-production","actor":"U0DEMO001","summary":"Deployment succeeded for demo-service","payload":{"channel":"C0DEMO123","ts":"1789300854.844369","text":"Deployment succeeded"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:38.180Z","external_id":"C0DEMO123:1789300838.180579","project":"demo-production","actor":"U0DEMO001","summary":"demo-api modified","payload":{"channel":"C0DEMO123","ts":"1789300838.180579","text":"demo-api modified"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:37.767Z","external_id":"C0DEMO123:1789300837.767919","project":"demo-production","actor":"U0DEMO001","summary":"demo-worker modified","payload":{"channel":"C0DEMO123","ts":"1789300837.767919","text":"demo-worker modified"}}`,
	}, "\n") + "\n"

	output, err := runworkgraphInput(t, repoRoot, input, "capture", "ingest", "--home", homeDir, "--source", "slack", "--json", "-")
	if err != nil {
		t.Fatalf("first bridged ingest: %v\n%s", err, output)
	}
	for _, expected := range []string{"Events read: 3", "Events inserted: 3", "Events deduplicated: 0"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected first ingest output to contain %q, got:\n%s", expected, output)
		}
	}

	output, err = runworkgraphInput(t, repoRoot, input, "capture", "ingest", "--home", homeDir, "--source", "slack", "--json", "-")
	if err != nil {
		t.Fatalf("second bridged ingest: %v\n%s", err, output)
	}
	for _, expected := range []string{"Events read: 3", "Events inserted: 0", "Events deduplicated: 3"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected second ingest output to contain %q, got:\n%s", expected, output)
		}
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE source = 'slack' AND project = 'demo-production'`).Scan(&count); err != nil {
		t.Fatalf("count bridged Slack events: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected three bridged Slack events, got %d", count)
	}
	var timestamp string
	if err := db.QueryRow(`SELECT timestamp FROM events WHERE id = ?`, "34452d3e0dd72c3d120bd0399e0f69a9").Scan(&timestamp); err != nil {
		t.Fatalf("read deterministic bridged event: %v", err)
	}
	if timestamp != "2026-09-13T12:00:54.844Z" {
		t.Fatalf("expected normalized event timestamp, got %q", timestamp)
	}
}

func TestHandEditedBridgedNotionCannotScheduleOrIngest(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	if err := os.WriteFile(filepath.Join(homeDir, "connectors.json"), []byte(`{
  "connectors": {
    "notion": {
      "enabled": true,
      "capture_mode": "bridged",
      "setup_state": "ready",
      "bridge_params": {"roots":["demo-root"],"preview_limit":500}
    }
  }
}
`), 0o600); err != nil {
		t.Fatalf("write legacy bridged Notion state: %v", err)
	}

	output, err := runworkgraph(t, repoRoot, "connectors", "poll", "--home", homeDir, "--once", "--connector", "notion")
	if err == nil || !strings.Contains(string(output), "notion only supports direct capture") {
		t.Fatalf("expected direct-only Notion scheduler error, got err=%v:\n%s", err, output)
	}

	input := `[{"type":"notion.page","timestamp":"2026-06-23T04:58:22.726-07:00","external_id":"11111111111111111111111111111111:2026-06-23T11:58:22.726Z","project":"demo-documentation","actor":"alex@example.com","summary":"Local development authentication guide","payload":{"id":"11111111111111111111111111111111","url":"https://www.notion.so/11111111111111111111111111111111","title":"Local development authentication guide","path":"Engineering Home / Documentation","page_last_edited_at":"2026-06-23T11:58:22.726Z","last_edited_by":"alex@example.com","preview":"Bounded local development guidance."}}]`
	output, err = runworkgraphInput(t, repoRoot, input, "capture", "ingest", "--home", homeDir, "--source", "notion", "--json", "-")
	if err == nil || !strings.Contains(string(output), "notion only supports direct capture") {
		t.Fatalf("expected direct-only Notion ingest error, got err=%v:\n%s", err, output)
	}
}

func TestBridgedPollEmitsOneCoalescedRequestWithoutReportingSuccess(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "slack")

	output, err := runworkgraph(t, repoRoot, "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack")
	if err != nil {
		t.Fatalf("emit bridged request: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "pending") || strings.Contains(string(output), "last success") {
		t.Fatalf("expected pending request without capture success, got:\n%s", output)
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	var requestID, since, until, status string
	if err := db.QueryRow(`SELECT id, since, until, status FROM capture_requests WHERE connector_id = 'slack'`).Scan(&requestID, &since, &until, &status); err != nil {
		t.Fatalf("read emitted request: %v", err)
	}
	if requestID == "" || status != "pending" {
		t.Fatalf("expected pending request, got id=%q status=%q", requestID, status)
	}
	sinceTime, err := time.Parse(time.RFC3339Nano, since)
	if err != nil {
		t.Fatalf("parse request since: %v", err)
	}
	untilTime, err := time.Parse(time.RFC3339Nano, until)
	if err != nil {
		t.Fatalf("parse request until: %v", err)
	}
	if window := untilTime.Sub(sinceTime); window < 23*time.Hour+59*time.Minute || window > 24*time.Hour+time.Minute {
		t.Fatalf("expected initial 24 hour window, got %s", window)
	}

	if output, err := runworkgraph(t, repoRoot, "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("coalesce bridged request: %v\n%s", err, output)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM capture_requests WHERE connector_id = 'slack'`).Scan(&count); err != nil {
		t.Fatalf("count coalesced requests: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one coalesced request, got %d", count)
	}

	var lastSuccess string
	contents, err := os.ReadFile(filepath.Join(homeDir, "connectors.json"))
	if err != nil {
		t.Fatalf("read connector state: %v", err)
	}
	var state struct {
		Connectors map[string]struct {
			LastSuccess string `json:"last_success_at"`
		} `json:"connectors"`
	}
	if err := json.Unmarshal(contents, &state); err != nil {
		t.Fatalf("parse connector state: %v", err)
	}
	lastSuccess = state.Connectors["slack"].LastSuccess
	if lastSuccess != "" {
		t.Fatalf("expected emission not to record success, got %q", lastSuccess)
	}
}

func TestClaimAndEmptyIngestCompletesRequestAndAdvancesControlCursor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("claim-file permission assertion is Unix-specific")
	}
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "calendar.microsoft")
	if output, err := runworkgraph(t, repoRoot, "connectors", "poll", "--home", homeDir, "--once", "--connector", "calendar.microsoft"); err != nil {
		t.Fatalf("emit calendar request: %v\n%s", err, output)
	}

	claimFile := filepath.Join(t.TempDir(), "calendar.claim.json")
	output, err := runworkgraph(t, repoRoot, "capture", "requests", "--claim", "--home", homeDir,
		"--connector", "calendar.microsoft", "--max", "1", "--worker", "facts", "--claim-file", claimFile)
	if err != nil {
		t.Fatalf("claim request: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "claim_token") {
		t.Fatalf("expected claim token to remain out of command output, got:\n%s", output)
	}
	info, err := os.Stat(claimFile)
	if err != nil {
		t.Fatalf("stat claim file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected claim file mode 0600, got %o", info.Mode().Perm())
	}
	var claim struct {
		RequestID string `json:"request_id"`
		Token     string `json:"claim_token"`
	}
	contents, err := os.ReadFile(claimFile)
	if err != nil {
		t.Fatalf("read claim file: %v", err)
	}
	if err := json.Unmarshal(contents, &claim); err != nil {
		t.Fatalf("parse claim file: %v", err)
	}
	if claim.RequestID == "" || claim.Token == "" {
		t.Fatalf("expected request id and token in claim file, got %#v", claim)
	}
	statusOutput, err := runworkgraph(t, repoRoot, "connectors", "status", "--home", homeDir)
	if err != nil {
		t.Fatalf("read claimed connector status: %v\n%s", err, statusOutput)
	}
	for _, expected := range []string{"calendar.microsoft (bridged)", "capture claimed", "worker facts", "lease expires"} {
		if !strings.Contains(string(statusOutput), expected) {
			t.Fatalf("claimed connector status omitted %q:\n%s", expected, statusOutput)
		}
	}
	if strings.Contains(string(statusOutput), claim.Token) || strings.Contains(string(statusOutput), "claim_token") {
		t.Fatalf("connector status exposed claim token:\n%s", statusOutput)
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	var requestUntil string
	if err := db.QueryRow(`SELECT until FROM capture_requests WHERE id = ?`, claim.RequestID).Scan(&requestUntil); err != nil {
		t.Fatalf("read request bound: %v", err)
	}
	output, err = runworkgraphInput(t, repoRoot, "[]", "capture", "ingest", "--home", homeDir,
		"--request", claim.RequestID, "--claim-file", claimFile, "--json", "-")
	if err != nil {
		t.Fatalf("complete empty capture: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Events read: 0") || !strings.Contains(string(output), "Request completed") {
		t.Fatalf("expected successful empty completion, got:\n%s", output)
	}

	var requestStatus, completedThrough string
	if err := db.QueryRow(`SELECT status FROM capture_requests WHERE id = ?`, claim.RequestID).Scan(&requestStatus); err != nil {
		t.Fatalf("read completed request: %v", err)
	}
	if err := db.QueryRow(`SELECT completed_through FROM capture_cursors WHERE connector_id = 'calendar.microsoft'`).Scan(&completedThrough); err != nil {
		t.Fatalf("read completed cursor: %v", err)
	}
	if requestStatus != "completed" || completedThrough != requestUntil {
		t.Fatalf("expected completed request and cursor %q, got status=%q cursor=%q", requestUntil, requestStatus, completedThrough)
	}
}

func TestDaemonSchedulesBridgedConnectorWithoutCallingProvider(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("initialize workgraph: %v", err)
	}
	providerCalled := make(chan struct{}, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		select {
		case providerCalled <- struct{}{}:
		default:
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":true,"messages":[],"response_metadata":{}}`))
	}))
	defer provider.Close()
	if err := os.WriteFile(filepath.Join(homeDir, "slack.json"), []byte(`{
  "access_token": "preserved-direct-token",
  "channels": ["C0DEMO123"],
  "user_scopes": [],
  "api_base_url": "`+provider.URL+`"
}
`), 0o600); err != nil {
		t.Fatalf("write preserved direct credentials: %v", err)
	}
	if _, err := workgraph.ConfigureBridgedConnector(workgraph.ConnectorBridgeConfig{
		HomeDir: homeDir, ID: "slack", BridgeParams: json.RawMessage(bridgeParamsForFact("slack")),
	}); err != nil {
		t.Fatalf("connect bridged Slack: %v", err)
	}

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		WatchDirs:    []string{tempDir},
	})
	if err != nil {
		t.Fatalf("start capture daemon: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- capture.Run(ctx) }()

	db := openBridgedCaptureDatabase(t, homeDir)
	deadline := time.Now().Add(2 * time.Second)
	for {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM capture_requests WHERE connector_id = 'slack' AND status = 'pending'`).Scan(&count)
		if err == nil && count == 1 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("daemon did not emit bridged Slack request: count=%d error=%v", count, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("stop capture daemon: %v", err)
	}
	select {
	case <-providerCalled:
		t.Fatal("workgraph called the Slack provider for a bridged connector")
	default:
	}
}

func TestExpiredClaimRetriesSameRequestAndInvalidatesOldToken(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	start := time.Now().UTC().Add(-time.Hour)
	emitted, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{
		HomeDir: homeDir, ConnectorID: "slack", Now: start,
	})
	if err != nil {
		t.Fatalf("emit capture request: %v", err)
	}
	first, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{
		HomeDir: homeDir, ConnectorID: "slack", Worker: "first", Max: 1, Now: start, Lease: time.Minute,
	})
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim: claims=%d error=%v", len(first), err)
	}

	recycledAt := start.Add(2 * time.Minute)
	claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{
		HomeDir: homeDir, ConnectorID: "slack", Worker: "second", Max: 1, Now: recycledAt,
	})
	if err != nil {
		t.Fatalf("recycle expired claim: %v", err)
	}
	if len(claims) != 0 {
		t.Fatalf("expected persisted retry backoff before reclaim, got %d claim(s)", len(claims))
	}
	second, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{
		HomeDir: homeDir, ConnectorID: "slack", Worker: "second", Max: 1, Now: recycledAt.Add(6 * time.Second),
	})
	if err != nil || len(second) != 1 {
		t.Fatalf("second claim after backoff: claims=%d error=%v", len(second), err)
	}
	if second[0].Request.ID != emitted.Request.ID || second[0].Request.Since != emitted.Request.Since || second[0].Request.Until != emitted.Request.Until {
		t.Fatalf("expected retry to preserve request and bounds, first=%#v second=%#v", emitted.Request, second[0].Request)
	}
	if second[0].ClaimToken == first[0].ClaimToken || second[0].Request.Attempts != 2 {
		t.Fatalf("expected a new token and second attempt, first=%#v second=%#v", first[0], second[0])
	}
	if _, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{
		HomeDir: homeDir, RequestID: emitted.Request.ID, ClaimToken: first[0].ClaimToken, Input: strings.NewReader("[]"),
	}); err == nil || !strings.Contains(err.Error(), "stale or invalid") {
		t.Fatalf("expected old claim token rejection, got %v", err)
	}
}

func TestSelectingDirectModeCancelsOrphanedBridgedRequest(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "slack")
	if output, err := runworkgraph(t, repoRoot, "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit orphan candidate: %v\n%s", err, output)
	}

	path := filepath.Join(homeDir, "connectors.json")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read connector state: %v", err)
	}
	contents = []byte(strings.Replace(string(contents), `"capture_mode": "bridged"`, `"capture_mode": "direct"`, 1))
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("simulate stale direct state: %v", err)
	}
	if output, err := runworkgraph(t, repoRoot, "connectors", "mode", "--home", homeDir, "slack", "direct"); err != nil {
		t.Fatalf("reselect direct mode: %v\n%s", err, output)
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	var status string
	if err := db.QueryRow(`SELECT status FROM capture_requests WHERE connector_id = 'slack'`).Scan(&status); err != nil {
		t.Fatalf("read orphaned request: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("expected orphaned request cancellation, got %q", status)
	}
}

func TestBridgeRenewsLeaseAndReportsRetryableFailureAfterExpiry(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	connectBridgedConnector(t, repoRoot(t), homeDir, "github")
	start := time.Now().UTC()
	emitted, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: "github", Now: start})
	if err != nil {
		t.Fatalf("emit request: %v", err)
	}
	claimed, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{
		HomeDir: homeDir, ConnectorID: "github", Worker: "facts", Max: 1, Now: start, Lease: time.Minute,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim request: claims=%d error=%v", len(claimed), err)
	}
	renewed, err := workgraph.RenewCaptureRequest(workgraph.CaptureRequestCapabilityConfig{
		HomeDir: homeDir, RequestID: emitted.Request.ID, ClaimToken: claimed[0].ClaimToken, Now: start.Add(30 * time.Second), Lease: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("renew request: %v", err)
	}
	if renewed.LeaseExpiresAt != start.Add(5*time.Minute+30*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("expected renewed lease, got %q", renewed.LeaseExpiresAt)
	}
	if err := workgraph.FailCaptureRequest(workgraph.CaptureRequestCapabilityConfig{
		HomeDir: homeDir, RequestID: emitted.Request.ID, ClaimToken: claimed[0].ClaimToken,
		Error: "snapshot serialization exceeded the lease", Now: start.Add(6 * time.Minute),
	}); err != nil {
		t.Fatalf("report request failure after lease expiry: %v", err)
	}
	requests, err := workgraph.ListCaptureRequests(workgraph.CaptureRequestListConfig{HomeDir: homeDir, ConnectorID: "github"})
	if err != nil || len(requests) != 1 {
		t.Fatalf("list retried request: requests=%d error=%v", len(requests), err)
	}
	if requests[0].Status != "pending" || requests[0].AvailableAt <= start.Add(6*time.Minute).Format(time.RFC3339Nano) {
		t.Fatalf("expected pending request after persisted backoff, got %#v", requests[0])
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	var lastError string
	if err := db.QueryRow(`SELECT last_error FROM capture_requests WHERE id = ?`, emitted.Request.ID).Scan(&lastError); err != nil {
		t.Fatalf("read retained expired-claim failure: %v", err)
	}
	if lastError != "snapshot serialization exceeded the lease" {
		t.Fatalf("expected expired claim failure to be retained, got %q", lastError)
	}
	if err := workgraph.FailCaptureRequest(workgraph.CaptureRequestCapabilityConfig{
		HomeDir: homeDir, RequestID: emitted.Request.ID, ClaimToken: claimed[0].ClaimToken,
		Error: "late duplicate failure", Now: start.Add(7 * time.Minute),
	}); err == nil || !strings.Contains(err.Error(), "stale or invalid") {
		t.Fatalf("expected released claim token to remain invalid, got %v", err)
	}
}

func initBridgedCaptureHome(t *testing.T) string {
	t.Helper()
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	if output, err := runworkgraph(t, repoRoot(t), "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	// Keep any client-global configuration written by plugin facts inside the
	// same disposable user home as the synthetic workgraph home.
	t.Setenv("HOME", filepath.Dir(homeDir))
	return homeDir
}

func connectBridgedConnector(t *testing.T, repoRoot string, homeDir string, connector string) {
	t.Helper()
	if output, err := runworkgraph(t, repoRoot, "connectors", "connect", "--home", homeDir, "--mode", "bridged",
		"--params-json", bridgeParamsForFact(connector), connector); err != nil {
		t.Fatalf("connect bridged %s: %v\n%s", connector, err, output)
	}
}

func bridgeParamsForFact(connector string) string {
	switch connector {
	case "github":
		return `{"repositories":["demo/repository"]}`
	case "slack":
		return `{"channels":["C0DEMO123"],"include_dms":false}`
	case "slack.lists":
		return `{"lists":["F0DEMO123"]}`
	case "mail.google", "mail.microsoft":
		return `{"mailboxes":["inbox"],"preview_limit":500}`
	case "calendar.google", "calendar.microsoft":
		return `{"calendars":["primary"],"future_days":30,"past_days":7}`
	case "azure.boards":
		return `{"area_path":"Demo","organization":"example-org","project":"Demo"}`
	default:
		return `{}`
	}
}

func runworkgraphInput(t *testing.T, repoRoot string, input string, args ...string) ([]byte, error) {
	t.Helper()
	command := exec.Command(workgraphFactsBinary, args...)
	command.Dir = repoRoot
	command.Stdin = strings.NewReader(input)
	return command.CombinedOutput()
}

func openBridgedCaptureDatabase(t *testing.T, homeDir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(homeDir, "workgraph.db"))
	if err != nil {
		t.Fatalf("open bridged capture database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func bridgedTableColumns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("read %s columns: %v", table, err)
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan %s column: %v", table, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s columns: %v", table, err)
	}
	return columns
}
