package workgraph

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"
)

func TestConnectorPollerWakeRespectsSpacingAndCoalescesDuringPoll(t *testing.T) {
	homeDir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		capture := testConnectorPollCapture(homeDir)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		wake := make(chan struct{}, 1)
		starts := make(chan time.Time, 8)
		release := make(chan struct{})
		attempt := 0
		go capture.runConnectorPoller(ctx, connectorPoller{
			id: "notion", interval: 5 * time.Minute, wake: wake,
			poll: func(ctx context.Context) error {
				attempt++
				starts <- time.Now()
				if attempt == 2 {
					select {
					case <-release:
					case <-ctx.Done():
					}
				}
				return nil
			},
		})
		first := <-starts
		synctest.Wait()
		time.Sleep(2 * time.Second)
		wake <- struct{}{}
		synctest.Wait()
		assertNoConnectorPoll(t, starts)
		second := <-starts
		if elapsed := second.Sub(first); elapsed != 10*time.Second {
			t.Fatalf("wake spacing: got %s", elapsed)
		}
		wake <- struct{}{}
		select {
		case wake <- struct{}{}:
		default:
		}
		close(release)
		third := <-starts
		if elapsed := third.Sub(second); elapsed != 10*time.Second {
			t.Fatalf("follow-up spacing: got %s", elapsed)
		}
		synctest.Wait()
		time.Sleep(time.Minute)
		assertNoConnectorPoll(t, starts)
	})
}

func TestConnectorPollerRetryAfterSurvivesRestartAndWake(t *testing.T) {
	homeDir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		capture := testConnectorPollCapture(homeDir)
		ctx, cancel := context.WithCancel(context.Background())
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			capture.runConnectorPoller(ctx, connectorPoller{
				id: "notion", interval: time.Hour,
				poll: func(context.Context) error {
					return fmt.Errorf("provider: %w", &ConnectorPollRetryAfter{
						Err: errors.New("rate limited"), RetryAfter: 2 * time.Minute,
					})
				},
			})
		}()
		synctest.Wait()
		entry, err := readConnectorPollState(homeDir, "notion")
		if err != nil {
			t.Fatal(err)
		}
		wantDeadline := time.Now().Add(2 * time.Minute)
		deadline, err := ConnectorPollNotBefore(homeDir, "notion")
		if err != nil || !deadline.Equal(wantDeadline) {
			t.Fatalf("retry-after deadline: %s, want %s (%v)", deadline, wantDeadline, err)
		}
		if entry.ConsecutiveFailures != 1 || entry.LastError != "provider: rate limited" || entry.LastSuccess != "" {
			t.Fatalf("retry-after must record a failure: %+v", entry)
		}
		if err := pollConnectorOnce(homeDir, "", "notion"); err == nil || err.Error() != "rate limited until "+deadline.Format(time.RFC3339Nano) {
			t.Fatalf("manual polling must respect deadline before opening a provider: %v", err)
		}
		cancel()
		<-finished
		restartCtx, stop := context.WithCancel(context.Background())
		defer stop()
		wake := make(chan struct{}, 1)
		starts := make(chan time.Time, 8)
		restarted := testConnectorPollCapture(homeDir)
		go restarted.runConnectorPoller(restartCtx, connectorPoller{
			id: "notion", interval: time.Hour, wake: wake,
			poll: func(context.Context) error {
				starts <- time.Now()
				return nil
			},
		})
		synctest.Wait()
		wake <- struct{}{}
		time.Sleep(time.Minute)
		synctest.Wait()
		assertNoConnectorPoll(t, starts)
		if started := <-starts; !started.Equal(deadline) {
			t.Fatalf("restart must poll at deadline: %s, want %s", started, deadline)
		}
		synctest.Wait()
		entry, err = readConnectorPollState(homeDir, "notion")
		if err != nil || entry.NotBefore != "" || entry.ConsecutiveFailures != 0 || entry.LastError != "" {
			t.Fatalf("successful retry must clear failure state and elapsed deadline: %+v (%v)", entry, err)
		}
	})
}

func TestConnectorPollerDeferralPreservesFailuresAndDeadlineDespiteWake(t *testing.T) {
	homeDir := t.TempDir()
	if err := recordConnectorPollAttempt(homeDir, "notion", time.Now(), time.Time{}, 2, errors.New("temporary failure")); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		capture := testConnectorPollCapture(homeDir)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		wake := make(chan struct{}, 1)
		starts := make(chan time.Time, 8)
		deadline := time.Now().Add(time.Minute)
		attempt := 0
		go capture.runConnectorPoller(ctx, connectorPoller{
			id: "notion", interval: time.Hour, wake: wake,
			poll: func(context.Context) error {
				starts <- time.Now()
				attempt++
				if attempt == 1 {
					return fmt.Errorf("provider: %w", &ConnectorPollDeferred{
						Reason: "budget low (not unauthorized)", NotBefore: deadline,
					})
				}
				return errors.New("still unavailable")
			},
		})
		<-starts
		synctest.Wait()
		entry, err := readConnectorPollState(homeDir, "notion")
		if err != nil || entry.ConsecutiveFailures != 2 || entry.LastError != "temporary failure" || entry.SetupState == "error" {
			t.Fatalf("deferral changed failure state: %+v (%v)", entry, err)
		}
		wake <- struct{}{}
		time.Sleep(30 * time.Second)
		synctest.Wait()
		assertNoConnectorPoll(t, starts)
		if started := <-starts; !started.Equal(deadline) {
			t.Fatalf("wake bypassed deferral: %s, want %s", started, deadline)
		}
		synctest.Wait()
		entry, err = readConnectorPollState(homeDir, "notion")
		if err != nil || entry.ConsecutiveFailures != 3 || entry.DeferredReason != "" {
			t.Fatalf("failure after deferral must extend consecutive failures: %+v (%v)", entry, err)
		}
	})
}

func TestConnectorPollerOrdinaryScheduleAndBackoff(t *testing.T) {
	homeDir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		capture := testConnectorPollCapture(homeDir)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		starts := make(chan time.Time, 8)
		attempt := 0
		go capture.runConnectorPoller(ctx, connectorPoller{
			id: "notion", interval: 3 * time.Second,
			poll: func(context.Context) error {
				starts <- time.Now()
				attempt++
				if attempt == 2 || attempt == 3 {
					return errors.New("temporary failure")
				}
				return nil
			},
		})
		previous := <-starts
		for _, want := range []time.Duration{3 * time.Second, time.Second, 2 * time.Second, 3 * time.Second} {
			started := <-starts
			if elapsed := started.Sub(previous); elapsed != want {
				t.Fatalf("ordinary poll delay: got %s, want %s", elapsed, want)
			}
			previous = started
		}
	})
}

func testConnectorPollCapture(homeDir string) *RunCapture {
	return &RunCapture{
		homeDir: homeDir, connectorPollTimeout: 30 * time.Second,
		connectorRetryInitial: time.Second, connectorRetryMax: time.Minute,
	}
}

func assertNoConnectorPoll(t *testing.T, starts <-chan time.Time) {
	t.Helper()
	select {
	case started := <-starts:
		t.Fatalf("unexpected poll at %s", started)
	default:
	}
}
