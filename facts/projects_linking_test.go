package facts

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestDeterministicProjectLinksResolveInOrder(t *testing.T) {
	now := time.Now().UTC()
	base := now.Add(-6 * time.Hour)
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "ask-kai", "# Ask Kai\n\nShipping https://github.com/Acme/Kai/pull/7 under AB#1563394.\nFix landed in https://github.com/acme/kai/commit/abcdef1234567890.\n")
	writeProjectMemory(t, home.MemoryDir, "mmo-gtm-segments", "---\nworkstream: mmo\n---\n# MMO GTM Segments\n\nTracked as AB#1563394.\n")
	writeProjectMemory(t, home.MemoryDir, "mmo-harness", "# MMO Harness\n")

	events := []storedEvent{
		slackLinkEvent("C123", "1000.000100", "1000.000100", "can you look at https://github.com/acme/kai/pull/7/files", base, "harness-team"),
		slackLinkEvent("C123", "1000.000200", "1000.000100", "sure", base.Add(5*time.Minute), "harness-team"),
		slackLinkEvent("C123", "2000.000100", "", "lunch?", base.Add(time.Hour), "harness-team"),
		slackLinkEvent("C123", "3000.000100", "3000.000100", "status of AB#1563394?", base.Add(90*time.Minute), "harness-team"),
		slackLinkEvent("C999", "4000.000100", "4000.000100", "acme/other#5 is flaky", base.Add(2*time.Hour), "random"),
		slackLinkEvent("C999", "5000.000100", "5000.000100", "still acme/other#5", base.Add(2*time.Hour+time.Minute), "random"),
		slackLinkEvent("C999", "6000.000100", "6000.000100", "acme/other#5 again", base.Add(2*time.Hour+2*time.Minute), "random"),
		slackLinkEvent("C999", "7000.000100", "7000.000100", "reverted https://github.com/acme/kai/commit/abcdef1", base.Add(2*time.Hour+3*time.Minute), "random"),
		{ID: "slack.message:C999:8000.000100", Source: "slack", Type: "slack.message", Timestamp: base.Add(2*time.Hour + 4*time.Minute), Project: "random", Payload: `{"channel_id":"C999","ts":"8000.000100","thread_ts":"8000.000100","text":"see attachment","untrusted":{"text":"https://github.com/acme/kai/pull/7"}}`},
	}
	for _, event := range events {
		insertProjectUnitEvent(t, home.DatabasePath, event)
	}
	if output, err := runworkgraph(t, repoRoot(t), "memory", "link", "--home", home.HomeDir, "--memory", home.MemoryDir, "--source", "harness-team", "mmo-harness"); err != nil {
		t.Fatalf("map container: %v %s", err, output)
	}

	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	seeded, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{
		Source:  "llm",
		Profile: "hosted-mini",
		Links: []workgraph.ProjectLinkProposal{
			{UnitID: "slack.thread:C999:4000.000100", UnitKind: "slack.thread", Container: "random", Target: "project", Project: "ask-kai", Kind: "fyi", Summary: "Flaky test", Confidence: 0.97, Evidence: []string{"slack.message:C999:4000.000100"}, Apply: true},
			{UnitID: "slack.thread:C999:5000.000100", UnitKind: "slack.thread", Container: "random", Target: "project", Project: "ask-kai", Kind: "fyi", Summary: "Flaky test", Confidence: 0.80, Evidence: []string{"slack.message:C999:5000.000100"}},
		},
	})
	if err != nil {
		t.Fatalf("seed llm links: %v", err)
	}

	pass := workgraph.ProjectLinkPassConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, MemoryDir: home.MemoryDir, Now: now}
	first, err := workgraph.LinkProjectUnits(pass)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	got := projectLinksByUnit(first.Written)
	expected := map[string][2]string{
		"slack.thread:C123:1000.000100": {"ask-kai", "shared_identifier"},
		"slack.burst:C123:2000.000100":  {"mmo-harness", "container_mapping"},
		"slack.thread:C999:7000.000100": {"ask-kai", "shared_identifier"},
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
	for _, unitID := range []string{"slack.thread:C123:3000.000100", "slack.thread:C999:6000.000100", "slack.thread:C999:8000.000100"} {
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
	if len(second.Written) != 1 || second.Written[0].UnitID != "slack.thread:C999:6000.000100" || second.Written[0].Project != "ask-kai" || second.Written[0].Rule != "shared_identifier" || second.RunID == first.RunID {
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

type projectLinkingHome struct {
	HomeDir      string
	MemoryDir    string
	DatabasePath string
}

func initProjectLinkingHome(t *testing.T) projectLinkingHome {
	t.Helper()
	root := t.TempDir()
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
	if _, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "deterministic", Links: []workgraph.ProjectLinkProposal{projectLinkProposal("unit-det", "mmo-harness", "container_mapping", true)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "hosted-mini", Links: []workgraph.ProjectLinkProposal{
		reviewLLMProposal("unit-llm", 0.97, true),
		reviewLLMProposal("unit-prop", 0.80, false),
	}}); err != nil {
		t.Fatal(err)
	}
	assertReviewUnits(t, workgraph.ProjectReviewConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}, "unit-prop", "unit-det", "unit-llm")

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
		if _, err := workgraph.AssignProjectLink(store, workgraph.ProjectLinkAssignment{UnitID: link.UnitID, Target: "project", Project: "ask-kai"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := workgraph.UndoProjectLink(store, run.Written[18].ID); err != nil {
		t.Fatal(err)
	}
	review := workgraph.ProjectReviewConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	assertReviewUnits(t, review, "unit-prop", "sample-18", "unit-det", "unit-llm", "sample-19")

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
	return workgraph.ProjectLinkProposal{UnitID: unitID, UnitKind: "slack.thread", Container: "harness-team", Target: "project", Project: "mmo-harness", Kind: "build", Summary: "Built " + unitID, Confidence: confidence, Evidence: []string{"slack.message:C123:" + unitID}, Apply: apply}
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
	writeProjectMemory(t, home.MemoryDir, "ask-kai", "# Ask Kai\n\nShips https://github.com/acme/kai/pull/7.\n")
	writeProjectMemory(t, home.MemoryDir, "mmo-harness", "# MMO Harness\n")
	for _, event := range []storedEvent{
		slackLinkEvent("C123", "1000.000100", "1000.000100", "review https://github.com/acme/kai/pull/7", base, "harness-team"),
		slackLinkEvent("C123", "2000.000100", "", "lunch?", base.Add(time.Hour), "harness-team"),
		slackLinkEvent("C123", "3000.000100", "3000.000100", "harness flake", base.Add(90*time.Minute), "harness-team"),
		slackLinkEvent("C999", "4000.000100", "4000.000100", "agent tooling over GraphQL", base.Add(2*time.Hour), "random"),
		slackLinkEvent("C999", "5000.000100", "5000.000100", "new idea", base.Add(2*time.Hour+time.Minute), "random"),
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
	if output, err := runworkgraph(t, repoRoot(t), append([]string{"memory", "link"}, append(args, "--source", "harness-team", "mmo-harness")...)...); err != nil {
		t.Fatalf("map container: %v %s", err, output)
	}
	eventsBefore := projectUnitEventsSnapshot(t, home.DatabasePath)

	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath}
	llm, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "hosted-mini", Links: []workgraph.ProjectLinkProposal{
		{UnitID: "slack.thread:C999:4000.000100", UnitKind: "slack.thread", Container: "random", Target: "project", Project: "mmo-harness", Kind: "advice", Summary: "Advised on agent tooling", Confidence: 0.97, Evidence: []string{"slack.message:C999:4000.000100"}, Apply: true},
		{UnitID: "slack.thread:C999:5000.000100", UnitKind: "slack.thread", Container: "random", Target: "project", Project: "ask-kai", Kind: "build", Summary: "Sketched an idea", Confidence: 0.80, Evidence: []string{"slack.message:C999:5000.000100"}},
	}})
	if err != nil {
		t.Fatalf("record llm run: %v", err)
	}
	llmAuto, llmProposed := llm.Written[0], llm.Written[1]

	listed := projects("", "review")
	links := projectLinksByUnit(readCurrentProjectLinks(t, store))
	shared, burst, mapped := links["slack.thread:C123:1000.000100"], links["slack.burst:C123:2000.000100"], links["slack.thread:C123:3000.000100"]
	if shared.Rule != "shared_identifier" || burst.Rule != "container_mapping" || mapped.Rule != "container_mapping" || shared.RunID == "" || shared.RunID != burst.RunID {
		t.Fatalf("expected review to run one deterministic pass, got %+v", links)
	}
	for _, expected := range []string{fmt.Sprintf("Link %d", llmProposed.ID), fmt.Sprintf("Link %d", shared.ID), fmt.Sprintf("Link %d", llmAuto.ID), "Sketched an idea", "evidence: slack.message:C999:5000.000100"} {
		if !strings.Contains(listed, expected) {
			t.Fatalf("default review omitted %q:\n%s", expected, listed)
		}
	}
	if strings.Index(listed, fmt.Sprintf("Link %d", llmProposed.ID)) > strings.Index(listed, fmt.Sprintf("Link %d", shared.ID)) {
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
	projects("s\ns\ns\nr\nask-kai\n", "review", "--auto")
	assertProjectLinkRow(t, store, llmAuto.ID, "rejected", "corrected")
	if current := currentProjectLink(t, store, llmAuto.UnitID); current.Source != "user" || current.Project != "ask-kai" || current.State != "applied" {
		t.Fatalf("expected r to reassign the LLM link to ask-kai, got %+v", current)
	}

	projects("x\ns\na\nq\n", "review")
	assertProjectLinkRow(t, store, llmProposed.ID, "rejected", "corrected")
	if current := currentProjectLink(t, store, llmProposed.UnitID); current.Source != "user" || current.Target != "none" {
		t.Fatalf("expected x to assign no project, got %+v", current)
	}
	if current := currentProjectLink(t, store, burst.UnitID); current.ID != burst.ID || current.Decision != "confirmed" || current.State != "applied" {
		t.Fatalf("expected a to confirm the auto link, got %+v", current)
	}

	explained := projects("", "explain", mapped.UnitID)
	for _, expected := range []string{"slack.thread:C123:3000.000100", "slack.message:C123:3000.000100", fmt.Sprintf("Link %d", mapped.ID), "source deterministic", "rule container_mapping", "run " + mapped.RunID,
		"workgraph projects review --assign slack.thread:C123:3000.000100 --project <slug>", fmt.Sprintf("workgraph projects review --reject %d", mapped.ID), fmt.Sprintf("workgraph projects undo %d", mapped.ID)} {
		if !strings.Contains(explained, expected) {
			t.Fatalf("explain omitted %q:\n%s", expected, explained)
		}
	}

	if out := projects("", "undo", fmt.Sprint(shared.ID)); !strings.Contains(out, "slack.thread:C123:1000.000100") {
		t.Fatalf("undo output: %s", out)
	}
	replacement := currentProjectLink(t, store, shared.UnitID)
	if replacement.State != "proposed" || replacement.Project != "ask-kai" || replacement.Source != "deterministic" {
		t.Fatalf("expected a proposed replacement after undo, got %+v", replacement)
	}
	projects("", "review", "--approve", fmt.Sprint(replacement.ID))
	assertProjectLinkRow(t, store, replacement.ID, "applied", "approved")
	if approved := currentProjectLink(t, store, shared.UnitID); approved.Auto || approved.Source != "deterministic" {
		t.Fatalf("expected an approved link to keep its source and clear auto, got %+v", approved)
	}

	projects("", "review", "--reject", fmt.Sprint(burst.ID))
	assertProjectLinkRow(t, store, burst.ID, "rejected", "corrected")
	projects("", "review", "--assign", burst.UnitID, "--project", "ask-kai")
	if current := currentProjectLink(t, store, burst.UnitID); current.Source != "user" || current.Project != "ask-kai" {
		t.Fatalf("expected --assign to apply a user link, got %+v", current)
	}
	if output, err := runworkgraph(t, repoRoot(t), append([]string{"projects", "review", "--assign", burst.UnitID, "--project", "missing"}, args...)...); err == nil {
		t.Fatalf("expected an unknown project to be refused: %s", output)
	}

	projects("", "review", "--assign", llmProposed.UnitID, "--new", "agent-tooling", "--workstream", "mmo")
	catalog, err := workgraph.LoadProjectCatalog(home.MemoryDir)
	if err != nil {
		t.Fatal(err)
	}
	created := false
	for _, entry := range catalog.Projects {
		created = created || (entry.Slug == "agent-tooling" && entry.Workstream == "mmo")
	}
	if !created || len(catalog.Problems) != 0 {
		t.Fatalf("expected --new to create project memory in workstream mmo, got %+v", catalog)
	}
	if current := currentProjectLink(t, store, llmProposed.UnitID); current.Source != "user" || current.Project != "agent-tooling" {
		t.Fatalf("expected --new to apply a user link, got %+v", current)
	}
	if output, err := runworkgraph(t, repoRoot(t), append([]string{"projects", "review", "--assign", llmProposed.UnitID, "--new", "ask-kai"}, args...)...); err == nil {
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
	if !strings.Contains(list, "- ask-kai: Ask Kai") || !strings.Contains(list, "3 units linked in the last 7 days") || !strings.Contains(list, "0 units linked in the last 7 days") {
		t.Fatalf("expected projects list to show applied unit counts:\n%s", list)
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
