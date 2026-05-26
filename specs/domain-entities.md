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
They are the durable user-owned source material for context that captured events
cannot know on their own.

Active memory can describe several scopes:

- personal: priorities, principles, preferences, working style, constraints
- organization: company strategy, planning memos, values, operating principles
- team: squad strategy, rituals, ownership, current goals
- project: project context, priorities, decisions, constraints, open questions

WorkGraph should preserve memory documents as inspectable local files or local
snapshots. Structured entities can later be derived from or linked to these
documents, but the source document remains the user-owned record.

External sources such as Notion, calendars, meetings, work trackers, chat tools,
and docs can support or suggest updates to active memory. They are not active
memory unless the user explicitly promotes or curates something into memory.

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
- structured interpretation must remain explainable from the source document or
  captured events

## Draft Future Entities

### Task

Status: draft

Represents unfinished or completed work inferred from events or declared manually.

### Person

Status: draft

Represents people mentioned or interacted with across tools.
People can be inferred from passive events or declared in active memory when a
user wants local context about stakeholders, collaborators, reviewers, or teams.

### Project

Status: draft

Represents inferred or declared bodies of work.
Projects can come from captured activity, explicit project memory, repositories,
work-tracking tools, or document collections. A declared project should not
require captured events before it can hold active memory.

### Artifact

Status: draft

Represents files, PRs, issues, docs, links, or other work objects.
Artifacts include strategy memos, Notion pages, calendar events, meeting
transcripts, Jira issues, Azure DevOps work items, pull requests, and local
files. Artifacts are evidence from local or external sources; they are not
active memory and are not automatically trusted summaries.

### EntityLink

Status: draft

Represents relationships between entities.
Links connect active memory and passive events to structured entities, such as a
strategy memo supporting a goal, a meeting discussing a project, or a pull
request implementing a decision.

### Recommendation

Status: draft

Represents system-generated suggestions.

### Decision

Status: draft

Represents decisions made by the user or team.
Decisions can be captured from explicit memory, strategy documents, meetings, or
tool activity. They should retain rationale and source evidence when known.

### Goal

Status: draft

Represents strategic goals, OKRs, or personal priorities.
Goals cover personal priorities, team goals, company strategy, OKRs, and other
direction-setting commitments. Goals may be declared directly in active memory
or derived from strategy documents, but derived goals must point back to source
evidence.

### Preference

Status: draft

Represents durable principles, preferences, working style, and decision
heuristics for the user, team, or organization.

Preferences are active memory, not observed behavior. Captured events may
provide evidence that a preference exists, but WorkGraph should not infer a
durable preference without an explicit local memory source or user approval.
