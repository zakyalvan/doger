package eventsql_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/doger"
	eventsql "github.com/kartaladev/doger/events/sql"
	"github.com/kartaladev/doger/events/sql/postgres"
)

// TestSubscribe_TinyEphemeralTTL is a regression test: an EphemeralTTL
// below 3ns made the maintenance interval zero, and time.NewTicker
// panicked in the subscription's goroutine.
func TestSubscribe_TinyEphemeralTTL(t *testing.T) {
	t.Parallel()
	db, _ := newFakeDB(t)
	tr, err := eventsql.New(db, postgres.Dialect(), eventsql.EphemeralTTL(time.Nanosecond))
	require.NoError(t, err)

	stop, err := tr.Subscribe(context.Background(), group("orders.placed", "mail"), func(context.Context, doger.Envelope) error { return nil })
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, stop(context.Background()))
}
