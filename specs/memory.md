# Active Memory

workgraph memory is explicit user-owned context stored in local files.

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
after `workgraph init` has created the base local workgraph state.

The command:

- creates personal memory at `workgraph-memory/personal.md`
- writes a Markdown starter with headings for priorities, principles,
  preferences, working style, and constraints
- reports the existing personal memory path without overwriting when the file
  is already present
- accepts explicit workgraph home and memory directory paths for non-default
  local state
- does not infer personal memory from captured events or external sources

## Initialize Organization Memory

`workgraph memory init --scope organization <organization>` creates a starter
organization memory file after `workgraph init` has created the base local
workgraph state.

The command:

- creates organization memory for any valid organization name
- writes a Markdown starter with headings for strategy, planning notes,
  operating principles, current priorities, constraints, and open questions
- reports the existing organization memory path without overwriting when the
  file is already present
- accepts explicit workgraph home and memory directory paths for non-default
  local state
- does not infer organization memory from captured events or external sources

## Initialize Team Memory

`workgraph memory init --scope team <team>` creates a starter team memory file
after `workgraph init` has created the base local workgraph state.

The command:

- creates team memory for any valid team name
- writes a Markdown starter with headings for strategy, rituals, ownership,
  current goals, constraints, and open questions
- reports the existing team memory path without overwriting when the file is
  already present
- accepts explicit workgraph home and memory directory paths for non-default
  local state
- does not infer team memory from captured events or external sources

## Initialize Project Memory

`workgraph memory init <project>` creates a starter project memory file after
`workgraph init` has created the base local workgraph state.

The command:

- creates project memory for any valid project name
- writes a Markdown starter with headings for context, current priorities,
  decisions, constraints, and open questions
- reports the existing project memory path without overwriting when the file is
  already present
- accepts explicit workgraph home and memory directory paths for non-default
  local state
- does not modify Git state or `.gitignore`

## Suggest Project Memory Updates

`workgraph memory suggest --scope project <project>` reviews captured evidence
for a project and prints draft memory update suggestions.

The command:

- reads recent captured events for the project
- points at the matching project memory path when known
- emits draft suggestions only
- includes event evidence for every suggestion
- does not create, overwrite, or edit memory files
- does not promote captured events or external artifacts into active memory
  without explicit user action

## Promote Project Memory

`workgraph memory promote --scope project <project> --evidence <event-id>
--text <memory text>` appends user-curated memory to project memory with a link
back to the supporting event.

The command:

- requires explicit memory text from the user or calling command
- requires an event id as supporting evidence
- verifies the event belongs to the target project
- creates project memory with the starter template when the file is missing
- appends promoted memory without overwriting existing content
- records the evidence id beside the promoted memory entry
- stores a durable `supported_by` link from the project memory file to the
  evidence event
- does not treat the event payload itself as active memory

## List Project Memory Links

`workgraph memory links --scope project <project>` lists durable links from a
project memory file to captured event evidence.

The command:

- reads links for the matching project memory path
- includes relation and event id for every link
- does not modify memory files or events

## Resume

When `workgraph resume <project>` finds matching project memory, the output
includes that explicit context beside recent captured activity.

When matching project memory exists but no events have been captured for the
project, resume still includes the project memory and clearly reports that no
recent activity was found.

When a project has recent activity but no matching project memory, resume points
to the path where a user can add it.

workgraph must not use project names to read outside the memory repo.
