package workgraph

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	aiRunBusyTimeout       = 5 * time.Second
	aiRunRetryInitialDelay = 100 * time.Millisecond
	aiRunRetryMaxDelay     = 2 * time.Second
	aiRunRetryBudget       = 15 * time.Second
	aiRunBeforeChildStart  func()
	aiRunOpenDatabase      = func(databasePath string, busyTimeout time.Duration) (*sql.DB, error) {
		return sql.Open("sqlite3", aiRunDatabaseDSN(databasePath, busyTimeout))
	}
)

func aiRunDatabaseDSN(databasePath string, busyTimeout time.Duration) string {
	slashed := filepath.ToSlash(databasePath)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	millis := max(busyTimeout.Milliseconds(), 1)
	location := url.URL{Scheme: "file", Path: slashed, RawQuery: "mode=rw&_busy_timeout=" + strconv.FormatInt(millis, 10)}
	return location.String()
}

func withAIRunDatabase(databasePath string, busyTimeout time.Duration, use func(*sql.DB) error) error {
	db, err := aiRunOpenDatabase(databasePath, busyTimeout)
	if err != nil {
		return fmt.Errorf("open AI event database: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	return use(db)
}

func retryAIRunDatabase(databasePath string, deadline time.Time, use func(*sql.DB) error) error {
	delay := aiRunRetryInitialDelay
	for {
		err := withAIRunDatabase(databasePath, min(aiRunBusyTimeout, time.Until(deadline)), use)
		if err == nil || !isAIRunDatabaseUnavailable(err) {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return err
		}
		time.Sleep(min(delay, remaining))
		if time.Until(deadline) <= 0 {
			return err
		}
		delay = min(delay*2, aiRunRetryMaxDelay)
	}
}

type aiRunChildExit struct {
	done <-chan struct{}
	at   *time.Time
}

func (exit aiRunChildExit) exited() bool {
	select {
	case <-exit.done:
		return true
	default:
		return false
	}
}

type aiRunStartResult struct {
	err         error
	delayedBy   error
	childExited bool
}

func persistAIRunStart(databasePath string, use func(*sql.DB) error, exit aiRunChildExit) aiRunStartResult {
	delay := aiRunRetryInitialDelay
	var firstUnavailable error
	for {
		busyTimeout := aiRunBusyTimeout
		if exit.exited() {
			busyTimeout = min(busyTimeout, time.Until(exit.at.Add(aiRunRetryBudget)))
		}
		err := withAIRunDatabase(databasePath, busyTimeout, use)
		if err == nil || !isAIRunDatabaseUnavailable(err) {
			return aiRunStartResult{err: err, delayedBy: firstUnavailable, childExited: exit.exited()}
		}
		if firstUnavailable == nil {
			firstUnavailable = err
		}
		if exit.exited() {
			deadline := exit.at.Add(aiRunRetryBudget)
			if time.Until(deadline) > 0 {
				err = retryAIRunDatabase(databasePath, deadline, use)
			}
			return aiRunStartResult{err: err, delayedBy: firstUnavailable, childExited: true}
		}
		select {
		case <-exit.done:
		case <-time.After(delay):
		}
		delay = min(delay*2, aiRunRetryMaxDelay)
	}
}
