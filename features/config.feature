Feature: WorkGraph configuration

Scenario: Initialize default capture config
  Given WorkGraph has not been initialized
  When I run "workgraph init"
  Then WorkGraph creates a config file under the WorkGraph home
  And the config watches existing common user-facing folders
  And the config does not watch the entire home directory when common folders exist
  And the config ignores the WorkGraph home directory

Scenario: Store portable absolute paths
  Given WorkGraph has been initialized
  When I inspect the config file
  Then watch and ignore paths are absolute paths for the current operating system
  And the paths do not rely on shell expansion of "$HOME"

Scenario: Ignore user-configured paths
  Given WorkGraph has been initialized
  And the config ignores a local path
  When a file changes under that path
  Then WorkGraph does not record a user work event

Scenario: Ignore high-noise names
  Given WorkGraph has been initialized
  And the config ignores the name "node_modules"
  When a file changes under a directory named "node_modules"
  Then WorkGraph does not record a user work event

Scenario: Use config when no watch flag is provided
  Given WorkGraph has been initialized
  And the config contains watch directories
  When I run "workgraph run"
  Then WorkGraph watches the configured directories

Scenario: Let CLI watch flags override configured watch roots
  Given WorkGraph has been initialized
  And the config contains watch directories
  When I run "workgraph run --foreground --watch ./project"
  Then WorkGraph watches "./project" for that run
  And configured ignore rules still apply

Scenario: Add the current directory as a watch root
  Given WorkGraph has been initialized
  And I am inside a project outside my home directory
  When I run "workgraph config add-watch"
  Then the project directory is added to the front of watch_dirs
  And the config stores the project directory as an absolute path
  And running capture without "--watch" watches that project

Scenario: Add a specific directory as a watch root
  Given WorkGraph has been initialized
  When I run "workgraph config add-watch /path/to/project"
  Then "/path/to/project" is added to the front of watch_dirs
  And running the command again does not duplicate it
