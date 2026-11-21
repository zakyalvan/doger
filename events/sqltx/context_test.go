package sqltx_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/doger"
	"github.com/kartaladev/doger/events/sqltx"
)

type traceKey struct{}

func TestFromAndSource(t *testing.T) {
	t.Parallel()

	// fixture: ctx1 holds tx1, begun on a cancellable parent carrying a
	// trace value
	type fixture struct {
		parentCancel context.CancelFunc
		ctx1         context.Context
		tx1          *sqltx.Tx
	}
	setup := func(t *testing.T) fixture {
		t.Helper()
		db, _ := newFakeDB(t)
		parent, cancel := context.WithCancel(context.WithValue(context.Background(), traceKey{}, "t1"))
		t.Cleanup(cancel)
		ctx1, tx1, err := sqltx.BeginTx(parent, db, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = tx1.Rollback() })

		return fixture{parentCancel: cancel, ctx1: ctx1, tx1: tx1}
	}

	tests := []struct {
		name string
		eval func(t *testing.T, f fixture) (got any, want any)
	}{
		{name: "from_before_end", eval: func(t *testing.T, f fixture) (any, any) {
			tx, ok := sqltx.From(f.ctx1)
			return pair(tx, ok), pair(f.tx1, true)
		}},
		{name: "from_after_commit", eval: func(t *testing.T, f fixture) (any, any) {
			require.NoError(t, f.tx1.Commit())
			tx, ok := sqltx.From(f.ctx1)
			return pair(tx, ok), pair(nil, false)
		}},
		{name: "from_after_rollback", eval: func(t *testing.T, f fixture) (any, any) {
			require.NoError(t, f.tx1.Rollback())
			tx, ok := sqltx.From(f.ctx1)
			return pair(tx, ok), pair(nil, false)
		}},
		{name: "from_background", eval: func(t *testing.T, f fixture) (any, any) {
			tx, ok := sqltx.From(context.Background())
			return pair(tx, ok), pair(nil, false)
		}},
		{name: "from_nil", eval: func(t *testing.T, f fixture) (any, any) {
			var nilCtx context.Context
			tx, ok := sqltx.From(nilCtx)
			return pair(tx, ok), pair(nil, false)
		}},
		{name: "lookup_before_end", eval: func(t *testing.T, f fixture) (any, any) {
			tx, ok := sqltx.Source().Lookup(f.ctx1)
			return []any{tx, ok}, []any{doger.Tx(f.tx1), true}
		}},
		{name: "lookup_detached", eval: func(t *testing.T, f fixture) (any, any) {
			tx, ok := sqltx.Source().Lookup(sqltx.Source().Detach(f.ctx1))
			return []any{tx, ok}, []any{nil, false}
		}},
		{name: "detach_keeps_values", eval: func(t *testing.T, f fixture) (any, any) {
			return sqltx.Source().Detach(f.ctx1).Value(traceKey{}), "t1"
		}},
		{name: "detach_not_cancelled", eval: func(t *testing.T, f fixture) (any, any) {
			detached := sqltx.Source().Detach(f.ctx1)
			f.parentCancel()
			return detached.Err(), nil
		}},
		{name: "nested_begin", eval: func(t *testing.T, f fixture) (any, any) {
			db, _ := newFakeDB(t)
			ctx2, tx2, err := sqltx.BeginTx(f.ctx1, db, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = tx2.Rollback() })
			tx, ok := sqltx.From(ctx2)
			return pair(tx, ok), pair(tx2, true)
		}},
		{name: "source_comparable", eval: func(t *testing.T, f fixture) (any, any) {
			return sqltx.Source() == sqltx.Source(), true
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, want := tt.eval(t, setup(t))
			assert.Equal(t, want, got)
		})
	}
}

// pair returns the results of From as a comparable slice, a nil *Tx as nil.
func pair(tx *sqltx.Tx, ok bool) []any {
	if tx == nil {
		return []any{nil, ok}
	}
	return []any{tx, ok}
}

func TestFromDB(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ctx     func(t *testing.T, db1, db2 *sql.DB) (context.Context, *sqltx.Tx)
		wantErr error
	}{
		{name: "begin_on_db1", ctx: func(t *testing.T, p, _ *sql.DB) (context.Context, *sqltx.Tx) {
			ctx, tx, err := sqltx.BeginTx(context.Background(), p, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = tx.Rollback() })
			return ctx, tx
		}},
		{name: "bind_on_db1", ctx: func(t *testing.T, p, _ *sql.DB) (context.Context, *sqltx.Tx) {
			raw, err := p.BeginTx(context.Background(), nil)
			require.NoError(t, err)
			ctx, tx := sqltx.BindTx(context.Background(), p, raw)
			t.Cleanup(func() { _ = tx.Rollback() })
			return ctx, tx
		}},
		{name: "background", ctx: func(*testing.T, *sql.DB, *sql.DB) (context.Context, *sqltx.Tx) {
			return context.Background(), nil
		}, wantErr: doger.ErrNoTransaction},
		{name: "nil", ctx: func(*testing.T, *sql.DB, *sql.DB) (context.Context, *sqltx.Tx) {
			return nil, nil
		}, wantErr: doger.ErrNoTransaction},
		{name: "committed", ctx: func(t *testing.T, p, _ *sql.DB) (context.Context, *sqltx.Tx) {
			ctx, tx, err := sqltx.BeginTx(context.Background(), p, nil)
			require.NoError(t, err)
			require.NoError(t, tx.Commit())
			return ctx, nil
		}, wantErr: doger.ErrNoTransaction},
		{name: "foreign", ctx: func(t *testing.T, _, p2 *sql.DB) (context.Context, *sqltx.Tx) {
			ctx, tx, err := sqltx.BeginTx(context.Background(), p2, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = tx.Rollback() })
			return ctx, nil
		}, wantErr: sqltx.ErrForeignTransaction},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db1, _ := newFakeDB(t)
			db2, _ := newFakeDB(t)
			ctx, want := tt.ctx(t, db1, db2)

			got, err := sqltx.FromDB(ctx, db1)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				if tt.wantErr == sqltx.ErrForeignTransaction { //nolint:errorlint // selects the row, not an error check
					assert.True(t, strings.HasPrefix(err.Error(), "doger: sqltx: "), err.Error())
				}
				return
			}
			require.NoError(t, err)
			assert.Same(t, want, got)
		})
	}
}
