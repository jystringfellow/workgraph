Feature: Projects and categorization

Scenario: Group projects under a workstream
  Given project memory for "mmo-gtm-segments" and "mmo-harness" declares workstream "mmo"
  And project memory for "ask-kai" has no frontmatter
  When I run "workgraph projects list"
  Then "mmo-gtm-segments" and "mmo-harness" are listed under "mmo"
  And "ask-kai" is listed without a workstream

Scenario: Link a Slack thread by a shared identifier
  Given a Slack channel is mapped to project "mmo-harness"
  And a thread in that channel cites a pull request recorded in "ask-kai" memory
  When workgraph links units of work
  Then the thread is linked to "ask-kai" by the shared identifier rule
  And the link is marked auto and can be undone

Scenario: Preview categorization without sending anything
  Given LLM categorization is enabled for Slack but not for Slack DMs
  When I run "workgraph llm associate --dry-run"
  Then workgraph prints the exact payload with channel units and no DM units
  And no request is sent to the model

Scenario: Apply only confident categorizations
  Given the model returns one unit at confidence 0.97 and another at 0.80
  When workgraph stores the results
  Then the first unit is linked automatically and marked auto
  And the second unit is proposed for review

Scenario: Never auto-apply a new project
  Given the model proposes a new project at confidence 0.99
  When workgraph stores the result
  Then the proposal waits for review

Scenario: Pause auto-apply when accuracy drops
  Given I corrected more than 5 percent of recent auto-applied links
  When the next categorization runs
  Then every result is proposed for review
  And "workgraph projects review" says auto-apply is paused and why

Scenario: Decide the uncertain items at the end of the day
  Given three units await a categorization decision
  When I run "workgraph next"
  Then a Categorize line shows three awaiting a decision and the count auto-applied today
  When I approve one, reassign one, and reject one in "workgraph projects review"
  Then the Categorize line is gone and no event was rewritten

Scenario: Start the morning with overnight work sorted
  Given agents opened pull requests overnight and the nightly categorization ran
  When I run "workgraph resume"
  Then activity since my last check-in is grouped by workstream and project
  And the remaining decisions are counted
