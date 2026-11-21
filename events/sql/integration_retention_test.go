package eventsql_test

import (
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	eventsql "github.com/kartaladev/doger/events/sql"
)

func TestRetention(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		opts          []eventsql.Option
		wantEvents    int
		wantDelivered int
	}{
		{name: "default"},
		{name: "keep_1h", opts: []eventsql.Option{eventsql.KeepDelivered(time.Hour)}, wantEvents: 1, wantDelivered: 1},
		{name: "keep_1s", opts: []eventsql.Option{eventsql.KeepDelivered(time.Second)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			forEachEngine(t, func(t *testing.T, db *testDB) {
				testWithTimeout(t, time.Minute)
				const dest = "orders.placed"
				tr := schemaTransport(t, db, append(tt.opts, eventsql.PollInterval(50*time.Millisecond))...)
				c := newCollector()
				subscribe(t, tr, group(dest, "mail"), c.handle)
				send(t, tr, "e1", dest)
				c.expect(t, 1)
				time.Sleep(2 * time.Second)

				assert.Equal(t, tt.wantEvents, db.Count(t, "SELECT COUNT(*) FROM doger_events"))
				assert.Equal(t, tt.wantDelivered, db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries WHERE state = 'delivered'"))
				assert.Equal(t, tt.wantDelivered, db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries"))
			})
		})
	}
}

// staleGroupWarning is the message of the stale group warning.
const staleGroupWarning = "doger: eventsql: a group has pending deliveries older than a day; drop it with DropGroup if its listener was removed" //nolint:gosec // G101: a log message, not a credential

// TestRetention_StaleGroupWarning is a regression test: the table-wide
// maintenance ran once per subscription, so a transport with two
// subscriptions warned twice about the same stale group. It runs once
// per transport.
func TestRetention_StaleGroupWarning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		subscriptions int
	}{
		{name: "one_subscription", subscriptions: 1},
		{name: "three_subscriptions", subscriptions: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			forEachEngine(t, func(t *testing.T, db *testDB) {
				testWithTimeout(t, time.Minute)
				logs := &logRecorder{}
				tr := schemaTransport(t, db, fast(eventsql.Logger(slog.New(logs)))...)
				// "removed" lost its listener two days ago
				seedGroup(t, db, "orders.placed", "removed", false)
				send(t, tr, "e1", "orders.placed")
				db.Exec(t, "UPDATE doger_event_deliveries SET created_at = "+db.Now()+" - "+db.Interval(48*time.Hour))

				for i := range tt.subscriptions {
					subscribe(t, tr, group("other.destination", fmt.Sprintf("g%d", i)), newCollector().handle)
				}

				waitFor(t, func() bool { return logs.Count(staleGroupWarning) > 0 }, "the stale group was not warned about")
				time.Sleep(time.Second) // several maintenance steps
				assert.Equal(t, 1, logs.Count(staleGroupWarning))
			})
		})
	}
}
