# Tasks

Specs: `specs/events/spec.md` (E) and `specs/event-publication-registry/spec.md` (R). Test names and files follow the design's Test Strategy table; decisions are referenced as D1-D11. All `go test` commands run from the repo root on package `.`.

## 1. Publish by assignability (E: "Publish delivers by assignability")

- [ ] 1.1 RED: add `TestPublish_Assignability` (all 6 rows) and `TestPublish_NoListeners` in `event_test.go`, using a minimal function listener via `Listen`; verify `go test -race -run 'TestPublish_(Assignability|NoListeners)' .` fails to compile naming only `Publish`/`Listen`
- [ ] 1.2 GREEN: add `Publish`, `Listen` (no options yet), `Listener[E]` and a root-owned listener registry matching by `reflect.Type.AssignableTo` (D1); verify the tests pass and `make test` is green
- [ ] 1.3 REFACTOR: Extract Function for the match step, Introduce Parameter Object for the registry entry; verify `make test` and `make lint` pass

## 2. Publish argument handling (E: "Publish argument handling")

- [ ] 2.1 RED: add `TestPublish_ZeroArguments` (5 rows: nil interface, nil pointer, nil injector, nil context, zero value); verify `go test -race -run TestPublish_ZeroArguments .` fails on the nil rows (missing `ErrInvalidEvent`)
- [ ] 2.2 GREEN: add `ErrInvalidEvent`, nil-event/nil-pointer detection, nil injector -> `DefaultRootScope`, nil context -> `context.Background()`; verify the test passes and `make test` is green
- [ ] 2.3 REFACTOR: Extract Function `normalizePublishArgs`; verify `make test` and `make lint` pass

## 3. Dispatch plan cache (D1 hot path)

- [ ] 3.1 RED: add `TestPublish_PlanCacheInvalidation` (publish, register a second listener, publish again: second publish reaches both); verify it fails once a naive cache is stubbed in, or record that it passes on arrival and show it fails by disabling invalidation (not committed)
- [ ] 3.2 GREEN: cache plans per dynamic type keyed by a registry version counter; verify the test passes and `make test` is green
- [ ] 3.3 REFACTOR: Extract Type `dispatchPlan`; add `BenchmarkPublish/{1,10,100}-listeners` in `bench/` as a baseline; verify `make test`, `make lint` and `go test -run x -bench BenchmarkPublish ./bench` pass

## 4. AsListener declares a listener service (E: "AsListener declares a listener service")

- [ ] 4.1 RED: add `TestAsListener_Registration` (3 rows), `TestAsListener_InterfaceCheckedAtDispatch`, `TestAsListener_Repeated` in `event_listener_test.go`; verify `go test -race -run TestAsListener_ .` fails to compile naming only `AsListener`/`ListenerError`/`ErrInvalidListener`
- [ ] 4.2 GREEN: add `AsListener[E]` as a `ProvideOption` capturing a typed closure (D2), registration panic text, instance check at first dispatch, `ListenerError` with its message and `Unwrap`, `ErrInvalidListener`; verify the tests pass and `make test` is green
- [ ] 4.3 REFACTOR: Extract Function shared with `AsStarter`'s registration-type check if the shapes match, else none needed with reason; verify `make test` and `make lint` pass

## 5. Lazy listener services (E: "Listener services are built lazily")

- [ ] 5.1 RED: add `TestListener_LazyBuild` (4 rows: lazy 0/1/3, transient 3) and `TestListener_ProviderError`; verify `go test -race -run 'TestListener_(LazyBuild|ProviderError)' .` fails on the build-count or error-wrapping assertions
- [ ] 5.2 GREEN: resolve service listeners by name from their own scope on first delivery, wrap provider errors in `*ListenerError` with the default `ListenerID` (D3 scope-path form); verify the tests pass and `make test` is green
- [ ] 5.3 REFACTOR: Extract Function `resolveListener`; verify `make test` and `make lint` pass

## 6. ListenMethod (E: "ListenMethod declares a method listener")

- [ ] 6.1 RED: add `TestListenMethod_MultipleEvents` and `TestListenMethod_Registration` (3 rows); verify `go test -race -run TestListenMethod_ .` fails to compile naming only `ListenMethod`
- [ ] 6.2 GREEN: add `ListenMethod[S, E]` with the registered-type and nil-method panics; verify the tests pass and `make test` is green
- [ ] 6.3 REFACTOR: Extract Function so `AsListener` and `ListenMethod` share one registration path (`AsListener` as a method adapter); verify `make test` and `make lint` pass
- [ ] 6.4 Add `ExampleListenMethod` and godoc on `Listener`, `AsListener`, `ListenMethod`; verify `go test -run ExampleListenMethod .` passes

## 7. Listen and cancel (E: "Listen registers a function listener")

- [ ] 7.1 RED: add `TestListen_Cancel` (3 rows: 0, 1, 2 cancels) and `TestListen_NilFunc`; verify `go test -race -run TestListen_ .` fails on the cancel and panic assertions
- [ ] 7.2 GREEN: return an idempotent cancel that unregisters and bumps the registry version; panic on nil function; nil injector -> `DefaultRootScope`; verify the tests pass and `make test` is green
- [ ] 7.3 REFACTOR: none expected (small surface); if cancel and service removal duplicate logic, Extract Function `unregister`; verify `make test` and `make lint` pass
- [ ] 7.4 Add `ExampleListen` and `ExamplePublish` documenting zero-config defaults; verify `go test -run 'Example(Listen|Publish)$' .` passes

## 8. Listener order (E: "Listener order is deterministic")

- [ ] 8.1 RED: add `TestDispatch_Order` with the r2, c1, c2, *Mailer, r1 setup in `event_dispatch_test.go`; verify `go test -race -run TestDispatch_Order .` fails to compile naming only `ListenerOrder`
- [ ] 8.2 GREEN: add `ListenerOption`, `ListenerOrder`; sort plans by order, scope depth from the publishing scope, registration sequence (D3); verify the test passes and `make test` is green
- [ ] 8.3 REFACTOR: Extract Function `lessListener`; verify `make test` and `make lint` pass

## 9. Listener filter (E: "Listener filter")

- [ ] 9.1 RED: add `TestDispatch_Filter` (4 rows incl. nil predicate and predicate panic); verify `go test -race -run TestDispatch_Filter .` fails to compile naming only `ListenerFilter`
- [ ] 9.2 GREEN: add typed `ListenerFilter`, evaluate it before resolving the listener, recover predicate panics into `*ListenerError`; verify the test passes and `make test` is green
- [ ] 9.3 REFACTOR: Extract Function `recoverAsListenerError` for reuse by group 12; verify `make test` and `make lint` pass

## 10. Scope reach (E: "Event reach across scopes")

- [ ] 10.1 Confirm `add-extensions` is archived (its `Extend` and `ext:*` private scopes exist); verify `openspec list --json` no longer lists it as in progress, otherwise implement 10.2-10.4 without the two `ext:audit` rows and track them in group 31
- [ ] 10.2 RED: add `TestDispatch_ScopeReach` (9 rows incl. `ext:audit` and `PublishLocal`); verify `go test -race -run TestDispatch_ScopeReach .` fails on the sibling/descendant/extension rows or to compile naming only `PublishLocal`
- [ ] 10.3 GREEN: restrict plans to the publishing scope, its ancestors and the root's extension private scopes; add `PublishOption` and `PublishLocal` (D4); verify the test passes and `make test` is green
- [ ] 10.4 REFACTOR: Extract Function `reachableScopes`; verify `make test` and `make lint` pass

## 11. Error modes (E: "Fail-fast is the default error mode", "EventContinue runs every listener")

- [ ] 11.1 RED: add `TestDispatch_FailFast` and `TestDispatch_ErrorMode` (4 rows); verify `go test -race -run 'TestDispatch_(FailFast|ErrorMode)' .` fails to compile naming only `EventErrorMode`/`EventContinue`
- [ ] 11.2 GREEN: add `EventErrorMode` (zero = `EventFailFast`), `InjectorOpts.EventErrorMode` copied by `copy()`, join errors in continue mode; verify the tests pass and `make test` is green
- [ ] 11.3 REFACTOR: Replace Conditional with Polymorphism only if a third mode appears, else none needed (two-branch switch is clearer); verify `make test` and `make lint` pass

## 12. Panics and nested publish (E: "Listener panics are recovered", "Nested publish completes before the outer dispatch continues")

- [ ] 12.1 RED: add `TestDispatch_Panic` (4 rows: error value, string, int, nil) and `TestDispatch_Nested`; verify `go test -race -run 'TestDispatch_(Panic|Nested)' .` fails on the panic rows (panic escapes) 
- [ ] 12.2 GREEN: recover listener panics with `%w` for errors and `%v` otherwise; hold no lock while listener code runs so nested `Publish` works; verify the tests pass and `make test` is green
- [ ] 12.3 REFACTOR: Inline any helper left single-use after 9.3; verify `make test` and `make lint` pass

## 13. Async delivery and its errors (E: "Async delivery")

- [ ] 13.1 RED: add `TestAsync_RunsAfterPublish` and `TestAsync_ErrorHandler` (2 rows) in `event_async_test.go`, wrapped in `testWithTimeout`; verify `go test -race -run TestAsync_ .` fails to compile naming only `Async`/`EventAsyncErrorHandler`
- [ ] 13.2 GREEN: add `Async()`, an unbounded-for-now executor with a `WaitGroup`, `context.WithoutCancel`, `InjectorOpts.EventAsyncErrorHandler` defaulting to `Logf` with the D6 message; verify the tests pass under `-race` and goleak reports no leaks
- [ ] 13.3 REFACTOR: Extract Type `defaultExecutor` behind `EventExecutor`; verify `make test` and `make lint` pass

## 14. Bounded default executor (E: "Default executor is bounded")

- [ ] 14.1 RED: add `TestAsync_Backpressure` (3 rows: 0, 3, 4 in flight at limit 4); verify `go test -race -run TestAsync_Backpressure .` fails on the at-limit row (Publish does not block)
- [ ] 14.2 GREEN: add a `GOMAXPROCS(0)*4` semaphore whose acquire selects on `ctx.Done()`; verify the test passes under `-race` within its timeout and `make test` is green
- [ ] 14.3 REFACTOR: Extract Function `acquireSlot`; verify `make test` and `make lint` pass

## 15. Replaceable executor (E: "Replaceable event executor")

- [ ] 15.1 RED: add `TestExecutor_Custom` and `TestExecutor_SubmitError`; verify `go test -race -run TestExecutor_ .` fails to compile naming only `InjectorOpts.EventExecutor`
- [ ] 15.2 GREEN: add `InjectorOpts.EventExecutor` (copied by `copy()`), route async deliveries through it, wrap `Submit` errors in `*ListenerError`; verify the tests pass and `make test` is green
- [ ] 15.3 REFACTOR: none expected; verify `make test` and `make lint` pass
- [ ] 15.4 Add `ExampleEventExecutor` (inline executor for tests) and godoc on `Async`, `EventExecutor`; verify `go test -run ExampleEventExecutor .` passes

## 16. Lifecycle events (E: "Container lifecycle events")

- [ ] 16.1 RED: add `TestLifecycleEvents_Order` (3 rows), `TestLifecycleEvents_Observational` (3 rows), `TestLifecycleEvents_HooksUnchanged` in `event_lifecycle_test.go`; verify `go test -race -run TestLifecycleEvents_ .` fails to compile naming only the `Container*` event types
- [ ] 16.2 GREEN: add the six event types and publish each right after its hooks in `root_start.go` and the shutdown path (D7), sending listener errors and panics to the async error handler; verify the tests pass and the existing `container-lifecycle-hooks` tests stay green under `make test`
- [ ] 16.3 REFACTOR: Extract Function `publishLifecycle` next to `runObserverHooks`; verify `make test` and `make lint` pass

## 17. Shutdown drain and publishing after shutdown (E: "Shutdown drains async deliveries", "Publish after shutdown")

- [ ] 17.1 RED: add `TestShutdown_Drain` (2 rows), `TestShutdown_PublishAfter` (2 rows), `TestLifecycleEvents_ClosedFunctionOnly`; verify `go test -race -run 'TestShutdown_|TestLifecycleEvents_ClosedFunctionOnly' .` fails to compile naming only `ErrPublishAfterShutdown`
- [ ] 17.2 GREEN: close async intake after `ContainerClosing`, call `Drain(shutdownCtx)` before services shut down, add the drain error to the report, reject publishing after Closed, deliver `ContainerClosed` to function listeners only (D7); verify the tests pass under `-race` and goleak reports no leaks
- [ ] 17.3 REFACTOR: Extract Function `closeEvents` from the shutdown path; verify `make test` and `make lint` pass

## 18. Concurrency and clone of listeners (E: "Concurrent publish and registration", "Clone copies listeners")

- [ ] 18.1 RED: add `TestPublish_ConcurrentRegistration` (in `testWithTimeout`) and `TestClone_Listeners` (2 rows); verify `go test -race -run 'TestPublish_ConcurrentRegistration|TestClone_Listeners' .` fails (race report or missing clone listeners)
- [ ] 18.2 GREEN: guard the registry with a mutex held only for snapshotting, copy it in `CloneWithOpts` after the scope tree and rebind service listeners (D11); verify the tests pass under `-race` and `make test` is green
- [ ] 18.3 REFACTOR: Move Function for registry copying next to the kind-entry copy in `root_scope.go`; verify `make test` and `make lint` pass

## 19. Recording and completing durable publications (R: "Durable publications are recorded before delivery")

- [ ] 19.1 RED: add `TestDurable_RecordAndComplete` (fake clock) and `TestDurable_OnlyDurableRecorded` (3 rows) in `event_publication_test.go` with a recording store; verify `go test -race -run TestDurable_ .` fails to compile naming only `Durable`/`PublicationStore`/`Publication`
- [ ] 19.2 GREEN: add the `PublicationStore` SPI and `Publication`, an in-memory store, `Durable()`, `crypto/rand` IDs, the default JSON codec and import-path type names, and Append-before-dispatch then `Complete` (D9 steps 1-5); verify the tests pass and `make test` is green
- [ ] 19.3 REFACTOR: Extract Function `recordPublications`; verify `make test` and `make lint` pass

## 20. Durable failures and recording failures (R: "Durable delivery failures are recorded", "Recording failures stop the publish")

- [ ] 20.1 RED: add `TestDurable_DeliveryFailure` (2 rows) and `TestDurable_RecordingFailure` (2 rows); verify `go test -race -run 'TestDurable_(DeliveryFailure|RecordingFailure)' .` fails on the `Fail`/no-listener-ran assertions
- [ ] 20.2 GREEN: call `Fail` and the async error handler on durable delivery errors without returning them; return encode/Append errors wrapped with the event type before any listener runs; verify the tests pass and `make test` is green
- [ ] 20.3 REFACTOR: none expected; verify `make test` and `make lint` pass

## 21. Listener IDs (R: "Durable listeners need a stable ID")

- [ ] 21.1 RED: add `TestDurable_ListenerID` (5 rows); verify `go test -race -run TestDurable_ListenerID .` fails to compile naming only `ListenerID`, or on the panic rows
- [ ] 21.2 GREEN: add `ListenerID`, the scope-path default, the durable-function and duplicate-ID panics (D3); verify the test passes and `make test` is green
- [ ] 21.3 REFACTOR: Extract Function `defaultListenerID` shared with group 5; verify `make test` and `make lint` pass

## 22. Event type registry and codec (R: "Event type registry", "Replaceable codec")

- [ ] 22.1 RED: add `TestRegisterEventType` (5 rows), `TestCodec_Choice` (2 rows), `FuzzJSONCodec_RoundTrip` in `event_codec_test.go`; verify `go test -race -run 'TestRegisterEventType|TestCodec_Choice' .` fails to compile naming only `RegisterEventType`/`ErrEventTypeConflict`/`EventCodec`
- [ ] 22.2 GREEN: add `RegisterEventType[E](root, name) error`, `ErrEventTypeConflict`, `EventCodec`, `InjectorOpts.EventCodec` (copied by `copy()`); verify the tests pass, `go test -run x -fuzz FuzzJSONCodec_RoundTrip -fuzztime 30s .` finds nothing, and `make test` is green
- [ ] 22.3 REFACTOR: Extract Type `eventTypeRegistry`; verify `make test` and `make lint` pass

## 23. Transaction binding (R: "Transaction binding and phases", "No bound transaction")

- [ ] 23.1 RED: add `TestTxPhase` (9 rows), `TestTx_DurableAppendInTx`, `TestTx_Fallback` (4 rows) in `event_tx_test.go` with a fake `Tx`/`TxBinder`; verify `go test -race -run 'TestTx' .` fails to compile naming only `Tx`/`TxBinder`/`Phase`/`TxFallback`
- [ ] 23.2 GREEN: add `Tx`, `TxBinder`, `TxPhase`, `Phase`, `TxFallback`/`TxRun`/`TxSkip`, the never-binding default binder, deferral to `OnCommit`/`OnRollback`, and the durable default phase (D8); verify the tests pass and `make test` is green
- [ ] 23.3 REFACTOR: Replace Conditional with Polymorphism for phase scheduling if the switch exceeds four arms, else none needed; verify `make test` and `make lint` pass

## 24. Redelivery at Start (R: "Redelivery at Start")

- [ ] 24.1 RED: add `TestRedelivery_Start` (5 rows), `TestRedelivery_Unresolvable` (3 rows), `TestRedelivery_ListenerError`, `TestRedelivery_ClaimError`; verify `go test -race -run TestRedelivery_ .` fails to compile naming only `PublicationBatchSize`/`ErrListenerNotFound`/`ErrEventTypeNotRegistered`
- [ ] 24.2 GREEN: run the claim loop after Starters and before Started hooks with `InjectorOpts.PublicationBatchSize` (default 100), `Fail` unresolvable publications, fail `Start` on a `Claim` error (D9 loop); verify the tests pass and `make test` is green
- [ ] 24.3 REFACTOR: Extract Function `redeliverBatch` for reuse by group 25; verify `make test` and `make lint` pass

## 25. Periodic retry and retention (R: "Periodic retry", "Retention of completed publications")

- [ ] 25.1 RED: add `TestRetry_Backoff` (7 rows), `TestRetry_StopsAtShutdown`, `TestRetention_Purge` (3 rows) in `event_retry_test.go` with the fake clock; verify `go test -race -run 'TestRetry_|TestRetention_' .` fails to compile naming only `EventRetryInterval`/`PublicationRetention`
- [ ] 25.2 GREEN: add one root-owned retry goroutine started after `Start` and stopped before services shut down, exponential backoff from `PublishedAt`, retention purge per D9; verify the tests pass under `-race` and goleak reports no leaks
- [ ] 25.3 REFACTOR: Extract Function `nextAttemptAt`; verify `make test` and `make lint` pass

## 26. Store and binder resolution (R: "Store and binder resolution")

- [ ] 26.1 RED: add `TestStoreResolution` (5 rows) and `TestStoreResolution_InMemoryWarning`; verify `go test -race -run TestStoreResolution .` fails to compile naming only `InjectorOpts.PublicationStore`
- [ ] 26.2 GREEN: resolve store and binder once per root (opts, then `NameOf[...]()` service including `AsDefault` yielding, then default) and log the in-memory warning once (D10); verify the tests pass and `make test` is green
- [ ] 26.3 REFACTOR: Extract generic Function `resolveOnce[T]` shared by store and binder; verify `make test` and `make lint` pass

## 27. Publication identity (R: "Publication identity during delivery")

- [ ] 27.1 RED: add `TestPublicationFromContext` (2 rows); verify `go test -race -run TestPublicationFromContext .` fails to compile naming only `PublicationFromContext`/`InjectorOpts.PublicationIDGenerator`
- [ ] 27.2 GREEN: add `PublicationFromContext` with an unexported context key and `InjectorOpts.PublicationIDGenerator`; verify the test passes and `make test` is green
- [ ] 27.3 REFACTOR: none expected; verify `make test` and `make lint` pass

## 28. Durable concurrency and clone (R: "Concurrent durable publishes", "Clone resolves its own store")

- [ ] 28.1 RED: add `TestDurable_ConcurrentPublish` (in `testWithTimeout`) and `TestClone_StoreIsolation`; verify `go test -race -run 'TestDurable_ConcurrentPublish|TestClone_StoreIsolation' .` fails (race report, duplicate completion, or shared store)
- [ ] 28.2 GREEN: make the in-memory store and completion path race-safe, and have the clone copy registrations and the type registry but resolve its own store, binder and retry loop (D11); verify the tests pass under `-race` and `make test` is green
- [ ] 28.3 REFACTOR: none expected; verify `make test` and `make lint` pass
- [ ] 28.4 Add `ExampleDurable` (in-memory default, then an `InjectorOpts.PublicationStore` override) and godoc on the durable SPI stating at-least-once and the in-memory store's limits; verify `go test -run ExampleDurable .` passes

## 29. Example module and guide

- [ ] 29.1 Rewrite `examples/event-driven` on `Publish`/`AsListener`/`ListenMethod`, replacing its hand-rolled `EventBus`; verify `cd examples/event-driven && go build ./... && go vet ./...` passes and the workspace module list is unchanged
- [ ] 29.2 Add an "Events" guide under `docs/` (defaults, overrides, scope reach, async, lifecycle events, durable delivery, transaction binding for store authors) and an upgrading note stating no breaks; verify the docs build used by the repo (or `npx markdownlint` if none) reports no errors

## 30. Integration checks

- [ ] 30.1 Reconcile the design's Test Strategy with the test files: every `#### Scenario:` has its test and every Examples row its subtest; verify by listing `go test -run . -v . | grep -c '^=== RUN'` against the table and fixing any gap
- [ ] 30.2 Run the full suite and every workspace module; verify `make test`, `make lint`, and `go build ./... && go test ./...` in each `./http/*` and `./examples/*` module pass
- [ ] 30.3 Validate the change; verify `openspec validate add-eventing --strict` passes

## 31. Deferred extension-scope reach

Only needed if 10.1 found `add-extensions` not yet archived. Otherwise check 31.1-31.3 off with the note "covered in group 10".

- [ ] 31.1 RED: add the two `ext:audit` rows to `TestDispatch_ScopeReach`; verify they fail once `add-extensions` is archived
- [ ] 31.2 GREEN: include the root's extension private scopes in `reachableScopes`; verify `go test -race -run TestDispatch_ScopeReach .` passes and `make test` is green
- [ ] 31.3 REFACTOR: none expected; verify `make test` and `make lint` pass

## Workflow follow-up

- Archive the change once `make test`, `make lint` and `openspec validate add-eventing --strict` pass and every scenario maps to a passing test.
- Follow-up change: ship the first crash-safe `PublicationStore` module (decides the design's open question about one `database/sql` module vs. per-database modules).
