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

Scenario: Explain a strong cross-source association
  Given a GitHub event and a Slack event cite the same normalized URL
  When I explain associations for either event
  Then workgraph proposes one high-confidence baseline association
  And the suggestion cites both event ids and the identical URL reason

Scenario: Coalesce a canonical event pair
  Given two cross-source events have qualifying deterministic evidence
  When the pair is evaluated repeatedly and in either order
  Then workgraph keeps one suggestion with a stable id and canonical event order

Scenario: Reject weak or misleading candidates
  Given events are from the same source, use different repositories, have only
    nearby timestamps, or have only generic title text
  When workgraph evaluates association candidates
  Then no association suggestion is produced for those pairs

Scenario: Preserve association lifecycle feedback
  Given a user dismissed or snoozed an association suggestion
  When the same pair is evaluated again
  Then workgraph does not reopen the suggestion

Scenario: Bound local candidate selection
  Given more than 200 cross-source events exist in the seven-day window
  When I explain associations for one event
  Then only the nearest 200 candidates are evaluated deterministically
