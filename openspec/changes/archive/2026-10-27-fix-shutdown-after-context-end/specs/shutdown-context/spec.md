# Spec Delta

## Purpose

Defines how a shutdown honors its context: which services are still shut down once the context has ended, how skipped ones are reported, and how the signal helper uses its context.

## ADDED Requirements

### Requirement: Services reached after the shutdown context ended are skipped
When `ShutdownWithContext` (root or child scope) reaches a built Shutdowner after its context ended, it SHALL NOT call its `Shutdown` method, and the report SHALL hold an error for it matching `ctx.Err()`. Any other service reached SHALL get no error. Every service reached SHALL be removed from its scope, and the `HookBeforeShutdown` and `HookAfterShutdown` hooks SHALL still run once for it, the latter with its report error (nil when none).

#### Scenario: A service reached after the context ended is skipped
```gherkin
Given a root scope with child scope "tenant", with one HookBeforeShutdown and one HookAfterShutdown hook recording the service names and the AfterShutdown error
And <service> registered as "s" in "tenant", counting its Shutdown calls
When the consumer calls tenant.ShutdownWithContext(ctx), with ctx <ctx>
Then "s" had its Shutdown called <calls> times
And the report <report>
And the BeforeShutdown and AfterShutdown hooks each saw "s" once, the AfterShutdown hook with the report's error for "s" (nil when it has none)
And doger.ShutdownNamed(tenant, "s") then returns an error matching ErrServiceNotFound

Examples:
  | service                                                                  | ctx                                   | calls | report                                                                      |
  | a built lazy service implementing Shutdown() error                       | cancelled before the call             | 0     | fails, with an error for "s" in scope "tenant" matching context.Canceled    |
  | a built lazy service implementing Shutdown(context.Context) error        | cancelled before the call             | 0     | fails, with an error for "s" in scope "tenant" matching context.Canceled    |
  | a value service (ProvideNamedValue) implementing Shutdown() error        | cancelled before the call             | 0     | fails, with an error for "s" in scope "tenant" matching context.Canceled    |
  | a built lazy service implementing Shutdown() error                       | with a 1ns deadline that has expired  | 0     | fails, with an error for "s" in scope "tenant" matching context.DeadlineExceeded |
  | a lazy service implementing Shutdown() error, never invoked              | cancelled before the call             | 0     | succeeds                                                                    |
  | a built lazy service implementing no Shutdowner interface (calls: 0 by definition) | cancelled before the call   | 0     | succeeds                                                                    |
  | an alias (AsNamed) to a built lazy Shutdowner registered in the root (calls: the target's) | cancelled before the call | 0 | succeeds                                                     |
  | a transient service implementing Shutdown() error, invoked once          | cancelled before the call             | 0     | succeeds                                                                    |
  | a built lazy service implementing Shutdown() error                       | context.Background()                  | 1     | succeeds                                                                    |
```

#### Scenario: An exported service reached after the context ended is skipped
```gherkin
Given a root scope with child scope "tenant"
And a built lazy "s" in "tenant" implementing Shutdown() error, counting its calls, exported to the root with doger.ExportNamed
And ctx cancelled before the call
When the consumer calls root.ShutdownWithContext(ctx)
Then "s" had its Shutdown called 0 times
And the report fails, with an error for "s" in scope "tenant" matching context.Canceled
And doger.ShutdownNamed(tenant, "s") then returns an error matching ErrServiceNotFound
```

#### Scenario: A deadline that ends during the shutdown skips the services after it
```gherkin
Given a root scope inside synctest.Test
And a built lazy "b" implementing Shutdown() error, counting its calls
And a built lazy "a" invoking "b", whose Shutdown() error sleeps 2s, then returns nil
When the consumer calls root.ShutdownWithContext(ctx), with ctx having a 1s timeout
Then "a" had its Shutdown called 1 time and has no error in the report
And "b" had its Shutdown called 0 times
And the report fails, with an error for "b" matching context.DeadlineExceeded
```

<!-- cases: 1 default: context.Background() never ends, so every service is shut down (last row) -->
<!-- cases: 2 overrides: the consumer controls the context; a still-live context with enough time shuts everything down -->
<!-- cases: 3 zero values: a service never built, implementing no Shutdowner interface, an alias or a transient service has nothing to shut down and reports no error (an alias never shuts its target down); a nil context is context.Background() (covered by container-start "Root Shutdown runs once") -->
<!-- cases: 4 boundaries: cancelled before the call, an expired deadline, a deadline ending between two services; a root that a child exports into, which shuts down through the export path -->
<!-- cases: 5 errors: the context error is reported under each skipped Shutdowner's name, and passed to the AfterShutdown hook -->
<!-- cases: lifecycle hooks: HookBeforeShutdown and HookAfterShutdown still run once for every service reached, skipped or not (inherited, documented) -->
<!-- cases: 6 duplicates: a skipped service is dropped, so a later shutdown of it finds nothing (ErrServiceNotFound) -->
<!-- cases: 7 lifecycle: a service whose Start still runs after a timed-out start wait is covered by container-start "A child shutdown wait is bounded by its context" -->
<!-- cases: 8 concurrency: the mid-shutdown deadline runs in a synctest bubble under -race -->
<!-- cases: N/A 9 panics: a skipped service runs no user code -->
<!-- cases: 10 compatibility: inherited from samber/do and unchanged; this requirement documents it -->

### Requirement: The signal helper's context only ends the wait for a signal
The context passed to `RootScope.ShutdownOnSignalsWithContext` SHALL only end the wait for a signal: whichever comes first, a signal or the context's end, the call SHALL shut down with a context that keeps that context's values without its cancellation or deadline, as `ShutdownOnSignals` does, and the `HookContainerClosing` and `HookContainerClosed` hooks SHALL receive it. The call SHALL return the signal, or nil when the context ended first.

#### Scenario: The signal helper shuts down when its context ends
```gherkin
Given a root scope with a built lazy "s" implementing Shutdown(context.Context) error, recording ctx.Err() and the value of ctx under the test key
And one hook registered for each of Closing and Closed, recording ctx.Err() and the value of ctx under the test key
And a context carrying the test key = "r-1", <ending>
When the consumer calls root.ShutdownOnSignalsWithContext(ctx) with no signal argument
Then the returned signal is nil
And the report succeeds
And "s" had its Shutdown called 1 time, with a context whose Err() is nil and whose test key value is "r-1"
And the Closing and Closed hooks each ran once, with a context whose Err() is nil and whose test key value is "r-1"

Examples:
  | ending                                           |
  | cancelled before the call                        |
  | with a 10ms timeout that expires while it waits  |
```

#### Scenario: The signal helper shuts down after a signal even when its context ends with it
```gherkin
Given a unix test process with a guard signal.Notify registered for SIGUSR1
And a root scope with a built lazy "s" implementing Shutdown(context.Context) error, recording ctx.Err()
And one hook registered for each of Closing and Closed, recording ctx.Err()
And ctx from signal.NotifyContext(context.Background(), SIGUSR1)
When the consumer calls root.ShutdownOnSignalsWithContext(ctx, SIGUSR1) and SIGUSR1 is sent to the process until the call returns
Then the returned signal is SIGUSR1 or nil
And the report succeeds
And "s" had its Shutdown called 1 time, with a context whose Err() is nil
And the Closing and Closed hooks each ran once, with a context whose Err() is nil
And this holds in each of 50 sequential repeats
```

<!-- cases: 1 default: ShutdownOnSignals passes context.Background(), unchanged -->
<!-- cases: 2 overrides: to bound the shutdown, the consumer waits for the signal itself (signal.NotifyContext) and calls ShutdownWithContext with its own deadline; documented on the Shutdowner page -->
<!-- cases: N/A 3 zero values: a nil context is not accepted by signal waiting today and is unchanged -->
<!-- cases: 4 boundaries: a context ended before the call, one ending while the call waits, and one ending right after a signal (NotifyContext for the same signal) -->
<!-- cases: N/A 5 errors: the shutdown reports service errors as ShutdownWithContext does -->
<!-- cases: N/A 6 duplicates: root shutdown runs once (container-start) -->
<!-- cases: 7 lifecycle: Closing and Closed hooks still run once (container-lifecycle-hooks "Closing and Closed hooks bracket the root shutdown"), now with the context that has no cancellation ("Hook contexts" holds: the helper passes it to ShutdownWithContext) -->
<!-- cases: 8 concurrency: the signal and the context's end race the select (and NotifyContext cancels right after the signal); the signal scenario repeats 50 times under -race, resending SIGUSR1 until the call returns -->
<!-- cases: N/A 9 panics: unchanged -->
<!-- cases: 10 compatibility: BEHAVIOR CHANGE (flagged) - when the context ended first, the call used to shut nothing down, return a report of context errors, and give the Closing and Closed hooks the ended context; after a signal, the shutdown used to be bounded and cancelled by the context, skipping services once it ended. It is now unbounded in both cases -->
<!-- cases: platform: the signal scenario runs on unix only (syscall.Kill, SIGUSR1); CI runs on ubuntu -->
