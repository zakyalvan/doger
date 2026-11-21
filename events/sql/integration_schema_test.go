package eventsql_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	eventsql "github.com/kartaladev/doger/events/sql"
)

// seedPending inserts n pending deliveries directly.
func seedPending(t *testing.T, db *testDB, n int) {
	t.Helper()
	for i := range n {
		db.Exec(t, "INSERT INTO doger_event_deliveries (event_key, destination, group_name, state, attempts, visible_after, created_at) VALUES (?, 'orders.placed', 'mail', 'pending', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)",
			"k"+string(rune('a'+i)))
	}
}

func TestEnsureSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(t *testing.T, db *testDB, tr *eventsql.Transport)
		call    func(tr *eventsql.Transport, ctx context.Context) error
		cancel  bool
		wantErr error
		naming  []string // tables the error names
		after   func(t *testing.T, db *testDB, tr *eventsql.Transport)
	}{
		{
			name:    "check_empty",
			call:    (*eventsql.Transport).CheckSchema,
			wantErr: eventsql.ErrSchemaMissing,
			naming:  []string{"doger_events", "doger_event_deliveries", "doger_event_groups"},
		},
		{
			name: "ensure_empty",
			call: (*eventsql.Transport).EnsureSchema,
			after: func(t *testing.T, _ *testDB, tr *eventsql.Transport) {
				require.NoError(t, tr.CheckSchema(context.Background()))
			},
		},
		{
			name: "ensure_existing_keeps_rows",
			setup: func(t *testing.T, db *testDB, tr *eventsql.Transport) {
				require.NoError(t, tr.EnsureSchema(context.Background()))
				seedPending(t, db, 3)
			},
			call: (*eventsql.Transport).EnsureSchema,
			after: func(t *testing.T, db *testDB, _ *eventsql.Transport) {
				assert.Equal(t, 3, db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries WHERE state = 'pending'"))
			},
		},
		{
			name: "check_partial",
			setup: func(t *testing.T, db *testDB, _ *eventsql.Transport) {
				db.Exec(t, "CREATE TABLE doger_events (id INT)")
			},
			call:    (*eventsql.Transport).CheckSchema,
			wantErr: eventsql.ErrSchemaMissing,
			naming:  []string{"doger_event_deliveries", "doger_event_groups"},
		},
		{
			name:    "ensure_cancelled",
			call:    (*eventsql.Transport).EnsureSchema,
			cancel:  true,
			wantErr: context.Canceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			forEachEngine(t, func(t *testing.T, db *testDB) {
				tr := db.Transport(t)
				if tt.setup != nil {
					tt.setup(t, db, tr)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if tt.cancel {
					cancel()
				}

				err := tt.call(tr, ctx)

				if tt.wantErr != nil {
					require.ErrorIs(t, err, tt.wantErr)
					for _, table := range tt.naming {
						assert.Contains(t, err.Error(), table)
					}
					if tt.naming != nil && len(tt.naming) < 3 {
						assert.NotContains(t, err.Error(), "doger_events")
					}
					return
				}
				require.NoError(t, err)
				if tt.after != nil {
					tt.after(t, db, tr)
				}
			})
		})
	}
}

func TestEnsureSchema_Concurrent(t *testing.T) {
	t.Parallel()
	forEachEngine(t, func(t *testing.T, db *testDB) {
		const n = 5
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := range n {
			tr := db.Transport(t)
			wg.Go(func() { errs[i] = tr.EnsureSchema(context.Background()) })
		}
		wg.Wait()

		for i, err := range errs {
			require.NoError(t, err, "transport %d", i)
		}
		require.NoError(t, db.Transport(t).CheckSchema(context.Background()))
	})
}
