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

| Registration | Started |
|---|---|
| Lazy or eager, registered type has a `Start` method | yes |
| Registered under an interface type without `Start` | no, unless registered with `doger.AsStarter()` |
| Transient | never (a factory has no single instance to start) |
| Alias (`As`, `Bind`) | no; the target is started once |

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

## Start is one-shot {#one-shot}

A container starts at most once:

- any later `Start` returns an error matching `doger.ErrAlreadyStarted`, even if the first one failed;
- `Start` after root `Shutdown()` began returns an error matching `doger.ErrStartAfterShutdown`.

A clone starts independently of its source. Clones share eager instances, so starting a clone starts an eager starter a second time.
