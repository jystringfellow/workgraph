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

`cmd/workgraph/main.go` remains a small command router and shared flag-value
definitions.

Command parsing and rendering live in focused per-domain files under
`cmd/workgraph/`:

- `cmd/workgraph/connectors_cmd.go` (`connectors`, `capture`, `bridge`, `plugin`)
- `cmd/workgraph/providers_cmd.go` (`slack`, `notion`, `github`, `git`, `calendar`, `mail`, `azure`)
- `cmd/workgraph/llm_cmd.go` and `cmd/workgraph/llm_associate.go` (`llm`)
- `cmd/workgraph/settings_cmd.go` (`init`, `settings`, `doctor`, `security`, `network`, `start`, `status`, `stop`)
- `cmd/workgraph/views_cmd.go` (`today`, `events`, `resume`, `memory`, `suggestions`, `associations`, `review`)
- `cmd/workgraph/ai.go`, `cmd/workgraph/next.go`, `cmd/workgraph/projects.go`, `cmd/workgraph/projects_review.go`, `cmd/workgraph/service.go`, `cmd/workgraph/help.go`, `cmd/workgraph/version.go`

The command package keeps CLI-only concerns such as flags, usage text,
stdout/stderr rendering, and exit codes. Core behavior remains in the
root package behind testable config/result structs.

## Connector Policy And Runtime

Connector governance spans managed settings (`managed_settings.go`), the
connector registry (`connector_registry.go`), connector runtime state
(`connector.go`), provider setup functions, and `workgraph start` polling.
Current enforcement evaluates managed policy (`enforceConnectorManagedSettings`,
`connectorAllowedByManagedPolicy`, and `enforceSlackDMManagedSettings`) before
provider setup, enablement, and polling. Future connector security work can
consolidate policy decisions behind a small internal surface when adding more
connector controls.

Target shape:

```text
connectorPolicy.CanSetup(id)
connectorPolicy.CanEnable(id)
connectorPolicy.CanPoll(id)
```

The policy layer answers whether an action is allowed and provides the
human-readable reason. Provider setup functions check policy before OAuth,
manual-token validation, or local credential writes. Runtime polling checks
policy before both explicit one-shot polling and unattended daemon polling.

Provider-specific high-risk options remain explicit when the semantics
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

The baseline association layer scores a bounded, cross-source candidate window
using concrete evidence:

- same project or repository
- same branch
- same commit SHA
- same pull request or issue number
- same URL
- close timestamp window
- normalized title or summary token overlap

The executable contract, weights, thresholds, canonical pair identity, and
false-positive guards live in `specs/event-associations.md`. Qualifying pairs
reuse the shared suggestion lifecycle; no separate association table is needed
for the first slice.

Scores should be explainable. A user or fact should be able to inspect why two
events were considered related.

Inspection command:

```text
workgraph associations explain <event-id>
```

Example output:

```text
Event: github.pull_request:owner/repo:42
Related associations:
- slack.message:C123:1712345678.000100
  score: 100 (high)
  state: proposed
  reasons:
  - identical normalized URL https://github.com/owner/repo/pull/42
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

`migrateSchema` in `init.go` ensures secondary indices on `events` (`timestamp`,
`project`, `source`, `type`) via `ensureIndex`, plus targeted partial indices
for active capture requests and Slack container repairs:

```sql
CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events (timestamp);
CREATE INDEX IF NOT EXISTS idx_events_project   ON events (project);
CREATE INDEX IF NOT EXISTS idx_events_source    ON events (source);
CREATE INDEX IF NOT EXISTS idx_events_type      ON events (type);
```

Indices run through `createSchema` on `init` for new installs and on database
open for upgrades.

## Schema Evolution Strategy

Schema initialization and upgrades run under an exclusive file lock
(`store_migration.go`) via `createSchema` and `migrateSchema` in `init.go`,
using `CREATE TABLE IF NOT EXISTS`, `ensureColumn`, `ensureIndex`, and
idempotent data backfills. This additive shim has no version tracking and no
story for destructive or reordering changes. When a future slice requires
ordered or non-additive migrations, consider:

- Store a `user_version` integer in the SQLite pragma (`PRAGMA user_version`).
- Apply numbered migrations in order, gated on the current version number.
- Keep migrations in a `[]migration` slice in `init.go` so they are easy to
  audit and facts can assert that schema versions advance correctly.

This does not need to land before a feature slice requires it, but each new
`ensureColumn` call adds future migration debt.

## HTTP Client Timeouts In Connector Polling

Connector polls now run with a 30-second outer context deadline. The runtime
clones caller-provided HTTP clients and binds requests to that context. Git and
GitHub provider commands also receive the poll context.

LLM clients already set explicit timeouts (10 s for model advertisement, 60 s
for completions). The connector runtime now applies the same discipline:

- each ready connector polls immediately in its own goroutine
- the outermost polling call has a configurable whole-poll deadline
- transient failures use capped exponential backoff
- authentication failures stop only the affected poller until setup is repaired
- a fact verifies that a stalled mock provider does not block file capture

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

Connector scheduling uses a small value type:

```go
type connectorPoller struct {
    id       string
    interval time.Duration
    poll     func(ctx context.Context) error
}
```

`StartRun` builds connector-specific capture state, and `Run` builds a
`[]connectorPoller` whose entries execute independently. Provider credentials,
cursors, and clients still live on `RunCapture`; move those into provider-owned
state only when another behavior change justifies the additional split.

## Non-Goals

- no repository-wide package split before behavior requires it
- no generic connector option bag that hides provider-specific security meaning
- no LLM-first association system
- no relevance scoring that silently mutates stored events
