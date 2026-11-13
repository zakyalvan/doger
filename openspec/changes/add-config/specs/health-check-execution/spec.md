# Spec Delta

## RENAMED Requirements

- FROM: `### Requirement: Creating a container starts no goroutine`
- TO: `### Requirement: Creating a container leaves no goroutine running`

## MODIFIED Requirements

### Requirement: Creating a container leaves no goroutine running
`New()`, `NewWithOpts()`, `MustNewWithOpts()`, `Clone()`, `CloneWithOpts()` and `MustCloneWithOpts()` SHALL NOT leave running a goroutine the library started, whatever `HealthCheckParallelism` and `HealthScheduler` are, for a caller context from the context package. A `ConfigSource`'s `Load` and its deadline timer may run goroutines while the constructor runs; none that the library started outlives it.

#### Scenario: New starts no goroutine
```gherkin
Given HealthCheckParallelism <n> and HealthScheduler <scheduler>
When the consumer builds a container with <constructor> and registers a Healthchecker value "db"
Then goleak, with its default retries, finds no goroutine that was started during the step still running

Examples:
  | n | scheduler                                                  | constructor                                                                                  |
  | 0 | nil                                                        | NewWithOpts(context.Background(), opts) with them                                            |
  | 4 | nil                                                        | NewWithOpts(context.Background(), opts) with them                                            |
  | 0 | a HealthScheduler that would start a goroutine when called | NewWithOpts(context.Background(), opts) with them                                            |
  | 4 | a HealthScheduler that would start a goroutine when called | Clone() of a container built with them                                                       |
  | 4 | a HealthScheduler that would start a goroutine when called | CloneWithOpts(context.Background(), opts) with them, of a zero-config container              |
  | 4 | nil                                                        | NewWithOpts with them, a context.WithTimeout(context.Background(), 1m) caller context and a ConfigSource whose Load returns nil |
  | 4 | nil                                                        | MustNewWithOpts(context.Background(), opts) with them and a ConfigSource whose Load returns nil |
  | 4 | nil                                                        | CloneWithOpts with them and a context.WithCancel(context.Background()) caller context, of a zero-config container |
  | 4 | nil                                                        | MustCloneWithOpts(context.Background(), opts) with them, of a zero-config container          |
```

<!-- cases: 1 default: 0 and nil -->
<!-- cases: 2 overrides: a positive parallelism (formerly started 4 pool goroutines, regression D6); a scheduler; a ConfigSource; caller contexts with a deadline and with cancellation -->
<!-- cases: N/A 3 zero values: covered by the default row -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: none -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: construction; when the scheduler is called is in health-check-scheduling; the config watcher starts only at Start (config-reload) -->
<!-- cases: 8 concurrency: goroutines still running after the constructor returns, checked with goleak's default retries so a goroutine that is already exiting is not counted; the guarantee covers goroutines the library starts, for a parent context from the context package (a parent of another kind may cost one goroutine inside the stdlib's context.AfterFunc machinery, documented) -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: 10 backward compatibility: New() no longer starts pool goroutines (an improvement); the requirement is RENAMED because the claim is narrowed to goroutines the library starts that outlive the constructor, since Load may run its own; the test keeps the name TestNew_NoHealthGoroutine -->
