# Resume Command

`workgraph resume` helps a user pick work back up from stored events.

The command is deterministic and local-first:

- reads from the SQLite event store
- uses captured event project names as resumable projects
- renders plain text without an LLM
- avoids unsupported claims or invented next steps

## Project List

When no project is provided, `workgraph resume` lists known projects ordered by most recent captured activity.

The output includes:

- `Resumable projects`
- each project name
- the last active timestamp
- the number of captured events for that project
- a hint to run `workgraph resume <project>`

Events without a project are omitted from the project list.

## Project Resume

When a project is provided, `workgraph resume <project>` shows recent evidence for that exact project.

The output includes:

- `Resume <project>`
- `Recent activity`
- `Relevant files` when file paths are known
- `Open GitHub work` when captured GitHub pull requests or issues for the project are known open

Recent activity is ordered newest first. File paths come from captured event payloads.

Known transient local file paths should not appear in project resume output or relevant files. Resume filters those paths before applying the recent activity cap so durable project evidence is not crowded out by temporary file churn such as macOS `.dat.nosync` files.

Project resume output shows at most 10 recent activity events by default. When older matching events exist, the output reports how many were omitted.

Open GitHub work comes from stored GitHub event payloads and is not limited by the recent activity cap. Closed or merged GitHub work is not shown in that section.

When no events exist for the requested project, the output says no recent activity was found and suggests checking the project name or running capture.
