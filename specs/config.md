# Configuration

WorkGraph keeps local configuration in the WorkGraph home:

```text
~/.workgraph/config.json
```

The config file answers what local paths WorkGraph watches and what paths it must never record.

## Defaults

`workgraph init` creates `config.json` when it does not already exist.

By default, WorkGraph watches existing common user-facing folders under the current user's home directory instead of recursively watching the entire home directory. Paths are resolved with Go's `os.UserHomeDir()` and persisted as absolute paths, not as shell tokens such as `$HOME`.

Default candidates include:

- Desktop
- Documents
- Downloads
- Code
- Projects
- Developer
- Work
- source
- repos

Only candidates that already exist are included. If none exist, WorkGraph falls back to the user home directory so the config is still usable.

Example on macOS:

```json
{
  "watch_dirs": ["/Users/craig/Desktop", "/Users/craig/Documents", "/Users/craig/Downloads", "/Users/craig/Code"],
  "conservative_watch_dirs": ["/Users/craig/Desktop", "/Users/craig/Documents", "/Users/craig/Downloads", "/Users/craig/Code"],
  "ignore_paths": ["/Users/craig/.workgraph"],
  "ignore_names": [".git", "node_modules", "DerivedData", ".noindex"]
}
```

Example on Windows:

```json
{
  "watch_dirs": ["C:\\Users\\Craig\\Desktop", "C:\\Users\\Craig\\Documents", "C:\\Users\\Craig\\Downloads", "C:\\Users\\Craig\\repos"],
  "conservative_watch_dirs": ["C:\\Users\\Craig\\Desktop", "C:\\Users\\Craig\\Documents", "C:\\Users\\Craig\\Downloads", "C:\\Users\\Craig\\repos"],
  "ignore_paths": ["C:\\Users\\Craig\\.workgraph"],
  "ignore_names": [".git", "node_modules", "DerivedData", ".noindex"]
}
```

The default must always ignore the WorkGraph home directory so database writes, logs, PID files, and config updates do not recursively become user work events.

## Fields

### watch_dirs

`watch_dirs` is a list of absolute directory paths. Foreground and background capture use these directories when no `--watch` flag is provided.

### conservative_watch_dirs

`conservative_watch_dirs` is a list of `watch_dirs` roots created by `workgraph
init` that should be traversed cautiously. Capture registers each conservative
root and its immediate children, but only recurses deeper into children that
look like active work directories. This keeps broad default folders such as
Documents from spending the watch budget on app libraries or nested folder-only
containers.

Explicit watch roots added by users are not added to `conservative_watch_dirs`.
If a user adds a directory with `workgraph config add-watch`, WorkGraph treats
that path as intentional and may recurse normally, subject to ignore rules and
the watch budget.

Users can add a watch root with:

```text
workgraph config add-watch [path]
```

If `path` is omitted, the command adds the current working directory. Added
paths are resolved to absolute paths and placed before existing watch roots so a
newly added project is not starved by a broad home-directory watch budget.
Adding a path that is already watched is idempotent.

### ignore_paths

`ignore_paths` is a list of absolute paths. A captured source path is ignored when it is the same as an ignored path or is a descendant of an ignored path.

This field is for user-owned directories or files that should never be tracked, such as caches, private folders, generated output, or the WorkGraph home.

### ignore_names

`ignore_names` is a list of file or directory basenames. A captured source path is ignored when any path segment matches one of these names.

This field is for high-noise project internals and generated content that commonly appear under watched roots, such as `.git`, `node_modules`, Xcode `DerivedData`, and Apple `.noindex` directories.

## Precedence

Capture path configuration uses this order:

```text
CLI flags > config file > defaults
```

If one or more `--watch` flags are provided, capture uses those watch roots for that run. Ignored paths and ignored names still apply.

## Portability

Configuration should store normalized absolute paths using Go's `filepath` behavior for the current operating system. Friendly tokens such as `~` may be accepted later, but they are not required for Phase 0.
