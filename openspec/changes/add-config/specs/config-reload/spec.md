# Spec Delta

## Purpose

Lets a running container follow configuration changes without a restart: a watching source stages each change, the root commits it only when every bound section still binds and validates, and live handles, property reads and subscribers then see the new values together, while a failed reload keeps the last good config and warns once.

## ADDED Requirements

<!-- Shared fixtures (root package tests, config_reload_test.go): "a test watcher source" is a root-local test type implementing ConfigSource and ConfigWatcher; its Watch keeps `changed` and calls it on the Watch goroutine when the test asks (D63), and it records every adoption. Every Given states the source's contents; a source that serves ServerConfig holds "server.port", and one that serves LimitsConfig holds "limits.per-minute", so the first bind validates. ServerConfig is {Host string; Port int `validate:"min=1,max=65535"`}; LimitsConfig is {PerMinute int `validate:"min=1"`; Burst int}. Constructors take a ctx (D55): `doger.NewWithOpts(context.Background(), opts, ...)`, `root.CloneWithOpts(context.Background(), opts)`. Tests that use testing/synctest build the root inside the bubble and are wrapped in testWithTimeout outside it; results are received on channels, never inferred from a mutex deadlock. Rows that use config.New are config-module tests (config/watch_test.go). The leaked-secret marker is the high-entropy "s3cret-9f2c7a41". -->

### Requirement: A watching source stages changes for the root
When the installed `ConfigSource` also implements `ConfigWatcher`, the root SHALL call `Watch(ctx, changed)` on a goroutine it owns once a member starts. The source SHALL call `changed(ctx, next, nil)` with an immutable staged `ConfigView`, holding no lock and never overlapping calls, and adopt `next` only when `changed` returns `true`. The root SHALL serialize `changed` calls, and a nil or typed-nil `next` with a nil error SHALL be a staging failure.

#### Scenario: The source adopts only what the root accepts
```gherkin
Given a root created with InjectorOpts.ConfigSource set to a test watcher source holding "server.port" = "8080"
And doger.BindConfig(root, "server", ServerConfig{}) returned a nil error
And root.Start() returned nil
When the test source calls changed(ctx, next, nil) with next holding "server.port" = "<staged>"
Then changed returns <commit>
And the test source recorded <adopted> adoption of next

Examples:
  | staged | commit | adopted |
  | 9090   | true   | one     |
  | 65535  | true   | one     |
  | 99999  | false  | no      |
  | eighty | false  | no      |
```

#### Scenario: A nil view is a staging failure
```gherkin
Given a root with InjectorOpts.Logf recording lines, a test watcher source holding "server.port" = "8080", doger.BindConfig(root, "server", ServerConfig{}) and a hook added with root.AddConfigReloadFailedHook that records errors
And root.Start() returned nil
When the test source calls changed(ctx, <next>, nil)
Then changed returns false and ConfigValue[ServerConfig].Get().Port is 8080
And the hook received one error matching doger.ErrInvalidConfigSource
And one recorded line contains "DI: config: reload failed" and "nil view"

Examples:
  | next                                   |
  | nil                                    |
  | a typed-nil *testView as a ConfigView  |
```

#### Scenario: Overlapping changed calls are serialized
```gherkin
Given a root with a test watcher source holding "server.port" = "8080", doger.BindConfig(root, "server", ServerConfig{}), and a ServerConfig.Validate that, only when Port is 9090, closes channel "entered" and waits for channel "release", and records when any call starts and ends
And root.Start() returned nil and a first goroutine is inside changed with next holding "server.port" = "9090", after "entered" closed
When a second goroutine calls changed with next holding "server.port" = "9191", and then the test closes "release"
Then both calls return true, the first before the second
And no Validate call for 9191 started before the first changed returned
And ConfigValue[ServerConfig].Get().Port is 9191
```

<!-- cases: 1 default: the 9090 row -->
<!-- cases: N/A 2 overrides: the contract has no options; config.New's options are in "A config.New source polls for changes" -->
<!-- cases: 3 zero values: the nil view scenario; a next with no keys binds every section to its defaults, see "Committed values reach live handles" -->
<!-- cases: 4 boundaries: 65535 (at the max) versus 99999 -->
<!-- cases: 5 errors: the validation row, the conversion row and the nil view rows (ErrInvalidConfigSource); staging errors are "A failed reload keeps the last good config" -->
<!-- cases: 6 duplicate: overlapping changed calls are serialized by the root's reload slot (a cap-1 channel acquired with select on the watch ctx) -->
<!-- cases: 7 lifecycle: Watch is called only after Start, see "The watcher runs while a member is started" -->
<!-- cases: 8 concurrency: the serialization scenario; reads are "Reads are safe during a reload" -->
<!-- cases: N/A 9 panics: covered by "A watch that stops" and "Callback panics follow HookContainerPanic" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Every read path follows the committed view
Property reads SHALL go through a view the root owns, switched to `next` only after every section bound, then every `ConfigValue` published, all before `changed` returns `true`. `ResolveConfig` and `BindProperties` SHALL bind from one view per call, and a `Properties` value SHALL read from the view committed when it was obtained, so no read mixes two views.

#### Scenario: Read paths agree after a commit and after a rejection
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" and "srv.port" = "8080"
And an extension "srv" whose private scope holds a transient "*app.Probe" whose provider records doger.ExtensionProperties(i) Lookup("port")
And doger.BindConfig(root, "server", ServerConfig{}) returned a nil error and root.Start() returned nil
When the test source calls changed with next holding "server.port" = "<staged>" and "srv.port" = "<staged>"
Then the read "<path>" returns <value>

Examples:
  | staged | path                                                    | value  |
  | 9090   | doger.ResolveConfig(root, "server", ServerConfig{}).Port | 9090   |
  | 9090   | ConfigValue[ServerConfig].Get().Port                    | 9090   |
  | 9090   | invoking "*app.Probe" (ExtensionProperties Lookup)      | "9090" |
  | 99999  | doger.ResolveConfig(root, "server", ServerConfig{}).Port | 8080   |
  | 99999  | ConfigValue[ServerConfig].Get().Port                    | 8080   |
  | 99999  | invoking "*app.Probe" (ExtensionProperties Lookup)      | "8080" |
```

#### Scenario: The staged view is invisible until commit
```gherkin
Given a root with a test watcher source holding "server.port" = "8080", doger.BindConfig(root, "server", ServerConfig{}), and a ServerConfig.Validate() that, only when Port is 9090, closes channel "entered" and waits for channel "release"
And root.Start() returned nil
And a goroutine has called changed with next holding "server.port" = "9090", and "entered" is closed
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then it returns Port 8080
And after "release" is closed and changed returned true, the same call returns Port 9090
```

#### Scenario: One BindProperties call reads one view
```gherkin
Given a root with a test watcher source holding "srv.host" = "h0" and "srv.port" = "1"
And an extension "srv" whose private scope holds a transient "*app.Probe" whose provider calls doger.BindProperties(doger.ExtensionProperties(i), &cfg) into struct{ Host string; Port int }
And root.Start() returned nil
When 8 goroutines invoke "*app.Probe" in a loop while the source commits 100 views, view n holding "srv.host" = "h<n>" and "srv.port" = "<n>"
Then every recorded cfg has Host equal to "h" followed by its Port
And the test passes under -race
```

<!-- cases: 1 default: the commit rows -->
<!-- cases: N/A 2 overrides: the read rule is not configurable, by design: a mixed read would break all-or-nothing -->
<!-- cases: N/A 3 zero values: covered by "Committed values reach live handles" -->
<!-- cases: 4 boundaries: before commit, after commit, after rejection -->
<!-- cases: 5 errors: a rejection keeps every read path on the old view (99999 rows) -->
<!-- cases: N/A 6 duplicate: one committed view per config state -->
<!-- cases: 7 lifecycle: the window between staging and commit is pinned by the "entered"/"release" handshake -->
<!-- cases: 8 concurrency: the handshake scenario and the BindProperties scenario under -race -->
<!-- cases: N/A 9 panics: covered by "Callback panics follow HookContainerPanic" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A reload is all or nothing across sections
The root SHALL bind and validate every section in the reload set from the same staged view, with no lock held while user code (`Validate`, `UnmarshalText`, a `ConfigValidator`, the view's `Lookup` and `Keys`) runs, and commit only when all of them succeed. No two sections SHALL ever be committed from different views.

#### Scenario: One invalid section blocks every section
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" and "limits.per-minute" = "10"
And doger.BindConfig(root, "server", ServerConfig{}) and doger.BindConfig(root, "limits", LimitsConfig{}) returned nil errors
And root.Start() returned nil
When the source calls changed with next holding "server.port" = "9090" and "limits.per-minute" = "<per-minute>"
Then ConfigValue[ServerConfig].Get().Port is <port> and ConfigValue[LimitsConfig].Get().PerMinute is <limit>

Examples:
  | per-minute | port | limit |
  | 20         | 9090 | 20    |
  | 1          | 9090 | 1     |
  | 0          | 8080 | 10    |
  | many       | 8080 | 10    |
```

#### Scenario: Readers never see a mixed value
```gherkin
Given a root with a test watcher source holding "server.host" = "h1" and "server.port" = "1"
And doger.BindConfig(root, "server", ServerConfig{}) returned a nil error and root.Start() returned nil
When 8 goroutines call ConfigValue[ServerConfig].Get() and doger.ResolveConfig(root, "server", ServerConfig{}) in a loop while the source commits 100 views, view n holding "server.host" = "h<n>" and "server.port" = "<n>"
Then every value read has Host equal to "h" followed by its Port
And the test passes under -race
```

<!-- cases: 1 default: the valid row -->
<!-- cases: N/A 2 overrides: all-or-nothing is not configurable, by design: a partial commit would mix views -->
<!-- cases: N/A 3 zero values: covered in "Committed values reach live handles" -->
<!-- cases: 4 boundaries: min=1 at 1 versus 0 -->
<!-- cases: 5 errors: the validation row and the conversion row -->
<!-- cases: N/A 6 duplicate: sections are distinct prefixes -->
<!-- cases: 7 lifecycle: before and after a commit -->
<!-- cases: 8 concurrency: the mixed-value scenario; user code without a lock is "Callbacks may call back into the container" -->
<!-- cases: N/A 9 panics: covered by "Callback panics follow HookContainerPanic" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Committed values reach live handles
After a commit, `ConfigValue[T].Get()` SHALL return the section bound from the committed view over its defaults, so a key removed from the source brings its default back. A plain `T` resolved before the reload SHALL keep its value, and a `T` invoked after the reload SHALL be the value registered at bind time.

#### Scenario: Live handle versus snapshot after a reload
```gherkin
Given a root with a test watcher source holding <before>
And doger.BindConfig(root, "server", ServerConfig{Host: "0.0.0.0", Port: 8080}) returned a nil error
And root.Start() returned nil and the consumer invoked ServerConfig as "snap"
When the source commits <after>
Then ConfigValue[ServerConfig].Get() is <live>
And "snap" is still <snap>
And doger.Invoke[ServerConfig](root) returns <snap>

Examples:
  | before                                       | after                  | live                          | snap                          |
  | "server.port" = "9000"                       | "server.port" = "9100" | {Host: "0.0.0.0", Port: 9100} | {Host: "0.0.0.0", Port: 9000} |
  | "server.port" = "9000", "server.host" = "h"  | "server.port" = "9000" | {Host: "0.0.0.0", Port: 9000} | {Host: "h", Port: 9000}       |
  | "server.port" = "9000"                       | no keys                | {Host: "0.0.0.0", Port: 8080} | {Host: "0.0.0.0", Port: 9000} |
```

<!-- cases: 1 default: the changed-key row -->
<!-- cases: N/A 2 overrides: defaults are the BindConfig argument, covered in config-binding -->
<!-- cases: 3 zero values: the no-keys row (the non-zero Port default keeps it valid) -->
<!-- cases: 4 boundaries: one key changed, one key removed, every key removed -->
<!-- cases: N/A 5 errors: failed reloads are a separate requirement -->
<!-- cases: N/A 6 duplicate: one handle per section -->
<!-- cases: 7 lifecycle: before and after the reload -->
<!-- cases: N/A 8 concurrency: covered by "A reload is all or nothing across sections" -->
<!-- cases: N/A 9 panics: binding panics are config-binding's -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A failed reload keeps the last good config
When a section fails to bind or validate, or the source reports a staging error through `changed(ctx, nil, err)`, the root SHALL keep the committed view, return `false`, warn once with keys, rules and value-free text only (one line per failing section), and enqueue the `HookConfigReloadFailed` hooks, each called as `fn(root, err)` with no context. The warning SHALL go to `InjectorOpts.Logf` when set, otherwise to `slog.Default()`.

#### Scenario: Failures warn and call the hooks
```gherkin
Given a root created with <logf>, a test watcher source holding "server.port" = "8080" and "db.password" = "s3cret-9f2c7a41", and two hooks added with root.AddConfigReloadFailedHook that record (r, err)
And doger.BindConfig(root, "server", ServerConfig{}) returned a nil error and root.Start() returned nil
When the source calls changed with <change>
Then changed returns false and ConfigValue[ServerConfig].Get().Port is 8080
And one warning in <sink> contains "DI: config: reload failed" and "<text>", and no warning contains "s3cret-9f2c7a41"
And both hooks received r == root and an err matching <matches>, in the order they were added

Examples:
  | logf                        | change                                                                      | sink                                              | text                      | matches                     |
  | InjectorOpts.Logf recording | next holding "server.port" = "99999", "db.password" = "s3cret-9f2c7a41"    | the recorded lines                                | server.port               | doger.ErrInvalidConfig      |
  | InjectorOpts.Logf recording | next holding "server.port" = "s3cret-9f2c7a41"                              | the recorded lines                                | server.port               | doger.ErrConfigConversion   |
  | InjectorOpts.Logf recording | nil next and err = a root-local *stageError whose ValueFree() returns true, with text "config.yaml:3: syntax error" | the recorded lines | config.yaml:3: syntax error | *stageError via errors.As |
  | InjectorOpts.Logf recording | nil next and err = errors.New("vault token s3cret-9f2c7a41")                | the recorded lines                                | *errors.errorString       | the errors.New value        |
  | no Logf                     | next holding "server.port" = "99999"                                        | a slog.Default handler set with t.Cleanup restore | server.port               | doger.ErrInvalidConfig      |
```

#### Scenario: Several failing sections warn once, one line per section
```gherkin
Given a root with InjectorOpts.Logf recording lines and a test watcher source holding "server.port" = "8080" and "limits.per-minute" = "10"
And doger.BindConfig(root, "server", ServerConfig{}) and doger.BindConfig(root, "limits", LimitsConfig{}) returned nil errors, and root.Start() returned nil
When the source calls changed with next holding "server.port" = "99999" and "limits.per-minute" = "0"
Then changed returns false
And exactly two recorded lines contain "DI: config: reload failed": the first naming "server.port" and the second naming "limits.per-minute", in bind order
```

#### Scenario: Hooks run after the start context ended
```gherkin
Given a root with a test watcher source holding "server.port" = "8080", doger.BindConfig(root, "server", ServerConfig{}), and a hook added with root.AddConfigReloadFailedHook that records (r, err)
And root.StartWithContext(ctx) returned nil, and ctx was cancelled after it returned
When the source calls changed with next holding "server.port" = "99999"
Then the hook recorded r == root and an err matching doger.ErrInvalidConfig, exactly once
```

<!-- cases: 1 default: the no-Logf row warns through slog.Default with no hooks needed -->
<!-- cases: 2 overrides: InjectorOpts.Logf rows; hooks added through InjectorOpts.HookConfigReloadFailed ([]func(root *RootScope, err error)) behave like AddConfigReloadFailedHook -->
<!-- cases: 3 zero values: nil hooks warn only; a nil fn passed to AddConfigReloadFailedHook is ignored -->
<!-- cases: 4 boundaries: zero, one and two hooks; one failing section versus two -->
<!-- cases: 5 errors: validation, conversion, value-free staging error (text shown) and plain staging error (type only) rows, each matchable -->
<!-- cases: N/A 6 duplicate: repeated identical failures are staged once, see "A poll stages each new content once" -->
<!-- cases: 7 lifecycle: the watcher keeps running after a failure; hooks get no context, so the start ctx's cancellation cannot reach them (D53, as HookContainerHealthChanged) -->
<!-- cases: 8 concurrency: hooks run on the dispatcher, see "Notifications run on one dispatcher" -->
<!-- cases: N/A 9 panics: covered by "Callback panics follow HookContainerPanic" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Subscribers are notified when their section changed
After a commit, each `ConfigValue[T].OnChange` subscriber SHALL be called once with the new value only when the section's raw key/value set under its prefix changed. Every handle SHALL be published before the first subscriber of that commit runs. A `StaticConfig` handle and the zero `ConfigValue[T]` SHALL never notify.

#### Scenario: Who is notified
```gherkin
Given a root with a test watcher source holding "server.port" = "8080", "limits.per-minute" = "10" and "limits.burst" = "5", BindConfig for "server" and "limits" with zero-value defaults, and one OnChange subscriber on each handle
And root.Start() returned nil
When the source commits <change>, keeping every other key
Then the server subscriber is called <server> and the limits subscriber <limits>

Examples:
  | change                                         | server                 | limits             |
  | "server.port" = "9090"                         | once with Port 9090    | never              |
  | "server.port" = "9090", "limits.burst" = "6"   | once with Port 9090    | once with Burst 6  |
  | the same keys and values                       | never                  | never              |
  | "server.unknown" = "x" added                   | once with Port 8080    | never              |
```

#### Scenario: Every handle is published before the first notification
```gherkin
Given a root with a test watcher source holding "server.port" = "8080", "limits.per-minute" = "10" and "limits.burst" = "5", BindConfig for "server" and "limits", and a server subscriber that records ConfigValue[LimitsConfig].Get().Burst
And root.Start() returned nil
When the source commits "server.port" = "9090", "limits.per-minute" = "10" and "limits.burst" = "6"
Then the recorded Burst is 6
```

#### Scenario: Static and zero handles never notify
```gherkin
Given h := <handle> and a subscriber added with h.OnChange
And a started root with a test watcher source holding "limits.per-minute" = "10" and doger.BindConfig(root, "limits", LimitsConfig{})
When the source commits "limits.per-minute" = "20"
Then the subscriber is never called and the returned stop func is safe to call twice

Examples:
  | handle                                             |
  | doger.StaticConfig(LimitsConfig{PerMinute: 10})    |
  | doger.ConfigValue[LimitsConfig]{}                  |
```

<!-- cases: 1 default: one subscriber per handle -->
<!-- cases: N/A 2 overrides: notification is not configurable: subscribers choose what to do with the value -->
<!-- cases: 3 zero values: the zero-handle row; a value-identical commit notifies nobody -->
<!-- cases: 4 boundaries: no change, one section changed, two sections changed, an unknown key added (raw set changed, bound value not) -->
<!-- cases: N/A 5 errors: a failed reload notifies nobody, see "A failed reload keeps the last good config" -->
<!-- cases: N/A 6 duplicate: two subscribers on one handle are each called once -->
<!-- cases: 7 lifecycle: publish-before-notify scenario -->
<!-- cases: N/A 8 concurrency: covered by "Notifications run on one dispatcher" -->
<!-- cases: N/A 9 panics: covered by "Callback panics follow HookContainerPanic" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Stopping a subscription
`OnChange(fn)` SHALL return a `stop func()` that is safe to call any number of times, from any goroutine. Once `stop` returns, no call of `fn` that has not started SHALL start; a call already running SHALL finish. A nil `fn` SHALL be ignored and return a no-op `stop`.

#### Scenario: Stop semantics
```gherkin
Given a root with a test watcher source holding "server.port" = "8080", doger.BindConfig(root, "server", ServerConfig{}), subscribers A and B added in that order on its handle, and root.Start() returned nil
And <setup>
When the source commits "server.port" = "9090"
Then A is called <a> and B is called <b>

Examples:
  | setup                                                                                   | a     | b     |
  | nothing                                                                                 | once  | once  |
  | A's stop was called before the commit                                                   | never | once  |
  | A's stop was called twice                                                               | never | once  |
  | A calls B's stop from inside its callback                                               | once  | never |
  | A blocks on channel "go"; the test calls B's stop from its own goroutine, then closes "go" | once  | never |
```

#### Scenario: A running call finishes after stop
```gherkin
Given a root with a test watcher source holding "server.port" = "8080", doger.BindConfig(root, "server", ServerConfig{}) and subscriber B that closes "entered" and blocks on channel "go", and root.Start() returned nil
And the source committed "server.port" = "9090" and "entered" is closed
When the test calls B's stop from its own goroutine, closes "go", and the source commits "server.port" = "9191"
Then stop returned before "go" was closed, B's first call finished with Port 9090, and B was called once in total
```

#### Scenario: A nil callback is ignored
```gherkin
Given h is the ConfigValue[ServerConfig] of a started root with a test watcher source holding "server.port" = "8080" and doger.BindConfig(root, "server", ServerConfig{})
And stop := h.OnChange(nil)
When the source commits "server.port" = "9090"
Then nothing panics and stop() returns without effect
```

<!-- cases: 1 default: the nothing row -->
<!-- cases: N/A 2 overrides: not configurable: stop is the only control a subscriber needs -->
<!-- cases: 3 zero values: the nil fn scenario -->
<!-- cases: 4 boundaries: stop before a round, during a round, twice -->
<!-- cases: N/A 5 errors: stop returns nothing -->
<!-- cases: 6 duplicate: double stop row -->
<!-- cases: 7 lifecycle: stop from inside a callback, mid-round -->
<!-- cases: 8 concurrency: stop from another goroutine while the dispatcher runs another subscriber, and while it runs the stopped subscriber itself -->
<!-- cases: N/A 9 panics: nil fn does not panic -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Notifications run on one dispatcher
Subscribers and `HookConfigReloadFailed` hooks SHALL run on one dispatcher goroutine per config state, never on the watcher goroutine, in bind order of their sections. A commit SHALL enqueue without blocking; the dispatcher SHALL coalesce to the latest value per section, and queued hook events to the latest error per hook owner. Once the dispatcher is told to stop, it SHALL drop pending notifications.

#### Scenario: A slow subscriber receives the latest value
```gherkin
Given a testing/synctest bubble with a root, a test watcher source holding "server.port" = "8080", doger.BindConfig(root, "server", ServerConfig{}), and a subscriber that blocks on channel "go" and records each Port it receives
And root.Start() returned nil
When the source commits "server.port" = "1", then "2", then "3" while the subscriber is blocked on its first call, and then "go" is closed
Then the recorded ports are [1, 3]
And every changed returned true without waiting for the subscriber
```

#### Scenario: Pending notifications are dropped at stop
```gherkin
Given a testing/synctest bubble with a root, a test watcher source holding "server.port" = "8080", doger.BindConfig(root, "server", ServerConfig{}), and a subscriber that blocks on channel "go" and records each Port it receives
And root.Start() returned nil, and the source committed "server.port" = "1" and then "2" while the subscriber was blocked on its first call
When root.ShutdownWithContext returned with a context that timed out after 1s, and then "go" is closed
Then the recorded ports are [1]
And the bubble ends with no goroutine left
```

<!-- cases: 1 default: one dispatcher per config state -->
<!-- cases: N/A 2 overrides: not configurable: coalescing bounds the queue to one entry per section and per hook owner -->
<!-- cases: N/A 3 zero values: no subscribers means no dispatch work -->
<!-- cases: 4 boundaries: one value in flight, two coalesced; a pending value at stop -->
<!-- cases: N/A 5 errors: hook events are staged once, see "A poll stages each new content once" -->
<!-- cases: N/A 6 duplicate: one dispatcher shared by a root and its clones, see "Clones share config and the watcher" -->
<!-- cases: 7 lifecycle: the dispatcher checks its done signal first and drops what is pending (the stop scenario) -->
<!-- cases: 8 concurrency: commits never wait for subscribers -->
<!-- cases: N/A 9 panics: covered by "Callback panics follow HookContainerPanic" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Callback panics follow HookContainerPanic
A panic in a subscriber, a `HookConfigReloadFailed` hook or `Watch` SHALL be recovered and passed to `HookContainerPanic` with the point `"config-on-change"`, `"config-reload-failed"` or `"config-watch"` when it is set, and otherwise logged once with the panic value's type only. The other callbacks of that round SHALL still run.

#### Scenario: A panicking callback
```gherkin
Given a root with InjectorOpts.Logf recording lines, <panic hook>, a test watcher source holding "server.port" = "8080", doger.BindConfig(root, "server", ServerConfig{}), a first <callback> that panics with "s3cret-9f2c7a41" and a second one that records its calls
And root.Start() returned nil
When the source <trigger>
Then the second <callback> is called once
And <outcome>
And no recorded line contains "s3cret-9f2c7a41"

Examples:
  | panic hook                                   | callback   | trigger                                  | outcome                                                                          |
  | no HookContainerPanic                        | subscriber | commits "server.port" = "9090"           | one recorded line contains "OnChange subscriber panicked" and "string"           |
  | no HookContainerPanic                        | hook       | calls changed with "server.port" = "99999" | one recorded line contains "HookConfigReloadFailed panicked" and "string"     |
  | HookContainerPanic recording (point, value)  | subscriber | commits "server.port" = "9090"           | HookContainerPanic received ("config-on-change", "s3cret-9f2c7a41") and nothing was logged |
  | HookContainerPanic recording (point, value)  | hook       | calls changed with "server.port" = "99999" | HookContainerPanic received ("config-reload-failed", "s3cret-9f2c7a41") and nothing was logged |
```

<!-- cases: 1 default: the no-HookContainerPanic rows log by type only -->
<!-- cases: 2 overrides: the HookContainerPanic rows; the "config-watch" point is in "A watch that stops" -->
<!-- cases: N/A 3 zero values: no callbacks means nothing can panic -->
<!-- cases: 4 boundaries: subscriber versus hook -->
<!-- cases: N/A 5 errors: callbacks return nothing -->
<!-- cases: N/A 6 duplicate: each panic is reported once -->
<!-- cases: 7 lifecycle: the next commit notifies normally -->
<!-- cases: N/A 8 concurrency: covered by "Notifications run on one dispatcher" -->
<!-- cases: 9 panics: the subject; the panic value is passed only to HookContainerPanic, whose godoc says it may hold a secret, and is never logged; a panic of HookContainerPanic itself propagates, as for the health scheduler -->
<!-- cases: N/A 10 compatibility: new API; HookContainerPanic gains three points -->

### Requirement: Callbacks may call back into the container
Subscribers, hooks and the user code a rebind runs SHALL be able to call `OnChange`, `BindConfig`, `ResolveConfig` and `go root.Shutdown()` without deadlock, because no lock is held while they run. A synchronous `Shutdown` from a callback SHALL be documented as unsupported: services still shut down, but its wait on itself uses up the context's deadline and fails the report.

#### Scenario: Re-entrant calls from a callback
```gherkin
Given a testing/synctest bubble with a root, a test watcher source holding "server.port" = "8080" and "limits.per-minute" = "10" in every view it stages, doger.BindConfig(root, "server", ServerConfig{}), h its ConfigValue[ServerConfig], and a <callback> that calls <call> and sends its result on a channel
And root.Start() returned nil
When the source <trigger>
Then the channel receives <result>

Examples:
  | callback                              | call                                                       | trigger                                    | result                     |
  | subscriber                            | h.OnChange(func(ServerConfig) {})                          | commits "server.port" = "9090"             | a stop func                |
  | subscriber                            | doger.BindConfig(root, "limits", LimitsConfig{})           | commits "server.port" = "9090"             | a nil error                |
  | subscriber                            | doger.ResolveConfig(root, "server", ServerConfig{})        | commits "server.port" = "9090"             | Port 9090                  |
  | ServerConfig.Validate during a rebind | doger.BindConfig(root, "limits", LimitsConfig{})           | calls changed with "server.port" = "9090"  | a nil error                |
  | ServerConfig.Validate during a rebind | doger.ResolveConfig(root, "server", ServerConfig{})        | calls changed with "server.port" = "9090"  | Port 8080 (the committed view) |
  | subscriber                            | go root.Shutdown(), forwarding its report                  | commits "server.port" = "9090"             | a report with Succeed true |
  | hook                                  | go root.Shutdown(), forwarding its report                  | calls changed with "server.port" = "99999" | a report with Succeed true |
```

<!-- cases: 1 default: the OnChange row (spike 2's deadlock) -->
<!-- cases: N/A 2 overrides: not configurable: re-entrancy follows from holding no lock -->
<!-- cases: N/A 3 zero values: covered elsewhere -->
<!-- cases: 4 boundaries: subscriber, hook and rebind user code -->
<!-- cases: N/A 5 errors: each call succeeds -->
<!-- cases: N/A 6 duplicate: a section bound from a callback joins the reload set like any other -->
<!-- cases: 7 lifecycle: `go root.Shutdown()` from a callback completes; a synchronous Shutdown from a callback is documented as unsupported and has no scenario -->
<!-- cases: 8 concurrency: the subject; results arrive on channels and the bubble test is bounded by testWithTimeout -->
<!-- cases: N/A 9 panics: none expected: every call is a supported re-entry -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Shutdown stops reloading before services and is bounded by its context
When the last started member begins shutting down, the root SHALL cancel the watch, make `changed` return `false` and enqueue nothing from then on, including a reload already in flight, shut the services down, and then wait for the watcher and dispatcher goroutines only until `ctx` ends, failing the report with an error matching `ctx.Err()` when it ends first.

#### Scenario: A blocked callback does not hang Shutdown
```gherkin
Given a testing/synctest bubble with a root, a test watcher source holding "server.port" = "8080", a service "*app.DB" whose Shutdown is recorded, doger.BindConfig(root, "server", ServerConfig{}), and a <blocker> that blocks until channel "release" is closed
And root.Start() returned nil and the source has committed or rejected a change that reached the blocker
When the consumer calls root.ShutdownWithContext with a context that times out after 1s
Then it returns after 1s of bubble time with report.Succeed false and an error matching context.DeadlineExceeded
And "*app.DB" Shutdown was called before the deadline
And after "release" is closed, the bubble ends with no goroutine left

Examples:
  | blocker                                  |
  | OnChange subscriber                      |
  | HookConfigReloadFailed hook              |
  | ServerConfig.Validate() during a rebind  |
```

#### Scenario: A reload in flight when shutdown begins does not commit
```gherkin
Given a testing/synctest bubble with a root, a test watcher source holding "server.port" = "8080", a service "*app.DB" whose Shutdown closes channel "dbDown", doger.BindConfig(root, "server", ServerConfig{}) whose ServerConfig.Validate, only when Port is 9090, closes "entered" and waits for "release", and a recording subscriber
And root.Start() returned nil and the Watch goroutine is inside changed with next holding "server.port" = "9090", after "entered" closed
When a goroutine calls root.Shutdown() and, once "dbDown" is closed, the test closes "release"
Then changed returns false, the subscriber is never called and ConfigValue[ServerConfig].Get().Port is 8080
And root.Shutdown() returns a report with Succeed true
```

#### Scenario: Changes after shutdown began are dropped
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" that kept the changed func, doger.BindConfig(root, "server", ServerConfig{}) and a recording subscriber
And root.Start() and then root.Shutdown() returned
When the test source calls the kept changed with next holding "server.port" = "9090"
Then changed returns false, the subscriber is never called and ConfigValue[ServerConfig].Get().Port is 8080
```

<!-- cases: 1 default: Shutdown() uses context.Background and waits until the callback returns, as today -->
<!-- cases: N/A 2 overrides: the ctx is the override -->
<!-- cases: 3 zero values: a nil ctx means context.Background, as for ShutdownWithContext today -->
<!-- cases: 4 boundaries: callback returns before versus after ctx ends; shutdown before, during and after a reload -->
<!-- cases: 5 errors: the deadline rows match context.DeadlineExceeded -->
<!-- cases: N/A 6 duplicate: double Shutdown is in "The watcher runs while a member is started" -->
<!-- cases: 7 lifecycle: the subject; services shut down before the bounded wait, so none is skipped; a reload in flight re-checks shutdown under the semaphore before it commits -->
<!-- cases: 8 concurrency: synctest bubble with testWithTimeout outside it; goroutines exit once released -->
<!-- cases: N/A 9 panics: callback panics are "Callback panics follow HookContainerPanic" -->
<!-- cases: N/A 10 compatibility: Shutdown's signature is unchanged -->

### Requirement: Reads are safe during a reload
`Lookup`, `Keys`, `ConfigValue.Get`, `ResolveConfig` and extension property reads SHALL be safe to call concurrently with commits and rejections.

#### Scenario: Concurrent reads under reload
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" and "srv.port" = "8080", doger.BindConfig(root, "server", ServerConfig{}) and root.Start() returned nil
When 8 goroutines call ConfigValue[ServerConfig].Get(), doger.ResolveConfig(root, "server", ServerConfig{}) and an extension view's Lookup("port") and Keys("") in a loop while the source calls changed 200 times alternating valid and invalid ports
Then the test passes under -race
And at least one commit and one rejection happened
```

<!-- cases: N/A 1-7: concurrency only -->
<!-- cases: 8 concurrency: the subject (spike 3); the fake source makes it deterministic in count -->
<!-- cases: N/A 9 panics, 10 compatibility: concurrency only -->

### Requirement: The watcher runs while a member is started
The watcher and dispatcher SHALL start at the first successful `Start` of any container sharing the config state and stop when the last started member shuts down. Membership SHALL count only successful starts. A start after every member stopped SHALL call `Watch` again only once the previous `Watch` returned, without blocking `Start`, and SHALL not call it if the new watch ends first.

#### Scenario: Watcher lifecycle
```gherkin
Given a testing/synctest bubble with a root using a test watcher source and clone := root.Clone()
When the consumer runs <steps>
Then the test source's Watch was called <calls> and is <state>
And when the bubble ends after cleanup, no goroutine is left

Examples:
  | steps                                                            | calls | state                                  |
  | nothing                                                          | 0     | not running                            |
  | root.Start()                                                     | 1     | running                                |
  | root.Start(), root.Shutdown()                                    | 1     | stopped, its ctx done                  |
  | root.Start(), root.Shutdown(), root.Shutdown()                    | 1     | stopped                                |
  | root.Shutdown()                                                  | 0     | not running                            |
  | root.Start(), clone.Start(), clone.Shutdown()                    | 1     | running                                |
  | root.Start(), clone.Start(), clone.Shutdown(), root.Shutdown()   | 1     | stopped                                |
  | clone.Start(), root.Shutdown()                                   | 1     | running (root was never started)       |
  | root.Start(), clone.Shutdown()                                   | 1     | running (clone was never started)      |
  | root.Start(), root.Shutdown(), clone.Start()                     | 2     | running, the second call after the first returned |
```

#### Scenario: A restart waits for a Watch that ignored its context
```gherkin
Given a testing/synctest bubble with a root using a test watcher source whose first Watch ignores its ctx and returns only when channel "release" is closed
And root.Start() returned nil, and root.ShutdownWithContext with a context that timed out after 1s returned an error matching context.DeadlineExceeded
When the consumer runs <steps>
Then root.Start() returned before "release" was closed
And the test source's Watch was called <calls>
And when the bubble ends, no goroutine is left

Examples:
  | steps                                                          | calls                                         |
  | root.Start(), then closes "release"                            | 2, the second call after the first returned   |
  | root.Start(), root.Shutdown(), then closes "release"           | 1                                             |
```

<!-- cases: 1 default: root Start/Shutdown row -->
<!-- cases: N/A 2 overrides: config.DisableWatch is in "A config.New source polls for changes" -->
<!-- cases: 3 zero values: the nothing row starts no goroutine -->
<!-- cases: 4 boundaries: one member, two members, unbalanced calls, restart -->
<!-- cases: N/A 5 errors: Watch errors are "A watch that stops" -->
<!-- cases: 6 duplicate: double Shutdown row -->
<!-- cases: 7 lifecycle: the subject (spike 1); a restart's launcher waits for the previous Watch's done channel with select on the new watch ctx, so Start never blocks and a stop before the old Watch returned leaks nothing -->
<!-- cases: 8 concurrency: goleak at the end of every row -->
<!-- cases: N/A 9 panics: covered by "A watch that stops" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A watch that stops
A `Watch` that returns nil before its context ends SHALL mean "not watching", and one that returns after its context ended, with nil or an error matching `ctx.Err()`, SHALL be a normal stop; neither is logged. Any other error or panic SHALL be reported once as `ErrConfigWatchFailed` naming only the type, to the hooks, and not restarted until the next start.

#### Scenario: How a watch ends
```gherkin
Given a root with InjectorOpts.Logf recording lines, a hook that records errors, a test watcher source holding "server.port" = "8080" whose Watch <ending>, and doger.BindConfig(root, "server", ServerConfig{})
When root.Start() is called and the watch ends <when>
Then root.Start() returns nil
And the recorded lines contain <logged>
And the hook received <hook>
And ConfigValue[ServerConfig].Get() still serves the committed value

Examples:
  | ending                                        | when                    | logged                                                                         | hook                                         |
  | returns nil at once                           | before Shutdown         | nothing                                                                        | nothing                                      |
  | returns ctx.Err() once ctx is done            | at root.Shutdown()      | nothing                                                                        | nothing                                      |
  | returns errors.New("inotify: token s3cret-9f2c7a41") | before Shutdown  | one line with "config watch failed" and "*errors.errorString", no "s3cret-9f2c7a41" | an error matching doger.ErrConfigWatchFailed |
  | panics with "s3cret-9f2c7a41" and no HookContainerPanic | before Shutdown | one line with "config watch failed" and "string", no "s3cret-9f2c7a41"   | an error matching doger.ErrConfigWatchFailed |
```

#### Scenario: A watch panic goes to HookContainerPanic when set
```gherkin
Given a root with a HookContainerPanic recording (point, value) and a test watcher source whose Watch panics with "s3cret-9f2c7a41"
When root.Start() is called
Then HookContainerPanic received ("config-watch", "s3cret-9f2c7a41") and root.Start() returned nil
```

<!-- cases: 1 default: the nil row is how config.DisableWatch is seen -->
<!-- cases: 2 overrides: HookContainerPanic scenario -->
<!-- cases: 3 zero values: nil return row -->
<!-- cases: 4 boundaries: returning before versus after ctx ended -->
<!-- cases: 5 errors: the error row, matchable with errors.Is -->
<!-- cases: N/A 6 duplicate: Watch is called once per start cycle -->
<!-- cases: 7 lifecycle: the container keeps serving the committed view; a later start after all members stopped calls Watch again -->
<!-- cases: N/A 8 concurrency: Watch runs on one goroutine -->
<!-- cases: 9 panics: the panic rows; the value is never logged -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Clones share config and the watcher
`Clone()` SHALL always share the parent's committed view, watcher and dispatcher, whatever the kind of its source, with no `Load` and no `SetLogf`. `CloneWithOpts` SHALL share them only when given the parent's own `ConfigSource` as the same pointer; a different source gets its own config state. One reload SHALL reach every container that shares the state.

#### Scenario: One reload reaches parent and clone
```gherkin
Given a root with <source> holding "server.port" = "8080", doger.BindConfig(root, "server", ServerConfig{}), and <clone>
And root.Start() returned nil
When the source commits "server.port" = "9090"
Then ConfigValue[ServerConfig].Get().Port is 9090 from both root and clone
And the test source's Watch was called once and its Load once

Examples:
  | source                                     | clone                                                                                                               |
  | a test watcher source (a pointer)          | clone := root.Clone()                                                                                               |
  | a test watcher source (a non-pointer value) | clone := root.Clone()                                                                                              |
  | a test watcher source (a pointer)          | clone, err := root.CloneWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: theSameSource}) with a nil err |
```

#### Scenario: A clone made during commits
```gherkin
Given a root with a test watcher source holding "server.host" = "h0" and "server.port" = "1", doger.BindConfig(root, "server", ServerConfig{}) and root.Start() returned nil
When 8 goroutines call root.Clone() and read the clone's ConfigValue[ServerConfig].Get() in a loop while the source commits 100 views, view n holding "server.host" = "h<n>" and "server.port" = "<n>"
Then every value read has Host equal to "h" followed by its Port
And after the last commit every clone reads Port 100
And the test passes under -race
```

<!-- cases: 1 default: the Clone row -->
<!-- cases: 2 overrides: CloneWithOpts with the parent's own pointer; a different source loads its own state (config-sources); a zero-size source type may compare equal as a pointer, as the godoc notes -->
<!-- cases: N/A 3 zero values: CloneWithOpts with no ConfigSource has no config state (config-sources) -->
<!-- cases: 4 boundaries: pointer versus non-pointer source under Clone() -->
<!-- cases: N/A 5 errors: Clone() cannot fail because it never loads; a clone's own load failure and ErrConfigConflict are config-sources' -->
<!-- cases: N/A 6 duplicate: one watcher per state -->
<!-- cases: 7 lifecycle: see "Every bound section takes part until its owners release it" -->
<!-- cases: 8 concurrency: the clone-during-commits scenario under -race; membership is "The watcher runs while a member is started" -->
<!-- cases: N/A 9 panics: cloning runs no user code -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Every bound section takes part until its owners release it
Every entry in the reload set SHALL be rebound at every reload and can veto it, whether or not an owner started. Its owners SHALL be the registered `ConfigValue[T]` wrappers: one is added at bind time and per clone that copies the registration, and released when the wrapper is forgotten (shutdown, override, removal) or its scope is collected. A failure SHALL fire the hooks and `Logf` of each owner container not shut down; a staging error those of each started member.

#### Scenario: Which sections gate a reload
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" and "limits.per-minute" = "10", doger.BindConfig(root, "server", ServerConfig{}) on the root, and <member> that then bound doger.BindConfig(member, "limits", LimitsConfig{})
And root.Start() returned nil and <member steps>
When the source calls changed with "server.port" = "9090" and "limits.per-minute" = "0"
Then changed returns <commit> and the root's ConfigValue[ServerConfig].Get().Port is <port>

Examples:
  | member                           | member steps                      | commit | port |
  | clone := root.Clone()            | clone.Start() returned nil        | false  | 8080 |
  | clone := root.Clone()            | clone.Start(), clone.Shutdown()   | true   | 9090 |
  | clone := root.Clone()            | nothing (never started)           | false  | 8080 |
  | clone := root.Clone()            | clone.Shutdown() (never started)  | true   | 9090 |
  | a child scope "jobs" of the root | nothing                           | false  | 8080 |
  | a child scope "jobs" of the root | jobs.Shutdown() returned          | true   | 9090 |
```

#### Scenario: A clone keeps receiving a section after its parent shut down
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" and doger.BindConfig(root, "server", ServerConfig{})
And clone := root.Clone(), root.Start() and clone.Start() returned nil
And root.Shutdown() returned
When the source commits "server.port" = "9090"
Then the clone's ConfigValue[ServerConfig].Get().Port is 9090
```

#### Scenario: A dropped clone stops vetoing once collected
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" and "limits.per-minute" = "10", doger.BindConfig(root, "server", ServerConfig{}), and a clone that bound doger.BindConfig(clone, "limits", LimitsConfig{}) and was then dropped without Shutdown
And root.Start() returned nil
When the test calls runtime.GC() and the source calls changed with "server.port" = "9090" and "limits.per-minute" = "0", retrying both until changed returns true or 100 attempts passed
Then changed returned true and the root's ConfigValue[ServerConfig].Get().Port is 9090
```

#### Scenario: Whose hooks a failure fires
```gherkin
Given a root with hook R and Logf R, holding doger.BindConfig(root, "server", ServerConfig{}) from a test watcher source holding "server.port" = "8080" and "limits.per-minute" = "10"
And clone, err := root.CloneWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: theSameSource, Logf: logC, HookConfigReloadFailed: []func(*doger.RootScope, error){hookC}}) with a nil err, made after the bind so it also owns "server"
And doger.BindConfig(clone, "limits", LimitsConfig{}) on the clone, both started, and <steps>
When the source <fails>
Then hooks <fired> are called, each once, and the warning goes to <logf>

Examples:
  | steps                     | fails                                     | fired       | logf             |
  | nothing                   | stages "limits.per-minute" = "0"          | C           | logC             |
  | nothing                   | stages "server.port" = "99999"            | R and C     | Logf R and logC  |
  | nothing                   | calls changed(ctx, nil, err)              | R and C     | Logf R and logC  |
  | clone.Shutdown() returned | stages "server.port" = "99999"            | R           | Logf R           |
  | root.Shutdown() returned  | stages "server.port" = "99999"            | C           | logC             |
```

<!-- cases: 1 default: the started-clone row -->
<!-- cases: N/A 2 overrides: ownership is not configurable: it follows the registrations, which the consumer already controls -->
<!-- cases: 3 zero values: a never-started clone's section still gates (no stale binding when it starts later) -->
<!-- cases: 4 boundaries: started, shut down, never started; clone versus child scope; one owner left versus none -->
<!-- cases: 5 errors: whose-hooks scenario; hooks are deduplicated per container; with no owner container left, the original root's hooks fire -->
<!-- cases: 6 duplicate: a clone that copied the registration is a second owner (parent-shutdown scenario) -->
<!-- cases: 7 lifecycle: the subject; runtime.AddCleanup on the owner scope is only the fallback for a clone dropped without Shutdown. Documented caveats: an OnChange subscriber that captures a clone keeps it alive; a clone dropped without Shutdown keeps vetoing until it is garbage-collected, which is not deterministic. Membership (started, not shut down) drives only the watcher and dispatcher -->
<!-- cases: 8 concurrency: owners are per-entry state outside the reload set; a reload commits when the current set is a subset of the one it rebound (removals only) and retries only for added entries, see "A bind racing a reload is never left behind" -->
<!-- cases: N/A 9 panics: owner bookkeeping runs no user code -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A bind racing a reload is never left behind
`BindConfig` SHALL bind against the committed view with no lock held and add its entry only if that view is still committed, binding again against the newer one otherwise, then register `T` and its handle with no lock held. A reload that finds entries added since it began SHALL rebind just those and abort if one fails. A bind racing a reload SHALL end with the handle holding the newer view.

#### Scenario: A bind racing a reload ends with the handle holding the newer view
```gherkin
Given a root with InjectorOpts.Logf recording lines and a test watcher source holding "server.port" = "8080" and "limits.per-minute" = "10", and doger.BindConfig(root, "server", ServerConfig{}) whose ServerConfig.Validate, when Port is 9090, closes channel "entered" and waits for channel "release"
And root.Start() returned nil and a goroutine is inside changed with next holding "server.port" = "9090" and "limits.per-minute" = "<staged>", after "entered" closed
When a second goroutine calls doger.BindConfig(root, "limits", LimitsConfig{}) and sends its result on channel "bound", and then "release" is closed
Then "bound" received a nil error before "release" was closed
And changed returns <commit>
And ConfigValue[ServerConfig].Get().Port is <port> and ConfigValue[LimitsConfig].Get().PerMinute is <limit>
And <logged>

Examples:
  | staged | commit | port | limit | logged                                                         |
  | 20     | true   | 9090 | 20    | no recorded line contains "reload failed"                      |
  | 0      | false  | 8080 | 10    | one recorded line contains "reload failed" and "limits.per-minute" |
```

#### Scenario: A reload that commits during a bind is not lost
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" and "limits.per-minute" = "10", doger.BindConfig(root, "server", ServerConfig{}), and a LimitsConfig.Validate that, only on its first call, closes channel "entered" and waits for channel "release"
And root.Start() returned nil and a goroutine is inside doger.BindConfig(root, "limits", LimitsConfig{}), after "entered" closed
When the source calls changed with next holding "server.port" = "8080" and "limits.per-minute" = "20", which returns true, and then "release" is closed
Then BindConfig returns a nil error
And ConfigValue[LimitsConfig].Get().PerMinute is 20
```

<!-- cases: N/A 1-3: the subject is the interleaving; default bind behaviour is config-binding's -->
<!-- cases: 4 boundaries: reload before the entry is added versus after -->
<!-- cases: 5 errors: an added entry that fails against the staged view aborts the reload (staged 0 row) -->
<!-- cases: N/A 6 duplicate: a conflicting registration is pre-checked before any work, see config-binding -->
<!-- cases: 7 lifecycle: bind during a reload, and a reload during a bind; the shutdown re-check after registering is config-binding's -->
<!-- cases: 8 concurrency: the channel handshakes pin both interleavings; runs under -race; BindConfig never waits for user code, and no user code (Logf included) runs under the semaphore -->
<!-- cases: N/A 9 panics: registration panics and rollback are config-binding's -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A config.New source polls for changes
A `config.New` source SHALL poll its watched files every `config.DefaultPollInterval` (10s) or `PollInterval(d)`; `PollInterval(0)` means the default and a negative interval fails `Load` with `config.ErrInvalidOption`. With `DisableWatch()`, `Watch` SHALL return nil at once. A second concurrent `Watch` on one source SHALL return `config.ErrWatching`.

#### Scenario: Poll interval and debounce
```gherkin
Given a testing/synctest bubble, a temp dir with config.yaml holding "server: {port: 8080}" and a root with InjectorOpts.ConfigSource = config.New(config.Dir(tmp), config.DotEnv(""), <option>) and doger.BindConfig(root, "server", ServerConfig{})
And root.Start() returned nil and config.yaml was rewritten to "server: {port: 9090}" at bubble time 0
When bubble time advances to <time>
Then ConfigValue[ServerConfig].Get().Port is <port>

Examples:
  | option                             | time  | port |
  | no option                          | 19s   | 8080 |
  | no option                          | 20s   | 9090 |
  | config.PollInterval(0)             | 20s   | 9090 |
  | config.PollInterval(2*time.Second) | 3s    | 8080 |
  | config.PollInterval(2*time.Second) | 4s    | 9090 |
  | config.DisableWatch()              | 1h    | 8080 |
```

#### Scenario: A negative poll interval fails construction
```gherkin
Given a temp dir with config.yaml holding "server: {port: 8080}"
When the consumer calls doger.NewWithOpts(context.Background(), &doger.InjectorOpts{ConfigSource: config.New(config.Dir(tmp), config.PollInterval(-time.Second))})
Then the root is nil and the error matches doger.ErrConfigLoadFailed and config.ErrInvalidOption
And errors.As finds a *config.OptionError with Option "PollInterval"
```

#### Scenario: One source is watched once at a time
```gherkin
Given src := config.New(config.Dir(tmp), config.DotEnv("")) after a successful src.Load(ctx)
And a goroutine is running src.Watch(ctx1, changed)
When the test calls src.Watch(ctx2, changed)
Then it returns an error matching config.ErrWatching
And after ctx1 is cancelled and the first Watch returned, src.Watch(ctx3, changed) runs normally
```

<!-- cases: 1 default: the no-option rows (10s interval, two polls) -->
<!-- cases: 2 overrides: PollInterval and DisableWatch rows -->
<!-- cases: 3 zero values: PollInterval(0) row -->
<!-- cases: 4 boundaries: one poll before the debounce completes (19s, 3s) versus at it (20s, 4s) -->
<!-- cases: 5 errors: the negative interval scenario and the concurrent Watch scenario -->
<!-- cases: N/A 6 duplicate: last PollInterval wins, as every scalar option -->
<!-- cases: 7 lifecycle: DisableWatch row; restart after the first Watch returned -->
<!-- cases: 8 concurrency: concurrent Watch scenario; synctest makes timing deterministic; the poll loop uses time.NewTicker, stopped on exit, and selects on ctx -->
<!-- cases: N/A 9 panics: polling runs no user code; layer, decoder and mapper panics are "Reloading parses safely" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: What a poll watches and reloads
The watched set SHALL be the files `Load` read plus the `.env` file only if it existed at `Load` (`DotEnv(path)` for its path, none with `DotEnv("")`), fingerprinted per file from the bytes `Load` read. A missing member, or one non-empty at `Load` and empty now, SHALL be a staging error. New files SHALL NOT be noticed, and process env SHALL be read only at `Load`.

#### Scenario: What a poll does
```gherkin
Given a testing/synctest bubble, t.Chdir(tmp), tmp/config.yaml holding "server: {port: 8080}", <dotenv at load>, clearEnvPrefix(t, "APP_") then APP_LIMITS_PER_MINUTE=10 set with t.Setenv, and a started root using config.New(config.PollInterval(time.Second)) with BindConfig for "server" and "limits" and a recording hook
When the test <change> and bubble time advances 2s
Then <result>

Examples:
  | dotenv at load       | change                                                                                   | result                                                            |
  | an empty tmp/.env    | rewrites config.yaml to "server: {port: 9090}"                                           | Port is 9090                                                      |
  | an empty tmp/.env    | rewrites .env to "APP_LIMITS_BURST=7"                                                    | Burst is 7                                                        |
  | an empty tmp/.env    | sets APP_LIMITS_BURST=9 with t.Setenv and rewrites config.yaml to "server: {port: 9091}" | Port is 9091 and Burst keeps its previous value                   |
  | an empty tmp/.env    | creates config.prod.yaml and config/config.yaml                                          | nothing is staged; Port stays 8080                                |
  | an empty tmp/.env    | deletes config.yaml                                                                      | Port stays 8080 and the hook got an error matching fs.ErrNotExist |
  | an empty tmp/.env    | truncates config.yaml to empty and keeps it empty                                        | Port stays 8080 and the hook got an error naming config.yaml      |
  | no tmp/.env          | does nothing for 1h of bubble time                                                       | no warning was logged and the hook was never called               |
  | no tmp/.env          | creates tmp/.env holding "APP_LIMITS_BURST=7"                                            | nothing is staged; Burst keeps its previous value                 |
```

#### Scenario: A Kubernetes ConfigMap swap is a change
```gherkin
Given a testing/synctest bubble, not on Windows, a temp dir where config.yaml has been a symlink to ..data/config.yaml since before Load and ..data is a symlink to v1/ holding "server: {port: 8080}"
And a started root using config.New(config.Dir(tmp), config.DotEnv(""), config.PollInterval(time.Second)) with doger.BindConfig(root, "server", ServerConfig{})
When ..data is atomically re-pointed to v2/ holding "server: {port: 9090}" and bubble time advances 2s
Then ConfigValue[ServerConfig].Get().Port is 9090
```

<!-- cases: 1 default: the config.yaml row -->
<!-- cases: 2 overrides: DotEnv(path) and DotEnv("") pick the watched .env; DisableWatch is in the previous requirement -->
<!-- cases: 3 zero values: no .env at Load is never a failure; an empty .env at Load becoming non-empty is a change -->
<!-- cases: 4 boundaries: non-empty to empty versus empty .env to non-empty -->
<!-- cases: 5 errors: deleted and truncated rows, matchable -->
<!-- cases: N/A 6 duplicate: each file is fingerprinted once per poll, over (path, length, sha256) -->
<!-- cases: 7 lifecycle: new files, including a later .env, are not noticed until the next construction -->
<!-- cases: N/A 8 concurrency: synctest; one poll at a time -->
<!-- cases: N/A 9 panics: covered by "Reloading parses safely" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A poll stages each new content once
A poll SHALL stage only when the same fingerprint, or the same failure, was seen on two consecutive polls and differs from the last staged one, whether that stage was accepted or rejected. The fingerprint SHALL cover each watched file and a hash of each added layer's last read, which is never logged.

#### Scenario: Repeated content is staged once
```gherkin
Given a testing/synctest bubble and a started root using config.New(config.Dir(tmp), config.DotEnv(""), config.PollInterval(time.Second)) over config.yaml holding "server: {port: 8080}", doger.BindConfig(root, "server", ServerConfig{}), InjectorOpts.Logf recording lines and a hook counting its calls
When config.yaml <change> and bubble time advances <time>
Then <result>

Examples:
  | change                                                                       | time | result                                                          |
  | is rewritten to "server: {port: 99999}" and kept for 5 polls                 | 6s   | the hook was called once and one warning was logged             |
  | is deleted and stays deleted for 5 polls                                     | 6s   | the hook was called once and one warning was logged             |
  | is truncated to empty just before the poll at 1s and rewritten to "server: {port: 9090}" before the poll at 2s | 3s | no reload failed, no warning was logged, and Port is 9090 |
  | is rewritten to "server: {port: 99999}", then back to "server: {port: 8080}" after 3s | 6s | the hook was called once and Port is 8080 |
```

<!-- cases: 1 default: the 5-poll rows -->
<!-- cases: N/A 2 overrides: the two-poll debounce is fixed: it rides out partial writes; PollInterval sets the cadence -->
<!-- cases: 3 zero values: an empty file seen once is not staged -->
<!-- cases: 4 boundaries: one poll versus two; a failure then a return to the committed bytes -->
<!-- cases: 5 errors: invalid content and missing file are each reported once; hook events coalesce -->
<!-- cases: 6 duplicate: the subject -->
<!-- cases: 7 lifecycle: layers are "Added layers are re-read when due" -->
<!-- cases: N/A 8 concurrency: synctest; one poll at a time -->
<!-- cases: N/A 9 panics: covered by "Reloading parses safely" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Added layers are re-read when due
A poll SHALL read an added layer when its refresh is due, `config.LayerRefresh(name, d)` (default `config.DefaultLayerRefresh`, 1 minute; 0 means the default), or when a watched file changed; a negative refresh reads it only at `Load` and on a file change. A layer whose 3 consecutive reads all differ SHALL warn once that it never settles; its changes are then not staged, but its last read is used when a file change stages.

#### Scenario: When an added layer is re-read
```gherkin
Given a testing/synctest bubble, a temp dir with config.yaml holding "server: {port: 8080}", and a layer v whose LoadLayer returns "server.port" = "8080" on its first call and "9090" from its second call on
And a started root using config.New(config.Dir(tmp), config.DotEnv(""), config.PollInterval(time.Second), config.Layer("vault", v), <option>) and doger.BindConfig(root, "server", ServerConfig{})
When <change> and bubble time advances to <time>
Then ConfigValue[ServerConfig].Get().Port is <port> and v's LoadLayer was called <reads> times

Examples:
  | option                                        | change                                               | time | port | reads |
  | no option                                     | nothing changes                                      | 59s  | 8080 | 1     |
  | no option                                     | nothing changes                                      | 61s  | 9090 | 2     |
  | config.LayerRefresh("vault", 0)               | nothing changes                                      | 61s  | 9090 | 2     |
  | config.LayerRefresh("vault", 5*time.Second)   | nothing changes                                      | 6s   | 9090 | 2     |
  | config.LayerRefresh("vault", -1)              | nothing changes                                      | 1h   | 8080 | 1     |
  | config.LayerRefresh("vault", -1)              | config.yaml is rewritten to "server: {port: 8080} # touched" at time 0 | 2s | 9090 | 2 |
```

#### Scenario: A failing added layer is retried
```gherkin
Given a testing/synctest bubble, InjectorOpts.Logf recording lines, a hook counting its calls, and a started root using config.New(config.Dir(tmp), config.DotEnv(""), config.PollInterval(time.Second), config.Layer("vault", v), config.LayerRefresh("vault", time.Second)) over config.yaml holding "server: {port: 8080}" and doger.BindConfig(root, "server", ServerConfig{})
And v's LoadLayer returns "server.port" = "8080" on its first call and then <reads>
When bubble time advances <time>
Then the hook was called <calls>, one warning was logged per call, and Port is <port>

Examples:
  | reads                                                                                            | time | calls | port |
  | fails with a value-free error on calls 2 and 3, then returns "server.port" = "9090"              | 5s   | once  | 9090 |
  | fails with a value-free error of the same type on every call from 2 on                           | 7s   | once  | 8080 |
  | fails on calls 2 and 3, returns "server.port" = "8080" on calls 4 and 5, then fails on every call from 6 on| 8s   | twice | 8080 |
```

#### Scenario: A layer that never settles
```gherkin
Given a testing/synctest bubble, InjectorOpts.Logf recording lines, and a started root using config.New(config.Dir(tmp), config.DotEnv(""), config.PollInterval(time.Second), config.Layer("vault", v), config.LayerRefresh("vault", time.Second)) over config.yaml holding "server: {port: 8080}" and doger.BindConfig(root, "server", ServerConfig{})
And v's LoadLayer returns "server.port" = "<8080 + n>" on its n-th call, n counted from 0
When <change> and bubble time advances to <time>
Then exactly one recorded line contains `layer "vault" never settles; its changes are not staged`
And ConfigValue[ServerConfig].Get() is <value>

Examples:
  | change                                                                     | time | value                                                                 |
  | nothing changes                                                            | 10s  | {Host: "", Port: 8080}                                  |
  | config.yaml is rewritten to "server: {host: h2, port: 8080}" at time 5s    | 8s   | {Host: "h2", Port: 8087}, from v's call at the 7s poll |
```

<!-- cases: 1 default: the DefaultLayerRefresh rows (59s versus 61s) -->
<!-- cases: 2 overrides: the LayerRefresh rows -->
<!-- cases: 3 zero values: LayerRefresh(name, 0) means the default -->
<!-- cases: 4 boundaries: refresh due versus not due; negative refresh with and without a file change; a layer failing once, repeatedly, and again after a success; 3 differing reads -->
<!-- cases: 5 errors: repeated errors of one type warn once until the layer succeeds again; layer contents are never logged -->
<!-- cases: N/A 6 duplicate: a repeated LayerRefresh for one name is config-files' option checks -->
<!-- cases: 7 lifecycle: the last good config is kept in every failing row; a never-settling layer is still read, and its last read is used when a file change stages -->
<!-- cases: N/A 8 concurrency: synctest; one poll at a time; a blocked layer is "Polls read like Load and stay bounded" -->
<!-- cases: N/A 9 panics: covered by "Reloading parses safely" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Polls read like Load and stay bounded
A poll SHALL read through the same reader as `Load`: each file opened without blocking and checked on the opened descriptor to be a regular file, so a FIFO never blocks, at most `MaxFileSize` bytes (default 1 MiB), following symlinks. Added layers SHALL be read under `config.StageTimeout(d)` (default `config.DefaultStageTimeout`, 10s; negative means no timeout).

#### Scenario: What a poll refuses or times out on
```gherkin
Given a testing/synctest bubble and a started root using config.New(config.Dir(tmp), config.DotEnv(""), config.PollInterval(time.Second), <options>) over config.yaml holding "server: {port: 8080}", doger.BindConfig(root, "server", ServerConfig{}) and a hook recording errors
When <change> and bubble time advances <time>
Then Port stays 8080 and the hook received <error>

Examples:
  | options                                                  | change                                                        | time | error                                          |
  | no option                                                | config.yaml is replaced by a FIFO (not on Windows)            | 2s   | an error matching config.ErrNotRegularFile naming config.yaml |
  | config.Layer("vault", v), config.LayerRefresh("vault", time.Second), where v's LoadLayer blocks from its second call on until its ctx ends | nothing changes | 8s | nothing yet |
  | config.Layer("vault", v), config.LayerRefresh("vault", time.Second), where v's LoadLayer blocks from its second call on until its ctx ends | nothing changes | 25s | an error matching context.DeadlineExceeded |
  | config.Layer("vault", v), config.LayerRefresh("vault", time.Second), config.StageTimeout(2*time.Second), where v's LoadLayer blocks from its second call on until its ctx ends | nothing changes | 8s | an error matching context.DeadlineExceeded |
```

#### Scenario: A poll applies the Load size limit
```gherkin
Given a testing/synctest bubble and a started root using config.New(config.Dir(tmp), config.DotEnv(""), config.PollInterval(time.Second), <options>) over config.yaml holding "server: {port: 8080}", doger.BindConfig(root, "server", ServerConfig{}) and a hook recording errors
When config.yaml is replaced by <content> and bubble time advances 2s
Then ConfigValue[ServerConfig].Get().Port is <port> and the hook received <error>

Examples:
  | options                       | content                                         | port | error                                     |
  | no option                     | a file of exactly 1 MiB holding "server: {port: 9090}" padded with a comment | 9090 | nothing                    |
  | no option                     | a file of 1 MiB + 1 byte, the same padded        | 8080 | an error matching config.ErrFileTooLarge  |
  | config.MaxFileSize(4 << 20)   | a 2 MiB file holding "server: {port: 9090}" padded with a comment | 9090 | nothing                  |
```

<!-- cases: 1 default: the 1 MiB rows, the FIFO row and the DefaultStageTimeout rows -->
<!-- cases: 2 overrides: the MaxFileSize and StageTimeout rows -->
<!-- cases: N/A 3 zero values: MaxFileSize(0) and StageTimeout(0) mean the default, as every config.New size and duration option (config-files) -->
<!-- cases: 4 boundaries: exactly 1 MiB versus 1 MiB + 1 byte; stage timeout 2s versus 10s (8s versus 25s); the blocking layer's first call is Load's, so construction never blocks -->
<!-- cases: 5 errors: each failing row is matchable -->
<!-- cases: N/A 6 duplicate: one reader for Load and polls -->
<!-- cases: 7 lifecycle: the last good config is kept on failure -->
<!-- cases: 8 concurrency: synctest; a blocked layer delays the poll, never a reader -->
<!-- cases: N/A 9 panics: covered by "Reloading parses safely" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Reloading parses safely
Parsing at `Load` or at a poll SHALL never crash the process: a YAML alias expansion bomb, a self-referencing anchor, nesting deeper than the depth cap, or a file larger than `MaxFileSize` (default 1 MiB) SHALL be a `*config.ParseError` or `config.ErrFileTooLarge`, failing construction at `Load` and the reload at a poll. A panic in a `LayerSource` or `DecodeFunc` during a poll SHALL be a staging error naming only its type; an `EnvMapper` panic fails the root's rebind, naming only its type.

#### Scenario: Hostile files
```gherkin
Given a temp dir whose config.yaml holds <content>
When the file is read <when>
Then <outcome>

Examples:
  | content                                                            | when                  | outcome                                                                                              |
  | a 9-level "billion laughs" alias chain                             | at construction       | NewWithOpts returns a nil root and an error matching doger.ErrConfigLoadFailed and *config.ParseError |
  | "a: &x [*x]"                                                       | at construction       | NewWithOpts returns a nil root and an error matching doger.ErrConfigLoadFailed and *config.ParseError |
  | a 2 MiB file of "k: v" lines                                       | at construction       | NewWithOpts returns a nil root and an error matching config.ErrFileTooLarge                          |
  | a 9-level "billion laughs" alias chain, written after a good Load  | at a poll (synctest)  | the reload fails, the last good config is kept, and the hook got a *config.ParseError                |
```

#### Scenario: A panic during a poll is a staging error
```gherkin
Given a testing/synctest bubble, InjectorOpts.Logf recording lines, a hook recording errors, and a started root using config.New(config.Dir(tmp), config.DotEnv(""), config.PollInterval(time.Second), <option>) over config.yaml holding "server: {port: 8080}" and doger.BindConfig(root, "server", ServerConfig{})
And <panicker> panics with "s3cret-9f2c7a41" from its second call on
When config.yaml is rewritten to "server: {port: 9090}" and bubble time advances 3s
Then Port stays 8080 and the hook received <error> whose text contains "panic of type string"
And no recorded line contains "s3cret-9f2c7a41"

Examples:
  | option                                                              | panicker      | error                 |
  | config.Layer("vault", v), config.LayerRefresh("vault", time.Second) | v's LoadLayer | a *config.LayerError  |
```

<!-- cases: 1 default: MaxFileSize default row -->
<!-- cases: N/A 2 overrides: config.MaxFileSize, config.MaxKeys and the depth cap are specified in config-files -->
<!-- cases: N/A 3 zero values: an empty file is valid at Load (config-files) -->
<!-- cases: 4 boundaries: over the 1 MiB default; caps are enforced before conversion to values -->
<!-- cases: 5 errors: every row is matchable -->
<!-- cases: N/A 6 duplicate: duplicate keys are config-files -->
<!-- cases: 7 lifecycle: at construction versus at a poll; a panic at the first Load is a Load error (config-files) -->
<!-- cases: N/A 8 concurrency: parsing is single-threaded per poll -->
<!-- cases: 9 panics: no row may crash or panic the process; user-code panics during a poll are recovered (the layer panic scenario; DecodeFunc and EnvMapper panics are config-files', and env is read only at Load); fuzz seeds include both YAML payloads and a deep TOML table -->
<!-- cases: N/A 10 compatibility: new API -->
