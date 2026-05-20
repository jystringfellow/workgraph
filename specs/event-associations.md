# Event Associations

Event associations make captured events easier to query without inventing unsupported summaries.

For Phase 0, associations are deterministic metadata derived from local evidence:

- project
- artifact path
- inferred session

These associations should improve `today` first and prepare the ground for `resume <project>` later.

## Project

Project inference prefers local repository evidence over folder names:

1. nearest enclosing git repository root
2. configured watch root name
3. parent folder name

The stored event `project` value should be stable and human-readable.

## Artifact Path

File events preserve the source path in `payload_json.path`.

Query commands may use that path as the event's artifact identity. A future artifacts table can normalize this, but Phase 0 should not require a new table before the behavior is useful.

## Session

Sessions are deterministic time groupings. Events belong together when they are close in time and associated with the same project.

For Phase 0, query-time sessions are enough. Durable session rows can come later when summaries or cross-command reuse need them.

## Constraints

Associations must be:

- derived from stored events, file paths, or local repository structure
- deterministic
- inspectable in SQLite or plain text output
- independent of an LLM
