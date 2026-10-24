package workgraph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	autoApplyAccuracyWindow  = 14 * 24 * time.Hour
	autoApplyMinimumSample   = 20
	autoApplyPausedEvent     = "projects.auto_apply_paused"
	autoApplyResumedEvent    = "projects.auto_apply_resumed"
	autoApplyRejectionWindow = 30 * 24 * time.Hour
)

type ProjectLinkAccuracyReport struct {
	Window      time.Duration
	Since       time.Time
	Profiles    []ProfileLinkAccuracy
	Paused      bool
	PauseReason string
	thresholds  autoApplyThresholds
	legacy      autoApplyPauseState
}

type ProfileLinkAccuracy struct {
	Profile     string
	Required    float64
	Confirmed   int
	Corrected   int
	Undone      int
	Decided     int
	Accuracy    float64
	Status      string
	Paused      bool
	PauseReason string
	pauseEvent  string
}

type autoApplyPauseState struct {
	Paused  bool    `json:"paused"`
	Reason  string  `json:"reason"`
	Profile *string `json:"profile"`
}

func ProjectLinkAccuracy(config ProjectLinkStoreConfig) (ProjectLinkAccuracyReport, error) {
	dbPath, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return ProjectLinkAccuracyReport{}, err
	}
	db, err := openDatabase(dbPath)
	if err != nil {
		return ProjectLinkAccuracyReport{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	return projectLinkAccuracy(db, now.UTC(), loadAutoApplyThresholds(config.HomeDir))
}

func (report ProjectLinkAccuracyReport) For(profile string) ProfileLinkAccuracy {
	for _, entry := range report.Profiles {
		if entry.Profile == profile {
			return entry
		}
	}
	return ProfileLinkAccuracy{Profile: profile, Required: report.thresholds.of(profile), Status: "insufficient_data", Paused: report.legacy.Paused, PauseReason: report.legacy.Reason}
}

func (report ProjectLinkAccuracyReport) Summary() string {
	if len(report.Profiles) == 0 {
		return "Project auto-apply accuracy: insufficient_data (0 decided auto links, 14 day window)"
	}
	var lines, paused []string
	for _, entry := range report.Profiles {
		line := fmt.Sprintf("Project auto-apply accuracy for %s: insufficient_data (%d decided auto links, 14 day window)", entry.Profile, entry.Decided)
		if entry.Status == "measured" {
			line = fmt.Sprintf("Project auto-apply accuracy for %s: %.2f (%d/%d decided auto links, 14 day window, required %.2f)", entry.Profile, entry.Accuracy, entry.Confirmed, entry.Decided, entry.Required)
		}
		lines = append(lines, line)
		if entry.Paused {
			paused = append(paused, "Auto-apply paused: "+entry.PauseReason)
		}
	}
	return strings.Join(append(lines, paused...), "\n")
}

func projectLinkAccuracy(db projectLinkQueryer, now time.Time, thresholds autoApplyThresholds, requested ...string) (ProjectLinkAccuracyReport, error) {
	report := ProjectLinkAccuracyReport{Window: autoApplyAccuracyWindow, Since: now.Add(-autoApplyAccuracyWindow), thresholds: thresholds}
	entries := map[string]*ProfileLinkAccuracy{}
	entry := func(profile string) *ProfileLinkAccuracy {
		if entries[profile] == nil {
			entries[profile] = &ProfileLinkAccuracy{Profile: profile, Required: thresholds.of(profile), Status: "insufficient_data"}
		}
		return entries[profile]
	}
	for _, profile := range requested {
		entry(profile)
	}
	exists, err := projectLinksTablePresent(db)
	if err != nil {
		return ProjectLinkAccuracyReport{}, err
	}
	if exists {
		links, err := queryProjectLinks(db, `WHERE source = 'llm'`)
		if err != nil {
			return ProjectLinkAccuracyReport{}, err
		}
		for _, link := range links {
			counted := entry(link.Profile)
			if !link.Auto || link.DecidedAt.Before(report.Since) || link.DecidedAt.After(now) {
				continue
			}
			switch link.Decision {
			case "confirmed":
				counted.Confirmed++
			case "corrected":
				counted.Corrected++
			case "undone":
				counted.Undone++
			}
		}
	}
	recorded, legacy, err := latestAutoApplyPauseStates(db)
	if err != nil {
		return ProjectLinkAccuracyReport{}, err
	}
	report.legacy = legacy
	for profile := range recorded {
		entry(profile)
	}
	for _, counted := range entries {
		state, found := recorded[counted.Profile]
		if !found {
			state = legacy
		}
		measureProfileAccuracy(counted, state)
		report.Profiles = append(report.Profiles, *counted)
	}
	sort.Slice(report.Profiles, func(i, j int) bool { return report.Profiles[i].Profile < report.Profiles[j].Profile })
	var reasons []string
	for _, counted := range report.Profiles {
		if counted.Paused {
			report.Paused = true
			reasons = append(reasons, counted.PauseReason)
		}
	}
	report.PauseReason = strings.Join(reasons, "; ")
	return report, nil
}

func measureProfileAccuracy(entry *ProfileLinkAccuracy, recorded autoApplyPauseState) {
	entry.Decided = entry.Confirmed + entry.Corrected + entry.Undone
	entry.Paused, entry.PauseReason = recorded.Paused, recorded.Reason
	if entry.Decided >= autoApplyMinimumSample {
		entry.Status = "measured"
		entry.Accuracy = float64(entry.Confirmed) / float64(entry.Decided)
		entry.Paused = entry.Accuracy < entry.Required
		entry.PauseReason = ""
		if entry.Paused {
			entry.PauseReason = fmt.Sprintf("%s accuracy %.2f over %d decided auto links in the last 14 days is below %.2f", entry.Profile, entry.Accuracy, entry.Decided, entry.Required)
		}
	}
	if entry.Paused != recorded.Paused {
		entry.pauseEvent = autoApplyResumedEvent
		if entry.Paused {
			entry.pauseEvent = autoApplyPausedEvent
		}
	}
}

func projectLinksTablePresent(db projectLinkQueryer) (bool, error) {
	rows, err := db.Query(`SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'project_links'`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}

func latestAutoApplyPauseStates(db projectLinkQueryer) (map[string]autoApplyPauseState, autoApplyPauseState, error) {
	rows, err := db.Query(`SELECT payload_json FROM events WHERE type IN (?, ?) ORDER BY timestamp DESC, rowid DESC`, autoApplyPausedEvent, autoApplyResumedEvent)
	if err != nil {
		return nil, autoApplyPauseState{}, fmt.Errorf("query auto-apply pause: %w", err)
	}
	defer rows.Close()
	states := map[string]autoApplyPauseState{}
	var legacy autoApplyPauseState
	legacyFound := false
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, autoApplyPauseState{}, err
		}
		var state autoApplyPauseState
		if err := json.Unmarshal([]byte(payload), &state); err != nil {
			return nil, autoApplyPauseState{}, fmt.Errorf("parse auto-apply pause: %w", err)
		}
		if state.Profile == nil {
			if !legacyFound {
				legacy, legacyFound = state, true
			}
			continue
		}
		if _, seen := states[*state.Profile]; !seen {
			states[*state.Profile] = state
		}
	}
	return states, legacy, rows.Err()
}

func recordProjectAutoApplyState(tx *sql.Tx, now time.Time, thresholds autoApplyThresholds) error {
	report, err := projectLinkAccuracy(tx, now, thresholds)
	if err != nil {
		return err
	}
	return recordAutoApplyPauseTransition(tx, now, report)
}

func recordAutoApplyPauseTransition(tx *sql.Tx, now time.Time, report ProjectLinkAccuracyReport) error {
	for _, entry := range report.Profiles {
		if err := recordProfilePauseTransition(tx, now, entry); err != nil {
			return err
		}
	}
	return nil
}

func recordProfilePauseTransition(tx *sql.Tx, now time.Time, entry ProfileLinkAccuracy) error {
	if entry.pauseEvent == "" {
		return nil
	}
	id, err := newEventID()
	if err != nil {
		return err
	}
	summary := "Project auto-apply resumed for " + entry.Profile
	if entry.Paused {
		summary = "Project auto-apply paused: " + entry.PauseReason
	}
	payload, err := json.Marshal(map[string]any{
		"profile": entry.Profile, "required_accuracy": entry.Required,
		"paused": entry.Paused, "reason": entry.PauseReason, "accuracy": entry.Accuracy,
		"confirmed": entry.Confirmed, "corrected": entry.Corrected, "undone": entry.Undone, "decided": entry.Decided,
		"window_days": int(autoApplyAccuracyWindow / (24 * time.Hour)),
	})
	if err != nil {
		return err
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES (?, 'projects', ?, ?, ?, '', '', ?, ?)`, entry.pauseEvent+":"+id, entry.pauseEvent, timestamp, string(payload), summary, timestamp); err != nil {
		return fmt.Errorf("record %s: %w", entry.pauseEvent, err)
	}
	return nil
}
