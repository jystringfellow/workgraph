//go:build darwin || linux

package workgraph

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func lockCaptureHome(home string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(home, "capture.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("capture home is already locked: %w", err)
	}
	return func() { file.Close() }, nil
}
