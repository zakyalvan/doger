# Spec Delta

## ADDED Requirements

### Requirement: Per-service health checks honor HealthCheckTimeout
`HealthCheck[T]`, `HealthCheckWithContext[T]`, `HealthCheckNamed` and `HealthCheckNamedWithContext` SHALL bound the check by `HealthCheckTimeout`, measured on the injector's clock, when it is greater than zero. `HealthCheckGlobalTimeout` SHALL NOT apply to them. With `HealthCheckTimeout` at zero they SHALL behave as before.

#### Scenario: Per-service checks are bounded by HealthCheckTimeout
```gherkin
Given an injector with a fake clock and HealthCheckTimeout <timeout> and HealthCheckGlobalTimeout <global>
And a service "svc" whose health check returns nil after <duration> on that clock
When the consumer calls <function> for "svc" with context.Background()
And the fake clock advances by <duration>
Then the result is <expected>

Examples:
  | function                    | timeout | global | duration | expected                                              |
  | HealthCheckNamed            | 10ms    | 0      | 20ms     | "DI: health check timeout: context deadline exceeded" |
  | HealthCheckNamedWithContext | 10ms    | 0      | 20ms     | "DI: health check timeout: context deadline exceeded" |
  | HealthCheck[*Svc]           | 10ms    | 0      | 9ms      | nil                                                   |
  | HealthCheckWithContext[*Svc]| 10ms    | 0      | 10ms     | "DI: health check timeout: context deadline exceeded" |
  | HealthCheckNamed            | 0       | 10ms   | 20ms     | nil                                                   |
  | HealthCheckNamed            | 0       | 0      | 1h       | nil                                                   |
```

#### Scenario: A per-service check returning an error in time keeps it
```gherkin
Given an injector with HealthCheckTimeout 10ms
And a service "svc" whose health check returns errors.New("db down") immediately
When the consumer calls HealthCheckNamed for "svc"
Then the result is the same "db down" error
And errors.Is(result, ErrHealthCheckTimeout) is false
```

<!-- cases: 1 default: HealthCheckTimeout 0 keeps the unbounded per-service behavior (last row) -->
<!-- cases: 2 overrides: HealthCheckTimeout bounds the check; HealthCheckGlobalTimeout alone does not (row 5) -->
<!-- cases: 3 zero values: both options 0 -->
<!-- cases: 4 boundaries: 9ms before, 10ms exactly at, 20ms past the timeout -->
<!-- cases: 5 errors: timeout message per the first requirement; a service error in time is kept -->
<!-- cases: N/A 6 not found: an unknown name keeps returning ErrServiceNotFound, unchanged -->
<!-- cases: N/A 7 lifecycle: per-service checks have no scope traversal beyond name resolution, unchanged -->
<!-- cases: 8 concurrency: fake-clock rows run in synctest; timed-out checks finish when the clock advances, so no goroutine leaks -->
<!-- cases: N/A 9 panics: unchanged and out of scope -->
<!-- cases: 10 compatibility: BEHAVIOR CHANGE (flagged) - a consumer who set HealthCheckTimeout and called per-service checks now gets a bound; with the option at 0 nothing changes -->
