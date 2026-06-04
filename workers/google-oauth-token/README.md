# Google OAuth Token Relay

This Cloudflare Worker performs the Google Calendar OAuth token exchange for
workgraph. It exists because the local CLI should not ship or ask users for the
Google OAuth client secret.

The Worker only handles OAuth token exchange, including authorization-code and
refresh-token grants. It does not receive calendar event data and should not log
request bodies, authorization codes, access tokens, or refresh tokens.

For local development, create `.dev.vars` from the example file and run the
Worker locally:

```sh
cp .dev.vars.example .dev.vars
wrangler dev
```

Set `GOOGLE_CLIENT_SECRET` in `.dev.vars` to the Google Desktop OAuth client
secret. The `.dev.vars` file is ignored by git and must not be committed.

For production, configure the Google client secret as a Cloudflare Worker
secret:

```sh
wrangler secret put GOOGLE_CLIENT_SECRET
```

Deploy from this directory:

```sh
wrangler deploy
```

The expected route is:

```text
https://workgraph-google-oauth-token.jystringfellow.workers.dev/calendar/google/token
```
