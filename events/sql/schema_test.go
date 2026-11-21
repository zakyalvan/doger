package eventsql_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	eventsql "github.com/kartaladev/doger/events/sql"
	"github.com/kartaladev/doger/events/sql/postgres"
)

// TestEnsureSchema_Unlock is a regression test: when releasing the schema
// lock failed, the connection went back to the pool still holding the
// session lock, blocking every later EnsureSchema. It is discarded.
func TestEnsureSchema_Unlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		unlock      func() (driver.Rows, error)
		wantDiscard bool
	}{
		{name: "released", unlock: func() (driver.Rows, error) { return &fakeRows{values: []driver.Value{int64(1)}}, nil }},
		{name: "unlock_fails", unlock: func() (driver.Rows, error) { return nil, errors.New("connection reset") }, wantDiscard: true},
		{name: "not_released", unlock: func() (driver.Rows, error) { return &fakeRows{values: []driver.Value{int64(0)}}, nil }, wantDiscard: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db, rec := newFakeDB(t)
			_, unlockSQL := postgres.Dialect().SchemaLockSQL()
			rec.query = func(q string) (driver.Rows, error) {
				switch {
				case q == unlockSQL:
					return tt.unlock()
				case strings.Contains(q, "pg_advisory_lock"):
					return &fakeRows{values: []driver.Value{int64(1)}}, nil
				}
				return nil, nil
			}
			tr, err := eventsql.New(db, postgres.Dialect())
			require.NoError(t, err)

			require.NoError(t, tr.EnsureSchema(context.Background()))

			if tt.wantDiscard {
				assert.Equal(t, 1, rec.Closes(), "the connection holding the lock was not discarded")
				assert.Zero(t, db.Stats().Idle)
			} else {
				assert.Zero(t, rec.Closes())
				assert.Equal(t, 1, db.Stats().Idle)
			}
		})
	}
}
