package workgraph

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"
)

const githubPRWatchHintDetailAge = 2 * time.Minute

type GitHubPRWatchHintConfig struct {
	HomeDir      string
	DatabasePath string
	PRURL        string
}

type GitHubPRWatchHintResult struct {
	HomeDir      string
	DatabasePath string
	Message      string
}

type githubPRWatchHints struct {
	pending bool
	urls    map[string]bool
}

func RecordGitHubPRWatchHint(config GitHubPRWatchHintConfig) (GitHubPRWatchHintResult, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return GitHubPRWatchHintResult{}, err
	}
	databasePath := config.DatabasePath
	if databasePath == "" {
		databasePath = filepath.Join(homeDir, "workgraph.db")
	}
	db, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		return GitHubPRWatchHintResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if _, err := db.Exec(connectorPollHintsSchema); err != nil {
		return GitHubPRWatchHintResult{}, fmt.Errorf("prepare hint schema: %w", err)
	}
	if _, err := db.Exec(`INSERT INTO connector_poll_hints (connector_id, received_at, pr_url) VALUES (?, ?, ?)`,
		githubPRWatchConnectorID, time.Now().UTC().Format(time.RFC3339Nano), strings.TrimSpace(config.PRURL)); err != nil {
		return GitHubPRWatchHintResult{}, fmt.Errorf("record hint: %w", err)
	}
	return GitHubPRWatchHintResult{
		HomeDir:      homeDir,
		DatabasePath: databasePath,
		Message:      "GitHub PR watch hint recorded; the daemon polls within seconds",
	}, nil
}

func takeGitHubPRWatchHints(db *sql.DB) (githubPRWatchHints, error) {
	rows, err := db.Query(`DELETE FROM connector_poll_hints WHERE connector_id = ? RETURNING pr_url`, githubPRWatchConnectorID)
	if err != nil {
		return githubPRWatchHints{}, fmt.Errorf("take poll hints: %w", err)
	}
	defer rows.Close()
	hints := githubPRWatchHints{urls: map[string]bool{}}
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return githubPRWatchHints{}, fmt.Errorf("take poll hints: %w", err)
		}
		hints.pending = true
		if url != "" {
			hints.urls[url] = true
		}
	}
	return hints, rows.Err()
}

func githubPRWatchHintPending(db *sql.DB) (bool, error) {
	var pending bool
	err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM connector_poll_hints WHERE connector_id = ?)`, githubPRWatchConnectorID).Scan(&pending)
	return pending, err
}

func (hints githubPRWatchHints) forcesDetail(url string, detailAge time.Duration) bool {
	if len(hints.urls) > 0 {
		return hints.urls[url]
	}
	return hints.pending && detailAge > githubPRWatchHintDetailAge
}

func (capture *RunCapture) watchGitHubPRWatchHints(ctx context.Context) {
	ticker := time.NewTicker(capture.connectorHintCheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			capture.signalGitHubPRWatchHints()
		}
	}
}

func (capture *RunCapture) signalGitHubPRWatchHints() {
	capture.githubPRWatchHintMu.Lock()
	defer capture.githubPRWatchHintMu.Unlock()
	pending, err := githubPRWatchHintPending(capture.db)
	if err != nil {
		capture.logConnectorPollError(githubPRWatchConnectorID, fmt.Errorf("check poll hints: %w", err))
		return
	}
	if !pending {
		return
	}
	select {
	case capture.githubPRWatchWake <- struct{}{}:
	default:
	}
}

func (capture *RunCapture) takeGitHubPRWatchHints(db *sql.DB) (githubPRWatchHints, error) {
	capture.githubPRWatchHintMu.Lock()
	defer capture.githubPRWatchHintMu.Unlock()
	select {
	case <-capture.githubPRWatchWake:
	default:
	}
	hints, err := takeGitHubPRWatchHints(db)
	if err != nil {
		return githubPRWatchHints{}, err
	}
	for url := range hints.urls {
		log.Printf("workgraph connector poll hint: connector=%s pr=%q", githubPRWatchConnectorID, normalizeUntrustedText(url))
	}
	return hints, nil
}
