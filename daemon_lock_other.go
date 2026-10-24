//go:build !darwin && !linux

package workgraph

func lockCaptureHome(home string) (func(), error) {
	return func() {}, nil
}

func lockDaemonReady(home string) (func(), error) {
	return func() {}, nil
}

func daemonReadyLockHeld(home string) bool {
	return true
}
