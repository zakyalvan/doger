package eventsql

import (
	"context"
	"fmt"
	"strconv"

	"github.com/kartaladev/doger"
)

// DeadLetter is a delivery that was dead-lettered: its handler failed
// MaxAttempts times, or failed permanently.
type DeadLetter struct {
	ID        string         // pass it to Resubmit
	Group     string         // the group whose handler failed
	Envelope  doger.Envelope // the event
	Attempts  int            // the attempts made
	LastError string         // the text of the last handler error, at most 2 KiB
}

// ListDeadLetters returns up to limit dead letters of group for
// destination, oldest first. It returns an error matching
// ErrInvalidOption for a limit below 1.
func (t *Transport) ListDeadLetters(ctx context.Context, destination, group string, limit int) ([]DeadLetter, error) {
	if limit < 1 {
		return nil, fmt.Errorf("%w: ListDeadLetters limit must be at least 1, got %d", ErrInvalidOption, limit)
	}
	rows, err := t.db.QueryContext(ctx, t.stmts.ListDeadLetters, destination, group, limit)
	if err != nil {
		return nil, fmt.Errorf("doger: eventsql: list dead letters: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var letters []DeadLetter
	for rows.Next() {
		var (
			l         = DeadLetter{Group: group}
			seq       int64
			headers   string
			published int64
		)
		if err := rows.Scan(&seq, &l.Attempts, &l.LastError, &l.Envelope.ID, &l.Envelope.Destination, &l.Envelope.EventType,
			&l.Envelope.Version, &l.Envelope.ContentType, &l.Envelope.Payload, &headers, &published); err != nil {
			return nil, fmt.Errorf("doger: eventsql: list dead letters: %w", err)
		}
		if err := decodeEnvelope(&l.Envelope, headers, published); err != nil {
			return nil, fmt.Errorf("doger: eventsql: list dead letters: %w", err)
		}
		l.ID = strconv.FormatInt(seq, 10)
		letters = append(letters, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("doger: eventsql: list dead letters: %w", err)
	}

	return letters, nil
}

// Resubmit makes the dead letters with the given IDs (DeadLetter.ID)
// pending again, visible at once, with their attempts reset. An ID that
// names no dead letter is ignored.
func (t *Transport) Resubmit(ctx context.Context, ids ...string) error {
	for _, id := range ids {
		seq, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			continue // not a dead letter ID
		}
		if _, err := t.db.ExecContext(ctx, t.stmts.Resubmit, seq); err != nil {
			return fmt.Errorf("doger: eventsql: resubmit %s: %w", id, err)
		}
	}

	return nil
}

// DropGroup unregisters group for destination and deletes its deliveries,
// pending and dead, in one transaction: a later send creates no delivery
// for it. Use it for a durable group whose listener was removed from the
// code, whose deliveries would otherwise accumulate. A running member
// registers the group again.
func (t *Transport) DropGroup(ctx context.Context, destination, group string) error {
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("doger: eventsql: drop group: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, t.stmts.DeleteGroup, destination, group); err != nil {
		return fmt.Errorf("doger: eventsql: drop group: %w", err)
	}
	if _, err := tx.ExecContext(ctx, t.stmts.DeleteGroupDeliveries, destination, group); err != nil {
		return fmt.Errorf("doger: eventsql: drop group: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("doger: eventsql: drop group: %w", err)
	}

	return nil
}
