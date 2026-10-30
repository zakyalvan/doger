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

The discussion record is the Claude Doc "doger/config: Config as a First-Class Component". It holds decisions D1–D45 with options and reasons. D33–D41 came from the first round of adversarial audits of this change, D42–D45 from the second; the second-round decisions were themselves audited for idiomatic Go before being applied. `unify-constructor` (one `doger.New(opts ...Option)`) is a separate, later change.

## What Changes

**Constructors**
- **BREAKING:** `NewWithOpts(opts, packages...)` and `(*RootScope).CloneWithOpts(opts)` now return `(*RootScope, error)`. On error the root is nil and nothing is left behind.
- New: `NewWithOptsContext(ctx, opts, packages...)` and `(*RootScope).CloneWithOptsContext(ctx, opts)` let the caller bound or cancel a slow `Load`. A nil ctx means `context.Background()`.
- New: `MustNewWithOpts` and `(*RootScope).MustCloneWithOpts`, for tests and examples, panic with the construction error itself.
- `New(packages...)`, `Clone()` and `DefaultRootScope` are unchanged: none can fail.
- **BEHAVIOR CHANGE (flagged):** no constructor writes to the caller's `*InjectorOpts` any more. Defaults are applied to a copy, so one options value can be reused after a failure. Previously `Logf`, `Clock` and empty hook lists were written back; no repository code relies on that.

**The contract, in the root module**
- `ConfigSource` = `PropertySource` + `PropertyKeys` + `Load(ctx) error`. Optional interfaces, detected by type assertion: `ConfigLogger` and `ConfigWatcher`. Every signature uses stdlib types only, so the config module never imports doger outside its tests.
- `InjectorOpts.ConfigSource` installs a source. The constructor then, in order:
  1. checks the options, before calling any method;
  2. makes the source the container's property source;
  3. calls `SetLogf` (only when the caller set `Logf`);
  4. calls `Load` once, under the caller's context and `ConfigTimeout` (10s by default; the earlier deadline wins; a user `Clock` bounds it in its own time base);
  5. runs the packages.
- Option errors:
  - a typed-nil source (nil pointer, map, func, chan or slice) fails with `ErrInvalidConfigSource`;
  - an `EnvPropertySource` in `InjectorOpts.PropertySource` with an invalid prefix fails with `ErrInvalidConfigSource`;
  - setting both `PropertySource` and `ConfigSource` fails with `ErrConfigConflict`.
- A failed or panicking `Load` returns a `*ConfigLoadError` matching `ErrConfigLoadFailed`, with the cause reachable through `errors.Is`/`errors.As`.
- **Readable, secret-free errors:** an error that implements `ValueFreeError` (`interface{ error; ValueFree() }`) promises its text holds no property value, so the root shows it: `DI: config: load failed: source *config.Source: config.yaml:3: syntax error`. Context errors show their text too. Every other cause is shown by type only, and every error type's `%#v` is type-only.
- A source serves one container and its clones. A clone shares the parent's source only when given the same pointer. This is documented; there is no global registry.
- `PropertySource` is unchanged. The new optional `PropertyKeys` (`Keys(prefix)`) lets lists, maps and pointers bind. `EnvPropertySource`, `MapSource` and extension `Properties` implement it.

**Key names**
- **BREAKING for env names:** `EnvPropertySource` follows Spring Boot's mapping: `.` becomes `_`, `-` is removed, everything is uppercased, and an instance is marked by `__`.
  - `redis.pool-size` reads `DOGER_REDIS_POOLSIZE`.
  - `redis/queue.addr` reads `DOGER_REDIS__QUEUE_ADDR`.
  - Distinct keys may share a variable, as in Spring. An old dashed extension name such as `DOGER_EXAMPLE__KV_ADDR` is now read as instance `kv` of extension `example`; the upgrading note warns about it.
- The key grammar gains `[i]` (list index) and `[key]` (map key) segments. In uniform form `[k]` with `k` in `[a-z0-9]+` equals `.k`, so a file's `[team]` and an env `..._TEAM` are one key and layer precedence decides.
- One prefix rule for both modules: `-` and `.` become `_`, the result is uppercased, and `_` is appended when missing; `""` turns the prefix off.

**Binding**
- The binder is driven by the target struct and lives in the root.
  - It asks the source for each field's canonical kebab key, by a pinned splitting rule (`OAuth2Token` → `oauth2-token`, `HTTPServerURL` → `http-server-url`), and writes only the fields a source has.
  - Prefixes, layer merges and `Keys` compare in uniform form, so multi-word fields bind from env too.
  - A `cfg` tag overrides a name; the tag key is configurable.
  - Types whose pointer implements `encoding.TextUnmarshaler` (`net.IP`, `time.Time`) convert as scalars.
  - Lists replace whole, maps merge by key, and pointers are allocated only when a key exists under them.
  - Recursive types, huge indices and non-ASCII field names are rejected.
  - Errors name keys and types, never values.
- Apps:
  - `BindConfig(i, prefix, defaults T)` and `BindConfigNamed(i, name, prefix, defaults T)` bind and validate at the call, then register `T` and a live `ConfigValue[T]` handle. `T` is always inferred.
  - `ResolveConfig(i, prefix, defaults T)` binds onto a consumer's own defaults and registers nothing; it works in every phase.
  - `StaticConfig` builds a handle for tests.
- Extensions: `BindProperties` and `ValidateConfig`.

**Validation**
- go-playground/validator is the root's default, so `validate:` tags are checked in every container. Building the validator is lazy; the dependency itself is paid on import.
- A type's own `Validate() error` also runs.
- `InjectorOpts.ConfigValidator` replaces go-playground; it is the only override.
- Errors list keys and rules; an author's error text is shown only when it implements `ValueFreeError`, otherwise by type.

**The config module, `github.com/kartaladev/doger/config`**
- `config.Files(opts...)` searches `.` and `./config` for `config.{yaml,yml,toml,json}`. It overlays `config.<profile>.<ext>` (profile from `APP_PROFILE`), then `./.env` as a layer, then process env under `APP_`.
- `.env` is never written to the process environment unless `DotEnvExport()` is set. Even then, only unset variables are exported, once.
- Load errors read `<file>:<line>: <category>`, never parser text or values, and implement `ValueFreeError`.
- Symlinks are followed (Docker and Kubernetes secrets keep working); profile names are restricted, so no path escapes the config directories. File size, key depth and key count are capped.
- Options: `Dir`, `File`, `Name`, `Format`, `Codec`, `Profile`, `ProfileEnv`, `EnvPrefix`, `EnvMapper`, `DotEnv`, `DotEnvExport`, `Layer`, `LayerOrder`, `Logger`, `DisableWatch`, `PollInterval`, `StageTimeout`, `MaxFileSize`. Its exported types (`FileFormat`, `Decoder`, `LayerSource`, `LayerName`, `ParseError`, `ParseCategory`, `FileError`, `LayerError`) and constants (`DefaultPollInterval`, `DefaultStageTimeout`) are declared in design.md.
- `Load` while watching fails with `ErrWatching`.
- Its non-test code imports only `go.yaml.in/yaml/v3`, `github.com/pelletier/go-toml/v2` and the stdlib.

**Reload (P2)**
- The source stages a parsed snapshot and calls `changed(ctx, next, err)`. The root rebinds and validates every section with no lock held around user code, then commits the snapshot as the view every reader sees, or keeps the last good one and warns. No reader ever mixes old and new values.
- Subscribers (`ConfigValue[T].OnChange`) and `HookConfigReloadFailed` hooks run on a dispatcher goroutine. To shut down from one of them, call `go root.Shutdown()`; a synchronous call waits on itself, as doger already documents for Starters. Panics in them go through `HookContainerPanic`.
- `config.Files` polls a content hash every 10s, follows Kubernetes ConfigMap symlink swaps, stages only after two identical polls, and warns once per rejected content.
- The watcher runs between the first successful `Start` and the last `Shutdown` of a root and its clones.

**Dependencies:**
- Root module: `github.com/go-playground/validator/v10` and its 7 modules (D7). Every doger consumer pays this in its module graph, `go.sum` and binary size, whether or not it uses config; only building the validator is lazy.
- Tooling: govulncheck pinned in a separate `tools/go.mod`, run in every module; it adds nothing to the root `go.mod`.

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
- `container-construction`: `NewWithOpts`, `NewWithOptsContext`, `CloneWithOpts` and `CloneWithOptsContext` returning errors; the Must variants; options never written; `New`, `Clone` and `DefaultRootScope` unchanged; package order and panics.
- `config-sources`: the `ConfigSource` contract and its optional interfaces; installing a source; `Load` under a context, timeout and user clock; option checks; `ConfigLoadError` and `ValueFreeError`; clone sharing; the secrecy of values.
- `config-binding`: the target-driven binder and its rules, `BindConfig`, `BindConfigNamed`, `ConfigValue[T]`, `StaticConfig`, `ResolveConfig` and `BindProperties`.
- `config-validation`: the go-playground default, `Validate() error`, `InjectorOpts.ConfigValidator`, `ValidateConfig`, and the shape of validation errors.
- `config-files`: the `config` module's `config.Files` source, covering discovery, formats, profiles, layers, `.env`, env, options, caps and load errors.
- `config-reload`: the `ConfigWatcher` contract, root-owned commit, dispatch of notifications and hooks, debounced polling, and the watcher's lifecycle with clones.

### Modified Capabilities
- `extension-properties`:
  - index and map-key segments in the key grammar;
  - the Spring env mapping with `__` for instances;
  - the optional `PropertyKeys`, with `MapSource`;
  - one prefix rule;
  - an installed `ConfigSource` as the property source.
- `health-check-execution`: "Creating a container starts no goroutine" is narrowed to goroutines that outlive the constructor, and covers the new constructors, because `Load` may run goroutines while the constructor runs.
- `module-identity`: the workspace gains the `config` module, and the `scheduler/gocron` row missing from the table is added, so the table again lists every workspace entry (27).
- `toolchain-support`: the `config` and `scheduler/gocron` modules declare the same Go version as every other module.

## Impact

- **Root module code:**
  - `root_scope.go`: constructors and clone, working on a copy of the options.
  - `injector.go`: `InjectorOpts` fields `ConfigSource`, `ConfigTimeout`, `ConfigValidator`, `ConfigTagKey`, `HookConfigReloadFailed`; `copy()` covers them.
  - `properties.go`: grammar, env mapping, `PropertyKeys`, `MapSource`.
  - New files: the contract, `ConfigLoadError` and `ValueFreeError`, the binder, typed binding, validation, and reload wiring.
- **Breaking API (flagged):**
  - The `NewWithOpts` and `CloneWithOpts` signatures. Migration: `root, err := doger.NewWithOpts(o)`, or `doger.MustNewWithOpts(o)` in tests; `clone, err := root.CloneWithOpts(o)`, or `root.MustCloneWithOpts(o)`.
  - `EnvPropertySource` variable names and prefix normalization. Migration: rename, for example `DOGER_REDIS_POOL__SIZE` to `DOGER_REDIS_POOLSIZE`; check variables of dashed extension names.

  No version is tagged and no app is deployed. `New(...)` and `Clone()` call sites are untouched.
- **Call sites:** about 193 `NewWithOpts` calls and 9 `CloneWithOpts` calls in code and tests. Also affected: `scheduler/gocron`, `http/*`, `examples/*`, and docs pages and README snippets that use them.
- **New module:** `config/`, added to `go.work`. Like `scheduler/gocron`, its `go.mod` requires doger with `replace github.com/kartaladev/doger => ../`. CI tests and lints it, including a `GOWORK=off` job that checks `go mod tidy -diff`. The release workflow drops its tests, tidies it, pins doger only if still required, and tags `config/vX.Y.Z` with the other tags. Its tests import both modules; the root never imports it.
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
| Config source | none: env only, via `EnvPropertySource("DOGER_")` | `InjectorOpts.ConfigSource` (`config.Files(...)` or any `ConfigSource`); `InjectorOpts.PropertySource` for a bare lookup |
| Source sharing | one source per container and its clones; a clone shares only the same pointer (documented, not policed) | `Clone` / `CloneWithOpts` with the same pointer share it; any other source is loaded |
| Construction errors | returned by `NewWithOpts` / `CloneWithOpts` with a nil root | `MustNewWithOpts` / `MustCloneWithOpts` panic instead |
| Caller's options | never written | – (reuse one value freely) |
| Load bound | 10s (`DefaultConfigTimeout`); the earlier of it and the caller's deadline | `ConfigTimeout` (negative = none); `NewWithOptsContext` / `CloneWithOptsContext`; `InjectorOpts.Clock` |
| Load error text | the cause's type; value-free and context errors in full | implement `ValueFreeError` on your source's errors |
| File locations and name | `.` then `./config`, base name `config` | `config.Dir`, `config.File`, `config.Name` |
| Formats | YAML, TOML, JSON by extension; two formats with one name fail | `config.Format`, `config.Codec` |
| File size and shape | 4 MiB per file; key depth 64; 100,000 keys | `config.MaxFileSize` |
| Profile | from `APP_PROFILE`; unset loads base files only; a set profile with no file fails | `config.Profile`, `config.ProfileEnv` |
| Precedence | added layers, env, `.env`, profile file, base file, struct defaults | `config.LayerOrder`, `config.Layer` |
| Env prefix | `APP_` for `config.Files`, `DOGER_` without a source; one normalization rule | `config.EnvPrefix` (`""` = off), `EnvPropertySource(prefix)`, `config.EnvMapper` |
| `.env` | `./.env` as a layer, never written to the process env | `config.DotEnv(path)`, `config.DotEnv("")`, `config.DotEnvExport()` (unset vars only) |
| Struct defaults | zero values | a prefilled struct: the `defaults` argument of `BindConfig` / `ResolveConfig`, `DefaultConfig()` (an extension's own function) before `BindProperties` |
| Key grammar and names | kebab keys with `[i]` and `[key]`; field names by the pinned splitting rule | a `cfg` tag on the field; `InjectorOpts.ConfigTagKey` |
| A list from one string | a single value is comma-split | a named type with `UnmarshalText`, or indexed keys |
| Validation | go-playground in every container, plus `Validate() error` | `InjectorOpts.ConfigValidator` |
| Watching | on, polling every 10s once the container starts; stages after two identical polls | `config.DisableWatch()`, `config.PollInterval(d)`, `config.StageTimeout(d)` |
| Change notification | only sections whose bound keys changed | `ConfigValue[T].OnChange`; `stop` to unsubscribe |
| Failed reload | keep the last good config, warn once with keys only | `HookConfigReloadFailed` / `AddConfigReloadFailedHook` |
| Callback panics | logged by type | `HookContainerPanic` (points `config-watch`, `config-reload-failed`, `config-on-change`) |
| Logging | `InjectorOpts.Logf` when set, else `slog.Default()` for warnings; a source logs to `slog.Default()` | `InjectorOpts.Logf`, `config.Logger(l)` |
| Secrecy | values never logged or put in errors; `%#v` of error types is type-only | none: a deliberate guarantee, as in `extension-properties` |
