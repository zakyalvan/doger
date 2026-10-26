# Spec Delta

## ADDED Requirements

### Requirement: The container measures health scheduling and reports on its clock
With `InjectorOpts.Clock` set, the container SHALL read from that clock the `HealthCheckInterval` between scheduled rounds, the scheduled-check timeout, the `HealthReportCacheTTL`, and the `CheckedAt`, `Duration` and `PendingSince` of health report entries.

#### Scenario: Report times come from the clock
```gherkin
Given an injector whose Clock is a fake clock set to 2026-01-01T00:00:00Z
And an invoked lazy service "svc" whose health check advances that fake clock by 4s and returns nil
When the consumer starts the container and reads HealthReport() after the first round finishes
Then the entry for "svc" has Duration 4s and CheckedAt 2026-01-01T00:00:04Z
```

#### Scenario: Health scheduling and caching wait for the clock, not for wall time
```gherkin
Given an injector whose Clock is a fake clock, HealthCheckInterval is 10s and HealthReportCacheTTL is 10s
And a Healthchecker "svc" that counts its calls
And the container is <state>
When 50ms of wall time pass without advancing the fake clock, and the consumer then reads HealthReport()
Then the health check of "svc" was called 1 time in total
And after the fake clock advances by 10s, <wait>, and the consumer reads HealthReport() again, it was called 2 times in total

Examples:
  | state                                        | wait                     |
  | started, after its first round               | the round finishes       |
  | never started, after one HealthReport() read | nothing more is awaited  |
```

<!-- cases: N/A 1 default: the real clock default is the existing "The injector clock defaults to the real clock" requirement -->
<!-- cases: 2 overrides: a fake clock drives the interval, the TTL and the report times -->
<!-- cases: 3 zero values: a zero Duration is in health-reports -->
<!-- cases: 4 boundaries: the interval and TTL boundaries are in health-check-scheduling and health-reports -->
<!-- cases: N/A 5 errors: measurement only -->
<!-- cases: N/A 6 not found/duplicate: no lookup involved -->
<!-- cases: 7 lifecycle: started and never-started containers -->
<!-- cases: 8 concurrency: the wall-time scenario shows rounds and the cache are independent of goroutine timing -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: every time the container measured before is unchanged -->
