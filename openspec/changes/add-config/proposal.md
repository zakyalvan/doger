# Proposal

## Why

Applications built on doger need typed, validated configuration, and extensions need more than single string lookups. Today the only option is golang-lib/config, which wraps viper. Throwaway spikes against it reproduced its problems:
- it pulls in 34 modules;
- its watcher leaks goroutines;
- it deadlocks when a callback registers another watcher;
- it races between reload and reads;
- it ignores env vars for keys the file lacks;
- it writes `.env` values into the process environment.

This change makes config a first-class doger component with its own core:
- a `ConfigSource` contract, a binder and validation in the root module;
- a `config` module that loads files and env without viper.

The ROADMAP item "Configuration validation at start" is part of it.

The discussion record is the Claude Doc "doger/config: Config as a First-Class Component". It holds decisions D1–D68 with options and reasons. D33–D41 came from the first round of adversarial audits of this change, D42–D45 from the second, and D46–D68 from the third; each round's decisions were themselves audited for idiomatic Go before being applied. `unify-constructor` (one `doger.New(opts ...Option)`) is a separate, later change that owns the final constructor shape; no version is tagged between the two changes.

## What Changes

**Constructors**
- **BREAKING:** `NewWithOpts(ctx, opts, packages...)` and `(*RootScope).CloneWithOpts(ctx, opts)` take the caller's context first and return `(*RootScope, error)`. The context bounds and cancels a slow `Load` and is never stored; a nil ctx means `context.Background()`. On error the root is nil and nothing is left behind. There is no separate context-taking constructor: these signatures break anyway, so the ctx joins the same break (D55), as `config.LoadDefaultConfig(ctx, ...)` and `pgxpool.New(ctx, ...)` take theirs.
- New: `MustNewWithOpts(ctx, opts, packages...)` and `(*RootScope).MustCloneWithOpts(ctx, opts)`, for tests and examples, panic with the construction error itself.
- `New(packages...)`, `Clone()` and `DefaultRootScope` are unchanged: none can fail. `New` equals `NewWithOpts(context.Background(), nil, packages...)`; `Clone()` always shares the parent's config, whatever the source's kind (D54).
- **BEHAVIOR CHANGE (flagged):** no constructor writes to the caller's `*InjectorOpts` any more, and the root keeps its own copy (D65):
  - defaults are applied to the copy, so one options value can be reused after a failure; previously `Logf`, `Clock` and empty hook lists were written back;
  - edits the caller makes to the struct after construction no longer reach the root;
  - `root.Add*Hook` appends to the root's copy, no longer into the caller's struct.

  No repository code relies on the old aliasing; scenarios assert `reflect.DeepEqual(before, *opts)` after every constructor.

**The contract, in the root module**
- `ConfigSource` = `PropertySource` + `PropertyKeys` + `Load(ctx) error`; `ConfigView` is the alias for `PropertySource` + `PropertyKeys`. Optional interfaces, detected by type assertion: `LogfSetter` and `ConfigWatcher`. Every signature uses stdlib types only, so the config module never imports doger outside its tests. Only the root calls `Load` on an installed source.
- `InjectorOpts.ConfigSource` installs a source. The constructor then, in order:
  1. checks the options, before calling any method;
  2. makes the source the container's property source;
  3. calls `SetLogf` (only when the caller set `Logf`);
  4. calls `Load` once, under the caller's context and `ConfigTimeout` (10s by default; the earlier deadline wins). A user `Clock`, detected by type so a caller-supplied real clock still counts as real, bounds `Done`/`Err` in its own time base, while `Deadline()` reports only the caller's deadline, so stdlib children still fire (D52);
  5. logs "DI: injector created";
  6. runs the packages.
- Option errors:
  - a typed-nil source (nil pointer, map, func, chan or slice) fails with `ErrInvalidConfigSource`;
  - an `EnvPropertySource` in `InjectorOpts.PropertySource` with an invalid prefix fails with `ErrInvalidConfigSource`;
  - setting both `PropertySource` and `ConfigSource` fails with `ErrConfigConflict`;
  - an invalid `ConfigTagKey` or a typed-nil `ConfigValidator` fails with `ErrInvalidConfigOption` (D67).
- A failed or panicking `Load` returns a `*ConfigLoadError` matching `ErrConfigLoadFailed`, with the cause reachable through `errors.Is`/`errors.As`.
- **Readable, secret-free errors:** an error whose `ValueFree() bool` (`ValueFreeError`) reports true promises its text holds no property value, so the root shows it: `DI: config: load failed: source *config.Source: config.yaml:3: syntax error`. Context errors show their text and win over other value-free errors. Every other cause is shown by type only, and every error type's `%#v` is type-only. The marker is a predicate, like `net.Error.Timeout()`, so a wrapper answers per instance. Only the config sentinels and `*ConfigLoadError`, `*ConfigBindError` and `*ConfigValidationError` implement it, with nil-safe pointer receivers; `StartError`, `ExtensionError` and `ShutdownReport` keep their texts (D57).
- A source serves one container and its clones. `Clone()` always shares; `CloneWithOpts` shares only when given the same pointer, after its option checks. This is documented; there is no global registry.
- `PropertySource` is unchanged. The new optional `PropertyKeys` (`Keys(prefix)`) lets lists, maps and pointers bind. `EnvPropertySource`, `MapSource` and extension `Properties` implement it.

**Key names**
- **BREAKING for env names:** `EnvPropertySource` follows Spring Boot's mapping: `.` becomes `_`, `-` is removed, everything is uppercased, and an instance is marked by `__`.
  - `redis.pool-size` reads `DOGER_REDIS_POOLSIZE`.
  - `redis/queue.addr` reads `DOGER_REDIS__QUEUE_ADDR`.
  - Distinct keys may share a variable, as in Spring. An old dashed extension name such as `DOGER_EXAMPLE__KV_ADDR` is now read as instance `kv` of extension `example`; the upgrading note warns about it.
- The key grammar gains `[i]` (list index) and `[key]` (map key) segments. In uniform form `[k]` with `k` in `[a-z0-9]+` equals `.k`, so a file's `[team]` and an env `..._TEAM` are one key and layer precedence decides. The bracket form is canonical (`labels[team]`, `hosts[0]`), also in error keys (D51).
- One prefix rule for both modules: `-` and `.` become `_`, the result is uppercased, and `_` is appended when missing. The root's `EnvPropertySource("")` keeps meaning "no prefix", because it looks keys up one by one; `config.EnvPrefix("")` is an invalid option, because the config source enumerates its prefix and would read `PATH`, `HOME` and every secret (D66).

**Binding**
- The binder is driven by the target struct and lives in the root.
  - It asks the source for each field's canonical kebab key, by a pinned splitting rule (`OAuth2Token` → `oauth2-token`, `HTTPServerURL` → `http-server-url`), and writes only the fields a source has. `Keys` is asked per field; a scalar field with no listed key is also looked up directly, so lookup-only layers such as `config.EnvMapper` reach the binder (D61).
  - Prefixes, layer merges and `Keys` compare in uniform form, so multi-word fields bind from env too.
  - A `cfg` tag overrides a name; the tag key is configurable.
  - Types whose pointer implements `encoding.TextUnmarshaler` (`net.IP`, `time.Time`) convert as scalars.
  - Lists replace whole, maps merge by key, and pointers are allocated only when a key exists under them.
  - Recursive types, huge indices and non-ASCII field names are rejected.
  - Errors name keys and types, never values.
- Apps:
  - `BindConfig(i, prefix, defaults T) (ConfigValue[T], error)` and `BindConfigNamed(i, name, prefix, defaults T) (ConfigValue[T], error)` bind and validate at the call, then register `T` and a live `ConfigValue[T]` handle, which they also return. `T` is always inferred. A reload that commits during the bind is never lost: the bind commits only against the view it bound from, else rebinds (D46).
  - `ResolveConfig(i, prefix, defaults T)` binds onto a consumer's own defaults and registers nothing; it works in every phase.
  - `StaticConfig` builds a handle for tests.
- Extensions: `BindProperties` and `ValidateConfig`.

**Validation**
- go-playground/validator is the root's default, so `validate:` tags are checked in every container. Building the validator is lazy; the dependency itself is paid on import.
- A type's own `Validate() error` also runs, on a copy: changes it makes to top-level fields are discarded.
- `InjectorOpts.ConfigValidator` replaces go-playground; it is the only override.
- Every validation failure is one `*ConfigValidationError{Section, Type, Violations, Err}` (D56). It lists keys and rules; an author's error text is shown only when it is value-free, otherwise by type.
- Malformed `validate` tags fail at bind time with `ErrInvalidConfigTarget`, before any value is validated (D68).

**The config module, `github.com/kartaladev/doger/config`**
- `config.New(opts...)` returns the module's one source type, `*config.Source`. It searches `.` and `./config` for `config.{yaml,yml,toml,json}`. It overlays `config.<profile>.<ext>` (profile from `APP_PROFILE`), then `./.env` as a layer, then process env under `APP_`.
- `.env` is never written to the process environment unless `DotEnvExport()` is set. Even then, only unset variables are exported, once.
- Load errors read `<file>:<line>: <category>`, never parser text or values, and are value-free. Option, profile and two-format errors carry value-free detail (`*OptionError`, `*ProfileError`, `*AmbiguousFormatError`); an unknown format is `ErrUnknownFormat` (D59).
- Symlinks are followed (Docker and Kubernetes secrets keep working); profile names are restricted, so no path escapes the config directories. A file is opened non-blocking and must be a regular file on the opened descriptor, so a FIFO can never hang `Load` (D62).
- Hostile files are bounded linearly: file size (1 MiB by default), key count (10,000 per file by default) and depth (64, fixed) are enforced before the document is converted to values, for YAML, TOML and JSON alike (D50).
- Options: `Dir`, `File`, `Name`, `Format`, `Decoder`, `Profile`, `ProfileEnv`, `EnvPrefix`, `EnvMapper`, `DotEnv`, `DotEnvExport`, `Layer`, `LayerOrder`, `LayerRefresh`, `Logger`, `DisableWatch`, `PollInterval`, `StageTimeout`, `MaxFileSize`, `MaxKeys`. `Dir`, `File` and `LayerOrder` called with no argument, and `EnvPrefix("")`, are `ErrInvalidOption`. Its exported types (`FileFormat`, `DecodeFunc`, `LayerSource` with `LoadLayer(ctx)`, `LayerName`, `ParseError`, the typed `ParseCategory` constants, `FileError`, `LayerError`, `OptionError`, `ProfileError`, `AmbiguousFormatError`) and constants (`DefaultPollInterval`, `DefaultStageTimeout`, `DefaultLayerRefresh`, `DefaultMaxFileSize`, `DefaultMaxKeys`) are declared in design.md.
- `Load` while watching fails with `ErrWatching`.
- Its non-test code imports only `go.yaml.in/yaml/v3`, `github.com/pelletier/go-toml/v2` (at least v2.4.3, which bounds nesting; its `unstable` parser does the linear pre-pass) and the stdlib.

**Reload (P2)**
- The source stages a parsed snapshot and calls `changed(ctx, next, err)`; calls never overlap, and a nil view is a staging failure. The root rebinds and validates every bound section, any of which can veto, with no lock held around user code, then commits the snapshot as the view every reader sees, or keeps the last good one and warns. No reader ever mixes old and new values (D47, D63).
- A section stays bound while one of its registered `ConfigValue[T]` handles does; it is released when the last one is removed or overridden, or its scope shuts down, with `runtime.AddCleanup` as a fallback. Two caveats are documented: an `OnChange` subscriber that captures a clone keeps it alive, and a clone dropped without `Shutdown` keeps vetoing until it is garbage-collected.
- Subscribers (`ConfigValue[T].OnChange`) are notified only when the raw key/value set under their prefix changed. They and `HookConfigReloadFailed` hooks (`func(root *RootScope, err error)`, with no stored context, like the async health hooks; D53) run on a dispatcher goroutine. To shut down from one of them, call `go root.Shutdown()`; a synchronous call waits on itself, as doger already documents for Starters. Panics in them go through `HookContainerPanic`.
- `config.New` polls a content hash every 10s, follows Kubernetes ConfigMap symlink swaps, stages only after two identical polls, and warns once per rejected content. Added layers are re-read when due, every minute by default (`config.LayerRefresh`), and folded into the fingerprint (D60).
- The watcher runs between the first successful `Start` and the last `Shutdown` of a root and its clones.

**Dependencies:**
- Root module: `github.com/go-playground/validator/v10` and its 7 modules, plus go-playground/assert in the module graph (D7). Every doger consumer pays this in its module graph, `go.sum` and binary size, whether or not it uses config; only building the validator is lazy.
- Tooling: govulncheck pinned in a separate `tools/go.mod` outside the workspace, built once with `GOWORK=off go -C tools build` and run with `GOWORK=off` in every module, so each is scanned as a consumer builds it (D48); it adds nothing to the root `go.mod`. dependabot and trivy cover `tools/`.

**Out of scope:**
- the option-based `doger.New(opts ...Option)`;
- packages that return errors;
- rebuilding services when config changes;
- gin integration;
- a `SetDefaults()` method;
- `ConfigPrefix()` sugar;
- legacy env names.

golang-lib/config is not changed.

## Capabilities

### New Capabilities
- `container-construction`: `NewWithOpts` and `CloneWithOpts` taking a context and returning errors; the Must variants; options never written or aliased; `New`, `Clone` and `DefaultRootScope` unchanged; package order and panics.
- `config-sources`: the `ConfigSource` contract, `ConfigView` and the optional interfaces; installing a source; `Load` under a context, timeout and user clock; option checks; `ConfigLoadError` and `ValueFreeError`; clone sharing; the secrecy of values.
- `config-binding`: the target-driven binder and its rules, `BindConfig`, `BindConfigNamed`, `ConfigValue[T]`, `StaticConfig`, `ResolveConfig` and `BindProperties`.
- `config-validation`: the go-playground default, `Validate() error`, `InjectorOpts.ConfigValidator`, `ValidateConfig`, and the one shape of validation errors.
- `config-files`: the `config` module's `config.New` source, covering discovery, formats, profiles, layers, `.env`, env, options, caps and load errors.
- `config-reload`: the `ConfigWatcher` contract, root-owned commit, dispatch of notifications and hooks, debounced polling, and the watcher's lifecycle with clones.

### Modified Capabilities
- `extension-properties`:
  - index and map-key segments in the key grammar;
  - the Spring env mapping with `__` for instances;
  - the optional `PropertyKeys`, with `MapSource`;
  - one prefix rule;
  - an installed `ConfigSource` as the property source.
- `health-check-execution`: "Creating a container starts no goroutine" is RENAMED to "Creating a container leaves no goroutine running" and narrowed to goroutines the library starts that outlive the constructor, for a parent context from the context package, because `Load` may run goroutines while the constructor runs. It covers the Must constructors.
- `module-identity`: the workspace gains the `config` module, and the `scheduler/gocron` row missing from the table is added, so the table again lists every workspace entry (27).
- `toolchain-support`: the `config`, `scheduler/gocron` and `tools` modules declare the same Go version as every other module.

## Impact

- **Root module code:**
  - `root_scope.go`: constructors and clone, working on a copy of the options.
  - `injector.go`: `InjectorOpts` fields `ConfigSource`, `ConfigTimeout`, `ConfigValidator`, `ConfigTagKey`, `HookConfigReloadFailed`; `copy()` covers them, and a reflection test checks it covers every field.
  - `properties.go`: grammar, env mapping, `PropertyKeys`, `MapSource`.
  - New files: the contract, `ConfigLoadError` and `ValueFreeError`, the binder, typed binding, validation, and reload wiring.
- **Breaking API (flagged):**
  - The `NewWithOpts` and `CloneWithOpts` signatures: a ctx first parameter and an error result. Migration: `root, err := doger.NewWithOpts(ctx, o)`, or `doger.MustNewWithOpts(ctx, o)` in tests; `clone, err := root.CloneWithOpts(ctx, o)`, or `root.MustCloneWithOpts(ctx, o)`. Use `context.Background()` where no context is at hand.
  - The options copy (BEHAVIOR CHANGE): later edits to `*InjectorOpts`, and `root.Add*Hook`, no longer reach the caller's struct or the root through it.
  - `EnvPropertySource` variable names and prefix normalization. Migration: rename, for example `DOGER_REDIS_POOL__SIZE` to `DOGER_REDIS_POOLSIZE`; check variables of dashed extension names.

  No version is tagged and no app is deployed, and none is tagged before `unify-constructor` lands. `New(...)` and `Clone()` call sites are untouched.
- **Call sites:** about 193 `NewWithOpts` calls and 9 `CloneWithOpts` calls in code and tests. Also affected: `scheduler/gocron`, `examples/*`, and docs pages and README snippets that use them. The `http/*` adapters call neither.
- **New module:** `config/`, added to `go.work`. Like `scheduler/gocron`, its `go.mod` requires doger with `replace github.com/kartaladev/doger => ../`. CI tests and lints it, including a `GOWORK=off` job that checks `go mod tidy -diff`, and a footprint check over its dependency module paths, excluding its own. The release workflow drops its tests, tidies it, pins doger only if `go -C config mod edit -json` still lists it in `Require` (D49), and tags `config/vX.Y.Z` with the other tags. Its tests import both modules; the root never imports it.
- **Repo:** govulncheck re-enabled, per module, from `tools/go.mod`; `.trivyignore` entries per CVE with an expiry and a reason.
- **Specs:** existing scenarios that construct roots still read the same. The `health-check-execution`, `module-identity` and `toolchain-support` deltas are listed above.
- **Docs:**
  - a config guide;
  - `Example*` tests for zero config and overrides;
  - an upgrading note for both breaks and the options behavior change;
  - the ROADMAP item marked done.

## Defaults & Control

| Behavior | Zero-config default | Consumer control |
|---|---|---|
| Config source | none: env only, via `EnvPropertySource("DOGER_")` | `InjectorOpts.ConfigSource` (`config.New(...)` or any `ConfigSource`); `InjectorOpts.PropertySource` for a bare lookup |
| Source sharing | one source per container and its clones; `Clone()` always shares; `CloneWithOpts` shares only the same pointer (documented, not policed) | `CloneWithOpts` with the same pointer shares it; any other source is loaded |
| Construction errors | returned by `NewWithOpts` / `CloneWithOpts` with a nil root | `MustNewWithOpts` / `MustCloneWithOpts` panic instead |
| Caller's options | never written, never aliased: the root keeps its own copy | – (reuse one value freely) |
| Load bound | 10s (`DefaultConfigTimeout`); the earlier of it and the caller's deadline | `ConfigTimeout` (negative = none); the `ctx` of `NewWithOpts` / `CloneWithOpts`; `InjectorOpts.Clock` |
| Load error text | the cause's type; value-free and context errors in full | implement `ValueFreeError` on your source's errors |
| File locations and name | `.` then `./config`, base name `config` | `config.Dir`, `config.File`, `config.Name` |
| Formats | YAML, TOML, JSON by extension; two formats with one name fail | `config.Format`, `config.Decoder(ext, fn)` |
| File size and shape | 1 MiB per file; 10,000 keys per file; key depth 64 (fixed: it bounds key length and recursion) | `config.MaxFileSize`, `config.MaxKeys` |
| Profile | from `APP_PROFILE`; unset loads base files only; a set profile with no file fails | `config.Profile`, `config.ProfileEnv` |
| Precedence | added layers, env, `.env`, profile file, base file, struct defaults | `config.LayerOrder` (leave out `LayerEnv` to turn env off), `config.Layer` |
| Added layers | re-read every minute (`DefaultLayerRefresh`) while watching | `config.LayerRefresh(name, d)` (negative = only at `Load` and on a file change) |
| Env prefix | `APP_` for `config.New`, `DOGER_` without a source; one normalization rule | `config.EnvPrefix` (`""` is invalid), `EnvPropertySource(prefix)` (`""` = no prefix), `config.EnvMapper` |
| `.env` | `./.env` as a layer, never written to the process env | `config.DotEnv(path)`, `config.DotEnv("")`, `config.DotEnvExport()` (unset vars only) |
| Struct defaults | zero values | a prefilled struct: the `defaults` argument of `BindConfig` / `ResolveConfig`, `DefaultConfig()` (an extension's own function) before `BindProperties` |
| Key grammar and names | kebab keys with `[i]` and `[key]`; field names by the pinned splitting rule | a `cfg` tag on the field; `InjectorOpts.ConfigTagKey` |
| A list from one string | a single value is comma-split | a named type with `UnmarshalText`, or indexed keys |
| Validation | go-playground in every container, plus `Validate() error` | `InjectorOpts.ConfigValidator` |
| Watching | on, polling every 10s once the container starts; stages after two identical polls | `config.DisableWatch()`, `config.PollInterval(d)`, `config.StageTimeout(d)` |
| Change notification | only sections whose raw key/value set under their prefix changed | `ConfigValue[T].OnChange`; `stop` to unsubscribe |
| Failed reload | keep the last good config, warn once with keys only | `HookConfigReloadFailed` / `AddConfigReloadFailedHook` (`func(root *RootScope, err error)`) |
| Callback panics | logged by type | `HookContainerPanic` (points `config-watch`, `config-reload-failed`, `config-on-change`) |
| Logging | `InjectorOpts.Logf` when set, else `slog.Default()` for warnings; a source logs to `slog.Default()` | `InjectorOpts.Logf`, `config.Logger(l)` |
| Secrecy | values never logged or put in errors; `%#v` of error types is type-only | none: a deliberate guarantee, as in `extension-properties` |
