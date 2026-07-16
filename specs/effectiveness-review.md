# Local Effectiveness Review

workgraph should provide a local-only review surface so users can assess whether
suggestions are helping them.

## Goals

- make suggestion quality visible to the user
- support personal tuning without product telemetry
- keep all review metrics local by default

## Core Metrics

The first review slice should report:

- suggestion acceptance rate
- dismissal rate and common dismissal reasons
- snooze rate
- connector freshness by source
- time-to-useful-suggestion

Definitions for the first slice are:

- A **disposition event** is an `accepted`, `dismissed`, or `snoozed`
  `suggestion_feedback` row whose `created_at` is inside the review window.
- Acceptance, dismissal, and snooze rates use the same denominator: all
  disposition events in the window. Each numerator counts its matching action.
  This makes the three rates add to 100 percent when data exists.
- Common dismissal reasons count `reason_code` values on dismissal events in the
  window, sort by count descending and then code ascending, and group a missing
  code as `unspecified`.
- A useful suggestion is a suggestion with at least one `accepted` or
  `completed` feedback event. Time-to-useful is the median whole-second elapsed
  time from `suggestions.created_at` to the first such event for each suggestion
  whose first useful event is in the review window. Even-sized samples use the
  arithmetic mean of the two middle values. Negative durations from malformed
  imported timestamps are excluded.
- A rate or time-to-useful result with no qualifying denominator/sample is
  `insufficient_data`, not zero.

Feedback rows, rather than the current suggestion status, define historical
metrics because feedback history is append-only.

## Time Windows

The review should support:

- current week
- last 7 days
- last 30 days

The default window is the current local week: Monday at 00:00:00 through the
review instant. `7d` and `30d` are rolling windows of exactly 168 and 720 hours
ending at the review instant. Window starts are inclusive and ends are
exclusive. Week boundaries and displayed timestamps use the local system
timezone; stored RFC3339 timestamps are compared as instants.

## Suggested CLI Surface

```text
workgraph review
workgraph review --since 7d
workgraph review --since 30d
workgraph review --format json
```

Only `week`, `7d`, and `30d` are accepted window values. `--format json` exposes
the same window, counts, denominators, availability states, metric values,
dismissal reasons, and connector states as text output.

## Connector Freshness

The review snapshots local connector runtime state at the review instant. It
includes connectors that report themselves connected. Freshness uses
`last_success_at`, never a failed `last_poll_at`:

- `unknown`: no successful poll is recorded
- `fresh`: last success is no more than two configured polling intervals old
- `stale`: last success is older than two configured polling intervals
- `disabled`: local polling is disabled
- `not_ready`: setup is not ready

A connector is independently marked `degraded` when setup is in error, a latest
poll error is present, or consecutive failures are non-zero. Thus a connector
may still have fresh data while its latest poll is degraded.

## Privacy And Telemetry

- metrics are computed from local records only
- no outbound reporting is required
- exports are explicit user actions

If export is supported, default output should be a local file path chosen by the
user.

## Data Inputs

The review should use:

- suggestion lifecycle records
- local feedback events, such as accept, dismiss, snooze, complete
- connector runtime status and poll history

The first review implementation depends on the shared suggestion storage
contracts in `specs/db-contracts.md`. Until suggestions and feedback are stored
durably, `workgraph review` should not invent placeholder quality metrics.

## Non-Goals For First Slice

- team-level dashboards
- cloud analytics pipelines
- behavioral profiling outside user-visible metrics
