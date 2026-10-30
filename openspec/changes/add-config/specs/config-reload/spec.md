# Spec Delta

## Purpose

Lets a running container follow configuration changes without a restart: a watching source stages each change, the root commits it only when every bound section still binds and validates, and live handles, property reads and subscribers then see the new values together, while a failed reload keeps the last good config and warns.

## ADDED Requirements

### Requirement: A watching source stages changes for the root
When the installed `ConfigSource` also implements `ConfigWatcher`, the root SHALL call `Watch(ctx, changed)` on a goroutine it owns once the container starts. The source SHALL call `changed(ctx, next, nil)` with an immutable staged `ConfigView`, holding no lock, and SHALL adopt `next` as its own state only when `changed` returns `true`; on `false` it SHALL drop `next`.

#### Scenario: The source adopts only what the root accepts
```gherkin
Given a root created with InjectorOpts.ConfigSource set to a test watcher source holding "server.port" = "8080"
And doger.BindConfig[ServerConfig](root, "server") returned nil, where ServerConfig has Port int `validate:"min=1,max=65535"`
And root.Start() returned nil
When the test source calls changed(ctx, next, nil) with next holding "server.port" = "<staged>"
Then changed returns <commit>
And the test source recorded <adopted> adoption of next

Examples:
  | staged | commit | adopted |
  | 9090   | true   | one     |
  | 99999  | false  | no      |
  | eighty | false  | no      |
  | 65535  | true   | one     |
```

<!-- cases: 1 default: the 9090 row -->
<!-- cases: N/A 2 overrides: the contract has no options; config.Files options are covered by "config.Files polls for changes" -->
<!-- cases: 3 zero values: a next with no keys binds every section to its defaults, see "Committed values reach live handles" -->
<!-- cases: 4 boundaries: 65535 (at the max) versus 99999 -->
<!-- cases: 5 errors: the validation row and the conversion row; staging errors are "A failed reload keeps the last good config" -->
<!-- cases: N/A 6 duplicate: one staged view per change -->
<!-- cases: 7 lifecycle: Watch is called only after Start, see "The watcher runs while a member is started" -->
<!-- cases: 8 concurrency: covered by "Reads are safe during a reload" -->
<!-- cases: N/A 9 panics: covered by "A watch that stops" and "A failed reload keeps the last good config" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Every read path follows the committed view
The container's property reads SHALL go through a view the root owns. The root SHALL switch it to `next` only after every section bound, then publish every `ConfigValue`, all before `changed` returns `true`. `Lookup` through the property source, `x.Properties()`, `ExtensionProperties`, `ResolveConfig` and `ConfigValue.Get` SHALL all agree once `changed` returns.

#### Scenario: Read paths agree after a commit and after a rejection
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" and an extension "srv" whose provider reads doger.ExtensionProperties(i)
And doger.BindConfig[ServerConfig](root, "server") returned nil and root.Start() returned nil
When the test source calls changed with next holding "server.port" = "<staged>"
Then the read "<path>" returns <value>

Examples:
  | staged | path                                                   | value  |
  | 9090   | property source Lookup("server.port")                  | "9090" |
  | 9090   | doger.ResolveConfig(root, "server", ServerConfig{}).Port | 9090  |
  | 9090   | ConfigValue[ServerConfig].Get().Port                   | 9090   |
  | 9090   | ExtensionProperties of "srv" Lookup("port") via a "srv" source holding "srv.port" | "9090" |
  | 99999  | property source Lookup("server.port")                  | "8080" |
  | 99999  | doger.ResolveConfig(root, "server", ServerConfig{}).Port | 8080  |
  | 99999  | ConfigValue[ServerConfig].Get().Port                   | 8080   |
```

#### Scenario: The staged view is invisible until commit
```gherkin
Given a root with a test watcher source holding "server.port" = "8080", a BindConfig[ServerConfig] for "server", and a ServerConfig.Validate() that blocks until a channel "release" is closed only when Port is 9090
And root.Start() returned nil
And a goroutine has called changed with next holding "server.port" = "9090" and a channel "entered" was closed from inside that Validate
When the consumer calls Lookup("server.port") on the root's property source
Then it returns ("8080", true)
And after "release" is closed, changed returns true and Lookup("server.port") returns ("9090", true)
```

<!-- cases: 1 default: the commit rows -->
<!-- cases: N/A 2 overrides: not configurable, by design -->
<!-- cases: N/A 3 zero values: covered by "Committed values reach live handles" -->
<!-- cases: 4 boundaries: before commit, after commit, after rejection -->
<!-- cases: N/A 5 errors: a rejection keeps every read path on the old view (99999 rows) -->
<!-- cases: N/A 6 duplicate: one committed view per container state -->
<!-- cases: 7 lifecycle: the window between staging and commit is pinned by the "entered"/"release" handshake -->
<!-- cases: 8 concurrency: the handshake scenario; "Reads are safe during a reload" covers -race -->
<!-- cases: N/A 9 panics: covered by "A failed reload keeps the last good config" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A reload is all or nothing across sections
The root SHALL bind and validate every section registered with `BindConfig` or `BindConfigNamed` from the same staged view, and commit only when all of them succeed. No two sections SHALL ever be committed from different views.

#### Scenario: One invalid section blocks every section
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" and "limits.per-minute" = "10"
And BindConfig[ServerConfig] for "server" and BindConfig[LimitsConfig] for "limits", where LimitsConfig has PerMinute int `validate:"min=1"`
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
Given a root with a test watcher source holding "server.host" = "h0" and "server.port" = "0"
And BindConfig[ServerConfig] for "server" and root.Start() returned nil
When 8 goroutines call ConfigValue[ServerConfig].Get() and doger.ResolveConfig(root, "server", ServerConfig{}) in a loop while the source commits 100 views where view n holds "server.host" = "h<n>" and "server.port" = "<n>"
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
<!-- cases: 8 concurrency: the mixed-value scenario -->
<!-- cases: N/A 9 panics: covered by "A failed reload keeps the last good config" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Committed values reach live handles
After a commit, `ConfigValue[T].Get()` SHALL return the section bound from the committed view over the section's defaults, so a key removed from the source brings its default back. A plain `T` resolved before the reload SHALL keep its value, and a `T` invoked after the reload SHALL be the value registered at bind time.

#### Scenario: Live handle versus snapshot after a reload
```gherkin
Given a root with a test watcher source holding <before>
And doger.BindConfig(root, "server", doger.ConfigDefaults(ServerConfig{Host: "0.0.0.0", Port: 8080})) returned nil
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
<!-- cases: N/A 2 overrides: defaults come from ConfigDefaults, covered in config-binding -->
<!-- cases: 3 zero values: the no-keys row -->
<!-- cases: 4 boundaries: one key changed, one key removed, every key removed -->
<!-- cases: N/A 5 errors: failed reloads are a separate requirement -->
<!-- cases: N/A 6 duplicate: one handle per section -->
<!-- cases: 7 lifecycle: before and after the reload -->
<!-- cases: N/A 8 concurrency: covered by "A reload is all or nothing across sections" -->
<!-- cases: N/A 9 panics, 10 compatibility: new API -->

### Requirement: A failed reload keeps the last good config
When a section fails to bind or validate, or the source reports a staging error through `changed(ctx, nil, err)`, the root SHALL keep the committed view, warn once with keys and rules but no value, and call every `HookConfigReloadFailed` hook in order on the dispatcher. The warning SHALL go to `InjectorOpts.Logf` when set, otherwise to `slog.Default()`.

#### Scenario: Failures warn and call the hooks
```gherkin
Given a root created with <logf>, a test watcher source holding "server.port" = "8080" and "db.password" = "s3cret", and two hooks added with root.AddConfigReloadFailedHook that record (scope, err)
And BindConfig[ServerConfig] for "server" and root.Start() returned nil
When the source calls changed with <change>
Then changed returns false and ConfigValue[ServerConfig].Get().Port is 8080
And one warning in <sink> contains "DI: config: reload failed" and "<names>" and no warning contains "s3cret"
And both hooks received scope == root and an err matching <matches>, in the order they were added

Examples:
  | logf                         | change                                                       | sink                         | names       | matches                        |
  | InjectorOpts.Logf recording  | next holding "server.port" = "99999", "db.password" = "s3cret" | the recorded lines           | server.port | doger.ErrInvalidConfig         |
  | InjectorOpts.Logf recording  | next holding "server.port" = "s3cret"                        | the recorded lines           | server.port | doger.ErrInvalidConfigValue    |
  | InjectorOpts.Logf recording  | nil next and err = &config.ParseError{File: "config.yaml", Line: 3, Category: "syntax"} | the recorded lines | config.yaml:3 | *config.ParseError via errors.As |
  | no Logf                      | next holding "server.port" = "99999"                         | a slog.Default handler set with t.Cleanup restore | server.port | doger.ErrInvalidConfig |
```

#### Scenario: A hook that panics does not stop reloads
```gherkin
Given a root with InjectorOpts.Logf recording lines, a test watcher source, BindConfig[ServerConfig] for "server", a first hook that panics with "boom" and a second hook that records its calls
And root.Start() returned nil
When the source calls changed with "server.port" = "99999" and then with "server.port" = "9090"
Then the first changed returns false, one recorded line contains "HookConfigReloadFailed panicked" and the panic value's type "string", and the second hook was called once
And the second changed returns true and ConfigValue[ServerConfig].Get().Port is 9090
```

<!-- cases: 1 default: the no-Logf row warns through slog.Default with no hooks needed -->
<!-- cases: 2 overrides: InjectorOpts.Logf rows; hooks added through InjectorOpts.HookConfigReloadFailed behave like AddConfigReloadFailedHook -->
<!-- cases: 3 zero values: nil hooks warn only; a nil fn passed to AddConfigReloadFailedHook is ignored -->
<!-- cases: 4 boundaries: zero, one and two hooks -->
<!-- cases: 5 errors: validation, conversion and staging-error rows, each matchable -->
<!-- cases: N/A 6 duplicate: hooks append -->
<!-- cases: 7 lifecycle: the watcher keeps running after a failure -->
<!-- cases: N/A 8 concurrency: hooks run on the dispatcher, see "Notifications run on one dispatcher" -->
<!-- cases: 9 panics: a panicking hook is recovered, logged by the panic value's type, and the next hook still runs -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Subscribers are notified when their section changed
After a commit, each `ConfigValue[T].OnChange` subscriber SHALL be called once with the new value only when the section's raw key/value set under its prefix changed. Every handle SHALL be published before the first subscriber of that commit runs. A `StaticConfig` handle and the zero `ConfigValue[T]` SHALL never notify.

#### Scenario: Who is notified
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" and "limits.burst" = "5", BindConfig for "server" and "limits", and one OnChange subscriber on each handle
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
When a root holding BindConfig[LimitsConfig] commits a changed "limits" section
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
<!-- cases: N/A 9 panics: covered by "Subscriber failures are contained" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Stopping a subscription
`OnChange(fn)` SHALL return a `stop` func that is safe to call any number of times; once `stop` returns, `fn` SHALL never be called again, including later in a notification round already in progress. A nil `fn` SHALL be ignored and return a no-op `stop`.

#### Scenario: Stop semantics
```gherkin
Given a root with BindConfig[ServerConfig] for "server", subscribers A and B added in that order on its handle, and root.Start() returned nil
And <setup>
When the source commits "server.port" = "<port>"
Then A is called <a> and B is called <b>

Examples:
  | setup                                      | port | a     | b     |
  | nothing                                    | 9090 | once  | once  |
  | A's stop was called before the commit      | 9090 | never | once  |
  | A's stop was called twice                  | 9090 | never | once  |
  | A calls B's stop from inside its callback  | 9090 | once  | never |
  | B calls its own stop from inside its callback, then a second commit to 9191 happens | 9090 | twice | once |
```

#### Scenario: A nil callback is ignored
```gherkin
Given h is the ConfigValue[ServerConfig] of a started root with BindConfig for "server"
When the consumer calls stop := h.OnChange(nil) and the source commits "server.port" = "9090"
Then nothing panics and stop() returns without effect
```

<!-- cases: 1 default: the nothing row -->
<!-- cases: N/A 2 overrides: not configurable -->
<!-- cases: 3 zero values: the nil fn scenario -->
<!-- cases: 4 boundaries: stop before a round, during a round, twice -->
<!-- cases: N/A 5 errors: stop returns nothing -->
<!-- cases: 6 duplicate: double stop row -->
<!-- cases: 7 lifecycle: stop from inside a callback, mid-round -->
<!-- cases: 8 concurrency: stop called while the dispatcher runs another subscriber is the mid-round row -->
<!-- cases: N/A 9 panics: nil fn does not panic -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Notifications run on one dispatcher
Subscribers and `HookConfigReloadFailed` hooks SHALL run on one dispatcher goroutine per config state, never on the watcher goroutine, in bind order of their sections. When commits outpace a slow subscriber, the dispatcher SHALL coalesce to the latest committed value per section, so a subscriber never receives a stale value after a newer one.

#### Scenario: A slow subscriber receives the latest value
```gherkin
Given a testing/synctest bubble with a root, a test watcher source, BindConfig[ServerConfig] for "server", and a subscriber that blocks on channel "go" and records each Port it receives
And root.Start() returned nil
When the source commits "server.port" = "1", then "2", then "3" while the subscriber is blocked on its first call, and then "go" is closed
Then the recorded ports are [1, 3]
And every commit returned true without waiting for the subscriber
```

<!-- cases: 1 default: one dispatcher per config state -->
<!-- cases: N/A 2 overrides: not configurable -->
<!-- cases: N/A 3 zero values: no subscribers means no dispatch work -->
<!-- cases: 4 boundaries: one value in flight, two coalesced -->
<!-- cases: N/A 5 errors: none -->
<!-- cases: N/A 6 duplicate: one dispatcher shared by a root and its clones, see "Clones share config and the watcher" -->
<!-- cases: 7 lifecycle: the dispatcher stops with the watcher, see "Shutdown is bounded by its context" -->
<!-- cases: 8 concurrency: commits never wait for subscribers -->
<!-- cases: N/A 9 panics: covered by "Subscriber failures are contained" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Subscriber failures are contained
A panicking subscriber SHALL be recovered and logged with the panic value's type only, and the other subscribers of that round SHALL still be called.

#### Scenario: A panicking subscriber
```gherkin
Given a root with InjectorOpts.Logf recording lines, BindConfig[ServerConfig] for "server", a first subscriber that panics with "s3cret-boom" and a second that records its calls
And root.Start() returned nil
When the source commits "server.port" = "9090"
Then the second subscriber is called once with Port 9090
And one recorded line contains "OnChange subscriber panicked" and "string", and no line contains "s3cret-boom"
```

<!-- cases: N/A 1-4: a single failure mode -->
<!-- cases: N/A 5 errors: subscribers return nothing -->
<!-- cases: N/A 6 duplicate: n/a -->
<!-- cases: N/A 7 lifecycle: the next commit notifies normally -->
<!-- cases: N/A 8 concurrency: dispatcher scenario -->
<!-- cases: 9 panics: the subject; the panic value is never printed -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Callbacks may call back into the container
A subscriber or a `HookConfigReloadFailed` hook SHALL be able to call `OnChange`, `BindConfig`, `ResolveConfig` and `Shutdown` on the same container without deadlock.

#### Scenario: Re-entrant calls from a callback
```gherkin
Given a testing/synctest bubble with a root, a test watcher source holding "server.port" = "8080", BindConfig[ServerConfig] for "server", and a <callback> that calls <call>
And root.Start() returned nil
When the source <trigger>
Then <call> returns <result> and synctest.Wait() returns, so no goroutine is blocked

Examples:
  | callback   | call                                                 | trigger                          | result                                    |
  | subscriber | h.OnChange(func(ServerConfig) {})                    | commits "server.port" = "9090"   | a stop func                               |
  | subscriber | doger.BindConfig[LimitsConfig](root, "limits")       | commits "server.port" = "9090"   | nil                                       |
  | subscriber | doger.ResolveConfig(root, "server", ServerConfig{})  | commits "server.port" = "9090"   | Port 9090                                 |
  | subscriber | root.Shutdown()                                      | commits "server.port" = "9090"   | a report with Succeed true                |
  | hook       | root.Shutdown()                                      | calls changed with "server.port" = "99999" | a report with Succeed true      |
```

<!-- cases: 1 default: the OnChange row (spike 2's deadlock) -->
<!-- cases: N/A 2 overrides: not configurable -->
<!-- cases: N/A 3 zero values: covered elsewhere -->
<!-- cases: 4 boundaries: subscriber versus hook -->
<!-- cases: N/A 5 errors: each call succeeds -->
<!-- cases: N/A 6 duplicate: n/a -->
<!-- cases: 7 lifecycle: Shutdown from inside the dispatcher stops the watcher without waiting on the dispatcher itself -->
<!-- cases: 8 concurrency: the subject; synctest.Wait proves no goroutine is stuck -->
<!-- cases: N/A 9 panics: none expected -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Shutdown is bounded by its context
`ShutdownWithContext(ctx)` SHALL stop the watcher and the dispatcher and wait for them only until `ctx` ends. When `ctx` ends first, the report SHALL fail with an error matching `ctx.Err()`, and the goroutines SHALL exit as soon as the blocked callback returns.

#### Scenario: A blocked callback does not hang Shutdown
```gherkin
Given a testing/synctest bubble with a root, BindConfig[ServerConfig] for "server", and a <blocker> that blocks until channel "release" is closed
And root.Start() returned nil and the source has committed or rejected a change that reached the blocker
When the consumer calls root.ShutdownWithContext with a context that times out after 1s
Then it returns after 1s of bubble time with report.Succeed false and an error matching context.DeadlineExceeded
And after "release" is closed, synctest.Wait() returns and goleak reports no goroutine

Examples:
  | blocker                                     |
  | OnChange subscriber                         |
  | HookConfigReloadFailed hook                 |
  | ServerConfig.Validate() during a rebind     |
```

<!-- cases: 1 default: Shutdown() uses context.Background and waits until the callback returns, as today -->
<!-- cases: N/A 2 overrides: the ctx is the override -->
<!-- cases: N/A 3 zero values: a nil ctx is not accepted by ShutdownWithContext today -->
<!-- cases: 4 boundaries: callback returns before versus after ctx ends -->
<!-- cases: 5 errors: the deadline row matches context.DeadlineExceeded -->
<!-- cases: N/A 6 duplicate: double Shutdown is in "The watcher runs while a member is started" -->
<!-- cases: 7 lifecycle: the subject -->
<!-- cases: 8 concurrency: synctest bubble, goleak after release -->
<!-- cases: N/A 9 panics: covered elsewhere -->
<!-- cases: N/A 10 compatibility: Shutdown's signature is unchanged -->

### Requirement: Reads are safe during a reload
`Lookup`, `Keys`, `ConfigValue.Get`, `ResolveConfig` and extension property reads SHALL be safe to call concurrently with commits and rejections.

#### Scenario: Concurrent reads under reload
```gherkin
Given a root with a test watcher source, BindConfig[ServerConfig] for "server" and root.Start() returned nil
When 8 goroutines call Lookup("server.port"), Keys("server"), ConfigValue[ServerConfig].Get() and doger.ResolveConfig(root, "server", ServerConfig{}) in a loop while the source calls changed 200 times alternating valid and invalid ports
Then the test passes under -race
And at least one commit and one rejection happened
```

<!-- cases: N/A 1-7: concurrency only -->
<!-- cases: 8 concurrency: the subject (spike 3); the fake source makes it deterministic in count -->
<!-- cases: N/A 9 panics, 10 compatibility -->

### Requirement: The watcher runs while a member is started
The watcher and dispatcher SHALL start at the first successful `Start` of any container sharing the config state (a root and its sharing clones) and stop when the last started member shuts down. Membership SHALL be tracked per container, so `Shutdown` without `Start`, and a second `Shutdown`, change nothing.

#### Scenario: Watcher lifecycle
```gherkin
Given a testing/synctest bubble with a root using a test watcher source and clone := root.Clone()
When the consumer runs <steps>
Then the test source's Watch is <state>
And after the final step, synctest.Wait() returns and goleak reports <leak>

Examples:
  | steps                                                        | state                                   | leak                 |
  | nothing                                                      | never called                            | no goroutine         |
  | root.Start()                                                 | running                                 | the watch goroutine is live (stopped in cleanup) |
  | root.Start(), root.Shutdown()                                | stopped, its ctx done                   | no goroutine         |
  | root.Start(), root.Shutdown(), root.Shutdown()               | stopped once                            | no goroutine         |
  | root.Shutdown()                                              | never called                            | no goroutine         |
  | root.Start(), clone.Start(), clone.Shutdown()                | running                                 | the watch goroutine is live (stopped in cleanup) |
  | root.Start(), clone.Start(), clone.Shutdown(), root.Shutdown() | stopped                               | no goroutine         |
  | clone.Start(), root.Shutdown()                               | running (root was never started)        | the watch goroutine is live (stopped in cleanup) |
  | root.Start(), clone.Shutdown()                               | running (clone was never started)       | the watch goroutine is live (stopped in cleanup) |
```

<!-- cases: 1 default: root Start/Shutdown row -->
<!-- cases: N/A 2 overrides: config.DisableWatch is in "config.Files polls for changes" -->
<!-- cases: 3 zero values: the nothing row starts no goroutine -->
<!-- cases: 4 boundaries: one member, two members, unbalanced calls -->
<!-- cases: N/A 5 errors: Watch errors are "A watch that stops" -->
<!-- cases: 6 duplicate: double Shutdown row -->
<!-- cases: 7 lifecycle: the subject (spike 1) -->
<!-- cases: 8 concurrency: goleak in every row -->
<!-- cases: N/A 9 panics: covered by "A watch that stops" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A watch that stops
A `Watch` that returns nil before its context ends SHALL mean "not watching" and SHALL NOT be logged. A `Watch` that returns an error or panics SHALL be logged once as `ErrConfigWatch` naming only the error's or panic value's type, passed to the `HookConfigReloadFailed` hooks, and not restarted.

#### Scenario: How a watch ends
```gherkin
Given a root with InjectorOpts.Logf recording lines, a hook that records errors, and a test watcher source whose Watch <ending>
When root.Start() is called and the watch ends
Then root.Start() returns nil
And the recorded lines contain <logged>
And the hook received <hook>
And ConfigValue[ServerConfig].Get() still serves the committed value

Examples:
  | ending                                                   | logged                                              | hook                                      |
  | returns nil at once                                      | nothing                                             | nothing                                   |
  | returns errors.New("inotify: token s3cret")              | one line with "config watch stopped" and "*errors.errorString", no "s3cret" | an error matching doger.ErrConfigWatch |
  | panics with "s3cret"                                     | one line with "config watch stopped" and "string", no "s3cret"              | an error matching doger.ErrConfigWatch |
```

<!-- cases: 1 default: the nil row is how config.DisableWatch is seen -->
<!-- cases: N/A 2 overrides: n/a -->
<!-- cases: 3 zero values: nil return row -->
<!-- cases: N/A 4 boundaries: n/a -->
<!-- cases: 5 errors: the error row, matchable with errors.Is -->
<!-- cases: N/A 6 duplicate: Watch is called once per start cycle -->
<!-- cases: 7 lifecycle: the container keeps serving the committed view -->
<!-- cases: N/A 8 concurrency: n/a -->
<!-- cases: 9 panics: the panic row; the value is never printed -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Clones share config and the watcher
`Clone()` and a `CloneWithOpts` given the parent's own `ConfigSource` SHALL share the committed view, the watcher and the dispatcher, so one reload reaches both. A section a clone or a child scope bound SHALL leave the reload set when that container or scope shuts down, and its failures SHALL fire that container's hooks and `Logf`.

#### Scenario: One reload reaches parent and clone
```gherkin
Given a root with a test watcher source holding "server.port" = "8080", BindConfig[ServerConfig] for "server", and clone := <clone>
And root.Start() returned nil
When the source commits "server.port" = "9090"
Then ConfigValue[ServerConfig].Get().Port is 9090 from both root and clone
And the test source's Watch was called once

Examples:
  | clone                                                                       |
  | root.Clone()                                                                |
  | root.CloneWithOpts(&doger.InjectorOpts{ConfigSource: theSameSource})        |
```

#### Scenario: A shut-down member's sections leave the reload set
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" and "limits.per-minute" = "10", BindConfig[ServerConfig] for "server" on the root, and <member> that bound BindConfig[LimitsConfig] for "limits" with `validate:"min=1"`
And root.Start() and <member start> returned nil
And <member> was shut down
When the source calls changed with "server.port" = "9090" and "limits.per-minute" = "0"
Then changed returns true and the root's ConfigValue[ServerConfig].Get().Port is 9090

Examples:
  | member                           | member start  |
  | clone := root.Clone()            | clone.Start() |
  | a child scope "jobs" of the root | nothing       |
```

#### Scenario: Whose hooks a failure fires
```gherkin
Given a root with hook R and Logf R, clone := root.CloneWithOpts(&doger.InjectorOpts{ConfigSource: theSameSource, Logf: logC, HookConfigReloadFailed: hooks C}), both started
And BindConfig[ServerConfig] for "server" on the root and BindConfig[LimitsConfig] for "limits" on the clone
When the source <fails>
Then hooks <fired> are called and the warning goes to <logf>

Examples:
  | fails                                                         | fired | logf   |
  | stages "limits.per-minute" = "0"                              | C     | logC   |
  | stages "server.port" = "99999"                                | R     | Logf R |
  | calls changed(ctx, nil, err)                                  | R     | Logf R |
```

<!-- cases: 1 default: the Clone row -->
<!-- cases: 2 overrides: CloneWithOpts with the parent's own source; a different source is a separate config state (container-construction) -->
<!-- cases: N/A 3 zero values: CloneWithOpts with no ConfigSource has no config state (config-sources) -->
<!-- cases: 4 boundaries: clone versus child scope -->
<!-- cases: 5 errors: whose-hooks scenario -->
<!-- cases: N/A 6 duplicate: one watcher per state -->
<!-- cases: 7 lifecycle: a shut-down member's sections leave the reload set -->
<!-- cases: N/A 8 concurrency: membership in "The watcher runs while a member is started" -->
<!-- cases: N/A 9 panics: n/a -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Binding during a reload is never left behind
`BindConfig` and `BindConfigNamed` SHALL bind and register a section under the reload lock against the committed view, so a section bound while a reload is in progress is bound from that reload's result once it commits, never from the view it replaced.

#### Scenario: BindConfig waits for an in-progress reload
```gherkin
Given a root with a test watcher source holding "server.port" = "8080" and "limits.per-minute" = "10", BindConfig[ServerConfig] for "server" whose Validate closes channel "entered" and waits for channel "release" when Port is 9090
And root.Start() returned nil and a goroutine is inside changed with next holding "server.port" = "9090" and "limits.per-minute" = "20", after "entered" closed
When a second goroutine calls doger.BindConfig[LimitsConfig](root, "limits") and then "release" is closed
Then changed returns true
And BindConfig returns nil after changed returned
And ConfigValue[LimitsConfig].Get().PerMinute is 20
```

<!-- cases: N/A 1-6: one interleaving -->
<!-- cases: 7 lifecycle: bind during a reload -->
<!-- cases: 8 concurrency: channel handshake pins the interleaving; runs under -race -->
<!-- cases: N/A 9 panics, 10 compatibility -->

### Requirement: config.Files polls for changes
`config.Files` SHALL poll its watched files every `DefaultPollInterval` (10s), or `PollInterval(d)`; `PollInterval(0)` means the default and a negative interval fails `Load` with `config.ErrInvalidOption`. A change SHALL be staged only after the same new fingerprint is seen on two consecutive polls. With `DisableWatch()`, `Watch` SHALL return nil at once.

#### Scenario: Poll interval and debounce
```gherkin
Given a testing/synctest bubble, a temp dir with config.yaml holding "server: {port: 8080}" and a root with InjectorOpts.ConfigSource = config.Files(config.Dir(tmp), <option>) and BindConfig[ServerConfig] for "server"
And root.Start() returned nil and config.yaml was rewritten to "server: {port: 9090}" at bubble time 0
When bubble time advances to <time>
Then ConfigValue[ServerConfig].Get().Port is <port>

Examples:
  | option                           | time  | port |
  | no option                        | 19s   | 8080 |
  | no option                        | 20s   | 9090 |
  | config.PollInterval(0)           | 20s   | 9090 |
  | config.PollInterval(2*time.Second) | 3s  | 8080 |
  | config.PollInterval(2*time.Second) | 4s  | 9090 |
  | config.DisableWatch()            | 1h    | 8080 |
```

#### Scenario: A negative poll interval fails construction
```gherkin
Given a temp dir with config.yaml holding "server: {port: 8080}"
When the consumer calls doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: config.Files(config.Dir(tmp), config.PollInterval(-time.Second))})
Then the root is nil and the error matches doger.ErrConfigLoad and config.ErrInvalidOption
```

#### Scenario: A partial write is not staged
```gherkin
Given a testing/synctest bubble and a started root using config.Files(config.Dir(tmp), config.PollInterval(time.Second)) over config.yaml holding "server: {port: 8080}" and BindConfig[ServerConfig] for "server"
When config.yaml is truncated to empty just before the poll at 1s and rewritten to "server: {port: 9090}" before the poll at 2s
Then at 2s no reload was staged and at 3s ConfigValue[ServerConfig].Get().Port is 9090
And no warning was logged
```

<!-- cases: 1 default: the no-option rows (10s interval, two polls) -->
<!-- cases: 2 overrides: PollInterval and DisableWatch rows -->
<!-- cases: 3 zero values: PollInterval(0) row -->
<!-- cases: 4 boundaries: one poll before the debounce completes (19s, 3s) versus at it (20s, 4s) -->
<!-- cases: 5 errors: the negative interval scenario -->
<!-- cases: N/A 6 duplicate: last PollInterval wins, as every scalar option -->
<!-- cases: 7 lifecycle: DisableWatch row -->
<!-- cases: 8 concurrency: synctest makes timing deterministic -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: What a poll reloads
The watched set SHALL be the files `Load` read plus `./.env`, fingerprinted from the bytes `Load` read. A changed member SHALL be staged; a missing member, or one non-empty at `Load` and empty now, SHALL be a staging error. New files SHALL NOT be noticed. Process env SHALL be read only at `Load`.

#### Scenario: What a poll does
```gherkin
Given a testing/synctest bubble, t.Chdir(tmp), tmp/config.yaml holding "server: {port: 8080}", an empty tmp/.env, APP_LIMITS_BURST unset, and a started root using config.Files(config.PollInterval(time.Second)) with BindConfig for "server" and "limits" and a recording hook
When the test <change> and bubble time advances 2s
Then <result>

Examples:
  | change                                                                   | result                                                                 |
  | rewrites config.yaml to "server: {port: 9090}"                           | Port is 9090                                                           |
  | rewrites .env to "APP_LIMITS_BURST=7"                                    | Burst is 7                                                             |
  | sets APP_LIMITS_BURST=9 with t.Setenv and rewrites config.yaml to "server: {port: 9091}" | Port is 9091 and Burst keeps its previous value     |
  | creates config.prod.yaml and config/config.yaml                          | nothing is staged; Port stays 8080                                     |
  | deletes config.yaml                                                      | Port stays 8080 and the hook got an error matching fs.ErrNotExist      |
  | truncates config.yaml to empty and keeps it empty                        | Port stays 8080 and the hook got an error naming config.yaml           |
```

#### Scenario: A Kubernetes ConfigMap swap is a change
```gherkin
Given a testing/synctest bubble, not on Windows, a temp dir where config.yaml is a symlink to ..data/config.yaml and ..data is a symlink to v1/ holding "server: {port: 8080}"
And a started root using config.Files(config.Dir(tmp), config.PollInterval(time.Second)) with BindConfig[ServerConfig] for "server"
When ..data is atomically re-pointed to v2/ holding "server: {port: 9090}" and bubble time advances 2s
Then ConfigValue[ServerConfig].Get().Port is 9090
```

<!-- cases: 1 default: the config.yaml row -->
<!-- cases: N/A 2 overrides: the watched set is not configurable; DisableWatch is in the previous requirement -->
<!-- cases: 3 zero values: an empty .env at Load becoming non-empty is a change, not a failure -->
<!-- cases: 4 boundaries: non-empty to empty versus empty .env to non-empty -->
<!-- cases: 5 errors: deleted and truncated rows, matchable -->
<!-- cases: N/A 6 duplicate: n/a -->
<!-- cases: 7 lifecycle: new files are not noticed until the next construction -->
<!-- cases: N/A 8 concurrency: synctest -->
<!-- cases: N/A 9 panics: covered by "Reloading parses safely" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Reloading parses safely
Parsing at `Load` or at a poll SHALL never crash the process: a YAML alias expansion bomb, a self-referencing anchor, or a file larger than `MaxFileSize` (default 4 MiB) SHALL be a `*config.ParseError` or `config.ErrFileTooLarge`, failing construction at `Load` and failing the reload at a poll.

#### Scenario: Hostile files
```gherkin
Given a temp dir whose config.yaml holds <content>
When the file is read <when>
Then <outcome>

Examples:
  | content                                                | when                         | outcome                                                                                   |
  | a 9-level "billion laughs" alias chain                 | at construction              | NewWithOpts returns a nil root and an error matching doger.ErrConfigLoad and *config.ParseError |
  | "a: &x [*x]"                                           | at construction              | NewWithOpts returns a nil root and an error matching doger.ErrConfigLoad and *config.ParseError |
  | 5 MiB of "k: v" lines                                  | at construction              | NewWithOpts returns a nil root and an error matching config.ErrFileTooLarge              |
  | a 9-level "billion laughs" alias chain, written after a good Load | at a poll (synctest) | the reload fails, the last good config is kept, and the hook got a *config.ParseError |
```

<!-- cases: 1 default: MaxFileSize default row -->
<!-- cases: N/A 2 overrides: config.MaxFileSize is specified in config-files -->
<!-- cases: N/A 3 zero values: an empty file is valid at Load (config-files) -->
<!-- cases: 4 boundaries: just over the 4 MiB default -->
<!-- cases: 5 errors: every row is matchable -->
<!-- cases: N/A 6 duplicate: duplicate keys are config-files -->
<!-- cases: 7 lifecycle: at construction versus at a poll -->
<!-- cases: N/A 8 concurrency: n/a -->
<!-- cases: 9 panics: no row may crash or panic the process; fuzz seeds include both YAML payloads -->
<!-- cases: N/A 10 compatibility: new API -->
