package workgraph

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

type lockTrackingDriver struct{}

type lockTrackingConn struct {
	mu       sync.Mutex
	openRead bool
}

type lockTrackingRows struct {
	conn *lockTrackingConn
	seen bool
}

func (d lockTrackingDriver) Open(name string) (driver.Conn, error) {
	return &lockTrackingConn{}, nil
}

func (c *lockTrackingConn) Prepare(query string) (driver.Stmt, error) {
	return nil, errors.New("prepare unsupported")
}

func (c *lockTrackingConn) Close() error { return nil }

func (c *lockTrackingConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions unsupported")
}

func (c *lockTrackingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if !strings.HasPrefix(query, "PRAGMA table_info(") {
		return nil, fmt.Errorf("unsupported query: %s", query)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.openRead = true
	return &lockTrackingRows{conn: c}, nil
}

func (c *lockTrackingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if strings.HasPrefix(query, "ALTER TABLE ") {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.openRead {
			return nil, fmt.Errorf("database is locked")
		}
	}
	return driver.RowsAffected(0), nil
}

func (r *lockTrackingRows) Columns() []string {
	return []string{"cid", "name", "type", "notnull", "dflt_value", "pk"}
}

func (r *lockTrackingRows) Close() error {
	r.conn.mu.Lock()
	defer r.conn.mu.Unlock()
	r.conn.openRead = false
	return nil
}

func (r *lockTrackingRows) Next(dest []driver.Value) error {
	if r.seen {
		return io.EOF
	}
	r.seen = true
	dest[0] = int64(0)
	dest[1] = "other_column"
	dest[2] = "TEXT"
	dest[3] = int64(0)
	dest[4] = nil
	dest[5] = int64(0)
	return nil
}

func TestEnsureColumnClosesPRAGMAReadBeforeAlter(t *testing.T) {
	sql.Register("lock-tracking", lockTrackingDriver{})
	db, err := sql.Open("lock-tracking", "test")
	if err != nil {
		t.Fatalf("open fake DB: %v", err)
	}
	defer db.Close()

	if err := ensureColumn(db, "events", "involvement_json", "TEXT CHECK (involvement_json IS NULL OR json_valid(involvement_json))"); err != nil {
		t.Fatalf("ensureColumn should close PRAGMA rows before ALTER TABLE: %v", err)
	}
}
