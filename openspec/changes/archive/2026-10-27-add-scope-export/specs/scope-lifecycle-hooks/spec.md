# Spec Delta

<!-- scope-lifecycle-hooks is introduced by consolidate-pending-lifecycle-work, which must be archived before this change. The MODIFIED block copies its requirement whole. -->

## MODIFIED Requirements

### Requirement: Scope Closing and Closed hooks bracket a child scope shutdown
When a child scope shuts down, directly or as part of its parent's shutdown, `HookScopeClosing` SHALL run with that scope before its services are shut down and `HookScopeClosed` after. They SHALL run once per shutdown of each child scope, children before their parent, except a child exporting into its parent (scope-export). A shutdown of a child scope that finds one of it running SHALL wait for it, bounded by its context, and return a copy of its report instead of walking the scope again.

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

#### Scenario: Closing and Closed around an exporting child
```gherkin
Given a root scope with child "tenant" and grandchild "jobs"
And a built lazy "*app.Pool" in "jobs" exported into "tenant" with doger.Export[*app.Pool](jobs), recording "shutdown jobs"
And a built lazy "*app.Worker" in "tenant" invoking "*app.Pool", recording "shutdown tenant"
And <more>
And HookScopeClosing and HookScopeClosed hooks recording "closing <scope>" and "closed <scope>"
When the consumer calls tenant.Shutdown()
Then the recorded events are <events>

Examples:
  | more                                                                        | events                                                                                                  |
  | nothing more                                                                | closing tenant, shutdown tenant, closing jobs, shutdown jobs, closed jobs, closed tenant                 |
  | a built lazy "x" in "jobs" invoking "*app.Worker", recording "shutdown x"   | closing tenant, closing jobs, shutdown x, shutdown tenant, shutdown jobs, closed jobs, closed tenant     |
```

#### Scenario: An exporting child with no service left closes once
```gherkin
Given a root scope with child "tenant" and grandchild "jobs"
And a built lazy "*app.Pool" in "jobs" exported into "tenant" with doger.Export[*app.Pool](jobs)
And a built lazy "*app.Worker" in "tenant" invoking "*app.Pool", recording "shutdown tenant"
And HookScopeClosing and HookScopeClosed hooks recording "closing <scope>" and "closed <scope>"
And the consumer called doger.ShutdownNamed(jobs, "*app.Pool")
When the consumer calls tenant.Shutdown()
Then "closing jobs" and "closed jobs" are each recorded once, "closed jobs" right after "closing jobs"
And both are recorded after "closing tenant" and before "closed tenant"
```

#### Scenario: Shutdowns of an exporting child join its parent's shutdown
```gherkin
Given a root scope, inside synctest.Test, with counting HookScopeClosing and HookScopeClosed hooks
And <containers>
And <running> is running in another goroutine, blocked as described
When the consumer calls <second> in another goroutine, waits until every goroutine is blocked, then releases the block
Then <outcome>

Examples:
  | containers | running | second | outcome |
  | child "redis" with a value "*app.Pool" exported with doger.Export[*app.Pool](redis), and a root service "app" invoking "*app.Pool" whose first Shutdown blocks | root.Shutdown(), blocked in the Shutdown of "app" | redis.Shutdown() | both reports succeed; each hook ran once; "app" and the pool were shut down once, the pool after "app" |
  | child "redis" with a value "*app.Pool" exported with doger.Export[*app.Pool](redis), and a HookScopeClosing that blocks its first call | redis.Shutdown(), blocked in that hook | root.Shutdown() | both reports succeed; each hook ran once; the pool was shut down once |
  | child "tenant" with grandchild "jobs", a value "*app.Pool" in "jobs" exported into "tenant", a "worker" in "tenant" invoking "*app.Pool" whose first Shutdown blocks, every HookScopeClosing panicking, and a custom Logf that panics when it logs a hook panic | tenant.Shutdown(), blocked in the Shutdown of "worker" | jobs.Shutdown() | the first call panics with the Logf value; jobs.Shutdown() returns a failed report containing "DI: scope shutdown did not complete" |
```

<!-- cases: 1 default: no hooks registered keeps shutdown unchanged (existing scope tests) -->
<!-- cases: N/A 2 overrides: Closing/Closed only observe -->
<!-- cases: N/A 3 zero values: covered by the registration requirement -->
<!-- cases: 4 boundaries: no child (root only), one child, nested grandchild, exporting grandchild, an exporting grandchild's service using a parent dependent, an exporting grandchild with no service left -->
<!-- cases: N/A 5 errors: shutdown errors stay in the report -->
<!-- cases: 6 duplicates: a child shut down directly stays attached to its parent, so a later parent shutdown fires its hooks again (last row) -->
<!-- cases: 7 lifecycle: direct child shutdown, as part of root shutdown, and an exporting child (closes inside its parent's Closing/Closed pair, after the parent's services that depend on its exports) -->
<!-- cases: 8 concurrency: overlapping shutdowns of one scope join (direct, through root Shutdown, bounded by a context, after a winner panic), and so do shutdowns of an exporting child and of its parent, in either order and after a panic in the parent's pass; sibling scopes shut down in parallel, so hooks for siblings may run concurrently; documented, not ordered -->
<!-- cases: N/A 9 panics: covered by the next requirement -->
<!-- cases: 10 compatibility: BEHAVIOR CHANGE (flagged) - the exception applies only to scopes that export into their parent; every other scope keeps children-first order -->
