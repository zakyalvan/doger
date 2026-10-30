# Design

## Context

See proposal.md (Why) and the two specs. This change builds on `add-eventing`, which must be archived first:

- **Listener bindings** live on service wrappers (`serviceOptions.listeners`) and in per-scope function-listener lists; dispatch builds immutable plans per (scope, type, local); `HookEventError` receives failures with no caller; executors are `EventExecutor`. This change adds options to those bindings and a recording step to dispatch.
- **Clock.** The root clock (`clockwork.Clock`, `container-clock`) drives every time in this change, so tests use a fake clock.
- **Ready and close hooks.** The health scheduler starts at `markReady` (`startScheduling`, `health_monitor.go:51`) and stops before the Closing hooks (`stopScheduling`, `health_monitor.go:107`); the retry loop and purge timer follow that placement. Unlike `stopScheduling`, which does not wait for checks in flight, the retry loop waits for its batch in progress, because a delivery that runs while services shut down would act on closed dependencies.
- **Extension scopes** are known through `root.ext` (installed and installing entries), which is how durable listeners are restricted to scopes that exist across restarts.
- **`AsDefault`** registrations yield to explicit ones (`add-default-registrations`), so a store extension can export an overridable default under `NameOf[PublicationStore]()` without core registering anything.
- **First-draft audit.** The first durable draft (in `add-eventing`'s history) hung `Start` on a failing row, could deliver after rollback when store and binder disagreed, duplicated deliveries across instances, and recorded filtered events. Each decision below names the finding it answers.

## Goals / Non-Goals

**Goals:**
- At-least-once delivery that survives crashes, never delivers a rolled-back event, and never delivers a leased publication twice.
- A store SPI that third-party modules can implement once and that grows only through optional interfaces.
- `Start` always terminates; containers without durable listeners do no new work.

**Non-Goals:**
- Exactly-once delivery; listeners deduplicate with `PublicationFromContext(ctx).ID`.
- Lease renewal for deliveries longer than the lease (see Open Questions).
- Ordering between publications; crash-safe store modules; broker bridges.

## Decisions

### Overview

```
+------------------------------- package doger --------------------------------+
|                                                                              |
|  Publish(ctx, i, e)  (add-eventing dispatch)                                 |
|     |                                                                        |
|     +-- plan --> filters --> durable listeners? --------------------+        |
|     |                                                              v        |
|     |   TxSource.Lookup(ctx) --(tx?)--> defer by TxPhase   +-------------+   |
|     |        (D2, D3)                                      | recorder    |   |
|     |                                                      | (D5, D6)    |   |
|     +-- deliver now / OnCommit / OnRollback  <-------------| encode, ID, |   |
|            |                                               | headers,    |   |
|            v                                               | Append ->   |   |
|     listener --ok--> Complete(token)                       | leased pubs |   |
|              --err-> Fail(token, next, dead?) (D8, D9)     +------+------+   |
|                                                                   |          |
|  Start: redelivery pass (D7)   Ready..Closing: retry loop, purge (D10)       |
+-------------------------------------------------------------------|----------+
                          PublicationStore SPI (+ optional ifaces)  |
==================================================================== | =========
              NewMemoryPublicationStore (core)   dogerevents-postgres (module)
```

### D1. Types for transaction-bound listeners
```go
type Tx interface {
    OnCommit(func(ctx context.Context))
    OnRollback(func(ctx context.Context))
}
type TxSource interface {
    Lookup(ctx context.Context) (Tx, bool)       // false once the tx has ended
    Detach(ctx context.Context) context.Context  // same values, no tx, not cancelled
}
type TxPhase int        // TxPhaseImmediate (zero), TxPhaseAfterCommit, TxPhaseAfterRollback, TxPhaseAfterCompletion
type TxFallbackMode int // TxFallbackRun (zero), TxFallbackSkip
func ListenerTxPhase(TxPhase) ListenerOption
func ListenerTxFallback(TxFallbackMode) ListenerOption
func ListenerDurable() ListenerOption
```
- Names carry the `Tx` prefix so they do not collide with `ContainerPhase` constants or `RootScope.Phase()` (audit). Both enums have `String()`, and the zero values are the safe defaults (precedent: `PlacementPrivate`, `EventFailFast`).
- `Detach` belongs to `TxSource` because only the module that put the transaction in the context can take it out (audit: after-commit work reused a context holding a committed `*sql.Tx`).
- Callbacks take a context so the `Tx` implementation can pass its own; core ignores it in favor of the detached publish context, which carries the publisher's values.

### D2. Deferring listeners
- `Publish` calls `TxSource.Lookup` once, only when the plan contains a listener with a deferred phase or a durable listener.
- With a transaction, deferred listeners of the plan are collected, in plan order, into one closure per callback kind and registered with `OnCommit` / `OnRollback` (`AfterCompletion` registers on both). The closure runs them with `Detach(publishCtx)`, recovers panics, and sends every error to `HookEventError` without stopping the rest. Async deferred listeners are submitted to the executor from the callback.
- Without a transaction, `TxFallbackRun` listeners join the immediate ones and `TxFallbackSkip` ones are dropped before recording (audit: skipped listeners must not be recorded).

```
 Publish(ctx with tx1)
   plan: [a: immediate] [b: after-commit] [d: durable, after-commit]
   |
   +-- record d (in tx1, D6) -------------------------> Append(ctx)
   +-- run a now
   +-- tx1.OnCommit(func(cctx){ run b, deliver d } with Detach(ctx))
   +-- return
 ... app commits tx1 ...
   OnCommit callback --> b(detached ctx) ; d(detached ctx) -> Complete
 ... or app rolls back ...
   nothing runs; d's row vanished with tx1
```

### D3. TxSource resolution and the store's own source
- Once per root: `InjectorOpts.TxSource`, else the service `NameOf[TxSource]()`, else the resolved store's `TxSource()` when it implements `TxAwareStore`, else none.
- When the resolved store is a `TxAwareStore` and the resolved source is a different value, resolution fails with `ErrTxSourceConflict` (audit: an independent binder writing outside the store's transaction produced ghost events).
- A durable publish with a transaction found but a store that is not `TxAwareStore` fails with `ErrTxNotSupported` in core, before `Append`, so the rule does not depend on each store.

### D4. Durable listener registration
- `ListenerDurable()` sets `durable` on the binding; its default phase becomes `TxPhaseAfterCommit`.
- At registration (in the same check that validates `AsListener`/`ListenMethod`/`Listen`):
  - the binding's scope must be the root or an extension private scope (found through `root.ext`, any entry state, so an extension can register during `Extend`);
  - `TxPhaseAfterRollback` and `TxPhaseAfterCompletion` panic (the record would roll back with the transaction);
  - durable function listeners need a non-empty `ListenerID`;
  - the ID must not equal another live durable listener's: checked by scanning the bindings and function listeners of the root and extension scopes, not by a separate registry, so overrides and removals can never leave a stale entry (the `add-eventing` audit's drift finding).
- `TxPhaseImmediate` is allowed for durable listeners only without a transaction; with one, `Publish` fails with `ErrTxPhaseNotDurable` (completing before commit would either fail invisibly or deadlock on some databases).

### D5. Publication model and store SPI
```go
type Publication struct {
    ID, ListenerID, EventType, ContentType string
    Version                                int
    Payload                                []byte
    Headers                                map[string]string
    PublishedAt                            time.Time
    Attempts                               int
    LastAttemptAt, NextAttemptAt           time.Time
    LastError                              string
    Dead                                   bool
}
type Claimed struct { Publication; Token string }

type PublicationStore interface { // core
    Append(ctx context.Context, req AppendRequest) ([]Claimed, error) // AppendRequest{Owner, Now, Lease, Publications}
    Complete(ctx context.Context, token string, info CompleteInfo) error // CompleteInfo{At, Delete}
    Fail(ctx context.Context, token string, info FailInfo) error         // FailInfo{Cause, At, NextAttemptAt, Dead}
}
type PublicationClaimer interface { // redelivery and retry
    Claim(ctx context.Context, req ClaimRequest) ([]Claimed, error) // ClaimRequest{Owner, Now, Lease, Limit}
}
type PublicationPurger interface { Purge(ctx context.Context, completedAtOrBefore time.Time) error }
type PublicationDeadLetters interface {
    DeadLetters(ctx context.Context, limit uint) ([]Publication, error)
    Resubmit(ctx context.Context, id string) error
}
type TxAwareStore interface { TxSource() TxSource }
```
- Three core methods; everything else is optional, following the library's own rule for SPIs ("optional interfaces, never new methods", `extension.go`). Request structs let fields be added without breaking implementations.
- **Contract** (documented on each method, checked by `publicationtest`): `Append` stores and leases each publication to `Owner` until `Now + Lease` with `Attempts = 1`; `Claim` returns at most `Limit` publications that are not dead, not leased (or whose lease expired), and have `NextAttemptAt <= Now`, leasing each to `Owner` and incrementing `Attempts`; `Complete` and `Fail` require the current token and otherwise return `ErrClaimLost`; `Complete{Delete: true}` deletes by ID; `Purge` deletes completed publications with completion time `<=` the bound.
- *Alternative:* the first draft's five-method interface with an unleased `Claim(before, limit)`. Rejected: no store could implement leasing behind it, `Start` looped on failed rows, and every addition would break implementations.

### D6. Recording
Order inside `Publish`, after the plan is filtered (filters and `TxFallbackSkip` first):
1. If no durable listener remains, skip all of this (no store resolution, no work).
2. Resolve the store and `TxSource` (D3, D12). Check the transaction rules (D3, D4).
3. Register the dynamic type if needed (D11); encode once; build one `Publication` per durable listener with an ID (D13), `Headers` from the propagator, and `PublishedAt = clock.Now()`.
4. `Append` in one call with the publish context (a `TxAwareStore` finds the transaction there). Any error or panic in steps 3-4 fails `Publish` with "DI: failed to record event `<type>`: <cause>" before any listener runs.
5. Deliver each durable listener in its phase with its `Claimed` token in the context (`PublicationFromContext`); on return call `Complete`/`Fail` with a detached context (D8).

Durable deliveries are scheduled independently of fail-fast. A non-durable error that stops the synchronous loop does not cancel deliveries already recorded: they still run in the same `Publish`, and `Publish` returns the non-durable error. The first draft instead left them for the next `Start`, so a caller who saw an error and retried published twice; now the error only ever describes non-durable listeners, which is documented on `Publish`.

### D7. Redelivery pass at Start
Runs after Starters and kind handlers, before the Started hooks, only when the root has durable listeners and the store implements `PublicationClaimer`; without a claimer, `HookEventError` gets `ErrRedeliveryNotSupported` once.

```
 cutoff := clock.Now(); seen := {}
 loop:
   batch := Claim(ctx, {Owner, Now: cutoff, Lease, Limit: BatchSize})  -- error: Start fails
   fresh := batch minus seen
   if len(fresh) == 0: done                 -- terminates even if a store re-returns rows
   for p in fresh:
     seen += p.ID
     resolve listener by ID      -- missing: Fail(ErrListenerNotFound)
     resolve type by name        -- missing: Fail(ErrEventTypeNotFound)
     check codec content type    -- mismatch: Fail(ErrCodecMismatch)
     decode into reflect.New(T)  -- error: Fail(decode error)
     deliver with Extract(headers) context, then Complete / Fail
```
- Rows claimed in this pass are leased to this owner, so a correct store does not return them again; `seen` makes termination independent of store behavior (audit: the first draft's loop never ended on a failing row).
- `Now: cutoff` excludes rows that become due during the pass, including live publishes appended after the pass began, which are leased anyway.

### D8. Outcomes, attempts and dead letters
```
                    Append (Attempts=1, leased)          Claim (Attempts+1, leased)
                              |                                   ^
                              v                                   |
 +-----------+  ok    +---------------+  err/panic   +----------------------------+
 | COMPLETED |<-------|  IN DELIVERY  |------------->| WAITING  NextAttemptAt =   |
 | (deleted  |Complete|  (lease held) |    Fail      | RetryPolicy.Next(n, at)    |
 |  or kept) |        +---------------+              +----------------------------+
 +-----------+                |                                   |
       |                      | Attempts >= Max on Fail           | lease expired after
       v Purge                v                                   | a crash: claimable
   (retention)          +-----------+   Resubmit                  |
                        |   DEAD    |---------------> WAITING (Attempts=0)
                        +-----------+
```
- `Fail` computes `NextAttemptAt` with the `RetryPolicy` and sets `Dead` when `Attempts >= PublicationMaxAttempts`.
- A stale token (`ErrClaimLost`) is reported to `HookEventError` and not retried: another owner has the publication.
- `Complete`, `Fail` and their errors use `TxSource.Detach(publishCtx)` (or the pass context at redelivery); their errors go to `HookEventError`.

### D9. Retry policy
```go
type RetryPolicy interface { Next(attempts int, lastAttemptAt time.Time) time.Time }
type ExponentialRetry struct { Base, Max time.Duration; Jitter float64; Rand func() float64 }
```
`delay = min(Max, Base << (attempts-1))` computed with an overflow guard (once `Base << k` would exceed `Max`, use `Max`), 0 for `attempts < 1`, then multiplied by `1 + Jitter*(2*r-1)` with `r` from `Rand` (default `math/rand/v2.Float64`; jitter needs no crypto randomness). Default `{1s, 1h, 0.1, nil}`. Computing from the last attempt fixes the first draft's collapse after downtime.

### D10. Retry loop and purge timer
- Started from the ready path next to `startScheduling`, only with durable listeners and a claimer (retry) or purger plus positive retention (purge). Each is one goroutine driven by a `clock.NewTicker`, owned by the root.
- Stopped next to `stopScheduling`, before the Closing hooks: cancel the loop context, then wait for the batch in progress (the spec's ordering). Never started when `Start` fails.
- Tick errors and panics go to `HookEventError`; the loop continues.

### D11. Codec and event type registry
```go
type EventCodec interface {
    ContentType() string
    Marshal(v any) ([]byte, error)
    Unmarshal(data []byte, v any) error
}
func RegisterEventType[E any](root *RootScope, name string, opts ...EventTypeOption) error // EventVersion(n)
```
- Shape follows `encoding/json`; decoding passes `reflect.New(T).Interface()` and dereferences once, so pointer event types round-trip as pointers.
- The registry maps name -> type and type -> (primary name, version). The import-path name of each recorded dynamic type is registered automatically and remains an alias after an explicit name is added, so stored rows keep decoding after a rename. A name bound to another type, or a second explicit name for a type, returns `ErrEventTypeConflict`; an empty name returns `ErrInvalidEventType`.

### D12. Store resolution and the in-memory store
```
 first durable Publish, or Start with durable listeners
            |
   InjectorOpts.PublicationStore set? --yes--> use it
            | no
   root service NameOf[PublicationStore]()? --yes--> invoke it (error/panic: fail)
   (an app service wins over an exported AsDefault() one)
            | no
   NewMemoryPublicationStore() + one-time Logf (log fallback)
   RequireDurableStore? --> fail Start / Publish with ErrDurableStoreRequired
```
- Resolution is guarded by a once-with-result (as `PropertySource` in `add-extensions`); no service is ever registered, so `ListProvidedServices` is unchanged (audit: registering a default changed observable state for every container).
- `NewMemoryPublicationStore()` implements every optional interface except `TxAwareStore`, honors leases and tokens, and is mutex-guarded. Exported so tests can inspect it (audit: scenarios could not observe an unexported store).

### D13. Headers and IDs
- `type PublicationPropagator interface { Inject(ctx context.Context) map[string]string; Extract(ctx context.Context, h map[string]string) context.Context }`; unset means no headers. Inject panics fail recording; Extract panics fail that delivery.
- Default IDs are UUIDv7: 48-bit Unix milliseconds from the root clock, version 7, variant bits, and 74 random bits from `crypto/rand`; time-sortable, which suits B-tree indexes in SQL stores. `InjectorOpts.PublicationIDGenerator` replaces it; an empty result fails with `ErrInvalidPublication`.

### D14. Clone
`CloneWithOpts` copies durable bindings with their wrappers (as in `add-eventing`) and copies the type registry. The resolved store, `TxSource`, retry loop, purge timer and owner ID are per root: a clone gets a fresh owner ID and resolves on first use. A store set in `InjectorOpts` is shared because `copy()` copies the field and the caller owns it; distinct owner IDs and leases keep two roots from delivering the same leased publication twice.

### D15. Conformance suite
Package `publicationtest` (inside the root module, so `make test` covers it) exports `TestStore(t *testing.T, newStore func(t *testing.T) PublicationStore)`, with named subtests: "append leases", "claim skips leased", "claim skips dead", "claim skips not due", "claim counts attempts", "stale token", "complete deletes", "purge is inclusive", "dead letters resubmit". Optional-interface subtests are skipped when the store does not implement them. Precedent: `testing/fstest.TestFS`.

## Risks / Trade-offs

- [A delivery longer than `PublicationLease` can be claimed by another owner and run twice] → documented; the lease default is 5m and configurable; listeners are idempotent by publication ID. Renewal is an open question.
- [Durable listeners are restricted to root and extension scopes] → documented with the reason (they must exist after a restart); request-scoped work uses non-durable listeners.
- [The in-memory store gives the API without crash safety] → one-time log line, `RequireDurableStore`, and refusal of bound transactions.
- [Clock skew between instances affects lease expiry in shared stores] → stores may use database time; the contract only requires leases to be honored relative to `Now` the root passes, documented for store authors.
- [`Fail` after a crash never runs, so `LastError` stays empty for crash attempts] → attempts still count at claim time; documented.
- [Jitter makes schedules non-deterministic] → tests use `Jitter: 0` or a fixed `Rand`.

## Public API & Defaults

Additive only. **No exported API breaks.**

| Identifier | Kind | Default / override |
|---|---|---|
| `Tx`, `TxSource` | interfaces | None bound; `InjectorOpts.TxSource`, a `NameOf[TxSource]()` service, or a `TxAwareStore` |
| `TxPhase` (`TxPhaseImmediate`, `TxPhaseAfterCommit`, `TxPhaseAfterRollback`, `TxPhaseAfterCompletion`), `ListenerTxPhase` | enum + option | Immediate; after commit for durable |
| `TxFallbackMode` (`TxFallbackRun`, `TxFallbackSkip`), `ListenerTxFallback` | enum + option | Run |
| `ListenerDurable()` | option | Off |
| `Publication`, `Claimed`, `AppendRequest`, `ClaimRequest`, `CompleteInfo`, `FailInfo` | types | n/a |
| `PublicationStore`, `PublicationClaimer`, `PublicationPurger`, `PublicationDeadLetters`, `TxAwareStore` | SPI | `NewMemoryPublicationStore()` |
| `NewMemoryPublicationStore() *MemoryPublicationStore` | constructor | Default store |
| `RetryPolicy`, `ExponentialRetry` | interface, struct | `{1s, 1h, 0.1}`; `InjectorOpts.PublicationRetryPolicy` |
| `EventCodec`, `RegisterEventType[E](root, name, opts...) error`, `EventTypeOption`, `EventVersion(n)` | SPI, func | JSON; `InjectorOpts.EventCodec` |
| `PublicationPropagator`, `PublicationFromContext(ctx) (Claimed, bool)` | interface, func | No headers; `InjectorOpts.PublicationPropagator` |
| `InjectorOpts.PublicationStore`, `TxSource`, `PublicationLease`, `PublicationMaxAttempts uint`, `PublicationBatchSize uint`, `PublicationRetryInterval`, `PublicationRetention`, `PublicationIDGenerator`, `RequireDurableStore` | fields | nil, nil, 5m, 10, 100, off, 0 (delete), UUIDv7, false |
| `ErrTxSourceConflict`, `ErrTxNotSupported`, `ErrTxPhaseNotDurable`, `ErrClaimLost`, `ErrListenerNotFound`, `ErrEventTypeNotFound`, `ErrEventTypeConflict`, `ErrInvalidEventType`, `ErrCodecMismatch`, `ErrInvalidPublication`, `ErrDurableStoreRequired`, `ErrRedeliveryNotSupported` | sentinels | n/a |
| package `publicationtest`: `TestStore` | test helper | n/a |

## Test Strategy

Each scenario maps to one Go test; `Examples` rows run as named subtests. All tests run under `-race`; goleak (`TestMain`) covers the retry loop, purge timer and async deferred deliveries; fake clocks drive every time-based test, advanced with `BlockUntil` handshakes so ticks are deterministic.

**`specs/transactional-events/spec.md`**

| Scenario | Test | Kind |
|---|---|---|
| Phases with a bound transaction | `event_tx_test.go: TestTxPhase/<row>` | table-driven, fake `Tx` |
| Deferred listeners keep listener order | `event_tx_test.go: TestTxPhase_Order` | unit |
| Context of a deferred listener | `event_tx_test.go: TestTx_DetachedContext` | unit |
| Deferred failures are reported | `event_tx_test.go: TestTx_DeferredFailures/<row>` | table-driven |
| No transaction or an ended one | `event_tx_test.go: TestTx_Fallback/<row>` | table-driven |
| Which TxSource is used | `event_tx_test.go: TestTxSourceResolution/<row>` | table-driven |
| A TxSource service is built once | `event_tx_test.go: TestTxSourceResolution_Once` | unit |
| Option validation (transactional-events) | `event_tx_test.go: TestTxOptions/<row>` | table-driven |
| Enum names | `event_tx_test.go: TestTxEnums_String/<row>` | table-driven |

**`specs/event-publication-registry/spec.md`**

| Scenario | Test | Kind |
|---|---|---|
| A durable delivery is recorded and completed | `event_publication_test.go: TestDurable_RecordAndComplete` | unit, fake clock |
| Only durable listeners that pass their filter are recorded | `event_publication_test.go: TestDurable_WhatIsRecorded/<row>` | table-driven |
| Interface listeners store the dynamic type | `event_publication_test.go: TestDurable_DynamicType/<row>` | table-driven |
| Durable delivery failure | `event_publication_test.go: TestDurable_DeliveryFailure/<row>` | table-driven |
| Durable failure does not stop other listeners | `event_publication_test.go: TestDurable_FailureDoesNotStop` | unit |
| A non-durable failure leaves durable deliveries recorded | `event_publication_test.go: TestDurable_NonDurableFailure` | unit |
| Recording errors | `event_publication_test.go: TestDurable_RecordingErrors/<row>` | table-driven |
| Registration rules | `event_publication_test.go: TestDurable_Registration/<row>` | table-driven |
| Durable immediate listeners refuse a bound transaction | `event_publication_test.go: TestDurable_ImmediateInTx` | unit |
| Durable delivery and the transaction outcome | `event_publication_tx_test.go: TestDurable_TxOutcome/<row>` | table-driven, fake `TxAwareStore` |
| A store without transaction support refuses a bound transaction | `event_publication_tx_test.go: TestDurable_TxNotSupported` | unit |
| A slow live delivery is not claimed by redelivery | `event_publication_lease_test.go: TestLease_LiveDelivery` | race, fake clock, `testWithTimeout` |
| A lost lease is reported | `event_publication_lease_test.go: TestLease_Lost` | unit |
| Incomplete publications are redelivered once per pass | `event_redelivery_test.go: TestRedelivery_Pass/<row>` | table-driven, `testWithTimeout` |
| Unresolvable publications are failed without blocking Start | `event_redelivery_test.go: TestRedelivery_Unresolvable/<row>` | table-driven, `testWithTimeout` |
| Claim error fails Start | `event_redelivery_test.go: TestRedelivery_ClaimError` | unit |
| No work without durable listeners | `event_redelivery_test.go: TestRedelivery_NoDurableListeners` | unit (compatibility) |
| Attempts up to dead-lettering | `event_deadletter_test.go: TestDeadLetter_Attempts/<row>` | table-driven, fake clock |
| A crash during delivery counts as an attempt | `event_deadletter_test.go: TestDeadLetter_CrashCounts` | unit |
| Resubmitting a dead letter | `event_deadletter_test.go: TestDeadLetter_Resubmit` | unit |
| Exponential schedule without jitter | `event_retry_policy_test.go: TestExponentialRetry/<row>` | table-driven |
| Jitter stays within bounds | `event_retry_policy_test.go: TestExponentialRetry_Jitter` | unit |
| Retry loop lifecycle | `event_retry_test.go: TestRetryLoop/<row>` | table-driven, fake clock, goleak |
| The loop stops before Closing hooks | `event_retry_test.go: TestRetryLoop_StopsBeforeClosing` | unit, goleak |
| Purge timing | `event_retention_test.go: TestRetention/<row>` | table-driven, fake clock |
| The purge bound is inclusive | `event_retention_test.go: TestMemoryStore_PurgeInclusive` | unit |
| Registering event type names | `event_codec_test.go: TestRegisterEventType/<row>` | table-driven |
| Old names still decode after an explicit rename | `event_codec_test.go: TestRegisterEventType_OldNameDecodes` | unit |
| Codec choice | `event_codec_test.go: TestCodec_Choice/<row>` | table-driven |
| JSON codec round trip | `event_codec_test.go: FuzzJSONCodec_RoundTrip` | fuzz |
| Context survives a restart | `event_headers_test.go: TestHeaders_Redelivery` | unit |
| PublicationFromContext outside delivery | `event_headers_test.go: TestPublicationFromContext_Outside/<row>` | table-driven |
| ID format | `event_id_test.go: TestPublicationID/<row>` | table-driven, fake clock |
| Resolution order | `event_store_test.go: TestStoreResolution/<row>` | table-driven |
| The in-memory store is announced and can be forbidden | `event_store_test.go: TestStoreResolution_InMemory/<row>` | table-driven |
| Option validation (event-publication-registry) | `event_publication_options_test.go: TestPublicationOptions/<row>` | table-driven |
| Concurrent durable publishing | `event_publication_race_test.go: TestDurable_Concurrent` | race, `testWithTimeout` |
| Clone store isolation and sharing | `event_publication_clone_test.go: TestClone_Store/<row>` | table-driven |
| Shared store across roots | `event_publication_clone_test.go: TestClone_SharedStoreAcrossRoots` | race, `testWithTimeout` |
| Conformance results | `publicationtest/conformance_test.go: TestTestStore/<row>` | table-driven (runs `TestStore` on a sub-`testing.T` and inspects which subtests failed) |

**Docs and performance**

| Purpose | Test | Kind |
|---|---|---|
| Defaults and overrides in godoc | `event_durable_example_test.go: ExampleListenerDurable`, `ExampleListenerTxPhase`, `ExampleNewMemoryPublicationStore`, `ExampleRegisterEventType`, `ExampleExponentialRetry` | Example |
| Recording overhead | `bench: BenchmarkPublish/durable-{1,10}-listeners` | benchmark (regression baseline) |

**Hot paths and their coverage:** recording (what is recorded, errors, transaction rules), outcomes (complete, fail, lost lease), redelivery (termination, unresolvable rows, claim errors, no-op without durable listeners), leases and concurrency (live vs. retry, shared stores, `-race`), retry and dead letters (fake clock), lifecycle placement (retry loop vs. Closing hooks, goleak), store resolution (order, failure, in-memory guard), and the conformance suite for third-party stores.

## Migration Plan

Additive; nothing to migrate. Applications adopt it per listener with `ListenerDurable()`; production deployments add a crash-safe store module (follow-up) or their own `PublicationStore` validated with `publicationtest.TestStore`. Rollback means removing `ListenerDurable()`; stored rows are then ignored (no durable listeners, no redelivery pass).

## Open Questions

- Lease renewal for long deliveries (an optional `PublicationLeaseRenewer` interface, renewed by the root while a delivery runs). It would be a new optional interface and new requirement, not a change to these, so it can follow without reworking this change.
- Whether the first crash-safe store is one `database/sql` module with a portable dialect subset or one module per database; decided by the follow-up change that ships it.
