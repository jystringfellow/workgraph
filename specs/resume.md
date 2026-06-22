# Resume Command

`workgraph resume` helps a user pick work back up from stored events.

The command is deterministic and local-first:

- reads from the SQLite event store
- uses captured event project names and local relevance evidence as resumable
  projects
- renders plain text without an LLM
- avoids unsupported claims or invented next steps

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

Examples of user-work evidence include:

- file events under the project
- git commits authored by a configured local git identity
- GitHub pull requests or issues authored by a configured local GitHub identity
- Slack conversations that have not yet been associated with another project
- explicit project memory

Broad watch-root projects such as `Downloads`, `Code`, `Desktop`, and
`Documents` should not appear in the default project list from file churn alone.

Older Slack events may have stored raw channel ids as their project before
conversation-name resolution was available. At read time, resume should merge
those older events under a resolved `channel_name` when another stored event has
the same Slack `channel_id` and a human-readable name. The raw `channel_id`
should remain in event payloads for traceability.

`workgraph resume --all` preserves the older broad behavior and lists every
project with captured events. This is useful for debugging, audits, and checking
project names before exact `workgraph resume <project>` calls.

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
