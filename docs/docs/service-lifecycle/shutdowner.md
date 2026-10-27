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

### Shutdown with a deadline {#shutdown-with-a-deadline}

`ShutdownWithContext()`, on the root or on a child scope, stops shutting services down once its context has ended. Every service it reaches after that:

- is **not** shut down: its `Shutdown` method is not called, whatever its signature;
- is reported, if it is a built service implementing a [Shutdowner interface](#shutdowner-interfaces): the report holds `ctx.Err()` (`context.Canceled` or `context.DeadlineExceeded`) under the service's name;
- is dropped: it is removed from its scope, so a later shutdown does not find it.

Any other service (never built, without a `Shutdown` method, an alias, a transient service or an export) has nothing to skip and is removed without an error. The `HookBeforeShutdown` and `HookAfterShutdown` hooks still run for every service reached, skipped or not; `HookAfterShutdown` receives the same error as the report (`nil` when none), so a hook that logs "shut down" should check it. The context can end before the call, during the shutdown (slow services used up the deadline), or while a child scope shutdown waits for a running start; in each case the shutdown proceeds with the ended context and skips the remaining services.

:::caution

Do not pass the context of `signal.NotifyContext` to `ShutdownWithContext()`. It is cancelled as soon as the signal arrives, so the shutdown would skip every service. Wait for the signal, then create a new context for the shutdown:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

// ... run the application until a signal arrives
<-ctx.Done()

// a fresh context, with a grace period for the shutdown
shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

report := injector.ShutdownWithContext(shutdownCtx)
if !report.Succeed {
	log.Println("shutdown error:", report.Error())
}
```

:::

`ShutdownOnSignalsWithContext()` uses its context only to stop waiting for a signal; the context neither bounds nor cancels the shutdown. Whichever comes first, a signal or the end of the context, it shuts every service down with a context that keeps the values of its context but has no cancellation or deadline, as `ShutdownOnSignals()` does, and returns the signal (`nil` when the context ended first). The `HookContainerClosing` and `HookContainerClosed` hooks receive that same context. So passing a `signal.NotifyContext` context to it is safe, and its shutdown is unbounded.

#### Defaults & control {#shutdown-with-a-deadline-control}

| Behavior                                                                      | Zero-config default                                                                                                                                                                                                    | Consumer control                                                                                                                                                                                                         |
| ----------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| A service reached after the shutdown context ended                            | If it is a built Shutdowner, its `Shutdown` is skipped and the report holds the context error under its name; any other service gets no error. Either way it is dropped, and the Before/After shutdown hooks still run | Pass a context that is still live with enough time, for example `context.WithTimeout(context.Background(), grace)` created after the signal; a context-aware shutdowner bounds its own work with the context it receives |
| `ShutdownOnSignalsWithContext`, after a signal or when its context ends first | Shuts every service down, with the context's values and no cancellation or deadline; the Closing and Closed hooks receive that context                                                                                 | To bound that shutdown, wait for the signal or the context yourself (`signal.NotifyContext`) and call `ShutdownWithContext` with your own deadline                                                                       |

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

doger.Provide(i, ...)
doger.Invoke[*MyService](i)

ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

report := i.ShutdownWithContext(ctx)
if !report.Succeed {
	log.Println("shutdown error:", report.Error())
}
```

**Play: https://go.dev/play/p/nkiBBYow2d5**
