Feature: GitHub integration

Scenario: Capture GitHub pull request activity
  Given workgraph has a GitHub event export with pull request activity
  When I run "workgraph github capture --events-file github-events.json"
  Then workgraph stores a GitHub pull request event
  And the event includes repository, PR number, URL, state, actor, and title

Scenario: Link GitHub activity to a local project by remote
  Given workgraph watches a local git repository
  And the repository remote points at a GitHub repository
  When workgraph captures GitHub activity for that repository
  Then the GitHub event project is the local repository project name

Scenario: Link GitHub activity to a local project by commit
  Given workgraph has captured a local git commit
  And GitHub activity references that commit SHA
  When workgraph captures the GitHub activity
  Then the GitHub event project matches the local git commit project

Scenario: Capture GitHub issues
  Given workgraph has a GitHub event export with issue activity
  When I run "workgraph github capture --events-file github-events.json"
  Then workgraph stores a GitHub issue event
  And the event includes repository, issue number, URL, state, actor, and title

Scenario: Refresh GitHub work state
  Given workgraph already captured open GitHub work
  And GitHub reports a newer closed state for that work
  When workgraph captures the GitHub activity again
  Then the stored GitHub event state is refreshed
  And workgraph does not create a duplicate GitHub event

Scenario: Capture GitHub activity while run is active
  Given workgraph is running
  And a configured local git repository has a GitHub remote
  And the GitHub CLI reports pull request activity for that repository
  When GitHub polling runs
  Then workgraph stores the GitHub pull request event without a manual capture command
  And GitHub appears in shared connector status

Scenario: Skip GitHub polling when rate limit is low
  Given workgraph is running
  And the GitHub CLI reports a low remaining API rate limit
  When GitHub polling runs
  Then workgraph does not query repository activity
  And workgraph keeps capture running

Scenario: Bound GitHub polling work per tick
  Given workgraph is watching many local repositories with GitHub remotes
  When GitHub polling runs
  Then workgraph queries only a bounded number of repositories
  And workgraph keeps capture running

Scenario: Connect GitHub through the local GitHub CLI
  Given the GitHub CLI is authenticated
  When I run "workgraph github connect"
  Then workgraph validates the local GitHub CLI authentication
  And GitHub is enabled for shared connector polling
  And workgraph reports how to disable or change GitHub polling
