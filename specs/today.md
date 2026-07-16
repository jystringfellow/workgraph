# Today Command

`workgraph today` answers what happened during the current local day using stored events.

The command is deterministic and local-first:

- reads from the SQLite event store
- filters events by the current local calendar day
- groups nearby events from the same project into sessions
- renders plain text without an LLM

## Output

When events exist, output includes:

- `Today`
- `Projects`
- `Sessions`
- a `Details: workgraph events today` hint for inspecting complete event labels

When no events exist for the local day, output includes `Today` and says no activity has been captured today.

## Event Labels

`today` keeps event labels compact while preserving the identifiers that make a line useful:

- file events use their captured path when no summary exists
- git commits use the commit subject plus branch and short SHA when available
- GitHub pull requests and issues use the title plus number and state when available
- whitespace, including embedded newlines and tabs, is collapsed so each event occupies one physical line
- the rendered label is capped at 160 Unicode characters including a trailing ellipsis when truncated

Compaction is presentation-only. `today` does not rewrite event summaries or
payloads in SQLite. `workgraph events today` remains the detailed inspection
path and renders the complete stored label, with optional `--type` and `--limit`
filters.

## Sessions

For Phase 0, sessions are inferred at query time. Consecutive events stay in the same session when:

- they belong to the same project
- they are no more than 30 minutes apart

The stored `sessions` table remains available for future durable session summaries, but `today` does not require precomputed session rows.

## What Next Suggestions (Future)

`today` should eventually include an optional `What next` section driven by
captured evidence.

Requirements:

- each suggestion cites the evidence used to produce it, such as event ids,
	source ids, or concrete artifact references
- each suggestion includes a short reason users can inspect
- users can suppress or dismiss a suggestion pattern without editing data
	manually
- suggestions remain non-destructive drafts until explicitly approved

Suggestion rendering and suppression behavior should align with
`specs/suggestion-explainability.md`.

## Association Context

The first deterministic association slice is inspected through
`workgraph associations explain <event-id>`. It does not add association rows to
`today`; this keeps raw event evidence and sessions primary until a separate fact
defines a compact high-confidence presentation.
