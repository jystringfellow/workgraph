package facts

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestDeterministicProjectLinksResolveInOrder(t *testing.T) {
	now := time.Now().UTC()
	base := now.Add(-6 * time.Hour)
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "atlas-bot", "# Atlas Bot\n\nShipping https://github.com/Acme/Atlas/pull/7 under AB#1234567.\nFix landed in https://github.com/acme/atlas/commit/abcdef1234567890.\nSpec: https://www.notion.so/acme/Roadmap-0123456789abcdef0123456789abcdef\nSign in at https://www.notion.so/login\n")
	writeProjectMemory(t, home.MemoryDir, "orbit-segments", "---\nworkstream: harbor\n---\n# Orbit Segments\n\nTracked as AB#1234567.\n")
	writeProjectMemory(t, home.MemoryDir, "harbor-harness", "# Harbor Harness\n")

	events := []storedEvent{
		slackLinkEvent("C123", "1000.000100", "1000.000100", "can you look at https://github.com/acme/atlas/pull/7/files", base, "harness-team"),
		slackLinkEvent("C123", "1000.000200", "1000.000100", "sure", base.Add(5*time.Minute), "harness-team"),
		slackLinkEvent("C123", "2000.000100", "", "lunch?", base.Add(time.Hour), "harness-team"),
		slackLinkEvent("C123", "3000.000100", "3000.000100", "status of AB#1234567?", base.Add(90*time.Minute), "harness-team"),
		slackLinkEvent("C999", "4000.000100", "4000.000100", "acme/other#5 is flaky", base.Add(2*time.Hour), "random"),
		slackLinkEvent("C999", "5000.000100", "5000.000100", "still acme/other#5", base.Add(2*time.Hour+time.Minute), "random"),
		slackLinkEvent("C999", "6000.000100", "6000.000100", "acme/other#5 again", base.Add(2*time.Hour+2*time.Minute), "random"),
		slackLinkEvent("C999", "7000.000100", "7000.000100", "reverted https://github.com/acme/atlas/commit/abcdef1", base.Add(2*time.Hour+3*time.Minute), "random"),
		slackLinkEvent("C999", "9000.000100", "9000.000100", "log in at https://www.notion.so/login", base.Add(2*time.Hour+5*time.Minute), "random"),
		{ID: "notion.page_updated:0123:1", Source: "notion", Type: "notion.page_updated", Timestamp: base.Add(3 * time.Hour), Payload: `{"object":"page","id":"01234567-89ab-cdef-0123-456789abcdef"}`},
		{ID: "github.pull_request:acme/a:1", Source: "github", Type: "github.pull_request", Timestamp: base.Add(3 * time.Hour), Project: "a", Payload: `{"repository":"acme/a","number":1,"head_ref":"feature","title":"Fix","body":"relates to https://github.com/acme/b/issues/9"}`},
		{ID: "github.pull_request:acme/a:3", Source: "github", Type: "github.pull_request", Timestamp: base.Add(3 * time.Hour), Project: "a", Payload: `{"repository":"acme/a","number":3,"head_ref":"feature","title":"Follow-up"}`},
		{ID: "github.pull_request:acme/b:2", Source: "github", Type: "github.pull_request", Timestamp: base.Add(3 * time.Hour), Project: "b", Payload: `{"repository":"acme/b","number":2,"head_ref":"feature","title":"Unrelated"}`},
		{ID: "slack.message:C999:8000.000100", Source: "slack", Type: "slack.message", Timestamp: base.Add(2*time.Hour + 4*time.Minute), Project: "random", Payload: `{"channel_id":"C999","ts":"8000.000100","thread_ts":"8000.000100","text":"see attachment","untrusted":{"text":"https://github.com/acme/atlas/pull/7"}}`},
	}
	insertProjectUnitEvent(t, home.DatabasePath, projectContainerFileEvent("harness-team", base))
	if output, err := runworkgraph(t, repoRoot(t), "memory", "link", "--home", home.HomeDir, "--memory", home.MemoryDir, "--source", "harness-team", "harbor-harness"); err != nil {
		t.Fatalf("map container: %v %s", err, output)
	}
	for _, event := range events {
		insertProjectUnitEvent(t, home.DatabasePath, event)
	}

	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	seeded, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{
		Source:  "llm",
		Profile: "hosted-mini",
		Links: []workgraph.ProjectLinkProposal{
			{UnitID: "slack.thread:C999:4000.000100", UnitKind: "slack.thread", Container: "random", Target: "project", Project: "atlas-bot", Kind: "fyi", Summary: "Flaky test", Confidence: 0.97, Evidence: []string{"slack.message:C999:4000.000100"}, Apply: true},
			{UnitID: "slack.thread:C999:5000.000100", UnitKind: "slack.thread", Container: "random", Target: "project", Project: "atlas-bot", Kind: "fyi", Summary: "Flaky test", Confidence: 0.80, Evidence: []string{"slack.message:C999:5000.000100"}},
		},
	})
	if err != nil {
		t.Fatalf("seed llm links: %v", err)
	}

	if _, err := workgraph.AssignProjectLink(store, workgraph.ProjectLinkAssignment{UnitID: "github.pr:acme/a#1", UnitKind: "github.pr", Container: "a", Evidence: []string{"github.pull_request:acme/a:1"}, Target: "project", Project: "atlas-bot"}); err != nil {
		t.Fatalf("seed user link: %v", err)
	}

	pass := workgraph.ProjectLinkPassConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, MemoryDir: home.MemoryDir, Now: now}
	first, err := workgraph.LinkProjectUnits(pass)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	got := projectLinksByUnit(first.Written)
	expected := map[string][2]string{
		"slack.thread:C123:1000.000100": {"atlas-bot", "shared_identifier"},
		"slack.burst:C123:2000.000100":  {"harbor-harness", "container_mapping"},
		"slack.thread:C999:7000.000100": {"atlas-bot", "shared_identifier"},
		"github.pr:acme/a#3":            {"atlas-bot", "shared_identifier"},
		"notion.page:01234567-89ab-cdef-0123-456789abcdef:" + base.Add(3*time.Hour).Local().Format(time.DateOnly): {"atlas-bot", "shared_identifier"},
	}
	if len(got) != len(expected) {
		t.Fatalf("expected links for %v, got %+v", expected, first.Written)
	}
	for unitID, want := range expected {
		link, ok := got[unitID]
		if !ok || link.Project != want[0] || link.Rule != want[1] {
			t.Fatalf("expected %s linked to %s by %s, got %+v", unitID, want[0], want[1], link)
		}
		if link.Source != "deterministic" || link.State != "applied" || !link.Auto || link.Confidence != 1 || link.RunID == "" || link.RunID != first.RunID {
			t.Fatalf("expected an applied auto deterministic link with the pass run id, got %+v", link)
		}
	}
	if evidence := got["slack.thread:C123:1000.000100"].Evidence; len(evidence) != 1 || evidence[0] != "slack.message:C123:1000.000100" {
		t.Fatalf("expected shared identifier evidence to cite the message, got %v", evidence)
	}
	for _, unitID := range []string{"slack.thread:C123:3000.000100", "slack.thread:C999:6000.000100", "slack.thread:C999:8000.000100", "slack.thread:C999:9000.000100", "github.pr:acme/b#2"} {
		if links, _ := workgraph.ReadProjectLinks(store, workgraph.ProjectLinksQuery{UnitID: unitID}); len(links) != 0 {
			t.Fatalf("expected no link for %s, got %+v", unitID, links)
		}
	}

	if _, err := workgraph.ApproveProjectLink(store, seeded.Written[1].ID); err != nil {
		t.Fatalf("approve seeded link: %v", err)
	}
	second, err := workgraph.LinkProjectUnits(pass)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if len(second.Written) != 1 || second.Written[0].UnitID != "slack.thread:C999:6000.000100" || second.Written[0].Project != "atlas-bot" || second.Written[0].Rule != "shared_identifier" || second.RunID == first.RunID {
		t.Fatalf("expected an approved link to seed the shared identifier in a new run, got %+v", second)
	}

	third, err := workgraph.LinkProjectUnits(pass)
	if err != nil {
		t.Fatalf("third pass: %v", err)
	}
	if len(third.Written) != 0 || third.RunID != "" {
		t.Fatalf("expected a pass with nothing to link to write nothing, got %+v", third)
	}
}

func projectContainerFileEvent(container string, at time.Time) storedEvent {
	return storedEvent{ID: "file.modified:" + container, Source: "file", Type: "file.modified", Timestamp: at, Project: container, Payload: `{"path":"/work/` + container + `/notes.md"}`, Summary: "notes"}
}

type projectLinkingHome struct {
	HomeDir      string
	MemoryDir    string
	DatabasePath string
}

func initProjectLinkingHome(t *testing.T) projectLinkingHome {
	t.Helper()
	return initProjectLinkingHomeAt(t, t.TempDir())
}

func initProjectLinkingHomeAt(t *testing.T, root string) projectLinkingHome {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	globalGitConfig := filepath.Join(root, "gitconfig")
	if err := os.WriteFile(globalGitConfig, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("GIT_CONFIG_GLOBAL", globalGitConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	home, memory := filepath.Join(root, "state"), filepath.Join(root, "memory")
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: home, MemoryDir: memory})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	return projectLinkingHome{HomeDir: home, MemoryDir: memory, DatabasePath: result.DatabasePath}
}

func slackLinkEvent(channelID, ts, threadTS, text string, timestamp time.Time, container string) storedEvent {
	eventType := "slack.message"
	if threadTS != "" && threadTS != ts {
		eventType = "slack.thread_reply"
	}
	fields := map[string]string{"channel_id": channelID, "ts": ts, "text": text}
	if threadTS != "" {
		fields["thread_ts"] = threadTS
	}
	payload, _ := json.Marshal(fields)
	return storedEvent{
		ID:        fmt.Sprintf("%s:%s:%s", eventType, channelID, ts),
		Source:    "slack",
		Type:      eventType,
		Timestamp: timestamp,
		Project:   container,
		Payload:   string(payload),
		Summary:   text,
	}
}

func projectLinksByUnit(links []workgraph.ProjectLink) map[string]workgraph.ProjectLink {
	byUnit := map[string]workgraph.ProjectLink{}
	for _, link := range links {
		byUnit[link.UnitID] = link
	}
	return byUnit
}

func TestProjectsDefaultReviewListsAutoLinksUntilTwentyDecided(t *testing.T) {
	now := time.Now().UTC()
	home := initProjectLinkingHome(t)
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	if _, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "deterministic", Links: []workgraph.ProjectLinkProposal{projectLinkProposal("unit-det", "harbor-harness", "container_mapping", true)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "hosted-mini", Links: []workgraph.ProjectLinkProposal{
		reviewLLMProposal("unit-llm", 0.97, true),
		reviewLLMProposal("unit-prop", 0.80, false),
	}}); err != nil {
		t.Fatal(err)
	}
	assertReviewUnits(t, workgraph.ProjectReviewConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}, "unit-prop", "unit-llm")

	sample := make([]workgraph.ProjectLinkProposal, 20)
	for i := range sample {
		sample[i] = reviewLLMProposal(fmt.Sprintf("sample-%02d", i), 0.99, true)
	}
	run, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "hosted-mini", Links: sample})
	if err != nil {
		t.Fatal(err)
	}
	stale := store
	stale.Now = now.Add(-15 * 24 * time.Hour)
	for _, link := range run.Written[:10] {
		if _, err := workgraph.ApproveProjectLink(stale, link.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, link := range run.Written[10:15] {
		if _, err := workgraph.ApproveProjectLink(store, link.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, link := range run.Written[15:18] {
		if _, err := workgraph.AssignProjectLink(store, workgraph.ProjectLinkAssignment{UnitID: link.UnitID, Target: "project", Project: "atlas-bot"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := workgraph.UndoProjectLink(store, run.Written[18].ID); err != nil {
		t.Fatal(err)
	}
	future := store
	future.Now = now.Add(time.Hour)
	for i := 0; i < 11; i++ {
		ahead, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "hosted-mini", Links: []workgraph.ProjectLinkProposal{reviewLLMProposal(fmt.Sprintf("future-%02d", i), 0.99, true)}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := workgraph.ApproveProjectLink(future, ahead.Written[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	review := workgraph.ProjectReviewConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	assertReviewUnits(t, review, "unit-prop", "sample-18", "unit-llm", "sample-19")

	if _, err := workgraph.UndoProjectLink(store, run.Written[19].ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		extra, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "hosted-mini", Links: []workgraph.ProjectLinkProposal{reviewLLMProposal(fmt.Sprintf("extra-%02d", i), 0.99, true)}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := workgraph.ApproveProjectLink(store, extra.Written[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	assertReviewUnits(t, review, "unit-prop", "sample-18", "sample-19")

	auto := review
	auto.Auto = true
	links, err := workgraph.ListProjectReview(auto)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, link := range links {
		if !link.Auto || link.State != "applied" {
			t.Fatalf("expected only applied auto links in --auto, got %+v", link)
		}
		listed[link.UnitID] = true
	}
	if !listed["unit-det"] || !listed["unit-llm"] || listed["unit-prop"] {
		t.Fatalf("expected --auto to keep undecided auto links after the sample reached 20, got %v", listed)
	}
	if len(links) < 2 || links[0].ID < links[len(links)-1].ID {
		t.Fatalf("expected --auto newest first, got %+v", links)
	}
	auto.Now = now.Add(25 * time.Hour)
	if links, err := workgraph.ListProjectReview(auto); err != nil || len(links) != 0 {
		t.Fatalf("expected --auto to default to the last 24 hours, got %+v %v", links, err)
	}
	auto.Since = 48 * time.Hour
	if links, err := workgraph.ListProjectReview(auto); err != nil || len(links) == 0 {
		t.Fatalf("expected --since to widen the window, got %+v %v", links, err)
	}
}

func reviewLLMProposal(unitID string, confidence float64, apply bool) workgraph.ProjectLinkProposal {
	return workgraph.ProjectLinkProposal{UnitID: unitID, UnitKind: "slack.thread", Container: "harness-team", Target: "project", Project: "harbor-harness", Kind: "build", Summary: "Built " + unitID, Confidence: confidence, Evidence: []string{"slack.message:C123:" + unitID}, Apply: apply}
}

func assertReviewUnits(t *testing.T, config workgraph.ProjectReviewConfig, expected ...string) {
	t.Helper()
	links, err := workgraph.ListProjectReview(config)
	if err != nil {
		t.Fatalf("list review: %v", err)
	}
	var units []string
	for _, link := range links {
		units = append(units, link.UnitID)
	}
	if fmt.Sprint(units) != fmt.Sprint(expected) {
		t.Fatalf("expected review %v, got %v", expected, units)
	}
}

func TestProjectsReviewCommandsRecordDecisions(t *testing.T) {
	now := time.Now().UTC()
	base := now.Add(-6 * time.Hour)
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "atlas-bot", "# Atlas Bot\n\nShips https://github.com/acme/atlas/pull/7.\n")
	writeProjectMemory(t, home.MemoryDir, "harbor-harness", "# Harbor Harness\n")
	insertProjectUnitEvent(t, home.DatabasePath, projectContainerFileEvent("harness-team", base))
	if output, err := runworkgraph(t, repoRoot(t), "memory", "link", "--home", home.HomeDir, "--memory", home.MemoryDir, "--source", "harness-team", "harbor-harness"); err != nil {
		t.Fatalf("map container: %v %s", err, output)
	}
	for _, event := range []storedEvent{
		slackLinkEvent("C123", "1000.000100", "1000.000100", "review https://github.com/acme/atlas/pull/7", base, "harness-team"),
		slackLinkEvent("C123", "2000.000100", "", "lunch?", base.Add(time.Hour), "harness-team"),
		slackLinkEvent("C123", "3000.000100", "3000.000100", "harness flake", base.Add(90*time.Minute), "harness-team"),
		slackLinkEvent("C999", "4000.000100", "4000.000100", "agent tooling over GraphQL", base.Add(2*time.Hour), "random"),
		slackLinkEvent("C999", "5000.000100", "5000.000100", "new idea", base.Add(2*time.Hour+time.Minute), "random"),
		slackLinkEvent("C999", "6000.000100", "6000.000100", "gateway sketch", base.Add(2*time.Hour+2*time.Minute), "random"),
	} {
		insertProjectUnitEvent(t, home.DatabasePath, event)
	}
	args := []string{"--home", home.HomeDir, "--memory", home.MemoryDir}
	projects := func(input string, extra ...string) string {
		t.Helper()
		output, err := runworkgraphInput(t, repoRoot(t), input, append(append([]string{"projects"}, extra...), args...)...)
		if err != nil {
			t.Fatalf("projects %v: %v %s", extra, err, output)
		}
		return string(output)
	}
	eventsBefore := projectUnitEventsSnapshot(t, home.DatabasePath)

	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath}
	llm, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "hosted-mini", Links: []workgraph.ProjectLinkProposal{
		{UnitID: "slack.thread:C999:4000.000100", UnitKind: "slack.thread", Container: "random", Target: "project", Project: "harbor-harness", Kind: "advice", Summary: "Advised on agent tooling", Confidence: 0.97, Evidence: []string{"slack.message:C999:4000.000100"}, Apply: true},
		{UnitID: "slack.thread:C999:5000.000100", UnitKind: "slack.thread", Container: "random", Target: "project", Project: "atlas-bot", Kind: "build", Summary: "Sketched an idea", Confidence: 0.80, Evidence: []string{"slack.message:C999:5000.000100"}},
		{UnitID: "slack.thread:C999:6000.000100", UnitKind: "slack.thread", Container: "random", Target: "new_project", NewProject: &workgraph.ProposedProject{Slug: "graphql-gateway", Name: "GraphQL Gateway", Workstream: "harbor"}, Kind: "build", Summary: "Sketched a gateway", Confidence: 0.90, Evidence: []string{"slack.message:C999:6000.000100"}},
	}})
	if err != nil {
		t.Fatalf("record llm run: %v", err)
	}
	llmAuto, llmProposed, llmNewProject := llm.Written[0], llm.Written[1], llm.Written[2]

	listed := projects("", "review")
	links := projectLinksByUnit(readCurrentProjectLinks(t, store))
	shared, burst, mapped := links["slack.thread:C123:1000.000100"], links["slack.burst:C123:2000.000100"], links["slack.thread:C123:3000.000100"]
	if shared.Rule != "shared_identifier" || burst.Rule != "container_mapping" || mapped.Rule != "container_mapping" || shared.RunID == "" || shared.RunID != burst.RunID {
		t.Fatalf("expected review to run one deterministic pass, got %+v", links)
	}
	for _, expected := range []string{fmt.Sprintf("Link %d", llmProposed.ID), fmt.Sprintf("Link %d", llmAuto.ID), "Sketched an idea", "evidence: slack.message:C999:5000.000100", "3 left to decide (workgraph projects review)."} {
		if !strings.Contains(listed, expected) {
			t.Fatalf("default review omitted %q:\n%s", expected, listed)
		}
	}
	for _, deterministic := range []workgraph.ProjectLink{shared, burst, mapped} {
		if strings.Contains(listed, fmt.Sprintf("Link %d:", deterministic.ID)) {
			t.Fatalf("default review listed deterministic link %d:\n%s", deterministic.ID, listed)
		}
	}
	if strings.Index(listed, fmt.Sprintf("Link %d", llmProposed.ID)) > strings.Index(listed, fmt.Sprintf("Link %d", llmAuto.ID)) {
		t.Fatalf("expected proposed links before undecided auto links:\n%s", listed)
	}

	autoList := projects("", "review", "--auto")
	for _, expected := range []string{"run " + shared.RunID, "rule shared_identifier", "run " + llm.RunID, "profile hosted-mini", "Advised on agent tooling"} {
		if !strings.Contains(autoList, expected) {
			t.Fatalf("review --auto omitted %q:\n%s", expected, autoList)
		}
	}
	if strings.Index(autoList, fmt.Sprintf("Link %d", mapped.ID)) > strings.Index(autoList, fmt.Sprintf("Link %d", llmAuto.ID)) {
		t.Fatalf("expected review --auto newest first:\n%s", autoList)
	}
	projects("s\ns\ns\nr\natlas-bot\n", "review", "--auto")
	assertProjectLinkRow(t, store, llmAuto.ID, "rejected", "corrected")
	if current := currentProjectLink(t, store, llmAuto.UnitID); current.Source != "user" || current.Project != "atlas-bot" || current.State != "applied" {
		t.Fatalf("expected r to reassign the LLM link to atlas-bot, got %+v", current)
	}

	if quit := projects("x\nq\n", "review"); !strings.Contains(quit, "1 left to decide (workgraph projects review).") {
		t.Fatalf("expected quitting to say what is left to decide:\n%s", quit)
	}
	assertProjectLinkRow(t, store, llmProposed.ID, "rejected", "corrected")
	if current := currentProjectLink(t, store, llmProposed.UnitID); current.Source != "user" || current.Target != "none" {
		t.Fatalf("expected x to assign no project, got %+v", current)
	}
	projects("", "review", "--approve", fmt.Sprint(burst.ID))
	if current := currentProjectLink(t, store, burst.UnitID); current.ID != burst.ID || current.Decision != "confirmed" || current.State != "applied" {
		t.Fatalf("expected a to confirm the auto link, got %+v", current)
	}

	explained := projects("", "explain", mapped.UnitID)
	for _, expected := range []string{"slack.thread:C123:3000.000100", "slack.message:C123:3000.000100", fmt.Sprintf("Link %d", mapped.ID), "source deterministic", "rule container_mapping", "run " + mapped.RunID,
		"workgraph projects review --assign slack.thread:C123:3000.000100 --project <slug> --home " + home.HomeDir + " --memory " + home.MemoryDir,
		fmt.Sprintf("workgraph projects review --reject %d --home %s --memory %s\n", mapped.ID, home.HomeDir, home.MemoryDir), fmt.Sprintf("workgraph projects undo %d --home %s --memory %s", mapped.ID, home.HomeDir, home.MemoryDir)} {
		if !strings.Contains(explained, expected) {
			t.Fatalf("explain omitted %q:\n%s", expected, explained)
		}
	}

	if out := projects("", "undo", fmt.Sprint(shared.ID)); !strings.Contains(out, "slack.thread:C123:1000.000100") || !strings.Contains(out, "workgraph projects review --home "+home.HomeDir+" --memory "+home.MemoryDir) {
		t.Fatalf("undo output: %s", out)
	}
	replacement := currentProjectLink(t, store, shared.UnitID)
	if replacement.State != "proposed" || replacement.Project != "atlas-bot" || replacement.Source != "deterministic" {
		t.Fatalf("expected a proposed replacement after undo, got %+v", replacement)
	}
	projects("", "review", "--approve", fmt.Sprint(replacement.ID))
	assertProjectLinkRow(t, store, replacement.ID, "applied", "approved")
	if approved := currentProjectLink(t, store, shared.UnitID); approved.Auto || approved.Source != "deterministic" {
		t.Fatalf("expected an approved link to keep its source and clear auto, got %+v", approved)
	}

	projects("", "review", "--approve", fmt.Sprint(llmNewProject.ID))
	if approved := currentProjectLink(t, store, llmNewProject.UnitID); approved.ID != llmNewProject.ID || approved.Target != "project" || approved.Project != "graphql-gateway" || approved.Source != "llm" || approved.Decision != "approved" || approved.Auto {
		t.Fatalf("expected an approved new project link to target the new project, got %+v", approved)
	}

	projects("", "review", "--reject", fmt.Sprint(burst.ID))
	assertProjectLinkRow(t, store, burst.ID, "rejected", "corrected")
	projects("", "review", "--assign", burst.UnitID, "--project", "atlas-bot")
	if current := currentProjectLink(t, store, burst.UnitID); current.Source != "user" || current.Project != "atlas-bot" {
		t.Fatalf("expected --assign to apply a user link, got %+v", current)
	}
	if output, err := runworkgraph(t, repoRoot(t), append([]string{"projects", "review", "--assign", burst.UnitID, "--project", "missing"}, args...)...); err == nil {
		t.Fatalf("expected an unknown project to be refused: %s", output)
	}

	projects("", "review", "--assign", llmProposed.UnitID, "--new", "agent-tooling", "--workstream", "harbor")
	catalog, err := workgraph.LoadProjectCatalog(home.MemoryDir)
	if err != nil {
		t.Fatal(err)
	}
	created, approvedNew := false, false
	for _, entry := range catalog.Projects {
		created = created || (entry.Slug == "agent-tooling" && entry.Workstream == "harbor")
		approvedNew = approvedNew || (entry.Slug == "graphql-gateway" && entry.Workstream == "harbor")
	}
	if !created || !approvedNew || len(catalog.Problems) != 0 {
		t.Fatalf("expected --new to create project memory in workstream harbor, got %+v", catalog)
	}
	if current := currentProjectLink(t, store, llmProposed.UnitID); current.Source != "user" || current.Project != "agent-tooling" {
		t.Fatalf("expected --new to apply a user link, got %+v", current)
	}
	if output, err := runworkgraph(t, repoRoot(t), append([]string{"projects", "review", "--assign", llmProposed.UnitID, "--new", "atlas-bot"}, args...)...); err == nil {
		t.Fatalf("expected --new to refuse an existing project: %s", output)
	}
	userExplained := projects("", "explain", llmProposed.UnitID)
	if !strings.Contains(userExplained, "source user") || !strings.Contains(userExplained, "decision corrected") || strings.Contains(userExplained, "workgraph projects undo") {
		t.Fatalf("expected explain to list every row and no undo for a user link:\n%s", userExplained)
	}

	if out := projects("", "undo", "--run", mapped.RunID); !strings.Contains(out, mapped.UnitID) {
		t.Fatalf("undo --run output: %s", out)
	}
	undone := currentProjectLink(t, store, mapped.UnitID)
	if undone.State != "proposed" {
		t.Fatalf("expected undo --run to leave a proposal, got %+v", undone)
	}
	projects("", "review")
	if again := currentProjectLink(t, store, mapped.UnitID); again.ID != undone.ID || again.State != "proposed" {
		t.Fatalf("expected a later pass not to reapply an undone unit, got %+v", again)
	}

	var decoded struct {
		Items []struct {
			LinkID int64  `json:"link_id"`
			UnitID string `json:"unit_id"`
			State  string `json:"state"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(projects("", "review", "--json")), &decoded); err != nil {
		t.Fatalf("review --json: %v", err)
	}
	if len(decoded.Items) != 1 || decoded.Items[0].LinkID != undone.ID || decoded.Items[0].State != "proposed" {
		t.Fatalf("expected review --json to list the undone proposal, got %+v", decoded)
	}

	list := projects("", "list")
	if !strings.Contains(list, "- atlas-bot: Atlas Bot") || !strings.Contains(list, "3 units linked in the last 7 days") || !strings.Contains(list, "0 units linked in the last 7 days") || !strings.Contains(list, "- graphql-gateway: graphql-gateway (1 unit linked in the last 7 days)") {
		t.Fatalf("expected projects list to show applied unit counts:\n%s", list)
	}
	decidedAll := projects("a\n", "review")
	if !strings.HasSuffix(strings.TrimSpace(decidedAll), "Nothing left to decide.") || strings.Count(decidedAll, "Link ") != 2 {
		t.Fatalf("expected the review to end once the last link was decided:\n%s", decidedAll)
	}
	if nothing := projects("", "review"); !strings.Contains(nothing, "Nothing to review.") {
		t.Fatalf("expected an empty review to say so:\n%s", nothing)
	}
	if projectUnitEventsSnapshot(t, home.DatabasePath) != eventsBefore {
		t.Fatalf("expected review decisions not to edit events")
	}
}

func readCurrentProjectLinks(t *testing.T, store workgraph.ProjectLinkStoreConfig) []workgraph.ProjectLink {
	t.Helper()
	links, err := workgraph.ReadProjectLinks(store, workgraph.ProjectLinksQuery{})
	if err != nil {
		t.Fatalf("read links: %v", err)
	}
	var current []workgraph.ProjectLink
	for _, link := range links {
		if link.State == "proposed" || link.State == "applied" {
			current = append(current, link)
		}
	}
	return current
}

func TestConcurrentDeterministicPassesBothSucceed(t *testing.T) {
	now := time.Now().UTC()
	for trial := 0; trial < 10; trial++ {
		home := initProjectLinkingHome(t)
		writeProjectMemory(t, home.MemoryDir, "atlas-bot", "# Atlas Bot\n\nShips https://github.com/acme/atlas/pull/7.\n")
		for i := 0; i < 30; i++ {
			insertProjectUnitEvent(t, home.DatabasePath, slackLinkEvent("C123", fmt.Sprintf("%d.000100", 1000+i), fmt.Sprintf("%d.000100", 1000+i), "see acme/atlas#7", now.Add(-time.Hour), "harness-team"))
		}
		if _, err := workgraph.RecordProjectLinkRun(workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath}, workgraph.ProjectLinkRun{Source: "deterministic", Links: []workgraph.ProjectLinkProposal{projectLinkProposal("unit-existing", "atlas-bot", "container_mapping", true)}}); err != nil {
			t.Fatal(err)
		}
		results := make(chan error, 2)
		start := make(chan struct{})
		for i := 0; i < 2; i++ {
			go func() {
				<-start
				_, err := workgraph.LinkProjectUnits(workgraph.ProjectLinkPassConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, MemoryDir: home.MemoryDir, Now: now})
				results <- err
			}()
		}
		close(start)
		for i := 0; i < 2; i++ {
			if err := <-results; err != nil {
				t.Fatalf("trial %d: concurrent pass failed: %v", trial, err)
			}
		}
		current := readCurrentProjectLinks(t, workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath})
		if len(current) != 31 {
			t.Fatalf("trial %d: expected one link per unit, got %d", trial, len(current))
		}
	}
}

func TestPrintedFixCommandsKeepTheRequestedCatalog(t *testing.T) {
	now := time.Now().UTC()
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "harbor-harness", "# Harbor Harness\n")
	defaultMemory := filepath.Join(os.Getenv("HOME"), "workgraph-memory")
	writeProjectMemory(t, defaultMemory, "decoy", "# Decoy\n\nShips https://github.com/acme/atlas/pull/7.\n")
	insertProjectUnitEvent(t, home.DatabasePath, slackLinkEvent("C123", "1000.000100", "1000.000100", "harness flake", now.Add(-3*time.Hour), "harness-team"))
	insertProjectUnitEvent(t, home.DatabasePath, slackLinkEvent("C999", "2000.000100", "2000.000100", "see acme/atlas#7", now.Add(-3*time.Hour), "random"))
	if output, err := runworkgraph(t, repoRoot(t), "memory", "link", "--home", home.HomeDir, "--memory", home.MemoryDir, "--source", "harness-team", "harbor-harness"); err != nil {
		t.Fatalf("map container: %v %s", err, output)
	}
	runPrinted := func(command string) string {
		t.Helper()
		fields := strings.Fields(command)
		if len(fields) < 2 || fields[0] != "workgraph" {
			t.Fatalf("unexpected printed command %q", command)
		}
		output, err := runworkgraph(t, repoRoot(t), fields[1:]...)
		if err != nil {
			t.Fatalf("%s: %v %s", command, err, output)
		}
		return string(output)
	}

	runPrinted("workgraph projects review --home " + home.HomeDir + " --memory " + home.MemoryDir)
	unitID := "slack.thread:C123:1000.000100"
	explained := runPrinted("workgraph projects explain " + unitID + " --home " + home.HomeDir + " --memory " + home.MemoryDir)
	undo := ""
	for _, line := range strings.Split(explained, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "workgraph projects undo ") {
			undo = strings.TrimSpace(line)
		}
	}
	if undo == "" || !strings.Contains(undo, "--memory "+home.MemoryDir) {
		t.Fatalf("expected explain to print an undo command with the memory option:\n%s", explained)
	}
	undone := runPrinted(undo)
	_, review, found := strings.Cut(undone, "waits in ")
	if !found || !strings.Contains(review, "--memory "+home.MemoryDir) {
		t.Fatalf("expected undo to print a review command with the memory option:\n%s", undone)
	}
	runPrinted(strings.TrimSpace(strings.SplitN(review, "\n", 2)[0]))

	links, err := workgraph.ReadProjectLinks(workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath}, workgraph.ProjectLinksQuery{UnitID: "slack.thread:C999:2000.000100"})
	if err != nil || len(links) != 0 {
		t.Fatalf("expected the printed commands to keep the requested catalog, got %+v %v", links, err)
	}
}

func TestListingsSurviveAReadOnlyDatabase(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file mode 0400 does not make the database read-only here")
	}
	now := time.Now().UTC()
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "harbor-harness", "# Harbor Harness\n")
	insertProjectUnitEvent(t, home.DatabasePath, slackLinkEvent("C123", "1000.000100", "", "kickoff", now.Add(-5*time.Hour), "harness-team"))
	linkSurfaceContainer(t, home, "--source", "harness-team", "harbor-harness")
	insertProjectUnitEvent(t, home.DatabasePath, slackLinkEvent("C123", "2000.000100", "", "lunch?", now.Add(-2*time.Hour), "harness-team"))
	insertProjectUnitEvent(t, home.DatabasePath, slackLinkEvent("C456", "3000.000100", "", "standup", time.Now(), "random"))
	if err := os.Chmod(home.DatabasePath, 0o400); err != nil {
		t.Fatal(err)
	}
	makeWritable := func() {
		for _, path := range []string{home.DatabasePath, home.DatabasePath + "-wal", home.DatabasePath + "-shm"} {
			if err := os.Chmod(path, 0o600); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(makeWritable)

	run := func(args ...string) (string, string) {
		t.Helper()
		args = append(args, "--home", home.HomeDir)
		if args[0] == "projects" {
			args = append(args, "--memory", home.MemoryDir)
		}
		command := exec.Command(workgraphFactsBinary, args...)
		var stdout, stderr strings.Builder
		command.Stdout, command.Stderr = &stdout, &stderr
		if err := command.Run(); err != nil {
			t.Fatalf("%v on a read-only database: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
		}
		if strings.Contains(stdout.String()+stderr.String(), "readonly database") {
			t.Fatalf("%v leaked the SQLite error:\nstdout:\n%s\nstderr:\n%s", args, stdout.String(), stderr.String())
		}
		return stdout.String(), stderr.String()
	}
	const warning = "pending project links could not be refreshed"
	stdout, stderr := run("projects", "review", "--json")
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil {
		t.Fatalf("expected the review listing as JSON on stdout: %v\n%s", err, stdout)
	}
	if strings.Count(stderr, warning) != 1 {
		t.Fatalf("expected one warning that links could not be refreshed, got:\n%s", stderr)
	}
	stdout, stderr = run("projects", "review")
	if !strings.Contains(stdout, "Nothing to review.") {
		t.Fatalf("expected the review listing on stdout, got:\n%s", stdout)
	}
	if strings.Count(stderr, warning) != 1 {
		t.Fatalf("expected one warning that links could not be refreshed, got:\n%s", stderr)
	}
	if stdout, _ := run("projects", "list"); !strings.Contains(stdout, "harbor-harness: Harbor Harness") {
		t.Fatalf("expected the project listing, got:\n%s", stdout)
	}
	run("next")
	const suggestionsWarning = "snoozed suggestions could not be refreshed"
	if _, stderr := run("today"); strings.Count(stderr, suggestionsWarning) != 1 {
		t.Fatalf("expected today to warn once that snoozed suggestions could not be refreshed, got:\n%s", stderr)
	}

	makeWritable()
	stdout, stderr = run("projects", "review", "--auto", "--json")
	if strings.Contains(stderr, warning) || !strings.Contains(stdout, "slack.burst:C123:2000.000100") {
		t.Fatalf("expected the pending container link once the database is writable:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}
