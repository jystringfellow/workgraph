Feature: Explicit event involvement

Scenario: Keep actor and involvement separate
  Given another person replies in a thread where the connected user participated
  When workgraph stores the reply
  Then the other person remains the actor
  And thread_participation explains why the event is relevant to the user

Scenario: Show directly relevant work by default
  Given workgraph has classified direct and ambient evidence today
  When I run "workgraph today"
  Then directly involved events are shown
  And explicitly classified ambient evidence is hidden
  When I run "workgraph events today"
  Then both remain inspectable

Scenario: Preserve legacy evidence during migration
  Given an older event has no involvement classification
  When I run "workgraph today"
  Then the older event remains visible

Scenario: Filter by one exact involvement
  Given workgraph has assigned and review-requested events today
  When I run "workgraph today --involvement review_requested"
  Then only review-requested events are shown

Scenario: Normalize bridged involvement
  Given a bridged event declares supported involvement values
  When workgraph ingests the event
  Then involvement is sorted and de-duplicated
  And an unsupported involvement value is rejected
