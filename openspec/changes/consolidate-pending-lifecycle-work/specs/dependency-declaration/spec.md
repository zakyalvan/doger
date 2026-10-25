# Spec Delta

## ADDED Requirements

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
