# Enterprise Security and Compliance

workgraph's connector model asks for sensitive local context, especially when
Slack uses user scopes to collect work context without manual bot invitations.
Enterprise users and IT administrators need clear guarantees about where data
lives, how it is protected, and what leaves the machine.

This spec captures security and compliance requirements before workgraph is
positioned for enterprise use.

## Goals

- Make the local-first architecture explicit and auditable.
- Protect captured company data at rest on lost or stolen devices.
- Make outbound network behavior clear and constrained.
- Prevent raw captured context from being sent to hosted AI providers without
  explicit user configuration and local filtering.
- Let company-managed devices apply admin-controlled settings that override
  workgraph user config for workgraph's own behavior.
- Provide an IT-readable compliance document that explains requested Slack
  scopes and data flow.

## Managed Settings

workgraph should support an admin-controlled managed settings file for
deployments where IT wants an approved workgraph build to follow centrally
defined defaults or locked values.

Managed settings should be explicit about what they can and cannot enforce. A
local config file cannot stop a user from running unrelated tools, compiling a
modified binary, or bypassing company policy outside workgraph. It can,
however, make approved workgraph behavior predictable, auditable, and compatible
with endpoint management, network controls, and centrally approved OAuth apps.

Requirements:

- read managed settings from platform-appropriate admin locations
- do not allow user settings, CLI flags, or environment variables to redirect
  the managed settings path at runtime
- allow facts to use internal test helpers without making path redirection part
  of the user-facing command surface
- support locked values that override CLI flags and local user config
- support unlocked managed defaults that local user config or CLI flags may
  override
- report managed setting provenance in human-readable and machine-readable
  diagnostics
- never print secrets while reporting effective config
- include an admin-facing deployment guide and recommended managed settings
  example that can be distributed through endpoint management

Initial managed controls should cover:

- disabling hosted LLM providers entirely
- restricting LLM base URLs or provider destinations to approved local/company
  endpoints
- disabling or enabling connector families
- disabling Slack direct-message and group-direct-message capture
- disabling full mail body capture when metadata-only capture exists
- setting connector scope defaults such as Slack channels, mailbox ids,
  calendar ids, or Azure Boards organization/project filters

Managed settings are a configuration and auditability mechanism, not an
"enterprise mode." They should be described as workgraph controls that can be
enforced only when paired with normal company software distribution and endpoint
policy.

## Local Data Protection

workgraph should support encrypted local storage for captured events and
connector data.

Requirements:

- encrypt the SQLite event store at rest
- store encryption keys in the operating system credential store, such as macOS
  Keychain or Windows Credential Manager
- avoid storing raw connector tokens in world-readable files
- preserve local-first operation without a workgraph cloud service
- document backup and recovery implications for encrypted local state

Markdown memory files are user-owned local files. If they contain sensitive
company context, workgraph should document how users can place the memory repo
inside their organization's approved encrypted storage location.

## Outbound AI Filtering

If workgraph integrates hosted LLM providers, it must filter outbound text
locally before sending requests.

Requirements:

- hosted LLM use is opt-in
- raw captured connector data is not sent to hosted AI providers by default
- managed settings can disable hosted LLM use or restrict LLM destinations for
  approved workgraph deployments
- local filters scrub high-risk patterns such as credentials, secrets, credit
  card numbers, access tokens, and configured internal string patterns
- outbound LLM requests are inspectable in logs or dry-run mode before enablement
- users can choose a provider or disable hosted AI entirely

Filtering is a risk-reduction layer, not a guarantee that all sensitive content
will be removed.

## Network Transparency

workgraph should document and expose the network destinations it contacts.

Initial expected destinations:

- Slack Web API over TLS when Slack is connected
- GitHub API or `gh` CLI network traffic when GitHub polling is enabled
- user-selected LLM provider endpoints only when AI features are explicitly
  configured

workgraph should not send raw captured work context to a workgraph-operated
cloud service as a requirement for core local functionality.

## IT-Friendly Compliance Document

The repository should include a concise compliance page for administrators.

The document should explain:

- why Slack user history scopes are requested
- which Slack scopes are requested and what each enables
- that raw captured data is stored locally by default
- how managed settings can restrict workgraph's LLM and connector behavior on
  company-managed devices
- where SQLite, connector config, and memory files live
- how local encryption works once implemented
- what network destinations workgraph contacts
- how hosted AI use is configured, filtered, and disabled
- what actions workgraph cannot take without explicit user approval

The document should be usable by an employee when requesting approval to install
the Slack app in an enterprise workspace.

## Non-Goals For This Slice

- enterprise admin dashboards
- centralized cloud sync
- full DLP certification
- legal claims beyond what the implementation actually enforces
