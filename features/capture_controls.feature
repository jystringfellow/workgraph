Feature: Background capture controls

Scenario: Start background capture
  Given WorkGraph has been initialized
  And the config contains watch directories
  When I run "workgraph run"
  Then WorkGraph starts capture without an attached terminal
  And capture state is written under the WorkGraph home

Scenario: Use default sane watch roots
  Given WorkGraph has been initialized with default config
  When I run "workgraph run"
  Then WorkGraph watches existing common user-facing folders
  And ignored paths and names are still excluded

Scenario: Refuse before initialization
  Given WorkGraph has not been initialized
  When I run "workgraph run"
  Then the command exits with an error
  And the output tells me to run "workgraph init"

Scenario: Report capture status
  Given WorkGraph background capture is running
  When I run "workgraph status"
  Then I see that capture is running
  And I see the PID
  And I see watched directories
  And I see ignored paths and names

Scenario: Stop background capture
  Given WorkGraph background capture is running
  When I run "workgraph stop"
  Then background capture stops
  And events already written to the database are preserved

Scenario: Report stopped status
  Given WorkGraph background capture is not running
  When I run "workgraph status"
  Then I see that background capture is not running

Scenario: Run foreground capture for debugging
  Given WorkGraph has been initialized
  And the config contains watch directories
  When I run "workgraph run --foreground"
  Then WorkGraph keeps capture attached to the current terminal
  And captured events are printed as they arrive
