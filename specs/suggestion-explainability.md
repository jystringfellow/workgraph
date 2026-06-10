# Suggestion Explainability

workgraph suggestions should be inspectable, suppressible, and reversible.
Users should be able to answer why a suggestion appeared before deciding whether
to act on it.

## Scope

Applies to suggestion-producing surfaces, including:

- watch root suggestions
- ignore rule suggestions
- what-next suggestions in today or resume
- association suggestions across connectors

## Requirements

Every suggestion should include:

- a stable suggestion id
- a suggestion type
- concise reason text
- cited evidence references, such as event ids or source ids
- confidence tier
- source lane, such as deterministic baseline or optional semantic lane

## Confidence Tiers

- `high`: strong evidence and clear linkage
- `medium`: useful but should be reviewed explicitly
- `low`: weak linkage, hidden by default unless requested

Confidence must be visible in output and never imply automatic execution.

## Suppression Controls

Users should be able to suppress suggestion patterns without deleting evidence.

Suppression should support:

- dismissing one suggestion instance
- suppressing a recurring pattern
- snoozing until a later time window
- undoing suppression

Suppression metadata is local-only and inspectable.

## Lifecycle

```text
proposed -> reviewed -> approved|dismissed|snoozed
```

Only explicit approval may trigger downstream actions that mutate configuration
or derived state.

## Storage

Suggestion records should preserve:

- original evidence references
- generated reason text
- confidence
- current state
- user feedback actions and timestamps

Storage should remain local and queryable from SQLite or local files.

The first implementation should use the SQLite contracts in
`specs/db-contracts.md`:

- `suggestions`
- `suggestion_feedback`
- `suggestion_suppressions`

This substrate should land before building new suggestion producers or review
metrics, so every producer shares the same evidence, confidence, lifecycle, and
suppression behavior.

## Constraints

- no silent auto-apply behavior
- no requirement for hosted AI
- no irreversible mutation from low-confidence suggestions
