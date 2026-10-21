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
closes when its link is decided. Text output shows the `Categorize:` line,
the pause line when auto-apply is paused, and at most three items.

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
  | `review_requested` | `{"request": {"direct", "teams", "unresolved_team"}}` |
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
  "<unit-id>"}`. Its `pull_request` is empty. The link summary is written by
  a model from captured text, so it appears only as `untrusted.summary` with
  `--include-untrusted`.

- `freshness.as_of` is when the facts behind this item were last read from
  the provider; each source defines which timestamp that is and when it goes
  stale (see `specs/github-pr-watch.md`, Attention Items). Items are also
  stale while their source is in setup `error`, and when
  `freshness.access` is `unreachable`.
- Local paths, tokens, and connector configuration never appear in an item,
  so items can be sent to a cloud agent without leaking the local machine's
  layout.

## Commands

```text
workgraph next [--project <p>] [--reason <r>] [--ids | --json [--include-untrusted]]
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
    alice replied on settings.go:18 — https://github.com/acme/workgraph-demo/pull/42#discussion_r1
    checks failed on 1111111 (lint, unit)
    review requested
Blocked
  acme/api#57 Cache warmup — https://github.com/acme/api/pull/57
    changes requested by bob
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
- Text output omits item ids; `--ids` appends `[<item id>]` to each item
  line. `--json` and `next explain` always carry them.
- Each line is built only from structured fields. Comment text never appears.
  Text output strips ANSI sequences, C0/C1 controls, bidi overrides and isolates,
  and zero-width format characters from provider strings; JSON preserves them.
- A source in setup `error`, or stale, adds a warning line naming the problem
  and the command to fix it, before the items.
- With no enabled sources, `next` says which connectors provide items and how
  to connect them. With sources but no items, it says nothing is waiting and
  shows the freshness line. The `categorize` group follows either case and
  needs no connector.
- `--project` and `--reason` are exact filters.

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
  of remaining pull requests when more exist. `categorize` items are counted
  as remaining decisions instead (see `specs/resume.md`). `resume <project>` shows that project's
  items after `Recent activity`. Showing items does not advance the resume
  check-in beyond the existing rules.
- **`today`**: stays a personal activity view. When items are open,
  including `categorize` items, it ends with `Waiting on you: <n> items
  (workgraph next)`.
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
  `access: "unreachable"`. `unknown` dimensions are shown as unknown.
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
- **N8. MCP returns the same items read-only.** The `next_items` tool returns
  the same ids as `next --json` and has no write behavior.
