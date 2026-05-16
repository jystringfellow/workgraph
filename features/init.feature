Feature: Initialize WorkGraph

Scenario: Create local WorkGraph home
  Given WorkGraph has not been initialized
  When I run "workgraph init"
  Then the local WorkGraph home exists at "~/.workgraph"

Scenario: Create the SQLite event database
  Given WorkGraph has not been initialized
  When I run "workgraph init"
  Then the SQLite database exists at "~/.workgraph/workgraph.db"
  And the database contains the active Phase 0 tables

Scenario: Create the local memory repo
  Given WorkGraph has not been initialized
  When I run "workgraph init"
  Then the local memory repo exists at "~/workgraph-memory"

Scenario: Initialize safely more than once
  Given WorkGraph has already been initialized
  When I run "workgraph init"
  Then existing events are preserved
  And existing memory files are preserved
  And the command exits successfully

Scenario: Report initialized paths
  Given WorkGraph has not been initialized
  When I run "workgraph init"
  Then the output includes the WorkGraph home path
  And the output includes the database path
  And the output includes the memory repo path
