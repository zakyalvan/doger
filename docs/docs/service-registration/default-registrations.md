---
title: Default registrations
description: Ship a service that the application can replace, whatever the registration order
sidebar_position: 6
---

# Default registrations

A reusable package often ships a service with a sensible default implementation: a dialer, a clock, a property source. The application should be able to replace it without forking the package and without caring whether it registers its own service before or after the package does.

Register the package's service with `doger.AsDefault()`. An explicit registration, one without `AsDefault()`, wins over it.

```go
// in the package
doger.Provide(scope, NewDefaultDialer, doger.AsDefault())

// in the application, before or after the package registers
doger.Provide(root, NewAppDialer)
```

Every registration form accepts the option: `Provide*` (lazy), `ProvideValue*` (eager), `ProvideTransient*`, and the `Package` helpers (`Lazy`, `Eager`, `Transient` and their named forms).

## Yielding to an ancestor {#yield}

On its first resolution, a default looks for an explicit service of **the same name** in an **ancestor** of its own scope (its parent, grandparent, up to the root). If it finds one, the default **yields**: it resolves to that service's instance, and its own provider is never called. Otherwise it **chooses** its own provider or value.

| Explicit service of the same name in         | The default                                       |
| -------------------------------------------- | ------------------------------------------------- |
| an ancestor scope                            | yields to it                                      |
| a sibling or a descendant scope              | is not affected: it uses its own provider         |
| nowhere                                      | uses its own provider                             |
| an ancestor, but registered with AsDefault() | is not affected: defaults never yield to defaults |

Because the decision happens at first resolution, not at registration, the order of registration does not matter.

```go
root := doger.New()
ext := root.Scope("ext")

doger.Provide(ext, NewDefaultDialer, doger.AsDefault())
doger.Provide(root, NewAppDialer) // registered afterwards: still wins

dialer := doger.MustInvoke[*Dialer](ext) // the application's dialer
```

An alias of a default (`As`, `Bind`) resolves through the default's decision.

## In the same scope {#same-scope}

| First registration  | Then                                 | Outcome                                                                  |
| ------------------- | ------------------------------------ | ------------------------------------------------------------------------ |
| a default           | an explicit registration of the name | the explicit one replaces the default, keeping its registration position |
| an explicit service | a default of the name                | the default is skipped, silently                                         |
| a default, resolved | an explicit registration of the name | panics: `DI: service ... has already been declared`                      |
| a default           | a second default of the name         | panics: `DI: service ... has already been declared`                      |

An instance a default built is never silently dropped: once resolved, it can only be replaced with `Override*`.

Options are validated before a default is skipped, so an invalid combination panics either way. The before-registration hooks run for a skipped default, the after-registration hooks do not.

## The decision is final {#frozen}

A default decides once and never changes its mind. These events fix the decision:

- an invocation by name;
- [`InvokeAs`](../service-invocation/accept-interfaces-return-structs.md) or struct injection selecting it by type;
- a [collection](../service-invocation/collection-invocation.md) including it;
- [`Start`, `Refresh` or `Scope.Start`](../service-lifecycle/starter.md) considering it as a Starter or a kind member;
- a health check of the default's scope (`HealthCheck` on that scope or a descendant), which decides without building anything;
- root shutdown, for an eager default never resolved.

Explaining the container (`ExplainInjector`, `ExplainService`, `ExplainNamedService`) never fixes a decision.

An explicit service registered in an ancestor afterwards does not replace a default that already chose its own provider. Resolve from the ancestor's scope to get the ancestor's service.

## Lifecycle {#lifecycle}

A default that **yields** behaves like an alias of the service it yields to. It is never started, shut down, health-checked or handled by a kind on its own, so the target goes through each of these exactly once. A health check from the default's scope reports the target's result under the name. The other options of the default (`AsStarter`, `DependsOn`, `AsRunnable`, kinds, `WithOrder`) have no effect.

A default that **uses its own provider** takes part in start, kind handling, health checks and shutdown exactly like the same registration without `AsDefault()`.

### Eager values {#eager}

An eager value exists at registration, so for `ProvideValue` the option means "replaceable until first resolved". The container shuts an eager default value down only when the default uses it. A value that was replaced in its scope, or that yielded, belongs to the code that created it: shut it down there.

## By type {#by-type}

`InvokeAs[T]` and struct injection with an empty tag prefer explicit services: they consider an unresolved default only when no other visible service is assignable to `T`. A default that already chose its own provider ranks as an explicit service; a default that yielded is never a candidate.

Collections (`InvokeAll`, `InvokeAllMap`, live collections) leave out an unresolved default when an explicit member is assignable to the default's **registered type**. Register a default meant to be replaced by type under the interface type:

```go
// replaced by any explicit PropertySource in collections
doger.Provide(root, func(i doger.Injector) (PropertySource, error) { return NewEnvSource(), nil }, doger.AsDefault())
```

A default registered under its concrete type (`*EnvSource`) is replaced only by another `*EnvSource`. In both cases, a default never hides an explicit service of the same name in an ancestor.

## Override {#override}

`Override*` ignores `AsDefault()`: an override is explicit by definition. It replaces whatever is registered under the name, default or not, with the usual caveat that a built instance is not shut down.

## Exporting a default {#export}

[Exporting](../container/scope.md#scope-export) a default from a child scope publishes a default in the parent: an explicit service of the published name in the parent wins, whether it is registered before or after `Export`.

```go
ext := root.Scope("ext")
doger.Provide(ext, NewClient, doger.AsDefault()) // the extension's client
doger.MustExport[*Client](ext)

doger.Provide(root, NewAppClient) // the application's client wins
```

Every reader then sees the application's instance: the root resolves it, and the private default in `ext` yields to it, as any default yields to an explicit service of an ancestor. `Export` over an explicit service of the published name returns nil and leaves that service in place. The private default never yields to its own export. Export a source registered without `AsDefault()` to publish an explicit service.

## Defaults & control {#control}

| Behavior                                               | Zero-config default                                       | Consumer control                                                                      |
| ------------------------------------------------------ | --------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| Registration without `AsDefault()`                     | Unchanged: explicit, and a duplicate name panics          | -                                                                                     |
| A default meets an explicit service of the same name   | The explicit service wins, whichever came first           | Don't mark the service as a default, or use `Override*`                               |
| A default and an explicit service of a compatible type | The explicit service wins by type; the default is skipped | Invoke the default by name, or register the explicit service under the default's name |
| Explicit registration after the default was resolved   | Panics like a duplicate                                   | Register before resolving, or use `Override*`                                         |
| A default that yields                                  | No start, shutdown, health check or kind handling         | Not overridable: lifecycle belongs to the service that provides the instance          |
| Exporting a default                                    | The export is a default in the parent                     | Register an explicit service of the published name, or export a non-default source    |

## Explain {#explain}

`ExplainService` and `ExplainNamedService` report the default state (`not-default`, `unresolved`, `chosen` or `yielded`) and, for a default that yielded, the scope and service it yielded to.
