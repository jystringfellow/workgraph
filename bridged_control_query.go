package workgraph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
)

var controlQueryEventCondition = `(type GLOB '*.control_query' OR (source IN (` + bridgedEventSourceList() + `) AND COALESCE(json_type(payload_json, '$.control_query'), '') = 'true'))`

var notControlQueryEvent = `NOT ` + controlQueryEventCondition

func bridgedEventSourceList() string {
	seen := map[string]bool{}
	for _, definition := range connectorRegistry {
		if definition.Bridged {
			seen[definition.ID] = true
			seen[definition.EventSource] = true
		}
	}
	sources := make([]string, 0, len(seen))
	for source := range seen {
		sources = append(sources, "'"+source+"'")
	}
	sort.Strings(sources)
	return strings.Join(sources, ", ")
}

var bridgedSummaryTitleFields = map[string]string{
	"mail.microsoft.message":   "subject",
	"calendar.microsoft.event": "subject",
	"notion.page_updated":      "title",
	"notion.database_updated":  "title",
	"azure.boards.workitem":    "title",
}

func errControlQueryEvent(index int) error {
	return fmt.Errorf(`event %d: control-query results are empty-window proof, not events; remove it and resubmit, and when no other events remain submit empty_proof {"control_query_passed":true} instead (MCP capture_ingest empty_proof argument, or CLI {"events":[],"empty_proof":{...}})`, index)
}

func isControlQueryEvent(eventType string, payloadJSON string) bool {
	if strings.HasSuffix(eventType, ".control_query") {
		return true
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return false
	}
	return strings.TrimSpace(string(payload["control_query"])) == "true"
}

func bridgedTitleSummary(eventType string, payloadJSON string) string {
	field, found := bridgedSummaryTitleFields[eventType]
	if !found {
		return ""
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return ""
	}
	var title string
	if err := json.Unmarshal(payload[field], &title); err != nil {
		return ""
	}
	return strings.TrimSpace(title)
}

func bridgedSummaryTypesByField() map[string][]string {
	byField := map[string][]string{}
	for eventType, field := range bridgedSummaryTitleFields {
		byField[field] = append(byField[field], eventType)
	}
	for field := range byField {
		sort.Strings(byField[field])
	}
	return byField
}

func bridgedSummaryTypeList() string {
	types := make([]string, 0, len(bridgedSummaryTitleFields))
	for eventType := range bridgedSummaryTitleFields {
		types = append(types, "'"+eventType+"'")
	}
	sort.Strings(types)
	return strings.Join(types, ", ")
}

func backfillBridgedSummaries(db *sql.DB) error {
	missingSummary := `(summary IS NULL OR summary = '') AND type IN (` + bridgedSummaryTypeList() + `)`
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_events_bridged_without_summary ON events (id) WHERE ` + missingSummary); err != nil {
		return fmt.Errorf("index bridged summary backfill: %w", err)
	}
	var summarized int64
	for field, types := range bridgedSummaryTypesByField() {
		title := `trim(json_extract(payload_json, '$.` + field + `'), char(9, 10, 13, 32))`
		placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(types)), ", ")
		args := make([]any, 0, len(types))
		for _, eventType := range types {
			args = append(args, eventType)
		}
		updated, err := db.Exec(`UPDATE events INDEXED BY idx_events_bridged_without_summary SET summary = `+title+`
			WHERE `+missingSummary+` AND type IN (`+placeholders+`)
			AND json_type(payload_json, '$.`+field+`') = 'text' AND `+title+` != ''`, args...)
		if err != nil {
			return fmt.Errorf("backfill bridged %s summaries: %w", field, err)
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return err
		}
		summarized += count
	}
	if summarized > 0 {
		log.Printf("workgraph store backfilled bridged event summaries: %d", summarized)
	}
	return nil
}
