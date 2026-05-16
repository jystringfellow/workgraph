package facts

import "testing"

// Domain entity facts cover WorkGraph concepts before storage.

func TestEventRequiresIDSourceTypeTimestampPayload(t *testing.T) {
	t.Skip("TBD: event requires id, source, type, timestamp, and payload_json")
}

func TestEventCanIncludeProjectActorAndSummary(t *testing.T) {
	t.Skip("TBD: event may include project, actor, and summary metadata")
}

func TestEventPayloadMustBeValidJSON(t *testing.T) {
	t.Skip("TBD: event payload_json must be valid JSON")
}

func TestEventTimestampUsesRFC3339(t *testing.T) {
	t.Skip("TBD: event timestamp uses RFC3339")
}

func TestSessionRequiresIDStartedAtEndedAt(t *testing.T) {
	t.Skip("TBD: session requires id, started_at, and ended_at")
}

func TestDomainSessionStartMustBeBeforeOrEqualEnd(t *testing.T) {
	t.Skip("TBD: session started_at must be before or equal to ended_at")
}

func TestMemoryDocRequiresIDPathKindContentUpdatedAt(t *testing.T) {
	t.Skip("TBD: memory doc requires id, path, kind, content, and updated_at")
}

func TestMemoryDocUpdatedAtUsesRFC3339(t *testing.T) {
	t.Skip("TBD: memory doc updated_at uses RFC3339")
}
