Feature: GitHub authored pull request watch

Scenario: Connect the authored pull request watch through the GitHub CLI
  Given the GitHub CLI is authenticated
  When I run "workgraph github prs connect"
  Then workgraph validates the local GitHub CLI authentication
  And workgraph records the resolved GitHub login without storing a token
  And github.authored_prs is enabled for shared connector polling
  And workgraph reports how to disable or change github.authored_prs polling

Scenario: Connecting GitHub participant capture does not enable the watch
  Given the GitHub CLI is authenticated
  When I run "workgraph github connect"
  Then github.authored_prs remains disabled

Scenario: First sight of a pull request emits one observed event
  Given github.authored_prs is connected
  And I have open authored pull requests with existing reviews, comments, and check results
  When the first poll runs
  Then workgraph stores one github.pull_request.observed event per open authored pull request
  And each observed event includes the current review, check, and awaiting-author thread state
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
  And "workgraph today" shows the pull request title, number, and "checks failed" with the short head commit

Scenario: A new commit resets check state
  Given workgraph has observed failed checks on my pull request
  When a poll reports a new head commit with pending checks
  Then workgraph stores no check event
  When a poll reports passing checks for the new head commit
  Then workgraph stores exactly one github.pull_request.checks_passed event for the new head commit

Scenario: A burst of comments between polls is captured
  Given workgraph has observed my open pull request
  When 150 conversation comments arrive between polls
  Then workgraph stores 150 github.pull_request.review_comment events

Scenario: Review bot feedback is captured and configured actors are silenced
  Given github.authored_prs ignores the actor "my-automation"
  When a poll reports review comments from me, from "my-automation", and from a review bot
  Then workgraph stores one github.pull_request.review_comment event for the review bot marked as a bot
  And workgraph stores no review_comment events for me or "my-automation"
  And workgraph stores one github.pull_request.author_replied event for my comment

Scenario: Agent replies are told apart from my own replies
  Given github.authored_prs uses the agent reply marker "[Robots on behalf of Stringfellow]"
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
  And the event summary and today label do not contain the comment text

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

Scenario: List only pull request transitions
  Given workgraph captured pull request transitions and other GitHub events today
  When I run "workgraph events today --type github.pull_request.*"
  Then workgraph lists only github.pull_request transition events

Scenario: Today shows what is waiting on me
  Given workgraph is watching my open authored pull requests
  And one pull request has failed checks, another has changes requested, and another has a thread awaiting my reply
  And another pull request is ready to merge
  When I run "workgraph today"
  Then the "Waiting on you" section lists each of those items with its pull request and reason
  And workgraph makes no GitHub request

Scenario: Bot coverage comments do not wait on me
  Given a coverage bot commented on my pull request's conversation
  When I run "workgraph github prs waiting"
  Then the coverage comment is not listed

Scenario: Threads an agent answered leave the list
  Given a thread on my pull request was awaiting my reply
  When an agent replies to it as me with the agent reply marker
  Then "workgraph github prs waiting" no longer lists the thread
  When the reviewer replies again
  Then "workgraph github prs waiting" lists the thread again

Scenario: Resume shows what is waiting on me for a project
  Given workgraph has observed my open pull requests for a project
  When I run "workgraph resume <project>"
  Then workgraph shows each pull request with its review, check, and unresolved-thread state
  And workgraph shows the items waiting on me for those pull requests

Scenario: Waiting on you warns when it is stale
  Given github.authored_prs has not polled successfully for more than three intervals
  When I run "workgraph github prs waiting"
  Then workgraph warns that the list may be out of date

Scenario: Rate-limited polls defer without losing state
  Given the GitHub CLI reports a low remaining GraphQL rate limit
  When github.authored_prs polling runs
  Then workgraph does not query pull requests
  And workgraph keeps the previous observations
  And "workgraph connectors status" shows when polling resumes

Scenario: Expired GitHub CLI login stops only this connector
  Given github.authored_prs is connected
  When the GitHub CLI reports bad credentials
  Then github.authored_prs setup state is error with reconnect guidance
  And other connectors and file capture keep running

Scenario: Pull requests link to local commits
  Given workgraph has captured a local git commit
  And my pull request's head commit is that local commit
  When I run "workgraph associations explain <pull-request-event-id>"
  Then workgraph shows the git commit as related

Scenario: Watched pull requests enrich captured GitHub pull request snapshots
  Given workgraph has captured my pull request through GitHub participant search
  When github.authored_prs observes that pull request
  Then the captured pull request snapshot includes its branch and head commit
  And the snapshot keeps its matched query provenance
  When GitHub participant search captures a newer version of the pull request
  Then the snapshot still includes its branch and head commit

Scenario: Failed checks carry the failing check names
  Given workgraph has observed my open pull request with pending checks
  When checks named "lint" and "unit" fail
  Then the github.pull_request.checks_failed event lists "lint" and "unit" with their details links
