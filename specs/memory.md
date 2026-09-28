# Active Memory

workgraph memory is explicit user-owned context stored in local files.

Captured events remain the source of truth for observed behavior. Memory can
record context that events cannot know on their own, such as priorities,
decisions, constraints, and open questions.

## Default Markdown Memory

The default active memory contracts are user-owned Markdown files:

```text
workgraph-memory/
  personal.md
  organizations/
    <organization-slug>.md
  teams/
    <team-slug>.md
  projects/
    <project-slug>.md
  artifacts/
    <date-or-topic>.html
```

`personal.md` stores personal active memory: role and scope, priorities,
principles, thinking models, voice and communication, working preferences,
rejected patterns, AI collaboration modes, preferences, working style, and
constraints. It is intentionally curated by the user and is not inferred from
captured events.

`<organization-slug>.md` is a lowercase kebab-case filename derived from the
organization name passed to `workgraph memory init --scope organization
<organization>`. Organization memory stores strategy, planning notes, operating
principles, important people and groups, current priorities, constraints, and
open questions. Missing organization memory is normal.

`<team-slug>.md` is a lowercase kebab-case filename derived from the team name
passed to `workgraph memory init --scope team <team>`. Team memory stores squad
strategy, people, operating norms, rituals, ownership, current goals,
constraints, and open questions. Missing team memory is normal.

`<project-slug>.md` is a lowercase kebab-case filename derived from the
project name passed to `workgraph memory init <project>` or
`workgraph resume <project>`. Missing project memory is normal.

Project memory may contain any Markdown the user wants to keep. Useful sections
include:

- context
- current priorities
- current bets
- key decisions and rationale
- people
- artifacts and links
- constraints
- open questions

Project memory remains readable Markdown without required frontmatter or
LLM-generated content.

## Captured Project Mappings

`memory link --source <captured-project> [--source <captured-project> ...]
<memory-project>` explicitly associates an existing memory file with captured
project identifiers. `memory unlink` removes those associations. Both accept
`--home`, `--database` and `--memory`. Flags precede the project argument.
All sources must exist in captured events when linking; an invalid source
rejects the entire request. Repeated links and unlinks are idempotent.

Mappings are stored in SQLite `memory_project_sources`, keyed by the absolute
memory document path and captured project. A project can have multiple sources,
and a captured project can support several workstreams. No raw events or
authored memory are rewritten. Moving a memory file requires relinking it.
Existing databases gain the table on the first explicit link/unlink command;
read-only queries do not migrate the database.

Suggest, resume and promotion validation include exact-name evidence plus all
explicitly mapped sources, deduplicated by event identity. Unlinking removes
that source from future queries but preserves already promoted evidence.
`memory links` shows captured project mappings separately from promoted evidence
links. Linking a source does not manufacture an evidence link.

## Discover Project Mismatches

`memory doctor [<project>]` reports stored mappings and up to five candidate
captured projects per memory document. It compares the filename and first H1
title with captured identifiers using deterministic local signals: normalized
name equality, an acronym of at least three words, or shared non-generic tokens
of at least three characters. CamelCase is split into words. Generic container
and work words are excluded from token overlap. These are suggestions, not
assertions that two projects are identical. Each candidate includes its reason,
event count and latest activity. Equal scores sort by event count then identifier.

Doctor displays explicit link commands and, with `--interactive`, asks separately
for each proposed association. Only `y` or `yes` accepts it; empty input, `no`
and EOF leave mappings untouched. An explicit `memory link` is also approval.
There is no automatic merge or rewrite. If names do not provide a reliable
candidate, doctor lists captured identifiers for manual selection.

Candidate hints also appear in today and resume, and after an empty memory
suggest result. Today considers only events included in its filtered daily view.
Existing mappings are excluded from suggestions. Queries never prompt or write
mappings; they point to doctor for interactive review. Missing memory directories
are normal. Discovery read errors are reported without hiding captured activity.
Titles used for display do not change document identity.

Markdown remains the default generated active memory format because it is
durable, diffable, easy to edit, and good for concise user-curated facts.

## Rich HTML Artifacts

HTML is a first-class local artifact format for richer human-facing outputs:

- implementation plans
- design explorations and prototypes
- code review explainers
- reports and research summaries
- custom review or editing surfaces

HTML artifacts can use tables, CSS, SVG, images, and local JavaScript when that
material helps a user understand, compare, or interact with the work. These
files should live under an inspectable local path such as
`workgraph-memory/artifacts/` or another user-chosen local directory.

Generated HTML artifacts are not active memory by default. They can cite or link
to captured event ids, memory docs, and source files, but workgraph must not
treat a generated report as durable memory unless the user explicitly promotes
or curates its contents into active memory.

When HTML is promoted into active memory, workgraph should preserve the source
file path and document kind (`html`) so later structured interpretation remains
explainable from the user-owned source document. Markdown starter files remain
the default for `workgraph memory init`.

## Initialize Personal Memory

`workgraph memory init --scope personal` creates a starter personal memory file
after `workgraph init` has created the base local workgraph state.

The command:

- creates personal memory at `workgraph-memory/personal.md`
- writes a Markdown starter with headings for role and scope, priorities,
  principles, thinking models, voice and communication, working preferences,
  rejected patterns, AI collaboration modes, preferences, working style, and
  constraints
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
- writes a Markdown starter with headings for strategic themes, strategy,
  planning notes, operating principles, important people and groups, current
  priorities, constraints, and open questions
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
- writes a Markdown starter with headings for strategy, people, operating norms,
  rituals, ownership, current goals, constraints, and open questions
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
  current bets, key decisions and rationale, people, artifacts and links,
  constraints, and open questions
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
