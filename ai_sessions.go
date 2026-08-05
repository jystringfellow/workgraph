package workgraph

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const aiSchemaVersion = 1
const aiDirtyPathLimit = 500

// AIRunConfig controls one wrapped CLI AI child-process lifetime.
type AIRunConfig struct {
	HomeDir              string
	DatabasePath         string
	WorkingDir           string
	Command              []string
	PredecessorSessionID string
	Stdin                io.Reader
	Stdout               io.Writer
	Stderr               io.Writer
}

// AIRunResult reports the shell exit status selected for the wrapped child.
type AIRunResult struct {
	SessionID string
	ExitCode  int
}

// AIObservedState is repository state collected by workgraph.
type AIObservedState struct {
	ObservedAt          string   `json:"observed_at"`
	CWD                 string   `json:"cwd"`
	WorktreeRoot        string   `json:"worktree_root"`
	GitCommonDir        string   `json:"git_common_dir"`
	Branch              string   `json:"branch"`
	Head                string   `json:"head"`
	DirtyPaths          []string `json:"dirty_paths"`
	DirtyPathCount      int      `json:"dirty_path_count"`
	DirtyPathsTruncated bool     `json:"dirty_paths_truncated"`
}

type aiStartedPayload struct {
	SchemaVersion        int             `json:"schema_version"`
	SessionID            string          `json:"session_id"`
	Tool                 string          `json:"tool"`
	ToolPath             string          `json:"tool_path,omitempty"`
	NativeSessionID      string          `json:"native_session_id,omitempty"`
	PredecessorSessionID string          `json:"predecessor_session_id,omitempty"`
	PID                  int             `json:"pid"`
	BootIdentity         string          `json:"boot_identity,omitempty"`
	ProcessStartIdentity string          `json:"process_start_identity,omitempty"`
	Observed             AIObservedState `json:"observed"`
}

type aiNativeBoundPayload struct {
	SchemaVersion        int    `json:"schema_version"`
	SessionID            string `json:"session_id"`
	Tool                 string `json:"tool"`
	NativeSessionID      string `json:"native_session_id"`
	Source               string `json:"source"`
	PredecessorSessionID string `json:"predecessor_session_id,omitempty"`
}

type aiOutcome struct {
	Kind     string `json:"kind"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Signal   string `json:"signal,omitempty"`
}

type aiEndedPayload struct {
	SchemaVersion     int              `json:"schema_version"`
	SessionID         string           `json:"session_id"`
	Outcome           aiOutcome        `json:"outcome"`
	ObservationStatus string           `json:"observation_status"`
	Observed          *AIObservedState `json:"observed,omitempty"`
}

// AICheckpointConfig controls one explicit structured checkpoint append.
type AICheckpointConfig struct {
	HomeDir          string
	DatabasePath     string
	HomeExplicit     bool
	DatabaseExplicit bool
	SessionID        string
	WorkingDir       string
	Input            io.Reader
}

// AICheckpointResult identifies the session and immutable event that were stored.
type AICheckpointResult struct {
	SessionID string
	EventID   string
}

type aiCheckpointPayload struct {
	SchemaVersion int             `json:"schema_version"`
	SessionID     string          `json:"session_id"`
	Observed      AIObservedState `json:"observed"`
	AgentStated   map[string]any  `json:"agent_stated"`
}

// AIProcessInspection describes current evidence for one PID.
type AIProcessInspection struct {
	Exists        bool
	StartIdentity string
}

// AIProcessInspector isolates platform process evidence for deterministic facts.
type AIProcessInspector interface {
	CurrentBootIdentity() (string, error)
	InspectProcess(pid int) (AIProcessInspection, error)
}

// AISessionsConfig controls read-only AI session listing.
type AISessionsConfig struct {
	HomeDir          string
	DatabasePath     string
	ProcessInspector AIProcessInspector
	Location         *time.Location
}

// AISessionsResult contains the derived local session overview.
type AISessionsResult struct {
	Sessions []AISessionSummary
	Warnings []string
	Message  string
}

// AISessionSummary is one event-derived AI session row.
type AISessionSummary struct {
	SessionID            string
	Tool                 string
	NativeSessionID      string
	PredecessorSessionID string
	Project              string
	Status               string
	StartedAt            time.Time
	LatestCheckpointAt   *time.Time
	LatestEventAt        time.Time
}

// AIShowConfig controls deterministic rendering for one stored session.
type AIShowConfig struct {
	HomeDir          string
	DatabasePath     string
	SessionID        string
	ProcessInspector AIProcessInspector
	Location         *time.Location
}

// AIShowResult contains one stored session handoff.
type AIShowResult struct {
	Session AISessionSummary
	Message string
}

type aiSessionProjection struct {
	Summary            AISessionSummary
	Started            *aiStartedPayload
	LatestCheckpoint   *aiCheckpointPayload
	CheckpointAt       time.Time
	Ended              *aiEndedPayload
	LatestNative       *aiNativeBoundPayload
	NativeBoundAt      time.Time
	EndedExists        bool
	Warnings           []string
	latestEventID      string
	latestCheckpointID string
	latestNativeID     string
	LatestObserved     *AIObservedState
	ObservedEventAt    time.Time
	ObservedSource     string
	observedEventID    string
}

type aiEventEnvelope struct {
	SchemaVersion int    `json:"schema_version"`
	SessionID     string `json:"session_id"`
}

// RunAISession launches a child directly and records its local lifecycle.
func RunAISession(config AIRunConfig) (AIRunResult, error) {
	if len(config.Command) == 0 {
		return AIRunResult{}, errors.New("agent command is required")
	}
	if _, present := os.LookupEnv("WORKGRAPH_AI_SESSION_ID"); present {
		message := "nested AI sessions are not supported"
		if config.PredecessorSessionID != "" {
			message += "; run resume outside the wrapped agent, or unset it if it is stale"
		}
		return AIRunResult{}, errors.New(message)
	}

	status, err := prepareRunStatus(RunConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return AIRunResult{}, err
	}
	workingDir := config.WorkingDir
	if workingDir == "" {
		workingDir, err = os.Getwd()
		if err != nil {
			return AIRunResult{}, fmt.Errorf("find AI session working directory: %w", err)
		}
	}
	workingDir, err = canonicalAIPath(workingDir)
	if err != nil {
		return AIRunResult{}, fmt.Errorf("resolve AI session working directory: %w", err)
	}

	resolvedExecutable, err := exec.LookPath(config.Command[0])
	if err != nil {
		return AIRunResult{}, fmt.Errorf("resolve AI tool %q: %w", filepath.Base(config.Command[0]), err)
	}
	resolvedExecutable, err = canonicalAIPath(resolvedExecutable)
	if err != nil {
		return AIRunResult{}, fmt.Errorf("resolve AI tool path %q: %w", filepath.Base(config.Command[0]), err)
	}
	tool := filepath.Base(resolvedExecutable)
	now := time.Now().UTC()
	observed, project, err := observeAIWorkingState(workingDir, now)
	if err != nil {
		return AIRunResult{}, fmt.Errorf("collect AI launch observation: %w", err)
	}
	sessionID, err := newAIUUID()
	if err != nil {
		return AIRunResult{}, fmt.Errorf("create AI session id: %w", err)
	}

	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return AIRunResult{}, fmt.Errorf("open AI event database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return AIRunResult{}, fmt.Errorf("open AI event database: %w", err)
	}
	predecessorSessionID := strings.TrimSpace(config.PredecessorSessionID)
	nativeLaunch, err := aiPrepareNativeLaunch(tool, config.Command[1:], status.HomeDir)
	if err != nil {
		return AIRunResult{}, fmt.Errorf("prepare AI native binding: %w", err)
	}
	nativeSessionID := nativeLaunch.NativeSessionID
	if predecessorSessionID == "" && nativeSessionID != "" {
		predecessorSessionID, err = findLatestAISessionByNativeID(db, tool, nativeSessionID, "")
		if err != nil {
			return AIRunResult{}, fmt.Errorf("find resumed AI session: %w", err)
		}
	}

	command := exec.Command(resolvedExecutable, nativeLaunch.Args...)
	command.Dir = workingDir
	command.Stdin = config.Stdin
	command.Stdout = config.Stdout
	command.Stderr = config.Stderr
	command.Env = aiChildEnvironment(os.Environ(), sessionID, status.HomeDir, status.DatabasePath, nativeLaunch.Environment)
	if err := command.Start(); err != nil {
		return AIRunResult{}, fmt.Errorf("start AI tool %q: %w", tool, err)
	}

	startedAt := time.Now().UTC()
	started := aiStartedPayload{
		SchemaVersion:        aiSchemaVersion,
		SessionID:            sessionID,
		Tool:                 tool,
		ToolPath:             resolvedExecutable,
		NativeSessionID:      nativeSessionID,
		PredecessorSessionID: predecessorSessionID,
		PID:                  command.Process.Pid,
		BootIdentity:         currentAIBootIdentity(),
		ProcessStartIdentity: currentAIProcessStartIdentity(command.Process.Pid),
		Observed:             observed,
	}
	if err := insertAIEvent(db, "ai.session_started:"+sessionID, "ai.session_started", startedAt, project, "AI session started ("+tool+")", started); err != nil {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
		return AIRunResult{}, fmt.Errorf("persist AI session start: %w", err)
	}

	waitErr := command.Wait()
	outcome, exitCode, outcomeErr := aiChildOutcome(command.ProcessState, waitErr)
	if outcomeErr != nil {
		return AIRunResult{}, outcomeErr
	}
	endedAt := time.Now().UTC()
	ended := aiEndedPayload{
		SchemaVersion:     aiSchemaVersion,
		SessionID:         sessionID,
		Outcome:           outcome,
		ObservationStatus: "unavailable",
	}
	var finalObserved AIObservedState
	var observeErr error
	if observed.WorktreeRoot == "" {
		finalObserved, observeErr = observeAINonGitWorkingState(workingDir, endedAt)
	} else {
		finalObserved, _, observeErr = observeAIWorkingState(workingDir, endedAt)
	}
	if observeErr != nil {
		if config.Stderr != nil {
			fmt.Fprintf(config.Stderr, "workgraph ai run: final observation unavailable: %v\n", observeErr)
		}
	} else {
		ended.ObservationStatus = "captured"
		ended.Observed = &finalObserved
	}
	summary := aiEndedSummary(outcome)
	if err := insertAIEvent(db, "ai.session_ended:"+sessionID, "ai.session_ended", endedAt, project, summary, ended); err != nil && config.Stderr != nil {
		fmt.Fprintf(config.Stderr, "workgraph ai run: persist AI session end: %v\n", err)
	}

	return AIRunResult{SessionID: sessionID, ExitCode: exitCode}, nil
}

// CheckpointAISession validates agent-stated input and appends observed state.
func CheckpointAISession(config AICheckpointConfig) (AICheckpointResult, error) {
	if config.HomeExplicit {
		if injected := os.Getenv("WORKGRAPH_AI_HOME"); injected != "" && !sameAIPath(config.HomeDir, injected) {
			return AICheckpointResult{}, errors.New("explicit home disagrees with WORKGRAPH_AI_HOME")
		}
	}
	if config.DatabaseExplicit {
		if injected := os.Getenv("WORKGRAPH_AI_DATABASE"); injected != "" && !sameAIPath(config.DatabasePath, injected) {
			return AICheckpointResult{}, errors.New("explicit database disagrees with WORKGRAPH_AI_DATABASE")
		}
	}
	sessionID := strings.TrimSpace(config.SessionID)
	environmentSessionID := strings.TrimSpace(os.Getenv("WORKGRAPH_AI_SESSION_ID"))
	if sessionID == "" {
		sessionID = environmentSessionID
	} else if environmentSessionID != "" && sessionID != environmentSessionID {
		return AICheckpointResult{}, errors.New("explicit session id disagrees with WORKGRAPH_AI_SESSION_ID")
	}
	if sessionID == "" {
		return AICheckpointResult{}, errors.New("session id is required")
	}
	agentStated, err := validateAICheckpointInput(config.Input)
	if err != nil {
		return AICheckpointResult{}, err
	}
	status, err := prepareRunStatus(RunConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return AICheckpointResult{}, err
	}
	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return AICheckpointResult{}, fmt.Errorf("open AI event database: %w", err)
	}
	defer db.Close()

	var startedJSON string
	var project string
	readStarted := func() error {
		return db.QueryRow(`SELECT payload_json, COALESCE(project, '') FROM events
			WHERE id = ? AND source = 'ai' AND type = 'ai.session_started'`, "ai.session_started:"+sessionID).Scan(&startedJSON, &project)
	}
	err = readStarted()
	if errors.Is(err, sql.ErrNoRows) && environmentSessionID != "" && sessionID == environmentSessionID {
		deadline := time.Now().Add(2 * time.Second)
		for errors.Is(err, sql.ErrNoRows) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			err = readStarted()
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return AICheckpointResult{}, fmt.Errorf("AI session %q was not found", sessionID)
	}
	if err != nil {
		return AICheckpointResult{}, fmt.Errorf("read AI session start: %w", err)
	}
	var endedCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events
		WHERE id = ? AND source = 'ai' AND type = 'ai.session_ended'`, "ai.session_ended:"+sessionID).Scan(&endedCount); err != nil {
		return AICheckpointResult{}, fmt.Errorf("read AI session lifecycle: %w", err)
	}
	if endedCount != 0 {
		return AICheckpointResult{}, errors.New("AI session has ended")
	}
	var started aiStartedPayload
	if err := json.Unmarshal([]byte(startedJSON), &started); err != nil || started.SchemaVersion != aiSchemaVersion {
		return AICheckpointResult{}, errors.New("AI session start payload is unsupported or invalid")
	}

	workingDir := config.WorkingDir
	if workingDir == "" {
		workingDir, err = os.Getwd()
		if err != nil {
			return AICheckpointResult{}, fmt.Errorf("find checkpoint working directory: %w", err)
		}
	}
	workingDir, err = canonicalAIPath(workingDir)
	if err != nil {
		return AICheckpointResult{}, fmt.Errorf("resolve checkpoint working directory: %w", err)
	}
	now := time.Now().UTC()
	var observed AIObservedState
	if started.Observed.WorktreeRoot != "" {
		observed, _, err = observeAIWorkingState(workingDir, now)
		if err != nil {
			return AICheckpointResult{}, fmt.Errorf("collect checkpoint observation: %w", err)
		}
		if observed.WorktreeRoot != started.Observed.WorktreeRoot || observed.GitCommonDir != started.Observed.GitCommonDir {
			return AICheckpointResult{}, errors.New("checkpoint checkout differs from the session checkout")
		}
	} else {
		if !aiPathWithin(started.Observed.CWD, workingDir) {
			return AICheckpointResult{}, errors.New("checkpoint directory is outside the non-Git session directory")
		}
		observed, err = observeAINonGitWorkingState(workingDir, now)
		if err != nil {
			return AICheckpointResult{}, fmt.Errorf("collect non-Git checkpoint observation: %w", err)
		}
	}

	eventUUID, err := newAIUUID()
	if err != nil {
		return AICheckpointResult{}, fmt.Errorf("create checkpoint event id: %w", err)
	}
	eventID := "ai.session_checkpointed:" + sessionID + ":" + eventUUID
	payload := aiCheckpointPayload{SchemaVersion: aiSchemaVersion, SessionID: sessionID, Observed: observed, AgentStated: agentStated}
	if err := insertAIEvent(db, eventID, "ai.session_checkpointed", now, project, "AI session checkpointed", payload); err != nil {
		return AICheckpointResult{}, fmt.Errorf("persist AI checkpoint: %w", err)
	}
	return AICheckpointResult{SessionID: sessionID, EventID: eventID}, nil
}

// ListAISessions projects every known local AI session from append-only events.
func ListAISessions(config AISessionsConfig) (AISessionsResult, error) {
	projections, warnings, err := loadAISessionProjections(config.HomeDir, config.DatabasePath, config.ProcessInspector)
	if err != nil {
		return AISessionsResult{}, err
	}
	summaries := make([]AISessionSummary, 0, len(projections))
	for _, projection := range projections {
		summaries = append(summaries, projection.Summary)
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].LatestEventAt.Equal(summaries[j].LatestEventAt) {
			return summaries[i].SessionID < summaries[j].SessionID
		}
		return summaries[i].LatestEventAt.After(summaries[j].LatestEventAt)
	})
	location := config.Location
	if location == nil {
		location = time.Local
	}
	result := AISessionsResult{Sessions: summaries, Warnings: warnings}
	result.Message = aiSessionsMessage(result, location)
	return result, nil
}

// ShowAISession renders only supported evidence already stored in events.
func ShowAISession(config AIShowConfig) (AIShowResult, error) {
	projections, _, err := loadAISessionProjections(config.HomeDir, config.DatabasePath, config.ProcessInspector)
	if err != nil {
		return AIShowResult{}, err
	}
	var selected *aiSessionProjection
	for index := range projections {
		if projections[index].Summary.SessionID == config.SessionID {
			selected = &projections[index]
			break
		}
	}
	if selected == nil {
		return AIShowResult{}, fmt.Errorf("AI session %q was not found", config.SessionID)
	}
	location := config.Location
	if location == nil {
		location = time.Local
	}
	result := AIShowResult{Session: selected.Summary}
	result.Message = aiShowMessage(selected, location)
	return result, nil
}

func loadAISessionProjections(homeDir string, databasePath string, inspector AIProcessInspector) ([]aiSessionProjection, []string, error) {
	status, err := prepareRunStatus(RunConfig{HomeDir: homeDir, DatabasePath: databasePath})
	if err != nil {
		return nil, nil, err
	}
	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return nil, nil, fmt.Errorf("open AI event database: %w", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, type, timestamp, COALESCE(project, ''), payload_json
		FROM events WHERE source = 'ai'`)
	if err != nil {
		return nil, nil, fmt.Errorf("query AI events: %w", err)
	}
	defer rows.Close()
	byID := make(map[string]*aiSessionProjection)
	var warnings []string
	for rows.Next() {
		var id, eventType, timestampText, project, payloadText string
		if err := rows.Scan(&id, &eventType, &timestampText, &project, &payloadText); err != nil {
			return nil, nil, fmt.Errorf("scan AI event: %w", err)
		}
		timestamp, err := time.Parse(time.RFC3339Nano, timestampText)
		if err != nil {
			warnings = append(warnings, "ignored AI event with invalid timestamp: "+id)
			continue
		}
		var envelope aiEventEnvelope
		if err := json.Unmarshal([]byte(payloadText), &envelope); err != nil || envelope.SessionID == "" {
			warnings = append(warnings, "ignored AI event with invalid envelope: "+id)
			continue
		}
		projection := byID[envelope.SessionID]
		if projection == nil {
			projection = &aiSessionProjection{Summary: AISessionSummary{SessionID: envelope.SessionID, Project: project, Tool: "unknown"}}
			byID[envelope.SessionID] = projection
		}
		if projection.Summary.LatestEventAt.IsZero() || timestamp.After(projection.Summary.LatestEventAt) || (timestamp.Equal(projection.Summary.LatestEventAt) && id > projection.latestEventID) {
			projection.Summary.LatestEventAt = timestamp
			projection.latestEventID = id
		}
		if envelope.SchemaVersion != aiSchemaVersion {
			projection.Warnings = append(projection.Warnings, fmt.Sprintf("unsupported schema version %d at %s", envelope.SchemaVersion, timestamp.Format(time.RFC3339Nano)))
			if eventType == "ai.session_ended" {
				projection.EndedExists = true
			}
			continue
		}
		switch eventType {
		case "ai.session_started":
			var started aiStartedPayload
			if err := json.Unmarshal([]byte(payloadText), &started); err != nil {
				projection.Warnings = append(projection.Warnings, "invalid supported start payload")
				continue
			}
			projection.Started = &started
			projection.Summary.Tool = started.Tool
			projection.Summary.StartedAt = timestamp
			selectAILatestObserved(projection, &started.Observed, timestamp, id, "start")
		case "ai.session_checkpointed":
			var checkpoint aiCheckpointPayload
			if err := json.Unmarshal([]byte(payloadText), &checkpoint); err != nil {
				projection.Warnings = append(projection.Warnings, "invalid supported checkpoint payload")
				continue
			}
			if projection.LatestCheckpoint == nil || timestamp.After(projection.CheckpointAt) || (timestamp.Equal(projection.CheckpointAt) && id > projection.latestCheckpointID) {
				projection.LatestCheckpoint = &checkpoint
				projection.CheckpointAt = timestamp
				projection.latestCheckpointID = id
			}
			selectAILatestObserved(projection, &checkpoint.Observed, timestamp, id, "checkpoint")
		case "ai.session_native_bound":
			var bound aiNativeBoundPayload
			if err := json.Unmarshal([]byte(payloadText), &bound); err != nil {
				projection.Warnings = append(projection.Warnings, "invalid supported native binding payload")
				continue
			}
			if projection.LatestNative == nil || timestamp.After(projection.NativeBoundAt) || (timestamp.Equal(projection.NativeBoundAt) && id > projection.latestNativeID) {
				projection.LatestNative = &bound
				projection.NativeBoundAt = timestamp
				projection.latestNativeID = id
			}
		case "ai.session_ended":
			projection.EndedExists = true
			var ended aiEndedPayload
			if err := json.Unmarshal([]byte(payloadText), &ended); err == nil {
				projection.Ended = &ended
				if ended.Observed != nil {
					selectAILatestObserved(projection, ended.Observed, timestamp, id, "end")
				}
			} else {
				projection.Warnings = append(projection.Warnings, "invalid supported end payload")
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate AI events: %w", err)
	}
	if inspector == nil {
		inspector = localAIProcessInspector{}
	}
	projections := make([]aiSessionProjection, 0, len(byID))
	for _, projection := range byID {
		if projection.LatestNative != nil {
			projection.Summary.NativeSessionID = projection.LatestNative.NativeSessionID
			projection.Summary.PredecessorSessionID = projection.LatestNative.PredecessorSessionID
		} else if projection.Started != nil {
			projection.Summary.NativeSessionID = projection.Started.NativeSessionID
			projection.Summary.PredecessorSessionID = projection.Started.PredecessorSessionID
		}
		projection.Summary.Status = deriveAIStatus(projection, inspector)
		if projection.LatestCheckpoint != nil {
			checkpointAt := projection.CheckpointAt
			projection.Summary.LatestCheckpointAt = &checkpointAt
		}
		warnings = append(warnings, projection.Warnings...)
		projections = append(projections, *projection)
	}
	return projections, warnings, nil
}

func selectAILatestObserved(projection *aiSessionProjection, observed *AIObservedState, eventAt time.Time, eventID string, source string) {
	if observed == nil {
		return
	}
	if projection.LatestObserved == nil || eventAt.After(projection.ObservedEventAt) || (eventAt.Equal(projection.ObservedEventAt) && eventID > projection.observedEventID) {
		copy := *observed
		copy.DirtyPaths = append([]string{}, observed.DirtyPaths...)
		projection.LatestObserved = &copy
		projection.ObservedEventAt = eventAt
		projection.ObservedSource = source
		projection.observedEventID = eventID
	}
}

func deriveAIStatus(projection *aiSessionProjection, inspector AIProcessInspector) string {
	if projection.EndedExists {
		return "ended"
	}
	if projection.Started == nil {
		return "unknown"
	}
	currentBoot, bootErr := inspector.CurrentBootIdentity()
	if bootErr == nil && projection.Started.BootIdentity != "" && currentBoot != "" && projection.Started.BootIdentity != currentBoot {
		return "interrupted"
	}
	process, err := inspector.InspectProcess(projection.Started.PID)
	if err != nil {
		return "unknown"
	}
	if !process.Exists {
		return "interrupted"
	}
	if projection.Started.ProcessStartIdentity == "" || process.StartIdentity == "" {
		return "unknown"
	}
	if projection.Started.ProcessStartIdentity == process.StartIdentity {
		return "running"
	}
	return "interrupted"
}

type localAIProcessInspector struct{}

func (localAIProcessInspector) CurrentBootIdentity() (string, error) {
	return currentAIBootIdentity(), nil
}

func (localAIProcessInspector) InspectProcess(pid int) (AIProcessInspection, error) {
	return inspectLocalAIProcess(pid)
}

func aiSessionsMessage(result AISessionsResult, location *time.Location) string {
	if len(result.Sessions) == 0 {
		return "No AI sessions recorded."
	}
	lines := []string{"AI sessions"}
	for _, session := range result.Sessions {
		project := session.Project
		if project == "" {
			project = "-"
		}
		checkpoint := "-"
		if session.LatestCheckpointAt != nil {
			checkpoint = session.LatestCheckpointAt.In(location).Format(time.RFC3339)
		}
		started := "-"
		if !session.StartedAt.IsZero() {
			started = session.StartedAt.In(location).Format(time.RFC3339)
		}
		lines = append(lines,
			fmt.Sprintf("%s %s %s", session.SessionID, session.Tool, session.Status),
			"  native session: "+safeAITerminalText(aiDisplayValue(session.NativeSessionID)),
			"  predecessor: "+safeAITerminalText(aiDisplayValue(session.PredecessorSessionID)),
			"  project: "+project,
			"  started: "+started,
			"  checkpoint: "+checkpoint,
			"  latest event: "+session.LatestEventAt.In(location).Format(time.RFC3339),
		)
	}
	for _, warning := range result.Warnings {
		lines = append(lines, "warning: "+warning)
	}
	return strings.Join(lines, "\n")
}

func aiShowMessage(session *aiSessionProjection, location *time.Location) string {
	project := session.Summary.Project
	if project == "" {
		project = "-"
	}
	lines := []string{
		"AI session " + safeAITerminalText(session.Summary.SessionID),
		"Tool: " + safeAITerminalText(session.Summary.Tool),
		"Native session: " + safeAITerminalText(aiDisplayValue(session.Summary.NativeSessionID)),
		"Predecessor: " + safeAITerminalText(aiDisplayValue(session.Summary.PredecessorSessionID)),
		"Status: " + session.Summary.Status,
		"Project: " + safeAITerminalText(project),
	}
	if session.LatestObserved != nil {
		observed := session.LatestObserved
		lines = append(lines, "", "Observed repository state",
			"Source: "+session.ObservedSource,
			"Observed: "+safeAITerminalText(observed.ObservedAt),
			"CWD: "+safeAITerminalText(observed.CWD),
		)
		if observed.WorktreeRoot != "" {
			lines = append(lines,
				"Worktree: "+safeAITerminalText(observed.WorktreeRoot),
				"Branch: "+safeAITerminalText(observed.Branch),
				"HEAD: "+safeAITerminalText(observed.Head),
				fmt.Sprintf("Dirty paths: %d", observed.DirtyPathCount),
			)
			for _, path := range observed.DirtyPaths {
				lines = append(lines, "- "+safeAITerminalText(path))
			}
			if observed.DirtyPathsTruncated {
				lines = append(lines, "- … truncated")
			}
		}
	}
	if session.LatestCheckpoint == nil {
		lines = append(lines, "", "No agent-stated checkpoint recorded.")
	} else {
		lines = append(lines, "", "Agent-stated checkpoint", "Recorded: "+session.CheckpointAt.In(location).Format(time.RFC3339))
		for _, field := range []struct {
			key   string
			label string
			array bool
		}{
			{"goal", "Goal", false},
			{"completed", "Completed", true},
			{"current_state", "Current state", false},
			{"next_actions", "Next actions", true},
			{"blockers", "Blockers", true},
			{"decisions", "Decisions", true},
		} {
			value, present := session.LatestCheckpoint.AgentStated[field.key]
			if !present {
				continue
			}
			lines = append(lines, field.label)
			if field.array {
				items, _ := value.([]any)
				if len(items) == 0 {
					lines = append(lines, "- None")
				}
				for _, item := range items {
					lines = append(lines, "- "+fmt.Sprint(item))
				}
			} else {
				lines = append(lines, fmt.Sprint(value))
			}
		}
	}
	if session.Ended != nil {
		lines = append(lines, "", "Outcome: "+aiOutcomeText(session.Ended.Outcome))
	}
	for _, warning := range session.Warnings {
		lines = append(lines, "Warning: "+safeAITerminalText(warning))
	}
	return strings.Join(lines, "\n")
}

func aiDisplayValue(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func aiOutcomeText(outcome aiOutcome) string {
	switch outcome.Kind {
	case "exited":
		if outcome.ExitCode != nil {
			return fmt.Sprintf("exit %d", *outcome.ExitCode)
		}
	case "signaled":
		return "signal " + outcome.Signal
	}
	return "unknown"
}

func safeAITerminalText(value string) string {
	value = strings.ToValidUTF8(value, "�")
	var builder strings.Builder
	for _, character := range value {
		if character <= 0x1f || (character >= 0x7f && character <= 0x9f) {
			fmt.Fprintf(&builder, "\\x%02X", character)
			continue
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

func validateAICheckpointInput(input io.Reader) (map[string]any, error) {
	if input == nil {
		return nil, errors.New("checkpoint stdin is required")
	}
	contents, err := io.ReadAll(io.LimitReader(input, 65537))
	if err != nil {
		return nil, errors.New("read checkpoint stdin")
	}
	if len(contents) > 65536 {
		return nil, errors.New("checkpoint exceeds 65,536 bytes")
	}
	if !utf8.Valid(contents) {
		return nil, errors.New("checkpoint must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	first, err := decoder.Token()
	if err != nil {
		return nil, errors.New("checkpoint must be one JSON object")
	}
	if delimiter, ok := first.(json.Delim); !ok || delimiter != '{' {
		return nil, errors.New("checkpoint must be one JSON object")
	}
	allowed := map[string]bool{"goal": true, "current_state": true, "completed": true, "next_actions": true, "blockers": true, "decisions": true}
	arrayField := map[string]bool{"completed": true, "next_actions": true, "blockers": true, "decisions": true}
	seen := make(map[string]bool)
	result := make(map[string]any)
	meaningful := false
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, errors.New("checkpoint object key is invalid")
		}
		field, ok := token.(string)
		if !ok {
			return nil, errors.New("checkpoint object key is invalid")
		}
		if seen[field] {
			return nil, fmt.Errorf("checkpoint field %q is duplicated", field)
		}
		seen[field] = true
		if !allowed[field] {
			return nil, fmt.Errorf("checkpoint field %q is unknown", field)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, fmt.Errorf("checkpoint field %q is invalid", field)
		}
		if arrayField[field] {
			values, hasText, err := validateAIStringArray(field, raw)
			if err != nil {
				return nil, err
			}
			result[field] = values
			meaningful = meaningful || hasText
			continue
		}
		value, err := validateAIString(field, raw, 16*1024)
		if err != nil {
			return nil, err
		}
		result[field] = value
		meaningful = meaningful || value != ""
	}
	if _, err := decoder.Token(); err != nil {
		return nil, errors.New("checkpoint JSON object is incomplete")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("checkpoint contains trailing JSON data")
	}
	if !meaningful {
		return nil, errors.New("checkpoint must contain meaningful text")
	}
	return result, nil
}

func validateAIStringArray(field string, raw json.RawMessage) ([]string, bool, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		return nil, false, fmt.Errorf("checkpoint field %q must be an array of strings", field)
	}
	if len(items) > 100 {
		return nil, false, fmt.Errorf("checkpoint field %q has more than 100 items", field)
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		value, err := validateAIString(field, item, 4*1024)
		if err != nil {
			return nil, false, err
		}
		if value == "" {
			return nil, false, fmt.Errorf("checkpoint field %q contains an empty item", field)
		}
		values = append(values, value)
	}
	return values, len(values) != 0, nil
}

func validateAIString(field string, raw json.RawMessage, maximum int) (string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", fmt.Errorf("checkpoint field %q may not be null", field)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("checkpoint field %q must be a string", field)
	}
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
	value = strings.TrimSpace(value)
	if len([]byte(value)) > maximum {
		return "", fmt.Errorf("checkpoint field %q is too large", field)
	}
	for _, character := range value {
		if (character <= 0x1f || (character >= 0x7f && character <= 0x9f)) && character != '\n' && character != '\t' {
			return "", fmt.Errorf("checkpoint field %q contains a control character", field)
		}
	}
	patterns, _, err := llmOutboundRedactionPatterns()
	if err != nil {
		return "", fmt.Errorf("load checkpoint sensitive patterns: %w", err)
	}
	for _, pattern := range patterns {
		if pattern.Pattern.MatchString(value) {
			return "", fmt.Errorf("checkpoint field %q contains a %s credential pattern", field, pattern.Name)
		}
	}
	return value, nil
}

func aiPathWithin(root string, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func sameAIPath(left string, right string) bool {
	leftPath, leftErr := canonicalAIPath(left)
	rightPath, rightErr := canonicalAIPath(right)
	return leftErr == nil && rightErr == nil && leftPath == rightPath
}

func insertAIEvent(db *sql.DB, id string, eventType string, timestamp time.Time, project string, summary string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	timestampText := timestamp.UTC().Format(time.RFC3339Nano)
	result, err := db.Exec(`INSERT OR IGNORE INTO events
		(id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES (?, 'ai', ?, ?, ?, ?, '', ?, ?)`, id, eventType, timestampText, string(encoded), project, summary, timestampText)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect event insert: %w", err)
	}
	if rowsAffected == 0 {
		var storedType, storedTimestamp, storedPayload, storedProject, storedActor, storedSummary string
		err := db.QueryRow(`SELECT type, timestamp, payload_json, COALESCE(project, ''), COALESCE(actor, ''), COALESCE(summary, '') FROM events WHERE id = ?`, id).
			Scan(&storedType, &storedTimestamp, &storedPayload, &storedProject, &storedActor, &storedSummary)
		if err != nil {
			return fmt.Errorf("inspect idempotent event: %w", err)
		}
		if storedType != eventType || storedTimestamp != timestampText || storedPayload != string(encoded) || storedProject != project || storedActor != "" || storedSummary != summary {
			return fmt.Errorf("event id %q already contains different data", id)
		}
	}
	return nil
}

func aiChildEnvironment(current []string, sessionID string, homeDir string, databasePath string, additional map[string]string) []string {
	blocked := map[string]bool{
		"WORKGRAPH_AI_SESSION_ID": true,
		"WORKGRAPH_AI_HOME":       true,
		"WORKGRAPH_AI_DATABASE":   true,
	}
	for name := range additional {
		blocked[name] = true
	}
	environment := make([]string, 0, len(current)+3+len(additional))
	for _, entry := range current {
		name, _, _ := strings.Cut(entry, "=")
		if !blocked[name] {
			environment = append(environment, entry)
		}
	}
	environment = append(environment,
		"WORKGRAPH_AI_SESSION_ID="+sessionID,
		"WORKGRAPH_AI_HOME="+homeDir,
		"WORKGRAPH_AI_DATABASE="+databasePath,
	)
	names := make([]string, 0, len(additional))
	for name := range additional {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		environment = append(environment, name+"="+additional[name])
	}
	return environment
}

func observeAIWorkingState(cwd string, observedAt time.Time) (AIObservedState, string, error) {
	cwd, err := canonicalAIPath(cwd)
	if err != nil {
		return AIObservedState{}, "", err
	}
	observed := AIObservedState{
		ObservedAt: observedAt.UTC().Format(time.RFC3339Nano),
		CWD:        cwd,
		DirtyPaths: []string{},
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return AIObservedState{}, "", fmt.Errorf("resolve git: %w", err)
	}
	worktreeOutput, err := runAIGit(gitPath, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		if isAINotRepositoryError(err) {
			return observed, "", nil
		}
		return AIObservedState{}, "", fmt.Errorf("resolve git worktree: %w", err)
	}
	worktreeRoot, err := canonicalAIPath(strings.TrimSpace(worktreeOutput))
	if err != nil {
		return AIObservedState{}, "", fmt.Errorf("canonicalize git worktree: %w", err)
	}
	commonOutput, err := runAIGit(gitPath, cwd, "rev-parse", "--git-common-dir")
	if err != nil {
		return AIObservedState{}, "", fmt.Errorf("resolve git common directory: %w", err)
	}
	commonPath := strings.TrimSpace(commonOutput)
	if !filepath.IsAbs(commonPath) {
		commonPath = filepath.Join(cwd, commonPath)
	}
	commonDir, err := canonicalAIPath(commonPath)
	if err != nil {
		return AIObservedState{}, "", fmt.Errorf("canonicalize git common directory: %w", err)
	}
	head, err := runAIGit(gitPath, cwd, "rev-parse", "HEAD")
	if err != nil {
		return AIObservedState{}, "", fmt.Errorf("resolve git HEAD: %w", err)
	}
	branch, branchErr := runAIGit(gitPath, cwd, "symbolic-ref", "--short", "-q", "HEAD")
	if branchErr != nil && !isAIExitCode(branchErr, 1) {
		return AIObservedState{}, "", fmt.Errorf("resolve git branch: %w", branchErr)
	}
	dirtyOutput, err := runAIGit(gitPath, worktreeRoot, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return AIObservedState{}, "", fmt.Errorf("collect git dirty paths: %w", err)
	}
	dirtyPaths, err := parseAIDirtyPaths([]byte(dirtyOutput))
	if err != nil {
		return AIObservedState{}, "", err
	}
	observed.WorktreeRoot = worktreeRoot
	observed.GitCommonDir = commonDir
	observed.Branch = strings.TrimSpace(branch)
	observed.Head = strings.TrimSpace(head)
	observed.DirtyPathCount = len(dirtyPaths)
	if len(dirtyPaths) > aiDirtyPathLimit {
		observed.DirtyPaths = append([]string{}, dirtyPaths[:aiDirtyPathLimit]...)
		observed.DirtyPathsTruncated = true
	} else {
		observed.DirtyPaths = dirtyPaths
	}
	return observed, aiProjectFromCommonDir(commonDir), nil
}

func observeAINonGitWorkingState(cwd string, observedAt time.Time) (AIObservedState, error) {
	cwd, err := canonicalAIPath(cwd)
	if err != nil {
		return AIObservedState{}, err
	}
	return AIObservedState{
		ObservedAt: observedAt.UTC().Format(time.RFC3339Nano),
		CWD:        cwd,
		DirtyPaths: []string{},
	}, nil
}

type aiGitError struct {
	code   int
	stderr string
}

func (err aiGitError) Error() string {
	return strings.TrimSpace(err.stderr)
}

func runAIGit(gitPath string, cwd string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", cwd}, args...)
	command := exec.Command(gitPath, commandArgs...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	output, err := command.Output()
	if err == nil {
		return string(output), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return "", aiGitError{code: exitErr.ExitCode(), stderr: string(exitErr.Stderr)}
	}
	return "", err
}

func isAINotRepositoryError(err error) bool {
	var gitErr aiGitError
	return errors.As(err, &gitErr) && gitErr.code == 128 && strings.Contains(gitErr.stderr, "not a git repository")
}

func isAIExitCode(err error, code int) bool {
	var gitErr aiGitError
	return errors.As(err, &gitErr) && gitErr.code == code
}

func parseAIDirtyPaths(output []byte) ([]string, error) {
	paths := make(map[string]struct{})
	for offset := 0; offset < len(output); {
		end := offset
		for end < len(output) && output[end] != 0 {
			end++
		}
		if end == len(output) {
			return nil, errors.New("parse git dirty paths: missing NUL terminator")
		}
		entry := output[offset:end]
		offset = end + 1
		if len(entry) < 4 || entry[2] != ' ' {
			return nil, errors.New("parse git dirty paths: malformed status entry")
		}
		path := strings.ToValidUTF8(string(entry[3:]), "�")
		paths[filepath.ToSlash(path)] = struct{}{}
		if entry[0] == 'R' || entry[1] == 'R' || entry[0] == 'C' || entry[1] == 'C' {
			end = offset
			for end < len(output) && output[end] != 0 {
				end++
			}
			if end == len(output) {
				return nil, errors.New("parse git dirty paths: missing rename source")
			}
			renamePath := strings.ToValidUTF8(string(output[offset:end]), "�")
			paths[filepath.ToSlash(renamePath)] = struct{}{}
			offset = end + 1
		}
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result, nil
}

func canonicalAIPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(absolute))
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func aiProjectFromCommonDir(commonDir string) string {
	base := filepath.Base(commonDir)
	if base == ".git" {
		return filepath.Base(filepath.Dir(commonDir))
	}
	return strings.TrimSuffix(base, ".git")
}

func newAIUUID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func currentAIBootIdentity() string {
	var value string
	switch runtime.GOOS {
	case "linux":
		contents, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
		if err != nil {
			return ""
		}
		value = strings.TrimSpace(string(contents))
	case "darwin":
		output, err := exec.Command("sysctl", "-n", "kern.boottime").Output()
		if err != nil {
			return ""
		}
		value = strings.TrimSpace(string(output))
	default:
		return ""
	}
	if value == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func currentAIProcessStartIdentity(pid int) string {
	switch runtime.GOOS {
	case "linux":
		contents, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		if err != nil {
			return ""
		}
		closingName := strings.LastIndexByte(string(contents), ')')
		if closingName < 0 || closingName+1 >= len(contents) {
			return ""
		}
		fields := strings.Fields(string(contents[closingName+1:]))
		if len(fields) < 20 {
			return ""
		}
		return fields[19]
	case "darwin":
		output, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(output))
	default:
		return ""
	}
}

func aiChildOutcome(state *os.ProcessState, waitErr error) (aiOutcome, int, error) {
	return platformAIChildOutcome(state, waitErr)
}

func aiEndedSummary(outcome aiOutcome) string {
	switch outcome.Kind {
	case "exited":
		if outcome.ExitCode != nil {
			return fmt.Sprintf("AI session ended (exit %d)", *outcome.ExitCode)
		}
	case "signaled":
		return "AI session ended (signal " + outcome.Signal + ")"
	}
	return "AI session ended"
}
