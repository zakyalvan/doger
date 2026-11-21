package eventsql_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEngines checks that every configured engine is reachable and that
// each test gets a schema or database of its own.
func TestEngines(t *testing.T) {
	t.Parallel()
	forEachEngine(t, func(t *testing.T, db *testDB) {
		require.NoError(t, db.DB.PingContext(context.Background()))
		db.Exec(t, "CREATE TABLE probe (id INT)")
		require.Equal(t, 0, db.Count(t, "SELECT COUNT(*) FROM probe"))
	})
}
