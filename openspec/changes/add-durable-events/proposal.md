# Proposal

## Why

`add-eventing` makes in-process events first class, but an event is lost when the process stops before a listener finishes, and a listener can act on data whose transaction later rolls back. Applications then hand-write an outbox, a retry loop and redelivery on start. This change adds what Spring offers through `@TransactionalEventListener` and Spring Modulith's event publication registry: listeners bound to transaction outcomes, and durable listeners with at-least-once delivery that survives crashes and works across instances. Like eventing, it is part of core: the contract, semantics and a zero-configuration default live in package `doger`; only crash-safe storage drivers live in separate modules.

An adversarial audit of the first draft (in `add-eventing`'s history) found that it could hang `Start`, deliver events whose transaction rolled back, and duplicate deliveries across instances. This proposal is built around fixing those: a lease-based store SPI, a store that owns its transaction lookup, a bounded redelivery pass, and dead-lettering.

## What Changes

**Transaction-bound listeners**
- Core defines `Tx` (`OnCommit(func(ctx))`, `OnRollback(func(ctx))`) and `TxSource` (`Lookup(ctx) (Tx, bool)`, `Detach(ctx) context.Context`). A store module provides both, for example `dogerpg.WithTx(ctx, tx)`, so core never depends on `database/sql`.
- `ListenerTxPhase(TxPhaseImmediate | TxPhaseAfterCommit | TxPhaseAfterRollback | TxPhaseAfterCompletion)` on any listener; deferred listeners run in the transaction callback with a **detached** context that no longer carries the finished transaction. Their errors go to `HookEventError`.
- `ListenerTxFallback(TxFallbackRun | TxFallbackSkip)` decides what a deferred listener does when no transaction is bound (default: run immediately).
- A transaction that already ended behaves as if none were bound (the `TxSource` contract).

**Durable listeners**
- `ListenerDurable()`: `Publish` records one publication per durable listener that passes its filter, then delivers it. Delivery errors are recorded with `Fail`, never returned by `Publish`; `Publish` returns only recording errors.
- Durable listeners are allowed with `TxPhaseAfterCommit` (their default) and, when no transaction is bound, immediate delivery. `TxPhaseImmediate` with a bound transaction, `TxPhaseAfterRollback` and `TxPhaseAfterCompletion` are rejected at registration, because they would complete before commit or lose the record with the rollback.
- Durable listeners must have a stable ID: a `ListenerID` or a default derived from the registration, unique among live durable listeners. Durable listeners live in the root or in extension scopes, which exist across restarts.
- Publications store the event's **dynamic** type name, its `Codec` content type, `Headers` from a replaceable propagator (trace and tenant context survive redelivery), and an optional `Version` from `RegisterEventType`.

**The store SPI**
- Core interface `PublicationStore`: `Append`, `Complete(token)`, `Fail(token, cause, nextAttemptAt)`. Optional interfaces: `PublicationClaimer` (`Claim(owner, now, lease, limit)` returning leased publications with tokens; stale tokens fail with `ErrClaimLost`), `PublicationPurger`, `PublicationDeadLetters` (list and resubmit), and `TxAwareStore` (the store's `TxSource`). New capabilities come as new optional interfaces, never new methods.
- Attempts are counted when a publication is claimed, so a crash during delivery still counts. After `PublicationMaxAttempts` the publication is dead-lettered and no longer claimed.
- Backoff is computed from the last attempt, with jitter and a cap, through a replaceable `RetryPolicy`.

**Redelivery and retry**
- At `Start`, one bounded pass delivers publications that were due when the pass began, each at most once per pass; it never loops on failing rows. A `Claim` error fails `Start`. Live deliveries hold their own lease, so redelivery and retry never take a publication in flight in the same process.
- An optional retry loop (`PublicationRetryInterval`) starts at Ready and stops before the Closing hooks, as the health scheduler does.
- Retention: completed publications are deleted by ID by default; with `PublicationRetention` they are kept and purged on a timer with an inclusive bound.

**Defaults and installation**
- `NewMemoryPublicationStore()` is the default and is exported for tests. It is not crash-safe, refuses a bound transaction (`ErrTxNotSupported`), and is announced once through the event error path. `InjectorOpts.RequireDurableStore` makes `Start` fail while it is in use.
- The store is resolved only when durable delivery is used (a durable listener exists or `InjectorOpts.PublicationStore` is set), never by registering a default service: `InjectorOpts.PublicationStore`, else a service named `NameOf[PublicationStore]()` (an extension may export an `AsDefault()` one), else the in-memory store. A failing store provider fails the publish or `Start`; it never falls back silently. Containers without durable listeners do no extra work at `Start`.
- A `publicationtest` package gives store authors a conformance suite.

**Out of scope:** the crash-safe store modules themselves (`dogerevents-postgres`, …), broker bridges, exactly-once delivery, and ordering guarantees between publications.

## Capabilities

### New Capabilities
- `transactional-events`: `Tx`, `TxSource`, transaction phases and fallback for listeners, detached contexts after completion, and error routing of deferred listeners.
- `event-publication-registry`: durable listeners, publication recording, the store SPI and its optional interfaces, claims and leases, attempts, retry policy and dead letters, redelivery at `Start`, the retry loop, retention, codec, event type registry, headers, store resolution, the in-memory store, and clones.

### Modified Capabilities
- None. This change requires `add-eventing` to be archived first and adds listener options without changing its requirements; the `events` capability is extended by new requirements in the two capabilities above.

## Impact

- Code: new `event_tx.go`, `event_publication.go`, `event_publication_store.go`, `event_codec.go`, `event_retry.go`, package `publicationtest`; listener options in the `add-eventing` files; `root_start.go` (redelivery pass), the ready and close paths (retry loop), `injector.go` (publication options, copied by `copy()`), `root_scope.go` (lazily allocated publication state, clone).
- API: additive only. No exported API breaks.
- Dependencies: no new module dependencies (IDs are UUIDv7-shaped from `crypto/rand` and the root clock). Requires `add-eventing` (archived) and `add-extensions` (archived).
- Docs: a "Durable events" guide (semantics, idempotency with publication IDs, writing a store, transaction binding), `Example*` tests, upgrading note (no breaks).

## Defaults & Control

| Behavior | Zero-config default | Consumer control |
|---|---|---|
| Transaction phase | Immediate; after commit for durable listeners | `ListenerTxPhase(...)` |
| No transaction bound | Deferred listeners run immediately | `ListenerTxFallback(TxFallbackSkip)` |
| Transaction lookup | None bound (in-memory store has no `TxSource`) | The store's `TxSource` (`TxAwareStore`) |
| Durability | Off per listener | `ListenerDurable()` |
| Publication store | In-memory, not crash-safe, announced once | `InjectorOpts.PublicationStore`, a named service, or a store extension; `RequireDurableStore` to forbid the default |
| Claim lease | 5 minutes | `InjectorOpts.PublicationLease` |
| Max attempts | 10, then dead-lettered | `InjectorOpts.PublicationMaxAttempts`; `PublicationDeadLetters` to resubmit |
| Backoff | Exponential from the last attempt, jitter, capped at 1h | `InjectorOpts.PublicationRetryPolicy` |
| Retry loop | Off | `InjectorOpts.PublicationRetryInterval` |
| Redelivery batch | 100 per claim | `InjectorOpts.PublicationBatchSize` |
| Retention | Completed publications deleted at once | `InjectorOpts.PublicationRetention` |
| Codec | `encoding/json`, content type `application/json` | `InjectorOpts.EventCodec` |
| Event type names | Import-path-qualified dynamic type | `RegisterEventType[E](root, name, opts...)` |
| Headers | None propagated | `InjectorOpts.PublicationPropagator` |
| Publication IDs | UUIDv7-shaped | `InjectorOpts.PublicationIDGenerator` |
