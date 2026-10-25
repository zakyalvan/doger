---
title: Declared dependencies
description: Declare a dependency between services that do not invoke each other
sidebar_position: 5
---

# Declared dependencies

doger records a dependency edge whenever a provider invokes another service. These edges order shutdown (dependents first) and, with [`Start`](../service-lifecycle/starter.md), construction and start.

Some dependencies are hidden: a provider keeps the injector and invokes a service later, at runtime, or two services only need to start and stop in a given order. Declare those edges explicitly.

## Static: the `DependsOn` option {#depends-on}

Attach `doger.DependsOn[T]()` or `doger.DependsOnNamed(name)` at registration:

```go
doger.Provide(injector, NewMetrics)

doger.Provide(injector, NewReporter, doger.DependsOn[*Metrics]())
// or by name
doger.ProvideNamed(injector, "reporter", NewReporter, doger.DependsOnNamed("metrics"))
```

When the dependent is built, each declared dependency is built first, in declaration order, and an edge is recorded exactly as if the provider had invoked it. A static edge therefore orders construction, start and shutdown.

- **Lazy and transient services** resolve their declared dependencies before the provider runs.
- **Eager values** resolve them on the first invocation. If that fails (for example the dependency is registered later), the next invocation tries again.
- The dependency is resolved from the dependent's scope or its ancestors, as `Invoke` would.
- Repeated declarations accumulate in order; duplicates are collapsed. `DependsOn*` is allowed on transient services.

### Errors {#depends-on-errors}

Building the dependent fails, without calling its provider, when:

- the declared dependency is not registered in the dependent's scope or an ancestor: the error matches `doger.ErrServiceNotFound` and names both services;
- the declared dependency closes a cycle: the error matches `doger.ErrCircularDependency` and names the chain, for example `` `a` -> `b` -> `a` ``.

Registering a declared dependency never fails: the target does not need to exist yet.

## Dynamic: `DeclareDependency` {#declare-dependency}

`doger.DeclareDependency(injector, dependent, dependency)` records `dependent -> dependency` at runtime, without building either service. A component-kind handler typically calls it during `Start`, for example to make a scheduler depend on the jobs it drives:

```go
if err := doger.DeclareDependency(injector, "scheduler", "job"); err != nil {
	return err
}
// on Shutdown: the scheduler stops before the job
```

- `dependent` is resolved from the injector (`nil` means `DefaultRootScope`); `dependency` is resolved from the **dependent's own scope or its ancestors**, as `Invoke` would. An edge therefore always points to the same scope or an ancestor, which keeps it consistent with children-first shutdown.
- An unresolved name returns an error matching `doger.ErrServiceNotFound`.
- If `dependency` already reaches `dependent`, through recorded edges or static `DependsOn` declarations (even of services not built yet), the call returns an error matching `doger.ErrCircularDependency`. A service cannot depend on itself.
- Declaring the same edge twice changes nothing. Concurrent calls are safe.

A dynamic edge builds nothing, so it orders **start** (when declared before or during `StageBeforeStart`) and **shutdown**, but not construction. A cycle that only appears once services are built, for example a declared edge later closed by a provider's invocation, is rejected by `Start`.

## Shutdown order {#shutdown-order}

Shutdown treats declared edges, static and dynamic, exactly like invoked dependencies: a dependent is shut down before the services it depends on.

## Clones {#clones}

A clone starts with an empty dependency graph:

- static `DependsOn` edges are recorded again when the clone builds the dependent;
- `DeclareDependency` edges are **not** copied. Re-declare them on the clone, for example from a kind handler, which runs again when the clone starts.
