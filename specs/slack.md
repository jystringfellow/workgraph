# Slack Integration

WorkGraph should ingest Slack activity as communication events that can support
project resume and later memory suggestions.

Slack ingestion starts with explicit capture from a local exported event file:

```text
workgraph slack capture --events-file slack-events.json
```

This gives WorkGraph a deterministic ingestion path before adding Slack
authentication, API pagination, or workspace sync.

Slack ingestion should also support read-only daemon collection from explicitly
configured channels:

```text
WORKGRAPH_SLACK_TOKEN=xoxb-... workgraph run --slack-channel C123
```

Users can configure Slack access through OAuth:

```text
workgraph slack connect
```

The command starts a local callback server, opens the user's browser to Slack,
asks the user to approve WorkGraph's read-only Slack access, exchanges the
redirect code with PKCE, and stores the resulting credentials locally. Users do
not need to provide Slack client ids, client secrets, or copy returned codes in
normal use.

By default, the daemon discovers public and private channels visible to the
authorized user and polls them for messages and thread replies. Users may pass
`--channel <channel-id>` during connect or run to restrict collection to an
explicit allowlist.

Direct messages and group direct messages are not collected by default. Users
must explicitly opt in with `--include-dms` during connect or
`--slack-include-dms` during run. When enabled, WorkGraph requests Slack
`im:*` and `mpim:*` read/history scopes and includes `im` and `mpim`
conversations in discovery.

After Slack is connected, users can change local Slack collection preferences:

```text
workgraph slack configure --include-dms
```

This updates local connector settings. If Slack was originally authorized
without DM scopes, the user may need to reconnect with `--include-dms` so Slack
grants those additional user scopes.

The default OAuth redirect URL for public distribution is a WorkGraph-controlled
HTTPS relay:

```text
https://workgraph.pages.dev/slack/callback
```

The relay immediately forwards the browser to the local daemon callback:

```text
http://localhost:2727/slack/callback
```

Slack requires distributed apps to use SSL for OAuth redirect URLs. WorkGraph
therefore must register the HTTPS relay URL with Slack, not the localhost URL.
The relay page lives in this repository at
`public/slack/callback/index.html` and can be hosted as a static Cloudflare
Pages site.

The Pages project also serves a minimal top-level page at `public/index.html`
so the default `https://workgraph.pages.dev` URL is intentional.

Official WorkGraph builds must include a Slack public-client id for this flow.
Local development builds may pass `--client-id` for testing against a developer
Slack app.

On successful authorization, WorkGraph stores Slack connector settings under
the local WorkGraph home with user-only file permissions. The daemon can then
use the stored token and channels without requiring daily exports or repeated
token flags.

The daemon:

- polls discovered or explicitly configured Slack channels while WorkGraph
  capture is running
- polls IM and MPIM conversations only when the user has explicitly opted in
- fetches channel messages and available thread replies
- stores events through the same ingestion path as exported Slack events
- avoids duplicates across polling cycles
- does not read channels outside the authorized user's Slack visibility
- does not send messages, reactions, or other Slack actions

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
