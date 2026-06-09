Feature: Git integration

Scenario: Connect local git capture
  Given workgraph has been initialized
  When I run "workgraph git connect"
  Then git is enabled for shared connector polling
  And workgraph reports how to disable or change git polling

Scenario: Capture local git commits
  Given workgraph has been initialized
  And a configured watch root contains a git repository with a commit
  When I run "workgraph git capture"
  Then workgraph stores a git commit event
  And the event project is the repository name
  And the event payload includes the commit SHA, branch, subject, author, and repository path

Scenario: Capture git commits while run is active
  Given workgraph is running
  And a configured watch root contains a git repository
  When I create a local git commit
  Then workgraph stores a git commit event without a manual capture command

Scenario: Avoid duplicate git commit events
  Given workgraph already captured a local git commit
  When I run "workgraph git capture" again
  Then workgraph does not create a duplicate event for that commit
