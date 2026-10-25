//go:build cgo

package workgraph

import (
	"errors"

	"github.com/mattn/go-sqlite3"
)

func IsDatabaseReadOnly(err error) bool {
	var sqliteErr sqlite3.Error
	return errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrNoExtended(sqlite3.ErrReadonly)
}
