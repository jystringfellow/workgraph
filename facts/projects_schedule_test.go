package facts

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestScheduledAssociateEmitsOneRequestOnlyWhenEligible(t *testing.T) {
	home := initScheduleHome(t)
	configureBridgeAssociateProfile(t, home, "slack")
	schedule := workgraph.LLMAssociateScheduleConfig{HomeDir: home.HomeDir}

	idle, err := workgraph.ScheduleLLMAssociate(schedule)
	if err != nil || idle.Eligible || idle.Created {
		t.Fatalf("expected no request without eligible units, got %+v %v", idle, err)
	}
	if requests := llmAssociateRequests(t, home); len(requests) != 0 {
		t.Fatalf("emitted a request with nothing to categorize: %+v", requests)
	}

	units := insertAssociateThreads(t, home, time.Now().UTC().Add(-6*time.Hour), "C1", "harness-team", 3)
	emitted, err := workgraph.ScheduleLLMAssociate(schedule)
	if err != nil || !emitted.Created || emitted.Executed {
		t.Fatalf("expected one emitted bridge request, got %+v %v", emitted, err)
	}
	if emitted.Request.Kind != "llm_task" || emitted.Request.ConnectorID != "llm.associate" || emitted.Request.Status != "pending" {
		t.Fatalf("unexpected request: %+v", emitted.Request)
	}
	if got := storedLLMRequestUnitIDs(t, home, emitted.Request.ID); !reflect.DeepEqual(got, []string{units[0].ID, units[1].ID, units[2].ID}) {
		t.Fatalf("expected params_json to hold the eligible unit ids, got %v", got)
	}
	coalesced, err := workgraph.ScheduleLLMAssociate(schedule)
	if err != nil || coalesced.Created || !coalesced.Eligible || coalesced.Request.ID != emitted.Request.ID {
		t.Fatalf("expected the next emit to coalesce, got %+v %v", coalesced, err)
	}
	if requests := llmAssociateRequests(t, home); len(requests) != 1 {
		t.Fatalf("expected one active request, got %+v", requests)
	}

	claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{HomeDir: home.HomeDir, Worker: "capture-worker", Max: 10})
	if err != nil || len(claims) != 0 {
		t.Fatalf("a claim without a kind returned categorization work: %+v %v", claims, err)
	}
	output, err := runworkgraph(t, repoRoot(t), "capture", "requests", "--claim", "--home", home.HomeDir, "--worker", "capture-worker", "--claim-file", filepath.Join(t.TempDir(), "claim.json"))
	if err != nil || !strings.Contains(string(output), "No capture requests available.") {
		t.Fatalf("CLI claim without a kind returned categorization work: %v\n%s", err, output)
	}
	for _, arguments := range []map[string]any{{"worker": "capture-worker"}, {"worker": "capture-worker", "connector": "llm.associate"}} {
		content, failure := callScheduleMCP(t, home, "capture_requests_claim", arguments)
		if failure != "" || len(content["claims"].([]any)) != 0 {
			t.Fatalf("MCP claim %v without a kind returned categorization work: %v %s", arguments, content, failure)
		}
	}

	content, failure := callScheduleMCP(t, home, "capture_requests_claim", map[string]any{"worker": "claude-code-drain-fact", "kind": "llm_task"})
	if failure != "" {
		t.Fatalf("claim llm_task: %s", failure)
	}
	claimed := content["claims"].([]any)
	if len(claimed) != 1 {
		t.Fatalf("expected the llm_task request, got %v", content)
	}
	claim := claimed[0].(map[string]any)
	task, _ := claim["task"].(map[string]any)
	if claim["request"].(map[string]any)["id"] != emitted.Request.ID || task == nil || task["client"] != "claude-code" {
		t.Fatalf("expected a categorization task for the emitted request, got %v", claim)
	}
	encodedTask, _ := json.Marshal(task)
	for _, unit := range units {
		if !strings.Contains(string(encodedTask), unit.ID) {
			t.Fatalf("task omitted %s:\n%s", unit.ID, encodedTask)
		}
	}
}

func TestScheduledAssociateRunsDirectlyAsDaemon(t *testing.T) {
	home := initScheduleHome(t)
	fake := newAssociateFakeModel(t)
	configureAssociateProfile(t, home, fake.URL, "slack")
	units := insertAssociateThreads(t, home, time.Now().UTC().Add(-6*time.Hour), "C1", "harness-team", 2)
	fake.Respond(associateResponse(associateEntry(units[0], "project", "ask-kai", 0.99), associateEntry(units[1], "project", "mmo-harness", 0.8)))
	schedule := workgraph.LLMAssociateScheduleConfig{HomeDir: home.HomeDir, HTTPClient: fake.Client}

	ran, err := workgraph.ScheduleLLMAssociate(schedule)
	if err != nil || !ran.Created || !ran.Executed || fake.Calls() != 1 {
		t.Fatalf("expected the daemon to emit and run one request, got %+v calls=%d %v", ran, fake.Calls(), err)
	}
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}
	assertAssociateLink(t, store, units[0].ID, "applied", true)
	assertAssociateLink(t, store, units[1].ID, "proposed", false)
	requests := llmAssociateRequests(t, home)
	if len(requests) != 1 || requests[0].Status != "completed" || requests[0].ClaimedBy != "daemon" || requests[0].Attempts != 1 {
		t.Fatalf("expected a completed request claimed by daemon, got %+v", requests)
	}
	assertNoLLMAssociateCaptureState(t, home)

	idle, err := workgraph.ScheduleLLMAssociate(schedule)
	if err != nil || idle.Eligible || fake.Calls() != 1 {
		t.Fatalf("expected no request once every unit is linked, got %+v calls=%d %v", idle, fake.Calls(), err)
	}

	late := insertAssociateThreads(t, home, time.Now().UTC().Add(-5*time.Hour), "C2", "harness-team", 1)
	fake.Respond("not json at all")
	if _, err := workgraph.ScheduleLLMAssociate(schedule); err == nil {
		t.Fatalf("expected a failed model call to fail the scheduled run")
	}
	requests = llmAssociateRequests(t, home)
	if len(requests) != 2 || requests[1].Status != "pending" || requests[1].Attempts != 1 {
		t.Fatalf("expected the failed request back in pending for retry, got %+v", requests)
	}
	if links, _ := workgraph.ReadProjectLinks(store, workgraph.ProjectLinksQuery{}); len(links) != 2 {
		t.Fatalf("failed run stored links for %s: %+v", late[0].ID, links)
	}
	assertNoLLMAssociateCaptureState(t, home)
}

func TestBridgeAssociateRequestCompletesOnlyWithValidClaimToken(t *testing.T) {
	home := initScheduleHome(t)
	configureBridgeAssociateProfile(t, home, "slack")
	units := insertAssociateThreads(t, home, time.Now().UTC().Add(-6*time.Hour), "C1", "harness-team", 2)
	output, err := runworkgraph(t, repoRoot(t), "llm", "associate", "--home", home.HomeDir, "--memory", home.MemoryDir)
	if err != nil || !strings.Contains(string(output), "LLM associate request emitted") {
		t.Fatalf("expected llm associate to emit a bridge request: %v\n%s", err, output)
	}
	requests := llmAssociateRequests(t, home)
	if len(requests) != 1 || requests[0].Status != "pending" || !strings.Contains(string(output), "Request: "+requests[0].ID) {
		t.Fatalf("expected one pending request named in the output, got %+v\n%s", requests, output)
	}
	emitted := workgraph.LLMAssociateScheduleResult{Request: requests[0]}

	claimFile := filepath.Join(t.TempDir(), "claim.json")
	output, err = runworkgraph(t, repoRoot(t), "capture", "requests", "--claim", "--kind", "llm_task", "--home", home.HomeDir, "--worker", "claude-code-drain-fact", "--claim-file", claimFile)
	if err != nil {
		t.Fatalf("claim llm_task: %v\n%s", err, output)
	}
	for _, expected := range []string{"Request: " + emitted.Request.ID, "Kind: llm_task", "Connector: llm.associate", "Task: ", units[0].ID, "WORKGRAPH_DATA"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("claim output omitted %q:\n%s", expected, output)
		}
	}

	response := associateResponse(associateEntry(units[0], "project", "ask-kai", 0.99), associateEntry(units[1], "project", "mmo-harness", 0.8))
	forged := filepath.Join(t.TempDir(), "forged.json")
	if err := os.WriteFile(forged, []byte(`{"request_id":"`+emitted.Request.ID+`","claim_token":"forged"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := runScheduleSubmit(t, home, emitted.Request.ID, forged, response); err == nil {
		t.Fatalf("accepted a forged claim token:\n%s", output)
	}
	if _, failure := callScheduleMCP(t, home, "llm_task_submit", map[string]any{"request_id": emitted.Request.ID, "claim_token": "forged", "result": json.RawMessage(response)}); failure == "" {
		t.Fatalf("MCP accepted a forged claim token")
	}
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}
	if links, _ := workgraph.ReadProjectLinks(store, workgraph.ProjectLinksQuery{}); len(links) != 0 {
		t.Fatalf("a forged submission stored links: %+v", links)
	}
	if requests := llmAssociateRequests(t, home); requests[0].Status != "claimed" {
		t.Fatalf("a forged submission changed the request: %+v", requests)
	}

	output, err = runScheduleSubmit(t, home, emitted.Request.ID, claimFile, response)
	if err != nil || !strings.Contains(string(output), "Request completed: "+emitted.Request.ID) {
		t.Fatalf("submit with the claim file: %v\n%s", err, output)
	}
	assertAssociateLink(t, store, units[0].ID, "applied", true)
	assertAssociateLink(t, store, units[1].ID, "proposed", false)
	if requests := llmAssociateRequests(t, home); requests[0].Status != "completed" || requests[0].ClaimedBy != "claude-code-drain-fact" {
		t.Fatalf("expected the request completed by the bridge worker, got %+v", requests)
	}
	if output, err := runScheduleSubmit(t, home, emitted.Request.ID, claimFile, response); err == nil {
		t.Fatalf("accepted a second submission with a spent claim token:\n%s", output)
	}
	assertNoLLMAssociateCaptureState(t, home)

	insertAssociateThreads(t, home, time.Now().UTC().Add(-5*time.Hour), "C2", "harness-team", 1)
	stale, err := workgraph.ScheduleLLMAssociate(workgraph.LLMAssociateScheduleConfig{HomeDir: home.HomeDir})
	if err != nil || !stale.Created {
		t.Fatalf("emit second request: %+v %v", stale, err)
	}
	ageLLMAssociateRequest(t, home, stale.Request.ID, 72*time.Hour)
	writeStaleBridgeHeartbeat(t, home.HomeDir, "claude-code", 72*time.Hour)
	for _, command := range [][]string{{"connectors", "status", "--home", home.HomeDir}, {"today", "--home", home.HomeDir}} {
		output, err := runworkgraph(t, repoRoot(t), command...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", command, err, output)
		}
		if strings.Contains(string(output), "llm.associate") || strings.Contains(strings.ToLower(string(output)), "stalled") {
			t.Fatalf("%v reported a categorization request as a bridge stall:\n%s", command, output)
		}
	}
}

func TestScheduledAssociateRechecksSourcePolicyAtClaim(t *testing.T) {
	home := initScheduleHome(t)
	configureBridgeAssociateProfile(t, home, "slack", "slack.dm")
	base := time.Now().UTC().Add(-6 * time.Hour)
	channel := associateSlackEvent("C100", "1760950800.000100", "channel", "harness-team", "Sam", "Agentic tooling over GraphQL for the harness", base)
	dm := associateSlackEvent("D200", "1760950900.000100", "im", "D200", "Robin", "private dm about compensation", base.Add(time.Minute))
	insertEvent(t, home.DatabasePath, channel)
	insertEvent(t, home.DatabasePath, dm)
	channelUnit := workgraph.ProjectUnit{ID: "slack.thread:C100:1760950800.000100", EventIDs: []string{channel.ID}}
	dmUnit := workgraph.ProjectUnit{ID: "slack.thread:D200:1760950900.000100", EventIDs: []string{dm.ID}}

	emitted, err := workgraph.ScheduleLLMAssociate(workgraph.LLMAssociateScheduleConfig{HomeDir: home.HomeDir})
	if err != nil || !emitted.Created {
		t.Fatalf("emit: %+v %v", emitted, err)
	}
	if got := storedLLMRequestUnitIDs(t, home, emitted.Request.ID); !reflect.DeepEqual(got, []string{channelUnit.ID, dmUnit.ID}) {
		t.Fatalf("expected both units scheduled while DMs were enabled, got %v", got)
	}
	if _, err := workgraph.ConfigureLLMSources(workgraph.LLMSourcesConfig{HomeDir: home.HomeDir, Task: "associate", Disable: []string{"slack.dm"}}); err != nil {
		t.Fatal(err)
	}

	content, failure := callScheduleMCP(t, home, "capture_requests_claim", map[string]any{"worker": "claude-code-drain-fact", "kind": "llm_task"})
	if failure != "" || len(content["claims"].([]any)) != 1 {
		t.Fatalf("claim llm_task: %v %s", content, failure)
	}
	encoded, _ := json.Marshal(content)
	if !strings.Contains(string(encoded), channelUnit.ID) {
		t.Fatalf("claimed task omitted the permitted channel unit:\n%s", encoded)
	}
	for _, leaked := range []string{"D200", "compensation", "Robin"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("claimed task leaked %q from a source disabled after emit:\n%s", leaked, encoded)
		}
	}
	claim := content["claims"].([]any)[0].(map[string]any)
	submitted, failure := callScheduleMCP(t, home, "llm_task_submit", map[string]any{
		"request_id":  emitted.Request.ID,
		"claim_token": claim["claim_token"],
		"result":      associateResponse(associateEntry(channelUnit, "project", "ask-kai", 0.8), associateEntry(dmUnit, "project", "ask-kai", 0.8)),
	})
	if failure != "" || submitted["request_completed"] != true {
		t.Fatalf("submit: %v %s", submitted, failure)
	}
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}
	assertAssociateLink(t, store, channelUnit.ID, "proposed", false)
	if links, _ := workgraph.ReadProjectLinks(store, workgraph.ProjectLinksQuery{}); len(links) != 1 {
		t.Fatalf("expected only the channel unit linked, got %+v", links)
	}
}

func initScheduleHome(t *testing.T) associateHome {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	home, memory := filepath.Join(root, "state"), filepath.Join(root, "workgraph-memory")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: home, MemoryDir: memory})
	if err != nil {
		t.Fatal(err)
	}
	writeProjectMemory(t, memory, "mmo-harness", "---\nworkstream: mmo\n---\n# MMO Harness\n\nTest harness.\n")
	writeProjectMemory(t, memory, "ask-kai", "# Ask Kai\n")
	return associateHome{HomeDir: home, MemoryDir: memory, DatabasePath: initialized.DatabasePath}
}

func configureBridgeAssociateProfile(t *testing.T, home associateHome, sources ...string) {
	t.Helper()
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "claude-bridge", Provider: "ai-client", Client: "claude-code", Model: "haiku", Execution: "bridge"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.UseLLMProfile(workgraph.LLMUseProfileConfig{HomeDir: home.HomeDir, Name: "claude-bridge", Task: "associate"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.EnableHostedLLM(workgraph.LLMHostedConfig{HomeDir: home.HomeDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.ConfigureLLMSources(workgraph.LLMSourcesConfig{HomeDir: home.HomeDir, Task: "associate", Enable: sources}); err != nil {
		t.Fatal(err)
	}
}

func llmAssociateRequests(t *testing.T, home associateHome) []workgraph.CaptureRequest {
	t.Helper()
	requests, err := workgraph.ListCaptureRequests(workgraph.CaptureRequestListConfig{HomeDir: home.HomeDir, ConnectorID: "llm.associate"})
	if err != nil {
		t.Fatalf("list llm.associate requests: %v", err)
	}
	return requests
}

func storedLLMRequestUnitIDs(t *testing.T, home associateHome, requestID string) []string {
	t.Helper()
	db, err := sql.Open("sqlite3", home.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var kind, params string
	if err := db.QueryRow(`SELECT kind, params_json FROM capture_requests WHERE id = ?`, requestID).Scan(&kind, &params); err != nil {
		t.Fatalf("read request %s: %v", requestID, err)
	}
	var decoded struct {
		UnitIDs []string `json:"unit_ids"`
	}
	if err := json.Unmarshal([]byte(params), &decoded); err != nil || kind != "llm_task" {
		t.Fatalf("unexpected stored request kind=%s params=%s: %v", kind, params, err)
	}
	return decoded.UnitIDs
}

func ageLLMAssociateRequest(t *testing.T, home associateHome, requestID string, age time.Duration) {
	t.Helper()
	db, err := sql.Open("sqlite3", home.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	created := time.Now().UTC().Add(-age).Format(time.RFC3339Nano)
	if _, err := db.Exec(`UPDATE capture_requests SET created_at = ?, available_at = ? WHERE id = ?`, created, created, requestID); err != nil {
		t.Fatal(err)
	}
}

func writeStaleBridgeHeartbeat(t *testing.T, homeDir string, client string, age time.Duration) {
	t.Helper()
	path := filepath.Join(homeDir, "bridge", client+".heartbeat")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(time.Now().UTC().Add(-age).Format(time.RFC3339Nano)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertNoLLMAssociateCaptureState(t *testing.T, home associateHome) {
	t.Helper()
	db, err := sql.Open("sqlite3", home.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var cursors int
	if err := db.QueryRow(`SELECT COUNT(*) FROM capture_cursors WHERE connector_id = 'llm.associate'`).Scan(&cursors); err != nil || cursors != 0 {
		t.Fatalf("categorization advanced a capture cursor: count=%d %v", cursors, err)
	}
	contents, err := os.ReadFile(filepath.Join(home.HomeDir, "connectors.json"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "llm.associate") {
		t.Fatalf("categorization wrote connector runtime state:\n%s", contents)
	}
}

func runScheduleSubmit(t *testing.T, home associateHome, requestID, claimFile, response string) ([]byte, error) {
	t.Helper()
	command := exec.Command(workgraphFactsBinary, "llm", "associate", "submit", "--home", home.HomeDir, "--request", requestID, "--claim-file", claimFile)
	command.Dir = repoRoot(t)
	command.Stdin = strings.NewReader(response)
	return command.CombinedOutput()
}

func callScheduleMCP(t *testing.T, home associateHome, tool string, arguments map[string]any) (map[string]any, string) {
	t.Helper()
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": arguments},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(workgraphFactsBinary, "bridge", "mcp", "--home", home.HomeDir)
	command.Dir = repoRoot(t)
	command.Stdin = strings.NewReader(string(request) + "\n")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("run MCP %s: %v\n%s", tool, err, stderr.String())
	}
	var response struct {
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &response); err != nil {
		t.Fatalf("decode MCP %s response: %v\n%s", tool, err, output)
	}
	if response.Error != nil {
		encoded, _ := json.Marshal(response.Error)
		return nil, string(encoded)
	}
	if response.Result["isError"] == true {
		encoded, _ := json.Marshal(response.Result["content"])
		return nil, string(encoded)
	}
	content, _ := response.Result["structuredContent"].(map[string]any)
	return content, ""
}
