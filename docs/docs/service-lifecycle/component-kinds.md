---
title: Component kinds
description: Introduce your own component types and handle them at container start
sidebar_position: 4
---

# Component kinds

A component kind lets you introduce a new type of component, for example "scheduled job", whose services a dedicated handler receives when the container [starts](./starter.md). The handler can wire them into another service, such as a scheduler.

## Creating a kind {#new-kind}

```go
// T: the interface every component satisfies. M: per-registration metadata.
var Scheduled = doger.NewKind[Job, Schedule]("scheduled")

type Job interface{ Run(ctx context.Context) error }
type Schedule struct{ Every time.Duration }
```

The name only appears in errors: two kinds created with the same name are still distinct.

## Opting a service in {#opt-in}

```go
doger.Provide(injector, NewCleanupJob, Scheduled.With(Schedule{Every: time.Hour}))
```

Registration panics when:

- the registered type is a concrete type not assignable to `T` (`DI: service ... is not assignable to ... required by kind ...`);
- the service is transient: a factory has no single instance to handle;
- the same kind is applied twice to one registration.

A service registered under an **interface type** not assignable to `T` is checked on its instance when `Start` runs; a mismatch fails `Start` with `DI: service ... does not implement ... required by kind ...`.

Opted-in services are built by `Start`, whether or not their kind has a handler.

## Handling a kind {#handle}

Register one handler per kind on the root container:

```go
Scheduled.Handle(root, doger.StageBeforeStart, func(ctx context.Context, c doger.Component[Job, Schedule]) error {
	cron.Add(c.Name, c.Meta.Every, c.Instance)
	// stop the cron before the jobs it drives
	return doger.DeclareDependency(c.Scope, "cron", c.Name)
})
```

`Component` carries the registering `Scope`, the service `Name`, the `Instance` typed `T` and the `Meta` typed `M`. For a service of the root scope, `Scope` is the root's `*Scope`: a shutdown reached through it bypasses the [root lifecycle](./starter.md#shutdown-during-start).

### Stages {#stages}

| Stage | Runs |
|---|---|
| `doger.StageBeforeStart` | after every candidate is built, before any starter starts |
| `doger.StageAfterStart` | after every starter has started |

Edges declared with `DeclareDependency` during `StageBeforeStart` order the starters of the same `Start`; edges from both stages order shutdown.

### Rules {#rules}

- A later `Handle` for the same kind **replaces** both the handler and the stage.
- A `nil` root means `doger.DefaultRootScope`.
- Handlers of one stage run kind by kind, in the order kinds were first given a handler. Within a kind, components arrive in dependency order, then scope depth, then registration order.
- The first handler error, or a recovered handler panic, stops `Start` with an error naming the kind and the service and wrapping the cause.
- The handler receives the Start-time context, cancelled when `Start` returns or when `Shutdown` begins.
- `Handle` panics on a zero `Kind` (not created with `NewKind`), an unknown stage, or a `nil` handler.
- Clones get only the default handlers; re-register custom handlers on a clone.
