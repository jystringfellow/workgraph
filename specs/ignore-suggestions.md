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

## On-Demand Deterministic Scan

`workgraph suggestions list` only renders suggestions already stored in SQLite;
it does not initiate analysis. Users can explicitly run the deterministic
ignore producer with:

```text
workgraph suggestions scan
workgraph suggestions scan --type ignore
```

Omitting `--type` runs every supported deterministic scanner. V1 supports only
`ignore`; unknown types fail without storing suggestions. The scan accepts a
positive `--limit` for the maximum number of candidates to store and render,
defaulting to 20 and capped at 100.

The ignore scan combines two local evidence sources:

- directory pressure under configured watch roots
- file events already stored in the local database during the preceding 24
  hours

Filesystem scanning:

- examines directory names and structure, never file contents
- does not follow symbolic links
- respects configured ignore paths, ignore names, the workgraph home, and the
  database path
- applies the same conservative top-level traversal rule as capture
- inspects at most 10,000 directories across all roots and reports when that
  bound truncates the scan
- chooses the highest generated-looking directory in a nested candidate tree so
  one generated subtree does not produce many overlapping path suggestions

A directory is generated-looking when its lowercase basename contains `cache`,
`generated`, `tmp`, `temp`, `derived`, `.noindex`, or `userdata`; exactly matches
`build`, `dist`, `out`, `target`, `release`, or `releases`; or ends in
`.xcarchive`. Existing ignore rules still take precedence over this candidate
classification.

The scanner proposes an `ignore_path` when a generated-looking subtree contains
at least 16 directories or has at least 8 captured file events in the lookback
window. It proposes an `ignore_name` when at least 8 captured file events occur
under the same generated-looking basename across at least 3 distinct candidate
paths. Counts are threshold rules, not probabilistic scores.

Every stored scan suggestion uses the deterministic baseline lane, includes the
matched signals, bounded sample paths and event ids, and coalesces through the
existing stable `(type, pattern_key)` identity. Active suppressions and existing
ignore rules remain effective.

The scan may append or refresh suggestion records, but it must not update
settings, start or stop capture, read file contents, or invoke an LLM. Its output
reports roots inspected, directory count, truncation, and the suggestions it
recorded. Applying a proposal still requires `workgraph suggestions approve`.
