package workgraph

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	llmAssociateConnectorID   = "llm.associate"
	captureRequestKindCapture = "capture"
	captureRequestKindLLMTask = "llm_task"
	llmAssociateDaemonWorker  = "daemon"
	llmAssociateDaemonLease   = 15 * time.Minute
	llmAssociateHourly        = time.Hour
	llmAssociateDailyHour     = 5
	llmAssociateDailyRequests = 10
)

type LLMAssociateScheduleConfig struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
	GitEmails    []string
	Now          time.Time
	Location     *time.Location
	HTTPClient   *http.Client
}

type LLMAssociateScheduleResult struct {
	Request  CaptureRequest
	Eligible bool
	Created  bool
	Executed bool
	Run      LLMAssociateResult
}

type LLMTask struct {
	Task     string                    `json:"task"`
	Profile  string                    `json:"profile"`
	Client   string                    `json:"client"`
	Model    string                    `json:"model,omitempty"`
	Messages []openAICompatibleMessage `json:"messages"`
}

type LLMTaskSubmitConfig struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
	GitEmails    []string
	RequestID    string
	ClaimToken   string
	Response     string
	Now          time.Time
	Location     *time.Location
}

type llmTaskParams struct {
	UnitIDs []string `json:"unit_ids"`
}

func ScheduleLLMAssociate(config LLMAssociateScheduleConfig) (LLMAssociateScheduleResult, error) {
	associate := config.associateConfig()
	if !llmAssociateScheduled(associate.HomeDir) {
		return LLMAssociateScheduleResult{}, nil
	}
	run, err := newAssociateRun(associate)
	if err != nil {
		return LLMAssociateScheduleResult{}, err
	}
	result, err := emitLLMAssociateRequest(associate, run)
	if err != nil || !result.Eligible || associateExecutesInBridge(run.profile) {
		return result, err
	}
	claims, err := claimCaptureRequests(CaptureRequestClaimConfig{
		HomeDir: associate.HomeDir, DatabasePath: associate.DatabasePath, ConnectorID: llmAssociateConnectorID,
		Kind: captureRequestKindLLMTask, Worker: llmAssociateDaemonWorker, Max: 1, Now: run.now, Lease: llmAssociateDaemonLease,
	})
	if err != nil || len(claims) == 0 {
		return result, err
	}
	result.Request = claims[0].Request
	result.Run, err = executeLLMTask(associate, run, claims[0])
	if err != nil {
		failErr := FailCaptureRequest(CaptureRequestCapabilityConfig{HomeDir: associate.HomeDir, DatabasePath: associate.DatabasePath, RequestID: claims[0].Request.ID, ClaimToken: claims[0].ClaimToken, Error: err.Error()})
		return result, errors.Join(err, failErr)
	}
	result.Executed = true
	return result, nil
}

func (config LLMAssociateScheduleConfig) associateConfig() LLMAssociateConfig {
	return LLMAssociateConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, MemoryDir: config.MemoryDir, GitEmails: config.GitEmails, Now: config.Now, Location: config.Location, HTTPClient: config.HTTPClient}
}

func llmAssociateScheduled(homeDir string) bool {
	homeDir, err := resolveLLMHomeDir(homeDir)
	if err != nil {
		return false
	}
	stored, err := readLLMConnectorConfig(homeDir)
	if err != nil || len(stored.TaskSources[llmTaskAssociate]) == 0 {
		return false
	}
	_, _, err = resolveLLMProfile(stored, "", associateRoute(stored))
	return err == nil
}

func associateExecutesInBridge(profile llmProfile) bool {
	return profile.Provider == "ai-client" && profile.Execution == llmExecutionBridge
}

func emitLLMAssociateRequest(config LLMAssociateConfig, run associateRun) (LLMAssociateScheduleResult, error) {
	if _, err := run.linkDeterministic(config); err != nil {
		return LLMAssociateScheduleResult{}, err
	}
	payload, err := buildAssociatePayload(run.homeDir, config, run.now, run.catalog, run.enabled, nil)
	if err != nil {
		return LLMAssociateScheduleResult{}, err
	}
	unitIDs := make([]string, 0, len(payload.Units))
	for _, unit := range payload.Units {
		unitIDs = append(unitIDs, unit.linkUnitID)
	}
	return emitLLMTaskRequest(config, unitIDs, run.now)
}

func emitLLMTaskRequest(config LLMAssociateConfig, unitIDs []string, now time.Time) (LLMAssociateScheduleResult, error) {
	status, err := prepareRunStatus(RunConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return LLMAssociateScheduleResult{}, err
	}
	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return LLMAssociateScheduleResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := createSchema(db); err != nil {
		return LLMAssociateScheduleResult{}, fmt.Errorf("prepare database schema: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return LLMAssociateScheduleResult{}, fmt.Errorf("begin llm task emission: %w", err)
	}
	defer tx.Rollback()
	if err := recycleExpiredCaptureClaims(tx, now); err != nil {
		return LLMAssociateScheduleResult{}, err
	}
	active, found, err := readActiveCaptureRequest(tx, llmAssociateConnectorID)
	if err != nil {
		return LLMAssociateScheduleResult{}, err
	}
	if found {
		return LLMAssociateScheduleResult{Request: active, Eligible: true}, tx.Commit()
	}
	if len(unitIDs) == 0 {
		return LLMAssociateScheduleResult{}, nil
	}
	requestID, err := newEventID()
	if err != nil {
		return LLMAssociateScheduleResult{}, fmt.Errorf("create llm task request id: %w", err)
	}
	params, err := json.Marshal(llmTaskParams{UnitIDs: unitIDs})
	if err != nil {
		return LLMAssociateScheduleResult{}, fmt.Errorf("encode llm task params: %w", err)
	}
	emittedAt := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`INSERT INTO capture_requests (
		id, connector_id, kind, since, until, params_json, status, attempts, available_at, created_at
	) VALUES (?, ?, ?, ?, ?, ?, 'pending', 0, ?, ?)`,
		requestID, llmAssociateConnectorID, captureRequestKindLLMTask, emittedAt, emittedAt, string(params), emittedAt, emittedAt); err != nil {
		return LLMAssociateScheduleResult{}, fmt.Errorf("emit llm task request: %w", err)
	}
	request, _, err := readCaptureRequest(tx, requestID)
	if err != nil {
		return LLMAssociateScheduleResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return LLMAssociateScheduleResult{}, fmt.Errorf("commit llm task request: %w", err)
	}
	return LLMAssociateScheduleResult{Request: request, Eligible: true, Created: true}, nil
}

func emitAssociateRequestNow(config LLMAssociateConfig, run associateRun) (LLMAssociateResult, error) {
	emitted, err := emitLLMAssociateRequest(config, run)
	if err != nil {
		return LLMAssociateResult{}, err
	}
	var lines []string
	switch {
	case emitted.Created:
		lines = []string{"LLM associate request emitted", "Request: " + emitted.Request.ID}
	case emitted.Eligible:
		lines = []string{"LLM associate request pending", "Request: " + emitted.Request.ID}
	default:
		lines = []string{"LLM associate complete"}
	}
	lines = append(lines, run.header(ProjectLinkRunResult{})...)
	if emitted.Eligible {
		lines = append(lines, "Execution: bridge worker")
	} else {
		lines = append(lines, "No eligible units for the enabled sources; no request was emitted.")
	}
	return LLMAssociateResult{ConfigPath: llmConfigPath(run.homeDir), Message: strings.Join(lines, "\n")}, nil
}

func executeLLMTask(config LLMAssociateConfig, run associateRun, claim ClaimedCaptureRequest) (LLMAssociateResult, error) {
	payload, err := llmTaskPayload(config, run, claim.Request.ID)
	if err != nil {
		return LLMAssociateResult{}, err
	}
	result := LLMAssociateResult{ConfigPath: llmConfigPath(run.homeDir), Sent: associatePayloadUnitIDs(payload)}
	if len(payload.Units) == 0 {
		return result, completeEmptyLLMTask(config, run, claim)
	}
	filtered, err := run.filteredMessages(payload)
	if err != nil {
		return LLMAssociateResult{}, err
	}
	response, err := run.call(config, filtered)
	if err != nil {
		return LLMAssociateResult{}, err
	}
	if err := run.apply(config, payload, response, completeLLMTaskTx(claim.Request.ID, claim.ClaimToken), &result); err != nil {
		return LLMAssociateResult{}, err
	}
	result.Message = llmTaskMessage(run, claim.Request.ID, result)
	return result, nil
}

func SubmitLLMTask(config LLMTaskSubmitConfig) (LLMAssociateResult, error) {
	associate := LLMAssociateConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, MemoryDir: config.MemoryDir, GitEmails: config.GitEmails, Now: config.Now, Location: config.Location}
	requestID := strings.TrimSpace(config.RequestID)
	if err := verifyLLMTaskClaim(associate, requestID, config.ClaimToken); err != nil {
		return LLMAssociateResult{}, err
	}
	run, err := newAssociateRun(associate)
	if err != nil {
		return LLMAssociateResult{}, err
	}
	payload, err := llmTaskPayload(associate, run, requestID)
	if err != nil {
		return LLMAssociateResult{}, err
	}
	result := LLMAssociateResult{ConfigPath: llmConfigPath(run.homeDir), Sent: associatePayloadUnitIDs(payload)}
	if err := run.apply(associate, payload, config.Response, completeLLMTaskTx(requestID, config.ClaimToken), &result); err != nil {
		return LLMAssociateResult{}, err
	}
	result.Message = llmTaskMessage(run, requestID, result)
	return result, nil
}

func llmTaskMessage(run associateRun, requestID string, result LLMAssociateResult) string {
	lines := append([]string{"LLM associate complete"}, run.header(ProjectLinkRunResult{})...)
	lines = append(lines, result.reportLines()...)
	return strings.Join(append(lines, "Request completed: "+requestID), "\n")
}

func verifyLLMTaskClaim(config LLMAssociateConfig, requestID string, claimToken string) error {
	dbPath, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	var kind, status, token, leaseExpires string
	err = db.QueryRow(`SELECT kind, status, COALESCE(claim_token, ''), COALESCE(lease_expires_at, '')
		FROM capture_requests WHERE id = ?`, requestID).Scan(&kind, &status, &token, &leaseExpires)
	if err == sql.ErrNoRows {
		return fmt.Errorf("llm task request %s was not found", requestID)
	}
	if err != nil {
		return fmt.Errorf("read llm task request: %w", err)
	}
	if kind != captureRequestKindLLMTask {
		return fmt.Errorf("capture request %s is not an llm_task request", requestID)
	}
	expires, parseErr := time.Parse(time.RFC3339Nano, leaseExpires)
	if status != "claimed" || strings.TrimSpace(claimToken) == "" || token != claimToken || parseErr != nil || !expires.After(time.Now()) {
		return errStaleLLMTaskClaim
	}
	return nil
}

var errStaleLLMTaskClaim = errors.New("llm task claim token is stale, invalid, or expired")

func completeLLMTaskTx(requestID string, claimToken string) func(*sql.Tx, time.Time) error {
	return func(tx *sql.Tx, _ time.Time) error {
		completedAt := time.Now().UTC().Format(time.RFC3339Nano)
		updated, err := tx.Exec(`UPDATE capture_requests SET
			status = 'completed', completed_at = ?, claim_token = NULL, lease_expires_at = NULL
			WHERE id = ? AND kind = ? AND status = 'claimed' AND claim_token = ? AND lease_expires_at > ?`,
			completedAt, requestID, captureRequestKindLLMTask, claimToken, completedAt)
		if err != nil {
			return fmt.Errorf("complete llm task request %s: %w", requestID, err)
		}
		if count, _ := updated.RowsAffected(); count != 1 {
			return errStaleLLMTaskClaim
		}
		return nil
	}
}

func completeEmptyLLMTask(config LLMAssociateConfig, run associateRun, claim ClaimedCaptureRequest) error {
	return withProjectLinksTx(run.store(config), completeLLMTaskTx(claim.Request.ID, claim.ClaimToken))
}

func llmTaskPayload(config LLMAssociateConfig, run associateRun, requestID string) (associatePayload, error) {
	unitIDs, err := storedLLMTaskUnitIDs(config, requestID)
	if err != nil {
		return associatePayload{}, err
	}
	return buildAssociatePayload(run.homeDir, config, run.now, run.catalog, run.enabled, unitIDs)
}

func storedLLMTaskUnitIDs(config LLMAssociateConfig, requestID string) ([]string, error) {
	dbPath, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	var raw string
	if err := db.QueryRow(`SELECT params_json FROM capture_requests WHERE id = ? AND kind = ?`, requestID, captureRequestKindLLMTask).Scan(&raw); err != nil {
		return nil, fmt.Errorf("read llm task request %s: %w", requestID, err)
	}
	var params llmTaskParams
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil, fmt.Errorf("parse llm task params: %w", err)
	}
	if params.UnitIDs == nil {
		params.UnitIDs = []string{}
	}
	return params.UnitIDs, nil
}

func llmTaskParamsSummary(raw string) json.RawMessage {
	var params llmTaskParams
	_ = json.Unmarshal([]byte(raw), &params)
	return json.RawMessage(fmt.Sprintf(`{"unit_count":%d}`, len(params.UnitIDs)))
}

func attachLLMTasks(config CaptureRequestClaimConfig, claimed []ClaimedCaptureRequest) ([]ClaimedCaptureRequest, error) {
	associate := LLMAssociateConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, MemoryDir: config.MemoryDir, GitEmails: config.GitEmails, Now: config.Now}
	run, err := newAssociateRun(associate)
	if err != nil {
		return nil, err
	}
	tasks := make([]ClaimedCaptureRequest, 0, len(claimed))
	for _, claim := range claimed {
		task, err := prepareLLMTask(associate, run, claim)
		if err != nil {
			if failErr := FailCaptureRequest(CaptureRequestCapabilityConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, RequestID: claim.Request.ID, ClaimToken: claim.ClaimToken, Error: err.Error()}); failErr != nil {
				return nil, errors.Join(err, failErr)
			}
			continue
		}
		if task == nil {
			continue
		}
		claim.Task = task
		tasks = append(tasks, claim)
	}
	return tasks, nil
}

func prepareLLMTask(config LLMAssociateConfig, run associateRun, claim ClaimedCaptureRequest) (*LLMTask, error) {
	payload, err := llmTaskPayload(config, run, claim.Request.ID)
	if err != nil {
		return nil, err
	}
	if len(payload.Units) == 0 {
		return nil, completeEmptyLLMTask(config, run, claim)
	}
	if err := run.checkOutboundPolicy(config); err != nil {
		return nil, err
	}
	filtered, err := run.filteredMessages(payload)
	if err != nil {
		return nil, err
	}
	return &LLMTask{Task: llmTaskAssociate, Profile: run.profileName, Client: run.profile.Client, Model: run.profile.Model, Messages: filtered.Messages}, nil
}

func llmAssociateBridgeRouted(config CaptureRequestClaimConfig) bool {
	run, err := newAssociateRun(LLMAssociateConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, MemoryDir: config.MemoryDir})
	return err == nil && associateExecutesInBridge(run.profile)
}

func normalizeCaptureRequestKind(kind string) (string, error) {
	switch strings.TrimSpace(kind) {
	case "", captureRequestKindCapture:
		return captureRequestKindCapture, nil
	case captureRequestKindLLMTask:
		return captureRequestKindLLMTask, nil
	default:
		return "", fmt.Errorf("capture request kind must be %s or %s", captureRequestKindCapture, captureRequestKindLLMTask)
	}
}

func normalizeCaptureRequestConnectorID(id string) (string, error) {
	if strings.ToLower(strings.TrimSpace(id)) == llmAssociateConnectorID {
		return llmAssociateConnectorID, nil
	}
	return normalizeConnectorID(id)
}

type llmAssociateCadence struct {
	lastHourly     time.Time
	dailyDay       string
	dailyRemaining int
}

func newLLMAssociateCadence(now time.Time) llmAssociateCadence {
	cadence := llmAssociateCadence{}
	if now.Hour() >= llmAssociateDailyHour {
		cadence.dailyDay = now.Format(time.DateOnly)
	}
	return cadence
}

func (cadence *llmAssociateCadence) due(now time.Time) bool {
	if day := now.Format(time.DateOnly); now.Hour() >= llmAssociateDailyHour && cadence.dailyDay != day {
		cadence.dailyDay = day
		cadence.dailyRemaining = llmAssociateDailyRequests
	}
	return cadence.dailyRemaining > 0 || cadence.lastHourly.IsZero() || now.Sub(cadence.lastHourly) >= llmAssociateHourly
}

func (cadence *llmAssociateCadence) record(now time.Time, result LLMAssociateScheduleResult, err error) {
	cadence.lastHourly = now
	switch {
	case cadence.dailyRemaining == 0:
	case err != nil || !result.Eligible:
		cadence.dailyRemaining = 0
	case result.Created:
		cadence.dailyRemaining--
	}
}
