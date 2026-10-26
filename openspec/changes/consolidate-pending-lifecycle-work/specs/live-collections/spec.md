# Spec Delta

## Purpose

Lets a component hold a collection that re-resolves its members on every read, so services registered after the component was built are included, unlike the snapshot returned by `InvokeAll`.

## ADDED Requirements

### Requirement: A live collection re-resolves on every read
`InvokeCollection[T](i) Collection[T]` SHALL return a value whose `All() ([]T, error)` and `Map() (map[string]T, error)` resolve the members on each call, with the same membership, order, self-exclusion, error and hook rules as `InvokeAll[T]`/`InvokeAllMap[T]` applied from `i`. `InvokeCollection` SHALL build nothing itself.

#### Scenario: Later registrations are visible
```gherkin
Given a root scope with lazy "console" implementing "EventHandler"
And c := InvokeCollection[EventHandler](root)
And the consumer then registers lazy "audit" implementing "EventHandler"
When the consumer calls <read>
Then the result is <result>

Examples:
  | read    | result                                |
  | c.All() | [console, audit], nil error           |
  | c.Map() | {"console", "audit"} keys, nil error  |
```

#### Scenario: Creating a live collection builds nothing
```gherkin
Given a root scope with lazy "console" implementing "EventHandler" whose provider counts its calls
When the consumer calls InvokeCollection[EventHandler](root)
Then the provider was called 0 times
And after c.All() it was called 1 time
```

#### Scenario: A live collection held by a provider
```gherkin
Given lazy "bus" whose provider stores InvokeCollection[EventHandler](i), and "bus" implements EventHandler
And lazy "console" implementing "EventHandler"
And "bus" has been invoked
And the consumer then registers lazy "audit" implementing "EventHandler"
When the bus calls All() on its collection
Then the result is [console, audit], without "bus"
And ExplainNamedService(root, "bus") lists no dependency on "audit" or "console"
```

#### Scenario: Reads after shutdown and failures
```gherkin
Given a root scope with <setup> and c := InvokeCollection[EventHandler](root)
When the consumer calls c.All()
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
When 50 goroutines call c.All() while 10 goroutines register lazy "h-<n>" implementing "EventHandler"
Then no data race is reported
And the "console" provider was called exactly 1 time
```

<!-- cases: 1 default: re-resolution on read -->
<!-- cases: 2 overrides: ordering overrides apply as for InvokeAll; a snapshot is InvokeAll -->
<!-- cases: 3 zero values: no member yields an empty non-nil slice; a nil injector means DefaultRootScope, as for InvokeAll -->
<!-- cases: N/A 4 boundaries: covered by collection-invocation -->
<!-- cases: 5 errors: member failure row -->
<!-- cases: 6 duplicates: self-exclusion and alias exclusion as for InvokeAll -->
<!-- cases: 7 lifecycle: after shutdown, late registration, held by a provider (reads after the provider returned have their own requirement) -->
<!-- cases: 8 concurrency: concurrent reads and registrations -->
<!-- cases: N/A 9 panics: an Order() panic is returned by All(), as in collection-ordering -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Reads after the holder's provider returned
When `i` is a provider's injector and that provider has returned, `All()`/`Map()` SHALL resolve from the provider's scope, excluding only the holder, without recording dependency edges, and keeping from its build-time invoker chain only the providers still running. Reads made while the provider runs keep the rules of `InvokeAll`.

#### Scenario: Reads after the holder's provider returned
```gherkin
Given lazy "app" implementing "EventHandler" whose provider invokes "bus"
And lazy "bus" implementing "EventHandler" whose provider stores c := InvokeCollection[EventHandler](i)
And "app" has been invoked, which built "bus"
When the bus calls <read> on its collection
Then the result is <result>
And ExplainNamedService(root, "bus") lists no dependency on "app"

Examples:
  | read    | result                       |
  | c.All() | [app], nil error             |
  | c.Map() | {"app"} keys, nil error      |
```

#### Scenario: A read while an outer provider still runs
```gherkin
Given lazy "app" implementing "EventHandler" whose provider invokes "bus", then calls All() on bus's collection and returns its error
And lazy "bus" implementing "EventHandler" whose provider stores c := InvokeCollection[EventHandler](i)
When the consumer invokes "app" within 1s
Then the invocation returns an error matching ErrCircularDependency
And its message contains "`app` -> `bus` -> `app`", naming the holder
```

#### Scenario: A read while an outer provider runs records no edge
```gherkin
Given lazy "app", not an EventHandler, whose provider invokes "bus", then calls All() on bus's collection and keeps the member names
And lazy "bus" implementing "EventHandler" whose provider stores c := InvokeCollection[EventHandler](i)
And lazy "console" implementing "EventHandler"
When the consumer invokes "app"
Then the invocation returns nil error and "app" kept [console]
And ExplainNamedService(root, "app") lists exactly the dependency bus
```

#### Scenario: A member built by such a read keeps the running chain
```gherkin
Given lazy "app" implementing "AppMarker", not an EventHandler, whose provider invokes "bus", calls All() on bus's collection, then invokes "member" and calls All() on member's collection
And lazy "bus" implementing "EventHandler" whose provider stores InvokeCollection[EventHandler](i)
And lazy "member" implementing "EventHandler" whose provider stores InvokeCollection[AppMarker](i)
When the consumer invokes "app" within 1s
Then the invocation returns an error matching ErrCircularDependency
```

#### Scenario: A read while the holder's provider runs
```gherkin
Given lazy "console" implementing "EventHandler"
And lazy "bus" implementing "EventHandler" whose provider calls All() on c := InvokeCollection[EventHandler](i) and records the result
When the consumer invokes "bus"
Then the recorded result is [console], without "bus"
And ExplainNamedService(root, "bus") lists dependency console
```

<!-- cases: 1 default: a holder built through one of its members -->
<!-- cases: N/A 2 overrides: there is no option -->
<!-- cases: N/A 3 zero values, 4 boundaries: covered by the main requirement -->
<!-- cases: N/A 5 errors: member failures are returned as for InvokeAll -->
<!-- cases: 6 duplicates: the holder stays excluded ("The holder is the service its provider builds") -->
<!-- cases: 7 lifecycle: during versus after the provider; an outer provider still running keeps cycle detection -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent reads are race-free". Two hazards are documented on Collection, not specified as behavior, because a read cannot know its goroutine or build chain: a goroutine outside the build chain that reads while an outer provider of the holder still runs gets ErrCircularDependency for that provider's service; a read from inside a member's provider, by code not on the holder's build chain, waits for that member's own build and never returns -->
<!-- cases: N/A 9 panics: as for InvokeAll -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: An invocation that ran no provider is over once it returned
An invocation SHALL stop counting as a running provider of the build chain once it has returned, even when no provider ran in it: the resolution of an eager value's declared dependencies, or an attempt that failed before its provider ran. A later read of a collection created below it SHALL NOT report a circular dependency on it.

#### Scenario: Invocations that ran no provider are not running
```gherkin
Given lazy "bus" implementing "EventHandler" whose provider stores c := InvokeCollection[EventHandler](i)
And <dependent> implementing "EventHandler", registered with DependsOnNamed("bus")
And <steps>
When the bus calls c.Map()
Then the result has keys <keys> and a nil error

Examples:
  | dependent    | steps                                                                                  | keys   |
  | eager value "e" | the consumer invoked "e"                                                             | {"e"}  |
  | eager value "e" | the consumer called InvokeStruct for a struct with field `E *ConsoleHandler \`do:"e"\`` | {"e"}  |
  | eager value "e" | the consumer called InvokeAs[*ConsoleHandler], which selected "e"                    | {"e"}  |
  | lazy "l", also with DependsOnNamed("missing") | the consumer invoked "l", which failed, then registered lazy "missing" (not an EventHandler) and invoked "l" again | {"l"} |
```

<!-- cases: 1 default: the eager row -->
<!-- cases: N/A 2 overrides: there is no option -->
<!-- cases: N/A 3 zero values, 4 boundaries: one dependent per row -->
<!-- cases: 5 errors: the failed-attempt row -->
<!-- cases: N/A 6 duplicates: a second invocation is over as well -->
<!-- cases: 7 lifecycle: the read happens after the dependent's invocation returned -->
<!-- cases: N/A 8 concurrency, 9 panics: as for the main requirement -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: The holder is the service its provider builds
The holder a live collection excludes SHALL be the service whose provider received `i`, whatever that provider's invoker builds afterwards from the same depth of the build chain.

#### Scenario: The holder stays excluded after its invoker builds siblings
```gherkin
Given lazy "a", "b", "c", "d" and "e", each implementing "EventHandler", where "a" invokes "b", "b" invokes "c", and "c" invokes "d" and then "e"
And "d"'s provider stores c := InvokeCollection[EventHandler](i)
And the consumer has invoked "a"
When "d" calls c.Map()
Then the result has keys {"a", "b", "c", "e"} and a nil error
```

<!-- cases: 1 default: the scenario -->
<!-- cases: N/A 2 overrides: there is no option -->
<!-- cases: 4 boundaries: a chain three deep, where the invoker chain's backing array has spare capacity -->
<!-- cases: N/A 3 zero values, 5 errors, 8 concurrency, 9 panics: as for the main requirement -->
<!-- cases: 6 duplicates: a sibling built after the holder ("e") stays a member -->
<!-- cases: 7 lifecycle: the read happens after the whole chain returned -->
<!-- cases: N/A 10 compatibility: new API -->
