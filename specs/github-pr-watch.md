# GitHub Pull Request Watch

Status: implemented for the author and reviewer roles and push hints, with
active facts in `facts/github_pr_watch_test.go`,
`facts/github_pr_watch_author_test.go`, and
`facts/github_pr_watch_reviewer_test.go`. The related `github` connector
fixes listed below are separate; only the `gh` auth classification fix is
implemented.

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
  one of the user's teams

Each poll observes each complete set, diffs it against the previous local
observation, and appends immutable transition events when reviews, comments,
checks, merge readiness, review requests, or lifecycle state change.

The watch is the first source for `workgraph next` (`specs/next.md`), the
single local list of what the user should do next. The watch defines the
GitHub attention items; `next` renders them and exposes them as a stable,
machine-readable feed that a later dispatch design can build on.

This is a first version to build and iterate on. Known limitations are listed
at the end rather than designed away.

Capture requires no LLM and makes no provider writes. A role whose poll fails
changes nothing locally for that role.

```text
push hint (optional) ─┐
interval ─────────────┴─> poll
  gh api graphql (status query per role, then detail query for changed
  author-role PRs)
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
| Scope | participant search (`involves`, `review_requested`) over an updated-time window | the current open author and review-requested sets |
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
  `ready_to_merge`, or `review_requested`, and its `triage.needs_user` is
  true. `observed` never qualifies, because its backlog is already in
  `Waiting on you`.
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
- The `github` connector's newer-wins upsert (`storeGitHubEvent`) changes to
  keep the existing `branch` and `commit` values when the incoming payload has
  none, and, when it inserts a new PR row, to copy `branch` and `commit` from
  the latest `github_pr_observations` row for the same repository and number.
  Poll order between the two connectors therefore does not matter.

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
4. records the scope (see Scope and fencing), marks setup `ready`, and enables
   polling
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
  "ignore_actors": [],
  "ignore_teams": [],
  "ignore_authors": [],
  "ignore_repositories": [],
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
- `ignore_actors` is an explicit login list. Their comments and reviews are
  removed before any state is derived, so they produce no events, never make
  a thread or conversation await the user, and never count as a
  `CHANGES_REQUESTED` reviewer. GitHub's own `reviewDecision` and merge state
  still reflect them, because those are provider merge rules. Bots are **not**
  ignored by default; every event records `actor_kind`.
- `ignore_teams` is a list of `org/team-slug` values. A request through an
  ignored team counts as absent.
- `ignore_authors` is a list of PR author logins whose review requests count
  as absent. The special value `bots` matches any bot author
  (`author.__typename` `Bot`). Logins match case-insensitively, ignoring an
  `app/` prefix and a `[bot]` suffix, so `app/dependabot`,
  `dependabot[bot]`, and `dependabot` are the same author. It applies only to
  the reviewer role: the user's own PRs, including ones a bot opened on the
  user's behalf, are never ignored by author.
- `ignore_repositories` is a list of `owner/name` repositories whose PRs are
  absent from both roles.
- Ignored PRs are dropped from the search results after the completeness
  check, so they create no observation, emit no event, and open no item.
  `ignore_authors` and `ignore_repositories` are omitted from the stored
  scope when empty, so adding them to an existing watch changes its scope
  only when they are set.
- `comment_body_max_runes` caps each stored comment or review body, in the
  range `0..65536`. `65536` is GitHub's own comment limit. `0` stores no
  comment text; events then carry only structure and links.
- `agent_reply_marker` is an exact attribution line that agents acting as the
  user append to their comments. A comment by `identity_login` whose last
  non-empty line equals the marker is attributed to `agent`; any other comment
  by `identity_login` is attributed to `author`. Empty disables the
  distinction. The marker means "some agent acting as the user", not a
  specific agent or run.
- `max_open_prs` is the completeness ceiling for each role, so a busy team
  queue cannot fail the author role.

### Scope and fencing

The user-facing scope is stored in `connectors.json` `bridge_params`, like
other connectors. The watch also keeps a small SQLite row that is the
authority for whether a poll may commit:

```text
github_pr_watch_state(connector_id PRIMARY KEY, enabled, scope_hash, poll_seq)
```

- `connect`, `disconnect`, `connectors disable`/`enable` for
  `github.pr_watch`, and any scope or role change first update this row in a
  SQLite transaction (deleting observations when the scope changes or the
  watch is disconnected) and increment `poll_seq`, then write
  `connectors.json`.
- At daemon start, and before each poll, the watch compares the
  `connectors.json` scope and enabled flag with this row. A mismatch, for
  example after a crash between the two writes, is applied as a scope change
  or disable through the same SQLite transaction.
- Changing approved scope resets the observation baseline (see State Diff rule
  1).

## Query Shape

Each poll uses one cheap **status query** per enabled role for the whole open
set, then, for author-role PRs only, a batched **merge and checks query** and
a **detail query** for the PRs that changed. The status query never asks for
`mergeable`, `mergeStateStatus`, or check contexts: those force GitHub to
compute a test merge per PR and push a 50-PR search past GitHub's roughly
10-second GraphQL timeout (`HTTP 502`) on accounts with many open PRs and
checks. The split keeps a typical poll at a few GraphQL points.

### Status query (every poll, per role)

The author search selects only what the author role needs from every PR on
every poll. Even `reviewDecision` alone costs enough per PR to bring a
50-PR author search near GitHub's timeout, so it moves to the merge and checks
query:

```graphql
query($q: String!, $first: Int!, $after: String) {
  viewer { login }
  rateLimit { cost remaining resetAt }
  search(type: ISSUE, query: $q, first: $first, after: $after) {
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
        commits(last: 1) {
          nodes { commit { oid statusCheckRollup { state contexts { totalCount } } } }
        }
      }
    }
  }
}
```

The reviewer search adds what only the reviewer role uses:

```graphql
query($q: String!, $login: String!, $first: Int!, $after: String) {
  ...same search and fields as the author search, plus:
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
}
```

- `$first` is the search page size: 20 by default, reduced after timeouts
  (see GitHub query timeouts). Completeness still requires paging to
  `hasNextPage = false` and `issueCount` equal to the nodes received across
  all pages.

`$q` by role:

| Role | Search |
|---|---|
| author | `is:pr is:open author:<identity> archived:false sort:updated-desc` |
| reviewer | `is:pr is:open review-requested:<identity> archived:false sort:updated-desc` (`user-review-requested:` for `direct`) |

- `viewerLatestReview` excludes `PENDING` reviews, which are the user's own
  unsubmitted drafts. A returned review with a null `submittedAt` is treated
  as absent. The reviewer role uses it only to tell `reviewed` from
  `withdrawn` and to emit `review_submitted`.
- Check status changes do not bump `updatedAt`, so the rollup `state` and
  context `totalCount` are read here on every poll. A change in either is what
  triggers the merge and checks query. The reviewer role ignores checks.

### Merge and checks query (author role, changed PRs only)

```graphql
query($id0: ID!, ...) {
  rateLimit { cost remaining resetAt }
  pr0: node(id: $id0) {
    ... on PullRequest {
      id headRefOid reviewDecision mergeable mergeStateStatus
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
  pr1: node(id: $id1) { ... }
}
```

- It runs for an author-role PR when it is new; its head SHA, `updatedAt`,
  rollup `state`, or context `totalCount` changed; its stored `mergeable` or
  `mergeStateStatus` is `UNKNOWN` (GitHub computes them lazily); its last
  fetch of this data is older than 30 minutes; or a hint forces it.
- Other PRs carry their previous `review_decision`, `mergeable`,
  `merge_state_status`, `checks_state`, and check runs forward. A submitted
  review bumps `updatedAt`, so `reviewDecision` changes are refetched on the
  next poll. A carried value never emits a
  transition. Changes that move none of the triggers, such as a base branch
  advance (`BEHIND`) or a re-run that ends in the same state, are seen within
  the 30-minute reconciliation or on a hint.
- At most 10 PRs are aliased per document. Errors fail the role.
- The query also selects `headRefOid`. When it differs from the search's
  head (a push landed between the two queries), that PR's merge and check
  data is ignored for the poll and refetched on the next one, so checks are
  never attributed to the wrong head.

### GitHub query timeouts

- When a merge and checks or detail document returns `HTTP 502` or `504`,
  the watch halves the batch and retries within the poll, down to one PR. A
  timeout on a one-PR document fails the role with `GitHub timed out on the
  query (HTTP 502)`.
- The reduced batch size is stored in connector state as soon as it shrinks,
  so the next poll starts there even when the whole-poll deadline cut the
  previous one short. Each poll that commits without a timeout grows it by
  one, up to 10.
- When a status search page times out, the watch halves the search page size
  (minimum 5), stores it in connector state, and retries the same page within
  the poll. Each poll without a search timeout grows it by 5, up to 20.
- When the status search still times out at 5, the poll is **deferred** for
  60 seconds with the reason `GitHub timed out on the query (HTTP 502)` in
  connector status. A deferral does not count as a failure. Roles that completed in the
  same poll still commit, and another role's real error is reported instead
  of the deferral.

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

The result is cached in connector state and refreshed at connect and hourly
after a successful refresh. A failed refresh records a warning, preserves the
previous teams and successful refresh timestamp, and retries on the next poll.
Rate limits use the normal retry-after deadline before another refresh. It is used only to label a request: a request whose `requestedReviewer`
is a team in the cache is `via <org>/<slug>`. A PR returned by the search whose
requests match no cached team is labeled `via team (unresolved)` rather than
dropped, because GitHub's search already decided it matches. When the user is
not among the returned requests and `reviewRequests.totalCount` exceeds the
returned nodes, the request is recorded with `incomplete: true`, because an
omitted request could be direct; `incomplete` is absent otherwise.

### Detail query (author role, changed PRs only)

Each changed PR gets its own alias, so each has its own watermark and cursor:

```graphql
query($id0: ID!, $since0: DateTime, $after0: String, ...) {
  rateLimit { cost remaining resetAt }
  pr0: node(id: $id0) {
    ... on PullRequest {
      id
      latestOpinionatedReviews(first: 20) {
        totalCount
        nodes { id state submittedAt author { __typename login } }
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
          comments(first: 100) {
            totalCount
            nodes {
              id state body createdAt url diffHunk replyTo { id }
              authorAssociation author { __typename login }
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
  - it is leaving the open set (see State Diff rule 8)
- At most 10 PRs are aliased per document.
- **Review thread comments** are read in one page of up to 100 per thread.
  Comments whose `state` is `PENDING` (the user's unsubmitted draft review
  comments) are dropped before anything else. A thread with more than 100
  comments is `unknown` for that poll.
- **Timeline watermark.** `$sinceN` is the PR's `timeline_since`: the start
  time of its last complete detail fetch minus 10 minutes of clock-skew
  overlap. It is null on first sight. Overlap is harmless because the diff
  deduplicates by provider id.
- **Timeline pagination and catch-up.** When `hasNextPage` is true, the PR is
  re-queried with `$afterN`, up to 10 pages per poll. If pages remain, the
  poll commits the events from the pages it read, stores the last `endCursor`
  as `timeline_cursor`, and marks the PR's comment and review dimensions
  `unknown`. The next poll resumes from `timeline_cursor`. When a fetch
  reaches `hasNextPage = false`, `timeline_cursor` is cleared and
  `timeline_since` advances.
- `latestOpinionatedReviews.totalCount > 20` makes the approval dimension
  `unknown`, so an unseen `CHANGES_REQUESTED` review cannot produce false
  readiness.
- PRs that leave a role's set are resolved by a follow-up
  `nodes(ids: [...])` query requesting `state merged mergedAt closedAt
  mergedBy { login } repository { isArchived }`, plus `reviewRequests` and
  `viewerLatestReview` for reviewer-role PRs.
- Query documents are passed with `gh api graphql --input <file>`, never as
  argument strings. The documents are constants in the binary; facts assert
  that no executed document contains `mutation`.

GitHub GraphQL is `POST` and does not support `ETag` or `If-None-Match`.
`gh api --cache` is never used, because duration-based caching would hide
transitions.

## Observation Model

Each poll produces one normalized `PullRequestObservation` per PR and role.
Only the diff engine creates events.

```text
role                   author | reviewer
repository, number, node_id, url, title, author_login, author_kind,
is_draft, state,
created_at, updated_at, head_ref, head_sha, base_ref, head_repository,
is_cross_repository, baseline_at

author role:
review_decision        APPROVED | CHANGES_REQUESTED | REVIEW_REQUIRED | none
opinionated_reviews    [{review_id, reviewer, actor_kind, state,
                         submitted_at}] | unknown
checks_state           passed | failed | pending | none | unknown
check_runs             [{id, name, status, conclusion, completed_at,
                         details_url}]
threads                [{thread_id, path, line, is_resolved, is_outdated,
                         depth, last_comment_id, last_comment_at,
                         last_responder, agent_replies, awaiting}]
                       | unknown
conversation           {last_human_comment_id, last_human_comment_at,
                        last_human_comment_url, last_user_comment_at}
mergeable              MERGEABLE | CONFLICTING | UNKNOWN
merge_state_status     raw value
ready                  true | false | unknown
timeline_since, timeline_cursor, detail_fetched_at

reviewer role:
review_request         {direct, teams, unresolved_team, incomplete?} | none
viewer_review          {id, state, submitted_at, commit} | none

both:
observed_at, verified_at, missing_polls, access   ok | unreachable
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
- Thread values are computed after dropping pending comments and comments by
  `ignore_actors`. `awaiting` on an unresolved thread is `user` when the last
  remaining comment's responder is `other`, otherwise `other`. Resolved
  threads have no `awaiting`.
- `last_responder` is the responder of the thread's last comment. `depth` is
  the thread's comment count. `agent_replies` counts comments attributed to
  `agent`.
- `verified_at` is the time of the last poll whose complete search returned
  this PR. A follow-up query alone does not advance it.
- `access` is `unreachable` while the follow-up returns null or `FORBIDDEN`.
- `baseline_at` is the `observed_at` of the poll that first saw the PR in the
  role. It survives observation resets because it is also stored on the
  `observed` event, together with the provider ids that baseline items need
  (see Opening evidence).
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
| `review_comment` | author | a new comment id by anyone other than the user appears in a thread or the conversation, or as a non-empty `COMMENTED` review body | `<comment_or_review_id>` | comment author | `createdAt` / `submittedAt` |
| `author_replied` | author | a new comment id by `identity_login` appears in a thread or the conversation | `<comment_id>` | `identity_login` | `createdAt` |
| `changes_requested` | author | a new review with state `CHANGES_REQUESTED` | `<review_id>` | reviewer | `submittedAt` |
| `approved` | author | a new review with state `APPROVED` | `<review_id>` | reviewer | `submittedAt` |
| `checks_failed` | author | `checks_state` becomes `failed` for a run set not yet reported on the head | `<head_sha>:<run_fingerprint>` | empty | latest context completion |
| `checks_passed` | author | `checks_state` becomes `passed` for a run set not yet reported on the head | `<head_sha>:<run_fingerprint>` | empty | latest context completion |
| `ready_to_merge` | author | `ready` becomes true (see rule 6) | `<head_sha>:<observed_at>` | empty | `observed_at` |
| `readiness_lost` | author | `ready` goes from true to false | `<head_sha>:<observed_at>` | empty | `observed_at` |
| `merge_conflict` | author | `mergeable` becomes `CONFLICTING` (see rule 6) | `<head_sha>:<observed_at>` | empty | `observed_at` |
| `review_requested` | reviewer | a request for the user (direct or through a team) appears that P did not have | `<observed_at>` | PR author | `observed_at` |
| `review_request_removed` | reviewer | the request for the user disappears while the PR stays open | `<observed_at>` | empty | `observed_at` |
| `review_submitted` | reviewer | `viewer_review` changes to a new submitted review | `<review_id>` | `identity_login` | `submittedAt` |
| `merged` | both | the PR left the open set and the follow-up shows merged | `<merged_at>` | `mergedBy.login` | `mergedAt` |
| `closed` | both | the PR left the open set and the follow-up shows closed and not merged | `<closed_at>` | empty | `closedAt` |
| `unwatched` | both | the watch stops watching an open PR in a role for any other reason (see rule 8) | `<role>:<observed_at>` | empty | `observed_at` |

The event id is
`github.pull_request.<suffix>:<node_id>[:<discriminator>]`. The PR's GraphQL
node id is stable across repository renames and transfers, so a rename
cannot re-emit captured occurrences. A PR is usually in only one role,
because GitHub does not request a review from the PR's author. A PR a bot
such as `copilot-swe-agent` opened on the user's behalf is the exception:
`author:@me` returns it and GitHub also requests the user's review, so it is
observed in both roles. Its `merged` and `closed` ids are the same in both
roles and are stored once. Rows are inserted with `INSERT ...
ON CONFLICT(id) DO NOTHING`. Transition events are immutable.

Other columns:

- `project`: `inferGitHubProject` (local remote, then repository name)
- `involvement_json`: author role `["authored"]`, which the query scope
  proves; reviewer role `["review_requested"]`.
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
  `[bot]`), or `team`. Observations record the PR author's kind the same way
  as `author_kind`.
- `actor_association` is GitHub's `authorAssociation` for comments and
  reviews. It is recorded as provider data. It means the author's relation to
  the repository's owner (for example `MEMBER` is organization membership),
  not repository write access. It is absent on events without a comment or
  review.
- `triage.category` is `review_feedback`, `ci`, `merge`, `review_request`,
  `user_reply`, or `lifecycle`.
- `triage.needs_user` is true for:
  - `review_comment` events whose thread or conversation is awaiting the user
  - `changes_requested`, `checks_failed`, `merge_conflict`, `ready_to_merge`,
    and `review_requested`
  - `observed` events whose state opens at least one attention item

  It is false otherwise. It is a deterministic hint, not a priority judgment.
  Surfaces that select on it also name the event types they accept.
- `triage.previous_responder` is the responder (`other`, `author`, or
  `agent`) of the comment immediately before this one in the same thread, or
  `null` for the first comment.
- `triage.has_suggestion` is true when the normalized body, before
  truncation, contains a line that is exactly a ```` ```suggestion ```` fence
  opener.
- `triage.failed_checks` (on `checks_failed`) lists up to 20
  `{name, conclusion, details_url}` entries, sorted by name.
- `triage.request` (on `review_requested` and reviewer `observed`) is
  `{direct, teams, unresolved_team}`. `triage.reason` (on
  `review_request_removed`) is `reviewed` when `viewer_review` changed since
  P, otherwise `withdrawn`.
- `triage.exit` (on `unwatched`) is `out_of_scope`, `left_search`, or
  `unreachable`.
- On `author_replied`, `triage.responder` is `author` or `agent`, and the
  payload has no `untrusted` body. The user's own text is not copied.
- `untrusted.origin` is `github.review_thread_comment`,
  `github.issue_comment`, or `github.review_body`.
- `observed` events carry the full current `state`, `baseline_at`, a
  `baseline` object with `head_sha`, `run_fingerprint`, `review_ids`,
  `threads` (entries with `thread_id` and `last_comment_id`), and
  `conversation.last_human_comment_id` (see Opening evidence), and, under
  `untrusted`, up to 20 open threads awaiting the user with path, line, URL,
  and last-comment body.

## State Diff And Dedupe Rules

The diff compares the previous observation P with the current observation C
for the same `node_id` and role.

1. **First sight and reset.**
   - A PR with no P and no stored `observed` event for the role emits exactly
     one `observed` event carrying its full current state and `baseline_at`.
     It emits no per-comment, per-review, or check events. Comments and
     reviews created at or before `baseline_at` are baseline.
   - A reviewer-role PR first seen after the role's first successful poll
     also emits `review_requested`, because a new request is a real
     transition, not backlog.
   - A PR with no P but a stored `observed` event for the role (an
     observation reset, scope change, or reconnect) does not re-baseline. It
     reuses the stored `baseline_at` and emits events for comments and
     reviews created after it that are not already stored.
   - When an author-role PR has a prior `closed` event, `reopened` is also
     emitted.
2. **Unchanged.** If C equals P in every dimension, no event is emitted and
   only `observed_at` and `verified_at` are refreshed.
3. **Unknown never transitions.** A dimension whose C value is `unknown`, or
   `mergeable: UNKNOWN`, is carried forward from P, emits nothing, and is
   reported as unknown, not as current, in attention items.
4. **Checks (author role).**
   - `pending` emits nothing. A failure is reported once the head's whole run
     set is terminal, not at the first failing job.
   - `run_fingerprint` is the first 12 hex characters of SHA-256 over the
     sorted `id@completedAt` values of the terminal CheckRuns and
     `context@createdAt` values of the terminal StatusContexts, so re-runs
     with new ids or a new completion time are new run sets.
   - A head emits a checks event when it reaches a terminal state whose
     fingerprint has not been reported for that head. Observing the same runs
     again cannot emit twice.
5. **Comments and reviews (author role).** Each comment or review id that is
   neither stored as an event nor baseline, and whose author is not in
   `ignore_actors`, emits one event, oldest first. Comments by
   `identity_login` emit `author_replied` instead of `review_comment`. Edits
   keep their id and emit nothing.
6. **Readiness and conflicts (author role).** `ready` is true only when every
   condition holds:
   - `state = OPEN` and `is_draft = false`
   - approval: `review_decision = APPROVED`, or `review_decision` is none,
     at least one `APPROVED` review exists, and no reviewer's latest
     opinionated review is `CHANGES_REQUESTED`
   - `checks_state` is `passed` or `none`
   - zero unresolved, non-outdated threads, which must be known
   - `mergeable = MERGEABLE`
   - `merge_state_status` is `CLEAN` or `HAS_HOOKS`. `BLOCKED`, `BEHIND`,
     `UNSTABLE`, `DIRTY`, and `DRAFT` are not ready; `UNKNOWN` makes `ready`
     unknown.

   Transitions compare C with the **last known** value, not with P's possibly
   unknown one. `ready_to_merge` is emitted when `ready` is true and the last
   known value was false or there was none since baseline; `readiness_lost`
   when it becomes false after true, with `triage.reasons` listing each failed
   condition (`draft`, `approval`, `checks`, `threads`, `conflicts`,
   `merge_state`). `merge_conflict` is emitted the same way when `mergeable`
   becomes `CONFLICTING`. A state already holding at first sight is covered by
   `observed` instead.
7. **Review requests (reviewer role).**
   - A request for the user appearing in C but not P emits
     `review_requested`. A re-request after the user reviewed is a new
     `review_requested`.
   - Requests through `ignore_teams` count as absent.
   - A new submitted `viewer_review` emits `review_submitted`.
8. **Leaving a role's set.** A PR in P but not in C is resolved through the
   follow-up query. For an author-role PR the detail query also runs first,
   so comments and reviews made just before a merge or close are captured.
   - Merged: emit `merged` and delete the observation.
   - Closed and not merged: emit `closed` and delete the observation.
   - Reviewer role, still `OPEN`, and the follow-up shows no request for the
     user: emit `review_request_removed` (`reviewed` or `withdrawn`) and
     delete the observation.
   - Still `OPEN` in an archived repository: emit `unwatched` with
     `exit: out_of_scope` and delete the observation.
   - Still `OPEN` and still matching the role (search lag): keep P, increment
     `missing_polls`, and emit nothing. After 6 consecutive missing polls,
     emit `unwatched` with `exit: left_search` and delete the observation.
   - Null or `FORBIDDEN`: keep P with `access: unreachable` and report
     `unreachable: <repo>#<n>` in connector status. After 6 consecutive
     unreachable polls, emit `unwatched` with `exit: unreachable` and delete
     the observation.
9. **Missed intermediate states.** Polling observes state, not history.
   Check and readiness states that come and go between polls are not seen.
   Comments and reviews are read by id, so they are not lost this way.
10. **Atomicity and concurrency.**
    - A poll commits in one SQLite transaction: the events, snapshot
      enrichment, and updated observations of every role whose set was
      complete, plus a `poll_seq` increment. A role whose search failed
      contributes nothing and keeps its previous observations. A database or
      diff error rolls back the whole transaction.
    - Before writing, the transaction checks that `github_pr_watch_state`
      still has the `poll_seq` and `scope_hash` the poll started with and is
      enabled. Otherwise the poll discards its work. It retries once only if
      the watch is still enabled with the same scope.

## Attention Items

The watch defines the GitHub attention items for `workgraph next`
(`specs/next.md`). Items are a **read-time projection** over the latest
observations and stored events. They store nothing and make no GitHub request.

| Reason | Role | Open while | Opening event | Item id |
|---|---|---|---|---|
| `reply` (thread) | author | an unresolved, non-outdated thread is `awaiting: user` | `review_comment` for the thread's last comment | `github:reply:<node_id>:<thread_id>:<last_comment_id>` |
| `reply` (conversation) | author | a human (`actor_kind: user`) issue comment is newer than the user's last conversation comment | that comment's `review_comment` | `github:reply:<node_id>:conversation:<comment_id>` |
| `review_requested` | reviewer | a request for the user is open | `review_requested` | `github:review_requested:<node_id>:<opened_by>` |
| `changes_requested` | author | a non-ignored reviewer's latest opinionated review is `CHANGES_REQUESTED` | `changes_requested` | `github:changes_requested:<node_id>:<review_id>` |
| `checks_failed` | author | `checks_state = failed` on the current head | `checks_failed` | `github:checks_failed:<node_id>:<head_sha>:<run_fingerprint>` |
| `conflicts` | author | `mergeable = CONFLICTING` | `merge_conflict` | `github:conflicts:<node_id>:<opened_by>` |
| `ready_to_merge` | author | `ready = true` | `ready_to_merge` | `github:ready_to_merge:<node_id>:<opened_by>` |

**Opening evidence.** An item's `opened_by` is its opening event when one is
stored. A condition that already held at baseline has no opening event, so
`opened_by` is the PR's `observed` event for that role and `since` is its
`baseline_at`. The `observed` event's `baseline` object stores `head_sha`
and `run_fingerprint` for checks, `review_ids` for reviews, `threads` entries
with `thread_id` and `last_comment_id`, and
`conversation.last_human_comment_id` for the latest human conversation
comment. Provider ids are matched recursively within `baseline`; checks use
`baseline.head_sha` and `baseline.run_fingerprint` explicitly. These values
let item ids and `opened_by` survive observation resets.

- Conversation `reply` items come only from issue comments. Review bodies do
  not open them. Their `subject.url` is the comment URL, from the opening
  event or, when a baseline opened the item, from
  `conversation.last_human_comment_url`. This applies to JSON, MCP, and
  watch output as well as text; it is a compatible addition, and a watch
  consumer sees one `changed` event when a stored URL first appears. A poll
  whose timeline includes the stored latest human comment fills its URL when
  the observation lacks one.
- Review threads from bots count; conversation comments from bots, such as
  coverage reports, do not.
- A thread an agent answered with the marker is `awaiting: other`, so it is
  not open.
- Draft PRs are included and marked `draft`.
- Dimensions that are `unknown` are shown as `unknown`, never as their
  carried-forward value.
- **Freshness.** Status-derived reasons (`review_requested`, `checks_failed`,
  `conflicts`, `ready_to_merge`) use the PR's `verified_at` and are stale
  after three intervals. Detail-derived reasons (`reply`,
  `changes_requested`) use `detail_fetched_at` and are stale after 30 minutes
  plus three intervals. Items of a PR with `access: unreachable` are stale.
- **Closing.** An item closes as `resolved` when its condition stops holding
  on a successful poll, `done` or `snoozed` when the user hides it, `merged`
  or `closed` from those events, and with the `exit` of an `unwatched` event
  otherwise. A withdrawn or reviewed request closes as `out_of_scope`. Only
  `resolved` claims the condition was addressed.

A new comment in a thread produces a new item id, so marking an item done
never hides later feedback.

## Polling, Push Hints, And Backoff

### Cadence

- One poll runs its role searches, merge and checks batches, and detail
  batches sequentially, one `gh` process each. On an account with about a
  hundred open PRs across both roles, the first poll after connecting or a
  scope change runs a dozen or more documents at a few seconds each, which
  exceeds the 30-second budget other connectors use. The watch's whole-poll
  budget is therefore 2 minutes. Roles completed before the budget runs out
  still commit (see Completeness), and the reduced batch and page sizes
  persist, so later polls are cheaper.

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
- A hint-triggered poll is an ordinary full poll that also forces the detail
  query for every author-role PR named by `--pr`, and for every author-role PR
  whose last detail fetch is older than 2 minutes when no hint named a PR.
- A hint never overrides the connector's not-before deadline.
- Hints are local only. A webhook receiver, `gh webhook forward`, a company
  event bus, or a cloud relay can call `workgraph github prs hint`. Receiving
  provider webhooks is out of scope here; the hint command is the integration
  point.

### Runtime extension

The current poller (`runConnectorPoller` in `run.go`) treats every poll as
success or failure and waits either the interval or the failure backoff. The
watch needs a small extension:

- The connector state gains one **not-before** deadline. No poll for the
  connector starts before it, whether triggered by the interval, a hint, or
  `github prs capture`. `capture` prints `rate limited until <time>` and
  exits non-zero without querying.
- A poll may return a **deferred** outcome with a reason and a not-before
  time. A deferral records no failure, leaves `consecutive_failures`
  unchanged, sets the deadline, and shows the reason in `connectors status`.
- A failed poll may carry a **retry-after** duration, which sets the deadline
  to now plus that duration.
- A poller may be **woken early** by a pending hint, subject to the 10-second
  spacing and the deadline.
- Connectors that use none of these behave exactly as today.

### Rate limits

- The watch reads the GraphQL bucket from each response's `rateLimit` and
  stores the last value in connector state.
- If the stored `remaining < 200` and its `resetAt` is in the future, the
  poll is **deferred** until `resetAt`.
- HTTP 200 with `errors[].type = RATE_LIMITED`, `HTTP 403` or `429`,
  `API rate limit exceeded`, or `secondary rate limit` is a failure with
  retry-after. The retry-after runs until the last known `resetAt` when it is
  in the future, otherwise 60 seconds. `gh api` does not expose response
  headers on errors, so `Retry-After` headers are not read. Observations are
  untouched.

### Failure modes

| Failure | Detection | Behavior |
|---|---|---|
| Rate limited | see above | defer or back off; nothing changes |
| Auth expired or revoked | `gh` exit with `HTTP 401`, `Bad credentials`, or `gh auth login` guidance | setup `error`, stop this poller, guidance `gh auth login` then `workgraph connectors validate github.pr_watch` |
| SSO not authorized | GraphQL `FORBIDDEN` / SAML | the role fails visibly with SSO guidance |
| GitHub query timeout | `gh` exit with `HTTP 502` or `504` | batches halve within the poll; a search timeout defers 60s and shows `GitHub timed out on the query` |
| Offline or DNS failure | `gh` network error | transient backoff (5s to 5m) |
| `gh` missing | exec error | setup `error` |
| Malformed JSON or schema drift | decode failure | the role fails; nothing changes for it |
| Completeness violated | see Completeness | the role fails; nothing changes for it |
| Team membership unreadable | missing `read:org` or `FORBIDDEN` on the teams query | warning in status; retain cached teams and refresh time, retry next poll; unmatched requests labeled `via team (unresolved)` |
| Whole-poll deadline (2m) | context deadline | transient |
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
  attach one Slack link or commit to dozens of events per PR.
- `author_replied` with `responder: author` and `review_submitted` are
  personal activity, so they may be association **targets**. Their related
  candidates are still only `observed` events and snapshot rows.
- Signals available on those rows without new rules: **commit** (`head_sha`
  matches a captured `git.commit`), **branch** (`branch` plus exact
  `repository`), and **URL** (a PR URL in a Slack message).
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
  - Each body is truncated to `comment_body_max_runes`, with
    `body_truncated`, `body_runes`, and `body_sha256` of the full normalized
    text. Diff hunks are capped at 4,000 runes.
  - Untrusted text never appears in `summary`, compact labels, attention item
    titles, project inference, association signals, or logs.
- **All provider strings are untrusted for agents.** Titles, branches, URLs,
  logins, team slugs, and paths are provider-controlled too. MCP and LLM
  consumers treat every payload string as data.
- **Logging.** Daemon logs record connector id, error class, and `gh` exit
  status, never response bodies.

## Known Limitations

These are accepted for the first version and can be revisited once the watch
is in use:

- **No follow-up after reviewing.** Once the user reviews a PR, its request
  is removed and the reviewer role stops watching it. Replies in the user's
  threads and new commits after a review do not open items. Authors usually
  re-request review, which reopens `review_requested`. A follow-up can add a
  bounded `reviewed-by:` search.
- **Threads over 100 comments** are `unknown` instead of paged.
- **Intermediate states between polls** are not seen (rule 9).
- **Dropped PRs** after 6 missing or unreachable polls close their items as
  `left_search` or `unreachable`, not `resolved`.

## Out Of Scope

- Any provider write by the watch.
- Dispatching attention items to agents (roadmap P2). `specs/next.md` defines
  the read-only feed that work can start from.
- Receiving provider webhooks directly. The hint command is the integration
  point.
- Bridged mode for the watch. The observation model is mode-agnostic, so it
  can be added later.
- `AB#` Azure Boards mentions as an association signal.
- Assigned PRs and issues, and GitHub Enterprise Server host selection.

## Open Questions

1. **Thread resolution and `updatedAt`.** Whether resolving a thread bumps
   the PR's `updatedAt` must be verified against the live API. The 30-minute
   reconciliation and named hints bound the delay if it does not.
2. **`timelineItems(since:)` semantics.** Whether `since` filters by creation
   or by update time must be verified. Either works with id dedupe.
3. **Team request semantics.** Whether a team request is removed when another
   team member reviews must be verified.
4. **Default `comment_body_max_runes`.** Should managed policy be able to
   lower or lock it?

## Delivery Slices

1. Runtime extension: deferred polls, retry-after, the not-before deadline,
   and early wake (fact 17).
2. Author role capture: connect, fencing, queries, observations, diff engine,
   events, reply attribution, snapshot enrichment, the untrusted payload,
   `today` personal-activity rules, and the `--type` wildcard (facts 1–16,
   18–23, 25–27).
3. Author role attention items in `workgraph next` and `resume` (facts 24, 28,
   and `specs/next.md`).
4. Reviewer role (facts 29–32).
5. Push hints (fact 35).

## Related `github` Connector Fixes

These defects in the shipped `github` connector were found while designing the
watch. They are independent of it and can land separately. Their placeholder
facts live in `facts/github_test.go`.

1. **Rate-limit pre-check masks failures.** `githubRateLimitAllowsPolling`
   returns false on any `gh api rate_limit` error, and the caller records
   that as a successful empty poll (`github.go`). Fix: a pre-check command
   failure fails the poll and passes the error to auth classification.
2. **`gh` auth errors are not classified.** `connectorAuthFailure`
   (`connector.go`) does not match `gh`'s `HTTP 401: Bad credentials` or
   `gh auth login` guidance. Fix: add those patterns. The watch relies on the
   same classification for its own auth failures (fact 15).
3. **Wrong rate-limit bucket.** `gh search` consumes the REST `search` bucket
   (30 requests per minute), but the pre-check reads `core` and requires
   `remaining >= 100`. Fix: read `resources.search`, with a threshold sized to
   the poll's searches plus 2.
4. **Bridged and direct GitHub identities diverge.** Bridged ingest hashes
   `<repo>#<kind>:<n>:<updated-at>` (`bridged_capture.go`), creating a new row
   for every update, and uses `repo`/`head` where direct uses
   `repository`/`branch`. Fix: normalize bridged PR and issue ingest onto the
   direct snapshot id and keys with the same newer-wins upsert.

## Facts

Facts live in `facts/github_pr_watch_test.go`. All use a fake `gh` script that
serves queued GraphQL fixtures and logs every invocation, following
`writeFakeGH`, and an injectable clock.

### Author role

1. **Connect validates gh and stays opt-in.** `github prs connect` logs
   `auth status` and a `viewer` probe and marks `github.pr_watch` ready.
   `github connect` alone leaves it disabled.
2. **First sight emits one `observed` per PR.** Two open authored PRs with
   existing reviews, comments, and failed checks yield exactly two `observed`
   events with full state, the `baseline` ids, and the awaiting-user backlog,
   and no other transition events.
3. **A reset neither duplicates nor loses.** After deleting observations, a
   comment created while observations were missing emits one
   `review_comment`, comments from before `baseline_at` emit nothing, and no
   second `observed` is stored.
4. **Unchanged state produces no new event** and advances `observed_at` and
   `verified_at`.
5. **Checks wait for the whole run set.** A head with one failed and one
   pending context emits nothing. When the pending context finishes, exactly
   one `checks_failed` is emitted. A new head that goes pending emits
   nothing.
6. **Check runs are keyed by execution.** Re-polling the same terminal runs
   emits nothing. A re-run with new run ids, or with the same id and a new
   `completedAt`, that finishes failed emits a second `checks_failed`.
7. **New reviews and comments emit by provider id**, oldest first, with
   `actor_kind`, `actor_association`, and `has_suggestion`. Three new
   comments in one thread produce three events.
8. **Timeline pagination catches up.** 150 conversation comments between
   polls produce 150 events in one poll. A backlog of 1,500 emits 1,000 on one
   poll with `timeline_cursor` stored and the dimension `unknown`, and the
   remaining 500 on the next poll with no other change.
9. **Ignored actors are removed before state; bots are captured.** A comment
   and a `CHANGES_REQUESTED` review by an `ignore_actors` login emit nothing,
   do not make the thread await the user, and open no item. A `Bot` comment
   emits `review_comment` with `actor_kind: "bot"`. A comment by
   `identity_login` emits `author_replied`.
10. **Reply attribution by marker and predecessor.** With
    `agent_reply_marker` set, a user comment ending in the marker emits
    `author_replied` with `responder: "agent"`, and a following reviewer
    comment has `previous_responder: "agent"`. A different account's comment
    ending in the marker is a `review_comment`.
11. **Pending draft comments are ignored.** A thread awaiting the user that
    gains a `PENDING` comment by the user still awaits the user and emits no
    `author_replied`; once the review is submitted, `author_replied` is
    emitted and the thread awaits `other`.
12. **Readiness and conflicts use the last known value.** Ready emits
    `ready_to_merge`; a new unresolved thread emits `readiness_lost` with
    `reasons: ["threads"]`; resolving it emits a second `ready_to_merge` on the
    same SHA. `BLOCKED` or `BEHIND` is not ready. After first sight with
    `mergeable: UNKNOWN`, a later `CONFLICTING` emits `merge_conflict`, and a
    later ready state emits `ready_to_merge`. Unknown values do not
    transition.
13. **Leaving the open set.** A PR merged right after a new comment emits the
    `review_comment` and `merged`. Closed emits `closed`. Archived emits
    `unwatched` with `exit: out_of_scope`. A PR still matching search is kept
    6 polls, then emits `unwatched` with `exit: left_search`. A `FORBIDDEN`
    PR's items are stale while other PRs are fresh, and after 6 polls it
    emits `unwatched` with `exit: unreachable`. A reopened PR emits
    `reopened`.
14. **Rate limits set one deadline.** A stored low budget defers the poll;
    a `RATE_LIMITED` error records a failure with the deadline at the last
    known `resetAt`. Either
    way observations are unchanged, and neither a hint nor `github prs
    capture` polls before the deadline.
15. **Auth expiry stops only this connector.**
16. **No token is ever persisted.**
17. **Deferred polls do not count as failures.** A deferral keeps
    `consecutive_failures` unchanged and sets the deadline. A connector that
    returns neither outcome keeps today's schedule.
18. **Comment text is stored with the untrusted flag.** A body with
    `Ignore previous instructions`, U+202E, an ANSI escape, a suggestion
    block, and more runes than the cap is stored only under
    `payload.untrusted`, with controls stripped, newlines and the suggestion
    block preserved, `body_truncated: true`, and `body_sha256`. The summary,
    event label, and attention item contain none of it. With
    `comment_body_max_runes: 0`, events carry no body text.
19. **An incomplete role fails without changes.** A role whose search is
    incomplete, has GraphQL errors, or is over its ceiling commits nothing and
    emits no `closed` events, while a complete role in the same poll commits.
20. **Read-only invocation and the type wildcard.** No `gh api graphql`
    invocation contains `mutation`, and `events today --type
    github.pull_request.*` lists transitions but not snapshots or issues.
21. **Snapshot enrichment is order-independent.** Watch-then-participant and
    participant-then-watch both leave one snapshot row with `branch`,
    `commit`, the original `matched_scopes`, and the participant `timestamp`.
22. **Associations use observed events only.** `head_sha` on an `observed`
    event associates with a captured `git.commit`. Transition events are not
    candidates. A URL inside `untrusted` creates no association.
23. **Only the user's own PR actions are personal activity.** With
    `observed`, `review_comment`, `checks_failed`, `ready_to_merge`, an agent
    `author_replied`, and an author `author_replied` stored today, `today`
    counts only the author reply. `events today` lists all of them.
24. **Resume relevant updates include only needs-user transitions.** After a
    check-in, a `review_comment` awaiting the user and a `checks_failed`
    appear under `Relevant updates since last check-in`; an `observed` with
    `needs_user: true`, a `checks_passed`, and an `author_replied` do not.
25. **Polls are fenced.** A `github prs capture` and a daemon poll that see
    the same new comment store one `review_comment`. A poll in flight when
    `github prs disconnect`, `connectors disable github.pr_watch`, or a scope
    change runs commits nothing and leaves no observations behind.
26. **Fencing recovers from a partial write.** When `connectors.json` shows
    the watch disabled but `github_pr_watch_state` is enabled, the next daemon
    start disables it, deletes observations, and increments `poll_seq`.
27. **Long threads are unknown, not wrong.** A thread with 101 comments marks
    thread values `unknown` and opens no `reply` item from carried state.
28. **Backlog items cite the observed event.** On first sight, a PR with
    failed checks, changes requested, a conflict, and an awaiting thread opens
    four items whose `opened_by` is the `observed` event and whose `since` is
    `baseline_at`. After an observation reset the item ids and `opened_by` are
    unchanged.

### Reviewer role

29. **Direct and team requests enter the reviewer set.** With
    `direct_and_team`, a direct request and a request through a cached team
    produce reviewer observations labeled `direct` and `via org/team`. A team
    missing from the cache is labeled `via team (unresolved)`. With `direct`,
    the team-only PR is absent. No detail query runs for reviewer-role PRs.
30. **First connect is backlog; later requests are transitions.** The first
    reviewer poll emits only `observed` per PR. A request appearing on a later
    poll emits `observed` and `review_requested`.
31. **Requests close at once.** After the user submits a review, the poll
    emits `review_submitted` and `review_request_removed` with
    `reason: "reviewed"`, and the item closes `out_of_scope`. A request
    withdrawn before any review emits `reason: "withdrawn"`. A `PENDING`
    draft review emits nothing. A re-request emits a new `review_requested`.
32. **Ignored teams.** A PR requested only through an `ignore_teams` team is
    absent.
33. **Bot authors are recorded and can be ignored.** A Copilot-style PR
    authored by a `Bot` and returned by both searches is observed in both
    roles with `author_login` and `author_kind: "bot"`; `next` shows it once,
    labeled `copilot-swe-agent, on your behalf`, with the reply and the
    review request under it. A Dependabot-style review request is tagged
    `[bot]`, and is absent with `ignore_authors: ["bots"]` or
    `["app/dependabot"]`. `ignore_repositories` removes PRs from both roles,
    and `ignore_authors` does not remove the user's own PRs. Malformed
    entries are rejected at connect. (`facts/github_pr_watch_reviewer_test.go`)
34. **The poll budget fits the work.** With the general connector poll
    deadline shorter than one `gh` call, a `github.pr_watch` poll still
    completes and records no error. (`facts/github_pr_watch_author_test.go`)

### Push hints

35. **A hint wakes the poller within seconds.** With a 5-minute interval, a
    hint recorded 30 seconds after a poll triggers a poll at the next 2-second
    check, and a second hint during that poll triggers exactly one follow-up.
    A hint within 10 seconds of the previous poll start waits until 10 seconds
    have passed. A hint naming a PR whose thread was resolved without an
    `updatedAt` change forces its detail query, so the `reply` item closes on
    that poll. A hint before the not-before deadline waits for it.
