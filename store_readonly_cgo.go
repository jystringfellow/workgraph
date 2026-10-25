//go:build cgo

package workgraph

import (
	"errors"

	"github.com/mattn/go-sqlite3"
)

func IsDatabaseReadOnly(err error) bool {
	var sqliteErr sqlite3.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code == sqlite3.ErrReadonly
}
