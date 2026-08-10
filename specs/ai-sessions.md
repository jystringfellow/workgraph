# AI Sessions

`workgraph ai` is a local continuity layer for cooperating CLI AI coding
agents. It records a small, structured restart artifact so a user can close a
terminal, stop a process, reboot the same machine, or change CLI agents and
later inspect durable evidence or resume a supported native agent session.

Structured checkpoints restore **work state** across supported or unsupported
agents. For tools with a verified native adapter, workgraph also records the
tool's opaque local session identifier and invokes that tool's native resume
command. Workgraph does not scrape or attempt to translate transcripts.

## Product and Security Boundary

AI session data remains local to the selected workgraph home and SQLite
database. V1 does not provide cloud synchronization, remote storage,
cross-machine resume, database export for resume, or path remapping.

The AI session commands themselves make no network requests. A child command
launched by `ai run` may use the network according to that tool's own behavior;
workgraph does not sandbox or change the child tool's capabilities.

A workgraph session is exactly one wrapped child-process lifetime. Resuming a
native conversation creates a new workgraph session with a new workgraph ID and
an immutable predecessor link to the prior lifetime. The native session ID is
tool-owned and may remain the same across those lifetimes.

Git worktrees are optional and user-controlled. Workgraph observes the checkout
in which a session starts, but never creates, switches, removes, or otherwise
manages worktrees.

## V1 Commands

```text
workgraph ai run [--home <path>] [--database <path>] -- <agent-command>
workgraph ai checkpoint [--home <path>] [--database <path>] [session-id] --stdin
workgraph ai sessions [--home <path>] [--database <path>] [--status <status>] [--limit <count>] [--all | --archived]
workgraph ai archive [--home <path>] [--database <path>] <session-id> [<session-id>...]
workgraph ai archive [--home <path>] [--database <path>] (--all | --status <status> [--before <date>]) (--dry-run | --yes)
workgraph ai unarchive [--home <path>] [--database <path>] <session-id> [<session-id>...]
workgraph ai show [--home <path>] [--database <path>] <session-id>
workgraph ai resume [--home <path>] [--database <path>] <session-id>
```

The `ai` group and every leaf command must participate in the help behavior
defined by `specs/cli-help.md`.

## Local Storage Resolution

All `ai` commands resolve the workgraph home and database in this order:

1. an explicit `--home` or `--database` flag
2. `WORKGRAPH_AI_HOME` or `WORKGRAPH_AI_DATABASE`
3. the normal local defaults: `~/.workgraph` and
   `<resolved-home>/workgraph.db`

Resolved paths are normalized absolute paths. `ai run` injects the resolved
values into its child, rather than copying raw flag values.

For `ai checkpoint`, an explicit home, database, or session ID that disagrees
with its corresponding injected value is an error. The command writes nothing.
A user who intentionally wants another store must first unset the injected
variables. The resolved database must already contain the referenced started
session; checkpoint never creates or imports a missing session.

Outside a wrapped session, `ai sessions`, `ai show`, and `ai resume` normally
use flags or the local defaults. The AI-specific environment variables are
scoped session defaults, not a new general workgraph configuration mechanism.

## Identifiers

Session IDs are lowercase UUIDv4 values generated with cryptographically secure
randomness. V1 commands require the exact full ID; prefix matching is deferred.

Event IDs are:

```text
ai.session_started:<session-id>
ai.session_native_bound:<session-id>:<native-id-digest>
ai.session_checkpointed:<session-id>:<event-uuid>
ai.session_ended:<session-id>
ai.session_archived:<session-id>:<event-uuid>
ai.session_unarchived:<session-id>:<event-uuid>
```

Start and end IDs make persistence retries idempotent. Every checkpoint has a
new UUIDv4 event component and therefore appends a distinct immutable event.
The native binding suffix is a SHA-256 digest of the tool name and native ID;
the opaque native ID itself appears only in the event payload.
Archive transitions also use new UUIDv4 event components. Repeating a command
when the session already has the requested archive state succeeds without
appending a redundant transition.

## Path, Checkout, and Project Identity

Paths used for identity are made absolute and cleaned, then have symlinks
resolved. Because these paths form an integrity boundary, failure to resolve an
identity path is fatal: `ai run` aborts before launch, and `ai checkpoint`
rejects the checkpoint without an event. Workgraph never silently compares a
resolved path with a cleaned-but-unresolved fallback.

When `git rev-parse --show-toplevel` succeeds from the launch directory, the
session is a Git session. Workgraph records:

- `cwd`: the current directory from which the command was invoked
- `worktree_root`: normalized absolute `git rev-parse --show-toplevel`
- `git_common_dir`: normalized absolute `git rev-parse --git-common-dir`
- `branch`: the observed branch name, or empty for detached HEAD
- `head`: the full observed commit ID

`worktree_root` identifies the checkout. `git_common_dir` groups linked
worktrees belonging to the same local repository. Independent clones remain
independent even when they share a remote; V1 never inspects Git remotes to
manufacture repository identity.

For a Git session, `project` is a deterministic display label derived from the
normalized `git_common_dir`:

- when its basename is `.git`, use the basename of its parent directory
- otherwise use its basename with one trailing `.git` suffix removed

Two independent clones can consequently have the same project label while
remaining different repository identities. Project is a label, not the
checkout integrity key.

Git's expected "not a repository" result makes the session non-Git. Failure to
execute Git, a permission failure, malformed output, or failure to collect any
required Git field after `--show-toplevel` succeeds is an observation failure,
not evidence of a non-Git directory. Launch observation failure aborts `ai run`
before the child starts and writes no event.

A valid non-Git session has empty Git fields and an empty `project` in V1. A
future explicit workgraph project mapping may supply a non-Git project; vague
folder-name or remote-based inference is not part of V1.

### Checkpoint binding

A Git checkpoint is accepted only when its newly resolved `worktree_root` and
`git_common_dir` both equal the values in the start event. Calling checkpoint
from a subdirectory of that checkout is valid. Calling it from another linked
worktree, clone, or repository is rejected without an event.

This is a path-binding integrity rule, not proof against replacement of a
repository at exactly the same canonical paths.

A non-Git session is bound to its canonical launch directory. Checkpoints from
that directory or one of its descendants are accepted; checkpoints from
outside that directory tree are rejected. The caller's current canonical
`cwd` is stored in each accepted snapshot. Descendant checks use path-component
containment, not a textual string-prefix comparison.

## Observed State

Workgraph, never the agent, captures observed state. All successfully captured
observations use this shape:

```json
{
  "observed_at": "2026-08-03T01:02:03.123456789Z",
  "cwd": "/path/to/checkout/subdirectory",
  "worktree_root": "/path/to/checkout",
  "git_common_dir": "/path/to/repository/.git",
  "branch": "feat/ai-sessions",
  "head": "92d00d4f00000000000000000000000000000000",
  "dirty_paths": ["features/ai_sessions.feature"],
  "dirty_path_count": 1,
  "dirty_paths_truncated": false
}
```

For non-Git observations, Git-specific strings are empty, `dirty_paths` is an
empty array, the count is zero, and truncation is false.

For Git observations, dirty paths include staged, unstaged, untracked, deleted,
and both source and destination paths for renames. Paths are:

- relative to `worktree_root`
- deduplicated
- sorted lexically for stable storage and rendering
- limited to the first 500 entries

`dirty_path_count` is the complete deduplicated count before truncation.
`dirty_paths_truncated` is true exactly when the count exceeds 500.

Workgraph captures observed state at start, checkpoint, and end. Start and
checkpoint require a complete observation. Final end observation is
best-effort because a child may remove or make its checkout inaccessible. If
final observation fails, the end event is still eligible for persistence with
`observation_status: "unavailable"` and no `observed` member. Raw observation
errors are written to stderr, not persisted in the payload.

## `workgraph ai run`

`ai run` resolves `argv[0]` with `exec.LookPath` and launches it directly. It
does not introduce an implicit shell. Stdin, stdout, and stderr pass through
unchanged.

`filepath.Base` of the resolved executable is stored as `tool`. The normalized
resolved executable path is stored separately as `tool_path` so a later native
resume launches the same local executable rather than trusting a changed
`PATH`. If the user explicitly launches `sh -c ...` or `npx ...`, the tool is
`sh` or `npx`. Workgraph never persists the remaining arguments.

Native behavior comes from a small verified adapter registry. An adapter
defines the executable basename, how to capture a native session ID, how to
recognize an explicit native resume invocation, and the exact argv needed to
resume. The verified adapters are:

```text
tool: codex
resume argv: <stored-tool-path> resume <native-session-id>

tool: claude
resume argv: <stored-tool-path> --resume <native-session-id>

tool: copilot
resume argv: <stored-tool-path> --resume=<native-session-id>

tool: opencode
resume argv: <stored-tool-path> --session <native-session-id>
```

The Copilot adapter applies to the direct GitHub Copilot CLI executable named
`copilot`. The preview `gh copilot` wrapper resolves to executable basename
`gh`, has a different argv boundary, and is not silently treated as the direct
adapter. Other tools remain supported by `ai run`, checkpoint, sessions, and
show, but native resume is unavailable until a separately facted adapter is
added. Workgraph never guesses a vendor command or treats one vendor's ID as
another vendor's ID.

"Verified" describes the adapter contract exercised by repository facts and
the manual compatibility checks below; it is not a promise that an untested
future vendor release will retain the same integration surface. The generic
wrapper, checkpoint, sessions, and show behavior remains independent of native
adapters. A native adapter must fail closed when its expected flags, callback
envelope, or exact session identity cannot be validated. Vendor drift must not
cause workgraph to guess an ID, read transcript storage, or launch a fresh
conversation while claiming it resumed an existing one.

For Codex, `ai run` adds session-scoped `SessionStart` and `SessionEnd` command
hooks. The start hook matches `startup`, `resume`, and `clear`; the end hook is
a best-effort bootstrap when the start hook was not yet trusted. Codex supplies
its `session_id` to both hooks on stdin. The internal workgraph handler
allowlists only the hook event name, bounded lifecycle source, cwd, and native
session ID; it ignores and never persists
`transcript_path`, model data, or any unknown field. Codex applies its normal
hook review and trust policy. Until the user trusts the exact hook definition,
the wrapped session remains valid but native resume is unavailable unless the
native ID was explicit in the launch command.

The hooks invoke the current resolved workgraph executable. During repository
development under `go run`, it uses a stable absolute `go run <repo>/cmd/workgraph`
command instead of the ephemeral compiled path so Codex's hook trust hash does
not change on every launch.

Codex CLI configuration overrides supplied by the user remain allowed except
for the exact `hooks.SessionStart` and `hooks.SessionEnd` keys that workgraph
must control for session binding. Before launch, workgraph rejects either key
when supplied through `-c`, `--config`, or their equals forms. It does not rely
on repeated-key ordering and does not reject unrelated Codex configuration
overrides. Hooks loaded by Codex from its ordinary user, project, and supported
hook configuration layers remain vendor-managed; the compatibility smoke test
must confirm that ordinary user hooks and the workgraph lifecycle hooks both
run for each supported Codex release.

For Claude Code, `ai run` supplies additional session settings containing
`SessionStart` and `SessionEnd` command hooks. Claude supplies `session_id`,
`cwd`, the hook event name, and a bounded lifecycle source on stdin. The same
allowlist and transcript exclusion used for Codex applies. The start matcher
accepts `startup`, `resume`, `clear`, `compact`, and `fork`. The injected
settings are additive to Claude's ordinary user, project, and local settings.
An explicit user-provided `--settings` argument is rejected before launch
rather than overwritten or copied into workgraph-managed storage.

For the direct GitHub Copilot CLI, workgraph generates a separate UUID and
prepends `--session-id=<uuid>` when the launch does not already select a
session. This makes the exact native identity known before launch without
reading Copilot session storage or installing a persistent hook. Explicit
`--session-id=<uuid>` and `--resume=<uuid>` selections are recognized. Bare
resume, continue, prefixes, and names remain wrapped but do not produce an
invented exact native identity. Workgraph never enables Copilot remote export
or remote control.

For OpenCode, `ai run` writes a small, inspectable adapter plugin beneath the
resolved workgraph home and adds its absolute path to the child's inline
`OPENCODE_CONFIG_CONTENT`. Existing valid inline configuration is merged and
not persisted; invalid inline configuration aborts before launch. The plugin
observes only parent session lifecycle events and invokes the internal
workgraph binding handler with the OpenCode session ID, cwd, event name, and a
bounded source. It does not read session messages, exports, prompts, files, or
diffs. Workgraph does not modify project or global OpenCode configuration.
`--pure` disables OpenCode plugins, so a new session launched that way remains
wrapped but cannot be natively bound unless an exact `--session` ID was already
provided.

Before launch, workgraph rejects the command if `WORKGRAPH_AI_SESSION_ID` is
already present. Nested wrapped sessions are not supported in V1. This also
applies to `ai resume`, because resume uses the same wrapper lifecycle and
workgraph cannot safely distinguish an active ancestor session from a manually
exported stale value. The error must explain that resume should run outside the
wrapped agent and that a known-stale value may be unset explicitly. Workgraph
never silently clears the guard variable.

The child receives:

```text
WORKGRAPH_AI_SESSION_ID=<session-id>
WORKGRAPH_AI_HOME=<resolved-home>
WORKGRAPH_AI_DATABASE=<resolved-database>
```

Workgraph replaces any existing values for those keys in the child environment
so each key appears once. Environment variables are not copied into events.

Launch sequencing is normative:

1. resolve storage, the executable, session identity, and launch observation
2. start the child and obtain its PID
3. obtain process-start identity when supported
4. persist `ai.session_started`
5. wait for the child
6. attempt a final observation and persist `ai.session_ended`
7. return the child's real exit outcome

No start event is written if executable resolution or child start fails. If the
child starts but the start event cannot be persisted, workgraph terminates and
reaps the child, reports the persistence failure, and exits nonzero. It must not
leave an untracked child running.

End observation and end-event persistence are best-effort. Their failures are
visible on stderr but never replace or suppress the child outcome. A normally
exited child returns its exact exit code. On Unix, a signal-terminated child is
stored with its normalized signal name and the wrapper returns `128 + signal
number`. An absent end event is handled by derived status and does not
invalidate prior session evidence.

When `ai run` recognizes an adapter's exact native resume selection, it stores
that native ID in the new start event. If prior workgraph events contain the
same tool and native ID, the new start event links to the matching session with
the latest event time. When an ID is selected inside Codex, Claude, Copilot, or
OpenCode, a supported lifecycle callback may derive the same predecessor after
launch. If no prior match exists, workgraph records the native ID without
inventing lineage.

Launching through `ai run` is local consent to lifecycle events and cooperative
checkpoints. It is not consent to transcript capture or any external action.

## `workgraph ai checkpoint`

Without a positional ID, checkpoint uses `WORKGRAPH_AI_SESSION_ID`. If neither
is present, it fails. If both are present and disagree, it fails.

Checkpoint accepts only agent-stated context on stdin. It validates the input
before collecting observed state or writing an event. A valid checkpoint for a
known session appends one event. Prior checkpoints are never mutated.

A child can invoke checkpoint immediately after it begins, while its wrapper is
still persisting the required post-start event. When the session ID comes from
the injected environment, checkpoint waits up to two seconds for that exact
deterministic start event to become visible. This bounded startup handshake
does not apply to unrelated explicit session lookups, which fail immediately.

A checkpoint is rejected when the session has an end event. A started session
without an end event may accept a checkpoint even when liveness is interrupted
or unknown; storage and checkout binding, rather than a potentially unavailable
process API, form the acceptance boundary.

### Input contract

Input must be valid UTF-8 JSON with these exact properties:

- at most 65,536 raw bytes, including whitespace
- exactly one top-level object
- trailing whitespace is allowed; another JSON value is rejected
- duplicate and unknown fields are rejected
- `null`, numbers, booleans, and nested objects are rejected

Duplicate-key detection requires token-level object parsing. Implementations
must not rely only on Go's ordinary struct unmarshal or
`DisallowUnknownFields`, because those do not reject duplicate keys.

Allowed fields and types are:

```json
{
  "goal": "string",
  "current_state": "string",
  "completed": ["string"],
  "next_actions": ["string"],
  "blockers": ["string"],
  "decisions": ["string"]
}
```

Every field is optional, but at least one field must contain meaningful,
non-whitespace text. Empty arrays are permitted alongside meaningful content
but do not make a checkpoint nonempty.

Normalization and field limits are:

- normalize CRLF and bare CR to LF
- trim leading and trailing whitespace from every string
- scalar strings: at most 16 KiB of normalized UTF-8
- arrays: at most 100 items
- array items: nonempty and at most 4 KiB of normalized UTF-8 each
- tab and LF are allowed; NUL and all other C0/C1 control characters are
  rejected

The raw-size check is performed by reading no more than 65,537 bytes. All
validation errors name the category and, when useful, the field, but never
echo submitted text. Invalid stdin is not logged or persisted.

Observed field names such as `branch`, `head`, `dirty_paths`, `worktree_root`,
and `git_common_dir` are unknown input fields and are rejected. An agent cannot
override workgraph observations.

### Credential-pattern guardrail

Before persistence, every submitted string is checked against deterministic,
high-confidence sensitive patterns. At minimum these cover private-key blocks,
recognized GitHub, Slack, Notion, AWS, bearer, and OpenAI-style token formats
with sufficient token length. Managed sensitive patterns may add categories.

Generic words such as `token`, `password`, and `API key` are not rejected by
themselves because they are legitimate checkpoint prose. A match rejects the
entire checkpoint. The error reports only the field and credential category,
never the matched value. Detection is a best-effort guardrail, not a guarantee
that curated text cannot contain an unrecognized secret.

Any validation, storage-binding, lifecycle, checkout-binding, observation, or
persistence failure writes no checkpoint event.

### Agent-invoked checkpoint convenience

The repository ships a reusable `workgraph-ai-checkpoint` agent skill. A user
may explicitly invoke it as `$workgraph-ai-checkpoint` or ask the current agent
to save, checkpoint, pause, or hand off the wrapped workgraph AI session. The
skill prepares the allowed agent-stated JSON from the current working context
and supplies it to `workgraph ai checkpoint --stdin` on the user's behalf. The
user does not need to write JSON, pipe `printf`, or use an agent UI's shell
escape syntax.

The skill is a convenience client of the checkpoint contract, not an alternate
write path. It must:

- require the injected `WORKGRAPH_AI_SESSION_ID` rather than inventing or
  discovering a session
- submit only the six allowed agent-stated fields and leave all observed state
  collection to workgraph
- keep the handoff concise and exclude credentials, transcript excerpts,
  prompts, terminal output, file contents, and diffs
- invoke the local `workgraph` executable directly when available, with
  `go run ./cmd/workgraph` allowed only while developing inside this repository
- run only after an explicit user request and report validation or persistence
  failures without claiming that a checkpoint was stored

The skill does not add scheduled, implicit, shutdown, signal, or process-exit
checkpoints. The explicit user invocation is the consent boundary for the
agent-authored summary.

After a successful append, `ai checkpoint` writes this receipt to stdout:

```text
AI checkpoint recorded
Session: <session-id>
Event: <event-id>
```

The receipt contains generated identifiers only and does not echo checkpoint
text. On failure, no success receipt is written.

## Event Contract

AI sessions use only these append-only event types:

- `ai.session_started`
- `ai.session_native_bound`
- `ai.session_checkpointed`
- `ai.session_ended`
- `ai.session_archived`
- `ai.session_unarchived`

No AI-specific mutable session table exists in V1. Events are the source of
truth, and current session views are projections computed at read time.

Shared event columns are:

```text
source:     ai
type:       one of the six event types
timestamp:  lifecycle occurrence time in UTC RFC3339Nano
created_at: persistence time in UTC RFC3339Nano
project:    stable project label from the start event, possibly empty
actor:      empty
summary:    deterministic lifecycle text defined below
```

`actor` is empty because the launched executable is execution metadata, not a
human actor. Tool identity belongs in the started payload and start summary.

`events.timestamp` is the authoritative lifecycle time. `created_at` describes
persistence only. Lifecycle timestamps are not duplicated as `started_at`,
`checkpointed_at`, or `ended_at` inside payloads. Readers parse timestamps into
time values before ordering; they do not rely on lexicographic SQLite ordering
of variable-width RFC3339Nano strings. Equal event timestamps are ordered by
event ID.

Every payload begins with the stable envelope fields `schema_version` and
`session_id`. V1 writes schema version 1.

### Started payload

```json
{
  "schema_version": 1,
  "session_id": "00000000-0000-4000-8000-000000000000",
  "tool": "codex",
  "tool_path": "/usr/local/bin/codex",
  "native_session_id": "native-id-if-known-at-launch",
  "predecessor_session_id": "prior-workgraph-id-if-known",
  "pid": 1234,
  "boot_identity": "opaque-digest",
  "process_start_identity": "opaque-platform-value",
  "observed": {}
}
```

`boot_identity` and `process_start_identity` are omitted when unavailable;
they are never written as `null`. Boot identity is an opaque one-way digest of
a boot-scoped platform value used only for equality. Workgraph does not store a
persistent machine identifier. `native_session_id` and
`predecessor_session_id` are omitted when they are not known at launch.

### Native-bound payload

```json
{
  "schema_version": 1,
  "session_id": "00000000-0000-4000-8000-000000000000",
  "tool": "codex",
  "native_session_id": "opaque-tool-owned-id",
  "source": "resume",
  "predecessor_session_id": "prior-workgraph-id-if-found"
}
```

One binding event is appended for each distinct native ID observed during a
wrapped lifetime. Repeated callbacks for the same workgraph session, tool, and
native ID are idempotent. A binding can add predecessor lineage when the
native ID was selected inside the child and therefore was unavailable in the
launch arguments. `source` is the adapter's bounded lifecycle value and never
contains prompt or transcript data.

### Checkpoint payload

```json
{
  "schema_version": 1,
  "session_id": "00000000-0000-4000-8000-000000000000",
  "observed": {},
  "agent_stated": {
    "goal": "Add durable restart context.",
    "current_state": "Writing executable facts.",
    "completed": ["Defined the event contract."],
    "next_actions": ["Implement session launch."],
    "blockers": [],
    "decisions": ["Keep all session data local."]
  }
}
```

Only fields present in accepted input need appear under `agent_stated`; absent
optional fields are omitted. Present empty arrays remain empty arrays.

### Ended payload

Normal exit:

```json
{
  "schema_version": 1,
  "session_id": "00000000-0000-4000-8000-000000000000",
  "outcome": {"kind": "exited", "exit_code": 0},
  "observation_status": "captured",
  "observed": {}
}
```

Signal termination uses
`{"kind":"signaled","signal":"SIGTERM"}`. A platform that cannot determine
the outcome uses `{"kind":"unknown"}`. `exit_code` and `signal` never coexist.
When final observation fails, `observation_status` is `unavailable` and
`observed` is omitted.

### Summaries

Summaries never include paths, branches, dirty filenames, arguments,
environment values, or agent-stated text. Exact forms are:

```text
AI session started (<tool>)
AI session native identity bound (<tool>)
AI session checkpointed
AI session ended (exit <code>)
AI session ended (signal <name>)
AI session ended
```

The final form represents an unknown outcome.

Archive summaries are exactly:

```text
AI session archived
AI session unarchived
```

## Status Derivation

Status is computed at query time and never stored. Precedence is:

1. an end event exists: `ended`
2. recorded and current boot identities are available and differ:
   `interrupted`
3. the original PID is definitively absent: `interrupted`
4. the PID exists and its process-start identity matches: `running`
5. the PID exists and its process-start identity differs: `interrupted`
6. workgraph cannot verify enough evidence: `unknown`

PID existence alone is never enough to report `running`. Unsupported process
APIs, permission failures, and missing identity evidence degrade to `unknown`
rather than invented confidence. An ordinary reboot changes the boot identity,
so sessions lacking end events become interrupted when that identity is
available.

## `workgraph ai sessions`

`ai sessions` is read-only. By default it lists every unarchived known session;
there is no hidden freshness cutoff or default count limit. `--all` includes
archived sessions, while `--archived` lists only archived sessions. `--all` and
`--archived` are mutually exclusive.

`--status <status>` accepts exactly one derived status: `running`,
`interrupted`, `ended`, or `unknown`. Any other value is a nonzero validation
error. `--limit <count>` accepts a non-negative integer. Zero means unlimited.
Visibility and status filters are applied before the limit. The limit is
applied after deterministic sorting, so it always selects the newest matching
sessions.

When a positive limit omits matching sessions, the output immediately after
the `AI sessions` heading includes:

```text
Showing <shown> of <matching> matching sessions
```

The count is computed after archive visibility and status filtering but before
the limit. It does not include sessions excluded by those filters.

Sessions are sorted by parsed latest event time descending, then full session
ID ascending. Each entry shows:

- full session ID
- tool, or `unknown` when unavailable
- latest native session ID, or `-`
- predecessor workgraph session ID, or `-`
- derived status
- project, or `-`
- started time
- latest checkpoint time, or `-`
- latest event time
- archive state as `yes` or `no`

Displayed times use local RFC3339 with an explicit UTC offset. The overview
never shows paths or agent-stated text. Liveness is inspected only for sessions
without an end event. With no sessions, output is exactly:

```text
No AI sessions recorded.
```

When sessions exist but none match the requested visibility or status filter,
output is exactly:

```text
No AI sessions matched.
```

## `workgraph ai archive` and `workgraph ai unarchive`

Archival is a reversible list-visibility state derived from append-only events.
It never deletes session events, removes checkpoints, terminates a process, or
changes the derived process status. It may be explicitly applied to a session
in any status. `ai show` and `ai resume` continue to accept archived sessions.

One or more explicit IDs may be archived or unarchived in one invocation.
Explicit IDs are deduplicated in first-appearance order and act immediately;
they cannot be combined with selector, preview, or confirmation flags. Every
ID must resolve before any write occurs. The required transitions are appended
in one SQLite transaction, so persistence failure rolls back the entire batch.

Selector-based archival accepts either `--all` by itself or one `--status`
filter with an optional `--before`. `--all` selects every unarchived session
and is mutually exclusive with `--status` and `--before`. `--before` is invalid
without `--status`, preventing an accidental age-only sweep across running
sessions. Selector filters combine with AND and never select sessions already
archived.

`--before YYYY-MM-DD` means strictly before local midnight at the start of that
date. An RFC3339 timestamp supplies an exact cutoff instead. The compared value
is the session's latest event timestamp, not its start or latest checkpoint
time. Invalid dates are rejected before writes.

For a nonempty selector match, selector-based archival requires exactly one of
`--dry-run` or `--yes`.
`--dry-run` prints matching session IDs, tools, derived statuses, and latest
event times in normal newest-first session order, but appends no events.
Without either flag, workgraph reports the match count and instructs the user
to preview or confirm, then exits nonzero without writes. `--yes` applies all
required transitions atomically. A zero-match selection succeeds without
writing an event.

For a known session, archive appends `ai.session_archived` and unarchive
appends `ai.session_unarchived`. Both payloads contain only the schema version
and session ID. The latest supported archive transition by parsed event
timestamp, then event ID, determines current archive state. A missing explicit
session is a nonzero error.

Successful transitions print:

```text
AI session archived
Session: <session-id>
Event: <event-id>
```

or the corresponding `unarchived` form. Requesting the current state is an
idempotent success, appends no event, and prints:

```text
AI session already archived
Session: <session-id>
```

or the corresponding `already unarchived` form.

Multiple explicit IDs and confirmed selector batches print a bounded summary
with matched, changed, and already-in-state counts followed by one session ID
and event ID for each appended transition. Preview and batch summaries never
show paths, native IDs, checkpoint text, or other agent-authored content.

`workgraph help ai archive` includes examples for explicit IDs, a guarded date
selection preview, and confirmed all-session archival. `workgraph help ai
sessions` includes examples for status, archived-only, and limited listings.

## `workgraph ai show`

`ai show` is read-only. It does not inspect current Git or filesystem state,
modify files, launch an agent, or execute or print a vendor-native resume
command.

For a known session it renders:

- session ID, tool, project, derived status, and archive state
- latest native session ID and predecessor workgraph session ID when available
- latest supported stored observed snapshot, whether from start, checkpoint,
  or end, with its observation time and source event
- latest supported agent-stated checkpoint with its event time
- end outcome when available
- explicit integrity or compatibility warnings

Observed and agent-stated sections remain visibly separate. They retain their
own timestamps and are never presented as if they were one simultaneous
snapshot. When no agent checkpoint exists, output includes exactly:

```text
No agent-stated checkpoint recorded.
```

Show succeeds when a recorded checkout has moved or disappeared because it
uses stored evidence only. A missing session is a nonzero error.

## `workgraph ai resume`

`ai resume <session-id>` means native continuation, not context rendering. The
command is process-launching: the explicit invocation authorizes workgraph to
start one local child process. Help output must use the verb `Launch` so this
side effect is clear. Workgraph never performs a network request itself, though
the resumed agent may do so.

Resume requires:

- a known workgraph session that is `ended` or `interrupted`
- a latest native session ID
- a verified adapter for the stored tool
- a stored latest observed cwd that still resolves locally
- a resolvable stored `tool_path`, with the stored basename still matching the
  adapter

Running and `unknown` sessions are rejected conservatively. Missing native
identity or adapter support produces an actionable nonzero error and does not
fall back to a guessed command or silently start a fresh conversation.

Workgraph launches the stored executable directly with the adapter-specific
resume argv listed above, from the latest stored cwd, and wraps that process
through the ordinary `ai run` lifecycle. The new workgraph session receives a
new UUID and records the requested session as its predecessor. The child exit
outcome remains the command outcome. Workgraph does not persist the constructed
argv.

An explicit manual launch using any verified adapter's exact session-ID form
follows the same recognition and predecessor lookup. The prior matching
lifetime is selected by latest parsed event time, then session ID for a
deterministic tie break.

## Schema Evolution and Invalid Stored Data

`schema_version` and `session_id` are the stable envelope across future payload
versions. V1 interprets only schema version 1.

An unknown version's event-specific payload is never rendered as trusted
context. If an older supported observation or checkpoint exists, workgraph may
show it only with an explicit warning that a newer unsupported event exists;
it must not silently call the older evidence the latest session state. Event
type and timestamp remain readable from event columns. An end event with a
valid envelope still establishes `ended` even when its versioned outcome is
unsupported.

Malformed or missing envelopes produce a data-integrity warning and are not
creatively reconstructed from event IDs or prose. `ai sessions` skips events
that cannot be associated with a session and emits a warning; associated
sessions use `unknown` for fields or status that cannot be established.

## Privacy Contract

Workgraph may store:

- generated workgraph and native session IDs, executable basename and resolved
  local path, PID, predecessor links, and opaque process/boot identity
- local timestamps and local paths
- Git project label, checkout identity, branch, HEAD, and bounded dirty paths
- curated checkpoint text explicitly submitted on stdin

Workgraph never automatically captures or persists:

- conversation transcripts or prompts
- terminal buffers, stdout, or stderr
- shell history
- environment variables or their values
- complete command argument lists; a verified adapter may retain only its
  validated native resume ID
- file contents or source diffs
- a persistent machine identifier

Native callbacks may contain fields such as `transcript_path`, but workgraph
allowlists bounded identity metadata and never opens, logs, or persists the
referenced transcript or unknown callback fields.

The credential-pattern check reduces accidental secret storage but cannot
prove arbitrary prose secret-free. Errors and logs must not echo checkpoint
input, argv beyond the executable basename and validated native resume ID,
environment values, file contents, or diffs.

Before terminal rendering, automatically observed strings are converted to
valid UTF-8 and C0/C1 control characters are shown as visible escapes. This
prevents a malicious filename or executable basename from injecting terminal
control sequences. Agent-stated text has already passed the stricter checkpoint
validation contract.

All stored state remains in the user-selected local workgraph database. The AI
session feature has no sync, upload, remote backup, or cross-machine behavior.

## Native Adapter Compatibility

Native continuation is a version-sensitive convenience layered on the stable
local checkpoint protocol. Before calling an adapter compatible with a vendor
release, maintainers run this smoke matrix with a temporary local workgraph
home and disposable sessions:

| Adapter | Launch identity check | Native continuation check | Coexistence check |
| --- | --- | --- | --- |
| Codex | trusted lifecycle callback binds the exact ID | `resume <id>` creates a linked lifetime | an ordinary user hook and the workgraph hooks both run |
| Claude Code | lifecycle callback binds the exact ID | `--resume <id>` creates a linked lifetime | ordinary settings remain active; explicit `--settings` conflicts fail before launch |
| GitHub Copilot CLI | assigned `--session-id=<id>` is stored | `--resume=<id>` creates a linked lifetime | remote control and export remain disabled |
| OpenCode | the injected plugin binds a parent session ID | `--session <id>` creates a linked lifetime | valid inline plugins are preserved and invalid inline config fails before launch |

The smoke run also verifies that a rejected or unavailable binding leaves the
generic wrapped session usable, no transcript or callback extras are stored,
and `ai show` remains available. Repository facts cover deterministic argument
construction, validation, persistence, privacy exclusions, and failure
behavior without requiring installed vendor CLIs or network access. Supported
vendor versions are release-validation evidence, not persisted session data or
a runtime network lookup.

## Implementation and Fact Seam

CLI parsing belongs in `cmd/workgraph/ai.go`; event projection, validation, Git
observation, and lifecycle behavior belong in the root package. The router adds
only the top-level `ai` branch.

Status queries use one narrow process inspector boundary that reports current
boot identity, PID existence, and process-start identity. Production code uses
the local operating-system implementation. Facts inject deterministic evidence
through the same boundary so reboot, PID reuse, permission failure, and missing
platform support are tested without timing assumptions or real machine-state
changes. This boundary must not grant process-control authority to read-only
`sessions` or `show` queries. `resume` has process-control authority only for
the explicitly selected local session and verified adapter command.

## Explicit V1 Deferrals

- transcript scraping or transcript storage
- editor and desktop-only integrations
- arbitrary process discovery and `ai attach`
- automatic checkpoints or automatically generated summaries
- `gh copilot` wrapper adaptation; the verified Copilot adapter targets the
  direct `copilot` executable
- vendor-native selection by ambiguous prefix, name, picker, or "last session"
- nested sessions
- multi-repository sessions
- worktree creation or management
- non-Git project inference without an explicit mapping
- abbreviated session IDs
- pagination, automatic retention, or permanent deletion
- live repository comparison during show
- cloud storage, synchronization, export-for-resume, path remapping, and
  cross-machine continuity
