package facts

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestNextCategorizeGroupListsLinksAwaitingADecision(t *testing.T) {
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "mpo-gtm-segments", "---\nworkstream: mpo\n---\n# MPO GTM Segments\n")
	writeProjectMemory(t, home.MemoryDir, "answer-bot", "# Answer Bot\n")
	now := time.Now().UTC().Truncate(time.Second)
	units := insertSurfaceThreads(t, home, now.Add(-6*time.Hour), "C1", "general", 6)
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	var proposals []workgraph.ProjectLinkProposal
	for _, unit := range units[:4] {
		proposals = append(proposals, surfaceProposal(unit, "mpo-gtm-segments", "coordination", false))
	}
	proposed, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "fake", Links: proposals})
	if err != nil {
		t.Fatal(err)
	}
	auto, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "fake", Links: []workgraph.ProjectLinkProposal{surfaceProposal(units[4], "answer-bot", "", true)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "deterministic", Links: []workgraph.ProjectLinkProposal{surfaceProposal(units[5], "answer-bot", "", true)}}); err != nil {
		t.Fatal(err)
	}
	slackBefore := surfaceSlackEvents(t, home.DatabasePath)
	config := workgraph.NextConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}

	result, err := workgraph.Next(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Message, "Categorize: 5 awaiting a decision (workgraph projects review), 2 auto-applied today (workgraph projects review --auto)") {
		t.Fatalf("expected the categorize line, got:\n%s", result.Message)
	}
	if shown := strings.Count(result.Message, "\n  slack.thread:C1:"); shown != 3 {
		t.Fatalf("expected at most three categorize units in text, got %d:\n%s", shown, result.Message)
	}
	if !strings.Contains(result.Message, "  "+units[0].ID+" in general: proposed project mpo-gtm-segments (confidence 0.80)") {
		t.Fatalf("expected the oldest proposed unit first, got:\n%s", result.Message)
	}
	if strings.Contains(result.Message, "SECRET SUMMARY") || strings.Contains(result.Message, "projects:categorize:") {
		t.Fatalf("text output must not show summaries or item ids without --ids:\n%s", result.Message)
	}

	items := surfaceNextItems(t, home, "next", "--json")
	if len(items) != 5 {
		t.Fatalf("expected five categorize items, got %+v", items)
	}
	links := append(append([]workgraph.ProjectLink{}, proposed.Written...), auto.Written...)
	for index, item := range items {
		link := links[index]
		if item.Reason != "categorize" || item.Group != "categorize" || item.Source != "projects" || item.ID != fmt.Sprintf("projects:categorize:%d", link.ID) {
			t.Fatalf("expected categorize item for link %d, got %+v", link.ID, item)
		}
		if strings.Join(item.Evidence, ",") != strings.Join(link.Evidence, ",") || item.Subject.ID != link.UnitID || item.Project != "general" {
			t.Fatalf("expected evidence and unit from link %d, got %+v", link.ID, item)
		}
		if item.Untrusted != nil {
			t.Fatalf("summary must stay out of JSON without --include-untrusted: %+v", item)
		}
	}
	untrusted := surfaceNextItems(t, home, "next", "--json", "--include-untrusted")
	if untrusted[0].Untrusted["summary"] != "SECRET SUMMARY" {
		t.Fatalf("expected the summary in the untrusted object, got %+v", untrusted[0].Untrusted)
	}
	explained, err := workgraph.ExplainNext(config, items[0].ID)
	if err != nil || !strings.Contains(explained, "Rule: categorize") || !strings.Contains(explained, strings.Join(links[0].Evidence, ", ")) {
		t.Fatalf("explain did not cite the rule and evidence: %v\n%s", err, explained)
	}
	if err := workgraph.DoneNext(config, items[4].ID); err != nil {
		t.Fatalf("done on a categorize item: %v", err)
	}

	review := workgraph.ProjectReviewConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, MemoryDir: home.MemoryDir, Now: now}
	if _, err := workgraph.ApproveProjectReviewLink(review, links[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.AssignProjectUnit(review, workgraph.ProjectUnitAssignment{UnitID: links[1].UnitID, Project: "answer-bot"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.RejectProjectLink(store, links[2].ID); err != nil {
		t.Fatal(err)
	}
	remaining := surfaceNextItems(t, home, "next", "--json")
	if len(remaining) != 1 || remaining[0].ID != items[3].ID {
		t.Fatalf("expected only the undecided proposal to remain, got %+v", remaining)
	}
	if _, err := workgraph.ApproveProjectReviewLink(review, links[3].ID); err != nil {
		t.Fatal(err)
	}
	result, err = workgraph.Next(config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Message, "Categorize:") || len(result.Items) != 0 {
		t.Fatalf("expected the categorize line to close once every link is decided or done, got %d items:\n%s", len(result.Items), result.Message)
	}
	if surfaceSlackEvents(t, home.DatabasePath) != slackBefore {
		t.Fatal("deciding categorize items must not edit events")
	}
}

func TestTodayGroupsActivityByWorkstreamThenProject(t *testing.T) {
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "mpo-gtm-segments", "---\nworkstream: mpo\n---\n# MPO GTM Segments\n")
	writeProjectMemory(t, home.MemoryDir, "answer-bot", "# Answer Bot\n")
	local := time.Now()
	now := time.Date(local.Year(), local.Month(), local.Day(), 15, 0, 0, 0, local.Location())
	threads := insertSurfaceThreads(t, home, now.Add(-4*time.Hour), "C1", "gtm-channel", 2)
	noneThread := insertSurfaceThreads(t, home, now.Add(-3*time.Hour), "C2", "answer-bot-repo", 1)[0]
	insertSurfaceEvent(t, home.DatabasePath, surfaceFileEvent("mpo-file", "Mercury.Platform.Orchestration", now.Add(-2*time.Hour)))
	insertSurfaceEvent(t, home.DatabasePath, surfaceFileEvent("answer-bot-file", "answer-bot-repo", now.Add(-90*time.Minute)))
	insertSurfaceEvent(t, home.DatabasePath, surfaceFileEvent("scratch-file", "scratch", now.Add(-time.Hour)))
	linkSurfaceContainer(t, home, "--workstream", "mpo", "--source", "Mercury.Platform.Orchestration")
	linkSurfaceContainer(t, home, "--source", "answer-bot-repo", "answer-bot")

	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	run, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "fake", Links: []workgraph.ProjectLinkProposal{
		surfaceProposal(threads[0], "mpo-gtm-segments", "review", true),
		surfaceProposal(threads[1], "mpo-gtm-segments", "coordination", false),
		surfaceProposal(noneThread, "answer-bot", "fyi", false),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.ApproveProjectLink(store, run.Written[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.RejectProjectLink(store, run.Written[2].ID); err != nil {
		t.Fatal(err)
	}

	today, err := workgraph.Today(workgraph.TodayConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, MemoryDir: home.MemoryDir, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	section := today.Message[strings.Index(today.Message, "Projects\n"):strings.Index(today.Message, "\nSessions")]
	expected := strings.Join([]string{
		"Projects",
		"- mpo workstream: 3 events",
		"  - mpo-gtm-segments project: 2 events; coordination, review; 1 auto-applied (workgraph projects review --auto)",
		"  - Mercury.Platform.Orchestration: 1 event",
		"- answer-bot project: 1 event",
		"- answer-bot-repo: 1 event",
		"- scratch: 1 event",
		"",
	}, "\n")
	if section != expected {
		t.Fatalf("expected projects grouped by workstream, project, then container:\n%s\ngot:\n%s", expected, section)
	}
	if !strings.Contains(today.Message, "gtm-channel (2 events)") {
		t.Fatalf("expected sessions to stay grouped by container:\n%s", today.Message)
	}
}

func TestResumeGroupsOvernightActivityByProject(t *testing.T) {
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "mpo-gtm-segments", "---\nworkstream: mpo\n---\n# MPO GTM Segments\n")
	writeProjectMemory(t, home.MemoryDir, "answer-bot", "# Answer Bot\n")
	now := time.Now().Truncate(time.Second)
	checkIn := now.Add(-10 * time.Hour)
	insertSurfaceEvent(t, home.DatabasePath, surfaceFileEvent("old-file", "old-work", checkIn.Add(-time.Hour)))
	earlier := insertSurfaceThreads(t, home, checkIn.Add(-3*time.Hour), "C1", "gtm-channel", 1)[0]
	overnight := insertSurfaceThreads(t, home, checkIn.Add(2*time.Hour), "C1", "gtm-channel", 1)[0]
	insertSurfaceEvent(t, home.DatabasePath, surfaceFileEvent("answer-bot-file", "answer-bot-repo", checkIn.Add(3*time.Hour)))
	linkSurfaceContainer(t, home, "--source", "answer-bot-repo", "answer-bot")
	insertSurfaceObservation(t, home.DatabasePath, "PR-agent", "author", "acme/gtm", 7, "Draft segment tracker", "copilot-swe-agent", checkIn.Add(time.Hour))
	insertSurfaceObservation(t, home.DatabasePath, "PR-agent", "reviewer", "acme/gtm", 7, "Draft segment tracker", "copilot-swe-agent", checkIn.Add(time.Hour))
	insertSurfaceObservation(t, home.DatabasePath, "PR-older", "author", "acme/gtm", 6, "Older agent work", "copilot-swe-agent", checkIn.Add(-2*time.Hour))
	prEvent := storedEvent{ID: "github.pull_request.observed:PR-agent", Source: "github", Type: "github.pull_request.observed", Timestamp: checkIn.Add(time.Hour), Project: "gtm", Payload: `{"node_id":"PR-agent","role":"author","repository":"acme/gtm","number":7}`}
	insertSurfaceEvent(t, home.DatabasePath, prEvent)

	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	if _, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "fake", Links: []workgraph.ProjectLinkProposal{
		surfaceProposal(earlier, "mpo-gtm-segments", "coordination", false),
		surfaceProposal(overnight, "mpo-gtm-segments", "review", true),
	}}); err != nil {
		t.Fatal(err)
	}
	prUnit := workgraph.ProjectUnit{ID: "github.pr:acme/gtm#7", Kind: "github.pr", Container: "gtm", EventIDs: []string{prEvent.ID}}
	if _, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "fake", Links: []workgraph.ProjectLinkProposal{surfaceProposal(prUnit, "mpo-gtm-segments", "build", true)}}); err != nil {
		t.Fatal(err)
	}
	config := workgraph.ResumeConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, MemoryDir: home.MemoryDir, Now: now}
	if err := workgraph.RecordResumeCheckIn(config, checkIn); err != nil {
		t.Fatal(err)
	}

	resume, err := workgraph.Resume(config)
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(resume.Message, "Activity since last check-in by project")
	if start < 0 || start < strings.Index(resume.Message, "Relevant updates since last check-in") {
		t.Fatalf("expected project activity after the check-in sections:\n%s", resume.Message)
	}
	section := resume.Message[start:]
	section = section[:strings.Index(section, "\n\nResumable projects")]
	expected := strings.Join([]string{
		"Activity since last check-in by project",
		"- mpo workstream: 2 events, 1 agent PR",
		"  - mpo-gtm-segments project: 2 events, 1 agent PR; build, review; 2 auto-applied (workgraph projects review --auto)",
		"    - agent PR acme/gtm#7 Draft segment tracker (copilot-swe-agent, on your behalf)",
		"- answer-bot project: 1 event",
		"",
		"Categorize: 3 decisions remaining (workgraph projects review)",
	}, "\n")
	if section != expected {
		t.Fatalf("expected overnight activity grouped by project:\n%s\ngot:\n%s", expected, section)
	}
	if !strings.Contains(resume.Message, "Resumable projects") {
		t.Fatalf("expected the resumable list to remain:\n%s", resume.Message)
	}
}

func TestNextCategorizeItemsCarryNoLocalPaths(t *testing.T) {
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "answer-bot", "# Answer Bot\n")
	now := time.Now().UTC().Truncate(time.Second)
	repoPath := "/Users/alice/private/customer-repo"
	commit := storedEvent{ID: "git.commit:abc123", Source: "git", Type: "git.commit", Timestamp: now.Add(-3 * time.Hour), Project: "customer-repo", Payload: fmt.Sprintf(`{"repo_path":%q,"commit":"abc123","subject":"Fix import"}`, repoPath)}
	insertSurfaceEvent(t, home.DatabasePath, commit)
	units, err := workgraph.ListProjectUnits(workgraph.ProjectUnitsConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now})
	if err != nil || len(units) != 1 || units[0].ID != "git.session:"+repoPath+":abc123" {
		t.Fatalf("expected one git session unit, got %+v %v", units, err)
	}
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	proposal := surfaceProposal(units[0], "", "build", false)
	proposal.Target = "new_project"
	proposal.NewProject = &workgraph.ProposedProject{Slug: "customer-import", Name: "Customer import in " + repoPath, Description: "Fixes imports under " + repoPath, Workstream: "mpo"}
	proposal.Summary = "Edited " + repoPath + "/main.go"
	if _, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "fake", Links: []workgraph.ProjectLinkProposal{proposal}}); err != nil {
		t.Fatal(err)
	}

	output, err := runworkgraph(t, repoRoot(t), "next", "--json", "--home", home.HomeDir)
	if err != nil {
		t.Fatalf("next --json: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "/Users/") {
		t.Fatalf("categorize items must not carry local paths:\n%s", output)
	}
	var items []workgraph.NextItem
	if err := json.Unmarshal(output, &items); err != nil || len(items) != 1 {
		t.Fatalf("expected one categorize item: %v\n%s", err, output)
	}
	if proposed, _ := items[0].Detail["proposed_project"].(map[string]any); len(proposed) != 2 || proposed["slug"] != "customer-import" || proposed["workstream"] != "mpo" {
		t.Fatalf("expected the structural proposed project fields in detail, got %+v", items[0].Detail)
	}
	mcp := exec.Command(workgraphFactsBinary, "bridge", "mcp", "--home", home.HomeDir)
	mcp.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"next_items","arguments":{}}}` + "\n")
	mcpOutput, err := mcp.CombinedOutput()
	if err != nil || !strings.Contains(string(mcpOutput), items[0].ID) || strings.Contains(string(mcpOutput), "/Users/") {
		t.Fatalf("MCP items must match without local paths: %v\n%s", err, mcpOutput)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var watched bytes.Buffer
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- workgraph.WatchNext(ctx, workgraph.NextConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now, WatchInterval: time.Hour}, &watched)
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-watchDone
	if !strings.Contains(watched.String(), items[0].ID) || strings.Contains(watched.String(), "/Users/") {
		t.Fatalf("watch items must match without local paths:\n%s", watched.String())
	}
	optIn := surfaceNextItems(t, home, "next", "--json", "--include-untrusted")
	withoutUntrusted := optIn[0]
	withoutUntrusted.Untrusted = nil
	structural, _ := json.Marshal(withoutUntrusted)
	untrusted, _ := json.Marshal(optIn[0].Untrusted)
	if strings.Contains(string(structural), "/Users/") || !strings.Contains(string(untrusted), "Fixes imports under "+repoPath) || optIn[0].Untrusted["summary"] != proposal.Summary {
		t.Fatalf("model-written text must appear only under untrusted:\n%s\n%s", structural, untrusted)
	}
	text, err := workgraph.Next(workgraph.NextConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now})
	if err != nil || !strings.Contains(text.Message, ": proposed new project customer-import (confidence 0.80)") || strings.Contains(text.Message, "/Users/") {
		t.Fatalf("expected the proposed slug in text without local paths: %v\n%s", err, text.Message)
	}
	subject := items[0].Subject.ID
	if !strings.HasPrefix(subject, "git.session:") || !strings.HasSuffix(subject, ":abc123") || strings.Contains(subject, "/") {
		t.Fatalf("expected a path-free git session reference, got %q", subject)
	}
	again := surfaceNextItems(t, home, "next", "--json")
	if again[0].Subject.ID != subject {
		t.Fatalf("expected a stable unit reference, got %q then %q", subject, again[0].Subject.ID)
	}
	config := workgraph.NextConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	explained, err := workgraph.ExplainNext(config, items[0].ID)
	if err != nil || strings.Contains(explained, repoPath) || !strings.Contains(explained, "git.commit:abc123") {
		t.Fatalf("explain must resolve the item without a local path: %v\n%s", err, explained)
	}
	if err := workgraph.DoneNext(config, items[0].ID); err != nil {
		t.Fatalf("done on a git session item: %v", err)
	}
	if remaining := surfaceNextItems(t, home, "next", "--json"); len(remaining) != 0 {
		t.Fatalf("expected done to hide the item, got %+v", remaining)
	}
}

func insertSurfaceThreads(t *testing.T, home projectLinkingHome, start time.Time, channelID, container string, count int) []workgraph.ProjectUnit {
	t.Helper()
	var units []workgraph.ProjectUnit
	for index := 0; index < count; index++ {
		at := start.Add(time.Duration(index) * 10 * time.Minute)
		ts := fmt.Sprintf("%d.000100", at.Unix())
		root := slackLinkEvent(channelID, ts, ts, "thread root", at, container)
		insertSurfaceEvent(t, home.DatabasePath, root)
		units = append(units, workgraph.ProjectUnit{ID: "slack.thread:" + channelID + ":" + ts, Kind: "slack.thread", Container: container, EventIDs: []string{root.ID}})
	}
	return units
}

func surfaceProposal(unit workgraph.ProjectUnit, project, kind string, apply bool) workgraph.ProjectLinkProposal {
	return workgraph.ProjectLinkProposal{
		UnitID: unit.ID, UnitKind: unit.Kind, Container: unit.Container, Target: "project", Project: project,
		Kind: kind, Summary: "SECRET SUMMARY", Confidence: 0.8, Evidence: unit.EventIDs, Rule: "fact", Apply: apply,
	}
}

func surfaceFileEvent(id, container string, at time.Time) storedEvent {
	return storedEvent{ID: id, Source: "file", Type: "file.modified", Timestamp: at, Project: container, Payload: fmt.Sprintf(`{"path":"/work/%s/notes.md"}`, container), Summary: "notes"}
}

func insertSurfaceEvent(t *testing.T, dbPath string, event storedEvent) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	at := event.Timestamp.UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, summary, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.Source, event.Type, at, event.Payload, event.Project, event.Summary, at); err != nil {
		t.Fatalf("insert event %s: %v", event.ID, err)
	}
}

func insertSurfaceObservation(t *testing.T, dbPath, nodeID, role, repository string, number int, title, author string, createdAt time.Time) {
	t.Helper()
	observation, err := json.Marshal(map[string]any{
		"node_id": nodeID, "repository": repository, "number": number, "role": role, "state": "OPEN", "title": title,
		"url": fmt.Sprintf("https://github.com/%s/pull/%d", repository, number), "author_login": author, "author_kind": "bot",
		"created_at": createdAt.UTC().Format(time.RFC3339), "observed_at": createdAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO github_pr_observations(connector_id,node_id,role,repository,number,observation_json,observed_at) VALUES ('github.pr_watch',?,?,?,?,?,?)`,
		nodeID, role, repository, number, string(observation), createdAt.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func linkSurfaceContainer(t *testing.T, home projectLinkingHome, args ...string) {
	t.Helper()
	command := append([]string{"memory", "link", "--home", home.HomeDir, "--memory", home.MemoryDir}, args...)
	if output, err := runworkgraph(t, repoRoot(t), command...); err != nil {
		t.Fatalf("memory link %v: %v\n%s", args, err, output)
	}
}

func surfaceNextItems(t *testing.T, home projectLinkingHome, args ...string) []workgraph.NextItem {
	t.Helper()
	output, err := runworkgraph(t, repoRoot(t), append(args, "--home", home.HomeDir)...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, output)
	}
	var items []workgraph.NextItem
	if err := json.Unmarshal(output, &items); err != nil {
		t.Fatalf("parse next items: %v\n%s", err, output)
	}
	return items
}

func surfaceSlackEvents(t *testing.T, dbPath string) string {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var snapshot sql.NullString
	if err := db.QueryRow(`SELECT group_concat(id || '|' || timestamp || '|' || payload_json || '|' || COALESCE(project, ''), ';') FROM (SELECT * FROM events WHERE source = 'slack' ORDER BY id)`).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot.String
}

func TestResumeProjectDrillDownSelectsLinkedActivity(t *testing.T) {
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "demo", "---\nworkstream: ws\n---\n# Demo\n")
	writeProjectMemory(t, home.MemoryDir, "ws-two", "---\nworkstream: ws\n---\n# WS Two\n")
	writeProjectMemory(t, home.MemoryDir, "other", "# Other\n")
	now := time.Now().Truncate(time.Second)
	checkIn := now.Add(-10 * time.Hour)
	linked := insertSurfaceThreads(t, home, checkIn.Add(time.Hour), "C1", "general", 1)[0]
	mappedNone := insertSurfaceThreads(t, home, checkIn.Add(2*time.Hour), "C2", "demo-repo", 1)[0]
	mappedOther := insertSurfaceThreads(t, home, checkIn.Add(3*time.Hour), "C3", "demo-repo", 1)[0]
	sibling := insertSurfaceThreads(t, home, checkIn.Add(4*time.Hour), "C4", "sibling-channel", 1)[0]
	insertSurfaceEvent(t, home.DatabasePath, surfaceFileEvent("mapped-file", "demo-repo", checkIn.Add(5*time.Hour)))
	insertSurfaceEvent(t, home.DatabasePath, surfaceFileEvent("named-file", "demo", checkIn.Add(6*time.Hour)))
	insertSurfaceEvent(t, home.DatabasePath, surfaceFileEvent("shared-file", "shared-repo", checkIn.Add(7*time.Hour)))
	insertSurfaceEvent(t, home.DatabasePath, surfaceFileEvent("workstream-named-file", "ws", checkIn.Add(8*time.Hour)))
	linkSurfaceContainer(t, home, "--source", "demo-repo", "demo")
	linkSurfaceContainer(t, home, "--source", "shared-repo", "demo")
	linkSurfaceContainer(t, home, "--source", "shared-repo", "other")

	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	run, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "fake", Links: []workgraph.ProjectLinkProposal{
		surfaceProposal(linked, "demo", "review", true),
		surfaceProposal(mappedNone, "demo", "fyi", true),
		surfaceProposal(mappedOther, "other", "build", true),
		surfaceProposal(sibling, "ws-two", "coordination", true),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.RejectProjectLink(store, run.Written[1].ID); err != nil {
		t.Fatal(err)
	}
	config := workgraph.ResumeConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, MemoryDir: home.MemoryDir, Now: now}
	if err := workgraph.RecordResumeCheckIn(config, checkIn); err != nil {
		t.Fatal(err)
	}
	overview, err := workgraph.Resume(config)
	if err != nil || !strings.Contains(overview.Message, "  - demo project: 2 events; review; 1 auto-applied") {
		t.Fatalf("expected the overview to show linked work under demo: %v\n%s", err, overview.Message)
	}

	config.Project = "demo"
	drill, err := workgraph.Resume(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := surfaceEventIDs(drill.Events); got != strings.Join([]string{"shared-file", "named-file", "mapped-file", linked.EventIDs[0]}, ",") {
		t.Fatalf("expected the linked thread, the mapped containers, and the named container, got %s:\n%s", got, drill.Message)
	}
	if !strings.Contains(drill.Message, "Recent activity") || strings.Contains(drill.Message, "No recent activity found") {
		t.Fatalf("expected recent activity for demo:\n%s", drill.Message)
	}

	config.Project = "other"
	other, err := workgraph.Resume(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := surfaceEventIDs(other.Events); got != strings.Join([]string{"shared-file", mappedOther.EventIDs[0]}, ",") {
		t.Fatalf("expected a container mapped to two projects in either drill-down, got %s:\n%s", got, other.Message)
	}

	config.Project = "ws"
	workstream, err := workgraph.Resume(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := surfaceEventIDs(workstream.Events); got != strings.Join([]string{"shared-file", "named-file", "mapped-file", sibling.EventIDs[0], linked.EventIDs[0]}, ",") {
		t.Fatalf("expected the workstream's projects' activity, got %s:\n%s", got, workstream.Message)
	}
	for _, expected := range []string{"Resume ws workstream", "Projects: demo, ws-two", "- demo project: 4 events", "- ws-two project: 1 event"} {
		if !strings.Contains(workstream.Message, expected) {
			t.Fatalf("expected %q in the workstream drill-down:\n%s", expected, workstream.Message)
		}
	}
	if strings.Index(workstream.Message, "- demo project") > strings.Index(workstream.Message, "- ws-two project") {
		t.Fatalf("expected the most recently active project first:\n%s", workstream.Message)
	}
	if strings.Contains(workstream.Message, "Add project memory") {
		t.Fatalf("a workstream has no memory file to suggest:\n%s", workstream.Message)
	}
}

func surfaceEventIDs(events []workgraph.ResumeEvent) string {
	var ids []string
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	return strings.Join(ids, ",")
}
