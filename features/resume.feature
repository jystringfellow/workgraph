Feature: Resume work

Scenario: Resume a project from recent activity
  Given WorkGraph has captured file events for a project
  When I run "workgraph resume <project>"
  Then I see recent activity
  And I see relevant files
  And I see unfinished work if known
  And I see a suggested next step