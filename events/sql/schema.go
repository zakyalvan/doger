package eventsql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
)

// ErrSchemaMissing is matched by the error of CheckSchema when a table of
// the transport does not exist; the message names the missing tables.
var ErrSchemaMissing = errors.New("doger: eventsql: schema missing")

// EnsureSchema creates the tables and indexes of the transport that do not
// exist, leaving existing ones and their rows unchanged. Concurrent
// callers are serialized with a database lock (pg_advisory_lock,
// GET_LOCK). It is for development and tests: in production, apply the
// dialect's DDL with your migration tool. The transport never calls it.
func (t *Transport) EnsureSchema(ctx context.Context) error {
	conn, err := t.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("doger: eventsql: ensure schema: %w", err)
	}
	defer func() { _ = conn.Close() }()

	lock, unlock := t.dialect.SchemaLockSQL()
	name := "doger_schema:" + t.cfg.tables.Prefix
	var held int
	if err := conn.QueryRowContext(ctx, lock, name).Scan(&held); err != nil {
		return fmt.Errorf("doger: eventsql: ensure schema: lock: %w", err)
	}
	if held != 1 {
		return fmt.Errorf("doger: eventsql: ensure schema: lock %q not acquired", name)
	}
	defer unlockSession(ctx, conn, unlock, name)

	for _, stmt := range t.dialect.DDL(t.cfg.tables) {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("doger: eventsql: ensure schema: %w", err)
		}
	}

	return nil
}

// unlockSession releases the session lock name of conn, even when ctx
// ended. When the release fails the connection is discarded instead of
// going back to the pool, since its session would keep the lock.
func unlockSession(ctx context.Context, conn *sql.Conn, unlock, name string) {
	var released int
	err := conn.QueryRowContext(context.WithoutCancel(ctx), unlock, name).Scan(&released)
	if err == nil && released == 1 {
		return
	}
	// driver.ErrBadConn makes database/sql close the connection
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}

// CheckSchema returns nil when the three tables of the transport exist,
// and an error matching ErrSchemaMissing naming the missing ones
// otherwise. It looks in information_schema only.
func (t *Transport) CheckSchema(ctx context.Context) error {
	var missing []string
	for _, table := range []string{t.cfg.tables.Events, t.cfg.tables.Deliveries, t.cfg.tables.Groups} {
		schema, name := splitTable(table)
		var n int
		err := t.db.QueryRowContext(ctx, t.stmts.TableExists, schema, name).Scan(&n)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("doger: eventsql: check schema: %w", err)
		}
		if n == 0 {
			missing = append(missing, table)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: tables %s do not exist", ErrSchemaMissing, strings.Join(missing, ", "))
	}

	return nil
}

// splitTable splits a possibly schema-qualified table name.
func splitTable(table string) (schema, name string) {
	if schema, name, ok := strings.CutLast(table, "."); ok {
		return schema, name
	}
	return "", table
}
