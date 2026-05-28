# Slack Integration

WorkGraph should ingest Slack activity as communication events that can support
project resume and later memory suggestions.

Slack ingestion starts with explicit capture from a local exported event file:

```text
workgraph slack capture --events-file slack-events.json
```

This gives WorkGraph a deterministic ingestion path before adding Slack
authentication, API pagination, or workspace sync.

The first MVP ingests:

- channel messages
- thread replies

Slack events should be stored in the existing event store:

- `source`: `slack`
- `type`: `slack.message` or `slack.thread_reply`
- `project`: explicit project from the export when present, otherwise the
  channel name
- `actor`: Slack user id or display name when available
- `summary`: short message text
- `payload_json`: source-specific details such as channel id, channel name,
  user, text, timestamp, thread timestamp, and permalink

Slack capture keeps one stored event per channel timestamp identity. Recapturing
the same exported message or thread reply must not create duplicates.

Slack ingestion must not send messages or perform automatic actions. Drafting
responses remains future work and must follow suggest -> draft -> approve -> act.
