# Bridge Worker Model Selection

The unattended bridged-capture worker may pin one model per supported client:

```text
workgraph plugin install --client codex --model <model>
workgraph plugin install --client claude-code --model <model>
workgraph plugin install --client copilot --model <model>
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
uses `codex exec ... --model <model>`, Claude Code drain uses `claude -p ...
--model <model>`, and Copilot drain uses `copilot ... --model=<model> -p ...`
when a selection exists.

The same per-client entry may hold `provider_tools`, the exact Copilot
`server(tool)` permissions approved with `--allow-provider-tool`. Reinstalling
merges newly approved tools with stored ones. `--clear-provider-tools`
(Copilot only) removes every stored tool before applying any
`--allow-provider-tool` in the same command, so the two together replace the
set. Stored tools are revalidated whenever the config is read: an entry that is
not an exact provider `server(tool)` name, names `workgraph`, or names a
reserved Copilot permission kind makes drain and install fail before launching
Copilot. An entry is removed only when it has no model, config directory, or
provider tools.
