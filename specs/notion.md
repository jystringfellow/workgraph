# Notion

workgraph Notion ingestion starts with a user-facing OAuth connection path.
Notion public connections do not support PKCE, so the local CLI must not exchange
authorization codes directly with a client secret.

Notion connection setup uses OAuth:

```text
workgraph notion connect
workgraph notion connect --no-browser
workgraph notion connect --code <oauth-code> --state <state>
workgraph notion disconnect
```

By default, connect opens the Notion authorization URL and completes OAuth
through a local callback registered as:

```text
http://localhost:2727/notion/callback
```

Manual connect with `--no-browser` prints the Notion authorization URL and does
not write local connector settings until the user reruns it with an OAuth code
and matching state. After code exchange, workgraph stores Notion connector
settings under the workgraph home directory with local-user-only file
permissions. Stored settings include access token, refresh token when granted,
token type, expiry when provided, workspace metadata, bot id, provider API base
URL, OAuth client id, and token relay URL when customized.

Notion connect commands should be idempotent when already connected. If Notion
has local connector settings and the user runs `workgraph notion connect`
without an explicit OAuth code, workgraph should not open OAuth again. It should
print that Notion is already connected and return successfully.

`workgraph notion disconnect` removes local Notion connector settings.
Disconnecting does not revoke access at Notion because Notion revocation is
managed from the user's Notion workspace connection settings. If Notion is not
connected, disconnect should print that it is already disconnected and return
successfully.

The first Notion OAuth slice is a narrow Cloudflare Worker token relay:

```text
https://workgraph-notion-oauth-token.jystringfellow.workers.dev/notion/token
```

The local CLI sends only the authorization code, redirect URI, client id, and
grant type to the relay. The relay validates the client id, adds HTTP Basic
authentication using the Notion client secret from Cloudflare Worker secrets,
forwards JSON token requests to Notion, returns Notion's JSON response, and does
not store or log OAuth codes, access tokens, refresh tokens, or request bodies.

The same relay handles Notion refresh-token exchanges. When stored Notion
credentials are expired or close to expiry, future capture should refresh the
access token through the relay. The local CLI sends the refresh token, client id,
and grant type to the relay; it still does not send or store the Notion client
secret.

Local development for the token relay should use Cloudflare Workers' `.dev.vars`
secret loading pattern. Developers can create
`workers/notion-oauth-token/.dev.vars` with `NOTION_CLIENT_SECRET=...` and run
`wrangler dev` from the Worker directory. The local `.dev.vars` file must be
ignored by git; production still uses `wrangler secret put NOTION_CLIENT_SECRET`.
