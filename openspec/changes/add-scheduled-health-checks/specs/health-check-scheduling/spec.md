# Spec Delta

## Purpose

Defines the container's background health-check scheduler: when it starts and stops, how often it runs a round, which services a round checks, how long a scheduled check may run, and the hooks that report health state changes, so that the cost of health checks does not grow with the number of callers.

## ADDED Requirements

### Requirement: Scheduling starts when the container becomes ready
A root container SHALL start one health-check scheduler when it reaches `PhaseReady`, and SHALL run the first round immediately. A container that never reaches `PhaseReady` (never started, Start failed, or Shutdown during Start) SHALL NOT start a scheduler or any other goroutine for health checks. `New()` and `NewWithOpts()` SHALL NOT start a goroutine.

#### Scenario: Scheduler starts only at Ready
```gherkin
Given an injector whose Clock is a fake clock and HealthCheckInterval is <interval>
And an eager service "db" whose health check counts its calls and returns nil
When the consumer <action>
Then the health check of "db" was called <calls> times without advancing the fake clock
And the number of goroutines the container started for health checks is <goroutines>

Examples:
  | interval      | action                                                   | calls | goroutines |
  | 0 (default)   | calls New only                                           | 0     | 0          |
  | 0 (default)   | calls Start, which succeeds                              | 1     | 1          |
  | 0 (default)   | calls Start, which fails because a Starter returns errors.New("boom") | 0 | 0  |
  | 0 (default)   | calls Shutdown while Start is blocked in a Starter       | 0     | 0          |
  | -1ns          | calls Start, which succeeds                              | 0     | 0          |
  | 0 (default)   | uses DefaultRootScope without calling Start              | 0     | 0          |
```

#### Scenario: A container that is never started passes goleak
```gherkin
Given a test package whose TestMain runs goleak.VerifyTestMain
And a test that calls New(), registers a Healthchecker "db" and calls HealthCheck()
When the test ends without calling Shutdown
Then goleak reports no leaked goroutine
```

<!-- cases: 1 default: the "0 (default)" rows -->
<!-- cases: 2 overrides: the negative interval row; positive intervals are the next requirement -->
<!-- cases: 3 zero values: interval 0 means the default; a nil opts is New() -->
<!-- cases: N/A 4 boundaries: the interval boundaries are in the next requirement -->
<!-- cases: N/A 5 errors: starting the scheduler cannot fail -->
<!-- cases: N/A 6 not found/duplicate: one scheduler per root container; a second Start returns ErrAlreadyStarted as specified in container-start -->
<!-- cases: 7 lifecycle: idle, failed, shutdown during start and ready are rows; stopping is a later requirement -->
<!-- cases: 8 concurrency: the goroutine count and the goleak scenario -->
<!-- cases: N/A 9 panics: panics in checks are specified in health-check-execution -->
<!-- cases: 10 backward compatibility: containers that never start behave exactly as before -->

### Requirement: Rounds run at the configured interval
After the first round, the scheduler SHALL start a round each time `HealthCheckInterval` elapses on the injector's clock, measured from the start of the previous round. `HealthCheckInterval` 0 SHALL mean `DefaultHealthCheckInterval` (60s), a positive value SHALL set the interval, and a negative value SHALL turn scheduling off.

#### Scenario: Rounds follow the interval on the fake clock
```gherkin
Given an injector whose Clock is a fake clock and HealthCheckInterval is <interval>
And an eager service "db" whose health check counts its calls and returns nil
And the container was started, so the first round ran
When the fake clock advances by <advance> in one step
Then the health check of "db" was called <calls> times in total

Examples:
  | interval    | advance    | calls |
  | 0 (default) | 59.999s    | 1     |
  | 0 (default) | 60s        | 2     |
  | 10s         | 9.999s     | 1     |
  | 10s         | 10s        | 2     |
  | 1ns         | 1ns        | 2     |
  | -1ns        | 10m        | 0     |
```

#### Scenario: Consecutive advances run one round each
```gherkin
Given an injector whose Clock is a fake clock and HealthCheckInterval is 10s
And an eager service "db" whose health check counts its calls and returns nil
And the container was started, so the first round ran
When the fake clock advances by 10s, 3 times, waiting for each round to finish
Then the health check of "db" was called 4 times in total
```

#### Scenario: A clock jump runs at most one round
```gherkin
Given an injector whose Clock is a fake clock and HealthCheckInterval is 10s
And an eager service "db" whose health check counts its calls and returns nil
And the container was started, so the first round ran
When the fake clock advances by <advance> in one step
Then the health check of "db" was called 2 times in total
And after a further 10s advance it was called 3 times in total

Examples:
  | advance |
  | 50s     |
  | 24h     |
```

<!-- cases: 1 default: the 60s rows -->
<!-- cases: 2 overrides: positive and negative intervals -->
<!-- cases: 3 zero values: 0 means the default -->
<!-- cases: 4 boundaries: one nanosecond before and exactly at the interval; the 1ns minimum positive interval -->
<!-- cases: N/A 5 errors: no invalid value; every int64 duration has a meaning -->
<!-- cases: N/A 6 not found/duplicate: not applicable to an interval -->
<!-- cases: 7 lifecycle: rounds after the first; the clock-jump scenario drops missed rounds -->
<!-- cases: 8 concurrency: rounds are waited for before the next advance, so counts are deterministic -->
<!-- cases: N/A 9 panics: specified in health-check-execution -->
<!-- cases: N/A 10 backward compatibility: new option -->

### Requirement: Each round checks the services present at that round
Each round SHALL check every service of every scope of the container that is a health checker when the round starts: a registered value or eager service implementing `Healthchecker` or `HealthcheckerWithContext`, or a built lazy service whose instance does. A round SHALL NOT check transient services, lazy services not yet built, or aliases (an alias reports its target's result).

#### Scenario: Services that appear later join the next round
```gherkin
Given an injector whose Clock is a fake clock and HealthCheckInterval is 10s
And the container was started with no health checker, so the first round checked nothing
And then <change>
When the fake clock advances by 10s and the round finishes
Then the health check of "<service>" was called <calls> times

Examples:
  | change                                                                  | service   | calls |
  | a Healthchecker value "late" is registered in the root scope            | late      | 1     |
  | a lazy Healthchecker "lazy" is registered and invoked                   | lazy      | 1     |
  | a lazy Healthchecker "lazy" is registered and not invoked               | lazy      | 0     |
  | a child scope "child" is created with a Healthchecker value "inner"     | inner     | 1     |
  | a transient Healthchecker "tmp" is registered and invoked               | tmp       | 0     |
  | a Healthchecker "db" is registered with an alias As[*DB, Healthchecker] | db        | 1     |
```

#### Scenario: Removed and replaced services leave the next round
```gherkin
Given an injector whose Clock is a fake clock and HealthCheckInterval is 10s
And a Healthchecker value "db" (instance A) in child scope "child"
And the container was started, so the first round checked instance A once
And then <change>
When the fake clock advances by 10s and the round finishes
Then instance A's health check was called <a calls> times in total
And instance B's health check was called <b calls> times in total

Examples:
  | change                                                   | a calls | b calls |
  | nothing                                                  | 2       | 0       |
  | "db" is overridden with a Healthchecker value (instance B) | 1     | 1       |
  | scope "child" is shut down                               | 1       | 0       |
```

<!-- cases: 1 default: interval only shortened to 10s for brevity; membership does not depend on the interval -->
<!-- cases: N/A 2 overrides: membership has no option -->
<!-- cases: 3 zero values: a container with no health checker runs empty rounds -->
<!-- cases: 4 boundaries: 0 and 1 checker; many is the multi-scope row -->
<!-- cases: N/A 5 errors: membership has no error path -->
<!-- cases: 6 not found/duplicate: Override replaces the checked instance; an alias does not add a check -->
<!-- cases: 7 lifecycle: late registration, late build, child scope creation and shutdown -->
<!-- cases: N/A 8 concurrency: covered by health-check-execution -->
<!-- cases: N/A 9 panics: specified in health-check-execution -->
<!-- cases: N/A 10 backward compatibility: new behavior -->

### Requirement: Scheduled checks have a default timeout
A scheduled check SHALL be bounded by `HealthCheckTimeout` when it is positive, by `DefaultScheduledHealthCheckTimeout` (10s) when it is 0, and SHALL be unbounded when it is negative. A scheduled check that reaches its bound SHALL be recorded as unhealthy with an error matching `ErrHealthCheckTimeout` and `context.DeadlineExceeded`. Forced runs SHALL keep their existing timeouts.

#### Scenario: Scheduled check bounds
```gherkin
Given an injector whose Clock is a fake clock, HealthCheckInterval is 0 and HealthCheckTimeout is <timeout>
And a service "db" whose HealthCheck(ctx) blocks until ctx is done, then returns ctx.Err()
And the container was started, so the first round is in flight
When the fake clock advances by <advance>
Then the report entry for "db" is <state> with error <error>

Examples:
  | timeout     | advance | state            | error                                                 |
  | 0 (default) | 9.999s  | HealthUnknown    | nil, with PendingSince set                            |
  | 0 (default) | 10s     | HealthUnhealthy  | "DI: health check timeout: context deadline exceeded" |
  | 2s          | 2s      | HealthUnhealthy  | "DI: health check timeout: context deadline exceeded" |
  | -1ns        | 59.999s | HealthUnknown    | nil, with PendingSince set                            |
```

#### Scenario: Forced runs keep their unbounded default
```gherkin
Given an injector whose Clock is a fake clock and HealthCheckTimeout is 0
And a service "db" whose HealthCheck(ctx) blocks until ctx is done
And the container was not started
When the consumer calls HealthCheck() and the fake clock advances by 10s
Then the call has not returned
And it returns once the check is released
```

<!-- cases: 1 default: the 10s rows -->
<!-- cases: 2 overrides: positive and negative HealthCheckTimeout -->
<!-- cases: 3 zero values: HealthCheckTimeout 0 selects the scheduled default -->
<!-- cases: 4 boundaries: one millisecond before and exactly at 10s -->
<!-- cases: 5 errors: the timeout error matches ErrHealthCheckTimeout and context.DeadlineExceeded, as in health-check-timeouts -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: the round in flight at Ready -->
<!-- cases: 8 concurrency: the check is released before the test ends, so no goroutine leaks -->
<!-- cases: N/A 9 panics: specified in health-check-execution -->
<!-- cases: 10 backward compatibility: the forced-run scenario keeps today's unbounded default -->

### Requirement: Scheduling stops when root shutdown begins
When root `Shutdown` begins, the scheduler SHALL stop before the Closing hooks run and before any service shuts down: no round SHALL start afterwards, and every scheduled check in flight SHALL see its context cancelled. Shutdown SHALL wait for the scheduler goroutine to exit, and SHALL NOT wait for a check that ignores its context.

#### Scenario: No round after shutdown begins
```gherkin
Given an injector whose Clock is a fake clock and HealthCheckInterval is 10s
And an eager service "db" whose health check counts its calls and returns nil
And the container was started, so the first round ran
And a Closing hook that advances the fake clock by 30s
When the consumer calls Shutdown
Then the health check of "db" was called 1 time in total
And goleak reports no leaked goroutine after Shutdown returns
```

#### Scenario: Shutdown does not wait for a check that ignores its context
```gherkin
Given an injector whose HealthCheckTimeout is -1ns
And a service "db" whose HealthCheck() blocks until released and ignores any context
And the container was started, so the first round's check of "db" is in flight
When the consumer calls Shutdown
Then Shutdown returns within 1s
And the check of "db" is released before the test ends
```

#### Scenario: A context-aware check in flight is cancelled at shutdown
```gherkin
Given an injector whose HealthCheckTimeout is -1ns
And a service "db" whose HealthCheck(ctx) blocks until ctx is done, then records ctx.Err() and returns it
And the container was started, so the first round's check of "db" is in flight
When the consumer calls Shutdown
Then the recorded error is context.Canceled before any service shutdown starts
```

<!-- cases: N/A 1 default: stopping has no option -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: N/A 3 zero values: a container that never started has no scheduler to stop (first requirement) -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: stopping cannot fail; a hung check does not fail Shutdown -->
<!-- cases: 6 not found/duplicate: a second Shutdown returns the first report as in container-start; the scheduler stops once -->
<!-- cases: 7 lifecycle: order against Closing hooks and service shutdown -->
<!-- cases: 8 concurrency: cancellation and goleak -->
<!-- cases: N/A 9 panics: specified in health-check-execution -->
<!-- cases: N/A 10 backward compatibility: Shutdown of an unstarted container is unchanged -->

### Requirement: A clone is scheduled independently
A clone SHALL start its own scheduler only when the clone itself reaches `PhaseReady`, SHALL NOT share health results with its source, and SHALL stop its scheduler at its own Shutdown.

#### Scenario: Clone scheduling follows the clone's lifecycle
```gherkin
Given a started injector "source" with a fake clock, HealthCheckInterval 10s and a Healthchecker value "db" returning nil
When the consumer clones it, <action>, then reads the clone's HealthReport()
Then the clone's report has Monitored <monitored> and the state of "db" is HealthHealthy
And the clone's first result for "db" called no HookServiceHealthChanged hook of the source

Examples:
  | action                              | monitored |
  | does not start the clone            | false     |
  | starts the clone                    | true      |
```

#### Scenario: The source keeps scheduling after its clone shuts down
```gherkin
Given a started injector "source" with a fake clock, HealthCheckInterval 10s and a Healthchecker value "db" that counts its calls
And a started clone of it
When the consumer shuts the clone down and the fake clock advances by 10s
Then the source's report has Monitored true
And "db" was checked by a source round after the clone shut down
```

<!-- cases: N/A 1 default: same defaults as any container -->
<!-- cases: N/A 2 overrides: CloneWithOpts takes the clone's own options -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: none -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: the clone starts idle, as in container-start -->
<!-- cases: N/A 8 concurrency: no shared state between clone and source -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: N/A 10 backward compatibility: clones that never start are unchanged -->

### Requirement: Service health changes call the service hooks
Each `HookServiceHealthChanged(scope, serviceName, previous, current ServiceHealth)` SHALL be called synchronously, once for each change of a service's `State` (including from `HealthUnknown` to its first result), whichever path recorded it, before that result reaches any caller. A new error with an unchanged state SHALL NOT call it. `AddServiceHealthChangedHook` SHALL append to the same list.

#### Scenario: Service hook calls follow state changes
```gherkin
Given an injector whose Clock is a fake clock and HealthCheckInterval is 10s
And a Healthchecker "db" whose results, round after round, are <results>
And a HookServiceHealthChanged hook that records (previous state, current state, current error)
When the container is started and the fake clock advances by 10s, <rounds - 1> times, waiting for each round
Then the hook recorded <calls>

Examples:
  | results                                   | rounds | calls                                                                                             |
  | nil, nil, nil                             | 3      | (Unknown, Healthy, nil)                                                                           |
  | nil, errors.New("down"), nil              | 3      | (Unknown, Healthy, nil), (Healthy, Unhealthy, "down"), (Unhealthy, Healthy, nil)                  |
  | errors.New("a"), errors.New("b")          | 2      | (Unknown, Unhealthy, "a")                                                                         |
```

#### Scenario: A forced run records changes too
```gherkin
Given a started injector with a Healthchecker "db" that returned nil in the first round
And a HookServiceHealthChanged hook that records calls
And "db" now returns errors.New("down")
When the consumer calls HealthCheck() on the root scope
Then the hook recorded (Healthy, Unhealthy, "down") before HealthCheck() returned
```

#### Scenario: Hooks run one at a time, in order of addition
```gherkin
Given a container with hooks A and B added in that order with AddServiceHealthChangedHook
And 10 Healthchecker services whose first results arrive concurrently
When the first round finishes
Then the hooks were called 20 times in total, never two at once
And for each service, A was called before B
```

<!-- cases: 1 default: no hook registered is the default; nothing is called -->
<!-- cases: 2 overrides: the InjectorOpts field and the Add method share one list -->
<!-- cases: 3 zero values: a nil hook in the list is skipped -->
<!-- cases: 4 boundaries: the first result counts as a change; an unchanged state does not -->
<!-- cases: N/A 5 errors: hooks return nothing -->
<!-- cases: 6 not found/duplicate: a removed service calls no hook -->
<!-- cases: 7 lifecycle: scheduled and forced paths -->
<!-- cases: 8 concurrency: serialized calls under concurrent results -->
<!-- cases: 9 panics: the hook-panic requirement below -->
<!-- cases: N/A 10 backward compatibility: new hook -->

### Requirement: Overall health changes call the container hooks
Each `HookContainerHealthChanged(root, previous, current HealthState)` SHALL be called once for each change of the container's overall state, computed as in `HealthReport()` on the root, after the service hooks for the same result. `AddContainerHealthChangedHook` SHALL append to the same list.

#### Scenario: Container hook calls follow the overall state
```gherkin
Given an injector whose Clock is a fake clock and HealthCheckInterval is 10s
And Healthcheckers "a" and "b" whose results per round are <results>
And a HookContainerHealthChanged hook that records (previous, current)
When the container is started and the fake clock advances by 10s once, waiting for each round
Then the hook recorded <calls>

Examples:
  | results                                         | calls                                             |
  | round 1: a nil, b nil; round 2: a nil, b nil    | (Unknown, Healthy)                                |
  | round 1: a nil, b nil; round 2: a nil, b "down" | (Unknown, Healthy), (Healthy, Unhealthy)          |
  | round 1: a "x", b nil; round 2: a "y", b nil    | (Unknown, Unhealthy)                              |
```

<!-- cases: 1 default: no hook registered -->
<!-- cases: 2 overrides: field and Add method -->
<!-- cases: 3 zero values: a nil hook is skipped -->
<!-- cases: 4 boundaries: the first overall result counts as a change -->
<!-- cases: N/A 5 errors: hooks return nothing -->
<!-- cases: N/A 6 not found/duplicate: one overall state per container -->
<!-- cases: 7 lifecycle: rounds after Ready -->
<!-- cases: 8 concurrency: ordering after service hooks -->
<!-- cases: 9 panics: the hook-panic requirement below -->
<!-- cases: N/A 10 backward compatibility: new hook -->

### Requirement: A panicking health hook does not stop scheduling
A panic in a `HookServiceHealthChanged` or `HookContainerHealthChanged` hook SHALL be recovered. It SHALL be passed to `HookContainerPanic(root, "healthchanged", value)` when that option is set and logged with `Logf` otherwise, and SHALL never be re-raised. The remaining hooks for the same change SHALL still be called, and later rounds SHALL still run.

#### Scenario: Hook panic is contained
```gherkin
Given an injector whose Clock is a fake clock, HealthCheckInterval is 10s and HookContainerPanic is <panic hook>
And service hooks A, which panics "boom", and B, added in that order
And a Healthchecker "db" that returns nil then errors.New("down")
When the container is started and the fake clock advances by 10s
Then B was called 2 times
And <observed>

Examples:
  | panic hook                         | observed                                                     |
  | set, recording (point, value)      | HookContainerPanic recorded ("healthchanged", "boom") 2 times |
  | nil                                | Logf received 2 lines containing "boom"                       |
```

<!-- cases: N/A 1 default: panics are not a default path -->
<!-- cases: 2 overrides: with and without HookContainerPanic -->
<!-- cases: N/A 3 zero values: a nil panic value is reported as runtime.PanicNilError, as for checks -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: hooks return nothing -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: the next round still runs -->
<!-- cases: N/A 8 concurrency: hooks are serialized -->
<!-- cases: 9 panics: this requirement -->
<!-- cases: N/A 10 backward compatibility: new hook -->
