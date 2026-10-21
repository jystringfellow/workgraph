Feature: Resume work

Scenario: Show where personal work last stopped
  Given workgraph captured personal work across several projects within the last week
  And unrelated GitHub issue snapshots were updated more recently
  And a newly cloned repository produced file creation events
  When I run "workgraph resume"
  Then I see the latest personal work session with its project and evidence
  And the GitHub snapshots do not move the handoff forward
  And the new clone does not move the handoff forward
  And I see how to inspect that project

Scenario: Find personal work through per-repository git identity
  Given my global git user.email is empty and each repository sets its own identity
  And workgraph captured my commits and commits from other authors
  When I run "workgraph resume"
  Then I see my latest commit session
  And commits from other authors do not move the handoff forward

Scenario: Say when git identity is unknown
  Given no git user.email can be found globally or in captured repositories
  When I run "workgraph resume"
  Then captured commits count as personal work
  And I see that the git identity is unknown

Scenario: Preserve context after a longer absence
  Given the latest personal work was more than a week ago
  When I run "workgraph resume"
  Then I see that last known session labeled older than a week

Scenario: Show personal work since the previous check-in
  Given I successfully ran plain "workgraph resume" before
  And workgraph has since captured personal work
  And workgraph has captured an explicitly relevant update and an unclassified repository issue
  When I run "workgraph resume"
  Then I see the previous check-in time and the new personal work
  And I see the explicitly relevant update separately
  And I do not see the unclassified repository issue in the handoff
  And the new check-in is recorded for the next run

Scenario: Keep diagnostic and project inspections from moving the check-in
  Given I successfully ran plain "workgraph resume" before
  When I run "workgraph resume <project>" or a diagnostic overview
  Then the default overview's check-in remains at its previous time

Scenario: List resumable projects
  Given workgraph has captured events for multiple projects
  When I run "workgraph resume"
  Then I see projects ordered by recent activity
  And I see how many events each project has
  And I see how to resume a specific project

Scenario: Cap the default resumable project list
  Given workgraph has personal activity in more than ten projects in the last 14 days
  And older projects with captured events
  When I run "workgraph resume"
  Then I see the ten most recently active projects from the last 14 days
  And I see how many more projects "workgraph resume --all" would show
  When I run "workgraph resume --all"
  Then I see every project with captured events

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
