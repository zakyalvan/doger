# Spec Delta

## ADDED Requirements

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
