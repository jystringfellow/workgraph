Feature: Active memory

Scenario: Initialize the local memory repo
  Given WorkGraph has not been initialized
  When I run "workgraph init"
  Then the local memory repo exists at "~/workgraph-memory"

Scenario: Preserve user-maintained memory files
  Given WorkGraph has already been initialized
  And the memory repo contains user-maintained files
  When I run "workgraph init"
  Then existing memory files are preserved

Scenario: Treat memory as user-owned context
  Given WorkGraph has access to local memory files
  When WorkGraph uses memory for context
  Then memory supports explanations and suggestions
  And captured events remain the source of truth for behavior
