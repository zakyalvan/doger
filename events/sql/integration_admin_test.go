package eventsql_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/doger"
	eventsql "github.com/kartaladev/doger/events/sql"
)

// seedDeadLetters registers "mail" on dest with dead letters d1 and d2 and
// one pending delivery p1.
func seedDeadLetters(t *testing.T, db *testDB, tr *eventsql.Transport, dest string) {
	t.Helper()
	seedGroup(t, db, dest, "mail", false)
	for _, id := range []string{"d1", "d2", "p1"} {
		send(t, tr, id, dest)
	}
	for i, id := range []string{"d1", "d2"} {
		db.Exec(t, "UPDATE doger_event_deliveries SET state = 'dead', attempts = ?, last_error = ? WHERE event_key IN (SELECT event_key FROM doger_events WHERE id = ?)",
			i+3, "boom "+id, id)
	}
}

func TestAdmin(t *testing.T) {
	t.Parallel()

	const dest = "orders.placed"
	tests := []struct {
		name string
		run  func(t *testing.T, db *testDB, tr *eventsql.Transport)
	}{
		{name: "list", run: func(t *testing.T, _ *testDB, tr *eventsql.Transport) {
			got, err := tr.ListDeadLetters(context.Background(), dest, "mail", 10)
			require.NoError(t, err)
			require.Len(t, got, 2)
			assert.Equal(t, "d1", got[0].Envelope.ID)
			assert.Equal(t, 3, got[0].Attempts)
			assert.Equal(t, "boom d1", got[0].LastError)
			assert.Equal(t, "mail", got[0].Group)
			assert.Equal(t, dest, got[0].Envelope.Destination)
			assert.JSONEq(t, `{"id":"d1"}`, string(got[0].Envelope.Payload))
			assert.Equal(t, "d2", got[1].Envelope.ID)
			assert.Equal(t, 4, got[1].Attempts)
			assert.Equal(t, "boom d2", got[1].LastError)
		}},
		{name: "list_limit", run: func(t *testing.T, _ *testDB, tr *eventsql.Transport) {
			got, err := tr.ListDeadLetters(context.Background(), dest, "mail", 1)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, "d1", got[0].Envelope.ID)
		}},
		{name: "list_other_group", run: func(t *testing.T, _ *testDB, tr *eventsql.Transport) {
			got, err := tr.ListDeadLetters(context.Background(), dest, "other", 10)
			require.NoError(t, err)
			assert.Empty(t, got)
		}},
		{name: "list_limit_0", run: func(t *testing.T, _ *testDB, tr *eventsql.Transport) {
			_, err := tr.ListDeadLetters(context.Background(), dest, "mail", 0)
			require.ErrorIs(t, err, eventsql.ErrInvalidOption)
		}},
		{name: "resubmit", run: func(t *testing.T, db *testDB, tr *eventsql.Transport) {
			dead, err := tr.ListDeadLetters(context.Background(), dest, "mail", 10)
			require.NoError(t, err)
			require.NoError(t, tr.Resubmit(context.Background(), dead[0].ID))

			got := &attempts{}
			c := newCollector()
			subscribe(t, tr, group(dest, "mail"), func(ctx context.Context, env doger.Envelope) error {
				if env.ID == "d1" {
					got.record(ctx)
				}
				return c.handle(ctx, env)
			})
			assert.ElementsMatch(t, []string{"d1", "p1"}, c.expect(t, 2))
			c.quiet(t)
			assert.Equal(t, []int{1}, got.Got())
			left, err := tr.ListDeadLetters(context.Background(), dest, "mail", 10)
			require.NoError(t, err)
			require.Len(t, left, 1)
			assert.Equal(t, "d2", left[0].Envelope.ID)
		}},
		{name: "resubmit_missing", run: func(t *testing.T, db *testDB, tr *eventsql.Transport) {
			require.NoError(t, tr.Resubmit(context.Background(), "missing"))
			assert.Equal(t, 2, db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries WHERE state = 'dead'"))
			assert.Equal(t, 1, db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries WHERE state = 'pending'"))
		}},
		{name: "drop_group", run: func(t *testing.T, db *testDB, tr *eventsql.Transport) {
			require.NoError(t, tr.DropGroup(context.Background(), dest, "mail"))
			assert.Zero(t, db.Count(t, "SELECT COUNT(*) FROM doger_event_groups"))
			assert.Zero(t, db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries"))
			send(t, tr, "later", dest)
			assert.Zero(t, db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries"))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			forEachEngine(t, func(t *testing.T, db *testDB) {
				testWithTimeout(t, time.Minute)
				tr := schemaTransport(t, db, fast()...)
				seedDeadLetters(t, db, tr, dest)
				tt.run(t, db, tr)
			})
		})
	}
}
