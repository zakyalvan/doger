# Proposal

## Why

Every doger application that reacts to "something happened" builds its own event bus. `examples/event-driven` shows the usual result: string event types, `any` payloads, no ordering, no async, no lifecycle integration. This change makes in-process eventing a **first-class part of package `doger`**, on the level of `Provide`, `Invoke` and `Start`, covering what Spring Framework offers through `ApplicationEventPublisher`, `@EventListener`, `@Order`, `@Async` and its context lifecycle events. Eventing is not an `Extension`: its API and semantics belong to core and work with zero configuration.

Durable, transaction-bound delivery (Spring Modulith's event publication registry, `@TransactionalEventListener`) is the planned follow-up change `add-durable-events`. It builds on the listener model defined here and needs a lease-based store SPI of its own, so it is split out to let in-process eventing ship first.

## What Changes

**Publishing**
- Any Go value is an event; no marker interface and no string topic. `doger.Publish(ctx, i, event, opts...) error` delivers to every reachable listener whose declared type the event's dynamic type is assignable to, so a listener for an interface receives every event that implements it.
- `PublishLocal()` restricts delivery to the publishing scope.

**Listening**
- A service opts in at registration with `AsListener[E]()` (it implements `Listener[E]`, `OnEvent(ctx, E) error`) or `ListenMethod((*Mailer).OnPlaced)`, the Go form of `@EventListener` on a method, so one service can listen to many event types. Nothing is built before an event reaches it, so lazy services stay lazy.
- The opt-in belongs to the registration, so it follows overrides, `AsDefault()` replacement and scope shutdown: a listener whose registration is gone is no longer reached.
- A function listener is registered with `doger.Listen(i, fn, opts...)`, which returns a stop function; it also ends when its scope shuts down.
- Listener options: `ListenerOrder`, `ListenerFilter`, `ListenerAsync` and `ListenerID`.

**Reach, order and errors**
- An event reaches the publishing scope, its ancestors and installed extension private scopes (Spring's child-to-parent rule, plus extensions), never siblings or descendants.
- Order: `ListenerOrder`, then scope depth, then registration order, decided without building listeners.
- The first listener error stops dispatch and `Publish` returns it as a `*ListenerError` (default); `EventContinue` runs every listener and joins the errors. Panics are recovered. A listener may publish; the nested dispatch runs inline. A publish that would build the service already being built returns an error matching `ErrCircularDependency` instead of deadlocking.
- Errors with no caller (async listeners, lifecycle listeners) go to `HookEventError`.

**Async delivery**
- `ListenerAsync()` delivers on an executor. The default is bounded (`EventAsyncParallelism`), applies backpressure to publishers, never deadlocks on nested async publishes, and is drained at root and scope shutdown. `InjectorOpts.EventExecutor` replaces it; `NewBoundedEventExecutor(n)` exposes the default for reuse and tests.

**Lifecycle**
- The root publishes `ContainerStarting`, `ContainerStarted`, `ContainerReady`, `ContainerFailed`, `ContainerClosing` and `ContainerClosed` right after the existing container hooks of the same point. The hooks are unchanged; lifecycle listeners observe only and never change `Start` or `Shutdown` results.

**Out of scope:** durable and transaction-bound delivery (`add-durable-events`), broker bridges, listener return values republished as events, and string-typed event names.

## Capabilities

### New Capabilities
- `events`: publishing and dispatch by assignability, listener declaration (`AsListener`, `ListenMethod`, `Listen`), listener lifetime, ordering, filters, scope reach, error modes and the error hook, panic recovery, nested publishing, async delivery and executors, container lifecycle events, shutdown draining, concurrency and clones.

### Modified Capabilities
- None. Lifecycle events are published in addition to the container hooks, so `container-lifecycle-hooks` requirements do not change.

## Impact

- Code: new `event.go`, `event_listener.go`, `event_dispatch.go`, `event_executor.go`; `options.go` (listener bindings in `serviceOptions`, as kinds); `scope.go` (per-scope function listeners and plan cache, dropped at scope shutdown); `root_scope.go` (lazily allocated event state, clone); `root_start.go` and the shutdown path (lifecycle events, drain); `injector.go` (`EventErrorMode`, `EventExecutor`, `EventAsyncParallelism`, `HookEventError`, copied by `copy()`).
- API: additive. No exported API breaks. Existing containers see no behavior change until a listener is registered; publishing a lifecycle event nobody listens to costs one check.
- Dependencies: no new module dependencies. Builds on `add-extensions` (archived) for extension-scope reach.
- Examples/docs: rewrite `examples/event-driven` on the new API; an "Events" guide; `Example*` tests for defaults and overrides; an upgrading note (no breaks).

## Defaults & Control

| Behavior | Zero-config default | Consumer control |
|---|---|---|
| Dispatch mode | Synchronous on the publisher's goroutine | `ListenerAsync()` per listener |
| Listener discovery | Opt-in at registration, no builds before an event arrives | `Listen` for function listeners |
| Listener lifetime | Follows its registration or its scope | Stop function returned by `Listen` |
| Order | `ListenerOrder` 0, then scope depth, then registration order | `ListenerOrder(n)` |
| Filter | None | `ListenerFilter(pred)` |
| Scope reach | Publishing scope, ancestors, extension scopes | `PublishLocal()` |
| Listener error | Fail fast; `Publish` returns `*ListenerError` | `InjectorOpts.EventErrorMode = EventContinue` |
| Errors with no caller | Logged through `Logf`, or the standard `log` package when `Logf` is unset | `InjectorOpts.HookEventError` |
| Async concurrency | `runtime.GOMAXPROCS(0) * 4` deliveries, blocking backpressure | `InjectorOpts.EventAsyncParallelism`, or `InjectorOpts.EventExecutor` |
| Async at shutdown | Drained, bounded by the shutdown context | The executor's `Shutdown` |
| Lifecycle events | Published after the hooks | Ignore them; hooks stay available |
| Listener IDs | Derived from scope path, service and event type | `ListenerID(id)` |
