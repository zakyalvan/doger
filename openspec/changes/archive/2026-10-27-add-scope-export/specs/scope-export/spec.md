# Spec Delta

## Purpose

Lets a consumer keep a subsystem's services private in a child scope and publish selected ones to the parent scope with `Export`, so the parent can use them while the child keeps ownership of their lifecycle.

## ADDED Requirements

### Requirement: Export publishes a child-scope service in the parent
`Export[T](from, opts...)` SHALL register in `from`'s parent a service that resolves the service named `NameOf[T]()` from `from`; `ExportNamed[T](from, name, opts...)` resolves `name`. The published name SHALL be the source name unless `ExportAs(name)` is given. Invoking it SHALL return the instance owned by `from`.

#### Scenario: Export publishes under the source or a chosen name
```gherkin
Given a root scope with child scope "redis"
And a lazy "*app.Client" registered in scope "redis", producing "client-1"
And the consumer called <export>
When the consumer invokes <published> from the root
Then the result is "client-1"
And it is the same instance as invoking "*app.Client" from scope "redis"
And the provider was called 1 time

Examples:
  | export                                                                  | published       |
  | doger.Export[*app.Client](redis)                                        | "*app.Client"   |
  | doger.Export[*app.Client](redis, doger.ExportAs("cache-client"))        | "cache-client"  |
  | doger.ExportNamed[*app.Client](redis, "*app.Client", doger.ExportAs("c")) | "c"           |
```

#### Scenario: Exported services stay private otherwise
```gherkin
Given a root scope with child scope "redis"
And lazy "*app.Client" and lazy "*app.Pool" registered in scope "redis"
And the consumer called doger.Export[*app.Client](redis)
When the consumer invokes "*app.Pool" from the root
Then the error matches ErrServiceNotFound
```

#### Scenario: Export from a grandchild through two scopes
```gherkin
Given a root scope with child scope "a" and grandchild scope "a/b"
And a lazy "*app.Client" registered in scope "a/b", producing "client-1"
And the consumer called doger.Export[*app.Client](b), then doger.Export[*app.Client](a)
When the consumer invokes "*app.Client" from the root
Then the result is "client-1"
```

<!-- cases: 2 overrides: ExportAs is the only option; rows 2 and 3 -->
<!-- cases: 3 zero values: covered by "Export validates its inputs" (nil scope, empty name) -->
<!-- cases: 4 boundaries: one export, a chained export across two scopes -->
<!-- cases: 5 errors: covered by "Export validates its inputs" -->
<!-- cases: 6 duplicate: covered by "Export validates its inputs" -->
<!-- cases: 7 lifecycle: covered by the lifecycle, shutdown and clone requirements -->
<!-- cases: 8 concurrency: covered by "Concurrent first invocations of an export" -->
<!-- cases: N/A 9 panics: covered by "Export validates its inputs" (Must variants) -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Export validates its inputs
`Export` and `ExportNamed` SHALL return an error and register nothing when `from` is nil or has no parent, the source is not registered in `from` itself or its type is not assignable to `T`, or the published name is empty or taken in the parent. A name held by an unresolved default is not taken: the export replaces it. `MustExport` and `MustExportNamed` SHALL panic with the error.

#### Scenario: Invalid exports are rejected
```gherkin
Given a root scope with child scope "redis"
And a lazy "*app.Client" registered in scope "redis"
And a lazy "*app.Other" registered in the root
When the consumer calls <call>
Then it returns an error containing "<message>"
And the root has no service named <name>

Examples:
  | call                                                              | message                                              | name          |
  | doger.Export[*app.Client](nil)                                    | DI: cannot export from a nil scope                   | "*app.Client" |
  | doger.Export[*app.Client](<the root's own *Scope>)                | has no parent scope to export to                     | "*app.Client" |
  | doger.Export[*app.Missing](redis)                                 | DI: service `*app.Missing` has not been declared     | "*app.Missing"|
  | doger.ExportNamed[*app.Other](redis, "*app.Client")               | DI: service `*app.Client` is not assignable to       | "*app.Client" |
  | doger.Export[*app.Client](redis, doger.ExportAs(""))              | DI: export name cannot be empty                      | ""            |
  | doger.Export[*app.Client](redis, doger.ExportAs("*app.Other"))    | DI: service `*app.Other` has already been declared   | (unchanged)   |
```

#### Scenario: A service visible only through an ancestor cannot be exported
```gherkin
Given a root scope with child scope "a" and grandchild scope "a/b"
And a lazy "*app.Client" registered in scope "a"
When the consumer calls doger.Export[*app.Client](b)
Then it returns an error containing "DI: service `*app.Client` has not been declared"
```

#### Scenario: Must variants panic with the error
```gherkin
Given a root scope with child scope "redis" and no service registered
When the consumer calls <call>
Then it panics with an error containing "DI: service `*app.Client` has not been declared"

Examples:
  | call                                                   |
  | doger.MustExport[*app.Client](redis)                   |
  | doger.MustExportNamed[*app.Client](redis, "*app.Client") |
```

<!-- cases: N/A 1 default: covered by "Export publishes a child-scope service in the parent" -->
<!-- cases: N/A 2 overrides: Override* on the published name replaces the export as for any service -->
<!-- cases: 3 zero values: nil scope, empty name rows -->
<!-- cases: 4 boundaries: source in the scope itself vs only in an ancestor -->
<!-- cases: 5 errors: every row; errors are plain errors like those of As, not sentinels -->
<!-- cases: 6 duplicate: last row; exporting the same service twice under one name hits the same row -->
<!-- cases: N/A 7 lifecycle, 8 concurrency: registration-time validation only -->
<!-- cases: 9 panics: Must variants -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: An export of a default is a default
An export whose source was registered with `AsDefault()` SHALL itself be a default in the parent: an explicit service of the published name replaces it or makes it skip, as specified in default-registrations, instead of failing. The source default SHALL NOT yield to an export of itself.

#### Scenario: The application's service wins over an exported default
```gherkin
Given a root scope with child scope "redis"
And a lazy "*app.Client" registered in scope "redis" with doger.AsDefault(), producing "ext-client"
And <registrations>
When the consumer invokes "*app.Client" from scope "<from>"
Then the result is "<result>"

Examples:
  | registrations                                                                          | from  | result     |
  | doger.Export[*app.Client](redis) only                                                  | root  | ext-client |
  | doger.Export[*app.Client](redis) only                                                  | redis | ext-client |
  | an explicit "*app.Client" ("app-client") in the root, then doger.Export[*app.Client](redis) | root  | app-client |
  | an explicit "*app.Client" ("app-client") in the root, then doger.Export[*app.Client](redis) | redis | app-client |
  | doger.Export[*app.Client](redis), then an explicit "*app.Client" ("app-client") in the root | root  | app-client |
  | doger.Export[*app.Client](redis), then an explicit "*app.Client" ("app-client") in the root | redis | app-client |
```

#### Scenario: Exporting a default where an explicit service exists succeeds without effect
```gherkin
Given a root scope with child scope "redis"
And a lazy "*app.Client" registered in scope "redis" with doger.AsDefault()
And an explicit lazy "*app.Client" registered in the root
When the consumer calls doger.Export[*app.Client](redis)
Then it returns nil
And the root's "*app.Client" is still the explicit registration
```

<!-- cases: 2 overrides: the explicit registration is the override, before or after the export -->
<!-- cases: N/A 3 zero values: covered by "Export validates its inputs" -->
<!-- cases: 4 boundaries: registration before, after, and no explicit service -->
<!-- cases: 5 errors: exporting a default over another default of the published name fails as a duplicate, as two defaults do in default-registrations -->
<!-- cases: 6 duplicate: second scenario -->
<!-- cases: 7 lifecycle: an exported default resolved before the explicit registration freezes as in default-registrations -->
<!-- cases: N/A 8 concurrency: covered by default-registrations' concurrency requirement -->
<!-- cases: N/A 9 panics: an explicit registration over a resolved exported default panics as in default-registrations -->
<!-- cases: 10 compatibility: exports of non-default services are unchanged -->

### Requirement: An export resolves from its source scope only
An export SHALL always resolve its source name from the scope it was exported from, whatever scope it is invoked from, and SHALL be listed with service type `ServiceTypeAlias`. A same-name service registered in the parent or elsewhere SHALL NOT change what the export resolves to.

#### Scenario: Invocation scope does not change the target
```gherkin
Given a root scope with child scopes "redis" and "app"
And a lazy "*app.Client" registered in scope "redis", producing "redis-client"
And a lazy "*app.Client" registered in scope "app", producing "app-client"
And the consumer called doger.Export[*app.Client](redis, doger.ExportAs("shared"))
When the consumer invokes "shared" from scope "<from>"
Then the result is "redis-client"

Examples:
  | from  |
  | root  |
  | app   |
  | redis |
```

#### Scenario: An export is listed as an alias
```gherkin
Given a root scope with child scope "redis" exporting "*app.Client"
And "*app.Client" was invoked once from the root
When the consumer calls ListProvidedServices on the root
Then the root lists "*app.Client"
And doger.ExplainNamedService(root, "*app.Client") reports ServiceTypeAlias and a dependency on "*app.Client" in scope "redis"
```

#### Scenario: A same-name grandchild does not capture the export
```gherkin
Given a root scope with child scopes "redis" and "app", and grandchild scope "app/redis"
And a lazy "*app.Client" in scope "redis" producing "direct", and a lazy "*app.Client" in scope "app/redis" producing "nested"
And the consumer called doger.Export[*app.Client](redis)
When the consumer invokes "*app.Client" from the root, in 100 new containers built the same way
Then every result is "direct"
```

#### Scenario: An export replaces an unresolved default in the parent
```gherkin
Given a root scope with child scope "redis"
And a lazy "*app.Client" registered in the root with doger.AsDefault(), producing "root-default", never invoked
And a lazy "*app.Client" registered in scope "redis" without options, producing "redis-client"
When the consumer calls doger.Export[*app.Client](redis)
Then it returns nil
And invoking "*app.Client" from the root returns "redis-client"
```

<!-- cases: N/A 2 overrides: no option changes resolution -->
<!-- cases: N/A 3 zero values, 5 invalid input: covered by "Export validates its inputs" -->
<!-- cases: 4 boundaries: invoked from parent, sibling and the source scope itself -->
<!-- cases: N/A 6 duplicate: covered by "Export validates its inputs" -->
<!-- cases: 7 lifecycle: covered by "An export has no lifecycle of its own" -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent first invocations of an export" -->
<!-- cases: N/A 9 panics: resolution errors are returned, as for aliases -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: An export has no lifecycle of its own
An export SHALL NOT be started, run, handled by a component kind or health-checked on its own. The exported service SHALL be started, handled and shut down exactly once, by the scope that owns it. A health check of the export SHALL run the exported service's health check, except from the source scope or its descendants, where the export is skipped.

#### Scenario: Exported Starter and Shutdowner run once
```gherkin
Given a root scope with child scope "redis"
And a lazy "*app.Pool" registered in scope "redis", implementing Start(ctx) error, Shutdown() error and HealthCheck() error, each counting calls
And the consumer called doger.Export[*app.Pool](redis)
And a lazy "*app.App" in the root that invokes "*app.Pool"
And "*app.App" was invoked from the root
And <before>
When the consumer calls <operation>
Then the pool's <method> was called <count> times

Examples:
  | before                      | operation          | method      | count |
  | nothing else happened       | root.Start()       | Start       | 1     |
  | root.Start() was called     | root.Shutdown()    | Shutdown    | 1     |
  | nothing else happened       | root.HealthCheck() | HealthCheck | 1     |
```

#### Scenario: Root health check reports the exported service
```gherkin
Given a root scope with child scope "redis"
And a lazy "*app.Pool" registered in scope "redis" whose HealthCheck() returns errors.New("pool down"), invoked once
And the consumer called doger.Export[*app.Pool](redis)
When the consumer calls HealthCheck on the root
Then the result has key "*app.Pool" with an error containing "pool down"
```

#### Scenario: A health check from the source scope checks the exported service once
```gherkin
Given a root scope with child scope "redis"
And a lazy "*app.Pool" registered in scope "redis" <source registered>, whose HealthCheck() counts calls and returns errors.New("pool down"), invoked once
And the consumer called doger.Export[*app.Pool](redis, doger.ExportAs("pool"))
When the consumer calls HealthCheck on scope "redis"
Then the result has key "*app.Pool" with an error containing "pool down"
And the result has no key "pool"
And the pool's HealthCheck was called 1 time

Examples:
  | source registered        |
  | without options          |
  | with doger.AsDefault()   |
```

<!-- cases: N/A 2 overrides: lifecycle ownership is not configurable; to make the parent own a service, register it in the parent -->
<!-- cases: N/A 3 zero values, 5 invalid input: no input -->
<!-- cases: 4 boundaries: health check from the parent and from the source scope -->
<!-- cases: 6 duplicate: the source-scope scenario pins one call and one key -->
<!-- cases: 7 lifecycle: start, shutdown and health check rows; shutdown order is in dependency-declaration -->
<!-- cases: N/A 8 concurrency: existing parallel start and shutdown apply unchanged -->
<!-- cases: N/A 9 panics: a panicking Start or Shutdown follows container-start and the shutdown report unchanged -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Start order across an export
A Starter in the parent that depends on an export SHALL be started after the exported Starter, because the export records a dependency from the published service to its source.

#### Scenario: Parent Starter starts after the exported one
```gherkin
Given a root scope with child scope "redis"
And a lazy "*app.Pool" in scope "redis" implementing Start(ctx) error that records its start
And the consumer called doger.Export[*app.Pool](redis)
And a lazy "*app.Server" in the root implementing Start(ctx) error that records its start and invokes "*app.Pool" in its provider
When the consumer calls Start on the root
Then the start order is "*app.Pool", "*app.Server"
```

<!-- cases: N/A 2-6: no input beyond registrations -->
<!-- cases: 7 lifecycle: ordering is the subject -->
<!-- cases: N/A 8 concurrency: with StartParallelism > 1 the dependency still orders the two (container-start "Parallel start keeps dependency order") -->
<!-- cases: N/A 9 panics, 10 compatibility: unchanged / new API -->

### Requirement: Collections and type selection see exports in the parent
In the parent and its other descendants, an export SHALL be a member of collections and a candidate of by-type selection, resolving to the exported instance. Read from the exporting scope or its descendants, the export SHALL NOT be a collection member, so the exported service is not counted twice. A provider's own export SHALL NOT be a by-type candidate for that provider, as for an `As` alias of it.

#### Scenario: Collection membership of an export
```gherkin
Given a root scope with child scopes "redis" and "app"
And a lazy "*app.Pool" in scope "redis" registered <source registered>, implementing HealthCheck() error
And the consumer called doger.Export[*app.Pool](redis, doger.ExportAs("pool"))
When the consumer calls doger.InvokeAllMap[doger.Healthchecker] from scope "<from>"
Then the keys are <keys>

Examples:
  | source registered                 | from  | keys                |
  | without options                   | root  | "pool"              |
  | without options                   | app   | "pool"              |
  | without options                   | redis | "*app.Pool"         |
  | with doger.AsDefault()            | root  | "pool"              |
  | with doger.AsDefault()            | redis | "*app.Pool"         |
```

#### Scenario: InvokeAs in the parent finds the export
```gherkin
Given a root scope with child scope "redis"
And a lazy "*app.Pool" in scope "redis" implementing HealthCheck() error
And the consumer called doger.Export[*app.Pool](redis)
When the consumer calls doger.InvokeAs[doger.Healthchecker] from the root
Then the result is the pool instance owned by scope "redis"
```

#### Scenario: A provider does not select its own export by type
```gherkin
Given a root scope with child scope "a" and grandchild scope "c" of "a"
And a lazy "dec" of type "*app.Decorator" in scope <scope> implementing "API", whose provider calls doger.InvokeAs[API] and wraps the result as "dec(<inner>)"
And the consumer called <exports>
And a lazy "base" of type "*app.Base" registered in the root afterwards, implementing "API" as "base"
When the consumer invokes "dec" from scope <scope>
Then the result is "dec(base)"

Examples:
  | scope | exports                                                                                          |
  | "a"   | doger.ExportNamed[API](a, "dec", doger.ExportAs("api"))                                          |
  | "c"   | doger.ExportNamed[API](c, "dec"), then doger.ExportNamed[API](a, "dec", doger.ExportAs("api"))   |
```

<!-- cases: N/A 2 overrides: membership has no option; the export's position is its own registration in the parent, so WithOrder on the source does not move it -->
<!-- cases: 3 zero values: no export means no member (existing "No match yields an empty collection") -->
<!-- cases: 4 boundaries: parent, sibling, exporting scope rows; a provider and its own renamed export, directly and through a chain of exports -->
<!-- cases: N/A 5 invalid input: unchanged collection errors -->
<!-- cases: N/A 6 duplicate: the exporting-scope row pins no double count -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: unchanged collection behavior -->
<!-- cases: 10 compatibility: aliases created with As/Bind stay excluded from collections -->

### Requirement: Cycles through an export are reported
A dependency cycle that passes through an export SHALL fail the invocation with an error matching `ErrCircularDependency`.

#### Scenario: Source provider depends back on the parent dependent
```gherkin
Given a root scope with child scope "redis"
And a lazy "*app.Client" in scope "redis" whose provider invokes "*app.App"
And the consumer called doger.Export[*app.Client](redis)
And a lazy "*app.App" in the root whose provider invokes "*app.Client"
When the consumer invokes "*app.App" from the root
Then the error matches ErrCircularDependency
```

<!-- cases: N/A 2-6: no input beyond registrations -->
<!-- cases: 7 lifecycle: cycle detection at build time -->
<!-- cases: N/A 8 concurrency, 9 panics: unchanged cycle detection -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: An export follows its source scope through clone and shutdown
A clone's export SHALL resolve from the clone's scope with the source scope's name, never from the original container. After the source scope was shut down, invoking the export SHALL fail with an error matching `ErrServiceNotFound`.

#### Scenario: Clone re-binds the export
```gherkin
Given a root scope with child scope "redis", a lazy "*app.Client" in it, and its export
And the original "*app.Client" was invoked from the root
When the consumer clones the root and invokes "*app.Client" from the clone
Then the result is a different instance from the original's
And the clone's scope "redis" built it
```

#### Scenario: Invoking an export after its source scope shut down
```gherkin
Given a root scope with child scope "redis", a lazy "*app.Client" in it, and its export, invoked once
And the consumer called Shutdown on scope "redis"
When the consumer invokes "*app.Client" from the root
Then the error matches ErrServiceNotFound
And the error message contains "redis"
```

<!-- cases: N/A 2 overrides, 3 zero values, 4 boundaries, 5 invalid input: no input -->
<!-- cases: N/A 6 duplicate: clones copy registrations as they are -->
<!-- cases: 7 lifecycle: clone and shutdown are the subject -->
<!-- cases: N/A 8 concurrency: Clone and Shutdown concurrency rules are unchanged -->
<!-- cases: N/A 9 panics: errors are returned -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Concurrent first invocations of an export
Concurrent first invocations of an export, from any scopes, SHALL build the exported service exactly once and return the same instance.

#### Scenario: One build under concurrent invocation
```gherkin
Given a root scope with child scope "redis"
And a lazy "*app.Client" in scope "redis" whose provider counts its calls, and its export
When 100 goroutines invoke "*app.Client" from the root concurrently
Then every goroutine receives the same instance
And the provider was called exactly 1 time
```

<!-- cases: N/A 2-7: no input beyond registrations -->
<!-- cases: 8 concurrency: the subject; runs under -race -->
<!-- cases: N/A 9 panics, 10 compatibility: unchanged / new API -->
