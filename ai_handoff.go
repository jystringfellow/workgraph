package workgraph

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	aiHandoffContextLimit     = 30 * 1024
	aiWindowsCommandLineLimit = 32767
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
		if source.newestCheckpointID != result.CheckpointEventID {
			return AIHandoffResult{}, errors.New("source session has a checkpoint dated after the one --stdin just recorded, so the recorded checkpoint cannot be handed off; check the system clock and inspect the source with `workgraph ai show`")
		}
	} else {
		result.ReusedCheckpoint = true
		result.CheckpointEventID = source.latestCheckpointID
	}
	if source.newestCheckpointID != "" && source.newestCheckpointID != source.latestCheckpointID {
		return AIHandoffResult{}, errors.New("source session's latest checkpoint is unsupported or invalid and cannot be handed off safely")
	}
	if source.newestObservationID != "" && source.newestObservationID != source.observedEventID {
		return AIHandoffResult{}, errors.New("source session's latest repository observation is unsupported or invalid and cannot be handed off safely")
	}
	if source.LatestCheckpoint == nil {
		return AIHandoffResult{}, errors.New("source session has no structured checkpoint; record one first or use --stdin while it is active")
	}

	contextBudget, err := aiHandoffContextBudget(config.HomeDir, config.DatabasePath, destination, destinationPath)
	if err != nil {
		return AIHandoffResult{}, err
	}
	continuation, err := buildAIHandoffContext(sourceSessionID, source.LatestCheckpoint, source.LatestObserved, contextBudget)
	if err != nil {
		return AIHandoffResult{}, err
	}
	destinationStdin := config.Stdin
	if config.CheckpointInput != nil {
		terminal, err := openAIControllingTerminal()
		if err != nil {
			return AIHandoffResult{}, aiHandoffCheckpointRetryError(config, destination, sourceSessionID,
				fmt.Errorf("--stdin consumed standard input and no controlling terminal is available for the destination: %w", err))
		}
		defer terminal.Close()
		destinationStdin = terminal
	}
	arguments := aiHandoffArguments(destination, continuation)
	launch, err := runAISession(AIRunConfig{
		HomeDir: config.HomeDir, DatabasePath: config.DatabasePath,
		WorkingDir:           source.LatestObserved.CWD,
		Command:              append([]string{destinationPath}, arguments...),
		PredecessorSessionID: sourceSessionID,
		Stdin:                destinationStdin, Stdout: config.Stdout, Stderr: config.Stderr,
	}, environmentSessionID)
	if err != nil {
		launchErr := fmt.Errorf("launch destination: %w", err)
		if config.CheckpointInput != nil {
			launchErr = aiHandoffCheckpointRetryError(config, destination, sourceSessionID, launchErr)
		}
		return AIHandoffResult{}, launchErr
	}
	result.DestinationSessionID = launch.SessionID
	result.ExitCode = launch.ExitCode
	return result, nil
}

func aiHandoffCheckpointRetryError(config AIHandoffConfig, destination, sourceSessionID string, err error) error {
	retry := []string{"workgraph", "ai", "handoff", destination, "--from", sourceSessionID}
	if config.HomeDir != "" {
		retry = append(retry, "--home", config.HomeDir)
	}
	if config.DatabasePath != "" {
		retry = append(retry, "--database", config.DatabasePath)
	}
	return fmt.Errorf("%w; the checkpoint was saved on the source; retry without --stdin from a terminal: `%s`", err, aiShellCommand(retry))
}

func findAIHandoffSource(homeDir, databasePath, sessionID string) (*aiSessionProjection, error) {
	projections, _, err := loadAISessionProjections(homeDir, databasePath, nil)
	if err != nil {
		return nil, err
	}
	for index := range projections {
		if projections[index].Summary.SessionID == sessionID {
			if projections[index].unorderableEvidence {
				return nil, errors.New("source session has unorderable evidence with an invalid timestamp; inspect and repair the source events before handoff")
			}
			return &projections[index], nil
		}
	}
	return nil, fmt.Errorf("AI session %q was not found", sessionID)
}

func aiHandoffContextBudget(homeDir, databasePath, destination, destinationPath string) (int, error) {
	status, err := prepareRunStatus(RunConfig{HomeDir: homeDir, DatabasePath: databasePath})
	if err != nil {
		return 0, err
	}
	invokedPath, err := filepath.Abs(destinationPath)
	if err != nil {
		return 0, fmt.Errorf("resolve destination AI tool path: %w", err)
	}
	prepared, err := aiPrepareNativeLaunch(destination, aiHandoffArguments(destination, ""), status.HomeDir)
	if err != nil {
		return 0, fmt.Errorf("prepare destination native binding: %w", err)
	}
	overhead := aiWindowsCommandLineCost(append([]string{invokedPath}, prepared.Args...)) - aiWindowsArgumentCost("")
	return min(aiHandoffContextLimit, aiWindowsCommandLineLimit-overhead), nil
}

func buildAIHandoffContext(sessionID string, checkpoint *aiCheckpointPayload, observed *AIObservedState, budget int) (string, error) {
	normalizedCheckpoint, err := validateAIStoredCheckpoint(checkpoint.AgentStated)
	if err != nil {
		return "", fmt.Errorf("stored checkpoint cannot be handed off safely: %w", err)
	}
	for _, field := range []struct{ name, value string }{
		{"branch", observed.Branch}, {"cwd", observed.CWD}, {"worktree_root", observed.WorktreeRoot},
	} {
		if err := validateAICredentialPatterns("repository field "+field.name, field.value); err != nil {
			return "", err
		}
	}
	if err := validateAICredentialPatterns("repository field dirty_paths", observed.DirtyPaths...); err != nil {
		return "", err
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
	encoded, err := encodeAIHandoffContext(context)
	if err != nil {
		return "", err
	}
	if aiWindowsArgumentCost(encoded) > budget && len(observed.DirtyPaths) > 0 {
		context.Repository.DirtyPathsTruncated = true
		kept := sort.Search(len(observed.DirtyPaths)-1, func(count int) bool {
			context.Repository.DirtyPaths = observed.DirtyPaths[:count+1]
			candidate, err := encodeAIHandoffContext(context)
			return err != nil || aiWindowsArgumentCost(candidate) > budget
		})
		context.Repository.DirtyPaths = append([]string{}, observed.DirtyPaths[:kept]...)
		if encoded, err = encodeAIHandoffContext(context); err != nil {
			return "", err
		}
	}
	if cost := aiWindowsArgumentCost(encoded); cost > budget {
		return "", fmt.Errorf("handoff context needs %d bytes, over the %d bytes left on the destination command line even without dirty paths; record a shorter checkpoint while the source is active, for example with --stdin", cost, budget)
	}
	return encoded, nil
}

func encodeAIHandoffContext(context aiHandoffContext) (string, error) {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(context); err != nil {
		return "", fmt.Errorf("encode handoff context: %w", err)
	}
	return strings.TrimSuffix(encoded.String(), "\n"), nil
}

func aiWindowsArgumentCost(argument string) int {
	return len(argument) + strings.Count(argument, `"`) + strings.Count(argument, `\`) + 2
}

func aiWindowsCommandLineCost(arguments []string) int {
	cost := len(arguments)
	for _, argument := range arguments {
		cost += aiWindowsArgumentCost(argument)
	}
	return cost
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
