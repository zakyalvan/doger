# collection-struct-injection Specification

## Purpose

Lets struct injection fill a slice or name-keyed map field with every visible service assignable to its element type, using the same membership and order rules as `InvokeAll`/`InvokeAllMap`.

## Requirements

### Requirement: The `*` tag injects a collection
For a field tagged with the configured struct tag key and the value `*` (default `do:"*"`), `InvokeStruct` SHALL set a `[]E` field to the result of `InvokeAll[E]` and a `map[string]E` field to the result of `InvokeAllMap[E]`, resolved from the same injector. A member failure SHALL fail `InvokeStruct` with the `InvokeAll` error.

#### Scenario: Collection fields
```gherkin
Given a root scope with lazy "console" and lazy "audit" implementing "EventHandler", registered in that order
And a struct with field <field>
When the consumer calls InvokeStruct for that struct
Then the field holds <value>
And the error is <error>

Examples:
  | field                                          | value                                             | error |
  | `Handlers []EventHandler \`do:"*"\``           | [console, audit]                                  | nil   |
  | `ByName map[string]EventHandler \`do:"*"\``    | {"console": console, "audit": audit}              | nil   |
  | `Empty []Unrelated \`do:"*"\``                  | an empty, non-nil slice                           | nil   |
  | `handlers []EventHandler \`do:"*"\`` (unexported) | [console, audit]                               | nil   |
```

#### Scenario: Custom struct tag key
```gherkin
Given a root scope created with StructTagKey "inject"
And lazy "console" implementing "EventHandler"
And a struct with field `Handlers []EventHandler \`inject:"*"\``
When the consumer calls InvokeStruct for that struct
Then Handlers is [console]
```

#### Scenario: Invalid collection fields
```gherkin
Given a root scope with lazy "console" implementing "EventHandler"
And a struct "S" with field <field>
When the consumer calls InvokeStruct for "S"
Then the error message is "<message>"

Examples:
  | field                                        | message                                                                    |
  | `H EventHandler \`do:"*"\``                   | DI: tag `*` requires a slice or map[string] field, got `S.H`               |
  | `M map[int]EventHandler \`do:"*"\``           | DI: tag `*` requires a slice or map[string] field, got `S.M`               |
  | `K map[handlerKey]EventHandler \`do:"*"\`` (type handlerKey string) | DI: tag `*` requires a slice or map[string] field, got `S.K` |
```

#### Scenario: A failing member fails the struct
```gherkin
Given a root scope with lazy "console" implementing "EventHandler" whose provider returns errBuild
And a struct with field `Handlers []EventHandler \`do:"*"\``
When the consumer calls InvokeStruct for that struct
Then errors.Is(err, errBuild) is true
And the error message contains "collection member `console`"
```

#### Scenario: Struct collections inside a provider exclude the invoking service
```gherkin
Given lazy "composite" implementing "EventHandler", whose provider calls InvokeStruct for a struct with field `Others []EventHandler \`do:"*"\``
And lazy "console" implementing "EventHandler"
When the consumer invokes "composite"
Then Others is [console]
```

<!-- cases: 1 default: the first row with the default tag key -->
<!-- cases: 2 overrides: StructTagKey; ordering overrides (Ordered, WithOrder) apply as for InvokeAll -->
<!-- cases: 3 zero values: an empty collection is a non-nil empty slice/map -->
<!-- cases: N/A 4 boundaries: covered by collection-invocation -->
<!-- cases: 5 errors: non-collection field kinds, map keys other than exactly string, and member failures -->
<!-- cases: N/A 6 duplicates: shadowing and alias exclusion follow collection-invocation -->
<!-- cases: 7 lifecycle: self-exclusion inside a provider -->
<!-- cases: N/A 8 concurrency: covered by collection-invocation -->
<!-- cases: N/A 9 panics: an Order() panic is returned as in collection-ordering -->
<!-- cases: 10 compatibility: BREAKING edge case (flagged) - a service literally named `*` can no longer be injected by tag; every other tag value is unchanged -->
