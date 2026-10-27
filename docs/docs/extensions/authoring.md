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
- **A failing `Extend` is rolled back**: return an error, and the private scope is shut down (with a context that never ends, so built services are shut down) and removed, with its exports. A panic is recovered into an `*ExtensionError` too.

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

An install made only through `Require` is replaced by a later explicit `doger.Extend` of the same identity with other options, as long as none of its services was resolved; afterwards, that `Extend` conflicts.

## Configuration {#configuration}

- During `Extend`, read properties with `x.Properties().Lookup("addr")`. The view is prefixed with your name and instance key: `redis.addr`, or `redis.queue.addr`.
- In providers, which run later, use `doger.ExtensionProperties(i)` with the injector the provider receives: it follows the container, so a clone with its own property source reads from it. The context `x` is valid only while `Extend` runs.
- Keys are lowercase alphanumeric words joined by single dashes, in dot-separated segments (`pool-size`, `tls.enabled`). An invalid key misses.
- Give precedence to your constructor's options, then to properties, then to your defaults. The library does not enforce it.

## Declining to install {#disabled}

The library has no enable/disable switch. To offer one, read it and decline by returning an error matching `doger.ErrExtensionDisabled`:

```go
if value, ok := x.Properties().Lookup("enabled"); ok && value == "false" {
    return fmt.Errorf("turned off by configuration: %w", doger.ErrExtensionDisabled)
}
```

`Extend` then rolls back, logs one line naming the extension, and returns `nil`. An extension that requires a disabled one gets an error matching `ErrRequiredExtensionDisabled`, which fails it instead of disabling it in turn.
