# Connectors

Connectors let workgraph capture context from services you already use. Setup is
local-first: credentials and connector settings are stored under
`~/.workgraph/`, and routine capture runs when you explicitly start workgraph.

```sh
workgraph start
```

Use connector controls to see what will be monitored:

```sh
workgraph connectors list
workgraph connectors status
workgraph connectors doctor
workgraph connectors upgrade
workgraph connectors disable <connector>
workgraph connectors enable <connector>
workgraph connectors interval <connector> 15m
```

`connectors doctor` reports local connector state that needs attention, such as
legacy configs without setup handoff state or credentials that recently failed
with invalid-auth errors. `connectors upgrade` performs a local-only
reconciliation of `connectors.json`; it does not contact provider APIs or
overwrite stored tokens.

## Slack

Connect Slack:

```sh
workgraph slack connect
```

Collect specific channels while connecting:

```sh
workgraph slack connect --channel C1234567890
```

Opt into direct and group direct messages explicitly:

```sh
workgraph slack connect --include-dms
```

If admin-managed settings lock Slack DM capture off, workgraph refuses
`--include-dms` before opening Slack OAuth and refuses capture startup if stored
Slack settings would poll DMs.

If you use a Slack List as a todo list, save its List id while connecting:

```sh
workgraph slack connect --list <list-id>
```

`workgraph start` then monitors that List as connector `slack.lists`.

Run a one-off Slack List capture for debugging:

```sh
workgraph slack lists capture --list-id <list-id>
```

Disconnect Slack:

```sh
workgraph slack disconnect
```

## Notion

Connect Notion:

```sh
workgraph notion connect
```

Connect Notion with a local internal integration token when OAuth is not
practical:

```sh
workgraph notion connect-token --token <token>
```

Disconnect Notion:

```sh
workgraph notion disconnect
```

Inspect Notion's local object index and captured page previews:

```sh
workgraph notion index list
workgraph notion index show <notion-page-or-database-id>
```

Run a one-off Notion capture for debugging:

```sh
workgraph notion capture
```

## Azure Boards

Connect Azure Boards:

```sh
workgraph azure boards connect \
  --organization <org> \
  --project <project> \
  --team <team>
```

Limit capture to one or more area paths:

```sh
workgraph azure boards connect \
  --organization <org> \
  --project <project> \
  --team <team> \
  --area-path '<area-path>'
```

Multiple `--area-path` flags are allowed and are combined as alternatives in
the default WIQL query. You can also provide a custom query:

```sh
workgraph azure boards connect \
  --organization <org> \
  --project <project> \
  --wiql '<wiql>'
```

Azure Boards uses the Microsoft OAuth PKCE flow and stores local connector
settings in `~/.workgraph/azure-boards.json`. After connecting, `workgraph
start` monitors matching work items as connector `azure.boards`.

Run a one-off capture for debugging:

```sh
workgraph azure boards capture \
  --organization <org> \
  --project <project> \
  --team <team>
```

Disconnect Azure Boards:

```sh
workgraph azure boards disconnect
```

## Calendar

Connect a calendar provider:

```sh
workgraph calendar connect google
workgraph calendar connect microsoft
```

Collect specific calendars while connecting:

```sh
workgraph calendar connect google --calendar-id <calendar-id>
workgraph calendar connect microsoft --calendar-id <calendar-id>
```

Disconnect a calendar provider:

```sh
workgraph calendar disconnect google
workgraph calendar disconnect microsoft
```

Run a one-off capture for debugging:

```sh
workgraph calendar capture --provider google --calendar-id <calendar-id>
workgraph calendar capture --provider microsoft --calendar-id <calendar-id>
```

## Mail

Connect a mail provider:

```sh
workgraph mail connect google
```

Disconnect a mail provider:

```sh
workgraph mail disconnect google
```

Run a one-off capture for debugging:

```sh
workgraph mail capture --provider google --mailbox-id <mailbox-id>
```

## Git And GitHub

Git capture is local and does not require account connection:

```sh
workgraph git connect
workgraph git capture
```

GitHub capture currently supports deterministic capture commands and local
remote-derived context:

```sh
workgraph github connect
workgraph github capture
```

`workgraph github connect` validates the authenticated `gh` CLI and enables
GitHub in the shared connector runtime.

You can rerun validation without changing provider credentials:

```sh
workgraph connectors validate github
```

## Manual Capture

Manual `capture` commands remain useful for imports, backfills, deterministic
tests, and troubleshooting. Routine capture should normally be:

```sh
workgraph init
workgraph <provider> connect
workgraph start
```

`workgraph start` should include enabled connected providers by default unless
you disable them with `workgraph connectors disable <connector>`.
