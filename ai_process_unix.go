//go:build unix

package workgraph

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
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

func openAIControllingTerminal() (*os.File, error) {
	return os.OpenFile("/dev/tty", os.O_RDWR, 0)
}

func isolateProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
}

func startAIRunSignalRelay() (attach func(*os.Process), stop func()) {
	received := make(chan os.Signal, 4)
	signal.Notify(received, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP)
	children := make(chan *os.Process, 1)
	done := make(chan struct{})
	go func() {
		var child *os.Process
		var pending []os.Signal
		for {
			select {
			case <-done:
				return
			case child = <-children:
				for _, value := range pending {
					_ = child.Signal(value)
				}
				pending = nil
			case value := <-received:
				// The terminal already delivers SIGINT and SIGQUIT to the child's process group.
				if value != syscall.SIGTERM && value != syscall.SIGHUP {
					continue
				}
				if child == nil {
					pending = append(pending, value)
				} else {
					_ = child.Signal(value)
				}
			}
		}
	}()
	attach = func(process *os.Process) { children <- process }
	stop = func() {
		signal.Stop(received)
		close(done)
	}
	return attach, stop
}
