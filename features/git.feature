Feature: Git integration

Scenario: Capture local git commits
  Given WorkGraph has been initialized
  And a configured watch root contains a git repository with a commit
  When I run "workgraph git capture"
  Then WorkGraph stores a git commit event
  And the event project is the repository name
  And the event payload includes the commit SHA, branch, subject, author, and repository path

Scenario: Capture git commits while run is active
  Given WorkGraph is running
  And a configured watch root contains a git repository
  When I create a local git commit
  Then WorkGraph stores a git commit event without a manual capture command

Scenario: Avoid duplicate git commit events
  Given WorkGraph already captured a local git commit
  When I run "workgraph git capture" again
  Then WorkGraph does not create a duplicate event for that commit
