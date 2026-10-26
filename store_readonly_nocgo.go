//go:build !cgo

package workgraph

func IsDatabaseReadOnly(err error) bool {
	return false
}
