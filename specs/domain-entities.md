# Domain Entity Contracts

These contracts describe the core WorkGraph concepts before storage.

Entities marked `active` are implementation targets.
Entities marked `draft` express product direction but are not required for Phase 0.

A draft entity becomes active only when a skipped placeholder is replaced with an executable failing fact.

Storage details live in `specs/db-contracts.md`.

## Active Entities

### Event

Status: active

Events are the source of truth for passive memory.

Every captured signal is normalized into an event and can later be queried by commands such as `workgraph today` and `workgraph resume <project>`.

Required fields:

- `id`: stable unique event identifier
- `source`: origin of the event, such as `file`, `git`, `github`, `slack`, or `cli`
- `type`: source-specific event type, such as `file.modified`
- `timestamp`: when the event happened, stored as an RFC3339 timestamp
- `payload_json`: valid JSON containing source-specific details

Optional fields:

- `project`: inferred project or workspace name
- `actor`: person, system, or tool responsible for the event, when known
- `summary`: short human-readable description

Contracts:

- `payload_json` must always be valid JSON
- raw source details should be preserved in `payload_json` when useful for debugging
- query behavior should rely on stored events, not generated summaries
- time-based queries use the current local day unless a command explicitly accepts another range

### Session

Status: active

Sessions are inferred groupings of related events.

Required fields:

- `id`: stable unique session identifier
- `started_at`: when the session began, stored as an RFC3339 timestamp
- `ended_at`: when the session ended, stored as an RFC3339 timestamp

Optional fields:

- `project`: inferred project or workspace name
- `summary`: short human-readable description

Contracts:

- `started_at` is before or equal to `ended_at`
- sessions may exist without summary
- sessions should be explainable from their source events

### MemoryDoc

Status: active

Memory docs represent active memory loaded from markdown, HTML, or other local files.

Required fields:

- `id`: stable unique memory document identifier
- `path`: local file path
- `kind`: document kind, such as `markdown`, `html`, or `text`
- `content`: loaded document content
- `updated_at`: when the source file was last updated, stored as an RFC3339 timestamp

Contracts:

- `path` identifies the source file
- `kind` is non-empty
- `content` may be empty
- `updated_at` is valid RFC3339

## Draft Future Entities

### Task

Status: draft

Represents unfinished or completed work inferred from events or declared manually.

### Person

Status: draft

Represents people mentioned or interacted with across tools.

### Project

Status: draft

Represents inferred or declared bodies of work.

### Artifact

Status: draft

Represents files, PRs, issues, docs, links, or other work objects.

### EntityLink

Status: draft

Represents relationships between entities.

### Recommendation

Status: draft

Represents system-generated suggestions.

### Decision

Status: draft

Represents decisions made by the user or team.

### Goal

Status: draft

Represents strategic goals, OKRs, or personal priorities.
