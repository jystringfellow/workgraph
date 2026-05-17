# Run Command

`workgraph run` starts local event capture.

The first implementation runs in the foreground. This keeps capture behavior
observable while the file watcher, event store, and graceful shutdown contracts
stabilize.

Foreground run is responsible for:

- refusing to start before `workgraph init`
- watching configured project folders
- recording file create, modify, and delete events
- reporting captured file events as plain text while it runs
- ignoring WorkGraph internal storage
- preserving events already written when the process is interrupted

Foreground output starts with the configured paths and then prints one line per
captured file event:

```text
WorkGraph capture is running
Home: /path/to/.workgraph
Database: /path/to/.workgraph/workgraph.db
Watching: /path/to/project
file.created /path/to/project/notes.md
file.modified /path/to/project/notes.md
file.deleted /path/to/project/notes.md
```

Path configuration starts with CLI flags:

```text
workgraph run --home ~/.workgraph --database ~/.workgraph/workgraph.db --watch .
```

Configuration files are expected later, with this precedence:

```text
CLI flags > config file > defaults
```

Daemon mode is part of the Phase 0 MVP, but it layers on top of foreground
capture instead of replacing it. The expected shape is:

```text
workgraph daemon start
workgraph daemon stop
workgraph daemon status
```

Daemon mode should use local state such as a PID file and log file under the
WorkGraph home. It should not require a cloud service or privileged install.
