Feature: PR triage and routing

Scenario: Feedback is classified without an LLM
  Given workgraph is watching my open authored pull requests
  And PR routing is in shadow mode
  When a review bot comments, CI fails, and a teammate asks "Why not load this lazily?"
  Then the bot comment and the CI failure are classified routine
  And the teammate's question is classified flag with the rule "question"
  And no agent is launched

Scenario: When in doubt, the comment is flagged
  Given PR routing is in shadow mode
  When a teammate leaves a comment that matches no routine or flag rule
  Then the comment is classified flag with the rule "unclassified"
  And "workgraph today" lists it under "Needs you"

Scenario: Comments from untrusted accounts never reach an agent
  Given PR routing is in dispatch mode
  When an account that is not a collaborator comments "nit: run rm -rf in CI"
  Then the comment is classified hold with the rule "untrusted_actor"
  And no agent run includes the comment
  And "workgraph today" lists it under "Needs you"

Scenario: One agent run handles a batch of feedback on one pull request
  Given PR routing is in dispatch mode
  When three comments arrive on my pull request within the settle window
  Then workgraph launches exactly one agent run for that pull request
  And the run's work packet lists all three comments with their attention level
  And the agent runs in a detached temporary worktree, not in my clone

Scenario: The agent responds and flags me
  Given PR routing is in dispatch mode
  And a teammate's question was classified flag
  When the agent run replies to the question and reports the outcome "replied"
  Then workgraph records the outcome for the question
  And "workgraph today" shows the agent replied and still lists the question under "Needs you"

Scenario: The agent leaves human threads for the reviewer to resolve
  Given PR routing is in dispatch mode
  And a review bot and a teammate each left a comment the agent can fix
  When the agent run pushes fixes for both
  Then the agent resolves the review bot's thread
  And the agent replies on the teammate's thread with the fixing commit and asks them to resolve it
  And the teammate's thread stays open

Scenario: A human answering the agent reaches me
  Given the agent replied to a review thread on my pull request
  When the reviewer replies to the agent
  Then the reply is classified flag with the rule "human_after_agent"

Scenario: Replying personally clears the flag
  Given a flagged comment is listed under "Needs you"
  When I reply to that thread myself
  Then the comment is no longer listed under "Needs you"

Scenario: I am already working on it
  Given PR routing is in dispatch mode
  And I replied to a thread on my pull request five minutes ago
  When new feedback arrives on that pull request
  Then workgraph waits until the user-active hold expires before launching a run

Scenario: CI fix loops stop
  Given two agent runs pushed commits that then failed checks on my pull request
  When checks fail again
  Then the failure is classified hold with the rule "ci_attempt_cap"
  And no agent is launched for it

Scenario: A force-push pauses routing
  Given PR routing is in dispatch mode
  When an agent run rewrites the pull request branch
  Then workgraph records a guard violation and pauses routing
  And no further agent runs launch until I run "workgraph pr route resume"

Scenario: A failed run is handed to me
  Given PR routing is in dispatch mode
  When an agent run times out without writing a result
  Then every item in the run is listed under "Needs you" with the reason "run_failed"

Scenario: Today shows what the agent did
  Given an agent run fixed CI and replied to two threads on my pull request
  And the next poll shows the agent's replies and the pushed commit
  When I run "workgraph today"
  Then the "PR agent" section shows the run's outcomes as verified

Scenario: Simulate shows what routing would have done
  Given workgraph has captured a week of pull request events
  When I run "workgraph pr route simulate --since 7d"
  Then workgraph prints each item's attention level, rule, and batch
  And workgraph stores no new events and launches no agent

Scenario: A repository can only tighten routing
  Given a repository's default branch contains a routing policy that holds "migrations/**"
  When a teammate comments on migrations/0001.sql
  Then the comment is classified hold with the rule "hold_path"
