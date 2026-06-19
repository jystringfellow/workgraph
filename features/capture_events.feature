Feature: Capture events

Scenario: Capture a file modification
  Given workgraph is running
  When a file changes inside a project folder
  Then workgraph records a file event
  And the event includes the path and operation
  And the event payload is valid JSON

Scenario: Capture file lifecycle operations
  Given workgraph is running
  When a file is created, modified, or deleted inside a project folder
  Then workgraph records the operation as "created", "modified", or "deleted"

Scenario: Ignore workgraph internal files
  Given workgraph is running
  When a file changes inside "~/.workgraph"
  Then workgraph does not record a user work event

Scenario: Ignore configured paths
  Given workgraph is running
  And the settings ignore a local directory
  When a file changes inside that ignored directory
  Then workgraph does not record a user work event

Scenario: Ignore configured names
  Given workgraph is running
  And the settings ignore the name ".git"
  When a file changes under a path segment named ".git"
  Then workgraph does not record a user work event

Scenario: Infer project from a git repository
  Given workgraph is running
  And a file changes inside a git repository
  When workgraph records the file event
  Then the event project is the repository name

Scenario: Infer project from folder when git is unavailable
  Given workgraph is running
  And a file changes outside a git repository
  When workgraph records the file event
  Then the event project is inferred from the nearest project folder

Scenario: Preserve source details for debugging
  Given workgraph captures an operational signal
  When the signal is stored as an event
  Then the event payload preserves source-specific details
