# Spec Delta

## Purpose

Lets a consumer observe where a root container is in its lifecycle (idle, starting, started, ready, failed, closing, closed) without hooking into every transition.

## ADDED Requirements

### Requirement: RootScope.Phase reports the container phase
`RootScope.Phase()` SHALL return a `ContainerPhase`: `PhaseIdle`, `PhaseStarting`, `PhaseStarted`, `PhaseReady`, `PhaseFailed`, `PhaseClosing` or `PhaseClosed`. Once root Shutdown has begun, the close phase SHALL be reported regardless of the start phase. `ContainerPhase.String()` SHALL return the lower-case phase name.

#### Scenario: Phase follows the lifecycle
```gherkin
Given a root scope with lazy Starter "a" whose Start blocks until released
And the consumer has <done>
When the consumer calls root.Phase()
Then the result is <phase>
And its String() is "<name>"

Examples:
  | done                                                         | phase         | name     |
  | done nothing                                                 | PhaseIdle     | idle     |
  | started Start() in another goroutine and "a" entered Start   | PhaseStarting | starting |
  | called Start(), which failed because a's provider returned errBuild | PhaseFailed | failed |
  | called Start() successfully                                  | PhaseReady    | ready    |
  | called Shutdown() without Start()                            | PhaseClosed   | closed   |
  | called Start() successfully and then Shutdown()              | PhaseClosed   | closed   |
```

#### Scenario: Hooks observe the intermediate phases
```gherkin
Given a root scope with a HookContainerStarted hook, a HookContainerReady hook and a HookContainerClosing hook, each recording root.Phase()
When the consumer calls Start() and then Shutdown()
Then the recorded phases are PhaseStarted, PhaseReady, PhaseClosing
```

#### Scenario: Phase is race-free
```gherkin
Given a root scope with lazy Starter "a"
When 20 goroutines call root.Phase() in a loop while the consumer calls Start() and then Shutdown()
Then no data race is reported
And every observed phase is one of PhaseIdle, PhaseStarting, PhaseStarted, PhaseReady, PhaseClosing, PhaseClosed
```

#### Scenario: Phase names
```gherkin
Given the value <phase>
When the consumer calls its String()
Then the result is "<name>"

Examples:
  | phase         | name     |
  | PhaseIdle     | idle     |
  | PhaseStarting | starting |
  | PhaseStarted  | started  |
  | PhaseReady    | ready    |
  | PhaseFailed   | failed   |
  | PhaseClosing  | closing  |
  | PhaseClosed   | closed   |
```

#### Scenario: Unknown phase values print as a number
```gherkin
Given the value ContainerPhase(42)
When the consumer calls its String()
Then the result is "ContainerPhase(42)"
```

<!-- cases: 1 default: a new container is PhaseIdle -->
<!-- cases: N/A 2 overrides: Phase is read-only -->
<!-- cases: 3 zero values: the zero ContainerPhase is PhaseIdle; an out-of-range value prints as a number -->
<!-- cases: N/A 4 boundaries: phases are an enumeration -->
<!-- cases: N/A 5 errors, 6 duplicates: no error path -->
<!-- cases: 7 lifecycle: every transition including shutdown without start and the intermediate phases seen by hooks -->
<!-- cases: 8 concurrency: race-free reads during transitions -->
<!-- cases: N/A 9 panics: no panicking path -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Phase is per container
A clone SHALL start in `PhaseIdle` whatever its source's phase, and a child scope SHALL have no phase of its own.

#### Scenario: Clone starts idle
```gherkin
Given a root scope that was started and is in PhaseReady
When the consumer calls root.Clone().Phase()
Then the result is PhaseIdle
And root.Phase() is still PhaseReady
```

<!-- cases: N/A 2 overrides, 3 zero values, 4 boundaries, 5 errors, 6 duplicates: a single rule -->
<!-- cases: 7 lifecycle: the clone row -->
<!-- cases: N/A 8 concurrency: covered by "Phase is race-free" -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: N/A 10 compatibility: new API -->
