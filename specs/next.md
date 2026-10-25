# Next

Status: implemented, with the GitHub pull request watch (`github.pr_watch`) as
its first source. Facts live in `facts/next_test.go`.

## Intent

`today` answers what the user did, and `resume` answers where they left off.
Neither answers **what should I do next?** The answer is scattered across
review requests, feedback on the user's own PRs, failing checks, and, later,
other sources.

`workgraph next` is the single local list of things waiting on the user. It:

- reads only local state; it makes no provider request and needs no LLM
- lists each item with the rule that opened it, a link, and how fresh the
  underlying capture is
- gives every item a stable id and a versioned JSON shape, so a later dispatch
  design can hand items to local or cloud agents without re-deriving them
- never acts on an item (`specs/principles.md`, principle 6)

The first source is the GitHub pull request watch (`specs/github-pr-watch.md`).
Other sources add items by implementing the same contract.

```text
connector polls (interval or push hint)
  -> events + observations committed
  -> next projection (read time)
  -> workgraph next | resume | today hint | next --watch --json | MCP
                                              └─> later: dispatch
```

## Attention Items

An attention item is a read-time projection over stored events and a source's
checkpoint state. Items are not stored, and are not suggestions: a suggestion
is a proposal awaiting approval, while an item is a pointer to something that
closes itself when its condition stops holding.

Each source defines, for every reason it produces:

- the condition under which the item is open
- a deterministic item id that changes when the underlying subject changes,
  so marking an item done never hides later activity
- the event that opened it (`since`)
- the evidence event ids

### Ordering

Items are ordered by reason group, then oldest `since` first:

1. someone is waiting on the user: `reply`, `review_requested`
2. the user's own work is blocked: `changes_requested`, `checks_failed`,
   `conflicts`
3. the user's own work can finish: `ready_to_merge`
4. links awaiting a categorization decision: `categorize`

Sources map their reasons into these groups. The order is deterministic and
documented, not a priority score.

The `categorize` group comes from project links (`specs/projects.md`, Daily
Loop Surfaces). It has one item per link that the default `workgraph projects
review` lists: every proposed link and, while the auto-apply accuracy sample
has fewer than 20 decided links, every undecided auto-applied link. An item
closes when its link is decided. Text output never lists `categorize` items
under `Waiting on you`. It shows one `Project links to review: <n> (<p>
project, <q> no project) (workgraph projects review), <m> auto-applied today
(workgraph projects review --auto)` line and the pause line when auto-apply
is paused. `workgraph next --reason categorize` also lists every item.
JSON, MCP, `explain`, `done`, and `snooze` still cover every item.

### JSON shape

```json
{
  "schema": "workgraph.next_item.v1",
  "id": "github:reply:PR_kwDO...:PRRT_kwDO...:PRRC_kwDO...",
  "source": "github",
  "reason": "reply",
  "group": "waiting_on_you",
  "role": "author",
  "project": "workgraph-demo",
  "since": "2026-10-16T17:02:11Z",
  "opened_by": "github.pull_request.review_comment:PR_kwDO...:PRRC_kwDO...",
  "evidence": ["github.pull_request.review_comment:PR_kwDO...:PRRC_kwDO..."],
  "subject": {
    "kind": "thread",
    "id": "PRRT_kwDO...",
    "url": "https://github.com/example-org/workgraph-demo/pull/42#discussion_r1",
    "path": "settings.go",
    "line": 18
  },
  "pull_request": {
    "repository": "example-org/workgraph-demo",
    "number": 42,
    "node_id": "PR_kwDO...",
    "url": "https://github.com/example-org/workgraph-demo/pull/42",
    "title": "Add settings helper",
    "branch": "feature/settings-helper",
    "base_ref": "main",
    "head_sha": "1111111111111111111111111111111111111111",
    "head_repository": "example-org/workgraph-demo",
    "is_draft": false,
    "is_cross_repository": false,
    "author": {"login": "octo", "kind": "user", "on_behalf": false}
  },
  "actor": {"login": "alice", "kind": "user", "association": "MEMBER"},
  "detail": {},
  "triage": {
    "last_responder": "other",
    "previous_responder": null,
    "agent_replies_in_thread": 0,
    "has_suggestion": true,
    "thread_depth": 1,
    "unknown": []
  },
  "freshness": {"as_of": "2026-10-16T17:02:11Z", "stale": false, "access": "ok"},
  "untrusted": {
    "trust": "untrusted",
    "body": "Could you rename this?",
    "body_truncated": false
  }
}
```

- `schema` is versioned. Adding a field is compatible; removing or renaming a
  field requires a new version.
- `triage` carries the deterministic inputs a later classifier needs. `next`
  does not classify; it never says whether an agent could take the item.
- `untrusted` is present only with `--include-untrusted` and only in JSON.
  It is never rendered in text output.
- `pull_request.head_repository` is the head repository's `owner/name`, or
  `null` when the fork was deleted, so a consumer can find the head remote of
  a cross-repository PR.
- `pull_request.author` is the PR author's login and `kind` (`user` or
  `bot`). `on_behalf` is true when a bot authored a PR that the source
  watches as the user's own, such as a PR `copilot-swe-agent` opened for the
  user. It is absent when the author is unknown.
- `detail` holds reason-specific fields:

  | Reason | `detail` |
  |---|---|
  | `review_requested` | `{"request": {"direct", "teams", "unresolved_team", "incomplete"?}}` |
  | `checks_failed` | `{"run_fingerprint", "failed_checks": [{"name", "conclusion", "details_url"}]}` |
  | `changes_requested` | `{"review_id", "reviewer"}` |
  | `reply` | `{}` (the thread or comment is in `subject`) |
  | `conflicts` | `{"base_ref"}` |
  | `ready_to_merge` | `{"merge_state_status"}` |
  | `categorize` | `{"link_id", "unit_kind", "state", "auto", "source", "target", "project", "proposed_project", "kind", "confidence", "rule", "profile", "run_id"}` |

- A `categorize` item has `source: "projects"`, `group: "categorize"`, an
  empty `role`, `project` set to the unit's container, `since` and
  `freshness.as_of` set to the link's creation time, `opened_by`
  `project_link:<link-id>`, and `subject` `{"kind": "unit", "id":
  "<unit-ref>"}`. The unit ref is the unit id, except that a `git.session`
  unit's local repository path is replaced by the first 12 hex characters of
  its SHA-256, as `git.session:<repo-hash>:<first-sha>`, so the item carries
  no local path. Its `pull_request` is empty. `detail.proposed_project`
  holds only the proposed `slug` and `workstream`. Model-written text, the
  link summary and the proposed project's name and description, appears only
  with `--include-untrusted`, as `untrusted.summary` and
  `untrusted.proposed_project`.

- `freshness.as_of` is when the facts behind this item were last read from
  the provider; each source defines which timestamp that is and when it goes
  stale (see `specs/github-pr-watch.md`, Attention Items). Items are also
  stale while their source is in setup `error`, and when
  `freshness.access` is `unreachable`.
- Local paths, tokens, and connector configuration never appear in an item,
  so items can be sent to a cloud agent without leaking the local machine's
  layout.
  The opt-in `untrusted` object is the exception: it carries captured or
  model-written text verbatim, which may mention anything, including a path.

## Commands

```text
workgraph next [--project <p>] [--reason <r>] [--ids] [--team-reviews]
workgraph next [--project <p>] [--reason <r>] --json [--include-untrusted]
workgraph next --watch --json [--include-untrusted]
workgraph next explain <item-id>
workgraph next done <item-id>
workgraph next snooze <item-id> --for <duration>
```

### `workgraph next`

```text
Next (as of 17:02)
Capture freshness: 2026-10-16T17:02:11Z
Waiting on you
  acme/api#77 Cache eviction [bot] — https://github.com/acme/api/pull/77
    review requested via acme/platform
  acme/workgraph-demo#42 Add settings helper (copilot-swe-agent, on your behalf) — https://github.com/acme/workgraph-demo/pull/42
    alice replied on settings.go:18 (2 threads) — https://github.com/acme/workgraph-demo/pull/42#discussion_r1
    reply requested in conversation — https://github.com/acme/workgraph-demo/pull/42#issuecomment-7
    checks failed on 1111111 (lint +1 more)
    review requested
Blocked
  acme/api#57 Cache warmup — https://github.com/acme/api/pull/57
    changes requested by bob (not yet known: review threads)
Ready
  acme/api#61 Retry policy — https://github.com/acme/api/pull/61
    ready to merge
```

- Text output groups items by pull request: one header line per PR, then one
  indented line per item. A PR appears once, in the section of its first
  item, so PR groups follow the documented item order of their most urgent
  item. Grouping is text-only: JSON, `--watch`, MCP, and item ids are per
  item, so `explain`, `done`, and `snooze` still act on one item.
- The header leads with `owner/repo#number title`, so equal numbers in
  different repositories stay distinct, then `[draft]`, the author label, and
  the PR link. A bot-authored PR the user is only asked to review is tagged
  `[bot]`; one a bot opened on the user's behalf is labeled `(<bot login>,
  on your behalf)`.
- An item line carries its own link only when it differs from the PR link,
  such as a review comment.
- A `reply` line names its location: `on <path>:<line>` for a review thread,
  `in conversation` for a PR conversation comment, which links the comment
  when its URL is known. Reply items for threads on the same `path:line`
  share one line with `(<n> threads)`, linking the first thread.
- A `checks_failed` line names the first failed check, with duplicate names
  removed, then `+<n> more` for the other distinct names. JSON keeps every
  entry of `detail.failed_checks`.
- `triage.unknown` dimensions read as `(not yet known: <labels>)`, using
  `check status`, `review threads`, `approvals`, `merge status`, `merge
  readiness`, `newer comments`, and `newer reviews`. Text output never shows
  the raw dimension tokens.
- Text output omits item ids; `--ids` appends `[<item id>]` to each item
  line. `--json` and `next explain` always carry them.
- Each line is built only from structured fields. Comment text never appears.
  Text output strips ANSI sequences, C0/C1 controls, bidi overrides and isolates,
  and zero-width format characters from provider strings; JSON preserves them.
- A source in setup `error`, or stale, adds a warning line naming the problem
  and the command to fix it, before the items.
- With no enabled sources, `next` says which connectors provide items and how
  to connect them. With sources but no items, it says nothing is waiting and
  shows the freshness line. `categorize` items do not count as waiting on
  the user. The `categorize` summary follows either case and needs no
  connector.
- `--project` and `--reason` are exact filters.

#### Team review requests

A `review_requested` item is a team-only review request when its request is
not direct (`teams` is non-empty or `unresolved_team` is true) and not
`incomplete`, it is the only open item on its pull request, and the pull
request was not opened on the user's behalf. Classification counts every open
item before `--project` and `--reason` filters, so filtering never folds a
request whose pull request has other items. Large teams can request review on hundreds of pull requests,
most of them bot dependency bumps, so text output summarizes these instead of
listing them under `Waiting on you`:

```text
Team review requests via acme/all-engineers: 4 PRs (3 bot) across 2 repos
  acme/widgets#12 Bump yaml [bot] — https://github.com/acme/widgets/pull/12
    review requested via acme/all-engineers
  acme/gadgets#8 Bump lodash [bot] — https://github.com/acme/gadgets/pull/8
    review requested via acme/all-engineers
  acme/widgets#11 Fix flaky test — https://github.com/acme/widgets/pull/11
    review requested via acme/all-engineers
  1 more: workgraph next --team-reviews
```

- Team summaries follow the `Waiting on you`, `Blocked`, and `Ready`
  sections, one per team label, sorted by label. The label is the item's
  teams joined by `, `, or `team (unresolved)`.
- Each summary counts pull requests, bot-authored pull requests (omitted when
  zero), and distinct repositories, then shows the three newest by `since`
  and a `more` line when there are more.
- `--team-reviews` lists every team-only pull request under its summary,
  sorted by repository then number, with no `more` line. It is text-only;
  with `--json` it is a usage error.
- A review request that is direct, `incomplete`, shares its pull request with
  another open item, or is on a pull request opened on the user's behalf
  stays listed under `Waiting on you` as before.
- JSON, `--watch`, MCP, `explain`, `done`, and `snooze` are unchanged and
  still include team-only items, so `done` and `snooze` act on them by id.

### `workgraph next explain <item-id>`

Prints the rule that opened the item, the condition currently holding, the
evidence events with their ids, and the observation time. It is the
deterministic answer to "why is this here?".

### `done` and `snooze`

- `workgraph next done <item-id>` appends a `next.item_done` event (source
  `workgraph`, id `next.item_done:<item-id>`) and hides that item id.
- `workgraph next snooze <item-id> --for <duration>` appends a
  `next.item_snoozed` event (id `next.item_snoozed:<item-id>:<until>`) and
  hides the item until then.
- Because a new comment, head, or run set produces a new item id, done and
  snooze never hide later activity.
- Both are local bookkeeping. Neither writes to a provider or a project
  link; a `categorize` item hidden this way still awaits its decision in
  `workgraph projects review`.

### `workgraph next --watch --json`

The subscription point for anything that reacts to items, including a later
dispatcher. It writes JSON Lines to stdout:

```json
{"op":"opened","item":{...},"initial":true}
{"op":"synced","as_of":"2026-10-16T17:02:11Z"}
{"op":"opened","item":{...}}
{"op":"closed","id":"github:reply:...","reason":"resolved"}
{"op":"changed","item":{...}}
```

- On start it emits every open item as `opened` with `initial: true`, then
  `synced`. A consumer that restarts gets the full set again and must treat
  item ids idempotently.
- After start it re-projects every second against the current clock and
  local state, whether or not anything was written. That covers poll
  commits, `next.*` events, snooze expiry, items becoming stale, setup
  errors, and disconnects. It emits `opened` and `closed` for the
  difference, and `changed` when an open item's non-id fields change, such as
  `freshness.stale` or `triage.unknown`. The projection is a local SQLite
  read, so the one-second cadence costs no provider requests.
- `closed.reason` says what is known, not more:
  - `resolved`: the condition stopped holding on a successful poll, or a
    `categorize` item's link was decided
  - `done` or `snoozed`: the user hid it
  - `merged` or `closed`: the PR was merged or closed
  - `out_of_scope`: the subject left what the source watches, for example a
    withdrawn or completed review request, an archived repository, or a
    disabled source
  - `left_search` or `unreachable`: the source could no longer see the
    subject; this does not claim it was addressed
- A snooze ending re-opens the item with the same id.
- `--watch` without `--json` is a usage error.

Together with push hints, the path from a provider change to a consumer is:
provider change → hint → poll within about 10 seconds → commit → `--watch`
line within about 1 second.

### MCP

The workgraph MCP server exposes a read-only `next_items` tool that returns
the same JSON items, with optional `project` and `reason` filters. Untrusted
fields stay in their delimited `untrusted` object and are never interpolated
into tool descriptions or instructions (`specs/llm-integration.md`).

## Other Surfaces

- **`resume`**: the default overview shows `Waiting on you` after `Where you
  left off`, listing at most five pull requests in `next` order in the same
  grouped format as `next`, then `<n> more: workgraph next` with the number
  of remaining pull requests when more exist. `categorize` items are not
  listed there; they are counted in the `Project links to review` line
  instead (see `specs/resume.md`). Team-only review requests are not listed;
  one `<n> team review requests: workgraph next` line counts them.
  `resume <project>` shows that project's items after `Recent activity`.
  Showing items does not advance the resume check-in beyond the existing
  rules.
- **`today`**: stays a personal activity view. When items other than
  `categorize` are open, it ends with `Waiting on you: <n> items (workgraph
  next)`, counting only those items. Team-only review requests are not part
  of `<n>`; when any are open the line reads `Waiting on you: <n> items + <m>
  team review requests (workgraph next)`. When `categorize` items are open,
  it then shows `Project links to review: <n> (<p> project, <q> no project)
  (workgraph projects review)`. Each line is omitted when its count is zero.
- A failed next projection does not fail `today` or `resume`. They omit the
  waiting items and show one short `Waiting on you unavailable: <error class>`
  warning. `next` itself still reports the error.
- **`connectors status`** shows each source's freshness.

## Dispatch Boundary

`next` does not dispatch. `next --watch --json` and the MCP tool are the
intended starting point for a later dispatch design (roadmap P2; an earlier
draft is parked on `agent/4f0e34fb/pr-routing-draft`), because they let
routing be added or replaced without changing capture. That design makes its
own choices.

## Out Of Scope

- Ranking by priority, effort, or LLM judgment.
- Any provider write, including from `done` or `snooze`.
- Sources other than the GitHub pull request watch and project links. Azure
  Boards assigned items and Slack mentions are natural next sources.

## Facts

Facts live in `facts/next_test.go` and use stored events and observations
only. No fact invokes `gh`.

- **N1. Lists each reason in documented order.** Fixtures for a thread
  awaiting the user, a review request, changes requested, failed checks, a
  conflict, a ready PR, a bot conversation comment, and a thread an agent
  answered list exactly six items, grouped and ordered as documented,
  without the bot comment or the agent-answered thread, and without invoking
  `gh`. Text lines lead with `owner/repo#number title` and contain no item
  id; `--ids` shows them.
- **N2. JSON items follow `workgraph.next_item.v1`.** For one item of each
  reason, the item has a stable id, `opened_by`, evidence ids, the
  documented `detail` fields, `pull_request.head_repository` (including a
  cross-repository PR and one whose fork was deleted, which is `null`), and
  no local path or token. Without `--include-untrusted` it has no
  `untrusted` field; with it, comment text appears only there. Text output
  never contains comment text.
- **N3. Done and snooze hide only that item id.** After `done` on a thread
  item, the thread is not listed; a new reviewer comment in the thread lists
  it again under a new id. A snoozed item returns when the snooze ends. Both
  append `next.*` events and write nothing to providers.
- **N4. Staleness is reported per item.** With the source in setup `error`,
  `next` prints a warning and every item is stale. An item whose own
  `as_of` is past its source's threshold is stale while other items are
  fresh, and an item whose PR is unreachable is stale with
  `access: "unreachable"`. `unknown` dimensions are shown as not yet known.
- **N5. Watch emits the initial set, then differences.** `next --watch
  --json` emits each open item with `initial: true` and then `synced`. After a
  poll commit that opens one item and resolves another, it emits exactly one
  `opened` and one `closed` with `reason: "resolved"`, within two seconds of
  the commit. With polling stopped, advancing the clock past a snooze emits
  `opened` for the same id, past three intervals emits `changed` with
  `freshness.stale: true`, and disconnecting the source emits `closed` with
  `reason: "out_of_scope"`.
- **N11. Categorize items follow project links.** See P10 in
  `specs/projects.md` (`facts/projects_surfaces_test.go`).
- **N9. Close reasons do not overclaim.** A PR dropped after 6 missing polls
  closes its items with `left_search`, an unreachable one with
  `unreachable`, and a withdrawn review request with `out_of_scope`; none
  closes with `resolved`.
- **N6. Resume and today show the same items.** The default `resume` overview
  lists the first five pull requests in `next` order, by `owner/repo#number
  title` and without item ids, and a `more` line. `resume
  <project>` lists that project's items. `today` prints only the count line
  and still counts only personal activity.
- **N7. Explain cites the rule and evidence.** `next explain <id>` prints the
  reason, the holding condition, and every evidence event id.
- **N10. A PR is shown once with all its reasons.** A bot-authored PR in
  both roles renders one header labeled `on your behalf` with its reply and
  review request under it, while JSON keeps both items and their ids
  (`facts/github_pr_watch_reviewer_test.go`).
- **N12. Team-only review requests are summarized per team.** With direct
  items, a direct-plus-team request, an on-behalf bot PR whose only item is a
  team request, an ordinary PR with a team request and a conflict,
  four team-only requests from one team across two repositories (three by
  bots), and one from another team, `next` lists the direct, direct-plus-team,
  and on-behalf PRs under `Waiting on you` and not under a summary, prints one
  summary per team with its counts, the three newest PRs, and a `more` line,
  and omits the oldest team-only PR. `--team-reviews` lists all five in team
  then repository order without a `more` line, and is a usage error with
  `--json`. `--json` still returns every item. `today` counts the five
  separately, and `resume` lists none of them and counts them. Snoozing one
  team-only item by id removes it from `--team-reviews` and the `today`
  count. With `--reason review_requested`, alone or with `--project`, a team
  request on a PR that also has a conflict stays under `Waiting on you`. A
  team request whose captured request list is truncated without the user is
  `incomplete` and stays listed, while a complete one is summarized
  (`facts/github_pr_watch_reviewer_test.go`).
- **N8. MCP returns the same items read-only.** The `next_items` tool returns
  the same ids as `next --json` and has no write behavior.
- **N13. Detail lines stay readable.** With two threads awaiting the user on
  one `path:line`, a failed check name repeated across runs among three
  distinct names, a PR whose threads are not loaded, and a baseline
  conversation comment, `next` prints one `(2 threads)` line linking the
  first thread (with `--ids` naming both ids), `checks failed on <sha>
  (<first> +2 more)`, `(not yet known: review threads)` and never
  `[unknown:`, and `reply requested in conversation — <comment url>`. JSON
  keeps both reply items and every failed check. A first poll that captures a
  conversation comment stores its URL so `next` links it
  (`facts/next_test.go`, `facts/github_pr_watch_author_test.go`).
