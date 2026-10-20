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
modification or deletion in a specific project, or a git commit whose author
email matches a configured local git identity. File creation alone does not
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

Proposed in `specs/next.md` and `specs/github-pr-watch.md`, not yet
implemented: the default overview shows a `Waiting on you` section after
`Where you left off` with at most five `workgraph next` items, and
`resume <project>` shows that project's items after `Recent activity`.
`github.pull_request.*` transition events qualify for `Relevant updates since
last check-in` only when `triage.needs_user` is true. Personal work evidence
for the handoff is unchanged.

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
each captured project instead of the normal project list. Each line says whether
the project would be shown or hidden, includes the reason, and preserves event
count plus last-active timestamp. This is intended for local debugging of
unexpected resume results, not as an admin audit surface.

## Project Resume

When a project is provided, `workgraph resume <project>` shows recent evidence for that exact project.

Project-specific resume is exact. It does not apply the project-list relevance
gate because the user has already named the project they want to inspect.

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
