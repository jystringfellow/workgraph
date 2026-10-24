# Database Contracts

These contracts describe intended durable state for workgraph.

Tables marked `active` are implementation targets.
Tables marked `draft` express product direction but are not required for Phase 0.

A draft contract becomes active only when a skipped placeholder is replaced with an executable failing fact.

The database is the durable operational memory for workgraph.

Domain entity contracts are defined in `specs/domain-entities.md`. This document defines durable table contracts and query requirements.

`workgraph init` creates the local SQLite database at:

```text
~/.workgraph/workgraph.db
```

For Phase 0, `workgraph init` creates active tables as empty schema. It does not populate `events`, `sessions`, or `memory_docs`.

## Storage Mode

The database uses SQLite write-ahead logging (`journal_mode=WAL`), so readers
do not block the writer and the writer does not block readers. Schema
preparation enables WAL; the mode is stored in the database file, so it is set
once on `init` or on the first open by a binary that has this contract, and it
persists.

- Files: a WAL database is the main file plus `workgraph.db-wal` and
  `workgraph.db-shm` next to it while any connection is open. SQLite creates
  them with the main file's permissions. Back up, move, or inspect the three
  together. `workgraph.db-migration.lock` is the migration lock described below;
  it holds no data.
- Copies: never copy only the main file of a database that is open. Use
  `sqlite3 ~/.workgraph/workgraph.db ".backup copy.db"` (the online backup API),
  or run `PRAGMA wal_checkpoint(TRUNCATE)` with capture stopped before copying.
  workgraph has no code path that copies the database file.
- Read-only access: `sqlite3 -readonly ~/.workgraph/workgraph.db` and
  `file:...?mode=ro` connections keep working, because the workgraph home stays
  writable so SQLite can use the `-shm` file.
- Older binaries: every workgraph release links a SQLite build with WAL
  support, so an older binary opens a WAL database normally and does not switch
  it back.
- Durability: connections run with `synchronous=NORMAL`, which the
  `mattn/go-sqlite3` driver sets on every connection by default. With WAL this
  is the standard pairing: the database cannot be corrupted by a crash or power
  loss, but a power loss (not a process crash) can drop the most recently
  committed transactions. workgraph accepts that: captured events are
  re-derivable from their sources on the next poll, and `FULL` would add an
  fsync to every commit on the capture hot path.
- Permissions: schema preparation restricts the database and any existing
  `-wal`, `-shm` and `-journal` files to `0600` before it enables WAL, so new
  sidecar files inherit user-only permissions and broader ones left by an
  older binary or an interrupted open are repaired.
- Busy timeout: every connection retries a locked database for 5 seconds (the
  `mattn/go-sqlite3` default `_busy_timeout=5000`, applied on every open);
  project link writes use 10 seconds with `_txlock=immediate`. Short contention
  between the daemon, bridge drains, and CLI commands retries instead of
  failing with `database is locked`.
- File capture never records the database or its `-wal`, `-shm`, `-journal`,
  or `-migration.lock` files, even when the database lives under a watched
  directory.

## Schema Migrations

Schema preparation (create tables, add columns, build indexes, run backfills)
runs whenever a workgraph process opens the store for writing, including the
capture worker before it reports ready. It is idempotent and quick once the
database is current, but the first open by a newer binary can hold the write
lock for a long time on a large database.

Preparation is a single coordinated step. Each process takes an exclusive
`flock` on `<database>-migration.lock` before it prepares the schema and holds
it until preparation finishes. Other processes that need to prepare the schema
wait for the lock (up to ten minutes, then fail with `database upgrade in
progress; try again when it finishes`) instead of failing with `database is
locked`, then find the work done. The lock is released automatically if the
holder exits. On platforms without `flock` (Windows), preparation is not
coordinated and relies on the busy timeout.

Read commands do not wait behind a migration. They try the migration lock
without blocking; when another process holds it they skip preparation and say
so instead of reading a schema that may still be old:

- capture-request listings return `ErrDatabaseUpgradeInProgress`
  (`database upgrade in progress; try again when it finishes`) instead of
  partial or empty request state.
- `connectors status` shows `capture unknown (database upgrade in progress)`
  for connected bridged connectors.
- `today` and `resume` read the current snapshot and print `Database upgrade
  in progress; capture status refreshes when it finishes.` in place of the
  bridge stall check. `resume` skips recording its overview check-in during the
  upgrade, so the next `resume` still reports work since the previous one.
- `status` and `doctor` report the upgrade; `doctor` also reports the journal
  mode as `Database: ok (journal mode: wal)`.

`start` waits through a migration as described in
[capture controls](capture-controls.md#waiting-for-readiness).

## Tables

### events

Status: active

Every captured signal is stored as an event.

Required fields:

- `id`
- `source`
- `type`
- `timestamp`
- `payload_json`
- `created_at`

Optional fields:

- `project`
- `actor`
- `summary`

Contract:

- `id` is unique
- `source` is non-empty
- `type` is non-empty
- `timestamp` is valid RFC3339
- `payload_json` is valid JSON
- events are queryable by project
- events are queryable by time range

### sessions

Status: active

Sessions are inferred groupings of related events.

Required fields:

- `id`
- `started_at`
- `ended_at`

Optional fields:

- `project`
- `summary`

Contract:

- `started_at` is before or equal to `ended_at`
- sessions may exist without summary
- sessions can be linked back to events later

### memory_docs

Status: active

Memory docs represent active memory loaded from markdown, HTML, or other local files.
They preserve user-owned source material for personal, organization, team, and
project memory.

External source artifacts can support or suggest updates to memory docs, but
they are not memory docs unless the user explicitly promotes or curates them
into active memory.

Required fields:

- `id`
- `path`
- `kind`
- `content`
- `updated_at`

Contract:

- `path` is unique
- `kind` is non-empty
- `content` may be empty
- `updated_at` is valid RFC3339

### memory_links

Status: active

Memory links connect active memory documents to captured evidence without
rewriting the evidence itself.

Required fields:

- `id`
- `memory_doc_path`
- `event_id`
- `relation`
- `created_at`

Contract:

- `id` is unique
- `memory_doc_path` is non-empty
- `event_id` is non-empty
- `relation` is non-empty, such as `supported_by`
- `created_at` is valid RFC3339

## Draft Future Tables

### tasks

Status: draft

Represents unfinished or completed work inferred from events or declared manually.

Fields:

- `id`
- `title`
- `status`
- `source`
- `source_ref`
- `project_id`
- `created_at`
- `updated_at`
- `due_at`

Contracts:

- `title` is non-empty
- `status` is one of: `open`, `done`, `ignored`
- `source_ref` links back to evidence when inferred

### people

Status: draft

Represents people mentioned or interacted with across tools.

Fields:

- `id`
- `name`
- `handle`
- `email`
- `source`
- `created_at`
- `updated_at`

Contracts:

- at least one identifier exists: `name`, `handle`, or `email`

### projects

Status: draft

Represents inferred or declared bodies of work.

Fields:

- `id`
- `name`
- `slug`
- `status`
- `description`
- `created_at`
- `updated_at`

Contracts:

- `slug` is unique
- `name` is non-empty

### artifacts

Status: draft

Represents files, PRs, issues, docs, links, or other work objects.

Fields:

- `id`
- `kind`
- `title`
- `uri`
- `project_id`
- `created_at`
- `updated_at`

Contracts:

- `kind` is non-empty
- `uri` is unique when present
- artifacts can represent local files, external docs, work items, meetings, or
  communication threads

### entity_links

Status: draft

Represents relationships between entities.

Fields:

- `id`
- `from_type`
- `from_id`
- `to_type`
- `to_id`
- `relation`
- `confidence`
- `created_at`

Contracts:

- `relation` is non-empty
- `confidence` is between 0 and 1

### recommendations

Status: draft, superseded by `suggestions`

Represents the older name for system-generated suggestions. New implementation
work should use `suggestions`.

Fields:

- `id`
- `kind`
- `title`
- `rationale`
- `status`
- `evidence_json`
- `created_at`
- `resolved_at`

Contracts:

- `rationale` must include evidence
- `status` is one of: `proposed`, `accepted`, `dismissed`, `acted`

### suggestions

Status: active target

Represents system-generated suggestions that remain reviewable until the user
acts. This is the shared substrate for watch-root suggestions, ignore-rule
suggestions, what-next suggestions, and association suggestions.

Required fields:

- `id`
- `type`
- `title`
- `reason`
- `confidence`
- `lane`
- `status`
- `evidence_json`
- `created_at`
- `updated_at`

Optional fields:

- `pattern_key`
- `resolved_at`

Contracts:

- `id` is unique and stable for one suggestion instance
- `type` is non-empty, such as `watch_root`, `ignore_path`, `ignore_name`,
  `what_next`, or `association`
- `reason` explains why the suggestion appeared
- `confidence` is one of: `high`, `medium`, `low`
- `lane` is one of: `baseline`, `semantic`, `manual`
- `status` is one of: `proposed`, `reviewed`, `approved`, `dismissed`,
  `snoozed`, `acted`
- `evidence_json` is valid JSON and cites event ids, source ids, paths, or
  provider object ids
- `pattern_key` identifies repeatable suggestion patterns for coalescing and
  suppression when available
- low-confidence suggestions must not trigger irreversible mutations
- association suggestions use a canonical two-event pair as `pattern_key` and
  store score, matched signals, reasons, sources, and the two canonical event
  ids in `evidence_json`
- association approval changes suggestion lifecycle only; it does not require
  another table and never mutates source events
- deterministic association refresh preserves non-proposed lifecycle states
  and does not change timestamps when the evidence is unchanged

### suggestion_feedback

Status: active target

Append-only local feedback events for suggestion lifecycle actions.

Required fields:

- `id`
- `suggestion_id`
- `action`
- `created_at`

Optional fields:

- `reason_code`
- `note`

Contracts:

- `id` is unique
- `suggestion_id` links to a suggestion id
- `action` is one of: `reviewed`, `accepted`, `dismissed`, `snoozed`,
  `completed`, `undone`
- `reason_code` is stable when present so review metrics can group it
- feedback events are append-only; updates to current suggestion state happen
  on the suggestion row
- a lifecycle state update and its feedback append happen in one transaction;
  snooze suppression changes join that same transaction
- `accepted` is emitted by approval, while `completed` moves an already approved
  suggestion to `acted` without repeating the approved mutation

### suggestion_suppressions

Status: active target

Local suppression rules that hide recurring suggestion patterns without
deleting evidence.

Required fields:

- `id`
- `type`
- `pattern_key`
- `created_at`

Optional fields:

- `until_at`
- `reason`

Contracts:

- `id` is unique
- `type` matches the suggestion type being suppressed
- `pattern_key` is non-empty
- `until_at`, when present, is a valid RFC3339 timestamp
- suppression is reversible and must not delete source evidence

### decisions

Status: draft

Represents decisions made by the user or team.

Fields:

- `id`
- `title`
- `context`
- `decision`
- `rationale`
- `project_id`
- `decided_at`
- `created_at`

Contracts:

- `decision` is non-empty
- `decided_at` is valid RFC3339

### goals

Status: draft

Represents strategic goals, OKRs, or personal priorities.

Fields:

- `id`
- `title`
- `scope`
- `status`
- `parent_goal_id`
- `created_at`
- `updated_at`

Contracts:

- `title` is non-empty
- `scope` is one of: `personal`, `team`, `organization`, `project`

### preferences

Status: draft

Represents durable principles, preferences, working style, and decision
heuristics.

Fields:

- `id`
- `scope`
- `subject`
- `statement`
- `source_memory_doc_id`
- `created_at`
- `updated_at`

Contracts:

- `statement` is non-empty
- `scope` is one of: `personal`, `team`, `organization`
- inferred preferences require an explicit source memory document or user
  approval before becoming durable

## Phase 0 Scope

Phase 0 starts with the active tables:

- `events`
- `sessions`
- `memory_docs`
