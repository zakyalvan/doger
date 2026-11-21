package sqltx_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/doger"
	"github.com/kartaladev/doger/events/sqltx"
)

type OrderPlaced struct{ ID string }

func TestDeferredListeners(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		end   func(tx *sqltx.Tx) error
		wantA int32
		wantR int32
	}{
		{name: "commit", end: (*sqltx.Tx).Commit, wantA: 1},
		{name: "rollback", end: (*sqltx.Tx).Rollback, wantR: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := doger.NewWithOpts(&doger.InjectorOpts{TxSource: sqltx.Source()})
			t.Cleanup(func() { root.Shutdown() })
			var a, r atomic.Int32
			doger.Listen(root, func(context.Context, OrderPlaced) error { a.Add(1); return nil },
				doger.ListenerTxPhase(doger.TxPhaseAfterCommit))
			doger.Listen(root, func(context.Context, OrderPlaced) error { r.Add(1); return nil },
				doger.ListenerTxPhase(doger.TxPhaseAfterRollback))

			db, _ := newFakeDB(t)
			ctx1, tx, err := sqltx.BeginTx(context.Background(), db, nil)
			require.NoError(t, err)
			require.NoError(t, doger.Publish(ctx1, root, OrderPlaced{ID: "1"}))
			assert.Zero(t, a.Load()+r.Load(), "deferred listeners ran during Publish")

			require.NoError(t, tt.end(tx))

			assert.Equal(t, tt.wantA, a.Load())
			assert.Equal(t, tt.wantR, r.Load())
		})
	}
}
