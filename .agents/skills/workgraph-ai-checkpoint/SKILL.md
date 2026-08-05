---
name: workgraph-ai-checkpoint
description: Record a durable, privacy-bounded checkpoint for the current wrapped workgraph AI coding session. Use when the user explicitly invokes $workgraph-ai-checkpoint, asks to save or checkpoint the current AI session, or requests an end-of-day handoff before pausing, stopping, or switching coding agents.
---

# workgraph AI Checkpoint

Turn the current agent context into one concise restart handoff and append it
through workgraph's existing checkpoint contract. Let workgraph collect all
observed repository and working-directory state.

## Workflow

1. Confirm that this is a wrapped workgraph AI session.
   - Check only `WORKGRAPH_AI_SESSION_ID`; do not print or enumerate the full
     environment.
   - If it is absent, stop and explain that the agent must be launched with
     `workgraph ai run -- <agent-command>`. Never invent or discover a session
     ID.
2. Prepare one JSON object from context already available in the current
   session.
   - Use only `goal`, `current_state`, `completed`, `next_actions`, `blockers`,
     and `decisions`.
   - Use strings for `goal` and `current_state`; use arrays of strings for the
     other fields.
   - Omit empty fields. Include at least one meaningful field.
   - State only progress and decisions supported by the current work. Mark
     genuine uncertainty as a blocker instead of inventing certainty.
3. Keep the handoff privacy-bounded.
   - Summarize; do not copy conversation prompts or transcript excerpts.
   - Exclude credentials, secrets, environment values, terminal output, file
     contents, source diffs, and full command arguments.
   - Do not supply observed state such as paths, branch, HEAD, dirty files,
     process identity, or timestamps. Workgraph observes those independently.
4. Submit the object to `workgraph ai checkpoint --stdin` from the session's
   current working directory.
   - Prefer the execution tool's direct stdin support when available.
   - Otherwise, use a safely quoted heredoc internally; the user must not need
     to write JSON, pipe `printf`, or use a `!` shell escape.
   - Use `workgraph` from `PATH` when available. Only when developing inside
     the workgraph repository and no installed binary is available, use
     `go run ./cmd/workgraph ai checkpoint --stdin`.
5. Verify the receipt before claiming success.
   - Require a zero exit status and stdout containing `AI checkpoint recorded`,
     `Session:`, and `Event:`.
   - Report the returned session and event IDs concisely.
   - On validation or persistence failure, report the sanitized command error.
     Do not claim success, retry with invented content, or echo rejected JSON.

## Boundaries

- Run only after an explicit user request. Do not schedule checkpoints or
  create them merely because the agent, terminal, or wrapped process exits.
- Record one checkpoint per invocation.
- Do not edit project files as part of checkpointing.
- Do not bypass `workgraph ai checkpoint --stdin` or write directly to SQLite.
