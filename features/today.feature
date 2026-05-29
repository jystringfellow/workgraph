Feature: Today view

Scenario: Show work from the current local day
  Given workgraph has captured events today
  When I run "workgraph today"
  Then I see events from the current local day
  And I do not see events from previous local days

Scenario: Show an empty state
  Given workgraph has captured no events today
  When I run "workgraph today"
  Then the output includes a "Today" section
  And the output says no activity has been captured yet

Scenario: Group today's work into sessions
  Given workgraph has captured multiple events today
  When I run "workgraph today"
  Then I see events grouped into time-based sessions

Scenario: Group sessions by project
  Given workgraph has captured events for multiple projects today
  When I run "workgraph today"
  Then I see sessions grouped by project

Scenario: Show GitHub activity with useful labels
  Given workgraph has captured GitHub pull request and issue events today
  When I run "workgraph today"
  Then GitHub pull requests include title, number, and state
  And GitHub issues include title, number, and state

Scenario: Show predictable output sections
  Given workgraph has captured events today
  When I run "workgraph today"
  Then the output includes a "Today" section
  And the output includes a "Projects" section when projects are known
  And the output includes a "Sessions" section when sessions are known
  And the output includes unfinished work when known

Scenario: Keep output simple for Phase 0
  Given workgraph has captured events today
  When I run "workgraph today"
  Then the output is plain text
  And the output does not require an LLM
