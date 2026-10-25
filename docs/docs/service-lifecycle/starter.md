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

## Start is one-shot {#one-shot}

A container starts at most once:

- any later `Start` returns an error matching `doger.ErrAlreadyStarted`, even if the first one failed;
- `Start` after root `Shutdown()` began returns an error matching `doger.ErrStartAfterShutdown`.

A clone starts independently of its source. Clones share eager instances, so starting a clone starts an eager starter a second time.
