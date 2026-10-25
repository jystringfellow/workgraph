# Resume Command

`workgraph resume` helps a user pick work back up from stored events.

The command is deterministic and local-first:

- reads from the SQLite event store
- uses captured event project names and local relevance evidence as resumable
  projects
- renders plain text without an LLM
- avoids unsupported claims or invented next steps

## Morning Handoff

The default `workgraph resume` overview starts with `Where you left off` before
the existing resumable project list. It uses the latest captured personal work
session, looking back seven days from the current time. If the latest known
personal work is older, it still shows that session and labels it older than a
week rather than claiming there is no context. A session contains consecutive
events from the same project no more than 30 minutes apart. It shows at most
three events, newest first, with their recorded time and labels, and a command
to inspect the project. If no supported personal work evidence exists, it says
so without guessing what the user worked on.

Personal work evidence for this first handoff is a non-transient file
modification or deletion in a specific project, or a git commit authored by the
local git identity described in `specs/git.md`. When an identity is known,
commits without an author email do not qualify. When no identity can be found
at all, commits qualify and the overview says the git identity is unknown
instead of claiming there is no personal work. File creation alone does not
qualify because a new clone can create an entire tree without personal work.
Broad folder and home-folder projects do not qualify.
GitHub issue and pull request snapshots do not qualify, even when the connected
user authored the item, because their stored timestamp is the item's latest
update by anyone. Other captured events remain available in project resume;
the handoff does not silently infer personal action from them.

Plain `workgraph resume` records a local check-in only after successfully
rendering its overview. Its next run shows the previous check-in time and
`Personal work since last check-in`, including supported personal events whose
event timestamp or first capture time is later than that check-in. The section
reports the count and at most five newest event lines, or says no new personal
work was captured. A separate `Relevant updates since last check-in` section
shows up to five other events with non-empty explicit involvement metadata,
such as a mention or review request, under the same timestamp or first-capture
rule. Unclassified and explicitly ambient events are omitted from this section;
GitHub work snapshots without direct involvement do not re-enter the handoff.
These sections report evidence from the local event store, not a claim that all
remote changes have been observed. The check-in advances even when no new work
is found, so a second run does not repeat the same interval.

The check-in is stored per workgraph database in SQLite. It is absent on first
use and created lazily for databases initialized before this feature.
Older databases without `events.involvement_json` remain readable; their events
are treated as unclassified for `Relevant updates since last check-in` until
the normal schema migration adds that column.
`workgraph resume <project>`, `workgraph resume --all`, and
`workgraph resume --debug-relevance` inspect context without advancing the
default overview's check-in. Calling the Go `Resume` query API also does not
advance it; the CLI records the check-in after successful output. A failed
render, query, or output does not advance the check-in.

With `workgraph next` (`specs/next.md`) and the GitHub pull request watch
(`specs/github-pr-watch.md`): the default overview shows a `Waiting on you` section after
`Where you left off` with at most five `workgraph next` pull requests in
`next` section order, stale pull requests last, and
`resume <project>` shows that project's items after `Recent activity`.
A `github.pull_request.*` event qualifies for `Relevant updates since last
check-in` only when its type is one of the needs-user transition types listed
in `specs/github-pr-watch.md` and its `triage.needs_user` is true;
`github.pull_request.observed` never qualifies. The existing timestamp or
first-capture rule still applies. Personal work evidence for the handoff is
unchanged.

After the check-in sections, the default overview shows `Activity since last
check-in by project`, grouped by workstream, then declared project, falling
back to the container for unlinked activity, as described in
`specs/projects.md` (Daily Loop Surfaces). Its activity is every event that
qualifies for either check-in section plus every event in a unit with an
applied project link, under the same timestamp or first-capture rule. It also
lists pull requests opened by agents since the check-in: watched pull
requests (`specs/github-pr-watch.md`) whose author kind is `bot` and whose
`created_at` is after the check-in, each as `agent PR <owner/repo>#<number>
<title>` with the bot login, and `on your behalf` when the user is watched as
the author. A pull request is placed by the link on its `github.pr` unit, or
by its repository name as the container. With nothing to show, the section
says `No new activity captured.`

When categorization decisions remain, the default overview then shows
`Project links to review: <n> (<p> project, <q> no project) (workgraph
projects review)`, where `<n>` is the number of `workgraph next` `categorize`
items, `<p>` those targeting an existing or new project, and `<q>` those
targeting no project. This line does not need
a previous check-in.

## Project List

When no project is provided, `workgraph resume` lists projects with user-work
evidence ordered by most recent captured activity. This avoids treating every
cloned repository or weakly inferred GitHub repository as resumable work.

The output includes:

- `Resumable projects`
- each project name
- the last active timestamp
- the number of captured events for that project
- a hint to run `workgraph resume <project>`

Events without a project are omitted from the project list.

The default list shows at most ten projects with user-work evidence in the last
14 days, most recent first. When `workgraph resume --all` would show more
projects, the list ends with `<n> more: workgraph resume --all`, where `<n>` is
the number of additional projects that list would show.

By default, weak evidence is omitted from the project list. Examples of weak
evidence include git commits authored by someone else in a cloned repository and
repository activity inferred only because a local clone exists.

By default, stale evidence older than the resume freshness window is also
omitted from the project list. `workgraph resume --all` still shows older
projects.

By default, raw Slack channel ids are omitted from the project list when no
resolved human-readable conversation name is known. Exact
`workgraph resume <raw-slack-id>` calls should still work for investigation or
recovery.

Examples of user-work evidence include:

- file events under the project
- git commits authored by a configured local git identity
- GitHub pull requests or issues authored by a configured local GitHub identity
- Slack conversations that have not yet been associated with another project
- explicit project memory

Broad watch-root projects such as `Downloads`, `Code`, `Desktop`, and
`Documents` should not appear in the default project list from file churn alone.
The current user's home-folder basename should be treated the same way, because
home-folder file churn is usually too broad to represent resumable project work.

Older Slack events may have stored raw channel ids as their project before
conversation-name resolution was available. At read time, resume should merge
those older events under a resolved `channel_name` when another stored event has
the same Slack `channel_id` and a human-readable name. The raw `channel_id`
should remain in event payloads for traceability.

`workgraph resume --all` preserves the older broad behavior and lists every
project with captured events. This is useful for debugging, audits, and checking
project names before exact `workgraph resume <project>` calls.

`workgraph resume --debug-relevance` renders the default relevance decision for
each container instead of the normal project list. Each line says whether
the project would be shown or hidden, includes the reason, and preserves event
count plus last-active timestamp. Visibility matches the default list:
projects with user-work evidence that the 14-day window or ten-project cap
leaves out are reported hidden with that reason. This is intended for local debugging of
unexpected resume results, not as an admin audit surface.

## Project Resume

When a project is provided, `workgraph resume <project>` shows recent evidence for that exact project.

Project-specific resume is exact. It does not apply the project-list relevance
gate because the user has already named the project they want to inspect.

It selects activity the way the morning overview groups it
(`specs/projects.md`, Daily Loop Surfaces):

- events in the container whose name is exactly `<project>`, as before
- events in units with an applied link to the project
- events with no applied link in containers mapped to the project with
  `memory link --source`
- never an event whose unit has an applied link to `none` or to another
  project, unless its container name is exactly `<project>`

When `<project>` names a workstream and no project memory has that slug, the
output starts with `Resume <workstream> workstream` and `Projects: <slugs>`,
selects the activity of every project in the workstream the same way,
including containers named exactly like a member project but not a container
named like the workstream, and
groups `Recent activity` by project, most recently active first, as `- <slug>
project: <n> events` with the event lines beneath. A workstream has no memory
file, so no project memory is shown or suggested.

The output includes:

- `Resume <project>`
- `Recent activity`
- `Relevant files` when file paths are known
- `Open GitHub work` when captured GitHub pull requests or issues for the project are known open
- `Project memory` when matching project Markdown memory exists

Recent activity is ordered newest first. File paths come from captured event payloads.

Known transient local file paths should not appear in project resume output or relevant files. Resume filters those paths before applying the recent activity cap so durable project evidence is not crowded out by temporary file churn such as macOS `.dat.nosync` files.

Project resume output shows at most 10 recent activity events by default. When older matching events exist, the output reports how many were omitted.

Open GitHub work comes from stored GitHub event payloads and is not limited by the recent activity cap. Closed or merged GitHub work is not shown in that section.

Project memory comes from matching Markdown under
`workgraph-memory/projects/<project-slug>.md`, where the slug is derived from
the requested project name with the same lowercase kebab-case rule used by
`workgraph memory init`. When matching project activity exists but project
memory does not, resume points to that path so the user can keep explicit
context such as priorities, decisions, constraints, and open questions.

When no events exist for the requested project, the output says no recent
activity was found and suggests checking the project name or running capture. If
matching project memory exists, the output includes it even though captured
events remain absent.
