# Spec Delta

## Purpose

Lets consumers run logic when a child scope starts (through `Scope.Start`) or shuts down, for example per-tenant setup and teardown, without registering per-service hooks.

## ADDED Requirements

### Requirement: Scope hooks are registered like container hooks
`InjectorOpts` SHALL provide `HookScopeStarting []func(context.Context, *Scope) error`, `HookScopeStarted`, `HookScopeClosing` and `HookScopeClosed` (`[]func(context.Context, *Scope)`), and `RootScope` SHALL provide `AddScopeStartingHook`, `AddScopeStartedHook`, `AddScopeClosingHook` and `AddScopeClosedHook`. Hooks SHALL run in registration order; nil hooks SHALL be ignored.

#### Scenario: Registration order and nil hooks
```gherkin
Given a root scope with HookScopeStarted hooks "h1", nil and "h2" in InjectorOpts
And root.AddScopeStartedHook("h3") and root.AddScopeStartedHook(nil)
And the root container has been started
When the consumer calls tenant.Start() on a child scope "tenant"
Then the recorded calls are h1 tenant, h2 tenant, h3 tenant
And no call panicked
```

<!-- cases: 1 default: none registered means no calls (covered by "Root scope fires no scope hook") -->
<!-- cases: 2 overrides: fields and Add* methods both append to the same list -->
<!-- cases: 3 zero values: nil hooks are rows of this scenario -->
<!-- cases: N/A 4 boundaries: 0, 1 and many hooks share the same loop -->
<!-- cases: N/A 5 errors, 6 duplicates: registration has no error path -->
<!-- cases: N/A 7 lifecycle: covered by the firing requirements -->
<!-- cases: N/A 8 concurrency: registration is configuration-time, as for every other hook -->
<!-- cases: N/A 9 panics: covered by "Scope hook panics follow the container panic policy" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Scope Starting hooks can veto Scope.Start
`Scope.Start` on a child scope SHALL run the `HookScopeStarting` hooks with that scope before starting anything; a non-nil error SHALL abort it with an error wrapping the hook's error, and a panic SHALL abort it with an error reading `scope starting hook panicked: <value>` that wraps the value when it is an error, as for container Starting hooks. `HookScopeStarted` hooks SHALL run after the scope's services started successfully.

#### Scenario: Starting hook allows or vetoes Scope.Start
```gherkin
Given a started root container and a child scope "tenant" with lazy Starter "worker"
And a HookScopeStarting hook that <behavior>
And a HookScopeStarted hook recording its scope name
When the consumer calls tenant.Start()
Then the result is <result>
And "worker" was started <starts> times
And the Started hook recorded <recorded>

Examples:
  | behavior                     | result                                                   | starts | recorded |
  | returns nil                  | nil                                                      | 1      | tenant   |
  | returns errVeto              | an error matching errVeto, containing "scope `tenant`"   | 0      | nothing  |
  | panics with "veto-boom"      | an error containing "scope `tenant` start aborted: DI: scope starting hook panicked: veto-boom" | 0 | nothing |
  | panics with errVeto          | an error matching errVeto, containing "scope starting hook panicked" | 0 | nothing  |
```

<!-- cases: 1 default: the nil row -->
<!-- cases: N/A 2 overrides: veto is the override -->
<!-- cases: N/A 3 zero values, 4 boundaries: one hook per row -->
<!-- cases: 5 errors: veto error and panic rows; a panic message mirrors the container's -->
<!-- cases: N/A 6 duplicates: a second Scope.Start starts nothing new and is covered in container-start -->
<!-- cases: 7 lifecycle: Scope.Start before the root started is covered in container-start (ErrNotStarted, no hooks) -->
<!-- cases: N/A 8 concurrency: covered in container-start -->
<!-- cases: 9 panics: the panic row -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Scope Started hooks skip a scope that is shutting down
`HookScopeStarted` hooks SHALL NOT run when the scope, or one of its ancestors, was shutting down when `Scope.Start` collected its services, so that nothing of it was started. `Scope.Start` SHALL still return nil then.

#### Scenario: Scope.Start during the scope's shutdown fires no Started hook
```gherkin
Given a started root container and child scope "tenant" with lazy Starter "w", registered after Start
And a counting HookScopeStarted hook
And <shutting-down>.Shutdown() is running in another goroutine, blocked in a HookScopeClosing hook, having begun <began>
When the consumer calls tenant.Start()
Then it returns nil
And "w" was started 0 times
And the Started hook ran 0 times

Examples:
  | shutting-down                                  | began                                                                                      |
  | tenant                                         | before tenant.Start() was called                                                           |
  | "parent", the scope "tenant" was created under | before tenant.Start() was called                                                           |
  | tenant                                         | from a HookScopeStarting hook, which waits until the shutdown is blocked, then returns nil |
```

<!-- cases: N/A 1 default, 2 overrides: there is no option -->
<!-- cases: N/A 3 zero values: covered by the veto requirement -->
<!-- cases: 4 boundaries: the scope itself and an ancestor; a shutdown before Scope.Start and one beginning after its Starting hooks ran (only the run's claim sees it) -->
<!-- cases: N/A 5 errors: Scope.Start returns nil -->
<!-- cases: N/A 6 duplicates: a later Scope.Start after the shutdown finds nothing to start -->
<!-- cases: 7 lifecycle: the subject -->
<!-- cases: 8 concurrency: the shutdown runs in another goroutine; runs under -race with goleak -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: N/A 10 compatibility: new API -->

#### Scenario: A shutdown that completed before Scope.Start claimed the scope
```gherkin
Given a started root container and child scope "tenant" with lazy Starter "w", registered after Start
And a counting HookScopeStarted hook
And tenant.Start() is running in another goroutine and has collected "w", but has not claimed its candidates
When tenant.Shutdown() runs to completion
And tenant.Start() continues
Then it returns nil
And "w" was started 0 times
And the Started hook ran 1 time, as for a tenant.Start() called after tenant.Shutdown() returned
```

### Requirement: Scope Starting hooks skip a scope that is shutting down
`Scope.Start` on a child scope SHALL check, before its Starting hooks run, whether the scope or one of its ancestors is shutting down. If so, it SHALL run no scope hook, start nothing and return nil.

#### Scenario: Scope.Start during the scope's shutdown fires no scope hook
```gherkin
Given a started root container and child scope "tenant" with lazy Starter "w", registered after Start
And counting HookScopeStarting and HookScopeStarted hooks
And <shutting-down>.Shutdown() is running in another goroutine, blocked in a HookScopeClosing hook
When the consumer calls tenant.Start()
Then it returns nil
And "w" was started 0 times
And the Starting hook ran 0 times and the Started hook ran 0 times

Examples:
  | shutting-down                                  |
  | tenant                                         |
  | "parent", the scope "tenant" was created under |
```

<!-- cases: N/A 1 default, 2 overrides: there is no option -->
<!-- cases: N/A 3 zero values: covered by the veto requirement -->
<!-- cases: 4 boundaries: the scope itself and an ancestor -->
<!-- cases: N/A 5 errors: Scope.Start returns nil -->
<!-- cases: N/A 6 duplicates: a Scope.Start after the shutdown returned runs the hooks again -->
<!-- cases: 7 lifecycle: the subject; a shutdown that begins after the check is covered by "Scope Started hooks skip a scope that is shutting down" -->
<!-- cases: 8 concurrency: the shutdown runs in another goroutine; runs under -race with goleak -->
<!-- cases: N/A 9 panics: no hook runs -->
<!-- cases: 10 compatibility: BEHAVIOR CHANGE (flagged) - before, Starting hooks ran for a scope that was shutting down -->

### Requirement: Scope hooks receive a non-nil context
Every scope hook SHALL receive a non-nil context: a nil context passed to `Scope.ShutdownWithContext` or `Scope.StartWithContext` SHALL reach the hooks as a non-nil context derived from `context.Background()`, as for container hooks.

#### Scenario: Nil context reaches scope hooks as non-nil
```gherkin
Given a started root container and child scope "tenant" with scope hooks recording their contexts
When the consumer calls <operation>
Then every recorded context is non-nil
And <count> contexts were recorded

Examples:
  | operation                       | count |
  | tenant.StartWithContext(nil)    | 2     |
  | tenant.ShutdownWithContext(nil) | 2     |
```

<!-- cases: 1 default: the nil context is the subject -->
<!-- cases: N/A 2 overrides: a non-nil context is passed through unchanged (existing scenarios) -->
<!-- cases: 3 zero values: nil -->
<!-- cases: N/A 4 boundaries, 5 errors, 6 duplicates, 8 concurrency, 9 panics: none -->
<!-- cases: 7 lifecycle: start and shutdown -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Scope Closing and Closed hooks bracket a child scope shutdown
When a child scope shuts down, directly or as part of its parent's shutdown, `HookScopeClosing` SHALL run with that scope before its services are shut down and `HookScopeClosed` after. They SHALL run once per shutdown of each child scope, children before their parent. A shutdown of a child scope that finds one of it running SHALL wait for it, bounded by its context, and return a copy of its report instead of walking the scope again.

#### Scenario: Closing and Closed around child scope shutdown
```gherkin
Given a root scope with child "tenant" and grandchild "jobs", each with a built lazy service implementing Shutdown() error that records "shutdown <scope>"
And HookScopeClosing and HookScopeClosed hooks recording "closing <scope>" and "closed <scope>"
When the consumer calls <operation>
Then the recorded events are <events>

Examples:
  | operation       | events                                                                                                   |
  | tenant.Shutdown() | closing jobs, shutdown jobs, closed jobs, closing tenant, shutdown tenant, closed tenant               |
  | root.Shutdown()   | closing jobs, shutdown jobs, closed jobs, closing tenant, shutdown tenant, closed tenant                |
  | jobs.Shutdown()   | closing jobs, shutdown jobs, closed jobs                                                               |
  | tenant.Shutdown() then root.Shutdown() | closing jobs, shutdown jobs, closed jobs, closing tenant, shutdown tenant, closed tenant, closing tenant, closed tenant |
```

#### Scenario: Overlapping shutdowns of a child scope join
```gherkin
Given a root scope, inside synctest.Test, with child "tenant" holding value services "a", "b", "c" and "d" that count their Shutdown calls
And a HookScopeClosing hook that blocks its first call until released, and a counting HookScopeClosed hook
And tenant.Shutdown() is running in another goroutine, blocked in that hook
When the consumer calls <second> in another goroutine, waits until every goroutine is blocked, then releases the hook
Then <outcome>

Examples:
  | second                                                                    | outcome                                                                                                 |
  | tenant.Shutdown()                                                         | both reports succeed; each hook ran once; each service shut down once                                    |
  | root.Shutdown()                                                           | both reports succeed; each hook ran once; each service shut down once                                    |
  | tenant.ShutdownWithContext(ctx), ctx with a 1s timeout, before the release | it returns a failed report keyed by "tenant" with an empty service name, matching context.DeadlineExceeded; the first report then succeeds |
  | tenant.Shutdown(), where a custom Logf panics in the first call after its services shut down | the first call panics with that value; the second gets a failed report containing "DI: scope shutdown did not complete" |
```

#### Scenario: Root scope fires no scope hook
```gherkin
Given a root scope with counting HookScopeStarting, HookScopeStarted, HookScopeClosing and HookScopeClosed hooks and no child scope
When the consumer calls Start() and then Shutdown()
Then every counting hook ran 0 times
```

<!-- cases: 1 default: no hooks registered keeps shutdown unchanged (existing scope tests) -->
<!-- cases: N/A 2 overrides: Closing/Closed only observe -->
<!-- cases: N/A 3 zero values: covered by the registration requirement -->
<!-- cases: 4 boundaries: no child (root only), one child, nested grandchild -->
<!-- cases: N/A 5 errors: shutdown errors stay in the report -->
<!-- cases: 6 duplicates: a child shut down directly stays attached to its parent, so a later parent shutdown fires its hooks again (last row) -->
<!-- cases: 7 lifecycle: direct child shutdown and as part of root shutdown -->
<!-- cases: 8 concurrency: overlapping shutdowns of one scope join (direct, through root Shutdown, bounded by a context, after a winner panic); sibling scopes shut down in parallel, so hooks for siblings may run concurrently; documented, not ordered -->
<!-- cases: N/A 9 panics: covered by the next requirement -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Scope hook panics follow the container panic policy
A panic in a `HookScopeStarted`, `HookScopeClosing` or `HookScopeClosed` hook SHALL be recovered so the operation completes, and then passed to `HookContainerPanic` with the point name `scope-started`, `scope-closing` or `scope-closed`; when `HookContainerPanic` is nil it SHALL re-panic with the original value after the operation completed. Later panics of the same operation SHALL be logged via `Logf` with their point name, as `scope-closing hook panicked`, without calling them container hooks.

#### Scenario: Observer scope hook panics
```gherkin
Given a started root container with a child scope "tenant" holding built lazy "svc" implementing Shutdown() error
And a <hook> hook that panics with "scope-boom"
And HookContainerPanic is <policy>
When the consumer calls <operation>
Then <outcome>
And "svc" <svc-state>

Examples:
  | hook             | policy             | operation         | outcome                                                     | svc-state                    |
  | HookScopeClosing | nil                | tenant.Shutdown() | the call panics with "scope-boom"                           | had Shutdown called 1 time   |
  | HookScopeClosed  | a recording func   | tenant.Shutdown() | the call returns and the recorder saw "scope-closed", "scope-boom" | had Shutdown called 1 time |
  | HookScopeStarted | a recording func   | tenant.Start()    | the call returns nil and the recorder saw "scope-started", "scope-boom" | had Shutdown called 0 times |
  | HookScopeClosing | a recording func   | tenant.Shutdown() | the call returns and the recorder saw "scope-closing", "scope-boom" | had Shutdown called 1 time |
  | HookScopeStarted | nil                | tenant.Start()    | a second Started hook ran, then the call panics with "scope-boom" | had Shutdown called 0 times |
```

#### Scenario: Later scope hook panics are logged with their point
```gherkin
Given a started root container with a child scope "tenant", a Logf recorder and a recording HookContainerPanic
And two HookScopeClosing hooks that panic with "first" and "second"
When the consumer calls tenant.Shutdown()
Then the recorder saw "scope-closing", "first"
And Logf received a message containing "DI: scope-closing hook panicked: second"
And no Logf message contains "container scope-closing"
```

<!-- cases: N/A 2 overrides: HookContainerPanic is the override -->
<!-- cases: N/A 3 zero values, 4 boundaries: one panicking hook per row -->
<!-- cases: N/A 5 errors: a Starting hook panic is a veto (previous requirement) -->
<!-- cases: 6 duplicates: the first panic is dispatched, later ones are logged with their point -->
<!-- cases: 7 lifecycle: the operation completes before the panic is dispatched -->
<!-- cases: N/A 8 concurrency: dispatch happens on the caller's goroutine -->
<!-- cases: 9 panics: the subject of this requirement -->
<!-- cases: N/A 10 compatibility: new API -->
