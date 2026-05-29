Feature: Run workgraph

Scenario: Start event capture
  Given workgraph has been initialized
  When I run "workgraph run"
  Then workgraph starts watching configured local work activity in the background
  And the command reports that capture started

Scenario: Refuse to run before initialization
  Given workgraph has not been initialized
  When I run "workgraph run"
  Then the command exits with an error
  And the output tells me to run "workgraph init"

Scenario: Choose what to watch
  Given workgraph has been initialized
  When I run "workgraph run --foreground --watch ./project"
  Then workgraph watches file activity inside "./project"
  And workgraph stores events in the configured local database
  And configured ignore rules still apply

Scenario: Capture file activity while running
  Given workgraph is running
  When a file is created, modified, or deleted inside a project folder
  Then workgraph records a file event
  And the event can be queried later
  And the foreground command reports the captured event for debugging

Scenario: Capture editor safe-save as modification
  Given workgraph is running
  And an editor saves a document by writing a scratch file and replacing the original
  When capture processes the filesystem activity
  Then workgraph records a modification for the document
  And workgraph does not report the editor scratch file as user work

Scenario: Skip inaccessible folders
  Given workgraph is watching a directory
  And a descendant directory cannot be read by the current process
  When capture starts
  Then workgraph skips the inaccessible subtree
  And workgraph keeps watching the accessible root

Scenario: Skip unsupported special files
  Given workgraph is watching a directory
  And a descendant path is a socket or other unsupported special file
  When capture starts
  Then workgraph skips the unsupported path
  And workgraph keeps watching the accessible root

Scenario: Skip generated index directories
  Given workgraph is watching a directory
  And a descendant directory contains generated Apple index data
  When capture starts
  Then workgraph skips the generated index subtree
  And workgraph keeps watching the accessible root

Scenario: Keep file descriptors available
  Given workgraph is watching a large directory tree
  When recursive watch setup reaches its resource budget
  Then workgraph reports that the watch limit was reached
  And workgraph reports a sample of registered watch directories
  And workgraph reports the first directory outside the watch budget
  And workgraph keeps capture running for already watched directories

Scenario: Register configured roots before deep traversal
  Given workgraph is configured to watch multiple directories
  And one configured directory contains a large nested tree
  When capture starts with a limited watch budget
  Then each configured root gets a watcher before deep descendants are registered

Scenario: Prioritize user-facing folders
  Given workgraph is watching a home directory
  And the home directory contains both hidden caches and Desktop files
  When recursive watch setup has a limited resource budget
  Then workgraph watches Desktop before hidden cache subtrees

Scenario: Skip implicit top-level hidden directories
  Given workgraph is watching a broad local directory
  And the broad directory contains a top-level hidden cache directory
  When capture starts
  Then workgraph does not implicitly watch the hidden cache directory

Scenario: Explicitly watch a hidden directory
  Given workgraph is configured to watch a hidden directory
  When capture starts
  Then workgraph watches the hidden directory

Scenario: Traverse default roots conservatively
  Given workgraph is configured to watch a broad default folder
  And the default folder contains a nested folder-only app library
  When capture starts
  Then workgraph watches the default folder and its immediate child
  And workgraph does not recursively watch the app library's descendants

Scenario: Recurse into work-like folders under default roots
  Given workgraph is configured to watch a broad default folder
  And an immediate child folder contains ordinary documents
  When capture starts
  Then workgraph watches descendants of the work-like folder

Scenario: Recurse into explicit watch roots
  Given workgraph is configured to watch an explicit folder
  And the explicit folder contains nested folders
  When capture starts
  Then workgraph watches descendants of the explicit folder

Scenario: Skip generated build output under explicit watch roots
  Given workgraph is configured to watch an explicit code folder
  And the code folder contains source files and generated build output
  When capture starts
  Then workgraph watches the source folders
  And workgraph skips the generated build output folders

Scenario: Stop gracefully
  Given workgraph is running
  When I stop the foreground command with Ctrl+C or SIGTERM
  Then workgraph stops watching local work activity
  And events already written to the database are preserved

Scenario: Use configured watch roots by default
  Given workgraph has been initialized
  And the config watches existing common user-facing folders
  When I run "workgraph run"
  Then workgraph watches the configured directories
