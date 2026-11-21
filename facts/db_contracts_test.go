package facts

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestEventsTableExists(t *testing.T) {
	db := openContractDatabase(t)

	var name string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'events'`).Scan(&name); err != nil {
		t.Fatalf("expected events table to exist: %v", err)
	}
	if name != "events" {
		t.Fatalf("expected events table, got %q", name)
	}
}

func TestEventsTableHasRequiredColumns(t *testing.T) {
	db := openContractDatabase(t)

	columns := tableColumns(t, db, "events")
	for _, column := range []string{"id", "source", "type", "timestamp", "payload_json", "created_at"} {
		if !columns[column] {
			t.Fatalf("expected events table to have column %q, got %#v", column, columns)
		}
	}
}

func TestEventsTableEnforcesUniqueID(t *testing.T) {
	db := openContractDatabase(t)
	insertContractEvent(t, db, "duplicate-event", "Cupcake", time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC))

	_, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"duplicate-event",
		"file",
		"file.modified",
		"2026-05-25T12:01:00Z",
		`{"path":"README.md"}`,
		"2026-05-25T12:01:00Z",
	)
	if err == nil {
		t.Fatalf("expected duplicate event id to be rejected")
	}
}

func TestEventsTableRejectsInvalidPayloadJSON(t *testing.T) {
	db := openContractDatabase(t)

	_, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"invalid-json",
		"file",
		"file.modified",
		"2026-05-25T12:00:00Z",
		`not-json`,
		"2026-05-25T12:00:00Z",
	)
	if err == nil {
		t.Fatalf("expected invalid payload_json to be rejected")
	}
}

func TestEventsCanBeInsertedAndReadFromDatabase(t *testing.T) {
	db := openContractDatabase(t)
	insertContractEvent(t, db, "readable-event", "Cupcake", time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC))

	var eventType string
	var payload string
	if err := db.QueryRow(`SELECT type, payload_json FROM events WHERE id = ?`, "readable-event").Scan(&eventType, &payload); err != nil {
		t.Fatalf("read event from database: %v", err)
	}
	if eventType != "file.modified" || payload != `{"path":"README.md"}` {
		t.Fatalf("expected stored event values, got type %q payload %q", eventType, payload)
	}
}

func TestEventsCanBeQueriedByProject(t *testing.T) {
	db := openContractDatabase(t)
	insertContractEvent(t, db, "cupcake-event", "Cupcake", time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC))
	insertContractEvent(t, db, "workgraph-event", "workgraph", time.Date(2026, 5, 25, 12, 5, 0, 0, time.UTC))

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE project = ?`, "Cupcake").Scan(&count); err != nil {
		t.Fatalf("query events by project: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one Cupcake event, got %d", count)
	}
}

func TestEventsCanBeQueriedByTimeRange(t *testing.T) {
	db := openContractDatabase(t)
	insertContractEvent(t, db, "old-event", "Cupcake", time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC))
	insertContractEvent(t, db, "recent-event", "Cupcake", time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC))

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE timestamp >= ? AND timestamp < ?`,
		"2026-05-25T00:00:00Z",
		"2026-05-26T00:00:00Z",
	).Scan(&count); err != nil {
		t.Fatalf("query events by time range: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one event in time range, got %d", count)
	}
}

func TestSessionsTableExists(t *testing.T) {
	db := openContractDatabase(t)

	var name string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&name); err != nil {
		t.Fatalf("expected sessions table to exist: %v", err)
	}
	if name != "sessions" {
		t.Fatalf("expected sessions table, got %q", name)
	}
}

func TestSessionStartMustBeBeforeOrEqualEnd(t *testing.T) {
	db := openContractDatabase(t)

	_, err := db.Exec(`INSERT INTO sessions (id, started_at, ended_at, project, summary)
		VALUES (?, ?, ?, ?, ?)`,
		"backwards-session",
		"2026-05-25T13:00:00Z",
		"2026-05-25T12:00:00Z",
		"Cupcake",
		"Backwards session",
	)
	if err == nil {
		t.Fatalf("expected session with ended_at before started_at to be rejected")
	}
}

func TestMemoryDocsTableExists(t *testing.T) {
	db := openContractDatabase(t)

	var name string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'memory_docs'`).Scan(&name); err != nil {
		t.Fatalf("expected memory_docs table to exist: %v", err)
	}
	if name != "memory_docs" {
		t.Fatalf("expected memory_docs table, got %q", name)
	}
}

func TestMemoryDocPathIsUnique(t *testing.T) {
	db := openContractDatabase(t)

	insertMemoryDoc := func(id string) error {
		_, err := db.Exec(`INSERT INTO memory_docs (id, path, kind, content, updated_at)
			VALUES (?, ?, ?, ?, ?)`,
			id,
			"/memory/projects/cupcake.md",
			"markdown",
			"# Cupcake",
			"2026-05-25T12:00:00Z",
		)
		return err
	}
	if err := insertMemoryDoc("memory-doc-1"); err != nil {
		t.Fatalf("insert memory doc: %v", err)
	}
	if err := insertMemoryDoc("memory-doc-2"); err == nil {
		t.Fatalf("expected duplicate memory doc path to be rejected")
	}
}

func TestMemoryLinksTableExists(t *testing.T) {
	db := openContractDatabase(t)

	var name string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'memory_links'`).Scan(&name); err != nil {
		t.Fatalf("expected memory_links table to exist: %v", err)
	}
	if name != "memory_links" {
		t.Fatalf("expected memory_links table, got %q", name)
	}
}

func TestMemoryLinksTableHasRequiredColumns(t *testing.T) {
	db := openContractDatabase(t)

	columns := tableColumns(t, db, "memory_links")
	for _, column := range []string{"id", "memory_doc_path", "event_id", "relation", "created_at"} {
		if !columns[column] {
			t.Fatalf("expected memory_links table to have column %q, got %#v", column, columns)
		}
	}
}

func TestNotionIndexTableHasRequiredColumns(t *testing.T) {
	db := openContractDatabase(t)

	columns := tableColumns(t, db, "notion_index")
	for _, column := range []string{
		"notion_id",
		"object_type",
		"title",
		"url",
		"parent_json",
		"properties_json",
		"content_preview",
		"content_synced_at",
		"created_time",
		"created_by",
		"last_edited_time",
		"last_edited_by",
		"source",
		"first_seen_at",
		"last_seen_at",
		"last_synced_at",
	} {
		if !columns[column] {
			t.Fatalf("expected notion_index table to have column %q, got %#v", column, columns)
		}
	}
}

func TestSuggestionsTablesHaveRequiredColumns(t *testing.T) {
	db := openContractDatabase(t)

	suggestionColumns := tableColumns(t, db, "suggestions")
	for _, column := range []string{"id", "type", "title", "reason", "confidence", "lane", "status", "evidence_json", "created_at", "updated_at", "pattern_key", "resolved_at"} {
		if !suggestionColumns[column] {
			t.Fatalf("expected suggestions table to have column %q, got %#v", column, suggestionColumns)
		}
	}

	feedbackColumns := tableColumns(t, db, "suggestion_feedback")
	for _, column := range []string{"id", "suggestion_id", "action", "created_at", "reason_code", "note"} {
		if !feedbackColumns[column] {
			t.Fatalf("expected suggestion_feedback table to have column %q, got %#v", column, feedbackColumns)
		}
	}

	suppressionColumns := tableColumns(t, db, "suggestion_suppressions")
	for _, column := range []string{"id", "type", "pattern_key", "created_at", "until_at", "reason"} {
		if !suppressionColumns[column] {
			t.Fatalf("expected suggestion_suppressions table to have column %q, got %#v", column, suppressionColumns)
		}
	}
}

func TestSuggestionsTableRejectsInvalidEvidenceJSON(t *testing.T) {
	db := openContractDatabase(t)

	_, err := db.Exec(`INSERT INTO suggestions
		(id, type, pattern_key, title, reason, confidence, lane, status, evidence_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"invalid-evidence",
		"ignore_path",
		"/repo/build",
		"Ignore build",
		"Generated build files were captured.",
		"high",
		"baseline",
		"proposed",
		`not-json`,
		"2026-05-25T12:00:00Z",
		"2026-05-25T12:00:00Z",
	)
	if err == nil {
		t.Fatalf("expected invalid suggestion evidence_json to be rejected")
	}
}

func TestDraftTablesDocumentFutureIntent(t *testing.T) {
	t.Skip("TBD: draft tables are documented but not required for Phase 0")
}

func TestDatabaseSchemaVersionAndSecondaryIndicesAfterInit(t *testing.T) {
	db := openContractDatabase(t)

	var userVersion int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if userVersion <= 0 {
		t.Fatalf("expected PRAGMA user_version > 0 after Init, got %d", userVersion)
	}
	if userVersion != workgraph.CurrentSchemaVersion {
		t.Fatalf("expected PRAGMA user_version %d, got %d", workgraph.CurrentSchemaVersion, userVersion)
	}

	for _, indexName := range []string{
		"idx_events_timestamp",
		"idx_events_project",
		"idx_events_source",
		"idx_events_type",
	} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'events' AND name = ?`, indexName).Scan(&name); err != nil {
			t.Fatalf("expected events index %q to exist: %v", indexName, err)
		}
	}
}

func TestLegacyV0DatabaseUpgradesThroughNumberedMigrations(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	dbPath := filepath.Join(homeDir, "workgraph.db")

	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir:      homeDir,
		DatabasePath: dbPath,
		MemoryDir:    filepath.Join(tempDir, "workgraph-memory"),
	})
	if err != nil {
		t.Fatalf("initial init failed: %v", err)
	}

	legacyDB, err := sql.Open("sqlite3", result.DatabasePath)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	for _, stmt := range []string{
		`PRAGMA user_version = 0`,
		`DROP TABLE events`,
		`CREATE TABLE events (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			type TEXT NOT NULL,
			timestamp TEXT NOT NULL,
			payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
			project TEXT,
			actor TEXT,
			summary TEXT,
			created_at TEXT NOT NULL
		)`,
		`INSERT INTO events (id, source, type, timestamp, payload_json, project, created_at)
			VALUES ('legacy-slack', 'slack', 'slack.reply', '2026-05-25T12:00:00Z', '{"channel_id":"C0DEMO001","channel_name":"squad-demo","ts":"1789300800.000100","text":"Reply"}', '#squad-demo', '2026-05-25T12:00:00Z')`,
		`DROP TABLE capture_requests`,
		`CREATE TABLE capture_requests (
			id TEXT PRIMARY KEY,
			connector_id TEXT NOT NULL,
			since TEXT NOT NULL,
			until TEXT NOT NULL,
			params_json TEXT NOT NULL CHECK (json_valid(params_json)),
			status TEXT NOT NULL CHECK (status IN ('pending', 'claimed', 'completed', 'cancelled')),
			attempts INTEGER NOT NULL DEFAULT 0,
			available_at TEXT NOT NULL,
			last_error TEXT,
			created_at TEXT NOT NULL,
			claim_token TEXT,
			claimed_by TEXT,
			claimed_at TEXT,
			lease_expires_at TEXT,
			completed_at TEXT,
			cancelled_at TEXT
		)`,
		`DROP TABLE notion_index`,
		`CREATE TABLE notion_index (
			notion_id TEXT PRIMARY KEY,
			object_type TEXT NOT NULL,
			title TEXT,
			url TEXT,
			parent_json TEXT CHECK (parent_json IS NULL OR json_valid(parent_json)),
			properties_json TEXT CHECK (properties_json IS NULL OR json_valid(properties_json)),
			created_time TEXT,
			created_by TEXT,
			last_edited_time TEXT,
			last_edited_by TEXT,
			source TEXT NOT NULL,
			first_seen_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			last_synced_at TEXT NOT NULL
		)`,
	} {
		if _, err := legacyDB.Exec(stmt); err != nil {
			legacyDB.Close()
			t.Fatalf("prepare legacy v0 fixture: %v", err)
		}
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy fixture: %v", err)
	}

	if _, err := workgraph.Init(workgraph.InitConfig{
		HomeDir:      homeDir,
		DatabasePath: dbPath,
		MemoryDir:    filepath.Join(tempDir, "workgraph-memory"),
	}); err != nil {
		t.Fatalf("upgrade init failed: %v", err)
	}

	db, err := sql.Open("sqlite3", result.DatabasePath)
	if err != nil {
		t.Fatalf("open upgraded database: %v", err)
	}
	defer db.Close()

	var userVersion int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("read upgraded user_version: %v", err)
	}
	if userVersion != workgraph.CurrentSchemaVersion {
		t.Fatalf("expected upgraded user_version %d, got %d", workgraph.CurrentSchemaVersion, userVersion)
	}

	if !tableColumns(t, db, "events")["involvement_json"] {
		t.Fatalf("expected upgraded events table to include involvement_json")
	}
	if !tableColumns(t, db, "capture_requests")["kind"] {
		t.Fatalf("expected upgraded capture_requests table to include kind")
	}
	notionCols := tableColumns(t, db, "notion_index")
	if !notionCols["content_preview"] || !notionCols["content_synced_at"] {
		t.Fatalf("expected upgraded notion_index table to include content_preview and content_synced_at")
	}
	if !tableColumns(t, db, "github_pr_watch_state")["reviewer_polled"] {
		t.Fatalf("expected upgraded github_pr_watch_state table to include reviewer_polled")
	}

	var eventType, project string
	if err := db.QueryRow(`SELECT type, project FROM events WHERE id = 'legacy-slack'`).Scan(&eventType, &project); err != nil {
		t.Fatalf("read upgraded slack event: %v", err)
	}
	if eventType != "slack.thread_reply" || project != "squad-demo" {
		t.Fatalf("expected legacy slack event to be backfilled during upgrade, got type=%q project=%q", eventType, project)
	}
}

func openContractDatabase(t *testing.T) *sql.DB {
	t.Helper()

	tempDir := t.TempDir()
	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir:   filepath.Join(tempDir, ".workgraph"),
		MemoryDir: filepath.Join(tempDir, "workgraph-memory"),
	})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	db, err := sql.Open("sqlite3", result.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close database: %v", err)
		}
	})
	return db
}

func tableColumns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()

	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("query %s columns: %v", table, err)
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan %s column: %v", table, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("query %s columns: %v", table, err)
	}
	return columns
}

func insertContractEvent(t *testing.T, db *sql.DB, id string, project string, timestamp time.Time) {
	t.Helper()

	_, err := db.Exec(`INSERT INTO events
		(id, source, type, timestamp, payload_json, project, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id,
		"file",
		"file.modified",
		timestamp.Format(time.RFC3339),
		`{"path":"README.md"}`,
		project,
		timestamp.Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert event %q: %v", id, err)
	}
}
