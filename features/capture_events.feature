Feature: Capture events

Scenario: Capture a file modification
  Given WorkGraph is running
  When a file changes inside a project folder
  Then WorkGraph records a file event
  And the event includes the path and operation
  And the event payload is valid JSON

Scenario: Capture file lifecycle operations
  Given WorkGraph is running
  When a file is created, modified, or deleted inside a project folder
  Then WorkGraph records the operation as "created", "modified", or "deleted"

Scenario: Ignore WorkGraph internal files
  Given WorkGraph is running
  When a file changes inside "~/.workgraph"
  Then WorkGraph does not record a user work event

Scenario: Infer project from a git repository
  Given WorkGraph is running
  And a file changes inside a git repository
  When WorkGraph records the file event
  Then the event project is the repository name

Scenario: Infer project from folder when git is unavailable
  Given WorkGraph is running
  And a file changes outside a git repository
  When WorkGraph records the file event
  Then the event project is inferred from the nearest project folder

Scenario: Preserve source details for debugging
  Given WorkGraph captures an operational signal
  When the signal is stored as an event
  Then the event payload preserves source-specific details
