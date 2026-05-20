package facts

import "testing"

func TestFileEventInfersProjectFromNearestGitRoot(t *testing.T) {
	t.Skip("TBD: file events prefer the nearest enclosing git repository for project inference")
}

func TestFileEventFallsBackToConfiguredWatchRoot(t *testing.T) {
	t.Skip("TBD: file events outside git repositories infer project from the configured watch root")
}

func TestFileEventPreservesArtifactPath(t *testing.T) {
	t.Skip("TBD: file event payload preserves the changed file path as artifact identity")
}

func TestAssociatedSessionsUseProjectAndTime(t *testing.T) {
	t.Skip("TBD: event sessions group nearby events from the same project deterministically")
}
