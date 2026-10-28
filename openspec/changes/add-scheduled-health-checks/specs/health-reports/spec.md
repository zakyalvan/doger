# Spec Delta

## Purpose

Defines the health report a consumer reads instead of running checks. It covers what the report contains and which services it covers for the container and for a scope, with per-service and overall states. It also says when a read may run checks on demand, and how the container phase gates readiness and liveness, so that serving health to many readers costs no more than serving one.

## ADDED Requirements

### Requirement: A report lists each health-checked service with its last result
`HealthReport(opts...)` and `HealthReportWithContext(ctx, opts...)` SHALL return a `HealthReport` whose `Services` holds one `ServiceHealth` per covered check. Each entry has its `ServiceDescription`, `Tags`, `Optional`, `State`, `Err`, and the `CheckedAt` and `Duration` of its last recorded result, plus `PendingSince` while a check is in flight. The report's `CheckedAt` SHALL be the latest entry `CheckedAt`, and its `Phase` the container phase at the read.

#### Scenario: Report entries after one read
```gherkin
Given an injector that is never started, whose Clock is a fake clock set to 2026-01-01T00:00:00Z
And a service "<name>" registered in <scope> whose health check advances the fake clock by <takes> and returns <result>
When the consumer calls HealthReport() on the root scope
Then the entry for "<name>" has State <state>, Err <err>, CheckedAt 2026-01-01T00:00:00Z plus <takes>, Duration <takes> and a zero PendingSince
And the report's CheckedAt is 2026-01-01T00:00:00Z plus <takes> and its Phase is PhaseIdle

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

<!-- cases: 1 default: zero-config container -->
<!-- cases: N/A 2 overrides: the report shape has no option; filters are in health-classification -->
<!-- cases: 3 zero values: a zero Duration for a check that takes no clock time; a zero PendingSince when idle -->
<!-- cases: 4 boundaries: services across scopes -->
<!-- cases: 5 errors: Err holds the check's own error unchanged -->
<!-- cases: N/A 6 not found/duplicate: duplicates are the coverage requirement -->
<!-- cases: N/A 7 lifecycle: covered by the phase requirements below -->
<!-- cases: 8 concurrency: each caller gets an isolated copy -->
<!-- cases: N/A 9 panics: a panicking check's error is specified in health-check-execution -->
<!-- cases: N/A 10 backward compatibility: new API -->

### Requirement: Report entries are ordered by scope path, then name
`Services` SHALL be ordered by the path of scope names from the root (the root first, then shallower before deeper, then by name), and then by service name.

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

### Requirement: Only health checkers appear in a report, once per underlying check
A report SHALL include a service only when it is a health checker at read time. It SHALL leave out values without a health check, transient services, and lazy services not yet built. An alias or an export SHALL NOT add an entry of its own: when its source is covered, the source's entry is listed once, at the source's scope.

#### Scenario: Report membership
```gherkin
Given an injector that is never started, with <registration>
When the consumer calls HealthReport() on <target>
Then the report lists <listed>

Examples:
  | registration                                                           | target              | listed                  |
  | a plain value root/"answer" = 42                                       | the RootScope       | no service              |
  | a lazy Healthchecker root/"db" that was never invoked                  | the RootScope       | no service              |
  | a lazy Healthchecker root/"db" that was invoked                        | the RootScope       | root/"db"               |
  | a transient Healthchecker root/"tmp" that was invoked                  | the RootScope       | no service              |
  | a Healthchecker value root/"db" and an alias "hc" to it (AsNamed)      | the RootScope       | root/"db" only          |
  | an alias "hc" to a lazy Healthchecker root/"db" that was never invoked | the RootScope       | no service              |
  | a Healthchecker value "child"/"db" exported to the root                | the RootScope       | "child"/"db" only       |
  | a Healthchecker value "child"/"db" exported to the root                | the root's own *Scope | "child"/"db"          |
```

<!-- cases: N/A 1 default: membership has no option -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: 3 zero values: a container with no checker yields an empty Services slice -->
<!-- cases: N/A 4 boundaries: covered by the ordering requirement -->
<!-- cases: N/A 5 errors: no error path -->
<!-- cases: 6 not found/duplicate: an alias or export does not duplicate the entry -->
<!-- cases: 7 lifecycle: a lazy service before and after build -->
<!-- cases: N/A 8 concurrency: a lazy service being built is in health-check-execution -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: HealthCheck() keeps its own keys, including alias keys (health-check-execution) -->

### Requirement: The root report covers the container, a scope report covers what the scope sees
`RootScope.HealthReport()` SHALL cover every scope of the container, extension scopes included. `Scope.HealthReport()` SHALL cover the services visible from that scope: its own and its ancestors', plus the sources of exports it sees. It SHALL leave out an ancestor's service shadowed by a nearer scope's service of the same name, and a default that yielded.

#### Scenario: Report coverage by scope
```gherkin
Given an injector that is never started, with Healthchecker values root/"db" (healthy), root/"cache" (healthy)
And child scope "child" with Healthchecker value "db" (returns errors.New("down")) and Healthchecker value "q" registered AsDefault, while root has a Healthchecker value "q"
And grandchild scope "leaf" of "child" with Healthchecker value "queue" (healthy)
When the consumer calls HealthReport() on <target>
Then the report lists <listed>

Examples:
  | target                  | listed                                                         |
  | the RootScope           | root/"cache", root/"db", root/"q", "child"/"db", "leaf"/"queue" |
  | child scope "child"     | root/"cache", root/"q", "child"/"db"                           |
  | grandchild scope "leaf" | root/"cache", root/"q", "child"/"db", "leaf"/"queue"           |
```

<!-- cases: N/A 1 default: coverage has no option -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: 3 zero values: a scope with no checker and no ancestors' checkers yields an empty Services slice -->
<!-- cases: 4 boundaries: root, one and two levels deep -->
<!-- cases: N/A 5 errors: none -->
<!-- cases: 6 not found/duplicate: shadowing of "db"; the yielded default "q" -->
<!-- cases: 7 lifecycle: nested scopes; extension scopes are scopes of the container -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new API -->

### Requirement: The overall state summarizes the covered services
A report's `State` SHALL be `HealthUnhealthy` when any covered service is unhealthy, else `HealthDegraded` when any is degraded, else `HealthUnknown` when any covered critical service has no result yet, else `HealthHealthy`, including when no service is covered. An optional service without a result SHALL NOT make the state unknown. The container-phase gate (a later requirement) then applies.

#### Scenario: Overall state
```gherkin
Given a report covering services in states <states>, on an idle container
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
<!-- cases: N/A 7 lifecycle: the phase gate and the closed container are later requirements -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new API -->

### Requirement: The container phase gates readiness and liveness
A report covering readiness (no tag filter, or a filter including `HealthTagReadiness`) SHALL have `State` `HealthUnhealthy` in `PhaseStarting`, `PhaseStarted`, `PhaseFailed` and `PhaseClosing`. A report covering liveness SHALL have `State` `HealthUnhealthy` in `PhaseFailed`. `PhaseIdle` and `PhaseReady` SHALL NOT change the state. `IgnoreContainerPhase()` SHALL turn the gate off. Health results SHALL never change the phase.

#### Scenario: Phase gate on readiness and liveness
```gherkin
Given an injector with a Healthchecker value "db" (no options) returning nil, whose last result was recorded as HealthHealthy
And the container is in <phase>
When the consumer calls HealthReport(<options>) on the root scope
Then the report's Phase is <phase> and its State is <state>

Examples:
  | phase                                      | options                                                 | state           |
  | PhaseIdle                                  | none                                                    | HealthHealthy   |
  | PhaseStarting (blocked in a Starter)       | none                                                    | HealthUnhealthy |
  | PhaseStarted (blocked in a Started hook)   | MatchHealthTags(HealthTagReadiness)                     | HealthUnhealthy |
  | PhaseReady                                 | none                                                    | HealthHealthy   |
  | PhaseFailed (a Starter returned an error)  | MatchHealthTags(HealthTagReadiness)                     | HealthUnhealthy |
  | PhaseFailed (a Starter returned an error)  | MatchHealthTags(HealthTagLiveness)                      | HealthUnhealthy |
  | PhaseClosing (blocked in a Closing hook)   | MatchHealthTags(HealthTagReadiness)                     | HealthUnhealthy |
  | PhaseClosing (blocked in a Closing hook)   | MatchHealthTags(HealthTagLiveness)                      | HealthHealthy   |
  | PhaseStarting (blocked in a Starter)       | MatchHealthTags(HealthTagLiveness)                      | HealthHealthy   |
  | PhaseClosing (blocked in a Closing hook)   | MatchHealthTags("cache")                                | HealthHealthy   |
  | PhaseClosing (blocked in a Closing hook)   | MatchHealthTags(HealthTagReadiness), IgnoreContainerPhase() | HealthHealthy |
```

#### Scenario: Health never changes the phase
```gherkin
Given a started injector in PhaseReady with a Healthchecker value "db" returning errors.New("down")
When the consumer calls HealthReport() and HealthCheck() on the root scope
Then the report's State is HealthUnhealthy
And Phase() still returns PhaseReady
```

<!-- cases: 1 default: the gate is on; Idle and Ready rows are ungated -->
<!-- cases: 2 overrides: IgnoreContainerPhase(); a custom-tag filter is ungated -->
<!-- cases: N/A 3 zero values: an idle container (zero phase) is ungated, so containers that never call Start behave as before -->
<!-- cases: 4 boundaries: every phase except Closed (next requirement); readiness vs liveness filters -->
<!-- cases: N/A 5 errors: the gate sets no Err; Phase tells the reason -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: this requirement; liveness stays Healthy while starting, unlike Spring Boot's BROKEN, so a slow Start does not trigger restarts (startup probes cover a hung Start) -->
<!-- cases: N/A 8 concurrency: the phase is read once per report -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new API; the phase itself is unchanged by health -->

### Requirement: A closed container reports no service and an unknown state
After root `Shutdown` completed, a report SHALL have `Phase` `PhaseClosed`, `Monitored` false, `State` `HealthUnknown` and no service, whatever its options, and a read SHALL NOT run any check.

#### Scenario: A closed container reports no service
```gherkin
Given a started injector with a Healthchecker "db" that counts its calls
When the container is shut down and the consumer calls HealthReport(<options>) on the root scope
Then the report has Phase PhaseClosed, Monitored false, State HealthUnknown and an empty Services slice
And the health check of "db" was not called by that read

Examples:
  | options                 |
  | none                    |
  | IgnoreContainerPhase()  |
```

<!-- cases: N/A 1 default: no option -->
<!-- cases: 2 overrides: IgnoreContainerPhase does not change it -->
<!-- cases: 3 zero values: an empty Services slice -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: none -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: after shutdown -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new API -->

### Requirement: Services not started yet are not checked by reports
Once the container left `PhaseIdle`, a covered service that `Start`, `Refresh` or `Scope.Start` would start, but that has not been started successfully yet, SHALL be listed as `HealthUnknown` and SHALL NOT be checked by a report read or a scheduled round. `HealthCheck()` and the single-service functions SHALL still check it.

#### Scenario: A service registered after Ready waits for Refresh
```gherkin
Given a started injector in PhaseReady
And an eager value "late" registered afterwards, implementing Start(ctx) error and HealthCheck() error counting its calls and returning errors.New("not started") until started
When the consumer <action>, then calls HealthReport() on the root scope
Then the entry for "late" is <state> and its check was called <calls> times by that read

Examples:
  | action                         | state           | calls |
  | does nothing else              | HealthUnknown   | 0     |
  | calls Refresh, which succeeds  | HealthHealthy   | 1     |
```

#### Scenario: Idle containers check every service
```gherkin
Given an injector that is never started, with an eager value "svc" implementing Start(ctx) error and HealthCheck() error returning nil
When the consumer calls HealthReport() on the root scope
Then the entry for "svc" is HealthHealthy
```

<!-- cases: 1 default: no option -->
<!-- cases: N/A 2 overrides: Refresh or Scope.Start is the way to make it checked -->
<!-- cases: N/A 3 zero values: an idle container keeps today's semantics -->
<!-- cases: 4 boundaries: before and after Refresh -->
<!-- cases: N/A 5 errors: the service's own error is not reported before it starts -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: registration and Extend after Ready, incremental start -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: forced paths still check such a service -->

### Requirement: A monitored container's report runs only first checks
While a `HealthScheduler` is scheduling the container's rounds, a report SHALL have `Monitored` true. A read SHALL return the recorded results, and SHALL check on demand only covered services that have no recorded result and no check in flight. A covered service whose first check is in flight SHALL be `HealthUnknown` with `PendingSince` set.

#### Scenario: Reads on a monitored container cost nothing
```gherkin
Given a started injector with a manual HealthScheduler and a Healthchecker "db" that counts its calls
And the test ran the round once
When 1000 goroutines call HealthReport() on the root scope concurrently
Then every report has Monitored true and "db" in HealthHealthy
And the health check of "db" was called 1 time in total
```

#### Scenario: A service never checked is checked once by a read
```gherkin
Given a started injector with a manual HealthScheduler, whose round the test has not run, and a fake clock set to 2026-01-01T00:00:00Z
And a Healthchecker "db" whose check <behavior>
When the consumer calls HealthReport() on the root scope
Then the report has Monitored true
And "db" is <entry>

Examples:
  | behavior                                                         | entry                                                          |
  | returns nil                                                      | HealthHealthy                                                  |
  | is already in flight from another reader, blocking until released | HealthUnknown with PendingSince 2026-01-01T00:00:00Z, once the reader's 20ms context ends |
```

<!-- cases: N/A 1 default: monitoring needs a scheduler -->
<!-- cases: 2 overrides: the scheduler is in health-check-scheduling -->
<!-- cases: 3 zero values: no result yet -->
<!-- cases: 4 boundaries: 1 and 1000 readers; first check in flight -->
<!-- cases: N/A 5 errors: none -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: Ready before and after a round; a service appearing while monitored is checked by the next read -->
<!-- cases: 8 concurrency: 1000 concurrent readers -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new API -->

### Requirement: An unmonitored container's report runs stale checks on demand
When no scheduler is scheduling and the container is in `PhaseIdle`, `PhaseFailed` or `PhaseReady`, a read SHALL check each covered service whose last result is missing or at least `HealthReportCacheTTL` old, and return the fresh results with `Monitored` false. A TTL of 0 means 1s, and a negative TTL means always stale. Concurrent reads SHALL share each in-flight check. In `PhaseStarting`, `PhaseStarted` and `PhaseClosing` a read SHALL run no check.

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
Given an injector with a Healthchecker "db" that counts its calls, HealthScheduler <scheduler>
And the container is in <phase>
When the consumer calls HealthReport(IgnoreContainerPhase()) on the root scope
Then the health check of "db" was called <calls> times by that read
And the report has Monitored <monitored>

Examples:
  | phase                                     | scheduler          | calls | monitored |
  | PhaseIdle                                 | nil                | 1     | false     |
  | PhaseIdle                                 | a manual scheduler | 1     | false     |
  | PhaseFailed (a Starter returned an error) | nil                | 1     | false     |
  | PhaseReady                                | nil                | 1     | false     |
  | PhaseReady, after one round               | a manual scheduler | 0     | true      |
  | PhaseStarting (blocked in a Starter)      | nil                | 0     | false     |
  | PhaseStarted (blocked in a Started hook)  | nil                | 0     | false     |
  | PhaseClosing (blocked in a Closing hook)  | a manual scheduler | 0     | false     |
```

#### Scenario: Reads without on-demand checks return the last recorded results
```gherkin
Given a started injector with a Healthchecker "db" whose last recorded result is errors.New("down")
And a Closing hook that blocks until released
When Shutdown is called and, while the Closing hook blocks, the consumer calls HealthReport(IgnoreContainerPhase()) on the root scope
Then the report has Monitored false
And "db" is HealthUnhealthy with the same "down" error
```

<!-- cases: 1 default: TTL 1s on an idle container -->
<!-- cases: 2 overrides: positive and negative TTL; with and without a scheduler -->
<!-- cases: 3 zero values: TTL 0 means 1s; a nil context means context.Background() (next requirement) -->
<!-- cases: 4 boundaries: one millisecond before and exactly at the TTL (exactly at the TTL is stale) -->
<!-- cases: N/A 5 errors: a read has no error return; check errors are in the entries -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: every phase is a row; results while closing -->
<!-- cases: 8 concurrency: 50 readers share one check -->
<!-- cases: N/A 9 panics: specified in health-check-execution -->
<!-- cases: N/A 10 backward compatibility: new API -->

### Requirement: A report read is bounded by its caller's context and a per-service bound
A read SHALL stop waiting for a service when ctx is done or when the report bound elapses: `HealthCheckTimeout` when positive, `DefaultHealthReportCheckTimeout` (10s) when 0, and no bound when negative. It SHALL report each unfinished check as `HealthUnhealthy` with an error matching `ErrHealthCheckTimeout` and the cause. `HealthCheckGlobalTimeout` SHALL NOT apply. `HealthReport()` and a nil ctx SHALL use `context.Background()`.

#### Scenario: A reader that stops waiting
```gherkin
Given an injector that is never started, with a fake clock and HealthCheckTimeout <timeout>
And a service "db" whose HealthCheck() blocks until released and ignores any context
When the consumer calls HealthReportWithContext with a context that is <context>, and the fake clock advances by <advance>
Then the read returns <returns>
And the entry for "db" is <entry>

Examples:
  | timeout | context                       | advance | returns                    | entry                                                                  |
  | 0       | never cancelled               | 10s     | after the advance          | HealthUnhealthy, "DI: health check timeout: context deadline exceeded" |
  | 0       | never cancelled               | 9.999s  | not yet                    | (still waiting)                                                        |
  | 20ms    | never cancelled               | 20ms    | after the advance          | HealthUnhealthy, "DI: health check timeout: context deadline exceeded" |
  | -1ns    | never cancelled               | 10m     | not yet                    | (still waiting)                                                        |
  | 0       | cancelled before the call     | 0s      | at once                    | HealthUnhealthy, "DI: health check timeout: context canceled"          |
```

#### Scenario: A reader leaving does not affect other readers
```gherkin
Given an injector that is never started, with a service "db" whose HealthCheck(ctx) blocks until released or ctx is done
And reader A calls HealthReportWithContext with a context cancelled after 10ms
And reader B calls HealthReport() at the same time
When A's context is cancelled, then the check is released
Then A's entry for "db" is HealthUnhealthy with an error matching ErrHealthCheckTimeout and context.Canceled
And B's entry for "db" is HealthHealthy
And a third read within the TTL returns HealthHealthy for "db" without a new check
```

<!-- cases: 1 default: 10s bound with HealthCheckTimeout 0 -->
<!-- cases: 2 overrides: a positive and a negative HealthCheckTimeout; the caller's ctx -->
<!-- cases: 3 zero values: HealthCheckTimeout 0; nil context -->
<!-- cases: 4 boundaries: one millisecond before and exactly at the 10s default -->
<!-- cases: 5 errors: the timeout error matches ErrHealthCheckTimeout and the cause with errors.Is -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: N/A 7 lifecycle: on-demand phases are in the previous requirement -->
<!-- cases: 8 concurrency: one reader leaving while another waits -->
<!-- cases: N/A 9 panics: specified in health-check-execution -->
<!-- cases: 10 backward compatibility: the timeout message matches health-check-timeouts -->

### Requirement: Results are recorded once per check, timeouts at the report bound
The result of every completed check SHALL be recorded for reports, whichever path ran it. When a read or a round stops waiting for a check at its report bound, that check SHALL be recorded as timed out at that moment, and its later result SHALL be discarded, also when it was waiting for a check it could not join to return. A caller's context ending SHALL NOT record a timeout.

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

#### Scenario: A timed-out check's late result is discarded
```gherkin
Given an injector that is never started, with a fake clock, HealthReportCacheTTL 1h and HealthCheckTimeout 0
And a service "db" whose HealthCheck() blocks until released and ignores any context, then returns nil
And a HookServiceHealthChanged hook that records calls
When the consumer calls HealthReport(), the fake clock advances by 10s, then the check is released and returns, and the consumer reads HealthReport() again
Then both reads show "db" HealthUnhealthy with an error matching ErrHealthCheckTimeout
And the hook recorded only (Unknown, Unhealthy)
```

<!-- cases: N/A 1 default: recording has no option -->
<!-- cases: N/A 2 overrides: none -->
<!-- cases: N/A 3 zero values: not applicable -->
#### Scenario: A wait that times out on an abandoned check records the timeout
```gherkin
Given an injector with HealthCheckTimeout 20ms, <mode>, and a Healthchecker "db" whose result nil was recorded
And "db"'s check now blocks until released, ignoring its context
And a HealthCheck() call on the root scope timed out on it, so the check stays in flight, abandoned
When <reader> waits for "db"
Then the entry for "db" is HealthUnhealthy with an error matching ErrHealthCheckTimeout

Examples:
  | mode                                      | reader                                                |
  | a manual HealthScheduler, started         | the test, running the round, then reading HealthReport() |
  | no scheduler, HealthReportCacheTTL -1ns   | the consumer, calling HealthReport()                  |
```

<!-- cases: 4 boundaries: at the bound and at the late return -->
<!-- cases: 5 errors: the forced path's error is recorded unchanged; the timeout error at the bound -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: forced checks; a late result; a wait for an abandoned check (review R2) -->
<!-- cases: N/A 8 concurrency: next requirement -->
<!-- cases: N/A 9 panics: specified in health-check-execution -->
<!-- cases: 10 backward compatibility: forced paths keep their return values -->

### Requirement: Nothing is recorded for an abandoned check, a service that left, or after shutdown began
A check cancelled because every caller waiting for it stopped waiting SHALL NOT be recorded, and neither SHALL a check whose service left the container while it was in flight (its scope shut down, or its registration was replaced). Once root `Shutdown` began, no result SHALL be recorded and no health hook SHALL be called.

#### Scenario: An abandoned check is not recorded
```gherkin
Given an injector that is never started, with a service "db" whose HealthCheck(ctx) returns ctx.Err() once ctx is done
And the consumer's only read used a context cancelled after 10ms
When the consumer calls HealthReportWithContext with a context that is already cancelled
Then "db" has no recorded result: its CheckedAt is zero
```

#### Scenario: Results after shutdown began are not recorded
```gherkin
Given a started injector with a Healthchecker "db" whose recorded result is nil
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
#### Scenario: A check in flight when its service leaves is not recorded
```gherkin
Given an injector that is never started, with a HookContainerHealthChanged hook that records calls
And a Healthchecker "db" in child scope "child" whose check blocks until released, then returns errors.New("down")
And a HealthCheck() call on scope "child" is running that check
When <leaving>, then the check is released and returns
Then the injector holds no health record for that registration
And the container hook recorded nothing after the service left

Examples:
  | leaving                                                     |
  | scope "child" is shut down                                  |
  | "db" is replaced in scope "child" with OverrideNamedValue    |
```

<!-- cases: 7 lifecycle: during shutdown, no alert is raised for services being shut down; a service leaving with its check in flight (review R1) -->
<!-- cases: 8 concurrency: abandonment by every waiter; a check returning after its service left -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new behavior -->
