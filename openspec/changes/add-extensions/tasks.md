# Tasks

Prerequisites: `consolidate-pending-lifecycle-work`, `add-default-registrations` and `add-scope-export` are archived, as is `fix-shutdown-after-context-end`, whose `shutdown-context` rule (services reached after the shutdown context ended are skipped) the rollback must avoid (this change uses `AsDefault`, the tiered `InvokeAs`, `registerLocked`, `ExportNamed`, exports of defaults with the forwarded export marker, and the export-aware shutdown), and `make test` is green before starting. Read design.md (D1–D10, Risks and the Test Strategy table) before each group. Production code lives in new `extension.go`, `extension_context.go` and `properties.go`; tests in `extension_test.go` and `properties_test.go`. Tests that set environment variables use `t.Setenv` and are not parallel.

## 0. Baseline

- [x] 0.1 Record the benchmark baseline with `go test -run '^$' -bench 'HealthCheck|InvokeAs' -benchmem -count 10 ./bench/ > <scratch>/bench-before.txt`; verify the file exists and `make test` passes.

## 1. The SPI types and a private install

- [x] 1.1 RED: add `TestExtend_PrivateLayout` (rows no_instance, instance; scenario "Default private layout with an export"), `TestExtend_Context` (2 rows), `TestExtend_NilRoot` (not parallel) and `TestExtend_Invalid` (8 rows; scenario "Invalid extensions are rejected", including typed_nil, trailing_dash and double_dash), using a test extension type defined in `extension_test.go`; verify `go test -race -run 'TestExtend_(PrivateLayout|Context|NilRoot|Invalid)' .` fails to compile only on `Extension`, `ExtensionInstance`, `ExtensionContext`, `Extend`, `ExtendScopeName`, `ExtensionExport` and `ErrInvalidExtension`.
- [x] 1.2 GREEN: add the design D1 types, `Extend` with validation (name grammar, typed-nil detection, `recover` around `Name`/`Instance`; D7 messages), private scope `ext:<name>[/<instance>]`, `ExtensionContext` (`Scope`, `Root`, `Instance`), and `ExtensionExport`/`ExtensionExportNamed` recording pending exports applied with `ExportNamed` after a successful `ext.Extend` (D4), with no registry yet; verify the 1.1 command passes and `make test` is green.
- [x] 1.3 REFACTOR: *Extract Function* `validateExtension(ext, opts) (id extensionID, err error)` and `privateScopeName(name, instance, opts)`; verify `make test` and `make lint` pass.
- [x] 1.4 Godoc for `Extension`, `ExtensionInstance`, `ExtensionContext`, `Extend`, `ExtensionExport*` and `ErrInvalidExtension` (the contract, the name grammar, the private default, exports applied on success, `x.Require` as the recommended way to install from inside `Extend`); verify `go doc Extend` shows it and `make lint` passes.

## 2. Placement chosen by author and installer

- [x] 2.1 RED: add `TestExtend_Placement` (6 rows; scenario "Placement by author and installer"); verify `go test -race -run TestExtend_Placement .` fails to compile only on `ExtensionPlacement`, `Placement`, `PlacementPrivate`, `PlacementFlat` and `Flatten`.
- [x] 2.2 GREEN: implement design D4 placement order (`Flatten` > author `PlacementFlat` > private; `ExtendScopeName` for the private name; exports no-op when flat); verify the 2.1 command passes and `make test` is green.
- [x] 2.3 REFACTOR: *Introduce Parameter Object* `installPlan{flat bool; scopeName string}` computed once per `Extend`; verify `make test` and `make lint` pass.
- [x] 2.4 Godoc for `ExtensionPlacement`, `Placement`, `PlacementPrivate`, `PlacementFlat`, `Flatten` and `ExtendScopeName` (zero value is private, installer options win, no rollback when flat); verify `go doc Flatten` shows it and `make lint` passes.

## 3. Identity, repeats and concurrent installs

- [x] 3.1 RED: add `TestExtend_Repeat` (6 rows; scenario "Repeated and colliding installations"), `TestExtend_ScopeNameResolvesCollision`, `TestExtend_Concurrent` (50 goroutines, in `testWithTimeout`) and `TestExtend_FuncOptionConflicts` (design Risks), with every `Extend` call in these tests wrapped in `assert.NotPanics` so the current duplicate-scope panic fails one row instead of aborting the run; verify `go test -race -run 'TestExtend_(Repeat|ScopeNameResolvesCollision|Concurrent|FuncOptionConflicts)' .` fails to compile on `ErrExtensionConflict`, then fails on the equal row (panic), the conflict rows and the concurrent call count.
- [x] 3.2 GREEN: implement the design D2 registry (`extMu`, `extensionID`, installing/installed/disabled entries with a `done` channel and owning session ID, waiters re-evaluate) and the D5 repeat rules (`reflect.DeepEqual` of values and effective options; scope-name collision as `ErrExtensionConflict`, recovering the duplicate-scope panic); verify the 3.1 command passes with `-count=10` and `make test` is green.
- [x] 3.3 REFACTOR: *Extract Function* `claim(session, id) (entry, wait <-chan struct{}, owner bool, err error)` and `release(id, outcome)` around the registry; verify `make test` and `make lint` pass.
- [x] 3.4 Godoc for `ErrExtensionConflict` and the identity rules on `Extend` (type plus instance, options as data, pointer constructors); verify `go doc ErrExtensionConflict` shows it and `make lint` passes.

## 4. Rollback of a failing Extend

- [x] 4.1 RED: add `TestExtend_FailureRollback` (rows error, error_panic, string_panic; scenario "Failure is reported and can be retried"), `TestExtend_FlattenNoRollback` and `TestExtend_RollbackFiresHooks` (registration, shutdown and scope Closing/Closed hooks of the private scope; the failing attempt builds its private service first, and the test asserts its `Shutdown` was called once); verify `go test -race -run 'TestExtend_(FailureRollback|FlattenNoRollback|RollbackFiresHooks)' .` fails to compile on `ExtensionError`, then fails because the scope stays attached (the retry hits the reserved name) and panics escape; `FlattenNoRollback` and the registration-hook part of `RollbackFiresHooks` pass on arrival, so show they fail by temporarily removing root registrations on flattened failure and by suppressing hooks in the private scope, and show the `Shutdown` assertion fails by temporarily rolling back with a cancelled context.
- [x] 4.2 GREEN: implement design D4 rollback (shut the private scope down with `context.Background()`, detach it with a new internal `Scope.detachChild(name)`, remove already-applied exports with a new internal `Scope.removeService(name)`, delete the identity entry, close `done`) and D7 (`ExtensionError` with `Unwrap`, panic recovery); verify the 4.1 command passes and `make test` is green.
- [x] 4.3 REFACTOR: *Extract Function* `rollbackInstall(root, plan, appliedExports)`; verify `make test` and `make lint` pass.
- [x] 4.4 Godoc for `ExtensionError` and the rollback contract on `Extend` (scope removed, hooks fire, required extensions stay, no rollback when flattened); verify `go doc ExtensionError` shows it and `make lint` passes.

## 5. Disabled extensions, Require and cycles

- [x] 5.1 RED: add `TestExtend_Disabled`, `TestRequire_Disabled`, `TestRequire_Idempotent`, `TestRequire_Cycle`, `TestRequire_CrossGoroutineCycle` (channel handshake), `TestExtend_AfterRequire` (rows unresolved_replaces, unrelated_root_invoked, resolved_conflicts) and the export and require rows of `TestExtensionContext_AfterExtend`, the cycle tests in `testWithTimeout`; verify `go test -race -run 'TestExtend_(Disabled|AfterRequire)|TestRequire_|TestExtensionContext_AfterExtend' .` fails to compile only on `ErrExtensionDisabled`, `ErrRequiredExtensionDisabled`, `ErrExtensionCycle` and `ExtensionContext.Require`.
- [x] 5.2 GREEN: implement design D6 (`Require` in the caller's session, session-stack cycles, disabled entries with one `Logf` line, `ErrRequiredExtensionDisabled` from `Require`), the D2 waits-for graph (cross-session cycles as `ErrExtensionCycle`), the D5 replacement of a `viaRequire` install judged by the private scope's own `orderedInvocation` and its descendants', and the D1 `closed` flag on the context; verify the 5.1 command passes with `-count=10` and `make test` is green.
- [x] 5.3 REFACTOR: *Extract Function* `installInSession(session, ext, opts)` shared by `Extend` and `Require`, and `waitsForCycle(graph, waiter, owner)`; verify `make test` and `make lint` pass.
- [x] 5.4 Godoc for `Require`, `ErrExtensionDisabled`, `ErrRequiredExtensionDisabled` and `ErrExtensionCycle` (idempotent Require, cycles across goroutines, the explicit-after-require rule, the context ends when `Extend` returns); verify `go doc ExtensionContext.Require` shows it and `make lint` passes.

## 6. Extend follows the container phase

- [x] 6.1 RED: add `TestExtend_Phases` (rows idle, starting, started, ready, failed, closing, closed; scenario "Extend in each phase"), with the starting row calling `Extend` from a `ContainerStarting` hook, the started row from a `ContainerStarted` hook and the closing row from a `ContainerClosing` hook; verify `go test -race -run TestExtend_Phases .` fails on the closing and closed rows (Extend succeeds today); the other rows pass on arrival from 1.2, so show they fail by temporarily rejecting every non-idle phase.
- [x] 6.2 GREEN: implement the design D3 phase check returning an error matching `ErrStartAfterShutdown`; verify the 6.1 command passes and `make test` is green.
- [x] 6.3 REFACTOR: none needed - one guard; verify `make test` and `make lint` pass.

## 7. Application wins, lifecycle and the root health check

- [x] 7.1 RED: add `TestExtend_AppServiceWins` (rows before, after; scenario "The application's client wins"), `TestExtend_AppServiceCheckedOnce` and `TestExtend_Lifecycle` (scenario "Lifecycle of extension services"); verify `go test -race -run 'TestExtend_(AppServiceWins|AppServiceCheckedOnce|Lifecycle)' .` fails on the health-map keys of `Lifecycle` (no `ext:redis/*redis.Pool`, an extra `*redis.Pool` key); `AppServiceWins`, `AppServiceCheckedOnce` (the root check does not walk extension scopes yet) and the start/shutdown assertions pass on arrival, so show they fail by temporarily registering the test extension's client without `AsDefault()` and by including yielded defaults in 7.2's walk.
- [x] 7.2 GREEN: implement design D8 in `RootScope.HealthCheckWithContext` (own services of each installed private extension scope keyed `<scope name>/<service name>`, yielded defaults and root exports sourced from extension scopes skipped, unchanged path without extensions); verify the 7.1 command passes and `make test` is green with the existing health-check suite unchanged.
- [x] 7.3 REFACTOR: *Extract Function* `extensionScopes()` returning the installed private scopes in a stable order; verify `make test` and `make lint` pass.
- [x] 7.4 Document the root `HealthCheck` behavior change in `docs/docs/service-lifecycle/healthchecker.md` and on a new page under `docs/docs/upgrading/`; verify `npm run build` in `docs/` passes.

## 8. Clone copies installed extensions

- [x] 8.1 RED: add `TestExtend_Clone` (scenario "Extend on a clone"); verify `go test -race -run TestExtend_Clone .` fails with `ErrExtensionConflict` (the cloned "ext:redis" child exists while the clone's registry has no entry for it).
- [x] 8.2 GREEN: copy installed and disabled registry entries and the scope-to-identity index in `CloneWithOpts` after the scope tree (design D10); verify the 8.1 command passes and `make test` is green.
- [x] 8.3 REFACTOR: *Extract Function* `copyExtensionsTo(clone)` next to `copyKindsTo`; verify `make test` and `make lint` pass.

## 9. The environment property source

- [x] 9.1 RED: in `properties_test.go` add `TestEnvPropertySource_Mapping` (5 rows; scenario "Key to variable mapping"), `TestEnvPropertySource_Distinct` and `TestEnvPropertySource_Unset`, using `t.Setenv`; verify `go test -race -run TestEnvPropertySource .` fails to compile only on `PropertySource` and `EnvPropertySource`.
- [x] 9.2 GREEN: add `PropertySource` and `EnvPropertySource(prefix)` with the design D9 mapping over `os.LookupEnv`; verify the 9.1 command passes and `make test` is green.
- [x] 9.3 REFACTOR: *Extract Function* `envName(prefix, key string) string`; verify `make test` and `make lint` pass.

## 10. Properties views, source selection, providers and secrecy

- [x] 10.1 RED: add `TestProperties_Prefixed` (3 rows), `TestProperties_InvalidKeys` (8 rows), `TestPropertySource_Selection` (rows env, service, opts, unrelated_lookup_ignored), `TestPropertySource_LateServiceIgnored`, `TestPropertySource_LateSameNamePanics`, `TestPropertySource_ConcurrentFirstRead`, `TestProperties_Secrecy` (map source holding a password; asserts every log line including the source debug line), `TestExtensionProperties_Clone` (rows root, clone), `TestExtensionProperties_OutsideExtension` and the properties row of `TestExtensionContext_AfterExtend`; verify `go test -race -run 'TestProperties_|TestPropertySource_|TestExtensionProperties_|TestExtensionContext_AfterExtend' .` fails to compile only on `Properties`, `ExtensionContext.Properties`, `ExtensionProperties` and `InjectorOpts.PropertySource`.
- [x] 10.2 GREEN: implement design D9 (`Properties` view with the compiled key regexp and the context's `closed` flag; once-per-root source decision with a stored result that other readers wait for; selection by `NameOf[PropertySource]()`; `InjectorOpts.PropertySource` copied by `copy()`; `ExtensionProperties` through the scope-to-identity index; debug log with `%T` only); verify the 10.1 command passes with `-count=10` and `make test` is green.
- [x] 10.3 REFACTOR: *Extract Function* `decidePropertySource(root) PropertySource`; verify `make test` and `make lint` pass.
- [x] 10.4 Godoc for `PropertySource`, `EnvPropertySource`, `Properties`, `ExtensionProperties` and `InjectorOpts.PropertySource` (key grammar, mapping, selection by name, decided at first read, providers use `ExtensionProperties`, re-entrant reads from the source's provider unsupported, values never logged); verify `go doc EnvPropertySource` shows it and `make lint` passes.

## 11. Documentation and integration

- [x] 11.1 Add `ExampleExtend`, `ExampleExtensionExport` and `ExampleEnvPropertySource` in `extension_example_test.go` with a small in-file extension, and the new `docs/docs/extensions/` section (`_category_.json`, `authoring.md`: contract, optional interfaces, `AsDefault` + export, options as data, pointer constructors, `Require`, `ExtensionProperties`, precedence convention, `ErrExtensionDisabled`; `installing.md`: `Extend`, `Flatten`, `ExtendScopeName`, instances, `InjectorOpts.PropertySource`, and that extension services are shut down by root `Shutdown`, linking the Shutdowner page's "Shutdown with a deadline" section); verify `go test -run 'ExampleExtend|ExampleExtensionExport|ExampleEnvPropertySource' .` and `npm run build` in `docs/` pass.
- [x] 11.2 Benchmark check: rerun the 0.1 command into `<scratch>/bench-after.txt` and `benchstat` it against the baseline; verify no statistically significant regression in `HealthCheck` or `InvokeAs` without extensions (record the output in the task evidence).
  - Evidence (benchstat, n=10, Apple M4 Pro): InvokeAs 103.0n → 102.1n (~, p=0.617); HealthCheck 45.49µ → 42.86µ (−5.77%, p=0.043, faster); B/op and allocs/op unchanged (144 B / 4 allocs; 34.15 KiB / 625 allocs).
- [x] 11.3 Integration check: `make test`, `make lint`, and `go build ./...` plus `go test ./...` in every `go.work` module under `http/*` and `examples/*`; verify all pass.
- [x] 11.4 Cross-check every `#### Scenario:` header in this change's specs against a passing Go test from the design Test Strategy table; verify none is missing and `openspec validate add-extensions --strict` passes.

## Workflow follow-up

- Archive this change after review, after `add-default-registrations` and `add-scope-export`.
- Commits and PRs for this change carry no AI attribution lines.
