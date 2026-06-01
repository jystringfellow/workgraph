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
