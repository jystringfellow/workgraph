package facts

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

const associateContainer = "Mindbody.Modernization.Orchestration"

func TestAssociateDryRunNarrowsCatalogAndRespectsSourceOptIn(t *testing.T) {
	home := initAssociateHome(t)
	fake := newAssociateFakeModel(t)
	base := time.Now().UTC().Add(-6 * time.Hour)
	insertEvent(t, home.DatabasePath, associateSlackEvent("C100", "1760950800.000100", "channel", associateContainer, "Sam", "Agentic tooling over GraphQL for the harness", base))
	insertEvent(t, home.DatabasePath, associateSlackEvent("D200", "1760950900.000100", "im", "D200", "Robin", "private dm about compensation", base.Add(time.Minute)))
	insertEvent(t, home.DatabasePath, associateSlackEvent("C900", "1760951000.000100", "", "legacy-channel", "Kai", "legacy capture without a conversation type", base.Add(2*time.Minute)))
	insertEvent(t, home.DatabasePath, storedEvent{ID: "github.pull_request:acme/harness:42", Source: "github", Type: "github.pull_request", Timestamp: base.Add(3 * time.Minute), Project: "harness", Actor: "octo", Summary: "Add harness runner", Payload: `{"repository":"acme/harness","number":42,"title":"Add harness runner"}`})
	insertEvent(t, home.DatabasePath, associateSlackEvent("C300", "1760951100.000100", "channel", "harness-team", "Sam", "decided channel unit", base.Add(4*time.Minute)))
	insertEvent(t, home.DatabasePath, associateSlackEvent("D400", "1760951200.000100", "im", "D400", "Robin", "decided dm unit", base.Add(5*time.Minute)))
	insertEvent(t, home.DatabasePath, associateSlackEvent("C700", "1760951300.000100", "", "legacy-root-name", "Kai", "legacy root of a thread", base.Add(6*time.Minute)))
	reply := associateSlackEvent("C700", "1760951400.000100", "channel", associateContainer, "Sam", "channel reply in a legacy thread", base.Add(7*time.Minute))
	reply.ID, reply.Type = "slack.thread_reply:C700:1760951400.000100", "slack.thread_reply"
	reply.Payload = strings.Replace(reply.Payload, `"thread_ts":"1760951400.000100"`, `"thread_ts":"1760951300.000100"`, 1)
	insertEvent(t, home.DatabasePath, reply)

	args := []string{"--home", home.HomeDir, "--memory", home.MemoryDir}
	if output, err := runworkgraph(t, repoRoot(t), append(append([]string{"memory", "link"}, args...), "--workstream", "mmo", "--source", associateContainer)...); err != nil {
		t.Fatalf("workstream link: %v\n%s", err, output)
	}
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}
	for _, decided := range []struct {
		unit, container string
		evidence        []string
	}{
		{"slack.thread:C300:1760951100.000100", "harness-team", []string{"slack.message:C300:1760951100.000100"}},
		{"slack.thread:D400:1760951200.000100", "D400", []string{"slack.message:D400:1760951200.000100"}},
		{"slack.thread:MIXED:1", "mixed", []string{"slack.message:D200:1760950900.000100", "slack.message:C300:1760951100.000100"}},
	} {
		if _, err := workgraph.AssignProjectLink(store, workgraph.ProjectLinkAssignment{UnitID: decided.unit, UnitKind: "slack.thread", Container: decided.container, Evidence: decided.evidence, Target: "project", Project: "ask-kai", Summary: "Decided " + decided.container + " private salary detail"}); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}
	if output, err := runworkgraph(t, repoRoot(t), "llm", "add", "local-associate", "--home", home.HomeDir, "--provider", "openai-compatible", "--base-url", fake.URL+"/v1", "--model", "small-model"); err != nil {
		t.Fatalf("llm add: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot(t), "llm", "use", "local-associate", "--home", home.HomeDir, "--for", "categorize"); err != nil {
		t.Fatalf("llm use categorize: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot(t), "llm", "sources", "--for", "associate", "--home", home.HomeDir)
	if err != nil || !strings.Contains(string(output), "Enabled: none") {
		t.Fatalf("expected no source enabled by default: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot(t), "llm", "sources", "--for", "associate", "--home", home.HomeDir, "--enable", "email"); err == nil {
		t.Fatalf("accepted an unknown source: %s", output)
	}
	dryRun := append([]string{"llm", "associate", "--dry-run"}, args...)
	output, err = runworkgraph(t, repoRoot(t), dryRun...)
	if err != nil {
		t.Fatalf("dry run with no sources: %v\n%s", err, output)
	}
	if payload := associateDryRunPayload(t, string(output)); len(payload.Units) != 0 || len(payload.Decisions) != 0 {
		t.Fatalf("expected an empty payload with no sources enabled, got %+v", payload)
	}

	if output, err := runworkgraph(t, repoRoot(t), "llm", "sources", "--for", "associate", "--home", home.HomeDir, "--enable", "slack"); err != nil || !strings.Contains(string(output), "Enabled: slack\n") {
		t.Fatalf("enable slack: %v\n%s", err, output)
	}
	output, err = runworkgraph(t, repoRoot(t), dryRun...)
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, output)
	}
	for _, expected := range []string{"LLM associate dry run", "Profile: local-associate", "Model: small-model", "Destination: " + fake.URL + "/v1", "Enabled sources: slack"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("dry run omitted %q:\n%s", expected, output)
		}
	}
	for _, leaked := range []string{"compensation", "decided dm unit", "legacy capture", "Add harness runner", "D200", "C900", "private salary detail", "legacy-root-name", "legacy root of a thread", "1760951300"} {
		if strings.Contains(string(output), leaked) {
			t.Fatalf("dry run leaked %q from a disabled source:\n%s", leaked, output)
		}
	}
	payload := associateDryRunPayload(t, string(output))
	if len(payload.Units) != 2 || payload.Units[0].UnitID != "slack.thread:C100:1760950800.000100" {
		t.Fatalf("expected the channel unit and the legacy thread's channel reply, got %v", associatePayloadUnitIDs(payload))
	}
	legacy := payload.Units[1]
	if strings.HasPrefix(legacy.UnitID, "slack.thread:") || legacy.Container != associateContainer || len(legacy.Events) != 1 || legacy.Events[0].ID != reply.ID {
		t.Fatalf("expected the legacy thread to carry only its permitted reply under an opaque id, got %+v", legacy)
	}
	unit := payload.Units[0]
	if unit.Container != associateContainer || !reflect.DeepEqual(unit.Participants, []string{"Sam"}) || len(unit.Events) != 1 || unit.Events[0].Summary != "Agentic tooling over GraphQL for the harness" {
		t.Fatalf("unexpected unit payload: %+v", unit)
	}
	if !reflect.DeepEqual(unit.Projects, []string{"mmo-gtm-segments", "mmo-harness"}) || !reflect.DeepEqual(associatePayloadProjectSlugs(payload), []string{"mmo-gtm-segments", "mmo-harness"}) {
		t.Fatalf("expected the mmo workstream catalog only, got unit %v payload %v", unit.Projects, associatePayloadProjectSlugs(payload))
	}
	if len(payload.Decisions) != 1 || payload.Decisions[0].Container != "harness-team" || payload.Decisions[0].Project != "ask-kai" || payload.Decisions[0].Decision != "chose" {
		t.Fatalf("expected only the channel decision as context, got %+v", payload.Decisions)
	}

	if output, err := runworkgraph(t, repoRoot(t), "llm", "sources", "--for", "associate", "--home", home.HomeDir, "--enable", "slack.dm"); err != nil || !strings.Contains(string(output), "Enabled: slack, slack.dm") {
		t.Fatalf("enable slack.dm: %v\n%s", err, output)
	}
	output, err = runworkgraph(t, repoRoot(t), dryRun...)
	if err != nil {
		t.Fatalf("dry run with dms: %v\n%s", err, output)
	}
	payload = associateDryRunPayload(t, string(output))
	if got := associatePayloadUnitIDs(payload); !reflect.DeepEqual(got, []string{"slack.thread:C100:1760950800.000100", "slack.thread:C700:1760951300.000100", "slack.thread:C900:1760951000.000100", "slack.thread:D200:1760950900.000100"}) {
		t.Fatalf("expected DM and unknown-type Slack units once slack.dm is enabled, got %v", got)
	}
	if len(payload.Decisions) != 3 {
		t.Fatalf("expected every decision once slack.dm is enabled, got %+v", payload.Decisions)
	}
	if fake.Calls() != 0 {
		t.Fatalf("dry run called the model %d times", fake.Calls())
	}
	links, err := workgraph.ReadProjectLinks(store, workgraph.ProjectLinksQuery{})
	if err != nil || len(links) != 3 {
		t.Fatalf("dry run changed project links: %+v %v", links, err)
	}
}

func TestAssociateDiscardsInvalidEntriesAndStoresValidOnes(t *testing.T) {
	home := initAssociateHome(t)
	fake := newAssociateFakeModel(t)
	configureAssociateProfile(t, home, fake.URL, "slack")
	now := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	units := insertAssociateThreads(t, home, now.Add(-6*time.Hour), "C1", "harness-team", 11)

	fake.Respond("not json at all")
	if _, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.Client, Now: now}); err == nil {
		t.Fatalf("expected non-JSON output to fail the run")
	}
	if links, _ := workgraph.ReadProjectLinks(workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}, workgraph.ProjectLinksQuery{}); len(links) != 0 {
		t.Fatalf("non-JSON output stored links: %+v", links)
	}
	oversized := associateEntry(units[5], "project", "ask-kai", 0.8)
	oversized["summary"] = strings.Repeat("x", 1<<20)
	fake.Respond(associateResponse(oversized))
	if _, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.Client, Now: now}); err == nil {
		t.Fatalf("expected an oversized response to fail the run")
	}
	if links, _ := workgraph.ReadProjectLinks(workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}, workgraph.ProjectLinksQuery{}); len(links) != 0 {
		t.Fatalf("oversized output stored links: %+v", links)
	}
	nullConfidence := associateEntry(units[7], "project", "ask-kai", 0.5)
	nullConfidence["confidence"] = nil
	missingConfidence := associateEntry(units[8], "project", "ask-kai", 0.5)
	delete(missingConfidence, "confidence")
	newProject := associateEntry(units[10], "new_project", "", 0.5)
	newProject["new_project"] = map[string]string{"slug": "graphql-tooling", "name": "GraphQL\x1b[31m tooling\n\u202eevil"}

	fake.Respond("```json\n" + associateResponse(
		associateEntry(units[0], "project", "no-such-project", 0.9),
		associateEntry(workgraph.ProjectUnit{ID: "slack.thread:C1:\x1b[2Jforged", EventIDs: []string{"slack.message:C1:9999999999.000100"}}, "project", "ask-kai", 0.9),
		withEvidence(associateEntry(units[1], "project", "ask-kai", 0.9), units[2].EventIDs[0]),
		withKind(associateEntry(units[2], "project", "ask-kai", 0.9), "gossip"),
		associateEntry(units[3], "project", "ask-kai", 1.5),
		associateEntry(units[4], "none", "", 0.5),
		associateEntry(units[4], "project", "ask-kai", 0.5),
		associateEntry(units[5], "project", "ask-kai", 0.8),
		associateEntry(units[6], "project", "ask-kai", 0.99),
		map[string]any{"unit_id": units[6].ID, "evidence": 123},
		nullConfidence,
		missingConfidence,
		withEvidence(associateEntry(units[9], "project", "ask-kai", 0.99), units[9].EventIDs[0], units[9].EventIDs[0]),
		newProject,
	) + "\n```")
	result, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.Client, Now: now})
	if err != nil {
		t.Fatalf("associate: %v", err)
	}
	if fake.Calls() != 3 || len(result.Sent) != 11 {
		t.Fatalf("expected one request per run with eleven units, got calls=%d sent=%v", fake.Calls(), result.Sent)
	}
	if len(result.Discarded) != 12 || result.Applied != 0 || result.Proposed != 2 {
		t.Fatalf("expected twelve discarded entries and two proposals, got %+v", result)
	}
	if strings.ContainsAny(result.Message, "\x1b\u202e") {
		t.Fatalf("run report printed raw control characters from model output:\n%q", result.Message)
	}
	for _, expected := range []string{"Discarded: 12", "evidence repeats", "unknown project", "not in the request", "not a member of the unit", "kind", "confidence", "more than once"} {
		if !strings.Contains(result.Message, expected) {
			t.Fatalf("run report omitted %q:\n%s", expected, result.Message)
		}
	}
	links, err := workgraph.ReadProjectLinks(workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}, workgraph.ProjectLinksQuery{})
	if err != nil || len(links) != 2 {
		t.Fatalf("expected two stored links, got %+v %v", links, err)
	}
	if proposed := links[1]; proposed.UnitID != units[10].ID || proposed.NewProject == nil || proposed.NewProject.Name != "GraphQL[31m tooling evil" {
		t.Fatalf("expected a sanitized new project name, got %+v", proposed.NewProject)
	}
	link := links[0]
	if link.UnitID != units[5].ID || link.Source != "llm" || link.Profile != "local-associate" || link.State != "proposed" || link.Auto || link.RunID != result.RunID || link.Kind != "build" || link.Container != "harness-team" {
		t.Fatalf("unexpected stored link: %+v", link)
	}
}

func TestAssociateAutoApplyGatesAndAccuracyPause(t *testing.T) {
	home := initAssociateHome(t)
	fake := newAssociateFakeModel(t)
	configureAssociateProfile(t, home, fake.URL, "slack")
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}
	now := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)

	rejected, err := workgraph.RecordProjectLinkRun(workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, Now: now.Add(-10 * 24 * time.Hour)}, workgraph.ProjectLinkRun{Source: "llm", Profile: "local-associate", Links: []workgraph.ProjectLinkProposal{
		{UnitID: "slack.thread:C2:1.0", UnitKind: "slack.thread", Container: "rejecting-team", Target: "project", Project: "ask-kai", Kind: "fyi", Summary: "old", Confidence: 0.99, Evidence: []string{"slack.message:C2:1.0"}, Apply: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.RejectProjectLink(workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, Now: now.Add(-10 * 24 * time.Hour)}, rejected.Written[0].ID); err != nil {
		t.Fatal(err)
	}

	units := insertAssociateThreads(t, home, now.Add(-6*time.Hour), "C1", "harness-team", 4)
	rejecting := insertAssociateThreads(t, home, now.Add(-5*time.Hour), "C2", "rejecting-team", 1)
	newProject := associateEntry(units[2], "new_project", "", 0.99)
	newProject["new_project"] = map[string]string{"slug": "graphql-tooling", "name": "GraphQL tooling"}
	fake.Respond(associateResponse(
		associateEntry(units[0], "project", "mmo-harness", 0.95),
		associateEntry(units[1], "project", "mmo-harness", 0.94),
		newProject,
		associateEntry(units[3], "none", "", 0.97),
		associateEntry(rejecting[0], "project", "ask-kai", 0.99),
	))
	result, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.Client, Now: now})
	if err != nil {
		t.Fatalf("associate: %v", err)
	}
	if result.Applied != 2 || result.Proposed != 3 || len(result.Discarded) != 0 {
		t.Fatalf("expected two applied and three proposed, got %+v", result)
	}
	assertAssociateLink(t, store, units[0].ID, "applied", true)
	assertAssociateLink(t, store, units[1].ID, "proposed", false)
	assertAssociateLink(t, store, units[2].ID, "proposed", false)
	assertAssociateLink(t, store, units[3].ID, "applied", true)
	assertAssociateLink(t, store, rejecting[0].ID, "proposed", false)
	accuracy, err := workgraph.ProjectLinkAccuracy(workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, Now: now})
	if err != nil || accuracy.Status != "insufficient_data" || accuracy.Decided != 1 || accuracy.Paused {
		t.Fatalf("expected auto-apply on with insufficient data, got %+v %v", accuracy, err)
	}

	seedDecidedAutoLinks(t, home, now.Add(-24*time.Hour), "low", 18, 2)
	accuracy, err = workgraph.ProjectLinkAccuracy(workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, Now: now})
	if err != nil || accuracy.Status != "measured" || accuracy.Decided != 21 || !accuracy.Paused {
		t.Fatalf("expected measured accuracy below 0.95 to pause, got %+v %v", accuracy, err)
	}
	paused := insertAssociateThreads(t, home, now.Add(-4*time.Hour), "C3", "harness-team", 1)
	fake.Respond(associateResponse(associateEntry(paused[0], "project", "mmo-harness", 0.99)))
	result, err = workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.Client, Now: now})
	if err != nil {
		t.Fatalf("associate while paused: %v", err)
	}
	if result.Applied != 0 || result.Proposed != 1 || !strings.Contains(result.Message, "Auto-apply paused: accuracy 0.86 over 21 decided") {
		t.Fatalf("expected the paused run to propose and say why, got %+v", result)
	}
	assertAssociateLink(t, store, paused[0].ID, "proposed", false)
	if got := autoApplyPauseEvents(t, home.DatabasePath); !reflect.DeepEqual(got, []string{"projects.auto_apply_paused"}) {
		t.Fatalf("expected one pause event, got %v", got)
	}
	review, err := workgraph.EffectivenessReview(workgraph.EffectivenessReviewConfig{HomeDir: home.HomeDir, Now: now})
	if err != nil || !strings.Contains(review.Message, "Project auto-apply accuracy: 0.86 (18/21 decided auto links, 14 day window)") || !strings.Contains(review.Message, "Auto-apply paused: accuracy 0.86") {
		t.Fatalf("expected workgraph review to report the metric and pause, got %v\n%s", err, review.Message)
	}

	later := now.Add(20 * 24 * time.Hour)
	stillPaused := insertAssociateThreads(t, home, later.Add(-4*time.Hour), "C4", "harness-team", 1)
	fake.Respond(associateResponse(associateEntry(stillPaused[0], "project", "mmo-harness", 0.99)))
	result, err = workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.Client, Now: later})
	if err != nil {
		t.Fatalf("associate after the sample expired: %v", err)
	}
	if result.Applied != 0 || result.Proposed != 1 || result.Accuracy.Status != "insufficient_data" || !result.Accuracy.Paused {
		t.Fatalf("expected the pause to survive a sample below 20, got %+v", result)
	}

	seedDecidedAutoLinks(t, home, later.Add(-time.Hour), "high", 20, 0)
	resumed := insertAssociateThreads(t, home, later.Add(-3*time.Hour), "C5", "harness-team", 1)
	fake.Respond(associateResponse(associateEntry(resumed[0], "project", "mmo-harness", 0.99)))
	result, err = workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.Client, Now: later})
	if err != nil {
		t.Fatalf("associate after accuracy recovered: %v", err)
	}
	if result.Applied != 1 || result.Accuracy.Paused {
		t.Fatalf("expected auto-apply to resume, got %+v", result)
	}
	if got := autoApplyPauseEvents(t, home.DatabasePath); !reflect.DeepEqual(got, []string{"projects.auto_apply_paused", "projects.auto_apply_resumed"}) {
		t.Fatalf("expected pause then resume events, got %v", got)
	}
}

func TestAssociateRecordsAMeasuredPauseWithoutAModelCall(t *testing.T) {
	home := initAssociateHome(t)
	fake := newAssociateFakeModel(t)
	configureAssociateProfile(t, home, fake.URL, "slack")
	now := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)

	seedDecidedAutoLinks(t, home, now.Add(-24*time.Hour), "low", 18, 2)
	if got := autoApplyPauseEvents(t, home.DatabasePath); !reflect.DeepEqual(got, []string{"projects.auto_apply_paused"}) {
		t.Fatalf("expected the deciding write to record the pause, got %v", got)
	}
	result, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.Client, Now: now})
	if err != nil || len(result.Sent) != 0 || fake.Calls() != 0 {
		t.Fatalf("expected a run with no eligible units to call no model, got %+v %v calls=%d", result, err, fake.Calls())
	}
	accuracy, err := workgraph.ProjectLinkAccuracy(workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, Now: now.Add(20 * 24 * time.Hour)})
	if err != nil || accuracy.Status != "insufficient_data" || !accuracy.Paused || !strings.Contains(accuracy.PauseReason, "accuracy 0.90 over 20 decided") {
		t.Fatalf("expected the recorded pause to outlive its sample, got %+v %v", accuracy, err)
	}
}

type associateHome struct {
	HomeDir      string
	MemoryDir    string
	DatabasePath string
}

func initAssociateHome(t *testing.T) associateHome {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	home, memory := filepath.Join(root, "state"), filepath.Join(root, "memory")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: home, MemoryDir: memory})
	if err != nil {
		t.Fatal(err)
	}
	writeProjectMemory(t, memory, "mmo-harness", "---\nworkstream: mmo\n---\n# MMO Harness\n\nTest harness.\n")
	writeProjectMemory(t, memory, "mmo-gtm-segments", "---\nworkstream: mmo\n---\n# MMO GTM Segments\n")
	writeProjectMemory(t, memory, "ask-kai", "# Ask Kai\n")
	return associateHome{HomeDir: home, MemoryDir: memory, DatabasePath: initialized.DatabasePath}
}

func configureAssociateProfile(t *testing.T, home associateHome, baseURL string, sources ...string) {
	t.Helper()
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "local-associate", Provider: "openai-compatible", BaseURL: baseURL + "/v1", Model: "small-model"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.UseLLMProfile(workgraph.LLMUseProfileConfig{HomeDir: home.HomeDir, Name: "local-associate", Task: "associate"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.ConfigureLLMSources(workgraph.LLMSourcesConfig{HomeDir: home.HomeDir, Task: "associate", Enable: sources}); err != nil {
		t.Fatal(err)
	}
}

func associateSlackEvent(channelID, ts, conversationType, container, actor, summary string, timestamp time.Time) storedEvent {
	payload := map[string]any{"channel_id": channelID, "ts": ts, "thread_ts": ts, "text": summary, "user_name": actor}
	if conversationType != "" {
		payload["conversation_type"] = conversationType
	}
	encoded, _ := json.Marshal(payload)
	return storedEvent{ID: fmt.Sprintf("slack.message:%s:%s", channelID, ts), Source: "slack", Type: "slack.message", Timestamp: timestamp, Project: container, Actor: actor, Summary: summary, Payload: string(encoded)}
}

func insertAssociateThreads(t *testing.T, home associateHome, start time.Time, channelID, container string, count int) []workgraph.ProjectUnit {
	t.Helper()
	var units []workgraph.ProjectUnit
	for i := 0; i < count; i++ {
		ts := fmt.Sprintf("%d.000100", start.Unix()+int64(i))
		event := associateSlackEvent(channelID, ts, "channel", container, "Sam", fmt.Sprintf("thread %d in %s", i, channelID), start.Add(time.Duration(i)*time.Minute))
		insertEvent(t, home.DatabasePath, event)
		units = append(units, workgraph.ProjectUnit{ID: fmt.Sprintf("slack.thread:%s:%s", channelID, ts), EventIDs: []string{event.ID}})
	}
	return units
}

func seedDecidedAutoLinks(t *testing.T, home associateHome, decidedAt time.Time, prefix string, confirmed, corrected int) {
	t.Helper()
	config := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, Now: decidedAt}
	var proposals []workgraph.ProjectLinkProposal
	for i := 0; i < confirmed+corrected; i++ {
		id := fmt.Sprintf("seed-%s-%d", prefix, i)
		proposals = append(proposals, workgraph.ProjectLinkProposal{UnitID: id, UnitKind: "slack.thread", Container: "seed-team", Target: "project", Project: "mmo-harness", Kind: "build", Summary: "seed", Confidence: 0.99, Evidence: []string{id}, Apply: true})
	}
	run, err := workgraph.RecordProjectLinkRun(config, workgraph.ProjectLinkRun{Source: "llm", Profile: "local-associate", Links: proposals})
	if err != nil {
		t.Fatal(err)
	}
	for i, link := range run.Written {
		if i < confirmed {
			_, err = workgraph.ApproveProjectLink(config, link.ID)
		} else {
			_, err = workgraph.RejectProjectLink(config, link.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func associateEntry(unit workgraph.ProjectUnit, target, project string, confidence float64) map[string]any {
	entry := map[string]any{"unit_id": unit.ID, "target": target, "kind": "build", "summary": "Worked on " + unit.ID, "confidence": confidence, "evidence": unit.EventIDs}
	if project != "" {
		entry["project"] = project
	}
	return entry
}

func withEvidence(entry map[string]any, evidence ...string) map[string]any {
	entry["evidence"] = evidence
	return entry
}

func withKind(entry map[string]any, kind string) map[string]any {
	entry["kind"] = kind
	return entry
}

func associateResponse(entries ...map[string]any) string {
	encoded, _ := json.Marshal(map[string]any{"schema": "workgraph.associate_result.v1", "units": entries})
	return string(encoded)
}

func assertAssociateLink(t *testing.T, config workgraph.ProjectLinkStoreConfig, unitID, state string, auto bool) {
	t.Helper()
	link := currentProjectLink(t, config, unitID)
	if link.State != state || link.Auto != auto || link.Source != "llm" {
		t.Fatalf("expected %s to be %s auto=%t, got %+v", unitID, state, auto, link)
	}
}

func autoApplyPauseEvents(t *testing.T, dbPath string) []string {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT type FROM events WHERE type GLOB 'projects.auto_apply_*' ORDER BY timestamp, rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var types []string
	for rows.Next() {
		var eventType string
		if err := rows.Scan(&eventType); err != nil {
			t.Fatal(err)
		}
		types = append(types, eventType)
	}
	return types
}

type associateFakeModel struct {
	URL      string
	Client   *http.Client
	mu       sync.Mutex
	calls    int
	response string
}

func newAssociateFakeModel(t *testing.T) *associateFakeModel {
	t.Helper()
	fake := &associateFakeModel{URL: "http://127.0.0.1:9"}
	fake.Client = &http.Client{Transport: associateRoundTripper(func(request *http.Request) (*http.Response, error) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		fake.calls++
		recorder := httptest.NewRecorder()
		_ = json.NewEncoder(recorder).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": fake.response}}}})
		return recorder.Result(), nil
	})}
	return fake
}

type associateRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip associateRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func (fake *associateFakeModel) Respond(response string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.response = response
}

func (fake *associateFakeModel) Calls() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.calls
}

type associatePayload struct {
	Projects []struct {
		Slug string `json:"slug"`
	} `json:"projects"`
	Units []struct {
		UnitID       string   `json:"unit_id"`
		Container    string   `json:"container"`
		Participants []string `json:"participants"`
		Projects     []string `json:"projects"`
		Events       []struct {
			ID      string `json:"id"`
			Summary string `json:"summary"`
		} `json:"events"`
	} `json:"units"`
	Decisions []struct {
		Container string `json:"container"`
		Decision  string `json:"decision"`
		Project   string `json:"project"`
	} `json:"recent_decisions"`
}

func associateDryRunPayload(t *testing.T, output string) associatePayload {
	t.Helper()
	_, rest, ok := strings.Cut(output, "<<<WORKGRAPH_DATA\n")
	data, _, closed := strings.Cut(rest, "\nWORKGRAPH_DATA>>>")
	if !ok || !closed {
		t.Fatalf("dry run did not print a delimited payload:\n%s", output)
	}
	var payload associatePayload
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		t.Fatalf("parse dry-run payload: %v\n%s", err, data)
	}
	return payload
}

func associatePayloadUnitIDs(payload associatePayload) []string {
	var ids []string
	for _, unit := range payload.Units {
		ids = append(ids, unit.UnitID)
	}
	sort.Strings(ids)
	return ids
}

func associatePayloadProjectSlugs(payload associatePayload) []string {
	var slugs []string
	for _, project := range payload.Projects {
		slugs = append(slugs, project.Slug)
	}
	return slugs
}
