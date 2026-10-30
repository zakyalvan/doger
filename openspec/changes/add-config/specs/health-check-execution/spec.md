# Spec Delta

## MODIFIED Requirements

### Requirement: Creating a container starts no goroutine
`New()`, `NewWithOpts()`, `NewWithOptsContext()`, `MustNewWithOpts()`, `Clone()`, `CloneWithOpts()`, `CloneWithOptsContext()` and `MustCloneWithOpts()` SHALL NOT leave running a goroutine they started, whatever `HealthCheckParallelism` and `HealthScheduler` are. A `ConfigSource`'s `Load` and its deadline timer may run goroutines while the constructor runs; none of them outlives it.

#### Scenario: New starts no goroutine
```gherkin
Given HealthCheckParallelism <n> and HealthScheduler <scheduler>
When the consumer builds a container with <constructor> and registers a Healthchecker value "db"
Then the number of goroutines is unchanged

Examples:
  | n | scheduler                                                  | constructor                                        |
  | 0 | nil                                                        | NewWithOpts with them                              |
  | 4 | nil                                                        | NewWithOpts with them                              |
  | 0 | a HealthScheduler that would start a goroutine when called | NewWithOpts with them                              |
  | 4 | a HealthScheduler that would start a goroutine when called | Clone() of a container built with them             |
  | 4 | a HealthScheduler that would start a goroutine when called | CloneWithOpts with them, of a zero-config container |
  | 4 | nil                                                        | NewWithOptsContext with them and a ConfigSource whose Load returns nil |
  | 4 | nil                                                        | MustNewWithOpts with them and a ConfigSource whose Load returns nil    |
  | 4 | nil                                                        | CloneWithOptsContext with them, of a zero-config container             |
  | 4 | nil                                                        | MustCloneWithOpts with them, of a zero-config container                |
```

<!-- cases: 1 default: 0 and nil -->
<!-- cases: 2 overrides: a positive parallelism (formerly started 4 pool goroutines, regression D6); a scheduler; a ConfigSource -->
<!-- cases: N/A 3 zero values: covered by the default row -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: none -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: construction; when the scheduler is called is in health-check-scheduling; the config watcher starts only at Start (config-reload) -->
<!-- cases: 8 concurrency: goroutine count after the constructor returns -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: 10 backward compatibility: New() no longer starts pool goroutines (an improvement); the claim is narrowed to goroutines that outlive the constructor, since Load may run its own -->
