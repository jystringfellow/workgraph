Feature: Run WorkGraph

Scenario: Start event capture
  Given WorkGraph has been initialized
  When I run "workgraph run"
  Then WorkGraph starts watching configured local work activity in the background
  And the command reports that capture started

Scenario: Refuse to run before initialization
  Given WorkGraph has not been initialized
  When I run "workgraph run"
  Then the command exits with an error
  And the output tells me to run "workgraph init"

Scenario: Choose what to watch
  Given WorkGraph has been initialized
  When I run "workgraph run --foreground --watch ./project"
  Then WorkGraph watches file activity inside "./project"
  And WorkGraph stores events in the configured local database
  And configured ignore rules still apply

Scenario: Capture file activity while running
  Given WorkGraph is running
  When a file is created, modified, or deleted inside a project folder
  Then WorkGraph records a file event
  And the event can be queried later
  And the foreground command reports the captured event for debugging

Scenario: Capture editor safe-save as modification
  Given WorkGraph is running
  And an editor saves a document by writing a scratch file and replacing the original
  When capture processes the filesystem activity
  Then WorkGraph records a modification for the document
  And WorkGraph does not report the editor scratch file as user work

Scenario: Skip inaccessible folders
  Given WorkGraph is watching a directory
  And a descendant directory cannot be read by the current process
  When capture starts
  Then WorkGraph skips the inaccessible subtree
  And WorkGraph keeps watching the accessible root

Scenario: Skip unsupported special files
  Given WorkGraph is watching a directory
  And a descendant path is a socket or other unsupported special file
  When capture starts
  Then WorkGraph skips the unsupported path
  And WorkGraph keeps watching the accessible root

Scenario: Skip generated index directories
  Given WorkGraph is watching a directory
  And a descendant directory contains generated Apple index data
  When capture starts
  Then WorkGraph skips the generated index subtree
  And WorkGraph keeps watching the accessible root

Scenario: Keep file descriptors available
  Given WorkGraph is watching a large directory tree
  When recursive watch setup reaches its resource budget
  Then WorkGraph reports that the watch limit was reached
  And WorkGraph reports a sample of registered watch directories
  And WorkGraph reports the first directory outside the watch budget
  And WorkGraph keeps capture running for already watched directories

Scenario: Prioritize user-facing folders
  Given WorkGraph is watching a home directory
  And the home directory contains both hidden caches and Desktop files
  When recursive watch setup has a limited resource budget
  Then WorkGraph watches Desktop before hidden cache subtrees

Scenario: Stop gracefully
  Given WorkGraph is running
  When I stop the foreground command with Ctrl+C or SIGTERM
  Then WorkGraph stops watching local work activity
  And events already written to the database are preserved

Scenario: Use configured watch roots by default
  Given WorkGraph has been initialized
  And the config watches existing common user-facing folders
  When I run "workgraph run"
  Then WorkGraph watches the configured directories
