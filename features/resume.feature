Feature: Resume work

Scenario: List resumable projects
  Given WorkGraph has captured events for multiple projects
  When I run "workgraph resume"
  Then I see projects ordered by recent activity
  And I see how many events each project has
  And I see how to resume a specific project

Scenario: Resume a project from recent activity
  Given WorkGraph has captured file events for a project
  When I run "workgraph resume <project>"
  Then I see recent activity
  And I see relevant files
  And I see unfinished work if known
  And I see a suggested next step

Scenario: Prioritize recent evidence
  Given WorkGraph has captured old and recent events for a project
  When I run "workgraph resume <project>"
  Then the most recent activity appears first
  And older activity appears only when it is relevant
  And the activity list is capped with an older event count when needed

Scenario: Show predictable output sections
  Given WorkGraph has captured events for a project
  When I run "workgraph resume <project>"
  Then the output includes a "Recent activity" section
  And the output includes a "Relevant files" section
  And the output includes an "Open questions" section when evidence is incomplete
  And the output includes a "Suggested next step" section

Scenario: Avoid unsupported speculation
  Given WorkGraph has limited evidence for a project
  When I run "workgraph resume <project>"
  Then the output only includes claims supported by events, files, or memory
  And uncertain next steps are labeled as suggestions

Scenario: Show a missing project state
  Given WorkGraph has no events for a project
  When I run "workgraph resume <project>"
  Then the output says no recent activity was found
  And the output suggests checking the project name or running capture
