package workgraph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const (
	autoApplyConfidence      = 0.95
	autoApplyAccuracyWindow  = 14 * 24 * time.Hour
	autoApplyMinimumSample   = 20
	autoApplyPausedEvent     = "projects.auto_apply_paused"
	autoApplyResumedEvent    = "projects.auto_apply_resumed"
	autoApplyRejectionWindow = 30 * 24 * time.Hour
)

type ProjectLinkAccuracyReport struct {
	Window      time.Duration
	Since       time.Time
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
	Paused bool   `json:"paused"`
	Reason string `json:"reason"`
}

func ProjectLinkAccuracy(config ProjectLinkStoreConfig) (ProjectLinkAccuracyReport, error) {
	dbPath, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return ProjectLinkAccuracyReport{}, err
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return ProjectLinkAccuracyReport{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	return projectLinkAccuracy(db, now.UTC())
}

func (report ProjectLinkAccuracyReport) Summary() string {
	line := fmt.Sprintf("Project auto-apply accuracy: insufficient_data (%d decided auto links, 14 day window)", report.Decided)
	if report.Status == "measured" {
		line = fmt.Sprintf("Project auto-apply accuracy: %.2f (%d/%d decided auto links, 14 day window)", report.Accuracy, report.Confirmed, report.Decided)
	}
	if report.Paused {
		line += "\nAuto-apply paused: " + report.PauseReason
	}
	return line
}

func projectLinkAccuracy(db projectLinkQueryer, now time.Time) (ProjectLinkAccuracyReport, error) {
	report := ProjectLinkAccuracyReport{Window: autoApplyAccuracyWindow, Since: now.Add(-autoApplyAccuracyWindow), Status: "insufficient_data"}
	exists, err := projectLinksTablePresent(db)
	if err != nil {
		return ProjectLinkAccuracyReport{}, err
	}
	if exists {
		links, err := queryProjectLinks(db, `WHERE source = 'llm' AND auto = 1 AND decision IN ('confirmed', 'corrected', 'undone')`)
		if err != nil {
			return ProjectLinkAccuracyReport{}, err
		}
		for _, link := range links {
			if link.DecidedAt.Before(report.Since) || link.DecidedAt.After(now) {
				continue
			}
			switch link.Decision {
			case "confirmed":
				report.Confirmed++
			case "corrected":
				report.Corrected++
			case "undone":
				report.Undone++
			}
		}
	}
	report.Decided = report.Confirmed + report.Corrected + report.Undone
	recorded, err := latestAutoApplyPauseState(db)
	if err != nil {
		return ProjectLinkAccuracyReport{}, err
	}
	report.Paused, report.PauseReason = recorded.Paused, recorded.Reason
	if report.Decided >= autoApplyMinimumSample {
		report.Status = "measured"
		report.Accuracy = float64(report.Confirmed) / float64(report.Decided)
		report.Paused = report.Accuracy < autoApplyConfidence
		report.PauseReason = ""
		if report.Paused {
			report.PauseReason = fmt.Sprintf("accuracy %.2f over %d decided auto links in the last 14 days is below 0.95", report.Accuracy, report.Decided)
		}
	}
	if report.Paused != recorded.Paused {
		report.pauseEvent = autoApplyResumedEvent
		if report.Paused {
			report.pauseEvent = autoApplyPausedEvent
		}
	}
	return report, nil
}

func projectLinksTablePresent(db projectLinkQueryer) (bool, error) {
	rows, err := db.Query(`SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'project_links'`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}

func latestAutoApplyPauseState(db projectLinkQueryer) (autoApplyPauseState, error) {
	rows, err := db.Query(`SELECT payload_json FROM events WHERE type IN (?, ?) ORDER BY timestamp DESC, rowid DESC LIMIT 1`, autoApplyPausedEvent, autoApplyResumedEvent)
	if err != nil {
		return autoApplyPauseState{}, fmt.Errorf("query auto-apply pause: %w", err)
	}
	defer rows.Close()
	var state autoApplyPauseState
	if rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return autoApplyPauseState{}, err
		}
		if err := json.Unmarshal([]byte(payload), &state); err != nil {
			return autoApplyPauseState{}, fmt.Errorf("parse auto-apply pause: %w", err)
		}
	}
	return state, rows.Err()
}

func recordAutoApplyPauseTransition(tx *sql.Tx, now time.Time, report ProjectLinkAccuracyReport) error {
	if report.pauseEvent == "" {
		return nil
	}
	id, err := newEventID()
	if err != nil {
		return err
	}
	summary := "Project auto-apply resumed"
	if report.Paused {
		summary = "Project auto-apply paused: " + report.PauseReason
	}
	payload, err := json.Marshal(map[string]any{
		"paused": report.Paused, "reason": report.PauseReason, "accuracy": report.Accuracy,
		"confirmed": report.Confirmed, "corrected": report.Corrected, "undone": report.Undone, "decided": report.Decided,
		"window_days": int(autoApplyAccuracyWindow / (24 * time.Hour)),
	})
	if err != nil {
		return err
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES (?, 'projects', ?, ?, ?, '', '', ?, ?)`, report.pauseEvent+":"+id, report.pauseEvent, timestamp, string(payload), summary, timestamp); err != nil {
		return fmt.Errorf("record %s: %w", report.pauseEvent, err)
	}
	return nil
}
