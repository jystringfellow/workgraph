# Agent Plugin

Status: implemented

## Intent

workgraph ships one agent-facing plugin that makes its local capabilities easy
to discover and use from supported AI coding clients. The plugin is named
`workgraph`; individual skills remain separately named capabilities inside it.

Codex, Claude Code, GitHub Copilot CLI, and Antigravity (`agy`) are the
supported hosts. Their plugin formats and registration commands differ, so
workgraph embeds one thin package per host, but every package represents the
same logical plugin and contains the same canonical workgraph skills.

## Package contract

Each host package contains:

- a plugin manifest named `workgraph`;
- one local MCP registration named `workgraph`, invoking the installed
  executable as `workgraph bridge mcp --home <workgraph-home>`;
- `workgraph-bridge`, including its normalized event-contract reference;
- `workgraph-memory`;
- `workgraph-ai-checkpoint`;
- host metadata required to install the package from its local workgraph
  marketplace.

The canonical skills live under `.agents/skills/`. Installation copies those
directories into the selected host package so safety rules do not drift between
Codex, Claude Code, Copilot, and repository-local use.

The MCP is currently limited to bridge capture and configuration tools. Bundling
it in the broader plugin does not silently expose model execution, provider
credentials, arbitrary database access, or future workgraph capabilities.
Additional MCP tools require their own specs and facts.

## Command surface

```sh
workgraph plugin install --client <codex|claude-code|copilot|agy>
workgraph plugin install --client <codex|claude-code|copilot|agy> --model <model>
workgraph plugin doctor --client <codex|claude-code|copilot|agy>
```

`plugin install`:

1. verifies the selected client executable exists;
2. copies the matching embedded package into workgraph-owned local state;
3. overlays all canonical workgraph skills;
4. writes an absolute MCP command and explicit workgraph home;
5. registers the local marketplace and installs or updates `workgraph`;
6. installs the opt-in macOS bridge drain worker unless `--no-launchd` is set;
7. persists an explicitly selected unattended model, preserves it when the
   model flags are omitted on reinstall, or clears it with `--clear-model`;
8. reports the client, model, package location, skills, MCP, and worker state;
9. tells the user that executable upgrades require a daemon restart and a new
   client session, because neither long-lived process hot-reloads its binary.

The command is idempotent and preserves unrelated client settings. Rerunning it
is the supported plugin update path after upgrading the workgraph binary.

`plugin doctor` is provider-free. It verifies the client executable, `workgraph`
manifest, all bundled skills and bridge references, local MCP tool discovery, a
disposable claim/empty-ingest round trip, bridge worker marker, configured
model, and heartbeat.

For compatibility, `workgraph bridge install` and `workgraph bridge doctor`
delegate to the same implementation. They install and diagnose the broader
`workgraph` plugin rather than a legacy `workgraph-bridge` plugin. `bridge mcp`
and `bridge drain` remain bridge-specific operational commands.

## Host packages

### Codex

The Codex package contains `.codex-plugin/plugin.json`, `.mcp.json`, and
`skills/`. Its marketplace entry is `workgraph@workgraph`, with explicit
installation/authentication policy and Productivity category metadata.

### Claude Code

The Claude Code package contains `.claude-plugin/plugin.json`, `.mcp.json`, and
`skills/`. Its marketplace entry is also `workgraph@workgraph`.

### GitHub Copilot CLI

The Copilot package contains `.github/plugin/marketplace.json` at the install
root and `plugins/workgraph/plugin.json`, `.mcp.json`, and `skills/`. The MCP
registration uses Copilot's `local` server type with all workgraph tools
exposed to the plugin. Installation runs `copilot plugin marketplace remove
workgraph` (tolerating an unregistered marketplace), `copilot plugin
marketplace add <install-root>`, and `copilot plugin install
workgraph@workgraph`. Copilot loads local-marketplace plugins live from the
install root rather than copying them, so reinstalling into the same root takes
effect without a plugin update, and the remove step re-points the marketplace
when the install root changes. Inside a Copilot session the plugin's MCP tools
are named `workgraph-<tool>`, and their permissions are `workgraph(<tool>)`.

Copilot has no persistent per-project permission settings comparable to
Claude Code's, so its unattended drain grants permissions per invocation:
`--allow-tool=workgraph(<tool>)` for each required workgraph drain tool, never
the whole `workgraph` server, shell, file write, or `--allow-all-tools`.
Because `--allow-tool` only adds approvals, the drain also fixes the visible
tool set with one `--available-tools=` per granted tool (`server(tool)` becomes
`server-tool`) plus `skill`, which Copilot needs to load plugin skills. It
denies `shell`, `write`, `url`, and `memory`, passes `--no-custom-instructions`
and `--disallow-temp-dir`, and removes `COPILOT_ALLOW_ALL` and
`COPILOT_CUSTOM_INSTRUCTIONS_DIRS` from the worker environment, so saved
approvals and environment overrides cannot widen the drain.
Optional provider tools use the exact `server(tool)` form, reject wildcards and
reserved Copilot permission kinds, and are stored in
`<workgraph-home>/bridge/workers.json`. Reinstalling merges newly allowed
provider tools with stored ones; `--clear-provider-tools` empties the stored
set first. Stored tools are revalidated before every drain. Provider tools are granted only to capture
drains; LLM-task drains receive only workgraph tools. With no approved provider
tool, the Copilot worker leaves capture requests pending rather than attempting
provider reads.

### Antigravity (`agy`)

The Antigravity package contains `plugins/workgraph/plugin.json`,
`mcp_config.json`, and `skills/`, the native plugin layout. The installed
`mcp_config.json` registers only the local `workgraph` stdio server with the
resolved workgraph executable and home. Installation first runs `agy plugin
validate <install-root>/plugins/workgraph --strict`; a validation failure is
reported and leaves the installed plugin unchanged. Only a valid package is
registered, with best-effort `agy plugin uninstall workgraph` followed by `agy
plugin install <install-root>/plugins/workgraph`. An install failure after a
successful uninstall is reported and leaves no workgraph plugin until install
is rerun; agy offers no atomic replacement.

Unattended drains run headlessly as `agy --settings <json> [--model <model>] -p
<prompt>`. The session-only settings contain only `permissions.allow` rules:
`mcp(workgraph_workgraph/<tool>)` for each required workgraph drain tool and, for
capture drains, each approved provider tool. Antigravity names an MCP server
supplied by a plugin `<plugin>_<server>`, so the packaged `workgraph` server is
`workgraph_workgraph`; this was verified against agy 1.4.10, where
`mcp(workgraph/<tool>)` is auto-denied headlessly. Neither `workgraph` nor
`workgraph_workgraph` may be approved as a provider server. Headless Antigravity auto-denies every
tool without an allow rule, so the worker never passes
`--dangerously-skip-permissions` or `--mode`. On agy 1.4.10 the session-only
allow list replaced the user's own allow rules for the run: a globally allowed
command was still denied. Optional
provider tools use the exact `mcp(server/tool)` form, reject wildcards and the
`workgraph` server, are stored in `<workgraph-home>/bridge/workers.json`, and
follow the same merge, `--clear-provider-tools`, revalidation, and
pending-without-provider-tool behavior as Copilot. Antigravity has no verified
per-account configuration directory, so `--config-dir` is rejected for `agy`.

Host-specific manifests may present different supported metadata, but their
plugin identity, semantic version, MCP command, and skill contents must remain
equivalent.

## LLM execution boundary

Installing the plugin is not required for `workgraph llm connect codex` or
`workgraph llm connect claude-code` or `workgraph llm connect copilot`.
Client-backed LLM profiles are initiated by
workgraph as bounded model calls; the plugin is initiated by an agent to use
local workgraph capabilities. Keeping those paths independent avoids granting
MCP or provider-tool access to summary prompts.

## Installation experience

On a supported work machine:

```sh
brew upgrade workgraph
workgraph init
workgraph plugin install --client claude-code # or codex
workgraph plugin doctor --client claude-code
```

The user starts a new client session after installation so newly installed
skills and MCP tools are discovered. They can then ask the client to configure
credential-free bridges, maintain workgraph memory, or checkpoint a wrapped AI
session. Each skill retains its own approval and safety boundaries.

## Security and compatibility

- Installation never copies provider or model credentials into workgraph.
- A selected client model is non-secret worker configuration stored under the
  workgraph home, not in generated launchd files.
- The generated MCP config contains only executable and local workgraph paths.
- Plugin installation does not enable hosted LLM consent or create an LLM
  profile.
- Installing one host does not modify the other host.
- `git` remains direct and cannot be configured through the bridge skill.
- Legacy bridge install and doctor command lines continue to work.
- Unsupported clients and missing executables fail before client settings are
  changed.

## Implementation checklist

1. [x] Add this spec, its feature, and failing package/CLI facts.
2. [x] Rename both embedded plugin identities from `workgraph-bridge` to
   `workgraph`.
3. [x] Bundle all three canonical skills and bridge references in both packages.
4. [x] Add `plugin install` and `plugin doctor` over a shared implementation.
5. [x] Retain bridge install/doctor compatibility aliases.
6. [x] Update help, onboarding, command, connector, and bridge documentation.
7. [x] Validate both plugin packages, and pass all facts.
8. [x] Add the GitHub Copilot CLI package, marketplace registration, per-drain
   permission grants, and persisted exact provider tools.

## Facts

- Codex, Claude Code, and Copilot packages use the `workgraph` plugin identity
  and valid host manifests.
- Installing either package places all three canonical skills and the bridge
  event-contract reference under `plugins/workgraph/skills`.
- Installed skill files exactly match their canonical `.agents/skills` source.
- Every package registers the local MCP as `workgraph` with an absolute binary
  path and explicit workgraph home.
- Every client is asked to install `workgraph@workgraph`; Copilot first adds
  the local marketplace.
- Copilot drain grants only the explicit workgraph drain tools and explicitly
  approved provider tools, and leaves capture pending without provider tools.
- Reinstalling is successful and preserves unrelated files in the install root.
- `plugin doctor` fails when any required skill is absent and succeeds without
  contacting a provider when the complete package is present.
- Bridge install/doctor aliases operate on the same `workgraph` package.
- Help and user documentation show the plugin-first setup path for every host.
