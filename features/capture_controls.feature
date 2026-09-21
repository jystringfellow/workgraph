Feature: Background capture controls

Scenario: Start background capture
  Given workgraph has been initialized
  And the settings contain watch directories
  When I run "workgraph start"
  Then workgraph starts capture without an attached terminal
  And capture state is written under the workgraph home

Scenario: Use default sane watch roots
  Given workgraph has been initialized with default settings
  When I run "workgraph start"
  Then workgraph watches existing common user-facing folders
  And ignored paths and names are still excluded

Scenario: Refuse before initialization
  Given workgraph has not been initialized
  When I run "workgraph start"
  Then the command exits with an error
  And the output tells me to run "workgraph init"

Scenario: Report capture status
  Given workgraph background capture is running
  When I run "workgraph status"
  Then I see that capture is running
  And I see the PID
  And I see watched directories
  And I see ignored paths and names
  And I see the daemon's running and on-disk build identities

Scenario: Warn when a running daemon pins an older executable
  Given workgraph background capture started before its executable was replaced
  When I run "workgraph status"
  Then I see that the running daemon binary is stale
  And I see how to stop and restart capture

Scenario: Commit background readiness state
  Given workgraph starts or restarts background capture
  When the command reports that capture is ready
  Then daemon state and the PID file identify the same running worker

Scenario: Preserve replacement state during a delayed shutdown
  Given a replacement capture worker becomes ready before the prior worker exits
  When the prior worker finishes shutting down
  Then daemon state and the PID file still identify the replacement worker
  And "workgraph status" reports that replacement capture is running

Scenario: Recover a live daemon whose state files are missing
  Given a background capture worker is alive
  And its daemon state and PID files are missing
  When I check status or start capture again
  Then workgraph identifies the existing worker
  And it does not launch a duplicate worker

Scenario: Stop reports only confirmed process exit
  Given one or more background capture workers match the workgraph home or database
  When I run "workgraph stop"
  Then workgraph signals every matching worker
  And it reports success only after every matching worker exits

Scenario: Stop after a background event burst
  Given background capture has recorded more events than its diagnostic output buffer holds
  When I run "workgraph stop"
  Then background capture exits after the termination signal
  And the captured events remain in the database

Scenario: Stop background capture
  Given workgraph background capture is running
  When I run "workgraph stop"
  Then background capture stops
  And events already written to the database are preserved

Scenario: Report stopped status
  Given workgraph background capture is not running
  When I run "workgraph status"
  Then I see that background capture is not running

Scenario: Report why background capture failed
  Given background capture exited after a fatal local capture error
  When I run "workgraph status"
  Then I see that background capture is not running
  And I see the last capture failure

Scenario: Run foreground capture for debugging
  Given workgraph has been initialized
  And the settings contain watch directories
  When I run "workgraph start --foreground"
  Then workgraph keeps capture attached to the current terminal
  And captured events are printed as they arrive

Scenario: Keep macOS background TLS available
  Given workgraph runs background capture on macOS
  When the "workgraph start" command returns
  Then the capture worker retains a live workgraph supervisor as its parent
  And HTTPS connectors continue using normal certificate verification
