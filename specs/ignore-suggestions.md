# Ignore Suggestions

workgraph should use captured event volume to suggest ignore rules for noisy
local paths.

When file capture observes more activity under a directory than seems plausibly
human-authored, workgraph should create a pending ignore suggestion instead of
modifying config silently.

Examples:

- generated build output producing many file events
- tool caches rewriting files repeatedly
- app-local user state such as Xcode `xcuserdata`

Suggestion behavior:

- group noisy activity by a meaningful parent directory or basename
- record the source events and time window that caused the suggestion
- explain why the path or name was suggested
- prefer a basename suggestion for repeated generated names such as `bin`
- prefer a path suggestion for one-off local noise
- do not add to `ignore_paths` or `ignore_names` until the user approves
- approving a path suggestion appends to `ignore_paths`
- approving a basename suggestion appends to `ignore_names`
- duplicate suggestions for the same path or name should be coalesced

This is the opposite of watch-root suggestions:

```text
untracked meaningful activity -> suggest watch root
tracked noisy activity -> suggest ignore rule
```

This preserves the workgraph rule: suggest -> draft -> approve -> act.
