# Proposal

## Why

`add-eventing` delivers events only inside one process. A doger application that runs on several instances, or talks to other services, still hand-writes the send to a broker, the outbox in its transaction, and the consumer that turns messages back into events. The next follow-up, `add-durable-events`, solved part of that with a publication store tied to one storage model (lease and claim per listener). It could not deliver to other services or to every instance, and it would have re-implemented what messaging libraries such as watermill and msgin already provide.

This change makes cross-process delivery a core contract instead: a small, transport-neutral seam in package `doger`, an in-memory default, and opt-in transports in their own modules. The first-party SQL transport (`add-sql-event-transport`), the watermill adapter (`add-watermill-event-transport`) and the experimental msgin adapter (`add-msgin-event-transport`) follow as separate changes. The research behind the delivery model (Spring Modulith, MassTransit, NServiceBus, watermill) is summarized in design.md.

This change **supersedes `add-durable-events`**. Its transaction-bound listeners, codec, event type registry, headers and IDs move here; its publication store, claims, leases, redelivery pass and retry loop are dropped, because those are a transport's job. Removing the `add-durable-events` change directory is left to the maintainer.

## What Changes

**Event types and the wire format**
- `RegisterEventType[E](root, name, opts...)` gives an event type a stable wire name, independent of its Go import path. `EventVersion(n)` and `EventDestination(d)` are type options.
- An externalized event travels as an `Envelope` (ID, destination, type name, version, content type, payload, headers, publish time). The codec (`EventCodec`, JSON by default), ID generator (UUIDv7 by default) and header propagator (none by default) are replaceable.

**Externalization (sending)**
- Externalization is opt-in per type: `EventExternalized()` or `EventExternalize(strategy)`. Unregistered and non-externalized types never leave the process.
- Four strategies, chosen per publish (`PublishExternalize`), per type, or globally (`InjectorOpts.EventExternalize`):
  - `ExternalizeAuto` (default): outbox when a transaction is bound and the transport supports it, immediate otherwise;
  - `ExternalizeImmediate`: sent during `Publish`, error returned;
  - `ExternalizeAfterCommit`: sent after commit, errors to `HookEventError`, documented as at-most-once on a crash;
  - `ExternalizeOutbox`: written in the caller's transaction by the transport, delivered after commit.
- An outbox with no transaction bound fails `Publish` with `ErrNoTransaction` unless `InjectorOpts.EventOutboxFallback` allows immediate sending.
- Externalization runs after in-process dispatch. `PublishLocal()` never externalizes.

**Remote listeners (receiving)**
- `ListenerDelivery(DeliverOnce)`: one instance of the group handles each event. The group is the listener ID.
- `ListenerDelivery(DeliverAll)`: every instance handles each event. The group adds `InjectorOpts.InstanceID`. A generated instance ID marks the subscription ephemeral.
- The default, `DeliverLocal`, keeps `add-eventing` behavior. A remote listener is reached only through the transport, never by local dispatch.
- A new group starts at the latest event; an existing group resumes where it stopped. A transport that cannot honor this rejects the subscription.
- Subscriptions open at the end of `Start`, after every Starter, and close before the Closing hooks, waiting for handlers in flight.
- A handler error asks the transport to redeliver. `PermanentError(err)` asks it to stop and dead-letter. Decode failures are permanent.

**The seam**
- `EventTransport` (`Send`, `Subscribe`) plus the optional `TxEventSender` (`SendTx`, `TxSource`) for the outbox.
- The transport comes from `InjectorOpts.EventTransport`, else a root service `NameOf[EventTransport]()`, else none.
- `NewMemoryEventTransport()` is the in-memory transport for tests and single-process use. Package `eventtransporttest` is the conformance suite every transport runs; `WithTx` adds outbox checks and `WithoutDeliverAll` lets a transport that cannot broadcast prove it rejects `DeliverAll`.

**Application ports**
- `PublisherFor(i)` returns a `*Publisher` with `Publish(ctx, events ...any) error`, so an application (for example one built on hexagonal architecture) can satisfy its own publishing port without its domain importing doger.
- Binding a `database/sql` transaction into the context for `TxSource` is not core: the shared `events/sqltx` module (`add-sql-event-transport`) provides it, and every SQL-based transport uses it.

**Transaction-bound listeners** (moved from `add-durable-events`)
- `Tx`, `TxSource`, `ListenerTxPhase`, `ListenerTxFallback`, detached contexts and error routing, unchanged in substance. The transport's own `TxSource` joins the resolution order.

**Out of scope:** the SQL, watermill and msgin transports (their own changes), exactly-once delivery, ordering between events, consumer-side deduplication (left to transports and listeners), and request-reply.

## Capabilities

### New Capabilities
- `event-types`: wire names, versions, aliases, the codec, the envelope, IDs and header propagation.
- `event-externalization`: which events leave the process, the externalization strategies and their precedence, outbox fallback, and externalize errors.
- `event-transport`: the transport seam and its optional capabilities, transport resolution, remote listeners and groups, instance identity, the subscription lifecycle, inbound dispatch and permanent errors, the in-memory transport, the conformance suite, and clones.
- `transactional-events`: transaction phases and fallback for listeners, detached contexts, deferred error routing, and `TxSource` resolution.

### Modified Capabilities
- None in `openspec/specs/`. This change requires `add-eventing` to be archived first and adds listener and publish options without changing the `events` requirements; it also requires `add-extensions` (archived).

## Impact

- Code: new `event_types.go`, `event_envelope.go`, `event_externalize.go`, `event_transport.go`, `event_remote.go`, `event_tx.go`, `event_memory_transport.go`, package `eventtransporttest`. Listener and publish options in the `add-eventing` files. `root_start.go` (open subscriptions), the shutdown path (close them before the Closing hooks), `injector.go` (new options, copied by `copy()`), `root_scope.go` (lazily allocated transport state, clone).
- API: additive only. No exported API breaks. Containers that externalize nothing and have no remote listener behave exactly as with `add-eventing`.
- Dependencies: none new. UUIDv7 IDs come from `crypto/rand` and the root clock. `eventtransporttest` uses only the standard `testing` package.
- Other changes: `add-durable-events` is superseded. `add-sql-event-transport`, `add-watermill-event-transport` and `add-msgin-event-transport` depend on this change.
- Docs: an "Event transports" guide (strategies, groups, idempotency, writing a transport, and a hexagonal-architecture section: domain versus integration events, the unit of work binding the transaction into `ctx`, listeners as driving adapters, publishing inside the unit of work), `Example*` tests for defaults and overrides, an upgrading note (no breaks).

## Defaults & Control

| Behavior | Zero-config default | Consumer control |
|---|---|---|
| Which events leave the process | None | `RegisterEventType(..., EventExternalized())` per type |
| Wire name | Required at registration; destination = the name | `EventDestination(d)`, `InjectorOpts.EventDestinationMapper` |
| Transport | None: externalized events stay in-process, logged once at Warn level through `InjectorOpts.Logger` (standard `log` when unset) | `InjectorOpts.EventTransport`, a `NameOf[EventTransport]()` service, `NewMemoryEventTransport()` |
| Externalization strategy | `ExternalizeAuto` | `PublishExternalize(s)` > `EventExternalize(s)` > `InjectorOpts.EventExternalize` |
| Outbox without a transaction | `Publish` fails with `ErrNoTransaction` | `InjectorOpts.EventOutboxFallback = OutboxFallbackImmediate` |
| After-commit send failure | Reported to `HookEventError`, not retried by core | Choose `ExternalizeOutbox` for crash safety |
| Listener delivery | `DeliverLocal` (in-process) | `ListenerDelivery(DeliverOnce)` or `ListenerDelivery(DeliverAll)` |
| Group name | `InjectorOpts.EventGroupPrefix` + listener ID (+ `/` + instance ID for `DeliverAll`) | `ListenerID(id)`, `InjectorOpts.EventGroupPrefix` |
| Instance ID | Generated per root, so `DeliverAll` subscriptions are ephemeral | `InjectorOpts.InstanceID` (stable, e.g. a pod ordinal) |
| Where a new group starts | The latest event | None: replaying a whole history on first start is never a safe default; a transport may offer its own replay tool |
| Handler error | Redelivered by the transport's policy | `PermanentError(err)` to stop redelivery |
| Codec | `encoding/json`, `application/json` | `InjectorOpts.EventCodec` |
| Event IDs | UUIDv7 | `InjectorOpts.EventIDGenerator` |
| Headers | None propagated | `InjectorOpts.EventPropagator` |
| Transaction phase of a listener | Immediate | `ListenerTxPhase(...)` |
| No transaction bound for a deferred listener | Runs immediately | `ListenerTxFallback(TxFallbackSkip)` |
| Transaction lookup | None, or the transport's `TxSource` | `InjectorOpts.TxSource`, a `NameOf[TxSource]()` service |
