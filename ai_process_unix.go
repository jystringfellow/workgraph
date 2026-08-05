//go:build unix

package workgraph

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
)

func inspectLocalAIProcess(pid int) (AIProcessInspection, error) {
	process, err := os.FindProcess(pid)
	if err != nil {
		return AIProcessInspection{}, err
	}
	err = process.Signal(syscall.Signal(0))
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return AIProcessInspection{Exists: false}, nil
	}
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return AIProcessInspection{}, err
	}
	return AIProcessInspection{Exists: true, StartIdentity: currentAIProcessStartIdentity(pid)}, nil
}

func platformAIChildOutcome(state *os.ProcessState, waitErr error) (aiOutcome, int, error) {
	if state == nil {
		return aiOutcome{}, 1, fmt.Errorf("wait for AI child: %w", waitErr)
	}
	if state.Success() {
		zero := 0
		return aiOutcome{Kind: "exited", ExitCode: &zero}, 0, nil
	}
	if waitStatus, ok := state.Sys().(syscall.WaitStatus); ok && waitStatus.Signaled() {
		signal := waitStatus.Signal()
		return aiOutcome{Kind: "signaled", Signal: aiSignalName(signal)}, 128 + int(signal), nil
	}
	exitCode := state.ExitCode()
	if exitCode >= 0 {
		return aiOutcome{Kind: "exited", ExitCode: &exitCode}, exitCode, nil
	}
	return aiOutcome{Kind: "unknown"}, 1, nil
}

func aiSignalName(signal syscall.Signal) string {
	switch signal {
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGTERM:
		return "SIGTERM"
	default:
		return "SIG" + strconv.Itoa(int(signal))
	}
}
