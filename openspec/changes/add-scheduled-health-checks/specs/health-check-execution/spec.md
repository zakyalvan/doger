# Spec Delta

## Purpose

Defines how the container runs health checks on every path (scheduled rounds, report reads, `HealthCheck()` and the single-service functions): sharing work between concurrent callers, bounding each caller's wait, limiting concurrency, containing panics and cycles, and never letting a health check block registration, building or shutdown.

## ADDED Requirements

### Requirement: Concurrent forced runs on a scope share one run
A `HealthCheck()` or `HealthCheckWithContext(ctx)` call made while another such call on the same scope is running SHALL join that run instead of starting its own, and SHALL receive the same results in a map of its own. A call made after the run finished SHALL start a new run.

#### Scenario: Concurrent calls share the run
```gherkin
Given an injector that is never started, with 10 Healthchecker values "svc-0" to "svc-9" that count their calls
And "svc-0"'s check blocks until released
When 50 goroutines call HealthCheck() on the root scope, and "svc-0" is released once all 50 wait
Then each service's health check was called 1 time
And every call returns a map with 10 entries, each nil
And deleting a key from one returned map leaves the others with 10 entries
```

#### Scenario: Sequential calls run again
```gherkin
Given an injector that is never started, with a Healthchecker "db" that counts its calls
When the consumer calls HealthCheck() on the root scope <n> times, one after another
Then the health check of "db" was called <n> times

Examples:
  | n |
  | 1 |
  | 3 |
```

#### Scenario: Runs on different scopes are separate
```gherkin
Given an injector with Healthchecker values root/"db" and "child"/"cache", whose checks count their calls and block until released
When one goroutine calls HealthCheck() on the root scope and another on scope "child", and the checks are released once both wait
Then the root call returns {"db": nil}
And the child call returns {"db": nil, "cache": nil}
And the health check of root/"db" was called 1 time
```

#### Scenario: A joined run never shows a missing entry
```gherkin
Given an injector that is never started, with a plain value "answer" = 42 and a Healthchecker "db" whose check blocks until released
And one goroutine is running HealthCheck() on the root scope
When the consumer calls HealthCheckWithContext with an already-cancelled context, 1000 times, each joining that run
Then every call returns a map whose entry for "answer" is nil and whose entry for "db" matches ErrHealthCheckTimeout
```

<!-- cases: 1 default: zero configuration -->
<!-- cases: N/A 2 overrides: coalescing has no option (see proposal Defaults & Control) -->
<!-- cases: 3 zero values: a scope with no service returns an empty map, as today; a plain value keeps its nil entry -->
<!-- cases: 4 boundaries: 1 and 50 callers; 1 and 3 sequential calls; 1000 joins -->
<!-- cases: N/A 5 errors: errors are per service, unchanged -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: root and child scopes -->
<!-- cases: 8 concurrency: 50 concurrent callers; isolated result maps; joining with an ended context -->
<!-- cases: N/A 9 panics: a later requirement -->
<!-- cases: 10 backward compatibility: return type and keys unchanged -->

### Requirement: Each caller's wait is bounded by its own context and its own timeout
Every caller SHALL stop waiting when its context ends or its own path's bound elapses, measured from its call: `HealthCheckTimeout` per service and `HealthCheckGlobalTimeout` for `HealthCheck()`; `HealthCheckSingleTimeout` (0 means `HealthCheckTimeout`) for the single-service functions. For each unfinished service it SHALL get an error matching `ErrHealthCheckTimeout` and the cause, whoever started the check.

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

<!-- cases: 1 default: no bound at zero configuration; the waiting rows -->
<!-- cases: 2 overrides: HealthCheckTimeout, HealthCheckSingleTimeout, HealthCheckGlobalTimeout and their interplay -->
<!-- cases: 3 zero values: 0 inherits or means no bound; a nil or background context waits -->
<!-- cases: 4 boundaries: one millisecond before and exactly at the bound -->
<!-- cases: 5 errors: the timeout message and errors.Is matches of health-check-timeouts -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: joining a check another caller started -->
<!-- cases: 8 concurrency: two callers with different contexts -->
<!-- cases: N/A 9 panics: a later requirement -->
<!-- cases: 10 backward compatibility: each path keeps the bounds of health-check-timeouts and of HealthCheckSingleTimeout from consolidate-pending-lifecycle-work -->

### Requirement: A check sees its starter's context
A check SHALL receive a context carrying the values and the deadline of the caller that started it, further bounded by that caller's own timeout. The context SHALL be cancelled when that bound elapses, when every caller waiting for the check stopped waiting, or when root shutdown begins. A caller that joins a check SHALL NOT change the check's context.

#### Scenario: Deadline and values reach the check
```gherkin
Given an injector that is never started, with a HealthcheckerWithContext "c" that records whether ctx.Deadline() is set and the value of key "trace"
When the consumer calls <entry> with a context carrying "trace" = "trace-1" and a deadline 1 minute ahead
Then the check recorded a deadline and "trace-1"

Examples:
  | entry                                  |
  | HealthCheckWithContext on the root scope |
  | HealthCheckNamedWithContext for "c"    |
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
While a service's check is running, any path that would check it SHALL wait for that check instead of starting another. A check that reached its bound, or that every caller left, SHALL stay in flight until it returns. A caller arriving then SHALL NOT receive its result: it SHALL wait, within its own bound, for that check to return, then start a fresh one.

#### Scenario: A check that ignores its context does not pile up
```gherkin
Given an injector that is never started, with HealthCheckTimeout 5ms
And a service "db" whose HealthCheck() blocks until released and ignores any context
When the consumer calls <entry> 100 times, one after another
Then every call returns "DI: health check timeout: context deadline exceeded" for "db"
And the health check of "db" was called 1 time
And after the check is released and the calls end, goleak reports no leaked goroutine

Examples:
  | entry                                  |
  | HealthCheck() on the root scope        |
  | HealthCheckNamed(injector, "db")       |
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

#### Scenario: After a check finishes, the next call checks again
```gherkin
Given an injector that is never started, with a Healthchecker "db" that counts its calls
When the consumer calls HealthCheckNamed(injector, "db") 2 times, one after another
Then the health check of "db" was called 2 times
```

<!-- cases: 1 default: zero configuration -->
<!-- cases: 2 overrides: with and without HealthCheckTimeout -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: 4 boundaries: 1, 2 and 100 calls -->
<!-- cases: 5 errors: a joining caller gets the check's error; an abandoned check's cancellation never reaches a new caller -->
<!-- cases: 6 not found/duplicate: an unknown name still returns ErrServiceNotFound, as today -->
<!-- cases: 7 lifecycle: joining, abandonment, and a fresh check after the old one returns -->
<!-- cases: 8 concurrency: never two checks at once; goleak -->
<!-- cases: N/A 9 panics: a later requirement -->
<!-- cases: 10 backward compatibility: sequential calls still check each time -->

### Requirement: HealthCheckParallelism caps running top-level checks
With `HealthCheckParallelism` n > 0, at most n top-level checks SHALL run at once across all paths. Waiting for a slot SHALL honor each caller's context and bound, SHALL NOT hold any scope lock, and SHALL have no queue limit. A check's own timeout SHALL start when it starts running. With 0 (default), checks SHALL NOT be limited.

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
And a service "hung" whose HealthCheck() blocks until released and ignores any context, already running from a HealthCheckNamed call
And a Healthchecker value "db"
When the consumer calls HealthCheckNamedWithContext with a context that times out after 20ms, for "db"
Then the call returns within 1s, with an error matching ErrHealthCheckTimeout and context.DeadlineExceeded
```

#### Scenario: Registration is not blocked by checks waiting for a slot
```gherkin
Given an injector that is never started, with HealthCheckParallelism 1
And a service "hung" whose HealthCheck() blocks until released and ignores any context, already running
And 1001 goroutines each calling HealthCheckNamed for a different Healthchecker "svc-0" to "svc-1000", all waiting for the slot
When the consumer calls ProvideNamedValue(injector, "late", 42)
Then the call returns within 1s
```

#### Scenario: A scheduled check's timeout does not count the wait for a slot
```gherkin
Given a started injector with a fake clock, HealthCheckParallelism 1, HealthCheckTimeout 0 and Healthcheckers "a" and "b"
And "a"'s check blocks on the fake clock for 15s, then returns nil
When the fake clock advances by 15s, then by 1s
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
<!-- cases: 5 errors: the timeout error while waiting for a slot -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: a scheduled check waiting for its slot -->
<!-- cases: 8 concurrency: slot waits, scope locks, registration -->
<!-- cases: N/A 9 panics: a later requirement -->
<!-- cases: 10 backward compatibility: the default stays unlimited; a positive value now also limits the single-service functions (flagged) -->

### Requirement: Creating a container starts no goroutine
`New()`, `NewWithOpts()`, `Clone()` and `CloneWithOpts()` SHALL NOT start a goroutine, whatever `HealthCheckParallelism` is.

#### Scenario: New starts no goroutine
```gherkin
Given HealthCheckParallelism <n>
When the consumer calls NewWithOpts with it and registers a Healthchecker value "db"
Then the number of goroutines is unchanged

Examples:
  | n |
  | 0 |
  | 4 |
```

<!-- cases: 1 default: 0 -->
<!-- cases: 2 overrides: a positive parallelism (formerly started 4 pool goroutines) -->
<!-- cases: N/A 3 zero values: covered by n = 0 -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: none -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: construction; the scheduler is in health-check-scheduling -->
<!-- cases: 8 concurrency: goroutine count -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: 10 backward compatibility: New() no longer starts pool goroutines (an improvement) -->

### Requirement: A check started from inside a check never waits for a slot
A health check called from inside a running check SHALL run on its caller's goroutine without taking a slot. When that service's check is waiting for a slot, the nested call SHALL run it at once, and every caller waiting for it SHALL get that result.

#### Scenario: Nested checks complete under a limit of one
```gherkin
Given an injector that is never started, with HealthCheckParallelism 1
And a Healthchecker value "leaf" returning nil
And a HealthcheckerWithContext "agg" whose check returns HealthCheckNamedWithContext(ctx, injector, "leaf")
When the consumer calls <entry>
Then the call returns within 1s with <result>

Examples:
  | entry                           | result                       |
  | HealthCheck() on the root scope | {"agg": nil, "leaf": nil}    |
  | HealthCheckNamed(injector, "agg") | nil                        |
```

<!-- cases: 1 default: an unlimited container runs nested checks as today -->
<!-- cases: 2 overrides: a limit of 1, the tightest -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: 4 boundaries: limit 1 -->
<!-- cases: N/A 5 errors: cycles are the next requirement -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: a nested call taking over a check waiting for its slot -->
<!-- cases: 8 concurrency: no deadlock under the limit -->
<!-- cases: N/A 9 panics: a later requirement -->
<!-- cases: 10 backward compatibility: composite checks keep working as today -->

### Requirement: A check that depends on itself returns a cycle error
A health check called, directly or through other checks, from inside its own running check SHALL return an error matching `ErrHealthCheckCycle`, whose message is ``DI: health check cycle on service `<name>` ``, instead of waiting for itself.

#### Scenario: Self-referencing checks
```gherkin
Given an injector that is never started
And <services>
When the consumer calls HealthCheckNamed(injector, "a")
Then the call returns within 1s
And the result is an error that matches ErrHealthCheckCycle and contains "<message>"

Examples:
  | services                                                                         | message                                  |
  | a HealthcheckerWithContext "a" whose check returns HealthCheckNamedWithContext(ctx, injector, "a") | DI: health check cycle on service `a` |
  | "a" whose check calls "b", and "b" whose check calls "a" (both with ctx)          | DI: health check cycle on service `a`    |
```

<!-- cases: N/A 1 default: a cycle is never the default path -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: 4 boundaries: a direct and a two-step cycle -->
<!-- cases: 5 errors: ErrHealthCheckCycle and its message -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: N/A 7 lifecycle: not applicable -->
<!-- cases: 8 concurrency: no self-wait -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: today such a check recurses without limit (probe: depth 1001 reached under a guard); detection needs the check to pass on its ctx -->

### Requirement: A panicking health check returns an error
A panic in a health check SHALL be recovered on every path. The result SHALL be an error matching `ErrHealthCheckPanic`, with the message `DI: health check panic: ` followed by the value; an error value SHALL also match, and a nil value SHALL match `*runtime.PanicNilError` with `errors.As`. When set, `HookContainerPanic(root, "healthcheck", value)` SHALL be called; a panic it raises SHALL propagate.

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

#### Scenario: A panic in a scheduled round does not stop scheduling
```gherkin
Given a started injector with a fake clock, HealthCheckInterval 10s and a service "db" whose HealthCheck() panics with "boom" on its first call and returns nil afterwards
And a HookServiceHealthChanged hook that records calls
When the fake clock advances by 10s and the round finishes
Then the report entry for "db" is HealthHealthy
And the hook recorded (Unknown, Unhealthy, "DI: health check panic: boom") then (Unhealthy, Healthy, nil)
```

<!-- cases: N/A 1 default: panics are not a default path -->
<!-- cases: 2 overrides: with and without HookContainerPanic -->
<!-- cases: 3 zero values: a nil panic value -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: 5 errors: message, ErrHealthCheckPanic, the wrapped error value, PanicNilError -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: forced, single-service, report and scheduled paths -->
<!-- cases: N/A 8 concurrency: recovery happens on the goroutine running the check -->
<!-- cases: 9 panics: this requirement; a panic raised by HookContainerPanic propagates so a consumer can still crash on purpose -->
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
<!-- cases: 7 lifecycle: shutdown during a check -->
<!-- cases: 8 concurrency: the lock is not held across the check -->
<!-- cases: N/A 9 panics: previous requirement -->
<!-- cases: 10 backward compatibility: results unchanged; a check may now run while its service shuts down, as eager services already can (flagged) -->

### Requirement: Listing services never waits for a lazy service being built
Finding which services are health checkers SHALL NOT wait for a lazy service whose provider is running: such a service SHALL count as not yet built.

#### Scenario: A provider in progress does not block health checks
```gherkin
Given an injector with a lazy service "slow" whose provider blocks until released, being invoked by another goroutine
And a Healthchecker value "db"
When the consumer calls <entry>
Then the call returns within 1s with <result>

Examples:
  | entry                                                         | result                       |
  | HealthCheckWithContext on the root scope with a 20ms context  | {"slow": nil, "db": nil}     |
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
<!-- cases: 10 backward compatibility: today the call blocks until the provider returns, ignoring its context (probe, defect D9) -->

### Requirement: Shadowed and aliased services are checked once per run
A scope-wide run SHALL NOT check an ancestor's service shadowed by a service of the same name in a nearer scope. An alias SHALL NOT check its target again in a run that also checks the target: the alias's entry SHALL get the target's result.

#### Scenario: Each underlying check runs once
```gherkin
Given an injector that is never started, with <registrations>, whose checks count their calls
When the consumer calls HealthCheck() on <target>
Then the map is <map>, where name(T) is the service name inferred for type T
And the counts are <counts>

Examples:
  | registrations                                                               | target         | map                                        | counts   |
  | root/"svc" (A) and "child"/"svc" (B)                                        | scope "child"  | {"svc": nil}                               | A 0, B 1 |
  | root/"svc" (A) and "child"/"svc" (B)                                        | the root scope | {"svc": nil}                               | A 1, B 0 |
  | root *DB value (A) with As[*DB, Healthchecker]                              | the root scope | {name(*DB): nil, name(Healthchecker): nil} | A 1      |
  | root *DB value (A) returning errors.New("down") with As[*DB, Healthchecker] | the root scope | both entries the same "down" error         | A 1      |
```

<!-- cases: N/A 1 default: no option -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: 4 boundaries: shadowing seen from the child and from the root -->
<!-- cases: 5 errors: the alias shares the target's error -->
<!-- cases: 6 not found/duplicate: shadowing and aliasing are the duplicates -->
<!-- cases: 7 lifecycle: child scopes -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: map keys and values are unchanged; only the number of calls drops -->

### Requirement: Forced results keep their existing shape
`HealthCheck()` / `HealthCheckWithContext()` SHALL keep returning a `map[string]error` keyed by service name over the scope and its ancestors, with a nil entry for each service that is not a health checker. On a `RootScope` they SHALL cover the root scope's services only, as the corrected doc comment states.

#### Scenario: Map shape is unchanged
```gherkin
Given an injector that is never started, with a plain value root/"answer" = 42, a Healthchecker root/"db" and a Healthchecker "child"/"cache"
When the consumer calls HealthCheck() on <target>
Then the map is <map>

Examples:
  | target         | map                                      |
  | the RootScope  | {"answer": nil, "db": nil}               |
  | scope "child"  | {"answer": nil, "db": nil, "cache": nil} |
```

<!-- cases: 1 default: zero configuration -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: 3 zero values: the nil entry for a plain value -->
<!-- cases: 4 boundaries: root and child -->
<!-- cases: N/A 5 errors: unchanged -->
<!-- cases: N/A 6 not found/duplicate: previous requirement -->
<!-- cases: 7 lifecycle: nested scopes -->
<!-- cases: N/A 8 concurrency: first requirement -->
<!-- cases: N/A 9 panics: a previous requirement -->
<!-- cases: 10 backward compatibility: this requirement -->
