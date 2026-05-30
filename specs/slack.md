# Slack Integration

workgraph should ingest Slack activity as communication events that can support
project resume and later memory suggestions.

Slack ingestion starts with explicit capture from a local exported event file:

```text
workgraph slack capture --events-file slack-events.json
```

This gives workgraph a deterministic ingestion path before adding Slack
authentication, API pagination, or workspace sync.

Slack ingestion should also support read-only daemon collection from explicitly
configured channels:

```text
WORKGRAPH_SLACK_TOKEN=xoxb-... workgraph start --slack-channel C123
```

Users can configure Slack access through OAuth:

```text
workgraph slack connect
```

The command starts a local callback server, opens the user's browser to Slack,
asks the user to approve workgraph's read-only Slack access, exchanges the
redirect code with PKCE, and stores the resulting credentials locally. Users do
not need to provide Slack client ids, client secrets, or copy returned codes in
normal use.

By default, the daemon discovers public and private channels visible to the
authorized user and polls them for messages and thread replies. Users may pass
`--channel <channel-id>` during connect or run to restrict collection to an
explicit allowlist.

Slack OAuth requests baseline channel, private-channel, user profile, and
workspace metadata read access so stored communication events can use readable
conversation and actor names while retaining Slack ids as evidence.

Direct messages and group direct messages are not collected by default. Users
must explicitly opt in with `--include-dms` during connect or
`--slack-include-dms` during run. When enabled, workgraph requests Slack
`im:*` and `mpim:*` read/history scopes and includes `im` and `mpim`
conversations in discovery.

Slack OAuth scopes are additive. To remove Slack-granted DM access, the user
must disconnect, which revokes the stored Slack token, and then reconnect
without `--include-dms`:

```text
workgraph slack disconnect
workgraph slack connect
```

After connecting with `--include-dms`, workgraph tells the user to run
`workgraph slack disconnect` before reconnecting without DM scopes if they
later want to remove Slack-granted DM access. After connecting without
`--include-dms`, workgraph tells the user that enabling DMs later requires
disconnecting and reconnecting with `--include-dms`.

When Slack connect or disconnect updates local connector settings and
background capture is already running, workgraph restarts that background
daemon so the Slack token, channels, and permission state take effect without a manual
`workgraph stop` and `workgraph start`.

The default OAuth redirect URL for public distribution is a workgraph-controlled
HTTPS relay:

```text
https://workgraph.pages.dev/slack/callback
```

The relay immediately forwards the browser to the local daemon callback:

```text
http://localhost:2727/slack/callback
```

Slack requires distributed apps to use SSL for OAuth redirect URLs. workgraph
therefore must register the HTTPS relay URL with Slack, not the localhost URL.
The relay page lives in this repository at
`public/slack/callback/index.html` and can be hosted as a static Cloudflare
Pages site.

The Pages project also serves a minimal top-level page at `public/index.html`
so the default `https://workgraph.pages.dev` URL is intentional.

Official workgraph builds must include a Slack public-client id for this flow.
Local development builds may pass `--client-id` for testing against a developer
Slack app.

On successful authorization, workgraph stores Slack connector settings under
the local workgraph home with user-only file permissions. The daemon can then
use the stored token and channels without requiring daily exports or repeated
token flags. The stored connector settings include Slack user scopes granted by
the OAuth response so status and messaging can distinguish channel-only
connections from DM-enabled connections. They also include the authorized Slack
user id so self-authored messages can be displayed as the user rather than as a
separate collaborator.
If older connector settings do not include that user id, the daemon resolves it
from Slack using the current token before storing new events.

The daemon:

- polls discovered or explicitly configured Slack channels while workgraph
  capture is running
- polls IM and MPIM conversations only when the user has explicitly opted in
- resolves Slack conversation names so public and private channels use channel
  names, IMs use the other participant name, and MPIMs use group DM/member names
- resolves Slack user ids to display names for event actors when user profile
  metadata is available
- normalizes Slack user and channel mention tokens in event summaries, labels
  mentions of the authorized Slack user as `(you)`, and preserves the original
  Slack ids as structured payload evidence
- marks events authored by the authorized Slack user as self-authored
- fetches channel messages and available thread replies
- continues polling known thread parents so replies added after the parent
  message was first seen are captured
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
  resolved conversation name
- `actor`: Slack user id or display name when available; `You (<display name>)`
  for messages authored by the authorized Slack user when that identity is known
- `summary`: short message text with Slack user and channel mention tokens
  normalized to readable `@name` and `#channel` forms when metadata is available
- `payload_json`: source-specific details such as channel id, channel name,
  user id, user display name when available, self-user marker, raw text,
  normalized text, structured mention ids/names, timestamp, thread timestamp,
  and permalink

Slack capture keeps one stored event per channel timestamp identity. Recapturing
the same exported message or thread reply must not create duplicates.

Slack ingestion must not send messages or perform automatic actions. Drafting
responses remains future work and must follow suggest -> draft -> approve -> act.
