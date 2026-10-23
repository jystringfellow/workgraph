Feature: Projects and categorization

Scenario: Group projects under a workstream
  Given project memory for "mpo-gtm-segments" and "mpo-harness" declares workstream "mpo"
  And project memory for "answer-bot" has no frontmatter
  When I run "workgraph projects list"
  Then "mpo-gtm-segments" and "mpo-harness" are listed under "mpo"
  And "answer-bot" is listed without a workstream

Scenario: Show project titles as plain text
  Given project memory for "mpo-terms-and-phrases" has the heading "MPO Terms &amp; Phrases"
  When I run "workgraph projects list"
  Then the project is listed as "MPO Terms & Phrases"
  And "workgraph llm associate --dry-run" sends the title "MPO Terms & Phrases"

Scenario: Keep terminal controls in project memory out of output
  Given project memory whose heading and description contain escape sequences, line breaks, or bidi overrides, written raw or as character references
  When I run "workgraph projects list"
  Then each title and description prints on its own line without control characters

Scenario: Group activity into units of work
  Given Slack, GitHub, git, Notion, and calendar events captured in any order
  When workgraph groups units of work
  Then the same unit ids and members are produced every time
  And a Slack burst inside its quiet gap is not eligible for categorization

Scenario: Keep a project with invalid frontmatter
  Given project memory for "broken" has frontmatter that is not closed
  When I run "workgraph projects list"
  Then "broken" is listed without a workstream
  And "workgraph memory doctor" reports the invalid frontmatter

Scenario: Map a container to a workstream
  Given captured events in "Mercury.Platform.Orchestration"
  When I run "workgraph memory link --workstream mpo --source Mercury.Platform.Orchestration"
  Then the container's catalog is narrowed to "mpo" projects
  And no project link is created
  And "workgraph projects list" shows the container under "mpo"

Scenario: Map a container to several projects
  Given captured Slack activity in "Acme.Platform.I18n"
  When I run "workgraph memory link --source Acme.Platform.I18n data-apis"
  And I run "workgraph memory link --source Acme.Platform.I18n mpo-terms-and-phrases"
  Then "workgraph llm associate --dry-run" offers only "data-apis" and "mpo-terms-and-phrases" for its units
  And no project link is created
  And "workgraph projects list" shows the container under both projects
  And "workgraph today" marks the container as mapped to both projects
  And "workgraph memory doctor" does not offer the container to another project
  When I also map the container to workstream "mpo"
  Then the dry run offers the workstream's projects and both mapped projects

Scenario: Keep projects named by a unit's identifiers when narrowing
  Given "Acme.Platform.I18n" is mapped to "data-apis" and "mpo-terms-and-phrases"
  And a thread there cites a pull request recorded in "mpo-terms-and-phrases" and "answer-bot" memory
  When I run "workgraph llm associate --dry-run"
  Then the thread is offered "answer-bot" as well as both mapped projects

Scenario: Suggest a workstream for a shared acronym
  Given memory exists for "mpo-harness"
  And captured events belong to "Mercury.Platform.Orchestration"
  When I run "workgraph memory doctor"
  Then the shared acronym is offered with a "memory link --workstream mpo" command

Scenario: Link a Slack thread by a shared identifier
  Given a Slack channel is mapped to project "mpo-harness"
  And a thread in that channel cites a pull request recorded in "answer-bot" memory
  When workgraph links units of work
  Then the thread is linked to "answer-bot" by the shared identifier rule
  And the link is marked auto and can be undone

Scenario: Fix a wrong automatic link with one key
  Given a deterministic and an LLM link were applied automatically
  When I run "workgraph projects review --auto"
  Then both links are listed with their run ids
  When I press "r" on one and enter "answer-bot"
  Then that unit is linked to "answer-bot" by me and the old link is corrected

Scenario: Explain how a unit is linked
  Given a unit was linked to "answer-bot" by me
  When I run "workgraph projects explain <unit-id>"
  Then the unit's members and every link row are listed
  And the reassign and reject commands are printed without an undo command

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

Scenario: Undo an automatic link
  Given a unit was linked to "mpo-harness" automatically
  When I run "workgraph projects undo <link-id>"
  Then the unit waits for review with "mpo-harness" proposed
  And later categorization runs do not link it automatically

Scenario: Review every automatic LLM link until there is enough history
  Given fewer than 20 automatic LLM links have been decided
  When I run "workgraph projects review"
  Then undecided automatic LLM links are listed beside proposed ones
  And deterministic links are not listed
  And the review ends by saying nothing is left to decide

Scenario: Pause auto-apply when accuracy drops
  Given I corrected more than 5 percent of recent auto-applied links
  When the next categorization runs
  Then every result is proposed for review
  And "workgraph projects review" says auto-apply is paused and why

Scenario: Keep auto-apply paused while the sample is small
  Given auto-apply was paused for low accuracy
  And fewer than 20 automatic links were decided in the last 14 days
  When the next categorization runs
  Then auto-apply stays paused and every result is proposed

Scenario: Discard invalid categorization results
  Given the model returns an unknown unit, an unknown project, foreign evidence, a bad kind, and an out-of-range confidence beside one valid entry
  When workgraph stores the results
  Then the invalid entries are discarded and counted
  And the valid entry is stored

Scenario: Schedule categorization only when there is work
  Given LLM categorization is enabled for Slack
  When the daemon checks the schedule with no uncategorized units
  Then no categorization request is emitted
  When uncategorized Slack units exist
  Then one "llm.associate" request is emitted
  And a capture worker claiming without a kind does not receive it

Scenario: Categorize in the daemon with a direct profile
  Given the associate route is a direct profile
  When the daemon emits a categorization request
  Then the daemon claims it as "daemon", calls the model, and writes links

Scenario: Categorize in the signed-in client bridge
  Given the associate route is an ai-client profile with bridge execution
  When the bridge worker claims the "llm_task" request and submits a result
  Then the result is stored only when the claim token is valid
  And no capture cursor or connector state changes

Scenario: Honor a source disabled after scheduling
  Given a categorization request was emitted with a Slack DM unit
  And Slack DMs were disabled before the request was claimed
  When the bridge worker claims the request
  Then the claimed payload omits the DM unit

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
