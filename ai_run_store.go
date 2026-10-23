package workgraph

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

const aiRunBusyTimeoutMillis = "5000"

var (
	aiRunRetryInitialDelay = 100 * time.Millisecond
	aiRunRetryMaxDelay     = 2 * time.Second
	aiRunRetryBudget       = 15 * time.Second
	aiRunOpenDatabase      = func(databasePath string) (*sql.DB, error) {
		return sql.Open("sqlite3", aiRunDatabaseDSN(databasePath))
	}
)

func aiRunDatabaseDSN(databasePath string) string {
	slashed := filepath.ToSlash(databasePath)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	location := url.URL{Scheme: "file", Path: slashed, RawQuery: "mode=rw&_busy_timeout=" + aiRunBusyTimeoutMillis}
	return location.String()
}

func withAIRunDatabase(databasePath string, use func(*sql.DB) error) error {
	db, err := aiRunOpenDatabase(databasePath)
	if err != nil {
		return fmt.Errorf("open AI event database: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	return use(db)
}

func retryAIRunDatabase(attempt func() error, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	delay := aiRunRetryInitialDelay
	for {
		err := attempt()
		if err == nil || !isAIRunDatabaseUnavailable(err) || time.Now().Add(delay).After(deadline) {
			return err
		}
		time.Sleep(delay)
		delay = min(delay*2, aiRunRetryMaxDelay)
	}
}

type aiRunStartResult struct {
	err         error
	delayedBy   error
	childExited bool
}

func persistAIRunStart(write func() error, exited <-chan struct{}) aiRunStartResult {
	delay := aiRunRetryInitialDelay
	var firstUnavailable error
	for {
		err := write()
		if err == nil || !isAIRunDatabaseUnavailable(err) {
			return aiRunStartResult{err: err, delayedBy: firstUnavailable, childExited: isAIRunChildExited(exited)}
		}
		if firstUnavailable == nil {
			firstUnavailable = err
		}
		select {
		case <-exited:
			return aiRunStartResult{err: retryAIRunDatabase(write, aiRunRetryBudget), delayedBy: firstUnavailable, childExited: true}
		case <-time.After(delay):
		}
		delay = min(delay*2, aiRunRetryMaxDelay)
	}
}

func isAIRunChildExited(exited <-chan struct{}) bool {
	select {
	case <-exited:
		return true
	default:
		return false
	}
}
