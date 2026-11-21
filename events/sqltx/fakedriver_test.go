package sqltx_test

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

// Errors the fake driver can be told to return.
var (
	ErrBeginFailed  = errors.New("fake: begin failed")
	ErrCommitFailed = errors.New("fake: commit failed")
)

// recorder records what the fake driver was asked to do, and holds the
// failures it injects.
type recorder struct {
	mu         sync.Mutex
	events     []string                // "begin", "commit", "rollback"
	isolations []driver.IsolationLevel // of every begin

	failBegin  error
	failCommit error
}

func (r *recorder) record(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

// Events returns the recorded events.
func (r *recorder) Events() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

// Isolations returns the isolation level of every begin.
func (r *recorder) Isolations() []driver.IsolationLevel {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.isolations)
}

// newFakeDB returns a *sql.DB on the recording fake driver, closed at the
// end of the test.
func newFakeDB(t *testing.T) (*sql.DB, *recorder) {
	t.Helper()
	db, rec := openFakeDB()
	t.Cleanup(func() { _ = db.Close() })

	return db, rec
}

// openFakeDB returns a *sql.DB on the recording fake driver.
func openFakeDB() (*sql.DB, *recorder) {
	rec := &recorder{}

	return sql.OpenDB(fakeConnector{rec: rec}), rec
}

type fakeConnector struct{ rec *recorder }

func (c fakeConnector) Connect(context.Context) (driver.Conn, error) {
	return &fakeConn{rec: c.rec}, nil
}
func (c fakeConnector) Driver() driver.Driver { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("fake: use the connector")
}

type fakeConn struct{ rec *recorder }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return fakeStmt{}, nil }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *fakeConn) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.rec.mu.Lock()
	failure := c.rec.failBegin
	c.rec.isolations = append(c.rec.isolations, opts.Isolation)
	c.rec.mu.Unlock()
	if failure != nil {
		return nil, failure
	}
	c.rec.record("begin")

	return fakeTx{rec: c.rec}, nil
}

type fakeTx struct{ rec *recorder }

func (tx fakeTx) Commit() error {
	tx.rec.mu.Lock()
	failure := tx.rec.failCommit
	tx.rec.mu.Unlock()
	tx.rec.record("commit")

	return failure
}

func (tx fakeTx) Rollback() error {
	tx.rec.record("rollback")
	return nil
}

type fakeStmt struct{}

func (fakeStmt) Close() error                               { return nil }
func (fakeStmt) NumInput() int                              { return -1 }
func (fakeStmt) Exec([]driver.Value) (driver.Result, error) { return driver.RowsAffected(0), nil }
func (fakeStmt) Query([]driver.Value) (driver.Rows, error)  { return fakeRows{}, nil }

type fakeRows struct{}

func (fakeRows) Columns() []string         { return nil }
func (fakeRows) Close() error              { return nil }
func (fakeRows) Next([]driver.Value) error { return io.EOF }
