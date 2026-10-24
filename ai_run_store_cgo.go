//go:build cgo

package workgraph

import (
	"context"
	"errors"

	"github.com/mattn/go-sqlite3"
)

func isAIRunDatabaseUnavailable(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var sqliteErr sqlite3.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	switch sqliteErr.Code {
	case sqlite3.ErrBusy, sqlite3.ErrLocked, sqlite3.ErrCantOpen, sqlite3.ErrInterrupt:
		return true
	}
	return false
}
