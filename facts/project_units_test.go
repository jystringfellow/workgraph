package facts

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestProjectUnitsAreDeterministic(t *testing.T) {
	base := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	now := base.Add(10 * time.Hour)
	events := projectUnitFixtureEvents(base)

	forward := initProjectUnitsHome(t)
	for _, event := range events {
		insertProjectUnitEvent(t, forward.DatabasePath, event)
	}
	reversed := initProjectUnitsHome(t)
	for index := len(events) - 1; index >= 0; index-- {
		insertProjectUnitEvent(t, reversed.DatabasePath, events[index])
	}

	config := workgraph.ProjectUnitsConfig{Now: now, Location: time.UTC}
	config.HomeDir, config.DatabasePath = forward.HomeDir, forward.DatabasePath
	first, err := workgraph.ListProjectUnits(config)
	if err != nil {
		t.Fatalf("list units: %v", err)
	}
	config.HomeDir, config.DatabasePath = reversed.HomeDir, reversed.DatabasePath
	second, err := workgraph.ListProjectUnits(config)
	if err != nil {
		t.Fatalf("list units in reverse capture order: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("expected the same units in any capture order\nforward: %+v\nreversed: %+v", first, second)
	}

	units := map[string]workgraph.ProjectUnit{}
	for _, unit := range first {
		units[unit.ID] = unit
	}
	expected := map[string][]string{
		"slack.thread:C123:1760950800.000100": {"slack.message:C123:1760950800.000100", "slack.thread_reply:C123:1760951400.000200", "slack.thread_reply:C123:1760952000.000300"},
		"slack.burst:C123:1760954400.000100":  {"slack.message:C123:1760954400.000100", "slack.message:C123:1760958000.000100"},
		"slack.burst:C123:1760968800.000100":  {"slack.message:C123:1760968800.000100"},
		"slack.burst:D456:1760983200.000100":  {"slack.message:D456:1760983200.000100"},
		"github.pr:acme/harness#42":           {"github.pull_request.review_requested:acme/harness:42:1", "github.pull_request:acme/harness:42"},
		"git.session:/work/harness:aaa111":    {"git.commit:aaa111", "git.commit:bbb222"},
		"git.session:/work/harness:ccc333":    {"git.commit:ccc333"},
		"notion.page:page-1:2026-10-20":       {"notion.page_updated:page-1:2026-10-20T09:00:00.000Z", "notion.page_updated:page-1:2026-10-20T11:00:00.000Z"},
		"calendar.event:google:primary:evt-1": {"calendar.event:google:primary:evt-1"},
	}
	if len(units) != len(expected) {
		t.Fatalf("expected %d units, got %d: %+v", len(expected), len(units), first)
	}
	for id, members := range expected {
		unit, ok := units[id]
		if !ok {
			t.Fatalf("expected unit %q, got %+v", id, first)
		}
		if !reflect.DeepEqual(unit.EventIDs, members) {
			t.Fatalf("expected unit %q members %v, got %v", id, members, unit.EventIDs)
		}
	}

	if units["slack.thread:C123:1760950800.000100"].Kind != "slack.thread" || units["slack.thread:C123:1760950800.000100"].Container != "harness-team" {
		t.Fatalf("expected thread kind and channel container, got %+v", units["slack.thread:C123:1760950800.000100"])
	}
	if units["github.pr:acme/harness#42"].Container != "Harness" {
		t.Fatalf("expected PR container from its earliest member, got %+v", units["github.pr:acme/harness#42"])
	}
	if units["calendar.event:google:primary:evt-1"].Kind != "calendar.event" {
		t.Fatalf("expected calendar unit id to match its stored event id, got %+v", units["calendar.event:google:primary:evt-1"])
	}
	for id, unit := range units {
		wantEligible := id != "slack.burst:D456:1760983200.000100"
		if unit.Eligible != wantEligible {
			t.Fatalf("expected unit %q eligible=%v, got %+v", id, wantEligible, unit)
		}
	}
	burst := units["slack.burst:C123:1760954400.000100"]
	if !burst.Start.Equal(base.Add(time.Hour)) || !burst.End.Equal(base.Add(2*time.Hour)) {
		t.Fatalf("expected burst time range from its members, got %s to %s", burst.Start, burst.End)
	}

	insertProjectUnitEvent(t, forward.DatabasePath, slackUnitEvent("C123", "1760963400.000100", "", base.Add(3*time.Hour+30*time.Minute), "harness-team"))
	config.HomeDir, config.DatabasePath = forward.HomeDir, forward.DatabasePath
	merged, err := workgraph.ListProjectUnits(config)
	if err != nil {
		t.Fatalf("list units after late message: %v", err)
	}
	for _, unit := range merged {
		if unit.ID == "slack.burst:C123:1760968800.000100" {
			t.Fatalf("expected the late message to merge the later burst into the earlier one, got %+v", merged)
		}
		if unit.ID == "slack.burst:C123:1760954400.000100" && len(unit.EventIDs) != 4 {
			t.Fatalf("expected the merged burst to keep the earlier id with four members, got %+v", unit)
		}
	}
}

func TestProjectLinkDecisionsRecordStateWithoutEditingEvents(t *testing.T) {
	home := initProjectUnitsHome(t)
	insertProjectUnitEvent(t, home.DatabasePath, slackUnitEvent("C123", "1760950800.000100", "", time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC), "harness-team"))
	eventsBefore := projectUnitEventsSnapshot(t, home.DatabasePath)
	now := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	config := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: now}

	deterministic, err := workgraph.RecordProjectLinkRun(config, workgraph.ProjectLinkRun{
		Source: "deterministic",
		Links: []workgraph.ProjectLinkProposal{
			projectLinkProposal("unit-confirm", "mmo-harness", "container_mapping", true),
			projectLinkProposal("unit-reassign", "mmo-harness", "container_mapping", true),
		},
	})
	if err != nil {
		t.Fatalf("record deterministic run: %v", err)
	}
	if deterministic.RunID == "" || len(deterministic.Written) != 2 {
		t.Fatalf("expected a run id and two written links, got %+v", deterministic)
	}
	for _, link := range deterministic.Written {
		if link.State != "applied" || !link.Auto || link.RunID != deterministic.RunID || link.Confidence != 1 || link.Rule != "container_mapping" {
			t.Fatalf("expected an applied auto deterministic link with the run id, got %+v", link)
		}
	}

	llm, err := workgraph.RecordProjectLinkRun(config, workgraph.ProjectLinkRun{
		Source:  "llm",
		Profile: "local-gemma",
		Links: []workgraph.ProjectLinkProposal{
			{UnitID: "unit-approve", UnitKind: "slack.thread", Container: "harness-team", Target: "project", Project: "ask-kai", Kind: "advice", Summary: "Advised​ Sam on\x07 GraphQL", Confidence: 0.82, Evidence: []string{"slack.message:C123:1760950800.000100"}},
			{UnitID: "unit-reject", UnitKind: "slack.thread", Container: "harness-team", Target: "project", Project: "ask-kai", Kind: "fyi", Summary: "Shared a link", Confidence: 0.7, Evidence: []string{"slack.message:C123:1760950800.000100"}},
		},
	})
	if err != nil {
		t.Fatalf("record llm run: %v", err)
	}
	proposed := llm.Written[0]
	if proposed.State != "proposed" || proposed.Auto || proposed.Profile != "local-gemma" || proposed.RunID != llm.RunID || proposed.Summary != "Advised Sam on GraphQL" {
		t.Fatalf("expected a proposed llm link with profile, run id, and a stripped summary, got %+v", proposed)
	}

	approved, err := workgraph.ApproveProjectLink(config, proposed.ID)
	if err != nil {
		t.Fatalf("approve proposed link: %v", err)
	}
	if approved.ID != proposed.ID || approved.State != "applied" || approved.Auto || approved.Source != "llm" || approved.Decision != "approved" || !approved.DecidedAt.Equal(now) {
		t.Fatalf("expected approving a proposed link to apply it with decision approved, got %+v", approved)
	}

	confirmed, err := workgraph.ApproveProjectLink(config, deterministic.Written[0].ID)
	if err != nil {
		t.Fatalf("approve auto link: %v", err)
	}
	if confirmed.State != "applied" || !confirmed.Auto || confirmed.Decision != "confirmed" {
		t.Fatalf("expected approving an auto link to confirm it, got %+v", confirmed)
	}
	if _, err := workgraph.ApproveProjectLink(config, confirmed.ID); err == nil {
		t.Fatalf("expected approving a decided link to fail")
	}

	reassigned, err := workgraph.AssignProjectLink(config, workgraph.ProjectLinkAssignment{UnitID: "unit-reassign", Target: "project", Project: "ask-kai"})
	if err != nil {
		t.Fatalf("reassign link: %v", err)
	}
	if reassigned.Source != "user" || reassigned.State != "applied" || reassigned.Auto || reassigned.Project != "ask-kai" || reassigned.RunID != "" || reassigned.Confidence != 1 || !reflect.DeepEqual(reassigned.Evidence, deterministic.Written[1].Evidence) || reassigned.Container != "harness-team" {
		t.Fatalf("expected an applied user link carrying the unit's evidence, got %+v", reassigned)
	}
	assertProjectLinkRow(t, config, deterministic.Written[1].ID, "rejected", "corrected")

	rejected, err := workgraph.RejectProjectLink(config, llm.Written[1].ID)
	if err != nil {
		t.Fatalf("reject link: %v", err)
	}
	if rejected.Source != "user" || rejected.State != "applied" || rejected.Target != "none" || rejected.Project != "" {
		t.Fatalf("expected rejecting to apply a user link with no project, got %+v", rejected)
	}
	assertProjectLinkRow(t, config, llm.Written[1].ID, "rejected", "corrected")

	created, err := workgraph.AssignProjectLink(config, workgraph.ProjectLinkAssignment{
		UnitID: "unit-new", UnitKind: "git.session", Container: "Harness", Evidence: []string{"git.commit:aaa111"},
		Target: "new_project", NewProject: &workgraph.ProposedProject{Slug: "mmo-words", Name: "MMO Words", Workstream: "mmo"},
	})
	if err != nil {
		t.Fatalf("assign new project: %v", err)
	}
	if created.Target != "new_project" || created.NewProject == nil || created.NewProject.Slug != "mmo-words" || created.Source != "user" || created.State != "applied" {
		t.Fatalf("expected an applied user link to a new project, got %+v", created)
	}

	links, err := workgraph.ReadProjectLinks(config, workgraph.ProjectLinksQuery{})
	if err != nil {
		t.Fatalf("read links: %v", err)
	}
	if len(links) != 7 {
		t.Fatalf("expected rows to be kept, never deleted, got %d: %+v", len(links), links)
	}
	if after := projectUnitEventsSnapshot(t, home.DatabasePath); after != eventsBefore {
		t.Fatalf("expected link decisions to leave events unchanged\nbefore: %s\nafter: %s", eventsBefore, after)
	}
}

func TestProjectLinkUndoLeavesAProposalNoLaterRunReapplies(t *testing.T) {
	home := initProjectUnitsHome(t)
	config := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath, Now: time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)}

	firstRun, err := workgraph.RecordProjectLinkRun(config, workgraph.ProjectLinkRun{
		Source: "deterministic",
		Links: []workgraph.ProjectLinkProposal{
			projectLinkProposal("unit-undo", "mmo-harness", "shared_identifier", true),
			projectLinkProposal("unit-user", "mmo-harness", "container_mapping", true),
		},
	})
	if err != nil {
		t.Fatalf("record deterministic run: %v", err)
	}
	llmRun, err := workgraph.RecordProjectLinkRun(config, workgraph.ProjectLinkRun{
		Source:  "llm",
		Profile: "hosted-mini",
		Links: []workgraph.ProjectLinkProposal{
			{UnitID: "unit-run-a", UnitKind: "slack.burst", Container: "harness-team", Target: "project", Project: "ask-kai", Kind: "build", Summary: "Built the thing", Confidence: 0.97, Evidence: []string{"slack.message:C123:1"}, Apply: true},
			{UnitID: "unit-run-b", UnitKind: "slack.burst", Container: "harness-team", Target: "none", Kind: "fyi", Summary: "Lunch", Confidence: 0.99, Evidence: []string{"slack.message:C123:2"}, Apply: true},
		},
	})
	if err != nil {
		t.Fatalf("record llm run: %v", err)
	}

	replacement, err := workgraph.UndoProjectLink(config, firstRun.Written[0].ID)
	if err != nil {
		t.Fatalf("undo link: %v", err)
	}
	original := firstRun.Written[0]
	if replacement.ID == original.ID || replacement.State != "proposed" || replacement.Auto || replacement.Target != original.Target || replacement.Project != original.Project || replacement.Source != original.Source || !reflect.DeepEqual(replacement.Evidence, original.Evidence) {
		t.Fatalf("expected a proposed replacement with the same target, source, and evidence, got %+v from %+v", replacement, original)
	}
	assertProjectLinkRow(t, config, original.ID, "rejected", "undone")
	if _, err := workgraph.UndoProjectLink(config, original.ID); err == nil {
		t.Fatalf("expected undoing an undone link to fail")
	}

	undone, err := workgraph.UndoProjectLinkRun(config, llmRun.RunID)
	if err != nil {
		t.Fatalf("undo run: %v", err)
	}
	if len(undone) != 2 {
		t.Fatalf("expected the whole run to be undone, got %+v", undone)
	}
	for _, link := range llmRun.Written {
		assertProjectLinkRow(t, config, link.ID, "rejected", "undone")
	}

	if _, err := workgraph.AssignProjectLink(config, workgraph.ProjectLinkAssignment{UnitID: "unit-user", Target: "project", Project: "ask-kai"}); err != nil {
		t.Fatalf("reassign link: %v", err)
	}

	later, err := workgraph.RecordProjectLinkRun(config, workgraph.ProjectLinkRun{
		Source:  "llm",
		Profile: "hosted-mini",
		Links: []workgraph.ProjectLinkProposal{
			{UnitID: "unit-undo", UnitKind: "slack.thread", Container: "harness-team", Target: "project", Project: "mmo-harness", Kind: "build", Summary: "Again", Confidence: 0.99, Evidence: []string{"slack.message:C123:1760950800.000100"}, Apply: true},
			{UnitID: "unit-run-a", UnitKind: "slack.burst", Container: "harness-team", Target: "project", Project: "ask-kai", Kind: "build", Summary: "Again", Confidence: 0.99, Evidence: []string{"slack.message:C123:1"}, Apply: true},
			{UnitID: "unit-user", UnitKind: "slack.thread", Container: "harness-team", Target: "project", Project: "mmo-harness", Kind: "build", Summary: "Again", Confidence: 0.99, Evidence: []string{"slack.message:C123:1760950800.000100"}, Apply: true},
		},
	})
	if err != nil {
		t.Fatalf("record later run: %v", err)
	}
	if len(later.Written) != 0 || !reflect.DeepEqual(later.Skipped, []string{"unit-undo", "unit-run-a", "unit-user"}) {
		t.Fatalf("expected a later run to skip undone and user-approved units, got %+v", later)
	}

	current := currentProjectLink(t, config, "unit-undo")
	if current.ID != replacement.ID || current.State != "proposed" {
		t.Fatalf("expected the undo replacement to stay proposed, got %+v", current)
	}
	user := currentProjectLink(t, config, "unit-user")
	if user.Source != "user" || user.Project != "ask-kai" || user.State != "applied" {
		t.Fatalf("expected the user-approved link to survive the later run, got %+v", user)
	}
}

func TestProjectLinkReadsDoNotMigrateAndWritesValidate(t *testing.T) {
	home := initProjectUnitsHome(t)
	config := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir, DatabasePath: home.DatabasePath}

	links, err := workgraph.ReadProjectLinks(config, workgraph.ProjectLinksQuery{})
	if err != nil || len(links) != 0 {
		t.Fatalf("expected no links before the first write, got %+v, %v", links, err)
	}
	if projectLinksTableExists(t, home.DatabasePath) {
		t.Fatalf("expected a read-only query not to create project_links")
	}

	invalid := []workgraph.ProjectLinkRun{
		{Source: "user", Links: []workgraph.ProjectLinkProposal{projectLinkProposal("unit", "p", "container_mapping", true)}},
		{Source: "llm", Links: []workgraph.ProjectLinkProposal{{UnitID: "unit", UnitKind: "slack.thread", Target: "project", Project: "p", Kind: "gossip", Confidence: 0.5, Evidence: []string{"e"}}}},
		{Source: "llm", Links: []workgraph.ProjectLinkProposal{{UnitID: "unit", UnitKind: "slack.thread", Target: "project", Project: "p", Kind: "fyi", Confidence: 1.5, Evidence: []string{"e"}}}},
		{Source: "llm", Links: []workgraph.ProjectLinkProposal{{UnitID: "unit", UnitKind: "slack.thread", Target: "project", Project: "p", Kind: "fyi", Confidence: 0.5}}},
		{Source: "llm", Links: []workgraph.ProjectLinkProposal{{UnitID: "unit", UnitKind: "slack.thread", Target: "new_project", Kind: "fyi", Confidence: 0.5, Evidence: []string{"e"}}}},
	}
	for index, run := range invalid {
		if _, err := workgraph.RecordProjectLinkRun(config, run); err == nil {
			t.Fatalf("expected invalid run %d to be refused: %+v", index, run)
		}
	}

	long := strings.Repeat("x", 130)
	result, err := workgraph.RecordProjectLinkRun(config, workgraph.ProjectLinkRun{
		Source: "llm",
		Links: []workgraph.ProjectLinkProposal{
			{UnitID: "unit", UnitKind: "slack.thread", Target: "project", Project: "p", Kind: "fyi", Summary: long, Confidence: 0.5, Evidence: []string{"e"}},
			{UnitID: "unit", UnitKind: "slack.thread", Target: "none", Kind: "fyi", Summary: "dup", Confidence: 0.5, Evidence: []string{"e"}},
		},
	})
	if err != nil {
		t.Fatalf("record run: %v", err)
	}
	if len(result.Written) != 1 || len([]rune(result.Written[0].Summary)) != 120 || !reflect.DeepEqual(result.Skipped, []string{"unit"}) {
		t.Fatalf("expected one link per unit with a 120 character summary, got %+v", result)
	}
	if !projectLinksTableExists(t, home.DatabasePath) {
		t.Fatalf("expected the first write to create project_links")
	}
}

type projectUnitsHome struct {
	HomeDir      string
	DatabasePath string
}

func initProjectUnitsHome(t *testing.T) projectUnitsHome {
	t.Helper()
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	result, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	return projectUnitsHome{HomeDir: homeDir, DatabasePath: result.DatabasePath}
}

func projectUnitFixtureEvents(base time.Time) []storedEvent {
	return []storedEvent{
		slackUnitEvent("C123", "1760950800.000100", "", base, "harness-team"),
		slackUnitEvent("C123", "1760951400.000200", "1760950800.000100", base.Add(10*time.Minute), "harness-team"),
		slackUnitEvent("C123", "1760952000.000300", "1760950800.000100", base.Add(20*time.Minute), "harness-team"),
		slackUnitEvent("C123", "1760954400.000100", "", base.Add(time.Hour), "harness-team"),
		slackUnitEvent("C123", "1760958000.000100", "", base.Add(2*time.Hour), "harness-team"),
		slackUnitEvent("C123", "1760968800.000100", "", base.Add(5*time.Hour), "harness-team"),
		slackUnitEvent("D456", "1760983200.000100", "", base.Add(9*time.Hour), "D456"),
		{ID: "github.pull_request:acme/harness:42", Source: "github", Type: "github.pull_request", Timestamp: base.Add(30 * time.Minute), Project: "harness", Payload: `{"repository":"Acme/Harness","number":42,"title":"Add harness"}`},
		{ID: "github.pull_request.review_requested:acme/harness:42:1", Source: "github", Type: "github.pull_request.review_requested", Timestamp: base.Add(15 * time.Minute), Project: "Harness", Payload: `{"repository":"acme/harness","number":42}`},
		{ID: "github.issue:acme/harness:7", Source: "github", Type: "github.issue", Timestamp: base, Project: "Harness", Payload: `{"repository":"acme/harness","number":7}`},
		{ID: "git.commit:aaa111", Source: "git", Type: "git.commit", Timestamp: base, Project: "harness", Payload: `{"repo_path":"/work/harness","commit":"aaa111"}`},
		{ID: "git.commit:bbb222", Source: "git", Type: "git.commit", Timestamp: base.Add(90 * time.Minute), Project: "harness", Payload: `{"repo_path":"/work/harness","commit":"bbb222"}`},
		{ID: "git.commit:ccc333", Source: "git", Type: "git.commit", Timestamp: base.Add(4 * time.Hour), Project: "harness", Payload: `{"repo_path":"/work/harness","commit":"ccc333"}`},
		{ID: "notion.page_updated:page-1:2026-10-20T09:00:00.000Z", Source: "notion", Type: "notion.page_updated", Timestamp: base, Payload: `{"object":"page","id":"page-1"}`},
		{ID: "notion.page_updated:page-1:2026-10-20T11:00:00.000Z", Source: "notion", Type: "notion.page_updated", Timestamp: base.Add(2 * time.Hour), Payload: `{"object":"page","id":"page-1"}`},
		{ID: "notion.page:page-1", Source: "notion", Type: "notion.page", Timestamp: base, Payload: `{"object":"page","id":"page-1"}`},
		{ID: "calendar.event:google:primary:evt-1", Source: "calendar", Type: "calendar.event", Timestamp: base.Add(3 * time.Hour), Payload: `{"provider":"google","calendar_id":"primary","event_id":"evt-1"}`},
		{ID: "file-1", Source: "file", Type: "created", Timestamp: base, Project: "harness", Payload: `{}`},
	}
}

func slackUnitEvent(channelID, ts, threadTS string, timestamp time.Time, project string) storedEvent {
	eventType := "slack.message"
	if threadTS != "" && threadTS != ts {
		eventType = "slack.thread_reply"
	}
	payload := fmt.Sprintf(`{"channel_id":%q,"ts":%q,"text":"hello"}`, channelID, ts)
	if threadTS != "" {
		payload = fmt.Sprintf(`{"channel_id":%q,"ts":%q,"thread_ts":%q,"text":"hello"}`, channelID, ts, threadTS)
	}
	return storedEvent{
		ID:        fmt.Sprintf("%s:%s:%s", eventType, channelID, ts),
		Source:    "slack",
		Type:      eventType,
		Timestamp: timestamp,
		Project:   project,
		Payload:   payload,
	}
}

func insertProjectUnitEvent(t *testing.T, dbPath string, event storedEvent) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, summary, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.Source, event.Type, event.Timestamp.UTC().Format(time.RFC3339Nano), event.Payload, event.Project, event.Summary, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert event %s: %v", event.ID, err)
	}
}

func projectUnitEventsSnapshot(t *testing.T, dbPath string) string {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var snapshot sql.NullString
	if err := db.QueryRow(`SELECT group_concat(id || '|' || type || '|' || timestamp || '|' || payload_json || '|' || COALESCE(project, ''), ';') FROM (SELECT * FROM events ORDER BY id)`).Scan(&snapshot); err != nil {
		t.Fatalf("snapshot events: %v", err)
	}
	return snapshot.String
}

func projectLinksTableExists(t *testing.T, dbPath string) bool {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'project_links')`).Scan(&exists); err != nil {
		t.Fatalf("check project_links: %v", err)
	}
	return exists
}

func projectLinkProposal(unitID, project, rule string, apply bool) workgraph.ProjectLinkProposal {
	return workgraph.ProjectLinkProposal{
		UnitID:    unitID,
		UnitKind:  "slack.thread",
		Container: "harness-team",
		Target:    "project",
		Project:   project,
		Evidence:  []string{"slack.message:C123:1760950800.000100"},
		Rule:      rule,
		Apply:     apply,
	}
}

func assertProjectLinkRow(t *testing.T, config workgraph.ProjectLinkStoreConfig, id int64, state, decision string) {
	t.Helper()
	links, err := workgraph.ReadProjectLinks(config, workgraph.ProjectLinksQuery{})
	if err != nil {
		t.Fatalf("read links: %v", err)
	}
	for _, link := range links {
		if link.ID != id {
			continue
		}
		if link.State != state || link.Decision != decision || link.DecidedAt.IsZero() {
			t.Fatalf("expected link %d state %q decision %q, got %+v", id, state, decision, link)
		}
		return
	}
	t.Fatalf("link %d not found in %+v", id, links)
}

func currentProjectLink(t *testing.T, config workgraph.ProjectLinkStoreConfig, unitID string) workgraph.ProjectLink {
	t.Helper()
	links, err := workgraph.ReadProjectLinks(config, workgraph.ProjectLinksQuery{UnitID: unitID})
	if err != nil {
		t.Fatalf("read links: %v", err)
	}
	for _, link := range links {
		if link.State == "proposed" || link.State == "applied" {
			return link
		}
	}
	t.Fatalf("no current link for %s in %+v", unitID, links)
	return workgraph.ProjectLink{}
}
