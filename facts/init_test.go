package facts

import "testing"

// First implementation slice: replace the home and database placeholders with
// executable assertions, verify they fail, then implement only enough init
// behavior to pass them before expanding the slice.

func TestInitCreatesWorkGraphHome(t *testing.T) {
	t.Skip("TBD: workgraph init creates ~/.workgraph")
}

func TestInitCreatesSQLiteDatabase(t *testing.T) {
	t.Skip("TBD: workgraph init creates ~/.workgraph/workgraph.db")
}

func TestInitCreatesMemoryRepo(t *testing.T) {
	t.Skip("TBD: workgraph init creates ~/workgraph-memory")
}

func TestInitIsIdempotent(t *testing.T) {
	t.Skip("TBD: workgraph init preserves existing events and memory files")
}

func TestInitReportsInitializedPaths(t *testing.T) {
	t.Skip("TBD: workgraph init reports home, database, and memory paths")
}
