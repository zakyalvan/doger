package eventsql

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kartaladev/doger"
)

// ErrPayloadTooLarge is matched by the error of Send and SendTx for a
// payload larger than MaxPayloadBytes; the error is permanent (see
// doger.IsPermanentError).
var ErrPayloadTooLarge = errors.New("doger: eventsql: payload too large")

// Send stores env and one pending delivery for each group registered for
// env.Destination when the write runs, in one transaction of its own. With
// no group registered it stores nothing and returns nil. It may be called
// concurrently.
func (t *Transport) Send(ctx context.Context, env doger.Envelope) error {
	if err := t.checkPayload(env); err != nil {
		return err
	}
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("doger: eventsql: send: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := t.writeEvent(ctx, tx, env); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("doger: eventsql: send: %w", err)
	}

	return nil
}

// querier runs statements: a *sql.DB, *sql.Conn or *sql.Tx.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// writeEvent stores env and its deliveries with q, which must be a
// transaction: the event, the fan-out and the delete of an event no group
// receives commit together.
func (t *Transport) writeEvent(ctx context.Context, q querier, env doger.Envelope) error {
	if err := t.checkPayload(env); err != nil {
		return err
	}
	headers, err := json.Marshal(env.Headers)
	if err != nil {
		return fmt.Errorf("doger: eventsql: send: encoding headers: %w", err)
	}
	key := newToken()

	payload := env.Payload
	if payload == nil {
		payload = []byte{} // the column is NOT NULL
	}

	if _, err := q.ExecContext(ctx, t.stmts.InsertEvent, key, env.ID, env.Destination, env.EventType, env.Version,
		env.ContentType, payload, string(headers), encodePublishedAt(env.PublishedAt)); err != nil {
		return fmt.Errorf("doger: eventsql: send: %w", err)
	}
	res, err := q.ExecContext(ctx, t.stmts.FanOut, key, env.Destination)
	if err != nil {
		return fmt.Errorf("doger: eventsql: send: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("doger: eventsql: send: %w", err)
	} else if n == 0 {
		if _, err := q.ExecContext(ctx, t.stmts.DeleteEvent, key); err != nil {
			return fmt.Errorf("doger: eventsql: send: %w", err)
		}
	}

	return nil
}

// checkPayload returns a permanent error matching ErrPayloadTooLarge for a
// payload larger than MaxPayloadBytes.
func (t *Transport) checkPayload(env doger.Envelope) error {
	if len(env.Payload) > t.cfg.maxPayloadBytes {
		return doger.PermanentError(fmt.Errorf("%w: %d bytes, MaxPayloadBytes is %d", ErrPayloadTooLarge, len(env.Payload), t.cfg.maxPayloadBytes))
	}

	return nil
}

// newToken returns a random 128-bit value in hex: event keys and lease
// tokens.
func newToken() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])

	return hex.EncodeToString(raw[:])
}
