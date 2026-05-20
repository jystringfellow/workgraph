# Run Command

`workgraph run` starts local event capture in the background and returns after
capture is ready.

`workgraph status` reports background capture state, and `workgraph stop` stops
background capture explicitly.

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

Background run output reports the configured paths and returns:

```text
WorkGraph capture started
PID: 12345
Home: /path/to/.workgraph
Database: /path/to/.workgraph/workgraph.db
Watching: /path/to/project
```

Path configuration starts with `~/.workgraph/config.json`. By default,
`workgraph init` configures capture to watch existing common user-facing folders
and ignore WorkGraph internal storage. Users can add more roots with
`workgraph config add-watch [path]`.

CLI flags can choose watch roots for a single run:

```text
workgraph run --home ~/.workgraph --database ~/.workgraph/workgraph.db --watch .
```

For debugging, `workgraph run --foreground` keeps capture attached to the
current terminal and prints one line per captured file event:

```text
WorkGraph capture is running
Home: /path/to/.workgraph
Database: /path/to/.workgraph/workgraph.db
Watching: /path/to/project
file.created /path/to/project/notes.md
file.modified /path/to/project/notes.md
file.deleted /path/to/project/notes.md
```

If recursive watch setup reaches its resource budget, capture keeps the watchers
that were already registered and reports that the watch limit was reached.
Resource limits must not crash foreground signal handling.

When the watch limit is reached, run output should include a small sample of
registered directories and the first directory that could not be watched. The
full registered directory list should remain available in runtime status for
debugging and future tooling.

When a watched root contains both user-facing folders and hidden/cache folders,
recursive setup should prioritize user-facing folders such as Desktop,
Documents, Downloads, and visible project folders before hidden or cache-heavy
subtrees.

Top-level hidden directories under a broad watched root should not be traversed
implicitly. If a hidden directory is explicitly listed in `watch_dirs`, that
explicit watch root overrides the hidden-directory skip.

Roots listed in `conservative_watch_dirs` are broad default roots. Capture
should register the root and immediate child directories, but it should only
recurse below an immediate child when that child looks like active work: it
contains ordinary files, project marker files, or is a common work container.
Folder-only app libraries and other deep containers should not consume the
default watch budget unless the user explicitly adds them as watch roots.

Path configuration uses this precedence:

```text
CLI flags > config file > defaults
```

See `specs/config.md` for the config contract.

Background capture uses local state such as a PID file and log file under the
WorkGraph home. It does not require a cloud service or privileged install.

See `specs/capture-controls.md` for the background capture command contract.
