---
title: Declared dependencies
description: Declare a dependency between services that do not invoke each other
sidebar_position: 5
---

# Declared dependencies

doger records a dependency edge whenever a provider invokes another service. These edges order shutdown (dependents first) and, with `Start`, construction and start.

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
