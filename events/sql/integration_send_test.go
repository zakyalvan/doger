package eventsql_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/doger"
	eventsql "github.com/kartaladev/doger/events/sql"
)

// envelope returns an envelope with ID id for destination.
func envelope(id, destination string) doger.Envelope {
	return doger.Envelope{
		ID:          id,
		Destination: destination,
		EventType:   "orders.placed",
		ContentType: "application/json",
		Version:     1,
		Payload:     []byte(`{"id":"` + id + `"}`),
		Headers:     map[string]string{"traceparent": "00-1"},
		PublishedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
}

// seedGroup registers group for destination directly.
func seedGroup(t *testing.T, db *testDB, destination, group string, ephemeral bool) {
	t.Helper()
	db.Exec(t, "INSERT INTO doger_event_groups (destination, group_name, ephemeral, heartbeat_at) VALUES (?, ?, ?, "+db.Now()+")",
		destination, group, ephemeral)
}

// schemaTransport returns a transport on db with its schema created.
func schemaTransport(t *testing.T, db *testDB, opts ...eventsql.Option) *eventsql.Transport {
	t.Helper()
	tr := db.Transport(t, opts...)
	require.NoError(t, tr.EnsureSchema(context.Background()))
	return tr
}

// pendingGroups returns the groups holding a pending delivery of envelope
// id, sorted.
func pendingGroups(t *testing.T, db *testDB, id string) []string {
	t.Helper()
	rows, err := db.DB.QueryContext(context.Background(), db.Rebind(
		"SELECT d.group_name FROM doger_event_deliveries d JOIN doger_events e ON e.event_key = d.event_key WHERE e.id = ? AND d.state = 'pending' ORDER BY d.group_name"), id)
	require.NoError(t, err)
	defer rows.Close()
	var groups []string
	for rows.Next() {
		var g string
		require.NoError(t, rows.Scan(&g))
		groups = append(groups, g)
	}
	require.NoError(t, rows.Err())
	return groups
}

func TestSend_DeliveriesPerGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		groups     []string
		size       int
		wantErr    error
		wantGroups []string
	}{
		{name: "one_group", groups: []string{"mail"}, size: 100, wantGroups: []string{"mail"}},
		{name: "two_groups", groups: []string{"mail", "cache/pod-0"}, size: 100, wantGroups: []string{"cache/pod-0", "mail"}},
		{name: "no_group", size: 100},
		{name: "max_payload", groups: []string{"mail"}, size: 1 << 20, wantGroups: []string{"mail"}},
		{name: "payload_too_large", groups: []string{"mail"}, size: 1<<20 + 1, wantErr: eventsql.ErrPayloadTooLarge},
		{name: "empty_payload", groups: []string{"mail"}, size: 0, wantGroups: []string{"mail"}},
		{name: "nil_payload", groups: []string{"mail"}, size: -1, wantGroups: []string{"mail"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			forEachEngine(t, func(t *testing.T, db *testDB) {
				tr := schemaTransport(t, db)
				for _, g := range tt.groups {
					seedGroup(t, db, "orders.placed", g, false)
				}
				env := envelope("e1", "orders.placed")
				env.Payload = nil // size -1
				if tt.size >= 0 {
					env.Payload = bytes.Repeat([]byte("x"), tt.size)
				}

				err := tr.Send(context.Background(), env)

				if tt.wantErr != nil {
					require.ErrorIs(t, err, tt.wantErr)
					assert.True(t, doger.IsPermanentError(err), "the error is permanent")
				} else {
					require.NoError(t, err)
				}
				assert.Equal(t, tt.wantGroups, pendingGroups(t, db, "e1"))
				wantEvents := 0
				if len(tt.wantGroups) > 0 {
					wantEvents = 1
				}
				assert.Equal(t, wantEvents, db.Count(t, "SELECT COUNT(*) FROM doger_events WHERE id = 'e1'"))
				assert.Equal(t, len(tt.wantGroups), db.Count(t, "SELECT COUNT(*) FROM doger_event_deliveries"))
			})
		})
	}
}

func TestSend_Failures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, db *testDB) error
		want func(t *testing.T, db *testDB, err error)
	}{
		{
			name: "unreachable",
			run: func(t *testing.T, db *testDB) error {
				dsn := "postgres://doger:doger@127.0.0.1:1/doger?sslmode=disable&connect_timeout=2" //nolint:gosec // G101: a test server that does not exist
				if db.kind == "mysql" {
					dsn = "doger:doger@tcp(127.0.0.1:1)/doger?timeout=2s"
				}
				unreachable, err := sql.Open(db.driver, dsn)
				require.NoError(t, err)
				t.Cleanup(func() { _ = unreachable.Close() })
				tr, err := eventsql.New(unreachable, db.Dialect())
				require.NoError(t, err)
				return tr.Send(context.Background(), envelope("e1", "orders.placed"))
			},
			want: func(t *testing.T, _ *testDB, err error) {
				require.Error(t, err)
				assert.Error(t, errors.Unwrap(err), "the driver's error is wrapped")
			},
		},
		{
			name: "cancelled_context",
			run: func(t *testing.T, db *testDB) error {
				tr := schemaTransport(t, db)
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return tr.Send(ctx, envelope("e1", "orders.placed"))
			},
			want: func(t *testing.T, _ *testDB, err error) {
				require.ErrorIs(t, err, context.Canceled)
			},
		},
		{
			name: "missing_table",
			run: func(t *testing.T, db *testDB) error {
				return db.Transport(t).Send(context.Background(), envelope("e1", "orders.placed"))
			},
			want: func(t *testing.T, db *testDB, err error) {
				require.Error(t, err)
				if db.kind == "mysql" {
					_, ok := errors.AsType[*gomysql.MySQLError](err)
					assert.True(t, ok, "wraps *mysql.MySQLError: %v", err)
				} else {
					_, ok := errors.AsType[*pgconn.PgError](err)
					assert.True(t, ok, "wraps *pgconn.PgError: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			forEachEngine(t, func(t *testing.T, db *testDB) {
				tt.want(t, db, tt.run(t, db))
			})
		})
	}
}

// TestSend_RoundTrip checks that a delivered envelope equals the sent
// one. Regression cases: a nil payload failed the send (NOT NULL), and a
// zero PublishedAt came back as a date in 1754.
func TestSend_RoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		modify func(env *doger.Envelope)
		want   func(env doger.Envelope) doger.Envelope // the expected delivered envelope
	}{
		{name: "full", modify: func(*doger.Envelope) {}, want: func(env doger.Envelope) doger.Envelope { return env }},
		{
			name:   "zero_published_at",
			modify: func(env *doger.Envelope) { env.PublishedAt = time.Time{} },
			want:   func(env doger.Envelope) doger.Envelope { return env },
		},
		{
			name:   "nil_payload",
			modify: func(env *doger.Envelope) { env.Payload = nil },
			want: func(env doger.Envelope) doger.Envelope {
				env.Payload = []byte{}
				return env
			},
		},
		{
			name:   "unix_epoch_published_at",
			modify: func(env *doger.Envelope) { env.PublishedAt = time.Unix(0, 1).UTC() },
			want:   func(env doger.Envelope) doger.Envelope { return env },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			forEachEngine(t, func(t *testing.T, db *testDB) {
				testWithTimeout(t, time.Minute)
				const dest = "orders.placed"
				tr := schemaTransport(t, db, fast()...)
				got := make(chan doger.Envelope, 1)
				subscribe(t, tr, group(dest, "mail"), func(_ context.Context, env doger.Envelope) error {
					got <- env
					return nil
				})
				env := envelope("e1", dest)
				tt.modify(&env)

				require.NoError(t, tr.Send(context.Background(), env))

				select {
				case delivered := <-got:
					want := tt.want(env)
					assert.True(t, want.PublishedAt.Equal(delivered.PublishedAt), "PublishedAt %v, want %v", delivered.PublishedAt, want.PublishedAt)
					assert.Equal(t, want.PublishedAt.IsZero(), delivered.PublishedAt.IsZero())
					want.PublishedAt, delivered.PublishedAt = time.Time{}, time.Time{}
					if len(want.Payload) == 0 {
						assert.Empty(t, delivered.Payload)
						want.Payload, delivered.Payload = nil, nil
					}
					assert.Equal(t, want, delivered)
				case <-time.After(waitTimeout):
					t.Fatal("the envelope was not delivered")
				}
			})
		})
	}
}
