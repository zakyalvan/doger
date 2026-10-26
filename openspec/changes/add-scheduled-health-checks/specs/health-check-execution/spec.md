# Spec Delta

## Purpose

Defines how the container runs health checks on every path (scheduled rounds, on-demand reads, `HealthCheck()` and the single-service functions): sharing work between concurrent callers, limiting concurrency, containing panics, and never letting a health check block registration or shutdown.

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
When one goroutine calls HealthCheck() on the root scope and another on scope "child" concurrently, and the checks are released once both wait
Then the root call returns {"db": nil}
And the child call returns {"db": nil, "cache": nil}
And the health check of root/"db" was called 1 time
```

<!-- cases: 1 default: zero configuration -->
<!-- cases: N/A 2 overrides: coalescing has no option (see proposal Defaults & Control) -->
<!-- cases: 3 zero values: a scope with no service returns an empty map, as today -->
<!-- cases: 4 boundaries: 1 and 50 callers; 1 and 3 sequential calls -->
<!-- cases: N/A 5 errors: errors are per service, unchanged -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: root and child scopes -->
<!-- cases: 8 concurrency: 50 concurrent callers; isolated result maps -->
<!-- cases: N/A 9 panics: next requirements -->
<!-- cases: 10 backward compatibility: return type and keys unchanged -->

### Requirement: Each caller waits only as long as its own context allows
A caller whose context ends while a shared run or check is in flight SHALL stop waiting and SHALL get, for each service not finished, an error matching `ErrHealthCheckTimeout` and its context's error. A check's context SHALL be cancelled only when every caller waiting for it stopped waiting, or when its own timeout elapses.

#### Scenario: One caller leaving does not cancel the check for the others
```gherkin
Given an injector that is never started, with a service "db" whose HealthCheck(ctx) blocks until released or ctx is done, then returns ctx.Err() or nil
When caller A calls HealthCheckWithContext with a context cancelled after 10ms, caller B calls HealthCheck() at the same time, A's context is cancelled, then the check is released
Then A's result for "db" is "DI: health check timeout: context canceled"
And B's result for "db" is nil
```

#### Scenario: The only caller leaving cancels the check
```gherkin
Given an injector that is never started, with a service "db" whose HealthCheck(ctx) blocks until ctx is done, then records and returns ctx.Err()
When the only caller calls HealthCheckWithContext with a context cancelled after 10ms
Then the call returns "DI: health check timeout: context canceled" for "db"
And the check recorded context.Canceled
```

<!-- cases: N/A 1 default: waiting has no option -->
<!-- cases: 2 overrides: HealthCheckTimeout and HealthCheckGlobalTimeout still bound the run, as in health-check-timeouts -->
<!-- cases: 3 zero values: a nil or background context waits for the result -->
<!-- cases: 4 boundaries: one caller vs several callers -->
<!-- cases: 5 errors: the timeout message and errors.Is matches of health-check-timeouts -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: N/A 7 lifecycle: not applicable -->
<!-- cases: 8 concurrency: this requirement -->
<!-- cases: N/A 9 panics: next requirement -->
<!-- cases: 10 backward compatibility: a single caller sees today's behavior, including the timeout scenarios of health-check-timeouts -->

### Requirement: A service never has two checks in flight
While a service's check is in flight, any path that would check it again (a round, an on-demand read, a forced run, or `HealthCheck[T]`, `HealthCheckWithContext[T]`, `HealthCheckNamed`, `HealthCheckNamedWithContext`) SHALL wait for that check's result instead of starting another. Each service SHALL have at most one goroutine running its check.

#### Scenario: A check that ignores its context does not pile up
```gherkin
Given an injector that is never started, with HealthCheckTimeout 5ms
And a service "db" whose HealthCheck() blocks until released and ignores any context
When the consumer calls <entry> 100 times, one after another
Then every call returns "DI: health check timeout: context deadline exceeded" for "db"
And the health check of "db" was called 1 time
And the container runs at most 1 goroutine for checks of "db"

Examples:
  | entry                                  |
  | HealthCheck() on the root scope        |
  | HealthCheckNamed(injector, "db")       |
```

#### Scenario: A call that joins an in-flight check gets its result
```gherkin
Given a started injector with a fake clock and a service "db" whose first check blocks until released, then returns errors.New("down")
And the first round's check of "db" is in flight
When the consumer calls HealthCheckNamed(injector, "db"), then the check is released
Then the call returns the same "down" error
And the health check of "db" was called 1 time
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
<!-- cases: 4 boundaries: 1 and 100 calls -->
<!-- cases: 5 errors: the joining caller gets the in-flight check's error -->
<!-- cases: 6 not found/duplicate: an unknown name still returns ErrServiceNotFound, as today -->
<!-- cases: 7 lifecycle: joining a scheduled check -->
<!-- cases: 8 concurrency: the goroutine bound -->
<!-- cases: N/A 9 panics: next requirement -->
<!-- cases: 10 backward compatibility: sequential calls still check each time -->

### Requirement: HealthCheckParallelism limits checks without blocking callers or registration
With `HealthCheckParallelism` n > 0, at most n checks SHALL run at once across all paths. Waiting for a slot SHALL honor the caller's context and the timeout options, SHALL NOT hold any scope lock, and SHALL have no queue limit. `New()` and `NewWithOpts()` SHALL NOT start a goroutine. With 0 (default), checks SHALL NOT be limited.

#### Scenario: Parallelism caps running checks
```gherkin
Given an injector that is never started, with HealthCheckParallelism <n>
And 20 Healthchecker values whose checks record the number running at once and block 1ms on the injector's clock
When the consumer calls HealthCheck() on the root scope
Then the highest number running at once is at most <max>
And every service's result is nil

Examples:
  | n           | max |
  | 1           | 1   |
  | 4           | 4   |
  | 0 (default) | 20  |
```

#### Scenario: Waiting for a slot honors the context
```gherkin
Given an injector that is never started, with HealthCheckParallelism 1
And a service "hung" whose HealthCheck() blocks until released and ignores any context, already running from another HealthCheckNamed call
And a Healthchecker value "db"
When the consumer calls HealthCheckNamedWithContext with a context that times out after 20ms, for "db"
Then the call returns within 1s, with an error matching ErrHealthCheckTimeout and context.DeadlineExceeded
```

#### Scenario: Registration is not blocked by checks waiting for a slot
```gherkin
Given an injector that is never started, with HealthCheckParallelism 1
And a service "hung" whose HealthCheck() blocks until released and ignores any context
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

#### Scenario: New starts no goroutine
```gherkin
Given HealthCheckParallelism <n>
When the consumer calls NewWithOpts with it
Then the number of goroutines is unchanged

Examples:
  | n |
  | 0 |
  | 4 |
```

<!-- cases: 1 default: unlimited -->
<!-- cases: 2 overrides: n = 1 and n = 4 -->
<!-- cases: 3 zero values: 0 means unlimited -->
<!-- cases: 4 boundaries: 1, 4 and 2000 services against a limit of 1 (the former 1000-slot queue) -->
<!-- cases: 5 errors: the timeout error while waiting for a slot -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: no goroutine before Start -->
<!-- cases: 8 concurrency: slot waits, scope locks, registration -->
<!-- cases: N/A 9 panics: next requirement -->
<!-- cases: 10 backward compatibility: the default stays unlimited; a positive value still caps concurrency, now also for the single-service functions (flagged) -->

### Requirement: A panicking health check returns an error
A panic in a service's health check SHALL be recovered on every path. The check's result SHALL be an error matching `ErrHealthCheckPanic`, whose message is `DI: health check panic: ` followed by the value. An error value SHALL also match with `errors.Is`, and a nil value SHALL match `*runtime.PanicNilError` with `errors.As`. The value SHALL also be passed to `HookContainerPanic(root, "healthcheck", value)` when set.

#### Scenario: Panic results
```gherkin
Given an injector with HookContainerPanic <panic hook>
And a service "db" whose HealthCheck() panics with <value>
When the consumer calls <entry>
Then the result for "db" has the message "<message>" and matches ErrHealthCheckPanic with errors.Is
And it also matches <match>
And <observed>

Examples:
  | panic hook                     | value                | entry                           | message                                                    | match                                          | observed                                               |
  | nil                            | "boom"               | HealthCheck() on the root scope | DI: health check panic: boom                               | nothing else                                   | the process keeps running                              |
  | nil                            | io.EOF               | HealthCheckNamed(injector, "db")| DI: health check panic: EOF                                | io.EOF with errors.Is                          | the process keeps running                              |
  | nil                            | nil                  | HealthReport() on an idle root  | DI: health check panic: panic called with nil argument (goexit=false) | *runtime.PanicNilError with errors.As | the process keeps running                              |
  | set, recording (point, value)  | "boom"               | HealthCheck() on the root scope | DI: health check panic: boom                               | nothing else                                   | HookContainerPanic recorded ("healthcheck", "boom")     |
```

#### Scenario: A panic in a scheduled round does not stop scheduling
```gherkin
Given a started injector with a fake clock, HealthCheckInterval 10s and a service "db" whose HealthCheck() panics with "boom" on its first call and returns nil afterwards
When the fake clock advances by 10s and the round finishes
Then the report entry for "db" is HealthHealthy
And a HookServiceHealthChanged hook recorded (Unknown, Unhealthy, "DI: health check panic: boom") then (Unhealthy, Healthy, nil)
```

<!-- cases: N/A 1 default: panics are not a default path -->
<!-- cases: 2 overrides: with and without HookContainerPanic -->
<!-- cases: 3 zero values: a nil panic value -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: 5 errors: message, ErrHealthCheckPanic, the wrapped error value, PanicNilError -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: forced, single-service, on-demand and scheduled paths -->
<!-- cases: N/A 8 concurrency: recovery happens on the check's own goroutine -->
<!-- cases: 9 panics: this requirement -->
<!-- cases: 10 backward compatibility: a crash becomes an error (flagged); a single-service panic used to reach the caller as a panic -->

### Requirement: A health check never holds a service's lock
A lazy service's health check SHALL run without holding that service's lock, so a check in flight SHALL NOT delay the service's shutdown, invocation or explanation.

#### Scenario: A check in flight does not delay the service
```gherkin
Given an injector with HealthCheckTimeout 5ms and an invoked lazy service "db" whose HealthCheck() blocks until released and ignores any context
And a HealthCheck() call on the root scope returned a timeout for "db", so the check is still in flight
When the consumer calls <action>
Then the call returns within 1s

Examples:
  | action                                  |
  | Shutdown() on the injector              |
  | Invoke[*DB](injector)                   |
  | ExplainNamedService(injector, "db")     |
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
<!-- cases: 10 backward compatibility: results unchanged; only waiting is removed -->

### Requirement: Shadowed and aliased services are checked once per run
A scope-wide run SHALL NOT check an ancestor's service shadowed by a service of the same name in a nearer scope. An alias SHALL NOT check its target again in a run that also checks the target: the alias's entry SHALL get the target's result.

#### Scenario: Each underlying check runs once
```gherkin
Given an injector that is never started, with <registrations>, whose checks count their calls
When the consumer calls HealthCheck() on <target>
Then the map is <map>, where name(T) is the service name inferred for type T
And the counts are <counts>

Examples:
  | registrations                                                      | target              | map                                   | counts                    |
  | root/"svc" (A) and "child"/"svc" (B)                               | scope "child"       | {"svc": nil}                          | A 0, B 1                  |
  | root/"svc" (A) and "child"/"svc" (B)                               | the root scope      | {"svc": nil}                          | A 1, B 0                  |
  | root *DB value (A) with As[*DB, Healthchecker]                     | the root scope      | {name(*DB): nil, name(Healthchecker): nil} | A 1                       |
  | root *DB value (A) returning errors.New("down") with As[*DB, Healthchecker] | the root scope | both entries the same "down" error | A 1                    |
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
  | target         | map                                         |
  | the RootScope  | {"answer": nil, "db": nil}                  |
  | scope "child"  | {"answer": nil, "db": nil, "cache": nil}    |
```

<!-- cases: 1 default: zero configuration -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: 3 zero values: the nil entry for a plain value -->
<!-- cases: 4 boundaries: root and child -->
<!-- cases: N/A 5 errors: unchanged -->
<!-- cases: N/A 6 not found/duplicate: previous requirement -->
<!-- cases: 7 lifecycle: nested scopes -->
<!-- cases: N/A 8 concurrency: first requirement -->
<!-- cases: N/A 9 panics: previous requirement -->
<!-- cases: 10 backward compatibility: this requirement -->
