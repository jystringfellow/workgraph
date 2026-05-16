# WorkGraph Agents Guide

## Purpose

This repository builds WorkGraph:

> the open substrate for personal work intelligence

Agents contributing to this repo must follow the development model and principles below.

## Development Model

Always follow:

```text
write spec → write feature → write failing fact → implement → pass → cross off roadmap
```

Facts are the source of truth for behavior.

## Architecture Overview

WorkGraph consists of:

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

## Facts

Before implementing behavior:

1. Write or unskip a test in `/facts`
2. Ensure it fails
3. Implement minimal code to pass
4. Do not modify tests to pass unless the spec changed

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
