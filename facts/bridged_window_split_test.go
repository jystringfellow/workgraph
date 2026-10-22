package facts

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestWindowTooLargeFailureNarrowsBoundedRequestDownToOneHour(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	home := associateHome{HomeDir: homeDir}
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	emitted, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: "slack"})
	if err != nil {
		t.Fatalf("emit slack request: %v", err)
	}
	since, until := parseRequestWindow(t, emitted.Request)
	if until.Sub(since) != 24*time.Hour {
		t.Fatalf("expected a 24h first window, got %s to %s", emitted.Request.Since, emitted.Request.Until)
	}

	claimSlack := func(step string) workgraph.ClaimedCaptureRequest {
		t.Helper()
		claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{HomeDir: homeDir, ConnectorID: "slack", Worker: "facts", Max: 1})
		if err != nil || len(claims) != 1 {
			t.Fatalf("%s: expected the narrowed request to be claimable immediately, claims=%d error=%v", step, len(claims), err)
		}
		if claims[0].Request.ID != emitted.Request.ID {
			t.Fatalf("%s: expected the same request to be narrowed in place, got %s", step, claims[0].Request.ID)
		}
		return claims[0]
	}

	claim := claimSlack("first claim")
	_, failure := callScheduleMCP(t, home, "capture_request_fail", map[string]any{
		"request_id": claim.Request.ID, "claim_token": claim.ClaimToken, "reason": "bogus", "error": "unknown reason",
	})
	if !strings.Contains(failure, "window_too_large") {
		t.Fatalf("expected an unknown failure reason to be rejected naming window_too_large, got %q", failure)
	}

	expectedWindows := []time.Duration{12 * time.Hour, 6 * time.Hour, 3 * time.Hour, 90 * time.Minute, time.Hour}
	for index, expected := range expectedWindows {
		if index > 0 {
			claim = claimSlack("narrowed claim")
		}
		_, failure := callScheduleMCP(t, home, "capture_request_fail", map[string]any{
			"request_id": claim.Request.ID, "claim_token": claim.ClaimToken,
			"reason": "window_too_large", "error": "cannot paginate the window within one lease",
		})
		if failure != "" {
			t.Fatalf("split %d: report window too large: %s", index, failure)
		}
		requests, err := workgraph.ListCaptureRequests(workgraph.CaptureRequestListConfig{HomeDir: homeDir, ConnectorID: "slack"})
		if err != nil || len(requests) != 1 {
			t.Fatalf("split %d: expected one slack request, got %d error=%v", index, len(requests), err)
		}
		request := requests[0]
		narrowedSince, narrowedUntil := parseRequestWindow(t, request)
		if request.Status != "pending" || !narrowedSince.Equal(since) || narrowedUntil.Sub(narrowedSince) != expected {
			t.Fatalf("split %d: expected pending %s window from %s, got %s %s to %s", index, expected, emitted.Request.Since, request.Status, request.Since, request.Until)
		}
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	var lastError string
	if err := db.QueryRow(`SELECT last_error FROM capture_requests WHERE id = ?`, emitted.Request.ID).Scan(&lastError); err != nil {
		t.Fatalf("read narrowed request error: %v", err)
	}
	if !strings.Contains(lastError, "window too large") || !strings.Contains(lastError, "cannot paginate the window within one lease") {
		t.Fatalf("expected narrowed request to record why it was narrowed, got %q", lastError)
	}

	claim = claimSlack("one-hour claim")
	failedAt := time.Now().UTC()
	if _, failure := callScheduleMCP(t, home, "capture_request_fail", map[string]any{
		"request_id": claim.Request.ID, "claim_token": claim.ClaimToken,
		"reason": "window_too_large", "error": "still too large",
	}); failure != "" {
		t.Fatalf("report one-hour window too large: %s", failure)
	}
	requests, err := workgraph.ListCaptureRequests(workgraph.CaptureRequestListConfig{HomeDir: homeDir, ConnectorID: "slack"})
	if err != nil || len(requests) != 1 {
		t.Fatalf("list minimum-window request: %d error=%v", len(requests), err)
	}
	minimumSince, minimumUntil := parseRequestWindow(t, requests[0])
	if !minimumSince.Equal(since) || minimumUntil.Sub(minimumSince) != time.Hour {
		t.Fatalf("expected the one-hour window to stay one hour, got %s to %s", requests[0].Since, requests[0].Until)
	}
	if requests[0].AvailableAt <= failedAt.Format(time.RFC3339Nano) {
		t.Fatalf("expected the minimum window to fail with normal backoff, got available_at %s", requests[0].AvailableAt)
	}

	if _, err := db.Exec(`UPDATE capture_requests SET available_at = ? WHERE id = ?`, failedAt.Format(time.RFC3339Nano), emitted.Request.ID); err != nil {
		t.Fatalf("skip backoff: %v", err)
	}
	claim = claimSlack("complete claim")
	if _, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{
		HomeDir: homeDir, RequestID: claim.Request.ID, ClaimToken: claim.ClaimToken,
		Input: strings.NewReader(`{"events":[],"empty_proof":{"exhaustive_query":true}}`),
	}); err != nil {
		t.Fatalf("complete narrowed request: %v", err)
	}
	next, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: "slack"})
	if err != nil || next.Coalesced {
		t.Fatalf("emit remainder request: coalesced=%v error=%v", next.Coalesced, err)
	}
	nextSince, nextUntil := parseRequestWindow(t, next.Request)
	if !nextSince.Equal(minimumUntil.Add(-5*time.Minute)) || !nextUntil.After(minimumUntil) || nextUntil.Sub(nextSince) > 24*time.Hour {
		t.Fatalf("expected the remainder to resume from the narrowed bound minus overlap, got %s to %s", next.Request.Since, next.Request.Until)
	}
}

func TestWindowTooLargeFromCLIAndSnapshotRequests(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack.lists")
	slack, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: "slack"})
	if err != nil {
		t.Fatalf("emit slack request: %v", err)
	}
	claimFile := filepath.Join(t.TempDir(), "claim.json")
	if output, err := runworkgraph(t, repoRoot(t), "capture", "requests", "--home", homeDir, "--claim", "--connector", "slack", "--max", "1", "--worker", "facts", "--claim-file", claimFile); err != nil {
		t.Fatalf("claim slack from CLI: %v\n%s", err, output)
	}
	if output, err := runworkgraphInput(t, repoRoot(t), `{"error":"too many pages","reason":"window_too_large"}`,
		"capture", "requests", "--home", homeDir, "--fail", slack.Request.ID, "--claim-file", claimFile, "--error-json", "-"); err != nil {
		t.Fatalf("report window too large from CLI: %v\n%s", err, output)
	}
	requests, err := workgraph.ListCaptureRequests(workgraph.CaptureRequestListConfig{HomeDir: homeDir, ConnectorID: "slack"})
	if err != nil || len(requests) != 1 {
		t.Fatalf("list slack request: %d error=%v", len(requests), err)
	}
	if since, until := parseRequestWindow(t, requests[0]); until.Sub(since) != 12*time.Hour {
		t.Fatalf("expected CLI report to halve the window, got %s to %s", requests[0].Since, requests[0].Until)
	}

	snapshot, err := workgraph.EmitBridgedCaptureRequest(workgraph.CaptureRequestEmitConfig{HomeDir: homeDir, ConnectorID: "slack.lists"})
	if err != nil {
		t.Fatalf("emit snapshot request: %v", err)
	}
	claims, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{HomeDir: homeDir, ConnectorID: "slack.lists", Worker: "facts", Max: 1})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim snapshot request: %d error=%v", len(claims), err)
	}
	failedAt := time.Now().UTC()
	if _, failure := callScheduleMCP(t, associateHome{HomeDir: homeDir}, "capture_request_fail", map[string]any{
		"request_id": snapshot.Request.ID, "claim_token": claims[0].ClaimToken,
		"reason": "window_too_large", "error": "list too large",
	}); failure != "" {
		t.Fatalf("report snapshot too large: %s", failure)
	}
	requests, err = workgraph.ListCaptureRequests(workgraph.CaptureRequestListConfig{HomeDir: homeDir, ConnectorID: "slack.lists"})
	if err != nil || len(requests) != 1 {
		t.Fatalf("list snapshot request: %d error=%v", len(requests), err)
	}
	if requests[0].Since != snapshot.Request.Since || requests[0].Until != snapshot.Request.Until || requests[0].AvailableAt <= failedAt.Format(time.RFC3339Nano) {
		t.Fatalf("expected a complete snapshot to keep its bounds and back off, got %#v", requests[0])
	}
}
