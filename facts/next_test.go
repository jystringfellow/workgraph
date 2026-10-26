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
	"slices"
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
	gh           *fakeGHGraphQL
}

func newNextFixture(t *testing.T) *nextFixture {
	t.Helper()
	home := t.TempDir()
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	f := &nextFixture{home: home, db: initialized.DatabasePath, now: time.Now().UTC().Truncate(time.Second), gh: writeFakeGHGraphQL(t, t.TempDir())}
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
			o["review_request"] = map[string]any{"direct": true, "teams": []string{"acme/platform"}, "unresolved_team": false}
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
	if args[0] == "resume" {
		args = append([]string{"resume", "--home", f.home}, args[1:]...)
	} else {
		args = append(args, "--home", f.home)
	}
	command := exec.Command(workgraphFactsBinary, args...)
	command.Env = append(os.Environ(), "PATH="+filepath.Dir(f.gh.Path)+string(os.PathListSeparator)+os.Getenv("PATH"))
	before := strings.Join(f.gh.Invocations(t), "\n")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, output)
	}
	if args[0] == "resume" {
		after := strings.Join(f.gh.Invocations(t), "\n")
		if len(after) <= len(before) || !strings.HasSuffix(after, "auth status --hostname github.com") {
			t.Fatal("resume did not discover the GitHub identity through the fake gh")
		}
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
	text := f.cli(t, "next")
	withIDs := f.cli(t, "next", "--ids")
	for _, item := range items {
		header := fmt.Sprintf("  %s#%d %s", item.PullRequest.Repository, item.PullRequest.Number, item.PullRequest.Title)
		if !strings.Contains(text, "\n"+header) {
			t.Fatalf("expected a line leading with %q: %s", header, text)
		}
		if strings.Contains(text, item.ID) || !strings.Contains(withIDs, item.ID) {
			t.Fatalf("expected item ids only with --ids: %s\n%s", text, withIDs)
		}
	}
	if strings.Contains(text, "[github:") {
		t.Fatalf("text output carries item ids: %s", text)
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

	t.Run("provider strings are safe only in text", func(t *testing.T) {
		f := newNextFixture(t)
		unsafe := "\x1b7\x1b8\x1bc\u009b31m\u009b0m\x1b[31m\u202e\u2066\u200b\u200c\u200d\u2060\ufeff\x00\t\r\n\u0085\x1b[0m\x1b]0;hidden title\a"
		title := "Visible" + unsafe + " title"
		f.observations[0]["title"] = title
		f.observations[0]["threads"].([]any)[0].(map[string]any)["path"] = "settings" + unsafe + ".go"
		f.save(t, f.observations[0])
		f.exec(t, `UPDATE events SET actor=? WHERE type='github.pull_request.review_comment'`, "ali"+unsafe+"ce")
		f.observations[1]["review_request"].(map[string]any)["teams"] = []string{"acme/plat" + unsafe + "form"}
		f.save(t, f.observations[1])
		f.observations[2]["opinionated_reviews"].([]any)[0].(map[string]any)["reviewer"] = "b" + unsafe + "ob"
		f.save(t, f.observations[2])
		items := f.items(t)
		if items[0].PullRequest.Title != title || items[0].Actor.Login != "ali"+unsafe+"ce" || items[0].Subject.Path != "settings"+unsafe+".go" {
			t.Fatalf("JSON changed provider strings: %+v", items[0])
		}
		if items[1].Detail["request"].(map[string]any)["teams"].([]any)[0] != "acme/plat"+unsafe+"form" || items[2].Detail["reviewer"] != "b"+unsafe+"ob" {
			t.Fatal("JSON changed team or reviewer")
		}
		for _, args := range [][]string{{"next"}, {"resume"}, {"resume", "demo"}} {
			out := f.cli(t, args...)
			for _, want := range []string{"Visible title", "alice replied on settings.go:18", "review requested via acme/platform", "changes requested by bob"} {
				if !strings.Contains(out, want) {
					t.Errorf("%v missing sanitized %q: %q", args, want, out)
				}
			}
			if strings.ContainsAny(out, "\x1b\u202e\u2066\u200b\u200c\u200d\u2060\ufeff\x00\r\u0085") || strings.Contains(out, "hidden title") {
				t.Errorf("%v contains unsafe provider text: %q", args, out)
			}
		}
	})

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
func TestNextDetailLinesStayReadable(t *testing.T) {
	f := newNextFixture(t)
	at := f.now.Add(-5 * time.Minute).Format(time.RFC3339Nano)
	threads := f.observations[0]["threads"].([]any)
	f.observations[0]["threads"] = append(threads, map[string]any{"thread_id": "T2", "path": "settings.go", "line": 18, "depth": 1, "last_comment_id": "C2", "last_comment_at": at, "last_responder": "other", "awaiting": "user"})
	f.save(t, f.observations[0])
	f.event(t, "github.pull_request.review_comment:PR0:C2", "review_comment", at, map[string]any{"node_id": "PR0", "role": "author", "triage": map[string]any{"needs_user": true, "thread_id": "T2"}, "actor_kind": "user", "untrusted": map[string]any{"comment_url": "https://github.com/acme/demo/pull/1#discussion_r2"}})

	f.observations[2]["threads"] = nil
	f.save(t, f.observations[2])

	failedRuns := func(names ...string) []any {
		runs := []any{}
		for i, name := range names {
			runs = append(runs, map[string]any{"id": fmt.Sprintf("RUN%d", i), "name": name, "status": "COMPLETED", "conclusion": "FAILURE", "completed_at": at})
		}
		return runs
	}
	f.observations[3]["check_runs"] = failedRuns(append(slices.Repeat([]string{"build"}, 20), "lint")...)
	f.save(t, f.observations[3])

	threads = f.observations[0]["threads"].([]any)
	for _, id := range []string{"3", "4"} {
		threads = append(threads, map[string]any{"thread_id": "T" + id, "path": "settings.go", "depth": 1, "last_comment_id": "C" + id, "last_comment_at": at, "last_responder": "other", "awaiting": "user"})
		f.event(t, "github.pull_request.review_comment:PR0:C"+id, "review_comment", at, map[string]any{"node_id": "PR0", "role": "author", "triage": map[string]any{"needs_user": true, "thread_id": "T" + id}, "actor_kind": "user"})
	}
	f.observations[0]["threads"] = threads
	f.save(t, f.observations[0])

	f.observations[5]["conversation"] = map[string]any{"last_human_comment_id": "IC9", "last_human_comment_at": at, "last_human_comment_url": "https://github.com/acme/demo/pull/6#issuecomment-9"}
	f.save(t, f.observations[5])
	f.event(t, "github.pull_request.observed:PR5:author", "observed", at, map[string]any{"node_id": "PR5", "role": "author", "baseline": map[string]any{"conversation": map[string]any{"last_human_comment_id": "IC9"}}})

	text := f.cli(t, "next")
	if strings.Contains(text, "[unknown:") {
		t.Fatalf("text shows an internal unknown token: %s", text)
	}
	if !strings.Contains(text, "changes requested by bob (not yet known: review threads)") {
		t.Fatalf("expected unknown threads to read as not yet known: %s", text)
	}
	if !strings.Contains(text, "checks failed on abc123 (build +1 more)") {
		t.Fatalf("expected the summary to count checks past the JSON cap: %s", text)
	}
	if strings.Count(text, "alice replied on settings.go\n") != 2 {
		t.Fatalf("expected threads without a line to stay on separate lines: %s", text)
	}
	if strings.Count(text, "settings.go:18") != 1 || !strings.Contains(text, "alice replied on settings.go:18 (2 threads) — https://github.com/acme/demo/pull/1#discussion_r1") {
		t.Fatalf("expected one line for two threads on the same line, linking the first: %s", text)
	}
	if !strings.Contains(text, "reply requested in conversation — https://github.com/acme/demo/pull/6#issuecomment-9") {
		t.Fatalf("expected the conversation comment to name its location and link: %s", text)
	}
	if withIDs := f.cli(t, "next", "--ids"); !strings.Contains(withIDs, "github:reply:PR0:T1:C1, github:reply:PR0:T2:C2]") {
		t.Fatalf("expected the collapsed line to keep both item ids: %s", withIDs)
	}

	items := f.items(t)
	replies, checks, conversationURL := 0, 0, ""
	for _, item := range items {
		if item.PullRequest.NodeID == "PR0" && item.Reason == "reply" {
			replies++
		}
		if item.PullRequest.NodeID == "PR5" && item.Reason == "reply" {
			conversationURL = item.Subject.URL
		}
		if item.Reason == "checks_failed" {
			if failed, ok := item.Detail["failed_checks"].([]any); ok {
				checks = len(failed)
			}
		}
	}
	if replies != 4 || checks != 20 {
		t.Fatalf("JSON should keep every thread and the capped checks, got %d replies and %d checks", replies, checks)
	}
	if conversationURL != "https://github.com/acme/demo/pull/6#issuecomment-9" {
		t.Fatalf("JSON should carry the conversation comment URL, got %q", conversationURL)
	}

	f.observations[3]["check_runs"] = failedRuns("lint", " lint ", "Lint")
	f.save(t, f.observations[3])
	if text := f.cli(t, "next"); !strings.Contains(text, "checks failed on abc123 (lint +1 more)") {
		t.Fatalf("expected names trimmed before dedupe with case variants kept: %s", text)
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
	if text := f.cli(t, "next"); !strings.Contains(text, "(not yet known: check status)") || strings.Contains(text, "[unknown:") {
		t.Fatalf("unknown dimensions should read as not yet known: %s", text)
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
	header := func(item workgraph.NextItem) string {
		return fmt.Sprintf("%s#%d %s", item.PullRequest.Repository, item.PullRequest.Number, item.PullRequest.Title)
	}
	for _, item := range f.items(t)[:5] {
		if !strings.Contains(resume.Message, header(item)) || strings.Contains(resume.Message, item.ID) {
			t.Fatalf("missing %s: %s", header(item), resume.Message)
		}
	}
	if !strings.Contains(resume.Message, "1 more: workgraph next") || strings.Contains(resume.Message, header(f.items(t)[5])) {
		t.Fatalf("limit: %s", resume.Message)
	}
	project, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: f.home, Project: "demo", Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range f.items(t) {
		if !strings.Contains(project.Message, header(item)) {
			t.Fatalf("project missing %s", header(item))
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
	for _, corrupt := range []string{"event", "observation", "settings"} {
		t.Run(corrupt, func(t *testing.T) {
			f := newNextFixture(t)
			switch corrupt {
			case "event":
				f.exec(t, `UPDATE events SET payload_json='{"node_id":[]}' WHERE type='github.pull_request.review_comment'`)
			case "observation":
				f.exec(t, `UPDATE github_pr_observations SET observation_json='{"node_id":[]}' WHERE node_id='PR0'`)
			case "settings":
				if err := os.WriteFile(filepath.Join(f.home, "connectors.json"), []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := workgraph.Next(f.config()); err == nil {
				t.Fatal("next must report the projection error")
			}
			for _, args := range [][]string{{"today"}, {"resume"}, {"resume", "demo"}, {"resume", "elsewhere"}} {
				t.Run(strings.Join(args, " "), func(t *testing.T) {
					out := f.cli(t, args...)
					if strings.Count(out, "Waiting on you") != 1 || !strings.Contains(out, "Waiting on you unavailable: invalid JSON") {
						t.Fatalf("expected exactly one short warning: %s", out)
					}
					if strings.Contains(out, "[github:") || strings.Contains(out, "Waiting on you:") {
						t.Fatalf("partial waiting items leaked: %s", out)
					}
					want := "Today"
					if args[0] == "resume" {
						want = "Where you left off"
						if len(args) > 1 {
							want = "Resume " + args[1]
						}
					}
					if !strings.Contains(out, want) {
						t.Fatalf("activity view missing: %s", out)
					}
				})
			}
		})
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

func (f *nextFixture) teamReview(t *testing.T, node, repository string, number int, team, author string, age time.Duration) {
	t.Helper()
	at := f.now.Add(-age).Format(time.RFC3339Nano)
	kind := "user"
	if strings.HasSuffix(author, "[bot]") {
		kind = "bot"
	}
	o := map[string]any{"node_id": node, "repository": repository, "number": number, "role": "reviewer", "state": "OPEN", "url": fmt.Sprintf("https://github.com/%s/pull/%d", repository, number), "title": fmt.Sprintf("Change %d", number), "head_ref": "feature", "base_ref": "main", "head_sha": "abc123", "author_login": author, "author_kind": kind, "baseline_at": at, "observed_at": f.now.Format(time.RFC3339Nano), "verified_at": f.now.Format(time.RFC3339Nano), "access": "ok", "review_request": map[string]any{"direct": false, "teams": []string{team}, "unresolved_team": false}}
	f.event(t, "github.pull_request.review_requested:"+node+":"+at, "review_requested", at, map[string]any{"node_id": node, "role": "reviewer", "head_sha": "abc123", "triage": map[string]any{"needs_user": true}, "actor_kind": "user"})
	f.observations = append(f.observations, o)
	f.save(t, o)
}

func TestNextSummarizesTeamOnlyReviewRequestsPerTeam(t *testing.T) {
	f := newNextFixture(t)
	f.teamReview(t, "TEAM10", "acme/widgets", 10, "acme/all-engineers", "dependabot[bot]", 60*time.Minute)
	f.teamReview(t, "TEAM11", "acme/widgets", 11, "acme/all-engineers", "carol", 50*time.Minute)
	f.teamReview(t, "TEAM12", "acme/widgets", 12, "acme/all-engineers", "dependabot[bot]", 40*time.Minute)
	f.teamReview(t, "TEAM8", "acme/gadgets", 8, "acme/all-engineers", "renovate[bot]", 30*time.Minute)
	f.teamReview(t, "TEAM3", "acme/apps", 3, "acme/mobile", "dave", 20*time.Minute)
	f.teamReview(t, "BEHALF", "acme/widgets", 20, "acme/all-engineers", "copilot-swe-agent[bot]", 15*time.Minute)
	f.authoredPR(t, "BEHALF", "acme/widgets", 20, "copilot-swe-agent[bot]", 15*time.Minute, nil)
	f.teamReview(t, "MIXED", "acme/widgets", 30, "acme/all-engineers", "erin", 14*time.Minute)
	f.authoredPR(t, "MIXED", "acme/widgets", 30, "erin", 14*time.Minute, func(o map[string]any) { o["mergeable"] = "CONFLICTING" })

	if items := f.items(t); len(items) != 14 {
		t.Fatalf("JSON must keep every item, got %d", len(items))
	}
	header := func(repository string, number int) string {
		return fmt.Sprintf("  %s#%d Change %d", repository, number, number)
	}
	text := f.cli(t, "next")
	engineers := "\nTeam review requests via acme/all-engineers: 4 PRs (3 bot) across 2 repos\n"
	mobile := "\nTeam review requests via acme/mobile: 1 PR across 1 repo\n"
	summary := strings.Index(text, engineers)
	if summary < 0 || strings.Index(text, mobile) < summary {
		t.Fatalf("expected one summary per team in label order: %s", text)
	}
	for _, direct := range []string{"  acme/demo#1 Work 1", "  acme/demo#2 Work 2", header("acme/widgets", 20), header("acme/widgets", 30)} {
		if at := strings.Index(text, direct); at < 0 || at > summary {
			t.Fatalf("expected %q listed before team summaries: %s", direct, text)
		}
	}
	if strings.Index(text, "\nWaiting on you\n") > strings.Index(text, "  acme/demo#2 Work 2") || !strings.Contains(text, "review requested via acme/platform") {
		t.Fatalf("direct-plus-team request must stay under Waiting on you: %s", text)
	}
	newest := []int{strings.Index(text, header("acme/widgets", 11)), strings.Index(text, header("acme/gadgets", 8)), strings.Index(text, header("acme/widgets", 12)), strings.Index(text, mobile)}
	for i := 1; i < len(newest); i++ {
		if newest[i-1] < summary || newest[i-1] > newest[i] {
			t.Fatalf("expected three team PRs under their summary, human-authored first, then newest first: %s", text)
		}
	}
	if strings.Contains(text, header("acme/widgets", 10)) || strings.Count(text, "\n  1 more: workgraph next --team-reviews") != 1 {
		t.Fatalf("expected the oldest team PR folded into a more line: %s", text)
	}
	if !strings.Contains(text[strings.Index(text, mobile):], header("acme/apps", 3)) {
		t.Fatalf("expected the mobile team PR under its summary: %s", text)
	}

	expanded := f.cli(t, "next", "--team-reviews")
	order := []string{engineers, header("acme/gadgets", 8), header("acme/widgets", 10), header("acme/widgets", 11), header("acme/widgets", 12), mobile, header("acme/apps", 3)}
	last := -1
	for _, want := range order {
		at := strings.Index(expanded, want)
		if at <= last {
			t.Fatalf("expected %q after the previous team line in team then repository order: %s", want, expanded)
		}
		last = at
	}
	if strings.Contains(expanded, "more: workgraph next --team-reviews") {
		t.Fatalf("expanded output must not fold: %s", expanded)
	}
	command := exec.Command(workgraphFactsBinary, "next", "--team-reviews", "--json", "--home", f.home)
	if err := command.Run(); err == nil {
		t.Fatal("--team-reviews with --json succeeded")
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
		t.Fatalf("usage error: %v", err)
	}

	today, err := workgraph.Today(workgraph.TodayConfig{HomeDir: f.home, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(today.Message, "Waiting on you: 9 items + 5 team review requests (workgraph next)") {
		t.Fatalf("today: %s", today.Message)
	}
	resume, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: f.home, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	for _, team := range []string{header("acme/widgets", 10), header("acme/widgets", 11), header("acme/widgets", 12), header("acme/gadgets", 8), header("acme/apps", 3)} {
		if strings.Contains(resume.Message, strings.TrimSpace(team)) {
			t.Fatalf("resume listed team-only %q: %s", team, resume.Message)
		}
	}
	if !strings.Contains(resume.Message, "\n5 team review requests: workgraph next") {
		t.Fatalf("resume must count team-only requests: %s", resume.Message)
	}

	snoozed := ""
	for _, item := range f.items(t, "--project", "demo", "--reason", "review_requested") {
		if item.PullRequest.NodeID == "TEAM3" {
			snoozed = item.ID
		}
	}
	if snoozed == "" {
		t.Fatal("team-only item missing from JSON")
	}
	if out := f.cli(t, "next", "snooze", snoozed, "--for", "1h"); !strings.Contains(out, "Snoozed: "+snoozed) {
		t.Fatalf("snooze: %s", out)
	}
	if out := f.cli(t, "next", "--team-reviews"); strings.Contains(out, mobile) || strings.Contains(out, header("acme/apps", 3)) {
		t.Fatalf("snoozed team-only item still listed: %s", out)
	}
	today, err = workgraph.Today(workgraph.TodayConfig{HomeDir: f.home, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(today.Message, "Waiting on you: 9 items + 4 team review requests (workgraph next)") {
		t.Fatalf("today after snooze: %s", today.Message)
	}
}

func (f *nextFixture) authoredPR(t *testing.T, node, repository string, number int, author string, age time.Duration, mutate func(map[string]any)) {
	t.Helper()
	at := f.now.Add(-age).Format(time.RFC3339Nano)
	kind := "user"
	if strings.HasSuffix(author, "[bot]") {
		kind = "bot"
	}
	o := map[string]any{"node_id": node, "repository": repository, "number": number, "role": "author", "state": "OPEN", "url": fmt.Sprintf("https://github.com/%s/pull/%d", repository, number), "title": fmt.Sprintf("Change %d", number), "head_ref": "feature", "base_ref": "main", "head_sha": "abc123", "author_login": author, "author_kind": kind, "baseline_at": at, "observed_at": f.now.Format(time.RFC3339Nano), "verified_at": f.now.Format(time.RFC3339Nano), "detail_fetched_at": f.now.Format(time.RFC3339Nano), "access": "ok", "threads": []any{}, "opinionated_reviews": []any{}, "checks_state": "none", "mergeable": "MERGEABLE", "ready": "false"}
	if mutate != nil {
		mutate(o)
	}
	if o["mergeable"] == "CONFLICTING" {
		f.event(t, "github.pull_request.merge_conflict:"+node+":abc123:"+at, "merge_conflict", at, map[string]any{"node_id": node, "role": "author", "head_sha": "abc123", "triage": map[string]any{"needs_user": true}, "actor_kind": "user"})
	}
	f.observations = append(f.observations, o)
	f.save(t, o)
}

func TestNextClassifiesTeamOnlyRequestsBeforeFilters(t *testing.T) {
	f := newNextFixture(t)
	f.teamReview(t, "MIXED", "acme/widgets", 30, "acme/all-engineers", "erin", 14*time.Minute)
	f.authoredPR(t, "MIXED", "acme/widgets", 30, "erin", 14*time.Minute, func(o map[string]any) { o["mergeable"] = "CONFLICTING" })
	f.teamReview(t, "TEAM8", "acme/gadgets", 8, "acme/all-engineers", "renovate[bot]", 30*time.Minute)
	for _, args := range [][]string{{"next", "--reason", "review_requested"}, {"next", "--project", "demo", "--reason", "review_requested"}} {
		out := f.cli(t, args...)
		mixed := strings.Index(out, "\n  acme/widgets#30 Change 30")
		summary := strings.Index(out, "\nTeam review requests via acme/all-engineers: 1 PR (1 bot) across 1 repo\n")
		if mixed < 0 || summary < 0 || mixed > summary || strings.Index(out, "\nWaiting on you\n") > mixed {
			t.Fatalf("%v: a team request on a PR with other items must stay under Waiting on you: %s", args, out)
		}
	}
}

func (f *nextFixture) rawItems(t *testing.T, args ...string) []map[string]any {
	t.Helper()
	var items []map[string]any
	if err := json.Unmarshal([]byte(f.cli(t, append([]string{"next", "--json"}, args...)...)), &items); err != nil {
		t.Fatal(err)
	}
	return items
}

func (f *nextFixture) changesRequested(t *testing.T, node string, age time.Duration) {
	t.Helper()
	at := f.now.Add(-age).Format(time.RFC3339Nano)
	f.event(t, "github.pull_request.changes_requested:"+node+":R-"+node, "changes_requested", at, map[string]any{"node_id": node, "role": "author", "head_sha": "abc123", "triage": map[string]any{"needs_user": true}, "actor_kind": "user"})
}

func (f *nextFixture) checksFailed(t *testing.T, node string, age time.Duration) {
	t.Helper()
	at := f.now.Add(-age).Format(time.RFC3339Nano)
	f.event(t, "github.pull_request.checks_failed:"+node+":abc123:fp-"+node, "checks_failed", at, map[string]any{"node_id": node, "role": "author", "head_sha": "abc123", "triage": map[string]any{"needs_user": true, "run_fingerprint": "fp-" + node}, "actor_kind": "user"})
}

func nextSections(items []map[string]any) map[string]string {
	sections := map[string]string{}
	for _, item := range items {
		pr := item["pull_request"].(map[string]any)
		sections[fmt.Sprintf("%s#%v:%s", pr["repository"], pr["number"], item["reason"])] = fmt.Sprint(item["section"])
	}
	return sections
}

func TestNextGroupsTheUsersFixesApartFromBlockedWork(t *testing.T) {
	f := newNextFixture(t)
	f.authoredPR(t, "HARBOR", "acme/harbor-harness", 7, "octo", 5*time.Minute, func(o map[string]any) {
		o["opinionated_reviews"] = []any{map[string]any{"review_id": "R-HARBOR", "reviewer": "quinn", "actor_kind": "user", "state": "CHANGES_REQUESTED"}}
		o["checks_state"] = "failed"
		o["check_runs"] = []any{map[string]any{"id": "RUN7", "name": "unit", "status": "COMPLETED", "conclusion": "FAILURE", "details_url": "https://github.com/acme/harbor-harness/actions/runs/7"}}
	})
	f.changesRequested(t, "HARBOR", 5*time.Minute)
	f.checksFailed(t, "HARBOR", 4*time.Minute)

	text := f.cli(t, "next")
	at := func(want string) int { return strings.Index(text, want) }
	waiting, fix, blocked, ready := at("\nWaiting on you\n"), at("\nNeeds your fix\n"), at("\nBlocked\n"), at("\nReady\n")
	if waiting < 0 || fix < waiting || blocked < fix || ready < blocked {
		t.Fatalf("expected Waiting on you, Needs your fix, Blocked, then Ready sections: %s", text)
	}
	for _, header := range []string{"\n  acme/demo#4 Work 4", "\n  acme/demo#5 Work 5"} {
		if line := at(header); line < fix || line > blocked {
			t.Fatalf("expected the failing-checks-only and conflicts-only PR %q under Needs your fix: %s", header, text)
		}
	}
	for _, header := range []string{"\n  acme/demo#3 Work 3", "\n  acme/harbor-harness#7 Change 7"} {
		if line := at(header); line < blocked || line > ready {
			t.Fatalf("expected the PR waiting on a reviewer %q under Blocked: %s", header, text)
		}
	}

	items := f.rawItems(t)
	want := map[string]string{
		"acme/demo#1:reply":                       "waiting_on_you",
		"acme/demo#2:review_requested":            "waiting_on_you",
		"acme/demo#3:changes_requested":           "blocked",
		"acme/demo#4:checks_failed":               "needs_fix",
		"acme/demo#5:conflicts":                   "needs_fix",
		"acme/demo#6:ready_to_merge":              "ready",
		"acme/harbor-harness#7:changes_requested": "blocked",
		"acme/harbor-harness#7:checks_failed":     "blocked",
	}
	if got := nextSections(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON sections: %v", got)
	}
	for _, item := range items {
		if item["reason"] == "checks_failed" && item["group"] != "blocked" {
			t.Fatalf("group must keep its reason mapping: %v", item)
		}
	}

	filtered := f.cli(t, "next", "--reason", "checks_failed")
	if strings.Index(filtered, "\n  acme/harbor-harness#7 Change 7") < strings.Index(filtered, "\nBlocked\n") || strings.Index(filtered, "\nBlocked\n") < strings.Index(filtered, "\nNeeds your fix\n") {
		t.Fatalf("a PR with changes requested stays Blocked when filtered to its failing checks: %s", filtered)
	}
}

func TestNextCollapsesStalePullRequests(t *testing.T) {
	f := newNextFixture(t)
	old := 45 * 24 * time.Hour
	stale := func(o map[string]any) { o["updated_at"] = f.now.Add(-old).Format(time.RFC3339Nano) }
	conflicted := func(o map[string]any) { stale(o); o["mergeable"] = "CONFLICTING" }
	f.authoredPR(t, "OLD1", "acme/widgets", 101, "octo", old, conflicted)
	f.authoredPR(t, "OLD2", "acme/gizmos", 102, "octo", old, conflicted)
	f.authoredPR(t, "OLD3", "acme/widgets", 103, "octo", 40*24*time.Hour, func(o map[string]any) {
		stale(o)
		o["opinionated_reviews"] = []any{map[string]any{"review_id": "R-OLD3", "reviewer": "quinn", "actor_kind": "user", "state": "CHANGES_REQUESTED"}}
	})
	f.changesRequested(t, "OLD3", 40*24*time.Hour)
	f.authoredPR(t, "RECENT", "acme/widgets", 104, "octo", 29*24*time.Hour, func(o map[string]any) {
		o["updated_at"] = f.now.Add(-29 * 24 * time.Hour).Format(time.RFC3339Nano)
		o["mergeable"] = "CONFLICTING"
	})
	f.authoredPR(t, "ASKED", "acme/widgets", 105, "octo", old, func(o map[string]any) {
		stale(o)
		o["threads"] = []any{map[string]any{"thread_id": "T-ASKED", "path": "main.go", "line": 3, "depth": 1, "last_comment_id": "C-ASKED", "last_responder": "other", "awaiting": "user"}}
	})
	f.event(t, "github.pull_request.review_comment:ASKED:C-ASKED", "review_comment", f.now.Add(-2*24*time.Hour).Format(time.RFC3339Nano), map[string]any{"node_id": "ASKED", "role": "author", "actor_kind": "user"})
	f.teamReview(t, "REVIEW", "acme/gizmos", 106, "acme/all-engineers", "atlas-dev", 3*24*time.Hour)
	f.observations[len(f.observations)-1]["review_request"] = map[string]any{"direct": true, "teams": []string{}, "unresolved_team": false}
	stale(f.observations[len(f.observations)-1])
	f.save(t, f.observations[len(f.observations)-1])

	header := func(repository string, number int) string {
		return fmt.Sprintf("\n  %s#%d Change %d", repository, number, number)
	}
	text := f.cli(t, "next")
	fix, blocked, ready := strings.Index(text, "\nNeeds your fix\n"), strings.Index(text, "\nBlocked\n"), strings.Index(text, "\nReady\n")
	fixLine := strings.Index(text, "\n  2 stale PRs (no activity in 30+ days): workgraph next --stale\n")
	blockedLine := strings.Index(text, "\n  1 stale PR (no activity in 30+ days): workgraph next --stale\n")
	if fixLine < fix || fixLine > blocked || blockedLine < blocked || blockedLine > ready {
		t.Fatalf("expected one stale line under Needs your fix and one under Blocked: %s", text)
	}
	if strings.Count(text, "stale PR") != 2 {
		t.Fatalf("expected exactly two stale lines: %s", text)
	}
	for _, folded := range []string{header("acme/widgets", 101), header("acme/gizmos", 102), header("acme/widgets", 103)} {
		if strings.Contains(text, folded) {
			t.Fatalf("expected stale PR %q collapsed: %s", folded, text)
		}
	}
	for _, listed := range []string{header("acme/widgets", 104), header("acme/widgets", 105), header("acme/gizmos", 106)} {
		if !strings.Contains(text, listed) {
			t.Fatalf("expected %q listed; recent activity and recent requests are never collapsed: %s", listed, text)
		}
	}

	expanded := f.cli(t, "next", "--stale")
	for _, listed := range []string{header("acme/widgets", 101), header("acme/gizmos", 102), header("acme/widgets", 103), header("acme/widgets", 104), header("acme/widgets", 105)} {
		if !strings.Contains(expanded, listed) {
			t.Fatalf("--stale must list %q: %s", listed, expanded)
		}
	}
	if strings.Contains(expanded, "workgraph next --stale") || !strings.Contains(expanded, "\n  2 stale PRs (no activity in 30+ days):\n") {
		t.Fatalf("--stale must list stale PRs under their stale line without the hint: %s", expanded)
	}
	if line, pr := strings.Index(expanded, "\n  2 stale PRs (no activity in 30+ days):\n"), strings.Index(expanded, header("acme/widgets", 101)); pr < line || pr > strings.Index(expanded, "\nBlocked\n") {
		t.Fatalf("--stale must list stale PRs in their section after the stale line: %s", expanded)
	}
	command := exec.Command(workgraphFactsBinary, "next", "--stale", "--json", "--home", f.home)
	if err := command.Run(); err == nil {
		t.Fatal("--stale with --json succeeded")
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
		t.Fatalf("usage error: %v", err)
	}

	items := f.rawItems(t)
	if len(items) != 12 {
		t.Fatalf("JSON must keep every item, got %d", len(items))
	}
	for _, item := range items {
		node := item["pull_request"].(map[string]any)["node_id"]
		want := node == "OLD1" || node == "OLD2" || node == "OLD3"
		if value, ok := item["stale"].(bool); !ok || value != want {
			t.Fatalf("expected stale=%v on %v: %v", want, node, item)
		}
	}
}

func TestNextTeamReviewPreviewShowsPeopleFirst(t *testing.T) {
	f := newNextFixture(t)
	f.teamReview(t, "HUMAN1", "acme/widgets", 41, "acme/all-engineers", "rowan", 90*time.Minute)
	f.teamReview(t, "HUMAN2", "acme/gizmos", 42, "acme/all-engineers", "sky", 80*time.Minute)
	f.teamReview(t, "BOT1", "acme/widgets", 43, "acme/all-engineers", "atlas-bot[bot]", 30*time.Minute)
	f.teamReview(t, "BOT2", "acme/widgets", 44, "acme/all-engineers", "atlas-bot[bot]", 20*time.Minute)
	f.teamReview(t, "BOT3", "acme/gizmos", 45, "acme/all-engineers", "atlas-bot[bot]", 10*time.Minute)
	text := f.cli(t, "next")
	summary := strings.Index(text, "\nTeam review requests via acme/all-engineers: 5 PRs (3 bot) across 2 repos\n")
	order := []int{summary, strings.Index(text, "\n  acme/gizmos#42 Change 42"), strings.Index(text, "\n  acme/widgets#41 Change 41"), strings.Index(text, "\n  acme/gizmos#45 Change 45"), strings.Index(text, "\n  2 more: workgraph next --team-reviews")}
	for i := 1; i < len(order); i++ {
		if order[i-1] < 0 || order[i] <= order[i-1] {
			t.Fatalf("expected human-authored PRs newest first, then the newest bot PR, then the more line: %s", text)
		}
	}
	if strings.Contains(text, "#43 Change 43") || strings.Contains(text, "#44 Change 44") {
		t.Fatalf("expected older bot PRs folded into the more line: %s", text)
	}
}

func nextStaleByNode(items []workgraph.NextItem) map[string]bool {
	stale := map[string]bool{}
	for _, item := range items {
		stale[item.PullRequest.NodeID] = item.Stale
	}
	return stale
}

func TestNextStaleIgnoresBaselineRequestsAndFallsBackToItemTimes(t *testing.T) {
	f := newNextFixture(t)
	old := f.now.Add(-45 * 24 * time.Hour).Format(time.RFC3339Nano)
	today := f.now.Add(-time.Minute).Format(time.RFC3339Nano)
	f.authoredPR(t, "BASEREPLY", "acme/widgets", 201, "octo", 45*24*time.Hour, func(o map[string]any) {
		o["updated_at"] = old
		o["baseline_at"] = today
		o["threads"] = []any{map[string]any{"thread_id": "T-BASE", "path": "main.go", "line": 9, "depth": 1, "last_comment_id": "C-BASE", "last_responder": "other", "awaiting": "user"}}
	})
	f.event(t, "github.pull_request.observed:BASEREPLY:"+today, "observed", today, map[string]any{"node_id": "BASEREPLY", "role": "author", "baseline_at": today, "baseline": map[string]any{"threads": []any{map[string]any{"thread_id": "T-BASE", "last_comment_id": "C-BASE"}}}})
	f.observations = append(f.observations, map[string]any{"node_id": "BASEREVIEW", "repository": "acme/gizmos", "number": 202, "role": "reviewer", "state": "OPEN", "url": "https://github.com/acme/gizmos/pull/202", "title": "Change 202", "head_ref": "feature", "base_ref": "main", "head_sha": "abc123", "author_login": "rowan", "author_kind": "user", "updated_at": old, "baseline_at": today, "observed_at": f.now.Format(time.RFC3339Nano), "verified_at": f.now.Format(time.RFC3339Nano), "access": "ok", "review_request": map[string]any{"direct": true, "teams": []string{}, "unresolved_team": false}})
	f.save(t, f.observations[len(f.observations)-1])
	f.event(t, "github.pull_request.observed:BASEREVIEW:"+today, "observed", today, map[string]any{"node_id": "BASEREVIEW", "role": "reviewer", "baseline_at": today})
	f.authoredPR(t, "NOUPDATE", "acme/widgets", 203, "octo", 45*24*time.Hour, func(o map[string]any) { o["mergeable"] = "CONFLICTING" })

	items := f.project(t, f.config())
	reasons := map[string]string{}
	for _, item := range items {
		reasons[item.PullRequest.NodeID] = item.Reason
	}
	if reasons["BASEREPLY"] != "reply" || reasons["BASEREVIEW"] != "review_requested" || reasons["NOUPDATE"] != "conflicts" {
		t.Fatalf("fixture items: %v", reasons)
	}
	stale := nextStaleByNode(items)
	if !stale["BASEREPLY"] || !stale["BASEREVIEW"] {
		t.Fatalf("a request seen only in today's first-capture baseline must not keep an old PR fresh: %v", stale)
	}
	if !stale["NOUPDATE"] {
		t.Fatalf("without updated_at, an old PR is stale by its newest item since: %v", stale)
	}
	if stale["PR0"] || stale["PR4"] {
		t.Fatalf("without updated_at, a PR with recent items is not stale: %v", stale)
	}
	text := f.cli(t, "next")
	if !strings.Contains(text, "\n  2 stale PRs (no activity in 30+ days): workgraph next --stale\n") || strings.Contains(text, "#201 Change 201") || strings.Contains(text, "#202 Change 202") {
		t.Fatalf("expected the baseline-only requests collapsed as stale under Waiting on you: %s", text)
	}
}

func TestNextStaleStartsAtExactlyThirtyDays(t *testing.T) {
	f := newNextFixture(t)
	updated := f.now.Add(-time.Hour)
	conflicted := func(at string) func(map[string]any) {
		return func(o map[string]any) { o["updated_at"] = at; o["mergeable"] = "CONFLICTING" }
	}
	f.authoredPR(t, "UTC", "acme/widgets", 301, "octo", time.Hour, conflicted(updated.UTC().Format(time.RFC3339Nano)))
	f.authoredPR(t, "EAST", "acme/widgets", 302, "octo", time.Hour, conflicted(updated.In(time.FixedZone("east", 5*60*60+30*60)).Format(time.RFC3339Nano)))
	f.authoredPR(t, "WEST", "acme/widgets", 303, "octo", time.Hour, conflicted(updated.In(time.FixedZone("west", -8*60*60)).Format(time.RFC3339Nano)))
	for _, tc := range []struct {
		offset time.Duration
		stale  bool
	}{{-time.Nanosecond, false}, {0, true}, {time.Nanosecond, true}} {
		config := f.config()
		config.Now = updated.Add(30*24*time.Hour + tc.offset)
		stale := nextStaleByNode(f.project(t, config))
		for _, node := range []string{"UTC", "EAST", "WEST"} {
			if stale[node] != tc.stale {
				t.Fatalf("30 days %+v after the last update: %s stale=%v, want %v", tc.offset, node, stale[node], tc.stale)
			}
		}
	}
}

func TestNextResumeListsPullRequestsInSectionOrder(t *testing.T) {
	f := newNextFixture(t)
	for _, item := range f.items(t) {
		if item.Reason == "review_requested" {
			f.cli(t, "next", "done", item.ID)
		}
	}
	f.authoredPR(t, "OLDBLOCK", "acme/widgets", 110, "octo", 40*24*time.Hour, func(o map[string]any) {
		o["updated_at"] = f.now.Add(-40 * 24 * time.Hour).Format(time.RFC3339Nano)
		o["opinionated_reviews"] = []any{map[string]any{"review_id": "R-OLDBLOCK", "reviewer": "quinn", "actor_kind": "user", "state": "CHANGES_REQUESTED"}}
	})
	f.changesRequested(t, "OLDBLOCK", 40*24*time.Hour)
	if items := f.items(t); len(items) != 6 {
		t.Fatalf("expected six PRs competing for five resume slots: %v", items)
	}
	resume, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: f.home, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	last := strings.Index(resume.Message, "\nWaiting on you\n")
	for _, header := range []string{"acme/demo#1 Work 1", "acme/demo#4 Work 4", "acme/demo#5 Work 5", "acme/demo#3 Work 3", "acme/demo#6 Work 6", "1 more: workgraph next"} {
		at := strings.Index(resume.Message, header)
		if last < 0 || at <= last {
			t.Fatalf("expected resume to list waiting, fix-only, blocked, then ready PRs, with the stale PR left for the more line; %q out of order: %s", header, resume.Message)
		}
		last = at
	}
	if strings.Contains(resume.Message, "acme/widgets#110") {
		t.Fatalf("the stale PR must sort after every active PR and fall outside the five: %s", resume.Message)
	}
}
