# Design

## Context

See proposal.md (Why) and `specs/msgin-event-transport/spec.md`. The contract is `add-event-transport` (design D1, D5, D6, D8, D10; spec `event-transport`), which must be archived first. Facts about msgin below were read from its source at commit `0c820e18975de76f9cf8e4a8929cad78ca665750` (2026-08-28, `main`, untagged):

- **SPI** (`spi.go`): `OutboundAdapter.Send(ctx, Message[any])`, `PollingSource.Poll`, `Delivery{Msg, Ack, Nack(requeue, delay)}`, `TopicPublisher`/`TopicSubscriber` (implemented only in-process by `channel.PubSub`).
- **SQL adapter** (`adapter/database/sql`, root module): `NewOutboundAdapter(db, table, LeaseDialect, opts...)`, `NewPollingSource(db, table, LeaseDialect, opts...)`, `WithSharedTransaction(TransactionResolver)` (strict: `ErrNoSharedTransaction` when the resolver finds nothing), `TransactionResolver func(ctx) (Querier, error)`, `InboxDeduper.MarkProcessed(ctx, *sql.Tx, msgID) (already bool, err)`, `ValidateIdent`, `ErrSchemaNotReady`, `ErrNilDialect`, `ErrInvalidTableName`. One table is one queue: a claim takes any visible row of that table.
- **Its options doc warns** that a strict shared-transaction `Outbound` must not be a dead-letter or invalid-message sink, because the divert path's context never carries the business transaction.
- **Consumer** (`endpoint`): `NewConsumer[T](src, Handler[T], opts...)` with `Run(ctx)` that drains in flight up to `WithShutdownTimeout`; `WithConcurrency`, `WithPollInterval`, `WithConsumerCodec(msgin.BytesPayloadCodec{})`, `WithRetryPolicy(RetryPolicy{MaxAttempts, Backoff, DeadLetter})` (a finite `MaxAttempts` needs a `DeadLetter`; a nil `Backoff` hot-spins), `WithInvalidMessageSink`. `msgin.Permanent(err)` routes to the invalid-message sink, falling back to the dead-letter sink.
- **Dialect modules** (`adapter/database/sql/{postgres,mysql,sqlite}`) are separate modules whose `go.mod` says `require github.com/kartaladev/msgin v0.0.0` plus `replace ... => ../../../..`. A consumer cannot resolve them by pseudo-version: the replace is ignored outside their own module, and `v0.0.0` is never published. The root module has no replace and resolves by pseudo-version.
- **Status** (`docs/HANDOVER.md`): nothing is tagged and "nothing should be"; breaking API changes are free. `pgx`, `redis` and `nats` adapters are design-only in `MESSAGING.md`.

doger precedent: `scheduler/gocron` is a separate module implementing a core interface, in `go.work`, with `replace` directives to the local doger. `add-sql-event-transport` defines the shared module `github.com/kartaladev/doger/events/sqltx` (package `sqltx`, doger + standard library only): `Tx` implementing `doger.Tx` with `SQL()`, `DB()`, `Commit()`, `Rollback()`, `OnCommit`, `OnRollback`; `BeginTx(ctx, db, opts)`, `InTx(ctx, db, fn)`, `BindTx(ctx, db, tx)`, `From(ctx)`, `FromDB(ctx, db)` (errors matching `doger.ErrNoTransaction` or `sqltx.ErrForeignTransaction`), `Source()`. This adapter uses it instead of its own binding.

## Goals / Non-Goals

**Goals:**
- Pass `eventtransporttest` for `DeliverOnce` and the outbox on SQLite and Postgres.
- Keep doger's root module, workspace and release unaffected by an unreleased dependency.
- Use msgin's own primitives (outbox, lease claim, retry, dead letter, inbox) instead of re-implementing them.

**Non-Goals:**
- `DeliverAll` (no msgin broadcast backend exists).
- Automatic cleanup of abandoned groups; ordering; exactly-once.
- Any change to msgin.

## Decisions

### D1. Experimental module, outside the workspace and the release
```
doger/
  go.work                      (unchanged: does NOT list events/msgin)
  events/msgin/                module github.com/kartaladev/doger/events/msgin
    go.mod                     require msgin <pseudo-version of MSGIN_COMMIT>, doger, doger/events/sqltx
                               (replace => ../.. and ../sqltx, as scheduler/gocron does for doger)
    MSGIN_COMMIT               0c820e1... (single source of truth for the pin)
    internal/conformance/      module .../events/msgin/internal/conformance (tests only)
      go.mod                   replace msgin + dialect modules => ./_deps/msgin/...
      _deps/ (gitignored)      populated by `make msgin-deps` (git clone + checkout MSGIN_COMMIT)
```
- **Not in `go.work`.** Listing it would make every workspace build resolve msgin and its dialect modules, and `make test` documentation says workspace modules must build. An unreleased dependency should not be able to break doger's main loop.
- **Own CI workflow** (`.github/workflows/events-msgin.yml`), triggered on changes under `events/msgin/**` and weekly, marked non-required. Doger's release tooling ignores the path, so no `events/msgin/v*` tag is cut until msgin tags v1 and this module is promoted by a later change.
- **Pin consistency:** a unit test in the adapter module reads `MSGIN_COMMIT` and checks that `go.mod`'s msgin pseudo-version ends with its first 12 characters.
- *Alternative:* in `go.work` with a `replace` to a sibling checkout. Rejected: it breaks for any contributor or CI job without that checkout.
- *Alternative:* vendor msgin's dialect code. Rejected: it forks an unreleased library.

### D2. The adapter depends on msgin's root module only; dialects are injected
```go
type Dialect struct{ /* lease msginsql.LeaseDialect; registry SQL flavor */ }
func Postgres(lease msginsql.LeaseDialect) Dialect
func MySQL(lease msginsql.LeaseDialect) Dialect
func SQLite(lease msginsql.LeaseDialect) Dialect
func New(db *sql.DB, d Dialect, opts ...Option) (*Transport, error)
```
- The caller already imports a dialect module (and must solve its resolution, e.g. with their own `replace`), so the adapter needs only the `LeaseDialect` interface from the root module.
- The wrapper adds the SQL for the adapter's own registry table: placeholders (`$1` vs `?`) and upsert syntax differ, and msgin forbids driver auto-detection (its ADR 0011), so the flavor is explicit too.
- `New` validates eagerly: nil `db` → `ErrNilDB`; nil lease → wraps `msginsql.ErrNilDialect`; identifiers through `msginsql.ValidateIdent` → `msginsql.ErrInvalidTableName`; numeric options → `ErrInvalidOption`; the retry policy through `msgin.RetryPolicy.Validate` (after the dead-letter sink is filled in).

### D3. Groups over queues: a registry and fan-out on send
msgin's SQL table is a single competing queue, so a log-like "every group sees every event" needs one queue per group.

```
 registry doger_event_groups
 +---------------+-------------+----------------------+----------------------+
 | destination   | grp         | queue_table          | dlq_table            |
 +---------------+-------------+----------------------+----------------------+
 | orders.placed | mail        | doger_q_3f9a...      | doger_d_3f9a...      |
 | orders.placed | audit       | doger_q_b210...      | doger_d_b210...      |
 +---------------+-------------+----------------------+----------------------+

 Send(env)                       Subscribe(dest, group, DeliverOnce)
   tx := db.BeginTx(ctx, nil)      ensure/verify queue + dlq tables
   SELECT queue_table              INSERT registry row if absent (committed)
     WHERE destination = dest      start msgin consumer on queue_table
   for each: Outbound(queue).Send(sqltx.BindTx(ctx, db, tx), msg)
   tx.Commit()
```
- Table names are `<prefix>q_<16 hex of SHA-256(dest \x00 group)>` and `<prefix>d_<...>`, always valid identifiers whatever the group text (`/`, `.`). 64 bits of hash make a collision across the few groups of one deployment negligible; the registry row stores the names, so a collision would also be detectable (the insert finds a different pair under the same table, and `Subscribe` fails with `ErrGroupConflict`).
- `Send` runs the registry read and all inserts in one private transaction, so a fan-out is all-or-nothing. `SendTx` does the same inside the caller's transaction.
- **Latest-start semantics** come for free: a group receives only sends whose registry read happens after its row is committed. At the boundary (a send concurrent with the first subscription) the event may or may not reach the new group, which matches "starts at the latest event".
- **Durable groups:** stopping a subscription keeps the row, so events accumulate for a `DeliverOnce` group across restarts and deploys. `RemoveGroup(ctx, dest, group)` deletes the row; the tables are left for the operator to drop.
- Outbounds are built per queue table on first use and cached in a `sync.Map`; each is built with `WithSharedTransaction(resolver)` so every insert goes through the transaction in context.
- *Alternatives:* (a) one queue per destination: only one group could ever exist; (b) msgin `channel.PubSub`: in-process only; (c) msgin `GroupStore`: it is the Aggregator's correlation-group store, not consumer groups; (d) a single table with a group column: msgin's claim SQL cannot filter by column, so it would need a custom `LeaseDialect` per database. The registry is the only option that reuses msgin unchanged.

### D4. Envelope mapping
| Envelope field | msgin message |
|---|---|
| `ID` | message ID (`msgin.WithID`) → header `msgin.message-id` |
| `Payload` | `[]byte` payload with `msgin.BytesPayloadCodec{}` |
| `ContentType` | header `msgin.content-type` |
| `Destination`, `EventType`, `Version`, `PublishedAt` | headers `doger.destination`, `doger.event-type`, `doger.version` (number), `doger.published-at` (RFC3339Nano, UTC) |
| `Headers[k]` | header `doger.h.` + k |
- Application headers live under `doger.h.` so they can never overwrite msgin's reserved keys.
- Decoding a delivery with a missing or malformed `doger.*` header returns `msgin.Permanent(...)`, so it is dead-lettered without calling the doger handler.
- `PublishedAt` is compared at microsecond precision because MySQL `DATETIME(6)` and Postgres `timestamptz` keep microseconds; it is stored in a header string anyway, so it survives at full precision on every database.

### D5. Consumption and settlement
```go
src, _ := msginsql.NewPollingSource(db, queueTable, lease, msginsql.WithLockedBy(instanceTag))
c, _ := endpoint.NewConsumer[[]byte](src, handler,
    endpoint.WithConsumerCodec[[]byte](msgin.BytesPayloadCodec{}),
    endpoint.WithConcurrency[[]byte](cfg.concurrency),        // default 4
    endpoint.WithPollInterval[[]byte](cfg.pollInterval),      // default 1s
    endpoint.WithRetryPolicy[[]byte](policyWithDLQ),          // default 10 attempts, 1s..5m x2, 0.1 jitter
    endpoint.WithInvalidMessageSink[[]byte](dlqOutbound),
    cfg.extraConsumerOptions...)
go c.Run(subCtx)
```
- `handler` decodes (D4), calls the doger `EnvelopeHandler` with recovery, and returns: nil → Ack; `doger.IsPermanentError(err)` → `msgin.Permanent(err)`; otherwise the error (msgin Nacks with backoff, then dead-letters).
- The dead-letter and invalid-message sink is a plain `Outbound` on the group's `dlq_table`, **without** `WithSharedTransaction`, per msgin's documented warning.
- `stop(ctx)` cancels `subCtx` and waits for `Run` to return or `ctx` to end; `WithShutdownTimeout` is set from nothing (msgin default) because doger's shutdown context already bounds the wait. A second call returns nil.
- The default `Backoff` is never nil, because msgin hot-spins on a nil backoff.
- `WithLockedBy` uses `doger_<hostname>_<random>` so lease owners are distinguishable in the table.

### D6. DeliverAll
`Subscribe` checks `sub.Mode` first and returns `doger.ErrSubscriptionNotSupported` before touching the database. When msgin ships a broadcast-capable backend, support becomes a new requirement; with a SQL-only backend it could also be emulated with per-instance groups plus ephemeral cleanup, which is deliberately not done here (it would leak tables for every pod that ever ran).

### D7. Transaction binding through `sqltx`
```go
func (t *Transport) TxSource() doger.TxSource { return sqltx.Source() }

resolver := func(ctx context.Context) (msginsql.Querier, error) {
    tx, err := sqltx.FromDB(ctx, t.db)
    switch {
    case errors.Is(err, doger.ErrNoTransaction):
        return nil, nil // msgin strict mode then returns ErrNoSharedTransaction
    case err != nil:
        return nil, err // sqltx.ErrForeignTransaction
    }
    return tx.SQL(), nil
}
```
- Every Outbound is built with `msginsql.WithSharedTransaction(resolver)`. `SendTx` maps msgin's `ErrNoSharedTransaction` to `doger.ErrNoTransaction`; `sqltx.ErrForeignTransaction` passes through. `Send` binds its private transaction with `sqltx.BindTx` so both paths share one resolver.
- `sqltx.FromDB` enforces msgin's documented "same-database invariant", which msgin cannot check because a `*sql.Tx` does not expose its pool. Using the shared helper keeps the error identical across the SQL and msgin adapters.
- Because doger resolves `TxSource` from a `TxEventSender` transport (core spec "TxSource resolution"), applications that begin transactions with `sqltx.BeginTx` or `sqltx.InTx` get after-commit listeners and the Auto outbox strategy with no extra configuration.
- *Alternative (the first draft):* an adapter-local `BeginTx`/`Tx`. Dropped in favor of the shared module, so an application using both the SQL transport and this one has one transaction type, and the callback semantics are specified and tested once.

### D8. Opt-in idempotent consumer
`WithIdempotentConsumer(businessDB, deduper)` wraps the handler with `sqltx.InTx(ctx, businessDB, fn)`: `fn` calls `deduper.MarkProcessed(ctx, tx.SQL(), env.ID)`; when `already`, it returns nil (commit) without calling the handler; otherwise it calls the doger handler with the context `InTx` bound, so a listener reaches the transaction through `sqltx.From(ctx)`. A handler error makes `InTx` roll back and is returned, so msgin redelivers; a commit error is returned too. Requires the inbox table (`sqlite.InboxDDL` and friends, or `deduper.EnsureSchema`).

### D9. Schema
- Without `WithEnsureSchema()`, the first `Subscribe`/`Send` touching a table calls msgin's `Ready` on the adapter (and a registry existence query) and returns `ErrSchemaNotReady` (which also matches `msginsql.ErrSchemaNotReady`) listing the missing tables. Results are cached per table after success.
- With it, the transport calls msgin's `EnsureSchema` for queue and dead-letter tables and runs its own idempotent `CREATE TABLE IF NOT EXISTS` for the registry. Concurrent ensures are safe because every statement is idempotent.
- `RegistryDDL(d Dialect, table string) (string, error)` returns the registry statement for migration tools, as msgin's `DDL` helpers do for queues.

### D10. Testing and CI
- **Adapter module tests** (no database): options, table names, envelope mapping (D4), pin consistency. Run with plain `go test ./...` in `events/msgin`.
- **Conformance module** (`internal/conformance`): SQLite through `modernc.org/sqlite` (no cgo) with msgin's `sqlite.DSN(..., WithSharedMemory())`, always; Postgres 16 through `github.com/jackc/pgx/v5/stdlib` when `DOGER_MSGIN_POSTGRES_DSN` is set (the CI workflow provides a service container). Each test uses a fresh database or schema.
- The conformance run needs `eventtransporttest.WithoutDeliverAll()` (Open Questions).

## Risks / Trade-offs

- [msgin changes its API without notice] → pinned commit, experimental notice, isolated CI that fails visibly without blocking doger.
- [Fan-out cost grows with groups per destination: one insert per group per event] → `DeliverOnce` groups are few (one per listener kind); documented. Batch inserts are a later optimization.
- [Abandoned groups keep receiving copies forever] → `RemoveGroup`, documented in the README with a query that finds groups whose queue only grows.
- [SQLite serializes writers] → acceptable for tests and single-node use; production guidance points to Postgres or MySQL.
- [A send concurrent with a new group's first subscription may or may not reach it] → matches the core contract's "starts at the latest event"; documented.
- [The dialect modules cannot be consumed by pseudo-version] → callers who want msgin today already handle this (it is msgin's own limitation); the README shows the `replace` they need.

## Public API & Defaults

New module; no change to doger's API.

| Identifier | Kind | Default / override |
|---|---|---|
| `New(db *sql.DB, d Dialect, opts ...Option) (*Transport, error)` | constructor | n/a |
| `Dialect`, `Postgres(lease)`, `MySQL(lease)`, `SQLite(lease)` | type, constructors | required |
| `(*Transport).Send`, `SendTx`, `Subscribe`, `QueueTable`, `DeadLetterTable`, `RemoveGroup` | methods | n/a |
| `WithTablePrefix(p)`, `WithRegistryTable(t)` | options | `"doger_"`, `"doger_event_groups"` |
| `WithEnsureSchema()` | option | off |
| `WithConcurrency(n)`, `WithPollInterval(d)` | options | 4, 1s |
| `WithRetry(msgin.RetryPolicy)` | option | 10 attempts, `resilience.ExponentialBackoff{1s, 5m, 2, 0.1}`; the dead-letter sink is filled in unless set |
| `WithDeadLetter(msgin.OutboundAdapter)` | option | per-group dead-letter table |
| `WithIdempotentConsumer(businessDB *sql.DB, d *msginsql.InboxDeduper)` | option | off |
| `WithMsginConsumerOptions(opts ...endpoint.ConsumerOption[[]byte])` | option | none (escape hatch for msgin flow control) |
| `(*Transport).TxSource() doger.TxSource` | method | returns `sqltx.Source()` |
| `RegistryDDL(d Dialect, table string) (string, error)` | func | n/a |
| `ErrNilDB`, `ErrInvalidOption`, `ErrSchemaNotReady`, `ErrGroupConflict` | sentinels | messages start with `doger: msgin: ` |

## Test Strategy

Each scenario maps to one Go test; `Examples` rows run as named subtests. All tests run with `-race`; `TestMain` in both modules runs goleak. Database tests live in `internal/conformance` and run on SQLite by default and on Postgres when `DOGER_MSGIN_POSTGRES_DSN` is set; rows marked "Postgres" are skipped with a logged reason otherwise.

| Scenario | Test | Kind |
|---|---|---|
| Constructor outcomes | `events/msgin/transport_test.go: TestNew/<row>` | table-driven (SQLite) |
| The transport satisfies doger's interfaces | `events/msgin/transport_test.go: TestTransport_Interfaces` | unit + compile-time `var _` checks |
| Deterministic, valid names | `events/msgin/schema_test.go: TestTableNames/<row>` | table-driven |
| Different pairs get different tables | `events/msgin/schema_test.go: TestTableNames_Distinct` | unit |
| A table already registered for another pair is rejected | `internal/conformance/schema_test.go: TestSchema_GroupConflict` | integration (SQLite) |
| Missing and present schema | `internal/conformance/schema_test.go: TestSchema/<row>` | table-driven (SQLite) |
| Pre-provisioned schema | `internal/conformance/schema_test.go: TestSchema_Preprovisioned` | integration (SQLite) |
| EnsureSchema is idempotent | `internal/conformance/schema_test.go: TestSchema_EnsureConcurrent` | race (SQLite, Postgres) |
| Copies per group | `internal/conformance/fanout_test.go: TestFanout/<row>` | table-driven |
| A new group starts at the latest event | `internal/conformance/fanout_test.go: TestFanout_NewGroupAtLatest` | integration |
| A durable group keeps events while no member runs | `internal/conformance/fanout_test.go: TestFanout_DurableGroup` | integration |
| Send failures are returned | `internal/conformance/fanout_test.go: TestSend_ClosedDB` | unit |
| Two instances in one group | `internal/conformance/compete_test.go: TestDeliverOnce_Compete` | integration (Postgres) |
| Broadcast subscription | `events/msgin/transport_test.go: TestSubscribe_DeliverAll/<row>` | table-driven |
| Field round trip | `internal/conformance/envelope_test.go: TestEnvelope_RoundTrip/<row>` | table-driven; mapping unit-tested in `events/msgin/envelope_test.go: TestEnvelopeMapping` |
| Commit and rollback | `internal/conformance/outbox_test.go: TestSendTx/<row>` | table-driven |
| SendTx without a usable transaction | `internal/conformance/outbox_test.go: TestSendTx_Unusable/<row>` | table-driven |
| Through doger's outbox strategy | `internal/conformance/outbox_test.go: TestOutbox_WithDoger/<row>` | integration (two doger roots) |
| Doger finds sqltx transactions through the transport | `internal/conformance/tx_test.go: TestTxSource_Sqltx/<row>` | table-driven (doger root) |
| Binding an application transaction | `internal/conformance/tx_test.go: TestSendTx_BindTx` | integration |
| Settlement outcomes | `internal/conformance/settle_test.go: TestSettlement/<row>` | table-driven |
| Undecodable stored messages are dead-lettered | `internal/conformance/settle_test.go: TestSettlement_Undecodable` | integration |
| A custom dead-letter sink | `internal/conformance/settle_test.go: TestSettlement_CustomDLQ` | unit |
| Duplicates and failures | `internal/conformance/inbox_test.go: TestIdempotent/<row>` | table-driven |
| Business writes join the dedup transaction | `internal/conformance/inbox_test.go: TestIdempotent_SharedTx` | integration |
| Stop outcomes | `internal/conformance/stop_test.go: TestStop/<row>` | table-driven, goleak |
| Removing a group | `internal/conformance/stop_test.go: TestRemoveGroup` | integration |
| Concurrent sends and subscriptions | `internal/conformance/concurrency_test.go: TestConcurrentUse` | race (Postgres; SQLite with reduced counts) |
| Conformance runs | `internal/conformance/conformance_test.go: TestConformance/<row>` | `eventtransporttest.Run` |

**Hot paths and their tests:**
- Fan-out send (registry read + N inserts in one transaction): `TestFanout`, `TestSendTx`, `TestConcurrentUse`.
- Delivery handler (decode, call, settlement mapping): `TestSettlement`, `TestSettlement_Undecodable`, `TestEnvelope_RoundTrip`.
- Transaction resolution and the database check: `TestSendTx_Unusable`, `TestTxSource_Sqltx`, `TestOutbox_WithDoger`.
- Subscription start and stop: `TestSchema`, `TestStop`, `TestDeliverOnce_Compete`.

## Migration Plan

New, experimental module; nothing to migrate. Adoption: add the module, provision tables with `RegistryDDL` and msgin's `DDL` helpers (or `WithEnsureSchema()` in development), set `InjectorOpts.EventTransport`, and begin business transactions with `sqltx.BeginTx` or `sqltx.InTx`. Rollback: switch `InjectorOpts.EventTransport` to another transport; undelivered rows remain in the queue tables until drained or dropped. Promotion out of experimental status is a separate change once msgin tags v1: it adds the module to `go.work`, replaces the conformance module's `replace` directives with real versions, and starts tagging.

## Open Questions

None. Resolved during planning: `eventtransporttest.WithoutDeliverAll()` is provided by `add-event-transport`, and the transaction binding is the shared `events/sqltx` module from `add-sql-event-transport`.
