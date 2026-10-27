# Tasks

Prerequisites: `consolidate-pending-lifecycle-work` and `add-default-registrations` are archived (this change adds to `service-aliases` and `collection-invocation`, and reuses `invokeWrapperInScope`, `registerLocked` and `serviceDefault` from `add-default-registrations`), and `make test` is green before starting. Read design.md (D1–D8, Risks and the Test Strategy table) before each group. Production code lives in a new `export.go`, its tests in `export_test.go`; the cross-scope shutdown pass (group 10) lives in a new `export_shutdown.go`, and the shutdown ordering is tested in `scope_test.go`.

## 0. Baseline

- [x] 0.1 Record the benchmark baseline with `go test -run '^$' -bench . -benchmem -count 10 ./bench/ > <scratch>/bench-before.txt` (`BenchmarkStart` exists from `add-default-registrations`); verify the file exists and `make test` passes.

## 1. Export registration and resolution

- [x] 1.1 RED: add `TestExport_Publishes` (3 rows; scenario "Export publishes under the source or a chosen name"), `TestExport_OthersStayPrivate`, `TestExport_Chained`, `TestExport_ResolvesFromSource` (3 rows), `TestExport_ListedAsAlias`, `TestExport_ConcurrentFirstInvocation` (100 goroutines), `TestExport_Invalid` (6 rows), `TestExport_SourceMustBeOwn`, `TestExport_DirectChildOnly` (100 runs), `TestExport_ReplacesUnresolvedDefault` and `TestMustExport_Panics` (2 rows); verify `go test -race -run 'TestExport_(Publishes|OthersStayPrivate|Chained|ResolvesFromSource|ListedAsAlias|ConcurrentFirstInvocation|Invalid|SourceMustBeOwn|DirectChildOnly|ReplacesUnresolvedDefault)|TestMustExport_Panics' .` fails to compile only on `Export`, `ExportNamed`, `MustExport`, `MustExportNamed`, `ExportOption` and `ExportAs`.
- [x] 1.2 GREEN: add `export.go` with the exported API (design Public API), `serviceExport[T]` (design D1: `ServiceTypeAlias`, `exportSource()` marker), resolution by *direct* child scope name (D2) through `invokeWrapperInScope` with the export edge, relying on the existing scope-qualified cycle check (D3), validation and registration through `registerLocked` returning its error (D7); `shutdown` a no-op and `isShutdowner`/`isHealthchecker` false for now; verify the 1.1 command passes and `make test` is green.
- [x] 1.3 REFACTOR: *Extract Function* `validateExport(from, sourceName, publishedName, target reflect.Type) error` and *Introduce Parameter Object* `exportOptions` for `ExportAs`; verify `make test` and `make lint` pass.
- [x] 1.4 Godoc for `Export`, `ExportNamed`, `MustExport`, `MustExportNamed`, `ExportOption` and `ExportAs` (source must be registered in `from` itself, published name, errors like `As`, the child keeps ownership); verify `go doc Export` shows it and `make lint` passes.

## 2. Lifecycle of an export

- [x] 2.1 RED: add `TestExport_Lifecycle` (rows start, shutdown, healthcheck; scenario "Exported Starter and Shutdowner run once"), `TestExport_RootHealthCheck`, `TestExport_SourceScopeHealthCheck`, `TestExport_TargetShutdownOnce` (rows root, published_name, owning_scope) and `TestExport_StartOrder`; verify `go test -race -run 'TestExport_(Lifecycle|RootHealthCheck|SourceScopeHealthCheck|TargetShutdownOnce|StartOrder)' .` fails on the healthcheck row, `RootHealthCheck` (no delegation yet) and the `explicit_source` row of `SourceScopeHealthCheck` (every registered name gets a result key, so the export key "pool" is reported with a nil error; its `default_source` row is added in 6.1); the start, shutdown, `TargetShutdownOnce` and `StartOrder` cases pass on arrival from 1.2 (alias type, no-op shutdown, recorded edge), so show they fail by temporarily reporting `ServiceTypeLazy` and forwarding `shutdown` to the source (the shutdown row and the `root` row of `TargetShutdownOnce` only fail that way once the export-aware shutdown order of 3.2 is in, because children-first shutdown removes the source before the export; re-check them after 3.2).
- [x] 2.2 GREEN: implement the design D4 table (health check delegating to the source wrapper) and pass the requesting scope down `Scope.asyncHealthCheckWithContext`'s ancestor walk so an export is skipped when the requester is its source scope or a descendant of it; verify the 2.1 command passes and `make test` is green, including the existing health-check suite unchanged.
- [x] 2.3 REFACTOR: *Introduce Parameter Object* for the health-check walk (`healthCheckRequest{ctx, requester}`) if the signature grows past two parameters; otherwise none needed; verify `make test` and `make lint` pass.

## 3. Shutdown order across exports

- [x] 3.1 RED: in `scope_test.go` add `TestScopeShutdown_ExportOrder` (rows direct, transitive, declared; scenario "Parent dependents shut down before the exporting child"), `TestScopeShutdown_ChainedExportOrder`, `TestScopeShutdown_SiblingBeforeExportDependent`, `TestScopeShutdown_WithoutExportsUnchanged`, `TestScopeShutdown_ExportingChildHookOrder` (scenario "Closing and Closed around an exporting child") and `TestExport_ShutdownOrderRepeated` (direct row 200× under `-race`), each in `testWithTimeout` (the `default_source` and `cloned` rows of `ExportOrder` are added in 6.1 and 7.1); verify `go test -race -run 'TestScopeShutdown_(ExportOrder|ChainedExportOrder|SiblingBeforeExportDependent|WithoutExportsUnchanged|ExportingChildHookOrder)|TestExport_ShutdownOrderRepeated' .` fails with a dependent that saw the pool closed (the audit's V1) and on the hook order; `WithoutExportsUnchanged` passes on arrival, so show it fails by temporarily applying the four-step order to every scope with a reversed step 1/3.
- [x] 3.2 GREEN: implement design D5 in `Scope.shutdownWithContext` (four steps when the scope holds exports: non-exporting children, own services with a dependency path to an export plus the export wrappers, exporting children, remaining own services; the scope's Closing hook before step 2 and Closed hook after step 4, so exporting children close inside the pair); verify the 3.1 command passes with `-count=5` and `make test` is green.
- [x] 3.3 REFACTOR: *Extract Function* `exportDependents(scope)` (the reverse DAG walk) and `partitionChildren(scope)`; verify `make test` and `make lint` pass.
- [x] 3.4 Document the export shutdown order and the scope-hook exception in the godoc of `Export` and `InjectorOpts.HookScopeClosing`, in the "Scope export" section of `docs/docs/container/scope.md`, and on the upgrading page (behavior change for exporting scopes); verify `go doc Export` shows it and `npm run build` in `docs/` passes.

## 4. Collections and type selection see exports

- [x] 4.1 RED: add `TestExport_CollectionMembership` (rows root, app, redis; scenario "Collection membership of an export"; the `default_source_*` rows are added in 6.1) and `TestExport_InvokeAs`; verify `go test -race -run 'TestExport_(CollectionMembership|InvokeAs)|TestInvokeAll_AliasExcluded' .` fails on the root and app rows (the export is skipped as an alias); `TestExport_InvokeAs`, the redis row and `TestInvokeAll_AliasExcluded` pass on arrival, so show the first two fail by temporarily excluding exports from by-type selection and the redis row by temporarily including exports everywhere.
- [x] 4.2 GREEN: in `collectionCandidatesFor` keep exports except when the reader's scope is the export's source scope or one of its descendants (design D6); verify the 4.1 command passes and `make test` is green with the existing collection tests unchanged.
- [x] 4.3 REFACTOR: *Extract Function* `readerSeesSource(reader, export)`; verify `make test` and `make lint` pass.

## 5. Cycles, clone and source-scope shutdown

- [x] 5.1 RED: add `TestExport_Cycle`, `TestExport_Clone` and `TestExport_AfterSourceShutdown`; verify `go test -race -run 'TestExport_(Cycle|Clone|AfterSourceShutdown)' .` fails on `Clone` (the uncloned wrapper resolves into the original container); `Cycle` and `AfterSourceShutdown` pass on arrival from 1.2 (the existing scope-qualified chain check, lookup by name), so show they fail by temporarily skipping the whole chain check and caching the source wrapper pointer.
- [x] 5.2 GREEN: implement `serviceExport.clone` (design D4: same names, fresh frames, bound to the new owner); verify the 5.1 command passes and `make test` is green.
- [x] 5.3 REFACTOR: none needed - one method; verify `make test` and `make lint` pass.

## 6. An export of a default is a default

- [x] 6.1 RED: add `TestExport_DefaultSource` (6 rows; scenario "The application's service wins over an exported default"), `TestExport_DefaultOverExplicitSkipped`, and the `default_source` rows of `TestScopeShutdown_ExportOrder`, `TestExport_CollectionMembership` (2) and `TestExport_SourceScopeHealthCheck`; verify `go test -race -run 'TestExport_(DefaultSource|DefaultOverExplicitSkipped|CollectionMembership|SourceScopeHealthCheck)|TestScopeShutdown_ExportOrder' .` fails on the app_first rows (duplicate error), the app_after rows (duplicate panic) and the export_only rows (`ErrCircularDependency` from the existing scope-qualified cycle check: the private default yields to the explicit export, which resolves the private default again in the same scope); the other `default_source` rows pass on arrival while the export is still explicit, so show they fail by temporarily wrapping it as a default without `asExport` unwrapping it.
- [x] 6.2 GREEN: implement design D8 (an export whose source is a `serviceDefault` is wrapped as a default in the parent and follows `registerLocked`'s default rules; `Export` returns nil when skipped; `serviceDefault` unwraps its export (`unwrapExport`, used by `asExport`) while unresolved or chosen, and reports none once yielded); verify the 6.1 command passes and `make test` is green.
- [x] 6.3 REFACTOR: *Extract Function* `wrapExportIfDefault(source, export)`; verify `make test` and `make lint` pass.
- [x] 6.4 Document exports of defaults in the godoc of `Export` and on the "Default registrations" docs page; verify `go doc Export` shows it and `npm run build` in `docs/` passes.

## 7. Zero cost without exports

- [x] 7.1 RED: add `TestScope_ExportCount` asserting the per-scope count after exporting, overriding the published name, shutting the parent down and cloning (design D7 mechanism), plus the `cloned` row of `TestScopeShutdown_ExportOrder`; verify `go test -race -run TestScope_ExportCount .` fails to compile only on the missing counter accessor; the `cloned` row passes on arrival (every scope still takes the four-step shutdown), so once 7.2 is in, show it fails by temporarily leaving the clone's `exportCount` at zero.
- [x] 7.2 GREEN: add the atomic per-scope `exportCount`, updated where an export enters or leaves a scope's map and set by `Scope.clone` from the wrappers it copies, and guard the four-step shutdown, the collection check and the health skip on it; verify the 7.1 command passes and `make test` is green.
- [x] 7.3 REFACTOR: none needed - one counter and two guards; verify `make test` and `make lint` pass.
- [x] 7.4 Benchmark gate: `go test -run '^$' -bench . -benchmem -count 10 ./bench/ > <scratch>/bench-after.txt` and `benchstat bench-before.txt bench-after.txt`; verify no statistically significant regression in `InvokeAll`, `InvokeAs`, `Shutdown` or `Start` (record the output in the task evidence).

## 8. Documentation and integration

- [x] 8.1 Add `ExampleExport` (private pool in a child scope, client exported and used by an app in the root) and `ExampleExportAs` in `export_example_test.go`, and complete the "Scope export" section of the scopes docs page (Defaults & Control table, ownership, collections, defaults); verify `go test -run 'ExampleExport' .` and `npm run build` in `docs/` pass.
- [x] 8.2 Integration check: `make test`, `make lint`, and `go build ./...` plus `go test ./...` in every `go.work` module under `http/*` and `examples/*`; verify all pass.
- [x] 8.3 Cross-check every `#### Scenario:` header in this change's specs against a passing Go test from the design Test Strategy table; verify none is missing and `openspec validate add-scope-export --strict` passes.

## 9. Review findings: RED

Findings 1 to 3 of the PR #12 review reproduce (throwaway probes: 50/50 and 200/200 wrong orders, 361/500 runs with the pool shut first, a circular dependency error). Finding 5 could not be reproduced and is handled as a simplification in group 12.

- [x] 9.1 RED: in `scope_test.go` add `TestScopeShutdown_ChildDependsOnParentDependent` (rows direct, chain; each 50 runs; scenario "An exporting child's service that depends on a parent dependent shuts down first"), `TestScopeShutdown_ExportingSiblings` (200 runs; scenario "Exporting siblings order shutdown through their exports"), the `child_depends_on_parent` row of `TestScopeShutdown_ExportingChildHookOrder` and `TestScopeShutdown_ExportingChildJoin` (synctest: a `Shutdown` of the exporting child while root `Shutdown` is in the pass joins it; each hook and service runs once), each in `testWithTimeout`; in `export_test.go` add `TestExport_InvokeAsSkipsOwnExport` (scenario "A provider does not select its own export by type"); verify `go test -race -run 'TestScopeShutdown_(ChildDependsOnParentDependent|ExportingSiblings|ExportingChildHookOrder|ExportingChildJoin)|TestExport_InvokeAsSkipsOwnExport' .` fails with the wrong orders (direct: "p" before "x"; chain: the pool before "x"), the pool shut before "x" in some sibling runs, the hook order, and `ErrCircularDependency` (`dec` -> `api` -> `dec`); if `ExportingChildJoin` passes on arrival, show it fails by temporarily skipping `joinClose` for exporting descendants once 10.1 is in.

## 10. Cross-scope shutdown pass: GREEN

- [x] 10.1 GREEN: implement design D5 (pass set of the scope and its exporting descendants, their non-exporting children first, one DAG snapshot, a no-dependents loop over the snapshot with the parallel fallback only for true cycles, lazy Closing/Closed hooks of exporting descendants, the join and start-wait protocol for each); verify the group 9 shutdown tests pass with `-count=5`, `TestExport_ShutdownOrderRepeated` and every existing shutdown, hook and join test pass, and `make test` is green.
- [x] 10.2 REFACTOR: *Remove Dead Code* `partitionChildren`, `exportDependents` and `DAG.listDependentsWithin` if the pass replaced them, and *Extract Function* the snapshot (`DAG.snapshotDependents(set)`) and the pass loop; verify `make test` and `make lint` pass after each step.

## 11. Own export in by-type selection: GREEN

- [x] 11.1 GREEN: extend `aliasResolvesTo` (design D6) to follow an export to its source and compare scope and name with the invoking provider; verify `TestExport_InvokeAsSkipsOwnExport` passes, `TestExport_InvokeAs` and the existing `InvokeAs` and decorator tests pass unchanged, and `make test` is green.
- [x] 11.2 REFACTOR: none needed - one branch in an existing loop; verify `make test` and `make lint` pass.

## 12. Review cleanups: REFACTOR

- [x] 12.1 *Change Function Declaration*: `validateExport` returns the source wrapper it found, and `ExportNamed` passes it to `wrapExportIfDefault` instead of looking it up again (design D7; finding 5, not reproducible, so a simplification, not a defect fix); verify `make test` passes.
- [x] 12.2 *Extract Function* `excludedAlias(service, reader)` with one `asExport` call, used by both collection walks (design D6; finding 7); verify `make test` passes.
- [x] 12.3 *Combine Functions*: replace `unwrapExport() (serviceWrapperExport, bool)` and `decoratesExport()` with one `unwrapExport() (export, yielded)`, and derive `asExport` and `wrapsExport` from it (design D1, D8; finding 8); verify `make test` and `make lint` pass.

## 13. Documentation, gate and verification

- [x] 13.1 Update the godoc of `Export` and the "Shutdown order" section of `docs/docs/container/scope.md` (dependency order across the scope and its exporting descendants, exporting siblings, child services that use parent dependents); verify `go doc Export` shows it and `npm run lint` and `npm run build` in `docs/` pass.
- [x] 13.2 Benchmark gate: `go test -run '^$' -bench '^Benchmark(Shutdown|InvokeAll|InvokeAllParallel|InvokeAs|Start)$' -benchmem -count 10 ./bench/` against `bench-before.txt` with `benchstat`; verify no statistically significant regression in time, B/op or allocs/op (record the output in the task evidence).
  - Evidence (`benchstat bench-before.txt bench-after.txt`, count 10): no statistically significant regression in time, B/op or allocs/op. Time: InvokeAll/10 −2.67%, /100 −2.72%, /1000 −3.72%, Start −2.75%, Shutdown −4.18%, InvokeAs and InvokeAllParallel ~. Shutdown B/op −0.05%, allocs 554 → 553. An interim run found the pass moved the caller's `hookPanic` to the heap on every shutdown (+1 alloc, ~64 B); fixed before the gate by passing it as a parameter instead of storing it in the pass.
- [x] 13.3 Integration and cross-check: `make test`, `make lint`, build and tests of every `go.work` module; every `#### Scenario:` header has a passing test from the Test Strategy table; `openspec validate add-scope-export --strict` passes.

## 14. Verification findings: RED

The `/opsx:verify` run found branches of the shutdown pass with no test (warnings 1-3) and three smaller gaps (report contents, a chained own export). The behavior is implemented by 10.1 and 11.1, so these tests are expected to pass on arrival.

- [x] 14.1 RED: in `scope_test.go` turn `TestScopeShutdown_ExportingChildJoin` into a table (rows root_first, the existing case, and child_first; scenario "Shutdowns of an exporting child join its parent's shutdown"), and add `TestScopeShutdown_ExportingChildJoinAfterPanic` (synctest, the panic row), `TestScopeShutdown_ExportCycle`, `TestScopeShutdown_ExportReport`, `TestScopeShutdown_ExportingChildWaitsForStart` (synctest) and the `no_service_left` row of `TestScopeShutdown_ExportingChildHookOrder`; in `export_test.go` turn `TestExport_InvokeAsSkipsOwnExport` into a table (rows direct, chained); each shutdown test in `testWithTimeout`. Verify `go test -race -run 'TestScopeShutdown_(ExportingChildJoin|ExportingChildJoinAfterPanic|ExportCycle|ExportReport|ExportingChildWaitsForStart|ExportingChildHookOrder)|TestExport_InvokeAsSkipsOwnExport' .`. Each test that passes on arrival must be shown to fail under its temporary mutation, reverted right after: child_first - `join` treats a child already shutting down elsewhere as joined (skip `waitForScopeClose`); AfterPanic - `release` is a no-op (the joined `jobs.Shutdown()` never returns); ExportCycle - `readyServices` returns nothing when no service is ready (the pass stops with services left); WaitsForStart - `join` skips `waitForStartingRun`; ExportReport - `mergedReport` drops the children's reports; no_service_left - `settle` skips the late HookScopeClosing; chained - `aliasResolvesTo` stops after the first export hop. A test that fails on arrival is a defect: record it under a "Defects" note here with its failing output and stop for 15.1.
  - Correction (found while applying, not defects): `DeclareDependency` refuses an edge that closes a cycle, so `ExportCycle` builds its cycle through an injector the pool's provider keeps; and a shutdown that outlives its context does not call the Shutdown of a service still starting, with or without exports (probe: `tenant/w` and `jobs/w` both report `context deadline exceeded`), so `ExportingChildWaitsForStart` asserts that error instead of a shutdown. The two dependency-declaration scenarios were corrected accordingly.

## 15. Verification findings: GREEN

- [x] 15.1 GREEN: only if 14.1 recorded a defect, fix it with the simplest change in `export_shutdown.go` or `invoke.go`; otherwise none needed. Verify the 14.1 command passes with `-count=5` and `make test` is green.
- [x] 15.2 REFACTOR: none expected; if 15.1 changed code, apply the smallest named refactoring it needs. Verify `make test` and `make lint` pass.

## 16. Coverage and validation

- [x] 16.1 Hot-path coverage: `go test -race -coverprofile` on the root package; verify every branch of `export_shutdown.go` is covered except panics inside the per-service goroutines, and record the per-function figures here. The "source gone" branch of `aliasResolvesTo` stays uncovered: by-type selection of an export whose source scope is gone has no specified behavior.
  - Evidence (`go test -race -coverprofile`, root package): every function of `export_shutdown.go` at 100.0%, no uncovered block (`shutdownWithExports`, `add`, `join`, `release`, `shutdownOtherChildren`, `shutdownServices`, `readyServices`, `serviceSet`, `hasRunningDependent`, `aliveServices`, `open`, `shutdownRound`, `settle`, `closable`, `mergedReport`, `exportingChildren`, `removeChild`); `aliasResolvesTo` 85.7%, its remaining blocks being the "source gone" branch noted above and the existing alias-loop bound.
- [x] 16.2 Integration and cross-check: `make test`, `make lint`, build and tests of every `go.work` module; every `#### Scenario:` header has a passing test from the Test Strategy table; `openspec validate add-scope-export --strict` passes.

## Workflow follow-up

- Archive this change after review, before applying `add-extensions` (which builds on it).
- Commits and PRs for this change carry no AI attribution lines.
