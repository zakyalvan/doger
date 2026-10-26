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
`Scope.Start` on a child scope SHALL run the `HookScopeStarting` hooks with that scope before starting anything; a non-nil error or a panic SHALL abort it with an error wrapping the hook's error. `HookScopeStarted` hooks SHALL run after the scope's services started successfully.

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
  | panics with "veto-boom"      | an error containing "veto-boom"                          | 0      | nothing  |
```

<!-- cases: 1 default: the nil row -->
<!-- cases: N/A 2 overrides: veto is the override -->
<!-- cases: N/A 3 zero values, 4 boundaries: one hook per row -->
<!-- cases: 5 errors: veto error and panic rows -->
<!-- cases: N/A 6 duplicates: a second Scope.Start starts nothing new and is covered in container-start -->
<!-- cases: 7 lifecycle: Scope.Start before the root started is covered in container-start (ErrNotStarted, no hooks) -->
<!-- cases: N/A 8 concurrency: covered in container-start -->
<!-- cases: 9 panics: the panic row -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Scope hooks receive a non-nil context
Every scope hook SHALL receive a non-nil context: a nil context passed to `Scope.ShutdownWithContext` or `Scope.StartWithContext` SHALL reach the hooks as `context.Background()`, as for container hooks.

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
When a child scope shuts down, directly or as part of its parent's shutdown, `HookScopeClosing` SHALL run with that scope before its services are shut down and `HookScopeClosed` after. They SHALL run once per shutdown of each child scope, children before their parent.

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
<!-- cases: N/A 8 concurrency: sibling scopes shut down in parallel today, so hooks for siblings may run concurrently; documented, not ordered -->
<!-- cases: N/A 9 panics: covered by the next requirement -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Scope hook panics follow the container panic policy
A panic in a `HookScopeStarted`, `HookScopeClosing` or `HookScopeClosed` hook SHALL be recovered so the operation completes, and then passed to `HookContainerPanic` with the point name `scope-started`, `scope-closing` or `scope-closed`; when `HookContainerPanic` is nil it SHALL re-panic with the original value after the operation completed.

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
```

<!-- cases: N/A 2 overrides: HookContainerPanic is the override -->
<!-- cases: N/A 3 zero values, 4 boundaries: one panicking hook per row -->
<!-- cases: N/A 5 errors: a Starting hook panic is a veto (previous requirement) -->
<!-- cases: N/A 6 duplicates: one panic per operation -->
<!-- cases: 7 lifecycle: the operation completes before the panic is dispatched -->
<!-- cases: N/A 8 concurrency: dispatch happens on the caller's goroutine -->
<!-- cases: 9 panics: the subject of this requirement -->
<!-- cases: N/A 10 compatibility: new API -->
