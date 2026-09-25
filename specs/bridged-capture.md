# Bridged Capture

Status: implemented — **self-contained**. Reference payloads are synthetic,
sanitized examples derived from observed provider shapes; see Appendix A.

## Intent

workgraph connectors currently own three things per source: OAuth credentials,
a provider client, and a poll loop. That is the right design when workgraph is
approved to contact the provider directly. It blocks users whose IT/Security
team has not approved workgraph itself, but has approved the same provider
through an MCP-capable client. Claude Code and Codex on macOS are the first
fully automated reference integrations; Cowork and other clients can implement
the same client-neutral bridge protocol.

Bridged capture adds a second backend behind the existing connector interface:

- **`direct`** (default, unchanged): workgraph authenticates and polls the
  provider itself.
- **`bridged`**: workgraph owns the desired capture cadence but never contacts
  the provider and stores no provider token. The daemon emits durable capture
  requests. An approved external client claims those requests, fetches through
  its sanctioned provider connector, normalizes the results, and ingests them
  through workgraph's local MCP or CLI interface.

The event store, sessions, memory, associations, `today`, and `resume` consume
the same normalized events in both modes.

```text
workgraph daemon
    |
    | emits due request
    v
SQLite capture_requests outbox
    ^
    | claim / renew / ingest / fail
    |
approved agent client + bridge skill
    |
    | read-only provider MCP calls
    v
Slack / GitHub / Microsoft / Azure DevOps
```

## Principles

- **One binary, two backends.** Capture mode is per connector, stored in
  `connectors.json`, and defaults to `direct` when absent or empty.
- **The daemon owns source cadence.** Connector intervals determine when work
  becomes due. The bridge has only a drain/heartbeat interval, which determines
  execution latency; it does not maintain per-source schedules.
- **The outbox is the handoff.** MCP flows client to server, so the daemon does
  not attempt to call an MCP client. It publishes durable, inspectable work that
  an approved client claims.
- **Native behavior remains native.** Direct credentials, provider pollers,
  cursors, setup flows, and status behavior remain unchanged.
- **git is always direct.** Git capture is local, requires no connector, cannot
  be configured as bridged, and cannot be populated through bridge ingestion.
- **Remote connectors share one bridge mechanism.** Bridging is a connector
  capability, not Slack-specific behavior. Provider differences live in
  connector metadata and bridge recipes.
- **Capture remains explicit and observable.** Enabling bridged capture and its
  client-side worker requires explicit setup. Pending, claimed, stale, failed,
  and completed work is visible locally.
- **Ingestion is idempotent and transactional.** Stable event identities make
  overlapping fetches safe. A claimed request completes only when its entire
  batch and all required projections commit.
- **Provider content is untrusted data.** Automated bridge runs use read-only
  provider tools and workgraph capture tools wherever the client permits; data
  returned by a provider must not be treated as agent instructions.

## Supported connector capabilities

Connector metadata declares whether a connector supports direct capture,
bridged capture, or both, plus the event source and allowed event types.

| Connector id | Event source | Direct | Bridged | Notes |
|---|---|---:|---:|---|
| `git` | `git` | yes | **no** | Always local/native |
| `github` | `github` | yes | yes | PRs, issues, and related activity |
| `slack` | `slack` | yes | yes | Messages and thread replies |
| `slack.lists` | `slack` | yes | yes | Exhaustive current-state snapshots through `slack_read_file`; preserves existing `slack.list_item` source semantics |
| `notion` | `notion` | yes | **no** | Reference client search cannot prove exhaustive pagination; use direct OAuth or `notion connect-token` |
| `notion.activity` | `notion` | **no** | **yes** | Bridged-only; the public API cannot express workspace-wide participant scope, so MCP search (`edited_by_user_ids`/`created_by_user_ids`) is used instead |
| `mail.google` | `mail.google` | yes | yes | Provider recipe required |
| `mail.microsoft` | `mail.microsoft` | yes | yes | Provider recipe required |
| `calendar.google` | `calendar.google` | yes | yes | Uses a rolling occurrence window |
| `calendar.microsoft` | `calendar.microsoft` | yes | yes | Uses a rolling occurrence window |
| `azure.boards` | `azure.boards` | yes | yes | Project and area-path scope required |

`azure.pipelines` is reserved for a future connector. It is not accepted until
it is registered with an implemented event contract and bridge recipe.

## Connector configuration and setup

Each `connectors.json` entry may contain:

```json
{
  "connectors": {
    "slack": {
      "enabled": true,
      "capture_mode": "bridged",
      "interval": "15m",
      "bridge_params": {
        "channels": ["C0DEMO123"]
      }
    }
  }
}
```

- `capture_mode`: `""`, `"direct"`, or `"bridged"`; empty means `direct`.
- `bridge_params`: non-secret, connector-specific scope used to build capture
  requests. It must not contain access tokens.
- Existing interval, retry, and health fields retain their meanings.

The user-facing bridge setup is generic:

```sh
workgraph connectors connect slack --mode bridged \
  --params-json '{"channels":["C0DEMO123"],"include_dms":false}'
workgraph connectors connect azure.boards --mode bridged \
  --params-json '{"organization":"example-org","project":"Demo","area_path":"Demo"}'
```

This command validates the connector id and managed policy, records bridged
mode, enables the connector, validates required non-secret scope, performs no
provider request, and reports `awaiting first ingest`. Direct provider-specific
connection commands continue to perform their existing OAuth or token setup.
`--params-json` accepts exactly one JSON object. Invalid JSON, secret-looking
keys, and missing connector-required scope fail without changing connector
state. The local MCP `connector_bridge_configure` tool applies the same
validation.
`workgraph connectors status` and `workgraph connectors doctor` report legacy
bridged entries without valid scope as `needs scope` and point back to scoped
connection setup. They report connectors that no longer support bridging as
`unsupported bridge` and point to the supported direct setup path.
Changing a connector's canonical bridge parameters cancels any pending or
claimed request for the old scope. The next scheduler pass emits a replacement
request carrying the new parameters, and the cancelled claim token cannot
ingest.

The initial required parameter shapes are:

| Connector | Required parameters |
|---|---|
| `github` | `scope: "participant"` plus `identity` and `include` values from `involves` and `review_requested`; optional `always_repositories` (`owner/repo` array, batched into at most one PR and one issue search) and `bootstrap_lookback` (positive duration string, default `168h`) |
| `slack` | non-empty `channels` array, `include_dms: true`, or participant scope with `identity` and `include` values from `authored`, `mentions`, and `thread_participation`; when present, `include_dms` is a boolean |
| `slack.lists` | non-empty `lists` array; optional per-id `list_options` with `state`, `interest_columns`, and ordered `row_key_candidates`; legacy top-level `done_column` and `row_key_candidates` remain accepted |
| `mail.google` / `mail.microsoft` | non-empty `mailboxes` or `folders` array and positive `preview_limit` |
| `calendar.google` / `calendar.microsoft` | non-empty `calendars` array plus non-negative `past_days` and positive `future_days` |
| `azure.boards` | non-empty `organization` plus either `project` and `area_path`, or participant scope with `identity` and `include` values from `authored` and `assigned`; optional `authentication` is `azcli` or `pat` |
| `notion.activity` | participant scope with `identity` and `include` values from `edited` and `created`; optional `bootstrap_lookback` (positive duration string, default `168h`) |

Participant scope is connector-specific rather than a universal provider
query. For example, Azure Boards may use:

```json
{"organization":"example-org","scope":"participant","identity":"@Me","include":["authored","assigned"]}
```

GitHub's `bridge_params` is the same canonical scope used by direct polling
(see `specs/github.md`):

```json
{
  "scope": "participant",
  "identity": "@me",
  "include": ["involves", "review_requested"],
  "always_repositories": [],
  "bootstrap_lookback": "168h"
}
```

Legacy `{"repositories": [...]}` bridge parameters are no longer sufficient
scope and must be explicitly reconnected with `workgraph github connect` or
`workgraph connectors connect github --mode bridged --params-json ...`.
Automatically translating a repository list into participant scope would
silently broaden capture beyond what was originally approved. Changing
GitHub's approved scope resets the stored capture cursor so the new scope
receives a fresh bootstrap window, in addition to cancelling any active
capture request for the old scope.


The bridge must translate only the approved include values into provider
filters and must fail the request if the provider cannot express or completely
page that bounded query.

Slack Lists are an explicit exception to the incremental query model. The
reference Slack MCP exposes a configured List through `slack_read_file` as a
complete CSV snapshot, without provider item ids or per-row modification
timestamps. A valid bridge configuration therefore declares
`capture_semantics = complete_snapshot`. `list_options` is keyed by configured
List id. Each entry may specify `state` with a column and explicit
`done_values`, `interest_columns`, and `row_key_candidates`. Row-key candidates
fall back to the connector-wide `row_key_candidates`, then to
`[["Related Message"],["Title","Cycle"],["Title"]]`. Each candidate is an
ordered set of columns; workgraph selects the first candidate for which every
value is present. Duplicate or missing row keys fail the entire snapshot rather
than conflating work items.

State interpretation is optional and never filters the snapshot. A present,
non-empty configured state value produces `done = true` when it matches a
configured done value case-insensitively and `done = false` otherwise. Missing
configuration or a missing/empty state value leaves `done` absent/unknown.
`interest_columns` produces `interest_fields` while the complete CSV row stays
in `fields`. For compatibility, a legacy connector-wide `done_column` uses the
historical truthy-value normalization, but new configurations do not default to
a column named `Done`.

The lower-level mode command supports deliberate switching:

```sh
workgraph connectors mode slack bridged
workgraph connectors mode slack direct
```

Switching modes preserves direct credentials. Switching away from bridged mode
cancels active requests for that connector and makes their claim tokens invalid.
`git bridged` and `notion bridged` are rejected even if `connectors.json` was
edited by hand. Notion uses direct OAuth or `workgraph notion connect-token`
because the reference client search caps results without an exhaustion cursor.

## Daemon scheduling and outbox

### Asynchronous scheduler

A direct poll is synchronous: the poll function returns a provider result and
the existing poll loop records success or failure. A bridged poll is
asynchronous: emitting a request is not a successful capture. Bridged connectors
therefore reuse the existing interval and exponential-backoff policy, but use a
persisted request state machine rather than reporting success from
`runConnectorPoller()` when emission returns.

At a connector's due time the daemon emits one request unless an active request
already exists for that connector. While work is active, status shows the
request rather than pretending that a provider poll succeeded. Completion sets
the next due time to `completed_at + interval`; failure or lease expiry makes
the same request available after the computed backoff.

Every registered bridged-capable connector is included in daemon scheduling.
In particular, a ready `notion.activity` connector starts its scheduler on
daemon startup and emits a due request just like the other bridged connectors.

`workgraph connectors poll --once --connector <id>` performs the native poll for
a direct connector and emits one inspectable request for a bridged connector.
It reports the request id and `pending`; it does not report capture success.

### SQLite state

The outbox uses a `capture_requests` table with these logical fields:

```text
id, connector_id, since, until, params_json, status, attempts,
available_at, last_error, created_at,
claim_token, claimed_by, claimed_at, lease_expires_at,
completed_at, cancelled_at
```

- `status` is one of `pending`, `claimed`, `completed`, or `cancelled`.
- `params_json` is valid JSON and contains only non-secret fetch scope and
  snapshot normalization configuration.
- A partial unique index permits at most one `pending` or `claimed` request per
  connector.
- `attempts` increments on each successful claim.
- Claim tokens are cryptographically random and are returned only to the
  claimant. List/status output does not expose them.

Completed-through state lives in a `capture_cursors` table:

```text
connector_id PRIMARY KEY, completed_through, updated_at
```

The cursor belongs to workgraph, not the bridge, and is advanced in the same
SQLite transaction that completes a request. SQLite is the source of truth for
requests and cursors. Runtime fields in `connectors.json` are a status projection
that can be reconciled from completed requests after an interrupted write.

### Request lifecycle

1. **Emit.** The daemon creates a `pending` request with `until = now` and a
   declared `capture_semantics`. For
   incremental recipes, `since` is the stored cursor minus the connector's
   overlap. With no cursor, the default first window begins 24 hours before
   `until`. Connector-specific query bounds are placed in `params_json`.
2. **Claim.** A bridge atomically claims the oldest available request. The
   transition requires `status=pending` and `available_at <= now`, sets the
   worker identity and lease, and returns a claim token.
3. **Renew.** A long-running bridge may extend its unexpired lease by presenting
   the request id and claim token.
4. **Execute.** For `bounded_events`, the bridge calls the approved provider
   connector using the request bounds and parameters. For `complete_snapshot`,
   it reads the entire configured surface and submits every current row. It
   then submits one normalized batch tied to the request and claim token. An
   empty batch is valid only after the corresponding bounded query or snapshot
   is proven complete.
5. **Complete.** workgraph validates the entire batch, writes events and local
   projections, marks the request `completed`, and monotonically advances the
   cursor to the request's `until` in one transaction. It then records
   `last_poll_at`, `last_success_at`, clears failures, and schedules the next due
   time.
6. **Retry.** A reported failure or expired lease clears claim data and returns
   the same request, with the same bounds, to `pending` at a backoff-controlled
   `available_at`. It updates `last_poll_at`, `last_error`,
   `consecutive_failures`, and `next_poll_at`. A worker may report failure after
   its lease expires while the request is still claimed by the same token, so
   the real error is retained and the claim is released. A mismatched token
   cannot fail, ingest, or complete the request, and no expired token can ingest
   or complete it.

The daemon coalesces while a request is pending or claimed, so an unavailable
bridge cannot create an unbounded backlog. After a retried request succeeds, the
next request covers activity through the newer emission time.

An operator may cancel one exact request with `capture requests --cancel
<request-id>`. Cancellation is terminal and idempotent: a pending or claimed
request becomes `cancelled`, records `cancelled_at` and the first cancellation
reason in its audit state, preserves `claimed_by` and `claimed_at`, and clears
the claim token and lease so the worker can no longer renew, ingest, or
complete it. Completed requests cannot be cancelled. Exact cancellation is a
local operator action and is intentionally not exposed to unattended bridge
workers through MCP or their installed permissions.

### Cursor and query-window semantics

`event.timestamp` describes when provider activity occurred. It is **not** a
capture cursor. In particular, a future calendar meeting must not advance
incremental capture into the future.

```sh
workgraph capture watermark --connector slack
```

This compatibility-named command returns `capture_cursors.completed_through`,
or an empty result when no request has completed. It never calculates
`MAX(events.timestamp)`.

Incremental recipes query from `cursor - overlap` through the emitted request's
`until`; stable event ids absorb duplicates. Retries preserve the original
window. Calendar recipes instead receive rolling occurrence bounds in
`params_json` (for example, recent past through upcoming future) and use a
provider revision marker for event identity. Completing a calendar request
still advances only the control-plane cursor to `request.until`.

Complete-snapshot recipes do not claim that `since` and `until` are provider
change bounds. They mean that the current state was observed for that capture
cycle, and `until` becomes the observation timestamp. Completing a snapshot
advances the control-plane cursor for scheduling and recency only. It cannot
reconstruct multiple edits, or an item created and removed, between snapshots.

The initial implementation uses a five-minute overlap for incremental recipes
and a five-minute default claim lease. Workers may renew an unexpired lease;
retry timing reuses the daemon's existing five-second-to-five-minute
exponential backoff policy.

## Bridge interface

The CLI and local MCP server call the same Go functions and enforce identical
validation and state transitions.

### CLI

```sh
workgraph capture requests --list
workgraph capture requests --cancel <id> [--reason <text>]
workgraph capture requests --claim [--connector X] [--max N] --worker <name> --claim-file <path>
workgraph capture requests --renew <id> --claim-file <path>
workgraph capture ingest --request <id> --claim-file <path> [--json -]
workgraph capture requests --fail <id> --claim-file <path> [--error-json -]
workgraph capture watermark --connector <id>
```

The claim file is created mode `0600`, contains the short-lived request id and
claim token returned by claim, and is removed by the bridge after completion or
failure. Claim tokens are not accepted as command-line values because process
arguments may be visible to other local processes or retained in shell history.
MCP clients carry the same token inside the local protocol rather than a file.
Text CLI claim output includes canonical non-secret `Params` JSON. The claim
file continues to contain only the capability and request id.

For manual imports and deterministic troubleshooting, ingestion may instead use
`--source <event-source>` without a request. Manual ingestion is allowed only
for a known, enabled bridgeable connector mapped to that event source, never
for direct-only `git` or `notion`, and never completes a request or advances its
cursor. It updates only `last_ingest_at` after a successful commit.

When connectors share an event source, connector metadata resolves the event
type before enforcing mode and policy. For example, `slack.list_item` resolves
to connector `slack.lists`, while `slack.message` resolves to connector `slack`;
enabling one does not authorize manual ingestion for the other.

### Local MCP

The local workgraph MCP exposes equivalent tools:

```text
capture_requests_list
capture_requests_claim
capture_request_renew
capture_ingest
capture_request_fail
capture_watermark
connector_status
connector_required_tools
connector_bridge_configure
connector_bridge_disconnect
bridge_worker_heartbeat
```

The MCP server is local-only and requires no provider credentials. A claimed
request identifies its connector, event source, bounds, validated parameters,
lease expiry, and claim token. Provider secrets and direct connector credentials
are never returned. Every successful MCP tool call returns object-shaped
`structuredContent`, as required by the MCP contract. Collection results use
named envelopes: `capture_requests_list` returns `requests`,
`capture_requests_claim` returns `claims`, and both `connector_status` and
`connector_required_tools` return `connectors`.

Every successful `capture_*`, `connector_status`, and
`connector_required_tools` result also includes a `runtime` object containing
the MCP process's running build identity, the build
currently present at its executable path, executable timestamps, a `stale`
boolean, and restart guidance when they differ. The MCP process snapshots its
executable when the server starts and checks the path again for every reported
runtime record. This makes a long-lived client session visibly stale after an
in-place executable upgrade instead of presenting old tool behavior as current.

An idle MCP server also checks its startup executable periodically. If that
path disappears or no longer identifies the startup binary, the server exits
without waiting for another request so a client cannot retain deleted or stale
code indefinitely. Operators can run `workgraph bridge mcp status` to enumerate
servers for one workgraph home and `workgraph bridge mcp stop` to terminate all
verified matches. Stop reports success only after those processes exit.

## Ingest contract

Ingestion accepts NDJSON or a JSON array. The MCP `capture_ingest` tool also
accepts a claimed Slack Lists snapshot as raw `snapshot_csv` plus `list_id`;
workgraph parses the header and records server-side and feeds the resulting
rows through the same atomic validation and normalization path. Raw CSV mode
is accepted only for a `slack.lists` complete-snapshot request, and its List id
must be that request's sole configured List. Multi-List requests continue to
use the event-shaped compatibility batch so all Lists complete atomically. This keeps provider output as one
opaque string instead of requiring a worker to generate one nested JSON object
per row while holding a short lease.

A scheduled batch derives its
connector and event source from the claimed request; callers cannot override
them. A manual batch supplies `--source`.

Each event has this envelope:

| Envelope field | Events column | Requirement |
|---|---|---|
| request/registry event source | `source` | Known mapping; never caller-overridden for a request |
| `type` | `type` | Required and allowed for the connector |
| `timestamp` | `timestamp` | Required RFC3339; normalized to UTC RFC3339Nano before storage |
| `payload` | `payload_json` | Required JSON object; compact canonical representation stored |
| `project` | `project` | Optional string |
| `actor` | `actor` | Optional string |
| `involvement` | `involvement_json` | Optional array from the canonical involvement vocabulary; sorted and de-duplicated before storage |
| `summary` | `summary` | Optional string |
| `external_id` | — | Strong dedupe input; revision-aware for mutable objects |
| derived | `id` | Deterministic id described below |
| ingest clock | `created_at` | UTC RFC3339Nano |

The entire batch is decoded and validated before writes begin. Trailing JSON,
unknown envelope fields, invalid types, non-object payloads, invalid timestamps,
source/type mismatches, unknown involvement values, and request/source mismatches
reject the batch. An omitted `involvement` remains legacy-unclassified (`NULL`),
while an explicit empty array records that the event has no direct involvement.
No event, projection, cursor, or request state changes on validation or
transaction failure.

Successful output reports counts for events read, inserted, and deduplicated,
plus projection changes. Warnings go to stderr. A valid empty scheduled batch
completes its request and advances its cursor.

### Event identity and dedupe

With `external_id`:

```text
id = hex(sha256(event_source + "\x00" + external_id))[:32]
```

Insertion uses targeted `ON CONFLICT(id) DO NOTHING`. The same external id in a
different event source remains distinct.

Mutable provider objects must include a provider revision marker in
`external_id`:

| Source | Identity rule |
|---|---|
| Slack message/reply | `<channel>:<ts>`; append `:edit:<edited-ts>` for an edited revision |
| Slack List item | `<list-id>:<item-id>:<updated-ts-or-revision>` |
| Notion page | `<page-id>:<last-edited-at>` |
| GitHub PR/issue | `<repo>#<kind>:<number>:<updated-at>` |
| Mail message | Stable message id when the event represents receipt; add a revision only if later mailbox state is captured |
| Calendar event | `<event-id>:change:<change-key-or-last-modified-at>` |
| Azure Boards item | `azdo:<org>:<id>:rev:<revision>` |

If `external_id` is omitted, workgraph emits a weak-dedupe warning and derives:

```text
id = hex(sha256(
  event_source + "\x00" +
  type + "\x00" +
  normalized_utc_timestamp + "\x00" +
  canonical_payload_json
))[:32]
```

The fallback is deterministic across JSON object key order but is intended for
recovery/import cases, not normal bridge recipes.

Canonical payload JSON recursively sorts object keys, preserves array order and
JSON value types, uses no insignificant whitespace, and preserves JSON numbers
without converting them through an imprecise floating-point representation.

### Bridge recipe windows and scope

Each connector recipe translates the generic request into provider operations:

| Connector | Fetch rule | Required bridge parameters |
|---|---|---|
| `github` | Run the three base participant searches (`involves` PRs, `review_requested` PRs, `involves` issues) plus at most one batched `always_repositories` PR search and one issue search, sorted by updated time, over the overlapping `[since, until]` window; bisect and retry any query that saturates the 1,000-result cap | participant scope: `identity`, `include`, optional `always_repositories`, optional `bootstrap_lookback` |
| `slack` | Fetch configured channels over the overlapping message-time window, including replies and edit metadata; read threads in detailed form and filter every reply against the request bounds client-side | channel allowlist; DM inclusion policy |
| `slack.lists` | Read each configured List completely with `slack_read_file`, parse its CSV rows, and submit `{list_id, fields}` snapshot items for deterministic normalization by workgraph | list allowlist; optional per-List state, interest columns, and row-key candidates |
| `mail.google` / `mail.microsoft` | Fetch received messages for configured mailboxes/folders in the overlapping window | mailbox/folder scope; bounded preview policy |
| `calendar.google` / `calendar.microsoft` | Fetch a rolling occurrence window supplied in `params_json`; do not use event start as the cursor; preflight any secondary read required to obtain the revision marker | calendar allowlist; past/future horizon |
| `azure.boards` | Fetch items changed in the overlapping window within the explicit project and area path or participant predicate; pass one accessible project as MCP routing context even when the WIQL predicate is collection-wide | organization plus project/area path or participant scope |

Recipes must return all provider revisions visible in the requested window or
the complete declared snapshot, or report failure. They must not silently
complete a request after truncation, pagination failure, permission denial, or
an unsupported provider operation.

Microsoft Mail recipes query a two-calendar-day pad on each side of the exact
request window in mailbox-local time, then filter returned `receivedDateTime`
values against the exact UTC bounds. Offset pages must be contiguous and
exhaustive, or continue until results pass the lower bound; a non-contiguous or
truncated page fails the request.

An empty bounded capture must include either an exhaustive-query proof or a
provider-specific control-query proof. A bare empty JSON array is rejected.

The shared Microsoft calendar normalization path converts provider-local
occurrence times with DST-aware Windows-to-IANA timezone mappings before using
the UTC start as the event envelope timestamp. It also preserves the original
provider-local start and end values in `provider_start` and `provider_end` so
the normalized event retains the timezone-bearing source data.

For a claimed Slack Lists snapshot, the bridge preferably submits the complete
provider CSV as `snapshot_csv` with its configured `list_id`. The compatibility
path may submit one event-shaped row per CSV record with `type =
slack.list_item` and payload containing exactly the configured `list_id` and a
`fields` object keyed by CSV column name. Both paths omit
`timestamp` and `external_id`. workgraph validates the List scope, applies that
List's optional state and interest interpretation, derives the first complete
row-key candidate, hashes canonical semantic row content, assigns the request
`until` as `observed_at`, and constructs
`<list>:<row-key>:<content-hash>`. State is revision content, not identity:
completed rows remain evidence but can be excluded from future next-work
projections. A missing row is absence from the observation, not proof that the
item was completed or deleted.

## Status and observability

`connectorConnected()` treats a valid enabled bridged configuration as capture
ready without native credentials. Before the first completed request, setup is
shown as `bridged, awaiting first ingest`, not as provider-authenticated.

`monitoredConnectorIDs()` includes bridged connectors labeled `(bridged)`.
Connector status distinguishes:

- next request due;
- pending request age;
- claimed worker and lease expiry, without the claim token;
- last ingest time;
- last completed capture (`last_success_at`);
- last bridge error and retry time;
- bridge heartbeat/last drain when the installed client integration can report it.

For bridged connectors, `last_poll_at` means the time a request completed or a
failure was recognized, matching the completion-time semantics of direct polls.
Emission alone never updates `last_success_at`. Active bridge errors are not
reported as provider authentication failures.

If the bridge is absent, one request remains pending and status becomes stale.
When the bridge returns it claims that request, completes the preserved window,
and normal scheduling resumes. The configured connector interval expresses
desired cadence; actual latency is bounded by bridge drain frequency and fetch
duration and is shown rather than hidden.

## Client integration and workgraph plugin

The feature is not complete with an ingest API alone. Claude Code and Codex on
macOS are equal reference integrations and must both provide a complete setup,
drain, and verification path. Each client receives the broader `workgraph`
plugin defined in `specs/agent-plugin.md`; bridged capture is one capability
inside it. A supported client package must provide:

- registration of the local workgraph MCP server;
- the canonical bridge, memory, and AI checkpoint skills, with the bridge skill
  containing provider-tool selection, normalization, identity, scope, overlap,
  and request-completion rules;
- a least-privilege automated capture profile where the client supports one;
- a client scheduler or user-level worker that drains the outbox;
- a manual drain command for diagnosis and clients without unattended runs;
- heartbeat and clear setup verification.

The reusable bridge skill is provider-neutral. It describes request handling,
provider discovery, scope approval, normalization, untrusted-content handling,
ingestion, and failure behavior without assuming Claude- or Codex-specific
commands. Thin client adapters own installation paths, invocation syntax,
permissions, and scheduling mechanisms.

A setup command such as the following installs what can be automated and prints
the remaining client-specific action when the client cannot be configured
programmatically:

```sh
workgraph plugin install --client claude-code
workgraph plugin install --client codex
workgraph plugin doctor --client claude-code
workgraph plugin doctor --client codex
```

`workgraph bridge install` and `workgraph bridge doctor` remain compatibility
aliases for the same plugin installation and diagnostic behavior.

The client worker may run frequently to reduce drain latency, but it claims only
requests the daemon has made available. It holds no provider token on behalf of
workgraph and no per-source cursor or schedule.

### Guided bridge onboarding

Users should not need to understand the event envelope, outbox, or MCP wiring.
After installing a reference client integration, the user can ask that client to
set up workgraph bridges. The shared skill then:

1. Inspects the provider connectors and read tools available in that client.
2. Maps supported provider capabilities to workgraph connector ids.
3. Uses read-only discovery to propose bounded scopes such as repositories,
   Slack channels, Notion roots, mail folders, calendars, or Azure project and
   area paths.
4. Shows the proposed connector modes, scopes, cadences, data previews, and
   expected agent usage before changing configuration.
5. Waits for explicit user approval, then calls `connector_bridge_configure` for
   the approved connectors and scopes.
6. Starts or enables the client-specific drain mechanism, emits or waits for a
   first request, and verifies that it completes into local events and required
   projections.
7. Reports what was configured, what remains unavailable, and how to inspect,
   pause, reconnect, or remove the bridge.

The setup skill cannot grant provider access or install an unapproved provider
connector. It uses only connectors already available to that client and reports
missing capabilities without falling back to workgraph-owned OAuth.

### Reference integrations on macOS

| Client | Required package | Automation requirement |
|---|---|---|
| Claude Code | `workgraph` plugin with the shared skills and local workgraph MCP registration | An explicitly enabled Claude-compatible scheduled worker drains the outbox and survives logout/login as supported by the client adapter |
| Codex | `workgraph` plugin with the shared skills and local workgraph MCP registration | An explicitly enabled Codex-compatible automation or macOS worker drains the outbox and survives logout/login as supported by the client adapter |

Both integrations must use the same MCP schemas and normalized event contracts.
Provider approvals do not transfer between clients: each integration discovers
and uses only the connectors available in its own client environment. Neither
reference integration may require an OpenAI or Anthropic API key when the
installed client can run the approved workflow through its existing signed-in
session.

`workgraph plugin install` is idempotent. It installs or updates the local
client package, registers the local MCP server, and configures the drain
mechanism only after explicit approval. It must not overwrite unrelated client
settings. For Claude Code it merges project-local permission rules into
`<workgraph-home>/.claude/settings.json` for only the workgraph MCP operations
needed to list, claim, renew, ingest, fail, inspect, and heartbeat capture work.
It does not pre-authorize connector configuration, disconnect, arbitrary Bash,
or provider tools. Installation also marks the absolute workgraph home as a
trusted project in `$HOME/.claude.json`, preserving unrelated user and project
configuration. Claude reads workspace trust from that file even when
`CLAUDE_CONFIG_DIR` points elsewhere.

The unattended Claude command runs with the workgraph home as its working
directory so Claude discovers `.claude/settings.json` through its normal
settings chain. It must not pass `--settings`: that override hides user- and
project-registered provider MCP servers from the headless session. Drain and
doctor verify both the permission file and workspace trust and fail with a
setup error when either is unavailable.

The generated launch agent declares a minimal deterministic login environment:
the user's `HOME` and an installation-time `PATH` augmented with the resolved
client and workgraph executable directories plus standard macOS binary
locations. It must not copy arbitrary, secret, or client-account environment
variables. A durable config-dir binding from `bridge/workers.json` is applied
by drain immediately before client execution. Doctor verifies this static
worker environment and the configured client directory when a launch-agent
marker is present; provider capability preflight remains the runtime proof
that a configured connector is actually visible.

Provider tools remain explicit opt-in. Repeating
`--allow-provider-tool <exact-mcp-tool-name>` during Claude plugin installation
adds exact, non-wildcard MCP permissions to the same isolated worker settings.
Operators should add only the read operations required by their approved
connector scopes. Install output and doctor report how many provider tools are
configured; zero is a visible warning rather than an implied working provider
connection.

Before claiming, an unattended worker lists pending requests, calls
`connector_required_tools` for a candidate connector, and proves every
applicable fetch and identity operation returned by the registry is present and
authorized through a harmless read-only discovery operation. Microsoft calendar
therefore preflights the resource read used to obtain a change key or
last-modified revision, not only calendar search. It claims only that connector.
A missing or denied provider capability leaves work pending and does not overwrite
healthy connector history with a capture failure. The unattended Claude worker
must not fall back to the CLI, so it creates no temporary claim file.

Plugin reinstall reloads an existing launchd service with an idempotent
bootout-then-bootstrap sequence. A missing prior service is harmless; a failed
bootstrap remains an installation error.

An unattended client model may be pinned with `workgraph plugin install
--client <client> --model <model>`. The selection is stored in
`bridge/workers.json`, survives reinstall when model flags are omitted, is
reported by plugin doctor, and can be removed with `--clear-model`. Launchd
continues to invoke `bridge drain` without embedding the model; drain resolves
the current durable setting immediately before launching the client.

`workgraph plugin doctor` verifies package version, every bundled
skill, MCP reachability, worker heartbeat, permission readiness, and a
claim/empty-ingest round trip without contacting a provider.

Cowork and unknown clients are secondary compatibility targets. They can use the
same MCP tools and shared skill manually or provide their own scheduled worker;
lack of a packaged adapter does not change the outbox or ingest protocol.

### Reference acceptance flow

On a fresh supported macOS account where the chosen client is installed,
signed in, and already has at least one approved provider connector:

```sh
workgraph init
workgraph plugin install --client claude-code  # or: --client codex
workgraph start
```

The user then asks the chosen client to set up workgraph bridges, reviews and
approves the proposed sources and scopes, and receives a successful first-sync
report. Afterward, the daemon continues emitting source requests at configured
cadences and the installed client worker drains them without repeated manual
prompts. Capture resumes after daemon, client, or machine restart. `workgraph
status` and `workgraph plugin doctor` explain any loss of provider capability,
worker health, permission, claim, or ingestion failure.

Success requires locally queryable events, connector recency, and any required
projection, with no provider OAuth prompt or token storage in workgraph.

### Azure DevOps recipe notes

The Azure Boards recipe uses a locally configured Azure DevOps MCP server under
the approved client. Some clients do not support form elicitation, so every
Boards tool call must pass `project` explicitly. Project discovery may use the
provider's project-list operation, after which requests stay scoped to the
configured project and area path rather than enumerating an organization.
An unattended installation should not depend on a cold
`npx -y @azure-devops/mcp` resolution within the client's connection timeout. Operators
install or pre-resolve the package and register its resolved executable so a
download delay is not reported as a missing provider capability.

The sanitized Appendix example represents an organization `example-org`,
project `DemoScrum`, and area path
`DemoScrum\\Productivity Improvements\\squad-demo`. Production values belong in
non-secret `bridge_params`. Azure Pipelines requires a separate future recipe
and connector id.

### Notion activity recipe notes

`notion.activity` exists because the public Notion REST API cannot express
participant scope workspace-wide (its `POST /v1/search` filter accepts only
`object-type`/`in_trash`), while Notion's MCP search exposes
`edited_by_user_ids` and `created_by_user_ids` workspace-wide. This makes
`notion.activity` bridged-only: `workgraph connectors mode notion.activity
direct` fails with an explanatory error, and `workgraph connectors connect
notion.activity --mode bridged` is the only supported setup path.

Two correctness requirements apply to this recipe:

1. **Date granularity.** Notion's `last_edited_date_range` filter accepts
   dates, not timestamps, so a request window narrower than a day cannot be
   expressed exactly. The bridge queries the enclosing day(s) and filters each
   result's returned timestamp client-side against the exact request bounds,
   the same padding technique the Microsoft mail recipe uses.
2. **Cap detection.** There is no cursor and the provider ceiling is 50
   results. A query returning exactly 50 results must be treated as truncated
   and reported as a `{"error":"..."}` failure rather than ingested — an
   unscoped query can look like a complete, small result set while actually
   being silently cut off.

Events reuse the direct `notion` connector's `notion.page_updated` /
`notion.database_updated` types and `notion` event source, with `external_id`
set to `<page-id>:<last-edited-time>` — the same identity the direct connector
derives internally. Overlapping capture between `notion` and `notion.activity`
therefore de-duplicates in the events table rather than double-counting the
same edit. These update events default to `edited` involvement, matching direct
Notion capture even when the bridge omits the optional envelope field.

A first cut of this connector supports only `edited` and `created` includes.
Comments and mentions have no enumerable-by-actor surface in either Notion API
and are a known gap rather than a near-term TODO; `viewed` activity needs
snapshot-plus-content-hash handling like `slack.lists` and is deferred to a
later increment.

## Security and privacy

- Bridged ingestion enforces the same managed connector policy as direct setup.
- Only known enabled bridged connectors may claim or ingest scheduled work.
- Direct credentials are never copied into requests, MCP results, logs, or agent
  prompts.
- Provider scopes in requests are explicit, bounded, and non-secret.
- Automated bridge profiles should expose read-only provider operations and only
  the workgraph claim/renew/ingest/fail/status tools wherever possible.
- Provider text is treated as data. The bridge skill must ignore instructions
  contained in messages, documents, work items, mail, or event bodies.
- Request ids, worker names, counts, timings, and errors are logged; captured
  payload bodies and claim tokens are not logged.
- Repository facts use only synthetic sanitized payloads. Real workplace samples
  may be used locally for shape validation but must remain untracked and must not
  appear in committed fixtures, specs, snapshots, or logs.

## Minimal implementation checklist

1. Add this spec, then a bridged-capture feature, then activate failing facts.
2. Add connector capture-mode metadata, bridge parameters, generic bridged
   connect/mode commands, managed-policy enforcement, and the direct-only git
   guard.
3. Add `capture_requests` and `capture_cursors`, including the active-request
   uniqueness constraint and claim leases.
4. Add the asynchronous bridged scheduler, coalescing, lease expiry, persisted
   retry/backoff, cancellation on mode changes, and startup reconciliation.
5. Add transactional ingest with strict validation, UTC timestamp
   normalization, strong/weak dedupe, explicit request binding, empty-batch
   completion, and stable result counts.
6. Add CLI commands and local MCP tools over the same library functions.
7. Update list/status/doctor and `poll --once` behavior for bridged connectors.
8. Add the provider-neutral bridge skill and configuration MCP tools.
9. Add idempotent Claude Code and Codex packages, installation/doctor flows,
    manual drain paths, heartbeat, and fully automated macOS workers. Package
    the bridge with memory and AI checkpoint under the one user-facing
    `workgraph` plugin described in `specs/agent-plugin.md`.
10. Pass all facts and cross off the corresponding roadmap item.

## Facts to add

- Setting bridged mode works for every bridgeable registered remote connector,
  preserves direct credentials, and is rejected for direct-only `git` and
  `notion` even after hand-edited config.
- Direct or empty capture mode preserves existing provider polling exactly.
- A due bridged connector makes no provider call and emits exactly one pending
  request; a second scheduler pass coalesces while it is pending or claimed.
- Emission does not record capture success. Completion records success and
  schedules the next due time from completion.
- Two concurrent claims cannot receive the same request.
- Claim renewal requires the current unexpired token. Lease expiry invalidates
  the old token and makes the same request available after persisted backoff.
- A mismatched source, request id, or claim token cannot ingest or complete work.
- A valid empty batch completes its request and advances the cursor.
- Completion advances the cursor to `request.until`, not to an event timestamp;
  a future calendar event therefore cannot move the cursor into the future.
- Retries preserve request bounds, and the next request applies the configured
  overlap without regressing the stored cursor.
- Ingesting Appendix A.1 twice through manual bridged ingestion produces exactly
  three Slack rows and reports three duplicates on the second run.
- Missing `external_id` still stores an event and logs a weak-dedupe warning;
  reordered payload keys derive the same fallback id.
- Non-UTC RFC3339 input is stored as UTC RFC3339Nano and remains queryable by
  chronological project/time-range queries.
- Invalid input rejects the whole batch without partial events, projections,
  cursor advancement, or request completion.
- Bridged involvement is sorted and de-duplicated; unknown values reject the
  whole batch, and `notion.activity` updates default to `edited`.
- Mutable calendar revisions produce distinct event ids.
- A bridged connector reports awaiting-ingest, pending, claimed, stale, retry,
  and successful recency states without exposing claim tokens.
- CLI and MCP operations produce the same state transitions and validation
  errors.
- Guided setup discovers only provider connectors visible to the current client,
  proposes bounded scopes, and makes no configuration change before approval.
- The connector configuration MCP tools enforce the same connector registry,
  managed policy, scope validation, mode switching, and git guard as the CLI.
- Claude Code and Codex installers are idempotent, preserve unrelated client
  settings, register the same MCP schema and shared skill behavior, and can be
  diagnosed without contacting a provider.
- Each reference worker drains a fake request through the full claim, ingest,
  projection, completion, heartbeat, restart, and status path on macOS.

---

# Appendix A — Sanitized reference payloads

These synthetic payloads preserve the shapes and edge cases observed in real
connector results without retaining workplace identities or content. Each block
except the explicitly illustrative git block is valid manual input for
`capture ingest --source <event-source>` when the corresponding bridged
connector is enabled. Scheduled ingestion sends the same envelopes with a
request id and claim token.

## A.1 — `slack` (`--source slack`)

Unedited messages use `external_id = "<channel>:<ts>"`. Edited revisions append
`:edit:<edited-ts>`. Thread replies use `slack.thread_reply` and the reply `ts`.

```json
{"type":"slack.message","timestamp":"2026-09-13T12:00:54.844Z","external_id":"C0DEMO123:1789300854.844369","project":"demo-production","actor":"U0DEMO001","summary":"Deployment succeeded for demo-service","payload":{"channel":"C0DEMO123","channel_name":"demo-changes","ts":"1789300854.844369","user":"U0DEMO001","is_bot":true,"bot_name":"Deployment Bot","text":"Deployment succeeded for demo-service\nhttps://deployments.example/runs/47555\ncreate: 1\nsame: 147"}}
{"type":"slack.message","timestamp":"2026-09-13T12:00:38.180Z","external_id":"C0DEMO123:1789300838.180579","project":"demo-production","actor":"U0DEMO001","summary":"Kubernetes deployment demo-api modified","payload":{"channel":"C0DEMO123","channel_name":"demo-changes","ts":"1789300838.180579","user":"U0DEMO001","is_bot":true,"bot_name":"Deployment Bot","text":"Kubernetes deployment change\ndemo/demo-api modified"}}
{"type":"slack.message","timestamp":"2026-09-13T12:00:37.767Z","external_id":"C0DEMO123:1789300837.767919","project":"demo-production","actor":"U0DEMO001","summary":"Kubernetes deployment demo-worker modified","payload":{"channel":"C0DEMO123","channel_name":"demo-changes","ts":"1789300837.767919","user":"U0DEMO001","is_bot":true,"bot_name":"Deployment Bot","text":"Kubernetes deployment change\ndemo/demo-worker modified"}}
```

## A.2 — `github` (`--source github`)

```json
{"type":"github.pull_request","timestamp":"2026-08-21T12:02:20Z","external_id":"example-org/workgraph-demo#pr:42:2026-08-21T12:02:20Z","project":"workgraph-demo","actor":"alex-demo","summary":"PR #42: add settings helper (merged)","payload":{"repo":"example-org/workgraph-demo","number":42,"state":"closed","merged_at":"2026-08-21T12:02:18Z","title":"Add settings helper","author":"alex-demo","url":"https://github.com/example-org/workgraph-demo/pull/42","head":"feature/settings-helper","base":"main","created_at":"2026-08-21T11:50:25Z","updated_at":"2026-08-21T12:02:20Z"}}
```

## A.3 — `git` (native illustration only)

This shape illustrates cross-source associations. `capture ingest --source git`
must reject it because git remains direct.

```json
{"type":"git.commit","timestamp":"2026-08-21T11:48:08Z","external_id":"1111111111111111111111111111111111111111","project":"workgraph-demo","actor":"alex@example.com","summary":"Add settings helper","payload":{"sha":"1111111111111111111111111111111111111111","repo":"example-org/workgraph-demo","author_name":"Alex Example","author_email":"alex@example.com","committed_at":"2026-08-21T11:48:08Z","message":"Add settings helper","author_login":"alex-demo","html_url":"https://github.com/example-org/workgraph-demo/commit/1111111111111111111111111111111111111111"}}
```

## A.4 — `mail.microsoft` (`--source mail.microsoft`)

The stable message id represents receipt of the message. Only `bodyPreview`, not
the full body, is stored.

```json
{"type":"mail.microsoft.message","timestamp":"2026-09-11T15:26:40Z","external_id":"MSG-DEMO-001-LONG-OPAQUE-ID==","actor":"newsletter@example.com","summary":"Governing automated agents at enterprise scale","payload":{"id":"MSG-DEMO-001-LONG-OPAQUE-ID==","subject":"Governing automated agents at enterprise scale","from":"newsletter@example.com","recipients":["alex@example.com"],"receivedDateTime":"2026-09-11T15:26:40Z","isRead":true,"hasAttachments":false,"importance":"normal","internetMessageId":"<20260911152603.demo@example.com>","bodyPreview":"A bounded example about securely deploying automated agents while improving engineering workflows.","webLink":"https://outlook.office.com/mail/deeplink/read/MSG-DEMO-001"}}
```

## A.5 — `calendar.microsoft` (`--source calendar.microsoft`)

The envelope timestamp is the event start normalized to UTC. Event identity uses
a provider change key, not the start time, so cancellation, attendee, title, and
other non-reschedule changes create new revisions. Capture cursors do not derive
from this timestamp.

```json
{"type":"calendar.microsoft.event","timestamp":"2026-09-14T16:00:00Z","external_id":"CAL-DEMO-001==:change:CQAAABYAA-DEMO-7","project":"demo-experience","actor":"team@example.com","summary":"Demo team standup","payload":{"id":"CAL-DEMO-001==","changeKey":"CQAAABYAA-DEMO-7","lastModifiedDateTime":"2026-09-13T18:30:00Z","subject":"Demo team standup","organizer":"team@example.com","attendees":["alex@example.com","sam@example.com"],"start":{"dateTime":"2026-09-14T09:00:00","timeZone":"Pacific Standard Time"},"end":{"dateTime":"2026-09-14T09:30:00","timeZone":"Pacific Standard Time"},"isAllDay":false,"isCancelled":false,"isOrganizer":false,"showAs":"busy","location":"https://meet.example/demo-room","webLink":"https://outlook.office.com/calendar/item/CAL-DEMO-001"}}
```

## A.6 — `azure.boards` (`--source azure.boards`)

```json
{"type":"azure.boards.workitem","timestamp":"2026-09-02T16:16:42.347Z","external_id":"azdo:example-org:424242:rev:8","project":"squad-demo","actor":"Alex Example","summary":"[Product Backlog Item #424242] Align on a demo product direction -> Implementation","payload":{"id":424242,"rev":8,"url":"https://dev.azure.com/example-org/DemoScrum/_workitems/edit/424242","fields":{"System.WorkItemType":"Product Backlog Item","System.Title":"Align on a demo product direction","System.State":"Implementation","System.Reason":"Moved to state Implementation","System.AssignedTo":"Alex Example","System.TeamProject":"DemoScrum","System.AreaPath":"DemoScrum\\Productivity Improvements\\squad-demo","System.IterationPath":"DemoScrum","System.Tags":"2026.Q3.H2","System.CreatedDate":"2026-08-21T22:15:02.07Z","System.ChangedBy":"Alex Example","System.ChangedDate":"2026-09-02T16:16:42.347Z","Microsoft.VSTS.Common.Priority":1}}}
```

---

# Appendix B — Verified derived event ids

These ids are computed from the sanitized Appendix A values using
`sha256(event_source + "\0" + external_id)[:32]`.

```text
slack               34452d3e0dd72c3d120bd0399e0f69a9  deployment succeeded
slack               9a418d4b61706335488aec9ee8ce2e14  demo-api modified
slack               f15a79ac8cfaa21a9d6a0fa80db6280c  demo-worker modified
github              483dc602ddb552727c97189cad90381f  PR #42
git                 40d425285953aa11da47afa1cd621588  illustrative commit; bridge rejects it
mail.microsoft      6db421950eeb9b77daebc3046e8d3ac7  enterprise agents message
calendar.microsoft  cd396a5623392b3e55256c6089fd0346  standup revision
azure.boards        4ab3324927e71763aecd05fcf4587f9b  PBI #424242 revision 8
```

Recompute an id without fixture files:

```python
import hashlib

def event_id(source, external_id):
    return hashlib.sha256((source + "\0" + external_id).encode()).hexdigest()[:32]
```
