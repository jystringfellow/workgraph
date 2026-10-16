# GitHub Authored Pull Request Watch

Status: proposed. No behavior described here is implemented until its facts in
`facts/github_authored_prs_test.go` and `facts/github_test.go` are active.

## Intent

workgraph already captures GitHub pull requests and issues the user is involved
in (`specs/github.md`). That connector stores one mutable snapshot row per PR,
which answers "what did I touch?" but not "what is waiting on me in my PRs?".

This spec adds a read-only watch over the user's **open authored pull
requests**. Each poll observes the complete open set, diffs it against the
previous local observation, and appends immutable transition events when
reviews, comments, checks, merge readiness, or lifecycle state change.

From those observations, workgraph answers one question locally and
deterministically: **what is waiting on me?** Threads awaiting a reply,
changes requested, failed checks, merge conflicts, and PRs ready to merge
appear in `today`, `resume`, and `workgraph github prs waiting`.

Capture requires no LLM and makes no provider writes. A failed poll changes
nothing locally. Routing feedback to agents is a later, separate design; this
spec records the structured fields such a design will need, and nothing more.

```text
gh api graphql (status query, then detail query for changed PRs)
  -> observation -> diff with previous observation
  -> transition events + new observation (one SQLite transaction)
  -> Waiting on you (read-time) / today / events today / resume
```

## Relationship To The Existing `github` Connector

The watch is a **separate registry entry**, `github.authored_prs`, with event
source `github`. This follows the `slack.lists` and `notion.activity` pattern of
a sub-connector that shares a source with its parent:

| | `github` | `github.authored_prs` |
|---|---|---|
| Scope | participant search (`involves`, `review_requested`) | open PRs authored by the identity |
| Query | `gh search prs/issues` over an updated-time window | `gh api graphql` over the current open set |
| Semantics | `bounded_events` | `complete_snapshot` |
| Storage | one mutable snapshot row per PR/issue | append-only transition events plus a local observation checkpoint |
| Cursor | `capture_cursors.completed_through` | `github_pr_observations` rows |

Keeping them separate means:

- enabling the watch never silently broadens or resets the existing
  participant scope or its cursor
- the watch has its own polling interval
- either can be disabled independently with `workgraph connectors disable`

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
  poll does not erase the enrichment. Poll order between the two connectors
  therefore does not matter.

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
- A workgraph-owned OAuth app or stored PAT would add token storage, refresh,
  and revocation work, and would duplicate what `gh` already does. Both are
  rejected.

## Setup

```text
workgraph github prs connect [--params-json '<scope-json>']
workgraph github prs capture
workgraph github prs waiting [--json]
workgraph github prs disconnect
```

`github prs connect`:

1. runs `gh auth status`
2. runs one probe query, `query { viewer { login } rateLimit { remaining resetAt } }`
3. records the scope in `connectors.json` `bridge_params`, marks setup
   `ready`, and enables polling
4. prints the controls: `workgraph connectors disable github.authored_prs`
   and `workgraph connectors interval github.authored_prs <duration>`

If either check fails, setup is marked `error` with the `gh` message and
guidance, and nothing is enabled. Connecting `github` does not enable
`github.authored_prs`; the watch is always opt-in.

`github prs disconnect` disables polling and deletes `github_pr_observations`
rows. Captured events are preserved.

`github prs capture` runs one poll immediately through the same function the
daemon uses.

### Scope

```json
{
  "scope": "authored_open",
  "identity": "@me",
  "ignore_actors": [],
  "comment_body_max_runes": 8000,
  "agent_reply_marker": "",
  "max_open_prs": 200
}
```

- `scope` must be `"authored_open"`.
- `identity` is the GitHub search identity, normally `@me`. The probe resolves
  and records the concrete `viewer.login` as non-secret `identity_login`.
- `ignore_actors` is an explicit login list whose comments and reviews never
  produce events or appear in Waiting on you. Bots are **not** ignored by
  default. Every event records `actor_kind` so consumers can treat bots
  differently.
- `comment_body_max_runes` caps each stored comment or review body, in the
  range `0..65536`. `65536` is GitHub's own comment limit. `0` stores no
  comment text; events then carry only structure and links.
- `agent_reply_marker` is an exact attribution line that agents acting as the
  user append to their comments, for example
  `[Robots on behalf of Stringfellow]`. A comment by `identity_login` whose
  last non-empty line equals the marker is attributed to `agent`; any other
  comment by `identity_login` is attributed to `author`. Empty disables the
  distinction. The marker identifies "an agent acting as the user", not a
  specific agent or run, and it cannot make another account's comment count
  as the user's.
- `max_open_prs` is the completeness ceiling.

Changing approved scope resets the observation baseline (see State Diff rule
1).

## Query Shape

Each poll uses a **status query** for the whole open set and a **detail
query** only for PRs that changed. Nesting comment connections under every
thread of every PR in one document multiplies GitHub's calculated cost: 50
PRs × 100 threads alone is about 5,000 connection requests, roughly 50 points
per poll, or 600 points an hour at a 5-minute interval. The split keeps a
typical poll at a few points. These are estimates; each response's measured
`rateLimit.cost` is recorded in connector state.

### Status query (every poll)

```graphql
query($q: String!, $after: String) {
  viewer { login }
  rateLimit { cost remaining resetAt }
  search(type: ISSUE, query: $q, first: 50, after: $after) {
    issueCount
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on PullRequest {
        id number url title isDraft state createdAt updatedAt
        repository { nameWithOwner }
        headRepository { nameWithOwner }
        isCrossRepository
        headRefName headRefOid baseRefName
        reviewDecision mergeable mergeStateStatus
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

- `$q` is `is:pr is:open author:<identity> archived:false sort:updated-desc`.
- Check status changes do not bump `updatedAt`, so the rollup is read here on
  every poll.

### Detail query (changed PRs only)

Each changed PR gets its own alias, so each has its own watermark and cursor:

```graphql
query($id0: ID!, $since0: DateTime, $after0: String, ...) {
  rateLimit { cost remaining resetAt }
  pr0: node(id: $id0) {
    ... on PullRequest {
      id body
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

- The detail query runs for a PR when:
  - it has no previous observation
  - its `updatedAt` differs from the previous observation
  - its last complete detail fetch is older than 30 minutes (reconciliation
    for changes that may not bump `updatedAt`, such as thread resolution)
- At most 10 PRs are aliased per document.
- **Timeline watermark.** `$sinceN` is the PR's `timeline_since`: the start
  time of its last complete detail fetch minus 10 minutes of clock-skew
  overlap. It is null on first sight. Overlap is harmless because the diff
  deduplicates by provider id.
- **Timeline pagination.** When `hasNextPage` is true, the PR is re-queried
  with `$afterN` until the page set is complete, up to 10 pages. Only a
  complete fetch advances `timeline_since`. A PR that exceeds 10 pages keeps
  its previous watermark, its comment and review dimensions are `unknown`
  for that poll, and connector status reports `detail incomplete:
  <repo>#<n>`. Comments are therefore never skipped by advancing past them.
- `latestOpinionatedReviews.totalCount > 20` makes the approval dimension
  `unknown`, so an unseen `CHANGES_REQUESTED` review cannot produce false
  readiness.
- PRs that leave the open set are resolved by a follow-up
  `nodes(ids: [...])` query requesting `state merged mergedAt closedAt
  mergedBy { login } repository { isArchived }`.
- Query documents are passed with `gh api graphql --input <file>`, never as
  argument strings. The documents are constants in the binary; facts assert
  that no executed document contains `mutation`.

### Conditional requests

GitHub GraphQL is `POST` and does not support `ETag` or `If-None-Match`.
Cost is controlled by the status/detail split and the polling interval.
`gh api --cache` is never used, because duration-based caching would hide
transitions.

## Observation Model

Each poll produces one normalized `PullRequestObservation` per PR. Only the
diff engine creates events.

```text
repository, number, node_id, url, title, is_draft, state, created_at, updated_at,
head_ref, head_sha, base_ref, head_repository, is_cross_repository,
baseline_at
review_decision        APPROVED | CHANGES_REQUESTED | REVIEW_REQUIRED | none
opinionated_reviews    [{reviewer, actor_kind, state}]  | unknown
checks_state           passed | failed | pending | none | unknown
check_runs             [{id, name, status, conclusion, details_url}]
threads                [{thread_id, path, line, is_resolved, resolved_by,
                         is_outdated, depth, last_author, last_responder,
                         agent_replies, awaiting}]
conversation           {last_human_comment_at, last_author_comment_at}
mergeable              MERGEABLE | CONFLICTING | UNKNOWN
merge_state_status     raw value
ready                  true | false | unknown
timeline_since, detail_fetched_at, observed_at
```

Mappings:

- `checks_state`:
  - `pending` while any context is not terminal (a CheckRun whose `status`
    is not `COMPLETED`, or a StatusContext in `PENDING` or `EXPECTED`),
    whatever the rollup state says
  - otherwise rollup `SUCCESS` maps to `passed` and `FAILURE` or `ERROR` to
    `failed`
  - a null rollup is `none`, and more than 100 contexts is `unknown`
- `responder` of a comment: `reviewer` when not by `identity_login`,
  otherwise `author` or `agent` by `agent_reply_marker`.
- `awaiting`: `author` when the thread's last comment's responder is
  `reviewer`, otherwise `reviewer`. Resolved threads have no `awaiting`.
- `last_responder` is the responder of the thread's last comment. `depth` is
  the thread's comment `totalCount`. `agent_replies` counts comments
  attributed to `agent` among those returned.
- `baseline_at` is the `observed_at` of the poll that first saw the PR. It
  survives observation resets because it is also stored on the `observed`
  event.
- More than 100 threads makes thread-derived values `unknown`.

Observations live in a connector-owned checkpoint table:

```text
github_pr_observations(
  connector_id, node_id, repository, number,
  observation_json, observed_at,
  PRIMARY KEY (connector_id, node_id))
```

This is provider checkpoint state, not a source of truth. Deleting it causes a
re-bootstrap that neither duplicates nor loses events (see rule 1).

## Event Schema

Events use source `github` and type prefix `github.pull_request.`.

| Type | Emitted when | Discriminator | `actor` | `timestamp` |
|---|---|---|---|---|
| `observed` | a PR is first seen by the watch | none | PR author | `observed_at` |
| `reopened` | a PR with a prior `closed` event re-enters the open set | `<closed_at>` | empty | `observed_at` |
| `review_comment` | a new comment id by anyone other than self appears in a thread, in the conversation, or as a non-empty `COMMENTED` review body | `<comment_or_review_id>` | comment author | `createdAt` / `submittedAt` |
| `author_replied` | a new comment id by `identity_login` appears in a thread or the conversation | `<comment_id>` | `identity_login` | `createdAt` |
| `changes_requested` | a new review with state `CHANGES_REQUESTED` | `<review_id>` | reviewer | `submittedAt` |
| `approved` | a new review with state `APPROVED` | `<review_id>` | reviewer | `submittedAt` |
| `checks_failed` | `checks_state` becomes `failed` for a run set not yet reported on the head | `<head_sha>:<run_fingerprint>` | empty | latest context completion |
| `checks_passed` | `checks_state` becomes `passed` for a run set not yet reported on the head | `<head_sha>:<run_fingerprint>` | empty | latest context completion |
| `ready_to_merge` | `ready` goes from false to true | `<head_sha>:<observed_at>` | empty | `observed_at` |
| `readiness_lost` | `ready` goes from true to false | `<head_sha>:<observed_at>` | empty | `observed_at` |
| `merged` | the PR left the open set and the follow-up shows merged | `<merged_at>` | `mergedBy.login` | `mergedAt` |
| `closed` | the PR left the open set and the follow-up shows closed and not merged | `<closed_at>` | empty | `closedAt` |

The event id is
`github.pull_request.<suffix>:<node_id>[:<discriminator>]`. The PR's GraphQL
node id is stable across repository renames and transfers, so a rename
cannot re-emit captured occurrences. Rows are inserted with `INSERT ... ON
CONFLICT(id) DO NOTHING`. Transition events are immutable.

Other columns:

- `project`: `inferGitHubProject` (local remote, then repository name)
- `involvement_json`: `["authored"]`, which the query scope proves
- `summary`: a fixed template built only from structured fields, for example
  `PR #42 checks failed on 1111111 (lint, unit)` or
  `PR #42 review comment from alice on settings.go:18`. It never includes
  comment text.

### Payload

```json
{
  "repository": "example-org/workgraph-demo",
  "number": 42,
  "node_id": "PR_kwDO...",
  "url": "https://github.com/example-org/workgraph-demo/pull/42",
  "title": "Add settings helper",
  "branch": "feature/settings-helper",
  "head_sha": "1111111111111111111111111111111111111111",
  "head_repository": "example-org/workgraph-demo",
  "is_cross_repository": false,
  "actor_kind": "user",
  "actor_association": "MEMBER",
  "state": {
    "review_decision": "REVIEW_REQUIRED",
    "checks_state": "failed",
    "unresolved_threads": 2,
    "threads_awaiting_author": 1,
    "mergeable": "MERGEABLE",
    "is_draft": false,
    "ready": false
  },
  "triage": {
    "category": "review_feedback",
    "needs_author": true,
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
- `triage.category` is `review_feedback`, `ci`, `merge`, `author_reply`, or
  `lifecycle`.
- `triage.needs_author` is true for:
  - `review_comment` events whose thread is awaiting the author
  - `changes_requested`
  - `checks_failed`
  - `ready_to_merge`
  - `observed` events whose state has `threads_awaiting_author > 0`, failed
    checks, or changes requested

  It is false otherwise. It is a deterministic hint, not a priority judgment.
- `triage.previous_responder` is the responder (`reviewer`, `author`, or
  `agent`) of the comment immediately before this one in the same thread, or
  `null` for the first comment. New comments are processed in chronological
  order, so several comments arriving between polls each get the correct
  predecessor.
- `triage.has_suggestion` is true when the normalized body, before
  truncation, contains a line that is exactly a ```` ```suggestion ```` fence
  opener.
- `triage.failed_checks` (on `checks_failed`) lists up to 20
  `{name, conclusion, details_url}` entries, sorted by name.
- On `author_replied`, `triage.responder` is `author` or `agent`, and the
  payload has no `untrusted` body. The user's own text is not copied.
- `observed` events carry the full current `state`, `baseline_at`, and,
  under `untrusted`, a `threads` array of up to 20 open threads awaiting the
  author, each with path, line, URL, and last-comment body.
- When a thread's returned 20 comments do not reach back to a comment
  already known (stored as an event, or created at or before `baseline_at`),
  the earliest emitted comment has `triage.gap: true`.

## State Diff And Dedupe Rules

The diff compares the previous observation P with the current observation C
for the same `node_id`.

1. **First sight and reset.**
   - A PR with no P and no stored `observed` event emits exactly one
     `observed` event carrying its full current state and `baseline_at`. It
     emits no per-comment, per-review, or check events. Comments and reviews
     created at or before `baseline_at` are baseline.
   - A PR with no P but a stored `observed` event (an observation reset,
     scope change, or reconnect) does not re-baseline. It reuses the stored
     `baseline_at` and emits events for comments and reviews created after it
     that are not already stored. Nothing that happened while observations
     were missing is lost, and nothing is duplicated.
   - When the PR has a prior `closed` event, `reopened` is also emitted.
2. **Unchanged.** If C equals P in every dimension, no event is emitted and
   only `observed_at` is refreshed.
3. **Unknown never transitions.** A dimension whose C value is `unknown`, or
   `mergeable: UNKNOWN`, is carried forward from P, emits nothing, and is
   reported as unknown, not as current, in Waiting on you.
4. **Checks.**
   - `pending` emits nothing. A failure is reported once the head's whole run
     set is terminal, not at the first failing job.
   - `run_fingerprint` is the first 12 hex characters of SHA-256 over the
     sorted CheckRun ids and `context@createdAt` values of the terminal run
     set.
   - A head emits a checks event when it reaches a terminal state whose
     fingerprint has not been reported for that head. Observing the same runs
     again cannot emit twice, and returning to an earlier SHA does not
     duplicate.
   - A re-run creates new run ids, so a re-run that finishes failed emits a
     new `checks_failed`. Jobs created progressively by `needs:` dependencies
     are covered by waiting for the whole set to be terminal.
   - A new head starts at whatever C reports, normally `pending`.
5. **Comments and reviews.** Each comment or review id that is neither stored
   as an event nor baseline emits one event, oldest first, unless its author
   is in `ignore_actors`. Comments by `identity_login` emit `author_replied`
   instead of `review_comment` and turn the thread's `awaiting` to
   `reviewer`. Edits keep their id and emit nothing.
6. **Readiness.** `ready` is true only when every condition holds:
   - `state = OPEN` and `is_draft = false`
   - approval: `review_decision = APPROVED`, or `review_decision` is none,
     at least one `APPROVED` review exists, and no reviewer's latest
     opinionated review is `CHANGES_REQUESTED`
   - `checks_state = passed`, or `checks_state = none` with
     `merge_state_status = CLEAN`
   - zero unresolved, non-outdated threads, which must be known
   - `mergeable = MERGEABLE`

   `ready` false to true emits `ready_to_merge`. True to false emits
   `readiness_lost`, with `triage.reasons` listing each failed condition
   (`draft`, `approval`, `checks`, `threads`, `conflicts`). Unknown does not
   transition. The discriminator includes the observing poll's
   `observed_at`, so readiness that is lost and regained on the same SHA
   emits `ready_to_merge` again, while repeated polls of one state never
   duplicate.
7. **Leaving the open set.** A PR in P but not in C is resolved through the
   follow-up query:
   - Merged: emit `merged` and delete the observation.
   - Closed and not merged: emit `closed` and delete the observation.
   - Still `OPEN` in an archived repository: delete the observation silently.
   - Still `OPEN` otherwise (search lag): keep P and emit nothing.
   - Null or `FORBIDDEN` (lost access): keep P, emit nothing, and report
     `unreachable: <repo>#<n>` in connector status.
8. **Missed intermediate states.** Polling observes state, not history.
   Check and readiness states that come and go between polls are not seen.
   Comments and reviews are not lost this way, because they are read by id
   from a complete timeline or a thread window marked with `gap`.
9. **Atomicity.** All events, the snapshot enrichment, and all updated
   observations for a poll commit in one transaction. Any failure rolls back
   everything.

## Waiting On You

Waiting on you is a **read-time projection** over the latest observations of
open authored PRs and their events. It stores nothing and makes no GitHub
request.

| Reason | Shown when | Since |
|---|---|---|
| `checks_failed` | `checks_state = failed` on the current head | the `checks_failed` event |
| `changes_requested` | any reviewer's latest opinionated review is `CHANGES_REQUESTED` | that review's event |
| `reply` | an unresolved, non-outdated thread is `awaiting: author` | the thread's last comment |
| `reply` | a human (`actor_kind: user`) conversation comment is newer than the user's last conversation comment | that comment |
| `conflicts` | `mergeable = CONFLICTING` | first observation in that state |
| `ready_to_merge` | `ready = true` | the `ready_to_merge` event |

- Review threads from bots count; conversation comments from bots, such as
  coverage reports, do not.
- A thread or comment is shown with its actor, `actor_kind`, path and line,
  and URL. Comment text is not shown in the compact view.
- A thread an agent answered with the marker shows as awaiting the reviewer,
  not the user. `--json` includes `last_responder` so the user can see agent
  answers.
- Items are ordered by reason in the table's order, then oldest first.
- Draft PRs are included and marked `draft`.
- Dimensions that are `unknown` are shown as `unknown` with the reason, never
  as their carried-forward value.
- **Freshness.** Output starts with `as of <last successful poll>`. When the
  connector is in setup `error`, or the last successful poll is older than
  three intervals, it adds a warning line naming the problem.

Surfaces:

- `workgraph today` adds a `Waiting on you` section when
  `github.authored_prs` is enabled, for example:

  ```text
  Waiting on you (as of 17:02)
    #42 Add settings helper: checks failed on 1111111 (lint, unit)
    #42 Add settings helper: alice on settings.go:18
    #57 Cache warmup: changes requested by bob
    #61 Retry policy: ready to merge
  ```

- `workgraph resume <project>` shows the same items for that project's PRs,
  together with each open PR's review, check, and thread state.
- `workgraph github prs waiting [--json]` prints the full list. `--json`
  keeps `untrusted` fields separately delimited.

## Polling And Backoff

### Cadence

- The default interval is `5m`. `workgraph connectors interval
  github.authored_prs 15m` lengthens it. The watch rejects intervals shorter
  than `1m`.
- The daemon polls once at startup. After the machine sleeps, the next poll
  runs within one interval of waking, and the timeline watermark and
  follow-up query catch up on everything that happened meanwhile.
- `workgraph github prs capture` polls immediately.
- Polling is the source of truth. Push hints from webhooks or a company event
  bus may later trigger an early poll, but they never carry state.

### Runtime extension

The current poller (`runConnectorPoller` in `run.go`) treats every poll as
success or failure, resets failures on success, and always waits either the
interval or the failure backoff. The watch needs a small extension:

- A poll may return a **deferred** outcome with a reason and a not-before
  time. A deferral records no failure, leaves `consecutive_failures`
  unchanged, sets `next_poll_at` to the later of the interval and the
  not-before time (capped at 30 minutes), and shows the reason in
  `connectors status`.
- A failed poll may carry a **retry-after** duration. The next attempt waits
  for the larger of the backoff and that duration.
- Connectors that return neither behave exactly as today.

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
| Auth expired or revoked | `gh` exit with `HTTP 401`, `Bad credentials`, or `gh auth login` guidance | setup `error`, stop this poller, guidance `gh auth login` then `workgraph connectors validate github.authored_prs` |
| SSO not authorized | GraphQL `FORBIDDEN` / SAML | poll fails visibly with SSO guidance |
| Offline or DNS failure | `gh` network error | transient backoff (5s to 5m) |
| `gh` missing | exec error | setup `error` |
| Malformed JSON or schema drift | decode failure | poll fails; nothing changes |
| Completeness violated | see Completeness | poll fails; nothing changes |
| Whole-poll deadline (30s) | context deadline | transient |
| Repository renamed or transferred | `nameWithOwner` changes under the same `node_id` | observation and event ids follow `node_id`; payloads use the new name |

## Completeness

The watch uses `complete_snapshot` semantics, so the open set must be
provably complete:

- the search must page to `hasNextPage = false`
- `issueCount` must equal the number of nodes received
- `issueCount > max_open_prs` fails the poll visibly
- GraphQL `errors` fail the whole poll, including SAML/SSO `FORBIDDEN` on some
  repositories, because dropping those PRs would look like closes. The
  exception is `RATE_LIMITED`, which is handled as a rate limit.

Nested connections that exceed their page mark only that dimension `unknown`.

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
   - Effect: bridged users see one duplicate PR row per revision in `today`,
     mode switches double-count, and association extraction misses the
     repository.
   - Fix: bridged `github.pull_request` and `github.issue` ingest normalizes
     onto the direct snapshot id and payload keys with the same newer-wins
     upsert. Existing duplicate rows are left in place.

## today, events today, resume

- `workgraph today` renders transition events with templated labels, for
  example `Add settings helper (#42): checks failed on 1111111 (lint)`, and
  adds the Waiting on you section. Comment text never reaches the compact
  view.
- `workgraph events today --type github.pull_request.*` lists only transition
  events. `--type` gains a trailing `.*` wildcard meaning "the type starts with
  this prefix and the next character is a dot". Exact matching is unchanged.
- The detailed `events today` row for a `review_comment` shows the first line
  of the body behind an explicit marker,
  `untrusted comment (alice, bot): "…"`.
- Resume relevance treats `github.pull_request.*` events with `authored`
  involvement as user-work evidence. Their `actor` is usually a reviewer, so
  the existing rule that requires actor to equal the login would discard them.

## Associations

Associations follow `specs/event-associations.md`: deterministic, local, no
fuzzy matching, and no time-only matches.

- **Only `observed` events and `github.pull_request` snapshot rows** are
  association candidates from this watch. Every transition event carries the
  same repository, URL, and head SHA, so treating each as a candidate would
  attach one Slack link or commit to dozens of events per PR. `associations
  explain` on a transition event explains its PR's `observed` event.
- Signals available on those rows without new rules:
  - **Commit**: `head_sha` matches captured `git.commit` events. Snapshot
    enrichment gives snapshot rows the same signal.
  - **Branch**: `branch` plus exact `repository`.
  - **URL**: a PR URL in a Slack message matches the PR `url`.
- Signal extraction skips the `untrusted` subtree. A comment cannot create
  association evidence by planting a URL.

## Security Notes

- **Read-only.** Only constant GraphQL `query` documents are executed. The
  watch never re-runs CI, comments, resolves, approves, merges, or pushes.
- **No token persistence.** See Auth Decision. Facts verify that a token
  sentinel never appears under the workgraph home or in the database, and that
  `gh auth token` is never invoked.
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
  - Untrusted text never appears in `summary`, compact `today` labels,
    project inference, association signals, or logs.
- **All provider strings are untrusted for agents.** Titles, branches, URLs,
  logins, and paths are provider-controlled too. MCP and LLM consumers treat
  every payload string as data.
- **MCP and prompt surfaces** that return these events keep `untrusted` as a
  separately delimited field and never interpolate it into instruction text
  (`specs/llm-integration.md`).
- **Logging.** Daemon logs record connector id, error class, and `gh` exit
  status, never response bodies.

## Out Of Scope

- Any provider write by the watch.
- Routing feedback to agents, or any dispatch. That is a later design; the
  watch records `actor_association`, `previous_responder`, `has_suggestion`,
  `agent_replies_in_thread`, and `is_cross_repository` so it has the inputs
  it needs.
- Bridged mode for the watch. The reference GitHub MCP is REST-shaped and
  needs several calls per PR. The observation model is mode-agnostic, so it
  can be added later.
- `AB#` Azure Boards mentions as an association signal.
- Review-request transitions. The `github` connector's `review_requested`
  scope covers PRs where the user is a reviewer.
- PRs where the user is a reviewer, and GitHub Enterprise Server host
  selection.

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
4. **Default `comment_body_max_runes`.** `8000` keeps nearly all review
   comments whole, at the cost of storing third-party text locally. Should
   managed policy be able to lower or lock it?

## Delivery Slices

0. Existing connector fixes (facts G1–G4).
1. Runtime extension for deferred polls and retry-after (fact 19).
2. Direct capture: connect, queries, observations, diff engine, events,
   reply attribution, snapshot enrichment, the untrusted payload, `today`
   labels, and the `--type` wildcard (facts 1–18, 20–22).
3. Waiting on you in `today`, `resume`, and `github prs waiting` (facts
   23–25).

## Facts

Facts for the watch live in `facts/github_authored_prs_test.go`. Slice-0 facts
live in `facts/github_test.go`. All use a fake `gh` script that serves queued
GraphQL fixtures and logs every invocation, following `writeFakeGH`, and an
injectable clock.

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

### Watch

1. **Connect validates gh and stays opt-in.** `github prs connect` logs
   `auth status` and a `viewer` probe and marks `github.authored_prs` ready.
   `github connect` alone leaves it disabled.
2. **First sight emits one `observed` per PR.** Two open PRs with existing
   reviews, comments, and failed checks yield exactly two `observed` events
   with full state and the awaiting-author backlog, and no other transition
   events.
3. **A reset neither duplicates nor loses.** After deleting observations, a
   comment created while observations were missing emits one
   `review_comment`, comments from before `baseline_at` emit nothing, and no
   second `observed` is stored.
4. **Same SHA and same state produces no new event.**
5. **Checks wait for the whole run set.** A head with one failed and one
   pending context emits nothing. When the pending context finishes, exactly
   one `checks_failed` is emitted. A new head that goes pending emits
   nothing.
6. **Check runs are keyed by run identity.** Re-polling the same terminal
   runs emits nothing. A re-run with new run ids that finishes failed emits a
   second `checks_failed`.
7. **New reviews and comments emit by provider id**, oldest first, with
   `actor_kind`, `actor_association`, and `has_suggestion`. Three new
   comments in one thread produce three events.
8. **Timeline pagination is lossless.** 150 conversation comments between
   polls produce 150 events across two pages. With the page limit exceeded,
   `timeline_since` is unchanged, the dimension is `unknown`, and the next
   complete fetch emits the remaining comments.
9. **Configured actors are ignored; bots are captured.** Comments by
   `ignore_actors` emit nothing. A `Bot` comment emits `review_comment` with
   `actor_kind: "bot"`. A comment by `identity_login` emits `author_replied`
   and flips the thread to `awaiting: reviewer`.
10. **Reply attribution by marker and predecessor.** With
    `agent_reply_marker` set, a user comment ending in the marker emits
    `author_replied` with `responder: "agent"`. A reviewer comment after it
    in the same poll has `previous_responder: "agent"`, and a second reviewer
    comment has `previous_responder: "reviewer"`. A different account's
    comment ending in the marker is a `review_comment`.
11. **Readiness is lost and regained.** Ready emits `ready_to_merge`. A new
    unresolved thread emits `readiness_lost` with `reasons: ["threads"]`.
    Resolving it emits a second `ready_to_merge` on the same SHA. Repeated
    polls of each state add nothing. `mergeable: UNKNOWN` and more than 20
    opinionated reviews do not transition.
12. **Leaving the open set.** Covers merged, closed, still `OPEN`, archived,
    `FORBIDDEN`, and reopened (which emits `reopened`).
13. **A rate-limited poll defers and keeps state.**
14. **A secondary rate limit backs off without losing state.**
15. **Auth expiry stops only this connector.**
16. **No token is ever persisted.**
17. **Comment text is stored with the untrusted flag.** A body with
    `Ignore previous instructions`, U+202E, an ANSI escape, a suggestion
    block, and more runes than the cap is stored only under
    `payload.untrusted`, with controls stripped, newlines and the suggestion
    block preserved, `body_truncated: true`, and `body_sha256`. The summary and
    `today` label contain none of it. With `comment_body_max_runes: 0`,
    events carry no body text.
18. **An incomplete snapshot fails without changes.**
19. **Deferred polls do not count as failures.** A poller returning a
    deferral keeps `consecutive_failures` unchanged and schedules
    `next_poll_at` at the not-before time. A failure with retry-after waits at
    least that long. A connector returning neither keeps today's schedule.
20. **Read-only invocation and the type wildcard.** No `gh api graphql`
    invocation contains `mutation`, and `events today --type
    github.pull_request.*` lists transitions but not snapshots or issues.
21. **Snapshot enrichment survives participant polls.** An existing
    `github.pull_request` row gains `branch` and `commit` while keeping
    `matched_scopes` and its `timestamp`. A newer `github` participant poll
    keeps them. No snapshot row is created for a PR the `github` connector
    never captured. Running the two connectors in either order gives the same
    row.
22. **Associations use observed events only.** `head_sha` on an `observed`
    event associates with a captured `git.commit`. Transition events are not
    candidates. A URL inside `untrusted` creates no association. Event ids
    stay the same across a repository rename.
23. **Waiting on you lists each reason.** Fixtures with failed checks,
    changes requested, a thread awaiting the author, a human conversation
    comment, a bot coverage comment, a conflict, and a ready PR list exactly
    the six expected items in order, without the bot conversation comment,
    and without invoking `gh`. `today`, `resume <project>`, and `github prs
    waiting` show the same items for the project's PRs.
24. **Agent answers leave Waiting on you.** A thread whose last comment is an
    agent reply with the marker is not listed. A reviewer reply after it is.
25. **Waiting on you reports staleness.** With the connector in setup `error`
    or the last successful poll older than three intervals, `today` and
    `github prs waiting` print a warning, and `unknown` dimensions are shown
    as unknown.
