//go:build cgo

package workgraph

import (
	"errors"

	"github.com/mattn/go-sqlite3"
)

func isAIRunDatabaseUnavailable(err error) bool {
	var sqliteErr sqlite3.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	switch sqliteErr.Code {
	case sqlite3.ErrBusy, sqlite3.ErrLocked, sqlite3.ErrCantOpen:
		return true
	}
	return false
}
