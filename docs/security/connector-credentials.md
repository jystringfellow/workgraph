# Connector Credential Hardening

workgraph stores connector credentials locally under `~/.workgraph/`. These
files are user-owned local state and should be protected as secrets because they
can contain access tokens, refresh tokens, OAuth metadata, manual internal
integration tokens, or credential-adjacent runtime state.

## Credential Inventory

| Path | Contents | Revocation behavior |
| --- | --- | --- |
| `~/.workgraph/slack.json` | Slack access token, granted scopes, workspace metadata, selected channels, selected Lists, DM opt-in state | `workgraph slack disconnect` revokes the Slack token when possible and removes local settings. |
| `~/.workgraph/calendar.json` | Google and Microsoft Calendar access tokens, refresh tokens, granted scopes, selected calendar ids, provider URLs | `workgraph calendar disconnect google` revokes the Google token when possible and removes local Google settings. Microsoft Calendar disconnect removes local credentials and explains that Microsoft consent must be revoked from the account or tenant when needed. |
| `~/.workgraph/mail.json` | Google and Microsoft Mail access tokens, refresh tokens, granted scopes, provider URLs | `workgraph mail disconnect google` revokes the Google token when possible and removes local Google settings. Microsoft Mail disconnect removes local credentials and explains that Microsoft consent must be revoked from the account or tenant when needed. |
| `~/.workgraph/notion.json` | Notion OAuth or manual-token access token, workspace metadata, provider URLs | `workgraph notion disconnect` removes local credentials. Notion revocation is handled from Notion integration settings when needed. |
| `~/.workgraph/azure-boards.json` | Azure Boards access token, refresh token, organization, project, filters, provider URLs | `workgraph azure boards disconnect` removes local credentials and explains any provider-side consent revocation steps when relevant. |
| `~/.workgraph/llm.json` | LLM profile metadata such as provider, base URL, model, AWS profile, Bedrock model ARN, and API key environment variable names | No raw API keys should be stored here. OpenAI-compatible hosted credentials should use environment variable references such as `api_key_env`; Bedrock uses local AWS credential resolution. |
| `~/.workgraph/connectors.json` | Runtime connector state, setup state, validation timestamps, and validation error summaries | Does not store provider access tokens, but is still written with local-user-only permissions because errors can be credential-adjacent. |

## File Permissions

The workgraph home directory itself should be `0700` on POSIX-style platforms so
the local user can traverse it and other local users cannot list or enter it.
`workgraph init` creates and repairs the directory with that mode.

Connector credential and runtime files are written with `0600` permissions where
the operating system supports POSIX-style file modes. Writers also repair
existing files back to `0600` when they rewrite connector state.

This protects against accidental local multi-user exposure on POSIX-style
platforms such as macOS and Linux, but it is not a replacement for disk
encryption, endpoint controls, or an OS credential store. Windows needs
equivalent ACL hardening because POSIX mode bits are not a Windows security
boundary. Windows ACL implementation should be verified by Windows CI before it
is considered complete.

## Diagnostics

User-facing diagnostics must not print connector credentials. In particular:

- `workgraph settings get --format json` reports managed settings and non-secret
  local settings counts, not access tokens or refresh tokens.
- `workgraph connectors doctor` reports connector readiness and token presence,
  but does not print connector credentials.
- `workgraph doctor` reports local readiness without printing stored bearer
  tokens, refresh tokens, PATs, client secrets, or authorization headers.

Diagnostics may report paths, provider names, granted scope names, selected
resource ids, and whether a token is present. They should not report token
values.

## Manual Tokens

Manual-token setup commands remain explicit. They are intended for enterprise
environments where OAuth app approval is blocked or slow, and they should use
the narrowest provider token scope and shortest practical lifetime.

OAuth remains the default setup path when available. Manual tokens should be
rotated and revoked from the provider console when no longer needed.

## Remaining Cross-Platform Hardening

The completed file-permission slice is POSIX connector credential file permission hardening.
Remaining work is split into separate platform and storage layers:

- Windows connector credential ACL hardening for credential and runtime files.
- Windows connector credential ACL design and CI readiness before implementation
  is claimed complete.
- Windows connector credential ACL implementation verified by Windows CI.
- OS credential-store backed connector secrets for raw access tokens and refresh
  tokens.
- OS credential-store backed SQLite encryption keys for encrypted local event
  storage.
- SQLite encryption at rest using the stored encryption keys.

For connector secrets, the target shape is to keep inspectable connector
metadata in JSON while storing access tokens and refresh tokens in macOS
Keychain, Windows Credential Manager, Linux Secret Service, or another
explicitly supported local key store.
