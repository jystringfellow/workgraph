# Architecture Improvements

This spec captures architectural cleanup that should guide upcoming feature
work. These are not standalone refactor mandates. Prefer landing them when they
support a behavior slice, so facts continue to drive the shape of the code.

## Goals

- keep command routing thin and easy to scan
- keep connector policy enforcement consistent across setup and runtime
- keep resume relevance and event association logic explainable and reusable
- avoid large refactors that do not improve an immediately testable behavior

## CLI Command Boundaries

`cmd/workgraph/main.go` should remain a small command router over time.

As command groups grow, move parsing and rendering into files grouped by
surface area, such as:

- `cmd/workgraph/resume.go`
- `cmd/workgraph/connectors.go`
- `cmd/workgraph/llm.go`
- `cmd/workgraph/settings.go`

The command package should keep CLI-only concerns such as flags, usage text,
stdout/stderr rendering, and exit codes. Core behavior should remain in the
root package behind testable config/result structs.

## Connector Policy And Runtime

Connector governance currently spans managed settings, connector runtime state,
provider setup functions, and `workgraph start` polling. Future connector
security work should consolidate policy decisions behind a small internal
surface before adding more connector controls.

Target shape:

```text
connectorPolicy.CanSetup(id)
connectorPolicy.CanEnable(id)
connectorPolicy.CanPoll(id)
```

The policy layer should answer whether an action is allowed and provide the
human-readable reason. Provider setup functions should ask policy before OAuth,
manual-token validation, or local credential writes. Runtime polling should ask
policy before both explicit one-shot polling and unattended daemon polling.

Provider-specific high-risk options should remain explicit when the semantics
are provider-specific. For example, Slack DM capture is not just another
connector id because it changes requested OAuth scopes and capture behavior
inside an otherwise allowed Slack connector.

## Resume Relevance

Bare `workgraph resume` should eventually list projects with user-work
evidence, not every project that has any stored event.

The relevance logic should live outside `resume.go` once it becomes more than a
simple grouping rule. It should answer questions such as:

- did the local user author or directly participate in this work?
- is this project active enough to be worth resuming?
- which concrete events explain why this project is shown?
- which events are weak evidence, such as cloned repository history authored by
  someone else?

Project-specific `workgraph resume <project>` should remain exact and complete.
Filtering should primarily affect the project list shown by bare
`workgraph resume`. A future `workgraph resume --all` can preserve the current
"all projects with events" behavior for debugging and audit.

## Identity And Ownership

Resume relevance and future association scoring need an explicit local identity
model. The first deterministic version can use:

- `git config --global user.email`
- repo-local `git config user.email`
- configured additional git email addresses
- configured GitHub logins when available

This should become inspectable local config rather than hidden inference. The
system should be conservative when identity is unknown.

## Event Associations

Cross-source association should start as a deterministic local baseline before
adding LLM or embedding ranking.

The baseline association layer should score candidate links using concrete
evidence:

- same project or repository
- same branch
- same commit SHA
- same pull request or issue number
- same URL
- same file path or directory
- close timestamp window
- same actor or participant
- normalized title or summary token overlap

Scores should be explainable. A user or fact should be able to inspect why two
events were considered related.

Suggested future command:

```text
workgraph associations explain <event-id>
```

Example output:

```text
Event github.pull_request:42
Likely related:
- git.commit:abc123
  score: 92
  reasons:
  - same repository workgraph
  - pull request references commit abc123
  - commit author matches local git identity
```

The semantic lane should only rank or explain a small deterministic candidate
set. It should not require sending broad raw event history to an LLM, and it
must remain optional under the existing LLM controls.

## Large Provider Files

Provider files such as Slack, calendar, mail, Notion, Azure Boards, and LLM
will likely continue to grow. Split them only when a behavior slice benefits
from the split.

Useful split boundaries are:

- setup and OAuth flow
- capture and provider API calls
- local storage and config files
- rendering and user-facing messages
- provider-specific facts or fixtures

Avoid introducing broad interfaces before multiple providers genuinely share a
stable shape.

## Database Indices And Query Performance

The `events` table has no secondary indices. As captured event volume grows,
queries against `timestamp`, `project`, `source`, and `type` columns degrade
to full table scans. `today.go` already filters events in Go after loading a
broad time window, which compounds this.

Add indices when landing the first behavior slice that exposes latency:

```sql
CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events (timestamp);
CREATE INDEX IF NOT EXISTS idx_events_project   ON events (project);
CREATE INDEX IF NOT EXISTS idx_events_source    ON events (source);
CREATE INDEX IF NOT EXISTS idx_events_type      ON events (type);
```

Drive the index addition through `createSchema` so it runs on `init` for new
installs and through `ensureIndex` (parallel to `ensureColumn`) for upgrades.
Write a fact that verifies the indices exist after `init`.

## Schema Evolution Strategy

The current `ensureColumn()` shim works for additive column changes but has no
version tracking and no story for destructive or reordering changes. Before
adding a third call to `ensureColumn`, consider:

- Store a `user_version` integer in the SQLite pragma (`PRAGMA user_version`).
- Apply numbered migrations in order, gated on the current version number.
- Keep migrations in a `[]migration` slice in `init.go` so they are easy to
  audit and facts can assert that schema versions advance correctly.

This does not need to land before the next feature slice, but each new
`ensureColumn` call adds future migration debt.

## HTTP Client Timeouts In Connector Polling

Connector `*http.Client` fields in `RunCapture` carry no documented timeout
guarantee. When callers pass `nil` (the default in daemon mode), the underlying
transport is `http.DefaultClient` which has no timeout. A slow or unresponsive
upstream API could hang a polling goroutine indefinitely, blocking the next
poll cycle and leaking a goroutine for the life of the daemon.

LLM clients already set explicit timeouts (10 s for model advertisement, 60 s
for completions). Apply the same discipline to connector clients:

- Set a reasonable read timeout (e.g., 30 s) when constructing the default
  connector client.
- Add a `context.WithTimeout` wrapping the outermost polling call so the
  whole round trip is bounded, not just the TCP dial.
- Write a fact that verifies a connector does not hang when the mock server
  stops responding.

## Suggestion Pattern Key Stability

Suggestion IDs are derived from `(type, pattern_key)`. The `UNIQUE` constraint
enforces one live suggestion per `(type, pattern_key)` pair, which is correct.
However, if a pattern key changes (for example, a path normalisation rule
changes its output), the old row becomes an orphan. Future suggestion producers
should treat `pattern_key` as immutable once a row exists, or implement an
explicit rename/merge path before changing the key format.

## RunCapture Struct Growth

The `RunCapture` struct has grown to 30+ fields, mixing file-watching state
(watcher, budget, suppress maps) with per-connector polling state (token,
channels, cursors, HTTP clients, intervals). Adding a new connector currently
requires editing the struct, `StartRun`, and the polling loop together.

Split connectors into a small value type when a new connector warrants it:

```go
type connectorPoller struct {
    id       string
    interval time.Duration
    poll     func(ctx context.Context) error
}
```

`StartRun` then builds a `[]connectorPoller` slice and the poll loop iterates
it uniformly. This is not a refactor mandate — prefer landing it when a new
connector or the connector policy layer needs it.

## Non-Goals

- no repository-wide package split before behavior requires it
- no generic connector option bag that hides provider-specific security meaning
- no LLM-first association system
- no relevance scoring that silently mutates stored events

