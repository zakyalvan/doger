# Tasks

Spec: `specs/msgin-event-transport/spec.md`. Test names and files follow the design's Test Strategy; decisions are D1-D10. Prerequisites: `add-event-transport` is archived (it provides `eventtransporttest.WithoutDeliverAll()`), and the `events/sqltx` module from `add-sql-event-transport` is merged. Unit commands run in `events/msgin`; database commands run in `events/msgin/internal/conformance` after `make msgin-deps`. Every RED must fail for the stated reason before its GREEN starts.

## 1. Module scaffolding and the msgin pin (no behavior)

- [ ] 1.1 Create module `github.com/kartaladev/doger/events/msgin` (Go 1.27) with `MSGIN_COMMIT` = `0c820e18975de76f9cf8e4a8929cad78ca665750`, a `require` of msgin's root module at that commit's pseudo-version, doger and `events/sqltx` via `replace ../..` and `replace ../sqltx` as in `scheduler/gocron`, package godoc and README carrying the experimental notice; verify `go build ./...` in `events/msgin` passes and the root `go.work` is unchanged
- [ ] 1.2 Create test module `events/msgin/internal/conformance` with `replace` directives into gitignored `_deps/msgin`, `make msgin-deps` that clones msgin and checks out `MSGIN_COMMIT`, `modernc.org/sqlite` and `pgx/v5/stdlib` test dependencies, and a `TestMain` with goleak; verify `make msgin-deps && go test ./...` in that module passes with zero tests
- [ ] 1.3 Add `TestPinConsistency` in `events/msgin` reading `MSGIN_COMMIT` and `go.mod`; verify it passes, and fails when `MSGIN_COMMIT` is edited (not committed)
- [ ] 1.4 Add `.github/workflows/events-msgin.yml` (paths `events/msgin/**`, weekly schedule, SQLite job and Postgres 16 service job, non-required) and exclude `events/msgin` from the release configuration; verify with `actionlint` and a dry run of the release tooling listing no `events/msgin` package

## 2. Construction and options (spec: "Constructing the transport")

- [ ] 2.1 RED: add `TestNew` (8 rows) and `TestTransport_Interfaces` in `transport_test.go`; verify `go test -race -run 'TestNew|TestTransport_Interfaces' .` fails to compile naming only `New`, `SQLite`, `Dialect`, `Transport`, the `With*` options, `ErrNilDB`, `ErrInvalidOption`
- [ ] 2.2 GREEN: add `Dialect` with `Postgres`/`MySQL`/`SQLite`, `New` with eager validation (D2) and the default config (D5), sentinels with the `doger: msgin: ` message prefix; add `var _ doger.TxEventSender = (*Transport)(nil)`; verify the tests pass
- [ ] 2.3 REFACTOR: Extract Function `validateConfig`; Introduce Parameter Object `config`; verify `go test -race ./...` and `golangci-lint run` pass in `events/msgin`

## 3. Table names (spec: "Table names")

- [ ] 3.1 RED: add `TestTableNames` (3 rows) and `TestTableNames_Distinct` in `schema_test.go`; verify the run fails to compile naming only `QueueTable`, `DeadLetterTable`
- [ ] 3.2 GREEN: hash-based names with the prefix (D3); verify the tests pass
- [ ] 3.3 REFACTOR: none needed: two one-line methods over one helper; verify `go test -race ./...` and lint pass

## 4. Schema checks and creation (spec: "Schema is checked, created only on request"; "Table names" conflict scenario)

- [ ] 4.1 RED: add `TestSchema` (2 rows), `TestSchema_Preprovisioned`, `TestSchema_EnsureConcurrent`, `TestSchema_GroupConflict` in `internal/conformance/schema_test.go`; verify the run fails to compile naming only `WithEnsureSchema`, `RegistryDDL`, `ErrSchemaNotReady`, `ErrGroupConflict`, `Subscribe`
- [ ] 4.2 GREEN: registry SQL per dialect flavor, `RegistryDDL`, readiness checks with per-table caching, opt-in ensure through msgin's `EnsureSchema`, group-conflict check on registration, and a `Subscribe` that registers but does not yet consume (D9); verify the tests pass on SQLite
- [ ] 4.3 REFACTOR: Extract Function `ensureTables`; Separate Query from Modifier between readiness checks and creation; verify both modules' tests and lint pass

## 5. Envelope mapping (spec: "Envelopes round-trip through msgin messages")

- [ ] 5.1 RED: add `TestEnvelopeMapping` (field-by-field encode and decode, missing and malformed `doger.*` headers) in `envelope_test.go` and `TestEnvelope_RoundTrip` (3 rows) in the conformance module; verify the runs fail on the missing mapping functions and on the round-trip assertions
- [ ] 5.2 GREEN: implement D4 with `msgin.WithID`, `BytesPayloadCodec`, namespaced headers and `msgin.Permanent` on decode failure; verify the tests pass
- [ ] 5.3 REFACTOR: Extract Function per direction (`toMessage`, `fromMessage`); verify tests and lint pass

## 6. Fan-out send (spec: "Send fans out to registered groups")

- [ ] 6.1 RED: add `TestFanout` (3 rows), `TestFanout_NewGroupAtLatest`, `TestFanout_DurableGroup`, `TestSend_ClosedDB` in `internal/conformance/fanout_test.go` (handler counts read from queue tables until group 8 adds consumers); verify the run fails on the row-count assertions
- [ ] 6.2 GREEN: `Send` reads the registry and inserts one copy per group in one private transaction through cached shared-transaction Outbounds (D3); verify the tests pass on SQLite
- [ ] 6.3 REFACTOR: Extract Function `fanOut` shared with `SendTx`; Encapsulate the Outbound cache; verify tests and lint pass

## 7. Transaction binding and outbox (spec: "Transaction binding for doger", "Outbox writes in the caller's transaction")

- [ ] 7.1 RED: add `TestSendTx` (2 rows), `TestSendTx_Unusable` (2 rows), `TestSendTx_BindTx` and `TestTxSource_Sqltx` (2 rows) in the conformance module, using `sqltx.BeginTx`, `sqltx.InTx` and `sqltx.BindTx`; verify the run fails to compile naming only `SendTx`, `TxSource`
- [ ] 7.2 GREEN: implement D7: `TxSource()` returns `sqltx.Source()`, the msgin `TransactionResolver` uses `sqltx.FromDB(ctx, db)` (no transaction → nil, so msgin's strict mode applies; `sqltx.ErrForeignTransaction` passes through), `Send` binds its private transaction with `sqltx.BindTx`, and `ErrNoSharedTransaction` maps to `doger.ErrNoTransaction`; verify the tests pass
- [ ] 7.3 REFACTOR: Extract Function `resolveQuerier` shared by `Send` and `SendTx`; verify tests and lint pass

## 8. Consumption and settlement (spec: "DeliverOnce members compete", "Handler outcomes map to msgin settlement", "DeliverAll is not supported")

- [ ] 8.1 RED: add `TestSubscribe_DeliverAll` (2 rows) in `transport_test.go`, and `TestSettlement` (5 rows), `TestSettlement_Undecodable`, `TestSettlement_CustomDLQ`, `TestDeliverOnce_Compete` (Postgres) in the conformance module; switch group 6 tests to handler counts; verify the runs fail on handler-call assertions and the DeliverAll error
- [ ] 8.2 GREEN: start a msgin consumer per subscription with the D5 options, the plain dead-letter Outbound, permanent-error mapping and panic recovery; reject `DeliverAll` first (D6); verify the tests pass on SQLite and, with `DOGER_MSGIN_POSTGRES_DSN` set, on Postgres
- [ ] 8.3 REFACTOR: Extract Function `newConsumer`; Replace Nested Conditional with Guard Clauses in the settlement mapping; verify tests and lint pass

## 9. Stopping and removing groups (spec: "Subscriptions stop cleanly")

- [ ] 9.1 RED: add `TestStop` (3 rows) and `TestRemoveGroup` in `internal/conformance/stop_test.go`, wrapped with a timeout helper; verify the run fails on the second-stop, deadline or registry assertions
- [ ] 9.2 GREEN: idempotent stop that cancels and waits for `Run` or the stop context; `RemoveGroup` deletes the registry row and evicts cached state; verify the tests pass with goleak clean
- [ ] 9.3 REFACTOR: none needed if stop is a `sync.Once` around cancel-and-wait; verify tests and lint pass

## 10. Idempotent consumer (spec: "Opt-in idempotent consumer")

- [ ] 10.1 RED: add `TestIdempotent` (3 rows) and `TestIdempotent_SharedTx` in `internal/conformance/inbox_test.go`; verify the run fails to compile naming only `WithIdempotentConsumer`
- [ ] 10.2 GREEN: wrap the handler per D8 with `InboxDeduper.MarkProcessed` inside `sqltx.InTx(ctx, businessDB, fn)`; nil arguments fail `New` with `ErrInvalidOption`; verify the tests pass
- [ ] 10.3 REFACTOR: Extract Function `withInbox` as a handler decorator; verify tests and lint pass

## 11. Concurrency and doger integration (spec: "Concurrent use"; "Outbox writes in the caller's transaction" doger scenario)

- [ ] 11.1 RED: add `TestConcurrentUse` and `TestOutbox_WithDoger` (2 rows, two doger roots, type registered with `EventExternalized()`) in the conformance module; verify they fail (the doger test on its outcome assertions, or pass on arrival — then record it and show the assertion can fail by disabling `TxSource`)
- [ ] 11.2 GREEN: fix any race or ordering issue the tests expose; verify both pass under `-race` on SQLite and Postgres
- [ ] 11.3 REFACTOR: none expected; verify tests and lint pass

## 12. Conformance (spec: "Conformance with doger's transport contract")

- [ ] 12.1 RED: add `TestConformance` (SQLite and Postgres rows) calling `eventtransporttest.Run(t, factory, eventtransporttest.WithoutDeliverAll(), eventtransporttest.WithTx(harness))`, where `WithoutDeliverAll()` (from `add-event-transport`) skips the `DeliverAll` subtests and asserts `Subscribe` with `DeliverAll` returns `doger.ErrSubscriptionNotSupported`, and the harness begins transactions with `sqltx.BeginTx`; verify it fails only where the transport diverges from the contract (or passes on arrival, recorded)
- [ ] 12.2 GREEN: fix every divergence the suite reports; verify `go test -race ./...` passes in the conformance module on both databases
- [ ] 12.3 REFACTOR: none expected; verify tests and lint pass

## 13. Documentation

- [ ] 13.1 Write the README (experimental notice, pin, dialect wrappers, schema steps with `RegistryDDL` and msgin `DDL`, an outbox example with `sqltx.InTx` and `EventExternalized()`, `RemoveGroup` and the abandoned-group query, the `replace` callers need for msgin's dialect modules) and `Example_outbox` in `events/msgin`; verify `go test -run Example ./...` passes in `events/msgin`
- [ ] 13.2 Confirm the CI workflow runs green on a branch for both jobs and that doger's `make test-all` and `make lint-all` (workspace modules, which exclude `events/msgin`) still pass; verify by the workflow run results and both make targets

## Workflow follow-up

- Apply `add-event-transport` and the `events/sqltx` part of `add-sql-event-transport` first.
- A later change promotes this module from experimental once msgin tags v1.
