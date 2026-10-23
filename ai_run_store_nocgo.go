//go:build !cgo

package workgraph

func isAIRunDatabaseUnavailable(error) bool {
	return false
}
