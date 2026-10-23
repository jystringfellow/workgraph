# Start Command

`workgraph start` starts local event capture in the background and returns after
capture is ready.

`workgraph status` reports background capture state, and `workgraph stop` stops
background capture explicitly.

The daemon records the build identity and executable modification metadata it
started with. While capture is running, `workgraph status` reports both the
running build and the build currently available at that executable path. If the
file was replaced after the daemon started, or if an older daemon predates this
metadata, status emits a prominent stale-binary warning with
`workgraph stop && workgraph start` recovery guidance. A live PID alone is not
evidence that the running daemon picked up an executable upgrade.

Capture control is responsible for:

- refusing to start before `workgraph init`
- watching configured local folders
- recording file create, modify, and delete events
- reporting capture state as plain text
- ignoring configured paths and names
- ignoring editor safe-save scratch files
- coalescing editor atomic-save replacement sequences into file modification events
- skipping inaccessible subtrees under watched roots without aborting capture
- skipping unsupported special files such as sockets without aborting capture
- skipping generated index/build cache subtrees that would exhaust watcher resources
- skipping top-level hidden directories under broad roots unless explicitly configured
- traversing init-owned default roots conservatively
- bounding recursive watch registration so capture does not exhaust process file descriptors
- registering configured watch roots before recursively descending into any one root
- preserving events already written when capture stops

Background start output reports the configured paths and returns:

```text
workgraph capture started
PID: 12345
Home: /path/to/.workgraph
Database: /path/to/.workgraph/workgraph.db
Watching: 1 configured directory
Monitoring: git, github, slack, notion
```

Path configuration starts with `~/.workgraph/settings.json`. File watching is
opt-in: `workgraph init` configures no watch roots, only ignore rules for
workgraph internal storage and repository scan roots for git capture (see
`specs/config.md`). Users choose directories to watch with
`workgraph settings add-watch [path]`.

When no watch root is configured, start output says file watching is off and
how to turn it on instead of reporting a watch count or limit:

```text
File watching: off
Watch a directory with workgraph settings add-watch <path>.
```

Connectors such as git, GitHub, Slack, and calendar keep capturing while file
watching is off.

CLI flags can choose watch roots for a single start:

```text
workgraph start --home ~/.workgraph --database ~/.workgraph/workgraph.db --watch .
```

CLI flags can also opt into read-only Slack collection for explicit channels:

```text
WORKGRAPH_SLACK_TOKEN=xoxb-... workgraph start --slack-channel C123
```

Slack collection only reads the configured channel ids, stores message and
thread-reply events, and does not post, react, or otherwise act in Slack.

For debugging, `workgraph start --foreground` keeps capture attached to the
current terminal and prints one line per captured file event:

```text
workgraph capture is running
Home: /path/to/.workgraph
Database: /path/to/.workgraph/workgraph.db
Watching: 1 configured directory
Monitoring: git, github, slack, notion
file.created /path/to/project/notes.md
file.modified /path/to/project/notes.md
file.deleted /path/to/project/notes.md
```

If recursive watch setup reaches its resource budget, capture keeps the watchers
that were already registered and reports that the watch limit was reached.
Resource limits must not crash foreground signal handling.

When the watch limit is reached, start output should stay compact. It should
include the count of registered directories, the last directory successfully
watched, the next directory that could not be watched, and guidance to add
priority directories with `workgraph settings add-watch`. The full registered
directory list should remain available in runtime status for debugging and
future tooling.

When a watched root contains both user-facing folders and hidden/cache folders,
recursive setup should prioritize user-facing folders such as Desktop,
Documents, Downloads, and visible project folders before hidden or cache-heavy
subtrees.

Top-level hidden directories under a broad watched root should not be traversed
implicitly. If a hidden directory is explicitly listed in `watch_dirs`, that
explicit watch root overrides the hidden-directory skip.

Capture configured programmatically with conservative roots registers each
conservative root and its immediate child directories, but only recurses below
an immediate child when that child looks like active work: it contains ordinary
files, project marker files, or is a common work container. Settings-file
entries listed in `conservative_watch_dirs` are automatic roots from older
versions and are not watched at all (see `specs/config.md`).

Path configuration uses this precedence:

```text
CLI flags > settings file > defaults
```

See `specs/config.md` for the config contract.

Background capture uses local state such as a PID file and log file under the
workgraph home. It does not require a cloud service or privileged install.

See `specs/capture-controls.md` for the background capture command contract.
