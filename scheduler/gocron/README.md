# dogergocron

`github.com/kartaladev/doger/scheduler/gocron` (package `dogergocron`) schedules the health rounds of a [doger](https://github.com/kartaladev/doger) container with [go-co-op/gocron v2](https://github.com/go-co-op/gocron). It implements `doger.HealthScheduler`.

```sh
go get github.com/kartaladev/doger/scheduler/gocron@latest
```

```go
scheduler, err := dogergocron.New()
if err != nil {
    return err
}
injector := doger.NewWithOpts(&doger.InjectorOpts{HealthScheduler: scheduler})
```

The container calls the scheduler once it reaches `PhaseReady` and stops it when root `Shutdown` begins. No goroutine runs before `Start`, and none remains after `Shutdown`.

## Defaults and overrides

| Default | Override |
|---|---|
| A duration job every 30s (`DefaultInterval`) | `WithInterval(d)`, or any job definition with `WithJobDefinition(def)`, such as `gocron.CronJob("*/5 * * * *", false)`; a job definition wins over an interval |
| The first round at once | `WithStartAt(opt)`, such as `gocron.WithStartDateTime(t)` |
| The container's clock | `WithScheduler(s)` uses `s`'s own clock |
| Singleton mode with `gocron.LimitModeReschedule`: a tick that finds a round still running is skipped | `WithJobOptions(opts...)`, applied after the defaults |
| One gocron scheduler per container, owned by the adapter | `WithScheduler(s)`: the job is added to `s`, which the application starts and shuts down; stopping removes only that job |

`WithSchedulerOptions(opts...)` passes options such as `gocron.WithLogger` or `gocron.WithMonitor` to the adapter's own gocron scheduler.

`New` returns an error matching `ErrInvalidOption` for a non-positive interval or a nil job definition, start-at option or scheduler.

## Caveats

- **Do not use gocron's distributed elector or locker for this job.** Health is per instance: an elector or a locker would run the round on one instance only, and the others would keep stale reports.
- Start-at options given through `WithJobOptions` combine with the default immediate start, because gocron combines them; use `WithStartAt` to change when the first round runs.
- A round is bounded by `HealthCheckTimeout`, or `doger.DefaultHealthReportCheckTimeout` (10s) when it is 0. Keep the interval above that bound: singleton mode skips the ticks that find a round still running.

## Versioning

This module is a nested module of the repository, like the HTTP adapters: each release of the root module also tags it, at the same version, with the module path prefix `scheduler/gocron/vX.Y.Z`, and pins it to require the root module at that version. `go get github.com/kartaladev/doger/scheduler/gocron@vX.Y.Z` resolves that tag.
