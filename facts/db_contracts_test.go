package facts

import "testing"

func TestEventsTableExists(t *testing.T) {
	t.Skip("TBD: database has events table")
}

func TestEventsRequireIDSourceTypeTimestampPayloadCreatedAt(t *testing.T) {
	t.Skip("TBD: events require id, source, type, timestamp, payload_json, created_at")
}

func TestEventIDIsUnique(t *testing.T) {
	t.Skip("TBD: event id is unique")
}

func TestDatabaseEventPayloadMustBeValidJSON(t *testing.T) {
	t.Skip("TBD: event payload_json must be valid JSON")
}

func TestEventsCanBeQueriedByProject(t *testing.T) {
	t.Skip("TBD: events can be queried by project")
}

func TestEventsCanBeQueriedByTimeRange(t *testing.T) {
	t.Skip("TBD: events can be queried by time range")
}

func TestSessionsTableExists(t *testing.T) {
	t.Skip("TBD: database has sessions table")
}

func TestSessionStartMustBeBeforeOrEqualEnd(t *testing.T) {
	t.Skip("TBD: session started_at must be before or equal to ended_at")
}

func TestMemoryDocsTableExists(t *testing.T) {
	t.Skip("TBD: database has memory_docs table")
}

func TestMemoryDocPathIsUnique(t *testing.T) {
	t.Skip("TBD: memory_docs path is unique")
}

func TestDraftTablesDocumentFutureIntent(t *testing.T) {
	t.Skip("TBD: draft tables are documented but not required for Phase 0")
}
