---
title: Events
description: Publish typed in-process events and react to them with lazily built listener services or functions, with ordering, scope reach, async delivery and lifecycle events.
sidebar_position: 6
---

# Events

`doger` ships an in-process event system: publish any Go value with `doger.Publish`, and react to it with listener services or functions. There is no event bus to build, no string topic and no `any` payload. Everything below works with zero configuration; every default can be overridden.

## Publishing {#publishing}

```go
err := doger.Publish(ctx, injector, OrderPlaced{ID: "42"})
```

An event reaches every listener declared for a type its dynamic type is assignable to: a listener of `OrderPlaced`, of an interface `OrderPlaced` implements, or of `any`. A listener of an interface implemented with pointer receivers receives only pointer events.

`Publish` returns an error matching `doger.ErrInvalidEvent` for a nil event (also a nil pointer, map, slice, channel or function, even inside an interface), and `doger.ErrNilInjector` for a nil injector. A nil context reaches listeners as `context.Background()`.

## Listeners {#listeners}

A **listener service** opts in at registration. It is not built before an event reaches it, so lazy services stay lazy:

```go
type Mailer struct{}

func (m *Mailer) OnEvent(ctx context.Context, e OrderPlaced) error { /* ... */ return nil }

doger.Provide(injector, NewMailer, doger.AsListener[OrderPlaced]())
```

One service can listen to several event types with method expressions, the Go form of an annotated listener method. It is built once:

```go
doger.Provide(injector, NewBilling,
    doger.ListenMethod((*Billing).OnPlaced),
    doger.ListenMethod((*Billing).OnCancelled),
)
```

A **function listener** is registered on a scope and returns a stop function (true on its first call, false afterwards):

```go
stop := doger.Listen(injector, func(ctx context.Context, e OrderPlaced) error { return nil })
defer stop()
```

Registration panics for a concrete type that does not implement `Listener[E]` (or is not the receiver type of `ListenMethod`); a service registered under an interface type is checked on its instance at its first delivery, which fails with an error matching `doger.ErrInvalidListener`.

### Options {#options}

| Option                       | Default     | Effect                                                                                                  |
| ---------------------------- | ----------- | ------------------------------------------------------------------------------------------------------- |
| `doger.ListenerOrder(n)`     | 0           | Lower runs first                                                                                        |
| `doger.ListenerFilter(pred)` | none        | Skips the listener, without building it, when `pred(event)` is false; the last one wins, nil means none |
| `doger.ListenerAsync()`      | synchronous | Delivers through the event executor                                                                     |
| `doger.ListenerID(id)`       | derived     | Names the listener in errors and logs                                                                   |

Default IDs are `<scope path>/<service>#<event type>` (with `/m<n>` for the n-th `ListenMethod`) and `<scope path>/func#<n>` for function listeners.

## Order {#order}

Synchronous listeners run by ascending `ListenerOrder`, then by reach (the publishing scope, each ancestor up to the root, then installed extension scopes in installation order), then in registration order. Order is decided without building anything: `WithOrder` and `Ordered` keep their collection-only meaning.

## Reach {#reach}

An event published through a scope reaches that scope, its ancestors and the private scopes of installed extensions. It never reaches sibling or descendant scopes, nor extensions still installing. `doger.PublishLocal()` keeps it in the publishing scope.

## Lifetime {#lifetime}

A service listener belongs to its registration: an override, an `AsDefault()` registration replaced or yielding, or the shutdown of its scope ends it. Shutting a scope down also ends its function listeners; a recreated scope starts empty. Publishing through a shut-down scope, or through a root whose shutdown completed, returns an error matching `doger.ErrPublishAfterShutdown`, and `Listen` on such a scope panics with an error matching it, since no event could reach the listener.

## Errors {#errors}

By default, the first failing listener stops dispatch and `Publish` returns a `*doger.ListenerError` naming the listener and the event type, and unwrapping to the cause. Panics are recovered into a `*ListenerError` too. To run every listener and get their joined errors:

```go
injector := doger.NewWithOpts(&doger.InjectorOpts{EventErrorMode: doger.EventContinue})
```

A done context stops dispatch with the context's error. A listener may publish: the nested dispatch runs inline. Publishing from a provider in a way that would build the service being built returns an error matching `doger.ErrCircularDependency` instead of deadlocking, and records no dependency edge.

## Async delivery {#async}

`ListenerAsync()` delivers on the event executor; `Publish` returns once the delivery is submitted. The listener's context keeps the publisher context's values but not its cancellation. A listener service is resolved, and built if needed, when the delivery is submitted.

The default executor runs at most `InjectorOpts.EventAsyncParallelism` deliveries at once (default `runtime.GOMAXPROCS(0) * 4`, read when the container is created). A publisher finding it full waits for a slot or for its context's end. A publish from an async listener never waits for a slot: it runs inline when none is free, so nested async publishing cannot deadlock. Replace the executor with `InjectorOpts.EventExecutor`; `doger.NewBoundedEventExecutor(n)` is the default implementation, for reuse and tests. An executor set in the options belongs to the container it was given to: clones share it, but a clone's shutdown never shuts it down.

### Errors with no caller {#hook-event-error}

Async and lifecycle listener failures have no caller to return to. They go to `InjectorOpts.HookEventError` as `*ListenerError`. When it is unset, they are logged with `InjectorOpts.Logger` at Error level, or, when `Logger` is unset too, with the standard `log` package: the one place `doger` logs without `Logger`, so that failures stay visible with zero configuration. Set either field to redirect or silence them.

## Lifecycle events {#lifecycle}

The root publishes `doger.ContainerStarting`, `ContainerStarted`, `ContainerReady`, `ContainerFailed{Err}`, `ContainerClosing` and `ContainerClosed{Report}` (each with `Root`) right after the [container hooks](./container-hooks.md) of the same point. Hooks are unchanged. Lifecycle listeners only observe: every one runs, their failures go to `HookEventError`, and `Start` and `Shutdown` results never change. `ContainerClosed` reaches function listeners only; as async deliveries are no longer accepted then, its async listeners run inline before `Shutdown` returns.

## Shutdown {#shutdown}

Root shutdown, after `ContainerClosing`, stops accepting async deliveries (a publish then fails with `ErrPublishAfterShutdown` for each async listener it reaches, and still runs its synchronous listeners), wakes publishers waiting for a slot, and waits for the deliveries in flight before shutting services down. When the shutdown context ends first, the deliveries' contexts are cancelled with cause `ErrPublishAfterShutdown`, `Shutdown` returns without waiting for them further, and the report holds the context's error. During service shutdown, synchronous publishes still reach function listeners and built service listeners, never building one. A child scope shutdown waits for the async deliveries to its own and its descendants' listeners first, and refuses new ones from the moment it begins. An async listener may shut its own scope or the container down by passing its context to `ShutdownWithContext`: that shutdown does not wait for the calling delivery. Called without that context, it would wait for itself.

## Other processes {#transports}

To deliver events to other processes, or to every instance of a service, see [Event transports](./event-transports.md).
