package facts

import "testing"

func TestFileWatcherRecordsFileChangeEvent(t *testing.T) {
	t.Skip("TBD: file watcher records file change events")
}

func TestFileWatcherIgnoresWorkGraphInternalFiles(t *testing.T) {
	t.Skip("TBD: file watcher ignores ~/.workgraph and generated database files")
}

func TestFileEventIncludesPathAndOperation(t *testing.T) {
	t.Skip("TBD: file events include path and operation such as created, modified, deleted")
}

func TestFileEventInfersProjectFromGitRepoOrFolder(t *testing.T) {
	t.Skip("TBD: file events infer project from git repo or parent folder")
}
