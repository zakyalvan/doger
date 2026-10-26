---
title: Start lifecycle and lifecycle hooks releases
description: Changes to check when upgrading to the doger releases that add the Start lifecycle, lifecycle hooks and collections
sidebar_position: 2
---

# Upgrading to the Start lifecycle and lifecycle hooks releases

The Start lifecycle release adds container start, registration options, component kinds and declared dependencies. The lifecycle hooks release adds container and scope hooks, incremental start and collections. The changes below can affect existing code.

## Registration functions take options {#registration-options}

`Provide*`, `Override*` and the `Package` helpers (`Lazy*`, `Eager*`, `Transient*`) gain a trailing variadic parameter, `opts ...doger.ProvideOption`.

Every existing **call** compiles and behaves unchanged:

```go
doger.Provide(injector, NewDatabase)                       // unchanged
doger.Provide(injector, NewServer, doger.AsStarter())      // new: with an option
```

Code that stores one of these generic functions **as a value** of its old function type no longer compiles, because the function type now ends with `...doger.ProvideOption`:

```go
// no longer compiles
var register func(doger.Injector, doger.Provider[*Database]) = doger.Provide[*Database]
```

Wrap the call in a closure instead:

```go
register := func(i doger.Injector, p doger.Provider[*Database]) {
	doger.Provide(i, p)
}
```

## Root shutdown runs once {#root-shutdown-runs-once}

`Shutdown()` and `ShutdownWithContext()` on the root scope now shut the container down exactly once. This fixes concurrent calls, which used to race and report spurious `ErrServiceNotFound` errors.

Behavior changes:

- A second root `Shutdown()` returns a **copy of the first report** instead of an empty successful one.
- It no longer shuts down services registered on the root after the first shutdown. Create a new container instead of reusing a shut-down one.
- A `nil` context is treated as `context.Background()`; it used to panic once a built service had a `Shutdown` method.

`Shutdown()` on child scopes is unchanged. See [Root shutdown runs once](../service-lifecycle/shutdowner.md#root-shutdown-runs-once).

## An alias never shuts its target down {#alias-shutdown}

An alias registered with `As`, `AsNamed`, `Bind` or `BindNamed` no longer shuts its target down. The target is shut down only through its own registration, exactly once. This fixes an eager target (`ProvideValue`) being shut down twice once its alias had been invoked.

Behavior changes:

- Shutting down a child scope no longer shuts down a parent-scope target through an alias registered in the child. The target keeps running until its own scope shuts down.
- `ShutdownNamed` on an alias name removes the alias and no longer shuts its target down.

Migration: shut the target down by its own name, or through its own scope.

## Clones copy kind handlers {#clone-kind-handlers}

`Clone()` and `CloneWithOpts()` now copy the source's kind handlers, including a replaced `Runnable` handler, with their stages and order. A clone used to get only the default `Runnable` handler, so custom kinds had no handler on it.

Migration: none if you re-registered the same handlers on the clone. To use a different handler on a clone, call `Kind.Handle` on the clone; it replaces the handler on the clone only.

## InvokeAs selects deterministically {#invoke-as-deterministic}

`InvokeAs[T]`, `MustInvokeAs[T]` and the implicit-aliasing fallback of `InvokeStruct` (an empty `do:""` tag with no service of that name) used to return an arbitrary match when several services were assignable to `T`, depending on map iteration. They now select deterministically: the service in the nearest scope, then the earliest registration, then the smallest service name.

Since the previous pick was random, no code could rely on a specific one, but a call that used to succeed by luck may now consistently return another match. Migration: use `InvokeNamed` to pin a specific service.

## The `do:"*"` struct tag injects a collection {#star-struct-tag}

**BREAKING (edge case).** In `InvokeStruct`, the tag value `*` now means "every service assignable to the element type": a `[]E` field receives `InvokeAll[E]`, and a `map[string]E` field receives `InvokeAllMap[E]`. A service literally named `*` can no longer be injected through a struct tag, and `*` on any other field kind is an error.

Migration: rename a service called `*`, or inject it by type.
