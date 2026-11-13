# Design

## Context

See proposal.md for the motivation. The decision record (D1–D68, with options and reasons) is the Claude Doc "doger/config: Config as a First-Class Component". D33–D41 came from the first round of adversarial audits of this change, D42–D45 from the second and D46–D68 from the third (summarized in decision 13 below). Every decision was audited for idiomatic Go before being applied. The specs in `specs/*/spec.md` are the behavior contract. This document explains how the change meets them.

Today:
- `New(packages...) *RootScope` (`root_scope.go:43`) delegates to `NewWithOpts(opts, packages...) *RootScope` (`root_scope.go:67`), which calls `opts.initDefaults()` on the caller's own struct (`injector.go:447`). Neither can fail. `CloneWithOpts` (`root_scope.go:738`) copies the container with replaced options; `Clone()` passes `s.opts.copy()`.
- Extensions read config through `PropertySource` (`properties.go:34`, `Lookup` only). The source is decided at the first property read: `InjectorOpts.PropertySource`, then a service named `NameOf[PropertySource]()`, then `EnvPropertySource("DOGER_")` with its one-to-one mapping.
- `extension.go:86` states the rule this change follows for every extension point: optional interfaces, never new methods on an existing one.
- The health scheduler (`health_monitor.go:107`) is the precedent for an internal, non-service background goroutine that `Shutdown` stops, and for routing callback panics through `HookContainerPanic`.
- The root module requires clockwork, go-type-to-string, testify and goleak, plus `go.yaml.in/yaml/v3` (indirect). `make test` covers the root module. `scheduler/gocron`, `http/*` and `examples/*` are separate workspace modules with `replace github.com/kartaladev/doger => ../..`. The release workflow drops tests, tidies the root, then pins the adapters to the released root.
- Every existing ctx-taking method maps a nil ctx to `context.Background()` (`root_start.go:91`, `root_scope.go:280`); the new constructors follow it.
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
|   root, err := doger.NewWithOpts(ctx, &doger.InjectorOpts{         |
|       ConfigSource: config.New(...)})                              |
|   h, err := doger.BindConfig(root, "server", ServerConfig{})       |
+-------------+----------------------------------------+-------------+
              | imports                                | imports
              v                                        v
+-------------------------------+      +-------------------------------+
| root module: doger            |      | config module: doger/config   |
|                               |      |                               |
| contract (stdlib types only)  |<.....| config.New(opts...)           |
|   PropertySource (unchanged)  | sat- |   -> *config.Source           |
|   PropertyKeys   (optional)   | is-  |                               |
|   ConfigSource                | fies | layers: base, profile, .env,  |
|   LogfSetter     (optional)   | (no  |   env, added Layer(...)s      |
|   ConfigWatcher  (optional)   | im-  | YAML / TOML / JSON / Decoder  |
|   ConfigView     (alias)      | port)| polling watcher (debounced)   |
|   ValueFreeError (marker)     |      | ParseError, FileError,        |
|                               |      |   LayerError (value-free)     |
| construction                  |      |                               |
|   NewWithOpts(ctx, ...),      |      | non-test imports: stdlib,     |
|   CloneWithOpts(ctx, ...),    |      |   yaml/v3, go-toml/v2         |
|   Must variants (ctx)         |      | go.mod: require doger +       |
| binder + typed binding        |      |   replace => ../ (tests only) |
| validation (go-playground)    |      +-------------------------------+
| reload: committed view,       |
|   dispatcher, membership      |
+-------------------------------+

  ......> structural satisfaction only: config's non-test code never imports
          doger; doger never imports config, not even in tests.
```

## Decisions

### 1. Constructors take a ctx, return errors and never write the caller's options (D5, D37, D38, D28, D55, D65, D67)

`New` and `Clone` stay as they are: they cannot fail. `NewWithOpts(ctx, opts, packages...)` and `CloneWithOpts(ctx, opts)` take the caller's context as their first parameter and return `(*RootScope, error)`. The `Must` variants take the same ctx and panic with the error value itself (`panic(err)`), like `must1` in `utils.go`.

- **Why a ctx parameter, not a new name (D55).** The `context` package says the ctx is the first parameter and is never stored. A constructor that does I/O takes one, as `config.LoadDefaultConfig(ctx, ...)` (AWS SDK v2), `storage.NewClient(ctx, ...)` and `pgxpool.New(ctx, ...)` do. The Go blog's `XxxContext` duplicate exists to avoid a break, and these two signatures break anyway because they now return an error, so the parameter costs nothing extra. A nil ctx means `context.Background()`, as in every existing ctx-taking method. The ctx is used only during the call; nothing keeps it (D53).
- `New(packages...)` behaves as `NewWithOpts(context.Background(), nil, packages...)`; `Clone()` loads nothing (decision 1, Clone sharing).

The constructor works on `o := opts.copy()`, never on `opts`:

```
NewWithOpts(ctx, opts, packages...)                (ctx nil -> Background)
  |
  |-- 0. logfSet := opts != nil && opts.Logf != nil   (before the copy)
  |       clockSet := opts != nil && opts.Clock != nil &&
  |                   reflect.TypeOf(opts.Clock) != reflect.TypeOf(realClock)
  |       o := opts.copy(); o.initDefaults()  (the caller's struct is never written)
  |
  |-- 1. option checks, in order, before any source method is called -------+
  |       a. typed-nil ConfigSource (ptr, map, func, chan, slice kinds)     |
  |          -> ErrInvalidConfigSource                                      |
  |       b. EnvPropertySource with an invalid prefix in o.PropertySource   |
  |          -> ErrInvalidConfigSource                                      |
  |       c. both PropertySource and ConfigSource -> ErrConfigConflict      |
  |       d. ConfigTagKey outside ^[A-Za-z_][A-Za-z0-9_]*$ or "validate",   |
  |          typed-nil ConfigValidator -> ErrInvalidConfigOption (D67)      |
  |                                                                         |
  |-- 2. property source := ConfigSource (decided now, not at first read;  |
  |       never registered as a service)                                    |
  |-- 3. logfSet && LogfSetter? -> SetLogf(o.Logf)                          |
  |-- 4. lctx := loadContext(ctx, o.ConfigTimeout, clockSet, o.Clock)       |
  |      Load(lctx); recover panics ----------------------------------------+
  |      cancel lctx when the constructor returns                           | fails
  |                                                                         v
  |                                          return (nil, err)
  |                                          *ConfigLoadError{Source, Err}
  |                                          or an option-check sentinel;
  |                                          no package ran, nothing built,
  |                                          nothing logged
  |-- 5. log "DI: injector created" (only for a container that is returned)
  |-- 6. run packages in order (a package panic propagates, as today)
  |-- 7. ConfigWatcher? -> remember it; Start/Shutdown drive it (decision 9)
  v
return (root, nil)
```

- `copy()` covers every field, including the 5 new ones. A reflection test fails when a field is added without being copied. `logfSet` and `clockSet` are stored on the root, never in options, so clones and reused options never look "set by the user". The nil check comes before `copy()`.
- **Options copy is a behavior change (D65), flagged in the proposal and the upgrading note.** Edits the caller makes to `*InjectorOpts` after construction no longer reach the root, and `root.Add*Hook` no longer appends into the caller's struct. Scenarios compare `before := *opts` with `*opts` through `reflect.DeepEqual` after every constructor.
- **Clone sharing (D15, D54).**
  - `Clone()` always shares the parent's config state, whatever the kind of its source: no `Load`, no `SetLogf`. So `Clone()` still cannot fail.
  - `CloneWithOpts` shares only when `o.ConfigSource` is the parent's own source as the same pointer (`reflect.ValueOf(src).Kind() == reflect.Pointer && src == parentSrc`). Comparing only pointers means `==` can never panic; godoc notes that pointers to a zero-size type may compare equal.
  - The option checks (`ErrConfigConflict`, typed-nil, D67) run before any sharing.
  - A nil source gives a clone with no config. Any other source, including non-pointer kinds, is loaded for the clone.
- **Alternatives.** Recording errors on the root and returning them from `Start` was rejected (D5). Changing `New` too was rejected (D37): it cannot fail, and changing it would rewrite about 800 call sites. Writing defaults back into the caller's options was rejected: it made "Logf set by the user" undecidable. A new pair of context-suffixed constructors (D55 as first drafted in round 3) was rejected by the user: it adds names for a break that happens anyway. `InjectorOpts.ConfigContext` was rejected (it stores a ctx), and so were pre-loading with a `Loaded() bool` marker (a second loading path that still uses an implicit Background) and loading in `Start` (packages call `BindConfig` at construction).

### 2. The Load context: real clock vs a user clock (D52)

- **Real clock** (no `InjectorOpts.Clock`, or a caller-supplied `clockwork.NewRealClock()`): `context.WithTimeout(ctx, ConfigTimeout)`. The stdlib keeps the earlier of the caller's deadline and `now + ConfigTimeout`. `ConfigTimeout` 0 means `DefaultConfigTimeout`; a negative value means no timeout (ctx alone bounds `Load`). This path also covers `testing/synctest`, because the real clock is fake inside a bubble.
- **Detecting a user clock.** `clockSet` is recorded before defaults (decision 1, step 0): `opts.Clock != nil && reflect.TypeOf(opts.Clock) != reflect.TypeOf(clockwork.NewRealClock())`. Comparing with `==` is unsafe: the real clock has size zero, so whether two pointers to it are equal is unspecified (probe `audit-r4/clk`).
- **A user clock** (`clockSet`): a small shim context.
  - `Deadline()` returns the **parent's** deadline (`ok == false` when it has none). The stdlib and every network dialer treat a deadline as wall time, so a clock-time deadline would make a child `WithTimeout` never fire and make dials fail at once.
  - `Done()` closes when either the parent's `Done` or the clock timer fires; this is where the clock bound lives.
  - `Err()` is the parent's error if the parent ended first, otherwise `context.DeadlineExceeded`. It is stored before `Done` closes and never blocks.
  - It implements `AfterFunc(func()) (stop func() bool)`, so `context.WithCancel(shim)` starts no goroutine.
  - A child `context.WithTimeout(lctx, d)` inside `Load` fires on wall time, and a caller deadline ends `Load` even while the user clock never advances.
- The constructor cancels the Load context when it returns. A source that keeps it sees it done (`Canceled`, unless its deadline had already passed).
- Cancellation is cooperative. The constructor waits for `Load` to return. A `Load` that ignores its context and returns nil after the deadline succeeds; this is documented.
- **Goroutines.** The guarantee "no goroutine outlives the constructor" covers goroutines the library starts, for a parent context from the context package; a parent of another kind may cost one goroutine inside the stdlib's `AfterFunc` machinery (documented). Tests check it with goleak's default retries.
- **Alternatives.** clockwork's `WithTimeout` was rejected: on a fake clock it starts a goroutine, and its `Err` blocks. Returning `Clock.Now() + t` from `Deadline()` (round 2) was rejected (C1): it mixes time bases for every consumer of the deadline.

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

- An installed `ConfigSource` is never registered as a service, so the "service named `NameOf[PropertySource]`" step cannot see it. Every property read goes to the source's committed view (decision 9).

- `PropertySource` is unchanged. `PropertyKeys` is optional (`extension.go:86`): `Keys(prefix)` returns every key whose uniform form matches the prefix's uniform form segment by segment, spelled so that the same source's `Lookup` finds it. Results are sorted byte-wise; `Keys("")` on a source returns every key, and an empty result is a non-nil `[]string` (also for `MapSource(nil)`). `Keys("redis")` never returns `redis/x.*` keys: an instance is a different extension view.
- `EnvPropertySource` keeps returning `PropertySource`; godoc says the value implements `PropertyKeys`. `MapSource` is a defined map type: it is not copied, and godoc says not to mutate it while in use. `Properties.Keys(k)` returns keys relative to the view and drops keys its own grammar rejects; `Keys("")` returns every key of the view, and an invalid `k` returns `[]`. Calling `Lookup` or `Keys` directly with an invalid prefix is a miss and `[]`.
- **Two fields stay (D36).** `ConfigSource` says that the container loads and may watch the source; `PropertySource` stays a bare lookup.
- **Alternatives.** Adding `Keys` to `PropertySource` (the old D30) was rejected: it breaks every implementation. Returning keys in canonical spelling was rejected: a `MapSource` stored as `allowedhosts` could not find them.

### 4. Uniform form, the env mapping and the key grammar (D39)

- **Grammar:** a full key is the extension's name (optionally `/instance`), then dotted kebab segments, each optionally followed by `[i]` (decimal, no leading zero) or `[k]` suffixes. The name and the instance take no suffix. A bracket whose content starts with a digit is always an index, so `labels[5xx]` is not a key; such a map key is addressed as a plain segment, `labels.5xx`.
- **Uniform form**, per segment: lowercase, with `-` and `_` removed. Bracket contents are untouched, except that `[k]` with `k` in `[a-z0-9]+` equals `.k` and an all-digit `.n` equals `[n]`. An instance segment never folds into an index. Prefix matching is segment-aware.
- **Canonical spelling (D51).** The bracket form is canonical: `labels[team]` for a map entry, `hosts[0]` for a list item. It is the spelling the binder prefers when a source holds several spellings of one uniform key, and the spelling of `ConfigBindError.Key` and `ConfigViolation.Key`.

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
          lowercase, a digit segment right after the name or the
          instance) are skipped
```

- **Prefix rule (both modules):** the raw input must be ASCII `[A-Za-z0-9_.-]`, checked before uppercasing (`ſ`, U+017F, would otherwise uppercase to `S`); then `-` and `.` become `_`, the result is uppercased, and `_` is appended when missing. A prefix that fails the check:
  - `config.EnvPrefix`: `Load` fails with `*OptionError{Option: "EnvPrefix"}` matching `ErrInvalidOption`. `config.EnvPrefix("")` is also `ErrInvalidOption` (D66): an empty prefix would enumerate PATH, HOME and every secret. Unprefixed variables are read through `EnvMapper`, which looks keys up one by one, and the env layer is turned off by leaving `LayerEnv` out of `LayerOrder`;
  - `EnvPropertySource` installed via options: the constructor fails with `ErrInvalidConfigSource` (`DI: config: invalid env prefix "<prefix>"`; a prefix is not a secret);
  - elsewhere: every lookup misses, and the decision log line names the prefix.

  `EnvPropertySource` never panics. `EnvPropertySource("")` keeps its meaning (no prefix): it looks keys up one by one and enumerates only for `Keys`; its godoc states the difference from `config.EnvPrefix("")`.
- **One golden table** holds the forward and reverse mapping: `testdata/envmapping.txt` in the root and a copy in `config/testdata/envmapping.txt` (the config module cannot read outside itself when built with `GOWORK=off`). A root test asserts the two copies are byte-equal, so the duplicated mapping code (the config module may not import doger) cannot drift.
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
- unsupported kinds at any depth through pointers, slices, maps and nested structs (arrays, chan, func, complex, uintptr, `unsafe.Pointer`, interfaces such as `any`, whose type text is `interface {}`, and maps with non-string keys; `map[string]any` is rejected for its `any` values);
- invalid or all-digit tags;
- two fields with one uniform name (outer vs promoted, or sibling promoted);
- unexported embedded pointers;
- any type cycle among bound non-scalar types (recursion);
- malformed `validate` tags (D68): a zero value of every struct type reachable in the plan is validated once, so a malformed tag behind `*Inner` or behind `[]Inner` with `dive` is caught before any source is consulted.

All of these return `ErrInvalidConfigTarget`. Types converted as scalars, `cfg:"-"` fields and unexported fields are exempt from the recursion check.

**Bind walk.**

```
defaults d --(copy-on-write)--> result r   (paths the binder writes are copied;
      |                                      untouched pointers stay shared)
      v
for each planned field f (prefix + key(f)):
   source has PropertyKeys?
     yes: ks := Keys(fieldKey), once per field (D64-B1) -- every lookup uses
          the keys exactly as returned (several spellings of one uniform key:
          the canonical bracket form wins, else the byte-wise smallest);
          a scalar field with no returned key also tries Lookup(canonical
          key) (D61), so an EnvMapper value or any lookup-only key is found
     no : Lookup(canonical key); a list/map/pointer field -> ErrInvalidConfigTarget
   |
   +-- scalar (exact time.Duration, url.URL, *T implements TextUnmarshaler,
   |   []byte raw, then by kind: string as is, strconv base 10, Bits()
   |   respected); the raw value is never trimmed
   |      convert; failure -> *ConfigBindError{Key, Type, Reason, Err}
   |      (Err only for UnmarshalText errors; never the value)
   +-- list:    indices from ks, de-duplicated in uniform form; need 0..max,
   |            max <= 10000, checked before allocating; else one scalar is
   |            comma-split (elements trimmed, empties kept); a scalar in a
   |            higher layer masks lower indices; element structs start at zero
   +-- map:     entries merge by key over the default map; struct entries merge
   |            field by field; nil default map stays nil without entries
   +-- pointer: allocated only when ks is non-empty under it; a value
   |            exactly at the pointer's own key is ignored, as for maps
   +-- struct:  recurse (embedded exported structs promote; tagged embed nests)
user panics (UnmarshalText, Lookup, Keys, an EnvMapper called by Lookup)
  -> *ConfigBindError naming the key: Reason "panic of type T" for a non-error
     value; Reason "panic" and Err = the value for an error value
     (panic(nil) shows *runtime.PanicNilError); registration hooks re-panic
```

- **Error keys (D51).** `ConfigBindError.Key` uses the canonical bracket spelling of the element or entry (`server.hosts[1]`, `server.labels[team]`), never the spelling the source used.
- **`ConfigBindError` text (D58).** `DI: config: invalid value for <key> (<type>)`, then `: <Reason>` when set, then `: <Err text>` when `Err` reports value-free, else `: <Err's type>`. `UnmarshalText` errors often hold their input (`net.ParseError.Text`, netip), so their text is never trusted. An error panic value is therefore printed as its type: `...: panic: *errors.errorString`.
- **`EnvMapper` panics.** The mapper runs inside the config source's `Lookup` (decision 8), so the root recovers its panic while binding, like any `Lookup` panic.

- **Alternatives.** A default-tag mechanism was rejected (D6). A `SetDefaults()` method was deferred. mapstructure was rejected (decided item 9). A deep copy of the defaults was rejected: it changes the identity of non-config pointers such as `*http.Client`.

### 6. Typed binding and registration (D8a, D9, D10, D43, D46, D64, D66)

- `BindConfig(i, prefix, defaults T) (ConfigValue[T], error)`, `BindConfigNamed(i, name, prefix, defaults T) (ConfigValue[T], error)` and `ResolveConfig(i, prefix, defaults T) (T, error)`. `T` is always inferred. On error the handle is the zero `ConfigValue[T]` (D66). A nil injector means `DefaultRootScope`; a typed-nil injector is `ErrInvalidConfigTarget`. `T` must be a struct type, otherwise `ErrInvalidConfigTarget`. The prefix is `""` or a valid key (`server`, `redis/queue`, `server.hosts[0]`); its first segment takes no suffix, so `hosts[0]` is invalid (D64-B2), as in extension-properties.
- **The bind protocol (D46, with the r5 overrides).** User code never runs under the commit semaphore, and that includes `Logf`.
  1. Pre-check the registration conflict (a non-default or resolved default registration under either name): `ErrConfigAlreadyBound` before any property read.
  2. Read the committed view V.
  3. Bind and validate against V, with no lock held (user code runs here).
  4. Take the commit semaphore. `BindConfig` has no ctx, so it waits.
  5. If the committed view is still V: add the entry to the entry set (CAS), its cell holding the value bound in step 3; release.
  6. Otherwise release, set V to the current view and go back to step 3. Each retry follows a committed reload, so the loop ends unless reloads never stop.
  7. With no lock held, register `T`, then `ConfigValue[T]` (`ConfigValueName(name)` for the named form). Registration hooks are user code.
  8. Re-check the container phase. If it has begun shutting down, roll back and return `ErrStartAfterShutdown`.
  - **Rollback** (a failed or panicking registration, or step 8): remove each wrapper we registered only if the slot still holds it (`removeService(name, match)`, no hooks, as the extension rollback does at `extension.go:737-745`); re-store a fresh `clone(scope)` of a replaced unresolved `AsDefault` registration through `registerLocked`, with no hooks (`supersede`/`forget` are one-way, so the same object cannot come back); CAS-remove the entry; then return the error or re-panic.
  - In P1 (task group 11) there is no reload, so steps 2–6 collapse to one bind and the entry set is not needed; group 20 adds steps 4–6, driven by the racing tests.

How a bind and a reload interleave (no update is lost, no lock is held around user code):

```
 BindConfig goroutine                          watcher goroutine (reload)
 --------------------                          --------------------------
 pre-check registrations
 V := committed view
 bind + validate against V (no lock)           S := entries.Load()
        |                                      rebind S from S' (no lock)
        |                                      take semaphore; entries == S
        |                                      view.Store(S'); publish; release
 take commit semaphore
 view still V?  no (S' committed) --> release; V := S'; bind + validate again
 take semaphore; view still V? yes
 CAS-add entry e (cell = value bound from S')
 release semaphore
 register T, then ConfigValue[T] (no lock; hooks run here)
 phase re-check; failure? -> roll back (slots we still hold, AsDefault
                             restored by a fresh clone, e removed)
 result: e's handle holds S', never V after S' committed
```

- **Duplicates.** A non-default registration under the name gives `ErrConfigAlreadyBound` and never panics. An unresolved `AsDefault` registration is replaced; a resolved one gives `ErrConfigAlreadyBound` (`default-registrations`). A child scope may shadow a parent.
- **Phases (D64-B3).** `BindConfig` and `BindConfigNamed` are refused with `ErrStartAfterShutdown` only on the container's phase, once it has begun shutting down, also when called on a child scope of that container (a child has no phase of its own). A bind racing `Shutdown` either succeeds, and its entry is released at that Shutdown, or returns `ErrStartAfterShutdown` with both names unregistered. `ResolveConfig` and `BindProperties` work in every phase: they read the last committed view once per call.
- **`ConfigValue[T]`** is `struct{ c *cell[T] }`. Copies share state, and the zero value works (`Get` returns zero `T`; `OnChange` returns a no-op stop). `Get` is one atomic load. Its result is read-only by documented contract, because maps and slices are shared with the registered `T`.
- **`BindProperties(p, target)`** binds through the view's underlying source with the same algorithm and rules as `BindConfig` (keys, tags, kinds, the D61 fallback) but never validates. A `Properties` value stays readable after the `Extend` call that produced it returns. It keeps its target-pointer shape, following the Extension SPI.
- **Alternatives.** A `ConfigBinding` package was dropped (D8a). `BindOption`/`ConfigDefaults` were replaced by the `defaults` argument (D43). Registering under the commit lock was rejected: registration hooks would run under it. The round-2 "pending entry added before registration" was rejected (X1): a reload that commits between the read of V and the add is lost (probe `audit-r3/casmodel`).

### 7. Validation and value-free text (D7, D34, D44, D56, D57, D64-B4, D67, D68)

```
which validator?                              validating one value v (a copy)
----------------                              -------------------------------
InjectorOpts.ConfigValidator != nil? -yes-> it    tags (or the replacement) on &copy
  (typed-nil -> ErrInvalidConfigOption at           | fails           | passes
   construction, D67)                               v                 v
        | no                               *ConfigValidationError   top-level T has
        v                                  {Section, Type,          Validate() error
go-playground (sync.OnceValue, the root     Violations sorted by    (value or pointer
default; tag names come from the field-     key: key, rule, param;  receiver, promoted
path -> key map, never its namespaces)      Err nil}                unambiguously)?
                                            matches ErrInvalidConfig call it on a copy;
                                                                      error -> Err set,
                                                                      Violations nil
error text: DI: config: invalid "<prefix or name>" (<T>): <key> (<rule>), ...
            DI: config: invalid "<prefix or name>" (<T>): <ctx text | value-free text | type of Err>
            (ValidateConfig has no section: DI: config: invalid (<T>): ...)
```

- **One error type (D56).** `ConfigValidationError{Section string; Type reflect.Type; Violations []ConfigViolation; Err error}` with `ConfigViolation{Key, Rule, Param string}` (`Param` from the tag, such as `65535`). Tag failures set `Violations` and leave `Err` nil; a `Validate()` or replacement-validator error sets `Err` and leaves `Violations` nil. `Unwrap()` returns `Err`; `Is(ErrInvalidConfig)` holds. `ValidateConfig` leaves `Section` empty and names `reflect.TypeOf(v).String()` as given.
- **The value-free marker (D57).** `type ValueFreeError interface{ error; ValueFree() bool }`, a predicate like `net.Error.Timeout() bool`, so a wrapper can answer per instance. It is found by walking the chain: a context error's text wins, then the first error whose `ValueFree()` reports true; a marker reporting false is skipped and the walk continues to its cause; otherwise only the type is shown. Only the root config sentinels, `*ConfigBindError`, `*ConfigValidationError` and `*ConfigLoadError` implement it. Their methods use pointer receivers and are nil-safe: a nil `*ConfigLoadError` answers `ValueFree()` with true and does not panic. Value receivers were rejected: `errors.As(err, &ConfigBindError{})` would compile and never match. `StartError`, `ExtensionError` and `ShutdownReport` keep their texts and do not implement the marker. A cause is always reachable through `errors.Is` and `errors.As`.
- **`Validate()` runs on a copy (D64-B4).** Changes to top-level fields are discarded. Maps, slices and pointees are shared, and godoc documents them as read-only for validators. Two promoted `Validate` methods at one depth are ambiguous by the Go method-set rule, so neither runs. Field types' `Validate` methods are not called.
- Fields skipped by `cfg:"-"`, and unexported fields, are not validated by the default validator (via `StructFiltered`). A replacement validator receives a pointer to a copy; its changes never reach the bound value. A malformed `validate` tag on any reachable struct type is `ErrInvalidConfigTarget` with a fixed message, found at plan time (decision 5, D68). A validator panic is `ErrInvalidConfig` (`validator panic of type T`). `BindProperties` never validates; `ValidateConfig` is its explicit counterpart.
- **Cost (D45, honest):** go-playground adds 7 modules (mimetype, locales, universal-translator, go-urn, x/crypto, x/sys, x/text) to every consumer's build, `go.sum` and binary, plus go-playground/assert (its tests only) to the module graph. Only building the validator instance is lazy.
- **Alternatives.** A go-playground adapter module was rejected: D7 says tags are always checked by default. A source-level validator was dropped (D34): a Go method set cannot depend on an option. Two error types, one per failure kind, were rejected (E2). A marker method with no result (round 2) was rejected: a wrapper could not decline per instance.

### 8. The config module (D50, D59, D60, D62, D66)

`config.New(opts...) *Source` (D66: one primary type, as `slog.New`). Options are checked at `Load`, not when `New` is called, and every invalid option is an `*OptionError{Option, Reason}` matching `ErrInvalidOption`. Options that need a value keep `...T` (`Dir`, `File`, `LayerOrder`), following `filepath.Join`; calling one with no argument is `ErrInvalidOption`, so a caller holding a slice still writes `Dir(dirs...)`. Paths are resolved to absolute at the first `Load`. Only the container calls `Load` on an installed source; a direct call is unsupported (godoc).

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
        | process env: APP_*         |  9292         captured once, at Load
        +----------------------------+
highest | config.Layer(name, src)    |  (vault)      LoadLayer at Load; at polls when
        +----------------------------+               LayerRefresh is due (decision 10)
config.LayerOrder(LayerBase, ..., "vault") reorders; a built-in layer left out is skipped,
an added layer left out is ErrInvalidOption.

Load(ctx) (serialized: a cap-1 channel acquired with select on ctx):
          read layers -> flatten -> merge in uniform form -> snapshot
          {sorted keys, map} --atomic.Pointer.Store--> Lookup / Keys (lock-free; one call
          sees one snapshot). A cancelled or failed Load keeps the previous snapshot.
```

- **Discovery.** Search `.` then `./config` for `config.{yaml,yml,toml,json}` and every extension added with `Decoder`, lowercase file names only:
  - two formats with one base name fail with `*AmbiguousFormatError{Files}` matching `ErrAmbiguousFormat`;
  - no file is fine, and a `./config` that is a regular file counts as absent;
  - a set profile with no file fails with `*ProfileError{Profile}` matching `ErrProfileNotFound`.

  `File(...)` turns off discovery and the profile overlay. `Dir` together with `File`, and `Profile` or `ProfileEnv` together with `File`, are `ErrInvalidOption`. `Format` with `File` forces the format. `Format` names a built-in format or a registered extension; a `Format` naming no decoder is an `*OptionError` (an option problem, with no file to name), while `ErrUnknownFormat` is kept for a file whose extension has no decoder, inside `*FileError{File, Err: ErrUnknownFormat}`. The `ProfileEnv` variable is read only from the process, never from `.env`, and never becomes a key. Profile names must match `^[a-z0-9][a-z0-9_-]*$`, so no path escapes. Symlinks are followed (no `os.Root`). `FileError.File` keeps the path as given.
- **Opening a file (D62).** Unix: `os.OpenFile(p, O_RDONLY|syscall.O_NONBLOCK, 0)`, then `f.Stat()`, which must report `Mode().IsRegular()`, else `ErrNotRegularFile`; Windows: a plain `Open` and `f.Stat()`. The check runs on the opened descriptor, never on the path, so a path swapped for a FIFO cannot hang `Load` (probe `audit-r4/fifo`). Reads go through `io.LimitReader(MaxFileSize+1)`; more is `ErrFileTooLarge`. `EACCES` is `<file>: permission denied` and matches `fs.ErrPermission`.
- **File keys:**
  - A whole key written as `"[...]"`, holding one or more printable ASCII characters other than `[`, `]` and whitespace, is kept verbatim as a map key.
  - Otherwise keys are split on `.`, with `/` allowed only in the first segment, and each segment, map keys included, is normalized with the field-naming rule (`userId` becomes `user-id`; bracket it to keep it verbatim). The result must match `[a-z0-9]+(-[a-z0-9]+)*`, so `a@b`, a trailing `-`, `--`, a segment of only `_`, a TOML quoted key holding `.` and a non-scalar YAML key are "invalid key".
  - An all-digit key becomes an index. Sequences become `[i]`, nested `[i][j]`; `null` means absent. Duplicates are checked in uniform form.
- **Hostile files are bounded linearly (D50).** Caps are enforced before the content is converted to values:
  - 64 segments per key ("too deep"; fixed, because it bounds key length and recursion; each `[i]`/`[k]` counts as a segment);
  - `DefaultMaxKeys = 10_000` keys per file ("too many keys", line 0), overridable with `config.MaxKeys(n)` (0 means the default, negative is `ErrInvalidOption`);
  - `DefaultMaxFileSize = 1 << 20` (1 MiB). The `yaml.Node` tree is fully built before any cap applies; a file four times that size costs about 170-240 MiB, 1 MiB about 60 MiB (probe `audit-r4/yamlmem`). The godoc states this memory cost.
  - **YAML** is parsed into a `yaml.Node` and walked once, never decoded into `any` first. The walk keeps a node budget that counts every node an alias or a `<<` merge expands ("excessive aliasing"), detects alias cycles, uses a hash set for duplicate keys, and applies both caps.
  - **TOML** gets a linear pre-pass with `go-toml/v2/unstable.Parser` that counts keys and dotted depth, inline tables and array values included, before `Unmarshal`.
  - **JSON** streams with `json.Decoder.Token`, counting as it goes, and reports "too deep" at depth 65.
  - **Nesting beyond a parser's own limit** is "syntax error". go-toml v2.4.3 and yaml v3 both stop at 10,000 levels with an error (probe `limits` in this round: `a = ` plus 500,000 `[` gives "arrays and inline tables are nested more than the maximum of 10000 levels deep"; 20,000 nested YAML flow sequences give "exceeded max depth of 10000"). Under that limit TOML and YAML report "too deep" from the pre-pass or the walk. go-toml before v2.4.3 has no limit and dies on a stack overflow (probe `audit-r4/tomldeep`), hence the floor.
- **Rendering.**
  - YAML keeps its literal text (`True`, `yes`, `1.0`, `0x1F`). A trailing `---` is "the last document": `a: 1\n---\n---\n` is "multiple documents".
  - JSON uses `UseNumber` literals; trailing data is a syntax error; an empty file or top-level `null` loads as no keys.
  - TOML uses go-toml/v2 (v2.4.3 or later): ints decimal, floats `strconv.FormatFloat('g', -1)`, offset datetimes `time.RFC3339Nano`, local date/time types as TOML text.
  - A `DecodeFunc` result (`type DecodeFunc func(data []byte) (map[string]any, error)`, added with `config.Decoder(ext, fn)`, `ext` case-insensitive with an optional leading dot, `""`/`"."` invalid) is rendered by Go type: `map[string]any`, `map[string]string`, `[]any`, `[]string`, `[]map[string]any`, every int/uint/float width (floats `strconv.FormatFloat(f, 'g', -1, bits)`), `bool`, `string`, `time.Time` (RFC3339Nano) and `json.Number`. `map[any]any` is "not a mapping"; any other type is "unsupported value". A `DecodeFunc` may return its own `*ParseError`, which keeps its line.
- **Env layer.** The process environment is captured once at `Load`. With `EnvMapper(fn)`, `fn(key)` is called by `Lookup` and the name is looked up in that capture, then in `.env`; a mapped layer contributes nothing to `Keys`, and scalar fields reach it through the binder's direct lookup (D61). A mapper panic therefore surfaces in the root's binder (decision 5).
- **`.env` grammar:**
  - `KEY=VALUE`, with an optional `export `; spaces around `=` are ignored; keys match `[A-Za-z_][A-Za-z0-9_]*`.
  - `#` starts a full-line comment, and ` #` an inline comment on unquoted values; unquoted values are trimmed.
  - Single quotes are literal. Double quotes support `\n`, `\t`, `\"` and `\\`, and keep other pairs as written.
  - No `${}` expansion and no multi-line values; CRLF and a BOM are accepted; a later line wins.
  - An invalid line is a `*ParseError` "invalid line".
  - `.env` keys go through the same prefix and mapping as process env; with `EnvMapper`, the process is consulted first, then `.env`.
- **`DotEnvExport()`** captures the process environment, then sets only unset variables (prefixed or not), once, at the first successful `Load`, and logs the names it skipped. A later explicit `Load` sees the exported variables as process env. The godoc warns about `os.Setenv` with cgo.
- **Added layers (D60).** `type LayerSource interface{ LoadLayer(ctx context.Context) (map[string]string, error) }`, so one type can be both a `doger.ConfigSource` and a layer. A layer's `*ParseError` has `File ""` and prints `layer "<name>": invalid key`.
- **Errors (D59).**
  - Sentinels are `type sentinelError string`; every config error type has a nil-safe pointer-receiver `ValueFree() bool`.
  - `*FileError{File, Err}` prints `<file>: does not exist | file too large | not a regular file | permission denied | unknown format`.
  - `*ParseError{File, Line, Category}` prints `<file>:<line>: <category>`, or `<file>: <category>` when the line is unknown. `ParseCategory` constants are typed and named with the type (`ParseCategorySyntax`, ..., `ParseCategoryPanic`), following the repo's `StartOpBuild` convention.
  - `*LayerError{Layer, Err}` prints `layer "<name>": <value-free text | type>`.
  - `*OptionError{Option, Reason}`, `*ProfileError{Profile}` and `*AmbiguousFormatError{Files}` carry value-free detail; `Is` matches `ErrInvalidOption`, `ErrProfileNotFound` and `ErrAmbiguousFormat`.
  - When the ctx is done, `Load` returns `ctx.Err()` itself, unwrapped, even if a layer returned it.
  - None of them carries a `config:` prefix, and none wraps parser text.
- **Panics.** A panic in `LoadLayer` or a `DecodeFunc` is recovered: at `Load` it is a `*LayerError` or a `*ParseError` with `ParseCategoryPanic`, naming only the panic value's type, and the previous snapshot is kept; during a poll it is a staging error (decision 10).
- **Logging:** an explicit `config.Logger(l)` wins over `SetLogf`, which wins over `slog.Default()`, looked up at each call.
- **Alternatives.** viper, fsnotify and godotenv were rejected (spikes 1–5). `os.Root` was dropped: it rejects absolute in-directory symlinks (Docker secrets), and profile validation already prevents traversal. Decoding YAML into `any` and calling go-toml `Unmarshal` directly were rejected (X4): both are quadratic in the number of keys and cannot be cancelled (probes `audit-r3/p1`, `p2`). A decoder interface registered per extension was replaced by a `DecodeFunc` (D66). An `os.Stat` before `Open` was rejected (F1). `EnvPrefix("")` meaning "no prefix" (r4) was rejected in r5.

### 9. Reload: the root owns the commit (D14, D33, D47, D53, D54, D63)

```
 watcher goroutine (source.Watch)          root config state
 --------------------------------          -----------------
 poll; stable fingerprint differs
 from the last *staged* one?
   | yes: parse + merge -> S'
   | (or err if staging failed)
 changed(ctx, S', err) ------------------> acquire the reload slot (cap-1 chan,
 (never overlapping; the root also          select on the watch ctx): calls are
  serializes them)                          serialized (D63)
                                           err != nil, S' nil or typed-nil, or
                                             shutting down? -> false (+ warning)
                                           S := entries.Load()      (CAS set, immutable)
                                           rebind EVERY entry of S from S'
                                             with NO lock held          (user code)
                                           all ok? no -> enqueue warning + hook event
                                                          per owner; return false
                                      +--> take commit semaphore (chan, select ctx.Done)
                                      |    shutting down? -> release; return false
                                      |    cur := entries.Load()
                                      |    cur is a subset of S (removals only)?
                                      |      yes -> view.Store(S')       (committed view)
                                      |             publish each ConfigValue cell
                                      |             enqueue notifications (non-blocking:
                                      |              coalescing map + cap-1 chan)
                                      |             release; return true
                                      |      no  -> release; rebind only cur \ S
                                      |             (an added entry fails -> abort,
                                      |              false + warning); S = cur
                                      +-------------+
 true:  adopt S' as the current state                          |
 false: drop S'                                                v
                                           dispatcher goroutine: for each section whose
                                           raw key/value set changed, call subscribers
                                           outside every lock; hooks are called as
                                           fn(root, err) with no ctx; panics go to
                                           HookContainerPanic("config-on-change" |
                                           "config-reload-failed"), else are logged by type
```

- **Every read path** (property reads, `ResolveConfig`, `BindProperties`, a `Properties` value obtained from a view) reads one committed view per call or value, through `atomic.Pointer[viewBox]`. A box is used because the dynamic type of the view changes, and two atomics cannot be compared as one snapshot. No reader mixes snapshots.
- **No lock around user code.** `Validate`, `UnmarshalText`, the source's `Lookup`/`Keys` on the staged view, registration hooks and `Logf` never run under the commit semaphore. The semaphore is a `chan struct{}` of capacity 1, so synctest sees it.
- **The retry loop ends (R1).** `S` is reassigned after each rebind of the added entries, and a retry happens only when entries were added, so it ends unless binds never stop. Removals alone never force a retry: a removed entry's rebind result is simply not published.
- **Participation and ownership (D47, r5).**
  - Every entry in the set is rebound at every reload and can veto it, whether or not an owner started. A clone that starts late therefore sees values that are already current, and nothing has to happen at `Start`.
  - The owners of an entry are the registered `ConfigValue[T]` wrappers. One owner is added at registration and one in `Scope.clone` (`scope.go:794`) for each clone that copies the registration. An owner is released in `RootScope.forget` (`root_refresh.go:238`), which `Override`, `removeService` and `serviceShutdown` all reach, so a `Shutdown` releases ownership even for a container that never started (its services are still shut down). `runtime.AddCleanup` on the wrapper's scope is the fallback for a clone dropped without `Shutdown` and for a scope detached without shutdown (`root_scope.go:803`).
  - Owners are mutable per-entry state kept outside the CAS set; the set pointer changes only when an entry is added or removed. An entry leaves the set when it has no owners left.
  - **Caveat 1:** an `OnChange` subscriber that captures a clone keeps that clone, and its entries, alive.
  - **Caveat 2:** a clone dropped without `Shutdown` keeps vetoing reloads until it is garbage-collected, which is not deterministic. Both caveats are documented on `Clone` and `OnChange`.
  - Cost per reload is O(entries), not O(clones), because clones share entries.
- **Membership** (the set of containers whose `Start` succeeded and that have not shut down) drives only the lifecycle of the watcher and the dispatcher. A Shutdown without a Start, and a double Shutdown, are no-ops for membership.

```
              root.Start()          clone.Start()        clone.Shutdown()      root.Shutdown()
members: {} ------------> {root} ------------> {root,clone} ------> {root} ------------> {}
           launch watcher +                                                 cancel watch; stop
           dispatcher (ctx from                                             dispatcher; wait for
           context.Background, like                                         both until the
           the health scheduler)                                            Shutdown ctx ends
{} after all stopped, then clone.Start(): a launcher goroutine waits for the previous
Watch's done channel with select on the NEW watch ctx, then calls Watch; Start never
blocks on it, and a Watch that ignores its ctx cannot leak past the next Shutdown.
config.New rejects a concurrent second Watch with ErrWatching.
```

- **Clones (D54).** `Clone()` always shares the parent's committed view, watcher and dispatcher, whatever the kind of its source; `CloneWithOpts` shares only for the parent's own source as the same pointer. A `Clone()` racing a commit is covered by a `-race` scenario: the clone's owners are added under the entry's owner lock, so the commit either publishes into the clone's wrappers or the clone copies the already published cell.
- **Shutdown order:** cancel the watch, stop accepting changes (`changed` returns false and enqueues nothing, including a reload already in flight, which re-checks under the semaphore), shut down services as today, then wait for the watcher and dispatcher until the Shutdown ctx ends. The dispatcher checks its done channel first, and pending notifications are dropped at stop.
- **D42:** a subscriber or hook must call `go root.Shutdown()`. A synchronous call waits on itself: services are not skipped, but the self-wait uses up the Shutdown deadline and fails the report with `ctx.Err()`. Godoc documents this (as for Starters), and there is no scenario for the synchronous case. Groups 19 and 21 own these docs.
- **A watch that ends:** with nil or `ctx.Err()` after its ctx ended, it is a normal stop. A nil return before that means "not watching" (`DisableWatch`). Any other error or panic is logged by type as `ErrConfigWatchFailed` and sent to the hooks; a panic goes to `HookContainerPanic("config-watch")` when that hook is set.
- **Hooks (D53).** `HookConfigReloadFailed []func(root *RootScope, err error)` and `AddConfigReloadFailedHook(fn func(root *RootScope, err error))`, following the async health hooks (`HookContainerHealthChanged`). No ctx is stored or passed; `OnChange` subscribers take none either.
  - A failing section fires the hooks and `Logf` of each owner container that has not shut down, deduplicated; if none is left, the original root's.
  - A staging failure fires each started member's hooks.
  - Coalescing is per hook owner: each owner keeps only its latest event.
  - Several failing sections warn once, one line per section.
- **Alternatives.** The source owning the swap (`changed(next) bool` without a root view) was rejected: the root could not publish after the commit. A mutex was rejected: it is held across user code and synctest cannot detect it. A generation counter was rejected: it can be read torn, and a cap on retries leaves exhaustion undefined. Live-member gating with `weak.Pointer[RootScope]` owners pruned at commit (round 2) was rejected (R2, R3): a section owned by a clone that had not started went stale, and per-request clones leaked owners. Passing hooks `context.WithoutCancel(startCtx)` was rejected (C2): it stores a context.

### 10. Polling (config.New(...).Watch) (D60)

```
every PollInterval (default 10s; 0 = default; negative = ErrInvalidOption at Load),
on a time.NewTicker stopped on exit, select on ctx:
  read each watched file with Load's reader (non-blocking open, regular files only,
        size-limited); a missing file, or an empty one that was non-empty at Load
        -> staging error
  watched set = files Load read + the .env file if it existed at Load
                (DotEnv(path) path; none with DotEnv(""))
  read each added layer whose LayerRefresh is due, or every layer when a watched
        file changed (under StageTimeout; a panic is a staging error)
  fp := per file (path, len, sha256) + a hash of each layer's sorted last read
        (the layer content is never logged)
  fp == the previous poll's fp?          (debounce over two polls: rides out
     no  -> wait                           partial writes; covers changes and failures)
     yes -> fp != lastStaged?
              no  -> wait (a rejected content warns once)
              yes -> stage; lastStaged = fp, except when the rejection came from
                     a layer error (then retried when the layer is next due;
                     identical layer-error types warn once until it succeeds)
Kubernetes ConfigMap: config.yaml -> ..data/config.yaml, ..data -> ..2026_10_30_v2/
                      the swap changes the bytes read -> new fp
new files appearing after Load are not noticed (documented)
```

- **Layer refresh.** `config.LayerRefresh(name LayerName, d time.Duration) Option`; default `DefaultLayerRefresh = time.Minute`; 0 means the default; a negative value reads the layer only at `Load` and when a watched file changes. A `LayerRefresh` naming no added layer is `ErrInvalidOption`.
- **A layer that never settles** (3 consecutive reads that all differ) warns once, `layer "<name>" never settles; its changes are not staged`; its read is still used whenever a file change stages.
- **Panics during a poll** in `LoadLayer` or a `DecodeFunc` are staging errors naming only the panic type. An `EnvMapper` runs in the staged view's `Lookup`, so its panic surfaces in the root's rebind and fails the reload the same way.

### 11. Errors (D56–D59, D67)

| Sentinel | Means |
|---|---|
| `ErrConfigLoadFailed` | `Load` failed or panicked (`*ConfigLoadError{Source reflect.Type; Err error}`) |
| `ErrConfigWatchFailed` | `Watch` ended abnormally |
| `ErrConfigConversion` | a value could not be converted (`*ConfigBindError{Key string; Type reflect.Type; Reason string; Err error}`) |
| `ErrInvalidConfig` | validation failed (`*ConfigValidationError{Section string; Type reflect.Type; Violations []ConfigViolation; Err error}`, `ConfigViolation{Key, Rule, Param string}`) |
| `ErrInvalidConfigTarget` | bad `T`, prefix, tag, kind, recursion, a name conflict, a malformed `validate` tag, or a list/map/pointer field on a Lookup-only source |
| `ErrInvalidConfigSource` | typed-nil source, an invalid env prefix in options, or a nil staged view |
| `ErrInvalidConfigOption` | an invalid `ConfigTagKey` or a typed-nil `ConfigValidator` (D67) |
| `ErrConfigConflict` | both `PropertySource` and `ConfigSource` set |
| `ErrConfigAlreadyBound` | a non-default (or resolved default) registration exists |

- Texts start with `DI: config: `. Types use `reflect.Type.String()` (`app.ServerConfig` in specs). Services use `NameOf`.
- Each error type implements `Is` for its sentinel, and `GoString()` with type-only output, so `%#v` never prints a value; `%v`, `%+v`, `%s` and `%q` already use `Error()`. All methods use pointer receivers and are nil-safe (D57).
- Load text is `DI: config: load failed: source <T>: <context text | value-free text | cause type | "panic of type T">`. A panic value that is an error is kept as the cause and shown by the same rule (its value-free text or its type); `panic(nil)` shows `*runtime.PanicNilError`. A panic value that implements the marker and reports true has its text shown.
- Problems with the source itself stay `ErrInvalidConfigSource`; problems with the root's own new options are `ErrInvalidConfigOption`.

### 12. Module, CI and release (D48, D49)

```
repo/
  go.mod                (root: + validator/v10; no tool directive)
  go.work               (+ ./config; tools/ is NOT a workspace module)
  tools/go.mod          (tool golang.org/x/vuln/cmd/govulncheck; go 1.27;
                         never required by any module)
  config/go.mod         skeleton (group 1): go 1.27, goleak, no doger require
                        group 18 adds:
                          require github.com/kartaladev/doger v0.0.0-00010101000000-000000000000
                          replace github.com/kartaladev/doger => ../
                        require go.yaml.in/yaml/v3, github.com/pelletier/go-toml/v2 >= v2.4.3
CI (test.yml / lint.yml / security.yml):
  root:   make test, make lint
  config: (cd config && go test -race ./... && golangci-lint run ./...)
  GOWORK=off job: each published module builds/tests with its replace and runs
                  go mod tidy -diff (go.mod/go.sum complete)
  footprint: (cd config && go list -deps -f '{{with .Module}}{{.Path}}{{end}}' . \
               | sort -u | grep -vx github.com/kartaladev/doger/config)
             lists only go.yaml.in/yaml/v3 and github.com/pelletier/go-toml/v2
  govulncheck (D48): build once, outside the workspace, then scan each module
             the way a consumer builds it:
               GOWORK=off go -C tools build -o "$RUNNER_TEMP/govulncheck" \
                 golang.org/x/vuln/cmd/govulncheck
               (or: GOWORK=off go -C tools tool -n govulncheck to get its path)
               for m in . config http/* scheduler/*; do
                 (cd "$m" && GOWORK=off "$RUNNER_TEMP/govulncheck" ./...); done
  dependabot: + a /tools entry;  trivy: skip-dirs tools;
  trivy: .trivyignore entries per CVE id, each with exp: date and a reason
release.yml (existing order kept):
  test -> drop tests (keep *_example_test.go) -> go mod tidy (root)
       -> (cd config && go mod tidy)
       -> pin config and each adapter (http/*, scheduler/*) only if still required
          (D49; jq exits non-zero when absent, so no bare grep under set -e):
          if go -C config mod edit -json \
               | jq -e '.Require[]? | select(.Path=="github.com/kartaladev/doger")' >/dev/null
          then go -C config mod edit -require=github.com/kartaladev/doger@"$SEMVER"; fi
       -> tag <semver>, http/<x>/<semver>, scheduler/<x>/<semver>, config/<semver>
```

- Running govulncheck through `go tool` with a `-modfile` flag was rejected (X2): it fails with "-modfile cannot be used in workspace mode" (probe `audit-r3/modfile`). A bare `grep` for the pin was rejected (X3): it also matches the `replace` line, runs against the root `go.mod`, and exits 1 under `-e` (probe `audit-r3/release`).
- No version is tagged between this change and `unify-constructor`, so the constructor break ships once.

### 13. Round-3 decisions (D46–D68)

The third audit round (6 blockers, 26 majors) produced D46–D68; r5 overrides are folded in. Each is applied in the decision named.

| ID | Fixes | Decision | Where |
|---|---|---|---|
| D46 | X1, B5 | `BindConfig` commits only if the view it bound against is still committed, else rebinds; registers with no lock; re-checks the phase; rollback restores what it replaced | 6 |
| D47 | R1–R3 | Every entry is rebound and can veto; owners are the registered wrappers, released in `forget`, `AddCleanup` as fallback; commit when the set shrank or stayed, retry on additions; two caveats documented | 9 |
| D48 | X2 | govulncheck built from `tools/` with `GOWORK=off` and run per module | 12 |
| D49 | X3 | Release pin via `go -C config mod edit -json \| jq -e` | 12 |
| D50 | X4 | Linear bounds: YAML node walk, TOML pre-pass, JSON streaming; `DefaultMaxKeys` 10,000; `DefaultMaxFileSize` 1 MiB; go-toml >= v2.4.3 | 8 |
| D51 | X5 | The bracket form is canonical | 4 |
| D52 | C1 | A user clock's context returns the parent's deadline; `clockSet` via `reflect.TypeOf` | 2 |
| D53 | C2 | `HookConfigReloadFailed []func(root *RootScope, err error)`, no ctx | 9 |
| D54 | C3 | `Clone()` always shares config state | 1, 9 |
| D55 | E1 | ctx is the first parameter of `NewWithOpts`, `CloneWithOpts` and the `Must` forms; nil means Background | 1 |
| D56 | E2 | One `ConfigValidationError{Section, Type, Violations, Err}` | 7 |
| D57 | E-m3, E-m5 | `ValueFree() bool`, pointer receivers, nil-safe, config errors only | 7 |
| D58 | E-m4, B-m2 | `ConfigBindError{Key, Type, Reason, Err}`; `Err` text only when value-free | 5 |
| D59 | F2 | `*OptionError`, `*ProfileError`, `*AmbiguousFormatError`, `ErrUnknownFormat`; typed `ParseCategory*` | 8 |
| D60 | F4, R5 | `LayerSource.LoadLayer`; `LayerRefresh` (default 1 minute) | 8, 10 |
| D61 | F3 | Scalar fields with no listed key fall back to `Lookup(canonical)` | 5 |
| D62 | F1 | Non-blocking open, `f.Stat()` on the descriptor | 8 |
| D63 | R4 | `changed` calls serialized; a nil view is `ErrInvalidConfigSource` | 9 |
| D64 | B1–B4 | `Keys` per field; prefix head takes no suffix; phase-only refusal; `Validate()` on a copy | 5, 6, 7 |
| D65 | T5 | Options copy flagged, DeepEqual rows on every constructor | 1 |
| D66 | n1, E-m6/7/9/10 | `LogfSetter`, `config.New`, `DecodeFunc`/`config.Decoder`, `BindConfig` returns its handle, `ConfigView` alias, `EnvPrefix("")` invalid, empty `Dir`/`File`/`LayerOrder` invalid | 1, 4, 6, 8 |
| D67 | B-m11 | `ErrInvalidConfigOption` for `ConfigTagKey` and typed-nil `ConfigValidator` | 1, 11 |
| D68 | F-m12 | Malformed validate tags caught at plan time on every reachable struct type | 5, 7 |

Judgment calls confirmed in this round (the forks' choices, now part of the design):
- `Format` naming no decoder is an `*OptionError` (matches `ErrInvalidOption`); `ErrUnknownFormat` is only for a file whose extension has no decoder. This narrows r4's D59.
- `EnvMapper` panics are recovered by the root while binding, because the mapper runs inside `Lookup`, not inside `Load`.
- A nil `*ConfigLoadError` reports true from its `ValueFree` method and does not panic.
- `labels[5xx]` is invalid (a bracket starting with a digit is an index); the entry is addressed as `labels.5xx`.
- `ConfigBindError` for an error panic value: `Reason` "panic", `Err` the value, printed by type (`panic: *errors.errorString`); for another value `Reason` "panic of type T".
- `Shutdown` of a never-started container still shuts its services down, so it releases ownership through `forget`.
- An added layer is read when its refresh is due and at every poll where a watched file changed.
- Registration rollback and the phase re-check (D46 steps 7–8) are P1 behavior, built in group 11; only the view check and retry (steps 4–6) wait for group 20.

## Risks / Trade-offs

- **[Two API breaks (`NewWithOpts`/`CloneWithOpts` and their `Must` forms take a ctx and return an error; env names) plus one behavior change (options copied, not written)]** → No tag exists and no app is deployed. gopatch rewrites the call sites. The upgrading note lists each break with its migration. `New`/`Clone` call sites are untouched. No tag is cut before `unify-constructor`, so callers migrate once.
- **[go-playground costs every consumer 7 modules (plus go-playground/assert in the module graph), even without config (D7)]** → The cost is stated plainly. govulncheck runs per module, and trivy ignores are per CVE with an expiry. `InjectorOpts.ConfigValidator` avoids the code path but not the dependency.
- **[The TOML pre-pass relies on `go-toml/v2/unstable`, which has no compatibility promise]** → Pinned at v2.4.3 or later; the Parser API has been identical from v2.0.8 to v2.4.3. Through MVS a consumer that requires a newer go-toml can change `unstable` under us, and doger's own tests cannot catch that. A compile-only test fails loudly in our CI when the API we use moves.
- **[Hostile-file memory]** → The `yaml.Node` tree is built before any cap applies; the 1 MiB default keeps it near 60 MiB, and `MaxFileSize` godoc states the cost for anyone who raises it.
- **[An owner kept alive by user code (D47, caveat 1)]** → An `OnChange` subscriber that captures a clone keeps it and its entries alive. Documented on `OnChange`.
- **[A clone dropped without `Shutdown` vetoes until collected (D47, caveat 2)]** → Release on GC is not deterministic. Documented on `Clone`; the supported pattern is `defer clone.Shutdown()`.
- **[The Spring env mapping exists in two modules]** → One golden table, copied into each module, with a byte-equality test.
- **[Distinct keys share env variables; old dashed extension names are reinterpreted as instances]** → This is Spring's trade-off. Scenarios pin it, and the upgrading note warns about `DOGER_EXAMPLE__KV_ADDR`.
- **[A synchronous `Shutdown` from a callback waits on itself]** → Documented in godoc next to the existing Starter rule: the report fails when the deadline ends. The supported scenario is `go root.Shutdown()`.
- **[A slow subscriber delays later notifications]** → The dispatcher coalesces to the latest value per section, so nothing piles up. Godoc tells subscribers to stay fast.
- **[Retries while binds or reloads keep arriving]** → A bind retries only after a reload committed; a reload retries only when entries were added and rebinds just those. Both loops end unless the other side never stops, which a real application does not do.
- **[A fake-clock context differs from a real one]** → It is used only for a user `Clock`. `Deadline()` returns the caller's deadline; only `Done`/`Err` follow the clock. With the real clock, the stdlib context is used.
- **[A `Load` that ignores its context blocks construction]** → Documented. A ctx passed to `NewWithOpts` plus a cooperative source is the remedy.
- **[One source in two unrelated containers]** → Undefined behavior, documented on `ConfigSource` (D40). Pointer-only clone sharing never panics.
- **[synctest cannot see `propertySourceDecision`'s existing mutex]** → `testWithTimeout` wraps every synctest test from outside the bubble.
- **[TOML datetime and float rendering differs from YAML literals]** → Pinned by scenarios and documented: an int field accepts TOML `1.0` but rejects YAML `1.0`.

## Public API & Defaults

**Root module `doger`.** BREAKING items are flagged.

```go
// construction (nil ctx = context.Background(); the ctx is never stored)
func New(packages ...func(Injector)) *RootScope                                        // unchanged
func NewWithOpts(ctx context.Context, opts *InjectorOpts, packages ...func(Injector)) (*RootScope, error) // BREAKING
func MustNewWithOpts(ctx context.Context, opts *InjectorOpts, packages ...func(Injector)) *RootScope
func (s *RootScope) Clone() *RootScope                                                 // unchanged; always shares config
func (s *RootScope) CloneWithOpts(ctx context.Context, opts *InjectorOpts) (*RootScope, error) // BREAKING
func (s *RootScope) MustCloneWithOpts(ctx context.Context, opts *InjectorOpts) *RootScope
func (s *RootScope) AddConfigReloadFailedHook(fn func(root *RootScope, err error))

// InjectorOpts additions (BEHAVIOR CHANGE: constructors copy *InjectorOpts and never write it)
ConfigSource           ConfigSource     // nil: env-only default property source
ConfigTimeout          time.Duration    // 0: DefaultConfigTimeout; < 0: no timeout
ConfigValidator        ConfigValidator  // nil: go-playground; typed nil: ErrInvalidConfigOption
ConfigTagKey           string           // "": DefaultConfigTagKey; invalid: ErrInvalidConfigOption
HookConfigReloadFailed []func(root *RootScope, err error) // nil: warn only

const DefaultConfigTimeout = 10 * time.Second
const DefaultConfigTagKey = "cfg"

// contract
type PropertySource interface{ Lookup(key string) (string, bool) }                  // unchanged
type PropertyKeys interface{ Keys(prefix string) []string }
type ConfigView = interface{ PropertySource; PropertyKeys }
type ConfigSource interface{ PropertySource; PropertyKeys; Load(ctx context.Context) error }
type LogfSetter interface{ SetLogf(logf func(format string, args ...any)) }
type ConfigWatcher interface {
	Watch(ctx context.Context, changed func(ctx context.Context, next ConfigView, err error) (commit bool)) error
}
type ConfigValidator interface{ Validate(v any) error }
type ValueFreeError interface{ error; ValueFree() bool }
type MapSource map[string]string // Lookup, Keys
func (p Properties) Keys(prefix string) []string

// binding
func BindConfig[T any](i Injector, prefix string, defaults T) (ConfigValue[T], error)
func BindConfigNamed[T any](i Injector, name, prefix string, defaults T) (ConfigValue[T], error)
func ConfigValueName(name string) string
func ResolveConfig[T any](i Injector, prefix string, defaults T) (T, error)
type ConfigValue[T any] struct{ /* pointer to a shared cell */ }
func (v ConfigValue[T]) Get() T
func (v ConfigValue[T]) OnChange(fn func(T)) (stop func())
func StaticConfig[T any](v T) ConfigValue[T]
func BindProperties(p Properties, target any) error
func ValidateConfig(i Injector, v any) error

// errors (pointer receivers, nil-safe; Is, Unwrap, type-only GoString)
var ErrConfigLoadFailed, ErrConfigWatchFailed, ErrConfigConversion, ErrInvalidConfig,
	ErrInvalidConfigTarget, ErrInvalidConfigSource, ErrInvalidConfigOption,
	ErrConfigConflict, ErrConfigAlreadyBound error                      // ValueFree() == true
type ConfigLoadError struct{ Source reflect.Type; Err error }         // Is(ErrConfigLoadFailed), ValueFree
type ConfigBindError struct{ Key string; Type reflect.Type; Reason string; Err error } // Is(ErrConfigConversion), ValueFree
type ConfigValidationError struct{ Section string; Type reflect.Type; Violations []ConfigViolation; Err error } // Is(ErrInvalidConfig), ValueFree
type ConfigViolation struct{ Key, Rule, Param string }
```

- **BREAKING:** `EnvPropertySource` variable names (Spring mapping, `__` for instances) and prefix normalization (`myapp_` reads `MYAPP_`, `my-app` reads `MY_APP_`). `EnvPropertySource("")` still means no prefix.
- `HookContainerPanic` gains the points `config-watch`, `config-reload-failed` and `config-on-change`; the hook receives the raw value, which may hold a secret. The godoc of `Logf` no longer says nil means "no logging" for config warnings (they go to `slog.Default()` when `Logf` is unset). The godoc of `ErrStartAfterShutdown` adds `BindConfig`/`BindConfigNamed`.

**Config module `config`.**

```go
func New(opts ...Option) *Source
func (s *Source) Load(ctx context.Context) error           // only the container calls it
func (s *Source) Lookup(key string) (string, bool)
func (s *Source) Keys(prefix string) []string
func (s *Source) SetLogf(logf func(format string, args ...any))
func (s *Source) Watch(ctx context.Context, changed func(ctx context.Context, next View, err error) bool) error
type View = interface{ Lookup(key string) (string, bool); Keys(prefix string) []string }

type Option func(*options)
func Dir(paths ...string) Option              // no argument or "" = ErrInvalidOption
func File(paths ...string) Option             // no argument or "" = ErrInvalidOption; turns off discovery
func Name(base string) Option
func Format(f FileFormat) Option              // "" = by extension; a built-in or a Decoder extension
func Decoder(ext string, fn DecodeFunc) Option // ext case-insensitive, leading "." optional
func Profile(name string) Option              // "" = no profile, overrides the variable
func ProfileEnv(name string) Option
func EnvPrefix(prefix string) Option          // "" = ErrInvalidOption; default "APP_"
func EnvMapper(fn func(key string) string) Option // returns the full variable name; nil = Spring mapping
func DotEnv(path string) Option               // "" = off
func DotEnvExport() Option
func Layer(name LayerName, src LayerSource) Option
func LayerOrder(names ...LayerName) Option    // full order, lowest first; no argument = ErrInvalidOption
func LayerRefresh(name LayerName, d time.Duration) Option  // 0 = DefaultLayerRefresh; < 0 = only at Load and on a file change
func Logger(l *slog.Logger) Option            // nil = unset
func DisableWatch() Option
func PollInterval(d time.Duration) Option     // 0 = DefaultPollInterval; < 0 = ErrInvalidOption
func StageTimeout(d time.Duration) Option     // 0 = DefaultStageTimeout; < 0 = none
func MaxFileSize(n int64) Option              // 0 = DefaultMaxFileSize; < 0 = ErrInvalidOption
func MaxKeys(n int) Option                    // 0 = DefaultMaxKeys; < 0 = ErrInvalidOption

type FileFormat string
const (YAML FileFormat = "yaml"; TOML FileFormat = "toml"; JSON FileFormat = "json")
type DecodeFunc func(data []byte) (map[string]any, error)
type LayerSource interface{ LoadLayer(ctx context.Context) (map[string]string, error) }
type LayerName string
const (LayerBase LayerName = "base"; LayerProfile LayerName = "profile"; LayerDotEnv LayerName = "dotenv"; LayerEnv LayerName = "env")
const DefaultPollInterval = 10 * time.Second
const DefaultStageTimeout = 10 * time.Second
const DefaultLayerRefresh = time.Minute
const DefaultMaxFileSize = 1 << 20 // 1 MiB; the YAML node tree costs about 60 MiB at this size
const DefaultMaxKeys = 10_000

type ParseCategory string
const (ParseCategorySyntax ParseCategory = "syntax error"; ParseCategoryDuplicateKey ParseCategory = "duplicate key";
	ParseCategoryMultipleDocuments ParseCategory = "multiple documents"; ParseCategoryNotMapping ParseCategory = "not a mapping";
	ParseCategoryExcessiveAliasing ParseCategory = "excessive aliasing"; ParseCategoryInvalidLine ParseCategory = "invalid line";
	ParseCategoryInvalidKey ParseCategory = "invalid key"; ParseCategoryTooDeep ParseCategory = "too deep";
	ParseCategoryTooManyKeys ParseCategory = "too many keys"; ParseCategoryUnsupportedValue ParseCategory = "unsupported value";
	ParseCategoryPanic ParseCategory = "panic")
// every error type: pointer receivers, nil-safe ValueFree() bool
type ParseError struct{ File string; Line int; Category ParseCategory }
type FileError struct{ File string; Err error }           // Unwrap
type LayerError struct{ Layer LayerName; Err error }      // Unwrap
type OptionError struct{ Option, Reason string }          // Is(ErrInvalidOption)
type ProfileError struct{ Profile string }                // Is(ErrProfileNotFound)
type AmbiguousFormatError struct{ Files []string }        // Is(ErrAmbiguousFormat)
var ErrAmbiguousFormat, ErrProfileNotFound, ErrInvalidOption, ErrFileTooLarge,
	ErrNotRegularFile, ErrUnknownFormat, ErrWatching error // type sentinelError string
```

Defaults are as in the proposal's Defaults & Control table; every one is overridable by the field or option named there.

**Migration for consumers** (every API break, flagged):
- **BREAKING** `doger.NewWithOpts(o, pkgs...)` becomes `root, err := doger.NewWithOpts(ctx, o, pkgs...)`, or `doger.MustNewWithOpts(ctx, o, pkgs...)` in tests; pass `context.Background()` (or nil) where no context is at hand.
- **BREAKING** `root.CloneWithOpts(o)` becomes `clone, err := root.CloneWithOpts(ctx, o)`, or `root.MustCloneWithOpts(ctx, o)`.
- **BEHAVIOR CHANGE** Code that read defaults back from its own `*InjectorOpts` after construction, edited the struct afterwards, or expected `root.Add*Hook` to append to it must use the container instead (no repository code does).
- **BREAKING** Rename env variables, for example `DOGER_REDIS_POOL__SIZE` to `DOGER_REDIS_POOLSIZE` and `DOGER_REDIS___QUEUE_ADDR` to `DOGER_REDIS__QUEUE_ADDR`. Check the variables of dashed extension names.
- Everything else is new API and breaks nothing.

## Migration Plan

Task-group dependencies (P1 = groups 1-18, P2 = groups 19-23, 24 = integration only):

```
[1 workspace, CI, release]
    |
[2 key grammar] -> [3 env mapping, prefixes, golden table] -> [4 PropertyKeys, MapSource,
                                                                  FuzzKeyGrammar]
                                                                    |
[5 ctx-taking fallible constructors, ConfigSource, ConfigLoadError, ValueFreeError,
   options copy, Clone sharing]
    |                 \
[6 option checks]   [7 Load deadline, ConfigTimeout, user-clock context]
    |
[8 binder: scalars, names, conversions, ResolveConfig] -> [9 lists, maps, pointers]
    -> [10 uniform form, tags, target checks, Lookup fallback]
    -> [11 BindConfig, handles, registration rollback, BindProperties]
    -> [12 validation]
[13 formats, flattening, caps] -> [14 discovery, profiles, paths, open and size]
    -> [15 env layer, keys] -> [16 .env, precedence, logging]
    -> [17 added layers, options, errors, Load lifecycle, panics]
[18 root + config end to end]                (needs 11, 12 and 17)
[19 watcher lifecycle, hooks API, dispatcher] (needs 11 and 12)
    -> [20 commit, read paths, bind/reload protocol (D46/D47 retry)]
    -> [21 failures, subscribers, shutdown] -> [22 clones and owners]
    -> [23 config.New polling, layer refresh]   (also needs 17)
[24 integration and docs] (after all)
```

1. Land on a branch. It is untagged, so there is no deprecation window, and no tag is cut before `unify-constructor`.
2. Rewrite call sites with gopatch (about 190 `NewWithOpts` and 12 `CloneWithOpts`; the ctx argument is `context.Background()` in tests and `ctx` where one is in scope): the root, `scheduler/gocron`, `examples/*`, `docs/` and the README. The `http/*` modules have no call sites.
3. Build and test every workspace module. Run the `GOWORK=off` job.
4. Rollback is a revert; there is no persistent state.

## Test Strategy

**Kinds:**
- table-driven unit tests with named subtests (testify; `require` for setup, `assert` for checks);
- `-race` on every concurrency scenario;
- goleak through `TestMain` in both modules (goleak tests are non-parallel, use `goleak.IgnoreCurrent()` and goleak's default retries);
- `testing/synctest` for time and goroutine scenarios: the root is built inside the bubble, and results are received inside the bubble with `select` on a result channel and `time.After` (bubble time), so a RED test fails with an assertion instead of a bubble deadlock, which would abort the whole test binary. `testWithTimeout` still wraps every synctest test from outside the bubble, as a backstop for blocking synctest cannot see (a `sync.Mutex` wait is not durably blocking);
- a fake `InjectorOpts.Clock` only for the user-clock context scenarios;
- `runtime.GC()` retries (up to 100 attempts) for the `AddCleanup` fallback;
- `Example*` tests;
- fuzz tests;
- CI steps for the dependency footprint, govulncheck and the release pin.

**Test hygiene:**
- `testWithTimeout` is copied into the config module's `main_test.go`.
- Helpers `clearEnvPrefix(t, "APP_")` and `clearEnvPrefix(t, "DOGER_")` use `t.Setenv` then `os.Unsetenv`, so cleanup restores the variables.
- `t.Chdir` and `t.Setenv` tests are not parallel; hostile-file row 4 is a synctest poll.
- `slog.SetDefault` and `log.SetOutput`/`SetFlags` are saved and restored with `t.Cleanup`.
- `DefaultRootScope` tests use a per-run unique service name and a uniquely named `DOGER_` variable, and are not parallel.
- Tests are named after their source files: `config_bind_test.go`, `config_validate_test.go`, `config_source_test.go`, `config_reload_test.go`, and `config/integration_test.go` for root + config end-to-end tests. The `config.New` tests keep the `TestFiles_*` prefix.
- The root reload tests use a root-local test watcher type, which calls `changed` on its `Watch` goroutine, and a root-local error type, never `config.*`.
- Examples live in `*_example_test.go` and use no helper from `main_test.go`. The rewritten `ExampleEnvPropertySource` sets its variable with `os.Setenv` after saving it with `os.LookupEnv`, and restores the saved state (value or unset) in a `defer`.
- Secrets planted in error and log assertions are high-entropy markers (`s3cret-9f2c71d04ab8`).
- The golden env table exists in both modules; `TestEnvMapping_GoldenCopiesEqual` (root, added in group 15 with the copy; not a scenario test) asserts that `testdata/envmapping.txt` and `config/testdata/envmapping.txt` are byte-equal.

**Spike to RED test:**
- spike 1, watcher leak: `TestConfigWatch_Lifecycle`
- spike 2, re-entrant `OnChange`: `TestConfigReload_ReentrantCallbacks`
- spike 3, reload race: `TestConfigReload_ConcurrentReads` and `config/TestFiles_ConcurrentReadsDuringLoad`
- spike 4, env-only key: `TestResolveConfig_EnvOnlyKey` and `config/TestBindConfig_EnvOnlyKeyWithFiles`
- spike 5, `.env` export: `config/TestFiles_DotEnv`

**Hot paths and their tests:**
- **Construction and load:** `TestNewWithOpts_ConstructionOutcome`, `TestNewWithOpts_ContextValues`, `TestNewWithOpts_Cancel`, `TestConfigSource_LoadFailure`, `TestConfigSource_LoadDeadlineRealClock`, `TestNewWithOpts_OptionsNotWritten`, `TestNewWithOpts_OptionsNotAliased`.
- **Property lookup, grammar and env:** `TestProperties_InvalidKeys`, `TestProperties_KeySuffixes`, `TestEnvPropertySource_Mapping`, `TestMapSource_KeysRoundTrip`.
- **Binder:** `TestBind_*`, including `TestBind_LookupFallbackForScalars`.
- **Typed binding and resolution:** `TestBindConfig_*`, `TestResolveConfig_*`, `TestConfigValue_Get`.
- **Validation:** `TestValidate_*`.
- **Reload commit:** `TestConfigWatch_CommitDecision`, `TestConfigReload_AllOrNothing`, `TestConfigReload_ReadPathsAgree`, `TestConfigWatch_NilViewIsStagingFailure`.
- **Concurrency:** `TestBindConfig_Concurrent`, `TestBindConfig_RacingReload`, `TestBindConfig_ReloadDuringBind`, `TestBindConfig_RacesShutdown`, `TestConfigWatch_ChangedSerialized`, `TestConfigReload_CloneRacingCommit`, `TestConfigReload_NoMixedValues`, `TestConfigReload_ConcurrentReads`, `config/TestFiles_LoadsSerialized`, `config/TestFiles_LoadWaitHonorsContext`.
- **Lifecycle and shutdown:** `TestConfigWatch_Lifecycle`, `TestConfigWatch_RestartWaitsForPreviousWatch`, `TestConfigReload_ShutdownBoundedByContext`, `TestConfigReload_ShutdownDuringReload`, `TestConfigReload_OwnersGate`, `TestConfigReload_DroppedCloneReleasedByGC`, `TestNew_NoHealthGoroutine`.
- **Loader:** `config/TestFiles_FlattenAndNormalize`, `config/TestFiles_LayerPrecedence`, `config/TestFiles_UniformFormPrecedence`, `config/TestFiles_DepthCapPerFormat`, `config/TestFiles_KeyCap`.

**Fuzz harness.** Fuzz tests run in-package against the decoders and parsers, not through temp files. Values are planted as high-entropy markers (`zq7Xk2vP9w`) in value positions only. The property is: no panic, no marker in `Error()` or `%#v`, and error text matching `^<file>(:\d+)?: <category>$`. Seed corpora are committed.
- **`FuzzFiles_YAML`:** seeds include billion-laughs, a `<<` merge bomb and a self-referencing anchor.
- **`FuzzFiles_TOML`:** seeds include a 100-deep dotted table and nested inline tables.
- **`FuzzFiles_JSON`**.
- **`FuzzFiles_DotEnv`**.
- **`FuzzBind_Scalars`:** random strings into every supported kind.
- **`FuzzKeyGrammar`** (group 4) asserts:
  - `uniform(reverse(envName(k))) == uniform(k)` for addressable keys;
  - `envName(reverse(v)) == v` for every v that `reverse` accepts;
  - seeds include `redis/0.addr`.

**Examples** (deterministic `// Output:` from `testdata/` dirs and unique prefixes):
- root: `ExampleNewWithOpts_config`, `ExampleBindConfig`, `ExampleBindConfigNamed`, `ExampleResolveConfig`, `ExampleConfigValue_OnChange`, `ExampleStaticConfig`, `ExampleBindProperties`, `ExampleValidateConfig`, `ExampleMapSource`, `ExampleEnvPropertySource` (rewritten);
- config: `ExampleNew`, `ExampleNew_overrides`, `ExampleLayer`, `ExampleDecoder`.

**Scenario map.** Every `#### Scenario:` in `specs/*/spec.md` and its one test (264 scenarios, 264 tests). File names without a `config/` prefix are in the root package. Examples rows run as named subtests.

| Capability | Scenario | Test | Kind | Group |
|---|---|---|---|---|
| config-binding | An env-only key binds | `config_bind_test.go: TestResolveConfig_EnvOnlyKey` | unit, table | 8 |
| config-binding | The source receives canonical keys | `config_bind_test.go: TestBind_CanonicalKeys` | unit, table | 8 |
| config-binding | Non-ASCII names | `config_bind_test.go: TestBind_NonASCIIName` | unit, table | 10 |
| config-binding | Multi-word lists, maps and pointers bind from env | `config_bind_test.go: TestBind_UniformKeysFromEnv` | unit, table | 9 |
| config-binding | Equal spellings in uniform form | `config_bind_test.go: TestBind_UniformFolding` | unit, table | 10 |
| config-binding | An instance segment is never folded | `config_bind_test.go: TestBind_InstanceNotFolded` | unit, table | 10 |
| config-binding | A map source round-trips its own spelling | `config_bind_test.go: TestBind_KeysLookedUpAsReturned` | unit, table | 10 |
| config-binding | Several spellings of one key in one source | `config_bind_test.go: TestBind_DuplicateSpellingTieBreak` | unit, table | 10 |
| config-binding | A scalar the source does not list is still looked up | `config_bind_test.go: TestBind_LookupFallbackForScalars` | unit, table | 10 |
| config-binding | Tags rename or skip fields | `config_bind_test.go: TestBind_TagOverride` | unit, table | 10 |
| config-binding | The tag key can be changed | `config_bind_test.go: TestBind_ConfigTagKey` | unit, table | 10 |
| config-binding | Two containers bind one type with different tag keys | `config_bind_test.go: TestBind_PlanCacheByTagKey` | unit, table | 10 |
| config-binding | An invalid tag key fails construction | `config_bind_test.go: TestNewWithOpts_InvalidConfigTagKey` | unit, table | 10 |
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
| config-binding | Errors from UnmarshalText | `config_bind_test.go: TestBind_UnmarshalTextErrors` | unit, table | 8 |
| config-binding | Element and entry keys in bind errors | `config_bind_test.go: TestBind_ErrorKeysCanonical` | unit, table | 9 |
| config-binding | Panics in user code | `config_bind_test.go: TestBind_PanicRecovered` | unit, table | 8 |
| config-binding | Defaults survive binding | `config_bind_test.go: TestBind_DefaultsNotMutated` | unit, table | 8 |
| config-binding | Invalid type parameters | `config_bind_test.go: TestBindConfig_InvalidType` | unit, table | 11 |
| config-binding | Valid and invalid prefixes | `config_bind_test.go: TestResolveConfig_PrefixGrammar` | unit, table | 8 |
| config-binding | A typed-nil injector | `config_bind_test.go: TestResolveConfig_TypedNilInjector` | unit, table | 8 |
| config-binding | A bound section is invocable | `config_bind_test.go: TestBindConfig_Registers` | unit, table | 11 |
| config-binding | A failed bind registers nothing | `config_bind_test.go: TestBindConfig_FailureRegistersNothing` | unit, table | 11 |
| config-binding | A failing second registration is rolled back | `config_bind_test.go: TestBindConfig_RegistrationRollback` | unit, table | 11 |
| config-binding | A bind that meets the start of shutdown is rolled back | `config_bind_test.go: TestBindConfig_ShutdownDuringRegistration` | unit (channels, testWithTimeout) | 11 |
| config-binding | A nil injector binds into DefaultRootScope | `config_bind_test.go: TestBindConfig_NilInjector` | unit (not parallel) | 11 |
| config-binding | A child scope binds from its container and registers locally | `config_bind_test.go: TestBindConfig_ChildScope` | unit, table | 11 |
| config-binding | Two databases of one type | `config_bind_test.go: TestBindConfigNamed_TwoPrefixes` | unit, table | 11 |
| config-binding | An empty name is rejected | `config_bind_test.go: TestBindConfigNamed_EmptyName` | unit, table | 11 |
| config-binding | Duplicate bindings | `config_bind_test.go: TestBindConfig_Duplicate` | unit, table | 11 |
| config-binding | Unresolved defaults and parent registrations give way | `config_bind_test.go: TestBindConfig_ReplacesDefaultShadowsParent` | unit, table | 11 |
| config-binding | Binding in each phase | `config_bind_test.go: TestBindConfig_Phases` | unit, table | 11 |
| config-binding | A bind racing Shutdown | `config_bind_test.go: TestBindConfig_RacesShutdown` | race | 11 |
| config-binding | Resolving in every phase | `config_bind_test.go: TestResolveConfig_AllPhases` | unit, table | 11 |
| config-binding | A PropertySource service registered after a bind | `config_bind_test.go: TestBind_DecidesPropertySource` | unit, table | 11 |
| config-binding | Two consumers resolve one prefix with different defaults | `config_bind_test.go: TestResolveConfig_ConsumerDefaults` | unit, table | 11 |
| config-binding | A failed resolve returns the zero value | `config_bind_test.go: TestResolveConfig_Failure` | unit, table | 11 |
| config-binding | Resolving inside a provider uses the provider's container | `config_bind_test.go: TestResolveConfig_InProviderAndClone` | unit, table | 11 |
| config-binding | Handle values | `config_bind_test.go: TestConfigValue_Get` | unit, table | 11 |
| config-binding | OnChange on a handle that never changes | `config_bind_test.go: TestConfigValue_OnChangeStatic` | unit, table | 11 |
| config-binding | An extension binds over its defaults | `config_bind_test.go: TestBindProperties_Extension` | unit, table | 11 |
| config-binding | BindProperties follows the binder's rules | `config_bind_test.go: TestBindProperties_SameRules` | unit, table | 11 |
| config-binding | A view stays usable after Extend returns | `config_bind_test.go: TestBindProperties_AfterExtend` | unit, table | 11 |
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
| config-files | Built-in and custom decoders | `config/decode_test.go: TestFiles_Decoders` | unit, table | 13 |
| config-files | Rendering a decoder's values | `config/decode_test.go: TestFiles_DecoderRendering` | unit, table | 13 |
| config-files | Flattening and normalization | `config/keys_test.go: TestFiles_FlattenAndNormalize` | unit, table | 13 |
| config-files | Keys that cannot be normalized | `config/keys_test.go: TestFiles_InvalidKeys` | unit, table | 13 |
| config-files | Shapes that fail | `config/keys_test.go: TestFiles_InvalidShapes` | unit, table | 13 |
| config-files | Empty content | `config/keys_test.go: TestFiles_EmptyDocuments` | unit, table | 13 |
| config-files | Depth cap per format | `config/keys_test.go: TestFiles_DepthCapPerFormat` | unit, table | 13 |
| config-files | Key cap | `config/keys_test.go: TestFiles_KeyCap` | unit, table | 13 |
| config-files | Rendering by format | `config/decode_test.go: TestFiles_ScalarRendering` | unit, table | 13 |
| config-files | Aliases and merges | `config/decode_test.go: TestFiles_YAMLAliasesAndMerge` | unit, table | 13 |
| config-files | Highest layer wins | `config/layers_test.go: TestFiles_LayerPrecedence` | unit (t.Setenv) | 16 |
| config-files | Spelling never decides precedence | `config/layers_test.go: TestFiles_UniformFormPrecedence` | unit (t.Setenv) | 15 |
| config-files | Lists across layers | `config/layers_test.go: TestFiles_ListFromOneLayer` | unit (t.Setenv) | 15 |
| config-files | Prefix forms | `config/env_test.go: TestFiles_EnvPrefix` | unit (t.Setenv) | 15 |
| config-files | Key to variable | `config/env_test.go: TestFiles_EnvMapping` | unit (golden table copy) | 15 |
| config-files | Variables mapped back to keys | `config/env_test.go: TestFiles_EnvReverseMapping` | unit (t.Setenv) | 15 |
| config-files | Mapper with and without a prefix | `config/env_test.go: TestFiles_EnvMapper` | unit (t.Setenv) | 15 |
| config-files | .env handling | `config/dotenv_test.go: TestFiles_DotEnv` | unit (spike 5) | 16 |
| config-files | Line forms | `config/dotenv_test.go: TestFiles_DotEnvLines` | unit, table | 16 |
| config-files | A later line wins | `config/dotenv_test.go: TestFiles_DotEnvLaterLineWins` | unit, table | 16 |
| config-files | Value forms | `config/dotenv_test.go: TestFiles_DotEnvValues` | unit, table | 16 |
| config-files | Export at the first Load | `config/dotenv_test.go: TestFiles_DotEnvExport` | unit (t.Setenv) | 16 |
| config-files | A second Load exports nothing and sees the exported variables | `config/dotenv_test.go: TestFiles_DotEnvExportOnce` | unit (t.Setenv) | 16 |
| config-files | A vault-style layer | `config/layers_test.go: TestFiles_AddedLayer` | unit, table | 17 |
| config-files | A failing added layer | `config/layers_test.go: TestFiles_AddedLayerLoadError` | unit, table | 17 |
| config-files | A layer key that cannot be normalized | `config/layers_test.go: TestFiles_AddedLayerInvalidKey` | unit, table | 17 |
| config-files | One type as both a source and a layer | `config/layers_test.go: TestFiles_LayerSourceAndConfigSource` | unit, table | 17 |
| config-files | Keys across layers | `config/keys_test.go: TestFiles_Keys` | unit (t.Setenv) | 15 |
| config-files | Ordering, duplicates and before Load | `config/keys_test.go: TestFiles_KeysOrderingAndDuplicates` | unit, table | 15 |
| config-files | A syntax error near a secret | `config/errors_test.go: TestFiles_LoadErrorHasNoValue` | unit, table | 13 |
| config-files | Error texts and matching | `config/errors_test.go: TestFiles_ErrorsValueFree` | unit, table | 17 |
| config-files | A context error is returned unwrapped | `config/files_test.go: TestFiles_ContextErrorUnwrapped` | unit, table | 17 |
| config-files | Size limit | `config/files_test.go: TestFiles_MaxFileSize` | unit, table | 14 |
| config-files | Non-regular files | `config/files_test.go: TestFiles_NotRegularFile` | unit (FIFO, skip on Windows) | 14 |
| config-files | Invalid options | `config/options_test.go: TestFiles_InvalidOptions` | unit, table | 17 |
| config-files | Option values that mean the default or unset | `config/options_test.go: TestFiles_OptionZeroValues` | unit, table | 17 |
| config-files | A cancelled load keeps the previous snapshot | `config/files_test.go: TestFiles_LoadContextCancelled` | unit, table | 17 |
| config-files | An already-cancelled context | `config/files_test.go: TestFiles_LoadAlreadyCancelled` | unit, table | 17 |
| config-files | Two overlapping Loads | `config/files_test.go: TestFiles_LoadsSerialized` | race | 17 |
| config-files | A waiting Load honors its context | `config/files_test.go: TestFiles_LoadWaitHonorsContext` | race | 17 |
| config-files | Load while watching | `config/watch_test.go: TestFiles_LoadWhileWatching` | synctest | 23 |
| config-files | Concurrent reads while loading | `config/files_test.go: TestFiles_ConcurrentReadsDuringLoad` | race (spike 3) | 17 |
| config-files | A panicking layer or decoder | `config/errors_test.go: TestFiles_UserPanics` | unit, table | 17 |
| config-files | Log sink | `config/options_test.go: TestFiles_LogSink` | unit (slog/log restore, not parallel) | 16 |
| config-files | An env-only key binds although the file lacks it | `config/integration_test.go: TestBindConfig_EnvOnlyKeyWithFiles` | unit (spike 4) | 18 |
| config-files | A scalar field binds from a mapped variable | `config/integration_test.go: TestBindConfig_EnvMapperScalar` | unit (t.Setenv) | 18 |
| config-files | Every file spelling binds the same field | `config/integration_test.go: TestResolveConfig_FileSpellings` | unit, table | 18 |
| config-files | Compile-time contract checks | `config/source_test.go: TestSource_NotAValidator` | build + unit | 23 |
| config-files | Dependency footprint | `CI: footprint step (go list -deps)` | CI step | 1 |
| config-reload | The source adopts only what the root accepts | `config_reload_test.go: TestConfigWatch_CommitDecision` | unit, table | 20 |
| config-reload | A nil view is a staging failure | `config_reload_test.go: TestConfigWatch_NilViewIsStagingFailure` | unit, table | 20 |
| config-reload | Overlapping changed calls are serialized | `config_reload_test.go: TestConfigWatch_ChangedSerialized` | race | 20 |
| config-reload | Read paths agree after a commit and after a rejection | `config_reload_test.go: TestConfigReload_ReadPathsAgree` | unit, table | 20 |
| config-reload | The staged view is invisible until commit | `config_reload_test.go: TestConfigWatch_StagedInvisibleUntilCommit` | unit, table | 20 |
| config-reload | One BindProperties call reads one view | `config_reload_test.go: TestConfigReload_BindPropertiesOneView` | race | 20 |
| config-reload | One invalid section blocks every section | `config_reload_test.go: TestConfigReload_AllOrNothing` | unit, table | 20 |
| config-reload | Readers never see a mixed value | `config_reload_test.go: TestConfigReload_NoMixedValues` | race | 20 |
| config-reload | Live handle versus snapshot after a reload | `config_reload_test.go: TestConfigReload_LiveVersusSnapshot` | unit, table | 20 |
| config-reload | Failures warn and call the hooks | `config_reload_test.go: TestConfigReload_FailedWarnsAndHooks` | unit, table | 21 |
| config-reload | Several failing sections warn once, one line per section | `config_reload_test.go: TestConfigReload_FailedWarnsPerSection` | unit, table | 21 |
| config-reload | Hooks run after the start context ended | `config_reload_test.go: TestConfigReload_HookAfterStartCtxEnded` | unit, table | 21 |
| config-reload | Who is notified | `config_reload_test.go: TestConfigValueOnChange_Notified` | unit, table | 21 |
| config-reload | Every handle is published before the first notification | `config_reload_test.go: TestConfigReload_PublishBeforeNotify` | unit, table | 21 |
| config-reload | Static and zero handles never notify | `config_reload_test.go: TestConfigValue_StaticAndZeroNeverNotify` | unit, table | 21 |
| config-reload | Stop semantics | `config_reload_test.go: TestConfigValueOnChange_Stop` | race | 21 |
| config-reload | A running call finishes after stop | `config_reload_test.go: TestConfigValueOnChange_StopDuringCall` | synctest | 21 |
| config-reload | A nil callback is ignored | `config_reload_test.go: TestConfigValueOnChange_NilFunc` | unit, table | 21 |
| config-reload | A slow subscriber receives the latest value | `config_reload_test.go: TestConfigReload_DispatcherCoalesces` | synctest | 21 |
| config-reload | Pending notifications are dropped at stop | `config_reload_test.go: TestConfigReload_PendingDroppedAtStop` | synctest | 21 |
| config-reload | A panicking callback | `config_reload_test.go: TestConfigReload_CallbackPanic` | unit, table | 21 |
| config-reload | Re-entrant calls from a callback | `config_reload_test.go: TestConfigReload_ReentrantCallbacks` | synctest (spike 2) | 21 |
| config-reload | A blocked callback does not hang Shutdown | `config_reload_test.go: TestConfigReload_ShutdownBoundedByContext` | synctest, goleak | 21 |
| config-reload | A reload in flight when shutdown begins does not commit | `config_reload_test.go: TestConfigReload_ShutdownDuringReload` | synctest | 21 |
| config-reload | Changes after shutdown began are dropped | `config_reload_test.go: TestConfigReload_ChangedAfterShutdown` | unit, table | 21 |
| config-reload | Concurrent reads under reload | `config_reload_test.go: TestConfigReload_ConcurrentReads` | race (spike 3) | 20 |
| config-reload | Watcher lifecycle | `config_reload_test.go: TestConfigWatch_Lifecycle` | synctest, goleak (spike 1) | 19 |
| config-reload | A restart waits for a Watch that ignored its context | `config_reload_test.go: TestConfigWatch_RestartWaitsForPreviousWatch` | synctest, goleak | 19 |
| config-reload | How a watch ends | `config_reload_test.go: TestConfigWatch_Ends` | unit, table | 19 |
| config-reload | A watch panic goes to HookContainerPanic when set | `config_reload_test.go: TestConfigWatch_PanicHook` | unit, table | 19 |
| config-reload | One reload reaches parent and clone | `config_reload_test.go: TestConfigReload_CloneShares` | unit, table | 22 |
| config-reload | A clone made during commits | `config_reload_test.go: TestConfigReload_CloneRacingCommit` | race | 22 |
| config-reload | Which sections gate a reload | `config_reload_test.go: TestConfigReload_OwnersGate` | unit, table | 22 |
| config-reload | A clone keeps receiving a section after its parent shut down | `config_reload_test.go: TestConfigReload_CloneOutlivesParent` | unit, table | 22 |
| config-reload | A dropped clone stops vetoing once collected | `config_reload_test.go: TestConfigReload_DroppedCloneReleasedByGC` | unit (runtime.GC retries, not parallel) | 22 |
| config-reload | Whose hooks a failure fires | `config_reload_test.go: TestConfigReload_HookOwnership` | unit, table | 22 |
| config-reload | A bind racing a reload ends with the handle holding the newer view | `config_reload_test.go: TestBindConfig_RacingReload` | race | 20 |
| config-reload | A reload that commits during a bind is not lost | `config_reload_test.go: TestBindConfig_ReloadDuringBind` | unit (channels, testWithTimeout) | 20 |
| config-reload | Poll interval and debounce | `config/watch_test.go: TestFiles_PollInterval` | synctest | 23 |
| config-reload | A negative poll interval fails construction | `config/watch_test.go: TestFiles_PollIntervalNegative` | unit, table | 23 |
| config-reload | One source is watched once at a time | `config/watch_test.go: TestFiles_WatchOnce` | synctest | 23 |
| config-reload | What a poll does | `config/watch_test.go: TestFiles_PollReloads` | synctest (t.Chdir) | 23 |
| config-reload | A Kubernetes ConfigMap swap is a change | `config/watch_test.go: TestFiles_PollSymlinkSwap` | synctest (skip on Windows) | 23 |
| config-reload | Repeated content is staged once | `config/watch_test.go: TestFiles_PollStagesOnce` | synctest | 23 |
| config-reload | When an added layer is re-read | `config/watch_test.go: TestFiles_LayerRefresh` | synctest | 23 |
| config-reload | A failing added layer is retried | `config/watch_test.go: TestFiles_PollRetriesLayerError` | synctest | 23 |
| config-reload | A layer that never settles | `config/watch_test.go: TestFiles_LayerNeverSettles` | synctest | 23 |
| config-reload | What a poll refuses or times out on | `config/watch_test.go: TestFiles_PollBounded` | synctest | 23 |
| config-reload | A poll applies the Load size limit | `config/watch_test.go: TestFiles_PollSizeLimit` | synctest | 23 |
| config-reload | Hostile files | `config/watch_test.go: TestFiles_HostileYAML` | unit + fuzz seeds | 23 |
| config-reload | A panic during a poll is a staging error | `config/watch_test.go: TestFiles_PollRecoversLayerPanic` | synctest | 23 |
| config-sources | Which types are sources | `config_source_test.go: TestConfigSource_Contract` | unit, table | 5 |
| config-sources | Property reads go to the installed source | `config_source_test.go: TestConfigSource_IsPropertySource` | unit, table | 5 |
| config-sources | The installed source is not a service | `config_source_test.go: TestConfigSource_NotAService` | unit, table | 5 |
| config-sources | Invalid option combinations | `config_source_test.go: TestConfigSource_OptionChecks` | unit, table | 6 |
| config-sources | SetLogf and Load order | `config_source_test.go: TestConfigSource_SetLogfOrder` | unit, table | 5 |
| config-sources | The logger reaches the source | `config_source_test.go: TestConfigSource_LogfReachesSource` | unit, table | 5 |
| config-sources | The Load deadline on the real clock | `config_source_test.go: TestConfigSource_LoadDeadlineRealClock` | synctest | 7 |
| config-sources | A Load that honors its deadline | `config_source_test.go: TestConfigSource_LoadTimeout` | synctest | 7 |
| config-sources | A Load that ignores its deadline | `config_source_test.go: TestConfigSource_LoadIgnoresDeadline` | synctest | 7 |
| config-sources | Deadline and Err on a fake clock | `config_source_test.go: TestConfigSource_FakeClockContext` | goleak | 7 |
| config-sources | Which side ends the Load context | `config_source_test.go: TestConfigSource_FakeClockEnds` | unit, table | 7 |
| config-sources | A caller deadline ends Load while the user clock stands still | `config_source_test.go: TestConfigSource_UserClockCallerDeadline` | synctest | 7 |
| config-sources | A child timeout inside Load fires with a user clock | `config_source_test.go: TestConfigSource_UserClockChildTimeout` | synctest | 7 |
| config-sources | A source that keeps its Load context | `config_source_test.go: TestConfigSource_LoadContextEnds` | goleak | 7 |
| config-sources | Load errors and panics | `config_source_test.go: TestConfigSource_LoadFailure` | unit, table | 5 |
| config-sources | Which text a load error shows | `config_source_test.go: TestConfigSource_ValueFreeText` | unit, table | 5 |
| config-sources | Which errors are value-free | `config_source_test.go: TestValueFreeError_Implementers` | unit, table | 6 |
| config-sources | Sharing on clone | `config_source_test.go: TestConfigSource_CloneShares` | unit, table | 5 |
| config-sources | A non-pointer source on clone | `config_source_test.go: TestConfigSource_CloneNonPointer` | unit, table | 5 |
| config-sources | Option checks run before sharing | `config_source_test.go: TestCloneWithOpts_ConflictBeforeSharing` | unit, table | 6 |
| config-sources | Secrets stay out of construction errors and logs | `config_source_test.go: TestConfigSource_NoValuesInErrorsOrLogs` | unit, table | 5 |
| config-validation | Default tag validation | `config_validate_test.go: TestValidate_DefaultTags` | unit, table | 12 |
| config-validation | Range boundaries | `config_validate_test.go: TestValidate_RangeBoundaries` | unit, table | 12 |
| config-validation | A failed validation registers nothing | `config_validate_test.go: TestBindConfig_ValidationRegistersNothing` | unit, table | 12 |
| config-validation | BindProperties does not validate | `config_validate_test.go: TestBindProperties_NoValidation` | unit, table | 12 |
| config-validation | Skipped fields with failing tags | `config_validate_test.go: TestValidate_SkippedFields` | unit, table | 12 |
| config-validation | Validate method results | `config_validate_test.go: TestValidate_Method` | unit, table | 12 |
| config-validation | Validate methods of fields and embedded types | `config_validate_test.go: TestValidate_TopLevelAndPromoted` | unit, table | 12 |
| config-validation | Changes made by Validate are not kept | `config_validate_test.go: TestValidate_MethodRunsOnCopy` | unit, table | 12 |
| config-validation | Messages for Validate errors | `config_validate_test.go: TestValidate_ValueFreeText` | unit, table | 12 |
| config-validation | Validator choice | `config_validate_test.go: TestValidate_Replacement` | unit, table | 12 |
| config-validation | Clones keep or replace the validator | `config_validate_test.go: TestValidate_CloneValidator` | unit, table | 12 |
| config-validation | A typed-nil validator fails construction | `config_validate_test.go: TestNewWithOpts_TypedNilValidator` | unit, table | 12 |
| config-validation | Defaults are validated too | `config_validate_test.go: TestValidate_MergedValue` | unit, table | 12 |
| config-validation | Error shape and keys | `config_validate_test.go: TestValidate_ErrorShape` | unit, table | 12 |
| config-validation | Secret values stay out of errors and logs | `config_validate_test.go: TestValidate_NoValuesLeaked` | unit, table | 12 |
| config-validation | A malformed tag | `config_validate_test.go: TestValidate_MalformedTag` | unit, table | 12 |
| config-validation | An extension validates after applying its options | `config_validate_test.go: TestValidateConfig_Extension` | unit, table | 12 |
| config-validation | Messages without a section | `config_validate_test.go: TestValidateConfig_MessageWithoutSection` | unit, table | 12 |
| config-validation | The injector given to ValidateConfig | `config_validate_test.go: TestValidateConfig_Injector` | unit (not parallel) | 12 |
| config-validation | Invalid values to validate | `config_validate_test.go: TestValidateConfig_InvalidTarget` | unit, table | 12 |
| config-validation | Validator panics | `config_validate_test.go: TestValidate_PanicRecovered` | unit, table | 12 |
| config-validation | Concurrent binds validate under -race | `config_validate_test.go: TestValidate_Concurrent` | race | 12 |
| container-construction | Construction outcome | `root_scope_test.go: TestNewWithOpts_ConstructionOutcome` | unit, table | 5 |
| container-construction | Options unchanged after construction | `root_scope_test.go: TestNewWithOpts_OptionsNotWritten` | unit, table | 5 |
| container-construction | The root and the caller's options stay apart | `root_scope_test.go: TestNewWithOpts_OptionsNotAliased` | unit, table | 5 |
| container-construction | Reusing one options value | `root_scope_test.go: TestNewWithOpts_ReuseOptions` | unit, table | 5 |
| container-construction | New is unchanged | `root_scope_test.go: TestNew_Unchanged` | unit (passes on arrival) | 5 |
| container-construction | Context values and nil | `root_scope_test.go: TestNewWithOpts_ContextValues` | unit, table | 5 |
| container-construction | Cancelling construction | `root_scope_test.go: TestNewWithOpts_Cancel` | unit, table | 5 |
| container-construction | An already cancelled context | `root_scope_test.go: TestNewWithOpts_AlreadyCancelled` | unit, table | 5 |
| container-construction | Load happens before the packages | `root_scope_test.go: TestNewWithOpts_LoadBeforePackages` | unit, table | 5 |
| container-construction | A failed construction runs no package | `root_scope_test.go: TestNewWithOpts_FailureRunsNoPackage` | unit, table | 5 |
| container-construction | Cloning with options | `root_scope_test.go: TestCloneWithOpts_Outcome` | unit, table | 5 |
| container-construction | Clone of a root with config | `root_scope_test.go: TestClone_SharesConfig` | unit, table | 5 |
| container-construction | Must constructors | `root_scope_test.go: TestMustConstructors` | unit, table | 5 |
| container-construction | Registering on the default root | `root_scope_test.go: TestDefaultRootScope_Usable` | unit (not parallel, unique names) | 5 |
| container-construction | The default root reads the environment | `root_scope_test.go: TestDefaultRootScope_ReadsEnv` | unit (t.Setenv, not parallel) | 5 |
| container-construction | Duplicate registration in a package | `root_scope_test.go: TestNew_PackagePanicPropagates` | unit, table | 5 |
| extension-properties | Invalid keys are not looked up | `properties_test.go: TestProperties_InvalidKeys` | unit (existing, extended) | 2 |
| extension-properties | Source selection | `properties_test.go: TestPropertySource_Selection` | unit (existing, rewritten) | 5 |
| extension-properties | A source registered after the first read is not used | `properties_test.go: TestPropertySource_LateServiceIgnored` | unit (existing, variable renamed) | 3 |
| extension-properties | Registering the interface name after the first read panics | `properties_test.go: TestPropertySource_LateSameNamePanics` | unit (existing, passes on arrival) | 5 |
| extension-properties | An installed config source is decided before any package runs | `properties_test.go: TestPropertySource_ConfigSourceAtConstruction` | unit, table | 6 |
| extension-properties | Index and map-key suffixes | `properties_test.go: TestProperties_KeySuffixes` | unit, table | 2 |
| extension-properties | Key to variable mapping | `properties_test.go: TestEnvPropertySource_Mapping` | unit (existing, rewritten; golden table) | 3 |
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
| health-check-execution | New starts no goroutine | `health_run_test.go: TestNew_NoHealthGoroutine` | goleak (existing, extended; new rows pass on arrival) | 7 |
| module-identity | Workspace module paths mirror their directories | `module_identity_test.go: TestModuleIdentity_WorkspaceModules` | unit (existing, row added) | 1 |
| toolchain-support | Module go directives declare 1.27 | `toolchain_test.go: TestToolchain_GoDirective` | unit (existing, row added) | 1 |
