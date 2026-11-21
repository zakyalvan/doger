package postgres_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	eventsql "github.com/kartaladev/doger/events/sql"
	"github.com/kartaladev/doger/events/sql/postgres"
)

func TestDDL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prefix  string
		want    []string // tables the statements create
		wantErr error
	}{
		{name: "default_prefix", prefix: "doger_", want: []string{"doger_events", "doger_event_deliveries", "doger_event_groups"}},
		{name: "invalid_prefix", prefix: "bad-name", wantErr: eventsql.ErrInvalidTableName},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stmts, err := postgres.DDL(tt.prefix)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, stmts)
				return
			}
			require.NoError(t, err)
			all := strings.Join(stmts, "\n")
			for _, table := range tt.want {
				assert.Contains(t, all, "CREATE TABLE IF NOT EXISTS \""+table+"\"")
			}
		})
	}
}
