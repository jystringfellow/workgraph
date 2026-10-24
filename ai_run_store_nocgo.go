//go:build !cgo

package workgraph

import (
	"context"
	"errors"
)

func isAIRunDatabaseUnavailable(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}
