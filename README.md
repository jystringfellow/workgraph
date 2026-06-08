<p align="center">
  <img src="public/assets/workgraph-lockup.png" alt="workgraph" width="480">
</p>

# workgraph

workgraph is a local-first attempt to build the open substrate for personal work intelligence.

Web: https://workgraph.pages.dev

It captures operational context, connects it to durable personal memory, and helps restore the state of work so people can think, decide, and execute with more continuity.

The goal is not merely productivity tracking. workgraph is meant to become infrastructure for contextual intelligence, strategic alignment, and personalized execution assistance over time.

## Current Shape

- `specs/` captures intent, principles, and the roadmap.
- `features/` describes user-visible behavior.
- `facts/` contains executable facts that define what must not regress.

Domain entity contracts live in `specs/domain-entities.md`. Durable database contracts live in `specs/db-contracts.md`.

The repository is currently in early specification mode: many facts are present as skipped placeholders until they are converted into executable failing tests.

## How This Project Is Built

workgraph uses a facts-first development loop:

```text
write spec → write feature → write failing fact → implement → pass → cross off roadmap
```

Specs explain intent. Features describe user-visible behavior. Facts enforce correctness.

```text
principles = constraints (must stay true)
roadmap = bets (likely to change)
facts = enforcement (cannot regress)
```

Therefore:

```text
Principles → stable
Roadmap → flexible
Facts → enforced
```

When prose and facts disagree, the facts win for behavior. Prose should then be updated to explain the current truth.

## Project Goals

workgraph helps answer:

- What did I do?
- Why did I do it?
- Who did I interact with?
- What remains unfinished?
- What matters next?
- How does my work align with goals?
- How do I resume context quickly?

## Planned Core Loop

The weekend V1 roadmap starts with:

- `workgraph init` (implemented)
- `workgraph start`
- `workgraph today`
- `workgraph resume <project>`
- SQLite event storage
- File system watching
- Basic project inference
- Time-based session grouping
- Simple output without an LLM

## Running Locally

During Phase 0, the safest way to run workgraph is from source:

```sh
go run ./cmd/workgraph init
```

This creates:

- `~/.workgraph/`
- `~/.workgraph/workgraph.db`
- `~/.workgraph/config.json`
- `~/workgraph-memory/`
- `~/workgraph-memory/projects/`

Project memory is user-owned Markdown under
`~/workgraph-memory/projects/<project-slug>.md`. It is a place to keep context
that captured events cannot know on their own, such as priorities, decisions,
constraints, and open questions. Project slugs are lowercase kebab-case.

The default config watches existing common folders such as Desktop, Documents,
Downloads, Code, Projects, Developer, Work, source, and repos, and ignores
workgraph internal storage. Paths are stored as resolved absolute paths so the
same behavior works on macOS, Linux, and Windows without relying on shell
expansion of `$HOME`. workgraph avoids recursively watching the entire home
directory when those common folders exist.

On macOS, watching protected folders such as Documents, Desktop, and Downloads
can trigger privacy prompts. To avoid approving each folder one by one, grant
Full Disk Access once to your terminal app or installed workgraph binary in
System Settings → Privacy & Security → Full Disk Access.

The config shape is:

```json
{
  "watch_dirs": ["/Users/craig/Desktop", "/Users/craig/Documents", "/Users/craig/Downloads", "/Users/craig/Code"],
  "ignore_paths": ["/Users/craig/.workgraph"],
  "ignore_names": [".git", "node_modules", "DerivedData", ".noindex", "xcuserdata", "bin", "obj", "dist", "build", "target", ".build", ".gradle"]
}
```

If you want to pick up the latest default config after a workgraph update, run:

```sh
go run ./cmd/workgraph init --force
```

This refreshes `~/.workgraph/config.json` while preserving captured events and
memory files.

To add the directory you are currently working in to the watched roots:

```sh
go run /path/to/workgraph/cmd/workgraph config add-watch
```

You can also add a specific folder:

```sh
go run /path/to/workgraph/cmd/workgraph config add-watch /Volumes/Craig/Code
```

Added watch roots are stored as absolute paths and placed before existing roots
so a broad home-directory watch budget does not starve a project you explicitly
added. Roots added with `config add-watch` are treated as explicit, so workgraph
can recurse through them more fully than init-owned default folders.

To start background file capture for the current directory:

```sh
go run ./cmd/workgraph start --watch .
```

If no `--watch` flag is provided, background capture uses the configured
`watch_dirs`. Configured `ignore_paths` and `ignore_names` apply either way.
The command returns after capture is ready.

When a watched tree is very large, workgraph caps recursive watch registration
to keep file descriptors available for the process. If output says `Watch limit
reached`, capture is still running for already registered directories, but you
should narrow `watch_dirs` to the folders you care about most. The output
includes a small sample of registered directories and the first directory that
was outside the watch budget. workgraph prioritizes user-facing folders such as
Desktop, Documents, and Downloads before hidden cache directories, and it skips
top-level hidden folders under broad watched roots unless you explicitly add
that hidden folder to `watch_dirs`. Init-owned default roots are traversed
conservatively: workgraph watches the default root and its immediate children,
then only recurses deeper into children that look like active work folders.

Use `status` and `stop` to inspect or stop background capture:

```sh
go run ./cmd/workgraph status
go run ./cmd/workgraph stop
```

For debugging, add `--foreground` to keep capture attached to the current
terminal and print captured events:

```sh
go run ./cmd/workgraph start --foreground --watch .
```

Some editors save by writing a temporary scratch file and replacing the original
document. workgraph normalizes that safe-save pattern into `file.modified` for
the document and ignores editor scratch files and `.DS_Store` metadata noise.

For isolated testing, keep workgraph state and watched files inside a temporary
directory:

```sh
tmpdir="$(mktemp -d /tmp/workgraph-run.XXXXXX)"
echo "$tmpdir" > /tmp/workgraph-run-dir
mkdir -p "$tmpdir/project"
go run ./cmd/workgraph init --home "$tmpdir/.workgraph" --memory "$tmpdir/memory"
go run ./cmd/workgraph start --home "$tmpdir/.workgraph" --watch "$tmpdir/project"
```

In another terminal, change a file under the watched project:

```sh
tmpdir="$(cat /tmp/workgraph-run-dir)"
echo "first note" > "$tmpdir/project/notes.md"
sleep 1
echo "second note" >> "$tmpdir/project/notes.md"
sleep 1
rm "$tmpdir/project/notes.md"
```

Then stop background capture:

```sh
go run ./cmd/workgraph stop --home "$tmpdir/.workgraph"
```

To watch events stream live instead, run foreground capture:

```sh
go run ./cmd/workgraph start --foreground --home "$tmpdir/.workgraph" --watch "$tmpdir/project"
```

The foreground terminal should print lines like:

```text
file.created /tmp/workgraph-run.abc123/project/notes.md
file.modified /tmp/workgraph-run.abc123/project/notes.md
file.deleted /tmp/workgraph-run.abc123/project/notes.md
```

The sleeps are only there to make the manual demo easy to inspect. Real file
capture uses filesystem notifications, but operating systems may coalesce very
fast write bursts, so a rapid create/write/delete sequence may not produce a
separate `modified` event for every write.

To inspect work captured during the current local day:

```sh
go run ./cmd/workgraph today
```

The output is deterministic plain text. When events exist, it includes `Today`,
`Projects`, and `Sessions` sections. Sessions are inferred from same-project
events that happen within 30 minutes of each other.

To inspect captured events without opening SQLite:

```sh
go run ./cmd/workgraph events today
go run ./cmd/workgraph events today --type notion.page_updated
go run ./cmd/workgraph events today --type slack.message --limit 10
```

To inspect Notion's local object index and captured page previews:

```sh
go run ./cmd/workgraph notion index list
go run ./cmd/workgraph notion index show <notion-page-or-database-id>
```

To resume a project from captured events and explicit project memory:

```sh
go run ./cmd/workgraph resume workgraph
```

To create the starter memory template for a project:

```sh
go run ./cmd/workgraph memory init "workgraph"
```

The command creates `~/workgraph-memory/projects/workgraph.md` if it is missing
and reports the existing path without overwriting it when memory is already
present.

When matching project memory exists, resume includes it beside recent activity.
When activity exists but memory does not, resume prints the Markdown path where
that project context can be added.

Background capture uses the same configured watch and ignore rules as
foreground capture. It does not start silently during `init`; capture is always
an explicit command. Capture state is stored under the workgraph home as local
PID, log, and state files.

To build a local binary:

```sh
go build -o ./bin/workgraph ./cmd/workgraph
./bin/workgraph init
```

To install the CLI into your Go binary path:

```sh
go install ./cmd/workgraph
workgraph init
```

Make sure your Go binary directory is on `PATH`. It is usually:

```sh
$(go env GOPATH)/bin
```

## Installing From GitHub

Once the current code is pushed to GitHub, install it with:

```sh
go install github.com/jystringfellow/workgraph/cmd/workgraph@latest
```

Then run:

```sh
workgraph init
```

Published release binaries may come later. For now, source builds and `go install` are the expected install paths.

## New Machine Setup

For a second machine, install from the current source checkout or from GitHub,
then initialize local state on that machine:

```sh
git clone https://github.com/jystringfellow/workgraph.git
cd workgraph
go install ./cmd/workgraph
workgraph init
workgraph start
```

If installing directly from GitHub is more convenient:

```sh
go install github.com/jystringfellow/workgraph/cmd/workgraph@latest
workgraph init
workgraph start
```

`workgraph start` uses the machine-local `~/.workgraph/config.json`, connected
accounts, and connector polling settings. OAuth-backed connectors need to be
connected once per machine because tokens are stored locally:

```sh
workgraph slack connect
workgraph notion connect
workgraph azure boards connect --organization <org> --project <project> --team <team> --area-path '<area-path>'
```

Use `workgraph connectors list` to confirm what will be monitored, and
`workgraph status` to confirm the daemon is running.

If you use a Slack List as a todo list, save its List id while connecting Slack:

```sh
workgraph slack connect --list <list-id>
```

`workgraph start` then monitors that List as connector `slack.lists`. You can
also run a one-off capture for debugging:

```sh
workgraph slack lists capture --list-id <list-id>
```

Azure Boards uses the Microsoft OAuth PKCE flow and stores its local connector
settings in `~/.workgraph/azure-boards.json`. After connecting, `workgraph
start` monitors matching work items as connector `azure.boards`. Multiple
`--area-path` flags are allowed and are combined as alternatives in the default
WIQL query.

To use an AWS Bedrock inference profile for summaries, make sure your normal AWS
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

## Inspecting The Database

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

To inspect captured file events from an isolated run:

```sh
tmpdir="$(cat /tmp/workgraph-run-dir)"
sqlite3 "$tmpdir/.workgraph/workgraph.db" \
  "SELECT type, json_extract(payload_json, '$.operation'), json_extract(payload_json, '$.path') FROM events ORDER BY timestamp;"
```

For a one-off schema check:

```sh
sqlite3 ~/.workgraph/workgraph.db ".schema"
```

## Verification

Run the facts with:

```sh
go test ./...
```

Skipped tests mark facts that are specified but not active yet. Before implementation, replace a skipped placeholder with real assertions and verify the test fails for the expected reason.
