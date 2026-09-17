# GitHub Integration

workgraph should ingest GitHub activity as cloud-side work events that connect
back to local projects when possible.

GitHub ingestion should start with explicit capture from a local exported event
file:

```text
workgraph github capture --events-file github-events.json
```

This gives workgraph a deterministic ingestion seam before adding network
authentication and API pagination. A later provider can fetch the same event
shape from GitHub directly.

`workgraph start` should also poll GitHub activity through the authenticated
GitHub CLI (`gh`) when it is available. GitHub polling captures activity for
the connected person rather than fanning out to every locally cloned
repository, so it stays bounded regardless of how many repositories are
watched and covers work in repositories that are not cloned locally.

## Capture scope

GitHub capture uses one canonical, participant-scoped capture scope shared by
direct and bridged capture:

```json
{
  "scope": "participant",
  "identity": "@me",
  "include": ["involves", "review_requested"],
  "always_repositories": [],
  "bootstrap_lookback": "168h"
}
```

- `scope` must be `"participant"`.
- `identity` is the GitHub search identity, typically `@me`.
- `include` selects which base searches run: `involves` (PRs and issues the
  identity is involved in) and `review_requested` (PRs requesting the
  identity's review).
- `always_repositories` is an optional `owner/repo` array of repositories to
  watch regardless of participation, batched into at most one PR search and
  one issue search rather than one search pair per repository.
- `bootstrap_lookback` is a positive duration string (default `168h`) used to
  bound the first capture window before a cursor exists.

`workgraph github connect [--params-json '<scope-json>']` validates `gh auth
status`, enables the `github` connector, and approves this scope, defaulting
to `{"scope":"participant","identity":"@me","include":["involves","review_requested"],"always_repositories":[],"bootstrap_lookback":"168h"}`
for a new connection. The same scope is reused verbatim when a bridged
capture request is emitted, so direct and bridged capture never drift apart.
Existing repository-only bridged configurations (`{"repositories": [...]}`)
are not automatically translated into participant scope, since that would
silently broaden capture; they must be explicitly reconnected with
`workgraph github connect` or `workgraph connectors connect github --mode
bridged --params-json ...`. Approving a changed scope resets the stored
capture cursor so the new scope receives a fresh bootstrap window; switching
between direct and bridged capture modes without changing scope preserves the
existing cursor.

## Capture window and cursor

Each poll captures `until` at the start of the run and derives `since` as
follows:

- On first capture (no stored cursor), `since = until - bootstrap_lookback`.
- On subsequent captures, `since = completed_through - 5m` overlap, so that
  events updated near the previous boundary are not missed.

Three base searches run against `[since, until]`, sorted by updated time:
pull requests the identity is involved in, pull requests requesting the
identity's review, and issues the identity is involved in — only for the
`include` values that are configured. `always_repositories`, when configured,
adds at most one additional batched PR search and one batched issue search
covering all listed repositories.

Each search requests at most 1,000 results (GitHub's search ceiling). If a
query returns exactly 1,000 results, its time window is bisected and both
halves are retried; inclusive-boundary duplicates are deduplicated during
merge. If a minimum window still saturates the cap, the poll fails visibly
rather than silently truncating results.

Results across the base and always-watched searches are merged: the same
pull request or issue returned by more than one query is stored once, with
the union of matched query provenance recorded as `matched_scopes` (for
example `{"matched_scopes":["involves","review_requested"]}`). GitHub's
`involves:@me` qualifier cannot distinguish authoring from commenting from
mention without additional per-item reads, so workgraph records this honest
provenance rather than inventing precision; a later enrichment may derive
`authored`, `assigned`, `commented`, or `mentioned` if additional provider
reads are justified.

All matched events are stored and the cursor is advanced to `until` in one
SQLite transaction. A failed or truncated search (malformed output, command
failure, timeout, or cap exhaustion that cannot be bisected further) fails
the whole poll and leaves the previous cursor untouched, so the next poll
retries the same window.

GitHub connect validates `gh auth status`, enables the `github` connector in
`connectors.json`, and reports the shared connector controls. Manual `github
capture` remains available for imports and facts.

Local git capture does not require account connection, but it should appear in
the same connector status view as an enabled local source when file/git capture
is active. Local git remotes are still used to link captured GitHub activity
to a local project, but they are no longer used to discover which
repositories to query.

GitHub capture keeps one stored work snapshot per repository PR or issue
identity. Recapturing the same PR or issue with a newer GitHub `updated_at`
refreshes the stored timestamp, state, title, actor, and payload without
creating a duplicate row. Older snapshots must not replace newer GitHub state.

The first MVP ingests:

- pull requests
- issues

GitHub events should be stored in the existing event store:

- `source`: `github`
- `type`: examples include `github.pull_request`, `github.issue`, and `github.comment`
- `project`: local project/repository name when it can be inferred
- `actor`: GitHub login when available
- `summary`: human-readable title or short description
- `payload_json`: source-specific details such as URL, number, repository, state,
  matched query provenance, and timestamps; branch and commit SHA are only
  populated for manually exported events, since GitHub's search API does not
  return them

Local linking rules:

1. Prefer a local git repository whose remote matches the GitHub repository.
2. If a commit SHA is present, link to a local project that has captured the same git commit.
3. Fall back to the GitHub repository name.

Ingestion must not require automatic actions. Drafting replies, comments, or PR
updates remains future work and must follow suggest -> draft -> approve -> act.

