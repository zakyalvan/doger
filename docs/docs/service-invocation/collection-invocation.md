---
title: Collection invocation
description: Invoke every service implementing an interface at once, as a slice or a name-keyed map
sidebar_position: 3
---

# Collection invocation

`doger.InvokeAll[T]` invokes every visible service whose registered type is assignable to `T` and returns them as a slice. It is the building block for plugin-style designs: event handlers, health reporters, HTTP middlewares, migrations.

```go
type EventHandler interface {
    Handle(event Event)
}

doger.Provide(injector, NewConsoleHandler) // *ConsoleHandler implements EventHandler
doger.Provide(injector, NewAuditHandler)   // *AuditHandler implements EventHandler
doger.Provide(injector, NewDatabase)       // *sql.DB does not

handlers, err := doger.InvokeAll[EventHandler](injector)
// handlers: the *ConsoleHandler and the *AuditHandler instances
```

## Membership {#membership}

A service is a member when the type it was **registered** with is assignable to `T`. Membership is decided without building anything:

- services of other types are never built (`*sql.DB` above is not invoked);
- lazy members are built as needed, eager values are returned as is, and transient members give a new instance on every call;
- a provider returning a typed nil (for example a nil `*NoopHandler`) gives a nil element wrapped in `T`.

A nil injector means `doger.DefaultRootScope`.

:::warning

`InvokeAll[any]` matches every service, so it builds every service of the visible scopes.

:::

Invocation hooks (`HookBeforeInvocation`, `HookAfterInvocation`) fire once per member, with the member's name, as if `Invoke` had been called once per member.

## Visibility {#visibility}

A collection invoked from a scope includes the services of that scope and of its ancestors, never those of sibling or child scopes.

When the same name exists in several visible scopes, only the **nearest** service is considered, even when it is not assignable to `T`:

```go
root := doger.New()
doger.ProvideNamed(root, "x", NewAHandler)   // *AHandler implements EventHandler
child := root.Scope("child")
doger.ProvideNamed(child, "x", NewDatabase)  // *sql.DB does not

handlers, _ := doger.InvokeAll[EventHandler](child)
// handlers is empty: the child's "x" hides the root's "x"
```

This matches what `doger.InvokeNamed(child, "x")` resolves.

Inside a provider, the invoking scope is the scope where that provider's service is **registered**, not the scope the consumer invoked it from, as for `Invoke`.

## Aliases {#aliases}

Aliases registered with `doger.As` or `doger.Bind` are never members. Their target represents itself, so an alias never duplicates a member:

```go
doger.Provide(injector, NewAuditHandler)
doger.As[*AuditHandler, EventHandler](injector)

handlers, _ := doger.InvokeAll[EventHandler](injector)
// one element: the *AuditHandler instance
```

## Composites {#composites}

When a collection is invoked from inside a provider, the service that provider builds is **not** a member, even if its registered type is assignable to `T`. A composite can therefore collect every other implementation of its own interface:

```go
doger.Provide(injector, func(i doger.Injector) (*CompositeHandler, error) {
    handlers, err := doger.InvokeAll[EventHandler](i) // every other EventHandler
    return &CompositeHandler{handlers: handlers}, err
})
```

Only the service whose provider is running is excluded. A deeper cycle still fails with `doger.ErrCircularDependency`: if `x`'s provider invokes `bus`, and `bus`'s provider collects every `EventHandler`, `x` is a member of that collection and the call fails with `` `x` -> `bus` -> `x` ``.

## Order {#order}

`InvokeAll` returns members in a deterministic order. By default, members come **ancestors first**: by scope depth from the root scope down to the invoking scope, then by registration order within a scope. An `Override` keeps the position of the registration it replaces. The order is the same across calls and in clones.

### Ordered {#ordered}

A member whose instance implements `doger.Ordered` is positioned by its `Order()` value, lowest first. A member that does not implement it counts as `0`. The sort is stable: members with equal values keep the default order.

```go
type Middleware struct{ order int }

func (m *Middleware) Order() int { return m.order }
```

For a transient member, `Order()` is read from the instance of each call.

### WithOrder {#with-order}

`doger.WithOrder(n)` is a registration option that sets a member's order without touching its type, for example for a third-party type. It takes precedence over `Order()`, which is then not called, and when given more than once the last one wins. Every `Provide*`, `Override*` and `Package` helper accepts it:

```go
doger.ProvideValue(injector, vendor.NewRateLimiter(), doger.WithOrder(-1))
```

`WithOrder` has no effect outside collections: `Invoke` ignores it.

### Order() panics {#order-panics}

If `Order()` panics, `InvokeAll` returns no collection and an error naming the member, such as ``DI: Order() of collection member `audit` panicked: boom``, wrapping the panic value when it is an error. `MustInvokeAll` panics with that error.

### Maps are unordered {#maps-are-unordered}

`InvokeAllMap` never calls `Order()`: a map has no order.

## Collection order vs lifecycle order {#collection-order-vs-lifecycle-order}

`Ordered` and `WithOrder` only change the order of the slices `InvokeAll` returns. They never change the lifecycle:

- **construction** follows invocations and [declared dependencies](../service-registration/declared-dependencies.md);
- **start** follows the dependency graph, then scope depth and registration order (see [Starting the container](../service-lifecycle/starter.md#order));
- **shutdown** follows the dependency graph: a provider that collected members is shut down **before** them, whatever their order.

To order a lifecycle, declare dependencies instead.

## Snapshots and live collections {#live-collections}

`InvokeAll` and `InvokeAllMap` return a **snapshot**: services registered after the call are not added to it, and changing the result does not affect later calls. Resolve once and keep the result outside hot loops.

`doger.InvokeCollection[T]` returns a **live** `doger.Collection[T]` instead. Creating it builds nothing; each `All()` or `Map()` call resolves the members again, with the same rules as `InvokeAll` and `InvokeAllMap`:

```go
type Bus struct {
    handlers doger.Collection[EventHandler]
}

doger.Provide(injector, func(i doger.Injector) (*Bus, error) {
    return &Bus{handlers: doger.InvokeCollection[EventHandler](i)}, nil
})

// later, after more handlers were registered:
handlers, err := bus.handlers.All() // includes them
```

When the collection was created from a provider's injector, every read excludes that provider's service. A read made while the provider runs follows `InvokeAll` exactly: it detects cycles and records the provider's dependency on each member. Once the provider has returned, reads resolve from the provider's scope without its build-time invocation chain and record no dependency, because a member may itself depend on the holder: recording the reverse edge would create a cycle, and the stale chain would report a false circular dependency. Resolve with `InvokeAll` inside the provider if shutdown order must follow the members. Providers of the build chain that are still running keep their cycle detection: a read made from an outer provider, while it runs, reports a circular dependency on that provider's service instead of waiting for it. A `Collection` is safe to copy and to read concurrently.

:::warning

A read cannot tell which goroutine or build chain it runs in, which leaves two hazards:

- A goroutine **outside** the build chain that reads while an outer provider of the holder is still running gets `ErrCircularDependency` for that provider's service, although it is not part of a cycle. Read once the whole build has returned.
- A read made from **inside a member's provider**, by code that is not on the holder's build chain, waits for that member's own build and never returns. Inside a provider, use `InvokeAll` on the provider's own injector: it excludes the provider's service.

:::
