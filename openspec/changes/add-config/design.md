# Design

## Context

See proposal.md for the motivation. The decision record is the Claude Doc "doger/config: Config as a First-Class Component":
- D1–D32 were decided in discussion.
- D33–D41 came from six adversarial audits of the first draft of this change, four on correctness and two on idiomatic Go.

This design follows the record. Where an earlier draft disagrees, the specs in `specs/*/spec.md` win.

The code today:
- `New(packages...) *RootScope` and `NewWithOpts(opts, packages...) *RootScope` cannot fail (`root_scope.go:43`, `:67`). `CloneWithOpts` copies the container with replaced options (`root_scope.go:738`).
- Extensions read configuration through `PropertySource` (`properties.go:34`, `Lookup` only). The source is decided at the first property read, in this order: `InjectorOpts.PropertySource`, then a service named `NameOf[PropertySource]()`, then `EnvPropertySource("DOGER_")` with its one-to-one mapping.
- `extension.go:86` sets the house rule: extension points grow as optional interfaces, never as new methods.
- The health scheduler is internal wiring of `RootScope`, not a registered service; it stops in `stopScheduling` without waiting for in-flight work (`health_monitor.go:107`).
- The root module requires clockwork, go-type-to-string, testify and goleak (plus `go.yaml.in/yaml/v3` indirect).
- `make test` covers the root module only. CI (`test.yml`, `lint.yml`) runs from the root. `release.yml` pins and tags `http/*/` and `scheduler/*/`. govulncheck is commented out in `security.yml`.

## Goals / Non-Goals

**Goals:**
- **P1:** config loads, binds and validates once at startup, with zero configuration.
- **P2:** config follows file changes without a restart, and a reload is all or nothing.
- Every opinionated default has an override that needs no fork (project rule 6).
- No viper, fsnotify, godotenv or mapstructure.
- No property value ever appears in a log line or an error.
- No global mutable state.
- No goroutine outlives `Shutdown` (goleak-clean).

**Non-Goals:**
- The option-based constructor `doger.New(opts ...Option)` and packages that can fail (`unify-constructor`).
- Rebuilding services when config changes.
- Remote sources beyond the `config.Layer` hook.
- Noticing config files that appear after `Load`.
- A golang-lib/config compatibility shim.

## Architecture at a glance

Two modules. Arrows point from the importer to what it imports.

```
+--------------------------------------------------------------+
| application                                                  |
|   root, err := doger.NewWithOpts(&doger.InjectorOpts{        |
|       ConfigSource: config.Files()}, app.Package)            |
|   err = doger.BindConfig[ServerConfig](root, "server")       |
+-----------+------------------------------------+-------------+
            | imports                            | imports
            v                                    v
+------------------------------+   +----------------------------+
| root module: doger           |   | config module: doger/config|
|                              |   |                            |
| contract                     |   | config.Files(opts...)      |
|   PropertySource (unchanged) |<..|   -> *config.Source        |
|   PropertyKeys   (optional)  |sat|                            |
|   ConfigSource               |is-| layers: base, profile,     |
|   ConfigLogger   (optional)  |fies .env, env, Layer(name,src)|
|   ConfigWatcher  (optional)  |(no|                            |
|   ConfigView     (alias)     |im-| decoders: YAML, TOML, JSON |
|                              |port| + Codec(ext, d)           |
| wiring (internal to          |   |                            |
| RootScope)                   |   | polling watcher (debounced)|
|   NewWithOpts[Context]       |   |                            |
|   committed-view pointer     |   | requires (non-test code):  |
|   reload commit, dispatcher  |   |   go.yaml.in/yaml/v3       |
|   watcher membership         |   |   pelletier/go-toml/v2     |
|                              |   | tests also import doger    |
| binder + typed binding       |   +----------------------------+
|   BindConfig, ResolveConfig  |
|   ConfigValue[T], MapSource  |
|   BindProperties             |
|                              |
| validation                   |
|   go-playground (lazy, D7)   |
|   or InjectorOpts.Config-    |
|   Validator; Validate()      |
|                              |
| requires: + validator/v10    |
+------------------------------+

  ......> structural satisfaction only: config's non-test code never
          imports doger, and doger never imports config (not even in tests).
```

## Decisions

### 1. Only the constructors that take options can fail (D5, D37, D38)

```
NewWithOptsContext(ctx, opts, packages...)      NewWithOpts(opts, p...) ==
  |                                             NewWithOptsContext(context.Background(), opts, p...)
  |-- ctx == nil? -> context.Background()
  |
  |-- 1. check options ------------------------------------------+
  |      typed-nil ConfigSource?        -> ErrInvalidConfigSource |  (checked first;
  |      ConfigSource and PropertySource -> ErrConfigConflict     |   no method called)
  |                                                              |
  |-- 2. property source := ConfigSource                         |
  |-- 3. SetLogf(opts.Logf), only if the user set Logf           |
  |-- 4. loadCtx := ctx                                          |
  |      ctx has no deadline? -> deadline = Clock.Now()+timeout  |
  |      (timeout: ConfigTimeout, 0 -> 10s, <0 -> none)          |
  |      Load(loadCtx) on the caller's goroutine ----------------+ error, panic,
  |      (the constructor waits: cancellation is cooperative)    | or ctx done
  |                                                              v
  |                                      return (nil, err)    err matches ErrConfigLoad
  |                                      no package ran, no   and the cause; text names
  |                                      global state held    types only
  |-- 5. run packages in order (a package panic propagates unchanged)
  |-- 6. source implements ConfigWatcher? -> prepare watcher wiring (starts at Start)
  v
return (root, nil)
```

- `New` and `Clone()` keep their signatures. Neither installs a source, so neither can fail. The ~800 bare `New()` calls are untouched.
- `MustNewWithOpts` calls `panic(err)`. The error already starts with `DI: config:`, so `recover` plus `errors.Is` works and the text isn't double-prefixed.
- `DefaultRootScope` keeps an internal constructor that cannot fail.
- Alternatives:
  - **All constructors return an error (D5=E as first decided).** Rejected in D37: `New` cannot fail, and the rewrite of ~812 call sites buys nothing.
  - **Record errors on the root for `Start`.** Rejected in D5: errors would surface late.
  - **No context-taking constructor.** Rejected in D38: a signal could not cancel a slow network `Load`.

### 2. A Load context that is safe on a fake clock

`clockwork.WithTimeout` on a fake clock starts a goroutine, and its `Err()` blocks until `Done`. That would break goleak and the main spec's "creating a container starts no goroutine". The constructor therefore builds its own small context:
- deadline: `Clock.Now() + timeout`;
- `Done`: closed by `Clock.AfterFunc`, plus the parent's `Done`;
- `Err()`: never blocks;
- values: the parent's;
- `cancel`: deferred, so no timer survives the call.

The helper sits next to `healthCheckContextWithin` (`di_lifecycle.go:319`) but has its own ≤0 semantics: negative means no deadline.

- Alternative: `clockwork.WithTimeout`. Rejected for the goroutine and the blocking `Err`.

### 3. The property source is decided at construction when a ConfigSource is set (D36)

```
today: decided at the first property read     with ConfigSource: decided at construction
-----------------------------------------     ------------------------------------------
InjectorOpts.PropertySource set? -yes-> use   InjectorOpts.ConfigSource set (and valid)?
        | no                                          | yes
        v                                             v
service NameOf[PropertySource]()? -yes-> use  committed view := the source
        | no                                  property reads go through the root's
        v                                     committed-view pointer (decision 8)
EnvPropertySource("DOGER_") as default        both fields set -> ErrConfigConflict
```

- Both fields stay, because each name says what it does.
- Alternative: one `PropertySource` field that is loaded when it has `Load` (D36 B). It is more idiomatic, but it hides loading behind a type assertion, and the user chose the explicit field.

### 4. PropertyKeys is optional (D35, superseding D30)

- `PropertySource` keeps one method.
- `PropertyKeys{Keys(prefix) []string}` is detected by type assertion, like `io.WriterTo`.
- A source without it binds scalars. A struct with a slice, map or pointer field fails with `ErrInvalidConfigTarget`, naming the field's key.
- `ConfigSource` embeds both interfaces, so config sources always list their keys.
- Implementations: `EnvPropertySource`, `MapSource` (a defined `map[string]string` type, not copied) and `Properties` (keys relative to the view).
- Alternative: a required `Keys` method (D30). Rejected because it breaks every custom source and the house rule.

### 5. Uniform form, canonical keys and the env mapping (D39)

```
Go field          canonical (sent to Lookup)   uniform form (compare)  env (prefix APP_)
---------------   --------------------------   ----------------------  -----------------------
ReadTimeout       server.read-timeout          server.readtimeout      APP_SERVER_READTIMEOUT
IssuerURI         server.issuer-uri            server.issueruri        APP_SERVER_ISSUERURI
OAuth2Token       server.oauth2-token          server.oauth2token      APP_SERVER_OAUTH2TOKEN
AllowedHosts []s  server.allowed-hosts[0]      server.allowedhosts[0]  APP_SERVER_ALLOWEDHOSTS_0
Labels map        server.labels[team]          server.labels[team]     APP_SERVER_LABELS_TEAM
Labels map        server.labels[X-Req]         server.labels[X-Req]    (not addressable from env)
redis instance    redis/queue.addr             redis/queue.addr        APP_REDIS__QUEUE_ADDR
```

- **Uniform form:** lowercase, with `-` and `_` removed per segment; the contents of `[...]` are untouched.
- `Keys(prefix)` matching, layer merging and precedence all compare in uniform form. Results are spelled with the caller's prefix.
- So `Keys("server.allowed-hosts")` finds `APP_SERVER_ALLOWEDHOSTS_0`, and an env value beats a file's `read-timeout`.

**Forward and reverse env mapping.** Both modules use the same mapping. Prefix normalization is the same in both: uppercase, append `_` when missing, `""` means no prefix.

```
key -> variable                          variable -> key (Keys)
---------------                          ----------------------
uppercase                                strip prefix
"."   -> "_"                             "__"           -> instance "/"
"-"   -> removed                         all-digit seg  -> "[i]"
"/"   -> "__"                            other segments -> lowercase, dash-free
"[i]" -> "_i"                            skip what doesn't parse (APP__X, lowercase
"[k]" -> "_K"   (k in [a-z0-9]+ only)    names, "___", empty segments)
```

- Distinct keys may share a variable (`pool-size` and `poolsize`, extensions `my-redis` and `myredis`). Spring accepts this too, and so do we.
- **One golden file.** `testdata/envmapping.txt` in the root holds the mapping table. The root's tests read it by relative path, and the config module's tests read it as `../testdata/envmapping.txt`. A `_test.go` fixture can't cross modules, and an `internal/` package would break the config module's footprint rule.
- **Invalid prefixes.** `EnvPropertySource` panics with `DI: config: invalid env prefix "<p>"`, like `regexp.MustCompile`. `config.EnvPrefix` fails `Load` with `ErrInvalidOption`.
- Alternative: literal prefix matching. Rejected: multi-word list, map and pointer fields would never bind from env (an audit blocker).

### 6. The binder walks the target

```
ResolveConfig / BindConfig[T](i, prefix, ConfigDefaults(d))
  |
  |-- T a struct? prefix valid? i not typed-nil? ---- no --> ErrInvalidConfigTarget
  |-- plan(T) once per reflect.Type (sync.Map cache): canonical names, tag key
  |   (InjectorOpts.ConfigTagKey, default "cfg"), recursion check -> ErrInvalidConfigTarget,
  |   uniform-name conflicts -> ErrConfigNameConflict
  |
  |-- w := deepCopy(d)   (visited-pointer map; caller's slices/maps never change)
  |
  |   for each planned field f (recursing into nested and embedded structs):
  |     key := prefix + "." + canonical(f)
  |     conversion order:
  |       1. exact types: time.Duration, url.URL, *T implements TextUnmarshaler
  |          (net.IP, time.Time, netip.Addr, slog.Level) -> scalar via Lookup
  |       2. kinds: string, bool, ints/uints/floats via strconv (base 10, Type.Bits())
  |       3. struct  -> recurse
  |          slice   -> Keys(key): indices, count must be max+1, max <= 10000,
  |                     checked before any allocation; none -> comma-split Lookup(key)
  |          map     -> string keys only; merge by key; struct values field by field
  |          pointer -> Keys(key) non-empty? allocate and recurse : leave nil
  |     a failure  -> *ConfigBindError{Key, Type}  (never the value, never the parser text)
  |     a panic in UnmarshalText or a source -> recovered, ErrInvalidConfigValue (type only)
  v
validate(w)   (decision 7)  -> on success: return w / register (decision 9)
```

- **Lists:** a higher layer's scalar at the list key masks lower layers' indices. A value that legitimately contains commas uses a named type with `UnmarshalText`; that is the documented override for comma-splitting.
- Alternatives:
  - **mapstructure.** Map-driven, so it can't bind env-only keys (spike 4).
  - **Kind-first conversion.** It would treat `net.IP` as a list of bytes.

### 7. Validation lives in the root, with one override (D7, D34)

```
validator := InjectorOpts.ConfigValidator != nil ? it : lazy go-playground (sync.OnceValue)
                         |
                  validator(&w) --fails--> *ConfigValidationError{Fields: [{Key, Rule}...]}
                         | ok                (keys from the binder's field-path map, sorted;
                         v                    never go-playground FieldErrors; Is ErrInvalidConfig)
          w (top-level only) has Validate() error?
                 | yes                     | no
                 v                         v
          err? --> wrapped: matches        done
          ErrInvalidConfig and the cause;
          text as written (author's duty)
validator panic -> recovered as ErrInvalidConfig naming the panic value's type
```

- Error text: `DI: config: invalid properties: server.port (max), db.primary.url (required)`.
- Map entries are reported as `key[entry]`. `ValidateConfig` reports keys relative to the value it was given.
- go-playground is built lazily, so importing doger costs nothing until a container validates.
- Alternatives:
  - **A source-level validator (`config.Validator`).** Impossible: a fixed method set can't change with an option (D34).
  - **Leave tags unchecked without a validator.** Rejected in D7.

### 8. config.Files: layers, discovery and an immutable snapshot (D12, D41)

```
lowest                                        server.port
  struct defaults (binder, not a layer)        8000
  base     ./config.yaml < ./config/config.yaml        8080
  profile  ./config.prod.yaml < ./config/config.prod.yaml  9090   APP_PROFILE=prod
  .env     ./.env (cwd), never os.Setenv by default    9191
  env      APP_* (read once at Load)                   9292
highest added layers, Layer(name, src), in order       (vault)
LayerOrder(LayerBase, LayerProfile, LayerEnv, LayerDotEnv, "vault") reorders; omitted = not read
```

```
discovery (zero config)
  "." then "./config"; per dir: config.{yaml,yml,toml,json}
    two formats in one dir  -> ErrAmbiguousFormat
    none anywhere           -> fine (defaults and env still bind)
  profile: Profile(name) | env ProfileEnv (default APP_PROFILE); "" = none
    name !~ ^[a-z0-9][a-z0-9_-]*$ -> ErrInvalidOption (no traversal)
    set but no file          -> ErrProfileNotFound
  every file opened through os.Root(dir): symlinks resolve only inside the dir
  every file read through io.LimitReader(MaxFileSize, default 4 MiB) -> ErrFileTooLarge

Load(ctx) (serialized):  read layers -> parse -> canonical keys -> merge (uniform form)
                         -> snapshot{sorted keys, map} --atomic.Pointer.Store--+
Lookup / Keys:           atomic.Pointer.Load() -> one snapshot per call  <-----+
```

- **Parsing:**
  - YAML is decoded into `any` first, which keeps yaml.v3's alias-ratio guard and its "contains itself" check. A walk of `yaml.Node` is used only to find line numbers.
  - Duplicate keys, multiple documents and a non-mapping top level fail in every format.
  - Errors are `*config.ParseError{File, Line, Category}` with text `<file>:<line>: <category>`. Parser text is never copied or wrapped.
- **Scalar rendering:**
  - YAML: the node's literal text.
  - JSON: the `UseNumber` literal.
  - TOML: decimal integers, floats as `strconv.FormatFloat(f, 'g', -1, 64)`, datetimes as RFC 3339.
  - `null` means the key is absent.
- **`.env`:**
  - Its own small parser: `export`, quotes, comments, escapes, no `${}`, CRLF and BOM accepted.
  - `DotEnvExport` sets only unset variables, once, at the first `Load`, and logs the names it skipped.
- **Option names:** bare (`Dir`, `File`, `Name`, `Format(FileFormat)`, `Codec`, `Profile`, `ProfileEnv`, `EnvPrefix`, `EnvMapper`, `DotEnv`, `DotEnvExport`, `Layer`, `LayerOrder`, `Logger`, `DisableWatch`, `PollInterval`, `MaxFileSize`). Clashing types are renamed: `FileFormat`, `Decoder`, `LayerSource`, `LayerName`.
- **Alternatives:**
  - `With*` names (D41 B). Rejected: longer, and the root's options don't use `With`.
  - Walking `yaml.Node` directly. Rejected: it allows billion-laughs memory exhaustion and a fatal stack overflow.

### 9. Typed binding and the handle

- **`BindConfig[T]` and `BindConfigNamed[T]`:**
  1. Take the container's reload lock.
  2. Bind and validate against the committed view.
  3. Register `T` and `ConfigValue[T]` atomically, through an internal non-panicking provide; both are registered or neither.
  4. Add a reload entry owned by the target scope.
- **Registration rules:**
  - An `AsDefault` registration under the name is replaced, as default-registrations specifies.
  - A non-default one returns `ErrConfigAlreadyBound`.
  - A child scope may shadow its parent's registration.
  - Once shutdown has begun, binding returns `ErrStartAfterShutdown`.
- **The handle:** `ConfigValue[T]` holds `*cell[T]`; the cell holds `atomic.Pointer[T]` and the subscribers.
  - Copies share state.
  - The zero value's `Get` returns zero `T`, and its `OnChange` returns a no-op `stop`.
  - `go vet copylocks` stays quiet.
  - `Get()` results are documented as read-only.
- **Other entry points:**
  - `ConfigValueName(name)` gives the named handle's service name.
  - `ResolveConfig` binds a copy of `defaults` and registers nothing.
  - `BindProperties` leaves `*target` unchanged on error.
- **Alternatives:**
  - **Inline `atomic.Pointer`.** Copies would miss reloads.
  - **A non-generic `BindOption`.** `T` can't be inferred, so the examples don't compile.

### 10. Reload: the root owns the commit, and a dispatcher runs callbacks (D14, D33)

```
watcher goroutine (source)        root (config state)                    dispatcher goroutine
--------------------------        -------------------                    --------------------
poll, debounce (decision 11)
parse+merge -> staged S'  (or err)
changed(ctx, S', err) ----------> lock reload
                                  err != nil? -> unlock, warn, enqueue hooks -> false
                                  for each entry e (copy of the entry list):
                                      v_e := e.rebind(S')  (bind+validate, no user
                                                            callback runs under the lock)
                                  any failure? -> unlock, warn (keys only),
                                                  enqueue HookConfigReloadFailed  -> false
                                  committed.Store(S')    (property reads, ResolveConfig,
                                                          ExtensionProperties now see S')
                                  for each e: e.publish(v_e)   (all handles before any
                                                                notification)
                                  for each section whose raw key/value set changed:
                                      enqueue (coalesce to the latest per section) ---> run subscribers,
                                  unlock                                                 outside every lock;
          <---------------------- return true                                            panic recovered,
adopt S' as its own state                                                                logged by type only;
(false: drop S')                                                                         hooks the same way
```

- **No mixed values:** every section is rebound from the same `S'` before anything is published, and the committed-view pointer moves before any handle does. A plain `T` registered at bind time never changes.
- **Contract rules:**
  - The source never holds a lock while calling `changed`, never mutates `next`, and adopts `next` only if `changed` returned true.
  - A staging failure arrives as `changed(ctx, nil, err)`.
- **Deadlock freedom:**
  - Subscribers and hooks never run on the watcher goroutine or under the reload lock. A hook may call `Shutdown`, `BindConfig` or `ResolveConfig`.
  - `Shutdown` stops the watcher and the dispatcher, and waits for them only until its context ends (`ShutdownWithContext`). It then reports `Succeed=false` with an error matching `context.DeadlineExceeded`.
- **Change detection:** compare the section's raw key/value set under its prefix (exact and cheap), not `reflect.DeepEqual`, which never treats a NaN field as equal.
- **Stopping a subscriber:** `stop()` marks it stopped with an `atomic.Bool`, checked before each call. After `stop` returns, the callback is never called again, even within a round that already started.
- **Alternatives:**
  - **`changed(next) bool` with publishing after the source's swap.** Impossible: the root never regains control (blocker).
  - **A two-phase `commit func()`.** Mixed views remain possible between the swap and the commit.
  - **Running subscribers on the watcher goroutine.** A hook that calls `Shutdown` would deadlock (blocker).

### 11. Watching, debounce and membership (D15, D16, D17, D40)

```
membership set of started members (root and clones share one config state)

  root.Start()   clone.Start()   clone.Shutdown()   root.Shutdown()
  {root}  ->     {root,clone} -> {root}       ->    {}  -> stop watcher + dispatcher
  ^ first member starts watcher+dispatcher           (bounded by Shutdown's ctx)

  Shutdown without Start, double Shutdown: no-op (set membership, not a counter)
  a member's Shutdown removes the reload entries bound in it or its child scopes
  source-level failures fire the original root's hooks/Logf; a section's failure fires
  the hooks/Logf of the container that bound it
```

```
config.Files watching (default on; DisableWatch() -> Watch returns nil at once = not watching)

tick every PollInterval (0 -> 10s, <0 -> ErrInvalidOption at Load)
  fingerprint := sha256 of the bytes of every file Load read, plus ./.env
  same as the committed one?               -> nothing
  differs, but differs from the last tick   -> remember it, wait one more tick (debounce)
  equal on two consecutive ticks            -> stage: parse+merge -> changed(ctx, S', nil)
  a watched file missing, or empty when it  -> changed(ctx, nil, err): failed reload
  was non-empty at Load                        (last good config kept)
  files that appear after Load are not watched (documented)
  ..data symlink swap (Kubernetes): os.ReadFile follows it -> new bytes -> reload
  env layer is the Load-time snapshot; added layers are re-read on each staging
```

- **Internal wiring:** the watcher and dispatcher belong to `RootScope`, like the health scheduler. They are not a registered service, so they don't appear in listings, exports or health reports, `Clone` doesn't copy them, and `DisableStarterAutoDetection` can't turn watching off by accident.
- **No source registry (D40):** godoc states that a source serves one container and its clones.
- **Context:** the watcher's context is `context.WithCancel(context.WithoutCancel(startCtx))`. Values such as tracing carry over; only cancellation is cut.
- **Alternatives:**
  - **A refcount.** It goes negative on unbalanced Start/Shutdown.
  - **A process-wide registry (D40 B).** It leaks, treats equal values as one source, and can panic on some types.
  - **A public `config.Ticker`.** A test seam would leak into the API; tests use `testing/synctest` instead.

### 12. Logging defaults and secrecy

- **Logging:** `SetLogf` is called only when the user set `InjectorOpts.Logf`. The root's config warnings go to `Logf` when set, and to `slog.Default().Warn` otherwise, so "warn by default" is actually heard. Standalone, the config module logs to `slog.Default()`, looked up per call, unless `Logger(l)` is given.
- **What error texts may name:** keys, types, files, lines and categories. Never a value.
- **A source's own error:** wrapped, so `errors.Is` reaches it, but the text names only its type. Example: `DI: config: load failed: source *main.vault: *main.sealedError`.
- **Fixed sentinel texts:** set in config-sources and config-binding.
- **Checks in the secrecy tests:** they plant a high-entropy marker value and assert it is absent from `err.Error()`, from `fmt.Sprintf("%#v", err)`, from every `errors.As` target, and from every log line.

### 13. Module, CI and release topology

```
repo
+-- go.mod            github.com/kartaladev/doger      (+ validator/v10)
+-- go.work           use ., ./config, ./http/*, ./scheduler/gocron, ./examples/*
+-- testdata/envmapping.txt  (golden table, read by both test suites)
+-- config/go.mod     github.com/kartaladev/doger/config
                      require yaml/v3, go-toml/v2, doger (tests only)

CI (test.yml / lint.yml)                 release.yml
------------------------                 -----------
make test           (root)               1. tag root  vX.Y.Z
make test-config    (cd config; -race)   2. pin http/*, scheduler/*, config/ to vX.Y.Z
make lint + lint config/                 3. drop *_test.go except *_example_test.go,
job GOWORK=off: go test ./... in            then go mod tidy in config/ (drops doger
  config/ against the pinned require        unless an Example imports it)
deps step: cd config && go list -deps .  4. tag config/vX.Y.Z (with http/*, scheduler/*)
  | grep -v std -> only yaml, toml
security.yml: govulncheck re-enabled; .trivyignore documents the validator x/ modules policy
```

- Alternatives:
  - **Adding `./config` to `make test`.** `./...` from the root can't reach a nested module.
  - **Not committing `go.work`.** It's already committed, so CI needs the `GOWORK=off` job to catch missing pins.

## Risks / Trade-offs

- **[Two breaking signatures: `NewWithOpts`, `CloneWithOpts`]** → No tag exists and no app is deployed. A gopatch rewrite moves ~193 `NewWithOpts` and ~44 clone call sites to `root, err :=`, or to `MustNewWithOpts` in tests. The upgrading note shows each migration.
- **[Env variable names change]** → Breaking for anyone using `DOGER_*`. The upgrading note lists old → new, and a scenario proves the old names are no longer read.
- **[go-playground adds 8 modules to every consumer (D7)]** → Accepted. It is built lazily, so it costs nothing at runtime until used. govulncheck is re-enabled, and the trivy policy covers it.
- **[The Spring mapping lives in two modules]** → One golden file is read by both test suites.
- **[Map keys outside `[a-z0-9]+` can't be set from env]** → Documented. Files and added layers can still set them.
- **[A `Load` that ignores its context blocks construction]** → Documented: cancellation is cooperative. `config.Files` honors ctx.
- **[A slow subscriber delays later notifications]** → The dispatcher coalesces to the latest value per section, and the watcher never waits on it. Documented: keep subscribers fast.
- **[Polling costs a sha256 of every watched file every 10s]** → Config files are small and `MaxFileSize` caps them. `PollInterval` and `DisableWatch` override this.
- **[Debounce delays a reload by one interval]** → This is deliberate: it stops half-written files from resetting config to defaults.
- **[Files created after `Load` are not noticed]** → Documented. The answer is a restart or a `Layer`.
- **[Pre-existing gap: the `module-identity` and `toolchain-support` main specs have no row for `scheduler/gocron`, though both claim to cover every workspace entry]** → Out of scope. This change adds only the `config` rows. Flagged for a follow-up change.

## Public API & Defaults

**Root module `doger`.** BREAKING items are flagged; everything else is additive.

- **Constructors:**
  - **BREAKING** `NewWithOpts(opts *InjectorOpts, packages ...func(Injector)) (*RootScope, error)`
  - **BREAKING** `(*RootScope).CloneWithOpts(opts *InjectorOpts) (*RootScope, error)`
  - `NewWithOptsContext(ctx context.Context, opts *InjectorOpts, packages ...func(Injector)) (*RootScope, error)`
  - `MustNewWithOpts(opts *InjectorOpts, packages ...func(Injector)) *RootScope`: panics with the error.
  - Unchanged: `New(packages ...func(Injector)) *RootScope`, `(*RootScope).Clone() *RootScope`, `DefaultRootScope`.
- **`InjectorOpts` fields:**
  - `ConfigSource ConfigSource`: nil means no config source.
  - `ConfigTimeout time.Duration`: 0 means `DefaultConfigTimeout` (10s); negative means none. Measured on `Clock`.
  - `ConfigValidator ConfigValidator`: nil means go-playground.
  - `ConfigTagKey string`: "" means `DefaultConfigTagKey` ("cfg").
  - `HookConfigReloadFailed []func(ctx context.Context, scope *RootScope, err error)`, plus `(*RootScope).AddConfigReloadFailedHook(fn)`.
- **Constants:** `DefaultConfigTimeout`, `DefaultConfigTagKey`, `DefaultConfigPollInterval`.
- **Interfaces:**
  - `PropertySource{Lookup}`: unchanged.
  - `PropertyKeys{Keys(prefix string) []string}`
  - `ConfigSource{PropertySource; PropertyKeys; Load(ctx context.Context) error}`
  - `ConfigLogger{SetLogf(func(format string, args ...any))}`
  - `type ConfigView = interface{ Lookup(key string) (string, bool); Keys(prefix string) []string }`
  - `ConfigWatcher{Watch(ctx context.Context, changed func(ctx context.Context, next ConfigView, err error) (commit bool)) error}`
  - `ConfigValidator{Validate(v any) error}`
  - `Properties.Keys(prefix string) []string`
- **Binding:**
  - `type BindOption[T any] func(*bindOptions[T])`
  - `ConfigDefaults[T any](v T) BindOption[T]`
  - `BindConfig[T any](i Injector, prefix string, opts ...BindOption[T]) error`
  - `BindConfigNamed[T any](i Injector, name, prefix string, opts ...BindOption[T]) error`
  - `ConfigValueName(name string) string`
  - `ResolveConfig[T any](i Injector, prefix string, defaults T) (T, error)`
  - `ConfigValue[T any]`, with `Get() T` and `OnChange(fn func(T)) (stop func())`
  - `StaticConfig[T any](v T) ConfigValue[T]`
  - `BindProperties(p Properties, target any) error`
  - `ValidateConfig(i Injector, v any) error`
  - `type MapSource map[string]string`
- **Errors** (every text starts with `DI: config:`):
  - sentinels: `ErrConfigLoad`, `ErrConfigConflict`, `ErrInvalidConfigSource`, `ErrInvalidConfigValue`, `ErrInvalidConfig`, `ErrInvalidConfigTarget`, `ErrConfigNameConflict`, `ErrConfigAlreadyBound`, `ErrConfigWatch`
  - error types: `*ConfigBindError{Key string; Type reflect.Type}`, `*ConfigValidationError{Fields []ConfigFieldError}`, `ConfigFieldError{Key, Rule string}`
- **BREAKING** `EnvPropertySource(prefix)` variable names: Spring mapping, `__` for an instance, and the prefix normalized. An invalid prefix panics.
- **Key grammar:** gains `[i]` and `[key]` suffixes.

**Config module `config`**
- **Constructor:** `Files(opts ...Option) *Source`, where `*Source` has `Load`, `Lookup`, `Keys`, `SetLogf` and `Watch`.
- **Types:** `type View = interface{...}`, `type Option func(*options)`.
- **Options:**
  - `Dir(paths ...string)`, `File(paths ...string)`, `Name(base string)`
  - `Format(FileFormat)`, with `YAML`, `TOML`, `JSON`; `Codec(ext string, d Decoder)`
  - `Profile(name string)`, `ProfileEnv(name string)`
  - `EnvPrefix(prefix string)`, `EnvMapper(func(key string) string)`
  - `DotEnv(path string)`, `DotEnvExport()`
  - `Layer(name string, src LayerSource)`, `LayerOrder(names ...LayerName)` with `LayerBase`, `LayerProfile`, `LayerDotEnv`, `LayerEnv`
  - `Logger(*slog.Logger)`
  - `DisableWatch()`, `PollInterval(time.Duration)`, `MaxFileSize(int64)`
- **Errors:** `ErrAmbiguousFormat`, `ErrProfileNotFound`, `ErrInvalidOption`, `ErrFileTooLarge`, `*ParseError{File string; Line int; Category string}`. A missing `File` matches `fs.ErrNotExist`.
- **Defaults:** as in the proposal's Defaults & Control table. Every one is overridden by the field or option named there.

**Migration for consumers:**
- `root := doger.NewWithOpts(o)` becomes `root, err := doger.NewWithOpts(o)`, or `doger.MustNewWithOpts(o)` in tests.
- `clone := root.CloneWithOpts(o)` becomes `clone, err := root.CloneWithOpts(o)`.
- `DOGER_REDIS_POOL__SIZE` becomes `DOGER_REDIS_POOLSIZE`, and `DOGER_REDIS___QUEUE_ADDR` becomes `DOGER_REDIS__QUEUE_ADDR`.
- `EnvPropertySource("MYAPP")` now reads `MYAPP_…`; before, the prefix was used as written.
- `doger.New(...)`, `Clone()` and custom `PropertySource` implementations need no change.

## Migration Plan

Task groups and their dependencies. Each arrow means "needs".

```
P1
 [1 scaffolding: module, go.work, identity/toolchain rows, CI, release, govulncheck]
   |
 [2 key grammar] -> [3 env mapping + golden file] -> [4 PropertyKeys, MapSource, Properties.Keys]
                                                          |
 [5 constructors + ConfigSource install] -> [6 Load ctx, timeout, logging] -> [7 failures, secrecy]
                                                          |
                                                  [8 clones]
                                                          |
 [9 binder: scalars+conversions] -> [10 names, tags] -> [11 presence, nesting, recursion]
     -> [12 lists, maps, pointers] -> [13 errors, panics, ResolveConfig API]
     -> [14 BindConfig + handles] -> [15 BindProperties] -> [16 validation + ValidateConfig]
                                                          |
 [17 discovery, formats, profiles] -> [18 keys, rendering, shapes, errors] -> [19 layers, env]
     -> [20 .env] -> [21 added layers, options, ctx, concurrency, logging] -> [22 end-to-end binding]
P2                                                        |
 [23 watcher lifecycle] -> [24 commit, all or nothing] -> [25 failures, hooks]
     -> [26 dispatcher, subscribers, bounded Shutdown] -> [27 clones, membership, bind vs reload]
     -> [28 config.Files polling, debounce, symlink, hostile files, contract checks]
                                                          |
 [29 integration only]
```

Steps:
1. Land the change on a branch. It is untagged, so there is no deprecation window.
2. Rewrite call sites with gopatch in the root, then in `scheduler/gocron`, `http/*`, `examples/*`, and the snippets in `docs/` and README.
3. Build and test every workspace module, plus the `GOWORK=off` job.
4. Rollback is a revert. No data or persistent state is involved.

## Test Strategy

**Test kinds:**
- Table-driven unit tests with named subtests (testify; `require` for preconditions, `assert` for checks).
- `-race` for every concurrency scenario.
- goleak via `TestMain` in both modules.
- `testing/synctest` for every timing scenario: load deadlines, polling, debounce, bounded `Shutdown`, dispatcher coalescing. There is no public ticker seam and no wall-clock sleeps.
- Fake `InjectorOpts.Clock` where the clock itself is under test.
- `t.TempDir` and `t.Chdir` for files.
- `t.Setenv` with a unique prefix per test (for example `TKEYS_`), so inherited CI variables (`APP_ENV`, `APP_PROFILE`) never leak in. Tests using `t.Chdir` or `t.Setenv` don't call `t.Parallel`.
- Tests that touch `DefaultRootScope` or `slog.SetDefault` swap them and restore with `t.Cleanup`, and are not parallel, so `-count=2` passes.
- `Example*` tests use `testdata/` directories and unique env prefixes, so their `// Output:` is deterministic.
- Fuzz tests with committed seed corpora:
  - `FuzzKeyGrammar`: accepted keys map to an env name and back to the same uniform form.
  - `FuzzBind_Scalars`
  - `FuzzFiles_YAML`, `FuzzFiles_TOML`, `FuzzFiles_JSON`, `FuzzFiles_DotEnv`

  Each fuzz test asserts no panic. Values are planted as a high-entropy marker (`zq8X…`) in value positions only, and the marker must never appear in an error. YAML seeds include billion-laughs and self-referencing anchors.
- CI steps: the config-module dependency footprint, and the `GOWORK=off` build.
- No benchmarks: config is not on a hot path. `ConfigValue.Get` is a single atomic load.

**Where tests live:**
- Root tests use `MapSource`, `EnvPropertySource` or fake sources and watchers.
- Tests that need `config.Files` live in `config/*_test.go`. That module imports doger; the root never imports config.

**Spikes → RED tests:**
- **Spike 1 (watcher leak):** `TestConfigWatch_Lifecycle`
- **Spike 2 (re-entrant OnChange):** `TestConfigReload_ReentrantCallbacks`
- **Spike 3 (reload race):** `TestConfigReload_ConcurrentReads` and `config/files_test.go: TestFiles_ConcurrentReadsDuringLoad`
- **Spike 4 (env-only key):** `TestResolveConfig_EnvOnlyKey` and `config/integration_test.go: TestFiles_BindEnvOnlyKey` (scenario "An env-only key binds although the file lacks it")
- **Spike 5 (`.env` export):** `config/dotenv_test.go: TestFiles_DotEnv`

**Hot paths and their tests:**
- **Construction and load:**
  - `TestNewWithOpts_ConstructionOutcome`
  - `TestNewWithOptsContext_LoadContext`
  - `TestConfigSource_LoadFailure`
  - `TestConfigSource_FakeClockContext`
- **Property lookup, grammar, env mapping:**
  - `TestProperties_InvalidKeys`
  - `TestProperties_KeySuffixes`
  - `TestEnvPropertySource_Mapping`
  - `TestEnvPropertySource_Keys`
- **Binder:** every `TestBind_*`
- **Typed binding:** `TestBindConfig_*`, `TestResolveConfig_*`, `TestConfigValue_Get`
- **Validation:** every `TestValidate_*`
- **Reload commit:**
  - `TestConfigWatch_CommitDecision`
  - `TestConfigReload_AllOrNothing`
  - `TestConfigReload_ReadPathsAgree`
- **Concurrency:**
  - `TestBindConfig_Concurrent`
  - `TestValidate_Concurrent`
  - `TestConfigReload_NoMixedValues`
  - `TestConfigReload_ConcurrentReads`
  - `TestBindConfig_DuringReload`
  - `TestFiles_ConcurrentReadsDuringLoad`
- **Lifecycle and shutdown:**
  - `TestConfigWatch_Lifecycle`
  - `TestConfigReload_ShutdownBoundedByContext`
  - `TestConfigReload_CloneShares`
  - `TestConfigReload_ShutdownMemberSectionsLeave`

**Example tests:**
- Root:
  - `ExampleNewWithOpts_config`
  - `ExampleNewWithOptsContext`
  - `ExampleBindConfig`
  - `ExampleResolveConfig`
  - `ExampleConfigValue_OnChange`
  - `ExampleStaticConfig`
  - `ExampleBindProperties`
  - `ExampleMapSource`
- Config: `ExampleFiles`, `ExampleFiles_overrides`

**Scenario map:**
- Every `#### Scenario:` in `specs/*/spec.md` appears exactly once below.
- File names without a module prefix are in the root package.
- Examples rows run as named subtests.

| Capability | Scenario | Test | Kind |
|---|---|---|---|
| container-construction | Construction outcome | `root_scope_test.go: TestNewWithOpts_ConstructionOutcome` | unit, table |
| container-construction | New is unchanged | `root_scope_test.go: TestNew_Unchanged` | unit |
| container-construction | The context reaching Load | `root_scope_test.go: TestNewWithOptsContext_LoadContext` | unit, table, fake clock |
| container-construction | Cancelling construction | `root_scope_test.go: TestNewWithOptsContext_Cancel` | unit, synctest |
| container-construction | Load happens before the packages | `root_scope_test.go: TestNewWithOpts_LoadBeforePackages` | unit |
| container-construction | A failed construction runs no package | `root_scope_test.go: TestNewWithOpts_FailureRunsNoPackage` | unit, table |
| container-construction | Cloning with options | `root_scope_test.go: TestCloneWithOpts_Outcome` | unit, table |
| container-construction | Clone of a root with config | `root_scope_test.go: TestClone_SharesConfig` | unit |
| container-construction | Must constructor | `root_scope_test.go: TestMustNewWithOpts` | unit, table |
| container-construction | Registering on the default root | `root_scope_test.go: TestDefaultRootScope_Usable` | unit, non-parallel |
| container-construction | Duplicate registration in a package | `root_scope_test.go: TestNew_PackagePanicPropagates` | unit, table |
| config-sources | Which types are sources | `config_source_test.go: TestConfigSource_Contract` | unit, table |
| config-sources | Property reads go to the installed source | `config_source_test.go: TestConfigSource_IsPropertySource` | unit |
| config-sources | Invalid option combinations | `config_source_test.go: TestConfigSource_OptionChecks` | unit, table |
| config-sources | SetLogf and Load order | `config_source_test.go: TestConfigSource_SetLogfOrder` | unit, table |
| config-sources | The logger reaches the source | `config_source_test.go: TestConfigSource_LogfReachesSource` | unit, non-parallel (slog default) |
| config-sources | The Load deadline | `config_source_test.go: TestConfigSource_LoadDeadline` | unit, table, fake clock |
| config-sources | The Load context on a fake clock | `config_source_test.go: TestConfigSource_FakeClockContext` | unit, goleak |
| config-sources | A Load that honors its deadline | `config_source_test.go: TestConfigSource_LoadTimeout` | unit, synctest |
| config-sources | A Load that ignores its deadline | `config_source_test.go: TestConfigSource_LoadIgnoresDeadline` | unit, synctest |
| config-sources | Load errors and panics | `config_source_test.go: TestConfigSource_LoadFailure` | unit, table |
| config-sources | Sharing on clone | `config_source_test.go: TestConfigSource_CloneShares` | unit, table |
| config-sources | Secrets stay out of construction errors and logs | `config_source_test.go: TestConfigSource_NoValuesInErrorsOrLogs` | unit, marker |
| extension-properties | Invalid keys are not looked up | `properties_test.go: TestProperties_InvalidKeys` | unit, table |
| extension-properties | Source selection | `properties_test.go: TestPropertySource_Selection` | unit, table |
| extension-properties | A source registered after the first read is not used | `properties_test.go: TestPropertySource_LateServiceIgnored` | unit |
| extension-properties | Registering the interface name after the first read panics | `properties_test.go: TestPropertySource_LateSameNamePanics` | unit |
| extension-properties | An installed config source is decided before any package runs | `properties_test.go: TestPropertySource_ConfigSourceAtConstruction` | unit |
| extension-properties | Index and map-key suffixes | `properties_test.go: TestProperties_KeySuffixes` | unit, table |
| extension-properties | Key to variable mapping | `properties_test.go: TestEnvPropertySource_Mapping` | unit, table (golden file) |
| extension-properties | Nested, instance and index keys use different variables | `properties_test.go: TestEnvPropertySource_Distinct` | unit |
| extension-properties | Keys that share a variable | `properties_test.go: TestEnvPropertySource_SharedVariables` | unit, table |
| extension-properties | Map keys the environment cannot address | `properties_test.go: TestEnvPropertySource_UnaddressableMapKeys` | unit, table |
| extension-properties | An unset variable is a miss | `properties_test.go: TestEnvPropertySource_Unset` | unit |
| extension-properties | Variables in the old mapping are no longer read | `properties_test.go: TestEnvPropertySource_OldMappingIgnored` | unit, table |
| extension-properties | An invalid prefix panics | `properties_test.go: TestEnvPropertySource_InvalidPrefixPanics` | unit, table |
| extension-properties | Map source keys | `properties_test.go: TestMapSource_Keys` | unit, table |
| extension-properties | Every key and the nil map | `properties_test.go: TestMapSource_AllKeys` | unit, table |
| extension-properties | A map source reads the map it is | `properties_test.go: TestMapSource_NotCopied` | unit |
| extension-properties | Environment source keys | `properties_test.go: TestEnvPropertySource_Keys` | unit, table |
| extension-properties | Keys through an extension view | `properties_test.go: TestProperties_Keys` | unit, table |
| extension-properties | Views that cannot list keys | `properties_test.go: TestProperties_KeysUnavailable` | unit, table |
| module-identity | Workspace module paths mirror their directories | `module_identity_test.go: TestModuleIdentity_WorkspaceModules` | unit, table |
| toolchain-support | Module go directives declare 1.27 | `toolchain_test.go: TestToolchain_GoDirective` | unit, table |
| config-binding | An env-only key binds | `config_bind_test.go: TestResolveConfig_EnvOnlyKey` | unit |
| config-binding | The source receives canonical keys | `config_bind_test.go: TestBind_CanonicalKeys` | unit, table |
| config-binding | Multi-word lists, maps and pointers bind from env | `config_bind_test.go: TestBind_UniformKeysFromEnv` | unit, table |
| config-binding | Tags rename or skip fields | `config_bind_test.go: TestBind_TagOverride` | unit, table |
| config-binding | The tag key can be changed | `config_bind_test.go: TestBind_ConfigTagKey` | unit, table |
| config-binding | Invalid tags | `config_bind_test.go: TestBind_InvalidTag` | unit, table |
| config-binding | Conflicting names | `config_bind_test.go: TestBind_NameConflict` | unit, table |
| config-binding | Present zero values overwrite defaults, absent keys keep them | `config_bind_test.go: TestBind_Presence` | unit, table |
| config-binding | One nested key keeps its siblings' defaults | `config_bind_test.go: TestBind_NestedSiblingDefaults` | unit |
| config-binding | Embedded structs bind under the parent's key | `config_bind_test.go: TestBind_Embedded` | unit, table |
| config-binding | Recursive targets | `config_bind_test.go: TestBind_RecursiveType` | unit, table |
| config-binding | Indexed list sources | `config_bind_test.go: TestBind_Lists` | unit, table |
| config-binding | A list of structs starts each element from zero | `config_bind_test.go: TestBind_ListOfStructs` | unit |
| config-binding | Comma values | `config_bind_test.go: TestBind_CommaSplit` | unit, table |
| config-binding | Map entries merge over defaults | `config_bind_test.go: TestBind_MapMerge` | unit, table |
| config-binding | A map of structs merges entries field by field | `config_bind_test.go: TestBind_MapOfStructs` | unit |
| config-binding | Non-string map keys are rejected | `config_bind_test.go: TestBind_MapKeyKind` | unit, table |
| config-binding | Pointer allocation | `config_bind_test.go: TestBind_Pointers` | unit, table |
| config-binding | A Lookup-only source | `config_bind_test.go: TestBind_WithoutPropertyKeys` | unit, table |
| config-binding | Conversions | `config_bind_test.go: TestBind_Conversions` | unit, table |
| config-binding | Conversion errors | `config_bind_test.go: TestBind_ConversionErrors` | unit, table |
| config-binding | Panics in user code | `config_bind_test.go: TestBind_PanicRecovered` | unit, table, marker |
| config-binding | Defaults survive binding | `config_bind_test.go: TestBind_DefaultsNotMutated` | unit |
| config-binding | Invalid type parameters | `config_bind_test.go: TestBindConfig_InvalidType` | unit, table |
| config-binding | Invalid prefixes | `config_bind_test.go: TestResolveConfig_InvalidPrefix` | unit, table |
| config-binding | A typed-nil injector | `config_bind_test.go: TestResolveConfig_TypedNilInjector` | unit |
| config-binding | A bound section is invocable | `config_bind_test.go: TestBindConfig_Registers` | unit |
| config-binding | Default options | `config_bind_test.go: TestBindConfig_DefaultsOption` | unit, table |
| config-binding | A failed bind registers nothing | `config_bind_test.go: TestBindConfig_FailureRegistersNothing` | unit, table |
| config-binding | A nil injector binds into DefaultRootScope | `config_bind_test.go: TestBindConfig_NilInjector` | unit, non-parallel |
| config-binding | A child scope binds from its container and registers locally | `config_bind_test.go: TestBindConfig_ChildScope` | unit |
| config-binding | Two databases of one type | `config_bind_test.go: TestBindConfigNamed_TwoPrefixes` | unit |
| config-binding | An empty name is rejected | `config_bind_test.go: TestBindConfigNamed_EmptyName` | unit |
| config-binding | Duplicate bindings | `config_bind_test.go: TestBindConfig_Duplicate` | unit, table |
| config-binding | Defaults and parent registrations give way | `config_bind_test.go: TestBindConfig_ReplacesDefaultShadowsParent` | unit, table |
| config-binding | Binding in each phase | `config_bind_test.go: TestBindConfig_Phases` | unit, table |
| config-binding | Resolving after shutdown | `config_bind_test.go: TestResolveConfig_AfterShutdown` | unit |
| config-binding | A PropertySource service registered after a bind | `config_bind_test.go: TestBind_DecidesPropertySource` | unit |
| config-binding | Two consumers resolve one prefix with different defaults | `config_bind_test.go: TestResolveConfig_ConsumerDefaults` | unit |
| config-binding | A failed resolve returns the zero value | `config_bind_test.go: TestResolveConfig_Failure` | unit |
| config-binding | Resolving inside a provider uses the provider's container | `config_bind_test.go: TestResolveConfig_InProviderAndClone` | unit, table |
| config-binding | Handle values | `config_bind_test.go: TestConfigValue_Get` | unit, table |
| config-binding | OnChange on a handle that never changes | `config_bind_test.go: TestConfigValue_OnChangeStatic` | unit |
| config-binding | An extension binds over its defaults | `extension_test.go: TestBindProperties_Extension` | unit, table |
| config-binding | A failed bind leaves the target unchanged | `extension_test.go: TestBindProperties_TargetUnchangedOnError` | unit |
| config-binding | Invalid targets | `extension_test.go: TestBindProperties_InvalidTarget` | unit, table |
| config-binding | Binding from a map literal | `config_bind_test.go: TestMapSource_Bind` | unit |
| config-binding | Concurrent use under -race | `config_bind_test.go: TestBindConfig_Concurrent` | race |
| config-validation | Default tag validation | `config_validate_test.go: TestValidate_DefaultTags` | unit, table |
| config-validation | Range boundaries | `config_validate_test.go: TestValidate_RangeBoundaries` | unit, table |
| config-validation | A skipped field with a failing tag | `config_validate_test.go: TestValidate_SkippedField` | unit |
| config-validation | Validate method results | `config_validate_test.go: TestValidate_Method` | unit, table |
| config-validation | A Validate error's text is kept as written | `config_validate_test.go: TestValidate_MethodErrorText` | unit |
| config-validation | Nested Validate methods are not called | `config_validate_test.go: TestValidate_TopLevelOnly` | unit |
| config-validation | Validator choice | `config_validate_test.go: TestValidate_Replacement` | unit, table |
| config-validation | Clones keep or replace the validator | `config_validate_test.go: TestValidate_CloneValidator` | unit, table |
| config-validation | Defaults are validated too | `config_validate_test.go: TestValidate_MergedValue` | unit, table |
| config-validation | Error shape and keys | `config_validate_test.go: TestValidate_ErrorShape` | unit, table |
| config-validation | Secret values stay out of errors and logs | `config_validate_test.go: TestValidate_NoValuesLeaked` | unit, marker |
| config-validation | An extension validates after applying its options | `extension_test.go: TestValidateConfig_Extension` | unit, table |
| config-validation | Invalid values to validate | `config_validate_test.go: TestValidateConfig_InvalidTarget` | unit, table |
| config-validation | Validator panics | `config_validate_test.go: TestValidate_PanicRecovered` | unit, table, marker |
| config-validation | Concurrent binds validate under -race | `config_validate_test.go: TestValidate_Concurrent` | race |
| config-files | Base files found in the working directory and ./config | `config/files_test.go: TestFiles_Discovery` | unit, table, t.Chdir |
| config-files | Both directories merge key by key | `config/files_test.go: TestFiles_DiscoveryMergesDirs` | unit, t.Chdir |
| config-files | Four files and a split list | `config/files_test.go: TestFiles_DiscoveryOrder` | unit, t.Chdir |
| config-files | Two formats for one base name | `config/files_test.go: TestFiles_AmbiguousFormat` | unit, table |
| config-files | Profile selection | `config/files_test.go: TestFiles_Profile` | unit, table, t.Setenv |
| config-files | A profile file without a base file | `config/files_test.go: TestFiles_ProfileWithoutBase` | unit |
| config-files | The profile variable is not a config key | `config/files_test.go: TestFiles_ProfileVariableNotAKey` | unit, t.Setenv |
| config-files | Profile names | `config/files_test.go: TestFiles_ProfileNames` | unit, table |
| config-files | Symlinks inside and outside the directory | `config/files_test.go: TestFiles_Symlinks` | unit, table, skip on Windows |
| config-files | Directories, files and base name | `config/files_test.go: TestFiles_ExplicitPaths` | unit, table |
| config-files | Built-in and custom decoders | `config/codec_test.go: TestFiles_Codecs` | unit, table |
| config-files | Flattening and normalization | `config/keys_test.go: TestFiles_FlattenAndNormalize` | unit, table |
| config-files | Shapes that fail | `config/keys_test.go: TestFiles_InvalidShapes` | unit, table |
| config-files | Rendering by format | `config/codec_test.go: TestFiles_ScalarRendering` | unit, table |
| config-files | Highest layer wins | `config/layers_test.go: TestFiles_LayerPrecedence` | unit, table, t.Setenv |
| config-files | Env overrides a dashed file key | `config/layers_test.go: TestFiles_UniformFormPrecedence` | unit, t.Setenv |
| config-files | Lists across layers | `config/layers_test.go: TestFiles_ListFromOneLayer` | unit, table, t.Setenv |
| config-files | Prefix forms | `config/env_test.go: TestFiles_EnvPrefix` | unit, table, t.Setenv |
| config-files | Key to variable | `config/env_test.go: TestFiles_EnvMapping` | unit, table (golden file) |
| config-files | Variables mapped back to keys | `config/env_test.go: TestFiles_EnvReverseMapping` | unit, table, t.Setenv |
| config-files | Mapper with and without a prefix | `config/env_test.go: TestFiles_EnvMapper` | unit, table, t.Setenv |
| config-files | .env handling | `config/dotenv_test.go: TestFiles_DotEnv` | unit, t.Chdir, t.Setenv |
| config-files | Line forms | `config/dotenv_test.go: TestFiles_DotEnvSyntax` | unit, table |
| config-files | A later line wins | `config/dotenv_test.go: TestFiles_DotEnvLaterLineWins` | unit |
| config-files | Export at the first Load | `config/dotenv_test.go: TestFiles_DotEnvExport` | unit, t.Setenv |
| config-files | A second Load exports nothing | `config/dotenv_test.go: TestFiles_DotEnvExportOnce` | unit, t.Setenv |
| config-files | A vault-style layer | `config/layers_test.go: TestFiles_AddedLayer` | unit, table |
| config-files | A failing added layer | `config/layers_test.go: TestFiles_AddedLayerLoadError` | unit, marker |
| config-files | Keys across layers | `config/keys_test.go: TestFiles_Keys` | unit, table, t.Setenv |
| config-files | A syntax error near a secret | `config/errors_test.go: TestFiles_LoadErrorHasNoValue` | unit, table, marker |
| config-files | Size limit | `config/files_test.go: TestFiles_MaxFileSize` | unit, table |
| config-files | Invalid options | `config/options_test.go: TestFiles_InvalidOptions` | unit, table |
| config-files | A cancelled load keeps the previous snapshot | `config/files_test.go: TestFiles_LoadContextCancelled` | unit |
| config-files | Two overlapping Loads | `config/files_test.go: TestFiles_LoadsSerialized` | race |
| config-files | Load while watching | `config/watch_test.go: TestFiles_LoadWhileWatching` | unit, synctest |
| config-files | Concurrent reads while loading | `config/files_test.go: TestFiles_ConcurrentReadsDuringLoad` | race |
| config-files | Log sink | `config/options_test.go: TestFiles_LogSink` | unit, non-parallel (slog default) |
| config-files | An env-only key binds although the file lacks it | `config/integration_test.go: TestFiles_BindEnvOnlyKey` | unit, t.Setenv |
| config-files | Every file spelling binds the same field | `config/integration_test.go: TestFiles_BindFileSpellings` | unit, table |
| config-files | Compile-time contract checks | `config/source_test.go: TestSource_Contract` | build + unit |
| config-files | Dependency footprint | `.github/workflows/test.yml: config-deps step (go list -deps . in config/)` | CI step |
| config-reload | The source adopts only what the root accepts | `config_reload_test.go: TestConfigWatch_CommitDecision` | unit, table, fake watcher |
| config-reload | Read paths agree after a commit and after a rejection | `config_reload_test.go: TestConfigReload_ReadPathsAgree` | unit, table |
| config-reload | The staged view is invisible until commit | `config_reload_test.go: TestConfigWatch_StagedInvisibleUntilCommit` | unit, synctest |
| config-reload | One invalid section blocks every section | `config_reload_test.go: TestConfigReload_AllOrNothing` | unit |
| config-reload | Readers never see a mixed value | `config_reload_test.go: TestConfigReload_NoMixedValues` | race |
| config-reload | Live handle versus snapshot after a reload | `config_reload_test.go: TestConfigReload_LiveVersusSnapshot` | unit |
| config-reload | Failures warn and call the hooks | `config_reload_test.go: TestConfigReload_FailedWarnsAndHooks` | unit, table, synctest |
| config-reload | A hook that panics does not stop reloads | `config_reload_test.go: TestConfigReload_HookPanic` | unit, synctest |
| config-reload | Who is notified | `config_reload_test.go: TestConfigValueOnChange_Notified` | unit, table, synctest |
| config-reload | Every handle is published before the first notification | `config_reload_test.go: TestConfigReload_PublishBeforeNotify` | unit, synctest |
| config-reload | Static and zero handles never notify | `config_reload_test.go: TestConfigValue_StaticAndZeroNeverNotify` | unit, synctest |
| config-reload | Stop semantics | `config_reload_test.go: TestConfigValueOnChange_Stop` | unit, table, synctest |
| config-reload | A nil callback is ignored | `config_reload_test.go: TestConfigValueOnChange_NilFunc` | unit |
| config-reload | A slow subscriber receives the latest value | `config_reload_test.go: TestConfigReload_DispatcherCoalesces` | unit, synctest |
| config-reload | A panicking subscriber | `config_reload_test.go: TestConfigValueOnChange_SubscriberPanic` | unit, synctest |
| config-reload | Re-entrant calls from a callback | `config_reload_test.go: TestConfigReload_ReentrantCallbacks` | unit, table, synctest |
| config-reload | A blocked callback does not hang Shutdown | `config_reload_test.go: TestConfigReload_ShutdownBoundedByContext` | synctest, goleak |
| config-reload | Concurrent reads under reload | `config_reload_test.go: TestConfigReload_ConcurrentReads` | race |
| config-reload | Watcher lifecycle | `config_reload_test.go: TestConfigWatch_Lifecycle` | unit, table, synctest, goleak |
| config-reload | How a watch ends | `config_reload_test.go: TestConfigWatch_Ends` | unit, table, synctest |
| config-reload | One reload reaches parent and clone | `config_reload_test.go: TestConfigReload_CloneShares` | unit, synctest |
| config-reload | A shut-down member's sections leave the reload set | `config_reload_test.go: TestConfigReload_ShutdownMemberSectionsLeave` | unit, synctest |
| config-reload | Whose hooks a failure fires | `config_reload_test.go: TestConfigReload_HookOwnership` | unit, table, synctest |
| config-reload | BindConfig waits for an in-progress reload | `config_reload_test.go: TestBindConfig_DuringReload` | race |
| config-reload | Poll interval and debounce | `config/watch_test.go: TestFiles_PollInterval` | unit, table, synctest |
| config-reload | A negative poll interval fails construction | `config/watch_test.go: TestFiles_PollIntervalNegative` | unit |
| config-reload | A partial write is not staged | `config/watch_test.go: TestFiles_PollDebouncesPartialWrite` | unit, synctest |
| config-reload | What a poll does | `config/watch_test.go: TestFiles_PollReloads` | unit, table, synctest, t.Chdir |
| config-reload | A Kubernetes ConfigMap swap is a change | `config/watch_test.go: TestFiles_PollSymlinkSwap` | unit, synctest, skip on Windows |
| config-reload | Hostile files | `config/watch_test.go: TestFiles_HostileYAML` | unit, table + fuzz seeds |
