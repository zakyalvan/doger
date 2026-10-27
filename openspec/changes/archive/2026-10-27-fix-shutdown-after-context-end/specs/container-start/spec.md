# Spec Delta

## MODIFIED Requirements

### Requirement: A child shutdown wait is bounded by its context
If the context of a child scope's `ShutdownWithContext` ends while it waits for a running start, the report SHALL fail with an error keyed by the scope with an empty service name wrapping `ctx.Err()`, and the shutdown SHALL proceed with that ended context: the services it then reaches SHALL be skipped and reported as `shutdown-context` specifies ("Services reached after the shutdown context ended are skipped").

#### Scenario: A child shutdown bounded by its context
```gherkin
Given a started root container inside synctest.Test and child scope "tenant" with lazy Starter "w", registered after Start, whose Start blocks until released and whose Shutdown() error records "shutdown"
And root.Refresh() is running in another goroutine and "w" has entered Start
When the consumer calls tenant.ShutdownWithContext(ctx) with a 1s timeout
Then it returns after 1s of bubble time with a failed report
And the report has an error keyed by scope "tenant" with an empty service name, matching context.DeadlineExceeded
And the report has an error for "w" in scope "tenant", matching context.DeadlineExceeded
And once "w" is released and Refresh returned, "shutdown" was never recorded and the start bookkeeping holds no entry for "w"
```

<!-- cases: 1 default: Shutdown() uses context.Background() and waits until the run returns -->
<!-- cases: 2 overrides: the context deadline -->
<!-- cases: N/A 3 zero values: a nil context means context.Background() -->
<!-- cases: N/A 4 boundaries: one deadline -->
<!-- cases: 5 errors: the context error in the report, for the scope and for the service the ended shutdown skipped -->
<!-- cases: N/A 6 duplicates, 9 panics: as for the wait -->
<!-- cases: 7 lifecycle: the shutdown proceeds with the ended context while the run is still running, so it skips the service still starting -->
<!-- cases: 8 concurrency: synctest bubble time; runs under -race with goleak -->
<!-- cases: 10 compatibility: the behavior is unchanged; the requirement now states that the services reached are skipped -->
