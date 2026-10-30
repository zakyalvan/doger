# Spec Delta

## Purpose

Binds configuration from the container's property source into typed Go structs, starting from the target struct, keeping prefilled defaults for absent keys, and exposing the result to apps and extensions as registered snapshots, live handles or one-off values.

## ADDED Requirements

### Requirement: Binding starts from the target struct
The binder SHALL walk the target struct's fields and look up each field's full key in the container's property source, so a key held only by an environment variable SHALL bind although no other layer has it.

#### Scenario: An env-only key binds
```gherkin
Given no InjectorOpts.ConfigSource or PropertySource
And the environment holds "DOGER_SERVICE_VERSION" = "9.9.9" and no other "DOGER_SERVICE_" variable
And root := doger.New()
And type ServiceConfig struct { Name string; Version string }
When the consumer calls doger.ResolveConfig(root, "service", ServiceConfig{Name: "demo"})
Then the result is ServiceConfig{Name: "demo", Version: "9.9.9"} and the error is nil
```

<!-- cases: 1 default: env-only default source; the config.Files variant of spike 4 is in config-files -->
<!-- cases: N/A 2 overrides: source choice is specified in extension-properties -->
<!-- cases: N/A 3-9: covered by the requirements below -->
<!-- cases: 10 compatibility: fixes spike 4 of golang-lib/config -->

### Requirement: Field keys are canonical kebab names
The binder SHALL derive each field's key segment by splitting the Go field name at case, acronym and digit-to-letter boundaries into lowercase kebab form, and SHALL pass `<prefix>.<segment>` to the source; an empty prefix SHALL produce the bare segment.

#### Scenario: The source receives canonical keys
```gherkin
Given a property source that records every key passed to Lookup and Keys and holds nothing
And a target struct whose only field is named <field>
When the consumer calls doger.ResolveConfig(root, "<prefix>", target)
Then the source received "<key>"

Examples:
  | field       | prefix | key                 |
  | Port        | server | server.port         |
  | ReadTimeout | server | server.read-timeout |
  | IssuerURI   | server | server.issuer-uri   |
  | TLSCert     | server | server.tls-cert     |
  | OAuth2Token | server | server.oauth2-token |
  | IPv6Addr    | server | server.ipv6-addr    |
  | AllowedURLs | server | server.allowed-urls |
  | IDs         | server | server.ids          |
  | S3Bucket    | server | server.s3-bucket    |
  | HTTPServer  | server | server.http-server  |
  | ID          | server | server.id           |
  | Port        |        | port                |
```

<!-- cases: 1 default: no tags needed -->
<!-- cases: 2 overrides: covered by "A tag overrides a field's key" -->
<!-- cases: 3 zero values: empty prefix row -->
<!-- cases: 4 boundaries: one-letter-free acronyms at start, middle and end; digit inside a word; plural acronym -->
<!-- cases: N/A 5 errors: naming cannot fail; conflicts have their own requirement -->
<!-- cases: N/A 6-9: pure naming -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Keys are matched in uniform form
The binder SHALL compare the keys a source's `Keys` returns with the field's key in uniform form (lowercase, `-` and `_` removed per segment, `[...]` contents untouched), so a source that reports dash-free keys SHALL still fill lists, maps and pointers whose names have several words.

#### Scenario: Multi-word lists, maps and pointers bind from env
```gherkin
Given no InjectorOpts.ConfigSource or PropertySource
And the environment holds "DOGER_SERVER_ALLOWEDHOSTS_0" = "a", "DOGER_SERVER_ALLOWEDHOSTS_1" = "b", "DOGER_SERVER_EXTRALABELS_TEAM" = "core" and "DOGER_SERVER_CLIENTTLS_ENABLED" = "true"
And type ServerConfig struct { AllowedHosts []string; ExtraLabels map[string]string; ClientTLS *TLSConfig }
When the consumer calls doger.ResolveConfig(doger.New(), "server", ServerConfig{})
Then the result is ServerConfig{AllowedHosts: ["a", "b"], ExtraLabels: {"team": "core"}, ClientTLS: &TLSConfig{Enabled: true}}
```

<!-- cases: 1 default: env-only source -->
<!-- cases: N/A 2 overrides: matching is not configurable -->
<!-- cases: N/A 3 zero values: an empty Keys result binds nothing -->
<!-- cases: 4 boundaries: list, map and pointer, each with a two-word name -->
<!-- cases: N/A 5-9: as for every bind -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A tag overrides a field's key
A struct tag under `InjectorOpts.ConfigTagKey` (default `cfg`) SHALL replace the field's derived key segment, `cfg:"-"` SHALL skip the field, an empty tag SHALL behave as no tag, and unexported fields SHALL be skipped.

#### Scenario: Tags rename or skip fields
```gherkin
Given a doger.MapSource holding "server.tls-cert-file" = "/etc/tls/cert.pem", "server.tls-cert" = "wrong", "server.secret" = "x" and "server.plain" = "p"
And type ServerConfig struct { TLSCert string `cfg:"tls-cert-file"`; Secret string `cfg:"-"`; Plain string `cfg:""`; hidden string }
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then the result is ServerConfig{TLSCert: "/etc/tls/cert.pem", Plain: "p"}
```

#### Scenario: The tag key can be changed
```gherkin
Given root := doger.MustNewWithOpts(&doger.InjectorOpts{PropertySource: doger.MapSource{"server.cert": "/c.pem"}, ConfigTagKey: "<tagKey>"})
And type ServerConfig struct { TLSCert string `cfg:"wrong" conf:"cert"` }
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then the result's TLSCert is "<value>"

Examples:
  | tagKey | value  |
  |        |        |
  | conf   | /c.pem |
```

<!-- cases: 1 default: cfg tag key, empty ConfigTagKey row -->
<!-- cases: 2 overrides: rename, skip, another tag key -->
<!-- cases: 3 zero values: cfg:"" and ConfigTagKey "" rows -->
<!-- cases: N/A 4 boundaries: one tag per field -->
<!-- cases: 5 errors: covered by "An invalid tag fails the bind" -->
<!-- cases: N/A 6-9: pure naming -->
<!-- cases: 10 compatibility: golang-lib/config cfg tags keep working when they hold kebab segments -->

### Requirement: An invalid tag fails the bind
A tag value that is not one valid key segment (`[a-z0-9]+(-[a-z0-9]+)*`), including one that holds `,`, SHALL fail the bind with an error matching `ErrInvalidConfigTarget` that names the struct type and the field, before any source is consulted.

#### Scenario: Invalid tags
```gherkin
Given type LogConfig struct { A string `cfg:"<tag>"` }
When the consumer calls doger.ResolveConfig(root, "log", LogConfig{})
Then errors.Is(err, doger.ErrInvalidConfigTarget) is true
And the error message is "DI: config: invalid target: LogConfig.A: tag \"<tag>\" is not a key segment"

Examples:
  | tag        |
  | TLSCert    |
  | tls.cert   |
  | Bad Name   |
  | file,omit  |
  | -file      |
```

<!-- cases: N/A 1 default, 2 overrides: error path -->
<!-- cases: 3 zero values: an empty tag is no tag, see "A tag overrides a field's key" -->
<!-- cases: 4 boundaries: uppercase, dotted, space, comma, leading dash -->
<!-- cases: 5 errors: sentinel matchable with errors.Is; text pinned -->
<!-- cases: N/A 6-9: checked before any lookup -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Two fields with one uniform key fail the bind
A struct with two bound fields whose keys are equal in uniform form SHALL fail the bind with an error matching `ErrConfigNameConflict` that names the struct type, both fields and the shared key, before any source is consulted.

#### Scenario: Conflicting names
```gherkin
Given type <type>
When the consumer calls doger.ResolveConfig(root, "log", LogConfig{})
Then errors.Is(err, doger.ErrConfigNameConflict) is true
And the error message is "<message>"

Examples:
  | type                                                      | message                                                                       |
  | LogConfig struct { LogFile string; Logfile string }       | DI: config: fields LogFile and Logfile of LogConfig share the key "logfile"   |
  | LogConfig struct { A string `cfg:"path"`; Path string }  | DI: config: fields A and Path of LogConfig share the key "path"               |
  | LogConfig struct { Common; Path string } with Common { Path string } | DI: config: fields Common.Path and Path of LogConfig share the key "path" |
```

<!-- cases: N/A 1 default, 2 overrides: error path only -->
<!-- cases: N/A 3 zero values: a struct with no fields has nothing to conflict -->
<!-- cases: 4 boundaries: derived versus tagged versus promoted names -->
<!-- cases: 5 errors: sentinel matchable with errors.Is; text pinned -->
<!-- cases: 6 duplicate: the subject -->
<!-- cases: N/A 7-9: checked before any lookup -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Presence decides, not the value
A field SHALL be written only when the source holds its key, and then even when the value converts to the zero value; a field whose key is absent SHALL keep the value it has in the defaults, and keys under the prefix that match no field SHALL be ignored.

#### Scenario: Present zero values overwrite defaults, absent keys keep them
```gherkin
Given a doger.MapSource holding <held>
And defaults ServerConfig{Port: 8080, Debug: true, Host: "0.0.0.0"}
When the consumer calls doger.ResolveConfig(root, "server", defaults)
Then the result is <result> and the error is nil

Examples:
  | held                       | result                                                    |
  | nothing                    | ServerConfig{Port: 8080, Debug: true, Host: "0.0.0.0"}    |
  | "server.port" = "0"        | ServerConfig{Port: 0, Debug: true, Host: "0.0.0.0"}       |
  | "server.debug" = "false"   | ServerConfig{Port: 8080, Debug: false, Host: "0.0.0.0"}   |
  | "server.host" = ""         | ServerConfig{Port: 8080, Debug: true, Host: ""}           |
  | "server.typo" = "x"        | ServerConfig{Port: 8080, Debug: true, Host: "0.0.0.0"}    |
```

<!-- cases: 1 default: nothing row -->
<!-- cases: N/A 2 overrides: defaults are the override mechanism -->
<!-- cases: 3 zero values: 0, false, "" rows -->
<!-- cases: 4 boundaries: absent versus present-empty; unknown key -->
<!-- cases: N/A 5 errors: covered by conversion requirements -->
<!-- cases: N/A 6-9: pure binding -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Nested and embedded structs bind field by field
A struct-typed field SHALL bind recursively under its own key, keeping the defaults of sibling fields; an exported or unexported non-pointer embedded struct SHALL bind its promoted exported fields under the parent's key, and an embedded pointer SHALL be treated as a pointer field.

#### Scenario: One nested key keeps its siblings' defaults
```gherkin
Given a doger.MapSource holding "redis.tls.enabled" = "true"
And defaults RedisConfig{Addr: "localhost:6379", TLS: TLSConfig{Enabled: false, MinVersion: "1.2"}}
When the consumer calls doger.ResolveConfig(root, "redis", defaults)
Then the result is RedisConfig{Addr: "localhost:6379", TLS: TLSConfig{Enabled: true, MinVersion: "1.2"}}
```

#### Scenario: Embedded structs bind under the parent's key
```gherkin
Given a doger.MapSource holding "server.timeout" = "5s"
And type <common> struct { Timeout time.Duration } embedded in type ServerConfig struct { <common>; Port int }
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then the result's Timeout is 5s

Examples:
  | common |
  | Common |
  | common |
```

<!-- cases: 1 default: untouched nested defaults -->
<!-- cases: N/A 2 overrides -->
<!-- cases: 3 zero values: an empty nested struct stays empty -->
<!-- cases: 4 boundaries: exported and unexported embedded types; embedded pointer is covered by the pointer requirement -->
<!-- cases: N/A 5-9: as for scalars -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Recursive types are rejected
A target type that reaches itself through any field, pointer, slice, array or map element SHALL fail the bind with an error matching `ErrInvalidConfigTarget` that names the type and the field path, before any source is consulted.

#### Scenario: Recursive targets
```gherkin
Given type <type>
When the consumer calls doger.ResolveConfig(root, "x", Node{})
Then errors.Is(err, doger.ErrInvalidConfigTarget) is true
And the error message contains "recursive type Node at <path>"

Examples:
  | type                                          | path        |
  | Node struct { Next *Node }                    | Next        |
  | Node struct { Kids []Node }                   | Kids        |
  | Node struct { ByName map[string]*Node }       | ByName      |
  | Node struct { Inner struct { Back *Node } }   | Inner.Back  |
```

<!-- cases: N/A 1 default, 2 overrides: error path -->
<!-- cases: N/A 3 zero values -->
<!-- cases: 4 boundaries: direct, through slice, map and nested anonymous struct -->
<!-- cases: 5 errors: sentinel; never a stack overflow -->
<!-- cases: N/A 6-8 -->
<!-- cases: 9 panics: an unbounded recursion would be a fatal error, so it is rejected up front -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Lists come whole from indexed keys
A slice field SHALL bind from indexed keys `<key>[i]` found through `Keys`, with exactly one key for every index from 0 to the highest, replacing the default slice; each element SHALL start from its zero value. A gap, a non-numeric index or an index above 10000 SHALL fail with `ErrInvalidConfigValue` before any allocation.

#### Scenario: Indexed list sources
```gherkin
Given a doger.MapSource holding <held>
And defaults ServerConfig{Hosts: []string{"d1", "d2", "d3"}}
When the consumer calls doger.ResolveConfig(root, "server", defaults)
Then the result's Hosts is <hosts> and the error is <error>

Examples:
  | held                                                 | hosts               | error                  |
  | nothing                                              | ["d1", "d2", "d3"]  | nil                    |
  | "server.hosts[0]" = "a"                              | ["a"]               | nil                    |
  | "server.hosts[0]" = "a", "server.hosts[1]" = "b"     | ["a", "b"]          | nil                    |
  | "server.hosts[0]" = "a", "server.hosts[2]" = "c"     | zero result         | ErrInvalidConfigValue  |
  | "server.hosts[x]" = "a"                              | zero result         | ErrInvalidConfigValue  |
  | "server.hosts[10000]" = "a" and indices 0 to 9999    | 10001 elements      | nil                    |
  | "server.hosts[10001]" = "a"                          | zero result         | ErrInvalidConfigValue  |
```

#### Scenario: A list of structs starts each element from zero
```gherkin
Given a doger.MapSource holding "app.upstreams[0].name" = "a", "app.upstreams[0].port" = "80" and "app.upstreams[1].name" = "b"
And defaults AppConfig{Upstreams: []Upstream{{Name: "d", Port: 9}}}
When the consumer calls doger.ResolveConfig(root, "app", defaults)
Then the result's Upstreams is [{Name: "a", Port: 80}, {Name: "b", Port: 0}]
```

<!-- cases: 1 default: nothing row keeps the default list -->
<!-- cases: N/A 2 overrides: see "A single value is split on commas" -->
<!-- cases: N/A 3 zero values: an empty list comes from the comma rule -->
<!-- cases: 4 boundaries: 0, 1, many elements; index at and one past 10000 -->
<!-- cases: 5 errors: gap, non-numeric and too-large rows match ErrInvalidConfigValue -->
<!-- cases: N/A 6 duplicate: one key per index after uniform matching -->
<!-- cases: N/A 7-8: pure binding -->
<!-- cases: 9 panics: a huge index never allocates -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A single value is split on commas
A slice field whose key has no indexed keys but a value under `<key>` SHALL bind by splitting that value on `,` and trimming ASCII spaces around each element, keeping empty elements; an empty value SHALL give an empty, non-nil slice, and indexed keys SHALL win over a value in the same source. A named slice type implementing `encoding.TextUnmarshaler` SHALL bind as a scalar instead.

#### Scenario: Comma values
```gherkin
Given a doger.MapSource holding <held>
And type ServerConfig struct { Hosts []string; Peers PeerList } where PeerList is []string with an UnmarshalText splitting on ";"
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then the result is <result>

Examples:
  | held                                              | result                                      |
  | "server.hosts" = "a, b,c"                         | ServerConfig{Hosts: ["a", "b", "c"]}        |
  | "server.hosts" = "a,,b"                           | ServerConfig{Hosts: ["a", "", "b"]}         |
  | "server.hosts" = "a,b,"                           | ServerConfig{Hosts: ["a", "b", ""]}         |
  | "server.hosts" = ""                               | ServerConfig{Hosts: []}                     |
  | "server.hosts" = "x,y", "server.hosts[0]" = "a"   | ServerConfig{Hosts: ["a"]}                  |
  | "server.peers" = "p1,x;p2"                        | ServerConfig{Peers: ["p1,x", "p2"]}         |
```

<!-- cases: 1 default: plain comma row -->
<!-- cases: 2 overrides: a TextUnmarshaler slice type opts out of comma splitting -->
<!-- cases: 3 zero values: empty value, empty elements -->
<!-- cases: 4 boundaries: leading, inner and trailing empty elements -->
<!-- cases: N/A 5 errors: element conversion errors as for scalars -->
<!-- cases: 6 duplicate: indexed keys win over a value in one source; across layers, see config-files -->
<!-- cases: N/A 7-9: pure binding -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Maps merge by key
A map field with a `string`-kinded key SHALL keep its default entries and set one entry per key found under the field's key, written nested as `<key>.<name>` or bracketed as `<key>[<name>]`; a bracketed name SHALL be kept verbatim and SHALL win over the nested form, and a nil default map SHALL stay nil when no entry exists.

#### Scenario: Map entries merge over defaults
```gherkin
Given a doger.MapSource holding <held>
And defaults ServerConfig{Labels: <default>}
When the consumer calls doger.ResolveConfig(root, "server", defaults)
Then the result's Labels is <labels>

Examples:
  | held                                                                   | default                              | labels                                               |
  | nothing                                                                | nil                                  | nil                                                  |
  | "server.labels.team" = "core"                                          | {"team": "default", "env": "prod"}   | {"team": "core", "env": "prod"}                      |
  | "server.labels[App.Kartala/Tier]" = "web"                              | nil                                  | {"App.Kartala/Tier": "web"}                          |
  | "server.labels.team" = "a", "server.labels[team]" = "b"                | nil                                  | {"team": "b"}                                        |
  | "server.labels.team.sub" = "x"                                         | nil                                  | nil                                                  |
```

#### Scenario: A map of structs merges entries field by field
```gherkin
Given a doger.MapSource holding "db.replicas.east.addr" = "e:5432" and "db.replicas.west.addr" = "w:5432"
And defaults DBConfig{Replicas: map[string]Replica{"east": {Addr: "x", Port: 1}}}
When the consumer calls doger.ResolveConfig(root, "db", defaults)
Then the result's Replicas is {"east": {Addr: "e:5432", Port: 1}, "west": {Addr: "w:5432", Port: 0}}
```

#### Scenario: Non-string map keys are rejected
```gherkin
Given type ServerConfig struct { Weights map[int]string }
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then errors.Is(err, doger.ErrInvalidConfigTarget) is true
And the error message contains "ServerConfig.Weights: map key type int is not a string kind"
```

<!-- cases: 1 default: default entries kept -->
<!-- cases: 2 overrides: a source entry replaces the scalar default entry; struct entries merge -->
<!-- cases: 3 zero values: nil default map stays nil, or is allocated on the first entry -->
<!-- cases: 4 boundaries: nested versus bracketed names; keys deeper than the value type are ignored -->
<!-- cases: 5 errors: non-string key kind -->
<!-- cases: 6 duplicate: same name nested and bracketed, bracketed wins -->
<!-- cases: N/A 7-9: pure binding -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Pointer fields are allocated only when a key exists under them
A pointer field SHALL stay as it is in the defaults when no key exists at or under its key, and otherwise SHALL bind into a copy of its default pointee, or a newly allocated zero value when the default is nil.

#### Scenario: Pointer allocation
```gherkin
Given a doger.MapSource holding <held>
And defaults ServerConfig{TLS: <default>} where TLS is a *TLSConfig
When the consumer calls doger.ResolveConfig(root, "server", defaults)
Then the result's TLS is <tls>

Examples:
  | held                           | default                         | tls                                            |
  | nothing                        | nil                             | nil                                            |
  | "server.tls.enabled" = "true"  | nil                             | &TLSConfig{Enabled: true}                      |
  | "server.tls.enabled" = "true"  | &TLSConfig{MinVersion: "1.2"}   | &TLSConfig{Enabled: true, MinVersion: "1.2"}   |
  | nothing                        | &TLSConfig{MinVersion: "1.2"}   | &TLSConfig{MinVersion: "1.2"}                  |
```

<!-- cases: 1 default: nil stays nil -->
<!-- cases: 2 overrides: default pointee kept and overlaid -->
<!-- cases: 3 zero values: nil default -->
<!-- cases: 4 boundaries: no key versus one key under the pointer -->
<!-- cases: N/A 5-9: pure binding -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Lists, maps and pointers need a source that lists its keys
When the container's property source does not implement `PropertyKeys`, a bind whose target has a slice, map or pointer field that needs `Keys` SHALL fail with an error matching `ErrInvalidConfigTarget` naming that field's key; scalar and nested struct fields SHALL bind through `Lookup` alone.

#### Scenario: A Lookup-only source
```gherkin
Given root := doger.MustNewWithOpts(&doger.InjectorOpts{PropertySource: lookupOnly}) where lookupOnly implements only Lookup and holds "server.port" = "9090"
When the consumer calls doger.ResolveConfig(root, "server", <target>)
Then the result is <result> and the error is <error>

Examples:
  | target                                          | result                      | error                                                                            |
  | ServerConfig{} with Port int                    | ServerConfig{Port: 9090}    | nil                                                                              |
  | ServerConfig{} with Port int and Hosts []string | zero result                 | matches ErrInvalidConfigTarget and contains "server.hosts needs a source that lists its keys" |
```

<!-- cases: 1 default: scalar row -->
<!-- cases: 2 overrides: implement PropertyKeys or wrap the values in doger.MapSource -->
<!-- cases: N/A 3 zero values -->
<!-- cases: 4 boundaries: scalar-only versus one list field -->
<!-- cases: 5 errors: sentinel and text -->
<!-- cases: N/A 6-9 -->
<!-- cases: 10 compatibility: PropertySource implementations without Keys keep working for scalars -->

### Requirement: Scalars are converted in a fixed order
The binder SHALL convert a raw value, untrimmed, by the first matching rule: `time.Duration` with `time.ParseDuration`; `url.URL` and `*url.URL` with `url.Parse`; a type whose pointer implements `encoding.TextUnmarshaler`; then by kind with `strconv` (base 10, the field's bit size; `ParseBool` spellings); then the slice, map, pointer and struct rules.

#### Scenario: Conversions
```gherkin
Given a doger.MapSource holding "x.v" = "<raw>"
And a target struct whose field V has type <type>
When the consumer calls doger.ResolveConfig(root, "x", target)
Then the result's V is <value> and the error is nil

Examples:
  | type          | raw                   | value                                   |
  | string        | " hello "             | " hello "                               |
  | int           | -42                   | -42                                     |
  | int8          | 127                   | 127                                     |
  | uint16        | 65535                 | 65535                                   |
  | float32       | 1.5                   | 1.5                                     |
  | bool          | true                  | true                                    |
  | bool          | 1                     | true                                    |
  | bool          | F                     | false                                   |
  | time.Duration | 1m30s                 | 90s                                     |
  | time.Duration | 0                     | 0                                       |
  | url.URL       | https://kartala.id/a  | a url.URL with Host "kartala.id"        |
  | *url.URL      | https://kartala.id/a  | a *url.URL with Host "kartala.id"       |
  | net.IP        | 10.0.0.1              | net.ParseIP("10.0.0.1")                 |
  | netip.Addr    | 10.0.0.1              | netip.MustParseAddr("10.0.0.1")         |
  | time.Time     | 2026-10-30T10:00:00Z  | time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC) |
  | slog.Level    | warn                  | slog.LevelWarn                          |
  | Port (uint16) | 8080                  | Port(8080)                              |
```

<!-- cases: 1 default: the basic kinds -->
<!-- cases: 2 overrides: TextUnmarshaler rows let a type define its own parsing, before the kind and slice rules (net.IP, slog.Level) -->
<!-- cases: 3 zero values: "0" duration; whitespace is kept for strings -->
<!-- cases: 4 boundaries: int8 max, uint16 max, bool spellings -->
<!-- cases: 5 errors: covered by "Conversion failures name the key and type only" -->
<!-- cases: N/A 6-9: pure conversion -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Conversion failures name the key and type only
A value that does not convert, or a key held for a field of an unsupported type, SHALL fail the bind with a `*ConfigBindError` matching `ErrInvalidConfigValue` whose `Key` is the full key and `Type` the target type, with message `DI: config: invalid value for <key> (<type>)`, never wrapping the parser's error.

#### Scenario: Conversion errors
```gherkin
Given a doger.MapSource holding "server.<key>" = "<raw>"
And type ServerConfig struct { Port int; Timeout time.Duration; Debug bool; Small int8; Count uint; Ratio float32; Ch chan int }
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then errors.As(err, &bindErr) is true with bindErr.Key "server.<key>" and bindErr.Type.String() "<type>"
And errors.Is(err, doger.ErrInvalidConfigValue) is true
And the error message is "DI: config: invalid value for server.<key> (<type>)"

Examples:
  | key     | raw         | type          |
  | port    | s3cret-80   | int           |
  | port    | 0x1F        | int           |
  | port    | 1_000       | int           |
  | port    | " 80"       | int           |
  | timeout | 30          | time.Duration |
  | debug   | yes-s3cret  | bool          |
  | small   | 128         | int8          |
  | count   | -1          | uint          |
  | ratio   | 1e40        | float32       |
  | ch      | s3cret      | chan int      |
```

<!-- cases: N/A 1 default, 2 overrides: error path -->
<!-- cases: 3 zero values: an untrimmed space fails for numbers -->
<!-- cases: 4 boundaries: int8 one past max, negative uint, float32 overflow, bare duration, other bases -->
<!-- cases: 5 errors: the subject; errors.Is and errors.As; message holds no value -->
<!-- cases: N/A 6-8: pure conversion -->
<!-- cases: 9 panics: covered by "A panic while binding is an error" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A panic while binding is an error
A panic raised during a bind by a type's `UnmarshalText` or by the source's `Lookup` or `Keys` SHALL be recovered and returned as an error matching `ErrInvalidConfigValue` that names the key being bound and the panic value's Go type only.

#### Scenario: Panics in user code
```gherkin
Given <panicking> panics with "bad s3cret" while binding "server.level"
And type ServerConfig struct { Level Level } where *Level implements encoding.TextUnmarshaler
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then errors.Is(err, doger.ErrInvalidConfigValue) is true
And the error message is "DI: config: invalid value for server.level (main.Level): panic with string"

Examples:
  | panicking                  |
  | Level.UnmarshalText        |
  | the source's Lookup        |
```

<!-- cases: N/A 1 default, 2 overrides: failure path -->
<!-- cases: N/A 3 zero values: panic(nil) is reported with the type *runtime.PanicNilError -->
<!-- cases: N/A 4 boundaries -->
<!-- cases: 5 errors: matchable sentinel; the value never appears -->
<!-- cases: N/A 6-8 -->
<!-- cases: 9 panics: the subject -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: The caller's defaults are never changed
The binder SHALL bind into a deep copy of the defaults it receives, so the caller's struct and the slices, maps and pointees reachable from it SHALL be unchanged after any bind, successful or not.

#### Scenario: Defaults survive binding
```gherkin
Given defaults := ServerConfig{Hosts: []string{"a"}, Labels: map[string]string{"team": "core"}, TLS: &TLSConfig{MinVersion: "1.2"}}
And a doger.MapSource holding "server.hosts[0]" = "b", "server.labels.team" = "ops", "server.tls.min-version" = "1.3" and <extra>
When the consumer calls doger.ResolveConfig(root, "server", defaults)
Then defaults still equals ServerConfig{Hosts: ["a"], Labels: {"team": "core"}, TLS: &TLSConfig{MinVersion: "1.2"}}

Examples:
  | extra                          |
  | nothing                        |
  | "server.port" = "not-a-number" |
```

<!-- cases: 1 default: successful bind row -->
<!-- cases: N/A 2 overrides -->
<!-- cases: N/A 3 zero values: nil slices and maps have nothing to alias -->
<!-- cases: 4 boundaries: success and failure rows -->
<!-- cases: 5 errors: failure row -->
<!-- cases: N/A 6-9 -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Targets and prefixes are checked
`BindConfig`, `BindConfigNamed` and `ResolveConfig` SHALL require `T` to be a struct type and `prefix` to be empty or a valid property key, and SHALL fail with an error matching `ErrInvalidConfigTarget` otherwise; a typed-nil injector SHALL fail the same way, while a nil injector SHALL mean `DefaultRootScope`.

#### Scenario: Invalid type parameters
```gherkin
When the consumer calls <call>
Then errors.Is(err, doger.ErrInvalidConfigTarget) is true
And the error message contains "<reason>"

Examples:
  | call                                                               | reason                          |
  | doger.BindConfig[*ServerConfig](root, "server")                    | *main.ServerConfig is not a struct |
  | doger.BindConfig[int](root, "server")                              | int is not a struct             |
  | doger.BindConfig[map[string]string](root, "server")                | map[string]string is not a struct |
  | doger.BindConfig[io.Reader](root, "server")                        | io.Reader is not a struct       |
  | doger.ResolveConfig(root, "server", (*ServerConfig)(nil))          | *main.ServerConfig is not a struct |
```

#### Scenario: Invalid prefixes
```gherkin
When the consumer calls doger.ResolveConfig(root, "<prefix>", ServerConfig{})
Then errors.Is(err, doger.ErrInvalidConfigTarget) is <invalid>

Examples:
  | prefix        | invalid |
  |               | false   |
  | server        | false   |
  | db.primary    | false   |
  | redis/queue   | false   |
  | hosts[0]      | false   |
  | Server        | true    |
  | server.       | true    |
  | .server       | true    |
  | a..b          | true    |
```

#### Scenario: A typed-nil injector
```gherkin
Given var scope *doger.Scope
When the consumer calls doger.ResolveConfig(scope, "server", ServerConfig{})
Then errors.Is(err, doger.ErrInvalidConfigTarget) is true
And the error message contains "nil *doger.Scope injector"
```

<!-- cases: N/A 1 default, 2 overrides: error path -->
<!-- cases: 3 zero values: empty prefix valid, typed-nil injector -->
<!-- cases: 4 boundaries: pointer, interface, map and basic types; valid and invalid prefix shapes -->
<!-- cases: 5 errors: sentinel and reason -->
<!-- cases: N/A 6-9: checked before any lookup -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: BindConfig binds, validates and registers a section
`BindConfig[T](i, prefix, opts...)` SHALL bind `T` under `prefix` from `i`'s container, starting from the last non-nil `ConfigDefaults` option, validate it (config-validation), and register in `i`'s scope `T` and `ConfigValue[T]` as one step; on any error it SHALL register neither. A nil `i` SHALL mean `DefaultRootScope`.

#### Scenario: A bound section is invocable
```gherkin
Given root := doger.MustNewWithOpts(&doger.InjectorOpts{PropertySource: doger.MapSource{"server.port": "9090"}})
When the consumer calls doger.BindConfig(root, "server", doger.ConfigDefaults(ServerConfig{Host: "0.0.0.0", Port: 8080}))
Then the error is nil
And doger.MustInvoke[ServerConfig](root) is ServerConfig{Host: "0.0.0.0", Port: 9090}
And doger.MustInvoke[doger.ConfigValue[ServerConfig]](root).Get() is ServerConfig{Host: "0.0.0.0", Port: 9090}
```

#### Scenario: Default options
```gherkin
Given root's property source holds nothing under "server"
When the consumer calls doger.BindConfig(root, "server", <opts>)
Then doger.MustInvoke[ServerConfig](root) is <value>

Examples:
  | opts                                                                                 | value                    |
  | no options (written doger.BindConfig[ServerConfig](root, "server"))                  | ServerConfig{}           |
  | doger.ConfigDefaults(ServerConfig{Port: 1}), doger.ConfigDefaults(ServerConfig{Port: 2}) | ServerConfig{Port: 2} |
  | doger.ConfigDefaults(ServerConfig{Port: 1}), nil                                     | ServerConfig{Port: 1}    |
```

#### Scenario: A failed bind registers nothing
```gherkin
Given root's property source holds "server.port" = "<raw>"
And type ServerConfig struct { Port int `validate:"min=1,max=65535"` }
When the consumer calls doger.BindConfig[ServerConfig](root, "server")
Then errors.Is(err, <sentinel>) is true
And doger.Invoke[ServerConfig](root) and doger.Invoke[doger.ConfigValue[ServerConfig]](root) both return errors matching doger.ErrServiceNotFound

Examples:
  | raw   | sentinel                    |
  | abc   | doger.ErrInvalidConfigValue |
  | 99999 | doger.ErrInvalidConfig      |
```

#### Scenario: A nil injector binds into DefaultRootScope
```gherkin
Given DefaultRootScope is swapped for a fresh root whose property source holds "nilinj.port" = "7070", restored at test cleanup, in a test that is not parallel
When the consumer calls doger.BindConfig[NilInjConfig](nil, "nilinj")
Then doger.MustInvoke[NilInjConfig](doger.DefaultRootScope).Port is 7070
```

#### Scenario: A child scope binds from its container and registers locally
```gherkin
Given root's property source holds "jobs.workers" = "4"
And child := root.Scope("jobs")
When the consumer calls doger.BindConfig[JobsConfig](child, "jobs")
Then doger.MustInvoke[JobsConfig](child).Workers is 4
And doger.Invoke[JobsConfig](root) returns an error matching doger.ErrServiceNotFound
```

<!-- cases: 1 default: no options row -->
<!-- cases: 2 overrides: ConfigDefaults; last one wins; a nil option is ignored -->
<!-- cases: 3 zero values: nil injector row; nil option row -->
<!-- cases: 4 boundaries: root and child scope; one and two options -->
<!-- cases: 5 errors: conversion and validation rows, nothing registered -->
<!-- cases: 6 duplicate: covered by "Binding a name twice fails" -->
<!-- cases: 7 lifecycle: covered by "Binding follows the container's phase" -->
<!-- cases: 8 concurrency: covered by "Concurrent binding and reads" -->
<!-- cases: N/A 9 panics: validator and binder panics have their own requirements -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: BindConfigNamed binds one type under several names
`BindConfigNamed[T](i, name, prefix, opts...)` SHALL behave as `BindConfig` but register `T` under `name` and `ConfigValue[T]` under `ConfigValueName(name)`, which SHALL be `name + ".value"`; an empty name SHALL fail with `ErrInvalidConfigTarget`.

#### Scenario: Two databases of one type
```gherkin
Given root's property source holds "db.primary.url" = "pg://p" and "db.replica.url" = "pg://r"
And doger.BindConfigNamed[DBConfig](root, "replica-db", "db.replica") returned nil
When the consumer calls doger.BindConfigNamed[DBConfig](root, "primary-db", "db.primary")
Then doger.MustInvokeNamed[DBConfig](root, "primary-db").URL is "pg://p"
And doger.MustInvokeNamed[DBConfig](root, "replica-db").URL is "pg://r"
And doger.MustInvokeNamed[doger.ConfigValue[DBConfig]](root, doger.ConfigValueName("primary-db")).Get().URL is "pg://p"
```

#### Scenario: An empty name is rejected
```gherkin
When the consumer calls doger.BindConfigNamed[ServerConfig](root, "", "server")
Then errors.Is(err, doger.ErrInvalidConfigTarget) is true
And the error message contains "empty service name"
And nothing is registered
```

<!-- cases: 1 default: as BindConfig -->
<!-- cases: N/A 2 overrides: as BindConfig -->
<!-- cases: 3 zero values: empty name -->
<!-- cases: 4 boundaries: two names, one type -->
<!-- cases: 5 errors: invalid target -->
<!-- cases: 6 duplicate: covered by "Binding a name twice fails" -->
<!-- cases: N/A 7-9: as BindConfig -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Binding a name twice fails
`BindConfig` or `BindConfigNamed` SHALL fail with an error matching `ErrConfigAlreadyBound` naming the service, without panicking, when the target scope already holds a non-default registration under `T`'s name or the handle's name, keeping it; an `AsDefault` registration SHALL be replaced, and a parent scope's registration SHALL be shadowed.

#### Scenario: Duplicate bindings
```gherkin
Given root where <first> has already happened
When the consumer calls <second>
Then errors.Is(err, doger.ErrConfigAlreadyBound) is true
And the error message is "DI: config: \"<named>\" is already bound"
And neither <second>'s value nor handle is registered

Examples:
  | first                                                                      | second                                                | named                            |
  | doger.BindConfig[ServerConfig](root, "server")                             | doger.BindConfig[ServerConfig](root, "other")         | main.ServerConfig                |
  | doger.ProvideValue(root, ServerConfig{Port: 1})                            | doger.BindConfig[ServerConfig](root, "server")        | main.ServerConfig                |
  | doger.ProvideNamedValue(root, "a.value", 1)                                | doger.BindConfigNamed[ServerConfig](root, "a", "x")   | a.value                          |
  | doger.BindConfigNamed[ServerConfig](root, "a", "x")                        | doger.BindConfigNamed[ServerConfig](root, "a", "y")   | a                                |
```

#### Scenario: Defaults and parent registrations give way
```gherkin
Given <existing>
And root's property source holds "server.port" = "9090"
When the consumer calls doger.BindConfig[ServerConfig](<scope>, "server")
Then the error is nil
And doger.MustInvoke[ServerConfig](<scope>).Port is 9090

Examples:
  | existing                                                                 | scope |
  | doger.ProvideValue(root, ServerConfig{Port: 1}, doger.AsDefault())       | root  |
  | doger.ProvideValue(root, ServerConfig{Port: 1}) and child := root.Scope("c") | child |
```

<!-- cases: N/A 1 default, 2 overrides: error path and replacement rules -->
<!-- cases: N/A 3 zero values: empty name has its own requirement -->
<!-- cases: 4 boundaries: T's name taken, handle's name taken, same name different prefix -->
<!-- cases: 5 errors: sentinel matchable with errors.Is; text pinned -->
<!-- cases: 6 duplicate: the subject; AsDefault replaced, parent shadowed -->
<!-- cases: N/A 7 lifecycle: same rule in every phase that allows binding -->
<!-- cases: 8 concurrency: covered by "Concurrent binding and reads" -->
<!-- cases: 9 panics: binding never panics on a duplicate, unlike Provide -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Binding follows the container's phase
`BindConfig`, `BindConfigNamed` and `ResolveConfig` SHALL work before `Start` and after it, and SHALL fail with an error matching `ErrStartAfterShutdown`, registering nothing, once the container has begun shutting down.

#### Scenario: Binding in each phase
```gherkin
Given root's property source holds "server.port" = "9090"
And root is <phase>
When the consumer calls doger.BindConfig[ServerConfig](root, "server")
Then the error is <error>

Examples:
  | phase                           | error                                 |
  | idle (never started)            | nil                                   |
  | started with root.Start()       | nil                                   |
  | shut down with root.Shutdown()  | an error matching ErrStartAfterShutdown |
```

#### Scenario: Resolving after shutdown
```gherkin
Given root has been shut down
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{Port: 1})
Then the result is ServerConfig{} and errors.Is(err, doger.ErrStartAfterShutdown) is true
```

<!-- cases: N/A 1 default, 2 overrides -->
<!-- cases: N/A 3 zero values -->
<!-- cases: 4 boundaries: before start, after start, after shutdown -->
<!-- cases: 5 errors: ErrStartAfterShutdown -->
<!-- cases: N/A 6 duplicate -->
<!-- cases: 7 lifecycle: the subject -->
<!-- cases: N/A 8-9 -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A bind is a property read
A bind SHALL read the container's property source as decided in `extension-properties`, and SHALL count as the container's first property read when nothing has read a property yet.

#### Scenario: A PropertySource service registered after a bind
```gherkin
Given root := doger.New() and doger.ResolveConfig(root, "server", ServerConfig{}) has returned
When the consumer registers a lazy service under the name doger.NameOf[doger.PropertySource]()
Then the registration panics with "DI: service `github.com/kartaladev/doger.PropertySource` has already been declared"
```

<!-- cases: N/A 1-6: the decision rules are in extension-properties -->
<!-- cases: 7 lifecycle: the bind decides the source -->
<!-- cases: N/A 8 concurrency: as in extension-properties -->
<!-- cases: 9 panics: the late registration panics as a duplicate -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: ResolveConfig binds onto the consumer's defaults
`ResolveConfig[T](i, prefix, defaults)` SHALL bind `prefix` from `i`'s container over a copy of `defaults`, validate the result and return it, registering nothing; on error it SHALL return the zero `T` and the error. A nil `i` SHALL mean `DefaultRootScope`.

#### Scenario: Two consumers resolve one prefix with different defaults
```gherkin
Given root's property source holds "payments.client.retries" = "5"
And doger.ResolveConfig(root, "payments.client", ClientConfig{Timeout: 3 * time.Second, Retries: 2}) returned ClientConfig{Timeout: 3s, Retries: 5}
When the consumer calls doger.ResolveConfig(root, "payments.client", ClientConfig{Timeout: 10 * time.Second})
Then the result is ClientConfig{Timeout: 10s, Retries: 5}
And doger.Invoke[ClientConfig](root) returns an error matching doger.ErrServiceNotFound
```

#### Scenario: A failed resolve returns the zero value
```gherkin
Given root's property source holds "payments.client.retries" = "many"
When the consumer calls doger.ResolveConfig(root, "payments.client", ClientConfig{Retries: 2})
Then the result is ClientConfig{} and errors.Is(err, doger.ErrInvalidConfigValue) is true
```

#### Scenario: Resolving inside a provider uses the provider's container
```gherkin
Given root created with PropertySource doger.MapSource{"payments.client.retries": "5"}
And a lazy "*Client" whose provider calls doger.ResolveConfig(i, "payments.client", ClientConfig{Retries: 2})
And clone, err := root.CloneWithOpts(&doger.InjectorOpts{PropertySource: doger.MapSource{"payments.client.retries": "7"}})
When the consumer invokes "*Client" from <container>
Then the client's Retries is <retries>

Examples:
  | container | retries |
  | root      | 5       |
  | clone     | 7       |
```

<!-- cases: 1 default: zero-value defaults behave as ResolveConfig(i, p, T{}) -->
<!-- cases: 2 overrides: the defaults argument -->
<!-- cases: 3 zero values: nil injector means DefaultRootScope, as for BindConfig -->
<!-- cases: 4 boundaries: original and cloned container -->
<!-- cases: 5 errors: zero value plus error -->
<!-- cases: 6 duplicate: resolving twice registers nothing, so it never conflicts -->
<!-- cases: 7 lifecycle: clone row; shutdown in "Binding follows the container's phase" -->
<!-- cases: 8 concurrency: covered by "Concurrent binding and reads" -->
<!-- cases: N/A 9 panics: binder and validator panics have their own requirements -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A config handle shares its current value
`ConfigValue[T]` SHALL be a handle whose copies share one current value; `Get` SHALL return it, `StaticConfig(v)` SHALL return a handle whose `Get` always returns `v`, and the zero handle's `Get` SHALL return the zero `T`.

#### Scenario: Handle values
```gherkin
Given <handle>
When the consumer calls Get()
Then the result is <value>

Examples:
  | handle                                                                                      | value                         |
  | doger.MustInvoke[doger.ConfigValue[LimitsConfig]](root) after binding "limits.per-minute" = "10" | LimitsConfig{PerMinute: 10} |
  | a copy h2 := h of that handle                                                               | LimitsConfig{PerMinute: 10}   |
  | doger.StaticConfig(LimitsConfig{PerMinute: 3})                                              | LimitsConfig{PerMinute: 3}    |
  | var h doger.ConfigValue[LimitsConfig]                                                       | LimitsConfig{}                |
```

#### Scenario: OnChange on a handle that never changes
```gherkin
Given <handle> and stop := handle.OnChange(fn)
When the consumer calls stop() twice
Then neither call panics and fn was never called

Examples:
  | handle                                          |
  | doger.StaticConfig(LimitsConfig{PerMinute: 3})  |
  | var h doger.ConfigValue[LimitsConfig]           |
```

<!-- cases: 1 default: bound handle row -->
<!-- cases: 2 overrides: StaticConfig for tests -->
<!-- cases: 3 zero values: zero handle rows -->
<!-- cases: 4 boundaries: original and copy -->
<!-- cases: N/A 5 errors: Get cannot fail -->
<!-- cases: N/A 6 duplicate -->
<!-- cases: 7 lifecycle: reload updates and notifications are specified in config-reload -->
<!-- cases: 8 concurrency: covered by "Concurrent binding and reads" -->
<!-- cases: 9 panics: Get, OnChange and stop never panic, also on a zero handle; stop is idempotent -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: BindProperties binds an extension's view
`BindProperties(props, target)` SHALL bind into the struct `target` points to from a `Properties` view, with keys relative to the view, keeping the struct's current values as defaults and leaving `*target` unchanged on any error; it SHALL NOT validate. A target that is not a non-nil pointer to a struct SHALL fail with `ErrInvalidConfigTarget`.

#### Scenario: An extension binds over its defaults
```gherkin
Given root's property source holds "redis.addr" = "cache:6379" and "redis/queue.pool-size" = "4"
And an extension "redis" with instance key "<instance>" whose Extend runs cfg := Config{Addr: "localhost:6379", PoolSize: 10} and records doger.BindProperties(x.Properties(), &cfg)
When the consumer calls doger.Extend(root, redisExt)
Then the recorded error is nil and cfg is <cfg>

Examples:
  | instance | cfg                                         |
  |          | Config{Addr: "cache:6379", PoolSize: 10}    |
  | queue    | Config{Addr: "localhost:6379", PoolSize: 4} |
```

#### Scenario: A failed bind leaves the target unchanged
```gherkin
Given root's property source holds "redis.addr" = "cache:6379" and "redis.pool-size" = "lots"
And an extension "redis" whose Extend runs cfg := Config{Addr: "localhost:6379", PoolSize: 10} and records doger.BindProperties(x.Properties(), &cfg)
When the consumer calls doger.Extend(root, redisExt)
Then the recorded error matches doger.ErrInvalidConfigValue
And cfg is still Config{Addr: "localhost:6379", PoolSize: 10}
```

#### Scenario: Invalid targets
```gherkin
Given an extension whose Extend records doger.BindProperties(x.Properties(), <target>)
When the consumer calls doger.Extend(root, ext)
Then the recorded error matches doger.ErrInvalidConfigTarget

Examples:
  | target             |
  | Config{}           |
  | (*Config)(nil)     |
  | nil                |
  | &someInt           |
```

<!-- cases: 1 default: no instance row -->
<!-- cases: 2 overrides: instance row -->
<!-- cases: 3 zero values: nil and typed-nil rows -->
<!-- cases: 4 boundaries: with and without an instance key -->
<!-- cases: 5 errors: invalid target; conversion error with the target unchanged -->
<!-- cases: N/A 6 duplicate: nothing is registered -->
<!-- cases: N/A 7 lifecycle: as for Properties() -->
<!-- cases: N/A 8 concurrency: as in extension-properties "Concurrent lookups" -->
<!-- cases: 9 panics: as in "A panic while binding is an error" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A map source is a property source
`MapSource` SHALL be a `map[string]string` type implementing `PropertySource` and `PropertyKeys` over its own entries without copying them, so a literal SHALL serve as a property source in tests and small programs.

#### Scenario: Binding from a map literal
```gherkin
Given root := doger.MustNewWithOpts(&doger.InjectorOpts{PropertySource: doger.MapSource{"server.port": "9090", "server.hosts[0]": "a"}})
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then the result is ServerConfig{Port: 9090, Hosts: ["a"]}
```

<!-- cases: 1 default: the subject -->
<!-- cases: N/A 2 overrides -->
<!-- cases: N/A 3 zero values: a nil MapSource holds nothing; Keys semantics are in extension-properties -->
<!-- cases: N/A 4-9: as for every source; mutating the map while in use is documented as unsupported -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Concurrent binding and reads
Concurrent calls to `BindConfig`, `BindConfigNamed`, `ResolveConfig` and `ConfigValue[T].Get` on one container SHALL be race-free; of concurrent bindings of one service name, exactly one SHALL succeed and the others SHALL fail with `ErrConfigAlreadyBound`, leaving no half registration.

#### Scenario: Concurrent use under -race
```gherkin
Given root's property source holds "server.port" = "9090", "limits.per-minute" = "10" and "jobs.workers" = "4"
And doger.BindConfig[LimitsConfig](root, "limits") returned nil
When 60 goroutines start together: 50 calling doger.ResolveConfig(root, "server", ServerConfig{}) and the LimitsConfig handle's Get(), and 10 calling doger.BindConfig[JobsConfig](root, "jobs")
Then every ResolveConfig returns ServerConfig{Port: 9090} and every Get returns LimitsConfig{PerMinute: 10}
And exactly 1 BindConfig returns nil and 9 return an error matching doger.ErrConfigAlreadyBound
And both JobsConfig and its handle resolve, and the race detector reports nothing
```

<!-- cases: N/A 1-7: concurrency only -->
<!-- cases: 8 concurrency: the subject; runs under -race -->
<!-- cases: N/A 9 panics, 10 compatibility -->
