package facts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

type nextFixture struct {
	home         string
	db           string
	now          time.Time
	observations []map[string]any
}

func newNextFixture(t *testing.T) *nextFixture {
	t.Helper()
	home := t.TempDir()
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	f := &nextFixture{home: home, db: initialized.DatabasePath, now: time.Now().UTC().Truncate(time.Second)}
	f.exec(t, `INSERT INTO github_pr_watch_state(connector_id, enabled) VALUES ('github.pr_watch', 1)`)
	f.runtime(t, "ready")
	types := []string{"review_comment", "review_requested", "changes_requested", "checks_failed", "merge_conflict", "ready_to_merge"}
	for i, typ := range types {
		node := fmt.Sprintf("PR%d", i)
		at := f.now.Add(time.Duration(i-10) * time.Minute).Format(time.RFC3339Nano)
		o := map[string]any{"node_id": node, "repository": "acme/demo", "number": i + 1, "role": "author", "state": "OPEN", "url": fmt.Sprintf("https://github.com/acme/demo/pull/%d", i+1), "title": fmt.Sprintf("Work %d", i+1), "head_ref": "feature", "base_ref": "main", "head_sha": "abc123", "head_repository": "acme/demo", "baseline_at": at, "observed_at": f.now.Format(time.RFC3339Nano), "verified_at": f.now.Format(time.RFC3339Nano), "detail_fetched_at": f.now.Format(time.RFC3339Nano), "access": "ok", "threads": []any{}, "opinionated_reviews": []any{}, "checks_state": "none", "mergeable": "MERGEABLE", "ready": "false"}
		discriminator := ""
		triage := map[string]any{"needs_user": true}
		switch i {
		case 0:
			o["threads"] = []any{map[string]any{"thread_id": "T1", "path": "settings.go", "line": 18, "depth": 2, "last_comment_id": "C1", "last_comment_at": at, "last_responder": "other", "agent_replies": 0, "awaiting": "user"}, map[string]any{"thread_id": "T-agent", "last_comment_id": "C-agent", "last_responder": "agent", "awaiting": "other"}}
			discriminator = "C1"
			triage["thread_id"] = "T1"
			triage["has_suggestion"] = true
			triage["previous_responder"] = "author"
			o["head_repository"] = "someone/fork"
			o["is_cross_repository"] = true
		case 1:
			o["role"] = "reviewer"
			o["review_request"] = map[string]any{"direct": false, "teams": []string{"acme/platform"}, "unresolved_team": false}
			discriminator = at
			o["head_repository"] = nil
		case 2:
			o["opinionated_reviews"] = []any{map[string]any{"review_id": "R1", "reviewer": "bob", "actor_kind": "user", "state": "CHANGES_REQUESTED", "submitted_at": at}}
			discriminator = "R1"
		case 3:
			o["checks_state"] = "failed"
			o["check_runs"] = []any{map[string]any{"id": "RUN1", "name": "lint", "status": "COMPLETED", "conclusion": "FAILURE", "completed_at": at, "details_url": "https://github.com/acme/demo/actions/runs/1"}}
			discriminator = "abc123:fixturehash1"
			triage["run_fingerprint"] = "fixturehash1"
		case 4:
			o["mergeable"] = "CONFLICTING"
			discriminator = "abc123:" + at
		case 5:
			o["ready"] = "true"
			o["merge_state_status"] = "CLEAN"
			discriminator = "abc123:" + at
		}
		payload := map[string]any{"node_id": node, "role": o["role"], "head_sha": "abc123", "triage": triage, "actor_kind": "user", "actor_association": "MEMBER"}
		if i == 0 {
			payload["untrusted"] = map[string]any{"trust": "untrusted", "origin": "github.review_thread_comment", "body": "SECRET COMMENT TEXT", "comment_url": "https://github.com/acme/demo/pull/1#discussion_r1"}
		}
		f.event(t, "github.pull_request."+typ+":"+node+":"+discriminator, typ, at, payload)
		f.observations = append(f.observations, o)
		f.save(t, o)
	}
	return f
}

func (f *nextFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite3", f.db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func (f *nextFixture) save(t *testing.T, o map[string]any) {
	t.Helper()
	raw, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT OR REPLACE INTO github_pr_observations(connector_id,node_id,role,repository,number,observation_json,observed_at) VALUES ('github.pr_watch',?,?,?,?,?,?)`, o["node_id"], o["role"], o["repository"], o["number"], string(raw), o["observed_at"])
}
func (f *nextFixture) event(t *testing.T, id, typ, at string, payload map[string]any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	involvement := `["authored"]`
	if payload["role"] == "reviewer" {
		involvement = `["review_requested"]`
	}
	f.exec(t, `INSERT INTO events(id,source,type,timestamp,created_at,project,actor,payload_json,involvement_json) VALUES (?,'github',?,?,?,'demo','alice',?,?)`, id, "github.pull_request."+typ, at, at, string(raw), involvement)
}
func (f *nextFixture) runtime(t *testing.T, state string) {
	t.Helper()
	raw := fmt.Sprintf(`{"connectors":{"github.pr_watch":{"enabled":true,"interval":"5m","setup_state":%q}}}`, state)
	if err := os.WriteFile(filepath.Join(f.home, "connectors.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
}
func (f *nextFixture) cli(t *testing.T, args ...string) string {
	t.Helper()
	args = append(args, "--home", f.home)
	command := exec.Command(workgraphFactsBinary, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, output)
	}
	return string(output)
}
func (f *nextFixture) items(t *testing.T, args ...string) []workgraph.NextItem {
	t.Helper()
	var items []workgraph.NextItem
	if err := json.Unmarshal([]byte(f.cli(t, append([]string{"next", "--json"}, args...)...)), &items); err != nil {
		t.Fatal(err)
	}
	return items
}

func TestNextListsEachReasonInDocumentedOrder(t *testing.T) {
	f := newNextFixture(t)
	botAt := f.now.Add(-time.Minute).Format(time.RFC3339Nano)
	f.observations[0]["conversation"] = map[string]any{"last_human_comment_id": "BOT", "last_human_comment_at": botAt}
	f.save(t, f.observations[0])
	f.event(t, "github.pull_request.review_comment:PR0:BOT", "review_comment", botAt, map[string]any{"node_id": "PR0", "role": "author", "actor_kind": "bot", "untrusted": map[string]any{"origin": "github.issue_comment"}})
	items := f.items(t)
	reasons := []string{}
	groups := []string{}
	for _, item := range items {
		reasons = append(reasons, item.Reason)
		groups = append(groups, item.Group)
	}
	if !reflect.DeepEqual(reasons, []string{"reply", "review_requested", "changes_requested", "checks_failed", "conflicts", "ready_to_merge"}) {
		t.Fatalf("reasons: %v", reasons)
	}
	if !reflect.DeepEqual(groups, []string{"waiting_on_you", "waiting_on_you", "blocked", "blocked", "blocked", "ready"}) {
		t.Fatalf("groups: %v", groups)
	}
	if got := f.items(t, "--reason", "checks_failed", "--project", "demo"); len(got) != 1 {
		t.Fatalf("filters: %v", got)
	}
	if got := f.items(t, "--project", "dem"); len(got) != 0 {
		t.Fatalf("filter must be exact: %v", got)
	}
	f.exec(t, `UPDATE github_pr_watch_state SET enabled=0`)
	if out := f.cli(t, "next"); !strings.Contains(out, "workgraph github prs connect") {
		t.Fatalf("setup: %s", out)
	}
	f.exec(t, `UPDATE github_pr_watch_state SET enabled=1`)
	f.exec(t, `DELETE FROM github_pr_observations`)
	if out := f.cli(t, "next"); !strings.Contains(out, "Nothing is waiting") || !strings.Contains(out, "Capture freshness:") {
		t.Fatalf("empty state: %s", out)
	}
	if out := strings.TrimSpace(f.cli(t, "next", "--json")); out != "[]" {
		t.Fatalf("empty JSON: %s", out)
	}

}

func TestNextJSONItemsFollowTheV1Schema(t *testing.T) {
	f := newNextFixture(t)
	items := f.items(t)
	again := f.items(t, "--include-untrusted")
	if len(items) != 6 || len(again) != 6 {
		t.Fatalf("items: %v", items)
	}
	fields := []string{"", "request", "review_id", "run_fingerprint", "base_ref", "merge_state_status"}
	for i, item := range items {
		if item.Schema != "workgraph.next_item.v1" || item.ID == "" || item.ID != again[i].ID || item.OpenedBy == "" || len(item.Evidence) == 0 || item.Since.IsZero() {
			t.Fatalf("invalid item: %+v", item)
		}
		if item.Untrusted != nil {
			t.Fatalf("untrusted leaked: %+v", item)
		}
		if fields[i] != "" && item.Detail[fields[i]] == nil {
			t.Fatalf("missing detail %s: %+v", fields[i], item)
		}
	}
	if items[0].PullRequest.HeadRepository == nil || *items[0].PullRequest.HeadRepository != "someone/fork" || !items[0].PullRequest.IsCrossRepository || items[1].PullRequest.HeadRepository != nil {
		t.Fatal("head repository contract")
	}
	if again[0].Untrusted["body"] != "SECRET COMMENT TEXT" {
		t.Fatalf("body: %v", again[0].Untrusted)
	}
	for _, args := range [][]string{{"next"}, {"next", "--json"}} {
		out := f.cli(t, args...)
		if strings.Contains(out, "SECRET COMMENT TEXT") || strings.Contains(out, f.home) {
			t.Fatalf("leaked: %s", out)
		}
	}
	if strings.Count(f.cli(t, "next", "--json", "--include-untrusted"), "SECRET COMMENT TEXT") != 1 {
		t.Fatal("body outside untrusted")
	}
	for _, option := range []string{"--watch", "--include-untrusted"} {
		command := exec.Command(workgraphFactsBinary, "next", option, "--home", f.home)
		if err := command.Run(); err == nil {
			t.Fatalf("%s without JSON succeeded", option)
		} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
			t.Fatalf("usage error: %v", err)
		}
	}

}

func (f *nextFixture) config() workgraph.NextConfig {
	return workgraph.NextConfig{HomeDir: f.home, DatabasePath: f.db, Now: f.now}
}
func (f *nextFixture) project(t *testing.T, config workgraph.NextConfig) []workgraph.NextItem {
	t.Helper()
	result, err := workgraph.Next(config)
	if err != nil {
		t.Fatal(err)
	}
	return result.Items
}
func TestNextDoneAndSnoozeHideOnlyThatItemID(t *testing.T) {
	f := newNextFixture(t)
	items := f.items(t)
	beforeDone, err := workgraph.Today(workgraph.TodayConfig{HomeDir: f.home, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	f.cli(t, "next", "done", items[0].ID)
	afterDone, err := workgraph.Today(workgraph.TodayConfig{HomeDir: f.home, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	if len(afterDone.Events) != len(beforeDone.Events) {
		t.Fatalf("done changed today's event count: before=%d after=%d", len(beforeDone.Events), len(afterDone.Events))
	}
	if got := f.items(t); len(got) != 5 {
		t.Fatalf("done: %v", got)
	}
	o := f.observations[0]
	thread := o["threads"].([]any)[0].(map[string]any)
	thread["last_comment_id"] = "C2"
	f.save(t, o)
	f.event(t, "github.pull_request.review_comment:PR0:C2", "review_comment", f.now.Format(time.RFC3339Nano), map[string]any{"node_id": "PR0", "role": "author", "actor_kind": "user"})
	found := false
	for _, item := range f.items(t) {
		if item.ID == "github:reply:PR0:T1:C2" {
			found = true
		}
	}
	if !found {
		t.Fatal("new feedback hidden by done")
	}
	f.cli(t, "next", "snooze", items[1].ID, "--for", "2h")
	if got := f.items(t); len(got) != 5 {
		t.Fatalf("snooze: %v", got)
	}
	config := f.config()
	config.Now = f.now.Add(2*time.Hour + time.Minute)
	if got := f.project(t, config); len(got) != 6 {
		t.Fatalf("expiry: %v", got)
	}
	db, err := sql.Open("sqlite3", f.db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE source='workgraph' AND type IN ('next.item_done','next.item_snoozed')`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("bookkeeping: %d %v", count, err)
	}
	if err := workgraph.DoneNext(f.config(), items[3].ID); err != nil {
		t.Fatal(err)
	}
	checks := f.observations[3]
	checks["head_sha"] = "newhead"
	checks["check_runs"].([]any)[0].(map[string]any)["id"] = "RUN2"
	f.save(t, checks)
	f.event(t, "github.pull_request.checks_failed:PR3:newhead:newrunset", "checks_failed", f.now.Format(time.RFC3339Nano), map[string]any{"node_id": "PR3", "role": "author", "head_sha": "newhead", "triage": map[string]any{"run_fingerprint": "newrunset"}})
	replacement := f.items(t, "--reason", "checks_failed")
	if len(replacement) != 1 || replacement[0].ID == items[3].ID || replacement[0].Detail["run_fingerprint"] != "newrunset" {
		t.Fatalf("new checks hidden by done: %v", replacement)
	}

}
func TestNextReportsStaleness(t *testing.T) {
	f := newNextFixture(t)
	f.observations[0]["detail_fetched_at"] = f.now.Add(-46 * time.Minute).Format(time.RFC3339Nano)
	f.save(t, f.observations[0])
	f.observations[2]["detail_fetched_at"] = f.now.Add(-20 * time.Minute).Format(time.RFC3339Nano)
	f.save(t, f.observations[2])
	f.observations[3]["verified_at"] = f.now.Add(-16 * time.Minute).Format(time.RFC3339Nano)
	f.save(t, f.observations[3])
	f.observations[4]["access"] = "unreachable"
	f.observations[4]["checks_state"] = "unknown"
	f.save(t, f.observations[4])
	items := f.project(t, f.config())
	for i, want := range []bool{true, false, false, true, true, false} {
		if items[i].Freshness.Stale != want {
			t.Fatalf("item %d stale=%v", i, items[i].Freshness)
		}
	}
	if items[4].Freshness.Access != "unreachable" || !strings.Contains(fmt.Sprint(items[4].Triage["unknown"]), "checks") {
		t.Fatalf("unknown/access: %+v", items[4])
	}
	if !strings.Contains(f.cli(t, "next"), "unknown: checks") {
		t.Fatal("unknown missing from text")
	}
	f.runtime(t, "error")
	for _, item := range f.project(t, f.config()) {
		if !item.Freshness.Stale {
			t.Fatal("setup error not stale")
		}
	}
	out := f.cli(t, "next")
	if !strings.Contains(out, "setup error") || !strings.Contains(out, "workgraph connectors validate github.pr_watch") {
		t.Fatalf("warning: %s", out)
	}
}

func startNextWatch(t *testing.T, f *nextFixture, config workgraph.NextConfig) <-chan workgraph.NextWatchEvent {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	lines := make(chan workgraph.NextWatchEvent, 100)
	config.WatchInterval = 10 * time.Millisecond
	go func() { defer writer.Close(); _ = workgraph.WatchNext(ctx, config, writer) }()
	go func() {
		defer close(lines)
		decoder := json.NewDecoder(reader)
		for {
			var line workgraph.NextWatchEvent
			if err := decoder.Decode(&line); err != nil {
				return
			}
			lines <- line
		}
	}()
	t.Cleanup(func() { cancel(); reader.Close() })
	return lines
}
func nextWatchLine(t *testing.T, lines <-chan workgraph.NextWatchEvent) workgraph.NextWatchEvent {
	t.Helper()
	select {
	case line, ok := <-lines:
		if !ok {
			t.Fatal("watch stopped")
		}
		return line
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not emit within two seconds")
		return workgraph.NextWatchEvent{}
	}
}
func nextWatchInitial(t *testing.T, lines <-chan workgraph.NextWatchEvent) {
	t.Helper()
	for i := 0; i < 6; i++ {
		line := nextWatchLine(t, lines)
		if line.Op != "opened" || !line.Initial {
			t.Fatalf("initial: %+v", line)
		}
	}
	if line := nextWatchLine(t, lines); line.Op != "synced" || line.AsOf == nil {
		t.Fatalf("synced: %+v", line)
	}
}
func TestNextWatchEmitsTheInitialSetThenDifferences(t *testing.T) {
	f := newNextFixture(t)
	t.Run("CLI initial JSON Lines", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, workgraphFactsBinary, "next", "--watch", "--json", "--home", f.home)
		output, err := command.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { cancel(); _ = command.Wait() }()
		decoder := json.NewDecoder(output)
		for i := 0; i < 7; i++ {
			var line workgraph.NextWatchEvent
			if err := decoder.Decode(&line); err != nil {
				t.Fatal(err)
			}
			if i < 6 && (line.Op != "opened" || !line.Initial) {
				t.Fatalf("initial: %+v", line)
			}
			if i == 6 && line.Op != "synced" {
				t.Fatalf("synced: %+v", line)
			}
		}
	})
	var clock atomic.Int64
	clock.Store(f.now.UnixNano())
	config := f.config()
	config.Clock = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	lines := startNextWatch(t, f, config)
	nextWatchInitial(t, lines)
	at := f.now.Format(time.RFC3339Nano)
	f.observations[5]["ready"] = "false"
	f.observations[0]["conversation"] = map[string]any{"last_human_comment_id": "C3", "last_human_comment_at": at}
	db, err := sql.Open("sqlite3", f.db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, o := range []map[string]any{f.observations[0], f.observations[5]} {
		raw, err := json.Marshal(o)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`UPDATE github_pr_observations SET observation_json=? WHERE node_id=?`, string(raw), o["node_id"]); err != nil {
			t.Fatal(err)
		}
	}
	payload := `{"node_id":"PR0","role":"author","actor_kind":"user","untrusted":{"body":"PRIVATE"}}`
	if _, err := tx.Exec(`INSERT INTO events(id,source,type,timestamp,created_at,project,actor,payload_json,involvement_json) VALUES ('github.pull_request.review_comment:PR0:C3','github','github.pull_request.review_comment',?,?,'demo','alice',?,'["authored"]')`, at, at, payload); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if line := nextWatchLine(t, lines); line.Op != "closed" || line.Reason != "resolved" {
		t.Fatalf("resolved: %+v", line)
	}
	opened := nextWatchLine(t, lines)
	if opened.Op != "opened" || opened.Initial || opened.Item.Untrusted != nil || opened.Item.Subject.ID != "C3" {
		t.Fatalf("opened: %+v", opened)
	}
	if err := workgraph.SnoozeNext(config, opened.Item.ID, time.Minute); err != nil {
		t.Fatal(err)
	}
	if line := nextWatchLine(t, lines); line.Op != "closed" || line.Reason != "snoozed" {
		t.Fatalf("snoozed: %+v", line)
	}
	clock.Store(f.now.Add(2 * time.Minute).UnixNano())
	if line := nextWatchLine(t, lines); line.Op != "opened" || line.Item.ID != opened.Item.ID {
		t.Fatalf("expiry: %+v", line)
	}
	clock.Store(f.now.Add(16 * time.Minute).UnixNano())
	for i := 0; i < 3; i++ {
		line := nextWatchLine(t, lines)
		if line.Op != "changed" || !line.Item.Freshness.Stale {
			t.Fatalf("staleness: %+v", line)
		}
	}
	f.runtime(t, "error")
	for i := 0; i < 3; i++ {
		line := nextWatchLine(t, lines)
		if line.Op != "changed" || !line.Item.Freshness.Stale {
			t.Fatalf("setup: %+v", line)
		}
	}
	f.exec(t, `UPDATE github_pr_watch_state SET enabled=0`)
	for i := 0; i < 6; i++ {
		line := nextWatchLine(t, lines)
		if line.Op != "closed" || line.Reason != "out_of_scope" {
			t.Fatalf("disconnect: %+v", line)
		}
	}
}
func TestNextResumeAndTodayShowTheSameItems(t *testing.T) {
	f := newNextFixture(t)
	resume, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: f.home, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	section := strings.Index(resume.Message, "Waiting on you")
	if section < strings.Index(resume.Message, "Where you left off") || section < 0 {
		t.Fatalf("section: %s", resume.Message)
	}
	for _, item := range f.items(t)[:5] {
		if !strings.Contains(resume.Message, item.ID) {
			t.Fatalf("missing %s: %s", item.ID, resume.Message)
		}
	}
	if !strings.Contains(resume.Message, "1 more: workgraph next") || strings.Contains(resume.Message, f.items(t)[5].ID) {
		t.Fatalf("limit: %s", resume.Message)
	}
	project, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: f.home, Project: "demo", Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range f.items(t) {
		if !strings.Contains(project.Message, item.ID) {
			t.Fatalf("project missing %s", item.ID)
		}
	}
	if strings.Index(project.Message, "Waiting on you") < strings.Index(project.Message, "Recent activity") {
		t.Fatalf("project order: %s", project.Message)
	}
	other, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: f.home, Project: "elsewhere", Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(other.Message, "Waiting on you") {
		t.Fatal("project filter")
	}
	today, err := workgraph.Today(workgraph.TodayConfig{HomeDir: f.home, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(today.Message, "Waiting on you: 6 items (workgraph next)") {
		t.Fatalf("today: %s", today.Message)
	}
	for _, item := range f.items(t) {
		if strings.Contains(today.Message, item.ID) {
			t.Fatal("today must show only count")
		}
	}
	f.exec(t, `UPDATE github_pr_watch_state SET enabled=0`)
	disabled, err := workgraph.Today(workgraph.TodayConfig{HomeDir: f.home, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(today.Events, disabled.Events) {
		t.Fatal("attention items changed today's activity")
	}
}
func TestNextExplainCitesTheRuleAndEvidence(t *testing.T) {
	f := newNextFixture(t)
	for _, item := range f.items(t) {
		out := f.cli(t, "next", "explain", item.ID)
		for _, want := range append([]string{item.Reason, "Condition:", "Observed at:"}, item.Evidence...) {
			if !strings.Contains(out, want) {
				t.Fatalf("explain missing %s: %s", want, out)
			}
		}
	}
}
func TestNextMCPToolReturnsTheSameItemsReadOnly(t *testing.T) {
	f := newNextFixture(t)
	db, err := sql.Open("sqlite3", f.db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var before, after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(workgraphFactsBinary, "bridge", "mcp", "--home", f.home)
	command.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"next_items","arguments":{"project":"demo","reason":"reply"}}}` + "\n")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("MCP: %v %s", err, output)
	}
	var response struct {
		Result struct {
			StructuredContent struct {
				Items []workgraph.NextItem `json:"items"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatal(err)
	}
	got := response.Result.StructuredContent.Items
	want := f.items(t, "--project", "demo", "--reason", "reply")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MCP mismatch: %s", output)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&after); err != nil || before != after {
		t.Fatalf("MCP wrote events: %v %d %d", err, before, after)
	}
}
func TestNextCloseReasonsDoNotOverclaim(t *testing.T) {
	for _, reason := range []string{"left_search", "unreachable", "out_of_scope", "merged", "closed", "done"} {
		t.Run(reason, func(t *testing.T) {
			f := newNextFixture(t)
			lines := startNextWatch(t, f, f.config())
			nextWatchInitial(t, lines)
			index := 0
			if reason == "out_of_scope" {
				index = 1
			}
			o := f.observations[index]
			node := o["node_id"].(string)
			if reason == "done" {
				if err := workgraph.DoneNext(f.config(), f.items(t)[0].ID); err != nil {
					t.Fatal(err)
				}
			} else {
				typ := "unwatched"
				triage := map[string]any{"exit": reason}
				if reason == "out_of_scope" {
					typ = "review_request_removed"
					triage = map[string]any{"reason": "withdrawn"}
				}
				if reason == "merged" || reason == "closed" {
					typ = reason
				}
				f.event(t, "github.pull_request."+typ+":"+node+":exit", typ, f.now.Format(time.RFC3339Nano), map[string]any{"node_id": node, "role": o["role"], "triage": triage})
				f.exec(t, `DELETE FROM github_pr_observations WHERE node_id=?`, node)
			}
			line := nextWatchLine(t, lines)
			if line.Op != "closed" || line.Reason != reason {
				t.Fatalf("close: %+v", line)
			}
		})
	}
}
