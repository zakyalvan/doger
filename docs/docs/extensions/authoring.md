---
title: Writing an extension
description: Implement the doger Extension contract to make a library installable in one call, with private internals, replaceable defaults and configuration
sidebar_position: 2
---

# Writing an extension

An extension implements two methods:

```go
type Extension interface {
    Name() string
    Extend(x *doger.ExtensionContext) error
}
```

`Name` is lowercase alphanumeric words joined by single dashes, such as `redis` or `redis-cluster`. `Extend` registers the services through `x`. Further extension points are optional interfaces, never new methods, so your extension keeps compiling as the SPI grows:

| Optional interface | Purpose |
|---|---|
| `ExtensionInstance` (`Instance() string`) | an instance key, to install the extension more than once |
| `ExtensionPlacement` (`Placement() Placement`) | `PlacementFlat` to register into the root; the default is `PlacementPrivate` |

## A complete extension {#example}

```go
package dogerredis

type Option func(*extension)

func Addr(addr string) Option     { return func(e *extension) { e.Addr = addr } }
func Instance(key string) Option  { return func(e *extension) { e.InstanceKey = key } }

// extension holds plain data only, so that installing it twice with the same
// options is recognized as the same install.
type extension struct {
    Addr        string
    InstanceKey string
}

// Extension returns a pointer: T and *T would be two identities.
func Extension(opts ...Option) doger.Extension {
    e := &extension{}
    for _, opt := range opts {
        opt(e)
    }
    return e
}

func (e *extension) Name() string     { return "redis" }
func (e *extension) Instance() string { return e.InstanceKey }

func (e *extension) Extend(x *doger.ExtensionContext) error {
    addr := e.Addr // the code option, read now: x is valid only during Extend

    // private internals
    doger.Provide(x.Scope(), func(i doger.Injector) (*redis.Pool, error) {
        return newPool(i, addr)
    }, doger.AsDefault())
    // the client the application uses, as a replaceable default
    doger.Provide(x.Scope(), newClient, doger.AsDefault())

    return doger.ExtensionExport[*redis.Client](x)
}

// newPool gives precedence to the code option, then to the "addr" property,
// then to the extension's default.
func newPool(i doger.Injector, addr string) (*redis.Pool, error) {
    if addr == "" {
        addr = "localhost:6379"
        if properties, ok := doger.ExtensionProperties(i); ok {
            if value, found := properties.Lookup("addr"); found {
                addr = value
            }
        }
    }
    return redis.NewPool(addr)
}
```

## Rules of thumb {#rules}

- **Register with `doger.AsDefault()` and export.** Exports of defaults are defaults: the application replaces your client by registering its own under the published name, before or after `Extend`, and your other services use the application's instance.
- **Keep options as plain data.** Installing the same identity twice is a no-op only when the values are deeply equal; a non-nil `func` field never is, so a second install with such an option conflicts.
- **Return a pointer from your constructor.** `T` and `*T` are two identities, and they collide on the private scope name.
- **Export only what the application uses.** Everything else stays private to your scope.
- **A failing `Extend` is rolled back**: return an error, and the private scope is shut down (with a context that never ends, so built services are shut down) and removed, with its exports. A panic is recovered into an `*ExtensionError` too, whether it happens in `Extend` or while your exports are applied (in a registration hook, for example). The rollback always completes: the scope is detached and the exports removed even when a scope hook panics during the shutdown, and that panic is raised afterwards, as for any shutdown. A shutdown that fails is reported: its report is joined to the returned error. Only the exports of the failed attempt are removed, never an application service registered under the published name.

## Depending on another extension {#require}

Install the extensions yours depends on with `x.Require`, never with `doger.Extend`:

```go
func (e *extension) Extend(x *doger.ExtensionContext) error {
    if err := x.Require(dogerredis.Extension()); err != nil {
        return err
    }
    // ...
}
```

`Require` returns `nil` when the identity is already installed, whatever its options, so several extensions can require the same one. A cycle (`a` requires `b` requires `a`), on one goroutine or across goroutines installing concurrently, fails with `ErrExtensionCycle` instead of deadlocking. A `doger.Extend` called from inside `Extend` is not tracked this way: if it leads back to an extension being installed, it deadlocks, as a `sync.Once.Do` called from its own function does.

An install made only through `Require` is replaced by a later explicit `doger.Extend` of the same identity with other options, as long as none of its services, nor any service of a scope below its private scope, was resolved; afterwards, that `Extend` conflicts. The replacing `Extend` may rename the private scope or flatten the extension. A required install that its author places flat (`PlacementFlat`) cannot be rolled back, so it is never replaced. If the replacing `Extend` fails, the required install is installed again from its value, so the extensions that required it keep working. Resolving the required extension's services while an `Extend` replaces it is unsupported: such a resolution may get a service that is then shut down.

## Configuration {#configuration}

- During `Extend`, read properties with `x.Properties().Lookup("addr")`. The view is prefixed with your name and instance key: `redis.addr`, or `redis/queue.addr` for the instance `queue`.
- In providers, which run later, use `doger.ExtensionProperties(i)` with the injector the provider receives: it follows the container, so a clone with its own property source reads from it. The context `x` is valid only while `Extend` runs. A provider registered in a scope you create below your private scope gets your view too.
- When the installer flattens your extension (`doger.Flatten()`), your providers run in the root, where `ExtensionProperties` returns a view that always misses (and `false`). To work flattened too, read your configuration in `Extend`, with `x.Properties()`, and capture it in your providers' closures, as the example above does with the `addr` option.
- Keys are lowercase alphanumeric words joined by single dashes, in dot-separated segments (`pool-size`, `tls.enabled`). An invalid key misses.
- Give precedence to your constructor's options, then to properties, then to your defaults. The library does not enforce it.

## Declining to install {#disabled}

The library has no enable/disable switch. To offer one, read it and decline by returning an error matching `doger.ErrExtensionDisabled`:

```go
if value, ok := x.Properties().Lookup("enabled"); ok && value == "false" {
    return fmt.Errorf("turned off by configuration: %w", doger.ErrExtensionDisabled)
}
```

`Extend` then rolls back, logs one line naming the extension, and returns `nil`. Installed with `doger.Flatten()`, nothing is rolled back: what you registered before declining stays in the root, so decline before registering anything. If shutting the declined attempt down fails, `Extend` returns an `*ExtensionError` holding the failed report instead, which does not match `ErrExtensionDisabled`, so a leak is never mistaken for a clean decline; the extension is still recorded as disabled. An extension that requires a disabled one gets an error matching `ErrRequiredExtensionDisabled`, which fails it instead of disabling it in turn.
