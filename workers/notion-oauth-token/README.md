# Notion OAuth Token Relay

This Cloudflare Worker performs the Notion OAuth token exchange for workgraph.
It exists because Notion public OAuth connections require a client secret at the
token endpoint, and the local CLI should not ship or ask users for that secret.

The Worker only handles OAuth token exchange, including authorization-code and
refresh-token grants. It does not receive Notion page or database data and
should not log request bodies, authorization codes, access tokens, or refresh
tokens.

For local development, create `.dev.vars` from the example file and run the
Worker locally:

```sh
cp .dev.vars.example .dev.vars
wrangler dev
```

Set `NOTION_CLIENT_SECRET` in `.dev.vars` to the Notion public connection client
secret. The `.dev.vars` file is ignored by git and must not be committed.

For production, configure the Notion client secret as a Cloudflare Worker
secret:

```sh
wrangler secret put NOTION_CLIENT_SECRET
```

Deploy from this directory:

```sh
wrangler deploy
```

The expected route is:

```text
https://workgraph-notion-oauth-token.jystringfellow.workers.dev/notion/token
```
