Feature: Next

Scenario: See everything waiting on me in one place
  Given workgraph is watching my GitHub pull requests
  And a reviewer is waiting on my reply and another pull request requests my review
  And one of my pull requests has changes requested, another has failed checks, and another is ready to merge
  When I run "workgraph next"
  Then workgraph lists items where someone is waiting on me first, then my blocked work, then work ready to finish
  And each item shows its pull request, reason, and link
  And workgraph makes no GitHub request

Scenario: Bot conversation comments and agent-answered threads are not listed
  Given a coverage bot commented on my pull request's conversation
  And an agent answered a review thread as me with the agent reply marker
  When I run "workgraph next"
  Then neither the coverage comment nor the answered thread is listed
  When the reviewer replies to the agent
  Then "workgraph next" lists the thread again

Scenario: Items are machine readable without exposing comment text by default
  Given workgraph has items waiting on me
  When I run "workgraph next --json"
  Then each item has a stable id, its reason, the event that opened it, and its evidence
  And no item contains comment text, local paths, or tokens
  When I run "workgraph next --json --include-untrusted"
  Then comment text appears only in a separate untrusted field

Scenario: Mark an item done without hiding later feedback
  Given a review thread is waiting on my reply
  When I run "workgraph next done <item-id>"
  Then "workgraph next" no longer lists that item
  And workgraph writes nothing to GitHub
  When the reviewer adds another comment to the thread
  Then "workgraph next" lists the thread again

Scenario: Snooze an item
  Given a pull request requests my review
  When I run "workgraph next snooze <item-id> --for 2h"
  Then "workgraph next" hides the item for two hours
  And lists it again afterwards if it is still open

Scenario: Explain why an item is listed
  Given an item is waiting on me
  When I run "workgraph next explain <item-id>"
  Then workgraph shows the rule that opened it, the condition that still holds, and the evidence event ids

Scenario: Warn when the list may be out of date
  Given the pull request watch has not polled successfully for more than three intervals
  When I run "workgraph next"
  Then workgraph warns that the list may be out of date and how to fix it

Scenario: Follow item changes as they happen
  Given workgraph has items waiting on me
  When I run "workgraph next --watch --json"
  Then workgraph prints every open item followed by a synced marker
  When a poll opens one item and resolves another
  Then workgraph prints one opened line and one closed line within seconds
  When a snoozed item's snooze ends while no poll runs
  Then workgraph prints an opened line for that item

Scenario: Resume and today point at what is next
  Given workgraph has items waiting on me
  When I run "workgraph resume"
  Then a "Waiting on you" section lists the first five items after "Where you left off"
  When I run "workgraph today"
  Then today ends with the number of items waiting on me and still shows only my own activity

Scenario: Project link reviews do not drown what is waiting on me
  Given workgraph has items waiting on me and project links awaiting a decision
  When I run "workgraph today"
  Then "Waiting on you" counts only the items waiting on me
  And a separate line counts the project links to review by project and no project
  When I run "workgraph next"
  Then the project links appear as one summary line pointing at "workgraph projects review"
  And "workgraph next --json" still returns every item

Scenario: Team-only review requests do not bury direct work
  Given a large team I belong to has review requests open on many pull requests
  And other pull requests request my review directly or need my reply
  When I run "workgraph next"
  Then the direct items are listed under "Waiting on you" as before
  And each team's requests are summarized on one line with pull request, bot, and repository counts and three pull requests, human-authored ones first
  When I run "workgraph next --team-reviews"
  Then every team-only request is listed under its team, ordered by repository
  When I run "workgraph today"
  Then the waiting count excludes team-only requests and reports them separately

Scenario: My own fixes are grouped apart from work waiting on others
  Given one of my pull requests only has failing checks and another only has a merge conflict
  And another of my pull requests has changes requested by a reviewer
  When I run "workgraph next"
  Then the pull requests with failing checks or conflicts are listed under "Needs your fix"
  And the pull request with changes requested stays under "Blocked"
  And "Needs your fix" comes after "Waiting on you" and before "Blocked"
  And "workgraph next --json" gives each item its section

Scenario: Stale pull requests do not crowd the list
  Given several of my pull requests have had no activity for more than 30 days
  And an old pull request has a reply requested from me this week
  When I run "workgraph next"
  Then each section shows one line counting its stale pull requests and pointing at "workgraph next --stale"
  And the pull request with the recent reply request is still listed
  When I run "workgraph next --stale"
  Then the stale pull requests are listed in full
  And "workgraph next --json" still returns every item with a stale flag

Scenario: Team review previews show people before bots
  Given a team's review requests include pull requests by people and newer dependency bumps by bots
  When I run "workgraph next"
  Then the team summary previews the pull requests by people first

Scenario: Agents read the same items through MCP
  Given workgraph has items waiting on me
  When an agent calls the workgraph MCP next_items tool
  Then it receives the same items as "workgraph next --json"
  And the tool cannot change anything

Scenario: Provider display strings cannot control the terminal
  Given an item has ANSI escapes, control characters, or invisible format characters in provider strings
  When I run "workgraph next"
  Then those characters and escape sequences are stripped from item lines
  And JSON output preserves the original provider strings

Scenario: A broken next projection does not hide activity
  Given a stored pull request payload or observation is malformed
  When I run "workgraph today" or "workgraph resume"
  Then the command succeeds and shows its activity view
  And one short warning replaces the waiting items
