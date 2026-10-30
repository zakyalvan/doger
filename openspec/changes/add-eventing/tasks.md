# Tasks

Spec: `specs/events/spec.md`. Test names and files follow the design's Test Strategy; decisions are D1-D11. Commands run from the repo root on package `.`. Every RED must fail for the stated reason before its GREEN starts.

## 1. Publish by assignability (spec: "Publish delivers by assignability")

- [ ] 1.1 RED: add `TestPublish_Assignability` (9 rows) and `TestPublish_NoListeners` in `event_test.go`, using function listeners from `Listen`; verify `go test -race -run 'TestPublish_(Assignability|NoListeners)' .` fails to compile naming only `Publish`, `Listen`
- [ ] 1.2 GREEN: add `Publish(ctx, i, event any, opts ...PublishOption)`, `PublishOption`, `Listen[E]` returning a stop func, per-scope function listener storage, and matching by `AssignableTo` over the publishing scope and its ancestors, building the plan on every call (D1, D2); verify the tests pass and `make test` is green
- [ ] 1.3 REFACTOR: Extract Function `matchListeners`; Introduce Parameter Object `funcListener`; verify `make test` and `make lint` pass

## 2. Argument handling (spec: "Publish argument handling", "Listen registers a function listener")

- [ ] 2.1 RED: add `TestPublish_ZeroArguments` (8 rows) and `TestListen_Arguments` (2 rows); verify `go test -race -run 'TestPublish_ZeroArguments|TestListen_Arguments' .` fails to compile naming only `ErrInvalidEvent`
- [ ] 2.2 GREEN: add `ErrInvalidEvent`, nil checks for nil-able kinds (also inside interfaces), nil injector -> `DefaultRootScope`, nil context -> `context.Background()`, and the nil-function panic in `Listen`; verify the tests pass and `make test` is green
- [ ] 2.3 REFACTOR: Extract Function `normalizePublish`; verify `make test` and `make lint` pass

## 3. Stopping function listeners (spec: "Listen registers a function listener")

- [ ] 3.1 RED: add `TestListen_Stop` (3 rows: 0, 1, 2 stops with returned values); verify `go test -race -run TestListen_Stop .` fails on the stop-result assertions
- [ ] 3.2 GREEN: make stop remove its entry once and return true, then false; verify the test passes and `make test` is green
- [ ] 3.3 REFACTOR: none needed: the stop closure is a single removal; verify `make test` and `make lint` pass

## 4. Listener errors and IDs for function listeners (spec: "Fail-fast is the default error mode", "Listener IDs")

- [ ] 4.1 RED: add `TestDispatch_FailFast`, `TestDispatch_TypedNilError`, and the function-listener and empty/explicit-ID rows of `TestListenerID`; verify `go test -race -run 'TestDispatch_(FailFast|TypedNilError)|TestListenerID' .` fails to compile naming only `ListenerError`, `ListenerID`, `ListenerOption`
- [ ] 4.2 GREEN: add `ListenerError` (message, `Unwrap`), `ListenerOption`, `ListenerID`, default function IDs `<scope path>/func#<n>` with percent-escaped segments, and fail-fast dispatch (D3, D5); verify the tests pass and `make test` is green
- [ ] 4.3 REFACTOR: Extract Function `scopePath` and `listenerError`; verify `make test` and `make lint` pass
- [ ] 4.4 Add `ExamplePublish` and `ExampleListen` documenting zero-config defaults, plus godoc on `Publish`, `Listen`, `ListenerError`; verify `go test -run 'Example(Publish|Listen)$' .` passes

## 5. Service listeners with AsListener (spec: "AsListener declares a listener service", "Listener services are built lazily", "Listener IDs")

- [ ] 5.1 RED: add `TestAsListener_Registration` (4 rows), `TestAsListener_InterfaceCheckedAtDispatch`, `TestListener_Build` (6 rows), `TestListener_ProviderError`, and the service rows of `TestListenerID` (incl. `ext:a/b` escaping); verify `go test -race -run 'TestAsListener_|TestListener_|TestListenerID' .` fails to compile naming only `AsListener`, `Listener`, `ErrInvalidListener`
- [ ] 5.2 GREEN: add `Listener[E]`, `AsListener[E]` appending a deduplicated `listenerBinding` to `serviceOptions.listeners` with the registration panic, the instance check with `ErrInvalidListener`, lazy resolution from the listener's scope, and default service IDs (D2, D3); verify the tests pass and `make test` is green
- [ ] 5.3 REFACTOR: Extract Function shared with `AsStarter`'s registered-type check where the shapes match (else none needed, with the reason recorded); verify `make test` and `make lint` pass
- [ ] 5.4 Add `ExampleAsListener` and godoc on `Listener`, `AsListener`; verify `go test -run ExampleAsListener .` passes

## 6. ListenMethod (spec: "ListenMethod declares a method listener", "Listener IDs")

- [ ] 6.1 RED: add `TestListenMethod_MultipleEvents`, `TestListenMethod_Registration` (3 rows) and the `/m2` row of `TestListenerID`; verify `go test -race -run 'TestListenMethod_|TestListenerID' .` fails to compile naming only `ListenMethod`
- [ ] 6.2 GREEN: add `ListenMethod[S, E]` with the type and nil-method panics and `/m<n>` IDs; verify the tests pass and `make test` is green
- [ ] 6.3 REFACTOR: express `AsListener` as a binding built by the same constructor as `ListenMethod` (Extract Function `newBinding`); verify `make test` and `make lint` pass
- [ ] 6.4 Add `ExampleListenMethod` and godoc; verify `go test -run ExampleListenMethod .` passes

## 7. Order (spec: "Listener order is deterministic")

- [ ] 7.1 RED: add `TestDispatch_Order` (r2, c1, c2, *Mailer, r1, x1, with an installed extension); verify `go test -race -run TestDispatch_Order .` fails to compile naming only `ListenerOrder`, or on the order assertion
- [ ] 7.2 GREEN: add `ListenerOrder`, extension scopes from `root.ext` installed entries after the root, and sorting by order, rank, registration index (D3, D4); verify the test passes and `make test` is green
- [ ] 7.3 REFACTOR: Extract Function `lessListener`; verify `make test` and `make lint` pass

## 8. Filter (spec: "Listener filter")

- [ ] 8.1 RED: add `TestDispatch_Filter` (5 rows) and `TestDispatch_FilterTypeMismatch`; verify `go test -race -run 'TestDispatch_Filter' .` fails to compile naming only `ListenerFilter`
- [ ] 8.2 GREEN: add `ListenerFilter[F]` checked against the listener type at registration, evaluated before resolution, last one winning, panics recovered into `*ListenerError`; verify the tests pass and `make test` is green
- [ ] 8.3 REFACTOR: Extract Function `recoverListenerError` for reuse by group 11; verify `make test` and `make lint` pass

## 9. Reach (spec: "Event reach across scopes")

- [ ] 9.1 RED: add `TestDispatch_Reach` (10 rows incl. `ext:fake` and `PublishLocal`) and `TestDispatch_ReachInstallingExtension`; verify `go test -race -run 'TestDispatch_Reach' .` fails to compile naming only `PublishLocal`, or on the `ext:fake`/installing rows
- [ ] 9.2 GREEN: add `PublishLocal`, include only installed extension scopes, exclude siblings and descendants (D4); verify the tests pass and `make test` is green
- [ ] 9.3 REFACTOR: Extract Function `reachedScopes`; verify `make test` and `make lint` pass

## 10. Listener lifetime (spec: "Listener lifetime follows its registration")

- [ ] 10.1 RED: add `TestListenerLifetime` (6 rows: override both ways, AsDefault replaced, AsDefault yield, scope shutdown, recreated scope) and `TestPublish_ShutDownScope` in `event_lifetime_test.go`; verify `go test -race -run 'TestListenerLifetime|TestPublish_ShutDownScope' .` fails to compile naming only `ErrPublishAfterShutdown`, or on the shutdown rows
- [ ] 10.2 GREEN: clear a scope's function listeners and mark it closed for publishing at scope shutdown; add `ErrPublishAfterShutdown`; confirm override and AsDefault rows pass through wrapper-bound bindings alone (D2, D7); verify the tests pass and `make test` is green
- [ ] 10.3 REFACTOR: Move Function for the scope-shutdown event teardown into one `closeEvents` step next to the existing scope shutdown; verify `make test` and `make lint` pass

## 11. Error modes, panics and options (spec: "EventContinue runs every listener", "Listener panics are recovered", "Event options are validated")

- [ ] 11.1 RED: add `TestDispatch_ErrorMode` (5 rows), `TestDispatch_Panic` (4 rows) and the error-mode and zero-value rows of `TestEventOptions`; verify `go test -race -run 'TestDispatch_(ErrorMode|Panic)|TestEventOptions' .` fails to compile naming only `EventErrorMode`, `EventContinue`, `EventFailFast`
- [ ] 11.2 GREEN: add `EventErrorMode` with `String()`, `InjectorOpts.EventErrorMode` copied by `copy()`, joined errors in continue mode, recovered listener panics, and the invalid-mode panic in `New`/`NewWithOpts`; verify the tests pass and `make test` is green
- [ ] 11.3 REFACTOR: none needed: a two-arm switch is clearer than polymorphism here; verify `make test` and `make lint` pass
- [ ] 11.4 Add `ExampleInjectorOpts_eventErrorMode`; verify `go test -run ExampleInjectorOpts_eventErrorMode .` passes

## 12. Nested publish and context (spec: "Nested publish runs inline", "Publish honors its context")

- [ ] 12.1 RED: add `TestDispatch_Nested` and `TestDispatch_Context` (3 rows); verify `go test -race -run 'TestDispatch_(Nested|Context)' .` fails on the context rows (listeners still called)
- [ ] 12.2 GREEN: check the context before dispatch and between listeners; ensure plans are immutable snapshots so no lock is held during listener code; verify the tests pass and `make test` is green
- [ ] 12.3 REFACTOR: none needed: two checks in one loop; verify `make test` and `make lint` pass

## 13. Publish during a build (spec: "Publish from a provider cannot deadlock")

- [ ] 13.1 RED: add `TestPublish_DuringBuild` (3 rows) inside `testWithTimeout`; verify `go test -race -run TestPublish_DuringBuild .` fails by timing out on the cycle rows
- [ ] 13.2 GREEN: resolve service listeners through a `virtualScope` chained to the caller's, without recording a DAG edge (D6); verify the test passes within its timeout and `make test` is green
- [ ] 13.3 REFACTOR: Extract Function `listenerInjector`; verify `make test` and `make lint` pass

## 14. Async delivery and the error hook (spec: "Async delivery", "Errors with no caller go to HookEventError")

- [ ] 14.1 RED: add `TestAsync_RunsAfterPublish` and `TestAsync_ErrorRouting` (5 rows, capturing `log` output) in `event_async_test.go`; verify `go test -race -run 'TestAsync_(RunsAfterPublish|ErrorRouting)' .` fails to compile naming only `ListenerAsync`, `HookEventError`
- [ ] 14.2 GREEN: add `ListenerAsync`, a minimal `EventExecutor` with `WaitGroup`, task contexts from `WithoutCancel` plus `WithCancelCause`, `InjectorOpts.HookEventError` with the `Logf` then `log` fallback and recovered hook panics (D8, D10); verify the tests pass under `-race` and goleak reports no leaks
- [ ] 14.3 REFACTOR: Extract Function `reportEventError`; verify `make test` and `make lint` pass
- [ ] 14.4 Add `ExampleListenerAsync` showing `HookEventError`; verify `go test -run ExampleListenerAsync .` passes

## 15. Bounded default executor (spec: "Default executor is bounded", "Replaceable event executor", "Event options are validated")

- [ ] 15.1 RED: add `TestAsync_Backpressure` (5 rows), `TestAsync_DefaultParallelism` (2 rows), `TestBoundedEventExecutor` (3 rows) and the parallelism rows of `TestEventOptions`; verify `go test -race -run 'TestAsync_(Backpressure|DefaultParallelism)|TestBoundedEventExecutor|TestEventOptions' .` fails to compile naming only `NewBoundedEventExecutor`, `EventAsyncParallelism`
- [ ] 15.2 GREEN: add `BoundedEventExecutor`, `NewBoundedEventExecutor(n uint)` with its panic, `InjectorOpts.EventAsyncParallelism` (0 = `GOMAXPROCS*4` read at creation), done-context-first and slot-or-ctx waiting (D8); verify the tests pass under `-race` within their timeouts
- [ ] 15.3 REFACTOR: Extract Function `acquire`; verify `make test` and `make lint` pass
- [ ] 15.4 Add `ExampleNewBoundedEventExecutor`; verify `go test -run ExampleNewBoundedEventExecutor .` passes

## 16. Nested async publishing (spec: "Nested async publishing never deadlocks")

- [ ] 16.1 RED: add `TestAsync_NestedNoDeadlock` inside `testWithTimeout`; verify `go test -race -run TestAsync_NestedNoDeadlock .` fails by timing out
- [ ] 16.2 GREEN: mark task contexts with the executor and run a nested submission inline when no slot is free (D8); verify the test passes and `make test` is green
- [ ] 16.3 REFACTOR: none needed: one branch in `Submit`; verify `make test` and `make lint` pass

## 17. Replaceable executor (spec: "Replaceable event executor")

- [ ] 17.1 RED: add `TestExecutor_Custom` and `TestExecutor_SubmitFailure` (2 rows); verify `go test -race -run TestExecutor_ .` fails to compile naming only `InjectorOpts.EventExecutor`
- [ ] 17.2 GREEN: add `InjectorOpts.EventExecutor` (copied by `copy()`), route async deliveries through it, turn `Submit` errors and panics into `*ListenerError`, call its `Shutdown` at root shutdown; verify the tests pass and `make test` is green
- [ ] 17.3 REFACTOR: none needed: the default executor already implements the interface; verify `make test` and `make lint` pass

## 18. Lifecycle events (spec: "Container lifecycle events", "Lifecycle listeners only observe")

- [ ] 18.1 RED: add `TestLifecycleEvents_Order` (3 rows), `TestLifecycleEvents_Data`, `TestLifecycleEvents_Observational` (5 rows) and `TestLifecycleEvents_HooksUnchanged` in `event_lifecycle_test.go`; verify `go test -race -run TestLifecycleEvents_ .` fails to compile naming only the `Container*` event types
- [ ] 18.2 GREEN: add the six event types and publish each after its hooks through the internal continue-mode path that reports to `HookEventError`, including before a re-raised pipeline panic (D9); verify the tests pass and the existing `container-lifecycle-hooks` tests stay green under `make test`
- [ ] 18.3 REFACTOR: Extract Function `publishLifecycle` beside `runObserverHooks`; verify `make test` and `make lint` pass

## 19. Root shutdown (spec: "Shutdown drains async deliveries", "Publishing after shutdown")

- [ ] 19.1 RED: add `TestShutdown_Drain` (2 rows), `TestShutdown_ReleasesWaiters`, `TestShutdown_PublishAfter` and `TestShutdown_ClosedFunctionOnly` in `event_shutdown_test.go`; verify `go test -race -run TestShutdown_ .` fails on the ordering, waiter and skip assertions
- [ ] 19.2 GREEN: after `ContainerClosing`, close async intake, wake waiting publishers with `ErrPublishAfterShutdown`, drain bounded by the shutdown context and report its error, skip unbuilt and removed service listeners during service shutdown, and deliver `ContainerClosed` to function listeners past the closed check (D7, D9); verify the tests pass under `-race` and goleak reports no leaks
- [ ] 19.3 REFACTOR: Extract Function `drainEvents` from the close path; verify `make test` and `make lint` pass

## 20. Scope shutdown drain (spec: "Scope shutdown drains its listeners")

- [ ] 20.1 RED: add `TestScopeShutdown_Drain`; verify `go test -race -run TestScopeShutdown_Drain .` fails with "conn-closed" recorded before "delivered"
- [ ] 20.2 GREEN: count in-flight async tasks per listener scope and wait for a scope's and its descendants' counters at scope shutdown, bounded by the context (D7); verify the test passes under `-race` and goleak reports no leaks
- [ ] 20.3 REFACTOR: Inline any helper from 10.3 that is now single-use; verify `make test` and `make lint` pass

## 21. Concurrency and the plan cache (spec: "Publish racing shutdown", "Concurrent publish and registration")

- [ ] 21.1 RED: add `TestPublish_RacingShutdown` and `TestPublish_ConcurrentRegistration` inside `testWithTimeout`, and `BenchmarkPublish/{0,1,10,100}-listeners` and `BenchmarkPublish/nested-scopes` in `bench/`; verify `go test -race -run 'TestPublish_(RacingShutdown|ConcurrentRegistration)' .` fails (race report, leak, or an error other than `ErrPublishAfterShutdown`), and record the benchmark baseline
- [ ] 21.2 GREEN: fix the races found (snapshot function listener lists under the scope lock, admit async tasks under the intake lock); verify the tests pass under `-race` and goleak reports no leaks
- [ ] 21.3 REFACTOR: Introduce the per-scope plan cache keyed by (type, local) with version-based invalidation (D4), behavior unchanged; verify `make test` and `make lint` pass and `go test -run x -bench BenchmarkPublish ./bench` improves on the 21.1 baseline

## 22. Clone (spec: "Clone copies listeners")

- [ ] 22.1 RED: add `TestClone_Listeners` (2 rows) and `TestClone_ExecutorOwnership` (2 rows); verify `go test -race -run TestClone_ .` fails on the clone rows
- [ ] 22.2 GREEN: copy each scope's function listeners in `CloneWithOpts`, keep plan caches and the default executor per root, and document that an `InjectorOpts.EventExecutor` is shared (D11); verify the tests pass and `make test` is green
- [ ] 22.3 REFACTOR: Move Function for the event-state copy next to the kind-entry copy in `root_scope.go`; verify `make test` and `make lint` pass

## 23. Example module and guide

- [ ] 23.1 Rewrite `examples/event-driven` on `Publish`, `AsListener`, `ListenMethod` and `ListenerAsync`, removing its hand-rolled `EventBus`; verify `cd examples/event-driven && go build ./... && go vet ./...` passes and the workspace module list is unchanged
- [ ] 23.2 Add an "Events" guide under `docs/` (defaults, overrides, reach, lifetime, async, lifecycle events, the `log` fallback) and an upgrading note stating no breaks; verify the docs pages render with the repo's docs tooling, or `npx markdownlint` when none applies

## 24. Integration checks

- [ ] 24.1 Reconcile the design's Test Strategy with the tests: every `#### Scenario:` has its test and every Examples row its subtest; verify with `go test -race -run 'Test(Publish|Listen|AsListener|ListenMethod|Listener|Dispatch|Async|Executor|BoundedEventExecutor|LifecycleEvents|Shutdown|ScopeShutdown|Clone|EventOptions)' -v . | grep -c '^=== RUN'` against the table, fixing any gap
- [ ] 24.2 Run the full suite and every workspace module; verify `make test`, `make lint`, and `go build ./... && go test ./...` in each `./http/*` and `./examples/*` module pass
- [ ] 24.3 Validate the change; verify `openspec validate add-eventing --strict` passes

## Workflow follow-up

- Archive the change once `make test`, `make lint` and `openspec validate add-eventing --strict` pass and every scenario maps to a passing test.
- Start `add-durable-events` (durable and transaction-bound delivery) on top of this listener model, carrying over the durable spec from this change's history and the audit's durable findings.
