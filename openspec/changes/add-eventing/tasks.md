# Tasks

Spec: `specs/events/spec.md`. Test names and files follow the design's Test Strategy; decisions are D1-D11. Commands run from the repo root on package `.`. Every RED must fail for the stated reason before its GREEN starts.

## 1. Publish by assignability (spec: "Publish delivers by assignability")

- [x] 1.1 RED: add `TestPublish_Assignability` (9 rows) and `TestPublish_NoListeners` in `event_test.go`, using function listeners from `Listen`; verify `go test -race -run 'TestPublish_(Assignability|NoListeners)' .` fails to compile naming only `Publish`, `Listen`
- [x] 1.2 GREEN: add `Publish(ctx, i, event any, opts ...PublishOption)`, `PublishOption`, `Listen[E]` returning a stop func, per-scope function listener storage, and matching by `AssignableTo` over the publishing scope and its ancestors, building the plan on every call (D1, D2); verify the tests pass and `make test` is green
- [x] 1.3 REFACTOR: Extract Function `matchListeners`; Introduce Parameter Object `funcListener`; verify `make test` and `make lint` pass

## 2. Argument handling (spec: "Publish argument handling", "Listen registers a function listener")

- [x] 2.1 RED: add `TestPublish_ZeroArguments` (8 rows) and `TestListen_Arguments` (2 rows); verify `go test -race -run 'TestPublish_ZeroArguments|TestListen_Arguments' .` fails to compile naming only `ErrInvalidEvent`
- [x] 2.2 GREEN: add `ErrInvalidEvent`, nil checks for nil-able kinds (also inside interfaces), nil injector -> `ErrNilInjector` (returned by `Publish`, panicked by `Listen`), nil context -> `context.Background()`, and the nil-function panic in `Listen`; verify the tests pass and `make test` is green
- [x] 2.3 REFACTOR: Extract Function `normalizePublish`; verify `make test` and `make lint` pass

## 3. Stopping function listeners (spec: "Listen registers a function listener")

- [x] 3.1 RED: add `TestListen_Stop` (3 rows: 0, 1, 2 stops with returned values); verify `go test -race -run TestListen_Stop .` fails on the stop-result assertions
- [x] 3.2 GREEN: make stop remove its entry once and return true, then false; verify the test passes and `make test` is green
- [x] 3.3 REFACTOR: none needed: the stop closure is a single removal; verify `make test` and `make lint` pass

## 4. Listener errors and IDs for function listeners (spec: "Fail-fast is the default error mode", "Listener IDs")

- [x] 4.1 RED: add `TestDispatch_FailFast`, `TestDispatch_TypedNilError`, and the function-listener and empty/explicit-ID rows of `TestListenerID`; verify `go test -race -run 'TestDispatch_(FailFast|TypedNilError)|TestListenerID' .` fails to compile naming only `ListenerError`, `ListenerID`, `ListenerOption`
- [x] 4.2 GREEN: add `ListenerError` (message, `Unwrap`), `ListenerOption`, `ListenerID`, default function IDs `<scope path>/func#<n>` with percent-escaped segments, and fail-fast dispatch (D3, D5); verify the tests pass and `make test` is green
- [x] 4.3 REFACTOR: Extract Function `scopePath` and `listenerError`; verify `make test` and `make lint` pass
- [x] 4.4 Add `ExamplePublish` and `ExampleListen` documenting zero-config defaults, plus godoc on `Publish`, `Listen`, `ListenerError`; verify `go test -run 'Example(Publish|Listen)$' .` passes

## 5. Service listeners with AsListener (spec: "AsListener declares a listener service", "Listener services are built lazily", "Listener IDs")

- [x] 5.1 RED: add `TestAsListener_Registration` (4 rows), `TestAsListener_InterfaceCheckedAtDispatch`, `TestListener_Build` (6 rows), `TestListener_ProviderError`, and the service rows of `TestListenerID` (incl. `ext:a/b` escaping); verify `go test -race -run 'TestAsListener_|TestListener_|TestListenerID' .` fails to compile naming only `AsListener`, `Listener`, `ErrInvalidListener`
- [x] 5.2 GREEN: add `Listener[E]`, `AsListener[E]` appending a deduplicated `listenerBinding` to `serviceOptions.listeners` with the registration panic, the instance check with `ErrInvalidListener`, lazy resolution from the listener's scope, and default service IDs (D2, D3); verify the tests pass and `make test` is green
- [x] 5.3 REFACTOR: Extract Function shared with `AsStarter`'s registered-type check where the shapes match (else none needed, with the reason recorded); verify `make test` and `make lint` pass
- [x] 5.4 Add `ExampleAsListener` and godoc on `Listener`, `AsListener`; verify `go test -run ExampleAsListener .` passes

## 6. ListenMethod (spec: "ListenMethod declares a method listener", "Listener IDs")

- [x] 6.1 RED: add `TestListenMethod_MultipleEvents`, `TestListenMethod_Registration` (3 rows) and the `/m2` row of `TestListenerID`; verify `go test -race -run 'TestListenMethod_|TestListenerID' .` fails to compile naming only `ListenMethod`
- [x] 6.2 GREEN: add `ListenMethod[S, E]` with the type and nil-method panics and `/m<n>` IDs; verify the tests pass and `make test` is green
- [x] 6.3 REFACTOR: express `AsListener` as a binding built by the same constructor as `ListenMethod` (Extract Function `newBinding`); verify `make test` and `make lint` pass
- [x] 6.4 Add `ExampleListenMethod` and godoc; verify `go test -run ExampleListenMethod .` passes

## 7. Order (spec: "Listener order is deterministic")

- [x] 7.1 RED: add `TestDispatch_Order` (r2, c1, c2, *Mailer, r1, x1, with an installed extension); verify `go test -race -run TestDispatch_Order .` fails to compile naming only `ListenerOrder`, or on the order assertion
- [x] 7.2 GREEN: add `ListenerOrder`, extension scopes from `root.ext` installed entries after the root, and sorting by order, rank, registration index (D3, D4); verify the test passes and `make test` is green
- [x] 7.3 REFACTOR: Extract Function `lessListener`; verify `make test` and `make lint` pass

## 8. Filter (spec: "Listener filter")

- [x] 8.1 RED: add `TestDispatch_Filter` (5 rows) and `TestDispatch_FilterTypeMismatch`; verify `go test -race -run 'TestDispatch_Filter' .` fails to compile naming only `ListenerFilter`
- [x] 8.2 GREEN: add `ListenerFilter[F]` checked against the listener type at registration, evaluated before resolution, last one winning, panics recovered into `*ListenerError`; verify the tests pass and `make test` is green
- [x] 8.3 REFACTOR: Extract Function `recoverListenerError` for reuse by group 11; verify `make test` and `make lint` pass

## 9. Reach (spec: "Event reach across scopes")

- [x] 9.1 RED: add `TestDispatch_Reach` (10 rows incl. `ext:fake` and `PublishLocal`) and `TestDispatch_ReachInstallingExtension`; verify `go test -race -run 'TestDispatch_Reach' .` fails to compile naming only `PublishLocal`, or on the `ext:fake`/installing rows
- [x] 9.2 GREEN: add `PublishLocal`, include only installed extension scopes, exclude siblings and descendants (D4); verify the tests pass and `make test` is green
- [x] 9.3 REFACTOR: Extract Function `reachedScopes`; verify `make test` and `make lint` pass

## 10. Listener lifetime (spec: "Listener lifetime follows its registration")

- [x] 10.1 RED: add `TestListenerLifetime` (6 rows: override both ways, AsDefault replaced, AsDefault yield, scope shutdown, recreated scope) and `TestPublish_ShutDownScope` in `event_lifetime_test.go`; verify `go test -race -run 'TestListenerLifetime|TestPublish_ShutDownScope' .` fails to compile naming only `ErrPublishAfterShutdown`, or on the shutdown rows
- [x] 10.2 GREEN: clear a scope's function listeners and mark it closed for publishing at scope shutdown; add `ErrPublishAfterShutdown`; confirm override and AsDefault rows pass through wrapper-bound bindings alone (D2, D7); verify the tests pass and `make test` is green
- [x] 10.3 REFACTOR: Move Function for the scope-shutdown event teardown into one `closeEvents` step next to the existing scope shutdown; verify `make test` and `make lint` pass

## 11. Error modes, panics and options (spec: "EventContinue runs every listener", "Listener panics are recovered", "Event options are validated")

- [x] 11.1 RED: add `TestDispatch_ErrorMode` (5 rows), `TestDispatch_Panic` (4 rows) and the error-mode and zero-value rows of `TestEventOptions`; verify `go test -race -run 'TestDispatch_(ErrorMode|Panic)|TestEventOptions' .` fails to compile naming only `EventErrorMode`, `EventContinue`, `EventFailFast`
- [x] 11.2 GREEN: add `EventErrorMode` with `String()`, `InjectorOpts.EventErrorMode` copied by `copy()`, joined errors in continue mode, recovered listener panics, and the invalid-mode panic in `New`/`NewWithOpts`; verify the tests pass and `make test` is green
- [x] 11.3 REFACTOR: none needed: a two-arm switch is clearer than polymorphism here; verify `make test` and `make lint` pass
- [x] 11.4 Add `ExampleInjectorOpts_eventErrorMode`; verify `go test -run ExampleInjectorOpts_eventErrorMode .` passes

## 12. Nested publish and context (spec: "Nested publish runs inline", "Publish honors its context")

- [x] 12.1 RED: add `TestDispatch_Nested` and `TestDispatch_Context` (3 rows); verify `go test -race -run 'TestDispatch_(Nested|Context)' .` fails on the context rows (listeners still called)
- [x] 12.2 GREEN: check the context before dispatch and between listeners; ensure plans are immutable snapshots so no lock is held during listener code; verify the tests pass and `make test` is green
- [x] 12.3 REFACTOR: none needed: two checks in one loop; verify `make test` and `make lint` pass

## 13. Publish during a build (spec: "Publish from a provider cannot deadlock")

- [x] 13.1 RED: add `TestPublish_DuringBuild` (3 rows) inside `testWithTimeout`; verify `go test -race -run TestPublish_DuringBuild .` fails by timing out on the cycle rows
- [x] 13.2 GREEN: resolve service listeners from their own scope, relying on the build wait-for graph for cycle detection, without recording a DAG edge (D6); verify the test passes within its timeout and `make test` is green
- [x] 13.3 REFACTOR: Extract Function `listenerInjector`; verify `make test` and `make lint` pass

## 14. Async delivery and the error hook (spec: "Async delivery", "Errors with no caller go to HookEventError")

- [x] 14.1 RED: add `TestAsync_RunsAfterPublish` and `TestAsync_ErrorRouting` (5 rows, capturing `log` output) in `event_async_test.go`; verify `go test -race -run 'TestAsync_(RunsAfterPublish|ErrorRouting)' .` fails to compile naming only `ListenerAsync`, `HookEventError`
- [x] 14.2 GREEN: add `ListenerAsync`, a minimal `EventExecutor` counting its running tasks, task contexts from `WithoutCancel` plus `WithCancelCause`, `InjectorOpts.HookEventError` with the `Logger` then `log` fallback and recovered hook panics (D8, D10); verify the tests pass under `-race` and goleak reports no leaks
- [x] 14.3 REFACTOR: Extract Function `reportEventError`; verify `make test` and `make lint` pass
- [x] 14.4 Add `ExampleListenerAsync` showing `HookEventError`; verify `go test -run ExampleListenerAsync .` passes

## 15. Bounded default executor (spec: "Default executor is bounded", "Replaceable event executor", "Event options are validated")

- [x] 15.1 RED: add `TestAsync_Backpressure` (5 rows), `TestAsync_DefaultParallelism` (2 rows), `TestBoundedEventExecutor` (3 rows) and the parallelism rows of `TestEventOptions`; verify `go test -race -run 'TestAsync_(Backpressure|DefaultParallelism)|TestBoundedEventExecutor|TestEventOptions' .` fails to compile naming only `NewBoundedEventExecutor`, `EventAsyncParallelism`
- [x] 15.2 GREEN: add `BoundedEventExecutor`, `NewBoundedEventExecutor(n uint)` with its panic, `InjectorOpts.EventAsyncParallelism` (0 = `GOMAXPROCS*4` read at creation), done-context-first and slot-or-ctx waiting (D8); verify the tests pass under `-race` within their timeouts
- [x] 15.3 REFACTOR: Extract Function `acquire`; verify `make test` and `make lint` pass
- [x] 15.4 Add `ExampleNewBoundedEventExecutor`; verify `go test -run ExampleNewBoundedEventExecutor .` passes

## 16. Nested async publishing (spec: "Nested async publishing never deadlocks")

- [x] 16.1 RED: add `TestAsync_NestedNoDeadlock` inside `testWithTimeout`; verify `go test -race -run TestAsync_NestedNoDeadlock .` fails by timing out
- [x] 16.2 GREEN: mark task contexts with the executor and run a nested submission inline when no slot is free (D8); verify the test passes and `make test` is green
- [x] 16.3 REFACTOR: none needed: one branch in `Submit`; verify `make test` and `make lint` pass

## 17. Replaceable executor (spec: "Replaceable event executor")

- [x] 17.1 RED: add `TestExecutor_Custom` and `TestExecutor_SubmitFailure` (2 rows); verify `go test -race -run TestExecutor_ .` fails to compile naming only `InjectorOpts.EventExecutor`
- [x] 17.2 GREEN: add `InjectorOpts.EventExecutor` (copied by `copy()`), route async deliveries through it, turn `Submit` errors and panics into `*ListenerError`, call its `Shutdown` at root shutdown; verify the tests pass and `make test` is green
- [x] 17.3 REFACTOR: none needed: the default executor already implements the interface; verify `make test` and `make lint` pass

## 18. Lifecycle events (spec: "Container lifecycle events", "Lifecycle listeners only observe")

- [x] 18.1 RED: add `TestLifecycleEvents_Order` (3 rows), `TestLifecycleEvents_Data`, `TestLifecycleEvents_Observational` (5 rows) and `TestLifecycleEvents_HooksUnchanged` in `event_lifecycle_test.go`; verify `go test -race -run TestLifecycleEvents_ .` fails to compile naming only the `Container*` event types
- [x] 18.2 GREEN: add the six event types and publish each after its hooks through the internal continue-mode path that reports to `HookEventError`, including before a re-raised pipeline panic (D9); verify the tests pass and the existing `container-lifecycle-hooks` tests stay green under `make test`
- [x] 18.3 REFACTOR: Extract Function `publishLifecycle` beside `runObserverHooks`; verify `make test` and `make lint` pass

## 19. Root shutdown (spec: "Shutdown drains async deliveries", "Publishing after shutdown")

- [x] 19.1 RED: add `TestShutdown_Drain` (2 rows), `TestShutdown_ReleasesWaiters`, `TestShutdown_PublishAfter` and `TestShutdown_ClosedFunctionOnly` in `event_shutdown_test.go`; verify `go test -race -run TestShutdown_ .` fails on the ordering, waiter and skip assertions
- [x] 19.2 GREEN: after `ContainerClosing`, close async intake, wake waiting publishers with `ErrPublishAfterShutdown`, drain bounded by the shutdown context and report its error, skip unbuilt and removed service listeners during service shutdown, and deliver `ContainerClosed` to function listeners past the closed check (D7, D9); verify the tests pass under `-race` and goleak reports no leaks
- [x] 19.3 REFACTOR: Extract Function `drainEvents` from the close path; verify `make test` and `make lint` pass

## 20. Scope shutdown drain (spec: "Scope shutdown drains its listeners")

- [x] 20.1 RED: add `TestScopeShutdown_Drain`; verify `go test -race -run TestScopeShutdown_Drain .` fails with "conn-closed" recorded before "delivered"
- [x] 20.2 GREEN: count in-flight async tasks per listener scope and wait for a scope's and its descendants' counters at scope shutdown, bounded by the context (D7); verify the test passes under `-race` and goleak reports no leaks
- [x] 20.3 REFACTOR: Inline any helper from 10.3 that is now single-use; verify `make test` and `make lint` pass

## 21. Concurrency and the plan cache (spec: "Publish racing shutdown", "Concurrent publish and registration")

- [x] 21.1 RED: add `TestPublish_RacingShutdown` and `TestPublish_ConcurrentRegistration` inside `testWithTimeout`, and `BenchmarkPublish/{0,1,10,100}-listeners` and `BenchmarkPublish/nested-scopes` in `bench/`; verify `go test -race -run 'TestPublish_(RacingShutdown|ConcurrentRegistration)' .` fails (race report, leak, or an error other than `ErrPublishAfterShutdown`), and record the benchmark baseline
- [x] 21.2 GREEN: fix the races found (snapshot function listener lists under the scope lock, admit async tasks under the intake lock); verify the tests pass under `-race` and goleak reports no leaks
- [x] 21.3 REFACTOR: Introduce the per-scope plan cache keyed by (type, local) with version-based invalidation (D4), behavior unchanged; verify `make test` and `make lint` pass and `go test -run x -bench BenchmarkPublish ./bench` improves on the 21.1 baseline

## 22. Clone (spec: "Clone copies listeners")

- [x] 22.1 RED: add `TestClone_Listeners` (2 rows) and `TestClone_ExecutorOwnership` (2 rows); verify `go test -race -run TestClone_ .` fails on the clone rows
- [x] 22.2 GREEN: copy each scope's function listeners in `CloneWithOpts`, keep plan caches and the default executor per root, and document that an `InjectorOpts.EventExecutor` is shared, never shut down by a clone (D11); verify the tests pass and `make test` is green
- [x] 22.3 REFACTOR: Move Function for the event-state copy next to the kind-entry copy in `root_scope.go`; verify `make test` and `make lint` pass

## 23. Example module and guide

- [x] 23.1 Rewrite `examples/event-driven` on `Publish`, `AsListener`, `ListenMethod` and `ListenerAsync`, removing its hand-rolled `EventBus`; verify `cd examples/event-driven && go build ./... && go vet ./...` passes and the workspace module list is unchanged
- [x] 23.2 Add an "Events" guide under `docs/` (defaults, overrides, reach, lifetime, async, lifecycle events, the `log` fallback) and an upgrading note stating no breaks; verify the docs pages render with the repo's docs tooling, or `npx markdownlint` when none applies

## 24. Integration checks

- [x] 24.1 Reconcile the design's Test Strategy with the tests: every `#### Scenario:` has its test and every Examples row its subtest; verify with `go test -race -run 'Test(Publish|Listen|AsListener|ListenMethod|Listener|Dispatch|Async|Executor|BoundedEventExecutor|LifecycleEvents|Shutdown|ScopeShutdown|Clone|EventOptions)' -v . | grep -c '^=== RUN'` against the table, fixing any gap
- [x] 24.2 Run the full suite and every workspace module; verify `make test`, `make lint`, and `go build ./... && go test ./...` in each `./http/*` and `./examples/*` module pass
- [x] 24.3 Validate the change; verify `openspec validate add-eventing` passes (never `--strict`: it rejects the fenced Gherkin scenarios the schema requires)

## 25. Review fixes (spec: "Publishing after shutdown", "Shutdown drains async deliveries", "Scope shutdown drains its listeners", "Container lifecycle events", "Listen registers a function listener", "Clone copies listeners")

Each defect found in verification and review was reproduced by a failing test first; the test names are in the design's Test Strategy.

- [x] 25.1 RED: add `TestShutdown_SyncListenersRunAfterAsyncRefusal` (2 rows); verify the EventFailFast row fails with the synchronous listener called 0 times
- [x] 25.2 GREEN: collect async refusals matching `ErrPublishAfterShutdown` and continue a fail-fast dispatch, returning them joined with any later error (D5); verify the test passes and `make test` is green
- [x] 25.3 RED: add `TestShutdown_ListenerErrorMatchingErrShutdown`; verify it fails with Publish returning nil
- [x] 25.4 GREEN: skip a service listener during root shutdown only when resolving it fails with `ErrShutdown`, never for an error its OnEvent returns (D7); verify the test passes
- [x] 25.5 RED: add `TestLifecycleEvents_ServiceListenerOnly`; verify it fails with the listener called 0 times
- [x] 25.6 GREEN: allocate the root's event state when a registration declares listener bindings (D9); verify the test passes
- [x] 25.7 RED: add `TestLifecycleEvents_AsyncClosed`; verify it fails with the listener not run and an `ErrPublishAfterShutdown` refusal reported to `HookEventError`
- [x] 25.8 GREEN: deliver the async listeners of a lifecycle event published after the async intake closed inline, and document it on `ContainerClosed` (D9); verify the test passes
- [x] 25.9 RED: add `TestShutdown_DrainHonoursDeadline`; verify it fails with ShutdownWithContext taking about 1s past a 20ms context
- [x] 25.10 GREEN: drop the post-abort grace wait, so the drain returns once the caller's context ended; make `TestShutdown_Drain`'s timed-out row wait for "cancelled" (D7); verify both pass 200 times under `-race`
- [x] 25.11 RED: add `TestBoundedEventExecutor_ShutdownTimeoutsLeakNoWaiter`; verify it fails with 5 goroutines left waiting after 5 timed-out Shutdown calls
- [x] 25.12 RED: add `TestShutdown_FromAsyncListener` (2 rows) inside `testWithTimeout`; verify both rows fail by timing out
- [x] 25.13 RED: add `TestScopeShutdown_RefusesDeliveryAdmittedAfter`; verify it fails with Publish returning nil after the scope's Shutdown returned
- [x] 25.14 GREEN: count running executor tasks with one shared channel per wait bound; mark task and delivery contexts so a Shutdown or drain called with one does not wait for the calling task; admit async deliveries under each child scope's lock with a closed check, refusing with `ErrPublishAfterShutdown` (D7, D8); verify 25.11-25.13 pass and `make test` is green
- [x] 25.15 RED: add `TestClone_ShutdownLeavesSuppliedExecutor`; verify it fails with "event executor is shut down" on the source
- [x] 25.16 GREEN: mark an `InjectorOpts.EventExecutor` a clone shares with its source as borrowed, never shut down by the clone, and update the field's and `CloneWithOpts`'s godoc (D11); verify the test passes
- [x] 25.17 RED: add `TestListen_ClosedScope` (2 rows); verify both fail with Listen not panicking
- [x] 25.18 GREEN: make `Listen` panic with an error matching `ErrPublishAfterShutdown` on a closed scope, checked under the scope's lock, and document it (D7); verify the test passes
- [x] 25.19 Add `TestLifecycleEvents_FailedBeforeRepanic` (ContainerFailed published before a start panic is re-raised) and assert in `TestShutdown_PublishAfter` that "*Built" received the event once, making "*DB" depend on "*Built" so it shuts down first; verify both pass
- [x] 25.20 REFACTOR: Remove the dead `plannedListener.deliver`; Extract Function `dispatchConfig.skips` and `dispatchConfig.async` from `dispatch`; verify `make test` and `make lint` pass
- [x] 25.21 Update the Events guide (closed-scope Listen, clone executor ownership, inline async ContainerClosed, sync listeners after refusals, deadline, self-shutdown with the delivery context); verify the page reads consistently with the godoc

## Workflow follow-up

- Archive the change once `make test`, `make lint` and `openspec validate add-eventing` pass and every scenario maps to a passing test.
- Start `add-durable-events` (durable and transaction-bound delivery) on top of this listener model, carrying over the durable spec from this change's history and the audit's durable findings.
