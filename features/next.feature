Feature: Next

Scenario: See everything waiting on me in one place
  Given workgraph is watching my GitHub pull requests
  And a reviewer is waiting on my reply and another pull request requests my review
  And one of my pull requests has changes requested, another has failed checks, and another is ready to merge
  When I run "workgraph next"
  Then workgraph lists the items waiting on others first, then my blocked work, then work ready to finish
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

Scenario: Resume and today point at what is next
  Given workgraph has items waiting on me
  When I run "workgraph resume"
  Then a "Waiting on you" section lists the first five items after "Where you left off"
  When I run "workgraph today"
  Then today ends with the number of items waiting on me and still shows only my own activity

Scenario: Agents read the same items through MCP
  Given workgraph has items waiting on me
  When an agent calls the workgraph MCP next_items tool
  Then it receives the same items as "workgraph next --json"
  And the tool cannot change anything
