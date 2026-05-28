package workgraph

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// SlackCaptureConfig controls Slack event ingestion.
type SlackCaptureConfig struct {
	HomeDir      string
	DatabasePath string
	WatchDirs    []string
	EventsFile   string
}

// SlackCaptureResult describes a Slack capture run.
type SlackCaptureResult struct {
	HomeDir      string
	DatabasePath string
	EventsStored int
	Message      string
}

type slackExportEvent struct {
	Kind        string `json:"kind"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Project     string `json:"project,omitempty"`
	User        string `json:"user"`
	Text        string `json:"text"`
	TS          string `json:"ts"`
	ThreadTS    string `json:"thread_ts,omitempty"`
	Permalink   string `json:"permalink"`
	Timestamp   string `json:"timestamp"`
}

type slackEventPayload struct {
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	User        string `json:"user"`
	Text        string `json:"text"`
	TS          string `json:"ts"`
	ThreadTS    string `json:"thread_ts,omitempty"`
	Permalink   string `json:"permalink"`
}

// CaptureSlackEvents stores Slack events from a local export file.
func CaptureSlackEvents(config SlackCaptureConfig) (SlackCaptureResult, error) {
	status, err := prepareRunStatus(RunConfig{
		HomeDir:      config.HomeDir,
		DatabasePath: config.DatabasePath,
		WatchDirs:    config.WatchDirs,
	})
	if err != nil {
		return SlackCaptureResult{}, err
	}
	if config.EventsFile == "" {
		return SlackCaptureResult{}, errors.New("events file is required")
	}

	eventsFile, err := filepath.Abs(config.EventsFile)
	if err != nil {
		return SlackCaptureResult{}, fmt.Errorf("resolve events file: %w", err)
	}
	contents, err := os.ReadFile(eventsFile)
	if err != nil {
		return SlackCaptureResult{}, fmt.Errorf("read events file: %w", err)
	}
	var exported []slackExportEvent
	if err := json.Unmarshal(contents, &exported); err != nil {
		return SlackCaptureResult{}, fmt.Errorf("parse events file: %w", err)
	}

	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return SlackCaptureResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return SlackCaptureResult{}, fmt.Errorf("open database: %w", err)
	}

	stored := 0
	for _, event := range exported {
		inserted, err := storeSlackEvent(db, event)
		if err != nil {
			return SlackCaptureResult{}, err
		}
		if inserted {
			stored++
		}
	}

	result := SlackCaptureResult{
		HomeDir:      status.HomeDir,
		DatabasePath: status.DatabasePath,
		EventsStored: stored,
	}
	result.Message = slackCaptureMessage(result)
	return result, nil
}

func storeSlackEvent(db *sql.DB, event slackExportEvent) (bool, error) {
	eventType := slackEventType(event.Kind)
	if eventType == "" {
		return false, nil
	}

	payload, err := json.Marshal(slackEventPayload{
		ChannelID:   event.ChannelID,
		ChannelName: event.ChannelName,
		User:        event.User,
		Text:        event.Text,
		TS:          event.TS,
		ThreadTS:    event.ThreadTS,
		Permalink:   event.Permalink,
	})
	if err != nil {
		return false, fmt.Errorf("encode slack event: %w", err)
	}

	timestamp := time.Now().UTC()
	if event.Timestamp != "" {
		parsed, err := time.Parse(time.RFC3339Nano, event.Timestamp)
		if err != nil {
			return false, fmt.Errorf("parse slack event timestamp: %w", err)
		}
		timestamp = parsed
	}

	result, err := db.Exec(`INSERT OR IGNORE INTO events
		(id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		fmt.Sprintf("%s:%s:%s", eventType, event.ChannelID, event.TS),
		"slack",
		eventType,
		timestamp.UTC().Format(time.RFC3339Nano),
		string(payload),
		inferSlackProject(event),
		event.User,
		event.Text,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return false, fmt.Errorf("store slack event: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store slack event: %w", err)
	}
	return rows > 0, nil
}

func slackEventType(kind string) string {
	switch kind {
	case "message":
		return "slack.message"
	case "thread_reply":
		return "slack.thread_reply"
	default:
		return ""
	}
}

func inferSlackProject(event slackExportEvent) string {
	if event.Project != "" {
		return event.Project
	}
	return strings.TrimPrefix(event.ChannelName, "#")
}

func slackCaptureMessage(result SlackCaptureResult) string {
	lines := []string{
		"Slack capture complete",
		"Home: " + result.HomeDir,
		"Database: " + result.DatabasePath,
		fmt.Sprintf("Events stored: %d", result.EventsStored),
	}
	return strings.Join(lines, "\n")
}
