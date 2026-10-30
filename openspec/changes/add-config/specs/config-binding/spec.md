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
<!-- cases: N/A 3-9: each is covered by a later requirement of this spec (presence, errors, lifecycle, concurrency, panics) -->
<!-- cases: 10 compatibility: fixes spike 4 of golang-lib/config -->

### Requirement: Field keys are canonical kebab names
The binder SHALL split a Go field name before byte i when s[i] is upper and s[i-1] lower; when s[i] is upper, s[i-1] a digit and s[i+1] lower; or when s[i] is upper, s[i-1] upper, s[i+1] lower and the upper run before i is 2+ letters, unless s[i+1] is "s" ending the name or followed by a non-lower byte. `_` also splits; empty parts drop; letter-to-digit never splits; parts are lowercased and joined with `-`.

#### Scenario: The source receives canonical keys
```gherkin
Given root's property source is a Lookup-only source that records every key passed to Lookup and holds nothing
And a target struct whose only field is a string named <field>
When the consumer calls doger.ResolveConfig(root, "<prefix>", target)
Then the source received "<key>"

Examples:
  | field         | prefix | key                    |
  | Port          | server | server.port            |
  | ReadTimeout   | server | server.read-timeout    |
  | IssuerURI     | server | server.issuer-uri      |
  | TLSCert       | server | server.tls-cert        |
  | OAuth2Token   | server | server.oauth2-token    |
  | IPv6Addr      | server | server.ipv6-addr       |
  | AllowedURLs   | server | server.allowed-urls    |
  | IDs           | server | server.ids             |
  | S3Bucket      | server | server.s3-bucket       |
  | HTTPServer    | server | server.http-server     |
  | ID            | server | server.id              |
  | API_Key       | server | server.api-key         |
  | ABTest        | server | server.ab-test         |
  | Use2FA        | server | server.use2fa          |
  | HTTPServerURL | server | server.http-server-url |
  | ARN           | server | server.arn             |
  | PDFs          | server | server.pdfs            |
  | Base64Data    | server | server.base64-data     |
  | X509Cert      | server | server.x509-cert       |
  | HTTP2Server   | server | server.http2-server    |
  | SHA256Sum     | server | server.sha256-sum      |
  | UTF8String    | server | server.utf8-string     |
  | K8sNamespace  | server | server.k8s-namespace   |
  | Use2FAToken   | server | server.use2fa-token    |
  | XAxis         | server | server.xaxis           |
  | ATest         | server | server.atest           |
  | Foo__Bar      | server | server.foo-bar         |
  | Port          |        | port                   |
```

<!-- cases: 1 default: no tags needed -->
<!-- cases: 2 overrides: a tag replaces the derived segment, see "A tag overrides a field's key" -->
<!-- cases: 3 zero values: empty prefix row; double underscore drops the empty part -->
<!-- cases: 4 boundaries: acronym at start, middle and end; plural acronym; digit inside and after an acronym; one-letter upper run (XAxis, ATest) -->
<!-- cases: 5 errors: covered by "A non-ASCII field name needs a tag" -->
<!-- cases: N/A 6 duplicate: conflicts have their own requirement -->
<!-- cases: N/A 7-9: naming is a pure function of the field name -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A non-ASCII field name needs a tag
An exported, untagged, bound field whose name holds a non-ASCII letter SHALL fail the bind with an error matching `ErrInvalidConfigTarget` that names the struct type and the field, before any source is consulted; a tagged or skipped field with such a name SHALL bind or be skipped normally.

#### Scenario: Non-ASCII names
```gherkin
Given a doger.MapSource holding "shop.size" = "XL"
And type ShopConfig struct { <field> }
When the consumer calls doger.ResolveConfig(root, "shop", ShopConfig{})
Then the error is <error>

Examples:
  | field                         | error                                                                                   |
  | Größe string                  | matching ErrInvalidConfigTarget with message "DI: config: invalid target: app.ShopConfig.Größe: field name is not ASCII; add a cfg tag" |
  | Größe string `cfg:"size"`     | nil, and the result's Größe is "XL"                                                      |
  | Größe string `cfg:"-"`        | nil                                                                                     |
```

<!-- cases: N/A 1 default, 2 overrides: a tag is the override, tagged row -->
<!-- cases: N/A 3 zero values: an empty name cannot occur in Go -->
<!-- cases: 4 boundaries: untagged, tagged, skipped -->
<!-- cases: 5 errors: sentinel and pinned text -->
<!-- cases: N/A 6-9: checked before any lookup -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Keys are compared in uniform form
The binder SHALL compare keys in uniform form: per segment lowercase with `-` and `_` removed; `[k]` with k in `[a-z0-9]+` equal to `.k`; an all-digit `.n` equal to `[n]`; any other bracket content verbatim; the instance segment after `/` never folded; and a prefix matching only whole segments, so `hosts` never matches `hostsx`.

#### Scenario: Multi-word lists, maps and pointers bind from env
```gherkin
Given no InjectorOpts.ConfigSource or PropertySource
And the environment holds "DOGER_SERVER_ALLOWEDHOSTS_0" = "a", "DOGER_SERVER_ALLOWEDHOSTS_1" = "b", "DOGER_SERVER_EXTRALABELS_TEAM" = "core" and "DOGER_SERVER_CLIENTTLS_ENABLED" = "true"
And type ServerConfig struct { AllowedHosts []string; ExtraLabels map[string]string; ClientTLS *TLSConfig }
When the consumer calls doger.ResolveConfig(doger.New(), "server", ServerConfig{})
Then the result is ServerConfig{AllowedHosts: ["a", "b"], ExtraLabels: {"team": "core"}, ClientTLS: &TLSConfig{Enabled: true}}
```

#### Scenario: Equal spellings in uniform form
```gherkin
Given a doger.MapSource holding <held>
And type ServerConfig struct { Hosts []string; Labels map[string]string }
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then the result is <result>

Examples:
  | held                                                       | result                                          |
  | "server.hosts.0" = "a", "server.hosts.1" = "b"             | ServerConfig{Hosts: ["a", "b"]}                 |
  | "server.labels[team]" = "core"                             | ServerConfig{Labels: {"team": "core"}}          |
  | "server.hostsx[0]" = "x", "server.hosts[0]" = "a"          | ServerConfig{Hosts: ["a"]}                      |
  | "server.hostsx[0]" = "x"                                   | ServerConfig{}                                  |
```

#### Scenario: An instance segment is never folded
```gherkin
Given a doger.MapSource holding "redis/0.addr" = "q:6379" and "redis[0].addr" = "wrong"
When the consumer calls doger.ResolveConfig(root, "redis/0", RedisConfig{})
Then the result's Addr is "q:6379"
```

<!-- cases: 1 default: env and map sources -->
<!-- cases: N/A 2 overrides: comparison is not configurable -->
<!-- cases: 3 zero values: no key under the prefix binds nothing (hostsx-only row) -->
<!-- cases: 4 boundaries: dotted versus bracketed index and map key; a longer segment sharing a prefix; an all-digit instance -->
<!-- cases: N/A 5 errors: comparison cannot fail -->
<!-- cases: 6 duplicate: equal uniform keys are resolved by "Keys are looked up as the source returns them" -->
<!-- cases: N/A 7-9: pure comparison -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Keys are looked up as the source returns them
When the source implements `PropertyKeys`, the binder SHALL find each field's keys with `Keys(<struct key>)` and `Lookup` each one exactly as returned; otherwise it SHALL `Lookup` the canonical key. When one source returns several keys with one uniform form, the binder SHALL use the one equal to the canonical spelling, otherwise the byte-wise smallest.

#### Scenario: A map source round-trips its own spelling
```gherkin
Given a doger.MapSource holding "server.allowedhosts[0]" = "a", "server.readtimeout" = "5s" and "server.ClientTLS.Enabled" = "true"
And type ServerConfig struct { AllowedHosts []string; ReadTimeout time.Duration; ClientTLS *TLSConfig }
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then the result is ServerConfig{AllowedHosts: ["a"], ReadTimeout: 5s, ClientTLS: &TLSConfig{Enabled: true}}
```

#### Scenario: Several spellings of one key in one source
```gherkin
Given a doger.MapSource holding <held>
And type AConfig struct { ReadTimeout time.Duration }
When the consumer calls doger.ResolveConfig(root, "a", AConfig{})
Then the result's ReadTimeout is <value>

Examples:
  | held                                                   | value |
  | "a.read-timeout" = "1s", "a.readtimeout" = "2s"        | 1s    |
  | "a.read_timeout" = "3s", "a.readtimeout" = "2s"        | 3s    |
  | "a.readTimeout" = "4s", "a.readtimeout" = "2s"         | 4s    |
```

<!-- cases: 1 default: MapSource in the canonical spelling is covered everywhere else -->
<!-- cases: N/A 2 overrides: a Lookup-only source is the opt-out, see "Lists, maps and pointers need a source that lists its keys" -->
<!-- cases: N/A 3 zero values: no key binds nothing -->
<!-- cases: 4 boundaries: canonical present; no canonical, byte-wise order decides ("_" 0x5F before "t"; "T" 0x54 before "t") -->
<!-- cases: N/A 5 errors: lookup cannot fail; conversion has its own requirement -->
<!-- cases: 6 duplicate: the subject; the choice never depends on map iteration order -->
<!-- cases: N/A 7-9: pure lookup -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A tag overrides a field's key
A struct tag under `InjectorOpts.ConfigTagKey` (default `cfg`) SHALL replace the field's derived key segment, `cfg:"-"` SHALL skip the field, an empty tag SHALL behave as no tag, and unexported fields SHALL be skipped; a tagged embedded struct SHALL bind as a named field under its tag.

#### Scenario: Tags rename or skip fields
```gherkin
Given a doger.MapSource holding "server.tls-cert-file" = "/etc/tls/cert.pem", "server.tls-cert" = "wrong", "server.secret" = "x", "server.plain" = "p" and "server.base.timeout" = "5s"
And type ServerConfig struct { TLSCert string `cfg:"tls-cert-file"`; Secret string `cfg:"-"`; Plain string `cfg:""`; hidden string; Common `cfg:"base"` } where Common is struct { Timeout time.Duration }
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then the result is ServerConfig{TLSCert: "/etc/tls/cert.pem", Plain: "p", Common: Common{Timeout: 5s}}
```

#### Scenario: The tag key can be changed
```gherkin
Given root := doger.MustNewWithOpts(&doger.InjectorOpts{PropertySource: doger.MapSource{"server.cert": "/c.pem", "server.wrong": "/w.pem"}, ConfigTagKey: "<tagKey>"})
And type ServerConfig struct { TLSCert string `cfg:"wrong" conf:"cert"` }
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then the result's TLSCert is "<value>"

Examples:
  | tagKey | value  |
  |        | /w.pem |
  | cfg    | /w.pem |
  | conf   | /c.pem |
```

#### Scenario: Two containers bind one type with different tag keys
```gherkin
Given rootA created with ConfigTagKey "" and rootB with ConfigTagKey "conf", both with PropertySource doger.MapSource{"server.cert": "/c.pem", "server.wrong": "/w.pem"}
And type ServerConfig struct { TLSCert string `cfg:"wrong" conf:"cert"` }
And doger.ResolveConfig(<first>, "server", ServerConfig{}) has returned
When the consumer calls doger.ResolveConfig(<second>, "server", ServerConfig{})
Then the second result's TLSCert is "<value>"

Examples:
  | first | second | value  |
  | rootA | rootB  | /c.pem |
  | rootB | rootA  | /w.pem |
```

<!-- cases: 1 default: cfg tag key, empty ConfigTagKey row -->
<!-- cases: 2 overrides: rename, skip, tagged embedded struct, another tag key -->
<!-- cases: 3 zero values: cfg:"" and ConfigTagKey "" rows -->
<!-- cases: 4 boundaries: one type bound with two tag keys in either order in one process -->
<!-- cases: 5 errors: covered by "An invalid tag fails the bind" -->
<!-- cases: N/A 6 duplicate: one tag per field per key -->
<!-- cases: N/A 7 lifecycle: the tag key is fixed per container -->
<!-- cases: 8 concurrency: the per-type plan is cached by type and tag key, so concurrent containers never share a wrong plan -->
<!-- cases: N/A 9 panics: tag parsing never panics -->
<!-- cases: 10 compatibility: golang-lib/config cfg tags keep working when they hold kebab segments -->

### Requirement: An invalid tag fails the bind
A tag value that is not one valid key segment (`[a-z0-9]+(-[a-z0-9]+)*`), that is all digits, or that holds `,` SHALL fail the bind with an error matching `ErrInvalidConfigTarget` that names the struct type and the field, before any source is consulted.

#### Scenario: Invalid tags
```gherkin
Given type LogConfig struct { A string `cfg:"<tag>"` }
When the consumer calls doger.ResolveConfig(root, "log", LogConfig{})
Then errors.Is(err, doger.ErrInvalidConfigTarget) is true
And the error message is "DI: config: invalid target: app.LogConfig.A: tag \"<tag>\" is not a key segment"

Examples:
  | tag        |
  | TLSCert    |
  | tls.cert   |
  | Bad Name   |
  | file,omit  |
  | -file      |
  | 8080       |
```

#### Scenario: A foreign tag key with options
```gherkin
Given root created with ConfigTagKey "json"
And type LogConfig struct { File string `json:"file,omitempty"` }
When the consumer calls doger.ResolveConfig(root, "log", LogConfig{})
Then errors.Is(err, doger.ErrInvalidConfigTarget) is true
And the error message is "DI: config: invalid target: app.LogConfig.File: tag \"file,omitempty\" is not a key segment"
```

<!-- cases: N/A 1 default, 2 overrides: error path -->
<!-- cases: 3 zero values: an empty tag is no tag, see "A tag overrides a field's key" -->
<!-- cases: 4 boundaries: uppercase, dotted, space, comma, leading dash, all digits (which would fold into an index) -->
<!-- cases: 5 errors: sentinel matchable with errors.Is; text pinned -->
<!-- cases: N/A 6-9: checked before any lookup -->
<!-- cases: N/A 10 compatibility: new API; "," is reserved for future tag options -->

### Requirement: Two fields with one uniform key fail the bind
A struct with two bound fields whose keys are equal in uniform form, including a promoted field equal to an outer field or to another promoted field, SHALL fail the bind with an error matching `ErrInvalidConfigTarget` that names the struct type, both fields and the shared key, before any source is consulted.

#### Scenario: Conflicting names
```gherkin
Given type <type>
When the consumer calls doger.ResolveConfig(root, "log", LogConfig{})
Then errors.Is(err, doger.ErrInvalidConfigTarget) is true
And the error message is "<message>"

Examples:
  | type                                                                         | message                                                                                          |
  | LogConfig struct { LogFile string; Logfile string }                          | DI: config: invalid target: fields LogFile and Logfile of app.LogConfig share the key "logfile"   |
  | LogConfig struct { A string `cfg:"path"`; Path string }                     | DI: config: invalid target: fields A and Path of app.LogConfig share the key "path"               |
  | LogConfig struct { Common; Path string } with Common { Path string }         | DI: config: invalid target: fields Common.Path and Path of app.LogConfig share the key "path"     |
  | LogConfig struct { A; B } with A { Path string } and B { Path string }       | DI: config: invalid target: fields A.Path and B.Path of app.LogConfig share the key "path"        |
```

<!-- cases: N/A 1 default, 2 overrides: error path only -->
<!-- cases: N/A 3 zero values: a struct with no fields has nothing to conflict -->
<!-- cases: 4 boundaries: derived versus tagged versus promoted names; two embedded structs at one depth -->
<!-- cases: 5 errors: sentinel matchable with errors.Is; text pinned -->
<!-- cases: 6 duplicate: the subject; unlike encoding/json, a shallower field does not shadow, because a silent shadow would bind the wrong field -->
<!-- cases: N/A 7-9: checked before any lookup -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Unsupported field types are rejected up front
A bound field whose type is a channel, function, complex number, `uintptr`, `unsafe.Pointer`, array, interface, or a map with a non-string key kind, and which the scalar rules do not convert, SHALL fail the bind with an error matching `ErrInvalidConfigTarget` naming the struct type, the field and its type, before any source is consulted.

#### Scenario: Unsupported kinds
```gherkin
Given a doger.MapSource holding nothing
And type ServerConfig struct { F <type> }
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then errors.Is(err, doger.ErrInvalidConfigTarget) is true
And the error message is "DI: config: invalid target: app.ServerConfig.F: type <type> cannot be bound"

Examples:
  | type             |
  | chan int         |
  | func()           |
  | complex128       |
  | uintptr          |
  | [2]string        |
  | any              |
  | map[int]string   |
```

<!-- cases: N/A 1 default, 2 overrides: error path; skip the field with cfg:"-" or give the type an UnmarshalText -->
<!-- cases: 3 zero values: rejected even when no key exists, so a typo in a type never waits for a key to appear -->
<!-- cases: 4 boundaries: every unsupported kind once -->
<!-- cases: 5 errors: sentinel and pinned text -->
<!-- cases: N/A 6-9: checked before any lookup -->
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
<!-- cases: 4 boundaries: absent versus present-empty; unknown key ignored -->
<!-- cases: N/A 5 errors: covered by the conversion requirements -->
<!-- cases: N/A 6-9: pure binding -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Nested and embedded structs bind field by field
A struct-typed field SHALL bind recursively under its own key, keeping sibling defaults. An untagged embedded struct, exported or not, SHALL bind its promoted exported fields under the parent's key. An untagged exported embedded pointer SHALL do the same, allocated only when one of its promoted keys exists; an unexported embedded pointer SHALL fail with `ErrInvalidConfigTarget`.

#### Scenario: One nested key keeps its siblings' defaults
```gherkin
Given a doger.MapSource holding "redis.tls.enabled" = "true"
And defaults RedisConfig{Addr: "localhost:6379", TLS: TLSConfig{Enabled: false, MinVersion: "1.2"}}
When the consumer calls doger.ResolveConfig(root, "redis", defaults)
Then the result is RedisConfig{Addr: "localhost:6379", TLS: TLSConfig{Enabled: true, MinVersion: "1.2"}}
```

#### Scenario: Embedded structs bind under the parent's key
```gherkin
Given a doger.MapSource holding <held>
And type ServerConfig struct { <embedded>; Port int } where Common and common are struct { Timeout time.Duration }
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then the result's embedded value is <value> and the error is <error>

Examples:
  | held                       | embedded | value                       | error                                                                                       |
  | "server.timeout" = "5s"    | Common   | Common{Timeout: 5s}         | nil                                                                                         |
  | "server.timeout" = "5s"    | common   | common{Timeout: 5s}         | nil                                                                                         |
  | "server.timeout" = "5s"    | *Common  | &Common{Timeout: 5s}        | nil                                                                                         |
  | nothing                    | *Common  | nil                         | nil                                                                                         |
  | nothing                    | *common  | none                        | matching ErrInvalidConfigTarget with message "DI: config: invalid target: app.ServerConfig.common: unexported embedded pointer cannot be set" |
```

<!-- cases: 1 default: untouched nested defaults -->
<!-- cases: 2 overrides: a tag makes an embedded struct a named field, see "A tag overrides a field's key" -->
<!-- cases: 3 zero values: an embedded nil pointer stays nil without keys -->
<!-- cases: 4 boundaries: exported and unexported, value and pointer embedding -->
<!-- cases: 5 errors: unexported embedded pointer, which reflection cannot set -->
<!-- cases: N/A 6 duplicate: promoted-name conflicts are "Two fields with one uniform key fail the bind" -->
<!-- cases: N/A 7-9: pure binding -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Recursive types are rejected
A cycle among the types of bound fields, through any struct, pointer, slice or map element and at any depth below the target, SHALL fail the bind with an error matching `ErrInvalidConfigTarget` naming the recursive type and the field path, before any source is consulted; skipped and unexported fields and types converted as scalars SHALL NOT count.

#### Scenario: Recursive targets
```gherkin
Given type <types>
When the consumer calls doger.ResolveConfig(root, "x", <target>)
Then errors.Is(err, doger.ErrInvalidConfigTarget) is true
And the error message is "DI: config: invalid target: recursive type app.Node at <path>"

Examples:
  | types                                                         | target          | path          |
  | Node struct { Next *Node }                                    | Node{}          | Next          |
  | Node struct { Kids []Node }                                   | Node{}          | Kids          |
  | Node struct { ByName map[string]*Node }                       | Node{}          | ByName        |
  | Node struct { Inner struct { Back *Node } }                   | Node{}          | Inner.Back    |
  | Node struct { Next *Node } and ServerConfig struct { Tree *Node } | ServerConfig{} | Tree.Next   |
```

#### Scenario: Back-pointers that are not bound
```gherkin
Given type <types>
When the consumer calls doger.ResolveConfig(root, "x", Node{})
Then the error is nil

Examples:
  | types                                                                     |
  | Node struct { Name string; parent *Node }                                 |
  | Node struct { Name string; Parent *Node `cfg:"-"` }                       |
  | Node struct { Name string; Next *Tree } where *Tree implements encoding.TextUnmarshaler and Tree struct { Next *Tree } |
```

<!-- cases: N/A 1 default, 2 overrides: skipping or a scalar type are the ways out, second scenario -->
<!-- cases: N/A 3 zero values: an empty struct has no cycle -->
<!-- cases: 4 boundaries: direct, through slice, map, anonymous struct, and a recursive type nested below a non-recursive target -->
<!-- cases: 5 errors: sentinel and pinned text; never a stack overflow -->
<!-- cases: N/A 6-8: checked before any lookup -->
<!-- cases: 9 panics: an unbounded recursion would be a fatal error that cannot be recovered, so it is rejected up front -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Lists come whole from indexed keys
A slice field SHALL bind from indexed keys `<key>[i]`, needing at least one key for every index 0 to the highest after uniform matching, and replace the default slice; each element SHALL start from its zero value. A gap, a non-numeric or non-canonical index, or an index above 10000 SHALL fail with `*ConfigBindError` matching `ErrConfigConversion` before any allocation.

#### Scenario: Indexed list sources
```gherkin
Given a doger.MapSource holding <held>
And defaults ServerConfig{Hosts: []string{"d1", "d2", "d3"}}
When the consumer calls doger.ResolveConfig(root, "server", defaults)
Then the result's Hosts is <hosts> and the error is <error>

Examples:
  | held                                                 | hosts               | error                                                                               |
  | nothing                                              | ["d1", "d2", "d3"]  | nil                                                                                 |
  | "server.hosts[0]" = "a"                              | ["a"]               | nil                                                                                 |
  | "server.hosts[0]" = "a", "server.hosts[1]" = "b"     | ["a", "b"]          | nil                                                                                 |
  | "server.hosts[10000]" = "a" and indices 0 to 9999    | 10001 elements      | nil                                                                                 |
  | "server.hosts[0]" = "a", "server.hosts[2]" = "c"     | none                | "DI: config: invalid value for server.hosts ([]string): missing index 1"            |
  | "server.hosts[x]" = "a"                              | none                | "DI: config: invalid value for server.hosts ([]string): index \"x\" is not a number" |
  | "server.hosts[01]" = "a"                             | none                | "DI: config: invalid value for server.hosts ([]string): index \"01\" is not canonical" |
  | "server.hosts[10001]" = "a"                          | none                | "DI: config: invalid value for server.hosts ([]string): index 10001 is over 10000"   |
```

#### Scenario: A list of structs starts each element from zero
```gherkin
Given a doger.MapSource holding "app.upstreams[0].name" = "a", "app.upstreams[0].port" = "80" and "app.upstreams[1].name" = "b"
And defaults AppConfig{Upstreams: []Upstream{{Name: "d", Port: 9}}}
When the consumer calls doger.ResolveConfig(root, "app", defaults)
Then the result's Upstreams is [{Name: "a", Port: 80}, {Name: "b", Port: 0}]
```

#### Scenario: A list of lists
```gherkin
Given a doger.MapSource holding "app.matrix[0][0]" = "1", "app.matrix[0][1]" = "2" and "app.matrix[1][0]" = "3"
And type AppConfig struct { Matrix [][]int }
When the consumer calls doger.ResolveConfig(root, "app", AppConfig{})
Then the result's Matrix is [[1, 2], [3]]
```

<!-- cases: 1 default: nothing row keeps the default list -->
<!-- cases: 2 overrides: a comma value, see "A single value is split on commas"; a TextUnmarshaler slice type -->
<!-- cases: N/A 3 zero values: an empty list comes from the comma rule -->
<!-- cases: 4 boundaries: 0, 1, many elements; index at and one past 10000; leading zero; nested list -->
<!-- cases: 5 errors: gap, non-numeric, non-canonical and too-large rows; errors.As gives Key "server.hosts" and Type []string -->
<!-- cases: 6 duplicate: several keys for one index (upstreams[0].name and [0].port, or two spellings) count as one index -->
<!-- cases: N/A 7-8: pure binding -->
<!-- cases: 9 panics: a huge index never allocates -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A single value is split on commas
A slice field with no indexed keys but a value at `<key>`, from any layer, SHALL bind by splitting on `,` and trimming U+0020 spaces around each element, keeping empty elements; an empty value SHALL give an empty, non-nil slice; indexed keys SHALL win over a value in one source. A named slice type implementing `encoding.TextUnmarshaler` SHALL bind as a scalar.

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
  | "server.hosts" = "\ta"                            | ServerConfig{Hosts: ["\ta"]}                |
  | "server.hosts" = ""                               | ServerConfig{Hosts: []}                     |
  | "server.hosts" = "x,y", "server.hosts[0]" = "a"   | ServerConfig{Hosts: ["a"]}                  |
  | "server.peers" = "p1,x;p2"                        | ServerConfig{Peers: ["p1,x", "p2"]}         |
```

<!-- cases: 1 default: plain comma row -->
<!-- cases: 2 overrides: a TextUnmarshaler slice type opts out of comma splitting; it is the documented escape for values holding commas -->
<!-- cases: 3 zero values: empty value, empty elements -->
<!-- cases: 4 boundaries: leading, inner and trailing empty elements; only U+0020 is trimmed, a tab is kept -->
<!-- cases: N/A 5 errors: element conversion errors follow the scalar rules -->
<!-- cases: 6 duplicate: indexed keys win over a value in one source; across layers, a value in a higher layer masks lower indices (config-files) -->
<!-- cases: N/A 7-9: pure binding -->
<!-- cases: 10 compatibility: values from YAML or TOML are split too, so "hosts: a,b" in a file is a two-element list, as documented -->

### Requirement: Maps merge by key
A map field with a `string`-kinded key SHALL keep its default entries and set one entry per name found under the field's key, nested or bracketed; a name in brackets SHALL be kept verbatim; a map of structs SHALL merge each entry field by field; a value exactly at the map's key and keys deeper than the value type SHALL be ignored; a nil default map SHALL stay nil when no entry exists.

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
  | "server.labels.team" = "a", "server.labels[team]" = "b"                | nil                                  | {"team": "a"}                                        |
  | "server.labels.404" = "nf"                                             | nil                                  | {"404": "nf"}                                        |
  | "server.labels.team.sub" = "x"                                         | nil                                  | nil                                                  |
  | "server.labels" = "x"                                                  | nil                                  | nil                                                  |
```

#### Scenario: A map of structs merges entries field by field
```gherkin
Given a doger.MapSource holding "db.replicas.east.addr" = "e:5432" and "db.replicas.west.addr" = "w:5432"
And defaults DBConfig{Replicas: map[string]Replica{"east": {Addr: "x", Port: 1}}}
When the consumer calls doger.ResolveConfig(root, "db", defaults)
Then the result's Replicas is {"east": {Addr: "e:5432", Port: 1}, "west": {Addr: "w:5432", Port: 0}}
```

#### Scenario: A map of lists
```gherkin
Given a doger.MapSource holding "acl.groups.admin[0]" = "ann", "acl.groups.admin[1]" = "bob" and "acl.groups.ops" = "carl,dee"
And type ACLConfig struct { Groups map[string][]string }
When the consumer calls doger.ResolveConfig(root, "acl", ACLConfig{})
Then the result's Groups is {"admin": ["ann", "bob"], "ops": ["carl", "dee"]}
```

<!-- cases: 1 default: default entries kept -->
<!-- cases: 2 overrides: a source entry replaces a scalar default entry; struct entries merge -->
<!-- cases: 3 zero values: nil default map stays nil, or is allocated on the first entry -->
<!-- cases: 4 boundaries: nested versus bracketed names; all-digit names; keys deeper than the value type; a value at the map key itself -->
<!-- cases: N/A 5 errors: non-string key kinds are rejected up front, see "Unsupported field types are rejected up front" -->
<!-- cases: 6 duplicate: the same name nested and bracketed is one key in uniform form; the canonical (nested) spelling wins, per "Keys are looked up as the source returns them" -->
<!-- cases: N/A 7-9: pure binding -->
<!-- cases: 10 compatibility: entry names from env arrive lowercase and dash-free (team-a from a file, teama from env), as in Spring; documented -->

### Requirement: Pointer fields are copied only on write
A pointer field SHALL keep the default's pointer itself when no key exists at or under its key. Otherwise the result SHALL hold a new pointee: a copy of the default pointee overlaid with the bound keys, or a new zero value when the default is nil. The default pointee SHALL never be written. A pointer to a scalar SHALL be allocated when its key exists.

#### Scenario: Pointer allocation
```gherkin
Given a doger.MapSource holding <held>
And defaults ServerConfig{TLS: <default>} where TLS is a *TLSConfig
When the consumer calls doger.ResolveConfig(root, "server", defaults)
Then the result's TLS is <tls>

Examples:
  | held                           | default                         | tls                                                                 |
  | nothing                        | nil                             | nil                                                                 |
  | "server.tls.enabled" = "true"  | nil                             | &TLSConfig{Enabled: true}                                           |
  | "server.tls.enabled" = "true"  | &TLSConfig{MinVersion: "1.2"}   | &TLSConfig{Enabled: true, MinVersion: "1.2"}, a different pointer   |
  | nothing                        | &TLSConfig{MinVersion: "1.2"}   | the default pointer itself, so result.TLS == defaults.TLS           |
```

#### Scenario: Pointers to scalars
```gherkin
Given a doger.MapSource holding <held>
And type ServerConfig struct { Debug *bool; Port *int }
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then the result is <result>

Examples:
  | held                       | result                                 |
  | nothing                    | ServerConfig{Debug: nil, Port: nil}    |
  | "server.debug" = "false"   | ServerConfig{Debug: &false, Port: nil} |
  | "server.port" = "0"        | ServerConfig{Debug: nil, Port: &0}     |
```

<!-- cases: 1 default: nil stays nil -->
<!-- cases: 2 overrides: a default pointee is copied and overlaid -->
<!-- cases: 3 zero values: nil default; a pointer to a present zero value -->
<!-- cases: 4 boundaries: no key versus one key under the pointer; pointer identity kept when untouched -->
<!-- cases: N/A 5 errors: conversion follows the scalar rules -->
<!-- cases: N/A 6-9: pure binding -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Lists, maps and pointers need a source that lists its keys
When the property source does not implement `PropertyKeys`, a bind whose target has a slice, map or pointer-to-struct field, excluding types the scalar rules convert, SHALL fail with an error matching `ErrInvalidConfigTarget` naming that field's key; scalar, pointer-to-scalar and nested struct fields SHALL bind through `Lookup` alone.

#### Scenario: A Lookup-only source
```gherkin
Given root := doger.MustNewWithOpts(&doger.InjectorOpts{PropertySource: lookupOnly}) where lookupOnly implements only Lookup and holds "server.port" = "9090", "server.ip" = "10.0.0.1", "server.base" = "https://kartala.id" and "server.peers" = "a;b"
And type ServerConfig struct { Port int; <field> }
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then the error is <error>

Examples:
  | field                 | error                                                                                                     |
  | IP net.IP             | nil, and IP is 10.0.0.1                                                                                   |
  | Base *url.URL         | nil, and Base's Host is "kartala.id"                                                                      |
  | Peers PeerList        | nil, and Peers is ["a", "b"]                                                                              |
  | Count *int            | nil, and Count is nil                                                                                     |
  | Hosts []string        | matching ErrInvalidConfigTarget with message "DI: config: invalid target: server.hosts needs a source that lists its keys" |
  | TLS *TLSConfig        | matching ErrInvalidConfigTarget with message "DI: config: invalid target: server.tls needs a source that lists its keys"   |
```

#### Scenario: BindProperties over a Lookup-only source
```gherkin
Given root created with PropertySource lookupOnly holding "redis.addr" = "c:6379"
And an extension "redis" whose Extend records doger.BindProperties(x.Properties(), &cfg) for cfg of type <type>
When the consumer calls doger.Extend(root, redisExt)
Then the recorded error is <error>

Examples:
  | type                                     | error                                       |
  | struct { Addr string }                   | nil, and cfg.Addr is "c:6379"               |
  | struct { Addr string; Hosts []string }   | matching ErrInvalidConfigTarget naming "hosts" |
```

<!-- cases: 1 default: scalar rows -->
<!-- cases: 2 overrides: implement PropertyKeys or wrap the values in doger.MapSource -->
<!-- cases: 3 zero values: a pointer to a scalar with no key stays nil -->
<!-- cases: 4 boundaries: scalar-converted slice and pointer types bind; plain list and pointer-to-struct fail -->
<!-- cases: 5 errors: sentinel and text; the same rule through BindProperties, so a list never comes back silently empty -->
<!-- cases: N/A 6-9: checked per bind -->
<!-- cases: 10 compatibility: PropertySource implementations without Keys keep working for scalars -->

### Requirement: Scalars are converted in a fixed order
The binder SHALL convert a raw value, untrimmed, by the first matching rule: exactly `time.Duration` via `time.ParseDuration`; `url.URL` or `*url.URL` via `url.Parse`; a type whose pointer implements `encoding.TextUnmarshaler`; `[]byte` as the raw bytes; by kind with `strconv` (integers base 10 at the field's bit size, floats `ParseFloat`, bools `ParseBool`); then the structural rules.

#### Scenario: Conversions
```gherkin
Given a doger.MapSource holding "x.v" = "<raw>"
And a target struct whose field V has type <type>
When the consumer calls doger.ResolveConfig(root, "x", target)
Then the result's V is <value> and the error is nil

Examples:
  | type                    | raw                   | value                                          |
  | string                  | " hello "             | " hello "                                      |
  | int                     | -42                   | -42                                            |
  | int8                    | 127                   | 127                                            |
  | uint16                  | 65535                 | 65535                                          |
  | float32                 | 1.5                   | 1.5                                            |
  | float64                 | NaN                   | math.NaN()                                     |
  | float64                 | Inf                   | math.Inf(1)                                    |
  | float64                 | 0x1p4                 | 16                                             |
  | bool                    | true                  | true                                           |
  | bool                    | 1                     | true                                           |
  | bool                    | F                     | false                                          |
  | time.Duration           | 1m30s                 | 90s                                            |
  | time.Duration           | 0                     | 0                                              |
  | Timeout (int64)         | 5                     | Timeout(5)                                     |
  | url.URL                 | https://kartala.id/a  | a url.URL with Host "kartala.id"               |
  | *url.URL                | https://kartala.id/a  | a *url.URL with Host "kartala.id"              |
  | net.IP                  | 10.0.0.1              | net.ParseIP("10.0.0.1")                        |
  | netip.Addr              | 10.0.0.1              | netip.MustParseAddr("10.0.0.1")                |
  | time.Time               | 2026-10-30T10:00:00Z  | an instant Equal to time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC) |
  | slog.Level              | warn                  | slog.LevelWarn                                 |
  | Port (uint16)           | 8080                  | Port(8080)                                     |
  | []byte                  | a,b                   | []byte("a,b")                                  |
```

<!-- cases: 1 default: the basic kinds -->
<!-- cases: 2 overrides: a TextUnmarshaler lets a type define its own parsing, before the kind and slice rules (net.IP, slog.Level) -->
<!-- cases: 3 zero values: "0" duration; whitespace is kept for strings -->
<!-- cases: 4 boundaries: int8 and uint16 max, bool spellings, float specials, a named type of int64 that is not time.Duration converts by kind -->
<!-- cases: 5 errors: covered by "Conversion failures name the key and type only" -->
<!-- cases: N/A 6-9: pure conversion -->
<!-- cases: 10 compatibility: the binder sees strings; file formats render scalars differently (YAML keeps 0x1F literally, TOML renders it 31), as specified in config-files, so one value may convert in one format and not another -->

### Requirement: Conversion failures name the key and type only
A value that does not convert SHALL fail the bind with a `*ConfigBindError` matching `ErrConfigConversion` whose `Key` is the full key and `Type` the target type, with message `DI: config: invalid value for <key> (<type>)` naming the type by `reflect.Type.String()`, never wrapping the parser's error.

#### Scenario: Conversion errors
```gherkin
Given a doger.MapSource holding "server.<key>" = "<raw>"
And type ServerConfig struct { Port int; Timeout time.Duration; Debug bool; Small int8; Count uint; Ratio float32 }
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then errors.As(err, &bindErr) is true with bindErr.Key "server.<key>" and bindErr.Type.String() "<type>"
And errors.Is(err, doger.ErrConfigConversion) is true
And the error message is "DI: config: invalid value for server.<key> (<type>)"
And fmt.Sprintf("%#v", err) does not contain "<raw>"

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
```

<!-- cases: N/A 1 default, 2 overrides: error path -->
<!-- cases: 3 zero values: an untrimmed space fails for numbers -->
<!-- cases: 4 boundaries: int8 one past max, negative uint, float32 overflow, bare duration, other integer bases -->
<!-- cases: 5 errors: the subject; errors.Is and errors.As; the message and %#v hold no value -->
<!-- cases: N/A 6-8: pure conversion -->
<!-- cases: 9 panics: covered by "A panic while binding is an error" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A panic while binding is an error
A panic raised during a bind by a type's `UnmarshalText` or by the source's `Lookup` or `Keys` SHALL be recovered and returned as a `*ConfigBindError` matching `ErrConfigConversion` that names the key being bound and the panic value's type only, never the panic value.

#### Scenario: Panics in user code
```gherkin
Given <panicking> panics with <value> while binding "server.level"
And type ServerConfig struct { Level Level } where *Level implements encoding.TextUnmarshaler
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then errors.Is(err, doger.ErrConfigConversion) is true
And the error message is "DI: config: invalid value for server.level (app.Level): panic of type <type>"

Examples:
  | panicking             | value              | type                    |
  | Level.UnmarshalText   | "bad s3cret"       | string                  |
  | the source's Lookup   | "bad s3cret"       | string                  |
  | the source's Keys     | "bad s3cret"       | string                  |
  | Level.UnmarshalText   | nil                | *runtime.PanicNilError  |
```

<!-- cases: N/A 1 default, 2 overrides: failure path -->
<!-- cases: 3 zero values: panic(nil) row -->
<!-- cases: N/A 4 boundaries: each panicking user hook once -->
<!-- cases: 5 errors: matchable sentinel; errors.As gives Key and Type; the value never appears -->
<!-- cases: N/A 6-8 -->
<!-- cases: 9 panics: the subject; these panics are returned as errors, not routed to HookContainerPanic, because the caller receives them synchronously -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: The caller's defaults are never changed
The binder SHALL never write through memory reachable from the defaults it receives, so the caller's struct and the slices, maps and pointees reachable from it SHALL be unchanged after any bind, successful or not.

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
<!-- cases: N/A 2 overrides: there is nothing to configure -->
<!-- cases: N/A 3 zero values: nil slices and maps have nothing to alias -->
<!-- cases: 4 boundaries: success and failure rows -->
<!-- cases: 5 errors: failure row -->
<!-- cases: N/A 6-9: pure binding -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: The type to bind must be a struct
`BindConfig`, `BindConfigNamed` and `ResolveConfig` SHALL require `T` to be a struct type that the scalar rules do not convert, and SHALL fail with an error matching `ErrInvalidConfigTarget` otherwise, registering nothing.

#### Scenario: Invalid type parameters
```gherkin
When the consumer calls <call>
Then errors.Is(err, doger.ErrInvalidConfigTarget) is true
And the error message is "DI: config: invalid target: <reason>"

Examples:
  | call                                                                 | reason                                   |
  | doger.BindConfig(root, "server", &ServerConfig{})                    | *app.ServerConfig is not a struct        |
  | doger.BindConfig(root, "server", 0)                                  | int is not a struct                      |
  | doger.BindConfig(root, "server", map[string]string{})                | map[string]string is not a struct        |
  | doger.BindConfig[io.Reader](root, "server", nil)                     | io.Reader is not a struct                |
  | doger.ResolveConfig(root, "server", (*ServerConfig)(nil))            | *app.ServerConfig is not a struct        |
  | doger.BindConfig(root, "server", time.Time{})                        | time.Time binds as a scalar              |
  | doger.ResolveConfig(root, "server", url.URL{})                       | url.URL binds as a scalar                |
```

<!-- cases: N/A 1 default, 2 overrides: error path -->
<!-- cases: 3 zero values: a typed-nil pointer row -->
<!-- cases: 4 boundaries: pointer, basic, map and interface types; struct types converted as scalars -->
<!-- cases: 5 errors: sentinel and pinned text -->
<!-- cases: N/A 6-9: checked before any lookup -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Prefixes follow the key grammar
A prefix SHALL be empty, or a name with an optional `/instance`, each followed by any number of `.segment`, `[index]` or `[key]` parts as in extension-properties; any other prefix SHALL fail with an error matching `ErrInvalidConfigTarget` naming the prefix, registering nothing.

#### Scenario: Valid and invalid prefixes
```gherkin
When the consumer calls doger.ResolveConfig(root, "<prefix>", ServerConfig{})
Then errors.Is(err, doger.ErrInvalidConfigTarget) is <invalid>

Examples:
  | prefix         | invalid |
  |                | false   |
  | server         | false   |
  | db.primary     | false   |
  | redis/queue    | false   |
  | redis/queue.tls| false   |
  | hosts[0]       | false   |
  | Server         | true    |
  | server.        | true    |
  | .server        | true    |
  | a..b           | true    |
  | redis/         | true    |
  | /queue         | true    |
```

<!-- cases: 1 default: empty prefix binds top-level keys -->
<!-- cases: N/A 2 overrides: the grammar is fixed -->
<!-- cases: 3 zero values: empty prefix row -->
<!-- cases: 4 boundaries: single segment, instance, index, trailing and leading separators -->
<!-- cases: 5 errors: sentinel -->
<!-- cases: N/A 6-9: checked before any lookup -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A typed-nil injector is rejected
A typed-nil injector passed to `BindConfig`, `BindConfigNamed` or `ResolveConfig` SHALL fail with an error matching `ErrInvalidConfigTarget`, while a nil injector SHALL mean `DefaultRootScope`.

#### Scenario: A typed-nil injector
```gherkin
Given var scope *doger.Scope
When the consumer calls doger.ResolveConfig(scope, "server", ServerConfig{})
Then errors.Is(err, doger.ErrInvalidConfigTarget) is true
And the error message is "DI: config: invalid target: nil *doger.Scope injector"
```

<!-- cases: N/A 1 default, 2 overrides: error path -->
<!-- cases: 3 zero values: typed nil here; untyped nil is "A nil injector binds into DefaultRootScope" -->
<!-- cases: N/A 4-9: checked before any lookup -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: BindConfig binds, validates and registers a section
`BindConfig[T](i, prefix, defaults)` SHALL bind `T` under `prefix` from `i`'s container over a copy of `defaults`, validate it (config-validation), and register `T` and `ConfigValue[T]` in `i`'s scope, both or neither. A nil `i` SHALL mean `DefaultRootScope`.

#### Scenario: A bound section is invocable
```gherkin
Given root := doger.MustNewWithOpts(&doger.InjectorOpts{PropertySource: doger.MapSource{"server.port": "9090"}})
When the consumer calls doger.BindConfig(root, "server", ServerConfig{Host: "0.0.0.0", Port: 8080})
Then the error is nil
And doger.MustInvoke[ServerConfig](root) is ServerConfig{Host: "0.0.0.0", Port: 9090}
And doger.MustInvoke[doger.ConfigValue[ServerConfig]](root).Get() is ServerConfig{Host: "0.0.0.0", Port: 9090}
```

#### Scenario: A failed bind registers nothing
```gherkin
Given root's property source holds "server.port" = "abc"
When the consumer calls doger.BindConfig(root, "server", ServerConfig{})
Then errors.Is(err, doger.ErrConfigConversion) is true
And doger.Invoke[ServerConfig](root) and doger.Invoke[doger.ConfigValue[ServerConfig]](root) both return errors matching doger.ErrServiceNotFound
```

#### Scenario: A failing second registration is rolled back
```gherkin
Given root with a HookAfterRegistration that panics with "hook boom" when the service name is doger.NameOf[doger.ConfigValue[ServerConfig]]()
And root's property source holds "server.port" = "9090"
When the consumer calls doger.BindConfig(root, "server", ServerConfig{})
Then the call panics with "hook boom"
And doger.Invoke[ServerConfig](root) and doger.Invoke[doger.ConfigValue[ServerConfig]](root) both return errors matching doger.ErrServiceNotFound
```

#### Scenario: A nil injector binds into DefaultRootScope
```gherkin
Given DefaultRootScope is swapped for a fresh root whose property source holds "nilinj.port" = "7070", restored at test cleanup, in a test that is not parallel
When the consumer calls doger.BindConfig(nil, "nilinj", NilInjConfig{})
Then doger.MustInvoke[NilInjConfig](doger.DefaultRootScope).Port is 7070
```

#### Scenario: A child scope binds from its container and registers locally
```gherkin
Given root's property source holds "jobs.workers" = "4"
And child := root.Scope("jobs")
When the consumer calls doger.BindConfig(child, "jobs", JobsConfig{})
Then doger.MustInvoke[JobsConfig](child).Workers is 4
And doger.Invoke[JobsConfig](root) returns an error matching doger.ErrServiceNotFound
```

<!-- cases: 1 default: zero-value defaults behave as BindConfig(i, p, T{}) -->
<!-- cases: 2 overrides: the defaults argument -->
<!-- cases: 3 zero values: nil injector row -->
<!-- cases: 4 boundaries: root and child scope -->
<!-- cases: 5 errors: conversion row registers nothing; validation failures are config-validation's rows -->
<!-- cases: 6 duplicate: covered by "Binding a name twice fails" -->
<!-- cases: 7 lifecycle: covered by "Only BindConfig follows the shutdown phase" -->
<!-- cases: 8 concurrency: covered by "Concurrent binding and reads"; a bind racing a reload is in config-reload -->
<!-- cases: 9 panics: a registration hook's panic propagates after the first registration is removed; binder and validator panics are errors -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: BindConfigNamed binds one type under several names
`BindConfigNamed[T](i, name, prefix, defaults)` SHALL behave as `BindConfig` but register `T` under `name` and `ConfigValue[T]` under `ConfigValueName(name)`, which SHALL be `name + ".value"`; an empty name SHALL fail with `ErrInvalidConfigTarget`, registering nothing.

#### Scenario: Two databases of one type
```gherkin
Given root's property source holds "db.primary.url" = "pg://p" and "db.replica.url" = "pg://r"
And doger.BindConfigNamed(root, "replica-db", "db.replica", DBConfig{}) returned nil
When the consumer calls doger.BindConfigNamed(root, "primary-db", "db.primary", DBConfig{})
Then doger.MustInvokeNamed[DBConfig](root, "primary-db").URL is "pg://p"
And doger.MustInvokeNamed[DBConfig](root, "replica-db").URL is "pg://r"
And doger.MustInvokeNamed[doger.ConfigValue[DBConfig]](root, doger.ConfigValueName("primary-db")).Get().URL is "pg://p"
```

#### Scenario: An empty name is rejected
```gherkin
When the consumer calls doger.BindConfigNamed(root, "", "server", ServerConfig{})
Then errors.Is(err, doger.ErrInvalidConfigTarget) is true
And the error message is "DI: config: invalid target: empty service name"
And doger.Invoke[doger.ConfigValue[ServerConfig]](root) returns an error matching doger.ErrServiceNotFound
```

<!-- cases: 1 default: as BindConfig -->
<!-- cases: N/A 2 overrides: as BindConfig -->
<!-- cases: 3 zero values: empty name -->
<!-- cases: 4 boundaries: two names, one type -->
<!-- cases: 5 errors: invalid target -->
<!-- cases: 6 duplicate: covered by "Binding a name twice fails" -->
<!-- cases: N/A 7-9: as BindConfig -->
<!-- cases: N/A 10 compatibility: new API; the name precedes the prefix, as godoc states, because both are strings -->

### Requirement: Binding a name twice fails
`BindConfig` or `BindConfigNamed` SHALL fail with an error matching `ErrConfigAlreadyBound` naming the service, without panicking and keeping the existing registration, when the target scope holds a registration under `T`'s or the handle's name that is not an unresolved `AsDefault` registration; an unresolved `AsDefault` one SHALL be replaced.

#### Scenario: Duplicate bindings
```gherkin
Given root where <first> has already happened
When the consumer calls <second>
Then errors.Is(err, doger.ErrConfigAlreadyBound) is true
And the error message is "DI: config: \"<named>\" is already bound"
And neither <second>'s value nor handle is registered

Examples:
  | first                                                                                 | second                                                      | named                                                     |
  | doger.BindConfig(root, "server", ServerConfig{})                                      | doger.BindConfig(root, "other", ServerConfig{})             | the value of doger.NameOf[ServerConfig]()                 |
  | doger.ProvideValue(root, ServerConfig{Port: 1})                                       | doger.BindConfig(root, "server", ServerConfig{})            | the value of doger.NameOf[ServerConfig]()                 |
  | doger.ProvideValue(root, ServerConfig{Port: 1}, doger.AsDefault()) and an Invoke of it | doger.BindConfig(root, "server", ServerConfig{})           | the value of doger.NameOf[ServerConfig]()                 |
  | doger.ProvideNamedValue(root, "a.value", 1)                                           | doger.BindConfigNamed(root, "a", "x", ServerConfig{})       | a.value                                                   |
  | doger.BindConfigNamed(root, "a", "x", ServerConfig{})                                 | doger.BindConfigNamed(root, "a", "y", ServerConfig{})       | a                                                         |
```

#### Scenario: Unresolved defaults and parent registrations give way
```gherkin
Given <existing>
And root's property source holds "server.port" = "9090"
When the consumer calls doger.BindConfig(<scope>, "server", ServerConfig{})
Then the error is nil
And doger.MustInvoke[ServerConfig](<scope>).Port is 9090

Examples:
  | existing                                                                       | scope |
  | doger.ProvideValue(root, ServerConfig{Port: 1}, doger.AsDefault()), never invoked | root  |
  | doger.ProvideValue(root, ServerConfig{Port: 1}) and child := root.Scope("c")   | child |
```

<!-- cases: N/A 1 default, 2 overrides: error path and replacement rules -->
<!-- cases: N/A 3 zero values: empty name has its own requirement -->
<!-- cases: 4 boundaries: T's name taken, handle's name taken, same name different prefix, resolved versus unresolved default -->
<!-- cases: 5 errors: sentinel matchable with errors.Is; text pinned with the service name from NameOf -->
<!-- cases: 6 duplicate: the subject; a resolved default is a duplicate, as in default-registrations; a parent registration is shadowed -->
<!-- cases: N/A 7 lifecycle: the same rule in every phase that allows binding -->
<!-- cases: 8 concurrency: covered by "Concurrent binding and reads" -->
<!-- cases: 9 panics: binding never panics on a duplicate, unlike Provide -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Only BindConfig follows the shutdown phase
`BindConfig` and `BindConfigNamed` SHALL fail with an error matching `ErrStartAfterShutdown`, registering nothing, once the target scope's container, or the child scope itself, has begun shutting down; `ResolveConfig` and `BindProperties` SHALL work in every phase, reading the last committed configuration.

#### Scenario: Binding in each phase
```gherkin
Given root's property source holds "server.port" = "9090"
And root is <phase>
When the consumer calls doger.BindConfig(<scope>, "server", ServerConfig{})
Then the error is <error>

Examples:
  | phase                                                     | scope            | error                                   |
  | idle (never started)                                      | root             | nil                                     |
  | starting, from a provider that runs during root.Start()   | root             | nil                                     |
  | started with root.Start()                                 | root             | nil                                     |
  | failed: root.Start() returned an error                    | root             | nil                                     |
  | closing, from a Shutdowner running during root.Shutdown() | root             | an error matching ErrStartAfterShutdown |
  | shut down with root.Shutdown()                            | root             | an error matching ErrStartAfterShutdown |
  | started, with child := root.Scope("c") shut down          | child            | an error matching ErrStartAfterShutdown |
```

#### Scenario: Resolving in every phase
```gherkin
Given root's property source holds "server.port" = "9090"
And root is <phase>
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{Port: 1})
Then the result is ServerConfig{Port: 9090} and the error is nil

Examples:
  | phase                                                     |
  | idle (never started)                                      |
  | closing, from a Shutdowner running during root.Shutdown() |
  | shut down with root.Shutdown()                            |
```

<!-- cases: N/A 1 default, 2 overrides: phases are not configurable -->
<!-- cases: N/A 3 zero values: no input beyond the phase -->
<!-- cases: 4 boundaries: every phase, and a child scope shut down on its own -->
<!-- cases: 5 errors: ErrStartAfterShutdown -->
<!-- cases: N/A 6 duplicate: as "Binding a name twice fails" -->
<!-- cases: 7 lifecycle: the subject; a service reading its drain timeout with ResolveConfig during shutdown works -->
<!-- cases: N/A 8-9: as for every bind -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A bind is a property read
A bind SHALL read the container's property source as decided in `extension-properties`, and SHALL count as the container's first property read when nothing has read a property yet.

#### Scenario: A PropertySource service registered after a bind
```gherkin
Given root := doger.New() and doger.ResolveConfig(root, "server", ServerConfig{}) has returned
When the consumer registers a lazy service under the name doger.NameOf[doger.PropertySource]()
Then the registration panics with "DI: service `github.com/kartaladev/doger.PropertySource` has already been declared"
```

<!-- cases: N/A 1-6: the decision rules, including a failing source provider leaving every property unset, are in extension-properties -->
<!-- cases: 7 lifecycle: the bind decides the source -->
<!-- cases: N/A 8 concurrency: as in extension-properties -->
<!-- cases: 9 panics: a panic while deciding the source reaches the caller, as in extension-properties; only Lookup, Keys and UnmarshalText panics during the bind itself are recovered -->
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
Then the result is ClientConfig{} and errors.Is(err, doger.ErrConfigConversion) is true
```

#### Scenario: Resolving inside a provider uses the provider's container
```gherkin
Given root created with PropertySource doger.MapSource{"payments.client.retries": "5"}
And a lazy "*Client" whose provider calls doger.ResolveConfig(i, "payments.client", ClientConfig{Retries: 2})
And clone, err := root.CloneWithOpts(&doger.InjectorOpts{PropertySource: doger.MapSource{"payments.client.retries": "7"}}) returned a nil err
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
<!-- cases: 7 lifecycle: clone row; phases in "Only BindConfig follows the shutdown phase" -->
<!-- cases: 8 concurrency: covered by "Concurrent binding and reads"; ResolveConfig reads one committed view and takes no lock -->
<!-- cases: N/A 9 panics: binder and validator panics have their own requirements -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A config handle shares its current value
`ConfigValue[T]` SHALL be a handle whose copies share one current value; `Get` SHALL return it, and callers SHALL treat it as read-only since its maps and slices are shared with other readers. `StaticConfig(v)` SHALL return a handle whose `Get` always returns `v`; the zero handle's `Get` SHALL return the zero `T`.

#### Scenario: Handle values
```gherkin
Given <handle>
When the consumer calls Get()
Then the result is <value>

Examples:
  | handle                                                                                            | value                         |
  | doger.MustInvoke[doger.ConfigValue[LimitsConfig]](root) after binding "limits.per-minute" = "10"  | LimitsConfig{PerMinute: 10}   |
  | a copy h2 := h of that handle                                                                     | LimitsConfig{PerMinute: 10}   |
  | doger.StaticConfig(LimitsConfig{PerMinute: 3})                                                    | LimitsConfig{PerMinute: 3}    |
  | var h doger.ConfigValue[LimitsConfig]                                                             | LimitsConfig{}                |
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
<!-- cases: N/A 6 duplicate: a handle is a value, not a registration -->
<!-- cases: 7 lifecycle: reload updates, notifications and stop semantics under reload are specified in config-reload -->
<!-- cases: 8 concurrency: covered by "Concurrent binding and reads" -->
<!-- cases: 9 panics: Get, OnChange and stop never panic, also on a zero handle; stop is idempotent -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: BindProperties binds an extension's view
`BindProperties(props, target)` SHALL bind into the struct `target` points to from a `Properties` view, with keys relative to the view and the view's container's `ConfigTagKey`, keeping the struct's current values as defaults and leaving `*target` unchanged on any error; it SHALL NOT validate. A target that is not a non-nil pointer to a struct SHALL fail with `ErrInvalidConfigTarget`.

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
Then the recorded error matches doger.ErrConfigConversion
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

#### Scenario: The zero view binds nothing
```gherkin
Given cfg := Config{Addr: "localhost:6379"}
When the consumer calls doger.BindProperties(doger.Properties{}, &cfg)
Then the error is nil and cfg is still Config{Addr: "localhost:6379"}
```

<!-- cases: 1 default: no instance row -->
<!-- cases: 2 overrides: instance row -->
<!-- cases: 3 zero values: nil and typed-nil targets; the zero Properties view -->
<!-- cases: 4 boundaries: with and without an instance key -->
<!-- cases: 5 errors: invalid target; conversion error with the target unchanged -->
<!-- cases: N/A 6 duplicate: nothing is registered -->
<!-- cases: 7 lifecycle: works in every phase, see "Only BindConfig follows the shutdown phase" -->
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
<!-- cases: N/A 2 overrides: a MapSource is itself the override for tests -->
<!-- cases: 3 zero values: a nil MapSource holds nothing; Keys semantics are in extension-properties -->
<!-- cases: N/A 4-9: as for every source; writing to the map while it is in use is a fatal runtime error, as documented in godoc -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Concurrent binding and reads
Concurrent calls to `BindConfig`, `BindConfigNamed`, `ResolveConfig` and `ConfigValue[T].Get` on one container SHALL be race-free; of concurrent bindings of one service name, exactly one SHALL succeed and the others SHALL fail with `ErrConfigAlreadyBound`, leaving no half registration.

#### Scenario: Concurrent use under -race
```gherkin
Given root's property source holds "server.port" = "9090", "limits.per-minute" = "10" and "jobs.workers" = "4"
And doger.BindConfig(root, "limits", LimitsConfig{}) returned nil
When 60 goroutines start together: 50 calling doger.ResolveConfig(root, "server", ServerConfig{}) and the LimitsConfig handle's Get(), and 10 calling doger.BindConfig(root, "jobs", JobsConfig{})
Then every ResolveConfig returns ServerConfig{Port: 9090} and every Get returns LimitsConfig{PerMinute: 10}
And exactly 1 BindConfig returns nil and 9 return an error matching doger.ErrConfigAlreadyBound
And both JobsConfig and its handle resolve, and the race detector reports nothing
```

<!-- cases: N/A 1-7: concurrency only; the other items are covered by the requirements above -->
<!-- cases: 8 concurrency: the subject; runs under -race; the per-type plan cache is shared safely -->
<!-- cases: N/A 9 panics: no input panics here -->
<!-- cases: N/A 10 compatibility: new API -->
