# Proposal

## Why

Every doger application that reacts to "something happened" builds its own event bus. `examples/event-driven` shows the usual result: string event types, `any` payloads, no ordering, no async, no lifecycle, and no delivery guarantee. Consumers who need an event to survive a crash also hand-write an outbox, a retry loop and redelivery on start. This change makes eventing a **first-class part of package `doger`**, on the level of `Provide`, `Invoke` and `Start`. It covers what Spring Framework offers (`ApplicationEventPublisher`, `@EventListener`, `@Order`, `@Async`, lifecycle events, `@TransactionalEventListener`) and what Spring Modulith adds on top (an event publication registry with at-least-once delivery). Eventing is not an `Extension`: its API, semantics and guarantees belong to core and work with zero configuration. Only storage drivers and broker clients, which core cannot depend on, plug in from outside, through a store SPI.

## What Changes

**Publishing and listening (core, in-process)**
- Any Go type is an event; no marker interface. `doger.Publish(ctx, i, e) error` dispatches to every listener whose declared type the event's dynamic type is assignable to, so a listener for an interface receives every event that implements it.
- A listener is a service that implements `Listener[E]` (`OnEvent(ctx, E) error`) and opts in at registration with `AsListener[E]()`. This works without building services up front, so lazy services stay lazy: a listener is built on the first event that reaches it. A service that reacts to several event types registers methods with `ListenMethod((*Mailer).OnOrderPlaced)`, the Go form of `@EventListener` on a method. A function listener is registered with `doger.Listen(i, func(ctx, E) error, opts...)`.
- Listener options: order (`ListenerOrder`, default 0, then scope depth and registration order; `Ordered` is not used, because reading it would build the listener), a filter predicate, and async delivery on a replaceable executor.
- **Which listeners an event reaches:** the publishing scope and its ancestors (Spring's child-to-parent rule), plus the private scopes of installed extensions, so extensions can react to application events.
- **Errors:** by default the first listener error stops dispatch and `Publish` returns it, wrapped and naming the listener. A replaceable error policy can run every listener and join the errors. Listener panics are recovered as errors.
- **Lifecycle events:** the root publishes `ContainerStarting`, `ContainerStarted`, `ContainerReady`, `ContainerFailed`, `ContainerClosing` and `ContainerClosed` as events at the same points where the existing container hooks fire. The hooks keep working unchanged.
- **Shutdown:** in-flight async deliveries are drained before services shut down, bounded by the shutdown context, so no goroutine leaks.

**Transaction-bound and durable delivery (core contract)**
- A `PublicationStore` SPI plus a `Codec` (default `encoding/json`) and an event-type registry, so a stored event can be decoded after a restart.
- Durable listeners (`AsListener[E](Durable())`): `Publish` records one publication per durable listener *before* dispatch, and marks it complete when the listener returns nil. A durable listener's delivery error is recorded with `Fail` and sent to the async error handler; for durable listeners `Publish` returns only encoding and `Append` errors. Incomplete publications are redelivered at `Start`, and optionally retried periodically with backoff. Delivery is **at-least-once**. Each publication carries an ID so listeners can deduplicate.
- **Transaction binding** lets the publication be written in the consumer's business transaction. That removes the dual-write window. Core defines `Tx` and `TxBinder`; each store module decides how a transaction is found (for example `dogerpg.WithTx(ctx, tx)`), so core never depends on `database/sql`. Listeners can run immediately, after commit, after rollback or after completion; durable listeners default to after commit.
- Retention: completed publications are deleted after a configurable period.
- **Default store:** in memory. It has the same semantics, is not crash-safe, needs zero configuration and suits tests. Crash-safe stores (Postgres, MySQL, SQLite) and broker externalization (Kafka, NATS) ship as separate `dogerevents-*` modules that implement the SPI, installed with `doger.Extend`.

**Out of scope:** the store and broker modules themselves, listener return values republished as new events, string-typed event names, and cross-process exactly-once delivery.

## Capabilities

### New Capabilities
- `events`: event publishing and dispatch by type, `Listener[E]`, `AsListener`, `Listen`, ordering, filters, async delivery and the executor, error policy, panic recovery, how events reach scopes, container lifecycle events, and draining at shutdown.
- `event-publication-registry`: durable listeners, `PublicationStore` SPI, codec and event-type registry, transaction binding, at-least-once completion, redelivery at `Start`, retry, retention, and the default in-memory store.

### Modified Capabilities
- None. Lifecycle events are published *in addition to* the existing container hooks, so `container-lifecycle-hooks` requirements do not change. Draining at shutdown applies only to async deliveries and is specified in `events`.

## Impact

- Code: new `event.go`, `event_listener.go`, `event_dispatch.go`, `event_publication.go`; `root_scope.go` (listener registry, clone); `root_start.go` / shutdown path (lifecycle events, redelivery at `Start`, drain); `options.go` (`AsListener` as a `ProvideOption`); `injector.go` (`InjectorOpts` fields for executor, error policy, store, codec).
- API: additive only. No exported API breaks.
- Dependencies: no new module dependencies in core (stdlib only). Builds on `component-kinds`-style opt-in metadata. Store and broker modules depend on `add-extensions` for installation, but core eventing does not.
- Examples/docs: rewrite `examples/event-driven` on the new API; an "Events" guide; `Example*` tests for defaults and overrides; an upgrading note (no breaks).

## Defaults & Control

| Behavior | Zero-config default | Consumer control |
|---|---|---|
| Dispatch mode | Synchronous on the publisher's goroutine | `Async()` per listener; replace the executor via `InjectorOpts` |
| Listener discovery | Opt-in at registration (`AsListener[E]()`), no eager builds | `Listen` for function listeners |
| Listener order | `ListenerOrder` 0, then scope depth, then registration order | `ListenerOrder(n)` |
| Listener filter | None | `ListenerFilter(func(E) bool)` |
| Listener error | Stop dispatch; `Publish` returns the wrapped error | Error policy option: run all and join, or log and continue |
| Listener panic | Recovered and returned as an error | Panic hook |
| Scope reach | Publishing scope, its ancestors, extension private scopes | Per-publish option to restrict to the local scope |
| Lifecycle events | Published alongside the hooks | Ignore them; hooks stay available |
| Async at shutdown | Drained, bounded by the shutdown context | Executor's own shutdown behavior |
| Durability | Off per listener; `Durable()` opts in | — (opt-in is the control) |
| Transaction phase | Immediate; after commit for durable listeners; runs immediately when no transaction is bound | `Phase(...)`, `TxFallback(TxSkip)` |
| Transaction binder | Never bound | A store module's `TxBinder`, or `InjectorOpts` / a named service |
| Publication store | In memory (not crash-safe) | `InjectorOpts` store, or a `dogerevents-*` module via `Extend` |
| Codec | `encoding/json` | `InjectorOpts` codec |
| Redelivery | At `Start`, then no periodic retry | Retry interval and backoff options |
| Retention | Completed publications deleted immediately | Retention period option |
| Redelivery batch size | 100 publications per `Claim` | `InjectorOpts.PublicationBatchSize` |
