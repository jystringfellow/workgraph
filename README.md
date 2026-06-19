<p align="center">
  <img src="public/assets/workgraph-lockup.png" alt="workgraph" width="480">
</p>

# workgraph

workgraph is a local-first attempt to build the open substrate for personal
work intelligence.

Web: https://workgraph.pages.dev

It captures operational context, connects it to durable personal memory, and
helps restore the state of work so people can think, decide, and execute with
more continuity.

The goal is not merely productivity tracking. workgraph is meant to become
infrastructure for contextual intelligence, strategic alignment, and
personalized execution assistance over time.

## Current Shape

- `specs/` captures intent, principles, and the roadmap.
- `features/` describes user-visible behavior.
- `facts/` contains executable facts that define what must not regress.

Domain entity contracts live in `specs/domain-entities.md`. Durable database
contracts live in `specs/db-contracts.md`.

The repository is currently in early specification mode: many facts are present
as skipped placeholders until they are converted into executable failing tests.

## How This Project Is Built

workgraph uses a facts-first development loop:

```text
write spec -> write feature -> write failing fact -> implement -> pass -> cross off roadmap
```

Specs explain intent. Features describe user-visible behavior. Facts enforce
correctness.

```text
principles = constraints (must stay true)
roadmap = bets (likely to change)
facts = enforcement (cannot regress)
```

When prose and facts disagree, the facts win for behavior. Prose should then be
updated to explain the current truth.

## Project Goals

workgraph helps answer:

- What did I do?
- Why did I do it?
- Who did I interact with?
- What remains unfinished?
- What matters next?
- How does my work align with goals?
- How do I resume context quickly?

## Local Onboarding

Install the CLI from this checkout:

```sh
go install ./cmd/workgraph
```

Make sure your Go binary directory is on `PATH`. It is usually:

```sh
$(go env GOPATH)/bin
```

Then initialize local state:

```sh
workgraph init
```

This creates the local workgraph home, SQLite database, settings file, and
user-owned memory directory:

- `~/.workgraph/`
- `~/.workgraph/workgraph.db`
- `~/.workgraph/settings.json`
- `~/workgraph-memory/`
- `~/workgraph-memory/projects/`

Connect the sources you want workgraph to capture. Start with the
[connectors guide](docs/connectors.md) for available connectors and setup
examples.

```sh
workgraph slack connect
workgraph notion connect
workgraph azure boards connect --organization <org> --project <project> --team <team>
```

Start local capture:

```sh
workgraph start
```

`workgraph start` uses the configured watch folders, connected accounts, and
connector polling settings. Capture is explicit: `init` never starts background
capture silently.

Use these commands to check or stop the daemon:

```sh
workgraph status
workgraph stop
```

For command details, debugging workflows, and local database inspection, see
the [command reference](docs/commands.md).

## Memory

Project memory is user-owned Markdown under
`~/workgraph-memory/projects/<project-slug>.md`. It is a place to keep context
that captured events cannot know on their own, such as priorities, decisions,
constraints, and open questions. Project slugs are lowercase kebab-case.

Create a starter memory file for a project:

```sh
workgraph memory init "workgraph"
```

Resume a project from captured events and explicit memory:

```sh
workgraph resume workgraph
```

## Useful References

- [Command reference](docs/commands.md) for CLI examples and debugging
  workflows.
- [Connectors guide](docs/connectors.md) for connecting Slack, Notion, Azure
  Boards, calendar, mail, and other capture sources.
- [Roadmap](specs/roadmap.md) for current implementation direction.
- [Init](specs/init.md), [start](specs/start.md), [today](specs/today.md), and
  [resume](specs/resume.md) for core command contracts.
- [Connector runtime](specs/connector-runtime.md) for connector polling
  behavior.

## Installing From GitHub

Once the current code is pushed to GitHub, install it with:

```sh
go install github.com/jystringfellow/workgraph/cmd/workgraph@latest
```

Then run:

```sh
workgraph init
```

Published release binaries may come later. For now, source builds and
`go install` are the expected install paths.

## Verification

Run the facts with:

```sh
go test ./...
```

Skipped tests mark facts that are specified but not active yet. Before
implementation, replace a skipped placeholder with real assertions and verify
the test fails for the expected reason.
