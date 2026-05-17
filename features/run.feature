Feature: Run WorkGraph

Scenario: Start event capture
  Given WorkGraph has been initialized
  When I run "workgraph run"
  Then WorkGraph starts watching local work activity in the foreground
  And the command reports that capture is running

Scenario: Refuse to run before initialization
  Given WorkGraph has not been initialized
  When I run "workgraph run"
  Then the command exits with an error
  And the output tells me to run "workgraph init"

Scenario: Choose what to watch
  Given WorkGraph has been initialized
  When I run "workgraph run --watch ./project"
  Then WorkGraph watches file activity inside "./project"
  And WorkGraph stores events in the configured local database

Scenario: Capture file activity while running
  Given WorkGraph is running
  When a file is created, modified, or deleted inside a project folder
  Then WorkGraph records a file event
  And the event can be queried later
  And the foreground command reports the captured event for debugging

Scenario: Stop gracefully
  Given WorkGraph is running
  When I stop the foreground command with Ctrl+C or SIGTERM
  Then WorkGraph stops watching local work activity
  And events already written to the database are preserved

Scenario: Daemonized capture
  Given foreground capture works
  When I start background capture
  Then WorkGraph records local work activity without an attached terminal
  And I can check status or stop it explicitly
