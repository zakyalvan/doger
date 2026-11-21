# Spec Delta

## Purpose

Makes watermill-sql (PostgreSQL and MySQL) meet doger's transport contract out of the box: consumer groups that start at the latest event and clean up after themselves, and a transactional outbox that writes events in the caller's database transaction bound through the shared `sqltx` module.

## ADDED Requirements

### Requirement: Subscriber factories map subscriptions to consumer groups
`PostgresSubscriberFactory(db, opts...)` and `MySQLSubscriberFactory(db, opts...)` SHALL return a `dogerwatermill.SubscriberFactory` that builds a watermill-sql subscriber whose `ConsumerGroup` is the subscription's group, so members of one group compete and different groups each receive every message. A nil `db` SHALL panic with "doger: dogerwmsql: nil *sql.DB".

#### Scenario: Groups on a SQL backend
```gherkin
Given a <database> reachable through its test DSN and two roots "b1" and "b2" using dogerwatermill transports with <factory>
And on both roots a remote listener "mail" with ListenerDelivery(DeliverOnce) and a remote listener "cache" with ListenerDelivery(DeliverAll) and InstanceIDs "b1" and "b2"
And both roots are started
When a third root publishes 20 OrderPlaced events through the same tables
Then "mail" ran 20 times in total across "b1" and "b2", and "cache" ran 20 times on "b1" and 20 times on "b2"

Examples:
  | database   | factory                                |
  | PostgreSQL | dogerwmsql.PostgresSubscriberFactory(db) |
  | MySQL      | dogerwmsql.MySQLSubscriberFactory(db)    |
```

#### Scenario: A nil database
```gherkin
Given no database
When the consumer calls <constructor>(nil)
Then it panics with "doger: dogerwmsql: nil *sql.DB"

Examples:
  | constructor                         |
  | dogerwmsql.PostgresSubscriberFactory |
  | dogerwmsql.MySQLSubscriberFactory    |
```

<!-- cases: N/A 4 boundaries: group names are validated by watermill-sql; doger groups are non-empty by construction -->
<!-- cases: N/A 8 concurrency: covered by the conformance suite -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: A new group starts at the latest message
A subscriber built by these factories for a group with no stored offset SHALL start after the last message already in the topic, not at offset 0. A group with a stored offset SHALL resume after it.

#### Scenario: New and existing groups
```gherkin
Given a <database> topic "orders.placed" that already holds 5 messages
And <group>
When a subscription for group "mail" starts and 1 more message is published
Then the subscription handled <handled> messages

Examples:
  | database   | group                                                          | handled |
  | PostgreSQL | no stored offset for "mail"                                    | 1       |
  | MySQL      | no stored offset for "mail"                                    | 1       |
  | PostgreSQL | a stored offset for "mail" acknowledging the first 3 messages  | 3       |
  | PostgreSQL | an empty topic instead of 5 messages, no stored offset         | 1       |
```

#### Scenario: Concurrent first subscribers of one group
```gherkin
Given a PostgreSQL topic "orders.placed" holding 5 messages and no stored offset for "mail"
When 4 subscriptions for group "mail" start at the same time and 1 more message is published
Then exactly 1 of them handled exactly 1 message
```

<!-- cases: N/A 2 overrides: none, the start position is part of doger's contract -->
<!-- cases: N/A 5 errors: a failing seed query fails Subscribe with the database error, as any watermill-sql initialization error does -->
<!-- cases: N/A 9 panics: no consumer callback is involved -->
<!-- cases: N/A 10 compatibility: new module; watermill-sql's own default (start at 0) is unchanged for subscribers built without these factories -->

### Requirement: Ephemeral groups are removed when they stop
When a subscription with `Ephemeral` true stops, the factory's subscriber SHALL delete that group's stored offset. `WithKeepEphemeralGroups()` SHALL disable the deletion. A non-ephemeral group's offset SHALL always be kept.

#### Scenario: Group rows after stop
```gherkin
Given a PostgreSQL topic "orders.placed" and a factory built with <options>
And a started subscription with Group "<group>" and Ephemeral <ephemeral> that acknowledged 1 message
When its stop function returns nil
Then the stored offset for "<group>" <kept>

Examples:
  | options                    | group          | ephemeral | kept           |
  | no options                 | cache/7f3a     | true      | no longer exists |
  | WithKeepEphemeralGroups()  | cache/7f3a     | true      | still exists   |
  | no options                 | mail           | false     | still exists   |
```

#### Scenario: Cleanup failure is reported
```gherkin
Given an ephemeral subscription whose database connection was closed before stop
When its stop function runs
Then it returns an error from the database driver and the subscriber is closed
```

<!-- cases: N/A 3,4,6: no further inputs -->
<!-- cases: N/A 8 concurrency: one stop per subscription; repeated stops return nil per watermill-event-transport -->
<!-- cases: N/A 9 panics: no callback -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: The outbox writes in the caller's transaction
`NewOutbox(db, transport, cfg)` SHALL return an `*Outbox` implementing `doger.TxEventSender` whose `TxSource()` is `sqltx.Source()`. `SendTx` SHALL resolve its transaction with `sqltx.FromDB(ctx, db)`, return that function's error unchanged, and write the message on the resolved `SQL()`, addressed to the envelope's destination through the forwarder topic. `Send` and `Subscribe` SHALL delegate to the transport.

#### Scenario: Outbox delivery follows the transaction
```gherkin
Given a started root "a" with InjectorOpts.EventTransport set to an Outbox over a dogerwatermill transport on PostgreSQL, and RegisterEventType[OrderPlaced](a, "orders.placed", EventExternalize(ExternalizeOutbox))
And a started root "b" on the same database with a remote listener "mail" (DeliverOnce)
And txCtx, tx, _ := sqltx.BeginTx(ctx, db, nil), and "a" published OrderPlaced{ID: "1"} with txCtx
When the consumer <action>
Then "mail" ran <ran> times within 5s

Examples:
  | action              | ran |
  | calls tx.Commit()   | 1   |
  | calls tx.Rollback() | 0   |
```

#### Scenario: SendTx and the bound transaction
```gherkin
Given an Outbox built on database "db"
When the consumer calls SendTx with <ctx>
Then it returns <result>

Examples:
  | ctx                                                                  | result                                                          |
  | the context returned by sqltx.BeginTx(ctx, db, nil)                  | nil, and the message row is visible inside that transaction only |
  | the context returned by sqltx.InTx(ctx, db, fn) inside fn            | nil, and the row commits when fn returns nil                    |
  | context.Background()                                                 | an error matching doger.ErrNoTransaction, and nothing was written |
  | the context returned by sqltx.BeginTx(ctx, otherDB, nil)             | an error matching sqltx.ErrForeignTransaction, and nothing was written |
  | the context of a transaction that was already committed             | an error matching doger.ErrNoTransaction                        |
```

#### Scenario: Outbox configuration
```gherkin
Given a database "db" and a transport "t"
When the consumer calls NewOutbox(<db>, <t>, <cfg>)
Then it returns <result>

Examples:
  | db  | t   | cfg                                 | result                                                                       |
  | db  | t   | OutboxConfig{}                      | an *Outbox whose TxSource() is sqltx.Source() and forwarder topic is "doger_outbox" |
  | db  | t   | OutboxConfig{ForwarderTopic: "fwd"} | an *Outbox whose forwarder topic is "fwd"                                    |
  | db  | t   | OutboxConfig{ForwarderTopic: ""}    | an *Outbox whose forwarder topic is "doger_outbox"                           |
  | nil | t   | OutboxConfig{}                      | an error matching dogerwatermill.ErrInvalidConfig                            |
  | db  | nil | OutboxConfig{}                      | an error matching dogerwatermill.ErrInvalidConfig                            |
```

<!-- cases: N/A 4 boundaries: no numeric input -->
<!-- cases: N/A 6 duplicate: one Outbox per database; several Outboxes on one database share tables by forwarder topic -->
<!-- cases: N/A 8 concurrency: SendTx runs on the publisher's goroutine inside its transaction; the forwarder is covered by the lifecycle requirement -->
<!-- cases: N/A 9 panics: SendTx panics are recovered by doger core as *ExternalizeError -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: The outbox forwarder follows the container lifecycle
The `Outbox` SHALL implement `doger.StarterWithContextAndError` and `doger.ShutdownerWithContextAndError`. `Start` SHALL run the forwarder in a goroutine and return once it is running; `Shutdown` SHALL close it and wait for it to exit. Messages committed while the forwarder is not running SHALL be relayed after it starts.

#### Scenario: Forwarder lifecycle
```gherkin
Given a root whose NameOf[doger.EventTransport]() service is an Outbox on PostgreSQL, and a remote listener "mail" on another started root
When <sequence>
Then "mail" ran <ran> times within 5s and no goroutine leaked after both roots shut down

Examples:
  | sequence                                                                                  | ran |
  | the root starts, then publishes one OrderPlaced inside sqltx.InTx                      | 1   |
  | one OrderPlaced is committed inside sqltx.InTx before the root starts, then it starts      | 1   |
  | the root starts, shuts down, and one OrderPlaced is committed inside sqltx.InTx afterwards       | 0   |
```

#### Scenario: Shutdown before Start
```gherkin
Given an Outbox that was never started
When the consumer calls Shutdown(context.Background())
Then it returns nil
```

<!-- cases: N/A 3,4,6: no further inputs -->
<!-- cases: N/A 5 errors: a forwarder that fails to start returns its error from Start, failing the container's Start as any Starter error does -->
<!-- cases: N/A 9 panics: forwarder panics are recovered by watermill's router -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Outbox conformance
An `Outbox` on PostgreSQL SHALL pass `eventtransporttest.Run` with `eventtransporttest.WithTx`, using a harness built on `sqltx.BeginTx`.

#### Scenario: Outbox passes the transaction subtests
```gherkin
Given PostgreSQL reachable through DOGER_TEST_POSTGRES_DSN and a harness built on sqltx.BeginTx
When eventtransporttest.Run(t, outboxFactory, eventtransporttest.WithTx(harness)) runs
Then every subtest, including "OutboxCommitDelivers" and "OutboxRollbackDiscards", passes with -race
```

<!-- cases: N/A 2-10: the suite owns these cases -->
