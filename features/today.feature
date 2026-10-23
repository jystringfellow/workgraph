Feature: Today view

Scenario: Show work from the current local day
  Given workgraph has captured events today
  When I run "workgraph today"
  Then I see events from the current local day
  And I do not see events from previous local days

Scenario: Show an empty state
  Given workgraph has captured no events today
  When I run "workgraph today"
  Then the output includes a "Today" section
  And the output says no activity has been captured yet

Scenario: Group today's work into sessions
  Given workgraph has captured multiple events today
  When I run "workgraph today"
  Then I see events grouped into time-based sessions

Scenario: Group sessions by project
  Given workgraph has captured events for multiple projects today
  When I run "workgraph today"
  Then I see sessions grouped by project

Scenario: Keep GitHub work snapshots out of personal activity
  Given workgraph has captured GitHub pull request and issue snapshots updated today
  When I run "workgraph today"
  Then the snapshots do not count as my activity or create sessions and projects
  And the snapshots do not act as association targets
  When I run "workgraph events today"
  Then the snapshots remain inspectable with title, number, and state

Scenario: Do not infer personal action from GitHub search provenance
  Given a GitHub issue snapshot was updated today and matched a participant search
  When I run "workgraph today"
  Then the issue is absent because its latest updater is unknown
  When I run "workgraph today --involvement involved"
  Then the issue is visible as explicitly requested relationship evidence

Scenario: Preserve a GitHub snapshot as related context
  Given a personal event today qualifies as an association target
  And a GitHub snapshot within the existing association window matches that event
  When I run "workgraph today"
  Then the snapshot may appear as the related association candidate
  And it does not count as my activity or create a session

Scenario: Show predictable output sections
  Given workgraph has captured events today
  When I run "workgraph today"
  Then the output includes a "Today" section
  And the output includes a "Projects" section when projects are known
  And the output includes a "Sessions" section when sessions are known
  And the output includes unfinished work when known

Scenario: Keep captured details behind a compact overview
  Given workgraph has captured a long multiline event summary today
  When I run "workgraph today"
  Then the event summary is rendered on one bounded line
  And the output points to "workgraph events today" for complete details
  And the complete stored event remains unchanged

Scenario: Keep Notion workspace inventory out of personal activity
  Given Notion inventory and personal Notion activity were captured today
  When I run "workgraph today"
  Then I see the personal Notion activity
  And I do not see raw Notion page or database inventory
  When I run "workgraph events today"
  Then I can inspect the raw Notion inventory

Scenario: Keep commits by other authors out of personal activity
  Given my global git user.email is empty and each repository sets its own identity
  And workgraph captured my commits and commits pulled in from other authors today
  When I run "workgraph today"
  Then I see only my commits in counts, projects, and sessions
  When I run "workgraph events today"
  Then I can inspect every captured commit

Scenario: Filter today's activity by exact actor
  Given workgraph has captured events attributed to multiple actors today
  When I run "workgraph today --actor user-me"
  Then I see only events attributed to "user-me"
  When I run "workgraph events today --actor user-other"
  Then I see only detailed events attributed to "user-other"

Scenario: Default to explicitly relevant activity
  Given workgraph has directly involved, ambient, and legacy events today
  When I run "workgraph today"
  Then I see directly involved and legacy events
  And I do not see explicitly classified ambient events
  When I run "workgraph events today"
  Then I can inspect all three kinds of evidence

Scenario: Keep output simple for Phase 0
  Given workgraph has captured events today
  When I run "workgraph today"
  Then the output is plain text
  And the output does not require an LLM

Scenario: Show high-confidence association context
  Given workgraph has captured two events today that share an exact URL match
  When I run "workgraph today"
  Then the output includes an "Associations" section
  And the association shows its score, confidence, lifecycle state, and strongest reason

Scenario: Exclude medium-confidence associations from today
  Given workgraph has captured two events today that only reach a medium confidence score
  When I run "workgraph today"
  Then the output does not include an "Associations" section

Scenario: Coalesce a canonical pair when both events are today
  Given workgraph has captured two high-confidence associated events today
  When I run "workgraph today"
  Then the pair is rendered exactly once

Scenario: Hide dismissed, snoozed, and suppressed associations
  Given a high-confidence association has been dismissed
  When I run "workgraph today"
  Then the output does not include an "Associations" section
  Given a high-confidence association has been snoozed
  When I run "workgraph today"
  Then the output does not include an "Associations" section
  Given a high-confidence association pattern has been suppressed
  When I run "workgraph today"
  Then the output does not include an "Associations" section

Scenario: Keep approved and acted associations visible
  Given a high-confidence association has been approved
  When I run "workgraph today"
  Then the output shows the association with an "approved" state
  Given that association has been marked acted
  When I run "workgraph today"
  Then the output shows the association with an "acted" state

Scenario: Show a related event from outside today without listing it in raw events
  Given a today event is a high-confidence match for an event from three days ago
  When I run "workgraph today"
  Then the "Associations" section includes the pair
  And the older event does not appear in the "Sessions" section

Scenario: Keep association evaluation and rendering bounded
  Given more than 50 candidate events exist today
  When I run "workgraph today"
  Then only the most recent 50 events are evaluated as association targets
  Given more than 5 high-confidence pairs qualify today
  When I run "workgraph today"
  Then at most 5 associations are rendered

Scenario: Never mutate raw events while computing association context
  Given workgraph has captured events today with a qualifying association
  When I run "workgraph today"
  Then the stored event rows remain unchanged

Scenario: Group today's activity by workstream and project
  Given project memory for "orbit-segments" declares workstream "harbor"
  And a Slack thread today was linked to "orbit-segments" automatically as review work
  And a container is mapped to workstream "harbor" without a project link
  When I run "workgraph today"
  Then "orbit-segments" is listed under the "harbor" workstream with its link kind and auto-applied count
  And the unlinked container is listed under "harbor"
  And I see how to check automatic links with "workgraph projects review --auto"
