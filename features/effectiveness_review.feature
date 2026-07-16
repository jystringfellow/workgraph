Feature: Local suggestion feedback and effectiveness review

Scenario: Snooze a suggestion until a deterministic instant
  Given workgraph has a proposed suggestion
  When I snooze the suggestion until a future RFC3339 instant
  Then the suggestion is snoozed
  And append-only snooze feedback and the expiring suppression are stored atomically

Scenario: Complete an approved suggestion
  Given workgraph has an approved suggestion
  When I mark the suggestion complete
  Then the suggestion is acted
  And append-only completed feedback is stored without repeating the approved action

Scenario: Review the current local week
  Given local suggestion feedback exists in and outside the current local week
  When I run "workgraph review"
  Then rates use only disposition events in the inclusive-start exclusive-end window
  And unavailable quality metrics are reported as insufficient data

Scenario: Review rolling local history as JSON
  Given local suggestion feedback and connector runtime state exist
  When I run "workgraph review --since 7d --format json"
  Then JSON reports the same metric values and connector states as text

Scenario: Review thirty rolling days
  Given local suggestion feedback exists in the last thirty days
  When I run "workgraph review --since 30d"
  Then the window is exactly 720 hours ending at the review instant

Scenario: Report useful suggestion latency
  Given suggestions have first accepted or completed feedback in the review window
  When I review local effectiveness
  Then time-to-useful is the deterministic median whole-second creation-to-feedback duration

Scenario: Report connector freshness and degradation
  Given connected connectors have local success and failure state
  When I review local effectiveness
  Then freshness is based on last success and configured polling interval
  And degraded state remains visible after a failed poll
