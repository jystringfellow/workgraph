# Event Associations

Event associations make captured events easier to query without inventing
unsupported summaries.

The association model should support two lanes:

- baseline lane: deterministic and local heuristics, works without an LLM
- semantic lane: optional LLM/embedding ranking for ambiguous candidate sets

These associations should improve `today` first and prepare the ground for
`resume <project>` later.

## Project

Project inference prefers local repository evidence over folder names:

1. nearest enclosing git repository root
2. configured watch root name
3. parent folder name

The stored event `project` value should be stable and human-readable.

## Artifact Path

File events preserve the source path in `payload_json.path`.

Query commands may use that path as the event's artifact identity. A future artifacts table can normalize this, but Phase 0 should not require a new table before the behavior is useful.

## Session

Sessions are deterministic time groupings. Events belong together when they are close in time and associated with the same project.

For Phase 0, query-time sessions are enough. Durable session rows can come later when summaries or cross-command reuse need them.

## Baseline Lane

The baseline lane is required and must be available with no LLM configured.

Baseline evidence includes:

- deterministic identifiers such as URL references, issue or PR numbers, event
	ids, thread ids, and provider object ids
- local file and repository evidence, including path, repo, branch, and commit
	references
- lightweight fuzzy heuristics such as normalized title similarity,
	participant overlap, and close timestamp windows

Baseline proposals should include human-readable reasons and cited event ids.

Baseline association work should land after the shared suggestion substrate in
`specs/suggestion-explainability.md` and `specs/db-contracts.md`, so association
candidates use the same evidence, confidence, lifecycle, and suppression model
as other suggestions.

## Semantic Lane (Optional)

The semantic lane is an explicit opt-in enhancement and must not be required
for core association behavior.

Semantic ranking may use:

- hosted LLM prompts when configured
- local embedding or local model similarity where available

Semantic outputs should produce scored candidate links and evidence notes, but
they remain suggestions until approved.

## Confidence Gates

Association suggestions should use confidence tiers:

- high confidence: can be shown as strong suggestions
- medium confidence: shown as review-needed suggestions
- low confidence: hidden by default or shown only in debug output

No lane should silently mutate event associations at low confidence.

## Constraints

Associations must be:

- derived from stored events, file paths, or local repository structure
- deterministic
- inspectable in SQLite or plain text output
- useful without an LLM
- explicit about which lane produced each suggestion
