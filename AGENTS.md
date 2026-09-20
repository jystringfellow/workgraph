# workgraph Agents Guide

## Purpose

This repository builds workgraph:

> the open substrate for personal work intelligence

Agents contributing to this repo must follow the development model and principles below.

## Development Model

Always follow:

```text
write spec → write feature → write failing fact → implement → pass → cross off roadmap
```

Facts are the source of truth for behavior.

## Architecture Overview

workgraph consists of:

- local daemon in Go
- SQLite event store
- local memory files
- CLI interface
- optional AI workers

Core loop:

```text
capture events → store → group → query (today / resume)
```

## Constraints

- Local-first: no required cloud dependencies
- User-owned data: SQLite plus inspectable local files
- Events are the source of truth
- No silent automation: suggest → draft → approve → act
- Prefer simple implementations over premature abstraction

## Coding Guidelines

- Keep functions small and explicit
- Avoid unnecessary interfaces until patterns stabilize
- Prefer clarity over cleverness
- Log important actions for debugging
- Make behavior testable

### Code Comments

Write code that communicates its behavior through clear naming, structure, types, and tests. Do not add code comments by default.

- Use a test to document surprising or non-obvious behavior whenever practical.
- Add a code comment only when the code cannot reasonably make an essential constraint or behavior clear on its own.
- Keep necessary comments brief, precise, and limited to the code they directly describe.
- Comments must describe the code as it exists now.
- Never use comments to discuss previous implementations, removed behavior, migrations from an older approach, anticipated changes, or future states.
- Do not add narrative comments that merely restate the code.
- Do not add comments to explain a change made in the current task or PR.
- Avoid TODO/FIXME comments; track future work outside the code instead.
- Treat docstrings and documentation comments as code comments for these purposes, except when they are required for public API, generated documentation, or tooling.

## Connector Guidelines

When adding an API-backed connector, prefer starting with the user-facing
connection setup (OAuth/device flow/token storage/disconnect) when feasible.
The first API integration slice should leave the user with a real way to connect
their own account and verify captured data locally. Provider adapters that only
accept raw tokens are useful for facts, but they should usually be a stepping
stone toward a connectable workflow, not the end of a user-visible slice.

## Facts

Before implementing behavior:

1. Replace a skipped placeholder in `/facts` with a believable executable test
2. Run it and verify it fails for the right reason
3. Implement minimal code to pass
4. Do not weaken tests to pass unless the spec changed

Deleting `t.Skip(...)` is not enough. A fact only becomes active when it contains real assertions that fail before implementation and pass after implementation.

## AI Usage Guidelines

- Do not invent behavior not covered by specs or facts
- If unsure, add a test first
- Prefer deterministic outputs over creative ones
- When using LLMs, validate outputs

## Non-Goals For Now

- Full automation
- Complex UI
- Multi-user systems
- Premature optimization

## When In Doubt

Favor:

- simplicity
- observability
- explicitness
- testability
