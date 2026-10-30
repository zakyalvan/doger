# Design

## Context

See proposal.md (Why). The parts of today's code that shape this design:

- **Lazy services.** Services are built on first invocation. Spring finds `@EventListener` methods by building every singleton at refresh; doger cannot do that without defeating laziness. Opt-in metadata at registration already exists for Starters (`AsStarter`) and kinds (`kind.With(meta)`) as `ProvideOption`s stored with the registration.
- **`Ordered`** (`di_lifecycle.go`) is documented to affect collections only, and it is read from a built instance. Using it for listener order would force a build to sort, so listener order comes from registration options instead (D3).
- **Container hooks** (`HookContainerStarting` … `HookContainerClosed`) fire at fixed points of `Start` and `Shutdown` (`root_start.go`, the shutdown path). The Starting hooks run before any service is instantiated and can veto `Start`.
- **Clock.** The root has an injectable clock (`container-clock`), used here for publication timestamps, retry and retention, so tests use a fake clock.
- **Clone.** `RootScope.CloneWithOpts` copies the scope tree, then the kind entries (`root_scope.go:756`). The listener registry follows the same pattern.
- **Go method sets.** A type cannot implement both `Listener[A]` and `Listener[B]`, because both need an `OnEvent` method with different parameters. A service that reacts to several event types needs a per-method registration (D2).
- **`add-extensions`** (in progress) creates private child scopes `ext:<name>` under the root. Durable stores ship as extensions, and listeners inside extension scopes must hear application events (D4).

## Goals / Non-Goals

**Goals:**
- Typed publish and listen with no reflection call on the dispatch hot path, and no service built before an event reaches it.
- Synchronous dispatch by default. Async delivery is bounded and leak-free, and is drained at shutdown.
- A durable contract in core (store SPI, codec, type registry, transaction binding, redelivery) that a third-party module can implement without core depending on any driver.
- Every opinionated choice can be overridden through `InjectorOpts`, a listener option or an interface.

**Non-Goals:**
- Exactly-once delivery. Durable delivery is at-least-once, and listeners deduplicate by publication ID.
- Ordering guarantees across async or durable deliveries. Only the order of synchronous calls within one `Publish` is guaranteed.
- Persisting non-durable deliveries, or replaying events to listeners registered later.
- Distributed claiming semantics beyond what the SPI exposes. Lease handling is the store's job (D9).

## Decisions

### Overview

How the pieces fit. Everything above the SPI line is package `doger`, uses only the standard library and works with zero configuration. Everything below is optional and installed by the consumer.

```
+--------------------------- package doger (core) ----------------------------+
|                                                                             |
|  Publish[E](ctx, i, e)                                                      |
|        |                                                                    |
|        v                                                                    |
|  +-------------+   plan    +------------------+   sync   +---------------+  |
|  | dispatcher  |---------->| listener registry|--------->| listeners     |  |
|  | (D1,D5)     |  (cache)  | AsListener,      |          | (built lazily)|  |
|  +-------------+           | ListenMethod,    |   async  +---------------+  |
|        |                   | Listen  (D2-D4)  |--------->| EventExecutor |  |
|        | durable           +------------------+          | (D6)          |  |
|        v                                                 +---------------+  |
|  +------------------+   +-----------+   +-----------+                       |
|  | publication      |-->| EventCodec|   | TxBinder  |  lifecycle events     |
|  | registry (D9)    |   | (json)    |   | (D8)      |  from Start/Shutdown  |
|  +------------------+   +-----------+   +-----------+  (D7)                 |
|        |                                     ^                              |
+--------|-------------------------------------|------------------------------+
         | PublicationStore SPI                | TxBinder SPI
=========|=====================================|=============================== SPI line
         v                                     |
  +---------------------+          +-----------+-----------+
  | in-memory store     |          | dogerevents-postgres  |  installed with
  | (core default,      |          | dogerevents-sqlite    |  doger.Extend (D10)
  |  not crash-safe)    |          | dogerevents-kafka ... |
  +---------------------+          +-----------------------+
```


### D1. Events are plain Go values; dispatch by assignability
- `Publish[E any](ctx context.Context, i Injector, event E) error`. No marker interface and no string topic.
- A listener declared for type `L` receives an event whose **dynamic** type is assignable to `L`. A listener for an interface type such as `DomainEvent` therefore receives every event that implements it, mirroring Spring's supertype matching. Publishing a nil interface value returns `ErrInvalidEvent`.
- **Dispatch plan cache:** the plan maps each dynamic `reflect.Type` to its ordered list of matching listeners. It is computed once and invalidated by a registry version counter when listeners are added or removed. The hot path is one map lookup plus typed closure calls.
- *Alternative:* exact static type only. That is simpler, but listening to an interface type, which is the common case in event-sourced code, would be impossible. Rejected.

```
Publish(ctx, i, OrderPlaced{...})
   |
   +-- event nil?  ------------------------------> ErrInvalidEvent
   |
   +-- closed?  ---------------------------------> ErrPublishAfterShutdown
   |
   +-- plan = cache[dynamic type]   (miss: build from registry, keyed by
   |                                 registry version; hit: one map lookup)
   |
   +-- any durable listener in plan?
   |      yes: encode once -> Append(publications) --fail--> error, nobody runs
   |
   +-- for each listener in plan order:
          filter false?  -> skip (listener not built)
          phase deferred and tx bound?  -> register OnCommit/OnRollback
          async?  -> EventExecutor.Submit
          else    -> resolve listener (lazy build) -> OnEvent(ctx, e)
                     error/panic -> *ListenerError -> error mode (D5)
```

### D2. Declaring listeners
- **`AsListener[E](opts ...ListenerOption) ProvideOption`:** the registered service implements `Listener[E]` (`OnEvent(context.Context, E) error`). A concrete registered type that does not implement it panics at registration, worded like `AsStarter`. A service registered under an interface type is checked on its instance at first dispatch, and a mismatch is a `ListenerError` wrapping `ErrInvalidListener`.
- **`ListenMethod[S, E](method func(S, context.Context, E) error, opts ...ListenerOption) ProvideOption`:** takes a method expression, for example `ListenMethod((*Mailer).OnOrderPlaced)`. This is the Go equivalent of `@EventListener` on a method, and it lets one service listen to many event types. `S` must be the registered type, which is checked at registration.
- **`Listen[E](i Injector, fn func(context.Context, E) error, opts ...ListenerOption) (cancel func())`:** a function listener in the scope of `i`. It returns a cancel function that unregisters it.
- **Laziness and kinds:** each option captures a typed closure `func(ctx, instance any, event any) error`, so dispatch makes no reflection call. A service listener is resolved by name from its own scope when an event first reaches it. A transient service listener gets a new instance per delivery.
- *Alternative:* discover `Listener[E]` by type-checking every instance. Rejected: it builds every service on the first `Publish`.

```
 registration time (nothing is built)          first matching event
 ------------------------------------          --------------------
 Provide(i, NewMailer,                          Invoke "*Mailer" in its scope
   AsListener[OrderPlaced](),        ---->      inst.(Listener[OrderPlaced])
   ListenMethod((*Mailer).OnCancelled))           .OnEvent(ctx, e)
        |                                       (*Mailer).OnCancelled(inst, ctx, e)
        v
 registry entry: {scope, service name, listened type,
                  typed closure, options}
```

### D3. Listener options and order
- Options: `ListenerOrder(n int)`, `ListenerFilter(func(E) bool)` (typed through a generic option), `Async()`, `Durable()`, `ListenerID(id string)`, `Phase(TxPhase)`.
- **Order:** ascending `ListenerOrder` (default 0), then scope depth (the publishing scope first, then its ancestors), then registration order. This is deterministic, needs no build, and is stable.
- **Listener identity** (`ListenerID`) defaults to `<scope path>/<service name>#<event type name>` for service listeners. The scope path joins scope names from the root with `/` (for example `[root]/billing`), and the event type name is the stored name (D9). Scope IDs are not used, because they are not stable across restarts and a durable listener's ID must be. Function listeners have no stable default, so `Durable()` on a function listener without a `ListenerID` panics at `Listen`.

Worked example for a publish through scope `req` (child of root):

```
 listener   scope    ListenerOrder   registered   position
 --------   ------   -------------   ----------   --------
 r2         [root]   -1              2nd          1   lowest order first
 c1         req       0              3rd          2   order 0, req is nearer
 c2         req       0              4th          3   same depth: registration
 *Mailer    req       0 (*)          5th          4
 r1         [root]    0              1st          5   order 0, root is farther

 (*) *Mailer implements Ordered returning -10; it is ignored, because
     reading it would build the listener.
```

### D4. Which listeners an event reaches
- An event published through injector `i` reaches listeners registered in `i`'s scope and its ancestors, plus listeners in the root's extension private scopes (`ext:*`, from `add-extensions`). Sibling and descendant scopes are not reached.
- The rationale: going upward matches doger's resolution direction and Spring's child-to-parent rule, and the extension rule lets an installed extension (a durable store, an audit module) react to application events without the application wiring it.
- A per-publish option `PublishLocal()` restricts delivery to `i`'s own scope.
- *Alternative:* broadcast to the whole container. Rejected: it breaks isolation between sibling scopes (tenants, requests).

```
                        [root]                 reached (ancestor)
                     /     |     \
                    /      |      \
          ext:audit    tenant-a    tenant-b
          reached      reached     NOT reached (sibling branch)
          (extension   (ancestor)
           scope)         |
                         req   <-------------- Publish(ctx, req, e)
                          |                    reached (self)
                       child                   NOT reached (descendant)

 PublishLocal(): only "req" is reached.
```

### D5. Synchronous dispatch and errors
- Synchronous listeners run on the publisher's goroutine in plan order, with the publisher's context.
- **Default error mode `EventFailFast` (the zero value):** the first error stops dispatch and `Publish` returns a `*ListenerError{Listener string; Event reflect.Type; Err error}`. `EventContinue` runs every synchronous listener and returns `errors.Join` of their `*ListenerError`s. The mode is chosen with `InjectorOpts.EventErrorMode`. Making the safe default the zero value follows the `PlacementPrivate` precedent in `add-extensions`, not the "enums start at 1" guideline.
- **Panics** are recovered into a `*ListenerError`. An error panic value is wrapped with `%w`, anything else is formatted with `%v`, as `ExtensionError` does.
- **Re-entrancy:** a listener may call `Publish`. That nested dispatch completes before the outer one continues, as in Spring. No lock is held while listener code runs.

```
 EventFailFast (default)              EventContinue
 -----------------------              -------------
 a ok -> b ERR -> stop                a ok -> b ERR -> c ERR
 return *ListenerError(b)             return errors.Join(LE(b), LE(c))
 c never runs                         every listener runs
```

### D6. Async delivery and the executor
- `type EventExecutor interface { Submit(ctx context.Context, task func(context.Context)) error; Drain(ctx context.Context) error }`.
- **Default executor:** a bounded semaphore (default `runtime.GOMAXPROCS(0) * 4` concurrent deliveries). When the semaphore is full, `Submit` blocks until a slot frees or `ctx` ends. That keeps backpressure on publishers instead of growing an unbounded queue. Each task runs in a goroutine tracked by a `sync.WaitGroup`.
- **Async context:** the task receives `context.WithoutCancel(publishCtx)`, which keeps values but outlives the request, and is cancelled when shutdown's drain deadline is reached.
- **Async errors** have no caller. They go to `InjectorOpts.EventAsyncErrorHandler`, which defaults to ``Logf("DI: async listener `<id>` failed on event `<type>`: <cause>")``. Durable delivery errors are also recorded in the store (D9).
- *Overrides:* `InjectorOpts.EventExecutor` replaces it entirely (a worker pool, an errgroup with a limit, or a synchronous executor for tests).

```
 publisher goroutine                 default executor (bounded, N slots)
 -------------------                 -----------------------------------
 Publish -> Submit(task) --+         +---+---+---+---+
                           |  slot   | 1 | 2 | 3 | 4 |   N = GOMAXPROCS*4
                           +-------->+---+---+---+---+
                           |  full?  block until a slot frees
                           |         or ctx ends -> ctx.Err()
 Publish returns <---------+
                                     task ctx = WithoutCancel(publishCtx)
                                       (values kept, publisher cancel ignored,
                                        cancelled only at drain deadline)
                                     error -> EventAsyncErrorHandler (Logf)
 Shutdown ------------------------>  Drain(ctx): WaitGroup.Wait or ctx end
```

### D7. Lifecycle events and shutdown order
- **Event types:** `ContainerStarting`, `ContainerStarted`, `ContainerReady`, `ContainerFailed{Err error}`, `ContainerClosing`, `ContainerClosed{Report *ShutdownReport}`, each with a `Root *RootScope` field.
- **When they're published:** each is published right **after** the hooks of the same point, from the same goroutine. Lifecycle listeners observe only: their errors and panics go to `EventAsyncErrorHandler` and never change `Start` or `Shutdown` results. The existing hooks keep their veto and their semantics unchanged, so `container-lifecycle-hooks` is not modified.
- **`ContainerStarting`** is published only after every Starting hook passed. Building a listener service at that point is the first instantiation of the `Start` pipeline. If that build fails, the error is reported to the error handler; it does not veto `Start`.
- **Shutdown sequence:**
  1. The Closing hooks run.
  2. `ContainerClosing` is published.
  3. The event system closes to new async submissions (`Publish` with an async listener now returns `ErrPublishAfterShutdown`; synchronous dispatch still works).
  4. `EventExecutor.Drain(shutdownCtx)` waits for in-flight deliveries. If the shutdown context ends first, their contexts are cancelled and the drain error is added to the report.
  5. Services shut down as today.
  6. The Closed hooks run, then `ContainerClosed` is published.
- **After Closed**, service listeners are shut down, so `ContainerClosed` reaches function listeners only, and any later `Publish` returns `ErrPublishAfterShutdown`.

```
 Start                                         Shutdown
 -----                                         --------
 HookContainerStarting (may veto)              HookContainerClosing
 event ContainerStarting                       event ContainerClosing
 build + start services                        close async intake
 kind handlers, Runnables                        (async Publish -> ErrPublishAfterShutdown)
 redelivery of publications (D9)               EventExecutor.Drain(shutdownCtx)
 HookContainerStarted                          stop retry loop (D9)
 event ContainerStarted                        shut services down (as today)
 HookContainerReady                            HookContainerClosed
 event ContainerReady                          event ContainerClosed (function listeners only)
                                               any Publish -> ErrPublishAfterShutdown
 on failure:
 HookContainerFailed -> event ContainerFailed

 Rule: an event always follows the hook of the same point, on the same
 goroutine; event listener errors never change the Start/Shutdown result.
```

### D8. Transaction binding (store-defined binder)
- **Core types:**
  ```go
  type Tx interface { OnCommit(func()); OnRollback(func()) }
  type TxBinder interface { Bind(ctx context.Context) (Tx, bool) }
  ```
  A store module (for example `dogerpg`) provides `WithTx(ctx, *sql.Tx)` and a `TxBinder` that recognises it, and registers the binder through its extension. Core never sees `*sql.Tx`.
- **Phases:** `TxPhase` is `PhaseImmediate` (zero, the default for non-durable listeners), `PhaseAfterCommit` (the default for durable listeners), `PhaseAfterRollback` and `PhaseAfterCompletion`. When `Publish` runs with a bound transaction, listeners with a non-immediate phase are deferred to the matching `OnCommit`/`OnRollback` callback. Without a bound transaction, they run immediately; `TxFallback(TxSkip)` on the listener skips them instead; for a durable listener nothing is recorded.
- **Why this default for missing transactions:** dropping an event silently is the worse surprise for zero-config users. Spring's default (`fallbackExecution=false`) is available as an opt-in.
- *Alternatives considered:* an explicit `PublishTx(ctx, i, tx, e)` adds a second entry point and a transaction type to core, and a core `WithTx(ctx, any)` is untyped. The user chose the store-defined binder.

```
 app code               doger core              store module            DB
 --------               ----------              ------------            --
 BEGIN tx ------------------------------------------------------------> tx
 ctx = dogerpg.WithTx(ctx, tx)
 INSERT order --------------------------------------------------------> tx
 Publish(ctx, e) -----> binder.Bind(ctx) -----> finds tx  (Tx, true)
                        Append(ctx, pubs) ----> INSERT publications --> tx
                        PhaseImmediate: run now
                        PhaseAfterCommit:  tx.OnCommit(deliver)
                        PhaseAfterRollback: tx.OnRollback(deliver)
 COMMIT -------------------------------------> OnCommit callbacks ----> tx
                        deliver -> listener -> Complete(id)

 crash between COMMIT and Complete?  the publication row is committed
 and incomplete, so it is redelivered at the next Start (D9).
 ROLLBACK?  the publication rows vanish with the business rows.
```

### D9. Event publication registry (durable listeners)
- **SPI:**
  ```go
  type Publication struct {
      ID, ListenerID, EventType string
      Payload                   []byte
      PublishedAt               time.Time
      Attempts                  int
      LastError                 string
  }
  type PublicationStore interface {
      Append(ctx context.Context, pubs []Publication) error            // inside the bound tx, if any
      Complete(ctx context.Context, id string, at time.Time) error
      Fail(ctx context.Context, id string, cause error, at time.Time) error
      Claim(ctx context.Context, before time.Time, limit int) ([]Publication, error)
      Purge(ctx context.Context, completedBefore time.Time) error
  }
  ```
  `Claim` returns incomplete publications older than `before`. A multi-instance store implements leasing there, for example with `FOR UPDATE SKIP LOCKED`.
- **Publish path for durable listeners:**
  1. Encode the event once.
  2. Build one `Publication` per matching durable listener.
  3. `Append` them all; the store writes into the transaction it finds in `ctx`.
  4. Schedule delivery by phase (D8).
  5. On success call `Complete`; on error or panic call `Fail` and report a `*ListenerError` to the async error handler. Durable delivery errors never become `Publish`'s result, because the publication stays recorded for redelivery.

  If `Append` fails, `Publish` returns that error and no durable listener runs. If `Append` succeeds but the process dies before delivery, the publication stays incomplete.
- **Redelivery:**
  - At `Start`, after every Starter has started and before the Started hooks, the root claims incomplete publications in batches of `InjectorOpts.PublicationBatchSize` (default 100) until a `Claim` returns none. A `Claim` error fails `Start`. It decodes each one through the type registry and delivers it to the listener named by `ListenerID`.
  - A publication whose listener no longer exists, whose type is not registered, or whose payload cannot be decoded gets `Fail` with `ErrListenerNotFound`, `ErrEventTypeNotRegistered` or the codec's error, and is left for the operator. It never blocks `Start`.
  - An optional periodic retry (`InjectorOpts.EventRetryInterval`, default 0 = off) runs a single goroutine owned by the root. It backs off exponentially per publication, and is stopped at step 3 of shutdown.
- **Codec and type registry:**
  - `type EventCodec interface { Encode(any) ([]byte, error); Decode([]byte, reflect.Type) (any, error) }`, defaulting to `encoding/json`.
  - `RegisterEventType[E](root *RootScope, name string) error` names a type for storage. It returns `ErrEventTypeConflict` when the name belongs to another type or the type already has another explicit name, and `ErrInvalidEvent` for an empty name. A durable listener's type is registered automatically under its default name.
  - **Stored names vs. messages:** the default stored name is the import-path-qualified form (`github.com/kartaladev/doger.OrderPlaced`), used for `Publication.EventType` and inside default `ListenerID`s. The short `reflect` form (`doger.OrderPlaced`) appears only in error and log messages. The short form would collide for two same-named types in packages of the same name (two `events.OrderPlaced`), and a collision in stored data is unrecoverable. An explicit name keeps stored events decodable after the type is renamed or moved.
- **Default store:** a mutex-guarded in-memory store. It has the same semantics and suits zero config and tests, but its records are lost on restart, which the godoc states plainly.
- **IDs:** 128 random bits from `crypto/rand` as hex, replaceable through `InjectorOpts.PublicationIDGenerator`.
- **Retention:** completed publications are purged immediately by default. `InjectorOpts.PublicationRetention` keeps them for that long and purges them on the retry tick, or at `Start` when retry is off.

Publication states:

```
                 Append (in tx, if bound)
                         |
                         v
                  +-------------+   deliver ok    +-----------+   retention   +--------+
                  | INCOMPLETE  |---------------->| COMPLETED |-------------->| PURGED |
                  | Attempts=n  |    Complete     +-----------+     Purge     +--------+
                  +-------------+
                     ^      |
                     |      | deliver error/panic, decode error,
                     +------+ listener or type not found:
                       Fail   Attempts=n+1, LastError set
                     (claimed again at the next Start or retry tick,
                      after backoff: PublishedAt + interval * 2^(n-1))
```

Redelivery loop at `Start` (and on each retry tick):

```
 loop:
   batch = Claim(ctx, now, PublicationBatchSize)   -- error: Start fails
   if len(batch) == 0: done
   for p in batch:
     E    = typeRegistry[p.EventType]   -- missing: Fail(ErrEventTypeNotRegistered)
     l    = listeners[p.ListenerID]     -- missing: Fail(ErrListenerNotFound)
     e    = codec.Decode(p.Payload, E)  -- error:   Fail(decode error)
     err  = l.OnEvent(ctxWith(p), e)
     err == nil ? Complete(p.ID) : Fail(p.ID, err)
```

### D10. Configuration and installation of a store
- Core resolves the store once per root, at the first durable `Publish` or at `Start`, in this order: `InjectorOpts.PublicationStore`; else a service registered under `NameOf[PublicationStore]()`; else the in-memory default. The same rule applies to `TxBinder`, whose default is "never bound". This mirrors the `PropertySource` decision in `add-extensions`, so an extension can register an `AsDefault()` store that the application can still override.
- A store extension is installed with `doger.Extend(root, dogerpg.Extension(db))`. It registers the store, the binder and (optionally) a codec under the published names.

```
 first durable Publish or Start
            |
            v
   InjectorOpts.PublicationStore set? --yes--> use it
            | no
            v
   service named NameOf[PublicationStore]() --yes--> use it
   in root? (app service beats an AsDefault()          (e.g. installed by
   one registered by an extension)                      dogerevents-postgres)
            | no
            v
   in-memory store + one-time Logf warning

 TxBinder: same chain, final fallback "never bound".
```

### D11. Clone
`CloneWithOpts` copies the listener registry after the scope tree. Service listener entries are rebound to the clone's scopes; function listeners are copied as-is (the same closure). The plan cache, executor, in-flight deliveries and the resolved store are not copied: the clone resolves its own on first use.

## Risks / Trade-offs

- [At-least-once means duplicate deliveries after a crash] → the publication ID is passed to listeners via `PublicationFromContext(ctx)`. The guide shows deduplication.
- [Building a listener service at `ContainerStarting` instantiates a service before Start's discovery] → documented. A service whose instantiation must wait for `Start` should listen to `ContainerStarted` instead.
- [Without a store extension, the in-memory store is silently non-durable] → `Logf` warns once, at the first durable `Publish`, that the in-memory store is in use. The godoc and guide say it.
- [The default `ListenerID` embeds the service name and event type name, so renaming either orphans stored publications] → `ListenerID` and `RegisterEventType` names are the stable override. Orphans fail with `ErrListenerNotFound` rather than being lost.
- [Blocking backpressure in `Submit` can slow publishers under load] → bounded by `ctx`. Replace the executor for drop or queue policies.
- [`ContainerClosed` cannot reach service listeners] → documented. Function listeners cover it.
- [A listener that calls `Publish` recursively without end overflows the stack] → not detected, the same as recursive function calls. Documented.

## Public API & Defaults

Additive only. **No exported API breaks.** `Ordered` keeps its collection-only meaning.

| Identifier | Kind | Default / override |
|---|---|---|
| `Publish[E](ctx, i, e, opts ...PublishOption) error` | func | Sync, fail-fast |
| `PublishLocal()` | `PublishOption` | Off: ancestors and extension scopes are reached |
| `Listener[E]` | interface | n/a |
| `AsListener[E](opts...)`, `ListenMethod[S,E](m, opts...)` | `ProvideOption` | Lazy build on first event |
| `Listen[E](i, fn, opts...) func()` | func | Returns its own unregister function |
| `ListenerOrder`, `ListenerFilter`, `Async`, `Durable`, `ListenerID`, `Phase`, `TxFallback` (`TxRun` default, `TxSkip`) | `ListenerOption` | Order 0, no filter, sync, not durable, derived ID, `PhaseImmediate` (`PhaseAfterCommit` when durable), run when there's no transaction |
| `ListenerError{Listener string; Event reflect.Type; Err error}` | error type | Message ``DI: listener `<id>` failed on event `<type>`: <cause>``; `Unwrap` returns `Err` |
| `ErrInvalidEvent`, `ErrInvalidListener`, `ErrPublishAfterShutdown`, `ErrListenerNotFound`, `ErrEventTypeNotRegistered`, `ErrEventTypeConflict` | sentinels | n/a |
| `ContainerStarting` … `ContainerClosed` | event types | Published after the matching hooks |
| `EventExecutor` | interface | Bounded semaphore; `InjectorOpts.EventExecutor` |
| `EventErrorMode` (`EventFailFast`, `EventContinue`) | enum | Fail-fast; `InjectorOpts.EventErrorMode` |
| `InjectorOpts.EventAsyncErrorHandler` | field | `Logf` |
| `Tx`, `TxBinder`, `TxPhase` | types | Never bound; a service under `NameOf[TxBinder]()` |
| `PublicationStore`, `Publication`, `PublicationFromContext` | SPI | In memory; `InjectorOpts.PublicationStore` or a named service |
| `EventCodec`, `RegisterEventType[E](root, name) error` | SPI / func | `encoding/json`, import-path type names; `InjectorOpts.EventCodec`, explicit names |
| `InjectorOpts.EventRetryInterval`, `PublicationRetention`, `PublicationIDGenerator`, `PublicationBatchSize` | fields | Off, 0, `crypto/rand` hex, 100 |

## Test Strategy

Every `#### Scenario:` in both specs maps to one Go test; `Examples` rows run as named subtests. All tests run under `-race`, and `TestMain`'s goleak check covers every async, drain and retry test.

**`specs/events/spec.md`**

| Scenario | Test | Kind |
|---|---|---|
| Publish matches listeners by the event's dynamic type | `event_test.go: TestPublish_Assignability/<row>` | table-driven |
| Publish with no listeners succeeds | `event_test.go: TestPublish_NoListeners` | unit |
| Publish rejects or normalizes zero arguments | `event_test.go: TestPublish_ZeroArguments/<row>` | table-driven |
| AsListener validates the registered type | `event_listener_test.go: TestAsListener_Registration/<row>` | table-driven |
| Interface-typed listener is checked at first dispatch | `event_listener_test.go: TestAsListener_InterfaceCheckedAtDispatch` | unit |
| Repeating AsListener for the same type registers one listener | `event_listener_test.go: TestAsListener_Repeated` | unit |
| Listener build follows the service kind | `event_listener_test.go: TestListener_LazyBuild/<row>` | table-driven |
| Listener provider error fails the delivery | `event_listener_test.go: TestListener_ProviderError` | unit |
| One service listens to several event types | `event_listener_test.go: TestListenMethod_MultipleEvents` | unit |
| ListenMethod validates its input | `event_listener_test.go: TestListenMethod_Registration/<row>` | table-driven |
| Cancel unregisters a function listener | `event_listener_test.go: TestListen_Cancel/<row>` | table-driven |
| Listen rejects a nil function | `event_listener_test.go: TestListen_NilFunc` | unit |
| Listeners run in order, depth, then registration order | `event_dispatch_test.go: TestDispatch_Order` | unit |
| Filter decides delivery | `event_dispatch_test.go: TestDispatch_Filter/<row>` | table-driven |
| Reach depends on where the listener lives | `event_dispatch_test.go: TestDispatch_ScopeReach/<row>` | table-driven |
| First error stops dispatch | `event_dispatch_test.go: TestDispatch_FailFast` | unit |
| Errors are joined in continue mode | `event_dispatch_test.go: TestDispatch_ErrorMode/<row>` | table-driven |
| Panic becomes a listener error | `event_dispatch_test.go: TestDispatch_Panic/<row>` | table-driven |
| Nested publish runs inline | `event_dispatch_test.go: TestDispatch_Nested` | unit |
| Async listener runs after Publish returns | `event_async_test.go: TestAsync_RunsAfterPublish` | race, `testWithTimeout` |
| Async errors go to the async error handler | `event_async_test.go: TestAsync_ErrorHandler/<row>` | table-driven, race |
| Backpressure at the limit | `event_async_test.go: TestAsync_Backpressure/<row>` | table-driven, race, `testWithTimeout` |
| Custom executor receives async deliveries | `event_async_test.go: TestExecutor_Custom` | unit |
| Submit error is returned | `event_async_test.go: TestExecutor_SubmitError` | unit |
| Lifecycle events follow their hooks | `event_lifecycle_test.go: TestLifecycleEvents_Order/<row>` | table-driven |
| Lifecycle listeners never change Start or Shutdown results | `event_lifecycle_test.go: TestLifecycleEvents_Observational/<row>` | table-driven |
| Existing hooks behave as before | `event_lifecycle_test.go: TestLifecycleEvents_HooksUnchanged` | unit (compatibility) |
| In-flight deliveries finish before services shut down | `event_async_test.go: TestShutdown_Drain/<row>` | table-driven, race, goleak |
| Publishing during and after shutdown | `event_async_test.go: TestShutdown_PublishAfter/<row>` | table-driven |
| ContainerClosed reaches function listeners only | `event_lifecycle_test.go: TestLifecycleEvents_ClosedFunctionOnly` | unit |
| Concurrent publishers and registrations | `event_race_test.go: TestPublish_ConcurrentRegistration` | race, `testWithTimeout` |
| Clone delivers to its own listeners | `event_clone_test.go: TestClone_Listeners/<row>` | table-driven |

**`specs/event-publication-registry/spec.md`**

| Scenario | Test | Kind |
|---|---|---|
| A durable delivery is recorded and completed | `event_publication_test.go: TestDurable_RecordAndComplete` | unit, fake clock |
| Only durable listeners are recorded | `event_publication_test.go: TestDurable_OnlyDurableRecorded/<row>` | table-driven |
| Failed durable delivery stays incomplete | `event_publication_test.go: TestDurable_DeliveryFailure/<row>` | table-driven |
| Encode or Append error | `event_publication_test.go: TestDurable_RecordingFailure/<row>` | table-driven |
| Listener IDs | `event_publication_test.go: TestDurable_ListenerID/<row>` | table-driven |
| Registering event type names | `event_codec_test.go: TestRegisterEventType/<row>` | table-driven |
| Codec choice | `event_codec_test.go: TestCodec_Choice/<row>` | table-driven |
| JSON codec round trip | `event_codec_test.go: FuzzJSONCodec_RoundTrip` | fuzz |
| Phases with a bound transaction | `event_tx_test.go: TestTxPhase/<row>` | table-driven, fake `Tx` |
| Durable records are written through the bound transaction | `event_tx_test.go: TestTx_DurableAppendInTx` | unit |
| Fallback without a transaction | `event_tx_test.go: TestTx_Fallback/<row>` | table-driven |
| Incomplete publications are redelivered | `event_publication_test.go: TestRedelivery_Start/<row>` | table-driven |
| Unresolvable publications fail without blocking Start | `event_publication_test.go: TestRedelivery_Unresolvable/<row>` | table-driven |
| Redelivery errors are recorded, not returned | `event_publication_test.go: TestRedelivery_ListenerError` | unit |
| Claim error fails Start | `event_publication_test.go: TestRedelivery_ClaimError` | unit |
| Retry with exponential backoff | `event_retry_test.go: TestRetry_Backoff/<row>` | table-driven, fake clock |
| Retry loop stops at shutdown | `event_retry_test.go: TestRetry_StopsAtShutdown` | goleak, fake clock |
| Purge timing | `event_retry_test.go: TestRetention_Purge/<row>` | table-driven, fake clock |
| Resolution order | `event_publication_test.go: TestStoreResolution/<row>` | table-driven |
| In-memory store warns once | `event_publication_test.go: TestStoreResolution_InMemoryWarning` | unit |
| Publication ID and context | `event_publication_test.go: TestPublicationFromContext/<row>` | table-driven |
| Concurrent durable publishing | `event_race_test.go: TestDurable_ConcurrentPublish` | race, `testWithTimeout` |
| Clone store isolation | `event_clone_test.go: TestClone_StoreIsolation` | unit |

**Docs and performance**

| Purpose | Test | Kind |
|---|---|---|
| Defaults and overrides in godoc | `event_example_test.go: ExamplePublish`, `ExampleListenMethod`, `ExampleListen`, `ExampleDurable`, `ExampleEventExecutor` | Example |
| Dispatch hot path baseline | `bench: BenchmarkPublish/{1,10,100}-listeners` | benchmark (regression baseline, no target) |

**Hot paths and their coverage:**
- **Publish / dispatch:** assignability, order, error modes, plan cache invalidation under concurrent registration (`-race`).
- **Listener resolution:** laziness, transient listeners, interface-typed registrations.
- **Lifecycle:** event placement relative to hooks, drain at shutdown, retry goroutine stop (goleak).
- **Scope traversal:** the scope-reach table.
- **Durable path:** append before dispatch, complete or fail, redelivery, transaction phases.

## Migration Plan

Additive; nothing to migrate. Applications with a hand-rolled bus can adopt the new API incrementally. `examples/event-driven` is rewritten on the new API to show this (the workspace module list is unchanged). Rollback means not using the API.

## Open Questions

- The default executor bound (`GOMAXPROCS*4`) is a starting point to revisit with the benchmark. Changing it changes no spec, because the bound is overridable and not part of the observable contract beyond "bounded".
- Whether a `database/sql` store with a portable dialect subset belongs in a first-party `dogerevents-sql` module or in per-database modules. This is decided in the follow-up change that ships the first store.
