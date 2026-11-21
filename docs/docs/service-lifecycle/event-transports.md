---
title: Event transports
description: Send events to other processes and receive them with remote listeners, through a transport-neutral seam, with outbox, after-commit and immediate strategies, consumer groups and a conformance suite.
sidebar_position: 7
---

# Event transports

[Events](./events.md) are delivered inside one process by default. An **event transport** carries them to other processes, and to the other instances of the same service: `doger` defines a small seam, `EventTransport`, ships an in-memory implementation, and leaves brokers and databases to transport modules. Nothing changes for a container that externalizes no event type and declares no remote listener: it resolves no transport and does no new work.

## Wire names {#event-types}

Only types registered with a stable wire name ever leave the process. The name is independent of the Go import path, so renaming a package does not rename a contract:

```go
err := doger.RegisterEventType[OrderPlaced](root, "orders.placed",
    doger.EventExternalized(),      // send it through the transport
    doger.EventVersion(2),          // default 1
    doger.EventDestination("shop"), // default: the name
    doger.EventAlias("order.created"), // older names that still decode to it
)
```

A name is bound to one concrete type: a conflicting registration fails with `ErrEventTypeConflict`, and an interface type, an empty name or an invalid option with `ErrInvalidEventType`. Registration is safe at any time, concurrently with publishing. `InjectorOpts.EventDestinationMapper` maps every destination, on send and subscribe alike, for example to prefix it per environment.

An externalized event travels as an `Envelope`: ID (UUIDv7 by default, `InjectorOpts.EventIDGenerator`), destination, type name, version, content type, payload (`encoding/json` by default, `InjectorOpts.EventCodec`), headers (`InjectorOpts.EventPropagator`, none by default) and publish time from the container clock.

## Choosing a transport {#transport}

The container uses `InjectorOpts.EventTransport`, else a root service named `doger.NameOf[doger.EventTransport]()` (built once on first use; an application registration wins over an `AsDefault()` one an extension exported), else none. With none, externalized events stay in-process and a warning is logged once.

```go
transport := doger.NewMemoryEventTransport() // tests and single-process applications
root := doger.NewWithOpts(&doger.InjectorOpts{EventTransport: transport})
```

The in-memory transport delivers one event per group, round robin within a group, starts a new group at the latest event, and retries a failing handler up to `MemoryMaxAttempts(n)` times (default 3) before dropping and logging the event. It keeps nothing across restarts.

## SQL transport {#sql-transport}

The `events/sql` module (package `eventsql`) is the supported transport for production: PostgreSQL 13+ or MySQL 8.0+ carries the events, through the `*sql.DB` your application already opens, with a transactional outbox. It depends on `doger` and `events/sqltx` only; you bring the driver.

1. Create its three tables with your migration tool, from the reference DDL that `postgres.DDL("doger_")` or `mysql.DDL("doger_")` returns (review it like any migration). `transport.EnsureSchema(ctx)` creates them in development and tests; `CheckSchema(ctx)` fails with `ErrSchemaMissing` when they are absent.
2. Make it the container's transport:

   ```go
   transport, err := eventsql.New(db, postgres.Dialect()) // or mysql.Dialect()
   if err != nil {
       return err
   }
   root := doger.NewWithOpts(&doger.InjectorOpts{EventTransport: transport})
   ```

3. Publish inside `sqltx.InTx(ctx, db, fn)`: with `ExternalizeAuto`, the event is written in that transaction and delivered once it commits.

Each send stores the event once and a delivery row per group registered for its destination, so a new group starts at the latest event and a durable group keeps the events sent while no member runs. Members claim deliveries with a lease and `FOR UPDATE SKIP LOCKED`: one member of a `DeliverOnce` group handles each event, every `DeliverAll` group gets a copy, and a crashed member's claims return when the lease expires (`Lease`, 30s). A failing handler is retried with exponential backoff (1s to 5m) up to `MaxAttempts` (10), then dead-lettered, at once for a `PermanentError`; `ListDeadLetters`, `Resubmit` and `DropGroup` are the operator's tools. Ephemeral groups are removed when their subscription stops, or `EphemeralTTL` (5m) after a crash. A group registered after an outbox transaction wrote its event does not receive that event; this holds under READ COMMITTED, while a PostgreSQL outbox transaction at REPEATABLE READ or SERIALIZABLE can also miss a group registered after its snapshot. Every default (`PollInterval`, `BatchSize`, `Workers`, `TablePrefix`, `KeepDelivered`, `MaxPayloadBytes`, `Backoff`, `Logger`) is an option of `New`; see the module README for the full table and the operations runbook.

## Externalization strategies {#strategies}

`Publish` first runs the in-process listeners, then hands the event to the transport. When is decided per publish by the first of `PublishExternalize(s)`, the type's `EventExternalize(s)` and `InjectorOpts.EventExternalize`:

| Strategy                    | When it is sent                                                                                               | Errors                                       |
| --------------------------- | ------------------------------------------------------------------------------------------------------------- | -------------------------------------------- |
| `ExternalizeAuto` (default) | Outbox when a transaction is bound and the transport implements `TxEventSender`; immediate otherwise          | as the chosen strategy                       |
| `ExternalizeImmediate`      | During `Publish`, with its context                                                                            | returned by `Publish` as `*ExternalizeError` |
| `ExternalizeAfterCommit`    | Once the transaction commits, with the detached context; nothing on rollback; immediate without a transaction | to `HookEventError`                          |
| `ExternalizeOutbox`         | Written in the caller's transaction with `SendTx`; delivered after commit                                     | returned by `Publish`                        |

`ExternalizeAfterCommit` keeps no record: a crash between the commit and the send loses the event (at most once on a crash). Use `ExternalizeOutbox` with a transport that has an outbox when every committed event must leave. `ExternalizeAuto` with a transaction but a transport without an outbox sends during `Publish`, so an event can leave for a transaction that later rolls back.

An outbox with no transaction fails with `ErrNoTransaction` unless `InjectorOpts.EventOutboxFallback = OutboxFallbackImmediate`. `Start` fails with `ErrOutboxNotSupported` when the outbox is configured and the transport cannot do it. Under `EventFailFast` a failing listener stops the externalization; under `EventContinue` both errors are joined. `PublishLocal()` never externalizes.

## Transactions {#transactions}

Core knows no transaction type. A `TxSource` finds the `Tx` of a context, and `Detach` returns a context without it. It comes from `InjectorOpts.TxSource`, a root service named `doger.NameOf[doger.TxSource]()`, or the transport's own `TxSource()`; a different one configured next to a `TxEventSender` fails `Start` with `ErrTxSourceConflict`. The shared `events/sqltx` module binds a `database/sql` transaction: `sqltx.InTx(ctx, db, fn)` (or `BeginTx`, or `BindTx` for a transaction another library began), with `sqltx.Source()` as the `TxSource`; the [SQL transport](#sql-transport) uses it for its outbox.

Listeners can wait for the transaction too: `ListenerTxPhase(TxPhaseAfterCommit)`, `TxPhaseAfterRollback` or `TxPhaseAfterCompletion` runs them from the transaction's callbacks with the detached context, their errors going to `HookEventError`. Without a transaction they run at once, or are skipped with `ListenerTxFallback(TxFallbackSkip)`.

## Remote listeners {#remote-listeners}

A listener with `ListenerDelivery` is reached only through the transport, never by in-process dispatch:

```go
doger.Listen(root, onPlaced, doger.ListenerID("invoice"), doger.ListenerDelivery(doger.DeliverOnce))
doger.Provide(root, NewCache, doger.ListenMethod((*Cache).OnPlaced,
    doger.ListenerID("cache"), doger.ListenerDelivery(doger.DeliverAll)))
```

- `DeliverOnce`: one instance of the group handles each event. The group is `InjectorOpts.EventGroupPrefix` + the listener ID.
- `DeliverAll`: every instance handles each event. The group also ends with `/` + `InjectorOpts.InstanceID`. Without an instance ID, one is generated per container and the subscription is marked `Ephemeral`; set a stable one (a pod ordinal) to resume after a restart.

A new group starts at the latest event: replaying a topic's history on first start is never a safe default. Give every remote listener an explicit `ListenerID`, because a renamed group is a new group.

Remote listeners are registered in the root or an extension's private scope, before `Start`, never async and never with a transaction phase; their event type must be registered (not necessarily externalized here). `Start` subscribes them after every Starter, before the Started hooks, and fails with `ErrNoEventTransport`, `ErrEventTypeNotFound` or `ErrListenerGroupConflict` when it cannot. Shutdown stops the subscriptions before the Closing hooks, waiting for handlers in flight up to the shutdown context.

## Failures and idempotency {#idempotency}

A handler error asks the transport to redeliver. Wrap an error with `PermanentError(err)` when retrying cannot help: the transport then dead-letters or drops the event. Envelopes that cannot be decoded fail permanently. Delivery is at least once, so make listeners idempotent with the envelope ID:

```go
func (c *Cache) OnPlaced(ctx context.Context, e OrderPlaced) error {
    env, _ := doger.EnvelopeFromContext(ctx)
    if c.seen(env.ID) {
        return nil
    }
    // ...
}
```

## Writing a transport {#writing-a-transport}

Implement `Send` and `Subscribe` (and `SendTx`, `TxSource` for an outbox), then run the conformance suite in your tests. It imports only the standard library and `doger`:

```go
func TestConformance(t *testing.T) {
    eventtransporttest.Run(t, func(t *testing.T) doger.EventTransport {
        return mytransport.New(t)
    }, eventtransporttest.WithTx(harness))
}
```

`WithTx` adds the outbox checks; `WithoutDeliverAll` is for a transport that cannot broadcast and must reject `DeliverAll` with `ErrSubscriptionNotSupported`.

## Hexagonal applications {#hexagonal}

Keep `doger` out of the domain:

- **Domain events** are plain structs raised by aggregates; **integration events** are the ones registered with a wire name and externalized. They can be the same type, or an application listener can translate one into the other.
- The **application layer** declares its own port, `type EventPublisher interface { Publish(ctx context.Context, events ...any) error }`, and the use case publishes through it. `doger.PublisherFor(root)` satisfies it: it publishes in order and stops at the first error.
- The **unit of work** binds the transaction into `ctx` (for `database/sql`, `sqltx.InTx`), so `ExternalizeAuto` picks the outbox without the use case knowing. Publish inside the unit of work: an event published after it returned is outside the transaction.
- **Remote listeners are driving adapters**: they translate an incoming event into a call of a use case, like an HTTP handler does for a request.
