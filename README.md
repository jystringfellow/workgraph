# WorkGraph

WorkGraph is a local-first personal work intelligence system.

It captures local work activity, connects it to active memory, and helps restore context quickly so work can resume with less reconstruction.

## Current Shape

- `specs/` captures intent, principles, and the roadmap.
- `features/` describes user-visible behavior.
- `facts/` contains executable facts that define what must not regress.

The repository is currently in early specification mode: many facts are present as skipped Go tests until the implementation exists.

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

- `workgraph init`
- `workgraph run`
- `workgraph today`
- `workgraph resume <project>`
- SQLite event storage
- File system watching
- Basic project inference
- Time-based session grouping
- Simple output without an LLM

## Verification

Run the facts with:

```sh
go test ./...
```

Skipped tests mark facts that are specified but not implemented yet.
