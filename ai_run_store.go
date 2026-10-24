package workgraph

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	// SQLite's busy handler counts requested sleeps rather than elapsed time, so
	// each lock wait stays short and retries are judged against a wall-clock deadline.
	aiRunLockWaitSlice     = 250 * time.Millisecond
	aiRunRetryInitialDelay = 50 * time.Millisecond
	aiRunRetryMaxDelay     = 500 * time.Millisecond
	aiRunRetryBudget       = 15 * time.Second
	aiRunBeforeChildStart  func()
	aiRunAfterConnect      func()
	aiRunSignalQueued      func()
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

type aiRunConnection struct {
	ctx      context.Context
	conn     *sql.Conn
	deadline time.Time
}

func (connection aiRunConnection) boundLockWait() error {
	remaining := time.Until(connection.deadline)
	if remaining <= 0 {
		return fmt.Errorf("AI event database retry window: %w", context.DeadlineExceeded)
	}
	lockWait := min(remaining, aiRunLockWaitSlice)
	_, err := connection.conn.ExecContext(connection.ctx, "PRAGMA busy_timeout = "+strconv.FormatInt(max(lockWait.Milliseconds(), 1), 10))
	return err
}

func (connection aiRunConnection) Exec(query string, args ...any) (sql.Result, error) {
	if err := connection.boundLockWait(); err != nil {
		return nil, err
	}
	return connection.conn.ExecContext(connection.ctx, query, args...)
}

func (connection aiRunConnection) Query(query string, args ...any) (*sql.Rows, error) {
	if err := connection.boundLockWait(); err != nil {
		return nil, err
	}
	return connection.conn.QueryContext(connection.ctx, query, args...)
}

func (connection aiRunConnection) QueryRow(query string, args ...any) *sql.Row {
	_ = connection.boundLockWait()
	return connection.conn.QueryRowContext(connection.ctx, query, args...)
}

func withAIRunDatabase(databasePath string, deadline time.Time, use func(aiRunConnection) error) error {
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	db, err := aiRunOpenDatabase(databasePath, min(aiRunLockWaitSlice, time.Until(deadline)))
	if err != nil {
		return fmt.Errorf("open AI event database: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open AI event database: %w", err)
	}
	defer conn.Close()
	if aiRunAfterConnect != nil {
		aiRunAfterConnect()
	}
	return use(aiRunConnection{ctx: ctx, conn: conn, deadline: deadline})
}

func retryAIRunDatabase(databasePath string, deadline time.Time, use func(aiRunConnection) error) error {
	delay := aiRunRetryInitialDelay
	for {
		err := withAIRunDatabase(databasePath, deadline, use)
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

func persistAIRunStart(databasePath string, use func(aiRunConnection) error, exit aiRunChildExit) aiRunStartResult {
	delay := aiRunRetryInitialDelay
	var firstUnavailable error
	for {
		deadline := time.Now().Add(aiRunRetryBudget)
		if exit.exited() {
			deadline = exit.at.Add(aiRunRetryBudget)
		}
		err := withAIRunDatabase(databasePath, deadline, use)
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
