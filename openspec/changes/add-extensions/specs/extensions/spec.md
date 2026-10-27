# Spec Delta

## Purpose

Defines the Extension SPI: a contract third-party modules implement so that any Go library's capability can be installed into a doger container with one `Extend` call, with private internals, replaceable defaults and full installer control.

## ADDED Requirements

### Requirement: Extend installs an extension into a private scope by default
`Extend(root, ext, opts...)` SHALL call `ext.Extend(x)` once with an `ExtensionContext` whose `Scope()` is a new child scope of the root named `ext:<Name()>`, or `ext:<Name()>/<instance>` when the extension has an instance key. Services registered there SHALL be invisible from the root except through exports made with `ExtensionExport`/`ExtensionExportNamed`.

#### Scenario: Default private layout with an export
```gherkin
Given a root scope
And an extension "redis" whose Extend registers lazy "*redis.Pool" and lazy "*redis.Client" in x.Scope() and calls doger.ExtensionExport[*redis.Client](x)
When the consumer calls doger.Extend(root, redisExt)
Then it returns nil
And the root has a child scope named "<scope>"
And invoking "*redis.Client" from the root succeeds
And invoking "*redis.Pool" from the root fails with an error matching ErrServiceNotFound

Examples:
  | instance | scope              |
  | (none)   | ext:redis          |
  | cache    | ext:redis/cache    |
```

#### Scenario: The context describes the installation
```gherkin
Given a root scope
And an extension "redis" with instance key "<instance>" whose Extend records x.Root(), x.Instance() and x.Scope().Name()
When the consumer calls doger.Extend(root, redisExt)
Then the recorded root is the root scope, the instance is "<instance>" and the scope name is "<scope>"

Examples:
  | instance | scope           |
  |          | ext:redis       |
  | cache    | ext:redis/cache |
```

#### Scenario: A nil root means DefaultRootScope
```gherkin
Given DefaultRootScope is reset to an empty root scope
When the consumer calls doger.Extend(nil, redisExt)
Then DefaultRootScope has a child scope named "ext:redis"
```

<!-- cases: 2 overrides: covered by "The author and the installer choose the placement" -->
<!-- cases: 3 zero values: nil root scenario; nil extension and empty name are covered by "Extend validates its inputs" -->
<!-- cases: 4 boundaries: with and without an instance key -->
<!-- cases: 5 errors: covered by "Extend validates its inputs" and "A failing Extend leaves nothing behind" -->
<!-- cases: 6 duplicate: covered by "Identity and repeated installation" -->
<!-- cases: 7 lifecycle: covered by "Extend follows the container phase" and "Extension services follow the container lifecycle" -->
<!-- cases: 8 concurrency: covered by "Concurrent Extend calls install each identity once" -->
<!-- cases: 9 panics: covered by "A failing Extend leaves nothing behind" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: The author and the installer choose the placement
An extension implementing `ExtensionPlacement` with `PlacementFlat` SHALL register into the root's own scope; `PlacementPrivate`, or no such method, SHALL use the private scope. The installer's `Flatten()` SHALL force the root, and `ExtendScopeName(name)` SHALL rename the private scope. When flattened, `ExtensionExport` SHALL do nothing and return nil.

#### Scenario: Placement by author and installer
```gherkin
Given a root scope
And an extension "redis" declaring placement <author> whose Extend registers lazy "*redis.Pool" and calls doger.ExtensionExport[*redis.Pool](x)
When the consumer calls doger.Extend(root, redisExt, <options>)
Then "*redis.Pool" is registered in <where>
And invoking "*redis.Pool" from the root succeeds

Examples:
  | author            | options                          | where                        |
  | (none)            | (none)                           | child scope "ext:redis"      |
  | PlacementPrivate  | (none)                           | child scope "ext:redis"      |
  | PlacementFlat     | (none)                           | the root's own scope         |
  | (none)            | doger.Flatten()                  | the root's own scope         |
  | PlacementFlat     | doger.ExtendScopeName("cache")   | the root's own scope         |
  | (none)            | doger.ExtendScopeName("cache")   | child scope "cache"          |
```

<!-- cases: 1 default: first row -->
<!-- cases: 2 overrides: author, installer and both rows -->
<!-- cases: 3 zero values: ExtendScopeName("") is an error, covered by "Extend validates its inputs" -->
<!-- cases: N/A 4 boundaries: placement has two values -->
<!-- cases: N/A 5 errors: placement cannot fail -->
<!-- cases: 6 duplicate: a scope name collision is covered by "Identity and repeated installation" -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: placement is decided before Extend runs -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Extend validates its inputs
`Extend` SHALL return an error matching `ErrInvalidExtension` and install nothing when the extension is nil or a typed nil, `Name()` or a non-empty instance key is not lowercase alphanumeric words joined by single dashes (`[a-z0-9]+(-[a-z0-9]+)*`), `Name()`, `Instance()` or `Placement()` panics, or `ExtendScopeName` is given an empty name. Validation SHALL NOT panic. `Require` SHALL validate its input the same way.

#### Scenario: Invalid extensions are rejected
```gherkin
Given a root scope
When the consumer calls doger.Extend(root, <ext>, <options>)
Then the error matches ErrInvalidExtension and contains "<message>"
And the root has no child scopes

Examples:
  | ext                                   | options                     | message                      |
  | nil                                   | (none)                      | extension is nil             |
  | (*redisExt)(nil)                      | (none)                      | extension is nil             |
  | an extension named "redis-"           | (none)                      | invalid extension name       |
  | an extension named "a--b"             | (none)                      | invalid extension name       |
  | an extension named ""                 | (none)                      | extension name is empty      |
  | an extension named "Redis Cache"      | (none)                      | invalid extension name       |
  | "redis" with instance key "cache.v2"  | (none)                      | invalid instance key         |
  | "redis"                               | doger.ExtendScopeName("")   | scope name is empty          |
  | an extension whose Name panics        | (none)                      | Name panicked                |
  | "redis" whose Instance panics         | (none)                      | Instance panicked            |
  | "redis" whose Placement panics        | (none)                      | Placement panicked           |
```

#### Scenario: Require validates its input
```gherkin
Given a root scope
And an extension "cache" whose Extend calls x.Require(<dep>) and returns its error
When the consumer calls doger.Extend(root, cacheExt)
Then the error matches ErrInvalidExtension and contains "<message>"
And the root has no child scopes

Examples:
  | dep                              | message                |
  | nil                              | extension is nil       |
  | an extension named "Redis Cache" | invalid extension name |
```

<!-- cases: N/A 1 default, 2 overrides: validation only -->
<!-- cases: 3 zero values: nil and typed-nil extension, empty name and empty scope name rows -->
<!-- cases: 4 boundaries: allowed versus disallowed characters -->
<!-- cases: 5 errors: every row, for Extend and Require; ErrInvalidExtension is matchable with errors.Is -->
<!-- cases: N/A 6 duplicate: covered by "Identity and repeated installation" -->
<!-- cases: N/A 7 lifecycle, 8 concurrency: validation runs before anything is installed -->
<!-- cases: 9 panics: Name, Instance and Placement panic rows; validation never panics -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Identity and repeated installation
An extension's identity SHALL be its Go type plus its instance key, the value of its optional `Instance() string` method (`ExtensionInstance`), empty without it. Repeating `Extend` for an installed identity SHALL do nothing when the two extension values are deeply equal and the `ExtendOption`s have the same effect, and SHALL fail with `ErrExtensionConflict` otherwise. A private scope name already used by another scope SHALL fail with `ErrExtensionConflict` instead of panicking.

#### Scenario: Repeated and colliding installations
```gherkin
Given a root scope where doger.Extend(root, <first>) returned nil
When the consumer calls doger.Extend(root, <second>)
Then the result is <result>
And Extend of <first> was called <calls> times in total

Examples:
  | first                                   | second                                   | result                                         | calls |
  | redis.Extension(redis.Addr("a:1"))      | redis.Extension(redis.Addr("a:1"))       | nil                                            | 1     |
  | redis.Extension(redis.Addr("a:1"))      | redis.Extension(redis.Addr("b:2"))       | an error matching ErrExtensionConflict         | 1     |
  | redis.Extension(redis.Instance("cache"))| redis.Extension(redis.Instance("queue")) | nil, with child scope "ext:redis/queue"        | 1     |
  | redis.Extension()                       | other.Extension() also named "redis"     | an error matching ErrExtensionConflict naming scope "ext:redis" | 1 |
  | redis.Extension()                       | redis.Extension() with doger.Flatten()   | an error matching ErrExtensionConflict         | 1     |
  | &redisExt{} (pointer)                   | redisExt{} (value of the same type)      | an error matching ErrExtensionConflict naming scope "ext:redis" | 1 |
  | a metrics extension that declined       | the same metrics value                   | nil                                            | 1     |
  | a metrics extension that declined       | another metrics value                    | an error matching ErrExtensionConflict         | 1     |
```

#### Scenario: A scope-name collision is resolved by the installer
```gherkin
Given a root scope where doger.Extend(root, redis.Extension()) returned nil
When the consumer calls doger.Extend(root, other.Extension(), doger.ExtendScopeName("other-redis")) for an extension also named "redis"
Then it returns nil
And the root has child scopes "ext:redis" and "other-redis"
```

<!-- cases: 2 overrides: ExtendScopeName resolves a collision -->
<!-- cases: N/A 3 zero values: covered by "Extend validates its inputs" -->
<!-- cases: 4 boundaries: same identity, other instance, other type with the same name, other options, pointer versus value (two identities: the guide asks authors to always return a pointer), installed or disabled identity -->
<!-- cases: 5 errors: conflict rows; ErrExtensionConflict is matchable with errors.Is -->
<!-- cases: 6 duplicate: the subject -->
<!-- cases: N/A 7 lifecycle: phases are covered by "Extend follows the container phase" -->
<!-- cases: 8 concurrency: covered by "Concurrent Extend calls install each identity once" -->
<!-- cases: N/A 9 panics: a collision never panics -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A failing Extend leaves nothing behind
When `ext.Extend` returns an error or panics, `Extend` SHALL return an `*ExtensionError` naming the extension and instance and wrapping the cause (a recovered panic as an error), SHALL remove the private scope and export nothing, and SHALL NOT record the identity, so a later `Extend` can retry. Extensions it required stay installed. With `Flatten()` registrations are not rolled back.

#### Scenario: Failure is reported and can be retried
```gherkin
Given a root scope
And an extension "redis" with instance "cache" whose first Extend registers lazy "*redis.Pool", calls doger.ExtensionExport[*redis.Pool](x) and then <fails>, and whose second Extend succeeds
When the consumer calls doger.Extend(root, redisExt) twice
Then the first call returns an error that errors.As finds as *ExtensionError with Name "redis" and Instance "cache"
And the first error <matches>
And after the first call the root has no child scope "ext:redis/cache" and no service "*redis.Pool"
And the second call returns nil and "*redis.Pool" is invokable from the root

Examples:
  | fails                              | matches                                        |
  | returns errors.New("dial failed")  | contains "dial failed"                          |
  | panics with errors.New("boom")     | wraps the panic error "boom" (errors.Is)        |
  | panics with the string "boom"      | contains "boom"                                 |
```

#### Scenario: A flattened extension keeps its partial registrations
```gherkin
Given a root scope
And an extension "redis" whose Extend registers lazy "*redis.Pool" and then returns errors.New("dial failed")
When the consumer calls doger.Extend(root, redisExt, doger.Flatten())
Then the error contains "dial failed"
And "*redis.Pool" is still registered in the root
```

<!-- cases: 2 overrides: Flatten disables rollback (second scenario) -->
<!-- cases: N/A 3 zero values: a nil error is success -->
<!-- cases: N/A 4 boundaries: one failing call followed by one retry -->
<!-- cases: 5 errors: error and panic rows; ExtensionError supports errors.As and Unwrap -->
<!-- cases: 6 duplicate: the retry is not rejected as a repeat -->
<!-- cases: 7 lifecycle: rolling back shuts the private scope down, so its shutdown and scope Closing/Closed hooks fire, as its registration hooks did (design D4); it shuts down with a context that never ends, so no service the attempt built is skipped (shutdown-context) -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent Extend calls install each identity once" -->
<!-- cases: 9 panics: the panic rows -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A rollback always completes and reports its failures
A panic while an extension's exports are applied SHALL fail the install like a panic in its `Extend`. A rollback SHALL detach the private scope and remove the exports even when a scope hook panics while the scope shuts down; that panic is then raised as for any shutdown. A failed shutdown report of the rollback SHALL be joined to the returned error. A rollback SHALL remove only the exports its attempt registered.

#### Scenario: Failures around the extension are rolled back
```gherkin
Given a root scope with <setup>
And an extension "redis" whose first Extend registers lazy "*redis.Client", builds it, calls doger.ExtensionExport[*redis.Client](x) and returns <first>, and whose second Extend succeeds
When the consumer calls doger.Extend(root, redisExt) twice
Then the first call <outcome>
And after the first call the root has no child scope "ext:redis"
And the second call returns nil

Examples:
  | setup                                                              | first                          | outcome                                                                  |
  | a HookAfterRegistration that panics with "hook boom" for "*redis.Client" in the root, once | nil | returns an *ExtensionError containing "hook boom"                         |
  | a HookScopeClosed that panics with "closed boom", once             | errors.New("dial failed")      | panics with "closed boom"                                                |
  | nothing, the client's Shutdown returning errors.New("close failed") | errors.New("dial failed")     | returns an *ExtensionError containing "dial failed" and "close failed"   |
```


#### Scenario: A rollback removes only its own exports
```gherkin
Given a root scope where the application registered "*redis.Client" producing "app-client"
And an extension "redis" whose Extend registers "*redis.Client" with doger.AsDefault(), calls doger.ExtensionExport[*redis.Client](x), then doger.ExtensionExport[*redis.Pool](x) for a "*redis.Pool" it never registered, and returns nil
When the consumer calls doger.Extend(root, redisExt)
Then the error names "*redis.Pool", whose export failed after the client's export was applied
And invoking "*redis.Client" from the root returns "app-client"
```


<!-- cases: N/A 1 default, 2 overrides: rollback is not configurable -->
<!-- cases: N/A 3 zero values: no input -->
<!-- cases: 4 boundaries: a panic in an export hook, a panic in a scope hook, a failing Shutdown; an application service under the published name -->
<!-- cases: 5 errors: the failed shutdown report is joined to the cause -->
<!-- cases: 6 duplicate: the retry is not rejected as a repeat; the application's service is not removed -->
<!-- cases: 7 lifecycle: the scope is detached and the exports removed whatever the shutdown does -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent Extend calls install each identity once" -->
<!-- cases: 9 panics: a panic while exports are applied becomes an *ExtensionError; a scope hook panic is raised after the cleanup -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: An extension can decline to install
When `ext.Extend` returns an error matching `ErrExtensionDisabled`, `Extend` SHALL return nil, log one line naming the extension and its instance key, install nothing, and record the identity as disabled. `Require` of a disabled extension, including one that declines during that `Require`, SHALL return an error matching `ErrRequiredExtensionDisabled`, which does not match `ErrExtensionDisabled`, so the requirer fails instead of being disabled.

#### Scenario: A disabled extension installs nothing
```gherkin
Given a root scope with a Logf that records lines
And an extension "metrics" with instance key "<instance>" whose Extend registers lazy "*metrics.Exporter" and returns fmt.Errorf("off by config: %w", doger.ErrExtensionDisabled)
When the consumer calls doger.Extend(root, metricsExt)
Then it returns nil
And one recorded log line contains "<line>"
And the root has no child scopes

Examples:
  | instance | line                          |
  |          | extension metrics disabled    |
  | eu       | extension metrics/eu disabled |
```

#### Scenario: Requiring a disabled extension fails
```gherkin
Given a root scope where <metrics>
And an extension "tracing" whose Extend calls x.Require(metricsExt) and returns its error
When the consumer calls doger.Extend(root, tracingExt)
Then the error matches ErrRequiredExtensionDisabled and names "metrics"
And the error does not match ErrExtensionDisabled
And the root has no child scope "ext:tracing"

Examples:
  | metrics                                                          |
  | the "metrics" extension was installed as disabled                |
  | the "metrics" extension is not installed and declines when installed |
```

<!-- cases: N/A 2 overrides: whether to disable is the extension author's choice; Flatten is covered by "A declined install that cannot be rolled back cleanly" -->
<!-- cases: N/A 3 zero values: no input -->
<!-- cases: 4 boundaries: with and without an instance key; disabled before or during the Require -->
<!-- cases: 5 errors: the Require scenario -->
<!-- cases: 6 duplicate: a repeated Extend of a disabled identity follows "Identity and repeated installation" (disabled rows there) -->
<!-- cases: 7 lifecycle: the declined attempt is shut down and removed -->
<!-- cases: N/A 8 concurrency, 9 panics: as for any Extend -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A declined install that cannot be rolled back cleanly
A flattened extension that declines SHALL keep the registrations it made, as a flattened failure does, and its identity SHALL be recorded as disabled. When shutting a declined attempt down fails, `Extend` SHALL return an `*ExtensionError` wrapping the failed report, which does not match `ErrExtensionDisabled`, and the identity SHALL still be recorded as disabled.

#### Scenario: A flattened declined install keeps its registrations
```gherkin
Given a root scope
And an extension "metrics" whose Extend registers lazy "*metrics.Exporter" and returns doger.ErrExtensionDisabled
When the consumer calls doger.Extend(root, metricsExt, doger.Flatten())
Then it returns nil
And "*metrics.Exporter" is still registered in the root
And a second doger.Extend(root, metricsExt, doger.Flatten()) returns nil
```


#### Scenario: A declined install whose shutdown fails is reported
```gherkin
Given a root scope
And an extension "metrics" whose Extend registers a value "*metrics.Exporter" whose Shutdown returns errors.New("close failed"), then returns doger.ErrExtensionDisabled
When the consumer calls doger.Extend(root, metricsExt)
Then the error is an *ExtensionError named "metrics" containing "close failed"
And the error does not match ErrExtensionDisabled
And the root has no child scopes
And a second doger.Extend(root, metricsExt) returns nil
```


<!-- cases: 2 overrides: Flatten keeps the registrations -->
<!-- cases: N/A 3 zero values, 4 boundaries: no input -->
<!-- cases: 5 errors: the failed shutdown is returned, not matching ErrExtensionDisabled -->
<!-- cases: 6 duplicate: a repeat of the disabled identity is a no-op in both scenarios -->
<!-- cases: 7 lifecycle: nothing is rolled back when flattened; the failed shutdown is reported -->
<!-- cases: N/A 8 concurrency, 9 panics: as for any Extend -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A required install that declines reports its failed shutdown
A `Require` whose install declines and fails its shutdown SHALL return an error matching `ErrRequiredExtensionDisabled` that also holds the failed report, and does not match `ErrExtensionDisabled`.

#### Scenario: A required install that declines and fails its shutdown is reported
```gherkin
Given a root scope
And an extension "metrics" whose Extend registers a value "*metrics.Exporter" whose Shutdown returns errors.New("close failed"), then returns doger.ErrExtensionDisabled
And an extension "tracing" whose Extend calls x.Require(metricsExt) and returns its error
When the consumer calls doger.Extend(root, tracingExt)
Then the error is an *ExtensionError named "tracing" matching ErrRequiredExtensionDisabled and containing "close failed"
And the error does not match ErrExtensionDisabled
And the root has no child scopes
```

<!-- cases: N/A 1 default, 2 overrides: as for any declined install -->
<!-- cases: N/A 3 zero values, 4 boundaries: no input -->
<!-- cases: 5 errors: the failed report is joined to ErrRequiredExtensionDisabled -->
<!-- cases: N/A 6 duplicate: covered by "Identity and repeated installation" -->
<!-- cases: 7 lifecycle: the declined attempt is shut down and removed -->
<!-- cases: N/A 8 concurrency, 9 panics: as for any Extend -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Require installs dependency extensions
`x.Require(dep)` SHALL install `dep` as `Extend` would and return nil when its identity is already installed, whatever its options; concurrent `Require` calls of one identity install it once. A cycle of `Require` calls SHALL fail with `ErrExtensionCycle`, on one goroutine, across goroutines, or through concurrent `Require` calls made by one `Extend`.

#### Scenario: Require is idempotent
```gherkin
Given a root scope
And extensions "cache" and "session", each of whose Extend calls x.Require(redis.Extension())
When the consumer calls doger.Extend(root, cacheExt) and doger.Extend(root, sessionExt)
Then both return nil
And the redis Extend was called exactly 1 time
```

#### Scenario: A Require cycle fails
```gherkin
Given a root scope
And extension "a" whose Extend calls x.Require(bExt), and extension "b" whose Extend calls x.Require(aExt)
When the consumer calls doger.Extend(root, aExt)
Then the error matches ErrExtensionCycle and names "a" and "b"
And the root has no child scopes
```

#### Scenario: Concurrent Require calls of one Extend
```gherkin
Given a root scope
And an extension "cache" whose Extend calls x.Require(redis.Extension()) from 10 goroutines and waits for them
When the consumer calls doger.Extend(root, cacheExt)
Then it returns nil
And every Require returned nil
And the redis Extend was called exactly 1 time
```

#### Scenario: A Require cycle across goroutines fails instead of deadlocking
```gherkin
Given a root scope
And extension "a" whose Extend signals "a started", waits for "b started", then calls x.Require(bExt)
And extension "b" whose Extend signals "b started", waits for "a started", then calls x.Require(aExt)
When one goroutine calls doger.Extend(root, aExt) and another calls doger.Extend(root, bExt)
Then both calls return within 1s
And at least one error matches ErrExtensionCycle
```

#### Scenario: A cycle through concurrent Require calls of one Extend fails
```gherkin
Given a root scope
And extension "b" being installed by one goroutine and extension "c" by another, each waiting until released
And extension "a", installed by a third goroutine, whose Extend calls x.Require(bExt) and x.Require(cExt) from two goroutines, then releases "b" and "c"
And extension "b", once released, calls x.Require(aExt)
When the three calls run
Then they all return within 2s
And at least one error matches ErrExtensionCycle
```

<!-- cases: N/A 2 overrides: covered by "An explicit Extend replaces an unused required install" -->
<!-- cases: N/A 3 zero values: Require(nil) fails as Extend(nil) does ("Extend validates its inputs") -->
<!-- cases: 4 boundaries: one and two requirers; two-extension cycle on one goroutine, across goroutines, and through concurrent Require calls of one Extend -->
<!-- cases: 5 errors: cycle rows -->
<!-- cases: 6 duplicate: the idempotent scenario; concurrent Require calls of one identity -->
<!-- cases: N/A 7 lifecycle: covered by "An explicit Extend replaces an unused required install" -->
<!-- cases: 8 concurrency: the cross-goroutine and concurrent-Require scenarios -->
<!-- cases: N/A 9 panics: a panic inside a required Extend follows "A failing Extend leaves nothing behind" for both extensions -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: An explicit Extend replaces an unused required install
An explicit `Extend` with another value of an identity installed privately and only through `Require` SHALL replace it while no service of its scope or of a descendant scope was resolved, and fail with `ErrExtensionConflict` otherwise; the replacement MAY rename the scope or flatten. A required install placed flat by its author SHALL never be replaced. If the replacing `Extend` fails, the required install SHALL be installed again from its value.

#### Scenario: An explicit Extend after Require
```gherkin
Given a root scope
And extension "cache" whose Extend calls x.Require(redis.Extension(redis.Addr("default:6379")))
And doger.Extend(root, cacheExt) returned nil
And <step>
When the consumer calls doger.Extend(root, redis.Extension(redis.Addr("prod:6379")))
Then the result is <result>
And the redis client uses address "<addr>"

Examples:
  | step                                          | options                              | result                                  | addr          |
  | nothing was invoked                           | (none)                               | nil                                     | prod:6379     |
  | an unrelated root service "*app.Config" was invoked | (none)                         | nil                                     | prod:6379     |
  | "*redis.Client" was invoked from the root     | (none)                               | an error matching ErrExtensionConflict  | default:6379  |
  | nothing was invoked                           | doger.ExtendScopeName("redis-prod")  | nil, child "redis-prod" and no child "ext:redis" | prod:6379 |
  | nothing was invoked                           | doger.Flatten()                      | nil, no child "ext:redis"               | prod:6379     |
  | a service of a child scope of "ext:redis" was invoked | (none)                       | an error matching ErrExtensionConflict  | default:6379  |
```


#### Scenario: A required install placed flat by its author is never replaced
```gherkin
Given a root scope
And extension "cache" whose Extend calls x.Require(flatRedis.Extension(flatRedis.Addr("default:6379"))) for an extension declaring PlacementFlat
And doger.Extend(root, cacheExt) returned nil
When the consumer calls doger.Extend(root, flatRedis.Extension(flatRedis.Addr("prod:6379")))
Then the error matches ErrExtensionConflict
```


#### Scenario: A failing replacement restores the required install
```gherkin
Given a root scope
And extension "cache" whose Extend calls x.Require(redis.Extension(redis.Addr("default:6379")))
And doger.Extend(root, cacheExt) returned nil
When the consumer calls doger.Extend(root, redis.Extension(redis.Addr("bad:6379"))) whose Extend returns errors.New("dial failed")
Then the error contains "dial failed"
And <then>

Examples:
  | then                                                                                                   |
  | invoking "*redis.Client" from the root returns a client using "default:6379"                           |
  | a later doger.Extend(root, redis.Extension(redis.Addr("prod:6379"))) returns nil, the restored install being unresolved |
```


<!-- cases: 2 overrides: the explicit Extend is the override; another scope name or Flatten -->
<!-- cases: N/A 3 zero values: no input -->
<!-- cases: 4 boundaries: nothing resolved, an unrelated service resolved, a service of the scope or of a descendant resolved; placed flat by the author -->
<!-- cases: 5 errors: conflict rows; a failing replacement -->
<!-- cases: 6 duplicate: an equal explicit Extend is a repeat ("Identity and repeated installation") -->
<!-- cases: 7 lifecycle: a failed replacement restores the required install -->
<!-- cases: 8 concurrency: resolving the required extension's services while an Extend replaces it is unsupported and documented (not reproducible, see proposal Defect) -->
<!-- cases: N/A 9 panics: a panic in the replacing Extend is a failing replacement -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A declined replacement keeps the required install
When the explicit `Extend` that replaces a required install declines with `ErrExtensionDisabled`, the required install SHALL be installed again from its value, and `Extend` SHALL return an `*ExtensionError` matching `ErrExtensionConflict` and not `ErrExtensionDisabled`. The identity SHALL stay installed through `Require`, not disabled.

#### Scenario: A declined replacement restores the required install
```gherkin
Given a root scope
And extension "cache" whose Extend calls x.Require(redis.Extension(redis.Addr("default:6379")))
And doger.Extend(root, cacheExt) returned nil
When the consumer calls doger.Extend(root, redis.Extension(redis.Addr("off:6379"))) whose Extend returns doger.ErrExtensionDisabled
Then the error is an *ExtensionError named "redis" matching ErrExtensionConflict
And the error does not match ErrExtensionDisabled
And <then>

Examples:
  | then                                                                                                   |
  | invoking "*redis.Client" from the root returns a client using "default:6379"                           |
  | a later doger.Extend(root, redis.Extension(redis.Addr("prod:6379"))) returns nil, the restored install being unresolved |
```

<!-- cases: 2 overrides: the explicit Extend is the override; its decline cannot remove a required install -->
<!-- cases: N/A 3 zero values, 4 boundaries: no input -->
<!-- cases: 5 errors: ErrExtensionConflict, not ErrExtensionDisabled -->
<!-- cases: 6 duplicate: the restored install stays replaceable while unresolved (second row) -->
<!-- cases: 7 lifecycle: the declined attempt is rolled back, then the required install is installed again -->
<!-- cases: N/A 8 concurrency: as in "An explicit Extend replaces an unused required install" -->
<!-- cases: N/A 9 panics: a panic is a failing replacement -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Extend follows the container phase
`Extend` SHALL be allowed while the container is idle, starting, started, ready or failed. Services an extension adds after a successful Start SHALL be started by the next `Refresh`; after a failed Start, `Refresh` fails with `ErrNotStarted` as today. In the closing or closed phase, `Extend` SHALL fail with an error matching `ErrStartAfterShutdown` and install nothing.

#### Scenario: Extend in each phase
```gherkin
Given a root scope in phase <phase>
And an extension "redis" whose Extend registers lazy "*redis.Pool" implementing Start(ctx) error, and exports it
When the consumer calls doger.Extend(root, redisExt)
Then the result is <result>

Examples:
  | phase                                     | result                                        |
  | idle                                      | nil                                           |
  | starting (called from a Starting hook)    | nil, and the pool is started by that Start    |
  | started (called from a Started hook)      | nil, and the pool is started by root.Refresh()|
  | ready                                     | nil, and the pool is started by root.Refresh()|
  | failed                                    | nil, the pool is not started, and root.Refresh() fails with ErrNotStarted |
  | closing (called from a Closing hook)      | an error matching ErrStartAfterShutdown       |
  | closed                                    | an error matching ErrStartAfterShutdown       |
```

<!-- cases: N/A 2 overrides: phases are not configurable -->
<!-- cases: N/A 3 zero values, 5 invalid input: phase is not an input -->
<!-- cases: 4 boundaries: every phase -->
<!-- cases: N/A 6 duplicate: covered by "Identity and repeated installation" -->
<!-- cases: 7 lifecycle: the subject -->
<!-- cases: 8 concurrency: Extend concurrent with Start is unsupported and documented, as for the hook adders -->
<!-- cases: N/A 9 panics: as for any Extend -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Extension services yield to the application
An extension that registers its services with `AsDefault()` and exports them SHALL let an application service of the published name win, whether the application registers it before or after `Extend`, for readers in the root and in the extension's private scope.

#### Scenario: The application's client wins
```gherkin
Given a root scope
And an extension "redis" whose Extend registers lazy "*redis.Client" with doger.AsDefault() producing "ext-client", and lazy "*redis.Cache" invoking "*redis.Client", and exports both
And the application registers an explicit "*redis.Client" producing "app-client" in the root <when> doger.Extend(root, redisExt)
When the consumer invokes "*redis.Cache" from the root
Then the cache uses "app-client"
And invoking "*redis.Client" from the root returns "app-client"

Examples:
  | when   |
  | before |
  | after  |
```

#### Scenario: The application's client is health-checked once
```gherkin
Given the setup of "The application's client wins", registered before, where the application's client implements HealthCheck() error counting calls, and "*redis.Cache" was invoked
When the consumer calls HealthCheck on the root
Then the application's client's HealthCheck was called 1 time
And the health map has key "*redis.Client" and no key "ext:redis/*redis.Client"
```

<!-- cases: 1 default: without the application service, the extension's client is used (scope-export "An export of a default is a default") -->
<!-- cases: 2 overrides: the application service is the override -->
<!-- cases: N/A 3 zero values, 4 boundaries, 5 invalid input: no input -->
<!-- cases: 6 duplicate: registration order rows -->
<!-- cases: N/A 7 lifecycle: frozen decisions follow default-registrations -->
<!-- cases: N/A 8 concurrency, 9 panics: as in default-registrations -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Extension services follow the container lifecycle
Services of an extension's private scope SHALL be started by `Start`, shut down by root `Shutdown` after the root services that depend on their exports, and checked by root `HealthCheck`, keyed `<scope name>/<service name>`; yielded defaults and exports from extension scopes SHALL be skipped there, so each service is checked once. Other child scopes SHALL keep today's root health check.

#### Scenario: Lifecycle of extension services
```gherkin
Given a root scope
And an extension "redis" whose private "*redis.Pool" implements Start(ctx) error, Shutdown() error and HealthCheck() error returning errors.New("pool down"), each counting calls, exported
And a lazy "*app.App" in the root invoking "*redis.Pool" and recording whether the pool was closed at its own shutdown
And an ordinary child scope "jobs" with a lazy "*app.Job" implementing HealthCheck() error, invoked
When the consumer calls Start, HealthCheck and Shutdown on the root
Then the pool's Start, HealthCheck and Shutdown were each called 1 time
And the health map has key "ext:redis/*redis.Pool" with an error containing "pool down" and no key "*redis.Pool"
And the health map has no key for "*app.Job"
And "*app.App" did not see the pool closed
```

#### Scenario: Only flattened extensions leave the root health check unchanged
```gherkin
Given a root scope
And an extension "redis" installed with doger.Flatten() whose lazy "*redis.Pool" implements HealthCheck() error and was invoked
And an ordinary child scope "jobs" with a lazy "*app.Job" implementing HealthCheck() error, invoked
When the consumer calls HealthCheck on the root
Then the health map has key "*redis.Pool" and no key starting with "ext:"
And the health map has no key for "*app.Job"
```

<!-- cases: N/A 2 overrides: lifecycle is not configurable; Flatten puts the services in the root, where they follow the ordinary rules (flattened scenario) -->
<!-- cases: N/A 3 zero values, 4 boundaries, 5 invalid input: no input -->
<!-- cases: 6 duplicate: each service checked once -->
<!-- cases: 7 lifecycle: the subject; a root Shutdown whose context ended skips extension-scope services exactly as shutdown-context specifies, with no extension-specific rule -->
<!-- cases: N/A 8 concurrency: existing parallel start, health check and shutdown apply -->
<!-- cases: N/A 9 panics: unchanged -->
<!-- cases: 10 compatibility: BEHAVIOR CHANGE (flagged) - root HealthCheck output gains extension-scope keys only in containers that use extensions; ordinary child scopes are unchanged ("jobs" row) -->

### Requirement: Clone copies installed extensions
`CloneWithOpts` SHALL copy the installed and disabled identities with their scopes, so `Extend` of such an identity on the clone follows "Identity and repeated installation", and the clone's exports resolve from the clone's private scopes. An installation still running when the clone is taken SHALL NOT be copied, nor its partly built scope, so the clone can install that identity itself.

#### Scenario: Extend on a clone
```gherkin
Given a root scope where doger.Extend(root, redis.Extension()) returned nil
And clone := root.Clone()
When the consumer calls doger.Extend(clone, redis.Extension())
Then it returns nil
And the redis Extend was called 1 time in total
And invoking "*redis.Client" from the clone returns an instance built by the clone's scope "ext:redis"
```

#### Scenario: Clone of other installation states
```gherkin
Given a root scope where <state>
And clone := root.Clone() taken <when>
When the consumer calls doger.Extend(clone, <ext>)
Then it returns nil
And <outcome>

Examples:
  | state                                         | when                              | ext       | outcome                                              |
  | the "metrics" extension was installed as disabled | after it returned             | metricsExt | the metrics Extend was called 1 time in total and the clone has no child scope "ext:metrics" |
  | redis's Extend is running and blocked inside  | while it runs, then it completes  | redisExt  | the redis Extend was called 2 times in total and the clone resolves "*redis.Client" from its own "ext:redis" |
```

<!-- cases: N/A 2 overrides, 3 zero values, 5 invalid input: no input -->
<!-- cases: 4 boundaries: installed, disabled, and running when cloned -->
<!-- cases: 6 duplicate: repeat Extend on the clone is a no-op -->
<!-- cases: 7 lifecycle: clone is the subject -->
<!-- cases: 8 concurrency: a clone taken while an Extend runs -->
<!-- cases: N/A 9 panics: nothing panics -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Concurrent Extend calls install each identity once
Concurrent `Extend` calls on one container SHALL install each identity at most once: a call for an identity being installed waits for that installation and gets a consistent result. Different identities MAY install concurrently.

#### Scenario: Concurrent installation of one identity
```gherkin
Given a root scope
When 50 goroutines call doger.Extend(root, redis.Extension()) concurrently
Then every call returns nil
And the redis Extend was called exactly 1 time
```

<!-- cases: N/A 2-7: no input beyond the call -->
<!-- cases: 8 concurrency: the subject; runs under -race -->
<!-- cases: N/A 9 panics, 10 compatibility: as for any Extend / new API -->

### Requirement: The extension context is valid only during Extend
`ExtensionExport`, `ExtensionExportNamed` and `Require` called with an `ExtensionContext` after its `Extend` returned SHALL fail with `ErrInvalidExtension`, and a `Properties` view taken from it SHALL return `("", false)` and log one line. Providers SHALL read properties through `ExtensionProperties(i)` instead.

#### Scenario: Using the context after Extend returned
```gherkin
Given a root scope
And an extension "redis" whose Extend stores x and returns nil
And doger.Extend(root, redisExt) returned nil
When the consumer calls <call> with the stored x
Then the result is <result>

Examples:
  | call                                             | result                                          |
  | doger.ExtensionExport[*redis.Pool](x)            | an error matching ErrInvalidExtension           |
  | x.Require(other.Extension())                     | an error matching ErrInvalidExtension           |
  | x.Properties().Lookup("addr"), twice             | ("", false) each time, with one log line naming "redis" |
```

<!-- cases: N/A 1 default, 2 overrides: misuse only -->
<!-- cases: N/A 3 zero values: a zero ExtensionContext is not constructible outside the package -->
<!-- cases: 4 boundaries: export, require and properties rows -->
<!-- cases: 5 errors: ErrInvalidExtension rows -->
<!-- cases: N/A 6 duplicate, 7 lifecycle: the context ends when Extend returns -->
<!-- cases: N/A 8 concurrency: the stored context is read-only -->
<!-- cases: N/A 9 panics: misuse never panics -->
<!-- cases: N/A 10 compatibility: new API -->
