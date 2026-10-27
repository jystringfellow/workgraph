package facts

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

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
			select {
			case runErr = <-done:
			case <-time.After(factWaitTimeout):
				runErr = fmt.Errorf("capture run did not stop within %s of cancellation", factWaitTimeout)
				t.Error(runErr)
			}
		})
		return runErr
	}
	t.Cleanup(func() { _ = stop() })
	return stop
}
