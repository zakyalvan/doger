# Spec Delta

<!-- service-aliases is introduced by consolidate-pending-lifecycle-work, which must be archived before this change. -->

## ADDED Requirements

### Requirement: An export never shuts its target down
Shutting down an export, through a scope shutdown or `ShutdownNamed` on the published name, SHALL remove the export without calling the exported service's `Shutdown` method. The exported service SHALL be shut down only through the scope that owns it, exactly once.

#### Scenario: Exported target is shut down once
```gherkin
Given a root scope with child scope "redis"
And a lazy "*app.Pool" in scope "redis" implementing Shutdown() error that counts its calls, invoked once
And the consumer called doger.Export[*app.Pool](redis, doger.ExportAs("pool"))
When the consumer calls <operation>
Then the pool's Shutdown was called <calls> times

Examples:
  | operation                         | calls |
  | root.Shutdown()                   | 1     |
  | doger.ShutdownNamed(root, "pool") | 0     |
  | redis.Shutdown()                  | 1     |
```

<!-- cases: N/A 2 overrides: shutting a target through its export is never correct -->
<!-- cases: N/A 3 zero values, 4 boundaries, 5 invalid input: no input -->
<!-- cases: 6 duplicate: root shutdown row pins exactly one call -->
<!-- cases: 7 lifecycle: root, published name and owning scope rows -->
<!-- cases: N/A 8 concurrency: covered by the existing repeated-runs scenario pattern; the test runs under -race -->
<!-- cases: N/A 9 panics: no new panic path -->
<!-- cases: 10 compatibility: aliases created with As/Bind are unchanged -->
