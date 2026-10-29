# Spec Delta

## Purpose

Defines optional scheduled health rounds through the `HealthScheduler` interface. It says when the container calls a scheduler and when it stops it, what a round does, and how clones are scheduled. It also defines the hooks that report health state changes, so a consumer can keep reports fresh and raise alerts without a reader, using any scheduler (such as the gocron adapter).

In the scenarios, a "manual HealthScheduler" is a test double. Its `ScheduleHealthRounds` records its calls, stores `round`, and returns a stop function that records its calls. The test then runs the stored round itself.

## ADDED Requirements

### Requirement: Scheduling is off unless a HealthScheduler is set
With `InjectorOpts.HealthScheduler` nil (default), the container SHALL run no round and start no goroutine for health, and reports SHALL have `Monitored` false. Setting it SHALL be the only way to enable scheduled rounds.

#### Scenario: No scheduler, no rounds
```gherkin
Given an injector with HealthScheduler nil and an eager Healthchecker "db" that counts its calls
When the consumer calls Start, which succeeds, and waits 50ms of wall time
Then the health check of "db" was called 0 times
And HealthReport(IgnoreContainerPhase()) on the root scope has Monitored false
```

#### Scenario: Started containers without a scheduler leave no goroutine
```gherkin
Given a test package whose TestMain runs goleak.VerifyTestMain
And a test that creates an injector with HealthScheduler nil, calls Start, which succeeds, and reads HealthReport()
When the test ends without calling Shutdown
Then goleak reports no goroutine started by the container's health checks
```

<!-- cases: 1 default: this requirement -->
<!-- cases: 2 overrides: setting a scheduler is the next requirement -->
<!-- cases: 3 zero values: a nil scheduler -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: none -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: a started container without a scheduler -->
<!-- cases: 8 concurrency: goleak -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: a started container behaves as before for health: nothing runs in the background -->

### Requirement: The scheduler is called once when the container becomes ready
A root container with a `HealthScheduler` SHALL call `ScheduleHealthRounds(clock, round)` exactly once, with the injector clock, when it reaches `PhaseReady`, before the Ready hooks run. A container that never reaches `PhaseReady` SHALL NOT call it. While the scheduler is scheduling, reports SHALL have `Monitored` true.

#### Scenario: The scheduler is called only at Ready
```gherkin
Given an injector with a manual HealthScheduler, a fake clock C, and a Ready hook that records the scheduler's call count
When the consumer <action>
Then ScheduleHealthRounds was called <calls> times, with clock C when called
And the Ready hook saw <ready calls> calls
And HealthReport(IgnoreContainerPhase()) on the root scope has Monitored <monitored>

Examples:
  | action                                                                 | calls | ready calls | monitored |
  | calls New only                                                         | 0     | (not run)   | false     |
  | calls Start, which succeeds                                            | 1     | 1           | true      |
  | calls Start, which fails because a Starter returns errors.New("boom")  | 0     | (not run)   | false     |
  | calls Shutdown while Start is blocked in a Starter, then releases it   | 0     | (not run)   | false     |
  | calls Start, which succeeds, then Refresh twice                        | 1     | 1           | true      |
```

#### Scenario: A scheduler that fails leaves the container unmonitored
```gherkin
Given an injector whose HealthScheduler's ScheduleHealthRounds <fails>, with HookContainerPanic <panic hook> and a Logf that records lines
When the consumer calls Start
Then Start returns nil and Phase() is PhaseReady
And HealthReport(IgnoreContainerPhase()) on the root scope has Monitored false
And <observed>

Examples:
  | fails                                | panic hook                    | observed                                                         |
  | returns errors.New("bad cron")       | nil                           | Logf recorded a line containing "health scheduler" and "bad cron" |
  | panics with "boom"                   | nil                           | Logf recorded a line containing "health scheduler" and "boom"     |
  | panics with "boom"                   | set, recording (point, value) | HookContainerPanic recorded ("healthscheduler", "boom")           |
```

<!-- cases: 1 default: no scheduler (previous requirement) -->
<!-- cases: 2 overrides: a scheduler set -->
<!-- cases: N/A 3 zero values: covered by the previous requirement -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: 5 errors: an error or a panic from the scheduler does not fail Start -->
<!-- cases: 6 not found/duplicate: called once per container, even after Refresh; a second Start returns ErrAlreadyStarted as in container-start -->
<!-- cases: 7 lifecycle: idle, failed, shutdown during start, ready, refresh -->
<!-- cases: N/A 8 concurrency: the call happens on Start's goroutine, outside the lifecycle lock -->
<!-- cases: 9 panics: a scheduler panic is recovered; a panic raised by HookContainerPanic propagates from Start (next requirement) -->
<!-- cases: N/A 10 backward compatibility: new option -->

### Requirement: A stop returned with a scheduler error is called
When `ScheduleHealthRounds` returns an error together with a stop function, the container SHALL stay unmonitored and SHALL call that stop once, before Start returns, with `context.Background()`. Its error SHALL be logged, and its panic handled as for any stop. Shutdown SHALL NOT call it again.

#### Scenario: A stop returned with an error is called at once
```gherkin
Given an injector whose HealthScheduler's ScheduleHealthRounds returns a stop function that counts its calls, together with errors.New("partial"), and a Logf that records lines
When the consumer calls Start, then Shutdown
Then Start returns nil and HealthReport(IgnoreContainerPhase()) on the root scope has Monitored false
And Logf recorded a line containing "health scheduler" and "partial"
And the stop was called 1 time before Start returned, and 1 time in total after Shutdown
```

<!-- cases: N/A 1 default: a scheduler returns a nil stop with its error -->
<!-- cases: 2 overrides: a third-party HealthScheduler returning both -->
<!-- cases: N/A 3 zero values: a nil stop with an error is the previous requirement -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: 5 errors: the scheduler's error is logged; the stop's error is logged too -->
<!-- cases: 6 not found/duplicate: the stop is called once, not again by Shutdown -->
<!-- cases: 7 lifecycle: at Start, before it returns (verification 3, S2) -->
<!-- cases: N/A 8 concurrency: on Start's goroutine -->
<!-- cases: 9 panics: a panic of that stop goes to HookContainerPanic when set, logged otherwise -->
<!-- cases: N/A 10 backward compatibility: new behavior -->

### Requirement: A panic HookContainerPanic raises for the scheduler leaves scheduling stopped
A panic that `HookContainerPanic` raises for a panicking `ScheduleHealthRounds` SHALL propagate from Start, leaving the container unmonitored, and a later Shutdown SHALL return.

#### Scenario: A re-panicking HookContainerPanic leaves scheduling stopped
```gherkin
Given an injector whose HealthScheduler's ScheduleHealthRounds panics with "boom", and a HookContainerPanic that panics again with the value it receives
When the consumer calls Start under a deferred recover(), then Shutdown
Then the recovered value is "boom"
And HealthReport(IgnoreContainerPhase()) on the root scope has Monitored false
And Shutdown returns within 1s
```

<!-- cases: N/A 1 default: HookContainerPanic is unset by default -->
<!-- cases: 2 overrides: a HookContainerPanic that panics again -->
<!-- cases: N/A 3 zero values: a nil hook logs (previous requirement) -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: not applicable -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: Start panics, then Shutdown -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: 9 panics: this requirement (review R3) -->
<!-- cases: N/A 10 backward compatibility: new behavior -->

### Requirement: A round checks every eligible service once and returns
Each call of `round(ctx)` SHALL check each underlying health checker of the container once, joining checks already in flight, and SHALL return once every check is recorded or its bound elapsed. Services not started yet are excluded (health-reports). Each check is bounded by `HealthCheckTimeout` when positive, `DefaultHealthReportCheckTimeout` when 0, and nothing when negative, and by ctx. A round SHALL return at once after scheduling stopped.

#### Scenario: Round results
```gherkin
Given a started injector with a manual HealthScheduler, a fake clock, and HealthCheckTimeout <timeout>
And Healthcheckers root/"a" returning nil, "child"/"b" returning errors.New("down"), and root/"c" whose HealthCheck() blocks until released and ignores any context
When the test runs the round and the fake clock advances by <advance>
Then the round returns <returns>
And the report lists "a" HealthHealthy, "b" HealthUnhealthy, and "c" <c>

Examples:
  | timeout | advance | returns           | c                                              |
  | 0       | 10s     | after the advance | HealthUnhealthy matching ErrHealthCheckTimeout |
  | 0       | 9.999s  | not yet           | HealthUnknown with PendingSince set            |
  | 50ms    | 50ms    | after the advance | HealthUnhealthy matching ErrHealthCheckTimeout |
```

#### Scenario: A round checks each service once
```gherkin
Given a started injector with a manual HealthScheduler and a Healthchecker root/"db" with an alias "hc" (AsNamed), and "child"/"svc" exported to the root, all counting their calls
And a HealthCheck() call on the root scope is running the check of "db", blocking until released, and has already checked "svc" (exported to the root scope) once
When the test runs the round and the check of "db" is released
Then the round called the health check of "db" 0 times, as it joined the running check, and that of "svc" 1 time
And the health check of "db" was called 1 time in total
```

#### Scenario: A round after scheduling stopped does nothing
```gherkin
Given a started injector with a manual HealthScheduler and a Healthchecker "db" that counts its calls
And the container was shut down
When the test runs the stored round
Then it returns at once and "db" was not checked by it
```

<!-- cases: 1 default: the 10s report bound -->
<!-- cases: 2 overrides: a positive HealthCheckTimeout -->
<!-- cases: 3 zero values: HealthCheckTimeout 0 -->
<!-- cases: 4 boundaries: one millisecond before and at the bound -->
<!-- cases: 5 errors: a failing check, a timed-out check -->
<!-- cases: 6 not found/duplicate: aliases, exports and joined checks -->
<!-- cases: 7 lifecycle: a round after Shutdown -->
<!-- cases: 8 concurrency: joining a forced check in flight -->
<!-- cases: N/A 9 panics: specified in health-check-execution -->
<!-- cases: N/A 10 backward compatibility: new behavior -->

### Requirement: Scheduling stops when root shutdown begins
When root `Shutdown` begins, through the `RootScope` or the root's own `*Scope`, the container SHALL stop scheduling before the Closing hooks run and before any service shuts down. It SHALL cancel the context of every round in progress and record nothing more. It SHALL then call the stop function once, with a context that Shutdown's context cannot cancel, and wait for it to return. Shutdown SHALL NOT wait for checks.

#### Scenario: Stop happens before the Closing hooks
```gherkin
Given a started injector with a manual HealthScheduler and a Closing hook that records whether stop was already called
When the consumer calls <shutdown>
Then stop was called 1 time
And <closing>
And the context stop received was not done

Examples:
  | shutdown                                                  | closing                          |
  | Shutdown() on the RootScope                               | the Closing hook ran after stop  |
  | ShutdownWithContext with an already-cancelled context     | the Closing hook ran after stop  |
  | Shutdown() on the injector a provider received for "db"   | no Closing hook ran, as on this path for any container |
```

#### Scenario: A context-aware check in a round is cancelled before services shut down
```gherkin
Given a started injector with a manual HealthScheduler and HealthCheckTimeout -1ns
And a service "db" whose HealthCheck(ctx) blocks until ctx is done, then returns ctx.Err()
And a Shutdowner "svc" whose Shutdown records whether the check's context was already done
And the test is running the round, so the check of "db" is in flight
When the consumer calls Shutdown
Then "svc" recorded that the check's context was done
And the round returned
```

#### Scenario: Shutdown does not wait for a check that ignores its context
```gherkin
Given a started injector with a manual HealthScheduler and HealthCheckTimeout -1ns
And a service "db" whose HealthCheck() blocks until released and ignores any context
And the test is running the round, so the check of "db" is in flight
When the consumer calls Shutdown
Then Shutdown returns within 1s
And the check of "db" is released before the test ends
```

<!-- cases: N/A 1 default: no scheduler means nothing to stop -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: N/A 3 zero values: a container that never reached Ready has no stop to call -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: 5 errors: an error returned by stop is logged with Logf and does not fail Shutdown -->
<!-- cases: 6 not found/duplicate: a second Shutdown returns the first report as in container-start; stop is called once -->
<!-- cases: 7 lifecycle: order against Closing hooks and service shutdown; the root's own *Scope path, which runs no Closing hook; an ended Shutdown context (shutdown-context) -->
<!-- cases: 8 concurrency: cancellation of a round in progress -->
<!-- cases: N/A 9 panics: a panic in stop is the next requirement -->
<!-- cases: N/A 10 backward compatibility: Shutdown without a scheduler is unchanged -->

### Requirement: Shutdown cancels the checks in flight with a container-shut-down cause
When root Shutdown begins, the container SHALL cancel the context of every check in flight with the cause `ErrStartAfterShutdown`, without waiting for the checks. A caller still waiting for such a check SHALL get an error matching `ErrHealthCheckTimeout` and `context.Canceled`.

#### Scenario: Callers of a check cancelled by Shutdown
```gherkin
Given an injector that is never started, with a service "db" whose HealthCheck(ctx) blocks until ctx is done, then records context.Cause(ctx) and returns ctx.Err()
And a HealthCheck() call on the root scope is waiting for that check
When the consumer calls Shutdown
Then the HealthCheck() call returns "DI: health check timeout: context canceled" for "db"
And the check recorded a cause matching ErrStartAfterShutdown
```

<!-- cases: N/A 1 default: no option -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: 5 errors: the caller's timeout error and the check's cause (verification 3, S1) -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: an injector never started, shut down with a forced check in flight -->
<!-- cases: 8 concurrency: the check is cancelled, not waited for -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: the cause's message is "DI: container is shut down" -->

### Requirement: Shutdown waits for a scheduler call in progress
When `ScheduleHealthRounds` is still running as root Shutdown begins, Shutdown SHALL wait for it to return, also past the end of Shutdown's context, then call the stop function it returned, so no goroutine of the schedule outlives Shutdown.

#### Scenario: Shutdown waits for a scheduler call in progress
```gherkin
Given an injector whose HealthScheduler's ScheduleHealthRounds blocks until released, then returns a stop function that counts its calls
And the consumer called Start, which is blocked in ScheduleHealthRounds with the container in PhaseReady
When the consumer calls ShutdownWithContext with a context whose deadline is 50ms ahead, and 1 minute passes
Then ShutdownWithContext has not returned
And once ScheduleHealthRounds is released, ShutdownWithContext and Start return, and the stop was called 1 time
```

<!-- cases: N/A 1 default: no scheduler means no call to wait for -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: 4 boundaries: Shutdown's 50ms deadline passes while the call is in progress -->
<!-- cases: N/A 5 errors: not applicable -->
<!-- cases: N/A 6 not found/duplicate: the stop is called once -->
<!-- cases: 7 lifecycle: Start blocked in the scheduler at PhaseReady; unlike a Starter, which Shutdown's deadline bounds (verification 3, S3) -->
<!-- cases: 8 concurrency: Shutdown and Start on different goroutines -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new behavior -->

### Requirement: A shutdown through the root's own *Scope ends health recording
A Shutdown through the root's own `*Scope` leaves the container phase unchanged, yet after it the container SHALL record no health result and call no health hook again, whichever path runs a check.

#### Scenario: A shutdown through the root's own *Scope ends health recording
```gherkin
Given an injector that is never started, with a HookServiceHealthChanged hook that records calls
And the consumer called Shutdown() on the injector a provider received
When a Healthchecker value "db" returning nil is registered and the consumer calls <calls>
Then the report's entry for "db" is HealthUnknown and its Phase is PhaseIdle
And the hook recorded nothing

Examples:
  | calls                                                   |
  | HealthReport() on the root scope                        |
  | HealthCheck() on the root scope, then HealthReport()    |
```

<!-- cases: 1 default: zero configuration -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: none -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: a service registered after that shutdown, read and force-checked (W4) -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: that path's shutdown otherwise behaves as before (container-start) -->

### Requirement: A failing stop does not fail Shutdown
An error returned by the stop function SHALL be logged with Logf. A panic in it SHALL be recovered and passed to `HookContainerPanic(root, "healthscheduler", value)` when that option is set, and logged otherwise. Neither SHALL fail Shutdown.

#### Scenario: A failing stop does not fail Shutdown
```gherkin
Given a started injector whose HealthScheduler's stop function <fails>, with HookContainerPanic <panic hook> and a Logf that records lines
When the consumer calls Shutdown
Then the report succeeds
And <observed>

Examples:
  | fails                          | panic hook                    | observed                                                         |
  | returns errors.New("busy")     | nil                           | Logf recorded a line containing "health scheduler" and "busy"     |
  | panics with "stop boom"        | nil                           | Logf recorded a line containing "health scheduler" and "stop boom" |
  | panics with "stop boom"        | set, recording (point, value) | HookContainerPanic recorded ("healthscheduler", "stop boom")      |
```

<!-- cases: N/A 1 default: a stop that returns nil -->
<!-- cases: 2 overrides: with and without HookContainerPanic -->
<!-- cases: N/A 3 zero values: a nil error is not logged -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: 5 errors: an error is logged -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: root Shutdown -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: 9 panics: this requirement (review R8) -->
<!-- cases: N/A 10 backward compatibility: new behavior -->

### Requirement: A clone is scheduled independently
A clone SHALL be scheduled only when the clone itself reaches `PhaseReady`, through its own `ScheduleHealthRounds` call, and SHALL NOT share health results with its source. Its own Shutdown SHALL stop only its own schedule. `Clone()` SHALL copy the scheduler and the health hooks with the other options, and `CloneWithOpts(opts)` SHALL use those of `opts`.

#### Scenario: Clone scheduling follows the clone's lifecycle
```gherkin
Given a started injector "source" with a manual HealthScheduler S and a Healthchecker value "db"
When the consumer calls <clone>, <action>
Then S.ScheduleHealthRounds was called <calls> times in total
And the clone's HealthReport(IgnoreContainerPhase()) has Monitored <monitored>

Examples:
  | clone                          | action                       | calls | monitored |
  | Clone()                        | does not start the clone     | 1     | false     |
  | Clone()                        | starts the clone             | 2     | true      |
  | CloneWithOpts(&InjectorOpts{}) | starts the clone             | 1     | false     |
```

#### Scenario: The source keeps scheduling after its clone shuts down
```gherkin
Given a started injector "source" with a manual HealthScheduler S and a started Clone() of it
When the consumer shuts the clone down
Then the stop function of the clone's schedule was called 1 time and the source's 0 times
And the source's report has Monitored true
```

#### Scenario: Clone keeps the health hooks
```gherkin
Given an injector that is never started, with a Healthchecker value "db", one HookServiceHealthChanged hook and one HookContainerHealthChanged hook, each counting its calls and recording the scope or root it received
When the consumer calls Clone(), then HealthCheck() on the clone
Then the service hook was called 1 time, with a scope of the clone
And the container hook was called 1 time, with the clone
```

<!-- cases: N/A 1 default: same defaults as any container -->
<!-- cases: 2 overrides: Clone copies the scheduler and the health hooks (verification 3, W5), CloneWithOpts takes the given ones -->
<!-- cases: N/A 3 zero values: CloneWithOpts with no scheduler -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: none -->
<!-- cases: N/A 6 not found/duplicate: one call per container -->
<!-- cases: 7 lifecycle: the clone starts idle, as in container-start -->
<!-- cases: N/A 8 concurrency: no shared state between clone and source -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: N/A 10 backward compatibility: clones that never start are unchanged -->

### Requirement: Service health changes call the service hooks in order
Each `HookServiceHealthChanged(scope, serviceName, previous, current ServiceHealth)` SHALL be called once per change of a service's recorded `State`, including from `HealthUnknown` to its first result, whichever path recorded it. Calls SHALL be serialized, in recording order. A new error with an unchanged state SHALL NOT call it. `AddServiceHealthChangedHook` SHALL append to the same list.

#### Scenario: Service hook calls follow state changes
```gherkin
Given a started injector with a manual HealthScheduler
And a Healthchecker "db" whose results, round after round, are <results>
And a HookServiceHealthChanged hook that records (previous state, current state, current error)
When the test runs the round <rounds> times
Then the hook recorded <calls>

Examples:
  | results                          | rounds | calls                                                                             |
  | nil, nil, nil                    | 3      | (Unknown, Healthy, nil)                                                           |
  | nil, errors.New("down"), nil     | 3      | (Unknown, Healthy, nil), (Healthy, Unhealthy, "down"), (Unhealthy, Healthy, nil)  |
  | errors.New("a"), errors.New("b") | 2      | (Unknown, Unhealthy, "a")                                                         |
```

#### Scenario: A forced run records changes too
```gherkin
Given an injector that is never started, with a Healthchecker "db" whose recorded result is nil
And a HookServiceHealthChanged hook that records calls
And "db" now returns errors.New("down")
When the consumer calls HealthCheck() on the root scope
Then within 1s the hook recorded (Healthy, Unhealthy, "down")
```

#### Scenario: Hooks run one at a time, in order of addition
```gherkin
Given a started injector with a manual HealthScheduler and hooks A and B added in that order with AddServiceHealthChangedHook
And 10 Healthchecker services whose first results arrive concurrently
When the test runs the round and the hooks have run
Then the hooks were called 20 times in total, never two at once
And for each service, A was called before B
```

#### Scenario: A hook may call health APIs
```gherkin
Given an injector that is never started, with a Healthchecker "db" that returns nil, then errors.New("down")
And a HookServiceHealthChanged hook that calls <call> and records its result
When the consumer calls HealthCheck() on the root scope twice
Then each call returns within 1s
And the hook's own call returned within 1s

Examples:
  | call                                         |
  | HealthCheckNamed(injector, "db")             |
  | HealthCheck() on the root scope              |
  | HealthReport() on the root scope             |
```

#### Scenario: Nil hooks are skipped
```gherkin
Given an injector that is never started, with a Healthchecker "db" returning nil, a HookContainerPanic and a Logf that record their calls
And HookServiceHealthChanged holding nil and a hook R that counts its calls, HookContainerHealthChanged holding nil
And AddServiceHealthChangedHook(nil) and AddContainerHealthChangedHook(nil) were called
When the consumer calls HealthCheck() on the root scope and the hooks have run
Then R was called 1 time
And HookContainerPanic was not called and Logf recorded no hook panic
```

<!-- cases: 1 default: no hook registered is the default; nothing is called -->
<!-- cases: 2 overrides: the InjectorOpts field and the Add method share one list -->
<!-- cases: 3 zero values: a nil hook in the list is skipped, in the field and through the Add methods (verification 3, W5) -->
<!-- cases: 4 boundaries: the first result counts as a change; an unchanged state does not -->
<!-- cases: N/A 5 errors: hooks return nothing -->
<!-- cases: 6 not found/duplicate: a removed service calls no service hook -->
<!-- cases: 7 lifecycle: round and forced paths; re-entrant calls from a hook -->
<!-- cases: 8 concurrency: serialized calls under concurrent results; a result reaches its callers without waiting for hooks -->
<!-- cases: 9 panics: the hook-panic requirement below -->
<!-- cases: N/A 10 backward compatibility: new hook; it takes no ctx, like HookContainerPanic, because it is delivered off any caller's request -->

### Requirement: Overall health changes call the container hooks
Each `HookContainerHealthChanged(root, previous, current HealthState)` SHALL be called once for each change of the overall state of the container's unfiltered report computed without the phase gate, whether a recorded result or a service appearing or leaving caused it, after the service hooks of the same result. When a service appearing or leaving changes it is the next requirement. `AddContainerHealthChangedHook` SHALL append to the same list.

#### Scenario: Container hook calls follow the overall state
```gherkin
Given a started injector with a manual HealthScheduler
And Healthcheckers "a" and "b" whose results per round are <results>
And a HookContainerHealthChanged hook that records (previous, current)
When the test runs the round twice
Then the hook recorded <calls>

Examples:
  | results                                                                               | calls                                    |
  | round 1: a nil, b nil; round 2: a nil, b nil                                          | (Unknown, Healthy)                       |
  | round 1: a nil, b nil; round 2: a nil, b "down"                                       | (Unknown, Healthy), (Healthy, Unhealthy) |
  | round 1: a "x", b nil; round 2: a "y", b nil                                          | (Unknown, Unhealthy)                     |
  | round 1: a nil, b nil; round 2: a nil, b "down", with b registered AsOptionalHealth() | (Unknown, Healthy), (Healthy, Degraded)  |
```

#### Scenario: A service leaving changes the overall state
```gherkin
Given a started injector with a manual HealthScheduler, a Healthchecker root/"db" returning nil and a Healthchecker "child"/"bad" returning errors.New("down")
And a HookContainerHealthChanged hook that records (previous, current)
And the test ran the round, so the overall state is Unhealthy
When scope "child" is shut down
Then within 1s the hook's last recorded call is (Unhealthy, Healthy)
```

#### Scenario: Container hooks follow the service hooks of the same result
```gherkin
Given an injector that is never started, with a Healthchecker "db" returning nil
And a HookServiceHealthChanged hook and a HookContainerHealthChanged hook appending to one shared list "service:<name>" and "container:<current state>"
When the consumer calls HealthCheck() on the root scope and the hooks have run
Then the list is "service:db", "container:healthy"
```

<!-- cases: 1 default: no hook registered -->
<!-- cases: 2 overrides: field and Add method -->
<!-- cases: 3 zero values: a nil hook is skipped -->
<!-- cases: 4 boundaries: the first overall result counts as a change, delivered after the service hooks of that result (verification 3, W5) -->
<!-- cases: N/A 5 errors: hooks return nothing -->
<!-- cases: 6 not found/duplicate: a removed service changes the overall state (timing: next requirement) -->
<!-- cases: 7 lifecycle: rounds after Ready; a child scope shutdown; the phase gate is a read-time view and never fires this hook -->
<!-- cases: 8 concurrency: the strict Unknown rule of health-reports makes each row's order independent of which result lands first -->
<!-- cases: 9 panics: the hook-panic requirement below -->
<!-- cases: N/A 10 backward compatibility: new hook -->

### Requirement: A service leaving changes the overall state at once, one appearing at the next listing
A service leaving the container SHALL change the overall state of the container hooks at once. A service appearing SHALL change it when the next round or root report lists it, where it counts as unknown until its first result, as in the report, or when its first result is recorded.

#### Scenario: A service appearing changes the overall state at the next round
```gherkin
Given a started injector with a manual HealthScheduler, a Healthchecker root/"a" returning nil, and a HookContainerHealthChanged hook that records (previous, current)
And the test ran the round, so the hook recorded (Unknown, Healthy)
When a Healthchecker value root/"b" returning <result> is registered
Then the hook recorded nothing more before the next round
And after the test runs the round, the hook's calls since "b" appeared are <calls>

Examples:
  | result             | calls                                        |
  | nil                | (Healthy, Unknown), (Unknown, Healthy)       |
  | errors.New("down") | (Healthy, Unknown), (Unknown, Unhealthy)     |
```

<!-- cases: N/A 1 default: no hook registered -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: 4 boundaries: before and after the next round -->
<!-- cases: 5 errors: an appearing service that fails -->
<!-- cases: 6 not found/duplicate: a registration after the first round -->
<!-- cases: 7 lifecycle: rounds after Ready; leaving at once is TestHookContainerHealthChanged_Membership -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: as alexliesenfeld/health, .NET and Spring Boot, aggregate health is evaluated when checks run or are read, never on registration -->

### Requirement: A panicking health hook does not stop health recording
A panic in a `HookServiceHealthChanged` or `HookContainerHealthChanged` hook SHALL be recovered. It SHALL be passed to `HookContainerPanic(root, "healthchanged", value)` when that option is set, and logged with `Logf` otherwise; a panic raised by `HookContainerPanic` SHALL propagate. The remaining hooks for the same change SHALL still be called, and later results SHALL still be recorded.

#### Scenario: Hook panic is contained
```gherkin
Given a started injector with a manual HealthScheduler and HookContainerPanic <panic hook>
And service hooks A, which panics "boom", and B, added in that order
And a Healthchecker "db" that returns nil then errors.New("down")
When the test runs the round twice
Then B was called 2 times
And <observed>

Examples:
  | panic hook                    | observed                                                      |
  | set, recording (point, value) | HookContainerPanic recorded ("healthchanged", "boom") 2 times |
  | nil                           | Logf received 2 lines containing "boom"                       |
```

<!-- cases: N/A 1 default: panics are not a default path -->
<!-- cases: 2 overrides: with and without HookContainerPanic -->
<!-- cases: N/A 3 zero values: a nil panic value is reported as runtime.PanicNilError, as for checks -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: hooks return nothing -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: the next round still records -->
<!-- cases: N/A 8 concurrency: hooks are serialized -->
<!-- cases: 9 panics: this requirement; a nil HookContainerPanic logs instead of re-raising as in consolidate-pending-lifecycle-work, because no caller is there to receive the panic (flagged) -->
<!-- cases: N/A 10 backward compatibility: new hook -->
