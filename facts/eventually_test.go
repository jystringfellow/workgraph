package facts

import (
	"context"
	"sync"
	"testing"
	"time"
)

const factWaitTimeout = 30 * time.Second

func eventually(t *testing.T, condition func() bool, format string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(factWaitTimeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf(format, args...)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type deadlineArmedLater struct {
	done     chan struct{}
	mu       sync.Mutex
	deadline time.Time
	err      error
}

func newDeadlineArmedLater() *deadlineArmedLater {
	return &deadlineArmedLater{done: make(chan struct{})}
}

func (ctx *deadlineArmedLater) arm(after time.Duration) {
	ctx.mu.Lock()
	ctx.deadline = time.Now().Add(after)
	ctx.mu.Unlock()
	time.AfterFunc(after, func() {
		ctx.mu.Lock()
		ctx.err = context.DeadlineExceeded
		ctx.mu.Unlock()
		close(ctx.done)
	})
}

func (ctx *deadlineArmedLater) Deadline() (time.Time, bool) {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return ctx.deadline, !ctx.deadline.IsZero()
}

func (ctx *deadlineArmedLater) Done() <-chan struct{} {
	return ctx.done
}

func (ctx *deadlineArmedLater) Err() error {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return ctx.err
}

func (ctx *deadlineArmedLater) Value(any) any {
	return nil
}
