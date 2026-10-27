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
Given an injector that is never started, with HealthCheckTimeout 5ms
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
<!-- cases: 2 overrides: with and without HealthCheckTimeout -->
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

#### Scenario: A report-path check's timeout does not count the wait for a slot
```gherkin
Given an injector that is never started, with a fake clock, HealthCheckParallelism 1, HealthCheckTimeout 0 and Healthcheckers "a" and "b"
And "a"'s check blocks on the fake clock for 9s, then returns nil
When the consumer calls HealthReport() on the root scope and the fake clock advances by 9s, then by 1s
Then "b" is HealthHealthy in the report, not timed out
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
<!-- cases: 7 lifecycle: a report-path check waiting for its slot -->
<!-- cases: 8 concurrency: slot waits, scope locks, registration (regression D2) -->
<!-- cases: N/A 9 panics: a later requirement -->
<!-- cases: 10 backward compatibility: the default stays unlimited; single-service functions keep skipping the limit, as today -->

### Requirement: A single-service check never waits for a slot
A single-service function SHALL run its check at once, without a slot. When the service's check is queued for a slot, the single-service call SHALL run it at once, and every caller waiting for that check SHALL get its result. Composite checks therefore complete under any limit.

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

<!-- cases: 1 default: an unlimited container runs nested checks as today -->
<!-- cases: 2 overrides: a limit of 1, the tightest -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: 4 boundaries: limit 1 -->
<!-- cases: N/A 5 errors: not applicable -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: a single-service call taking over a check queued for a slot -->
<!-- cases: 8 concurrency: no deadlock under the limit -->
<!-- cases: N/A 9 panics: a later requirement -->
<!-- cases: 10 backward compatibility: composite checks keep working as today; a check calling itself still recurses as today (cycle detection is out of scope) -->

### Requirement: Creating a container starts no goroutine
`New()`, `NewWithOpts()`, `Clone()` and `CloneWithOpts()` SHALL NOT start a goroutine, whatever `HealthCheckParallelism` and `HealthScheduler` are.

#### Scenario: New starts no goroutine
```gherkin
Given HealthCheckParallelism <n> and HealthScheduler <scheduler>
When the consumer calls NewWithOpts with them and registers a Healthchecker value "db"
Then the number of goroutines is unchanged

Examples:
  | n | scheduler                                  |
  | 0 | nil                                        |
  | 4 | nil                                        |
  | 0 | a HealthScheduler that would start a goroutine when called |
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
A panic in a health check SHALL be recovered on every path. The result SHALL be an error matching `ErrHealthCheckPanic`, with the message `DI: health check panic: ` followed by the value. An error value SHALL also match, and a nil value SHALL match `*runtime.PanicNilError` with `errors.As`. When set, `HookContainerPanic(root, "healthcheck", value)` SHALL be called, and a panic it raises SHALL propagate.

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
  | nil                            | nil     | HealthReport() on an idle root   | DI: health check panic: panic called with nil argument (goexit=false)  | *runtime.PanicNilError with errors.As | the process keeps running                           |
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
<!-- cases: 9 panics: this requirement (regression D7); a panic raised by HookContainerPanic propagates so a consumer can still crash on purpose -->
<!-- cases: 10 backward compatibility: a crash becomes an error (flagged); a single-service panic used to reach the caller as a panic -->

### Requirement: A health check never holds a service's lock
A lazy service's health check SHALL run without holding that service's lock, so a check in flight SHALL NOT delay the service's shutdown or invocation.

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
