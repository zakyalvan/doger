# Design

## Context

See proposal.md for the motivation. The decision record (D1–D45, with options and reasons) is the Claude Doc "doger/config: Config as a First-Class Component". D33–D41 came from the first round of adversarial audits of this change. D42–D45 came from the second round, and those decisions were audited for idiomatic Go before being applied. The specs in `specs/*/spec.md` are the behavior contract. This document explains how the change meets them.

Today:
- `New(packages...) *RootScope` (`root_scope.go:43`) delegates to `NewWithOpts(opts, packages...) *RootScope` (`root_scope.go:67`), which calls `opts.initDefaults()` on the caller's own struct (`injector.go:447`). Neither can fail. `CloneWithOpts` (`root_scope.go:738`) copies the container with replaced options; `Clone()` passes `s.opts.copy()`.
- Extensions read config through `PropertySource` (`properties.go:34`, `Lookup` only). The source is decided at the first property read: `InjectorOpts.PropertySource`, then a service named `NameOf[PropertySource]()`, then `EnvPropertySource("DOGER_")` with its one-to-one mapping.
- `extension.go:86` states the rule this change follows for every extension point: optional interfaces, never new methods on an existing one.
- The health scheduler (`health_monitor.go:107`) is the precedent for an internal, non-service background goroutine that `Shutdown` stops, and for routing callback panics through `HookContainerPanic`.
- The root module requires clockwork, go-type-to-string, testify and goleak, plus `go.yaml.in/yaml/v3` (indirect). `make test` covers the root module. `scheduler/gocron`, `http/*` and `examples/*` are separate workspace modules with `replace github.com/kartaladev/doger => ../..`. The release workflow drops tests, tidies the root, then pins the adapters to the released root.
- `testing/synctest` is available and already used (`scope_test.go`, `health_monitor_test.go`). `testWithTimeout` lives in `main_test.go`.

## Goals / Non-Goals

**Goals:**
- Config that loads, binds and validates once at startup (P1, task groups 1–18), then follows file changes without a restart (P2, groups 19–23).
- No viper, fsnotify, godotenv or mapstructure anywhere.
- Every construction error reaches the caller at the constructor. No error or log line written by the library contains a property value.
- **No mutable process-wide state.** Exactly two process-wide values exist, both immutable once built: the field-plan cache keyed by `(reflect.Type, tagKey)` and the lazily built go-playground validator. Neither is observable as state.

**Non-Goals:**
- The option-based `doger.New(opts ...Option)` and fallible packages: these are `unify-constructor`.
- Rebuilding services when config changes. Remote sources beyond the `config.Layer` hook. A golang-lib/config compatibility shim.
- Policing that a source serves one container (D40). It is documented instead.

## Architecture at a glance

```
+--------------------------------------------------------------------+
| application                                                        |
|   root, err := doger.NewWithOpts(&doger.InjectorOpts{              |
|       ConfigSource: config.Files(...)})                            |
|   err = doger.BindConfig(root, "server", ServerConfig{Port: 8080}) |
+-------------+----------------------------------------+-------------+
              | imports                                | imports
              v                                        v
+-------------------------------+      +-------------------------------+
| root module: doger            |      | config module: doger/config   |
|                               |      |                               |
| contract (stdlib types only)  |<.....| config.Files(opts...)         |
|   PropertySource (unchanged)  | sat- |   -> *config.Source           |
|   PropertyKeys   (optional)   | is-  |                               |
|   ConfigSource                | fies | layers: base, profile, .env,  |
|   ConfigLogger   (optional)   | (no  |   env, added Layer(...)s      |
|   ConfigWatcher  (optional)   | im-  | YAML / TOML / JSON / Codec    |
|   ConfigView     (alias)      | port)| polling watcher (debounced)   |
|   ValueFreeError (marker)     |      | ParseError, FileError,        |
|                               |      |   LayerError (value-free)     |
| construction                  |      |                               |
|   NewWithOpts[Context],       |      | non-test imports: stdlib,     |
|   CloneWithOpts[Context],     |      |   yaml/v3, go-toml/v2         |
|   Must variants               |      | go.mod: require doger +       |
| binder + typed binding        |      |   replace => ../ (tests only) |
| validation (go-playground)    |      +-------------------------------+
| reload: committed view,       |
|   dispatcher, membership      |
+-------------------------------+

  ......> structural satisfaction only: config's non-test code never imports
          doger; doger never imports config, not even in tests.
```

## Decisions

### 1. Constructors return errors, never write the caller's options (D5, D37, D38, D28)

`New` and `Clone` stay as they are: they cannot fail. `NewWithOpts`, `CloneWithOpts` and their `...Context` variants return `(*RootScope, error)`. The `Must` variants panic with the error value itself (`panic(err)`), like `must1` in `utils.go`.

The constructor works on `o := opts.copy()`, never on `opts`:

```
NewWithOptsContext(ctx, opts, packages...)          (ctx nil -> Background)
  |
  |-- 0. o := opts.copy(); logfSet := opts != nil && opts.Logf != nil
  |       o.initDefaults()           (the caller's struct is never written)
  |
  |-- 1. option checks, in order, before any source method is called -------+
  |       a. typed-nil ConfigSource (ptr, map, func, chan, slice kinds)     |
  |          -> ErrInvalidConfigSource                                      |
  |       b. EnvPropertySource with an invalid prefix in o.PropertySource   |
  |          -> ErrInvalidConfigSource                                      |
  |       c. both PropertySource and ConfigSource -> ErrConfigConflict      |
  |                                                                         |
  |-- 2. property source := ConfigSource (decided now, not at first read)   |
  |-- 3. logfSet && ConfigLogger? -> SetLogf(o.Logf)                        |
  |-- 4. lctx := loadContext(ctx, o.ConfigTimeout, o.Clock)  (decision 2)   |
  |      Load(lctx); recover panics ----------------------------------------+
  |      cancel lctx when the constructor returns                           | fails
  |                                                                         v
  |                                          return (nil, err)
  |                                          *ConfigLoadError{Source, Err}
  |                                          or an option-check sentinel;
  |                                          no package ran, nothing built
  |-- 5. run packages in order (a package panic propagates, as today)
  |-- 6. ConfigWatcher? -> remember it; Start/Shutdown drive it (decision 11)
  |-- 7. log "DI: injector created" (only for a returned container)
  v
return (root, nil)
```

- `copy()` covers every field, including the 5 new ones. A reflection test fails when a field is added without being copied. `logfSet` is stored on the root, never in options, so clones and reused options never look "set by the user".
- **Clone sharing (D15).** `Clone()`, or `CloneWithOpts` given `o.ConfigSource` that is the parent's own source as the same pointer (`reflect.ValueOf(src).Kind() == reflect.Pointer && src == parentSrc`), shares the parent's config state. A nil source gives a clone with no config. Any other source, including non-pointer kinds, is loaded for the clone. Comparing only pointers means `==` can never panic.
- **Alternatives.** Recording errors on the root and returning them from `Start` was rejected (D5). Changing `New` too was rejected (D37): it cannot fail, and changing it would rewrite about 800 call sites. Writing defaults back into the caller's options was rejected: it made "Logf set by the user" undecidable.

### 2. The Load context: real clock vs a user clock

- **Real clock (the default `InjectorOpts.Clock`):** `context.WithTimeout(ctx, ConfigTimeout)`. The stdlib keeps the earlier of the caller's deadline and `now + ConfigTimeout`. `ConfigTimeout` 0 means `DefaultConfigTimeout`; a negative value means no timeout (ctx alone bounds `Load`). This path also covers `testing/synctest`, because the real clock is fake inside a bubble.
- **A user clock** (any other `Clock`): a small shim context.
  - `Deadline()` is `Clock.Now() + t`, documented as being in the clock's time base.
  - `Done()` closes when either the parent's `Done` or the clock timer fires.
  - `Err()` is the parent's error if the parent ended first, otherwise `context.DeadlineExceeded`. It is stored before `Done` closes and never blocks.
  - It implements `AfterFunc(func()) (stop func() bool)`, so `context.WithCancel(shim)` starts no goroutine.
  - Caller deadlines and clock deadlines are never compared: they are different time bases.
- The constructor cancels the Load context when it returns. A source that keeps it sees it done (`Canceled`, unless its deadline had already passed).
- Cancellation is cooperative. The constructor waits for `Load` to return. A `Load` that ignores its context and returns nil after the deadline succeeds; this is documented.
- **Alternatives.** clockwork's `WithTimeout` was rejected: on a fake clock it starts a goroutine, and its `Err` blocks. Comparing wall time with the clock's time was rejected as meaningless.

### 3. The property source and the source contract (D35, D36)

```
first property read (today)               construction with a ConfigSource
---------------------------               --------------------------------
InjectorOpts.PropertySource set? --yes--> use it
        | no                                 decided at once: the ConfigSource
        v                                    (both fields set -> ErrConfigConflict)
service named NameOf[PropertySource]? -yes-> use it
        | no
        v
EnvPropertySource("DOGER_") as a default
```

- `PropertySource` is unchanged. `PropertyKeys` is optional (`extension.go:86`): `Keys(prefix)` returns every key whose uniform form matches the prefix's uniform form segment by segment, spelled so that the same source's `Lookup` finds it. Results are sorted byte-wise; `Keys("")` on a source returns every key.
- `EnvPropertySource` keeps returning `PropertySource`; godoc says the value implements `PropertyKeys`. `MapSource` is a defined map type: it is not copied, and godoc says not to mutate it while in use. `Properties.Keys` returns keys relative to the view, drops keys its own grammar rejects, and returns `[]` for an empty or invalid prefix.
- **Two fields stay (D36).** `ConfigSource` says that the container loads and may watch the source; `PropertySource` stays a bare lookup.
- **Alternatives.** Adding `Keys` to `PropertySource` (the old D30) was rejected: it breaks every implementation. Returning keys in canonical spelling was rejected: a `MapSource` stored as `allowedhosts` could not find them.

### 4. Uniform form, the env mapping and the key grammar (D39)

- **Grammar:** a full key is the extension's name (optionally `/instance`), then dotted kebab segments, each optionally followed by `[i]` (decimal, no leading zero) or `[k]` suffixes.
- **Uniform form**, per segment: lowercase, with `-` and `_` removed. Bracket contents are untouched, except that `[k]` with `k` in `[a-z0-9]+` equals `.k` and an all-digit `.n` equals `[n]`. An instance segment never folds. Prefix matching is segment-aware.

```
canonical key            uniform form          env name (prefix DOGER_)
-----------------------  --------------------  -------------------------------
redis.pool-size          redis.poolsize        DOGER_REDIS_POOLSIZE
redis/queue.addr         redis/queue.addr      DOGER_REDIS__QUEUE_ADDR
server.hosts[0]          server.hosts[0]       DOGER_SERVER_HOSTS_0
server.labels[team]      server.labels.team    DOGER_SERVER_LABELS_TEAM
server.labels[team/core] server.labels[team/core]  (not addressable from env)

forward:  upper; "." -> "_"; "-" removed; "/" -> "__"; "[i]" -> "_I"; "[k]" -> "_K"
          (k must match [a-z0-9]+, else not addressable)
reverse:  "__" after the name = instance; an all-digit segment = [n];
          other segments = lowercase dash-free names; names that do not parse
          (leading zero, "__" later, all-digit first segment, "___",
          lowercase) are skipped
```

- **Prefix rule (both modules):** `-` and `.` become `_`, the result is uppercased, and `_` is appended when missing; `""` means off. A prefix still outside `[A-Z0-9_]`:
  - `config.EnvPrefix`: `Load` fails with `ErrInvalidOption`;
  - `EnvPropertySource` installed via options: the constructor fails with `ErrInvalidConfigSource` (`DI: config: invalid env prefix "<prefix>"`; a prefix is not a secret);
  - elsewhere: every lookup misses, and the decision log line names the prefix.

  `EnvPropertySource` never panics.
- **One golden file**, `testdata/envmapping.txt` in the root, holds the forward and reverse table. Both modules' tests read it, so the duplicated mapping code (the config module may not import doger) cannot drift.
- **Alternatives.** Keeping the old one-to-one mapping was rejected (decided item 7). A panicking `EnvPropertySource` was rejected: a panic needs a `Must` name.

### 5. The binder (decided items 4, 9)

**Field names.** Split before byte i when:
- (a) `s[i]` is upper and `s[i-1]` is lower;
- (b) `s[i]` is upper, `s[i-1]` is a digit and `s[i+1]` is lower;
- (c) `s[i]` is upper, `s[i-1]` is upper and `s[i+1]` is lower, the upper run ending at i-1 has 2 or more letters, and it is not the case that `s[i+1]=='s'` followed by the end or a non-lowercase letter.

`_` also splits, and empty parts are dropped. Letter-to-digit never splits.

| Field | Key | Field | Key |
|---|---|---|---|
| IssuerURI | issuer-uri | HTTPServerURL | http-server-url |
| TLSCert | tls-cert | ARN | arn |
| OAuth2Token | oauth2-token | PDFs | pdfs |
| IPv6Addr | ipv6-addr | Base64Data | base64-data |
| AllowedURLs | allowed-urls | X509Cert | x509-cert |
| IDs | ids | HTTP2Server | http2-server |
| S3Bucket | s3-bucket | SHA256Sum | sha256-sum |
| HTTPServer | http-server | UTF8String | utf8-string |
| ID | id | K8sNamespace | k8s-namespace |
| API_Key | api-key | Use2FAToken | use2fa-token |
| ABTest | ab-test | XAxis | xaxis |
| Use2FA | use2fa | ATest | atest |
| Foo__Bar | foo-bar | | |

A non-ASCII name is `ErrInvalidConfigTarget`, but only for an untagged field that is not skipped.

**Plan.** A field plan is built once per `(reflect.Type, tagKey)` and cached in an immutable `sync.Map` entry. Plan building rejects up front:
- unsupported kinds (arrays, chan, func, complex, uintptr, `unsafe.Pointer`, interfaces, maps with non-string keys);
- invalid or all-digit tags;
- two fields with one uniform name (outer vs promoted, or sibling promoted);
- unexported embedded pointers;
- any type cycle among bound non-scalar types (recursion).

All of these return `ErrInvalidConfigTarget`. Types converted as scalars, `cfg:"-"` fields and unexported fields are exempt from the recursion check.

**Bind walk.**

```
defaults d --(copy-on-write)--> result r   (paths the binder writes are copied;
      |                                      untouched pointers stay shared)
      v
for each planned field f (prefix + key(f)):
   source has PropertyKeys?
     yes: ks := Keys(fieldKey) -- every lookup uses the keys exactly as returned
          (several spellings of one uniform key: canonical wins, else smallest)
     no : Lookup(canonical key); a list/map/pointer field -> ErrInvalidConfigTarget
   |
   +-- scalar (exact time.Duration, url.URL, *T implements TextUnmarshaler,
   |   then by kind: strconv base 10, Bits() respected, []byte raw)
   |      convert; failure -> *ConfigBindError{Key, Type}  (never the value)
   +-- list:    indices from ks, de-duplicated in uniform form; need 0..max,
   |            max <= 10000, checked before allocating; else one scalar is
   |            comma-split (elements trimmed, empties kept); a scalar in a
   |            higher layer masks lower indices; element structs start at zero
   +-- map:     entries merge by key over the default map; struct entries merge
   |            field by field; nil default map stays nil without entries
   +-- pointer: allocated only when ks is non-empty under it
   +-- struct:  recurse (embedded exported structs promote; tagged embed nests)
user panics (UnmarshalText, Lookup, Keys) -> ErrConfigConversion, naming the key and
the panic type only
```

- **Alternatives.** A default-tag mechanism was rejected (D6). A `SetDefaults()` method was deferred. mapstructure was rejected (decided item 9). A deep copy of the defaults was rejected: it changes the identity of non-config pointers such as `*http.Client`.

### 6. Typed binding and registration (D8a, D9, D10, D43)

- `BindConfig(i, prefix, defaults T)`, `BindConfigNamed(i, name, prefix, defaults T)` and `ResolveConfig(i, prefix, defaults T) (T, error)`. `T` is always inferred. A nil injector means `DefaultRootScope`; a typed-nil injector is `ErrInvalidConfigTarget`. `T` must be a struct type, otherwise `ErrInvalidConfigTarget`. The prefix is `""` or a valid key (`server`, `redis/queue`).
- **Order inside `BindConfig`:**
  1. Bind and validate against the committed view V, with no lock held (user code runs here).
  2. Under the commit semaphore, CAS-add a *pending* entry to the entry set, then release.
  3. With no lock held, register `T`, then `ConfigValue[T]` (`ConfigValueName(name)` for the named form). Registration hooks are user code. If either registration fails or panics, remove the one already registered, CAS-remove the entry, and return the error or re-panic.
  4. If a reload committed after V, it has already rebound and published the pending entry, so no update is lost.
How a bind and a reload interleave (no update is lost, no lock is held around user code):

```
 BindConfig goroutine                          watcher goroutine (reload)
 --------------------                          --------------------------
 bind + validate against committed view V      set := entries.Load()
 (no lock; user code runs here)                rebind set from S' (no lock)
 take commit semaphore                                |
 CAS-add pending entry e  ---------------+            |
 release semaphore                       |     take commit semaphore
 register T, then ConfigValue[T]         +---> entries.Load() != set (e was added)
 (no lock; registration hooks run here)        release; rebind only e from S'; retry
 failure? remove the first registration,       take semaphore; set unchanged:
   CAS-remove e, return the error              view.Store(S'); publish all incl. e
                                               release
 result: e's handle holds S' (the newer view), never V after S' committed
```

- **Duplicates.** A non-default registration under the name gives `ErrConfigAlreadyBound` and never panics. An unresolved `AsDefault` registration is replaced; a resolved one gives `ErrConfigAlreadyBound` (`default-registrations`). A child scope may shadow a parent.
- **Phases.** `BindConfig` and `BindConfigNamed` are refused with `ErrStartAfterShutdown` once the target scope's container has begun shutting down. `ResolveConfig` and `BindProperties` work in every phase: they read the last committed view once per call.
- **`ConfigValue[T]`** is `struct{ c *cell[T] }`. Copies share state, and the zero value works (`Get` returns zero `T`; `OnChange` returns a no-op stop). `Get` is one atomic load. Its result is read-only by contract, because maps and slices are shared with the registered `T`.
- **Alternatives.** A `ConfigBinding` package was dropped (D8a). `BindOption`/`ConfigDefaults` were replaced by the `defaults` argument (D43). Registering under the commit lock was rejected: registration hooks would run under it.

### 7. Validation and value-free text (D7, D34, D44)

```
which validator?                              validating one value v (a copy)
----------------                              -------------------------------
InjectorOpts.ConfigValidator != nil? -yes-> it    tags (or the replacement) on &copy
        | no                                        | fails           | passes
        v                                           v                 v
go-playground (sync.OnceValue, the root    *ConfigValidationError   top-level T has
default; tag names come from the field-    {Violations sorted by    Validate() error
path -> key map, never its namespaces)      key; key + rule only}    (value or pointer
                                            matches ErrInvalidConfig  receiver, promoted
                                                                      counts)? call it
error text: DI: config: invalid "<prefix or name>" (<T>): <ValueFreeError text | type>
            (ValidateConfig has no section: DI: config: invalid <T>: ...)
```

- `ValueFreeError` (`interface{ error; ValueFree() }`) is found with `errors.AsType[ValueFreeError]`. Context errors also count as value-free. A cause is always reachable through `errors.Is` and `errors.As`.
- Fields skipped by `cfg:"-"`, and unexported fields, are not validated by the default validator (via `StructFiltered`). A replacement validator receives a pointer to a copy; its changes never reach the bound value. A malformed `validate` tag is `ErrInvalidConfigTarget` with a fixed message. A validator panic is `ErrInvalidConfig` (`validator panic of type T`).
- **Cost (D45, honest):** go-playground adds 7 modules (mimetype, locales, universal-translator, go-urn, x/crypto, x/sys, x/text) to every consumer's module graph, `go.sum` and binary. Only building the validator instance is lazy.
- **Alternatives.** A go-playground adapter module was rejected: D7 says tags are always checked by default. A source-level validator was dropped (D34): a Go method set cannot depend on an option.

### 8. The config module

`config.Files(opts...) *Source`. Options are checked at `Load`, not when `Files` is called. Paths are resolved to absolute at the first `Load`.

```
                                       server.port
lowest  +----------------------------+ ------------
        | struct defaults (binder)   |  8000         (kept when no layer has the key)
        +----------------------------+
        | base: ./config.yaml        |  8080
        |       ./config/config.yaml |               (./config wins key by key; a list
        +----------------------------+                comes whole from the highest file)
        | profile: config.prod.yaml  |  9090         APP_PROFILE=prod (process only)
        +----------------------------+
        | .env: ./.env               |  9191         never os.Setenv unless DotEnvExport
        +----------------------------+
        | process env: APP_*         |  9292         read once, at Load
        +----------------------------+
highest | config.Layer(name, src)    |  (vault)      re-read at each poll, StageTimeout
        +----------------------------+
config.LayerOrder(LayerBase, ..., "vault") reorders; a built-in layer left out is skipped,
an added layer left out is ErrInvalidOption.

Load(ctx) (serialized): read layers -> flatten -> merge in uniform form -> snapshot
          {sorted keys, map} --atomic.Pointer.Store--> Lookup / Keys (lock-free; one call
          sees one snapshot). A cancelled Load keeps the previous snapshot.
```

- **Discovery.** Search `.` then `./config` for `config.{yaml,yml,toml,json}` and `Codec` extensions:
  - two formats with one base name fail as ambiguous;
  - no file is fine;
  - a set profile with no file fails with `ErrProfileNotFound`.

  `File(...)` turns off discovery and the profile overlay. `Dir` together with `File` is `ErrInvalidOption`. `Format` with `File` forces the format. The `ProfileEnv` variable is read only from the process, never from `.env`, and never becomes a key. Profile names must match `^[a-z0-9][a-z0-9_-]*$`, so no path escapes. Symlinks are followed (no `os.Root`). Only regular files are read (`os.Stat` before `Open`), through `io.LimitReader(MaxFileSize+1)`.
- **File keys:**
  - A key written as `"[...]"` is checked first and kept verbatim.
  - Otherwise keys are split on `.`, with `/` allowed only in the first segment, and each segment normalized with the field-naming rule.
  - An all-digit key becomes an index.
  - Duplicates are checked in uniform form.
  - Sequences become `[i]`, nested `[i][j]`; `null` means absent.
  - Invalid keys (empty, whitespace, non-ASCII, leading `-`, a bare `x[0]`) are `*ParseError` "invalid key".
  - Caps: 64 segments per key ("too deep"), 100,000 keys per file ("too many keys").
- **YAML** is decoded into `any` first, which keeps yaml.v3's alias-ratio guard. A `yaml.Node` walk then gives literal scalar text, line numbers, alias resolution and `<<` merge keys. A trailing `---` is allowed only when it holds an empty `!!null`. A self-referencing anchor is "excessive aliasing".
- **Rendering.**
  - YAML keeps its literal text (`True`, `yes`, `1.0`, `0x1F`).
  - JSON uses `UseNumber` literals; trailing data is a syntax error; an empty file or top-level `null` loads as no keys.
  - TOML uses go-toml/v2 (v2.4.0 or later, for duplicate-key lines): ints decimal, floats `strconv.FormatFloat('g', -1)`, datetimes RFC 3339, local date/time types as TOML text.
  - `Decoder` output is rendered by Go type: integers in decimal, `float64` with `strconv.FormatFloat(f, 'g', -1, 64)`, `bool` as `true`/`false`, strings as they are, `time.Time` as RFC 3339, `json.Number` as its literal. `map[any]any` is "not a mapping"; any other type is "unsupported value".
- **`.env` grammar:**
  - `KEY=VALUE`, with an optional `export `; spaces around `=` are ignored; keys match `[A-Za-z_][A-Za-z0-9_]*`.
  - `#` starts a full-line comment, and ` #` an inline comment on unquoted values; unquoted values are trimmed.
  - Single quotes are literal. Double quotes support `\n`, `\t`, `\"` and `\\`, and keep other pairs as written.
  - No `${}` expansion and no multi-line values; CRLF and a BOM are accepted; a later line wins.
  - An invalid line is a `*ParseError` "invalid line".
  - `.env` keys go through the same prefix and mapping as process env; with `EnvMapper`, the process is consulted first, then `.env`.
- **`DotEnvExport()`** sets only unset variables (prefixed or not), once, at the first `Load`, and logs the names it skipped. The godoc warns about `os.Setenv` with cgo.
- **Errors.**
  - Sentinels are `type sentinelError string` implementing `ValueFree`.
  - `*FileError{File, Err}` prints `<file>: does not exist | file too large | not a regular file`.
  - `*ParseError{File, Line, Category}` prints `<file>:<line>: <category>`, or `<file>: <category>` when the line is unknown.
  - `*LayerError{Layer, Err}` prints `layer "<name>": <value-free text | type>`.
  - None of them carries a `config:` prefix, and none wraps parser text.
- **Logging:** an explicit `config.Logger(l)` wins over `SetLogf`, which wins over `slog.Default()`, looked up at each call.
- **Alternatives.** viper, fsnotify and godotenv were rejected (spikes 1–5). `os.Root` was dropped: it rejects absolute in-directory symlinks (Docker secrets), and profile validation already prevents traversal.

### 9. Reload: the root owns the commit (D14, D33)

```
 watcher goroutine (source.Watch)          root config state
 --------------------------------          -----------------
 poll; stable fingerprint differs
 from the last *staged* one?
   | yes: parse + merge -> S'
   | (or err if staging failed)
 changed(ctx, S', err) ------------------> err != nil or shutting down? -> false
                                           set := entries.Load()      (CAS set, immutable)
                                           rebind every live-owned entry of set
                                             from S' with NO lock held  (user code)
                                           all ok? no -> enqueue warning + hook event;
                                                          return false
                                           take commit semaphore (chan, select ctx.Done)
                                           entries.Load() == set?
                                             no  -> release; rebind only the added
                                                    entries; retry
                                             yes -> view.Store(S')       (committed view)
                                                    publish each ConfigValue cell
                                                    enqueue notifications (non-blocking:
                                                     coalescing map + cap-1 chan)
                                                    release; return true
 true:  adopt S' as the current state                          |
 false: drop S'                                                v
                                           dispatcher goroutine: for each section whose
                                           raw key/value set changed, call subscribers
                                           outside every lock; hooks get
                                           context.WithoutCancel(startCtx); panics go to
                                           HookContainerPanic("config-on-change" |
                                           "config-reload-failed"), else are logged by type
```

- **Every read path** (property reads, `ResolveConfig`, `BindProperties`, a `Properties` value obtained from a view) reads one committed view per call or value, through `atomic.Pointer[viewBox]`. A box is used because the dynamic type of the view changes. No reader mixes snapshots.
- **No lock around user code.** `Validate`, `UnmarshalText`, the source's `Lookup`/`Keys` on the staged view and registration hooks never run under the commit semaphore. The semaphore is a `chan struct{}` of capacity 1, so synctest sees it and `ShutdownWithContext` can abandon it on `ctx.Done()`.
- **Owners and membership.**
  - Each entry holds its owners (the binding container and every clone that copied the registration) as `weak.Pointer[RootScope]`, pruned at each commit.
  - An entry is rebound, and can veto a reload, only while one owner is a started member that has not shut down.
  - Membership is a set of containers whose `Start` succeeded and that have not shut down. A Shutdown without a Start, and a double Shutdown, are no-ops.

```
              root.Start()          clone.Start()        clone.Shutdown()      root.Shutdown()
members: {} ------------> {root} ------------> {root,clone} ------> {root} ------------> {}
           launch watcher +                                                 cancel watch; stop
           dispatcher (ctx from                                             dispatcher; wait for
           context.Background, like                                         both until the
           the health scheduler)                                            Shutdown ctx ends
{} after all stopped, then clone.Start(): Watch is called again, after the previous Watch
returned; config.Files rejects a concurrent second Watch with ErrWatching.
```

- **Shutdown order:** cancel the watch, stop accepting changes (`changed` returns false and enqueues nothing), shut down services as today, then wait for the watcher and dispatcher until the Shutdown ctx ends.
- **D42:** a subscriber or hook must call `go root.Shutdown()`. A synchronous call waits on itself; with a deadline, every service the shutdown reaches afterwards is skipped. Godoc documents this (as for Starters), and there is no scenario for the synchronous case.
- **A watch that ends:** with nil or `ctx.Err()` after its ctx ended, it is a normal stop. A nil return before that means "not watching" (`DisableWatch`). Any other error or panic is logged by type as `ErrConfigWatchFailed` and sent to the hooks; a panic goes to `HookContainerPanic("config-watch")` when that hook is set.
- **Which hooks fire:** for a failing section, its binding container's hooks and `Logf`; for a staging failure, the original root's.
- **Alternatives.** The source owning the swap (`changed(next) bool` without a root view) was rejected: the root could not publish after the commit. A mutex was rejected: it is held across user code and synctest cannot detect it. A generation counter was rejected: it can be read torn, and a cap on retries leaves exhaustion undefined.

### 10. Polling (config.Files.Watch)

```
every PollInterval (default 10s; 0 = default; negative = ErrInvalidOption at Load):
  fp := sha256 over the watched set, read with Load's reader (regular files only,
        size-limited); a missing file or an empty one that was non-empty at Load
        -> staging error
  watched set = files Load read + the .env file if it existed at Load
                (DotEnv(path) path; none with DotEnv(""))
  fp == the previous poll's fp?          (debounce: covers changes and failures)
     no  -> wait
     yes -> fp != lastStaged?
              no  -> wait (a rejected content warns once)
              yes -> stage (added layers re-read under StageTimeout);
                     lastStaged = fp, except when the rejection came from a layer
                     error or validator state (then retried next poll)
Kubernetes ConfigMap: config.yaml -> ..data/config.yaml, ..data -> ..2026_10_30_v2/
                      the swap changes the bytes read -> new fp
new files appearing after Load are not noticed (documented)
```

### 11. Errors

| Sentinel | Means |
|---|---|
| `ErrConfigLoadFailed` | `Load` failed or panicked (`*ConfigLoadError{Source reflect.Type; Err error}`) |
| `ErrConfigWatchFailed` | `Watch` ended abnormally |
| `ErrConfigConversion` | a value could not be converted (`*ConfigBindError{Key string; Type reflect.Type}`) |
| `ErrInvalidConfig` | validation failed (`*ConfigValidationError{Violations []ConfigViolation}`, `ConfigViolation{Key, Rule string}`) |
| `ErrInvalidConfigTarget` | bad `T`, prefix, tag, kind, recursion, a name conflict, or a list/map/pointer field on a Lookup-only source |
| `ErrInvalidConfigSource` | typed-nil source, or an invalid env prefix in options |
| `ErrConfigConflict` | both `PropertySource` and `ConfigSource` set |
| `ErrConfigAlreadyBound` | a non-default (or resolved default) registration exists |

- Texts start with `DI: config: `. Types use `reflect.Type.String()` (`app.ServerConfig` in specs). Services use `NameOf`.
- Each error type implements `Is` for its sentinel, and `GoString()` with type-only output, so `%#v` never prints a value; `%v`, `%+v`, `%s` and `%q` already use `Error()`.
- Load text is `DI: config: load failed: source <T>: <value-free text | context text | cause type | "panic of type T">`. A panic value that is an error is kept as the cause; `panic(nil)` shows `*runtime.PanicNilError`.

### 12. Module, CI and release

```
repo/
  go.mod                (root: + validator/v10; no tool directive)
  go.work               (+ ./config)
  tools/go.mod          (golang.org/x/vuln pinned; never required by any module)
  config/go.mod         require github.com/kartaladev/doger v0.0.0-00010101000000-000000000000
                        replace github.com/kartaladev/doger => ../
                        require go.yaml.in/yaml/v3, github.com/pelletier/go-toml/v2 >= v2.4.0
CI (test.yml / lint.yml / security.yml):
  root:   make test, make lint
  config: (cd config && go test -race ./... && golangci-lint run ./...)
  GOWORK=off job: each published module builds/tests with its replace and runs
                  go mod tidy -diff (go.mod/go.sum complete)
  footprint: (cd config && go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' .)
             lists only the config package, yaml/v3 and go-toml/v2
  govulncheck: per module dir: go tool -modfile=<path>/tools/go.mod govulncheck ./...
  trivy: .trivyignore entries per CVE id, each with exp: date and a reason
release.yml (existing order kept):
  test -> drop tests (keep *_example_test.go) -> go mod tidy (root)
       -> (cd config && go mod tidy)
       -> pin adapters (http/*, scheduler/*) and config only if still required:
          grep -q 'kartaladev/doger ' config/go.mod && go mod edit -require=...@$SEMVER
       -> tag <semver>, http/<x>/<semver>, scheduler/<x>/<semver>, config/<semver>
```

## Risks / Trade-offs

- **[Two API breaks (`NewWithOpts`/`CloneWithOpts` signatures, env names) plus one behavior change (options not written)]** → No tag exists and no app is deployed. gopatch rewrites the call sites. The upgrading note lists each break with its migration. `New`/`Clone` call sites are untouched.
- **[go-playground costs every consumer 7 modules, even without config (D7)]** → The cost is stated plainly. govulncheck runs per module, and trivy ignores are per CVE with an expiry. `InjectorOpts.ConfigValidator` avoids the code path but not the dependency.
- **[The Spring env mapping exists in two modules]** → One golden file is read by both test suites.
- **[Distinct keys share env variables; old dashed extension names are reinterpreted as instances]** → This is Spring's trade-off. Scenarios pin it, and the upgrading note warns about `DOGER_EXAMPLE__KV_ADDR`.
- **[A synchronous `Shutdown` from a callback deadlocks]** → Documented in godoc next to the existing Starter rule. The supported scenario is `go root.Shutdown()`.
- **[A slow subscriber delays later notifications]** → The dispatcher coalesces to the latest value per section, so nothing piles up. Godoc tells subscribers to stay fast.
- **[Reload commit retries while binds keep arriving]** → Each retry rebinds only newly added entries. The loop ends unless binds never stop, which a real application does not do.
- **[A fake-clock context differs from a real one]** → It is used only for a user `Clock`. `Deadline()` is documented as clock-based. With the real clock, the stdlib context is used.
- **[A `Load` that ignores its context blocks construction]** → Documented. `NewWithOptsContext` plus a cooperative source is the remedy.
- **[One source in two unrelated containers]** → Undefined behavior, documented on `ConfigSource` (D40). Pointer-only clone sharing never panics.
- **[synctest cannot see `propertySourceDecision`'s existing mutex]** → `testWithTimeout` wraps every synctest test from outside the bubble.
- **[TOML datetime and float rendering differs from YAML literals]** → Pinned by scenarios and documented: an int field accepts TOML `1.0` but rejects YAML `1.0`.

## Public API & Defaults

**Root module `doger`.** BREAKING items are flagged.

```go
// construction
func New(packages ...func(Injector)) *RootScope                                   // unchanged
func NewWithOpts(opts *InjectorOpts, packages ...func(Injector)) (*RootScope, error) // BREAKING
func NewWithOptsContext(ctx context.Context, opts *InjectorOpts, packages ...func(Injector)) (*RootScope, error)
func MustNewWithOpts(opts *InjectorOpts, packages ...func(Injector)) *RootScope
func (s *RootScope) Clone() *RootScope                                            // unchanged
func (s *RootScope) CloneWithOpts(opts *InjectorOpts) (*RootScope, error)         // BREAKING
func (s *RootScope) CloneWithOptsContext(ctx context.Context, opts *InjectorOpts) (*RootScope, error)
func (s *RootScope) MustCloneWithOpts(opts *InjectorOpts) *RootScope
func (s *RootScope) AddConfigReloadFailedHook(fn func(ctx context.Context, scope *RootScope, err error))

// InjectorOpts additions (BEHAVIOR CHANGE: constructors never write *InjectorOpts)
ConfigSource           ConfigSource     // nil: env-only default property source
ConfigTimeout          time.Duration    // 0: DefaultConfigTimeout; < 0: no timeout
ConfigValidator        ConfigValidator  // nil: go-playground
ConfigTagKey           string           // "": DefaultConfigTagKey
HookConfigReloadFailed []func(ctx context.Context, scope *RootScope, err error) // nil: warn only

const DefaultConfigTimeout = 10 * time.Second
const DefaultConfigTagKey = "cfg"

// contract
type PropertySource interface{ Lookup(key string) (string, bool) }                  // unchanged
type PropertyKeys interface{ Keys(prefix string) []string }
type ConfigSource interface{ PropertySource; PropertyKeys; Load(ctx context.Context) error }
type ConfigLogger interface{ SetLogf(logf func(format string, args ...any)) }
type ConfigView = interface{ Lookup(key string) (string, bool); Keys(prefix string) []string }
type ConfigWatcher interface {
	Watch(ctx context.Context, changed func(ctx context.Context, next ConfigView, err error) (commit bool)) error
}
type ConfigValidator interface{ Validate(v any) error }
type ValueFreeError interface{ error; ValueFree() }
type MapSource map[string]string // Lookup, Keys
func (p Properties) Keys(prefix string) []string

// binding
func BindConfig[T any](i Injector, prefix string, defaults T) error
func BindConfigNamed[T any](i Injector, name, prefix string, defaults T) error
func ConfigValueName(name string) string
func ResolveConfig[T any](i Injector, prefix string, defaults T) (T, error)
type ConfigValue[T any] struct{ /* pointer to a shared cell */ }
func (v ConfigValue[T]) Get() T
func (v ConfigValue[T]) OnChange(fn func(T)) (stop func())
func StaticConfig[T any](v T) ConfigValue[T]
func BindProperties(p Properties, target any) error
func ValidateConfig(i Injector, v any) error

// errors
var ErrConfigLoadFailed, ErrConfigWatchFailed, ErrConfigConversion, ErrInvalidConfig,
	ErrInvalidConfigTarget, ErrInvalidConfigSource, ErrConfigConflict, ErrConfigAlreadyBound error
type ConfigLoadError struct{ Source reflect.Type; Err error }   // Is(ErrConfigLoadFailed), Unwrap
type ConfigBindError struct{ Key string; Type reflect.Type }    // Is(ErrConfigConversion)
type ConfigValidationError struct{ Violations []ConfigViolation } // Is(ErrInvalidConfig)
type ConfigViolation struct{ Key, Rule string }
```

- **BREAKING:** `EnvPropertySource` variable names (Spring mapping, `__` for instances) and prefix normalization (`myapp_` reads `MYAPP_`, `my-app` reads `MY_APP_`).
- `HookContainerPanic` gains the points `config-watch`, `config-reload-failed` and `config-on-change`; the hook receives the raw value, which may hold a secret. The godoc of `Logf` no longer says nil means "no logging" for config warnings (they go to `slog.Default()` when `Logf` is unset). The godoc of `ErrStartAfterShutdown` adds `BindConfig`/`BindConfigNamed`.

**Config module `config`.**

```go
func Files(opts ...Option) *Source
func (s *Source) Load(ctx context.Context) error
func (s *Source) Lookup(key string) (string, bool)
func (s *Source) Keys(prefix string) []string
func (s *Source) SetLogf(logf func(format string, args ...any))
func (s *Source) Watch(ctx context.Context, changed func(ctx context.Context, next View, err error) bool) error
type View = interface{ Lookup(key string) (string, bool); Keys(prefix string) []string }

type Option func(*options)
func Dir(paths ...string) Option
func File(paths ...string) Option
func Name(base string) Option
func Format(f FileFormat) Option          // "" = by extension
func Codec(ext string, d Decoder) Option  // ext starts with ".", case-insensitive
func Profile(name string) Option          // "" = no profile, overrides the variable
func ProfileEnv(name string) Option
func EnvPrefix(prefix string) Option      // "" = off
func EnvMapper(fn func(key string) string) Option // returns the full variable name
func DotEnv(path string) Option           // "" = off
func DotEnvExport() Option
func Layer(name LayerName, src LayerSource) Option
func LayerOrder(names ...LayerName) Option
func Logger(l *slog.Logger) Option        // nil = unset
func DisableWatch() Option
func PollInterval(d time.Duration) Option // 0 = DefaultPollInterval; < 0 = ErrInvalidOption
func StageTimeout(d time.Duration) Option // 0 = DefaultStageTimeout; < 0 = none
func MaxFileSize(n int64) Option          // 0 = DefaultMaxFileSize; < 0 = ErrInvalidOption

type FileFormat string
const (YAML FileFormat = "yaml"; TOML FileFormat = "toml"; JSON FileFormat = "json")
type Decoder interface{ Decode(data []byte) (map[string]any, error) }
type LayerSource interface{ Load(ctx context.Context) (map[string]string, error) }
type LayerName string
const (LayerBase LayerName = "base"; LayerProfile LayerName = "profile"; LayerDotEnv LayerName = "dotenv"; LayerEnv LayerName = "env")
const DefaultPollInterval = 10 * time.Second
const DefaultStageTimeout = 10 * time.Second
const DefaultMaxFileSize = 4 << 20

type ParseCategory string
const (CategorySyntax ParseCategory = "syntax error"; CategoryDuplicateKey = "duplicate key";
	CategoryMultipleDocuments = "multiple documents"; CategoryNotMapping = "not a mapping";
	CategoryExcessiveAliasing = "excessive aliasing"; CategoryInvalidLine = "invalid line";
	CategoryInvalidKey = "invalid key"; CategoryTooDeep = "too deep";
	CategoryTooManyKeys = "too many keys"; CategoryUnsupportedValue = "unsupported value")
type ParseError struct{ File string; Line int; Category ParseCategory } // ValueFree
type FileError struct{ File string; Err error }                        // ValueFree, Unwrap
type LayerError struct{ Layer LayerName; Err error }                   // ValueFree, Unwrap
var ErrAmbiguousFormat, ErrProfileNotFound, ErrInvalidOption, ErrFileTooLarge,
	ErrNotRegularFile, ErrWatching error // type sentinelError string, ValueFree
```

Defaults are as in the proposal's Defaults & Control table; every one is overridable by the field or option named there.

**Migration for consumers:**
- `doger.NewWithOpts(o)` becomes `root, err := doger.NewWithOpts(o)`, or `doger.MustNewWithOpts(o)` in tests.
- `root.CloneWithOpts(o)` becomes `clone, err := ...`, or `root.MustCloneWithOpts(o)`.
- Code that read defaults back from its own `*InjectorOpts` after construction must read them from the container instead (no repository code does).
- Rename env variables, for example `DOGER_REDIS_POOL__SIZE` to `DOGER_REDIS_POOLSIZE` and `DOGER_REDIS___QUEUE_ADDR` to `DOGER_REDIS__QUEUE_ADDR`. Check the variables of dashed extension names.

## Migration Plan

Task-group dependencies (P1 = groups 1-18, P2 = groups 19-23, 24 = integration only):

```
[1 workspace, CI, release]
    |
[2 key grammar] -> [3 env mapping, prefixes, golden file, FuzzKeyGrammar] -> [4 PropertyKeys, MapSource]
                                                                                   |
[5 fallible constructors, ConfigSource, ConfigLoadError, ValueFreeError, options copy]
    |                 \
[6 option checks]   [7 Load contexts, deadlines, clone contexts]
    |
[8 binder: scalars, names, conversions, ResolveConfig] -> [9 lists, maps, pointers]
    -> [10 uniform form, tags, target checks] -> [11 BindConfig, handles, BindProperties]
    -> [12 validation]
[13 formats, flattening, rendering] -> [14 discovery, profiles, paths, limits]
    -> [15 env layer, keys] -> [16 .env, layer precedence, logging]
    -> [17 added layers, options, errors, Load lifecycle] -> [18 root + config end to end]
[19 watcher lifecycle, hooks API, dispatcher] -> [20 commit, read paths]
    -> [21 failures, subscribers, shutdown] -> [22 clones and owners]
    -> [23 config.Files polling]
[24 integration and docs] (after all)
```

1. Land on a branch. It is untagged, so there is no deprecation window.
2. Rewrite call sites with gopatch: `NewWithOpts` and `CloneWithOpts` in the root, `scheduler/gocron`, `http/*`, `examples/*`, `docs/` and the README.
3. Build and test every workspace module. Run the `GOWORK=off` job.
4. Rollback is a revert; there is no persistent state.

## Test Strategy

**Kinds:**
- table-driven unit tests with named subtests (testify; `require` for setup, `assert` for checks);
- `-race` on every concurrency scenario;
- goleak through `TestMain` in both modules (goleak tests are non-parallel and use `goleak.IgnoreCurrent()`);
- `testing/synctest` for time and goroutine scenarios: the root is built inside the bubble, every synctest test is wrapped in `testWithTimeout` outside the bubble, and results are received on channels, never inferred from a deadlock (a `sync.Mutex` wait is not durably blocking);
- a fake `InjectorOpts.Clock` only for the user-clock context scenarios;
- `Example*` tests;
- fuzz tests;
- CI steps for the dependency footprint.

**Test hygiene:**
- `testWithTimeout` is copied into the config module's `main_test.go`.
- Helpers `clearEnvPrefix(t, "APP_")` and `clearEnvPrefix(t, "DOGER_")` use `t.Setenv` then `os.Unsetenv`, so cleanup restores the variables.
- `t.Chdir` and `t.Setenv` tests are not parallel.
- `slog.SetDefault` and `log.SetOutput`/`SetFlags` are saved and restored with `t.Cleanup`.
- `DefaultRootScope` tests use a per-run unique service name and are not parallel.
- Tests are named after their source files: `config_bind_test.go`, `config_validate_test.go`, `config_source_test.go`, `config_reload_test.go`, and `config/integration_test.go` for root + config end-to-end tests.
- The root reload tests use a root-local test watcher type and a root-local error type, never `config.*`.

**Spike to RED test:**
- spike 1, watcher leak: `TestConfigWatch_Lifecycle`
- spike 2, re-entrant `OnChange`: `TestConfigReload_ReentrantCallbacks`
- spike 3, reload race: `TestConfigReload_ConcurrentReads` and `config/TestFiles_ConcurrentReadsDuringLoad`
- spike 4, env-only key: `TestResolveConfig_EnvOnlyKey` and `config/TestBindConfig_EnvOnlyKeyWithFiles`
- spike 5, `.env` export: `config/TestFiles_DotEnv`

**Hot paths and their tests:**
- **Construction and load:** `TestNewWithOpts_ConstructionOutcome`, `TestConfigSource_LoadFailure`, `TestConfigSource_LoadDeadlineRealClock`, `TestNewWithOpts_OptionsNotWritten`.
- **Property lookup, grammar and env:** `TestProperties_InvalidKeys`, `TestProperties_KeySuffixes`, `TestEnvPropertySource_Mapping`, `TestMapSource_KeysRoundTrip`.
- **Binder:** `TestBind_*`.
- **Typed binding and resolution:** `TestBindConfig_*`, `TestResolveConfig_*`, `TestConfigValue_Get`.
- **Validation:** `TestValidate_*`.
- **Reload commit:** `TestConfigWatch_CommitDecision`, `TestConfigReload_AllOrNothing`, `TestConfigReload_ReadPathsAgree`.
- **Concurrency:** `TestBindConfig_Concurrent`, `TestBindConfig_RacingReload`, `TestConfigReload_NoMixedValues`, `TestConfigReload_ConcurrentReads`, `config/TestFiles_LoadsSerialized`.
- **Lifecycle and shutdown:** `TestConfigWatch_Lifecycle`, `TestConfigReload_ShutdownBoundedByContext`, `TestConfigReload_OwnersGate`, `TestNew_NoHealthGoroutine`.
- **Loader:** `config/TestFiles_FlattenAndNormalize`, `config/TestFiles_LayerPrecedence`, `config/TestFiles_UniformFormPrecedence`.

**Fuzz harness.** Fuzz tests run in-package against the decoders and parsers, not through temp files. Values are planted as high-entropy markers (`zq7Xk2vP9w`) in value positions only. The property is: no panic, no marker in `Error()` or `%#v`, and error text matching `^<file>(:\d+)?: <category>$`. Seed corpora are committed.
- **`FuzzFiles_YAML`:** seeds include billion-laughs and a self-referencing anchor.
- **`FuzzFiles_TOML`:** seeds include a 100-deep dotted table.
- **`FuzzFiles_JSON`**.
- **`FuzzFiles_DotEnv`**.
- **`FuzzBind_Scalars`:** random strings into every supported kind.
- **`FuzzKeyGrammar`** asserts:
  - `uniform(reverse(envName(k))) == uniform(k)` for addressable keys;
  - `envName(reverse(v)) == v` for every v that `reverse` accepts;
  - seeds include `redis/0.addr`.

**Examples** (no `os.Setenv`; `testdata/` dirs and unique prefixes, so `// Output:` is deterministic):
- root: `ExampleNewWithOpts_config`, `ExampleNewWithOptsContext`, `ExampleBindConfig`, `ExampleBindConfigNamed`, `ExampleResolveConfig`, `ExampleConfigValue_OnChange`, `ExampleStaticConfig`, `ExampleBindProperties`, `ExampleValidateConfig`, `ExampleMapSource`, `ExampleEnvPropertySource` (rewritten);
- config: `ExampleFiles`, `ExampleFiles_overrides`, `ExampleLayer`, `ExampleCodec`.

**Scenario map.** Every `#### Scenario:` in `specs/*/spec.md` and its one test. File names without a `config/` prefix are in the root package. Examples rows run as named subtests.

| Capability | Scenario | Test | Kind | Group |
|---|---|---|---|---|
| config-binding | An env-only key binds | `config_bind_test.go: TestResolveConfig_EnvOnlyKey` | unit, table | 8 |
| config-binding | The source receives canonical keys | `config_bind_test.go: TestBind_CanonicalKeys` | unit, table | 8 |
| config-binding | Non-ASCII names | `config_bind_test.go: TestBind_NonASCIIName` | unit, table | 8 |
| config-binding | Multi-word lists, maps and pointers bind from env | `config_bind_test.go: TestBind_UniformKeysFromEnv` | unit, table | 9 |
| config-binding | Equal spellings in uniform form | `config_bind_test.go: TestBind_UniformFolding` | unit, table | 10 |
| config-binding | An instance segment is never folded | `config_bind_test.go: TestBind_InstanceNotFolded` | unit, table | 10 |
| config-binding | A map source round-trips its own spelling | `config_bind_test.go: TestBind_KeysLookedUpAsReturned` | unit, table | 10 |
| config-binding | Several spellings of one key in one source | `config_bind_test.go: TestBind_DuplicateSpellingTieBreak` | unit, table | 10 |
| config-binding | Tags rename or skip fields | `config_bind_test.go: TestBind_TagOverride` | unit, table | 10 |
| config-binding | The tag key can be changed | `config_bind_test.go: TestBind_ConfigTagKey` | unit, table | 10 |
| config-binding | Two containers bind one type with different tag keys | `config_bind_test.go: TestBind_PlanCacheByTagKey` | unit, table | 10 |
| config-binding | Invalid tags | `config_bind_test.go: TestBind_InvalidTag` | unit, table | 10 |
| config-binding | A foreign tag key with options | `config_bind_test.go: TestBind_ForeignTagKeyOptions` | unit, table | 10 |
| config-binding | Conflicting names | `config_bind_test.go: TestBind_NameConflict` | unit, table | 10 |
| config-binding | Unsupported kinds | `config_bind_test.go: TestBind_UnsupportedKinds` | unit, table | 10 |
| config-binding | Present zero values overwrite defaults, absent keys keep them | `config_bind_test.go: TestBind_Presence` | unit, table | 8 |
| config-binding | One nested key keeps its siblings' defaults | `config_bind_test.go: TestBind_NestedSiblingDefaults` | unit, table | 8 |
| config-binding | Embedded structs bind under the parent's key | `config_bind_test.go: TestBind_Embedded` | unit, table | 8 |
| config-binding | Recursive targets | `config_bind_test.go: TestBind_RecursiveType` | unit, table | 10 |
| config-binding | Back-pointers that are not bound | `config_bind_test.go: TestBind_RecursionExemptions` | unit, table | 10 |
| config-binding | Indexed list sources | `config_bind_test.go: TestBind_Lists` | unit, table | 9 |
| config-binding | A list of structs starts each element from zero | `config_bind_test.go: TestBind_ListOfStructs` | unit, table | 9 |
| config-binding | A list of lists | `config_bind_test.go: TestBind_ListOfLists` | unit, table | 9 |
| config-binding | Comma values | `config_bind_test.go: TestBind_CommaSplit` | unit, table | 9 |
| config-binding | Map entries merge over defaults | `config_bind_test.go: TestBind_MapMerge` | unit, table | 9 |
| config-binding | A map of structs merges entries field by field | `config_bind_test.go: TestBind_MapOfStructs` | unit, table | 9 |
| config-binding | A map of lists | `config_bind_test.go: TestBind_MapOfLists` | unit, table | 9 |
| config-binding | Pointer allocation | `config_bind_test.go: TestBind_Pointers` | unit, table | 9 |
| config-binding | Pointers to scalars | `config_bind_test.go: TestBind_PointerToScalar` | unit, table | 9 |
| config-binding | A Lookup-only source | `config_bind_test.go: TestBind_WithoutPropertyKeys` | unit, table | 9 |
| config-binding | BindProperties over a Lookup-only source | `config_bind_test.go: TestBindProperties_WithoutPropertyKeys` | unit, table | 11 |
| config-binding | Conversions | `config_bind_test.go: TestBind_Conversions` | unit, table | 8 |
| config-binding | Conversion errors | `config_bind_test.go: TestBind_ConversionErrors` | unit, table | 8 |
| config-binding | Panics in user code | `config_bind_test.go: TestBind_PanicRecovered` | unit, table | 8 |
| config-binding | Defaults survive binding | `config_bind_test.go: TestBind_DefaultsNotMutated` | unit, table | 8 |
| config-binding | Invalid type parameters | `config_bind_test.go: TestBindConfig_InvalidType` | unit, table | 11 |
| config-binding | Valid and invalid prefixes | `config_bind_test.go: TestResolveConfig_PrefixGrammar` | unit, table | 8 |
| config-binding | A typed-nil injector | `config_bind_test.go: TestResolveConfig_TypedNilInjector` | unit, table | 8 |
| config-binding | A bound section is invocable | `config_bind_test.go: TestBindConfig_Registers` | unit, table | 11 |
| config-binding | A failed bind registers nothing | `config_bind_test.go: TestBindConfig_FailureRegistersNothing` | unit, table | 11 |
| config-binding | A failing second registration is rolled back | `config_bind_test.go: TestBindConfig_RegistrationRollback` | unit, table | 11 |
| config-binding | A nil injector binds into DefaultRootScope | `config_bind_test.go: TestBindConfig_NilInjector` | unit (not parallel) | 11 |
| config-binding | A child scope binds from its container and registers locally | `config_bind_test.go: TestBindConfig_ChildScope` | unit, table | 11 |
| config-binding | Two databases of one type | `config_bind_test.go: TestBindConfigNamed_TwoPrefixes` | unit, table | 11 |
| config-binding | An empty name is rejected | `config_bind_test.go: TestBindConfigNamed_EmptyName` | unit, table | 11 |
| config-binding | Duplicate bindings | `config_bind_test.go: TestBindConfig_Duplicate` | unit, table | 11 |
| config-binding | Unresolved defaults and parent registrations give way | `config_bind_test.go: TestBindConfig_ReplacesDefaultShadowsParent` | unit, table | 11 |
| config-binding | Binding in each phase | `config_bind_test.go: TestBindConfig_Phases` | unit, table | 11 |
| config-binding | Resolving in every phase | `config_bind_test.go: TestResolveConfig_AllPhases` | unit, table | 11 |
| config-binding | A PropertySource service registered after a bind | `config_bind_test.go: TestBind_DecidesPropertySource` | unit, table | 11 |
| config-binding | Two consumers resolve one prefix with different defaults | `config_bind_test.go: TestResolveConfig_ConsumerDefaults` | unit, table | 11 |
| config-binding | A failed resolve returns the zero value | `config_bind_test.go: TestResolveConfig_Failure` | unit, table | 11 |
| config-binding | Resolving inside a provider uses the provider's container | `config_bind_test.go: TestResolveConfig_InProviderAndClone` | unit, table | 11 |
| config-binding | Handle values | `config_bind_test.go: TestConfigValue_Get` | unit, table | 11 |
| config-binding | OnChange on a handle that never changes | `config_bind_test.go: TestConfigValue_OnChangeStatic` | unit, table | 11 |
| config-binding | An extension binds over its defaults | `config_bind_test.go: TestBindProperties_Extension` | unit, table | 11 |
| config-binding | A failed bind leaves the target unchanged | `config_bind_test.go: TestBindProperties_TargetUnchangedOnError` | unit, table | 11 |
| config-binding | Invalid targets | `config_bind_test.go: TestBindProperties_InvalidTarget` | unit, table | 11 |
| config-binding | The zero view binds nothing | `config_bind_test.go: TestBindProperties_ZeroView` | unit, table | 11 |
| config-binding | Binding from a map literal | `config_bind_test.go: TestMapSource_Bind` | unit, table | 10 |
| config-binding | Concurrent use under -race | `config_bind_test.go: TestBindConfig_Concurrent` | race | 11 |
| config-files | Base files found in the working directory and ./config | `config/files_test.go: TestFiles_Discovery` | unit (t.Chdir, not parallel) | 14 |
| config-files | Both directories merge key by key | `config/files_test.go: TestFiles_DiscoveryMergesDirs` | unit (t.Chdir) | 14 |
| config-files | Four files and a split list | `config/files_test.go: TestFiles_DiscoveryOrder` | unit (t.Chdir) | 14 |
| config-files | Two formats for one base name | `config/files_test.go: TestFiles_AmbiguousFormat` | unit, table | 14 |
| config-files | Profile selection | `config/files_test.go: TestFiles_Profile` | unit (t.Setenv) | 14 |
| config-files | Profile files in one directory or without a base | `config/files_test.go: TestFiles_ProfilePlacement` | unit, table | 14 |
| config-files | The profile variable is never a config key and never read from .env | `config/files_test.go: TestFiles_ProfileVariable` | unit (t.Setenv) | 16 |
| config-files | Profile names | `config/files_test.go: TestFiles_ProfileNames` | unit, table | 14 |
| config-files | Symlinked config files | `config/files_test.go: TestFiles_Symlinks` | unit (skip on Windows) | 14 |
| config-files | Directories, files and base name | `config/files_test.go: TestFiles_ExplicitPaths` | unit, table | 14 |
| config-files | File, Dir, Format and the profile together | `config/files_test.go: TestFiles_ExplicitFileProfileFormat` | unit, table | 14 |
| config-files | A later working-directory change is not followed | `config/files_test.go: TestFiles_PathsResolvedAtFirstLoad` | unit (t.Chdir) | 14 |
| config-files | Built-in and custom decoders | `config/codec_test.go: TestFiles_Codecs` | unit, table | 13 |
| config-files | Rendering a decoder's values | `config/codec_test.go: TestFiles_DecoderRendering` | unit, table | 13 |
| config-files | Flattening and normalization | `config/keys_test.go: TestFiles_FlattenAndNormalize` | unit, table | 13 |
| config-files | Keys that cannot be normalized | `config/keys_test.go: TestFiles_InvalidKeys` | unit, table | 13 |
| config-files | Shapes that fail | `config/keys_test.go: TestFiles_InvalidShapes` | unit, table | 13 |
| config-files | Empty content | `config/keys_test.go: TestFiles_EmptyDocuments` | unit, table | 13 |
| config-files | Depth and key caps | `config/keys_test.go: TestFiles_DepthAndKeyCaps` | unit, table | 13 |
| config-files | Rendering by format | `config/codec_test.go: TestFiles_ScalarRendering` | unit, table | 13 |
| config-files | Aliases and merges | `config/codec_test.go: TestFiles_YAMLAliasesAndMerge` | unit, table | 13 |
| config-files | Highest layer wins | `config/layers_test.go: TestFiles_LayerPrecedence` | unit (t.Setenv) | 16 |
| config-files | Spelling never decides precedence | `config/layers_test.go: TestFiles_UniformFormPrecedence` | unit (t.Setenv) | 15 |
| config-files | Lists across layers | `config/layers_test.go: TestFiles_ListFromOneLayer` | unit (t.Setenv) | 15 |
| config-files | Prefix forms | `config/env_test.go: TestFiles_EnvPrefix` | unit (t.Setenv) | 15 |
| config-files | Key to variable | `config/env_test.go: TestFiles_EnvMapping` | unit (golden file) | 15 |
| config-files | Variables mapped back to keys | `config/env_test.go: TestFiles_EnvReverseMapping` | unit (t.Setenv) | 15 |
| config-files | Mapper with and without a prefix | `config/env_test.go: TestFiles_EnvMapper` | unit (t.Setenv) | 15 |
| config-files | .env handling | `config/dotenv_test.go: TestFiles_DotEnv` | unit (spike 5) | 16 |
| config-files | Line forms | `config/dotenv_test.go: TestFiles_DotEnvLines` | unit, table | 16 |
| config-files | A later line wins | `config/dotenv_test.go: TestFiles_DotEnvLaterLineWins` | unit, table | 16 |
| config-files | Value forms | `config/dotenv_test.go: TestFiles_DotEnvValues` | unit, table | 16 |
| config-files | Export at the first Load | `config/dotenv_test.go: TestFiles_DotEnvExport` | unit (t.Setenv) | 16 |
| config-files | A second Load exports nothing | `config/dotenv_test.go: TestFiles_DotEnvExportOnce` | unit (t.Setenv) | 16 |
| config-files | A vault-style layer | `config/layers_test.go: TestFiles_AddedLayer` | unit, table | 17 |
| config-files | A failing added layer | `config/layers_test.go: TestFiles_AddedLayerLoadError` | unit, table | 17 |
| config-files | A layer key that cannot be normalized | `config/layers_test.go: TestFiles_AddedLayerInvalidKey` | unit, table | 17 |
| config-files | Keys across layers | `config/keys_test.go: TestFiles_Keys` | unit (t.Setenv) | 15 |
| config-files | Ordering, duplicates and before Load | `config/keys_test.go: TestFiles_KeysOrderingAndDuplicates` | unit, table | 15 |
| config-files | A syntax error near a secret | `config/errors_test.go: TestFiles_LoadErrorHasNoValue` | unit, table | 13 |
| config-files | Error texts and matching | `config/errors_test.go: TestFiles_ErrorsValueFree` | unit, table | 17 |
| config-files | Size limit | `config/files_test.go: TestFiles_MaxFileSize` | unit, table | 14 |
| config-files | Non-regular files | `config/files_test.go: TestFiles_NotRegularFile` | unit (FIFO, skip on Windows) | 14 |
| config-files | Invalid options | `config/options_test.go: TestFiles_InvalidOptions` | unit, table | 17 |
| config-files | A cancelled load keeps the previous snapshot | `config/files_test.go: TestFiles_LoadContextCancelled` | unit, table | 17 |
| config-files | An already-cancelled context | `config/files_test.go: TestFiles_LoadAlreadyCancelled` | unit, table | 17 |
| config-files | Two overlapping Loads | `config/files_test.go: TestFiles_LoadsSerialized` | race | 17 |
| config-files | Load while watching | `config/watch_test.go: TestFiles_LoadWhileWatching` | synctest | 23 |
| config-files | Concurrent reads while loading | `config/files_test.go: TestFiles_ConcurrentReadsDuringLoad` | race (spike 3) | 17 |
| config-files | Log sink | `config/options_test.go: TestFiles_LogSink` | unit (slog/log restore, not parallel) | 16 |
| config-files | An env-only key binds although the file lacks it | `config/integration_test.go: TestBindConfig_EnvOnlyKeyWithFiles` | unit (spike 4) | 18 |
| config-files | Every file spelling binds the same field | `config/integration_test.go: TestResolveConfig_FileSpellings` | unit, table | 18 |
| config-files | Compile-time contract checks | `config/source_test.go: TestSource_NotAValidator` | build + unit | 23 |
| config-files | Dependency footprint | `CI: footprint step (go list -deps)` | CI step | 1 |
| config-reload | The source adopts only what the root accepts | `config_reload_test.go: TestConfigWatch_CommitDecision` | unit, table | 20 |
| config-reload | Read paths agree after a commit and after a rejection | `config_reload_test.go: TestConfigReload_ReadPathsAgree` | unit, table | 20 |
| config-reload | The staged view is invisible until commit | `config_reload_test.go: TestConfigWatch_StagedInvisibleUntilCommit` | unit, table | 20 |
| config-reload | One BindProperties call reads one view | `config_reload_test.go: TestConfigReload_BindPropertiesOneView` | race | 20 |
| config-reload | One invalid section blocks every section | `config_reload_test.go: TestConfigReload_AllOrNothing` | unit, table | 20 |
| config-reload | Readers never see a mixed value | `config_reload_test.go: TestConfigReload_NoMixedValues` | race | 20 |
| config-reload | Live handle versus snapshot after a reload | `config_reload_test.go: TestConfigReload_LiveVersusSnapshot` | unit, table | 20 |
| config-reload | Failures warn and call the hooks | `config_reload_test.go: TestConfigReload_FailedWarnsAndHooks` | unit, table | 21 |
| config-reload | Hooks receive the start context's values, never a cancelled context | `config_reload_test.go: TestConfigReload_HookContext` | unit, table | 21 |
| config-reload | Who is notified | `config_reload_test.go: TestConfigValueOnChange_Notified` | unit, table | 21 |
| config-reload | Every handle is published before the first notification | `config_reload_test.go: TestConfigReload_PublishBeforeNotify` | unit, table | 21 |
| config-reload | Static and zero handles never notify | `config_reload_test.go: TestConfigValue_StaticAndZeroNeverNotify` | unit, table | 21 |
| config-reload | Stop semantics | `config_reload_test.go: TestConfigValueOnChange_Stop` | race | 21 |
| config-reload | A running call finishes after stop | `config_reload_test.go: TestConfigValueOnChange_StopDuringCall` | synctest | 21 |
| config-reload | A nil callback is ignored | `config_reload_test.go: TestConfigValueOnChange_NilFunc` | unit, table | 21 |
| config-reload | A slow subscriber receives the latest value | `config_reload_test.go: TestConfigReload_DispatcherCoalesces` | synctest | 21 |
| config-reload | A panicking callback | `config_reload_test.go: TestConfigReload_CallbackPanic` | unit, table | 21 |
| config-reload | Re-entrant calls from a callback | `config_reload_test.go: TestConfigReload_ReentrantCallbacks` | synctest (spike 2) | 21 |
| config-reload | A blocked callback does not hang Shutdown | `config_reload_test.go: TestConfigReload_ShutdownBoundedByContext` | synctest, goleak | 21 |
| config-reload | Changes after shutdown began are dropped | `config_reload_test.go: TestConfigReload_ChangedAfterShutdown` | unit, table | 21 |
| config-reload | Concurrent reads under reload | `config_reload_test.go: TestConfigReload_ConcurrentReads` | race (spike 3) | 20 |
| config-reload | Watcher lifecycle | `config_reload_test.go: TestConfigWatch_Lifecycle` | synctest, goleak (spike 1) | 19 |
| config-reload | How a watch ends | `config_reload_test.go: TestConfigWatch_Ends` | unit, table | 19 |
| config-reload | A watch panic goes to HookContainerPanic when set | `config_reload_test.go: TestConfigWatch_PanicHook` | unit, table | 19 |
| config-reload | One reload reaches parent and clone | `config_reload_test.go: TestConfigReload_CloneShares` | unit, table | 22 |
| config-reload | Which sections gate a reload | `config_reload_test.go: TestConfigReload_OwnersGate` | unit, table | 22 |
| config-reload | A clone keeps receiving a section after its parent shut down | `config_reload_test.go: TestConfigReload_CloneOutlivesParent` | unit, table | 22 |
| config-reload | Whose hooks a failure fires | `config_reload_test.go: TestConfigReload_HookOwnership` | unit, table | 22 |
| config-reload | A bind racing a reload ends with the handle holding the newer view | `config_reload_test.go: TestBindConfig_RacingReload` | race | 20 |
| config-reload | Poll interval and debounce | `config/watch_test.go: TestFiles_PollInterval` | synctest | 23 |
| config-reload | A negative poll interval fails construction | `config/watch_test.go: TestFiles_PollIntervalNegative` | unit, table | 23 |
| config-reload | One source is watched once at a time | `config/watch_test.go: TestFiles_WatchOnce` | synctest | 23 |
| config-reload | What a poll does | `config/watch_test.go: TestFiles_PollReloads` | synctest (t.Chdir) | 23 |
| config-reload | A Kubernetes ConfigMap swap is a change | `config/watch_test.go: TestFiles_PollSymlinkSwap` | synctest (skip on Windows) | 23 |
| config-reload | Repeated content is staged once | `config/watch_test.go: TestFiles_PollStagesOnce` | synctest | 23 |
| config-reload | A failing added layer is retried | `config/watch_test.go: TestFiles_PollRetriesLayerError` | synctest | 23 |
| config-reload | What a poll refuses or times out on | `config/watch_test.go: TestFiles_PollBounded` | synctest | 23 |
| config-reload | Hostile files | `config/watch_test.go: TestFiles_HostileYAML` | unit + fuzz seeds | 23 |
| config-sources | Which types are sources | `config_source_test.go: TestConfigSource_Contract` | unit, table | 5 |
| config-sources | Property reads go to the installed source | `config_source_test.go: TestConfigSource_IsPropertySource` | unit, table | 5 |
| config-sources | Invalid option combinations | `config_source_test.go: TestConfigSource_OptionChecks` | unit, table | 6 |
| config-sources | SetLogf and Load order | `config_source_test.go: TestConfigSource_SetLogfOrder` | unit, table | 5 |
| config-sources | The logger reaches the source | `config_source_test.go: TestConfigSource_LogfReachesSource` | unit, table | 5 |
| config-sources | The Load deadline on the real clock | `config_source_test.go: TestConfigSource_LoadDeadlineRealClock` | synctest | 7 |
| config-sources | A Load that honors its deadline | `config_source_test.go: TestConfigSource_LoadTimeout` | synctest | 7 |
| config-sources | A Load that ignores its deadline | `config_source_test.go: TestConfigSource_LoadIgnoresDeadline` | synctest | 7 |
| config-sources | Deadline and Err on a fake clock | `config_source_test.go: TestConfigSource_FakeClockContext` | goleak | 7 |
| config-sources | Which side ends the Load context | `config_source_test.go: TestConfigSource_FakeClockEnds` | unit, table | 7 |
| config-sources | A source that keeps its Load context | `config_source_test.go: TestConfigSource_LoadContextEnds` | goleak | 7 |
| config-sources | Load errors and panics | `config_source_test.go: TestConfigSource_LoadFailure` | unit, table | 5 |
| config-sources | Which text a load error shows | `config_source_test.go: TestConfigSource_ValueFreeText` | unit, table | 5 |
| config-sources | Sharing on clone | `config_source_test.go: TestConfigSource_CloneShares` | unit, table | 5 |
| config-sources | A non-pointer source is loaded again | `config_source_test.go: TestConfigSource_CloneNonPointer` | unit, table | 5 |
| config-sources | Secrets stay out of construction errors and logs | `config_source_test.go: TestConfigSource_NoValuesInErrorsOrLogs` | unit, table | 5 |
| config-validation | Default tag validation | `config_validate_test.go: TestValidate_DefaultTags` | unit, table | 12 |
| config-validation | Range boundaries | `config_validate_test.go: TestValidate_RangeBoundaries` | unit, table | 12 |
| config-validation | A failed validation registers nothing | `config_validate_test.go: TestBindConfig_ValidationRegistersNothing` | unit, table | 12 |
| config-validation | Skipped fields with failing tags | `config_validate_test.go: TestValidate_SkippedFields` | unit, table | 12 |
| config-validation | Validate method results | `config_validate_test.go: TestValidate_Method` | unit, table | 12 |
| config-validation | Validate methods of fields and embedded types | `config_validate_test.go: TestValidate_TopLevelAndPromoted` | unit, table | 12 |
| config-validation | Messages for Validate errors | `config_validate_test.go: TestValidate_ValueFreeText` | unit, table | 12 |
| config-validation | Validator choice | `config_validate_test.go: TestValidate_Replacement` | unit, table | 12 |
| config-validation | Clones keep or replace the validator | `config_validate_test.go: TestValidate_CloneValidator` | unit, table | 12 |
| config-validation | Defaults are validated too | `config_validate_test.go: TestValidate_MergedValue` | unit, table | 12 |
| config-validation | Error shape and keys | `config_validate_test.go: TestValidate_ErrorShape` | unit, table | 12 |
| config-validation | Secret values stay out of errors and logs | `config_validate_test.go: TestValidate_NoValuesLeaked` | unit, table | 12 |
| config-validation | A malformed tag | `config_validate_test.go: TestValidate_MalformedTag` | unit, table | 12 |
| config-validation | An extension validates after applying its options | `config_validate_test.go: TestValidateConfig_Extension` | unit, table | 12 |
| config-validation | Messages without a section | `config_validate_test.go: TestValidateConfig_MessageWithoutSection` | unit, table | 12 |
| config-validation | Invalid values to validate | `config_validate_test.go: TestValidateConfig_InvalidTarget` | unit, table | 12 |
| config-validation | Validator panics | `config_validate_test.go: TestValidate_PanicRecovered` | unit, table | 12 |
| config-validation | Concurrent binds validate under -race | `config_validate_test.go: TestValidate_Concurrent` | race | 12 |
| container-construction | Construction outcome | `root_scope_test.go: TestNewWithOpts_ConstructionOutcome` | unit, table | 5 |
| container-construction | Options unchanged after construction | `root_scope_test.go: TestNewWithOpts_OptionsNotWritten` | unit, table | 5 |
| container-construction | Reusing one options value | `root_scope_test.go: TestNewWithOpts_ReuseOptions` | unit, table | 5 |
| container-construction | New is unchanged | `root_scope_test.go: TestNew_Unchanged` | unit (passes on arrival) | 5 |
| container-construction | Context values and nil | `root_scope_test.go: TestNewWithOptsContext_Values` | unit, table | 7 |
| container-construction | Cancelling construction | `root_scope_test.go: TestNewWithOptsContext_Cancel` | unit, table | 7 |
| container-construction | An already cancelled context | `root_scope_test.go: TestNewWithOptsContext_AlreadyCancelled` | unit, table | 7 |
| container-construction | Load happens before the packages | `root_scope_test.go: TestNewWithOpts_LoadBeforePackages` | unit, table | 5 |
| container-construction | A failed construction runs no package | `root_scope_test.go: TestNewWithOpts_FailureRunsNoPackage` | unit, table | 5 |
| container-construction | Cloning with options | `root_scope_test.go: TestCloneWithOpts_Outcome` | unit, table | 7 |
| container-construction | Clone of a root with config | `root_scope_test.go: TestClone_SharesConfig` | unit, table | 5 |
| container-construction | Must constructors | `root_scope_test.go: TestMustConstructors` | unit, table | 5 |
| container-construction | Registering on the default root | `root_scope_test.go: TestDefaultRootScope_Usable` | unit (not parallel, unique names) | 5 |
| container-construction | Duplicate registration in a package | `root_scope_test.go: TestNew_PackagePanicPropagates` | unit, table | 7 |
| extension-properties | Invalid keys are not looked up | `properties_test.go: TestProperties_InvalidKeys` | unit (existing, extended) | 2 |
| extension-properties | Source selection | `properties_test.go: TestPropertySource_Selection` | unit (existing, rewritten) | 5 |
| extension-properties | A source registered after the first read is not used | `properties_test.go: TestPropertySource_LateServiceIgnored` | unit (existing, variable renamed) | 3 |
| extension-properties | Registering the interface name after the first read panics | `properties_test.go: TestPropertySource_LateSameNamePanics` | unit (existing, passes on arrival) | 5 |
| extension-properties | An installed config source is decided before any package runs | `properties_test.go: TestPropertySource_ConfigSourceAtConstruction` | unit, table | 6 |
| extension-properties | Index and map-key suffixes | `properties_test.go: TestProperties_KeySuffixes` | unit, table | 2 |
| extension-properties | Key to variable mapping | `properties_test.go: TestEnvPropertySource_Mapping` | unit (existing, rewritten; golden file) | 3 |
| extension-properties | Nested, instance and index keys use different variables | `properties_test.go: TestEnvPropertySource_Distinct` | unit (existing, rewritten) | 3 |
| extension-properties | Keys that share a variable | `properties_test.go: TestEnvPropertySource_SharedVariables` | unit, table | 3 |
| extension-properties | Map keys the environment cannot address | `properties_test.go: TestEnvPropertySource_UnaddressableMapKeys` | unit, table | 3 |
| extension-properties | An unset variable is a miss | `properties_test.go: TestEnvPropertySource_Unset` | unit (existing, passes on arrival) | 3 |
| extension-properties | Variables in the old mapping are no longer read | `properties_test.go: TestEnvPropertySource_OldMappingIgnored` | unit, table | 3 |
| extension-properties | Prefix forms | `properties_test.go: TestEnvPropertySource_PrefixForms` | unit, table | 3 |
| extension-properties | An invalid prefix never panics | `properties_test.go: TestEnvPropertySource_InvalidPrefix` | unit, table | 6 |
| extension-properties | Map source keys | `properties_test.go: TestMapSource_Keys` | unit, table | 4 |
| extension-properties | Every key Keys returns is found by Lookup | `properties_test.go: TestMapSource_KeysRoundTrip` | unit, table | 4 |
| extension-properties | Every key and the nil map | `properties_test.go: TestMapSource_AllKeys` | unit, table | 4 |
| extension-properties | A map source reads the map it is | `properties_test.go: TestMapSource_NotCopied` | unit, table | 4 |
| extension-properties | Environment source keys | `properties_test.go: TestEnvPropertySource_Keys` | unit (t.Setenv, prefix TKEYS_) | 4 |
| extension-properties | Keys through an extension view | `properties_test.go: TestProperties_Keys` | unit, table | 4 |
| extension-properties | Views that cannot list keys | `properties_test.go: TestProperties_KeysUnavailable` | unit, table | 4 |
| health-check-execution | New starts no goroutine | `health_run_test.go: TestNew_NoHealthGoroutine` | goleak (existing, extended) | 7 |
| module-identity | Workspace module paths mirror their directories | `module_identity_test.go: TestModuleIdentity_WorkspaceModules` | unit (existing, row added) | 1 |
| toolchain-support | Module go directives declare 1.27 | `toolchain_test.go: TestToolchain_GoDirective` | unit (existing, row added) | 1 |
