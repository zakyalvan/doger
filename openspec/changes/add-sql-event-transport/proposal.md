# Proposal

## Why

`add-event-transport` defines how doger events cross process boundaries but ships only an in-memory transport. Running several instances, or several applications, needs a transport that survives crashes. The maintainers chose a first-party one as the supported default, so production use does not depend on watermill or msgin (see `add-event-transport` proposal and the exploration behind it). Most doger applications already run a relational database, so the database they already operate becomes the event transport, with a transactional outbox for free.

## What Changes

**Two new modules**, each depending only on doger and the standard library; the caller injects the `*sql.DB` and its driver:
- `github.com/kartaladev/doger/events/sqltx` (directory `events/sqltx`, package `sqltx`): the shared transaction helper, also used by the watermill (`add-watermill-event-transport`) and msgin (`add-msgin-event-transport`) adapters.
- `github.com/kartaladev/doger/events/sql` (directory `events/sql`, package `eventsql`): the SQL transport, which imports `sqltx`.

**Transport**
- `eventsql.New(db, dialect, opts...) (*Transport, error)` returns a `doger.EventTransport` that is also a `doger.TxEventSender`.
- Dialects `postgres.Dialect()` (PostgreSQL 13+) and `mysql.Dialect()` (MySQL 8.0+), in packages `events/sql/postgres` and `events/sql/mysql`. They hold SQL text only and import no driver. The dialect is passed explicitly; there is no driver detection.
- Storage: a group registry and one delivery row per registered group of the destination, written in the same transaction as the event (the event insert, an `INSERT ... SELECT` fan-out from the registry, and the delete of an event no group receives). New groups therefore start at the latest event, as `add-event-transport` requires.
- Consumers claim rows with a lease and `FOR UPDATE SKIP LOCKED`, so members of a `DeliverOnce` group compete and every group gets its own copy. A crash releases a claim when its lease expires.
- Retries with exponential backoff and jitter; dead-lettering after the last attempt (also at claim time for a delivery whose attempts a crash used up) or at once for `doger.PermanentError`; admin functions to list and resubmit dead letters and to drop a group.
- Ephemeral `DeliverAll` groups are removed when their subscription stops, and by a heartbeat-based reaper after a crash.

**Transactions (`sqltx`) and the outbox**
- `*sql.Tx` has no commit hooks, so `sqltx` wraps it in `sqltx.Tx` (implements `doger.Tx`: `SQL()`, `DB()`, `Commit()`, `Rollback()`, `OnCommit`, `OnRollback`), created by `sqltx.BeginTx(ctx, db, opts)`, `sqltx.InTx(ctx, db, fn)`, or `sqltx.BindTx(ctx, db, tx)` for transactions another library began. `sqltx.From(ctx)` finds the active one and `sqltx.Source()` is the matching `doger.TxSource`.
- The transport's `TxSource()` returns `sqltx.Source()`. `SendTx` inserts the event with the transaction of `sqltx.FromDB(ctx, db)`: a rollback leaves nothing, a commit makes it deliverable. A transaction on another `*sql.DB` fails with `sqltx.ErrForeignTransaction`, the one error all three SQL-based adapters share.

**Schema**
- The caller owns the schema: `postgres.DDL(prefix)` and `mysql.DDL(prefix)` return reference statements for the caller's migration tool, and the optional `(*Transport).EnsureSchema(ctx)` creates them in development and tests.
- Table names come from a validated prefix (`TablePrefix`, default `doger_`), so no unvalidated identifier reaches SQL.

**Tests and CI**
- The module passes `eventtransporttest.Run`, including `WithTx`, against real PostgreSQL and MySQL. Integration tests read DSNs from the environment; `make test-sql` runs them with Docker Compose, locally and in a new CI job.

**Out of scope:** SQLite and other engines (no `SKIP LOCKED`), `LISTEN/NOTIFY` push delivery (needs a driver-specific API), ordering guarantees, and exactly-once delivery.

## Capabilities

### New Capabilities
- `sql-event-transport`: sending (including `SendTx` in the caller's transaction), the group registry, claiming, leases, retries, dead letters, ephemeral groups, cleanup, admin functions and options of the SQL transport.
- `sql-transactions`: the `sqltx` module: binding `database/sql` transactions to a context, commit and rollback callbacks, `From`, and the `doger.TxSource` it provides.
- `sql-event-schema`: dialects, table-name validation, reference DDL and `EnsureSchema`.

### Modified Capabilities
- None. The changes are stacked: this one builds on `add-event-transport`, which is implemented in the same working tree but not yet archived, and must be archived first. The module implements its `event-transport` contract without changing it.

## Impact

- Code: new module `events/sqltx` (`tx.go`, `context.go`); new module `events/sql` (`transport.go`, `send.go`, `outbox.go`, `subscribe.go`, `maintenance.go`, `lease.go`, `backoff.go`, `rows.go`, `dialect.go`, `schema.go`, `admin.go`, `options.go`), packages `events/sql/postgres` and `events/sql/mysql`; two `go.work` entries; `scripts/release-steps.sh` module list if it is not derived from `go.work`. `events/sql` requires `events/sqltx` at the same release version.
- Dependencies of the published modules: doger (and `sqltx` for `events/sql`) only. `sqltx` test-only: `github.com/stretchr/testify` and `go.uber.org/goleak` (it uses a fake driver, no database). `events/sql` test-only: `github.com/jackc/pgx/v5` (its `stdlib` driver) and `github.com/go-sql-driver/mysql`, dropped from the released `go.mod` by the existing release step.
- CI: new job `sql-integration` (ubuntu) running `make test-sql`, which starts `events/sql/compose.yaml` (`postgres:17.12-alpine`, `postgres:13.23-alpine`, `mysql:8.4.13`, `mysql:8.0.46`, raised `max_connections`); Dependabot's root run ignores `pgx` and `go-sql-driver/mysql`, which the `events/**` run covers.
- Docs: module README, a section in the "Event transports" guide, `Example*` tests.
- API: new modules, no change to doger's exported API.

## Defaults & Control

| Behavior | Zero-config default | Consumer control |
|---|---|---|
| Dialect | None: required argument (`ErrNilDialect` otherwise) | `postgres.Dialect()`, `mysql.Dialect()`, or a custom `Dialect` |
| Table names | Prefix `doger_` | `TablePrefix(p)`, validated at `New` |
| Schema creation | Never at runtime | `postgres.DDL`/`mysql.DDL` for migrations; `EnsureSchema(ctx)` for dev and tests |
| Poll interval when idle | 1s | `PollInterval(d)` |
| Claim batch size | 16 | `BatchSize(n)` |
| Handlers per subscription | 4 | `Workers(n)` |
| Lease (and handler deadline) | 30s | `Lease(d)` |
| Max attempts before dead letter | 10 | `MaxAttempts(n)` |
| Backoff | `DefaultBackoff()`: exponential from 1s, ×2, capped at 5m, ±10% jitter | `Backoff(policy)` with a `BackoffPolicy`, e.g. `ExponentialBackoff{...}` (`Max <= 0`: no cap) or `FixedBackoff(d)` |
| Permanent handler error | Dead-lettered at once | Listener returns a non-permanent error to retry instead |
| Ephemeral group lifetime | Removed on stop; reaped 5m after its last heartbeat | `EphemeralTTL(d)` |
| Durable (`DeliverOnce`, stable `DeliverAll`) groups | Kept while no instance runs, so events wait | `(*Transport).DropGroup(ctx, destination, group)` |
| Acknowledged deliveries | Deleted at once | `KeepDelivered(d)` to keep them for auditing |
| Payload size | 1 MiB maximum, larger sends fail permanently | `MaxPayloadBytes(n)` |
| Logging | `slog.Default()` | `Logger(*slog.Logger)` |
| Transaction binding | None: plain `Send` | `sqltx.BeginTx`, `sqltx.InTx`, `sqltx.BindTx` |
| Transaction lookup for doger | The transport's `TxSource()` = `sqltx.Source()` | `InjectorOpts.TxSource` (setting `sqltx.Source()` there too is not a conflict) |
| Callback panics in `sqltx.Tx` | Recovered and logged through `slog.Default()`; other callbacks still run | None: a callback panic must never undo a committed transaction's bookkeeping; the callback's own code handles its errors |
| Same-database check for `SendTx` | Enforced by `sqltx.FromDB`: `sqltx.ErrForeignTransaction` when `Tx.DB()` differs | None: writing the outbox on another database would break atomicity silently |
