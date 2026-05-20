# Watch Suggestions

WorkGraph should use higher-signal activity sources to suggest local watch roots.

When another capture mechanism observes work in a local directory that is not
already watched, WorkGraph should create a pending suggestion instead of
modifying config silently.

Examples:

- git activity in an unwatched repository
- GitHub activity linked to a local checkout
- editor or tool activity reported by a future integration

Suggestion behavior:

- record the candidate directory as an absolute path
- record the source that observed the activity
- explain why the directory was suggested
- do not add the directory to `watch_dirs` until the user approves
- approving the suggestion uses the same behavior as `workgraph config add-watch`
- duplicate suggestions for the same directory should be coalesced

This preserves the WorkGraph rule: suggest -> draft -> approve -> act.
