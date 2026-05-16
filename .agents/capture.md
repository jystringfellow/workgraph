# Agent: Capture Event

## Purpose

Normalize incoming operational signals into WorkGraph events.

## Input

- source, such as files, git, GitHub, Slack, or CLI
- raw payload from the source
- optional observed timestamp

## Output

- normalized event object

## Requirements

- Include `id`, `source`, `type`, `timestamp`, and `payload`
- Store source-specific details in valid JSON
- Infer project when the source provides enough evidence
- Preserve enough raw context to debug the event later
- Avoid dropping data silently

## Notes

This should be deterministic. Avoid LLMs here unless a later spec and fact explicitly require them.
