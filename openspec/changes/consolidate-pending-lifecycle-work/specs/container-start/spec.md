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
`RootScope.Refresh()` / `RefreshWithContext(ctx)` SHALL run the start pipeline over the services and scopes not yet started or handled: build them, run kind handlers for their new components, and start their Starters, with the same order, context, error and per-service hook rules as `Start`. A service SHALL be started at most once per container.

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

<!-- cases: 1 default: Refresh after Start -->
<!-- cases: N/A 2 overrides: StartParallelism and context apply as for Start -->
<!-- cases: 3 zero values: nothing new; a nil context is treated as context.Background(), as for Start -->
<!-- cases: 4 boundaries: zero, one and several new services; repeated Refresh -->
<!-- cases: 5 errors: failures are wrapped as for Start ("Refresh failures") -->
<!-- cases: 6 duplicates: a second Refresh starts nothing again -->
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

#### Scenario: Scope.Start on the root's own scope
```gherkin
Given a started root container with lazy Starter "late" registered after Start
And s is the *Scope a root service's provider received
When the consumer calls s.Start()
Then it returns nil
And "late" was started 1 time
```

<!-- cases: 1 default: subtree start -->
<!-- cases: N/A 2 overrides: as for Refresh -->
<!-- cases: N/A 3 zero values: an empty subtree returns nil (as "Refresh with nothing new") -->
<!-- cases: 4 boundaries: child with grandchild; sibling untouched -->
<!-- cases: N/A 5 errors: as for Refresh -->
<!-- cases: 6 duplicates: the root's own scope behaves as Refresh -->
<!-- cases: 7 lifecycle: before Start is covered by "Incremental start needs a started container" -->
<!-- cases: N/A 8 concurrency: Scope.Start and Refresh share the serialization of "Concurrent Refresh calls are serialized" -->
<!-- cases: N/A 9 panics: as for Start -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Incremental start needs a started container
`Refresh` and `Scope.Start` SHALL return an error matching `ErrNotStarted` ("DI: container not started") while the container's first `Start` has not succeeded, and an error matching `ErrStartAfterShutdown` once root Shutdown has begun, which takes precedence. They SHALL start nothing in either case.

#### Scenario: Incremental start in the wrong phase
```gherkin
Given a root scope with lazy Starter "a" and child scope "tenant" with lazy Starter "w"
And the consumer has <previous>
When the consumer calls <operation>
Then the result matches <result>
And "w" was started 0 times

Examples:
  | previous                                                   | operation      | result                |
  | done nothing                                               | root.Refresh() | ErrNotStarted         |
  | done nothing                                               | tenant.Start() | ErrNotStarted         |
  | called Start(), which failed because a's provider returned errBuild | root.Refresh() | ErrNotStarted |
  | called Start() and then Shutdown()                         | root.Refresh() | ErrStartAfterShutdown |
  | called Shutdown() without Start()                          | tenant.Start() | ErrStartAfterShutdown |
```

<!-- cases: N/A 2 overrides: the phase rule has no option -->
<!-- cases: 3 zero values: an idle container -->
<!-- cases: N/A 4 boundaries: phases are discrete -->
<!-- cases: 5 errors: both sentinels and their precedence -->
<!-- cases: N/A 6 duplicates: covered by Refresh -->
<!-- cases: 7 lifecycle: idle, failed, closed -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent Refresh calls are serialized" -->
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
Then Shutdown returns after Refresh returned
And the report succeeds
And Refresh returns an error matching ErrStartAfterShutdown
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
With `InjectorOpts.StartParallelism` n > 1, Start, Refresh and Scope.Start SHALL run up to n Starters concurrently, each only after every Starter it depends on has started. The first failure SHALL stop new Starters from being scheduled; Start SHALL wait for running ones and return that failure. 0 and 1 SHALL mean sequential.

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

<!-- cases: 1 default: 0 and 1 rows are the sequential default -->
<!-- cases: 2 overrides: n > 1 rows -->
<!-- cases: 3 zero values: StartParallelism 0 -->
<!-- cases: 4 boundaries: n = 1, 2, number of independent Starters, more than that -->
<!-- cases: 5 errors: the first failure is returned; running Starters are awaited -->
<!-- cases: N/A 6 duplicates: each Starter starts once -->
<!-- cases: 7 lifecycle: dependency order is kept ("d" after "a") -->
<!-- cases: 8 concurrency: per-service hooks may run concurrently and must synchronize their own state; runs under -race with goleak -->
<!-- cases: N/A 9 panics: a recovered Start panic is a failure as in "Start errors and panics" -->
<!-- cases: 10 compatibility: the default stays sequential -->

### Requirement: Start failures are StartError values
Every failure of a service or kind handler that Start, Refresh or Scope.Start returns SHALL be a `*StartError{Scope, Service, Kind, Op, Cause}` (`Op` is `build`, `start` or `handle`; `Kind` is empty unless `Op` is `handle`) whose `Error()` keeps the existing message and whose `Unwrap()` returns `Cause`.

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
<!-- cases: N/A 3 zero values: a nil error on success -->
<!-- cases: N/A 4 boundaries: one failure per Start -->
<!-- cases: 5 errors: every failure point and the non-service errors -->
<!-- cases: N/A 6 duplicates: covered by the one-shot requirement -->
<!-- cases: 7 lifecycle: Refresh and Scope.Start return the same type (covered by their failure rows in tasks) -->
<!-- cases: N/A 8 concurrency: parallel start returns the first StartError -->
<!-- cases: 9 panics: a recovered Start panic is Op "start" with the panic as cause -->
<!-- cases: 10 compatibility: messages and errors.Is behavior are unchanged; errors.As is additive -->
