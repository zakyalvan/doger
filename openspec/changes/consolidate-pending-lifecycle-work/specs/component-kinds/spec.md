# Spec Delta

## ADDED Requirements

### Requirement: Clones copy kind handlers
`RootScope.Clone()` and `CloneWithOpts(opts)` SHALL copy the source's kind handler registrations, including consumer replacements of the default `Runnable` handler, with their stages and order. Registering a handler on the clone afterwards SHALL replace it on the clone only.

#### Scenario: Clone keeps replaced handlers
```gherkin
Given a root scope with lazy "job" opted into kind k and runnable "migrate"
And k.Handle(root, StageBeforeStart, handler recording "k <name>")
And Runnable.Handle(root, StageAfterStart, handler recording "custom <name>")
When the consumer calls Start() on <container>
Then the recorded events are <events>

Examples:
  | container                                                                    | events                    |
  | root.Clone()                                                                 | k job, custom migrate     |
  | root.CloneWithOpts(&InjectorOpts{})                                          | k job, custom migrate     |
```

#### Scenario: Replacing a handler on the clone leaves the source unchanged
```gherkin
Given a root scope with lazy "job" opted into kind k and k.Handle(root, StageBeforeStart, handler recording "source")
And clone := root.Clone() with k.Handle(clone, StageBeforeStart, handler recording "clone")
When the consumer calls Start() on root
Then the recorded events are source
```

<!-- cases: 1 default: a clone of a container without replaced handlers keeps the default Runnable behavior (first row's runnable part) -->
<!-- cases: 2 overrides: re-registering on the clone -->
<!-- cases: N/A 3 zero values: a container with no handlers clones to none -->
<!-- cases: N/A 4 boundaries: one and two kinds -->
<!-- cases: N/A 5 errors: no error path -->
<!-- cases: 6 duplicates: replacement on the clone keeps the slot order -->
<!-- cases: 7 lifecycle: clone of a started container starts independently (container-start) -->
<!-- cases: N/A 8 concurrency: handlers are configuration-time -->
<!-- cases: N/A 9 panics: no new path -->
<!-- cases: 10 compatibility: BEHAVIOR CHANGE (flagged) - before, a clone got only the default Runnable handler; the design listed this as an open question -->

### Requirement: Handlers run for new components on incremental start
`Refresh` and `Scope.Start` SHALL call each stage's handler for the components that have not been handled yet, in the same order rules as `Start`. A component SHALL be handled at most once per kind per container.

#### Scenario: Refresh handles only new components
```gherkin
Given a root scope with lazy "a" opted into kind k and k.Handle(root, StageBeforeStart, handler recording names)
And the consumer called Start()
And then the consumer registers lazy "b" opted into k
When the consumer calls Refresh() twice
Then the handler recorded a, b
```

#### Scenario: Runners run once
```gherkin
Given a root scope with runnable "migrate" and the consumer called Start()
And then the consumer registers runnable "seed"
When the consumer calls Refresh()
Then "migrate" ran 1 time in total
And "seed" ran 1 time
```

<!-- cases: 1 default: the default Runnable handler on Refresh -->
<!-- cases: N/A 2 overrides: replaced handlers follow the same rule -->
<!-- cases: N/A 3 zero values: no new component calls no handler -->
<!-- cases: 4 boundaries: zero, one new component; repeated Refresh -->
<!-- cases: 5 errors: a handler error fails Refresh as it fails Start (StartError Op "handle") -->
<!-- cases: 6 duplicates: at most once per kind per container -->
<!-- cases: 7 lifecycle: Scope.Start restricts components to its subtree, as for Starters -->
<!-- cases: N/A 8 concurrency: covered by container-start "Concurrent Refresh calls are serialized" -->
<!-- cases: N/A 9 panics: recovered as for Start -->
<!-- cases: N/A 10 compatibility: new API -->
