---
name: workgraph-ai-checkpoint
description: Record a durable, privacy-bounded checkpoint for the current wrapped workgraph AI coding session. Use when the user explicitly invokes $workgraph-ai-checkpoint, asks to save or checkpoint the current AI session, or requests an end-of-day handoff before pausing, stopping, or switching coding agents; or, when WORKGRAPH_AI_CHECKPOINT_POLICY=agent is set, at work milestones without asking.
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
   - Write terse bullets, about 1-2 KB in total, restating the full current
     state rather than history.
   - State only progress and decisions supported by the current work. Mark
     genuine uncertainty as a blocker instead of inventing certainty.
3. Keep the handoff privacy-bounded.
   - Summarize; do not copy conversation prompts or transcript excerpts.
   - Exclude credentials, secrets, environment values, terminal output, file
     contents, source diffs, and full command arguments.
   - Do not supply observed state such as paths, branch, HEAD, dirty files,
     process identity, or timestamps. Workgraph observes those independently.
4. Submit the object to `workgraph ai checkpoint --stdin` from the session's
   current working directory. Add `--agent-initiated` when the user did not
   ask for this checkpoint.
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

## Standing Consent

Only when `WORKGRAPH_AI_CHECKPOINT_POLICY=agent` is set, checkpoint without
asking at milestones: after a commit, push, or PR; after a review round or test
result that changes the plan; before switching tasks or stopping; when context
is nearly full or was just compacted.

- Never on a timer or after trivial steps; at most about once per milestone.
- Do not read files or run commands just to write a checkpoint.
- Tell the user in at most one short line.

## Boundaries

- Without standing consent, run only after an explicit user request. Never
  schedule checkpoints or create them because a process exits.
- Record one checkpoint per invocation.
- Do not edit project files as part of checkpointing.
- Do not bypass `workgraph ai checkpoint --stdin` or write directly to SQLite.
