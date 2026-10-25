# Projects, Workstreams, And Categorization

Status: D1 through D4 implemented. Slices are listed in
`specs/roadmap.md`. Facts live in `facts/projects*_test.go`.

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
  file at `workgraph-memory/projects/<slug>.md`. Examples: `orbit-segments`,
  `harbor-terms-and-phrases`, `harbor-harness`, `atlas-bot`. A project spans many
  containers and its slug is its identity.
- **Workstream**: an optional parent of projects, such as `harbor` over
  `harbor-harness` and `orbit-segments`. It is declared by
  `workstream: <slug>` in project memory frontmatter. There is no workstream
  table.
- **Unit of work** (unit): a deterministic group of related events, such as a
  Slack thread or a GitHub PR with its commits. Units, not containers or single
  events, are what get linked to projects.
- **Project link**: a stored decision that one unit belongs to one project, to
  no project, or to a proposed new project.

Memory doctor candidates based on a shared acronym, such as `HPO Segment Tracker ->
Helix.Platform.Operations (shared acronym HPO)`, are workstream
signals. Doctor shows them as workstream suggestions (see
`specs/memory.md`).

## Project Catalog

The catalog is read from `workgraph-memory/projects/*.md` on each use. Memory
files remain the record; there is no projects table.

Optional frontmatter:

```markdown
---
workstream: harbor
aliases: [orbit segments, segment tracker]
description: Segment definitions for the Orbit launch.
---
# Orbit Segments
```

- `workstream` is a lowercase kebab-case slug.
- `aliases` are extra names used by deterministic matching and shown to the
  LLM.
- `description` is one line, at most 200 characters. When it is absent, the
  first non-heading paragraph is used, truncated to 200 characters.
- A file without frontmatter is still a project with no workstream or
  aliases. Invalid frontmatter is reported by `memory doctor` and treated as
  absent; it never hides the project.
- Frontmatter starts with `---` on the first line and ends at the next `---`
  line. Each line is `key: value`, a `- item` continuing a list, blank, or a
  `#` comment. `aliases` is a `[a, b]` flow list or a `- item` block list.
  Values may be quoted. Unknown keys are ignored. Frontmatter is invalid when
  it is not closed, a line has no key, `workstream` is not a slug, or
  `description` is longer than 200 characters.

A catalog entry is `{slug, title, workstream, aliases, description}`. The
title is the first H1, falling back to the slug.

The title and a paragraph description are Markdown read as plain text: HTML
character references such as `&amp;` are decoded, so `# Terms &amp; Phrases`
and `# Terms & Phrases` both give `Terms & Phrases`. Text output and the
`associate_events` request show `&` as written, never as `&amp;` or
`\u0026`; the request keeps `<` and `>` JSON-escaped so captured text cannot
spell its data delimiters.

Catalog titles and descriptions, from frontmatter or the first paragraph,
are display text: after decoding, ANSI escape sequences, C0 and
C1 control characters, DEL, and format characters such as bidi overrides are
removed, and every run of whitespace, including line and paragraph
separators, becomes one space. A memory file therefore cannot clear the
terminal, move the cursor, or start a new output line.

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

- A Slack message is a thread member when it has a `thread_ts` or its `ts`
  is the `thread_ts` of another stored message in the same channel, so a
  root captured before its replies joins the thread.
- A Slack burst is closed, and eligible for categorization, only after its
  quiet gap has passed. An open burst is shown as activity but not
  categorized yet.
- GitHub repositories in unit ids are lowercased. Notion page units use the
  user's page edit events (`notion.page_updated`), and the local day is the
  day of the event timestamp in local time.
- A unit's container is the container of its earliest member.
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
- `rule` names the deterministic rule and `profile` names the LLM profile.
  `run_id` identifies the deterministic linking pass or LLM run that wrote
  the link; user links have none.
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
workgraph memory link --workstream harbor --source Helix.Platform.Operations
```

A workstream mapping creates no link. It narrows the LLM catalog for that
container's units to the workstream's projects (plus `none` and
`new_project`), and today and resume group its unlinked units under the
workstream. `memory unlink --workstream <slug> --source <container>` removes
the mapping. Workstream mappings are stored in `memory_project_sources` keyed by
the reserved pseudo-path `workstream:<slug>`. The `workstream:` prefix is
parsed before any path handling: it is never passed to `filepath.Abs` or the
memory-document helpers, and memory document paths, which are absolute, cannot
take that form.

A container may also map to several projects, by running `memory link
--source <container> <project>` once per project. Such a container gets no
deterministic link. Instead it narrows the LLM catalog for its units to the
mapped catalog projects (plus `none` and `new_project`), the same way a
workstream mapping does. A container mapped to exactly one project narrows
nothing; its units reach the LLM only when a stronger rule withheld the
link.

When a container has both a workstream mapping and two or more mapped
projects, its catalog is the union of the workstream's projects and the
mapped projects. The union never drops a project the user mapped explicitly,
which an intersection would whenever a mapped project sits outside the
workstream.

Narrowing never removes a project that the unit's identifiers name. A
narrowed catalog also includes every catalog project that the shared
identifier rule matches for the unit, so a unit whose identifiers split
between a mapped project and another project still gets both as candidates.
Those identifiers come only from the unit's permitted members, and the
user-approved links that seed the rule contribute only identifiers from
members whose source is enabled.

`projects list` prints `  Containers: <container>, ...` beneath each project
that has mapped containers, so a container mapped to several projects is
listed under each of them. `memory doctor`, and the mapping hints in `today`
and `resume`, never offer a container that already has a project or
workstream mapping.

## Deterministic Linking

Deterministic rules run before any LLM call, in this order. The first rule
that resolves to exactly one project wins:

1. **User link**: an existing user decision for the unit.
2. **Shared identifier**: the unit cites an identifier that appears in exactly
   one project's memory file or in units whose user-approved links name
   exactly one project. Auto-applied and proposed links never seed this rule,
   so an unconfirmed classification cannot spread. Identifiers are Azure Boards ids (`AB#1234567`), normalized URLs
   (PR, issue, Notion page), `owner/repo#number`, commit SHAs (prefix match as
   in `specs/event-associations.md`), and branch names within the same
   repository. Extraction reuses the association signal extractor and skips
   `untrusted` payload subtrees.
3. **Container mapping**: the unit's container is mapped to one project.

An identifier that matches more than one project produces no deterministic
link; the unit goes to the LLM with those projects listed as hints.

- A unit's identifiers come from its member events. When they name more than
  one project in total, the unit gets no deterministic link at all, including
  from a container mapping.
- Memory files contribute PR and issue URLs, `owner/repo#number` references,
  commit URLs, Notion page URLs, and Azure Boards ids found in their text.
- URL identifiers are GitHub PR, issue, and commit URLs, compared as
  `owner/repo#number` or commit SHA, and Notion page URLs, compared by the page
  id at the end of the URL; a Notion page event's own id matches them. Other
  URLs, including Notion URLs without a page id, are not identifiers.
- Branch identifiers are `<repository>@<branch>`, where the repository is the
  head repository, GitHub `owner/repo`, or git `repo_path` recorded in the
  same payload object as the branch. `main`, `master`, `develop`, and `trunk`
  are not identifiers.
- A container mapping applies only when the container maps to exactly one
  catalog project.
- Open Slack bursts are not linked until they close.

A deterministic pass runs at the start of every `projects review` that lists
links (not the flag forms), and before `llm associate` selects units, except
in a dry run, which writes nothing. Each
pass that writes links records them under one new `run_id`; a pass with
nothing to link writes nothing.

Deterministic links are applied immediately, not proposed, with `auto =
true`, `confidence = 1`, the rule, and the `run_id` of the linking pass, so
they are visible and undoable like any auto-applied link.

## LLM Categorization

This is the `associate_events` task in `specs/llm-integration.md`.

### Input

- units with no applied or proposed link, at most 50 per run, oldest first
- per unit: id, kind, container display name, time range, participants'
  display names, cited identifiers, and member events (id, timestamp, actor
  name, summary or title) capped at 20 events and 2,000 characters
- the catalog: slug, title, workstream, aliases, description; narrowed by a
  workstream mapping or a mapping to several projects when one applies, and
  always including projects the unit's identifiers name
- up to 20 distinct recent user decisions: container, decision, target,
  project, kind, and how many times the user made that decision

Every part of the payload passes the source policy in Privacy below.

The payload is one JSON document:

```json
{
  "schema": "workgraph.associate_request.v1",
  "projects": [{"slug": "harbor-harness", "title": "Harbor Harness", "workstream": "harbor", "aliases": [], "description": "..."}],
  "units": [
    {
      "unit_id": "slack.thread:C123:1760000000.000100",
      "kind": "slack.thread",
      "container": "harness-team",
      "start": "2026-10-20T09:00:00Z",
      "end": "2026-10-20T09:20:00Z",
      "participants": ["Sam"],
      "identifiers": ["https://github.com/acme/harness/pull/42"],
      "projects": ["harbor-harness"],
      "events": [{"id": "slack.message:C123:1760000000.000100", "timestamp": "2026-10-20T09:00:00Z", "actor": "Sam", "summary": "..."}]
    }
  ],
  "recent_decisions": [{"container": "harness-team", "decision": "chose", "target": "project", "project": "harbor-harness", "kind": "advice", "count": 3}]
}
```

- `projects` is the union of the catalogs of the units in the request. Each
  unit's `projects` lists the slugs it may be linked to: its container's
  catalog after any workstream or several-project narrowing, plus projects
  its identifiers name.
- The 50-unit limit counts units after the source policy, so units with no
  remaining members do not use up the batch.
- Event summaries are capped so a unit's summaries total at most 2,000
  characters; events past the cap are left out.
- A recent decision is a user link (`chose` its target) or an LLM or
  deterministic link the user corrected or undid (`rejected` its target),
  newest first. It carries only structural fields: the container of its
  earliest evidence event, the decision, target, project, and kind. Stored
  link summaries are never sent, because their provenance cannot be checked
  against the source policy.
- A decision is included only when its project is `none` or a project
  declared in the current catalog, so a rejected new-project slug that was
  never created is left out, and only when every evidence event belongs to
  a current unit whose members are all permitted. Each evidence event is
  resolved to the unit that contains it now, so a decision on a unit that
  was regrouped, for example a burst that became a thread, is judged by the
  new unit; a decision whose evidence is in no current unit is left out.
- Decisions with the same container, decision, target, project, and kind are
  one entry whose `count` is how many of them passed the source policy, so
  the 20 entries are 20 different decisions. Entries are ordered by their
  newest decision. Counting reads at most the 500 newest decisions. A
  decision from a disabled source neither adds an entry nor adds to a count.
  The prompt says that each entry is distinct and what `count` means. When a
  stored snapshot is reused, an entry without `count` counts as 1, repeated
  entries are summed, and each count is capped at the count currently
  permitted. Adding
  `count` is a compatible change, so the schema stays
  `workgraph.associate_request.v1`.
- A unit's container, time range, participants, and identifiers come from its
  permitted members only, and catalog narrowing uses that container. When the
  policy removed any member, the unit is sent under an opaque per-request id
  (`unit-<n>`) that is mapped back to the real unit id on validation.

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
      "project": "harbor-harness",
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
- `kind` is not in the enum, or `confidence` is missing, null, or not a
  number in [0, 1]
- `evidence` is empty, repeats an id, or cites an id that is not a member of
  the unit
- `summary` is empty
- `target` is not `project`, `new_project`, or `none`, or the entry is not a
  JSON object of the documented shape

`target = project` must name a slug from that unit's `projects`. Every entry
that shares a duplicated `unit_id` is discarded, including entries that fail
other validation. Units the model leaves out stay uncategorized. Proposed
project names and descriptions are stripped of control and format characters
and bounded like summaries, and model-supplied ids in the run report are
quoted and escaped.

Non-JSON output fails the run, and the units stay uncategorized. A response
wrapped in one Markdown code fence is read as the JSON inside it. A response
larger than 1 MiB fails the run.

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
- a recent user decision is included only when every evidence event of its
  link is still stored and comes from an enabled source, and it carries no
  summary text
- the catalog contains only project memory frontmatter, titles, and
  descriptions

Slack capture must persist the conversation type (`channel`,
`private_channel`, `im`, or `mpim`) in the event payload before D3. A Slack
event whose type is missing or unknown, including events captured before
that change, is treated as `slack.dm`.

`workgraph llm associate --dry-run` prints the profile, destination, enabled
sources, and the exact filtered payload, and sends nothing. For a hosted
profile the printed request has already passed the outbound filter.
`workgraph llm sources --for associate` with no flag prints the enabled
sources. `associate` is the only task with sources. When no unit passes the
policy, `llm associate` reports that and calls no model.

### Model

The task route is `associate`. It accepts any profile configured with
`workgraph llm`, local or hosted, and the task contract is the same for every
provider:

```text
workgraph llm use local-gemma --for associate
workgraph llm add hosted-mini --provider openai-compatible --base-url <url> --model <model> --api-key-env <VAR>
workgraph llm use hosted-mini --for associate
workgraph llm connect claude-code --name haiku --model claude-haiku-5-5 --for associate
```

Both execution modes in Scheduling are supported: a direct API call from
workgraph, or a signed-in client through the bridge. A small, fast model is
the intended default. Existing `categorize` routes are read as `associate`.

## Auto-Apply

Every LLM link records the profile that decided it in `profile`. An LLM result
is applied automatically, with `auto = true`, only when all of these hold:

- `confidence` is at or above the deciding profile's auto-apply threshold
- the entry passed validation
- `target` is `project` or `none`; new-project proposals are never
  auto-applied
- the user has not rejected a link from this container to this project in
  the last 30 days
- auto-apply is not paused for the deciding profile

Every other valid result is stored as `proposed`. A rejection is a link
from the same container with the same target and project that the user
corrected or undid; for `target = none` the pair is the container and none.
`none` results stay reviewable like any other proposal; nothing hides them.

`workgraph projects undo <link-id>` and `workgraph projects undo --run
<run-id>` undo auto-applied links, deterministic or LLM. Each unit gets a
proposed replacement that waits for the user (see Project Links).

### Thresholds

Each profile carries its own auto-apply threshold, so a model whose
confidences top out below 0.95 can still auto-apply without a code change:

```text
workgraph llm add haiku --provider ai-client --client claude-code --model <model> --auto-apply-confidence 0.9
workgraph llm set haiku --auto-apply-confidence 0.9
workgraph llm set haiku --verify-floor 0.6
```

- The threshold is stored as `auto_apply_confidence` on the profile in
  `llm.json`. Without it the threshold is 0.95. It must be a number from 0.5
  to 1.0.
- `verify_floor` is the lowest confidence from this profile that is worth a
  verifier call (see Verifier). Without it the floor is 0.6. It must be a
  number from 0 to 1.
- `llm set <profile>` changes only the settings it is given on an existing
  profile and needs at least one. `llm add` replaces the whole profile, so
  settings it is not given return to their defaults.
- `llm list` prints each profile's threshold and floor, as `auto-apply 0.95,
  verify-floor 0.60`.
- A link whose profile is no longer configured is judged against 0.95. When
  `llm.json` cannot be read, every profile is judged against 0.95.

### Accuracy Gate

The gate runs per deciding profile. A profile's auto-apply accuracy uses its
links with `auto = true` and `source = llm` whose `decided_at` falls in the
last 14 days, up to now; later timestamps are not counted:

```text
accuracy = confirmed / (confirmed + corrected + undone)
```

The accuracy a profile must keep is its own auto-apply threshold. A
confidence is the model's claim of how likely a link is to be right, so the
links it auto-applied at or above the threshold should be right at least that
often; one setting controls both, and lowering it is an explicit choice to
trade precision for fewer reviews. A model that is worse than it claims still
pauses.

- Fewer than 20 decided links is `insufficient_data`; that profile's
  auto-apply stays on.
- While a profile's sample is `insufficient_data`, every undecided
  auto-applied LLM link from that profile appears in `projects review` and in
  the `next` Categorize count beside proposed links, so the evening review
  builds the sample. Deterministic links never appear there: they come from
  rules and container mappings the user set up and are not counted, so they
  stay available through `projects review --auto`, `projects explain`, and
  `projects undo`.
- When measured accuracy over at least 20 decided links is below the
  profile's threshold, auto-apply pauses for that profile and workgraph
  appends a `projects.auto_apply_paused` event naming it. Its new results are
  stored as `proposed`, and `projects review`, `next`, and `workgraph review`
  say auto-apply is paused for it and why. A pause on one profile does not
  stop another.
- A pause lifts only when measured accuracy over at least 20 decided links is
  back at or above the threshold, recorded as a `projects.auto_apply_resumed`
  event naming the profile. A sample that falls below 20 does not lift a
  pause.
- After a profile's sample reaches 20, its undecided auto-applied LLM links
  leave the default review and remain available through `projects review
  --auto`.
- A profile's pause state is its latest `projects.auto_apply_paused` or
  `projects.auto_apply_resumed` event, overridden by the measured result
  whenever at least 20 links are decided. An event without a profile, written
  before the gate was per profile, applies to every profile that has no event
  of its own. Reading the metric never writes. Every write to
  `project_links`, including approve, assign, reject, and undo, and every
  `llm associate` run that is not a dry run appends an event for each profile
  whose computed state differs from its latest event. `llm associate` does so
  before it selects units or applies anything, so a pause is recorded even
  when no unit is eligible.
- The gate reports every profile that has LLM links or a pause event, sorted
  by name, one line each: `Project auto-apply accuracy for <profile>: <a>
  (<confirmed>/<decided> decided auto links, 14 day window, required <t>)`, or
  `Project auto-apply accuracy for <profile>: insufficient_data (<decided>
  decided auto links, 14 day window)`, then `Auto-apply paused: <reason>` for
  each paused profile. The reason starts with the profile name. With no such
  profile the line is `Project auto-apply accuracy: insufficient_data (0
  decided auto links, 14 day window)`.

`workgraph review` reports this metric, its 14-day window, and its sample size
beside the suggestion metrics in `specs/effectiveness-review.md`; it does not
use that spec's windows. Its JSON `project_auto_apply` and the `projects
review --json` `auto_apply` object carry `window_days`, `paused` (any profile
paused), `pause_reason` (the paused profiles' reasons joined by `; `), and
`profiles`, one object per reported profile with `profile`,
`required_accuracy`, `status`, `accuracy`, `sample_size`, `confirmed`,
`corrected`, `undone`, `paused`, and `pause_reason`. This replaces the
earlier single-profile shape: the top-level `status`, `accuracy`,
`sample_size`, `confirmed`, `corrected`, and `undone` fields are gone, and
consumers read them from `profiles`.

### Verifier

A second, usually stronger, profile can decide the units the associate
profile is unsure of:

```text
workgraph llm use sonnet --for associate-verify
```

- The route is `associate-verify`. It is used only when set; it never falls
  back to the default profile. When it names the associate profile itself, no
  unit is verified. Without the route, categorization is unchanged.
- After the associate pass validates its results, a unit is escalated when
  its valid result has confidence at or above the associate profile's verify
  floor and below that profile's auto-apply threshold. Results at or above
  the threshold, below the floor, and discarded entries are never escalated.
- The verifier receives only the escalated units, each exactly as the
  associate profile received it, with the associate result added as
  `primary_answer` (`target`, `project`, `new_project`, `kind`, `summary`,
  `confidence`), plus the catalog entries those units offer and the same
  recent decisions. It never receives more than the associate profile saw.
  The verifier payload is built from the payload the associate model
  actually received, after its outbound filter, never rebuilt from current
  data and never from an earlier attempt: a direct run uses what it just
  sent, a bridge claim stores the filtered payload it returned, and a
  verify request stores the escalated units, their catalog entries, the
  recent decisions, and whether the associate request passed the hosted
  outbound filter. Before that stored payload is reused, it is checked
  against current permissions: a unit whose permitted members changed, or
  that is no longer eligible, is not verified, a catalog entry no longer
  offered to the unit is dropped, and a decision no longer permitted is
  dropped. Nothing added after the associate pass is sent, and a redaction
  made for the associate is never undone. The request passes the current
  hosted outbound filter, on top of those redactions, when either profile is
  hosted.
- The verifier answers in the associate result shape and is validated the
  same way. Its valid result becomes the unit's link, with `profile` set to
  the verifier, so auto-apply uses the verifier's threshold, recent
  rejections, and accuracy gate. Both results are stored in
  `project_link_verifications` (link id, both profiles, and both results), and
  `projects explain` prints them under the link.
- When the verifier leaves a unit out or its entry is discarded, the
  associate result is stored for that unit unchanged; discards are counted
  with a `verifier:` prefix. When a direct verifier call fails, every
  escalated unit keeps its associate result and the run report says why.
  Before the associate model is called, the verifier passes the same hosted
  opt-in and managed-provider checks as the associate profile, so a verifier
  that is not allowed fails the run before anything is sent. The checks run
  again immediately before every verifier call, with `llm.json` and the source
  policy read again at that moment, so withdrawn hosted consent, a disabled
  source, or a route changed to a profile that is not allowed sends nothing
  for the affected units, and they keep their associate results.
- The verifier follows the execution rule of its own profile. A direct
  verifier is called in the same run as the associate pass, whether that pass
  ran directly or was submitted by the bridge, before any link is written. A
  bridge verifier gets an `llm_task` request on the same `llm.associate`
  connector, with `params_json` `{"task": "associate-verify", "unit_ids":
  [...], "primary": {...}}`, emitted in the transaction that writes the
  associate links and completes the associate request; escalated units are not
  linked until it completes. Because the connector has one active request, no
  associate request is emitted while a verify request is active, and a direct
  `llm associate` run with a bridge verifier refuses to start while any
  `llm.associate` request is active, printing it.
- A verify request is claimed by the client of the verifier route and runs
  with that profile's model and config directory. The claim transaction only
  selects requests whose task this worker's client is routed to, so a worker
  authorized for an associate request cannot claim a verify request that
  replaced it, and preparing the task checks the claiming worker against the
  task's profile again before any message is returned. The claim records the
  profile it was prepared for; a submission after the route for that task
  changed is refused and stores nothing, and the worker fails the request so
  the routed profile claims it again. When the verifier route
  runs directly at claim time, the daemon runs the request; when the route is
  gone, the daemon completes it by storing the associate results.
- A `system-one` associate profile escalates the same way, using the returned
  probability of the chosen option as the confidence; its verifier payload is
  built from the same filtered field values as its System One requests, so a
  redaction made for System One is never undone, and units it skipped,
  discarded, or redacted only in the composed request form are not
  escalated. A `system-one` profile cannot be the verifier:
  routing one is refused, and a hand-routed one fails as a verifier call (see
  `specs/llm-integration.md`).
- `--dry-run` sends nothing and prints `Verifier: <profile>, confidence <floor>
  to below <threshold>`. A run report adds `Escalated: <n>`, and, when a
  verifier ran, `Verified: <n> by <profile> (<changed> changed)`, or
  `Verify request emitted: <id> (<n> units)` for a bridge verifier.

### Backlog Verification

Links proposed before a verifier was routed are never escalated by the
associate pass, so they can wait in review indefinitely. `llm verify-backlog`
sends them to the verifier:

```text
workgraph llm verify-backlog --dry-run
workgraph llm verify-backlog [--limit 50] [--since 168h]
```

- A waiting link is a `proposed` LLM link with no decision, no stored
  verification, and a profile other than the verifier, whose confidence is at
  or above its own profile's verify floor and below that profile's auto-apply
  threshold, and whose target is `none` or a project declared in the current
  catalog. A link whose profile is no longer configured is judged against
  0.6 and 0.95. A unit that has any decided link, including an undone or
  corrected one, is never verified. `--since` keeps links created within that
  duration. Links are taken oldest first.
- New-project proposals, and proposals for a project that is no longer
  declared, in the band are not sent; they stay in `projects review` and are
  counted as `Left in review, new or undeclared project: <n>`.
- It refuses before reading the backlog when `associate-verify` is not
  routed, names a profile that is not configured, names a `system-one`
  profile, or names a profile with bridge execution: backlog verification
  calls the verifier directly. Outside a dry run, the verifier passes the
  hosted opt-in and managed-provider checks first.
- Stored model text cannot be traced to the members, other units, catalog,
  or redactions that produced it, so backlog verification never reuses a
  snapshot and never sends stored model text. Each unit is rebuilt from
  current content under the current source policy, catalog, and managed
  redactions, with the current recent decisions, and the request passes the
  hosted outbound filter when the link's profile is hosted or no longer
  configured, or the verifier is hosted. A link is sent only when every
  current member of its unit is permitted (the unit would not be sent under
  an opaque id) and the link's evidence is among those members; any other
  link is skipped. `primary_answer` holds only the waiting link's `target`,
  its `project` when the target is a declared project, and its
  `confidence`; the stored summary, kind, and new-project fields are never
  sent. The verifier answers in the associate result shape, as in Verifier.
- The consent, route, and source-policy checks run again immediately before
  every verifier call, as in Verifier. Each request carries at most 50 units,
  and a run sends at most `--limit` units, 50 by default.
- Each request's verdicts are written in one transaction that first records
  the verifier's pause transition and re-reads every link: a link that is no
  longer proposed and undecided, that has a verification, or whose unit has a
  decided link is skipped. A verdict replaces the waiting link in place, with
  `profile` set to the verifier, `run_id` set to this run, so `projects
  undo --run` undoes what it applied, and `created_at` set to the time of
  the verdict, so `projects review --auto --since` and the `next` auto count
  see it as new. `projects review --auto` lists newest `created_at` first.
  It is applied only as in Auto-Apply for the verifier: its threshold, recent rejections, new-project rule, and
  accuracy gate. Both results are stored in `project_link_verifications`.
- A link the verifier left out or whose entry was discarded keeps the
  associate result and is recorded in `project_link_backlog_checks` (link
  id, verifier profile, time), so the same verifier is not asked again. A
  failed call writes nothing and stops the run, so the links stay waiting.
  Running the command twice never sends the same link to the same verifier
  twice after it answered.
- `--dry-run` reads only. It prints the verifier, provider, model,
  destination, enabled sources, `Waiting in the verify band: <n>`, `Left in
  review, new or undeclared project: <n>`, the limit,
  `Would send: <n>`, `Skipped: <n>`, and the first request exactly as it
  would be sent, after the outbound filter.
- A run prints `Run`, `Sent`, `Verified`, `Applied`, `Kept for review`
  (verified but proposed, plus links the verifier left out), `Corrected`
  (the verifier chose a different target), `Skipped`, `Discarded` with
  `verifier:` reasons, `Verifier failed: <reason>` when a call failed,
  `Remaining`, and the accuracy summary.
- Backlog verification runs only when the user runs the command; the daemon
  never runs it.

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

### Scheduling Details

- `"execution"` is valid only on an `ai-client` profile, and its only value
  is `bridge`. Without it a profile runs directly.
- The daemon checks the schedule every minute. It does nothing while the
  `associate` route resolves to no profile or no source is enabled. The hourly
  emit runs at daemon start and then at most once an hour. The 05:00 run
  starts at the first check at or after 05:00 local each day, not at daemon
  start; each check during it emits the next request once the previous one
  is no longer active, until 10 were emitted, none is eligible, or a check
  fails.
- Each emit first records any auto-apply pause transition and runs the
  deterministic pass, as `llm associate` does, so `params_json` lists only
  units still uncategorized after deterministic rules:
  `{"unit_ids": ["<unit id>", ...]}`, at most 50, oldest first, counted after
  the source policy. `since` and `until` both hold the emit time.
- At claim the payload is rebuilt from the listed units that are still
  eligible and unlinked, under the current source policy and catalog. A unit
  sent under an opaque id is numbered by its position in `params_json`
  (`unit-<n>`), so the rebuilt payload maps the same ids at submit. When no
  listed unit remains, the claim completes the request with no links and does
  not return it.
- Only the routed client may claim an `llm_task`: the worker name must start
  with the profile's client and a hyphen (for example `claude-code-drain-…`).
  Any other claim returns nothing and leaves the request pending.
  `workgraph bridge drain` launches a client only when a request is
  claimable now, `pending` with `available_at` reached, and for an
  `llm_task` only when this client is the routed one. A drain session runs
  either capture or categorization; a categorization session uses the
  profile's model and config directory, not the worker's pinned model. When
  both kinds are claimable, drain alternates between them, starting with
  categorization, so capture work that stays claimable cannot starve it
  (`specs/bridged-capture.md`).
- A bridge claim carries `task`: the profile's `client` and `model` and the
  outbound-filtered `messages`. The hosted opt-in and managed-provider checks
  run before the task is returned; when they fail, the request returns to
  `pending` with that error.
- A submission is the model's response text, parsed and validated exactly as
  a direct response, against the payload rebuilt with the same rules. Output
  that is not JSON fails the submission and leaves the claim held; the worker
  then reports the failure with `capture_request_fail`.
- A direct run whose model call or parse fails returns the request to
  `pending` with the error and the outbox backoff. Stopping the daemon
  cancels an in-flight model call, HTTP or client process, and returns the
  request the same way. A client runs in its own process group, which is
  killed on cancel, and output waiting is bounded to 5 seconds.
- A completed task records a 24-hour cooldown for each sent unit that got no
  link, whether the model left it out or every entry for it was discarded.
  Scheduled emits, hourly and 05:00, and `llm associate` with a bridge
  profile skip a cooled-down unit until the cooldown ends or its permitted
  members change. A bridge claim records the member set it sent; at submit a
  unit whose members changed since the claim is not cooled down. A direct
  `llm associate` run ignores cooldowns.
- Failures, lease expiry, and drain timeouts of `llm_task` requests never
  write connector runtime state, and `llm_task` requests are never reported
  as bridge stalls: `llm.associate` is not a capture connector. A bridge
  request left pending by an absent worker stays visible in `capture requests
  --list` and coalesces later emits.
- With a bridge profile, `llm associate` performs one emit and prints `LLM
  associate request emitted` or `LLM associate request pending` with
  `Request: <id>`, or that no unit is eligible. `--dry-run` still prints the
  payload without emitting.

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

- `projects list` prints workstreams, their projects, and the containers
  mapped to each workstream, then projects with no workstream. It names how
  many project files have invalid frontmatter and points to `memory doctor`.
  From D2 each project shows its applied unit count for the last 7 days: its
  current applied links created, or approved, in that window.
- Listing forms of `projects review` (default, `--auto`, and `--json`) start
  with the auto-apply accuracy lines from the Accuracy Gate and, for each
  paused profile, `Auto-apply paused: <reason>`; `--json` carries them as
  `auto_apply`.
- Interactive `projects review` shows each proposed unit, oldest first:
  container, time range, kind, summary, proposed target, confidence, and
  evidence ids. Keys: `a` approve, `r` reassign (prompts for a slug), `n` new
  project (prompts for a slug and optional workstream), `x` no project, `s`
  skip, `q` quit. EOF or empty input skips, so with no input the command
  prints every item once and changes nothing, which is the default list.
- While a profile's auto-apply sample is `insufficient_data`, the default
  review also lists that profile's undecided auto-applied LLM links, oldest
  first, after the proposed ones (see Accuracy Gate). Deterministic links are
  not listed.
- After the last item, the default review prints `Nothing left to decide.`
  when every listed link was decided, or `<n> left to decide (workgraph
  projects review).` when some were skipped or the review was quit.
- `r`, `--project`, and `n`/`--new` take a catalog project slug; `--new`
  refuses a slug that already has project memory.
- Approving a proposed `new_project` link creates its project memory, as
  `--new` does, and applies the link with `target = project` and the new slug;
  `proposed_project_json` is kept.
- Link writes use immediate transactions with a busy timeout, so concurrent
  passes and decisions wait for each other instead of failing.
- `projects review --auto [--since <duration>]` lists auto-applied links,
  deterministic and LLM, from the last 24 hours by default, newest first, with
  link id, run id, rule or profile, target, and summary. The same keys apply,
  so a wrong link is fixed with one key: `r` reassign, `x` no project.
- Approving an applied auto link records `decision = confirmed`. Approving a
  proposed link applies it with `source` unchanged, `auto = false`, and
  `decision = approved`.
- `--reject` and `x` assign no project: the link is corrected to a user link
  with `target = none`. `--reject <link-id>` and `--assign <unit-id>` accept
  applied links as well as proposed ones.
- `--new` creates project memory with the existing `memory init` starter,
  adds the workstream frontmatter when given, and applies a user link.
- `projects explain` prints the unit's members, every link row with its
  source, rule or run, and decision, and for the current link the exact
  `projects review --assign` and `--reject` commands that change it and, when
  the link is undoable, the `projects undo` command. User and user-approved
  links other than confirmed auto links are not undoable. Printed commands
  carry the `--home`, `--database`, and `--memory` options the command was
  given, shell-quoted, because undo leads to a review that runs a linking
  pass against that catalog.
- Flag forms make the same changes as the keys and never prompt.

## Daily Loop Surfaces

These surfaces only read links; none runs a linking pass or writes a link.

- **`next`** gains a `categorize` group after `ready`, with one item per link
  that the default `projects review` lists. Text output summarizes the group in
  one line `Project links to review: <n> (<p> project, <q> no project)
  (workgraph projects review), <m> auto-applied today (workgraph projects
  review --auto)`, then `Auto-apply paused: <reason>` when paused. `<p>`
  counts links targeting an existing or new project and `<q>` links targeting
  no project. The items themselves are listed only with `--reason
  categorize`, oldest first, as `<unit-ref> in <container>:
  <proposed|auto-applied> <target> (confidence <c>)`, where `<unit-ref>` is
  the item's path-free unit reference (`specs/next.md`). `<m>` counts undoable
  auto-applied links created since local midnight. The group is shown while a link awaits a decision or
  auto-apply is paused. JSON items use reason `categorize`, id
  `projects:categorize:<link-id>`, and evidence from the link. The item
  closes when the link is decided. See `specs/next.md`.
- **`today`** groups activity by workstream, then project, using applied
  links; unlinked activity falls back to its container. See
  `specs/today.md`.
- **`resume`** uses the same grouping. The morning overview shows activity
  since the last check-in per project, including PR units opened by agents
  overnight, and the `Project links to review` line. See `specs/resume.md`.
- **`resume <project>`** selects the same activity the overview places under
  that project, and `resume <workstream>` shows its projects' activity grouped
  by project. Two exceptions to that parity are deliberate: a container whose
  name is exactly the project is always included, and a container mapped to
  several projects appears in each of their drill-downs, while the overview
  places a container under a project only when it maps to exactly one. See
  `specs/resume.md`, Project Resume.

Grouping, shared by `today` and `resume`:

- An event in a unit with an applied `project` link belongs to that project,
  under the project's workstream when its memory declares one.
- An event with no applied link belongs to the project its container maps to
  when the container maps to exactly one catalog project. An applied `none`
  link keeps the event out of every project.
- Any other event stays under its container, nested under a workstream when
  the container maps to exactly one workstream. A container mapped to two or
  more catalog projects appends `; mapped to <slug>, <slug>` to its line, so
  it does not read as unmapped.
- Lines are `- <workstream> workstream: <counts>`, `- <slug> project:
  <counts>[; <kinds>][; <k> auto-applied (workgraph projects review
  --auto)]`, and `- <container>: <counts>`, with projects and containers in a
  workstream indented beneath it. `<kinds>` are the distinct link kinds, such
  as `review, coordination`, of the project's units in view, and `<k>` counts
  those units whose applied link is auto-applied. Entries are ordered by
  count, most first, then name.

## Slices

- **D1 Model and container wording**: catalog frontmatter, `projects list`,
  workstream mappings, doctor workstream suggestions, and "container" wording
  in memory and resume output.
- **D2 Units and deterministic linking**: unit grouping, `project_links`,
  deterministic rules, `projects explain`, `undo`, flag-form and interactive
  review, and applied unit counts in `projects list`.
- **D3 LLM categorization**: Slack conversation type capture, `llm
  associate` with dry run, source opt-in, validation, auto-apply, accuracy
  gate, and review metrics. D3 runs only directly or manually with the
  routed profile; scheduled outbox and bridge execution are D4.
- **D4 Scheduling and daily loop**: kind-aware `llm.associate` outbox
  requests, direct and bridge execution, and the `next`, `today`, and
  `resume` surfaces. Existing capture-only workers are unchanged. D4 depends
  on in-flight changes to those commands.
- **D5 Thresholds and verifier**: per-profile auto-apply thresholds and verify
  floors with `llm set`, the per-profile accuracy gate, and the
  `associate-verify` route, direct and bridge.
- **D6 Backlog verification and distinct decisions**: `llm verify-backlog`
  with dry run, limit, since, and resumability; counted, distinct
  `recent_decisions`. Bridge verifiers for the backlog are deferred.

## Deferred

- linking one unit to several projects
- absorbing git commits into PR units
- file, mail, and Azure Boards units
- LLM-chosen unit boundaries
- workstream memory files and workstream-level descriptions
- learned per-container priors beyond recent user decisions
- backlog verification through a bridge verifier
- scheduled backlog verification by the daemon

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
  mapped to another, and the link is applied immediately with `auto = true`
  and a run id; an identifier in two projects produces no link; an identifier
  seen only in an auto-applied or proposed link seeds nothing.
- **P4. Workstream mappings narrow the catalog.** A container mapped to `harbor`
  sends only `harbor` projects in the dry-run payload and creates no link.
- **P4a. Several-project mappings narrow the catalog.** A container mapped to
  `data-apis` and `harbor-terms-and-phrases` sends only those projects in the
  dry-run payload and creates no link; adding a workstream mapping sends the
  union of both; `projects list` shows the container under each project,
  `today` marks its line with both projects, and `memory doctor` does not
  offer it to another project; a unit whose identifiers name a project
  outside the mapped or workstream set still has that project in its catalog,
  unless the only seed is a link from a disabled source.
- **P5. Dry run sends nothing and respects opt-in.** With only `slack`
  enabled, the payload contains channel units but no DM, unknown-type Slack,
  or GitHub units, and no decision context from those sources; the fake
  provider receives no request.
- **P6. Invalid results are discarded.** Unknown units, unknown projects,
  foreign evidence ids, bad kinds, and out-of-range confidence are discarded
  and counted; valid entries in the same response are stored.
- **P7. Auto-apply gates hold.** With the default threshold, confidence 0.95
  applies, 0.94 proposes, a new
  project proposes, a recent rejection for the pair proposes, and measured
  accuracy below 0.95 over 20 decided links pauses auto-apply for that
  profile and says so;
  the pause survives the sample dropping below 20; with fewer than 20
  decided links, auto-apply stays on and undecided LLM auto links, but not
  deterministic links, are listed in the default review and the `next` count;
  the interactive review ends with a summary line of what is left to decide.
- **P8. Review and undo record decisions.** Approve, reassign, reject, new
  project, and undo record the documented state and decision without editing
  events; `review --auto` lists a deterministic and an LLM auto link with
  their run ids, `r` corrects one, and `undo --run` undoes a whole run;
  `explain` prints the commands that change the current link; undo leaves a
  proposed replacement that no later deterministic or
  LLM run reapplies; user-approved links survive later runs.
- **P9. Scheduled requests use the outbox.** The daemon emits one
  `llm.associate` request when eligible units exist and none when they do
  not; a claim without a kind never returns it to a capture worker; a
  bridge-executed request completes only with a valid claim token; a
  direct-path request run by the daemon writes links; a source disabled
  between emit and claim is left out of the claimed payload.
- **P10. Daily surfaces group by project.** `next` shows the categorize line
  with three awaiting units and the auto-applied count, JSON items carry the
  link id and evidence, and approving, reassigning, and rejecting closes them
  without editing events. `today` groups by workstream, then project, then
  container, listing link kinds and the auto-applied count with the
  `projects review --auto` hint. `resume` groups activity since the check-in
  the same way, lists an agent-opened PR under its project, and counts the
  remaining decisions. `resume <project>` lists linked and mapped activity
  without units linked to `none` or another project, and `resume <workstream>`
  groups its projects' activity by project.
- **P11. Thresholds are per profile.** `llm add --auto-apply-confidence 0.9`
  and `llm set` store the threshold and verify floor, `llm set` keeps every
  other setting, values outside their ranges are refused, and `llm list`
  prints both; a 0.92 result applies for a 0.9 profile and proposes for a
  default profile.
- **P12. The accuracy gate is per profile.** A profile at threshold 0.9 with
  18 of 20 decided links confirmed keeps auto-applying, while a default
  profile with the same sample pauses; the pause names its profile, appears in
  `projects review` and `workgraph review`, and does not stop the other
  profile; a pause event without a profile still holds a profile with no
  event of its own.
- **P13. A direct verifier decides uncertain units.** With `associate-verify`
  routed, only units in the floor-to-threshold band reach the verifier, with
  the same unit payload plus `primary_answer` and nothing from other units;
  the verifier's result is stored under its profile and auto-applies at its
  threshold, both results appear in `projects explain`, a unit the verifier
  leaves out keeps the associate result, and a verifier call failure keeps
  every associate result and says so. Without the route no verifier is called.
- **P14. A bridge verifier uses the outbox.** A bridge verifier gets an
  `associate-verify` `llm_task` request in the transaction that completes the
  associate request; no associate request is emitted while it is active; only
  the verifier's client can claim it and the claim carries the verifier's
  model; its submission writes the verifier's links and completes it.
- **P15. A system-one associate profile works with a verifier.** Floor-to-
  threshold units escalate to a direct verifier with the hosted-filtered
  state, and the verifier's result is stored under its profile; a bridge
  verifier gets an `associate-verify` request from direct and daemon runs;
  discarded units are not escalated; a `system-one` verifier is refused at
  `llm use` and, when hand-routed, at run time with the associate results
  kept; `llm set` thresholds change what a `system-one` profile auto-applies.
- **P16. Waiting proposals are verified once.** `llm verify-backlog` sends
  only undecided proposed LLM links in their profile's floor-to-threshold
  band, never a user-decided unit; the verdict replaces the link under the
  verifier profile, applies only through the verifier's threshold and
  accuracy gate, appears in `projects explain` and in `projects review
  --auto --since 1h` when applied now, and is undone by `projects undo
  --run`; new-project proposals stay in review; a unit with a member from a
  source disabled since the proposal is skipped; no stored summary or
  new-project text, nothing from other units in the original batch, and no
  earlier snapshot's text or catalog description is sent, and the rebuilt
  request carries current redactions; `--dry-run` prints the count,
  destination, and an example request and calls nothing; it refuses without
  a verifier route and for a `system-one` or bridge verifier; a second run
  does not resend a link the verifier answered or left out, while a failed
  call leaves links waiting.
- **P17. Recent decisions are distinct.** Fifteen identical decisions become
  one entry with `count` 15 beside each distinct decision; a decision from a
  disabled source adds neither an entry nor a count, and the hosted outbound
  filter still redacts decision containers; a rejected new-project slug and a
  decision on a unit whose member is now from a disabled source are left out
  of every associate and verify payload; a snapshot stored without counts
  sends its repeated decision once to a deferred verifier, capped at the
  count still permitted.
