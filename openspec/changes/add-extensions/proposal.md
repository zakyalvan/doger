# Proposal

## Why

Plugging a Go library (Redis, a SQL pool, OpenTelemetry, a scheduler) into a doger container is hand-written glue in every application today. `Package(...)` groups registrations, but a package has no identity, no defaults the app can replace, no private internals, no configuration and no way to depend on another package. The existing HTTP adapters (`dogerchi`, `dogerecho`, …) show the demand, but each one invents its own shape.

This change defines an SPI: a contract a third-party module implements once, so that any library capability can be installed in one line, with opinionated defaults and full consumer control. It plays the role Spring Boot auto-configuration plays, done the Go way: explicit, typed, and with no global state.

## What Changes

**The contract**
- `type Extension interface { Name() string; Extend(x *ExtensionContext) error }`. Future extension points come as optional interfaces, never as new methods on `Extension`.
- `ExtensionContext` is a struct, so the library can add capabilities to it without breaking implementors. It provides `Scope()` (where to register), `Root()`, `Instance()`, `Properties()`, `Require(ext)`, and export helpers that delegate to `add-scope-export`. It is valid only while `Extend` runs; providers read configuration through `ExtensionProperties(i)`, which is clone-safe.
- Implementations live in their own modules named `doger<lib>` and expose a constructor `Extension(opts...)`.

**Installing**
- `doger.Extend(root *RootScope, ext Extension, opts ...ExtendOption) error`. It accepts only the root; a nil root means `DefaultRootScope`, as elsewhere in the library. Activation is explicit only: there is no `import _` registry in this change.
- **Placement:** the author picks the default layout, which is a private child scope plus exports, or flat. The installer overrides it with `ExtendOption`s: `Flatten()` registers into root, and a scope-name override handles clashes. Exports are no-ops when the extension is flattened.
- **Identity:** the extension's Go type plus an optional instance key (an extension option such as `dogerredis.Instance("cache")`).
  - An identical repeat call does nothing.
  - A repeat call with different options fails.
  - An install made through `Require` is replaced by a later explicit `Extend` with a different value while no service of its private scope has been resolved; otherwise that `Extend` conflicts.
  - Each identity gets its own private scope under a reserved name prefix. A name collision is reported as an error, not a panic.
- **Staged and atomic:** `ext.Extend` registers into a pending private scope. Exports are applied and the identity is recorded only on success. On failure, the pending scope is dropped and a retry is possible. `Flatten()` is documented as having no rollback.
- **`Require` and cycles:** `x.Require(other)` installs a dependency extension idempotently. A cycle (A requires B requires A) fails with `ErrExtensionCycle`, on one goroutine or across goroutines (a waits-for graph), instead of deadlocking. Requiring a disabled extension fails the requirer with `ErrRequiredExtensionDisabled`. There is no ordering between extensions: resolution is lazy and `Start` orders services by the dependency graph.
- **Phase:** `Extend` is allowed in Idle, Starting (from a Starting hook), Started, Ready and Failed. After `Start`, call `Refresh` to start what the extension added. It is rejected in Closing and Closed with an error matching `ErrStartAfterShutdown`. Like the hook adders, it must not be called concurrently with `Start`.
- **Clone:** `CloneWithOpts` copies the set of installed extensions along with their scopes.
- **Errors:** errors name the extension and its instance and wrap the cause. A panic inside `Extend` is recovered and returned as an error.
- **Services an extension provides** are registered with `AsDefault()` in its private scope and exported. Exports of defaults are defaults (`add-scope-export`), so an application service registered under the published name wins whether it is registered before or after `Extend`, and the extension's own code also resolves the application's instance. Replacement is by name: the published name, by default the type name. Replacement by a differently named service of the same type applies where the extension or the application resolves by type (`InvokeAs`, collections).
- **Health (BEHAVIOR CHANGE, flagged, only for containers that use extensions):** root `HealthCheck` also checks the services of extension private scopes, keyed by scope, and skips exports whose source is an extension private scope, so each service is checked once. Ordinary child scopes behave as before.

**Configuration**
- `type PropertySource interface { Lookup(key string) (string, bool) }`. `x.Properties()` returns a view prefixed with the extension's identity.
- **Default source:** environment variables with a configurable prefix (default `DOGER_`) and a one-to-one mapping (`.` becomes `_`, `-` becomes `__`; names, instance keys and key segments are lowercase alphanumeric words joined by single dashes). The source is chosen per container, in this order: `InjectorOpts.PropertySource` when set; otherwise a service the application registered under `NameOf[PropertySource]()` (services of other names are never selected); otherwise the environment default, registered with `AsDefault()` lazily on the first property read. Because `Extend` may read properties, a `PropertySource` service registered after the first read fails with the duplicate panic naming it; `InjectorOpts.PropertySource` avoids any ordering. Property values are never logged.
- **Precedence:** code options, then properties, then the extension's built-in defaults. There is no built-in enable/disable toggle. An extension may offer its own, and a disabled extension returns a distinct, logged outcome.

**Out of scope:** an import-based registry, binding properties into structs, and ordering hints between extensions.

## Capabilities

### New Capabilities
- `extensions`: the `Extension` contract, `Extend`, `ExtensionContext`, placement and overrides, identity and instances, staging and rollback, `Require`, phase rules, clone, errors and the health check of private scopes.
- `extension-properties`: `PropertySource`, the default environment source and its key mapping, prefixed views, precedence and secrecy.

### Modified Capabilities
- None. Root `HealthCheck` coverage of extension scopes is specified in `extensions`, because it applies only to scopes `Extend` creates; it is flagged as a behavior change of root `HealthCheck` output for containers that use extensions.

## Impact

- Code: new files `extension.go`, `extension_context.go` and `properties.go`; `root_scope.go` (extension registry, clone); `scope.go` (detaching a child scope internally, the extension-scope health check).
- API: additive only (new `InjectorOpts.PropertySource` field among others). No exported API breaks.
- Dependencies: requires `add-default-registrations` (defaults that yield, used by `AsDefault()` registrations and by the default `PropertySource`) and `add-scope-export` (exports). Also requires `consolidate-pending-lifecycle-work` to be archived, for `Refresh` and phases.
- Docs: an "Extensions" guide for authors and installers, runnable `Example*` tests with a small in-package extension (a separate example module would change the exhaustive workspace list pinned by `module-identity`), and an upgrading note (no breaks).

## Defaults & Control

| Behavior | Zero-config default | Consumer control |
|---|---|---|
| Where an extension registers | Author's layout: private scope plus exports (or flat) | `Flatten()`, scope-name override |
| Multiple instances | One instance per extension type | The extension's instance option (for example `Instance("cache")`) |
| Services the extension provides | Exported `AsDefault()` registrations, so app services win in any registration order | Provide your own service under the published name (by default the type name); by type where it is resolved by type |
| Configuration source | Environment variables with the `DOGER_` prefix | `InjectorOpts.PropertySource` (no ordering constraint), or a `PropertySource` service registered before the first property read (an adapter module for viper or koanf); change the prefix |
| Precedence | Code options, then properties, then extension defaults | Set the code option to pin a value |
| Repeat calls with the same identity | No-op if the extension value and the `ExtendOption`s are identical; `ErrExtensionConflict` otherwise | Use an instance key for a second instance |
| `Extend` after `Start` | Allowed; call `Refresh` | – (rejected only after shutdown began, because nothing can start then) |
| Extension enable/disable | Not provided by the library | The extension author may offer a toggle |
