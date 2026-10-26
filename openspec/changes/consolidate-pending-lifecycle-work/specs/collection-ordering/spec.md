# Spec Delta

## Purpose

Defines a deterministic, overridable order for slice collections returned by `InvokeAll`, kept strictly separate from lifecycle (construction, start and shutdown) ordering.

## ADDED Requirements

### Requirement: Default collection order is ancestors first, then registration order
Without any `Ordered` member or `WithOrder` option, `InvokeAll[T]` SHALL order members by scope depth from the root scope down to the invoking scope, then within one scope by registration order, then by service name. The order SHALL be identical across calls and in clones.

#### Scenario: Default order across scopes
```gherkin
Given a root scope where lazy services are registered in the order <rootRegs>
And a child scope "child" where lazy services are registered in the order <childRegs>
And every registered service implements "EventHandler" and not "Ordered"
When the consumer calls InvokeAll for type "EventHandler" from scope "child"
Then the result order is <order>

Examples:
  | rootRegs        | childRegs   | order                        |
  | "c", "a", "b"   | none        | "c", "a", "b"                |
  | "b", "a"        | "z", "y"    | "b", "a", "z", "y"           |
  | none            | "y", "z"    | "y", "z"                     |
  | "a"             | "a2"        | "a", "a2"                    |
```

#### Scenario: Order is stable across repeated calls
```gherkin
Given a root scope with 50 lazy services "h-01" to "h-50" registered in that order, all implementing "EventHandler"
When the consumer calls InvokeAll for type "EventHandler" 20 times
Then every result has the order "h-01" to "h-50"
```

#### Scenario: Override replaces a registration in place
```gherkin
Given a root scope with lazy services "a", "b", "c" registered in that order, all implementing "EventHandler"
When "b" is overridden with a new provider and the consumer calls InvokeAll for type "EventHandler"
Then the result order is "a", "b", "c"
```

#### Scenario: Default order in a clone
```gherkin
Given a root scope with lazy services "a" and "b" registered in that order, both implementing "EventHandler"
And the consumer clones the root scope with Clone()
And a lazy service "c" implementing "EventHandler" is registered in the clone
When the consumer calls InvokeAll for type "EventHandler" on the clone 20 times
Then every result has the order "a", "b", "c"
```

<!-- cases: N/A 2 overrides: Ordered and WithOrder are covered by their own requirements -->
<!-- cases: N/A 3 zero values: an empty collection has no order; covered by collection-invocation -->
<!-- cases: N/A 5 errors: default ordering cannot fail -->
<!-- cases: N/A 6 duplicates: equal registration indexes cannot occur within one container (start-lifecycle D3 copies the counter on clone); the name tie-break is defensive and design-only -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent collection callers" in collection-invocation -->
<!-- cases: N/A 9 panics: no panicking path without Ordered -->

### Requirement: Ordered members sort ascending and stably
A member whose instance implements `Ordered` SHALL be positioned by `Order()`, lowest value first. A member that does not implement `Ordered` SHALL count as `0`. The sort SHALL be stable: members with equal values keep the default order.

#### Scenario: Ordered values
```gherkin
Given a root scope where services are registered in the order "a", "b", "c", "d"
And their order values are <values> where "-" means the member does not implement Ordered
When the consumer calls InvokeAll for type "EventHandler"
Then the result order is <order>

Examples:
  | values                        | order                |
  | -, -, -, -                    | "a", "b", "c", "d"   |
  | 3, 1, 2, 0                    | "d", "b", "c", "a"   |
  | 1, -, -1, -                   | "c", "b", "d", "a"   |
  | 5, 5, 5, 5                    | "a", "b", "c", "d"   |
  | -2147483648, 2147483647, 0, - | "a", "c", "d", "b"   |
  | 0, -, 0, -                    | "a", "b", "c", "d"   |
```

#### Scenario: Order is read from each transient instance
```gherkin
Given a root scope
And a lazy service "fixed" implementing "Ordered" with value 0
And a transient service "flip" whose instance implements "Ordered" with value <value> on call <call>
When the consumer calls InvokeAll for type "EventHandler" for call <call>
Then the result order is <order>

Examples:
  | call | value | order            |
  | 1    | -1    | "flip", "fixed"  |
  | 2    | 1     | "fixed", "flip"  |
```

<!-- cases: N/A 2 overrides: WithOrder is covered by its own requirement -->
<!-- cases: N/A 5 errors: covered by "Order() panic is reported" -->
<!-- cases: N/A 6 duplicates: equal values are covered by the stable-sort rows -->
<!-- cases: N/A 7 lifecycle: covered by "Ordering never affects lifecycle order" -->
<!-- cases: N/A 8 concurrency: Order() is called on the calling goroutine -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: WithOrder is a registration option for every registration style
`WithOrder(n int)` SHALL be a `ProvideOption` accepted by every `Provide*` and `Override*` function and every `Package` helper (`Lazy*`, `Eager*`, `Transient*`). It SHALL set the member's order value to `n`, take precedence over the instance's `Order()`, which SHALL then not be called, and when given more than once the last one SHALL win.

#### Scenario: Precedence
```gherkin
Given a root scope where services "a", "b", "c" are registered in that order
And "a" implements Ordered with value 10 and is registered <aOption>
And "b" does not implement Ordered and is registered <bOption>
And "c" implements Ordered with value 0 and is registered without options
When the consumer calls InvokeAll for type "EventHandler"
Then the result order is <order>

Examples:
  | aOption                          | bOption            | order            |
  | without options                  | without options    | "b", "c", "a"    |
  | with WithOrder(-1)               | without options    | "a", "b", "c"    |
  | without options                  | with WithOrder(20) | "c", "a", "b"    |
  | with WithOrder(0)                | with WithOrder(0)  | "a", "b", "c"    |
  | with WithOrder(5), WithOrder(-5) | without options    | "a", "b", "c"    |
```

#### Scenario: WithOrder on every registration helper
```gherkin
Given a root scope with a lazy service "first" implementing "EventHandler" registered without options
And a service "second" implementing "EventHandler" registered through <helper> with WithOrder(-1)
When the consumer calls InvokeAll for type "EventHandler"
Then the result order is "second", "first"

Examples:
  | helper                  |
  | Provide                 |
  | ProvideNamed            |
  | ProvideValue            |
  | ProvideNamedValue       |
  | ProvideTransient        |
  | ProvideNamedTransient   |
  | Override                |
  | OverrideNamed           |
  | OverrideValue           |
  | OverrideNamedValue      |
  | OverrideTransient       |
  | OverrideNamedTransient  |
  | Package(Lazy)           |
  | Package(LazyNamed)      |
  | Package(Eager)          |
  | Package(EagerNamed)     |
  | Package(Transient)      |
  | Package(TransientNamed) |
```

#### Scenario: Order() is not called when WithOrder is set
```gherkin
Given a root scope with a lazy service "a" implementing "EventHandler" and "Ordered" whose Order() panics
And "a" is registered with WithOrder(1)
When the consumer calls InvokeAll for type "EventHandler"
Then the error is nil
And the result has 1 element
```

#### Scenario: WithOrder outside collections has no effect
```gherkin
Given a root scope with a lazy service "a" registered with WithOrder(-100)
When the consumer invokes "a" by name
Then the instance is returned
And no error occurs
```

<!-- cases: N/A 3 zero values: WithOrder(0) is covered by the precedence rows -->
<!-- cases: N/A 4 boundaries: int extremes are covered by "Ordered values"; WithOrder stores the same int -->
<!-- cases: N/A 7 lifecycle: covered by "Ordering never affects lifecycle order" -->
<!-- cases: N/A 8 concurrency: the option is fixed at registration -->
<!-- cases: N/A 9 panics: WithOrder itself cannot panic -->
<!-- cases: N/A 10 compatibility: alias registrations (As/Bind) accept no options and are excluded from collections, so WithOrder cannot be attached to them -->

### Requirement: Order() panic is reported
If `Order()` panics, `InvokeAll[T]` SHALL return no collection and an error reading ``DI: Order() of collection member `<name>` panicked: <value>``. When the panic value is an error it SHALL be wrapped, so `errors.Is` matches it.

#### Scenario: Panicking Order()
```gherkin
Given a root scope with lazy services "ok" and "bad" implementing "EventHandler" and "Ordered"
And Order() of "bad" panics with <panic>
When the consumer calls InvokeAll for type "EventHandler"
Then the result is nil
And the error message contains "bad"
And <match>

Examples:
  | panic                       | match                                     |
  | error ErrOrderBroken        | errors.Is(err, ErrOrderBroken) is true    |
  | string "boom"               | the error message is "DI: Order() of collection member `bad` panicked: boom" |
  | Order() on a nil *Handler   | the error message contains "nil pointer"  |
```

#### Scenario: MustInvokeAll surfaces the Order() panic as its panic
```gherkin
Given a root scope with a lazy service "bad" whose Order() panics with error ErrOrderBroken
When the consumer calls MustInvokeAll for type "EventHandler"
Then the call panics with an error for which errors.Is(err, ErrOrderBroken) is true
```

<!-- cases: N/A 2 overrides: setting WithOrder avoids calling Order(), covered above -->
<!-- cases: N/A 3 zero values, 4 boundaries: a single panicking member is sufficient -->
<!-- cases: N/A 8 concurrency: Order() is called on the calling goroutine -->

### Requirement: Ordering never affects lifecycle order
`Ordered` and `WithOrder` SHALL affect only the order of `InvokeAll` slices. Construction, start and shutdown order SHALL remain defined solely by the dependency graph and registration order.

#### Scenario: Shutdown ignores collection order
```gherkin
Given a root scope with lazy services "a" and "b" implementing "EventHandler" and "Shutdowner"
And "a" is registered before "b"
And "a" is registered with WithOrder(10) and "b" with WithOrder(-10)
And a lazy service "bus" implementing "Shutdowner" whose provider calls InvokeAll for type "EventHandler"
When the consumer invokes "bus" and then shuts down the root scope
Then "bus" is shut down before both "a" and "b"
And the collection seen by "bus" was "b", "a"
```

#### Scenario: Start ignores collection order
```gherkin
Given a root scope with Starter services "a" and "b" registered in that order with no dependency between them
And "a" is registered with WithOrder(10) and "b" with WithOrder(-10)
When the consumer starts the root scope
Then "a" is started before "b"
```

<!-- cases: N/A 2 overrides, 3 zero values, 4 boundaries, 5 errors: this requirement asserts independence only -->
<!-- cases: N/A 8 concurrency, 9 panics: covered by collection-invocation and the Order() panic requirement -->

### Requirement: Name-keyed collections are unordered
`InvokeAllMap[T]` SHALL NOT call `Order()` on members, because a map has no order.

#### Scenario: Order() not called for map collections
```gherkin
Given a root scope with a lazy service "bad" implementing "EventHandler" and "Ordered" whose Order() panics with error ErrOrderBroken
When the consumer calls InvokeAllMap for type "EventHandler"
Then the error is nil
And the map has key "bad"
```

<!-- cases: N/A 2 overrides, 3 zero values, 4 boundaries, 5 errors, 6 duplicates, 7 lifecycle, 8 concurrency: no ordering logic runs for maps -->
<!-- cases: 9 panics: covered by the scenario above (a panicking Order() is never reached) -->
<!-- cases: 10 compatibility: purely additive; existing Invoke APIs do not consult Ordered -->
