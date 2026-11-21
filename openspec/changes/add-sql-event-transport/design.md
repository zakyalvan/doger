# Design

## Context

See proposal.md (Why) and the three specs. The contract is `add-event-transport` (archived first): `EventTransport` (`Send`, `Subscribe` returning a stop function), `TxEventSender` (`SendTx`, `TxSource`), the handler's return value as the acknowledgement (nil ack, error retry, `doger.IsPermanentError` dead-letter), groups named by doger (`DeliverOnce` shared, `DeliverAll` per instance, `Ephemeral` for generated instance IDs), new groups starting at the latest event, and the `eventtransporttest` suite.

Repository constraints:
- Separate modules follow `scheduler/gocron`: own `go.mod` at Go 1.27, listed in `go.work`, released by `scripts/release-steps.sh`, which already drops test-only requirements from the released `go.mod` and pins released siblings to the release version (`events/sql` requires `events/sqltx`).
- CI (`test.yml`) runs `make test-all` on ubuntu, macOS and Windows. Only ubuntu runners support service containers.
- msgin's SQL adapter (lease/claim with `delivery_count` and a fence token, `SKIP LOCKED`, DB-server time for persisted timestamps) and watermill-sql (log plus per-group offsets, with new groups seeded at offset 0) were studied during the exploration.

## Goals / Non-Goals

**Goals:**
- At-least-once delivery that survives crashes, with a crash costing at most one lease of delay.
- Correct competing consumers and per-group copies on PostgreSQL 13+ and MySQL 8.0+.
- No driver import and no goroutine outside running subscriptions.

**Non-Goals:**
- Push delivery (`LISTEN/NOTIFY`); polling only, with an immediate re-poll after a full batch.
- Ordering within a group (claims are oldest first, but parallel workers and retries reorder).
- Automatic schema migration in production.

## Decisions

### D1. Storage model: fan-out at write over a group registry
```
doger_event_groups      (destination, group_name, ephemeral, heartbeat_at)        PK (destination, group_name)
doger_events            (id, destination, event_type, version, content_type,
                         payload, headers, published_at)                          PK id (envelope ID)
doger_event_deliveries  (seq, event_id, group_name, state, attempts,
                         visible_after, lease_owner, lease_token, lease_until,
                         last_error, delivered_at)                               PK seq; UNIQUE (event_id, group_name)
                         index (group_name, state, visible_after, seq)
```
- `Send` runs, in one transaction (its own, or the caller's for `SendTx`): `INSERT` into events, then `INSERT INTO deliveries SELECT ... FROM groups WHERE destination = ?`. With no group, the event row is deleted in the same statement batch (PostgreSQL: a CTE that inserts the event only `WHERE EXISTS` a group; MySQL: insert, then delete when the second insert affected 0 rows).
- **Why not a log plus per-group offsets** (watermill-sql): offsets give ordering and cheap fan-out, but a group's single offset serializes its members (watermill locks the offset row with `FOR UPDATE`), so `DeliverOnce` members do not run in parallel; per-message retries, attempts and dead letters need extra tables anyway; and a new group must be seeded at the head explicitly. Fan-out rows make every delivery independently claimable, retriable and dead-letterable, and "start at latest" is a consequence of the data model rather than a rule each code path must remember.
- **Why not one row per destination with a `processed_by` set** (msgin-style queue): it supports one group per queue; multiple groups need multiple queues the publisher must know about, which the topic/log model of `add-event-transport` rules out.
- Cost: a payload stored once, one small delivery row per group. Write amplification grows with the number of groups per destination, which is small in practice (one per listener, plus one per instance for `DeliverAll`).

### D2. The registration race
A group must see every event whose write runs after the group's registration committed. The `INSERT ... SELECT` reads the registry at statement time under READ COMMITTED (PostgreSQL default; MySQL 8 InnoDB `INSERT ... SELECT` takes shared next-key locks on the scanned registry rows, which serializes it against a concurrent registration of that destination). Consequences, pinned by the scenario "Registration racing a send":
- An outbox transaction that wrote before a registration committed does not reach that group, even if it commits later. This matches "the group starts at the latest event": the event was written before the group existed.
- No event can be half fanned out: deliveries of one event come from one statement.
Considered: `SERIALIZABLE` sends. Rejected: it would impose the isolation level on the caller's outbox transaction.

### D3. Claiming
- PostgreSQL: `UPDATE deliveries SET lease_owner=$owner, lease_token=$token, lease_until=now()+$lease, attempts=attempts+1 WHERE seq IN (SELECT seq FROM deliveries WHERE group_name=$g AND state='pending' AND visible_after<=now() AND (lease_until IS NULL OR lease_until<now()) ORDER BY seq LIMIT $n FOR UPDATE SKIP LOCKED) RETURNING ...` joined to events.
- MySQL: the same `SELECT ... FOR UPDATE SKIP LOCKED` and `UPDATE` in one short transaction (no `RETURNING`).
- Persisted times use the database clock (`now()`, `UTC_TIMESTAMP(6)`), so lease expiry is free of app-to-DB clock skew. The app clock only drives polling.
- `lease_token` is a random 128-bit value per claim. Ack (`DELETE ... WHERE seq=? AND lease_token=?`), nack and dead-letter updates check it; 0 rows affected means the lease was lost (`ErrLeaseLost`, logged, never retried).
- Attempts count at claim time, so a crash still counts. `AttemptFromContext(ctx)` exposes the attempt to handlers.
- The handler context gets a deadline at `lease_until` (computed from the claim's returned DB time minus a 1s margin), so a slow handler is cancelled before another member can claim the row.

### D4. Retry, backoff, dead letters
- Nack: `state='pending', visible_after=now()+backoff(attempts), lease_*=NULL, last_error=$err` (error text truncated to 2 KiB).
- Dead letter: `state='dead'` when `attempts >= MaxAttempts` or `doger.IsPermanentError(err)`.
- `Backoff` is an interface `Delay(attempt int) time.Duration`. Default `ExponentialBackoff{Initial: 1s, Factor: 2, Max: 5m, Jitter: 0.1}`; `FixedBackoff(d)` for tests. Jitter uses `math/rand/v2` (not security-relevant).
- Admin: `ListDeadLetters`, `Resubmit` (`state='pending', attempts=0, visible_after=now()`), `DropGroup` (delete deliveries and the registry row in one transaction).

### D5. Subscription runtime
```
Subscribe(ctx, sub, h):
  upsert group (destination, group, ephemeral, heartbeat=now())
  start poller goroutine + Workers(n) worker goroutines, joined by a WaitGroup
poller loop:
  claim up to min(BatchSize, free workers) --> send to workers (unbuffered channel)
  batch full ? poll again now : wait PollInterval (time.NewTimer + Reset) or ctx.Done
  every PollInterval*10: heartbeat own group; reap ephemeral groups older than EphemeralTTL;
                         delete delivered rows older than KeepDelivered and orphan events
stop(ctx):
  cancel poller --> release leases of claimed-but-not-started deliveries
  wait workers or ctx.Done (then cancel handler contexts and wait for them to return)
  if ephemeral: delete deliveries + registry row
```
- Claimed rows never wait in a buffer longer than a lease: the poller claims only as many rows as workers are free, so "claimed but not started" is at most one batch, released on stop.
- Cleanup piggybacks on running subscriptions, so the transport owns no goroutine of its own and needs no `Close`. Several members may clean concurrently; deletes are idempotent.
- Every goroutine selects on its context; `stop` returns only after all of them exit (goleak-checked).

### D6. Transactions without hooks: the shared `sqltx` module
`*sql.Tx` exposes no commit callbacks, and doger's `Tx` needs `OnCommit`/`OnRollback`. Options considered:
- (a) **A wrapper type the application commits through.** Chosen: explicit, driver-neutral, and any code that takes a `*sql.Tx` keeps working through `tx.SQL()`.
- (b) A custom `driver.Connector` wrapping the user's driver to observe commits. Rejected: invasive (the user must open the DB through us), and pooled connections make the mapping from commit to context fragile.
- (c) No hooks: support only `ExternalizeOutbox` and drop after-commit listeners for SQL users. Rejected: `ListenerTxPhase` would silently fall back to "no transaction".

The wrapper lives in its own module, `github.com/kartaladev/doger/events/sqltx`, because three transports need the same transaction for the outbox and doger needs one `TxSource` for all of them: this SQL transport, the watermill `wmsql` module (`add-watermill-event-transport`) and the msgin adapter (`add-msgin-event-transport`). Two wrappers would give an application two context keys, and doger's `ErrTxSourceConflict` check would reject the mix.

```go
package sqltx

type Tx struct{ /* *sql.Tx, *sql.DB, mu, callbacks, ended atomic.Bool */ }
func (t *Tx) SQL() *sql.Tx
func (t *Tx) DB() *sql.DB
func (t *Tx) Commit() error
func (t *Tx) Rollback() error
func (t *Tx) OnCommit(func(context.Context))
func (t *Tx) OnRollback(func(context.Context))

func BeginTx(ctx context.Context, db *sql.DB, opts *sql.TxOptions) (context.Context, *Tx, error)
func InTx(ctx context.Context, db *sql.DB, fn func(ctx context.Context) error) error
func BindTx(ctx context.Context, db *sql.DB, tx *sql.Tx) (context.Context, *Tx)
func From(ctx context.Context) (*Tx, bool)
func FromDB(ctx context.Context, db *sql.DB) (*Tx, error) // doger.ErrNoTransaction or ErrForeignTransaction
func Source() doger.TxSource
var ErrNilDB, ErrForeignTransaction error
```
- Callbacks are kept in a mutex-guarded slice; `ended` is an `atomic.Bool`. `Commit` runs the SQL commit, then the `OnCommit` callbacks, or the `OnRollback` callbacks if the commit failed. Each callback is panic-recovered and logged through `slog.Default()`; the callbacks receive `context.WithoutCancel` of the context the transaction was bound to.
- `From` and `Source().Lookup` return false once `ended` is set. `Detach` uses `context.WithoutCancel` plus a context value that masks the transaction key.
- `Source()` returns a zero-size comparable value, so configuring `sqltx.Source()` both in `InjectorOpts.TxSource` and through a transport is the same source, not an `ErrTxSourceConflict`.
- `InTx` passes only the context; `fn` gets the `*sql.Tx` with `sqltx.From(ctx)` and `.SQL()`. This keeps the signature identical for all three adapters and makes the transaction reachable from any layer that already takes a context.
- Panics use the `doger: sqltx: ` prefix.
- `FromDB(ctx, db)` is the one same-database check for every SQL-based adapter (this transport, watermill `wmsql`, msgin), so they all return `sqltx.ErrForeignTransaction` instead of three different errors. `SendTx` is `FromDB(ctx, t.db)` plus the write; `eventsql` declares no error of its own for it (nothing is released, so no alias is kept). `BindTx` takes the `*sql.DB` from the caller because a `*sql.Tx` does not expose it; a wrong value cannot be detected and is documented, as msgin documents the same invariant.

### D7. Dialects and identifiers
- `type Dialect interface { Name() string; Statements(t Tables) Statements; DDL(t Tables) []string; SchemaLockSQL() (lock, unlock string) }` where `Statements` is a struct of prepared SQL strings (send, claim, ack, nack, dead, release, heartbeat, reap, cleanup, admin). Exported so other engines with `SKIP LOCKED` (for example MariaDB 10.6+) can be added outside the module.
- Identifiers: the prefix is validated (regex and length in the spec) before any SQL is built, then each identifier is quoted by the dialect (`"..."` for PostgreSQL, backticks for MySQL). Values always travel as bind parameters.
- `EnsureSchema` serializes concurrent callers with `pg_advisory_xact_lock(hashtext(prefix))` on PostgreSQL and `GET_LOCK(prefix, 10)` on MySQL, then runs `CREATE ... IF NOT EXISTS`. `CHECK` constraints are avoided for MySQL 8.0.15 and older compatibility; `state` is a short `VARCHAR`.
- The reference DDL is a starting point for the consumer's migration, reviewed by them against their data volumes; the module never alters a schema in production code paths.

### D8. Test infrastructure
- Integration tests live in the module (package `eventsql_test`) and import `github.com/jackc/pgx/v5/stdlib` and `github.com/go-sql-driver/mysql` as test-only requirements, which the existing release step removes from the published `go.mod`.
- DSNs come from `DOGER_SQL_POSTGRES_DSN` and `DOGER_SQL_MYSQL_DSN`. Unset, the tests skip, unless `DOGER_SQL_REQUIRE=1`, which turns a missing DSN into a failure. Each test creates its own schema (PostgreSQL) or database (MySQL) with a random name and drops it after, so tests run in parallel.
- CI: a new `sql-integration` job on ubuntu with service containers `postgres:17`, `postgres:13`, `mysql:8.4`, `mysql:8.0` and `DOGER_SQL_REQUIRE=1`. The existing matrix keeps running the unit tests on all three OSes.
- Local: `make test-sql` brings up `events/sql/compose.yaml` with the same images, exports the DSNs and runs `go test -race ./...` in the module.
- *Alternative: testcontainers-go.* Rejected for now: a heavy test-only dependency tree, Docker access from inside tests on macOS and Windows runners, and slower startup per package. The env-DSN approach also lets users point the suite at a managed database.
- Unit tests that need no engine (options, prefix validation, backoff, the `Tx` wrapper's callback logic, statement building) use a minimal fake `database/sql/driver` in `fakedriver_test.go`.

## Risks / Trade-offs

- [Polling latency up to `PollInterval` when idle] → 1s default, configurable; a full batch re-polls at once. `LISTEN/NOTIFY` can come later as an optional dialect capability without changing these specs.
- [Hot rows: many members polling the same group index] → `SKIP LOCKED`, small batches, and the composite index; documented sizing guidance in the README.
- [Write amplification for many `DeliverAll` instances] → one small delivery row per group per event; the guide recommends `DeliverAll` only for cache-invalidation-sized events.
- [`sqltx.BindTx` misuse: committing the raw `*sql.Tx` skips callbacks and leaves the context reporting an active transaction] → godoc and the spec's last row; `sqltx.InTx` is the recommended entry point.
- [A shared module is a contract for three adapters] → its API is fixed by this change and covered by its own tests; the other two changes only import it.
- [Stale non-ephemeral groups keep accumulating deliveries after a listener is removed from code] → `DropGroup` and a README runbook; the transport logs a warning when a group's oldest pending delivery is older than 24h (cheap check during cleanup).
- [A long outbox transaction written before a registration commits misses the new group] → inherent to "start at latest"; documented in D2.

## Public API & Defaults

New modules `github.com/kartaladev/doger/events/sqltx` (package `sqltx`) and `github.com/kartaladev/doger/events/sql` (package `eventsql`). No change to doger's API.

| Identifier | Kind | Default / override |
|---|---|---|
| `New(db *sql.DB, d Dialect, opts ...Option) (*Transport, error)` | constructor | n/a |
| `sqltx.Tx` (`SQL`, `DB`, `Commit`, `Rollback`, `OnCommit`, `OnRollback`) | type (`doger.Tx`) | n/a |
| `sqltx.BeginTx(ctx, db, opts)`, `sqltx.InTx(ctx, db, fn)`, `sqltx.BindTx(ctx, db, tx)`, `sqltx.From(ctx)`, `sqltx.FromDB(ctx, db)`, `sqltx.Source()`, `sqltx.ErrNilDB`, `sqltx.ErrForeignTransaction` | funcs, sentinels | callbacks logged through `slog.Default()` |
| `Transport.Send`, `Subscribe`, `SendTx`, `TxSource` (= `sqltx.Source()`) | `doger.TxEventSender` methods | n/a |
| `Transport.EnsureSchema(ctx)`, `CheckSchema(ctx)` | methods | never called implicitly |
| `Transport.ListDeadLetters(ctx, dest, group, limit) ([]DeadLetter, error)`, `Resubmit(ctx, ids ...string)`, `DropGroup(ctx, dest, group)` | methods | n/a |
| `AttemptFromContext(ctx) (int, bool)` | func | n/a |
| `TablePrefix`, `PollInterval`, `BatchSize`, `Workers`, `Lease`, `MaxAttempts`, `Backoff`, `EphemeralTTL`, `KeepDelivered`, `MaxPayloadBytes`, `Logger` | `Option` | `doger_`, 1s, 16, 4, 30s, 10, exponential 1s..5m ±10%, 5m, 0 (delete), 1 MiB, `slog.Default()` |
| `Backoff` interface, `ExponentialBackoff`, `FixedBackoff(d)` | policy | exponential |
| `Dialect`, `Tables`, `Statements` | SPI | n/a |
| `postgres.Dialect()`, `postgres.DDL(prefix)`, `mysql.Dialect()`, `mysql.DDL(prefix)` | funcs | n/a |
| `ErrNilDB`, `ErrNilDialect`, `ErrInvalidOption`, `ErrInvalidTableName`, `ErrSchemaMissing`, `ErrPayloadTooLarge`, `ErrLeaseLost` | sentinels | n/a |

## Test Strategy

Each scenario maps to one Go test; `Examples` rows run as named subtests. Engine rows run once per configured engine as subtests named by engine (`/postgres17`, `/mysql84`, ...). Everything runs under `-race`; `TestMain` runs goleak. Time-dependent tests use short options (`PollInterval(20ms)`, `FixedBackoff(10ms)`, `Lease(200ms)`) and wait with polling helpers bounded by `testWithTimeout`-style deadlines, never fixed sleeps for assertions.

**`specs/sql-event-schema/spec.md`**

| Scenario | Test | Kind |
|---|---|---|
| Constructor arguments | `options_test.go: TestNew_Arguments/<row>` | table-driven, fake driver |
| Prefix validation | `options_test.go: TestNew_TablePrefix/<row>` | table-driven |
| DDL output | `postgres/ddl_test.go: TestDDL/<row>`, `mysql/ddl_test.go: TestDDL/<row>` | table-driven |
| DDL applied by a migration tool | `integration_schema_test.go: TestDDL_Conformance/<engine>` | integration |
| EnsureSchema and CheckSchema | `integration_schema_test.go: TestEnsureSchema/<row>` | integration |
| Concurrent EnsureSchema | `integration_schema_test.go: TestEnsureSchema_Concurrent` | integration, race |

**`specs/sql-event-transport/spec.md`**

| Scenario | Test | Kind |
|---|---|---|
| Conformance on each engine | `integration_conformance_test.go: TestConformance/<engine>` | conformance, race, goleak |
| Deliveries per group | `integration_send_test.go: TestSend_DeliveriesPerGroup/<row>` | integration |
| Send failures are returned | `integration_send_test.go: TestSend_Failures/<row>` | integration |
| Registration and history | `integration_groups_test.go: TestGroups_History/<row>` | integration |
| Registration racing a send | `integration_groups_test.go: TestGroups_RegistrationRace` | integration, two connections |
| Two instances | `integration_compete_test.go: TestCompete_TwoInstances` | integration, race |
| Workers bound handler concurrency | `integration_compete_test.go: TestCompete_Workers/<row>` | integration |
| Lease outcomes | `integration_lease_test.go: TestLease/<row>` | integration |
| Attempts are visible to the handler | `integration_lease_test.go: TestLease_AttemptInContext` | integration |
| Retry outcomes | `integration_retry_test.go: TestRetry/<row>` | integration |
| The default backoff | `backoff_test.go: TestExponentialBackoff_Default/<row>` | table-driven, seeded jitter |
| Admin operations | `integration_admin_test.go: TestAdmin/<row>` | integration |
| Ephemeral group lifecycle | `integration_groups_test.go: TestGroups_Ephemeral/<row>` | integration |
| Stop outcomes | `integration_stop_test.go: TestStop/<row>` | integration, goleak |
| No goroutine after stop | `integration_stop_test.go: TestStop_NoGoroutine` | goleak |
| Retention | `integration_retention_test.go: TestRetention/<row>` | integration |

**`specs/sql-transactions/spec.md`** (module `events/sqltx`, all unit tests on a fake `database/sql/driver`; no database needed)

| Scenario | Test | Kind |
|---|---|---|
| Beginning a transaction | `sqltx/tx_test.go: TestBeginTx/<row>` | table-driven, fake driver |
| Callback outcomes | `sqltx/tx_test.go: TestTx_Callbacks/<row>` | table-driven, fake driver |
| A callback registered after the end | `sqltx/tx_test.go: TestTx_LateCallback` | unit |
| Callbacks registered concurrently | `sqltx/tx_test.go: TestTx_ConcurrentCallbacks` | race |
| InTx outcomes | `sqltx/tx_test.go: TestInTx/<row>` | table-driven, fake driver |
| InTx argument errors | `sqltx/tx_test.go: TestInTx_Arguments/<row>` | table-driven |
| Binding an existing transaction | `sqltx/tx_test.go: TestBindTx/<row>` | table-driven, fake driver |
| BindTx argument panics | `sqltx/tx_test.go: TestBindTx_Panics/<row>` | table-driven |
| Lookup and Detach | `sqltx/context_test.go: TestFromAndSource/<row>` | table-driven |
| FromDB outcomes | `sqltx/context_test.go: TestFromDB/<row>` | table-driven, fake driver |
| Deferred listeners through sqltx | `sqltx/doger_test.go: TestDeferredListeners/<row>` | integration with a doger root, fake driver |

**`specs/sql-event-transport/spec.md`, outbox requirement** (module `events/sql`)

| Scenario | Test | Kind |
|---|---|---|
| Outbox writes | `integration_tx_test.go: TestSendTx/<row>` | integration |
| The outbox through doger | `integration_tx_test.go: TestOutbox_EndToEnd/<row>` | integration, two doger roots |
| The same TxSource configured twice is not a conflict | `integration_tx_test.go: TestTxSource_SameSourceTwice` | unit (fake driver) |

**Examples (godoc):** `sqltx`: `ExampleInTx`, `ExampleBindTx` (runnable with the fake driver, with `// Output:`). `eventsql`: `ExampleNew`, `ExampleTransport_SendTx`, `ExampleTransport_EnsureSchema` in `example_test.go`, compiled always and run only when a PostgreSQL DSN is set (no `// Output:` comment, so they compile-check in the unit matrix).

**Hot paths and their tests:**
- Send fan-out (`INSERT ... SELECT`, no-group delete): `TestSend_DeliveriesPerGroup`, `TestGroups_RegistrationRace`, conformance.
- Claim, ack, nack, dead-letter with the lease token: `TestCompete_TwoInstances`, `TestLease`, `TestRetry`, conformance.
- Poller and worker lifecycle: `TestCompete_Workers`, `TestStop`, `TestStop_NoGoroutine`.
- `sqltx` callbacks and lookup: `TestTx_Callbacks`, `TestTx_ConcurrentCallbacks`, `TestFromAndSource`, `TestFromDB`, `TestDeferredListeners`; outbox through it: `TestSendTx`, `TestOutbox_EndToEnd`.
- Identifier validation (security): `TestNew_TablePrefix`, `TestDDL`.

## Migration Plan

New module; nothing to migrate. Adoption: apply `postgres.DDL("doger_")` (or MySQL) through the application's migration tool, set `InjectorOpts.EventTransport` to `eventsql.New(db, postgres.Dialect())`, and wrap writes in `sqltx.InTx(ctx, db, fn)`. Rollback: unset the transport; pending rows stay in the tables and are delivered when it is set again.

## Open Questions

- Whether to ship a MariaDB dialect (10.6+ has `SKIP LOCKED`) in this module or leave it to the `Dialect` SPI; it adds a requirement, not a change to these.
- Metrics (pending, dead, lease losses): a hook or `slog` attributes are enough for now; a metrics interface can follow when doger defines one.
