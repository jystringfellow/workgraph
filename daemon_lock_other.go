//go:build !darwin && !linux

package workgraph

func lockCaptureHome(home string) (func(), error) {
	return func() {}, nil
}
