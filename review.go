package workgraph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EffectivenessReviewConfig controls the local suggestion effectiveness review.
type EffectivenessReviewConfig struct {
	HomeDir      string
	DatabasePath string
	Since        string
	Format       string
	Now          time.Time
}

// ReviewWindow is the deterministic, inclusive-start/exclusive-end metric window.
type ReviewWindow struct {
	Kind     string    `json:"kind"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Timezone string    `json:"timezone"`
}

// ReviewRateMetric describes one disposition rate and its shared denominator.
type ReviewRateMetric struct {
	Status      string   `json:"status"`
	Count       int      `json:"count"`
	Denominator int      `json:"denominator"`
	RatePercent *float64 `json:"rate_percent"`
}

// ReviewDismissalReason is one stable dismissal reason count.
type ReviewDismissalReason struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

// ReviewDurationMetric describes the median time to a suggestion's first useful event.
type ReviewDurationMetric struct {
	Status        string   `json:"status"`
	SampleSize    int      `json:"sample_size"`
	MedianSeconds *float64 `json:"median_seconds"`
}

// ReviewConnectorState describes freshness from current local connector state.
type ReviewConnectorState struct {
	ID                  string `json:"id"`
	Freshness           string `json:"freshness"`
	Degraded            bool   `json:"degraded"`
	Enabled             bool   `json:"enabled"`
	SetupState          string `json:"setup_state"`
	IntervalSeconds     int64  `json:"interval_seconds"`
	LastSuccessAt       string `json:"last_success_at,omitempty"`
	AgeSeconds          *int64 `json:"age_seconds"`
	LastError           string `json:"last_error,omitempty"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
}

// EffectivenessReviewResult contains local-only review metrics and rendered output.
type EffectivenessReviewResult struct {
	Window           ReviewWindow
	Acceptance       ReviewRateMetric
	Dismissal        ReviewRateMetric
	Snooze           ReviewRateMetric
	DismissalReasons []ReviewDismissalReason
	TimeToUseful     ReviewDurationMetric
	Connectors       []ReviewConnectorState
	Message          string
}

type effectivenessReviewPayload struct {
	Window           ReviewWindow            `json:"window"`
	Acceptance       ReviewRateMetric        `json:"acceptance_rate"`
	Dismissal        ReviewRateMetric        `json:"dismissal_rate"`
	Snooze           ReviewRateMetric        `json:"snooze_rate"`
	DismissalReasons []ReviewDismissalReason `json:"dismissal_reasons"`
	TimeToUseful     ReviewDurationMetric    `json:"time_to_useful_suggestion"`
	Connectors       []ReviewConnectorState  `json:"connectors"`
}

// EffectivenessReview computes suggestion and connector metrics entirely from local state.
func EffectivenessReview(config EffectivenessReviewConfig) (EffectivenessReviewResult, error) {
	window, err := resolveReviewWindow(config.Since, config.Now)
	if err != nil {
		return EffectivenessReviewResult{}, err
	}
	format := strings.TrimSpace(config.Format)
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" {
		return EffectivenessReviewResult{}, fmt.Errorf("unsupported review format %q", config.Format)
	}

	runStatus, err := prepareRunStatus(RunConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return EffectivenessReviewResult{}, err
	}
	db, err := openSuggestionDatabase(runStatus.HomeDir, runStatus.DatabasePath)
	if err != nil {
		return EffectivenessReviewResult{}, err
	}
	defer db.Close()
	result, err := collectSuggestionReview(db, window)
	if err != nil {
		return EffectivenessReviewResult{}, err
	}
	connectorRuntime, err := readConnectorRuntimeFile(runStatus.HomeDir)
	if err != nil {
		return EffectivenessReviewResult{}, err
	}
	result.Connectors = collectReviewConnectorStates(connectorStatuses(runStatus.HomeDir, connectorRuntime), window.End)
	if result.DismissalReasons == nil {
		result.DismissalReasons = []ReviewDismissalReason{}
	}
	if result.Connectors == nil {
		result.Connectors = []ReviewConnectorState{}
	}

	if format == "json" {
		contents, err := json.MarshalIndent(reviewPayload(result), "", "  ")
		if err != nil {
			return EffectivenessReviewResult{}, fmt.Errorf("encode effectiveness review: %w", err)
		}
		result.Message = string(contents)
	} else {
		result.Message = formatEffectivenessReviewText(result)
	}
	return result, nil
}

func resolveReviewWindow(since string, configuredNow time.Time) (ReviewWindow, error) {
	now := configuredNow
	if now.IsZero() {
		now = time.Now()
	}
	kind := strings.TrimSpace(since)
	if kind == "" {
		kind = "week"
	}
	window := ReviewWindow{Kind: kind, End: now, Timezone: now.Location().String()}
	switch kind {
	case "week":
		local := now.In(now.Location())
		daysSinceMonday := (int(local.Weekday()) + 6) % 7
		window.Start = time.Date(local.Year(), local.Month(), local.Day()-daysSinceMonday, 0, 0, 0, 0, local.Location())
	case "7d":
		window.Start = now.Add(-168 * time.Hour)
	case "30d":
		window.Start = now.Add(-720 * time.Hour)
	default:
		return ReviewWindow{}, fmt.Errorf("unsupported review window %q", since)
	}
	return window, nil
}

func collectSuggestionReview(db *sql.DB, window ReviewWindow) (EffectivenessReviewResult, error) {
	result := EffectivenessReviewResult{Window: window}
	dispositions := map[string]int{"accepted": 0, "dismissed": 0, "snoozed": 0}
	reasons := map[string]int{}
	rows, err := db.Query(`SELECT action, COALESCE(reason_code, ''), created_at FROM suggestion_feedback WHERE action IN ('accepted', 'dismissed', 'snoozed')`)
	if err != nil {
		return EffectivenessReviewResult{}, fmt.Errorf("query suggestion dispositions: %w", err)
	}
	for rows.Next() {
		var action, reason, createdAt string
		if err := rows.Scan(&action, &reason, &createdAt); err != nil {
			rows.Close()
			return EffectivenessReviewResult{}, fmt.Errorf("read suggestion disposition: %w", err)
		}
		created, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			rows.Close()
			return EffectivenessReviewResult{}, fmt.Errorf("parse suggestion feedback timestamp %q: %w", createdAt, err)
		}
		if created.Before(window.Start) || !created.Before(window.End) {
			continue
		}
		dispositions[action]++
		if action == "dismissed" {
			reason = strings.TrimSpace(reason)
			if reason == "" {
				reason = "unspecified"
			}
			reasons[reason]++
		}
	}
	if err := rows.Close(); err != nil {
		return EffectivenessReviewResult{}, fmt.Errorf("close suggestion dispositions: %w", err)
	}
	if err := rows.Err(); err != nil {
		return EffectivenessReviewResult{}, fmt.Errorf("query suggestion dispositions: %w", err)
	}
	denominator := dispositions["accepted"] + dispositions["dismissed"] + dispositions["snoozed"]
	result.Acceptance = reviewRate(dispositions["accepted"], denominator)
	result.Dismissal = reviewRate(dispositions["dismissed"], denominator)
	result.Snooze = reviewRate(dispositions["snoozed"], denominator)
	for code, count := range reasons {
		result.DismissalReasons = append(result.DismissalReasons, ReviewDismissalReason{Code: code, Count: count})
	}
	sort.Slice(result.DismissalReasons, func(i, j int) bool {
		if result.DismissalReasons[i].Count != result.DismissalReasons[j].Count {
			return result.DismissalReasons[i].Count > result.DismissalReasons[j].Count
		}
		return result.DismissalReasons[i].Code < result.DismissalReasons[j].Code
	})
	duration, err := collectTimeToUseful(db, window)
	if err != nil {
		return EffectivenessReviewResult{}, err
	}
	result.TimeToUseful = duration
	return result, nil
}

func reviewRate(count int, denominator int) ReviewRateMetric {
	metric := ReviewRateMetric{Status: "insufficient_data", Count: count, Denominator: denominator}
	if denominator == 0 {
		return metric
	}
	rate := float64(count) * 100 / float64(denominator)
	metric.Status = "available"
	metric.RatePercent = &rate
	return metric
}

func collectTimeToUseful(db *sql.DB, window ReviewWindow) (ReviewDurationMetric, error) {
	rows, err := db.Query(`SELECT s.id, s.created_at, sf.created_at
		FROM suggestions s
		JOIN suggestion_feedback sf ON sf.suggestion_id = s.id
		WHERE sf.action IN ('accepted', 'completed')`)
	if err != nil {
		return ReviewDurationMetric{}, fmt.Errorf("query time to useful suggestion: %w", err)
	}
	defer rows.Close()
	type usefulSuggestion struct {
		created time.Time
		first   time.Time
	}
	usefulByID := map[string]usefulSuggestion{}
	for rows.Next() {
		var id, createdAt, usefulAt string
		if err := rows.Scan(&id, &createdAt, &usefulAt); err != nil {
			return ReviewDurationMetric{}, fmt.Errorf("read time to useful suggestion: %w", err)
		}
		created, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return ReviewDurationMetric{}, fmt.Errorf("parse suggestion timestamp %q: %w", createdAt, err)
		}
		useful, err := time.Parse(time.RFC3339Nano, usefulAt)
		if err != nil {
			return ReviewDurationMetric{}, fmt.Errorf("parse useful feedback timestamp %q: %w", usefulAt, err)
		}
		current, exists := usefulByID[id]
		if !exists || useful.Before(current.first) {
			usefulByID[id] = usefulSuggestion{created: created, first: useful}
		}
	}
	if err := rows.Err(); err != nil {
		return ReviewDurationMetric{}, fmt.Errorf("query time to useful suggestion: %w", err)
	}
	var durations []int64
	for _, suggestion := range usefulByID {
		if suggestion.first.Before(window.Start) || !suggestion.first.Before(window.End) || suggestion.first.Before(suggestion.created) {
			continue
		}
		durations = append(durations, int64(suggestion.first.Sub(suggestion.created)/time.Second))
	}
	metric := ReviewDurationMetric{Status: "insufficient_data", SampleSize: len(durations)}
	if len(durations) == 0 {
		return metric, nil
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	middle := len(durations) / 2
	median := float64(durations[middle])
	if len(durations)%2 == 0 {
		median = float64(durations[middle-1]+durations[middle]) / 2
	}
	metric.Status = "available"
	metric.MedianSeconds = &median
	return metric, nil
}

func collectReviewConnectorStates(statuses []ConnectorStatus, now time.Time) []ReviewConnectorState {
	var result []ReviewConnectorState
	for _, status := range statuses {
		if !status.Connected {
			continue
		}
		state := ReviewConnectorState{
			ID:                  status.ID,
			Enabled:             status.Enabled,
			SetupState:          status.SetupState,
			IntervalSeconds:     int64(status.Interval / time.Second),
			LastSuccessAt:       status.LastSuccess,
			LastError:           status.LastError,
			ConsecutiveFailures: status.ConsecutiveFailures,
			Degraded:            status.SetupState == "error" || strings.TrimSpace(status.LastError) != "" || status.ConsecutiveFailures > 0,
		}
		switch {
		case !status.Enabled:
			state.Freshness = "disabled"
		case status.SetupState != "ready":
			state.Freshness = "not_ready"
		case strings.TrimSpace(status.LastSuccess) == "":
			state.Freshness = "unknown"
		default:
			lastSuccess, err := time.Parse(time.RFC3339Nano, status.LastSuccess)
			if err != nil || lastSuccess.After(now) {
				state.Freshness = "unknown"
				break
			}
			age := int64(now.Sub(lastSuccess) / time.Second)
			state.AgeSeconds = &age
			if now.Sub(lastSuccess) <= 2*status.Interval {
				state.Freshness = "fresh"
			} else {
				state.Freshness = "stale"
			}
		}
		result = append(result, state)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func reviewPayload(result EffectivenessReviewResult) effectivenessReviewPayload {
	return effectivenessReviewPayload{
		Window: result.Window, Acceptance: result.Acceptance, Dismissal: result.Dismissal,
		Snooze: result.Snooze, DismissalReasons: result.DismissalReasons,
		TimeToUseful: result.TimeToUseful, Connectors: result.Connectors,
	}
}

func formatEffectivenessReviewText(result EffectivenessReviewResult) string {
	lines := []string{
		"Local effectiveness review",
		fmt.Sprintf("Window: %s [%s, %s) %s", result.Window.Kind, result.Window.Start.Format(time.RFC3339), result.Window.End.Format(time.RFC3339), result.Window.Timezone),
		formatReviewRateLine("Acceptance rate", result.Acceptance),
		formatReviewRateLine("Dismissal rate", result.Dismissal),
		formatReviewRateLine("Snooze rate", result.Snooze),
		"Dismissal reasons:",
	}
	if len(result.DismissalReasons) == 0 {
		lines = append(lines, "- none in window")
	} else {
		for _, reason := range result.DismissalReasons {
			lines = append(lines, fmt.Sprintf("- %s: %d", reason.Code, reason.Count))
		}
	}
	if result.TimeToUseful.MedianSeconds == nil {
		lines = append(lines, fmt.Sprintf("Time to useful suggestion: insufficient_data (%d samples)", result.TimeToUseful.SampleSize))
	} else {
		lines = append(lines, fmt.Sprintf("Time to useful suggestion: available, %s seconds median (%d samples)", formatReviewNumber(*result.TimeToUseful.MedianSeconds), result.TimeToUseful.SampleSize))
	}
	lines = append(lines, "Connector freshness:")
	if len(result.Connectors) == 0 {
		lines = append(lines, "- no connected connectors")
	} else {
		for _, connector := range result.Connectors {
			line := fmt.Sprintf("- %s: freshness %s, degraded %t, enabled %t, setup %s, interval %d seconds", connector.ID, connector.Freshness, connector.Degraded, connector.Enabled, connector.SetupState, connector.IntervalSeconds)
			if connector.LastSuccessAt != "" {
				line += ", last success " + connector.LastSuccessAt
			}
			if connector.AgeSeconds != nil {
				line += fmt.Sprintf(", age %d seconds", *connector.AgeSeconds)
			}
			if connector.LastError != "" {
				line += ", last error " + connector.LastError
			}
			line += fmt.Sprintf(", consecutive failures %d", connector.ConsecutiveFailures)
			lines = append(lines, line)
		}
	}
	lines = append(lines, "Local only: no metrics were transmitted.")
	return strings.Join(lines, "\n")
}

func formatReviewRateLine(label string, metric ReviewRateMetric) string {
	if metric.RatePercent == nil {
		return fmt.Sprintf("%s: insufficient_data (%d/%d disposition events)", label, metric.Count, metric.Denominator)
	}
	return fmt.Sprintf("%s: available, %.2f%% (%d/%d disposition events)", label, *metric.RatePercent, metric.Count, metric.Denominator)
}

func formatReviewNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
