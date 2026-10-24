# Capture Control Commands

workgraph controls local event capture with top-level commands:

```text
workgraph start
workgraph status
workgraph stop
```

Background capture is explicit. workgraph does not start capture silently during
`init`, `today`, or other read commands.

If background capture exits because of a fatal local watcher or event-store
error, `workgraph status` preserves and reports the last failure. Starting
capture again clears the prior failure after the new worker becomes ready.

Daemon state belongs to the worker PID recorded in it. A retiring worker must
only remove or replace state that still identifies that worker. In particular,
if a replacement worker becomes ready before the prior worker finishes shutting
down, the prior worker must not erase the replacement's `daemon.json` or
`daemon.pid` files.

The state files are a cache of process state, not the authority for whether a
worker exists. Status, start, and stop must also discover live capture workers
that match the resolved workgraph home or database. Missing or stale state must
not allow a second worker to start against the same database.

On macOS, the detached capture worker must retain a live workgraph supervisor
as its parent. This avoids a Go/macOS platform-verifier failure where HTTPS
requests from an orphaned child fail with `SecPolicyCreateSSL error: 0` after
the command that launched it exits (Go issue
[`#68557`](https://github.com/golang/go/issues/68557)). The supervisor performs
no capture itself and exits when its worker exits.

## Commands

```text
workgraph start
workgraph status
workgraph stop
```

## Start

`workgraph start` starts local event capture in the background and returns after
capture is ready.

Ready means the worker is running and both `daemon.json` and `daemon.pid` have
been written for the same worker PID. Connector-triggered daemon restarts have
the same guarantee: the connector command must not return while the replacement
worker's PID file is still pending.

### Waiting for readiness

Before it reports ready, the worker opens the database and prepares its schema
(see [Schema migrations](db-contracts.md#schema-migrations)). On the first
start after an upgrade this can take more than a minute on a large database,
either in the worker itself or while it waits for another workgraph process to
finish the same migration.

From the moment it holds the capture home lock until it is ready, the worker
keeps `daemon.starting.json` under the workgraph home up to date. The file
records the worker PID, database path and a heartbeat refreshed every second.
It is removed once `daemon.json` and `daemon.pid` are written, or when the
worker exits. The worker registers its single `SIGTERM`/`SIGINT` handler before
it takes the capture lock and uses it for its whole life: a signal during
startup cancels the wait for another process's migration and the worker exits
cleanly; a signal while this worker is itself migrating takes effect when the
current migration step finishes. A worker killed outright can leave the file
behind; readers ignore a heartbeat whose PID is not
running, is not a capture worker for this home or database, or has not updated
for 15 seconds, so a stale file or a reused PID is never trusted or signalled.

Once startup finishes, the worker also holds an exclusive `flock` on
`daemon.ready.lock` for the rest of its life. Process discovery (used when
`daemon.json` is missing or stale) only recovers state files for a matching
worker that holds this lock; a matching worker that does not is reported as
starting and no state is written for it, so discovery cannot make start report
readiness before startup is complete.

`start` distinguishes a busy worker from a dead one:

- ready: the worker wrote `daemon.json` and `daemon.pid` for the same live PID;
  start prints the started message and exits 0.
- worker exited: the process start launched exited before becoming ready and
  no process holds the capture home lock; start fails at once with `daemon did not become ready: capture worker exited during
  startup; see <home>/daemon.log` and exits 1.
- no sign of life: neither readiness nor a live heartbeat appeared within two
  seconds; start fails with the existing `daemon did not become ready` error
  and exits 1.
- stalled: the heartbeat stopped updating for 15 seconds; start fails with
  `daemon did not become ready: capture worker stopped reporting startup
  progress` and exits 1.
- busy: the heartbeat is live; start keeps waiting. When a database migration
  lock has been held for half a second, it prints `Upgrading database…` once,
  then the usual started message when the worker becomes ready.

Start waits at most five minutes for a live, busy worker. If the worker is
still starting then, start does not claim failure. It prints
`workgraph capture is still starting (upgrading database); check workgraph
status` (without the parenthetical when no migration is running) and exits 75
(`EX_TEMPFAIL`), so scripts can tell "not ready yet" apart from both success
and failure. The worker keeps starting in the background.

Running `start` again while a worker is starting joins the wait instead of
launching a second worker. If two starts race and both launch workers, the
loser exits on the capture home lock. Its start then keeps waiting while any
process holds `capture.lock` (the winner), and fails only once nobody holds it,
or when the holder publishes neither readiness nor a heartbeat for 15 seconds. Installed capture services use the same heartbeat
rules; start has no child process to watch in that case, so a dead worker shows
up as a missing or stale heartbeat.

It must:

- refuse to start before `workgraph init`
- read watch and ignore rules from `~/.workgraph/settings.json`
- use configured watch roots when no `--watch` flag is provided
- allow `--watch` flags to override configured watch roots for that run
- write local capture state under the workgraph home
- avoid recording events from ignored paths or names
- refuse to launch a duplicate when a matching capture worker is already alive,
  even if the daemon state files are missing

The default initialized settings watch existing common user-facing folders, so a newly initialized workgraph can start background capture without trying to recursively watch the entire home directory.

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

When not running, status says background capture is not running. If the last
worker exited with a fatal capture error, status also shows that last failure.

While a worker is starting, status reports it instead of "not running":

```text
workgraph capture is starting (upgrading database)
PID: 12345
Home: /path/to/.workgraph
```

When capture is running and another workgraph process is migrating the
database, status adds `Database upgrade in progress; capture status refreshes
when it finishes.` `doctor` reports `Daemon: starting PID <pid>` for a starting
worker.

## Stop

`workgraph stop` stops background capture explicitly and preserves events already written to SQLite.

Stopping should remove stale capture state when the process exits cleanly. If
more than one background `__capture-worker` process is running for the same
workgraph home or database, stop should terminate all of those matching workers
without stopping foreground commands or workers for other homes.

Stop reports success only after every matching worker has exited. If graceful
shutdown exceeds its deadline, stop retains inspectable daemon state and returns
an error identifying the worker PIDs that are still alive.

Background event reporting and connector cleanup must not prevent the worker
from observing `SIGTERM`. Connector shutdown is bounded so a stuck poller cannot
keep the worker alive indefinitely. Diagnostic event output may be drained or
dropped in background mode because SQLite remains the source of truth.

## Local State

Capture state lives under the workgraph home. The expected files are:

- `daemon.pid`
- `daemon.log`
- `daemon.json`
- `daemon.starting.json` while a worker is starting
- `capture.lock` and `daemon.ready.lock` (empty lock files)

The exact file names can change if the implementation needs it, but capture state must stay local, inspectable, and outside the captured user event stream.

## Non-Goals

Phase 0 background capture does not require:

- launch agents or system services
- privileged installation
- cloud coordination
- automatic start on login
