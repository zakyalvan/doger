# Spec Delta

## MODIFIED Requirements

### Requirement: The container measures time only on its clock
With `InjectorOpts.Clock` set, the container SHALL read every time it measures from that clock: the `HealthCheckTimeout` and `HealthCheckGlobalTimeout` deadlines, a lazy service's build time (`ServiceBuildTime` in `ExplainService` and `ExplainNamedService` output), `ShutdownReport.ShutdownTime` and `ShutdownReport.ServiceShutdownTime`, the `HealthCheckInterval` between scheduled rounds, the scheduled-check timeout, the `HealthReportCacheTTL`, and the `CheckedAt`, `Duration` and `PendingSince` of health report entries.

#### Scenario: Reported durations come from the clock
```gherkin
Given an injector whose Clock is a fake clock
And a lazy service "svc" whose <phase> advances that fake clock by <advance> and returns no error
When the consumer <action>
Then <measurement> is exactly <advance>

Examples:
  | phase    | advance | action                                    | measurement                                     |
  | provider | 3s      | invokes "svc" and calls ExplainNamedService | ServiceBuildTime of "svc"                     |
  | provider | 0s      | invokes "svc" and calls ExplainNamedService | ServiceBuildTime of "svc" (omitted when zero) |
  | Shutdown | 2s      | invokes "svc" and calls Shutdown          | ShutdownReport.ServiceShutdownTime for "svc"   |
  | Shutdown | 2s      | invokes "svc" and calls Shutdown          | ShutdownReport.ShutdownTime                     |
  | health check | 4s  | invokes "svc", starts the container and reads HealthReport() after the first round | Duration of the report entry for "svc" |
```

#### Scenario: Health scheduling and caching wait for the clock, not for wall time
```gherkin
Given an injector whose Clock is a fake clock, HealthCheckInterval is 10s and HealthReportCacheTTL is 10s
And a Healthchecker "svc" that counts its calls
And the container is <state>
When 50ms of wall time pass without advancing the fake clock, and the consumer then reads HealthReport()
Then the health check of "svc" was called 1 time in total
And after the fake clock advances by 10s and the consumer reads HealthReport() again, it was called 2 times in total

Examples:
  | state                                                  |
  | started, after its first round                         |
  | never started, after one HealthReport() read           |
```

#### Scenario: Timeout errors read the same on any clock
```gherkin
Given an injector whose Clock is <clock> and HealthCheckTimeout is 10ms
And a service "svc" whose health check blocks until released
When the 10ms timeout elapses on that clock
Then the result for "svc" has the message "DI: health check timeout: context deadline exceeded"
And it matches ErrHealthCheckTimeout and context.DeadlineExceeded with errors.Is

Examples:
  | clock                                      |
  | nil (real clock, inside synctest.Test)     |
  | clockwork.NewFakeClock()                   |
```

#### Scenario: Health-check timeouts wait for the clock, not for wall time
```gherkin
Given an injector whose Clock is a fake clock and <option> is 10ms
And a service "svc" whose health check blocks until released
When the consumer starts HealthCheckWithContext on the injector
And 50ms of wall time pass without advancing the fake clock
Then the call has not returned
And after the fake clock advances by 10ms the result for "svc" is "DI: health check timeout: context deadline exceeded"

Examples:
  | option                   |
  | HealthCheckTimeout       |
  | HealthCheckGlobalTimeout |
```

<!-- cases: 1 default: previous requirement -->
<!-- cases: 2 overrides: every row sets a fake clock; each time read the container makes is a row, including the health scheduling and report cache -->
<!-- cases: 3 zero values: a 0s advance yields a zero build time, which explain output omits as today -->
<!-- cases: 4 boundaries: the exact-limit and one-before boundaries are in health-check-timeouts -->
<!-- cases: N/A 5 errors: measurement only; timeout errors are specified in health-check-timeouts -->
<!-- cases: N/A 6 not found/duplicate: no lookup involved -->
<!-- cases: 7 lifecycle: build (first invocation) and shutdown are the measured phases -->
<!-- cases: 8 concurrency: the wall-time scenario shows the deadline is independent of goroutine timing; the blocked check is released before the test ends, so no goroutine leaks -->
<!-- cases: N/A 9 panics: unchanged -->
<!-- cases: 10 backward compatibility: field names, JSON keys and the omission of a zero build time are unchanged -->

