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
<!-- cases: 9 panics: a scheduler panic is recovered; a panic raised by HookContainerPanic propagates from Start -->
<!-- cases: N/A 10 backward compatibility: new option -->

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
And a HealthCheck() call on the root scope is running the check of "db", blocking until released
When the test runs the round and the check of "db" is released
Then the health check of "db" was called 1 time and that of "svc" 1 time
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
Then stop was called 1 time, before the Closing hook ran
And the context stop received was not done

Examples:
  | shutdown                                                  |
  | Shutdown() on the RootScope                               |
  | ShutdownWithContext with an already-cancelled context     |
  | Shutdown() on the injector a provider received for "db"   |
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
<!-- cases: 7 lifecycle: order against Closing hooks and service shutdown; the root's own *Scope path; an ended Shutdown context (shutdown-context) -->
<!-- cases: 8 concurrency: cancellation of a round in progress -->
<!-- cases: N/A 9 panics: a panic in stop is recovered like a scheduler panic -->
<!-- cases: N/A 10 backward compatibility: Shutdown without a scheduler is unchanged -->

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

<!-- cases: N/A 1 default: same defaults as any container -->
<!-- cases: 2 overrides: Clone copies the scheduler, CloneWithOpts takes the given one -->
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

<!-- cases: 1 default: no hook registered is the default; nothing is called -->
<!-- cases: 2 overrides: the InjectorOpts field and the Add method share one list -->
<!-- cases: 3 zero values: a nil hook in the list is skipped -->
<!-- cases: 4 boundaries: the first result counts as a change; an unchanged state does not -->
<!-- cases: N/A 5 errors: hooks return nothing -->
<!-- cases: 6 not found/duplicate: a removed service calls no service hook -->
<!-- cases: 7 lifecycle: round and forced paths; re-entrant calls from a hook -->
<!-- cases: 8 concurrency: serialized calls under concurrent results; a result reaches its callers without waiting for hooks -->
<!-- cases: 9 panics: the hook-panic requirement below -->
<!-- cases: N/A 10 backward compatibility: new hook; it takes no ctx, like HookContainerPanic, because it is delivered off any caller's request -->

### Requirement: Overall health changes call the container hooks
Each `HookContainerHealthChanged(root, previous, current HealthState)` SHALL be called once for each change of the overall state of the container's unfiltered report computed without the phase gate, whether a recorded result or a service appearing or leaving caused it, after the service hooks of the same result. `AddContainerHealthChangedHook` SHALL append to the same list.

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

<!-- cases: 1 default: no hook registered -->
<!-- cases: 2 overrides: field and Add method -->
<!-- cases: 3 zero values: a nil hook is skipped -->
<!-- cases: 4 boundaries: the first overall result counts as a change -->
<!-- cases: N/A 5 errors: hooks return nothing -->
<!-- cases: 6 not found/duplicate: a removed service changes the overall state -->
<!-- cases: 7 lifecycle: rounds after Ready; a child scope shutdown; the phase gate is a read-time view and never fires this hook -->
<!-- cases: 8 concurrency: the strict Unknown rule of health-reports makes each row's order independent of which result lands first -->
<!-- cases: 9 panics: the hook-panic requirement below -->
<!-- cases: N/A 10 backward compatibility: new hook -->

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
