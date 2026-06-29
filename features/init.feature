Feature: Initialize workgraph

Scenario: Create local workgraph home
  Given workgraph has not been initialized
  When I run "workgraph init"
  Then the local workgraph home exists at "~/.workgraph"

Scenario: Create the SQLite event database
  Given workgraph has not been initialized
  When I run "workgraph init"
  Then the SQLite database exists at "~/.workgraph/workgraph.db"
  And the database contains the active Phase 0 tables
  And the database has performance indices on events (timestamp, project, source, type)

Scenario: Create the local memory repo
  Given workgraph has not been initialized
  When I run "workgraph init"
  Then the local memory repo exists at "~/workgraph-memory"
  And the project memory directory exists at "~/workgraph-memory/projects"

Scenario: Create default settings
  Given workgraph has not been initialized
  When I run "workgraph init"
  Then the settings file exists at "~/.workgraph/settings.json"
  And the settings watch existing common user-facing folders
  And the settings do not watch the entire home directory when common folders exist
  And the settings ignore the workgraph home directory
  And the settings store resolved absolute paths

Scenario: Initialize safely more than once
  Given workgraph has already been initialized
  When I run "workgraph init"
  Then existing events are preserved
  And existing memory files are preserved
  And existing settings edits are preserved
  And the command exits successfully

Scenario: Force refresh default settings
  Given workgraph has already been initialized
  And the settings contain old or custom defaults
  When I run "workgraph init --force"
  Then the settings are replaced with the current defaults
  And existing events are preserved
  And existing memory files are preserved

Scenario: Report initialized paths
  Given workgraph has not been initialized
  When I run "workgraph init"
  Then the output includes the workgraph home path
  And the output includes the database path
  And the output includes the memory repo path
  And the output explains where project memory lives
  And the output includes the settings path

Scenario: Explain macOS folder access setup
  Given workgraph has not been initialized on macOS
  When I run "workgraph init"
  Then the output explains that macOS may prompt for protected folder access
  And the output suggests granting Full Disk Access once to avoid repeated prompts
