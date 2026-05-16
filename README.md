# WorkGraph

WorkGraph is a local-first attempt to build the open substrate for personal work intelligence.

It captures operational context, connects it to durable personal memory, and helps restore the state of work so people can think, decide, and execute with more continuity.

The goal is not merely productivity tracking. WorkGraph is meant to become infrastructure for contextual intelligence, strategic alignment, and personalized execution assistance over time.

## Current Shape

- `specs/` captures intent, principles, and the roadmap.
- `features/` describes user-visible behavior.
- `facts/` contains executable facts that define what must not regress.

Domain entity contracts live in `specs/domain-entities.md`. Durable database contracts live in `specs/db-contracts.md`.

The repository is currently in early specification mode: many facts are present as skipped placeholders until they are converted into executable failing tests.

## How This Project Is Built

WorkGraph uses a facts-first development loop:

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

WorkGraph helps answer:

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
- `workgraph run`
- `workgraph today`
- `workgraph resume <project>`
- SQLite event storage
- File system watching
- Basic project inference
- Time-based session grouping
- Simple output without an LLM

## Running Locally

During Phase 0, the safest way to run WorkGraph is from source:

```sh
go run ./cmd/workgraph init
```

This creates:

- `~/.workgraph/`
- `~/.workgraph/workgraph.db`
- `~/workgraph-memory/`

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

## Inspecting The Database

WorkGraph stores local operational memory in SQLite:

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

## Verification

Run the facts with:

```sh
go test ./...
```

Skipped tests mark facts that are specified but not active yet. Before implementation, replace a skipped placeholder with real assertions and verify the test fails for the expected reason.
