# Spec Delta

## Purpose

Validates configuration values bound with `BindConfig`, `BindConfigNamed`, `ResolveConfig` and `ValidateConfig` before any service uses them: `validate:` tags through go-playground by default in every container, a type's own `Validate() error`, one replaceable validator, and errors that list keys and rules but never values.

## ADDED Requirements

### Requirement: Tags are validated by default in every container
`BindConfig`, `BindConfigNamed` and `ResolveConfig` SHALL check the bound value's `validate:` struct tags with go-playground/validator when `InjectorOpts.ConfigValidator` is nil, whatever the property source, and a failure SHALL match `ErrInvalidConfig`; `BindProperties` SHALL NOT validate.

#### Scenario: Default tag validation
```gherkin
Given a root created with <opts>
And its property source holds "server.port" = "<port>"
And type ServerConfig struct { Port int `validate:"min=1,max=65535"` }
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{Port: 8080})
Then errors.Is(err, doger.ErrInvalidConfig) is <invalid>

Examples:
  | opts                                                                       | port  | invalid |
  | doger.New() with DOGER_SERVER_PORT set                                     | 9090  | false   |
  | doger.New() with DOGER_SERVER_PORT set                                     | 99999 | true    |
  | InjectorOpts{PropertySource: doger.MapSource{"server.port": port}}         | 99999 | true    |
  | InjectorOpts{ConfigSource: a test ConfigSource holding "server.port"}      | 0     | true    |
```

#### Scenario: Range boundaries
```gherkin
Given root's property source holds "server.port" = "<port>"
And type ServerConfig struct { Port int `validate:"min=1,max=65535"` }
When the consumer calls doger.BindConfig(root, "server", ServerConfig{})
Then errors.Is(err, doger.ErrInvalidConfig) is <invalid>

Examples:
  | port  | invalid |
  | 0     | true    |
  | 1     | false   |
  | 65535 | false   |
  | 65536 | true    |
```

#### Scenario: A failed validation registers nothing
```gherkin
Given root's property source holds "server.port" = "99999"
And type ServerConfig struct { Port int `validate:"min=1,max=65535"` }
When the consumer calls doger.BindConfig(root, "server", ServerConfig{})
Then errors.Is(err, doger.ErrInvalidConfig) is true
And doger.Invoke[ServerConfig](root) and doger.Invoke[doger.ConfigValue[ServerConfig]](root) both return errors matching doger.ErrServiceNotFound
```

<!-- cases: 1 default: doger.New() rows: no ConfigSource, go-playground still checks tags -->
<!-- cases: 2 overrides: covered by "One validator replaces go-playground" -->
<!-- cases: 3 zero values: a struct without tags passes; a nil InjectorOpts.ConfigValidator means go-playground -->
<!-- cases: 4 boundaries: min and max, at and one past the limit -->
<!-- cases: 5 errors: ErrInvalidConfig, nothing registered; shape in "Validation errors list keys and rules, never values" -->
<!-- cases: N/A 6 duplicate: validation has no registry -->
<!-- cases: N/A 7 lifecycle: validation runs at each bind and each reload rebind (config-reload) -->
<!-- cases: 8 concurrency: covered by "Concurrent validation" -->
<!-- cases: 9 panics: covered by "A panicking validator is an error" -->
<!-- cases: 10 compatibility: BindProperties leaves validation to ValidateConfig, after the extension applies its options -->

### Requirement: The default validator skips fields binding skips
The default go-playground validator SHALL NOT check a field that binding skips (unexported, or `-` under the container's `ConfigTagKey`) nor anything inside such a field; a replacement validator receives the whole struct and decides for itself.

#### Scenario: Skipped fields with failing tags
```gherkin
Given root created with ConfigTagKey "<tagKey>"
And type ServerConfig struct { Port int `validate:"min=1"`; <skipped> }
And root's property source holds "server.port" = "8080"
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then the error is nil

Examples:
  | tagKey | skipped                                                                          |
  |        | Internal string `cfg:"-" validate:"required"`                                   |
  |        | Inner InnerConfig `cfg:"-"` where InnerConfig has X string `validate:"required"` |
  |        | secret string `validate:"required"`                                             |
  | conf   | Internal string `conf:"-" validate:"required"`                                  |
```

<!-- cases: 1 default: cfg tag key rows -->
<!-- cases: 2 overrides: another ConfigTagKey row; a replacement validator is not bound by this rule -->
<!-- cases: 3 zero values: the skipped fields stay empty -->
<!-- cases: 4 boundaries: skipped scalar, skipped nested struct with inner tags, unexported field -->
<!-- cases: N/A 5 errors: a skipped field can never fail -->
<!-- cases: N/A 6-9: as for every validation -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A type's own Validate method also runs
When the bound top-level type, or a pointer to it, has a method `Validate() error`, including one promoted from an embedded type, it SHALL run after the validator and only when the validator passed; a non-nil result SHALL fail the bind matching both `ErrInvalidConfig` and the returned error. Validate methods of field types SHALL NOT be called.

#### Scenario: Validate method results
```gherkin
Given root's property source holds "server.port" = "<port>"
And ServerConfig has `validate:"min=1,max=65535"` on Port and a <receiver> receiver method Validate() that returns errReserved when Port == 22 and records each call
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then errors.Is(err, doger.ErrInvalidConfig) is <invalid>
And errors.Is(err, errReserved) is <reserved>
And Validate was called <calls> times

Examples:
  | receiver | port  | invalid | reserved | calls |
  | value    | 8080  | false   | false    | 1     |
  | value    | 22    | true    | true     | 1     |
  | pointer  | 22    | true    | true     | 1     |
  | value    | 99999 | true    | false    | 0     |
```

#### Scenario: Validate methods of fields and embedded types
```gherkin
Given type TLSConfig with a method Validate() that records each call
And type ServerConfig struct { <fields> }
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then TLSConfig.Validate was called <calls> times

Examples:
  | fields               | calls |
  | TLS TLSConfig        | 0     |
  | TLSConfig            | 1     |
```

<!-- cases: 1 default: value receiver row -->
<!-- cases: 2 overrides: Validate adds rules tags cannot express -->
<!-- cases: 3 zero values: a nil error passes; a typed-nil error returned as error is non-nil and fails -->
<!-- cases: 4 boundaries: failing tags skip Validate (calls 0); a named field's Validate is not called; a promoted Validate is the top-level type's own method -->
<!-- cases: 5 errors: wrapped and matchable as both sentinels; the text rule is "Validation failures from code show only value-free text" -->
<!-- cases: N/A 6 duplicate: one method per type -->
<!-- cases: N/A 7 lifecycle: runs at each bind -->
<!-- cases: N/A 8 concurrency: as for tags -->
<!-- cases: 9 panics: covered by "A panicking validator is an error" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Validation failures from code show only value-free text
An error from a type's `Validate()` or from a replacement validator SHALL fail with message `DI: config: invalid "<section>" (<type>): <detail>`, where section is the service name for `BindConfigNamed` and the prefix otherwise, and detail is the text of the first `ValueFreeError` in the chain, else the error's type; the cause SHALL stay reachable with `errors.Is` and `errors.As`.

#### Scenario: Messages for Validate errors
```gherkin
Given root's property source holds "db.primary.port" = "22"
And DBConfig.Validate returns <returned> for port 22
When the consumer calls <call>
Then the error message is "<message>"
And errors.Is(err, doger.ErrInvalidConfig) is true and errors.As finds the returned error

Examples:
  | returned                                         | call                                                                    | message                                                                       |
  | errors.New("port 22 is reserved")                | doger.ResolveConfig(root, "db.primary", DBConfig{})                     | DI: config: invalid "db.primary" (app.DBConfig): *errors.errorString          |
  | a value of app.reservedPort implementing doger.ValueFreeError with text "port is reserved" | doger.ResolveConfig(root, "db.primary", DBConfig{}) | DI: config: invalid "db.primary" (app.DBConfig): port is reserved |
  | fmt.Errorf("check: %w", a reservedPort value)    | doger.ResolveConfig(root, "db.primary", DBConfig{})                     | DI: config: invalid "db.primary" (app.DBConfig): port is reserved             |
  | errors.New("port 22 is reserved")                | doger.BindConfigNamed(root, "primary-db", "db.primary", DBConfig{})     | DI: config: invalid "primary-db" (app.DBConfig): *errors.errorString          |
```

<!-- cases: 1 default: a plain error shows its type only -->
<!-- cases: 2 overrides: implementing ValueFreeError opts the text in -->
<!-- cases: N/A 3 zero values: a nil error passes -->
<!-- cases: 4 boundaries: marked error direct and wrapped; prefix versus service name -->
<!-- cases: 5 errors: the subject; both sentinels and the cause reachable -->
<!-- cases: N/A 6-9: as for every validation -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: One validator replaces go-playground
A non-nil `InjectorOpts.ConfigValidator` SHALL replace go-playground for every bind in the container and its clones that keep it, receiving a pointer to a copy of the merged struct whose changes SHALL NOT reach the bound value; a non-nil error SHALL fail the bind as in "Validation failures from code show only value-free text", and the type's `Validate()` SHALL still run after it passes.

#### Scenario: Validator choice
```gherkin
Given root, err := doger.NewWithOpts(<opts>) returned a nil err
And a recording validator optsValidator returning <returns>, which also sets the received struct's Port to 1
And type ServerConfig struct { Port int `validate:"max=10"` } with a Validate() that records each call, and a property source holding "server.port" = "99"
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then optsValidator recorded <recorded>, Validate was called <calls> times, and the result and error are <outcome>

Examples:
  | opts                                                       | returns | recorded                    | calls | outcome                                                                                           |
  | &doger.InjectorOpts{ConfigValidator: optsValidator}        | nil     | &ServerConfig{Port: 99}     | 1     | ServerConfig{Port: 99}, nil                                                                       |
  | &doger.InjectorOpts{ConfigValidator: optsValidator}        | errX    | &ServerConfig{Port: 99}     | 0     | zero result, "DI: config: invalid \"server\" (app.ServerConfig): *errors.errorString" matching ErrInvalidConfig and errX |
  | &doger.InjectorOpts{}                                      | nil     | nothing                     | 0     | zero result, an error matching ErrInvalidConfig (go-playground's max)                             |
```

#### Scenario: Clones keep or replace the validator
```gherkin
Given root created with InjectorOpts{ConfigValidator: optsValidator}
And clone is <clone>
When the consumer calls doger.ResolveConfig(clone, "server", ServerConfig{})
Then <used> validated the value

Examples:
  | clone                                                                       | used          |
  | root.Clone()                                                                | optsValidator |
  | the clone from root.CloneWithOpts(&doger.InjectorOpts{})                    | go-playground |
  | the clone from root.CloneWithOpts(&doger.InjectorOpts{ConfigValidator: other}) | other      |
```

<!-- cases: 1 default: go-playground row -->
<!-- cases: 2 overrides: InjectorOpts.ConfigValidator; there is no source-level validator -->
<!-- cases: 3 zero values: nil field means go-playground -->
<!-- cases: 4 boundaries: passing and failing replacement; a replacement that mutates its argument -->
<!-- cases: 5 errors: a replacement's error is wrapped as both sentinels, shown by type unless value-free -->
<!-- cases: N/A 6 duplicate: one field -->
<!-- cases: 7 lifecycle: Clone keeps the parent's options; CloneWithOpts replaces them, as for every option -->
<!-- cases: N/A 8 concurrency: as for tags -->
<!-- cases: 9 panics: covered by "A panicking validator is an error" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Validation runs on the merged value
Validation SHALL run once per bind on the value after defaults and every source layer, and for `ValidateConfig` on the value as the caller passes it, so a default that breaks a rule SHALL fail like a bad source value.

#### Scenario: Defaults are validated too
```gherkin
Given root's property source holds <held>
And type ServerConfig struct { Port int `validate:"min=1"`; Host string `validate:"required"` }
When the consumer calls doger.ResolveConfig(root, "server", <defaults>)
Then errors.Is(err, doger.ErrInvalidConfig) is <invalid>

Examples:
  | held                    | defaults                                  | invalid |
  | nothing                 | ServerConfig{Port: 8080, Host: "0.0.0.0"} | false   |
  | nothing                 | ServerConfig{Port: 0, Host: "0.0.0.0"}    | true    |
  | "server.port" = "8080"  | ServerConfig{Port: 0, Host: "0.0.0.0"}    | false   |
  | "server.host" = ""      | ServerConfig{Port: 8080, Host: "0.0.0.0"} | true    |
```

<!-- cases: 1 default: valid defaults row -->
<!-- cases: 2 overrides: a source value fixes a bad default -->
<!-- cases: 3 zero values: zero default, empty source value -->
<!-- cases: N/A 4 boundaries: covered by "Tags are validated by default in every container" -->
<!-- cases: 5 errors: ErrInvalidConfig rows -->
<!-- cases: N/A 6-9: as for every validation -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Validation errors list keys and rules, never values
A tag failure SHALL be a `*ConfigValidationError` matching `ErrInvalidConfig`, whose `Violations` hold each failing field's full key (map entries as `key[name]`, promoted fields under the parent's key) and rule, sorted by key, with message `DI: config: invalid properties: <key> (<rule>), ...`; it SHALL NOT wrap or expose the validator's field errors or any value.

#### Scenario: Error shape and keys
```gherkin
Given root's property source holds <held>
And type AppConfig struct { Server ServerConfig; DB DBConfig `cfg:"db"`; Upstreams []Upstream; Common } with tags as in <tags>
When the consumer calls doger.ResolveConfig(root, "app", AppConfig{})
Then errors.As(err, &verr) is true and verr.Violations is <violations>
And the error message is "<message>"

Examples:
  | held                                                         | tags                                                         | violations                                    | message                                                                       |
  | "app.server.port" = "99999", "app.db.url" = ""               | Port `validate:"max=65535"`, URL `validate:"required"`       | [{app.db.url required} {app.server.port max}] | DI: config: invalid properties: app.db.url (required), app.server.port (max)  |
  | "app.upstreams[1].name" = ""                                 | Upstream.Name `validate:"required"` with `dive` on Upstreams | [{app.upstreams[1].name required}]            | DI: config: invalid properties: app.upstreams[1].name (required)              |
  | "app.timeout" = "0s"                                         | Common.Timeout `validate:"gt=0"`                             | [{app.timeout gt}]                            | DI: config: invalid properties: app.timeout (gt)                              |
  | "app.db.replicas[east].addr" = ""                            | Replica.Addr `validate:"required"` with `dive` on Replicas   | [{app.db.replicas[east].addr required}]       | DI: config: invalid properties: app.db.replicas[east].addr (required)         |
```

#### Scenario: Secret values stay out of errors and logs
```gherkin
Given a root with a Logf that records lines and a property source holding "db.password" = "s3cret"
And type DBConfig struct { Password string `validate:"min=10"` }
When the consumer calls doger.ResolveConfig(root, "db", DBConfig{})
Then the error message is "DI: config: invalid properties: db.password (min)"
And none of the error message, fmt.Sprintf("%#v", err), fmt.Sprintf("%+v", err) or any recorded log line contains "s3cret"
And errors.As(err, new(validator.ValidationErrors)) is false
```

<!-- cases: N/A 1 default, 2 overrides: shape of the error -->
<!-- cases: 3 zero values: empty value rows (required) -->
<!-- cases: 4 boundaries: one versus several failures; nested, list, embedded and map keys -->
<!-- cases: 5 errors: the subject; errors.As on the exported type; the singular case uses the same wording; %#v is type-only through GoString -->
<!-- cases: N/A 6 duplicate: one entry per key -->
<!-- cases: N/A 7-9: as for every validation -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A malformed validate tag is a target error
A `validate:` tag the validator cannot parse SHALL fail the bind with an error matching `ErrInvalidConfigTarget`, not `ErrInvalidConfig`, naming the struct type and saying the tag is malformed, without the validator's own message.

#### Scenario: A malformed tag
```gherkin
Given type ServerConfig struct { Port int `validate:"foo"` }
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then errors.Is(err, doger.ErrInvalidConfigTarget) is true and errors.Is(err, doger.ErrInvalidConfig) is false
And the error message is "DI: config: invalid target: app.ServerConfig: malformed validate tag"
```

<!-- cases: N/A 1 default, 2 overrides: error path of the default validator; a replacement validator reports its own errors -->
<!-- cases: N/A 3 zero values: an empty validate tag means no rule -->
<!-- cases: N/A 4 boundaries: one malformed tag -->
<!-- cases: 5 errors: target sentinel, because the struct is wrong, not the configuration -->
<!-- cases: N/A 6-8: as for every validation -->
<!-- cases: 9 panics: go-playground panics on such a tag; the panic is recovered into this error -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: ValidateConfig validates an extension's merged value
`ValidateConfig(i, v)` SHALL validate `v`, a struct or a non-nil pointer to one, with `i`'s container validator and `v`'s own `Validate() error`, reporting keys relative to `v` and naming `v`'s type without a section; it SHALL return nil, an error matching `ErrInvalidConfig`, or `ErrInvalidConfigTarget` for any other `v`. A nil `i` SHALL mean `DefaultRootScope`.

#### Scenario: An extension validates after applying its options
```gherkin
Given root's property source holds "redis.pool-size" = "0"
And an extension "redis" whose Extend binds cfg := Config{PoolSize: 10} with doger.BindProperties, applies its option <option>, then returns doger.ValidateConfig(x.Root(), cfg) where PoolSize has `validate:"min=1"`
When the consumer calls doger.Extend(root, redisExt)
Then the returned error <result>

Examples:
  | option          | result                                                                                              |
  | none            | wraps an error matching doger.ErrInvalidConfig with message "DI: config: invalid properties: pool-size (min)" |
  | WithPoolSize(5) | is nil                                                                                              |
```

#### Scenario: Messages without a section
```gherkin
Given type Config with a Validate() returning errors.New("bad")
When the consumer calls doger.ValidateConfig(root, Config{})
Then the error message is "DI: config: invalid app.Config: *errors.errorString"
```

#### Scenario: Invalid values to validate
```gherkin
When the consumer calls doger.ValidateConfig(root, <v>)
Then errors.Is(err, doger.ErrInvalidConfigTarget) is true

Examples:
  | v                   |
  | nil                 |
  | (*Config)(nil)      |
  | 5                   |
  | []Config{}          |
```

<!-- cases: 1 default: no option row -->
<!-- cases: 2 overrides: code option wins before validation -->
<!-- cases: 3 zero values: nil and typed-nil v; nil injector means DefaultRootScope -->
<!-- cases: 4 boundaries: struct value and pointer accepted; non-struct rejected -->
<!-- cases: 5 errors: ErrInvalidConfig wrapped in the extension's error; ErrInvalidConfigTarget; message without a section -->
<!-- cases: N/A 6-9: as for tags -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: A panicking validator is an error
A panic in the container's validator or in a type's `Validate()` SHALL be recovered and returned as an error matching `ErrInvalidConfig`, with message `DI: config: invalid "<section>" (<type>): validator panic of type <panic type>`, never the panic value.

#### Scenario: Validator panics
```gherkin
Given <panicking> panics with <value>
When the consumer calls doger.ResolveConfig(root, "server", ServerConfig{})
Then errors.Is(err, doger.ErrInvalidConfig) is true
And the error message is "DI: config: invalid \"server\" (app.ServerConfig): validator panic of type <type>"

Examples:
  | panicking                     | value                        | type                    |
  | InjectorOpts.ConfigValidator  | "bad value s3cret"           | string                  |
  | ServerConfig.Validate         | errors.New("s3cret")         | *errors.errorString     |
  | ServerConfig.Validate         | nil                          | *runtime.PanicNilError  |
```

<!-- cases: N/A 1 default, 2 overrides: failure path -->
<!-- cases: 3 zero values: panic(nil) row -->
<!-- cases: N/A 4 boundaries: one panic per row -->
<!-- cases: 5 errors: matches ErrInvalidConfig; the value never appears -->
<!-- cases: N/A 6-8: as for every validation -->
<!-- cases: 9 panics: the subject; returned as an error to the caller, not routed to HookContainerPanic, because the caller receives it synchronously; a malformed tag is "A malformed validate tag is a target error" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Concurrent validation
Concurrent binds on one container SHALL validate concurrently without data races, including with the default go-playground validator.

#### Scenario: Concurrent binds validate under -race
```gherkin
Given root's property source holds "server.port" = "99999" and "jobs.workers" = "4"
When 100 goroutines start together, half calling doger.ResolveConfig(root, "server", ServerConfig{}) and half doger.ResolveConfig(root, "jobs", JobsConfig{})
Then every server call returns an error matching doger.ErrInvalidConfig
And every jobs call returns JobsConfig{Workers: 4}
And the race detector reports nothing
```

<!-- cases: N/A 1-7: concurrency only; the other items are covered above -->
<!-- cases: 8 concurrency: the subject; runs under -race; the lazily built validator is created once -->
<!-- cases: N/A 9 panics: no input panics here -->
<!-- cases: N/A 10 compatibility: new API -->
