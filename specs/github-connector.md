# GitHub Authored Pull Request Watch

Status: proposed. No behavior described here is implemented until its facts in
`facts/github_authored_prs_test.go` and `facts/github_test.go` are active.

## Intent

workgraph already captures GitHub pull requests and issues the user is involved
in (`specs/github.md`). That connector stores one mutable snapshot row per PR,
which answers "what did I touch?" but not "what changed on my PRs while I was
away?".

This spec adds a read-only watch over the user's **open authored pull
requests**. Each poll observes the complete open set, diffs it against the
previous local observation, and appends immutable transition events when
review, comment, check, merge-readiness, or lifecycle state changes.

The purpose is **triage**. Every transition event carries enough structured,
deterministic context for a downstream consumer to decide whether and where to
route it without an LLM: the comment, the thread, the file and line, the failed
checks, and who is waiting on whom. That consumer could be the user, `today`,
`resume`, or an orchestration agent (see Downstream Triage And Routing). The
watch itself only observes.

Capture is deterministic. It requires no LLM and makes no provider writes. A
failed poll changes nothing locally.

```text
gh api graphql (status query, then detail query for changed PRs)
  -> observation -> diff with previous observation
  -> transition events + new observation (one SQLite transaction)
  -> today / events today / resume / associations / triage consumers
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
- the watch can have its own polling interval
- either can be disabled independently with `workgraph connectors disable`

### Snapshot enrichment

GitHub search does not return branch or head commit, so `github.pull_request`
snapshot rows captured by `github` lack them, and branch and commit
associations cannot fire. The watch fills that gap:

- When the watch observes a PR whose snapshot row exists, it sets `branch`
  and `commit` (head SHA) in that row's payload with `json_set`.
- Every other payload field, including `matched_scopes`, and the row's
  `timestamp` are left unchanged, so the snapshot's newer-wins rule is not
  disturbed.
- It never creates a snapshot row. Snapshot existence remains the `github`
  connector's decision.
- When the head SHA changes, the enriched values change with it.

`specs/github.md` must be amended: branch and commit are populated for
exported events *and* for PRs observed by `github.authored_prs`.

## Auth Decision

**Direct mode reuses the authenticated `gh` CLI. Bridged mode reuses an
approved client's GitHub MCP. workgraph stores no PAT and never reads a
provider token in either mode.**

Justification:

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
- Bridged mode follows `specs/bridged-capture.md`. workgraph never receives
  the token.
- A workgraph-owned OAuth app or stored PAT would add token storage, refresh,
  and revocation work, and would duplicate what `gh` already does. Both are
  rejected.

## Setup

```text
workgraph github prs connect [--params-json '<scope-json>']
workgraph github prs capture
workgraph github prs disconnect
workgraph connectors connect github.authored_prs --mode bridged --params-json '<scope-json>'
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
- `ignore_actors` is an explicit login list whose comments, reviews, and
  review requests never produce events. Use it to silence specific bots or
  the user's own automation accounts. Bots are **not** ignored by default.
  Review-bot feedback is captured, and every event records `actor_kind` so
  consumers can treat bots differently.
- `comment_body_max_runes` caps each stored comment or review body, in the
  range `0..65536`. `65536` is GitHub's own comment limit. `0` stores no
  comment text; events then carry only structure and links.
- `agent_reply_marker` is the exact attribution line that agents acting as
  the user append to their comments, for example
  `[Robots on behalf of Stringfellow]`. The user's own comments whose last
  non-empty line equals the marker are attributed to `agent`; all other
  comments by `identity_login` are attributed to `author`. Empty disables
  the distinction. The marker distinguishes only between the user's own
  human and agent comments. It cannot make another account's comment count
  as the user's.
- `max_open_prs` is the completeness ceiling.

Changing approved scope cancels pending bridged requests and resets the
observation baseline.

## Query Shape

Each poll uses a **status query** for the whole open set and a **detail
query** only for PRs that changed. Nesting comment connections under every
thread of every PR in one document multiplies GitHub's calculated cost: 50
PRs × 100 threads alone is about 5,000 connection requests, roughly 50 points
per poll. At a 5-minute interval that would still consume most of the hourly
budget.

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
        headRefName headRefOid baseRefName
        reviewDecision mergeable mergeStateStatus
        reviewRequests(first: 20) {
          totalCount
          nodes { requestedReviewer {
            __typename
            ... on User { login }
            ... on Bot { login }
            ... on Team { combinedSlug }
          } }
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

- `$q` is `is:pr is:open author:<identity> archived:false sort:updated-desc`.
- The status query costs about 150 connection requests per 50 PRs (about 2
  points). Check status changes do not bump `updatedAt`, so the rollup must be
  read here on every poll.

### Detail query (changed PRs only)

```graphql
query($ids: [ID!]!, $since: DateTime) {
  rateLimit { cost remaining resetAt }
  nodes(ids: $ids) {
    ... on PullRequest {
      id body
      latestOpinionatedReviews(first: 20) { nodes { id state author { __typename login } } }
      timelineItems(since: $since, first: 100,
                    itemTypes: [ISSUE_COMMENT, PULL_REQUEST_REVIEW]) {
        totalCount
        pageInfo { hasNextPage }
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
}
```

- The detail query runs for a PR when:
  - it has no previous observation
  - its `updatedAt` differs from the previous observation
  - its last detail fetch is older than 30 minutes (reconciliation for
    changes that may not bump `updatedAt`, such as thread resolution)
- At most 10 PRs are batched per detail query, at about 1–2 points per PR.
- `$since` is the oldest `detail_fetched_at` in the batch, minus 5 minutes
  for clock skew. It is null for a PR with no previous observation. Fetching
  conversation comments and reviews by time instead of by `last: N` means a
  burst of more than 20 comments between polls is not lost. Overlap is
  harmless because the diff deduplicates by provider id. `hasNextPage`
  true marks the timeline dimension `unknown` for that poll.
- PRs that leave the open set are resolved by a follow-up
  `nodes(ids: [...])` query requesting `state merged mergedAt closedAt
  mergedBy { login } repository { isArchived }`.
- The query text is passed to `gh api graphql` through a file or stdin, never
  as a full argument string. Only `query` documents are allowed; any document
  containing `mutation` is rejected before execution.
- Each response's `rateLimit.cost` is recorded in connector state, so real
  cost is inspectable.

### Conditional requests

GitHub GraphQL is `POST` and does not support `ETag` or `If-None-Match`.
Cost is controlled by the status/detail split and the polling interval.
`gh api --cache` is never used, because duration-based caching would hide
transitions.

## Observation Model

Each poll produces one normalized `PullRequestObservation` per PR. Direct and
bridged modes build the same structure, and only the diff engine creates
events.

```text
repository, number, node_id, url, title, is_draft, state, created_at, updated_at,
head_ref, head_sha, base_ref,
review_decision        APPROVED | CHANGES_REQUESTED | REVIEW_REQUIRED | none
requested_reviewers    sorted logins / team slugs, excluding ignore_actors
reviews                [{review_id, reviewer, actor_kind, state, commit_sha, submitted_at}]
checks_state           passed | failed | pending | none | unknown
check_runs             [{id, name, conclusion, details_url}]
threads                [{thread_id, path, line, is_resolved, resolved_by, is_outdated,
                         seen_comment_ids, depth, last_author,
                         last_responder, agent_replies, awaiting}]
conversation           {seen_comment_ids, last_author, last_responder}
mergeable              MERGEABLE | CONFLICTING | UNKNOWN
merge_state_status     raw value
ready                  true | false | unknown
detail_fetched_at, observed_at
```

Mappings:

- `checks_state`: rollup `SUCCESS` maps to `passed`, `FAILURE` or `ERROR` to
  `failed`, `PENDING` or `EXPECTED` to `pending`, and a null rollup to
  `none`. More than 100 contexts makes it `unknown`.
- `awaiting`: `author` when the thread's last comment is not by
  `identity_login`, otherwise `reviewer`. Resolved threads have no
  `awaiting`.
- `last_responder`: `reviewer` when the last comment is not by
  `identity_login`, otherwise `author` or `agent` by `agent_reply_marker`.
- `depth` is the thread's comment `totalCount`. `agent_replies` counts
  comments attributed to `agent`.
- More than 100 threads makes thread-derived values `unknown`.

Observations live in a connector-owned checkpoint table:

```text
github_pr_observations(
  connector_id, node_id, repository, number,
  observation_json, observed_at,
  PRIMARY KEY (connector_id, node_id))
```

This is provider checkpoint state, not a source of truth. Deleting it causes a
re-bootstrap. Event ids are provider-occurrence based, so re-bootstrap does
not duplicate events.

## Event Schema

Events use source `github` and type prefix `github.pull_request.`.

| Type | Emitted when | Discriminator | `actor` | `timestamp` |
|---|---|---|---|---|
| `observed` | a PR is first seen by the watch | `<created_at>` | PR author | `createdAt` on first connect, else `observed_at` |
| `reopened` | a PR with a prior `closed` event re-enters the open set | `<last closed_at>` | empty | `observed_at` |
| `review_requested` | a reviewer is newly present in `requested_reviewers` | `<reviewer>:<head_sha>` | requested reviewer | `observed_at` |
| `review_comment` | a new comment id appears in a thread, in the conversation, or as a non-empty `COMMENTED` review body, by anyone other than self | `<comment_or_review_id>` | comment author | `createdAt` / `submittedAt` |
| `author_replied` | a new comment id by `identity_login` appears in a thread or the conversation | `<comment_id>` | `identity_login` | `createdAt` |
| `changes_requested` | a new review with state `CHANGES_REQUESTED` | `<review_id>` | reviewer | `submittedAt` |
| `approved` | a new review with state `APPROVED` | `<review_id>` | reviewer | `submittedAt` |
| `checks_failed` | the rollup becomes `failed` for the current head | `<head_sha>:<run_fingerprint>` | empty | latest context completion, else `observed_at` |
| `checks_passed` | the rollup becomes `passed` for the current head | `<head_sha>:<run_fingerprint>` | empty | same |
| `ready_to_merge` | `ready` becomes true | `<head_sha>:r<epoch>` | empty | `observed_at` |
| `readiness_lost` | `ready` goes from true to false | `<head_sha>:r<epoch>` | empty | `observed_at` |
| `merged` | the PR left the open set and the follow-up shows merged | `<merged_at>` | `mergedBy.login` | `mergedAt` |
| `closed` | the PR left the open set and the follow-up shows closed and not merged | `<closed_at>` | empty | `closedAt` |

All type names in the table are suffixes of `github.pull_request.`.

The event id is `github.pull_request.<suffix>:<repository>#<number>:<discriminator>`.
Rows are inserted with `INSERT ... ON CONFLICT(id) DO NOTHING`. Transition
events are immutable.

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
  "url": "https://github.com/example-org/workgraph-demo/pull/42",
  "title": "AB#1234 Add settings helper",
  "branch": "feature/settings-helper",
  "head_sha": "1111111111111111111111111111111111111111",
  "actor_kind": "user",
  "actor_association": "MEMBER",
  "transition": {"dimension": "comment", "from": null, "to": "new"},
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
    "last_responder": "reviewer",
    "agent_replies_in_thread": 0,
    "has_suggestion": true,
    "failed_checks": []
  },
  "referenced_urls": ["https://dev.azure.com/example-org/Demo/_workitems/edit/1234"],
  "work_item_refs": ["AB#1234"],
  "observed_at": "2026-10-16T17:02:11Z",
  "capture_mode": "direct",
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
  reviews (`OWNER`, `MEMBER`, `COLLABORATOR`, `CONTRIBUTOR`,
  `FIRST_TIME_CONTRIBUTOR`, `NONE`, ...). It is absent on events without a
  comment or review.
- `triage.category` is `review_feedback`, `review_request`, `ci`, `merge`,
  `author_reply`, or `lifecycle`.
- `triage.has_suggestion` is true when the stored body contains a line that
  is exactly a ```` ```suggestion ```` fence opener. It is computed from the
  normalized body before truncation.
- On `author_replied`, `triage.responder` is `author` or `agent`, and the
  payload has no `untrusted` body. The user's own text is not copied.
- `triage.needs_author` is true for:
  - `review_comment` events whose thread is awaiting the author
  - `changes_requested`
  - `checks_failed`
  - `ready_to_merge`
  - `observed` events whose state has `threads_awaiting_author > 0`, failed
    checks, or changes requested

  It is false otherwise. This flag is a deterministic routing hint, not a
  priority judgment.
- `triage.failed_checks` (on `checks_failed`) lists up to 20
  `{name, conclusion, details_url}` entries, sorted by name.
- `observed` events carry the full current `state` and, under `untrusted`, a
  `threads` array of up to 20 open threads awaiting the author, each with
  path, line, URL, and last-comment body. This lets a consumer triage a PR's
  existing backlog without historical comment events.
- `review_comment` events created when a poll found more than 20 unseen
  comments in one thread mark `triage.gap: true` on the earliest emitted
  comment.
- `branch` and `head_sha` use field names the association extractor already
  recognizes.

## State Diff And Dedupe Rules

The diff compares the previous observation P with the current observation C
for the same `node_id`.

1. **First sight.** A PR with no P emits exactly one `observed` event
   carrying its full current state. It emits no per-comment, per-review, or
   check events. Existing comment, review, and run identities become the
   baseline.
   - This applies on first connect (one `observed` per open PR, bounded by
     `max_open_prs`) and when a new PR is opened.
   - After an observation reset or scope change, the `observed` event id
     collides with the existing one, so nothing duplicates.
   - When the PR has a prior `closed` event, `reopened` is emitted instead.
2. **Unchanged.** If C equals P in every dimension, no event is emitted and
   only `observed_at` is refreshed.
3. **Unknown never transitions.** A dimension whose C value is `unknown`, or
   `mergeable: UNKNOWN`, is carried forward from P and emits nothing.
4. **New head commit.** When `head_sha` changes, checks for the new SHA start
   at whatever C reports, normally `pending`. `pending` emits nothing. The
   first terminal rollup emits one `checks_failed` or `checks_passed`. Review
   and comment events are not keyed by SHA, so a push cannot create them.
5. **Check runs and re-runs.** `run_fingerprint` is the first 12 hex
   characters of SHA-256 over the sorted CheckRun ids and
   `context@createdAt` values in the rollup.
   - Observing the same runs again cannot emit twice.
   - A re-run creates new run ids, so a re-run that fails again emits a new
     `checks_failed`.
   - Returning to an earlier SHA with already-seen runs does not duplicate.
6. **Comments and reviews.** Each comment or review id not in P's seen set
   emits one event, oldest first, unless its author is in `ignore_actors`.
   Comments by `identity_login` emit `author_replied` with `responder`
   `author` or `agent` instead of `review_comment`, and turn the thread's
   `awaiting` to `reviewer`. A consumer uses these to tell a thread the user
   answered personally from one an agent answered.
7. **Review requests.** A reviewer who is newly present in
   `requested_reviewers` emits `review_requested:<reviewer>:<head_sha>`.
8. **Readiness.** `ready` is true only when every condition holds:
   - `state = OPEN` and `is_draft = false`
   - approval: `review_decision = APPROVED`, or `review_decision` is none,
     at least one `APPROVED` review exists, and no reviewer's latest
     opinionated review is `CHANGES_REQUESTED`
   - `checks_state = passed`, or `checks_state = none` with
     `merge_state_status = CLEAN`
   - zero unresolved, non-outdated threads, which must be known
   - `mergeable = MERGEABLE`

   Rules for the events:
   - `ready` false to true emits `ready_to_merge`. True to false emits
     `readiness_lost`, with `triage.reasons` listing each failed condition
     (`draft`, `approval`, `checks`, `threads`, `conflicts`).
   - Unknown does not transition.
   - `epoch` is the number of `readiness_lost` events already stored for this
     PR and SHA. It is derived from events, so it survives an observation
     reset. Readiness that is lost and regained on the same SHA therefore emits
     `ready_to_merge` again, and repeated polls of the same state never
     duplicate.
9. **Leaving the open set.** A PR in P but not in C is resolved through the
   follow-up query:
   - Merged: emit `merged` and delete the observation.
   - Closed and not merged: emit `closed` and delete the observation.
   - Still `OPEN` in an archived repository: delete the observation silently.
   - Still `OPEN` otherwise (search lag): keep P and emit nothing.
   - Null or `FORBIDDEN` (lost access): keep P, emit nothing, and report
     `unreachable: <repo>#<n>` in connector status.

   The follow-up uses tracked node ids, so merges and closes during downtime
   are still resolved on the next successful poll.
10. **Missed intermediate states.** Polling observes state, not history.
    Check and readiness states that come and go between polls are not seen.
    Comments and reviews are not lost this way, because they are read by id,
    subject to the 20-per-thread window and `triage.gap`.
11. **Atomicity.** All events, the snapshot enrichment, and all updated
    observations for a poll commit in one transaction. Any failure rolls back
    everything.

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

## Polling And Backoff

### Cadence

The watch uses the existing fixed-interval poller with no runtime extension.

- The default interval is `5m`. `workgraph connectors interval
  github.authored_prs 15m` lengthens it. The minimum is `1m`, and managed
  policy minimums win.
- The daemon polls once at startup. After the machine sleeps, the next poll
  runs within one interval of waking, and the follow-up and timeline queries
  catch up on everything that happened meanwhile.
- `workgraph github prs capture` polls immediately.
- At a 5-minute interval a typical poll costs 2–20 GraphQL points, about
  25–250 points per hour out of 5,000.

Polling is the source of truth. Push hints from webhooks or a company event
bus may later trigger an early poll, but they never carry state.

### Rate limits

- The watch reads the GraphQL bucket from each response's `rateLimit`.
- If `remaining < 200`, the poll is **deferred**: no query, no events, and
  observations untouched. The next scheduled poll checks again, and status
  shows `rate limited until <resetAt>`. A deferral is not a failure.
- HTTP 200 with `errors[].type = RATE_LIMITED`, `HTTP 403` or `429`,
  `API rate limit exceeded`, `secondary rate limit`, or a `Retry-After`
  header is a transient failure. The next attempt waits for
  `max(backoff, Retry-After)`. Observations are untouched.

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
| Repository renamed or transferred | `nameWithOwner` changes under the same `node_id` | observation follows `node_id`; new event ids use the new name |

## Existing Connector Fixes (Slice 0)

These are defects in the shipped `github` connector. The watch depends on them
being fixed, so they come first, each with its own fact in
`facts/github_test.go`.

1. **Rate-limit pre-check masks failures.** `githubRateLimitAllowsPolling`
   returns false on any `gh api rate_limit` error, and the caller records
   that as a successful empty poll. An expired login or a missing `gh`
   therefore captures nothing, silently and forever.
   - Fix: a pre-check command failure fails the poll and passes the error to
     auth classification. Only a successfully read low budget is a deferral.
2. **`gh` auth errors are not classified.** `connectorAuthFailure` matches
   `status 401`, `unauthenticated`, and `invalid credentials`, but not `gh`'s
   `HTTP 401: Bad credentials` or `gh auth login` guidance.
   - Fix: add those patterns, so both GitHub connectors move to setup
     `error` instead of retrying indefinitely.
3. **Wrong rate-limit bucket.** `gh search` consumes the REST `search` bucket
   (30 requests per minute), but the pre-check reads `core`.
   - Fix: check `resources.search` for `github` and `graphql` for the watch.
4. **Bridged and direct GitHub identities diverge.** Direct capture writes
   `github.pull_request:<repo>:<n>` and refreshes it when newer. Bridged
   ingest hashes `external_id` `<repo>#<kind>:<n>:<updated-at>`
   (`bridged_capture.go`), which creates a new row for every update. The
   bridged reference payload also uses `repo` and `head` where direct uses
   `repository` and `branch`.
   - Effect: bridged users see one duplicate PR row per revision in `today`,
     mode switches double-count, and association extraction misses the
     repository.
   - Fix: bridged `github.pull_request` and `github.issue` ingest
     normalizes onto the direct snapshot id and payload keys with the same
     newer-wins upsert.
   - This amends the identity table in `specs/bridged-capture.md`. Existing
     duplicate rows are left in place and are not rewritten.

## Bridged Mode

- The registry entry declares `Bridged: true` and
  `CaptureSemantics: "complete_snapshot"`.
- Required logical tools:
  - `github.search_pull_requests`: fetch the complete open authored set with
    a total count
  - `github.read_pull_request_status`: fetch and identity for head SHA,
    review decision, and check runs with ids
  - `github.read_pull_request_reviews`: fetch and identity for reviews,
    review threads, and comments with ids
- The worker submits one raw observation item per PR, plus a resolution for
  each PR it was asked to resolve. workgraph runs the same normalizer and diff
  engine. The worker never constructs transition events.
- A missing total count is treated as incomplete.
- The reference GitHub MCP is REST-shaped and needs several calls per PR, so
  bridged mode is a later slice. The contract is fixed now so the diff engine
  is mode-agnostic.

## today, events today, resume

- `workgraph today` renders transition events with templated labels, for
  example `Add settings helper (#42): checks failed on 1111111 (lint)`.
  Comment text never reaches the compact view.
- `workgraph events today --type github.pull_request.*` lists only transition
  events. `--type` gains a trailing `.*` wildcard meaning "the type starts with
  this prefix and the next character is a dot". Exact matching is unchanged.
- The detailed `events today` row for a `review_comment` shows the first line
  of the body behind an explicit marker,
  `untrusted comment (alice, bot): "…"`.
- `workgraph resume <project>` gains a `Pull requests` section built from
  observations and transition events, for example
  `#42 Add settings helper — changes requested by alice; checks failed on 1111111; 1 thread awaiting you`.
  It makes no provider calls.
- Resume relevance treats `github.pull_request.*` events with `authored`
  involvement as user-work evidence. Their `actor` is usually a reviewer, so
  the existing rule that requires actor to equal the login would discard them.

## Associations

Associations follow `specs/event-associations.md`: deterministic, local, no
fuzzy matching, and no time-only matches.

Signals available without spec changes:

- **Commit**: `head_sha` matches captured `git.commit` events (85 points).
  Snapshot enrichment gives `github.pull_request` rows the same signal.
- **Branch**: `branch` plus exact `repository` (65 points).
- **URL**: a PR URL in a Slack message matches the PR `url`. Slack and Azure
  Boards URLs in the PR body are stored in `referenced_urls` and match a
  Slack `permalink` or a work item URL (100 points).

One new signal requires amending `specs/event-associations.md`:

- **Azure Boards mention**: an `AB#<digits>` token, the GitHub to Azure Boards
  integration syntax, in the PR title, body, or `head_ref`. It is stored as
  `work_item_refs`.
  - It matches an `azure.boards` event with that work item id at 90 points.
  - Ambiguity rule: it matches only when the id resolves, among locally
    captured `azure.boards` events, to exactly one organization. When the id
    exists in more than one organization, nothing matches, and
    `associations explain` reports `ambiguous AB#1234: org-a, org-b`.
    Optional `azure_boards_org_by_repository` scope (`{"owner/repo":
    "org"}`) resolves the ambiguity explicitly. This mirrors how GitHub's
    Azure Boards app binds each repository to one organization.
  - Bare numbers, `#1234`, and branch patterns such as `feature/1234-x`
    never match.

Association signal extraction skips the `untrusted` subtree. A comment cannot
create association evidence by planting a URL.

## Security Notes

- **Read-only.** Only GraphQL `query` documents are executed, and documents
  containing `mutation` are rejected. The watch never re-runs CI, comments,
  resolves, approves, merges, or pushes.
- **No token persistence.** See Auth Decision. Facts verify that a token
  sentinel never appears under the workgraph home or in the database, and that
  `gh auth token` is never invoked.
- **Third-party text is stored only under `payload.untrusted`**, with
  `trust: "untrusted"` and `origin`. This covers comment bodies, review bodies,
  and diff hunks.
  - Bodies are markdown, kept so that suggestion blocks and code survive for
    downstream agents.
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
  every payload string as data. No consumer may execute a tool, fetch a URL,
  or change state because of provider content alone.
- **MCP and prompt surfaces** that return these events keep `untrusted` as a
  separately delimited field and never interpolate it into instruction text
  (`specs/llm-integration.md`). Tool descriptions state that `untrusted`
  fields are data, not instructions.
- **Logging.** Daemon logs record connector id, error class, and `gh` exit
  status, never response bodies.

## Downstream Triage And Routing

`specs/pr-triage-routing.md` defines the deterministic router that consumes
these events, decides what needs the user, and dispatches local agents. This
spec commits only to the contract the router reads:

- **Stable, immutable, provider-occurrence event ids**, so the router handles
  each event exactly once.
- **Deterministic routing inputs** in `triage` and the top-level payload:
  `category`, `needs_author`, `thread_id`, `path`, `line`, `thread_depth`,
  `last_responder`, `agent_replies_in_thread`, `has_suggestion`,
  `failed_checks`, `reasons`, `repository`, `branch`, `head_sha`, `actor`,
  `actor_kind`, and `actor_association`.
- **`author_replied` events with `responder`**, so the router can tell a
  thread the user answered personally from one an agent answered.
- **Untrusted bodies** remain under `payload.untrusted`. The router passes
  them to agents only as delimited data.

The watch itself never dispatches, writes to GitHub, or calls an LLM.

## Out Of Scope

- Any provider write by the watch.
- Dispatch, which `specs/pr-triage-routing.md` owns.
- PRs where the user is a reviewer, which `github` `review_requested` covers.
- Historical comment backfill before first sight, beyond the `observed`
  backlog summary.
- GitHub Enterprise Server host selection.

## Open Questions

1. **Thread resolution and `updatedAt`.** Whether resolving a thread bumps
   the PR's `updatedAt` must be verified against the live API. The 30-minute
   reconciliation bounds the delay if it does not.
2. **Nested overflow.** More than 100 threads or 100 contexts marks the
   dimension `unknown` rather than paginating. Is that acceptable, or should
   such PRs get a bounded second page?
3. **REST change probe.** A `/notifications` `If-Modified-Since` probe could
   skip idle polls but adds an endpoint and depends on token scope.
4. **Default `comment_body_max_runes`.** `8000` keeps nearly all review
   comments whole for agents, at the cost of storing third-party text locally.
   Should managed policy be able to lower or lock it?

## Delivery Slices

0. Existing connector fixes (facts G1–G4).
1. Direct capture: connect, queries, observations, diff engine, events,
   reply attribution, snapshot enrichment, the untrusted payload, `today`
   labels, and the `--type` wildcard (facts 1–18, 20, 22, 23).
2. The `AB#` association signal and the untrusted-subtree exclusion (fact 19).
3. Resume `Pull requests` section (fact 21).
4. Bridged recipe.

Routing is delivered separately under `specs/pr-triage-routing.md` and
depends on slice 1.

## Facts

Facts for the watch live in `facts/github_authored_prs_test.go`. Slice-0 facts
live in `facts/github_test.go`. All use a fake `gh` script that serves queued
GraphQL fixtures and logs every invocation, following `writeFakeGH`.

### Slice 0 — existing connector

- **G1. Rate-limit pre-check failure is a visible poll failure.** When the
  fake `gh api rate_limit` exits non-zero, the `github` poll records
  `last_error` and increments `consecutive_failures`, rather than recording a
  successful empty poll.
- **G2. `gh` bad credentials is an auth failure.** When `gh` exits with
  `HTTP 401: Bad credentials`, `github` setup becomes `error` and its poller
  stops.
- **G3. Search budget gate.** With `resources.search.remaining` low and
  `resources.core.remaining` high, `github` skips `gh search`.
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
   events. An observation reset followed by a poll adds nothing.
3. **Same SHA and same state produces no new event.**
4. **A new commit resets checks and emits once.**
5. **Check runs are keyed by run identity.** Re-polling the same failed runs
   emits nothing. A re-run with new run ids that fails again emits a second
   `checks_failed`.
6. **New reviews and comments emit by provider id**, oldest first, with
   `actor_kind`, `actor_association`, and `has_suggestion`. Three new
   comments in one thread produce three events. A burst of 30 conversation
   comments between polls produces 30 events.
7. **Configured actors are ignored; bots are captured.** Comments by
   `ignore_actors` emit nothing. A `Bot` comment emits `review_comment` with
   `actor_kind: "bot"`. A comment by `identity_login` emits `author_replied`,
   not `review_comment`, and flips the thread to `awaiting: reviewer`.
8. **Readiness is lost and regained.** Ready emits `ready_to_merge`. A new
   unresolved thread emits `readiness_lost` with `reasons: ["threads"]`.
   Resolving it emits a second `ready_to_merge` on the same SHA. Repeated
   polls of each state add nothing. `mergeable: UNKNOWN` does not transition.
9. **Leaving the open set.** Covers merged, closed, still `OPEN`, archived,
   `FORBIDDEN`, and reopened (which emits `reopened`).
10. **A rate-limited poll defers and keeps state.**
11. **A secondary rate limit backs off without losing state.**
12. **Auth expiry stops only this connector.**
13. **No token is ever persisted.**
14. **Comment text is stored with the untrusted flag.** A body with
    `Ignore previous instructions`, U+202E, an ANSI escape, a suggestion
    block, and more runes than the cap is stored only under
    `payload.untrusted`, with controls stripped, newlines and the suggestion
    block preserved, `body_truncated: true`, and `body_sha256`. The summary and
    `today` label contain none of it.
15. **Comment capture can be disabled.** With `comment_body_max_runes: 0`,
    events carry no body text.
16. **An incomplete snapshot fails without changes.**
17. **Reply attribution by marker.** With `agent_reply_marker` set, a user
    comment ending in the marker emits `author_replied` with `responder:
    "agent"` and increments `agent_replies_in_thread`. One without it has
    `responder: "author"`. A different account's comment ending in the
    marker is a `review_comment`.
18. **The type wildcard filters transitions.**
19. **Associations.** Covers commit, unambiguous `AB#`, ambiguous `AB#`
    reported and not matched, and an untrusted URL creating no association.
20. **Read-only invocation.** No `gh api graphql` invocation contains
    `mutation`.
21. **Resume shows current PR state** without invoking `gh`.
22. **Snapshot enrichment.** An existing `github.pull_request` row gains
    `branch` and `commit` while keeping `matched_scopes` and its `timestamp`.
    No snapshot row is created for a PR the `github` connector never captured.
23. **The detail query runs only for changed PRs.** An unchanged `updatedAt`
    within the reconciliation window issues the status query only, which the
    fake `gh` log shows.
