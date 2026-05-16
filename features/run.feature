Feature: Run WorkGraph

Scenario: Start event capture
  Given WorkGraph has been initialized
  When I run "workgraph run"
  Then WorkGraph starts watching local work activity
  And the command reports that capture is running

Scenario: Refuse to run before initialization
  Given WorkGraph has not been initialized
  When I run "workgraph run"
  Then the command exits with an error
  And the output tells me to run "workgraph init"

Scenario: Capture file activity while running
  Given WorkGraph is running
  When a file is created, modified, or deleted inside a project folder
  Then WorkGraph records a file event
  And the event can be queried later

Scenario: Stop gracefully
  Given WorkGraph is running
  When I stop the command
  Then WorkGraph stops watching local work activity
  And events already written to the database are preserved
