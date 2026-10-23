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
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
	_ "github.com/mattn/go-sqlite3"
)

func TestMicrosoftMailRecipeUsesPaddedLocalWindowAndExactBounds(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load mailbox timezone: %v", err)
	}
	since := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	until := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	window, err := workgraph.MicrosoftMailRecipeWindow(since, until, location)
	if err != nil {
		t.Fatalf("build Microsoft mail recipe window: %v", err)
	}
	if !window.Since.Equal(time.Date(2026, 9, 8, 8, 0, 0, 0, location)) ||
		!window.Until.Equal(time.Date(2026, 9, 13, 8, 0, 0, 0, location)) {
		t.Fatalf("expected mailbox-local two-day padding, got %#v", window)
	}

	messages, err := workgraph.NormalizeMicrosoftMailRecipe([]workgraph.MicrosoftMailRecipePage{
		{
			URL:     "messages?$skip=0",
			NextURL: "messages?$skip=2",
			Messages: []workgraph.MicrosoftMailRecipeMessage{
				{ID: "after-window", ReceivedDateTime: "2026-09-11T12:00:01Z"},
				{ID: "inside-one", ReceivedDateTime: "2026-09-11T11:59:59Z"},
			},
		},
		{
			URL:      "messages?$skip=2",
			Complete: true,
			Messages: []workgraph.MicrosoftMailRecipeMessage{
				{ID: "inside-two", ReceivedDateTime: "2026-09-10T12:00:00Z"},
				{ID: "before-window", ReceivedDateTime: "2026-09-10T11:59:59Z"},
			},
		},
	}, since, until)
	if err != nil {
		t.Fatalf("normalize Microsoft mail recipe: %v", err)
	}
	if len(messages) != 2 || messages[0].ID != "inside-one" || messages[1].ID != "inside-two" {
		t.Fatalf("expected exact UTC-bound filtering, got %#v", messages)
	}
}

func TestMicrosoftMailRecipeRejectsNonContiguousOrTruncatedPages(t *testing.T) {
	since := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	until := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		pages []workgraph.MicrosoftMailRecipePage
	}{
		{
			name: "non-contiguous offset",
			pages: []workgraph.MicrosoftMailRecipePage{
				{URL: "messages?$skip=0", NextURL: "messages?$skip=2"},
				{URL: "messages?$skip=3", Complete: true},
			},
		},
		{
			name: "truncated terminal page",
			pages: []workgraph.MicrosoftMailRecipePage{
				{URL: "messages?$skip=0", Truncated: true},
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := workgraph.NormalizeMicrosoftMailRecipe(test.pages, since, until); err == nil {
				t.Fatalf("expected recipe to reject %s", test.name)
			}
		})
	}
}

func TestBridgedEmptyResultRequiresExhaustiveOrControlProof(t *testing.T) {
	if err := workgraph.ValidateBridgedEmptyProof(workgraph.BridgedEmptyProof{}); err == nil {
		t.Fatal("expected unproven empty result to be rejected")
	}
	for _, proof := range []workgraph.BridgedEmptyProof{
		{ExhaustiveQuery: true},
		{ControlQueryPassed: true},
	} {
		if err := workgraph.ValidateBridgedEmptyProof(proof); err != nil {
			t.Fatalf("expected proven empty result to pass: %v", err)
		}
	}
}

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

func TestNotionActivityIsBridgedOnlyAndRejectsDirectMode(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)

	output, err := runworkgraph(t, repoRoot, "connectors", "mode", "--home", homeDir, "notion.activity", "direct")
	if err == nil || !strings.Contains(string(output), "notion.activity only supports bridged capture") {
		t.Fatalf("expected notion.activity to reject direct mode, got err=%v:\n%s", err, output)
	}

	connectBridgedConnector(t, repoRoot, homeDir, "notion.activity")
	contents, err := os.ReadFile(filepath.Join(homeDir, "connectors.json"))
	if err != nil {
		t.Fatalf("read connectors config: %v", err)
	}
	if !strings.Contains(string(contents), `"capture_mode": "bridged"`) {
		t.Fatalf("expected notion.activity to be configured bridged, got:\n%s", contents)
	}
}

func TestNotionActivityBridgedIngestDedupesAgainstDirectNotionIdentity(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "notion.activity")

	now := time.Now().UTC().Add(-time.Minute)
	emitted, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: "notion.activity", Now: now})
	if err != nil {
		t.Fatalf("emit notion.activity request: %v", err)
	}
	if emitted.Request.Source != "notion" {
		t.Fatalf("expected notion.activity events to share the notion source, got %#v", emitted.Request)
	}
	claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{HomeDir: homeDir, ConnectorID: "notion.activity", Worker: "facts", Max: 1, Now: now, Lease: 10 * time.Minute})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim notion.activity request: claims=%d error=%v", len(claims), err)
	}
	input := `[{"type":"notion.page_updated","timestamp":"2026-06-07T16:30:00Z","external_id":"page-1:2026-06-07T16:30:00Z","summary":"Updated launch plan","payload":{"object":"page","id":"page-1"}}]`
	result, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{HomeDir: homeDir, RequestID: emitted.Request.ID, ClaimToken: claims[0].ClaimToken, Input: strings.NewReader(input)})
	if err != nil {
		t.Fatalf("ingest notion.activity: %v", err)
	}
	if result.EventsInserted != 1 {
		t.Fatalf("expected one inserted event, got %#v", result)
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	var count int
	var involvement string
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(involvement_json), '') FROM events WHERE id = 'notion.page_updated:page-1:2026-06-07T16:30:00Z'`).Scan(&count, &involvement); err != nil {
		t.Fatalf("count notion event: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected bridged event id to match the direct notion connector's identity scheme, got %d rows", count)
	}
	if involvement != `["edited"]` {
		t.Fatalf("expected notion.activity bridge to preserve personal edit involvement, got %q", involvement)
	}

	second, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{HomeDir: homeDir, Source: "notion.activity", Input: strings.NewReader(input)})
	if err != nil {
		t.Fatalf("re-ingest notion.activity: %v", err)
	}
	if second.EventsDuplicate != 1 || second.EventsInserted != 0 {
		t.Fatalf("expected the repeated edit to de-duplicate, got %#v", second)
	}
}

func TestBridgedCaptureNormalizesAndValidatesInvolvement(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "slack")

	input := `[{"type":"slack.message","timestamp":"2026-06-07T16:30:00Z","external_id":"message-1","involvement":["mentioned","authored","mentioned"],"payload":{"channel_id":"C0DEMO123","channel_name":"demo-changes","conversation_type":"channel"}}]`
	result, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{
		HomeDir: homeDir,
		Source:  "slack",
		Input:   strings.NewReader(input),
	})
	if err != nil {
		t.Fatalf("ingest event with involvement: %v", err)
	}
	if result.EventsInserted != 1 {
		t.Fatalf("expected one inserted event, got %#v", result)
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	var stored string
	if err := db.QueryRow(`SELECT involvement_json FROM events WHERE type = 'slack.message'`).Scan(&stored); err != nil {
		t.Fatalf("read stored involvement: %v", err)
	}
	if stored != `["authored","mentioned"]` {
		t.Fatalf("expected canonical involvement, got %s", stored)
	}

	invalid := `[{"type":"slack.message","timestamp":"2026-06-07T16:31:00Z","external_id":"message-2","involvement":["nearby"],"payload":{"channel_id":"C0DEMO123","channel_name":"demo-changes","conversation_type":"channel"}}]`
	if _, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{
		HomeDir: homeDir,
		Source:  "slack",
		Input:   strings.NewReader(invalid),
	}); err == nil || !strings.Contains(err.Error(), `unknown involvement "nearby"`) {
		t.Fatalf("expected unknown involvement to be rejected, got %v", err)
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

func TestExactCaptureRequestCancellationPreservesAuditAndInvalidatesClaim(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "slack")
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	emitted, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{
		HomeDir: homeDir, ConnectorID: "slack", Now: now,
	})
	if err != nil {
		t.Fatalf("emit request: %v", err)
	}
	claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{
		HomeDir: homeDir, ConnectorID: "slack", Worker: "facts", Max: 1, Now: now,
	})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim request: claims=%d error=%v", len(claims), err)
	}
	claimed := claims[0]

	output, err := runworkgraph(t, repoRoot, "capture", "requests", "--cancel", emitted.Request.ID, "--reason", "operator requested cancellation", "--home", homeDir)
	if err != nil || !strings.Contains(string(output), "Capture request cancelled") {
		t.Fatalf("cancel request: %v", err)
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	var status, cancelledAt, claimedBy, claimedAt, leaseExpiresAt, lastError string
	if err := db.QueryRow(`SELECT status, COALESCE(cancelled_at, ''), COALESCE(claimed_by, ''), COALESCE(claimed_at, ''), COALESCE(lease_expires_at, ''), COALESCE(last_error, '') FROM capture_requests WHERE id = ?`, emitted.Request.ID).
		Scan(&status, &cancelledAt, &claimedBy, &claimedAt, &leaseExpiresAt, &lastError); err != nil {
		t.Fatalf("read cancelled request: %v", err)
	}
	if status != "cancelled" || cancelledAt == "" || claimedBy != "facts" || claimedAt == "" || leaseExpiresAt != "" || lastError != "operator requested cancellation" {
		t.Fatalf("unexpected cancelled audit state: status=%q cancelled_at=%q claimed_by=%q claimed_at=%q lease=%q last_error=%q", status, cancelledAt, claimedBy, claimedAt, leaseExpiresAt, lastError)
	}
	if _, err := workgraph.RenewCaptureRequest(workgraph.CaptureRequestCapabilityConfig{
		HomeDir: homeDir, RequestID: claimed.Request.ID, ClaimToken: claimed.ClaimToken, Now: now.Add(2 * time.Minute),
	}); err == nil {
		t.Fatal("expected cancelled claim token to be invalid")
	}
	if err := workgraph.CancelCaptureRequest(workgraph.CaptureRequestCancelConfig{
		HomeDir: homeDir, RequestID: emitted.Request.ID, Now: now.Add(2 * time.Minute), Reason: "repeat cancellation",
	}); err != nil {
		t.Fatalf("repeat cancellation should be idempotent: %v", err)
	}
	var repeatReason string
	if err := db.QueryRow(`SELECT COALESCE(last_error, '') FROM capture_requests WHERE id = ?`, emitted.Request.ID).Scan(&repeatReason); err != nil {
		t.Fatalf("read repeat cancellation: %v", err)
	}
	if repeatReason != "operator requested cancellation" {
		t.Fatalf("repeat cancellation overwrote audit reason with %q", repeatReason)
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
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:54.844Z","external_id":"C0DEMO123:1789300854.844369","project":"demo-production","actor":"U0DEMO001","summary":"Deployment succeeded for demo-service","payload":{"channel_id":"C0DEMO123","channel_name":"demo-changes","conversation_type":"channel","ts":"1789300854.844369","text":"Deployment succeeded"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:38.180Z","external_id":"C0DEMO123:1789300838.180579","project":"demo-production","actor":"U0DEMO001","summary":"demo-api modified","payload":{"channel_id":"C0DEMO123","channel_name":"demo-changes","conversation_type":"channel","ts":"1789300838.180579","text":"demo-api modified"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:37.767Z","external_id":"C0DEMO123:1789300837.767919","project":"demo-production","actor":"U0DEMO001","summary":"demo-worker modified","payload":{"channel_id":"C0DEMO123","channel_name":"demo-changes","conversation_type":"channel","ts":"1789300837.767919","text":"demo-worker modified"}}`,
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

func TestBridgedSlackIngestKeepsConversationTypeForPrivacy(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	t.Setenv("HOME", filepath.Dir(homeDir))
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
	connectBridgedConnector(t, repoRoot, homeDir, "slack")

	input := strings.Join([]string{
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:00Z","external_id":"C0DEMO123:1789300800.000100","payload":{"channel_id":"C0DEMO123","channel_name":"demo-public","conversation_type":"channel","ts":"1789300800.000100","text":"Public channel"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:01Z","external_id":"G0DEMO123:1789300801.000100","payload":{"channel_id":"G0DEMO123","channel_name":"demo-private","conversation_type":"private_channel","ts":"1789300801.000100","text":"Private channel"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:02Z","external_id":"D0DEMO123:1789300802.000100","payload":{"channel_id":"D0DEMO123","channel_name":"DM: Lila","conversation_type":"im","ts":"1789300802.000100","text":"Direct message"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:03Z","external_id":"G0DEMO456:1789300803.000100","payload":{"channel_id":"G0DEMO456","channel_name":"Group DM: Lila, Omar","conversation_type":"mpim","ts":"1789300803.000100","text":"Group message"}}`,
	}, "\n") + "\n"
	if output, err := runworkgraphInput(t, repoRoot, input, "capture", "ingest", "--home", homeDir, "--source", "slack", "--json", "-"); err != nil {
		t.Fatalf("bridged ingest: %v\n%s", err, output)
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	for channel, expected := range map[string]string{"C0DEMO123": "slack", "G0DEMO123": "slack", "D0DEMO123": "slack.dm", "G0DEMO456": "slack.dm"} {
		var payloadJSON string
		if err := db.QueryRow(`SELECT payload_json FROM events WHERE source = 'slack' AND json_extract(payload_json, '$.channel_id') = ?`, channel).Scan(&payloadJSON); err != nil {
			t.Fatalf("read bridged Slack event %s: %v", channel, err)
		}
		if got := workgraph.SlackPrivacySource(payloadJSON); got != expected {
			t.Fatalf("expected %s privacy source %q, got %q for %s", channel, expected, got, payloadJSON)
		}
	}
	if got := workgraph.SlackPrivacySource(`{"channel_id":"C0DEMO456","channel_name":"demo-legacy","ts":"1789300804.000100"}`); got != "slack.dm" {
		t.Fatalf("expected a stored event without conversation_type to stay a DM, got %q", got)
	}
}

func TestBridgedSlackIngestEnforcesChannelContract(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "slack")

	named := `{"type":"slack.message","timestamp":"2026-09-13T12:00:00Z","external_id":"C0DEMO001:1789300800.000100","payload":{"channel_id":"C0DEMO001","channel_name":"squad-demo","conversation_type":"channel","ts":"1789300800.000100","text":"Named"}}`
	rejected := []struct {
		name    string
		event   string
		message []string
	}{
		{"missing channel_id", `{"type":"slack.message","timestamp":"2026-09-13T12:00:01Z","external_id":"C0DEMO002:1789300801.000100","payload":{"channel":"C0DEMO002","channel_name":"guild-demo","conversation_type":"channel","ts":"1789300801.000100","text":"Legacy channel key"}}`, []string{"event 2", "channel_id is required"}},
		{"missing conversation_type", `{"type":"slack.message","timestamp":"2026-09-13T12:00:01Z","external_id":"C0DEMO002:1789300801.000100","payload":{"channel_id":"C0DEMO002","channel_name":"guild-demo","ts":"1789300801.000100","text":"No type"}}`, []string{"event 2", "conversation_type is required", "channel, private_channel, im, or mpim"}},
		{"unrecognized conversation_type", `{"type":"slack.thread_reply","timestamp":"2026-09-13T12:00:01Z","external_id":"C0DEMO002:1789300801.000100","payload":{"channel_id":"C0DEMO002","channel_name":"guild-demo","conversation_type":"public","ts":"1789300801.000100","thread_ts":"1789300800.000100","text":"Odd type"}}`, []string{"event 2", "conversation_type is required", `"public"`}},
		{"missing channel_name", `{"type":"slack.reply","timestamp":"2026-09-13T12:00:01Z","external_id":"C0DEMO002:1789300801.000100","payload":{"channel_id":"C0DEMO002","conversation_type":"channel","ts":"1789300801.000100","thread_ts":"1789300800.000100","text":"No name"}}`, []string{"event 2", "channel_name is required", "C0DEMO002", "conversation info"}},
		{"raw id channel_name", `{"type":"slack.message","timestamp":"2026-09-13T12:00:01Z","external_id":"C0DEMO002:1789300801.000100","payload":{"channel_id":"C0DEMO002","channel_name":"C0DEMO002","conversation_type":"channel","ts":"1789300801.000100","text":"Raw id"}}`, []string{"event 2", "channel_name is required"}},
	}
	for _, tc := range rejected {
		output, err := runworkgraphInput(t, repoRoot, named+"\n"+tc.event+"\n", "capture", "ingest", "--home", homeDir, "--source", "slack", "--json", "-")
		if err == nil {
			t.Fatalf("%s: expected rejection, got:\n%s", tc.name, output)
		}
		for _, expected := range tc.message {
			if !strings.Contains(string(output), expected) {
				t.Fatalf("%s: expected error to mention %q, got:\n%s", tc.name, expected, output)
			}
		}
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	var stored int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE source = 'slack'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Fatalf("expected rejected batches to store nothing, got %d events", stored)
	}

	if output, err := runworkgraphInput(t, repoRoot, named+"\n", "capture", "ingest", "--home", homeDir, "--source", "slack", "--json", "-"); err != nil {
		t.Fatalf("ingest named channel: %v\n%s", err, output)
	}
	known := `{"type":"slack.message","timestamp":"2026-09-13T12:00:02Z","external_id":"C0DEMO001:1789300802.000100","payload":{"channel_id":"C0DEMO001","conversation_type":"channel","ts":"1789300802.000100","text":"Known id without name"}}`
	if output, err := runworkgraphInput(t, repoRoot, known+"\n", "capture", "ingest", "--home", homeDir, "--source", "slack", "--json", "-"); err != nil {
		t.Fatalf("expected a known channel_id without channel_name to be accepted: %v\n%s", err, output)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, created_at) VALUES ('legacy-untyped', 'slack', 'slack.message', ?, ?, 'kept', ?)`, stamp, `{"channel_id":"C0DEMO009","channel_name":"private-name","ts":"1789300700.000100"}`, stamp); err != nil {
		t.Fatal(err)
	}
	untypedAlias := `{"type":"slack.message","timestamp":"2026-09-13T12:00:03Z","external_id":"C0DEMO009:1789300803.000100","payload":{"channel_id":"C0DEMO009","conversation_type":"channel","ts":"1789300803.000100","text":"Name only known from a DM-treated event"}}`
	if output, err := runworkgraphInput(t, repoRoot, untypedAlias+"\n", "capture", "ingest", "--home", homeDir, "--source", "slack", "--json", "-"); err == nil || !strings.Contains(string(output), "channel_name is required for C0DEMO009") {
		t.Fatalf("expected a name known only from a DM-treated event to be refused, got %v:\n%s", err, output)
	}
	var project sql.NullString
	var payloadJSON string
	if err := db.QueryRow(`SELECT project, payload_json FROM events WHERE json_extract(payload_json, '$.ts') = '1789300802.000100'`).Scan(&project, &payloadJSON); err != nil {
		t.Fatal(err)
	}
	if project.String != "squad-demo" {
		t.Fatalf("expected the stored channel name as container, got %+v", project)
	}
	if payloadJSON != `{"channel_id":"C0DEMO001","conversation_type":"channel","text":"Known id without name","ts":"1789300802.000100"}` {
		t.Fatalf("expected the payload stored as submitted, got %s", payloadJSON)
	}
}

func TestBridgedSlackReplyJoinsThreadUnit(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "slack")

	input := strings.Join([]string{
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:00Z","external_id":"C0DEMO001:1789300800.000100","payload":{"channel_id":"C0DEMO001","channel_name":"squad-demo","conversation_type":"channel","ts":"1789300800.000100","text":"Root"}}`,
		`{"type":"slack.reply","timestamp":"2026-09-13T12:05:00Z","external_id":"C0DEMO001:1789301100.000200","payload":{"channel_id":"C0DEMO001","channel_name":"squad-demo","conversation_type":"channel","ts":"1789301100.000200","thread_ts":"1789300800.000100","text":"Reply"}}`,
	}, "\n") + "\n"
	for attempt := 0; attempt < 2; attempt++ {
		if output, err := runworkgraphInput(t, repoRoot, input, "capture", "ingest", "--home", homeDir, "--source", "slack", "--json", "-"); err != nil {
			t.Fatalf("bridged ingest attempt %d: %v\n%s", attempt+1, err, output)
		}
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	types := map[string]int{}
	rows, err := db.Query(`SELECT type FROM events WHERE source = 'slack'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var eventType string
		if err := rows.Scan(&eventType); err != nil {
			t.Fatal(err)
		}
		types[eventType]++
	}
	rows.Close()
	if !reflect.DeepEqual(types, map[string]int{"slack.message": 1, "slack.thread_reply": 1}) {
		t.Fatalf("expected the reply stored once as slack.thread_reply, got %v", types)
	}

	units, err := workgraph.ListProjectUnits(workgraph.ProjectUnitsConfig{HomeDir: homeDir, Now: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC), Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 1 || units[0].ID != "slack.thread:C0DEMO001:1789300800.000100" || len(units[0].EventIDs) != 2 || units[0].Container != "squad-demo" {
		t.Fatalf("expected one thread unit with root and reply, got %+v", units)
	}
}

func TestBridgedSlackIngestDerivesContainerFromChannelName(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "slack")

	input := strings.Join([]string{
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:00Z","external_id":"C0DEMO001:1789300800.000100","payload":{"channel_id":"C0DEMO001","channel_name":"squad-demo-excellence","conversation_type":"channel","ts":"1789300800.000100","text":"Named channel"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:01Z","external_id":"C0DEMO002:1789300801.000100","payload":{"channel_id":"C0DEMO002","channel_name":"#guild-demo","conversation_type":"private_channel","ts":"1789300801.000100","text":"Hash channel"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:02Z","external_id":"C0DEMO003:1789300802.000100","project":"demo-production","payload":{"channel_id":"C0DEMO003","channel_name":"demo-changes","conversation_type":"channel","ts":"1789300802.000100","text":"Explicit project"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:03Z","external_id":"D0DEMO004:1789300803.000100","payload":{"channel_id":"D0DEMO004","channel_name":"DM: Lila","conversation_type":"im","ts":"1789300803.000100","text":"Direct message"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:04Z","external_id":"C0DEMO005:1789300804.000100","project":"#demo-release","payload":{"channel_id":"C0DEMO005","channel_name":"demo-release-chat","conversation_type":"channel","ts":"1789300804.000100","text":"Hash project"}}`,
	}, "\n") + "\n"
	if output, err := runworkgraphInput(t, repoRoot, input, "capture", "ingest", "--home", homeDir, "--source", "slack", "--json", "-"); err != nil {
		t.Fatalf("bridged ingest: %v\n%s", err, output)
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	expected := map[string]sql.NullString{
		"C0DEMO001": {String: "squad-demo-excellence", Valid: true},
		"C0DEMO002": {String: "guild-demo", Valid: true},
		"C0DEMO003": {String: "demo-production", Valid: true},
		"D0DEMO004": {String: "DM: Lila", Valid: true},
		"C0DEMO005": {String: "demo-release", Valid: true},
	}
	for channel, want := range expected {
		var project sql.NullString
		var payloadJSON string
		if err := db.QueryRow(`SELECT project, payload_json FROM events WHERE source = 'slack' AND json_extract(payload_json, '$.channel_id') = ?`, channel).Scan(&project, &payloadJSON); err != nil {
			t.Fatalf("read bridged Slack event %s: %v", channel, err)
		}
		if project != want {
			t.Fatalf("expected %s project %+v, got %+v", channel, want, project)
		}
		if channel == "D0DEMO004" && workgraph.SlackPrivacySource(payloadJSON) != "slack.dm" {
			t.Fatalf("expected DM container to keep DM privacy, got %s", payloadJSON)
		}
	}
}

func TestBridgedSlackIngestStripsChannelKindPrefix(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "slack")

	input := strings.Join([]string{
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:00Z","external_id":"C0123ABCDEF:1789300800.000100","payload":{"channel_id":"C0123ABCDEF","channel_name":"ZC:C0123ABCDEF:Team Channel","conversation_type":"channel","ts":"1789300800.000100","text":"Prefixed name"}}`,
		`{"type":"slack.thread_reply","timestamp":"2026-09-13T12:00:01Z","external_id":"C0123ABCDEF:1789300801.000100","project":"ZC:C0123ABCDEF:Team Channel","payload":{"channel_id":"C0123ABCDEF","channel_name":"ZC:C0123ABCDEF:Team Channel","conversation_type":"channel","ts":"1789300801.000100","thread_ts":"1789300800.000100","text":"Prefixed project"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:02Z","external_id":"C0123ABCDEF:1789300802.000100","payload":{"channel_id":"C0123ABCDEF","channel_name":"Team Channel","conversation_type":"channel","ts":"1789300802.000100","text":"Plain name"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:03Z","external_id":"C0DEMO0099:1789300803.000100","payload":{"channel_id":"C0DEMO0099","channel_name":"ZC:C0123ABCDEF:Other Channel","conversation_type":"channel","ts":"1789300803.000100","text":"Mismatched id"}}`,
	}, "\n") + "\n"
	if output, err := runworkgraphInput(t, repoRoot, input, "capture", "ingest", "--home", homeDir, "--source", "slack", "--json", "-"); err != nil {
		t.Fatalf("bridged ingest: %v\n%s", err, output)
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	expected := map[string]struct{ project, channelName, raw string }{
		"1789300800.000100": {"Team Channel", "Team Channel", "ZC:C0123ABCDEF:Team Channel"},
		"1789300801.000100": {"Team Channel", "Team Channel", "ZC:C0123ABCDEF:Team Channel"},
		"1789300802.000100": {"Team Channel", "Team Channel", ""},
		"1789300803.000100": {"ZC:C0123ABCDEF:Other Channel", "ZC:C0123ABCDEF:Other Channel", ""},
	}
	for ts, want := range expected {
		var project sql.NullString
		var payloadJSON string
		if err := db.QueryRow(`SELECT project, payload_json FROM events WHERE source = 'slack' AND json_extract(payload_json, '$.ts') = ?`, ts).Scan(&project, &payloadJSON); err != nil {
			t.Fatalf("read bridged Slack event %s: %v", ts, err)
		}
		if project.String != want.project {
			t.Fatalf("expected %s container %q, got %q", ts, want.project, project.String)
		}
		var payload struct {
			ChannelName      string `json:"channel_name"`
			ChannelNameRaw   string `json:"channel_name_raw"`
			ConversationType string `json:"conversation_type"`
		}
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			t.Fatalf("decode %s payload: %v", ts, err)
		}
		if payload.ChannelName != want.channelName || payload.ChannelNameRaw != want.raw || payload.ConversationType != "channel" {
			t.Fatalf("expected %s payload channel_name %q and channel_name_raw %q, got %s", ts, want.channelName, want.raw, payloadJSON)
		}
		if workgraph.SlackPrivacySource(payloadJSON) != "slack" {
			t.Fatalf("expected %s to keep channel privacy, got %s", ts, payloadJSON)
		}
	}
}

func TestBridgedSlackIngestKindPrefixBoundaries(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "slack")

	type row struct{ channelID, channelName, conversationType, project, privacy string }
	rows := map[string]row{
		"1789300900.000100": {"D0DEMO0201", "ZC:D0DEMO0201:DM: Lila", "im", "DM: Lila", "slack.dm"},
		"1789300901.000100": {"G0DEMO0202", "ZC:G0DEMO0202:Group DM: Ada, Grace", "mpim", "Group DM: Ada, Grace", "slack.dm"},
		"1789300902.000100": {"C0DEMO0203", "Team: Ops: Notes", "channel", "Team: Ops: Notes", "slack"},
		"1789300903.000100": {"C0DEMO0204", ":C0DEMO0204:Zero", "channel", ":C0DEMO0204:Zero", "slack"},
		"1789300904.000100": {"C0DEMO0205", "ABCDE:C0DEMO0205:Five", "channel", "ABCDE:C0DEMO0205:Five", "slack"},
		"1789300905.000100": {"C0DEMO0206", "ABCD:C0DEMO0206:Four", "channel", "Four", "slack"},
		"1789300906.000100": {"X0DEMO0207", "ZC:X0DEMO0207:Other", "channel", "ZC:X0DEMO0207:Other", "slack"},
		"1789300907.000100": {"C0DEMO0208", "ZC:C0DEMO0208:Restricted Name", "im", "Restricted Name", "slack.dm"},
	}
	var lines []string
	for ts, r := range rows {
		lines = append(lines, fmt.Sprintf(`{"type":"slack.message","timestamp":"2026-09-13T12:00:00Z","external_id":"%s:%s","payload":{"channel_id":"%s","channel_name":%q,"conversation_type":"%s","ts":"%s","text":"Boundary"}}`, r.channelID, ts, r.channelID, r.channelName, r.conversationType, ts))
	}
	if output, err := runworkgraphInput(t, repoRoot, strings.Join(lines, "\n")+"\n", "capture", "ingest", "--home", homeDir, "--source", "slack", "--json", "-"); err != nil {
		t.Fatalf("bridged ingest: %v\n%s", err, output)
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	for ts, want := range rows {
		var project sql.NullString
		var payloadJSON string
		if err := db.QueryRow(`SELECT project, payload_json FROM events WHERE source = 'slack' AND json_extract(payload_json, '$.ts') = ?`, ts).Scan(&project, &payloadJSON); err != nil {
			t.Fatalf("read bridged Slack event %s: %v", ts, err)
		}
		if project.String != want.project {
			t.Fatalf("expected %s container %q, got %q", ts, want.project, project.String)
		}
		if workgraph.SlackPrivacySource(payloadJSON) != want.privacy {
			t.Fatalf("expected %s privacy %q, got %s", ts, want.privacy, payloadJSON)
		}
	}

	restricted := `{"type":"slack.message","timestamp":"2026-09-13T12:01:00Z","external_id":"C0DEMO0208:1789300960.000100","payload":{"channel_id":"C0DEMO0208","conversation_type":"channel","ts":"1789300960.000100","text":"Channel event without a name"}}` + "\n"
	output, err := runworkgraphInput(t, repoRoot, restricted, "capture", "ingest", "--home", homeDir, "--source", "slack", "--json", "-")
	if err == nil || !strings.Contains(string(output), "channel_name is required") {
		t.Fatalf("expected a stripped name seen only on a DM event never to name a channel event, got err=%v:\n%s", err, output)
	}

	aliased := `{"type":"slack.message","timestamp":"2026-09-13T12:01:01Z","external_id":"C0DEMO0206:1789300961.000100","payload":{"channel_id":"C0DEMO0206","conversation_type":"channel","ts":"1789300961.000100","text":"Channel event named by alias"}}` + "\n"
	if output, err := runworkgraphInput(t, repoRoot, aliased, "capture", "ingest", "--home", homeDir, "--source", "slack", "--json", "-"); err != nil {
		t.Fatalf("bridged ingest named by stripped alias: %v\n%s", err, output)
	}
	var project sql.NullString
	if err := db.QueryRow(`SELECT project FROM events WHERE source = 'slack' AND json_extract(payload_json, '$.ts') = '1789300961.000100'`).Scan(&project); err != nil {
		t.Fatal(err)
	}
	if project.String != "Four" {
		t.Fatalf("expected the stripped channel name as the alias container, got %q", project.String)
	}
}

func TestBridgedSlackIngestGivesEachDMConversationItsOwnContainer(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "slack")

	named := `{"type":"slack.message","timestamp":"2026-09-13T12:00:00Z","external_id":"C0DEMO023:1789300800.000100","payload":{"channel_id":"C0DEMO023","channel_name":"Group DM: Lila, Omar","conversation_type":"mpim","ts":"1789300800.000100","text":"Named group"}}` + "\n"
	if output, err := runworkgraphInput(t, repoRoot, named, "capture", "ingest", "--home", homeDir, "--source", "slack", "--json", "-"); err != nil {
		t.Fatalf("bridged ingest of named group DM: %v\n%s", err, output)
	}
	input := strings.Join([]string{
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:01Z","external_id":"D0DEMO021:1789300801.000100","payload":{"channel_id":"D0DEMO021","channel_name":"DM","conversation_type":"im","ts":"1789300801.000100","text":"Unnamed DM"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:02Z","external_id":"C0DEMO022:1789300802.000100","payload":{"channel_id":"C0DEMO022","channel_name":"Group DM","conversation_type":"mpim","ts":"1789300802.000100","text":"Unnamed group"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:03Z","external_id":"C0DEMO023:1789300803.000100","project":"Group DM","payload":{"channel_id":"C0DEMO023","channel_name":"Group DM","conversation_type":"mpim","ts":"1789300803.000100","text":"Generic name for a known group"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:04Z","external_id":"D0DEMO024:1789300804.000100","payload":{"channel_id":"D0DEMO024","channel_name":"DM with Sam Rivera","conversation_type":"im","ts":"1789300804.000100","text":"Legacy DM form"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:05Z","external_id":"C0DEMO025:1789300805.000100","project":"Group DM","payload":{"channel_id":"C0DEMO025","channel_name":"Group DM (Lila, Ren)","conversation_type":"mpim","ts":"1789300805.000100","text":"Legacy group form"}}`,
		`{"type":"slack.message","timestamp":"2026-09-13T12:00:06Z","external_id":"C0DEMO026:1789300806.000100","payload":{"channel_id":"C0DEMO026","channel_name":"DM (Ada, Grace)","conversation_type":"mpim","ts":"1789300806.000100","text":"Group labelled as DM"}}`,
	}, "\n") + "\n"
	if output, err := runworkgraphInput(t, repoRoot, input, "capture", "ingest", "--home", homeDir, "--source", "slack", "--json", "-"); err != nil {
		t.Fatalf("bridged ingest of generic DM names: %v\n%s", err, output)
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	expected := map[string]struct{ project, channelName string }{
		"D0DEMO021:1789300801.000100": {"DM (D0DEMO021)", "DM"},
		"C0DEMO022:1789300802.000100": {"Group DM (C0DEMO022)", "Group DM"},
		"C0DEMO023:1789300803.000100": {"Group DM: Lila, Omar", "Group DM"},
		"D0DEMO024:1789300804.000100": {"DM: Sam Rivera", "DM with Sam Rivera"},
		"C0DEMO025:1789300805.000100": {"Group DM: Lila, Ren", "Group DM (Lila, Ren)"},
		"C0DEMO026:1789300806.000100": {"Group DM: Ada, Grace", "DM (Ada, Grace)"},
	}
	for key, want := range expected {
		var project sql.NullString
		var payloadJSON string
		if err := db.QueryRow(`SELECT project, payload_json FROM events WHERE source = 'slack' AND json_extract(payload_json, '$.channel_id') || ':' || json_extract(payload_json, '$.ts') = ?`, key).Scan(&project, &payloadJSON); err != nil {
			t.Fatalf("read bridged Slack event %s: %v", key, err)
		}
		if project.String != want.project {
			t.Fatalf("expected %s container %q, got %q", key, want.project, project.String)
		}
		var payload struct {
			ChannelName string `json:"channel_name"`
		}
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil || payload.ChannelName != want.channelName {
			t.Fatalf("expected %s payload stored as submitted, got %s", key, payloadJSON)
		}
		if workgraph.SlackPrivacySource(payloadJSON) != "slack.dm" {
			t.Fatalf("expected %s to keep DM privacy, got %s", key, payloadJSON)
		}
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
	if err == nil || !strings.Contains(string(output), "empty bridged capture requires") {
		t.Fatalf("expected unproven empty capture to fail, got err=%v output=%s", err, output)
	}
	output, err = runworkgraphInput(t, repoRoot, `{"events":[],"empty_proof":{"exhaustive_query":true}}`, "capture", "ingest", "--home", homeDir,
		"--request", claim.RequestID, "--claim-file", claimFile, "--json", "-")
	if err != nil {
		t.Fatalf("complete proven-empty capture: %v\n%s", err, output)
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

func TestCalendarBridgeProgressUsesRequestCompletionBound(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "calendar.microsoft")
	now := time.Now().UTC()
	emitted, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{
		HomeDir: homeDir, ConnectorID: "calendar.microsoft", Now: now,
	})
	if err != nil {
		t.Fatalf("emit calendar request: %v", err)
	}
	claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{
		HomeDir: homeDir, ConnectorID: "calendar.microsoft", Worker: "facts", Max: 1,
		Now: now, Lease: 10 * time.Minute,
	})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim calendar request: claims=%d error=%v", len(claims), err)
	}
	result, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{
		HomeDir: homeDir, RequestID: emitted.Request.ID, ClaimToken: claims[0].ClaimToken,
		Input: strings.NewReader(fmt.Sprintf(`[{
  "type":"calendar.microsoft.event",
  "timestamp":%q,
  "external_id":"event-1:change:rev-7",
  "payload":{"start":%q,"provider_start":"2026-09-21T09:00:00","provider_end":"2026-09-21T10:00:00"}
}]`, now.Add(-time.Hour).Format(time.RFC3339), now.Add(-time.Hour).Format(time.RFC3339))),
	})
	if err != nil {
		t.Fatalf("ingest calendar event: %v", err)
	}
	if !result.RequestCompleted {
		t.Fatal("expected calendar request to complete")
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	defer db.Close()
	var completedThrough string
	if err := db.QueryRow(`SELECT completed_through FROM capture_cursors WHERE connector_id = 'calendar.microsoft'`).Scan(&completedThrough); err != nil {
		t.Fatalf("read calendar cursor: %v", err)
	}
	if completedThrough != emitted.Request.Until {
		t.Fatalf("expected cursor at request completion %q, got %q", emitted.Request.Until, completedThrough)
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

func TestDaemonSchedulesNotionActivityConnector(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("initialize workgraph: %v", err)
	}
	if _, err := workgraph.ConfigureBridgedConnector(workgraph.ConnectorBridgeConfig{
		HomeDir: homeDir, ID: "notion.activity", BridgeParams: json.RawMessage(bridgeParamsForFact("notion.activity")),
	}); err != nil {
		t.Fatalf("connect bridged notion.activity: %v", err)
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
		err := db.QueryRow(`SELECT COUNT(*) FROM capture_requests WHERE connector_id = 'notion.activity' AND status = 'pending'`).Scan(&count)
		if err == nil && count == 1 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("daemon did not emit bridged notion.activity request: count=%d error=%v", count, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("stop capture daemon: %v", err)
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

func TestBridgedBackfillEmitsOneDayAtATimeAndStaysDue(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	start := time.Now().UTC().Add(-72 * time.Hour)
	for _, connector := range []string{"slack", "calendar.microsoft", "slack.lists"} {
		connectBridgedConnector(t, repoRoot, homeDir, connector)
		completeBridgedRequestAt(t, homeDir, connector, start)
	}

	now := time.Now().UTC()
	slack, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: "slack", Now: now})
	if err != nil {
		t.Fatalf("emit slack backfill request: %v", err)
	}
	since, until := parseRequestWindow(t, slack.Request)
	if !since.Equal(start.Add(-5*time.Minute)) || until.Sub(since) != 24*time.Hour {
		t.Fatalf("expected one-day backfill window from cursor minus overlap, got %s to %s", slack.Request.Since, slack.Request.Until)
	}
	if !connectorNextPollDue(t, homeDir, "slack", now) {
		t.Fatal("expected a behind connector to be due again immediately after completion")
	}

	for _, connector := range []string{"calendar.microsoft", "slack.lists"} {
		emitted, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: connector, Now: now})
		if err != nil {
			t.Fatalf("emit %s request: %v", connector, err)
		}
		if _, until := parseRequestWindow(t, emitted.Request); !until.Equal(now) {
			t.Fatalf("expected %s request to end now, got %s", connector, emitted.Request.Until)
		}
	}
}

func TestBridgedNineteenDayGapDrainsDayByDayWithoutGaps(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	cursor := time.Now().UTC().Add(-19 * 24 * time.Hour)
	completeBridgedRequestAt(t, homeDir, "slack", cursor)

	cycles := 0
	for {
		cycles++
		if cycles > 25 {
			t.Fatal("expected the 19-day gap to drain within 25 one-day cycles")
		}
		now := time.Now().UTC()
		emitted, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: "slack", Now: now})
		if err != nil || emitted.Coalesced {
			t.Fatalf("cycle %d emit: coalesced=%v error=%v", cycles, emitted.Coalesced, err)
		}
		since, until := parseRequestWindow(t, emitted.Request)
		if overlap := cursor.Sub(since); overlap != 5*time.Minute {
			t.Fatalf("cycle %d expected exact 5m overlap with cursor %s, got since %s", cycles, cursor.Format(time.RFC3339Nano), emitted.Request.Since)
		}
		caughtUp := until.Equal(now)
		if !caughtUp && until.Sub(since) != 24*time.Hour {
			t.Fatalf("cycle %d expected a 24h window while behind, got %s to %s", cycles, emitted.Request.Since, emitted.Request.Until)
		}
		claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{HomeDir: homeDir, ConnectorID: "slack", Worker: "facts", Max: 1, Now: now})
		if err != nil || len(claims) != 1 || claims[0].Request.ID != emitted.Request.ID {
			t.Fatalf("cycle %d claim: claims=%d error=%v", cycles, len(claims), err)
		}
		if _, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{
			HomeDir: homeDir, RequestID: emitted.Request.ID, ClaimToken: claims[0].ClaimToken,
			Input: strings.NewReader(`{"events":[],"empty_proof":{"exhaustive_query":true}}`),
		}); err != nil {
			t.Fatalf("cycle %d complete: %v", cycles, err)
		}
		watermark, err := workgraph.CaptureWatermark(workgraph.CaptureRequestListConfig{HomeDir: homeDir, ConnectorID: "slack"})
		if err != nil || watermark != emitted.Request.Until {
			t.Fatalf("cycle %d expected cursor at %s, got %q error=%v", cycles, emitted.Request.Until, watermark, err)
		}
		if caughtUp {
			break
		}
		if !connectorNextPollDue(t, homeDir, "slack", time.Now().UTC()) {
			t.Fatalf("cycle %d expected the next request to be due immediately while behind", cycles)
		}
		cursor = until
	}
	if cycles != 20 {
		t.Fatalf("expected 19 days to drain in 20 overlapping one-day cycles, got %d", cycles)
	}
	if connectorNextPollDue(t, homeDir, "slack", time.Now().UTC()) {
		t.Fatal("expected a caught-up connector to wait for its interval")
	}
}

func TestStalledBridgeIsVisibleInConnectorsTodayAndResume(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "slack")
	stalledAt := time.Now().UTC().Add(-19*24*time.Hour - time.Hour)
	if _, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: "slack", Now: stalledAt}); err != nil {
		t.Fatalf("emit stalled request: %v", err)
	}
	clientPath := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\necho 'starting session'\necho 'Failed to authenticate: OAuth session expired' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatalf("write failing fake Codex: %v", err)
	}
	if output, err := runworkgraph(t, repoRoot, "bridge", "drain", "--home", homeDir, "--client", "codex", "--client-command", clientPath); err == nil {
		t.Fatalf("expected expired client login to fail the drain:\n%s", output)
	}

	for _, command := range []string{"list", "status"} {
		output, err := runworkgraph(t, repoRoot, "connectors", command, "--home", homeDir)
		if err != nil {
			t.Fatalf("connectors %s: %v\n%s", command, err, output)
		}
		for _, expected := range []string{"bridge stalled 19d: no completed capture yet", "last drain error (codex): Failed to authenticate: OAuth session expired"} {
			if !strings.Contains(string(output), expected) {
				t.Fatalf("connectors %s omitted %q:\n%s", command, expected, output)
			}
		}
	}

	warning := "Bridge stalled: slack (19d); run workgraph plugin doctor --client codex"
	today, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, MemoryDir: filepath.Join(t.TempDir(), "memory")})
	if err != nil {
		t.Fatalf("today: %v", err)
	}
	if strings.Count(today.Message, warning) != 1 {
		t.Fatalf("today omitted one stalled bridge warning %q:\n%s", warning, today.Message)
	}
	resume, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: homeDir, MemoryDir: filepath.Join(t.TempDir(), "memory")})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if strings.Count(resume.Message, warning) != 1 {
		t.Fatalf("resume omitted one stalled bridge warning %q:\n%s", warning, resume.Message)
	}
}

func TestExpiredClaimIsLabeledAndRecycledByEmitCycle(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "slack")
	start := time.Now().UTC().Add(-3 * time.Hour)
	emitted, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: "slack", Now: start})
	if err != nil {
		t.Fatalf("emit request: %v", err)
	}
	if claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{
		HomeDir: homeDir, ConnectorID: "slack", Worker: "hung-worker", Max: 1, Now: start, Lease: time.Hour,
	}); err != nil || len(claims) != 1 {
		t.Fatalf("claim request: claims=%d error=%v", len(claims), err)
	}

	output, err := runworkgraph(t, repoRoot, "capture", "requests", "--list", "--home", homeDir)
	if err != nil {
		t.Fatalf("list requests: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), emitted.Request.ID+": claimed (lease expired 2h ago)") {
		t.Fatalf("expected expired lease label in request list:\n%s", output)
	}
	requests, err := workgraph.ListCaptureRequests(workgraph.CaptureRequestListConfig{HomeDir: homeDir, ConnectorID: "slack"})
	if err != nil || len(requests) != 1 {
		t.Fatalf("list requests for MCP: requests=%d error=%v", len(requests), err)
	}
	if requests[0].Status != "claimed" || requests[0].StatusDetail != "claimed (lease expired 2h ago)" {
		t.Fatalf("expected MCP listing to keep status and add expired lease detail, got %#v", requests[0])
	}

	recycled, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: "slack"})
	if err != nil {
		t.Fatalf("daemon emit cycle: %v", err)
	}
	if !recycled.Coalesced || recycled.Request.ID != emitted.Request.ID || recycled.Request.Status != "pending" || recycled.Request.ClaimedBy != "" {
		t.Fatalf("expected emit cycle to return the expired claim to pending, got %#v", recycled)
	}
}

func TestDisablingBridgedConnectorCancelsItsActiveRequest(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	repoRoot := repoRoot(t)
	connectBridgedConnector(t, repoRoot, homeDir, "slack")
	emitted, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: "slack"})
	if err != nil {
		t.Fatalf("emit request: %v", err)
	}
	claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{HomeDir: homeDir, ConnectorID: "slack", Worker: "facts", Max: 1})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim request: claims=%d error=%v", len(claims), err)
	}

	output, err := runworkgraph(t, repoRoot, "connectors", "disable", "--home", homeDir, "slack")
	if err != nil {
		t.Fatalf("disable connector: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Cancelled capture request "+emitted.Request.ID+" (connector disabled; was claimed by facts)") {
		t.Fatalf("expected disable to report cancelled claimed work:\n%s", output)
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	var status, lastError string
	var claimToken sql.NullString
	if err := db.QueryRow(`SELECT status, last_error, claim_token FROM capture_requests WHERE id = ?`, emitted.Request.ID).Scan(&status, &lastError, &claimToken); err != nil {
		t.Fatalf("read disabled request: %v", err)
	}
	if status != "cancelled" || lastError != "connector disabled" || claimToken.Valid {
		t.Fatalf("expected cancelled request without claim token, got status=%q error=%q token=%v", status, lastError, claimToken.Valid)
	}
	if _, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{
		HomeDir: homeDir, RequestID: emitted.Request.ID, ClaimToken: claims[0].ClaimToken, Input: strings.NewReader(`{"events":[],"empty_proof":{"exhaustive_query":true}}`),
	}); err == nil {
		t.Fatal("expected cancelled claim to reject ingest")
	}
}

func completeBridgedRequestAt(t *testing.T, homeDir string, connector string, at time.Time) {
	t.Helper()
	emitted, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: connector, Now: at})
	if err != nil {
		t.Fatalf("emit %s request: %v", connector, err)
	}
	claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{
		HomeDir: homeDir, ConnectorID: connector, Worker: "facts", Max: 1, Now: at, Lease: time.Since(at) + time.Hour,
	})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim %s request: claims=%d error=%v", connector, len(claims), err)
	}
	if _, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{
		HomeDir: homeDir, RequestID: emitted.Request.ID, ClaimToken: claims[0].ClaimToken,
		Input: strings.NewReader(`{"events":[],"empty_proof":{"exhaustive_query":true}}`),
	}); err != nil {
		t.Fatalf("complete %s request: %v", connector, err)
	}
}

func parseRequestWindow(t *testing.T, request workgraph.CaptureRequest) (time.Time, time.Time) {
	t.Helper()
	since, err := time.Parse(time.RFC3339Nano, request.Since)
	if err != nil {
		t.Fatalf("parse request since: %v", err)
	}
	until, err := time.Parse(time.RFC3339Nano, request.Until)
	if err != nil {
		t.Fatalf("parse request until: %v", err)
	}
	return since, until
}

func connectorNextPollDue(t *testing.T, homeDir string, connector string, now time.Time) bool {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(homeDir, "connectors.json"))
	if err != nil {
		t.Fatalf("read connector state: %v", err)
	}
	var state struct {
		Connectors map[string]struct {
			NextPoll string `json:"next_poll_at"`
		} `json:"connectors"`
	}
	if err := json.Unmarshal(contents, &state); err != nil {
		t.Fatalf("parse connector state: %v", err)
	}
	next, err := time.Parse(time.RFC3339, state.Connectors[connector].NextPoll)
	if err != nil {
		t.Fatalf("parse %s next poll %q: %v", connector, state.Connectors[connector].NextPoll, err)
	}
	return !next.After(now)
}

func initBridgedCaptureHome(t *testing.T) string {
	t.Helper()
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	if output, err := runworkgraph(t, repoRoot(t), "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}
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
		return `{"scope":"participant","identity":"@me","include":["involves","review_requested"]}`
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
	case "notion.activity":
		return `{"scope":"participant","identity":"@me","include":["edited","created"]}`
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
