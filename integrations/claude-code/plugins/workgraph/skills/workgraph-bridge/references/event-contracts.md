# Bridged event contracts

Every event is an object with required `type`, RFC3339 `timestamp`, and object
`payload`; `external_id`, `project`, `actor`, and `summary` are optional. Prefer
`external_id` because it provides strong deterministic dedupe.

Use these connector/source mappings and revision-aware identities:

| Connector | Source and type | `external_id` |
|---|---|---|
| GitHub | `github`, `github.pull_request` or `github.issue` | `<repo>#<kind>:<number>:<updated-at>` |
| Slack | `slack`, `slack.message`/`slack.reply` | `<channel>:<ts>`; append edited timestamp for edits |
| Slack Lists | `slack`, `slack.list_item` | `<list>:<row-key>:<revision-or-content-hash>` |
| Google/Microsoft mail | `mail.google`/`mail.microsoft`, `<source>.message` | stable message id |
| Google/Microsoft calendar | `calendar.google`/`calendar.microsoft`, `<source>.event` | `<event-id>:change:<change-key-or-last-modified>` |
| Azure Boards | `azure.boards`, `azure.boards.workitem` | `azdo:<org>:<id>:rev:<revision>` |

Mail stores a bounded body preview, not a full body. Calendar envelope time is
the occurrence start converted to UTC, but request completion—not event start—
advances capture progress. Preserve the provider's timezone-bearing start/end
values inside the payload.

Return all pages in the requested bounded query. If a connector cannot express
the bounds, cannot finish pagination, or lacks required permissions, report the
request as failed rather than silently truncating it.

For Microsoft mail, express the provider date filter in the mailbox-local time
zone over a two-day pad on each side of the requested window. Filter the
returned `receivedDateTime` values against the exact UTC request bounds and
follow offset pagination contiguously until results pass the lower bound.

For Microsoft calendar, interpret the Windows zone id `Pacific Standard Time`
as DST-aware `America/Los_Angeles`, not a fixed UTC-8 offset. Use the occurrence
start converted to UTC as the event timestamp and preserve the provider's
timezone-bearing start and end objects in the payload.

An empty batch requires an exhaustive bounded query or an applicable control
query that proves emptiness. A zero-result search alone is not proof when the
provider search is indexed, capped, or partial.

When a Slack Lists connector exposes no stable revision, hash canonical JSON of
the normalized semantic row fields and use the lowercase hex SHA-256 digest as
the revision surrogate. Exclude field ordering and capture-time metadata. Use a
provider item id as `row-key` when available; otherwise use a documented stable
combination of configured columns. A renamed row may therefore appear new when
the provider exposes no stable identity. A missing row is not a deletion unless
the provider exposes deletion state or a later contract adds snapshot/tombstone
comparison.
