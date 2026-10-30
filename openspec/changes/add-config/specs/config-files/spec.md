# Spec Delta

## Purpose

The `github.com/kartaladev/doger/config` module's `config.Files` source: it finds config files, layers them with `.env`, process env and added sources into one immutable snapshot, and answers `Lookup` and `Keys` for the container. It does this without viper, without writing the process environment by default, and without ever putting a value in an error.

## ADDED Requirements

### Requirement: Zero-config file discovery
`config.Files()` with no options SHALL search `.` and then `./config` for a base file named `config` with the extension `.yaml`, `.yml`, `.toml` or `.json`. Every file found SHALL load, and `./config` SHALL win over `.` key by key. Finding no file at all SHALL NOT be an error.

#### Scenario: Base files found in the working directory and ./config
```gherkin
Given a temp working directory (t.Chdir, test not parallel) containing <files>
And no APP_* variable (clearEnvPrefix(t, "APP_")) and no .env
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
Given a temp working directory (t.Chdir, clearEnvPrefix(t, "APP_")) with config.yaml holding server.host: "a" and server.port: 8080
And config/config.yaml holding server.port: 8181
When the consumer calls config.Files().Load(ctx)
Then Lookup("server.host") returns ("a", true) and Lookup("server.port") returns ("8181", true)
```

<!-- cases: 1 default: the whole requirement -->
<!-- cases: 2 overrides: explicit paths are "Explicit paths skip discovery" -->
<!-- cases: 3 zero values: an empty file and no file at all -->
<!-- cases: 4 boundaries: one directory, both directories -->
<!-- cases: 5 errors: ambiguous formats are "One format per base name" -->
<!-- cases: 6 duplicate: a key in both directories (./config wins) -->
<!-- cases: N/A 7 lifecycle: discovery runs in Load, covered by "Paths are resolved at the first Load" -->
<!-- cases: 8 concurrency: covered by "Reads during Load see one snapshot" -->
<!-- cases: N/A 9 panics: Load returns errors; parsers are fuzzed under "Load errors carry no values" -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Discovery order across directories and profiles
Files SHALL load from lowest to highest as: base files in search order, then profile files in search order, so `./config.yaml` < `./config/config.yaml` < `./config.prod.yaml` < `./config/config.prod.yaml`. A list SHALL come whole from the highest file that holds it; lists never merge by index across files.

#### Scenario: Four files and a split list
```gherkin
Given a temp working directory (t.Chdir, clearEnvPrefix(t, "APP_")) with APP_PROFILE=prod and:
  | file                    | content                                      |
  | config.yaml             | a: base-dot,  b: base-dot,  hosts: [1, 2, 3] |
  | config/config.yaml      | a: base-cfg,  b: base-cfg,  hosts: [4]       |
  | config.prod.yaml        | a: prod-dot                                  |
  | config/config.prod.yaml | c: prod-cfg                                  |
When the consumer calls config.Files().Load(ctx)
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
<!-- cases: N/A 3 zero values: an empty or missing file is covered by discovery -->
<!-- cases: 4 boundaries: every adjacent pair of the four files; a list shorter in the higher file -->
<!-- cases: N/A 5 errors: ordering itself cannot fail -->
<!-- cases: 6 duplicate: key a in three files -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: Load only; covered by the Load requirements -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: One format per base name
Within one directory, two files with the same base name and different extensions, `.yaml` and `.yml` included, SHALL fail `Load` with an error matching `config.ErrAmbiguousFormat` that names both files. `config.Format(f)` with a `config.FileFormat` (`config.YAML`, `config.TOML`, `config.JSON`) SHALL restrict discovery to that format; `Format("")` SHALL mean "by extension".

#### Scenario: Two formats for one base name
```gherkin
Given a temp working directory (t.Chdir, clearEnvPrefix(t, "APP_")) with <files>
When the consumer calls <source>.Load(ctx)
Then Load returns <result>

Examples:
  | files                     | source                                   | result                                                                          |
  | config.yaml, config.toml  | config.Files()                           | an error matching config.ErrAmbiguousFormat naming config.yaml and config.toml |
  | config.yaml, config.yml   | config.Files()                           | an error matching config.ErrAmbiguousFormat naming config.yaml and config.yml  |
  | config.yaml, config.toml  | config.Files(config.Format(config.TOML)) | nil, and only config.toml was read                                            |
  | config.yaml, config.toml  | config.Files(config.Format(config.YAML)) | nil, and only config.yaml was read                                            |
  | config.yaml, config.yml   | config.Files(config.Format(config.YAML)) | an error matching config.ErrAmbiguousFormat                                   |
  | config.yaml, config.toml  | config.Files(config.Format(""))          | an error matching config.ErrAmbiguousFormat                                   |
```

<!-- cases: 1 default: the first two rows -->
<!-- cases: 2 overrides: Format rows -->
<!-- cases: 3 zero values: Format("") row; an unknown FileFormat is "Options are checked when Load runs" -->
<!-- cases: 4 boundaries: .yaml with .yml under Format(YAML) is still two files -->
<!-- cases: 5 errors: ErrAmbiguousFormat matchable with errors.Is -->
<!-- cases: N/A 6 duplicate: the duplicate is the subject -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Profile overlay
The profile SHALL come from `config.Profile(p)`, else from the process variable named by `config.ProfileEnv(name)` (default `APP_PROFILE`), never from `.env`; that variable SHALL NOT become a config key. `config.<profile>.<ext>` SHALL load over the base files. `Profile("")` SHALL mean no profile. A set profile with no file in any directory SHALL fail `Load` matching `config.ErrProfileNotFound`. A profile file with no base file SHALL load alone.

#### Scenario: Profile selection
```gherkin
Given a temp working directory (t.Chdir, clearEnvPrefix(t, "APP_")) with config.yaml holding server.port: 8080 and config.prod.yaml holding server.port: 9090
And <env> (t.Setenv)
When the consumer calls <source>.Load(ctx)
Then Load returns <error> and Lookup("server.port") returns <result>

Examples:
  | env               | source                                    | error                                    | result         |
  | nothing set       | config.Files()                            | nil                                      | ("8080", true) |
  | APP_PROFILE=prod  | config.Files()                            | nil                                      | ("9090", true) |
  | APP_PROFILE=prod  | config.Files(config.Profile(""))          | nil                                      | ("8080", true) |
  | APP_PROFILE=prod  | config.Files(config.Profile("dev"))       | config.ErrProfileNotFound naming "dev"   | ("", false)    |
  | nothing set       | config.Files(config.Profile("prod"))      | nil                                      | ("9090", true) |
  | MY_ENV=prod       | config.Files(config.ProfileEnv("MY_ENV")) | nil                                      | ("9090", true) |
  | APP_PROFILE=stage | config.Files()                            | config.ErrProfileNotFound naming "stage" | ("", false)    |
  | APP_PROFILE=      | config.Files()                            | nil                                      | ("8080", true) |
```

#### Scenario: Profile files in one directory or without a base
```gherkin
Given a temp working directory (t.Chdir, clearEnvPrefix(t, "APP_")) holding <files>
When the consumer calls config.Files(config.Profile("prod")).Load(ctx)
Then Lookup("server.port") returns <result>

Examples:
  | files                                                                            | result         |
  | only config.prod.yaml with server.port: 9090                                     | ("9090", true) |
  | config.yaml with server.port: 8080 and config/config.prod.yaml with server.port: 9191 | ("9191", true) |
```

#### Scenario: The profile variable is never a config key and never read from .env
```gherkin
Given a temp working directory (t.Chdir, clearEnvPrefix(t, "APP_")) with config.yaml (server.port: 8080), config.prod.yaml (server.port: 9090) and <setup>
When the consumer calls config.Files().Load(ctx)
Then Lookup("server.port") returns <port>, Lookup("profile") returns ("", false) and Keys("") contains no "profile"

Examples:
  | setup                                        | port           |
  | process APP_PROFILE=prod                     | ("9090", true) |
  | .env holding APP_PROFILE=prod, process unset | ("8080", true) |
```

<!-- cases: 1 default: APP_PROFILE rows -->
<!-- cases: 2 overrides: Profile beats APP_PROFILE; Profile("") turns it off; ProfileEnv renames the variable -->
<!-- cases: 3 zero values: an empty APP_PROFILE means no profile; ProfileEnv("") is "Options are checked when Load runs" -->
<!-- cases: 4 boundaries: profile without base; profile file in one directory only -->
<!-- cases: 5 errors: ErrProfileNotFound; invalid names are "Profile names are restricted" -->
<!-- cases: N/A 6 duplicate: one profile at a time -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: Load only -->
<!-- cases: N/A 10 compatibility: new module; golang-lib's "dev" default is in the adoption guide -->

### Requirement: Profile names are restricted
A profile name SHALL match `^[a-z0-9][a-z0-9_-]*$`; any other name SHALL fail `Load` matching `config.ErrInvalidOption`. Because a valid name holds no `/` or `.`, a profile file can never lie outside the searched directories.

#### Scenario: Profile names
```gherkin
Given a temp working directory (t.Chdir, clearEnvPrefix(t, "APP_")) with config.yaml and config.<name>.yaml for every valid name below
When the consumer calls config.Files(config.Profile("<name>")).Load(ctx)
Then Load returns <error>

Examples:
  | name         | error                                     |
  | prod         | nil                                       |
  | qa_1         | nil                                       |
  | eu-west-1    | nil                                       |
  | ../x         | an error matching config.ErrInvalidOption |
  | prod/../../x | an error matching config.ErrInvalidOption |
  | Prod         | an error matching config.ErrInvalidOption |
  | -x           | an error matching config.ErrInvalidOption |
```

<!-- cases: N/A 1 default, 2 overrides: the rule is not configurable on purpose (it is the traversal guard) -->
<!-- cases: 3 zero values: Profile("") is "no profile" -->
<!-- cases: 4 boundaries: leading digit, underscore, dash; uppercase rejected -->
<!-- cases: 5 errors: ErrInvalidOption rows -->
<!-- cases: N/A 6 duplicate, 7 lifecycle, 8 concurrency: a name is checked once per Load -->
<!-- cases: N/A 9 panics: validation returns an error -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Symlinks are followed
Files SHALL be opened by path with symlinks followed, inside or outside their directory, so Kubernetes ConfigMap `..data` links and Docker or Kubernetes secrets mounted elsewhere load as if they were regular files.

#### Scenario: Symlinked config files
```gherkin
Given a temp directory d holding config.yaml as <link>, where every target holds server.port: 8080 (test skipped on Windows)
When the consumer calls config.Files(config.Dir("d")).Load(ctx)
Then Load returns nil and Lookup("server.port") returns ("8080", true)

Examples:
  | link                                                           |
  | a relative link config.yaml -> ..data/config.yaml, ..data -> ..v1 inside d |
  | an absolute link to a file in another temp directory           |
```

<!-- cases: 1 default: symlinks are always followed -->
<!-- cases: N/A 2 overrides: there is no containment option; profile names are restricted instead -->
<!-- cases: N/A 3 zero values: a dangling link is a missing file ("Config errors are typed and value-free") -->
<!-- cases: 4 boundaries: relative inside link, absolute outside link -->
<!-- cases: N/A 5 errors: a link to a non-regular file is "Files over the size limit and non-regular files are refused" -->
<!-- cases: N/A 6 duplicate, 7 lifecycle, 8 concurrency, 9 panics: plain file reads -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Explicit paths skip discovery
`config.Dir(paths...)` SHALL replace the searched directories, all loading and the later winning; a directory that does not exist SHALL fail `Load` matching `fs.ErrNotExist`. `config.File(paths...)` SHALL load exactly those files, the later winning; a missing file SHALL fail `Load` matching `fs.ErrNotExist`. `config.Name(base)` SHALL change the base name used by discovery.

#### Scenario: Directories, files and base name
```gherkin
Given temp directories a/ with config.yaml (server.port: 1), b/ with config.yaml (server.port: 2), empty/ with no file, a file c/app.toml with [server] port = 3, and d/myapp.yaml with server.port: 4 (clearEnvPrefix(t, "APP_"))
When the consumer calls <source>.Load(ctx)
Then Load returns <error> and Lookup("server.port") returns <result>

Examples:
  | source                                              | error                                                    | result      |
  | config.Files(config.Dir("a"))                       | nil                                                      | ("1", true) |
  | config.Files(config.Dir("a", "b"))                  | nil                                                      | ("2", true) |
  | config.Files(config.Dir("b", "a"))                  | nil                                                      | ("1", true) |
  | config.Files(config.Dir("a", "a"))                  | nil                                                      | ("1", true) |
  | config.Files(config.Dir("empty"))                   | nil                                                      | ("", false) |
  | config.Files(config.Dir("nope"))                    | an error matching fs.ErrNotExist naming "nope"           | ("", false) |
  | config.Files(config.File("c/app.toml"))             | nil                                                      | ("3", true) |
  | config.Files(config.File("c/missing.yaml"))         | an error matching fs.ErrNotExist naming "c/missing.yaml" | ("", false) |
  | config.Files(config.Dir("d"), config.Name("myapp")) | nil                                                      | ("4", true) |
```

<!-- cases: N/A 1 default: discovery is the previous requirements -->
<!-- cases: 2 overrides: every row -->
<!-- cases: 3 zero values: an empty directory; Dir(), Dir("") and File() are "Options are checked when Load runs" -->
<!-- cases: 4 boundaries: one and two directories, both orders -->
<!-- cases: 5 errors: missing directory and missing file match fs.ErrNotExist -->
<!-- cases: 6 duplicate: the same directory twice behaves as once -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Explicit files ignore the profile and force a format
With `config.File(...)`, discovery and the profile overlay SHALL be off, so an inherited `APP_PROFILE` is ignored without error. `config.Dir` and `config.Name` keep the profile overlay. `config.Dir` together with `config.File` SHALL fail `Load` matching `config.ErrInvalidOption`. `config.Format(f)` with `config.File` SHALL parse every listed file as `f`, whatever its extension.

#### Scenario: File, Dir, Format and the profile together
```gherkin
Given a temp working directory (t.Chdir, clearEnvPrefix(t, "APP_")) with APP_PROFILE=prod set, config.yaml (server.port: 8080), config.prod.yaml (server.port: 9090) and app.conf holding YAML server.port: 7070
When the consumer calls <source>.Load(ctx)
Then Load returns <error> and Lookup("server.port") returns <result>

Examples:
  | source                                                         | error                                     | result         |
  | config.Files(config.File("config.yaml"))                       | nil                                       | ("8080", true) |
  | config.Files(config.Dir("."))                                  | nil                                       | ("9090", true) |
  | config.Files(config.Dir("."), config.File("config.yaml"))      | an error matching config.ErrInvalidOption | ("", false)    |
  | config.Files(config.File("app.conf"), config.Format(config.YAML)) | nil                                    | ("7070", true) |
  | config.Files(config.File("app.conf"))                          | an error naming "app.conf" (no decoder)   | ("", false)    |
```

<!-- cases: N/A 1 default: discovery is the default -->
<!-- cases: 2 overrides: every row -->
<!-- cases: N/A 3 zero values: covered by "Options are checked when Load runs" -->
<!-- cases: 4 boundaries: File with and without Format -->
<!-- cases: 5 errors: Dir with File; an unknown extension without Format -->
<!-- cases: N/A 6 duplicate, 7 lifecycle, 8 concurrency, 9 panics: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Paths are resolved at the first Load
Relative directories and files, the discovery directories `.` and `./config`, and the `.env` path SHALL be resolved against the working directory once, at the first `Load`, and later loads and polls SHALL use those absolute paths.

#### Scenario: A later working-directory change is not followed
```gherkin
Given temp directories x/ with config.yaml (server.port: 1) and y/ with config.yaml (server.port: 2)
And src := config.Files() loaded once after t.Chdir("x") (test not parallel)
And the test then calls t.Chdir("y")
When the consumer calls src.Load(ctx) again
Then Lookup("server.port") returns ("1", true)
```

<!-- cases: 1 default: discovery directories -->
<!-- cases: N/A 2 overrides: absolute paths are already absolute -->
<!-- cases: N/A 3 zero values, 4 boundaries: one rule -->
<!-- cases: N/A 5 errors: resolving a path does not fail -->
<!-- cases: N/A 6 duplicate -->
<!-- cases: 7 lifecycle: resolved at the first Load; polls reuse them (config-reload) -->
<!-- cases: N/A 8 concurrency, 9 panics -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Formats are chosen by extension
Files SHALL be parsed by extension, compared case-insensitively: YAML for `.yaml` and `.yml`, TOML for `.toml`, JSON for `.json`. `config.Codec(ext, d)`, with `ext` starting with `.` and `d` a `config.Decoder`, SHALL add or replace the decoder for exactly that extension (the last call wins) and SHALL join discovery and the one-format check. A file whose extension has no decoder SHALL fail `Load` naming the file.

#### Scenario: Built-in and custom decoders
```gherkin
Given a temp working directory (t.Chdir, clearEnvPrefix(t, "APP_")) holding <file> with server.port: 8080 in that file's format
When the consumer calls config.Files(<options>).Load(ctx)
Then Load returns <error> and Lookup("server.port") returns <result>

Examples:
  | file        | options                                                                      | error                                                       | result               |
  | app.yaml    | config.File("app.yaml")                                                      | nil                                                         | ("8080", true)       |
  | app.YML     | config.File("app.YML")                                                       | nil                                                         | ("8080", true)       |
  | app.json    | config.File("app.json")                                                      | nil                                                         | ("8080", true)       |
  | app.hcl     | config.File("app.hcl")                                                       | an error naming "app.hcl"                                   | ("", false)          |
  | app.hcl     | config.File("app.hcl"), config.Codec(".hcl", hclDecoder)                     | nil                                                         | ("8080", true)       |
  | app.hcl     | config.File("app.hcl"), config.Codec(".hcl", failing), config.Codec(".hcl", hclDecoder) | nil                                              | ("8080", true)       |
  | app.yaml    | config.File("app.yaml"), config.Codec(".yaml", upperDecoder)                 | nil                                                         | ("8080-upper", true) |
  | app.yml     | config.File("app.yml"), config.Codec(".yaml", upperDecoder)                  | nil                                                         | ("8080", true)       |
  | app.hcl     | config.File("app.hcl"), config.Codec(".hcl", failing)                        | a *config.ParseError for "app.hcl", line 0, "syntax error"  | ("", false)          |
  | config.hcl  | config.Codec(".hcl", hclDecoder)                                             | nil                                                         | ("8080", true)       |
```

<!-- cases: 1 default: built-in rows -->
<!-- cases: 2 overrides: Codec add, replace, discovery rows -->
<!-- cases: 3 zero values: Codec with a nil decoder or an ext without "." is "Options are checked when Load runs" -->
<!-- cases: 4 boundaries: upper-case extension; .yaml codec does not cover .yml -->
<!-- cases: 5 errors: unknown extension; a decoder error becomes a *config.ParseError whose text never holds the decoder's message -->
<!-- cases: 6 duplicate: the last Codec for one extension wins -->
<!-- cases: N/A 7 lifecycle, 8 concurrency: Load only -->
<!-- cases: N/A 9 panics: a panicking decoder is recovered by the root as ErrConfigLoadFailed (config-sources) -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Decoder values are rendered by Go type
`config.Decoder` is `interface{ Decode(data []byte) (map[string]any, error) }`. Its result SHALL flatten like a file: nested `map[string]any` and `[]any`, `nil` absent. Scalars SHALL render as: integers in decimal, `float64` with `strconv.FormatFloat(f, 'g', -1, 64)`, `bool` as `true`/`false`, `string` as is, `time.Time` in RFC 3339, `json.Number` literally. A `map[any]any` SHALL fail with category "not a mapping"; any other type with "unsupported value".

#### Scenario: Rendering a decoder's values
```gherkin
Given a decoder for ".x" whose Decode returns map[string]any{"v": <value>}
And a temp directory holding a.x
When the consumer calls config.Files(config.File("a.x"), config.Codec(".x", decoder)).Load(ctx)
Then Load returns <error> and Lookup("v") returns <result>

Examples:
  | value                                         | error                                             | result                          |
  | int64(-42)                                    | nil                                               | ("-42", true)                   |
  | uint64(18446744073709551615)                  | nil                                               | ("18446744073709551615", true)  |
  | 0.5                                           | nil                                               | ("0.5", true)                   |
  | true                                          | nil                                               | ("true", true)                  |
  | time.Date(1979, 5, 27, 7, 32, 0, 0, time.UTC) | nil                                               | ("1979-05-27T07:32:00Z", true)  |
  | json.Number("1e3")                            | nil                                               | ("1e3", true)                   |
  | nil                                           | nil                                               | ("", false)                     |
  | map[any]any{"k": 1}                           | a *config.ParseError for "a.x", "not a mapping"   | ("", false)                     |
  | struct{}{}                                    | a *config.ParseError for "a.x", "unsupported value" | ("", false)                   |
```

<!-- cases: 1 default: every built-in Go type -->
<!-- cases: N/A 2 overrides: rendering is fixed so binding is format-independent -->
<!-- cases: 3 zero values: nil is absent -->
<!-- cases: 4 boundaries: max uint64, negative int -->
<!-- cases: 5 errors: map[any]any and unsupported types -->
<!-- cases: N/A 6 duplicate: a Go map has unique keys -->
<!-- cases: N/A 7 lifecycle, 8 concurrency: Load only -->
<!-- cases: N/A 9 panics: see "Formats are chosen by extension" -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: File keys become canonical keys
A whole key written as `"[...]"` SHALL be kept verbatim as a map key, checked before splitting. Other keys SHALL split on `.`; `/` SHALL mark an extension instance and is allowed only in the first segment; each segment SHALL be normalized to lowercase kebab by the field-naming rule (config-binding). Nested maps SHALL flatten to dotted keys, sequences to `[i]` (nested to `[i][j]`), an all-digit key to an index, and `null` SHALL mean absent.

#### Scenario: Flattening and normalization
```gherkin
Given config.yaml holding:
  server:
    issuerUri: https://kartala.id
    read_timeout: 5s
    hosts: [10.0.0.1, 10.0.0.2]
    matrix: [[1, 2], [3]]
    ports: {8080: http}
    labels:
      "[team/core]": payments
      "[X-Request-ID]": abc
      "[App.Kartala/Tier]": web
      tier: web
    gone: null
    empty-list: []
    empty-map: {}
  redis/queue:
    addr: q:6379
And clearEnvPrefix(t, "APP_")
When the consumer calls config.Files(config.File("config.yaml")).Load(ctx)
Then Lookup("<key>") returns <result>

Examples:
  | key                              | result                       |
  | server.issuer-uri                | ("https://kartala.id", true) |
  | server.read-timeout              | ("5s", true)                 |
  | server.hosts[0]                  | ("10.0.0.1", true)           |
  | server.hosts[2]                  | ("", false)                  |
  | server.matrix[0][1]              | ("2", true)                  |
  | server.matrix[1][0]              | ("3", true)                  |
  | server.ports[8080]               | ("http", true)               |
  | server.labels[team/core]         | ("payments", true)           |
  | server.labels[X-Request-ID]      | ("abc", true)                |
  | server.labels[App.Kartala/Tier]  | ("web", true)                |
  | server.labels.tier               | ("web", true)                |
  | server.gone                      | ("", false)                  |
  | server.empty-list[0]             | ("", false)                  |
  | redis/queue.addr                 | ("q:6379", true)             |
```

<!-- cases: N/A 1 default, 2 overrides: normalization is not configurable; a tag on the target struct renames a field at binding -->
<!-- cases: 3 zero values: null, an empty sequence and an empty map produce no keys -->
<!-- cases: 4 boundaries: index 0, past the end, nested sequence, all-digit key -->
<!-- cases: 5 errors: keys that cannot be normalized are "Invalid file keys fail the load" -->
<!-- cases: 6 duplicate: keys that normalize together are "Invalid file shapes fail the load" -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Invalid file keys fail the load
A file key that cannot become a canonical key SHALL fail `Load` with a `*config.ParseError` of category "invalid key" naming the file and the line: an empty key, a key with whitespace or non-ASCII letters, a leading `-`, a `/` outside the first segment, a null key, or an index suffix outside brackets.

#### Scenario: Keys that cannot be normalized
```gherkin
Given a temp directory holding a.yaml whose line <line> is <content>
When the consumer calls config.Files(config.File("a.yaml")).Load(ctx)
Then Load returns a *config.ParseError with File "a.yaml", Line <line> and Category "invalid key"

Examples:
  | line | content              |
  | 1    | "": 1                |
  | 1    | "bad key": 1         |
  | 1    | größe: 1             |
  | 1    | -a: 1                |
  | 2    | "server:\n  x/y: 1"  |
  | 1    | ~: 1                 |
  | 1    | "x[0]": 1            |
```

<!-- cases: N/A 1 default, 2 overrides: key rules are fixed -->
<!-- cases: 3 zero values: empty and null keys -->
<!-- cases: 4 boundaries: "/" in the first segment is an instance (previous requirement) versus a later segment -->
<!-- cases: 5 errors: every row; errors.As reaches *config.ParseError -->
<!-- cases: N/A 6 duplicate: see "Invalid file shapes fail the load" -->
<!-- cases: N/A 7 lifecycle, 8 concurrency: Load only -->
<!-- cases: 9 panics: FuzzFiles_YAML seeds include every row -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Invalid file shapes fail the load
`Load` SHALL fail with a `*config.ParseError` naming the file, the line where known and a `config.ParseCategory` when a file holds: two keys equal in uniform form, or one key twice in any format ("duplicate key"); a second YAML document that is not empty ("multiple documents"); a top level that is a list or scalar ("not a mapping"); aliases that expand past yaml.v3's limits or contain themselves ("excessive aliasing"); or trailing JSON data ("syntax error").

#### Scenario: Shapes that fail
```gherkin
Given a temp directory holding <file> with <content>
When the consumer calls config.Files(config.File("<file>")).Load(ctx)
Then Load returns a *config.ParseError with File "<file>", Line <line> and Category "<category>"

Examples:
  | file   | content                                       | line | category           |
  | a.yaml | logger: {log-file: x.log, logFile: y.log}     | 1    | duplicate key      |
  | a.yaml | "logfile: x\nlog-file: y"                     | 2    | duplicate key      |
  | a.yaml | "port: 1\nport: 2"                            | 2    | duplicate key      |
  | a.toml | "port = 1\nport = 2"                          | 2    | duplicate key      |
  | a.json | "{\"port\": 1,\n \"port\": 2}"                | 2    | duplicate key      |
  | a.yaml | "a: 1\n---\nb: 2"                             | 3    | multiple documents |
  | a.yaml | "a: 1\n---\n~"                                | 3    | multiple documents |
  | a.yaml | "- 1\n- 2"                                    | 1    | not a mapping      |
  | a.json | [1, 2]                                        | 1    | not a mapping      |
  | a.yaml | a "billion laughs" alias bomb of 9 levels      | 0    | excessive aliasing |
  | a.yaml | a: &x [*x]                                    | 0    | excessive aliasing |
  | a.json | {"a": 1} {"b": 2}                             | 1    | syntax error       |
```

<!-- cases: N/A 1 default, 2 overrides: shape rules are fixed -->
<!-- cases: 3 zero values: empty documents are "Empty files and documents load as no keys" -->
<!-- cases: 4 boundaries: line 0 when the parser reports no position; "---" followed by "~" is not empty -->
<!-- cases: 5 errors: every row; errors.As reaches *config.ParseError -->
<!-- cases: 6 duplicate: exact and uniform-form duplicates in every format; the error names neither value. TOML duplicate lines need github.com/pelletier/go-toml/v2 v2.4.0 or later -->
<!-- cases: N/A 7 lifecycle, 8 concurrency: Load only -->
<!-- cases: 9 panics: an alias cycle never overflows the stack; FuzzFiles_YAML seeds include the bomb and the self-alias -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Empty files and documents load as no keys
A file that is empty or holds only whitespace or comments, a top-level `null` in YAML or JSON, and a YAML file whose last document is an empty `---` (a `!!null` scalar with empty text) SHALL load as no keys without error.

#### Scenario: Empty content
```gherkin
Given a temp directory holding <file> with <content>
When the consumer calls config.Files(config.File("<file>")).Load(ctx)
Then Load returns nil and Lookup("a") returns <result>

Examples:
  | file   | content                  | result      |
  | a.yaml | ""                       | ("", false) |
  | a.yaml | "# only a comment"       | ("", false) |
  | a.json | "  \n"                   | ("", false) |
  | a.json | null                     | ("", false) |
  | a.yaml | ~                        | ("", false) |
  | a.toml | ""                       | ("", false) |
  | a.yaml | "a: 1\n---\n"            | ("1", true) |
  | a.yaml | "a: 1\n---\n# trailing" | ("1", true) |
```

<!-- cases: 1 default: the whole requirement -->
<!-- cases: N/A 2 overrides: fixed -->
<!-- cases: 3 zero values: the subject -->
<!-- cases: 4 boundaries: trailing empty document with and without a comment -->
<!-- cases: N/A 5 errors: a non-empty second document is "Invalid file shapes fail the load" -->
<!-- cases: N/A 6 duplicate, 7 lifecycle, 8 concurrency, 9 panics: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Hostile files are bounded
A file SHALL fail `Load` with a `*config.ParseError` when a flattened key would have more than 64 segments ("too deep", with the line where known) or when it yields more than 100,000 keys ("too many keys", line 0). Neither case SHALL exhaust memory or stack.

#### Scenario: Depth and key caps
```gherkin
Given a temp directory holding <file> generated as <content>
When the consumer calls config.Files(config.File("<file>")).Load(ctx)
Then Load returns <result>

Examples:
  | file   | content                                       | result                                               |
  | a.toml | one dotted key of 64 segments = 1             | nil                                                  |
  | a.toml | one dotted key of 65 segments = 1             | a *config.ParseError, Line 1, Category "too deep"    |
  | a.yaml | 65 nested mappings                            | a *config.ParseError, Category "too deep"            |
  | a.toml | a table nested 1,000,000 levels (2 MB)        | a *config.ParseError, Category "too deep"            |
  | a.yaml | 100,000 keys k0..k99999                       | nil                                                  |
  | a.yaml | 100,001 keys                                  | a *config.ParseError, Line 0, Category "too many keys" |
```

<!-- cases: N/A 1 default, 2 overrides: the caps are fixed safety limits; MaxFileSize bounds bytes -->
<!-- cases: N/A 3 zero values: covered by "Empty files and documents load as no keys" -->
<!-- cases: 4 boundaries: exactly at and one past each cap -->
<!-- cases: 5 errors: too deep, too many keys -->
<!-- cases: N/A 6 duplicate, 7 lifecycle, 8 concurrency: Load only -->
<!-- cases: 9 panics: a deep table never overflows the stack (flattening is iterative); FuzzFiles_TOML seeds include it -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Scalars become strings the same way per format
Scalars SHALL be stored as strings: YAML as the scalar node's literal text; JSON numbers as their literal text; TOML integers in decimal, floats with `strconv.FormatFloat(f, 'g', -1, 64)`, offset datetimes in RFC 3339, and local dates, times and datetimes in TOML's own text; JSON and TOML booleans as `true` or `false`. YAML keeps `True`, `yes` and `1.0` as written; the binder parses each string.

#### Scenario: Rendering by format
```gherkin
Given a temp directory holding <file> with <content>
When the consumer calls config.Files(config.File("<file>")).Load(ctx)
Then Lookup("v") returns ("<value>", true)

Examples:
  | file   | content                     | value                |
  | a.json | {"v": 12345678901234567890} | 12345678901234567890 |
  | a.json | {"v": 1e3}                  | 1e3                  |
  | a.json | {"v": true}                 | true                 |
  | a.json | {"v": ""}                   |                      |
  | a.toml | v = 1.0                     | 1                    |
  | a.toml | v = 0.5                     | 0.5                  |
  | a.toml | v = 1979-05-27T07:32:00Z    | 1979-05-27T07:32:00Z |
  | a.toml | v = 1979-05-27              | 1979-05-27           |
  | a.toml | v = 07:32:00                | 07:32:00             |
  | a.toml | v = 1979-05-27T07:32:00     | 1979-05-27T07:32:00  |
  | a.toml | v = 0x1F                    | 31                   |
  | a.yaml | v: 0x1F                     | 0x1F                 |
  | a.yaml | v: 1.0                      | 1.0                  |
  | a.yaml | v: True                     | True                 |
  | a.yaml | v: yes                      | yes                  |
  | a.yaml | v: 5s                       | 5s                   |
```

<!-- cases: N/A 1 default, 2 overrides: rendering is fixed; the binder parses strings, so an int field rejects YAML "1.0" but accepts TOML "1" (documented: TOML hands floats over without their text) -->
<!-- cases: 3 zero values: an empty string stays "" and present -->
<!-- cases: 4 boundaries: integer past int64, exponent, hex, local date/time -->
<!-- cases: N/A 5 errors: rendering cannot fail -->
<!-- cases: N/A 6 duplicate, 7 lifecycle, 8 concurrency, 9 panics: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: YAML anchors, aliases and merge keys
YAML SHALL first be decoded into `any`, keeping yaml.v3's alias limits, and then walked as a node tree for literal text and lines: an alias SHALL take its anchor's value and a `<<` merge key SHALL merge the referenced mappings under the keys written beside it.

#### Scenario: Aliases and merges
```gherkin
Given a.yaml holding:
  port: &p 8080
  base: &b {x: 1, y: 2}
  svc:
    <<: *b
    y: 3
  copy: *p
When the consumer calls config.Files(config.File("a.yaml")).Load(ctx)
Then Lookup("<key>") returns <result>

Examples:
  | key     | result         |
  | copy    | ("8080", true) |
  | svc.x   | ("1", true)    |
  | svc.y   | ("3", true)    |
  | svc.<<  | ("", false)    |
```

<!-- cases: 1 default: the subject -->
<!-- cases: N/A 2 overrides: fixed -->
<!-- cases: N/A 3 zero values: an alias of null is absent like null -->
<!-- cases: 4 boundaries: a key beside a merge wins over the merged one -->
<!-- cases: N/A 5 errors: alias bombs and cycles are "Invalid file shapes fail the load" -->
<!-- cases: N/A 6 duplicate: merge precedence is the duplicate rule here -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Layer precedence
For a key held by several layers, `Lookup` SHALL return the highest layer's value, from highest: added layers, process env, `.env`, profile files, base files. Layers SHALL match keys in uniform form: lowercase, `-` and `_` removed per segment, `[k]` with `k` in `[a-z0-9]+` equal to `.k`, an all-digit `.n` equal to `[n]`, other brackets verbatim, and an instance segment never folded.

#### Scenario: Highest layer wins
```gherkin
Given config.yaml holding server.port: 8080, config.prod.yaml holding server.port: 9090, APP_PROFILE=prod (t.Chdir, clearEnvPrefix(t, "APP_"), t.Setenv)
And <dotenv>
And <env>
When the consumer calls config.Files().Load(ctx)
Then Lookup("server.port") returns ("<value>", true)

Examples:
  | dotenv                            | env                  | value |
  | no .env                           | nothing set          | 9090  |
  | .env holding APP_SERVER_PORT=9191 | nothing set          | 9191  |
  | .env holding APP_SERVER_PORT=9191 | APP_SERVER_PORT=9292 | 9292  |
  | no .env                           | APP_SERVER_PORT=9292 | 9292  |
  | no .env                           | APP_SERVER_PORT=     |       |
```

#### Scenario: Spelling never decides precedence
```gherkin
Given config.yaml holding <file content> (t.Chdir, clearEnvPrefix(t, "APP_"))
And <variable> is set (t.Setenv)
When the consumer calls config.Files().Load(ctx)
Then Lookup("<key>") returns <result>

Examples:
  | file content                       | variable                    | key                     | result       |
  | server.read-timeout: 5s            | APP_SERVER_READTIMEOUT=9s   | server.read-timeout     | ("9s", true) |
  | server.read-timeout: 5s            | APP_SERVER_READTIMEOUT=9s   | server.readtimeout      | ("9s", true) |
  | server.allowed-hosts: [a, b]       | APP_SERVER_ALLOWEDHOSTS_0=z | server.allowed-hosts[0] | ("z", true)  |
  | server.allowed-hosts: [a, b]       | APP_SERVER_ALLOWEDHOSTS_0=z | server.allowed-hosts[1] | ("", false)  |
  | server.labels: {"[team]": a}       | APP_SERVER_LABELS_TEAM=b    | server.labels[team]     | ("b", true)  |
  | server.labels: {"[team]": a}       | APP_SERVER_LABELS_TEAM=b    | server.labels.team      | ("b", true)  |
  | redis.queue: [x]                   | nothing                     | redis.queue.0           | ("x", true)  |
  | redis/0: {addr: i}                 | APP_REDIS_0_ADDR=n          | redis/0.addr            | ("i", true)  |
```

<!-- cases: 1 default: the order itself -->
<!-- cases: 2 overrides: LayerOrder is "Added layers and their order" -->
<!-- cases: 3 zero values: a set but empty env var is the empty value and wins -->
<!-- cases: 4 boundaries: every adjacent pair of layers; dashed against dash-free; bracket against dot; an instance key "0" never folds into an index (the env variable names redis[0].addr, not the instance) -->
<!-- cases: N/A 5 errors: precedence has no error path -->
<!-- cases: 6 duplicate: the same key in four layers -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: A list comes whole from one layer
A list key SHALL take all its entries from the highest layer that holds the key in any form: indexed entries or one scalar. A scalar in a higher layer SHALL mask a lower layer's indices, so the binder can comma-split it. An empty sequence in a higher file holds no key and masks nothing.

#### Scenario: Lists across layers
```gherkin
Given a temp working directory (t.Chdir, clearEnvPrefix(t, "APP_")) with config.yaml holding server.hosts: [a, b, c]
And <higher>
And src := config.Files() after Load(ctx) returned nil
When the consumer calls src.Keys("server.hosts")
Then the result is <keys> and src.Lookup("<lookup>") returns <value>

Examples:
  | higher                                                 | keys                                                      | lookup          | value         |
  | nothing                                                | ["server.hosts[0]", "server.hosts[1]", "server.hosts[2]"] | server.hosts[2] | ("c", true)   |
  | APP_SERVER_HOSTS_0=z                                   | ["server.hosts[0]"]                                       | server.hosts[1] | ("", false)   |
  | APP_SERVER_HOSTS=z,y                                   | ["server.hosts"]                                          | server.hosts    | ("z,y", true) |
  | APP_PROFILE=prod and config.prod.yaml with hosts: []   | ["server.hosts[0]", "server.hosts[1]", "server.hosts[2]"] | server.hosts[0] | ("a", true)   |
```

<!-- cases: 1 default: file-only row -->
<!-- cases: N/A 2 overrides: list precedence follows layer order -->
<!-- cases: 3 zero values: an empty sequence in a higher file masks nothing; null is absent -->
<!-- cases: 4 boundaries: shorter higher list; scalar over indices -->
<!-- cases: N/A 5 errors: no error path -->
<!-- cases: 6 duplicate: the same list in two layers -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Env prefix
The env layer SHALL read variables under a prefix, `APP_` by default. `config.EnvPrefix(p)` SHALL turn `-` and `.` into `_`, uppercase the result and append `_` when missing; `""` SHALL turn the prefix off. A prefix still holding characters outside `[A-Z0-9_]` SHALL fail `Load` matching `config.ErrInvalidOption`. `doger.EnvPropertySource` normalizes the same way.

#### Scenario: Prefix forms
```gherkin
Given clearEnvPrefix(t, "APP_") and <variable>=x is set (t.Setenv)
When the consumer calls <source>.Load(ctx)
Then Load returns <error> and Lookup("logger.log-file") returns <result>

Examples:
  | source                                   | variable              | error                                     | result      |
  | config.Files()                           | APP_LOGGER_LOGFILE    | nil                                       | ("x", true) |
  | config.Files(config.EnvPrefix("MYSVC"))  | MYSVC_LOGGER_LOGFILE  | nil                                       | ("x", true) |
  | config.Files(config.EnvPrefix("MYSVC_")) | MYSVC_LOGGER_LOGFILE  | nil                                       | ("x", true) |
  | config.Files(config.EnvPrefix("mysvc"))  | MYSVC_LOGGER_LOGFILE  | nil                                       | ("x", true) |
  | config.Files(config.EnvPrefix("my-svc")) | MY_SVC_LOGGER_LOGFILE | nil                                       | ("x", true) |
  | config.Files(config.EnvPrefix("my.svc")) | MY_SVC_LOGGER_LOGFILE | nil                                       | ("x", true) |
  | config.Files(config.EnvPrefix(""))       | LOGGER_LOGFILE        | nil                                       | ("x", true) |
  | config.Files(config.EnvPrefix("MYSVC"))  | APP_LOGGER_LOGFILE    | nil                                       | ("", false) |
  | config.Files(config.EnvPrefix("my svc")) | MY_SVC_LOGGER_LOGFILE | an error matching config.ErrInvalidOption | ("", false) |
  | config.Files(config.EnvPrefix("møsvc"))  | MØSVC_LOGGER_LOGFILE  | an error matching config.ErrInvalidOption | ("", false) |
```

<!-- cases: 1 default: APP_ row -->
<!-- cases: 2 overrides: every EnvPrefix row -->
<!-- cases: 3 zero values: "" turns the prefix off; then every process variable that maps back to a key is a key (documented: PATH and HOME become keys) -->
<!-- cases: 4 boundaries: with and without trailing "_"; lower-case input; "-" and "." normalized -->
<!-- cases: 5 errors: prefixes with a space or non-ASCII letter match ErrInvalidOption -->
<!-- cases: N/A 6 duplicate: the last EnvPrefix wins, as every scalar option -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Env names follow Spring's mapping
A key SHALL map to a variable by uppercasing, `.` to `_`, removing `-`, `/` to `__` and `[i]` to `_i`; a map key `[k]` SHALL map to `_K` only when `k` matches `[a-z0-9]+`, and other map keys are not addressable from env. Variables SHALL map back with `__` as the instance separator and an all-digit segment as an index. The table SHALL be the root's golden file `testdata/envmapping.txt`.

#### Scenario: Key to variable
```gherkin
Given clearEnvPrefix(t, "APP_") and <variable>=v is set (t.Setenv)
When the consumer calls config.Files().Load(ctx)
Then Lookup("<key>") returns ("v", true)

Examples:
  | key                  | variable               |
  | server.read-timeout  | APP_SERVER_READTIMEOUT |
  | server.issuer-uri    | APP_SERVER_ISSUERURI   |
  | server.hosts[0]      | APP_SERVER_HOSTS_0     |
  | my.service[0].other  | APP_MY_SERVICE_0_OTHER |
  | server.labels[team]  | APP_SERVER_LABELS_TEAM |
  | redis/queue.addr     | APP_REDIS__QUEUE_ADDR  |
```

#### Scenario: Variables mapped back to keys
```gherkin
Given only these variables start with "APP_": <variables> (clearEnvPrefix(t, "APP_"), then t.Setenv)
And src := config.Files() after Load(ctx) returned nil
When the consumer calls src.Keys("")
Then the result is <keys>

Examples:
  | variables                                   | keys                                   |
  | APP_SERVER_READTIMEOUT=1                    | ["server.readtimeout"]                 |
  | APP_REDIS__QUEUE_ADDR=q                     | ["redis/queue.addr"]                   |
  | APP_SERVER_HOSTS_0=a, APP_SERVER_HOSTS_1=b  | ["server.hosts[0]", "server.hosts[1]"] |
  | APP__X=1, app_lower=1, APP_A___B=1          | []                                     |
```

<!-- cases: 1 default: Spring mapping rows -->
<!-- cases: 2 overrides: EnvMapper is "A custom env mapper" -->
<!-- cases: 3 zero values: an empty variable value is ("", true) -->
<!-- cases: 4 boundaries: index in the middle and at the end; instance separator -->
<!-- cases: 5 errors: names that do not map back (leading "__", lower case, "___") are skipped, not errors -->
<!-- cases: 6 duplicate: pool-size and poolsize share APP_..._POOLSIZE, as in Spring (decided) -->
<!-- cases: N/A 7 lifecycle: env is captured at Load ("Lists ... " and config-reload) -->
<!-- cases: N/A 8 concurrency, 9 panics: Load only -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: A custom env mapper
`config.EnvMapper(fn)` SHALL replace the mapping: `fn(key)` returns the full variable name, `EnvPrefix` is not applied to it, and the name SHALL be looked up in the process environment captured at `Load`, then in `.env`. A mapped env layer SHALL contribute nothing to `Keys`, so list, map and pointer fields cannot bind from it. A nil mapper SHALL keep the Spring mapping.

#### Scenario: Mapper with and without a prefix
```gherkin
Given a temp working directory (t.Chdir, clearEnvPrefix(t, "APP_")) with <setup>
And mapper := func(key string) string returning "SERVER__PORT" for "server.port"
And src := config.Files(config.EnvPrefix("MYSVC"), config.EnvMapper(mapper)) after Load(ctx) returned nil
When the consumer calls src.Lookup("server.port")
Then the result is <result> and src.Keys("server") returns []

Examples:
  | setup                                                        | result         |
  | process SERVER__PORT=7070 and MYSVC_SERVER_PORT=1            | ("7070", true) |
  | .env holding SERVER__PORT=6060, process SERVER__PORT unset   | ("6060", true) |
  | .env holding SERVER__PORT=6060 and process SERVER__PORT=7070 | ("7070", true) |
  | nothing set                                                  | ("", false)    |
```

<!-- cases: N/A 1 default: Spring mapping is the previous requirement -->
<!-- cases: 2 overrides: the subject -->
<!-- cases: 3 zero values: a nil mapper keeps Spring; a mapper returning "" is a miss -->
<!-- cases: 4 boundaries: .env only, process only, both -->
<!-- cases: N/A 5 errors: a mapper cannot fail -->
<!-- cases: N/A 6 duplicate: the last EnvMapper wins -->
<!-- cases: 7 lifecycle: the environment is captured at Load; later changes are not seen -->
<!-- cases: N/A 8 concurrency: the mapper is called concurrently and must be pure (documented) -->
<!-- cases: N/A 9 panics: a panicking mapper is the caller's bug; the root recovers it during binding (config-binding) -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: .env is a layer that never writes the process environment
`config.Files` SHALL read `./.env` from the working directory when it exists, as a layer between the profile files and process env, under the same prefix and mapping, and SHALL NOT set any process variable. `config.DotEnv(path)` SHALL read another file and `config.DotEnv("")` SHALL skip it; an explicit path that is missing SHALL fail `Load` matching `fs.ErrNotExist`.

#### Scenario: .env handling
```gherkin
Given a temp working directory (t.Chdir, clearEnvPrefix(t, "APP_")) with .env holding APP_SPIKE_SECRET=from-dotenv and secrets.env holding APP_SPIKE_SECRET=from-file
And APP_SPIKE_SECRET is unset in the process (t.Setenv then os.Unsetenv, so cleanup restores it)
When the consumer calls <source>.Load(ctx)
Then Load returns <error>, Lookup("spike.secret") returns <result> and os.LookupEnv("APP_SPIKE_SECRET") returns ("", false)

Examples:
  | source                                     | error                                                 | result                |
  | config.Files()                             | nil                                                   | ("from-dotenv", true) |
  | config.Files(config.DotEnv("secrets.env")) | nil                                                   | ("from-file", true)   |
  | config.Files(config.DotEnv(""))            | nil                                                   | ("", false)           |
  | config.Files(config.DotEnv("missing.env")) | an error matching fs.ErrNotExist naming "missing.env" | ("", false)           |
```

<!-- cases: 1 default: first row (spike 5 RED test) -->
<!-- cases: 2 overrides: DotEnv path and off; export is "DotEnvExport sets only unset variables" -->
<!-- cases: 3 zero values: DotEnv(""); no ./.env is not an error -->
<!-- cases: N/A 4 boundaries: one file per source -->
<!-- cases: 5 errors: an explicit missing .env -->
<!-- cases: 6 duplicate: a variable in .env and the process: the process wins ("Layer precedence") -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: Load only -->
<!-- cases: N/A 10 compatibility: new module; golang-lib's os.Setenv behaviour is the opt-in export -->

### Requirement: .env lines
Each `.env` line SHALL be blank, a `#` comment, or `[export ]KEY=VALUE`, with spaces around `KEY` and `=` ignored and `KEY` matching `[A-Za-z_][A-Za-z0-9_]*`. CRLF and a UTF-8 BOM SHALL be accepted, a later line SHALL win, and any other line SHALL fail `Load` with a `*config.ParseError` of category "invalid line" naming `.env` and the line.

#### Scenario: Line forms
```gherkin
Given a temp working directory (t.Chdir) with .env holding the single line <line>
When the consumer calls config.Files(config.EnvPrefix("")).Load(ctx)
Then Load returns <error> and Lookup("k") returns <result>

Examples:
  | line             | error                                                 | result      |
  | K=v              | nil                                                   | ("v", true) |
  | export K=v       | nil                                                   | ("v", true) |
  | K = v            | nil                                                   | ("v", true) |
  | K=v<CR><LF>      | nil                                                   | ("v", true) |
  | <BOM>K=v         | nil                                                   | ("v", true) |
  | # only a comment | nil                                                   | ("", false) |
  | K=               | nil                                                   | ("", true)  |
  | 1K=v             | a *config.ParseError for ".env" line 1 "invalid line" | ("", false) |
  | not a pair s3cret| a *config.ParseError for ".env" line 1 "invalid line" | ("", false) |
```

#### Scenario: A later line wins
```gherkin
Given a temp working directory (t.Chdir) with .env holding "K=a" on line 1 and "K=b" on line 2
When the consumer calls config.Files(config.EnvPrefix("")).Load(ctx)
Then Lookup("k") returns ("b", true)
```

<!-- cases: N/A 1 default, 2 overrides: the syntax is fixed -->
<!-- cases: 3 zero values: empty value, comment-only file -->
<!-- cases: 4 boundaries: spaces around "="; CRLF; BOM; key starting with a digit -->
<!-- cases: 5 errors: invalid lines; the error never contains "s3cret" -->
<!-- cases: 6 duplicate: later line wins (not an error, unlike file formats) -->
<!-- cases: N/A 7 lifecycle, 8 concurrency: Load only -->
<!-- cases: 9 panics: FuzzFiles_DotEnv; no input panics -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: .env values
An unquoted value SHALL drop leading and trailing whitespace and end at ` #`. A single-quoted value SHALL be literal. A double-quoted value SHALL support `\n`, `\t`, `\"` and `\\` and keep any other backslash pair as written. Quoted values SHALL end on their line; only whitespace or a ` #` comment may follow the closing quote. `${...}` SHALL NOT be expanded.

#### Scenario: Value forms
```gherkin
Given a temp working directory (t.Chdir) with .env holding the single line <line>
When the consumer calls config.Files(config.EnvPrefix("")).Load(ctx)
Then Load returns <error> and Lookup("k") returns <result>

Examples:
  | line                 | error                                                 | result                    |
  | K=v   # note         | nil                                                   | ("v", true)               |
  | K=v#not-a-comment    | nil                                                   | ("v#not-a-comment", true) |
  | K=  v                | nil                                                   | ("v", true)               |
  | K='a $X \n'          | nil                                                   | ("a $X \\n", true)        |
  | K="a\nb\t\"q\" \\"   | nil                                                   | ("a<LF>b<TAB>\"q\" \\", true) |
  | K="C:\qx"            | nil                                                   | ("C:\\qx", true)          |
  | K="v" # note         | nil                                                   | ("v", true)               |
  | K=${HOME}            | nil                                                   | ("${HOME}", true)         |
  | K="v" junk           | a *config.ParseError for ".env" line 1 "invalid line" | ("", false)               |
  | K="unterminated s3cret | a *config.ParseError for ".env" line 1 "invalid line" | ("", false)             |
```

<!-- cases: N/A 1 default, 2 overrides: the syntax is fixed -->
<!-- cases: 3 zero values: whitespace-only value is "" -->
<!-- cases: 4 boundaries: "#" with and without a space before it; known and unknown escapes -->
<!-- cases: 5 errors: text after a closing quote; unterminated quote (multi-line values are not supported); the error never contains "s3cret" -->
<!-- cases: N/A 6 duplicate: see ".env lines" -->
<!-- cases: N/A 7 lifecycle, 8 concurrency: Load only -->
<!-- cases: 9 panics: FuzzFiles_DotEnv -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: DotEnvExport sets only unset variables
`config.DotEnvExport()` SHALL, at the first successful `Load` only and never on a reload, set every `.env` variable that is unset in the process, whether or not it carries the env prefix, and SHALL log one line naming, never valuing, every variable it skipped because it was already set.

#### Scenario: Export at the first Load
```gherkin
Given a temp working directory (t.Chdir) with .env holding APP_A=1, APP_B=2 and OTHER=3; APP_B=0 already set; APP_A and OTHER unset (t.Setenv then os.Unsetenv, so cleanup restores all three)
And src := config.Files(config.DotEnvExport(), config.Logger(rec)) where rec records lines
When the consumer calls src.Load(ctx)
Then os.Getenv("APP_A") is "1", os.Getenv("APP_B") is "0" and os.Getenv("OTHER") is "3"
And rec holds one line naming "APP_B" and containing neither "2" nor "0"
```

#### Scenario: A second Load exports nothing
```gherkin
Given the source above after its first Load, and .env changed to APP_C=3 with APP_C unset (t.Setenv then os.Unsetenv)
When the consumer calls src.Load(ctx) again
Then os.LookupEnv("APP_C") returns ("", false) and src.Lookup("c") returns ("3", true)
```

<!-- cases: 1 default: without DotEnvExport nothing is exported (".env is a layer ...") -->
<!-- cases: 2 overrides: the subject -->
<!-- cases: 3 zero values: an empty .env exports nothing and logs nothing -->
<!-- cases: 4 boundaries: prefixed and unprefixed variables -->
<!-- cases: N/A 5 errors: os.Setenv failures are logged by name and do not fail Load -->
<!-- cases: 6 duplicate: an already-set variable is skipped -->
<!-- cases: 7 lifecycle: first Load only; never during Watch -->
<!-- cases: N/A 8 concurrency: export happens inside the serialized Load; godoc warns that os.Setenv races cgo getenv -->
<!-- cases: N/A 9 panics: no user code runs -->
<!-- cases: N/A 10 compatibility: golang-lib exported unconditionally; this is the opt-in -->

### Requirement: Added layers and their order
`config.Layer(name LayerName, src LayerSource)`, with `LayerSource` as `interface{ Load(ctx context.Context) (map[string]string, error) }`, SHALL add a layer above process env in the order added; its keys SHALL be normalized like file keys and it is loaded during `Load`. `config.LayerOrder(names...)` SHALL set the full order, lowest first, from `LayerBase`, `LayerProfile`, `LayerDotEnv`, `LayerEnv` and added names; an omitted built-in layer SHALL NOT be read.

#### Scenario: A vault-style layer
```gherkin
Given config.yaml holding db.password: from-file and APP_DB_PASSWORD=from-env (t.Chdir, clearEnvPrefix(t, "APP_"), t.Setenv)
And vault := a LayerSource returning {"db.password": "from-vault"} whose Load counts calls
When the consumer calls <source>.Load(ctx)
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
When the consumer calls config.Files(config.Layer("vault", vault)).Load(ctx)
Then Load returns a *config.LayerError with Layer "vault" whose text is `layer "vault": *errors.errorString`
And errors.Is(err, errVault) is true and neither err.Error() nor fmt.Sprintf("%#v", err) contains "s3cret"
```

#### Scenario: A layer key that cannot be normalized
```gherkin
Given vault := a LayerSource returning {"bad key": "x"}
When the consumer calls config.Files(config.Layer("vault", vault)).Load(ctx)
Then Load returns a *config.LayerError with Layer "vault" wrapping a *config.ParseError of category "invalid key"
```

<!-- cases: 1 default: no added layer row -->
<!-- cases: 2 overrides: Layer and LayerOrder rows, including omitting env -->
<!-- cases: 3 zero values: Layer(name, nil), an empty name, a built-in name, a repeated name, an unknown LayerOrder name and an added layer missing from LayerOrder are "Options are checked when Load runs" -->
<!-- cases: 4 boundaries: zero, one added layer; first and last positions -->
<!-- cases: 5 errors: a failing layer's error is wrapped in *config.LayerError, text type-only; an invalid layer key -->
<!-- cases: N/A 6 duplicate: a repeated layer name is an option error -->
<!-- cases: 7 lifecycle: loaded during Load; re-reading at polls under config.StageTimeout is specified by config-reload -->
<!-- cases: N/A 8 concurrency: covered by "Reads during Load see one snapshot" -->
<!-- cases: N/A 9 panics: the module does not recover a layer's panic; the root recovers it as ErrConfigLoadFailed (config-sources) -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Keys lists every key under a prefix
`Keys(prefix)` SHALL return, after precedence and byte-wise sorted, every key whose uniform form equals `prefix` or extends it segment by segment, each uniform key once, the prefix part spelled as the caller wrote it and the rest as the highest layer holds it. `Lookup` SHALL find every key `Keys` returns. An empty prefix SHALL return every key; before the first `Load` the result SHALL be empty.

#### Scenario: Keys across layers
```gherkin
Given config.yaml holding server.port: 8080 and server.labels: {team: core} (t.Chdir, clearEnvPrefix(t, "APP_"))
And APP_SERVER_HOST=h, APP_SERVERS_0=x and APP_SERVER_ALLOWEDHOSTS_0=z are set (t.Setenv)
And src := config.Files() after Load(ctx) returned nil
When the consumer calls src.Keys("<prefix>")
Then the result is <keys>, and src.Lookup of every returned key finds a value

Examples:
  | prefix               | keys                                                                           |
  | server               | ["server.allowedhosts[0]", "server.host", "server.labels.team", "server.port"] |
  | server.labels        | ["server.labels.team"]                                                         |
  | server.allowed-hosts | ["server.allowed-hosts[0]"]                                                    |
  | servers              | ["servers[0]"]                                                                 |
  | missing              | []                                                                             |
```

#### Scenario: Ordering, duplicates and before Load
```gherkin
Given a temp working directory (t.Chdir, clearEnvPrefix(t, "APP_")) with config.yaml holding <content> and <env>
And src := config.Files() <state>
When the consumer calls src.Keys("<prefix>")
Then the result is <keys>

Examples:
  | content                               | env                       | state                         | prefix | keys                                     |
  | h: [a, b, c, d, e, f, g, h, i, j, k]  | nothing                   | after Load                    | h      | ["h[0]", "h[10]", "h[1]", "h[2]", "h[3]", "h[4]", "h[5]", "h[6]", "h[7]", "h[8]", "h[9]"] |
  | server.read-timeout: 5s               | APP_SERVER_READTIMEOUT=9s | after Load                    | server | ["server.readtimeout"]                   |
  | server.port: 8080                     | nothing                   | before any Load               |        | []                                       |
```

<!-- cases: 1 default: env and file keys together -->
<!-- cases: N/A 2 overrides: Keys has no options -->
<!-- cases: 3 zero values: empty prefix; before Load -->
<!-- cases: 4 boundaries: "server" never matches "servers"; byte-wise order puts "[10]" before "[2]" (documented) -->
<!-- cases: N/A 5 errors: Keys never fails -->
<!-- cases: 6 duplicate: a key in two layers is listed once, spelled as the highest layer holds it -->
<!-- cases: 7 lifecycle: before Load returns [] -->
<!-- cases: 8 concurrency: covered by "Reads during Load see one snapshot" -->
<!-- cases: N/A 9 panics: Keys does not panic -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Load errors carry no values
Every `Load` failure caused by file content SHALL be a `*config.ParseError{File, Line, Category}` whose text is `<file>:<line>: <category>` (or `<file>: <category>` when the line is 0), `Category` being a `config.ParseCategory`. It SHALL NOT wrap the parser's error or keep any source text, so no value reaches its message, `%#v`, or `errors.As` targets.

#### Scenario: A syntax error near a secret
```gherkin
Given a temp directory holding <file> whose line <line> is <content> containing "s3cret"
When the consumer calls config.Files(config.File("<file>")).Load(ctx)
Then err.Error() is "<message>"
And errors.As(err, &pe) holds with pe.File "<file>" and pe.Category config.CategorySyntax
And neither err.Error(), fmt.Sprintf("%#v", err) nor errors.Unwrap(err) contains "s3cret"

Examples:
  | file   | line | content                       | message                |
  | a.yaml | 3    | password: "s3cret             | a.yaml:3: syntax error |
  | a.toml | 2    | password = "s3cret\q"         | a.toml:2: syntax error |
  | a.json | 1    | {"password": "s3cret" "x": 1} | a.json:1: syntax error |
  | a.yaml | 0    | a: "s3cret\q"                 | a.yaml: syntax error   |
```

<!-- cases: N/A 1 default, 2 overrides: secrecy is not configurable (same guarantee as extension-properties) -->
<!-- cases: 3 zero values: line 0 row when the parser gives no position -->
<!-- cases: 4 boundaries: first line, later line -->
<!-- cases: 5 errors: every row; the categories are config.CategorySyntax, CategoryDuplicateKey, CategoryMultipleDocuments, CategoryNotMapping, CategoryExcessiveAliasing, CategoryInvalidLine, CategoryInvalidKey, CategoryTooDeep, CategoryTooManyKeys and CategoryUnsupportedValue -->
<!-- cases: N/A 6 duplicate, 7 lifecycle, 8 concurrency: Load only -->
<!-- cases: 9 panics: FuzzFiles_YAML, FuzzFiles_TOML, FuzzFiles_JSON and FuzzFiles_DotEnv fuzz the in-package decoders with a high-entropy marker in value positions and assert the error text matches ^<file>(:\d+)?: <category>$ and never holds the marker -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Config errors are typed and value-free
Every error `Load` returns, except a context error, SHALL be a `*config.ParseError`, `*config.FileError{File, Err}`, `*config.LayerError{Layer, Err}` or a config sentinel, and each SHALL implement `ValueFree()` so the root prints its text. Texts SHALL hold no value and no `config:` prefix. `FileError` and `LayerError` SHALL unwrap to their cause; `LayerError` SHALL print its cause's text only when the cause is value-free, else its type.

#### Scenario: Error texts and matching
```gherkin
Given clearEnvPrefix(t, "APP_") and <setup>
When the consumer calls <source>.Load(ctx)
Then err.Error() is "<text>", errors.Is(err, <target>) is true, and any(err).(interface{ ValueFree() }) succeeds

Examples:
  | setup                                       | source                                     | text                                     | target                   |
  | no file c/missing.yaml                      | config.Files(config.File("c/missing.yaml")) | c/missing.yaml: does not exist           | fs.ErrNotExist           |
  | a config.yaml of 4 MiB + 1 B in dir d       | config.Files(config.Dir("d"))              | d/config.yaml: file too large            | config.ErrFileTooLarge   |
  | a FIFO named config.yaml in dir d (not Windows) | config.Files(config.Dir("d"))          | d/config.yaml: not a regular file        | config.ErrNotRegularFile |
  | config.yaml and config.toml in dir d        | config.Files(config.Dir("d"))              | d/config.yaml, d/config.toml: ambiguous format | config.ErrAmbiguousFormat |
  | config.yaml in dir d                        | config.Files(config.Dir("d"), config.Profile("dev")) | profile "dev" not found        | config.ErrProfileNotFound |
  | nothing                                     | config.Files(config.Dir())                 | invalid option Dir: no path              | config.ErrInvalidOption  |
  | vault returning errVault (text holds s3cret) | config.Files(config.Layer("vault", vault)) | layer "vault": *errors.errorString      | errVault                 |
```

<!-- cases: N/A 1 default, 2 overrides: error shapes are fixed -->
<!-- cases: N/A 3 zero values: covered by the option row -->
<!-- cases: 4 boundaries: a cause that is not value-free is printed by type only -->
<!-- cases: 5 errors: every row; errors.Is reaches the cause through Unwrap; %#v prints type-only text (GoString) -->
<!-- cases: N/A 6 duplicate, 7 lifecycle, 8 concurrency: Load only -->
<!-- cases: N/A 9 panics: errors are plain values -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Files over the size limit and non-regular files are refused
Every file, `.env` included, SHALL be checked with `os.Stat` before it is opened and refused with `config.ErrNotRegularFile` unless it is a regular file after following symlinks, so a FIFO never blocks `Load`. It SHALL be read through a limit of `config.MaxFileSize(n)` bytes, 4 MiB by default; a larger file SHALL fail matching `config.ErrFileTooLarge`. Polls reuse this reader (config-reload).

#### Scenario: Size limit
```gherkin
Given a temp directory d holding config.yaml of <size>
When the consumer calls config.Files(config.Dir("d")<extra>).Load(ctx)
Then Load returns <error>

Examples:
  | size        | extra                          | error                                                       |
  | 4 MiB       |                                | nil                                                         |
  | 4 MiB + 1 B |                                | an error matching config.ErrFileTooLarge naming config.yaml |
  | 4 MiB       | , config.MaxFileSize(0)        | nil                                                         |
  | 2 KiB       | , config.MaxFileSize(1024)     | an error matching config.ErrFileTooLarge                    |
  | 8 MiB       | , config.MaxFileSize(16 << 20) | nil                                                         |
```

#### Scenario: Non-regular files
```gherkin
Given a temp directory d where config.yaml is <kind> (test skipped on Windows)
When the consumer calls config.Files(config.Dir("d")).Load(ctx) under testWithTimeout
Then Load returns an error matching config.ErrNotRegularFile naming config.yaml and does not block

Examples:
  | kind                         |
  | a FIFO                       |
  | a directory                  |
  | a symlink to a FIFO          |
```

<!-- cases: 1 default: 4 MiB rows; regular-file check always on -->
<!-- cases: 2 overrides: MaxFileSize rows -->
<!-- cases: 3 zero values: MaxFileSize(0) means the default; a negative size is "Options are checked when Load runs" -->
<!-- cases: 4 boundaries: exactly at and one past the limit -->
<!-- cases: 5 errors: ErrFileTooLarge, ErrNotRegularFile -->
<!-- cases: N/A 6 duplicate, 7 lifecycle: Load only; polls are config-reload's -->
<!-- cases: 8 concurrency: a FIFO never blocks because Stat comes before Open -->
<!-- cases: N/A 9 panics: plain file reads -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Options are checked when Load runs
An invalid option SHALL fail `Load` matching `config.ErrInvalidOption` and naming the option. Invalid are: an empty path or no path in `Dir` or `File`, an empty `Name` or one with `/`, an unknown `FileFormat`, a `Codec` without a decoder or a leading `.`, `ProfileEnv("")`, a `Layer` without a source or with an empty, repeated or built-in name, a `LayerOrder` that is empty, repeats or misses a name, and a negative `MaxFileSize` or `PollInterval`. `Logger(nil)` and `EnvMapper(nil)` mean unset.

#### Scenario: Invalid options
```gherkin
Given a temp working directory (t.Chdir, clearEnvPrefix(t, "APP_")) and vault, a valid LayerSource
When the consumer calls config.Files(<option>).Load(ctx)
Then Load returns an error matching config.ErrInvalidOption whose message names "<named>"

Examples:
  | option                                                       | named        |
  | config.Dir()                                                 | Dir          |
  | config.Dir("")                                               | Dir          |
  | config.File()                                                | File         |
  | config.Name("")                                              | Name         |
  | config.Name("a/b")                                           | Name         |
  | config.Format("ini")                                         | Format       |
  | config.Codec(".hcl", nil)                                    | Codec        |
  | config.Codec("hcl", hclDecoder)                              | Codec        |
  | config.ProfileEnv("")                                        | ProfileEnv   |
  | config.Layer("vault", nil)                                   | Layer        |
  | config.Layer("", vault)                                      | Layer        |
  | config.Layer("env", vault)                                   | Layer        |
  | config.Layer("vault", vault), config.Layer("vault", vault)   | Layer        |
  | config.LayerOrder()                                          | LayerOrder   |
  | config.LayerOrder(config.LayerBase, "nope")                  | LayerOrder   |
  | config.LayerOrder(config.LayerEnv, config.LayerEnv)          | LayerOrder   |
  | config.Layer("vault", vault), config.LayerOrder(config.LayerBase) | LayerOrder |
  | config.MaxFileSize(-1)                                       | MaxFileSize  |
  | config.PollInterval(-time.Second)                            | PollInterval |
```

<!-- cases: N/A 1 default: no invalid option by default -->
<!-- cases: 2 overrides: every row -->
<!-- cases: 3 zero values: Logger(nil) and EnvMapper(nil) mean unset; MaxFileSize(0), PollInterval(0) and StageTimeout(0) mean the default; StageTimeout with a negative value means no timeout (config-reload) -->
<!-- cases: 4 boundaries: built-in layer name collision; added layer missing from LayerOrder -->
<!-- cases: 5 errors: every row matches ErrInvalidOption -->
<!-- cases: 6 duplicate: repeated layer names -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: options are plain data checked in Load -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Load honors its context
`Load(ctx)` SHALL stop and return an error matching `ctx.Err()` when the context ends before loading finishes, return before reading any file when the context has already ended, and keep answering from the snapshot it held before that `Load`.

#### Scenario: A cancelled load keeps the previous snapshot
```gherkin
Given a source config.Files(config.File("config.yaml"), config.Layer("slow", slow)) already loaded once with server.port: 8080
And config.yaml now holds server.port: 9090 and slow's Load blocks until its context ends
When the consumer calls Load(ctx) with ctx cancelled once slow's Load has started (channel handshake)
Then the error matches context.Canceled with errors.Is
And Lookup("server.port") returns ("8080", true)
```

#### Scenario: An already-cancelled context
```gherkin
Given src := config.Files(config.File("config.yaml")) never loaded, and a context cancelled before the call
When the consumer calls src.Load(ctx)
Then the error matches context.Canceled, config.yaml was never opened (recorded by a test hook on the reader), and src.Keys("") returns []
```

<!-- cases: N/A 1 default, 2 overrides: the timeout belongs to the container (config-sources) -->
<!-- cases: 3 zero values: an already-cancelled context returns before reading any file -->
<!-- cases: 4 boundaries: a deadline matches context.DeadlineExceeded the same way -->
<!-- cases: 5 errors: the error matches ctx.Err() and is not wrapped in a config type -->
<!-- cases: N/A 6 duplicate: one context per call -->
<!-- cases: 7 lifecycle: a failed Load keeps the previous snapshot -->
<!-- cases: 8 concurrency: cancellation from another goroutine; no goroutine outlives Load (goleak) -->
<!-- cases: N/A 9 panics: no user code runs except layers -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Loads are serialized and stop while watching
Concurrent `Load` calls SHALL run one at a time: a second call SHALL start reading only after the first returns. While `Watch` is running, `Load` SHALL return an error matching `config.ErrWatching` and change nothing; after `Watch` has returned, `Load` SHALL work again.

#### Scenario: Two overlapping Loads
```gherkin
Given a source whose config.yaml holds v: 1, with slow, an added layer that records "start" and "end" around each Load and blocks its first Load on a channel
And Load A has read config.yaml and is blocked in slow, and config.yaml is then rewritten to v: 2, and Load B has been called
When the consumer releases the channel
Then slow recorded start, end, start, end (no interleaving)
And Lookup("v") returns ("2", true)
```

#### Scenario: Load while watching
```gherkin
Given a testing/synctest bubble with a loaded source src whose Watch <state>
When the consumer calls src.Load(ctx)
Then Load returns <error>

Examples:
  | state                                   | error                                  |
  | is running                              | an error matching config.ErrWatching   |
  | has returned after its context ended    | nil                                    |
```

<!-- cases: N/A 1 default, 2 overrides: serialization is fixed -->
<!-- cases: N/A 3 zero values: a never-loaded source has nothing to serialize against -->
<!-- cases: 4 boundaries: before, during and after Watch -->
<!-- cases: 5 errors: Load while watching matches ErrWatching (a second concurrent Watch is config-reload's) -->
<!-- cases: N/A 6 duplicate: see overlapping Loads -->
<!-- cases: 7 lifecycle: Load, Watch, Load again -->
<!-- cases: 8 concurrency: overlapping Loads, under -race -->
<!-- cases: N/A 9 panics, 10 compatibility: no user code; new module -->

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
<!-- cases: N/A 9 panics, 10 compatibility: no user code; new module -->

### Requirement: Where the source logs
The source SHALL log through `config.Logger(l)` when set, else through the function given to `SetLogf` when the container called it, else through `slog.Default()` looked up at each log call. It SHALL never log a value.

#### Scenario: Log sink
```gherkin
Given a config.Files(config.DotEnvExport()<option>) source whose Load logs one skipped-variable line
And slog.SetDefault set to a recording handler after the source was built (t.Cleanup restores slog's and log's defaults; test not parallel)
And <setlogf>
When the consumer calls Load(ctx)
Then the line was recorded by <sink>

Examples:
  | option               | setlogf            | sink           |
  |                      | SetLogf not called | slog.Default() |
  |                      | SetLogf(f) called  | f              |
  | , config.Logger(l)   | SetLogf not called | l              |
  | , config.Logger(l)   | SetLogf(f) called  | l              |
  | , config.Logger(nil) | SetLogf(f) called  | f              |
```

<!-- cases: 1 default: slog.Default row -->
<!-- cases: 2 overrides: Logger wins over SetLogf; SetLogf over slog.Default -->
<!-- cases: 3 zero values: Logger(nil) means unset -->
<!-- cases: 4 boundaries: slog.Default replaced after the source was built still receives the line -->
<!-- cases: N/A 5 errors: logging cannot fail -->
<!-- cases: N/A 6 duplicate: the last Logger wins -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: logging is stateless -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Structs bind end to end through config.Files
A container whose `ConfigSource` is `config.Files(...)` SHALL bind a struct field from env even when no file holds its key, and SHALL bind a field from every file spelling of its name, so file and env layers reach the binder as one source.

#### Scenario: An env-only key binds although the file lacks it
```gherkin
Given a directory holding config.yaml with "service:\n  name: demo\n"
And clearEnvPrefix(t, "APP_") and t.Setenv("APP_SERVICE_VERSION", "9.9.9")
And root, err := doger.NewWithOpts(&doger.InjectorOpts{ConfigSource: config.Files(config.Dir(dir), config.DotEnv(""))}) returned a nil err
When the consumer calls doger.BindConfig(root, "service", ServiceConfig{})
Then the error is nil and doger.MustInvoke[ServiceConfig](root) is ServiceConfig{Name: "demo", Version: "9.9.9"}
```

#### Scenario: Every file spelling binds the same field
```gherkin
Given a directory holding config.yaml with "server:\n  <spelling>: https://kartala.id\n"
And clearEnvPrefix(t, "APP_")
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
`*config.Source` SHALL satisfy `doger.ConfigSource`, `doger.ConfigLogger` and `doger.ConfigWatcher` structurally and SHALL NOT implement `doger.ConfigValidator`. Its non-test code SHALL NOT import doger and SHALL depend only on `go.yaml.in/yaml/v3` and `github.com/pelletier/go-toml/v2` at v2.4.0 or later (needed for duplicate-key lines). Watching is specified by `config-reload`.

#### Scenario: Compile-time contract checks
```gherkin
Given the config module's test file declaring var _ doger.ConfigSource, var _ doger.ConfigLogger and var _ doger.ConfigWatcher all equal to (*config.Source)(nil)
When the consumer type-asserts any((*config.Source)(nil)) to doger.ConfigValidator
Then the declarations compile and the assertion's ok is false
```

#### Scenario: Dependency footprint
```gherkin
Given the CI job for the config module
When it runs go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./... in config/ without test files
Then every listed path starts with "github.com/kartaladev/doger/config", "go.yaml.in/yaml/v3" or "github.com/pelletier/go-toml/v2"
And config/go.mod requires github.com/pelletier/go-toml/v2 at v2.4.0 or later
```

<!-- cases: N/A 1-9: build-level guarantees -->
<!-- cases: 10 compatibility: golang-lib/config is untouched; this module is separate -->
