# Spec Delta

## Purpose

Defines the `github.com/kartaladev/doger/scheduler/gocron` module (package `dogergocron`), a `doger.HealthScheduler` built on go-co-op/gocron v2. It gives consumers scheduled health rounds with safe defaults, and lets them use any gocron schedule or an existing gocron scheduler. The root module gains no dependency.

## ADDED Requirements

### Requirement: New returns a scheduler with safe defaults
`dogergocron.New(opts...)` SHALL return a `*Scheduler` implementing `doger.HealthScheduler`. With no option, each `ScheduleHealthRounds` call SHALL:
- create and start its own gocron scheduler on the given clock;
- run the first round at once, then a round every `DefaultInterval` (30s);
- never run two rounds of one schedule at once: a tick that finds a round still running SHALL be skipped.

#### Scenario: Default cadence on the container clock
```gherkin
Given an injector whose Clock is a fake clock, HealthScheduler is dogergocron.New(), and a Healthchecker "db" that counts its calls
When the consumer calls Start, which succeeds, and the fake clock advances by <advance>, waiting for each due round
Then the health check of "db" was called <calls> times

Examples:
  | advance | calls |
  | 0s      | 1     |
  | 29.999s | 1     |
  | 30s     | 2     |
  | 60s     | 3     |
```

#### Scenario: A slow round skips the next tick
```gherkin
Given an injector whose Clock is a fake clock, HealthCheckTimeout is -1ns, HealthScheduler is dogergocron.New()
And a Healthchecker "db" whose first check blocks until released and counts its calls
When the consumer calls Start, the fake clock advances by 30s, the check is released, and the fake clock advances by 30s more
Then the health check of "db" was called 2 times, never two at once
```

<!-- cases: 1 default: this requirement -->
<!-- cases: 2 overrides: next requirement -->
<!-- cases: N/A 3 zero values: no option is the default -->
<!-- cases: 4 boundaries: one millisecond before and at the interval -->
<!-- cases: N/A 5 errors: a later requirement -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: the first round at Ready -->
<!-- cases: 8 concurrency: singleton rounds -->
<!-- cases: N/A 9 panics: checks recover their panics in the root module; gocron also recovers job panics -->
<!-- cases: N/A 10 backward compatibility: new module -->

### Requirement: Every default can be overridden
`WithInterval(d)` SHALL set the default job's interval, `WithJobDefinition(def)` SHALL replace the job definition (such as `gocron.CronJob`), and `WithStartAt(opt)` SHALL replace the default immediate first round. `WithSchedulerOptions` and `WithJobOptions` SHALL apply after the defaults, so an option wins over the default it overrides, except start-at options, which gocron combines. Repeated options SHALL accumulate in order.

#### Scenario: Overridden cadence
```gherkin
Given an injector whose Clock is a fake clock, HealthScheduler is dogergocron.New(<options>), and a Healthchecker "db" that counts its calls
When the consumer calls Start and the fake clock advances by <advance>, waiting for each due round
Then the health check of "db" was called <calls> times

Examples:
  | options                                                                                  | advance | calls |
  | WithInterval(10s)                                                                        | 20s     | 3     |
  | WithJobDefinition(gocron.DurationJob(5s))                                                | 5s      | 2     |
  | WithJobDefinition(gocron.DurationJob(5s)), WithInterval(10s)                             | 10s     | 3     |
  | WithStartAt(gocron.WithStartDateTime(the fake clock's now + 10s))                        | 9.999s  | 0     |
  | WithStartAt(gocron.WithStartDateTime(the fake clock's now + 10s))                        | 10s     | 1     |
```

#### Scenario: Scheduler options reach gocron
```gherkin
Given dogergocron.New(WithSchedulerOptions(gocron.WithLogger(L))) where L records calls
When a container with it is started and shut down
Then L recorded at least 1 call
```

<!-- cases: N/A 1 default: previous requirement -->
<!-- cases: 2 overrides: interval, job definition, start-at, job and scheduler options -->
<!-- cases: N/A 3 zero values: invalid values are the next requirement -->
<!-- cases: 4 boundaries: before and at a delayed start -->
<!-- cases: N/A 5 errors: next requirement -->
<!-- cases: 6 not found/duplicate: a job definition wins over an interval given after it (WithJobDefinition takes precedence over WithInterval) -->
<!-- cases: N/A 7 lifecycle: not applicable -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new module -->

### Requirement: Invalid configuration is reported as an error
`New` SHALL return an error matching `dogergocron.ErrInvalidOption` for a non-positive `WithInterval` or a nil `WithJobDefinition`, `WithStartAt` or `WithScheduler`. `ScheduleHealthRounds` SHALL return the error gocron reports for a job or scheduler it rejects, leaving no goroutine behind.

#### Scenario: Invalid options
```gherkin
Given the option <option>
When the consumer calls dogergocron.New with it
Then New returns <result>

Examples:
  | option                             | result                                                                     |
  | WithInterval(0)                    | an error matching ErrInvalidOption containing "interval must be positive"  |
  | WithInterval(-1s)                  | an error matching ErrInvalidOption containing "interval must be positive"  |
  | WithJobDefinition(nil)             | an error matching ErrInvalidOption containing "job definition is nil"      |
  | WithScheduler(nil)                 | an error matching ErrInvalidOption containing "scheduler is nil"           |
  | WithStartAt(nil)                   | an error matching ErrInvalidOption containing "start-at option is nil"     |
  | WithInterval(1ns)                  | a scheduler and a nil error                                                |
```

#### Scenario: A schedule gocron rejects
```gherkin
Given dogergocron.New(WithJobDefinition(gocron.CronJob("not a cron", false)))
When ScheduleHealthRounds is called with a fake clock and a round
Then it returns an error from gocron and a nil stop function
And goleak reports no goroutine started by gocron
```

<!-- cases: N/A 1 default: defaults are valid -->
<!-- cases: N/A 2 overrides: each invalid option is a row -->
<!-- cases: 3 zero values: zero interval, nil definition, nil start-at, nil scheduler -->
<!-- cases: 4 boundaries: 0, negative and the smallest positive interval -->
<!-- cases: 5 errors: ErrInvalidOption and gocron's own error -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: a rejected schedule leaves the container unmonitored (health-check-scheduling) -->
<!-- cases: 8 concurrency: goleak after a rejected schedule -->
<!-- cases: N/A 9 panics: options never panic -->
<!-- cases: N/A 10 backward compatibility: new module -->

### Requirement: A shared gocron scheduler is used without being owned
With `WithScheduler(s)`, `ScheduleHealthRounds` SHALL add one job to `s` and SHALL NOT create, start or shut down any gocron scheduler. The returned stop function SHALL remove only that job, SHALL return once no round of that job runs (within its context), and SHALL treat a job `s` no longer holds as removed. The clock passed by the container SHALL then be ignored in favor of `s`'s own clock, and starting `s` stays the owner's job.

#### Scenario: Shared scheduler
```gherkin
Given a gocron scheduler S, started by the application, with one application job "report"
And an injector whose HealthScheduler is dogergocron.New(WithScheduler(S))
When the consumer calls Start, then Shutdown on the injector
Then while the container was ready, S held 2 jobs
And after Shutdown, S holds only "report" and is still running
```

#### Scenario: Stopping a shared schedule
```gherkin
Given a gocron scheduler S, started by the application, and dogergocron.New(WithScheduler(S)) scheduling a round on S
And <state>
When the stop function is called with context.Background()
Then <returns>

Examples:
  | state                                                 | returns                                                   |
  | the round is running, blocked until released          | stop returns only after the round is released and returns |
  | the application shut S down                           | stop returns nil                                          |
```

<!-- cases: N/A 1 default: an own scheduler is the default -->
<!-- cases: 2 overrides: this requirement -->
<!-- cases: N/A 3 zero values: nil is an invalid option -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: a job S rejects is returned as in the previous requirement -->
<!-- cases: 6 not found/duplicate: two containers (or a clone) sharing S each add and remove their own job -->
<!-- cases: 7 lifecycle: S outlives the container -->
<!-- cases: N/A 8 concurrency: gocron schedulers are safe for concurrent use -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new module -->

### Requirement: Stopping leaves no goroutine
The stop function SHALL shut down the adapter's own gocron scheduler, waiting for it within its context, and return gocron's error. After a container using the adapter shut down, no goroutine of the adapter or of gocron SHALL remain, whatever context Shutdown received.

#### Scenario: Clean stop
```gherkin
Given a test package whose TestMain runs goleak.VerifyTestMain
And an injector with HealthScheduler dogergocron.New() and a Healthchecker "db"
When the consumer calls Start, then <shutdown>
Then goleak reports no leaked goroutine when the test ends

Examples:
  | shutdown                                               |
  | Shutdown()                                             |
  | ShutdownWithContext with an already-cancelled context  |
```

<!-- cases: N/A 1 default: no option -->
<!-- cases: N/A 2 overrides: a shared scheduler is the previous requirement -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: 5 errors: gocron's shutdown error is returned to the container, which logs it -->
<!-- cases: N/A 6 not found/duplicate: stop is called once by the container -->
<!-- cases: 7 lifecycle: an ended Shutdown context (shutdown-context) -->
<!-- cases: 8 concurrency: goleak -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new module -->
