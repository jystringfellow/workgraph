Feature: GitHub integration

Scenario: Capture GitHub pull request activity
  Given WorkGraph is connected to GitHub
  And a repository has pull request activity
  When I run "workgraph github capture"
  Then WorkGraph stores a GitHub pull request event
  And the event includes repository, PR number, URL, state, actor, and title

Scenario: Link GitHub activity to a local project by remote
  Given WorkGraph watches a local git repository
  And the repository remote points at a GitHub repository
  When WorkGraph captures GitHub activity for that repository
  Then the GitHub event project is the local repository project name

Scenario: Link GitHub activity to a local project by commit
  Given WorkGraph has captured a local git commit
  And GitHub activity references that commit SHA
  When WorkGraph captures the GitHub activity
  Then the GitHub event project matches the local git commit project

Scenario: Capture GitHub issues
  Given WorkGraph is connected to GitHub
  And a repository has issue activity
  When I run "workgraph github capture"
  Then WorkGraph stores a GitHub issue event
  And the event includes repository, issue number, URL, state, actor, and title
