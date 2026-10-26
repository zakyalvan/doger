# Spec Delta

## Purpose

Makes resolution by assignable type deterministic: `InvokeAs[T]`, `MustInvokeAs[T]` and the implicit-aliasing fallback of struct injection pick the same service on every run, instead of an arbitrary map-iteration match.

## ADDED Requirements

### Requirement: InvokeAs picks the first match deterministically
When several visible services are assignable to `T`, `InvokeAs[T]` and `MustInvokeAs[T]` SHALL select the one in the nearest scope (the invoking scope before its ancestors), then the earliest registration, then the smallest service name. A service hidden by a service of the same name in a nearer scope SHALL NOT be a candidate, as for collections. Aliases SHALL remain candidates, as today, except those of the invoking service.

#### Scenario: Selection among several matches
```gherkin
Given a root scope and a child scope "request"
And services registered in order: <registrations>, each implementing "Greeter"
When the consumer calls InvokeAs[Greeter] from <scope>
Then the selected service is "<selected>"

Examples:
  | registrations                                              | scope     | selected |
  | "b" in root, "a" in root                                   | root      | b        |
  | "a" in root, "b" in root                                   | root      | a        |
  | "a" in root, "z" in "request"                              | "request" | z        |
  | "a" in root, "z" in "request"                              | root      | a        |
  | "b" in root, "a" in root, then Override of "b" in root     | root      | b        |
  | "b" in root, then root cloned, "a" in the clone's root     | the clone | b        |
  | "b" in root, "a" in root, then root cloned                 | the clone | b        |
  | "x" in root, "a" in root, alias "g" of "a" as Greeter in "request" | "request" | g, which resolves "a" |
```

#### Scenario: A shadowed service is not a candidate
```gherkin
Given a root scope with lazy "db" implementing "Store"
And a child scope "request" where "db" is registered as <child_db>
When the consumer calls <call> from "request"
Then the result is <result>

Examples:
  | child_db                     | call                                                    | result                                           |
  | the value "a string"         | InvokeAs[Store]                                          | an error matching ErrServiceNotMatch             |
  | the value "a string"         | InvokeStruct for a struct with field `S Store \`do:""\`` | an error matching ErrServiceNotFound, as when no service implements Store |
  | lazy "db" implementing Store | InvokeAs[Store]                                          | the child's "db"                                 |
```

#### Scenario: Selection is stable across runs
```gherkin
Given a root scope with 20 lazy services "s00" to "s19" registered in that order, each implementing "Greeter"
When the consumer calls InvokeAs[Greeter] on a new container built the same way, 100 times
Then every call selects "s00"
```

#### Scenario: Single and missing matches are unchanged
```gherkin
Given a root scope with <services>
When the consumer calls InvokeAs[Greeter]
Then the result is <result>

Examples:
  | services                           | result                         |
  | one lazy "a" implementing Greeter  | "a", nil error                 |
  | no service implementing Greeter    | an error matching ErrServiceNotMatch |
```

<!-- cases: 1 default: the first row; there is no option -->
<!-- cases: N/A 2 overrides: InvokeNamed selects a specific service -->
<!-- cases: 3 zero values: no matching service keeps ErrServiceNotMatch -->
<!-- cases: 4 boundaries: one match, two, twenty -->
<!-- cases: N/A 5 errors: error paths are unchanged -->
<!-- cases: 6 duplicates: Override keeps the position of the first registration (registration-options); a same-name service in a nearer scope hides an ancestor's -->
<!-- cases: 7 lifecycle: child scope and clone rows -->
<!-- cases: 8 concurrency: the stable scenario replaces the former random pick; runs under -race -->
<!-- cases: N/A 9 panics: unchanged -->
<!-- cases: 10 compatibility: BEHAVIOR CHANGE (flagged) - the pick was random, so no consumer could rely on a specific one; hooks still receive the requested type name. BEHAVIOR CHANGE (flagged) - a shadowed ancestor service is no longer returned. -->

### Requirement: InvokeAs skips the invoking service
When `InvokeAs[T]` or `MustInvokeAs[T]` is called from a provider, the service that provider builds SHALL NOT be a candidate, as for collections, and neither SHALL an alias whose target chain resolves to it, so a decorator can resolve the service it wraps. When it is the only match, the result SHALL be an error matching `ErrServiceNotMatch`.

#### Scenario: The invoking service is not a candidate
```gherkin
Given a root scope with lazy "decor" implementing "Logger", whose provider calls InvokeAs[Logger] and wraps the result
And <others>
When the consumer calls InvokeNamed for "decor"
Then the result is <result>

Examples:
  | others                                            | result                                               |
  | lazy "base" implementing Logger, registered after "decor" | "decor" wrapping "base", nil error                  |
  | lazy "base" implementing Logger, registered before "decor" | "decor" wrapping "base", nil error                 |
  | no other service implementing Logger              | an error matching ErrServiceNotMatch, not ErrCircularDependency |
  | alias "decor-alias" of "decor" as Logger, then lazy "base" implementing Logger | "decor" wrapping "base", nil error |
  | alias "a1" of "decor", alias "a2" of "a1", both as Logger, then lazy "base" implementing Logger | "decor" wrapping "base", nil error |
  | alias "decor-alias" of "decor" as Logger, and no other service | an error matching ErrServiceNotMatch, not ErrCircularDependency |
```

#### Scenario: MustInvokeAs from a provider
```gherkin
Given a root scope with lazy "decor" implementing "Greeter", whose provider wraps MustInvokeAs[Greeter](i)
And lazy "base" implementing "Greeter", registered after "decor"
When the consumer calls InvokeNamed for "decor"
Then "decor" wraps "base"
And ExplainNamedService(root, "decor") lists exactly the dependency base
```

<!-- cases: 1 default: the first row -->
<!-- cases: N/A 2 overrides: InvokeNamed selects a specific service, including the provider's own -->
<!-- cases: 3 zero values: no other match gives ErrServiceNotMatch -->
<!-- cases: 4 boundaries: zero and one other match -->
<!-- cases: 5 errors: ErrServiceNotMatch instead of ErrCircularDependency -->
<!-- cases: 6 duplicates: registration order no longer matters for self-selection; aliases of the invoking service, direct or chained, are skipped too -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: unchanged -->
<!-- cases: 10 compatibility: BEHAVIOR CHANGE (flagged) - a decorator calling InvokeAs for its own interface no longer fails with ErrCircularDependency; a lone self-match fails with ErrServiceNotMatch. BEHAVIOR CHANGE (flagged) - an alias of the invoking service is skipped too -->

### Requirement: Implicit-aliasing struct injection uses the same selection
When `InvokeStruct` falls back to type-based resolution for an empty tag (`do:""`) because no service has the field type's name, it SHALL select the candidate by the same rule as `InvokeAs`, including skipping the service whose provider calls it.

#### Scenario: Struct fallback selects deterministically
```gherkin
Given a root scope with lazy "b" then lazy "a", both implementing "Greeter"
And a struct type with field `G Greeter \`do:""\``
When the consumer calls InvokeStruct for that struct 100 times on new containers built the same way
Then field G is service "b" every time
```

<!-- cases: 1 default: the scenario -->
<!-- cases: N/A 2 overrides: a named tag selects a specific service -->
<!-- cases: N/A 3 zero values: an empty tag is the subject -->
<!-- cases: N/A 4 boundaries: covered by the InvokeAs requirement -->
<!-- cases: N/A 5 errors: unchanged -->
#### Scenario: Struct fallback skips the invoking service
```gherkin
Given a root scope with lazy "decor" implementing "Greeter", whose provider calls InvokeStruct for a struct with field `G Greeter \`do:""\``
And lazy "base" implementing "Greeter", registered after "decor"
When the consumer calls InvokeNamed for "decor"
Then the provider's field G is service "base"
```

<!-- cases: N/A 6 duplicates, 7 lifecycle: covered by the InvokeAs requirement -->
<!-- cases: 8 concurrency: 100 repetitions -->
<!-- cases: N/A 9 panics: unchanged -->
<!-- cases: 10 compatibility: BEHAVIOR CHANGE (flagged), same as InvokeAs -->
