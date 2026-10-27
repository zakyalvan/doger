# Spec Delta

## Purpose

Gives extensions configuration without tying the library to a config package: a `PropertySource` contract, an environment-variable default, and per-extension prefixed views, with the source chosen by the application.

## ADDED Requirements

### Requirement: Properties are read through an identity-prefixed view
`x.Properties().Lookup(key)` SHALL look up `<name>.<key>`, or `<name>/<instance>.<key>` when the extension has an instance key, in the container's property source, and return its value and whether it was found. An instance view SHALL never look up a key of the view without an instance, and the reverse.

#### Scenario: Prefixed lookups
```gherkin
Given a root scope with InjectorOpts.PropertySource set to a map source holding "redis.addr" = "a:1" and "redis/cache.addr" = "c:3"
And an extension "redis" with instance key "<instance>" whose Extend records x.Properties().Lookup("<key>")
When the consumer calls doger.Extend(root, redisExt)
Then the recorded result is ("<value>", <found>)

Examples:
  | instance | key     | value | found |
  |          | addr    | a:1   | true  |
  | cache    | addr    | c:3   | true  |
  |          | port    |       | false |
```

#### Scenario: Views of different instances never share a key
```gherkin
Given a root scope with InjectorOpts.PropertySource set to a source that records every key it receives
When an extension "redis" without an instance key looks up "queue.addr" and an extension "redis" with instance key "queue" looks up "addr"
Then the source received "redis.queue.addr" and "redis/queue.addr"
```

<!-- cases: N/A 2 overrides: the source is chosen by "The property source is chosen by the application" -->
<!-- cases: 3 zero values: a missing key returns ("", false) -->
<!-- cases: 4 boundaries: with and without an instance key; an instance key equal to a key segment -->
<!-- cases: 5 invalid input: covered by "Property keys are validated" -->
<!-- cases: N/A 6 duplicate: a key has one value per source -->
<!-- cases: N/A 7 lifecycle: lookups are allowed whenever Extend or a provider runs -->
<!-- cases: 8 concurrency: covered by "Concurrent lookups" -->
<!-- cases: N/A 9 panics: Lookup does not panic -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Property keys are validated
A full property key SHALL be the extension's name, optionally followed by `/` and its instance key, then one or more segments each preceded by a single dot; the name, the instance key and each segment are lowercase alphanumeric words joined by single dashes (`[a-z0-9]+(-[a-z0-9]+)*`), so the environment mapping is one-to-one. `Lookup` of a key that would produce an invalid full key SHALL return `("", false)` without consulting the source.

#### Scenario: Invalid keys are not looked up
```gherkin
Given a root scope with InjectorOpts.PropertySource set to a source that records every key it receives
And an extension "redis" whose Extend calls x.Properties().Lookup("<key>")
When the consumer calls doger.Extend(root, redisExt)
Then the lookup returns ("", false)
And the source received <received>

Examples:
  | key          | received        |
  | Addr         | no key          |
  | addr..port   | no key          |
  | .addr        | no key          |
  |              | no key          |
  | addr-        | no key          |
  | -addr        | no key          |
  | pool--size   | no key          |
  | queue/addr   | no key          |
  | pool-size    | "redis.pool-size" |
```

<!-- cases: N/A 1 default, 2 overrides: validation only -->
<!-- cases: 3 zero values: empty key row -->
<!-- cases: 4 boundaries: valid dash row versus invalid rows -->
<!-- cases: 5 errors: invalid keys are a miss, not an error, so configuration falls back to defaults -->
<!-- cases: N/A 6-9: pure validation -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: The environment source maps keys to variable names
`EnvPropertySource(prefix)` SHALL map a full key to an environment variable by upper-casing it, replacing each `-` with `__`, each `.` with `_` and each `/` with `___`, and prepending `prefix`. The default source SHALL be `EnvPropertySource("DOGER_")`.

#### Scenario: Key to variable mapping
```gherkin
Given the environment variable "<variable>" is set to "v"
And a source <source>
When the consumer calls source.Lookup("<key>")
Then the result is ("v", true)

Examples:
  | source                          | key                    | variable                    |
  | doger.EnvPropertySource("DOGER_") | redis.addr           | DOGER_REDIS_ADDR            |
  | doger.EnvPropertySource("DOGER_") | redis.cache.addr     | DOGER_REDIS_CACHE_ADDR      |
  | doger.EnvPropertySource("DOGER_") | redis/cache.addr     | DOGER_REDIS___CACHE_ADDR    |
  | doger.EnvPropertySource("DOGER_") | redis.cache-addr     | DOGER_REDIS_CACHE__ADDR     |
  | doger.EnvPropertySource("MYAPP_") | redis.addr           | MYAPP_REDIS_ADDR            |
  | doger.EnvPropertySource("")       | redis.addr           | REDIS_ADDR                  |
```

#### Scenario: Distinct keys never share a variable
```gherkin
Given the environment variable "DOGER_REDIS_CACHE_ADDR" is set to "dot", "DOGER_REDIS_CACHE__ADDR" to "dash" and "DOGER_REDIS___CACHE_ADDR" to "instance"
When the consumer looks up "redis.cache.addr", "redis.cache-addr" and "redis/cache.addr" with doger.EnvPropertySource("DOGER_")
Then the results are ("dot", true), ("dash", true) and ("instance", true)
```

#### Scenario: An unset variable is a miss
```gherkin
Given the environment variable "DOGER_REDIS_ADDR" is unset
When the consumer calls doger.EnvPropertySource("DOGER_").Lookup("redis.addr")
Then the result is ("", false)
```

<!-- cases: 1 default: the DOGER_ rows -->
<!-- cases: 2 overrides: other prefix and empty prefix rows -->
<!-- cases: 3 zero values: empty prefix row; an empty variable value is ("", true) as os.LookupEnv reports it -->
<!-- cases: 4 boundaries: one, two and three segments, dash, instance separator -->
<!-- cases: N/A 5 invalid input: keys reaching the source are already valid -->
<!-- cases: 6 duplicate: the distinct-keys scenario covers all three separators; a run of underscores is 1, 2 or 3 long and two runs are never adjacent -->
<!-- cases: N/A 7 lifecycle: the source is stateless -->
<!-- cases: N/A 8 concurrency: os.LookupEnv is safe for concurrent use -->
<!-- cases: N/A 9 panics: Lookup does not panic -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: The property source is chosen by the application
At a container's first property read, its source SHALL be decided and kept: `InjectorOpts.PropertySource` when non-nil; otherwise the root's service named `NameOf[PropertySource]()`; otherwise `EnvPropertySource("DOGER_")`, registered in the root as a default under that name. Services of other names are never selected.

#### Scenario: Source selection
```gherkin
Given a root scope created with <opts>
And <registered>
And the environment variable "DOGER_REDIS_ADDR" is set to "from-env"
And an extension "redis" whose Extend records x.Properties().Lookup("addr")
When the consumer calls doger.Extend(root, redisExt)
Then the recorded value is "<value>"

Examples:
  | opts                                           | registered                                                         | value      |
  | no PropertySource                              | nothing                                                            | from-env   |
  | no PropertySource                              | a lazy doger.PropertySource (an *app.MapSource) with "redis.addr" = "from-service" | from-service |
  | PropertySource = map with "redis.addr" = "from-opts" | a lazy doger.PropertySource (an *app.MapSource) with "redis.addr" = "from-service" | from-opts |
  | no PropertySource                              | a lazy "*app.Cache" with a Lookup(string) (string, bool) method returning "from-cache" | from-env |
```

#### Scenario: A source registered after the first read is not used
```gherkin
Given a root scope with no InjectorOpts.PropertySource, "DOGER_REDIS_ADDR" set to "env-a" and "DOGER_REDIS_B_ADDR" set to "env-b"
And doger.Extend(root, redisExt) read the property "addr"
And a lazy "*app.MapSource" (registered under its own type name) with "redis.b.addr" = "late" is registered afterwards
When a second extension "redis" with instance "b" reads "addr" during its Extend
Then it reads "env-b"
```

#### Scenario: Registering the interface name after the first read panics
```gherkin
Given a root scope with no InjectorOpts.PropertySource whose first property read happened
When the consumer registers a lazy service under the name doger.NameOf[doger.PropertySource]()
Then the registration panics with "DI: service `github.com/kartaladev/doger.PropertySource` has already been declared"
```

<!-- cases: 1 default: the environment row -->
<!-- cases: 2 overrides: service and InjectorOpts rows -->
<!-- cases: 3 zero values: a nil InjectorOpts.PropertySource means unset -->
<!-- cases: 4 boundaries: before and after the first read -->
<!-- cases: 5 errors: the late same-name registration panics as a duplicate (default-registrations) -->
<!-- cases: 6 duplicate: late registration scenarios -->
<!-- cases: 7 lifecycle: the decision is kept for the container's life -->
<!-- cases: 8 concurrency: covered by "Concurrent lookups" -->
<!-- cases: 9 panics: third scenario -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A failed source decision is not kept
If deciding the source panics, the panic SHALL reach the reader and nothing SHALL be kept, so the next read decides again. If the selected source service fails to resolve, every property SHALL be unset for the container, and one line SHALL be logged with the error's type, never its message.

#### Scenario: A failed decision is retried
```gherkin
Given a root scope with no InjectorOpts.PropertySource, "DOGER_REDIS_ADDR" set to "from-env", and a HookAfterRegistration that panics with "hook boom" the first time a service named doger.NameOf[doger.PropertySource]() is registered
When an extension "redis" reads "addr" during its Extend, twice
Then the first doger.Extend returns an *ExtensionError containing "hook boom"
And the second reads ("from-env", true)
```

#### Scenario: A failing source provider leaves every property unset
```gherkin
Given a root scope with a Logf that records lines and a lazy doger.PropertySource whose provider returns errors.New("vault sealed: token s3cret")
And the environment variable "DOGER_REDIS_ADDR" is set to "from-env"
When an extension "redis" reads "addr" during its Extend
Then the read returns ("", false)
And one recorded log line contains "property source unavailable"
And no recorded log line contains "s3cret" or "vault sealed"
```

<!-- cases: N/A 1 default, 2 overrides: failure handling is not configurable; InjectorOpts.PropertySource avoids the provider -->
<!-- cases: N/A 3 zero values, 4 boundaries: no input -->
<!-- cases: 5 errors: a failing provider leaves every property unset -->
<!-- cases: 6 duplicate: the retried decision registers the default once -->
<!-- cases: 7 lifecycle: a failed decision is not kept; a provider failure is kept like any decision -->
<!-- cases: N/A 8 concurrency: as in "Concurrent lookups" -->
<!-- cases: 9 panics: a panic while deciding reaches the reader and is retried -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Property values are never logged
The library SHALL NOT write property values to its logger or include them in errors it returns; when it logs a failure of a property source, it SHALL log the error's type only.

#### Scenario: Values stay out of logs and errors
```gherkin
Given a root scope with a Logf that records lines and InjectorOpts.PropertySource holding "redis.password" = "s3cret"
And an extension "redis" whose Extend reads "password" and then returns errors.New("auth failed")
When the consumer calls doger.Extend(root, redisExt)
Then no recorded log line and not the returned error contain "s3cret"
```

<!-- cases: N/A 2-7: secrecy has no input beyond the value -->
<!-- cases: N/A 8 concurrency, 9 panics: unchanged -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Concurrent lookups
Concurrent property reads from several extensions or providers SHALL decide the source once and return consistent values.

#### Scenario: Concurrent first reads decide the source once
```gherkin
Given a root scope with no InjectorOpts.PropertySource and a lazy doger.PropertySource (an *app.MapSource) holding "redis.addr" = "a:1", whose provider counts its calls
And an extension "redis" whose Extend starts 50 goroutines calling x.Properties().Lookup("addr") and waits for them
When the consumer calls doger.Extend(root, redisExt)
Then every call returned ("a:1", true)
And the "*app.MapSource" provider was called exactly 1 time
```

<!-- cases: N/A 2-7: no input beyond the reads -->
<!-- cases: 8 concurrency: the subject; runs under -race -->
<!-- cases: N/A 9 panics, 10 compatibility: Lookup does not panic / new API -->

### Requirement: Providers read properties through their injector
`ExtensionProperties(i)` SHALL return the properties view of the extension whose private scope `i` belongs to, or a descendant of it, bound to `i`'s container, and `true`; for an injector outside any private extension scope, including the provider of a flattened extension, it SHALL return a view that always misses, and `false`.

#### Scenario: A provider reads properties in the original and the cloned container
```gherkin
Given a root scope created with InjectorOpts.PropertySource holding "redis.addr" = "orig"
And an extension "redis" whose provider of "*redis.Client" records doger.ExtensionProperties(i).Lookup("addr")
And doger.Extend(root, redisExt) returned nil
And clone := root.CloneWithOpts with InjectorOpts.PropertySource holding "redis.addr" = "cloned"
When the consumer invokes "*redis.Client" from <container>
Then the recorded value is "<value>"

Examples:
  | container | value  |
  | root      | orig   |
  | clone     | cloned |
```

#### Scenario: Injectors inside and outside extension scopes
```gherkin
Given a root scope with InjectorOpts.PropertySource holding "redis.addr" = "a:1"
And a lazy "*app.Svc" whose provider records doger.ExtensionProperties(i) and its Lookup("addr"), registered in <where>
When the consumer invokes "*app.Svc" from that scope
Then the recorded ok is <ok> and the Lookup returned <result>

Examples:
  | where                                                       | ok    | result        |
  | the root                                                    | false | ("", false)   |
  | a child scope "jobs" of the root                            | false | ("", false)   |
  | a child scope "pool" of the private scope of extension "redis" | true | ("a:1", true) |
  | the root, by extension "redis" installed with doger.Flatten() | false | ("", false)  |
```

<!-- cases: N/A 2 overrides: the view follows the container's source -->
<!-- cases: 3 zero values: an injector outside extensions gets a missing view -->
<!-- cases: 4 boundaries: original and cloned container; root, ordinary child, descendant of a private scope, flattened extension -->
<!-- cases: N/A 5 errors: misses, not errors -->
<!-- cases: N/A 6 duplicate: one view per extension identity -->
<!-- cases: 7 lifecycle: clone row -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent lookups" -->
<!-- cases: N/A 9 panics: never panics -->
<!-- cases: N/A 10 compatibility: new API -->
