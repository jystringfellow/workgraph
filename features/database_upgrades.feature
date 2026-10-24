Feature: Database upgrades and concurrent access

Scenario: Start waits while the database is upgraded
  Given workgraph has been initialized
  And another workgraph process is migrating the database
  When I run "workgraph start"
  Then I see "Upgrading database…"
  And start reports capture started after the migration finishes

Scenario: Status reports a worker that is still starting
  Given a capture worker is waiting for a database migration
  When I run "workgraph status"
  Then I see that capture is starting and upgrading the database

Scenario: Start reports a slow upgrade truthfully
  Given a live capture worker is still upgrading the database after start's wait limit
  When "workgraph start" stops waiting
  Then it says capture is still starting and to check "workgraph status"
  And it exits with code 75 without claiming the start failed

Scenario: Start fails promptly when the worker exits
  Given the capture worker exits during startup
  When I run "workgraph start"
  Then start fails at once with "daemon did not become ready"
  And the error points at the daemon log

Scenario: Concurrent starts share one worker
  Given another workgraph process is migrating the database
  When two "workgraph start" commands run at the same time
  Then both succeed and report the same capture worker

Scenario: Starting state is never trusted from an unrelated process
  Given the startup heartbeat names a PID that is not a capture worker
  When I run "workgraph status" or "workgraph stop"
  Then capture is reported as not running
  And the unrelated process is not signalled

Scenario: Short commands do not fail during a migration
  Given another workgraph process is migrating a database with an older capture request shape
  When I run "workgraph today" or "workgraph resume"
  Then it reads without waiting and reports the upgrade in progress
  And "workgraph connectors status" says bridged capture state is unknown
  When a connector poll runs
  Then it waits for the migration and then succeeds

Scenario: Listings read a current schema while another process prepares it
  Given another process holds the migration lock on a current database
  When I list capture requests
  Then the pending requests are listed normally

Scenario: Brief write contention retries
  Given another process holds a short write transaction
  When a connector poll runs
  Then it retries and succeeds without "database is locked"

Scenario: The database uses write-ahead logging
  Given a new or older rollback-journal database
  When workgraph opens it
  Then the database is in WAL mode
  And doctor reports the journal mode
  And read-only access still works

Scenario: Database files stay user-only
  Given an existing database and WAL files with broad permissions
  When workgraph opens the database
  Then the database and its WAL files are readable only by the user

Scenario: File capture ignores database files
  Given the database lives under a watched directory
  When capture records file activity
  Then no events are recorded for the database or its sidecar files
