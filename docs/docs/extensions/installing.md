---
title: Installing extensions
description: Install a library integration into a container with doger.Extend, and control where it registers, its instances and its configuration
sidebar_position: 1
---

# Installing extensions

An extension packages the integration of a library (a Redis client, a SQL pool, a tracer...) so that an application installs it with one call. Extension modules are conventionally named `doger<lib>` and expose a constructor `Extension(opts...)`.

```go
root := doger.New()

if err := doger.Extend(root, dogerredis.Extension(dogerredis.Addr("cache:6379"))); err != nil {
    log.Fatal(err)
}

client := doger.MustInvoke[*redis.Client](root)
```

`Extend` takes the root container (`nil` means `doger.DefaultRootScope`) and calls the extension's `Extend` once.

## Where the services go {#placement}

By default, an extension registers its services into a **private child scope** of the root, named `ext:<name>`, or `ext:<name>/<instance>` for an extension with an instance key. Only the services it **exports** are visible from the root; its internals (a pool, a dialer...) stay private.

| Control                          | Effect                                                                                    |
| -------------------------------- | ----------------------------------------------------------------------------------------- |
| (default)                        | the extension's choice: private scope plus exports, or flat                               |
| `doger.Flatten()`                | register into the root's own scope, whatever the extension chose; exports are then no-ops |
| `doger.ExtendScopeName("cache")` | name the private scope `cache` instead of `ext:<name>`, for example to avoid a clash      |

```go
doger.Extend(root, dogerredis.Extension(), doger.Flatten())
doger.Extend(root, otherredis.Extension(), doger.ExtendScopeName("other-redis"))
```

A flattened install that fails is **not** rolled back: the services it registered before failing stay in the root.

## Installing twice, and instances {#identity}

An extension is identified by its Go type plus its instance key.

- Installing the same identity again with an equal extension value and the same options does nothing.
- With another value or other options, `Extend` returns an error matching `doger.ErrExtensionConflict`.
- Two different extensions whose private scopes would have the same name also conflict; rename one with `ExtendScopeName`.

To install a second instance, use the extension's instance option, for example `dogerredis.Instance("queue")`: it installs into `ext:redis/queue`.

Concurrent `Extend` calls install an identity once: a call for an identity being installed waits for that installation.

## Replacing what an extension provides {#replacing}

Extensions register their services as defaults (`doger.AsDefault()`) and export them. Register your own service under the published name, by default the type name, and it wins, whether you register it before or after `Extend`. The extension's own services then use yours too.

```go
doger.ProvideValue(root, myClient) // *redis.Client: replaces the extension's client
```

Where the extension or the application resolves by type (`InvokeAs`, collections), a service of the same type under another name takes part too.

## Configuration {#configuration}

Extensions read their configuration through a **property source**. Values given in code, as constructor options, win over properties, which win over the extension's defaults.

The default source reads environment variables prefixed with `DOGER_`. The key `redis.addr` maps to `DOGER_REDIS_ADDR`; a dot becomes `_` and a dash `__`, so `redis.cache-addr` maps to `DOGER_REDIS_CACHE__ADDR`. For an instance, the instance key comes after the name and a slash: `redis/queue.addr` maps to `DOGER_REDIS___QUEUE_ADDR` (three underscores), so it never shares a variable with `redis.queue.addr` (`DOGER_REDIS_QUEUE_ADDR`), a key of the extension without an instance.

To use another source, set `InjectorOpts.PropertySource`:

```go
root := doger.NewWithOpts(&doger.InjectorOpts{
    PropertySource: doger.EnvPropertySource("MYAPP_"), // or an adapter of viper, koanf...
})
```

or register a `doger.PropertySource` service in the root under `doger.NameOf[doger.PropertySource]()` before the first property read. The source is decided at the first read and kept; a source registered later is not used. `InjectorOpts.PropertySource` has no ordering constraint.

Property values are never logged, and no error the library returns contains one.

## When to call Extend {#phases}

`Extend` is allowed before `Start`, from a `HookContainerStarting` hook (the services are then started by that `Start`), and after `Start`: call `root.Refresh()` to start what the extension added. Once shutdown began, `Extend` returns an error matching `doger.ErrStartAfterShutdown`. Do not call it concurrently with `Start`.

## Lifecycle and health checks {#lifecycle}

The services of an extension's private scope are started by `Start`, shut down by root `Shutdown` after the root services that use their exports, and checked by root `HealthCheck`, keyed `<scope name>/<service name>`, for example `ext:redis/*redis.Pool`. See [Extension scopes](../service-lifecycle/healthchecker.md#extension-scopes).

A root `Shutdown` follows the usual rules: once its context ended, the services it reaches, extension services included, are skipped. See [Shutdown with a deadline](../service-lifecycle/shutdowner.md#shutdown-with-a-deadline).

## Errors {#errors}

| Error                           | Meaning                                                                     |
| ------------------------------- | --------------------------------------------------------------------------- |
| `ErrInvalidExtension`           | nil extension, invalid name or instance key, empty `ExtendScopeName`        |
| `ErrExtensionConflict`          | the identity is installed differently, or the private scope name is taken   |
| `*ExtensionError` (`errors.As`) | the extension failed to install; `Name`, `Instance`, and the cause in `Err` |
| `ErrExtensionCycle`             | extensions require each other in a cycle                                    |
| `ErrStartAfterShutdown`         | `Extend` after shutdown began                                               |

A failing install leaves nothing behind: the private scope is shut down and removed, its exports are removed, and a later `Extend` can retry.

A clone (`root.Clone()`) keeps the installed extensions, bound to its own copies of their scopes.
