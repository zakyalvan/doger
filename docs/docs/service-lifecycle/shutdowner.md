---
title: Services shutdown
description: Graceful service shutdown
sidebar_position: 2
---

# Graceful service shutdown

If your service maintains state such as a connection pool, a buffer, etc., you might want to close or flush it gracefully when the application exits.

When the `doger.Shutdown[type]()` or the `injector.Shutdown()` function is called, the framework triggers the `Shutdown` method of each service implementing the `doger.Shutdowner` interface, in reverse invocation order.

🛑 Services can be shut down properly, in reverse initialization order. Requesting a shutdown on a scope also shuts down its children recursively.

## Trigger shutdown {#trigger-shutdown}

A shutdown can be triggered on a root scope:

```go
// on demand
injector.Shutdown() *doger.ShutdownReport
injector.ShutdownWithContext(context.Context) *doger.ShutdownReport

// on signal (helper methods on the root scope)
injector.ShutdownOnSignals(...os.Signal) (os.Signal, *doger.ShutdownReport)
injector.ShutdownOnSignalsWithContext(context.Context, ...os.Signal) (os.Signal, *doger.ShutdownReport)
```

...on a single service:

```go
// returns error on failure
doger.Shutdown[T any](doger.Injector) error
doger.ShutdownWithContext[T any](context.Context, doger.Injector) error
doger.ShutdownNamed(doger.Injector, string) error
doger.ShutdownNamedWithContext(context.Context, doger.Injector, string) error

// panics on failure
doger.MustShutdown[T any](doger.Injector)
doger.MustShutdownWithContext[T any](context.Context, doger.Injector)
doger.MustShutdownNamed(doger.Injector, string)
doger.MustShutdownNamedWithContext(context.Context, doger.Injector, string)
```

:::info

If no signal is passed to `injector.ShutdownOnSignals(...)`, both `syscall.SIGTERM` and `os.Interrupt` are handled by default.

:::

### Root shutdown runs once {#root-shutdown-runs-once}

`Shutdown()` and `ShutdownWithContext()` on the root scope shut services down exactly once per container:

- The first caller does the work. Concurrent and later callers wait for it and return a **copy** of its report, so they see the same errors. Changing a copy affects no other caller.
- A caller that arrives after the shutdown has completed always gets the first report, even when its own context has already ended.
- A caller still waiting when its own context ends returns a failed report with a single error, keyed by the root scope with an empty service name, wrapping `ctx.Err()`. The first caller's shutdown keeps running.
- If the first caller panics before producing a report (for example in a custom `Logf`), the panic reaches that caller and every other caller gets a failed report containing `DI: root shutdown did not complete`.
- A `nil` context is treated as `context.Background()`.
- Services registered on the root after the first shutdown are not shut down by a later root `Shutdown()`.

Child scopes follow a similar rule while a shutdown of them runs: a concurrent `scope.Shutdown()`, or root `Shutdown()` reaching that scope, waits for the running shutdown (bounded by its context) and gets a copy of its report. Once that shutdown has finished, a later `scope.Shutdown()` runs again.

:::caution

Only the root scope methods follow this rule. `Shutdown()` on a child scope, and a shutdown reached through the root's `*Scope` (for example the injector a provider receives), run the per-scope shutdown directly, as before.

:::

## Shutdowner interfaces {#shutdowner-interfaces}

Your service can implement one of the following signatures:

```go
type Shutdowner interface {
	Shutdown()
}

type ShutdownerWithError interface {
	Shutdown() error
}

type ShutdownerWithContext interface {
	Shutdown(context.Context)
}

type ShutdownerWithContextAndError interface {
	Shutdown(context.Context) error
}
```

### Example {#example}

```go
// Ensure at compile-time MyService implements doger.ShutdownerWithContextAndError
var _ doger.ShutdownerWithContextAndError = (*MyService)(nil)

type MyService struct {}

func (*MyService) Shutdown(context.Context) error {
    // ...
    return nil
}

i := doger.New()

Provide(i, ...)
Invoke(i, ...)

ctx := context.WithTimeout(10 * time.Second)
errors := i.ShutdownWithContext(ctx)
if err != nil {
	log.Println("shutdown error:", err)
}
```

**Play: https://go.dev/play/p/nkiBBYow2d5**
