---
name: workgraph-memory
description: Draft, create, and edit workgraph project memory Markdown for durable user-owned context. Use when a user asks an AI agent to write or revise workgraph memory, preserve project priorities or decisions, turn a conversation into project context, or help maintain files under workgraph-memory/projects.
---

# workgraph Memory

Keep workgraph project memory concise, durable, and explicitly grounded in what
the user said or approved. Treat memory as user-owned context beside captured
events, not as an invented account of behavior.

## Workflow

1. Resolve the project name and ensure project memory exists.
   - Use the project name the user provides when one is available.
   - If the user does not name a project and the current workspace is a Git
     repository, use the Git repo root basename as the candidate unless local
     workgraph context suggests a different project name.
   - If neither signal is available, ask for the project name before creating
     memory.
   - Prefer `workgraph memory init "<project>"` when the command is available.
     Use the path reported by workgraph instead of reproducing its slugging rule
     by hand.
   - If workgraph says it is not initialized, tell the user to run
     `workgraph init` or ask before running setup on their behalf.
2. Locate the workgraph memory contract before editing.
   - In the workgraph repo, read `specs/memory.md` when available.
   - Default project memory lives under `workgraph-memory/projects/<project-slug>.md`.
3. Gather durable context.
   - Use direct user statements and confirmed decisions as memory.
   - Use events, GitHub work, diffs, or chat history as evidence to propose
     memory, not as permission to invent priorities or decisions.
   - Ask for confirmation when the desired memory content is materially
     ambiguous.
4. Edit narrowly.
   - Preserve existing user-authored content unless the user asks to revise it.
   - Prefer updating existing sections over accumulating duplicate notes.
   - Keep context that will still help after the current session; leave transient
     progress logs out unless the user wants them.
5. Verify the result.
   - Keep Markdown readable by hand.
   - Check that project memory stays inside the workgraph memory directory.
   - Summarize what durable context changed and call out any inferred draft text.

## Memory Shape

Use these sections when they fit the project:

- `Context`
- `Current priorities`
- `Decisions`
- `Constraints`
- `Open questions`

Do not force every section to contain text. An empty starter template is better
than weak filler.

## Drafting Rules

- Write confirmed decisions plainly.
- Put unresolved or inferred material under `Open questions` or label it as a
  draft proposal for the user to approve.
- Prefer short bullets for priorities, constraints, and decisions.
- Avoid private-source excerpts, full transcripts, and generated summaries that
  lose the user's wording or certainty.
- Do not modify `.gitignore`, initialize Git, or choose sync policy unless the
  user explicitly asks.

## Example

For a user-confirmed project decision:

```markdown
## Decisions
- Project memory starts as user-owned Markdown and appears in deterministic
  resume output.
```
