# GitHub Pull Request Watch

Status: proposed. No behavior described here is implemented until its facts in
`facts/github_pr_watch_test.go` and `facts/github_test.go` are active.

## Intent

workgraph already captures GitHub pull requests and issues the user is involved
in (`specs/github.md`). That connector stores one mutable snapshot row per PR.
`today` excludes those snapshots because their timestamp is the item's latest
update by anyone (`specs/today.md`), and a snapshot never records that a review
request was cleared. Snapshots therefore cannot answer "what is waiting on me
in GitHub right now?".

This spec adds a read-only watch over two sets of open pull requests:

- **author role**: open PRs the user authored
- **reviewer role**: open PRs requesting the user's review, directly or through
  one of the user's teams, plus open PRs the user has recently reviewed

Each poll observes each complete set, diffs it against the previous local
observation, and appends immutable transition events when reviews, comments,
checks, merge readiness, review requests, or lifecycle state change.

The watch is the first source for `workgraph next` (`specs/next.md`), the
single local list of what the user should do next. The watch defines the
GitHub attention items; `next` renders them and exposes them as a stable,
machine-readable feed that a later dispatch design can hand to agents.

Capture requires no LLM and makes no provider writes. A failed poll changes
nothing locally.

```text
push hint (optional) ─┐
interval ─────────────┴─> poll
  gh api graphql (status query per role, then detail query for changed PRs)
  -> observation -> diff with previous observation
  -> transition events + new observations (one SQLite transaction)
  -> attention items (read time) -> workgraph next / next --watch / MCP
```

## Relationship To Existing Surfaces

### The `github` connector

The watch is a **separate registry entry**, `github.pr_watch`, with event
source `github`. This follows the `slack.lists` and `notion.activity` pattern of
a sub-connector that shares a source with its parent:

| | `github` | `github.pr_watch` |
|---|---|---|
| Scope | participant search (`involves`, `review_requested`) over an updated-time window | the current open author and reviewer sets |
| Query | `gh search prs/issues` | `gh api graphql` |
| Semantics | `bounded_events` | `complete_snapshot` per role |
| Storage | one mutable snapshot row per PR/issue | append-only transition events plus a local observation checkpoint |
| Cursor | `capture_cursors.completed_through` | `github_pr_observations` rows |

Keeping them separate means:

- enabling the watch never silently broadens or resets the existing
  participant scope or its cursor
- the watch has its own polling interval
- either can be disabled independently with `workgraph connectors disable`

### `today` and `resume`

`today` is a personal activity view, and the `resume` morning handoff starts
from the user's own work (`specs/today.md`, `specs/resume.md`). The watch
keeps both rules:

- Only transition events that record the user's own action at its own
  timestamp count as personal activity: `author_replied` with
  `responder: author`, and `review_submitted`. Every other transition event,
  including `observed` and agent replies, is excluded from `today`'s default
  event count, project counts, sessions, and association targets, exactly
  like snapshots. All of them remain inspectable through `workgraph events
  today`.
- `today` gains no attention section. When the watch is enabled and items are
  open, `today` ends with one line: `Waiting on you: <n> items (workgraph
  next)`.
- The `resume` morning handoff's personal work evidence is unchanged. It still
  uses only file and git evidence.
- `resume`'s `Relevant updates since last check-in` shows events with
  explicit involvement whose timestamp or first capture time is after the
  check-in. A watch event qualifies only when both hold: its type is
  `review_comment`, `changes_requested`, `checks_failed`, `merge_conflict`,
  `ready_to_merge`, `review_requested`, or `pushed_since_review`, and its
  `triage.needs_user` is true. `observed` never qualifies, even with
  `needs_user: true`, because its backlog is already in `Waiting on you`.
- The default `resume` overview and `resume <project>` show attention items
  from `specs/next.md`.

### Snapshot enrichment

GitHub search does not return branch or head commit, so `github.pull_request`
snapshot rows captured by `github` lack them, and branch and commit
associations cannot fire. The watch fills that gap:

- When the watch observes a PR whose snapshot row exists, it sets `branch`
  and `commit` (head SHA) in that row's payload with `json_set`.
- Every other payload field, including `matched_scopes`, and the row's
  `timestamp` are left unchanged.
- It never creates a snapshot row. Snapshot existence remains the `github`
  connector's decision.
- The `github` connector's newer-wins upsert (`storeGitHubEvent`) currently
  replaces the whole payload. It changes to keep the existing `branch` and
  `commit` values when the incoming payload has none, so a later participant
  poll does not erase the enrichment.
- When the `github` connector inserts a new PR snapshot row, it copies
  `branch` and `commit` from the latest `github_pr_observations` row for the
  same repository and number, if one exists. Poll order between the two
  connectors therefore does not matter.

## Auth Decision

**The watch reuses the authenticated `gh` CLI. workgraph stores no PAT and
never reads a provider token.**

- The existing `github` connector already authenticates through `gh` and
  validates with `gh auth status`. The watch adds no new credential surface.
- `gh` owns its token in the OS keychain or its own config. workgraph invokes
  `gh api graphql` as a child process and receives only response JSON. It must
  never invoke `gh auth token`, read `gh`'s config files, or forward a token
  through arguments, environment, logs, or `connectors.json`.
- workgraph does not set `GH_TOKEN` or `GITHUB_TOKEN`. If the user's
  environment sets one, `gh` may use it, but workgraph never persists it. The
  opt-in user service passes only `HOME` and `PATH`
  (`specs/capture-reliability.md`), so service-mode capture uses `gh`'s stored
  login.
- Team attribution for the reviewer role needs the `read:org` scope, which
  `gh auth login` grants by default. Without it the watch still works, and
  team requests are labeled `via team (unresolved)`.
- A workgraph-owned OAuth app or stored PAT would add token storage, refresh,
  and revocation work, and would duplicate what `gh` already does. Both are
  rejected.

## Setup

```text
workgraph github prs connect [--params-json '<scope-json>']
workgraph github prs capture
workgraph github prs hint [--pr <url>]
workgraph github prs disconnect
```

`github prs connect`:

1. runs `gh auth status`
2. runs one probe query, `query { viewer { login } rateLimit { remaining resetAt } }`
3. when the reviewer role includes team requests, resolves the user's teams
   (see Team membership); a failure here is a warning, not a setup error
4. records the scope in `connectors.json` `bridge_params`, marks setup
   `ready`, and enables polling
5. prints the controls: `workgraph connectors disable github.pr_watch` and
   `workgraph connectors interval github.pr_watch <duration>`

If step 1 or 2 fails, setup is marked `error` with the `gh` message and
guidance, and nothing is enabled. Connecting `github` does not enable
`github.pr_watch`; the watch is always opt-in.

`github prs disconnect` disables polling and deletes `github_pr_observations`
rows. Captured events are preserved.

`github prs capture` runs one poll immediately through the same function the
daemon uses. `github prs hint` requests an early poll (see Push hints).

### Scope

```json
{
  "scope": "open_prs",
  "identity": "@me",
  "roles": ["author", "reviewer"],
  "review_requests": "direct_and_team",
  "reviewed_window": "336h",
  "ignore_actors": [],
  "ignore_teams": [],
  "comment_body_max_runes": 8000,
  "agent_reply_marker": "",
  "max_open_prs": {"author": 200, "reviewer": 200}
}
```

- `scope` must be `"open_prs"`.
- `identity` is the GitHub search identity, normally `@me`. The probe resolves
  and records the concrete `viewer.login` as non-secret `identity_login`.
- `roles` is a non-empty subset of `author` and `reviewer`.
- `review_requests` is `direct` or `direct_and_team`. `direct` searches
  `user-review-requested:<identity>`; `direct_and_team` searches
  `review-requested:<identity>`, which GitHub matches for the user and for
  teams the user belongs to.
- `reviewed_window` bounds how long an open PR the user has reviewed stays in
  the reviewer set after its last update. It is a positive duration, default
  `336h` (14 days).
- `ignore_actors` is an explicit login list whose comments and reviews never
  produce events or attention items. Bots are **not** ignored by default.
  Every event records `actor_kind` so consumers can treat bots differently.
- `ignore_teams` is a list of `org/team-slug` values. A request through an
  ignored team counts as absent.
- `comment_body_max_runes` caps each stored comment or review body, in the
  range `0..65536`. `65536` is GitHub's own comment limit. `0` stores no
  comment text; events then carry only structure and links.
- `agent_reply_marker` is an exact attribution line that agents acting as the
  user append to their comments. A comment by `identity_login` whose last
  non-empty line equals the marker is attributed to `agent`; any other comment
  by `identity_login` is attributed to `author`. Empty disables the
  distinction. The marker means "some agent acting as the user", not a
  specific agent or run; per-run attribution belongs to the dispatch design.
- `max_open_prs` is the completeness ceiling for each role, so a busy team
  queue cannot fail the author role.

Changing approved scope resets the observation baseline (see State Diff rule
1).

## Query Shape

Each poll uses one **status query** per enabled role for the whole open set,
and a **detail query** only for PRs that changed. Nesting comment connections
under every thread of every PR in one document multiplies GitHub's calculated
cost: 50 PRs × 100 threads alone is about 5,000 connection requests, roughly
50 points per poll. The split keeps a typical poll at a few points. These are
estimates; each response's measured `rateLimit.cost` is recorded in connector
state.

### Status query (every poll, per role)

```graphql
query($q: String!, $login: String!, $after: String) {
  viewer { login }
  rateLimit { cost remaining resetAt }
  search(type: ISSUE, query: $q, first: 50, after: $after) {
    issueCount
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on PullRequest {
        id number url title isDraft state createdAt updatedAt
        author { __typename login }
        repository { nameWithOwner }
        headRepository { nameWithOwner }
        isCrossRepository
        headRefName headRefOid baseRefName
        reviewDecision mergeable mergeStateStatus
        reviewRequests(first: 50) {
          totalCount
          nodes { requestedReviewer {
            __typename
            ... on User { login }
            ... on Team { slug organization { login } }
          } }
        }
        viewerLatestReview: reviews(author: $login, last: 1,
            states: [APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED]) {
          nodes { id state submittedAt commit { oid } }
        }
        commits(last: 1) {
          nodes { commit {
            oid
            statusCheckRollup {
              state
              contexts(first: 100) {
                totalCount
                nodes {
                  __typename
                  ... on CheckRun { id name status conclusion completedAt detailsUrl }
                  ... on StatusContext { context state createdAt targetUrl }
                }
              }
            }
          } }
        }
      }
    }
  }
}
```

`$q` by role:

| Role | Search |
|---|---|
| author | `is:pr is:open author:<identity> archived:false sort:updated-desc` |
| reviewer, requested | `is:pr is:open review-requested:<identity> archived:false sort:updated-desc` (`user-review-requested:` for `direct`) |
| reviewer, reviewed | `is:pr is:open reviewed-by:<identity> -author:<identity> archived:false updated:>=<now - reviewed_window> sort:updated-desc` |

- `viewerLatestReview` filters out `PENDING` reviews, which are the user's
  own unsubmitted drafts. A returned review with a null `submittedAt` is
  treated as absent.
- The two reviewer searches are merged by node id into one reviewer set.
- Check status changes do not bump `updatedAt`, so the rollup is read here on
  every poll. For the reviewer role, check state is recorded but opens no
  attention item.

### Team membership

For `direct_and_team`, the watch resolves which teams the user belongs to:

```graphql
query($login: String!, $after: String) {
  viewer { organizations(first: 100, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes { login teams(first: 100, userLogins: [$login]) {
      totalCount nodes { slug }
    } }
  } }
}
```

The result is cached in connector state and refreshed at connect and at most
hourly. It is used only to label a request: a request whose `requestedReviewer`
is a team in the cache is `via <org>/<slug>`. A PR returned by the search whose
requests match no cached team is labeled `via team (unresolved)` rather than
dropped, because GitHub's search already decided it matches.

### Detail query (changed PRs only)

Each changed PR gets its own alias, so each has its own watermark and cursor:

```graphql
query($id0: ID!, $since0: DateTime, $after0: String, ...) {
  rateLimit { cost remaining resetAt }
  pr0: node(id: $id0) {
    ... on PullRequest {
      id
      latestOpinionatedReviews(first: 20) {
        totalCount
        nodes { id state author { __typename login } }
      }
      timelineItems(since: $since0, after: $after0, first: 100,
                    itemTypes: [ISSUE_COMMENT, PULL_REQUEST_REVIEW]) {
        pageInfo { hasNextPage endCursor }
        nodes {
          __typename
          ... on IssueComment {
            id body createdAt url authorAssociation author { __typename login }
          }
          ... on PullRequestReview {
            id state body submittedAt url authorAssociation commit { oid }
            author { __typename login }
          }
        }
      }
      reviewThreads(first: 100) {
        totalCount
        nodes {
          id isResolved isOutdated path line originalLine resolvedBy { login }
          comments(last: 20) {
            totalCount
            nodes {
              id body createdAt url diffHunk replyTo { id } authorAssociation
              author { __typename login }
            }
          }
        }
      }
    }
  }
  pr1: node(id: $id1) { ... }
}
```

- The detail query runs for an author-role PR when:
  - it has no previous observation
  - its `updatedAt` differs from the previous observation
  - its last complete detail fetch is older than 30 minutes (reconciliation
    for changes that may not bump `updatedAt`, such as thread resolution)
  - a hint forces it (see Push hints)
- It runs for a reviewer-role PR under the same conditions, but only once the
  user has reviewed it or commented on it. A PR that only requests the user's
  review needs no detail, which keeps team queues cheap.
- At most 10 PRs are aliased per document.
- **Timeline watermark.** `$sinceN` is the PR's `timeline_since`: the start
  time of its last complete detail fetch minus 10 minutes of clock-skew
  overlap. It is null on first sight. Overlap is harmless because the diff
  deduplicates by provider id.
- **Timeline pagination.** When `hasNextPage` is true, the PR is re-queried
  with `$afterN` until the page set is complete, up to 10 pages per poll.
  Only a complete fetch advances `timeline_since`.
- **Timeline catch-up.** A PR that exceeds 10 pages in one poll commits the
  events from the pages it read and stores the last `endCursor` as
  `timeline_cursor` in its observation. The next poll resumes from
  `timeline_cursor` instead of starting again from `timeline_since`, so a
  backlog of any size completes over successive polls without an external
  change. While catching up, the PR's comment and review dimensions are
  `unknown` and connector status reports `detail catching up: <repo>#<n>`.
  When a fetch reaches `hasNextPage = false`, `timeline_cursor` is cleared and
  `timeline_since` advances. Comments are therefore never skipped by
  advancing past them.
- **Thread windows.** A thread's `comments(last: 20)` window is complete when
  it contains the thread's first comment or a comment already known (stored
  as an event, or created at or before `baseline_at`). When it is not, the
  watch pages backward through that thread with a per-thread query
  (`node(id: <thread_id>) { ... on PullRequestReviewThread { comments(last:
  20, before: $cursor) { ... } } }`), up to 5 pages, until the window is
  complete. A thread that is still incomplete after 5 pages emits the
  comments it read, marks the earliest with `triage.gap: true`, and is
  `unknown` for that poll rather than losing participation.
- `latestOpinionatedReviews.totalCount > 20` makes the approval dimension
  `unknown`, so an unseen `CHANGES_REQUESTED` review cannot produce false
  readiness.
- PRs that leave a role's set are resolved by a follow-up
  `nodes(ids: [...])` query requesting `state merged mergedAt closedAt
  mergedBy { login } repository { isArchived } updatedAt`, plus
  `reviewRequests` and the viewer's latest review for reviewer-role PRs.
- Query documents are passed with `gh api graphql --input <file>`, never as
  argument strings. The documents are constants in the binary; facts assert
  that no executed document contains `mutation`.

### Conditional requests

GitHub GraphQL is `POST` and does not support `ETag` or `If-None-Match`.
Cost is controlled by the status/detail split, the polling interval, and push
hints. `gh api --cache` is never used, because duration-based caching would
hide transitions.

## Observation Model

Each poll produces one normalized `PullRequestObservation` per PR and role.
Only the diff engine creates events.

```text
role                   author | reviewer
repository, number, node_id, url, title, author_login, is_draft, state,
created_at, updated_at, head_ref, head_sha, base_ref, head_repository,
is_cross_repository, baseline_at
review_decision        APPROVED | CHANGES_REQUESTED | REVIEW_REQUIRED | none
opinionated_reviews    [{reviewer, actor_kind, state}]  | unknown
checks_state           passed | failed | pending | none | unknown
check_runs             [{id, name, status, conclusion, completed_at,
                         details_url}]
threads                [{thread_id, path, line, is_resolved, resolved_by,
                         is_outdated, depth, last_author, last_responder,
                         agent_replies, user_participates, awaiting,
                         window_complete}]
conversation           {last_human_comment_at, last_user_comment_at}
mergeable              MERGEABLE | CONFLICTING | UNKNOWN
merge_state_status     raw value
ready                  true | false | unknown
review_request         {direct, teams, unresolved_team} | none   (reviewer)
viewer_review          {id, state, submitted_at, commit} | none  (reviewer)
timeline_since, timeline_cursor, detail_fetched_at, observed_at,
verified_at, missing_polls, access   ok | unreachable
```

Mappings:

- `checks_state`:
  - `pending` while any context is not terminal (a CheckRun whose `status`
    is not `COMPLETED`, or a StatusContext in `PENDING` or `EXPECTED`),
    whatever the rollup state says
  - otherwise rollup `SUCCESS` maps to `passed` and `FAILURE` or `ERROR` to
    `failed`
  - a null rollup is `none`, and more than 100 contexts is `unknown`
- `responder` of a comment: `other` when not by `identity_login`, otherwise
  `author` or `agent` by `agent_reply_marker`.
- `user_participates` is true when any comment read in the thread is by
  `identity_login`, or when P already had it true for that thread. Once true
  it stays true, so a user comment scrolling out of the 20-comment window
  never drops the thread.
- `verified_at` is the time of the last poll that read this PR's status from
  a complete search or the follow-up query. `access` is `unreachable` while
  the follow-up returns null or `FORBIDDEN`.
- `awaiting` on an unresolved thread is `user` when the last comment's
  responder is `other` and, in the reviewer role, `user_participates` is
  true. Otherwise it is `other`. Resolved threads have no `awaiting`.
- `last_responder` is the responder of the thread's last comment. `depth` is
  the thread's comment `totalCount`. `agent_replies` counts comments
  attributed to `agent` among those returned.
- `baseline_at` is the `observed_at` of the poll that first saw the PR in the
  role. It survives observation resets because it is also stored on the
  `observed` event.
- More than 100 threads makes thread-derived values `unknown`.

Observations live in a connector-owned checkpoint table:

```text
github_pr_observations(
  connector_id, node_id, role, repository, number,
  observation_json, observed_at,
  PRIMARY KEY (connector_id, node_id, role))
```

This is provider checkpoint state, not a source of truth. Deleting it causes a
re-bootstrap that neither duplicates nor loses events (see rule 1).

## Event Schema

Events use source `github` and type prefix `github.pull_request.`.

| Type | Role | Emitted when | Discriminator | `actor` | `timestamp` |
|---|---|---|---|---|---|
| `observed` | both | a PR is first seen by the watch in a role | `<role>` | PR author | `observed_at` |
| `reopened` | author | a PR with a prior `closed` event re-enters the open set | `<closed_at>` | empty | `observed_at` |
| `review_comment` | both | a new comment id by anyone other than the user appears in a thread (reviewer role: only threads the user participates in), in the conversation (author role), or as a non-empty `COMMENTED` review body (author role) | `<comment_or_review_id>` | comment author | `createdAt` / `submittedAt` |
| `author_replied` | both | a new comment id by `identity_login` appears in a thread or the conversation | `<comment_id>` | `identity_login` | `createdAt` |
| `changes_requested` | author | a new review with state `CHANGES_REQUESTED` | `<review_id>` | reviewer | `submittedAt` |
| `approved` | author | a new review with state `APPROVED` | `<review_id>` | reviewer | `submittedAt` |
| `checks_failed` | author | `checks_state` becomes `failed` for a run set not yet reported on the head | `<head_sha>:<run_fingerprint>` | empty | latest context completion |
| `checks_passed` | author | `checks_state` becomes `passed` for a run set not yet reported on the head | `<head_sha>:<run_fingerprint>` | empty | latest context completion |
| `ready_to_merge` | author | `ready` goes from false to true | `<head_sha>:<observed_at>` | empty | `observed_at` |
| `readiness_lost` | author | `ready` goes from true to false | `<head_sha>:<observed_at>` | empty | `observed_at` |
| `merge_conflict` | author | `mergeable` becomes `CONFLICTING` from a known non-conflicting value | `<head_sha>:<observed_at>` | empty | `observed_at` |
| `review_requested` | reviewer | a request for the user (direct or through a team) appears that P did not have | `<observed_at>` | PR author | `observed_at` |
| `review_request_removed` | reviewer | the request for the user disappears while the PR stays open | `<observed_at>` | empty | `observed_at` |
| `review_submitted` | reviewer | a new review by `identity_login` | `<review_id>` | `identity_login` | `submittedAt` |
| `pushed_since_review` | reviewer | the head changes after the user's latest review | `<head_sha>` | PR author | `observed_at` |
| `merged` | both | the PR left the open set and the follow-up shows merged | `<merged_at>` | `mergedBy.login` | `mergedAt` |
| `closed` | both | the PR left the open set and the follow-up shows closed and not merged | `<closed_at>` | empty | `closedAt` |

The event id is
`github.pull_request.<suffix>:<node_id>[:<discriminator>]`. The PR's GraphQL
node id is stable across repository renames and transfers, so a rename
cannot re-emit captured occurrences. A PR is never in both roles, because
GitHub does not request a review from the PR's author, so `merged` and
`closed` ids cannot collide across roles. Rows are inserted with `INSERT ...
ON CONFLICT(id) DO NOTHING`. Transition events are immutable.

Other columns:

- `project`: `inferGitHubProject` (local remote, then repository name)
- `involvement_json`: author role `["authored"]`, which the query scope
  proves. Reviewer role `["review_requested"]` while a request for the user is
  open, otherwise `["thread_participation"]`.
- `summary`: a fixed template built only from structured fields, for example
  `PR #42 checks failed on 1111111 (lint, unit)`,
  `PR #42 review comment from alice on settings.go:18`, or
  `PR #77 review requested via acme/platform`. It never includes comment text.

### Payload

```json
{
  "role": "author",
  "repository": "example-org/workgraph-demo",
  "number": 42,
  "node_id": "PR_kwDO...",
  "url": "https://github.com/example-org/workgraph-demo/pull/42",
  "title": "Add settings helper",
  "branch": "feature/settings-helper",
  "base_ref": "main",
  "head_sha": "1111111111111111111111111111111111111111",
  "head_repository": "example-org/workgraph-demo",
  "is_cross_repository": false,
  "actor_kind": "user",
  "actor_association": "MEMBER",
  "state": {
    "review_decision": "REVIEW_REQUIRED",
    "checks_state": "failed",
    "unresolved_threads": 2,
    "threads_awaiting_user": 1,
    "mergeable": "MERGEABLE",
    "is_draft": false,
    "ready": false
  },
  "triage": {
    "category": "review_feedback",
    "needs_user": true,
    "thread_id": "PRRT_kwDO...",
    "path": "settings.go",
    "line": 18,
    "is_outdated": false,
    "reply_to": null,
    "thread_depth": 1,
    "previous_responder": null,
    "agent_replies_in_thread": 0,
    "has_suggestion": true,
    "failed_checks": []
  },
  "observed_at": "2026-10-16T17:02:11Z",
  "untrusted": {
    "trust": "untrusted",
    "origin": "github.review_thread_comment",
    "author": "alice",
    "comment_url": "https://github.com/example-org/workgraph-demo/pull/42#discussion_r1",
    "body": "Could you rename this?\n\n```suggestion\nfunc loadSettings() error {\n```",
    "body_truncated": false,
    "body_runes": 71,
    "body_sha256": "...",
    "diff_hunk": "@@ -12,7 +12,7 @@ ..."
  }
}
```

- `actor_kind` is `user`, `bot` (`__typename` `Bot` or a login ending in
  `[bot]`), or `team`.
- `actor_association` is GitHub's `authorAssociation` for comments and
  reviews. It is recorded as provider data. It means the author's relation to
  the repository's owner (for example `MEMBER` is organization membership),
  not repository write access. It is absent on events without a comment or
  review.
- `triage.category` is `review_feedback`, `ci`, `merge`, `review_request`,
  `user_reply`, or `lifecycle`.
- `triage.needs_user` is true for:
  - `review_comment` events whose thread or conversation is awaiting the user
  - `changes_requested`, `checks_failed`, `merge_conflict`, and
    `ready_to_merge`
  - `review_requested`, and `pushed_since_review` when the user's latest
    review is `CHANGES_REQUESTED` or `COMMENTED`
  - `observed` events whose state opens at least one attention item

  It is false otherwise. It is a deterministic hint, not a priority judgment.
  Surfaces that select on it also name the event types they accept; for
  example `resume` never shows `observed` events (see Relationship To
  Existing Surfaces).
- `triage.previous_responder` is the responder (`other`, `author`, or
  `agent`) of the comment immediately before this one in the same thread, or
  `null` for the first comment. New comments are processed in chronological
  order, so several comments arriving between polls each get the correct
  predecessor.
- `triage.has_suggestion` is true when the normalized body, before
  truncation, contains a line that is exactly a ```` ```suggestion ```` fence
  opener.
- `triage.failed_checks` (on `checks_failed`) lists up to 20
  `{name, conclusion, details_url}` entries, sorted by name.
- `triage.request` (on `review_requested` and reviewer `observed`) is
  `{direct, teams, unresolved_team}`. `triage.reason` (on
  `review_request_removed`) is `reviewed` when `viewer_review` changed since
  P, otherwise `withdrawn`.
- On `author_replied`, `triage.responder` is `author` or `agent`, and the
  payload has no `untrusted` body. The user's own text is not copied.
- `observed` events carry the full current `state`, `baseline_at`, and,
  under `untrusted`, a `threads` array of up to 20 open threads awaiting the
  user, each with path, line, URL, and last-comment body.
- When a thread's window is still incomplete after backward paging (see
  Thread windows), the earliest emitted comment has `triage.gap: true`.

## State Diff And Dedupe Rules

The diff compares the previous observation P with the current observation C
for the same `node_id` and role.

1. **First sight and reset.**
   - A PR with no P and no stored `observed` event for the role emits exactly
     one `observed` event carrying its full current state and `baseline_at`.
     It emits no per-comment, per-review, or check events. Comments and
     reviews created at or before `baseline_at` are baseline.
   - A reviewer-role PR first seen after the role's first successful poll
     also emits `review_requested` when it has an open request for the user,
     because a new request is a real transition, not backlog.
   - A PR with no P but a stored `observed` event for the role (an
     observation reset, scope change, or reconnect) does not re-baseline. It
     reuses the stored `baseline_at` and emits events for comments and
     reviews created after it that are not already stored. Nothing that
     happened while observations were missing is lost, and nothing is
     duplicated.
   - When an author-role PR has a prior `closed` event, `reopened` is also
     emitted.
2. **Unchanged.** If C equals P in every dimension, no event is emitted and
   only `observed_at` is refreshed.
3. **Unknown never transitions.** A dimension whose C value is `unknown`, or
   `mergeable: UNKNOWN`, is carried forward from P, emits nothing, and is
   reported as unknown, not as current, in attention items.
4. **Checks (author role).**
   - `pending` emits nothing. A failure is reported once the head's whole run
     set is terminal, not at the first failing job.
   - `run_fingerprint` is the first 12 hex characters of SHA-256 over the
     sorted `id@completedAt` values of the terminal CheckRuns and
     `context@createdAt` values of the terminal StatusContexts. Including
     `completedAt` covers apps that reset and re-run an existing check run
     under the same id.
   - A head emits a checks event when it reaches a terminal state whose
     fingerprint has not been reported for that head. Observing the same runs
     again cannot emit twice, and returning to an earlier SHA does not
     duplicate.
   - A re-run creates new run ids or a new `completedAt`, so a re-run that
     finishes failed emits a new `checks_failed`. Jobs created progressively
     by `needs:` dependencies are covered by waiting for the whole set to be
     terminal.
   - A new head starts at whatever C reports, normally `pending`.
5. **Comments and reviews.** Each comment or review id that is neither stored
   as an event nor baseline emits one event, oldest first, unless its author
   is in `ignore_actors`. Comments by `identity_login` emit `author_replied`
   instead of `review_comment` and turn the thread's `awaiting` to `other`.
   Reviews by `identity_login` in the reviewer role emit `review_submitted`.
   Edits keep their id and emit nothing.
6. **Readiness (author role).** `ready` is true only when every condition
   holds:
   - `state = OPEN` and `is_draft = false`
   - approval: `review_decision = APPROVED`, or `review_decision` is none,
     at least one `APPROVED` review exists, and no reviewer's latest
     opinionated review is `CHANGES_REQUESTED`
   - `checks_state` is `passed` or `none`
   - zero unresolved, non-outdated threads, which must be known
   - `mergeable = MERGEABLE`
   - `merge_state_status` is `CLEAN` or `HAS_HOOKS`. `BLOCKED` (branch
     protection), `BEHIND`, `UNSTABLE`, `DIRTY`, and `DRAFT` are not ready;
     `UNKNOWN` makes `ready` unknown.

   `ready` false to true emits `ready_to_merge`. True to false emits
   `readiness_lost`, with `triage.reasons` listing each failed condition
   (`draft`, `approval`, `checks`, `threads`, `conflicts`, `merge_state`), and
   `triage.merge_state_status` carrying the raw value. Unknown does not
   transition. The discriminator includes the observing poll's
   `observed_at`, so readiness that is lost and regained on the same SHA
   emits `ready_to_merge` again, while repeated polls of one state never
   duplicate.
7. **Review requests (reviewer role).**
   - A request for the user appearing in C but not P emits
     `review_requested`; disappearing while the PR stays open emits
     `review_request_removed`. A re-request after the user reviewed is a new
     `review_requested`.
   - `head_sha` changing after `viewer_review.commit` emits
     `pushed_since_review` once per head.
   - Requests through `ignore_teams` count as absent. A PR whose only
     requests are through ignored teams, and which the user has not reviewed,
     is excluded from the reviewer set.
8. **Leaving a role's set.** A PR in P but not in C is resolved through the
   follow-up query. Each outcome records an `exit` on the observation before
   it is deleted, so `next` can report why items closed:
   - Merged: emit `merged` and delete the observation (`exit: merged`).
   - Closed and not merged: emit `closed` and delete the observation
     (`exit: closed`).
   - Still `OPEN` in an archived repository: delete the observation silently
     (`exit: out_of_scope`).
   - Reviewer role, still `OPEN`, and the follow-up confirms no request for
     the user (or only requests through `ignore_teams`):
     - when the user has not reviewed it, or last reviewed it and the PR was
       last updated before `reviewed_window`: emit `review_request_removed`
       if P had a request, then delete the observation (`exit: out_of_scope`)
     - otherwise the reviewed search is lagging; treat it as search lag below
   - Still `OPEN` and the follow-up shows it still matches the role's search
     (search lag): keep P, increment `missing_polls`, and emit nothing. After
     6 consecutive missing polls, delete the observation (`exit: left_search`)
     and report `left search: <repo>#<n>` in connector status.
   - Null or `FORBIDDEN` (lost access): keep P with `access: unreachable`,
     emit nothing, and report `unreachable: <repo>#<n>` in connector status.
     Its items stay listed but are marked unreachable and stale (see
     Attention Items). After 6 consecutive unreachable polls the observation
     is deleted (`exit: unreachable`).
9. **Missed intermediate states.** Polling observes state, not history.
   Check and readiness states that come and go between polls are not seen.
   Comments and reviews are not lost this way, because they are read by id
   from a complete timeline or a thread window marked with `gap`.
10. **Atomicity and concurrency.**
    - A poll commits in one transaction: the events, snapshot enrichment, and
      updated observations of every role whose set was complete, plus the
      connector's `poll_seq` increment. A role whose search failed
      contributes nothing and keeps its previous observations (see
      Completeness). A database or diff error rolls back the whole
      transaction.
    - The transaction first checks that `poll_seq` still equals the value
      read when the poll started. If it changed, the poll discards its work
      and retries once from the current observations.
    - Every operation that changes what a poll would write increments
      `poll_seq` in the same transaction as its own change: another poll's
      commit, `github prs disconnect`, a scope or role change, and deleting
      observations. A poll that was already running when the user
      disconnected therefore cannot restore deleted observations, and after
      a disconnect its retry finds the connector disabled and stops.

## Attention Items

The watch defines the GitHub attention items for `workgraph next`
(`specs/next.md`). Items are a **read-time projection** over the latest
observations and their events. They store nothing and make no GitHub request.

| Reason | Role | Open while | Opening event |
|---|---|---|---|
| `review_requested` | reviewer | a request for the user is open | `review_requested` |
| `rereview` | reviewer | the user's latest review is `CHANGES_REQUESTED` or `COMMENTED` and the head changed since | `pushed_since_review` |
| `checks_failed` | author | `checks_state = failed` on the current head | `checks_failed` |
| `changes_requested` | author | any reviewer's latest opinionated review is `CHANGES_REQUESTED` | `changes_requested` |
| `reply` | both | an unresolved, non-outdated thread is `awaiting: user` | the `review_comment` for the thread's last comment |
| `reply` | author | a human (`actor_kind: user`) conversation comment is newer than the user's last conversation comment | that comment's `review_comment` |
| `conflicts` | author | `mergeable = CONFLICTING` | `merge_conflict` |
| `ready_to_merge` | author | `ready = true` | `ready_to_merge` |

**Opening evidence.** An item's `opened_by` is its opening event when one is
stored. A condition that already held at baseline has no opening event, so
`opened_by` is the PR's `observed` event for that role, and `since` is that
event's `baseline_at`. Both are stored events, so `opened_by`, `since`, and
the item id survive observation resets.

- Conversation `reply` items come only from issue comments. Review bodies do
  not open them; a `CHANGES_REQUESTED` review opens `changes_requested`
  instead.
- Review threads from bots count; conversation comments from bots, such as
  coverage reports, do not.
- A thread an agent answered with the marker is `awaiting: other`, so it is
  not open. The item keeps `last_responder` so agent answers stay visible
  while the thread is open.
- Draft PRs are included and marked `draft`.
- Dimensions that are `unknown` are shown as `unknown` with the reason, never
  as their carried-forward value.
- **Per-PR freshness.** An item's `freshness.as_of` is the PR's
  `verified_at`, not the connector's last success. It is stale when
  `verified_at` is older than three intervals or the PR's `access` is
  `unreachable`, even while other PRs poll fine.
- An item closes when its condition stops holding (`resolved`), when the
  user marks it done or snoozes it, or when its PR's observation is deleted.
  A deleted observation closes the item with the observation's `exit`:
  `merged`, `closed`, `out_of_scope`, `left_search`, or `unreachable`. Only
  `resolved` claims the condition was addressed.

Item ids are deterministic so a consumer can track an item across polls:

| Reason | Item id |
|---|---|
| `review_requested` | `github:review_requested:<node_id>:<opened_by>` |
| `rereview` | `github:rereview:<node_id>:<head_sha>` |
| `checks_failed` | `github:checks_failed:<node_id>:<head_sha>:<run_fingerprint>` |
| `changes_requested` | `github:changes_requested:<node_id>:<review_id>` |
| `reply` (thread) | `github:reply:<node_id>:<thread_id>:<last_comment_id>` |
| `reply` (conversation) | `github:reply:<node_id>:conversation:<comment_id>` |
| `conflicts` | `github:conflicts:<node_id>:<opened_by>` |
| `ready_to_merge` | `github:ready_to_merge:<node_id>:<opened_by>` |

A new comment in a thread produces a new item id, so marking an item done
never hides later feedback.

## Polling, Push Hints, And Backoff

### Cadence

- The default interval is `5m`. `workgraph connectors interval
  github.pr_watch 15m` lengthens it. The watch rejects intervals shorter
  than `1m`.
- The daemon polls once at startup. After the machine sleeps, the next poll
  runs within one interval of waking, and the timeline watermark and
  follow-up query catch up on everything that happened meanwhile.
- `workgraph github prs capture` polls immediately.

### Push hints

Polling is the source of truth. A push hint only shortens the wait for the
next poll, so the time from a GitHub change to an attention item drops from
up to one interval to seconds.

- `workgraph github prs hint [--pr <url>]` records a hint in SQLite
  (`connector_poll_hints`: connector id, received at, optional PR URL). A hint
  carries no state. `--pr` is only a selector: it is matched exactly against
  the `url` of an existing observation and otherwise ignored.
- The daemon checks for hints every 2 seconds. When at least one is pending
  and the last poll started more than 10 seconds ago, it polls now and clears
  the hints received before that poll started. Hints arriving during a poll
  trigger at most one follow-up poll.
- A hint-triggered poll is an ordinary full poll, so completeness and the
  diff rules are unchanged, with one addition: it forces the detail query for
  every PR whose `--pr` matched, and for every PR whose last detail fetch is
  older than 2 minutes when no hint named a PR. Changes that do not bump
  `updatedAt`, such as thread resolution, therefore reach `next` within
  seconds of a hint that names the PR, and within a few minutes of an
  unnamed one.
- A hint never overrides the connector's not-before deadline (see Runtime
  extension).
- Hints are local only. A webhook receiver, `gh webhook forward`, a company
  event bus, or a cloud relay can call `workgraph github prs hint`. Receiving
  provider webhooks is out of scope here because it needs a reachable endpoint
  and webhook secrets; the hint command is the integration point.

### Runtime extension

The current poller (`runConnectorPoller` in `run.go`) treats every poll as
success or failure, resets failures on success, and always waits either the
interval or the failure backoff. The watch needs a small extension:

- The connector state gains one **not-before** deadline. No poll for the
  connector starts before it, whether triggered by the interval, a hint, or
  `github prs capture`. `capture` prints `rate limited until <time>` and
  exits non-zero without querying.
- A poll may return a **deferred** outcome with a reason and a not-before
  time. A deferral records no failure, leaves `consecutive_failures`
  unchanged, sets the not-before deadline, and shows the reason in
  `connectors status`.
- A failed poll may carry a **retry-after** duration. It sets the not-before
  deadline to now plus that duration, and the next attempt waits for the
  larger of the backoff and the deadline.
- Provider deadlines are honored as given. Only ordinary scheduling, the
  interval and the failure backoff, is capped.
- A poller may be **woken early** by a pending hint, subject to the 10-second
  minimum spacing and the not-before deadline.
- Connectors that use none of these behave exactly as today.

### Rate limits

- The watch reads the GraphQL bucket from each response's `rateLimit` and
  stores the last value in connector state.
- If the stored `remaining < 200` and its `resetAt` is in the future, the
  poll is **deferred** until `resetAt`: no query, no events, and observations
  untouched. Status shows `rate limited until <resetAt>`.
- HTTP 200 with `errors[].type = RATE_LIMITED`, `HTTP 403` or `429`,
  `API rate limit exceeded`, `secondary rate limit`, or a `Retry-After`
  header is a failure with retry-after. Observations are untouched.

### Failure modes

| Failure | Detection | Behavior |
|---|---|---|
| Rate limited | see above | defer or back off; nothing changes |
| Auth expired or revoked | `gh` exit with `HTTP 401`, `Bad credentials`, or `gh auth login` guidance | setup `error`, stop this poller, guidance `gh auth login` then `workgraph connectors validate github.pr_watch` |
| SSO not authorized | GraphQL `FORBIDDEN` / SAML | poll fails visibly with SSO guidance |
| Offline or DNS failure | `gh` network error | transient backoff (5s to 5m) |
| `gh` missing | exec error | setup `error` |
| Malformed JSON or schema drift | decode failure | poll fails; nothing changes |
| Completeness violated | see Completeness | that role fails; nothing changes for it |
| Team membership unreadable | missing `read:org` or `FORBIDDEN` on the teams query | warning in status; team requests labeled `via team (unresolved)` |
| Whole-poll deadline (30s) | context deadline | transient |
| Repository renamed or transferred | `nameWithOwner` changes under the same `node_id` | observation and event ids follow `node_id`; payloads use the new name |

## Completeness

The watch uses `complete_snapshot` semantics per role, so each role's open
set must be provably complete:

- each search must page to `hasNextPage = false`
- `issueCount` must equal the number of nodes received
- `issueCount > max_open_prs[role]` fails that role visibly
- GraphQL `errors` fail the role, including SAML/SSO `FORBIDDEN` on some
  repositories, because dropping those PRs would look like closes. The
  exception is `RATE_LIMITED`, which is handled as a rate limit.

When one role fails and the other is complete, the complete role commits and
the poll is reported as failed. Nested connections that exceed their page
mark only that dimension `unknown`.

## Existing Connector Fixes (Slice 0)

These are defects in the shipped `github` connector. Each has its own fact in
`facts/github_test.go`.

1. **Rate-limit pre-check masks failures.** `githubRateLimitAllowsPolling`
   returns false on any `gh api rate_limit` error, and the caller records
   that as a successful empty poll (`github.go`). An expired login or a
   missing `gh` therefore captures nothing, silently and forever.
   - Fix: a pre-check command failure fails the poll and passes the error to
     auth classification. Only a successfully read low budget skips the poll,
     reported as a deferral once the runtime extension exists.
2. **`gh` auth errors are not classified.** `connectorAuthFailure`
   (`connector.go`) matches `status 401`, `unauthenticated`, and `invalid
   credentials`, but not `gh`'s `HTTP 401: Bad credentials` or `gh auth
   login` guidance.
   - Fix: add those patterns, so GitHub connectors move to setup `error`
     instead of retrying indefinitely.
3. **Wrong rate-limit bucket.** `gh search` consumes the REST `search` bucket
   (30 requests per minute), but the pre-check reads `core` and requires
   `remaining >= 100`.
   - Fix: read `resources.search`, and require `remaining` to cover the
     number of searches the poll will issue (one per configured `include`
     value and kind, plus the `always_repositories` searches) plus 2 for
     window bisection. The `core` threshold of 100 can never pass for a
     30-request bucket, so the threshold must change with the bucket.
4. **Bridged and direct GitHub identities diverge.** Direct capture writes
   `github.pull_request:<repo>:<n>` and refreshes it when newer. Bridged
   ingest hashes `external_id` `<repo>#<kind>:<n>:<updated-at>`
   (`bridged_capture.go`), which creates a new row for every update. The
   bridged reference payload also uses `repo` and `head` where direct uses
   `repository` and `branch`.
   - Effect: `today` hides snapshots, but `events today`, `resume`'s
     `Open GitHub work`, and associations see one duplicate PR row per
     revision, mode switches double-count, and association extraction misses
     the repository.
   - Fix: bridged `github.pull_request` and `github.issue` ingest normalizes
     onto the direct snapshot id and payload keys with the same newer-wins
     upsert. Existing duplicate rows are left in place.

## events today

- `workgraph events today --type github.pull_request.*` lists only transition
  events. `--type` gains a trailing `.*` wildcard meaning "the type starts with
  this prefix and the next character is a dot". Exact matching is unchanged.
- Transition events render with templated labels, for example
  `Add settings helper (#42): checks failed on 1111111 (lint)`.
- The detailed row for a `review_comment` shows the first line of the body
  behind an explicit marker, `untrusted comment (alice, bot): "…"`.

## Associations

Associations follow `specs/event-associations.md`: deterministic, local, no
fuzzy matching, and no time-only matches.

- **Only `observed` events and `github.pull_request` snapshot rows** are
  related candidates from this watch. Every transition event carries the
  same repository, URL, and head SHA, so treating each as a candidate would
  attach one Slack link or commit to dozens of events per PR. `associations
  explain` on a transition event explains its PR's `observed` event.
- `author_replied` with `responder: author` and `review_submitted` are
  personal activity, so they may be association **targets**. Their related
  candidates are still only `observed` events and snapshot rows.
- Signals available on those rows without new rules:
  - **Commit**: `head_sha` matches captured `git.commit` events. Snapshot
    enrichment gives snapshot rows the same signal.
  - **Branch**: `branch` plus exact `repository`.
  - **URL**: a PR URL in a Slack message matches the PR `url`.
- Signal extraction skips the `untrusted` subtree. A comment cannot create
  association evidence by planting a URL.

## Security Notes

- **Read-only.** Only constant GraphQL `query` documents are executed. The
  watch never re-runs CI, comments, resolves, approves, merges, requests
  reviews, or pushes.
- **No token persistence.** See Auth Decision. Facts verify that a token
  sentinel never appears under the workgraph home or in the database, and that
  `gh auth token` is never invoked.
- **Hints carry no state.** A hint only schedules a poll. Its optional PR URL
  is used only as an exact-match selector against existing observations, is
  logged after the same control-character stripping as comment text, and is
  never stored as event data.
- **Third-party text is stored only under `payload.untrusted`**, with
  `trust: "untrusted"` and `origin`. This covers comment bodies, review bodies,
  and diff hunks.
  - Bodies are markdown, kept so that suggestion blocks and code survive.
  - Before storage, C0 and C1 control characters other than newline and tab,
    ANSI escape sequences, bidi override and isolate characters
    (U+202A–U+202E, U+2066–U+2069), and zero-width characters are removed.
    Whitespace and line structure are preserved.
  - Each body is truncated to `comment_body_max_runes`, with
    `body_truncated`, `body_runes`, and `body_sha256` of the full normalized
    text. Diff hunks are capped at 4,000 runes.
  - Untrusted text never appears in `summary`, compact labels, attention item
    titles, project inference, association signals, or logs.
- **All provider strings are untrusted for agents.** Titles, branches, URLs,
  logins, team slugs, and paths are provider-controlled too. MCP and LLM
  consumers treat every payload string as data.
- **MCP and prompt surfaces** that return these events or attention items
  keep `untrusted` as a separately delimited field and never interpolate it
  into instruction text (`specs/llm-integration.md`).
- **Logging.** Daemon logs record connector id, error class, and `gh` exit
  status, never response bodies.

## Out Of Scope

- Any provider write by the watch.
- Dispatching attention items to agents. `specs/next.md` defines the item
  feed a dispatcher consumes; classification, leases, runner permissions, and
  run attribution are a separate design (roadmap P2).
- Receiving provider webhooks directly. The hint command is the integration
  point.
- Bridged mode for the watch. The reference GitHub MCP is REST-shaped and
  needs several calls per PR. The observation model is mode-agnostic, so it
  can be added later.
- `AB#` Azure Boards mentions as an association signal.
- Assigned PRs and issues, and GitHub Enterprise Server host selection.

## Open Questions

1. **Thread resolution and `updatedAt`.** Whether resolving a thread bumps
   the PR's `updatedAt` must be verified against the live API. The 30-minute
   reconciliation bounds the delay if it does not.
2. **`timelineItems(since:)` semantics.** Whether `since` filters by creation
   or by update time must be verified. Either works with id dedupe, but update
   time would re-fetch edited comments.
3. **Rollup precedence.** Whether the rollup reports `FAILURE` while other
   contexts are pending must be verified. The watch does not depend on it,
   because it waits for every context to be terminal.
4. **Team request semantics.** That `review-requested:@me` matches team
   requests and `user-review-requested:@me` matches only direct ones must be
   verified, along with whether a team request is removed when another team
   member reviews.
5. **Default `comment_body_max_runes`.** `8000` keeps nearly all review
   comments whole, at the cost of storing third-party text locally. Should
   managed policy be able to lower or lock it?

## Delivery Slices

0. Existing connector fixes (facts G1–G4).
1. Runtime extension: deferred polls, retry-after, the not-before deadline,
   and early wake (fact 19).
2. Author role capture: connect, queries, observations, diff engine, events,
   reply attribution, thread windows, timeline catch-up, snapshot
   enrichment, the untrusted payload, `today` personal-activity rules, the
   `--type` wildcard, and poll concurrency (facts 1–18, 20–23, 25, 32, 33).
3. Author role attention items in `workgraph next` and `resume` (facts 24,
   34, and `specs/next.md`).
4. Reviewer role: review-request and reviewed searches, team attribution,
   reviewer events, and reviewer attention items (facts 26–30, 35).
5. Push hints (fact 31).

## Facts

Facts for the watch live in `facts/github_pr_watch_test.go`. Slice-0 facts
live in `facts/github_test.go`. All use a fake `gh` script that serves queued
GraphQL fixtures and logs every invocation, following `writeFakeGH`, and an
injectable clock. Facts are numbered by when they were added, not by slice.

### Slice 0 — existing connector

- **G1. Rate-limit pre-check failure is a visible poll failure.** When the
  fake `gh api rate_limit` exits non-zero, the `github` poll records
  `last_error` and increments `consecutive_failures`, rather than recording a
  successful empty poll.
- **G2. `gh` bad credentials is an auth failure.** When `gh` exits with
  `HTTP 401: Bad credentials`, `github` setup becomes `error` and its poller
  stops.
- **G3. Search budget gate.** With `resources.search.remaining` below the
  poll's search count plus 2 and `resources.core.remaining` high, `github`
  skips `gh search`. With `resources.search.remaining` at 30 and `core` at 0,
  it polls.
- **G4. Bridged and direct share one PR identity.** Ingesting two bridged
  revisions of the same PR, then direct-capturing it, leaves exactly one
  `github.pull_request` row with the newest state, `repository`, and
  `branch`.

### Author role

1. **Connect validates gh and stays opt-in.** `github prs connect` logs
   `auth status` and a `viewer` probe and marks `github.pr_watch` ready.
   `github connect` alone leaves it disabled.
2. **First sight emits one `observed` per PR.** Two open authored PRs with
   existing reviews, comments, and failed checks yield exactly two `observed`
   events with full state and the awaiting-user backlog, and no other
   transition events.
3. **A reset neither duplicates nor loses.** After deleting observations, a
   comment created while observations were missing emits one
   `review_comment`, comments from before `baseline_at` emit nothing, no
   second `observed` is stored, and open items keep their ids and
   `opened_by`.
4. **Same SHA and same state produces no new event.**
5. **Checks wait for the whole run set.** A head with one failed and one
   pending context emits nothing. When the pending context finishes, exactly
   one `checks_failed` is emitted. A new head that goes pending emits
   nothing.
6. **Check runs are keyed by run execution.** Re-polling the same terminal
   runs emits nothing. A re-run with new run ids that finishes failed emits a
   second `checks_failed`, and so does a re-run that reuses the run id with a
   new `completedAt`.
7. **New reviews and comments emit by provider id**, oldest first, with
   `actor_kind`, `actor_association`, and `has_suggestion`. Three new
   comments in one thread produce three events.
8. **Timeline pagination catches up.** 150 conversation comments between
   polls produce 150 events across two pages in one poll. A backlog of 1,500
   comments emits the first 1,000 on one poll with the dimension `unknown`
   and `timeline_cursor` stored, and the remaining 500 on the next poll with
   no other change, after which `timeline_since` advances and
   `timeline_cursor` is cleared.
9. **Configured actors are ignored; bots are captured.** Comments by
   `ignore_actors` emit nothing. A `Bot` comment emits `review_comment` with
   `actor_kind: "bot"`. A comment by `identity_login` emits `author_replied`
   and flips the thread to `awaiting: other`.
10. **Reply attribution by marker and predecessor.** With
    `agent_reply_marker` set, a user comment ending in the marker emits
    `author_replied` with `responder: "agent"`. A reviewer comment after it
    in the same poll has `previous_responder: "agent"`, and a second reviewer
    comment has `previous_responder: "other"`. A different account's comment
    ending in the marker is a `review_comment`.
11. **Readiness is lost and regained.** Ready emits `ready_to_merge`. A new
    unresolved thread emits `readiness_lost` with `reasons: ["threads"]`.
    Resolving it emits a second `ready_to_merge` on the same SHA. Repeated
    polls of each state add nothing. With passing checks, approval, no
    threads, and `MERGEABLE`, a `merge_state_status` of `BLOCKED` or `BEHIND`
    is not ready, and `readiness_lost` from those lists `merge_state`.
    `mergeable: UNKNOWN`, `merge_state_status: UNKNOWN`, and more than 20
    opinionated reviews do not transition.
12. **Leaving the open set.** Merged and closed emit terminal events and
    record exits `merged` and `closed`. Archived records `out_of_scope`. A PR
    still `OPEN` and still matching search is kept for 6 polls, then
    recorded `left_search`. A `FORBIDDEN` PR is kept with `access:
    unreachable`, its items are marked unreachable and stale while other PRs
    poll successfully, and after 6 polls it is recorded `unreachable`. A
    reopened PR emits `reopened`.
13. **A rate-limited poll defers and keeps state.**
14. **A secondary rate limit sets a hard deadline.** A `RATE_LIMITED` error
    with `Retry-After: 120` records a failure, leaves observations unchanged,
    and sets the not-before deadline. Neither a hint nor `github prs capture`
    polls before it; `capture` prints `rate limited until` and exits
    non-zero.
15. **Auth expiry stops only this connector.**
16. **No token is ever persisted.**
17. **Comment text is stored with the untrusted flag.** A body with
    `Ignore previous instructions`, U+202E, an ANSI escape, a suggestion
    block, and more runes than the cap is stored only under
    `payload.untrusted`, with controls stripped, newlines and the suggestion
    block preserved, `body_truncated: true`, and `body_sha256`. The summary,
    event label, and attention item contain none of it. With
    `comment_body_max_runes: 0`, events carry no body text.
18. **An incomplete role fails without changes.** A role whose search is
    incomplete, has GraphQL errors, or is over its ceiling commits nothing
    and emits no `closed` events, while a complete role in the same poll
    commits and `poll_seq` increments once.
19. **Deferred polls do not count as failures.** A poller returning a
    deferral keeps `consecutive_failures` unchanged and sets the not-before
    deadline, even when it is more than 30 minutes away. A failure with
    retry-after waits at least that long. A connector returning neither keeps
    today's schedule.
20. **Read-only invocation and the type wildcard.** No `gh api graphql`
    invocation contains `mutation`, and `events today --type
    github.pull_request.*` lists transitions but not snapshots or issues.
21. **Snapshot enrichment is order-independent.** An existing
    `github.pull_request` row gains `branch` and `commit` while keeping
    `matched_scopes` and its `timestamp`, and a newer `github` participant
    poll keeps them. When the watch observes a PR first, no snapshot row is
    created, and a later participant poll inserts the row with `branch` and
    `commit` already set. Both orders give the same row.
22. **Associations use observed events only.** `head_sha` on an `observed`
    event associates with a captured `git.commit`. Transition events are not
    candidates. A URL inside `untrusted` creates no association. Event ids
    stay the same across a repository rename.
23. **Only the user's own PR actions are personal activity.** With
    `observed`, `review_comment`, `checks_failed`, `ready_to_merge`, an
    `author_replied` with `responder: agent`, and an `author_replied` with
    `responder: author` stored today, `today` counts only the
    `responder: author` reply and builds no session from the others.
    `events today` lists all of them.
24. **Resume relevant updates include only needs-user transitions.** After a
    resume check-in, a `review_comment` awaiting the user and a
    `checks_failed` appear under `Relevant updates since last check-in`. An
    `observed` event with `needs_user: true`, a `checks_passed`, and an
    `author_replied` do not. An event captured after the check-in with an
    earlier provider timestamp still appears. The morning handoff's personal
    work evidence is unchanged.
25. **Polls are fenced by `poll_seq`.** A `github prs capture` and a daemon
    poll that both see the same new comment store exactly one
    `review_comment`, and the losing poll retries from the winner's
    observations. A poll in flight when `github prs disconnect` runs commits
    nothing, leaves no observations behind, and does not retry.
32. **Thread windows keep participation and events.** In a thread with 25
    comments where the user's comment is the 3rd, a poll reads back to the
    known comment, emits each new comment once, and keeps
    `user_participates` true. A thread whose window cannot be completed in
    5 backward pages emits what it read with `triage.gap: true` and keeps
    participation.
33. **New conflicts have an opening event.** A PR going from `MERGEABLE` to
    `CONFLICTING` emits one `merge_conflict`. Repeated polls add nothing.
34. **Backlog items cite the observed event.** On first sight, a PR with
    failed checks, changes requested, a conflict, and an awaiting thread
    opens four items whose `opened_by` is the PR's `observed` event and whose
    `since` is `baseline_at`. After an observation reset the item ids and
    `opened_by` are unchanged.

### Reviewer role

26. **Direct and team requests enter the reviewer set.** With
    `direct_and_team`, a direct request and a request through a cached team
    both produce reviewer observations, labeled `direct` and `via org/team`.
    A team missing from the cache is labeled `via team (unresolved)`. With
    `direct`, the team-only PR is absent.
27. **First connect is backlog; later requests are transitions.** The first
    reviewer poll emits only `observed` per PR. A request appearing on a later
    poll emits `observed` and `review_requested`.
28. **Reviewing removes the request and keeps watching.** After the user
    submits a `CHANGES_REQUESTED` review, the poll emits `review_submitted`
    and `review_request_removed` with `reason: "reviewed"`, the PR stays in
    the reviewer set through the reviewed search, and a new head emits one
    `pushed_since_review`. A re-request emits a new `review_requested`. A
    later `PENDING` draft review by the user changes nothing: it emits no
    `review_submitted` and the `rereview` item stays open.
29. **Reviewer comment events are limited to the user's threads.** A comment
    in a thread the user participates in emits `review_comment` and makes the
    thread `awaiting: user`, including when the user's own comment is no
    longer in the last 20. A comment in another thread, and conversation
    comments, emit nothing. A requested-only PR runs no detail query.
30. **Ignored teams and aging out.** A PR requested only through an
    `ignore_teams` team is absent. A reviewed PR last updated before
    `reviewed_window` is dropped silently without a `closed` event and its
    items close with `out_of_scope`.
35. **A withdrawn request closes at once.** When a request is removed from an
    open PR the user never reviewed, the next poll emits
    `review_request_removed` with `reason: "withdrawn"`, deletes the
    observation, and closes the item with `out_of_scope`, without waiting 6
    polls.

### Push hints

31. **A hint wakes the poller within seconds.** With a 5-minute interval, a
    hint recorded 30 seconds after a poll triggers a poll at the next
    2-second check. A second hint during that poll triggers exactly one
    follow-up. A hint within 10 seconds of the previous poll start waits until
    10 seconds have passed. A hint naming a PR whose thread was resolved
    without an `updatedAt` change forces its detail query, so the `reply` item
    closes on that poll. A hint during a rate-limit deferral or a
    secondary-limit deadline waits for the deadline.
