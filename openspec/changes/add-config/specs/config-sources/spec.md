# Spec Delta

## Purpose

Defines the `ConfigSource` contract and how a container installs it through `InjectorOpts.ConfigSource`. The source becomes the container's property source, is loaded once under a bounded context before any package runs, and is shared only with that container's clones. Load failures come back as readable errors that never carry a property value.

## ADDED Requirements

### Requirement: The ConfigSource contract
`ConfigSource` SHALL be `interface{ PropertySource; PropertyKeys; Load(ctx context.Context) error }`, with stdlib types only; its `Keys` SHALL follow `PropertyKeys` (extension-properties). `ConfigLogger` (`SetLogf(func(format string, args ...any))`) SHALL be optional, detected on the installed source. The godoc SHALL state that a source serves one container and its clones, and that `Lookup` and `Keys` must be safe for concurrent use, including while `Load` runs.

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
  | Lookup, Keys, Load, SetLogf | doger.ConfigLogger   | true   |
  | Lookup, Keys, Load          | doger.ConfigLogger   | false  |
```

<!-- cases: N/A 1 default: a contract has no default; without a source the container reads env (extension-properties) -->
<!-- cases: N/A 2 overrides: a contract, not a behavior -->
<!-- cases: N/A 3 zero values: typed-nil and unset sources are "Option checks happen before anything is called" -->
<!-- cases: 4 boundaries: each missing method -->
<!-- cases: N/A 5 errors: Lookup and Keys report misses; Load errors are "A failed load fails construction" -->
<!-- cases: N/A 6 duplicate: sharing a source outside clones is documented as unsupported, not policed (no process-wide state) -->
<!-- cases: N/A 7 lifecycle: loading is its own requirement -->
<!-- cases: N/A 8 concurrency: a documentation obligation on sources; config.Files meets it (config-files) -->
<!-- cases: N/A 9 panics: a contract -->
<!-- cases: N/A 10 compatibility: new API; PropertySource is unchanged -->

### Requirement: The installed source is the container's property source
When `InjectorOpts.ConfigSource` is set, it SHALL be the container's property source from construction on, before any package or extension reads a property. A `PropertySource` service registered in the root and the environment default SHALL then never be consulted.

#### Scenario: Property reads go to the installed source
```gherkin
Given a ConfigSource src holding "redis.addr" = "from-config"
And the environment variable "DOGER_REDIS_ADDR" is set to "from-env"
And a package pkg that registers a lazy doger.PropertySource (a doger.MapSource) with "redis.addr" = "from-service" and installs an extension "redis" whose Extend and whose "*redis.Client" provider record <read>
And root, err := doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src}, pkg) returned a nil err
When the consumer invokes "*redis.Client" from root
Then the recorded result is ("from-config", true)

Examples:
  | read                                        |
  | x.Properties().Lookup("addr")               |
  | doger.ExtensionProperties(i).Lookup("addr") |
```

<!-- cases: 1 default: without a ConfigSource the selection rules of extension-properties apply unchanged -->
<!-- cases: 2 overrides: the installed source beats a registered PropertySource service and env -->
<!-- cases: N/A 3 zero values: a nil ConfigSource means unset -->
<!-- cases: 4 boundaries: a read during construction (Extend inside a package) and a read from a provider afterwards -->
<!-- cases: 5 errors: setting PropertySource too is "Option checks happen before anything is called" -->
<!-- cases: N/A 6 duplicate: one property source per container -->
<!-- cases: 7 lifecycle: the Extend read happens during construction, after Load -->
<!-- cases: N/A 8 concurrency: covered by extension-properties "Concurrent lookups" -->
<!-- cases: N/A 9 panics: reads do not panic -->
<!-- cases: 10 compatibility: containers without a ConfigSource choose their source as before -->

### Requirement: Option checks happen before anything is called
Before calling any method of either source, the constructor SHALL check, in order: a `ConfigSource` holding a nil pointer, map, func, chan or slice fails with `ErrInvalidConfigSource`; an `EnvPropertySource` in `InjectorOpts.PropertySource` whose prefix stays invalid after normalization fails with `ErrInvalidConfigSource`; a valid `ConfigSource` together with a `PropertySource` fails with `ErrConfigConflict`. An unset `ConfigSource` means no source.

#### Scenario: Invalid option combinations
```gherkin
Given a ConfigSource src of type "*app.Vault" whose methods each count their calls, and a PropertySource ps
When the consumer calls doger.NewWithOpts(<opts>)
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
<!-- cases: 2 overrides: either field alone is valid; the conflict row -->
<!-- cases: 3 zero values: nil interface, and typed nil of pointer, map and func kinds (chan and slice follow the same rule) -->
<!-- cases: 4 boundaries: the check order when two problems occur (typed nil plus PropertySource); "my-app" normalizes to "MY_APP_" and is valid -->
<!-- cases: 5 errors: exact texts and errors.Is matching; type names use reflect.Type.String() -->
<!-- cases: N/A 6 duplicate: not applicable -->
<!-- cases: 7 lifecycle: rejected before SetLogf and Load -->
<!-- cases: N/A 8 concurrency: no runtime state -->
<!-- cases: 9 panics: a typed-nil source is rejected instead of panicking in Load; the env prefix is not a secret, so it may be quoted -->
<!-- cases: N/A 10 compatibility: new API; an invalid EnvPropertySource prefix used outside InjectorOpts is extension-properties -->

### Requirement: The source's logger is set only when the user chose one
When the installed source implements `ConfigLogger` and the caller's `InjectorOpts.Logf` was non-nil, the constructor SHALL call `SetLogf` with it once, before `Load`. Otherwise it SHALL NOT call `SetLogf`, so the source keeps its own default logging. Whether `Logf` was set SHALL be judged on the caller's value, before defaults apply.

#### Scenario: SetLogf and Load order
```gherkin
Given a ConfigSource src that appends each call ("SetLogf", "Load") to a call log, and implements doger.ConfigLogger <logger>
When the consumer calls doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src, Logf: <logf>})
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
And a ConfigSource src implementing doger.ConfigLogger that in Load writes "loaded 3 keys" through the logf it received
When the consumer calls doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src, Logf: recorder})
Then the recorder holds a line containing "loaded 3 keys"
```

<!-- cases: 1 default: the nil-logf rows leave the source's own default (slog.Default for config.Files) in place -->
<!-- cases: 2 overrides: the recorder rows -->
<!-- cases: 3 zero values: nil Logf rows; reuse of one options value is container-construction "Reusing one options value" -->
<!-- cases: 4 boundaries: logger implemented or not, Logf set or not -->
<!-- cases: N/A 5 errors: SetLogf returns nothing -->
<!-- cases: 6 duplicate: SetLogf is called at most once per load; a clone sharing the source does not call it again (config-sources "A clone shares its parent's source") -->
<!-- cases: 7 lifecycle: SetLogf before Load -->
<!-- cases: N/A 8 concurrency: runs on the constructor's goroutine -->
<!-- cases: N/A 9 panics: a panicking SetLogf propagates like a package panic; not specified further -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Load runs once under a bounded context
The constructor SHALL call `Load` exactly once. With the real clock, its context SHALL be `context.WithTimeout(ctx, ConfigTimeout)` of the caller's context, so the earlier deadline wins. A zero `ConfigTimeout` SHALL mean `DefaultConfigTimeout` (10s); a negative one SHALL add no deadline of its own.

#### Scenario: The Load deadline on the real clock
```gherkin
Given the test runs inside a testing/synctest bubble, whose clock starts at 2000-01-01T00:00:00Z
And a ConfigSource src whose Load records ctx.Deadline()
And a caller context ctx with <caller deadline>
When the consumer calls doger.NewWithOptsContext(ctx, &doger.InjectorOpts{ConfigSource: src, ConfigTimeout: <timeout>})
Then the recorded deadline is <deadline>

Examples:
  | caller deadline | timeout | deadline                      |
  | none            | 0       | (2000-01-01T00:00:10Z, true)  |
  | none            | 2s      | (2000-01-01T00:00:02Z, true)  |
  | none            | -1ns    | (zero time, false)            |
  | +1s             | 10s     | (2000-01-01T00:00:01Z, true)  |
  | +1m             | 2s      | (2000-01-01T00:00:02Z, true)  |
  | +1m             | -1ns    | (2000-01-01T00:01:00Z, true)  |
```

#### Scenario: A Load that honors its deadline
```gherkin
Given the test runs inside a testing/synctest bubble
And a ConfigSource src whose Load waits on ctx.Done() and returns ctx.Err()
When the consumer calls doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src, ConfigTimeout: 2s})
Then the call returns after 2s of bubble time with a nil root
And errors.Is(err, context.DeadlineExceeded) and errors.Is(err, doger.ErrConfigLoadFailed) are both true
```

#### Scenario: A Load that ignores its deadline
```gherkin
Given the test runs inside a testing/synctest bubble
And a ConfigSource src whose Load sleeps 5s, ignoring ctx, and returns nil
When the consumer calls doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src, ConfigTimeout: 2s})
Then the call returns after 5s of bubble time with a non-nil root and a nil error
```

<!-- cases: 1 default: the 0 row is DefaultConfigTimeout -->
<!-- cases: 2 overrides: ConfigTimeout rows and caller deadlines; the earlier of the two always wins -->
<!-- cases: 3 zero values: the 0 row -->
<!-- cases: 4 boundaries: caller deadline before and after now+ConfigTimeout; -1ns is the smallest negative -->
<!-- cases: 5 errors: the honoring scenario; other Load failures are "A failed load fails construction" -->
<!-- cases: N/A 6 duplicate: Load is called exactly once -->
<!-- cases: 7 lifecycle: the constructor waits for Load to return; cancellation is cooperative, so a Load that ignores ctx and succeeds is a success (documented in godoc) -->
<!-- cases: 8 concurrency: deterministic through synctest bubble time -->
<!-- cases: N/A 9 panics: a panicking Load is "A failed load fails construction" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A user clock bounds Load in its own time base
When `InjectorOpts.Clock` is set, the `Load` context SHALL be done when either the caller's context is done or `ConfigTimeout` elapses on that clock. Its `Deadline()` SHALL report the clock's deadline, the caller's and the clock's deadlines SHALL never be compared, its `Err` SHALL never block, and neither creating it nor deriving `WithCancel` children from it SHALL start a goroutine.

#### Scenario: Deadline and Err on a fake clock
```gherkin
Given InjectorOpts.Clock set to a fake clock at 2026-01-01T00:00:00Z and ConfigTimeout <timeout>
And a ConfigSource src whose Load records ctx.Deadline() and ctx.Err(), and derives context.WithCancel(ctx) and cancels it
When the consumer calls doger.NewWithOpts with ConfigSource src
Then the recorded deadline is <deadline>
And the recorded Err is nil, returned without blocking
And goleak finds no goroutine started during the call once it has returned

Examples:
  | timeout | deadline                     |
  | 0       | (2026-01-01T00:00:10Z, true) |
  | 2s      | (2026-01-01T00:00:02Z, true) |
  | -1ns    | (zero time, false)           |
```

#### Scenario: Which side ends the Load context
```gherkin
Given InjectorOpts.Clock set to a fake clock and ConfigTimeout 2s
And a ConfigSource src whose Load signals that it started, then waits on ctx.Done() and returns ctx.Err()
When <event> after Load started
Then doger.NewWithOptsContext returns a nil root
And errors.Is(err, <cause>) and errors.Is(err, doger.ErrConfigLoadFailed) are both true

Examples:
  | event                                                    | cause                    |
  | the test advances the fake clock by 2s                   | context.DeadlineExceeded |
  | the test cancels the caller's context                    | context.Canceled         |
```

<!-- cases: 1 default: the 0 row is DefaultConfigTimeout on the clock -->
<!-- cases: 2 overrides: ConfigTimeout rows; the caller's cancellation still applies -->
<!-- cases: 3 zero values: the 0 row -->
<!-- cases: 4 boundaries: clock fires exactly at +2s; a negative timeout adds no clock deadline -->
<!-- cases: 5 errors: DeadlineExceeded from the clock, Canceled from the caller -->
<!-- cases: N/A 6 duplicate: Load is called once -->
<!-- cases: 7 lifecycle: the caller's deadline is never compared with the clock's, since they are different time bases (documented) -->
<!-- cases: 8 concurrency: no goroutine is started by the context or its WithCancel children (the context implements AfterFunc) -->
<!-- cases: N/A 9 panics: as "A failed load fails construction" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: The Load context ends when the constructor returns
The context passed to `Load` SHALL be done, with `Err` returning `context.Canceled` unless it already ended, once the constructor returns, so a source cannot keep using it; and construction SHALL leave no goroutine running that it started.

#### Scenario: A source that keeps its Load context
```gherkin
Given a ConfigSource src whose Load stores its ctx and returns nil
And InjectorOpts.Clock is <clock>
When the consumer calls doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src, Clock: <clock>})
Then after the call returns, the stored ctx.Done() is closed and ctx.Err() is context.Canceled
And goleak finds no goroutine started during the call

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
<!-- cases: 8 concurrency: no goroutine outlives the constructor (health-check-execution delta) -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A failed load fails construction
When `Load` returns an error or panics, the constructor SHALL return a nil root and a `*ConfigLoadError{Source reflect.Type; Err error}` matching `ErrConfigLoadFailed`, whose `Unwrap` returns the cause so `errors.Is` and `errors.As` reach it. A panic SHALL be recovered; an error panic value SHALL be the cause, and any other value SHALL be described by type only.

#### Scenario: Load errors and panics
```gherkin
Given a ConfigSource src of type "*app.Vault" whose Load <fails>
When the consumer calls doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src})
Then the returned root is nil
And errors.Is(err, doger.ErrConfigLoadFailed) is true and errors.As(err, &loadErr) gives loadErr.Source.String() == "*app.Vault"
And err.Error() is "<text>"
And <match>

Examples:
  | fails                                                      | text                                                                                   | match                                                  |
  | returns errSealed = &app.SealedError{Token: "s3cret"}      | DI: config: load failed: source *app.Vault: *app.SealedError                           | errors.Is(err, errSealed) is true                      |
  | returns context.DeadlineExceeded                           | DI: config: load failed: source *app.Vault: context deadline exceeded                  | errors.Is(err, context.DeadlineExceeded) is true       |
  | returns fmt.Errorf("vault: %w", context.Canceled)           | DI: config: load failed: source *app.Vault: context canceled                           | errors.Is(err, context.Canceled) is true               |
  | panics with "token s3cret rejected"                        | DI: config: load failed: source *app.Vault: panic of type string                       | err.Error() does not contain "s3cret"                  |
  | panics with errPanic = errors.New("token s3cret rejected") | DI: config: load failed: source *app.Vault: panic of type *errors.errorString          | errors.Is(err, errPanic) is true                       |
  | panics with nil                                            | DI: config: load failed: source *app.Vault: panic of type *runtime.PanicNilError       | errors.As(err, &pn) is true for pn *runtime.PanicNilError |
```

<!-- cases: N/A 1 default, 2 overrides: failure handling is fixed -->
<!-- cases: N/A 3 zero values: a nil error from Load is success -->
<!-- cases: 4 boundaries: typed error, context errors (direct and wrapped), string panic, error panic, panic(nil) -->
<!-- cases: 5 errors: the subject; matchable with errors.Is on ErrConfigLoadFailed and on the cause; errors.As on *ConfigLoadError; exact texts pinned -->
<!-- cases: N/A 6 duplicate: not applicable -->
<!-- cases: 7 lifecycle: no package runs (container-construction) -->
<!-- cases: N/A 8 concurrency: Load runs on the constructor's goroutine -->
<!-- cases: 9 panics: panic rows; a panic's text is never shown, since it may hold a value -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Value-free error text is shown
`ValueFreeError` SHALL be `interface{ error; ValueFree() }`, an error whose text promises to hold no property value. In a load-failed message the root SHALL show the text of the first error in the chain that implements it (`errors.AsType[ValueFreeError]`), and the text of `context.Canceled` and `context.DeadlineExceeded`; every other cause SHALL be shown by its type only.

#### Scenario: Which text a load error shows
```gherkin
Given a ConfigSource src of type "*app.Vault" whose Load returns <cause>
When the consumer calls doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src})
Then err.Error() is "<text>"

Examples:
  | cause                                                                                          | text                                                                         |
  | &app.ParseErr{File: "config.yaml", Line: 3} (implements ValueFree; text "config.yaml:3: syntax error") | DI: config: load failed: source *app.Vault: config.yaml:3: syntax error |
  | fmt.Errorf("layer vault: %w", &app.ParseErr{File: "config.yaml", Line: 3})                     | DI: config: load failed: source *app.Vault: config.yaml:3: syntax error      |
  | &app.SealedError{Token: "s3cret"} (does not implement ValueFree)                               | DI: config: load failed: source *app.Vault: *app.SealedError                 |
  | errors.New("token s3cret")                                                                     | DI: config: load failed: source *app.Vault: *errors.errorString              |
```

<!-- cases: 1 default: an unmarked error is shown by type -->
<!-- cases: 2 overrides: a source opts in per error type by implementing ValueFree -->
<!-- cases: N/A 3 zero values: a nil cause is success -->
<!-- cases: 4 boundaries: a marked error directly and wrapped once; an unmarked typed and untyped error -->
<!-- cases: N/A 5 errors: the subject is the text, matching is "A failed load fails construction" -->
<!-- cases: N/A 6 duplicate: the first marked error in the chain wins -->
<!-- cases: N/A 7 lifecycle, 8 concurrency: pure formatting -->
<!-- cases: N/A 9 panics: panics are described by type -->
<!-- cases: N/A 10 compatibility: new API; config.Files errors implement the marker (config-files) -->

### Requirement: A clone shares its parent's source only when it is the same pointer
`Clone()`, and `CloneWithOpts`/`CloneWithOptsContext` given a `ConfigSource` of pointer kind equal to the parent's, SHALL share the parent's loaded source and committed config: `Load` and `SetLogf` SHALL NOT be called again. Any other source, including a non-pointer value equal to the parent's, SHALL be loaded for the clone; no source means no config source. Comparison SHALL never panic.

#### Scenario: Sharing on clone
```gherkin
Given clearEnvPrefix(t, "DOGER_") has removed every inherited DOGER_ variable for the test
And root, err := doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src, Logf: recorder}) succeeded, with src a *app.Vault holding "app.name" = "a" and counting its Load and SetLogf calls
When the consumer calls <call>
Then the clone is non-nil and the error, if any, is nil
And the clone's property "app.name" reads <read>
And src.Load and src.SetLogf were each called exactly 1 time

Examples:
  | call                                                                    | read        |
  | root.Clone()                                                            | ("a", true) |
  | root.CloneWithOpts(&doger.InjectorOpts{ConfigSource: src})              | ("a", true) |
  | root.CloneWithOpts(&doger.InjectorOpts{ConfigSource: src, Logf: other}) | ("a", true) |
  | root.CloneWithOpts(&doger.InjectorOpts{})                               | ("", false) |
```

#### Scenario: A non-pointer source is loaded again
```gherkin
Given a source type app.ValueVault, a struct with a map field (so == on it would panic), whose Load counts calls on a shared counter
And root, err := doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: v}) succeeded for a value v of that type
When the consumer calls root.CloneWithOpts(&doger.InjectorOpts{ConfigSource: v})
Then the call does not panic and returns a non-nil clone and a nil error
And the shared Load counter is 2
```

<!-- cases: 1 default: Clone row -->
<!-- cases: 2 overrides: a different source per clone is container-construction "Cloning with options"; a different Logf with the same source does not call SetLogf again -->
<!-- cases: 3 zero values: the empty-opts row gives a clone without a source, reading env with DOGER_ cleared -->
<!-- cases: 4 boundaries: Clone, same pointer, same pointer with new Logf, no source, non-pointer value -->
<!-- cases: N/A 5 errors: sharing cannot fail; a loaded clone fails as NewWithOpts -->
<!-- cases: 6 duplicate: the subject; sharing outside clones is undefined and documented -->
<!-- cases: 7 lifecycle: watcher sharing after Start is config-reload -->
<!-- cases: N/A 8 concurrency: as Clone -->
<!-- cases: 9 panics: comparing a non-comparable value never panics, because only pointer kinds are compared -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Values never appear in the library's errors or logs
Text the library adds to config errors and log lines SHALL contain only key names, type names, rule names and the text of value-free errors, never a property value. The `GoString` of its error types SHALL be type-only, so `%#v` leaks nothing. A source's error SHALL stay reachable with `errors.Is` and `errors.As`.

#### Scenario: Secrets stay out of construction errors and logs
```gherkin
Given a root Logf recorder that records lines
And a ConfigSource src of type "*app.Vault" holding "db.password" = "s3cret", whose Load returns errSecret = app.SecretError{Token: "s3cret"}, a value type whose Error text holds the token
When the consumer calls doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src, Logf: recorder})
Then err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err) and fmt.Sprintf("%#v", err) do not contain "s3cret"
And no recorded log line contains "s3cret"
And errors.Is(err, errSecret) is true
```

<!-- cases: N/A 2-4: secrecy has no input beyond the value -->
<!-- cases: 5 errors: the library's text names types only, while the cause stays reachable; the claim covers text the library adds, not the cause's own Error() -->
<!-- cases: N/A 6-8: unchanged by state -->
<!-- cases: 9 panics: covered by "A failed load fails construction" panic rows -->
<!-- cases: 10 compatibility: the same type-only policy extension-properties uses for a failing source -->
