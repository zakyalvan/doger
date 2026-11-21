package eventsql_test

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/doger"
	eventsql "github.com/kartaladev/doger/events/sql"
	"github.com/kartaladev/doger/events/sql/postgres"
	"github.com/kartaladev/doger/events/sqltx"
)

// txHarness drives sqltx transactions on db for the conformance suite.
type txHarness struct{ db *sql.DB }

func (h txHarness) Begin(ctx context.Context) (context.Context, error) {
	txCtx, _, err := sqltx.BeginTx(ctx, h.db, nil)
	return txCtx, err
}

func (h txHarness) Commit(ctx context.Context) error {
	tx, ok := sqltx.From(ctx)
	if !ok {
		return errors.New("no transaction")
	}
	return tx.Commit()
}

func (h txHarness) Rollback(ctx context.Context) error {
	tx, ok := sqltx.From(ctx)
	if !ok {
		return errors.New("no transaction")
	}
	return tx.Rollback()
}

func TestSendTx(t *testing.T) {
	t.Parallel()

	const dest = "orders.placed"
	tests := []struct {
		name        string
		ctx         func(t *testing.T, db1, db2 *sql.DB) (ctx context.Context, end func() error)
		wantErr     error
		wantHandled bool
	}{
		{name: "commit", wantHandled: true, ctx: func(t *testing.T, db1, _ *sql.DB) (context.Context, func() error) {
			ctx, tx, err := sqltx.BeginTx(context.Background(), db1, nil)
			require.NoError(t, err)
			return ctx, tx.Commit
		}},
		{name: "rollback", ctx: func(t *testing.T, db1, _ *sql.DB) (context.Context, func() error) {
			ctx, tx, err := sqltx.BeginTx(context.Background(), db1, nil)
			require.NoError(t, err)
			return ctx, tx.Rollback
		}},
		{name: "no_transaction", wantErr: doger.ErrNoTransaction, ctx: func(*testing.T, *sql.DB, *sql.DB) (context.Context, func() error) {
			return context.Background(), nil
		}},
		{name: "committed_transaction", wantErr: doger.ErrNoTransaction, ctx: func(t *testing.T, db1, _ *sql.DB) (context.Context, func() error) {
			ctx, tx, err := sqltx.BeginTx(context.Background(), db1, nil)
			require.NoError(t, err)
			require.NoError(t, tx.Commit())
			return ctx, nil
		}},
		{name: "foreign_transaction", wantErr: sqltx.ErrForeignTransaction, ctx: func(t *testing.T, _, db2 *sql.DB) (context.Context, func() error) {
			ctx, tx, err := sqltx.BeginTx(context.Background(), db2, nil)
			require.NoError(t, err)
			return ctx, tx.Commit
		}},
		{name: "bound_transaction", wantHandled: true, ctx: func(t *testing.T, db1, _ *sql.DB) (context.Context, func() error) {
			raw, err := db1.BeginTx(context.Background(), nil)
			require.NoError(t, err)
			ctx, tx := sqltx.BindTx(context.Background(), db1, raw)
			return ctx, tx.Commit
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			forEachEngine(t, func(t *testing.T, db *testDB) {
				testWithTimeout(t, time.Minute)
				a := schemaTransport(t, db, fast()...)
				db2, err := sql.Open(db.driver, scopedDSN(t, db.engine, db.schema))
				require.NoError(t, err)
				t.Cleanup(func() { _ = db2.Close() })
				c := newCollector()
				subscribe(t, a, group(dest, "mail"), c.handle)
				ctx, end := tt.ctx(t, db.DB, db2)

				err = a.SendTx(ctx, envelope("e1", dest))
				if end != nil {
					require.NoError(t, end())
				}

				if tt.wantErr != nil {
					require.ErrorIs(t, err, tt.wantErr)
				} else {
					require.NoError(t, err)
				}
				if tt.wantHandled {
					assert.Equal(t, []string{"e1"}, c.expect(t, 1))
				}
				c.quiet(t)
			})
		})
	}
}

// OrderPlaced is the event of the doger outbox tests.
type OrderPlaced struct{ ID string }

func TestOutbox_EndToEnd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		fnErr     error
		wantMail  int
		wantAfter int32
	}{
		{name: "commit", wantMail: 1, wantAfter: 1},
		{name: "rollback", fnErr: errOutOfStock},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			forEachEngine(t, func(t *testing.T, db *testDB) {
				testWithTimeout(t, time.Minute)
				a := schemaTransport(t, db, fast()...)

				rootA := doger.NewWithOpts(&doger.InjectorOpts{EventTransport: a})
				t.Cleanup(func() { rootA.Shutdown() })
				require.NoError(t, doger.RegisterEventType[OrderPlaced](rootA, "orders.placed", doger.EventExternalized()))
				var after atomic.Int32
				doger.Listen(rootA, func(context.Context, OrderPlaced) error { after.Add(1); return nil },
					doger.ListenerTxPhase(doger.TxPhaseAfterCommit))
				require.NoError(t, rootA.Start())

				rootB := doger.NewWithOpts(&doger.InjectorOpts{EventTransport: db.Transport(t, fast()...)})
				t.Cleanup(func() { rootB.Shutdown() })
				require.NoError(t, doger.RegisterEventType[OrderPlaced](rootB, "orders.placed"))
				mail := make(chan OrderPlaced, 4)
				doger.Listen(rootB, func(_ context.Context, e OrderPlaced) error { mail <- e; return nil },
					doger.ListenerID("mail"), doger.ListenerDelivery(doger.DeliverOnce))
				require.NoError(t, rootB.Start())

				var afterDuringFn int32
				err := sqltx.InTx(context.Background(), db.DB, func(ctx context.Context) error {
					if err := doger.Publish(ctx, rootA, OrderPlaced{ID: "1"}); err != nil {
						return err
					}
					afterDuringFn = after.Load()
					return tt.fnErr
				})
				require.ErrorIs(t, err, tt.fnErr)

				assert.Zero(t, afterDuringFn, "the after-commit listener ran before the commit")
				assert.Equal(t, tt.wantAfter, after.Load())
				for range tt.wantMail {
					select {
					case got := <-mail:
						assert.Equal(t, OrderPlaced{ID: "1"}, got)
					case <-time.After(waitTimeout):
						t.Fatal("mail did not handle OrderPlaced")
					}
				}
				select {
				case got := <-mail:
					t.Errorf("unexpected %v", got)
				case <-time.After(quietPeriod):
				}
			})
		})
	}
}

func TestTxSource_SameSourceTwice(t *testing.T) {
	t.Parallel()
	db, _ := newFakeDB(t)
	tr, err := eventsql.New(db, postgres.Dialect())
	require.NoError(t, err)
	root := doger.NewWithOpts(&doger.InjectorOpts{TxSource: sqltx.Source(), EventTransport: tr})
	t.Cleanup(func() { root.Shutdown() })

	require.NoError(t, root.Start())
	assert.Equal(t, sqltx.Source(), tr.TxSource())
}

// TestGroups_RegistrationRace runs on PostgreSQL only: on MySQL the
// fan-out's shared next-key locks make the registration wait for T1's
// commit, so the scenario's order (register, then commit T1) cannot occur;
// the outcome (no e1 for the group) is the same.
func TestGroups_RegistrationRace(t *testing.T) {
	t.Parallel()
	forEachEngine(t, func(t *testing.T, db *testDB) {
		testWithTimeout(t, time.Minute)
		const dest = "orders.placed"
		tr := schemaTransport(t, db, fast()...)
		ctx1, t1, err := sqltx.BeginTx(context.Background(), db.DB, nil)
		require.NoError(t, err)
		require.NoError(t, tr.SendTx(ctx1, envelope("e1", dest)))

		c := newCollector()
		subscribe(t, tr, group(dest, "mail"), c.handle) // registers and commits
		require.NoError(t, t1.Commit())
		send(t, tr, "e2", dest)

		assert.Equal(t, []string{"e2"}, c.expect(t, 1))
		c.quiet(t)
	}, "postgres")
}
