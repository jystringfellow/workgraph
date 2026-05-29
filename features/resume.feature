Feature: Resume work

Scenario: List resumable projects
  Given workgraph has captured events for multiple projects
  When I run "workgraph resume"
  Then I see projects ordered by recent activity
  And I see how many events each project has
  And I see how to resume a specific project

Scenario: Resume a project from recent activity
  Given workgraph has captured file events for a project
  When I run "workgraph resume <project>"
  Then I see recent activity
  And I see relevant files
  And I see unfinished work if known
  And I see a suggested next step

Scenario: Prioritize recent evidence
  Given workgraph has captured old and recent events for a project
  When I run "workgraph resume <project>"
  Then the most recent activity appears first
  And older activity appears only when it is relevant
  And the activity list is capped with an older event count when needed

Scenario: Omit transient file evidence
  Given workgraph has captured durable project edits and transient local file churn
  When I run "workgraph resume <project>"
  Then the durable edits appear in recent activity
  And transient file paths do not appear in recent activity or relevant files

Scenario: Show known open GitHub work
  Given workgraph has captured open and closed GitHub work for a project
  When I run "workgraph resume <project>"
  Then open GitHub pull requests and issues appear in their own section
  And closed or merged GitHub work does not appear in that section
  And open GitHub work is not hidden by the recent activity cap

Scenario: Show predictable output sections
  Given workgraph has captured events for a project
  When I run "workgraph resume <project>"
  Then the output includes a "Recent activity" section
  And the output includes a "Relevant files" section
  And the output includes an "Open questions" section when evidence is incomplete
  And the output includes a "Suggested next step" section

Scenario: Avoid unsupported speculation
  Given workgraph has limited evidence for a project
  When I run "workgraph resume <project>"
  Then the output only includes claims supported by events, files, or memory
  And uncertain next steps are labeled as suggestions

Scenario: Show a missing project state
  Given workgraph has no events for a project
  When I run "workgraph resume <project>"
  Then the output says no recent activity was found
  And the output suggests checking the project name or running capture
