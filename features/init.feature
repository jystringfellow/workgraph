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

Scenario: Create default config
  Given WorkGraph has not been initialized
  When I run "workgraph init"
  Then the config file exists at "~/.workgraph/config.json"
  And the config watches existing common user-facing folders
  And the config does not watch the entire home directory when common folders exist
  And the config ignores the WorkGraph home directory
  And the config stores resolved absolute paths

Scenario: Initialize safely more than once
  Given WorkGraph has already been initialized
  When I run "workgraph init"
  Then existing events are preserved
  And existing memory files are preserved
  And existing config edits are preserved
  And the command exits successfully

Scenario: Force refresh default config
  Given WorkGraph has already been initialized
  And the config contains old or custom defaults
  When I run "workgraph init --force"
  Then the config is replaced with the current default config
  And existing events are preserved
  And existing memory files are preserved

Scenario: Report initialized paths
  Given WorkGraph has not been initialized
  When I run "workgraph init"
  Then the output includes the WorkGraph home path
  And the output includes the database path
  And the output includes the memory repo path
  And the output includes the config path

Scenario: Explain macOS folder access setup
  Given WorkGraph has not been initialized on macOS
  When I run "workgraph init"
  Then the output explains that macOS may prompt for protected folder access
  And the output suggests granting Full Disk Access once to avoid repeated prompts
