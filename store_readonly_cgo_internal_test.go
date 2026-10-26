//go:build cgo

package workgraph

import (
	"errors"
	"fmt"
	"testing"

	"github.com/mattn/go-sqlite3"
)

func TestIsDatabaseReadOnlyAcceptsOnlyThePlainReadOnlyCode(t *testing.T) {
	readOnly := sqlite3.Error{Code: sqlite3.ErrReadonly, ExtendedCode: sqlite3.ErrNoExtended(sqlite3.ErrReadonly)}
	if !IsDatabaseReadOnly(readOnly) || !IsDatabaseReadOnly(fmt.Errorf("store project link for unit: %w", readOnly)) {
		t.Fatal("expected a plain read-only error, bare or wrapped, to be read-only")
	}
	for _, extended := range []sqlite3.ErrNoExtended{sqlite3.ErrReadonlyDbMoved, sqlite3.ErrReadonlyRecovery, sqlite3.ErrReadonlyCantLock, sqlite3.ErrReadonlyRollback} {
		err := fmt.Errorf("wrapped: %w", sqlite3.Error{Code: sqlite3.ErrReadonly, ExtendedCode: extended})
		if IsDatabaseReadOnly(err) {
			t.Fatalf("expected extended code %d to stay a real error", extended)
		}
	}
	for _, err := range []error{nil, errors.New("attempt to write a readonly database"), sqlite3.Error{Code: sqlite3.ErrBusy, ExtendedCode: sqlite3.ErrNoExtended(sqlite3.ErrBusy)}} {
		if IsDatabaseReadOnly(err) {
			t.Fatalf("expected %v not to be read-only", err)
		}
	}
}
