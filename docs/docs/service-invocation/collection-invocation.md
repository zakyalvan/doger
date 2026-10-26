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
