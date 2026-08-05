//go:build !unix

package workgraph

import (
	"errors"
	"fmt"
	"os"
)

func inspectLocalAIProcess(int) (AIProcessInspection, error) {
	return AIProcessInspection{}, errors.New("process inspection is unsupported")
}

func platformAIChildOutcome(state *os.ProcessState, waitErr error) (aiOutcome, int, error) {
	if state == nil {
		return aiOutcome{}, 1, fmt.Errorf("wait for AI child: %w", waitErr)
	}
	exitCode := state.ExitCode()
	if exitCode >= 0 {
		return aiOutcome{Kind: "exited", ExitCode: &exitCode}, exitCode, nil
	}
	return aiOutcome{Kind: "unknown"}, 1, nil
}
