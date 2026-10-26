# Design

## Context

See proposal.md (Why) and this change's specs. This change builds on `add-default-registrations` (`AsDefault`, `serviceDefault`, the tiered `InvokeAs`, `registerLocked`) and `add-scope-export` (`Export`, exports of defaults with a forwarded export marker, export-aware shutdown and health check). The relevant parts of the current code:

- **Child scopes.** `Scope.Scope(name)` creates and attaches a child and panics on a duplicate name. A shut-down child keeps its name reserved, and nothing detaches a child without shutting the parent down (verified by both audits).
- **Shutting a child down fires hooks.** `BeforeShutdown`/`AfterShutdown` fire even for never-built lazy services, along with scope Closing/Closed (verified).
- **`ListInvokedServices`** includes ancestors (`scope.go:280-298`). Only the scope's own `orderedInvocation` shows what *it* resolved.
- **`InjectorOpts.copy()`** (`injector.go:320`) copies options for clones, and `Logf` defaults to a no-op. `RootScope.CloneWithOpts` (`root_scope.go:726`) clones the scope tree and copies kind entries.
- **Phases.** `RootScope.Phase()` reports the container phase. `Refresh` and `Scope.Start` require the started or ready phase (`checkIncrementalPhase`, `root_refresh.go:104`). A `ContainerStarting` hook runs before discovery and holds no `lifecycleMu`. Closing hooks run after the close phase is set.
- **Root health check.** `RootScope.HealthCheckWithContext` checks the root's own services only; child scopes are never walked.
- **Name inference.** `NameOf` of an interface has no `*` prefix: `github.com/kartaladev/doger.PropertySource`.

## Goals / Non-Goals

**Goals:**
- A small, stable SPI: two required methods, optional interfaces for everything else, a struct context that can grow.
- All-or-nothing installation in the default (private) layout, predictable identity and conflict rules, and dependency installs that never deadlock.
- No global state, and no configuration library in core.

**Non-Goals:**
- An import-based registry, struct binding of properties, and ordering hints between extensions (proposal, out of scope).
- Enforcing configuration precedence (code options, then properties, then defaults). That is an authoring convention.
- `Extend` concurrent with `Start` (documented, as for the hook adders).
- A `PropertySource` provider that reads properties while its own source is being decided (documented as unsupported; see D9).

## Decisions

### D1. The SPI types
```go
type Extension interface { Name() string; Extend(x *ExtensionContext) error }
type ExtensionPlacement interface { Placement() Placement }  // optional
type ExtensionInstance interface { Instance() string }      // optional
type Placement int                                          // PlacementPrivate (zero), PlacementFlat
type ExtensionContext struct { /* unexported */ }           // Scope, Root, Instance, Properties, Require
type ExtendOption func(*extendOptions)                      // Flatten, ExtendScopeName
func ExtensionExport[T any](x *ExtensionContext, opts ...ExportOption) error
func ExtensionExportNamed[T any](x *ExtensionContext, name string, opts ...ExportOption) error
func ExtensionProperties(i Injector) (Properties, bool)
```
- **`PlacementPrivate` is the zero value.** This departs from the "enums start at 1" guideline on purpose: the zero value is the safe, opinionated default.
- **Export helpers are package functions**, because Go methods cannot have type parameters.
- **Optional interfaces** follow the library's `Healthchecker`/`Shutdowner` style, so implementors never break when new extension points are added.
- **Names and instance keys** must be lowercase alphanumeric words joined by single dashes, `[a-z0-9]+(-[a-z0-9]+)*`. The same grammar applies to property-key segments, which keeps the environment mapping one-to-one (D9).
- **Typed nil.** A typed-nil extension is detected with `reflect` before `Name()` is called. `Name()` and `Instance()` are called under `recover`, so validation never panics.
- **Context validity.** An `ExtensionContext` is valid only while its `Extend` runs. A `closed` flag set on return makes `ExtensionExport*` and `Require` fail with `ErrInvalidExtension`, and makes its `Properties` view miss and log once. Providers use `ExtensionProperties(i)` instead, which is clone-safe because it binds to the provider's own container.

### D2. Installation entries and a waits-for graph
- `RootScope` gains `extMu` (short critical sections only) and `extensions map[extensionID]*extensionEntry`, where `extensionID{typ reflect.Type; instance string}`.
- **Entry states:**
  - **installing**: a `done` channel and the session ID that owns it;
  - **installed**: value, effective options, scope name, `viaRequire` flag, export names;
  - **disabled**.
- **Waiting is checked for cycles.** Each session (one top-level `Extend` plus the `Require` calls made inside it) has an ID. Before a session waits on an entry another session is installing, it records an edge `waiter → owner` in a waits-for graph under `extMu`. If that edge closes a cycle, the call returns `ErrExtensionCycle` instead of waiting. The edge is removed when the wait ends.
- **Cycles inside one session** are found from the session's requirement stack.
- **Concurrency outcome:** different identities install concurrently, the same identity installs once, and neither a one-goroutine nor a cross-goroutine cycle can deadlock. User code (`ext.Extend`) never runs under `extMu`.
- **`doger.Extend` from inside an `Extend`** is a new top-level session. It can wait on another session safely, but a cycle through it is reported, not deadlocked. The godoc still recommends `x.Require`.

### D3. Phase check
`Extend` reads `root.Phase()` once, before claiming the identity. In `PhaseClosing`/`PhaseClosed` it returns an error matching `ErrStartAfterShutdown`. Services added in the started or ready phase are started by `Refresh`. In the failed phase `Refresh` returns `ErrNotStarted`, as today. A shutdown that begins during a running `Extend` is not prevented; that is the same rule as for hook adders and registrations, and it is documented.

### D4. Placement, staging and rollback
- **Placement order:** `Flatten()` wins, then the author's `PlacementFlat`, otherwise private. The private scope name is `ExtendScopeName(name)` if given, else `ext:<Name()>[/<instance>]`.
- **Private layout:**
  - `Extend` checks the root's direct children for the name and returns `ErrExtensionConflict` if it is taken. A race is closed by recovering the duplicate-scope panic of `root.Scope(name)` into the same error.
  - The child is created and attached at once, because providers need a real scope. `ExtensionExport*` records pending exports, applied with `ExportNamed` after `ext.Extend` returns nil. An export error counts as a failure.
- **Rollback:**
  - The child is shut down and then detached by a new internal `Scope.detachChild(name)`, which frees the reserved name for a retry.
  - Exports applied in that attempt are removed by a new internal `Scope.removeService(name)`.
  - The identity entry is deleted and `done` closed.
  - Rolling back fires the private scope's shutdown hooks and scope Closing/Closed hooks, just as its registration hooks fired. Hooks observe what happens to scopes and services, not successful installs. This is documented and pinned by a test.
- **Flattened:** `x.Scope()` is the root's own `*Scope`, and exports are no-ops. A failure deletes the identity entry but keeps the registrations (documented).

### D5. Identity, equality and replacing a required install
- **Repeat `Extend` of an installed identity:** no-op when the extension values are `reflect.DeepEqual` *and* the effective `ExtendOption`s match. Otherwise it returns `ErrExtensionConflict` naming the identity.
- **Authoring rules in the guide:**
  - Keep options as plain data. A non-nil `func` field compares unequal.
  - Always return a pointer from the constructor. `T` and `*T` are two identities that collide on the scope name, which a spec row pins.
- **Explicit `Extend` over a `viaRequire` entry with an unequal value:**
  - If no service of its private scope or that scope's descendants was resolved, the old install is rolled back (D4) and the new one installed. "Resolved" is judged by each scope's own `orderedInvocation`, read under its lock. Resolving an export invokes its source there, so exports count too.
  - Otherwise `ErrExtensionConflict`.
  - Flattened `viaRequire` installs cannot be rolled back, so they always conflict.

### D6. Require, cycles and disabled extensions
- **`x.Require(dep)`** runs the D2 steps for `dep` in the caller's session.
  - Installed: returns nil.
  - Disabled: returns an error matching `ErrRequiredExtensionDisabled`, naming `dep`. It deliberately does not wrap `ErrExtensionDisabled`, so a requirer that returns it fails instead of being recorded as disabled.
  - On the session stack: `ErrExtensionCycle` naming the chain, for example `a -> b -> a`.
  - A cross-session cycle: `ErrExtensionCycle` from the waits-for graph.
  - The required entry is flagged `viaRequire`.
- **Disabled outcome:** when `ext.Extend` returns an error matching `ErrExtensionDisabled`, the attempt is rolled back (D4), the entry is recorded as disabled, `Logf("DI: extension %s disabled", name)` is called, and `Extend` returns nil.
- **A failure while installing a dependency** fails the requirer through the error `Require` returns. Each attempt rolls back only its own scope, and dependencies that installed successfully stay installed (spec).

### D7. Errors
- **Sentinels:** `ErrInvalidExtension`, `ErrExtensionConflict`, `ErrExtensionCycle`, `ErrExtensionDisabled`, `ErrRequiredExtensionDisabled`.
- **`ExtensionError`:** `type ExtensionError struct { Name, Instance string; Err error }`, with `Error()` returning ``DI: extension `redis` (instance `cache`): <cause>`` and `Unwrap()`.
- **Panics** in `ext.Extend` are recovered into `Err`. An error panic value is wrapped with `%w`; anything else is formatted with `%v`.
- **Validation errors** are wrapped in an `*ExtensionError` when a name is known, and returned bare otherwise (nil or typed-nil extension).

### D8. Root health check covers extension scopes
`RootScope.HealthCheckWithContext` adds the *own* services of each installed private extension scope, keyed `<scope name>/<service name>`. It skips:
- yielded defaults there, since the application's service they yielded to is checked under its own key;
- root exports whose source is an extension private scope, identified by the export marker, which `add-scope-export` makes survive the default wrapping.

So each service is checked once. Containers without extensions take exactly today's path.

### D9. Properties
- **`PropertySource`** is `interface{ Lookup(key string) (string, bool) }`.
- **`EnvPropertySource(prefix)`** maps a key by upper-casing it, replacing `-` with `__` and `.` with `_`, and prepending the prefix, then calls `os.LookupEnv`. With the D1 grammar, a run of underscores is either 1 (a dot) or 2 (a dash) and two runs are never adjacent, so distinct keys never map to the same variable.
- **`Properties`** is a value type: the root, the identity prefix, and a pointer to its context's `closed` flag (nil for views from `ExtensionProperties`). `Lookup` builds the full key, validates it against a package-level compiled regexp, and asks the root's source.
- **Source decision:** once per root, at the first `Lookup`, through a `sync.Once`-style guard with a stored result. Readers on other goroutines wait for the decision rather than missing. The order is:
  1. `opts.PropertySource`;
  2. else the root's service named `NameOf[PropertySource]()`, invoked by name;
  3. else `ProvideNamedValue(root, NameOf[PropertySource](), EnvPropertySource("DOGER_"), AsDefault())`, then invoke it.

  Selection is by that name only, so an unrelated service with a `Lookup` method is never picked.
- **Re-entrant reads:** a `PropertySource` provider that reads properties during the decision would wait on itself. This is documented as unsupported (Non-Goals) and not detected, because a lock cannot tell the same goroutine from another one, as the audit proved for `TryLock`.
- **`ExtensionProperties(i)`** maps the scope `i` resolves into, via the registry's scope-to-identity index, to that extension's prefix, and binds the view to `i`'s root. A clone therefore reads the clone's source.
- **Wiring:** `InjectorOpts.PropertySource` is a new field, copied by `copy()`. A clone decides again on its first read.
- **Secrecy:** the library logs the chosen source with `%T` only, never `%v`, and no error it builds formats a property value.

### D10. Clone
`CloneWithOpts` copies the installed and disabled entries (not installing ones) and the scope-to-identity index, after the scope tree, as it does for kind entries. Exports in the clone re-bind through `add-scope-export`'s clone rule. Sessions and the waits-for graph are never copied.

## Risks / Trade-offs

- [`reflect.DeepEqual` treats non-nil `func` fields as unequal; `T` and `*T` are distinct identities] → authoring rules in the guide. Spec rows and `TestExtend_FuncOptionConflicts` pin the behavior.
- [A `PropertySource` registered under another name is never used] → documented. `InjectorOpts.PropertySource`, or registering under `NameOf[PropertySource]()`, are the supported ways. A late same-name registration panics loudly.
- [A re-entrant property read from the source's own provider deadlocks] → documented as unsupported. The godoc of `PropertySource` says to read configuration in the source's constructor arguments, not through `Properties`.
- [Rollback fires shutdown and scope hooks for services that were never used] → documented. `TestExtend_RollbackFiresHooks` pins it.
- [A shutdown that races `Extend`] → not prevented. The phase check plus the existing shutdown semantics apply, documented with the hook-adder rule.
- [The waits-for graph adds bookkeeping to waiting] → it is touched only when a call waits on another session, which is rare (the same identity installed concurrently). Plain installs only take `extMu` briefly.

## Public API & Defaults

| Identifier | Kind | Default / Override |
|---|---|---|
| `Extension`, `ExtensionPlacement`, `ExtensionInstance` | interfaces | Only `Name`/`Extend` required |
| `Placement`, `PlacementPrivate`, `PlacementFlat` | enum | Zero is private |
| `ExtensionContext` (`Scope`, `Root`, `Instance`, `Properties`, `Require`) | struct | Built by `Extend` only; valid during `Extend` |
| `Extend(root *RootScope, ext Extension, opts ...ExtendOption) error` | func | nil root means `DefaultRootScope` |
| `ExtendOption`, `Flatten()`, `ExtendScopeName(name)` | options | Author's layout, scope `ext:<name>[/<instance>]` |
| `ExtensionExport[T]`, `ExtensionExportNamed[T]` | funcs | Applied when `Extend` succeeds; no-op when flattened |
| `ExtensionProperties(i Injector) (Properties, bool)` | func | Missing view outside extension scopes |
| `ErrInvalidExtension`, `ErrExtensionConflict`, `ErrExtensionCycle`, `ErrExtensionDisabled`, `ErrRequiredExtensionDisabled` | sentinels | Match with `errors.Is` |
| `ExtensionError` | error type | Match with `errors.As` |
| `PropertySource`, `EnvPropertySource(prefix)`, `Properties` | config SPI | Environment source with `DOGER_` |
| `InjectorOpts.PropertySource` | new field | nil means unset |

Not BREAKING: all additions. The root `HealthCheck` behavior change applies only to containers that install extensions. It is flagged in the spec and goes on the upgrading page.

## Test Strategy

Production code goes in `extension.go` and `extension_context.go` (tested in `extension_test.go`) and `properties.go` (tested in `properties_test.go`). Examples (`ExampleExtend`, `ExampleExtensionExport`, `ExampleEnvPropertySource`) live in `extension_example_test.go`, with a small in-file extension.

Tests are table-driven with testify and run under `-race`. Tests that wait on channels use `testWithTimeout`. Tests using `t.Setenv` or `DefaultRootScope` are not parallel.

| Scenario | Test | Kind |
|---|---|---|
| Default private layout with an export | extension_test.go: `TestExtend_PrivateLayout/{no_instance,instance}` | unit, table |
| The context describes the installation | extension_test.go: `TestExtend_Context/{no_instance,instance}` | unit, table |
| A nil root means DefaultRootScope | extension_test.go: `TestExtend_NilRoot` (not parallel) | unit |
| Placement by author and installer | extension_test.go: `TestExtend_Placement/{default,author_private,author_flat,flatten,flat_ignores_scope_name,scope_name}` | unit, table |
| Invalid extensions are rejected | extension_test.go: `TestExtend_Invalid/{nil,typed_nil,trailing_dash,double_dash,empty_name,bad_name,bad_instance,empty_scope_name}` | unit, table |
| Repeated and colliding installations | extension_test.go: `TestExtend_Repeat/{equal,unequal,other_instance,same_name_other_type,other_options,pointer_vs_value}` | unit, table |
| A scope-name collision is resolved by the installer | extension_test.go: `TestExtend_ScopeNameResolvesCollision` | unit |
| Failure is reported and can be retried | extension_test.go: `TestExtend_FailureRollback/{error,error_panic,string_panic}` | unit, table |
| A flattened extension keeps its partial registrations | extension_test.go: `TestExtend_FlattenNoRollback` | unit |
| A disabled extension installs nothing | extension_test.go: `TestExtend_Disabled` | unit |
| Requiring a disabled extension fails | extension_test.go: `TestRequire_Disabled` | unit |
| Require is idempotent | extension_test.go: `TestRequire_Idempotent` | unit |
| A Require cycle fails | extension_test.go: `TestRequire_Cycle` (in `testWithTimeout`) | unit, timeout |
| An explicit Extend after Require | extension_test.go: `TestExtend_AfterRequire/{unresolved_replaces,unrelated_root_invoked,resolved_conflicts}` | unit, table |
| A Require cycle across goroutines fails instead of deadlocking | extension_test.go: `TestRequire_CrossGoroutineCycle` (channel handshake, `testWithTimeout`) | race, timeout |
| Extend in each phase | extension_test.go: `TestExtend_Phases/{idle,starting,started,ready,failed,closing,closed}` | unit, table |
| The application's client wins | extension_test.go: `TestExtend_AppServiceWins/{before,after}` | unit, table |
| The application's client is health-checked once | extension_test.go: `TestExtend_AppServiceCheckedOnce` | unit |
| Lifecycle of extension services | extension_test.go: `TestExtend_Lifecycle` | unit |
| Extend on a clone | extension_test.go: `TestExtend_Clone` | unit |
| Concurrent installation of one identity | extension_test.go: `TestExtend_Concurrent` (50 goroutines) | race |
| Using the context after Extend returned | extension_test.go: `TestExtensionContext_AfterExtend/{export,require,properties}` | unit, table |
| Prefixed lookups | properties_test.go: `TestProperties_Prefixed/{no_instance,instance,missing}` | unit, table |
| Invalid keys are not looked up | properties_test.go: `TestProperties_InvalidKeys/{upper,double_dot,leading_dot,empty,trailing_dash,leading_dash,double_dash,dash}` | unit, table |
| Key to variable mapping | properties_test.go: `TestEnvPropertySource_Mapping/{default,two_segments,dash,custom_prefix,empty_prefix}` (`t.Setenv`) | unit, table |
| Distinct keys never share a variable | properties_test.go: `TestEnvPropertySource_Distinct` | unit |
| An unset variable is a miss | properties_test.go: `TestEnvPropertySource_Unset` | unit |
| Source selection | properties_test.go: `TestPropertySource_Selection/{env,service,opts,unrelated_lookup_ignored}` | unit, table |
| A source registered after the first read is not used | properties_test.go: `TestPropertySource_LateServiceIgnored` | unit |
| Registering the interface name after the first read panics | properties_test.go: `TestPropertySource_LateSameNamePanics` | unit |
| Values stay out of logs and errors | properties_test.go: `TestProperties_Secrecy` (map source with a password; asserts the source debug line too) | unit |
| Concurrent first reads decide the source once | properties_test.go: `TestPropertySource_ConcurrentFirstRead` | race |
| A provider reads properties in the original and the cloned container | properties_test.go: `TestExtensionProperties_Clone/{root,clone}` | unit, table |
| An injector outside an extension scope | properties_test.go: `TestExtensionProperties_OutsideExtension` | unit |

The Risks section adds `TestExtend_FuncOptionConflicts` and `TestExtend_RollbackFiresHooks`.

**Hot paths and their coverage:**
- `Extend`'s state machine (claim, wait with the waits-for graph, repeat, replace, rollback): the repeat, failure, after-require, cycle, concurrent and clone tests.
- Health-check aggregation: the lifecycle and checked-once tests, plus the existing health-check suite. A container without extensions is unchanged.
- `Properties.Lookup` and `ExtensionProperties`: the prefixed, invalid-key, selection, concurrent and clone tests.
- `Extend` is not on an invocation hot path. `benchstat` on `HealthCheck` and `InvokeAs` confirms there is no regression without extensions.

## Migration Plan

Additive. Docs:
- **Extensions section** (`docs/docs/extensions/`, new):
  - authors' guide: contract, optional interfaces, `AsDefault` plus export, options as data, pointer constructors, `Require`, `ExtensionProperties` in providers, precedence convention, `ErrExtensionDisabled`;
  - installers' guide: `Extend`, `Flatten`, `ExtendScopeName`, instances, `InjectorOpts.PropertySource`.
- **`docs/docs/service-lifecycle/healthchecker.md`:** extension-scope keys.
- **Upgrading page:** the root `HealthCheck` behavior change.

Rollback is a revert.

Order: `consolidate-pending-lifecycle-work`, `add-default-registrations` and `add-scope-export` are archived first.
