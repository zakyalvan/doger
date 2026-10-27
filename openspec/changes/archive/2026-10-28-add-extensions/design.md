# Design

## Context

See proposal.md (Why) and this change's specs. This change builds on `add-default-registrations` (`AsDefault`, `serviceDefault`, the tiered `InvokeAs`, `registerLocked`) and `add-scope-export` (`Export`, exports of defaults with a forwarded export marker, export-aware shutdown and health check). The relevant parts of the current code:

- **Child scopes.** `Scope.Scope(name)` creates and attaches a child and panics on a duplicate name. A shut-down child keeps its name reserved, and nothing detaches a child without shutting the parent down (verified by both audits).
- **Shutting a child down fires hooks.** `BeforeShutdown`/`AfterShutdown` fire even for never-built lazy services, along with scope Closing/Closed (verified).
- **`ListInvokedServices`** includes ancestors (`scope.go:299-319`). Only the scope's own `orderedInvocation` shows what *it* resolved.
- **`InjectorOpts.copy()`** (`injector.go:328`) copies options for clones, and `Logf` defaults to a no-op. `RootScope.CloneWithOpts` (`root_scope.go:742`) clones the scope tree and copies kind entries.
- **Shutdown honors its context** (`shutdown-context`, from `fix-shutdown-after-context-end`). Services a shutdown reaches after its context ended are skipped: a built Shutdowner's `Shutdown` is not called and the report holds `ctx.Err()` under its name, any other service gets no error, and all of them are dropped. The `BeforeShutdown`/`AfterShutdown` hooks still run. The export-aware path (`Scope.shutdownWithExports`) skips the same way. `ShutdownOnSignalsWithContext` shuts down with `context.WithoutCancel(ctx)`.
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
- `RootScope` gains one field, `ext atomic.Pointer[extensionRegistry]`, set on the first `Extend`, the first property read, or the first clone (D10). The registry holds a mutex (short critical sections only) and `entries map[extensionID]*extensionEntry`, where `extensionID{typ reflect.Type; instance string}`, plus the private scope names and the waits-for graph. A container without extensions holds no registry, or an empty one after a clone; with no entry, every path is today's. `RootScope` is exactly at a 352-byte allocation size class and one pointer moves it to the next; a benchmark probe and the final benchstat showed no measurable effect.
- **Entry states:**
  - **installing**: a `done` channel and the session ID that owns it;
  - **installed**: value, effective options, scope name, `viaRequire` flag, export names;
  - **disabled**.
- **Waiting is checked for cycles.** Each session (one top-level `Extend` plus the `Require` calls made inside it) has an ID. Before a session waits on an entry another session is installing, it adds an edge `waiter → owner` to a waits-for graph under the registry mutex. If that edge closes a cycle, the call returns `ErrExtensionCycle` instead of waiting. The edge is removed when the wait ends.
- **The graph is a multigraph.** One `Extend` may run `Require` calls on several goroutines, so a session can wait on several owners at once, and on the same owner more than once: edges are counted per `(waiter, owner)` pair, removing one wait removes one count, and the cycle check is a depth-first search over every outgoing edge (proposal Defect F4: a single edge per session was overwritten and its removal dropped the other goroutine's wait).
- **Cycles inside one session** are found from the requirement chain each `ExtensionContext` carries (immutable, outermost first). A wait on an entry the same session installs, but which is not on the waiter's chain, is a concurrent `Require` of one `Extend`: it waits without an edge, since the entry is being installed by a sibling goroutine of the same call.
- **Concurrency outcome:** different identities install concurrently, the same identity installs once, and neither a one-goroutine nor a cross-goroutine cycle can deadlock. User code (`ext.Extend`) never runs under `extMu`.
- **`doger.Extend` from inside an `Extend`** is a new top-level session on the same goroutine. The waits-for graph cannot see that the outer session is blocked on it (that needs goroutine identity), so a cycle through a nested `doger.Extend` deadlocks, as a reentrant `sync.Once.Do` does. This is documented as unsupported, worded like `sync.Once`, and the godoc directs extensions to `x.Require`, whose chain detects cycles (Guice's `binder.install` and Spring's import stack follow the same model). Found during apply with a deadlock probe; no test pins a deadlock.

### D3. Phase check
`Extend`, and `Require`, read `root.Phase()` once, before claiming the identity. In `PhaseClosing`/`PhaseClosed` it returns an error matching `ErrStartAfterShutdown`. Services added in the started or ready phase are started by `Refresh`. In the failed phase `Refresh` returns `ErrNotStarted`, as today. A shutdown that begins during a running `Extend` is not prevented; that is the same rule as for hook adders and registrations, and it is documented.

### D4. Placement, staging and rollback
- **Placement order:** `Flatten()` wins, then the author's `PlacementFlat`, otherwise private. The private scope name is `ExtendScopeName(name)` if given, else `ext:<Name()>[/<instance>]`.
- **Private layout:**
  - `Extend` checks the root's direct children for the name and returns `ErrExtensionConflict` if it is taken. A race is closed by recovering the duplicate-scope panic of `root.Scope(name)` into the same error.
  - The child is created and attached at once, because providers need a real scope. `ExtensionExport*` records pending exports, applied with `ExportNamed` after `ext.Extend` returns nil. An export error counts as a failure.
- **Rollback:**
  - The child is shut down with `context.Background()` and then detached by a new internal `Scope.detachChild(name)`, which frees the reserved name for a retry. `Extend` takes no context, and a context that never ends keeps the `shutdown-context` skip from applying, so every service the failed attempt built gets its `Shutdown` called.
  - Exports applied in that attempt are removed by a new internal `Scope.removeService(name, match)`, which removes the service only when `match` accepts it: an export whose source is the attempt's scope (`readerSeesSource`, the helper `add-scope-export` already uses), so an application service registered under the published name is never removed. Its removal bookkeeping (counts, type matches, invocation order, DAG) is one internal `removeLocked(name)` shared with `serviceShutdown`, so the two removals cannot drift apart.
  - **The rollback always completes.** The detach and the export removal run in a deferred step, so they happen even when a scope hook panics during the shutdown; that panic, raised by the shutdown as for any shutdown when `HookContainerPanic` is unset, then propagates (proposal Defect F12).
  - **Failures are always reported.** A failed shutdown report of a rollback is joined to the error the call returns: a failure, a failing replacement, and a declined attempt (D6) alike (proposal Defect F6).
  - **Panics around the extension's code.** The recovery covers the whole attempt after the scope is created: `ext.Extend` and the application of the exports, whose registration hooks are user code (proposal Defect F11).
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
  - The replacement claims the identity in the claim step and keeps the private scope name reserved when the new plan uses the same name; otherwise it reserves the new name (or none, when flat) and releases the old one.
  - **A failed or declined replacement restores the required install** (proposal Defects F1 and F13): when the new install fails, including a failure of the old install's rollback shutdown, or declines with `ErrExtensionDisabled`, the old value is installed again in the same call, as a required install (`viaRequire`), and the returned `*ExtensionError` joins the replacement's outcome with any failure of that reinstall. Reinstalling is safe because nothing of the old install had been resolved.
  - **A declined replacement is a conflict.** Its error matches `ErrExtensionConflict`, not `ErrExtensionDisabled`: the installer's decline cannot take effect while the install is required. Nothing is recorded as disabled and no disabled line is logged.
  - **Concurrent resolution is unsupported.** A resolution of the old install's services that starts between the claim's check and the rollback would get a service that is then shut down. Closing that window would need an exclusive lock on every invocation of the scope; instead it is documented (godoc of `Require` and the authors' guide), like `Extend` concurrent with `Start`. It was not reproduced (review finding F2).
  - Otherwise `ErrExtensionConflict`.
  - Flattened `viaRequire` installs cannot be rolled back, so they always conflict.

### D6. Require, cycles and disabled extensions
- **`x.Require(dep)`** runs the D2 steps for `dep` in the caller's session.
  - Installed: returns nil.
  - Disabled: returns an error matching `ErrRequiredExtensionDisabled`, naming `dep`. It deliberately does not wrap `ErrExtensionDisabled`, so a requirer that returns it fails instead of being recorded as disabled.
  - On the session stack: `ErrExtensionCycle` naming the chain, for example `a -> b -> a`.
  - A cross-session cycle: `ErrExtensionCycle` from the waits-for graph.
  - The required entry is flagged `viaRequire`.
- **Disabled outcome:** when `ext.Extend` returns an error matching `ErrExtensionDisabled`, the attempt is rolled back (D4), the entry is recorded as disabled, `Logf("DI: extension %s disabled", name)` is called (`name/instance` for an instance), and `Extend` returns nil.
  - Flattened, nothing is rolled back (D4), so the registrations stay; the identity is still recorded as disabled.
  - When the rollback's shutdown fails, `Extend` returns an `*ExtensionError` wrapping the report and not the `ErrExtensionDisabled` cause, so callers checking `errors.Is(err, ErrExtensionDisabled)` do not mistake a leak for a clean decline; the identity is still recorded as disabled.
  - A `Require` whose own install ends disabled returns `ErrRequiredExtensionDisabled`, like a `Require` of an identity already disabled. When that declined attempt's shutdown failed, the `*ExtensionError` holding the report is joined to it, so the leak is reported through the requirer too.
- **An explicit `Extend` replacing a required install that declines** restores it and conflicts (D5).
- **A failure while installing a dependency** fails the requirer through the error `Require` returns. Each attempt rolls back only its own scope, and dependencies that installed successfully stay installed (spec).

### D7. Errors
- **Sentinels:** `ErrInvalidExtension`, `ErrExtensionConflict`, `ErrExtensionCycle`, `ErrExtensionDisabled`, `ErrRequiredExtensionDisabled`.
- **`ExtensionError`:** `type ExtensionError struct { Name, Instance string; Err error }`, with `Error()` returning ``DI: extension `redis` (instance `cache`): <cause>`` and `Unwrap()`.
- **Panics** in `ext.Extend` are recovered into `Err`. An error panic value is wrapped with `%w`; anything else is formatted with `%v`.
- **Validation errors** are wrapped in an `*ExtensionError` once the name is known to be valid (an invalid instance key, an empty `ExtendScopeName`, a panicking `Instance` or `Placement`), and returned bare otherwise: a nil or typed-nil extension, an empty or invalid name, or a panicking `Name`.

### D8. Root health check covers extension scopes
`RootScope.HealthCheckWithContext` adds the *own* services of each installed private extension scope, keyed `<scope name>/<service name>`. It skips:
- yielded defaults there, since the application's service they yielded to is checked under its own key;
- root exports whose source is an extension private scope, identified by the export marker, which `add-scope-export` makes survive the default wrapping.

So each service is checked once. Containers without extensions, or whose extensions are all flattened, take exactly today's path. `RootScope.HealthCheck` goes through `RootScope.HealthCheckWithContext` (it used to call the scope's method directly), so both take the extension path.

The root's own services and each extension scope's own services are queued by one internal helper extracted from `Scope.asyncHealthCheckWithContext` (the own-services loop, without the ancestor walk) that takes a skip predicate. The existing path uses it unchanged; the extension path passes a predicate that skips yielded defaults and, only when the root holds exports (`exportCount() > 0`), exports whose source is an extension scope. This removes the copied loop and the per-service scan of every extension scope (review findings F9 and F10).

### D9. Properties
- **`PropertySource`** is `interface{ Lookup(key string) (string, bool) }`.
- **`EnvPropertySource(prefix)`** maps a key by upper-casing it, replacing `-` with `__`, `.` with `_` and `/` with `___`, and prepending the prefix, then calls `os.LookupEnv`. With the D1 grammar, a run of underscores is 1 (a dot), 2 (a dash) or 3 (the instance separator) long and two runs are never adjacent, so distinct keys never map to the same variable.
- **Full keys** are `<name>.<key>` or `<name>/<instance>.<key>`. The `/` keeps an instance view out of the default view's key space (proposal Defect F7: `redis.queue.addr` was both the default view's `queue.addr` and the `queue` instance's `addr`), and it mirrors the private scope name `ext:<name>/<instance>`. The key a view is given cannot contain `/`: the key grammar rejects it.
- **`Properties`** is a value type: the root, the identity prefix, and a pointer to its context (nil for views from `ExtensionProperties`), whose `closed` flag ends the view and whose once-only log line reports the first late read. `Lookup` builds the full key, validates it against a package-level compiled regexp, and asks the root's source.
- **Source decision:** once per root, at the first `Lookup`, under a mutex with a stored result and a decided flag, not a `sync.Once`: a panic while deciding (a registration hook, for example) propagates to the reader and leaves the decision undecided, so the next read decides again (proposal Defect F5: `sync.Once` marked a panicking decision as done, leaving a nil source). Readers on other goroutines wait for the decision rather than missing. The order is:
  1. `opts.PropertySource`;
  2. else register `EnvPropertySource("DOGER_")` under `NameOf[PropertySource]()` with `AsDefault()`, unless a service of that name is already registered in the root, then invoke that name. An explicit service registered by the application wins over the default (default-registrations), so this one step covers both the application's service and the default, with no window between a check and a registration.
  3. If invoking the name fails, every property is unset for the container (a source that always misses) and one line is logged with the error's type (`%T`), never its message, which could hold a secret.

  Selection is by that name only, so an unrelated service with a `Lookup` method is never picked.
- **Re-entrant reads:** a `PropertySource` provider that reads properties during the decision would wait on itself. This is documented as unsupported (Non-Goals) and not detected, because a lock cannot tell the same goroutine from another one, as the audit proved for `TryLock`.
- **`ExtensionProperties(i)`** walks from the scope `i` resolves into up to the root's direct child, maps that child, via the registry's scope-to-identity index, to its extension's prefix, and binds the view to `i`'s root. A clone therefore reads the clone's source, and a descendant of a private scope gets its extension's view. A flattened extension's providers run in the root, so they get the missing view; their extension reads its configuration in `Extend` instead (documented).
- **Wiring:** `InjectorOpts.PropertySource` is a new field, copied by `copy()`. A clone decides again on its first read.
- **Secrecy:** the library logs the chosen source with `%T` only, never `%v`, and no error it builds formats a property value.

### D10. Clone
`CloneWithOpts` copies the installed and disabled entries (not installing ones) and the scope-to-identity index, as it does for kind entries. Exports in the clone re-bind through `add-scope-export`'s clone rule. Sessions and the waits-for graph are never copied.
- **Consistent with a running `Extend`** (proposal Defect F3). The scope tree is cloned and the entries copied while holding the source's registry mutex, so no installation ends between the two. Installations still running at that moment are not copied, and their partly built private scopes are detached from the clone, so the clone can install those identities itself. The lock order (registry, then scope) is the one the claim step already uses. To hold that mutex, a clone creates the source's registry if it has none yet, so a first `Extend` racing the clone takes the same lock; an empty registry changes no path.

## Risks / Trade-offs

- [`reflect.DeepEqual` treats non-nil `func` fields as unequal; `T` and `*T` are distinct identities] → authoring rules in the guide. Spec rows and `TestExtend_FuncOptionConflicts` pin the behavior.
- [A `PropertySource` registered under another name is never used] → documented. `InjectorOpts.PropertySource`, or registering under `NameOf[PropertySource]()`, are the supported ways. A late same-name registration panics loudly.
- [A re-entrant property read from the source's own provider deadlocks] → documented as unsupported. The godoc of `PropertySource` says to read configuration in the source's constructor arguments, not through `Properties`.
- [Rollback fires shutdown and scope hooks for services that were never used] → documented. `TestExtend_RollbackFiresHooks` pins it.
- [A rollback whose shutdown context ended would skip, and leak, the services the failed attempt built (`shutdown-context`)] → rollback shuts down with `context.Background()`. `TestExtend_RollbackFiresHooks` asserts the built service's `Shutdown` was called once.
- [A resolution of a required install's services racing the explicit `Extend` that replaces it] → documented as unsupported (D5); not reproduced.
- [The recovery of the duplicate-scope panic in `newPrivateScope` has no test] → the claim step rejects a taken name before it, and between the two runs no user code, so only a concurrent `root.Scope(name)` by the application can reach it; it stays as a defensive branch, named in the hot-path list as untestable.
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
| Invalid extensions are rejected | extension_test.go: `TestExtend_Invalid/{nil,typed_nil,trailing_dash,double_dash,empty_name,bad_name,bad_instance,empty_scope_name,name_panics,instance_panics,placement_panics}` | unit, table |
| Require validates its input | extension_test.go: `TestRequire_Invalid/{nil,bad_name}` | unit, table |
| Repeated and colliding installations | extension_test.go: `TestExtend_Repeat/{equal,unequal,other_instance,same_name_other_type,other_options,pointer_vs_value,disabled_equal,disabled_unequal}` | unit, table |
| A scope-name collision is resolved by the installer | extension_test.go: `TestExtend_ScopeNameResolvesCollision` | unit |
| Failure is reported and can be retried | extension_test.go: `TestExtend_FailureRollback/{error,error_panic,string_panic}` | unit, table |
| A flattened extension keeps its partial registrations | extension_test.go: `TestExtend_FlattenNoRollback` | unit |
| Failures around the extension are rolled back | extension_test.go: `TestExtend_RollbackAround/{export_hook_panic,closed_hook_panic,shutdown_fails}` | unit, table |
| A rollback removes only its own exports | extension_test.go: `TestExtend_RollbackKeepsAppService` | unit |
| A disabled extension installs nothing | extension_test.go: `TestExtend_Disabled/{no_instance,instance}` | unit, table |
| A flattened declined install keeps its registrations | extension_test.go: `TestExtend_DisabledFlattened` | unit |
| A declined install whose shutdown fails is reported | extension_test.go: `TestExtend_DisabledShutdownFails` | unit |
| Requiring a disabled extension fails | extension_test.go: `TestRequire_Disabled/{installed_disabled,declines_now}` | unit, table |
| A required install that declines and fails its shutdown is reported | extension_test.go: `TestRequire_DisabledShutdownFails` | unit |
| Require is idempotent | extension_test.go: `TestRequire_Idempotent` | unit |
| A Require cycle fails | extension_test.go: `TestRequire_Cycle` (in `testWithTimeout`) | unit, timeout |
| An explicit Extend after Require | extension_test.go: `TestExtend_AfterRequire/{unresolved_replaces,unrelated_root_invoked,resolved_conflicts,other_scope_name,flatten,descendant_resolved}` | unit, table |
| A required install placed flat by its author is never replaced | extension_test.go: `TestExtend_AfterRequireAuthorFlat` | unit |
| A failing replacement restores the required install | extension_test.go: `TestExtend_FailedReplacementRestores/{invoke,replace_again}` | unit, table |
| A declined replacement restores the required install | extension_test.go: `TestExtend_DeclinedReplacementRestores/{invoke,replace_again}` | unit, table |
| Concurrent Require calls of one Extend | extension_test.go: `TestRequire_ConcurrentSameSession` (10 goroutines, `testWithTimeout`) | race, timeout |
| A Require cycle across goroutines fails instead of deadlocking | extension_test.go: `TestRequire_CrossGoroutineCycle` (channel handshake, `testWithTimeout`) | race, timeout |
| A cycle through concurrent Require calls of one Extend fails | extension_test.go: `TestRequire_ConcurrentCycle` (channel gates, `testWithTimeout`) | race, timeout |
| Extend in each phase | extension_test.go: `TestExtend_Phases/{idle,starting,started,ready,failed,closing,closed}` | unit, table |
| The application's client wins | extension_test.go: `TestExtend_AppServiceWins/{before,after}` | unit, table |
| The application's client is health-checked once | extension_test.go: `TestExtend_AppServiceCheckedOnce` | unit |
| Lifecycle of extension services | extension_test.go: `TestExtend_Lifecycle` | unit |
| Only flattened extensions leave the root health check unchanged | extension_test.go: `TestExtend_FlattenedHealthCheck` | unit |
| Extend on a clone | extension_test.go: `TestExtend_Clone` | unit |
| Clone of other installation states | extension_test.go: `TestExtend_CloneStates/{disabled,running}` (the running row gated by channels, `testWithTimeout`) | unit, table, race |
| Concurrent installation of one identity | extension_test.go: `TestExtend_Concurrent` (50 goroutines) | race |
| Using the context after Extend returned | extension_test.go: `TestExtensionContext_AfterExtend/{export,require,properties}` (the properties row reads twice) | unit, table |
| Prefixed lookups | properties_test.go: `TestProperties_Prefixed/{no_instance,instance,missing}` | unit, table |
| Views of different instances never share a key | properties_test.go: `TestProperties_InstancesDistinct` | unit |
| Invalid keys are not looked up | properties_test.go: `TestProperties_InvalidKeys/{upper,double_dot,leading_dot,empty,trailing_dash,leading_dash,double_dash,slash,dash}` | unit, table |
| Key to variable mapping | properties_test.go: `TestEnvPropertySource_Mapping/{default,two_segments,instance,dash,custom_prefix,empty_prefix}` (`t.Setenv`) | unit, table |
| Distinct keys never share a variable | properties_test.go: `TestEnvPropertySource_Distinct` | unit |
| An unset variable is a miss | properties_test.go: `TestEnvPropertySource_Unset` | unit |
| Source selection | properties_test.go: `TestPropertySource_Selection/{env,service,opts,unrelated_lookup_ignored}` | unit, table |
| A source registered after the first read is not used | properties_test.go: `TestPropertySource_LateServiceIgnored` | unit |
| Registering the interface name after the first read panics | properties_test.go: `TestPropertySource_LateSameNamePanics` | unit |
| A failed decision is retried | properties_test.go: `TestPropertySource_DecisionRetried` (`t.Setenv`) | unit |
| A failing source provider leaves every property unset | properties_test.go: `TestPropertySource_ProviderFails` (`t.Setenv`) | unit |
| Values stay out of logs and errors | properties_test.go: `TestProperties_Secrecy` (map source with a password; asserts the source debug line too) | unit |
| Concurrent first reads decide the source once | properties_test.go: `TestPropertySource_ConcurrentFirstRead` | race |
| A provider reads properties in the original and the cloned container | properties_test.go: `TestExtensionProperties_Clone/{root,clone}` | unit, table |
| Injectors inside and outside extension scopes | properties_test.go: `TestExtensionProperties_Injectors/{root,child,descendant,flattened}` (replaces `TestExtensionProperties_OutsideExtension`) | unit, table |

The Risks section adds `TestExtend_FuncOptionConflicts` and `TestExtend_RollbackFiresHooks`. D5 adds `TestExtend_ReplacedRollbackFailsRestores` (a failing rollback of the replaced install restores it).

**Hot paths and their coverage:**
- `Extend`'s state machine (claim, wait with the waits-for graph, repeat, replace, rollback): the repeat, failure, after-require, cycle, concurrent and clone tests.
- Health-check aggregation: the lifecycle and checked-once tests, plus the existing health-check suite. A container without extensions is unchanged.
- `Properties.Lookup` and `ExtensionProperties`: the prefixed, invalid-key, selection, concurrent and clone tests.
- Rollback (D4): the failure, around-the-extension, own-exports and declined-shutdown tests; the deferred cleanup is shown failing by removing the defer.
- Replacement (D5): the after-require rows, the author-flat, failing-replacement and declined-replacement tests; the restore is shown failing by removing the reinstall.
- The waits-for multigraph (D2): the concurrent same-session and concurrent-cycle tests, run with `-count=10`.
- Clone under the registry lock (D10): the clone-states rows.
- The source decision (D9): the retried and provider-failure tests.
- Untestable by design: the duplicate-scope panic recovery in `newPrivateScope` (see Risks); and the root health check's choice of today's path when every extension is flattened. With no private extension scope, the extension path queues exactly the root's own services and skips exactly what today's path skips, so a mutation sending such a container down the extension path is equivalent: no test can observe it. The choice is a performance guarantee, and `TestExtend_FlattenedHealthCheck` pins the observable contract (no `ext:` keys, no child scope walked).
- `Extend` is not on an invocation hot path. `benchstat` on `HealthCheck` and `InvokeAs` confirms there is no regression without extensions, also after the health-check queue refactor (D8).

## Migration Plan

Additive. Docs:
- **Extensions section** (`docs/docs/extensions/`, new):
  - authors' guide: contract, optional interfaces, `AsDefault` plus export, options as data, pointer constructors, `Require`, `ExtensionProperties` in providers, precedence convention, `ErrExtensionDisabled`;
  - installers' guide: `Extend`, `Flatten`, `ExtendScopeName`, instances, `InjectorOpts.PropertySource`.
- **`docs/docs/service-lifecycle/healthchecker.md`:** extension-scope keys.
- **Upgrading page:** the root `HealthCheck` behavior change.

Rollback is a revert.

Order: `consolidate-pending-lifecycle-work`, `add-default-registrations` and `add-scope-export` are archived first.
