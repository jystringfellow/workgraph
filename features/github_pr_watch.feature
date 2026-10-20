Feature: GitHub pull request watch

Scenario: Connect the pull request watch through the GitHub CLI
  Given the GitHub CLI is authenticated
  When I run "workgraph github prs connect"
  Then workgraph validates the local GitHub CLI authentication
  And workgraph records the resolved GitHub login without storing a token
  And github.pr_watch is enabled for shared connector polling
  And workgraph reports how to disable or change github.pr_watch polling

Scenario: Connecting GitHub participant capture does not enable the watch
  Given the GitHub CLI is authenticated
  When I run "workgraph github connect"
  Then github.pr_watch remains disabled

Scenario: First sight of a pull request emits one observed event
  Given github.pr_watch is connected
  And I have open authored pull requests with existing reviews, comments, and check results
  When the first poll runs
  Then workgraph stores one github.pull_request.observed event per open authored pull request
  And each observed event includes the current review, check, and awaiting-me thread state
  And workgraph stores no per-comment, per-review, or check events for existing activity

Scenario: Comments made while observations were reset are not lost
  Given workgraph has observed my open pull request
  And the watch's observations are deleted
  When a collaborator comments and the next poll runs
  Then workgraph stores one github.pull_request.review_comment event for the comment
  And workgraph stores no second observed event

Scenario: Unchanged pull request state produces no events
  Given workgraph has observed my open pull request
  When a poll reports the same head commit and the same review, check, and thread state
  Then workgraph stores no new transition events

Scenario: Checks are reported once the whole run finishes
  Given workgraph has observed my open pull request with pending checks
  When one check fails while another is still running
  Then workgraph stores no check event
  When the remaining check finishes
  Then workgraph stores one github.pull_request.checks_failed event
  And "workgraph events today" shows the pull request title, number, and "checks failed" with the short head commit

Scenario: A new commit resets check state
  Given workgraph has observed failed checks on my pull request
  When a poll reports a new head commit with pending checks
  Then workgraph stores no check event
  When a poll reports passing checks for the new head commit
  Then workgraph stores exactly one github.pull_request.checks_passed event for the new head commit

Scenario: Failed checks carry the failing check names
  Given workgraph has observed my open pull request with pending checks
  When checks named "lint" and "unit" fail
  Then the github.pull_request.checks_failed event lists "lint" and "unit" with their details links

Scenario: A burst of comments between polls is captured
  Given workgraph has observed my open pull request
  When 150 conversation comments arrive between polls
  Then workgraph stores 150 github.pull_request.review_comment events

Scenario: Review bot feedback is captured and configured actors are silenced
  Given github.pr_watch ignores the actor "my-automation"
  When a poll reports review comments from me, from "my-automation", and from a review bot
  Then workgraph stores one github.pull_request.review_comment event for the review bot marked as a bot
  And workgraph stores no review_comment events for me or "my-automation"
  And workgraph stores one github.pull_request.author_replied event for my comment

Scenario: Agent replies are told apart from my own replies
  Given github.pr_watch uses the agent reply marker "[Robots on behalf of Stringfellow]"
  When an agent replies to a review thread as me and ends the reply with the marker
  Then workgraph stores one github.pull_request.author_replied event attributed to an agent
  When the reviewer replies to the agent
  Then the reviewer's comment records that the previous responder was an agent

Scenario: Review comment text is stored as untrusted data
  Given workgraph has observed my open pull request
  When a collaborator leaves a long review comment containing instructions and control characters
  Then workgraph stores the comment text only under an untrusted payload field
  And the comment body keeps its line structure and suggestion blocks with control characters removed
  And the body is truncated to the configured limit
  And the event summary, event label, and attention item do not contain the comment text

Scenario: Ready to merge is reported again after it is lost and regained
  Given my pull request is approved with passing checks and one unresolved thread
  When the thread is resolved
  Then workgraph stores one github.pull_request.ready_to_merge event for the head commit
  When the next poll reports the same state
  Then workgraph stores no additional ready_to_merge event
  When a reviewer opens a new unresolved thread
  Then workgraph stores one github.pull_request.readiness_lost event naming unresolved threads as the reason
  When that thread is resolved
  Then workgraph stores another github.pull_request.ready_to_merge event

Scenario: Merged pull requests stop being watched
  Given workgraph has observed my open pull request
  When the pull request is merged
  Then workgraph stores one github.pull_request.merged event
  And workgraph no longer watches that pull request

Scenario: Pull requests that silently leave search do not linger
  Given workgraph has observed my open pull request
  When the pull request stays open but is missing from search for six polls
  Then workgraph stops watching it
  And "workgraph connectors status" reports that it left search

Scenario: Other people's pull request activity is not my activity
  Given workgraph captured review comments, check results, and readiness changes on my pull requests today
  And I replied to one review thread myself
  When I run "workgraph today"
  Then only my reply counts as my activity
  And the review comments, check results, and readiness changes do not create sessions or projects
  When I run "workgraph events today --type github.pull_request.*"
  Then workgraph lists all of those pull request transition events

Scenario: Resume relevant updates show pull request changes that need me
  Given I checked in with "workgraph resume"
  And a reviewer left a comment awaiting my reply and checks failed on my pull request
  And checks passed on another pull request
  When I run "workgraph resume"
  Then "Relevant updates since last check-in" lists the review comment and the failed checks
  And it does not list the passing checks

Scenario: Rate-limited polls defer without losing state
  Given the GitHub CLI reports a low remaining GraphQL rate limit
  When github.pr_watch polling runs
  Then workgraph does not query pull requests
  And workgraph keeps the previous observations
  And "workgraph connectors status" shows when polling resumes

Scenario: Expired GitHub CLI login stops only this connector
  Given github.pr_watch is connected
  When the GitHub CLI reports bad credentials
  Then github.pr_watch setup state is error with reconnect guidance
  And other connectors and file capture keep running

Scenario: Pull requests link to local commits
  Given workgraph has captured a local git commit
  And my pull request's head commit is that local commit
  When I run "workgraph associations explain <pull-request-event-id>"
  Then workgraph shows the git commit as related

Scenario: Watched pull requests enrich captured GitHub pull request snapshots
  Given workgraph has captured my pull request through GitHub participant search
  When github.pr_watch observes that pull request
  Then the captured pull request snapshot includes its branch and head commit
  And the snapshot keeps its matched query provenance
  When GitHub participant search captures a newer version of the pull request
  Then the snapshot still includes its branch and head commit

Scenario: Review requests reach me directly and through my teams
  Given github.pr_watch watches review requests for me and my teams
  And one pull request requests my review directly
  And another requests a review from a team I belong to
  When a poll runs
  Then workgraph watches both pull requests in the reviewer role
  And the first is labeled as a direct request and the second names the team

Scenario: A new review request is a transition, not backlog
  Given github.pr_watch has completed its first reviewer poll
  When a new pull request requests my review
  Then workgraph stores a github.pull_request.review_requested event

Scenario: Reviewing a pull request keeps it watched for follow-up
  Given a pull request requests my review
  When I submit a review requesting changes
  Then workgraph stores github.pull_request.review_submitted and github.pull_request.review_request_removed events
  When the author pushes a new commit
  Then workgraph stores one github.pull_request.pushed_since_review event
  When the author requests my review again
  Then workgraph stores a new github.pull_request.review_requested event

Scenario: Only threads I am part of follow up on pull requests I review
  Given I reviewed a pull request and started one review thread
  When the author replies in my thread and comments in another thread
  Then workgraph stores a review_comment event for the reply in my thread
  And workgraph stores no event for the other thread

Scenario: Ignored teams and stale reviews drop out
  Given github.pr_watch ignores the team "acme/everyone"
  When a pull request requests a review only from "acme/everyone"
  Then workgraph does not watch it
  When a pull request I reviewed has not been updated within the reviewed window
  Then workgraph stops watching it without recording it as closed

Scenario: A push hint triggers a poll within seconds
  Given github.pr_watch polls every five minutes
  When I run "workgraph github prs hint"
  Then workgraph polls within seconds instead of waiting for the interval
  And the hint itself changes nothing that workgraph stores
