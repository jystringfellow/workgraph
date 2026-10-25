package facts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestVerifyBacklogSendsOnlyWaitingLinksInTheBand(t *testing.T) {
	home, models, now := initVerifyBacklogHome(t)
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}
	units := proposeVerifyBacklog(t, home, models, now, "C1", 0.97, 0.80, 0.40, 0.70, 0.85)
	decided := currentProjectLink(t, store, units[4].ID)
	if _, err := workgraph.ApproveProjectLink(store, decided.ID); err != nil {
		t.Fatal(err)
	}
	routeBigVerifier(t, home, models)

	corrected := associateEntry(units[1], "project", "atlas-bot", 0.97)
	corrected["summary"] = "Verifier says atlas bot"
	models.Respond("big-model", associateResponse(corrected, associateEntry(units[3], "project", "harbor-harness", 0.75)))
	result, err := workgraph.VerifyLLMBacklog(workgraph.LLMVerifyBacklogConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: models.Client, Now: now})
	if err != nil {
		t.Fatalf("verify backlog: %v", err)
	}
	if models.Calls("big-model") != 1 || result.Waiting != 2 || result.Sent != 2 || result.Verified != 2 || result.Applied != 1 || result.Kept != 1 || result.Corrected != 1 || result.Skipped != 0 || result.Remaining != 0 {
		t.Fatalf("unexpected backlog result, calls=%d: %+v\n%s", models.Calls("big-model"), result, result.Message)
	}
	for _, expected := range []string{"Verifier: big-verifier", "Waiting in the verify band: 2", "Verified: 2", "Applied: 1", "Kept for review: 1", "Corrected: 1", "Skipped: 0", "Project auto-apply accuracy for big-verifier"} {
		if !strings.Contains(result.Message, expected) {
			t.Fatalf("summary omitted %q:\n%s", expected, result.Message)
		}
	}
	request := models.LastRequest("big-model")
	for _, expected := range []string{units[1].ID, units[3].ID, `\"primary_answer\": {`, "thread 1 in C1"} {
		if !strings.Contains(request, expected) {
			t.Fatalf("verifier request omitted %q:\n%s", expected, request)
		}
	}
	for _, leaked := range []string{units[0].ID, units[2].ID, units[4].ID, "thread 0 in C1", "thread 2 in C1", "thread 4 in C1"} {
		if strings.Contains(request, leaked) {
			t.Fatalf("verifier request carried %q, which is not waiting in the band:\n%s", leaked, request)
		}
	}

	applied := currentProjectLink(t, store, units[1].ID)
	if applied.State != "applied" || !applied.Auto || applied.Profile != "big-verifier" || applied.Project != "atlas-bot" || applied.Summary != "Verifier says atlas bot" || applied.RunID != result.RunID {
		t.Fatalf("expected the verifier's answer applied under its profile, got %+v", applied)
	}
	if kept := currentProjectLink(t, store, units[3].ID); kept.State != "proposed" || kept.Profile != "big-verifier" || kept.Project != "harbor-harness" || kept.Confidence != 0.75 {
		t.Fatalf("expected the verifier's uncertain answer kept for review, got %+v", kept)
	}
	if below := currentProjectLink(t, store, units[2].ID); below.State != "proposed" || below.Profile != "local-associate" {
		t.Fatalf("expected the link below the floor untouched, got %+v", below)
	}
	if user := currentProjectLink(t, store, units[4].ID); user.State != "applied" || user.Decision != "approved" || user.Profile != "local-associate" {
		t.Fatalf("expected the user-approved link untouched, got %+v", user)
	}
	explain, err := runworkgraph(t, repoRoot(t), "projects", "explain", units[1].ID, "--home", home.HomeDir, "--memory", home.MemoryDir)
	if err != nil {
		t.Fatalf("projects explain: %v\n%s", err, explain)
	}
	for _, expected := range []string{"  Associate local-associate: project harbor-harness (confidence 0.80)", "  Verifier big-verifier: project atlas-bot (confidence 0.97)"} {
		if !strings.Contains(string(explain), expected) {
			t.Fatalf("explain omitted %q:\n%s", expected, explain)
		}
	}
	if undone, err := workgraph.UndoProjectLinkRun(store, result.RunID); err != nil || len(undone) != 1 || undone[0].UnitID != units[1].ID {
		t.Fatalf("expected projects undo --run to undo the backlog run's applied link, got %+v %v", undone, err)
	}
}

func TestVerifyBacklogDryRunCallsNothing(t *testing.T) {
	home, models, now := initVerifyBacklogHome(t)
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}
	units := proposeVerifyBacklog(t, home, models, now, "C1", 0.80, 0.70, 0.30)
	routeBigVerifier(t, home, models)
	before, err := workgraph.ReadProjectLinks(store, workgraph.ProjectLinksQuery{})
	if err != nil {
		t.Fatal(err)
	}

	output, err := runworkgraph(t, repoRoot(t), "llm", "verify-backlog", "--dry-run", "--limit", "1", "--home", home.HomeDir, "--memory", home.MemoryDir)
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, output)
	}
	for _, expected := range []string{"LLM verify backlog dry run", "Verifier: big-verifier", "Model: big-model", "Destination: " + models.URL + "/v1", "Waiting in the verify band: 2", "Limit: 1", "Would send: 1", "Example request:", "<<<WORKGRAPH_DATA", units[0].ID, `"primary_answer": {`} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("dry run omitted %q:\n%s", expected, output)
		}
	}
	for _, unexpected := range []string{units[1].ID, units[2].ID} {
		if strings.Contains(string(output), unexpected) {
			t.Fatalf("dry-run example carried %q beyond the limit or the band:\n%s", unexpected, output)
		}
	}
	if models.Calls("big-model") != 0 {
		t.Fatalf("dry run called the verifier %d times", models.Calls("big-model"))
	}
	after, err := workgraph.ReadProjectLinks(store, workgraph.ProjectLinksQuery{})
	if err != nil || fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("dry run changed project links: %v\nbefore %+v\nafter %+v", err, before, after)
	}
}

func TestVerifyBacklogRefusesWithoutAUsableVerifier(t *testing.T) {
	home, models, now := initVerifyBacklogHome(t)
	proposeVerifyBacklog(t, home, models, now, "C1", 0.80)
	args := []string{"llm", "verify-backlog", "--home", home.HomeDir, "--memory", home.MemoryDir}

	output, err := runworkgraph(t, repoRoot(t), args...)
	if err == nil || !strings.Contains(string(output), "no llm profile is routed for associate-verify") || !strings.Contains(string(output), "llm use <profile> --for associate-verify") {
		t.Fatalf("expected a refusal without a verifier route: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot(t), append(args, "--dry-run")...); err == nil {
		t.Fatalf("expected the dry run to refuse without a verifier route:\n%s", output)
	}

	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "decider", Provider: "system-one", BaseURL: "http://127.0.0.1:8000", Model: "clef-flash"}); err != nil {
		t.Fatal(err)
	}
	handRouteVerifier(t, home, "decider")
	output, err = runworkgraph(t, repoRoot(t), args...)
	if err == nil || !strings.Contains(string(output), "system-one profiles cannot verify yet") {
		t.Fatalf("expected a hand-routed system-one verifier to be refused: %v\n%s", err, output)
	}

	configureBridgeVerifier(t, home, "codex-verify")
	output, err = runworkgraph(t, repoRoot(t), args...)
	if err == nil || !strings.Contains(string(output), `"codex-verify" for associate-verify runs in the bridge`) {
		t.Fatalf("expected a bridge verifier to be refused: %v\n%s", err, output)
	}
	if models.Calls("big-model") != 0 {
		t.Fatalf("a refused run called a model")
	}
	if link := currentProjectLink(t, workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}, verifyBacklogUnitID("C1", now, 0)); link.Profile != "local-associate" || link.State != "proposed" {
		t.Fatalf("a refused run changed the backlog link: %+v", link)
	}
}

func TestVerifyBacklogIsResumable(t *testing.T) {
	home, models, now := initVerifyBacklogHome(t)
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}
	units := proposeVerifyBacklog(t, home, models, now, "C1", 0.80, 0.81, 0.82)
	routeBigVerifier(t, home, models)
	config := workgraph.LLMVerifyBacklogConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: models.Client, Now: now, Limit: 2}

	models.Respond("big-model", associateResponse(associateEntry(units[0], "project", "harbor-harness", 0.9)))
	first, err := workgraph.VerifyLLMBacklog(config)
	if err != nil || first.Sent != 2 || first.Verified != 1 || first.Kept != 2 || first.Remaining != 1 {
		t.Fatalf("expected two links sent, one verified and one left out, got %+v %v\n%s", first, err, first.Message)
	}
	if link := currentProjectLink(t, store, units[1].ID); link.Profile != "local-associate" || link.State != "proposed" {
		t.Fatalf("expected the link the verifier left out to keep the associate result, got %+v", link)
	}

	models.Fail("big-model")
	failed, err := workgraph.VerifyLLMBacklog(config)
	if err != nil || failed.Waiting != 1 || failed.Verified != 0 || !strings.Contains(failed.Message, "Verifier failed:") || failed.Remaining != 1 {
		t.Fatalf("expected a failed call to leave the link waiting, got %+v %v\n%s", failed, err, failed.Message)
	}
	request := models.LastRequest("big-model")
	if !strings.Contains(request, units[2].ID) || strings.Contains(request, units[0].ID) || strings.Contains(request, units[1].ID) {
		t.Fatalf("expected the second run to send only the link not yet checked:\n%s", request)
	}

	models.Recover("big-model")
	models.Respond("big-model", associateResponse(associateEntry(units[2], "project", "atlas-bot", 0.9)))
	second, err := workgraph.VerifyLLMBacklog(config)
	if err != nil || second.Waiting != 1 || second.Verified != 1 || second.Remaining != 0 {
		t.Fatalf("expected the failed link verified on the next run, got %+v %v\n%s", second, err, second.Message)
	}
	calls := models.Calls("big-model")
	third, err := workgraph.VerifyLLMBacklog(config)
	if err != nil || third.Waiting != 0 || third.Sent != 0 || models.Calls("big-model") != calls {
		t.Fatalf("expected nothing left to verify, got %+v %v calls=%d", third, err, models.Calls("big-model")-calls)
	}
}

func TestVerifyBacklogAppliesThroughTheVerifierAccuracyGate(t *testing.T) {
	home, models, now := initVerifyBacklogHome(t)
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}
	units := proposeVerifyBacklog(t, home, models, now, "C1", 0.80, 0.80)
	routeBigVerifier(t, home, models)
	seedDecidedProfileLinks(t, home, now.Add(-time.Hour), "big-verifier", "gate", 18, 2)
	config := workgraph.LLMVerifyBacklogConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: models.Client, Now: now, Limit: 1}

	models.Respond("big-model", associateResponse(associateEntry(units[0], "project", "harbor-harness", 0.98)))
	paused, err := workgraph.VerifyLLMBacklog(config)
	if err != nil || paused.Verified != 1 || paused.Applied != 0 || paused.Kept != 1 || !strings.Contains(paused.Message, "Auto-apply paused: big-verifier") {
		t.Fatalf("expected the paused verifier's confident answer kept for review, got %+v %v\n%s", paused, err, paused.Message)
	}
	if link := currentProjectLink(t, store, units[0].ID); link.State != "proposed" || link.Profile != "big-verifier" {
		t.Fatalf("expected the verified link proposed while the verifier is paused, got %+v", link)
	}

	setProfileThresholds(t, home, "big-verifier", 0.9, -1)
	models.Respond("big-model", associateResponse(associateEntry(units[1], "project", "harbor-harness", 0.92)))
	resumed, err := workgraph.VerifyLLMBacklog(config)
	if err != nil || resumed.Verified != 1 || resumed.Applied != 1 {
		t.Fatalf("expected the verifier's own threshold to apply once its gate passes, got %+v %v\n%s", resumed, err, resumed.Message)
	}
	if link := currentProjectLink(t, store, units[1].ID); link.State != "applied" || !link.Auto || link.Profile != "big-verifier" {
		t.Fatalf("expected the verified link applied, got %+v", link)
	}
}

func TestVerifyBacklogReusesTheSnapshotTheAssociateSaw(t *testing.T) {
	home := initScheduleHome(t)
	writeInventedProjects(t, home)
	managedPath := filepath.Join(t.TempDir(), "managed-settings.json")
	writeManagedPatterns(t, managedPath, `"ORBIT-[0-9]{4}-SECRET"`)
	restore := workgraph.SetManagedSettingsPathForTest(managedPath)
	defer restore()
	configureBridgeAssociateProfile(t, home, "slack")
	start := time.Now().UTC().Add(-6 * time.Hour)
	ts := fmt.Sprintf("%d.000100", start.Unix())
	event := associateSlackEvent("C1", ts, "channel", "harness-team", "Sam", "Rotate ORBIT-1234-SECRET before launch", start)
	insertEvent(t, home.DatabasePath, event)
	unit := workgraph.ProjectUnit{ID: "slack.thread:C1:" + ts, EventIDs: []string{event.ID}}
	if emitted, err := workgraph.ScheduleLLMAssociate(workgraph.LLMAssociateScheduleConfig{HomeDir: home.HomeDir}); err != nil || !emitted.Created {
		t.Fatalf("emit: %+v %v", emitted, err)
	}
	claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{HomeDir: home.HomeDir, Kind: "llm_task", Worker: "claude-code-drain-fact", MemoryDir: home.MemoryDir})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim: %+v %v", claims, err)
	}
	response := associateResponse(associateEntry(unit, "project", "harbor-harness", 0.8))
	if _, err := workgraph.SubmitLLMTask(workgraph.LLMTaskSubmitConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, RequestID: claims[0].Request.ID, ClaimToken: claims[0].ClaimToken, Response: response}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if link := currentProjectLink(t, workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}, unit.ID); link.State != "proposed" || link.Profile != "claude-bridge" {
		t.Fatalf("expected a waiting associate proposal, got %+v", link)
	}

	writeManagedPatterns(t, managedPath, ``)
	verifier := newRecordingVerifier(t)
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "local-verify", Provider: "openai-compatible", BaseURL: verifier.URL + "/v1", Model: "local-verify-model"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.UseLLMProfile(workgraph.LLMUseProfileConfig{HomeDir: home.HomeDir, Name: "local-verify", Task: "associate-verify"}); err != nil {
		t.Fatal(err)
	}
	result, err := workgraph.VerifyLLMBacklog(workgraph.LLMVerifyBacklogConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir})
	if err != nil || result.Sent != 1 {
		t.Fatalf("verify backlog: %+v %v", result, err)
	}
	requests := verifier.Requests()
	if len(requests) != 1 || !strings.Contains(requests[0], "[REDACTED:managed-pattern]") || strings.Contains(requests[0], "ORBIT-1234-SECRET") {
		t.Fatalf("expected the verifier to receive the redaction the associate saw:\n%s", strings.Join(requests, "\n"))
	}
}

func TestScheduledAssociatePassDrainsABoundedBacklog(t *testing.T) {
	home, models, now := initVerifyBacklogHome(t)
	var confidences []float64
	for i := 0; i < 12; i++ {
		confidences = append(confidences, 0.8)
	}
	proposeVerifyBacklog(t, home, models, now, "C1", confidences...)
	routeBigVerifier(t, home, models)
	models.Respond("big-model", `{"schema":"workgraph.associate_result.v1","units":[]}`)
	schedule := workgraph.LLMAssociateScheduleConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: models.Client, Now: now}

	drained, ran, err := workgraph.ScheduleLLMVerifyBacklog(schedule)
	if err != nil || !ran || drained.Sent != 10 || drained.Remaining != 2 || models.Calls("big-model") != 1 {
		t.Fatalf("expected the scheduled pass to drain ten links in one call, got %+v ran=%t calls=%d %v", drained, ran, models.Calls("big-model"), err)
	}
	if output, err := runworkgraph(t, repoRoot(t), "llm", "verify-backlog", "--schedule", "off", "--home", home.HomeDir); err != nil || !strings.Contains(string(output), "Scheduled backlog verification: off") {
		t.Fatalf("turn scheduled backlog verification off: %v\n%s", err, output)
	}
	if _, ran, err := workgraph.ScheduleLLMVerifyBacklog(schedule); err != nil || ran || models.Calls("big-model") != 1 {
		t.Fatalf("expected no scheduled drain once it is off, ran=%t calls=%d %v", ran, models.Calls("big-model"), err)
	}
}

func TestRecentDecisionsAreDistinctAndCounted(t *testing.T) {
	home := initAssociateHome(t)
	managedPath := filepath.Join(t.TempDir(), "managed-settings.json")
	writeManagedPatterns(t, managedPath, `"ORBIT-[0-9]{4}-SECRET"`)
	restore := workgraph.SetManagedSettingsPathForTest(managedPath)
	defer restore()
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "hosted-associate", Provider: "openai-compatible", BaseURL: "https://models.example.test/v1", Model: "hosted-model"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.UseLLMProfile(workgraph.LLMUseProfileConfig{HomeDir: home.HomeDir, Name: "hosted-associate", Task: "associate"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.EnableHostedLLM(workgraph.LLMHostedConfig{HomeDir: home.HomeDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.ConfigureLLMSources(workgraph.LLMSourcesConfig{HomeDir: home.HomeDir, Task: "associate", Enable: []string{"slack"}}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-48 * time.Hour)
	decide := func(index int, channelID, conversationType, container, project string) {
		at := start.Add(time.Duration(index) * time.Minute)
		ts := fmt.Sprintf("%d.000100", at.Unix())
		event := associateSlackEvent(channelID, ts, conversationType, container, "Sam", "decided work "+ts, at)
		insertEvent(t, home.DatabasePath, event)
		store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, Now: at}
		if _, err := workgraph.AssignProjectLink(store, workgraph.ProjectLinkAssignment{UnitID: "slack.thread:" + channelID + ":" + ts, UnitKind: "slack.thread", Container: container, Evidence: []string{event.ID}, Target: "project", Project: project, Summary: "Decided"}); err != nil {
			t.Fatal(err)
		}
	}
	distinct := []struct{ container, project string }{
		{"orbit-team", "orbit-segments"}, {"atlas-team", "atlas-bot"}, {"harness-team", "atlas-bot"},
		{"widgets-team", "harbor-harness"}, {"launch-ORBIT-4321-SECRET", "orbit-segments"}, {"review-team", "atlas-bot"},
	}
	for index, entry := range distinct {
		decide(index, fmt.Sprintf("C%d", index), "channel", entry.container, entry.project)
	}
	for index := 0; index < 15; index++ {
		decide(100+index, "C900", "channel", "harness-team", "harbor-harness")
	}
	decide(200, "D900", "im", "harness-team", "harbor-harness")
	decide(201, "D901", "im", "private-chat", "atlas-bot")

	result, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if strings.Contains(result.Message, "ORBIT-4321-SECRET") || !strings.Contains(result.Message, "launch-[REDACTED:managed-pattern]") {
		t.Fatalf("expected the outbound filter to redact a decision container:\n%s", result.Message)
	}
	if strings.Contains(result.Message, "private-chat") {
		t.Fatalf("a decision from a disabled source reached the payload:\n%s", result.Message)
	}
	_, rest, _ := strings.Cut(result.Message, "<<<WORKGRAPH_DATA\n")
	data, _, _ := strings.Cut(rest, "\nWORKGRAPH_DATA>>>")
	var payload struct {
		Decisions []struct {
			Container string `json:"container"`
			Decision  string `json:"decision"`
			Target    string `json:"target"`
			Project   string `json:"project"`
			Kind      string `json:"kind"`
			Count     *int   `json:"count"`
		} `json:"recent_decisions"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		t.Fatalf("parse payload: %v\n%s", err, data)
	}
	if len(payload.Decisions) != 7 {
		t.Fatalf("expected one counted entry for the repeated decision and every distinct one, got %+v", payload.Decisions)
	}
	seen := map[string]int{}
	for _, decision := range payload.Decisions {
		if decision.Count == nil {
			t.Fatalf("decision without a count: %+v", decision)
		}
		key := decision.Container + "->" + decision.Project
		if _, repeated := seen[key]; repeated {
			t.Fatalf("decision %s appears twice: %+v", key, payload.Decisions)
		}
		seen[key] = *decision.Count
	}
	if seen["harness-team->harbor-harness"] != 15 {
		t.Fatalf("expected the repeated decision counted 15 times, without the one from a disabled source, got %v", seen)
	}
	for _, entry := range distinct {
		container := strings.Replace(entry.container, "ORBIT-4321-SECRET", "[REDACTED:managed-pattern]", 1)
		if seen[container+"->"+entry.project] != 1 {
			t.Fatalf("expected the distinct decision %s->%s once, got %v", container, entry.project, seen)
		}
	}
	if !strings.Contains(result.Message, "each is a distinct decision, and count is how many times the user made it") {
		t.Fatalf("expected the prompt to explain the decision counts:\n%s", result.Message)
	}
}

func initVerifyBacklogHome(t *testing.T) (associateHome, *routedAssociateModels, time.Time) {
	t.Helper()
	home := initAssociateHome(t)
	writeInventedProjects(t, home)
	models := newRoutedAssociateModels(t)
	configureAssociateProfile(t, home, models.URL, "slack")
	return home, models, time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
}

func proposeVerifyBacklog(t *testing.T, home associateHome, models *routedAssociateModels, now time.Time, channelID string, confidences ...float64) []workgraph.ProjectUnit {
	t.Helper()
	units := insertAssociateThreads(t, home, now.Add(-6*time.Hour), channelID, "harness-team", len(confidences))
	var entries []map[string]any
	for index, confidence := range confidences {
		entries = append(entries, associateEntry(units[index], "project", "harbor-harness", confidence))
	}
	models.Respond("small-model", associateResponse(entries...))
	if _, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: models.Client, Now: now.Add(-time.Hour)}); err != nil {
		t.Fatalf("associate: %v", err)
	}
	return units
}

func verifyBacklogUnitID(channelID string, now time.Time, index int) string {
	return fmt.Sprintf("slack.thread:%s:%d.000100", channelID, now.Add(-6*time.Hour).Unix()+int64(index))
}

func routeBigVerifier(t *testing.T, home associateHome, models *routedAssociateModels) {
	t.Helper()
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "big-verifier", Provider: "openai-compatible", BaseURL: models.URL + "/v1", Model: "big-model"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.UseLLMProfile(workgraph.LLMUseProfileConfig{HomeDir: home.HomeDir, Name: "big-verifier", Task: "associate-verify"}); err != nil {
		t.Fatal(err)
	}
}

func handRouteVerifier(t *testing.T, home associateHome, profile string) {
	t.Helper()
	path := filepath.Join(home.HomeDir, "llm.json")
	var stored map[string]any
	contents, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(contents, &stored) != nil {
		t.Fatalf("read llm.json: %v", err)
	}
	stored["task_profiles"].(map[string]any)["associate-verify"] = profile
	edited, _ := json.MarshalIndent(stored, "", "  ")
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatal(err)
	}
}
