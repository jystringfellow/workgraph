package facts

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestReroutedVerifierStillPassesOutboundPolicy(t *testing.T) {
	home := initScheduleHome(t)
	writeInventedProjects(t, home)
	models := newRoutedAssociateModels(t)
	configureAssociateProfile(t, home, models.URL, "slack")
	configureBridgeVerifier(t, home, "codex-verify")
	units := insertAssociateThreads(t, home, time.Now().UTC().Add(-6*time.Hour), "C1", "harness-team", 1)
	models.Respond("small-model", associateResponse(associateEntry(units[0], "project", "harbor-harness", 0.8)))
	schedule := workgraph.LLMAssociateScheduleConfig{HomeDir: home.HomeDir, HTTPClient: models.Client}
	if ran, err := workgraph.ScheduleLLMAssociate(schedule); err != nil || !strings.Contains(ran.Run.Message, "Verify request emitted: ") {
		t.Fatalf("expected a pending verify request, got %+v %v", ran, err)
	}

	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "hosted-verify", Provider: "openai-compatible", BaseURL: "https://models.example.test/v1", Model: "hosted-model"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.UseLLMProfile(workgraph.LLMUseProfileConfig{HomeDir: home.HomeDir, Name: "hosted-verify", Task: "associate-verify"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.DisableHostedLLM(workgraph.LLMHostedConfig{HomeDir: home.HomeDir}); err != nil {
		t.Fatal(err)
	}
	ran, err := workgraph.ScheduleLLMAssociate(schedule)
	if err != nil || !ran.Executed {
		t.Fatalf("expected the daemon to run the rerouted verify request, got %+v %v", ran, err)
	}
	if models.Calls("hosted-model") != 0 {
		t.Fatalf("a verifier without hosted opt-in received %d requests", models.Calls("hosted-model"))
	}
	if !strings.Contains(ran.Run.Message, "Verifier failed: hosted LLM use is not enabled") {
		t.Fatalf("expected the run to say why the verifier was not called:\n%s", ran.Run.Message)
	}
	if link := currentProjectLink(t, workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}, units[0].ID); link.State != "proposed" || link.Profile != "local-associate" {
		t.Fatalf("expected the associate result kept, got %+v", link)
	}
}

func TestDeferredVerifyUsesTheAssociateSnapshot(t *testing.T) {
	home := initScheduleHome(t)
	writeInventedProjects(t, home)
	fake := newAssociateFakeModel(t)
	configureAssociateProfile(t, home, fake.URL, "slack")
	configureBridgeVerifier(t, home, "codex-verify")
	start := time.Now().UTC().Add(-6 * time.Hour)
	units := insertAssociateThreads(t, home, start, "C1", "harness-team", 1)
	fake.Respond(associateResponse(associateEntry(units[0], "project", "harbor-harness", 0.8)))
	if ran, err := workgraph.ScheduleLLMAssociate(workgraph.LLMAssociateScheduleConfig{HomeDir: home.HomeDir, HTTPClient: fake.Client}); err != nil || !strings.Contains(ran.Run.Message, "Verify request emitted: ") {
		t.Fatalf("expected a pending verify request, got %+v %v", ran, err)
	}

	writeProjectMemory(t, home.MemoryDir, "late-project", "# Late Project\n")
	decided := insertAssociateThreads(t, home, start.Add(time.Hour), "C8", "decided-later", 1)
	if _, err := workgraph.AssignProjectLink(workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}, workgraph.ProjectLinkAssignment{UnitID: decided[0].ID, UnitKind: "slack.thread", Container: "decided-later", Evidence: decided[0].EventIDs, Target: "project", Project: "atlas-bot", Summary: "Decided later"}); err != nil {
		t.Fatal(err)
	}
	insertAssociateThreads(t, home, start.Add(2*time.Hour), "C9", "harness-team", 1)
	dryRun, err := runworkgraph(t, repoRoot(t), "llm", "associate", "--dry-run", "--home", home.HomeDir, "--memory", home.MemoryDir)
	if err != nil || !strings.Contains(string(dryRun), "late-project") || !strings.Contains(string(dryRun), "decided-later") {
		t.Fatalf("expected a fresh payload to carry the later catalog entry and decision: %v\n%s", err, dryRun)
	}

	content, failure := callScheduleMCP(t, home, "capture_requests_claim", map[string]any{"worker": "codex-drain-fact", "kind": "llm_task"})
	if failure != "" || len(content["claims"].([]any)) != 1 {
		t.Fatalf("claim the verify request: %v %s", content, failure)
	}
	task, _ := json.Marshal(content["claims"].([]any)[0].(map[string]any)["task"])
	if !strings.Contains(string(task), units[0].ID) || !strings.Contains(string(task), "primary_answer") {
		t.Fatalf("expected the escalated unit in the verify task: %s", task)
	}
	for _, unseen := range []string{"late-project", "decided-later"} {
		if strings.Contains(string(task), unseen) {
			t.Fatalf("verify task carried %q, which the associate model never saw: %s", unseen, task)
		}
	}
}

func TestBridgeSubmitEscalatesOnlyWhatTheAssociateSaw(t *testing.T) {
	home := initScheduleHome(t)
	writeInventedProjects(t, home)
	configureBridgeAssociateProfile(t, home, "slack")
	verifier := newRecordingVerifier(t)
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "local-verify", Provider: "openai-compatible", BaseURL: verifier.URL + "/v1", Model: "local-verify-model"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.UseLLMProfile(workgraph.LLMUseProfileConfig{HomeDir: home.HomeDir, Name: "local-verify", Task: "associate-verify"}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-6 * time.Hour)
	units := insertAssociateThreads(t, home, start, "C1", "harness-team", 2)
	emitted, err := workgraph.ScheduleLLMAssociate(workgraph.LLMAssociateScheduleConfig{HomeDir: home.HomeDir})
	if err != nil || !emitted.Created {
		t.Fatalf("emit: %+v %v", emitted, err)
	}
	claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{HomeDir: home.HomeDir, Kind: "llm_task", Worker: "claude-code-drain-fact", MemoryDir: home.MemoryDir})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim: %+v %v", claims, err)
	}

	writeProjectMemory(t, home.MemoryDir, "late-project", "# Late Project\n")
	insertEvent(t, home.DatabasePath, associateThreadReply(start.Add(time.Second), "C1", "a reply after the claim"))
	response := associateResponse(associateEntry(units[0], "project", "harbor-harness", 0.8), associateEntry(units[1], "project", "harbor-harness", 0.8))
	result, err := workgraph.SubmitLLMTask(workgraph.LLMTaskSubmitConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, RequestID: claims[0].Request.ID, ClaimToken: claims[0].ClaimToken, Response: response})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	requests := verifier.Requests()
	if len(requests) != 1 || !strings.Contains(requests[0], units[0].ID) {
		t.Fatalf("expected one verifier request for the unchanged unit, got %d:\n%s", len(requests), strings.Join(requests, "\n"))
	}
	for _, unseen := range []string{units[1].ID, "late-project", "a reply after the claim"} {
		if strings.Contains(requests[0], unseen) {
			t.Fatalf("verifier request carried %q, which the associate model never saw:\n%s", unseen, requests[0])
		}
	}
	if result.Escalated != 1 {
		t.Fatalf("expected only the unchanged unit escalated, got %+v", result)
	}
	if link := currentProjectLink(t, workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}, units[1].ID); link.State != "proposed" || link.Profile != "claude-bridge" {
		t.Fatalf("expected the changed unit to keep the associate result, got %+v", link)
	}
}

func TestVerifySubmitRejectsARouteChangedAfterTheClaim(t *testing.T) {
	home := initScheduleHome(t)
	writeInventedProjects(t, home)
	fake := newAssociateFakeModel(t)
	configureAssociateProfile(t, home, fake.URL, "slack")
	configureBridgeVerifier(t, home, "codex-verify")
	units := insertAssociateThreads(t, home, time.Now().UTC().Add(-6*time.Hour), "C1", "harness-team", 1)
	fake.Respond(associateResponse(associateEntry(units[0], "project", "harbor-harness", 0.8)))
	if ran, err := workgraph.ScheduleLLMAssociate(workgraph.LLMAssociateScheduleConfig{HomeDir: home.HomeDir, HTTPClient: fake.Client}); err != nil || !strings.Contains(ran.Run.Message, "Verify request emitted: ") {
		t.Fatalf("expected a pending verify request, got %+v %v", ran, err)
	}
	claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{HomeDir: home.HomeDir, Kind: "llm_task", Worker: "codex-drain-fact", MemoryDir: home.MemoryDir})
	if err != nil || len(claims) != 1 || claims[0].Task == nil || claims[0].Task.Profile != "codex-verify" {
		t.Fatalf("claim: %+v %v", claims, err)
	}

	loose := 0.5
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "codex-loose", Provider: "ai-client", Client: "codex", Model: "gpt-loose", Execution: "bridge", AutoApplyConfidence: &loose}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.UseLLMProfile(workgraph.LLMUseProfileConfig{HomeDir: home.HomeDir, Name: "codex-loose", Task: "associate-verify"}); err != nil {
		t.Fatal(err)
	}
	verdict := associateResponse(associateEntry(units[0], "project", "atlas-bot", 0.6))
	if _, err := workgraph.SubmitLLMTask(workgraph.LLMTaskSubmitConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, RequestID: claims[0].Request.ID, ClaimToken: claims[0].ClaimToken, Response: verdict}); err == nil || !strings.Contains(err.Error(), "route changed") {
		t.Fatalf("expected the submission to be refused after the route changed, got %v", err)
	}
	if links, _ := workgraph.ReadProjectLinks(workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}, workgraph.ProjectLinksQuery{UnitID: units[0].ID}); len(links) != 0 {
		t.Fatalf("a refused submission stored links: %+v", links)
	}
	for _, request := range llmAssociateRequests(t, home) {
		if request.ID == claims[0].Request.ID && request.Status != "claimed" {
			t.Fatalf("a refused submission changed the request: %+v", request)
		}
	}
}

func configureBridgeVerifier(t *testing.T, home associateHome, name string) {
	t.Helper()
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: name, Provider: "ai-client", Client: "codex", Model: "gpt-verify", Execution: "bridge"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.UseLLMProfile(workgraph.LLMUseProfileConfig{HomeDir: home.HomeDir, Name: name, Task: "associate-verify"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.EnableHostedLLM(workgraph.LLMHostedConfig{HomeDir: home.HomeDir}); err != nil {
		t.Fatal(err)
	}
}

type recordingVerifier struct {
	URL      string
	mu       sync.Mutex
	requests []string
}

func newRecordingVerifier(t *testing.T) *recordingVerifier {
	t.Helper()
	verifier := &recordingVerifier{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		verifier.mu.Lock()
		verifier.requests = append(verifier.requests, string(body))
		verifier.mu.Unlock()
		_ = json.NewEncoder(writer).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": `{"units":[]}`}}}})
	}))
	t.Cleanup(server.Close)
	verifier.URL = server.URL
	return verifier
}

func (verifier *recordingVerifier) Requests() []string {
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	return append([]string(nil), verifier.requests...)
}

func writeInventedProjects(t *testing.T, home associateHome) {
	t.Helper()
	writeProjectMemory(t, home.MemoryDir, "harbor-harness", "# Harbor Harness\n")
	writeProjectMemory(t, home.MemoryDir, "atlas-bot", "# Atlas Bot\n")
}
