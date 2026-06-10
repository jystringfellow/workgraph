# Personalization Feedback Loop

workgraph personalization should improve relevance for one user on one machine
using local feedback only.

## Goals

- learn from user actions, such as accept, dismiss, snooze, and complete
- rerank future suggestions using local preference signals
- keep personalization inspectable and reversible

## Feedback Events

The system should capture structured local feedback events:

- event timestamp
- suggestion id and type
- action: `accepted`, `dismissed`, `snoozed`, `completed`
- optional reason code for dismiss or snooze

Feedback capture should be append-only for auditability.

The first implementation should write feedback through `suggestion_feedback`
from `specs/db-contracts.md` and update the current state on the related
`suggestions` row. Ranking weights and preference rules should come later, once
feedback records exist.

## Local Ranking Model

A lightweight local reranker should combine:

- deterministic relevance features
- connector freshness
- recent user feedback outcomes
- optional user-edited preference rules

The first implementation may use weighted scoring instead of a learned ML model.

## Editable Preferences

Users should be able to inspect and edit advanced preference rules, for example:

- prioritize specific projects or collaborators
- down-rank recurring noisy suggestion types
- adjust snooze defaults

Manual edits should be explicit and preserved separately from derived weights.

## Reset And Export

Personalization controls should include:

- reset learned weights without deleting raw feedback history
- reset all personalization state
- export local feedback and preference state

Reset actions should require explicit confirmation.

## Privacy And Scope

- no cross-user training
- no required cloud sync
- no hidden model updates outside local records

## Constraints

- personalization should never hide raw captured evidence
- personalized ranking should remain explainable in user-visible output
- low-confidence suggestions should remain review-oriented
