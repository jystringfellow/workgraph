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
- Provide an IT-readable compliance document that explains requested Slack
  scopes and data flow.

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
