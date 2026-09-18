# Bridged event contracts

Every bounded event is an object with required `type`, RFC3339 `timestamp`, and
object `payload`; `external_id`, `project`, `actor`, and `summary` are optional.
Claimed complete-snapshot rows may omit fields that workgraph explicitly derives
under the connector-specific contract below. Prefer `external_id` for ordinary
events because it provides strong deterministic dedupe.

Use these connector/source mappings and revision-aware identities:

| Connector | Source and type | `external_id` |
|---|---|---|
| GitHub | `github`, `github.pull_request` or `github.issue` | `<repo>#<kind>:<number>:<updated-at>` |
| Slack | `slack`, `slack.message`/`slack.reply` | `<channel>:<ts>`; append edited timestamp for edits |
| Slack Lists | `slack`, `slack.list_item` | `<list>:<row-key>:<revision-or-content-hash>` |
| Google/Microsoft mail | `mail.google`/`mail.microsoft`, `<source>.message` | stable message id |
| Google/Microsoft calendar | `calendar.google`/`calendar.microsoft`, `<source>.event` | `<event-id>:change:<change-key-or-last-modified>` |
| Azure Boards | `azure.boards`, `azure.boards.workitem` | `azdo:<org>:<id>:rev:<revision>` |
| Notion activity | `notion`, `notion.page_updated`/`notion.database_updated` | `<page-id>:<last-edited-time>` |

Mail stores a bounded body preview, not a full body. Calendar envelope time is
the occurrence start converted to UTC, but request completion—not event start—
advances capture progress. Preserve the provider's timezone-bearing start/end
values inside the payload.

Return all pages in the requested bounded query or every row in a declared
complete snapshot. If a connector cannot express the bounds, cannot finish the
query or snapshot, or lacks required permissions, report the request as failed
rather than silently truncating it.

For Slack messages, request `slack_read_thread` in detailed form because concise
output omits per-message timestamps. Treat its `oldest` and `latest` arguments
as advisory: filter every returned reply against the exact UTC request bounds
during normalization.

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

Slack Lists use declared `complete_snapshot` semantics with `slack_read_file`.
For a one-List request, submit the complete provider output verbatim as
`snapshot_csv` with `list_id`; workgraph parses it server-side. Multi-List
compatibility batches submit every CSV record as `slack.list_item` with payload
containing `list_id` and a `fields` object, and omit `timestamp` and
`external_id`. workgraph selects
the first complete per-List row-key candidate, applies optional state and
interest-column interpretation, assigns the request `until` as `observed_at`,
hashes canonical JSON of the semantic row, and derives
`<list>:<row-key>:<content-hash>`. Field order and observation metadata do not
affect the content hash. Duplicate or missing row keys fail the complete batch.
State is revision content, not identity, and never filters capture, so completed
rows remain evidence while future next-work views can exclude them. Without a
state mapping, `done` remains absent/unknown.
A renamed row may appear new when no stable related-message or other key is
available. A missing row is not a deletion or proof of completion.

For Microsoft calendar, preflight and use any secondary resource read required
to obtain the change key or last-modified value. A proven-empty result does not
verify this identity path.

For Azure Boards participant scope, pass one accessible project as the MCP
routing argument for `wit_query` while retaining the approved collection-wide
participant predicate. The routing project is not an additional scope filter.

For Notion activity, `last_edited_date_range` accepts dates, not timestamps;
query the enclosing day(s) and filter each result's returned timestamp
client-side against the exact request bounds, the same padding technique used
for Microsoft mail. The provider result ceiling is 50 with no cursor, so a
response of exactly 50 rows must be treated as truncated and reported as a
failure rather than ingested as if it were complete.
