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

When no events exist for the local day, output includes `Today` and says no activity has been captured today.

## Sessions

For Phase 0, sessions are inferred at query time. Consecutive events stay in the same session when:

- they belong to the same project
- they are no more than 30 minutes apart

The stored `sessions` table remains available for future durable session summaries, but `today` does not require precomputed session rows.
