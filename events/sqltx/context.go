package sqltx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kartaladev/doger"
)

// ErrForeignTransaction is matched by the error of FromDB when the
// context's transaction belongs to another *sql.DB. Every SQL-based doger
// adapter returns it for an outbox write in a foreign transaction, which
// would otherwise break atomicity silently.
var ErrForeignTransaction = errors.New("doger: sqltx: the transaction belongs to another *sql.DB")

// txKey is the context key of the bound *Tx. Detach stores a nil *Tx under
// it to mask the transaction.
type txKey struct{}

// From returns the transaction ctx holds, and true until it is committed
// or rolled back; nil and false otherwise, or for a nil ctx.
func From(ctx context.Context) (*Tx, bool) {
	if ctx == nil {
		return nil, false
	}
	tx, _ := ctx.Value(txKey{}).(*Tx)
	if tx == nil || tx.ended.Load() {
		return nil, false
	}

	return tx, true
}

// FromDB returns the active transaction ctx holds when it belongs to db.
// It returns an error matching [doger.ErrNoTransaction] when ctx holds no
// active transaction, and one matching ErrForeignTransaction when the
// transaction belongs to another *sql.DB.
func FromDB(ctx context.Context, db *sql.DB) (*Tx, error) {
	tx, ok := From(ctx)
	if !ok {
		return nil, fmt.Errorf("doger: sqltx: no active transaction in the context: %w", doger.ErrNoTransaction)
	}
	if tx.db != db {
		return nil, ErrForeignTransaction
	}

	return tx, nil
}

// Source returns the [doger.TxSource] of sqltx: Lookup answers as [From]
// and Detach returns a context with the same values, no transaction and no
// cancellation. Every call returns the same comparable value, so setting
// it in InjectorOpts.TxSource next to a transport whose TxSource is
// Source() is not a conflict.
func Source() doger.TxSource { return source{} }

// source is the zero-size TxSource of sqltx.
type source struct{}

func (source) Lookup(ctx context.Context) (doger.Tx, bool) {
	tx, ok := From(ctx)
	if !ok {
		return nil, false
	}

	return tx, true
}

func (source) Detach(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	return context.WithValue(context.WithoutCancel(ctx), txKey{}, (*Tx)(nil))
}
