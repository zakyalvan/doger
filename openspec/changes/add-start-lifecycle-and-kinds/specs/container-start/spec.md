# Spec Delta

## Purpose

Lets a consumer start a fully wired container in one call, so background components are built and started in dependency order, and then stopped by the existing shutdown, which runs exactly once per root container.

## ADDED Requirements

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
`Start` SHALL build every candidate (Starters and services opted into any kind) before starting any Starter. A service's dependencies, invoked or declared with `DependsOn`, are built before it. It SHALL then start Starters one at a time: a Starter starts only after every Starter it depends on, directly or transitively, has started. Ties are broken by scope depth, then registration order.

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

<!-- cases: N/A 2 overrides: ordering is overridden by declaring edges (dependency-declaration) -->
<!-- cases: N/A 4 boundaries: 0, 1 and many Starters are covered across scenarios -->
<!-- cases: 6 duplicates: a service reachable from several Starters is built once (lazy singleton semantics) -->
<!-- cases: 7 lifecycle: child scopes are covered by "Child scopes are started after their ancestors"; cycles by dependency-declaration ("Start rejects a cycle closed by a dynamic edge") -->
<!-- cases: 8 concurrency: Starters start sequentially; concurrent Invoke during Start is covered by "Concurrent Start and Invoke" -->
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
Services registered after `Start` returned, and child scopes created after `Start` took its discovery snapshot, SHALL NOT be started or handled automatically in this change. They remain invokable as usual.

#### Scenario: Late registration
```gherkin
Given a root scope that has been started
When the consumer registers lazy Starter "late" and invokes it
Then "late" is built
And "late" was started 0 times
```

<!-- cases: N/A 2 overrides: scope-level Start is phase 2 -->
<!-- cases: N/A 3, 4, 5, 9: no inputs or failure modes beyond the scenario -->
<!-- cases: N/A 7 lifecycle: registrations made during Start (for example by a provider) are unspecified in this change and are not covered by any guarantee -->
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
