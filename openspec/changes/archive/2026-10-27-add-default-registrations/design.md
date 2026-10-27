# Design

## Context

See proposal.md (Why) and the specs of this change for the required behavior. The current code shapes the approach as follows:

- **Registration storage.** A registration is stored as a service wrapper (`serviceLazy`, `serviceEager`, `serviceTransient`, `serviceAlias`) in the scope's `services` map. `provide` checks `serviceExist` and then calls `serviceSet` (`di.go:191-205`). Those are two separate critical sections, and `serviceSet` runs the registration hooks. `override` skips the check (`di.go:326`).
- **Lookup by name** is `serviceGetRec` (own scope, then parents). It returns the first wrapper found, so a nearer same-name registration shadows a farther one.
- **By-type selection** (`selectByType`, `invoke.go:415`) walks scopes nearest first and skips names shadowed by a nearer scope (`shadowedBetween`). **Collections** (`collectionCandidatesFor`, `invoke.go:357`) walk `serviceForEachRec` and keep the first wrapper per name (`seen`).
- **Start discovery** (`newStartCandidate`, `root_start.go:525`) uses the wrapper's registered type and `getServiceType` (skipping transients and aliases). Runner auto-detection is off by default (`EnableRunnerAutoDetection`), so only an explicit `AsRunnable()` makes a service run.
- **Health check.** `Scope.asyncHealthCheckWithContext` (`scope.go:344-365`) queues every own service, then *also* queues every ancestor service and keeps an ancestor's result only for names the scope does not hold. A shadowed ancestor is therefore checked and its result discarded. Root `HealthCheck` never walks child scopes. The audit pinned both with tests.
- **Eager values** are shut down at scope shutdown whether or not they were invoked (`serviceEager.shutdown`).
- **Cycle detection** is by service name along the virtual-scope invoker chain. A default that yields resolves a service with the same name as itself.
- **Explain enums.** `DependencySource` already serializes to JSON by name through `MarshalText` (`dag.go:69`). The root scope's name is `DefaultRootScopeName` = `[root]`.

## Goals / Non-Goals

**Goals:**
- One implementation of default semantics for every registration form.
- No measurable cost for containers that never use `AsDefault()`.
- A deterministic, race-free decision between "use own provider" and "yield", and between "replace" and "resolve". Name, by-type and collection reads all follow that decision.

**Non-Goals:**
- Defaults across sibling or descendant scopes (specified as not counting).
- Shutting down the previous instance on `Override*` (the existing caveat stays).
- Changing a decision after the first resolution.
- The general behavior that a scope's health check also runs shadowed ancestor checks and discards their results (existing behavior). For defaults, only the reported result is fixed: a yielded default reports its target's result.
- Exports (`add-scope-export`) and how they interact with defaults. That change specifies them.

## Decisions

### D1. A decorator wrapper, `serviceDefault[T]`
- `AsDefault()` sets `serviceOptions.isDefault`. `provide` wraps the lazy, eager or transient wrapper in `serviceDefault[T]` (inner wrapper, own scope, state machine). It implements `serviceWrapper[T]` and forwards every optional wrapper interface.
- *Alternative:* state plus checks in each of the three wrappers. Rejected: three copies of a concurrency-sensitive state machine.
- *Cost:* forwarding must be complete, so compile-time interface assertions and a forwarding table test guard it.

### D2. State machine
```
             decide(): an ancestor has a non-default "name"?
 unresolved ---------------------------------------------> yielded(targetScope, targetWrapper)
     |  \                 no
     |   +-------------------------------------------------> chosen   (use own provider/value)
     |
     +-- explicit Provide in same scope while unresolved ----> superseded (replaced in the map atomically)
```
- **Guarding:** `decide()` runs under the default's `mu`, is idempotent, and publishes the decided state through an `atomic` value, so later reads take no lock.
- **The lookup** walks the parent chain only (`scope.parentScope` and up), skips `serviceDefault` wrappers, and stops at the first non-default wrapper with the name. An alias counts as non-default.
- **Chosen:** delegate to the inner wrapper outside `mu`. The inner wrapper serializes its build as today. A failed build keeps "chosen" and is retried on the next invoke, which is why the state is called *chosen*, not *built*.
- **Yielded:** delegate outside `mu` to the target wrapper in `targetScope`, invoked directly rather than by name from the default's scope. The delegation runs the target scope's before/after-invocation hooks, adds a child virtual scope, and records a scope-qualified edge from the default to the target. Cycle detection is already scope-qualified (`virtualScope.chainContains` compares the name *and* the scope, from `consolidate-pending-lifecycle-work`), so this hop needs no special case: the default and its target share a name but not a scope, and a real loop through them is still `ErrCircularDependency`. The helper is `invokeWrapperInScope`, which `add-scope-export` reuses.
- **Superseded:** a reader holding the old wrapper pointer retries resolution of the name from the default's scope. Because the swap and the state change happen together (D4), the retry finds the new entry, never the old one.

### D3. What fixes the decision, and how by-type reads follow it
- The decision is fixed by the first of these events:
  - an invocation by name;
  - by-type selection picking the default;
  - a collection including it;
  - Start, Refresh or `Scope.Start` considering it as a candidate (a starter or a kind opt-in);
  - a health check of the default's scope or of a descendant (D6), which decides without building;
  - shutdown of an unresolved eager default (D6).
- Explain (`ExplainInjector`, `ExplainService`, `ExplainNamedService`) never decides: introspection leaves a default replaceable (D6).
- **By-type selection uses two tiers.** Tier 1 holds non-defaults plus *chosen* defaults. Tier 2 holds *unresolved* defaults, and is used only if tier 1 is empty; picking one calls `decide()`. *Yielded* defaults are never candidates, since their target is the real service. Each tier keeps the existing order: nearest scope, then registration index, then name.
- **Collections:**
  - drop *unresolved* defaults whose registered type a non-default member is assignable to;
  - drop *yielded* defaults;
  - keep *chosen* defaults;
  - call `decide()` on each unresolved default they keep.
- This makes name, by-type and collection reads agree after a late ancestor registration (the spec scenarios "A default that chose its own provider keeps its rank" and "…stays a member").

### D4. Same-scope rules: one registration helper, under the scope lock
`provide` (and, in `add-scope-export`, `Export`) registers through one helper, `registerLocked(scope, name, incoming) (registerOutcome, error)`, with outcomes stored, replaced or skipped. `provide` panics on the error, and `Export` returns it.
- **Hooks:** `HookBeforeRegistration` runs before the helper, so before the duplicate check, as its public contract says ("called before a service is registered"), and also for a registration that turns out to be a skipped default. `HookAfterRegistration` runs after the helper and only when the outcome is stored or replaced.
- **Registration index:** computed inside the helper from the map directly, not through `serviceGet`, which would take the read lock the helper already holds as a writer.
- **Explicit over explicit is unchanged:** an explicit registration whose name the scope already holds as a non-default panics with the duplicate message before the before-registration hook and before option validation, exactly as before this change, so registrations without `AsDefault()` behave as before (spec "Registrations without AsDefault are unchanged"). The order of this section (validate, hook, helper) applies only when a default is involved. `Export` (`add-scope-export`) calls `registerLocked` directly and always gets the helper's outcome.

The helper runs with the scope's write lock held:
1. If the name is free, store the wrapper (unchanged path).
2. If the existing entry is a `serviceDefault` and the new registration is explicit: take the default's `mu`. If the default is unresolved, set it superseded and store the new wrapper *before releasing either lock*. Otherwise release and panic with the existing duplicate message.
3. If the new registration is a default and the existing entry is non-default: skip. The after-registration hook does not run; the before-registration hook already ran (spec "Registration hooks around defaults").
4. Default over default: panic with the duplicate message.

- **Options are validated first** (`validateServiceOptions`), so an invalid combination panics even when the default would be skipped.
- **Lock order is strictly toward the root:** scope `S.mu` → `default(S).mu` → `ancestor(S).mu` (read locks taken by `decide()`). `decide()` never touches its own scope's lock, and no code path holds a lock on a nearer scope while taking a farther one in the other direction, so the order has no cycle. No user code runs under these locks: providers run outside `mu` (D2), and the registration hooks run after step 2 released both locks.
- The earlier `TryLock` idea is dropped. Because builds run outside `mu`, a provider that registers its own name meets a decided default and gets the duplicate panic. `TestAsDefault_ProviderRegistersOwnName` is kept as a regression pin.

### D5. Override ignores `isDefault`
`override` clears `isDefault` after validation. If it replaces a `serviceDefault` in the same scope, it marks it superseded under the same lock pairing as D4, so readers retry.

### D6. Lifecycle of the wrapper
| State | getServiceType | isShutdowner/shutdown | isHealthchecker/healthcheck | Start candidate |
|---|---|---|---|---|
| unresolved (lazy/transient) | inner's | `false` / no-op (nothing built) | inner's (nothing built) | decide first |
| unresolved (eager) | inner's | `isShutdowner`: inner's, without deciding / `shutdown`: `decide()`, then as chosen or yielded | inner's | decide first |
| chosen | inner's | inner's | inner's | as inner |
| yielded | inner's | `false` / no-op | `false` / nil | never |
| superseded | not in the map | – | – | – |

- **Health check:** `Scope.asyncHealthCheckWithContext` first calls `decide()` on each unresolved default it holds (deciding builds nothing), then skips yielded defaults when it queues the scope's own services. The name then keeps the ancestor's result, and the target's error is reported. Without the decision, an unresolved lazy default would report nil and hide the ancestor's result.
- **Eager defaults:** an unresolved eager default is decided at shutdown, and only a chosen eager value is shut down (the spec's "Who shuts an eager default value down"). A superseded eager value is no longer in the map, so it is never shut down by the container.
- **Introspection never decides:** `isShutdowner` and `isHealthchecker` read the current state only. `ExplainInjector` calls them, and must leave a default replaceable (spec "Explaining does not resolve a default", `TestExplainInjector_DoesNotDecideDefault`).

### D7. Unresolved and yielded defaults never shadow non-defaults
- `shadowedBetween` and the per-name `seen` set of collections ignore a `serviceDefault` that is unresolved or yielded *when the shadowed entry is a non-default*. A chosen default shadows like a non-default, and a default always shadows a farther default of the same name.
- So a nearer unresolved default never hides an ancestor's non-default of the same name, and is then dropped as yielded (D3). A chosen default keeps hiding a later ancestor registration, which matches by-name reads. The name appears once in every case (spec "A chosen or nearer default keeps one entry per name").

### D8. Zero cost when unused
- Each scope keeps an atomic count of the default wrappers it holds.
- The guards, as built (measured against the base commit, task 10.4):
  - **By-type selection** reads `typeMatch.isDefault`, a flag the per-scope `matchesFor` cache records when it is built (it is rebuilt whenever the scope's services change). The no-defaults path is the original single loop; a default match takes the side path `firstTierDefault`, and tier 2 is `selectUnresolvedDefault`. Walking the chain's counts on every call cost more than the type assertions it saved.
  - **Collections** dispatch once per read on `chainHasDefaults`: without defaults on the chain they use the original walk, otherwise `collectionCandidatesWithDefaults` applies D3 and D7.
  - **The health-check skip and the Start `decide()` call** are guarded by the count of the scope they walk (`isYieldedDefaultIn`).
- **Layout:** the count grows `Scope` from 120 to 128 bytes, and `RootScope`, which embeds it, from 352 to 360 bytes, past its 352-byte allocation size class, which slowed the parallel invocation benchmarks (the effect V66 of `consolidate-pending-lifecycle-work` recorded). `startPhase` and `closePhase` are `int32` and share a word, keeping `RootScope` at 352 bytes.
- `serviceDefault` exists only with the option.
- `Scope.clone` writes the cloned map directly, bypassing `serviceSet`, so it also sets the clone's counts from the wrappers it copies. Otherwise every guarded rule would be off in a clone.

### D9. Explain
- **New fields on `ExplainServiceOutput`:**
  - `DefaultState DefaultState` (`json:"default_state,omitempty"`);
  - `DefaultYieldedToScopeName string` (`json:"default_yielded_to_scope_name,omitempty"`);
  - `DefaultYieldedToService string` (`json:"default_yielded_to_service,omitempty"`).
- **`DefaultState`** is an int enum: `DefaultStateNone` (zero, `not-default`), `DefaultStateUnresolved`, `DefaultStateChosen`, `DefaultStateYielded`. It has `String()` and, like `DependencySource`, `MarshalText`/`UnmarshalText`, so JSON shows the name.
- `omitempty` on a `TextMarshaler` int omits the zero value, and the non-default zero value marshals as `not-default`. The test pins the exact JSON for both.

### D10. Clone
`clone` returns a new `serviceDefault` wrapping `inner.clone(newScope)` in the unresolved state.

## Risks / Trade-offs

- **[Incomplete forwarding in the decorator]** → compile-time interface assertions, plus `TestAsDefault_ForwardsWrapperInterfaces` comparing each form with and without the option.
- **[Skipping one name check masks a real cycle]** → only the yield hop is skipped. `TestAsDefault_RealCycleThroughYield` pins a real cycle.
- **[Lock-order mistakes deadlock registration against resolution]** → D4's order is strictly toward the root. The race tests (`ReplaceRacesResolve` stress, `ConcurrentFirstResolution`) run under `-race` with `testWithTimeout`, so a deadlock fails fast instead of hanging.
- **[A superseded eager default value is never shut down]** → this matches `Override*` today. It is documented: the code that created the value owns it.
- **[Start and shutdown fix the decision, which surprises late registrations]** → documented on `AsDefault` and the starter page. The error is the existing duplicate panic, naming the service.
- **[Hot-path regression]** → D8 guards, plus `benchstat` on `bench/` for `InvokeAs`, `InvokeAll`, `InvokeLazySteadyState` and a new `BenchmarkStart` (task 0.1), with no defaults registered. The first measurement failed: `InvokeAllParallel` +22%, `InvokeLazyParallel` +6% (the `RootScope` size class), `InvokeAs` +5–8% (the chain walk and an extracted per-scope function), one extra allocation per registration (an escaping closure) and two per collection (eagerly built maps). After the fixes in D8, no gate benchmark regresses and allocations are unchanged (task 10.4 evidence).
- **[Introspection fixes a decision]** → found by verification: `ExplainInjector` called `isShutdowner`, which decided an unresolved eager default, so a later explicit registration panicked. `isShutdowner` no longer decides (D6); `TestExplainInjector_DoesNotDecideDefault` pins it.

## Public API & Defaults

| Identifier | Kind | Default / Override |
|---|---|---|
| `AsDefault() ProvideOption` | new option | Not applied unless given. When given, explicit registrations win, by name (same scope or ancestor) and by type (`InvokeAs`, struct injection, collections). Override: register explicitly, or use `Override*` |
| `DefaultState`, `DefaultStateNone`, `DefaultStateUnresolved`, `DefaultStateChosen`, `DefaultStateYielded`, `String`, `MarshalText`, `UnmarshalText` | new type | Zero value `DefaultStateNone` |
| `ExplainServiceOutput.DefaultState`, `.DefaultYieldedToScopeName`, `.DefaultYieldedToService` | new fields | Empty for non-defaults; omitted from JSON when empty |

Not BREAKING. Behavior without `AsDefault()` is unchanged, which the "Registrations without AsDefault are unchanged" scenarios, the unchanged rows of the MODIFIED requirements and the existing suite pin. One health-check change applies only to yielded defaults (D6).

## Test Strategy

The production code goes in `service_default.go`, tested in `service_default_test.go`, with an example in `service_default_example_test.go`. Changes to other files are tested in their existing test files. Tests are table-driven with named subtests and testify, and run under `-race` (`make test`). Tests that could block use `testWithTimeout`. Tests that touch `DefaultRootScope` (`TestAsDefault_NilInjector`) do not call `t.Parallel()`. The explain tests live in `di_explain_default_test.go`, a split of the explain tests by concern: `di_explain_test.go` asserts source line numbers of its own providers, which new imports there would shift.

| Scenario | Test | Kind |
|---|---|---|
| Default resolves when nothing replaces it | service_default_test.go: `TestAsDefault_ResolvesWithoutReplacement/{lazy_once,lazy_three,eager,transient}` | unit, table |
| A nil injector registers the default on DefaultRootScope | service_default_test.go: `TestAsDefault_NilInjector` (not parallel) | unit |
| Placement decides whether a default yields | service_default_test.go: `TestAsDefault_YieldsByPlacement/{root,sibling,descendant,none}` | unit, table |
| A grandchild default yields to the root | service_default_test.go: `TestAsDefault_GrandchildYieldsToRoot` | unit |
| Registration hooks around defaults | di_test.go: `TestProvide_DefaultRegistrationHooks/{default_then_explicit,explicit_then_default}` | unit, table |
| Registration order does not matter | service_default_test.go: `TestAsDefault_OrderIndependent/{default_first,explicit_first}` | unit, table |
| Repeating AsDefault is the same as giving it once | service_default_test.go: `TestAsDefault_Repeated` | unit |
| A default does not yield to another default | service_default_test.go: `TestAsDefault_DoesNotYieldToDefault` | unit |
| An alias of a default resolves through the default's decision | service_default_test.go: `TestAsDefault_AliasResolvesThroughDecision` | unit |
| Explicit and default registrations of one name in one scope | di_test.go: `TestProvide_DefaultSameScope/{explicit_replaces,default_skipped,after_resolve_panics,second_default_panics,eager_replaced,transient_replaced}` | unit, table |
| A replaced default keeps its registration position | di_test.go: `TestProvide_DefaultReplacementKeepsIndex` | unit |
| A late ancestor registration does not change a resolved default | service_default_test.go: `TestAsDefault_DecisionFrozen/{ext,root}` | unit, table |
| The yielded-to service is started, shut down and checked once | service_default_test.go: `TestAsDefault_YieldedLifecycle/{start,run,shutdown,root_healthcheck,ext_healthcheck}` | unit, table |
| A health check from the default's scope reports the target's result | service_default_test.go: `TestAsDefault_YieldedHealthCheckResult/{invoked,never_invoked}` | unit, table |
| A default Starter without a replacement is started and shut down | service_default_test.go: `TestAsDefault_ChosenLifecycle` | unit |
| Who shuts an eager default value down | service_default_test.go: `TestAsDefault_EagerShutdownOwnership/{alone,replaced_uninvoked,replaced_invoked,superseded}` | unit, table |
| Override with AsDefault registers an explicit service | di_test.go: `TestOverride_IgnoresAsDefault` | unit |
| Concurrent first resolutions agree | service_default_test.go: `TestAsDefault_ConcurrentFirstResolution` (100 goroutines) | race |
| Replacement racing first resolution has one outcome | service_default_test.go: `TestAsDefault_ReplaceRacesResolve/{replace_first,resolve_first}` + `TestAsDefault_ReplaceRacesResolveStress` (1,000×) | race, timeout |
| Explain output of default registrations | di_explain_default_test.go: `TestExplainNamedService_DefaultState/{unresolved,chosen,yielded,not_default}` | unit, table |
| Default state in JSON | di_explain_default_test.go: `TestExplainNamedService_DefaultStateJSON/{yielded,not_default_omits_fields,unknown_text}` | unit |
| Explaining does not resolve a default | di_explain_default_test.go: `TestExplainInjector_DoesNotDecideDefault/{eager,lazy}` | unit, table |
| An explicit duplicate in one scope still panics | di_test.go: `TestProvide_ExplicitDuplicateUnchanged` | unit |
| A nearer explicit service still shadows an ancestor's | di_test.go: `TestProvide_ExplicitShadowingUnchanged` | unit |
| Each constructor yields an option recognized by registration | options_test.go: existing `TestProvideOptions_Constructors` + row `as_default` | unit, table |
| Options on a default apply only when it uses its own provider | options_test.go: `TestProvideOptions_AsDefaultCombined/{starter_alone,starter_yielded,runnable_alone,runnable_yielded}` | unit, table |
| AsDefault adds no registration panic of its own | options_test.go: `TestProvideOptions_AsDefaultPanics/{provide,value,transient,named,package,transient_starter}` | unit, table |
| An invalid option panics even when the default would be skipped | options_test.go: `TestProvideOptions_AsDefaultValidatedBeforeSkip` | unit |
| Selection among several matches | invoke_test.go: existing `TestInvokeAs_Deterministic` + row `default_in_request` | unit, table |
| A shadowed service is not a candidate | invoke_test.go: existing `TestInvokeAs_Shadowing` (unchanged) | unit, table |
| Selection is stable across runs | invoke_test.go: existing `TestInvokeAs_DeterministicStable` (unchanged) | unit |
| Single and missing matches are unchanged | invoke_test.go: existing `TestInvokeAs_SingleAndMissing` (unchanged) | unit, table |
| Selection by type with defaults | invoke_test.go: `TestInvokeAs_PrefersNonDefault/{ext_vs_root,root_vs_root,default_only,unrelated_explicit}` | unit, table |
| A default that chose its own provider keeps its rank | invoke_test.go: `TestInvokeAs_ChosenDefaultKeepsRank` | unit |
| A same-name default does not hide the ancestor's service | invoke_test.go: `TestInvokeAs_DefaultDoesNotShadow` | unit |
| Two defaults are ordered as before | invoke_test.go: `TestInvokeAs_TwoDefaultsNearestFirst` | unit |
| Struct injection by type prefers non-default services | invoke_test.go: `TestInvokeStruct_PrefersNonDefault` | unit |
| Membership by registered type | invoke_all_test.go: existing `TestInvokeAll_Membership` (unchanged) | unit, table |
| Eager values are members without a provider call | invoke_all_test.go: existing `TestInvokeAll_EagerMembers` (unchanged) | unit |
| Nil injector uses the default root scope | invoke_all_test.go: existing `TestInvokeAll_NilInjector` (unchanged) | unit |
| Typed-nil member is returned as is | invoke_all_test.go: existing `TestInvokeAll_TypedNilMember` (unchanged) | unit |
| Scope visibility and shadowing | invoke_all_test.go: existing `TestInvokeAll_Visibility` + row `default_does_not_shadow` | unit, table |
| Interface type any matches every non-alias service | invoke_all_test.go: existing `TestInvokeAll_AnyType` (unchanged) | unit |
| Collection inside a provider sees the provider's scope | invoke_all_test.go: existing `TestInvokeAll_ProviderScopeVisibility` (unchanged) | unit |
| Default membership by registered type | invoke_all_test.go: `TestInvokeAll_DefaultMembership/{iface_replaced,iface_alone,concrete_kept,concrete_replaced}` | unit, table |
| A default that chose its own provider stays a member | invoke_all_test.go: `TestInvokeAll_ChosenDefaultStaysMember` | unit |
| A default does not exclude unrelated members | invoke_all_test.go: `TestInvokeAll_DefaultKeepsUnrelated` | unit |
| Same-name default in a nearer scope | invoke_all_test.go: `TestInvokeAllMap_YieldedDefaultDoesNotShadow` | unit |
| A chosen or nearer default keeps one entry per name | invoke_all_test.go: `TestInvokeAllMap_DefaultKeepsOneEntry/{chosen_then_explicit,two_defaults}` | unit, table |
| Start candidates with defaults | root_start_test.go: `TestStart_DefaultCandidates/{yielded,alone,refresh}` | unit, table |
| A default yields even when it was never invoked before Start | root_start_test.go: `TestStart_DefaultDecidedAtDiscovery` | unit |
| Scope.Start skips a default that yields | root_start_test.go: `TestScopeStart_SkipsYieldedDefault` | unit |

The Risks section adds these tests, all in `service_default_test.go`:
- `TestAsDefault_ForwardsWrapperInterfaces`;
- `TestAsDefault_RealCycleThroughYield` (`ErrCircularDependency`);
- `TestAsDefault_ProviderRegistersOwnName` (the duplicate panic, no hang);
- `TestAsDefault_Clone` (state reset).

Examples (docs): `ExampleAsDefault` and `ExampleAsDefault_byType`.

**Hot paths and their coverage:**
- `provide` duplicate resolution: the same-scope table and the race tests.
- `serviceDefault.getInstance`, both the decide path and the fast path: the resolve, yield, frozen, alias and concurrency tests.
- `selectByType` tiers: the `InvokeAs` and struct-injection tests.
- Collection candidates: the `InvokeAll` and `InvokeAllMap` tests, plus `TestCollection_DefaultReplacedByType` (collection_test.go) for live collections.
- Default counts (D8): `TestScope_DefaultCount/{registered,skipped,replaced,overridden,override_as_default,shutdown,clone}` and the `clone` row of `TestInvokeAll_DefaultMembership`.
- Start discovery: the Start, Refresh and `Scope.Start` tests.
- Shutdown and health check: the lifecycle, health-check and eager-ownership tests.
- Zero-cost path: `benchstat` on `bench/`.
- Goroutine leaks: the existing `goleak` in `TestMain`.

## Migration Plan

Additive. Consumers need no migration. Docs: a "Default registrations" page, the options page, the `InvokeAs` and collection pages, and the starter page (decisions fixed at Start). Rollback is a revert.

Order: archive `consolidate-pending-lifecycle-work` first, because this change's MODIFIED blocks copy its requirements. Then this change, then `add-scope-export`, then `add-extensions`.
