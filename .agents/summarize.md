# Agent: Summarize Session

## Purpose

Summarize a group of events into a human-readable work session.

## Input

- list of events
- optional project context
- optional memory context

## Output

- session summary
- key actions
- possible open tasks
- source event references

## Requirements

- Keep output structured enough to validate
- Reference source events for claims
- Avoid inventing tasks not supported by events
- Preserve uncertainty when evidence is incomplete
- Prefer concise summaries over broad narrative

## Notes

This may use LLMs, but output structure and evidence references must be validated before use.
