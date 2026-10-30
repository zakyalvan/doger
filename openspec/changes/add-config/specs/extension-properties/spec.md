# Spec Delta

## MODIFIED Requirements

### Requirement: Property keys are validated
A full property key SHALL be the extension's name, optionally followed by `/` and its instance key, then one or more segments each preceded by a single dot; the name, the instance key and each segment are lowercase alphanumeric words joined by single dashes (`[a-z0-9]+(-[a-z0-9]+)*`), and a segment may carry the suffixes of "Keys may index lists and maps". `Lookup` of a key that would produce an invalid full key SHALL return `("", false)` without consulting the source.

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
<!-- cases: N/A 6 duplicate, 7 lifecycle, 8 concurrency, 9 panics: pure validation of one string -->
<!-- cases: 10 compatibility: the rows are unchanged from the main spec; the one-to-one environment clause is dropped because the environment mapping now follows Spring (see "The environment source maps keys by Spring Boot's rules"); suffixes are referenced here so the archived spec stays coherent, and defined in the ADDED "Keys may index lists and maps" -->

### Requirement: The property source is chosen by the application
A container's source SHALL be `InjectorOpts.ConfigSource` when non-nil, decided when the constructor runs. Otherwise, at the container's first property read, it SHALL be decided and kept: `InjectorOpts.PropertySource` when non-nil; otherwise the root's service named `NameOf[PropertySource]()`; otherwise `EnvPropertySource("DOGER_")`, registered in the root as a default under that name. Services of other names are never selected.

#### Scenario: Source selection
```gherkin
Given a root scope created with <opts>
And <registered>
And the environment variable "DOGER_REDIS_ADDR" is set to "from-env"
And an extension "redis" whose Extend records x.Properties().Lookup("addr")
When the consumer calls doger.Extend(root, redisExt)
Then the recorded value is "<value>"

Examples:
  | opts                                                                    | registered                                                                              | value        |
  | no PropertySource                                                       | nothing                                                                                 | from-env     |
  | no PropertySource                                                       | a lazy doger.PropertySource (doger.MapSource{"redis.addr": "from-service"})             | from-service |
  | PropertySource = doger.MapSource{"redis.addr": "from-opts"}             | a lazy doger.PropertySource (doger.MapSource{"redis.addr": "from-service"})             | from-opts    |
  | no PropertySource                                                       | a lazy "*app.Cache" with a Lookup(string) (string, bool) method returning "from-cache"  | from-env     |
  | ConfigSource = a test config source holding "redis.addr" = "from-config" | a lazy doger.PropertySource (doger.MapSource{"redis.addr": "from-service"})            | from-config  |
```

#### Scenario: A source registered after the first read is not used
```gherkin
Given a root scope with no InjectorOpts.PropertySource, "DOGER_REDIS_ADDR" set to "env-a" and "DOGER_REDIS__B_ADDR" set to "env-b"
And doger.Extend(root, redisExt) read the property "addr"
And a lazy "*app.MapSource" (registered under its own type name) with "redis/b.addr" = "late" is registered afterwards
When a second extension "redis" with instance "b" reads "addr" during its Extend
Then it reads "env-b"
```

#### Scenario: Registering the interface name after the first read panics
```gherkin
Given a root scope with no InjectorOpts.PropertySource whose first property read happened
When the consumer registers a lazy service under the name doger.NameOf[doger.PropertySource]()
Then the registration panics with "DI: service `github.com/kartaladev/doger.PropertySource` has already been declared"
```

#### Scenario: An installed config source is decided before any package runs
```gherkin
Given a test config source holding "redis.addr" = "from-config"
And root, err := doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: src}, pkg) returned a nil err, where pkg registers a lazy doger.PropertySource (doger.MapSource{"redis.addr": "from-service"})
When the consumer calls doger.Extend(root, redisExt) whose Extend reads "addr"
Then the read returns ("from-config", true)
```

<!-- cases: 1 default: the environment row -->
<!-- cases: 2 overrides: service, PropertySource and ConfigSource rows -->
<!-- cases: 3 zero values: a nil InjectorOpts.PropertySource or ConfigSource means unset -->
<!-- cases: 4 boundaries: before and after the first read; ConfigSource decided at construction -->
<!-- cases: 5 errors: the late same-name registration panics as a duplicate (default-registrations); setting both PropertySource and ConfigSource is ErrConfigConflict (config-sources); an invalid env prefix is in "The environment prefix is normalized" -->
<!-- cases: 6 duplicate: late registration scenarios -->
<!-- cases: 7 lifecycle: the decision is kept for the container's life -->
<!-- cases: 8 concurrency: covered by "Concurrent lookups" -->
<!-- cases: 9 panics: third scenario -->
<!-- cases: 10 compatibility: without ConfigSource the selection order is unchanged; a source with only Lookup is still selected (the *app.Cache row is rejected only because of its name); the rows that used the test type *app.MapSource now use doger.MapSource, so TestPropertySource_Selection is rewritten (same assertions); the instance variable in the late-registration scenario follows the new mapping -->

## REMOVED Requirements

### Requirement: The environment source maps keys to variable names
**Reason**: The one-to-one mapping (`-` to `__`, `/` to `___`) is replaced by Spring Boot's relaxed mapping, under which distinct keys can share a variable; the promise "distinct keys never share a variable" no longer holds, so the requirement is replaced rather than edited.
**Migration**: See "The environment source maps keys by Spring Boot's rules". Rename `DOGER_REDIS_POOL__SIZE` to `DOGER_REDIS_POOLSIZE` and `DOGER_REDIS___QUEUE_ADDR` to `DOGER_REDIS__QUEUE_ADDR`. Warning: an old name with `__` from a dashed name, such as `DOGER_EXAMPLE__KV_ADDR` for `example-kv.addr`, is now read as the instance key `example/kv.addr`; rename it to `DOGER_EXAMPLEKV_ADDR`.

## ADDED Requirements

### Requirement: Keys may index lists and maps
A segment after the name SHALL accept index suffixes `[i]`, where `i` is `0` or a decimal number without a leading zero, and map-key suffixes `[k]`, where `k` is non-empty, holds no `[`, `]` or whitespace, and starts with neither a digit nor `-`; the name and the instance key take no suffix. `Lookup` of a key with a malformed suffix SHALL return `("", false)` without consulting the source.

#### Scenario: Index and map-key suffixes
```gherkin
Given a root scope with InjectorOpts.PropertySource set to a source that records every key it receives
And an extension "redis" whose Extend calls x.Properties().Lookup("<key>")
When the consumer calls doger.Extend(root, redisExt)
Then the source received <received>

Examples:
  | key               | received                  |
  | hosts[0]          | "redis.hosts[0]"          |
  | hosts[10].port    | "redis.hosts[10].port"    |
  | labels[team/core] | "redis.labels[team/core]" |
  | labels[Team-A]    | "redis.labels[Team-A]"    |
  | matrix[0][1]      | "redis.matrix[0][1]"      |
  | hosts[]           | no key                    |
  | hosts[-1]         | no key                    |
  | hosts[01]         | no key                    |
  | hosts[a           | no key                    |
  | hosts]0[          | no key                    |
  | [0]               | no key                    |
  | labels[team core] | no key                    |
  | labels[5xx]       | no key                    |
```

<!-- cases: N/A 1 default, 2 overrides: grammar only -->
<!-- cases: 3 zero values: empty brackets row -->
<!-- cases: 4 boundaries: index 0 and 10 versus a leading zero; one and two suffixes; a map key with a slash and with upper case; a suffix right after the view prefix ([0]) -->
<!-- cases: 5 errors: malformed suffixes are a miss -->
<!-- cases: N/A 6 duplicate, 7 lifecycle, 8 concurrency, 9 panics: pure validation of one string -->
<!-- cases: 10 compatibility: every key valid before stays valid; map keys starting with a digit (such as 5xx) cannot be addressed, documented -->

### Requirement: The environment source maps keys by Spring Boot's rules
`EnvPropertySource(prefix)` SHALL map a key to the normalized prefix followed by the key upper-cased, with `-` removed, `/` replaced by `__`, `.` by `_`, and `[i]` or `[k]` by `_` and the upper-cased index or key. A map key outside `[a-z0-9]+` SHALL be a miss. Keys that differ only in dashes, or in `[k]` versus `.k`, SHALL map to the same variable. `EnvPropertySource` SHALL return a `PropertySource` that also implements `PropertyKeys`.

#### Scenario: Key to variable mapping
```gherkin
Given the environment variable "<variable>" is set to "v"
When the consumer calls doger.EnvPropertySource("DOGER_").Lookup("<key>")
Then the result is ("v", true)

Examples:
  | key                 | variable                 |
  | redis.addr          | DOGER_REDIS_ADDR         |
  | redis.cache.addr    | DOGER_REDIS_CACHE_ADDR   |
  | redis/cache.addr    | DOGER_REDIS__CACHE_ADDR  |
  | redis.pool-size     | DOGER_REDIS_POOLSIZE     |
  | redis.hosts[0]      | DOGER_REDIS_HOSTS_0      |
  | redis.hosts[1].port | DOGER_REDIS_HOSTS_1_PORT |
  | redis.labels[team]  | DOGER_REDIS_LABELS_TEAM  |
```

#### Scenario: Nested, instance and index keys use different variables
```gherkin
Given the environment variable "DOGER_REDIS_QUEUE_ADDR" is set to "nested", "DOGER_REDIS__QUEUE_ADDR" to "instance" and "DOGER_REDIS_QUEUE_0_ADDR" to "index"
When the consumer looks up "<key>" with doger.EnvPropertySource("DOGER_")
Then the result is ("<value>", true)

Examples:
  | key                 | value    |
  | redis.queue.addr    | nested   |
  | redis/queue.addr    | instance |
  | redis.queue[0].addr | index    |
```

#### Scenario: Keys that share a variable
```gherkin
Given the environment variable "<variable>" is set to "shared"
And first and second are "<first>" and "<second>"
When the consumer looks up first and second with doger.EnvPropertySource("DOGER_")
Then both results are ("shared", true)

Examples:
  | first              | second            | variable                 |
  | redis.pool-size    | redis.poolsize    | DOGER_REDIS_POOLSIZE     |
  | redis.cache-addr   | redis.cacheaddr   | DOGER_REDIS_CACHEADDR    |
  | redis.queue[0]     | redis.queue.0     | DOGER_REDIS_QUEUE_0      |
  | redis.labels[team] | redis.labels.team | DOGER_REDIS_LABELS_TEAM  |
  | my-redis.addr      | myredis.addr      | DOGER_MYREDIS_ADDR       |
  | redis/cache-a.addr | redis/cachea.addr | DOGER_REDIS__CACHEA_ADDR |
```

#### Scenario: Map keys the environment cannot address
```gherkin
Given the environment variables "DOGER_REDIS_LABELS_TEAM__CORE" and "DOGER_REDIS_LABELS_TEAMA" are set to "x"
When the consumer looks up "<key>" with doger.EnvPropertySource("DOGER_")
Then the result is ("", false)

Examples:
  | key                     |
  | redis.labels[team/core] |
  | redis.labels[Team-A]    |
```

#### Scenario: An unset variable is a miss
```gherkin
Given the environment variable "DOGER_REDIS_ADDR" is unset
When the consumer calls doger.EnvPropertySource("DOGER_").Lookup("redis.addr")
Then the result is ("", false)
```

#### Scenario: Variables in the old mapping are no longer read
```gherkin
Given the environment variable "<old>" is set to "old" and no variable of the new mapping is set
When the consumer calls doger.EnvPropertySource("DOGER_").Lookup("<key>")
Then the result is <result>

Examples:
  | key              | old                      | result         |
  | redis.pool-size  | DOGER_REDIS_POOL__SIZE   | ("", false)    |
  | redis/queue.addr | DOGER_REDIS___QUEUE_ADDR | ("", false)    |
  | example-kv.addr  | DOGER_EXAMPLE__KV_ADDR   | ("", false)    |
  | example/kv.addr  | DOGER_EXAMPLE__KV_ADDR   | ("old", true)  |
```

<!-- cases: 1 default: the DOGER_ rows -->
<!-- cases: N/A 2 overrides: the prefix override is "The environment prefix is normalized" -->
<!-- cases: 3 zero values: an empty variable value is ("", true) as os.LookupEnv reports it -->
<!-- cases: 4 boundaries: one, two and three segments, dash, instance separator, index, index then segment, map key -->
<!-- cases: 5 errors: unaddressable map keys miss -->
<!-- cases: 6 duplicate: shared-variable rows, accepted as in Spring, including dashed extension names, instance keys and [k] versus .k; a nested group (_), an instance (__) and an index (_0) stay distinct -->
<!-- cases: N/A 7 lifecycle: the source reads the environment at each call -->
<!-- cases: N/A 8 concurrency: os.LookupEnv is safe for concurrent use -->
<!-- cases: N/A 9 panics: Lookup never panics -->
<!-- cases: 10 compatibility: BREAKING, variable names of keys with dashes or instances change; an old dashed name with __ is now read as an instance key (example/kv.addr row), stated in the REMOVED requirement's migration and the upgrading note; ExampleEnvPropertySource and docs/docs/extensions/installing.md are rewritten -->

### Requirement: The environment prefix is normalized
`EnvPropertySource(prefix)` SHALL turn `-` and `.` into `_`, upper-case the result and append `_` when missing; `""` means no prefix. A prefix still holding a character outside `[A-Z0-9_]` SHALL never panic: installed through `InjectorOpts.PropertySource` it SHALL fail the constructor with `ErrInvalidConfigSource`; otherwise every lookup SHALL miss and the property-source decision log line SHALL name the invalid prefix.

#### Scenario: Prefix forms
```gherkin
Given the environment variable "<variable>" is set to "v"
When the consumer calls doger.EnvPropertySource("<prefix>").Lookup("redis.addr")
Then the result is ("v", true)

Examples:
  | prefix  | variable           |
  | DOGER_  | DOGER_REDIS_ADDR   |
  | MYAPP_  | MYAPP_REDIS_ADDR   |
  | MYAPP   | MYAPP_REDIS_ADDR   |
  | myapp   | MYAPP_REDIS_ADDR   |
  | my-app  | MY_APP_REDIS_ADDR  |
  | my.app_ | MY_APP_REDIS_ADDR  |
  |         | REDIS_ADDR         |
```

#### Scenario: An invalid prefix never panics
```gherkin
Given a Logf that records lines and src := doger.EnvPropertySource("MY APP")
When the consumer <uses>
Then <outcome>

Examples:
  | uses                                                                                                   | outcome                                                                                                         |
  | calls doger.NewWithOpts(&doger.InjectorOpts{PropertySource: src, Logf: logf})                           | the root is nil and the error matches doger.ErrInvalidConfigSource and contains "invalid env prefix \"MY APP\"" |
  | registers src as the root's lazy doger.PropertySource service and reads "redis.addr" through an extension | the read returns ("", false) and one recorded line contains "invalid env prefix \"MY APP\""                    |
```

<!-- cases: 1 default: DOGER_ row -->
<!-- cases: 2 overrides: MYAPP_, MYAPP, myapp, dashed and dotted prefixes, and the empty prefix -->
<!-- cases: 3 zero values: empty prefix row -->
<!-- cases: 4 boundaries: with and without a trailing _; lower case; separators -->
<!-- cases: 5 errors: the invalid prefix fails construction when installed through options, otherwise misses and is logged -->
<!-- cases: N/A 6 duplicate: one prefix per source -->
<!-- cases: N/A 7 lifecycle: the prefix is normalized once, when the source is made -->
<!-- cases: N/A 8 concurrency: the source is immutable -->
<!-- cases: 9 panics: EnvPropertySource never panics; the prefix is not a secret, so it may be logged -->
<!-- cases: 10 compatibility: BREAKING for prefixes that relied on being used as written: "myapp_" now reads MYAPP_, "my-app" now reads MY_APP_ (it never matched a variable before); config.EnvPrefix follows the same rule (config-files) -->

### Requirement: A property source can list its keys
A source that implements `PropertyKeys` SHALL return from `Keys(prefix)`, sorted byte-wise and each once, every key whose uniform form matches the prefix's segment by segment, spelled so that the same source's `Lookup` finds it. Uniform form lowercases, drops `-` and `_` per segment, and folds `[k]` (k in `[a-z0-9]+`) into `.k` and `.n` (all digits) into `[n]`; an instance segment never folds. An empty prefix returns every key.

#### Scenario: Map source keys
```gherkin
Given src := doger.MapSource{"redis.hosts[0]": "a", "redis.hosts[1]": "b", "redis.hostsx": "c", "redis.addr": "d", "redis": "e", "server.allowedhosts[0]": "f", "redis.labels[team]": "g", "redis.queue.0": "h"}
When the consumer calls src.Keys("<prefix>")
Then the result is <keys>

Examples:
  | prefix               | keys                                                                                                                    |
  | redis.hosts          | ["redis.hosts[0]", "redis.hosts[1]"]                                                                                    |
  | redis                | ["redis", "redis.addr", "redis.hosts[0]", "redis.hosts[1]", "redis.hostsx", "redis.labels[team]", "redis.queue.0"]      |
  | server.allowed-hosts | ["server.allowedhosts[0]"]                                                                                              |
  | redis.labels.team    | ["redis.labels[team]"]                                                                                                  |
  | redis.queue[0]       | ["redis.queue.0"]                                                                                                       |
  | kafka                | []                                                                                                                      |
```

#### Scenario: Every key Keys returns is found by Lookup
```gherkin
Given src := doger.MapSource{"server.allowedhosts[0]": "f", "a.read-timeout": "1", "a.readtimeout": "2"}
When the consumer calls src.Keys("<prefix>") and then src.Lookup on each returned key
Then the keys are <keys> and every Lookup returns found

Examples:
  | prefix               | keys                                |
  | server.allowed-hosts | ["server.allowedhosts[0]"]          |
  | a                    | ["a.read-timeout", "a.readtimeout"] |
```

#### Scenario: Every key and the nil map
```gherkin
Given src := <source>
When the consumer calls src.Keys("")
Then the result is <keys>

Examples:
  | source                                    | keys                     |
  | doger.MapSource{"b.x": "1", "a.y": "2"}   | ["a.y", "b.x"]           |
  | doger.MapSource(nil)                      | []                       |
```

#### Scenario: A map source reads the map it is
```gherkin
Given m := doger.MapSource{"redis.addr": "a:1"}
And the consumer then sets m["redis.port"] = "1" before any read
When the consumer calls m.Keys("redis")
Then the result is ["redis.addr", "redis.port"]
```

<!-- cases: 1 default: the env and map sources implement it -->
<!-- cases: 2 overrides: any custom source may implement PropertyKeys; one that does not still works for scalars (config-binding) -->
<!-- cases: 3 zero values: empty prefix and nil map rows -->
<!-- cases: 4 boundaries: "redis.hostsx" is not under "redis.hosts" (segment-aware); exact-prefix key "redis"; byte-wise order puts [10] before [2]; [k] versus .k and .n versus [n] folding -->
<!-- cases: N/A 5 errors: Keys returns no error -->
<!-- cases: 6 duplicate: two stored keys with one uniform form are both returned; the binder picks the canonical spelling, else the lexically smallest (config-binding) -->
<!-- cases: 7 lifecycle: MapSource is not copied; its godoc says mutating it while in use is a fatal concurrent map write -->
<!-- cases: N/A 8 concurrency: a MapSource is read-only while in use; covered by "Concurrent lookups" for views -->
<!-- cases: N/A 9 panics: Keys does not panic -->
<!-- cases: 10 compatibility: PropertySource is unchanged, so existing sources keep compiling -->

### Requirement: The environment source lists its keys
The `PropertySource` returned by `EnvPropertySource(prefix)` SHALL implement `PropertyKeys`, mapping each variable under the prefix back to a key: `__` after the name marks an instance (never folded into an index), an all-digit segment without a leading zero becomes an index `[i]`, and every other segment becomes a lowercase dash-free name. Variables that cannot be mapped back SHALL be skipped.

#### Scenario: Environment source keys
```gherkin
Given clearEnvPrefix(t, "TKEYS_") and only these variables start with "TKEYS_": "TKEYS_REDIS_HOSTS_0" = "a", "TKEYS_REDIS_HOSTS_1" = "b", "TKEYS_REDIS_POOLSIZE_0" = "5", "TKEYS_REDIS__QUEUE_ADDR" = "q", "TKEYS_REDIS__0_ADDR" = "i", "TKEYS_REDIS" = "r", "TKEYS__X" = "1", "TKEYS_REDIS___Y" = "2", "TKEYS_redis_z" = "3", "TKEYS_REDIS_HOSTS_01" = "4", "TKEYS_REDIS_HOSTS__X" = "6", "TKEYS_0_X" = "7"
When the consumer calls doger.EnvPropertySource("TKEYS_").(doger.PropertyKeys).Keys("<prefix>")
Then the result is <keys>

Examples:
  | prefix          | keys                                                                                       |
  | redis.hosts     | ["redis.hosts[0]", "redis.hosts[1]"]                                                       |
  | redis.pool-size | ["redis.poolsize[0]"]                                                                      |
  | redis/queue     | ["redis/queue.addr"]                                                                       |
  | redis/0         | ["redis/0.addr"]                                                                           |
  |                 | ["redis", "redis.hosts[0]", "redis.hosts[1]", "redis.poolsize[0]", "redis/0.addr", "redis/queue.addr"] |
  | kafka           | []                                                                                         |
```

<!-- cases: 1 default: the scan of a prefix -->
<!-- cases: N/A 2 overrides: the prefix is the override -->
<!-- cases: 3 zero values: a prefix with no variables returns []; an empty prefix returns every key -->
<!-- cases: 4 boundaries: a single-segment variable, an index, an instance, an all-digit instance (redis/0, not redis[0]) -->
<!-- cases: 5 errors: malformed names are skipped, not errors: an empty segment (__X right after the prefix), triple underscore, lower case, a leading zero (HOSTS_01), __ after the name position (HOSTS__X), an all-digit first segment (0_X) -->
<!-- cases: N/A 6 duplicate: a variable maps to one key -->
<!-- cases: N/A 7 lifecycle: read at each call -->
<!-- cases: N/A 8 concurrency: os.Environ is safe for concurrent use -->
<!-- cases: N/A 9 panics: Keys does not panic -->
<!-- cases: 10 compatibility: new method on the unexported concrete type; EnvPropertySource still returns PropertySource; clearEnvPrefix keeps inherited TKEYS_ variables out of the test -->

### Requirement: Extension views list keys relative to the view
`x.Properties().Keys(k)` and `ExtensionProperties(i).Keys(k)` SHALL return the source's keys under the view's prefix plus `k`, with the view's prefix removed, dropping keys the key grammar rejects. They SHALL return `[]` when the source has no `PropertyKeys`, when `k` is not a valid key (including `""`), for the zero `Properties{}`, and, logging as `Lookup` does, after the view's `Extend` returned.

#### Scenario: Keys through an extension view
```gherkin
Given a root scope with InjectorOpts.PropertySource = <source>
And an extension "redis" with instance key "<instance>" whose Extend records x.Properties().Keys("<k>")
When the consumer calls doger.Extend(root, redisExt)
Then the recorded keys are <keys>

Examples:
  | source                                                                                     | instance | k     | keys                     |
  | doger.MapSource{"redis.hosts[0]": "a", "redis.hosts[1]": "b", "redis/cache.hosts[0]": "c"} |          | hosts | ["hosts[0]", "hosts[1]"] |
  | doger.MapSource{"redis.hosts[0]": "a", "redis.hosts[1]": "b", "redis/cache.hosts[0]": "c"} | cache    | hosts | ["hosts[0]"]             |
  | doger.MapSource{"redis.hosts[0]": "a", "redis.Hosts[1]": "b"}                              |          | hosts | ["hosts[0]"]             |
  | doger.MapSource{"redis.hosts[0]": "a"}                                                     |          | Hosts | []                       |
  | doger.MapSource{"redis.hosts[0]": "a"}                                                     |          |       | []                       |
  | a source with only a Lookup method holding "redis.hosts[0]" = "a"                          |          | hosts | []                       |
```

#### Scenario: Views that cannot list keys
```gherkin
Given a root scope with InjectorOpts.PropertySource = doger.MapSource{"redis.hosts[0]": "a"} and a Logf that records lines
When the consumer calls Keys("hosts") on <view>
Then the result is []
And the recorded lines contain <logged>

Examples:
  | view                                                           | logged                                                                      |
  | doger.Properties{}                                             | nothing                                                                     |
  | the Properties of extension "redis" kept after Extend returned | one line "DI: properties of extension redis read after its Extend returned" |
```

<!-- cases: 1 default: the map-source rows -->
<!-- cases: 2 overrides: a source without PropertyKeys -->
<!-- cases: 3 zero values: zero Properties row; an empty k returns [] on a view, while a source's Keys("") returns every key (stated in both requirements) -->
<!-- cases: 4 boundaries: with and without an instance key; a stored key with upper case is dropped -->
<!-- cases: 5 errors: an invalid k returns [] -->
<!-- cases: N/A 6 duplicate: keys come from the source, each once -->
<!-- cases: 7 lifecycle: a view used after Extend returned -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent lookups" -->
<!-- cases: N/A 9 panics: Keys does not panic -->
<!-- cases: N/A 10 compatibility: new method -->
