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
    HookBeforeShutdown:     []func(scope *doger.Scope, serviceName string){},
    HookAfterShutdown:      []func(scope *doger.Scope, serviceName string, err error){},
    HookBeforeStart:        []func(scope *doger.Scope, serviceName string){},
    HookAfterStart:         []func(scope *doger.Scope, serviceName string, err error){},

    Logf: func(format string, args ...any) {
        // ...
    },

    HealthCheckParallelism:   100,
    HealthCheckGlobalTimeout: 1 * time.Second,
    HealthCheckTimeout:       100 * time.Millisecond,

    DisableStarterAutoDetection: false, // true: Start only starts AsStarter() services
})
```

### Start hooks {#start-hooks}

`HookBeforeStart` and `HookAfterStart` run around each service's `Start` method when the container [starts](../service-lifecycle/starter.md), in the order they were added. `HookAfterStart` receives the error the method returned, **unwrapped**: `nil` on success, or the recovered panic as an error. No start hook runs for an `AsStarter()` service whose instance has no `Start` method, since no method is called. A panicking hook propagates out of `Start`, as a panicking shutdown hook does.

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
