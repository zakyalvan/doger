package eventsql_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/doger"
	eventsql "github.com/kartaladev/doger/events/sql"
	"github.com/kartaladev/doger/events/sqltx"
)

func TestGroups_History(t *testing.T) {
	t.Parallel()

	const dest = "orders.placed"
	tests := []struct {
		name   string
		sub    doger.Subscription
		before func(t *testing.T, db *testDB, tr *eventsql.Transport)
		want   []string
	}{
		{
			name: "never_subscribed",
			sub:  group(dest, "mail"),
			before: func(t *testing.T, _ *testDB, tr *eventsql.Transport) {
				send(t, tr, "e1", dest)
				send(t, tr, "e2", dest)
			},
			want: []string{"e3"},
		},
		{
			name: "subscribed_and_stopped",
			sub:  group(dest, "mail"),
			before: func(t *testing.T, _ *testDB, tr *eventsql.Transport) {
				stop := subscribe(t, tr, group(dest, "mail"), newCollector().handle)
				require.NoError(t, stop(context.Background()))
				send(t, tr, "e1", dest)
				send(t, tr, "e2", dest)
			},
			want: []string{"e1", "e2", "e3"},
		},
		{
			name: "ephemeral_subscribed_and_stopped",
			sub:  doger.Subscription{Destination: dest, Group: "cache/x", Mode: doger.DeliverAll, Ephemeral: true},
			before: func(t *testing.T, _ *testDB, tr *eventsql.Transport) {
				stop := subscribe(t, tr, doger.Subscription{Destination: dest, Group: "cache/x", Mode: doger.DeliverAll, Ephemeral: true}, newCollector().handle)
				require.NoError(t, stop(context.Background()))
				send(t, tr, "e1", dest)
			},
			want: []string{"e3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			forEachEngine(t, func(t *testing.T, db *testDB) {
				testWithTimeout(t, time.Minute)
				tr := schemaTransport(t, db, fast()...)
				tt.before(t, db, tr)
				c := newCollector()
				subscribe(t, tr, tt.sub, c.handle)
				send(t, tr, "e3", dest)

				got := c.expect(t, len(tt.want))
				c.quiet(t)
				assert.ElementsMatch(t, tt.want, got)
			})
		})
	}
}

func TestGroups_Ephemeral(t *testing.T) {
	t.Parallel()

	const dest = "orders.placed"
	ephemeral := func(name string) doger.Subscription {
		return doger.Subscription{Destination: dest, Group: name, Mode: doger.DeliverAll, Ephemeral: true}
	}
	tests := []struct {
		name      string
		setup     func(t *testing.T, db *testDB, tr *eventsql.Transport)
		group     string
		wantKept  bool
		wantDeliv int // deliveries of the group afterwards
	}{
		{
			name:  "stopped_normally",
			group: "cache/a",
			setup: func(t *testing.T, _ *testDB, tr *eventsql.Transport) {
				stop := subscribe(t, tr, ephemeral("cache/a"), newCollector().handle)
				require.NoError(t, stop(context.Background()))
			},
		},
		{
			name:  "crashed",
			group: "cache/b",
			setup: func(t *testing.T, db *testDB, tr *eventsql.Transport) {
				seedGroup(t, db, dest, "cache/b", true)
				for _, id := range []string{"b1", "b2", "b3"} {
					send(t, tr, id, dest)
				}
			},
		},
		{
			name:  "durable_without_member",
			group: "audit",
			setup: func(t *testing.T, db *testDB, tr *eventsql.Transport) {
				seedGroup(t, db, dest, "audit", false)
				for _, id := range []string{"a1", "a2", "a3"} {
					send(t, tr, id, dest)
				}
			},
			wantKept:  true,
			wantDeliv: 3,
		},
		{
			name:  "running",
			group: "cache/c",
			setup: func(t *testing.T, _ *testDB, tr *eventsql.Transport) {
				subscribe(t, tr, ephemeral("cache/c"), newCollector().handle)
			},
			wantKept: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			forEachEngine(t, func(t *testing.T, db *testDB) {
				testWithTimeout(t, time.Minute)
				tr := schemaTransport(t, db, eventsql.EphemeralTTL(time.Second), eventsql.PollInterval(50*time.Millisecond))
				tt.setup(t, db, tr)
				// "mail" ignores the events, so the others keep theirs
				subscribe(t, tr, group("other.destination", "mail"), newCollector().handle)
				time.Sleep(2 * time.Second)

				groups := db.Count(t, "SELECT COUNT(*) FROM doger_event_groups WHERE group_name = ?", tt.group)
				deliveries := db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries WHERE group_name = ?", tt.group)
				if tt.wantKept {
					assert.Equal(t, 1, groups, "the group was removed")
				} else {
					assert.Zero(t, groups, "the group was kept")
				}
				assert.Equal(t, tt.wantDeliv, deliveries)
			})
		})
	}
}

// TestGroups_EphemeralStopDuringOutbox is a regression test: an outbox
// transaction that fanned out to an ephemeral group and committed after
// the group's stop removed it left a delivery no group claims, forever.
// Maintenance deletes the deliveries of groups that no longer exist.
// PostgreSQL only: on MySQL the fan-out's shared locks make the stop wait
// for the commit, so the orphan cannot occur.
func TestGroups_EphemeralStopDuringOutbox(t *testing.T) {
	t.Parallel()
	forEachEngine(t, func(t *testing.T, db *testDB) {
		testWithTimeout(t, time.Minute)
		const dest = "orders.placed"
		tr := schemaTransport(t, db, eventsql.EphemeralTTL(time.Second), eventsql.PollInterval(50*time.Millisecond))
		// "mail" runs the maintenance
		subscribe(t, tr, group("other.destination", "mail"), newCollector().handle)
		stop := subscribe(t, tr, doger.Subscription{Destination: dest, Group: "cache/a", Mode: doger.DeliverAll, Ephemeral: true}, newCollector().handle)

		ctx, tx, err := sqltx.BeginTx(context.Background(), db.DB, nil)
		require.NoError(t, err)
		require.NoError(t, tr.SendTx(ctx, envelope("e1", dest)))
		require.NoError(t, stop(context.Background()))
		require.NoError(t, tx.Commit())

		waitFor(t, func() bool {
			return db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries WHERE group_name = 'cache/a'") == 0
		}, "the delivery of the removed group was kept")
		waitFor(t, func() bool { return db.Count(t, "SELECT COUNT(*) FROM doger_events") == 0 }, "the event was kept")
		assert.Zero(t, db.Count(t, "SELECT COUNT(*) FROM doger_event_groups WHERE group_name = 'cache/a'"))
	}, "postgres")
}
