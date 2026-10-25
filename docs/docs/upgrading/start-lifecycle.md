---
title: Start lifecycle release
description: Changes to check when upgrading to the doger release that adds the Start lifecycle
sidebar_position: 2
---

# Upgrading to the Start lifecycle release

This release adds container start, registration options, component kinds and declared dependencies. Two changes can affect existing code.

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
