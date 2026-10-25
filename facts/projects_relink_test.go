package facts

import (
	"database/sql"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestMappingMoveRelinksContainerMappingLinks(t *testing.T) {
	now := time.Now().UTC()
	base := now.Add(-6 * time.Hour)
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "orbit-segments", "# Orbit Segments\n")
	writeProjectMemory(t, home.MemoryDir, "harbor-harness", "# Harbor Harness\n")
	writeProjectMemory(t, home.MemoryDir, "atlas-bot", "# Atlas Bot\n\nShips https://github.com/acme/atlas/pull/7.\n")
	plain := []string{"1000.000100", "1100.000100", "1200.000100", "1300.000100", "1400.000100"}
	for i, ts := range plain {
		insertProjectUnitEvent(t, home.DatabasePath, slackLinkEvent("C200", ts, ts, fmt.Sprintf("segment sync %d", i), base.Add(time.Duration(i)*time.Minute), "orbit-ui"))
	}
	for i, ts := range []string{"2000.000100", "2100.000100", "2200.000100", "2300.000100", "2400.000100"} {
		text := fmt.Sprintf("orbit chatter %d", i)
		if i == 0 {
			text = "see acme/atlas#7"
		}
		insertProjectUnitEvent(t, home.DatabasePath, slackLinkEvent("C200", ts, ts, text, base.Add(time.Duration(10+i)*time.Minute), "orbit-ui"))
	}
	shared, user, confirmed, undone, llmAuto := "slack.thread:C200:2000.000100", "slack.thread:C200:2100.000100", "slack.thread:C200:2200.000100", "slack.thread:C200:2300.000100", "slack.thread:C200:2400.000100"
	args := []string{"--home", home.HomeDir, "--memory", home.MemoryDir}
	run := func(command ...string) string {
		t.Helper()
		output, err := runworkgraph(t, repoRoot(t), append(command, args...)...)
		if err != nil {
			t.Fatalf("%v: %v %s", command, err, output)
		}
		return string(output)
	}
	mapping := func(action, project string) string {
		t.Helper()
		output, err := runworkgraph(t, repoRoot(t), append(append([]string{"memory", action}, args...), "--source", "orbit-ui", project)...)
		if err != nil {
			t.Fatalf("memory %s %s: %v %s", action, project, err, output)
		}
		return string(output)
	}
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath}
	if _, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "hosted-mini", Links: []workgraph.ProjectLinkProposal{
		{UnitID: llmAuto, UnitKind: "slack.thread", Container: "orbit-ui", Target: "project", Project: "atlas-bot", Kind: "fyi", Summary: "Orbit chatter", Confidence: 0.97, Evidence: []string{"slack.message:C200:2400.000100"}, Apply: true},
	}}); err != nil {
		t.Fatal(err)
	}

	linked := mapping("link", "orbit-segments")
	if !strings.Contains(linked, "Relinked 9 units from orbit-ui to orbit-segments; 0 pending proposals resolved.") {
		t.Fatalf("expected memory link to link the container's units at once:\n%s", linked)
	}
	before := projectLinksByUnit(readCurrentProjectLinks(t, store))
	for _, ts := range plain {
		if link := before["slack.thread:C200:"+ts]; link.Rule != "container_mapping" || link.Project != "orbit-segments" || link.State != "applied" || !link.Auto {
			t.Fatalf("expected a container mapping link for %s, got %+v", ts, link)
		}
	}
	if link := before[shared]; link.Rule != "shared_identifier" || link.Project != "atlas-bot" {
		t.Fatalf("expected the shared identifier to win over the mapping, got %+v", link)
	}
	if link := before[llmAuto]; link.Source != "llm" {
		t.Fatalf("expected the auto-applied LLM link to stay, got %+v", link)
	}
	if _, err := workgraph.AssignProjectLink(store, workgraph.ProjectLinkAssignment{UnitID: user, Target: "project", Project: "atlas-bot"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.ApproveProjectLink(store, before[confirmed].ID); err != nil {
		t.Fatal(err)
	}
	undoneProposal, err := workgraph.UndoProjectLink(store, before[undone].ID)
	if err != nil {
		t.Fatal(err)
	}
	protected := projectLinksByUnit(readCurrentProjectLinks(t, store))
	accuracyBefore, err := workgraph.ProjectLinkAccuracy(store)
	if err != nil {
		t.Fatal(err)
	}

	unlinked := mapping("unlink", "orbit-segments")
	if !strings.Contains(unlinked, "Unlinked 5 units from orbit-ui, which maps to no single project; 0 pending proposals resolved.") {
		t.Fatalf("expected memory unlink to release the container's mapping links:\n%s", unlinked)
	}
	released := projectLinksByUnit(readCurrentProjectLinks(t, store))
	for _, ts := range plain {
		if link, ok := released["slack.thread:C200:"+ts]; ok {
			t.Fatalf("expected %s to wait for categorization with no link, got %+v", ts, link)
		}
		assertProjectLinkRow(t, store, before["slack.thread:C200:"+ts].ID, "rejected", "")
	}

	moved := mapping("link", "harbor-harness")
	if !strings.Contains(moved, "Relinked 5 units from orbit-ui to harbor-harness; 0 pending proposals resolved.") {
		t.Fatalf("expected memory link to relink the released units:\n%s", moved)
	}
	after := projectLinksByUnit(readCurrentProjectLinks(t, store))
	runID := after["slack.thread:C200:"+plain[0]].RunID
	for _, ts := range plain {
		link := after["slack.thread:C200:"+ts]
		if link.Rule != "container_mapping" || link.Project != "harbor-harness" || link.Source != "deterministic" || !link.Auto || link.RunID == "" || link.RunID != runID {
			t.Fatalf("expected %s moved to harbor-harness in one run, got %+v", ts, link)
		}
	}
	for _, unitID := range []string{shared, user, confirmed, undone, llmAuto} {
		if after[unitID].ID != protected[unitID].ID || after[unitID].State != protected[unitID].State || after[unitID].Project != protected[unitID].Project {
			t.Fatalf("expected %s unchanged, got %+v want %+v", unitID, after[unitID], protected[unitID])
		}
	}
	if after[undone].ID != undoneProposal.ID {
		t.Fatalf("expected the undo's proposal to stay, got %+v", after[undone])
	}

	several := mapping("link", "orbit-segments")
	if !strings.Contains(several, "Unlinked 5 units from orbit-ui, which maps to no single project; 0 pending proposals resolved.") {
		t.Fatalf("expected a mapping to several projects to release the mapping links:\n%s", several)
	}
	back := mapping("unlink", "harbor-harness")
	if !strings.Contains(back, "Relinked 5 units from orbit-ui to orbit-segments; 0 pending proposals resolved.") {
		t.Fatalf("expected removing the second mapping to relink to the remaining project:\n%s", back)
	}

	explained := run("projects", "explain", "slack.thread:C200:"+plain[0])
	if strings.Count(explained, "superseded") != 2 || !strings.Contains(explained, "project harbor-harness") || !strings.Contains(explained, "run "+runID) {
		t.Fatalf("expected explain to show each superseded row and its replacement:\n%s", explained)
	}
	accuracyAfter, err := workgraph.ProjectLinkAccuracy(store)
	if err != nil {
		t.Fatal(err)
	}
	if accuracyAfter.Summary() != accuracyBefore.Summary() {
		t.Fatalf("expected superseded rows to count in no accuracy sample:\n%s\n%s", accuracyBefore.Summary(), accuracyAfter.Summary())
	}
}

func TestNewMappingResolvesPendingProposals(t *testing.T) {
	now := time.Now().UTC()
	base := now.Add(-6 * time.Hour)
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "harbor-harness", "# Harbor Harness\n")
	writeProjectMemory(t, home.MemoryDir, "atlas-bot", "# Atlas Bot\n")
	stamps := []string{"1000.000100", "1100.000100", "1200.000100", "1300.000100"}
	for i, ts := range stamps {
		insertProjectUnitEvent(t, home.DatabasePath, slackLinkEvent("C300", ts, ts, fmt.Sprintf("sync note %d", i), base.Add(time.Duration(i)*time.Minute), "harbor-sync"))
	}
	unit := func(ts string) string { return "slack.thread:C300:" + ts }
	proposal := func(ts, target, project string, confidence float64, apply bool) workgraph.ProjectLinkProposal {
		p := workgraph.ProjectLinkProposal{UnitID: unit(ts), UnitKind: "slack.thread", Container: "harbor-sync", Target: target, Project: project, Kind: "coordination", Summary: "Sync " + ts, Confidence: confidence, Evidence: []string{"slack.message:C300:" + ts}, Apply: apply}
		if target == "new_project" {
			p.Project, p.NewProject = "", &workgraph.ProposedProject{Slug: "sync-tools", Name: "Sync Tools"}
		}
		return p
	}
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	seeded, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "hosted-mini", Links: []workgraph.ProjectLinkProposal{
		proposal(stamps[0], "project", "atlas-bot", 0.7, false),
		proposal(stamps[1], "none", "", 0.6, false),
		proposal(stamps[2], "new_project", "", 0.8, false),
		proposal(stamps[3], "project", "atlas-bot", 0.97, true),
	}})
	if err != nil {
		t.Fatal(err)
	}
	undoneProposal, err := workgraph.UndoProjectLink(store, seeded.Written[3].ID)
	if err != nil {
		t.Fatal(err)
	}

	output, err := runworkgraph(t, repoRoot(t), "memory", "link", "--home", home.HomeDir, "--memory", home.MemoryDir, "--source", "harbor-sync", "harbor-harness")
	if err != nil {
		t.Fatalf("memory link: %v %s", err, output)
	}
	if !strings.Contains(string(output), "Relinked 0 units from harbor-sync to harbor-harness; 3 pending proposals resolved.") {
		t.Fatalf("expected memory link to resolve the pending proposals:\n%s", output)
	}
	current := projectLinksByUnit(readCurrentProjectLinks(t, store))
	for i, ts := range stamps[:3] {
		link := current[unit(ts)]
		if link.Source != "deterministic" || link.Rule != "container_mapping" || link.Project != "harbor-harness" || link.State != "applied" || !link.Auto {
			t.Fatalf("expected %s resolved to harbor-harness, got %+v", ts, link)
		}
		assertProjectLinkRow(t, store, seeded.Written[i].ID, "rejected", "")
	}
	if current[unit(stamps[3])].ID != undoneProposal.ID || current[unit(stamps[3])].State != "proposed" {
		t.Fatalf("expected an undo's proposal to wait for the user, got %+v", current[unit(stamps[3])])
	}
	assertReviewUnits(t, workgraph.ProjectReviewConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}, unit(stamps[3]))

	explained, err := runworkgraph(t, repoRoot(t), "projects", "explain", unit(stamps[0]), "--home", home.HomeDir, "--memory", home.MemoryDir)
	if err != nil {
		t.Fatalf("explain: %v %s", err, explained)
	}
	text := string(explained)
	supersededAt, replacedAt := strings.Index(text, fmt.Sprintf("Link %d: rejected project atlas-bot", seeded.Written[0].ID)), strings.Index(text, "rule container_mapping")
	if supersededAt < 0 || !strings.Contains(text, "superseded") || replacedAt < supersededAt {
		t.Fatalf("expected explain to show the superseded proposal before its replacement:\n%s", text)
	}
}

func TestRecheckResolvesProposalsWithDeterministicRules(t *testing.T) {
	now := time.Now().UTC()
	base := now.Add(-6 * time.Hour)
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "atlas-bot", "# Atlas Bot\n")
	writeProjectMemory(t, home.MemoryDir, "harbor-harness", "# Harbor Harness\n")
	insertProjectUnitEvent(t, home.DatabasePath, slackLinkEvent("C400", "1000.000100", "1000.000100", "can you review acme/widgets#42", base, "widgets-chat"))
	insertProjectUnitEvent(t, home.DatabasePath, slackLinkEvent("C400", "1100.000100", "1100.000100", "lunch plans", base.Add(time.Minute), "widgets-chat"))
	insertProjectUnitEvent(t, home.DatabasePath, slackLinkEvent("C400", "1200.000100", "1200.000100", "acme/widgets#42 again", base.Add(2*time.Minute), "widgets-chat"))
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	seeded, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "hosted-mini", Links: []workgraph.ProjectLinkProposal{
		{UnitID: "slack.thread:C400:1000.000100", UnitKind: "slack.thread", Container: "widgets-chat", Target: "none", Kind: "review", Summary: "Review request", Confidence: 0.6, Evidence: []string{"slack.message:C400:1000.000100"}},
		{UnitID: "slack.thread:C400:1100.000100", UnitKind: "slack.thread", Container: "widgets-chat", Target: "project", Project: "harbor-harness", Kind: "fyi", Summary: "Lunch", Confidence: 0.5, Evidence: []string{"slack.message:C400:1100.000100"}},
		{UnitID: "slack.thread:C400:1200.000100", UnitKind: "slack.thread", Container: "widgets-chat", Target: "project", Project: "harbor-harness", Kind: "review", Summary: "Review again", Confidence: 0.97, Evidence: []string{"slack.message:C400:1200.000100"}, Apply: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	undoneProposal, err := workgraph.UndoProjectLink(store, seeded.Written[2].ID)
	if err != nil {
		t.Fatal(err)
	}
	writeProjectMemory(t, home.MemoryDir, "atlas-bot", "# Atlas Bot\n\nTracks https://github.com/acme/widgets/pull/42.\n")

	output, err := runworkgraph(t, repoRoot(t), "projects", "review", "--recheck", "--home", home.HomeDir, "--memory", home.MemoryDir)
	if err != nil {
		t.Fatalf("recheck: %v %s", err, output)
	}
	current := projectLinksByUnit(readCurrentProjectLinks(t, store))
	resolved := current["slack.thread:C400:1000.000100"]
	if resolved.Source != "deterministic" || resolved.Rule != "shared_identifier" || resolved.Project != "atlas-bot" || resolved.State != "applied" || resolved.RunID == "" {
		t.Fatalf("expected recheck to resolve the proposal by the shared identifier, got %+v", resolved)
	}
	if !strings.Contains(string(output), "Rechecked 2 pending proposals; 1 resolved by deterministic rules (run "+resolved.RunID+").") {
		t.Fatalf("expected recheck to print its counts:\n%s", output)
	}
	assertProjectLinkRow(t, store, seeded.Written[0].ID, "rejected", "")
	if waiting := current["slack.thread:C400:1100.000100"]; waiting.ID != seeded.Written[1].ID || waiting.State != "proposed" {
		t.Fatalf("expected an unresolved proposal to wait, got %+v", waiting)
	}
	if waiting := current["slack.thread:C400:1200.000100"]; waiting.ID != undoneProposal.ID || waiting.State != "proposed" {
		t.Fatalf("expected an undo's proposal to wait for the user, got %+v", waiting)
	}
	again, err := runworkgraph(t, repoRoot(t), "projects", "review", "--recheck", "--home", home.HomeDir, "--memory", home.MemoryDir)
	if err != nil || !strings.Contains(string(again), "Rechecked 1 pending proposal; 0 resolved by deterministic rules.") {
		t.Fatalf("expected a second recheck to resolve nothing: %v\n%s", err, again)
	}
}

func TestBulkReviewDecidesManyLinksInOneProcess(t *testing.T) {
	now := time.Now().UTC()
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "harbor-harness", "# Harbor Harness\n")
	writeProjectMemory(t, home.MemoryDir, "atlas-bot", "# Atlas Bot\n")
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	var proposals []workgraph.ProjectLinkProposal
	for i := 0; i < 8; i++ {
		proposals = append(proposals, reviewLLMProposal(fmt.Sprintf("bulk-%d", i), 0.7, false))
	}
	seeded, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "hosted-mini", Links: proposals})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(seeded.Written))
	for i, link := range seeded.Written {
		ids[i] = fmt.Sprint(link.ID)
	}
	args := []string{"--home", home.HomeDir, "--memory", home.MemoryDir}
	review := func(input string, extra ...string) (string, error) {
		t.Helper()
		output, err := runworkgraphInput(t, repoRoot(t), input, append(append([]string{"projects", "review"}, extra...), args...)...)
		return string(output), err
	}

	approved, err := review("", "--approve", ids[0]+","+ids[1], "--approve", ids[2])
	if err != nil {
		t.Fatalf("bulk approve: %v %s", err, approved)
	}
	for _, link := range seeded.Written[:3] {
		assertProjectLinkRow(t, store, link.ID, "applied", "approved")
		if !strings.Contains(approved, fmt.Sprintf("Link %d: applied", link.ID)) {
			t.Fatalf("expected a line for link %d:\n%s", link.ID, approved)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(approved), "Approved 3 links.") {
		t.Fatalf("expected an approve summary:\n%s", approved)
	}

	rejected, err := review("", "--reject", ids[3], "--reject", "999999")
	if err == nil {
		t.Fatalf("expected a missing id to fail the command:\n%s", rejected)
	}
	assertProjectLinkRow(t, store, seeded.Written[3].ID, "rejected", "corrected")
	if !strings.Contains(rejected, "workgraph projects review: project link 999999 not found") || !strings.Contains(rejected, "Rejected 1 link; 1 failed.") {
		t.Fatalf("expected the missing id reported without stopping the rest:\n%s", rejected)
	}

	assigned, err := review("", "--assign", "bulk-4", "--assign", "bulk-5", "--project", "atlas-bot")
	if err != nil || !strings.Contains(assigned, "Assigned 2 units to atlas-bot.") {
		t.Fatalf("bulk assign: %v\n%s", err, assigned)
	}
	created, err := review("", "--assign", "bulk-6", "--assign", "bulk-7", "--new", "orbit-tools")
	if err != nil || !strings.Contains(created, "Assigned 2 units to orbit-tools.") {
		t.Fatalf("bulk assign to a new project: %v\n%s", err, created)
	}
	for unitID, project := range map[string]string{"bulk-4": "atlas-bot", "bulk-5": "atlas-bot", "bulk-6": "orbit-tools", "bulk-7": "orbit-tools"} {
		if link := currentProjectLink(t, store, unitID); link.Source != "user" || link.Project != project {
			t.Fatalf("expected %s assigned to %s, got %+v", unitID, project, link)
		}
	}
}

func TestApproveAboveConfirmsAndFilters(t *testing.T) {
	now := time.Now().UTC()
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "harbor-harness", "# Harbor Harness\n")
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	high := reviewLLMProposal("above-project", 0.95, false)
	none := reviewLLMProposal("above-none", 0.91, false)
	none.Target, none.Project = "none", ""
	elsewhere := reviewLLMProposal("above-elsewhere", 0.92, false)
	elsewhere.Container = "orbit-ui"
	low := reviewLLMProposal("below", 0.85, false)
	newProject := reviewLLMProposal("above-new", 0.99, false)
	newProject.Target, newProject.Project, newProject.NewProject = "new_project", "", &workgraph.ProposedProject{Slug: "sync-tools", Name: "Sync Tools"}
	undoSource := reviewLLMProposal("above-undone", 0.97, true)
	seeded, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "hosted-mini", Links: []workgraph.ProjectLinkProposal{high, none, elsewhere, low, newProject, undoSource}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.UndoProjectLink(store, seeded.Written[5].ID); err != nil {
		t.Fatal(err)
	}
	args := []string{"--home", home.HomeDir, "--memory", home.MemoryDir}
	review := func(input string, extra ...string) string {
		t.Helper()
		output, err := runworkgraphInput(t, repoRoot(t), input, append(append([]string{"projects", "review"}, extra...), args...)...)
		if err != nil {
			t.Fatalf("projects review %v: %v %s", extra, err, output)
		}
		return string(output)
	}
	proposedUnits := func() []string {
		t.Helper()
		var units []string
		for _, link := range readCurrentProjectLinks(t, store) {
			if link.State == "proposed" {
				units = append(units, link.UnitID)
			}
		}
		return units
	}
	all := fmt.Sprint(proposedUnits())

	for _, input := range []string{"n\n", ""} {
		declined := review(input, "--approve-above", "0.9")
		if !strings.Contains(declined, "3 proposed links at or above 0.9 match.") || !strings.Contains(declined, "Approve them? [y/N]") || !strings.Contains(declined, "No links approved.") {
			t.Fatalf("expected --approve-above to ask and decline on %q:\n%s", input, declined)
		}
		if fmt.Sprint(proposedUnits()) != all {
			t.Fatalf("expected no change without confirmation, got %v", proposedUnits())
		}
	}

	filtered := review("", "--approve-above", "0.9", "--target", "project", "--container", "harness-team", "--yes")
	if !strings.Contains(filtered, fmt.Sprintf("Link %d: applied project harbor-harness", seeded.Written[0].ID)) || !strings.HasSuffix(strings.TrimSpace(filtered), "Approved 1 link.") {
		t.Fatalf("expected only the matching project link approved:\n%s", filtered)
	}
	assertProjectLinkRow(t, store, seeded.Written[0].ID, "applied", "approved")

	confirmedNone := review("y\n", "--approve-above", "0.9", "--target", "none")
	if !strings.Contains(confirmedNone, "1 proposed link at or above 0.9 match.") || !strings.HasSuffix(strings.TrimSpace(confirmedNone), "Approved 1 link.") {
		t.Fatalf("expected the confirmed none link approved:\n%s", confirmedNone)
	}
	assertProjectLinkRow(t, store, seeded.Written[1].ID, "applied", "approved")
	if approved := currentProjectLink(t, store, "above-none"); approved.Auto || approved.Source != "llm" {
		t.Fatalf("expected an ordinary approval, got %+v", approved)
	}

	left := fmt.Sprint(proposedUnits())
	if left != "[above-elsewhere below above-new above-undone]" {
		t.Fatalf("expected the other proposals to wait, got %s", left)
	}
	if nothing := review("", "--approve-above", "0.999"); !strings.Contains(nothing, "No proposed links at or above 0.999.") {
		t.Fatalf("expected no match to say so:\n%s", nothing)
	}
	for _, invalid := range []string{"1.5", "-0.1", "NaN", "nan", "Inf", "-Inf"} {
		if output, err := runworkgraph(t, repoRoot(t), append([]string{"projects", "review", "--approve-above", invalid, "--yes"}, args...)...); err == nil || !strings.Contains(string(output), "takes a confidence from 0 to 1") {
			t.Fatalf("expected --approve-above %s to be refused: %v\n%s", invalid, err, output)
		}
	}
	reviewConfig := workgraph.ProjectReviewConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	if _, err := workgraph.ProjectLinksToApproveAbove(reviewConfig, workgraph.ProjectApproveAboveFilter{Confidence: math.NaN()}); err == nil {
		t.Fatal("expected the library to refuse a NaN confidence")
	}
	if _, err := workgraph.ProjectLinksToApproveAbove(reviewConfig, workgraph.ProjectApproveAboveFilter{Confidence: math.Inf(-1)}); err == nil {
		t.Fatal("expected the library to refuse an infinite confidence")
	}
	if fmt.Sprint(proposedUnits()) != left {
		t.Fatalf("expected refused confidences to approve nothing, got %v", proposedUnits())
	}
}

func TestRelinkAcrossBatchesUsesOneRun(t *testing.T) {
	now := time.Now().UTC()
	base := now.Add(-48 * time.Hour)
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "harbor-harness", "# Harbor Harness\n")
	const count = 620
	var events []storedEvent
	var proposals []workgraph.ProjectLinkProposal
	for i := 0; i < count; i++ {
		ts := fmt.Sprintf("%d.000100", 100000+i)
		events = append(events, slackLinkEvent("C900", ts, ts, "sync note", base.Add(time.Duration(i)*time.Second), "harbor-sync"))
		proposals = append(proposals, workgraph.ProjectLinkProposal{UnitID: "slack.thread:C900:" + ts, UnitKind: "slack.thread", Container: "harbor-sync", Target: "none", Kind: "fyi", Summary: "Sync", Confidence: 0.6, Evidence: []string{"slack.message:C900:" + ts}})
	}
	insertProjectUnitEventsAtOnce(t, home.DatabasePath, events)
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	if _, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "hosted-mini", Links: proposals}); err != nil {
		t.Fatal(err)
	}
	args := []string{"--home", home.HomeDir, "--memory", home.MemoryDir}
	run := func(command ...string) string {
		t.Helper()
		output, err := runworkgraph(t, repoRoot(t), command...)
		if err != nil {
			t.Fatalf("%v: %v %s", command, err, output)
		}
		return string(output)
	}

	linked := run(append(append([]string{"memory", "link"}, args...), "--source", "harbor-sync", "harbor-harness")...)
	if !strings.Contains(linked, fmt.Sprintf("Relinked 0 units from harbor-sync to harbor-harness; %d pending proposals resolved.", count)) {
		t.Fatalf("expected every proposal resolved across batches:\n%s", linked)
	}
	current := readCurrentProjectLinks(t, store)
	runs := map[string]int{}
	for _, link := range current {
		if link.Rule != "container_mapping" || link.State != "applied" {
			t.Fatalf("expected only container mapping links, got %+v", link)
		}
		runs[link.RunID]++
	}
	if len(current) != count || len(runs) != 1 {
		t.Fatalf("expected %d links in one run, got %d links in runs %v", count, len(current), runs)
	}
	var runID string
	for id := range runs {
		runID = id
	}

	undone := run(append([]string{"projects", "undo", "--run", runID}, args...)...)
	if !strings.Contains(undone, fmt.Sprintf("Undid %d auto-applied links", count)) {
		t.Fatalf("expected undo --run to reverse the whole relink:\n%s", undone)
	}
	for _, link := range readCurrentProjectLinks(t, store) {
		if link.State != "proposed" || link.Source != "deterministic" {
			t.Fatalf("expected each unit to wait in review after undo, got %+v", link)
		}
	}
	rechecked := run(append([]string{"projects", "review", "--recheck"}, args...)...)
	if !strings.Contains(rechecked, "Rechecked 0 pending proposals; 0 resolved by deterministic rules.") {
		t.Fatalf("expected recheck to leave an undo's proposals alone:\n%s", rechecked)
	}
	again := run(append(append([]string{"memory", "link"}, args...), "--source", "harbor-sync", "harbor-harness")...)
	if !strings.Contains(again, "Relinked 0 units from harbor-sync to harbor-harness; 0 pending proposals resolved.") {
		t.Fatalf("expected a repeated mapping not to reapply undone links:\n%s", again)
	}
}

func insertProjectUnitEventsAtOnce(t *testing.T, dbPath string, events []storedEvent) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, event := range events {
		if _, err := tx.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, summary, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			event.ID, event.Source, event.Type, event.Timestamp.UTC().Format(time.RFC3339Nano), event.Payload, event.Project, event.Summary, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("insert event %s: %v", event.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func seedRelinkBatchProposals(t *testing.T, count int) (projectLinkingHome, workgraph.ProjectLinkStoreConfig, []workgraph.ProjectLink) {
	t.Helper()
	now := time.Now().UTC()
	base := now.Add(-48 * time.Hour)
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "harbor-harness", "# Harbor Harness\n")
	var events []storedEvent
	var proposals []workgraph.ProjectLinkProposal
	for i := 0; i < count; i++ {
		ts := fmt.Sprintf("%d.000100", 100000+i)
		events = append(events, slackLinkEvent("C900", ts, ts, "sync note", base.Add(time.Duration(i)*time.Second), "harbor-sync"))
		proposals = append(proposals, workgraph.ProjectLinkProposal{UnitID: "slack.thread:C900:" + ts, UnitKind: "slack.thread", Container: "harbor-sync", Target: "none", Kind: "fyi", Summary: "Sync", Confidence: 0.6, Evidence: []string{"slack.message:C900:" + ts}})
	}
	insertProjectUnitEventsAtOnce(t, home.DatabasePath, events)
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	seeded, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "hosted-mini", Links: proposals})
	if err != nil {
		t.Fatal(err)
	}
	return home, store, seeded.Written
}

func execProjectLinksSQL(t *testing.T, dbPath, statement string) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(statement); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

func TestRelinkReportsCommittedBatchesWhenALaterBatchFails(t *testing.T) {
	home, store, _ := seedRelinkBatchProposals(t, 620)
	execProjectLinksSQL(t, home.DatabasePath, `CREATE TRIGGER forced_batch_failure BEFORE INSERT ON project_links
		WHEN NEW.source = 'deterministic' AND (SELECT count(*) FROM project_links WHERE run_id = NEW.run_id) >= 500
		BEGIN SELECT RAISE(ABORT, 'forced batch failure'); END`)
	output, err := runworkgraph(t, repoRoot(t), "memory", "link", "--home", home.HomeDir, "--memory", home.MemoryDir, "--source", "harbor-sync", "harbor-harness")
	text := string(output)
	if err == nil {
		t.Fatalf("expected a failed later batch to exit 1:\n%s", text)
	}
	runs := map[string]int{}
	waiting := 0
	for _, link := range readCurrentProjectLinks(t, store) {
		switch link.State {
		case "applied":
			runs[link.RunID]++
		case "proposed":
			waiting++
		}
	}
	if len(runs) != 1 || waiting != 120 {
		t.Fatalf("expected one committed batch of 500 and 120 waiting proposals, got runs %v and %d waiting", runs, waiting)
	}
	var runID string
	for id := range runs {
		runID = id
	}
	finish := "workgraph memory link --home " + home.HomeDir + " --memory " + home.MemoryDir + " --source harbor-sync harbor-harness"
	for _, expected := range []string{
		"Relinked 0 units from harbor-sync to harbor-harness; 500 pending proposals resolved.",
		"Relinking stopped: 120 units were not changed. Committed replacements are in run " + runID + "; workgraph projects undo --run " + runID + " reverses them.",
		"To finish, run: " + finish,
		"forced batch failure",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected the failure output to include %q:\n%s", expected, text)
		}
	}

	execProjectLinksSQL(t, home.DatabasePath, `DROP TRIGGER forced_batch_failure`)
	finished, err := runworkgraph(t, repoRoot(t), strings.Fields(finish)[1:]...)
	if err != nil || !strings.Contains(string(finished), "Relinked 0 units from harbor-sync to harbor-harness; 120 pending proposals resolved.") {
		t.Fatalf("expected the printed command to finish the relink: %v\n%s", err, finished)
	}
}

func TestBulkReviewReportsCommittedBatchesWhenALaterBatchFails(t *testing.T) {
	home, store, seeded := seedRelinkBatchProposals(t, 620)
	execProjectLinksSQL(t, home.DatabasePath, `CREATE TRIGGER forced_batch_failure BEFORE UPDATE ON project_links
		WHEN NEW.decision = 'approved' AND OLD.decision = '' AND (SELECT count(*) FROM project_links WHERE decision = 'approved') >= 500
		BEGIN SELECT RAISE(ROLLBACK, 'forced batch failure'); END`)
	ids := make([]string, len(seeded))
	for i, link := range seeded {
		ids[i] = fmt.Sprint(link.ID)
	}
	output, err := runworkgraph(t, repoRoot(t), "projects", "review", "--approve", strings.Join(ids, ","), "--home", home.HomeDir, "--memory", home.MemoryDir)
	text := string(output)
	if err == nil {
		t.Fatalf("expected a failed later batch to exit 1:\n%s", text)
	}
	approved := 0
	for _, link := range readCurrentProjectLinks(t, store) {
		if link.Decision == "approved" {
			approved++
		}
	}
	if approved != 500 || strings.Count(text, ": applied project") != 0 || strings.Count(text, ": applied no project") != 500 {
		t.Fatalf("expected the first batch approved and printed, got %d approved:\n%s", approved, text)
	}
	for _, expected := range []string{
		fmt.Sprintf("%d not decided: its batch was rolled back", seeded[500].ID),
		fmt.Sprintf("%d not decided: its batch was rolled back", seeded[619].ID),
		"Approved 500 links; 120 failed.",
		"forced batch failure",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected the failure output to include %q:\n%s", expected, text[max(0, len(text)-3000):])
		}
	}
}
