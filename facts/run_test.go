package facts

import "testing"

func TestRunStartsEventCaptureAfterInit(t *testing.T) {
	t.Skip("TBD: run starts local event capture after workgraph init")
}

func TestRunRefusesBeforeInit(t *testing.T) {
	t.Skip("TBD: run exits with guidance when WorkGraph has not been initialized")
}

func TestRunCapturesFileActivity(t *testing.T) {
	t.Skip("TBD: run captures created, modified, and deleted file events")
}

func TestRunPreservesWrittenEventsWhenStopped(t *testing.T) {
	t.Skip("TBD: run preserves already-written events when stopped")
}
