package eventsql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kartaladev/doger"
)

// queryAll runs stmt with q and returns its rows, each read by scan.
func queryAll[T any](ctx context.Context, q querier, scan func(*sql.Rows) (T, error), stmt string, args ...any) ([]T, error) {
	rows, err := q.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var all []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		all = append(all, v)
	}

	return all, rows.Err()
}

// scanOne reads a row of a single column.
func scanOne[T any](rows *sql.Rows) (v T, err error) {
	err = rows.Scan(&v)
	return v, err
}

// decodeEnvelope completes env, scanned from a row, with its stored
// headers (JSON) and publication time (Unix nanoseconds).
func decodeEnvelope(env *doger.Envelope, headers string, published int64) error {
	if err := json.Unmarshal([]byte(headers), &env.Headers); err != nil {
		return fmt.Errorf("decoding headers of %q: %w", env.ID, err)
	}
	env.PublishedAt = decodePublishedAt(published)

	return nil
}

// encodePublishedAt returns the stored form of a publication time: Unix
// nanoseconds, 0 for the zero time.Time.
func encodePublishedAt(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.UnixNano()
}

// decodePublishedAt returns the publication time stored as published: the
// zero time.Time for 0, so an exact Unix epoch reads back as zero too.
func decodePublishedAt(published int64) time.Time {
	if published == 0 {
		return time.Time{}
	}
	return time.Unix(0, published).UTC()
}
