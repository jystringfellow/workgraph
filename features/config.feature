Feature: workgraph settings

Scenario: Initialize default capture settings
  Given workgraph has not been initialized
  When I run "workgraph init"
  Then workgraph creates a settings file under the workgraph home
  And the settings watch existing common user-facing folders
  And the default watch folders are marked for conservative traversal
  And the settings do not watch the entire home directory when common folders exist
  And the settings ignore the workgraph home directory

Scenario: Store portable absolute paths
  Given workgraph has been initialized
  When I inspect the settings file
  Then watch and ignore paths are absolute paths for the current operating system
  And the paths do not rely on shell expansion of "$HOME"

Scenario: Ignore user-configured paths
  Given workgraph has been initialized
  And the settings ignore a local path
  When a file changes under that path
  Then workgraph does not record a user work event

Scenario: Ignore high-noise names
  Given workgraph has been initialized
  And the settings ignore the name "node_modules"
  When a file changes under a directory named "node_modules"
  Then workgraph does not record a user work event

Scenario: Ignore generated build output by default
  Given workgraph has been initialized
  When I inspect the settings file
  Then the settings ignore common generated build directory names
  And the settings ignore Xcode user state directories

Scenario: Use settings when no watch flag is provided
  Given workgraph has been initialized
  And the settings contain watch directories
  When I run "workgraph start"
  Then workgraph watches the configured directories

Scenario: Let CLI watch flags override configured watch roots
  Given workgraph has been initialized
  And the settings contain watch directories
  When I run "workgraph start --foreground --watch ./project"
  Then workgraph watches "./project" for that run
  And configured ignore rules still apply

Scenario: Add the current directory as a watch root
  Given workgraph has been initialized
  And I am inside a project outside my home directory
  When I run "workgraph settings add-watch"
  Then the project directory is added to the front of watch_dirs
  And the settings store the project directory as an absolute path
  And running capture without "--watch" watches that project

Scenario: Add a specific directory as a watch root
  Given workgraph has been initialized
  When I run "workgraph settings add-watch /path/to/project"
  Then "/path/to/project" is added to the front of watch_dirs
  And "/path/to/project" is treated as an explicit watch root
  And running the command again does not duplicate it

Scenario: Respect admin-managed settings
  Given workgraph has been initialized
  And an admin-managed settings file disables hosted LLM providers
  And the local user settings enable a hosted LLM profile
  When I inspect the effective workgraph settings
  Then hosted LLM providers are disabled
  And the output explains that the value came from managed settings
  And workgraph does not expose secrets while reporting effective config
