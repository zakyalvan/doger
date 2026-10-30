# Proposal

## Why

Applications built on doger need typed, validated configuration, and extensions need more than single string lookups. Today the only option is golang-lib/config, which wraps viper. Five spikes reproduced its problems:
- it pulls in 34 modules;
- it leaks goroutines;
- it deadlocks when a callback registers another watcher;
- it races during reload;
- it ignores env vars for keys the file lacks;
- it writes `.env` values into the process environment.

This change makes config a first-class doger component with its own core:
- a `ConfigSource` contract, a binder and validation in the root module;
- a `config` module that loads files and env without viper.

The ROADMAP item "Configuration validation at start" is part of it.

The discussion record is the Claude Doc "doger/config: Config as a First-Class Component". It holds decisions D1–D41, with options and reasons; D33–D41 came from an adversarial audit of the first draft of this change. `unify-constructor` (one `doger.New(opts ...Option)`) is a separate, later change.

## What Changes

**Constructors**
- **BREAKING:** `NewWithOpts(opts, packages...)` and `(*RootScope).CloneWithOpts(opts)` now return `(*RootScope, error)`. On error the root is nil and nothing is left behind.
- `New(packages...)` and `Clone()` are unchanged: neither can fail.
- New: `NewWithOptsContext(ctx, opts, packages...)` lets the caller cancel or bound a slow `Load`.
- New: `MustNewWithOpts`, for tests and examples, panics with the error.

**The contract, in the root module**
- `ConfigSource` = `PropertySource` + `PropertyKeys` + `Load(ctx) error`. Optional interfaces, detected by type assertion: `ConfigLogger` and `ConfigWatcher`. Every signature uses stdlib types only, so the config module never imports doger outside its tests.
- `InjectorOpts.ConfigSource` installs a source. `NewWithOpts` then, in order:
  1. checks the options;
  2. makes the source the container's property source;
  3. calls `SetLogf` (only when the user set `Logf`);
  4. calls `Load` under `ConfigTimeout` (10s by default);
  5. runs the packages.
- Errors: a typed-nil source fails with `ErrInvalidConfigSource`; setting both `PropertySource` and `ConfigSource` fails with `ErrConfigConflict`.
- A source serves one container and its clones. This is documented; there is no global registry.
- `PropertySource` is unchanged. The new optional `PropertyKeys` (`Keys(prefix)`) lets lists, maps and pointers bind. `EnvPropertySource`, `MapSource` and extension `Properties` implement it.

**Key names**
- **BREAKING for env names:** `EnvPropertySource` follows Spring Boot's mapping: `.` becomes `_`, `-` is removed, everything is uppercased, and an instance is marked by `__`.
  - `redis.pool-size` reads `DOGER_REDIS_POOLSIZE`.
  - `redis/queue.addr` reads `DOGER_REDIS__QUEUE_ADDR`.
  - Distinct keys may share a variable, as in Spring.
- The key grammar gains `[i]` (list index) and `[key]` (map key) segments.
- One prefix rule applies to both modules: uppercase, then append `_` when missing; `""` turns the prefix off.

**Binding**
- The binder is driven by the target struct and lives in the root.
  - It asks the source for each field's canonical kebab key and writes only the fields a source has.
  - Prefixes, layer merges and `Keys` compare in a uniform form, so multi-word fields bind from env too.
  - A `cfg` tag overrides a name; the tag key is configurable.
  - Types whose pointer implements `encoding.TextUnmarshaler` (`net.IP`, `time.Time`) convert as scalars.
  - Lists replace whole, maps merge by key, and pointers are allocated only when a key exists under them.
  - Recursive types and huge indices are rejected.
  - Errors name keys and types, never values.
- Apps:
  - `BindConfig[T]` and `BindConfigNamed[T]` bind and validate at the call, then register `T` and a live `ConfigValue[T]` handle.
  - `ConfigDefaults` (a generic `BindOption[T]`) supplies a prefilled struct.
  - `ResolveConfig[T]` binds onto a consumer's own defaults and registers nothing.
  - `StaticConfig` builds a handle for tests.
- Extensions: `BindProperties` and `ValidateConfig`.

**Validation**
- go-playground/validator is the root's default, built lazily, so `validate:` tags are checked in every container.
- A type's own `Validate() error` also runs.
- `InjectorOpts.ConfigValidator` replaces go-playground; it is the only override.
- Errors list keys and rules, never values.

**The config module, `github.com/kartaladev/doger/config`**
- `config.Files(opts...)` searches `.` and `./config` for `config.{yaml,yml,toml,json}`. It overlays `config.<profile>.<ext>` (profile from `APP_PROFILE`), then `./.env` as a layer, then process env under `APP_`.
- `.env` is never written to the process environment unless `DotEnvExport()` is set. Even then, only unset variables are exported, once.
- Load errors read `<file>:<line>: <category>`, never parser text or values.
- Options: `Dir`, `File`, `Name`, `Format`, `Codec`, `Profile`, `ProfileEnv`, `EnvPrefix`, `EnvMapper`, `DotEnv`, `DotEnvExport`, `Layer`, `LayerOrder`, `Logger`, `DisableWatch`, `PollInterval`, `MaxFileSize`.
- Its non-test code imports only `go.yaml.in/yaml/v3`, `github.com/pelletier/go-toml/v2` and the stdlib.

**Reload (P2)**
- The source stages a parsed snapshot and calls `changed(ctx, next, err)`. The root rebinds and validates every section, then commits the snapshot as the view every reader sees, or keeps the last good one and warns. No reader ever mixes old and new values.
- Subscribers (`ConfigValue[T].OnChange`) and `HookConfigReloadFailed` hooks run on a dispatcher goroutine. They may therefore call `Shutdown`, and `Shutdown` waits only until its context ends.
- `config.Files` polls a content hash every 10s, follows Kubernetes ConfigMap symlink swaps, and stages only after two identical polls, so half-written files are ignored.
- The watcher runs between the first `Start` and the last `Shutdown` of a root and its clones.

**Dependencies (root module):** `github.com/go-playground/validator/v10` and its 7 modules (D7).

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
- `container-construction`: `NewWithOpts`, `NewWithOptsContext` and `CloneWithOpts` returning errors; `MustNewWithOpts`; `New`, `Clone` and `DefaultRootScope` unchanged; package order and panics.
- `config-sources`: the `ConfigSource` contract and its optional interfaces; installing a source; `Load` under a context and timeout; option checks; clone sharing; the text of errors and the secrecy of values.
- `config-binding`: the target-driven binder and its rules, `BindConfig`, `BindConfigNamed`, `ConfigDefaults`, `ConfigValue[T]`, `StaticConfig`, `ResolveConfig` and `BindProperties`.
- `config-validation`: the go-playground default, `Validate() error`, `InjectorOpts.ConfigValidator`, `ValidateConfig`, and the shape of validation errors.
- `config-files`: the `config` module's `config.Files` source, covering discovery, formats, profiles, layers, `.env`, env, options and load errors.
- `config-reload`: the `ConfigWatcher` contract, root-owned commit, dispatch of notifications and hooks, debounced polling, and the watcher's lifecycle with clones.

### Modified Capabilities
- `extension-properties`:
  - index and map-key segments in the key grammar;
  - the Spring env mapping with `__` for instances;
  - the optional `PropertyKeys`, with `MapSource`;
  - one prefix rule;
  - an installed `ConfigSource` as the property source.
- `module-identity`: the workspace gains the `config` module.
- `toolchain-support`: the `config` module declares the same Go version as every other module.

## Impact

- **Root module code:**
  - `root_scope.go`: constructors and clone.
  - `injector.go`: `InjectorOpts` fields `ConfigSource`, `ConfigTimeout`, `ConfigValidator`, `ConfigTagKey`, `HookConfigReloadFailed`.
  - `properties.go`: grammar, env mapping, `PropertyKeys`, `MapSource`.
  - New files: the contract, binder, typed binding, validation, and reload wiring.
- **Breaking API (flagged):**
  - The `NewWithOpts` and `CloneWithOpts` signatures. Migration: `root, err := doger.NewWithOpts(o)`, or `doger.MustNewWithOpts(o)` in tests; the same for `CloneWithOpts`.
  - `EnvPropertySource` variable names. Migration: rename, for example `DOGER_REDIS_POOL__SIZE` to `DOGER_REDIS_POOLSIZE`.

  No version is tagged and no app is deployed. `New(...)` call sites are untouched.
- **Call sites:** about 193 `NewWithOpts` and 7 `CloneWithOpts` calls in code and tests. Also affected: `scheduler/gocron`, `http/*`, `examples/*`, and docs pages and README snippets that use them.
- **New module:** `config/`, added to `go.work`. CI tests and lints it, including a `GOWORK=off` job, and the release workflow tags `config/vX.Y.Z`. Its tests import both modules; the root never imports it.
- **Repo:** govulncheck re-enabled, since `validator/v10` widens the root's dependency graph.
- **Specs:** the existing scenarios that construct roots still read the same. The `module-identity` and `toolchain-support` deltas add the `config` row.
- **Docs:**
  - a config guide;
  - `Example*` tests for zero config and overrides;
  - an upgrading note for both breaks;
  - the ROADMAP item marked done.

## Defaults & Control

| Behavior | Zero-config default | Consumer control |
|---|---|---|
| Config source | none: env only, via `EnvPropertySource("DOGER_")` | `InjectorOpts.ConfigSource` (`config.Files(...)` or any `ConfigSource`); `InjectorOpts.PropertySource` for a bare lookup |
| Source sharing | one source per container and its clones (documented, not policed) | `Clone` / `CloneWithOpts` with the same source share it |
| Construction errors | returned by `NewWithOpts` with a nil root | `MustNewWithOpts` panics instead |
| Load bound | 10s (`DefaultConfigTimeout`) on the container's clock | `ConfigTimeout` (negative = none); `NewWithOptsContext` with a caller ctx deadline or cancellation |
| File locations and name | `.` then `./config`, base name `config` | `config.Dir`, `config.File`, `config.Name` |
| Formats | YAML, TOML, JSON by extension; two formats with one name fail | `config.Format`, `config.Codec` |
| File size | 4 MiB per file | `config.MaxFileSize` |
| Profile | from `APP_PROFILE`; unset loads base files only; a set profile with no file fails | `config.Profile`, `config.ProfileEnv` |
| Precedence | added layers, env, `.env`, profile file, base file, struct defaults | `config.LayerOrder`, `config.Layer` |
| Env prefix | `APP_` for `config.Files`, `DOGER_` without a source; one normalization rule | `config.EnvPrefix` (`""` = off), `EnvPropertySource(prefix)`, `config.EnvMapper` |
| `.env` | `./.env` as a layer, never written to the process env | `config.DotEnv(path)`, `config.DotEnv("")`, `config.DotEnvExport()` (unset vars only) |
| Struct defaults | zero values | a prefilled struct: `ConfigDefaults(v)`, `ResolveConfig`'s `defaults`, `DefaultConfig()` before `BindProperties` |
| Key grammar and names | kebab keys with `[i]` and `[key]`; field names in uniform form | a `cfg` tag on the field; `InjectorOpts.ConfigTagKey` |
| A list from one string | a single value is comma-split | a named type with `UnmarshalText`, or indexed keys |
| Validation | go-playground in every container, plus `Validate() error` | `InjectorOpts.ConfigValidator` |
| Watching | on, polling every 10s once the container starts; stages after two identical polls | `config.DisableWatch()`, `config.PollInterval(d)` |
| Change notification | only sections whose bound keys changed | `ConfigValue[T].OnChange`; `stop` to unsubscribe |
| Failed reload | keep the last good config, warn with keys only | `HookConfigReloadFailed` / `AddConfigReloadFailedHook` |
| Logging | `InjectorOpts.Logf` when set, else `slog.Default()` for warnings; a source logs to `slog.Default()` | `InjectorOpts.Logf`, `config.Logger(l)` |
| Secrecy | values never logged or put in errors | none: a deliberate guarantee, as in `extension-properties` |
