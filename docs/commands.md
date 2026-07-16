# Command Reference

This is a practical reference for local workgraph commands. The README keeps
the first-run path short; this file keeps the operational detail.

## Install

Install the CLI into your Go binary path:

```sh
go install ./cmd/workgraph
workgraph init
```

Make sure your Go binary directory is on `PATH`. It is usually:

```sh
$(go env GOPATH)/bin
```

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

The default settings watch existing common folders such as Desktop, Documents,
Downloads, Code, Projects, Developer, Work, source, and repos. Paths are stored
as resolved absolute paths.

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

Added roots are treated as explicit and are placed before existing roots, so a
project you care about does not get starved by broad default watches.

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

Inspect captured events without opening SQLite:

```sh
workgraph events today
workgraph events today --type notion.page_updated
workgraph events today --type slack.message --limit 10
```

`workgraph events today` is the drill-down view: it shows complete stored event
labels and event IDs, and can be narrowed by event type or recent result count.

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
`connectors upgrade` reconciles legacy connector runtime state locally without
contacting provider APIs or rewriting credentials.

Poll ready enabled connectors once without starting the daemon:

```sh
workgraph connectors poll --once
workgraph connectors poll --once --connector notion
```

See the [connectors guide](connectors.md) for provider-specific setup.

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
