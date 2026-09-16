package workgraph

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const maxBridgedIngestBytes = 16 << 20

const (
	bridgedInitialWindow = 24 * time.Hour
	bridgedOverlap       = 5 * time.Minute
	bridgedClaimLease    = 5 * time.Minute
)

// CaptureRequest is one durable unit of provider work for an approved bridge.
type CaptureRequest struct {
	ID             string          `json:"id"`
	ConnectorID    string          `json:"connector_id"`
	Source         string          `json:"source"`
	Since          string          `json:"since"`
	Until          string          `json:"until"`
	Params         json.RawMessage `json:"params"`
	Status         string          `json:"status"`
	Attempts       int             `json:"attempts"`
	AvailableAt    string          `json:"available_at"`
	ClaimedBy      string          `json:"claimed_by,omitempty"`
	ClaimedAt      string          `json:"claimed_at,omitempty"`
	LeaseExpiresAt string          `json:"lease_expires_at,omitempty"`
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
	var storedToken, requestStatus, leaseExpires string
	err = db.QueryRow(`SELECT connector_id, attempts, COALESCE(claim_token, ''), status, COALESCE(lease_expires_at, '')
		FROM capture_requests WHERE id = ?`, strings.TrimSpace(config.RequestID)).Scan(
		&connectorID, &attempts, &storedToken, &requestStatus, &leaseExpires)
	if err != nil {
		return fmt.Errorf("read capture request: %w", err)
	}
	if requestStatus != "claimed" || storedToken != config.ClaimToken {
		return fmt.Errorf("capture request claim token is stale or invalid")
	}
	leaseTime, err := time.Parse(time.RFC3339Nano, leaseExpires)
	if err != nil || !leaseTime.After(now) {
		return fmt.Errorf("capture request claim lease has expired")
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
	if id == "git" {
		return CaptureRequestEmitResult{}, fmt.Errorf("connector git only supports direct capture")
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
		ID:          requestID,
		ConnectorID: id,
		Source:      eventSourceForConnector(id),
		Since:       since.Format(time.RFC3339Nano),
		Until:       now.Format(time.RFC3339Nano),
		Params:      params,
		Status:      "pending",
		AvailableAt: now.Format(time.RFC3339Nano),
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
	return connectorID
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
	HomeDir      string
	DatabasePath string
	Source       string
	RequestID    string
	ClaimToken   string
	Input        io.Reader
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
	NotionProjection int
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
	Notion      *bridgedNotionProjection
}

type bridgedNotionProjection struct {
	ID             string
	ObjectType     string
	Title          string
	URL            string
	ParentJSON     string
	PropertiesJSON string
	Preview        string
	CreatedTime    string
	CreatedBy      string
	LastEditedTime string
	LastEditedBy   string
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
	if !connectorEnabled(state, connectorID) || connectorCaptureMode(state, connectorID) != "bridged" {
		return BridgedIngestResult{}, fmt.Errorf("connector %s is not enabled in bridged mode", connectorID)
	}
	if err := enforceConnectorManagedSettings(connectorID); err != nil {
		return BridgedIngestResult{}, err
	}

	prepared := make([]preparedBridgedEvent, 0, len(envelopes))
	for index, envelope := range envelopes {
		event, err := prepareBridgedEvent(source, envelope)
		if err != nil {
			return BridgedIngestResult{}, fmt.Errorf("event %d: %w", index+1, err)
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
		if event.Notion != nil {
			if err := projectBridgedNotionEvent(tx, *event.Notion, createdAt); err != nil {
				return BridgedIngestResult{}, err
			}
			result.NotionProjection++
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
		fmt.Sprintf("Notion projections: %d", result.NotionProjection),
	}
	if result.RequestCompleted {
		lines = append(lines, "Request completed: "+claimedRequest.ID)
	}
	result.Message = strings.Join(lines, "\n")
	return result, nil
}

func bridgedConnectorID(source string) (string, error) {
	if source == "git" {
		return "", fmt.Errorf("connector git only supports direct capture")
	}
	return normalizeConnectorID(source)
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
	if source == "notion" {
		projection, err := prepareBridgedNotionProjection(eventType, json.RawMessage(payloadJSON))
		if err != nil {
			return preparedBridgedEvent{}, err
		}
		event.Notion = &projection
	}
	return event, nil
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

func prepareBridgedNotionProjection(eventType string, payload json.RawMessage) (bridgedNotionProjection, error) {
	var page struct {
		ID               string          `json:"id"`
		URL              string          `json:"url"`
		Title            string          `json:"title"`
		Path             string          `json:"path"`
		Properties       json.RawMessage `json:"properties"`
		Preview          string          `json:"preview"`
		CreatedTime      string          `json:"created_time"`
		CreatedBy        string          `json:"created_by"`
		PageLastEditedAt string          `json:"page_last_edited_at"`
		LastEditedTime   string          `json:"last_edited_time"`
		LastEditedBy     string          `json:"last_edited_by"`
	}
	if err := json.Unmarshal(payload, &page); err != nil {
		return bridgedNotionProjection{}, fmt.Errorf("parse Notion projection: %w", err)
	}
	if strings.TrimSpace(page.ID) == "" {
		return bridgedNotionProjection{}, fmt.Errorf("Notion payload id is required")
	}
	objectType := strings.TrimPrefix(eventType, "notion.")
	if objectType == "" {
		return bridgedNotionProjection{}, fmt.Errorf("Notion object type is required")
	}
	lastEdited := page.PageLastEditedAt
	if strings.TrimSpace(lastEdited) == "" {
		lastEdited = page.LastEditedTime
	}
	lastEdited, err := normalizeOptionalRFC3339(lastEdited, true)
	if err != nil {
		return bridgedNotionProjection{}, fmt.Errorf("Notion last edited time: %w", err)
	}
	createdTime, err := normalizeOptionalRFC3339(page.CreatedTime, false)
	if err != nil {
		return bridgedNotionProjection{}, fmt.Errorf("Notion created time: %w", err)
	}
	propertiesJSON := "{}"
	if len(bytes.TrimSpace(page.Properties)) > 0 && string(bytes.TrimSpace(page.Properties)) != "null" {
		propertiesJSON, err = canonicalJSONObject(page.Properties)
		if err != nil {
			return bridgedNotionProjection{}, fmt.Errorf("Notion properties: %w", err)
		}
	}
	parentJSON, err := json.Marshal(map[string]string{"path": strings.TrimSpace(page.Path)})
	if err != nil {
		return bridgedNotionProjection{}, fmt.Errorf("encode Notion parent: %w", err)
	}
	return bridgedNotionProjection{
		ID:             strings.TrimSpace(page.ID),
		ObjectType:     objectType,
		Title:          strings.TrimSpace(page.Title),
		URL:            strings.TrimSpace(page.URL),
		ParentJSON:     string(parentJSON),
		PropertiesJSON: propertiesJSON,
		Preview:        truncateUTF8(strings.TrimSpace(page.Preview), 4000),
		CreatedTime:    createdTime,
		CreatedBy:      strings.TrimSpace(page.CreatedBy),
		LastEditedTime: lastEdited,
		LastEditedBy:   strings.TrimSpace(page.LastEditedBy),
	}, nil
}

func normalizeOptionalRFC3339(value string, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		if required {
			return "", fmt.Errorf("is required")
		}
		return "", nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", err
	}
	return parsed.UTC().Format(time.RFC3339Nano), nil
}

func truncateUTF8(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return strings.TrimSpace(value)
}

func projectBridgedNotionEvent(tx *sql.Tx, page bridgedNotionProjection, now string) error {
	var existingLastEdited string
	err := tx.QueryRow(`SELECT COALESCE(last_edited_time, '') FROM notion_index WHERE notion_id = ?`, page.ID).Scan(&existingLastEdited)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read bridged Notion page %s: %w", page.ID, err)
	}
	if err == nil && existingLastEdited != "" {
		existingTime, parseErr := time.Parse(time.RFC3339Nano, existingLastEdited)
		if parseErr != nil {
			return fmt.Errorf("parse indexed Notion timestamp for %s: %w", page.ID, parseErr)
		}
		incomingTime, _ := time.Parse(time.RFC3339Nano, page.LastEditedTime)
		if incomingTime.Before(existingTime) {
			return nil
		}
	}
	_, err = tx.Exec(`INSERT INTO notion_index (
		notion_id, object_type, title, url, parent_json, properties_json,
		content_preview, content_synced_at, created_time, created_by,
		last_edited_time, last_edited_by, source, first_seen_at, last_seen_at, last_synced_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(notion_id) DO UPDATE SET
		object_type = excluded.object_type,
		title = excluded.title,
		url = excluded.url,
		parent_json = excluded.parent_json,
		properties_json = excluded.properties_json,
		content_preview = excluded.content_preview,
		content_synced_at = excluded.content_synced_at,
		created_time = COALESCE(excluded.created_time, notion_index.created_time),
		created_by = COALESCE(excluded.created_by, notion_index.created_by),
		last_edited_time = excluded.last_edited_time,
		last_edited_by = excluded.last_edited_by,
		source = excluded.source,
		last_seen_at = excluded.last_seen_at,
		last_synced_at = excluded.last_synced_at`,
		page.ID, page.ObjectType, emptyStringAsNull(page.Title), emptyStringAsNull(page.URL),
		page.ParentJSON, page.PropertiesJSON, emptyStringAsNull(page.Preview), now,
		emptyStringAsNull(page.CreatedTime), emptyStringAsNull(page.CreatedBy),
		page.LastEditedTime, emptyStringAsNull(page.LastEditedBy), "bridged", now, now, now,
	)
	if err != nil {
		return fmt.Errorf("project bridged Notion page %s: %w", page.ID, err)
	}
	return nil
}

func bridgedDefaultDatabasePath(homeDir string) string {
	return filepath.Join(homeDir, "workgraph.db")
}
