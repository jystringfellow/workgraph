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
- `Associations`, only when at least one high-confidence deterministic
  association qualifies (see Association Context below)
- a `Details: workgraph events today` hint for inspecting complete event labels

When no events exist for the local day, output includes `Today` and says no activity has been captured today. No `Associations` heading is shown in that case.

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

`today` includes a compact, additive `Associations` section built from the same
deterministic baseline evaluator used by `workgraph associations explain
<event-id>`. It supplements raw events and sessions; it never replaces or
regroups them, and the related event outside today is never added to the
`Events`/`Sessions` output.

Visibility and bounds:

- only score 80-100 (`high` confidence), lane `baseline`, type `association`
  pairs qualify
- at least one cited event must belong to the current local day; the other
  cited event may fall outside today as long as it remains inside the
  producer's existing seven-day candidate window
- dismissed, snoozed, and explicitly suppressed associations are hidden;
  proposed, reviewed, approved, and acted associations remain visible
- the same canonical pair renders at most once even when both cited events
  occurred today
- `today` evaluates at most the 50 most recent events from today as
  association targets, preserves the existing 200-candidate window per
  evaluated target, and renders at most 5 association pairs
- rendered associations are ordered by score descending, then by the most
  recent cited event timestamp descending, then by canonical pattern key
  ascending
- each rendered association shows only its cited event ids, score,
  confidence, lifecycle state, and the single strongest matched reason
- no `Associations` heading is shown when no qualifying context exists

`today` is a read-only consumer of the shared suggestion substrate: it never
inserts or updates `suggestions` rows itself (only `associations explain`
coalesces new rows), and it never rewrites or deletes raw events. It does call
the same snoozed-suggestion expiration step already used elsewhere in the
substrate, so a snooze whose window has passed is treated as `proposed` again.
See `specs/event-associations.md` for the full evaluator, scoring, and
lifecycle contract.
