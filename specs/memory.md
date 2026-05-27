# Active Memory

WorkGraph memory is explicit user-owned context stored in local files.

Captured events remain the source of truth for observed behavior. Memory can
record context that events cannot know on their own, such as priorities,
decisions, constraints, and open questions.

## Project Markdown

The first memory contracts are user-owned Markdown files:

```text
workgraph-memory/
  personal.md
  organizations/
    <organization-slug>.md
  teams/
    <team-slug>.md
  projects/
    <project-slug>.md
```

`personal.md` stores personal active memory: priorities, principles,
preferences, working style, and constraints. It is intentionally curated by the
user and is not inferred from captured events.

`<organization-slug>.md` is a lowercase kebab-case filename derived from the
organization name passed to `workgraph memory init --scope organization
<organization>`. Organization memory stores strategy, planning notes, operating
principles, current priorities, constraints, and open questions. Missing
organization memory is normal.

`<team-slug>.md` is a lowercase kebab-case filename derived from the team name
passed to `workgraph memory init --scope team <team>`. Team memory stores squad
strategy, rituals, ownership, current goals, constraints, and open questions.
Missing team memory is normal.

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

## Initialize Personal Memory

`workgraph memory init --scope personal` creates a starter personal memory file
after `workgraph init` has created the base local WorkGraph state.

The command:

- creates personal memory at `workgraph-memory/personal.md`
- writes a Markdown starter with headings for priorities, principles,
  preferences, working style, and constraints
- reports the existing personal memory path without overwriting when the file
  is already present
- accepts explicit WorkGraph home and memory directory paths for non-default
  local state
- does not infer personal memory from captured events or external sources

## Initialize Organization Memory

`workgraph memory init --scope organization <organization>` creates a starter
organization memory file after `workgraph init` has created the base local
WorkGraph state.

The command:

- creates organization memory for any valid organization name
- writes a Markdown starter with headings for strategy, planning notes,
  operating principles, current priorities, constraints, and open questions
- reports the existing organization memory path without overwriting when the
  file is already present
- accepts explicit WorkGraph home and memory directory paths for non-default
  local state
- does not infer organization memory from captured events or external sources

## Initialize Team Memory

`workgraph memory init --scope team <team>` creates a starter team memory file
after `workgraph init` has created the base local WorkGraph state.

The command:

- creates team memory for any valid team name
- writes a Markdown starter with headings for strategy, rituals, ownership,
  current goals, constraints, and open questions
- reports the existing team memory path without overwriting when the file is
  already present
- accepts explicit WorkGraph home and memory directory paths for non-default
  local state
- does not infer team memory from captured events or external sources

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
