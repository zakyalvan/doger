# Tasks

Specs: `specs/sql-transactions` (module `events/sqltx`), `specs/sql-event-schema`, `specs/sql-event-transport` (module `events/sql`). Test names and files follow the design's Test Strategy; decisions are D1-D8. Group 1 runs in `events/sqltx`; later groups run in `events/sql` unless noted. Prerequisite: `add-event-transport` is archived and on `main`. Integration commands need `make test-sql` (Docker) or the `DOGER_SQL_*_DSN` variables; set `DOGER_SQL_REQUIRE=1` so a missing database fails instead of skipping. Every RED must fail for the stated reason before its GREEN starts.

## 1. Shared transaction module `events/sqltx` (spec sql-transactions: all requirements)

- [ ] 1.1 Create module `github.com/kartaladev/doger/events/sqltx` (Go 1.27, package `sqltx`, `doc.go`, `main_test.go` with goleak, `fakedriver_test.go` recording Begin/Commit/Rollback and able to fail them), add it to `go.work`; verify `go build ./...` in the module and `make modules` lists `events/sqltx`
- [ ] 1.2 RED: add `TestBeginTx` (6 rows), `TestTx_Callbacks` (6 rows), `TestTx_LateCallback`, `TestTx_ConcurrentCallbacks`, `TestInTx` (4 rows), `TestInTx_Arguments` (2 rows), `TestBindTx` (3 rows), `TestBindTx_Panics` (2 rows), `TestFromAndSource` (11 rows), `TestFromDB` (6 rows), `TestDeferredListeners` (2 rows); verify `go test -race ./...` in `events/sqltx` fails to compile naming only `Tx`, `BeginTx`, `InTx`, `BindTx`, `From`, `FromDB`, `Source`, `ErrNilDB`, `ErrForeignTransaction`
- [ ] 1.3 GREEN: implement design D6 (wrapper with mutex-guarded callbacks and `atomic.Bool` ended flag, panic recovery logged via `slog.Default()`, context key, `From`, `FromDB` with `doger.ErrNoTransaction` and `ErrForeignTransaction`, comparable zero-size `Source()` with `Lookup`/`Detach`, `BeginTx`, `InTx` with rollback and re-panic, `BindTx` with `doger: sqltx: ` panics) and `var _ doger.Tx = (*Tx)(nil)`; verify the tests pass with `-race` and goleak is clean
- [ ] 1.4 REFACTOR: Extract Function `runCallbacks`; Extract Function `end(commit bool)` shared by `Commit` and `Rollback`; verify `go test -race ./...` in the module and `make lint-all` pass
- [ ] 1.5 Add `ExampleInTx` and `ExampleBindTx` (runnable with `// Output:` on the fake driver), godoc, and a README stating that `events/sql`, the watermill `wmsql` module and the msgin adapter share it; verify `go test -run 'ExampleInTx|ExampleBindTx' ./...` in `events/sqltx` passes

## 2. Transport module scaffolding and test infrastructure

- [ ] 2.1 Create module `github.com/kartaladev/doger/events/sql` (Go 1.27, package `eventsql`, `doc.go`, `main_test.go` with goleak) requiring `events/sqltx` (workspace-resolved), add it to `go.work`, and confirm `scripts/release-steps.sh modules` lists it; verify `go build ./...` and `make modules` include `events/sql`
- [ ] 2.2 Add `compose.yaml` (postgres:17, postgres:13, mysql:8.4, mysql:8.0), `make test-sql`, the DSN/`DOGER_SQL_REQUIRE` test helper that creates and drops a random schema or database per test, and the `sql-integration` CI job; verify `make test-sql` runs an empty integration test against all four engines locally and in CI
- [ ] 2.3 Add `fakedriver_test.go`, a minimal `database/sql/driver` that records statements and connection attempts; verify `go test -race ./...` passes

## 3. Constructor and options (spec sql-event-schema: "New validates its arguments", "Table names are validated")

- [ ] 3.1 RED: add `TestNew_Arguments` (13 rows) and `TestNew_TablePrefix` (9 rows) in `options_test.go`; verify `go test -race -run 'TestNew_' .` fails to compile naming only `New`, `Option`, the option functions, `Dialect`, `ErrNilDB`, `ErrNilDialect`, `ErrInvalidOption`, `ErrInvalidTableName`, `postgres.Dialect`, `mysql.Dialect`
- [ ] 3.2 GREEN: add `New`, the options with D5 defaults and validation, prefix validation (regex, 40 bytes), the `Dialect`/`Tables`/`Statements` SPI and stub `postgres`/`mysql` dialects with quoting; verify the tests pass and `go test -race ./...` is green
- [ ] 3.3 REFACTOR: Extract Function `validateIdent`; Introduce Parameter Object `config`; verify `go test -race ./...` and `make lint-all` pass
- [ ] 3.4 Add `ExampleNew` and godoc on `New` and every option stating its default; verify `go vet ./...` and `go test -run ExampleNew .` pass

## 4. Schema (spec sql-event-schema: "Reference DDL for migrations", "EnsureSchema creates missing tables")

- [ ] 4.1 RED: add `postgres/ddl_test.go: TestDDL`, `mysql/ddl_test.go: TestDDL` (3 rows across both), `TestEnsureSchema` (6 rows), `TestEnsureSchema_Concurrent`; verify the unit tests fail to compile naming only `DDL`, and the integration tests fail naming only `EnsureSchema`, `CheckSchema`, `ErrSchemaMissing`
- [ ] 4.2 GREEN: write the D1 tables and index for both dialects, `EnsureSchema` under the advisory/`GET_LOCK` lock (D7), and `CheckSchema` via `information_schema`; verify the tests pass on all four engines
- [ ] 4.3 REFACTOR: Extract Function `tableNames(prefix)` shared by DDL and statements; verify `go test -race ./...` and `make lint-all` pass
- [ ] 4.4 Add `ExampleTransport_EnsureSchema` and README "Schema" section (migration-first, DDL review); verify `go test -run ExampleTransport_EnsureSchema .` compiles and passes when a DSN is set

## 5. Send and fan-out (spec sql-event-transport: "Send writes one delivery per registered group")

- [ ] 5.1 RED: add `TestSend_DeliveriesPerGroup` (5 rows) and `TestSend_Failures` (3 rows) in `integration_send_test.go`, seeding groups directly with SQL; verify they fail naming only `Transport.Send`, `ErrPayloadTooLarge`
- [ ] 5.2 GREEN: implement `Send` as one transaction with the event insert, the `INSERT ... SELECT` fan-out, and the no-group delete per dialect (D1), plus the payload limit returning a permanent error; verify the tests pass on all four engines
- [ ] 5.3 REFACTOR: Extract Function `writeEvent(ctx, q querier, env)` to share with `SendTx`; verify `go test -race ./...` and `make lint-all` pass

## 6. Subscribe, claim and acknowledge (spec sql-event-transport: "Groups start at the latest event and keep waiting events", "Members of a group compete")

- [ ] 6.1 RED: add `TestGroups_History` (3 rows), `TestGroups_RegistrationRace`, `TestCompete_TwoInstances`, `TestCompete_Workers` (3 rows); verify they fail naming only `Transport.Subscribe`
- [ ] 6.2 GREEN: implement group upsert, the poller and worker goroutines (D5), the `SKIP LOCKED` claim per dialect with lease token and DB time (D3), ack by token, and a minimal stop; verify the tests pass on all four engines with `-race`
- [ ] 6.3 REFACTOR: Extract Function `claimBatch`; Split Phase between polling and dispatching to workers; verify `go test -race ./...` and `make lint-all` pass

## 7. Leases (spec sql-event-transport: "Leases recover crashed and slow handlers")

- [ ] 7.1 RED: add `TestLease` (3 rows) and `TestLease_AttemptInContext`; verify they fail on the handler-deadline and late-ack rows and to compile naming only `AttemptFromContext`, `ErrLeaseLost`
- [ ] 7.2 GREEN: set the handler deadline from the claim's lease end minus the margin, log `ErrLeaseLost` when an ack affects 0 rows, and add `AttemptFromContext`; verify the tests pass on all four engines
- [ ] 7.3 REFACTOR: none expected beyond Extract Function `handlerContext`; verify `go test -race ./...` and `make lint-all` pass

## 8. Retries and dead letters (spec sql-event-transport: "Retries, backoff and dead letters", "Dead letters can be inspected and resubmitted")

- [ ] 8.1 RED: add `TestExponentialBackoff_Default` (4 rows), `TestRetry` (5 rows), `TestAdmin` (7 rows); verify they fail to compile naming only `Backoff`, `ExponentialBackoff`, `FixedBackoff`, `ListDeadLetters`, `DeadLetter`, `Resubmit`, `DropGroup`
- [ ] 8.2 GREEN: implement nack with backoff, dead-lettering on max attempts and permanent errors, the backoff policies, and the admin methods (D4); verify the tests pass on all four engines
- [ ] 8.3 REFACTOR: Replace Conditional with a `settle(outcome)` function covering ack, nack and dead; verify `go test -race ./...` and `make lint-all` pass
- [ ] 8.4 Add README "Operations" section (dead letters, resubmitting, dropping a group, stale-group warning); verify links in the README resolve

## 9. Stop, ephemeral groups and retention (spec sql-event-transport: "Stopping a subscription releases its claims", "Ephemeral groups are cleaned up", "Delivered rows are removed", "The transport honors the doger transport contract")

- [ ] 9.1 RED: add `TestStop` (2 rows), `TestStop_NoGoroutine`, `TestGroups_Ephemeral` (4 rows), `TestRetention` (3 rows), and `TestConformance` (4 engines) running `eventtransporttest.Run` without `WithTx`; verify they fail on lease release, ephemeral removal and the suite's `StopWaitsHandlers` and `NewGroupAtLatest` subtests
- [ ] 9.2 GREEN: release unstarted claims on stop, wait for workers bounded by the stop context, delete ephemeral groups on stop, and add heartbeat, reaping and retention to the poller's periodic step (D5); verify the tests pass on all four engines and goleak is clean
- [ ] 9.3 REFACTOR: Extract Function `maintenance(ctx)` for the periodic step; verify `go test -race ./...` and `make lint-all` pass

## 10. Outbox (spec sql-event-transport: "SendTx writes in the bound transaction", "The transport honors the doger transport contract")

- [ ] 10.1 RED: add `TestSendTx` (6 rows), `TestOutbox_EndToEnd` (2 rows), `TestTxSource_SameSourceTwice`, and enable `eventtransporttest.WithTx` in `TestConformance` with a harness using `sqltx.BeginTx` on the transport's `*sql.DB`; verify they fail to compile naming only `Transport.SendTx`, `Transport.TxSource`
- [ ] 10.2 GREEN: implement `SendTx` as `sqltx.FromDB(ctx, t.db)` (its error returned unchanged) then `writeEvent` on the Tx's `SQL()`, `TxSource()` returning `sqltx.Source()`, and `var _ doger.TxEventSender = (*Transport)(nil)`; verify the tests pass on all four engines
- [ ] 10.3 REFACTOR: none needed if `SendTx` is `FromDB` plus `writeEvent`; verify `go test -race ./...` and `make lint-all` pass
- [ ] 10.4 Add `ExampleTransport_SendTx` (with `sqltx.InTx`) and the README "Outbox" section with the `sqltx.BindTx` misuse warning; verify `go test -run ExampleTransport_SendTx .` compiles and passes when a DSN is set

## 11. Integration and docs

- [ ] 11.1 Add the SQL transport section to the "Event transports" guide in `docs/` and a module README quick start (DDL, `New`, `InjectorOpts.EventTransport`, `sqltx.InTx`); verify the docs build per `docs/` instructions
- [ ] 11.2 Run `make test-all`, `make lint-all`, and `make test-sql`; verify all pass and the release dry run (`scripts/release-steps.sh` on a copy, as in `test.yml`) drops the pgx and mysql test requirements from `events/sql/go.mod` and pins `events/sqltx` to the release version

## Workflow follow-up

- Archive after review; tag `events/sqltx/v0.x.0` and `events/sql/v0.x.0` with the next doger release, before the watermill and msgin adapters that import `sqltx`.
