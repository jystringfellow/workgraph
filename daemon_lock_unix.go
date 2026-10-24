//go:build darwin || linux

package workgraph

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	captureLockAttempts = 5
	captureLockRetry    = 20 * time.Millisecond
)

func lockCaptureHome(home string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(home, "capture.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	var lockErr error
	for attempt := 0; attempt < captureLockAttempts; attempt++ {
		if lockErr = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); lockErr == nil {
			return func() { file.Close() }, nil
		}
		time.Sleep(captureLockRetry)
	}
	file.Close()
	return nil, fmt.Errorf("capture home is already locked: %w", lockErr)
}

func captureHomeLocked(home string) bool {
	return flockHeld(filepath.Join(home, "capture.lock"))
}

func flockHeld(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return false
}

func lockDaemonReady(home string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(home, "daemon.ready.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("daemon readiness is already locked: %w", err)
	}
	return func() { file.Close() }, nil
}

func daemonReadyLockHeld(home string) bool {
	return flockHeld(filepath.Join(home, "daemon.ready.lock"))
}
