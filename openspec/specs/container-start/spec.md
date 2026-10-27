# container-start Specification

## Purpose

Lets a consumer start a fully wired container in one call, so background components are built and started in dependency order, and then stopped by the existing shutdown, which runs exactly once per root container.

## Requirements

### Requirement: Starter variants are detected by registered type
`Start` SHALL start every non-transient, non-alias service whose registered type implements one of `Starter`, `StarterWithError`, `StarterWithContext` or `StarterWithContextAndError`, plus every service registered with `AsStarter()`. It SHALL call the variant's `Start` method exactly once. Unlike `Shutdowner*`, which is detected on the instance, detection uses the registered type.

#### Scenario: Each Starter variant is started once
```gherkin
Given a root scope with lazy "<service>" of a type whose method is <method>
When the consumer calls StartWithContext(ctx)
Then the start method was called 1 time
And StartWithContext returns nil

Examples:
  | service | method                          |
  | s1      | Start()                         |
  | s2      | Start() error                   |
  | s3      | Start(context.Context)          |
  | s4      | Start(context.Context) error    |
```

#### Scenario: Which registrations are started
```gherkin
Given a root scope with "<service>" registered as <registration>
And every instance also implements Shutdown() error
When the consumer calls Start() and then Shutdown()
Then the instance's Start was called <starts> times
And the instance's Shutdown was called <shutdowns> times

Examples:
  | service       | registration                                                                              | starts | shutdowns |
  | *app.Server   | lazy, type has Start(ctx) error                                                           | 1      | 1         |
  | *app.Server   | eager value, type has Start() error                                                       | 1      | 1         |
  | app.Lifecycle | lazy registered as interface app.Lifecycle (no Start in interface), instance has Start()  | 0      | 0         |
  | app.Lifecycle | same as above, plus AsStarter()                                                           | 1      | 1         |
  | app.Lifecycle | same as above, plus doger.Invoke[app.Lifecycle] before Start()                            | 0      | 1         |
  | *app.Server   | lazy, plus As[*app.Server, app.Startable] alias                                           | 1      | 1         |
  | *app.Config   | lazy, type has no Start                                                                   | 0      | 0         |
  | *app.Server   | transient, type has Start() error                                                         | 0      | 0         |
```

<!-- cases: 2 overrides: DisableStarterAutoDetection is covered by "Starter auto-detection can be disabled"; AsStarter by the app.Lifecycle rows -->
<!-- cases: N/A 3 zero values: a container with no services is covered by "Start on an empty container" -->
<!-- cases: 6 duplicates: the alias row pins that a service is started once even when also reachable through an alias -->
<!-- cases: 9 panics: covered by "Start errors and panics" -->
<!-- cases: 10 compatibility: the "Invoke before Start" row pins that instance-based Shutdowner detection is unchanged while Starter detection is type-based -->

### Requirement: Starter auto-detection can be disabled
When `InjectorOpts.DisableStarterAutoDetection` is true, `Start` SHALL start only services registered with `AsStarter()`.

#### Scenario: Auto-detection toggle
```gherkin
Given a root scope created with DisableStarterAutoDetection = <disabled>
And lazy "a" whose type has Start() error, registered without options
And lazy "b" whose type has Start() error, registered with AsStarter()
When the consumer calls Start()
Then the started services are <started>

Examples:
  | disabled | started |
  | false    | a, b    |
  | true     | b       |
```

<!-- cases: N/A 3 zero values: the zero value of the field is false, which is the default row -->
<!-- cases: N/A 4 boundaries, 5 errors, 9 panics: a boolean toggle -->
<!-- cases: N/A 7 lifecycle, 8 concurrency: options are fixed at container creation -->
<!-- cases: N/A 10 compatibility: new option -->

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

### Requirement: Start errors and panics
On the first failure, `Start` SHALL stop and return an error that names the service and scope and wraps the cause (`errors.Is`/`errors.As`). Failures are a build error, a `Start` error, a recovered panic in `Start`, or a kind-handler error. Starters not yet reached SHALL NOT be started, and nothing is rolled back. A panic that escapes `Start` SHALL still leave the container marked as started and SHALL NOT block a later `Shutdown`.

#### Scenario: A Starter failure stops the remaining Starters
```gherkin
Given a root scope with lazy Starters "a", "b", "c" registered in that order, with no dependencies
And "b" fails with <failure>
When the consumer calls Start()
Then the returned error message contains "service `b`"
And <cause-check>
And the started services are a
And "c" was built but not started

Examples:
  | failure                            | cause-check                            |
  | Start() returning errBoom          | errors.Is(err, errBoom) is true        |
  | Start(ctx) panicking with "kaboom" | the error message contains "kaboom"    |
  | Start(ctx) panicking with errBoom  | errors.Is(err, errBoom) is true        |
```

#### Scenario: A build failure stops Start before any Starter
```gherkin
Given a root scope with lazy Starters "a", "b", "c" registered in that order, with no dependencies
And "b" is registered with <registration>
When the consumer calls Start()
Then the returned error message contains "service `b`"
And errors.Is(err, <cause>) is true
And "a" was built and "c" was not built
And no Starter was started

Examples:
  | registration                                   | cause              |
  | a provider returning errBuild                  | errBuild           |
  | DependsOnNamed("missing")                      | ErrServiceNotFound |
```

#### Scenario: AsStarter on an interface whose instance cannot start
```gherkin
Given a root scope with lazy "app.Lifecycle" registered with AsStarter()
And the provider returns an instance with no Start method
When the consumer calls Start()
Then the error message contains "DI: service `app.Lifecycle` was opted in with AsStarter but does not implement any Starter interface"
```

#### Scenario: Shutdown after a failed Start reaches every built service
```gherkin
Given a root scope where Starter "a" starts, Starter "b" returns errBoom from Start, and Starter "c" is built but never started
And all three implement Shutdown() error
And the consumer called Start(), which returned an error
When the consumer calls Shutdown()
Then Shutdown() was called on a, b and c
```

#### Scenario: A panic escaping Start still releases Shutdown
```gherkin
Given a root scope with lazy Starter "a" implementing Shutdown() error
And a HookBeforeInvocation that panics with "hook-boom"
And the consumer called Start(), which panicked with "hook-boom"
When the consumer calls Shutdown() inside testWithTimeout(1s)
Then Shutdown returns a successful report
And a second Start() returns an error matching ErrStartAfterShutdown
```

<!-- cases: N/A 2 overrides: no rollback by design; the caller decides (Shutdown, retry, exit) -->
<!-- cases: N/A 3 zero values: a nil error from Start() error is the success case -->
<!-- cases: 7 lifecycle: covered by "Shutdown after a failed Start" and "A panic escaping Start still releases Shutdown" -->
<!-- cases: N/A 8 concurrency: the pipeline is sequential -->
<!-- cases: 10 compatibility: Shutdown behavior for services that were never started is unchanged -->

### Requirement: Start honours its context
`StartWithContext` SHALL check its context before each candidate build, each kind-handler call and each Starter. A cancelled or expired context SHALL stop Start with an error wrapping `ctx.Err()`. Context-aware `Start(ctx)` variants, handlers and runners SHALL receive a context derived from it, which is cancelled when `Start` returns. `Start()` SHALL use `context.Background()`, and a nil context SHALL be treated as `context.Background()`.

#### Scenario: Cancellation and deadline
```gherkin
Given a root scope with lazy Starters "a" then "b", inside synctest.Test
And "a" is a Start(ctx) error that <a-behavior>
When the consumer calls StartWithContext(ctx) where ctx <ctx>
Then errors.Is(err, <cause>) is true
And "b" was not started

Examples:
  | ctx                         | a-behavior                                    | cause                    |
  | is already cancelled        | returns nil                                   | context.Canceled         |
  | has a 5s deadline           | blocks until ctx.Done() and returns ctx.Err() | context.DeadlineExceeded |
  | is cancelled while "a" runs | returns nil after the cancellation            | context.Canceled         |
```

#### Scenario: Nil context
```gherkin
Given a root scope with one lazy Starter "a" of type Start(ctx) error
When the consumer calls StartWithContext(nil)
Then Start returns nil
And "a" received a non-nil context
```

#### Scenario: Start-time context ends when Start returns
```gherkin
Given a root scope with one lazy Starter "a" of type Start(ctx) error that keeps the received context
When the consumer calls Start()
Then Start returns nil
And the kept context's Err() is context.Canceled
```

<!-- cases: N/A 2 overrides: there is no Start timeout option; callers use context deadlines -->
<!-- cases: 3 zero values: nil context is covered by "Nil context" -->
<!-- cases: 8 concurrency: deadline scenarios run under testing/synctest, so time is virtual and no goroutine leaks -->
<!-- cases: N/A 9 panics: covered by "Start errors and panics" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Start is one-shot
A root container SHALL start at most once. Any `Start` after the first, whether that one succeeded or failed, SHALL return an error matching `ErrAlreadyStarted` and do nothing. Any `Start` after root `Shutdown` began SHALL return an error matching `ErrStartAfterShutdown`, which takes precedence. A clone SHALL start independently of its source.

#### Scenario: Repeated and late Start calls
```gherkin
Given a root scope with one lazy Starter "a"
And the consumer has already <previous>
When the consumer calls Start()
Then the result matches <result>
And "a" had Start called <calls> times in total

Examples:
  | previous                                                      | result                 | calls |
  | called nothing                                                | nil                    | 1     |
  | called Start() successfully                                   | ErrAlreadyStarted      | 1     |
  | called Start(), which failed because a's provider returned errBuild | ErrAlreadyStarted | 0     |
  | called Shutdown()                                             | ErrStartAfterShutdown  | 0     |
  | called Start() successfully and then Shutdown()               | ErrStartAfterShutdown  | 1     |
```

#### Scenario: Concurrent Start calls
```gherkin
Given a root scope with one lazy Starter "a"
When 10 goroutines call Start() concurrently
Then exactly 1 call returns nil
And 9 calls return an error matching ErrAlreadyStarted
And "a" was started 1 time
```

#### Scenario: Concurrent Start and Invoke
```gherkin
Given a root scope with lazy Starter "a" whose provider counts its calls
When Start() runs while 50 goroutines invoke "a"
Then the "a" provider was called exactly 1 time
And no data race is reported
```

#### Scenario: Clone of a started container
```gherkin
Given a root scope with <registration> Starter "a" counting Start calls per instance
And the consumer called Start() on the root scope
When the consumer calls Start() on root.Clone()
Then the clone's Start returns nil
And the Start calls are <calls>

Examples:
  | registration | calls                                     |
  | lazy         | 1 on the original instance, 1 on a new instance |
  | eager value  | 2 on the same shared instance             |
```

<!-- cases: N/A 2 overrides: one-shot is fixed; re-running is the deferred Refresh operation -->
<!-- cases: N/A 3 zero values: covered by the "called nothing" row -->
<!-- cases: 7 lifecycle: start after start, after a failed start, after shutdown, and clones are covered -->
<!-- cases: N/A 9 panics: covered by "A panic escaping Start still releases Shutdown" -->
<!-- cases: 10 compatibility: Clone keeps sharing eager instances, as today; the eager row documents the consequence -->

### Requirement: Shutdown cancels a running Start
A root `Shutdown` that begins while `Start` runs SHALL cancel Start's context with cause `ErrStartAfterShutdown` and wait until `Start` returns or its own context is done; in the latter case its report SHALL fail with a root-scope error matching that context's error. `Start` SHALL then stop before the next Starter or handler call and return an error matching `ErrStartAfterShutdown`. Nothing a late Starter brings up is rolled back.

#### Scenario: Shutdown cancels a running Start
```gherkin
Given a root scope inside synctest.Test with <role> "a" registered before <role> "z", both implementing Shutdown() error
And "a" <a-behavior>
And Start() is running in another goroutine and "a" has entered its <method> method
When the consumer calls ShutdownWithContext(<shutdown-ctx>)
Then Shutdown returns <when>
And the shutdown report <report>
And Start returns an error matching ErrStartAfterShutdown
And "z" had its <method> method called 0 times

Examples:
  | role             | method | a-behavior                                                  | shutdown-ctx            | when                          | report                                                          |
  | lazy Starter     | Start  | returns ctx.Err() when ctx is done                          | context.Background()    | after Start returned          | succeeds and a's Shutdown was called 1 time                     |
  | lazy Runner      | Run    | returns ctx.Err() when ctx is done                          | context.Background()    | after Start returned          | succeeds and a's Shutdown was called 1 time                     |
  | lazy Starter     | Start  | ignores ctx and returns nil once released; a helper goroutine releases it after synctest.Wait() shows Shutdown blocked | context.Background() | after Start returned | succeeds and a's Shutdown was called 1 time |
  | lazy Starter     | Start  | ignores ctx and returns nil once released; the test releases it only after Shutdown returned | a 1s deadline | at the 1s deadline, before Start returned | fails, has an error for the root scope, every error matches context.DeadlineExceeded, and a's Shutdown was called 0 times |
```

<!-- cases: N/A 2 overrides: cancellation is always on; a Starter that must finish ignores ctx and Shutdown's deadline bounds the wait -->
<!-- cases: N/A 3 zero values: a Shutdown with no running Start is covered by "Root Shutdown runs once" -->
<!-- cases: 7 lifecycle: the last row documents that a Starter finishing after Shutdown stopped waiting is not rolled back -->
<!-- cases: 8 concurrency: every row runs under synctest with a channel handshake; goleak checks the Start goroutine exits -->
<!-- cases: N/A 9 panics: covered by "A panic escaping Start still releases Shutdown" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Root Shutdown runs once
Root `Shutdown`/`ShutdownWithContext` SHALL shut services down exactly once per root container. A concurrent or later caller SHALL wait for the first one, or for its own context, and SHALL return a copy of the first caller's report. A waiting caller whose context ends first SHALL return a failed report with one error, keyed by the root scope with an empty service name, wrapping `ctx.Err()`. This fixes spurious `ErrServiceNotFound` errors and a data race.

#### Scenario: Concurrent root Shutdown runs once
```gherkin
Given a root scope created with <opts>
And built lazy services "a", "b" and "c", each counting Shutdown() error calls
When 20 goroutines call Shutdown() on the root scope concurrently
Then every returned report has Succeed true and no errors
And every returned report equals the first caller's report
And each of a, b and c had Shutdown called 1 time
And no data race is reported

Examples:
  | opts                                  |
  | &InjectorOpts{}                       |
  | &InjectorOpts{HealthCheckParallelism: 2} |
```

#### Scenario: Later root Shutdown returns the first report
```gherkin
Given a root scope with built lazy "a" whose Shutdown() returns errClose
And the consumer called Shutdown(), which returned report r1
When the consumer calls Shutdown() again
Then the returned report equals r1, including the errClose entry for "a"
And it is a distinct copy: mutating its Errors map leaves r1 unchanged
And "a" had Shutdown called 1 time
```

#### Scenario: Waiting Shutdown caller honours its context
```gherkin
Given a root scope inside synctest.Test with built lazy "a" whose Shutdown() blocks until released
And a first Shutdown() is running in another goroutine and "a" has entered Shutdown()
When the consumer calls ShutdownWithContext(ctx) where ctx has a 1s deadline
Then it returns at the 1s deadline with Succeed false
And its only error is keyed by the root scope with an empty service name and matches context.DeadlineExceeded
And "a" had Shutdown called 1 time after it is released
```

<!-- cases: N/A 2 overrides: running shutdown more than once is never correct; there is nothing to override -->
<!-- cases: 3 zero values: an empty container is covered by existing shutdown tests; a nil context is covered by "Root Shutdown reports stay usable" -->
<!-- cases: 5 errors: per-service errors are carried in the copied report ("Later root Shutdown returns the first report") -->
<!-- cases: 7 lifecycle: double shutdown is "Later root Shutdown returns the first report"; Start after Shutdown is covered by "Start is one-shot" -->
<!-- cases: 8 concurrency: first scenario under -race with 20 goroutines; deadline in "Waiting Shutdown caller honours its context" -->
<!-- cases: N/A 9 panics: covered by "Root Shutdown reports stay usable" -->
<!-- cases: 10 compatibility: BEHAVIOR CHANGE - a later root Shutdown returns the first report instead of an empty one, and no longer shuts down services registered on the root after the first Shutdown; Scope.Shutdown on child scopes is unchanged -->

### Requirement: Root Shutdown reports stay usable
A root Shutdown caller that finds the first shutdown completed SHALL get a copy of the first report, even with an ended context. The stored report SHALL be isolated from every caller's copy. A nil context SHALL mean `context.Background()`. If the first caller panics before producing a report, every other caller SHALL get a failed report whose only root-scope error (empty service name) contains `DI: root shutdown did not complete`.

#### Scenario: The stored report is isolated from the first caller
```gherkin
Given a root scope with built lazy "a" whose Shutdown() returns errClose
And the consumer called Shutdown(), which returned report r1
And the consumer then deleted every entry of r1's Errors map
When the consumer calls Shutdown() again
Then the returned report has the errClose entry for "a"
```

#### Scenario: Later Shutdown with an ended context still returns the first report
```gherkin
Given a root scope with built lazy "a" implementing Shutdown() error
And the consumer called Shutdown(), which returned report r1
When the consumer calls ShutdownWithContext(ctx) 100 times, where ctx <ctx>
Then every returned report equals r1
And "a" had Shutdown called 1 time

Examples:
  | ctx                                      |
  | was cancelled before the calls           |
  | has a deadline that passed before the calls |
```

#### Scenario: Nil Shutdown context
```gherkin
Given a root scope with built lazy "a" whose Shutdown(ctx) error records the context it receives
And the consumer has <previous>
When the consumer calls ShutdownWithContext(nil)
Then it returns a report with Succeed true
And "a" received a non-nil context exactly once

Examples:
  | previous            |
  | called nothing      |
  | called Shutdown()   |
```

#### Scenario: A panicking first caller still releases the others
```gherkin
Given a root scope whose Logf panics with "log-boom" when the message contains "requested shutdown"
And the consumer called Shutdown(), which panicked with "log-boom"
When the consumer calls <call>
Then it returns without panicking
And the report has Succeed false
And its only error is keyed by the root scope with an empty service name and its message contains "DI: root shutdown did not complete"

Examples:
  | call                                          |
  | Shutdown()                                    |
  | ShutdownWithContext(ctx) with a 1s deadline   |
```

<!-- cases: N/A 2 overrides: these rules only make every caller's report usable; there is nothing to override -->
<!-- cases: 3 zero values: a nil context is covered by "Nil Shutdown context" -->
<!-- cases: 4 boundaries: a context that ended before the call, after the first shutdown completed, is covered by "Later Shutdown with an ended context still returns the first report" -->
<!-- cases: 5 errors: the "did not complete" report is covered by "A panicking first caller still releases the others" -->
<!-- cases: N/A 6 duplicates: repeated calls are the subject of "Root Shutdown runs once" -->
<!-- cases: 7 lifecycle: callers after completion are covered by the ended-context and isolation scenarios -->
<!-- cases: 8 concurrency: 100 calls per row pin completion over an ended context, which a random select would violate about half the time -->
<!-- cases: 9 panics: a panic on the first caller's goroutine propagates to that caller and the others get a failed report; panics in per-service shutdown hooks happen on internal goroutines and terminate the process, as today -->
<!-- cases: 10 compatibility: a nil context no longer panics when a built service has a Shutdowner* method (fix); the rest is part of the run-once behavior change -->

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

### Requirement: Per-service start hooks
For each Starter, `Start` SHALL call every `HookBeforeStart(scope, name)` just before its `Start` method and every `HookAfterStart(scope, name, err)` just after, where `err` is the error the method returned, unwrapped (nil on success, the recovered panic as an error). Hooks SHALL run in the order they were added. `AddBeforeStartHook`/`AddAfterStartHook` SHALL append to the same lists.

#### Scenario: Hooks around each Starter
```gherkin
Given a root scope with HookBeforeStart and HookAfterStart recording calls
And a hook added later with AddAfterStartHook recording "late-after"
And Starters "a" (succeeds) then "b" (returns errBoom)
When the consumer calls Start()
Then the recorded calls are: before a, after a nil, late-after a, before b, after b errBoom, late-after b
And the err passed for "b" is errBoom itself, not a wrapping error
```

<!-- cases: N/A 3 zero values: no hooks registered means no calls (default; covered by every other scenario) -->
<!-- cases: N/A 4 boundaries: 0, 1 and many hooks follow the same slice iteration as the existing shutdown hooks -->
<!-- cases: 9 panics: a panicking hook propagates exactly as a panicking shutdown hook does today (no new recovery); container state is still released, see "A panic escaping Start still releases Shutdown" -->
<!-- cases: N/A 8 concurrency: hooks run on the Start goroutine -->
<!-- cases: N/A 10 compatibility: new hooks -->

### Requirement: No start hooks without a Start method
No start hook SHALL be called for a service opted in with `AsStarter()` whose instance implements no Starter variant, because no `Start` method is called. `Start` SHALL return the error instead.

#### Scenario: No hooks for an AsStarter instance without a Start method
```gherkin
Given a root scope with HookBeforeStart and HookAfterStart recording calls
And lazy "app.Lifecycle" registered with AsStarter(), whose provider returns an instance with no Start method
When the consumer calls Start()
Then Start returns an error containing "DI: service `app.Lifecycle` was opted in with AsStarter but does not implement any Starter interface"
And no start hook was called
```

<!-- cases: N/A 2 overrides, 3 zero values, 4 boundaries: a single rule with one input shape -->
<!-- cases: 5 errors: the returned error is the one specified in "AsStarter on an interface whose instance cannot start" -->
<!-- cases: N/A 6 duplicates, 7 lifecycle, 8 concurrency: hooks run on the Start goroutine -->
<!-- cases: N/A 9 panics: no method is called, so nothing can panic -->
<!-- cases: N/A 10 compatibility: new hooks -->

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
And a second root.Refresh() was called in another goroutine and is waiting for the pipeline held by the first
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
And lazy Starters "a", "b" and "c" without dependencies, registered <when>, where "a" and "c" take 1s to start and "b" returns at once
When the consumer calls <operation>
Then it panics on the calling goroutine with "hook-boom"
And no Starter's Start is still running when the panic is raised
And Phase() is <phase> afterwards

Examples:
  | n | hook            | when         | operation | phase       |
  | 1 | HookBeforeStart | before Start | Start()   | PhaseFailed |
  | 4 | HookBeforeStart | before Start | Start()   | PhaseFailed |
  | 4 | HookAfterStart  | before Start | Start()   | PhaseFailed |
  | 4 | HookBeforeStart | after Start  | Refresh() | PhaseReady  |
  | 4 | HookAfterStart  | after Start  | Refresh() | PhaseReady  |
```

<!-- cases: 1 default: the n = 1 row is the sequential reference -->
<!-- cases: N/A 2 overrides: there is no panic option for per-service hooks -->
<!-- cases: N/A 3 zero values, 4 boundaries: one panicking hook among three Starters -->
<!-- cases: N/A 5 errors: a returned error is covered by "A failure stops scheduling" -->
<!-- cases: N/A 6 duplicates: the first panic wins, as for container hooks -->
<!-- cases: 7 lifecycle: the phase is failed after Start, and stays ready after Refresh -->
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
And component "j" of kind "k", registered with "a", whose StageBeforeStart handler records its context
And a HookScopeStarting hook recording its context
When the consumer calls <operation> with <ctx>
Then <result>

Examples:
  | scope                         | operation               | ctx                                   | result                                                                                          |
  | the root                      | RefreshWithContext      | a context holding value "v" for key k | it returns nil and "a" and the handler recorded a context holding "v" for k                      |
  | child scope "tenant"          | tenant.StartWithContext | a context holding value "v" for key k | it returns nil and "a", the handler and the Starting hook recorded a context holding "v" for k   |
  | the root                      | RefreshWithContext      | an already cancelled context          | it returns an error matching context.Canceled; "a" was started 0 times and the handler not called |
  | child scope "tenant"          | tenant.StartWithContext | an already cancelled context          | it returns an error matching context.Canceled; "a" was started 0 times and the handler not called |
  | the root                      | RefreshWithContext      | nil                                   | it returns nil and "a" and the handler recorded a non-nil context                                |
  | child scope "tenant"          | tenant.StartWithContext | nil                                   | it returns nil and "a" and the handler recorded a non-nil context                                |
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
If the context of a child scope's `ShutdownWithContext` ends while it waits for a running start, the report SHALL fail with an error keyed by the scope with an empty service name wrapping `ctx.Err()`, and the shutdown SHALL proceed with that ended context: the services it then reaches SHALL be skipped and reported as `shutdown-context` specifies ("Services reached after the shutdown context ended are skipped").

#### Scenario: A child shutdown bounded by its context
```gherkin
Given a started root container inside synctest.Test and child scope "tenant" with lazy Starter "w", registered after Start, whose Start blocks until released and whose Shutdown() error records "shutdown"
And root.Refresh() is running in another goroutine and "w" has entered Start
When the consumer calls tenant.ShutdownWithContext(ctx) with a 1s timeout
Then it returns after 1s of bubble time with a failed report
And the report has an error keyed by scope "tenant" with an empty service name, matching context.DeadlineExceeded
And the report has an error for "w" in scope "tenant", matching context.DeadlineExceeded
And once "w" is released and Refresh returned, "shutdown" was never recorded and the start bookkeeping holds no entry for "w"
```

<!-- cases: 1 default: Shutdown() uses context.Background() and waits until the run returns -->
<!-- cases: 2 overrides: the context deadline -->
<!-- cases: N/A 3 zero values: a nil context means context.Background() -->
<!-- cases: N/A 4 boundaries: one deadline -->
<!-- cases: 5 errors: the context error in the report, for the scope and for the service the ended shutdown skipped -->
<!-- cases: N/A 6 duplicates, 9 panics: as for the wait -->
<!-- cases: 7 lifecycle: the shutdown proceeds with the ended context while the run is still running, so it skips the service still starting -->
<!-- cases: 8 concurrency: synctest bubble time; runs under -race with goleak -->
<!-- cases: 10 compatibility: the behavior is unchanged; the requirement now states that the services reached are skipped -->

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

### Requirement: A default that yields is not a start candidate
`Start`, `Refresh` and `Scope.Start` SHALL NOT start, or pass to a kind handler, a default that yields to a visible non-default service of the same name, even if its registered type is a Starter or it was given `AsStarter()` or a kind option. A default that does not yield SHALL be a candidate as any registration.

#### Scenario: Start candidates with defaults
```gherkin
Given a root scope with child scope "ext"
And a lazy "*app.Server" registered in scope "ext" with doger.AsDefault(), implementing Start(ctx) error and counting calls
And <explicit>
And <before>
When the consumer calls <operation> on the root
Then the default server's Start was called <default_starts> times
And the root server's Start was called <root_starts> times

Examples:
  | explicit                                                       | before                          | operation | default_starts | root_starts |
  | an explicit "*app.Server" in the root, counting Start calls    | nothing else happened           | Start     | 0              | 1           |
  | nothing                                                        | nothing else happened           | Start     | 1              | 0           |
  | an explicit "*app.Server" in the root, counting Start calls    | the root was started            | Refresh   | 0              | 1           |
```

#### Scenario: A default yields even when it was never invoked before Start
```gherkin
Given a root scope with child scope "ext"
And a lazy "*app.Server" registered in scope "ext" with doger.AsDefault() and doger.AsRunnable(), never invoked
And an explicit lazy "*app.Server" registered in the root with doger.AsRunnable(), implementing Run(ctx) error and counting calls
When the consumer calls Start on the root
Then the root server's Run was called 1 time
And the default's provider was called 0 times
```

#### Scenario: Scope.Start skips a default that yields
```gherkin
Given a started root scope
And a child scope "ext" created after Start, with a lazy "*app.Server" registered with doger.AsDefault(), implementing Start(ctx) error
And an explicit lazy "*app.Server" in the root that was started by the root Start
When the consumer calls Start on scope "ext"
Then the root server's Start was called 1 time in total
And the default server's provider was called 0 times
```

<!-- cases: N/A 2 overrides: DisableStarterAutoDetection applies to built defaults as to any service -->
<!-- cases: N/A 3 zero values, 4 boundaries, 5 invalid input: no input beyond registrations -->
<!-- cases: N/A 6 duplicate: same-scope cases are in default-registrations -->
<!-- cases: 7 lifecycle: Start, Refresh and Scope.Start are covered -->
<!-- cases: N/A 8 concurrency: Start discovery runs under the existing locks; concurrent first resolution is in default-registrations -->
<!-- cases: N/A 9 panics: a skipped default runs no code -->
<!-- cases: 10 compatibility: candidates without AsDefault are unchanged -->
