Feature: Today view

Scenario: Show work from the current local day
  Given WorkGraph has captured events today
  When I run "workgraph today"
  Then I see events from the current local day
  And I do not see events from previous local days

Scenario: Group today's work into sessions
  Given WorkGraph has captured multiple events today
  When I run "workgraph today"
  Then I see events grouped into time-based sessions

Scenario: Group sessions by project
  Given WorkGraph has captured events for multiple projects today
  When I run "workgraph today"
  Then I see sessions grouped by project

Scenario: Show predictable output sections
  Given WorkGraph has captured events today
  When I run "workgraph today"
  Then the output includes a "Today" section
  And the output includes project names when known
  And the output includes unfinished work when known
