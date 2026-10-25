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
