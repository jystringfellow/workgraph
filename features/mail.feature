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
