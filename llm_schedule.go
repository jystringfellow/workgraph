package workgraph

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
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
	llmAssociateCooldown      = 24 * time.Hour
	associateCooldownsSchema  = `CREATE TABLE IF NOT EXISTS associate_cooldowns (
		unit_id TEXT PRIMARY KEY,
		members_hash TEXT NOT NULL,
		until TEXT NOT NULL
	)`
)

type LLMAssociateScheduleConfig struct {
	Context      context.Context
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
	Task    string                         `json:"task,omitempty"`
	UnitIDs []string                       `json:"unit_ids"`
	Sent    map[string]string              `json:"sent,omitempty"`
	Primary map[string]associateEscalation `json:"primary,omitempty"`

	Profile  string             `json:"profile,omitempty"`
	Snapshot *associateSnapshot `json:"snapshot,omitempty"`
}

type llmTaskWork struct {
	task           string
	payload        associatePayload
	sent           associateSent
	primaries      []ProjectLinkProposal
	claimedProfile string
}

var errLLMTaskRouteChanged = errors.New("llm task route changed since the claim; fail this request so the routed profile can claim it again")

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
	if err != nil || !result.Eligible {
		return result, err
	}
	params, err := storedLLMTaskParams(associate, result.Request.ID)
	if err != nil {
		return result, err
	}
	if _, profile, routed := run.taskProfile(params.Task); routed && associateExecutesInBridge(profile) {
		return result, nil
	}
	directTasks := run.claimableTasks(func(profile llmProfile, routed bool) bool { return !routed || !associateExecutesInBridge(profile) })
	if len(directTasks) == 0 {
		return result, nil
	}
	claims, err := claimCaptureRequests(CaptureRequestClaimConfig{
		HomeDir: associate.HomeDir, DatabasePath: associate.DatabasePath, ConnectorID: llmAssociateConnectorID,
		Kind: captureRequestKindLLMTask, Worker: llmAssociateDaemonWorker, Max: 1, Now: run.now, Lease: llmAssociateDaemonLease,
		llmTasks: directTasks,
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
	return LLMAssociateConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, MemoryDir: config.MemoryDir, GitEmails: config.GitEmails, Now: config.Now, Location: config.Location, HTTPClient: config.HTTPClient, ctx: config.Context}
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
	cooled, err := activeAssociateCooldowns(config, run.now)
	if err != nil {
		return LLMAssociateScheduleResult{}, err
	}
	payload, err := buildAssociatePayload(run.homeDir, config, run.now, run.catalog, run.enabled, nil, cooled)
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
	db, err := openDatabase(status.DatabasePath)
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
	work, err := loadLLMTaskWork(config, run, claim.Request.ID)
	if err != nil {
		return LLMAssociateResult{}, err
	}
	result := LLMAssociateResult{ConfigPath: llmConfigPath(run.homeDir), Sent: associatePayloadUnitIDs(work.payload)}
	if len(work.payload.Units) == 0 {
		return result, completeEmptyLLMTask(config, run, claim)
	}
	completion := llmTaskCompletion(claim.Request.ID, claim.ClaimToken, work.payload)
	if work.task == llmTaskAssociateVerify {
		verified := work.primaries
		result.Escalated = len(work.primaries)
		if run.hasVerifier {
			result.verifier = run.verifierName
			verified = run.verifyDirect(config, work.sent, work.primaries, &result)
		}
		if err := run.write(config, work.payload, verified, associateDeferral{}, claim.Request.ID, completion, &result); err != nil {
			return LLMAssociateResult{}, err
		}
		result.Message = llmTaskMessage(run, claim.Request.ID, result)
		return result, nil
	}
	if run.profile.Provider == llmProviderSystemOne {
		return executeSystemOneTask(config, run, claim, work.payload, completion, result)
	}
	filtered, err := run.filteredMessages(work.payload)
	if err != nil {
		return LLMAssociateResult{}, err
	}
	response, err := run.call(config, filtered)
	if err != nil {
		return LLMAssociateResult{}, err
	}
	if err := run.apply(config, work.payload, run.sentFrom(work.payload, filtered), response, claim.Request.ID, completion, &result); err != nil {
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
	work, err := loadLLMTaskWork(associate, run, requestID)
	if err != nil {
		return LLMAssociateResult{}, err
	}
	if name, _, routed := run.taskProfile(work.task); work.claimedProfile != "" && (!routed || name != work.claimedProfile) {
		return LLMAssociateResult{}, errLLMTaskRouteChanged
	}
	result := LLMAssociateResult{ConfigPath: llmConfigPath(run.homeDir), Sent: associatePayloadUnitIDs(work.payload)}
	completion := llmTaskCompletion(requestID, config.ClaimToken, work.payload)
	if work.task == llmTaskAssociateVerify {
		result.verifier, result.Escalated = run.verifierName, len(work.primaries)
		verified, err := run.mergeVerdicts(work.payload, work.primaries, config.Response, &result)
		if err != nil {
			return LLMAssociateResult{}, err
		}
		err = run.write(associate, work.payload, verified, associateDeferral{}, requestID, completion, &result)
	} else {
		err = run.apply(associate, work.payload, work.sent, config.Response, requestID, completion, &result)
	}
	if err != nil {
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
	db, err := openDatabase(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := createSchema(db); err != nil {
		return fmt.Errorf("prepare capture outbox: %w", err)
	}
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

func llmTaskCompletion(requestID string, claimToken string, payload associatePayload) func(*sql.Tx, time.Time, ProjectLinkRunResult) error {
	return func(tx *sql.Tx, now time.Time, written ProjectLinkRunResult) error {
		if err := completeLLMTaskTx(requestID, claimToken)(tx, now); err != nil {
			return err
		}
		sent, err := sentLLMTaskMembers(tx, requestID)
		if err != nil {
			return err
		}
		return recordAssociateCooldowns(tx, now, payload, written, sent)
	}
}

func recordAssociateCooldowns(tx *sql.Tx, now time.Time, payload associatePayload, written ProjectLinkRunResult, sent map[string]string) error {
	progressed := map[string]bool{}
	for _, link := range written.Written {
		progressed[link.UnitID] = true
	}
	for _, unitID := range written.Skipped {
		progressed[unitID] = true
	}
	if _, err := tx.Exec(associateCooldownsSchema); err != nil {
		return fmt.Errorf("ensure associate cooldowns: %w", err)
	}
	until := now.Add(llmAssociateCooldown).UTC().Format(time.RFC3339Nano)
	for _, unit := range payload.Units {
		hash := associateMembersHash(unit)
		if progressed[unit.linkUnitID] || (sent != nil && sent[unit.linkUnitID] != hash) {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO associate_cooldowns (unit_id, members_hash, until) VALUES (?, ?, ?)
			ON CONFLICT(unit_id) DO UPDATE SET members_hash = excluded.members_hash, until = excluded.until`,
			unit.linkUnitID, hash, until); err != nil {
			return fmt.Errorf("record associate cooldown for %s: %w", unit.linkUnitID, err)
		}
	}
	return nil
}

func sentLLMTaskMembers(tx *sql.Tx, requestID string) (map[string]string, error) {
	var raw string
	if err := tx.QueryRow(`SELECT params_json FROM capture_requests WHERE id = ?`, requestID).Scan(&raw); err != nil {
		return nil, fmt.Errorf("read llm task request %s: %w", requestID, err)
	}
	var params llmTaskParams
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil, fmt.Errorf("parse llm task params: %w", err)
	}
	return params.Sent, nil
}

func recordSentLLMTaskMembers(config LLMAssociateConfig, claim ClaimedCaptureRequest, payload associatePayload, profile string, snapshot *associateSnapshot) error {
	params, err := storedLLMTaskParams(config, claim.Request.ID)
	if err != nil {
		return err
	}
	params.Profile = profile
	if snapshot != nil {
		params.Snapshot = snapshot
	}
	params.Sent = map[string]string{}
	for _, unit := range payload.Units {
		params.Sent[unit.linkUnitID] = associateMembersHash(unit)
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode llm task params: %w", err)
	}
	dbPath, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return err
	}
	db, err := openDatabase(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	updated, err := db.Exec(`UPDATE capture_requests SET params_json = ? WHERE id = ? AND status = 'claimed' AND claim_token = ?`, string(encoded), claim.Request.ID, claim.ClaimToken)
	if err != nil {
		return fmt.Errorf("record llm task members: %w", err)
	}
	if count, _ := updated.RowsAffected(); count != 1 {
		return errStaleLLMTaskClaim
	}
	return nil
}

func activeAssociateCooldowns(config LLMAssociateConfig, now time.Time) (map[string]string, error) {
	dbPath, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return nil, err
	}
	db, err := openDatabase(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if _, err := db.Exec(associateCooldownsSchema); err != nil {
		return nil, fmt.Errorf("ensure associate cooldowns: %w", err)
	}
	rows, err := db.Query(`SELECT unit_id, members_hash FROM associate_cooldowns WHERE until > ?`, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("read associate cooldowns: %w", err)
	}
	defer rows.Close()
	cooled := map[string]string{}
	for rows.Next() {
		var unitID, hash string
		if err := rows.Scan(&unitID, &hash); err != nil {
			return nil, fmt.Errorf("read associate cooldown: %w", err)
		}
		cooled[unitID] = hash
	}
	return cooled, rows.Err()
}

func associateMembersHash(unit associateUnit) string {
	members := make([]string, 0, len(unit.members))
	for id := range unit.members {
		members = append(members, id)
	}
	sort.Strings(members)
	digest := sha256.Sum256([]byte(strings.Join(members, "\n")))
	return hex.EncodeToString(digest[:])
}

func completeEmptyLLMTask(config LLMAssociateConfig, run associateRun, claim ClaimedCaptureRequest) error {
	return withProjectLinksTx(run.store(config), completeLLMTaskTx(claim.Request.ID, claim.ClaimToken))
}

func loadLLMTaskWork(config LLMAssociateConfig, run associateRun, requestID string) (llmTaskWork, error) {
	params, err := storedLLMTaskParams(config, requestID)
	if err != nil {
		return llmTaskWork{}, err
	}
	payload, err := buildAssociatePayload(run.homeDir, config, run.now, run.catalog, run.enabled, params.UnitIDs, nil)
	if err != nil {
		return llmTaskWork{}, err
	}
	work := llmTaskWork{task: params.Task, payload: payload, claimedProfile: params.Profile}
	switch {
	case params.Task == llmTaskAssociateVerify:
		base := params.Snapshot.within(payload)
		work.payload = verifyPayloadFor(base.payload, params.Primary)
		work.sent = associateSent{payload: work.payload, hosted: base.hosted}
		work.primaries = primaryProposals(work.payload, params.Primary)
	case params.Snapshot != nil:
		work.sent = params.Snapshot.within(payload)
	default:
		work.sent = (*associateSnapshot)(nil).within(payload)
	}
	return work, nil
}

func storedLLMTaskParams(config LLMAssociateConfig, requestID string) (llmTaskParams, error) {
	dbPath, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return llmTaskParams{}, err
	}
	db, err := openDatabase(dbPath)
	if err != nil {
		return llmTaskParams{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	var raw string
	if err := db.QueryRow(`SELECT params_json FROM capture_requests WHERE id = ? AND kind = ?`, requestID, captureRequestKindLLMTask).Scan(&raw); err != nil {
		return llmTaskParams{}, fmt.Errorf("read llm task request %s: %w", requestID, err)
	}
	var params llmTaskParams
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return llmTaskParams{}, fmt.Errorf("parse llm task params: %w", err)
	}
	if params.UnitIDs == nil {
		params.UnitIDs = []string{}
	}
	return params, nil
}

func activeLLMAssociateRequest(config LLMAssociateConfig) (string, bool, error) {
	dbPath, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return "", false, err
	}
	db, err := openDatabase(dbPath)
	if err != nil {
		return "", false, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := createSchema(db); err != nil {
		return "", false, fmt.Errorf("prepare capture outbox: %w", err)
	}
	var id string
	err = db.QueryRow(`SELECT id FROM capture_requests WHERE connector_id = ? AND status IN ('pending', 'claimed') LIMIT 1`, llmAssociateConnectorID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read active llm task request: %w", err)
	}
	return id, true, nil
}

func activeLLMTaskName(config LLMAssociateConfig) string {
	id, active, err := activeLLMAssociateRequest(config)
	if err != nil || !active {
		return ""
	}
	params, err := storedLLMTaskParams(config, id)
	if err != nil {
		return ""
	}
	return params.Task
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
	work, err := loadLLMTaskWork(config, run, claim.Request.ID)
	if err != nil {
		return nil, err
	}
	if len(work.payload.Units) == 0 {
		return nil, completeEmptyLLMTask(config, run, claim)
	}
	name, profile, routed := run.taskProfile(work.task)
	if !routed {
		return nil, fmt.Errorf("no llm profile is routed for %s", llmTaskAssociateVerify)
	}
	if !associateExecutesInBridge(profile) || !strings.HasPrefix(strings.TrimSpace(claim.Request.ClaimedBy), profile.Client+"-") {
		return nil, fmt.Errorf("llm task %s is routed to %s, not to this worker", claim.Request.ID, name)
	}
	var filtered llmOutboundFilterResult
	var snapshot *associateSnapshot
	if work.task == llmTaskAssociateVerify {
		if err := run.checkVerifierPolicy(config); err != nil {
			return nil, err
		}
		filtered, err = run.filteredVerifyMessages(work.sent)
	} else {
		if err := run.checkOutboundPolicy(config); err != nil {
			return nil, err
		}
		filtered, err = run.filteredMessages(work.payload)
		snapshot = snapshotOf(run.sentFrom(work.payload, filtered))
	}
	if err != nil {
		return nil, err
	}
	if err := recordSentLLMTaskMembers(config, claim, work.payload, name, snapshot); err != nil {
		return nil, err
	}
	return &LLMTask{Task: firstNonEmpty(work.task, llmTaskAssociate), Profile: name, Client: profile.Client, Model: profile.Model, Messages: filtered.Messages}, nil
}

func routedBridgeLLMTaskProfile(config LLMAssociateConfig) (llmProfile, bool) {
	run, err := newAssociateRun(config)
	if err != nil {
		return llmProfile{}, false
	}
	_, profile, routed := run.taskProfile(activeLLMTaskName(config))
	return profile, routed && associateExecutesInBridge(profile)
}

func bridgeLLMTaskProfile(homeDir string, client string) (llmProfile, bool) {
	profile, routed := routedBridgeLLMTaskProfile(LLMAssociateConfig{HomeDir: homeDir})
	if !routed || profile.Client != client {
		return llmProfile{}, false
	}
	return profile, true
}

func (run associateRun) claimableTasks(allowed func(llmProfile, bool) bool) []string {
	var tasks []string
	for _, task := range []string{"", llmTaskAssociateVerify} {
		if _, profile, routed := run.taskProfile(task); allowed(profile, routed) {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

func bridgeClaimableLLMTasks(config CaptureRequestClaimConfig) []string {
	run, err := newAssociateRun(LLMAssociateConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, MemoryDir: config.MemoryDir})
	if err != nil {
		return nil
	}
	worker := strings.TrimSpace(config.Worker)
	return run.claimableTasks(func(profile llmProfile, routed bool) bool {
		return routed && associateExecutesInBridge(profile) && strings.HasPrefix(worker, profile.Client+"-")
	})
}

func captureRequestClaimableAt(request CaptureRequest, now time.Time) bool {
	available, err := time.Parse(time.RFC3339Nano, request.AvailableAt)
	return request.Status == "pending" && err == nil && !available.After(now)
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
