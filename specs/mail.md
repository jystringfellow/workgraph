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

Google Mail uses the same Google OAuth app as Google Calendar. The user connects
mail explicitly with:

```text
workgraph mail connect google
```

Google Mail OAuth uses authorization code with PKCE, the same workgraph Google
client id, and the same workgraph Google OAuth token relay used by Google
Calendar. The local CLI must not send or accept a Google client secret.

Google Mail has one connect mode. It requests read-only full message and
metadata access:

```text
https://www.googleapis.com/auth/gmail.readonly
```

`gmail.readonly` lets workgraph read email messages and metadata so it can
create local mail records, connect email conversations to projects, and restore
relevant context. Full message content is needed because subjects and headers
alone often omit decisions, requests, links, and follow-up items.

Google Mail credentials are stored separately from calendar credentials in the
workgraph home directory. Disconnecting Google Mail should revoke the stored
Google token when possible and remove local mail connector settings without
disconnecting Google Calendar.

Google Mail capture reads message ids from Gmail and fetches each message in
full format. Captured events are stored as `mail.message` with stable ids based
on provider, mailbox, and message id. Header fields provide subject, sender,
recipients, and message time; Gmail snippets and text/plain body parts provide
local context when available.

Microsoft Mail
--------------

Microsoft Mail uses the same Microsoft Entra application as Microsoft Calendar
while keeping workgraph commands, local config, and consent prompts separate.
Microsoft supports incremental delegated consent, so
`workgraph calendar connect microsoft` can request calendar permissions first
and `workgraph mail connect microsoft` can later request mail permissions for
the same app.

Microsoft Mail has one connect mode. It requests read-only full message access
for the signed-in user's mailbox and mailboxes shared with them:

```text
offline_access
https://graph.microsoft.com/Mail.Read
https://graph.microsoft.com/Mail.Read.Shared
```

`Mail.Read` reads the signed-in user's mailbox. `Mail.Read.Shared` covers mail
the signed-in user can access in shared or delegated mailboxes and is only valid
for work or school accounts.

Microsoft Mail credentials are stored separately from calendar credentials in
the workgraph home directory. Disconnecting Microsoft Mail should remove local
mail connector settings without disconnecting Microsoft Calendar.

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
