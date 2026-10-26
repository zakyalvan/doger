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

**Shadowing.** A service hidden by a service of the same name in a nearer scope is no longer a candidate, as for `InvokeAll`. For example, with `db` (a `*PG` implementing `Store`) in the root and a `db` string in a child scope, `InvokeAs[Store](child)` used to return the hidden root `*PG`; it now returns an error matching `ErrServiceNotMatch`, and the struct fallback returns `ErrServiceNotFound` instead of failing with "not assignable". Migration: rename one of the services, or invoke the ancestor's service by name from its own scope.

**The invoking service is skipped.** Called from a provider, `InvokeAs` and the struct fallback skip the service that provider builds, as `InvokeAll` does. A decorator that resolves its own interface used to fail with `ErrCircularDependency` whenever it picked itself; it now resolves the service it wraps. When the provider's own service is the only match, the error now matches `ErrServiceNotMatch` instead of `ErrCircularDependency`. Migration: none for decorators; code that checked for `ErrCircularDependency` in that case checks for `ErrServiceNotMatch`.

Performance: to pick deterministically, `InvokeAs` checks every service of the nearest scope that has a match, instead of stopping at the first one found. In a scope with 20 services this costs about 0.1µs (+9%) per call, with fewer allocations than before. On hot paths, prefer `InvokeNamed` or `Invoke` with the concrete type, or resolve once and keep the instance.

## The `do:"*"` struct tag injects a collection {#star-struct-tag}

**BREAKING (edge case).** In `InvokeStruct`, the tag value `*` now means "every service assignable to the element type": a `[]E` field receives `InvokeAll[E]`, and a `map[string]E` field receives `InvokeAllMap[E]`. A service literally named `*` can no longer be injected through a struct tag, and `*` on any other field kind is an error.

Migration: rename a service called `*`, or inject it by type.

## Single-service health checks honor HealthCheckTimeout {#per-service-health-check-timeout}

`HealthCheck[T]`, `HealthCheckWithContext[T]`, `HealthCheckNamed` and `HealthCheckNamedWithContext` are now bounded by `InjectorOpts.HealthCheckTimeout` when it is greater than zero, like each check of `injector.HealthCheck()`. They used to run unbounded. `HealthCheckGlobalTimeout` still does not apply to them.

Migration: none if `HealthCheckTimeout` is `0`. Otherwise, a single-service check that runs longer than the timeout now returns an error matching `ErrHealthCheckTimeout`.

## Failed hooks run when a per-service hook panics during Start {#failed-on-hook-panic}

When a per-service hook (`HookBeforeStart`, `HookAfterStart`, `HookAfterBuild`) panics during the first `Start`, the panic is still re-raised on the calling goroutine, but `Start` now runs the `HookContainerFailed` hooks first, with an error wrapping the panic value. They used to be skipped, although the container was marked failed. A Failed hook that panics meanwhile goes to `HookContainerPanic` when set, and is logged otherwise, so the original panic is the one re-raised.

Migration: none, unless a Failed hook assumed that `Start` had returned. Recover the panic in the per-service hook to turn it into a normal failure.

## A child scope shutdown waits for a running start {#child-shutdown-waits}

`Scope.Shutdown()` and `Scope.ShutdownWithContext(ctx)` on a child scope now wait for a `Start`, `Refresh` or `Scope.Start` that is starting services of that scope before shutting it down, bounded by `ctx`. They used to run concurrently with it, so a service could start after its scope had been shut down and keep running. A run that begins while the scope shuts down now skips it.

Migration: none for shutdowns that happen after start. Code running during a start (a starter, kind handler, provider or hook) must not synchronously shut down a scope that this start is starting: it would wait for itself. Shut the scope down after the start returned, or bound the wait with `ShutdownWithContext`.
