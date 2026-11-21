package eventsql

import (
	"context"

	"github.com/kartaladev/doger"
	"github.com/kartaladev/doger/events/sqltx"
)

var _ doger.TxEventSender = (*Transport)(nil)

// SendTx writes env and its deliveries with the transaction sqltx binds to
// ctx, so they commit or roll back with it: a rollback leaves nothing, a
// commit makes env deliverable. It returns the error of sqltx.FromDB
// unchanged when ctx holds no active transaction (doger.ErrNoTransaction)
// or one of another *sql.DB (sqltx.ErrForeignTransaction).
func (t *Transport) SendTx(ctx context.Context, env doger.Envelope) error {
	tx, err := sqltx.FromDB(ctx, t.db)
	if err != nil {
		return err
	}

	return t.writeEvent(ctx, tx.SQL(), env)
}

// TxSource returns sqltx.Source(), the TxSource that finds the
// transactions SendTx writes in. Setting sqltx.Source() in
// InjectorOpts.TxSource as well is the same source, not a conflict.
func (t *Transport) TxSource() doger.TxSource { return sqltx.Source() }
