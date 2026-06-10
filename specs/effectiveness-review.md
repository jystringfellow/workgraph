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

Definitions should be explicit and stable so users can compare periods.

## Time Windows

The review should support:

- current week
- last 7 days
- last 30 days

Week boundaries should use the local system timezone.

## Suggested CLI Surface

```text
workgraph review
workgraph review --since 7d
workgraph review --format json
```

`--format json` should expose the same metric values for local scripting.

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
