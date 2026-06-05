# Mail

workgraph should eventually ingest email as high-sensitivity communication
context. Mail must be an explicit opt-in connector separate from calendar,
because email content has different user trust, provider review, and workplace
policy implications.

Potential commands:

```text
workgraph mail connect google
workgraph mail connect microsoft
workgraph mail capture --provider google
workgraph mail capture --provider microsoft
workgraph mail disconnect google
workgraph mail disconnect microsoft
```

Mail ingestion should start with read-only behavior. Captured events should use
a provider-neutral shape such as:

- `provider`: `google` or `microsoft`
- `mailbox_id`: provider mailbox id or `me`
- `message_id`: provider message id
- `thread_id`: provider thread/conversation id when available
- `subject`: message subject
- `timestamp`: sent or received timestamp
- `from`: sender display name or email
- `to`, `cc`: recipient display names or emails
- `snippet`: provider preview when available
- `body_text`: optional normalized body text when explicitly enabled
- `body_html`: optional original HTML body when explicitly enabled
- `project`: optional explicit project association

Google Mail
-----------

Google Mail should probably use a separate Google OAuth app from Google
Calendar, such as `workgraph-mail`, unless we decide the approval and consent
tradeoffs of a combined app are worth it.

The same Google OAuth app can technically request scopes incrementally with
`include_granted_scopes=true`. If a user grants Calendar scopes first and later
grants Gmail scopes to the same app, Google can return a combined authorization
covering both sets of scopes. However, Gmail read scopes are Restricted Google
Workspace scopes, so combining them with Calendar can complicate approval,
consent warnings, and revocation behavior.

Potential Google scopes:

```text
https://www.googleapis.com/auth/gmail.readonly
https://www.googleapis.com/auth/gmail.metadata
```

`gmail.readonly` allows reading messages and metadata. `gmail.metadata` avoids
message bodies and attachments but still reads headers and other mailbox
metadata. Both are Restricted scopes and require a separate Google review path
before workgraph should request them from users.

Microsoft Mail
--------------

Microsoft Mail can likely use the same Microsoft Entra application as Microsoft
Calendar while keeping workgraph commands, local config, and consent prompts
separate. Microsoft supports incremental delegated consent, so
`workgraph calendar connect microsoft` can request calendar permissions first
and `workgraph mail connect microsoft` can later request mail permissions for
the same app.

Potential Microsoft Graph delegated scopes:

```text
Mail.ReadBasic
Mail.Read
Mail.Read.Shared
offline_access
```

`Mail.ReadBasic` reads basic message properties without body, preview body,
attachments, or extended properties. `Mail.Read` reads the signed-in user's
mailbox. `Mail.Read.Shared` covers mail the signed-in user can access in shared
or delegated mailboxes and is only valid for work or school accounts.

Azure DevOps
------------

Azure DevOps may use Microsoft Entra ID authentication, but it is not the same
resource as Microsoft Graph mail or calendar. Azure DevOps should be modeled as
a separate work tracking connector with separate scopes, storage, and facts.

Privacy And Consent
-------------------

Before implementing mail ingestion, update the privacy policy and any provider
review material to describe exactly whether workgraph reads metadata only,
message snippets, full body text, HTML bodies, or attachments. The first mail
slice should prefer metadata/snippet ingestion unless full content is necessary
for a user-visible workflow.
