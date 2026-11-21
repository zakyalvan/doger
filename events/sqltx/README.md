# sqltx

`github.com/kartaladev/doger/events/sqltx` (package `sqltx`) binds `database/sql` transactions to a context so that a [doger](https://github.com/kartaladev/doger) container can find them. A `*sql.Tx` has no commit hooks, so `sqltx.Tx` wraps it with `OnCommit` and `OnRollback` callbacks and implements `doger.Tx`; `sqltx.Source()` is the matching `doger.TxSource`. It depends on doger and the standard library only; your application opens the `*sql.DB` with the driver of its choice.

```sh
go get github.com/kartaladev/doger/events/sqltx@latest
```

It is shared by every SQL-based doger event transport: the SQL transport (`events/sql`), the watermill `wmsql` module and the msgin adapter. They all find the transaction through this module, so an application has one context key and one `TxSource`, and they all reject an outbox write in a transaction of another `*sql.DB` with the same `sqltx.ErrForeignTransaction`.

## Usage

```go
root := doger.NewWithOpts(&doger.InjectorOpts{TxSource: sqltx.Source()})

err := sqltx.InTx(ctx, db, func(ctx context.Context) error {
    tx, _ := sqltx.From(ctx)
    if _, err := tx.SQL().ExecContext(ctx, "UPDATE stock SET quantity = quantity - 1 WHERE id = $1", id); err != nil {
        return err // rolls back
    }
    return doger.Publish(ctx, root, OrderPlaced{ID: id}) // after-commit listeners and the outbox see the transaction
})
```

| Function | What it does |
|---|---|
| `InTx(ctx, db, fn)` | Recommended. Runs `fn` in a new transaction: commits on nil, rolls back on an error (returned) or a panic (re-panicked) |
| `BeginTx(ctx, db, opts)` | Begins a transaction and returns a context holding it; end it with `tx.Commit()` or `tx.Rollback()` |
| `BindTx(ctx, db, tx)` | Adopts a `*sql.Tx` another library began on `db` |
| `From(ctx)` | The active transaction of `ctx`, until it ends |
| `FromDB(ctx, db)` | As `From`, but fails with `doger.ErrNoTransaction` or `ErrForeignTransaction` |
| `Source()` | The `doger.TxSource`; the same comparable value every time |

## Defaults

| Behavior | Default | Control |
|---|---|---|
| Transaction binding | None: nothing is bound until you call `InTx`, `BeginTx` or `BindTx` | Choose the entry point |
| Callbacks | Run after the commit (or after the rollback, or a failed commit), in registration order, each once, with a context that is not cancelled | — |
| A panicking callback | Recovered and logged through `slog.Default()`; the other callbacks still run | Handle errors inside the callback |
| A callback registered after the end | Never runs; logged through `slog.Default()` | — |
| A second `Commit` or `Rollback` | Returns `sql.ErrTxDone` and runs nothing | — |

## Pitfall: ending the raw transaction

Always end a transaction through the `sqltx.Tx`. Committing the `*sql.Tx` returned by `SQL()`, or the one you passed to `BindTx`, commits the database transaction but the wrapper never learns of it: the callbacks do not run and the context still reports an active transaction. `InTx` avoids the mistake.
