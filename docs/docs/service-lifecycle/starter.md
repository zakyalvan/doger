---
title: Starting the container
description: Build and start background components in dependency order
sidebar_position: 3
---

# Starting the container

Background components such as servers, consumers and schedulers need to be started once the container is wired. `Start()` builds every component that needs starting, then starts them in dependency order.

```go
root := doger.New()
doger.Provide(root, NewDB)                      // Start(ctx) error -> auto-started
doger.Provide(root, NewHTTPServer)              // invokes *DB -> started after DB

if err := root.StartWithContext(ctx); err != nil {
	_ = root.Shutdown()                         // the caller owns cleanup
	return err
}
```

`Start()` and `StartWithContext(ctx)` exist on the root scope only.

## Starter interfaces {#starter-interfaces}

A service is started when it implements one of:

```go
type Starter interface{ Start() }
type StarterWithError interface{ Start() error }
type StarterWithContext interface{ Start(context.Context) }
type StarterWithContextAndError interface{ Start(context.Context) error }
```

There is no `Stop` counterpart: a started service is stopped by its [`Shutdown` method](./shutdowner.md) when the container shuts down. A starter without a `Shutdown` method is allowed.

## Which services are started {#detection}

Unlike `Shutdowner`, which is detected on the built instance, a starter is detected on its **registered type**, so a lazy service is discovered without being built.

| Registration                                        | Started                                           |
| --------------------------------------------------- | ------------------------------------------------- |
| Lazy or eager, registered type has a `Start` method | yes                                               |
| Registered under an interface type without `Start`  | no, unless registered with `doger.AsStarter()`    |
| Transient                                           | never (a factory has no single instance to start) |
| Alias (`As`, `Bind`)                                | no; the target is started once                    |

```go
// The interface has no Start method: opt in explicitly.
doger.Provide(root, NewLifecycle, doger.AsStarter())
```

With `AsStarter()` on an interface-typed registration, the instance is checked when `Start` runs.

`Start` covers the root scope and every child scope that exists when it runs. Services registered after `Start`, and scopes created after it, are not started; they stay invokable as usual.

### Disabling auto-detection {#disable-auto-detection}

```go
root := doger.NewWithOpts(&doger.InjectorOpts{
	DisableStarterAutoDetection: true, // only AsStarter() services are started
})
```

:::caution Surprising auto-detection

Any registered type with a matching `Start` method is started, including standard-library types such as `*exec.Cmd`. A `*doger.RootScope` registered as a service of another container is started, but never shut down automatically. Code that already calls `Start()` by hand would start such services twice once it calls `root.Start()`. Disable auto-detection, or register the service under an interface type without `Start`.

:::

## Order {#order}

`Start` runs in two passes:

1. **Build.** Every candidate is built first, so every provider call happens before the first `Start` method. A service's dependencies, whether invoked by its provider or declared with [`DependsOn`](../service-registration/declared-dependencies.md), are built before it.
2. **Start.** Starters start one at a time. A starter starts only after every starter it depends on, directly or through services that are not starters, has started.

The dependency graph is the only source of ordering; there is no `Order` or `Phase` interface. To force an order between services that do not invoke each other, declare a dependency edge with `DependsOn` or `DeclareDependency`.

### Ties {#ties}

Starters with no dependency path between them start in a deterministic order:

1. by scope depth: the root scope first, then its children, then grandchildren;
2. then by registration order. An `Override` in the same scope keeps the position of the first registration; a clone keeps its source's positions, and services registered later in the clone come after them.

```go
request := root.Scope("request")
doger.ProvideNamed(request, "z", NewZ) // registered first, but in a child scope
doger.ProvideNamed(root, "b", NewB)
doger.ProvideNamed(root, "a", NewA)
_ = root.Start() // starts b, a, z
```

Starters are started sequentially.

### Cycles {#cycles}

A cycle that only exists once services are built, for example a [declared edge](../service-registration/declared-dependencies.md#declare-dependency) later closed by a provider's invocation, makes `Start` fail with an error matching `doger.ErrCircularDependency`, naming the chain, before any starter is started.

## Context {#context}

`StartWithContext(ctx)` derives its own cancellable context from `ctx`; `Start()` uses `context.Background()`, and a `nil` context is treated the same way.

- `Start(ctx)` methods, kind handlers and runners receive the derived context.
- The derived context is **cancelled when `Start` returns**. Do not keep it for long-running work: start your own goroutine with its own context in `Start`, and stop it in `Shutdown`.
- `Start` checks the context before each build, each handler call and each starter. A cancelled or expired context stops `Start` with an error wrapping `ctx.Err()`, so `errors.Is(err, context.DeadlineExceeded)` works.

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := root.StartWithContext(ctx); err != nil {
	_ = root.Shutdown()
	return err
}
```

## Errors {#errors}

On the first failure, `Start` stops and returns an error that names the service and its scope and wraps the cause (`errors.Is` / `errors.As` work). A failure is:

- a build error, including a missing or circular [declared dependency](../service-registration/declared-dependencies.md);
- an error returned by a `Start` method;
- a panic inside a `Start` method, recovered and returned as an error;
- an `AsStarter()` service whose instance has no `Start` method.

Starters not reached yet are not started, and **nothing is rolled back**. Cleanup is the caller's job:

```go
if err := root.Start(); err != nil {
	_ = root.Shutdown() // shuts down every built service, started or not
	return err
}
```

Services must therefore tolerate `Shutdown` without a prior `Start`. A panic that escapes `Start` (for example from a hook) still marks the container as started, so a later `Shutdown()` never blocks on it.

### StartError {#start-error}

A failure of a service or of a kind handler is a `*doger.StartError`. Its `Error()` message is the one shown above, and `errors.Is` reaches the cause through `Unwrap`. Use `errors.AsType` (or `errors.As`) to inspect it:

| Field     | Content                                                             |
| --------- | ------------------------------------------------------------------- |
| `Scope`   | the scope the service is registered in                              |
| `Service` | the service name                                                    |
| `Kind`    | the kind whose handler failed; empty unless `Op` is `StartOpHandle` |
| `Op`      | `StartOpBuild`, `StartOpStart` or `StartOpHandle`                   |
| `Cause`   | the underlying error                                                |

```go
if startErr, ok := errors.AsType[*doger.StartError](err); ok {
	log.Printf("%s failed at %s in scope %s: %v", startErr.Service, startErr.Op, startErr.Scope.Name(), startErr.Cause)
}
```

Errors that are not tied to a service are not `StartError` values: `ErrAlreadyStarted`, `ErrStartAfterShutdown`, a cancelled context and a dependency cycle.

## Shutdown during Start {#shutdown-during-start}

A root `Shutdown()` that begins while `Start` runs:

1. cancels the Start-time context with cause `doger.ErrStartAfterShutdown`;
2. waits for `Start` to return, bounded by its own context;
3. shuts services down.

`Start` stops before its next starter or handler and returns an error matching `doger.ErrStartAfterShutdown`.

If Shutdown's own context ends first (for example a 10-second deadline), its report fails with an error for the root scope wrapping `ctx.Err()`, and shutdown proceeds while `Start` is still running. A starter that ignores its context and finishes after that is **not rolled back**: honour `ctx` in `Start(ctx)`.

:::danger Do not call Shutdown from Start-time code

A starter, kind handler, runner, provider or hook that runs during `Start` must not call `root.Shutdown()` synchronously: Shutdown waits for `Start`, which waits for that code. Return an error instead; `Start` stops and the caller shuts down. Calling `Start` from such code returns `ErrAlreadyStarted` immediately.

:::

Only the root scope coordinates Start and Shutdown. A shutdown reached through the root's `*Scope` (the injector a provider receives) or a child scope bypasses this coordination.

## Start is one-shot {#one-shot}

A container starts at most once:

- any later `Start` returns an error matching `doger.ErrAlreadyStarted`, even if the first one failed;
- `Start` after root `Shutdown()` began returns an error matching `doger.ErrStartAfterShutdown`.

A clone starts independently of its source. Clones share eager instances, so starting a clone starts an eager starter a second time.
