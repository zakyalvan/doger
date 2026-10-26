# Spec Delta

## MODIFIED Requirements

### Requirement: Start builds candidates and starts them in dependency order
`Start` SHALL build every candidate (Starters and services opted into any kind) before starting any Starter. A service's dependencies, invoked or declared with `DependsOn`, are built before it. It SHALL then start Starters one at a time, unless `InjectorOpts.StartParallelism` is greater than 1: a Starter starts only after every Starter it depends on, directly or transitively, has started. Ties are broken by scope depth, then registration order.

#### Scenario: Dependency-ordered build and start
```gherkin
Given a root scope recording provider calls and start calls
And lazy Starter "http" whose provider invokes "db"
And lazy Starter "db"
And lazy non-starter "repo" whose provider invokes "db"
And lazy Starter "consumer" whose provider invokes "repo"
When the consumer calls Start()
Then every provider call happens before the first start call
And the start order is db, http, consumer
And "repo" is built but not started
```

#### Scenario: Child scopes are started after their ancestors
```gherkin
Given a grandchild scope "jobs" under child scope "tenant-a" with lazy Starter "cron", registered first
And child scope "tenant-a" with lazy Starter "worker" invoking "db", registered second
And the root scope with lazy Starter "db", registered third
When the consumer calls Start() on the root scope
Then the start order is db, worker, cron
```

#### Scenario: Start on an empty container
```gherkin
Given a root scope with no registrations
When the consumer calls Start()
Then Start returns nil
```

<!-- cases: 2 overrides: ordering is overridden by declaring edges (dependency-declaration); concurrency by StartParallelism ("Parallel start keeps dependency order") -->
<!-- cases: N/A 4 boundaries: 0, 1 and many Starters are covered across scenarios -->
<!-- cases: 6 duplicates: a service reachable from several Starters is built once (lazy singleton semantics) -->
<!-- cases: 7 lifecycle: child scopes are covered by "Child scopes are started after their ancestors"; cycles by dependency-declaration ("Start rejects a cycle closed by a dynamic edge") -->
<!-- cases: 8 concurrency: Starters start sequentially by default; concurrent Invoke during Start is covered by "Concurrent Start and Invoke"; parallel start by its own requirement -->
<!-- cases: N/A 9 panics: covered by "Start errors and panics" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Registrations after Start are not started
Services registered after `Start` returned, and child scopes created after `Start` took its discovery snapshot, SHALL NOT be started or handled automatically. They remain invokable as usual, and `RootScope.Refresh` or `Scope.Start` starts them.

#### Scenario: Late registration
```gherkin
Given a root scope that has been started
When the consumer registers lazy Starter "late" and invokes it
Then "late" is built
And "late" was started 0 times
```

<!-- cases: 2 overrides: Refresh and Scope.Start start late registrations (their own requirements) -->
<!-- cases: N/A 3, 4, 5, 9: no inputs or failure modes beyond the scenario -->
<!-- cases: N/A 7 lifecycle: registrations made during Start (for example by a provider) are unspecified and are not covered by any guarantee -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent Start and Invoke" -->
<!-- cases: N/A 10 compatibility: new API -->

## ADDED Requirements

### Requirement: Refresh starts what is not started yet
`RootScope.Refresh()` / `RefreshWithContext(ctx)` SHALL run the start pipeline over the services and scopes not yet started or handled: build them, run kind handlers for their new components, and start their Starters, with the same order, context, error and per-service hook rules as `Start`. A service SHALL be started successfully at most once per container: a Starter whose build or Start failed is not recorded as started, so the next Refresh or Scope.Start that covers it SHALL try it again.

#### Scenario: Refresh starts late registrations and new scopes once
```gherkin
Given a root scope with lazy Starter "a" and the consumer called Start()
And then the consumer registers lazy Starter "late" in the root and creates child scope "tenant" with lazy Starter "worker"
When the consumer calls Refresh() <times>
Then every call returns nil
And the start counts are a: 1, late: 1, worker: 1
And the start order of the first Refresh is late, worker

Examples:
  | times |
  | once  |
  | twice |
```

#### Scenario: Refresh with nothing new
```gherkin
Given a root scope with lazy Starter "a" and the consumer called Start()
When the consumer calls Refresh()
Then Refresh returns nil
And "a" was started 1 time
```

#### Scenario: A Starter that failed is tried again
```gherkin
Given a started root container and lazy Starter "late", registered after Start in <scope>, whose <failing> returns errBoom on its first call and nil afterwards
When the consumer calls <operation> twice
Then the first call returns an error matching errBoom and the second returns nil
And "late"'s provider was called <builds> times and its Start <starts> times
When the consumer calls <operation> again
Then it returns nil
And "late"'s provider was called <builds> times and its Start <starts> times

Examples:
  | scope                                     | operation      | failing  | builds | starts |
  | the root                                  | root.Refresh() | Start    | 1      | 2      |
  | child scope "tenant", created after Start | tenant.Start() | Start    | 1      | 2      |
  | the root                                  | root.Refresh() | provider | 2      | 1      |
  | child scope "tenant", created after Start | tenant.Start() | provider | 2      | 1      |
```

<!-- cases: 1 default: Refresh after Start -->
<!-- cases: N/A 2 overrides: StartParallelism and context apply as for Start -->
<!-- cases: 3 zero values: nothing new; a nil context is treated as context.Background(), as for Start -->
<!-- cases: 4 boundaries: zero, one and several new services; repeated Refresh -->
<!-- cases: 5 errors: failures are wrapped as for Start ("Incremental start failures are StartError values"); a Starter whose Start or build failed is tried again ("A Starter that failed is tried again") -->
<!-- cases: 6 duplicates: a second Refresh starts nothing again; only a successful Start counts -->
<!-- cases: 7 lifecycle: before Start and after Shutdown are covered by "Incremental start needs a started container" -->
<!-- cases: 8 concurrency: covered by "Concurrent Refresh calls are serialized" -->
<!-- cases: 9 panics: a panicking Start method is recovered as for Start -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Scope.Start starts a scope subtree incrementally
`Scope.Start()` / `Scope.StartWithContext(ctx)` on a child scope SHALL do what `Refresh` does, restricted to that scope and its descendants. Ancestor services the subtree depends on SHALL be built as needed but SHALL NOT be started by it.

#### Scenario: Scope.Start starts only its subtree
```gherkin
Given a started root container
And child scopes "tenant-a" with lazy Starter "wa" and "tenant-b" with lazy Starter "wb", created after Start
And grandchild "jobs" under "tenant-a" with lazy Starter "cron"
And lazy Starter "late" registered in the root after Start
When the consumer calls tenant-a.Start()
Then it returns nil
And the started services are wa, cron
And "wb" and "late" were started 0 times
```

#### Scenario: Scope.Start builds ancestors but does not start them
```gherkin
Given a started root container and lazy Starter "db" registered in the root after Start
And child scope "tenant", created after Start, with lazy Starter "worker" whose provider invokes "db"
When the consumer calls tenant.Start()
Then it returns nil
And "worker" was started 1 time
And "db" is built and was started 0 times
```

#### Scenario: Scope.Start on the root's own scope
```gherkin
Given a started root container with lazy Starter "late" registered after Start
And s is the root's own *Scope, as a kind handler receives it in Component.Scope for a root service (a provider receives an Injector, not a *Scope)
When the consumer calls s.Start()
Then it returns nil
And "late" was started 1 time
```

<!-- cases: 1 default: subtree start -->
<!-- cases: N/A 2 overrides: as for Refresh -->
<!-- cases: N/A 3 zero values: an empty subtree returns nil (as "Refresh with nothing new") -->
<!-- cases: 4 boundaries: child with grandchild; sibling untouched; an ancestor dependency is built, not started -->
<!-- cases: N/A 5 errors: as for Refresh -->
<!-- cases: 6 duplicates: the root's own scope behaves as Refresh -->
<!-- cases: 7 lifecycle: before Start is covered by "Incremental start needs a started container" -->
<!-- cases: N/A 8 concurrency: Scope.Start and Refresh share the serialization of "Concurrent Refresh calls are serialized" -->
<!-- cases: N/A 9 panics: as for Start -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Incremental start needs a started container
`Refresh` and `Scope.Start` SHALL return an error matching `ErrNotStarted` ("DI: container not started") while the container's first `Start` has not succeeded, including while that `Start` is still running, and an error matching `ErrStartAfterShutdown` once root Shutdown has begun, which takes precedence. They SHALL decide this without waiting for a running `Start`, and SHALL start nothing in either case.

#### Scenario: Incremental start in the wrong phase
```gherkin
Given a root scope with lazy Starter "a" and child scope "tenant" with lazy Starter "w"
And the consumer has <previous>
When the consumer calls <operation>
Then the result matches <result>
And the operation started nothing ("w" is started by no Refresh or Scope.Start)

Examples:
  | previous                                                   | operation      | result                |
  | done nothing                                               | root.Refresh() | ErrNotStarted         |
  | done nothing                                               | tenant.Start() | ErrNotStarted         |
  | called Start(), which failed because a's provider returned errBuild | root.Refresh() | ErrNotStarted |
  | called Start() and then Shutdown()                         | root.Refresh() | ErrStartAfterShutdown |
  | called Shutdown() without Start()                          | tenant.Start() | ErrStartAfterShutdown |
```

#### Scenario: Incremental start while Start runs
```gherkin
Given a root scope with lazy Starter "a" and child scope "tenant" with lazy Starter "w"
And a's Start calls <operation> <how>, records its result, then returns nil
When the consumer calls Start()
Then Start returns nil
And the recorded result matches ErrNotStarted
And the recorded call returned before a's Start returned
And "w" was started 1 time, by Start

Examples:
  | operation      | how                                     |
  | root.Refresh() | synchronously                           |
  | tenant.Start() | synchronously                           |
  | root.Refresh() | in another goroutine, awaited by a's Start |
```

#### Scenario: A Refresh queued behind a Shutdown
```gherkin
Given a started root container with a HookContainerClosing hook that sleeps 100ms
And lazy Starter "late", registered after Start, whose Start(ctx) blocks until ctx is done and returns ctx.Err()
And a first root.Refresh() is running in another goroutine and "late" has entered Start
And a second root.Refresh() was called in another goroutine 50ms later, waiting for the first
When the consumer calls root.Shutdown()
Then both Refresh calls return an error matching ErrStartAfterShutdown
And "late"'s Start was called 1 time
```

<!-- cases: N/A 2 overrides: the phase rule has no option -->
<!-- cases: 3 zero values: an idle container -->
<!-- cases: N/A 4 boundaries: phases are discrete -->
<!-- cases: 5 errors: both sentinels and their precedence -->
<!-- cases: N/A 6 duplicates: covered by Refresh -->
<!-- cases: 7 lifecycle: idle, starting, failed, closed; a Refresh that was waiting for the pipeline when Shutdown began -->
<!-- cases: 8 concurrency: a call during a running Start fails fast instead of waiting for it (no deadlock from Start-time code); a queued Refresh re-checks the phase once it holds the pipeline -->
<!-- cases: N/A 9 panics: no method is called -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Concurrent Refresh calls are serialized
Concurrent `Refresh` and `Scope.Start` calls SHALL run one at a time, so each new service is started exactly once. A root Shutdown that begins during an incremental start SHALL cancel it with cause `ErrStartAfterShutdown` and wait for it, as for `Start`.

#### Scenario: Concurrent incremental starts
```gherkin
Given a started root container and 10 lazy Starters "s0" to "s9" registered after Start
When 10 goroutines call root.Refresh() concurrently
Then every call returns nil
And each of s0 to s9 was started exactly 1 time
And no data race is reported
```

#### Scenario: Shutdown cancels a running Refresh
```gherkin
Given a started root container inside synctest.Test with lazy Starter "late" registered after Start, whose Start(ctx) blocks until ctx is done and returns ctx.Err()
And Refresh() is running in another goroutine and "late" has entered Start
When the consumer calls Shutdown()
Then Shutdown returns only after the Refresh run ended, its Starter having returned
And the report succeeds
And Refresh returns an error matching ErrStartAfterShutdown
```

#### Scenario: Shutdown interrupts a scope Starting hook
```gherkin
Given a started root container inside synctest.Test and child scope "tenant", created after Start
And a HookScopeStarting hook that starts root Shutdown in a goroutine, waits for ctx.Done() and returns ctx.Err()
When the consumer calls tenant.Start()
Then it returns an error for which errors.Is(err, ErrStartAfterShutdown) is true
```

<!-- cases: N/A 2 overrides: serialization is not configurable -->
<!-- cases: N/A 3 zero values, 4 boundaries: 10 callers -->
<!-- cases: 5 errors: the cancelled Refresh error -->
<!-- cases: N/A 6 duplicates: the exactly-once counts -->
<!-- cases: 7 lifecycle: Shutdown during Refresh -->
<!-- cases: 8 concurrency: the subject; runs under -race with goleak -->
<!-- cases: N/A 9 panics: as for Start -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Parallel start keeps dependency order
With `InjectorOpts.StartParallelism` n > 1, Start, Refresh and Scope.Start SHALL run up to n Starters concurrently, each only after every Starter it depends on has started. The first failure SHALL stop new Starters from being scheduled; Start SHALL wait for running ones and return that failure. 0 and 1 SHALL mean sequential, and a value larger than the number of Starters SHALL behave as that number.

#### Scenario: Independent Starters start concurrently
```gherkin
Given a root scope inside synctest.Test created with StartParallelism <n>
And lazy Starters registered in order "a", "b", "c", "d", "e", each blocking 1s in Start, where only "d"'s provider invokes "a"
When the consumer calls Start()
Then Start returns nil after <elapsed> of bubble time
And "d" started after "a" returned

Examples:
  | n | elapsed |
  | 0 | 5s      |
  | 1 | 5s      |
  | 2 | 3s      |
  | 3 | 2s      |
  | 8 | 2s      |
  | 18446744073709551615 (max uint) | 2s |
```

#### Scenario: A failure stops scheduling
```gherkin
Given a root scope inside synctest.Test created with StartParallelism 2
And lazy Starters "a" (returns errBoom after 1s), "b" (blocks 2s, then returns nil) and "c", registered in that order, without dependencies
When the consumer calls Start()
Then Start returns at 2s with an error matching errBoom and naming "a"
And "c" was started 0 times
And "b" was started 1 time
```

#### Scenario: Start hooks under parallel start
```gherkin
Given a root scope created with StartParallelism 4 and HookBeforeStart/HookAfterStart hooks recording calls under a mutex
And 8 lazy Starters without dependencies
When the consumer calls Start()
Then each Starter has exactly one before and one after record
And each Starter's before record precedes its own after record
And no data race is reported
```

#### Scenario: Parallel incremental start
```gherkin
Given a started root container inside synctest.Test created with StartParallelism 3
And lazy Starters "a", "b" and "c" registered after Start in <scope>, each blocking 1s in Start, without dependencies
When the consumer calls <operation>
Then it returns nil after 1s of bubble time
And each of a, b and c was started 1 time

Examples:
  | scope                                   | operation      |
  | the root                                | root.Refresh() |
  | child scope "tenant", created after Start | tenant.Start() |
```

#### Scenario: Parallel incremental start keeps dependency order
```gherkin
Given a started root container inside synctest.Test created with StartParallelism 3
And lazy Starters "a" (blocking 2s), "b", "c" and "d" (each blocking 1s) registered after Start in <scope>, where only "d"'s provider invokes "a"
When the consumer calls <operation>
Then it returns nil after 3s of bubble time
And "d" started after "a" returned

Examples:
  | scope                                   | operation      |
  | the root                                | root.Refresh() |
  | child scope "tenant", created after Start | tenant.Start() |
```

<!-- cases: 1 default: 0 and 1 rows are the sequential default -->
<!-- cases: 2 overrides: n > 1 rows -->
<!-- cases: 3 zero values: StartParallelism 0 -->
<!-- cases: 4 boundaries: n = 1, 2, number of independent Starters, more than that, the maximum uint -->
<!-- cases: 5 errors: the first failure is returned; running Starters are awaited; a Shutdown during that wait has its own requirement -->
<!-- cases: N/A 6 duplicates: each Starter starts once -->
<!-- cases: 7 lifecycle: dependency order is kept ("d" after "a") -->
<!-- cases: 8 concurrency: per-service hooks may run concurrently and must synchronize their own state; runs under -race with goleak -->
<!-- cases: 9 panics: a recovered Start panic is a failure as in "Start errors and panics"; hook panics have their own requirement -->
<!-- cases: 10 compatibility: the default stays sequential -->

### Requirement: A failure returned after Shutdown began matches ErrStartAfterShutdown
When root Shutdown began before Start, Refresh or Scope.Start returns a service failure (for example while a parallel start waits for its running Starters after the first failure), that failure SHALL also match `ErrStartAfterShutdown`, as every start failure after Shutdown began does, and keep matching its own cause.

#### Scenario: A failure followed by Shutdown under parallel start
```gherkin
Given a root container inside synctest.Test created with StartParallelism 2
And lazy Starters "a" (returns errBoom after 1s) and "b" (returns its context's error once cancelled, or nil after 10s), without dependencies, registered <when>
And root Shutdown() is called in another goroutine at 1.5s of bubble time
When the consumer calls <operation>
Then it returns an error matching both errBoom and ErrStartAfterShutdown
And "b"'s context was cancelled

Examples:
  | when                         | operation |
  | before Start                 | Start()   |
  | after Start() returned nil   | Refresh() |
```

<!-- cases: N/A 1 default, 2 overrides: there is no option -->
<!-- cases: N/A 3 zero values, 4 boundaries: one failure and one Shutdown -->
<!-- cases: 5 errors: the failure matches both its cause and ErrStartAfterShutdown -->
<!-- cases: N/A 6 duplicates: an error already carrying the abort cause is not wrapped again -->
<!-- cases: 7 lifecycle: Start and Refresh rows -->
<!-- cases: 8 concurrency: synctest bubble time; runs under -race with goleak -->
<!-- cases: N/A 9 panics: a panic is re-raised as before -->
<!-- cases: N/A 10 compatibility: completes the existing "Shutdown cancels a running Start" rule for parallel start -->

### Requirement: Start hook panics under parallel start reach the caller
With `StartParallelism` n > 1, a panic in a per-service start hook (`HookBeforeStart`, `HookAfterStart`) SHALL be re-raised on the goroutine that called Start, Refresh or Scope.Start once the running Starters have returned, exactly as in sequential mode.

#### Scenario: A panicking start hook under parallel start
```gherkin
Given a root scope created with StartParallelism <n> and a <hook> hook that panics with "hook-boom" for "b"
And lazy Starters "a", "b" and "c" without dependencies, where "a" and "c" take 1s to start and "b" returns at once
When the consumer calls Start()
Then Start panics on the calling goroutine with "hook-boom"
And no Starter's Start is still running when the panic is raised
And Phase() is PhaseFailed afterwards

Examples:
  | n | hook            |
  | 1 | HookBeforeStart |
  | 4 | HookBeforeStart |
  | 4 | HookAfterStart  |
```

<!-- cases: 1 default: the n = 1 row is the sequential reference -->
<!-- cases: N/A 2 overrides: there is no panic option for per-service hooks -->
<!-- cases: N/A 3 zero values, 4 boundaries: one panicking hook among three Starters -->
<!-- cases: N/A 5 errors: a returned error is covered by "A failure stops scheduling" -->
<!-- cases: N/A 6 duplicates: the first panic wins, as for container hooks -->
<!-- cases: 7 lifecycle: the phase is failed afterwards -->
<!-- cases: 8 concurrency: no Starter is left running; runs under -race with goleak -->
<!-- cases: 9 panics: the subject -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Start failures are StartError values
Every failure of a service or kind handler that Start, Refresh or Scope.Start returns SHALL be a `*StartError{Scope, Service, Kind, Op, Cause}` (`Op` is `build`, `start` or `handle`; `Kind` is empty unless `Op` is `handle`) whose `Error()` keeps the existing message and whose `Unwrap()` returns `Cause`. Because its fields are exported, `Error()` SHALL NOT panic on a value built by the consumer: a nil `Scope` reads as an empty scope name and a nil `Cause` as `<nil>`.

#### Scenario: StartError fields per failure point
```gherkin
Given a root scope where <failure>
When the consumer calls Start()
Then errors.As(err, &startErr) is true
And startErr has Service "<service>", Scope name "[root]", Kind "<kind>" and Op "<op>"
And errors.Is(err, <cause>) is true
And err.Error() is unchanged from the message the failure produced before this change

Examples:
  | failure                                               | service | kind      | op     | cause              |
  | lazy Starter "b"'s provider returns errBuild           | b       |           | build  | errBuild           |
  | lazy Starter "b"'s Start returns errBoom               | b       |           | start  | errBoom            |
  | kind "k"'s handler returns errBoom for lazy "j"        | j       | k         | handle | errBoom            |
  | lazy "b" registered with DependsOnNamed("missing")      | b       |           | build  | ErrServiceNotFound |
```

#### Scenario: Incremental start failures are StartError values
```gherkin
Given a started root container and child scope "tenant", created after Start, where <failure>
When the consumer calls <operation>
Then errors.As(err, &startErr) is true
And startErr has Service "<service>", Scope name "tenant", Kind "<kind>" and Op "<op>"
And errors.Is(err, <cause>) is true

Examples:
  | failure                                          | operation      | service | kind | op     | cause    |
  | lazy Starter "b"'s provider returns errBuild      | root.Refresh() | b       |      | build  | errBuild |
  | lazy Starter "b"'s Start returns errBoom          | tenant.Start() | b       |      | start  | errBoom  |
  | kind "k"'s handler returns errBoom for lazy "j"   | root.Refresh() | j       | k    | handle | errBoom  |
```

#### Scenario: StartError values built by the consumer
```gherkin
Given a *StartError built by the consumer with <fields>
When the consumer calls Error()
Then it does not panic
And it returns "<message>"

Examples:
  | fields                                                   | message                                                   |
  | Service "db", Op start, Cause errors.New("boom"), no Scope | DI: failed to start service `db` in scope ``: boom        |
  | Service "db", Op build, Scope the root's, no Cause         | DI: failed to build service `db` in scope `[root]`: <nil> |
  | Service "j", Kind "k", Op handle, no Scope, no Cause       | DI: kind `k` failed for service `j` in scope ``: <nil>    |
  | all fields zero                                          | DI: failed to build service `` in scope ``: <nil>         |
```

#### Scenario: Errors that are not service failures are not StartError
```gherkin
Given a root scope where <failure>
When the consumer calls Start()
Then errors.As(err, &startErr) is false
And errors.Is(err, <sentinel>) is true

Examples:
  | failure                                  | sentinel              |
  | Start() was already called successfully   | ErrAlreadyStarted     |
  | Shutdown() was already called             | ErrStartAfterShutdown |
  | the context is already cancelled          | context.Canceled      |
```

<!-- cases: N/A 2 overrides: the error type is fixed -->
<!-- cases: 3 zero values: a nil error on success; a StartError with a nil Scope or Cause, or all fields zero ("StartError values built by the consumer") -->
<!-- cases: N/A 4 boundaries: one failure per Start -->
<!-- cases: 5 errors: every failure point and the non-service errors -->
<!-- cases: N/A 6 duplicates: covered by the one-shot requirement -->
<!-- cases: 7 lifecycle: Refresh and Scope.Start return the same type ("Incremental start failures are StartError values") -->
<!-- cases: N/A 8 concurrency: parallel start returns the first StartError -->
<!-- cases: 9 panics: a recovered Start panic is Op "start" with the panic as cause -->
<!-- cases: 10 compatibility: messages and errors.Is behavior are unchanged; errors.As is additive -->

### Requirement: Child scope shutdown waits for a start that is starting its services
A child scope's `Shutdown`/`ShutdownWithContext` that begins while a `Start`, `Refresh` or `Scope.Start` is running, and that run has services of this scope among its candidates, SHALL wait for that run to return, without cancelling it, before shutting anything down. A run that begins while the scope shuts down SHALL NOT start or handle services of that scope or of its descendant scopes. Shutting such a scope down synchronously from code running in that run deadlocks, as for root Shutdown.

#### Scenario: Child shutdown waits for a running start
```gherkin
Given a root container and child scope "tenant" with lazy Starter "w" whose Start blocks until released
And the consumer has <before>
And <run> is running in another goroutine and "w" has entered Start
When the consumer calls tenant.Shutdown() in another goroutine
Then tenant.Shutdown() has not returned while "w" is blocked
When "w" is released
Then <run> returns nil
And tenant.Shutdown() returns a successful report
And "w" was shut down 1 time, after its Start returned
And the start bookkeeping holds no entry for "w"

Examples:
  | before                                          | run            |
  | done nothing                                    | root.Start()   |
  | called Start(), then registered "w" in "tenant" | root.Refresh() |
  | called Start(), then registered "w" in "tenant" | tenant.Start() |
```

#### Scenario: A start that begins during a child shutdown skips the scope
```gherkin
Given a started root container and child scope "tenant" with lazy Starter "w" registered after Start
And tenant.Shutdown() is running in another goroutine, blocked in a HookScopeClosing hook
When the consumer calls root.Refresh()
Then Refresh returns nil
And "w" was started 0 times
```

#### Scenario: A scope created under a shutting-down scope is skipped
```gherkin
Given a started root container and child scope "tenant"
And tenant.Shutdown() is running in another goroutine, blocked in a HookScopeClosing hook
And the consumer then creates grandchild "jobs" under "tenant" with lazy Starter "w"
When the consumer calls root.Refresh()
Then Refresh returns nil
And "w" was started 0 times
```

#### Scenario: A scope created during a running start does not wait
```gherkin
Given a root container with lazy Starter "a" whose provider creates child scope "tmp", registers and invokes lazy "x" in it, and calls tmp.Shutdown()
When the consumer calls Start()
Then Start returns nil
And tmp.Shutdown() returned a successful report while Start was running
```

<!-- cases: 1 default: waiting is not configurable -->
<!-- cases: 2 overrides: the context bounds the wait ("A child shutdown wait is bounded by its context") -->
<!-- cases: N/A 3 zero values: a nil context means context.Background(), as for every Shutdown -->
<!-- cases: 4 boundaries: a run with no candidate in the scope (a scope created during the run) does not wait -->
<!-- cases: N/A 5 errors: the context error has its own requirement -->
<!-- cases: N/A 6 duplicates: a second Shutdown of the same scope finds no running run with candidates in it -->
<!-- cases: 7 lifecycle: Start, Refresh and Scope.Start rows; a run that begins during the shutdown -->
<!-- cases: 8 concurrency: the subject; runs under -race with goleak -->
<!-- cases: N/A 9 panics: hook panics during the wait follow scope-lifecycle-hooks -->
<!-- cases: 10 compatibility: BEHAVIOR CHANGE (flagged) - a child scope Shutdown no longer races a running start; Start-time code that shuts down a scope the run is starting deadlocks -->

### Requirement: A run skips services shut down before it claims them
A Start, Refresh or Scope.Start SHALL NOT build, handle or start a service that its scope no longer holds when the run claims its candidates, such as one removed by a child scope shutdown that ran entirely after the run collected it. Such a service SHALL NOT fail the run, and a same-name service of an ancestor scope SHALL NOT be started again through it.

#### Scenario: A child shutdown completes between collection and claim
```gherkin
Given a started root container with <ancestor>
And child scope "tenant" with lazy Starter "w", registered after Start
And <run> is running in another goroutine and has collected "w", but has not claimed its candidates
When tenant.Shutdown() runs to completion
And <run> continues
Then <run> returns nil
And tenant's "w" was started 0 times
And the root's "w" was started <root_starts> times

Examples:
  | ancestor                     | run            | root_starts |
  | no service "w"               | root.Refresh() | 0           |
  | lazy Starter "w" in the root | root.Refresh() | 1           |
  | no service "w"               | tenant.Start() | 0           |
  | lazy Starter "w" in the root | tenant.Start() | 1           |
```

<!-- cases: N/A 1 default, 2 overrides: there is no option -->
<!-- cases: N/A 3 zero values: an empty scope has nothing to claim -->
<!-- cases: 4 boundaries: the shutdown completes after collection and before the claim; a shutdown still running at the claim is covered by "Child scope shutdown waits for a start that is starting its services" -->
<!-- cases: 5 errors: the run returns nil instead of a build error matching ErrServiceNotFound -->
<!-- cases: 6 duplicates: a same-name ancestor Starter is not started twice -->
<!-- cases: 7 lifecycle: Refresh and Scope.Start rows; Start cannot race a child scope created before it in this way without a test seam, and shares the claim -->
<!-- cases: 8 concurrency: the subject; the test pauses collection deterministically; runs under -race with goleak -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: N/A 10 compatibility: fixes a race; no behavior that worked before changes -->

### Requirement: An aborted run matches ErrStartAfterShutdown when the caller cancelled first
When the caller's context was cancelled before root Shutdown began, a Start, Refresh or Scope.Start that returns after Shutdown began SHALL return an error matching `ErrStartAfterShutdown` and the caller's cause, whether its pipeline failed or returned nil. A cause SHALL appear once in the message.

#### Scenario: Caller cancellation, then Shutdown
```gherkin
Given a root container inside synctest.Test created with StartParallelism <n>
And lazy Starter "a" whose Start blocks until released, then returns <outcome>, registered <when>
And with n = 4, lazy Starter "b", without dependencies, whose Start blocks until released, then returns nil
And <operation> is running with a cancellable context ctx and "a" (and "b" with n = 4) has entered Start
When the consumer cancels ctx, then calls Shutdown() in another goroutine, waits until it is blocked, and releases the Starters
Then <operation> returns an error matching ErrStartAfterShutdown and context.Canceled
And <check>

Examples:
  | n | outcome | when                       | operation             | check                                        |
  | 1 | errBoom | before Start               | StartWithContext(ctx)   | it matches errBoom                           |
  | 1 | errBoom | after Start() returned nil | RefreshWithContext(ctx) | it matches errBoom                           |
  | 4 | errBoom | before Start               | StartWithContext(ctx)   | it matches errBoom                           |
  | 4 | errBoom | after Start() returned nil | RefreshWithContext(ctx) | it matches errBoom                           |
  | 1 | nil     | before Start               | StartWithContext(ctx)   | its message contains "context canceled" once |
  | 1 | nil     | after Start() returned nil | RefreshWithContext(ctx) | its message contains "context canceled" once |
```

#### Scenario: The abort outcome rule
```gherkin
Given a run whose context was cancelled with cause <cause>, after which Shutdown began
When the run's outcome is <result>
Then the returned error is <returned>

Examples:
  | cause                 | result                                    | returned                                                                  |
  | ErrStartAfterShutdown | nil                                       | "DI: start aborted: DI: container is shut down"                           |
  | context.Canceled      | nil                                       | "DI: start aborted: DI: container is shut down: context canceled"          |
  | ErrStartAfterShutdown | errBoom                                   | "boom (start aborted: DI: container is shut down)"                        |
  | context.Canceled      | errBoom                                   | "boom (start aborted: DI: container is shut down: context canceled)"       |
  | context.Canceled      | "boom (start aborted: context canceled)"  | "boom (start aborted: context canceled) (start aborted: DI: container is shut down)" |
  | context.Canceled      | an error already matching ErrStartAfterShutdown | that error unchanged                                                |
```

<!-- cases: N/A 1 default, 2 overrides: there is no option -->
<!-- cases: N/A 3 zero values: a nil outcome is a row -->
<!-- cases: N/A 4 boundaries: one cancellation and one Shutdown -->
<!-- cases: 5 errors: the subject; both causes match -->
<!-- cases: 6 duplicates: a cause already carried is not wrapped again -->
<!-- cases: 7 lifecycle: Start and Refresh rows; Scope.Start shares finishIncremental with Refresh -->
<!-- cases: 8 concurrency: sequential and parallel rows; synctest bubble; runs under -race with goleak -->
<!-- cases: N/A 9 panics: covered by container-lifecycle-hooks "Shutdown during an in-flight Start" -->
<!-- cases: N/A 10 compatibility: messages are unchanged when Shutdown's cause came first -->

### Requirement: Incremental start uses the caller's context
`RefreshWithContext(ctx)` and `Scope.StartWithContext(ctx)` SHALL derive the run's context from ctx: its values SHALL reach Starters, kind handlers and scope Starting hooks, its cancellation SHALL abort the run with an error matching `context.Canceled`, and a nil ctx SHALL be treated as `context.Background()`.

#### Scenario: The caller's context reaches the run
```gherkin
Given a started root container and lazy Starter "a", registered after Start in <scope>, whose Start(ctx) records ctx
And a HookScopeStarting hook recording its context
When the consumer calls <operation> with <ctx>
Then <result>

Examples:
  | scope                         | operation          | ctx                                   | result                                                                        |
  | the root                      | RefreshWithContext | a context holding value "v" for key k | it returns nil and "a" recorded a context holding "v" for k                    |
  | child scope "tenant"          | tenant.StartWithContext | a context holding value "v" for key k | it returns nil and "a" and the Starting hook recorded a context holding "v" for k |
  | the root                      | RefreshWithContext | an already cancelled context          | it returns an error matching context.Canceled and "a" was started 0 times      |
  | child scope "tenant"          | tenant.StartWithContext | an already cancelled context     | it returns an error matching context.Canceled and "a" was started 0 times      |
  | the root                      | RefreshWithContext | nil                                   | it returns nil and "a" recorded a non-nil context                              |
  | child scope "tenant"          | tenant.StartWithContext | nil                              | it returns nil and "a" recorded a non-nil context                              |
```

<!-- cases: 1 default: Refresh() and Scope.Start() use context.Background() -->
<!-- cases: 2 overrides: the caller's context is the subject -->
<!-- cases: 3 zero values: the nil rows -->
<!-- cases: N/A 4 boundaries: one Starter per row -->
<!-- cases: 5 errors: the cancelled rows -->
<!-- cases: N/A 6 duplicates: none -->
<!-- cases: 7 lifecycle: Refresh and Scope.Start rows -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent Refresh calls are serialized" -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A child shutdown wait is bounded by its context
If the context of a child scope's `ShutdownWithContext` ends while it waits for a running start, the report SHALL fail with an error keyed by the scope with an empty service name wrapping `ctx.Err()`, and the shutdown SHALL proceed.

#### Scenario: A child shutdown bounded by its context
```gherkin
Given a started root container inside synctest.Test and child scope "tenant" with lazy Starter "w", registered after Start, whose Start blocks until released
And root.Refresh() is running in another goroutine and "w" has entered Start
When the consumer calls tenant.ShutdownWithContext(ctx) with a 1s timeout
Then it returns after 1s of bubble time with a failed report
And the report has an error keyed by scope "tenant" with an empty service name, matching context.DeadlineExceeded
And once "w" is released and Refresh returned, the start bookkeeping holds no entry for "w"
```

<!-- cases: 1 default: Shutdown() uses context.Background() and waits until the run returns -->
<!-- cases: 2 overrides: the context deadline -->
<!-- cases: N/A 3 zero values: a nil context means context.Background() -->
<!-- cases: N/A 4 boundaries: one deadline -->
<!-- cases: 5 errors: the context error in the report -->
<!-- cases: N/A 6 duplicates, 9 panics: as for the wait -->
<!-- cases: 7 lifecycle: the shutdown proceeds while the run is still running -->
<!-- cases: 8 concurrency: synctest bubble time; runs under -race with goleak -->
<!-- cases: N/A 10 compatibility: new behavior of the wait -->

### Requirement: A run checks its context between scheduling steps
`Start`, `Refresh` and `Scope.Start` SHALL check the run's context before each kind-handler call and, under parallel start, before scheduling each Starter, as `StartWithContext` does before each candidate build. Once the caller's context is cancelled, no further handler call or Starter SHALL begin, even when every call so far succeeded, and the run SHALL fail with an error matching the context's error.

#### Scenario: A cancel between two steps
```gherkin
Given a root scope, with <setup>, registered <when>
And the consumer passes ctx to <operation>
When <canceller> cancels ctx and returns nil
Then the operation returns an error matching context.Canceled
And <skipped> did not begin

Examples:
  | setup                                                                      | when         | operation                   | canceller                     | skipped                        |
  | kind "k" with a StageBeforeStart handler and components "j1" then "j2"     | before Start | StartWithContext(ctx)       | the handler, for "j1"         | the handler call for "j2"      |
  | kind "k" with a StageBeforeStart handler and components "j1" then "j2"     | after Start  | RefreshWithContext(ctx)     | the handler, for "j1"         | the handler call for "j2"      |
  | StartParallelism 2 and Starters "a", "b" (returns once "a" returned), "c"  | before Start | StartWithContext(ctx)       | "a"'s Start                   | "c"'s Start                    |
  | StartParallelism 2 and Starters "a", "b" (returns once "a" returned), "c"  | after Start  | RefreshWithContext(ctx)     | "a"'s Start                   | "c"'s Start                    |
```

<!-- cases: 1 default: the checks always run -->
<!-- cases: N/A 2 overrides: the caller's context is the control -->
<!-- cases: N/A 3 zero values: a nil context is context.Background() and never cancels -->
<!-- cases: 4 boundaries: the cancel lands between two components of one kind, and between two scheduling steps -->
<!-- cases: 5 errors: context.Canceled -->
<!-- cases: N/A 6 duplicates: none -->
<!-- cases: 7 lifecycle: Start and Refresh rows -->
<!-- cases: 8 concurrency: the parallel scheduler rows; runs under -race -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: N/A 10 compatibility: the checks existed; this pins them -->
