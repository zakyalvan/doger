---
title: Service health check
description: Check the health of doger services individually or globally by implementing the Healthchecker interface, with configurable parallelism and timeouts.
sidebar_position: 1
---

# Service health check

A health check verifies that a service's dependencies (a database, a cache, an external API...) are still reachable, on demand. If your service relies on a dependency, you might want to periodically check its state.

When the `doger.HealthCheck[type]()` or the `injector.HealthCheck()` function is called, the framework triggers the `HealthCheck` method of each service implementing the `doger.Healthchecker` interface, in reverse invocation order.

🕵️ Service health can be checked individually or globally. Requesting a health check on a nested scope will run checks on ancestors.

Lazy services that were not invoked are not checked.

## Trigger health check {#trigger-health-check}

A health check can be triggered for a root injector:

```go
// returns the status (error or nil) for each service
injector.HealthCheck() map[string]error
injector.HealthCheckWithContext(context.Context) map[string]error
```

...on a single service:

```go
// returns error on failure
doger.HealthCheck[T any](doger.Injector) error
doger.HealthCheckWithContext[T any](context.Context, doger.Injector) error
doger.HealthCheckNamed[T any](doger.Injector, string) error
doger.HealthCheckNamedWithContext[T any](context.Context, doger.Injector, string) error
```

## Healthchecker interfaces {#healthchecker-interfaces}

Your service can implement one of the following signatures:

```go
type Healthchecker interface {
	HealthCheck() error
}

type HealthcheckerWithContext interface {
	HealthCheck(context.Context) error
}
```

### Example {#example}

```go
// Ensure at compile-time MyService implements doger.HealthcheckerWithContext
var _ doger.HealthcheckerWithContext = (*MyService)(nil)

type MyService struct {}

func (*MyService) HealthCheck(context.Context) error {
    // ...
    return nil
}

i := doger.New()

doger.Provide(i, ...)
doger.Invoke(i, ...)

ctx := context.WithTimeout(10 * time.Second)
i.HealthCheckWithContext(ctx)
```

**Play: https://go.dev/play/p/ILV7UpAJDtc**

## Health check options {#health-check-options}

The root scope can be created with health check parameters, for controlling parallelism or timeouts.

```go
doger.InjectorOpts{
    // ...

    // By default, health checks are triggered concurrently.
    // HealthCheckParallelism==1 will trigger sequential checks.
    HealthCheckParallelism    uint

    // When many services are checked at the same time.
    HealthCheckGlobalTimeout: time.Duration
    HealthCheckTimeout:       time.Duration
}
```

Example:

```go
type MyPostgreSQLConnection struct {
    DB *sql.DB
}

func (pg *MyPostgreSQLConnection) HealthCheck() error {
    return pg.DB.Ping() // <- might be very slow
}

i := doger.NewWithOpts(&doger.InjectorOpts{
    HealthCheckParallelism:   100,
    HealthCheckGlobalTimeout: 1 * time.Second,
    HealthCheckTimeout:       100 * time.Millisecond,
})

doger.Provide(i, NewMyPostgreSQLConnection)
_ = doger.MustInvoke[*MyPostgreSQLConnection](i)

status := i.HealthCheckWithContext(ctx)
// {
//     "*github.com/samber/example.MyPostgreSQLConnection": "DI: health check timeout: context deadline exceeded",
// }
```

## Timeouts and ended contexts {#timeouts-and-ended-contexts}

The result of a health check does not depend on goroutine scheduling:

- When the service has a health check and its context is done (cancelled, or past a deadline set by you or by the timeout options) by the time the result is observed, the result is the timeout error, whatever the check returned. This holds for single-service checks and for `injector.HealthCheck*`.
- The timeout error matches `doger.ErrHealthCheckTimeout` and the context error with `errors.Is`. Its message is `DI: health check timeout: context canceled` or `DI: health check timeout: context deadline exceeded`.
- A check that returns before its context ends keeps its own result.
- A service without a health check (a value without a `HealthCheck` method, a lazy service not invoked yet, a transient service) always reports `nil`, even on an ended context.
- `HealthCheckTimeout` and `HealthCheckGlobalTimeout` are measured on the injector's [clock](../container/options.md#clock). A check that ends exactly when one of these timeouts elapses is a timeout. A deadline you set on your own context counts when that context is done.

```go
ctx, cancel := context.WithCancel(context.Background())
cancel()

err := doger.HealthCheckNamedWithContext(ctx, i, "db")
errors.Is(err, doger.ErrHealthCheckTimeout) // true
errors.Is(err, context.Canceled)            // true
```

### Testing timeouts with a fake clock {#testing-timeouts-with-a-fake-clock}

Set `InjectorOpts.Clock` to a fake clock to reach `HealthCheckTimeout` or `HealthCheckGlobalTimeout` without waiting for it. See [Clock](../container/options.md#clock).

:::caution
On a fake clock, `ctx.Err()` blocks until the context is done. In a `HealthCheck(ctx)` method, wait on `<-ctx.Done()` instead of polling `ctx.Err()`.
:::
