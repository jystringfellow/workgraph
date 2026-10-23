# Command Reference

This is a practical reference for local workgraph commands. The README keeps
the first-run path short; this file keeps the operational detail.

## Install

Install the CLI into your Go binary path:

```sh
go install ./cmd/workgraph
```

When `go env GOBIN` is empty, Go installs commands under
`$(go env GOPATH)/bin`. Add that directory to `PATH`; do not try to execute the
directory itself:

```sh
export PATH="$(go env GOPATH)/bin:$PATH"
command -v workgraph
workgraph version
workgraph init
```

If `go env GOBIN` prints a directory, add that directory to `PATH` instead.

Install or explicitly upgrade the latest GitHub version through Go with:

```sh
go install github.com/jystringfellow/workgraph/cmd/workgraph@latest
workgraph version
```

Replacing the executable does not replace an already running daemon process.
After an upgrade, restart it explicitly so connector polling uses the new
binary:

```sh
workgraph stop
workgraph start
```

`workgraph status` shows both the running daemon build and the build currently
at its executable path. It prints a warning and this restart command when they
differ. A daemon created by an older release without startup-build metadata is
also treated as potentially stale.

When the Homebrew tap is enabled, install and upgrade with:

```sh
brew install jystringfellow/tap/workgraph
brew upgrade workgraph
```

Tagged GitHub releases also provide platform archives and `checksums.txt` for
manual verification. workgraph does not perform automatic update checks or
silently replace its own executable.

You can also build a local binary inside the checkout:

```sh
go build -o ./bin/workgraph ./cmd/workgraph
./bin/workgraph init
```

During development, commands can be run directly from source:

```sh
go run ./cmd/workgraph init
go run ./cmd/workgraph start
```

## Built-in Help

List all public commands:

```sh
workgraph help
workgraph --help
```

Get usage, a description, and available options for any command. Command groups
also list their immediate subcommands:

```sh
workgraph help connectors
workgraph help connectors poll
workgraph connectors poll --help
```

`help`, `-h`, and `--help` are read-only and exit successfully. They can be
placed after a public command path without running that command.

## Init

Initialize local state:

```sh
workgraph init
```

This creates:

- `~/.workgraph/`
- `~/.workgraph/workgraph.db`
- `~/.workgraph/settings.json`
- `~/workgraph-memory/`
- `~/workgraph-memory/projects/`

To refresh init-owned defaults after a workgraph update while preserving
captured events and memory files:

```sh
workgraph init --force
```

For isolated testing, keep state under temporary directories:

```sh
tmpdir="$(mktemp -d /tmp/workgraph-run.XXXXXX)"
workgraph init --home "$tmpdir/.workgraph" --memory "$tmpdir/memory"
```

## Watch Settings

File watching is opt-in. A fresh install watches no directories, and
`workgraph start` reports `File watching: off` until you add one. Git, GitHub,
and other connectors capture your work without file watching: git capture finds
local repositories by scanning `repo_dirs` in settings, which `init` fills with
existing code folders such as Code, Projects, Developer, Work, source, and
repos. Repository scan roots are walked on each poll and are never watched.
Paths are stored as resolved absolute paths.

Settings from older versions that listed automatic folders such as Desktop,
Documents, Downloads, and Code in `conservative_watch_dirs` stop watching those
folders; directories you added with `add-watch` keep being watched, and git
capture keeps scanning the automatic folders for repositories. The settings file
is not rewritten.

Add the current directory to the watched roots:

```sh
workgraph settings add-watch
```

Add a specific folder:

```sh
workgraph settings add-watch /Volumes/Craig/Code
```

Inspect effective settings:

```sh
workgraph settings get
workgraph settings get --format json
```

The JSON form is intended for admin review and endpoint verification. It reports
managed controls, provenance, and non-secret local settings counts without
printing connector credentials or captured data.

Added roots are placed before existing roots and watched recursively, subject to
ignore rules and the watch budget. Watched roots are also scanned for git
repositories.

On macOS, watching protected folders such as Documents, Desktop, and Downloads
can trigger privacy prompts. To avoid approving each folder one by one, grant
Full Disk Access once to your terminal app or installed workgraph binary in
System Settings -> Privacy & Security -> Full Disk Access.

## Start, Status, And Stop

Start background capture:

```sh
workgraph start
```

Start capture for a single explicit directory:

```sh
workgraph start --watch .
```

If no `--watch` flag is provided, background capture uses settings
`watch_dirs`. Settings `ignore_paths` and `ignore_names` apply either way.
The command returns after capture is ready.

On macOS, background capture uses a lightweight `__capture-supervisor` process
as the parent of the `__capture-worker`. This preserves macOS platform
certificate verification after the interactive `workgraph start` command exits.
The supervisor performs no capture itself and exits when the worker stops.

Inspect or stop background capture:

```sh
workgraph status
workgraph stop
```

Connector poll failures do not stop background file capture. `workgraph status`
shows enabled connectors with active poll errors, and the same failures are
written to `~/.workgraph/daemon.log`. If capture exits because of a fatal local
watcher or event-store error, the next `workgraph status` shows `Last failure`
and the daemon log path.

Diagnose local readiness without contacting provider APIs or exposing secrets:

```sh
workgraph doctor
```

Doctor reports initialization, daemon state, configured watch roots, OAuth
connector token presence, and LLM profile readiness.

Inspect configured network destinations without contacting provider APIs or
exposing secrets:

```sh
workgraph network destinations
workgraph network destinations --format json
```

The command reports connector API endpoints, OAuth token endpoints or relays,
and configured LLM profile destinations. It reads local configuration only and
does not print access tokens, refresh tokens, API key environment variable
names, or captured work data.

Collect a versioned endpoint security report without contacting providers or
printing local secrets and captured content:

```sh
workgraph security report
workgraph security report --format json
```

The report inventories effective local file permissions, managed-settings
presence, credential storage, SQLite encryption state, configured network
destination count, and stable remediation findings. See
`docs/security/endpoint-security.md` for deployment guidance and current known
gaps.

For debugging, keep capture attached to the current terminal and print captured
events:

```sh
workgraph start --foreground --watch .
```

Foreground capture prints lines like:

```text
file.created /path/to/project/notes.md
file.modified /path/to/project/notes.md
file.deleted /path/to/project/notes.md
```

Some editors save by writing a temporary scratch file and replacing the
original document. workgraph normalizes that safe-save pattern into
`file.modified` for the document and ignores editor scratch files and `.DS_Store`
metadata noise.

When a watched tree is very large, workgraph caps recursive watch registration
to keep file descriptors available. If output says `Watch limit reached`,
capture is still running for already registered directories, but you should
narrow `watch_dirs` to the folders you care about most.

## Today, Events, And Resume

Inspect work captured during the current local day:

```sh
workgraph today
```

The output is deterministic plain text. When events exist, it includes `Today`,
`Projects`, and `Sessions` sections plus a detail-command hint. Event labels are
kept to one line and at most 160 Unicode characters so long Slack messages,
Notion content, and other captured summaries do not overwhelm the overview.
This compaction does not change the stored event.

When at least one high-confidence (score 80-100) deterministic association
qualifies, the output also includes a compact `Associations` section, additive
to the raw events and sessions above it. Each rendered pair shows its cited
event ids, score, confidence, lifecycle state, and strongest matched reason.
At most 50 of today's most recent events are evaluated as association targets
and at most 5 pairs are rendered; dismissed, snoozed, and suppressed
associations are hidden. See the Associations section below for the shared
scoring and lifecycle contract.

Inspect captured events without opening SQLite:

```sh
workgraph events today
workgraph events today --type notion.page_updated
workgraph events today --actor user-me
workgraph events today --involvement review_requested
workgraph events today --type slack.message --limit 10
```

`workgraph events today` is the drill-down view: it shows complete stored event
labels, event IDs, actor metadata, and involvement classification, and can be
narrowed by exact actor, involvement, event type, or recent result count. The
compact overview supports the same exact filters with `workgraph today --actor
<actor>` and `workgraph today --involvement <kind>`. `today` includes direct and
legacy-unclassified evidence while hiding evidence explicitly classified with
no direct involvement; `events today` retains the complete evidence view.

Inspect and optionally accept project evidence mappings:

```sh
workgraph memory doctor
workgraph memory doctor --interactive
workgraph memory link --source Mercury.Platform.Orchestration --source widgets-ui "MPO Harness"
workgraph memory links "MPO Harness"
workgraph memory unlink --source widgets-ui "MPO Harness"
```

Create the memory project before linking. Mappings include captured evidence in
suggest, resume and promotion without renaming events or rewriting authored
memory. Flags precede the project argument. All memory commands accept `--home`,
`--memory` and, except init, `--database`. Today also accepts `--memory` for
project-mapping discovery. Discovery suggests associations; only an explicit
link command or interactive acceptance creates one.

For opt-in login startup and crash recovery on macOS or Linux:

```sh
workgraph stop
workgraph service install
workgraph service status
workgraph stop
workgraph start
workgraph service uninstall
```

Service commands accept `--home`. Stop disables the installed job and login
startup; start enables it again. Services use saved settings, so start-time
overrides require uninstalling the service first. The definition records the
current executable path; reinstall after moving it. macOS uses launchd and
`daemon.log`; Linux uses systemd's user session and journal. No root service or
lingering is installed. Uninstall preserves local data and logs.

Create a starter memory template for a project:

```sh
workgraph memory init "workgraph"
```

Resume a project from captured events and explicit project memory:

```sh
workgraph resume workgraph
```

When matching project memory exists, resume includes it beside recent activity.
When activity exists but memory does not, resume prints the Markdown path where
that project context can be added.

## Associations

Inspect deterministic cross-source associations for one stored event:

```sh
workgraph associations explain <event-id>
```

The command considers at most the nearest 200 events from other sources in the
closed seven-day window before and after the requested event. Qualifying pairs
show their stable suggestion id, score, confidence, lifecycle state, cited event
ids, matched signals, and reasons. Missing events fail explicitly; an event with
insufficient evidence succeeds with an honest empty result.

High- and medium-confidence pairs are coalesced into the local suggestion
store. Dismissal and snooze state survive re-evaluation. Association approval is
lifecycle-only and never changes or deletes raw events. No LLM is used.

`workgraph today` reuses this same evaluator to surface a compact,
high-confidence-only `Associations` section, but it never writes new
suggestion rows itself; only `associations explain` coalesces new rows.

## Suggestions

Inspect locally stored suggestions:

```sh
workgraph suggestions list
workgraph suggestions list --status proposed
workgraph suggestions list --limit 10
```

Dismiss a suggestion without deleting its evidence:

```sh
workgraph suggestions dismiss <id> --reason <code>
```

Snooze a suggestion until a future RFC3339 instant, or mark an approved
suggestion complete after its action proved useful:

```sh
workgraph suggestions snooze <id> --until 2026-07-17T09:00:00-07:00 --reason later
workgraph suggestions complete <id> --note "This helped"
```

Approve an ignore-rule suggestion and update local config, or approve an
association as a lifecycle-only decision:

```sh
workgraph suggestions approve <id>
```

Suggestions are local SQLite records with evidence, confidence, lifecycle
status, feedback, and suppression support. Local file capture may create
proposed ignore-rule suggestions when generated-looking paths produce repeated
file events. Approval is explicit: approving an `ignore_path` suggestion appends
to `ignore_paths`, and approving an `ignore_name` suggestion appends to
`ignore_names`. No LLM profile is required for suggestion storage or
deterministic ignore-rule or association suggestions. Association approval does
not create another link record or mutate either source event.

## Local Effectiveness Review

Review suggestion outcomes and connector freshness from local state only:

```sh
workgraph review
workgraph review --since 7d
workgraph review --since 30d
workgraph review --format json
```

The default window begins Monday at midnight in the local system timezone.
Rolling windows are exactly 168 or 720 hours. Acceptance, dismissal, and snooze
rates share the disposition-event denominator; unavailable metrics say
`insufficient_data` instead of reporting zero quality. Connector freshness uses
the last successful poll, while a separate degraded state preserves current
poll failures. No review data is transmitted.

## Connectors

List and tune connector polling:

```sh
workgraph connectors list
workgraph connectors status
workgraph connectors doctor
workgraph connectors upgrade
workgraph connectors validate github
workgraph connectors disable <connector>
workgraph connectors enable <connector>
workgraph connectors interval <connector> 15m
```

`connectors doctor` reports local setup issues and upgrade hints.
Azure Boards bridges configured with `--authentication azcli` validate the
session with `az account get-access-token`; use `--az <path>` to override the
Azure CLI executable.

Local MCP servers can be inspected and stopped per workgraph home:

```text
workgraph bridge mcp status
workgraph bridge mcp stop
```
`connectors upgrade` reconciles legacy connector runtime state locally without
contacting provider APIs or rewriting credentials.

Poll ready enabled connectors once without starting the daemon:

```sh
workgraph connectors poll --once
workgraph connectors poll --once --connector notion
```

See the [connectors guide](connectors.md) for provider-specific setup.

Watch your open authored pull requests through the authenticated `gh` CLI.
The watch is opt-in, read-only, and stores no token:

```sh
workgraph github prs connect --params-json '{"scope":"open_prs","identity":"@me","roles":["author"]}'
workgraph github prs capture
workgraph events today --type 'github.pull_request.*'
workgraph github prs disconnect
```

Background capture polls the watch only after it starts or restarts, so
after `github prs connect` run `workgraph start`, or `workgraph stop` then
`workgraph start` if capture is already running. To verify immediately, run
`workgraph github prs capture` and then `workgraph next`.

`github prs disconnect` deletes the watch's observations and keeps captured
events.

Ask the running daemon to poll the watch within seconds, for example from a
webhook relay. `--pr` refreshes that pull request's review threads:

```sh
workgraph github prs hint
workgraph github prs hint --pr https://github.com/acme/widget/pull/42
```

Inspect the canonical read-only provider operations needed by every bridged
recipe, or one connector:

```sh
workgraph connectors required-tools
workgraph connectors required-tools calendar.microsoft
```

Each operation is labeled `fetch`, `identity`, or both. The names are stable
logical capabilities rather than client-specific MCP permission strings.

## AI Session Handoff

Continue a wrapped AI coding session in another verified local CLI:

```sh
workgraph ai handoff codex
workgraph ai handoff claude --from <session-id>
workgraph ai handoff copilot --stdin < checkpoint.json
workgraph ai handoff opencode --from <session-id>
```

The supported destinations are `codex`, `claude`, `copilot`, and `opencode`.
Without `--from`, handoff uses `WORKGRAPH_AI_SESSION_ID`. It reuses the source
session's latest structured checkpoint, or `--stdin` can append a fresh
checkpoint while the source is still active. Ended sessions cannot accept a
new checkpoint but can hand off from their latest stored one. Same-agent
handoff starts a new linked Workgraph lifetime; it does not resume or copy the
provider conversation.

Handoff is the deliberate handoff-only exception to the nested-session guard:
it may be invoked from the wrapped source session so that the destination gets
its own Workgraph session identity. Ordinary `ai run` and `ai resume` continue
to reject nested sessions. A reused checkpoint and repository observation must
be the latest compatible evidence. If a newer checkpoint or repository
observation is unsupported or invalid, handoff stops before launch instead of
silently using an older projection. Any source event with an unparseable
timestamp also blocks handoff because its evidence cannot be ordered.

With `--stdin`, checkpoint validation and append happen before destination
launch. Because the checkpoint consumes standard input, the destination reads
from the controlling terminal instead, so run `--stdin` handoffs from an
interactive terminal. Without one, the checkpoint is kept on the source and
handoff stops before launch with the `--from` command to rerun from a
terminal. If that append succeeds but launch later fails, the successfully
appended checkpoint remains on the source session for inspection or retry. The
error says it was saved and gives a retry command without `--stdin`, preserving
the selected home and database; no destination lifecycle or lineage is created.
A destination that does launch and exits nonzero retains its truthful start and
end events and returns the tool's exit outcome.

The continuation context is passed as one command-line argument and is kept
within 30,720 bytes, and within what remains of the 32,767-character Windows
command line after the destination path and adapter arguments. Dirty paths
are trimmed first and marked truncated; a checkpoint too large to fit on its
own stops the handoff before launch. A `--stdin` handoff also stops before
launch if the source already holds a checkpoint dated after the one just
appended.

The destination receives a deliberately constructed prompt containing only
the six checkpoint fields and bounded repository metadata already permitted by
the AI-session model. Credential patterns in branch, cwd, worktree root, or
any dirty path reject the handoff without echoing or redacting the matched
value. Workgraph does not persist or transport transcripts,
source prompts, terminal output, shell history, environment values, complete
command arguments, file contents, or source diffs. The destination start event
records `predecessor_session_id`, while the continuation prompt itself is not
stored in Workgraph events. These exclusions bound the context Workgraph
captures and constructs. The launched tool retains normal child-process
capabilities, including whatever filesystem or network access it ordinarily
has.

## Agent Plugin

Install or update one logical `workgraph` plugin for Codex or Claude Code:

```sh
workgraph plugin install --client codex
# or
workgraph plugin install --client claude-code
workgraph plugin install --client claude-code --model <approved-model>
workgraph plugin install --client claude-code --config-dir "$HOME/.claude-enterprise"
```

Claude's unattended worker trusts the workgraph home and discovers its local
`.claude/settings.json` through the normal settings chain. It does not pass a
`--settings` override, so user- and project-registered provider MCP servers stay
available. Its workgraph drain tools are allowed automatically, while provider
tools require exact-name opt-in. Repeat the flag for every approved read-only
provider tool:

```sh
workgraph plugin install --client claude-code \
  --allow-provider-tool mcp__claude_ai_Slack__slack_read_channel \
  --allow-provider-tool mcp__claude_ai_Slack__slack_read_thread
```

Wildcards are rejected. Reinstalling preserves previously allowed provider
tools and reloads an existing launchd worker.

`--model <model>` pins the unattended drain worker for that client. The choice
is stored in `<workgraph-home>/bridge/workers.json`, appears in install and
doctor output, and survives reinstall when model flags are omitted. Use
`--clear-model` to return to the client's configured default. The generated
launchd file does not contain the selected model.

`--config-dir <path>` binds the worker to that signed-in client account context
and stores the absolute path beside the model in `bridge/workers.json`.
Reinstalling without either config-dir flag preserves it. Use
`--clear-config-dir` to return to the ambient client default. The generated
launchd file does not contain the binding; drain reads it immediately before
invoking the client.

The host packages use their native manifest formats but bundle the same local
MCP and three canonical skills: `workgraph-bridge`, `workgraph-memory`, and
`workgraph-ai-checkpoint`. Verify the client executable, package, skills, MCP,
disposable bridge round trip, worker, and heartbeat without contacting a
provider:

```sh
workgraph plugin doctor --client codex
workgraph plugin doctor --client claude-code
```

Rerun `plugin install` after upgrading workgraph to refresh the package. Start a
new Codex or Claude Code session after installation so the client discovers the
new skills and MCP tools. If the daemon was already running when its executable
was upgraded, also run `workgraph stop` followed by `workgraph start`; plugin
installation reloads the bridge worker, not the daemon. Installing the plugin
does not create an LLM profile or enable hosted-model consent; `workgraph llm
connect` remains an independent model-execution setup path.

Successful capture and connector-status MCP results contain a `runtime` record
with running and on-disk build identities. `stale: true` means the current
client session still owns an MCP process started from the previous executable;
start a new client session after reinstalling the plugin.

The older `workgraph bridge install` and `workgraph bridge doctor` commands are
compatibility aliases that operate on this same broader plugin.

## Bridged Capture

Bridged capture is for remote sources that are already approved through Codex
or Claude Code but cannot be connected to workgraph with direct OAuth. workgraph
keeps the schedule, cursor, retries, and local event store; the signed-in client
performs the provider reads and returns normalized events. `git` always remains
local and direct.

Install the workgraph plugin for one macOS reference client:

```sh
workgraph plugin install --client codex
# or
workgraph plugin install --client claude-code
```

Verify the local package, MCP server, worker, and client executable without
contacting a provider:

```sh
workgraph plugin doctor --client codex
```

Ask the installed client to use its workgraph bridge skill to discover approved
provider connectors and propose source scopes and cadences. After approval, the
client configures each source through the local MCP. The equivalent explicit
CLI setup is:

```sh
workgraph connectors connect slack --mode bridged \
  --params-json '{"channels":["C0DEMO123"],"include_dms":false}'
workgraph connectors interval slack 15m
workgraph start
```

Inspect the daemon-owned outbox and cursor:

```sh
workgraph capture requests --list
workgraph capture watermark --connector slack
workgraph connectors status
```

The installed worker normally claims and completes requests automatically. For
diagnosis, trigger one drain or execute the capability-based protocol manually:

```sh
workgraph bridge drain --client codex
workgraph capture requests --claim --connector slack --max 1 \
  --worker manual --claim-file /private/path/claim.json
workgraph capture ingest --request <request-id> \
  --claim-file /private/path/claim.json --json -
workgraph capture requests --renew <request-id> \
  --claim-file /private/path/claim.json
workgraph capture requests --fail <request-id> \
  --claim-file /private/path/claim.json --error-json -
```

`--error-json` reads `{"error":"..."}` and an optional `"reason":"window_too_large"`,
which narrows a `bounded_events` request to the first half of its window, never
below one hour, instead of retrying the same bounds.

`bridge drain` stops the client session after `--timeout` (default `20m`),
returns only the requests that drain's worker claimed to `pending`, and records the failure for
`connectors status`. `workgraph connectors disable <id>` cancels that
connector's pending or claimed request.

Claim output includes `Capture semantics`. Most connectors return
`bounded_events`; `slack.lists` returns `complete_snapshot`. For Slack Lists,
read every configured List with `slack_read_file`. An MCP worker handling a
request scoped to one List passes that result verbatim as `snapshot_csv` with
`list_id`; workgraph parses the CSV server-side, derives observation time,
applies the configured state and interest columns, derives row identity, and
calculates the content-hash revision. Multi-List and manual CLI batches retain
the event-shaped `{list_id, fields}` compatibility path. State configuration
never filters completed rows, and Lists without state configuration leave
`done` absent.

The claim output includes the canonical non-secret connector parameters as
`Params`. The private claim file contains only the request id and short-lived
claim token.

Claim files contain short-lived local capabilities. Keep them private, never
put their contents in process arguments or logs, and remove them after the
request completes. Manual source ingestion is available for recovery and import
work through `capture ingest --source <source>`, but scheduled workers should
always bind ingestion to a claimed request.

## Local Database

workgraph stores local operational memory in SQLite:

```sh
sqlite3 ~/.workgraph/workgraph.db
```

Useful SQLite commands:

```sql
.tables
.schema events
.schema sessions
.schema memory_docs
SELECT COUNT(*) FROM events;
SELECT * FROM events ORDER BY timestamp DESC LIMIT 10;
```

For a one-off schema check:

```sh
sqlite3 ~/.workgraph/workgraph.db ".schema"
```

## LLM Summaries

For an OpenAI-compatible local endpoint, add the exact model name workgraph
should request and verify the endpoint advertises it:

```sh
workgraph llm add local-gemma \
  --provider openai-compatible \
  --base-url http://localhost:11434/v1 \
  --model gemma4-64k:latest

workgraph llm doctor --profile local-gemma
```

`llm doctor` checks `/v1/models` for OpenAI-compatible profiles and reports
whether the configured model is advertised. It does not print API key
environment variable names or connector data.

### Signed-in Codex and Claude Code

Instead of running a local model or configuring another API credential, use an
already signed-in AI client as an LLM profile:

```sh
workgraph llm connect codex --for summarize
# or
workgraph llm connect claude-code --for summarize
```

The default profile name is `codex` or `claude-code`. Multiple profiles and
task-specific routes can coexist:

```sh
workgraph llm connect codex --name personal-codex --for summarize
workgraph llm connect claude-code --name work-claude \
  --config-dir "$HOME/.claude-enterprise" \
  --model <approved-model> --for categorize
workgraph llm list
workgraph llm use personal-codex --for summarize
```

`--config-dir` stores the signed-in client account context with the profile:
`CODEX_HOME` for Codex or `CLAUDE_CONFIG_DIR` for Claude Code. `llm list` and
`llm doctor` report the binding, and later invocations do not depend on the
ambient shell selecting the same account.

`categorize` is a reserved task route; the project-categorization suggestion
workflow is not implemented yet. Current client-backed execution supports
`llm test`, `llm doctor`, and `llm summarize today`.

Client-backed models are still hosted models, even though workgraph stores no
model token and calls only the local client executable. Explicitly allow focused
captured context to leave the machine, then test and summarize:

```sh
workgraph llm hosted enable
workgraph llm doctor --profile codex
workgraph llm test --profile codex
workgraph llm summarize today --dry-run
workgraph llm summarize today
```

If `llm test` reports that the client's selected model is unavailable, configure
an accessible client model explicitly and rerun the test:

```sh
workgraph llm connect claude-code --model <approved-model> --for summarize
workgraph llm test --profile claude-code
```

workgraph applies the same focused-context selection, outbound secret filtering,
and managed provider policy used by direct hosted profiles. It sends the prompt
through standard input to an ephemeral, non-writing client invocation and does
not expose capture connectors to the summarization task. Client-backed output is
buffered and validated before it is printed; `--no-stream` remains available for
consistent full-response output across every provider.

Hosted LLM profiles, such as Bedrock or non-local OpenAI-compatible endpoints,
require explicit hosted LLM opt-in before workgraph sends prompt content:

```sh
workgraph llm hosted status
workgraph llm hosted enable
workgraph llm hosted disable
```

`llm hosted enable` records a local acknowledgement that hosted LLMs may receive
prompt text derived from captured work context, connector data, and memory
context. Managed settings can still disable hosted LLMs or restrict them to
approved providers, destinations, and models.

To use an AWS Bedrock inference profile for summaries, make sure normal AWS
credentials work first:

```sh
aws sts get-caller-identity --profile work
```

Then add and route a Bedrock LLM profile:

```sh
workgraph llm add bedrock-work \
  --provider bedrock \
  --aws-profile work \
  --region us-east-1 \
  --model-arn arn:aws:bedrock:us-east-1:123456789012:inference-profile/example

workgraph llm test --profile bedrock-work
workgraph llm hosted enable
workgraph llm use bedrock-work --for summarize
workgraph llm summarize today --dry-run
workgraph llm summarize today
```

The Bedrock call uses local AWS credential resolution through the AWS SDK and
sends the configured ARN as the Bedrock Runtime Converse model id.

### System One decision models

System One models such as Jev, Clef, Clef-flash, and Microsoft-Decision-1 do
not generate text. They answer typed questions with a probability per option,
so workgraph uses them only for project association:

```sh
workgraph llm add clef-local \
  --provider system-one \
  --base-url http://127.0.0.1:8000 \
  --model clef-flash
workgraph llm use clef-local --for associate
workgraph llm associate --dry-run
```

`--base-url` is required. workgraph posts to `<base-url>/v1/systemone` (or
`<base-url>/systemone` when the base URL ends in `/v1`); a base URL that
already ends in `/systemone`, or a Workers AI `/ai/run/<model>` URL, is used
unchanged. For a hosted endpoint, name the environment variable that holds the
key; workgraph sends it as a bearer token and never stores or prints it:

```sh
workgraph llm add jev \
  --provider system-one \
  --base-url https://api.typesafe.ai \
  --model jev-latest \
  --api-key-env TYPESAFE_API_KEY
workgraph llm hosted enable
```

A loopback base URL is treated as local, like a local OpenAI-compatible
endpoint. Every other base URL is hosted and needs `llm hosted enable`.
Each unit becomes one request with a `project` choice (opaque ids `p1`,
`p2`, ... for the unit's candidate projects, plus `no_project`, so slugs
are not option keys) and a `kind` choice, and the link's confidence is
the returned probability of the chosen project. Redirects are refused, and errors name only
the scheme and host with a fixed message, never provider output. `system-one` profiles run
direct only, cannot use `--execution bridge`, and are refused by `llm test` and
`llm summarize today`.

## New Machine Setup

For a second machine, install from source or GitHub, initialize local state, and
connect the accounts you want captured on that machine:

```sh
git clone https://github.com/jystringfellow/workgraph.git
cd workgraph
go install ./cmd/workgraph
workgraph init
workgraph start
```

OAuth-backed connectors need to be connected once per machine because tokens
are stored locally.
