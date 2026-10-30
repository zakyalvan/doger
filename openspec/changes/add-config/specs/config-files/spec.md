# Spec Delta

## Purpose

The `github.com/kartaladev/doger/config` module's `config.Files` source: it finds config files, layers them with `.env`, process env and added sources into one immutable snapshot, and answers `Lookup` and `Keys` for the container. It does this without viper, without writing the process environment by default, and without ever putting a value in an error.

## ADDED Requirements

### Requirement: Zero-config file discovery
`config.Files()` with no options SHALL search `.` and then `./config` for a base file named `config` with the extension `.yaml`, `.yml`, `.toml` or `.json`. Every file found SHALL load, and `./config` SHALL win over `.` key by key. Finding no file at all SHALL NOT be an error.

#### Scenario: Base files found in the working directory and ./config
```gherkin
Given a temp working directory (t.Chdir, test not parallel) containing <files>
And no APP_* variable and no .env exist
When the consumer calls config.Files().Load(ctx)
Then Load returns <error>
And Lookup("server.port") returns <result>

Examples:
  | files                                                                            | error | result         |
  | config.yaml with server.port: 8080                                               | nil   | ("8080", true) |
  | config.yml with server.port: 8080                                                | nil   | ("8080", true) |
  | config.toml with [server] port = 8080                                            | nil   | ("8080", true) |
  | config.json with {"server":{"port":8080}}                                        | nil   | ("8080", true) |
  | config/config.yaml with server.port: 8181                                        | nil   | ("8181", true) |
  | config.yaml with server.port: 8080 and config/config.yaml with server.port: 8181 | nil   | ("8181", true) |
  | an empty config.yaml                                                             | nil   | ("", false)    |
  | nothing                                                                          | nil   | ("", false)    |
```

#### Scenario: Both directories merge key by key
```gherkin
Given a temp working directory with config.yaml holding server.host: "a" and server.port: 8080
And config/config.yaml holding server.port: 8181
When the consumer loads config.Files()
Then Lookup("server.host") returns ("a", true) and Lookup("server.port") returns ("8181", true)
```

<!-- cases: 1 default: the whole requirement -->
<!-- cases: 2 overrides: explicit paths are "Explicit paths skip discovery" -->
<!-- cases: 3 zero values: an empty file and no file at all -->
<!-- cases: 4 boundaries: one directory, both directories -->
<!-- cases: 5 errors: ambiguous formats are "One format per base name" -->
<!-- cases: 6 duplicate: a key in both directories (./config wins) -->
<!-- cases: N/A 7 lifecycle: discovery runs in Load -->
<!-- cases: 8 concurrency: covered by "Reads during Load see one snapshot" -->
<!-- cases: N/A 9 panics: Load returns errors -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Discovery order across directories and profiles
Files SHALL load from lowest to highest as: base files in search order, then profile files in search order, so `./config.yaml` < `./config/config.yaml` < `./config.prod.yaml` < `./config/config.prod.yaml`. A list SHALL come whole from the highest file that holds it; lists never merge by index across files.

#### Scenario: Four files and a split list
```gherkin
Given a temp working directory (t.Chdir) with APP_PROFILE=prod and:
  | file                    | content                                 |
  | config.yaml             | a: base-dot,  b: base-dot,  hosts: [1, 2, 3] |
  | config/config.yaml      | a: base-cfg,  b: base-cfg,  hosts: [4]      |
  | config.prod.yaml        | a: prod-dot                              |
  | config/config.prod.yaml | c: prod-cfg                              |
When the consumer loads config.Files()
Then Lookup("<key>") returns <result>

Examples:
  | key      | result             |
  | a        | ("prod-dot", true) |
  | b        | ("base-cfg", true) |
  | c        | ("prod-cfg", true) |
  | hosts[0] | ("4", true)        |
  | hosts[1] | ("", false)        |
```

<!-- cases: 1 default: the documented order -->
<!-- cases: N/A 2 overrides: LayerOrder reorders layers, not files within a layer -->
<!-- cases: N/A 3 zero values: covered by discovery -->
<!-- cases: 4 boundaries: every adjacent pair of the four files; a list shorter in the higher file -->
<!-- cases: N/A 5 errors: ordering cannot fail -->
<!-- cases: 6 duplicate: key a in three files -->
<!-- cases: N/A 7-9: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: One format per base name
Within one directory, two files with the same base name and different extensions, `.yaml` and `.yml` included, SHALL fail `Load` with an error matching `config.ErrAmbiguousFormat` that names both files. `config.Format(f)` SHALL restrict discovery to one `config.FileFormat`.

#### Scenario: Two formats for one base name
```gherkin
Given a temp working directory (t.Chdir) with <files>
When the consumer loads <source>
Then Load returns <result>

Examples:
  | files                     | source                                   | result                                                                        |
  | config.yaml, config.toml  | config.Files()                           | an error matching config.ErrAmbiguousFormat naming config.yaml and config.toml |
  | config.yaml, config.yml   | config.Files()                           | an error matching config.ErrAmbiguousFormat naming config.yaml and config.yml  |
  | config.yaml, config.toml  | config.Files(config.Format(config.TOML)) | nil, and only config.toml was read                                            |
  | config.yaml, config.toml  | config.Files(config.Format(config.YAML)) | nil, and only config.yaml was read                                            |
  | config.yaml, config.yml   | config.Files(config.Format(config.YAML)) | an error matching config.ErrAmbiguousFormat                                   |
```

<!-- cases: 1 default: the first two rows -->
<!-- cases: 2 overrides: Format rows -->
<!-- cases: 3 zero values: an unknown FileFormat is "Options are checked when Load runs" -->
<!-- cases: 4 boundaries: .yaml with .yml under Format(YAML) is still two files -->
<!-- cases: 5 errors: ErrAmbiguousFormat matchable with errors.Is -->
<!-- cases: N/A 6-9: no state, order or concurrency beyond Load -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Profile overlay
The profile SHALL come from `config.Profile(p)`, else from the variable named by `config.ProfileEnv(name)` (default `APP_PROFILE`), and `config.<profile>.<ext>` SHALL load over the base files. `Profile("")` SHALL mean no profile. A set profile with no file in any directory SHALL fail `Load` matching `config.ErrProfileNotFound`. A profile file with no base file SHALL load alone.

#### Scenario: Profile selection
```gherkin
Given a temp working directory (t.Chdir, t.Setenv) with config.yaml holding server.port: 8080 and config.prod.yaml holding server.port: 9090
And <env>
When the consumer loads <source>
Then Load returns <error> and Lookup("server.port") returns <result>

Examples:
  | env              | source                                    | error                                    | result         |
  | nothing set      | config.Files()                            | nil                                      | ("8080", true) |
  | APP_PROFILE=prod | config.Files()                            | nil                                      | ("9090", true) |
  | APP_PROFILE=prod | config.Files(config.Profile(""))          | nil                                      | ("8080", true) |
  | APP_PROFILE=prod | config.Files(config.Profile("dev"))       | config.ErrProfileNotFound naming "dev"   | ("", false)    |
  | nothing set      | config.Files(config.Profile("prod"))      | nil                                      | ("9090", true) |
  | MY_ENV=prod      | config.Files(config.ProfileEnv("MY_ENV")) | nil                                      | ("9090", true) |
  | APP_PROFILE=stage| config.Files()                            | config.ErrProfileNotFound naming "stage" | ("", false)    |
  | APP_PROFILE=     | config.Files()                            | nil                                      | ("8080", true) |
```

#### Scenario: A profile file without a base file
```gherkin
Given a temp working directory (t.Chdir) holding only config.prod.yaml with server.port: 9090
When the consumer loads config.Files(config.Profile("prod"))
Then Lookup("server.port") returns ("9090", true)
```

#### Scenario: The profile variable is not a config key
```gherkin
Given a temp working directory (t.Chdir) with config.prod.yaml and APP_PROFILE=prod set
When the consumer loads config.Files()
Then Lookup("profile") returns ("", false) and Keys("") contains no "profile"
```

<!-- cases: 1 default: APP_PROFILE rows -->
<!-- cases: 2 overrides: Profile beats APP_PROFILE; Profile("") turns it off; ProfileEnv renames the variable -->
<!-- cases: 3 zero values: an empty APP_PROFILE means no profile; ProfileEnv("") is "Options are checked when Load runs" -->
<!-- cases: 4 boundaries: profile without base; profile in one directory only -->
<!-- cases: 5 errors: ErrProfileNotFound; invalid names are "Profile names cannot escape the config directories" -->
<!-- cases: N/A 6 duplicate: one profile at a time -->
<!-- cases: N/A 7-9: Load only -->
<!-- cases: N/A 10 compatibility: new module; golang-lib's "dev" default is in the adoption guide -->

### Requirement: Profile names cannot escape the config directories
A profile name SHALL match `^[a-z0-9][a-z0-9_-]*$`; any other name SHALL fail `Load` matching `config.ErrInvalidOption`. Every file SHALL be opened through `os.Root` of its directory, so a symlink that stays inside the directory resolves and one that leaves it fails `Load` naming the file.

#### Scenario: Profile names
```gherkin
Given a temp working directory (t.Chdir) with config.yaml and config.<name>.yaml for every valid name below
When the consumer loads config.Files(config.Profile("<name>"))
Then Load returns <error>

Examples:
  | name          | error                                  |
  | prod          | nil                                    |
  | qa_1          | nil                                    |
  | eu-west-1     | nil                                    |
  | ../x          | an error matching config.ErrInvalidOption |
  | prod/../../x  | an error matching config.ErrInvalidOption |
  | Prod          | an error matching config.ErrInvalidOption |
  | -x            | an error matching config.ErrInvalidOption |
```

#### Scenario: Symlinks inside and outside the directory
```gherkin
Given a temp directory d holding <link> as a symlink
When the consumer loads config.Files(config.Dir("d"))
Then Load returns <error>

Examples:
  | link                                                       | error                                   |
  | config.yaml -> ..data/config.yaml, ..data -> ..v1 (inside d) | nil                                    |
  | config.yaml -> ../outside.yaml                             | an error naming "d/config.yaml"         |
```

<!-- cases: N/A 1 default, 2 overrides: the rule is not configurable on purpose -->
<!-- cases: 3 zero values: Profile("") is "no profile" -->
<!-- cases: 4 boundaries: leading digit, underscore, dash; uppercase rejected -->
<!-- cases: 5 errors: ErrInvalidOption rows and the escaping symlink -->
<!-- cases: N/A 6-8 -->
<!-- cases: N/A 9 panics -->
<!-- cases: N/A 10 compatibility: new module; symlink rows skip on Windows -->

### Requirement: Explicit paths skip discovery
`config.Dir(paths...)` SHALL replace the searched directories, all loading and the later winning, and a directory that does not exist SHALL fail `Load` matching `fs.ErrNotExist`. `config.File(paths...)` SHALL load exactly those files, the later winning, and a missing file SHALL fail `Load` matching `fs.ErrNotExist`. `config.Name(base)` SHALL change the base name used by discovery.

#### Scenario: Directories, files and base name
```gherkin
Given temp directories a/ with config.yaml (server.port: 1), b/ with config.yaml (server.port: 2), empty/ with no file, a file c/app.toml with [server] port = 3, and d/myapp.yaml with server.port: 4
When the consumer loads <source>
Then Load returns <error> and Lookup("server.port") returns <result>

Examples:
  | source                                              | error                                       | result      |
  | config.Files(config.Dir("a"))                       | nil                                         | ("1", true) |
  | config.Files(config.Dir("a", "b"))                  | nil                                         | ("2", true) |
  | config.Files(config.Dir("b", "a"))                  | nil                                         | ("1", true) |
  | config.Files(config.Dir("empty"))                   | nil                                         | ("", false) |
  | config.Files(config.Dir("nope"))                    | an error matching fs.ErrNotExist naming "nope" | ("", false) |
  | config.Files(config.File("c/app.toml"))             | nil                                         | ("3", true) |
  | config.Files(config.File("c/missing.yaml"))         | an error matching fs.ErrNotExist naming "c/missing.yaml" | ("", false) |
  | config.Files(config.Dir("d"), config.Name("myapp")) | nil                                         | ("4", true) |
```

<!-- cases: N/A 1 default: discovery is the previous requirements -->
<!-- cases: 2 overrides: every row -->
<!-- cases: 3 zero values: an empty directory; Dir() with no paths is "Options are checked when Load runs" -->
<!-- cases: 4 boundaries: one and two directories, both orders -->
<!-- cases: 5 errors: missing directory and missing file match fs.ErrNotExist -->
<!-- cases: 6 duplicate: the same directory twice loads once -->
<!-- cases: N/A 7-9: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Formats are chosen by extension
Files SHALL be parsed by extension, compared case-insensitively: YAML for `.yaml` and `.yml`, TOML for `.toml`, JSON for `.json`. `config.Codec(ext, d)`, with `ext` starting with `.` and `d` a `config.Decoder`, SHALL add a format or replace a built-in one, the last call per extension winning. A file whose extension has no decoder SHALL fail `Load` naming the file.

#### Scenario: Built-in and custom decoders
```gherkin
Given a temp directory holding <file>
When the consumer loads config.Files(config.File("<file>")<extra>)
Then Load returns <error> and Lookup("server.port") returns <result>

Examples:
  | file     | extra                                  | error                       | result               |
  | app.yaml |                                        | nil                         | ("8080", true)       |
  | app.YML  |                                        | nil                         | ("8080", true)       |
  | app.json |                                        | nil                         | ("8080", true)       |
  | app.hcl  |                                        | an error naming "app.hcl"   | ("", false)          |
  | app.hcl  | , config.Codec(".hcl", hclDecoder)     | nil                         | ("8080", true)       |
  | app.yaml | , config.Codec(".yaml", upperDecoder)  | nil                         | ("8080-upper", true) |
```

<!-- cases: 1 default: built-in rows -->
<!-- cases: 2 overrides: Codec add and replace rows -->
<!-- cases: 3 zero values: Codec with a nil decoder or an ext without "." is "Options are checked when Load runs" -->
<!-- cases: 4 boundaries: upper-case extension -->
<!-- cases: 5 errors: unknown extension; a decoder error becomes a *config.ParseError ("Load errors carry no values") -->
<!-- cases: 6 duplicate: the last Codec for one extension wins -->
<!-- cases: N/A 7-9: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: File keys become canonical keys
A file key SHALL be split on `.`; `/` inside a segment SHALL mark an extension instance; a key written as `"[...]"` SHALL be kept verbatim as a map key; every other segment SHALL be normalized to lowercase kebab. Nested maps SHALL flatten to dotted keys, sequences to `[i]` (nested sequences to `[i][j]`), and `null` SHALL mean the key is absent.

#### Scenario: Flattening and normalization
```gherkin
Given config.yaml holding:
  server:
    issuerUri: https://kartala.id
    read_timeout: 5s
    hosts: [10.0.0.1, 10.0.0.2]
    matrix: [[1, 2], [3]]
    labels:
      "[team/core]": payments
      "[X-Request-ID]": abc
      tier: web
    gone: null
  redis/queue:
    addr: q:6379
When the consumer loads config.Files(config.File("config.yaml"))
Then Lookup("<key>") returns <result>

Examples:
  | key                          | result                       |
  | server.issuer-uri            | ("https://kartala.id", true) |
  | server.read-timeout          | ("5s", true)                 |
  | server.hosts[0]              | ("10.0.0.1", true)           |
  | server.hosts[2]              | ("", false)                  |
  | server.matrix[0][1]          | ("2", true)                  |
  | server.matrix[1][0]          | ("3", true)                  |
  | server.labels[team/core]     | ("payments", true)           |
  | server.labels[X-Request-ID]  | ("abc", true)                |
  | server.labels.tier           | ("web", true)                |
  | server.gone                  | ("", false)                  |
  | redis/queue.addr             | ("q:6379", true)             |
```

<!-- cases: N/A 1 default, 2 overrides: normalization is not configurable; a tag on the target struct renames a field at binding -->
<!-- cases: 3 zero values: null, an empty sequence and an empty map produce no keys -->
<!-- cases: 4 boundaries: index 0, past the end, nested sequence -->
<!-- cases: 5 errors: invalid file shapes are "Invalid file shapes fail the load" -->
<!-- cases: 6 duplicate: keys that normalize together are "Invalid file shapes fail the load" -->
<!-- cases: N/A 7-9: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Invalid file shapes fail the load
`Load` SHALL fail with a `*config.ParseError` naming the file, the line where known, and a fixed category when a file holds: two keys that normalize to one name ("duplicate key"); the same key twice in any format ("duplicate key"); more than one YAML document ("multiple documents"); a top level that is not a mapping ("not a mapping"); or YAML aliases expanding past yaml.v3's limits ("excessive aliasing").

#### Scenario: Shapes that fail
```gherkin
Given a temp directory holding <file> with <content>
When the consumer loads config.Files(config.File("<file>"))
Then Load returns a *config.ParseError with File "<file>", Line <line> and Category "<category>"

Examples:
  | file        | content                                            | line | category           |
  | a.yaml      | logger: {log-file: x.log, logFile: y.log}          | 1    | duplicate key      |
  | a.yaml      | "port: 1\nport: 2"                                 | 2    | duplicate key      |
  | a.toml      | "port = 1\nport = 2"                               | 2    | duplicate key      |
  | a.json      | {"port": 1, "port": 2}                             | 1    | duplicate key      |
  | a.yaml      | "a: 1\n---\nb: 2"                                  | 2    | multiple documents |
  | a.yaml      | "- 1\n- 2"                                         | 1    | not a mapping      |
  | a.json      | [1, 2]                                             | 1    | not a mapping      |
  | a.yaml      | a "billion laughs" alias bomb of 9 levels           | 0    | excessive aliasing |
```

<!-- cases: N/A 1 default, 2 overrides: shape rules are fixed -->
<!-- cases: 3 zero values: an empty file is not an error (discovery) -->
<!-- cases: 4 boundaries: line 0 when the parser reports no position -->
<!-- cases: 5 errors: every row; errors.As reaches *config.ParseError -->
<!-- cases: 6 duplicate: duplicate and normalized-duplicate rows; the error names neither value -->
<!-- cases: N/A 7, 8: Load only -->
<!-- cases: 9 panics: an alias cycle never overflows the stack; fuzz target FuzzFiles_YAML seeds include the bomb and a self-alias -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Scalars become strings the same way in every format
Scalar values SHALL be stored as strings: YAML as the node's literal text, JSON numbers as their literal text, TOML integers in decimal, TOML floats with `strconv.FormatFloat(f, 'g', -1, 64)`, TOML datetimes in RFC 3339, and booleans as `true` or `false`.

#### Scenario: Rendering by format
```gherkin
Given a temp directory holding <file> with <content>
When the consumer loads config.Files(config.File("<file>"))
Then Lookup("v") returns ("<value>", true)

Examples:
  | file   | content                         | value                |
  | a.json | {"v": 12345678901234567890}     | 12345678901234567890 |
  | a.json | {"v": 1e3}                      | 1e3                  |
  | a.json | {"v": true}                     | true                 |
  | a.toml | v = 1.0                         | 1                    |
  | a.toml | v = 0.5                         | 0.5                  |
  | a.toml | v = 1979-05-27T07:32:00Z        | 1979-05-27T07:32:00Z |
  | a.toml | v = 0x1F                        | 31                   |
  | a.yaml | v: 0x1F                         | 0x1F                 |
  | a.yaml | v: 5s                           | 5s                   |
  | a.yaml | v: yes                          | yes                  |
```

<!-- cases: N/A 1 default, 2 overrides: rendering is fixed; the binder parses strings -->
<!-- cases: 3 zero values: empty string values stay "" and present -->
<!-- cases: 4 boundaries: integer past int64, exponent, hex -->
<!-- cases: N/A 5 errors: rendering cannot fail -->
<!-- cases: N/A 6-9 -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Layer precedence
For a key held by several layers, `Lookup` SHALL return the highest layer's value, from highest: added layers, process env, `.env`, profile files, base files. Layers SHALL match keys in uniform form (lowercase, `-` and `_` removed per segment, `[...]` untouched), so env's dash-free names override dashed file keys.

#### Scenario: Highest layer wins
```gherkin
Given config.yaml holding server.port: 8080, config.prod.yaml holding server.port: 9090, APP_PROFILE=prod (t.Chdir, t.Setenv)
And <dotenv>
And <env>
When the consumer loads config.Files()
Then Lookup("server.port") returns ("<value>", true)

Examples:
  | dotenv                            | env                  | value |
  | no .env                           | nothing set          | 9090  |
  | .env holding APP_SERVER_PORT=9191 | nothing set          | 9191  |
  | .env holding APP_SERVER_PORT=9191 | APP_SERVER_PORT=9292 | 9292  |
  | no .env                           | APP_SERVER_PORT=9292 | 9292  |
```

#### Scenario: Env overrides a dashed file key
```gherkin
Given config.yaml holding server.read-timeout: 5s and server.allowed-hosts: [a, b]
And APP_SERVER_READTIMEOUT=9s and APP_SERVER_ALLOWEDHOSTS_0=z are set
When the consumer loads config.Files()
Then Lookup("<key>") returns <result>

Examples:
  | key                       | result        |
  | server.read-timeout       | ("9s", true)  |
  | server.readtimeout        | ("9s", true)  |
  | server.allowed-hosts[0]   | ("z", true)   |
  | server.allowed-hosts[1]   | ("", false)   |
```

<!-- cases: 1 default: the order itself -->
<!-- cases: 2 overrides: LayerOrder is "Added layers and their order" -->
<!-- cases: 3 zero values: a set but empty env var is the empty value and wins -->
<!-- cases: 4 boundaries: every adjacent pair of layers; dashed against dash-free -->
<!-- cases: N/A 5 errors: precedence has no error path -->
<!-- cases: 6 duplicate: the same key in four layers -->
<!-- cases: N/A 7-9: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: A list comes whole from one layer
A list key SHALL take all its entries from the highest layer that holds the key in any form: indexed entries or one scalar. A scalar in a higher layer SHALL mask a lower layer's indices, so the binder can comma-split it.

#### Scenario: Lists across layers
```gherkin
Given config.yaml holding server.hosts: [a, b, c]
And <env>
When the consumer loads config.Files() and calls Keys("server.hosts")
Then the result is <keys> and Lookup("<lookup>") returns <value>

Examples:
  | env                       | keys                  | lookup           | value         |
  | nothing set               | ["server.hosts[0]", "server.hosts[1]", "server.hosts[2]"] | server.hosts[2] | ("c", true) |
  | APP_SERVER_HOSTS_0=z      | ["server.hosts[0]"]   | server.hosts[1]  | ("", false)   |
  | APP_SERVER_HOSTS=z,y      | ["server.hosts"]      | server.hosts     | ("z,y", true) |
```

<!-- cases: 1 default: file-only row -->
<!-- cases: N/A 2 overrides: list precedence follows layer order -->
<!-- cases: 3 zero values: an empty sequence in a higher file masks nothing; null is absent -->
<!-- cases: 4 boundaries: shorter higher list; scalar over indices -->
<!-- cases: N/A 5 errors -->
<!-- cases: 6 duplicate: the same list in two layers -->
<!-- cases: N/A 7-9: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Env prefix
The env layer SHALL read variables under a prefix, `APP_` by default. `config.EnvPrefix(p)` SHALL uppercase `p` and append `_` when missing; `""` SHALL turn the prefix off. A prefix with characters other than `[A-Za-z0-9_]` SHALL fail `Load` matching `config.ErrInvalidOption`. The same normalization applies to `doger.EnvPropertySource`.

#### Scenario: Prefix forms
```gherkin
Given <variable>=x is set (t.Setenv)
When the consumer loads <source>
Then Load returns <error> and Lookup("logger.log-file") returns <result>

Examples:
  | source                                   | variable              | error                                     | result      |
  | config.Files()                           | APP_LOGGER_LOGFILE    | nil                                       | ("x", true) |
  | config.Files(config.EnvPrefix("MYSVC"))  | MYSVC_LOGGER_LOGFILE  | nil                                       | ("x", true) |
  | config.Files(config.EnvPrefix("MYSVC_")) | MYSVC_LOGGER_LOGFILE  | nil                                       | ("x", true) |
  | config.Files(config.EnvPrefix("mysvc"))  | MYSVC_LOGGER_LOGFILE  | nil                                       | ("x", true) |
  | config.Files(config.EnvPrefix(""))       | LOGGER_LOGFILE        | nil                                       | ("x", true) |
  | config.Files(config.EnvPrefix("MYSVC"))  | APP_LOGGER_LOGFILE    | nil                                       | ("", false) |
  | config.Files(config.EnvPrefix("my-svc")) | MY_SVC_LOGGER_LOGFILE | an error matching config.ErrInvalidOption | ("", false) |
  | config.Files(config.EnvPrefix("my.svc")) | MY_SVC_LOGGER_LOGFILE | an error matching config.ErrInvalidOption | ("", false) |
```

<!-- cases: 1 default: APP_ row -->
<!-- cases: 2 overrides: every EnvPrefix row -->
<!-- cases: 3 zero values: "" turns the prefix off; then every process variable that maps back to a key is a key (documented) -->
<!-- cases: 4 boundaries: with and without trailing "_"; lower-case input -->
<!-- cases: 5 errors: invalid prefixes match ErrInvalidOption -->
<!-- cases: N/A 6 duplicate: the last EnvPrefix wins -->
<!-- cases: N/A 7-9: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Env names follow Spring's mapping
A key SHALL map to a variable by uppercasing, `.` to `_`, removing `-`, `/` to `__` and `[i]` to `_i`; a map key `[k]` SHALL map to `_K` only when `k` matches `[a-z0-9]+`, and other map keys are not addressable from env. The mapping table SHALL be the shared golden file `testdata/envmapping.txt` of the root module.

#### Scenario: Key to variable
```gherkin
Given <variable>=v is set (t.Setenv)
When the consumer loads config.Files()
Then Lookup("<key>") returns ("v", true)

Examples:
  | key                  | variable                |
  | server.read-timeout  | APP_SERVER_READTIMEOUT  |
  | server.issuer-uri    | APP_SERVER_ISSUERURI    |
  | server.hosts[0]      | APP_SERVER_HOSTS_0      |
  | my.service[0].other  | APP_MY_SERVICE_0_OTHER  |
  | server.labels[team]  | APP_SERVER_LABELS_TEAM  |
  | redis/queue.addr     | APP_REDIS__QUEUE_ADDR   |
```

#### Scenario: Variables mapped back to keys
```gherkin
Given only these variables start with "APP_": <variables> (t.Setenv; inherited APP_* variables cleared first)
When the consumer loads config.Files() and calls Keys("")
Then the result is <keys>

Examples:
  | variables                                        | keys                              |
  | APP_SERVER_READTIMEOUT=1                         | ["server.readtimeout"]            |
  | APP_REDIS__QUEUE_ADDR=q                          | ["redis/queue.addr"]              |
  | APP_SERVER_HOSTS_0=a, APP_SERVER_HOSTS_1=b        | ["server.hosts[0]", "server.hosts[1]"] |
  | APP__X=1, app_lower=1, APP_A___B=1               | []                                |
```

<!-- cases: 1 default: Spring mapping rows -->
<!-- cases: 2 overrides: EnvMapper is "A custom env mapper" -->
<!-- cases: 3 zero values: an empty variable value is ("", true) -->
<!-- cases: 4 boundaries: index in the middle and at the end; instance separator -->
<!-- cases: 5 errors: names that do not map back (leading "__", lower case, "___") are skipped, not errors -->
<!-- cases: 6 duplicate: pool-size and poolsize share APP_..._POOLSIZE, as in Spring (decided) -->
<!-- cases: N/A 7-9: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: A custom env mapper
`config.EnvMapper(fn)` SHALL replace the mapping: `fn(key)` returns the full variable name, `EnvPrefix` is not applied to it, and the lookup reads the environment captured at `Load`. A mapped env layer SHALL contribute nothing to `Keys`, so list, map and pointer fields cannot bind from it. A nil mapper SHALL keep the Spring mapping.

#### Scenario: Mapper with and without a prefix
```gherkin
Given SERVER__PORT=7070 and MYSVC_SERVER_PORT=1 are set (t.Setenv)
And mapper := func(key string) string returning "SERVER__PORT" for "server.port"
When the consumer loads config.Files(config.EnvPrefix("MYSVC"), config.EnvMapper(mapper))
Then Lookup("server.port") returns ("7070", true)
And Keys("server") returns []
```

<!-- cases: N/A 1 default: Spring mapping is the previous requirement -->
<!-- cases: 2 overrides: the subject -->
<!-- cases: 3 zero values: a nil mapper keeps Spring; a mapper returning "" is a miss -->
<!-- cases: N/A 4 boundaries -->
<!-- cases: N/A 5 errors: a mapper cannot fail -->
<!-- cases: N/A 6 duplicate: the last EnvMapper wins -->
<!-- cases: 7 lifecycle: the environment is captured at Load; later changes are not seen -->
<!-- cases: N/A 8 concurrency: the mapper is called concurrently and must be pure (documented) -->
<!-- cases: N/A 9 panics: a panicking mapper is the caller's bug; the root recovers it as ErrInvalidConfigValue during binding -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: .env is a layer that never writes the process environment
`config.Files` SHALL read `./.env` from the working directory when it exists, as a layer between the profile files and process env, under the same prefix and mapping, and SHALL NOT set any process variable. `config.DotEnv(path)` SHALL read another file and `config.DotEnv("")` SHALL skip it; an explicit path that is missing SHALL fail `Load` matching `fs.ErrNotExist`.

#### Scenario: .env handling
```gherkin
Given a temp working directory (t.Chdir) with .env holding APP_SPIKE_SECRET=from-dotenv and secrets.env holding APP_SPIKE_SECRET=from-file
And APP_SPIKE_SECRET is unset in the process (t.Setenv then os.Unsetenv, so cleanup restores it)
When the consumer loads <source>
Then Load returns <error>, Lookup("spike.secret") returns <result> and os.LookupEnv("APP_SPIKE_SECRET") returns ("", false)

Examples:
  | source                                      | error                                      | result                |
  | config.Files()                              | nil                                        | ("from-dotenv", true) |
  | config.Files(config.DotEnv("secrets.env"))  | nil                                        | ("from-file", true)   |
  | config.Files(config.DotEnv(""))             | nil                                        | ("", false)           |
  | config.Files(config.DotEnv("missing.env"))  | an error matching fs.ErrNotExist naming "missing.env" | ("", false) |
```

<!-- cases: 1 default: first row (spike 5 RED test) -->
<!-- cases: 2 overrides: DotEnv path and off; export is "DotEnvExport sets only unset variables" -->
<!-- cases: 3 zero values: DotEnv(""); no ./.env is not an error -->
<!-- cases: N/A 4 boundaries: one file -->
<!-- cases: 5 errors: an explicit missing .env -->
<!-- cases: 6 duplicate: a variable in .env and the process: the process wins ("Layer precedence") -->
<!-- cases: N/A 7-9: Load only -->
<!-- cases: N/A 10 compatibility: new module; golang-lib's os.Setenv behaviour is the opt-in export -->

### Requirement: .env syntax
Each `.env` line SHALL be blank, a `#` comment, or `[export ]KEY=VALUE`. An unquoted value SHALL end at ` #`; a single-quoted value SHALL be literal; a double-quoted value SHALL support `\n`, `\"` and `\\`; `${...}` SHALL NOT be expanded. CRLF and a UTF-8 BOM SHALL be accepted, a later line SHALL win, and any other line SHALL fail `Load` with a `*config.ParseError` category "invalid line".

#### Scenario: Line forms
```gherkin
Given a temp working directory (t.Chdir) with .env holding the single line <line>
When the consumer loads config.Files(config.EnvPrefix(""))
Then Load returns <error> and Lookup("k") returns <result>

Examples:
  | line                         | error                                                   | result               |
  | K=v                          | nil                                                     | ("v", true)          |
  | export K=v                   | nil                                                     | ("v", true)          |
  | K=v # note                   | nil                                                     | ("v", true)          |
  | K=v#not-a-comment            | nil                                                     | ("v#not-a-comment", true) |
  | K='a $X \n'                  | nil                                                     | ("a $X \\n", true)   |
  | K="a\nb \"q\" \\"            | nil                                                     | ("a<LF>b \"q\" \\", true) |
  | K=${HOME}                    | nil                                                     | ("${HOME}", true)    |
  | K=v<CR><LF>                  | nil                                                     | ("v", true)          |
  | <BOM>K=v                     | nil                                                     | ("v", true)          |
  | # only a comment             | nil                                                     | ("", false)          |
  | K=                           | nil                                                     | ("", true)           |
  | not a pair s3cret            | a *config.ParseError for ".env" line 1 "invalid line"   | ("", false)          |
  | K="unterminated s3cret       | a *config.ParseError for ".env" line 1 "invalid line"   | ("", false)          |
```

#### Scenario: A later line wins
```gherkin
Given .env holding "K=a" on line 1 and "K=b" on line 2
When the consumer loads config.Files(config.EnvPrefix(""))
Then Lookup("k") returns ("b", true)
```

<!-- cases: N/A 1 default, 2 overrides: the syntax is fixed -->
<!-- cases: 3 zero values: empty value, comment-only file -->
<!-- cases: 4 boundaries: "#" with and without a space before it; CRLF; BOM -->
<!-- cases: 5 errors: invalid lines; the error never contains "s3cret" -->
<!-- cases: 6 duplicate: later line wins (not an error, unlike file formats) -->
<!-- cases: N/A 7, 8: Load only -->
<!-- cases: 9 panics: FuzzFiles_DotEnv; no input panics -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: DotEnvExport sets only unset variables
`config.DotEnvExport()` SHALL, at the first successful `Load` only and never on a reload, set each `.env` variable that is unset in the process, and SHALL log one line naming, never valuing, every variable it skipped because it was already set.

#### Scenario: Export at the first Load
```gherkin
Given .env holding APP_A=1 and APP_B=2, APP_B=0 already set and APP_A unset (t.Setenv then os.Unsetenv, so cleanup restores both)
And a source config.Files(config.DotEnvExport(), config.Logger(rec)) where rec records lines
When the consumer calls Load(ctx)
Then os.Getenv("APP_A") is "1" and os.Getenv("APP_B") is "0"
And rec holds one line naming "APP_B" and containing neither "2" nor "0"
```

#### Scenario: A second Load exports nothing
```gherkin
Given the source above after its first Load, and .env changed to APP_C=3 with APP_C unset
When the consumer calls Load(ctx) again
Then os.LookupEnv("APP_C") returns ("", false) and Lookup("c") returns ("3", true)
```

<!-- cases: 1 default: without DotEnvExport nothing is exported (".env is a layer ...") -->
<!-- cases: 2 overrides: the subject -->
<!-- cases: 3 zero values: an empty .env exports nothing and logs nothing -->
<!-- cases: N/A 4 boundaries -->
<!-- cases: N/A 5 errors: os.Setenv failures are logged by name and do not fail Load -->
<!-- cases: 6 duplicate: an already-set variable is skipped -->
<!-- cases: 7 lifecycle: first Load only; never during Watch -->
<!-- cases: N/A 8 concurrency: export happens inside the serialized Load -->
<!-- cases: N/A 9 panics -->
<!-- cases: N/A 10 compatibility: golang-lib exported unconditionally; this is the opt-in -->

### Requirement: Added layers and their order
`config.Layer(name, src)` SHALL add a `config.LayerSource` above process env, in the order added; it is loaded and copied into the snapshot during `Load`. `config.LayerOrder(names...)` SHALL set the full order from lowest to highest using `LayerBase`, `LayerProfile`, `LayerDotEnv`, `LayerEnv` and added layer names; a layer it omits SHALL NOT be read.

#### Scenario: A vault-style layer
```gherkin
Given config.yaml holding db.password: from-file and APP_DB_PASSWORD=from-env (t.Chdir, t.Setenv)
And vault := a LayerSource holding "db.password" = "from-vault" whose Load counts calls
When the consumer loads <source>
Then Lookup("db.password") returns <result> and vault's Load was called <calls> times

Examples:
  | source                                                                                                   | result               | calls |
  | config.Files(config.Layer("vault", vault))                                                               | ("from-vault", true) | 1     |
  | config.Files(config.Layer("vault", vault), config.LayerOrder("vault", config.LayerBase, config.LayerEnv)) | ("from-env", true)   | 1     |
  | config.Files(config.Layer("vault", vault), config.LayerOrder(config.LayerBase, "vault"))                 | ("from-vault", true) | 1     |
  | config.Files(config.LayerOrder(config.LayerBase))                                                        | ("from-file", true)  | 0     |
  | config.Files()                                                                                           | ("from-env", true)   | 0     |
```

#### Scenario: A failing added layer
```gherkin
Given vault := a LayerSource whose Load returns errVault, errors.New("vault sealed: token s3cret")
When the consumer loads config.Files(config.Layer("vault", vault))
Then Load returns an error matching errVault with errors.Is
And the config module's own text in that error names the layer "vault" and not "s3cret"
```

<!-- cases: 1 default: no added layer row -->
<!-- cases: 2 overrides: Layer and LayerOrder rows, including omitting env -->
<!-- cases: 3 zero values: Layer(name, nil), an empty name, a built-in name, a repeated name and an unknown LayerOrder name are "Options are checked when Load runs" -->
<!-- cases: 4 boundaries: zero, one added layer; first and last positions -->
<!-- cases: 5 errors: a failing layer's error is wrapped; the root reports its type only (config-sources) -->
<!-- cases: N/A 6 duplicate: a repeated layer name is an option error -->
<!-- cases: 7 lifecycle: captured at Load; re-read on reload is specified by config-reload -->
<!-- cases: N/A 8 concurrency: covered by "Reads during Load see one snapshot" -->
<!-- cases: N/A 9 panics: the module does not recover a layer's panic; the root recovers it as ErrConfigLoad -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Keys lists every key under a prefix
`Keys(prefix)` SHALL return, after precedence and byte-wise sorted, every key equal to `prefix` or under it (`prefix.` or `prefix[`), each once, comparing in uniform form and spelling each result with the caller's prefix. An empty prefix SHALL return every key; before the first `Load` it SHALL return an empty list.

#### Scenario: Keys across layers
```gherkin
Given config.yaml holding server.port: 8080 and server.labels: {team: core}
And APP_SERVER_HOST=h, APP_SERVERS_0=x and APP_SERVER_ALLOWEDHOSTS_0=z are set (t.Setenv)
When the consumer loads config.Files() and calls Keys("<prefix>")
Then the result is <keys>

Examples:
  | prefix                 | keys                                                                                   |
  | server                 | ["server.allowedhosts[0]", "server.host", "server.labels.team", "server.port"]          |
  | server.labels          | ["server.labels.team"]                                                                 |
  | server.allowed-hosts   | ["server.allowed-hosts[0]"]                                                            |
  | servers                | ["servers[0]"]                                                                         |
  | missing                | []                                                                                     |
```

<!-- cases: 1 default: env and file keys together -->
<!-- cases: N/A 2 overrides: Keys has no options -->
<!-- cases: 3 zero values: empty prefix; before Load -->
<!-- cases: 4 boundaries: "server" never matches "servers"; byte-wise order puts "[10]" before "[2]" (documented) -->
<!-- cases: N/A 5 errors: Keys never fails -->
<!-- cases: 6 duplicate: a key in two layers is listed once -->
<!-- cases: 7 lifecycle: before Load returns [] -->
<!-- cases: 8 concurrency: covered by "Reads during Load see one snapshot" -->
<!-- cases: N/A 9 panics: Keys does not panic -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Load errors carry no values
Every `Load` failure caused by file content SHALL be a `*config.ParseError{File, Line, Category}` whose text is `<file>:<line>: <category>` (or `<file>: <category>` when the line is 0). It SHALL NOT wrap the parser's error or keep any source text, so no value reaches its message, `%#v`, or `errors.As` targets.

#### Scenario: A syntax error near a secret
```gherkin
Given a temp directory holding <file> whose line <line> is <content> containing "s3cret"
When the consumer loads config.Files(config.File("<file>"))
Then err.Error() is "<message>"
And errors.As(err, &pe) holds with pe.File "<file>" and pe.Category "syntax error"
And neither err.Error(), fmt.Sprintf("%#v", err) nor errors.Unwrap(err) contains "s3cret"

Examples:
  | file     | line | content                       | message                        |
  | a.yaml   | 3    | password: "s3cret             | a.yaml:3: syntax error         |
  | a.toml   | 2    | password = "s3cret\q"         | a.toml:2: syntax error         |
  | a.json   | 1    | {"password": "s3cret" "x": 1} | a.json:1: syntax error         |
  | a.yaml   | 0    | a: "s3cret\q"                 | a.yaml: syntax error           |
```

<!-- cases: N/A 1 default, 2 overrides: secrecy is not configurable (same guarantee as extension-properties) -->
<!-- cases: 3 zero values: line 0 row when the parser gives no position -->
<!-- cases: 4 boundaries: first line, later line -->
<!-- cases: 5 errors: every row; categories are listed in "Invalid file shapes fail the load" and ".env syntax" -->
<!-- cases: N/A 6-8: Load only -->
<!-- cases: 9 panics: FuzzFiles_YAML, FuzzFiles_TOML and FuzzFiles_DotEnv plant a high-entropy marker in value positions and assert it never appears in an error -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Files over the size limit are refused
Every file, `.env` included, SHALL be read through a limit, `config.MaxFileSize(n)` bytes, 4 MiB by default. A larger file SHALL fail `Load` matching `config.ErrFileTooLarge` and naming the file.

#### Scenario: Size limit
```gherkin
Given a temp directory holding config.yaml of <size>
When the consumer loads config.Files(config.Dir("d")<extra>)
Then Load returns <error>

Examples:
  | size        | extra                         | error                                                |
  | 4 MiB       |                               | nil                                                  |
  | 4 MiB + 1 B |                               | an error matching config.ErrFileTooLarge naming config.yaml |
  | 2 KiB       | , config.MaxFileSize(1024)    | an error matching config.ErrFileTooLarge             |
  | 8 MiB       | , config.MaxFileSize(16 << 20) | nil                                                 |
```

<!-- cases: 1 default: 4 MiB rows -->
<!-- cases: 2 overrides: MaxFileSize rows -->
<!-- cases: 3 zero values: MaxFileSize(0) means the default; a negative size is "Options are checked when Load runs" -->
<!-- cases: 4 boundaries: exactly at and one past the limit -->
<!-- cases: 5 errors: ErrFileTooLarge -->
<!-- cases: N/A 6-8 -->
<!-- cases: N/A 9 panics -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Options are checked when Load runs
An invalid option SHALL fail `Load` matching `config.ErrInvalidOption` and naming the option: `Dir()` with no path, an unknown `FileFormat`, `Codec` with a nil decoder or an extension without `.`, `ProfileEnv("")`, `Layer` with a nil source, an empty, repeated or built-in name, `LayerOrder` with an unknown or repeated name, and a negative `MaxFileSize` or `PollInterval`. `Logger(nil)` and `EnvMapper(nil)` SHALL mean unset.

#### Scenario: Invalid options
```gherkin
Given a temp working directory (t.Chdir)
When the consumer loads config.Files(<option>)
Then Load returns an error matching config.ErrInvalidOption whose message names "<named>"

Examples:
  | option                                         | named        |
  | config.Dir()                                   | Dir          |
  | config.Format("ini")                           | Format       |
  | config.Codec(".hcl", nil)                      | Codec        |
  | config.Codec("hcl", hclDecoder)                | Codec        |
  | config.ProfileEnv("")                          | ProfileEnv   |
  | config.Layer("vault", nil)                     | Layer        |
  | config.Layer("", vault)                        | Layer        |
  | config.Layer("env", vault)                     | Layer        |
  | config.LayerOrder(config.LayerBase, "nope")    | LayerOrder   |
  | config.LayerOrder(config.LayerEnv, config.LayerEnv) | LayerOrder |
  | config.MaxFileSize(-1)                         | MaxFileSize  |
```

<!-- cases: N/A 1 default: no invalid option by default -->
<!-- cases: 2 overrides: every row -->
<!-- cases: 3 zero values: Logger(nil) and EnvMapper(nil) mean unset; MaxFileSize(0) means default -->
<!-- cases: 4 boundaries: built-in layer name collision -->
<!-- cases: 5 errors: every row matches ErrInvalidOption -->
<!-- cases: 6 duplicate: repeated layer names -->
<!-- cases: N/A 7-9: options are plain data checked in Load -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Load honors its context
`Load(ctx)` SHALL stop and return an error matching `ctx.Err()` when the context ends before loading finishes, and the source SHALL keep answering from the snapshot it held before that `Load`.

#### Scenario: A cancelled load keeps the previous snapshot
```gherkin
Given a source config.Files(config.File("config.yaml"), config.Layer("slow", slow)) already loaded once with server.port: 8080
And config.yaml now holds server.port: 9090 and slow's Load blocks until its context ends
When the consumer calls Load(ctx) with ctx cancelled once slow's Load has started (channel handshake)
Then the error matches context.Canceled with errors.Is
And Lookup("server.port") returns ("8080", true)
```

<!-- cases: N/A 1 default, 2 overrides: the timeout belongs to the container (config-sources) -->
<!-- cases: 3 zero values: an already-cancelled context returns before reading any file -->
<!-- cases: 4 boundaries: a deadline matches context.DeadlineExceeded -->
<!-- cases: 5 errors: the error matches ctx.Err() -->
<!-- cases: N/A 6 duplicate -->
<!-- cases: 7 lifecycle: a failed Load keeps the previous snapshot -->
<!-- cases: 8 concurrency: cancellation from another goroutine; no goroutine outlives Load (goleak) -->
<!-- cases: N/A 9 panics -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Loads are serialized and stop once watching starts
Concurrent `Load` calls SHALL run one at a time, so the snapshot kept is the one produced by the last `Load` to start. After `Watch` has started, `Load` SHALL return an error matching `config.ErrInvalidOption` and change nothing; reloads then happen only through `Watch`.

#### Scenario: Two overlapping Loads
```gherkin
Given a loaded source whose config.yaml holds v: 1, and slow, an added layer whose first reload blocks on a channel
And config.yaml is rewritten to v: 2 after Load A has started and before Load B starts
When the consumer releases the channel after Load B has been called
Then Lookup("v") returns ("2", true)
```

#### Scenario: Load while watching
```gherkin
Given a source whose Watch is running (synctest bubble)
When the consumer calls Load(ctx)
Then the error matches config.ErrInvalidOption and Lookup answers from the same snapshot as before
```

<!-- cases: N/A 1 default, 2 overrides -->
<!-- cases: N/A 3 zero values -->
<!-- cases: 4 boundaries: before and after Watch starts -->
<!-- cases: 5 errors: Load while watching -->
<!-- cases: N/A 6 duplicate -->
<!-- cases: 7 lifecycle: Load, then Watch -->
<!-- cases: 8 concurrency: overlapping Loads, under -race -->
<!-- cases: N/A 9 panics, 10 compatibility -->

### Requirement: Reads during Load see one snapshot
`Lookup` and `Keys` SHALL be safe to call from many goroutines while `Load` runs. Each single call SHALL answer from one complete snapshot, the one before `Load` or the one it produced.

#### Scenario: Concurrent reads while loading
```gherkin
Given a loaded config.Files(config.File("config.yaml")) whose file holds a.x: 1 and a.y: 1
And the file is rewritten to a.x: 2 and a.y: 2
When 50 goroutines call Lookup("a.x") and Keys("a") in a loop while the consumer calls Load(ctx) 20 times
Then every Lookup returned ("1", true) or ("2", true)
And every Keys returned ["a.x", "a.y"]
And go test -race reports no data race
```

<!-- cases: N/A 1-7: concurrency only -->
<!-- cases: 8 concurrency: the subject; runs under -race (spike 3 RED test) -->
<!-- cases: N/A 9 panics, 10 compatibility -->

### Requirement: Where the source logs
The source SHALL log through the function given to `SetLogf` when the container called it, else through `config.Logger(l)`, else through `slog.Default()` looked up at each log call. It SHALL never log a value.

#### Scenario: Log sink
```gherkin
Given a config.Files(config.DotEnvExport()<option>) source whose Load logs one skipped-variable line
And slog.SetDefault set to a recording handler after the source was built (t.Cleanup restores it; test not parallel)
And <setlogf>
When the consumer calls Load(ctx)
Then the line was recorded by <sink>

Examples:
  | option               | setlogf            | sink           |
  |                      | SetLogf not called | slog.Default() |
  | , config.Logger(l)   | SetLogf not called | l              |
  | , config.Logger(l)   | SetLogf(f) called  | f              |
  | , config.Logger(nil) | SetLogf not called | slog.Default() |
```

<!-- cases: 1 default: slog.Default row -->
<!-- cases: 2 overrides: Logger and SetLogf -->
<!-- cases: 3 zero values: Logger(nil) means unset -->
<!-- cases: 4 boundaries: slog.Default replaced after the source was built still receives the line -->
<!-- cases: N/A 5 errors: logging cannot fail -->
<!-- cases: N/A 6 duplicate: the last Logger wins -->
<!-- cases: N/A 7-9 -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Structs bind end to end through config.Files
A container whose `ConfigSource` is `config.Files(...)` SHALL bind a struct field from env even when no file holds its key, and SHALL bind a field from every file spelling of its name, so file and env layers reach the binder as one source.

#### Scenario: An env-only key binds although the file lacks it
```gherkin
Given a directory holding config.yaml with "service:\n  name: demo\n"
And t.Setenv("APP_SERVICE_VERSION", "9.9.9")
And root, err := doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: config.Files(config.Dir(dir), config.DotEnv(""))}) returned a nil err
When the consumer calls doger.ResolveConfig(root, "service", ServiceConfig{})
Then the result is ServiceConfig{Name: "demo", Version: "9.9.9"} and the error is nil
```

#### Scenario: Every file spelling binds the same field
```gherkin
Given a directory holding config.yaml with "server:\n  <spelling>: https://kartala.id\n"
And root, err := doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: config.Files(config.Dir(dir), config.DotEnv(""))}) returned a nil err
When the consumer calls doger.ResolveConfig(root, "server", struct{ IssuerURI string }{})
Then the result's IssuerURI is "https://kartala.id" and the error is nil

Examples:
  | spelling   |
  | issuer-uri |
  | issuerUri  |
  | issuer_uri |
  | issueruri  |
```

<!-- cases: 1 default: zero-config Files over one directory -->
<!-- cases: N/A 2 overrides: binding rules are config-binding's; this checks the two modules together -->
<!-- cases: 3 zero values: a key held by no layer keeps the default (config-binding "Presence decides") -->
<!-- cases: 4 boundaries: env-only versus file-only fields in one struct -->
<!-- cases: N/A 5 errors: conversion errors are config-binding's -->
<!-- cases: N/A 6 duplicate: keys that normalize together fail the load ("Invalid file shapes fail the load") -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: covered by the binding and reload specs -->
<!-- cases: 10 compatibility: spike 4 of golang-lib/config, where an env var for a key missing from the file was ignored -->

### Requirement: The module satisfies the root contract and stays small
`*config.Source` SHALL satisfy `doger.ConfigSource`, `doger.ConfigLogger` and `doger.ConfigWatcher` structurally and SHALL NOT implement `doger.ConfigValidator`. The config package's non-test code SHALL NOT import doger and SHALL depend on no module other than `go.yaml.in/yaml/v3` and `github.com/pelletier/go-toml/v2`. Watching is specified by `config-reload`.

#### Scenario: Compile-time contract checks
```gherkin
Given the config module's test file declaring var _ doger.ConfigSource, var _ doger.ConfigLogger and var _ doger.ConfigWatcher all equal to (*config.Source)(nil)
When the consumer type-asserts any((*config.Source)(nil)) to doger.ConfigValidator
Then the declarations compile and the assertion's ok is false
```

#### Scenario: Dependency footprint
```gherkin
Given the CI job for the config module
When it runs go list -deps . in config/ without test files
Then every non-stdlib package path starts with "github.com/kartaladev/doger/config", "go.yaml.in/yaml/v3" or "github.com/pelletier/go-toml/v2"
```

<!-- cases: N/A 1-9: build-level guarantees -->
<!-- cases: 10 compatibility: golang-lib/config is untouched; this module is separate -->
