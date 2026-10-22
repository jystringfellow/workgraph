package workgraph

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	captureFailureWindowTooLarge = "window_too_large"
	bridgedMinSplitWindow        = time.Hour
)

func validateCaptureFailureReason(reason string) error {
	if reason == "" || reason == captureFailureWindowTooLarge {
		return nil
	}
	return fmt.Errorf("unknown capture failure reason %q; supported reason: %s", reason, captureFailureWindowTooLarge)
}

func narrowedCaptureUntil(since time.Time, until time.Time) (time.Time, bool) {
	window := until.Sub(since)
	narrowed := window / 2
	if narrowed < bridgedMinSplitWindow {
		narrowed = bridgedMinSplitWindow
	}
	if narrowed >= window {
		return until, false
	}
	return since.Add(narrowed), true
}

func narrowCaptureRequestWindow(db *sql.DB, requestID string, claimToken string, errorMessage string, now time.Time) (bool, error) {
	requestID = strings.TrimSpace(requestID)
	var sinceText, untilText string
	if err := db.QueryRow(`SELECT since, until FROM capture_requests WHERE id = ?`, requestID).Scan(&sinceText, &untilText); err != nil {
		return false, fmt.Errorf("read capture request window: %w", err)
	}
	since, err := time.Parse(time.RFC3339Nano, sinceText)
	if err != nil {
		return false, fmt.Errorf("parse capture request since: %w", err)
	}
	until, err := time.Parse(time.RFC3339Nano, untilText)
	if err != nil {
		return false, fmt.Errorf("parse capture request until: %w", err)
	}
	narrowedUntil, ok := narrowedCaptureUntil(since.UTC(), until.UTC())
	if !ok {
		return false, nil
	}
	narrowedText := narrowedUntil.Format(time.RFC3339Nano)
	detail := fmt.Sprintf("window too large; narrowed to %s through %s: %s", sinceText, narrowedText, errorMessage)
	updated, err := db.Exec(`UPDATE capture_requests SET
		status = 'pending', until = ?, available_at = ?, last_error = ?, claim_token = NULL,
		claimed_by = NULL, claimed_at = NULL, lease_expires_at = NULL
		WHERE id = ? AND status = 'claimed' AND claim_token = ?`,
		narrowedText, now.Format(time.RFC3339Nano), detail, requestID, claimToken)
	if err != nil {
		return false, fmt.Errorf("narrow capture request window: %w", err)
	}
	if count, _ := updated.RowsAffected(); count != 1 {
		return false, fmt.Errorf("capture request claim token is stale or invalid")
	}
	return true, nil
}
