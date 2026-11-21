package eventsql_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	eventsql "github.com/kartaladev/doger/events/sql"
	"github.com/kartaladev/doger/events/sql/mysql"
	"github.com/kartaladev/doger/events/sql/postgres"
)

func TestNew_Arguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		nilDB      bool
		dialect    eventsql.Dialect
		opts       []eventsql.Option
		wantErr    error
		wantNaming string
	}{
		{name: "postgres", dialect: postgres.Dialect()},
		{name: "mysql", dialect: mysql.Dialect()},
		{name: "nil_db", nilDB: true, dialect: postgres.Dialect(), wantErr: eventsql.ErrNilDB},
		{name: "nil_dialect", dialect: nil, wantErr: eventsql.ErrNilDialect},
		{name: "batch_size_0", dialect: postgres.Dialect(), opts: []eventsql.Option{eventsql.BatchSize(0)}, wantErr: eventsql.ErrInvalidOption, wantNaming: "BatchSize"},
		{name: "workers_0", dialect: postgres.Dialect(), opts: []eventsql.Option{eventsql.Workers(0)}, wantErr: eventsql.ErrInvalidOption, wantNaming: "Workers"},
		{name: "lease_0", dialect: postgres.Dialect(), opts: []eventsql.Option{eventsql.Lease(0)}, wantErr: eventsql.ErrInvalidOption, wantNaming: "Lease"},
		{name: "poll_interval_negative", dialect: postgres.Dialect(), opts: []eventsql.Option{eventsql.PollInterval(-time.Second)}, wantErr: eventsql.ErrInvalidOption, wantNaming: "PollInterval"},
		{name: "max_attempts_0", dialect: postgres.Dialect(), opts: []eventsql.Option{eventsql.MaxAttempts(0)}, wantErr: eventsql.ErrInvalidOption, wantNaming: "MaxAttempts"},
		{name: "max_payload_bytes_0", dialect: postgres.Dialect(), opts: []eventsql.Option{eventsql.MaxPayloadBytes(0)}, wantErr: eventsql.ErrInvalidOption, wantNaming: "MaxPayloadBytes"},
		{name: "ephemeral_ttl_0", dialect: postgres.Dialect(), opts: []eventsql.Option{eventsql.EphemeralTTL(0)}, wantErr: eventsql.ErrInvalidOption, wantNaming: "EphemeralTTL"},
		{name: "batch_size_1_workers_1", dialect: postgres.Dialect(), opts: []eventsql.Option{eventsql.BatchSize(1), eventsql.Workers(1)}},
		{name: "logger_nil", dialect: postgres.Dialect(), opts: []eventsql.Option{eventsql.Logger((*slog.Logger)(nil))}, wantErr: eventsql.ErrInvalidOption, wantNaming: "Logger"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db, rec := newFakeDB(t)
			if tt.nilDB {
				db = nil
			}

			tr, err := eventsql.New(db, tt.dialect, tt.opts...)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, tr)
				assert.Contains(t, err.Error(), tt.wantNaming)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, tr)
			}
			assert.Zero(t, rec.Connects(), "New contacted the database")
		})
	}
}

func TestNew_TablePrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    []eventsql.Option
		want    []string // events, deliveries, groups
		wantErr bool
	}{
		{name: "default", want: []string{"doger_events", "doger_event_deliveries", "doger_event_groups"}},
		{name: "shop", opts: []eventsql.Option{eventsql.TablePrefix("shop_")}, want: []string{"shop_events", "shop_event_deliveries", "shop_event_groups"}},
		{name: "schema_qualified", opts: []eventsql.Option{eventsql.TablePrefix("app.doger_")}, want: []string{"app.doger_events", "app.doger_event_deliveries", "app.doger_event_groups"}},
		{name: "empty", opts: []eventsql.Option{eventsql.TablePrefix("")}, wantErr: true},
		{name: "dash", opts: []eventsql.Option{eventsql.TablePrefix("bad-name")}, wantErr: true},
		{name: "injection", opts: []eventsql.Option{eventsql.TablePrefix("x; DROP TABLE users; --")}, wantErr: true},
		{name: "two_dots", opts: []eventsql.Option{eventsql.TablePrefix("a.b.c_")}, wantErr: true},
		{name: "40_bytes", opts: []eventsql.Option{eventsql.TablePrefix(strings.Repeat("a", 40))}, want: []string{strings.Repeat("a", 40) + "events", strings.Repeat("a", 40) + "event_deliveries", strings.Repeat("a", 40) + "event_groups"}},
		{name: "41_bytes", opts: []eventsql.Option{eventsql.TablePrefix(strings.Repeat("a", 41))}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db, _ := newFakeDB(t)

			tr, err := eventsql.New(db, postgres.Dialect(), tt.opts...)

			if tt.wantErr {
				require.ErrorIs(t, err, eventsql.ErrInvalidTableName)
				assert.Nil(t, tr)
				return
			}
			require.NoError(t, err)
			tables := eventsql.TablesOf(tr)
			assert.Equal(t, tt.want, []string{tables.Events, tables.Deliveries, tables.Groups})
		})
	}
}
