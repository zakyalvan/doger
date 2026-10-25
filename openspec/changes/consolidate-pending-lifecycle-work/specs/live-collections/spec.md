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
And ExplainNamedService(root, "bus") lists dependencies audit, console
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
<!-- cases: 7 lifecycle: after shutdown, late registration, held by a provider with edges recorded at read time -->
<!-- cases: 8 concurrency: concurrent reads and registrations -->
<!-- cases: N/A 9 panics: an Order() panic is returned by All(), as in collection-ordering -->
<!-- cases: N/A 10 compatibility: new API -->
