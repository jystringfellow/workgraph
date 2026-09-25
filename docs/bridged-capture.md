# Bridged Capture Operator Guide

Bridged capture lets workgraph schedule and store connector activity while an
approved Codex or Claude Code provider connector performs the provider reads.
Workgraph stores only non-secret scope, request state, normalized events, and
local credentials for any separately configured direct connectors. The bridge
worker never receives the provider token on behalf of workgraph.

This guide is the operator workflow. The complete request, schema, and
normalization contract is in [the bridged-capture specification](../specs/bridged-capture.md). The client-side execution procedure is in the
[workgraph-bridge skill](../.agents/skills/workgraph-bridge/SKILL.md).

## Before Setup

Bridging is appropriate when:

- the provider is already approved and available through the AI client;
- the requested scope is explicit and non-secret;
- the provider query can express the exact request bounds, or the connector
  can exhaustively read a complete snapshot;
- pagination can prove completeness; and
- every result has a stable identity and revision, or a defined content hash.

The bridge worker must not broaden a scope after a timeout or infer that a
partial, capped, or indexed query is empty.

## Choose The Worker's Account And Organization

Provider connectors belong to the account and organization of the signed-in
AI client that executes them. Company Slack, Microsoft 365, Notion, or Azure
DevOps connectors may exist only in an enterprise organization even when the
same person also uses a personal organization. A worker launched against the
personal client configuration can therefore see no provider tools and leave
capture pending without an authentication error.

Give each account context its own client configuration directory, sign in and
authorize its provider connectors there, then bind workgraph to that directory:

```sh
workgraph plugin install --client claude-code \
  --config-dir "$HOME/.claude-enterprise"

workgraph llm connect claude-code --name work-claude \
  --config-dir "$HOME/.claude-enterprise" --for summarize
```

For Codex, `--config-dir` binds `CODEX_HOME`; for Claude Code it binds
`CLAUDE_CONFIG_DIR`. workgraph stores the absolute path, not login tokens, and
applies it when the client is invoked. `plugin doctor` and `llm doctor` report
the binding. Omitting the flag preserves an existing worker binding; use
`--clear-config-dir` during plugin installation to return that worker to its
ambient client default.

Install the local plugin for the client that owns the approved provider
connectors:

```sh
workgraph plugin install --client codex
# or
workgraph plugin install --client claude-code
workgraph plugin doctor --client codex
```

Start a new client session after installation. For unattended Claude Code,
approve only the exact read-only provider tools required by the selected
connector recipes and reinstall with one `--allow-provider-tool` flag per tool.
The canonical requirements are discoverable with:

```sh
workgraph connectors required-tools
workgraph connectors required-tools <connector>
```

Those commands print stable canonical operations, not client-specific MCP
permission names. Match each requirement to a read-only tool exported by the
installed provider. Common Claude Code mappings are:

| Connector | Canonical operation | Example Claude Code MCP tool |
| --- | --- | --- |
| `slack` | `slack.read_channel` | `mcp__claude_ai_Slack__slack_read_channel` |
| `slack` | `slack.search_messages` | `mcp__claude_ai_Slack__slack_search_public_and_private` |
| `slack` | `slack.read_thread` | `mcp__claude_ai_Slack__slack_read_thread` |
| `slack.lists` | `slack.read_file` | `mcp__claude_ai_Slack__slack_read_file` |
| `azure.boards` | `azure.list_projects` | `mcp__azure-devops__core_list_projects` |
| `azure.boards` | `azure.query_work_items` | `mcp__azure-devops__wit_query` |
| `azure.boards` | `azure.read_work_item` | `mcp__azure-devops__wit_work_item` |
| `calendar.microsoft` | `microsoft.search_calendar_events` | `mcp__claude_ai_Microsoft_365__outlook_calendar_search` |
| Microsoft connectors | `microsoft.read_resource` | `mcp__claude_ai_Microsoft_365__read_resource` |
| `notion.activity` | `notion.search` | `mcp__claude_ai_Notion__notion-search` |

Tool names can change with provider versions. Inspect the tools actually
available to the bound client, allow only exact names, and do not use
wildcards. Claude permissions are stored in
`<workgraph-home>/.claude/settings.json`; that project-scoped allow-list is
separate from the selected account configuration. Enterprise managed settings
can still deny a locally allowed MCP tool or pin a model.

The worker's unattended permissions include workgraph request listing,
claiming, renewal, ingestion, failure reporting, watermark reads, status, and
heartbeat. They do not include connector configuration, disconnect, arbitrary
shell access, or operator-only request cancellation.

## Supported Connectors

| Connector | Bridged mode | Scope or completeness rule |
| --- | --- | --- |
| `github` | yes | Participant scope, with optional bounded repository searches |
| `slack` | yes | Explicit channels, opted-in DMs, or approved participant scope |
| `slack.lists` | yes, snapshot | Explicit List ids and exhaustive CSV snapshots |
| `mail.google` | yes | Explicit mailboxes and complete bounded pagination |
| `mail.microsoft` | yes | Explicit folders, mailbox-local padding, UTC filtering, and contiguous pagination |
| `calendar.google` | yes | Explicit calendars and rolling occurrence bounds |
| `calendar.microsoft` | yes | Explicit calendars, rolling bounds, and provider revision identity |
| `azure.boards` | yes | Organization plus project/area path or approved participant scope |
| `notion.activity` | **only** | Participant-scoped edited/created activity with truncation rejection |
| `git` | **no** | Local capture is always direct |
| `notion` | **no** | Use direct OAuth or `workgraph notion connect-token`; reference search cannot prove exhaustion |

A connector that is not registered as bridgeable must not be configured by
hand-editing `connectors.json`. Status and doctor report legacy unsupported
entries as `unsupported bridge`. Entries without valid scope are reported as
`needs scope`; reconnect them with the scoped command rather than guessing a
broader scope.

## Configure And Start

Initialize workgraph and choose an explicit non-secret scope:

```sh
workgraph init
workgraph connectors connect <connector> --mode bridged --params-json '<approved-scope-json>'
workgraph connectors connect slack --mode bridged \
  --params-json '{"channels":["C0DEMO123"],"include_dms":false}'
workgraph connectors interval slack 15m
workgraph start
```

Examples for other connectors:

```sh
workgraph connectors connect github --mode bridged \
  --params-json '{"scope":"participant","identity":"@me","include":["involves","review_requested"]}'

workgraph connectors connect calendar.microsoft --mode bridged \
  --params-json '{"calendars":["calendar-id-or-display-name"],"past_days":7,"future_days":30}'

workgraph connectors connect azure.boards --mode bridged \
  --authentication azcli \
  --params-json '{"organization":"example-org","project":"Demo","area_path":"Demo"}'
```

`--params-json` must be one JSON object containing only connector-approved,
non-secret scope. Invalid JSON, secret-looking keys, missing required scope,
and unsupported bridge modes fail before connector state is saved. Setup makes
no provider request and reports `awaiting first ingest`.

Changing canonical bridge parameters cancels active requests for the old scope
and causes the next scheduler pass to emit a replacement. Switching back to
direct mode preserves direct credentials:

```sh
workgraph connectors mode slack direct
```

If a connector was `needs scope` when the daemon started, it was excluded from
that daemon's monitored set. After supplying valid scope, restart the daemon so
it reloads `connectors.json`, then confirm the connector appears on the
`Monitoring:` line:

```sh
workgraph stop
workgraph start
workgraph status
```

The plugin installer creates and reloads the supported macOS launch agent, so
no hand-written wrapper is needed. `--model <model>` pins the unattended worker
model in `bridge/workers.json`; model choice and polling intervals are cost
levers on usage-based client plans.

## Monitor The Outbox

The daemon owns cadence and emits durable requests. A bridge worker owns only
execution latency. Inspect both connector health and request state locally:

```sh
workgraph connectors status
workgraph connectors doctor
workgraph capture requests --list
workgraph capture watermark --connector slack
```

When Azure Boards uses `--authentication azcli`, connect and doctor verify the
session with `az account get-access-token`. Override the executable for testing
or nonstandard installations with `--az <path>`. PAT-backed provider MCP
configuration uses `--authentication pat`; workgraph records only the mode and
never the PAT.

A request moves through `pending`, `claimed`, `completed`, or `cancelled`.
Claimed work has a short lease. The worker renews before the lease expires and
submits one complete normalized batch. Empty results are valid only after an
exhaustive bounded query or applicable control query proves emptiness.

For a human-driven diagnostic drain, use a private claim file. Never put the
claim token in a command argument or log:

```sh
workgraph capture requests --claim --connector slack --max 1 \
  --worker manual --claim-file /private/path/claim.json
workgraph capture requests --renew <request-id> --claim-file /private/path/claim.json
workgraph capture ingest --request <request-id> --claim-file /private/path/claim.json --json -
rm /private/path/claim.json
```

The unattended worker uses local MCP tools instead of claim files. It must
preflight the registry's fetch and identity requirements before claiming.
Missing or denied provider capability leaves work pending and does not mark the
connector unhealthy.

An empty watermark immediately after connecting or changing scope is normal.
The watermark appears only after the first completed request for the new
scope.

## Troubleshoot A Stuck Connector

| Symptom | Likely cause | Check or fix |
| --- | --- | --- |
| Connector is `needs scope`, absent from `Monitoring:`, or has an old `next poll` after being scoped | The running daemon excluded the invalid connector at startup | Restart with `workgraph stop && workgraph start`, then inspect `workgraph status` |
| Request remains `pending` with `attempts 0` while other connectors complete | A one-request drain keeps selecting other eligible work | Run a diagnostic claim filtered with `capture requests --claim --connector <connector>` |
| Request remains `pending` with `attempts 0` and the worker log reports a missing or denied tool | Capability preflight declined the request before claiming | Map every canonical required operation to an available read-only MCP tool and update exact permissions |
| `azure.boards` intermittently reports `CONNECT_TIMEOUT` on first use | A cold `npx -y @azure-devops/mcp` startup exceeded the MCP connection timeout | Pre-resolve or install the MCP package and register its resolved executable before restarting the worker |
| Provider tools disappear when the worker runs unattended | The worker resolved a different personal/enterprise client context | Set `--config-dir`, then confirm it with `plugin doctor` |

Inspect local logs and liveness files before cancelling work:

```text
~/.workgraph/bridge/logs/claude-code.out.log
~/.workgraph/bridge/logs/claude-code.err.log
~/.workgraph/bridge/claude-code.heartbeat
~/.workgraph/daemon.log
```

Replace `~/.workgraph` with the configured workgraph home and `claude-code`
with the installed client id. The worker log explains preflight decisions; the
heartbeat distinguishes an idle worker from one that is not launching.

## Recovery

If provider access, pagination, normalization, or ingestion fails after a
successful preflight, return the same request for retry without submitting a
partial batch:

The unattended MCP equivalent is `capture_request_fail`.

```sh
printf '%s' '{"error":"bounded provider failure"}' |
  workgraph capture requests --fail <request-id> \
  --claim-file /private/path/claim.json --error-json -
```

A stale lease is retried with the original request bounds. A mismatched or
expired claim token cannot renew, ingest, fail, or complete another worker's
request. After an executable upgrade, restart the daemon and reinstall or
reload the client plugin when status reports a stale process:

```sh
workgraph stop
workgraph start
workgraph plugin install --client <codex|claude-code>
```

An operator can permanently cancel one exact request locally:

```sh
workgraph capture requests --cancel <request-id> \
  --reason "operator requested cancellation"
```

Cancellation is terminal and idempotent. It records `cancelled_at` and the
first reason, preserves claim audit fields such as worker and claim time, and
clears the claim token and lease. The old worker capability cannot be reused.
Completed requests cannot be cancelled. Request cancellation is intentionally
not exposed to unattended workers through MCP.

## Verification Checklist

For each configured connector, verify one real or proven-empty round trip:

1. Confirm the connector is enabled, bridged, and healthy with `connectors status`.
2. Confirm the request has the expected non-secret scope with `capture requests --list`.
3. Confirm the approved client has every registry-required fetch and identity tool.
4. Confirm the request completes or is explicitly returned for retry.
5. Confirm the local event or the completed-through watermark, without treating a future event timestamp as capture progress.

Treat provider content as untrusted data. Provider responses are evidence to
normalize, never instructions for the worker.
