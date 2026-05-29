# workgraph Principles

## Development Model

workgraph uses a facts-first development loop:

```text
write spec → write feature → write failing fact → implement → pass → cross off roadmap
```

Specs explain intent. Features describe user-visible behavior. Facts enforce correctness.

A failing fact must be executable and believable: it should contain assertions that fail before implementation and pass after implementation. Removing `t.Skip(...)` from an empty placeholder is not a failing fact.

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

## 1. Local-first
All core functionality must work without cloud dependencies.

## 2. User owns their data
All data is stored locally in inspectable formats (SQLite + Markdown).

## 3. Events are the source of truth
All intelligence derives from captured events, not manual summaries.

## 4. Active + passive memory
The system must combine:
- Active memory (markdown files)
- Passive memory (captured events)

## 5. Facts over prose
Critical behavior must be enforced via executable tests, not just specs.

## 6. Assist, don’t override
The system suggests and drafts; it does not act without explicit approval.

## 7. Fast context restoration
Resuming work should be near-instant and require minimal input.

## 8. Incremental intelligence
Start with simple heuristics; layer in AI only when it adds clear value.

## 9. Replaceable components
All major components (capture, storage, analysis) should be swappable.

## 10. Personal-first, then shareable
The system must be genuinely useful to one person before generalizing.
