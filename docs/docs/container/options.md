---
title: Container options
description: Configure a doger injector — default options, the global container, package initialization, custom hooks, and health check timeouts.
sidebar_position: 2
---

# Container options

The DI container in `doger` is called an injector (`doger.Injector`, implemented by `*doger.RootScope`). It can be created with sensible defaults via `doger.New()`, or tuned with `doger.NewWithOpts()` for hooks, logging, and health check behavior.

## Default options {#default-options}

The simplest way to start is to use the default parameters:

```go
import "github.com/kartaladev/doger"

injector := doger.New()
```

## Global container {#global-container}

For a quick start, you may use the default global container. This is highly discouraged in production.

```go
import "github.com/kartaladev/doger"

doger.Provide(nil, ...)
doger.Invoke(nil, ...)

// equivalent to:

doger.Provide(doger.DefaultRootScope, ...)
doger.Invoke(doger.DefaultRootScope, ...)
```

## Register services on container initialization {#register-services-on-container-initialization}

The services can be assembled into a package, and then, imported all at once into a new container.

```go
// pkg/stores/package.go

var Package = doger.Package(
    doger.Lazy(NewPostgreSQLConnectionService),
    doger.Lazy(NewUserRepository),
    doger.Lazy(NewArticleRepository),
    doger.EagerNamed("repository.logger", slog.New(slog.NewTextHandler(os.Stdout, nil))),
)
```

```go
// cmd/main.go

import "example/pkg/stores"

injector := doger.New(stores.Package)
```

## Custom options {#custom-options}

```go
import "github.com/kartaladev/doger"

injector := doger.NewWithOpts(&doger.InjectorOpts{
    HookBeforeRegistration: []func(scope *doger.Scope, serviceName string){},
    HookAfterRegistration:  []func(scope *doger.Scope, serviceName string){},
    HookBeforeInvocation:   []func(scope *doger.Scope, serviceName string){},
    HookAfterInvocation:    []func(scope *doger.Scope, serviceName string, err error){},
    HookAfterBuild:         []func(scope *doger.Scope, serviceName string, instance any){},
    HookBeforeShutdown:     []func(scope *doger.Scope, serviceName string){},
    HookAfterShutdown:      []func(scope *doger.Scope, serviceName string, err error){},
    HookBeforeStart:        []func(scope *doger.Scope, serviceName string){},
    HookAfterStart:         []func(scope *doger.Scope, serviceName string, err error){},

    Logf: func(format string, args ...any) {
        // ...
    },

    HealthCheckParallelism:   100,
    HealthCheckGlobalTimeout: 1 * time.Second,
    HealthCheckTimeout:       100 * time.Millisecond, // also bounds HealthCheck[T] and HealthCheckNamed

    Clock: nil, // nil: the real clock
    DisableStarterAutoDetection: false, // true: Start only starts AsStarter() services
    StartParallelism: 0, // 0 or 1: sequential; n > 1: up to n Starters at a time
})
```

### Clock {#clock}

`InjectorOpts.Clock` is the container's time source, a [`clockwork.Clock`](https://pkg.go.dev/github.com/jonboulle/clockwork#Clock). The container reads time only from it: the `HealthCheckTimeout` and `HealthCheckGlobalTimeout` deadlines, lazy service build times (`ServiceBuildTime` in explain output), and `ShutdownReport.ShutdownTime`/`ServiceShutdownTime`.

- **Default:** `nil`, which means the real clock (`clockwork.NewRealClock()`).
- **Override:** pass a fake clock to control time in tests, for example to reach a health-check timeout without waiting for it.
- Child scopes and `Clone()` use the root scope's clock. `CloneWithOpts(opts)` uses `opts.Clock`.

```go
import "github.com/jonboulle/clockwork"

clock := clockwork.NewFakeClock()
injector := doger.NewWithOpts(&doger.InjectorOpts{
    Clock:              clock,
    HealthCheckTimeout: 5 * time.Second,
})

// ... start injector.HealthCheck() in a goroutine, then:
clock.Advance(5 * time.Second) // a check still running now times out
```

:::caution
With a fake clock, a context the container creates for a health check blocks on `ctx.Err()` until it is done (clockwork behavior). In a `HealthCheck(ctx)` method, wait on `<-ctx.Done()` instead of polling `ctx.Err()`.
:::

### Start hooks {#start-hooks}

`HookBeforeStart` and `HookAfterStart` run around each service's `Start` method when the container [starts](../service-lifecycle/starter.md), in the order they were added. `HookAfterStart` receives the error the method returned, **unwrapped**: `nil` on success, or the recovered panic as an error. No start hook runs for an `AsStarter()` service whose instance has no `Start` method, since no method is called. A panicking hook propagates out of `Start`, as a panicking shutdown hook does.

### Build hook {#build-hook}

`HookAfterBuild` receives each instance the container builds, with its scope and service name: after every successful provider call (each transient instance included) and after an eager value's first successful invocation. It runs between the before- and after-invocation hooks. It does not run for a failed build, a cached lazy instance or an alias (the alias's target reports its own build). A panicking hook propagates to the caller. The hook runs after the service released its lock, so it may invoke, health-check or explain the service it receives; another goroutine may already be using the instance while the hook runs.

```go
injector.AddAfterBuildHook(func(scope *doger.Scope, serviceName string, instance any) {
    metrics.ServicesBuilt.WithLabelValues(serviceName).Inc()
})
```

### Add hooks at runtime {#add-hooks-at-runtime}

Hooks can also be registered after the injector is created using helper methods on the root scope. These append to the corresponding hook lists in `doger.InjectorOpts` and apply to subsequent registrations/invocations/starts/shutdowns.

```go
import "github.com/kartaladev/doger"

injector := doger.New()

// Registration hooks
injector.AddBeforeRegistrationHook(func(scope *doger.Scope, serviceName string) {
    // ...
})
injector.AddAfterRegistrationHook(func(scope *doger.Scope, serviceName string) {
    // ...
})

// Invocation hooks
injector.AddBeforeInvocationHook(func(scope *doger.Scope, serviceName string) {
    // ...
})
injector.AddAfterInvocationHook(func(scope *doger.Scope, serviceName string, err error) {
    // ...
})

// Start hooks
injector.AddBeforeStartHook(func(scope *doger.Scope, serviceName string) {
    // ...
})
injector.AddAfterStartHook(func(scope *doger.Scope, serviceName string, err error) {
    // ...
})

// Shutdown hooks
injector.AddBeforeShutdownHook(func(scope *doger.Scope, serviceName string) {
    // ...
})
injector.AddAfterShutdownHook(func(scope *doger.Scope, serviceName string, err error) {
    // ...
})
```
