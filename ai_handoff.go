package workgraph

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type AIHandoffConfig struct {
	HomeDir          string
	DatabasePath     string
	HomeExplicit     bool
	DatabaseExplicit bool
	SourceSessionID  string
	Destination      string
	CheckpointInput  io.Reader
	Stdin            io.Reader
	Stdout           io.Writer
	Stderr           io.Writer
}

type AIHandoffResult struct {
	SourceSessionID      string
	DestinationSessionID string
	CheckpointEventID    string
	ReusedCheckpoint     bool
	ExitCode             int
}

type aiHandoffContext struct {
	Instruction     string                     `json:"instruction"`
	SourceSessionID string                     `json:"source_session_id"`
	Checkpoint      map[string]any             `json:"checkpoint"`
	Repository      aiHandoffRepositoryContext `json:"repository"`
}

type aiHandoffRepositoryContext struct {
	ObservedAt          string   `json:"observed_at"`
	CWD                 string   `json:"cwd"`
	WorktreeRoot        string   `json:"worktree_root,omitempty"`
	Branch              string   `json:"branch,omitempty"`
	Head                string   `json:"head,omitempty"`
	DirtyPaths          []string `json:"dirty_paths"`
	DirtyPathCount      int      `json:"dirty_path_count"`
	DirtyPathsTruncated bool     `json:"dirty_paths_truncated"`
}

func HandoffAISession(config AIHandoffConfig) (AIHandoffResult, error) {
	if config.HomeExplicit {
		if injected := os.Getenv("WORKGRAPH_AI_HOME"); injected != "" && !sameAIPath(config.HomeDir, injected) {
			return AIHandoffResult{}, errors.New("explicit home disagrees with WORKGRAPH_AI_HOME")
		}
	}
	if config.DatabaseExplicit {
		if injected := os.Getenv("WORKGRAPH_AI_DATABASE"); injected != "" && !sameAIPath(config.DatabasePath, injected) {
			return AIHandoffResult{}, errors.New("explicit database disagrees with WORKGRAPH_AI_DATABASE")
		}
	}

	destination := strings.TrimSpace(config.Destination)
	if _, supported := aiNativeAdapters[destination]; !supported {
		return AIHandoffResult{}, fmt.Errorf("destination %q is unsupported; choose codex, claude, copilot, or opencode", destination)
	}
	destinationPath, err := exec.LookPath(destination)
	if err != nil {
		return AIHandoffResult{}, fmt.Errorf("resolve destination AI tool %q: %w", destination, err)
	}

	sourceSessionID := strings.TrimSpace(config.SourceSessionID)
	environmentSessionID := strings.TrimSpace(os.Getenv("WORKGRAPH_AI_SESSION_ID"))
	if sourceSessionID == "" {
		sourceSessionID = environmentSessionID
	} else if environmentSessionID != "" && sourceSessionID != environmentSessionID {
		return AIHandoffResult{}, errors.New("explicit source session disagrees with WORKGRAPH_AI_SESSION_ID")
	}
	if sourceSessionID == "" {
		return AIHandoffResult{}, errors.New("source session is required; run inside a wrapped session or use --from <session-id>")
	}

	source, err := findAIHandoffSource(config.HomeDir, config.DatabasePath, sourceSessionID)
	if err != nil {
		return AIHandoffResult{}, err
	}
	if _, supported := aiNativeAdapters[source.Summary.Tool]; !supported {
		return AIHandoffResult{}, fmt.Errorf("source AI tool %q is unsupported for handoff", source.Summary.Tool)
	}
	if source.LatestObserved == nil || source.LatestObserved.CWD == "" {
		return AIHandoffResult{}, errors.New("source session has no usable observed working directory")
	}

	result := AIHandoffResult{SourceSessionID: sourceSessionID}
	if config.CheckpointInput != nil {
		if source.EndedExists {
			return AIHandoffResult{}, errors.New("source AI session has ended; omit --stdin to reuse its latest checkpoint")
		}
		checkpoint, err := CheckpointAISession(AICheckpointConfig{
			HomeDir: config.HomeDir, DatabasePath: config.DatabasePath,
			HomeExplicit: config.HomeExplicit, DatabaseExplicit: config.DatabaseExplicit,
			SessionID: sourceSessionID, Input: config.CheckpointInput,
		})
		if err != nil {
			return AIHandoffResult{}, fmt.Errorf("capture source checkpoint: %w", err)
		}
		result.CheckpointEventID = checkpoint.EventID
		source, err = findAIHandoffSource(config.HomeDir, config.DatabasePath, sourceSessionID)
		if err != nil {
			return AIHandoffResult{}, err
		}
	} else {
		result.ReusedCheckpoint = true
	}
	if source.newestCheckpointID != "" && source.newestCheckpointID != source.latestCheckpointID {
		return AIHandoffResult{}, errors.New("source session's latest checkpoint is unsupported or invalid and cannot be handed off safely")
	}
	if source.LatestCheckpoint == nil {
		return AIHandoffResult{}, errors.New("source session has no structured checkpoint; record one first or use --stdin while it is active")
	}

	continuation, err := buildAIHandoffContext(sourceSessionID, source.LatestCheckpoint, source.LatestObserved)
	if err != nil {
		return AIHandoffResult{}, err
	}
	arguments := aiHandoffArguments(destination, continuation)
	launch, err := runAISession(AIRunConfig{
		HomeDir: config.HomeDir, DatabasePath: config.DatabasePath,
		WorkingDir:           source.LatestObserved.CWD,
		Command:              append([]string{destinationPath}, arguments...),
		PredecessorSessionID: sourceSessionID,
		Stdin:                config.Stdin, Stdout: config.Stdout, Stderr: config.Stderr,
	}, environmentSessionID)
	if err != nil {
		return AIHandoffResult{}, fmt.Errorf("launch destination: %w", err)
	}
	result.DestinationSessionID = launch.SessionID
	result.ExitCode = launch.ExitCode
	return result, nil
}

func findAIHandoffSource(homeDir, databasePath, sessionID string) (*aiSessionProjection, error) {
	projections, _, err := loadAISessionProjections(homeDir, databasePath, nil)
	if err != nil {
		return nil, err
	}
	for index := range projections {
		if projections[index].Summary.SessionID == sessionID {
			return &projections[index], nil
		}
	}
	return nil, fmt.Errorf("AI session %q was not found", sessionID)
}

func buildAIHandoffContext(sessionID string, checkpoint *aiCheckpointPayload, observed *AIObservedState) (string, error) {
	encodedCheckpoint, err := json.Marshal(checkpoint.AgentStated)
	if err != nil {
		return "", errors.New("stored checkpoint cannot be handed off safely")
	}
	normalizedCheckpoint, err := validateAICheckpointInput(bytes.NewReader(encodedCheckpoint))
	if err != nil {
		return "", fmt.Errorf("stored checkpoint cannot be handed off safely: %w", err)
	}
	context := aiHandoffContext{
		Instruction:     "Continue from the structured Workgraph handoff context. Verify repository state before changing files.",
		SourceSessionID: sessionID,
		Checkpoint:      normalizedCheckpoint,
		Repository: aiHandoffRepositoryContext{
			ObservedAt: observed.ObservedAt, CWD: observed.CWD,
			WorktreeRoot: observed.WorktreeRoot, Branch: observed.Branch, Head: observed.Head,
			DirtyPaths: append([]string{}, observed.DirtyPaths...), DirtyPathCount: observed.DirtyPathCount,
			DirtyPathsTruncated: observed.DirtyPathsTruncated,
		},
	}
	encoded, err := json.Marshal(context)
	if err != nil {
		return "", fmt.Errorf("encode handoff context: %w", err)
	}
	return string(encoded), nil
}

func aiHandoffArguments(destination, continuation string) []string {
	switch destination {
	case "copilot":
		return []string{"--interactive", continuation}
	case "opencode":
		return []string{"--prompt", continuation}
	default:
		return []string{continuation}
	}
}
