package facts

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalBridgeMCPAdvertisesToolsAndConfiguresScopedConnector(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"facts","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"connector_bridge_configure","arguments":{"connector":"slack","interval":"10m","bridge_params":{"channels":["C0DEMO123"]}}}}`,
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
