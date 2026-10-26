# Spec Delta

<!-- type-resolution is introduced by consolidate-pending-lifecycle-work, which must be archived before this change. The MODIFIED block copies its requirement whole. -->

## MODIFIED Requirements

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
  | "a" in root, "z" with doger.AsDefault() in "request"       | "request" | a        |
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
<!-- cases: 10 compatibility: BEHAVIOR CHANGE (flagged) - the pick was random, so no consumer could rely on a specific one; hooks still receive the requested type name. BEHAVIOR CHANGE (flagged) - a shadowed ancestor service is no longer returned. The default row applies only to registrations given AsDefault(). -->

## ADDED Requirements

### Requirement: InvokeAs prefers non-default services over defaults
By-type selection (`InvokeAs[T]` and implicit-aliasing struct injection) SHALL consider unresolved defaults only if no other visible candidate is assignable to `T`. A default that already chose its own provider SHALL rank, and shadow, as a non-default; a yielded default SHALL NOT be a candidate. An unresolved or yielded default SHALL NOT hide a same-name non-default in an ancestor.

#### Scenario: Selection by type with defaults
```gherkin
Given a root scope with child scope "ext"
And a lazy "envSource" (an *app.EnvSource) registered in <default_in> with doger.AsDefault(), never invoked
And <explicit>
When the consumer calls doger.InvokeAs[app.PropertySource] from scope "ext"
Then the selected service is "<selected>"

Examples:
  | default_in | explicit                                                     | selected   |
  | ext        | a lazy "mySource" (an *app.MySource) registered in root      | mySource   |
  | root       | a lazy "mySource" (an *app.MySource) registered in root      | mySource   |
  | ext        | nothing                                                      | envSource  |
  | ext        | a lazy "other" (an *app.Other, not a PropertySource) in root | envSource  |
```

#### Scenario: A default that chose its own provider keeps its rank
```gherkin
Given a root scope with child scope "ext"
And a lazy "envSource" (an *app.EnvSource) registered in scope "ext" with doger.AsDefault()
And "envSource" was invoked from scope "ext"
And a lazy "mySource" (an *app.MySource) registered in the root afterwards
When the consumer calls doger.InvokeAs[app.PropertySource] from scope "ext"
Then the selected service is "envSource"
```

#### Scenario: A same-name default does not hide the ancestor's service
```gherkin
Given a root scope with child scope "ext"
And a lazy "source" (an *app.MySource) registered in the root
And a lazy "source" (an *app.EnvSource) registered in scope "ext" with doger.AsDefault()
When the consumer calls doger.InvokeAs[app.PropertySource] from scope "ext"
Then the result is the root's *app.MySource instance
```

#### Scenario: Two defaults are ordered as before
```gherkin
Given a root scope with child scope "ext"
And a lazy "rootSource" registered in the root with doger.AsDefault()
And a lazy "extSource" registered in scope "ext" with doger.AsDefault(), both *app.EnvSource
When the consumer calls doger.InvokeAs[app.PropertySource] from scope "ext"
Then the selected service is "extSource"
```

#### Scenario: Struct injection by type prefers non-default services
```gherkin
Given a root scope
And a lazy "envSource" (an *app.EnvSource) registered with doger.AsDefault()
And a lazy "mySource" (an *app.MySource) registered without options
When the consumer calls doger.InvokeStruct[struct{ Src app.PropertySource `do:""` }]
Then field Src holds the "mySource" instance
```

<!-- cases: 2 overrides: invoking by name selects a default explicitly; registering an explicit service is the override -->
<!-- cases: 3 zero values: no match yields the existing ErrServiceNotMatch -->
<!-- cases: 4 boundaries: zero, one and two defaults -->
<!-- cases: N/A 5 invalid input: unchanged InvokeAs errors -->
<!-- cases: 6 duplicate: the same-name scenario -->
<!-- cases: 7 lifecycle: the chosen-default scenario pins the frozen decision -->
<!-- cases: N/A 8 concurrency: selection runs under the existing locks; decide() is covered in default-registrations -->
<!-- cases: N/A 9 panics: selection does not panic -->
<!-- cases: 10 compatibility: without defaults, selection is unchanged -->
