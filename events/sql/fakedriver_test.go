package eventsql_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
)

// fakeRecorder records the connection attempts and statements of the
// fake driver, for unit tests that need no engine.
type fakeRecorder struct {
	mu         sync.Mutex
	connects   int
	closes     int
	statements []string
	// query, when set, answers a statement instead of the default (no
	// row, no error): a nil driver.Rows means none.
	query func(query string) (driver.Rows, error)
}

// Closes returns the number of closed connections.
func (r *fakeRecorder) Closes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closes
}

// Connects returns the number of connection attempts.
func (r *fakeRecorder) Connects() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.connects
}

// Statements returns the prepared or executed statements.
func (r *fakeRecorder) Statements() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.statements)
}

// newFakeDB returns a *sql.DB on the fake driver, closed at the end of the
// test: every statement succeeds, affecting no row and returning none.
func newFakeDB(t *testing.T) (*sql.DB, *fakeRecorder) {
	t.Helper()
	rec := &fakeRecorder{}
	db := sql.OpenDB(fakeConnector{rec: rec})
	t.Cleanup(func() { _ = db.Close() })

	return db, rec
}

type fakeConnector struct{ rec *fakeRecorder }

func (c fakeConnector) Connect(context.Context) (driver.Conn, error) {
	c.rec.mu.Lock()
	defer c.rec.mu.Unlock()
	c.rec.connects++

	return &fakeConn{rec: c.rec}, nil
}

func (c fakeConnector) Driver() driver.Driver { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("fake: use the connector")
}

type fakeConn struct{ rec *fakeRecorder }

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	c.rec.mu.Lock()
	defer c.rec.mu.Unlock()
	c.rec.statements = append(c.rec.statements, query)

	return fakeStmt{rec: c.rec, query: query}, nil
}

func (c *fakeConn) Close() error {
	c.rec.mu.Lock()
	defer c.rec.mu.Unlock()
	c.rec.closes++
	return nil
}

func (c *fakeConn) Begin() (driver.Tx, error) { return fakeTx{}, nil }

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeStmt struct {
	rec   *fakeRecorder
	query string
}

func (fakeStmt) Close() error  { return nil }
func (fakeStmt) NumInput() int { return -1 }

func (s fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	if _, err := s.answer(); err != nil {
		return nil, err
	}
	return driver.RowsAffected(0), nil
}

func (s fakeStmt) Query([]driver.Value) (driver.Rows, error) {
	rows, err := s.answer()
	if err != nil || rows != nil {
		return rows, err
	}
	return &fakeRows{}, nil
}

// answer runs the recorder's query hook, if any.
func (s fakeStmt) answer() (driver.Rows, error) {
	s.rec.mu.Lock()
	query := s.rec.query
	s.rec.mu.Unlock()
	if query == nil {
		return nil, nil
	}
	return query(s.query)
}

// fakeRows returns its rows, each one value, in a single column.
type fakeRows struct{ values []driver.Value }

func (*fakeRows) Columns() []string { return []string{"v"} }
func (*fakeRows) Close() error      { return nil }

func (r *fakeRows) Next(dest []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	dest[0], r.values = r.values[0], r.values[1:]
	return nil
}

// TestFakeDriver pins the fake driver's recording, which the unit tests
// rely on: opening the *sql.DB connects nothing, a statement does.
func TestFakeDriver(t *testing.T) {
	t.Parallel()
	db, rec := newFakeDB(t)
	if rec.Connects() != 0 {
		t.Fatalf("connects after open: %d, want 0", rec.Connects())
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if rec.Connects() != 1 || !slices.Equal(rec.Statements(), []string{"SELECT 1"}) {
		t.Fatalf("connects %d, statements %v", rec.Connects(), rec.Statements())
	}
}
