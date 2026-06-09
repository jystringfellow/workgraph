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
GitHub CLI (`gh`) when it is available. The daemon must be conservative:

- discover GitHub repositories from configured local git remotes
- check GitHub rate-limit state before querying repository activity
- skip GitHub polling when the remaining request budget is low
- query a bounded number of repositories per poll
- use a low-frequency polling interval by default
- store events through the same ingestion path as exported events
- avoid duplicate events across polling cycles

GitHub polling should participate in the shared connector runtime. workgraph can
infer GitHub from local git remotes and the authenticated `gh` CLI:

```text
workgraph github connect
```

GitHub connect validates `gh auth status`, enables the `github` connector in
`connectors.json`, and reports the shared connector controls. Manual `github
capture` remains available for imports and facts.

Local git capture does not require account connection, but it should appear in
the same connector status view as an enabled local source when file/git capture
is active.

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
- `payload_json`: source-specific details such as URL, number, repository, state, branch, commit SHA, and timestamps

Local linking rules:

1. Prefer a local git repository whose remote matches the GitHub repository.
2. If a commit SHA is present, link to a local project that has captured the same git commit.
3. Fall back to the GitHub repository name.

Ingestion must not require automatic actions. Drafting replies, comments, or PR
updates remains future work and must follow suggest -> draft -> approve -> act.
