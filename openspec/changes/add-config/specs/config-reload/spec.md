# Spec Delta

## Purpose

Lets a running container follow configuration changes without a restart: a watching source stages each change, the root commits it only when every bound section still binds and validates, and live handles, property reads and subscribers then see the new values together, while a failed reload keeps the last good config and warns once.

## ADDED Requirements

<!-- Shared fixtures (root package tests, config_reload_test.go): "a test watcher source" is a root-local test type implementing ConfigSource and ConfigWatcher; its Watch hands `changed` to the test and records every adoption. ServerConfig is {Host string; Port int `validate:"min=1,max=65535"`}; LimitsConfig is {PerMinute int `validate:"min=1"`; Burst int}. Tests that use testing/synctest build the root inside the bubble and are wrapped in testWithTimeout outside it; results are received on channels, never inferred from a mutex deadlock. Rows that use config.Files are config-module tests (config/watch_test.go). -->

### Requirement: A watching source stages changes for the root
When the installed `ConfigSource` also implements `ConfigWatcher`, the root SHALL call `Watch(ctx, changed)` on a goroutine it owns once a member starts. The source SHALL call `changed(ctx, next, nil)` with an immutable staged `ConfigView`, holding no lock, and SHALL adopt `next` as its own state only when `changed` returns `true`; on `false` it SHALL drop `next`.

#### Scenario: The source adopts only what the root accepts
```gherkin
Given a root created with InjectorOpts.ConfigSource set to a test watcher source holding "server.port" = "8080"
And doger.BindConfig(root, "server", ServerConfig{}) returned nil
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

<!-- cases: 1 default: the 9090 row -->
<!-- cases: N/A 2 overrides: the contract has no options; config.Files options are in "config.Files polls for changes" -->
<!-- cases: 3 zero values: a next with no keys binds every section to its defaults, see "Committed values reach live handles" -->
<!-- cases: 4 boundaries: 65535 (at the max) versus 99999 -->
<!-- cases: 5 errors: the validation row and the conversion row; staging errors are "A failed reload keeps the last good config" -->
<!-- cases: N/A 6 duplicate: one staged view per change -->
<!-- cases: 7 lifecycle: Watch is called only after Start, see "The watcher runs while a member is started" -->
<!-- cases: 8 concurrency: covered by "Reads are safe during a reload" -->
<!-- cases: N/A 9 panics: covered by "A watch that stops" and "Callback panics follow HookContainerPanic" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Every read path follows the committed view
Property reads SHALL go through a view the root owns, switched to `next` only after every section bound, then every `ConfigValue` published, all before `changed` returns `true`. `ResolveConfig` and `BindProperties` SHALL bind from one view per call, and a `Properties` value SHALL read from the view committed when it was obtained, so no read mixes two views.

#### Scenario: Read paths agree after a commit and after a rejection
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" and "srv.port" = "8080"
And an extension "srv" whose private scope holds a transient "*app.Probe" whose provider records doger.ExtensionProperties(i) Lookup("port")
And doger.BindConfig(root, "server", ServerConfig{}) returned nil and root.Start() returned nil
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
<!-- cases: N/A 2 overrides: the read rule is not configurable, by design -->
<!-- cases: N/A 3 zero values: covered by "Committed values reach live handles" -->
<!-- cases: 4 boundaries: before commit, after commit, after rejection -->
<!-- cases: 5 errors: a rejection keeps every read path on the old view (99999 rows) -->
<!-- cases: N/A 6 duplicate: one committed view per config state -->
<!-- cases: 7 lifecycle: the window between staging and commit is pinned by the "entered"/"release" handshake -->
<!-- cases: 8 concurrency: the handshake scenario and the BindProperties scenario under -race -->
<!-- cases: N/A 9 panics: covered by "Callback panics follow HookContainerPanic" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A reload is all or nothing across sections
The root SHALL bind and validate every participating section from the same staged view, with no lock held while user code (`Validate`, `UnmarshalText`, a `ConfigValidator`, the view's `Lookup` and `Keys`) runs, and commit only when all of them succeed. No two sections SHALL ever be committed from different views.

#### Scenario: One invalid section blocks every section
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" and "limits.per-minute" = "10"
And doger.BindConfig(root, "server", ServerConfig{}) and doger.BindConfig(root, "limits", LimitsConfig{}) returned nil
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
And doger.BindConfig(root, "server", ServerConfig{}) returned nil and root.Start() returned nil
When 8 goroutines call ConfigValue[ServerConfig].Get() and doger.ResolveConfig(root, "server", ServerConfig{}) in a loop while the source commits 100 views, view n holding "server.host" = "h<n>" and "server.port" = "<n>"
Then every value read has Host equal to "h" followed by its Port
And the test passes under -race
```

<!-- cases: 1 default: the valid row -->
<!-- cases: N/A 2 overrides: all-or-nothing is not configurable, by design -->
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
And doger.BindConfig(root, "server", ServerConfig{Host: "0.0.0.0", Port: 8080}) returned nil
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
<!-- cases: 3 zero values: the no-keys row -->
<!-- cases: 4 boundaries: one key changed, one key removed, every key removed -->
<!-- cases: N/A 5 errors: failed reloads are a separate requirement -->
<!-- cases: N/A 6 duplicate: one handle per section -->
<!-- cases: 7 lifecycle: before and after the reload -->
<!-- cases: N/A 8 concurrency: covered by "A reload is all or nothing across sections" -->
<!-- cases: N/A 9 panics: binding panics are config-binding's -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A failed reload keeps the last good config
When a participating section fails to bind or validate, or the source reports a staging error through `changed(ctx, nil, err)`, the root SHALL keep the committed view, return `false`, warn once with keys, rules and value-free text only, and enqueue the `HookConfigReloadFailed` hooks. The warning SHALL go to `InjectorOpts.Logf` when set, otherwise to `slog.Default()`.

#### Scenario: Failures warn and call the hooks
```gherkin
Given a root created with <logf>, a test watcher source holding "server.port" = "8080" and "db.password" = "s3cret", and two hooks added with root.AddConfigReloadFailedHook that record (scope, err)
And doger.BindConfig(root, "server", ServerConfig{}) returned nil and root.Start() returned nil
When the source calls changed with <change>
Then changed returns false and ConfigValue[ServerConfig].Get().Port is 8080
And one warning in <sink> contains "DI: config: reload failed" and "<text>", and no warning contains "s3cret"
And both hooks received scope == root and an err matching <matches>, in the order they were added

Examples:
  | logf                        | change                                                                      | sink                                              | text                      | matches                     |
  | InjectorOpts.Logf recording | next holding "server.port" = "99999", "db.password" = "s3cret"             | the recorded lines                                | server.port               | doger.ErrInvalidConfig      |
  | InjectorOpts.Logf recording | next holding "server.port" = "s3cret"                                       | the recorded lines                                | server.port               | doger.ErrConfigConversion   |
  | InjectorOpts.Logf recording | nil next and err = a root-local *stageError implementing doger.ValueFreeError with text "config.yaml:3: syntax error" | the recorded lines | config.yaml:3: syntax error | *stageError via errors.As |
  | InjectorOpts.Logf recording | nil next and err = errors.New("vault token s3cret")                         | the recorded lines                                | *errors.errorString       | the errors.New value        |
  | no Logf                     | next holding "server.port" = "99999"                                        | a slog.Default handler set with t.Cleanup restore | server.port               | doger.ErrInvalidConfig      |
```

#### Scenario: Hooks receive the start context's values, never a cancelled context
```gherkin
Given a root with a test watcher source, doger.BindConfig(root, "server", ServerConfig{}), and a hook that records ctx.Value(traceKey) and ctx.Err()
And root.StartWithContext was called with a context carrying traceKey = "t-1" that was cancelled after Start returned
When the source calls changed with next holding "server.port" = "99999"
Then the hook recorded "t-1" and a nil ctx.Err()
```

<!-- cases: 1 default: the no-Logf row warns through slog.Default with no hooks needed -->
<!-- cases: 2 overrides: InjectorOpts.Logf rows; hooks added through InjectorOpts.HookConfigReloadFailed behave like AddConfigReloadFailedHook -->
<!-- cases: 3 zero values: nil hooks warn only; a nil fn passed to AddConfigReloadFailedHook is ignored -->
<!-- cases: 4 boundaries: zero, one and two hooks -->
<!-- cases: 5 errors: validation, conversion, value-free staging error (text shown) and plain staging error (type only) rows, each matchable -->
<!-- cases: N/A 6 duplicate: repeated identical failures are coalesced, see "A poll stages each new content once" -->
<!-- cases: 7 lifecycle: the watcher keeps running after a failure; hook ctx outlives Start's cancellation -->
<!-- cases: 8 concurrency: hooks run on the dispatcher, see "Notifications run on one dispatcher" -->
<!-- cases: N/A 9 panics: covered by "Callback panics follow HookContainerPanic" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Subscribers are notified when their section changed
After a commit, each `ConfigValue[T].OnChange` subscriber SHALL be called once with the new value only when the section's raw key/value set under its prefix changed. Every handle SHALL be published before the first subscriber of that commit runs. A `StaticConfig` handle and the zero `ConfigValue[T]` SHALL never notify.

#### Scenario: Who is notified
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" and "limits.burst" = "5", BindConfig for "server" and "limits" with zero-value defaults, and one OnChange subscriber on each handle
And root.Start() returned nil
When the source commits <change>
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
Given a root with BindConfig for "server" and "limits" and a server subscriber that records ConfigValue[LimitsConfig].Get().Burst
And root.Start() returned nil
When the source commits "server.port" = "9090" and "limits.burst" = "6"
Then the recorded Burst is 6
```

#### Scenario: Static and zero handles never notify
```gherkin
Given h := <handle> and a subscriber added with h.OnChange
When a root holding doger.BindConfig(root, "limits", LimitsConfig{}) commits a changed "limits" section
Then the subscriber is never called and the returned stop func is safe to call twice

Examples:
  | handle                                             |
  | doger.StaticConfig(LimitsConfig{PerMinute: 10})    |
  | doger.ConfigValue[LimitsConfig]{}                  |
```

<!-- cases: 1 default: one subscriber per handle -->
<!-- cases: N/A 2 overrides: notification is not configurable -->
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
Given a root with doger.BindConfig(root, "server", ServerConfig{}), subscribers A and B added in that order on its handle, and root.Start() returned nil
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
Given a root with doger.BindConfig(root, "server", ServerConfig{}) and subscriber B that closes "entered" and blocks on channel "go", and root.Start() returned nil
And the source committed "server.port" = "9090" and "entered" is closed
When the test calls B's stop from its own goroutine, closes "go", and the source commits "server.port" = "9191"
Then stop returned before "go" was closed, B's first call finished with Port 9090, and B was called once in total
```

#### Scenario: A nil callback is ignored
```gherkin
Given h is the ConfigValue[ServerConfig] of a started root with doger.BindConfig(root, "server", ServerConfig{})
And stop := h.OnChange(nil)
When the source commits "server.port" = "9090"
Then nothing panics and stop() returns without effect
```

<!-- cases: 1 default: the nothing row -->
<!-- cases: N/A 2 overrides: not configurable -->
<!-- cases: 3 zero values: the nil fn scenario -->
<!-- cases: 4 boundaries: stop before a round, during a round, twice -->
<!-- cases: N/A 5 errors: stop returns nothing -->
<!-- cases: 6 duplicate: double stop row -->
<!-- cases: 7 lifecycle: stop from inside a callback, mid-round -->
<!-- cases: 8 concurrency: stop from another goroutine while the dispatcher runs another subscriber, and while it runs the stopped subscriber itself -->
<!-- cases: N/A 9 panics: nil fn does not panic -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Notifications run on one dispatcher
Subscribers and `HookConfigReloadFailed` hooks SHALL run on one dispatcher goroutine per config state, never on the watcher goroutine, in bind order of their sections. A commit SHALL enqueue without blocking; when commits outpace a slow subscriber, the dispatcher SHALL coalesce to the latest value per section, and queued hook events SHALL coalesce to the latest error.

#### Scenario: A slow subscriber receives the latest value
```gherkin
Given a testing/synctest bubble with a root, a test watcher source, doger.BindConfig(root, "server", ServerConfig{}), and a subscriber that blocks on channel "go" and records each Port it receives
And root.Start() returned nil
When the source commits "server.port" = "1", then "2", then "3" while the subscriber is blocked on its first call, and then "go" is closed
Then the recorded ports are [1, 3]
And every changed returned true without waiting for the subscriber
```

<!-- cases: 1 default: one dispatcher per config state -->
<!-- cases: N/A 2 overrides: not configurable -->
<!-- cases: N/A 3 zero values: no subscribers means no dispatch work -->
<!-- cases: 4 boundaries: one value in flight, two coalesced -->
<!-- cases: N/A 5 errors: hook coalescing is pinned in "A poll stages each new content once" -->
<!-- cases: N/A 6 duplicate: one dispatcher shared by a root and its clones, see "Clones share config and the watcher" -->
<!-- cases: 7 lifecycle: the dispatcher stops with the watcher, see "Shutdown stops reloading before services and is bounded by its context" -->
<!-- cases: 8 concurrency: commits never wait for subscribers -->
<!-- cases: N/A 9 panics: covered by "Callback panics follow HookContainerPanic" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Callback panics follow HookContainerPanic
A panic in a subscriber, a `HookConfigReloadFailed` hook or `Watch` SHALL be recovered and passed to `HookContainerPanic` with the point `"config-on-change"`, `"config-reload-failed"` or `"config-watch"` when it is set, and otherwise logged once with the panic value's type only. The other callbacks of that round SHALL still run.

#### Scenario: A panicking callback
```gherkin
Given a root with InjectorOpts.Logf recording lines, <panic hook>, doger.BindConfig(root, "server", ServerConfig{}), a first <callback> that panics with "s3cret-boom" and a second one that records its calls
And root.Start() returned nil
When the source <trigger>
Then the second <callback> is called once
And <outcome>
And no recorded line contains "s3cret-boom"

Examples:
  | panic hook                                   | callback   | trigger                                  | outcome                                                                          |
  | no HookContainerPanic                        | subscriber | commits "server.port" = "9090"           | one recorded line contains "OnChange subscriber panicked" and "string"           |
  | no HookContainerPanic                        | hook       | calls changed with "server.port" = "99999" | one recorded line contains "HookConfigReloadFailed panicked" and "string"     |
  | HookContainerPanic recording (point, value)  | subscriber | commits "server.port" = "9090"           | HookContainerPanic received ("config-on-change", "s3cret-boom") and nothing was logged |
  | HookContainerPanic recording (point, value)  | hook       | calls changed with "server.port" = "99999" | HookContainerPanic received ("config-reload-failed", "s3cret-boom") and nothing was logged |
```

<!-- cases: 1 default: the no-HookContainerPanic rows log by type only -->
<!-- cases: 2 overrides: the HookContainerPanic rows; the "config-watch" point is in "A watch that stops" -->
<!-- cases: N/A 3 zero values: no callbacks, no panics -->
<!-- cases: 4 boundaries: subscriber versus hook -->
<!-- cases: N/A 5 errors: callbacks return nothing -->
<!-- cases: N/A 6 duplicate: each panic is reported once -->
<!-- cases: 7 lifecycle: the next commit notifies normally -->
<!-- cases: N/A 8 concurrency: covered by "Notifications run on one dispatcher" -->
<!-- cases: 9 panics: the subject; the panic value is passed only to HookContainerPanic, whose godoc says it may hold a secret, and is never logged; a panic of HookContainerPanic itself propagates, as for the health scheduler -->
<!-- cases: N/A 10 compatibility: new API; HookContainerPanic gains three points -->

### Requirement: Callbacks may call back into the container
Subscribers, hooks and the user code a rebind runs SHALL be able to call `OnChange`, `BindConfig`, `ResolveConfig` and `go root.Shutdown()` without deadlock, because no lock is held while they run. A synchronous `Shutdown` from a callback SHALL be documented as unsupported (it waits on itself, as for Starters).

#### Scenario: Re-entrant calls from a callback
```gherkin
Given a testing/synctest bubble with a root, a test watcher source holding "server.port" = "8080", doger.BindConfig(root, "server", ServerConfig{}), and a <callback> that calls <call> and sends its result on a channel
And root.Start() returned nil
When the source <trigger>
Then the channel receives <result>

Examples:
  | callback                              | call                                                       | trigger                                    | result                     |
  | subscriber                            | h.OnChange(func(ServerConfig) {})                          | commits "server.port" = "9090"             | a stop func                |
  | subscriber                            | doger.BindConfig(root, "limits", LimitsConfig{})           | commits "server.port" = "9090"             | nil                        |
  | subscriber                            | doger.ResolveConfig(root, "server", ServerConfig{})        | commits "server.port" = "9090"             | Port 9090                  |
  | ServerConfig.Validate during a rebind | doger.BindConfig(root, "limits", LimitsConfig{})           | calls changed with "server.port" = "9090"  | nil                        |
  | ServerConfig.Validate during a rebind | doger.ResolveConfig(root, "server", ServerConfig{})        | calls changed with "server.port" = "9090"  | Port 8080 (the committed view) |
  | subscriber                            | go root.Shutdown(), forwarding its report                  | commits "server.port" = "9090"             | a report with Succeed true |
  | hook                                  | go root.Shutdown(), forwarding its report                  | calls changed with "server.port" = "99999" | a report with Succeed true |
```

<!-- cases: 1 default: the OnChange row (spike 2's deadlock) -->
<!-- cases: N/A 2 overrides: not configurable -->
<!-- cases: N/A 3 zero values: covered elsewhere -->
<!-- cases: 4 boundaries: subscriber, hook and rebind user code -->
<!-- cases: N/A 5 errors: each call succeeds -->
<!-- cases: N/A 6 duplicate: a section bound from a callback joins the reload set like any other -->
<!-- cases: 7 lifecycle: `go root.Shutdown()` from a callback completes; a synchronous Shutdown from a callback is documented as unsupported and has no scenario -->
<!-- cases: 8 concurrency: the subject; results arrive on channels and the bubble test is bounded by testWithTimeout -->
<!-- cases: N/A 9 panics: none expected -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Shutdown stops reloading before services and is bounded by its context
When the last started member begins shutting down, the root SHALL cancel the watch, make `changed` return `false` and enqueue nothing from then on, shut the services down, and then wait for the watcher and dispatcher goroutines only until `ctx` ends, failing the report with an error matching `ctx.Err()` when it ends first.

#### Scenario: A blocked callback does not hang Shutdown
```gherkin
Given a testing/synctest bubble with a root, a service "*app.DB" whose Shutdown is recorded, doger.BindConfig(root, "server", ServerConfig{}), and a <blocker> that blocks until channel "release" is closed
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

#### Scenario: Changes after shutdown began are dropped
```gherkin
Given a root with a test watcher source that kept the changed func, doger.BindConfig(root, "server", ServerConfig{}) and a recording subscriber
And root.Start() and then root.Shutdown() returned
When the test source calls the kept changed with next holding "server.port" = "9090"
Then changed returns false, the subscriber is never called and ConfigValue[ServerConfig].Get().Port is 8080
```

<!-- cases: 1 default: Shutdown() uses context.Background and waits until the callback returns, as today -->
<!-- cases: N/A 2 overrides: the ctx is the override -->
<!-- cases: 3 zero values: a nil ctx means context.Background, as for ShutdownWithContext today -->
<!-- cases: 4 boundaries: callback returns before versus after ctx ends -->
<!-- cases: 5 errors: the deadline rows match context.DeadlineExceeded -->
<!-- cases: N/A 6 duplicate: double Shutdown is in "The watcher runs while a member is started" -->
<!-- cases: 7 lifecycle: the subject; services shut down before the bounded wait, so none is skipped -->
<!-- cases: 8 concurrency: synctest bubble with testWithTimeout outside it; goroutines exit once released -->
<!-- cases: N/A 9 panics: covered elsewhere -->
<!-- cases: N/A 10 compatibility: Shutdown's signature is unchanged -->

### Requirement: Reads are safe during a reload
`Lookup`, `Keys`, `ConfigValue.Get`, `ResolveConfig` and extension property reads SHALL be safe to call concurrently with commits and rejections.

#### Scenario: Concurrent reads under reload
```gherkin
Given a root with a test watcher source, doger.BindConfig(root, "server", ServerConfig{}) and root.Start() returned nil
When 8 goroutines call ConfigValue[ServerConfig].Get(), doger.ResolveConfig(root, "server", ServerConfig{}) and an extension view's Lookup("port") and Keys("") in a loop while the source calls changed 200 times alternating valid and invalid ports
Then the test passes under -race
And at least one commit and one rejection happened
```

<!-- cases: N/A 1-7: concurrency only -->
<!-- cases: 8 concurrency: the subject (spike 3); the fake source makes it deterministic in count -->
<!-- cases: N/A 9 panics, 10 compatibility: concurrency only -->

### Requirement: The watcher runs while a member is started
The watcher and dispatcher SHALL start at the first successful `Start` of any container sharing the config state and stop when the last started member shuts down. Membership SHALL count only successful starts, so `Shutdown` without `Start` and a second `Shutdown` change nothing. A start after every member stopped SHALL call `Watch` again once the previous `Watch` returned.

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

<!-- cases: 1 default: root Start/Shutdown row -->
<!-- cases: N/A 2 overrides: config.DisableWatch is in "config.Files polls for changes" -->
<!-- cases: 3 zero values: the nothing row starts no goroutine -->
<!-- cases: 4 boundaries: one member, two members, unbalanced calls, restart -->
<!-- cases: N/A 5 errors: Watch errors are "A watch that stops" -->
<!-- cases: 6 duplicate: double Shutdown row -->
<!-- cases: 7 lifecycle: the subject (spike 1) -->
<!-- cases: 8 concurrency: goleak at the end of every row -->
<!-- cases: N/A 9 panics: covered by "A watch that stops" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A watch that stops
A `Watch` that returns nil before its context ends SHALL mean "not watching", and one that returns after its context ended, with nil or an error matching `ctx.Err()`, SHALL be a normal stop; neither is logged. Any other error or panic SHALL be reported once as `ErrConfigWatchFailed` naming only the type, to the hooks, and not restarted until the next start.

#### Scenario: How a watch ends
```gherkin
Given a root with InjectorOpts.Logf recording lines, a hook that records errors, a test watcher source whose Watch <ending>, and doger.BindConfig(root, "server", ServerConfig{})
When root.Start() is called and the watch ends <when>
Then root.Start() returns nil
And the recorded lines contain <logged>
And the hook received <hook>
And ConfigValue[ServerConfig].Get() still serves the committed value

Examples:
  | ending                                        | when                    | logged                                                                         | hook                                         |
  | returns nil at once                           | before Shutdown         | nothing                                                                        | nothing                                      |
  | returns ctx.Err() once ctx is done            | at root.Shutdown()      | nothing                                                                        | nothing                                      |
  | returns errors.New("inotify: token s3cret")   | before Shutdown         | one line with "config watch failed" and "*errors.errorString", no "s3cret"    | an error matching doger.ErrConfigWatchFailed |
  | panics with "s3cret" and no HookContainerPanic | before Shutdown        | one line with "config watch failed" and "string", no "s3cret"                 | an error matching doger.ErrConfigWatchFailed |
```

#### Scenario: A watch panic goes to HookContainerPanic when set
```gherkin
Given a root with a HookContainerPanic recording (point, value) and a test watcher source whose Watch panics with "s3cret"
When root.Start() is called
Then HookContainerPanic received ("config-watch", "s3cret") and root.Start() returned nil
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
`Clone()`, and `CloneWithOpts` given the parent's own `ConfigSource` as the same pointer, SHALL share the committed view, the watcher and the dispatcher, so one reload reaches both. A non-pointer source, or a different one, SHALL get its own config state.

#### Scenario: One reload reaches parent and clone
```gherkin
Given a root with a test watcher source (a pointer) holding "server.port" = "8080", doger.BindConfig(root, "server", ServerConfig{}), and <clone>
And root.Start() returned nil
When the source commits "server.port" = "9090"
Then ConfigValue[ServerConfig].Get().Port is 9090 from both root and clone
And the test source's Watch was called once

Examples:
  | clone                                                                                           |
  | clone := root.Clone()                                                                           |
  | clone, err := root.CloneWithOpts(&doger.InjectorOpts{ConfigSource: theSameSource}) with a nil err |
```

<!-- cases: 1 default: the Clone row -->
<!-- cases: 2 overrides: CloneWithOpts with the parent's own pointer; a different or non-pointer source loads its own state (config-sources) -->
<!-- cases: N/A 3 zero values: CloneWithOpts with no ConfigSource has no config state (config-sources) -->
<!-- cases: N/A 4 boundaries: covered by the two rows -->
<!-- cases: N/A 5 errors: a clone's own load failure is config-sources' -->
<!-- cases: N/A 6 duplicate: one watcher per state -->
<!-- cases: 7 lifecycle: see "Only live started owners gate a reload" -->
<!-- cases: N/A 8 concurrency: membership in "The watcher runs while a member is started" -->
<!-- cases: N/A 9 panics: n/a -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Only live started owners gate a reload
Each section's reload entry SHALL hold its owners (the container that bound it and every clone that copied the registration) weakly. A section SHALL take part in a reload, and veto it, only while at least one owner is started and not shut down, and a child scope's section only until that scope shuts down. A failure SHALL fire the hooks and `Logf` of the owner that bound the failing section; a staging error fires the root's.

#### Scenario: Which sections gate a reload
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" and "limits.per-minute" = "10", doger.BindConfig(root, "server", ServerConfig{}) on the root, and <member> that bound doger.BindConfig(member, "limits", LimitsConfig{})
And root.Start() returned nil and <member steps>
When the source calls changed with "server.port" = "9090" and "limits.per-minute" = "0"
Then changed returns <commit> and the root's ConfigValue[ServerConfig].Get().Port is <port>

Examples:
  | member                           | member steps                      | commit | port |
  | clone := root.Clone()            | clone.Start() returned nil        | false  | 8080 |
  | clone := root.Clone()            | clone.Start(), clone.Shutdown()   | true   | 9090 |
  | clone := root.Clone()            | nothing (never started)           | true   | 9090 |
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

#### Scenario: Whose hooks a failure fires
```gherkin
Given a root with hook R and Logf R, and clone, err := root.CloneWithOpts(&doger.InjectorOpts{ConfigSource: theSameSource, Logf: logC, HookConfigReloadFailed: []func(context.Context, *doger.RootScope, error){hookC}}) with a nil err, both started
And doger.BindConfig(root, "server", ServerConfig{}) on the root and doger.BindConfig(clone, "limits", LimitsConfig{}) on the clone
When the source <fails>
Then hooks <fired> are called and the warning goes to <logf>

Examples:
  | fails                                     | fired | logf   |
  | stages "limits.per-minute" = "0"          | C     | logC   |
  | stages "server.port" = "99999"            | R     | Logf R |
  | calls changed(ctx, nil, err)              | R     | Logf R |
```

<!-- cases: 1 default: the started-clone row -->
<!-- cases: N/A 2 overrides: ownership is not configurable -->
<!-- cases: 3 zero values: a never-started clone's section does not gate -->
<!-- cases: 4 boundaries: started, shut down, never started; clone versus child scope -->
<!-- cases: 5 errors: whose-hooks scenario -->
<!-- cases: 6 duplicate: a clone that copied the registration is a second owner (parent-shutdown scenario) -->
<!-- cases: 7 lifecycle: the subject; a garbage-collected clone stops owning its entries (weak pointers, pruned at each commit) -->
<!-- cases: N/A 8 concurrency: covered by "A reload is all or nothing across sections" -->
<!-- cases: N/A 9 panics: n/a -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A bind racing a reload is never left behind
`BindConfig` SHALL bind against the committed view with no lock held, then add its entry to the reload set, then register `T` and its handle with no lock held. A reload that commits after the entry was added SHALL rebind that entry, so a bind racing a reload ends with the handle holding the newer view.

#### Scenario: A bind racing a reload ends with the handle holding the newer view
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" and "limits.per-minute" = "10", and doger.BindConfig(root, "server", ServerConfig{}) whose ServerConfig.Validate, when Port is 9090, closes channel "entered" and waits for channel "release"
And root.Start() returned nil and a goroutine is inside changed with next holding "server.port" = "9090" and "limits.per-minute" = "20", after "entered" closed
When a second goroutine calls doger.BindConfig(root, "limits", LimitsConfig{}) and sends its result on channel "bound", and then "release" is closed
Then "bound" received nil before "release" was closed
And changed returns true
And ConfigValue[LimitsConfig].Get().PerMinute is 20
```

<!-- cases: N/A 1-6: one interleaving -->
<!-- cases: 7 lifecycle: bind during a reload -->
<!-- cases: 8 concurrency: the channel handshake pins the interleaving; runs under -race; BindConfig never waits for the reload -->
<!-- cases: N/A 9 panics: registration panics are config-binding's -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: config.Files polls for changes
`config.Files` SHALL poll its watched files every `config.DefaultPollInterval` (10s) or `PollInterval(d)`; `PollInterval(0)` means the default and a negative interval fails `Load` with `config.ErrInvalidOption`. With `DisableWatch()`, `Watch` SHALL return nil at once. A second concurrent `Watch` on one source SHALL return `config.ErrWatching`.

#### Scenario: Poll interval and debounce
```gherkin
Given a testing/synctest bubble, a temp dir with config.yaml holding "server: {port: 8080}" and a root with InjectorOpts.ConfigSource = config.Files(config.Dir(tmp), config.DotEnv(""), <option>) and doger.BindConfig(root, "server", ServerConfig{})
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
When the consumer calls doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: config.Files(config.Dir(tmp), config.PollInterval(-time.Second))})
Then the root is nil and the error matches doger.ErrConfigLoadFailed and config.ErrInvalidOption
```

#### Scenario: One source is watched once at a time
```gherkin
Given src := config.Files(config.Dir(tmp), config.DotEnv("")) after a successful src.Load(ctx)
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
<!-- cases: 8 concurrency: concurrent Watch scenario; synctest makes timing deterministic -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: What a poll watches and reloads
The watched set SHALL be the files `Load` read plus the `.env` file only if it existed at `Load` (`DotEnv(path)` for its path, none with `DotEnv("")`), fingerprinted from the bytes `Load` read. A missing member, or one non-empty at `Load` and empty now, SHALL be a staging error. New files SHALL NOT be noticed, and process env SHALL be read only at `Load`.

#### Scenario: What a poll does
```gherkin
Given a testing/synctest bubble, t.Chdir(tmp), tmp/config.yaml holding "server: {port: 8080}", <dotenv at load>, APP_LIMITS_BURST unset, and a started root using config.Files(config.PollInterval(time.Second)) with BindConfig for "server" and "limits" and a recording hook
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
And a started root using config.Files(config.Dir(tmp), config.DotEnv(""), config.PollInterval(time.Second)) with doger.BindConfig(root, "server", ServerConfig{})
When ..data is atomically re-pointed to v2/ holding "server: {port: 9090}" and bubble time advances 2s
Then ConfigValue[ServerConfig].Get().Port is 9090
```

<!-- cases: 1 default: the config.yaml row -->
<!-- cases: 2 overrides: DotEnv(path) and DotEnv("") pick the watched .env; DisableWatch is in the previous requirement -->
<!-- cases: 3 zero values: no .env at Load is never a failure; an empty .env at Load becoming non-empty is a change -->
<!-- cases: 4 boundaries: non-empty to empty versus empty .env to non-empty -->
<!-- cases: 5 errors: deleted and truncated rows, matchable -->
<!-- cases: N/A 6 duplicate: n/a -->
<!-- cases: 7 lifecycle: new files, including a later .env, are not noticed until the next construction -->
<!-- cases: N/A 8 concurrency: synctest -->
<!-- cases: N/A 9 panics: covered by "Reloading parses safely" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A poll stages each new content once
A poll SHALL stage only when the same fingerprint, or the same failure, was seen on two consecutive polls and differs from the last staged one, whether that stage was accepted or rejected. A rejection caused by an added layer's error SHALL NOT be remembered, so the next poll retries it.

#### Scenario: Repeated content is staged once
```gherkin
Given a testing/synctest bubble and a started root using config.Files(config.Dir(tmp), config.DotEnv(""), config.PollInterval(time.Second)) over config.yaml holding "server: {port: 8080}", doger.BindConfig(root, "server", ServerConfig{}), InjectorOpts.Logf recording lines and a hook counting its calls
When config.yaml <change> and bubble time advances <time>
Then <result>

Examples:
  | change                                                                       | time | result                                                          |
  | is rewritten to "server: {port: 99999}" and kept for 5 polls                 | 6s   | the hook was called once and one warning was logged             |
  | is deleted and stays deleted for 5 polls                                     | 6s   | the hook was called once and one warning was logged             |
  | is truncated to empty just before the poll at 1s and rewritten to "server: {port: 9090}" before the poll at 2s | 3s | no reload failed, no warning was logged, and Port is 9090 |
  | is rewritten to "server: {port: 99999}", then back to "server: {port: 8080}" after 3s | 6s | the hook was called once and Port is 8080 |
```

#### Scenario: A failing added layer is retried
```gherkin
Given a testing/synctest bubble and a started root using config.Files(config.Dir(tmp), config.DotEnv(""), config.PollInterval(time.Second), config.Layer("vault", v)) where v's Load returns "server.port" = "8080" at Load
And v's next Load fails once with a value-free error and then returns "server.port" = "9090"
When config.yaml is touched with the same bytes rewritten and bubble time advances 4s
Then the hook was called once and Port is 9090
```

<!-- cases: 1 default: the 5-poll rows -->
<!-- cases: N/A 2 overrides: the debounce is not configurable; PollInterval sets the cadence -->
<!-- cases: 3 zero values: an empty file seen once is not staged -->
<!-- cases: 4 boundaries: one poll versus two; a failure then a return to the committed bytes -->
<!-- cases: 5 errors: invalid content and missing file are each reported once; hook events coalesce -->
<!-- cases: 6 duplicate: the subject -->
<!-- cases: 7 lifecycle: a layer error is retried at the next poll -->
<!-- cases: N/A 8 concurrency: synctest -->
<!-- cases: N/A 9 panics: covered by "Reloading parses safely" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Polls read like Load and stay bounded
A poll SHALL read through the same reader as `Load`: regular files only (checked with `os.Stat` before opening, so a FIFO never blocks), at most `MaxFileSize` bytes, and following symlinks. Added layers SHALL be re-read at each staging under `config.StageTimeout(d)` (default `config.DefaultStageTimeout`, 10s; negative means no timeout).

#### Scenario: What a poll refuses or times out on
```gherkin
Given a testing/synctest bubble and a started root using config.Files(config.Dir(tmp), config.DotEnv(""), config.PollInterval(time.Second), <options>) over config.yaml holding "server: {port: 8080}", doger.BindConfig(root, "server", ServerConfig{}) and a hook recording errors
When <change> and bubble time advances <time>
Then Port stays 8080 and the hook received <error>

Examples:
  | options                                                  | change                                                        | time | error                                          |
  | no option                                                | config.yaml is replaced by 5 MiB of "k: v" lines              | 2s   | an error matching config.ErrFileTooLarge       |
  | no option                                                | config.yaml is replaced by a FIFO (not on Windows)            | 2s   | an error naming config.yaml as not a regular file |
  | config.Layer("vault", v) where v's Load blocks until its ctx ends | config.yaml is rewritten to "server: {port: 9090}" twice | 13s  | an error matching context.DeadlineExceeded     |
  | config.Layer("vault", v), config.StageTimeout(2*time.Second) where v's Load blocks until its ctx ends | config.yaml is rewritten to "server: {port: 9090}" twice | 5s | an error matching context.DeadlineExceeded |
```

<!-- cases: 1 default: MaxFileSize and DefaultStageTimeout rows -->
<!-- cases: 2 overrides: StageTimeout row -->
<!-- cases: N/A 3 zero values: StageTimeout(0) means the default, as every config.Files duration option (config-files) -->
<!-- cases: 4 boundaries: 5 MiB over the 4 MiB default; timeout 2s versus 10s -->
<!-- cases: 5 errors: each row is matchable -->
<!-- cases: N/A 6 duplicate: n/a -->
<!-- cases: 7 lifecycle: the last good config is kept in every row -->
<!-- cases: N/A 8 concurrency: synctest -->
<!-- cases: N/A 9 panics: covered by "Reloading parses safely" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Reloading parses safely
Parsing at `Load` or at a poll SHALL never crash the process: a YAML alias expansion bomb, a self-referencing anchor, nesting deeper than the depth cap, or a file larger than `MaxFileSize` (default 4 MiB) SHALL be a `*config.ParseError` or `config.ErrFileTooLarge`, failing construction at `Load` and failing the reload at a poll.

#### Scenario: Hostile files
```gherkin
Given a temp dir whose config.yaml holds <content>
When the file is read <when>
Then <outcome>

Examples:
  | content                                                            | when                  | outcome                                                                                              |
  | a 9-level "billion laughs" alias chain                             | at construction       | NewWithOpts returns a nil root and an error matching doger.ErrConfigLoadFailed and *config.ParseError |
  | "a: &x [*x]"                                                       | at construction       | NewWithOpts returns a nil root and an error matching doger.ErrConfigLoadFailed and *config.ParseError |
  | 5 MiB of "k: v" lines                                              | at construction       | NewWithOpts returns a nil root and an error matching config.ErrFileTooLarge                          |
  | a 9-level "billion laughs" alias chain, written after a good Load  | at a poll (synctest)  | the reload fails, the last good config is kept, and the hook got a *config.ParseError                |
```

<!-- cases: 1 default: MaxFileSize default row -->
<!-- cases: N/A 2 overrides: config.MaxFileSize and the depth cap are specified in config-files -->
<!-- cases: N/A 3 zero values: an empty file is valid at Load (config-files) -->
<!-- cases: 4 boundaries: just over the 4 MiB default -->
<!-- cases: 5 errors: every row is matchable -->
<!-- cases: N/A 6 duplicate: duplicate keys are config-files -->
<!-- cases: 7 lifecycle: at construction versus at a poll -->
<!-- cases: N/A 8 concurrency: n/a -->
<!-- cases: 9 panics: no row may crash or panic the process; fuzz seeds include both YAML payloads and a deep TOML table -->
<!-- cases: N/A 10 compatibility: new API -->
