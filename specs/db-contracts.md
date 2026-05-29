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

Status: draft

Represents system-generated suggestions.

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
