# Spec Delta

## ADDED Requirements

### Requirement: A default that yields is not a start candidate
`Start`, `Refresh` and `Scope.Start` SHALL NOT start, or pass to a kind handler, a default that yields to a visible non-default service of the same name, even if its registered type is a Starter or it was given `AsStarter()` or a kind option. A default that does not yield SHALL be a candidate as any registration.

#### Scenario: Start candidates with defaults
```gherkin
Given a root scope with child scope "ext"
And a lazy "*app.Server" registered in scope "ext" with doger.AsDefault(), implementing Start(ctx) error and counting calls
And <explicit>
And <before>
When the consumer calls <operation> on the root
Then the default server's Start was called <default_starts> times
And the root server's Start was called <root_starts> times

Examples:
  | explicit                                                       | before                          | operation | default_starts | root_starts |
  | an explicit "*app.Server" in the root, counting Start calls    | nothing else happened           | Start     | 0              | 1           |
  | nothing                                                        | nothing else happened           | Start     | 1              | 0           |
  | an explicit "*app.Server" in the root, counting Start calls    | the root was started            | Refresh   | 0              | 1           |
```

#### Scenario: A default yields even when it was never invoked before Start
```gherkin
Given a root scope with child scope "ext"
And a lazy "*app.Server" registered in scope "ext" with doger.AsDefault() and doger.AsRunnable(), never invoked
And an explicit lazy "*app.Server" registered in the root with doger.AsRunnable(), implementing Run(ctx) error and counting calls
When the consumer calls Start on the root
Then the root server's Run was called 1 time
And the default's provider was called 0 times
```

#### Scenario: Scope.Start skips a default that yields
```gherkin
Given a started root scope
And a child scope "ext" created after Start, with a lazy "*app.Server" registered with doger.AsDefault(), implementing Start(ctx) error
And an explicit lazy "*app.Server" in the root that was started by the root Start
When the consumer calls Start on scope "ext"
Then the root server's Start was called 1 time in total
And the default server's provider was called 0 times
```

<!-- cases: N/A 2 overrides: DisableStarterAutoDetection applies to built defaults as to any service -->
<!-- cases: N/A 3 zero values, 4 boundaries, 5 invalid input: no input beyond registrations -->
<!-- cases: N/A 6 duplicate: same-scope cases are in default-registrations -->
<!-- cases: 7 lifecycle: Start, Refresh and Scope.Start are covered -->
<!-- cases: N/A 8 concurrency: Start discovery runs under the existing locks; concurrent first resolution is in default-registrations -->
<!-- cases: N/A 9 panics: a skipped default runs no code -->
<!-- cases: 10 compatibility: candidates without AsDefault are unchanged -->
