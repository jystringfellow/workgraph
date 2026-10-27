package facts

import (
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
