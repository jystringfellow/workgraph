package workgraph

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const slackListSnapshotChunksSchema = `CREATE TABLE IF NOT EXISTS slack_list_snapshot_chunks (
	request_id TEXT NOT NULL,
	claim_token TEXT NOT NULL,
	sequence INTEGER NOT NULL,
	fragment TEXT NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (request_id, claim_token, sequence)
);`

const slackListSnapshotChunksClaimTrigger = `CREATE TRIGGER IF NOT EXISTS slack_list_snapshot_chunks_follow_claim
AFTER UPDATE OF status, claim_token ON capture_requests
BEGIN
	DELETE FROM slack_list_snapshot_chunks
	WHERE request_id = NEW.id
		AND (NEW.status <> 'claimed' OR NEW.claim_token IS NULL OR claim_token <> NEW.claim_token);
END;`

const slackListSnapshotChunksRequestDeleteTrigger = `CREATE TRIGGER IF NOT EXISTS slack_list_snapshot_chunks_follow_request_delete
AFTER DELETE ON capture_requests
BEGIN
	DELETE FROM slack_list_snapshot_chunks WHERE request_id = OLD.id;
END;`

const slackListSnapshotChunksAbandonedSweep = `DELETE FROM slack_list_snapshot_chunks
WHERE NOT EXISTS (
	SELECT 1 FROM capture_requests
	WHERE capture_requests.id = slack_list_snapshot_chunks.request_id
		AND capture_requests.status = 'claimed'
		AND capture_requests.claim_token = slack_list_snapshot_chunks.claim_token
);`

type SlackListStateOptions struct {
	Column     string   `json:"column"`
	DoneValues []string `json:"done_values"`
}

type SlackListOptions struct {
	State            *SlackListStateOptions `json:"state,omitempty"`
	InterestColumns  []string               `json:"interest_columns,omitempty"`
	RowKeyCandidates [][]string             `json:"row_key_candidates,omitempty"`
}

func defaultSlackListRowKeyCandidates() [][]string {
	return [][]string{{"Related Message"}, {"Title", "Cycle"}, {"Title"}}
}

func normalizeSlackListOptions(options SlackListOptions) (SlackListOptions, error) {
	if options.State != nil {
		state := *options.State
		state.Column = strings.TrimSpace(state.Column)
		if state.Column == "" || len(state.DoneValues) == 0 {
			return SlackListOptions{}, fmt.Errorf("state requires a non-empty column and done_values array")
		}
		state.DoneValues = normalizeSlackListColumns(state.DoneValues)
		if len(state.DoneValues) == 0 {
			return SlackListOptions{}, fmt.Errorf("state requires a non-empty column and done_values array")
		}
		options.State = &state
	}
	options.InterestColumns = normalizeSlackListColumns(options.InterestColumns)
	if len(options.RowKeyCandidates) > 0 {
		candidates, err := normalizeSlackListRowKeyCandidates(options.RowKeyCandidates)
		if err != nil {
			return SlackListOptions{}, err
		}
		options.RowKeyCandidates = candidates
	}
	return options, nil
}

func normalizeSlackListOptionsMap(options map[string]SlackListOptions, listIDs []string) (map[string]SlackListOptions, error) {
	if len(options) == 0 {
		return nil, nil
	}
	allowed := make(map[string]string, len(listIDs))
	for _, listID := range listIDs {
		trimmed := strings.TrimSpace(listID)
		if trimmed != "" {
			allowed[strings.ToLower(trimmed)] = trimmed
		}
	}
	normalized := make(map[string]SlackListOptions, len(options))
	for listID, option := range options {
		canonicalID, found := allowed[strings.ToLower(strings.TrimSpace(listID))]
		if !found {
			return nil, fmt.Errorf("list_options contains unconfigured Slack List %q", strings.TrimSpace(listID))
		}
		parsed, err := normalizeSlackListOptions(option)
		if err != nil {
			return nil, fmt.Errorf("list_options %q: %w", canonicalID, err)
		}
		normalized[canonicalID] = parsed
	}
	return normalized, nil
}

func normalizeSlackListColumns(columns []string) []string {
	normalized := make([]string, 0, len(columns))
	seen := map[string]bool{}
	for _, column := range columns {
		column = strings.TrimSpace(column)
		key := strings.ToLower(column)
		if column == "" || seen[key] {
			continue
		}
		seen[key] = true
		normalized = append(normalized, column)
	}
	return normalized
}

func normalizeSlackListRowKeyCandidates(candidates [][]string) ([][]string, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("row_key_candidates must be a non-empty array of non-empty string arrays")
	}
	normalized := make([][]string, len(candidates))
	for index, candidate := range candidates {
		normalized[index] = normalizeSlackListColumns(candidate)
		if len(normalized[index]) == 0 || len(normalized[index]) != len(candidate) {
			return nil, fmt.Errorf("row_key_candidates must be a non-empty array of non-empty string arrays")
		}
	}
	return normalized, nil
}

func slackListOptionsFor(options map[string]SlackListOptions, listID string) SlackListOptions {
	option, _ := findSlackListOptions(options, listID)
	return option
}

func findSlackListOptions(options map[string]SlackListOptions, listID string) (SlackListOptions, bool) {
	for configuredID, option := range options {
		if strings.EqualFold(strings.TrimSpace(configuredID), strings.TrimSpace(listID)) {
			return option, true
		}
	}
	return SlackListOptions{}, false
}

func interpretSlackListFields(fields map[string]any, options SlackListOptions, legacyDoneColumn string) (*bool, map[string]any, error) {
	var done *bool
	state := options.State
	legacy := false
	if state == nil && strings.TrimSpace(legacyDoneColumn) != "" {
		state = &SlackListStateOptions{Column: strings.TrimSpace(legacyDoneColumn)}
		legacy = true
	}
	if state != nil {
		value, found := slackListSnapshotField(fields, state.Column)
		if found && snapshotFieldText(value) != "" {
			interpreted := false
			if legacy {
				var err error
				interpreted, err = normalizeSlackListDone(value)
				if err != nil {
					return nil, nil, fmt.Errorf("state column %q: %w", state.Column, err)
				}
			} else {
				actual := strings.TrimSpace(snapshotFieldText(value))
				for _, candidate := range state.DoneValues {
					if strings.EqualFold(actual, strings.TrimSpace(candidate)) {
						interpreted = true
						break
					}
				}
			}
			done = &interpreted
		}
	}

	interest := map[string]any{}
	for _, column := range options.InterestColumns {
		if value, found := slackListSnapshotField(fields, column); found {
			interest[column] = value
		}
	}
	if len(interest) == 0 {
		interest = nil
	}
	return done, interest, nil
}

func slackListRevisionKey(listID string, rowKey string) string {
	return listID + "\x00" + rowKey
}

func slackListRevisionHash(listID string, fields map[string]any, done *bool) string {
	stable := map[string]string{}
	collided := map[string]bool{}
	for column, value := range fields {
		raw := snapshotFieldText(value)
		if raw == "" || strings.ContainsAny(raw, "\r\n") {
			continue
		}
		text := strings.Join(strings.Fields(raw), " ")
		key := strings.ToLower(strings.Join(strings.Fields(column), " "))
		if _, exists := stable[key]; exists {
			collided[key] = true
		}
		stable[key] = text
	}
	for key := range collided {
		delete(stable, key)
	}
	columns := make([]string, 0, len(stable))
	for column := range stable {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	revision := []any{listID}
	if done != nil {
		revision = append(revision, map[string]bool{"done": *done})
	}
	for _, column := range columns {
		revision = append(revision, []string{column, stable[column]})
	}
	encoded, _ := json.Marshal(revision)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

type slackListRevisionQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func slackListRevisionEventID(externalID string) string {
	digest := sha256.Sum256([]byte("slack\x00" + externalID))
	return hex.EncodeToString(digest[:16])
}

type storedSlackListRevision struct {
	id          string
	timestamp   time.Time
	createdAt   time.Time
	payloadJSON string
}

type latestSlackListRevision struct {
	eventID         string
	done            *bool
	sameStateHashes map[string]bool
}

func sameSlackListState(a *bool, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func slackListRevisionHistory(db slackListRevisionQueryer, listIDs []string) (map[string]latestSlackListRevision, map[string]bool, error) {
	query := `SELECT id, timestamp, created_at, payload_json FROM events
		WHERE type = 'slack.list_item' AND json_extract(payload_json, '$.capture_semantics') = 'complete_snapshot'`
	args := make([]any, 0, len(listIDs))
	if len(listIDs) > 0 {
		query += ` AND json_extract(payload_json, '$.list_id') IN (?` + strings.Repeat(", ?", len(listIDs)-1) + `)`
		for _, listID := range listIDs {
			args = append(args, listID)
		}
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("read Slack List revisions: %w", err)
	}
	defer rows.Close()
	var stored []storedSlackListRevision
	for rows.Next() {
		var revision storedSlackListRevision
		var timestamp, createdAt string
		if err := rows.Scan(&revision.id, &timestamp, &createdAt, &revision.payloadJSON); err != nil {
			return nil, nil, fmt.Errorf("scan Slack List revision: %w", err)
		}
		revision.timestamp, _ = time.Parse(time.RFC3339Nano, timestamp)
		revision.createdAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		stored = append(stored, revision)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("read Slack List revisions: %w", err)
	}
	sort.Slice(stored, func(i, j int) bool {
		if !stored[i].timestamp.Equal(stored[j].timestamp) {
			return stored[i].timestamp.Before(stored[j].timestamp)
		}
		if !stored[i].createdAt.Equal(stored[j].createdAt) {
			return stored[i].createdAt.Before(stored[j].createdAt)
		}
		return stored[i].id < stored[j].id
	})
	latest := map[string]latestSlackListRevision{}
	repeated := map[string]bool{}
	for _, revision := range stored {
		id, payloadJSON := revision.id, revision.payloadJSON
		var payload struct {
			ListID string         `json:"list_id"`
			RowKey string         `json:"row_key"`
			Fields map[string]any `json:"fields"`
			Done   *bool          `json:"done"`
		}
		decoder := json.NewDecoder(bytes.NewReader([]byte(payloadJSON)))
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil || payload.ListID == "" || payload.RowKey == "" {
			continue
		}
		key := slackListRevisionKey(payload.ListID, payload.RowKey)
		revision := slackListRevisionHash(payload.ListID, payload.Fields, payload.Done)
		previous, found := latest[key]
		if found && previous.sameStateHashes[revision] {
			repeated[id] = true
			continue
		}
		hashes := map[string]bool{}
		if found && sameSlackListState(previous.done, payload.Done) {
			hashes = previous.sameStateHashes
		}
		hashes[revision] = true
		latest[key] = latestSlackListRevision{eventID: id, done: payload.Done, sameStateHashes: hashes}
	}
	return latest, repeated, nil
}

func repeatedSlackListRevisionIDs(db *sql.DB) (map[string]bool, error) {
	_, repeated, err := slackListRevisionHistory(db, nil)
	return repeated, err
}

type SlackListSnapshotCSVConfig struct {
	HomeDir      string
	DatabasePath string
	RequestID    string
	ClaimToken   string
	ListID       string
	CSV          string
	Continues    bool
}

func IngestSlackListSnapshotCSV(config SlackListSnapshotCSVConfig) (BridgedIngestResult, error) {
	status, err := prepareRunStatus(RunConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return BridgedIngestResult{}, err
	}
	db, err := openDatabase(status.DatabasePath)
	if err != nil {
		return BridgedIngestResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := createSchema(db); err != nil {
		return BridgedIngestResult{}, fmt.Errorf("prepare database schema: %w", err)
	}
	requestID := strings.TrimSpace(config.RequestID)
	listID := strings.TrimSpace(config.ListID)
	if config.Continues {
		return stageSlackListSnapshotChunk(db, status, requestID, config.ClaimToken, listID, config.CSV)
	}

	staged, err := stagedSlackListSnapshot(db, requestID, config.ClaimToken)
	if err != nil {
		return BridgedIngestResult{}, err
	}
	result, err := ingestSlackListSnapshotCSV(config, requestID, listID, staged+config.CSV)
	if err != nil {
		if staged == "" {
			return BridgedIngestResult{}, err
		}
		if _, clearErr := db.Exec(`DELETE FROM slack_list_snapshot_chunks WHERE request_id = ? AND claim_token = ?`, requestID, config.ClaimToken); clearErr != nil {
			return BridgedIngestResult{}, fmt.Errorf("%w; clear staged Slack List snapshot chunks: %v", err, clearErr)
		}
		return BridgedIngestResult{}, fmt.Errorf("%w; staged snapshot chunks were discarded, so resend the snapshot from its first fragment", err)
	}
	return result, nil
}

func ingestSlackListSnapshotCSV(config SlackListSnapshotCSVConfig, requestID string, listID string, contents string) (BridgedIngestResult, error) {
	envelopes, err := slackListSnapshotEnvelopesFromCSV(listID, contents)
	if err != nil {
		return BridgedIngestResult{}, err
	}
	events, err := json.Marshal(envelopes)
	if err != nil {
		return BridgedIngestResult{}, fmt.Errorf("encode Slack List snapshot rows: %w", err)
	}
	return IngestBridgedCapture(BridgedIngestConfig{
		HomeDir: config.HomeDir, DatabasePath: config.DatabasePath,
		RequestID: requestID, ClaimToken: config.ClaimToken, SnapshotListID: listID,
		Input: bytes.NewReader(events),
	})
}

func stagedSlackListSnapshot(db *sql.DB, requestID string, claimToken string) (string, error) {
	rows, err := db.Query(`SELECT fragment FROM slack_list_snapshot_chunks
		WHERE request_id = ? AND claim_token = ? ORDER BY sequence ASC`, requestID, claimToken)
	if err != nil {
		return "", fmt.Errorf("read staged Slack List snapshot chunks: %w", err)
	}
	defer rows.Close()
	var builder strings.Builder
	for rows.Next() {
		var fragment string
		if err := rows.Scan(&fragment); err != nil {
			return "", fmt.Errorf("scan staged Slack List snapshot chunk: %w", err)
		}
		builder.WriteString(fragment)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("read staged Slack List snapshot chunks: %w", err)
	}
	return builder.String(), nil
}

func stageSlackListSnapshotChunk(db *sql.DB, status RunStatus, requestID string, claimToken string, listID string, fragment string) (BridgedIngestResult, error) {
	request, found, err := readCaptureRequestDB(db, requestID)
	if err != nil {
		return BridgedIngestResult{}, err
	}
	if !found {
		return BridgedIngestResult{}, fmt.Errorf("capture request %s was not found", requestID)
	}
	if request.Kind != captureRequestKindCapture || request.ConnectorID != "slack.lists" || request.CaptureSemantics != "complete_snapshot" {
		return BridgedIngestResult{}, fmt.Errorf("snapshot_continues requires a claimed Slack Lists complete-snapshot request")
	}
	if err := validateSlackListRawSnapshotScope(request, listID); err != nil {
		return BridgedIngestResult{}, err
	}
	now := time.Now().UTC()
	tx, err := db.Begin()
	if err != nil {
		return BridgedIngestResult{}, fmt.Errorf("begin Slack List snapshot chunk: %w", err)
	}
	defer tx.Rollback()
	renewed, err := tx.Exec(`UPDATE capture_requests SET lease_expires_at = ?
		WHERE id = ? AND status = 'claimed' AND claim_token = ? AND lease_expires_at > ?`,
		now.Add(bridgedClaimLease).Format(time.RFC3339Nano), requestID, claimToken, now.Format(time.RFC3339Nano))
	if err != nil {
		return BridgedIngestResult{}, fmt.Errorf("renew capture request: %w", err)
	}
	if count, _ := renewed.RowsAffected(); count != 1 || strings.TrimSpace(claimToken) == "" {
		return BridgedIngestResult{}, fmt.Errorf("capture request %s is not validly claimed", requestID)
	}
	var sequence, stagedBytes int
	if err := tx.QueryRow(`SELECT COUNT(*), COALESCE(SUM(length(CAST(fragment AS BLOB))), 0)
		FROM slack_list_snapshot_chunks WHERE request_id = ? AND claim_token = ?`, requestID, claimToken).Scan(&sequence, &stagedBytes); err != nil {
		return BridgedIngestResult{}, fmt.Errorf("read staged Slack List snapshot size: %w", err)
	}
	if stagedBytes+len(fragment) > maxBridgedIngestBytes {
		return BridgedIngestResult{}, fmt.Errorf("snapshot_csv exceeds %d bytes", maxBridgedIngestBytes)
	}
	if _, err := tx.Exec(`INSERT INTO slack_list_snapshot_chunks (request_id, claim_token, sequence, fragment, created_at)
		VALUES (?, ?, ?, ?, ?)`, requestID, claimToken, sequence, fragment, now.Format(time.RFC3339Nano)); err != nil {
		return BridgedIngestResult{}, fmt.Errorf("stage Slack List snapshot chunk: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return BridgedIngestResult{}, fmt.Errorf("commit Slack List snapshot chunk: %w", err)
	}
	return BridgedIngestResult{
		HomeDir:      status.HomeDir,
		DatabasePath: status.DatabasePath,
		Source:       request.Source,
		Message: fmt.Sprintf("Slack List snapshot chunk %d staged (%d bytes total); request %s stays claimed until a final snapshot_csv chunk without snapshot_continues",
			sequence+1, stagedBytes+len(fragment), requestID),
	}, nil
}
