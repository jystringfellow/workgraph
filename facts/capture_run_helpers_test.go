package facts

import (
	"context"
	"sync"
	"testing"

	workgraph "github.com/jystringfellow/workgraph"
)

func runCaptureUntilStopped(t *testing.T, capture *workgraph.RunCapture) func() error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- capture.Run(ctx) }()
	var once sync.Once
	var runErr error
	stop := func() error {
		once.Do(func() {
			cancel()
			runErr = <-done
		})
		return runErr
	}
	t.Cleanup(func() { _ = stop() })
	return stop
}
