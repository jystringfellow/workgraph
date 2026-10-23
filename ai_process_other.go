//go:build !unix

package workgraph

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
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

func openAIControllingTerminal() (*os.File, error) {
	return os.OpenFile("CONIN$", os.O_RDWR, 0)
}

func isolateProcessGroup(*exec.Cmd) {}

func relayAIRunSignals(*os.Process) func() {
	received := make(chan os.Signal, 1)
	signal.Notify(received, os.Interrupt)
	return func() { signal.Stop(received) }
}
