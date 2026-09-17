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
  And a connected GitHub identity participates in pull requests and issues
  And the GitHub CLI reports pull request activity for that identity
  When GitHub polling runs
  Then workgraph stores the GitHub pull request event without a manual capture command
  And GitHub appears in shared connector status

Scenario: Skip GitHub polling when rate limit is low
  Given workgraph is running
  And the GitHub CLI reports a low remaining API rate limit
  When GitHub polling runs
  Then workgraph does not query participant activity
  And workgraph keeps capture running

Scenario: Capture participant-scoped activity across repositories
  Given a GitHub connection is approved with participant scope and identity "@me"
  When GitHub polling runs
  Then workgraph runs the involves and review-requested searches for that identity
  And workgraph stores matching pull request and issue activity regardless of which repository it is in

Scenario: Capture review-requested pull requests
  Given a GitHub connection includes the review_requested scope
  And the GitHub CLI reports a pull request requesting the identity's review
  When GitHub polling runs
  Then workgraph stores the review-requested pull request event
  And the stored event records review_requested in its matched query provenance

Scenario: Capture activity from an uncloned repository
  Given a GitHub connection is approved with participant scope
  And the GitHub CLI reports participant activity in a repository with no local clone
  When GitHub polling runs
  Then workgraph stores the GitHub event
  And the event falls back to the GitHub repository name when no local project can be inferred

Scenario: Bound the bootstrap and incremental capture windows
  Given a GitHub connection is approved with a configured bootstrap lookback
  And no capture cursor exists yet
  When GitHub polling runs
  Then workgraph queries a window bounded by the configured bootstrap lookback
  When GitHub polling runs again
  Then workgraph queries a window starting five minutes before the previous cursor

Scenario: Advance the watermark only after complete success
  Given a GitHub connection is approved with participant scope
  When a GitHub poll completes successfully
  Then workgraph advances the stored capture cursor to the poll's until time

Scenario: Watch explicit repositories regardless of participation
  Given a GitHub connection configures always_repositories with two repositories
  When GitHub polling runs
  Then workgraph runs at most one batched pull request search and one batched issue search covering both repositories
  And workgraph stores activity from those repositories even without participation

Scenario: Failed or truncated searches do not advance the watermark
  Given a GitHub connection is approved with participant scope
  And the GitHub CLI returns malformed search output
  When GitHub polling runs
  Then the poll fails
  And the stored capture cursor is unchanged

Scenario: Connect GitHub through the local GitHub CLI
  Given the GitHub CLI is authenticated
  When I run "workgraph github connect"
  Then workgraph validates the local GitHub CLI authentication
  And GitHub is enabled for shared connector polling
  And workgraph approves the default participant capture scope
  And workgraph reports how to disable or change GitHub polling
