# Bridged event contracts

Every event is an object with required `type`, RFC3339 `timestamp`, and object
`payload`; `external_id`, `project`, `actor`, and `summary` are optional. Prefer
`external_id` because it provides strong deterministic dedupe.

Use these connector/source mappings and revision-aware identities:

| Connector | Source and type | `external_id` |
|---|---|---|
| GitHub | `github`, `github.pull_request` or `github.issue` | `<repo>#<kind>:<number>:<updated-at>` |
| Slack | `slack`, `slack.message`/`slack.reply` | `<channel>:<ts>`; append edited timestamp for edits |
| Slack Lists | `slack`, `slack.list_item` | `<list>:<item>:<revision>` |
| Notion | `notion`, `notion.page` | `<page-id>:<last-edited-at>` |
| Google/Microsoft mail | `mail.google`/`mail.microsoft`, `<source>.message` | stable message id |
| Google/Microsoft calendar | `calendar.google`/`calendar.microsoft`, `<source>.event` | `<event-id>:change:<change-key-or-last-modified>` |
| Azure Boards | `azure.boards`, `azure.boards.workitem` | `azdo:<org>:<id>:rev:<revision>` |

Notion page payloads include `id`, `url`, `title`, `path`,
`page_last_edited_at`, and a bounded `preview`; optional projection fields are
`properties`, `created_time`, `created_by`, and `last_edited_by`. Do not fetch or
store a full page body for capture.

Mail stores a bounded body preview, not a full body. Calendar envelope time is
the occurrence start converted to UTC, but request completion—not event start—
advances capture progress. Preserve the provider's timezone-bearing start/end
values inside the payload.

Return all pages in the requested bounded query. If a connector cannot express
the bounds, cannot finish pagination, or lacks required permissions, report the
request as failed rather than silently truncating it.
