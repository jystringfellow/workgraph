# Connectors

Connectors let workgraph capture context from services you already use. Setup is
local-first: credentials and connector settings are stored under
`~/.workgraph/`, and routine capture runs when you explicitly start workgraph.

```sh
workgraph start
```

Use connector controls to see what will be monitored:

```sh
workgraph connectors list
workgraph connectors status
workgraph connectors doctor
workgraph connectors upgrade
workgraph connectors disable <connector>
workgraph connectors enable <connector>
workgraph connectors interval <connector> 15m
```

`connectors doctor` reports local connector state that needs attention, such as
legacy configs without setup handoff state or credentials that recently failed
with invalid-auth errors. `connectors upgrade` performs a local-only
reconciliation of `connectors.json`; it does not contact provider APIs or
overwrite stored tokens.

## Bridged remote connectors

When workgraph itself is not approved for a provider OAuth connection, a
signed-in Codex or Claude Code installation can execute capture through provider
connectors already approved in that client. Install the workgraph agent plugin:

```sh
workgraph plugin install --client codex
# or
workgraph plugin install --client claude-code
workgraph plugin doctor --client codex
```

For Claude Code unattended capture, explicitly approve the exact read-only
provider MCP tools used by the configured connector recipes:

```sh
workgraph plugin install --client claude-code \
  --allow-provider-tool mcp__azure-devops__wit_query \
  --allow-provider-tool mcp__azure-devops__wit_work_item
```

For the provider versions exercised by the reference verification, a complete
Slack message/List plus Azure Boards read set is:

```text
mcp__claude_ai_Slack__slack_search_public_and_private
mcp__claude_ai_Slack__slack_read_channel
mcp__claude_ai_Slack__slack_read_thread
mcp__claude_ai_Slack__slack_read_file
mcp__claude_ai_Slack__slack_list_user_channels
mcp__azure-devops__wit_query
mcp__azure-devops__wit_work_item
mcp__azure-devops__core_list_projects
```

Tool names are provider-version-specific. Approve only tools that are present,
read-only, and needed for the selected scopes; do not copy the list blindly.

A provider connector is bridgeable only if it can express the bounded window
exactly, paginate to exhaustion with proof of completeness, and assign every
item a stable identity plus revision (or a defined content-hash surrogate).
The reference-client matrix is:

| Connector | Bridged | Required scope / completeness rule |
|---|---:|---|
| `slack` | yes | Explicit channels or the approved `@Me` participant strategy; exhaust channel and thread pagination |
| `slack.lists` | yes | Explicit list ids; use a canonical semantic-row content hash when no revision exists |
| `mail.microsoft` | yes | Explicit mailboxes/folders; query a mailbox-local ±2-day pad, filter returned UTC `receivedDateTime`, and page contiguously past the lower bound |
| `calendar.microsoft` | yes | Explicit calendars and horizons; map `Pacific Standard Time` to DST-aware `America/Los_Angeles`, emit occurrence start in UTC, and preserve provider start/end values |
| `azure.boards` | yes | Explicit organization plus project/area path or the approved `@Me` participant strategy |
| `notion` | **no** | Reference search caps results without an exhaustion cursor; use direct OAuth or `workgraph notion connect-token` |

An empty batch is valid only after an exhaustive bounded query or an applicable
control query proves emptiness. Never infer emptiness from one zero-result
search when the provider search is indexed, capped, or partial.

Installation records the workgraph home as a trusted project in
`$HOME/.claude.json`. The worker starts in that directory, allowing Claude to
discover `.claude/settings.json` through its normal settings chain while still
loading user- and project-registered provider MCP servers. It deliberately does
not pass `--settings`, because that override hides those provider servers in a
headless session. The worker preflights provider capability before claiming, so
a missing provider permission leaves work pending rather than degrading
connector health.

For Azure DevOps MCP configured with Azure CLI authentication, `az account
show` proves only that a cached profile exists. Verify unattended readiness with
`az account get-access-token`; consider a predictably expiring PAT when the
client connector supports one and long-lived unattended behavior matters. For
Azure DevOps-only identities, `az login` may report `No subscriptions found`;
that message is not itself an Azure DevOps authentication failure, so use the
token command and an actual read-only Azure DevOps discovery call as the check.

The plugin also contains the `workgraph-memory` and
`workgraph-ai-checkpoint` skills alongside `workgraph-bridge`. Start a new
client session after installation so all three skills and the local MCP are
discovered. Rerunning the install command refreshes the complete plugin after a
workgraph upgrade. An already running daemon still holds the previous binary in
memory, so run `workgraph stop` followed by `workgraph start` after upgrading;
plugin installation reloads the bridge worker but does not restart the daemon.

Then ask that client to set up workgraph bridges. It discovers available
read-only provider tools, proposes non-secret scopes and cadences, waits for
approval, and configures the local workgraph MCP. A connector can also be put in
bridged mode explicitly:

```sh
workgraph connectors connect slack --mode bridged \
  --params-json '{"scope":"participant","identity":"@Me","include":["authored","mentions","thread_participation"]}'
workgraph connectors interval slack 15m
workgraph start
```

The daemon emits bounded capture requests on the configured cadence. The
installed per-user worker drains those requests; workgraph never receives the
provider token and never calls that provider directly. Inspect operation with:

```sh
workgraph capture requests --list
workgraph capture watermark --connector slack
workgraph connectors status
workgraph bridge drain --client codex
```

Bridging is supported for registered remote connectors with event contracts.
Local `git` and Notion capture cannot be bridged. Switching a connector back to
`direct` preserves any direct credentials already stored by workgraph:

```sh
workgraph connectors mode slack direct
```

See the [bridged capture specification](../specs/bridged-capture.md) for the
outbox lifecycle, claim security, source matrix, and normalization contracts.

## Slack

Connect Slack:

```sh
workgraph slack connect
```

Collect specific channels while connecting:

```sh
workgraph slack connect --channel C1234567890
```

Opt into direct and group direct messages explicitly:

```sh
workgraph slack connect --include-dms
```

If admin-managed settings lock Slack DM capture off, workgraph refuses
`--include-dms` before opening Slack OAuth and refuses capture startup if stored
Slack settings would poll DMs.

If you use a Slack List as a todo list, save its List id while connecting:

```sh
workgraph slack connect --list <list-id>
```

`workgraph start` then monitors that List as connector `slack.lists`.

Run a one-off Slack List capture for debugging:

```sh
workgraph slack lists capture --list-id <list-id>
```

Disconnect Slack:

```sh
workgraph slack disconnect
```

## Notion

Notion capture is direct-only. The reference client search caps results without
an exhaustion cursor, so it cannot prove that a bridged request is complete.

Connect Notion:

```sh
workgraph notion connect
```

Connect Notion with a local internal integration token when OAuth is not
practical:

```sh
workgraph notion connect-token --token <token>
```

Disconnect Notion:

```sh
workgraph notion disconnect
```

Inspect Notion's local object index and captured page previews:

```sh
workgraph notion index list
workgraph notion index show <notion-page-or-database-id>
```

Run a one-off Notion capture for debugging:

```sh
workgraph notion capture
```

## Azure Boards

Connect Azure Boards:

```sh
workgraph azure boards connect \
  --organization <org> \
  --project <project> \
  --team <team>
```

Limit capture to one or more area paths:

```sh
workgraph azure boards connect \
  --organization <org> \
  --project <project> \
  --team <team> \
  --area-path '<area-path>'
```

Multiple `--area-path` flags are allowed and are combined as alternatives in
the default WIQL query. You can also provide a custom query:

```sh
workgraph azure boards connect \
  --organization <org> \
  --project <project> \
  --wiql '<wiql>'
```

Azure Boards uses the Microsoft OAuth PKCE flow and stores local connector
settings in `~/.workgraph/azure-boards.json`. After connecting, `workgraph
start` monitors matching work items as connector `azure.boards`.

Run a one-off capture for debugging:

```sh
workgraph azure boards capture \
  --organization <org> \
  --project <project> \
  --team <team>
```

Disconnect Azure Boards:

```sh
workgraph azure boards disconnect
```

## Calendar

Connect a calendar provider:

```sh
workgraph calendar connect google
workgraph calendar connect microsoft
```

Collect specific calendars while connecting:

```sh
workgraph calendar connect google --calendar-id <calendar-id>
workgraph calendar connect microsoft --calendar-id <calendar-id>
```

Disconnect a calendar provider:

```sh
workgraph calendar disconnect google
workgraph calendar disconnect microsoft
```

If Google Calendar polling reports `invalid_grant`, reconnect with a fresh
credential:

```sh
workgraph calendar disconnect google
workgraph calendar connect google
workgraph connectors poll --once --connector calendar.google
```

When Google reports that the old token is already invalid, disconnect removes
that credential locally so the new OAuth connection can proceed. Settings for
other calendar providers are preserved.

Run a one-off capture for debugging:

```sh
workgraph calendar capture --provider google --calendar-id <calendar-id>
workgraph calendar capture --provider microsoft --calendar-id <calendar-id>
```

## Mail

Connect a mail provider:

```sh
workgraph mail connect google
```

Disconnect a mail provider:

```sh
workgraph mail disconnect google
```

Run a one-off capture for debugging:

```sh
workgraph mail capture --provider google --mailbox-id <mailbox-id>
```

## Git And GitHub

Git capture is local and does not require account connection:

```sh
workgraph git connect
workgraph git capture
```

GitHub capture currently supports deterministic capture commands and local
remote-derived context:

```sh
workgraph github connect
workgraph github capture
```

`workgraph github connect` validates the authenticated `gh` CLI and enables
GitHub in the shared connector runtime.

You can rerun validation without changing provider credentials:

```sh
workgraph connectors validate github
```

## Manual Capture

Manual `capture` commands remain useful for imports, backfills, deterministic
tests, and troubleshooting. Routine capture should normally be:

```sh
workgraph init
workgraph <provider> connect
workgraph start
```

`workgraph start` should include enabled connected providers by default unless
you disable them with `workgraph connectors disable <connector>`.
