# Spec Delta

## ADDED Requirements

### Requirement: The container measures health reports and scheduling on its clock
With `InjectorOpts.Clock` set, the container SHALL pass that clock to `HealthScheduler.ScheduleHealthRounds`. It SHALL also read from it the `HealthReportCacheTTL`, the report-path check bound, and the `CheckedAt`, `Duration` and `PendingSince` of health report entries.

#### Scenario: Report times come from the clock
```gherkin
Given an injector that is never started, whose Clock is a fake clock set to 2026-01-01T00:00:00Z
And an invoked lazy service "svc" whose health check advances that fake clock by 4s and returns nil
When the consumer reads HealthReport() on the root scope
Then the entry for "svc" has Duration 4s and CheckedAt 2026-01-01T00:00:04Z
```

#### Scenario: Health caching waits for the clock, not for wall time
```gherkin
Given an injector that is never started, whose Clock is a fake clock and HealthReportCacheTTL is 10s
And a Healthchecker "svc" that counts its calls
And the consumer read HealthReport() once
When 50ms of wall time pass without advancing the fake clock, and the consumer reads HealthReport() again
Then the health check of "svc" was called 1 time in total
And after the fake clock advances by 10s and the consumer reads HealthReport() again, it was called 2 times in total
```

#### Scenario: The scheduler receives the injector clock
```gherkin
Given an injector whose Clock is <clock> and whose HealthScheduler is a manual HealthScheduler
When the consumer calls Start, which succeeds
Then ScheduleHealthRounds received <received>

Examples:
  | clock                  | received                 |
  | a fake clock C         | C                        |
  | nil (default)          | a real clockwork clock   |
```

<!-- cases: 1 default: the real clock row; the real-clock default is the existing "The injector clock defaults to the real clock" requirement -->
<!-- cases: 2 overrides: a fake clock drives the TTL, the report bound, the report times and the scheduler -->
<!-- cases: 3 zero values: a nil Clock; a zero Duration is in health-reports -->
<!-- cases: 4 boundaries: the TTL and bound boundaries are in health-reports -->
<!-- cases: N/A 5 errors: measurement only -->
<!-- cases: N/A 6 not found/duplicate: no lookup involved -->
<!-- cases: 7 lifecycle: never-started and started containers -->
<!-- cases: 8 concurrency: the wall-time scenario shows caching and bounds are independent of goroutine timing -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: every time the container measured before is unchanged -->
