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
