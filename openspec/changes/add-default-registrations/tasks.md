# Tasks

Prerequisites: `consolidate-pending-lifecycle-work` is archived (its specs `type-resolution`, `collection-invocation`, `service-aliases` and the current `registration-options`/`container-start` text are main specs), and `make test` is green before starting. Read design.md (D1–D10, Risks and the Test Strategy table) before each group: tasks reference it for detail. Production code for the wrapper lives in a new `service_default.go`, its tests in `service_default_test.go`.

## 0. Baseline

- [ ] 0.1 Add `BenchmarkStart` to `bench/do_bench_test.go` (a root with 50 lazy Starters, `New` + `Start` per iteration) as scaffolding for the D8 gate; verify `go test -run '^$' -bench BenchmarkStart ./bench/` runs and `make test` passes.
- [ ] 0.2 Record the benchmark baseline with `go test -run '^$' -bench . -benchmem -count 10 ./bench/ > <scratch>/bench-before.txt`; verify the file exists.

## 1. AsDefault option and the default wrapper (chosen path)

- [ ] 1.1 RED: add `TestAsDefault_ResolvesWithoutReplacement` (4 rows; scenario "Default resolves when nothing replaces it"), `TestAsDefault_NilInjector` (not parallel), `TestAsDefault_ForwardsWrapperInterfaces` (design Risks), the `as_default` row of `TestProvideOptions_Constructors` and `TestProvideOptions_AsDefaultPanics` (6 rows; scenario "AsDefault adds no registration panic of its own"); verify `go test -race -run 'TestAsDefault_(ResolvesWithoutReplacement|NilInjector|ForwardsWrapperInterfaces)|TestProvideOptions_(Constructors|AsDefaultPanics)' .` fails to compile only on the missing `AsDefault`.
- [ ] 1.2 GREEN: add `AsDefault()` setting `serviceOptions.isDefault`, and `serviceDefault[T]` (design D1) wrapping the inner wrapper in `provide` when set, forwarding every optional wrapper interface, with only the "chosen" path; verify the 1.1 command passes and `make test` is green.
- [ ] 1.3 REFACTOR: *Extract Function* `wrapIfDefault(scope, name, service, options)` out of `provide`; verify `make test` and `make lint` pass.
- [ ] 1.4 Godoc for `AsDefault` (what a default is, that explicit registrations win, every registration form accepted); verify `go doc AsDefault` shows it and `make lint` passes.

## 2. Yielding to a visible non-default service

- [ ] 2.1 RED: add `TestAsDefault_YieldsByPlacement` (rows root, sibling, descendant, none; scenario "Placement decides whether a default yields"), `TestAsDefault_GrandchildYieldsToRoot`, `TestAsDefault_OrderIndependent` (2 rows), `TestAsDefault_Repeated`, `TestAsDefault_DoesNotYieldToDefault`, `TestAsDefault_AliasResolvesThroughDecision` and `TestAsDefault_RealCycleThroughYield`; verify `go test -race -run 'TestAsDefault_(YieldsByPlacement|GrandchildYieldsToRoot|OrderIndependent|Repeated|DoesNotYieldToDefault|AliasResolvesThroughDecision|RealCycleThroughYield)' .` fails on every row that expects "app-dialer" (the default is built instead); the sibling, descendant, none and default-vs-default rows pass on arrival, so show they fail by temporarily making `decide()` always yield to the root.
- [ ] 2.2 GREEN: implement design D2 `decide()` (parent-chain lookup skipping defaults, state under `mu`, atomic fast path) and the yielded delegation through a new `invokeWrapperInScope` (target wrapper invoked directly in its scope with hooks, child virtual scope and a scope-qualified edge, relying on the existing scope-qualified cycle check); verify the 2.1 command passes and `make test` is green.
- [ ] 2.3 REFACTOR: *Extract Function* `lookupNonDefaultAbove(scope, name)`; verify `make test` and `make lint` pass.

## 3. Same-scope registrations of a name with a default

- [ ] 3.1 RED: in `di_test.go` add `TestProvide_DefaultSameScope` (6 rows; scenario "Explicit and default registrations of one name in one scope"), `TestProvide_DefaultReplacementKeepsIndex`, `TestProvide_ExplicitDuplicateUnchanged`, `TestProvide_ExplicitShadowingUnchanged`, `TestProvide_DefaultRegistrationHooks` (2 rows), `TestProvideOptions_AsDefaultValidatedBeforeSkip` (options_test.go) and `TestAsDefault_ProviderRegistersOwnName` (in `testWithTimeout`); verify `go test -race -run 'TestProvide_(DefaultSameScope|DefaultReplacementKeepsIndex|ExplicitDuplicateUnchanged|ExplicitShadowingUnchanged)|TestProvide_DefaultRegistrationHooks|TestProvideOptions_AsDefaultValidatedBeforeSkip|TestAsDefault_ProviderRegistersOwnName' .` fails on the explicit_replaces, default_skipped, eager_replaced and transient_replaced rows and both hook rows (duplicate panic today); the two Unchanged tests, the two panic rows, `ValidatedBeforeSkip` and `ProviderRegistersOwnName` pass on arrival, so show each can fail by temporarily treating every duplicate as replaceable.
- [ ] 3.2 GREEN: implement design D4 in `provide` (validate options first; before-registration hook before the duplicate check; under the scope write lock, compute the index from the map, swap an unresolved default for the explicit wrapper while holding the default's `mu`, skip a default over a non-default, error otherwise with the existing message, which `provide` panics with; after-registration hook only when stored or replaced) and the superseded retry path in `serviceDefault`; verify the 3.1 command passes and `make test` is green.
- [ ] 3.3 REFACTOR: *Extract Function* `registerLocked(scope, name, incoming) (registerOutcome, error)` out of `provide` (outcomes stored, replaced, skipped), so `add-scope-export`'s `Export` can reuse it and return the error; verify `make test` and `make lint` pass.

## 4. Frozen decision, Override and Clone

- [ ] 4.1 RED: add `TestAsDefault_DecisionFrozen` (rows ext, root), `TestOverride_IgnoresAsDefault` (di_test.go) and `TestAsDefault_Clone` (state reset); verify `go test -race -run 'TestAsDefault_(DecisionFrozen|Clone)|TestOverride_IgnoresAsDefault' .` fails on `Clone` (a clone keeps the decided state); `DecisionFrozen` and `Override_IgnoresAsDefault` pass on arrival (only `provide` wraps, and `decide()` is idempotent), so show they fail by temporarily wrapping in `override` and by re-running `decide()` on every invoke.
- [ ] 4.2 GREEN: clear `isDefault` in `override` and mark a replaced same-scope default superseded (design D5); implement `serviceDefault.clone` with the state reset (design D10); verify the 4.1 command passes and `make test` is green.
- [ ] 4.3 REFACTOR: none needed if 4.2 adds only the two guarded branches; otherwise *Extract Function* for the supersede-on-override path; verify `make test` and `make lint` pass.

## 5. Lifecycle of defaults and Start candidates

- [ ] 5.1 RED: add `TestAsDefault_YieldedLifecycle` (rows start, run, shutdown, root_healthcheck, ext_healthcheck), `TestAsDefault_YieldedHealthCheckResult`, `TestAsDefault_ChosenLifecycle`, `TestAsDefault_EagerShutdownOwnership` (4 rows), `TestProvideOptions_AsDefaultCombined` (4 rows), and in `root_start_test.go` `TestStart_DefaultCandidates` (3 rows), `TestStart_DefaultDecidedAtDiscovery` and `TestScopeStart_SkipsYieldedDefault`; verify `go test -race -run 'TestAsDefault_(YieldedLifecycle|YieldedHealthCheckResult|ChosenLifecycle|EagerShutdownOwnership)|TestProvideOptions_AsDefaultCombined|TestStart_Default|TestScopeStart_SkipsYieldedDefault' .` fails on the start and run rows (the default is a candidate), `TestAsDefault_YieldedHealthCheckResult` (both rows report nil instead of "pool down") and the eager ownership rows; the shutdown, root_healthcheck and ext_healthcheck count rows, `ChosenLifecycle` and the `*_alone` rows pass on arrival (the ext count may also flap, because the discarded ancestor check is asynchronous), so show they fail by temporarily forwarding every yielded call to the inner wrapper.
- [ ] 5.2 GREEN: implement the design D6 table (yielded: no shutdown, no health check; unresolved eager decided at shutdown), `decide()` of unresolved defaults followed by the yielded-default skip in `Scope.asyncHealthCheckWithContext`, and the D3 `decide()` call in `newStartCandidate` after starter/kinds are computed from the registered type; verify the 5.1 command passes and `make test` is green.
- [ ] 5.3 REFACTOR: *Replace Conditional with Polymorphism* if the state switch repeats across `shutdown`/`healthcheck`/`isShutdowner`/`isHealthchecker` (one `forwardTarget()` returning the wrapper to forward to, or nil); *Extract Function* `isYieldedDefault(service)` shared with groups 7 and 8; verify `make test` and `make lint` pass.
- [ ] 5.4 Document in the godoc of `AsDefault` and on `docs/docs/service-lifecycle/starter.md` that Start and shutdown fix the decision of the defaults they consider, and who owns a superseded eager value; verify `go doc AsDefault` shows it and `npm run build` in `docs/` passes.

## 6. Concurrency of first resolution and replacement

- [ ] 6.1 RED: add `TestAsDefault_ConcurrentFirstResolution` (100 goroutines), `TestAsDefault_ReplaceRacesResolve` (rows replace_first, resolve_first, pinned by a channel handshake) and `TestAsDefault_ReplaceRacesResolveStress` (both orders unpinned, 1,000×), each in `testWithTimeout`; these pass on arrival after groups 2–3, so show each can fail by temporarily removing the `mu` guard in `decide()` and the default-`mu` hold in the D4 swap (a race report or a wrong outcome), then restore.
- [ ] 6.2 GREEN: fix whatever 6.1 exposes in the design D2/D4 locking (if nothing, record that the guards from 2.2/3.2 hold); verify `go test -race -count=20 -run 'TestAsDefault_(ConcurrentFirstResolution|ReplaceRacesResolve)' .` passes, goleak reports no leak and `make test` is green.
- [ ] 6.3 REFACTOR: none needed beyond what 6.2 touched; verify `make test` and `make lint` pass.

## 7. By-type selection prefers non-default services

- [ ] 7.1 RED: in `invoke_test.go` add the `default_in_request` row to `TestInvokeAs_Deterministic`, and `TestInvokeAs_PrefersNonDefault` (4 rows), `TestInvokeAs_ChosenDefaultKeepsRank`, `TestInvokeAs_DefaultDoesNotShadow`, `TestInvokeAs_TwoDefaultsNearestFirst` and `TestInvokeStruct_PrefersNonDefault`; verify `go test -race -run 'TestInvokeAs_(Deterministic|PrefersNonDefault|ChosenDefaultKeepsRank|DefaultDoesNotShadow|TwoDefaultsNearestFirst)|TestInvokeStruct_PrefersNonDefault' .` fails on `default_in_request`, `ext_vs_root`, `root_vs_root`, `DefaultDoesNotShadow` and the struct case; `ChosenDefaultKeepsRank`, `TwoDefaultsNearestFirst` and the `default_only`/`unrelated_explicit` rows pass on arrival, so show they fail by temporarily reversing the tier order.
- [ ] 7.2 GREEN: implement design D3 tiers in `selectByType` (tier 1 non-defaults and chosen defaults, tier 2 unresolved defaults with `decide()` on pick, yielded excluded) and make `shadowedBetween` ignore default wrappers (D7); verify the 7.1 command passes and `make test` is green with the other `TestInvokeAs_*` rows unchanged.
- [ ] 7.3 REFACTOR: *Extract Function* `selectInTier(injector, target, tier)`; verify `make test` and `make lint` pass.
- [ ] 7.4 Add `ExampleAsDefault_byType` and a note on the `InvokeAs` docs page about the non-default preference; verify `go test -run ExampleAsDefault_byType .` and `npm run build` in `docs/` pass.

## 8. Collections and defaults

- [ ] 8.1 RED: in `invoke_all_test.go` add the `default_does_not_shadow` row to `TestInvokeAll_Visibility`, `TestInvokeAll_DefaultMembership` (4 rows), `TestInvokeAll_ChosenDefaultStaysMember`, `TestInvokeAll_DefaultKeepsUnrelated`, `TestInvokeAllMap_YieldedDefaultDoesNotShadow`, `TestInvokeAllMap_DefaultKeepsOneEntry` (2 rows), and a default row in an existing `Collection.All` test; verify `go test -race -run 'TestInvokeAll_(Visibility|DefaultMembership|ChosenDefaultStaysMember|DefaultKeepsUnrelated)|TestInvokeAllMap_(YieldedDefaultDoesNotShadow|DefaultKeepsOneEntry)|TestCollection' .` fails on `iface_replaced`, `concrete_replaced` and `default_does_not_shadow` (child default member and root hidden); `YieldedDefaultDoesNotShadow` passes on arrival (the shadowing default yields to the root instance after group 2), as do `ChosenDefaultStaysMember`, `DefaultKeepsUnrelated` and the `*_alone`/`concrete_kept` rows, so show they fail by temporarily dropping every default from collections.
- [ ] 8.2 GREEN: in `collectionCandidatesFor` let an unresolved or yielded default not shadow a same-name non-default (a chosen default, and any default over a farther default, still shadow; design D7), then apply design D3's collection rule (drop unresolved defaults replaced by type, drop yielded defaults, keep chosen ones, `decide()` the rest); verify the 8.1 command passes and `make test` is green with the existing collection tests unchanged.
- [ ] 8.3 REFACTOR: *Extract Function* `dropReplacedDefaults(candidates)`; verify `make test` and `make lint` pass.
- [ ] 8.4 Note on the collection docs page that defaults are replaced by type only when registered under the interface type; verify `npm run build` in `docs/` passes.

## 9. Explain reports default status

- [ ] 9.1 RED: in `di_explain_test.go` add `TestExplainNamedService_DefaultState` (4 rows) and `TestExplainNamedService_DefaultStateJSON`; verify `go test -race -run 'TestExplainNamedService_DefaultState' .` fails to compile only on `DefaultState`, its constants and the new fields.
- [ ] 9.2 GREEN: add `DefaultState` (zero `DefaultStateNone`, `String`, `MarshalText`, `UnmarshalText`), `ExplainServiceOutput.DefaultState`, `.DefaultYieldedToScopeName` and `.DefaultYieldedToService` with `omitempty` JSON tags, filled from `serviceDefault` (design D9); verify the 9.1 command passes and `make test` is green with existing explain JSON assertions unchanged.
- [ ] 9.3 REFACTOR: *Extract Function* `explainDefault(service) (state, scopeName, service)` if the fill spans more than one site, otherwise none needed; verify `make test` and `make lint` pass.
- [ ] 9.4 Godoc for `DefaultState` and the new fields; verify `go doc DefaultState` shows the values and `make lint` passes.

## 10. Zero cost when unused

- [ ] 10.1 RED: add `TestScope_DefaultCount` asserting the per-scope count after registering, replacing, skipping and overriding defaults, and in a clone (design D8 mechanism), plus a clone row in `TestInvokeAll_DefaultMembership`; verify `go test -race -run TestScope_DefaultCount .` fails to compile only on the missing counter accessor.
- [ ] 10.2 GREEN: add the atomic per-scope default count, updated wherever a `serviceDefault` enters or leaves a scope's map and set by `Scope.clone` from the wrappers it copies, and guard the tier-2 pass, the default filters, the shadowing exception, the health-check skip and the Start `decide()` call on a non-zero count along the walked chain; verify the 10.1 command passes and `make test` is green.
- [ ] 10.3 REFACTOR: *Extract Function* `chainHasDefaults(scope)`; verify `make test` and `make lint` pass.
- [ ] 10.4 Benchmark gate: `go test -run '^$' -bench . -benchmem -count 10 ./bench/ > <scratch>/bench-after.txt` and `benchstat bench-before.txt bench-after.txt`; verify no statistically significant regression in `InvokeLazySteadyState`, `InvokeAs`, `InvokeAll` or `Start` (record the benchstat output in the task evidence).

## 11. Documentation page and integration

- [ ] 11.1 Add `ExampleAsDefault` (child-scope default yielding to the application's service) in `service_default_example_test.go` and the page `docs/docs/service-registration/default-registrations.md` (yield rule, same-scope rules, frozen decision, lifecycle and eager ownership, by-type and collection rules, Override, Defaults & Control table); verify `go test -run ExampleAsDefault .` and `npm run build` in `docs/` pass.
- [ ] 11.2 Integration check: `make test`, `make lint`, and `go build ./...` plus `go test ./...` in every `go.work` module under `http/*` and `examples/*`; verify all pass.
- [ ] 11.3 Cross-check every `#### Scenario:` header in this change's specs against a passing Go test from the design Test Strategy table; verify none is missing and `openspec validate add-default-registrations --strict` passes.

## Workflow follow-up

- Archive this change after review, before applying `add-scope-export` and `add-extensions` (which build on it).
- Commits and PRs for this change carry no AI attribution lines.
