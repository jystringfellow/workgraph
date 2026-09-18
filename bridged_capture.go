package workgraph

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxBridgedIngestBytes = 16 << 20

const (
	bridgedInitialWindow = 24 * time.Hour
	bridgedOverlap       = 5 * time.Minute
	bridgedClaimLease    = 5 * time.Minute
)

// CaptureRequest is one durable unit of provider work for an approved bridge.
type CaptureRequest struct {
	ID               string          `json:"id"`
	ConnectorID      string          `json:"connector_id"`
	Source           string          `json:"source"`
	CaptureSemantics string          `json:"capture_semantics"`
	Since            string          `json:"since"`
	Until            string          `json:"until"`
	Params           json.RawMessage `json:"params"`
	Status           string          `json:"status"`
	Attempts         int             `json:"attempts"`
	AvailableAt      string          `json:"available_at"`
	ClaimedBy        string          `json:"claimed_by,omitempty"`
	ClaimedAt        string          `json:"claimed_at,omitempty"`
	LeaseExpiresAt   string          `json:"lease_expires_at,omitempty"`
}

// CaptureRequestEmitConfig controls one daemon-owned bridged scheduling pass.
type CaptureRequestEmitConfig struct {
	HomeDir      string
	DatabasePath string
	ConnectorID  string
	Now          time.Time
}

// CaptureRequestEmitResult describes an emitted or coalesced request.
type CaptureRequestEmitResult struct {
	Request   CaptureRequest
	Coalesced bool
}

// CaptureRequestClaimConfig controls atomic bridge request claiming.
type CaptureRequestClaimConfig struct {
	HomeDir      string
	DatabasePath string
	ConnectorID  string
	Worker       string
	Max          int
	Now          time.Time
	Lease        time.Duration
}

// CaptureRequestListConfig controls non-secret outbox inspection.
type CaptureRequestListConfig struct {
	HomeDir      string
	DatabasePath string
	ConnectorID  string
}

// CaptureRequestCapabilityConfig presents a short-lived claim capability.
type CaptureRequestCapabilityConfig struct {
	HomeDir      string
	DatabasePath string
	RequestID    string
	ClaimToken   string
	Error        string
	Now          time.Time
	Lease        time.Duration
}

// ClaimedCaptureRequest includes the secret capability needed to complete work.
type ClaimedCaptureRequest struct {
	Request    CaptureRequest `json:"request"`
	ClaimToken string         `json:"claim_token"`
}

// ListCaptureRequests returns outbox metadata without claim capabilities.
func ListCaptureRequests(config CaptureRequestListConfig) ([]CaptureRequest, error) {
	status, err := prepareRunStatus(RunConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	query := `SELECT id, connector_id, since, until, params_json, status, attempts,
		available_at, COALESCE(claimed_by, ''), COALESCE(claimed_at, ''), COALESCE(lease_expires_at, '')
		FROM capture_requests`
	args := []any{}
	if strings.TrimSpace(config.ConnectorID) != "" {
		id, err := normalizeConnectorID(config.ConnectorID)
		if err != nil {
			return nil, err
		}
		query += ` WHERE connector_id = ?`
		args = append(args, id)
	}
	query += ` ORDER BY created_at, id`
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list capture requests: %w", err)
	}
	defer rows.Close()
	requests := []CaptureRequest{}
	for rows.Next() {
		request, _, err := scanCaptureRequest(rows)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list capture requests: %w", err)
	}
	return requests, nil
}

// CaptureWatermark returns daemon-owned completed-through state for one connector.
func CaptureWatermark(config CaptureRequestListConfig) (string, error) {
	status, err := prepareRunStatus(RunConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return "", err
	}
	id, err := normalizeConnectorID(config.ConnectorID)
	if err != nil {
		return "", err
	}
	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return "", fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	var watermark string
	err = db.QueryRow(`SELECT completed_through FROM capture_cursors WHERE connector_id = ?`, id).Scan(&watermark)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read capture watermark: %w", err)
	}
	return watermark, nil
}

// RenewCaptureRequest extends an unexpired claimed request lease.
func RenewCaptureRequest(config CaptureRequestCapabilityConfig) (CaptureRequest, error) {
	status, err := prepareRunStatus(RunConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return CaptureRequest{}, err
	}
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	lease := config.Lease
	if lease <= 0 {
		lease = bridgedClaimLease
	}
	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return CaptureRequest{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	expires := now.Add(lease).Format(time.RFC3339Nano)
	updated, err := db.Exec(`UPDATE capture_requests SET lease_expires_at = ?
		WHERE id = ? AND status = 'claimed' AND claim_token = ? AND lease_expires_at > ?`,
		expires, strings.TrimSpace(config.RequestID), config.ClaimToken, now.Format(time.RFC3339Nano))
	if err != nil {
		return CaptureRequest{}, fmt.Errorf("renew capture request: %w", err)
	}
	count, _ := updated.RowsAffected()
	if count != 1 {
		return CaptureRequest{}, fmt.Errorf("capture request claim token is stale, invalid, or expired")
	}
	request, found, err := readCaptureRequestDB(db, strings.TrimSpace(config.RequestID))
	if err != nil {
		return CaptureRequest{}, err
	}
	if !found {
		return CaptureRequest{}, fmt.Errorf("capture request was not found after renewal")
	}
	return request, nil
}

// FailCaptureRequest returns claimed work to pending after persisted backoff.
// A matching token may report failure after lease expiry while the request is
// still claimed, preserving the real failure before the daemon reaps it.
func FailCaptureRequest(config CaptureRequestCapabilityConfig) error {
	status, err := prepareRunStatus(RunConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return err
	}
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	var connectorID string
	var attempts int
	var storedToken, requestStatus string
	err = db.QueryRow(`SELECT connector_id, attempts, COALESCE(claim_token, ''), status
		FROM capture_requests WHERE id = ?`, strings.TrimSpace(config.RequestID)).Scan(
		&connectorID, &attempts, &storedToken, &requestStatus)
	if err != nil {
		return fmt.Errorf("read capture request: %w", err)
	}
	if requestStatus != "claimed" || storedToken != config.ClaimToken {
		return fmt.Errorf("capture request claim token is stale or invalid")
	}
	delay := connectorRetryDelay(defaultConnectorRetryInitial, defaultConnectorRetryMax, attempts)
	availableAt := now.Add(delay)
	errorMessage := strings.TrimSpace(config.Error)
	if errorMessage == "" {
		errorMessage = "bridge reported capture failure"
	}
	updated, err := db.Exec(`UPDATE capture_requests SET
		status = 'pending', available_at = ?, last_error = ?, claim_token = NULL,
		claimed_by = NULL, claimed_at = NULL, lease_expires_at = NULL
		WHERE id = ? AND status = 'claimed' AND claim_token = ?`,
		availableAt.Format(time.RFC3339Nano), errorMessage, config.RequestID, config.ClaimToken)
	if err != nil {
		return fmt.Errorf("fail capture request: %w", err)
	}
	count, _ := updated.RowsAffected()
	if count != 1 {
		return fmt.Errorf("capture request claim token is stale or invalid")
	}
	return recordConnectorPollAttempt(status.HomeDir, connectorID, now, availableAt, attempts, fmt.Errorf("%s", errorMessage))
}

// EmitBridgedCaptureRequest persists one bounded request or returns the active request.
func EmitBridgedCaptureRequest(config CaptureRequestEmitConfig) (CaptureRequestEmitResult, error) {
	status, err := prepareRunStatus(RunConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return CaptureRequestEmitResult{}, err
	}
	id, err := normalizeConnectorID(config.ConnectorID)
	if err != nil {
		return CaptureRequestEmitResult{}, err
	}
	if err := validateBridgeableConnector(id); err != nil {
		return CaptureRequestEmitResult{}, err
	}
	state, err := readConnectorRuntimeFile(status.HomeDir)
	if err != nil {
		return CaptureRequestEmitResult{}, err
	}
	if !connectorEnabled(state, id) || connectorCaptureMode(state, id) != "bridged" {
		return CaptureRequestEmitResult{}, fmt.Errorf("connector %s is not enabled in bridged mode", id)
	}
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return CaptureRequestEmitResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := createSchema(db); err != nil {
		return CaptureRequestEmitResult{}, fmt.Errorf("prepare database schema: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return CaptureRequestEmitResult{}, fmt.Errorf("begin capture request emission: %w", err)
	}
	defer tx.Rollback()

	active, found, err := readActiveCaptureRequest(tx, id)
	if err != nil {
		return CaptureRequestEmitResult{}, err
	}
	if found {
		if err := tx.Commit(); err != nil {
			return CaptureRequestEmitResult{}, fmt.Errorf("commit coalesced capture request: %w", err)
		}
		return CaptureRequestEmitResult{Request: active, Coalesced: true}, nil
	}

	since := now.Add(-bridgedInitialWindow)
	var cursor string
	err = tx.QueryRow(`SELECT completed_through FROM capture_cursors WHERE connector_id = ?`, id).Scan(&cursor)
	if err == nil {
		parsed, parseErr := time.Parse(time.RFC3339Nano, cursor)
		if parseErr != nil {
			return CaptureRequestEmitResult{}, fmt.Errorf("parse capture cursor for %s: %w", id, parseErr)
		}
		since = parsed.UTC().Add(-bridgedOverlap)
	} else if err != sql.ErrNoRows {
		return CaptureRequestEmitResult{}, fmt.Errorf("read capture cursor for %s: %w", id, err)
	}
	requestID, err := newEventID()
	if err != nil {
		return CaptureRequestEmitResult{}, fmt.Errorf("create capture request id: %w", err)
	}
	params, err := validatedBridgeParams(id, state.entry(id).BridgeParams)
	if err != nil {
		return CaptureRequestEmitResult{}, err
	}
	request := CaptureRequest{
		ID:               requestID,
		ConnectorID:      id,
		Source:           eventSourceForConnector(id),
		CaptureSemantics: captureSemanticsForConnector(id),
		Since:            since.Format(time.RFC3339Nano),
		Until:            now.Format(time.RFC3339Nano),
		Params:           params,
		Status:           "pending",
		AvailableAt:      now.Format(time.RFC3339Nano),
	}
	_, err = tx.Exec(`INSERT INTO capture_requests (
		id, connector_id, since, until, params_json, status, attempts, available_at, created_at
	) VALUES (?, ?, ?, ?, ?, 'pending', 0, ?, ?)`,
		request.ID, request.ConnectorID, request.Since, request.Until, string(request.Params), request.AvailableAt, request.AvailableAt,
	)
	if err != nil {
		return CaptureRequestEmitResult{}, fmt.Errorf("emit capture request: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CaptureRequestEmitResult{}, fmt.Errorf("commit capture request: %w", err)
	}
	return CaptureRequestEmitResult{Request: request}, nil
}

// ClaimCaptureRequests atomically leases available requests to one bridge worker.
func ClaimCaptureRequests(config CaptureRequestClaimConfig) ([]ClaimedCaptureRequest, error) {
	status, err := prepareRunStatus(RunConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return nil, err
	}
	worker := strings.TrimSpace(config.Worker)
	if worker == "" {
		return nil, fmt.Errorf("bridge worker name is required")
	}
	max := config.Max
	if max <= 0 {
		max = 1
	}
	if max > 100 {
		return nil, fmt.Errorf("claim max cannot exceed 100")
	}
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	lease := config.Lease
	if lease <= 0 {
		lease = bridgedClaimLease
	}
	connectorID := strings.TrimSpace(config.ConnectorID)
	if connectorID != "" {
		connectorID, err = normalizeConnectorID(connectorID)
		if err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin capture claim: %w", err)
	}
	defer tx.Rollback()
	if err := recycleExpiredCaptureClaims(tx, now); err != nil {
		return nil, err
	}

	query := `SELECT id FROM capture_requests
		WHERE status = 'pending' AND available_at <= ?`
	args := []any{now.Format(time.RFC3339Nano)}
	if connectorID != "" {
		query += ` AND connector_id = ?`
		args = append(args, connectorID)
	}
	query += ` ORDER BY available_at, created_at, id LIMIT ?`
	args = append(args, max)
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list claimable capture requests: %w", err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read claimable capture request: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close claimable capture requests: %w", err)
	}

	claimed := make([]ClaimedCaptureRequest, 0, len(ids))
	for _, id := range ids {
		token, err := newEventID()
		if err != nil {
			return nil, fmt.Errorf("create capture claim token: %w", err)
		}
		claimedAt := now.Format(time.RFC3339Nano)
		leaseExpires := now.Add(lease).Format(time.RFC3339Nano)
		updated, err := tx.Exec(`UPDATE capture_requests SET
			status = 'claimed', attempts = attempts + 1, claim_token = ?, claimed_by = ?,
			claimed_at = ?, lease_expires_at = ?
			WHERE id = ? AND status = 'pending' AND available_at <= ?`,
			token, worker, claimedAt, leaseExpires, id, claimedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("claim capture request %s: %w", id, err)
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("count claimed capture request %s: %w", id, err)
		}
		if count == 0 {
			continue
		}
		request, found, err := readCaptureRequest(tx, id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("claimed capture request %s disappeared", id)
		}
		claimed = append(claimed, ClaimedCaptureRequest{Request: request, ClaimToken: token})
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit capture claims: %w", err)
	}
	return claimed, nil
}

func recycleExpiredCaptureClaims(tx *sql.Tx, now time.Time) error {
	rows, err := tx.Query(`SELECT id, attempts FROM capture_requests
		WHERE status = 'claimed' AND lease_expires_at <= ?`, now.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("list expired capture claims: %w", err)
	}
	type expiredClaim struct {
		id       string
		attempts int
	}
	expired := []expiredClaim{}
	for rows.Next() {
		var claim expiredClaim
		if err := rows.Scan(&claim.id, &claim.attempts); err != nil {
			rows.Close()
			return fmt.Errorf("read expired capture claim: %w", err)
		}
		expired = append(expired, claim)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close expired capture claims: %w", err)
	}
	for _, claim := range expired {
		delay := connectorRetryDelay(defaultConnectorRetryInitial, defaultConnectorRetryMax, claim.attempts)
		_, err := tx.Exec(`UPDATE capture_requests SET
			status = 'pending', available_at = ?, last_error = 'claim lease expired',
			claim_token = NULL, claimed_by = NULL, claimed_at = NULL, lease_expires_at = NULL
			WHERE id = ? AND status = 'claimed'`, now.Add(delay).UTC().Format(time.RFC3339Nano), claim.id)
		if err != nil {
			return fmt.Errorf("recycle expired capture claim %s: %w", claim.id, err)
		}
	}
	return nil
}

func readActiveCaptureRequest(tx *sql.Tx, connectorID string) (CaptureRequest, bool, error) {
	row := tx.QueryRow(`SELECT id, connector_id, since, until, params_json, status, attempts,
		available_at, COALESCE(claimed_by, ''), COALESCE(claimed_at, ''), COALESCE(lease_expires_at, '')
		FROM capture_requests WHERE connector_id = ? AND status IN ('pending', 'claimed') LIMIT 1`, connectorID)
	return scanCaptureRequest(row)
}

func readCaptureRequest(tx *sql.Tx, id string) (CaptureRequest, bool, error) {
	row := tx.QueryRow(`SELECT id, connector_id, since, until, params_json, status, attempts,
		available_at, COALESCE(claimed_by, ''), COALESCE(claimed_at, ''), COALESCE(lease_expires_at, '')
		FROM capture_requests WHERE id = ?`, id)
	return scanCaptureRequest(row)
}

func readCaptureRequestDB(db *sql.DB, id string) (CaptureRequest, bool, error) {
	row := db.QueryRow(`SELECT id, connector_id, since, until, params_json, status, attempts,
		available_at, COALESCE(claimed_by, ''), COALESCE(claimed_at, ''), COALESCE(lease_expires_at, '')
		FROM capture_requests WHERE id = ?`, id)
	return scanCaptureRequest(row)
}

type captureRequestScanner interface {
	Scan(dest ...any) error
}

func scanCaptureRequest(row captureRequestScanner) (CaptureRequest, bool, error) {
	var request CaptureRequest
	var params string
	err := row.Scan(&request.ID, &request.ConnectorID, &request.Since, &request.Until, &params,
		&request.Status, &request.Attempts, &request.AvailableAt, &request.ClaimedBy,
		&request.ClaimedAt, &request.LeaseExpiresAt)
	if err == sql.ErrNoRows {
		return CaptureRequest{}, false, nil
	}
	if err != nil {
		return CaptureRequest{}, false, fmt.Errorf("read capture request: %w", err)
	}
	request.Source = eventSourceForConnector(request.ConnectorID)
	request.CaptureSemantics = captureSemanticsForConnector(request.ConnectorID)
	request.Params = json.RawMessage(params)
	return request, true, nil
}

func canonicalBridgeParams(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return json.RawMessage(`{}`), nil
	}
	canonical, err := canonicalJSONObject(raw)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(canonical), nil
}

func eventSourceForConnector(connectorID string) string {
	if connectorID == "slack.lists" {
		return "slack"
	}
	if connectorID == "notion.activity" {
		return "notion"
	}
	return connectorID
}

func captureSemanticsForConnector(connectorID string) string {
	if connectorID == "slack.lists" {
		return "complete_snapshot"
	}
	return "bounded_events"
}

func cancelActiveCaptureRequests(homeDir string, connectorID string, when time.Time) error {
	db, err := sql.Open("sqlite3", bridgedDefaultDatabasePath(homeDir))
	if err != nil {
		return fmt.Errorf("open capture outbox: %w", err)
	}
	defer db.Close()
	if err := createSchema(db); err != nil {
		return fmt.Errorf("prepare capture outbox: %w", err)
	}
	_, err = db.Exec(`UPDATE capture_requests SET
		status = 'cancelled', cancelled_at = ?, claim_token = NULL, lease_expires_at = NULL
		WHERE connector_id = ? AND status IN ('pending', 'claimed')`,
		when.UTC().Format(time.RFC3339Nano), connectorID)
	if err != nil {
		return fmt.Errorf("cancel active capture requests for %s: %w", connectorID, err)
	}
	return nil
}

func completeClaimedCaptureRequest(tx *sql.Tx, request CaptureRequest, claimToken string, completedAt string) error {
	updated, err := tx.Exec(`UPDATE capture_requests SET
		status = 'completed', completed_at = ?, claim_token = NULL, lease_expires_at = NULL
		WHERE id = ? AND status = 'claimed' AND claim_token = ? AND lease_expires_at > ?`,
		completedAt, request.ID, claimToken, completedAt,
	)
	if err != nil {
		return fmt.Errorf("complete capture request %s: %w", request.ID, err)
	}
	count, err := updated.RowsAffected()
	if err != nil {
		return fmt.Errorf("count completed capture request %s: %w", request.ID, err)
	}
	if count != 1 {
		return fmt.Errorf("capture request claim token is stale or invalid")
	}

	requestUntil, err := time.Parse(time.RFC3339Nano, request.Until)
	if err != nil {
		return fmt.Errorf("parse capture request until: %w", err)
	}
	var stored string
	err = tx.QueryRow(`SELECT completed_through FROM capture_cursors WHERE connector_id = ?`, request.ConnectorID).Scan(&stored)
	if err == sql.ErrNoRows {
		_, err = tx.Exec(`INSERT INTO capture_cursors (connector_id, completed_through, updated_at) VALUES (?, ?, ?)`,
			request.ConnectorID, requestUntil.UTC().Format(time.RFC3339Nano), completedAt)
	} else if err == nil {
		storedTime, parseErr := time.Parse(time.RFC3339Nano, stored)
		if parseErr != nil {
			return fmt.Errorf("parse capture cursor for %s: %w", request.ConnectorID, parseErr)
		}
		if requestUntil.After(storedTime) {
			_, err = tx.Exec(`UPDATE capture_cursors SET completed_through = ?, updated_at = ? WHERE connector_id = ?`,
				requestUntil.UTC().Format(time.RFC3339Nano), completedAt, request.ConnectorID)
		}
	}
	if err != nil {
		return fmt.Errorf("advance capture cursor for %s: %w", request.ConnectorID, err)
	}
	return nil
}

// BridgedIngestConfig controls an explicit local bridged capture ingest.
type BridgedIngestConfig struct {
	HomeDir        string
	DatabasePath   string
	Source         string
	RequestID      string
	ClaimToken     string
	SnapshotListID string
	Input          io.Reader
}

// BridgedIngestResult describes one local bridged capture ingest.
type BridgedIngestResult struct {
	HomeDir          string
	DatabasePath     string
	Source           string
	EventsRead       int
	EventsInserted   int
	EventsDuplicate  int
	WeakDedupe       int
	RequestCompleted bool
	Message          string
}

type bridgedEventEnvelope struct {
	Type       string          `json:"type"`
	Timestamp  string          `json:"timestamp"`
	Payload    json.RawMessage `json:"payload"`
	Project    string          `json:"project,omitempty"`
	Actor      string          `json:"actor,omitempty"`
	Summary    string          `json:"summary,omitempty"`
	ExternalID string          `json:"external_id,omitempty"`
}

type preparedBridgedEvent struct {
	ID          string
	Type        string
	Timestamp   string
	PayloadJSON string
	Project     string
	Actor       string
	Summary     string
	WeakDedupe  bool
	SnapshotKey string
}

// IngestBridgedCapture validates and atomically stores normalized bridged events.
func IngestBridgedCapture(config BridgedIngestConfig) (BridgedIngestResult, error) {
	status, err := prepareRunStatus(RunConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return BridgedIngestResult{}, err
	}
	if config.Input == nil {
		return BridgedIngestResult{}, fmt.Errorf("capture input is required")
	}
	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return BridgedIngestResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return BridgedIngestResult{}, fmt.Errorf("open database: %w", err)
	}
	if err := createSchema(db); err != nil {
		return BridgedIngestResult{}, fmt.Errorf("prepare database schema: %w", err)
	}

	state, err := readConnectorRuntimeFile(status.HomeDir)
	if err != nil {
		return BridgedIngestResult{}, err
	}
	source := strings.ToLower(strings.TrimSpace(config.Source))
	connectorID := ""
	var claimedRequest *CaptureRequest
	requestID := strings.TrimSpace(config.RequestID)
	if requestID != "" {
		request, found, err := readCaptureRequestDB(db, requestID)
		if err != nil {
			return BridgedIngestResult{}, err
		}
		if !found {
			return BridgedIngestResult{}, fmt.Errorf("capture request %s was not found", requestID)
		}
		if request.Status != "claimed" || strings.TrimSpace(config.ClaimToken) == "" {
			return BridgedIngestResult{}, fmt.Errorf("capture request %s is not validly claimed", requestID)
		}
		var storedToken string
		if err := db.QueryRow(`SELECT COALESCE(claim_token, '') FROM capture_requests WHERE id = ?`, requestID).Scan(&storedToken); err != nil {
			return BridgedIngestResult{}, fmt.Errorf("read capture request claim: %w", err)
		}
		if storedToken != config.ClaimToken {
			return BridgedIngestResult{}, fmt.Errorf("capture request claim token is stale or invalid")
		}
		leaseExpires, err := time.Parse(time.RFC3339Nano, request.LeaseExpiresAt)
		if err != nil || !leaseExpires.After(time.Now()) {
			return BridgedIngestResult{}, fmt.Errorf("capture request claim lease has expired")
		}
		connectorID = request.ConnectorID
		source = request.Source
		claimedRequest = &request
	} else {
		if source == "" {
			return BridgedIngestResult{}, fmt.Errorf("capture source is required")
		}
	}
	envelopes, err := decodeBridgedEvents(config.Input)
	if err != nil {
		return BridgedIngestResult{}, err
	}
	if claimedRequest == nil {
		connectorID, err = bridgedConnectorIDForEvents(source, envelopes)
		if err != nil {
			return BridgedIngestResult{}, err
		}
	}
	if err := validateBridgeableConnector(connectorID); err != nil {
		return BridgedIngestResult{}, err
	}
	if strings.TrimSpace(config.SnapshotListID) != "" {
		if claimedRequest == nil || connectorID != "slack.lists" || claimedRequest.CaptureSemantics != "complete_snapshot" {
			return BridgedIngestResult{}, fmt.Errorf("snapshot_csv requires a claimed Slack Lists complete-snapshot request")
		}
		if err := validateSlackListRawSnapshotScope(*claimedRequest, config.SnapshotListID); err != nil {
			return BridgedIngestResult{}, err
		}
	}
	if !connectorEnabled(state, connectorID) || connectorCaptureMode(state, connectorID) != "bridged" {
		return BridgedIngestResult{}, fmt.Errorf("connector %s is not enabled in bridged mode", connectorID)
	}
	if err := enforceConnectorManagedSettings(connectorID); err != nil {
		return BridgedIngestResult{}, err
	}

	prepared := make([]preparedBridgedEvent, 0, len(envelopes))
	snapshotKeys := map[string]bool{}
	for index, envelope := range envelopes {
		var event preparedBridgedEvent
		switch {
		case claimedRequest != nil && claimedRequest.CaptureSemantics == "complete_snapshot" && connectorID == "slack.lists":
			event, err = prepareSlackListSnapshotEvent(*claimedRequest, envelope)
		case connectorID == "notion.activity":
			event, err = prepareNotionActivityEvent(envelope)
		default:
			event, err = prepareBridgedEvent(source, envelope)
		}
		if err != nil {
			return BridgedIngestResult{}, fmt.Errorf("event %d: %w", index+1, err)
		}
		if event.SnapshotKey != "" {
			if snapshotKeys[event.SnapshotKey] {
				return BridgedIngestResult{}, fmt.Errorf("event %d: duplicate Slack List snapshot row key", index+1)
			}
			snapshotKeys[event.SnapshotKey] = true
		}
		if !connectorAllowsBridgedEvent(connectorID, event.Type) {
			return BridgedIngestResult{}, fmt.Errorf("event %d: type %q is not allowed for connector %s", index+1, event.Type, connectorID)
		}
		prepared = append(prepared, event)
	}

	tx, err := db.Begin()
	if err != nil {
		return BridgedIngestResult{}, fmt.Errorf("begin capture ingest: %w", err)
	}
	defer tx.Rollback()

	result := BridgedIngestResult{
		HomeDir:      status.HomeDir,
		DatabasePath: status.DatabasePath,
		Source:       source,
		EventsRead:   len(prepared),
	}
	now := time.Now().UTC()
	createdAt := now.Format(time.RFC3339Nano)
	for _, event := range prepared {
		insert, err := tx.Exec(`INSERT OR IGNORE INTO events (
			id, source, type, timestamp, payload_json, project, actor, summary, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			event.ID, source, event.Type, event.Timestamp, event.PayloadJSON,
			emptyStringAsNull(event.Project), emptyStringAsNull(event.Actor),
			emptyStringAsNull(event.Summary), createdAt,
		)
		if err != nil {
			return BridgedIngestResult{}, fmt.Errorf("store event %s: %w", event.ID, err)
		}
		rows, err := insert.RowsAffected()
		if err != nil {
			return BridgedIngestResult{}, fmt.Errorf("count stored event %s: %w", event.ID, err)
		}
		if rows == 0 {
			result.EventsDuplicate++
		} else {
			result.EventsInserted++
		}
		if event.WeakDedupe {
			result.WeakDedupe++
		}
	}
	if claimedRequest != nil {
		if err := completeClaimedCaptureRequest(tx, *claimedRequest, config.ClaimToken, createdAt); err != nil {
			return BridgedIngestResult{}, err
		}
		result.RequestCompleted = true
	}
	if err := tx.Commit(); err != nil {
		return BridgedIngestResult{}, fmt.Errorf("commit capture ingest: %w", err)
	}
	if claimedRequest != nil {
		if err := recordConnectorCaptureCompletion(status.HomeDir, connectorID, now); err != nil {
			return BridgedIngestResult{}, fmt.Errorf("record connector capture completion: %w", err)
		}
	} else if err := recordConnectorIngest(status.HomeDir, connectorID, now); err != nil {
		return BridgedIngestResult{}, fmt.Errorf("record connector ingest: %w", err)
	}

	lines := []string{
		"Bridged capture ingest complete",
		"Source: " + result.Source,
		fmt.Sprintf("Events read: %d", result.EventsRead),
		fmt.Sprintf("Events inserted: %d", result.EventsInserted),
		fmt.Sprintf("Events deduplicated: %d", result.EventsDuplicate),
	}
	if result.RequestCompleted {
		lines = append(lines, "Request completed: "+claimedRequest.ID)
	}
	result.Message = strings.Join(lines, "\n")
	return result, nil
}

func bridgedConnectorID(source string) (string, error) {
	connectorID, err := normalizeConnectorID(source)
	if err != nil {
		return "", err
	}
	if err := validateBridgeableConnector(connectorID); err != nil {
		return "", err
	}
	return connectorID, nil
}

func bridgedConnectorIDForEvents(source string, events []bridgedEventEnvelope) (string, error) {
	if source == "slack" && len(events) > 0 {
		allListItems := true
		for _, event := range events {
			if strings.TrimSpace(event.Type) != "slack.list_item" {
				allListItems = false
				break
			}
		}
		if allListItems {
			return "slack.lists", nil
		}
	}
	return bridgedConnectorID(source)
}

func connectorAllowsBridgedEvent(connectorID string, eventType string) bool {
	switch connectorID {
	case "slack":
		return eventType == "slack.message" || eventType == "slack.reply" || eventType == "slack.thread_reply"
	case "slack.lists":
		return eventType == "slack.list_item"
	case "notion.activity":
		return eventType == "notion.page_updated" || eventType == "notion.database_updated"
	default:
		return strings.HasPrefix(eventType, eventSourceForConnector(connectorID)+".")
	}
}

func decodeBridgedEvents(input io.Reader) ([]bridgedEventEnvelope, error) {
	contents, err := io.ReadAll(io.LimitReader(input, maxBridgedIngestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read capture input: %w", err)
	}
	if len(contents) > maxBridgedIngestBytes {
		return nil, fmt.Errorf("capture input exceeds %d bytes", maxBridgedIngestBytes)
	}
	contents = bytes.TrimSpace(contents)
	if len(contents) == 0 {
		return nil, fmt.Errorf("capture input is empty")
	}
	if contents[0] == '[' {
		var events []bridgedEventEnvelope
		if err := decodeStrictJSON(contents, &events); err != nil {
			return nil, fmt.Errorf("parse capture JSON array: %w", err)
		}
		return events, nil
	}

	lines := bytes.Split(contents, []byte{'\n'})
	events := make([]bridgedEventEnvelope, 0, len(lines))
	for index, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var event bridgedEventEnvelope
		if err := decodeStrictJSON(line, &event); err != nil {
			return nil, fmt.Errorf("parse capture NDJSON line %d: %w", index+1, err)
		}
		events = append(events, event)
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("capture input contains no events")
	}
	return events, nil
}

func slackListSnapshotEnvelopesFromCSV(listID string, contents string) ([]bridgedEventEnvelope, error) {
	listID = strings.TrimSpace(listID)
	if listID == "" {
		return nil, fmt.Errorf("list_id is required with snapshot_csv")
	}
	if len(contents) > maxBridgedIngestBytes {
		return nil, fmt.Errorf("snapshot_csv exceeds %d bytes", maxBridgedIngestBytes)
	}
	reader := csv.NewReader(strings.NewReader(contents))
	header, err := reader.Read()
	if err == io.EOF {
		return nil, fmt.Errorf("snapshot_csv is empty")
	}
	if err != nil {
		return nil, fmt.Errorf("parse Slack List CSV header: %w", err)
	}
	seen := map[string]bool{}
	for index, column := range header {
		column = strings.TrimSpace(strings.TrimPrefix(column, "\ufeff"))
		key := strings.ToLower(column)
		if column == "" {
			return nil, fmt.Errorf("Slack List CSV column %d has an empty header", index+1)
		}
		if seen[key] {
			return nil, fmt.Errorf("Slack List CSV has duplicate column %q", column)
		}
		seen[key] = true
		header[index] = column
	}

	events := make([]bridgedEventEnvelope, 0)
	for recordNumber := 2; ; recordNumber++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse Slack List CSV record %d: %w", recordNumber, err)
		}
		fields := make(map[string]any, len(header))
		for index, column := range header {
			fields[column] = record[index]
		}
		payload, err := json.Marshal(slackListSnapshotInput{ListID: listID, Fields: fields})
		if err != nil {
			return nil, fmt.Errorf("encode Slack List CSV record %d: %w", recordNumber, err)
		}
		events = append(events, bridgedEventEnvelope{Type: "slack.list_item", Payload: payload})
	}
	return events, nil
}

func decodeStrictJSON(contents []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("unexpected data after JSON value")
	}
	return nil
}

func prepareBridgedEvent(source string, envelope bridgedEventEnvelope) (preparedBridgedEvent, error) {
	eventType := strings.TrimSpace(envelope.Type)
	if eventType == "" {
		return preparedBridgedEvent{}, fmt.Errorf("type is required")
	}
	if !strings.HasPrefix(eventType, source+".") {
		return preparedBridgedEvent{}, fmt.Errorf("type %q does not belong to source %q", eventType, source)
	}
	parsedTimestamp, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(envelope.Timestamp))
	if err != nil {
		return preparedBridgedEvent{}, fmt.Errorf("timestamp must be valid RFC3339: %w", err)
	}
	payloadJSON, err := canonicalJSONObject(envelope.Payload)
	if err != nil {
		return preparedBridgedEvent{}, fmt.Errorf("payload: %w", err)
	}

	externalID := strings.TrimSpace(envelope.ExternalID)
	weak := externalID == ""
	identity := source + "\x00" + externalID
	if weak {
		identity = source + "\x00" + eventType + "\x00" + parsedTimestamp.UTC().Format(time.RFC3339Nano) + "\x00" + payloadJSON
	}
	digest := sha256.Sum256([]byte(identity))
	event := preparedBridgedEvent{
		ID:          hex.EncodeToString(digest[:16]),
		Type:        eventType,
		Timestamp:   parsedTimestamp.UTC().Format(time.RFC3339Nano),
		PayloadJSON: payloadJSON,
		Project:     strings.TrimSpace(envelope.Project),
		Actor:       strings.TrimSpace(envelope.Actor),
		Summary:     strings.TrimSpace(envelope.Summary),
		WeakDedupe:  weak,
	}
	return event, nil
}

// prepareNotionActivityEvent mirrors the direct notion connector's event
// identity (eventType:externalID, unhashed) so overlapping capture between
// notion and notion.activity de-duplicates in the events table instead of
// storing the same edit twice.
func prepareNotionActivityEvent(envelope bridgedEventEnvelope) (preparedBridgedEvent, error) {
	eventType := strings.TrimSpace(envelope.Type)
	if eventType != "notion.page_updated" && eventType != "notion.database_updated" {
		return preparedBridgedEvent{}, fmt.Errorf("notion.activity requires type notion.page_updated or notion.database_updated")
	}
	externalID := strings.TrimSpace(envelope.ExternalID)
	if externalID == "" {
		return preparedBridgedEvent{}, fmt.Errorf("notion.activity requires external_id in the form <page-id>:<last-edited-time>")
	}
	parsedTimestamp, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(envelope.Timestamp))
	if err != nil {
		return preparedBridgedEvent{}, fmt.Errorf("timestamp must be valid RFC3339: %w", err)
	}
	payloadJSON, err := canonicalJSONObject(envelope.Payload)
	if err != nil {
		return preparedBridgedEvent{}, fmt.Errorf("payload: %w", err)
	}
	return preparedBridgedEvent{
		ID:          notionEventID(eventType, externalID),
		Type:        eventType,
		Timestamp:   parsedTimestamp.UTC().Format(time.RFC3339Nano),
		PayloadJSON: payloadJSON,
		Project:     strings.TrimSpace(envelope.Project),
		Actor:       strings.TrimSpace(envelope.Actor),
		Summary:     strings.TrimSpace(envelope.Summary),
	}, nil
}

type slackListSnapshotParams struct {
	Lists            []string                    `json:"lists"`
	DoneColumn       string                      `json:"done_column,omitempty"`
	RowKeyCandidates [][]string                  `json:"row_key_candidates"`
	ListOptions      map[string]SlackListOptions `json:"list_options,omitempty"`
}

type slackListSnapshotInput struct {
	ListID string         `json:"list_id"`
	Fields map[string]any `json:"fields"`
}

func validateSlackListRawSnapshotScope(request CaptureRequest, listID string) error {
	var params slackListSnapshotParams
	if err := decodeStrictJSON(request.Params, &params); err != nil {
		return fmt.Errorf("decode Slack List snapshot parameters: %w", err)
	}
	listID = strings.TrimSpace(listID)
	if len(params.Lists) != 1 || strings.TrimSpace(params.Lists[0]) != listID {
		return fmt.Errorf("snapshot_csv list_id must be the request's sole configured Slack List")
	}
	return nil
}

func prepareSlackListSnapshotEvent(request CaptureRequest, envelope bridgedEventEnvelope) (preparedBridgedEvent, error) {
	if strings.TrimSpace(envelope.Type) != "slack.list_item" {
		return preparedBridgedEvent{}, fmt.Errorf("complete Slack List snapshots require type %q", "slack.list_item")
	}
	if strings.TrimSpace(envelope.Timestamp) != "" || strings.TrimSpace(envelope.ExternalID) != "" {
		return preparedBridgedEvent{}, fmt.Errorf("Slack List snapshot rows must omit timestamp and external_id; workgraph derives them")
	}
	var params slackListSnapshotParams
	if err := decodeStrictJSON(request.Params, &params); err != nil {
		return preparedBridgedEvent{}, fmt.Errorf("decode Slack List snapshot parameters: %w", err)
	}
	var input slackListSnapshotInput
	if err := decodeStrictJSON(envelope.Payload, &input); err != nil {
		return preparedBridgedEvent{}, fmt.Errorf("decode Slack List snapshot payload: %w", err)
	}
	input.ListID = strings.TrimSpace(input.ListID)
	if input.ListID == "" || len(input.Fields) == 0 {
		return preparedBridgedEvent{}, fmt.Errorf("Slack List snapshot payload requires list_id and fields")
	}
	allowed := false
	for _, listID := range params.Lists {
		if input.ListID == strings.TrimSpace(listID) {
			allowed = true
			break
		}
	}
	if !allowed {
		return preparedBridgedEvent{}, fmt.Errorf("Slack List %q is outside the approved list scope", input.ListID)
	}
	options, hasListOptions := findSlackListOptions(params.ListOptions, input.ListID)
	rowKeyCandidates := options.RowKeyCandidates
	if len(rowKeyCandidates) == 0 {
		rowKeyCandidates = params.RowKeyCandidates
	}
	rowKey, err := slackListSnapshotRowKey(input.Fields, rowKeyCandidates)
	if err != nil {
		return preparedBridgedEvent{}, err
	}
	legacyDoneColumn := params.DoneColumn
	if hasListOptions {
		legacyDoneColumn = ""
	}
	done, interestFields, err := interpretSlackListFields(input.Fields, options, legacyDoneColumn)
	if err != nil {
		return preparedBridgedEvent{}, err
	}
	semantic := map[string]any{"fields": input.Fields, "list_id": input.ListID}
	if done != nil {
		semantic["done"] = *done
	}
	if len(interestFields) > 0 {
		semantic["interest_fields"] = interestFields
	}
	semanticJSON, err := json.Marshal(semantic)
	if err != nil {
		return preparedBridgedEvent{}, fmt.Errorf("encode Slack List semantic row: %w", err)
	}
	contentDigest := sha256.Sum256(semanticJSON)
	contentHash := hex.EncodeToString(contentDigest[:])
	observedAt, err := time.Parse(time.RFC3339Nano, request.Until)
	if err != nil {
		return preparedBridgedEvent{}, fmt.Errorf("parse Slack List snapshot observation time: %w", err)
	}
	payload := map[string]any{
		"capture_semantics": "complete_snapshot",
		"content_hash":      contentHash,
		"fields":            input.Fields,
		"list_id":           input.ListID,
		"observed_at":       observedAt.UTC().Format(time.RFC3339Nano),
		"row_key":           rowKey,
	}
	if done != nil {
		payload["done"] = *done
	}
	if len(interestFields) > 0 {
		payload["interest_fields"] = interestFields
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return preparedBridgedEvent{}, fmt.Errorf("encode Slack List snapshot payload: %w", err)
	}
	externalID := input.ListID + ":" + rowKey + ":" + contentHash
	eventDigest := sha256.Sum256([]byte("slack\x00" + externalID))
	summary := strings.TrimSpace(envelope.Summary)
	if summary == "" {
		if len(options.InterestColumns) > 0 {
			if value, found := slackListSnapshotField(input.Fields, options.InterestColumns[0]); found {
				summary = snapshotFieldText(value)
			}
		}
		if summary == "" {
			if title, found := slackListSnapshotField(input.Fields, "Title"); found {
				summary = snapshotFieldText(title)
			}
		}
	}
	project := strings.TrimSpace(envelope.Project)
	if project == "" {
		project = "slack-list:" + input.ListID
	}
	return preparedBridgedEvent{
		ID:          hex.EncodeToString(eventDigest[:16]),
		Type:        "slack.list_item",
		Timestamp:   observedAt.UTC().Format(time.RFC3339Nano),
		PayloadJSON: string(payloadJSON),
		Project:     project,
		Actor:       strings.TrimSpace(envelope.Actor),
		Summary:     summary,
		SnapshotKey: input.ListID + "\x00" + rowKey,
	}, nil
}

func slackListSnapshotRowKey(fields map[string]any, candidates [][]string) (string, error) {
	for _, candidate := range candidates {
		selected := make(map[string]string, len(candidate))
		complete := len(candidate) > 0
		for _, column := range candidate {
			value, found := slackListSnapshotField(fields, column)
			text := snapshotFieldText(value)
			if !found || text == "" {
				complete = false
				break
			}
			selected[column] = text
		}
		if complete {
			encoded, _ := json.Marshal(selected)
			digest := sha256.Sum256(encoded)
			return hex.EncodeToString(digest[:16]), nil
		}
	}
	return "", fmt.Errorf("Slack List row has no complete configured row-key candidate")
}

func slackListSnapshotField(fields map[string]any, name string) (any, bool) {
	name = strings.TrimSpace(name)
	for key, value := range fields {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return value, true
		}
	}
	return nil, false
}

func snapshotFieldText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case bool:
		return strconv.FormatBool(typed)
	default:
		encoded, _ := json.Marshal(typed)
		return strings.TrimSpace(string(encoded))
	}
}

func normalizeSlackListDone(value any) (bool, error) {
	if value == nil {
		return false, nil
	}
	if done, ok := value.(bool); ok {
		return done, nil
	}
	normalized := strings.ToLower(strings.TrimSpace(snapshotFieldText(value)))
	switch normalized {
	case "", "0", "false", "no", "n", "open", "todo", "unchecked", "☐":
		return false, nil
	case "1", "true", "yes", "y", "done", "checked", "x", "✓", "☑":
		return true, nil
	default:
		return false, fmt.Errorf("unsupported value %q", normalized)
	}
}

func canonicalJSONObject(raw json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", fmt.Errorf("is required")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("must be valid JSON: %w", err)
	}
	if _, ok := value.(map[string]any); !ok {
		return "", fmt.Errorf("must be a JSON object")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode canonical JSON: %w", err)
	}
	return string(canonical), nil
}

func bridgedDefaultDatabasePath(homeDir string) string {
	return filepath.Join(homeDir, "workgraph.db")
}
