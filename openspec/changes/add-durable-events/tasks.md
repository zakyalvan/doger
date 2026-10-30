# Tasks

Specs: `specs/transactional-events/spec.md` (T) and `specs/event-publication-registry/spec.md` (R). Test names and files follow the design's Test Strategy; decisions are D1-D15. Commands run from the repo root on package `.` unless stated. Every RED must fail for the stated reason before its GREEN starts.

## 0. Prerequisite

- [ ] 0.1 Confirm `add-eventing` is implemented and archived; verify `openspec list --json` does not list it and `go doc . Publish` and `go doc . ListenerOption` succeed

## 1. Transaction phases (T: "Listeners run in their transaction phase", "Transaction options are validated")

- [ ] 1.1 RED: add `TestTxPhase` (9 rows), `TestTxPhase_Order`, `TestTxOptions` (3 rows) and `TestTxEnums_String` (7 rows) in `event_tx_test.go` with a fake `Tx`/`TxSource` passed via `InjectorOpts.TxSource`; verify `go test -race -run 'TestTx(Phase|Options|Enums)' .` fails to compile naming only `Tx`, `TxSource`, `TxPhase*`, `ListenerTxPhase`, `InjectorOpts.TxSource`
- [ ] 1.2 GREEN: add `Tx`, `TxSource`, `TxPhase` with `String()`, `ListenerTxPhase`, `InjectorOpts.TxSource` (copied by `copy()`), one `Lookup` per publish when a deferred listener is in the plan, and per-callback closures in plan order (D1, D2); verify the tests pass and `make test` is green
- [ ] 1.3 REFACTOR: Extract Function `deferListeners` out of dispatch; verify `make test` and `make lint` pass

## 2. Detached context and deferred failures (T: "Deferred listeners run detached", "Deferred listener errors go to HookEventError")

- [ ] 2.1 RED: add `TestTx_DetachedContext` and `TestTx_DeferredFailures` (2 rows); verify `go test -race -run 'TestTx_(DetachedContext|DeferredFailures)' .` fails on the context and error-routing assertions
- [ ] 2.2 GREEN: run deferred listeners with `TxSource.Detach(publishCtx)`, recover panics, report every failure to `HookEventError` and continue with the rest (D2); verify the tests pass and `make test` is green
- [ ] 2.3 REFACTOR: reuse `add-eventing`'s `reportEventError` instead of any local copy (Inline Function); verify `make test` and `make lint` pass

## 3. Fallback without a transaction (T: "Fallback when no transaction is bound")

- [ ] 3.1 RED: add `TestTx_Fallback` (5 rows); verify `go test -race -run TestTx_Fallback .` fails to compile naming only `TxFallbackMode`, `ListenerTxFallback`, `TxFallbackSkip`
- [ ] 3.2 GREEN: add `TxFallbackMode` with `String()` and `ListenerTxFallback`, run deferred listeners immediately or drop them before recording when `Lookup` reports false (D2); verify the test passes and `make test` is green
- [ ] 3.3 REFACTOR: none needed: one branch in plan filtering; verify `make test` and `make lint` pass
- [ ] 3.4 Add `ExampleListenerTxPhase` with a fake `TxSource`; verify `go test -run ExampleListenerTxPhase .` passes

## 4. TxSource resolution without a store (T: "TxSource resolution")

- [ ] 4.1 RED: add the rows of `TestTxSourceResolution` that need no store (opts, service, none) and `TestTxSourceResolution_Once`; verify `go test -race -run TestTxSourceResolution .` fails on the service rows (service not consulted, or built more than once)
- [ ] 4.2 GREEN: resolve `InjectorOpts.TxSource`, else the `NameOf[TxSource]()` service, once per root with a guarded result (D3); verify the tests pass and `make test` is green
- [ ] 4.3 REFACTOR: Extract generic Function `resolveOnce[T]` for reuse by the store resolution in group 20; verify `make test` and `make lint` pass

## 5. Store SPI and the in-memory store (R: "Store conformance suite", first rows)

- [ ] 5.1 RED: add package `publicationtest` with `TestStore` subtests "append leases", "stale token", "complete deletes", and `TestTestStore` rows for `NewMemoryPublicationStore()` and "a store whose Complete accepts any token"; verify `go test -race ./publicationtest` fails to compile naming only `PublicationStore`, `NewMemoryPublicationStore`, `Claimed`, `AppendRequest`, `CompleteInfo`, `FailInfo`, `ErrClaimLost`
- [ ] 5.2 GREEN: add `Publication`, `Claimed`, the request and info structs, `PublicationStore`, `ErrClaimLost`, and a mutex-guarded `MemoryPublicationStore` implementing `Append`, `Complete`, `Fail` with leases and tokens (D5, D12); verify `go test -race ./publicationtest` passes and `make test` is green
- [ ] 5.3 REFACTOR: Extract Function `checkToken` in the memory store; verify `make test` and `make lint` pass

## 6. Recording durable publications (R: "Durable publications are recorded before delivery")

- [ ] 6.1 RED: add `TestDurable_RecordAndComplete`, `TestDurable_WhatIsRecorded` (6 rows) and `TestDurable_DynamicType` (2 rows) in `event_publication_test.go` with a recording store wrapping the memory store; verify `go test -race -run 'TestDurable_(RecordAndComplete|WhatIsRecorded|DynamicType)' .` fails to compile naming only `ListenerDurable`, `InjectorOpts.PublicationStore`
- [ ] 6.2 GREEN: add `ListenerDurable`, `InjectorOpts.PublicationStore`, the recording step after filtering (default JSON encoding, import-path dynamic type names, UUIDv7 IDs from the root clock, one `Append`), delivery with the claim token in context, and `Complete` (D6, D11, D13); verify the tests pass and `make test` is green
- [ ] 6.3 REFACTOR: Extract Function `recordPublications` and Introduce Parameter Object `recording`; verify `make test` and `make lint` pass

## 7. Retry policy (R: "Retry policy")

- [ ] 7.1 RED: add `TestExponentialRetry` (7 rows) and `TestExponentialRetry_Jitter` in `event_retry_policy_test.go`; verify `go test -race -run TestExponentialRetry .` fails to compile naming only `RetryPolicy`, `ExponentialRetry`
- [ ] 7.2 GREEN: add `RetryPolicy` and `ExponentialRetry` with the overflow guard, the attempts-below-1 rule and injectable `Rand` (D9); verify the tests pass and `make test` is green
- [ ] 7.3 REFACTOR: none needed: a pure function; verify `make test` and `make lint` pass
- [ ] 7.4 Add `ExampleExponentialRetry`; verify `go test -run ExampleExponentialRetry .` passes

## 8. Delivery outcomes and lost leases (R: "Delivery outcomes are recorded", "Live deliveries hold a lease")

- [ ] 8.1 RED: add `TestDurable_DeliveryFailure` (2 rows), `TestDurable_FailureDoesNotStop`, `TestDurable_NonDurableFailure` and `TestLease_Lost`; verify `go test -race -run 'TestDurable_(DeliveryFailure|FailureDoesNotStop|NonDurableFailure)|TestLease_Lost' .` fails to compile naming only `InjectorOpts.PublicationRetryPolicy`, or on the outcome assertions
- [ ] 8.2 GREEN: add `InjectorOpts.PublicationRetryPolicy`; on error or panic call `Fail` with the policy's next attempt and report to `HookEventError`; never return durable delivery errors; schedule durable deliveries independently of fail-fast; report `ErrClaimLost` without retrying (D6, D8); verify the tests pass and `make test` is green
- [ ] 8.3 REFACTOR: Extract Function `finishDelivery`; verify `make test` and `make lint` pass

## 9. Recording failures (R: "Recording failures stop the publish")

- [ ] 9.1 RED: add `TestDurable_RecordingErrors` (5 rows); verify `go test -race -run TestDurable_RecordingErrors .` fails to compile naming only `EventCodec`, `InjectorOpts.EventCodec`, `InjectorOpts.PublicationIDGenerator`, `ErrInvalidPublication`
- [ ] 9.2 GREEN: add the `EventCodec` interface with the JSON default, `InjectorOpts.EventCodec` and `PublicationIDGenerator`, `ErrInvalidPublication`, and wrap encode, ID and `Append` errors and panics with "DI: failed to record event `<type>`" before any listener runs (D6); verify the test passes and `make test` is green
- [ ] 9.3 REFACTOR: Extract Function `recordingError`; verify `make test` and `make lint` pass

## 10. Durable registration rules (R: "Durable listener registration rules")

- [ ] 10.1 RED: add `TestDurable_Registration` (8 rows) and `TestDurable_ImmediateInTx`; verify `go test -race -run 'TestDurable_(Registration|ImmediateInTx)' .` fails on the panic rows or to compile naming only `ErrTxPhaseNotDurable`
- [ ] 10.2 GREEN: check scope (root or extension via `root.ext`), phase, function-listener ID and live-ID uniqueness by scanning root and extension scopes at registration; add `ErrTxPhaseNotDurable` (D4); verify the tests pass and `make test` is green
- [ ] 10.3 REFACTOR: Extract Function `durableIDsInUse`; verify `make test` and `make lint` pass

## 11. Recording inside a transaction (R: "Recording inside a transaction"; T: "TxSource resolution", store rows)

- [ ] 11.1 RED: add `TestDurable_TxOutcome` (2 rows) and `TestDurable_TxNotSupported` in `event_publication_tx_test.go`, plus the `TxAwareStore` rows of `TestTxSourceResolution`; verify `go test -race -run 'TestDurable_Tx|TestTxSourceResolution' .` fails to compile naming only `TxAwareStore`, `ErrTxNotSupported`, `ErrTxSourceConflict`
- [ ] 11.2 GREEN: add `TxAwareStore`, use the store's `TxSource` in resolution, fail with `ErrTxSourceConflict` on a different configured source, fail durable publishes in a transaction with `ErrTxNotSupported` when the store is not transaction-aware, and complete with a detached context after commit (D2, D3); verify the tests pass and `make test` is green
- [ ] 11.3 REFACTOR: Move Function for the transaction checks into one `checkDurableTx`; verify `make test` and `make lint` pass

## 12. Redelivery pass at Start (R: "Redelivery at Start")

- [ ] 12.1 RED: add `TestRedelivery_Pass` (5 rows), `TestRedelivery_Unresolvable` (4 rows), `TestRedelivery_ClaimError` and `TestRedelivery_NoDurableListeners` in `event_redelivery_test.go`, each inside `testWithTimeout`; verify `go test -race -run TestRedelivery_ .` fails to compile naming only `PublicationClaimer`, `ClaimRequest`, `InjectorOpts.PublicationBatchSize`, `ErrListenerNotFound`, `ErrEventTypeNotFound`, `ErrCodecMismatch`
- [ ] 12.2 GREEN: add `PublicationClaimer` (and `Claim` on the memory store), `PublicationBatchSize`, the sentinels, and the pass with a cutoff and seen-set after Starters and before the Started hooks, only with durable listeners and a claimer (D7); verify the tests pass within their timeouts and `make test` is green
- [ ] 12.3 REFACTOR: Extract Function `redeliverBatch` for reuse by group 13; verify `make test` and `make lint` pass

## 13. Retry loop (R: "Retry loop")

- [ ] 13.1 RED: add `TestRetryLoop` (4 rows) and `TestRetryLoop_StopsBeforeClosing` in `event_retry_test.go` with a fake clock and `BlockUntil`; verify `go test -race -run TestRetryLoop .` fails to compile naming only `InjectorOpts.PublicationRetryInterval`
- [ ] 13.2 GREEN: start one ticker goroutine from the ready path next to `startScheduling`, stop it and wait for its batch next to `stopScheduling`, report tick failures to `HookEventError` (D10); verify the tests pass under `-race` and goleak reports no leaks
- [ ] 13.3 REFACTOR: Extract Type `retryLoop` with start and stop methods; verify `make test` and `make lint` pass

## 14. Leases for live deliveries (R: "Live deliveries hold a lease")

- [ ] 14.1 RED: add `TestLease_LiveDelivery` inside `testWithTimeout`; verify `go test -race -run TestLease_LiveDelivery .` fails to compile naming only `InjectorOpts.PublicationLease`, or with "mail" entered twice
- [ ] 14.2 GREEN: add `PublicationLease` (default 5m) and a per-root owner ID, pass both to `Append` and `Claim`, and make the memory store's `Claim` skip unexpired leases (D5, D14); verify the test passes and `make test` is green
- [ ] 14.3 REFACTOR: none needed: lease data flows through the existing request structs; verify `make test` and `make lint` pass

## 15. Attempts and dead letters (R: "Attempts and dead letters")

- [ ] 15.1 RED: add `TestDeadLetter_Attempts` (3 rows), `TestDeadLetter_CrashCounts`, `TestDeadLetter_Resubmit` in `event_deadletter_test.go`, and the `publicationtest` subtests "claim counts attempts", "claim skips dead", "dead letters resubmit"; verify `go test -race -run TestDeadLetter_ . ./publicationtest` fails to compile naming only `PublicationDeadLetters`, `InjectorOpts.PublicationMaxAttempts`
- [ ] 15.2 GREEN: count attempts in `Claim`, mark publications dead in `Fail` at `PublicationMaxAttempts` (default 10), add `PublicationDeadLetters` to the memory store (D8); verify the tests pass and `make test` is green
- [ ] 15.3 REFACTOR: Extract Function `failInfo` computing next attempt and dead; verify `make test` and `make lint` pass

## 16. Retention (R: "Retention of completed publications")

- [ ] 16.1 RED: add `TestRetention` (3 rows) and `TestMemoryStore_PurgeInclusive` in `event_retention_test.go`, and the `publicationtest` subtest "purge is inclusive" with its exclusive-bound `TestTestStore` row; verify `go test -race -run 'TestRetention|TestMemoryStore_PurgeInclusive' . ./publicationtest` fails to compile naming only `PublicationPurger`, `InjectorOpts.PublicationRetention`
- [ ] 16.2 GREEN: complete with `Delete: true` when retention is 0; otherwise keep rows and run a purge ticker from Start every `min(retention, 1h)` with an inclusive bound, stopped with the retry loop (D10); verify the tests pass under `-race` and goleak reports no leaks
- [ ] 16.3 REFACTOR: share the ticker lifecycle of `retryLoop` (Extract Superclass-style shared helper `rootTicker`); verify `make test` and `make lint` pass

## 17. Codec and event type registry (R: "Codec and event type registry")

- [ ] 17.1 RED: add `TestRegisterEventType` (6 rows), `TestRegisterEventType_OldNameDecodes`, `TestCodec_Choice` (2 rows) and `FuzzJSONCodec_RoundTrip` in `event_codec_test.go`; verify `go test -race -run 'TestRegisterEventType|TestCodec_Choice' .` fails to compile naming only `RegisterEventType`, `EventVersion`, `ErrEventTypeConflict`, `ErrInvalidEventType`
- [ ] 17.2 GREEN: add the registry with aliases and versions, `RegisterEventType`, `EventTypeOption`, `EventVersion`, content-type checks at redelivery (D11); verify the tests pass, `go test -run x -fuzz FuzzJSONCodec_RoundTrip -fuzztime 30s .` finds nothing, and `make test` is green
- [ ] 17.3 REFACTOR: Extract Type `eventTypeRegistry`; verify `make test` and `make lint` pass
- [ ] 17.4 Add `ExampleRegisterEventType`; verify `go test -run ExampleRegisterEventType .` passes

## 18. Headers and publication context (R: "Headers propagate across redelivery")

- [ ] 18.1 RED: add `TestHeaders_Redelivery` and `TestPublicationFromContext_Outside` (3 rows) in `event_headers_test.go`; verify `go test -race -run 'TestHeaders_|TestPublicationFromContext_' .` fails to compile naming only `PublicationPropagator`, `InjectorOpts.PublicationPropagator`, `PublicationFromContext`
- [ ] 18.2 GREEN: add the propagator option, `Inject` at recording and `Extract` at live delivery and redelivery, and `PublicationFromContext` with an unexported key (D13); verify the tests pass and `make test` is green
- [ ] 18.3 REFACTOR: none needed: two call sites; verify `make test` and `make lint` pass

## 19. Publication IDs (R: "Publication IDs")

- [ ] 19.1 RED: add `TestPublicationID` (2 rows) in `event_id_test.go`; verify `go test -race -run TestPublicationID .` fails on the UUIDv7 format or ordering assertion
- [ ] 19.2 GREEN: make the default generator a full UUIDv7 (millisecond timestamp from the root clock, version and variant bits, `crypto/rand`), keeping the custom generator path (D13); verify the test passes and `make test` is green
- [ ] 19.3 REFACTOR: Extract Function `newUUIDv7`; verify `make test` and `make lint` pass

## 20. Store resolution and the in-memory store (R: "Store resolution")

- [ ] 20.1 RED: add `TestStoreResolution` (6 rows) and `TestStoreResolution_InMemory` (2 rows) in `event_store_test.go`; verify `go test -race -run TestStoreResolution .` fails to compile naming only `InjectorOpts.RequireDurableStore`, `ErrDurableStoreRequired`, or on the service rows
- [ ] 20.2 GREEN: resolve the store once (opts, named service with `AsDefault` yielding, in-memory) without registering a service, fail on a failing provider, log the in-memory announcement once with the `log` fallback, and enforce `RequireDurableStore` (D12); verify the tests pass and `make test` is green
- [ ] 20.3 REFACTOR: use `resolveOnce[T]` from 4.3 for the store (Inline duplicated guard code); verify `make test` and `make lint` pass
- [ ] 20.4 Add `ExampleNewMemoryPublicationStore` and `ExampleListenerDurable` showing the default and an override; verify `go test -run 'Example(NewMemoryPublicationStore|ListenerDurable)' .` passes

## 21. Option validation (R: "Publication options are validated")

- [ ] 21.1 RED: add `TestPublicationOptions` (7 rows) in `event_publication_options_test.go`; verify `go test -race -run TestPublicationOptions .` fails on the negative-duration panic rows
- [ ] 21.2 GREEN: apply the 0-means-default rules and panic for negative durations in `New`/`NewWithOpts`; verify the test passes and `make test` is green
- [ ] 21.3 REFACTOR: Extract Function `validatePublicationOpts` next to the existing options defaults; verify `make test` and `make lint` pass

## 22. Concurrency and clones (R: "Concurrent durable publishing", "Clone resolves its own store")

- [ ] 22.1 RED: add `TestDurable_Concurrent`, `TestClone_Store` (2 rows) and `TestClone_SharedStoreAcrossRoots`, each concurrent test inside `testWithTimeout`; verify `go test -race -run 'TestDurable_Concurrent|TestClone_' .` fails (race report, duplicate delivery, or shared resolved store)
- [ ] 22.2 GREEN: fix the races found, give clones a fresh owner ID and their own resolution, retry loop and purge timer, and copy the type registry (D14); verify the tests pass under `-race` and goleak reports no leaks
- [ ] 22.3 REFACTOR: Move Function for publication-state copying next to the event-state copy from `add-eventing`; verify `make test` and `make lint` pass

## 23. Conformance suite completion (R: "Store conformance suite")

- [ ] 23.1 RED: add the remaining `publicationtest` subtests ("claim skips leased", "claim skips not due") and the `TestTestStore` row "a store whose Claim also returns leased publications"; verify `go test -race ./publicationtest` fails on that row (the broken store passes)
- [ ] 23.2 GREEN: implement the subtests against the contract in D5, skipping optional-interface subtests for stores that lack them; verify `go test -race ./publicationtest` passes and the memory store passes every subtest
- [ ] 23.3 REFACTOR: Extract Function `fixturePublications` shared by the subtests; verify `make test` and `make lint` pass
- [ ] 23.4 Add godoc on `TestStore` with a usage example for store authors; verify `go doc ./publicationtest TestStore` shows it

## 24. Guide and benchmark

- [ ] 24.1 Add `BenchmarkPublish/durable-{1,10}-listeners` in `bench/` as a regression baseline; verify `go test -run x -bench 'BenchmarkPublish/durable' ./bench` runs
- [ ] 24.2 Add a "Durable events" guide under `docs/` (at-least-once semantics, idempotency with `PublicationFromContext`, transaction binding, phases, writing a store with `publicationtest`, dead letters, the in-memory store's limits) and an upgrading note stating no breaks; verify the docs pages render with the repo's docs tooling, or `npx markdownlint` when none applies

## 25. Integration checks

- [ ] 25.1 Reconcile the design's Test Strategy with the tests: every `#### Scenario:` in both specs has its test and every Examples row its subtest; verify with `go test -race -run 'Test(Tx|Durable|Lease|Redelivery|RetryLoop|ExponentialRetry|DeadLetter|Retention|MemoryStore|RegisterEventType|Codec|Headers|PublicationFromContext|PublicationID|StoreResolution|PublicationOptions|Clone_Store|Clone_Shared)' -v . ./publicationtest | grep -c '^=== RUN'` against the tables, fixing any gap
- [ ] 25.2 Run the full suite and every workspace module; verify `make test`, `make lint`, and `go build ./... && go test ./...` in each `./http/*` and `./examples/*` module pass
- [ ] 25.3 Validate the change; verify `openspec validate add-durable-events --strict` passes

## Workflow follow-up

- Archive the change once `make test`, `make lint` and `openspec validate add-durable-events --strict` pass and every scenario maps to a passing test.
- Follow-up change: the first crash-safe store module (decides the design's open question about one `database/sql` module vs. one per database), validated with `publicationtest.TestStore`.
- Possible follow-up: lease renewal for long deliveries as an optional `PublicationLeaseRenewer` interface (design open question).
