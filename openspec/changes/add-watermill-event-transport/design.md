# Design

## Context

See proposal.md (Why) and the two specs. The contract is `add-event-transport` (archived first): `doger.EventTransport` (`Send`, `Subscribe` returning a stop function), the optional `doger.TxEventSender` (`SendTx`, `TxSource`), `Subscription{Destination, Group, Mode, Ephemeral}`, the handler's error as the acknowledgement signal, `doger.PermanentError`, the rule that a new group starts at the latest event, and `eventtransporttest.Run`.

Facts read from the watermill sources (v1.5.3 and the backend modules in the module cache) that shape this design:

- `message.Publisher.Publish(topic, msgs...)` takes no context; `message.Subscriber.Subscribe(ctx, topic)` returns `<-chan *message.Message` and "to receive the next message, `Ack()` must be called", so one subscription handles one message at a time. `Message` has `UUID`, `Metadata` (`map[string]string`, `Get`/`Set`), `Payload`, `Ack`, `Nack`, `Context`, `SetContext`.
- The consumer group is subscriber configuration, not a `Subscribe` argument: watermill-sql `SubscriberConfig.ConsumerGroup`, watermill-kafka `SubscriberConfig.ConsumerGroup`, watermill-redisstream `SubscriberConfig.ConsumerGroup`, watermill-nats `QueueGroupPrefix` / `DurablePrefix`. watermill's own `cqrs.EventProcessorConfig.SubscriberConstructor` builds one subscriber per handler for the same reason.
- Where a new group starts:

  | Backend | New group starts at | Source | Doger contract |
  |---|---|---|---|
  | watermill-sql (Postgres, MySQL) | offset 0: `BeforeSubscribingQueries` inserts "zero offsets" | `offsets_adapter_postgresql.go` | needs a wrapped offsets adapter (D3) |
  | watermill-kafka | sarama default `Consumer.Offsets.Initial` = `OffsetNewest`; `DefaultSaramaSubscriberConfig` does not change it | `pkg/kafka/subscriber.go` | met by default; recipe warns against `OffsetOldest` |
  | watermill-redisstream | `OldestId` defaults to `"0"` (oldest); `"$"` means latest | `pkg/redisstream/subscriber.go` | recipe sets `OldestId: "$"` |
  | watermill-nats JetStream | consumer default deliver policy (all) unless `SubscribeOptions` say otherwise | `pkg/nats/jetstream.go` | recipe adds `nats.DeliverNew()` |
  | watermill-nats core, gochannel | live only, no redelivery on `Nack`, gochannel fans out to every subscriber (no groups) | `pkg/nats`, `pubsub/gochannel/pubsub.go` | cannot pass the suite; documented as unsupported for `DeliverOnce` |

- watermill-sql publishes on a `*sql.Tx` through `sql.NewPublisher(sql.TxFromStdSQL(tx), ...)`, one publisher per transaction; `forwarder.NewPublisher` wraps a publisher to send enveloped messages to a forwarder topic, and `forwarder.NewForwarder(sub, pub, logger, cfg)` with `Run(ctx)`/`Close()` relays them.
- watermill-sql's go.mod requires pgx, lib/pq and the MySQL driver, so it must not be a dependency of the base adapter module.
- Transactions are bound by the shared module `events/sqltx` from `add-sql-event-transport` (deps: doger and the standard library), which this change's `wmsql` module requires.

## Goals / Non-Goals

**Goals:**
- One adapter that works with any watermill backend whose subscribers can be built per group.
- SQL helpers that pass the full conformance suite, including the outbox subtests, in CI.
- No goroutine outlives `stop`; no change to doger core.

**Non-Goals:**
- Typed helpers for Kafka, NATS and Redis (documented recipes; follow-up modules if asked for).
- Using watermill's `Router`, middleware or `cqrs` package. The adapter needs exactly one consuming loop per subscription, and the router would add a second lifecycle to coordinate with doger's.
- Metrics. watermill's prometheus metrics can wrap the publisher and subscribers the consumer passes in.

## Decisions

### D1. Two modules
- `events/watermill` (package `dogerwatermill`) requires doger and watermill core only. `events/watermill/wmsql` (package `dogerwmsql`) adds watermill-sql and `events/sqltx`. Both paths mirror their directories (`module-identity`).
- *Alternative:* one module with the SQL helpers inside. Rejected: every Kafka or NATS user would download pgx and the MySQL driver.
- *Alternative:* one helper module per backend now. Rejected for this change: only SQL is needed to pass the suite in CI, and untested helpers would be worse than documented recipes. Added later on demand without changing these specs.

### D2. Construction and the subscriber factory
```go
type SubscriberFactory func(ctx context.Context, sub doger.Subscription) (message.Subscriber, error)

func New(pub message.Publisher, subs SubscriberFactory, opts ...Option) (*Transport, error)
// Options: WithRetryDelay(d), WithMaxAttempts(n), WithPoisonTopic(t), WithMetadataPrefix(p),
//          WithLogger(watermill.LoggerAdapter), WithClock(doger.Clock)
var ErrInvalidConfig = errors.New("doger: dogerwatermill: invalid configuration")
```
- The factory receives the whole `doger.Subscription`, so it can set the group, use `Ephemeral` for non-durable consumers, and choose per destination. A factory returning `(nil, nil)` is a configuration error, not a panic.
- The transport owns each subscriber the factory built and closes it in `stop`. It never closes `pub`: the caller created it and may share it.
- Options return validation errors through `New` (functional options with errors, per golang-design-patterns).

### D3. Starting new SQL groups at the head
- `dogerwmsql` wraps watermill-sql's `DefaultPostgreSQLOffsetsAdapter` / `DefaultMySQLOffsetsAdapter`, replacing `BeforeSubscribingQueries` with an insert that seeds `offset_acked` and `last_processed_transaction_id` from the newest row of the messages table (`ON CONFLICT DO NOTHING` / `INSERT IGNORE`), so an existing group keeps its offset and concurrent first subscribers race safely on the primary key.
- *Alternative:* subscribe, then skip messages older than the subscription time. Rejected: it depends on clocks and still acks old rows one by one.
- *Alternative:* reject SQL for `DeliverAll`. Rejected: SQL is the backend most doger users already have.
- The seed query is verified against both databases by "New and existing groups" and "Concurrent first subscribers of one group".

### D4. Ephemeral groups
`Ephemeral` subscriptions get a subscriber wrapper whose `Close` deletes the group's row from the offsets table after closing the watermill subscriber. Messages are not deleted; watermill-sql keeps the log. `WithKeepEphemeralGroups()` disables it for debugging.

### D5. Message mapping
- UUID = envelope ID, so watermill-level deduplication (for example NATS `TrackMsgID`) and doger's `EnvelopeFromContext(ctx).ID` agree.
- Metadata keys: `<prefix>event_type`, `version` (decimal), `content_type`, `published_at` (`time.RFC3339Nano`, UTC), and `<prefix>h_<name>` per header. A prefix avoids clashes with backend and middleware keys; `h_` keeps headers in their own namespace.
- `Send` checks `ctx.Err()` first, sets `msg.SetContext(ctx)` (backends that honor it can cancel), and recovers panics from `Publish` into errors, because watermill's `Publish` takes no context.

### D6. The consuming loop
```
Subscribe: sub := factory(ctx, s); ch := sub.Subscribe(loopCtx, s.Destination); go loop
loop:  for msg in ch:
         env, ok := decode(msg)            !ok        -> giveUp(msg)
         err := h(msg.Context(), env)      nil        -> Ack, forget(msg.UUID)
                                           permanent  -> giveUp(msg)
                                           attempts>=max (max>0) -> giveUp(msg)
                                           else       -> wait(retryDelay | loopCtx) ; Nack
giveUp: poison topic set ? Publish(poison, msg) ok -> Ack ; err -> log, Nack
                         : log, Ack
stop(ctx): cancel loopCtx ; wait loop done or ctx.Done() ; sub.Close() ; once
```
- One goroutine per subscription with a clear owner (the stop function) and exit (`loopCtx` cancel plus the channel closing), per golang-concurrency. A `sync.Once` makes stop idempotent; a `done` channel signals the loop's exit.
- Attempts live in a `map[string]int` owned by the loop goroutine (no lock), keyed by UUID and deleted on ack or give-up, so memory is bounded by messages in flight. The count resets on restart; the guide says so, and backends with native delivery counts can be added later.
- The retry wait uses the injected clock (`WithClock`, default real clock) so tests drive it with a fake clock, and selects on `loopCtx.Done()` so stop never waits a full delay.
- Concurrency per subscription is one message at a time, as watermill delivers. Consumers needing parallelism scale through backend partitions or more instances; a per-subscription worker pool would break per-key ordering that Kafka users expect.

### D7. Outbox over the shared `sqltx` binder
- Transactions come from `github.com/kartaladev/doger/events/sqltx` (defined by `add-sql-event-transport`): `sqltx.Tx` implements `doger.Tx` and exposes `SQL() *sql.Tx` and `DB() *sql.DB`; `BeginTx`, `InTx` and `BindTx` put it in a context; `From(ctx)` finds it; `FromDB(ctx, db)` finds it only when bound to `db`, returning errors matching `doger.ErrNoTransaction` (none bound or ended) or `sqltx.ErrForeignTransaction` (bound to another `*sql.DB`); `Source()` is the `doger.TxSource`.
- `Outbox.TxSource()` returns `sqltx.Source()`. `Outbox.SendTx` calls `sqltx.FromDB(ctx, outboxDB)` and returns its error unchanged, so every SQL adapter reports a missing transaction as `doger.ErrNoTransaction` and a transaction on another database as `sqltx.ErrForeignTransaction` (a row written there would silently lose the atomicity the outbox exists for). `ErrInvalidConfig` stays for construction errors only. Otherwise it builds `forwarder.NewPublisher(sql.NewPublisher(sql.TxFromStdSQL(tx.SQL()), ...), ForwarderTopic)` and publishes the mapped message to the envelope's destination. One publisher per call, as watermill-sql requires one publisher per transaction.
- The forwarder (`forwarder.NewForwarder` over a watermill-sql subscriber on the forwarder topic and the transport's publisher) delivers messages after commit.
- `Outbox` implements `doger.StarterWithContextAndError` / `ShutdownerWithContextAndError`, so registering it as the `NameOf[doger.EventTransport]()` service ties the forwarder to the container.
- *Alternative:* a binder private to `dogerwmsql`. Rejected by the maintainer: two binders would let an application bind a transaction the after-commit listeners see but the outbox does not.
- *Alternative:* leave the outbox to a documented recipe. Rejected: the outbox is the reason teams pick a SQL-backed transport, and the conformance suite can only check it if it is code.

## Risks / Trade-offs

- [Head-of-line blocking: a failing message blocks its subscription until it succeeds or gives up] → default `WithMaxAttempts(10)` with a 1s delay bounds the block to about 10s per message; poison topic preserves the message.
- [Attempt counts reset on restart, so a crash loop can retry forever] → documented; permanent errors and poison topics are the intended exits.
- [Recipes for Kafka, NATS and Redis are not exercised in CI] → each recipe states the exact setting that makes new groups start at the latest event and links the backend source; typed helpers with tests are the follow-up.
- [The SQL seed query depends on watermill-sql's table layout] → pinned to watermill-sql/v4; the integration tests fail on a layout change, and Dependabot upgrades run them.
- [CI on macOS and Windows has no database services] → database tests skip outside Linux CI only when `CI` is unset; Linux CI requires the DSNs (spec "Missing database configuration").

## Public API & Defaults

New modules; no change to doger core. **No breaking change.**

| Identifier | Kind | Default / override |
|---|---|---|
| `dogerwatermill.New(pub, subs, opts...) (*Transport, error)` | constructor | n/a |
| `dogerwatermill.SubscriberFactory` | func type | consumer-provided |
| `WithRetryDelay(d)`, `WithMaxAttempts(n)`, `WithPoisonTopic(t)`, `WithMetadataPrefix(p)`, `WithLogger(l)`, `WithClock(c)` | options | 1s, 10, none (ack and log), `doger_`, slog default, real clock |
| `dogerwatermill.ErrInvalidConfig` | sentinel | n/a |
| `dogerwmsql.PostgresSubscriberFactory(db, opts...)`, `MySQLSubscriberFactory(db, opts...)` | constructors | new groups at head; ephemeral cleanup on |
| `dogerwmsql.WithKeepEphemeralGroups()`, `WithSubscriberConfig(func(*sql.SubscriberConfig))` | options | off; watermill-sql defaults |
| `dogerwmsql.NewOutbox(db, transport, OutboxConfig) (*Outbox, error)`, `OutboxConfig{ForwarderTopic, Logger}` | outbox | `doger_outbox`, transport's logger; `TxSource()` is always `sqltx.Source()` |

## Test Strategy

Each scenario maps to one Go test; `Examples` rows run as named subtests. All tests run with `-race`; each module's `TestMain` runs goleak. Unit tests use fakes in `fakes_test.go`: a recording publisher, a fake subscriber with a controllable channel that records `Ack`/`Nack`/`Close`, and an in-process fake broker (topic → subscribers, round robin per group) for round trips. Database tests read `DOGER_TEST_POSTGRES_DSN` / `DOGER_TEST_MYSQL_DSN`, create uniquely named tables per test, skip locally when unset, and fail in CI when unset.

**`specs/watermill-event-transport/spec.md`** (module `events/watermill`)

| Scenario | Test | Kind |
|---|---|---|
| Constructor arguments | `transport_test.go: TestNew/<row>` | table-driven |
| Construction is passive | `transport_test.go: TestNew_Passive` | unit, goleak |
| Message fields | `send_test.go: TestSend_Fields/<row>` | table-driven |
| Send failures | `send_test.go: TestSend_Failures/<row>` | table-driven |
| Subscribe outcomes | `subscribe_test.go: TestSubscribe/<row>` | table-driven |
| Round trip through a fake broker | `subscribe_test.go: TestRoundTrip/<row>` | table-driven |
| Foreign messages | `subscribe_test.go: TestForeignMessages/<row>` | table-driven |
| Handler outcomes | `ack_test.go: TestAck/<row>` | table-driven |
| Retry delay | `ack_test.go: TestAck_RetryDelay/<row>` | table-driven, fake clock |
| Poison publish failure | `ack_test.go: TestAck_PoisonFailure` | unit |
| Stop waits for the handler | `stop_test.go: TestStop_WaitsHandler` | unit, goleak, `testWithTimeout` |
| Stop context bounds the wait | `stop_test.go: TestStop_Deadline` | unit, `testWithTimeout` |
| Stop edge cases | `stop_test.go: TestStop_Edges/<row>` | table-driven, goleak |
| Concurrent sends and subscriptions | `transport_test.go: TestConcurrent` | race |
| Conformance on SQL backends | `wmsql/conformance_test.go: TestConformance/<row>` | conformance, database |
| Missing database configuration | `wmsql/dsn_test.go: TestDSNRequired/<row>` | table-driven |

The conformance test lives in `wmsql` because it needs watermill-sql; it exercises `dogerwatermill.Transport` end to end.

**`specs/watermill-sql-support/spec.md`** (module `events/watermill/wmsql`)

| Scenario | Test | Kind |
|---|---|---|
| Groups on a SQL backend | `factory_test.go: TestFactory_Groups/<row>` | integration, database |
| A nil database | `factory_test.go: TestFactory_NilDB/<row>` | table-driven |
| New and existing groups | `offsets_test.go: TestOffsets_Start/<row>` | table-driven, database |
| Concurrent first subscribers of one group | `offsets_test.go: TestOffsets_ConcurrentSeed` | race, database |
| Group rows after stop | `factory_test.go: TestFactory_EphemeralCleanup/<row>` | table-driven, database |
| Cleanup failure is reported | `factory_test.go: TestFactory_CleanupError` | unit, database |
| Outbox delivery follows the transaction | `outbox_test.go: TestOutbox_Delivery/<row>` | integration (two roots), database |
| SendTx and the bound transaction | `outbox_test.go: TestOutbox_SendTx/<row>` (rows assert `doger.ErrNoTransaction` and `sqltx.ErrForeignTransaction` with `errors.Is`) | table-driven, database |
| Outbox configuration | `outbox_test.go: TestNewOutbox/<row>` | table-driven |
| Forwarder lifecycle | `outbox_test.go: TestOutbox_Lifecycle/<row>` | integration, goleak, database |
| Shutdown before Start | `outbox_test.go: TestOutbox_ShutdownBeforeStart` | unit |
| Outbox passes the transaction subtests | `conformance_test.go: TestOutboxConformance` | conformance, database |

**Examples:** `ExampleNew` (memory-free fake backend), `ExampleWithPoisonTopic` in `events/watermill`; `ExamplePostgresSubscriberFactory`, `ExampleNewOutbox` (with `sqltx.InTx`) in `wmsql` (compile-only, no `// Output:`, since they need a database).

**Hot paths and their tests:** `Send` mapping (`TestSend_Fields`, `TestRoundTrip`); the consuming loop's ack/nack/give-up branches (`TestAck`, `TestForeignMessages`, `TestAck_PoisonFailure`); stop and goroutine exit (`TestStop_*`, goleak); the head-seeding query (`TestOffsets_Start`, `TestOffsets_ConcurrentSeed`); `SendTx` on the bound `sqltx` transaction and its error branches (`TestOutbox_Delivery`, `TestOutbox_SendTx`). The binder's own callbacks are tested in `add-sql-event-transport`.

## Migration Plan

New modules; nothing to migrate. Adopting: create a watermill publisher, pass `dogerwatermill.New(pub, factory)` as `InjectorOpts.EventTransport` (or register an `Outbox` as the transport service). Rolling back: remove the option; watermill tables stay in the database untouched.

## Open Questions

- Typed helper modules for Kafka, NATS JetStream and Redis Streams, each with its own CI backend; to be requested per backend.
