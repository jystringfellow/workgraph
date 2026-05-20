# GitHub Integration

WorkGraph should ingest GitHub activity as cloud-side work events that connect
back to local projects when possible.

GitHub ingestion should start with explicit capture:

```text
workgraph github capture
```

The first MVP should ingest:

- pull requests
- issues
- review comments or PR comments when available

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
