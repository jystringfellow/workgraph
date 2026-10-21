# Projects, Workstreams, And Categorization

Status: planned. Slices D1 through D4 are listed in `specs/roadmap.md`. Facts
will live in `facts/projects_test.go`.

## Intent

Captured activity is filed under where it happened: a repository, a Slack
channel, a DM, a Notion workspace. That is not how the user thinks about
work. The user thinks in projects, and many projects span several places at
once. A lot of the user's value is glue work, such as a Slack conversation
with a colleague about agentic tooling and GraphQL, which never shows up as
engineering work in any one repository.

This spec links captured activity to the projects the user declares, so that
every tracked item belongs to a real project or at least carries a one-line
summary of what kind of task it was. It runs on a schedule, so the user can
end each day by deciding the few uncertain items and start the next day with
overnight activity, including work agents did, already sorted.

```text
events -> units of work -> deterministic links -> LLM categorization
       -> auto-apply (confident) | review (uncertain)
       -> next "Categorize" | today / resume grouped by workstream -> project
```

## Vocabulary

- **Container**: where activity happened, such as a repository, Slack channel,
  DM, or Notion workspace. It is stored in the events `project` column. The
  column is not renamed or migrated; user-facing text calls the value a
  container or source.
- **Project**: a body of work the user declares, backed by a project memory
  file at `workgraph-memory/projects/<slug>.md`. Examples: `mmo-gtm-segments`,
  `mmo-words-and-phrases`, `mmo-harness`, `ask-kai`. A project spans many
  containers and its slug is its identity.
- **Workstream**: an optional parent of projects, such as `mmo` over the
  `mmo-*` projects. It is declared by `workstream: <slug>` in project memory
  frontmatter. There is no workstream table.
- **Unit of work** (unit): a deterministic group of related events, such as a
  Slack thread or a GitHub PR with its commits. Units, not containers or single
  events, are what get linked to projects.
- **Project link**: a stored decision that one unit belongs to one project, to
  no project, or to a proposed new project.

Memory doctor candidates based on a shared acronym, such as `MMO GTM Tracker ->
Mindbody.Modernization.Orchestration (shared acronym MMO)`, are workstream
signals. Doctor shows them as workstream suggestions (see
`specs/memory.md`).

## Project Catalog

The catalog is read from `workgraph-memory/projects/*.md` on each use. Memory
files remain the record; there is no projects table.

Optional frontmatter:

```markdown
---
workstream: mmo
aliases: [GTM segments, segment tracker]
description: Go-to-market segment definitions for the MMO launch.
---
# MMO GTM Segments
```

- `workstream` is a lowercase kebab-case slug.
- `aliases` are extra names used by deterministic matching and shown to the
  LLM.
- `description` is one line, at most 200 characters. When it is absent, the
  first non-heading paragraph is used, truncated to 200 characters.
- A file without frontmatter is still a project with no workstream or
  aliases. Invalid frontmatter is reported by `memory doctor` and treated as
  absent; it never hides the project.

A catalog entry is `{slug, title, workstream, aliases, description}`. The
title is the first H1, falling back to the slug.

## Units Of Work

Units are computed at read time from stored events. Each unit has a stable id,
a kind, a container, a time range, and member event ids. Grouping is
deterministic: the same events always produce the same units and ids.

| Unit kind | Members | Unit id |
| --- | --- | --- |
| `slack.thread` | a root message and its replies | `slack.thread:<channel_id>:<thread_ts>` |
| `slack.burst` | non-thread messages in one channel or DM, split at a quiet gap of 2 hours | `slack.burst:<channel_id>:<first_ts>` |
| `github.pr` | GitHub events for one PR | `github.pr:<owner/repo>#<number>` |
| `git.session` | the user's commits in one local repository, split at a 2 hour gap | `git.session:<repo_path>:<first_sha>` |
| `notion.page` | the user's activity on one page on one local day | `notion.page:<page_id>:<date>` |
| `calendar.event` | one calendar event | the existing event identity `calendar.event:<provider>:<calendar_id>:<event_id>` |

- A Slack burst is closed, and eligible for categorization, only after its
  quiet gap has passed. An open burst is shown as activity but not
  categorized yet.
- A late-captured message that bridges two closed bursts merges them under the
  earlier id. Links on the later id are kept and shown by `projects explain`
  as belonging to a unit that no longer exists; they are not moved silently.
- Git commits are not absorbed into PR units. Git capture stores only the
  local `repo_path` and the branch checked out at capture time, which does
  not establish a canonical repository or PR membership. A commit session and
  a PR can still reach the same project through a shared identifier.
- Events that belong to no unit kind above, such as file events, are covered
  only by container mappings in the first slices.
- An event belongs to at most one unit.

## Project Links

Project links live in a SQLite `project_links` table:

```text
id, unit_id, unit_kind, container, target, project, proposed_project_json,
source, kind, summary, confidence, evidence_json, state, auto, rule,
profile, run_id, created_at, decided_at, decision
```

- `target` is `project`, `new_project`, or `none`. `project` holds the slug
  for `project`; `proposed_project_json` holds `{slug, name, description,
  workstream}` for `new_project`.
- `source` is `deterministic`, `llm`, or `user`.
- `kind` is one of `review`, `build`, `coordination`, `advice`, `unblock`,
  `decision`, or `fyi`. Deterministic links may leave it empty.
- `summary` is one line of at most 120 characters, stripped of control and
  format characters.
- `confidence` is between 0 and 1. Deterministic and user links use 1.
- `evidence_json` is a non-empty array of member event ids.
- `state` is `proposed`, `applied`, or `rejected`.
- `auto` is true when the link was applied without the user deciding it.
- `rule` names the deterministic rule; `profile` and `run_id` identify the LLM
  run.
- `decision` is recorded with `decided_at` when the user acts on an existing
  link:
  - `approved`: the user applied a proposed link
  - `confirmed`: the user kept an auto-applied link
  - `corrected`: the user replaced the link with another target
  - `undone`: the user undid an auto-applied link
- A link is **user-approved** when its `source` is `user` or its `decision` is
  `approved` or `confirmed`.

Contracts:

- at most one link per unit is `proposed` or `applied` (partial unique index)
- rows are never deleted; a decision changes `state` once and records
  `decision`
- reassigning, including choosing no project, rejects the old row with
  `decision = corrected` and inserts an applied `user` row in the same
  transaction
- undo rejects the auto-applied row with `decision = undone` and, in the same
  transaction, inserts a `proposed` replacement with the same target, source,
  and evidence, so review shows what was undone
- links never rewrite or delete events, and never edit memory files except
  when the user explicitly creates a new project
- deterministic and LLM runs consider only units with no `proposed` or
  `applied` link, so they never replace an existing link; a unit returned to
  review by undo cannot be reapplied automatically until the user decides it
- only the user changes a user-approved link
- the table is created on the first command that writes a link; read-only
  queries do not migrate the database

## Container Mappings

`memory link --source <container> <project>` remains a shortcut: every unit in
that container gets an applied deterministic link with `rule =
container_mapping`, unless a stronger rule or a user link applies.

A container may instead map to a workstream:

```text
workgraph memory link --workstream mmo --source Mindbody.Modernization.Orchestration
```

A workstream mapping creates no link. It narrows the LLM catalog for that
container's units to the workstream's projects (plus `none` and
`new_project`), and today and resume group its unlinked units under the
workstream. Workstream mappings are stored in `memory_project_sources` keyed by
the reserved pseudo-path `workstream:<slug>`. The `workstream:` prefix is
parsed before any path handling: it is never passed to `filepath.Abs` or the
memory-document helpers, and memory document paths, which are absolute, cannot
take that form.

## Deterministic Linking

Deterministic rules run before any LLM call, in this order. The first rule
that resolves to exactly one project wins:

1. **User link**: an existing user decision for the unit.
2. **Shared identifier**: the unit cites an identifier that appears in exactly
   one project's memory file or in units whose user-approved links name
   exactly one project. Auto-applied and proposed links never seed this rule,
   so an unconfirmed classification cannot spread. Identifiers are Azure Boards ids (`AB#1563394`), normalized URLs
   (PR, issue, Notion page), `owner/repo#number`, commit SHAs (prefix match as
   in `specs/event-associations.md`), and branch names within the same
   repository. Extraction reuses the association signal extractor and skips
   `untrusted` payload subtrees.
3. **Container mapping**: the unit's container is mapped to one project.

An identifier that matches more than one project produces no deterministic
link; the unit goes to the LLM with those projects listed as hints.

Deterministic links are applied with `auto = true`, `confidence = 1`, and the
rule recorded, so they are visible and undoable like any auto-applied link.

## LLM Categorization

This is the `associate_events` task in `specs/llm-integration.md`.

### Input

- units with no applied or proposed link, at most 50 per run, oldest first
- per unit: id, kind, container display name, time range, participants'
  display names, cited identifiers, and member events (id, timestamp, actor
  name, summary or title) capped at 20 events and 2,000 characters
- the catalog: slug, title, workstream, aliases, description; narrowed by a
  workstream mapping when one applies
- up to 20 recent user decisions: container, unit summary, chosen project or
  rejection

Every part of the payload passes the source policy in Privacy below.

Captured text is untrusted and is placed in a delimited data section separate
from the instructions. Outbound filtering runs before any hosted call.

### Output

```json
{
  "schema": "workgraph.associate_result.v1",
  "units": [
    {
      "unit_id": "slack.thread:C123:1760000000.000100",
      "target": "project",
      "project": "mmo-harness",
      "new_project": null,
      "kind": "advice",
      "summary": "Advised Sam on agentic tooling over GraphQL for the harness",
      "confidence": 0.82,
      "evidence": ["slack.message:C123:1760000000.000100"]
    }
  ]
}
```

### Validation

Each result entry is validated independently. An entry is discarded, and
counted in the run report, when:

- its `unit_id` was not in the request, or appears twice
- `target = project` names a slug not in the catalog given to the model
- `target = new_project` lacks a valid slug and name, or reuses an existing
  slug
- `kind` is not in the enum, or `confidence` is not a number in [0, 1]
- `evidence` is empty or cites an id that is not a member of the unit
- `summary` is empty

Non-JSON output fails the run, and the units stay uncategorized.

### Privacy

LLM categorization is opt-in per source:

```text
workgraph llm sources --for associate
workgraph llm sources --for associate --enable slack
workgraph llm sources --for associate --enable slack.dm
workgraph llm sources --for associate --disable github
```

Sources are `slack`, `slack.dm`, `github`, `git`, `notion`, and `calendar`.
None is enabled by default. `slack.dm` covers DMs and group DMs and must be
enabled separately from `slack`. The setting is stored in `llm.json` as
`task_sources.associate`. Units from disabled sources still get deterministic
links.

The policy applies to the whole payload, not only to unit selection:

- each member event is checked against its own source; events from disabled
  sources are omitted, and a unit with no remaining members is not sent
- participants, identifiers, and the time range are derived only from the
  members that remain
- a recent user decision is included only when its unit passes the same
  policy
- the catalog contains only project memory frontmatter, titles, and
  descriptions

Slack capture must persist the conversation type (`channel`,
`private_channel`, `im`, or `mpim`) in the event payload before D3. A Slack
event whose type is missing or unknown, including events captured before
that change, is treated as `slack.dm`.

`workgraph llm associate --dry-run` prints the profile, destination, enabled
sources, and the exact filtered payload, and sends nothing.

### Model

The task route is `associate`:

```text
workgraph llm use local-gemma --for associate
workgraph llm connect claude-code --name haiku --model claude-haiku-5-5 --for associate
```

A small, fast model is the intended default. Existing `categorize` routes are
read as `associate`.

## Auto-Apply

An LLM result is applied automatically, with `auto = true`, only when all of
these hold:

- `confidence >= 0.95`
- the entry passed validation
- `target` is `project` or `none`; new-project proposals are never
  auto-applied
- the user has not rejected a link from this container to this project in
  the last 30 days
- auto-apply is not paused

Every other valid result is stored as `proposed`.

`workgraph projects undo <link-id>` and `workgraph projects undo --run
<run-id>` undo auto-applied links, deterministic or LLM. Each unit gets a
proposed replacement that waits for the user (see Project Links).

### Accuracy Gate

Auto-apply accuracy uses links with `auto = true` and `source = llm` whose
`decided_at` falls in the last 14 days:

```text
accuracy = confirmed / (confirmed + corrected + undone)
```

- Fewer than 20 decided links is `insufficient_data`; auto-apply stays on.
- While the sample is `insufficient_data`, every undecided auto-applied link,
  deterministic or LLM, appears in `projects review` and in the `next`
  Categorize count beside proposed links, so the evening review builds the
  sample.
- When measured accuracy over at least 20 decided links is below 0.95,
  auto-apply pauses and workgraph appends a `projects.auto_apply_paused`
  event. New results are stored as `proposed`, and `projects review`, `next`,
  and `workgraph review` say auto-apply is paused and why.
- A pause lifts only when measured accuracy over at least 20 decided links is
  back at or above 0.95, recorded as a `projects.auto_apply_resumed` event. A
  sample that falls below 20 does not lift a pause.
- After the sample reaches 20, `projects review --auto` lists recent
  auto-applied links for spot-checks.

`workgraph review` reports this metric, its 14-day window, and its sample size
beside the suggestion metrics in `specs/effectiveness-review.md`; it does not
use that spec's windows.

## Scheduling

Categorization runs through the bridged-capture outbox
(`specs/bridged-capture.md`). No new scheduler is added.

- `capture_requests` gains a `kind` column: `capture` (default) or
  `llm_task`. LLM requests use connector id `llm.associate` and the existing
  one-active-request-per-connector index, leases, retries, backoff, and
  cancellation. They have no capture cursor.
- The daemon emits a request hourly and once at 05:00 local, only when
  eligible uncategorized units exist. `params_json` holds the unit ids; the
  payload is rebuilt when the request is claimed.
- **Direct path**: when the `associate` route is a direct or `ai-client`
  profile, the daemon claims and runs the request itself as worker `daemon`.
- **Bridge path**: when the routed profile is an `ai-client` profile with
  `"execution": "bridge"`, the daemon leaves the request for the installed
  bridge worker, which runs it with that profile's client and model and
  submits through `workgraph llm associate submit --request <id> --claim-file
  <path>` or the MCP tool `llm_task_submit`.
- Completion validates the result, writes links, and completes the request in
  one transaction. It does not advance a capture cursor or connector runtime
  state.
- Emit, claim, and complete are kind-aware. `llm.associate` is accepted
  without registering a capture connector. A claim without an explicit kind
  claims only `capture` requests, so existing capture-only workers behave
  exactly as before; bridge workers claim `llm_task` requests only when they
  ask for that kind.
- The 05:00 run repeats, up to 10 requests, until no eligible units remain.
- `workgraph llm associate` runs one batch now with the routed profile. From
  D4, with a bridge profile it emits a request and prints its id.

## Review

```text
workgraph projects list
workgraph projects review [--auto] [--json]
workgraph projects review --approve <link-id>
workgraph projects review --reject <link-id>
workgraph projects review --assign <unit-id> --project <slug>
workgraph projects review --assign <unit-id> --new <slug> [--workstream <slug>]
workgraph projects undo <link-id> | --run <run-id>
workgraph projects explain <unit-id>
```

- `projects list` prints workstreams, their projects, and projects with no
  workstream, with applied unit counts for the last 7 days.
- Interactive `projects review` shows each proposed unit, oldest first:
  container, time range, kind, summary, proposed target, confidence, and
  evidence ids. Keys: `a` approve, `r` reassign (prompts for a slug), `n` new
  project, `x` no project, `s` skip, `q` quit. EOF or empty input skips.
- Approving an applied auto link records `decision = confirmed`. Approving a
  proposed link applies it with `source` unchanged, `auto = false`, and
  `decision = approved`.
- `--reject` and `x` assign no project: the link is corrected to a user link
  with `target = none`.
- `--new` creates project memory with the existing `memory init` starter,
  adds the workstream frontmatter when given, and applies a user link.
- `projects explain` prints the unit's members, every link row with its
  source, rule or run, and decision.
- Flag forms make the same changes as the keys and never prompt.

## Daily Loop Surfaces

- **`next`** gains a `categorize` group after `ready`. Text output shows at
  most three proposed units and one line `Categorize: <n> awaiting a
  decision, <m> auto-applied today (workgraph projects review)`. JSON items
  use reason `categorize`, id `projects:categorize:<link-id>`, and evidence
  from the link. The item closes when the link is decided. See
  `specs/next.md`.
- **`today`** groups activity by workstream, then project, using applied
  links; unlinked activity falls back to its container. Each project line
  lists the kinds of its units. See `specs/today.md`.
- **`resume`** uses the same grouping. The morning overview shows activity
  since the last check-in per project, including PR units opened by agents
  overnight, and the count of remaining decisions. See `specs/resume.md`.

## Slices

- **D1 Model and container wording**: catalog frontmatter, `projects list`,
  workstream mappings, doctor workstream suggestions, and "container" wording
  in memory and resume output.
- **D2 Units and deterministic linking**: unit grouping, `project_links`,
  deterministic rules, `projects explain`, `undo`, and flag-form review.
- **D3 LLM categorization**: Slack conversation type capture, `llm
  associate` with dry run, source opt-in, validation, auto-apply, accuracy
  gate, interactive review, and review metrics. D3 runs only directly or
  manually with the routed profile; there is no outbox or bridge execution.
- **D4 Scheduling and daily loop**: kind-aware `llm.associate` outbox
  requests, direct and bridge execution, and the `next`, `today`, and
  `resume` surfaces. Existing capture-only workers are unchanged. D4 depends
  on in-flight changes to those commands.

## Deferred

- linking one unit to several projects
- absorbing git commits into PR units
- file, mail, and Azure Boards units
- LLM-chosen unit boundaries
- workstream memory files and workstream-level descriptions
- learned per-container priors beyond recent user decisions

## Facts

- **P1. Catalog reads frontmatter.** Project files with and without
  frontmatter appear in `projects list` under their workstream; invalid
  frontmatter is reported by doctor and the project is still listed.
- **P2. Units are deterministic.** The same Slack, GitHub, git, Notion, and
  calendar fixtures produce the same unit ids and members in any capture
  order; calendar unit ids match stored calendar event ids; an open burst is
  not eligible.
- **P3. Deterministic rules resolve in order.** A thread citing a PR URL in
  one project's memory links to that project even though its channel is
  mapped to another; an identifier in two projects produces no link; an
  identifier seen only in an auto-applied or proposed link seeds nothing.
- **P4. Workstream mappings narrow the catalog.** A container mapped to `mmo`
  sends only `mmo` projects in the dry-run payload and creates no link.
- **P5. Dry run sends nothing and respects opt-in.** With only `slack`
  enabled, the payload contains channel units but no DM, unknown-type Slack,
  or GitHub units, and no decision context from those sources; the fake
  provider receives no request.
- **P6. Invalid results are discarded.** Unknown units, unknown projects,
  foreign evidence ids, bad kinds, and out-of-range confidence are discarded
  and counted; valid entries in the same response are stored.
- **P7. Auto-apply gates hold.** Confidence 0.95 applies, 0.94 proposes, a new
  project proposes, a recent rejection for the pair proposes, and measured
  accuracy below 0.95 over 20 decided links pauses auto-apply and says so;
  the pause survives the sample dropping below 20; with fewer than 20
  decided links, undecided auto links are listed in review.
- **P8. Review and undo record decisions.** Approve, reassign, reject, new
  project, and undo record the documented state and decision without editing
  events; undo leaves a proposed replacement that no later deterministic or
  LLM run reapplies; user-approved links survive later runs.
- **P9. Scheduled requests use the outbox.** The daemon emits one
  `llm.associate` request when eligible units exist and none when they do
  not; a claim without a kind never returns it to a capture worker; a
  bridge-executed request completes only with a valid claim token.
- **P10. Daily surfaces group by project.** `next` shows the categorize line,
  and `today` and `resume` group by workstream, then project, then container.
