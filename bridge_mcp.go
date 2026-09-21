package workgraph

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type BridgeMCPConfig struct {
	HomeDir      string
	DatabasePath string
	Input        io.Reader
	Output       io.Writer
	Context      context.Context
	runtime      ProcessRuntimeStatus
}

type BridgeMCPProcessResult struct {
	HomeDir string
	PIDs    []int
	Message string
}

type bridgeMCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type bridgeMCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func ServeBridgeMCP(config BridgeMCPConfig) error {
	if config.Input == nil || config.Output == nil {
		return fmt.Errorf("bridge MCP requires input and output")
	}
	if strings.TrimSpace(config.runtime.Executable) == "" {
		config.runtime = captureProcessRuntimeStatus()
	}
	ctx := config.Context
	if ctx == nil {
		ctx = context.Background()
	}
	scanner := bufio.NewScanner(config.Input)
	scanner.Buffer(make([]byte, 64*1024), maxBridgedIngestBytes)
	encoder := json.NewEncoder(config.Output)
	lines := make(chan string)
	scanDone := make(chan error, 1)
	stopScan := make(chan struct{})
	defer close(stopScan)
	go func() {
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-stopScan:
				return
			}
		}
		scanDone <- scanner.Err()
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var scanned string
		select {
		case scanned = <-lines:
		case <-ctx.Done():
			return nil
		case err := <-scanDone:
			if err != nil {
				return fmt.Errorf("read bridge MCP request: %w", err)
			}
			return nil
		case <-ticker.C:
			if inspectProcessRuntimeStatus(config.runtime, "MCP server").Stale {
				return nil
			}
			continue
		}

		line := strings.TrimSpace(scanned)
		if line == "" {
			continue
		}
		var request bridgeMCPRequest
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			if err := encoder.Encode(bridgeMCPError(nil, -32700, "parse error")); err != nil {
				return err
			}
			continue
		}
		if len(request.ID) == 0 {
			continue
		}
		result, rpcErr := handleBridgeMCPRequest(config, request)
		response := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(request.ID)}
		if rpcErr != nil {
			response["error"] = rpcErr
		} else {
			response["result"] = result
		}
		if err := encoder.Encode(response); err != nil {
			return fmt.Errorf("write bridge MCP response: %w", err)
		}
	}
}

func StatusBridgeMCP(homeDir string) (BridgeMCPProcessResult, error) {
	homeDir, err := resolveHomeDir(homeDir)
	if err != nil {
		return BridgeMCPProcessResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return BridgeMCPProcessResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	processes, err := listDaemonProcesses()
	if err != nil {
		return BridgeMCPProcessResult{}, fmt.Errorf("list bridge MCP processes: %w", err)
	}
	matches := matchingBridgeMCPProcesses(homeDir, processes)
	result := BridgeMCPProcessResult{HomeDir: homeDir, PIDs: make([]int, 0, len(matches))}
	lines := []string{"workgraph bridge MCP servers", "Home: " + homeDir}
	if len(matches) == 0 {
		lines = append(lines, "Running: 0")
	} else {
		lines = append(lines, "Running: "+strconv.Itoa(len(matches)))
		for _, process := range matches {
			result.PIDs = append(result.PIDs, process.PID)
			lines = append(lines, "PID: "+strconv.Itoa(process.PID))
		}
	}
	result.Message = strings.Join(lines, "\n")
	return result, nil
}

func StopBridgeMCP(homeDir string) (BridgeMCPProcessResult, error) {
	result, err := StatusBridgeMCP(homeDir)
	if err != nil {
		return BridgeMCPProcessResult{}, err
	}
	for _, pid := range result.PIDs {
		if err := signalDaemonProcess(pid, syscall.SIGTERM); err != nil && processRunning(pid) {
			return BridgeMCPProcessResult{}, fmt.Errorf("stop bridge MCP process %d: %w", pid, err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	remaining := append([]int(nil), result.PIDs...)
	for time.Now().Before(deadline) {
		current, statusErr := StatusBridgeMCP(result.HomeDir)
		if statusErr != nil {
			return BridgeMCPProcessResult{}, statusErr
		}
		remaining = current.PIDs
		if len(remaining) == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(remaining) > 0 {
		current, statusErr := StatusBridgeMCP(result.HomeDir)
		if statusErr != nil {
			return BridgeMCPProcessResult{}, statusErr
		}
		remaining = current.PIDs
	}
	if len(remaining) > 0 {
		pids := make([]string, 0, len(remaining))
		for _, pid := range remaining {
			pids = append(pids, strconv.Itoa(pid))
		}
		return BridgeMCPProcessResult{}, fmt.Errorf("bridge MCP processes did not stop after SIGTERM: %s", strings.Join(pids, ", "))
	}
	result.Message = fmt.Sprintf("workgraph bridge MCP servers stopped: %d\nHome: %s", len(result.PIDs), result.HomeDir)
	return result, nil
}

func matchingBridgeMCPProcesses(homeDir string, processes []daemonProcess) []daemonProcess {
	homeDir = cleanProcessPath(homeDir)
	matches := []daemonProcess{}
	for _, process := range processes {
		if strings.HasPrefix(process.State, "Z") {
			continue
		}
		args := strings.Fields(process.Command)
		server := false
		for index := 0; index+1 < len(args); index++ {
			if args[index] != "bridge" || args[index+1] != "mcp" {
				continue
			}
			if index+2 < len(args) && (args[index+2] == "status" || args[index+2] == "stop") {
				break
			}
			server = true
			break
		}
		if !server || cleanProcessPath(flagValue(args, "--home")) != homeDir {
			continue
		}
		matches = append(matches, process)
	}
	return matches
}

func handleBridgeMCPRequest(config BridgeMCPConfig, request bridgeMCPRequest) (any, map[string]any) {
	switch request.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "workgraph-bridge", "version": config.runtime.Running.Version},
		}, nil
	case "tools/list":
		return map[string]any{"tools": bridgeMCPTools()}, nil
	case "tools/call":
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(request.Params, &call); err != nil {
			return nil, bridgeMCPErrorData(-32602, "invalid tool call parameters")
		}
		value, err := callBridgeMCPTool(config, call.Name, call.Arguments)
		if err != nil {
			return map[string]any{"content": []any{map[string]any{"type": "text", "text": err.Error()}}, "isError": true}, nil
		}
		if strings.HasPrefix(call.Name, "capture_") || call.Name == "connector_status" || call.Name == "connector_required_tools" {
			value = bridgeMCPResultWithRuntime(value, inspectProcessRuntimeStatus(config.runtime, "MCP server"))
		}
		encoded, _ := json.Marshal(value)
		return map[string]any{
			"content":           []any{map[string]any{"type": "text", "text": string(encoded)}},
			"structuredContent": value,
		}, nil
	default:
		return nil, bridgeMCPErrorData(-32601, "method not found")
	}
}

func bridgeMCPResultWithRuntime(value any, runtime ProcessRuntimeStatus) map[string]any {
	result := map[string]any{}
	if encoded, err := json.Marshal(value); err == nil {
		_ = json.Unmarshal(encoded, &result)
	}
	if result == nil {
		result = map[string]any{}
	}
	result["runtime"] = runtime
	return result
}

func bridgeMCPTools() []bridgeMCPTool {
	object := func(properties map[string]any, required ...string) map[string]any {
		schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	str := map[string]any{"type": "string"}
	integer := map[string]any{"type": "integer", "minimum": 1, "maximum": 100}
	captureIngest := object(map[string]any{
		"request_id": str, "claim_token": str, "source": str, "events": map[string]any{},
		"list_id": str, "snapshot_csv": str,
	})
	captureIngest["oneOf"] = []any{
		map[string]any{"required": []string{"events"}},
		map[string]any{"required": []string{"request_id", "claim_token", "list_id", "snapshot_csv"}},
	}
	return []bridgeMCPTool{
		{"capture_requests_list", "List non-secret bridged capture outbox metadata.", object(map[string]any{"connector": str})},
		{"capture_requests_claim", "Atomically claim daemon-scheduled capture work.", object(map[string]any{"connector": str, "worker": str, "max": integer}, "worker")},
		{"capture_request_renew", "Renew an unexpired capture claim.", object(map[string]any{"request_id": str, "claim_token": str}, "request_id", "claim_token")},
		{"capture_ingest", "Validate and atomically ingest one normalized event batch, or parse one claimed Slack List snapshot from raw CSV server-side.", captureIngest},
		{"capture_request_fail", "Return claimed work for retry with bounded error details.", object(map[string]any{"request_id": str, "claim_token": str, "error": str}, "request_id", "claim_token")},
		{"capture_watermark", "Read a connector completed-through cursor.", object(map[string]any{"connector": str}, "connector")},
		{"connector_status", "Read connector capture mode and health.", object(map[string]any{})},
		{"connector_required_tools", "Read canonical provider fetch and identity requirements before claiming work.", object(map[string]any{"connector": str})},
		{"connector_bridge_configure", "Configure approved non-secret bridge scope and cadence.", object(map[string]any{"connector": str, "interval": str, "bridge_params": map[string]any{"type": "object"}}, "connector", "interval", "bridge_params")},
		{"connector_bridge_disconnect", "Switch a bridged connector back to direct mode and cancel active work.", object(map[string]any{"connector": str}, "connector")},
		{"bridge_worker_heartbeat", "Report that an installed client bridge worker is alive.", object(map[string]any{"worker": str}, "worker")},
	}
}

func callBridgeMCPTool(config BridgeMCPConfig, name string, raw json.RawMessage) (any, error) {
	var args map[string]json.RawMessage
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode %s arguments: %w", name, err)
	}
	rawStringArg := func(key string) string {
		var value string
		_ = json.Unmarshal(args[key], &value)
		return value
	}
	stringArg := func(key string) string {
		return strings.TrimSpace(rawStringArg(key))
	}
	base := CaptureRequestListConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath}
	switch name {
	case "capture_requests_list":
		base.ConnectorID = stringArg("connector")
		requests, err := ListCaptureRequests(base)
		return map[string]any{"requests": requests}, err
	case "capture_requests_claim":
		var max int
		_ = json.Unmarshal(args["max"], &max)
		claims, err := ClaimCaptureRequests(CaptureRequestClaimConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, ConnectorID: stringArg("connector"), Worker: stringArg("worker"), Max: max})
		return map[string]any{"claims": claims}, err
	case "capture_request_renew":
		return RenewCaptureRequest(CaptureRequestCapabilityConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, RequestID: stringArg("request_id"), ClaimToken: stringArg("claim_token")})
	case "capture_ingest":
		events := args["events"]
		snapshotCSV := rawStringArg("snapshot_csv")
		snapshotListID := ""
		if len(events) > 0 && snapshotCSV != "" {
			return nil, fmt.Errorf("specify events or snapshot_csv, not both")
		}
		if snapshotCSV != "" {
			snapshotListID = stringArg("list_id")
			envelopes, err := slackListSnapshotEnvelopesFromCSV(snapshotListID, snapshotCSV)
			if err != nil {
				return nil, err
			}
			events, err = json.Marshal(envelopes)
			if err != nil {
				return nil, fmt.Errorf("encode Slack List snapshot rows: %w", err)
			}
		}
		if len(events) == 0 {
			return nil, fmt.Errorf("events are required")
		}
		return IngestBridgedCapture(BridgedIngestConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, Source: stringArg("source"), RequestID: stringArg("request_id"), ClaimToken: stringArg("claim_token"), SnapshotListID: snapshotListID, Input: strings.NewReader(string(events))})
	case "capture_request_fail":
		err := FailCaptureRequest(CaptureRequestCapabilityConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, RequestID: stringArg("request_id"), ClaimToken: stringArg("claim_token"), Error: stringArg("error")})
		return map[string]any{"failed": err == nil}, err
	case "capture_watermark":
		base.ConnectorID = stringArg("connector")
		watermark, err := CaptureWatermark(base)
		return map[string]any{"connector": base.ConnectorID, "completed_through": watermark}, err
	case "connector_status":
		status, err := StatusConnectors(ConnectorListConfig{HomeDir: config.HomeDir})
		return map[string]any{"connectors": status.Connectors}, err
	case "connector_required_tools":
		requirements, err := RequiredConnectorTools(stringArg("connector"))
		return map[string]any{"connectors": requirements.Connectors}, err
	case "connector_bridge_configure":
		interval, err := time.ParseDuration(stringArg("interval"))
		if err != nil || interval <= 0 {
			return nil, fmt.Errorf("interval must be a positive Go duration")
		}
		return ConfigureBridgedConnector(ConnectorBridgeConfig{HomeDir: config.HomeDir, ID: stringArg("connector"), Interval: interval, BridgeParams: args["bridge_params"]})
	case "connector_bridge_disconnect":
		return SetConnectorMode(ConnectorModeConfig{HomeDir: config.HomeDir, ID: stringArg("connector"), Mode: "direct"})
	case "bridge_worker_heartbeat":
		worker := stringArg("worker")
		observedAt, err := RecordBridgeWorkerHeartbeat(config.HomeDir, config.DatabasePath, worker)
		return map[string]any{"worker": worker, "observed_at": observedAt}, err
	default:
		return nil, fmt.Errorf("unknown bridge tool %q", name)
	}
}

func RecordBridgeWorkerHeartbeat(homeDir string, databasePath string, worker string) (string, error) {
	worker = strings.TrimSpace(worker)
	if worker == "" {
		return "", fmt.Errorf("worker is required")
	}
	status, err := prepareRunStatus(RunConfig{HomeDir: homeDir, DatabasePath: databasePath})
	if err != nil {
		return "", err
	}
	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return "", fmt.Errorf("open bridge heartbeat database: %w", err)
	}
	defer db.Close()
	if err := createSchema(db); err != nil {
		return "", err
	}
	observedAt := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`INSERT INTO bridge_workers (worker, last_heartbeat_at, details_json)
		VALUES (?, ?, '{}') ON CONFLICT(worker) DO UPDATE SET last_heartbeat_at = excluded.last_heartbeat_at`, worker, observedAt)
	if err != nil {
		return "", fmt.Errorf("record bridge worker heartbeat: %w", err)
	}
	return observedAt, nil
}

func bridgeMCPError(id any, code int, message string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": bridgeMCPErrorData(code, message)}
}

func bridgeMCPErrorData(code int, message string) map[string]any {
	return map[string]any{"code": code, "message": message}
}
