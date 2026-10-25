# Spec Delta

## Purpose

Defines what a health check returns when its context ends or a configured health-check timeout elapses, so that timeout results are deterministic and always matchable with `errors.Is`.

## ADDED Requirements

### Requirement: A health check whose context ended returns a timeout error
When a health check's context is done (cancelled or past its deadline) by the time its result is observed, the call SHALL return an error that matches `ErrHealthCheckTimeout` and the context's error with `errors.Is`, with the message `DI: health check timeout: <context error>`, whatever the service's check returned. This holds for the per-service functions and for scope-wide health checks, on every call.

#### Scenario: Health check on an ended context is a timeout
```gherkin
Given a root scope with a service "svc" whose health check <check>
And a context that is <context>
When the consumer calls <entry> for "svc" 1000 times, each with a fresh scope and context
Then every call returns an error with message "DI: health check timeout: <ctx message>"
And every error matches ErrHealthCheckTimeout and <ctx error> with errors.Is

Examples:
  | entry                                       | context                            | check                                                     | ctx message               | ctx error                |
  | HealthCheckNamedWithContext                 | cancelled before the call          | Healthchecker returning nil                               | context canceled          | context.Canceled         |
  | HealthCheckNamedWithContext                 | past its deadline before the call  | Healthchecker returning nil                               | context deadline exceeded | context.DeadlineExceeded |
  | HealthCheckNamedWithContext                 | cancelled before the call          | HealthcheckerWithContext returning ctx.Err()              | context canceled          | context.Canceled         |
  | HealthCheckNamedWithContext                 | cancelled during the check         | HealthcheckerWithContext returning nil once ctx is done   | context canceled          | context.Canceled         |
  | HealthCheckNamedWithContext                 | cancelled during the check         | HealthcheckerWithContext returning errors.New("db down") once ctx is done | context canceled | context.Canceled |
  | HealthCheckWithContext (injector)           | cancelled before the call          | Healthchecker returning nil                               | context canceled          | context.Canceled         |
  | HealthCheckWithContext (injector)           | past its deadline before the call  | HealthcheckerWithContext returning ctx.Err()              | context deadline exceeded | context.DeadlineExceeded |
  | HealthCheckWithContext (child scope)        | cancelled before the call          | Healthchecker returning nil, registered in the root scope | context canceled          | context.Canceled         |
```

#### Scenario: Health check that finishes before its context ends keeps its result
```gherkin
Given a root scope with a service "svc" whose health check returns <result>
And a context that is cancelled only after the call returns
When the consumer calls <entry> for "svc"
Then the call returns <expected>

Examples:
  | entry                              | result                  | expected                |
  | HealthCheckNamedWithContext        | nil                     | nil                     |
  | HealthCheckNamedWithContext        | errors.New("db down")   | the same "db down" error |
  | HealthCheckWithContext (injector)  | nil                     | nil for "svc"           |
  | HealthCheckWithContext (injector)  | errors.New("db down")   | the same "db down" error for "svc" |
  | HealthCheckNamed (no context)      | nil                     | nil                     |
```

<!-- cases: 1 default: the "keeps its result" scenario is the zero-configuration path (no timeout options, live context) -->
<!-- cases: N/A 2 overrides: the rule has no option by design (see proposal Defaults & Control); timeout options are the next requirement -->
<!-- cases: 3 zero values: context.Background() (never done) is the "HealthCheckNamed (no context)" row; a nil context is unchanged behavior and not part of this requirement -->
<!-- cases: 4 boundaries: before the call vs during the check; a result observed exactly at the deadline is covered by the clock-driven boundary rows of the next requirement -->
<!-- cases: 5 errors: each row states the message and both errors.Is matches; a service error returned in time stays as is -->
<!-- cases: N/A 6 not found/duplicate: an unknown service name keeps returning ErrServiceNotFound, unchanged -->
<!-- cases: 7 lifecycle: the child-scope row checks a service inherited from the root scope -->
<!-- cases: 8 concurrency: the 1000-call repetition pins the formerly random outcome; cancellation during the check is a row -->
<!-- cases: N/A 9 panics: panic behavior of health checks is unchanged and out of scope -->
<!-- cases: 10 backward compatibility: message text is unchanged; errors.Is(err, ErrHealthCheckTimeout) keeps matching; errors.Is with the context error now also matches (additive); results returned before the context ends are unchanged -->

### Requirement: Configured health-check timeouts elapse on the injector's clock
`HealthCheckTimeout` (per service) and `HealthCheckGlobalTimeout` (per scope-wide check) SHALL be measured on the injector's clock. A check still running when its timeout elapses SHALL return the timeout error with `context deadline exceeded`; a check that returns before SHALL keep its result. A zero duration SHALL mean no timeout.

#### Scenario: Timeout options decide the result on the clock
```gherkin
Given an injector with a fake clock and options <options>
And a service "svc" whose health check returns <result> after <duration> on that clock
When the consumer calls HealthCheckWithContext on the injector with context.Background()
And the fake clock advances by <advance>
Then the result for "svc" is <expected>

Examples:
  | options                                              | result                | duration | advance | expected                                                  |
  | HealthCheckTimeout: 10ms                             | nil                   | 20ms     | 20ms    | "DI: health check timeout: context deadline exceeded"     |
  | HealthCheckTimeout: 10ms                             | nil                   | 9ms      | 9ms     | nil                                                       |
  | HealthCheckTimeout: 10ms                             | nil                   | 10ms     | 10ms    | "DI: health check timeout: context deadline exceeded"     |
  | HealthCheckTimeout: 10ms                             | errors.New("db down") | 5ms      | 5ms     | the same "db down" error                                  |
  | HealthCheckGlobalTimeout: 10ms                       | nil                   | 20ms     | 20ms    | "DI: health check timeout: context deadline exceeded"     |
  | HealthCheckGlobalTimeout: 10ms                       | nil                   | 9ms      | 9ms     | nil                                                       |
  | HealthCheckTimeout: 50ms, HealthCheckGlobalTimeout: 10ms | nil               | 20ms     | 20ms    | "DI: health check timeout: context deadline exceeded"     |
  | HealthCheckTimeout: 10ms, HealthCheckGlobalTimeout: 50ms | nil               | 20ms     | 20ms    | "DI: health check timeout: context deadline exceeded"     |
  | (none: both 0)                                       | nil                   | 1h       | 1h      | nil                                                       |
```

#### Scenario: Sequential health checks share the global timeout
```gherkin
Given an injector with a fake clock, HealthCheckParallelism 1 and HealthCheckGlobalTimeout 50ms
And services "a", "b", "c" and "d" whose health checks each return nil after 20ms on that clock
When the consumer calls HealthCheckWithContext on the injector with context.Background()
And the fake clock advances in 10ms steps until the call returns
Then exactly 2 results are nil
And exactly 2 results are "DI: health check timeout: context deadline exceeded"
```

#### Scenario: Health check does not time out before the clock reaches the timeout
```gherkin
Given an injector with a fake clock and HealthCheckTimeout 10ms
And a service "svc" whose health check blocks until released
When the consumer starts HealthCheckWithContext on the injector
And the fake clock advances by 9ms
Then the call has not returned
And after the fake clock advances by another 1ms the result for "svc" is "DI: health check timeout: context deadline exceeded"
```

<!-- cases: 1 default: the "(none: both 0)" row: no timeout with zero configuration -->
<!-- cases: 2 overrides: per-service and global timeout rows, and both combined (the shorter one wins) -->
<!-- cases: 3 zero values: both options 0 means no timeout -->
<!-- cases: 4 boundaries: 9ms (one before), 10ms (exactly at the limit), 20ms (past it); the "does not time out before" scenario pins the boundary from the other side -->
<!-- cases: 5 errors: timeout message and errors.Is matching per the previous requirement; a service error returned in time is kept -->
<!-- cases: N/A 6 not found/duplicate: unrelated to timeouts -->
<!-- cases: 7 lifecycle: HealthCheckParallelism 1 runs checks through the health-check pool; the pool is stopped by Shutdown at the end of each test -->
<!-- cases: 8 concurrency: the sequential scenario fixes the outcome by count, not by map order; timed-out checks that are still running finish when the clock advances, so no goroutine leaks -->
<!-- cases: N/A 9 panics: unchanged and out of scope -->
<!-- cases: 10 backward compatibility: option names, zero-value meaning and messages are unchanged; with the default real clock the timeouts behave as before, minus the random outcome -->
