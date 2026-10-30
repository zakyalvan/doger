# Spec Delta

## Purpose

Defines how a container is constructed when construction can fail. `NewWithOpts`, `NewWithOptsContext` and `CloneWithOpts` return an error and a nil root, and `MustNewWithOpts` panics for tests and examples. `New`, `Clone` and `DefaultRootScope` keep their signatures because they cannot fail.

## ADDED Requirements

### Requirement: Option constructors return an error and a nil root on failure
`NewWithOpts(opts *InjectorOpts, packages ...func(Injector)) (*RootScope, error)` SHALL return a usable root and a nil error on success, and a nil root and a non-nil error on failure. A nil `opts` SHALL behave like `&InjectorOpts{}`. Without a `ConfigSource`, construction SHALL never fail.

#### Scenario: Construction outcome
```gherkin
Given <setup>
When the consumer calls doger.NewWithOpts(<opts>)
Then the returned root is <root>
And the returned error is <error>

Examples:
  | setup                                                                    | opts                                       | root    | error                                            |
  | nothing                                                                  | nil                                        | non-nil | nil                                              |
  | nothing                                                                  | &doger.InjectorOpts{}                      | non-nil | nil                                              |
  | a ConfigSource src whose Load returns nil                                | &doger.InjectorOpts{ConfigSource: src}     | non-nil | nil                                              |
  | a ConfigSource src whose Load returns errSealed = errors.New("sealed")   | &doger.InjectorOpts{ConfigSource: src}     | nil     | an error where errors.Is(err, errSealed) is true |
```

<!-- cases: 1 default: nil opts row -->
<!-- cases: 2 overrides: InjectorOpts rows; config-specific failures are specified in config-sources -->
<!-- cases: 3 zero values: nil opts and empty InjectorOpts rows -->
<!-- cases: 4 boundaries: with and without a ConfigSource; success and failure -->
<!-- cases: 5 errors: the failing-Load row; every error kind and text is specified in config-sources -->
<!-- cases: N/A 6 duplicate: duplicate registrations are "A package panic still panics" -->
<!-- cases: 7 lifecycle: a nil root on failure means nothing to shut down; "Packages run only after the config is loaded" shows no package ran -->
<!-- cases: N/A 8 concurrency: construction holds no process-wide state, so concurrent constructors are independent -->
<!-- cases: 9 panics: covered by "A package panic still panics" -->
<!-- cases: 10 compatibility: BREAKING signature change, flagged in the proposal; without a ConfigSource the result is always a root and nil -->

### Requirement: New keeps its signature
`New(packages ...func(Injector)) *RootScope` SHALL keep its signature and behavior: it never installs a `ConfigSource` and cannot fail.

#### Scenario: New is unchanged
```gherkin
Given a package pkg that provides a lazy service "*app.Svc"
When the consumer calls root := doger.New(pkg)
Then root is a non-nil *doger.RootScope
And doger.Invoke[*app.Svc](root) returns a non-nil *app.Svc and a nil error
```

<!-- cases: 1 default: the subject -->
<!-- cases: N/A 2 overrides: NewWithOpts is the override -->
<!-- cases: N/A 3 zero values: New() with no packages behaves as before -->
<!-- cases: N/A 4 boundaries: unchanged -->
<!-- cases: N/A 5 errors: New cannot fail -->
<!-- cases: N/A 6 duplicate: "A package panic still panics" -->
<!-- cases: N/A 7 lifecycle, 8 concurrency: unchanged -->
<!-- cases: N/A 9 panics: "A package panic still panics" -->
<!-- cases: 10 compatibility: the subject; about 800 call sites keep compiling -->

### Requirement: NewWithOptsContext bounds construction with the caller's context
`NewWithOptsContext(ctx context.Context, opts *InjectorOpts, packages ...func(Injector)) (*RootScope, error)` SHALL behave as `NewWithOpts`, passing a context derived from `ctx` to `Load`. When `ctx` has a deadline, `InjectorOpts.ConfigTimeout` SHALL NOT apply. A nil `ctx` SHALL mean `context.Background()`. `NewWithOpts(opts, ...)` SHALL equal `NewWithOptsContext(context.Background(), opts, ...)`.

#### Scenario: The context reaching Load
```gherkin
Given InjectorOpts.Clock set to a fake clock at 2026-01-01T00:00:00Z and InjectorOpts.ConfigTimeout 2s
And a ConfigSource src whose Load records ctx.Deadline() and ctx.Value(traceKey)
When the consumer calls doger.NewWithOptsContext(<ctx>, opts with ConfigSource src)
Then the recorded deadline is <deadline>
And the recorded trace value is <trace>

Examples:
  | ctx                                                                         | deadline                     | trace |
  | context.Background()                                                        | (2026-01-01T00:00:02Z, true) | nil   |
  | nil                                                                         | (2026-01-01T00:00:02Z, true) | nil   |
  | context.WithValue(context.Background(), traceKey, "t-1")                    | (2026-01-01T00:00:02Z, true) | "t-1" |
  | a context with deadline 2026-01-01T00:01:00Z                                | (2026-01-01T00:01:00Z, true) | nil   |
  | a context with deadline 2026-01-01T00:00:01Z                                | (2026-01-01T00:00:01Z, true) | nil   |
```

#### Scenario: Cancelling construction
```gherkin
Given a ConfigSource src whose Load blocks until its context is done and returns ctx.Err()
And a context ctx that the test cancels once Load has started
When the consumer calls doger.NewWithOptsContext(ctx, &doger.InjectorOpts{ConfigSource: src})
Then the call returns a nil root
And errors.Is(err, context.Canceled) and errors.Is(err, doger.ErrConfigLoad) are both true
```

<!-- cases: 1 default: Background row; NewWithOpts equals this with Background -->
<!-- cases: 2 overrides: caller deadline later and earlier than ConfigTimeout; context values pass through -->
<!-- cases: 3 zero values: nil ctx row -->
<!-- cases: 4 boundaries: caller deadline before and after now+ConfigTimeout -->
<!-- cases: 5 errors: cancellation scenario -->
<!-- cases: N/A 6 duplicate: one Load per construction -->
<!-- cases: 7 lifecycle: cancellation before any package runs -->
<!-- cases: 8 concurrency: cancellation from another goroutine, synchronized by a channel when Load starts -->
<!-- cases: N/A 9 panics: a panicking Load is specified in config-sources -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Packages run only after the config is loaded
The constructor SHALL run its packages in order, after the installed `ConfigSource` has loaded. When construction fails before the packages, no package SHALL run.

#### Scenario: Load happens before the packages
```gherkin
Given a ConfigSource src whose Load appends "load" to a shared log
And packages pkgA and pkgB that append "pkgA" and "pkgB" to that log
When the consumer calls doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src}, pkgA, pkgB)
Then the log is ["load", "pkgA", "pkgB"]
```

#### Scenario: A failed construction runs no package
```gherkin
Given <setup>
And a package pkgA that appends "pkgA" to a shared log
When the consumer calls doger.NewWithOpts(<opts>, pkgA)
Then the returned root is nil
And the log is empty

Examples:
  | setup                                                        | opts                                                         |
  | a ConfigSource src whose Load returns errors.New("sealed")   | &doger.InjectorOpts{ConfigSource: src}                       |
  | a ConfigSource src and a PropertySource ps                   | &doger.InjectorOpts{ConfigSource: src, PropertySource: ps}   |
  | a nil *app.Vault                                             | &doger.InjectorOpts{ConfigSource: (*app.Vault)(nil)}         |
```

<!-- cases: 1 default: without a source, packages run as before -->
<!-- cases: N/A 2 overrides: the order is fixed -->
<!-- cases: N/A 3 zero values: no packages is "Construction outcome" -->
<!-- cases: 4 boundaries: two packages show the order -->
<!-- cases: 5 errors: failed-load, conflict and typed-nil rows -->
<!-- cases: N/A 6 duplicate: not applicable to ordering -->
<!-- cases: 7 lifecycle: the subject -->
<!-- cases: N/A 8 concurrency: construction runs on the caller's goroutine -->
<!-- cases: N/A 9 panics: "A package panic still panics" -->
<!-- cases: 10 compatibility: packages still run in the order given -->

### Requirement: CloneWithOpts returns an error
`(*RootScope).CloneWithOpts(opts *InjectorOpts) (*RootScope, error)` SHALL construct the clone like `NewWithOpts(opts)`, sharing the parent's config only as specified in config-sources. On failure it SHALL return a nil clone and a non-nil error, and leave the parent unchanged. A nil `opts` SHALL behave like `&InjectorOpts{}`.

#### Scenario: Cloning with options
```gherkin
Given a root created with InjectorOpts.ConfigSource srcA holding "app.name" = "a"
And a second ConfigSource srcB holding "app.name" = "b", whose Load <load> and counts its calls
When the consumer calls root.CloneWithOpts(<opts>)
Then the returned clone is <clone>
And the returned error is <error>
And srcB.Load was called <loads> times
And the root's property "app.name" still reads ("a", true)

Examples:
  | opts                                       | load                                     | clone                                       | error                                            | loads |
  | &doger.InjectorOpts{ConfigSource: srcB}    | returns nil                              | non-nil, reading "app.name" as ("b", true)  | nil                                              | 1     |
  | &doger.InjectorOpts{ConfigSource: srcB}    | returns errSealed = errors.New("sealed") | nil                                         | an error where errors.Is(err, errSealed) is true | 1     |
  | &doger.InjectorOpts{}                      | returns nil                              | non-nil, reading "app.name" as ("", false)  | nil                                              | 0     |
  | nil                                        | returns nil                              | non-nil, reading "app.name" as ("", false)  | nil                                              | 0     |
```

<!-- cases: 1 default: sharing on Clone() is "Clone keeps its signature" and config-sources -->
<!-- cases: 2 overrides: a different source per clone -->
<!-- cases: 3 zero values: empty and nil opts rows give a clone without a config source -->
<!-- cases: 4 boundaries: success, failure, no source -->
<!-- cases: 5 errors: failure row; conflict and typed-nil apply as in NewWithOpts -->
<!-- cases: N/A 6 duplicate: sharing the parent's own source is config-sources -->
<!-- cases: 7 lifecycle: the parent is unchanged after a failed clone -->
<!-- cases: N/A 8 concurrency: as NewWithOpts -->
<!-- cases: N/A 9 panics: as NewWithOpts -->
<!-- cases: 10 compatibility: BREAKING signature change, flagged in the proposal -->

### Requirement: Clone keeps its signature
`(*RootScope).Clone() *RootScope` SHALL keep its signature and SHALL NOT fail: the clone shares its parent's config instead of loading it again.

#### Scenario: Clone of a root with config
```gherkin
Given a root created with InjectorOpts.ConfigSource src holding "app.name" = "a", whose Load counts its calls
When the consumer calls clone := root.Clone()
Then clone is a non-nil *doger.RootScope
And the clone's property "app.name" reads ("a", true)
And src.Load was called exactly 1 time
```

<!-- cases: 1 default: the subject -->
<!-- cases: N/A 2 overrides: CloneWithOpts is the override -->
<!-- cases: N/A 3 zero values: a root without config clones as before -->
<!-- cases: N/A 4 boundaries: one clone; repeated clones share the same way -->
<!-- cases: N/A 5 errors: Clone cannot fail: it never loads, so no config error can occur -->
<!-- cases: N/A 6 duplicate: sharing rules are in config-sources -->
<!-- cases: 7 lifecycle: clone after load; watcher sharing after Start is in config-reload -->
<!-- cases: N/A 8 concurrency: as before -->
<!-- cases: N/A 9 panics: Clone does not panic on config -->
<!-- cases: 10 compatibility: signature unchanged -->

### Requirement: MustNewWithOpts panics on error
`MustNewWithOpts(opts *InjectorOpts, packages ...func(Injector)) *RootScope` SHALL return the root when construction succeeds, and SHALL panic with the construction error itself as the panic value when it fails.

#### Scenario: Must constructor
```gherkin
Given <setup>
When the consumer calls doger.MustNewWithOpts(<opts>)
Then <outcome>

Examples:
  | setup                                                                  | opts                                     | outcome                                                                                                            |
  | nothing                                                                | nil                                      | a non-nil root is returned                                                                                         |
  | a ConfigSource src whose Load returns errSealed = errors.New("sealed") | &doger.InjectorOpts{ConfigSource: src}   | it panics with an error value v where errors.Is(v, errSealed) is true and v.Error() starts with "DI: config: load failed" |
```

<!-- cases: 1 default: nil opts row -->
<!-- cases: 2 overrides: the ConfigSource row -->
<!-- cases: 3 zero values: nil opts row -->
<!-- cases: N/A 4 boundaries: success or failure -->
<!-- cases: 5 errors: the panic value is the error, matchable with errors.Is; its text is not prefixed twice -->
<!-- cases: N/A 6 duplicate: as NewWithOpts -->
<!-- cases: N/A 7 lifecycle: as NewWithOpts -->
<!-- cases: N/A 8 concurrency: as NewWithOpts -->
<!-- cases: 9 panics: the subject -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: DefaultRootScope stays usable
`DefaultRootScope` SHALL be a usable root without any constructor call, with no `ConfigSource`, and a nil injector SHALL keep meaning `DefaultRootScope`.

#### Scenario: Registering on the default root
```gherkin
Given DefaultRootScope is replaced for the test by doger.New() and restored with t.Cleanup, and the test does not run in parallel
And a lazy "*app.Svc" registered with doger.Provide(nil, newSvc)
When the consumer calls doger.Invoke[*app.Svc](nil)
Then it returns a non-nil *app.Svc and a nil error
```

<!-- cases: 1 default: the subject -->
<!-- cases: N/A 2 overrides: replace DefaultRootScope with a constructed root -->
<!-- cases: 3 zero values: a nil injector means DefaultRootScope -->
<!-- cases: N/A 4-6: no input -->
<!-- cases: N/A 7 lifecycle: unchanged -->
<!-- cases: N/A 8 concurrency: unchanged -->
<!-- cases: N/A 9 panics: building it cannot fail -->
<!-- cases: 10 compatibility: the subject; the swap keeps the test repeatable under -count=2 -->

### Requirement: A package panic still panics
A panic raised by a package, such as a duplicate registration, SHALL propagate out of `New`, `NewWithOpts` and `NewWithOptsContext` unchanged, not be returned as an error.

#### Scenario: Duplicate registration in a package
```gherkin
Given a package pkg that registers a lazy service named "db" twice
When the consumer calls <call>
Then the call panics with "DI: service `db` has already been declared"

Examples:
  | call                                                        |
  | doger.New(pkg)                                              |
  | doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src}, pkg) |
```

<!-- cases: N/A 1-4: only the panic path is specified -->
<!-- cases: 5 errors: a panic, not an error -->
<!-- cases: 6 duplicate: the subject -->
<!-- cases: 7 lifecycle: a panic after Load leaves nothing global behind, since no process-wide state is claimed -->
<!-- cases: N/A 8 concurrency: unchanged -->
<!-- cases: 9 panics: the subject -->
<!-- cases: 10 compatibility: packages behave as before; error-returning packages are a later change -->
