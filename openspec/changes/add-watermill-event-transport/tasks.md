# Tasks

Specs: `specs/watermill-event-transport`, `specs/watermill-sql-support`. Test names and files follow the design's Test Strategy; decisions are D1-D7. Prerequisites: `add-event-transport` is archived and on `main`; `events/sqltx` from `add-sql-event-transport` exists before group 10. Commands for `events/watermill` run in that directory, for `wmsql` in `events/watermill/wmsql`. Database tests need `DOGER_TEST_POSTGRES_DSN` and `DOGER_TEST_MYSQL_DSN` (for example `docker run postgres:17` and `mysql:8.4` locally). Every RED must fail for the stated reason before its GREEN starts.

## 1. Module scaffolding (no behavior)

- [ ] 1.1 Create modules `github.com/kartaladev/doger/events/watermill` and `.../events/watermill/wmsql` (Go 1.27, `TestMain` with goleak, `doc.go`), add both to `go.work`, `.github/dependabot.yml` and `make modules`; verify `make test-all` and `make lint-all` pass with the empty packages
- [ ] 1.2 Add PostgreSQL and MySQL service containers to the Linux CI job setting `DOGER_TEST_POSTGRES_DSN` / `DOGER_TEST_MYSQL_DSN`, with SHA-pinned actions; verify the workflow passes `make lint-all` and the existing `release-integrity` checks

## 2. Construction (spec: "Constructing the transport")

- [ ] 2.1 RED: add `TestNew` (7 rows) and `TestNew_Passive` in `events/watermill/transport_test.go` with the fakes from the design; verify `go test -race -run TestNew .` fails to compile naming only `New`, `SubscriberFactory`, `Transport`, `ErrInvalidConfig` and the `With*` options
- [ ] 2.2 GREEN: add `New`, the options with validation and defaults (D2), and `var _ doger.EventTransport = (*Transport)(nil)`; verify the tests pass and `go test -race ./...` is green
- [ ] 2.3 REFACTOR: Extract Function `applyOptions`; verify `go test -race ./...` and `make lint-all` pass
- [ ] 2.4 Add `ExampleNew` and godoc on `New`, `SubscriberFactory` and each option; verify `go test -run ExampleNew .` passes

## 3. Sending (spec: "Send maps an envelope to a watermill message")

- [ ] 3.1 RED: add `TestSend_Fields` (2 rows) and `TestSend_Failures` (3 rows) in `send_test.go`; verify `go test -race -run TestSend .` fails on the metadata assertions
- [ ] 3.2 GREEN: implement the D5 mapping, the context check, `SetContext` and panic recovery; verify the tests pass and `go test -race ./...` is green
- [ ] 3.3 REFACTOR: Extract Function `encodeMessage` and constants for metadata keys; verify `go test -race ./...` and `make lint-all` pass

## 4. Subscribing and decoding (spec: "Subscribe builds one subscriber per subscription", "Received messages map back to envelopes")

- [ ] 4.1 RED: add `TestSubscribe` (4 rows), `TestRoundTrip` (2 rows), `TestForeignMessages` (2 rows) in `subscribe_test.go` with the in-process fake broker; verify `go test -race -run 'TestSubscribe|TestRoundTrip|TestForeignMessages' .` fails on the factory and round-trip assertions
- [ ] 4.2 GREEN: implement `Subscribe` (factory call, subscriber close on error, one consuming goroutine) and `decodeMessage`; acks only, retries come in group 5; verify the tests pass and goleak is clean
- [ ] 4.3 REFACTOR: Extract Function `decodeMessage`; Introduce Parameter Object `subscription` for loop state; verify `go test -race ./...` and `make lint-all` pass

## 5. Acknowledgement, retries and poison (spec: "Acknowledgement, retries and poison handling")

- [ ] 5.1 RED: add `TestAck` (7 rows), `TestAck_RetryDelay` (3 rows, fake clock), `TestAck_PoisonFailure` in `ack_test.go`; verify `go test -race -run TestAck .` fails on the nack counts and poison rows
- [ ] 5.2 GREEN: implement the D6 loop branches: per-UUID attempt map owned by the loop, clock-driven delay selecting on the loop context, give-up to poison topic or ack and log; verify the tests pass and `go test -race ./...` is green
- [ ] 5.3 REFACTOR: Extract Function `giveUp` and `retryLater`; Replace Nested Conditional with Guard Clauses in the loop; verify `go test -race ./...` and `make lint-all` pass
- [ ] 5.4 Add `ExampleWithPoisonTopic` and godoc on retry and poison behavior (including the per-process attempt count); verify `go test -run ExampleWithPoisonTopic .` passes

## 6. Stop and concurrency (spec: "Stop ends the subscription", "The transport is safe for concurrent use")

- [ ] 6.1 RED: add `TestStop_WaitsHandler`, `TestStop_Deadline`, `TestStop_Edges` (3 rows) in `stop_test.go` and `TestConcurrent` in `transport_test.go`, hang-prone ones in `testWithTimeout`; verify `go test -race -run 'TestStop|TestConcurrent' .` fails on the wait and double-stop assertions
- [ ] 6.2 GREEN: implement stop with `sync.Once`, loop cancel, done channel, bounded wait and subscriber `Close` with panic recovery; verify the tests pass, goleak is clean, and `go test -race ./...` is green
- [ ] 6.3 REFACTOR: none expected beyond Extract Function `waitDone`; verify `go test -race ./...` and `make lint-all` pass

## 7. SQL factories and head seeding (spec: "Subscriber factories map subscriptions to consumer groups", "A new group starts at the latest message")

- [ ] 7.1 RED: add `TestFactory_Groups` (2 rows), `TestFactory_NilDB` (2 rows), `TestOffsets_Start` (4 rows), `TestOffsets_ConcurrentSeed` and the DSN helper with `TestDSNRequired` (2 rows) in `wmsql`; verify `go test -race ./...` in `wmsql` fails to compile naming only `PostgresSubscriberFactory`, `MySQLSubscriberFactory`, `WithSubscriberConfig`
- [ ] 7.2 GREEN: implement both factories and the wrapped offsets adapters whose seed query starts new groups at the newest row (D3); verify the tests pass against both databases and `go test -race ./...` is green
- [ ] 7.3 REFACTOR: Extract Function per dialect seed query; Parameterize Function for the shared factory body; verify `go test -race ./...` and `make lint-all` pass
- [ ] 7.4 Add `ExamplePostgresSubscriberFactory` and the guide's backend recipe table (Kafka `OffsetNewest`, Redis `OldestId: "$"`, JetStream `nats.DeliverNew()`, unsupported NATS core and gochannel); verify the example compiles with `go vet ./...`

## 8. Ephemeral cleanup (spec: "Ephemeral groups are removed when they stop")

- [ ] 8.1 RED: add `TestFactory_EphemeralCleanup` (3 rows) and `TestFactory_CleanupError`; verify they fail on the "no longer exists" row
- [ ] 8.2 GREEN: wrap ephemeral subscribers so `Close` deletes the group's offset row; add `WithKeepEphemeralGroups`; verify the tests pass and `go test -race ./...` is green
- [ ] 8.3 REFACTOR: none needed if the wrapper is one type; otherwise Extract Function `deleteGroup`; verify `go test -race ./...` and `make lint-all` pass

## 9. Conformance of the transport (spec: "The transport passes the conformance suite")

- [ ] 9.1 RED: add `TestConformance` (2 rows) in `wmsql/conformance_test.go` calling `eventtransporttest.Run`; verify it runs and any failing subtest is recorded (if all pass on arrival, record that and show a subtest fails with the head seeding temporarily disabled)
- [ ] 9.2 GREEN: fix whatever the suite reports in `events/watermill` or `wmsql`; verify `TestConformance` passes on both databases with `-race`
- [ ] 9.3 REFACTOR: Extract Function `newTestTransport` shared by conformance and integration tests; verify `go test -race ./...` and `make lint-all` pass

## 10. Outbox (spec: "The outbox writes in the caller's transaction", "The outbox forwarder follows the container lifecycle", "Outbox conformance")

- [ ] 10.1 RED: add `TestOutbox_Delivery` (2 rows), `TestOutbox_SendTx` (5 rows), `TestNewOutbox` (5 rows), `TestOutbox_Lifecycle` (3 rows), `TestOutbox_ShutdownBeforeStart`, `TestOutboxConformance` (harness on `sqltx.BeginTx`); verify the run fails to compile naming only `NewOutbox`, `Outbox`, `OutboxConfig`
- [ ] 10.2 GREEN: implement `NewOutbox` with `TxSource()` = `sqltx.Source()`, `SendTx` resolving `sqltx.FromDB(ctx, outboxDB)` and returning its `doger.ErrNoTransaction` / `sqltx.ErrForeignTransaction` errors unchanged and publishing through `forwarder.NewPublisher` over a watermill-sql publisher on `tx.SQL()` (D7), delegation of `Send`/`Subscribe`, and `Start`/`Shutdown` running and closing the `forwarder.Forwarder`; verify the tests pass, goleak is clean, and `go test -race ./...` is green
- [ ] 10.3 REFACTOR: Extract Function `boundTx`; Move Function message mapping shared with `dogerwatermill` into an exported helper only if both modules need it (else none, with the reason); verify `go test -race ./...` and `make lint-all` pass
- [ ] 10.4 Add `ExampleNewOutbox`; verify it compiles with `go vet ./...`

## 11. Integration and docs

- [ ] 11.1 Write the "watermill transport" guide in `docs/` (setup, options, SQL helpers, outbox with `sqltx`, recipes, at-least-once and idempotency, attempt counting caveat) and a CHANGELOG entry; verify the docs build per `docs/` instructions
- [ ] 11.2 Run `make test-all`, `make lint-all` and `make vuln` with both DSNs set; verify all pass

## Workflow follow-up

- Archive after review; requires `add-event-transport` archived first.
- Group 10 (outbox) starts only after `add-sql-event-transport` has delivered `events/sqltx`; groups 1-9 do not depend on it.
