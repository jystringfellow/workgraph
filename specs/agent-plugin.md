# Agent Plugin

Status: implemented

## Intent

workgraph ships one agent-facing plugin that makes its local capabilities easy
to discover and use from supported AI coding clients. The plugin is named
`workgraph`; individual skills remain separately named capabilities inside it.

Codex and Claude Code are the first supported hosts. Their plugin formats and
registration commands differ, so workgraph embeds one thin package per host,
but both packages represent the same logical plugin and contain the same
canonical workgraph skills.

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
Codex, Claude Code, and repository-local use.

The MCP is currently limited to bridge capture and configuration tools. Bundling
it in the broader plugin does not silently expose model execution, provider
credentials, arbitrary database access, or future workgraph capabilities.
Additional MCP tools require their own specs and facts.

## Command surface

```sh
workgraph plugin install --client <codex|claude-code>
workgraph plugin doctor --client <codex|claude-code>
```

`plugin install`:

1. verifies the selected client executable exists;
2. copies the matching embedded package into workgraph-owned local state;
3. overlays all canonical workgraph skills;
4. writes an absolute MCP command and explicit workgraph home;
5. registers the local marketplace and installs or updates `workgraph`;
6. installs the opt-in macOS bridge drain worker unless `--no-launchd` is set;
7. reports the client, package location, skills, MCP, and worker state;
8. tells the user that executable upgrades require a daemon restart and a new
   client session, because neither long-lived process hot-reloads its binary.

The command is idempotent and preserves unrelated client settings. Rerunning it
is the supported plugin update path after upgrading the workgraph binary.

`plugin doctor` is provider-free. It verifies the client executable, `workgraph`
manifest, all bundled skills and bridge references, local MCP tool discovery, a
disposable claim/empty-ingest round trip, bridge worker marker, and heartbeat.

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

Host-specific manifests may present different supported metadata, but their
plugin identity, semantic version, MCP command, and skill contents must remain
equivalent.

## LLM execution boundary

Installing the plugin is not required for `workgraph llm connect codex` or
`workgraph llm connect claude-code`. Client-backed LLM profiles are initiated by
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
7. [x] Validate both plugin packages, pass all facts, and cross off the roadmap.

## Facts

- Codex and Claude Code packages use the `workgraph` plugin identity and valid
  host manifests.
- Installing either package places all three canonical skills and the bridge
  event-contract reference under `plugins/workgraph/skills`.
- Installed skill files exactly match their canonical `.agents/skills` source.
- Both packages register the local MCP as `workgraph` with an absolute binary
  path and explicit workgraph home.
- Both clients are asked to install `workgraph@workgraph`.
- Reinstalling is successful and preserves unrelated files in the install root.
- `plugin doctor` fails when any required skill is absent and succeeds without
  contacting a provider when the complete package is present.
- Bridge install/doctor aliases operate on the same `workgraph` package.
- Help and user documentation show the plugin-first setup path for both hosts.
