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
- `~/.workgraph/config.json`
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

## Watch Configuration

The default config watches existing common folders such as Desktop, Documents,
Downloads, Code, Projects, Developer, Work, source, and repos. Paths are stored
as resolved absolute paths.

Add the current directory to the watched roots:

```sh
workgraph config add-watch
```

Add a specific folder:

```sh
workgraph config add-watch /Volumes/Craig/Code
```

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

If no `--watch` flag is provided, background capture uses configured
`watch_dirs`. Configured `ignore_paths` and `ignore_names` apply either way.
The command returns after capture is ready.

Inspect or stop background capture:

```sh
workgraph status
workgraph stop
```

Diagnose local readiness without contacting provider APIs or exposing secrets:

```sh
workgraph doctor
```

Doctor reports initialization, daemon state, configured watch roots, OAuth
connector token presence, and LLM profile readiness.

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
`Projects`, and `Sessions` sections.

Inspect captured events without opening SQLite:

```sh
workgraph events today
workgraph events today --type notion.page_updated
workgraph events today --type slack.message --limit 10
```

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

## Connectors

List and tune connector polling:

```sh
workgraph connectors list
workgraph connectors status
workgraph connectors disable <connector>
workgraph connectors enable <connector>
workgraph connectors interval <connector> 15m
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
