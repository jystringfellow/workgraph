package facts

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestLocalBridgeMCPAdvertisesToolsAndConfiguresScopedConnector(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"facts","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"connector_bridge_configure","arguments":{"connector":"slack","interval":"10m","bridge_params":{"channels":["C0DEMO123"]}}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"capture_requests_list","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"capture_requests_claim","arguments":{"worker":"facts"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"connector_status","arguments":{}}}`,
	}
	command := exec.Command(workgraphFactsBinary, "bridge", "mcp", "--home", homeDir)
	command.Dir = repoRoot(t)
	command.Stdin = strings.NewReader(strings.Join(requests, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("run local bridge MCP: %v\nstderr:\n%s\nstdout:\n%s", err, stderr.String(), stdout.String())
	}
	for _, tool := range []string{
		"capture_requests_list", "capture_requests_claim", "capture_request_renew",
		"capture_ingest", "capture_request_fail", "capture_watermark",
		"connector_status", "connector_bridge_configure", "connector_bridge_disconnect",
		"bridge_worker_heartbeat",
	} {
		if !strings.Contains(stdout.String(), `"name":"`+tool+`"`) {
			t.Fatalf("MCP tools/list omitted %s:\n%s", tool, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "provider-token") {
		t.Fatalf("MCP output exposed a provider credential:\n%s", stdout.String())
	}
	collectionFields := map[float64]string{4: "requests", 5: "claims", 6: "connectors"}
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		var response map[string]any
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatalf("parse MCP response: %v\n%s", err, line)
		}
		field, found := collectionFields[response["id"].(float64)]
		if !found {
			continue
		}
		result, ok := response["result"].(map[string]any)
		if !ok {
			t.Fatalf("MCP tool %d returned no result object: %#v", int(response["id"].(float64)), response)
		}
		structured, ok := result["structuredContent"].(map[string]any)
		if !ok {
			t.Fatalf("MCP tool %d structuredContent is not an object: %#v", int(response["id"].(float64)), result["structuredContent"])
		}
		if _, ok := structured[field].([]any); !ok {
			t.Fatalf("MCP tool %d structuredContent omitted %q collection: %#v", int(response["id"].(float64)), field, structured)
		}
		runtimeInfo, ok := structured["runtime"].(map[string]any)
		if !ok {
			t.Fatalf("MCP tool %d omitted runtime build information: %#v", int(response["id"].(float64)), structured)
		}
		for _, build := range []string{"running", "on_disk"} {
			identity, ok := runtimeInfo[build].(map[string]any)
			if !ok || strings.TrimSpace(identity["version"].(string)) == "" {
				t.Fatalf("MCP tool %d omitted %s build identity: %#v", int(response["id"].(float64)), build, runtimeInfo)
			}
		}
		if stale, ok := runtimeInfo["stale"].(bool); !ok || stale {
			t.Fatalf("expected fresh MCP runtime, got %#v", runtimeInfo)
		}
	}

	contents, err := os.ReadFile(filepath.Join(homeDir, "connectors.json"))
	if err != nil {
		t.Fatalf("read configured connector: %v", err)
	}
	var state struct {
		Connectors map[string]struct {
			CaptureMode  string          `json:"capture_mode"`
			Interval     string          `json:"interval"`
			BridgeParams json.RawMessage `json:"bridge_params"`
		} `json:"connectors"`
	}
	if err := json.Unmarshal(contents, &state); err != nil {
		t.Fatalf("parse connector state: %v", err)
	}
	slack := state.Connectors["slack"]
	var bridgeParams struct {
		Channels []string `json:"channels"`
	}
	if err := json.Unmarshal(slack.BridgeParams, &bridgeParams); err != nil {
		t.Fatalf("parse bridge parameters: %v", err)
	}
	if slack.CaptureMode != "bridged" || slack.Interval != "10m0s" || len(bridgeParams.Channels) != 1 || bridgeParams.Channels[0] != "C0DEMO123" {
		t.Fatalf("unexpected MCP-configured Slack state: %#v", slack)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "slack.json")); !os.IsNotExist(err) {
		t.Fatalf("MCP bridged setup created provider credentials: %v", err)
	}
}

func TestBridgeMCPReportsStaleAfterItsExecutableChanges(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	binaryPath := filepath.Join(t.TempDir(), "workgraph")
	binary, err := os.ReadFile(workgraphFactsBinary)
	if err != nil {
		t.Fatalf("read facts binary: %v", err)
	}
	if err := os.WriteFile(binaryPath, binary, 0o700); err != nil {
		t.Fatalf("copy facts binary: %v", err)
	}

	command := exec.Command(binaryPath, "bridge", "mcp", "--home", homeDir)
	command.Dir = repoRoot(t)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatalf("open MCP stdin: %v", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("open MCP stdout: %v", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start MCP server: %v", err)
	}
	scanner := bufio.NewScanner(stdout)
	writeRequest := func(request string) map[string]any {
		t.Helper()
		if _, err := io.WriteString(stdin, request+"\n"); err != nil {
			t.Fatalf("write MCP request: %v", err)
		}
		if !scanner.Scan() {
			t.Fatalf("read MCP response: %v\nstderr:\n%s", scanner.Err(), stderr.String())
		}
		var response map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatalf("decode MCP response: %v\n%s", err, scanner.Text())
		}
		return response
	}
	writeRequest(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"facts","version":"1"}}}`)

	changedAt := time.Now().Add(2 * time.Minute)
	if err := os.Chtimes(binaryPath, changedAt, changedAt); err != nil {
		t.Fatalf("replace MCP executable timestamp: %v", err)
	}
	response := writeRequest(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"capture_requests_list","arguments":{}}}`)
	result := response["result"].(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	runtimeInfo := structured["runtime"].(map[string]any)
	if runtimeInfo["stale"] != true || !strings.Contains(runtimeInfo["warning"].(string), "start a new client session") {
		t.Fatalf("expected changed MCP executable to report stale runtime, got %#v", runtimeInfo)
	}
	if runtimeInfo["started_executable_modified_at"] == runtimeInfo["on_disk_executable_modified_at"] {
		t.Fatalf("expected MCP executable timestamps to differ, got %#v", runtimeInfo)
	}

	if err := stdin.Close(); err != nil {
		t.Fatalf("close MCP stdin: %v", err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("wait for MCP server: %v\nstderr:\n%s", err, stderr.String())
	}
}

func TestBridgeMCPIngestsRawSlackListCSVServerSide(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	params := json.RawMessage(`{"lists":["FRAWCSV"],"list_options":{"FRAWCSV":{"state":{"column":"Status","done_values":["Done"]},"interest_columns":["Title","Priority"],"row_key_candidates":[["Title"]]}}}`)
	if _, err := workgraph.ConfigureBridgedConnector(workgraph.ConnectorBridgeConfig{
		HomeDir: homeDir, ID: "slack.lists", BridgeParams: params,
	}); err != nil {
		t.Fatalf("configure Slack Lists bridge: %v", err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	emitted, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{
		HomeDir: homeDir, ConnectorID: "slack.lists", Now: now,
	})
	if err != nil {
		t.Fatalf("emit Slack Lists request: %v", err)
	}
	claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{
		HomeDir: homeDir, ConnectorID: "slack.lists", Worker: "facts", Max: 1, Now: now, Lease: 10 * time.Minute,
	})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim Slack Lists request: claims=%d error=%v", len(claims), err)
	}
	csvSnapshot := "Title,Status,Priority,Notes\r\n\"Review design, phase 2\",Open,High,\"Keep \"\"all\"\" context\"\r\nSchedule meeting,Done,Medium,\r\n"
	arguments, err := json.Marshal(map[string]any{
		"request_id":   emitted.Request.ID,
		"claim_token":  claims[0].ClaimToken,
		"list_id":      "FRAWCSV",
		"snapshot_csv": csvSnapshot,
	})
	if err != nil {
		t.Fatalf("encode raw CSV ingest arguments: %v", err)
	}
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "capture_ingest", "arguments": json.RawMessage(arguments)},
	})
	if err != nil {
		t.Fatalf("encode MCP request: %v", err)
	}
	command := exec.Command(workgraphFactsBinary, "bridge", "mcp", "--home", homeDir)
	command.Dir = repoRoot(t)
	command.Stdin = strings.NewReader(string(request) + "\n")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("ingest raw Slack List CSV over MCP: %v", err)
	}
	var response map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output), &response); err != nil {
		t.Fatalf("decode MCP ingest response: %v\n%s", err, output)
	}
	result := response["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("raw CSV ingest failed: %#v", result)
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	rows, err := db.Query(`SELECT summary, payload_json FROM events WHERE type = 'slack.list_item' ORDER BY summary`)
	if err != nil {
		t.Fatalf("read raw CSV events: %v", err)
	}
	defer rows.Close()
	seen := map[string]map[string]any{}
	for rows.Next() {
		var summary, raw string
		if err := rows.Scan(&summary, &raw); err != nil {
			t.Fatalf("scan raw CSV event: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("decode raw CSV payload: %v", err)
		}
		seen[summary] = payload
	}
	if len(seen) != 2 || seen["Review design, phase 2"] == nil || seen["Schedule meeting"]["done"] != true {
		t.Fatalf("expected two parsed Slack List rows with configured state, got %#v", seen)
	}
	fields := seen["Review design, phase 2"]["fields"].(map[string]any)
	if fields["Notes"] != `Keep "all" context` {
		t.Fatalf("quoted CSV field was not preserved: %#v", fields)
	}
}
