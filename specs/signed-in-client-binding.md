# Signed-in Client Bindings

workgraph can invoke signed-in AI clients for bridged capture and LLM tasks.
Those invocations must be able to select the same account context without
depending on the shell that happens to launch them.

A client binding contains a configuration directory. The binding is
non-secret, local-user-only configuration. It maps to the client-native
environment variable at execution time:

| Client | Environment variable |
| --- | --- |
| `claude-code` | `CLAUDE_CONFIG_DIR` |
| `codex` | `CODEX_HOME` |

`workgraph plugin install --client <client> --config-dir <path>` stores the
binding with that client's worker settings in `bridge/workers.json`.
Reinstallation without a config-dir flag preserves the binding, while
`--clear-config-dir` returns the worker to the ambient client default. The
generated launch agent does not become configuration state; `bridge drain`
resolves and applies the durable binding immediately before invoking the
client.

`workgraph llm connect <client> --config-dir <path>` and `workgraph llm add
<profile> --provider ai-client --client <client> --config-dir <path>` store the
binding with the profile in `llm.json`. Each profile may therefore target a
different account context. LLM execution applies the profile binding before
invoking the client.

Config-dir paths are resolved to absolute paths and must name an existing
directory when configured. A configured binding replaces any ambient value
for that client's config-dir environment variable. An unconfigured binding
preserves the existing ambient behavior for backward compatibility.

Install, list, and doctor output report the effective stored config-dir as an
exact path or `ambient default`. Doctor verifies that a configured directory
still exists without invoking the client or contacting a provider. workgraph
does not infer provider account or organization names from client-private
state and does not store login tokens.
