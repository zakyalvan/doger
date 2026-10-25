# Spec Delta

## Purpose

Lets a consumer resolve every visible service assignable to a type in one call, as a slice or a name-keyed map, with predictable visibility, error and dependency semantics.

## ADDED Requirements

### Requirement: Collect every visible service assignable to a type
`InvokeAll[T]` SHALL return one element per visible, non-alias service whose registered type is assignable to `T`, building lazy members as needed. It SHALL NOT build services whose registered type is not assignable to `T`. A nil injector SHALL mean the default root scope.

#### Scenario: Membership by registered type
```gherkin
Given a root scope
And a lazy service "console" registered as type "*ConsoleHandler" which implements "EventHandler"
And a lazy service "audit" registered as type "*AuditHandler" which implements "EventHandler"
And a lazy service "db" registered as type "*sql.DB" whose provider counts its calls
When the consumer calls InvokeAll for type "<T>"
Then the result contains the services <members>
And the error is nil
And the provider of "db" was called <dbCalls> times

Examples:
  | T             | members              | dbCalls |
  | EventHandler  | "console", "audit"   | 0       |
  | *AuditHandler | "audit"              | 0       |
  | *sql.DB       | "db"                 | 1       |
  | fmt.Stringer  | none                 | 0       |
```

#### Scenario: Eager values are members without a provider call
```gherkin
Given a root scope
And an eager value "static" of type "*StaticHandler" which implements "EventHandler"
And a lazy service "console" registered as type "*ConsoleHandler" which implements "EventHandler"
When the consumer calls InvokeAll for type "EventHandler"
Then the result contains "static" and "console"
And the error is nil
```

#### Scenario: Nil injector uses the default root scope
```gherkin
Given the default root scope has a lazy service "console" of type "*ConsoleHandler" implementing "EventHandler"
When the consumer calls InvokeAll for type "EventHandler" with a nil injector
Then the result contains "console"
And the error is nil
```

#### Scenario: Typed-nil member is returned as is
```gherkin
Given a root scope
And a lazy service "noop" of type "*NoopHandler" whose provider returns a nil "*NoopHandler" and no error
When the consumer calls InvokeAll for type "EventHandler"
Then the result has 1 element
And that element is a nil "*NoopHandler" wrapped in "EventHandler"
And the error is nil
```

<!-- cases: N/A 2 overrides: membership has no option; ordering options are covered by collection-ordering -->
<!-- cases: N/A 4 boundaries: zero, one and several members are covered by the Stringer, *AuditHandler and EventHandler rows -->
<!-- cases: N/A 5 errors: covered by "A failing member fails the whole call" -->
<!-- cases: N/A 6 duplicates: aliases and shadowing are covered by their own requirements -->
<!-- cases: N/A 7 lifecycle: covered by "Collections after shutdown" and the self-exclusion requirement -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent collection callers" -->
<!-- cases: N/A 9 panics: provider panics are covered by "A failing member fails the whole call" -->
<!-- cases: N/A 10 compatibility: covered by "InvokeAs behavior is unchanged" -->

### Requirement: Visibility follows scope ancestry with nearest-name shadowing
A collection invoked from a scope SHALL include services of that scope and its ancestors only. When the same service name exists in several visible scopes, only the nearest one SHALL be considered, even if the nearest one is not assignable to `T`. Inside a provider, the invoking scope SHALL be the scope where that provider's service is registered.

#### Scenario: Scope visibility and shadowing
```gherkin
Given a root scope with a lazy service "<rootName>" of type "<rootType>"
And a child scope "child" of root with a lazy service "<childName>" of type "<childType>"
And a sibling scope "sibling" of root with a lazy service "s" of type "*SiblingHandler"
When the consumer calls InvokeAll for type "EventHandler" from scope "<from>"
Then the result contains the services <members>

Examples:
  | rootName | rootType        | childName | childType       | from    | members                  |
  | a        | *AHandler       | b         | *BHandler       | child   | root "a", child "b"      |
  | a        | *AHandler       | b         | *BHandler       | root    | root "a"                 |
  | x        | *AHandler       | x         | *BHandler       | child   | child "x" only           |
  | x        | *AHandler       | x         | *sql.DB         | child   | none                     |
  | x        | *AHandler       | x         | *BHandler       | sibling | root "x", sibling "s"    |
```

#### Scenario: Interface type any matches every non-alias service
```gherkin
Given a root scope with lazy services "a" of type "int" and "b" of type "string"
And an alias of "a" registered as type "any"
When the consumer calls InvokeAll for type "any"
Then the result contains exactly "a" and "b"
And both providers were called exactly 1 time
```

#### Scenario: Collection inside a provider sees the provider's scope
```gherkin
Given a root scope with a lazy service "a" of type "*AHandler" implementing "EventHandler"
And a lazy service "bus" of type "*Bus" in the root scope whose provider calls InvokeAll for type "EventHandler" and records the names
And a child scope "child" with a lazy service "b" of type "*BHandler" implementing "EventHandler"
When the consumer invokes "bus" from scope "child"
Then the recorded names are exactly "a"
```

<!-- cases: N/A 2 overrides: visibility is not configurable -->
<!-- cases: N/A 3 zero values: an empty visible set is covered by "No match yields an empty collection" -->
<!-- cases: N/A 5 errors: visibility cannot fail -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent collection callers" -->
<!-- cases: N/A 9 panics: no panicking path -->
<!-- cases: N/A 10 compatibility: matches what InvokeNamed resolves for each name -->

### Requirement: Aliases are excluded from collections
Services registered through `As`/`Bind` aliases SHALL NOT appear in a collection. The aliased original SHALL represent itself when its registered type is assignable to `T`.

#### Scenario: Alias does not duplicate its target
```gherkin
Given a root scope
And a lazy service "*AuditHandler" of type "*AuditHandler" implementing "EventHandler"
And an alias of "*AuditHandler" registered as type "EventHandler"
When the consumer calls InvokeAll for type "EventHandler"
Then the result has exactly 1 element
And that element is the instance of "*AuditHandler"
```

<!-- cases: N/A 2 overrides: alias exclusion is not configurable; register the instance directly to include it -->
<!-- cases: N/A 3 zero values: an alias-only match is impossible, because As requires its target to be assignable to the alias type -->
<!-- cases: N/A 4 boundaries: covered by the 1-element scenario above -->
<!-- cases: N/A 5 errors, 8 concurrency: alias filtering has no failure or shared state -->
<!-- cases: N/A 9 panics: no panicking path -->
<!-- cases: N/A 10 compatibility: aliases keep resolving through Invoke and InvokeAs unchanged -->

### Requirement: No match yields an empty collection
When no visible service matches, `InvokeAll[T]` SHALL return an empty, non-nil slice and `InvokeAllMap[T]` SHALL return an empty, non-nil map. Both SHALL return a nil error, never `ErrServiceNotFound` or `ErrServiceNotMatch`.

#### Scenario: Empty results
```gherkin
Given a root scope containing <services>
When the consumer calls "<function>" for type "EventHandler"
Then the result is non-nil with length 0
And the error is nil

Examples:
  | services                     | function     |
  | no services                  | InvokeAll    |
  | no services                  | InvokeAllMap |
  | only "db" of type "*sql.DB"  | InvokeAll    |
  | only "db" of type "*sql.DB"  | InvokeAllMap |
```

<!-- cases: N/A 2 overrides: no option -->
<!-- cases: N/A 7 lifecycle: covered by "Collections after shutdown" -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent collection callers" -->
<!-- cases: N/A 9 panics: no panicking path -->

### Requirement: A failing member fails the whole call
When any member fails to build, the call SHALL return no collection and an error that names the failing member and wraps its cause, so `errors.Is` matches the cause. Members already built stay built.

#### Scenario: Member failure modes
```gherkin
Given a root scope
And a lazy service "console" of type "*ConsoleHandler" implementing "EventHandler"
And a lazy service "audit" of type "*AuditHandler" implementing "EventHandler" whose provider <behavior>
When the consumer calls InvokeAll for type "EventHandler"
Then the result is nil
And the error message contains "audit"
And errors.Is(err, <cause>) is true

Examples:
  | behavior                                   | cause                 |
  | returns error ErrAuditDown                 | ErrAuditDown          |
  | panics with error ErrAuditPanic            | ErrAuditPanic         |
  | invokes "missing" which is not registered  | ErrServiceNotFound    |
```

#### Scenario: Must variants panic with the same error
```gherkin
Given a root scope
And a lazy service "audit" of type "*AuditHandler" implementing "EventHandler" whose provider returns error ErrAuditDown
When the consumer calls "<function>" for type "EventHandler"
Then the call panics with an error for which errors.Is(err, ErrAuditDown) is true

Examples:
  | function         |
  | MustInvokeAll    |
  | MustInvokeAllMap |
```

#### Scenario: Must variants return the collection on success
```gherkin
Given a root scope with lazy services "console" and "audit" implementing "EventHandler"
When the consumer calls "<function>" for type "EventHandler"
Then it does not panic
And the result has 2 elements

Examples:
  | function         |
  | MustInvokeAll    |
  | MustInvokeAllMap |
```

<!-- cases: N/A 2 overrides: failure semantics are not configurable; the caller can resolve members individually -->
<!-- cases: N/A 3 zero values: covered by "No match yields an empty collection" -->
<!-- cases: N/A 4 boundaries: one failing member among two is sufficient; the first failure stops the call -->
<!-- cases: N/A 7 lifecycle: cycles are covered by "A collection excludes the service that invokes it" -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent collection callers" -->

### Requirement: A collection excludes the service that invokes it
When a collection is invoked from inside a provider, the service that provider builds SHALL NOT be a member, even if its registered type is assignable to `T`. Any other member that is already in the invocation chain SHALL still fail the call with `ErrCircularDependency`.

#### Scenario: Composite collects every other implementation
```gherkin
Given a root scope with lazy services "console" and "audit" implementing "EventHandler"
And a lazy service "composite" of type "*CompositeHandler" implementing "EventHandler" whose provider calls InvokeAll for type "EventHandler" and records the names
When the consumer invokes "composite"
Then the error is nil
And the recorded names are exactly "console" and "audit"
```

#### Scenario: Deeper cycles still fail
```gherkin
Given a root scope
And a lazy service "x" of type "*XHandler" implementing "EventHandler" whose provider invokes "bus" by name
And a lazy service "bus" of type "*Bus" whose provider calls InvokeAll for type "EventHandler"
When the consumer invokes "x"
Then the error matches ErrCircularDependency
And the error message contains "`x` -> `bus` -> `x`"
```

<!-- cases: N/A 2 overrides: none needed; including itself can only fail with ErrCircularDependency -->
<!-- cases: N/A 3 zero values: a composite with no other member gets an empty collection, covered by the empty-result requirement -->
<!-- cases: N/A 4 boundaries, 6 duplicates: one composite and one indirect cycle are sufficient -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent collection callers" -->
<!-- cases: N/A 9 panics: no panicking path -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Name-keyed collection
`InvokeAllMap[T]` SHALL return the same members as `InvokeAll[T]`, keyed by service name, under the same visibility, alias, self-exclusion, shadowing and failure rules.

#### Scenario: Map collection keys
```gherkin
Given a root scope with lazy services "console" of type "*ConsoleHandler" and "audit" of type "*AuditHandler", both implementing "EventHandler"
And a child scope "child" with a lazy service "console" of type "*ChildConsoleHandler" implementing "EventHandler"
When the consumer calls InvokeAllMap for type "EventHandler" from scope "<from>"
Then the map has keys <keys>
And the value for "console" is the instance from scope "<consoleScope>"

Examples:
  | from  | keys                 | consoleScope |
  | root  | "console", "audit"   | root         |
  | child | "console", "audit"   | child        |
```

<!-- cases: N/A 2 overrides: no option -->
<!-- cases: N/A 4 boundaries: empty covered by "No match yields an empty collection" -->
<!-- cases: N/A 5 errors, 9 panics: covered by "A failing member fails the whole call" Must rows -->
<!-- cases: N/A 8 concurrency: shares the slice variant's pipeline, covered by "Concurrent collection callers" -->

### Requirement: The invoker depends on every member
When a collection is invoked from inside a provider, the invoking service SHALL record a dependency on every member it successfully built, as `Invoke` does for a single dependency. Invocation hooks SHALL fire once per member, with the member's name.

#### Scenario: Dependency edges order shutdown
```gherkin
Given a root scope
And lazy services "console" and "audit" implementing "EventHandler" and "Shutdowner"
And a lazy service "bus" implementing "Shutdowner" whose provider calls InvokeAll for type "EventHandler"
When the consumer invokes "bus" and then shuts down the root scope
Then the explain output of "bus" lists dependencies "console" and "audit"
And "bus" is shut down before "console" and before "audit"
```

#### Scenario: Invocation hooks per member
```gherkin
Given a root scope with an after-invocation hook that records service names
And lazy services "console" and "audit" implementing "EventHandler"
When the consumer calls InvokeAll for type "EventHandler"
Then the hook recorded exactly "console" 1 time and "audit" 1 time
```

#### Scenario: No edges when invoked outside a provider
```gherkin
Given a root scope with lazy services "console" and "audit" implementing "EventHandler"
When the consumer calls InvokeAll for type "EventHandler" directly on the root scope
Then "console" and "audit" have no dependents
```

<!-- cases: N/A 2 overrides: edge recording is not configurable; it is what makes lifecycle ordering correct -->
<!-- cases: N/A 3 zero values: an empty collection records no edges, covered by the empty-result requirement -->
<!-- cases: N/A 5 errors: a member that fails to build records no edge; the call fails as a whole -->
<!-- cases: N/A 9 panics: no panicking path -->

### Requirement: InvokeAs behavior is unchanged
`InvokeAs[T]` SHALL keep reporting the requested type name, not the resolved service's name, to `HookBeforeInvocation` and `HookAfterInvocation`.

#### Scenario: InvokeAs hooks receive the requested type name
```gherkin
Given a root scope with before- and after-invocation hooks that record service names
And a lazy service "audit" of type "*AuditHandler" implementing "EventHandler"
When the consumer calls InvokeAs for type "EventHandler"
Then both hooks recorded the name of type "EventHandler" and not "audit"
```

<!-- cases: N/A 2 overrides, 3 zero values, 4 boundaries, 5 errors, 6 duplicates, 7 lifecycle, 8 concurrency, 9 panics: existing InvokeAs tests cover these unchanged; this requirement pins the hook name the shared helper could change -->

### Requirement: Transient members and snapshot semantics
A transient member SHALL contribute a new instance on every call. The returned collection SHALL be a snapshot: services registered after the call are not added to it, and mutating the returned slice or map SHALL NOT affect later calls.

#### Scenario: Transient and singleton members across calls
```gherkin
Given a root scope
And a transient service "req" of type "*ReqHandler" implementing "EventHandler"
And a lazy service "console" of type "*ConsoleHandler" implementing "EventHandler"
When the consumer calls InvokeAll for type "EventHandler" twice
Then the "req" elements of the two results are different instances
And the "console" elements of the two results are the same instance
```

#### Scenario: Later registration is not visible in an earlier snapshot
```gherkin
Given a root scope with a lazy service "console" implementing "EventHandler"
And the consumer calls InvokeAll for type "EventHandler" and keeps result R1
When a lazy service "audit" implementing "EventHandler" is registered and InvokeAll is called again as R2
Then R1 has 1 element
And R2 has 2 elements
```

#### Scenario: Caller mutation does not leak
```gherkin
Given a root scope with lazy services "console" and "audit" implementing "EventHandler"
And the consumer calls InvokeAll for type "EventHandler" and sets every element of the result to nil
When the consumer calls InvokeAll for type "EventHandler" again
Then no element of the new result is nil
```

#### Scenario: Collections after shutdown
```gherkin
Given a root scope with a lazy service "console" implementing "EventHandler" that has been invoked
And the root scope has been shut down
When the consumer calls InvokeAll for type "EventHandler"
Then the result is empty
And the error is nil
```

<!-- cases: N/A 2 overrides: snapshot semantics are fixed; live collections are phase 2 -->
<!-- cases: N/A 9 panics: no panicking path -->

### Requirement: Concurrent collection callers
Concurrent calls to `InvokeAll`/`InvokeAllMap`, including calls concurrent with registrations or with a root shutdown, SHALL be free of data races and leaked goroutines. Each lazy member SHALL be built exactly once while no shutdown runs.

#### Scenario: Concurrent callers build each member once
```gherkin
Given a root scope with lazy services "console" and "audit" implementing "EventHandler" whose providers count their calls
When 100 goroutines call InvokeAll for type "EventHandler" concurrently
Then every goroutine receives 2 elements
And each provider was called exactly 1 time
And the race detector reports no race
And goleak reports no leaked goroutine
```

#### Scenario: Concurrent registration and collection
```gherkin
Given a root scope with a lazy service "console" implementing "EventHandler"
When 50 goroutines register distinct services "h-<n>" implementing "EventHandler" while 50 goroutines call InvokeAll for type "EventHandler"
Then every result contains "console"
And every result has between 1 and 51 elements
And the race detector reports no race
```

#### Scenario: Concurrent collection and shutdown
```gherkin
Given a root scope with lazy services "console" and "audit" implementing "EventHandler" whose providers return a new instance and no error
When 50 goroutines call InvokeAll for type "EventHandler" while another goroutine shuts down the root scope
Then every call returns a nil error and between 0 and 2 elements
And the race detector reports no race
And goleak reports no leaked goroutine
```

<!-- cases: N/A 2 overrides, 3 zero values, 4 boundaries: covered by earlier requirements; this requirement is about interleaving only -->
<!-- cases: N/A 9 panics: no panicking path -->
<!-- cases: 10 compatibility: Invoke, InvokeNamed and InvokeAs behavior is unchanged; covered by the existing invoke_test.go and di_test.go suites and "InvokeAs behavior is unchanged" -->
