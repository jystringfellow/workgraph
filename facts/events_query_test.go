package facts

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestEventsTodayFiltersByProjectAndQuerySubstring(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initRes, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := localTodayAnchor(time.Now())
	insertEvent(t, initRes.DatabasePath, storedEvent{
		ID:          "evt-alpha-1",
		Source:      "git",
		Type:        "git.commit",
		Timestamp:   now.Add(-2 * time.Hour),
		Project:     "alpha",
		Actor:       "alice",
		Involvement: `["authored"]`,
		Summary:     "Implement OAuth refresh flow",
		Payload:     `{"commit":"11111112222222","branch":"main","subject":"Implement OAuth refresh flow"}`,
	})
	insertEvent(t, initRes.DatabasePath, storedEvent{
		ID:          "evt-alpha-2",
		Source:      "file",
		Type:        "file.modified",
		Timestamp:   now.Add(-90 * time.Minute),
		Project:     "alpha",
		Actor:       "alice",
		Involvement: `["authored"]`,
		Summary:     "Update README notes",
		Payload:     `{"path":"/workspace/alpha/README.md","operation":"modified"}`,
	})
	insertEvent(t, initRes.DatabasePath, storedEvent{
		ID:          "evt-beta-1",
		Source:      "git",
		Type:        "git.commit",
		Timestamp:   now.Add(-time.Hour),
		Project:     "beta",
		Actor:       "bob",
		Involvement: `["reviewed"]`,
		Summary:     "Implement OAuth token verifier",
		Payload:     `{"commit":"33333334444444","branch":"main","subject":"Implement OAuth token verifier"}`,
	})

	byProject, err := workgraph.EventsToday(workgraph.EventsTodayConfig{
		HomeDir:      homeDir,
		DatabasePath: initRes.DatabasePath,
		Project:      "alpha",
	})
	if err != nil {
		t.Fatalf("EventsToday by project failed: %v", err)
	}
	if len(byProject.Events) != 2 {
		t.Fatalf("expected 2 alpha events, got %d", len(byProject.Events))
	}
	if !strings.Contains(byProject.Message, "Project: alpha") {
		t.Fatalf("expected Project header in message, got:\n%s", byProject.Message)
	}

	byProjectAndQuery, err := workgraph.EventsToday(workgraph.EventsTodayConfig{
		HomeDir:      homeDir,
		DatabasePath: initRes.DatabasePath,
		Project:      "alpha",
		Query:        "oauth REFRESH",
	})
	if err != nil {
		t.Fatalf("EventsToday by project and query failed: %v", err)
	}
	if len(byProjectAndQuery.Events) != 1 || byProjectAndQuery.Events[0].ID != "evt-alpha-1" {
		t.Fatalf("expected only evt-alpha-1, got %+v", byProjectAndQuery.Events)
	}
	if !strings.Contains(byProjectAndQuery.Message, "Query: oauth REFRESH") {
		t.Fatalf("expected Query header in message, got:\n%s", byProjectAndQuery.Message)
	}

	byPathQuery, err := workgraph.EventsToday(workgraph.EventsTodayConfig{
		HomeDir:      homeDir,
		DatabasePath: initRes.DatabasePath,
		Query:        "readme.md",
	})
	if err != nil {
		t.Fatalf("EventsToday by path query failed: %v", err)
	}
	if len(byPathQuery.Events) != 1 || byPathQuery.Events[0].ID != "evt-alpha-2" {
		t.Fatalf("expected evt-alpha-2 for path query, got %+v", byPathQuery.Events)
	}
}

func TestEventsTodayJSONFormatIsValidAndDeterministic(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initRes, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	now := localTodayAnchor(time.Now())
	insertEvent(t, initRes.DatabasePath, storedEvent{
		ID:          "evt-json-1",
		Source:      "github",
		Type:        "github.pull_request",
		Timestamp:   now.Add(-30 * time.Minute),
		Project:     "workgraph",
		Actor:       "octocat",
		Involvement: `["authored","mentioned"]`,
		Summary:     "Add JSON event query support",
		Payload:     `{"number":99,"state":"open","title":"Add JSON event query support","path":"/repos/workgraph/pulls/99"}`,
	})

	cfg := workgraph.EventsTodayConfig{
		HomeDir:      homeDir,
		DatabasePath: initRes.DatabasePath,
		Type:         "github.*",
		Project:      "workgraph",
		Actor:        "octocat",
		Involvement:  "authored",
		Query:        "json event",
		Format:       "json",
	}
	first, err := workgraph.EventsToday(cfg)
	if err != nil {
		t.Fatalf("first EventsToday JSON failed: %v", err)
	}
	second, err := workgraph.EventsToday(cfg)
	if err != nil {
		t.Fatalf("second EventsToday JSON failed: %v", err)
	}
	if first.Message != second.Message {
		t.Fatalf("expected deterministic JSON output, got:\n%s\nvs:\n%s", first.Message, second.Message)
	}

	var decoded struct {
		Date        string `json:"date"`
		Type        string `json:"type"`
		Project     string `json:"project"`
		Actor       string `json:"actor"`
		Involvement string `json:"involvement"`
		Query       string `json:"query"`
		Events      []struct {
			ID          string   `json:"id"`
			Source      string   `json:"source"`
			Type        string   `json:"type"`
			Timestamp   string   `json:"timestamp"`
			Project     string   `json:"project"`
			Actor       string   `json:"actor"`
			Involvement []string `json:"involvement"`
			Path        string   `json:"path"`
			Summary     string   `json:"summary"`
			Label       string   `json:"label"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(first.Message), &decoded); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, first.Message)
	}
	if decoded.Date == "" || decoded.Type != "github.*" || decoded.Project != "workgraph" || decoded.Actor != "octocat" || decoded.Involvement != "authored" || decoded.Query != "json event" {
		t.Fatalf("unexpected top-level JSON metadata: %+v", decoded)
	}
	if len(decoded.Events) != 1 {
		t.Fatalf("expected 1 event in JSON output, got %d", len(decoded.Events))
	}
	ev := decoded.Events[0]
	if ev.ID != "evt-json-1" || ev.Source != "github" || ev.Type != "github.pull_request" || ev.Project != "workgraph" || ev.Actor != "octocat" {
		t.Fatalf("unexpected event fields in JSON output: %+v", ev)
	}
	if len(ev.Involvement) != 2 || ev.Involvement[0] != "authored" || ev.Involvement[1] != "mentioned" {
		t.Fatalf("unexpected involvement in JSON output: %+v", ev.Involvement)
	}
	if _, err := time.Parse(time.RFC3339, ev.Timestamp); err != nil {
		t.Fatalf("expected RFC3339 timestamp in JSON output, got %q: %v", ev.Timestamp, err)
	}
}

func TestQueryEventsAcrossMultiDayRangeWithFiltersAndLimit(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initRes, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	loc := time.FixedZone("test-local", -7*60*60)
	day1 := time.Date(2026, 5, 10, 10, 0, 0, 0, loc)
	day2 := time.Date(2026, 5, 11, 14, 0, 0, 0, loc)
	day3 := time.Date(2026, 5, 12, 16, 0, 0, 0, loc)
	day4 := time.Date(2026, 5, 13, 9, 0, 0, 0, loc)

	insertEvent(t, initRes.DatabasePath, storedEvent{
		ID:          "range-1",
		Source:      "git",
		Type:        "git.commit",
		Timestamp:   day1,
		Project:     "workgraph",
		Actor:       "alice",
		Involvement: `["authored"]`,
		Summary:     "Initial schema migration",
		Payload:     `{"commit":"aaaa111","branch":"main","subject":"Initial schema migration"}`,
	})
	insertEvent(t, initRes.DatabasePath, storedEvent{
		ID:          "range-2",
		Source:      "git",
		Type:        "git.commit",
		Timestamp:   day2,
		Project:     "workgraph",
		Actor:       "alice",
		Involvement: `["authored"]`,
		Summary:     "Add SQLite index for query migration",
		Payload:     `{"commit":"bbbb222","branch":"main","subject":"Add SQLite index for query migration"}`,
	})
	insertEvent(t, initRes.DatabasePath, storedEvent{
		ID:          "range-3",
		Source:      "git",
		Type:        "git.commit",
		Timestamp:   day3,
		Project:     "workgraph",
		Actor:       "alice",
		Involvement: `["authored"]`,
		Summary:     "Finalize migration docs",
		Payload:     `{"commit":"cccc333","branch":"main","subject":"Finalize migration docs"}`,
	})
	insertEvent(t, initRes.DatabasePath, storedEvent{
		ID:          "range-other-source",
		Source:      "slack",
		Type:        "slack.message",
		Timestamp:   day2.Add(time.Hour),
		Project:     "workgraph",
		Actor:       "alice",
		Involvement: `["authored"]`,
		Summary:     "Discuss migration rollout",
		Payload:     `{"channel_id":"C1"}`,
	})
	insertEvent(t, initRes.DatabasePath, storedEvent{
		ID:          "range-outside",
		Source:      "git",
		Type:        "git.commit",
		Timestamp:   day4,
		Project:     "workgraph",
		Actor:       "alice",
		Involvement: `["authored"]`,
		Summary:     "Out of window migration commit",
		Payload:     `{"commit":"dddd444","branch":"main","subject":"Out of window migration commit"}`,
	})

	res, err := workgraph.QueryEvents(workgraph.EventsQueryConfig{
		HomeDir:      homeDir,
		DatabasePath: initRes.DatabasePath,
		Since:        day1.Add(-time.Hour),
		Until:        day3.Add(time.Hour),
		Source:       "git",
		Type:         "git.*",
		Project:      "workgraph",
		Actor:        "alice",
		Involvement:  "authored",
		Query:        "MIGRATION",
		Limit:        2,
	})
	if err != nil {
		t.Fatalf("QueryEvents failed: %v", err)
	}
	if len(res.Events) != 2 {
		t.Fatalf("expected 2 events after limit, got %d", len(res.Events))
	}
	if res.Events[0].ID != "range-2" || res.Events[1].ID != "range-3" {
		t.Fatalf("expected most recent 2 matching events [range-2, range-3], got [%s, %s]", res.Events[0].ID, res.Events[1].ID)
	}
	if !strings.Contains(res.Message, "range-2") || !strings.Contains(res.Message, "range-3") || strings.Contains(res.Message, "range-1") {
		t.Fatalf("unexpected text output from QueryEvents:\n%s", res.Message)
	}

	jsonRes, err := workgraph.QueryEvents(workgraph.EventsQueryConfig{
		HomeDir:      homeDir,
		DatabasePath: initRes.DatabasePath,
		Since:        day1.Add(-time.Hour),
		Until:        day3.Add(time.Hour),
		Source:       "git",
		Type:         "git.commit",
		Project:      "workgraph",
		Query:        "sqlite index",
		Format:       "json",
	})
	if err != nil {
		t.Fatalf("QueryEvents JSON failed: %v", err)
	}
	var parsed struct {
		Source  string `json:"source"`
		Type    string `json:"type"`
		Project string `json:"project"`
		Query   string `json:"query"`
		Events  []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(jsonRes.Message), &parsed); err != nil {
		t.Fatalf("unmarshal QueryEvents JSON: %v\n%s", err, jsonRes.Message)
	}
	if len(parsed.Events) != 1 || parsed.Events[0].ID != "range-2" {
		t.Fatalf("expected range-2 in QueryEvents JSON, got %+v", parsed)
	}
}

func TestQueryEventsOrdersAndBoundsFractionalRFC3339NanoTimestamps(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initRes, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	db, err := sql.Open("sqlite3", initRes.DatabasePath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	rows := []struct {
		id        string
		timestamp string
	}{
		{"ts-whole", "2026-06-01T12:00:00Z"},
		{"ts-offset-quarter", "2026-06-01T08:00:00.25-04:00"},
		{"ts-half", "2026-06-01T12:00:00.5Z"},
	}
	for _, row := range rows {
		if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at)
			VALUES (?, 'git', 'git.commit', ?, '{"commit":"abc","subject":"ts test"}', 'workgraph', 'alice', 'ts test', ?)`,
			row.id, row.timestamp, row.timestamp); err != nil {
			t.Fatalf("insert %s: %v", row.id, err)
		}
	}

	since := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	untilHalf := time.Date(2026, 6, 1, 12, 0, 0, 500_000_000, time.UTC)
	untilAfter := time.Date(2026, 6, 1, 12, 0, 1, 0, time.UTC)

	all, err := workgraph.QueryEvents(workgraph.EventsQueryConfig{
		HomeDir:      homeDir,
		DatabasePath: initRes.DatabasePath,
		Since:        since,
		Until:        untilAfter,
	})
	if err != nil {
		t.Fatalf("QueryEvents all failed: %v", err)
	}
	if len(all.Events) != 3 {
		t.Fatalf("expected all 3 fractional/offset events in [%s, %s), got %d: %+v", since, untilAfter, len(all.Events), all.Events)
	}
	if all.Events[0].ID != "ts-whole" || all.Events[1].ID != "ts-offset-quarter" || all.Events[2].ID != "ts-half" {
		t.Fatalf("expected chronological order [ts-whole, ts-offset-quarter, ts-half], got [%s, %s, %s]", all.Events[0].ID, all.Events[1].ID, all.Events[2].ID)
	}

	bounded, err := workgraph.QueryEvents(workgraph.EventsQueryConfig{
		HomeDir:      homeDir,
		DatabasePath: initRes.DatabasePath,
		Since:        since,
		Until:        untilHalf,
	})
	if err != nil {
		t.Fatalf("QueryEvents bounded failed: %v", err)
	}
	if len(bounded.Events) != 2 || bounded.Events[0].ID != "ts-whole" || bounded.Events[1].ID != "ts-offset-quarter" {
		t.Fatalf("expected [ts-whole, ts-offset-quarter] in [12:00:00Z, 12:00:00.5Z), got %+v", bounded.Events)
	}

	limited, err := workgraph.QueryEvents(workgraph.EventsQueryConfig{
		HomeDir:      homeDir,
		DatabasePath: initRes.DatabasePath,
		Since:        since,
		Until:        untilAfter,
		Limit:        1,
	})
	if err != nil {
		t.Fatalf("QueryEvents limited failed: %v", err)
	}
	if len(limited.Events) != 1 || limited.Events[0].ID != "ts-half" {
		t.Fatalf("expected Limit:1 to keep newest event ts-half, got %+v", limited.Events)
	}
}
