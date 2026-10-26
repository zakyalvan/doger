# Spec Delta

## Purpose

Lets a component hold a collection that re-resolves its members on every read, so services registered after the component was built are included, unlike the snapshot returned by `InvokeAll`. Each read takes the reader's injector, so cycle detection and dependency edges follow the code that reads, not the code that created the collection.

## ADDED Requirements

### Requirement: A live collection re-resolves on every read
`InvokeCollection[T](i) Collection[T]` SHALL return a value whose `All(r Injector) ([]T, error)` and `Map(r Injector) (map[string]T, error)` resolve the members on each call, from the scope of `i`, with the membership, order, error and hook rules of `InvokeAll[T]`/`InvokeAllMap[T]`. `InvokeCollection` SHALL build nothing itself.

#### Scenario: Later registrations are visible
```gherkin
Given a root scope with lazy "console" implementing "EventHandler"
And c := InvokeCollection[EventHandler](root)
And the consumer then registers lazy "audit" implementing "EventHandler"
When the consumer calls <read>
Then the result is <result>

Examples:
  | read         | result                                |
  | c.All(nil)   | [console, audit], nil error           |
  | c.Map(nil)   | {"console", "audit"} keys, nil error  |
```

#### Scenario: Creating a live collection builds nothing
```gherkin
Given a root scope with lazy "console" implementing "EventHandler" whose provider counts its calls
When the consumer calls InvokeCollection[EventHandler](root)
Then the provider was called 0 times
And after c.All(nil) it was called 1 time
```

#### Scenario: Reads after shutdown and failures
```gherkin
Given a root scope with <setup> and c := InvokeCollection[EventHandler](root)
When the consumer calls c.All(nil)
Then the result is <result>

Examples:
  | setup                                                        | result                                                    |
  | lazy "console", and the root scope has been shut down        | an empty, non-nil slice and a nil error                   |
  | lazy "console" whose provider returns errBuild               | nil and an error matching errBuild naming "console"       |
  | no service implementing EventHandler                         | an empty, non-nil slice and a nil error                   |
```

#### Scenario: Concurrent reads are race-free
```gherkin
Given a root scope with lazy "console" implementing "EventHandler" whose provider counts its calls, and c := InvokeCollection[EventHandler](root)
When 50 goroutines call c.All(nil) while 10 goroutines register lazy "h-<n>" implementing "EventHandler"
Then no data race is reported
And the "console" provider was called exactly 1 time
```

<!-- cases: 1 default: re-resolution on read -->
<!-- cases: 2 overrides: ordering overrides apply as for InvokeAll; a snapshot is InvokeAll -->
<!-- cases: 3 zero values: no member yields an empty non-nil slice; a nil i means DefaultRootScope, as for InvokeAll -->
<!-- cases: N/A 4 boundaries: covered by collection-invocation -->
<!-- cases: 5 errors: member failure row -->
<!-- cases: 6 duplicates: alias exclusion as for InvokeAll; the holder has its own requirement -->
<!-- cases: 7 lifecycle: after shutdown, late registration -->
<!-- cases: 8 concurrency: concurrent reads and registrations -->
<!-- cases: N/A 9 panics: an Order() panic is returned by All, as in collection-ordering -->
<!-- cases: N/A 10 compatibility: new API; the read signature changed before any release, so no consumer breaks -->

### Requirement: The holder is excluded from every read
When `i` is a provider's injector, the service that provider builds (the holder) SHALL be excluded from every read of the collection, whoever reads and whenever, even after its provider returned. `InvokeCollection` SHALL keep the holder's scope and name, not its provider's build chain.

#### Scenario: The holder stays excluded
```gherkin
Given lazy "app" implementing "EventHandler" whose provider invokes "bus"
And lazy "bus" implementing "EventHandler" whose provider stores c := InvokeCollection[EventHandler](i)
And "app" has been invoked, which built "bus"
When the consumer calls <read> on bus's collection
Then the result is <result>
And ExplainNamedService(root, "bus") lists no dependency on "app"

Examples:
  | read        | result                       |
  | c.All(nil)  | [app], nil error             |
  | c.Map(nil)  | {"app"} keys, nil error      |
```

#### Scenario: The holder stays excluded after its invoker builds siblings
```gherkin
Given lazy "a", "b", "c", "d" and "e", each implementing "EventHandler", where "a" invokes "b", "b" invokes "c", and "c" invokes "d" and then "e"
And "d"'s provider stores c := InvokeCollection[EventHandler](i)
And the consumer has invoked "a"
When the consumer calls c.Map(nil)
Then the result has keys {"a", "b", "c", "e"} and a nil error
```

<!-- cases: 1 default: a holder built through one of its members -->
<!-- cases: N/A 2 overrides: there is no option -->
<!-- cases: 3 zero values: InvokeCollection(root) has no holder -->
<!-- cases: 4 boundaries: a chain three deep -->
<!-- cases: N/A 5 errors: member failures are returned as for InvokeAll -->
<!-- cases: 6 duplicates: a sibling built after the holder ("e") stays a member -->
<!-- cases: 7 lifecycle: the reads happen after the whole chain returned -->
<!-- cases: N/A 8 concurrency, 9 panics: as for the main requirement -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Reads follow the reader's injector
A read with `r` SHALL apply the self-exclusion, cycle detection and dependency edges of `InvokeAll` called from `r`. When `r` is a provider's injector, its service SHALL be excluded, its dependency on each member SHALL be recorded, and cycles through its build chain SHALL be reported. When `r` is nil, or not a provider's injector, the read SHALL use no build chain and record no edge.

#### Scenario: A provider reads with its own injector
```gherkin
Given lazy "console" implementing "EventHandler"
And lazy "bus" implementing "EventHandler" whose provider stores c := InvokeCollection[EventHandler](i)
And lazy "app" whose provider invokes "bus", then calls c.All(i) on bus's collection with its own injector and keeps the member names
And "app" is <app_kind>
When the consumer invokes "app"
Then the invocation returns a nil error and "app" kept [console]
And ExplainNamedService(root, "app") lists exactly the dependencies bus and console

Examples:
  | app_kind                     |
  | not an EventHandler          |
  | implementing "EventHandler"  |
```

#### Scenario: The reader is excluded by name
```gherkin
Given lazy "bus" implementing "EventHandler" in the root whose provider stores c := InvokeCollection[EventHandler](i)
And lazy "app" and "console" implementing "EventHandler" in the root
And child scope "child" with lazy "app", not an EventHandler, whose provider invokes "bus", then calls c.Map(<reader>) on bus's collection and keeps the member names
When the consumer invokes "app" from "child"
Then the invocation returns a nil error
And the read had the members <members>

Examples:
  | reader                   | members        |
  | i, the child app's own   | [console]      |
  | nil                      | [app, console] |
```

The child's "app" shares its name with the root's "app", so a read with its own injector excludes the root's "app" as well, as `InvokeAs` skips its invoker and the aliases resolving to it by name.

#### Scenario: A read with nil records no edge
```gherkin
Given lazy "console" implementing "EventHandler"
And lazy "bus" implementing "EventHandler" whose provider stores c := InvokeCollection[EventHandler](i)
And lazy "app", not an EventHandler, whose provider invokes "bus", then calls c.All(nil) on bus's collection
When the consumer invokes "app"
Then the invocation returns a nil error
And ExplainNamedService(root, "app") lists exactly the dependency bus
```

#### Scenario: A read while the holder's provider runs
```gherkin
Given lazy "console" implementing "EventHandler"
And lazy "bus" implementing "EventHandler" whose provider calls c.All(i) on c := InvokeCollection[EventHandler](i) and records the result
When the consumer invokes "bus"
Then the recorded result is [console], without "bus"
And ExplainNamedService(root, "bus") lists dependency console
```

#### Scenario: A goroutine reads while an outer provider runs (former hazard 1)
```gherkin
Given lazy "app" implementing "EventHandler" whose provider invokes "bus", then blocks until released
And lazy "bus" implementing "EventHandler" whose provider stores c := InvokeCollection[EventHandler](i)
And the consumer invokes "app" in another goroutine, and "app" is blocked
When a third goroutine calls c.All(nil), and "app" is released 100ms later
Then that read returns [app] and a nil error, not ErrCircularDependency
```

#### Scenario: A member's provider reads the collection (former hazard 2)
```gherkin
Given lazy "bus" implementing "EventHandler" whose provider stores c := InvokeCollection[EventHandler](i), already invoked
And lazy "audit" implementing "EventHandler"
And lazy "console" implementing "EventHandler" whose provider <reads>
When the consumer invokes "console" within 1s
Then <result>

Examples:
  | reads                                                                      | result                                                  |
  | calls c.All(i) with its own injector and keeps the member names            | a nil error, and console kept [audit]                   |
  | invokes lazy "helper", whose provider calls c.All(j) with its own injector | an error matching ErrCircularDependency naming "console" |
```

#### Scenario: Reads outside providers ignore finished invocations
```gherkin
Given lazy "bus" implementing "EventHandler" whose provider stores c := InvokeCollection[EventHandler](i)
And <dependent> implementing "EventHandler", registered with DependsOnNamed("bus")
And <steps>
When the consumer calls c.Map(nil)
Then the result has keys <keys> and a nil error

Examples:
  | dependent    | steps                                                                                  | keys   |
  | eager value "e" | the consumer invoked "e"                                                             | {"e"}  |
  | eager value "e" | the consumer called InvokeStruct for a struct with field `E *ConsoleHandler \`do:"e"\`` | {"e"}  |
  | eager value "e" | the consumer called InvokeAs[*ConsoleHandler], which selected "e"                    | {"e"}  |
  | lazy "l", also with DependsOnNamed("missing") | the consumer invoked "l", which failed, then registered lazy "missing" (not an EventHandler) and invoked "l" again | {"l"} |
```

<!-- cases: 1 default: nil reader, no chain, no edge -->
<!-- cases: 2 overrides: a provider's injector as reader -->
<!-- cases: 3 zero values: nil reader -->
<!-- cases: N/A 4 boundaries: covered by the main requirement -->
<!-- cases: 5 errors: a cycle through the reader's own chain -->
<!-- cases: 6 duplicates: the reader's own service is excluded, as for InvokeAll; by name, so a same-named member of the holder's scope it shadows is excluded too -->
<!-- cases: 7 lifecycle: reads during and after providers; finished invocations never count -->
<!-- cases: 8 concurrency: a goroutine outside the build chain waits for the outer provider instead of failing (former hazard 1) -->
<!-- cases: N/A 9 panics: as for InvokeAll -->
<!-- cases: 10 compatibility: replaces the reads-after-the-holder-returned rules of this unreleased change; both documented hazards are gone -->
