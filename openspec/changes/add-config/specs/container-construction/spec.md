# Spec Delta

## Purpose

Defines how a container is constructed when construction can fail. `NewWithOpts` and `CloneWithOpts` take the caller's context first and return an error and a nil root; `MustNewWithOpts` and `MustCloneWithOpts` take the same context and panic for tests and examples. `New`, `Clone` and `DefaultRootScope` keep their signatures because they cannot fail, and no constructor writes to the caller's options.

## ADDED Requirements

### Requirement: Option constructors return an error and a nil root on failure
`NewWithOpts(ctx context.Context, opts *InjectorOpts, packages ...func(Injector)) (*RootScope, error)` SHALL return a usable root and a nil error on success, and a nil root and a non-nil error on failure. A nil `opts` SHALL behave like `&InjectorOpts{}`. Without a `ConfigSource`, and with valid `PropertySource`, `ConfigTagKey` and `ConfigValidator` options, construction SHALL never fail.

#### Scenario: Construction outcome
```gherkin
Given <setup>
When the consumer calls doger.NewWithOpts(context.Background(), <opts>)
Then the returned root is <root>
And the returned error is <error>

Examples:
  | setup                                                                     | opts                                     | root    | error                                            |
  | nothing                                                                   | nil                                      | non-nil | nil                                              |
  | nothing                                                                   | &doger.InjectorOpts{}                    | non-nil | nil                                              |
  | a ConfigSource src whose Load returns nil                                 | &doger.InjectorOpts{ConfigSource: src}   | non-nil | nil                                              |
  | a ConfigSource src whose Load returns errSealed = errors.New("sealed")    | &doger.InjectorOpts{ConfigSource: src}   | nil     | an error where errors.Is(err, errSealed) is true |
```

<!-- cases: 1 default: nil opts row -->
<!-- cases: 2 overrides: InjectorOpts rows; every config-specific failure is specified in config-sources (source checks) and config-binding/config-validation (ErrInvalidConfigOption for ConfigTagKey and ConfigValidator) -->
<!-- cases: 3 zero values: nil opts and empty InjectorOpts rows -->
<!-- cases: 4 boundaries: with and without a ConfigSource; success and failure -->
<!-- cases: 5 errors: the failing-Load row; error kinds and texts are config-sources -->
<!-- cases: N/A 6 duplicate: duplicate registrations are "A package panic still panics" -->
<!-- cases: 7 lifecycle: a nil root on failure means nothing to shut down; "Packages run only after the config is loaded" shows no package ran -->
<!-- cases: N/A 8 concurrency: construction holds no process-wide state, so concurrent constructors are independent -->
<!-- cases: 9 panics: "A package panic still panics" -->
<!-- cases: 10 compatibility: BREAKING signature change (a ctx first parameter and an error result), flagged in the proposal; without a ConfigSource the result is always a root and nil -->

### Requirement: The caller's options are never written
No constructor SHALL write to the `*InjectorOpts` it receives: defaults SHALL be applied to a private copy, success or failure, so one options value can be reused. The root SHALL keep its own copy: edits the caller makes to the options after construction SHALL NOT reach the root, and `root.Add*Hook` SHALL append to the root's copy, never to the caller's struct.

#### Scenario: Options unchanged after construction
```gherkin
Given clearEnvPrefix(t, "DOGER_") has removed every inherited DOGER_ variable for the test
And root := doger.New()
And opts := <opts>, with Logf, Clock and every hook list unset, and src a *app.Vault whose Load returns <load>
And before := *opts, a shallow copy taken before the call
When the consumer calls <call>
Then reflect.DeepEqual(before, *opts) is true

Examples:
  | opts                                     | load                  | call                                                   |
  | &doger.InjectorOpts{}                    | nil                   | doger.NewWithOpts(context.Background(), opts)          |
  | &doger.InjectorOpts{ConfigSource: src}   | nil                   | doger.NewWithOpts(context.Background(), opts)          |
  | &doger.InjectorOpts{ConfigSource: src}   | errors.New("sealed")  | doger.NewWithOpts(context.Background(), opts)          |
  | &doger.InjectorOpts{ConfigSource: src}   | nil                   | doger.MustNewWithOpts(context.Background(), opts)      |
  | &doger.InjectorOpts{ConfigSource: src}   | nil                   | root.CloneWithOpts(context.Background(), opts)         |
  | &doger.InjectorOpts{ConfigSource: src}   | errors.New("sealed")  | root.CloneWithOpts(context.Background(), opts)         |
  | &doger.InjectorOpts{ConfigSource: src}   | nil                   | root.MustCloneWithOpts(context.Background(), opts)     |
```

#### Scenario: The root and the caller's options stay apart
```gherkin
Given opts := &doger.InjectorOpts{} and root, err := doger.NewWithOpts(context.Background(), opts) returned a nil err
And hook is a func(*doger.Scope, string) that records the service names it receives
And <after>
When the consumer registers a lazy service "svc" in root
Then <outcome>

Examples:
  | after                                                                                    | outcome                                                                    |
  | root.AddAfterRegistrationHook(hook) was called                                           | hook recorded ["svc"] and opts.HookAfterRegistration is still nil          |
  | opts.HookAfterRegistration = []func(*doger.Scope, string){hook} was assigned afterwards  | hook recorded nothing                                                      |
```

#### Scenario: Reusing one options value
```gherkin
Given opts := &doger.InjectorOpts{ConfigSource: src}, with src implementing doger.LogfSetter and recording its calls, and Logf unset
And src's first Load returns errors.New("sealed") and its second Load returns nil
When the consumer calls doger.NewWithOpts(context.Background(), opts) a second time after the first call failed
Then the second call returns a non-nil root and a nil error
And src recorded ["Load", "Load"], with no "SetLogf" call
```

<!-- cases: 1 default: unset fields stay unset; the empty-options row -->
<!-- cases: 2 overrides: a set ConfigSource is kept as given; DeepEqual is used only on options whose func fields are nil, since non-nil funcs never compare equal -->
<!-- cases: 3 zero values: every unset field; the empty-options row -->
<!-- cases: 4 boundaries: success and failure for NewWithOpts and CloneWithOpts; the Must forms; a failed then a successful construction with one value -->
<!-- cases: N/A 5 errors: no error path of its own -->
<!-- cases: N/A 6 duplicate: not applicable -->
<!-- cases: 7 lifecycle: reuse after failure; edits after construction; Add*Hook after construction -->
<!-- cases: N/A 8 concurrency: the copy is private to one construction -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: 10 compatibility: BEHAVIOR CHANGE, flagged in the proposal and the upgrading note: NewWithOpts used to write Logf, Clock and empty hook lists back into the caller's struct, later edits to the struct reached the root, and root.Add*Hook appended into the caller's struct; no repository code relied on any of it -->

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

### Requirement: The caller's context bounds and cancels construction
`NewWithOpts` and `CloneWithOpts` SHALL derive the context passed to `Load` from their `ctx` parameter, so its values and cancellation reach `Load`; the context is used only during the call and never stored. A nil `ctx` SHALL mean `context.Background()`. `New(packages...)` SHALL behave as `NewWithOpts(context.Background(), nil, packages...)`.

#### Scenario: Context values and nil
```gherkin
Given a ConfigSource src whose Load records ctx.Value(traceKey)
And ctxT1 := context.WithValue(context.Background(), traceKey, "t-1")
And root := doger.New()
When the consumer calls <call>
Then the call returns a non-nil root and a nil error
And the recorded trace value is <trace>

Examples:
  | call                                                                                  | trace |
  | doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src})       | nil   |
  | doger.NewWithOpts(nil, &doger.InjectorOpts{ConfigSource: src})                        | nil   |
  | doger.NewWithOpts(ctxT1, &doger.InjectorOpts{ConfigSource: src})                      | "t-1" |
  | root.CloneWithOpts(nil, &doger.InjectorOpts{ConfigSource: src})                       | nil   |
  | root.CloneWithOpts(ctxT1, &doger.InjectorOpts{ConfigSource: src})                     | "t-1" |
```

#### Scenario: Cancelling construction
```gherkin
Given a ConfigSource src whose Load signals on a channel that it started, then waits on ctx.Done() and returns ctx.Err()
And a context ctx that the test cancels once that signal arrives
And root := doger.New()
When the consumer calls <call>
Then the call returns a nil root
And errors.Is(err, context.Canceled) and errors.Is(err, doger.ErrConfigLoadFailed) are both true

Examples:
  | call                                                                 |
  | doger.NewWithOpts(ctx, &doger.InjectorOpts{ConfigSource: src})       |
  | root.CloneWithOpts(ctx, &doger.InjectorOpts{ConfigSource: src})      |
```

#### Scenario: An already cancelled context
```gherkin
Given a context ctx that is already cancelled
And a ConfigSource src whose Load returns ctx.Err() and counts its calls
And root := doger.New()
When the consumer calls <call>
Then the call returns a nil root
And errors.Is(err, context.Canceled) and errors.Is(err, doger.ErrConfigLoadFailed) are both true
And src.Load was called exactly 1 time

Examples:
  | call                                                                 |
  | doger.NewWithOpts(ctx, &doger.InjectorOpts{ConfigSource: src})       |
  | root.CloneWithOpts(ctx, &doger.InjectorOpts{ConfigSource: src})      |
```

<!-- cases: 1 default: the Background row; New equals NewWithOpts with Background and nil options -->
<!-- cases: 2 overrides: context values pass through, for NewWithOpts and CloneWithOpts; how a caller deadline combines with ConfigTimeout is config-sources "Load runs once under a bounded context" -->
<!-- cases: 3 zero values: nil ctx rows for both constructors -->
<!-- cases: 4 boundaries: cancelled while Load runs, and before construction starts -->
<!-- cases: 5 errors: both cancellation scenarios match ErrConfigLoadFailed and context.Canceled -->
<!-- cases: N/A 6 duplicate: one Load per construction -->
<!-- cases: 7 lifecycle: Load is still called with an already done context, so the source decides; no package runs on failure; the ctx is not kept after the call (config-sources "The Load context ends when the constructor returns") -->
<!-- cases: 8 concurrency: cancellation from another goroutine, synchronized by a channel when Load starts -->
<!-- cases: N/A 9 panics: a panicking Load is config-sources -->
<!-- cases: 10 compatibility: the ctx parameter is part of the one BREAKING change to NewWithOpts and CloneWithOpts, flagged in the proposal; no separate context-taking constructor exists -->

### Requirement: Packages run only after the config is loaded
The constructor SHALL run its packages in order, after the installed `ConfigSource` has loaded. The "DI: injector created" log line SHALL be written after `Load` and before the first package, and only for a container that is returned. When construction fails before the packages, no package SHALL run.

#### Scenario: Load happens before the packages
```gherkin
Given a root Logf recorder that appends "created" to a shared log for each line containing "DI: injector created"
And a ConfigSource src whose Load appends "load" to that log
And packages pkgA and pkgB that append "pkgA" and "pkgB" to that log
When the consumer calls doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src, Logf: recorder}, pkgA, pkgB)
Then the log is ["load", "created", "pkgA", "pkgB"]
```

#### Scenario: A failed construction runs no package
```gherkin
Given a root Logf recorder that records lines
And <setup>
And a package pkgA that appends "pkgA" to a shared log
When the consumer calls doger.NewWithOpts(context.Background(), <opts>, pkgA)
Then the returned root is nil
And the shared log is empty
And no recorded line contains "DI: injector created"

Examples:
  | setup                                                        | opts                                                                          |
  | a ConfigSource src whose Load returns errors.New("sealed")   | &doger.InjectorOpts{ConfigSource: src, Logf: recorder}                        |
```

<!-- cases: 1 default: without a source, packages run as before -->
<!-- cases: N/A 2 overrides: the order is fixed -->
<!-- cases: N/A 3 zero values: no packages is "Construction outcome" -->
<!-- cases: 4 boundaries: two packages show the order; the creation line sits between Load and the first package -->
<!-- cases: 5 errors: the failed-load row; option failures are "A construction rejected by its options runs no package" (group 6, D77) -->
<!-- cases: N/A 6 duplicate: not applicable to ordering -->
<!-- cases: 7 lifecycle: the subject; the creation log line marks a returned container only -->
<!-- cases: N/A 8 concurrency: construction runs on the caller's goroutine -->
<!-- cases: N/A 9 panics: "A package panic still panics" -->
<!-- cases: 10 compatibility: packages still run in the order given; the creation line still precedes them -->

### Requirement: CloneWithOpts returns an error
`(*RootScope).CloneWithOpts(ctx context.Context, opts *InjectorOpts) (*RootScope, error)` SHALL construct the clone like `NewWithOpts(ctx, opts)`, sharing the parent's config only as specified in config-sources. A clone that does not share SHALL rebind and validate every copied config handle against its own view. On failure it SHALL return a nil clone and a non-nil error and leave the parent unchanged. A nil `opts` SHALL behave like `&InjectorOpts{}`.

#### Scenario: A construction rejected by its options runs no package
```gherkin
Given a root Logf recorder that records lines
And <setup>
And a package pkgA that appends "pkgA" to a shared log
When the consumer calls doger.NewWithOpts(context.Background(), <opts>, pkgA)
Then the returned root is nil and errors.Is(err, <sentinel>) is true
And the shared log is empty
And no recorded line contains "DI: injector created"

Examples:
  | setup                                       | opts                                                                       | sentinel                     |
  | a ConfigSource src and a PropertySource ps  | &doger.InjectorOpts{ConfigSource: src, PropertySource: ps, Logf: recorder} | doger.ErrConfigConflict      |
  | nothing                                     | &doger.InjectorOpts{ConfigSource: (*app.Vault)(nil), Logf: recorder}       | doger.ErrInvalidConfigSource |
```

<!-- cases: N/A 1 default: covered by "Load happens before the packages" -->
<!-- cases: N/A 2 overrides: the order is fixed -->
<!-- cases: N/A 3 zero values: a nil options value is "Construction outcome" -->
<!-- cases: 4 boundaries: the option checks run before Load and before any package -->
<!-- cases: 5 errors: the conflict and typed-nil rows -->
<!-- cases: N/A 6 duplicate: not applicable -->
<!-- cases: 7 lifecycle: no creation line for a rejected construction -->
<!-- cases: N/A 8 concurrency: construction runs on the caller's goroutine -->
<!-- cases: N/A 9 panics: option checks do not call user code -->
<!-- cases: N/A 10 compatibility: new options -->

#### Scenario: Cloning with options
```gherkin
Given clearEnvPrefix(t, "DOGER_") has removed every inherited DOGER_ variable for the test
And a root created with InjectorOpts.ConfigSource srcA holding "app.name" = "a"
And a second *app.Vault srcB holding "app.name" = "b", whose Load <load> and counts its calls
When the consumer calls <call>
Then the returned clone is <clone>
And the returned error is <error>
And srcB.Load was called <loads> times
And the root's property "app.name" still reads ("a", true)

Examples:
  | call                                                                              | load                                     | clone                                      | error                                            | loads |
  | root.CloneWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: srcB}) | returns nil                              | non-nil, reading "app.name" as ("b", true) | nil                                              | 1     |
  | root.CloneWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: srcB}) | returns errSealed = errors.New("sealed") | nil                                        | an error where errors.Is(err, errSealed) is true | 1     |
  | root.CloneWithOpts(nil, &doger.InjectorOpts{ConfigSource: srcB})                  | returns nil                              | non-nil, reading "app.name" as ("b", true) | nil                                              | 1     |
  | root.CloneWithOpts(context.Background(), &doger.InjectorOpts{})                   | returns nil                              | non-nil, reading "app.name" as ("", false) | nil                                              | 0     |
  | root.CloneWithOpts(context.Background(), nil)                                     | returns nil                              | non-nil, reading "app.name" as ("", false) | nil                                              | 0     |
```

#### Scenario: Copied config handles follow the clone's source
```gherkin
Given a root created with InjectorOpts.ConfigSource srcA, where srcA is a test watcher source holding "server.port" = "8080"
And doger.BindConfig(root, "server", ServerConfig{Port: 8000}) returned a nil error
When the consumer calls clone, err := root.CloneWithOpts(context.Background(), <opts>)
Then err is <error>
And <clone>
And the root's ConfigValue[ServerConfig].Get().Port is still 8080

Examples:
  | opts                                                                                  | error                                                                                         | clone                                                                       |
  | &doger.InjectorOpts{ConfigSource: srcB}, srcB a test watcher source holding "server.port" = "7070"  | nil                                                                     | the clone's ConfigValue[ServerConfig].Get().Port is 7070                    |
  | &doger.InjectorOpts{}                                                                 | nil                                                                                           | the clone's ConfigValue[ServerConfig].Get().Port is 8000 (the defaults)     |
  | &doger.InjectorOpts{ConfigSource: srcB}, srcB a test watcher source holding "server.port" = "99999" | an error matching doger.ErrInvalidConfig whose text contains "server" and not "99999" | clone is nil                                                                |
  | &doger.InjectorOpts{ConfigSource: srcA}                                               | nil                                                                                           | the clone's ConfigValue[ServerConfig].Get().Port is 8080, shared with the root |
```

<!-- cases: 1 default: sharing on Clone() is "Clone keeps its signature" and config-sources -->
<!-- cases: 2 overrides: a different source per clone; context values and cancellation are "The caller's context bounds and cancels construction" -->
<!-- cases: 3 zero values: nil ctx, empty and nil opts rows; the last two give a clone without a config source, reading env; inherited DOGER_ variables are cleared so the miss is deterministic -->
<!-- cases: 4 boundaries: success, failure, no source -->
<!-- cases: 5 errors: failure row; conflict and typed-nil apply as in NewWithOpts, and are checked before any sharing (config-sources) -->
<!-- cases: N/A 6 duplicate: sharing the parent's own source is config-sources -->
<!-- cases: 7 lifecycle: the parent is unchanged after a failed clone; a clone that does not share rebinds and validates every copied config handle against its own view, and a failure fails CloneWithOpts (D71, "Copied config handles follow the clone's source") -->
<!-- cases: N/A 8 concurrency: as NewWithOpts; a Clone racing a reload commit is config-reload -->
<!-- cases: N/A 9 panics: as NewWithOpts -->
<!-- cases: 10 compatibility: BREAKING CloneWithOpts signature change (a ctx first parameter and an error result), flagged in the proposal -->

### Requirement: Clone keeps its signature
`(*RootScope).Clone() *RootScope` SHALL keep its signature and SHALL NOT fail: the clone always shares its parent's config, whatever the kind of the parent's source, instead of loading it again.

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
<!-- cases: N/A 4 boundaries: one clone; repeated clones share the same way; a non-pointer source is config-sources "Clone shares a source of any kind" -->
<!-- cases: N/A 5 errors: Clone cannot fail: it never loads, so no config error can occur -->
<!-- cases: N/A 6 duplicate: sharing rules are config-sources -->
<!-- cases: 7 lifecycle: clone after load; watcher sharing after Start is config-reload -->
<!-- cases: N/A 8 concurrency: a Clone racing a reload commit is config-reload -->
<!-- cases: N/A 9 panics: Clone does not panic on config -->
<!-- cases: 10 compatibility: signature unchanged -->

### Requirement: Must constructors panic with the construction error
`MustNewWithOpts(ctx context.Context, opts *InjectorOpts, packages ...func(Injector)) *RootScope` and `(*RootScope).MustCloneWithOpts(ctx context.Context, opts *InjectorOpts) *RootScope` SHALL return the root or clone when construction succeeds, and SHALL panic with the construction error itself as the panic value when it fails. A nil `ctx` SHALL mean `context.Background()`.

#### Scenario: Must constructors
```gherkin
Given <setup>
When the consumer calls <call>
Then <outcome>

Examples:
  | setup                                                                     | call                                                                                  | outcome                                                                                                                       |
  | nothing                                                                   | doger.MustNewWithOpts(context.Background(), nil)                                      | a non-nil root is returned                                                                                                    |
  | nothing                                                                   | doger.MustNewWithOpts(nil, nil)                                                       | a non-nil root is returned                                                                                                    |
  | a ConfigSource src whose Load returns errSealed = errors.New("sealed")    | doger.MustNewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src})   | it panics with an error value v where errors.Is(v, errSealed) is true and v.Error() starts with "DI: config: load failed"     |
  | a root built with doger.New()                                             | root.MustCloneWithOpts(context.Background(), nil)                                     | a non-nil clone is returned                                                                                                   |
  | a root built with doger.New(), and src as above                           | root.MustCloneWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src})  | it panics with an error value v where errors.Is(v, doger.ErrConfigLoadFailed) is true                                         |
```

<!-- cases: 1 default: nil opts rows -->
<!-- cases: 2 overrides: the ConfigSource rows -->
<!-- cases: 3 zero values: nil opts rows; a nil ctx row -->
<!-- cases: N/A 4 boundaries: success or failure -->
<!-- cases: 5 errors: the panic value is the error, matchable with errors.Is; its text starts with "DI: " once, never twice -->
<!-- cases: N/A 6 duplicate: as NewWithOpts -->
<!-- cases: N/A 7 lifecycle: as NewWithOpts -->
<!-- cases: N/A 8 concurrency: as NewWithOpts; cancellation is "The caller's context bounds and cancels construction" -->
<!-- cases: 9 panics: the subject -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: DefaultRootScope stays usable
`DefaultRootScope`, built at package initialization with `New()`, SHALL be a usable root with no `ConfigSource`, reading properties from the environment default, and a nil injector SHALL keep meaning `DefaultRootScope`.

#### Scenario: Registering on the default root
```gherkin
Given the test does not run in parallel
And name := "app.svc-" + a random suffix unique to this run, so repeated runs (-count=2) never collide
And a lazy "*app.Svc" registered with doger.ProvideNamed(nil, name, newSvc)
When the consumer calls doger.InvokeNamed[*app.Svc](nil, name)
Then it returns a non-nil *app.Svc and a nil error
```

#### Scenario: The default root reads the environment
```gherkin
Given the test does not run in parallel
And sfx is a random suffix of 12 lowercase letters unique to this run
And t.Setenv("DOGER_TDEFAULT" + strings.ToUpper(sfx) + "_ADDR", "from-env")
And an extension named "tdefault" + sfx whose Extend records x.Properties().Lookup("addr")
When the consumer calls doger.Extend(nil, ext)
Then the recorded result is ("from-env", true)
```

<!-- cases: 1 default: the package-init value itself, not a replacement -->
<!-- cases: N/A 2 overrides: replace DefaultRootScope with a constructed root -->
<!-- cases: 3 zero values: a nil injector means DefaultRootScope -->
<!-- cases: N/A 4-6: no input -->
<!-- cases: N/A 7 lifecycle: unchanged -->
<!-- cases: N/A 8 concurrency: the tests are non-parallel because they touch the global and the environment -->
<!-- cases: N/A 9 panics: New cannot fail -->
<!-- cases: 10 compatibility: the subject; the unique names keep the tests repeatable, at the cost of one leftover registration and one installed extension per run in the test binary's global; the uniquely named DOGER_ variable shows the environment default is in use without reading internal state -->

### Requirement: A package panic still panics
A panic raised by a package, such as a duplicate registration, SHALL propagate out of `New`, `NewWithOpts` and `MustNewWithOpts` unchanged, not be returned as an error.

#### Scenario: Duplicate registration in a package
```gherkin
Given a package pkg that registers a lazy service named "db" twice
And a ConfigSource src whose Load returns nil
When the consumer calls <call>
Then the call panics with an error value whose text is "DI: service `db` has already been declared"

Examples:
  | call                                                                                         |
  | doger.New(pkg)                                                                               |
  | doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src}, pkg)         |
  | doger.MustNewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src}, pkg)     |
```

<!-- cases: N/A 1-4: only the panic path is specified -->
<!-- cases: 5 errors: a panic, not an error -->
<!-- cases: 6 duplicate: the subject -->
<!-- cases: 7 lifecycle: a panic after Load leaves nothing global behind, since no process-wide state is claimed -->
<!-- cases: N/A 8 concurrency: unchanged -->
<!-- cases: 9 panics: the subject; the panic value is an error, as di.go raises it -->
<!-- cases: 10 compatibility: packages behave as before; error-returning packages are a later change -->
