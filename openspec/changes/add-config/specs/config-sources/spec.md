# Spec Delta

## Purpose

Defines the `ConfigSource` contract and how a container installs it through `InjectorOpts.ConfigSource`. The source becomes the container's property source, is loaded once under a bounded context before any package runs, and is shared only with that container's clones. Load failures come back as readable errors that never carry a property value.

## ADDED Requirements

### Requirement: The ConfigSource contract
`ConfigSource` SHALL be `interface{ PropertySource; PropertyKeys; Load(ctx context.Context) error }`, with stdlib types only; `ConfigView` SHALL be the alias `interface{ PropertySource; PropertyKeys }`, and `LogfSetter` (`SetLogf(func(format string, args ...any))`) SHALL be optional. The godoc SHALL state that a source serves one container and its clones, that only the root calls `Load`, and that `Lookup` and `Keys` must be safe for concurrent use, including while `Load` runs.

#### Scenario: Which types are sources
```gherkin
Given a type "*app.Vault" with the methods <methods>
When the consumer checks whether "*app.Vault" implements <target>
Then the result is <result>

Examples:
  | methods                     | target               | result |
  | Lookup, Keys, Load          | doger.ConfigSource   | true   |
  | Lookup, Load                | doger.ConfigSource   | false  |
  | Lookup, Keys                | doger.ConfigSource   | false  |
  | Lookup, Keys, Load          | doger.PropertySource | true   |
  | Lookup, Keys, Load          | doger.PropertyKeys   | true   |
  | Lookup, Keys                | doger.ConfigView     | true   |
  | Lookup                      | doger.ConfigView     | false  |
  | Lookup, Keys, Load, SetLogf | doger.LogfSetter     | true   |
  | Lookup, Keys, Load          | doger.LogfSetter     | false  |
```

<!-- cases: N/A 1 default: a contract has no default; without a source the container reads env (extension-properties) -->
<!-- cases: N/A 2 overrides: a contract, not a behavior -->
<!-- cases: N/A 3 zero values: typed-nil and unset sources are "Option checks happen before anything is called" -->
<!-- cases: 4 boundaries: each missing method; ConfigView needs both read methods -->
<!-- cases: N/A 5 errors: Lookup and Keys report misses; Load errors are "A failed load fails construction" -->
<!-- cases: N/A 6 duplicate: sharing a source outside clones is documented as unsupported, not policed (no process-wide state) -->
<!-- cases: N/A 7 lifecycle: loading is its own requirement; a direct Load call by the application is documented as unsupported, since the root alone calls it -->
<!-- cases: N/A 8 concurrency: a documentation obligation on sources; config.New meets it (config-files) -->
<!-- cases: N/A 9 panics: a contract -->
<!-- cases: N/A 10 compatibility: new API; PropertySource is unchanged; ConfigView is an alias, so any type with Lookup and Keys satisfies it -->

### Requirement: The installed source is the container's property source
When `InjectorOpts.ConfigSource` is set, its committed view SHALL be the container's property source from construction on, before any package or extension reads a property. A `PropertySource` service registered in the root and the environment default SHALL then never be consulted, and the source SHALL never be registered as a service.

#### Scenario: Property reads go to the installed source
```gherkin
Given a ConfigSource src holding "redis.addr" = "from-config"
And the environment variable "DOGER_REDIS_ADDR" is set to "from-env"
And a package pkg that registers a lazy doger.PropertySource (a doger.MapSource) with "redis.addr" = "from-service" and installs an extension "redis" whose Extend and whose "*redis.Client" provider record <read>
And root, err := doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src}, pkg) returned a nil err
When the consumer invokes "*redis.Client" from root
Then the recorded result is ("from-config", true)

Examples:
  | read                                                                  |
  | x.Properties().Lookup("addr")                                         |
  | p, _ := doger.ExtensionProperties(i), then p.Lookup("addr")           |
```

#### Scenario: The installed source is not a service
```gherkin
Given a ConfigSource src of type "*app.Vault"
And root, err := doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src}) returned a nil err, with no package
When the consumer calls root.ListProvidedServices()
Then the result holds no service named doger.NameOf[doger.PropertySource](), doger.NameOf[doger.ConfigSource]() or doger.NameOf[*app.Vault]()
```

<!-- cases: 1 default: without a ConfigSource the selection rules of extension-properties apply unchanged -->
<!-- cases: 2 overrides: the installed source beats a registered PropertySource service and env -->
<!-- cases: N/A 3 zero values: a nil ConfigSource means unset -->
<!-- cases: 4 boundaries: a read during construction (Extend inside a package) and a read from a provider afterwards -->
<!-- cases: 5 errors: setting PropertySource too is "Option checks happen before anything is called" -->
<!-- cases: 6 duplicate: one property source per container; the source never occupies a service name, so it cannot collide with an application's registration -->
<!-- cases: 7 lifecycle: the Extend read happens during construction, after Load; after a reload, reads see the newly committed view (config-reload) -->
<!-- cases: N/A 8 concurrency: covered by extension-properties "Concurrent lookups" -->
<!-- cases: N/A 9 panics: reads do not panic -->
<!-- cases: 10 compatibility: containers without a ConfigSource choose their source as before -->

### Requirement: Option checks happen before anything is called
Before calling any method of either source, the constructor SHALL check, in order: a `ConfigSource` holding a nil pointer, map, func, chan or slice fails with `ErrInvalidConfigSource`; an `EnvPropertySource` in `InjectorOpts.PropertySource` whose prefix stays invalid after normalization fails with `ErrInvalidConfigSource`; a valid `ConfigSource` together with a `PropertySource` fails with `ErrConfigConflict`. An unset `ConfigSource` means no source.

#### Scenario: Invalid option combinations
```gherkin
Given a ConfigSource src of type "*app.Vault" whose methods each count their calls, and a PropertySource ps
When the consumer calls doger.NewWithOpts(context.Background(), <opts>)
Then the returned root is <root>
And the returned error is <error>
And no method was called on src or on the typed-nil value

Examples:
  | opts                                                                         | root    | error                                                                                                         |
  | &doger.InjectorOpts{ConfigSource: nil}                                       | non-nil | nil                                                                                                           |
  | &doger.InjectorOpts{ConfigSource: (*app.Vault)(nil)}                         | nil     | errors.Is(err, doger.ErrInvalidConfigSource), text "DI: config: invalid config source: nil *app.Vault"       |
  | &doger.InjectorOpts{ConfigSource: app.MapVault(nil)}                         | nil     | errors.Is(err, doger.ErrInvalidConfigSource), text "DI: config: invalid config source: nil app.MapVault"     |
  | &doger.InjectorOpts{ConfigSource: app.FuncVault(nil)}                        | nil     | errors.Is(err, doger.ErrInvalidConfigSource), text "DI: config: invalid config source: nil app.FuncVault"    |
  | &doger.InjectorOpts{ConfigSource: (*app.Vault)(nil), PropertySource: ps}     | nil     | errors.Is(err, doger.ErrInvalidConfigSource), text "DI: config: invalid config source: nil *app.Vault"       |
  | &doger.InjectorOpts{PropertySource: doger.EnvPropertySource("my$app")}       | nil     | errors.Is(err, doger.ErrInvalidConfigSource), text "DI: config: invalid env prefix \"my$app\""               |
  | &doger.InjectorOpts{PropertySource: doger.EnvPropertySource("my-app")}       | non-nil | nil                                                                                                           |
  | &doger.InjectorOpts{ConfigSource: src, PropertySource: ps}                   | nil     | errors.Is(err, doger.ErrConfigConflict), text "DI: config: both PropertySource and ConfigSource are set"     |
```

<!-- cases: 1 default: the unset row -->
<!-- cases: 2 overrides: either field alone is valid; the conflict row; the ConfigTagKey and ConfigValidator checks (ErrInvalidConfigOption) are config-binding and config-validation -->
<!-- cases: 3 zero values: nil interface, and typed nil of pointer, map and func kinds (chan and slice follow the same rule) -->
<!-- cases: 4 boundaries: the check order when two problems occur (typed nil plus PropertySource); "my-app" normalizes to "MY_APP_" and is valid -->
<!-- cases: 5 errors: exact texts and errors.Is matching; type names use reflect.Type.String() -->
<!-- cases: N/A 6 duplicate: not applicable -->
<!-- cases: 7 lifecycle: rejected before SetLogf and Load; a nil opts is replaced by an empty one before the options are copied -->
<!-- cases: N/A 8 concurrency: no runtime state -->
<!-- cases: 9 panics: a typed-nil source is rejected instead of panicking in Load; the env prefix is not a secret, so it may be quoted -->
<!-- cases: N/A 10 compatibility: new API; an invalid EnvPropertySource prefix used outside InjectorOpts is extension-properties -->

### Requirement: The source's logger is set only when the user chose one
When the installed source implements `LogfSetter` and the caller's `InjectorOpts.Logf` was non-nil, the constructor SHALL call `SetLogf` with it once, before `Load`. Otherwise it SHALL NOT call `SetLogf`, so the source keeps its own default logging. Whether `Logf` was set SHALL be judged on the caller's value, before defaults apply.

#### Scenario: SetLogf and Load order
```gherkin
Given a ConfigSource src that appends each call ("SetLogf", "Load") to a call log, and implements doger.LogfSetter <logger>
When the consumer calls doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src, Logf: <logf>})
Then the call log is <log>

Examples:
  | logger | logf     | log                 |
  | yes    | recorder | ["SetLogf", "Load"] |
  | yes    | nil      | ["Load"]            |
  | no     | recorder | ["Load"]            |
  | no     | nil      | ["Load"]            |
```

#### Scenario: The logger reaches the source
```gherkin
Given a root Logf recorder that records lines
And a ConfigSource src implementing doger.LogfSetter that in Load writes "loaded 3 keys" through the logf it received
When the consumer calls doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src, Logf: recorder})
Then the recorder holds a line containing "loaded 3 keys"
```

<!-- cases: 1 default: the nil-logf rows leave the source's own default (slog.Default for config.New) in place -->
<!-- cases: 2 overrides: the recorder rows -->
<!-- cases: 3 zero values: nil Logf rows; reuse of one options value is container-construction "Reusing one options value" -->
<!-- cases: 4 boundaries: logger implemented or not, Logf set or not -->
<!-- cases: N/A 5 errors: SetLogf returns nothing -->
<!-- cases: 6 duplicate: SetLogf is called at most once per load; a clone sharing the source does not call it again (config-sources "A clone shares its parent's source only when it is the same pointer") -->
<!-- cases: 7 lifecycle: SetLogf before Load -->
<!-- cases: N/A 8 concurrency: runs on the constructor's goroutine -->
<!-- cases: N/A 9 panics: a panicking SetLogf propagates like a package panic; not specified further -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Load runs once under a bounded context
The constructor SHALL call `Load` exactly once. With the real clock, which is an unset `InjectorOpts.Clock` or one whose dynamic type is that of `clockwork.NewRealClock()`, its context SHALL be `context.WithTimeout(ctx, ConfigTimeout)` of the caller's context, so the earlier deadline wins. A zero `ConfigTimeout` SHALL mean `DefaultConfigTimeout` (10s); a negative one SHALL add no deadline of its own.

#### Scenario: The Load deadline on the real clock
```gherkin
Given the test runs inside a testing/synctest bubble, whose clock starts at 2000-01-01T00:00:00Z
And a ConfigSource src whose Load records ctx.Deadline()
And a caller context ctx with <caller deadline>
When the consumer calls doger.NewWithOpts(ctx, &doger.InjectorOpts{ConfigSource: src, ConfigTimeout: <timeout>, Clock: <clock>})
Then the recorded deadline is <deadline>

Examples:
  | caller deadline | timeout | clock                      | deadline                      |
  | none            | 0       | nil                        | (2000-01-01T00:00:10Z, true)  |
  | none            | 2s      | nil                        | (2000-01-01T00:00:02Z, true)  |
  | none            | -1ns    | nil                        | (zero time, false)            |
  | +1s             | 10s     | nil                        | (2000-01-01T00:00:01Z, true)  |
  | +1m             | 2s      | nil                        | (2000-01-01T00:00:02Z, true)  |
  | +1m             | -1ns    | nil                        | (2000-01-01T00:01:00Z, true)  |
  | none            | 2s      | clockwork.NewRealClock()   | (2000-01-01T00:00:02Z, true)  |
```

#### Scenario: A Load that honors its deadline
```gherkin
Given the test runs inside a testing/synctest bubble
And a ConfigSource src whose Load waits on ctx.Done() and returns ctx.Err()
When the consumer calls doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src, ConfigTimeout: 2s})
Then the call returns after 2s of bubble time with a nil root
And errors.Is(err, context.DeadlineExceeded) and errors.Is(err, doger.ErrConfigLoadFailed) are both true
```

#### Scenario: A Load that ignores its deadline
```gherkin
Given the test runs inside a testing/synctest bubble
And a ConfigSource src whose Load sleeps 5s, ignoring ctx, and returns nil
When the consumer calls doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src, ConfigTimeout: 2s})
Then the call returns after 5s of bubble time with a non-nil root and a nil error
```

<!-- cases: 1 default: the 0 row is DefaultConfigTimeout -->
<!-- cases: 2 overrides: ConfigTimeout rows and caller deadlines; the earlier of the two always wins; a caller-supplied real clock counts as the real clock -->
<!-- cases: 3 zero values: the 0 row; a nil Clock -->
<!-- cases: 4 boundaries: caller deadline before and after now+ConfigTimeout; -1ns is the smallest negative -->
<!-- cases: 5 errors: the honoring scenario; other Load failures are "A failed load fails construction" -->
<!-- cases: N/A 6 duplicate: Load is called exactly once -->
<!-- cases: 7 lifecycle: the constructor waits for Load to return; cancellation is cooperative, so a Load that ignores ctx and succeeds is a success (documented in godoc) -->
<!-- cases: 8 concurrency: deterministic through synctest bubble time -->
<!-- cases: N/A 9 panics: a panicking Load is "A failed load fails construction" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A user clock bounds Load in its own time base
When `InjectorOpts.Clock` is a user clock (non-nil, and not of the real clock's type), the `Load` context SHALL be done when the caller's context is done or `ConfigTimeout` elapses on that clock. `Deadline()` SHALL return the caller's deadline (`ok` false if none), `Err` SHALL never block, and, for a caller context from the context package, neither creating it nor deriving children from it SHALL start a goroutine.

#### Scenario: Deadline and Err on a fake clock
```gherkin
Given InjectorOpts.Clock set to a fake clock at 2026-01-01T00:00:00Z and ConfigTimeout <timeout>
And a caller context ctx with <caller deadline>
And a ConfigSource src whose Load records ctx.Deadline() and ctx.Err(), and derives context.WithCancel(ctx) and cancels it
When the consumer calls doger.NewWithOpts(ctx, &doger.InjectorOpts{ConfigSource: src, ConfigTimeout: <timeout>, Clock: clock})
Then the recorded deadline is <deadline>
And the recorded Err is nil, returned without blocking
And goleak, with its default retries, finds no goroutine started during the call once it has returned

Examples:
  | timeout | caller deadline                                   | deadline                     |
  | 0       | none                                              | (zero time, false)           |
  | 2s      | none                                              | (zero time, false)           |
  | -1ns    | none                                              | (zero time, false)           |
  | 2s      | context.WithDeadline at 2030-01-01T00:00:00Z      | (2030-01-01T00:00:00Z, true) |
```

#### Scenario: Which side ends the Load context
```gherkin
Given InjectorOpts.Clock set to a fake clock and ConfigTimeout 2s
And a ConfigSource src whose Load signals that it started, then waits on ctx.Done() and returns ctx.Err()
When <event> after Load started
Then doger.NewWithOpts(ctx, ...) returns a nil root
And errors.Is(err, <cause>) and errors.Is(err, doger.ErrConfigLoadFailed) are both true

Examples:
  | event                                                    | cause                    |
  | the test advances the fake clock by 2s                   | context.DeadlineExceeded |
  | the test cancels the caller's context                    | context.Canceled         |
```

#### Scenario: A caller deadline ends Load while the user clock stands still
```gherkin
Given the test runs inside a testing/synctest bubble
And InjectorOpts.Clock set to a fake clock that the test never advances, and ConfigTimeout 10s
And a caller context ctx from context.WithTimeout(context.Background(), 1s)
And a ConfigSource src whose Load waits on ctx.Done() and returns ctx.Err()
When the consumer calls doger.NewWithOpts(ctx, &doger.InjectorOpts{ConfigSource: src, ConfigTimeout: 10s, Clock: clock})
Then the call returns after 1s of bubble time with a nil root
And errors.Is(err, context.DeadlineExceeded) and errors.Is(err, doger.ErrConfigLoadFailed) are both true
```

#### Scenario: A child timeout inside Load fires with a user clock
```gherkin
Given the test runs inside a testing/synctest bubble
And InjectorOpts.Clock set to a fake clock that the test never advances, and ConfigTimeout 10s
And a ConfigSource src whose Load derives child, cancel := context.WithTimeout(ctx, 1s), waits on child.Done(), records child.Err() and returns nil
When the consumer calls doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src, ConfigTimeout: 10s, Clock: clock})
Then the call returns after 1s of bubble time with a non-nil root and a nil error
And the recorded child Err is context.DeadlineExceeded
```

<!-- cases: 1 default: the 0 row is DefaultConfigTimeout on the clock -->
<!-- cases: 2 overrides: ConfigTimeout rows; the caller's cancellation and deadline still apply -->
<!-- cases: 3 zero values: the 0 row; no caller deadline gives (zero time, false) -->
<!-- cases: 4 boundaries: the clock fires exactly at +2s; a negative timeout adds no clock bound; a caller deadline with a clock that never advances -->
<!-- cases: 5 errors: DeadlineExceeded from the clock or the caller's deadline, Canceled from the caller -->
<!-- cases: N/A 6 duplicate: Load is called once -->
<!-- cases: 7 lifecycle: Deadline() reports only the caller's wall deadline, so stdlib children (WithTimeout, net dials) see a wall time; the clock bound is carried by Done and Err alone (documented) -->
<!-- cases: 8 concurrency: no goroutine is started by the context or its children for a parent from the context package (the context implements AfterFunc); a parent of another kind may cost one goroutine inside the stdlib, documented -->
<!-- cases: N/A 9 panics: as "A failed load fails construction" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: The Load context ends when the constructor returns
The context passed to `Load` SHALL be done, with `Err` returning `context.Canceled` unless it already ended, once the constructor returns, so a source cannot keep using it. For a caller context from the context package, construction SHALL leave no goroutine running that the library started.

#### Scenario: A source that keeps its Load context
```gherkin
Given a ConfigSource src whose Load stores its ctx and returns nil
And InjectorOpts.Clock is <clock>
When the consumer calls doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src, Clock: <clock>})
Then after the call returns, the stored ctx.Done() is closed and ctx.Err() is context.Canceled
And goleak, with its default retries, finds no goroutine started during the call

Examples:
  | clock                      |
  | nil (the real clock)       |
  | a fake clock               |
```

<!-- cases: 1 default: the real clock row -->
<!-- cases: 2 overrides: a user clock row -->
<!-- cases: N/A 3 zero values: covered by the default row -->
<!-- cases: N/A 4 boundaries: a context already ended by its deadline keeps DeadlineExceeded -->
<!-- cases: N/A 5 errors: none of its own -->
<!-- cases: N/A 6 duplicate: one context per construction -->
<!-- cases: 7 lifecycle: the subject -->
<!-- cases: 8 concurrency: no goroutine the library started outlives the constructor (health-check-execution delta); goroutines a source starts itself are the source's own -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A failed load fails construction
When `Load` returns an error or panics, the constructor SHALL return a nil root and a `*ConfigLoadError{SourceType reflect.Type; Err error}` matching `ErrConfigLoadFailed`, whose `Unwrap` returns the cause so `errors.Is` and `errors.As` reach it. A panic SHALL be recovered; an error panic value SHALL be the cause, and its text SHALL be shown only when it is value-free; any other value SHALL be described by type only.

#### Scenario: Load errors and panics
```gherkin
Given a ConfigSource src of type "*app.Vault" whose Load <fails>
When the consumer calls doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src})
Then the returned root is nil
And errors.Is(err, doger.ErrConfigLoadFailed) is true and errors.As(err, &loadErr) gives loadErr.SourceType.String() == "*app.Vault"
And err.Error() is "<text>"
And <match>

Examples:
  | fails                                                                   | text                                                                                   | match                                                     |
  | returns errSealed = &app.SealedError{Token: "s3cret-9f2c71d04ab8"}      | DI: config: load failed: source *app.Vault: *app.SealedError                           | errors.Is(err, errSealed) is true                         |
  | returns context.DeadlineExceeded                                        | DI: config: load failed: source *app.Vault: context deadline exceeded                  | errors.Is(err, context.DeadlineExceeded) is true          |
  | returns fmt.Errorf("vault: %w", context.Canceled)                        | DI: config: load failed: source *app.Vault: context canceled                           | errors.Is(err, context.Canceled) is true                  |
  | panics with "token s3cret-9f2c71d04ab8 rejected"                        | DI: config: load failed: source *app.Vault: panic of type string                       | err.Error() does not contain "s3cret-9f2c71d04ab8"        |
  | panics with errPanic = errors.New("token s3cret-9f2c71d04ab8 rejected") | DI: config: load failed: source *app.Vault: panic of type *errors.errorString          | errors.Is(err, errPanic) is true                          |
  | panics with errVF = &app.ParseErr{File: "config.yaml", Line: 3}         | DI: config: load failed: source *app.Vault: config.yaml:3: syntax error                | errors.Is(err, errVF) is true                             |
  | panics with nil                                                         | DI: config: load failed: source *app.Vault: panic of type *runtime.PanicNilError       | errors.As(err, &pn) is true for pn *runtime.PanicNilError |
```

<!-- cases: N/A 1 default, 2 overrides: failure handling is fixed -->
<!-- cases: N/A 3 zero values: a nil error from Load is success -->
<!-- cases: 4 boundaries: typed error, context errors (direct and wrapped), string panic, error panic, value-free error panic (app.ParseErr implements ValueFree() bool and returns true), panic(nil) -->
<!-- cases: 5 errors: the subject; matchable with errors.Is on ErrConfigLoadFailed and on the cause; errors.As on *ConfigLoadError; exact texts pinned -->
<!-- cases: N/A 6 duplicate: not applicable -->
<!-- cases: 7 lifecycle: no package runs (container-construction) -->
<!-- cases: N/A 8 concurrency: Load runs on the constructor's goroutine -->
<!-- cases: 9 panics: panic rows; a panic's text is shown only when the panic value is a value-free error, since any other text may hold a value -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Value-free error text is shown
`ValueFreeError` SHALL be `interface{ error; ValueFree() bool }`: an error reporting true promises its text holds no property value. In a load-failed message the root SHALL show the text of `context.Canceled` or `context.DeadlineExceeded` when the chain holds one; otherwise the text of the first error in the chain whose `ValueFree()` reports true; otherwise the cause's type only.

#### Scenario: Which text a load error shows
```gherkin
Given a ConfigSource src of type "*app.Vault" whose Load returns <cause>
When the consumer calls doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src})
Then err.Error() is "<text>"

Examples:
  | cause                                                                                                         | text                                                                         |
  | &app.ParseErr{File: "config.yaml", Line: 3} (ValueFree() reports true; text "config.yaml:3: syntax error")      | DI: config: load failed: source *app.Vault: config.yaml:3: syntax error      |
  | fmt.Errorf("layer vault: %w", &app.ParseErr{File: "config.yaml", Line: 3})                                     | DI: config: load failed: source *app.Vault: config.yaml:3: syntax error      |
  | &app.MaybeErr{Free: false, Err: &app.ParseErr{File: "config.yaml", Line: 3}} (ValueFree() reports false)       | DI: config: load failed: source *app.Vault: config.yaml:3: syntax error      |
  | &app.MaybeErr{Free: false, Err: errors.New("token s3cret-9f2c71d04ab8")} (ValueFree() reports false)           | DI: config: load failed: source *app.Vault: *app.MaybeErr                    |
  | errors.Join(&app.ParseErr{File: "config.yaml", Line: 3}, context.DeadlineExceeded)                             | DI: config: load failed: source *app.Vault: context deadline exceeded        |
  | &app.SealedError{Token: "s3cret-9f2c71d04ab8"} (does not implement ValueFreeError)                             | DI: config: load failed: source *app.Vault: *app.SealedError                 |
  | errors.New("token s3cret-9f2c71d04ab8")                                                                        | DI: config: load failed: source *app.Vault: *errors.errorString              |
```

#### Scenario: Which errors are value-free
```gherkin
Given err := <err>
When the consumer checks vf, ok := errors.AsType[doger.ValueFreeError](err) and, when ok, calls vf.ValueFree()
Then the result is <result>

Examples:
  | err                                                                     | result              |
  | doger.ErrConfigLoadFailed                                               | ok, true            |
  | doger.ErrInvalidConfigSource                                            | ok, true            |
  | doger.ErrConfigConflict                                                 | ok, true            |
  | &doger.ConfigLoadError{SourceType: reflect.TypeFor[*app.Vault](), Err: errors.New("token s3cret-9f2c71d04ab8")} | ok, true |
  | (*doger.ConfigLoadError)(nil)                                           | ok, true, no panic  |
  | doger.ErrServiceNotFound                                                | not ok              |
  | &doger.StartError{}                                                     | not ok              |
```

<!-- cases: 1 default: an unmarked error is shown by type -->
<!-- cases: 2 overrides: a source opts in per error, by implementing ValueFree() bool; a wrapper may answer false per instance and the walk continues to its cause -->
<!-- cases: 3 zero values: a nil *ConfigLoadError answers without panicking (pointer receivers, nil-safe); a nil cause is success -->
<!-- cases: 4 boundaries: a marked error directly and wrapped once; a marker reporting false over a value-free and over an unmarked cause; a context error beside a value-free one; an unmarked typed and untyped error -->
<!-- cases: N/A 5 errors: the subject is the text, matching is "A failed load fails construction" -->
<!-- cases: 6 duplicate: a context error wins over any other value-free error in the same chain; otherwise the first value-free error in the chain wins -->
<!-- cases: N/A 7 lifecycle, 8 concurrency: pure formatting -->
<!-- cases: N/A 9 panics: panics are described by type unless the panic value is a value-free error -->
<!-- cases: 10 compatibility: new API; only the config sentinels and the config error types (*ConfigLoadError, *ConfigBindError, *ConfigValidationError) implement the marker; StartError, ExtensionError, ShutdownReport and the other sentinels keep their texts and do not; config.New errors implement it (config-files) -->

### Requirement: A clone shares its parent's source only when it is the same pointer
`Clone()` SHALL always share the parent's loaded source and committed config, whatever the source's kind. `CloneWithOpts` SHALL share them only for a `ConfigSource` of pointer kind equal to the parent's, after its option checks pass; sharing calls neither `Load` nor `SetLogf`. Any other source SHALL be loaded for the clone, and no source means none. Comparison SHALL never panic.

#### Scenario: Sharing on clone
```gherkin
Given clearEnvPrefix(t, "DOGER_") has removed every inherited DOGER_ variable for the test
And root, err := doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src, Logf: recorder}) succeeded, with src a *app.Vault holding "app.name" = "a" and counting its Load and SetLogf calls
When the consumer calls <call>
Then the clone is non-nil and the error, if any, is nil
And the clone's property "app.name" reads <read>
And src.Load and src.SetLogf were each called exactly 1 time

Examples:
  | call                                                                                          | read        |
  | root.Clone()                                                                                  | ("a", true) |
  | root.CloneWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src})              | ("a", true) |
  | root.CloneWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src, Logf: other}) | ("a", true) |
  | root.CloneWithOpts(context.Background(), &doger.InjectorOpts{})                               | ("", false) |
```

#### Scenario: A non-pointer source on clone
```gherkin
Given a source type app.ValueVault, a struct with a map field (so == on it would panic), holding "app.name" = "a", whose Load counts calls on a shared counter
And root, err := doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: v}) succeeded for a value v of that type
When the consumer calls <call>
Then the call does not panic and returns a non-nil clone and, if any, a nil error
And the clone's property "app.name" reads ("a", true)
And the shared Load counter is <loads>

Examples:
  | call                                                                             | loads |
  | root.Clone()                                                                     | 1     |
  | root.CloneWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: v})   | 2     |
```

#### Scenario: Option checks run before sharing
```gherkin
Given root, err := doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src}) succeeded, with src a *app.Vault counting its Load calls
And a PropertySource ps
When the consumer calls root.CloneWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src, PropertySource: ps})
Then the returned clone is nil
And errors.Is(err, doger.ErrConfigConflict) is true
And src.Load was called exactly 1 time
```

<!-- cases: 1 default: Clone rows -->
<!-- cases: 2 overrides: a different source per clone is container-construction "Cloning with options"; a different Logf with the same source does not call SetLogf again -->
<!-- cases: 3 zero values: the empty-opts row gives a clone without a source, reading env with DOGER_ cleared -->
<!-- cases: 4 boundaries: Clone, same pointer, same pointer with new Logf, no source, non-pointer value through Clone and through CloneWithOpts -->
<!-- cases: 5 errors: sharing cannot fail; the conflict check runs before sharing; a loaded clone fails as NewWithOpts -->
<!-- cases: 6 duplicate: the subject; sharing outside clones is undefined and documented; the godoc notes that two pointers to a zero-size type may compare equal -->
<!-- cases: 7 lifecycle: watcher sharing after Start, and a Clone racing a reload commit, are config-reload -->
<!-- cases: N/A 8 concurrency: as Clone -->
<!-- cases: 9 panics: comparing a non-comparable value never panics, because only pointer kinds are compared -->
<!-- cases: 10 compatibility: Clone keeps its signature and cannot fail, whatever the source's kind -->

### Requirement: Values never appear in the library's errors or logs
Text the library adds to config errors and log lines SHALL contain only key names, type names, rule names and the text of value-free errors, never a property value. The `GoString` of its error types SHALL be type-only, so `%#v` leaks nothing. A source's error SHALL stay reachable with `errors.Is` and `errors.As`.

#### Scenario: Secrets stay out of construction errors and logs
```gherkin
Given a root Logf recorder that records lines
And a ConfigSource src of type "*app.Vault" holding "db.password" = "s3cret-9f2c71d04ab8", whose Load returns errSecret = app.SecretError{Token: "s3cret-9f2c71d04ab8"}, a value type whose Error text holds the token
When the consumer calls doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: src, Logf: recorder})
Then err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err) and fmt.Sprintf("%#v", err) do not contain "s3cret-9f2c71d04ab8"
And no recorded log line contains "s3cret-9f2c71d04ab8"
And errors.Is(err, errSecret) is true
```

<!-- cases: N/A 1 default, 2 overrides, 3 zero values, 4 boundaries: secrecy has no input beyond the value -->
<!-- cases: 5 errors: the library's text names types only, while the cause stays reachable; the claim covers text the library adds, not the cause's own Error() -->
<!-- cases: N/A 6 duplicate, 7 lifecycle, 8 concurrency: unchanged by state -->
<!-- cases: 9 panics: covered by "A failed load fails construction" panic rows -->
<!-- cases: 10 compatibility: the same type-only policy extension-properties uses for a failing source; the secret is a high-entropy string, so an accidental match is impossible -->
