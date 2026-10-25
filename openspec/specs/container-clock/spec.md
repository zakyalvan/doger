# container-clock Specification

## Purpose

Defines the container's time source: the `InjectorOpts.Clock` option, its real-clock default, and the timeouts and durations the container measures on it, so consumers can control time in their own tests.

## Requirements

### Requirement: The injector clock defaults to the real clock
`InjectorOpts.Clock` (type `clockwork.Clock` from `github.com/jonboulle/clockwork`) SHALL set the injector's time source. A nil `Clock`, including `New()` and `NewWithOpts(nil)`, SHALL use the real clock, so timeouts and durations follow wall time as before.

#### Scenario: Zero configuration measures health-check timeouts on wall time
```gherkin
Given an injector created by <constructor> with HealthCheckTimeout 10ms where the constructor allows options, inside synctest.Test
And a service "svc" whose health check returns nil after time.Sleep(<sleep>)
When the consumer calls HealthCheckWithContext on the injector with context.Background()
Then the result for "svc" is <expected>

Examples:
  | constructor                                          | sleep | expected                                              |
  | NewWithOpts(&InjectorOpts{HealthCheckTimeout: 10ms}) | 20ms  | "DI: health check timeout: context deadline exceeded" |
  | NewWithOpts(&InjectorOpts{HealthCheckTimeout: 10ms}) | 5ms   | nil                                                   |
  | NewWithOpts(&InjectorOpts{HealthCheckTimeout: 10ms, Clock: nil}) | 20ms | "DI: health check timeout: context deadline exceeded" |
  | New() (no timeout)                                   | 20ms  | nil                                                   |
  | NewWithOpts(nil) (no timeout)                        | 20ms  | nil                                                   |
```

<!-- cases: 1 default: every row uses the default real clock; synctest makes wall time deterministic -->
<!-- cases: 2 overrides: a fake clock is the next requirement -->
<!-- cases: 3 zero values: Clock nil, NewWithOpts(nil) and New() rows -->
<!-- cases: 4 boundaries: 5ms (before) and 20ms (after) the 10ms timeout; the exact-limit boundary is specified in health-check-timeouts on a fake clock -->
<!-- cases: 5 errors: the timeout message; matching rules are in health-check-timeouts -->
<!-- cases: N/A 6 not found/duplicate: no lookup involved -->
<!-- cases: N/A 7 lifecycle: scopes and clones are the last requirement -->
<!-- cases: N/A 8 concurrency: covered by the fake-clock requirements and the existing race suite -->
<!-- cases: N/A 9 panics: a nil Clock is a valid default, never a panic -->
<!-- cases: 10 backward compatibility: a consumer that never sets Clock gets the previous wall-clock behavior -->

### Requirement: The container measures time only on its clock
With `InjectorOpts.Clock` set, the container SHALL read every time it measures from that clock: the `HealthCheckTimeout` and `HealthCheckGlobalTimeout` deadlines, a lazy service's build time (`ServiceBuildTime` in `ExplainService` and `ExplainNamedService` output), and `ShutdownReport.ShutdownTime` and `ShutdownReport.ServiceShutdownTime`.

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
<!-- cases: 2 overrides: every row sets a fake clock; each time read the container makes is a row -->
<!-- cases: 3 zero values: a 0s advance yields a zero build time, which explain output omits as today -->
<!-- cases: 4 boundaries: the exact-limit and one-before boundaries are in health-check-timeouts -->
<!-- cases: N/A 5 errors: measurement only; timeout errors are specified in health-check-timeouts -->
<!-- cases: N/A 6 not found/duplicate: no lookup involved -->
<!-- cases: 7 lifecycle: build (first invocation) and shutdown are the measured phases -->
<!-- cases: 8 concurrency: the wall-time scenario shows the deadline is independent of goroutine timing; the blocked check is released before the test ends, so no goroutine leaks -->
<!-- cases: N/A 9 panics: unchanged -->
<!-- cases: 10 backward compatibility: field names, JSON keys and the omission of a zero build time are unchanged -->

### Requirement: Scopes and clones use the root scope's clock
Child scopes SHALL use their root scope's clock. `Clone()` SHALL keep the original clock. `CloneWithOpts(opts)` SHALL use `opts.Clock`, defaulting to the real clock when nil.

#### Scenario: Derived injectors measure timeouts on the expected clock
```gherkin
Given a root injector whose Clock is fake clock F and HealthCheckTimeout is 10ms
And a service "svc" whose health check blocks until released, registered in <where>
When the consumer calls HealthCheckWithContext on <target>
And fake clock F advances by 10ms
Then the result for "svc" is <expected>

Examples:
  | where             | target                                           | expected                                              |
  | a child scope "c" | child scope "c"                                  | "DI: health check timeout: context deadline exceeded" |
  | the root scope    | child scope "c" (inherited service)              | "DI: health check timeout: context deadline exceeded" |
  | the root scope    | Clone() of the root injector                     | "DI: health check timeout: context deadline exceeded" |
  | the root scope    | CloneWithOpts with a new fake clock G, timeout 10ms | still running: G has not advanced                  |
```

<!-- cases: 1 default: CloneWithOpts with Clock nil falls back to the real clock (covered by the first requirement's Clock: nil row) -->
<!-- cases: 2 overrides: CloneWithOpts with its own clock -->
<!-- cases: N/A 3 zero values: covered by the first requirement -->
<!-- cases: N/A 4 boundaries: covered in health-check-timeouts -->
<!-- cases: N/A 5 errors: timeout message only -->
<!-- cases: N/A 6 not found/duplicate: no lookup involved -->
<!-- cases: 7 lifecycle: child scope, inherited service, Clone and CloneWithOpts rows -->
<!-- cases: 8 concurrency: blocked checks are released before each test ends, so no goroutine leaks -->
<!-- cases: N/A 9 panics: unchanged -->
<!-- cases: 10 backward compatibility: Clone and CloneWithOpts keep copying options as before; Clock is copied like the other fields -->
