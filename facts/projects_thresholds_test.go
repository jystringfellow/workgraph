package facts

import (
	"database/sql"
	"encoding/json"
	"errors"
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

func TestLLMProfileThresholdSettings(t *testing.T) {
	home := initAssociateHome(t)
	writeInventedProjects(t, home)
	args := []string{"--home", home.HomeDir}
	add := append([]string{"llm", "add", "tuned"}, args...)
	add = append(add, "--provider", "openai-compatible", "--base-url", "http://localhost:11434/v1", "--model", "small-model", "--auto-apply-confidence", "0.9")
	if output, err := runworkgraph(t, repoRoot(t), add...); err != nil || !strings.Contains(string(output), "Auto-apply confidence: 0.90") {
		t.Fatalf("llm add with a threshold: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot(t), append([]string{"llm", "add", "plain"}, append(args, "--provider", "openai-compatible", "--base-url", "http://localhost:11434/v1", "--model", "other-model")...)...); err != nil {
		t.Fatalf("llm add plain: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot(t), append([]string{"llm", "set", "tuned", "--verify-floor", "0.5"}, args...)...); err != nil || !strings.Contains(string(output), "LLM profile updated: tuned") {
		t.Fatalf("llm set verify floor: %v\n%s", err, output)
	}
	for _, refused := range [][]string{
		{"llm", "set", "tuned", "--auto-apply-confidence", "0.4"},
		{"llm", "set", "tuned", "--auto-apply-confidence", "1.2"},
		{"llm", "set", "tuned", "--verify-floor", "-0.1"},
		{"llm", "set", "tuned", "--auto-apply-confidence", "high"},
		{"llm", "set", "tuned"},
		{"llm", "set", "missing", "--verify-floor", "0.5"},
		{"llm", "add", "bad", "--provider", "openai-compatible", "--base-url", "http://localhost:11434/v1", "--model", "m", "--auto-apply-confidence", "0.3"},
	} {
		if output, err := runworkgraph(t, repoRoot(t), append(refused, args...)...); err == nil {
			t.Fatalf("%v was accepted:\n%s", refused, output)
		}
	}
	contents, err := os.ReadFile(filepath.Join(home.HomeDir, "llm.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Profiles map[string]struct {
			Provider            string   `json:"provider"`
			BaseURL             string   `json:"base_url"`
			Model               string   `json:"model"`
			AutoApplyConfidence *float64 `json:"auto_apply_confidence"`
			VerifyFloor         *float64 `json:"verify_floor"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(contents, &stored); err != nil {
		t.Fatal(err)
	}
	tuned := stored.Profiles["tuned"]
	if tuned.Provider != "openai-compatible" || tuned.BaseURL != "http://localhost:11434/v1" || tuned.Model != "small-model" || tuned.AutoApplyConfidence == nil || *tuned.AutoApplyConfidence != 0.9 || tuned.VerifyFloor == nil || *tuned.VerifyFloor != 0.5 {
		t.Fatalf("expected llm set to keep every other setting, got %+v\n%s", tuned, contents)
	}
	if _, found := stored.Profiles["bad"]; found {
		t.Fatalf("a refused add stored a profile:\n%s", contents)
	}
	if plain := stored.Profiles["plain"]; plain.AutoApplyConfidence != nil || plain.VerifyFloor != nil {
		t.Fatalf("expected defaults to stay unset, got %+v", plain)
	}
	output, err := runworkgraph(t, repoRoot(t), append([]string{"llm", "list"}, args...)...)
	if err != nil {
		t.Fatalf("llm list: %v\n%s", err, output)
	}
	for _, expected := range []string{"- plain: openai-compatible other-model auto-apply 0.95, verify-floor 0.60", "- tuned: openai-compatible small-model auto-apply 0.90, verify-floor 0.50"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("llm list omitted %q:\n%s", expected, output)
		}
	}
}

func TestAssociateAutoAppliesAtTheProfileThreshold(t *testing.T) {
	home := initAssociateHome(t)
	writeInventedProjects(t, home)
	fake := newAssociateFakeModel(t)
	configureAssociateProfile(t, home, fake.URL, "slack")
	setProfileThresholds(t, home, "local-associate", 0.9, -1)
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}
	now := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	units := insertAssociateThreads(t, home, now.Add(-6*time.Hour), "C1", "harness-team", 3)

	fake.Respond(associateResponse(
		associateEntry(units[0], "project", "harbor-harness", 0.92),
		associateEntry(units[1], "project", "harbor-harness", 0.89),
	))
	result, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.Client, Now: now})
	if err != nil || result.Applied != 1 || result.Proposed != 1 {
		t.Fatalf("expected one applied at 0.92 and one proposed at 0.89, got %+v %v", result, err)
	}
	assertAssociateLink(t, store, units[0].ID, "applied", true)
	assertAssociateLink(t, store, units[1].ID, "proposed", false)

	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "strict", Provider: "openai-compatible", BaseURL: fake.URL + "/v1", Model: "small-model"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.UseLLMProfile(workgraph.LLMUseProfileConfig{HomeDir: home.HomeDir, Name: "strict", Task: "associate"}); err != nil {
		t.Fatal(err)
	}
	fake.Respond(associateResponse(associateEntry(units[2], "project", "harbor-harness", 0.92)))
	result, err = workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.Client, Now: now})
	if err != nil || result.Applied != 0 || result.Proposed != 1 {
		t.Fatalf("expected the default profile to propose at 0.92, got %+v %v", result, err)
	}
	if link := currentProjectLink(t, store, units[2].ID); link.State != "proposed" || link.Profile != "strict" {
		t.Fatalf("expected a proposal from the strict profile, got %+v", link)
	}
}

func TestAccuracyGateIsPerProfile(t *testing.T) {
	home := initAssociateHome(t)
	writeInventedProjects(t, home)
	fake := newAssociateFakeModel(t)
	configureAssociateProfile(t, home, fake.URL, "slack")
	setProfileThresholds(t, home, "local-associate", 0.9, -1)
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "strict", Provider: "openai-compatible", BaseURL: fake.URL + "/v1", Model: "small-model"}); err != nil {
		t.Fatal(err)
	}
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}
	now := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	seedDecidedProfileLinks(t, home, now.Add(-24*time.Hour), "local-associate", "tuned", 18, 2)
	seedDecidedProfileLinks(t, home, now.Add(-24*time.Hour), "strict", "strict", 18, 2)

	accuracy, err := workgraph.ProjectLinkAccuracy(workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	tuned, strict := accuracy.For("local-associate"), accuracy.For("strict")
	if tuned.Status != "measured" || tuned.Paused || tuned.Required != 0.9 || strict.Status != "measured" || !strict.Paused || strict.Required != 0.95 {
		t.Fatalf("expected only the strict profile to pause, got tuned=%+v strict=%+v", tuned, strict)
	}
	if !strings.Contains(strict.PauseReason, "strict accuracy 0.90 over 20 decided auto links in the last 14 days is below 0.95") {
		t.Fatalf("expected the pause reason to name the profile and its threshold, got %q", strict.PauseReason)
	}
	if got := autoApplyPauseEventProfiles(t, home.DatabasePath); !reflect.DeepEqual(got, []string{"projects.auto_apply_paused:strict"}) {
		t.Fatalf("expected one pause event for the strict profile, got %v", got)
	}

	units := insertAssociateThreads(t, home, now.Add(-6*time.Hour), "C1", "harness-team", 2)
	fake.Respond(associateResponse(associateEntry(units[0], "project", "harbor-harness", 0.95)))
	result, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.Client, Now: now})
	if err != nil || result.Applied != 1 {
		t.Fatalf("expected the tuned profile to keep auto-applying while strict is paused, got %+v %v", result, err)
	}
	for _, expected := range []string{
		"Project auto-apply accuracy for local-associate: 0.90 (18/20 decided auto links, 14 day window, required 0.90)",
		"Project auto-apply accuracy for strict: 0.90 (18/20 decided auto links, 14 day window, required 0.95)",
		"Auto-apply paused: strict accuracy 0.90 over 20 decided",
	} {
		if !strings.Contains(result.Message, expected) {
			t.Fatalf("run report omitted %q:\n%s", expected, result.Message)
		}
	}
	review, err := workgraph.EffectivenessReview(workgraph.EffectivenessReviewConfig{HomeDir: home.HomeDir, Now: now, Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		AutoApply struct {
			Paused      bool   `json:"paused"`
			PauseReason string `json:"pause_reason"`
			Profiles    []struct {
				Profile          string   `json:"profile"`
				RequiredAccuracy float64  `json:"required_accuracy"`
				Status           string   `json:"status"`
				Accuracy         *float64 `json:"accuracy"`
				SampleSize       int      `json:"sample_size"`
				Paused           bool     `json:"paused"`
			} `json:"profiles"`
		} `json:"project_auto_apply"`
	}
	if err := json.Unmarshal([]byte(review.Message), &payload); err != nil {
		t.Fatalf("decode review: %v\n%s", err, review.Message)
	}
	profiles := payload.AutoApply.Profiles
	if !payload.AutoApply.Paused || !strings.HasPrefix(payload.AutoApply.PauseReason, "strict accuracy") || len(profiles) != 2 || profiles[0].Profile != "local-associate" || profiles[0].Paused || profiles[0].RequiredAccuracy != 0.9 || profiles[1].Profile != "strict" || !profiles[1].Paused || profiles[1].SampleSize != 20 {
		t.Fatalf("unexpected review metric: %+v", payload.AutoApply)
	}

	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "fresh", Provider: "openai-compatible", BaseURL: fake.URL + "/v1", Model: "small-model"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.UseLLMProfile(workgraph.LLMUseProfileConfig{HomeDir: home.HomeDir, Name: "fresh", Task: "associate"}); err != nil {
		t.Fatal(err)
	}
	insertLegacyAutoApplyPause(t, home, now.Add(-time.Hour))
	fake.Respond(associateResponse(associateEntry(units[1], "project", "harbor-harness", 0.99)))
	result, err = workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.Client, Now: now})
	if err != nil || result.Applied != 0 || result.Proposed != 1 || !strings.Contains(result.Message, "Auto-apply paused: legacy pause") {
		t.Fatalf("expected a pause event without a profile to hold a profile with no event, got %+v %v", result, err)
	}
	assertAssociateLink(t, store, units[1].ID, "proposed", false)
	if tuned := result.Accuracy.For("local-associate"); tuned.Paused {
		t.Fatalf("a measured profile kept a legacy pause: %+v", tuned)
	}
}

func TestDirectVerifierDecidesUncertainUnits(t *testing.T) {
	home := initAssociateHome(t)
	writeInventedProjects(t, home)
	models := newRoutedAssociateModels(t)
	configureAssociateProfile(t, home, models.URL, "slack")
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "big-verifier", Provider: "openai-compatible", BaseURL: models.URL + "/v1", Model: "big-model"}); err != nil {
		t.Fatal(err)
	}
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}
	now := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	config := workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: models.Client, Now: now}

	unrouted := insertAssociateThreads(t, home, now.Add(-7*time.Hour), "C0", "harness-team", 1)
	models.Respond("small-model", associateResponse(associateEntry(unrouted[0], "project", "harbor-harness", 0.8)))
	if result, err := workgraph.AssociateWithLLM(config); err != nil || models.Calls("big-model") != 0 || strings.Contains(result.Message, "Escalated") {
		t.Fatalf("expected no verifier without the route, got %+v calls=%d %v", result, models.Calls("big-model"), err)
	}

	if output, err := runworkgraph(t, repoRoot(t), "llm", "use", "big-verifier", "--for", "associate-verify", "--home", home.HomeDir); err != nil {
		t.Fatalf("route the verifier: %v\n%s", err, output)
	}
	dryRun, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: models.Client, Now: now, DryRun: true})
	if err != nil || !strings.Contains(dryRun.Message, "Verifier: big-verifier, confidence 0.60 to below 0.95") || models.Calls("big-model") != 0 {
		t.Fatalf("expected the dry run to name the verifier and call nothing: %v\n%s", err, dryRun.Message)
	}

	units := insertAssociateThreads(t, home, now.Add(-6*time.Hour), "C1", "harness-team", 4)
	models.Respond("small-model", associateResponse(
		associateEntry(units[0], "project", "harbor-harness", 0.97),
		associateEntry(units[1], "project", "harbor-harness", 0.80),
		associateEntry(units[2], "project", "harbor-harness", 0.40),
		associateEntry(units[3], "none", "", 0.85),
	))
	verdict := associateEntry(units[1], "project", "atlas-bot", 0.96)
	verdict["summary"] = "Verifier says atlas bot"
	models.Respond("big-model", associateResponse(verdict))
	result, err := workgraph.AssociateWithLLM(config)
	if err != nil {
		t.Fatalf("associate with a verifier: %v", err)
	}
	if models.Calls("small-model") != 2 || models.Calls("big-model") != 1 {
		t.Fatalf("expected one associate and one verifier call, got small=%d big=%d", models.Calls("small-model"), models.Calls("big-model"))
	}
	for _, expected := range []string{"Escalated: 2", "Verified: 1 by big-verifier (1 changed)", "Applied: 2", "Proposed: 2"} {
		if !strings.Contains(result.Message, expected) {
			t.Fatalf("run report omitted %q:\n%s", expected, result.Message)
		}
	}
	request := models.LastRequest("big-model")
	for _, expected := range []string{units[1].ID, units[3].ID, `\"primary_answer\": {`, "Worked on " + units[1].ID, "thread 1 in C1"} {
		if !strings.Contains(request, expected) {
			t.Fatalf("verifier request omitted %q:\n%s", expected, request)
		}
	}
	for _, leaked := range []string{units[0].ID, units[2].ID, "thread 0 in C1", "thread 2 in C1", unrouted[0].ID} {
		if strings.Contains(request, leaked) {
			t.Fatalf("verifier request carried %q from a unit it was not asked about:\n%s", leaked, request)
		}
	}

	if link := currentProjectLink(t, store, units[0].ID); link.State != "applied" || link.Profile != "local-associate" {
		t.Fatalf("expected the confident unit applied by the associate profile, got %+v", link)
	}
	verified := currentProjectLink(t, store, units[1].ID)
	if verified.State != "applied" || !verified.Auto || verified.Profile != "big-verifier" || verified.Project != "atlas-bot" || verified.Summary != "Verifier says atlas bot" || verified.RunID != result.RunID {
		t.Fatalf("expected the verifier's answer applied under its profile, got %+v", verified)
	}
	if link := currentProjectLink(t, store, units[2].ID); link.State != "proposed" || link.Profile != "local-associate" {
		t.Fatalf("expected the unit below the floor proposed by the associate profile, got %+v", link)
	}
	if link := currentProjectLink(t, store, units[3].ID); link.State != "proposed" || link.Profile != "local-associate" || link.Target != "none" {
		t.Fatalf("expected the unit the verifier left out to keep the associate result, got %+v", link)
	}
	explain, err := runworkgraph(t, repoRoot(t), "projects", "explain", units[1].ID, "--home", home.HomeDir, "--memory", home.MemoryDir)
	if err != nil {
		t.Fatalf("projects explain: %v\n%s", err, explain)
	}
	for _, expected := range []string{"  Associate local-associate: project harbor-harness (confidence 0.80)", "  Verifier big-verifier: project atlas-bot (confidence 0.96)"} {
		if !strings.Contains(string(explain), expected) {
			t.Fatalf("explain omitted %q:\n%s", expected, explain)
		}
	}

	failing := insertAssociateThreads(t, home, now.Add(-5*time.Hour), "C2", "harness-team", 1)
	models.Respond("small-model", associateResponse(associateEntry(failing[0], "project", "harbor-harness", 0.7)))
	models.Fail("big-model")
	result, err = workgraph.AssociateWithLLM(config)
	if err != nil || !strings.Contains(result.Message, "Verifier failed:") || result.Proposed != 1 {
		t.Fatalf("expected a failed verifier call to keep the associate result, got %+v %v", result, err)
	}
	if link := currentProjectLink(t, store, failing[0].ID); link.State != "proposed" || link.Profile != "local-associate" {
		t.Fatalf("expected the associate proposal kept, got %+v", link)
	}
}

func TestBridgeVerifierUsesTheOutbox(t *testing.T) {
	home := initScheduleHome(t)
	writeInventedProjects(t, home)
	fake := newAssociateFakeModel(t)
	configureAssociateProfile(t, home, fake.URL, "slack")
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "codex-verify", Provider: "ai-client", Client: "codex", Model: "gpt-verify", Execution: "bridge"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.UseLLMProfile(workgraph.LLMUseProfileConfig{HomeDir: home.HomeDir, Name: "codex-verify", Task: "associate-verify"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.EnableHostedLLM(workgraph.LLMHostedConfig{HomeDir: home.HomeDir}); err != nil {
		t.Fatal(err)
	}
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}
	units := insertAssociateThreads(t, home, time.Now().UTC().Add(-6*time.Hour), "C1", "harness-team", 2)
	fake.Respond(associateResponse(associateEntry(units[0], "project", "atlas-bot", 0.99), associateEntry(units[1], "project", "harbor-harness", 0.8)))
	schedule := workgraph.LLMAssociateScheduleConfig{HomeDir: home.HomeDir, HTTPClient: fake.Client}

	ran, err := workgraph.ScheduleLLMAssociate(schedule)
	if err != nil || !ran.Executed || fake.Calls() != 1 || !strings.Contains(ran.Run.Message, "Verify request emitted: ") {
		t.Fatalf("expected the daemon to run the associate request and emit a verify request, got %+v calls=%d %v", ran, fake.Calls(), err)
	}
	assertAssociateLink(t, store, units[0].ID, "applied", true)
	if links, _ := workgraph.ReadProjectLinks(store, workgraph.ProjectLinksQuery{UnitID: units[1].ID}); len(links) != 0 {
		t.Fatalf("expected the escalated unit to wait for the verifier, got %+v", links)
	}
	requests := llmAssociateRequests(t, home)
	if len(requests) == 2 && requests[0].Status == "pending" {
		requests[0], requests[1] = requests[1], requests[0]
	}
	if len(requests) != 2 || requests[0].Status != "completed" || requests[1].Status != "pending" || storedLLMRequestTask(t, home, requests[1].ID) != "associate-verify" {
		t.Fatalf("expected a completed associate request and a pending verify request, got %+v", requests)
	}
	if got := storedLLMRequestUnitIDs(t, home, requests[1].ID); !reflect.DeepEqual(got, []string{units[1].ID}) {
		t.Fatalf("expected only the escalated unit in the verify request, got %v", got)
	}
	verifyID := requests[1].ID

	late := insertAssociateThreads(t, home, time.Now().UTC().Add(-5*time.Hour), "C2", "harness-team", 1)
	waiting, err := workgraph.ScheduleLLMAssociate(schedule)
	if err != nil || waiting.Created || waiting.Executed || waiting.Request.ID != verifyID || fake.Calls() != 1 {
		t.Fatalf("expected no associate request while the verify request is active, got %+v calls=%d %v", waiting, fake.Calls(), err)
	}
	output, err := runworkgraph(t, repoRoot(t), "llm", "associate", "--home", home.HomeDir, "--memory", home.MemoryDir)
	if err == nil || !strings.Contains(string(output), "Request: "+verifyID) || fake.Calls() != 1 {
		t.Fatalf("expected a direct run to refuse while the verify request is active: %v\n%s", err, output)
	}

	content, failure := callScheduleMCP(t, home, "capture_requests_claim", map[string]any{"worker": "claude-code-drain-fact", "kind": "llm_task"})
	if failure != "" || len(content["claims"].([]any)) != 0 {
		t.Fatalf("a client that is not the verifier's claimed the verify request: %v %s", content, failure)
	}
	fixtureDir := t.TempDir()
	logPath := filepath.Join(fixtureDir, "codex.log")
	clientPath := filepath.Join(fixtureDir, "codex")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+logPath+"\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", home.HomeDir, "--client", "codex", "--client-command", clientPath); err != nil {
		t.Fatalf("codex drain: %v\n%s", err, output)
	}
	if logged, err := os.ReadFile(logPath); err != nil || !strings.Contains(string(logged), "--model gpt-verify") {
		t.Fatalf("expected the drain to launch the verifier's client with its model: %v\n%s", err, logged)
	}

	content, failure = callScheduleMCP(t, home, "capture_requests_claim", map[string]any{"worker": "codex-drain-fact", "kind": "llm_task"})
	if failure != "" || len(content["claims"].([]any)) != 1 {
		t.Fatalf("the verifier's client could not claim the verify request: %v %s", content, failure)
	}
	claim := content["claims"].([]any)[0].(map[string]any)
	task := claim["task"].(map[string]any)
	encodedTask, _ := json.Marshal(task)
	if task["task"] != "associate-verify" || task["profile"] != "codex-verify" || task["model"] != "gpt-verify" || !strings.Contains(string(encodedTask), units[1].ID) || !strings.Contains(string(encodedTask), "primary_answer") || strings.Contains(string(encodedTask), units[0].ID) {
		t.Fatalf("unexpected verify task: %s", encodedTask)
	}
	verdict := associateResponse(associateEntry(units[1], "project", "atlas-bot", 0.97))
	submitted, failure := callScheduleMCP(t, home, "llm_task_submit", map[string]any{"request_id": verifyID, "claim_token": claim["claim_token"], "result": json.RawMessage(verdict)})
	if failure != "" || submitted["request_completed"] != true {
		t.Fatalf("submit the verifier result: %v %s", submitted, failure)
	}
	if link := currentProjectLink(t, store, units[1].ID); link.State != "applied" || link.Profile != "codex-verify" || link.Project != "atlas-bot" {
		t.Fatalf("expected the verifier's answer applied under its profile, got %+v", link)
	}
	for _, request := range llmAssociateRequests(t, home) {
		if request.ID == verifyID && request.Status != "completed" {
			t.Fatalf("expected the verify request completed, got %+v", request)
		}
	}

	fake.Respond(associateResponse(associateEntry(late[0], "project", "atlas-bot", 0.99)))
	resumed, err := workgraph.ScheduleLLMAssociate(schedule)
	if err != nil || !resumed.Created || !resumed.Executed || fake.Calls() != 2 {
		t.Fatalf("expected associate requests to resume after verification, got %+v calls=%d %v", resumed, fake.Calls(), err)
	}
}

func setProfileThresholds(t *testing.T, home associateHome, profile string, autoApply, verifyFloor float64) {
	t.Helper()
	config := workgraph.LLMSetProfileConfig{HomeDir: home.HomeDir, Name: profile}
	if autoApply >= 0 {
		config.AutoApplyConfidence = &autoApply
	}
	if verifyFloor >= 0 {
		config.VerifyFloor = &verifyFloor
	}
	if _, err := workgraph.SetLLMProfile(config); err != nil {
		t.Fatal(err)
	}
}

func seedDecidedProfileLinks(t *testing.T, home associateHome, decidedAt time.Time, profile, prefix string, confirmed, corrected int) {
	t.Helper()
	config := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, Now: decidedAt}
	var proposals []workgraph.ProjectLinkProposal
	for i := 0; i < confirmed+corrected; i++ {
		id := fmt.Sprintf("seed-%s-%d", prefix, i)
		proposals = append(proposals, workgraph.ProjectLinkProposal{UnitID: id, UnitKind: "slack.thread", Container: "seed-team", Target: "project", Project: "harbor-harness", Kind: "build", Summary: "seed", Confidence: 0.99, Evidence: []string{id}, Apply: true})
	}
	run, err := workgraph.RecordProjectLinkRun(config, workgraph.ProjectLinkRun{Source: "llm", Profile: profile, Links: proposals})
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

func insertLegacyAutoApplyPause(t *testing.T, home associateHome, at time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite3", home.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	timestamp := at.UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES ('projects.auto_apply_paused:legacy', 'projects', 'projects.auto_apply_paused', ?, '{"paused":true,"reason":"legacy pause"}', '', '', 'Project auto-apply paused: legacy pause', ?)`, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
}

func autoApplyPauseEventProfiles(t *testing.T, dbPath string) []string {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT type, COALESCE(json_extract(payload_json, '$.profile'), '') FROM events WHERE type GLOB 'projects.auto_apply_*' ORDER BY timestamp, rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var events []string
	for rows.Next() {
		var eventType, profile string
		if err := rows.Scan(&eventType, &profile); err != nil {
			t.Fatal(err)
		}
		events = append(events, eventType+":"+profile)
	}
	return events
}

func storedLLMRequestTask(t *testing.T, home associateHome, requestID string) string {
	t.Helper()
	db, err := sql.Open("sqlite3", home.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var task string
	if err := db.QueryRow(`SELECT COALESCE(json_extract(params_json, '$.task'), '') FROM capture_requests WHERE id = ?`, requestID).Scan(&task); err != nil {
		t.Fatal(err)
	}
	return task
}

type routedAssociateModels struct {
	URL       string
	Client    *http.Client
	mu        sync.Mutex
	calls     map[string]int
	responses map[string]string
	failing   map[string]bool
	last      map[string]string
	hooks     map[string]func()
}

func newRoutedAssociateModels(t *testing.T) *routedAssociateModels {
	t.Helper()
	models := &routedAssociateModels{URL: "http://127.0.0.1:9", calls: map[string]int{}, responses: map[string]string{}, failing: map[string]bool{}, last: map[string]string{}, hooks: map[string]func(){}}
	models.Client = &http.Client{Transport: associateRoundTripper(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		var decoded struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &decoded)
		models.mu.Lock()
		hook := models.hooks[decoded.Model]
		models.mu.Unlock()
		if hook != nil {
			hook()
		}
		models.mu.Lock()
		defer models.mu.Unlock()
		models.calls[decoded.Model]++
		models.last[decoded.Model] = string(body)
		if models.failing[decoded.Model] {
			return nil, errors.New("model unavailable")
		}
		recorder := httptest.NewRecorder()
		_ = json.NewEncoder(recorder).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": models.responses[decoded.Model]}}}})
		return recorder.Result(), nil
	})}
	return models
}

func (models *routedAssociateModels) Respond(model, response string) {
	models.mu.Lock()
	defer models.mu.Unlock()
	models.responses[model] = response
}

func (models *routedAssociateModels) OnCall(model string, hook func()) {
	models.mu.Lock()
	defer models.mu.Unlock()
	models.hooks[model] = hook
}

func (models *routedAssociateModels) Fail(model string) {
	models.mu.Lock()
	defer models.mu.Unlock()
	models.failing[model] = true
}

func (models *routedAssociateModels) Recover(model string) {
	models.mu.Lock()
	defer models.mu.Unlock()
	delete(models.failing, model)
}

func (models *routedAssociateModels) Calls(model string) int {
	models.mu.Lock()
	defer models.mu.Unlock()
	return models.calls[model]
}

func (models *routedAssociateModels) LastRequest(model string) string {
	models.mu.Lock()
	defer models.mu.Unlock()
	return models.last[model]
}
