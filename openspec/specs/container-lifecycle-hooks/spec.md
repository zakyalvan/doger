# container-lifecycle-hooks Specification

## Purpose

Lets consumers run container-wide logic once when the root container starts, becomes ready, fails to start, begins closing, and finishes closing.

## Requirements

### Requirement: Starting hooks run first and can abort Start
Each registered `HookContainerStarting` hook SHALL run on the calling goroutine after the one-shot guard of `Start` and before its context check, discovery or any instantiation. If one returns a non-nil error, the remaining Starting hooks SHALL NOT run, no service SHALL be instantiated or started, and `Start` SHALL return an error that wraps the hook's error.

#### Scenario: Starting hook allows or vetoes Start
```gherkin
Given a root container with a lazy Starter service "server" whose provider records that it was built
And HookContainerStarting hooks returning <results> in registration order
When the consumer calls Start
Then Start returns <start_error>
And the provider of "server" was called <builds> times
And the number of Starting hooks that ran is <ran>

Examples:
  | results                            | start_error                     | builds | ran |
  | nil                                | nil                             | 1      | 1   |
  | nil, nil                           | nil                             | 1      | 2   |
  | errors.New("missing DB_URL")       | error wrapping "missing DB_URL" | 0      | 1   |
  | nil, errors.New("no license"), nil | error wrapping "no license"     | 0      | 2   |
```

#### Scenario: Starting hooks run before the context check
```gherkin
Given a root container with a lazy Starter service "server" and a HookContainerStarting hook that <behavior>
And a context that is already cancelled
When the consumer calls StartWithContext with that context
Then Start returns an error for which errors.Is(err, context.Canceled) is true
And the Starting hook ran 1 time
And the provider of "server" was called 0 times

Examples:
  | behavior          |
  | returns ctx.Err() |
  | returns nil       |
```

<!-- cases: N/A 2 overrides: veto is the hook's own return value; there is no option -->
<!-- cases: N/A 4 boundaries: zero, one and several hooks are covered by the veto rows and "Started and Ready fire for an empty container" -->
<!-- cases: N/A 6 duplicates: a hook registered twice simply runs twice; covered by the ordering requirement -->
<!-- cases: N/A 7 lifecycle: Shutdown from a Starting hook is covered by "Starting and Closing hooks must not call Shutdown synchronously" -->
<!-- cases: N/A 8 concurrency: covered by "Container hooks fire at most once per container" -->
<!-- cases: N/A 9 panics: covered by "Starting hook panics are vetoes" -->
<!-- cases: N/A 10 compatibility: new API; no hook registered means no behavior change (covered by the root-only requirement) -->

### Requirement: Hook contexts
Starting hooks SHALL receive a context derived from the `Start` context that keeps its values. Started, Ready and Failed hooks SHALL receive the context passed to `Start`. Closing and Closed hooks SHALL receive the context passed to `Shutdown`. A nil context passed to `StartWithContext` or `ShutdownWithContext` SHALL reach hooks as a non-nil context. Every hook SHALL receive the root container it is registered on.

#### Scenario: Starting hook receives the Start context
```gherkin
Given a root container with a HookContainerStarting hook that records its context and root
And a context carrying the value "trace-id" = "abc"
When the consumer calls StartWithContext with that context
Then the hook received a context whose "trace-id" value is "abc"
And the hook received the same root container
```

#### Scenario: Nil context reaches hooks as non-nil
```gherkin
Given a root container with one hook registered for each of <hooks> that records whether its context is nil
When the consumer calls <operation> with a nil context
Then every recorded context is non-nil
And no hook panicked

Examples:
  | hooks                     | operation           |
  | Starting, Started, Ready  | StartWithContext    |
  | Closing, Closed           | ShutdownWithContext |
```

#### Scenario: Observer hooks receive the operation's context
```gherkin
Given a root container with a <hook> hook that records the "trace-id" value of its context
And <setup>
When the consumer calls <operation> with a context carrying "trace-id" = "abc"
Then the hook recorded "abc"

Examples:
  | hook    | setup                                         | operation           |
  | Started | nothing else                                  | StartWithContext    |
  | Ready   | nothing else                                  | StartWithContext    |
  | Failed  | a Starting hook that returns errVeto          | StartWithContext    |
  | Closing | nothing else                                  | ShutdownWithContext |
  | Closed  | nothing else                                  | ShutdownWithContext |
```

<!-- cases: N/A 2 overrides: contexts are not configurable -->
<!-- cases: N/A 4 boundaries, 5 errors, 6 duplicates: context passing has no limits or failure modes -->
<!-- cases: N/A 7 lifecycle, 8 concurrency: cancellation of the Starting context by Shutdown is covered by "Shutdown during an in-flight Start" -->
<!-- cases: N/A 9 panics: covered by the panic requirements -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Started and Ready hooks mark a successful start
When the start pipeline returns nil and the close phase is still open, `Start` SHALL release the running Start (start-lifecycle D6), then run each `HookContainerStarted` hook, then each `HookContainerReady` hook, on the calling goroutine, and then return nil. Started SHALL therefore run after every Starter and every `StageAfterStart` handler, including the built-in `Runnable` handler.

#### Scenario: Hook order relative to the start pipeline
```gherkin
Given a root container whose recorder logs every lifecycle step
And a Starter service "server" and a Runnable service "migrate"
And one hook registered for each of Starting, Started and Ready
When the consumer calls Start
Then Start returns nil
And the recorded order is "starting-hook, server.Start, migrate.Run, started-hook, ready-hook"
```

#### Scenario: Started and Ready fire for an empty container
```gherkin
Given a root container with no services
And one hook registered for each of Starting, Started, Ready and Failed
When the consumer calls Start
Then Start returns nil
And the recorded order is "starting-hook, started-hook, ready-hook"
And the Failed hook did not run
```

<!-- cases: N/A 2 overrides: firing points are fixed -->
<!-- cases: N/A 3 zero values: an empty container is covered above; nil hooks are covered by the ordering requirement -->
<!-- cases: N/A 5 errors: these hooks have no error return; failures are covered by the Failed requirement -->
<!-- cases: N/A 6 duplicates: covered by the ordering requirement -->
<!-- cases: N/A 7 lifecycle: Shutdown from these hooks is covered by "Post-start and Closed hooks may shut the container down" -->
<!-- cases: N/A 8 concurrency: covered by "Shutdown during an in-flight Start" and the at-most-once requirement -->
<!-- cases: N/A 9 panics: covered by "Observer hook panics are recovered and re-raised" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Failed hooks observe a failed start
When `Start` fails after its one-shot guard, it SHALL release the running Start, run each `HookContainerFailed` hook with the exact error it is about to return, and then return that error. Failures include a Starting veto, a cancelled context, a provider, Starter, kind-handler or Runner error, and a `Shutdown` that began before the pipeline finished. Started and Ready SHALL NOT run. Guard errors SHALL NOT run any container hook.

#### Scenario: Failure point decides which hooks run
```gherkin
Given a root container with one hook registered for each of Starting, Started, Ready and Failed
And the start pipeline fails at "<failure_point>" with cause <cause>
When the consumer calls Start
Then Start returns an error for which errors.Is(err, <cause>) is true
And the Failed hook received the same error value that Start returned
And the hooks that ran, in order, are "<hooks_ran>"

Examples:
  | failure_point                       | cause            | hooks_ran        |
  | Starting hook veto                  | boom             | starting, failed |
  | context already cancelled           | context.Canceled | starting, failed |
  | provider of a Starter service       | boom             | starting, failed |
  | StageBeforeStart kind handler       | boom             | starting, failed |
  | Start method of a Starter           | boom             | starting, failed |
  | Run method of a Runnable            | boom             | starting, failed |
  | StageAfterStart kind handler        | boom             | starting, failed |
```

#### Scenario: One-shot guard errors do not fire Failed
```gherkin
Given a root container with one hook registered for each of Starting and Failed
And the container is in state "<state>"
When the consumer calls Start
Then Start returns an error for which errors.Is(err, <error>) is true
And neither the Starting nor the Failed hook ran during this call

Examples:
  | state                          | error                 |
  | started                        | ErrAlreadyStarted     |
  | failed (after a Starting veto) | ErrAlreadyStarted     |
  | shut down                      | ErrStartAfterShutdown |
```

<!-- cases: N/A 2 overrides: firing points are fixed -->
<!-- cases: N/A 3 zero values: an error is always non-nil when Failed runs -->
<!-- cases: N/A 6 duplicates: covered by the ordering requirement -->
<!-- cases: N/A 7 lifecycle: Shutdown from a Failed hook is covered by "Post-start and Closed hooks may shut the container down" -->
<!-- cases: N/A 8 concurrency: covered by "Shutdown during an in-flight Start" -->
<!-- cases: N/A 9 panics: a pipeline panic has its own requirement; Failed hook panics are covered by "Observer hook panics are recovered and re-raised" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A pipeline panic runs Failed hooks before it is re-raised
When a panic escapes the start pipeline (from a per-service hook such as `HookBeforeStart`, `HookAfterStart` or `HookAfterBuild`; a Starter's or handler's own panic is already an error), `Start` SHALL release the running Start and run each Failed hook with an error wrapping the panic value, as a recovered Start panic does, and then re-raise the original value on the calling goroutine. A Failed hook panic then SHALL go to `HookContainerPanic` when set, and be logged otherwise.

#### Scenario: A pipeline panic runs Failed hooks before it is re-raised
```gherkin
Given a root container created with StartParallelism <n> and a <hook> hook that panics with <value>
And one hook registered for each of Starting, Started, Ready and Failed
And lazy Starters "a" and "b"
When the consumer calls Start
Then Start panics on the calling goroutine with <value>
And the hooks that ran, in order, are "starting, failed"
And the Failed hook received an error <check>
And Phase() is PhaseFailed afterwards

Examples:
  | n | hook            | value   | check                                  |
  | 1 | HookBeforeStart | errBoom | for which errors.Is(err, errBoom) is true |
  | 1 | HookAfterBuild  | "boom"  | whose message contains "boom"          |
  | 4 | HookAfterStart  | errBoom | for which errors.Is(err, errBoom) is true |
```

#### Scenario: A Failed hook panic does not replace the pipeline panic
```gherkin
Given a root container with a HookBeforeStart hook that panics with "pipeline"
And a Failed hook that panics with "failed-hook"
And HookContainerPanic <policy>
And lazy Starter "a"
When the consumer calls Start
Then Start panics with "pipeline"
And <outcome>

Examples:
  | policy                             | outcome                                              |
  | nil                                | the logger received a line containing "failed-hook" |
  | set to a function recording panics | the policy recorded point "failed" with "failed-hook" |
```

<!-- cases: 1 default: the n = 1 rows; the nil-policy row -->
<!-- cases: 2 overrides: HookContainerPanic receives a Failed hook panic -->
<!-- cases: N/A 3 zero values, 4 boundaries: one panicking hook per row -->
<!-- cases: 5 errors: Failed receives an error wrapping the panic value -->
<!-- cases: 6 duplicates: the pipeline panic wins over a Failed hook panic -->
<!-- cases: 7 lifecycle: the phase is failed and Started/Ready do not run -->
<!-- cases: 8 concurrency: the parallel row re-raises on the calling goroutine -->
<!-- cases: 9 panics: the subject -->
<!-- cases: 10 compatibility: BEHAVIOR CHANGE (flagged) - before, Failed hooks did not run when a per-service hook panicked during Start -->

### Requirement: Post-start and Closed hooks may shut the container down
A Started, Ready, Failed or Closed hook SHALL be able to call root `Shutdown` synchronously without deadlocking, because the running Start or Shutdown is released before those hooks run (start-lifecycle D6). That call SHALL return a report, Closing and Closed SHALL each fire exactly once in total, and the outer operation SHALL still return its own result.

#### Scenario: Post-start or Closed hook calls Shutdown
```gherkin
Given a root container with a built Shutdowner service "db" and one counting hook registered for each of Closing and Closed
And <setup>
And a <hook> hook that calls root.Shutdown() and records the report
When the consumer calls <operation> within a 1s test timeout
Then <operation> returns <result>
And the counting Closing hook ran exactly 1 time
And the counting Closed hook ran exactly 1 time
And the recorded report has Succeed true

Examples:
  | hook    | setup                                               | operation | result                                          |
  | Started | a Starter service "server"                          | Start     | nil                                             |
  | Ready   | a Starter service "server"                          | Start     | nil                                             |
  | Failed  | a Starter service "server" whose Start returns boom | Start     | an error for which errors.Is(err, boom) is true |
  | Closed  | no Starter                                          | Shutdown  | a report with Succeed true                      |
```

<!-- cases: N/A 2 overrides, 3 zero values, 4 boundaries: one Shutdown call per row is sufficient -->
<!-- cases: N/A 5 errors: Shutdown has no error return; the report is asserted -->
<!-- cases: N/A 6 duplicates: a second Shutdown is covered by the at-most-once requirement -->
<!-- cases: N/A 8 concurrency: covered by "Shutdown during an in-flight Start" -->
<!-- cases: N/A 9 panics: covered by the panic requirements -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Starting and Closing hooks must not call Shutdown synchronously
A `HookContainerStarting` or `HookContainerClosing` hook MUST NOT call root `Shutdown` without a deadline: that call waits for the running Start or Shutdown (start-lifecycle D6). `ShutdownWithContext` called from such a hook with a deadline SHALL return when the deadline expires with a failed report. A Start whose Starting hook did so SHALL fail with an error wrapping `ErrStartAfterShutdown`.

#### Scenario: Starting or Closing hook calls Shutdown with a deadline
```gherkin
Given a root container with counting Closing, Closed and Failed hooks
And a <hook> hook that calls root.ShutdownWithContext with a 1s deadline, records the report and returns <returns>
When the consumer calls <operation> inside synctest.Test
Then the recorded report has Succeed false and every error in it matches context.DeadlineExceeded
And <outcome>
And the counting Closing hook ran exactly 1 time
And the counting Closed hook ran exactly 1 time

Examples:
  | hook     | returns | operation | outcome                                                                                              |
  | Starting | nil     | Start     | Start returns an error matching ErrStartAfterShutdown and the Failed hook ran 1 time                 |
  | Starting | errVeto | Start     | Start returns an error matching both ErrStartAfterShutdown and errVeto and the Failed hook ran 1 time |
  | Closing  | nil     | Shutdown  | Shutdown returns a report with Succeed true and the Failed hook ran 0 times                          |
```

<!-- cases: N/A 2 overrides: the deadline is the consumer's escape hatch -->
<!-- cases: N/A 3 zero values: Shutdown without a deadline is the forbidden call and is documented, not tested -->
<!-- cases: N/A 4 boundaries, 6 duplicates: a single calling hook per row is sufficient -->
<!-- cases: N/A 9 panics: covered by "Starting hook panics are vetoes" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Closing and Closed hooks bracket the root shutdown
The root `Shutdown` call that moves the close phase from open to closing (start-lifecycle D6), including through `ShutdownOnSignals*`, SHALL run each `HookContainerClosing` hook before any service is shut down. Once all services are shut down and the close phase is closed, it SHALL run each `HookContainerClosed` hook with the `*ShutdownReport` it returns. This holds whether or not `Start` ran or succeeded. Mutating the report from a hook is unsupported.

#### Scenario: Closing and Closed around service shutdown from every state
```gherkin
Given a root container in state "<state>" with a built Shutdowner service "db"
And one hook registered for each of Closing and Closed
When the consumer calls Shutdown
Then the recorded order is "closing-hook, db.Shutdown, closed-hook"
And the Closed hook received the same report pointer that Shutdown returned

Examples:
  | state                       |
  | configured (never started)  |
  | ready                       |
  | failed                      |
```

#### Scenario: Closed hook sees shutdown errors in the report
```gherkin
Given a root container with a built Shutdowner service "db" whose Shutdown returns errors.New("flush failed")
And one Closed hook that records the report
When the consumer calls Shutdown
Then the recorded report has Succeed false
And the recorded report has one error for service "db"
```

#### Scenario: Signal-driven shutdown fires the hooks
```gherkin
Given a root container with one hook registered for each of Closing and Closed
And a context that is cancelled before any signal arrives
When the consumer calls ShutdownOnSignalsWithContext with that context
Then the Closing hook ran once
And the Closed hook ran once
```

<!-- cases: N/A 2 overrides: firing points are fixed -->
<!-- cases: N/A 3 zero values: a nil Shutdown context is covered by "Hook contexts" -->
<!-- cases: N/A 4 boundaries: zero services is the "configured" row without "db" being relevant; one service is sufficient -->
<!-- cases: N/A 5 errors: Closing and Closed have no error return and cannot veto -->
<!-- cases: N/A 6 duplicates: repeated Shutdown is covered by the at-most-once requirement -->
<!-- cases: N/A 8 concurrency: covered by the at-most-once requirement and "Shutdown during an in-flight Start" -->
<!-- cases: N/A 9 panics: covered by "Observer hook panics are recovered and re-raised" -->
<!-- cases: N/A 10 compatibility: covered by "No hooks registered leaves behavior unchanged" -->

### Requirement: Shutdown during an in-flight Start
If root `Shutdown` begins while the start pipeline runs, it SHALL cancel the running Start with cause `ErrStartAfterShutdown` and fire Closing only after the pipeline returned or its own context ended (start-lifecycle D6). `Start` SHALL then skip Started and Ready and run Failed with an error wrapping `ErrStartAfterShutdown`, even if the pipeline returned nil. Once the pipeline has returned, Closing and Closed MAY run before or while Started, Ready or Failed hooks run.

#### Scenario: Shutdown races a running Start
```gherkin
Given a root container with a built Shutdowner service "db" and one hook registered for each of Started, Ready, Failed, Closing and Closed
And <setup>
When the consumer calls Start in one goroutine and Shutdown in another at "<shutdown_point>", inside synctest.Test with channel handshakes
Then Start returns <start_result>
And Started ran <started> times, Ready <ready>, Failed <failed>, Closing 1 and Closed 1
And <ordering>

Examples:
  | shutdown_point            | setup                                                                                                   | start_result                                                     | started | ready | failed | ordering                                                                    |
  | Start finishes first      | a Started hook that blocks until Shutdown has returned                                                  | nil                                                              | 1       | 1     | 0      | the recorded order is "started-hook, closing-hook, closed-hook, ready-hook" |
  | Shutdown cancels Start    | a Starter "a" whose Start(ctx) blocks until ctx.Done() and returns ctx.Err()                            | an error for which errors.Is(err, ErrStartAfterShutdown) is true | 0       | 0     | 1      | the Closing hook ran after "a".Start returned                               |
  | Shutdown in the last step | a StageAfterStart kind handler that starts Shutdown in a goroutine, waits until synctest.Wait shows it blocked, and returns nil | an error for which errors.Is(err, ErrStartAfterShutdown) is true | 0       | 0     | 1      | the Closing hook ran after the handler returned                             |
```

#### Scenario: Shutdown interrupts a Starting hook or a late pipeline step
```gherkin
Given a root container inside synctest.Test with a Failed hook recording its error
And <setup>
When the consumer calls Start
Then Start returns an error for which errors.Is(err, ErrStartAfterShutdown) is true
And the Failed hook received that same error

Examples:
  | setup                                                                                                                                         |
  | a Starting hook that starts Shutdown in a goroutine, waits for ctx.Done() and returns ctx.Err()                                                |
  | lazy "x" registered as any with kind "k" for Job, whose provider starts Shutdown in a goroutine, waits until it is blocked and returns a non-Job value |
  | lazy Starter "scheduler", lazy Starter "job" whose provider invokes "scheduler", DeclareDependency(root, "scheduler", "job") before Start, and a StageBeforeStart handler of kind "k" for lazy "j" that starts Shutdown in a goroutine, waits until it is blocked and returns nil, so the cycle check fails |
```

#### Scenario: A pipeline panic after Shutdown began
```gherkin
Given a root container inside synctest.Test created with StartParallelism <n>, with a Failed hook recording its error
And a HookAfterStart hook that panics with errBoom for "a"
And lazy Starter "a" whose Start(ctx) blocks until ctx.Done() and returns nil, <others>
When the consumer calls Start, and Shutdown() is called in another goroutine once "a" has entered Start
Then Start panics with errBoom
And the Failed hook received an error matching both errBoom and ErrStartAfterShutdown

Examples:
  | n | others                                  |
  | 1 | and no other service                    |
  | 4 | and lazy Starter "b" that returns nil   |
```

#### Scenario: A start finishing as Shutdown begins
```gherkin
Given 20000 fresh root containers, each with a StageAfterStart handler of kind "k" for lazy "j" that signals another goroutine to call Shutdown() and then returns nil
When the consumer calls <operation> on each and waits for its Shutdown
Then every call returns nil or an error for which errors.Is(err, ErrStartAfterShutdown) is true

Examples:
  | operation                                                   |
  | Start()                                                     |
  | Refresh(), after Start() and registering "j" with the handler |
```

<!-- cases: N/A 2 overrides: the Shutdown context deadline is the only control and is covered by "Starting and Closing hooks must not call Shutdown synchronously" -->
<!-- cases: N/A 3 zero values, 4 boundaries: one Start and one Shutdown are sufficient; many callers are covered by the at-most-once requirement -->
<!-- cases: 5 errors: the cancellation error is asserted in each row, and holds when the pipeline returns at the moment Shutdown begins -->
<!-- cases: N/A 9 panics: covered by the panic requirements -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Container hooks run synchronously in registration order
Each container hook list SHALL run on the goroutine that called `Start` or `Shutdown`: first the hooks from `InjectorOpts`, in slice order, then those added with `RootScope.AddContainer<State>Hook`, in call order. A nil hook SHALL be skipped, and adding a nil hook SHALL be a no-op.

#### Scenario: Registration order
```gherkin
Given InjectorOpts.HookContainerStarted contains hooks "opt-1" and "opt-2"
And RootScope.AddContainerStartedHook was called with "add-1" then "add-2"
When the consumer calls Start
Then the Started hooks ran in the order "opt-1, opt-2, add-1, add-2"
And every hook ran before Start returned
```

#### Scenario: Nil hooks are ignored
```gherkin
Given InjectorOpts.<field> contains nil followed by a recording hook
And RootScope.<adder> was called with nil
And <setup>
When the consumer runs <operation>
Then <operation> completes without panicking
And the recording hook ran once

Examples:
  | field                   | adder                       | setup                                  | operation |
  | HookContainerStarting   | AddContainerStartingHook    | no other hooks                         | Start     |
  | HookContainerStarted    | AddContainerStartedHook     | no other hooks                         | Start     |
  | HookContainerReady      | AddContainerReadyHook       | no other hooks                         | Start     |
  | HookContainerFailed     | AddContainerFailedHook      | a Starting hook that returns boom      | Start     |
  | HookContainerClosing    | AddContainerClosingHook     | no other hooks                         | Shutdown  |
  | HookContainerClosed     | AddContainerClosedHook      | no other hooks                         | Shutdown  |
```

<!-- cases: N/A 2 overrides: ordering is fixed -->
<!-- cases: N/A 4 boundaries: zero hooks is covered by the root-only requirement; four is sufficient for ordering -->
<!-- cases: N/A 5 errors: ordering has no error path; errors are covered by the Starting and Failed requirements -->
<!-- cases: N/A 6 duplicates: the same function added twice runs twice; ordering covers it -->
<!-- cases: N/A 7 lifecycle: hooks added during a run are documented (design Risks), not specified -->
<!-- cases: N/A 8 concurrency: registration is configuration-time only, like the existing Add*Hook methods -->
<!-- cases: N/A 9 panics: covered by the panic requirements -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Clone copies container hooks but not state
`RootScope.Clone` SHALL copy the registered container hooks and `HookContainerPanic` into the clone but not the lifecycle phases. `RootScope.CloneWithOpts(opts)` SHALL use only the container hooks in `opts`.

#### Scenario: Clone copies hooks but not state
```gherkin
Given a root container with a Started hook that counts calls
And the container has been started
When the consumer clones it with <clone_call> and calls Start on the clone
Then Start on the clone returns nil
And the Started hook count is <count>

Examples:
  | clone_call                          | count |
  | Clone()                             | 2     |
  | CloneWithOpts(&InjectorOpts{})      | 1     |
```

<!-- cases: N/A 2 overrides: CloneWithOpts is the override and is a row -->
<!-- cases: N/A 3 zero values: CloneWithOpts with empty options is a row -->
<!-- cases: N/A 4 boundaries, 5 errors, 6 duplicates: cloning has no limits or failure modes -->
<!-- cases: N/A 8 concurrency: Clone keeps the existing locking -->
<!-- cases: N/A 9 panics: no panicking path -->
<!-- cases: 10 compatibility: CloneWithOpts keeps its existing rule of using only the given options -->

### Requirement: Observer hook panics are recovered and re-raised
A panic in a Started, Ready, Failed, Closing or Closed hook SHALL be recovered. The remaining hooks of that list and the rest of `Start` or `Shutdown`, including its phase transitions, SHALL still run. Once the operation is complete, the first recovered panic SHALL be passed on the calling goroutine to `InjectorOpts.HookContainerPanic` with the hook point name; later ones SHALL be logged via `Logf`. When `HookContainerPanic` is nil, the default SHALL re-panic with the recovered value.

#### Scenario: Default re-panics after the operation completes
```gherkin
Given a root container with a built Shutdowner service "db" and no HookContainerPanic
And <setup>
And a <hook> hook that panics with "kaboom", followed by a recording hook in the same list
When the consumer calls <operation> and recovers its panic
Then the recovered value is "kaboom"
And <completed> before the panic reached the caller
And a later call to Start returns an error for which errors.Is(err, <later_error>) is true

Examples:
  | hook    | setup                                               | operation | completed                                              | later_error           |
  | Started | a Ready hook that records its call                  | Start     | the following Started hook and the Ready hook ran      | ErrAlreadyStarted     |
  | Ready   | no other hooks                                      | Start     | the following Ready hook ran                           | ErrAlreadyStarted     |
  | Failed  | a Starter service "server" whose Start returns boom | Start     | the following Failed hook ran                          | ErrAlreadyStarted     |
  | Closing | a Closed hook that records its call                 | Shutdown  | "db" was shut down and the following Closing and the Closed hook ran | ErrStartAfterShutdown |
  | Closed  | no other hooks                                      | Shutdown  | the following Closed hook ran                          | ErrStartAfterShutdown |
```

#### Scenario: HookContainerPanic overrides the default
```gherkin
Given a root container whose HookContainerPanic records the hook point and the recovered value
And a <hook> hook that panics with "kaboom", followed by a <hook> hook that panics with "boom"
And a Logf recorder
When the consumer calls <operation>
Then <operation> returns <result> without panicking
And HookContainerPanic was called exactly 1 time with hook "<point>" and value "kaboom"
And Logf received a message containing "boom"

Examples:
  | hook    | operation | result                     | point   |
  | Started | Start     | nil                        | started |
  | Closing | Shutdown  | a report with Succeed true | closing |
```

<!-- cases: N/A 3 zero values: panic(nil) surfaces as *runtime.PanicNilError (Go 1.21+) and is handled like any panic value -->
<!-- cases: N/A 4 boundaries: one and two panicking hooks are covered; more add nothing -->
<!-- cases: N/A 5 errors: observer hooks have no error return -->
<!-- cases: N/A 6 duplicates: covered by the two-panic override rows -->
<!-- cases: N/A 8 concurrency: panics are handled on the calling goroutine only -->
<!-- cases: 10 compatibility: the default propagates the panic, like per-service hooks do -->

### Requirement: Starting hook panics are vetoes
A panic in a `HookContainerStarting` hook SHALL be recovered and treated as a veto: the remaining Starting hooks SHALL NOT run, no service SHALL be instantiated, Failed hooks SHALL run, and `Start` SHALL return an error containing the panic value, wrapping it with `%w` when it is an error. It SHALL NOT be passed to `HookContainerPanic`.

#### Scenario: Panicking Starting hook
```gherkin
Given a root container with a lazy Starter service "server" whose provider records that it was built
And a HookContainerPanic that records its calls
And a Starting hook that panics with <value>, followed by a recording Starting hook
When the consumer calls Start
Then Start returns an error for which <match>
And the following Starting hook did not run
And the provider of "server" was called 0 times
And the Failed hook ran 1 time
And HookContainerPanic was called 0 times

Examples:
  | value              | match                                     |
  | string "kaboom"    | the error message contains "kaboom"       |
  | error ErrKaboom    | errors.Is(err, ErrKaboom) is true         |
```

<!-- cases: N/A 2 overrides: recover inside the hook to allow Start -->
<!-- cases: N/A 3 zero values: panic(nil) surfaces as *runtime.PanicNilError, an error, and follows the error row -->
<!-- cases: N/A 4 boundaries, 6 duplicates: one panicking hook is sufficient -->
<!-- cases: N/A 7 lifecycle, 8 concurrency: a veto is a failed Start, covered by the Failed and at-most-once requirements -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Container hooks fire at most once per container
Each phase transition SHALL happen at most once for a root container. Concurrent or repeated `Start` calls SHALL run the Starting, Started, Ready and Failed hooks at most once in total. Concurrent or repeated root `Shutdown` calls SHALL run Closing and Closed exactly once in total, and later calls SHALL fire no container hooks.

#### Scenario: Concurrent Start calls
```gherkin
Given a root container with a Starter service "server" and one counting hook for each of Starting, Started and Ready
When 50 goroutines call Start concurrently
Then exactly 1 call returns nil
And 49 calls return an error for which errors.Is(err, ErrAlreadyStarted) is true
And each counting hook ran exactly 1 time
And the test passes under -race with no leaked goroutines
```

#### Scenario: Repeated and concurrent Shutdown calls
```gherkin
Given a started root container with one counting hook for each of Closing and Closed
When <callers> goroutines call Shutdown <times> times each
Then the Closing hook ran exactly 1 time
And the Closed hook ran exactly 1 time
And every Shutdown call returned a report with Succeed true

Examples:
  | callers | times |
  | 1       | 2     |
  | 20      | 1     |
```

#### Scenario: Hook calling Start re-entrantly
```gherkin
Given a root container whose Started hook calls Start on the same root and records the result
When the consumer calls Start
Then the outer Start returns nil
And the recorded inner result is an error for which errors.Is(err, ErrAlreadyStarted) is true
And the Started hook ran exactly 1 time
```

<!-- cases: N/A 2 overrides: the at-most-once rule has no option -->
<!-- cases: N/A 3 zero values, 4 boundaries: 1, 2 and 50 callers are covered -->
<!-- cases: N/A 5 errors: guard errors are covered by "One-shot guard errors do not fire Failed" -->
<!-- cases: N/A 9 panics: covered by the panic requirements -->
<!-- cases: N/A 10 compatibility: first-report semantics for concurrent Shutdown callers are owned by start-lifecycle D6 -->

### Requirement: Only the root container fires container hooks
Container hooks SHALL fire only for the root container's `Start` and `Shutdown`. Shutting down a child scope, including through a `Scope` or a provider's injector, SHALL NOT fire any container hook. With no container hooks registered, `Start` and `Shutdown` SHALL behave exactly as they do without this change.

#### Scenario: Child scope shutdown fires nothing
```gherkin
Given a started root container with one counting hook for each of Closing and Closed
And a child scope "request" with a built Shutdowner service "session"
When the consumer calls Shutdown on scope "request"
Then "session" was shut down
And the Closing hook ran 0 times
And the Closed hook ran 0 times
```

#### Scenario: No hooks registered leaves behavior unchanged
```gherkin
Given a root container created with <constructor> and no container hooks
And a Starter service "server" and a Shutdowner service "db"
When the consumer calls Start and then Shutdown
Then Start returns nil
And the shutdown report has Succeed true
And neither call panicked

Examples:
  | constructor                     |
  | New()                           |
  | NewWithOpts(&InjectorOpts{})    |
  | NewWithOpts(nil)                |
```

<!-- cases: N/A 2 overrides: root-only firing is not configurable; child scopes have their own hooks (capability scope-lifecycle-hooks) -->
<!-- cases: N/A 4 boundaries: there is exactly one root per container -->
<!-- cases: N/A 5 errors, 6 duplicates: no failure path -->
<!-- cases: N/A 8 concurrency: covered by the at-most-once requirement -->
<!-- cases: N/A 9 panics: covered by the panic requirements -->

### Requirement: Incremental start fires no container hooks
`RootScope.Refresh` and `Scope.Start` SHALL NOT fire any container hook: Starting, Started, Ready and Failed fire at most once per container, for its first `Start`. Per-service start hooks and kind handlers still run for the services that the incremental start starts.

#### Scenario: Refresh after Start fires no container hook
```gherkin
Given a root container with counting Starting, Started, Ready and Failed hooks
And a child scope "tenant"
And the consumer called Start(), which returned nil
And then registered lazy Starter "late" in <target>
When the consumer calls <operation>
Then <operation> returns nil
And "late" was started 1 time
And the Starting, Started, Ready and Failed hooks ran 1, 1, 1 and 0 times in total

Examples:
  | target   | operation      |
  | the root | root.Refresh() |
  | "tenant" | tenant.Start() |
```

<!-- cases: N/A 2 overrides: container hooks are once per container by design; scope hooks cover child scopes -->
<!-- cases: N/A 3 zero values, 4 boundaries: one incremental start per row is sufficient -->
<!-- cases: 5 errors: a failing incremental start does not fire Failed; it returns its error (capability container-start) -->
<!-- cases: N/A 6 duplicates: covered by the at-most-once requirement -->
<!-- cases: 7 lifecycle: Refresh after Start is the subject; Refresh before Start is covered in container-start -->
<!-- cases: N/A 8 concurrency: covered by container-start "Concurrent Refresh calls are serialized" -->
<!-- cases: N/A 9 panics: per-service hook panics propagate as specified in container-start -->
<!-- cases: N/A 10 compatibility: new API -->
