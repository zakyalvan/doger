# Tasks

Prerequisite: `add-start-lifecycle-and-kinds` has landed: the registration index unique per root container and copied on clone, and `ProvideOption` metadata. Commands assume the root module `github.com/kartaladev/doger`. Tests live in `invoke_all_test.go` and `invoke_all_order_test.go` (package `doger`); Examples in `di_example_test.go`. Load `golang-how-to`, then `golang-testing`, `golang-stretchr-testify`, `golang-concurrency` and `golang-refactoring`. The docs page is `docs/docs/service-invocation/collection-invocation.md`.

## 1. InvokeAs behavior is unchanged (characterization baseline)

- [ ] 1.1 RED: add `TestInvokeAs_HookReceivesTypeName` (scenario "InvokeAs hooks receive the requested type name"); verify `go test -race -run TestInvokeAs_HookReceivesTypeName .` passes on arrival (characterization), record that, and show it can fail by temporarily passing the real service name to the hooks in `invokeByGenericType` (not committed)
- [ ] 1.2 GREEN: no production change; this test guards the D3 extraction in 2.3; verify `make test` is green
- [ ] 1.3 REFACTOR: none needed, since no code changed; verify `make test` and `make lint` pass

## 2. Collect every visible service assignable to a type

- [ ] 2.1 RED: add `TestInvokeAll_Membership` (scenario "Membership by registered type", 4 rows), `TestInvokeAll_EagerMembers` (scenario "Eager values are members without a provider call"), `TestInvokeAll_NilInjector` (scenario "Nil injector uses the default root scope") and `TestInvokeAll_TypedNilMember` (scenario "Typed-nil member is returned as is"); verify `go test -race -run 'TestInvokeAll_(Membership|EagerMembers|NilInjector|TypedNilMember)' .` fails to compile only on the missing `InvokeAll`
- [ ] 2.2 GREEN: add `InvokeAll[T]` in `di.go` backed by a pipeline in `invoke.go` that filters `serviceForEachRec` by `serviceCanCastToGeneric[T]` only and builds members sequentially, copying the per-member logic of `invokeByGenericType` with the member name for hooks (design D3); verify the 2.1 command passes and `make test` is green
- [ ] 2.3 REFACTOR: *Extract Function* `invokeServiceInstance(injector, vScope, hookName, realName, scope, service)` (design D3) and call it from `invokeByGenericType` (hookName = type name) and the collection pipeline (hookName = member name); verify `go test -race -run 'TestInvokeAs|TestMustInvokeAs|TestInvokeByGenericType|TestInvokeAll' .`, `make test` and `make lint` pass
- [ ] 2.4 Add `ExampleInvokeAll` (members by registered type) and godoc on `InvokeAll` (registered-type membership, the `InvokeAll[any]` sharp edge, per-member hooks, snapshot semantics); verify `go test -run ExampleInvokeAll$ .` passes
- [ ] 2.5 Create `docs/docs/service-invocation/collection-invocation.md` with the membership section; verify `npm --prefix docs run build` succeeds

## 3. Visibility, shadowing and alias exclusion

- [ ] 3.1 RED: add `TestInvokeAll_Visibility` (scenario "Scope visibility and shadowing", 5 rows), `TestInvokeAll_AnyType` (scenario "Interface type any matches every non-alias service"), `TestInvokeAll_ProviderScopeVisibility` (scenario "Collection inside a provider sees the provider's scope") and `TestInvokeAll_AliasExcluded` (scenario "Alias does not duplicate its target"); verify `go test -race -run 'TestInvokeAll_(Visibility|AnyType|ProviderScopeVisibility|AliasExcluded)' .` fails on the shadowing rows `x/*BHandler` and `x/*sql.DB` and on the alias duplicates; ProviderScopeVisibility passes on arrival through `virtualScope` delegation, so record that and show its assertion can fail by temporarily registering "b" in the root scope (not committed)
- [ ] 3.2 GREEN: keep the first occurrence of each name before type filtering (nearest wins) and drop `ServiceTypeAlias` (design D1 steps 1, 3, 4); verify the 3.1 command passes and `make test` is green
- [ ] 3.3 REFACTOR: *Extract Function* `collectionCandidates[T](injector) []collectionCandidate` (name, scope, depth, registration index, service; not `candidate`, which start-lifecycle already declares in the same package) for reuse by the map variant; verify `make test` and `make lint` pass
- [ ] 3.4 Add the visibility, shadowing and alias section to the docs page and godoc (shadowing by a non-matching nearest service; provider-scope visibility); verify `npm --prefix docs run build` succeeds

## 4. Empty results, failure semantics and MustInvokeAll

- [ ] 4.1 RED: add `TestInvokeAll_Empty` (scenario "Empty results", `InvokeAll` rows), `TestInvokeAll_MemberFailure` (scenario "Member failure modes", 3 rows), and `TestMustInvokeAll_Panics` / `TestMustInvokeAll_Success` (scenarios "Must variants panic with the same error" / "Must variants return the collection on success", `MustInvokeAll` rows); verify `go test -race -run 'TestInvokeAll_(Empty|MemberFailure)|TestMustInvokeAll_(Panics|Success)' .` fails to compile only on `MustInvokeAll`, then on the nil-vs-empty and member-name assertions
- [ ] 4.2 GREEN: return `[]T{}` on no match, wrap the first member error as ``DI: could not invoke collection member `%s`: %w`` and add `MustInvokeAll` via `must1` (design D3, D6, D7); verify the 4.1 command passes and `make test` is green
- [ ] 4.3 REFACTOR: *Extract Function* `wrapMemberError(name string, err error) error`; verify `make test` and `make lint` pass

## 5. Name-keyed collection (InvokeAllMap)

- [ ] 5.1 RED: add `TestInvokeAllMap_Keys` (scenario "Map collection keys", 2 rows) and the `InvokeAllMap`/`MustInvokeAllMap` rows of `TestInvokeAll_Empty`, `TestMustInvokeAll_Panics` and `TestMustInvokeAll_Success`; verify `go test -race -run 'TestInvokeAllMap_Keys|TestInvokeAll_Empty|TestMustInvokeAll_' .` fails to compile only on the missing `InvokeAllMap`/`MustInvokeAllMap`
- [ ] 5.2 GREEN: add `InvokeAllMap` on `collectionCandidates` with the same build loop, returning `map[string]T{}` on no match, and `MustInvokeAllMap` via `must1`; verify the 5.1 command passes and `make test` is green
- [ ] 5.3 REFACTOR: *Extract Function* `buildMembers` shared by the slice and map variants, removing the duplicated loop; verify `make test` and `make lint` pass
- [ ] 5.4 Add `ExampleInvokeAllMap` and godoc on `InvokeAllMap`/`MustInvokeAllMap` (same members as `InvokeAll`, keyed by name, unordered); verify `go test -run ExampleInvokeAllMap .` passes

## 6. A collection excludes the service that invokes it

- [ ] 6.1 RED: add `TestInvokeAll_CompositeExcludesSelf` (scenario "Composite collects every other implementation") and `TestInvokeAll_DeeperCycle` (scenario "Deeper cycles still fail"); verify `go test -race -run 'TestInvokeAll_(CompositeExcludesSelf|DeeperCycle)' .` fails on CompositeExcludesSelf with `ErrCircularDependency`; DeeperCycle passes on arrival, so record that and show it can fail by temporarily excluding every invoker-chain member (not committed)
- [ ] 6.2 GREEN: drop the candidate named like the last entry of the virtual scope's invoker chain (design D3a); verify the 6.1 command passes and `make test` is green
- [ ] 6.3 REFACTOR: *Extract Function* `invokingServiceName(injector) (string, bool)` if the check is inline in `collectionCandidates`, otherwise none needed; verify `make test` and `make lint` pass
- [ ] 6.4 Add the composite section to the docs page and the self-exclusion rule to the `InvokeAll`/`InvokeAllMap` godoc; verify `npm --prefix docs run build` succeeds

## 7. Dependency edges, hooks and snapshot semantics

- [ ] 7.1 RED: add `TestInvokeAll_EdgesOrderShutdown`, `TestInvokeAll_HooksPerMember`, `TestInvokeAll_NoEdgesOutsideProvider`, `TestInvokeAll_TransientFresh`, `TestInvokeAll_Snapshot`, `TestInvokeAll_CallerMutation` and `TestInvokeAll_AfterShutdown` (scenarios "Dependency edges order shutdown", "Invocation hooks per member", "No edges when invoked outside a provider", "Transient and singleton members across calls", "Later registration is not visible in an earlier snapshot", "Caller mutation does not leak", "Collections after shutdown"); verify `go test -race -run 'TestInvokeAll_(Edges|Hooks|NoEdges|TransientFresh|Snapshot|CallerMutation|AfterShutdown)' .`; these pass on arrival through `invokeServiceInstance`, so record that and show EdgesOrderShutdown and HooksPerMember can fail by temporarily skipping `addDependency` and passing the type name as hookName (not committed)
- [ ] 7.2 GREEN: no production change expected; fix only what 7.1 reports; verify the 7.1 command passes and `make test` is green
- [ ] 7.3 REFACTOR: none needed if 7.2 changed nothing, otherwise *Inline Function* any helper made redundant; verify `make test` and `make lint` pass

## 8. Concurrent collection callers

- [ ] 8.1 RED: add `TestInvokeAll_ConcurrentSingleBuild` (100 goroutines), `TestInvokeAll_ConcurrentRegistration` (50 + 50 goroutines) and `TestInvokeAll_ConcurrentShutdown` (50 collectors + 1 shutdown), all with `testWithTimeout`, for scenarios "Concurrent callers build each member once", "Concurrent registration and collection" and "Concurrent collection and shutdown"; verify `go test -race -count=5 -run 'TestInvokeAll_Concurrent' .`; they pass on arrival because the wrappers serialize builds, so record that and show the counter assertion can fail with a deliberately unsynchronized fake provider in a throwaway experiment (not committed)
- [ ] 8.2 GREEN: no production change expected; fix any race, leak or double build reported; verify the 8.1 command passes with `-race` and goleak reports no leak
- [ ] 8.3 REFACTOR: none needed: no new shared state (design D8); verify `make test` and `make lint` pass

## 9. Default collection order

- [ ] 9.1 RED: add `TestInvokeAll_DefaultOrder` (scenario "Default order across scopes", 4 rows), `TestInvokeAll_DefaultOrderStable` (50 services × 20 calls), `TestInvokeAll_OverrideKeepsPosition` and `TestInvokeAll_DefaultOrderClone` (scenarios "Order is stable across repeated calls", "Override replaces a registration in place", "Default order in a clone"); verify `go test -race -count=10 -run 'TestInvokeAll_(DefaultOrder|OverrideKeepsPosition)' .` fails on order assertions (map iteration order)
- [ ] 9.2 GREEN: sort candidates by (scope depth, registration index, name) before building (design D2); verify the 9.1 command passes with `-count=10` and `make test` is green
- [ ] 9.3 REFACTOR: *Introduce Parameter Object*: make `collectionCandidate` carry `depth` and `index` explicitly if they are still loose values; verify `make test` and `make lint` pass
- [ ] 9.4 Add `BenchmarkInvokeAll/{10,100,1000}` and `BenchmarkInvokeAllParallel` to `bench/do_bench_test.go`; verify `go test -run '^$' -bench BenchmarkInvokeAll -benchmem ./bench/` runs and record the results in the change notes

## 10. Ordered members sort ascending and stably

- [ ] 10.1 RED: add `TestInvokeAll_Ordered` (scenario "Ordered values", 6 rows) and `TestInvokeAll_OrderedTransient` (scenario "Order is read from each transient instance", 2 rows); verify `go test -race -run 'TestInvokeAll_Ordered' .` fails to compile only on the missing `Ordered`
- [ ] 10.2 GREEN: add `type Ordered interface{ Order() int }` (in `di_lifecycle.go` or a new `ordered.go`) and the stable ordering pass for `InvokeAll` only, with `Ordered` > `0` (design D4); verify the 10.1 command passes and `make test` is green
- [ ] 10.3 REFACTOR: *Extract Function* `orderValue(name, instance, opts) (int, error)` and *Replace Loop with Pipeline* using `slices.SortStableFunc`; verify `make test` and `make lint` pass
- [ ] 10.4 Add `ExampleOrdered` and godoc on `Ordered` (ascending, missing = 0, stable, collection-only); verify `go test -run ExampleOrdered .` passes

## 11. WithOrder is a registration option for every registration style

- [ ] 11.1 RED: add `TestInvokeAll_WithOrderPrecedence` (scenario "Precedence", 5 rows), `TestInvokeAll_WithOrderHelpers` (scenario "WithOrder on every registration helper", 18 rows), `TestInvokeAll_WithOrderSkipsOrderMethod` (scenario "Order() is not called when WithOrder is set") and `TestWithOrder_NoEffectOnInvoke` (scenario "WithOrder outside collections has no effect"); verify `go test -race -run 'TestInvokeAll_WithOrder|TestWithOrder_' .` fails to compile only on the missing `WithOrder`
- [ ] 11.2 GREEN: add `WithOrder(n int) ProvideOption` storing `*int` in the service metadata, last one wins, and read it first in `orderValue` (precedence WithOrder > Ordered > 0, design D4, D5); verify the 11.1 command passes and `make test` is green
- [ ] 11.3 REFACTOR: none needed, since 11.2 only adds one branch to `orderValue`; verify `make test` and `make lint` pass
- [ ] 11.4 Add `ExampleWithOrder` (third-party type ordered without editing it), godoc on `WithOrder`, and the ordering section of the docs page; verify `go test -run ExampleWithOrder .` passes and `npm --prefix docs run build` succeeds

## 12. Order() panic and map-collection independence

- [ ] 12.1 RED: add `TestInvokeAll_OrderPanic` (scenario "Panicking Order()", 3 rows), `TestMustInvokeAll_OrderPanic` (scenario "MustInvokeAll surfaces the Order() panic as its panic") and `TestInvokeAllMap_IgnoresOrdered` (scenario "Order() not called for map collections"); verify `go test -race -run 'TestInvokeAll_OrderPanic|TestMustInvokeAll_OrderPanic|TestInvokeAllMap_IgnoresOrdered' .` fails because the panic escapes `InvokeAll`; IgnoresOrdered passes on arrival, so record that and show it can fail by temporarily running the ordering pass in `InvokeAllMap` (not committed)
- [ ] 12.2 GREEN: recover inside `orderValue`, wrapping error panics with `%w` and stringifying others, both naming the member (design D4); verify the 12.1 command passes and `make test` is green
- [ ] 12.3 REFACTOR: reuse start-lifecycle's existing `recoverAsError(r any) error` inside `orderValue` (same `%w`/`%v` rule as start-lifecycle D11) instead of a local recover-to-error block; add no new helper; verify `make test` and `make lint` pass

## 13. Ordering never affects lifecycle order

- [ ] 13.1 RED: add `TestInvokeAll_OrderDoesNotAffectShutdown` and `TestInvokeAll_OrderDoesNotAffectStart` (scenarios "Shutdown ignores collection order" and "Start ignores collection order"); verify `go test -race -run 'TestInvokeAll_OrderDoesNotAffect' .`; they pass on arrival, so record that and show each can fail against a throwaway change that feeds `WithOrder` into start and shutdown ordering (not committed)
- [ ] 13.2 GREEN: no production change expected, since `WithOrder` is read only by `orderValue`; verify the 13.1 command passes and `make test` is green
- [ ] 13.3 REFACTOR: none needed; verify `make test` and `make lint` pass
- [ ] 13.4 Add the "collection order vs lifecycle order" section to the docs page; verify `npm --prefix docs run build` succeeds

## 14. Integration checks

- [ ] 14.1 Full run: verify `make test` (race and goleak) and `make lint` pass and `go build ./...` succeeds in every workspace module under `http/` and `examples/`
- [ ] 14.2 Traceability: verify `go test -v -run 'TestInvokeAll|TestInvokeAllMap|TestMustInvokeAll|TestWithOrder|TestInvokeAs_HookReceivesTypeName|Example(InvokeAll|InvokeAllMap|Ordered|WithOrder)' .` lists every test in design.md's Test Strategy as PASS, one per scenario and Examples row
- [ ] 14.3 Add a MODIFIED delta `specs/registration-options/spec.md` (the main spec exists once `add-start-lifecycle-and-kinds` is archived) that copies the `ProvideOption` requirement whole and appends `WithOrder(n)` to its "currently" list, and list `registration-options` under Modified Capabilities in proposal.md; verify `openspec validate add-collection-invocation --strict` passes

## Workflow follow-up

- Propose `InvokeAs[T]` determinism (first match by registration order) as a separate, explicitly flagged behavior change.
- Consider struct-tag `[]T` injection and live collections in phase 2.
- Archive this change after review, once `make test` passes and the scenario-to-test mapping is confirmed.
