# Spec Delta

## Purpose

Defines the health report a consumer reads instead of running checks: what it contains, which services it covers for the container and for a scope, its per-service and overall states, and when a read may run checks on demand, so that serving health to many readers costs no more than serving one.

## ADDED Requirements

### Requirement: A report lists each health-checked service with its last result
`HealthReport(opts...)` and `HealthReportWithContext(ctx, opts...)` SHALL return a `HealthReport` whose `Services` holds one `ServiceHealth` per covered health checker: its `ServiceDescription`, `Tags`, `Optional`, `State`, `Err`, `CheckedAt` and `Duration` of its last recorded result, and `PendingSince` while a check is in flight. The report's `CheckedAt` SHALL be the latest entry `CheckedAt`.

#### Scenario: Report entries after one round
```gherkin
Given an injector whose Clock is a fake clock set to 2026-01-01T00:00:00Z
And a service "<name>" registered in <scope> whose health check advances the fake clock by <takes> and returns <result>
And the container was started and its first round finished
When the consumer calls HealthReport() on the root scope
Then the entry for "<name>" has State <state>, Err <err>, CheckedAt 2026-01-01T00:00:00Z plus <takes>, Duration <takes> and a zero PendingSince
And the report's CheckedAt is 2026-01-01T00:00:00Z plus <takes>

Examples:
  | name  | scope               | takes | result             | state           | err                   |
  | db    | the root scope      | 0s    | nil                | HealthHealthy   | nil                   |
  | db    | the root scope      | 2s    | errors.New("down") | HealthUnhealthy | the same "down" error |
  | cache | child scope "child" | 1s    | nil                | HealthHealthy   | nil                   |
```

#### Scenario: Each report is the caller's own copy
```gherkin
Given an injector that is never started, with a Healthchecker value "db" returning nil
And the consumer read a report and set its Services[0].State to HealthUnhealthy
When the consumer calls HealthReport() again
Then the entry for "db" has State HealthHealthy
```

<!-- cases: 1 default: zero-config container after its first round -->
<!-- cases: N/A 2 overrides: the report shape has no option; filters are in health-classification -->
<!-- cases: 3 zero values: a zero Duration for a check that takes no clock time; a zero PendingSince when idle -->
<!-- cases: 4 boundaries: services across scopes -->
<!-- cases: 5 errors: Err holds the check's own error unchanged -->
<!-- cases: N/A 6 not found/duplicate: duplicates are the visibility requirement -->
<!-- cases: N/A 7 lifecycle: covered by the state requirements below -->
<!-- cases: 8 concurrency: each caller gets an isolated copy -->
<!-- cases: N/A 9 panics: a panicking check's error is specified in health-check-execution -->
<!-- cases: N/A 10 backward compatibility: new API -->

### Requirement: Report entries are ordered by scope path, then name
`Services` SHALL be ordered by the scope's path of names from the root (the root first, then shallower before deeper, then by name), and then by service name.

#### Scenario: Entries are ordered by scope path then name
```gherkin
Given an injector that is never started, with Healthchecker values "b" and "a" in the root scope, "z" in child scope "x", "c" in child scope "w", and "q" in scope "leaf" under "x" and "p" in scope "leaf" under "w"
When the consumer calls HealthReport() on the root scope
Then the services are listed as root/"a", root/"b", "w"/"c", "x"/"z", "w/leaf"/"p", "x/leaf"/"q"
```

<!-- cases: N/A 1 default: ordering has no option -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: N/A 3 zero values: an empty report has no order -->
<!-- cases: 4 boundaries: two scopes with the same name at the same depth -->
<!-- cases: N/A 5 errors: none -->
<!-- cases: 6 not found/duplicate: duplicate scope names under different parents -->
<!-- cases: N/A 7 lifecycle: not applicable -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new API -->

### Requirement: Only health checkers appear in a report
A report SHALL include a service only when it is a health checker at read time. It SHALL leave out values without a health check, transient services, and lazy services not yet built. An alias of a covered health checker SHALL appear with its target's result.

#### Scenario: Report membership
```gherkin
Given an injector that is never started, with <registration> in the root scope
When the consumer calls HealthReport() on the root scope
Then the report lists <listed>

Examples:
  | registration                                                      | listed                                      |
  | a plain value "answer" = 42                                       | no service                                  |
  | a lazy Healthchecker "db" that was never invoked                  | no service                                  |
  | a lazy Healthchecker "db" that was invoked                        | "db"                                        |
  | a transient Healthchecker "tmp" that was invoked                  | no service                                  |
  | a Healthchecker value "db" and an alias "hc" to it (AsNamed)      | "db" and "hc", both with the result of "db" |
  | an alias "hc" to a lazy Healthchecker "db" that was never invoked | no service                                  |
```

<!-- cases: N/A 1 default: membership has no option -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: 3 zero values: a container with no checker yields an empty Services slice -->
<!-- cases: N/A 4 boundaries: covered by the ordering requirement -->
<!-- cases: N/A 5 errors: no error path -->
<!-- cases: 6 not found/duplicate: an alias does not duplicate the check, only the entry -->
<!-- cases: 7 lifecycle: a lazy service before and after build -->
<!-- cases: N/A 8 concurrency: a lazy service being built is in health-check-execution -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: HealthCheck() still returns nil entries for non-checkers (health-check-execution) -->

### Requirement: The root report covers the container, a scope report covers what the scope sees
`RootScope.HealthReport()` SHALL cover every scope of the container. `Scope.HealthReport()` SHALL cover the services visible from that scope: its own and its ancestors', leaving out an ancestor's service shadowed by a service of the same name in a nearer scope.

#### Scenario: Report coverage by scope
```gherkin
Given an injector that is never started, with Healthchecker values root/"db" (healthy), root/"cache" (healthy)
And child scope "child" with Healthchecker value "db" (returns errors.New("down"))
And grandchild scope "leaf" of "child" with Healthchecker value "queue" (healthy)
When the consumer calls HealthReport() on <target>
Then the report lists <listed>

Examples:
  | target                  | listed                                                |
  | the RootScope           | root/"cache", root/"db", "child"/"db", "leaf"/"queue" |
  | child scope "child"     | root/"cache", "child"/"db"                            |
  | grandchild scope "leaf" | root/"cache", "child"/"db", "leaf"/"queue"            |
```

<!-- cases: N/A 1 default: coverage has no option -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: 3 zero values: a scope with no checker and no ancestors' checkers yields an empty Services slice -->
<!-- cases: 4 boundaries: root, one and two levels deep -->
<!-- cases: N/A 5 errors: none -->
<!-- cases: 6 not found/duplicate: shadowing of "db" -->
<!-- cases: 7 lifecycle: nested scopes -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new API -->

### Requirement: The overall state summarizes the covered services
A report's `State` SHALL be `HealthUnhealthy` when any covered service is unhealthy; else `HealthDegraded` when any is degraded; else `HealthUnknown` when any covered critical service has no result yet; else `HealthHealthy`, including when no service is covered. An optional service without a result SHALL NOT make the state unknown.

#### Scenario: Overall state
```gherkin
Given a report covering services in states <states>
When the overall state is computed
Then it is <overall>

Examples:
  | states                                 | overall         |
  | Healthy, Healthy                       | HealthHealthy   |
  | Healthy, Unhealthy                     | HealthUnhealthy |
  | Unknown, Unhealthy                     | HealthUnhealthy |
  | Unknown (critical), Healthy            | HealthUnknown   |
  | Unknown (optional), Healthy            | HealthHealthy   |
  | Unknown, Unknown                       | HealthUnknown   |
  | Healthy, Degraded                      | HealthDegraded  |
  | Unknown (critical), Degraded           | HealthDegraded  |
  | Degraded, Unhealthy                    | HealthUnhealthy |
  | none                                   | HealthHealthy   |
```

#### Scenario: HealthState names
```gherkin
Given the HealthState value <value>
When the consumer calls String()
Then it returns "<name>"

Examples:
  | value           | name           |
  | HealthUnknown   | unknown        |
  | HealthHealthy   | healthy        |
  | HealthDegraded  | degraded       |
  | HealthUnhealthy | unhealthy      |
  | HealthState(9)  | HealthState(9) |
```

<!-- cases: 1 default: HealthUnknown is the zero value -->
<!-- cases: N/A 2 overrides: the rule has no option; consumers can derive their own from Services -->
<!-- cases: 3 zero values: no covered service -->
<!-- cases: 4 boundaries: every state against the others; critical vs optional Unknown; an unknown enum value -->
<!-- cases: N/A 5 errors: none -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: N/A 7 lifecycle: the closed container is the next requirement -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new API -->

### Requirement: A closed container reports no service and an unknown state
After root `Shutdown` completed, a report SHALL have `Monitored` false, `State` `HealthUnknown` and no service, and a read SHALL NOT run any check.

#### Scenario: A closed container reports no service
```gherkin
Given a started injector with a Healthchecker "db" that counts its calls
When the container is shut down and the consumer calls HealthReport() on the root scope
Then the report has Monitored false, State HealthUnknown and an empty Services slice
And the health check of "db" was not called by that read
```

<!-- cases: N/A 1 default: no option -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: 3 zero values: an empty Services slice -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: none -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: after shutdown -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new API -->

### Requirement: A monitored container's report never runs a check
While the scheduler runs, a report read SHALL return the recorded results without running any check, with `Monitored` true. A covered service with no result yet SHALL be `HealthUnknown`, with `PendingSince` set while its first check is in flight.

#### Scenario: Reads on a monitored container cost nothing
```gherkin
Given a started injector with HealthCheckInterval 0 and a fake clock
And a Healthchecker "db" that counts its calls
And the first round finished
When 1000 goroutines call HealthReport() on the root scope concurrently
Then every report has Monitored true and "db" in HealthHealthy
And the health check of "db" was called 1 time in total
```

#### Scenario: Before the first result
```gherkin
Given a started injector with HealthCheckInterval 0 and a fake clock set to 2026-01-01T00:00:00Z
And a Healthchecker "db" whose first check blocks until released
When the consumer calls HealthReport() on the root scope
Then the report has Monitored true and State HealthUnknown
And "db" is HealthUnknown with PendingSince 2026-01-01T00:00:00Z
```

<!-- cases: 1 default: the default interval -->
<!-- cases: N/A 2 overrides: the interval is in health-check-scheduling -->
<!-- cases: 3 zero values: no result yet is HealthUnknown -->
<!-- cases: 4 boundaries: 1 and 1000 readers -->
<!-- cases: N/A 5 errors: none -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: Ready before and after the first round -->
<!-- cases: 8 concurrency: 1000 concurrent readers -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new API -->

### Requirement: An unmonitored container's report runs stale checks on demand
When no scheduler runs and the container is in `PhaseIdle`, `PhaseFailed` or `PhaseReady`, a read SHALL check each covered service whose last result is missing or at least `HealthReportCacheTTL` old (0 means 1s; negative means always), and return the fresh results with `Monitored` false. Concurrent reads SHALL share each in-flight check.

#### Scenario: On-demand reads honor the cache TTL
```gherkin
Given an injector that is never started, with a fake clock, HealthReportCacheTTL <ttl>, and a Healthchecker "db" that counts its calls
And the consumer read HealthReport() once
When the fake clock advances by <advance> and the consumer reads HealthReport() again
Then the health check of "db" was called <calls> times in total
And both reports have Monitored false

Examples:
  | ttl         | advance | calls |
  | 0 (default) | 999ms   | 1     |
  | 0 (default) | 1s      | 2     |
  | 5s          | 4.999s  | 1     |
  | 5s          | 5s      | 2     |
  | -1ns        | 0s      | 2     |
```

#### Scenario: Concurrent on-demand reads share one check
```gherkin
Given an injector that is never started, with a Healthchecker "db" that counts its calls and blocks until released
When 50 goroutines call HealthReport() on the root scope, and the check is released once all 50 wait
Then the health check of "db" was called 1 time
And all 50 reports have "db" in HealthHealthy
```

#### Scenario: On-demand reads by phase
```gherkin
Given an injector with a Healthchecker "db" that counts its calls and HealthCheckInterval <interval>
And the container is in <phase>
When the consumer calls HealthReport() on the root scope
Then the health check of "db" was called <calls> times by that read
And the report has Monitored <monitored>

Examples:
  | phase                                     | interval    | calls | monitored |
  | PhaseIdle                                 | 0 (default) | 1     | false     |
  | PhaseIdle                                 | -1ns        | 1     | false     |
  | PhaseFailed (a Starter returned an error) | 0 (default) | 1     | false     |
  | PhaseReady, after the first round         | 0 (default) | 0     | true      |
  | PhaseReady                                | -1ns        | 1     | false     |
  | PhaseStarting (blocked in a Starter)      | 0 (default) | 0     | false     |
  | PhaseStarted (blocked in a Started hook)  | 0 (default) | 0     | false     |
  | PhaseClosing (blocked in a Closing hook)  | 0 (default) | 0     | false     |
```

#### Scenario: Reads without on-demand checks return the last recorded results
```gherkin
Given a started injector with a Healthchecker "db" that returned errors.New("down") in its last round
And a Closing hook that blocks until released
When Shutdown is called and, while the Closing hook blocks, the consumer calls HealthReport() on the root scope
Then the report has Monitored false
And "db" is HealthUnhealthy with the same "down" error
```

<!-- cases: 1 default: TTL 1s on an idle container -->
<!-- cases: 2 overrides: positive and negative TTL; scheduling off on Idle and Ready -->
<!-- cases: 3 zero values: TTL 0 means 1s; a nil context means context.Background() (next requirement) -->
<!-- cases: 4 boundaries: one millisecond before and exactly at the TTL (exactly at the TTL is stale) -->
<!-- cases: N/A 5 errors: a read has no error return; check errors are in the entries -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: every phase is a row; results while closing -->
<!-- cases: 8 concurrency: 50 readers share one check -->
<!-- cases: N/A 9 panics: specified in health-check-execution -->
<!-- cases: N/A 10 backward compatibility: new API -->

### Requirement: An on-demand read is bounded by its caller's context and the per-service timeout
`HealthReportWithContext(ctx)` SHALL stop waiting when ctx is done or when `HealthCheckTimeout` (if positive) elapses for a service, and SHALL report each unfinished check as `HealthUnhealthy` with an error matching `ErrHealthCheckTimeout` and the cause, in that read's report only. `HealthCheckGlobalTimeout` SHALL NOT apply. `HealthReport()` and a nil ctx SHALL use `context.Background()`.

#### Scenario: A reader that stops waiting
```gherkin
Given an injector that is never started, with HealthCheckTimeout <timeout>
And a service "db" whose HealthCheck(ctx) blocks until released or ctx is done
When the consumer calls HealthReportWithContext with a context that is <context>
Then the read returns <returns>
And the entry for "db" is <entry>

Examples:
  | timeout | context                       | returns                    | entry                                                                  |
  | 0       | cancelled 10ms after the call | 10ms after the call        | HealthUnhealthy, "DI: health check timeout: context canceled"          |
  | 0       | cancelled before the call     | at once                    | HealthUnhealthy, "DI: health check timeout: context canceled"          |
  | 0       | nil                           | once the check is released | HealthHealthy                                                          |
  | 20ms    | never cancelled               | 20ms after the call        | HealthUnhealthy, "DI: health check timeout: context deadline exceeded" |
```

#### Scenario: A reader leaving does not affect other readers
```gherkin
Given an injector that is never started, with a service "db" whose HealthCheck(ctx) blocks until released or ctx is done
And reader A calls HealthReportWithContext with a context cancelled after 10ms
And reader B calls HealthReport() at the same time
When A's context is cancelled, then the check is released
Then A's entry for "db" is HealthUnhealthy with an error matching ErrHealthCheckTimeout
And B's entry for "db" is HealthHealthy
And a third read within the TTL returns HealthHealthy for "db" without a new check
```

<!-- cases: 1 default: no default bound, per user decision; HealthReport() waits -->
<!-- cases: 2 overrides: HealthCheckTimeout bounds the wait -->
<!-- cases: 3 zero values: nil context -->
<!-- cases: 4 boundaries: cancelled before vs during the read -->
<!-- cases: 5 errors: the timeout error matches ErrHealthCheckTimeout and the cause with errors.Is -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: N/A 7 lifecycle: on-demand phases are in the previous requirement -->
<!-- cases: 8 concurrency: one reader leaving while another waits -->
<!-- cases: N/A 9 panics: specified in health-check-execution -->
<!-- cases: 10 backward compatibility: the timeout message matches health-check-timeouts -->

### Requirement: Every completed check refreshes the report
The result of every check SHALL be recorded for reports, whichever path ran it: a scheduled round, an on-demand read, `HealthCheck()` / `HealthCheckWithContext()`, or a single-service function. A check whose own bound elapsed SHALL be recorded as timed out at that moment, and its later result SHALL be recorded when it returns.

#### Scenario: Forced and single-service checks refresh the report
```gherkin
Given an injector that is never started, with a fake clock, HealthReportCacheTTL 1h and a Healthchecker value of type *DB, registered with ProvideValue
And a first HealthReport() read recorded nil for it
And it now returns errors.New("down")
When the consumer calls <entry>, then HealthReport() on the root scope
Then the entry for name(*DB) is HealthUnhealthy with the same "down" error

Examples:
  | entry                                         |
  | HealthCheck() on the root scope               |
  | HealthCheckWithContext(ctx) on the root scope |
  | HealthCheckNamed(injector, name(*DB))         |
  | HealthCheck[*DB](injector)                    |
```

#### Scenario: A timed-out check is recorded at its bound and again when it returns
```gherkin
Given a started injector with a fake clock, HealthCheckInterval 0 and HealthCheckTimeout 0
And a service "db" whose HealthCheck() blocks until released and ignores any context, then returns nil
And the first round's check of "db" is running
When the fake clock advances by 10s, then the check is released and returns
Then after the advance, "db" was HealthUnhealthy with an error matching ErrHealthCheckTimeout and PendingSince set
And after the release, "db" is HealthHealthy with a zero PendingSince
```

<!-- cases: N/A 1 default: recording has no option -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: 3 zero values: a zero PendingSince once the check returned -->
<!-- cases: 4 boundaries: at the bound and at the late return -->
<!-- cases: 5 errors: the forced path's error is recorded unchanged; the timeout error at the bound -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: forced checks after Ready; a late result -->
<!-- cases: N/A 8 concurrency: next requirement -->
<!-- cases: N/A 9 panics: specified in health-check-execution -->
<!-- cases: 10 backward compatibility: forced paths keep their return values -->

### Requirement: Nothing is recorded for an abandoned check or after shutdown began
A check cancelled because every caller waiting for it stopped waiting SHALL NOT be recorded. Once root `Shutdown` began, no result SHALL be recorded and no health hook SHALL be called.

#### Scenario: An abandoned check is not recorded
```gherkin
Given an injector that is never started, with a service "db" whose HealthCheck(ctx) returns ctx.Err() once ctx is done
And the consumer's only read used a context cancelled after 10ms
When the consumer calls HealthReportWithContext with a context that is already cancelled
Then "db" has no recorded result: its CheckedAt is zero
```

#### Scenario: Results after shutdown began are not recorded
```gherkin
Given a started injector with a Healthchecker "db" that returned nil in the first round
And a HookServiceHealthChanged hook that records calls
And a Shutdowner "svc" that, during Shutdown, makes "db" return errors.New("closed") and calls HealthCheck() on the root scope
When the consumer calls Shutdown
Then the hook recorded only (Unknown, Healthy, nil)
```

<!-- cases: N/A 1 default: no option -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: 3 zero values: a zero CheckedAt for a service never recorded -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: not applicable -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: during shutdown, no alert is raised for services being shut down -->
<!-- cases: 8 concurrency: abandonment by every waiter -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new behavior -->
