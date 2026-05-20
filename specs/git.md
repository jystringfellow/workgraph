# Git Integration

WorkGraph captures local git commits as first-class events.

Local git capture runs as part of `workgraph run`, so commits made while the
daemon is active are captured without a separate manual step.

Manual capture is also available for debugging and backfill:

```text
workgraph git capture
```

Both paths scan configured watch roots for git repositories, read recent commits,
and store them in the local event store. They do not require GitHub or network
access.

Commit event contract:

- `source`: `git`
- `type`: `git.commit`
- `project`: nearest git repository directory name
- `actor`: commit author email when available
- `summary`: commit subject
- `payload_json.repo_path`: absolute repository path
- `payload_json.commit`: commit SHA
- `payload_json.branch`: current branch name when available
- `payload_json.subject`: commit subject
- `payload_json.author_name`: commit author name
- `payload_json.author_email`: commit author email

Commit event IDs are deterministic so repeated git capture runs do not create
duplicates for the same commit.

Git capture should respect configured watch roots and ignore rules. It should
skip unsupported or unreadable directories rather than aborting the whole scan.

This is the local foundation for later GitHub ingestion. GitHub events can link
to the same project and commits once local commit capture exists.
