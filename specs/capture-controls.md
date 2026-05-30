# Capture Control Commands

workgraph controls local event capture with top-level commands:

```text
workgraph start
workgraph status
workgraph stop
```

Background capture is explicit. workgraph does not start capture silently during
`init`, `today`, or other read commands.

## Commands

```text
workgraph start
workgraph status
workgraph stop
```

## Start

`workgraph start` starts local event capture in the background and returns after
capture is ready.

It must:

- refuse to start before `workgraph init`
- read watch and ignore rules from `~/.workgraph/config.json`
- use configured watch roots when no `--watch` flag is provided
- allow `--watch` flags to override configured watch roots for that run
- write local capture state under the workgraph home
- avoid recording events from ignored paths or names

The default initialized config watches existing common user-facing folders, so a newly initialized workgraph can start background capture without trying to recursively watch the entire home directory.

For debugging, `workgraph start --foreground` runs capture attached to the current
terminal and prints captured events as they arrive.

## Status

`workgraph status` reports whether background capture is running.

When running, status includes:

- PID
- workgraph home
- database path
- watched directories
- ignored paths
- ignored names

When not running, status says background capture is not running.

## Stop

`workgraph stop` stops background capture explicitly and preserves events already written to SQLite.

Stopping should remove stale capture state when the process exits cleanly. If
more than one background `__capture-worker` process is running for the same
workgraph home or database, stop should terminate all of those matching workers
without stopping foreground commands or workers for other homes.

## Local State

Capture state lives under the workgraph home. The expected files are:

- `daemon.pid`
- `daemon.log`

The exact file names can change if the implementation needs it, but capture state must stay local, inspectable, and outside the captured user event stream.

## Non-Goals

Phase 0 background capture does not require:

- launch agents or system services
- privileged installation
- cloud coordination
- automatic start on login
