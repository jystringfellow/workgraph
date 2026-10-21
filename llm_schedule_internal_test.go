package workgraph

import (
	"errors"
	"testing"
	"time"
)

func TestLLMAssociateCadenceRunsHourlyAndRepeatsTheDailyRun(t *testing.T) {
	start := time.Date(2026, 10, 21, 14, 0, 0, 0, time.Local)
	cadence := newLLMAssociateCadence(start)
	if !cadence.due(start) || cadence.dailyRemaining != 0 {
		t.Fatalf("expected an hourly run at start without a daily run, got %+v", cadence)
	}
	cadence.record(start, LLMAssociateScheduleResult{Eligible: true, Created: true}, nil)
	if cadence.due(start.Add(59 * time.Minute)) {
		t.Fatalf("expected no run within the hour")
	}
	if !cadence.due(start.Add(time.Hour)) {
		t.Fatalf("expected the next hourly run")
	}

	daily := time.Date(2026, 10, 22, 5, 0, 0, 0, time.Local)
	for created := 0; created < llmAssociateDailyRequests; created++ {
		if !cadence.due(daily) {
			t.Fatalf("expected daily run %d to be due", created+1)
		}
		cadence.record(daily, LLMAssociateScheduleResult{Eligible: true}, nil)
		if !cadence.due(daily) {
			t.Fatalf("expected a coalesced daily check to keep the run open")
		}
		cadence.record(daily, LLMAssociateScheduleResult{Eligible: true, Created: true}, nil)
	}
	if cadence.due(daily.Add(time.Minute)) {
		t.Fatalf("expected the daily run to stop after %d requests", llmAssociateDailyRequests)
	}

	next := time.Date(2026, 10, 23, 5, 30, 0, 0, time.Local)
	if !cadence.due(next) {
		t.Fatalf("expected the next day's run")
	}
	cadence.record(next, LLMAssociateScheduleResult{}, nil)
	if cadence.dailyRemaining != 0 {
		t.Fatalf("expected the daily run to stop when nothing is eligible, got %d", cadence.dailyRemaining)
	}
	later := time.Date(2026, 10, 24, 6, 0, 0, 0, time.Local)
	cadence.due(later)
	cadence.record(later, LLMAssociateScheduleResult{}, errors.New("model unavailable"))
	if cadence.dailyRemaining != 0 {
		t.Fatalf("expected a failed check to end the daily run")
	}
}
