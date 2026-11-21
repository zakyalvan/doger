# Tasks

Specs: `specs/event-types`, `specs/event-externalization`, `specs/event-transport`, `specs/transactional-events`. Test names and files follow the design's Test Strategy; decisions are D1-D11. Commands run from the repo root on package `.` unless noted. Prerequisite: `add-eventing` is archived and its code is on `main`. Every RED must fail for the stated reason before its GREEN starts.

## 1. Transaction phases (spec transactional-events: "Listeners run in their transaction phase", "Deferred listeners run detached")

- [ ] 1.1 RED: add fakes `fakeTx`/`fakeTxSource` in `event_transport_fakes_test.go` and tests `TestTxPhase` (9 rows), `TestTxPhase_Order`, `TestTx_DetachedContext` in `event_tx_test.go`; verify `go test -race -run 'TestTxPhase|TestTx_DetachedContext' .` fails to compile naming only `Tx`, `TxSource`, `TxPhase*`, `ListenerTxPhase`, `InjectorOpts.TxSource`
- [ ] 1.2 GREEN: add `Tx`, `TxSource`, `TxPhase` constants, `ListenerTxPhase`, `InjectorOpts.TxSource` (copied by `copy()`), one `Lookup` per publish when the plan has deferred listeners, and OnCommit/OnRollback closures running with `Detach(ctx)` (design D1-D2 of add-durable-events, carried over); verify the tests pass and `make test` is green
- [ ] 1.3 REFACTOR: Extract Function `deferListeners`; Split Phase between plan partitioning and callback registration; verify `make test` and `make lint` pass

## 2. Deferred errors, fallback and options (spec transactional-events: "Deferred listener errors go to HookEventError", "Fallback when no transaction is bound", "Transaction options are validated")

- [ ] 2.1 RED: add `TestTx_DeferredFailures` (2 rows), `TestTx_Fallback` (5 rows), `TestTxOptions` (3 rows), `TestTxEnums_String` (7 rows); verify `go test -race -run 'TestTx_(DeferredFailures|Fallback)|TestTxOptions|TestTxEnums_String' .` fails to compile naming only `ListenerTxFallback`, `TxFallback*`
- [ ] 2.2 GREEN: route deferred errors and panics to `HookEventError` as `*ListenerError`, add `TxFallbackMode`, `ListenerTxFallback`, validation panics and `String()` methods; verify the tests pass and `make test` is green
- [ ] 2.3 REFACTOR: Extract Function `runDeferred` shared by commit and rollback closures; verify `make test` and `make lint` pass
- [ ] 2.4 Add `ExampleListenerTxPhase` and godoc on `Tx`, `TxSource`, `TxPhase`, `TxFallbackMode`; verify `go test -run ExampleListenerTxPhase .` passes

## 3. Event type registry (spec event-types: "RegisterEventType binds a wire name to a type", "Event type options")

- [ ] 3.1 RED: add `TestRegisterEventType` (8 rows), `TestRegisterEventType_NilRoot`, `TestEventTypeOptions` (8 rows), `TestEventTypeOptions_AliasConflict` in `event_types_test.go`; verify `go test -race -run 'TestRegisterEventType|TestEventTypeOptions' .` fails to compile naming only `RegisterEventType`, `EventTypeOption`, `EventVersion`, `EventDestination`, `EventAlias`, `ErrEventTypeConflict`, `ErrInvalidEventType`
- [ ] 3.2 GREEN: add the copy-on-write registry behind an `atomic.Pointer` on the root (D2), the options, interface-type rejection and the two sentinels; verify the tests pass and `make test` is green
- [ ] 3.3 REFACTOR: Extract Function `validateEventTypeOptions`; Introduce Parameter Object `eventTypeInfo`; verify `make test` and `make lint` pass

## 4. Registry concurrency and clones (spec event-types: "Registration is safe while events flow", "Clones copy the event type registry")

- [ ] 4.1 RED: add `TestRegisterEventType_Concurrent` (race) and `TestRegisterEventType_Clone`; verify `go test -race -run 'TestRegisterEventType_(Concurrent|Clone)' .` fails on the clone isolation assertion (the race test may pass on arrival; record that and show it fails with the mutex temporarily removed)
- [ ] 4.2 GREEN: copy the registry pointer's map in `Clone`/`CloneWithOpts`; verify the tests pass and `make test` is green
- [ ] 4.3 REFACTOR: none needed if the clone copy is one call; otherwise Extract Function `cloneEventRegistry`; verify `make test` and `make lint` pass
- [ ] 4.4 Add `ExampleRegisterEventType` and godoc on `RegisterEventType` and its options; verify `go test -run ExampleRegisterEventType .` passes

## 5. Transport SPI and resolution (spec event-transport: "Transport resolution")

- [ ] 5.1 RED: add the recording transport fake and `TestTransportResolution` (6 rows), `TestTransportResolution_Once` in `event_transport_test.go`, externalizing through a minimal `EventExternalize(ExternalizeImmediate)` path; verify `go test -race -run TestTransportResolution .` fails to compile naming only `EventTransport`, `Envelope`, `Subscription`, `EnvelopeHandler`, `InjectorOpts.EventTransport`, `EventExternalize`, `ExternalizeImmediate`, `ExternalizeError`
- [ ] 5.2 GREEN: add the D1 types, `TxEventSender`, once-with-result resolution (`InjectorOpts.EventTransport` > `NameOf[EventTransport]()` service > none), and an immediate externalization that wraps errors in `*ExternalizeError`; verify the tests pass and `make test` is green
- [ ] 5.3 REFACTOR: Extract Function `resolveTransport` mirroring `TxSource` resolution; verify `make test` and `make lint` pass

## 6. Codec, envelope and IDs (spec event-types: "The default codec is JSON", "Envelopes carry identity and metadata")

- [ ] 6.1 RED: add `TestCodec_RoundTrip` (3 rows), `TestCodec_MarshalError`, `TestEnvelope_Fields` (3 rows), `TestEnvelope_DefaultIDs` in `event_envelope_test.go`; verify `go test -race -run 'TestCodec_|TestEnvelope_' .` fails to compile naming only `EventCodec`, `InjectorOpts.EventCodec`, `InjectorOpts.EventIDGenerator`, `ErrInvalidEnvelope`
- [ ] 6.2 GREEN: add the JSON codec, envelope building with the root clock, UUIDv7 generation from `crypto/rand`, and panic recovery into `*ExternalizeError` (D3); verify the tests pass and `make test` is green
- [ ] 6.3 REFACTOR: Extract Function `newUUIDv7`; Extract Function `buildEnvelope`; verify `make test` and `make lint` pass

## 7. Which events leave the process (spec event-externalization: "Only externalized event types leave the process", "No transport keeps externalized events in-process", "Immediate externalization returns the send error")

- [ ] 7.1 RED: add `TestExternalize_WhichTypes` (4 rows), `TestExternalize_ListenerCount` (3 rows), `TestExternalize_NoTransport`, `TestExternalize_Immediate` (3 rows), `TestExternalize_ImmediateContext` in `event_externalize_test.go`; verify `go test -race -run 'TestExternalize_(WhichTypes|ListenerCount|NoTransport|Immediate)' .` fails to compile naming only `EventExternalized`
- [ ] 7.2 GREEN: add `EventExternalized`, the externalization step after synchronous dispatch, the one-time "no EventTransport" log, and context propagation to `Send`; verify the tests pass and `make test` is green
- [ ] 7.3 REFACTOR: Extract Function `externalizeEvent`; Replace Nested Conditional with Guard Clauses in the externalization step; verify `make test` and `make lint` pass

## 8. Strategy precedence, Auto and after-commit (spec event-externalization: "Externalization strategy precedence", "ExternalizeAuto chooses outbox or immediate", "After-commit externalization runs in the commit callback")

- [ ] 8.1 RED: add `TestExternalize_Precedence` (6 rows), `TestExternalize_Auto` (4 rows), `TestExternalize_AfterCommit` (5 rows), `TestExternalize_AfterCommitDetached`; verify `go test -race -run 'TestExternalize_(Precedence|Auto|AfterCommit)' .` fails to compile naming only `ExternalizeStrategy`, `ExternalizeAuto`, `ExternalizeAfterCommit`, `ExternalizeOutbox`, `PublishExternalize`, `InjectorOpts.EventExternalize`
- [ ] 8.2 GREEN: add the strategy enum and the D4 selection, after-commit sends through `OnCommit` with `Detach`, errors to `HookEventError`; verify the tests pass and `make test` is green
- [ ] 8.3 REFACTOR: Replace Conditional with Polymorphism only if the strategy switch exceeds one screen, else Extract Function `chooseStrategy`; verify `make test` and `make lint` pass

## 9. Outbox (spec event-externalization: "Outbox externalization writes in the caller's transaction", "Outbox without a transaction", "Outbox requires a capable transport"; spec transactional-events: "TxSource resolution")

- [ ] 9.1 RED: add the TxEventSender wrapper fake and `TestExternalize_Outbox` (2 rows), `TestExternalize_OutboxError`, `TestExternalize_OutboxFallback` (3 rows), `TestExternalize_OutboxNotSupported` (3 rows), `TestTxSourceResolution` (7 rows), `TestTxSourceResolution_ConflictAtStart`, `TestTxSourceResolution_Once`; verify the run fails to compile naming only `OutboxFallback*`, `InjectorOpts.EventOutboxFallback`, `ErrNoTransaction`, `ErrOutboxNotSupported`, `ErrTxSourceConflict`
- [ ] 9.2 GREEN: call `SendTx` under the outbox strategy, add the fallback, the Start check for outbox support, and TxSource resolution including the transport's own source and the conflict check; verify the tests pass and `make test` is green
- [ ] 9.3 REFACTOR: Extract Function `resolveTxSource`; Consolidate Duplicate Conditional Fragments between Start-time and publish-time outbox checks; verify `make test` and `make lint` pass
- [ ] 9.4 Add `ExamplePublishExternalize` and `ExampleInjectorOpts_eventExternalize` documenting Auto, the overrides and the after-commit caveat; verify `go test -run 'ExamplePublishExternalize|ExampleInjectorOpts_eventExternalize' .` passes

## 10. Externalization edges (spec event-externalization: "Externalization follows in-process dispatch outcome", "PublishLocal and scopes", "Externalization and the container lifecycle", "Externalization options are validated")

- [ ] 10.1 RED: add `TestExternalize_AfterDispatch` (3 rows), `TestExternalize_Scopes` (4 rows), `TestExternalize_Lifecycle` (4 rows), `TestExternalize_Concurrent`, `TestExternalizeOptions_Invalid` (4 rows), `TestExternalizeEnums_String` (8 rows); verify the run fails on the EventContinue join row, the PublishLocal rows and the validation panics
- [ ] 10.2 GREEN: join externalization errors under `EventContinue`, skip externalization for `PublishLocal()`, add validation panics and `String()` methods; verify the tests pass and `make test` is green
- [ ] 10.3 REFACTOR: none expected beyond moving validation next to `EventErrorMode` validation (Move Function); verify `make test` and `make lint` pass

## 10b. Publisher handle (spec event-externalization: "Publisher handle for application ports")

- [ ] 10b.1 RED: add `TestPublisherFor` (5 rows) and `TestPublisherFor_SatisfiesPort` in `event_publisher_test.go`; verify `go test -race -run TestPublisherFor .` fails to compile naming only `PublisherFor`, `Publisher`
- [ ] 10b.2 GREEN: add `Publisher`, `PublisherFor` and `Publish(ctx, events...)` stopping at the first error (D12); verify the tests pass and `make test` is green
- [ ] 10b.3 REFACTOR: none needed: one loop over `Publish`; verify `make test` and `make lint` pass
- [ ] 10b.4 Add `ExamplePublisherFor` showing a use case publishing through its own port inside a unit of work; verify `go test -run ExamplePublisherFor .` passes

## 11. Remote listener declaration (spec event-transport: "ListenerDelivery declares a remote listener", "Remote listener registration rules")

- [ ] 11.1 RED: add `TestRemote_NotLocal` (6 rows), `TestRemote_RegistrationPanics` (6 rows), `TestRemote_RegistrationOK` (5 rows), `TestEventDelivery_String` (4 rows) in `event_remote_test.go`; verify the run fails to compile naming only `EventDelivery`, `DeliverLocal`, `DeliverOnce`, `DeliverAll`, `ListenerDelivery`
- [ ] 11.2 GREEN: add the delivery mode to listener bindings, skip remote bindings in plan building, and the registration panics (D5); verify the tests pass (rows needing inbound delivery may stay failing until group 14; mark them `t.Skip("group 14")` and remove the skip there) and `make test` is green
- [ ] 11.3 REFACTOR: Extract Function `validateRemoteBinding` next to the existing registration checks; verify `make test` and `make lint` pass

## 12. Start validation, groups and destinations (spec event-transport: "Start validates remote listeners", "Group names and instance identity"; spec event-types: "Destination mapping")

- [ ] 12.1 RED: add `TestRemote_StartValidation` (5 rows), `TestRemote_Subscriptions` (5 rows), `TestRemote_DefaultGroup`, `TestDestinationMapper` (3 rows); verify the run fails to compile naming only `ErrNoEventTransport`, `ErrEventTypeNotFound`, `ErrListenerGroupConflict`, `InjectorOpts.EventGroupPrefix`, `InjectorOpts.InstanceID`, `InjectorOpts.EventDestinationMapper`
- [ ] 12.2 GREEN: validate remote listeners at the end of `runStart`, compute groups and the generated instance ID, apply the mapper on both send and subscribe; verify the tests pass and `make test` is green
- [ ] 12.3 REFACTOR: Extract Function `subscriptionFor`; Introduce Parameter Object `remoteListener`; verify `make test` and `make lint` pass

## 13. Subscription lifecycle (spec event-transport: "Subscriptions follow the container lifecycle")

- [ ] 13.1 RED: add `TestRemote_SubscribeOrder`, `TestRemote_SubscribeError`, `TestRemote_ShutdownWaits`, `TestRemote_ShutdownDeadline`, `TestRemote_StopError` in `event_remote_lifecycle_test.go` (hang-prone ones wrapped in `testWithTimeout`); verify the run fails on the recorded-order and stop assertions
- [ ] 13.2 GREEN: open subscriptions after Starters and before the Started hooks, unwind on error, stop them in reverse order after `stopScheduling` and before the Closing hooks with the shutdown context, panics and errors to `HookEventError` (D6); verify the tests pass, goleak is clean and `make test` is green
- [ ] 13.3 REFACTOR: Extract Function `openSubscriptions` / `closeSubscriptions` mirroring `startScheduling` / `stopScheduling`; verify `make test` and `make lint` pass

## 14. Inbound dispatch and permanent errors (spec event-transport: "Inbound dispatch reaches one listener", "Permanent errors stop redelivery", "Inbound deliveries may run concurrently"; spec event-types: "Headers propagate through the envelope")

- [ ] 14.1 RED: add `TestInbound` (5 rows), `TestInbound_LazyOnce`, `TestInbound_Decode` (5 rows), `TestPermanentError` (6 rows), `TestInbound_Concurrent`, `TestPropagator_RoundTrip`, `TestPropagator_None`; remove the group 11 skips; verify the run fails to compile naming only `PermanentError`, `IsPermanentError`, `ErrCodecMismatch`, `EnvelopeFromContext`, `EventPropagator`, `InjectorOpts.EventPropagator`
- [ ] 14.2 GREEN: build one handler per remote listener: decode by name and alias, content-type check, `Extract`, envelope in context, filter, lazy resolve, panic recovery, `*ListenerError` keeping permanent markers (D7, D8); verify the tests pass and `make test` is green
- [ ] 14.3 REFACTOR: Extract Function `decodeEnvelope`; Split Phase between decoding and invoking; verify `make test` and `make lint` pass
- [ ] 14.4 Add `ExampleListenerDelivery` and `ExamplePermanentError` and godoc on the remote listener API; verify `go test -run 'ExampleListenerDelivery|ExamplePermanentError' .` passes

## 15. In-memory transport (spec event-transport: "The in-memory transport")

- [ ] 15.1 RED: add `TestMemoryTransport_Groups`, `TestMemoryTransport_Redelivery` (4 rows), `TestMemoryTransport_NewGroupAtLatest`, `TestMemoryTransport_InvalidOptions` in `event_memory_transport_test.go`; verify the run fails to compile naming only `NewMemoryEventTransport`, `MemoryMaxAttempts`, `MemoryLogger`
- [ ] 15.2 GREEN: implement D9 (groups, round robin, start at latest, one goroutine per subscription, bounded redelivery, stop waits); verify the tests pass with `-race`, goleak is clean and `make test` is green
- [ ] 15.3 REFACTOR: Extract Function `deliverWithRetry`; Encapsulate Collection for group membership; verify `make test` and `make lint` pass
- [ ] 15.4 Add `ExampleNewMemoryEventTransport`; verify `go test -run ExampleNewMemoryEventTransport .` passes

## 16. Conformance suite (spec event-transport: "The conformance suite")

- [ ] 16.1 RED: add `eventtransporttest/run_test.go` with `TestRun_CatchesDefects` (4 rows, broken test transports, recording `testing.TB`) `TestRun_WithTx` and `TestRun_WithoutDeliverAll` (2 rows), plus `TestMemoryTransport_Conformance` in the root package; verify `go test -race ./eventtransporttest/... -run TestRun` fails to compile naming only `eventtransporttest.Run`, `WithTx`, `TxHarness`, `WithoutDeliverAll`
- [ ] 16.2 GREEN: implement `Run` with the subtests named in the spec, standard library only (D10); verify the tests pass and `go list -deps ./eventtransporttest | grep -v -e '^[a-z/]*$' -e kartaladev/doger` prints nothing
- [ ] 16.3 REFACTOR: Extract Function per subtest family; verify `make test` and `make lint` pass

## 17. Clones (spec event-transport: "Clones and transports")

- [ ] 17.1 RED: add `TestRemote_Clone`; verify `go test -race -run TestRemote_Clone .` fails on the subscription count or instance IDs
- [ ] 17.2 GREEN: copy delivery modes with bindings, share an `InjectorOpts` transport, keep subscription state and generated instance IDs per root (D11); verify the test passes and `make test` is green
- [ ] 17.3 REFACTOR: none needed if clone handling is one copy; otherwise Extract Function `cloneTransportState`; verify `make test` and `make lint` pass

## 18. Integration and docs

- [ ] 18.1 Add `BenchmarkPublish_NotExternalized` and compare with the `add-eventing` publish benchmark via `benchstat`; verify the non-externalized path shows no new allocation
- [ ] 18.2 Write the "Event transports" guide in `docs/` (strategies, groups, instance IDs, idempotency with `EnvelopeFromContext(ctx).ID`, writing a transport with `eventtransporttest`, and a hexagonal-architecture section per design D12) and an upgrading note; verify the docs build per `docs/` instructions
- [ ] 18.3 Rewrite `examples/event-driven` to show `DeliverOnce` across two roots with the in-memory transport; verify `go test ./...` in that module passes
- [ ] 18.4 Run `make test-all` and `make lint-all` (every `go.work` module); verify both pass

## Workflow follow-up

- Decide whether to delete `openspec/changes/add-durable-events` (superseded by this change).
- Archive this change before starting `add-sql-event-transport` and `add-watermill-event-transport`.
