package sqltx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/kartaladev/doger"
)

// ErrNilDB is matched by the error of BeginTx and InTx for a nil *sql.DB.
var ErrNilDB = errors.New("doger: sqltx: nil *sql.DB")

// Tx is a database/sql transaction bound to a context, with the commit and
// rollback callbacks a *sql.Tx lacks. It implements [doger.Tx]. Commit and
// Rollback end it through this wrapper so the callbacks run; ending the
// *sql.Tx returned by SQL directly skips them and leaves the context
// reporting an active transaction.
type Tx struct {
	tx  *sql.Tx
	db  *sql.DB
	ctx context.Context // the context the callbacks receive, not cancelled

	mu         sync.Mutex
	onCommit   []func(context.Context)
	onRollback []func(context.Context)
	ended      atomic.Bool // set by the first Commit or Rollback
}

var _ doger.Tx = (*Tx)(nil)

// SQL returns the *sql.Tx, for running statements in the transaction.
func (t *Tx) SQL() *sql.Tx { return t.tx }

// DB returns the *sql.DB the transaction belongs to.
func (t *Tx) DB() *sql.DB { return t.db }

// Commit commits the transaction, then runs the OnCommit callbacks in
// registration order; when the commit fails it runs the OnRollback
// callbacks instead and returns the error. A second Commit or Rollback
// returns sql.ErrTxDone and runs nothing.
func (t *Tx) Commit() error { return t.end(true) }

// Rollback rolls the transaction back, then runs the OnRollback callbacks
// in registration order. A second Commit or Rollback returns sql.ErrTxDone
// and runs nothing.
func (t *Tx) Rollback() error { return t.end(false) }

// end commits or rolls back once, then runs the callbacks of the outcome:
// the OnRollback ones when a commit fails.
func (t *Tx) end(commit bool) error {
	if !t.ended.CompareAndSwap(false, true) {
		return sql.ErrTxDone
	}
	var err error
	if commit {
		err = t.tx.Commit()
	} else {
		err = t.tx.Rollback()
	}
	t.runCallbacks(commit && err == nil)

	return err
}

// OnCommit registers fn to run once the transaction committed. A nil fn is
// ignored; a fn registered once the transaction ended never runs and is
// logged through slog.Default().
func (t *Tx) OnCommit(fn func(context.Context)) { t.register(&t.onCommit, fn) }

// OnRollback registers fn to run once the transaction rolled back, or its
// commit failed. A nil fn is ignored; a fn registered once the transaction
// ended never runs and is logged through slog.Default().
func (t *Tx) OnRollback(fn func(context.Context)) { t.register(&t.onRollback, fn) }

// register appends fn to *list unless the transaction ended.
func (t *Tx) register(list *[]func(context.Context), fn func(context.Context)) {
	if fn == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ended.Load() {
		slog.Default().Warn("doger: sqltx: callback registered on a finished transaction; it will not run")
		return
	}
	*list = append(*list, fn)
}

// runCallbacks runs the commit or the rollback callbacks, each once, a
// panicking one recovered and logged.
func (t *Tx) runCallbacks(committed bool) {
	t.mu.Lock()
	callbacks := t.onRollback
	if committed {
		callbacks = t.onCommit
	}
	t.onCommit, t.onRollback = nil, nil
	t.mu.Unlock()

	for _, fn := range callbacks {
		runCallback(t.ctx, fn)
	}
}

// runCallback runs fn, recovering and logging a panic.
func runCallback(ctx context.Context, fn func(context.Context)) {
	defer func() {
		if r := recover(); r != nil {
			slog.Default().Error("doger: sqltx: transaction callback panicked", "panic", fmt.Sprint(r))
		}
	}()
	fn(ctx)
}

// BeginTx begins a transaction on db with opts and returns a context
// holding it, and the transaction. A nil ctx means context.Background().
// It returns an error matching ErrNilDB for a nil db, and the error of
// db.BeginTx otherwise.
func BeginTx(ctx context.Context, db *sql.DB, opts *sql.TxOptions) (context.Context, *Tx, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil {
		return ctx, nil, ErrNilDB
	}
	raw, err := db.BeginTx(ctx, opts)
	if err != nil {
		return ctx, nil, fmt.Errorf("doger: sqltx: begin: %w", err)
	}
	txCtx, tx := bind(ctx, db, raw)

	return txCtx, tx, nil
}

// InTx runs fn with a context holding a new transaction on db (find it
// with [From]), commits when fn returns nil and returns the commit error,
// rolls back when fn returns an error and returns that error, and rolls
// back and re-panics when fn panics. It returns an error matching ErrNilDB
// for a nil db, without calling fn, and panics with "doger: sqltx: nil InTx
// function" for a nil fn.
func InTx(ctx context.Context, db *sql.DB, fn func(ctx context.Context) error) error {
	if fn == nil {
		panic(errors.New("doger: sqltx: nil InTx function"))
	}
	txCtx, tx, err := BeginTx(ctx, db, nil)
	if err != nil {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback()
			panic(r)
		}
	}()
	if err := fn(txCtx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return errors.Join(err, rbErr)
		}
		return err
	}

	return tx.Commit()
}

// BindTx returns a context holding a Tx around tx, a transaction another
// library began on db, without beginning one. The callbacks run only when
// the transaction ends through the returned Tx: committing tx directly
// skips them and leaves the context reporting an active transaction. db
// must be the *sql.DB tx was begun on (a *sql.Tx does not expose it, so a
// wrong one cannot be detected). It panics with "doger: sqltx: nil
// *sql.Tx" or "doger: sqltx: nil *sql.DB" for a nil argument.
func BindTx(ctx context.Context, db *sql.DB, tx *sql.Tx) (context.Context, *Tx) {
	if tx == nil {
		panic(errors.New("doger: sqltx: nil *sql.Tx"))
	}
	if db == nil {
		panic(errors.New("doger: sqltx: nil *sql.DB"))
	}
	if ctx == nil {
		ctx = context.Background()
	}

	return bind(ctx, db, tx)
}

// bind wraps raw and returns a context derived from ctx holding it.
func bind(ctx context.Context, db *sql.DB, raw *sql.Tx) (context.Context, *Tx) {
	tx := &Tx{tx: raw, db: db}
	txCtx := context.WithValue(ctx, txKey{}, tx)
	tx.ctx = context.WithoutCancel(txCtx)

	return txCtx, tx
}
