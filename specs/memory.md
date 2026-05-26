# Active Memory

WorkGraph memory is explicit user-owned context stored in local files.

Captured events remain the source of truth for observed behavior. Memory can
record context that events cannot know on their own, such as priorities,
decisions, constraints, and open questions.

## Project Markdown

The first memory contract is project-scoped Markdown:

```text
workgraph-memory/
  projects/
    <project-slug>.md
```

`<project-slug>.md` is a lowercase kebab-case filename derived from the
project name passed to `workgraph memory init <project>` or
`workgraph resume <project>`. Missing project memory is normal.

Project memory may contain any Markdown the user wants to keep. Useful sections
include:

- context
- current priorities
- decisions
- constraints
- open questions

This slice treats project memory as readable Markdown content. It does not
require frontmatter, infer aliases, or generate memory with an LLM.

## Initialize Project Memory

`workgraph memory init <project>` creates a starter project memory file after
`workgraph init` has created the base local WorkGraph state.

The command:

- creates project memory for any valid project name
- writes a Markdown starter with headings for context, current priorities,
  decisions, constraints, and open questions
- reports the existing project memory path without overwriting when the file is
  already present
- accepts explicit WorkGraph home and memory directory paths for non-default
  local state
- does not modify Git state or `.gitignore`

## Resume

When `workgraph resume <project>` finds matching project memory, the output
includes that explicit context beside recent captured activity.

When matching project memory exists but no events have been captured for the
project, resume still includes the project memory and clearly reports that no
recent activity was found.

When a project has recent activity but no matching project memory, resume points
to the path where a user can add it.

WorkGraph must not use project names to read outside the memory repo.
