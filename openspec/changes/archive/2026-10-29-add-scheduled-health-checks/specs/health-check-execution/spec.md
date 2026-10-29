# Spec Delta

## Purpose

Defines how the container runs health checks on every path (report reads, scheduled rounds, `HealthCheck()` and the single-service functions). It covers sharing work between concurrent callers, bounding each caller's wait, and limiting concurrency. It ensures panics are contained, each underlying check runs once, and a health check never blocks registration, building or shutdown.

## ADDED Requirements

### Requirement: Each caller's wait is bounded by its own context and its own timeout
Every caller SHALL stop waiting when its context ends or its own path's bound elapses, measured from its call. The bounds are `HealthCheckTimeout` per service and `HealthCheckGlobalTimeout` for `HealthCheck()`, and `HealthCheckSingleTimeout` (0 means `HealthCheckTimeout`) for the single-service functions. For each unfinished service the caller SHALL get an error matching `ErrHealthCheckTimeout` and the cause, whoever started the check.

#### Scenario: Each caller keeps its own bound when it joins a check
```gherkin
Given an injector that is never started, with a fake clock, HealthCheckTimeout <timeout>, HealthCheckSingleTimeout <single> and HealthCheckGlobalTimeout <global>
And a service "db" whose HealthCheck() blocks until released and ignores any context, whose check another caller started earlier and is still running
When the consumer calls <entry> and the fake clock advances by <advance>
Then the call returns <result> for "db"

Examples:
  | timeout | single | global | entry                                  | advance | result                                                   |
  | 50ms    | 0      | 0      | HealthCheckNamed(injector, "db")       | 50ms    | "DI: health check timeout: context deadline exceeded"    |
  | 50ms    | 0      | 0      | HealthCheckNamed(injector, "db")       | 49ms    | nothing yet (still waiting)                              |
  | 50ms    | 20ms   | 0      | HealthCheckNamed(injector, "db")       | 20ms    | "DI: health check timeout: context deadline exceeded"    |
  | 50ms    | -1ns   | 0      | HealthCheckNamed(injector, "db")       | 10m     | nothing yet (still waiting)                              |
  | 0       | 0      | 30ms   | HealthCheck() on the root scope        | 30ms    | "DI: health check timeout: context deadline exceeded"    |
  | 0       | 0      | 30ms   | HealthCheck() on the root scope        | 29ms    | nothing yet (still waiting): its own bound, not the starter's |
  | 0       | 0      | 30ms   | HealthCheckNamed(injector, "db")       | 10m     | nothing yet (still waiting): the global timeout does not apply |
```

#### Scenario: One caller leaving does not cancel the check for the others
```gherkin
Given an injector that is never started, with a service "db" whose HealthCheck(ctx) blocks until released or ctx is done, then returns ctx.Err() or nil
When caller A calls HealthCheckWithContext with a context cancelled after 10ms, caller B calls HealthCheck() at the same time, A's context is cancelled, then the check is released
Then A's result for "db" is "DI: health check timeout: context canceled"
And B's result for "db" is nil
```

<!-- cases: 1 default: no bound at zero configuration for forced paths; the waiting rows -->
<!-- cases: 2 overrides: HealthCheckTimeout, HealthCheckSingleTimeout, HealthCheckGlobalTimeout and their interplay -->
<!-- cases: 3 zero values: 0 inherits or means no bound; a nil or background context waits -->
<!-- cases: 4 boundaries: one millisecond before and exactly at the bound -->
<!-- cases: 5 errors: the timeout message and errors.Is matches of health-check-timeouts -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: joining a check another caller started -->
<!-- cases: 8 concurrency: two callers with different contexts -->
<!-- cases: N/A 9 panics: a later requirement -->
<!-- cases: 10 backward compatibility: each forced path keeps the bounds of health-check-timeouts and of HealthCheckSingleTimeout; report paths are in health-reports -->

### Requirement: A joining caller gets what the check returned
A caller that joined a check SHALL get what that check returned once it returns within the caller's own bound and context, also when the starter's bound cancelled the check's context meanwhile. A check that returns its context's error after that context ended counts as a timeout, matching `ErrHealthCheckTimeout` and that cause.

#### Scenario: A joining caller gets the check's result after the starter's bound
```gherkin
Given an injector that is never started, with a fake clock and HealthCheckGlobalTimeout 30ms
And a service "db" whose check <check>
And a HealthCheck() call on the root scope started that check, and 10ms later the consumer called HealthCheckNamed(injector, "db")
When the fake clock advances by 50ms, so the HealthCheck() call returns its timeout for "db", and the check is released
Then the consumer's call returns <result>
And the health check of "db" was called 1 time

Examples:
  | check                                                                        | result                                                  |
  | blocks until released, ignoring its context, then returns nil                | nil                                                     |
  | blocks until released, ignoring its context, then returns errors.New("down") | the same "down" error                                   |
  | blocks until released or its context is done, then returns ctx.Err()         | "DI: health check timeout: context deadline exceeded"   |
```

<!-- cases: N/A 1 default: no option -->
<!-- cases: 2 overrides: the starter's HealthCheckGlobalTimeout; the joiner's unbounded single-service path -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: 4 boundaries: the starter's bound passed before the check returned -->
<!-- cases: 5 errors: the check's nil, its own error, and its ended context's error as a timeout -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: the starter leaves at its bound while the joiner waits (verification 3, W1) -->
<!-- cases: 8 concurrency: two callers with different bounds share one check -->
<!-- cases: N/A 9 panics: a later requirement -->
<!-- cases: N/A 10 backward compatibility: on 76404ef each caller ran its own check -->

### Requirement: A caller whose context has already ended starts no check
A caller whose context has already ended when it calls, and that finds no check in flight for the service it can join, SHALL start none and SHALL wait for no check started later: it SHALL get its timeout error at once, matching `ErrHealthCheckTimeout` and the context's cause, as on 76404ef. Likewise, a caller queued behind a check it could not join, whose context ends before that check returns, SHALL NOT have a fresh check started for it: it SHALL get its timeout error.

#### Scenario: A caller whose context has already ended starts no check
```gherkin
Given an injector that is never started, with a Logf that records its lines, and a service "db" whose HealthCheck(ctx) blocks until released or ctx is done, with no check of "db" in flight
When the consumer calls <entry> with a context that is already cancelled
Then the call returns at once with <result>
And the Logf recorded no line ending in "requested health check for service db"

Examples:
  | entry                                        | result                                                                        |
  | HealthCheckWithContext on the root scope     | "DI: health check timeout: context canceled" for "db" in the map              |
  | HealthCheckNamedWithContext(ctx, injector, "db") | "DI: health check timeout: context canceled"                              |
  | HealthReportWithContext on the root scope    | the entry for "db" HealthUnhealthy, "DI: health check timeout: context canceled" |
```

#### Scenario: A caller that gives up behind an abandoned check starts no fresh check
```gherkin
Given an injector that is never started, with a service "db" whose HealthCheck() blocks until released, ignoring its context, whose first check a HealthCheckNamedWithContext call left running, abandoned
And a caller of "db" queued behind that check, as HealthCheckWithContext queues each service before it waits
When the caller's context is cancelled <when>, then the abandoned check returns and the caller waits
Then the caller gets "DI: health check timeout: context canceled"
And the health check of "db" was called 1 time

Examples:
  | when             |
  | before it queues |
  | after it queues  |
```

<!-- cases: N/A 1 default: a context that has not ended behaves as in the previous requirement -->
<!-- cases: N/A 2 overrides: no option changes this rule -->
<!-- cases: 3 zero values: a context cancelled before the call -->
<!-- cases: 4 boundaries: no check in flight; joining a check in flight is the previous requirement -->
<!-- cases: 5 errors: "DI: health check timeout: context canceled" on each entry point (verification W2) -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: a check abandoned just before returns, then a caller that already gave up; or that caller gives up after queueing behind it, before it returns (verification W2, code review R14) -->
<!-- cases: 8 concurrency: no goroutine is started for such a caller -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: as on 76404ef, which never ran a check for such a caller -->

### Requirement: A check sees its starter's context
A check SHALL receive a context carrying the values and the deadline of the caller that started it, further bounded by that caller's own bound. The context SHALL be cancelled when that bound elapses, when every caller waiting for the check has stopped waiting, or when root shutdown begins. A caller that joins a check SHALL NOT change the check's context.

#### Scenario: Deadline and values reach the check
```gherkin
Given an injector that is never started, with a HealthcheckerWithContext "c" that records whether ctx.Deadline() is set and the value of key "trace"
When the consumer calls <entry> with a context carrying "trace" = "trace-1" and a deadline 1 minute ahead
Then the check recorded a deadline and "trace-1"

Examples:
  | entry                                    |
  | HealthCheckWithContext on the root scope |
  | HealthCheckNamedWithContext for "c"      |
  | HealthReportWithContext on the root scope |
```

#### Scenario: The only caller leaving cancels the check
```gherkin
Given an injector that is never started, with a service "db" whose HealthCheck(ctx) blocks until ctx is done, then records and returns ctx.Err()
When the only caller calls HealthCheckWithContext with a context cancelled after 10ms
Then the call returns "DI: health check timeout: context canceled" for "db"
And the check recorded context.Canceled
```

<!-- cases: 1 default: zero configuration -->
<!-- cases: 2 overrides: the starter's timeout further bounds the context -->
<!-- cases: N/A 3 zero values: a background context carries no deadline, as today -->
<!-- cases: N/A 4 boundaries: covered by the previous requirement -->
<!-- cases: 5 errors: the cancellation cause reaches the check -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: shutdown cancellation is in health-check-scheduling -->
<!-- cases: 8 concurrency: cancellation when the last caller leaves -->
<!-- cases: N/A 9 panics: a later requirement -->
<!-- cases: 10 backward compatibility: the deadline and values a check sees today are kept for its starter; a joining caller's values do not reach the check (flagged) -->

### Requirement: A service never has two checks in flight
While a service's check is running, any path that would check it SHALL wait for that check instead of starting another. A check that reached its bound, or that every caller left, SHALL stay in flight until it returns. A caller arriving then SHALL NOT receive its result. It SHALL wait, within its own bound, for that check to return, then start a fresh one.

#### Scenario: A check that ignores its context does not pile up
```gherkin
Given an injector that is never started, with HealthCheckTimeout 5ms and a negative HealthReportCacheTTL, so no HealthReport read is answered from a recorded result
And a service "db" whose HealthCheck() blocks until released and ignores any context
When the consumer calls <entry> 100 times, one after another
Then every call returns or reports "DI: health check timeout: context deadline exceeded" for "db"
And the health check of "db" was called 1 time
And after the check is released and the calls end, goleak reports no leaked goroutine

Examples:
  | entry                                  |
  | HealthCheck() on the root scope        |
  | HealthCheckNamed(injector, "db")       |
  | HealthReport() on the root scope       |
```

#### Scenario: Concurrent forced calls share the in-flight checks
```gherkin
Given an injector that is never started, with 10 Healthchecker values "svc-0" to "svc-9" that count their calls and block until released
When 50 goroutines call HealthCheck() on the root scope, and the checks are released once all 50 wait
Then each service's health check was called 1 time
And every call returns a map with 10 entries, each nil
And deleting a key from one returned map leaves the others with 10 entries
```

#### Scenario: A call that joins a running check gets its result
```gherkin
Given an injector that is never started, with a service "db" whose check blocks until released, then returns errors.New("down")
And a HealthCheck() call on the root scope is running that check
When the consumer calls HealthCheckNamed(injector, "db"), then the check is released
Then the call returns the same "down" error
And the health check of "db" was called 1 time
```

#### Scenario: An abandoned check is never joined
```gherkin
Given an injector that is never started, with a service "db" whose HealthCheck(ctx) blocks until released, then returns ctx.Err()
And a HealthCheckWithContext call whose context was cancelled after 10ms left that check running
When the consumer calls HealthCheck() on the root scope, then the first check is released
Then the call does not return context.Canceled for "db"
And the health check of "db" was called 2 times, never two at once
```

#### Scenario: Sequential calls check again
```gherkin
Given an injector that is never started, with a Healthchecker "db" that counts its calls
When the consumer calls <entry> <n> times, one after another
Then the health check of "db" was called <n> times

Examples:
  | entry                            | n |
  | HealthCheck() on the root scope  | 1 |
  | HealthCheck() on the root scope  | 3 |
  | HealthCheckNamed(injector, "db") | 2 |
```

<!-- cases: 1 default: zero configuration -->
<!-- cases: 2 overrides: with and without HealthCheckTimeout; caching off (negative HealthReportCacheTTL), so every report read meets the check in flight (verification 2, W1) -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: 4 boundaries: 1, 2, 3, 50 and 100 calls -->
<!-- cases: 5 errors: a joining caller gets the check's error; an abandoned check's cancellation never reaches a new caller -->
<!-- cases: 6 not found/duplicate: an unknown name still returns ErrServiceNotFound, as today -->
<!-- cases: 7 lifecycle: joining, abandonment, and a fresh check after the old one returns -->
<!-- cases: 8 concurrency: never two checks at once; 50 concurrent forced calls; isolated result maps; goleak -->
<!-- cases: N/A 9 panics: a later requirement -->
<!-- cases: 10 backward compatibility: sequential calls still check each time; concurrent forced calls return the same maps with fewer check calls (regression D1) -->

### Requirement: HealthCheckParallelism caps checks without blocking callers or registration
With `HealthCheckParallelism` n > 0, at most n checks started by `HealthCheck()`, report reads and scheduled rounds SHALL run at once. Waiting for a slot SHALL honor each caller's context and bound, SHALL NOT hold any scope lock, and SHALL have no queue limit. A check's own bound SHALL start when it starts running. The single-service functions SHALL NOT take a slot, as today. With 0 (default), checks SHALL NOT be limited.

#### Scenario: Parallelism caps running checks
```gherkin
Given an injector that is never started, with HealthCheckParallelism <n>
And 20 Healthchecker values whose checks block until released
When the consumer starts HealthCheck() on the root scope and waits until no further check starts
Then exactly <running> checks are running
And after every running check is released, the call returns 20 nil entries

Examples:
  | n           | running |
  | 1           | 1       |
  | 4           | 4       |
  | 0 (default) | 20      |
```

#### Scenario: Waiting for a slot honors the context
```gherkin
Given an injector that is never started, with HealthCheckParallelism 1
And a service "hung" whose HealthCheck() blocks until released and ignores any context
And a Healthchecker value "db"
And one HealthCheck() call on the root scope holds the slot with "hung"
When the consumer calls HealthCheckWithContext on the root scope with a context that times out after 20ms
Then the call returns within 1s, with an error for "db" matching ErrHealthCheckTimeout and context.DeadlineExceeded
```

#### Scenario: Registration is not blocked by checks waiting for a slot
```gherkin
Given an injector that is never started, with HealthCheckParallelism 1
And a service "hung" whose HealthCheck() blocks until released and ignores any context, already running
And 1001 goroutines calling HealthCheck() on the root scope, all waiting
When the consumer calls ProvideNamedValue(injector, "late", 42)
Then the call returns within 1s
```

#### Scenario: Many services with a small limit all complete
```gherkin
Given an injector that is never started, with HealthCheckParallelism 1 and 2000 Healthchecker values returning nil
When the consumer calls HealthCheck() on the root scope
Then the map has 2000 entries, each nil
```

<!-- cases: 1 default: unlimited -->
<!-- cases: 2 overrides: n = 1 and n = 4 -->
<!-- cases: 3 zero values: 0 means unlimited -->
<!-- cases: 4 boundaries: exactly n running; 2000 services against a limit of 1 (the former 1000-slot queue) -->
<!-- cases: 5 errors: the timeout error while waiting for a slot (regression D2) -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: a check waiting for its slot (its callers' bounds: the next requirements) -->
<!-- cases: 8 concurrency: slot waits, scope locks, registration (regression D2) -->
<!-- cases: N/A 9 panics: a later requirement -->
<!-- cases: 10 backward compatibility: the default stays unlimited; single-service functions keep skipping the limit, as today -->

### Requirement: A check holds its slot only until its bound has passed
A check SHALL hold its `HealthCheckParallelism` slot until it returns, its context ends (its bound elapsed, every caller stopped waiting, or root shutdown began), or the report bound has passed since it took the slot (no time limit when the report bound is negative). A check that keeps running after that, ignoring its context, SHALL NOT count against the limit, so a hung check never stalls the callers queued behind it.

#### Scenario: A check that outlives its bound gives its slot back
```gherkin
Given an injector with HealthCheckParallelism 1, HealthCheckTimeout <timeout> and a manual HealthScheduler
And a service "hog" whose HealthCheck() blocks until released and ignores any context, holding the only slot in <holder>
And a Healthchecker "db" that counts its calls, registered once "hog" holds the slot
When <entry> is called
Then it returns <after> after its call, with <result>
And the health check of "db" was called 1 time

Examples:
  | timeout | holder                                                                    | entry                                          | after | result                           |
  | 1s      | a HealthCheck() call that timed out on "hog" and returned                 | HealthCheck() on the root scope                | 1s    | nil for "db"                     |
  | 1s      | a HealthCheck() call that timed out on "hog" and returned                 | HealthReport() on the root scope               | 1s    | the entry for "db" HealthHealthy |
  | 1s      | a HealthCheck() call that timed out on "hog" and returned                 | the stored round, once the injector is started | 1s    | "db" recorded HealthHealthy      |
  | 0       | a HealthCheck() call on the root scope still waiting for "hog", unbounded | HealthReport() on the root scope               | 10s   | the entry for "db" HealthHealthy |
```

<!-- cases: N/A 1 default: no limit by default, so no check holds a slot -->
<!-- cases: 2 overrides: HealthCheckTimeout 1s, and 0 with the 10s report bound -->
<!-- cases: N/A 3 zero values: with a negative report bound a slot has no time limit, as the check has no bound -->
<!-- cases: 4 boundaries: a slot held past its holder's bound; the callers return at their own bounds -->
<!-- cases: N/A 5 errors: the timeouts of the callers behind are the bound requirements' -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: a hung check giving its slot back once abandoned, or past the report bound while an unbounded caller still waits for it (code review R13) -->
<!-- cases: 8 concurrency: forced calls, report reads and rounds behind a hung check under a limit of 1 -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: a hung check no longer holds its slot until it returns, as a 76404ef pool worker did (flagged) -->

### Requirement: A caller's per-service bound starts when its check starts running
While a check waits for a `HealthCheckParallelism` slot, the per-service bound of every caller waiting for it (`HealthCheckTimeout` on forced paths, the report bound on report paths) SHALL NOT run: it SHALL start when the check starts running. Each caller's context and `HealthCheckGlobalTimeout` SHALL still bound its wait from its call. That wait is bounded too: each check ahead of it holds its slot at most until the report bound has passed (previous requirement).

#### Scenario: A caller's per-service bound does not count the wait for a slot
```gherkin
Given an injector that is never started, with a fake clock, HealthCheckParallelism 1 and HealthCheckTimeout <timeout>
And Healthcheckers whose checks block on the fake clock for <takes>, one each, then return nil
When the consumer calls <entry> on the root scope and the fake clock advances until every check returned
Then every service is <result>

Examples:
  | timeout | takes      | entry          | result                   |
  | 0       | 9s, 0s     | HealthReport() | HealthHealthy in the report |
  | 0       | 9s, 2s     | HealthReport() | HealthHealthy in the report |
  | 0       | 6s, 6s, 6s | HealthReport() | HealthHealthy in the report |
  | 10s     | 9s, 2s     | HealthCheck()  | nil in the map           |
```

#### Scenario: HealthCheckGlobalTimeout bounds the wait for a slot from the call
```gherkin
Given an injector that is never started, with a fake clock, HealthCheckParallelism 1, HealthCheckTimeout 50ms and HealthCheckGlobalTimeout 30ms
And a Healthchecker "hog" in child scope "child", whose check blocks until released and holds the only slot through a HealthReport() read on that scope
And a Healthchecker root/"db" returning nil
When the consumer calls HealthCheck() on the root scope, so the check of "db" waits for the slot, and the fake clock advances by <advance>
Then the call has <returned>

Examples:
  | advance | returned                                                             |
  | 29ms    | not returned                                                         |
  | 30ms    | returned "DI: health check timeout: context deadline exceeded" for "db" |
```

<!-- cases: N/A 1 default: no limit by default, so no check waits for a slot -->
<!-- cases: 2 overrides: HealthCheckParallelism 1 with the report bound (HealthCheckTimeout 0) and with a positive HealthCheckTimeout -->
<!-- cases: 3 zero values: HealthCheckTimeout 0 uses the 10s report bound -->
<!-- cases: 4 boundaries: checks that each fit the bound while their total exceeds it; a check that returns at once; one millisecond before and at a global timeout reached while waiting for a slot (verification 3, W5) -->
<!-- cases: 5 errors: no timeout error for a check that fits its own bound (review R9) -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: report-path and forced checks waiting for their slot -->
<!-- cases: 8 concurrency: checks serialized by a limit of 1 -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: as with the 76404ef pool, whose workers applied HealthCheckTimeout when they ran a job -->

### Requirement: A caller queued behind an abandoned check keeps its bound from its call
A caller that waits for a check it could not join (an abandoned check still running) SHALL keep its per-service bound running from its call, through that wait and through the slot wait of the check started for it afterwards, so a hung abandoned check never holds it past its bound.

#### Scenario: A caller queued behind an abandoned check keeps its bound from its call
```gherkin
Given an injector that is never started, with HealthCheckParallelism 1 and HealthCheckTimeout 20ms
And a service "leaf" whose first check blocks until released, ignoring its context, and was left running, abandoned, by HealthCheckNamedWithContext with a context that timed out after 10ms
And a service "hog" whose check blocks until released, ignoring its context
When <entry> is called on the root scope, 1ms later a HealthCheck() call gives "hog" the only slot, which it holds within its own 20ms bound, and 4ms after that the first check of "leaf" is released, so the check started for the caller waits for that slot
Then the call returns 20ms after it was made, with <result>
And "leaf" was checked once when the call returns

Examples:
  | entry          | result                                                                                  |
  | HealthCheck()  | "DI: health check timeout: context deadline exceeded" for "leaf" in the map             |
  | HealthReport() | the entry for "leaf" HealthUnhealthy, "DI: health check timeout: context deadline exceeded" |
```

<!-- cases: N/A 1 default: no limit by default, so no check waits for a slot -->
<!-- cases: 2 overrides: HealthCheckParallelism 1, HealthCheckTimeout 20ms -->
<!-- cases: N/A 3 zero values: with no per-service bound, the caller's context alone bounds its wait -->
<!-- cases: 4 boundaries: the bound elapsing while the check started for the caller still waits for its slot -->
<!-- cases: 5 errors: the timeout of a forced and of a report-path caller (verification W1) -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: queued behind an abandoned check, then behind a slot held by another check (review R2) -->
<!-- cases: 8 concurrency: a limit of 1 held by a hung check -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new behavior of this change -->

### Requirement: A single-service check never waits for a slot
A single-service function SHALL run its check at once, without a slot. When the service's check is queued for a slot, the single-service call SHALL run it at once, and every caller waiting for that check SHALL get its result. Composite checks therefore complete under any limit. A caller that waited for a check it could not join SHALL keep its own path's slot rule and bound in the check started for it afterwards.

#### Scenario: Nested checks complete under a limit of one
```gherkin
Given an injector that is never started, with HealthCheckParallelism 1
And a Healthchecker value "leaf" returning nil
And a HealthcheckerWithContext "agg" whose check returns HealthCheckNamedWithContext(ctx, injector, "leaf")
When the consumer calls <entry>
Then the call returns within 1s with <result>
And the health check of "leaf" never ran twice at once

Examples:
  | entry                             | result                    |
  | HealthCheck() on the root scope   | {"agg": nil, "leaf": nil} |
  | HealthCheckNamed(injector, "agg") | nil                       |
```

#### Scenario: Callers queued behind a check they could not join keep their own slot rule
```gherkin
Given an injector that is never started, with HealthCheckParallelism 1, HealthCheckTimeout <timeout> and HealthCheckSingleTimeout <single>
And a service "leaf" whose first check blocks until released, ignoring its context, and whose later checks record whether their context has a deadline, then return nil
And <holder>
And <first> left the first check of "leaf" running, abandoned
When <second> is called, then the first check of "leaf" is released
Then <observed>

Examples:
  | timeout | single | holder                                                                                  | first                                                                 | second                             | observed                                                                 |
  | 0       | 0      | a service "hog" whose check blocks until released holds the slot in a HealthCheck() call | HealthCheckNamedWithContext with a context that times out after 10ms | HealthCheck() on the root scope    | the second check of "leaf" does not start while "hog" holds the slot      |
  | 50ms    | -1ns   | no other service                                                                        | HealthCheck() on the root scope, timing out on "leaf" after 50ms      | HealthCheckNamed(injector, "leaf") | the second check of "leaf" runs, and its context has no deadline         |
```

<!-- cases: 1 default: an unlimited container runs nested checks as today -->
<!-- cases: 2 overrides: a limit of 1, the tightest -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: 4 boundaries: limit 1 -->
<!-- cases: N/A 5 errors: not applicable -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: a single-service call taking over a check queued for a slot; callers queued behind an abandoned check, single-service and limited (review R10) -->
<!-- cases: 8 concurrency: no deadlock under the limit -->
<!-- cases: N/A 9 panics: a later requirement -->
<!-- cases: 10 backward compatibility: composite checks keep working as today; a check calling itself still recurses as today (cycle detection is out of scope) -->

### Requirement: Creating a container starts no goroutine
`New()`, `NewWithOpts()`, `Clone()` and `CloneWithOpts()` SHALL NOT start a goroutine, whatever `HealthCheckParallelism` and `HealthScheduler` are.

#### Scenario: New starts no goroutine
```gherkin
Given HealthCheckParallelism <n> and HealthScheduler <scheduler>
When the consumer builds a container with <constructor> and registers a Healthchecker value "db"
Then the number of goroutines is unchanged

Examples:
  | n | scheduler                                                  | constructor                                        |
  | 0 | nil                                                        | NewWithOpts with them                              |
  | 4 | nil                                                        | NewWithOpts with them                              |
  | 0 | a HealthScheduler that would start a goroutine when called | NewWithOpts with them                              |
  | 4 | a HealthScheduler that would start a goroutine when called | Clone() of a container built with them             |
  | 4 | a HealthScheduler that would start a goroutine when called | CloneWithOpts with them, of a zero-config container |
```

<!-- cases: 1 default: 0 and nil -->
<!-- cases: 2 overrides: a positive parallelism (formerly started 4 pool goroutines, regression D6); a scheduler -->
<!-- cases: N/A 3 zero values: covered by the default row -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: none -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: construction; when the scheduler is called is in health-check-scheduling -->
<!-- cases: 8 concurrency: goroutine count -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: 10 backward compatibility: New() no longer starts pool goroutines (an improvement) -->

### Requirement: A panicking health check returns an error
A panic in a health check SHALL be recovered on every path. The result SHALL be an error matching `ErrHealthCheckPanic`, with the message `DI: health check panic: ` followed by the value. An error value SHALL also match, and a nil value SHALL match `*runtime.PanicNilError` with `errors.As`. When set, `HookContainerPanic(root, "healthcheck", value)` SHALL be called, and a panic it raises SHALL propagate (next requirement).

#### Scenario: Panic results
```gherkin
Given an injector with HookContainerPanic <panic hook>
And a service "db" whose HealthCheck() panics with <value>
When the consumer calls <entry>
Then the result for "db" has the message "<message>" and matches ErrHealthCheckPanic with errors.Is
And it also matches <match>
And <observed>

Examples:
  | panic hook                     | value   | entry                            | message                                                                | match                                 | observed                                            |
  | nil                            | "boom"  | HealthCheck() on the root scope  | DI: health check panic: boom                                           | nothing else                          | the process keeps running                           |
  | nil                            | io.EOF  | HealthCheckNamed(injector, "db") | DI: health check panic: EOF                                            | io.EOF with errors.Is                 | the process keeps running                           |
  | nil                            | nil     | HealthReport() on an idle root   | DI: health check panic: runtime error: panic called with nil argument | *runtime.PanicNilError with errors.As | the process keeps running                           |
  | set, recording (point, value)  | "boom"  | HealthCheck() on the root scope  | DI: health check panic: boom                                           | nothing else                          | HookContainerPanic recorded ("healthcheck", "boom") |
```

#### Scenario: A panic in a scheduled round does not stop later rounds
```gherkin
Given an injector with a manual HealthScheduler, started, and a service "db" whose HealthCheck() panics with "boom" on its first call and returns nil afterwards
And a HookServiceHealthChanged hook that records calls
When the test runs the round twice
Then the report entry for "db" is HealthHealthy
And the hook recorded (Unknown, Unhealthy, "DI: health check panic: boom") then (Unhealthy, Healthy, nil)
```

<!-- cases: N/A 1 default: panics are not a default path -->
<!-- cases: 2 overrides: with and without HookContainerPanic -->
<!-- cases: 3 zero values: a nil panic value -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: 5 errors: message, ErrHealthCheckPanic, the wrapped error value, PanicNilError -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: forced, single-service, report and round paths -->
<!-- cases: N/A 8 concurrency: recovery happens on the goroutine running the check -->
<!-- cases: 9 panics: this requirement (regression D7); a panic raised by HookContainerPanic propagates (next requirement) -->
<!-- cases: 10 backward compatibility: a crash becomes an error (flagged); a single-service panic used to reach the caller as a panic -->

### Requirement: A panic HookContainerPanic raises for a check reaches the waiting callers
A panic that `HookContainerPanic` raises for a health check SHALL be raised again on the goroutine of every caller waiting for that check, which can recover it. When no caller waits for the check, it SHALL be raised on the goroutine running the check.

#### Scenario: A panic HookContainerPanic raises reaches the waiting callers
```gherkin
Given an injector with a manual HealthScheduler, started, with HookContainerPanic that panics again with the value it receives
And a service "db" whose HealthCheck() panics with "boom"
When the consumer calls <entry> under a deferred recover()
Then the recovered value is "boom"
And the process keeps running

Examples:
  | entry                            |
  | HealthCheckNamed(injector, "db") |
  | HealthCheck() on the root scope  |
  | HealthReport() on the root scope |
  | the stored round                 |
```

#### Scenario: With no caller waiting, the hook's panic is raised on the check's goroutine
```gherkin
Given a child process running an injector that is never started, with HookContainerPanic that panics again with the value it receives
And a service "db" whose HealthCheck() blocks until released, ignoring its context, then panics with "boom"
When the consumer calls HealthCheckNamedWithContext with a context it then cancels, the call returns, and the check is released
Then the child process exits with a non-zero status and its output starts with "panic: boom"
```

<!-- cases: N/A 1 default: HookContainerPanic is unset by default -->
<!-- cases: 2 overrides: a HookContainerPanic that panics again -->
<!-- cases: N/A 3 zero values: a nil hook keeps the error outcome (previous requirement) -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: the hook's panic replaces the error outcome for the waiting callers -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: forced, single-service, report and round paths -->
<!-- cases: 8 concurrency: every waiter of a shared check gets the panic on its own goroutine; with none, the check's goroutine -->
<!-- cases: 9 panics: this requirement (review R4) -->
<!-- cases: 10 backward compatibility: on 76404ef a single-service check without a deadline panicked on the caller's goroutine, where it could be recovered; this keeps that possible -->

### Requirement: A health check never holds a service's lock
A lazy service's health check SHALL run without holding that service's lock, so a check in flight SHALL NOT delay the service's shutdown or invocation. Root Shutdown SHALL NOT wait for a check in flight either: a check may run while its service shuts down.

#### Scenario: A check in flight does not delay the service
```gherkin
Given an injector with HealthCheckTimeout 5ms and an invoked lazy service "db" whose HealthCheck() blocks until released and ignores any context
And a HealthCheck() call on the root scope returned a timeout for "db", so the check is still in flight
When the consumer calls <action>
Then the call returns within 1s

Examples:
  | action                     |
  | Shutdown() on the injector |
  | Invoke[*DB](injector)      |
```

<!-- cases: N/A 1 default: no option -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: not applicable -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: shutdown during a check (regression D3) -->
<!-- cases: 8 concurrency: the lock is not held across the check -->
<!-- cases: N/A 9 panics: previous requirement -->
<!-- cases: 10 backward compatibility: results unchanged; a check may now run while its service shuts down, as eager services already can (flagged) -->

### Requirement: Health checks never wait for a lazy service being built
Deciding whether a service is a health checker, on any path, SHALL NOT wait for a lazy service whose provider is running: such a service SHALL count as not yet built.

#### Scenario: A provider in progress does not block health checks
```gherkin
Given an injector with a lazy service "slow" whose provider blocks until released, being invoked by another goroutine
And a Healthchecker value "db"
When the consumer calls <entry>
Then the call returns within 1s with <result>

Examples:
  | entry                                                         | result                         |
  | HealthCheckWithContext on the root scope with a 20ms context  | {"slow": nil, "db": nil}       |
  | HealthReport() on the root scope                              | "db" listed, "slow" not listed |
```

<!-- cases: N/A 1 default: no option -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: not applicable -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: a build in progress -->
<!-- cases: 8 concurrency: no wait on the provider's lock -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: on 76404ef the call blocks in the queued goroutine's checker detection until the provider returns, ignoring its context (regression D9) -->

### Requirement: Each underlying check runs once per call
A scope-wide run SHALL check each underlying service once. It SHALL NOT check an ancestor's service shadowed by a nearer scope's service of the same name, nor a default that yielded to another service. An alias or export SHALL NOT check its source again in a run that also checks the source: its entry, when the map has one, SHALL get the source's result.

#### Scenario: Each underlying check runs once
```gherkin
Given an injector that is never started, with <registrations>, whose checks count their calls
When the consumer calls HealthCheck() on <target>
Then the map is <map>, where name(T) is the service name inferred for type T
And the counts are <counts>

Examples:
  | registrations                                                                 | target         | map                                        | counts   |
  | root/"svc" (A) and "child"/"svc" (B)                                          | scope "child"  | {"svc": nil}                               | A 0, B 1 |
  | root/"svc" (A) and "child"/"svc" (B)                                          | the root scope | {"svc": nil}                               | A 1, B 0 |
  | root *DB value (A) with As[*DB, Healthchecker]                                | the root scope | {name(*DB): nil, name(Healthchecker): nil} | A 1      |
  | root *DB value (A) returning errors.New("down") with As[*DB, Healthchecker]   | the root scope | both entries the same "down" error         | A 1      |
  | "child"/"svc" (A) exported to the root with ExportNamed                       | the root's own *Scope | {"svc": nil}                        | A 1      |
  | "child"/"svc" (A) exported to the root with ExportNamed                       | scope "child"  | {"svc": nil}                               | A 1      |
  | "child"/"svc" (A) registered AsDefault, and root/"svc" (B)                    | scope "child"  | {"svc": nil}                               | A 0, B 1 |
```

<!-- cases: N/A 1 default: no option -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: 4 boundaries: shadowing seen from the child and from the root -->
<!-- cases: 5 errors: the alias shares the target's error -->
<!-- cases: 6 not found/duplicate: shadowing, aliasing, exporting and yielded defaults are the duplicates (regressions D4, D5; export and default rows keep the behavior of scope-export and default-registrations, verified on 76404ef) -->
<!-- cases: 7 lifecycle: child scopes -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: map keys and values are unchanged; only the number of calls drops -->

### Requirement: Forced results keep their existing shape
`HealthCheck()` / `HealthCheckWithContext()` on a `Scope` SHALL keep returning a `map[string]error` keyed by service name over the scope and its ancestors, with a nil entry for each service that is not a health checker. On a `RootScope` they SHALL cover the root scope's services and those of installed extension scopes, keyed `<scope name>/<service name>` as the `extensions` capability specifies, and no other child scope.

#### Scenario: Map shape is unchanged
```gherkin
Given an injector that is never started, with a plain value root/"answer" = 42, a Healthchecker root/"db", a Healthchecker "child"/"cache"
And an extension "redis" installed with Extend whose private scope holds a Healthchecker "pool", not exported
When the consumer calls HealthCheck() on <target>
Then the map is <map>

Examples:
  | target         | map                                                          |
  | the RootScope  | {"answer": nil, "db": nil, "ext:redis/pool": nil}            |
  | scope "child"  | {"answer": nil, "db": nil, "cache": nil}                     |
```

<!-- cases: 1 default: zero configuration -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: 3 zero values: the nil entry for a plain value -->
<!-- cases: 4 boundaries: root and child; an extension scope -->
<!-- cases: N/A 5 errors: unchanged -->
<!-- cases: N/A 6 not found/duplicate: previous requirement -->
<!-- cases: 7 lifecycle: nested scopes and extension scopes -->
<!-- cases: N/A 8 concurrency: covered by the in-flight requirement -->
<!-- cases: N/A 9 panics: a previous requirement -->
<!-- cases: 10 backward compatibility: this requirement; the RootScope.HealthCheck doc comment is corrected to state this coverage (D8) -->
