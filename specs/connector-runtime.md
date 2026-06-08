# Connector Runtime

workgraph should make connected services behave consistently. Connecting a
provider should configure it for normal capture loops; users should not need to
remember separate manual capture commands for routine updates.

The target user model is:

```text
workgraph slack connect
workgraph github connect
workgraph calendar connect google
workgraph mail connect google
workgraph notion connect
workgraph azure boards connect --organization <org> --project <project>
workgraph start
```

After `workgraph start`, all enabled connected providers should poll at explicit,
visible intervals. Manual capture commands remain for debugging, imports,
backfills, and one-off runs.

## Principles

- **No silent automation**: connection and polling state must be visible. Users
  can disable connector polling or individual connectors.
- **Connect means capture-ready**: after a connector is connected, `workgraph
  start` should include it by default unless disabled.
- **Manual capture remains**: `capture` commands continue to exist for testable
  deterministic ingestion and troubleshooting.
- **One poll path per connector**: daemon polling and manual `connectors poll`
  should use the same connector-specific capture function.
- **Inspectable status**: `workgraph status` should show enabled connector
  polling, last poll time, last error, and next poll time when available.
- **Conservative APIs**: API connectors must dedupe events, honor rate limits,
  and back off after failures.

## Desired Commands

```text
workgraph connectors list
workgraph connectors enable <connector>
workgraph connectors disable <connector>
workgraph connectors interval <connector> <duration>
```

The first runtime control slice stores polling preferences in
`connectors.json`. Disabling a connector stops polling without removing OAuth
credentials from the provider-specific config file.

Future runtime commands should include:

```text
workgraph connectors poll --once
workgraph connectors poll --connector slack
workgraph start --no-connectors
workgraph start --connector <connector>
```

Provider-specific connect commands remain:

```text
workgraph slack connect
workgraph calendar connect google
workgraph mail connect google
workgraph notion connect
workgraph azure boards connect
```

Future GitHub setup should follow the same pattern. GitHub currently polls from
local git remotes through the authenticated `gh` CLI when available. A future
`workgraph github connect` can validate `gh auth status`, store local polling
preferences, and make GitHub polling visible like other connectors.

Git is local and does not require account connection, but it should still appear
in connector status as an enabled local source when file/git capture is active.

## Migration Steps

1. Keep current manual capture commands.
2. Add Notion capture from stored Notion settings.
3. Add calendar polling from stored Google/Microsoft settings.
4. Add mail polling from stored Google/Microsoft settings.
5. Add Notion polling from stored settings.
6. Add `workgraph connectors list`, `enable`, `disable`, and `interval`.
7. Introduce `workgraph connectors poll --once` with a shared result shape.
8. Move Slack daemon polling and Slack Lists polling behind the same connector
   poll status model while preserving Slack connect/disconnect restart behavior.
9. Move GitHub `gh` polling behind the same connector poll status model and add
   optional `github connect` validation/config.
10. Add Azure Boards polling from local Microsoft OAuth settings.
11. Show connector polling in `workgraph status`.
12. Wire the shared poll loop into `workgraph start` by default for connected
    providers.

## Connector State

Each connected provider should expose:

- connector id, such as `slack`, `slack.lists`, `github`, `calendar.google`,
  `calendar.microsoft`, `mail.google`, `mail.microsoft`, `notion`, or
  `azure.boards`
- enabled/disabled polling
- poll interval
- last successful poll time
- last error
- next poll time
- stored cursor or provider-specific checkpoint when available

The first implementation may store this state in the provider config files or a
small connector state file. A later pass can normalize it once patterns
stabilize.
