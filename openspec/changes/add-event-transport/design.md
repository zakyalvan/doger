# Design

## Context

See proposal.md (Why) and the four specs. This change builds on `add-eventing`, which must be archived first:

- **Listener bindings** live on service wrappers (`serviceOptions.listeners`) and in per-scope function-listener lists. Dispatch builds immutable plans per (scope, dynamic type, local). `HookEventError` receives failures with no caller. This change adds a delivery mode to bindings, a filter in plan building that drops remote listeners, and an externalization step after dispatch.
- **Start and shutdown.** `runStart` starts every Starter, then `dispatchAfterStart` runs the Started hooks and `markReady`. The health scheduler starts at `markReady` (`startScheduling`) and stops before the Closing hooks (`stopScheduling`). Subscriptions must open where a failure can still fail `Start`, so they open at the end of `runStart`, not at `markReady`.
- **Clock.** The root clock (`clockwork.Clock`) stamps `PublishedAt` and the UUIDv7 time, so tests use a fake clock.
- **`AsDefault`** registrations yield to explicit ones (`add-default-registrations`), so an adapter extension can export an overridable transport under `NameOf[EventTransport]()`. `scheduler/gocron` is the precedent for a core interface implemented by a separate module.
- **Superseded work.** The transaction-bound listener design of `add-durable-events` (its D1–D3, D11, D13) carries over almost unchanged. Its store SPI, claims, leases, redelivery pass, retry loop and purge timer do not.

### Industry practice (research behind the strategies)

| Framework | Default send | Outbox | Skip the default per message |
|---|---|---|---|
| Spring Modulith | After commit (`externalization.mode=module-listener`), a registry row written in the transaction | `mode=outbox` (Namastack, JobRunr) | n/a |
| MassTransit | Directly to the broker | Bus Outbox, opt-in | `[SkipOutbox]` |
| NServiceBus | Immediately outside handlers, batched inside | Outbox, opt-in per endpoint; `TransactionalSession` outside handlers | `RequireImmediateDispatch()` |
| watermill | Directly to the broker | SQL publisher on a `*sql.Tx` + `forwarder` | n/a |

All of them make the outbox opt-in configuration and deduplicate on the consumer side by message ID. Sources: [Spring Modulith events](https://docs.spring.io/spring-modulith/reference/events.html), [MassTransit outbox](https://masstransit.io/documentation/patterns/transactional-outbox), [NServiceBus outbox](https://docs.particular.net/nservicebus/outbox/), [watermill forwarder](https://watermill.io/advanced/forwarder/).

## Goals / Non-Goals

**Goals:**
- One seam small enough that a watermill, msgin or SQL adapter is a thin module.
- Both "one instance reacts" and "every instance reacts" without the publisher knowing the consumers.
- Containers that externalize nothing and have no remote listener do no new work and allocate nothing new.

**Non-Goals:**
- A durable store in core. Durability is the transport's property (SQL transport, watermill forwarder, msgin outbox).
- Exactly-once delivery, ordering, partition keys, request-reply.
- Consumer-side deduplication in core; listeners use `EnvelopeFromContext(ctx).ID`.

## Decisions

### Overview

```
+---------------------------------- package doger -----------------------------------+
|                                                                                    |
|  Publish(ctx, i, e) --> add-eventing dispatch (remote listeners filtered out)      |
|        |                                                                           |
|        +--> externalized type? (D2) --no--> done                                       |
|        |        | yes                                                              |
|        |        v                                                                  |
|        |   strategy (D4) --> Immediate: Send now ----------------+                 |
|        |                 --> AfterCommit: Tx.OnCommit(Send) -----+                 |
|        |                 --> Outbox: SendTx in caller's tx ------+                 |
|        |                                                         |                 |
|  Start: open Subscribe per remote listener (D6) <---------+      |                 |
|  handler(ctx, env) --> decode (D3) --> one listener (D7)  |      |                 |
+-----------------------------------------------------------|------|-----------------+
                                       EventTransport SPI    |      |
=============================================================|======|==================
   NewMemoryEventTransport (core)    events/sql    events/watermill    events/msgin
```

### D1. The seam
```go
type Envelope struct {
    ID, Destination, EventType, ContentType string
    Version     int
    Payload     []byte
    Headers     map[string]string
    PublishedAt time.Time
}

type EventDelivery int // DeliverLocal (zero), DeliverOnce, DeliverAll

type Subscription struct {
    Destination string
    Group       string
    Mode        EventDelivery // DeliverOnce or DeliverAll
    Ephemeral   bool          // DeliverAll with a generated instance ID
}

type EnvelopeHandler func(ctx context.Context, env Envelope) error

type EventTransport interface {
    Send(ctx context.Context, env Envelope) error
    Subscribe(ctx context.Context, sub Subscription, h EnvelopeHandler) (stop func(context.Context) error, err error)
}

type TxEventSender interface {
    EventTransport
    SendTx(ctx context.Context, env Envelope) error // writes in the transaction found in ctx
    TxSource() TxSource
}
```
- Two methods because every adapter studied has exactly a publish and a subscribe primitive (watermill `Publisher`/`Subscriber`, msgin `OutboundAdapter`/source, Kafka producer/consumer). Splitting them into two interfaces was considered; one interface keeps resolution and the conformance suite simple, and a send-only transport can return `ErrSubscriptionNotSupported` from `Subscribe`.
- The handler's return value is the acknowledgement contract: nil means ack, an error means nack, a permanent error (D8) means dead-letter or drop. This maps onto watermill `Ack`/`Nack`, msgin `Ack`/`Nack(requeue, delay)` and Kafka offset commits without core knowing any of them.
- `stop` mirrors `HealthScheduler`'s stop function: it must return only after handlers in flight finish or its context ends.
- `Subscribe` receives the subscription context, cancelled when the subscription stops. The handler's context derives from the transport's message context, so the transport controls handler deadlines.
- *Alternative:* a generic `EventTransport[T]`. Rejected: a transport carries every event type, and the payload is bytes by the time it reaches the transport.

### D2. Registry and externalize opt-in
- `RegisterEventType[E](root, name, opts...)` stores, under a mutex-protected copy-on-write map published through an `atomic.Pointer`, name → type and type → (name, version, destination, strategy, externalized). Plans read the pointer without locks, so registration never blocks dispatch.
- Interface types are rejected because decoding needs a concrete type. Registering an externalized type is how a team states a wire contract; *alternative (externalizing everything once a transport is set)* was rejected in the exploration because it leaks internal events and names destinations after Go import paths.
- `EventAlias` replaces the automatic import-path alias of `add-durable-events`: only explicitly registered types travel, so an import-path alias has nothing to keep decoding.

### D3. Envelope, codec, IDs, headers
- Carried over from `add-durable-events` D11 and D13, renamed: `EventCodec` (JSON default), `InjectorOpts.EventIDGenerator` (UUIDv7: 48-bit Unix ms from the root clock, version and variant bits, 74 random bits from `crypto/rand`), `InjectorOpts.EventPropagator` (`Inject`/`Extract`).
- Decoding uses `reflect.New(T).Interface()` and dereferences once, so pointer types round-trip as pointers.
- On receive, a content type different from the codec's, an unknown type name, and an undecodable payload are permanent errors (D8): retrying cannot fix them.

### D4. Externalization strategies
```
strategy = PublishExternalize ?? type.EventExternalize ?? InjectorOpts.EventExternalize ?? ExternalizeAuto
ExternalizeAuto:   tx found && transport is TxEventSender ? ExternalizeOutbox : ExternalizeImmediate
ExternalizeOutbox: no tx ? (fallback Immediate ? ExternalizeImmediate : ErrNoTransaction)
ExternalizeAfterCommit: no tx ? ExternalizeImmediate : Tx.OnCommit(send with Detach(ctx))
```
- The externalization runs after synchronous in-process dispatch, on the publisher's goroutine, so a fail-fast listener error stops it and `EventContinue` joins errors (spec "Externalization follows in-process dispatch outcome"). Running it first was considered; it would publish an event whose local handling the caller is about to see fail.
- `ExternalizeImmediate` and `ExternalizeOutbox` return errors as `*ExternalizeError{EventType, Destination string; Strategy ExternalizeStrategy; Err error}`; `ExternalizeAfterCommit` sends them to `HookEventError`. Panics in `Send`, `SendTx`, the codec, the ID generator and `Inject` are recovered into the same error.
- `ExternalizeAfterCommit` keeps no record, so a crash between commit and send loses the event. It exists for broker-only setups that must not emit events for rolled-back transactions; the godoc says "at most once on crash" and points to `ExternalizeOutbox`.
- `ErrOutboxNotSupported` is checked at `Start` for global and per-type outbox settings, and per publish for `PublishExternalize(ExternalizeOutbox)`.

### D5. Remote listeners and groups
- `ListenerDelivery(m)` sets `delivery` on the existing listener binding. Plan building skips bindings whose `delivery != DeliverLocal`, so in-process dispatch never reaches them and no extra branch runs per listener at dispatch time.
- Registration checks reuse the `add-eventing` registration hook: root or extension scope (through `root.ext`, any entry state), explicit ID for function listeners, no async, no deferred phase, not after `Start`. The "not after Start" rule keeps subscription management in one place (`Start`/shutdown); dynamic remote listeners can come later as a new requirement without changing these.
- Group = `EventGroupPrefix + listenerID` (+ `"/" + InstanceID` for `DeliverAll`). The prefix exists because default listener IDs are derived from Go names, which two services sharing a library could both produce.
- `InstanceID` defaults to a UUIDv7 per root. `Ephemeral` tells the transport it may use non-durable consumers and clean them up (NATS core subscription, Kafka group without committed offsets, a SQL group row deleted on stop).
- **A new group starts at the latest event** for both modes. Found while researching watermill-sql: it seeds a new group at offset 0 ("zero offsets"), so an ephemeral `DeliverAll` group would replay the topic on every deploy. The conformance suite enforces this; adapters seed new groups at the head.

### D6. Subscription lifecycle
```
runStart:  Starters ok --> validate remote listeners (ErrNoEventTransport, ErrEventTypeNotFound,
                           ErrListenerGroupConflict, ErrOutboxNotSupported, ErrTxSourceConflict)
                       --> Subscribe each, in registration order
                           error --> stop opened ones (reverse order), return error (Failed hooks)
                       --> Started hooks, markReady, Ready hooks
Shutdown:  stopScheduling --> stop subscriptions (reverse order, shutdown ctx) --> Closing hooks --> services
```
- Stop errors and panics go to `HookEventError`; shutdown continues.
- Subscriptions are held in a lazily allocated `eventTransportState` on the root, nil for containers without remote listeners.

### D7. Inbound dispatch
- Each subscription gets a handler closure bound to one listener binding: decode, `Extract` headers, attach the envelope (`EnvelopeFromContext`), evaluate the filter, resolve the listener (service listeners through the root, so lazy services build once under the existing wrapper mutex), call it with panic recovery.
- The handler wraps failures in `*ListenerError` with the listener ID and event type, preserving permanent markers so `IsPermanentError` sees through it.

### D8. Permanent errors
- `PermanentError(err)` wraps in an unexported type with `Unwrap`; `IsPermanentError` uses `errors.As`. Same shape as msgin's `Permanent` and watermill users' `Ack`-on-error handling, so adapters map it directly.

### D9. In-memory transport
- `NewMemoryEventTransport(opts...)` keeps, per destination, a list of groups; each group has members (subscriptions) and a cursor for round robin. `Send` copies the envelope to each existing group's queue (so groups created later never see it: start at latest). Each subscription runs one goroutine draining its queue; `stop` cancels and waits.
- Redelivery: up to `MemoryMaxAttempts(n)` (default 3) immediate attempts, then drop and log through `MemoryLogger(func(string, ...any))`, defaulting to the standard `log` package.
- It is externalized because tests of applications, of core, and of adapters (as a reference) need it. It is not `TxEventSender`; core tests wrap it with a fake transaction to exercise the outbox.

### D10. Conformance suite
- `eventtransporttest.Run(t *testing.T, factory func(t *testing.T) doger.EventTransport, opts ...Option)` runs subtests named in the spec, using only the standard library so adapter modules do not inherit test dependencies.
- `WithTx(harness)` enables the outbox subtests; the harness begins, commits and rolls back a transaction and returns a context the transport's `TxSource` recognizes.
- The suite's broken-transport tests use a recording `testing.TB` wrapper to assert a subtest fails.

### D12. Publisher handle and hexagonal applications
```go
type Publisher struct{ /* injector */ }
func PublisherFor(i Injector) *Publisher
func (p *Publisher) Publish(ctx context.Context, events ...any) error
```
- Every hexagonal application writes the same driven adapter around `Publish`; the handle is that adapter, returned as a concrete type (accept interfaces, return structs) so the application's own port interface, declared in its application layer, is satisfied without the domain importing doger.
- Events are published in order and the first error stops the rest, because the events of one use case usually belong to one transaction that the caller will roll back.
- The transaction travels in `ctx`: a unit-of-work adapter binds it (for `database/sql`, `sqltx.InTx` from the shared `events/sqltx` module), so `ExternalizeAuto` picks the outbox with no knowledge in the use case. Publishing after the unit of work returns runs outside the transaction; the guide calls this out.
- *Alternative:* a generic `Publisher[E]`. Rejected: a use case publishes several event types at once.

### D11. Clone
`CloneWithOpts` copies bindings (with delivery modes) and the registry. A transport set in `InjectorOpts` is shared because the caller owns it; subscription state, the instance ID (unless set) and the resolved service transport are per root.

## Risks / Trade-offs

- [A strategy default the user did not expect: `ExternalizeAuto` sends immediately when a transaction is bound but the transport has no outbox, which can emit an event for a rolled-back transaction] → the godoc of `ExternalizeAuto` says so; `ExternalizeAfterCommit` or an outbox-capable transport fixes it; the "Auto resolution" scenario pins the rule.
- [Default listener IDs change when code is renamed, which renames `DeliverOnce` groups and starts them at the latest event, skipping events published during the deploy] → the guide recommends an explicit `ListenerID` for every remote listener; a stable name is a one-line option.
- [Ephemeral groups accumulate in transports that cannot clean up] → `Ephemeral` is passed so adapters can clean up; the SQL transport change owns its cleanup policy.
- [Handlers run on transport goroutines with transport-chosen concurrency] → documented; core is safe for concurrent handler calls ("Inbound deliveries may run concurrently").
- [`ExternalizeAfterCommit` loses events on a crash] → documented as at-most-once; `ExternalizeOutbox` is the durable choice.

## Public API & Defaults

Additive only. **No exported API breaks.**

| Identifier | Kind | Default / override |
|---|---|---|
| `Envelope`, `Subscription`, `EnvelopeHandler` | types | n/a |
| `EventTransport`, `TxEventSender` | SPI | None; `InjectorOpts.EventTransport` or a `NameOf[EventTransport]()` service |
| `NewMemoryEventTransport(opts ...MemoryTransportOption) *MemoryEventTransport`, `MemoryMaxAttempts(n)`, `MemoryLogger(fn)` | constructor, options | 3 attempts, standard `log` |
| `RegisterEventType[E](root, name, opts ...EventTypeOption) error` | func | n/a |
| `EventExternalized()`, `EventExternalize(ExternalizeStrategy)`, `EventVersion(n)`, `EventDestination(d)`, `EventAlias(names...)` | `EventTypeOption` | not externalized, version 1, destination = name, no aliases |
| `ExternalizeStrategy` (`ExternalizeAuto`, `ExternalizeImmediate`, `ExternalizeAfterCommit`, `ExternalizeOutbox`), `PublishExternalize(s)` | enum, `PublishOption` | `ExternalizeAuto` |
| `OutboxFallback` (`OutboxFallbackFail`, `OutboxFallbackImmediate`) | enum | Fail |
| `EventDelivery` (`DeliverLocal`, `DeliverOnce`, `DeliverAll`), `ListenerDelivery(m)` | enum, `ListenerOption` | `DeliverLocal` |
| `EventCodec`, `EventPropagator` | SPI | JSON; no headers |
| `EnvelopeFromContext(ctx) (Envelope, bool)` | func | n/a |
| `PermanentError(err) error`, `IsPermanentError(err) bool` | funcs | n/a |
| `PublisherFor(i) *Publisher`, `(*Publisher).Publish(ctx, events ...any) error` | func, method | nil injector = `DefaultRootScope` |
| `ExternalizeError` | error type | `errors.As`, `Unwrap` |
| `Tx`, `TxSource`, `TxPhase` (+ constants), `ListenerTxPhase`, `TxFallbackMode` (+ constants), `ListenerTxFallback` | SPI, enums, options | Immediate; Run |
| `InjectorOpts.EventTransport`, `EventExternalize`, `EventOutboxFallback`, `EventCodec`, `EventIDGenerator`, `EventPropagator`, `EventDestinationMapper`, `EventGroupPrefix`, `InstanceID`, `TxSource` | fields, copied by `copy()` | nil, Auto, Fail, JSON, UUIDv7, nil, identity, "", generated, nil |
| `ErrNoEventTransport`, `ErrEventTypeNotFound`, `ErrEventTypeConflict`, `ErrInvalidEventType`, `ErrInvalidEnvelope`, `ErrCodecMismatch`, `ErrNoTransaction`, `ErrOutboxNotSupported`, `ErrListenerGroupConflict`, `ErrSubscriptionNotSupported`, `ErrTxSourceConflict` | sentinels | n/a |
| package `eventtransporttest`: `Run`, `Option`, `WithTx`, `TxHarness`, `WithoutDeliverAll` | test helper | all checks; `WithoutDeliverAll` swaps the DeliverAll checks for a rejection check |

## Test Strategy

Each scenario maps to one Go test; `Examples` rows run as named subtests. Everything runs under `-race`; `TestMain`'s goleak check covers subscriptions, the in-memory transport and after-commit sends. Fake clocks drive envelope times; channel handshakes replace sleeps. Shared fakes live in `event_transport_fakes_test.go`: a recording transport, a fake `Tx`/`TxSource`, and a TxEventSender wrapper around the in-memory transport.

**`specs/event-types/spec.md`**

| Scenario | Test | Kind |
|---|---|---|
| Registering event types | `event_types_test.go: TestRegisterEventType/<row>` | table-driven |
| A nil root means DefaultRootScope | `event_types_test.go: TestRegisterEventType_NilRoot` | unit |
| Version, destination and aliases | `event_types_test.go: TestEventTypeOptions/<row>` | table-driven |
| An alias bound to another type conflicts | `event_types_test.go: TestEventTypeOptions_AliasConflict` | unit |
| The mapper applies to both sides | `event_types_test.go: TestDestinationMapper/<row>` | table-driven |
| Round trip through the codec | `event_envelope_test.go: TestCodec_RoundTrip/<row>` | table-driven |
| Encoding failures fail the externalization | `event_envelope_test.go: TestCodec_MarshalError` | unit |
| Envelope fields | `event_envelope_test.go: TestEnvelope_Fields/<row>` | table-driven, fake clock |
| Default IDs are unique and time ordered | `event_envelope_test.go: TestEnvelope_DefaultIDs` | race, fake clock |
| Trace context crosses the transport | `event_envelope_test.go: TestPropagator_RoundTrip` | integration (two roots, memory transport) |
| No propagator | `event_envelope_test.go: TestPropagator_None` | unit |
| Concurrent registration and publishing | `event_types_test.go: TestRegisterEventType_Concurrent` | race |
| Registry isolation after a clone | `event_types_test.go: TestRegisterEventType_Clone` | unit |

**`specs/event-externalization/spec.md`**

| Scenario | Test | Kind |
|---|---|---|
| Externalized, registered and unregistered types | `event_externalize_test.go: TestExternalize_WhichTypes/<row>` | table-driven |
| Externalization does not depend on local listeners | `event_externalize_test.go: TestExternalize_ListenerCount/<row>` | table-driven |
| Externalized type without a transport | `event_externalize_test.go: TestExternalize_NoTransport` | unit |
| Which strategy applies | `event_externalize_test.go: TestExternalize_Precedence/<row>` | table-driven, fake `Tx` |
| Auto resolution | `event_externalize_test.go: TestExternalize_Auto/<row>` | table-driven |
| Immediate send outcomes | `event_externalize_test.go: TestExternalize_Immediate/<row>` | table-driven |
| Send honors the publish context | `event_externalize_test.go: TestExternalize_ImmediateContext` | unit, `testWithTimeout` |
| After-commit outcomes | `event_externalize_test.go: TestExternalize_AfterCommit/<row>` | table-driven, fake `Tx` |
| The after-commit send is detached | `event_externalize_test.go: TestExternalize_AfterCommitDetached` | unit |
| Outbox outcomes | `event_externalize_test.go: TestExternalize_Outbox/<row>` | integration, fake `TxEventSender` |
| SendTx failure is returned | `event_externalize_test.go: TestExternalize_OutboxError` | unit |
| Strict and fallback | `event_externalize_test.go: TestExternalize_OutboxFallback/<row>` | table-driven |
| Outbox with a plain transport | `event_externalize_test.go: TestExternalize_OutboxNotSupported/<row>` | table-driven |
| Listener failure and externalize | `event_externalize_test.go: TestExternalize_AfterDispatch/<row>` | table-driven |
| Local publishing and child scopes | `event_externalize_test.go: TestExternalize_Scopes/<row>` | table-driven |
| Externalization across the lifecycle | `event_externalize_test.go: TestExternalize_Lifecycle/<row>` | table-driven |
| Concurrent publishers each externalize once | `event_externalize_test.go: TestExternalize_Concurrent` | race |
| Invalid values | `event_externalize_test.go: TestExternalizeOptions_Invalid/<row>` | table-driven |
| Enum names (event-externalization) | `event_externalize_test.go: TestExternalizeEnums_String/<row>` | table-driven |

**`specs/event-transport/spec.md`**

| Scenario | Test | Kind |
|---|---|---|
| Which transport is used | `event_transport_test.go: TestTransportResolution/<row>` | table-driven |
| A transport service is built once | `event_transport_test.go: TestTransportResolution_Once` | race |
| Local publishing does not reach remote listeners | `event_remote_test.go: TestRemote_NotLocal/<row>` | table-driven |
| Invalid remote listeners | `event_remote_test.go: TestRemote_RegistrationPanics/<row>` | table-driven |
| Valid remote listeners | `event_remote_test.go: TestRemote_RegistrationOK/<row>` | table-driven |
| Enum names (event-transport) | `event_remote_test.go: TestEventDelivery_String/<row>` | table-driven |
| Start-time validation | `event_remote_test.go: TestRemote_StartValidation/<row>` | table-driven |
| Subscriptions the transport receives | `event_remote_test.go: TestRemote_Subscriptions/<row>` | table-driven |
| Default listener IDs name service listener groups | `event_remote_test.go: TestRemote_DefaultGroup` | unit |
| Subscriptions open after Starters | `event_remote_lifecycle_test.go: TestRemote_SubscribeOrder` | unit |
| A failing Subscribe fails Start | `event_remote_lifecycle_test.go: TestRemote_SubscribeError` | unit |
| Shutdown stops subscriptions and waits for handlers | `event_remote_lifecycle_test.go: TestRemote_ShutdownWaits` | unit, goleak |
| The shutdown context bounds the wait | `event_remote_lifecycle_test.go: TestRemote_ShutdownDeadline` | unit, `testWithTimeout` |
| Stop errors are reported | `event_remote_lifecycle_test.go: TestRemote_StopError` | unit |
| Inbound outcomes | `event_inbound_test.go: TestInbound/<row>` | table-driven, integration |
| Lazy service listeners are built on first delivery | `event_inbound_test.go: TestInbound_LazyOnce` | unit |
| Decoding failures are permanent | `event_inbound_test.go: TestInbound_Decode/<row>` | table-driven |
| Marking and detecting permanent errors | `event_inbound_test.go: TestPermanentError/<row>` | table-driven |
| Concurrent inbound deliveries | `event_inbound_test.go: TestInbound_Concurrent` | race |
| Groups in memory | `event_memory_transport_test.go: TestMemoryTransport_Groups` | integration |
| Redelivery limits in memory | `event_memory_transport_test.go: TestMemoryTransport_Redelivery/<row>` | table-driven |
| A new group starts at the latest event | `event_memory_transport_test.go: TestMemoryTransport_NewGroupAtLatest` | unit |
| Invalid memory options | `event_memory_transport_test.go: TestMemoryTransport_InvalidOptions` | unit |
| The in-memory transport passes the suite | `event_memory_transport_test.go: TestMemoryTransport_Conformance` | conformance, race, goleak |
| The suite catches a broken transport | `eventtransporttest/run_test.go: TestRun_CatchesDefects/<row>` | table-driven, recording `testing.TB` |
| Transaction checks for a TxEventSender | `eventtransporttest/run_test.go: TestRun_WithTx` | conformance |
| Transports without DeliverAll | `eventtransporttest/run_test.go: TestRun_WithoutDeliverAll/<row>` | table-driven, recording `testing.TB` |
| Clone subscriptions | `event_remote_test.go: TestRemote_Clone` | unit |

**`specs/transactional-events/spec.md`**

| Scenario | Test | Kind |
|---|---|---|
| Phases with a bound transaction | `event_tx_test.go: TestTxPhase/<row>` | table-driven, fake `Tx` |
| Deferred listeners keep listener order | `event_tx_test.go: TestTxPhase_Order` | unit |
| Context of a deferred listener | `event_tx_test.go: TestTx_DetachedContext` | unit |
| Deferred failures are reported | `event_tx_test.go: TestTx_DeferredFailures/<row>` | table-driven |
| No transaction or an ended one | `event_tx_test.go: TestTx_Fallback/<row>` | table-driven |
| Which TxSource is used | `event_tx_test.go: TestTxSourceResolution/<row>` | table-driven |
| A conflicting TxSource fails Start | `event_tx_test.go: TestTxSourceResolution_ConflictAtStart` | unit |
| A TxSource service is built once | `event_tx_test.go: TestTxSourceResolution_Once` | unit |
| Option validation | `event_tx_test.go: TestTxOptions/<row>` | table-driven |
| Enum names (transactional-events) | `event_tx_test.go: TestTxEnums_String/<row>` | table-driven |

**`specs/event-externalization/spec.md` (publisher handle)**

| Scenario | Test | Kind |
|---|---|---|
| Publishing through the handle | `event_publisher_test.go: TestPublisherFor/<row>` | table-driven |
| The handle satisfies a consumer-defined port | `event_publisher_test.go: TestPublisherFor_SatisfiesPort` | unit (compile-time assignment) |

**Examples (godoc):** `ExamplePublisherFor` (a use case publishing through its own port), `ExampleRegisterEventType`, `ExampleListenerDelivery`, `ExamplePublishExternalize`, `ExampleNewMemoryEventTransport`, `ExampleInjectorOpts_eventExternalize`, `ExamplePermanentError` in `event_transport_example_test.go`.

**Hot paths and their tests:**
- Publish of a non-externalized type (must add nothing): `TestExternalize_WhichTypes` rows 1-2 plus a benchmark `BenchmarkPublish_NotExternalized` compared with `add-eventing`'s publish benchmark.
- Strategy selection and each send path: `TestExternalize_Precedence`, `TestExternalize_Auto`, `TestExternalize_Immediate`, `TestExternalize_AfterCommit`, `TestExternalize_Outbox`, `TestExternalize_OutboxFallback`.
- Inbound handler (decode, filter, resolve, call, error wrapping): `TestInbound`, `TestInbound_Decode`, `TestInbound_Concurrent`.
- Subscription open and stop: `TestRemote_SubscribeOrder`, `TestRemote_SubscribeError`, `TestRemote_ShutdownWaits`, `TestRemote_ShutdownDeadline`.
- Registry reads under concurrent writes: `TestRegisterEventType_Concurrent`.

## Migration Plan

Additive; nothing to migrate for `add-eventing` users. Projects that had planned on `add-durable-events`: `ListenerDurable()` becomes `RegisterEventType(..., EventExternalized())` on the publisher plus `ListenerDelivery(DeliverOnce)` on the listener and a crash-safe transport (`add-sql-event-transport`). Rollback means removing the options; nothing is persisted by core.

## Open Questions

- Dynamic remote listeners (registered after `Start`). Excluded by a registration rule now; allowing them later adds a requirement without changing these.
- Lease or visibility extension for long handlers is a per-transport concern; whether core should expose a hook for it can be decided when an adapter needs it.
