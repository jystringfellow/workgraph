# Bridge Worker Model Selection

The unattended bridged-capture worker may pin one model per supported client:

```text
workgraph plugin install --client codex --model <model>
workgraph plugin install --client claude-code --model <model>
```

`workgraph bridge install` exposes the same compatibility flags. A model name
is non-secret client configuration; it is not an LLM profile, credential, or
hosted-model consent grant.

workgraph stores the selection in `bridge/workers.json`, keyed by client. The
generated launchd file continues to invoke `workgraph bridge drain` without a
model argument. At execution time, drain reads the durable worker config and
passes the selected model to the client CLI. This keeps the setting inspectable
and prevents generated launchd files from becoming configuration state.

Install behavior is deliberate:

- `--model <model>` sets or replaces the selected model for that client.
- Omitting both model flags preserves an existing selection across reinstall.
- `--clear-model` removes the selection and returns that client to its default.
- Supplying `--model` and `--clear-model` together is rejected.

Install and doctor output report either the exact selected model or `client
default`. The worker config never contains provider credentials. Codex drain
uses `codex exec ... --model <model>` and Claude Code drain uses `claude -p ...
--model <model>` when a selection exists.
