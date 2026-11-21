# Proposal

## Why

`add-event-transport` defines the transport seam and promises that msgin can sit behind it. msgin (`github.com/kartaladev/msgin`) already provides the hard parts a SQL-backed transport needs: a transactional outbox (`Outbound` with `WithSharedTransaction`), competing consumers on a queue table (lease and fence token, `SKIP LOCKED`), retry with backoff, dead-lettering, and an idempotent-consumer inbox (`InboxDeduper`). An adapter lets doger users who already run msgin reuse it instead of a second messaging stack.

msgin is untagged and pre-v1, and its own handover says nothing should be tagged yet, so this adapter ships as **experimental**: it is built and tested, but not released with doger and carries no compatibility promise until msgin tags v1.

## What Changes

- New module `github.com/kartaladev/doger/events/msgin` (directory `events/msgin`, package `dogermsgin`), Go 1.27, depending on the msgin root module at a pinned pseudo-version and on doger.
- `dogermsgin.New(db, dialect, opts...) (*Transport, error)` returns a transport implementing `doger.EventTransport` and `doger.TxEventSender` over msgin's `database/sql` adapter. The caller injects msgin's `LeaseDialect` (Postgres, MySQL or SQLite) inside a `dogermsgin.Postgres`/`MySQL`/`SQLite` wrapper that adds the group-registry SQL, so this module never depends on msgin's dialect modules.
- **Groups over a queue.** msgin SQL is a queue, not a log, so the transport keeps a group registry table: `Subscribe` registers its (destination, group) and its queue table, and `Send` writes one message into the queue table of every group registered for the destination. A group registered after a send never sees it, so new groups start at the latest event.
- **`DeliverOnce`** runs a msgin consumer on the group's queue table; instances in the group compete through msgin's lease.
- **`DeliverAll`** returns `doger.ErrSubscriptionNotSupported`: msgin has no shipped broadcast adapter (its `pgx`, `redis` and `nats` adapters are design-only).
- **Outbox.** `SendTx` writes the group copies inside the caller's transaction through msgin's strict shared transaction. The transaction comes from the shared `github.com/kartaladev/doger/events/sqltx` module (defined by `add-sql-event-transport`): the transport's `TxSource()` is `sqltx.Source()`, and msgin's `TransactionResolver` uses `sqltx.FromDB(ctx, db)`, so `SendTx` rejects a transaction opened on another database with `sqltx.ErrForeignTransaction`, an invariant msgin itself cannot check.
- **Errors.** A handler error is a msgin `Nack` under the transport's retry policy (exponential backoff, 10 attempts, then a per-group dead-letter table). A `doger.PermanentError` becomes `msgin.Permanent` and goes to the dead-letter table at once.
- **Opt-in idempotent consumer** with msgin's `InboxDeduper`: the handler runs inside a business transaction that records the envelope ID, and a redelivery is acknowledged without calling the listener.
- **Schema** is never created at runtime unless the caller opts in (`WithEnsureSchema()`). Helpers name the tables, and msgin's `DDL` functions produce the statements for a migration tool.
- **Experimental gating:** the module is not in doger's `go.work` or release workflow. It has its own CI job that pins a msgin commit, and it runs `eventtransporttest` against SQLite (always) and Postgres (in CI).

**Out of scope:** `DeliverAll`, a non-SQL msgin backend, and cleanup of groups that no longer exist beyond an explicit `RemoveGroup` call.

## Capabilities

### New Capabilities
- `msgin-event-transport`: the msgin-backed transport, covering construction and options, the group registry and fan-out, `DeliverOnce` consumption, the unsupported `DeliverAll`, the outbox and its `TxSource`, error mapping to retries and dead letters, the opt-in idempotent consumer, schema handling, lifecycle, and conformance.

### Modified Capabilities
- None. The adapter implements the `event-transport` contract of `add-event-transport` without changing it.

## Impact

- Code: new module `events/msgin` (`transport.go`, `registry.go`, `envelope.go`, `outbox.go`, `consumer.go`, `inbox.go`, `options.go`, `schema.go`), and a test module `events/msgin/internal/conformance` with SQLite and Postgres runs.
- Dependencies (adapter module only): doger, `github.com/kartaladev/doger/events/sqltx`, and `github.com/kartaladev/msgin` (pseudo-version of a pinned commit) with its transitive `clockwork` and `robfig/cron`. Test module only: msgin's SQLite and Postgres dialect modules, `modernc.org/sqlite`, and a Postgres driver. Doger's root module gains nothing.
- Prerequisites: `add-event-transport` archived (it provides `eventtransporttest.WithoutDeliverAll()`, which this adapter's conformance run uses), and `add-sql-event-transport` archived or at least its `events/sqltx` module merged.
- CI: a separate, non-blocking workflow for the experimental module; doger's release workflow ignores it.
- Docs: module README and package godoc with the experimental notice, the schema steps, and the outbox example.

## Defaults & Control

| Behavior | Zero-config default | Consumer control |
|---|---|---|
| Stability | Experimental: no compatibility promise until msgin tags v1 | Pin the module version; nothing to override |
| SQL dialect | Required argument (no driver detection, as in msgin): `dogermsgin.Postgres(...)`, `MySQL(...)` or `SQLite(...)` wrapping msgin's `LeaseDialect` | Any msgin `LeaseDialect`, including a custom one, inside the matching wrapper |
| Table names | Prefix `doger_` + a short hash per (destination, group) queue and dead-letter table; registry `doger_event_groups` | `WithTablePrefix(p)`, `WithRegistryTable(t)` |
| Schema | Checked at `Subscribe`/`Send`; missing tables fail with `ErrSchemaNotReady` naming them | `WithEnsureSchema()` creates them (dev and tests) |
| `DeliverOnce` | msgin consumer, 4 workers, poll every 1s, lease strategy | `WithConcurrency(n)`, `WithPollInterval(d)`, `WithMsginConsumerOptions(...)` |
| `DeliverAll` | `ErrSubscriptionNotSupported` | None until msgin ships a broadcast adapter |
| Retry | Exponential backoff 1s to 5m, ×2, 0.1 jitter; 10 attempts | `WithRetry(policy)` (a `msgin.RetryPolicy` minus the dead-letter sink, which the transport owns) |
| Dead letters | Per-group dead-letter table | `WithDeadLetter(adapter)` |
| Permanent errors | Dead-lettered at once, not retried | Return a non-permanent error to retry |
| Outbox without a transaction | Handled by doger (`ErrNoTransaction` by default) | doger's `InjectorOpts.EventOutboxFallback` |
| Transaction binding | `sqltx.BeginTx`, `sqltx.InTx` or `sqltx.BindTx`; the transport's `TxSource()` is `sqltx.Source()` | `InjectorOpts.TxSource` in doger, as long as it finds `sqltx.Tx` values for the outbox |
| Transaction on another database | `SendTx` fails with an error matching `sqltx.ErrForeignTransaction` | None: writing the outbox outside the business database would break atomicity |
| Idempotent consumer | Off | `WithIdempotentConsumer(businessDB, deduper)` |
| Shutdown | Subscription stop waits for handlers up to doger's shutdown context | Set doger's shutdown context |
