# Spec Delta

## Purpose

Defines the `ConfigSource` contract and how a container installs it through `InjectorOpts.ConfigSource`. The source becomes the container's property source, is loaded once under a bounded context before any package runs, and is shared only with that container's clones. The library never puts property values into its errors or logs.

## ADDED Requirements

### Requirement: The ConfigSource contract
`ConfigSource` SHALL be `interface{ PropertySource; PropertyKeys; Load(ctx context.Context) error }`, with stdlib types only. Its `Keys` SHALL follow `PropertyKeys` (extension-properties). `ConfigLogger` (`SetLogf(func(format string, args ...any))`) SHALL be an optional interface, detected on the installed source. The godoc SHALL state that a source serves one container and its clones, and that `Lookup` and `Keys` must be safe for concurrent use, including while `Load` runs.

#### Scenario: Which types are sources
```gherkin
Given a type "*app.Vault" with the methods <methods>
When the consumer checks whether "*app.Vault" implements <target>
Then the result is <result>

Examples:
  | methods                     | target              | result |
  | Lookup, Keys, Load          | doger.ConfigSource  | true   |
  | Lookup, Load                | doger.ConfigSource  | false  |
  | Lookup, Keys                | doger.ConfigSource  | false  |
  | Lookup, Keys, Load          | doger.PropertySource | true  |
  | Lookup, Keys, Load          | doger.PropertyKeys  | true   |
  | Lookup, Keys, Load, SetLogf | doger.ConfigLogger  | true   |
  | Lookup, Keys, Load          | doger.ConfigLogger  | false  |
```

<!-- cases: 1 default: the contract has no default; without a source the container reads env (extension-properties) -->
<!-- cases: N/A 2 overrides: a contract, not a behavior -->
<!-- cases: N/A 3 zero values: typed-nil and unset sources are "Option checks happen before anything is called" -->
<!-- cases: 4 boundaries: each missing method -->
<!-- cases: N/A 5 errors: Lookup and Keys report misses; Load errors are "A failed load fails construction" -->
<!-- cases: N/A 6 duplicate: sharing a source is documented, not policed (no process-wide state) -->
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
And root, err := doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src}, pkg)
When the consumer invokes "*redis.Client" from root
Then the recorded result is ("from-config", true)

Examples:
  | read                                       |
  | x.Properties().Lookup("addr")              |
  | doger.ExtensionProperties(i).Lookup("addr") |
```

<!-- cases: 1 default: without a ConfigSource the selection rules of extension-properties apply unchanged -->
<!-- cases: 2 overrides: the installed source beats a registered PropertySource service and env -->
<!-- cases: N/A 3 zero values: a nil ConfigSource means unset -->
<!-- cases: 4 boundaries: a read during construction (Extend inside a package) and a read from a provider afterwards -->
<!-- cases: 5 errors: setting PropertySource too is "Setting both sources is a conflict" -->
<!-- cases: N/A 6 duplicate: one property source per container -->
<!-- cases: 7 lifecycle: the Extend read happens during construction, after Load -->
<!-- cases: N/A 8 concurrency: covered by extension-properties "Concurrent lookups" -->
<!-- cases: N/A 9 panics: reads do not panic -->
<!-- cases: 10 compatibility: containers without a ConfigSource choose their source as before -->

### Requirement: Option checks happen before anything is called
The constructor SHALL check the options in this order, before calling any method of the source: a `ConfigSource` holding a nil pointer SHALL fail with `ErrInvalidConfigSource`; then a valid `ConfigSource` set together with `PropertySource` SHALL fail with `ErrConfigConflict`. An unset (nil interface) `ConfigSource` means no source.

#### Scenario: Invalid option combinations
```gherkin
Given a ConfigSource src of type "*app.Vault" whose methods each count their calls, and a PropertySource ps
When the consumer calls doger.NewWithOpts(<opts>)
Then the returned root is <root>
And the returned error is <error>
And no method of src was called

Examples:
  | opts                                                                       | root    | error                                                                              |
  | &doger.InjectorOpts{ConfigSource: nil}                                     | non-nil | nil                                                                                |
  | &doger.InjectorOpts{ConfigSource: (*app.Vault)(nil)}                       | nil     | errors.Is(err, doger.ErrInvalidConfigSource), text "DI: config: invalid config source: nil *app.Vault" |
  | &doger.InjectorOpts{ConfigSource: (*app.Vault)(nil), PropertySource: ps}   | nil     | errors.Is(err, doger.ErrInvalidConfigSource), text "DI: config: invalid config source: nil *app.Vault" |
  | &doger.InjectorOpts{ConfigSource: src, PropertySource: ps}                 | nil     | errors.Is(err, doger.ErrConfigConflict), text "DI: config: both PropertySource and ConfigSource are set" |
```

<!-- cases: 1 default: the unset row -->
<!-- cases: 2 overrides: either field alone is valid; the conflict row -->
<!-- cases: 3 zero values: nil interface and typed nil rows -->
<!-- cases: 4 boundaries: the check order when both problems occur (third row) -->
<!-- cases: 5 errors: exact texts and errors.Is matching -->
<!-- cases: N/A 6 duplicate: not applicable -->
<!-- cases: 7 lifecycle: rejected before SetLogf and Load -->
<!-- cases: N/A 8 concurrency: no runtime state -->
<!-- cases: 9 panics: a typed-nil source is rejected instead of panicking in Load -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: The source's logger is set only when the user chose one
When the installed source implements `ConfigLogger` and `InjectorOpts.Logf` was set by the user, the constructor SHALL call `SetLogf` with it once, before `Load`. Otherwise it SHALL NOT call `SetLogf`, so the source keeps its own default logging.

#### Scenario: SetLogf and Load order
```gherkin
Given a ConfigSource src that appends each call ("SetLogf", "Load") to a call log, and implements doger.ConfigLogger <logger>
When the consumer calls doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src, Logf: <logf>})
Then the call log is <log>

Examples:
  | logger | logf     | log                    |
  | yes    | recorder | ["SetLogf", "Load"]    |
  | yes    | nil      | ["Load"]               |
  | no     | recorder | ["Load"]               |
  | no     | nil      | ["Load"]               |
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
<!-- cases: 3 zero values: nil Logf rows -->
<!-- cases: 4 boundaries: logger implemented or not, Logf set or not -->
<!-- cases: N/A 5 errors: SetLogf returns nothing -->
<!-- cases: 6 duplicate: SetLogf is called at most once -->
<!-- cases: 7 lifecycle: SetLogf before Load -->
<!-- cases: N/A 8 concurrency: runs on the constructor's goroutine -->
<!-- cases: N/A 9 panics: a panicking SetLogf propagates like a package panic; not specified further -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Load runs once under a bounded context
The constructor SHALL call `Load` exactly once with a context whose deadline is `InjectorOpts.ConfigTimeout` after the call, on the container's clock, unless the caller's context has a deadline. A zero `ConfigTimeout` SHALL mean `DefaultConfigTimeout` (10s); a negative one SHALL mean no deadline. The context's `Err` SHALL never block, and creating it SHALL start no goroutine.

#### Scenario: The Load deadline
```gherkin
Given InjectorOpts.Clock set to a fake clock at 2026-01-01T00:00:00Z
And a ConfigSource src whose Load records ctx.Deadline()
When the consumer calls doger.NewWithOpts with ConfigSource src and ConfigTimeout <timeout>
Then the recorded deadline is <deadline>

Examples:
  | timeout | deadline                       |
  | 0       | (2026-01-01T00:00:10Z, true)   |
  | 2s      | (2026-01-01T00:00:02Z, true)   |
  | 1m      | (2026-01-01T00:01:00Z, true)   |
  | -1ns    | (zero time, false)             |
```

#### Scenario: The Load context on a fake clock
```gherkin
Given InjectorOpts.Clock set to a fake clock and ConfigTimeout 2s
And a ConfigSource src whose Load calls ctx.Err() and records the result
When the consumer calls doger.NewWithOpts with ConfigSource src
Then the recorded result is nil, returned without blocking
And after the call returns, goleak finds no goroutine started by the constructor
```

#### Scenario: A Load that honors its deadline
```gherkin
Given InjectorOpts.Clock set to a fake clock and ConfigTimeout 2s
And a ConfigSource src whose Load signals that it started, then waits on ctx.Done() and returns ctx.Err()
When the test advances the fake clock by 2s after Load started
Then doger.NewWithOpts returns a nil root
And errors.Is(err, context.DeadlineExceeded) and errors.Is(err, doger.ErrConfigLoad) are both true
```

#### Scenario: A Load that ignores its deadline
```gherkin
Given InjectorOpts.Clock set to a fake clock and ConfigTimeout 2s
And a ConfigSource src whose Load advances the fake clock by 5s, ignores ctx, and returns nil
When the consumer calls doger.NewWithOpts with ConfigSource src
Then the call returns a non-nil root and a nil error
```

<!-- cases: 1 default: the 0 row is DefaultConfigTimeout -->
<!-- cases: 2 overrides: 2s, 1m and negative rows; a caller deadline is container-construction "The context reaching Load" -->
<!-- cases: 3 zero values: the 0 row -->
<!-- cases: 4 boundaries: -1ns is the smallest negative; the deadline is reached exactly at +2s -->
<!-- cases: 5 errors: the honoring scenario; other Load failures are the next requirement -->
<!-- cases: N/A 6 duplicate: Load is called exactly once -->
<!-- cases: 7 lifecycle: the constructor waits for Load to return; cancellation is cooperative, so a Load that ignores ctx and succeeds is a success (documented) -->
<!-- cases: 8 concurrency: a fake-clock context starts no goroutine and Err never blocks (goleak) -->
<!-- cases: N/A 9 panics: a panicking Load is the next requirement -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A failed load fails construction
When `Load` returns an error or panics, the constructor SHALL return a nil root and an error matching `ErrConfigLoad`, whose text names the source's type and the error's type, never the error's text. A returned error SHALL also match the source's error with `errors.Is`. A panic SHALL be recovered, and the text SHALL name the panic value's type only.

#### Scenario: Load errors and panics
```gherkin
Given a ConfigSource src of type "*app.Vault" whose Load <fails>
When the consumer calls doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src})
Then the returned root is nil
And errors.Is(err, doger.ErrConfigLoad) is true
And err.Error() is "<text>"
And <match>

Examples:
  | fails                                            | text                                                                    | match                                         |
  | returns errSealed = errors.New("vault sealed")   | DI: config: load failed: source *app.Vault: *errors.errorString         | errors.Is(err, errSealed) is true             |
  | returns context.DeadlineExceeded                 | DI: config: load failed: source *app.Vault: context.deadlineExceededError | errors.Is(err, context.DeadlineExceeded) is true |
  | panics with "token s3cret rejected"              | DI: config: load failed: source *app.Vault: panic of type string        | err.Error() does not contain "s3cret"         |
  | panics with errors.New("token s3cret rejected")  | DI: config: load failed: source *app.Vault: panic of type *errors.errorString | err.Error() does not contain "s3cret"   |
```

<!-- cases: N/A 1 default, 2 overrides: failure handling is fixed -->
<!-- cases: N/A 3 zero values: a nil error from Load is success -->
<!-- cases: 4 boundaries: error, context error, string panic, error panic -->
<!-- cases: 5 errors: the subject; matchable with errors.Is on ErrConfigLoad and on the source's error; exact texts pinned -->
<!-- cases: N/A 6 duplicate: not applicable -->
<!-- cases: 7 lifecycle: no package runs (container-construction) -->
<!-- cases: N/A 8 concurrency: Load runs on the constructor's goroutine -->
<!-- cases: 9 panics: panic rows; the panic value's text is never shown, since it may hold a value -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A clone shares its parent's source
`Clone()`, and `CloneWithOpts` given the parent's own `ConfigSource` value, SHALL share the parent's loaded source and committed config: `Load` and `SetLogf` SHALL NOT be called again. `CloneWithOpts` without a `ConfigSource` SHALL give a clone with no config source, and one with a different source SHALL load that source.

#### Scenario: Sharing on clone
```gherkin
Given root, err := doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src, Logf: recorder}) succeeded, with src holding "app.name" = "a" and counting its Load and SetLogf calls
When the consumer calls <call>
Then the clone is non-nil and the error, if any, is nil
And the clone's property "app.name" reads <read>
And src.Load and src.SetLogf were each called exactly 1 time

Examples:
  | call                                                                      | read          |
  | root.Clone()                                                              | ("a", true)   |
  | root.CloneWithOpts(&doger.InjectorOpts{ConfigSource: src})                | ("a", true)   |
  | root.CloneWithOpts(&doger.InjectorOpts{ConfigSource: src, Logf: other})   | ("a", true)   |
  | root.CloneWithOpts(&doger.InjectorOpts{})                                 | ("", false)   |
```

<!-- cases: 1 default: Clone row -->
<!-- cases: 2 overrides: a different source per clone is container-construction "Cloning with options"; a different Logf with the same source does not call SetLogf again -->
<!-- cases: 3 zero values: the empty-opts row gives a clone without a source -->
<!-- cases: 4 boundaries: Clone, same source, same source with new Logf, no source -->
<!-- cases: N/A 5 errors: sharing cannot fail -->
<!-- cases: 6 duplicate: the subject; sharing outside clones is undefined and documented -->
<!-- cases: 7 lifecycle: watcher sharing after Start is in config-reload -->
<!-- cases: N/A 8 concurrency: as Clone -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Values never appear in the library's errors or logs
Text the library adds to config errors and log lines SHALL contain only key names, type names and rule names, never a property value or the text of a source's error. A source's error SHALL stay reachable with `errors.Is` and `errors.As`.

#### Scenario: Secrets stay out of construction errors and logs
```gherkin
Given a root Logf recorder that records lines
And a ConfigSource src of type "*app.Vault" holding "db.password" = "s3cret", whose Load returns errSecret = errors.New("vault sealed for token s3cret")
When the consumer calls doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src, Logf: recorder})
Then err.Error() and fmt.Sprintf("%+v", err) do not contain "s3cret"
And no recorded log line contains "s3cret"
And errors.Is(err, errSecret) is true
```

<!-- cases: N/A 2-4: secrecy has no input beyond the value -->
<!-- cases: 5 errors: the library's text names types only, while the cause stays reachable -->
<!-- cases: N/A 6-8: unchanged by state -->
<!-- cases: 9 panics: covered by "A failed load fails construction" panic rows -->
<!-- cases: 10 compatibility: the same type-only policy extension-properties uses for a failing source -->
