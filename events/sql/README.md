# eventsql

`github.com/kartaladev/doger/events/sql` (package `eventsql`) is a [doger](https://github.com/kartaladev/doger) event transport on PostgreSQL 13+ or MySQL 8.0+: the database your application already runs carries its events between processes, and a transactional outbox comes with it. It implements `doger.EventTransport` and `doger.TxEventSender`, and depends on doger and `events/sqltx` only: you open the `*sql.DB` with the driver of your choice (for example `github.com/jackc/pgx/v5/stdlib` or `github.com/go-sql-driver/mysql`).

```sh
go get github.com/kartaladev/doger/events/sql@latest
```

## Quick start

1. Create the tables with your migration tool, from the reference DDL (see [Schema](#schema)):

   ```go
   stmts, err := postgres.DDL("doger_") // or mysql.DDL
   ```

2. Build the transport and make it the container's transport:

   ```go
   db, err := sql.Open("pgx", dsn)
   transport, err := eventsql.New(db, postgres.Dialect())
   root := doger.NewWithOpts(&doger.InjectorOpts{EventTransport: transport})
   ```

3. Publish inside a transaction with `sqltx.InTx`: the outbox writes the event in that transaction, so it is delivered once the transaction commits and never when it rolls back (see [Outbox](#outbox)).

   ```go
   err := sqltx.InTx(ctx, db, func(ctx context.Context) error {
       tx, _ := sqltx.From(ctx)
       if _, err := tx.SQL().ExecContext(ctx, "INSERT INTO orders (id) VALUES ($1)", id); err != nil {
           return err
       }
       return doger.Publish(ctx, root, OrderPlaced{ID: id})
   })
   ```

Remote listeners (`doger.ListenerDelivery`) in any process using the same database then receive the event: one member of each `DeliverOnce` group, every `DeliverAll` group.

## Defaults and overrides

| Behavior | Default | Override |
|---|---|---|
| Dialect | None: a required argument (`ErrNilDialect`) | `postgres.Dialect()`, `mysql.Dialect()`, or your own `Dialect` |
| Table names | Prefix `doger_`: `doger_events`, `doger_event_deliveries`, `doger_event_groups` | `TablePrefix(p)`, validated by `New` (`ErrInvalidTableName`) |
| Schema creation | Never at run time | `postgres.DDL` / `mysql.DDL` for migrations; `EnsureSchema(ctx)` for development and tests |
| Poll interval when idle | 1s; a full batch claims again at once | `PollInterval(d)` |
| Claim batch size | 16 | `BatchSize(n)` |
| Handlers running at once per subscription | 4 | `Workers(n)` |
| Lease, and the handler's deadline | 30s | `Lease(d)` |
| Attempts before a dead letter | 10 | `MaxAttempts(n)` |
| Retry delay | Exponential from 1s, ×2, capped at 5m, ±10% jitter | `Backoff(policy)`, e.g. `FixedBackoff(d)` or `ExponentialBackoff{...}` (a `Max` of 0 means no cap) |
| A permanent handler error (`doger.PermanentError`) | Dead-lettered at once | Return a plain error to retry instead |
| Ephemeral groups | Removed when their subscription stops; reaped 5m after their last heartbeat after a crash | `EphemeralTTL(d)` |
| Durable groups (`DeliverOnce`, stable `DeliverAll`) | Kept while no instance runs, so events wait for them | `DropGroup(ctx, destination, group)` |
| Acknowledged deliveries | Deleted at once | `KeepDelivered(d)` keeps them, marked delivered, for d |
| Payload size | 1 MiB; larger sends fail with a permanent `ErrPayloadTooLarge` | `MaxPayloadBytes(n)` |
| Logging | `slog.Default()` | `Logger(l)` |

`New` never contacts the database and fails with `ErrNilDB`, `ErrNilDialect`, `ErrInvalidOption` (naming the option) or `ErrInvalidTableName`.

## Schema

The application owns the schema. `postgres.DDL(prefix)` and `mysql.DDL(prefix)` return the statements creating the three tables and their indexes; copy them into a migration of your migration tool (golang-migrate, Atlas, Flyway, ...) and review them against your data volumes and conventions before applying them. Check the schema at startup with `CheckSchema(ctx)`, which fails with `ErrSchemaMissing` naming the missing tables.

`EnsureSchema(ctx)` runs the same statements (`CREATE ... IF NOT EXISTS`) under a database lock, so concurrent callers are safe: use it in development and tests, never as your production migration. The transport never calls it and never alters a schema.

| Table | Holds |
|---|---|
| `<prefix>event_groups` | One row per registered group of a destination, with its heartbeat |
| `<prefix>events` | Each sent envelope, stored once |
| `<prefix>event_deliveries` | One row per event and group: its state, attempts, lease, last error |

The table prefix may name a schema (`TablePrefix("app.doger_")`); it must match `^([A-Za-z_][A-Za-z0-9_]*\.)?[A-Za-z_][A-Za-z0-9_]*$` and be at most 40 bytes, so no unvalidated identifier reaches SQL. Values always travel as bind parameters. On MySQL the tables use `utf8mb4_bin`, so destinations and group names are case-sensitive as on PostgreSQL.

## Delivery

Each `Send` stores the envelope once and one delivery row per group registered for its destination at the time of the write, in one transaction. A group is registered by its first `Subscribe`, so a new group starts at the latest event; a durable group stays registered while no member runs, and the events sent meanwhile wait for it.

Members of a group claim deliveries with `FOR UPDATE SKIP LOCKED` and a lease: each delivery is handled by one member at a time, members of a `DeliverOnce` group share the work, and each `DeliverAll` group receives every event. A subscription claims only when one of its workers is free, up to `BatchSize` deliveries at once; it polls every `PollInterval` when idle and at once after a full batch. Delivery is at least once and unordered: make listeners idempotent with the envelope ID.

The handler's context carries the attempt (`AttemptFromContext(ctx)`) and ends shortly before the lease ends, so a slow handler is cancelled before another member can claim the delivery. A crashed member's claims are claimed again once their lease expires. A result that arrives after the lease was lost is ignored and logged as `ErrLeaseLost`. Persisted times use the database clock, so leases are free of clock skew between your instances.

A handler error makes the delivery visible again after the `Backoff` delay for that attempt; after `MaxAttempts` attempts, or at once for an error wrapped with `doger.PermanentError`, the delivery is dead-lettered with the error text (made valid UTF-8 without NUL bytes, at most 2 KiB). Attempts are counted when a delivery is claimed: a delivery whose last attempt ended without a result (its member crashed) is dead-lettered when it is claimed again, without reaching the handler. A nil payload is stored and delivered as an empty one; a zero `PublishedAt` is delivered as the zero `time.Time`.

Stopping a subscription stops claiming, releases the deliveries it claimed but did not start, their attempt uncounted (other members claim them at once; so are the claims of a batch that waited for a worker past their lease), and waits for the running handlers until the stop context ends, then cancels them and returns within 100ms even if a handler ignores its context. A handler interrupted this way keeps its attempt counted and its delivery is claimable again at once, so a handler that always hangs is dead-lettered eventually. An ephemeral group is removed when its subscription stops, with its deliveries, in one transaction. Every subscription heartbeats its group. While at least one subscription runs, the transport runs one maintenance loop (not one per subscription): it removes an ephemeral group whose heartbeat is older than `EphemeralTTL`, after a crash, and deletes the delivered rows, the deliveries of groups that no longer exist (an outbox transaction committing after its ephemeral group stopped leaves them) and the events that are no longer needed. None of these goroutines remains once every subscription stopped and its handlers returned.

## Operations

**Dead letters.** List them per group, oldest first, with their envelope, attempts and last error, then resubmit the ones whose cause you fixed:

```go
letters, err := transport.ListDeadLetters(ctx, "orders.placed", "mail", 100)
for _, l := range letters {
    log.Printf("%s %s: %d attempts, %s", l.ID, l.Envelope.ID, l.Attempts, l.LastError)
}
err = transport.Resubmit(ctx, letters[0].ID) // pending again, attempts reset
```

A dead letter stays in `<prefix>event_deliveries` (state `dead`) until you resubmit it or drop its group; a resubmitted delivery goes through `MaxAttempts` attempts again.

**Dropping a group.** A durable group keeps receiving deliveries after its listener is removed from the code, and nothing consumes them. Remove it once no instance runs that listener any more:

```go
err := transport.DropGroup(ctx, "orders.placed", "mail")
```

It unregisters the group and deletes its pending and dead deliveries in one transaction; a member subscribing again registers it anew, at the latest event. Renaming a listener ID creates a new group: drop the old one.

**Stale groups.** During cleanup, the transport logs a warning (at most once an hour per transport) for every group whose oldest pending delivery is older than 24 hours: usually a removed listener whose group should be dropped, or a consumer that cannot keep up.

**Sizing.** Members of a group poll the same index; keep `BatchSize` small and `PollInterval` reasonable for many members. A subscription claims up to `BatchSize` deliveries as soon as one worker is free, and the claimed ones wait for its workers: size `BatchSize` to `Workers` so that the members of a group share the work fairly. Every `DeliverAll` instance adds a delivery row per event, so keep `DeliverAll` for small, cache-invalidation-sized events. `KeepDelivered(d)` keeps acknowledged rows: size the table for d.

## Outbox

The transport is a `doger.TxEventSender`: `SendTx` writes the event and its deliveries in the transaction that `events/sqltx` binds to the context, so they commit or roll back with your own writes. Its `TxSource()` is `sqltx.Source()`, so the container finds the same transactions: with the default `ExternalizeAuto`, a publish inside a transaction goes through the outbox, and `ListenerTxPhase(TxPhaseAfterCommit)` listeners run after the commit. Setting `InjectorOpts.TxSource = sqltx.Source()` as well is not a conflict.

```go
err := sqltx.InTx(ctx, db, func(ctx context.Context) error {
    // your writes, through the bound transaction
    tx, _ := sqltx.From(ctx)
    if _, err := tx.SQL().ExecContext(ctx, "UPDATE stock SET quantity = quantity - 1 WHERE sku = $1", sku); err != nil {
        return err // rolls back: the event is never delivered
    }
    return doger.Publish(ctx, root, StockReserved{SKU: sku}) // written in the same transaction
})
```

- Publish inside the function: an event published after `InTx` returned is outside the transaction.
- `SendTx` fails with `doger.ErrNoTransaction` when the context holds no active transaction, and with `sqltx.ErrForeignTransaction` when the transaction belongs to another `*sql.DB` than the transport's: writing the outbox on another database would break atomicity silently.
- A group registered after the outbox transaction wrote its event, even if the transaction commits later, does not receive it: the event was written before the group existed. This holds under READ COMMITTED (PostgreSQL's default), and on MySQL at its default REPEATABLE READ, where the fan-out's shared locks make the registration wait for the commit. A PostgreSQL outbox transaction at REPEATABLE READ or SERIALIZABLE fans out from its snapshot, so it can also miss a group registered after the snapshot was taken but before the event was written.

> **Warning: `sqltx.BindTx`.** `BindTx` adopts a `*sql.Tx` that another library began. End the transaction through the returned `sqltx.Tx` (`tx.Commit()`, `tx.Rollback()`): committing the raw `*sql.Tx` commits the database transaction, but the after-commit listeners never run and the context keeps reporting an active transaction. Prefer `sqltx.InTx`, which cannot be misused this way.
