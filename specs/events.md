# WorkGraph Event Schema

Events are the source of truth for passive memory.

Every captured signal is normalized into an event, stored locally, and queried by commands such as `workgraph today` and `workgraph resume <project>`.

## SQLite Database

`workgraph init` creates the local database at:

```text
~/.workgraph/workgraph.db
```

The initial database contains an `events` table.

The broader database contract lives in `specs/db-contracts.md`.

```sql
CREATE TABLE events (
  id TEXT PRIMARY KEY,
  source TEXT NOT NULL,
  type TEXT NOT NULL,
  timestamp TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  project TEXT,
  actor TEXT,
  summary TEXT,
  created_at TEXT NOT NULL
);
```

## Required Fields

- `id`: stable unique event identifier
- `source`: origin of the event, such as `file`, `git`, `github`, `slack`, or `cli`
- `type`: source-specific event type, such as `file.modified`
- `timestamp`: when the event happened, stored as an RFC3339 timestamp
- `payload_json`: valid JSON containing source-specific details

## Optional Fields

- `project`: inferred project or workspace name
- `actor`: person, system, or tool responsible for the event, when known
- `summary`: short human-readable description

## Invariants

- `payload_json` must always be valid JSON.
- Events must be insertable and readable without cloud dependencies.
- Raw source details should be preserved in `payload_json` when useful for debugging.
- Query behavior should rely on stored events, not generated summaries.
- Time-based queries use the current local day unless a command explicitly accepts another range.
