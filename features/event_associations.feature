Feature: Event associations

Scenario: Prefer git repository project inference
  Given workgraph is watching a configured local directory
  And a file changes inside a git repository
  When workgraph records the file event
  Then the event project is the nearest git repository name

Scenario: Fall back to watched root project inference
  Given workgraph is watching a configured directory
  And a file changes outside a git repository
  When workgraph records the file event
  Then the event project is inferred from the configured watch root

Scenario: Preserve artifact identity
  Given workgraph records a file event
  When the event is stored
  Then the event payload includes the changed file path

Scenario: Associate events into sessions
  Given workgraph has captured multiple events for the same project
  When I query local work activity
  Then nearby events are grouped into deterministic sessions
