package facts

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestNextCategorizeGroupListsLinksAwaitingADecision(t *testing.T) {
	home := initProjectLinkingHome(t)
	writeProjectMemory(t, home.MemoryDir, "mmo-gtm-segments", "---\nworkstream: mmo\n---\n# MMO GTM Segments\n")
	writeProjectMemory(t, home.MemoryDir, "ask-kai", "# Ask Kai\n")
	now := time.Now().UTC().Truncate(time.Second)
	units := insertSurfaceThreads(t, home, now.Add(-6*time.Hour), "C1", "general", 5)
	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	var proposals []workgraph.ProjectLinkProposal
	for _, unit := range units[:4] {
		proposals = append(proposals, surfaceProposal(unit, "mmo-gtm-segments", "coordination", false))
	}
	proposed, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "fake", Links: proposals})
	if err != nil {
		t.Fatal(err)
	}
	auto, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "deterministic", Links: []workgraph.ProjectLinkProposal{surfaceProposal(units[4], "ask-kai", "", true)}})
	if err != nil {
		t.Fatal(err)
	}
	slackBefore := surfaceSlackEvents(t, home.DatabasePath)
	config := workgraph.NextConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}

	result, err := workgraph.Next(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Message, "Categorize: 5 awaiting a decision (workgraph projects review), 1 auto-applied today (workgraph projects review --auto)") {
		t.Fatalf("expected the categorize line, got:\n%s", result.Message)
	}
	if shown := strings.Count(result.Message, "\n  slack.thread:C1:"); shown != 3 {
		t.Fatalf("expected at most three categorize units in text, got %d:\n%s", shown, result.Message)
	}
	if !strings.Contains(result.Message, "  "+units[0].ID+" in general: proposed project mmo-gtm-segments (confidence 0.80)") {
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
	if _, err := workgraph.AssignProjectUnit(review, workgraph.ProjectUnitAssignment{UnitID: links[1].UnitID, Project: "ask-kai"}); err != nil {
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
	writeProjectMemory(t, home.MemoryDir, "mmo-gtm-segments", "---\nworkstream: mmo\n---\n# MMO GTM Segments\n")
	writeProjectMemory(t, home.MemoryDir, "ask-kai", "# Ask Kai\n")
	local := time.Now()
	now := time.Date(local.Year(), local.Month(), local.Day(), 15, 0, 0, 0, local.Location())
	threads := insertSurfaceThreads(t, home, now.Add(-4*time.Hour), "C1", "gtm-channel", 2)
	noneThread := insertSurfaceThreads(t, home, now.Add(-3*time.Hour), "C2", "ask-kai-repo", 1)[0]
	insertSurfaceEvent(t, home.DatabasePath, surfaceFileEvent("mmo-file", "Mindbody.Modernization.Orchestration", now.Add(-2*time.Hour)))
	insertSurfaceEvent(t, home.DatabasePath, surfaceFileEvent("ask-kai-file", "ask-kai-repo", now.Add(-90*time.Minute)))
	insertSurfaceEvent(t, home.DatabasePath, surfaceFileEvent("scratch-file", "scratch", now.Add(-time.Hour)))
	linkSurfaceContainer(t, home, "--workstream", "mmo", "--source", "Mindbody.Modernization.Orchestration")
	linkSurfaceContainer(t, home, "--source", "ask-kai-repo", "ask-kai")

	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	run, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "fake", Links: []workgraph.ProjectLinkProposal{
		surfaceProposal(threads[0], "mmo-gtm-segments", "review", true),
		surfaceProposal(threads[1], "mmo-gtm-segments", "coordination", false),
		surfaceProposal(noneThread, "ask-kai", "fyi", false),
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
		"- mmo workstream: 3 events",
		"  - mmo-gtm-segments project: 2 events; coordination, review; 1 auto-applied (workgraph projects review --auto)",
		"  - Mindbody.Modernization.Orchestration: 1 event",
		"- ask-kai project: 1 event",
		"- ask-kai-repo: 1 event",
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
	writeProjectMemory(t, home.MemoryDir, "mmo-gtm-segments", "---\nworkstream: mmo\n---\n# MMO GTM Segments\n")
	writeProjectMemory(t, home.MemoryDir, "ask-kai", "# Ask Kai\n")
	now := time.Now().Truncate(time.Second)
	checkIn := now.Add(-10 * time.Hour)
	insertSurfaceEvent(t, home.DatabasePath, surfaceFileEvent("old-file", "old-work", checkIn.Add(-time.Hour)))
	earlier := insertSurfaceThreads(t, home, checkIn.Add(-3*time.Hour), "C1", "gtm-channel", 1)[0]
	overnight := insertSurfaceThreads(t, home, checkIn.Add(2*time.Hour), "C1", "gtm-channel", 1)[0]
	insertSurfaceEvent(t, home.DatabasePath, surfaceFileEvent("ask-kai-file", "ask-kai-repo", checkIn.Add(3*time.Hour)))
	linkSurfaceContainer(t, home, "--source", "ask-kai-repo", "ask-kai")
	insertSurfaceObservation(t, home.DatabasePath, "PR-agent", "acme/gtm", 7, "Draft segment tracker", "copilot-swe-agent", checkIn.Add(time.Hour))
	insertSurfaceObservation(t, home.DatabasePath, "PR-older", "acme/gtm", 6, "Older agent work", "copilot-swe-agent", checkIn.Add(-2*time.Hour))

	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}
	if _, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "llm", Profile: "fake", Links: []workgraph.ProjectLinkProposal{
		surfaceProposal(earlier, "mmo-gtm-segments", "coordination", false),
		surfaceProposal(overnight, "mmo-gtm-segments", "review", true),
	}}); err != nil {
		t.Fatal(err)
	}
	prUnit := workgraph.ProjectUnit{ID: "github.pr:acme/gtm#7", Kind: "github.pr", Container: "gtm", EventIDs: []string{"github.pull_request.observed:PR-agent"}}
	if _, err := workgraph.RecordProjectLinkRun(store, workgraph.ProjectLinkRun{Source: "deterministic", Links: []workgraph.ProjectLinkProposal{surfaceProposal(prUnit, "mmo-gtm-segments", "", true)}}); err != nil {
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
		"- mmo workstream: 1 event, 1 agent PR",
		"  - mmo-gtm-segments project: 1 event, 1 agent PR; review; 1 auto-applied (workgraph projects review --auto)",
		"    - agent PR acme/gtm#7 Draft segment tracker (copilot-swe-agent, on your behalf)",
		"- ask-kai project: 1 event",
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

func insertSurfaceObservation(t *testing.T, dbPath, nodeID, repository string, number int, title, author string, createdAt time.Time) {
	t.Helper()
	observation, err := json.Marshal(map[string]any{
		"node_id": nodeID, "repository": repository, "number": number, "role": "author", "state": "OPEN", "title": title,
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
	if _, err := db.Exec(`INSERT INTO github_pr_observations(connector_id,node_id,role,repository,number,observation_json,observed_at) VALUES ('github.pr_watch',?,'author',?,?,?,?)`,
		nodeID, repository, number, string(observation), createdAt.UTC().Format(time.RFC3339Nano)); err != nil {
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
