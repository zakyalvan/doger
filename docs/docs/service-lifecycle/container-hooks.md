---
title: Container hooks
description: Run container-wide logic when the root container starts, becomes ready, fails to start and shuts down
sidebar_position: 5
---

# Container hooks

Container hooks run container-wide logic once per root container: when it starts, when it is ready, when its start fails, and around its shutdown. They complement the per-service hooks (`HookBeforeStart`, `HookAfterShutdown`…), which run once per service.

| Hook                    | Signature                                  | Fires                                      |
| ----------------------- | ------------------------------------------ | ------------------------------------------ |
| `HookContainerStarting` | `func(context.Context, *RootScope) error`  | first `Start`, before anything is built    |
| `HookContainerStarted`  | `func(context.Context, *RootScope)`        | first `Start`, once its pipeline succeeded |
| `HookContainerReady`    | `func(context.Context, *RootScope)`        | first `Start`, after every Started hook    |
| `HookContainerFailed`   | `func(context.Context, *RootScope, error)` | first `Start`, when it fails               |

Each hook list is a field of `doger.InjectorOpts`, and has a matching `RootScope.AddContainer<State>Hook` method.

## Phases {#phases}

A root container has a start phase and a close phase. Each transition happens at most once, and the hooks fire at the transitions:

```mermaid
stateDiagram-v2
    direction LR
    state "start phase" as start {
        idle --> starting: Start (Starting hooks)
        starting --> started: pipeline succeeded
        started --> ready
        starting --> failed: pipeline failed
    }
    state "close phase" as close {
        open --> closing: Shutdown
        closing --> closed
    }
```

The two phases are independent: a container can shut down without ever starting, or while it is starting.

## Starting {#starting}

`HookContainerStarting` hooks run on the goroutine that calls the first `Start`, after its one-shot guard and **before** its context check, discovery or any instantiation. They receive the Start-time context, which keeps the values of the context passed to `StartWithContext` and is cancelled by a root `Shutdown`.

A Starting hook can **veto** `Start` by returning an error: the remaining Starting hooks do not run, nothing is built or started, and `Start` returns an error wrapping the hook's error.

```go
root := doger.New()

root.AddContainerStartingHook(func(ctx context.Context, root *doger.RootScope) error {
    if os.Getenv("DB_URL") == "" {
        return errors.New("missing DB_URL") // fail fast: nothing is built
    }
    return nil
})

err := root.Start()
// err: DI: container start aborted: missing DB_URL
```

Because Starting hooks run before the context check, they run even when the context passed to `StartWithContext` is already cancelled. `Start` then fails with an error matching `context.Canceled`.

## Started and Ready {#started-and-ready}

When the start pipeline succeeds, `Start` runs every `HookContainerStarted` hook, then every `HookContainerReady` hook, on the goroutine that called `Start`, and then returns nil. Both receive the context passed to `StartWithContext`.

- **Started** means "the pipeline succeeded". Use it for last wiring, such as registering routes or warming caches.
- **Ready** means "every Started hook has returned". Use it to signal readiness to an orchestrator.

Started fires **after** the runnables: the pipeline builds every candidate, starts the Starters, then runs the `StageAfterStart` handlers, including the built-in `Runnable` handler, and only then fires Started. For a Starter "server" and a Runnable "migrate", the order is:

```text
starting-hook, server.Start, migrate.Run, started-hook, ready-hook
```

Started and Ready also fire for a container with no services.

```go
root.AddContainerReadyHook(func(ctx context.Context, root *doger.RootScope) {
    readiness.Store(true) // the readiness probe now reports healthy
})
```
