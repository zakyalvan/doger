# dependency-declaration Specification

## Purpose

Lets a consumer or a component-kind handler declare a dependency between services that do not invoke each other, so construction, start and shutdown follow one deterministic dependency order.

## Requirements

### Requirement: Static declared dependencies are built first
When a registration carries `DependsOn[T]()` or `DependsOnNamed(name)`, the container SHALL build each declared dependency, in declaration order, before calling the dependent's provider (for an eager value: before its first invocation returns). It SHALL record a dependency edge for each, exactly as if the provider had invoked it. This SHALL hold for `Invoke` and for `Start`.

#### Scenario: Declared dependency built before the dependent
```gherkin
Given a root scope recording provider calls
And lazy "db" and lazy "cache"
And lazy "worker" registered with <options> whose provider invokes nothing
When the consumer invokes "worker"
Then the provider call order is <order>
And ExplainNamedService(root, "worker") lists dependencies <deps>

Examples:
  | options                                       | order             | deps      |
  | DependsOnNamed("db")                          | db, worker        | db        |
  | DependsOnNamed("cache"), DependsOnNamed("db") | cache, db, worker | cache, db |
  | none                                          | worker            | none      |
```

#### Scenario: Declared dependency on an eager service
```gherkin
Given a root scope with eager value "cfg"
And lazy "worker" registered with DependsOnNamed("cfg")
When the consumer invokes "worker"
Then ExplainNamedService(root, "worker") lists dependency "cfg"
And ExplainNamedService(root, "cfg") lists dependent "worker"
```

#### Scenario: Declared dependency of an eager service
```gherkin
Given a root scope with eager value "srv" registered with DependsOnNamed("db")
And <setup>
When the consumer invokes "srv" <times>
Then the invocations return <results>
And ExplainNamedService(root, "srv") lists dependencies <deps>

Examples:
  | setup                                                    | times                               | results                         | deps |
  | lazy "db" registered before the first invocation         | twice                               | nil, nil                        | db   |
  | lazy "db" registered only between the two invocations    | twice                               | ErrServiceNotFound, then nil    | db   |
  | nothing else                                             | once                                | ErrServiceNotFound              | none |
```

#### Scenario: Declared dependency resolved through scope ancestry
```gherkin
Given a root scope with lazy "db"
And a child scope "request" with lazy "handler" registered with DependsOnNamed("db")
When the consumer invokes "handler" from scope "request"
Then "db" is built before "handler"
And the recorded edge goes from "request/handler" to "[root]/db"
```

#### Scenario: Concurrent invocation builds declared dependencies once
```gherkin
Given a root scope with lazy "db" whose provider counts its calls
And lazy "worker" registered with DependsOnNamed("db")
When 100 goroutines invoke "worker" concurrently
Then the "db" provider was called exactly 1 time
And the "worker" provider was called exactly 1 time
```

<!-- cases: N/A 2 overrides: declaring is itself the override of implicit (invocation-only) ordering -->
<!-- cases: 3 zero values: DependsOnNamed("") is covered by "Missing or circular declared dependency" -->
<!-- cases: N/A 4 boundaries: 0, 1 and many dependencies are covered by the Examples rows -->
<!-- cases: 7 lifecycle: an eager dependent retries a failed resolution on the next invocation ("Declared dependency of an eager service", second row) -->
<!-- cases: N/A 9 panics: a declared dependency whose provider panics surfaces as the existing provider-panic error; no new path -->
<!-- cases: 10 compatibility: the "none" row pins unchanged behavior for registrations without DependsOn -->

### Requirement: Declared dependency errors
Building a service whose declared dependency is not registered in its scope or any ancestor SHALL fail with an error matching `ErrServiceNotFound`, naming both services. A declared dependency that closes a cycle SHALL fail with an error matching `ErrCircularDependency` that names the chain. Neither SHALL call the dependent's provider.

#### Scenario: Missing or circular declared dependency
```gherkin
Given a root scope with registrations <setup>
When the consumer invokes "<invoked>"
Then the error matches <error>
And the error message contains "<message>"
And the provider of "<invoked>" was not called

Examples:
  | setup                                                             | invoked | error                 | message           |
  | lazy "worker" DependsOnNamed("db")                                | worker  | ErrServiceNotFound    | `db`              |
  | lazy "worker" DependsOnNamed("")                                  | worker  | ErrServiceNotFound    | ``                |
  | lazy "a" DependsOnNamed("b"); lazy "b" DependsOnNamed("a")        | a       | ErrCircularDependency | `a` -> `b` -> `a` |
  | lazy "a" DependsOnNamed("a")                                      | a       | ErrCircularDependency | `a` -> `a`        |
  | lazy "a" DependsOnNamed("b"); lazy "b" whose provider invokes "a" | a       | ErrCircularDependency | `a` -> `b` -> `a` |
```

<!-- cases: N/A 2 overrides: errors cannot be suppressed -->
<!-- cases: 7 lifecycle: through Start, covered in container-start ("A build failure stops Start before any Starter", DependsOnNamed("missing") row) -->
<!-- cases: N/A 8 concurrency: error paths share the build path covered above -->
<!-- cases: N/A 9 panics: errors are returned, not raised -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Dynamic dependency declaration
`DeclareDependency(i, dependent, dependency)` SHALL record `dependent -> dependency` without building either, resolving `dependent` from `i` and `dependency` from the dependent's scope or its ancestors. An unresolved name SHALL give `ErrServiceNotFound`. If `dependency` already reaches `dependent` through recorded or static `DependsOn` edges, it SHALL give `ErrCircularDependency`. Repeating an edge changes nothing. Such edges order start and shutdown, not construction.

#### Scenario: Declaring edges dynamically
```gherkin
Given a root scope with lazy "scheduler", lazy "job-a", lazy "job-b" and lazy "job-s" registered with DependsOnNamed("scheduler"), none built
And the edge "job-b" -> "scheduler" recorded by invoking "job-b", whose provider invokes "scheduler"
And a child scope "tenant" with lazy "tenant-job"
And <given>
When the consumer calls DeclareDependency(<injector>, "<dependent>", "<dependency>")
Then the result is <result>

Examples:
  | given                                       | injector | dependent  | dependency | result                                                 |
  | nothing else                                | root     | scheduler  | job-a      | nil, ExplainNamedService lists job-a under scheduler   |
  | the edge scheduler -> job-a already declared | root    | scheduler  | job-a      | nil, scheduler still has exactly 1 dependency job-a    |
  | nothing else                                | root     | scheduler  | missing    | ErrServiceNotFound                                     |
  | nothing else                                | root     | missing    | job-a      | ErrServiceNotFound                                     |
  | nothing else                                | root     | ""         | job-a      | ErrServiceNotFound                                     |
  | nothing else                                | root     | scheduler  | job-b      | ErrCircularDependency                                  |
  | nothing else                                | root     | job-a      | job-a      | ErrCircularDependency                                  |
  | nothing else                                | root     | scheduler  | job-s      | ErrCircularDependency (static DependsOn, not built)    |
  | nothing else                                | tenant   | scheduler  | tenant-job | ErrServiceNotFound (not visible from scheduler's scope) |
  | nothing else                                | tenant   | tenant-job | scheduler  | nil, edge "tenant/tenant-job" -> "[root]/scheduler"    |
  | DefaultRootScope replaced by this root and restored in t.Cleanup | nil | scheduler | job-a | nil, edge recorded in DefaultRootScope            |
```

#### Scenario: Concurrent declarations are race-free
```gherkin
Given a root scope with lazy "scheduler" and 50 lazy services "job-0" to "job-49"
When 50 goroutines each call DeclareDependency(root, "scheduler", "job-<n>")
Then all 50 calls return nil
And ExplainNamedService(root, "scheduler") lists 50 dependencies
```

#### Scenario: Start rejects a cycle closed by a dynamic edge
```gherkin
Given a root scope with lazy Starter "scheduler" registered first
And lazy Starter "job" registered second, whose provider invokes "scheduler"
And DeclareDependency(root, "scheduler", "job") returned nil before Start, because nothing was built yet
When the consumer calls Start()
Then errors.Is(err, ErrCircularDependency) is true
And the error message contains "`scheduler`" and "`job`"
And no Starter was started
```

<!-- cases: N/A 2 overrides: no option -->
<!-- cases: 3 zero values: an empty name and a nil injector (default root scope) are rows of "Declaring edges dynamically" -->
<!-- cases: 7 lifecycle: cross-scope visibility rows; cycles closed only after build are rejected by Start ("Start rejects a cycle closed by a dynamic edge") -->
<!-- cases: N/A 9 panics: no panic paths; all failures are returned errors -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Clones do not copy declared edges
A clone SHALL NOT copy edges declared with `DeclareDependency`. Static `DependsOn` edges SHALL be recorded again when the clone builds the dependent.

#### Scenario: Clones do not copy declared edges
```gherkin
Given a root scope with lazy "scheduler", lazy "job" and lazy "worker" registered with DependsOnNamed("job")
And DeclareDependency(root, "scheduler", "job") returned nil
And "worker" was invoked on the root scope
When the consumer clones the root scope and invokes "<invoked>" on the clone
Then ExplainNamedService(clone, "<invoked>") lists dependencies <deps>

Examples:
  | invoked   | deps |
  | scheduler | none |
  | worker    | job  |
```

<!-- cases: N/A 2 overrides: re-declare edges on the clone, e.g. from a kind handler, which runs again when the clone starts -->
<!-- cases: N/A 3 zero values, 4 boundaries, 5 errors: no inputs beyond the scenario -->
<!-- cases: 7 lifecycle: the two rows cover a declared edge and a static edge -->
<!-- cases: N/A 8 concurrency: Clone keeps the existing scope locking -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: N/A 10 compatibility: Clone never copied the dependency graph -->

### Requirement: Declared edges order shutdown
Shutdown SHALL treat declared edges, static and dynamic, exactly like invoked dependencies: a dependent is shut down before the services it depends on.

#### Scenario: Scheduler is shut down before the jobs it drives
```gherkin
Given a root scope with built lazy "scheduler" and built lazy "job" recording shutdown order
And <edge>
When the consumer calls Shutdown()
Then the shutdown order is <order>

Examples:
  | edge                                              | order          |
  | DeclareDependency(root, "scheduler", "job")       | scheduler, job |
  | "scheduler" registered with DependsOnNamed("job") | scheduler, job |
```

<!-- cases: N/A 2 overrides: ordering is defined by edges only -->
<!-- cases: N/A 3 zero values, 4 boundaries: the no-edge case is the existing parallel-shutdown behavior -->
<!-- cases: N/A 5 errors: shutdown errors are reported as today -->
<!-- cases: 7 lifecycle: edges always point to the same scope or an ancestor ("Dynamic dependency declaration"), so the children-first shutdown never contradicts them -->
<!-- cases: N/A 9 panics: unchanged -->
<!-- cases: 10 compatibility: services without declared edges shut down exactly as before -->

### Requirement: Deterministic ordering ties
Wherever this change orders services that have no dependency path between them (discovery, start, kind handling), the container SHALL order them by scope depth (root first), then registration order.

#### Scenario: Ties broken by scope depth then registration order
```gherkin
Given a root scope and child scope "request"
And Starters registered in order: "z" in "request", "b" in root, "a" in root, with no dependencies between them
When the consumer calls Start()
Then the start order is b, a, z
```

<!-- cases: N/A 2 overrides: reorder registrations or declare edges to change ties -->
<!-- cases: N/A 3 zero values, 5 errors, 9 panics: ordering of an empty set is empty; no failure modes -->
<!-- cases: 8 concurrency: the order is identical across 100 repeated runs; covered by a repeat test in design.md -->
<!-- cases: N/A 10 compatibility: existing APIs did not promise an order -->

### Requirement: Explained edges report their source
Each `ExplainServiceDependencyOutput` SHALL carry `Source`: `DependencySourceInvoked` (a provider invoked the service), `DependencySourceDependsOn` (a `DependsOn` declaration) or `DependencySourceDeclared` (`DeclareDependency`). An edge recorded several ways SHALL report the first of invoked, depends-on, declared. `String()` SHALL append ` (depends-on)` or ` (declared)` to non-invoked edges only.

#### Scenario: Source per edge
```gherkin
Given a root scope with lazy "db", lazy "cache", lazy "job"
And lazy "worker" whose provider invokes "db", registered with DependsOnNamed("cache")
And "worker" has been invoked
And DeclareDependency(root, "worker", "job") returned nil
And <extra>
When the consumer calls ExplainNamedService(root, "worker")
Then the dependency sources are <sources>
And its String() output contains <marks>

Examples:
  | extra                                             | sources                                      | marks                                      |
  | nothing else                                      | cache: depends-on, db: invoked, job: declared | "cache (depends-on)", "job (declared)" and "db" without a suffix |
  | DeclareDependency(root, "worker", "db") returned nil | cache: depends-on, db: invoked, job: declared | "db" without a suffix                    |
```

#### Scenario: An edge both depended on and invoked
```gherkin
Given a root scope with lazy "queue"
And lazy "worker" registered with DependsOnNamed("queue"), whose provider invokes "queue"
And "worker" has been invoked
When the consumer calls ExplainNamedService(root, "worker")
Then the dependency on "queue" has Source DependencySourceInvoked
```

#### Scenario: Dependents report the same source
```gherkin
Given the setup of "Source per edge", first row
When the consumer calls ExplainNamedService(root, "job")
Then "job" lists dependent "worker" with Source DependencySourceDeclared
```

#### Scenario: Output without declared edges is unchanged
```gherkin
Given a root scope where lazy "worker"'s provider invokes lazy "db"
And "worker" has been invoked
When the consumer calls ExplainNamedService(root, "worker").String()
Then the output equals the output produced before this change
And the JSON encoding has field "source" with value "invoked" for "db"
```

<!-- cases: 1 default: invoked edges print as before -->
<!-- cases: N/A 2 overrides: the source is observation only -->
<!-- cases: 3 zero values: the zero DependencySource is DependencySourceInvoked, so existing literals keep their meaning -->
<!-- cases: N/A 4 boundaries: one edge per source -->
<!-- cases: N/A 5 errors: no error path -->
<!-- cases: 6 duplicates: an edge recorded as invoked and declared reports invoked -->
<!-- cases: 7 lifecycle: clones re-record DependsOn edges as depends-on and do not copy declared edges (existing requirement) -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent declarations are race-free" -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: 10 compatibility: String() of invoked-only graphs is unchanged; adding the field breaks only unkeyed struct literals -->

### Requirement: Dependency sources round-trip as text
`DependencySource` SHALL encode as and decode from the texts `invoked`, `depends-on` and `declared`, so explained output round-trips through JSON. Any other text SHALL be a decoding error.

#### Scenario: Explained output round-trips through JSON
```gherkin
Given the JSON object {"service": "db", "source": <text>}
When the consumer decodes it into an ExplainServiceDependencyOutput
Then the result is <result>

Examples:
  | text         | result                                                           |
  | "invoked"    | Source DependencySourceInvoked, nil error                        |
  | "depends-on" | Source DependencySourceDependsOn, nil error                      |
  | "declared"   | Source DependencySourceDeclared, nil error                       |
  | "other"      | an error naming "other"                                          |
```

<!-- cases: 1 default: the three texts -->
<!-- cases: N/A 2 overrides: the texts are fixed -->
<!-- cases: N/A 3 zero values: the zero value encodes as "invoked" ("Output without declared edges is unchanged") -->
<!-- cases: N/A 4 boundaries: one value per text -->
<!-- cases: 5 errors: an unknown text -->
<!-- cases: N/A 6 duplicates, 7 lifecycle, 8 concurrency, 9 panics: a pure value conversion -->
<!-- cases: 10 compatibility: additive; JSON encoding is unchanged -->

### Requirement: Explain follows the service's own scope and stops at cycles
`ExplainNamedService` SHALL report the dependencies and dependents recorded for the service in the scope that registers it, whichever scope explains it. A dependency graph closed into a cycle by a build after a `DeclareDependency` edge SHALL be explained in finite output: a service repeated along one branch SHALL be listed once more, without its own dependencies.

#### Scenario: Explaining a graph with a cycle
```gherkin
Given a root scope with lazy "a", whose provider invokes "b", lazy "b", whose provider <b-provider>, and lazy "c"
And DeclareDependency(root, <dependent>, "a") was called before the consumer invoked "a"
When the consumer calls ExplainNamedService(root, "a")
Then the dependencies read <dependencies> and the dependents <dependents>, each written as name(children)

Examples:
  | b-provider      | dependent | dependencies | dependents |
  | returns at once | "b"       | b(a())       | b(a())     |
  | invokes "c"     | "c"       | b(c(a()))    | c(b(a()))  |
```

#### Scenario: Explaining from another scope
```gherkin
Given a root scope with lazy "a", whose provider invokes lazy "b", and a child scope "request"
And the consumer invoked "a"
When the consumer calls ExplainNamedService(request, "a") and ExplainNamedService(request, "b")
Then the dependencies of "a" equal those explained from the root, exactly b()
And the dependents of "b" are exactly a()
```

<!-- cases: 1 default: explaining from the registering scope is unchanged -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: N/A 3 zero values: a service without edges explains as before -->
<!-- cases: 4 boundaries: a two- and a three-service cycle -->
<!-- cases: N/A 5 errors: explaining never fails for a registered service -->
<!-- cases: 6 duplicates: a repeated service is listed once more, without its dependencies -->
<!-- cases: 7 lifecycle: a cycle closed by a build after the declaration -->
<!-- cases: N/A 8 concurrency: none -->
<!-- cases: 9 panics: no stack overflow -->
<!-- cases: 10 compatibility: fixes a crash and empty output; acyclic output from the registering scope is unchanged -->

### Requirement: Edges through an export order shutdown across scopes
An export is the only edge that points from a parent scope into a child scope. When a scope shuts down, every service of that scope with a dependency path to an export SHALL be shut down before the scope's child that owns the exported service. Scopes without exports SHALL shut down exactly as before.

#### Scenario: Parent dependents shut down before the exporting child
```gherkin
Given a root scope with child scope "redis"
And a lazy "*app.Pool" in scope "redis" implementing Shutdown() that records its shutdown and whether it was already closed
And the consumer called doger.Export[*app.Pool](redis)
And <dependents> in the root, each implementing Shutdown() that records whether "*app.Pool" was already closed
And every service was invoked
When the consumer calls Shutdown on the root
Then the shutdown order is <order>
And no dependent saw "*app.Pool" closed

Examples:
  | dependents                                                         | order                                    |
  | a lazy "*app.App" invoking "*app.Pool"                             | "*app.App", "*app.Pool"                  |
  | a lazy "*app.App" invoking "*app.Repo", which invokes "*app.Pool"  | "*app.App", "*app.Repo", "*app.Pool"     |
  | a lazy "*app.App" declared with DependsOnNamed("*app.Pool")        | "*app.App", "*app.Pool"                  |
  | a lazy "*app.App" invoking "*app.Pool", the pool registered with doger.AsDefault() | "*app.App", "*app.Pool"  |
  | a lazy "*app.App" invoking "*app.Pool", the whole root cloned and the clone shut down instead | "*app.App", "*app.Pool" |
```

#### Scenario: Chained exports order shutdown across three scopes
```gherkin
Given a root scope with child scope "a" and grandchild scope "a/b"
And a lazy "*app.Pool" in scope "a/b" recording its shutdown
And the consumer called doger.Export[*app.Pool](b), then doger.Export[*app.Pool](a)
And a lazy "*app.App" in the root invoking "*app.Pool" and recording its shutdown
When the consumer calls Shutdown on the root
Then "*app.App" was shut down before "*app.Pool"
```

#### Scenario: A sibling child that uses a parent dependent shuts down first
```gherkin
Given a root scope with child scopes "redis" and "web"
And a lazy "*app.Pool" in scope "redis", exported with doger.Export[*app.Pool](redis)
And a lazy "*app.App" in the root invoking "*app.Pool"
And a lazy "*app.Handler" in scope "web" invoking "*app.App"
And every service was invoked and records its shutdown
When the consumer calls Shutdown on the root
Then the shutdown order is "*app.Handler", "*app.App", "*app.Pool"
```

#### Scenario: Scopes without exports keep children-first shutdown
```gherkin
Given a root scope with child scope "c"
And a lazy "*app.Root" in the root and a lazy "*app.Child" in scope "c" invoking "*app.Root", both recording shutdown
When the consumer calls Shutdown on the root
Then "*app.Child" was shut down before "*app.Root"
```

<!-- cases: N/A 2 overrides: DeclareDependency adds edges inside the scopes it allows; nothing can reverse an export's order -->
<!-- cases: N/A 3 zero values: no export is the third scenario -->
<!-- cases: 4 boundaries: direct, transitive and declared paths; one and two scope levels -->
<!-- cases: N/A 5 errors: shutdown errors are reported as today -->
<!-- cases: N/A 6 duplicate: one export per published name -->
<!-- cases: 7 lifecycle: the subject; a child scope shut down directly is covered in scope-export -->
<!-- cases: 8 concurrency: the ordering holds with the existing parallel shutdown; the scenarios run under -race -->
<!-- cases: N/A 9 panics: unchanged -->
<!-- cases: 10 compatibility: the third scenario pins the unchanged order -->

### Requirement: Shutdown follows dependency paths across exporting scopes
When a scope holding exports shuts down, services of the scope and of its exporting descendants SHALL shut down in dependency order, whatever scope each belongs to: a service of an exporting child, or of an exporting sibling, that depends directly or transitively on a service of the scope SHALL shut down before it.

#### Scenario: An exporting child's service that depends on a parent dependent shuts down first
```gherkin
Given a root scope with child scope "redis"
And a lazy "*app.Pool" in scope "redis" recording its shutdown, exported with doger.Export[*app.Pool](redis)
And <services>, each recording its shutdown
And "x" was invoked from scope "redis"
When the consumer calls Shutdown on the root
Then the shutdown order is <order>

Examples:
  | services                                                                                                   | order                    |
  | a lazy "p" in the root invoking "*app.Pool", and a lazy "x" in scope "redis" invoking "p"                   | "x", "p", "*app.Pool"    |
  | a lazy "p1" in the root invoking "*app.Pool", a lazy "p2" in the root invoking "p1", and a lazy "x" in scope "redis" invoking "p2" | "x", "p2", "p1", "*app.Pool" |
```

#### Scenario: Exporting siblings order shutdown through their exports
```gherkin
Given a root scope with child scopes "a" and "b"
And a lazy "*app.Pool" in scope "b" recording its shutdown, exported with doger.Export[*app.Pool](b)
And a lazy "*app.Client" in scope "a", exported with doger.Export[*app.Client](a)
And a lazy "x" in scope "a" invoking "*app.Pool" and recording its shutdown, invoked from scope "a"
When the consumer calls Shutdown on the root, in 200 new containers built the same way
Then every time "x" was shut down before "*app.Pool"
```

#### Scenario: A dependency cycle through an export still shuts every service down
```gherkin
Given a root scope with child scope "redis"
And a lazy "*app.Pool" in scope "redis" recording its shutdown, whose provider keeps the injector it receives, exported with doger.Export[*app.Pool](redis)
And "*app.Pool" was invoked from the root, then a lazy "app" in the root invoking "*app.Pool" and recording its shutdown was invoked from the root
And the consumer then invoked "app" through the injector the provider of "*app.Pool" kept, recording the edge "*app.Pool" -> "app" (doger.DeclareDependency refuses that edge, since it closes a cycle)
When the consumer calls Shutdown on the root
Then the report succeeds
And "app" and "*app.Pool" were each shut down once
```

#### Scenario: The shutdown report covers the exporting descendants
```gherkin
Given a root scope with child scope "redis"
And a lazy "*app.Pool" in scope "redis", exported with doger.Export[*app.Pool](redis) and invoked from the root
When the consumer calls Shutdown on the root
Then the report's Services contain "*app.Pool" in scope "[root]" and "*app.Pool" in scope "redis"
```

#### Scenario: An exporting child waits for a running start
```gherkin
Given a root scope, inside synctest.Test, with child "tenant" and grandchild "jobs"
And a lazy "w" in "jobs" whose Start blocks until released and that records its Shutdown calls, exported into "tenant" with doger.ExportNamed[*app.Worker](jobs, "w")
And the root was started, and jobs.Start() is running in another goroutine, blocked in that Start
When the consumer calls tenant.ShutdownWithContext(ctx), ctx with a 1s timeout
Then the report fails with an error keyed by scope "jobs" with an empty service name, matching context.DeadlineExceeded
And, as for a child scope without exports, it fails for "w" in scope "jobs" with an error matching context.DeadlineExceeded, and the Shutdown of "w" was not called
```

<!-- cases: N/A 2 overrides: DeclareDependency and DependsOn add edges the order follows; nothing reverses it -->
<!-- cases: N/A 3 zero values: scopes without exports are covered by "Scopes without exports keep children-first shutdown" -->
<!-- cases: 4 boundaries: a child service using a parent dependent (direct), a transitive chain through the parent, exporting siblings, a dependency cycle through an export (parallel fallback) -->
<!-- cases: 5 errors: an exporting child that waits for a running start past the context reports it under its own scope; service shutdown errors are reported as today, and the report covers the exporting descendants -->
<!-- cases: N/A 6 duplicate: each service shuts down once (covered by "An export never shuts its target down") -->
<!-- cases: 7 lifecycle: the subject; hook order around it is in scope-lifecycle-hooks -->
<!-- cases: 8 concurrency: the order holds with the parallel shutdown, in repeated runs (exporting siblings, 200 containers) under -race -->
<!-- cases: 9 panics: a panic in the pass releases the joined exporting descendants (scenario in scope-lifecycle-hooks) -->
<!-- cases: 10 compatibility: scopes without exports keep the old order -->
