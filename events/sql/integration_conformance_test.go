package eventsql_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/doger"
	eventsql "github.com/kartaladev/doger/events/sql"
	"github.com/kartaladev/doger/events/sql/mysql"
	"github.com/kartaladev/doger/events/sql/postgres"
	"github.com/kartaladev/doger/eventtransporttest"
)

// conformanceOptions are the options of the conformance factories.
func conformanceOptions() []eventsql.Option {
	return fast(eventsql.Backoff(eventsql.FixedBackoff(10 * time.Millisecond)))
}

func TestConformance(t *testing.T) {
	t.Parallel()
	forEachEngine(t, func(t *testing.T, db *testDB) {
		testWithTimeout(t, 5*time.Minute)
		require.NoError(t, db.Transport(t).EnsureSchema(context.Background()))
		eventtransporttest.Run(t, func(t *testing.T) doger.EventTransport {
			return db.Transport(t, conformanceOptions()...)
		}, eventtransporttest.WithTx(txHarness{db: db.DB}))
	})
}

// TestDDL_Conformance applies the reference DDL as a migration tool would,
// statement by statement, then runs the conformance suite on it.
func TestDDL_Conformance(t *testing.T) {
	t.Parallel()
	forEachEngine(t, func(t *testing.T, db *testDB) {
		testWithTimeout(t, 5*time.Minute)
		ddl := postgres.DDL
		if db.kind == "mysql" {
			ddl = mysql.DDL
		}
		stmts, err := ddl("doger_")
		require.NoError(t, err)
		for _, stmt := range stmts {
			db.Exec(t, stmt)
		}
		eventtransporttest.Run(t, func(t *testing.T) doger.EventTransport {
			return db.Transport(t, conformanceOptions()...)
		}, eventtransporttest.WithTx(txHarness{db: db.DB}))
	})
}
