Feature: Calendar ingestion

Scenario: Capture normalized calendar events
  Given workgraph has been initialized
  And a normalized calendar event export exists
  When I run "workgraph calendar capture --events-file <calendar-events.json>"
  Then workgraph stores calendar.event records
  And each record preserves provider ids, timing, attendees, location, and meeting URL
  And the event timestamp is the calendar start time

Scenario: Avoid duplicate calendar events
  Given workgraph has already captured a calendar event
  When I capture the same provider calendar event again
  Then workgraph keeps one calendar.event record for that provider event

Scenario: Keep calendar provider adapters normalized
  Given Google Calendar and Outlook expose different event API shapes
  When their events are captured by workgraph
  Then both providers produce the same calendar.event payload contract

Scenario: Capture Google Calendar events
  Given workgraph has been initialized
  And Google Calendar has events available to the authorized user
  When I run "workgraph calendar capture --provider google --calendar-id primary"
  Then workgraph stores those Google events as calendar.event records
  And the records use the same normalized payload contract as imported calendar events
  And expired Google Calendar access tokens refresh through the workgraph Google token relay before capture

Scenario: Connect Google Calendar with OAuth
  Given workgraph has been initialized
  When I run "workgraph calendar connect google"
  Then workgraph opens Google OAuth and stores local Google Calendar connector settings after approval
  And the browser flow uses PKCE without requiring a client secret
  And the OAuth code exchange goes through the workgraph Google token relay
  And the token relay uses Cloudflare secrets in production and an ignored .dev.vars file for local development
  When I run "workgraph calendar connect google --no-browser"
  Then workgraph prints a Google OAuth authorization URL
  And workgraph does not store local Google Calendar connector settings yet
  When I rerun "workgraph calendar connect google" with the OAuth code and matching state
  Then workgraph stores local Google Calendar connector settings

Scenario: Disconnect Google Calendar
  Given Google Calendar is already connected
  When I run "workgraph calendar disconnect google"
  Then workgraph revokes the stored Google Calendar token
  And workgraph removes local Calendar connector settings

Scenario: Verify Microsoft Calendar publisher domain
  Given workgraph has a Microsoft Entra application
  When Microsoft checks the workgraph Pages publisher domain
  Then workgraph serves the Microsoft identity association file from ".well-known/microsoft-identity-association.json"
  And the file includes the Microsoft application id for workgraph

Scenario: Connect Microsoft Calendar with OAuth
  Given workgraph has been initialized
  When I run "workgraph calendar connect microsoft"
  Then workgraph opens Microsoft OAuth and stores local Microsoft Calendar connector settings after approval
  And the browser flow uses PKCE without requiring a client secret
  And the OAuth request targets Microsoft Graph calendar scopes, not Azure DevOps scopes
  When I run "workgraph calendar connect microsoft --no-browser"
  Then workgraph prints a Microsoft OAuth authorization URL
  And workgraph does not store local Microsoft Calendar connector settings yet
