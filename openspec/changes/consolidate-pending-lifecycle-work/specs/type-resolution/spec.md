# Spec Delta

## Purpose

Makes resolution by assignable type deterministic: `InvokeAs[T]`, `MustInvokeAs[T]` and the implicit-aliasing fallback of struct injection pick the same service on every run, instead of an arbitrary map-iteration match.

## ADDED Requirements

### Requirement: InvokeAs picks the first match deterministically
When several visible services are assignable to `T`, `InvokeAs[T]` and `MustInvokeAs[T]` SHALL select the one in the nearest scope (the invoking scope before its ancestors), then the earliest registration, then the smallest service name. A service hidden by a service of the same name in a nearer scope SHALL NOT be a candidate, as for collections. Aliases SHALL remain candidates, as today.

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
  | the value "a string"         | InvokeStruct for a struct with field `S Store \`do:""\`` | an error matching ErrServiceNotMatch             |
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
<!-- cases: 10 compatibility: BEHAVIOR CHANGE (flagged) - the pick was random, so no consumer could rely on a specific one; hooks still receive the requested type name. BEHAVIOR CHANGE (flagged) - a shadowed ancestor service is no longer returned -->

### Requirement: Implicit-aliasing struct injection uses the same selection
When `InvokeStruct` falls back to type-based resolution for an empty tag (`do:""`) because no service has the field type's name, it SHALL select the candidate by the same rule as `InvokeAs`.

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
<!-- cases: N/A 6 duplicates, 7 lifecycle: covered by the InvokeAs requirement -->
<!-- cases: 8 concurrency: 100 repetitions -->
<!-- cases: N/A 9 panics: unchanged -->
<!-- cases: 10 compatibility: BEHAVIOR CHANGE (flagged), same as InvokeAs -->
