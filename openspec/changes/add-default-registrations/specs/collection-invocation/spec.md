# Spec Delta

<!-- collection-invocation is introduced by consolidate-pending-lifecycle-work, which must be archived before this change. MODIFIED blocks copy its requirements whole. Live collections follow these rules through their "membership rules of InvokeAll". -->

## MODIFIED Requirements

### Requirement: Collect every visible service assignable to a type
`InvokeAll[T]` SHALL return one element per visible, non-alias service whose registered type is assignable to `T`, building lazy members as needed, except defaults excluded as specified in "Collections exclude a default replaced by type". It SHALL NOT build services whose registered type is not assignable to `T`. A nil injector SHALL mean the default root scope.

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
<!-- cases: N/A 6 duplicates: aliases, shadowing and defaults are covered by their own requirements -->
<!-- cases: N/A 7 lifecycle: covered by "Collections after shutdown" and the self-exclusion requirement -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent collection callers" -->
<!-- cases: N/A 9 panics: provider panics are covered by "A failing member fails the whole call" -->
<!-- cases: N/A 10 compatibility: covered by "InvokeAs behavior is unchanged"; the default exception applies only to registrations given AsDefault() -->

### Requirement: Visibility follows scope ancestry with nearest-name shadowing
A collection invoked from a scope SHALL include services of that scope and its ancestors only. When the same service name exists in several visible scopes, only the nearest one SHALL be considered, even if the nearest one is not assignable to `T`, except that an unresolved or yielded default SHALL NOT hide a non-default of the same name (see "A default that yields does not shadow its target"). Inside a provider, the invoking scope SHALL be the scope where that provider's service is registered.

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
  | x        | *AHandler       | x (registered with doger.AsDefault()) | *BHandler | child | root "x" only |
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
<!-- cases: N/A 10 compatibility: matches what InvokeNamed resolves for each name; the default row applies only to registrations given AsDefault() -->

## ADDED Requirements

### Requirement: Collections exclude a default replaced by type
`InvokeAll`, `InvokeAllMap` and live collections SHALL exclude an unresolved default when a visible non-default member is assignable to the default's registered type; including a default in a collection resolves it. A default that already chose its own provider SHALL stay a member. Defaults meant to be replaced by type SHALL be registered under the interface type.

#### Scenario: Default membership by registered type
```gherkin
Given a root scope
And "envSource" registered with doger.AsDefault() under type <registered_as>, never invoked
And <explicit>
When the consumer calls doger.InvokeAll[app.PropertySource] on the root
Then the members are <members>

Examples:
  | registered_as      | explicit                                       | members                  |
  | app.PropertySource | a lazy "mySource" (an *app.MySource)           | mySource                 |
  | app.PropertySource | nothing                                        | envSource                |
  | *app.EnvSource     | a lazy "mySource" (an *app.MySource)           | envSource, mySource      |
  | *app.EnvSource     | a lazy "envSource2" (an *app.EnvSource)        | envSource2               |
```

#### Scenario: A default that chose its own provider stays a member
```gherkin
Given a root scope
And "envSource" registered with doger.AsDefault() under type app.PropertySource
And "envSource" was invoked
And a lazy "mySource" (an *app.MySource) registered afterwards
When the consumer calls doger.InvokeAll[app.PropertySource] on the root
Then the members are envSource, mySource
```

#### Scenario: A default does not exclude unrelated members
```gherkin
Given a root scope
And a lazy "*app.Pool" registered with doger.AsDefault(), implementing HealthCheck() error
And a lazy "*app.Server" registered without options, implementing HealthCheck() error
When the consumer calls doger.InvokeAll[doger.Healthchecker] on the root
Then the members are "*app.Pool" and "*app.Server"
```

<!-- cases: 2 overrides: invoking the default by name still returns it; registering an explicit service is the override -->
<!-- cases: N/A 3 zero values: an empty collection is unchanged ("No match yields an empty collection") -->
<!-- cases: 4 boundaries: zero and one replacing member are rows; several members follow the same rule -->
<!-- cases: N/A 5 invalid input: unchanged collection errors -->
<!-- cases: 6 same name: covered by "A default that yields does not shadow its target" -->
<!-- cases: 7 lifecycle: the chosen-default scenario pins the frozen decision -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent collection callers" unchanged -->
<!-- cases: N/A 9 panics: membership does not panic -->
<!-- cases: 10 compatibility: without defaults, membership is unchanged -->

### Requirement: A default that yields does not shadow its target
In a collection, a default whose name matches a visible non-default service in an ancestor SHALL NOT shadow that service and SHALL NOT be a member itself, so the non-default service is a member exactly once.

#### Scenario: Same-name default in a nearer scope
```gherkin
Given a root scope with child scope "ext"
And an explicit lazy "*app.Pool" registered in the root, implementing HealthCheck() error
And a lazy "*app.Pool" registered in scope "ext" with doger.AsDefault(), never invoked
When the consumer calls doger.InvokeAllMap[doger.Healthchecker] from scope "ext"
Then the map has exactly 1 entry, "*app.Pool", holding the root instance
```

#### Scenario: A chosen or nearer default keeps one entry per name
```gherkin
Given a root scope with child scope "ext"
And <setup>
When the consumer calls doger.InvokeAllMap[doger.Healthchecker] from scope "ext"
Then the map has exactly 1 entry, "*app.Pool", holding the instance of scope "ext"

Examples:
  | setup                                                                                                                  |
  | a default "*app.Pool" in "ext" invoked from "ext", then an explicit "*app.Pool" registered in the root                 |
  | a default "*app.Pool" in the root and a default "*app.Pool" in "ext", both never invoked                               |
```

<!-- cases: N/A 2 overrides, 3 zero values, 5 invalid input: no input -->
<!-- cases: 4 boundaries: unresolved, chosen and two-default cases -->
<!-- cases: 7 lifecycle: membership follows the default's decision; a chosen default shadows like a non-default -->
<!-- cases: N/A 8 concurrency, 9 panics: unchanged -->
<!-- cases: 10 compatibility: nearest-name shadowing between non-default services is unchanged -->
