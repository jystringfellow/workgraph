Feature: Mail ingestion

Scenario: Connect Google Mail with OAuth
  Given workgraph has been initialized
  When I run "workgraph mail connect google"
  Then workgraph opens Google OAuth and stores local Google Mail connector settings after approval
  And the browser flow uses PKCE without requiring a client secret
  And the OAuth request uses the same workgraph Google app as Google Calendar
  And the OAuth request targets the Gmail read-only full-content scope
  When I run "workgraph mail connect google --no-browser"
  Then workgraph prints a Google Mail OAuth authorization URL
  And workgraph does not store local Google Mail connector settings yet

Scenario: Connect Microsoft Mail with OAuth
  Given workgraph has been initialized
  When I run "workgraph mail connect microsoft"
  Then workgraph opens Microsoft OAuth and stores local Microsoft Mail connector settings after approval
  And the browser flow uses PKCE without requiring a client secret
  And the OAuth request uses the same workgraph Microsoft app as Microsoft Calendar
  And the OAuth request targets read-only Microsoft Graph mail scopes
  When I run "workgraph mail connect microsoft --no-browser"
  Then workgraph prints a Microsoft Mail OAuth authorization URL
  And workgraph does not store local Microsoft Mail connector settings yet

Scenario: Disconnect Google Mail
  Given Google Mail and Microsoft Mail are connected
  When I run "workgraph mail disconnect google"
  Then workgraph revokes the Google Mail token when possible
  And workgraph removes only the local Google Mail connector settings
  And Microsoft Mail remains connected

Scenario: Capture Google Mail messages
  Given Google Mail is connected
  When I run "workgraph mail capture --provider google"
  Then workgraph stores Gmail messages as normalized mail.message events
  And each event keeps provider, mailbox id, message id, thread id, headers, snippet, and available text body
  And recapturing the same Gmail message updates the existing event instead of creating a duplicate
