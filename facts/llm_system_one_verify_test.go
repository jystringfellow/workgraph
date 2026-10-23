package facts

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestSystemOneAssociateEscalatesUncertainUnitsToADirectVerifier(t *testing.T) {
	home := initSystemOneHome(t, true)
	models := newSystemOneWithChat(t)
	configureSystemOneProfile(t, home, "https://decisions.example.net", "", "slack")
	addChatVerifier(t, home, "big-verifier")
	if _, err := workgraph.EnableHostedLLM(workgraph.LLMHostedConfig{HomeDir: home.HomeDir}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	units := insertSystemOneThreads(t, home, now.Add(-6*time.Hour), "confident unit", "uncertain unit with ghp_abcdefghijklmnopqrstuvwxyz0123", "doubtful unit", "invalid unit")
	build := systemOneChoice("build", 1, map[string]any{"build": 1})
	models.systemOne.answer("confident unit", systemOneChoice("p1", 0.97, map[string]any{"p1": 0.97}), build)
	models.systemOne.answer("uncertain unit", systemOneChoice("p2", 0.8, map[string]any{"p2": 0.8, "no_project": 0.2}), build)
	models.systemOne.answer("doubtful unit", systemOneChoice("p1", 0.4, map[string]any{"p1": 0.4}), build)
	models.systemOne.answer("invalid unit", systemOneChoice("p1", 0.8, map[string]any{"p1": 0.8}), systemOneChoice("gossip", 1, map[string]any{"gossip": 1}))
	verdict := associateEntry(units[1], "project", "atlas-bot", 0.96)
	verdict["summary"] = "Verifier picked atlas"
	models.respondChat(associateResponse(verdict))

	result, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: models.client, Now: now})
	if err != nil {
		t.Fatalf("associate: %v", err)
	}
	if calls := len(models.systemOne.received()); calls != 4 || models.chatCalls() != 1 {
		t.Fatalf("expected four System One calls and one verifier call, got %d and %d", calls, models.chatCalls())
	}
	for _, expected := range []string{"Verifier: big-verifier, confidence 0.60 to below 0.95", "Escalated: 1", "Verified: 1 by big-verifier (1 changed)", "Applied: 2", "Proposed: 1", "Discarded: 1"} {
		if !strings.Contains(result.Message, expected) {
			t.Fatalf("run report omitted %q:\n%s", expected, result.Message)
		}
	}
	request := models.lastChat()
	for _, expected := range []string{units[1].ID, "primary_answer", "[REDACTED:github-token]", "harbor-harness"} {
		if !strings.Contains(request, expected) {
			t.Fatalf("verifier request omitted %q:\n%s", expected, request)
		}
	}
	for _, leaked := range []string{"ghp_abcdefghijklmnopqrstuvwxyz0123", units[0].ID, units[2].ID, units[3].ID, "confident unit", "doubtful unit", "invalid unit"} {
		if strings.Contains(request, leaked) {
			t.Fatalf("verifier request carried %q:\n%s", leaked, request)
		}
	}

	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}
	if link := currentProjectLink(t, store, units[0].ID); link.State != "applied" || link.Profile != "decisions" || link.Confidence != 0.97 {
		t.Fatalf("expected the confident unit applied by the system-one profile, got %+v", link)
	}
	verified := currentProjectLink(t, store, units[1].ID)
	if verified.State != "applied" || verified.Profile != "big-verifier" || verified.Project != "atlas-bot" || verified.Summary != "Verifier picked atlas" {
		t.Fatalf("expected the verifier's answer stored under its profile, got %+v", verified)
	}
	if link := currentProjectLink(t, store, units[2].ID); link.State != "proposed" || link.Profile != "decisions" {
		t.Fatalf("expected the unit below the floor proposed by the system-one profile, got %+v", link)
	}
	if links, _ := workgraph.ReadProjectLinks(store, workgraph.ProjectLinksQuery{UnitID: units[3].ID}); len(links) != 0 {
		t.Fatalf("a discarded unit was stored or escalated: %+v", links)
	}
}

func TestSystemOneAssociateEmitsAVerifyRequestForABridgeVerifier(t *testing.T) {
	for _, path := range []string{"direct", "daemon"} {
		t.Run(path, func(t *testing.T) {
			home := initSystemOneHome(t, true)
			fake := newSystemOneFake(t)
			configureSystemOneProfile(t, home, "http://127.0.0.1:8000", "", "slack")
			configureBridgeVerifier(t, home, "codex-verify")
			units := insertSystemOneThreads(t, home, time.Now().UTC().Add(-6*time.Hour), "confident unit", "uncertain unit")
			build := systemOneChoice("build", 1, map[string]any{"build": 1})
			fake.answer("confident unit", systemOneChoice("p1", 0.99, map[string]any{"p1": 0.99}), build)
			fake.answer("uncertain unit", systemOneChoice("p2", 0.8, map[string]any{"p2": 0.8}), build)

			var message string
			if path == "direct" {
				result, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.client})
				if err != nil {
					t.Fatalf("associate: %v", err)
				}
				message = result.Message
			} else {
				scheduled, err := workgraph.ScheduleLLMAssociate(workgraph.LLMAssociateScheduleConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.client})
				if err != nil || !scheduled.Executed {
					t.Fatalf("expected the daemon to run the system-one request, got %+v %v", scheduled, err)
				}
				message = scheduled.Run.Message
			}
			if !strings.Contains(message, "Verify request emitted: ") || len(fake.received()) != 2 {
				t.Fatalf("expected a verify request after two System One calls:\n%s", message)
			}
			store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}
			if link := currentProjectLink(t, store, units[0].ID); link.State != "applied" || link.Profile != "decisions" {
				t.Fatalf("expected the confident unit applied, got %+v", link)
			}
			if links, _ := workgraph.ReadProjectLinks(store, workgraph.ProjectLinksQuery{UnitID: units[1].ID}); len(links) != 0 {
				t.Fatalf("expected the escalated unit to wait for the verifier, got %+v", links)
			}
			var verifyID string
			for _, request := range llmAssociateRequests(t, home) {
				if request.Status == "pending" && storedLLMRequestTask(t, home, request.ID) == "associate-verify" {
					verifyID = request.ID
				}
			}
			if verifyID == "" {
				t.Fatalf("no pending associate-verify request: %+v", llmAssociateRequests(t, home))
			}
			if got := storedLLMRequestUnitIDs(t, home, verifyID); !reflect.DeepEqual(got, []string{units[1].ID}) {
				t.Fatalf("expected only the escalated unit in the verify request, got %v", got)
			}
		})
	}
}

func TestSystemOneProfilesCannotVerify(t *testing.T) {
	home := initSystemOneHome(t, true)
	models := newSystemOneWithChat(t)
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "chat-associate", Provider: "openai-compatible", BaseURL: "http://127.0.0.1:9/v1", Model: "small-model"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.UseLLMProfile(workgraph.LLMUseProfileConfig{HomeDir: home.HomeDir, Name: "chat-associate", Task: "associate"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.ConfigureLLMSources(workgraph.LLMSourcesConfig{HomeDir: home.HomeDir, Task: "associate", Enable: []string{"slack"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "decider", Provider: "system-one", BaseURL: "http://127.0.0.1:8000", Model: "clef-flash"}); err != nil {
		t.Fatal(err)
	}
	if output, err := runworkgraph(t, repoRoot(t), "llm", "use", "decider", "--for", "associate-verify", "--home", home.HomeDir); err == nil || !strings.Contains(string(output), "system-one profiles cannot verify yet") {
		t.Fatalf("expected a system-one verifier route to be refused: %v\n%s", err, output)
	}

	path := filepath.Join(home.HomeDir, "llm.json")
	var stored map[string]any
	contents, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(contents, &stored) != nil {
		t.Fatalf("read llm.json: %v", err)
	}
	stored["task_profiles"].(map[string]any)["associate-verify"] = "decider"
	edited, _ := json.MarshalIndent(stored, "", "  ")
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	units := insertSystemOneThreads(t, home, time.Now().UTC().Add(-6*time.Hour), "uncertain unit")
	models.respondChat(associateResponse(associateEntry(units[0], "project", "harbor-harness", 0.8)))

	result, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: models.client})
	if err != nil {
		t.Fatalf("expected the run to succeed when the hand-routed verifier is refused: %v", err)
	}
	if !strings.Contains(result.Message, "Escalated: 1") || !strings.Contains(result.Message, "Verifier failed: system-one profiles cannot verify yet") {
		t.Fatalf("expected the refusal in the run report:\n%s", result.Message)
	}
	if calls := len(models.systemOne.received()); calls != 0 || models.chatCalls() != 1 {
		t.Fatalf("expected only the associate call, got system-one=%d chat=%d", calls, models.chatCalls())
	}
	if link := currentProjectLink(t, workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}, units[0].ID); link.State != "proposed" || link.Profile != "chat-associate" || link.Project != "harbor-harness" {
		t.Fatalf("expected the associate result kept, got %+v", link)
	}
}

func TestSystemOneProfileThresholdControlsAutoApply(t *testing.T) {
	home := initSystemOneHome(t, true)
	fake := newSystemOneFake(t)
	args := []string{"--home", home.HomeDir}
	if output, err := runworkgraph(t, repoRoot(t), append([]string{"llm", "add", "decisions", "--provider", "system-one", "--base-url", "http://127.0.0.1:8000", "--model", "clef-flash", "--verify-floor", "0.5"}, args...)...); err != nil {
		t.Fatalf("llm add system-one with a verify floor: %v\n%s", err, output)
	}
	configureSystemOneRoute(t, home, "slack")
	now := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	config := workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.client, Now: now}
	build := systemOneChoice("build", 1, map[string]any{"build": 1})
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}

	first := insertSystemOneThreads(t, home, now.Add(-6*time.Hour), "first unit")
	fake.answer("first unit", systemOneChoice("p1", 0.92, map[string]any{"p1": 0.92}), build)
	if result, err := workgraph.AssociateWithLLM(config); err != nil || result.Proposed != 1 || result.Applied != 0 {
		t.Fatalf("expected 0.92 to be proposed at the default threshold, got %+v %v", result, err)
	}

	if output, err := runworkgraph(t, repoRoot(t), append([]string{"llm", "set", "decisions", "--auto-apply-confidence", "0.9"}, args...)...); err != nil {
		t.Fatalf("llm set: %v\n%s", err, output)
	}
	output, err := runworkgraph(t, repoRoot(t), append([]string{"llm", "list"}, args...)...)
	if err != nil || !strings.Contains(string(output), "- decisions: system-one clef-flash host http://127.0.0.1:8000 auto-apply 0.90, verify-floor 0.50") {
		t.Fatalf("expected llm list to show the host and thresholds: %v\n%s", err, output)
	}
	second := insertSystemOneThreads(t, home, now.Add(-5*time.Hour), "second unit")
	fake.answer("second unit", systemOneChoice("p1", 0.92, map[string]any{"p1": 0.92}), build)
	if result, err := workgraph.AssociateWithLLM(config); err != nil || result.Applied != 1 || result.Proposed != 0 {
		t.Fatalf("expected 0.92 to auto-apply at the profile threshold 0.90, got %+v %v", result, err)
	}
	if link := currentProjectLink(t, store, first[0].ID); link.State != "proposed" || link.Profile != "decisions" {
		t.Fatalf("expected the earlier unit to stay proposed, got %+v", link)
	}
	if link := currentProjectLink(t, store, second[0].ID); link.State != "applied" || !link.Auto || link.Profile != "decisions" || link.Confidence != 0.92 {
		t.Fatalf("expected the later unit applied under the system-one profile, got %+v", link)
	}
}

func addChatVerifier(t *testing.T, home associateHome, name string) {
	t.Helper()
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: name, Provider: "openai-compatible", BaseURL: "http://127.0.0.1:9/v1", Model: "big-model"}); err != nil {
		t.Fatal(err)
	}
	if output, err := runworkgraph(t, repoRoot(t), "llm", "use", name, "--for", "associate-verify", "--home", home.HomeDir); err != nil {
		t.Fatalf("route the verifier: %v\n%s", err, output)
	}
}

func insertSystemOneThreads(t *testing.T, home associateHome, start time.Time, summaries ...string) []workgraph.ProjectUnit {
	t.Helper()
	var units []workgraph.ProjectUnit
	for index, summary := range summaries {
		ts := fmt.Sprintf("%d.000100", start.Unix()+int64(index))
		event := associateSlackEvent("C1", ts, "channel", systemOneContainer, "Sam", summary, start.Add(time.Duration(index)*time.Minute))
		insertEvent(t, home.DatabasePath, event)
		units = append(units, workgraph.ProjectUnit{ID: "slack.thread:C1:" + ts, EventIDs: []string{event.ID}})
	}
	return units
}

type systemOneWithChat struct {
	systemOne *systemOneFake
	client    *http.Client
	mu        sync.Mutex
	calls     int
	last      string
	response  string
}

func newSystemOneWithChat(t *testing.T) *systemOneWithChat {
	t.Helper()
	models := &systemOneWithChat{systemOne: newSystemOneFake(t)}
	models.client = &http.Client{Transport: associateRoundTripper(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/systemone") {
			return models.systemOne.roundTrip(request)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		models.mu.Lock()
		defer models.mu.Unlock()
		models.calls++
		models.last = string(body)
		recorder := httptest.NewRecorder()
		_ = json.NewEncoder(recorder).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": models.response}}}})
		return recorder.Result(), nil
	})}
	return models
}

func (models *systemOneWithChat) respondChat(response string) {
	models.mu.Lock()
	defer models.mu.Unlock()
	models.response = response
}

func (models *systemOneWithChat) chatCalls() int {
	models.mu.Lock()
	defer models.mu.Unlock()
	return models.calls
}

func (models *systemOneWithChat) lastChat() string {
	models.mu.Lock()
	defer models.mu.Unlock()
	return models.last
}
