# Endpoint Security Review

workgraph is local-first software that captures work context into files owned by
the local user. This guide gives endpoint administrators a concise way to
inspect the controls active on a particular installation and records the
remaining storage risks. The report is review evidence, not a compliance attestation.

## Collect Review Evidence

After initializing workgraph, collect the versioned JSON report:

```sh
workgraph security report --format json
workgraph settings get --format json
workgraph network destinations --format json
```

`workgraph security report` is read-only. It does not contact provider APIs,
refresh credentials, start capture, or modify local state. It reports local
paths, file modes, control states, counts, and stable findings. The output does
not include connector tokens, authorization headers, client secrets, API keys,
API-key environment variable names, captured events, or memory-file contents.

The settings report supplies managed settings provenance without secrets. The
network destinations report lists configured connector, OAuth, and LLM
endpoints so administrators can compare them with approved egress policy.

## Enforced Controls

On macOS and Linux, workgraph creates and repairs `~/.workgraph/` to `0700`.
The SQLite database, local settings, connector credential files, connector
runtime state, and daemon state, PID, and log files are created and repaired to
`0600`. These modes prevent access by other local users but do not replace
device encryption or endpoint access controls.

Connector credentials and captured events remain local by default. Core capture
does not require a workgraph cloud service. Provider traffic is limited to
configured connectors and their OAuth endpoints. Hosted LLM use requires
explicit configuration, and outbound text is filtered locally before approved
hosted requests.

Admin-controlled managed settings can lock down:

- allowed and disabled connector IDs
- Slack direct-message capture
- hosted LLM availability
- approved LLM providers, destinations, and models
- organization-specific outbound sensitive patterns

See [Managed Settings Deployment](managed-settings.md) for fixed policy paths
and the recommended endpoint-managed example.

## Known Storage Gaps

workgraph does not currently provide application-level SQLite encryption. Raw
connector access and refresh tokens are protected by local file permissions but
are not yet stored in an OS credential store such as macOS Keychain or Windows
Credential Manager. The security report records both gaps as high-severity
findings so an endpoint cannot be mistaken for having controls that are still
planned.

Windows credential ACL hardening has not been implemented or verified in
Windows CI. Windows deployment should remain out of scope for a security-
approved pilot until that work is complete.

## Interim macOS Deployment Requirements

For a limited company-managed macOS deployment before the remaining storage
work is complete:

1. Require organization-managed full-disk encryption, such as FileVault, and
   normal screen-lock and endpoint access policy.
2. Distribute the recommended managed settings through endpoint management.
3. Keep hosted LLM providers disabled unless Security approves a provider,
   destination, model, and data-handling policy.
4. Lock Slack direct-message capture off unless the additional scope and data
   collection are explicitly approved.
5. Review `workgraph network destinations --format json` against the approved
   egress allowlist.
6. Approve connector OAuth scopes individually and use the narrowest practical
   manual-token scopes when OAuth is unavailable.
7. Treat `~/.workgraph/`, backups of that directory, and the configured memory
   repository as company-sensitive local data.

These requirements reduce exposure for an internal pilot; they do not remove
the need for SQLite encryption, OS credential-store integration, software
distribution controls, and the organization's normal application review.

## Interpreting Status

The report status remains `attention_required` while high-severity storage or
permission findings exist. Each finding includes a stable `code`, `severity`,
`description`, and `remediation`. Automation should key on the finding code,
not prose.

Current expected storage findings are:

- `sqlite_not_encrypted`
- `connector_secrets_file_backed`

Additional permission findings indicate endpoint drift and should be corrected
before use. Re-running `workgraph init` repairs the home, database, and settings
permissions on supported POSIX systems. Rewriting connector configuration or
restarting capture repairs the corresponding connector and daemon files.
