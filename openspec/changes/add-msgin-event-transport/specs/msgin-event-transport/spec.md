# Spec Delta

## Purpose

Lets a doger application carry externalized events through msgin's `database/sql` adapter: an outbox in the caller's transaction, competing consumers per group, retries and dead letters, and an optional idempotent consumer, behind doger's `EventTransport` contract and marked experimental until msgin is released.

## ADDED Requirements

### Requirement: Constructing the transport
`dogermsgin.New(db, dialect, opts...)` SHALL return a `*Transport` implementing `doger.EventTransport` and `doger.TxEventSender`, or a nil transport and an error when `db` is nil (`ErrNilDB`), the dialect or its msgin `LeaseDialect` is nil (msgin's `ErrNilDialect`), or an option is invalid (`ErrInvalidOption`, or msgin's `ErrInvalidTableName`).

#### Scenario: Constructor outcomes
```gherkin
Given a SQLite *sql.DB "db" opened with modernc.org/sqlite
When the consumer calls dogermsgin.New(<db>, <dialect>, <options>)
Then it returns <result>

Examples:
  | db  | dialect                | options                          | result                                                   |
  | db  | SQLite(sqlite.LeaseDialect()) | no options                       | a *Transport and nil                                      |
  | nil | SQLite(sqlite.LeaseDialect()) | no options                       | nil and an error matching ErrNilDB                        |
  | db  | SQLite(nil)                   | no options                       | nil and an error matching msginsql.ErrNilDialect          |
  | db  | SQLite(sqlite.LeaseDialect()) | WithTablePrefix("bad-prefix;")   | nil and an error matching msginsql.ErrInvalidTableName    |
  | db  | SQLite(sqlite.LeaseDialect()) | WithRegistryTable("")            | nil and an error matching msginsql.ErrInvalidTableName    |
  | db  | SQLite(sqlite.LeaseDialect()) | WithConcurrency(0)               | nil and an error matching ErrInvalidOption                |
  | db  | SQLite(sqlite.LeaseDialect()) | WithPollInterval(0)              | nil and an error matching ErrInvalidOption                |
  | db  | SQLite(sqlite.LeaseDialect()) | WithRetry(msgin.RetryPolicy{MaxAttempts: -1}) | nil and an error matching msgin.ErrInvalidMaxAttempts |
```

#### Scenario: The transport satisfies doger's interfaces
```gherkin
Given a *Transport returned by dogermsgin.New(db, dogermsgin.SQLite(sqlite.LeaseDialect()))
When the consumer asserts it to doger.EventTransport and doger.TxEventSender
Then both assertions succeed
```

<!-- cases: N/A 4 boundaries: WithConcurrency(1) and large values are accepted; 0 is the boundary row -->
<!-- cases: N/A 6 duplicate: options apply in order, the last wins -->
<!-- cases: N/A 7 lifecycle: construction opens no connection and starts no goroutine -->
<!-- cases: N/A 8 concurrency: construction is single-threaded -->
<!-- cases: N/A 9 panics: New returns errors and never panics -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Table names
For each (destination, group) the transport SHALL use a queue table and a dead-letter table named `<prefix>q_<hash>` and `<prefix>d_<hash>`, where `<hash>` is the first 16 hex digits of SHA-256 of `destination + "\x00" + group` and the prefix defaults to "doger_". `QueueTable` and `DeadLetterTable` SHALL return these names.

#### Scenario: Deterministic, valid names
```gherkin
Given a *Transport built with <options>
When the consumer calls QueueTable("<destination>", "<group>") and DeadLetterTable("<destination>", "<group>")
Then the results are <queue> and <dlq>, and both pass msginsql.ValidateIdent

Examples:
  | options                    | destination    | group        | queue                         | dlq                           |
  | no options                 | orders.placed  | mail         | "doger_q_" + hash("orders.placed\x00mail")  | "doger_d_" + hash("orders.placed\x00mail") |
  | WithTablePrefix("shop_")   | orders.placed  | mail         | "shop_q_" + hash("orders.placed\x00mail")   | "shop_d_" + hash("orders.placed\x00mail")  |
  | no options                 | orders.placed  | mail/pod-0   | "doger_q_" + hash("orders.placed\x00mail/pod-0") | "doger_d_" + hash("orders.placed\x00mail/pod-0") |
```

#### Scenario: Different pairs get different tables
```gherkin
Given a *Transport with no options
When the consumer computes QueueTable for ("orders.placed", "mail"), ("orders.placed", "audit") and ("orders.cancelled", "mail")
Then the three names are distinct
```

#### Scenario: A table already registered for another pair is rejected
```gherkin
Given a *Transport with WithEnsureSchema() and a registry row inserted directly that maps ("orders.cancelled", "x") to QueueTable("orders.placed", "mail")
When the consumer calls Subscribe for destination "orders.placed", group "mail", mode DeliverOnce
Then it returns an error matching ErrGroupConflict and starts no consumer
```

<!-- cases: N/A 3 zero values: empty destinations and groups never reach the transport; doger validates them -->
<!-- cases: N/A 5 errors: names are always valid identifiers by construction -->
<!-- cases: N/A 7,8,9: pure functions -->
<!-- cases: N/A 10 compatibility: the naming scheme is part of the experimental contract and may change before msgin v1 -->

### Requirement: Schema is checked, created only on request
Without `WithEnsureSchema()`, `Subscribe` and `Send` SHALL fail with an error matching `ErrSchemaNotReady` that names each missing table and SHALL run no DDL. With it, the transport SHALL create the registry, queue and dead-letter tables it needs, idempotently.

#### Scenario: Missing and present schema
```gherkin
Given an empty SQLite database and a *Transport built with <options>
When the consumer calls Subscribe for destination "orders.placed", group "mail", mode DeliverOnce
Then it returns <result>

Examples:
  | options             | result                                                                                 |
  | no options          | an error matching ErrSchemaNotReady whose message names "doger_event_groups"           |
  | WithEnsureSchema()  | a stop function and nil, and the registry, queue and dead-letter tables exist          |
```

#### Scenario: Pre-provisioned schema
```gherkin
Given a SQLite database where the consumer created "doger_event_groups" with dogermsgin.RegistryDDL(dogermsgin.SQLite(sqlite.LeaseDialect()), "doger_event_groups") and the queue and dead-letter tables with sqlite.DDL(QueueTable("orders.placed", "mail")) and sqlite.DDL(DeadLetterTable("orders.placed", "mail"))
And a *Transport with no options
When the consumer calls Subscribe for destination "orders.placed", group "mail", mode DeliverOnce
Then it returns a stop function and nil
```

#### Scenario: EnsureSchema is idempotent
```gherkin
Given a *Transport built with WithEnsureSchema()
When two transports on the same database subscribe the same destination and group concurrently
Then both Subscribe calls return nil
```

<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 6 duplicate: covered by the idempotent scenario -->
<!-- cases: N/A 9 panics: driver errors are returned -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Send fans out to registered groups
`Send` SHALL insert one message per group registered for the envelope's destination, into that group's queue table, and SHALL return nil without writing when no group is registered. A group registered after a `Send` SHALL NOT receive that event.

#### Scenario: Copies per group
```gherkin
Given a *Transport with WithEnsureSchema() and <groups> registered for "orders.placed"
When the consumer calls Send with an envelope for destination "orders.placed"
Then Send returns nil and each registered group's queue table holds 1 message, <total> in total

Examples:
  | groups                  | total |
  | none                    | 0     |
  | "mail"                  | 1     |
  | "mail", "audit", "bi"   | 3     |
```

#### Scenario: A new group starts at the latest event
```gherkin
Given a *Transport with WithEnsureSchema() and group "mail" registered for "orders.placed"
And the consumer sent 5 envelopes to "orders.placed"
When a subscription for group "late" on "orders.placed" starts and the consumer sends 1 more envelope
Then the "late" handler is called 1 time and the "mail" handler 6 times
```

#### Scenario: A durable group keeps events while no member runs
```gherkin
Given a *Transport with WithEnsureSchema() whose subscription for group "mail" on "orders.placed" was stopped
When the consumer sends 3 envelopes to "orders.placed" and then subscribes group "mail" again
Then the "mail" handler is called 3 times
```

#### Scenario: Send failures are returned
```gherkin
Given a *Transport whose database was closed
When the consumer calls Send with an envelope for destination "orders.placed"
Then Send returns a non-nil error that does not satisfy doger.IsPermanentError
```

<!-- cases: N/A 3 zero values: an empty envelope never reaches a transport; doger builds it -->
<!-- cases: N/A 4 boundaries: 0, 1 and many groups are the rows -->
<!-- cases: N/A 7 lifecycle: Send needs no running subscription -->
<!-- cases: N/A 9 panics: driver errors are returned -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: DeliverOnce members compete
A `DeliverOnce` subscription SHALL run a msgin consumer on the group's queue table, so that each event is handled by exactly one member of the group across all transports sharing the database, apart from redeliveries after a failure or an expired lease.

#### Scenario: Two instances in one group
```gherkin
Given two *Transport values "i1" and "i2" on the same Postgres database, each subscribed with group "mail", mode DeliverOnce, on "orders.placed"
When 100 envelopes with distinct IDs are sent to "orders.placed"
Then the handlers of "i1" and "i2" together are called 100 times with 100 distinct IDs
```

<!-- cases: N/A 2,3,4,5,6: inputs are covered by "Send fans out to registered groups" -->
<!-- cases: N/A 7 lifecycle: covered by "Subscriptions stop cleanly" -->
<!-- cases: N/A 9 panics: covered by "Handler outcomes map to msgin settlement" -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: DeliverAll is not supported
`Subscribe` with `Mode` `DeliverAll` SHALL return an error matching `doger.ErrSubscriptionNotSupported`, register nothing and start nothing.

#### Scenario: Broadcast subscription
```gherkin
Given a *Transport with WithEnsureSchema()
When the consumer calls Subscribe for destination "orders.placed", group "cache/pod-0", mode DeliverAll, Ephemeral <ephemeral>
Then it returns a nil stop function and an error matching doger.ErrSubscriptionNotSupported
And the registry holds no row for "cache/pod-0"

Examples:
  | ephemeral |
  | true      |
  | false     |
```

<!-- cases: N/A 1-7: one input, one outcome -->
<!-- cases: N/A 8,9: no goroutine starts -->
<!-- cases: N/A 10 compatibility: support may be added when msgin ships a broadcast adapter, as a new requirement -->

### Requirement: Envelopes round-trip through msgin messages
A delivered envelope SHALL equal the sent one in `ID`, `Destination`, `EventType`, `Version`, `ContentType`, `Payload`, `Headers` and `PublishedAt` (at microsecond precision). The msgin message ID SHALL be the envelope ID.

#### Scenario: Field round trip
```gherkin
Given a *Transport with WithEnsureSchema() and a DeliverOnce subscription for group "mail" on "orders.placed"
When the consumer sends Envelope{ID: "0190f3c2-...", Destination: "orders.placed", EventType: "orders.placed", Version: 2, ContentType: "application/json", Payload: {"ID":"1"}, Headers: <headers>, PublishedAt: 2026-01-01T00:00:00.123456Z}
Then the handler receives an envelope with the same field values and Headers <received>

Examples:
  | headers                                   | received                                  |
  | {}                                        | {}                                        |
  | {"traceparent": "00-abc-def-01"}          | {"traceparent": "00-abc-def-01"}          |
  | {"msgin.message-id": "spoof"}             | {"msgin.message-id": "spoof"}             |
```
The third row shows that application headers cannot overwrite msgin's reserved headers: they travel under a namespace and come back unchanged.

<!-- cases: N/A 4 boundaries: payload size limits are msgin's (WithMaxPayloadBytes through WithMsginConsumerOptions) -->
<!-- cases: N/A 7,8: covered by other requirements -->
<!-- cases: N/A 9 panics: a corrupt stored envelope is a permanent error; see "Handler outcomes map to msgin settlement" -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Outbox writes in the caller's transaction
`SendTx` SHALL write the group copies with the `*sql.Tx` of `sqltx.FromDB(ctx, db)` for the transport's database, never committing or rolling it back, and SHALL return that call's error: one matching `doger.ErrNoTransaction` when none is bound or it ended, or `sqltx.ErrForeignTransaction` when it belongs to another `*sql.DB`. Commit SHALL make the copies deliverable; rollback SHALL discard them.

#### Scenario: Commit and rollback
```gherkin
Given a *Transport with WithEnsureSchema() and a DeliverOnce subscription for group "mail" on "orders.placed"
And ctx, tx returned by sqltx.BeginTx(context.Background(), db, nil)
And SendTx(ctx, envelope for "orders.placed") returned nil
When the consumer calls tx.<end>()
Then the "mail" handler is called <calls> times within 5s

Examples:
  | end      | calls |
  | Commit   | 1     |
  | Rollback | 0     |
```

#### Scenario: SendTx without a usable transaction
```gherkin
Given a *Transport with WithEnsureSchema() on database "db"
When the consumer calls SendTx with <ctx> and an envelope for "orders.placed"
Then it returns <error> and nothing is written to "db"

Examples:
  | ctx                                                                 | error                                     |
  | context.Background()                                                | an error matching doger.ErrNoTransaction  |
  | the context from sqltx.BeginTx(context.Background(), otherDB, nil)  | an error matching sqltx.ErrForeignTransaction |
```

#### Scenario: Through doger's outbox strategy
```gherkin
Given a doger root with InjectorOpts.EventTransport set to a *Transport, RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalized()) returned nil, and a second root with a DeliverOnce listener "mail" for OrderPlaced on the same database
When the first root publishes OrderPlaced{ID: "1"} with a context from sqltx.BeginTx and the consumer then calls <end> on that transaction
Then the "mail" listener runs <calls> times within 5s

Examples:
  | end      | calls |
  | Commit   | 1     |
  | Rollback | 0     |
```

<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 6 duplicate: SendTx twice writes two copies per group, as Send does -->
<!-- cases: N/A 8 concurrency: a *sql.Tx is used by one goroutine at a time; documented -->
<!-- cases: N/A 9 panics: driver errors are returned -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: The transport uses the shared sqltx binding
`(*Transport).TxSource()` SHALL return `sqltx.Source()`, so doger resolves the transaction of any `sqltx.Tx` (begun with `sqltx.BeginTx` or `sqltx.InTx`, or bound with `sqltx.BindTx`) for after-commit listeners and the outbox. The binding's own behavior (callbacks, lookup, detach) is specified by the `sqltx` module.

#### Scenario: Doger finds sqltx transactions through the transport
```gherkin
Given a doger root with InjectorOpts.EventTransport set to a *Transport and a function listener "l" for OrderPlaced with ListenerTxPhase(TxPhaseAfterCommit)
When the consumer publishes OrderPlaced{ID: "1"} inside sqltx.InTx(ctx, db, fn) where fn <outcome>
Then "l" ran <ran> times

Examples:
  | outcome                 | ran |
  | returns nil (commits)   | 1   |
  | returns ErrOutOfStock (rolls back) | 0 |
```

#### Scenario: Binding an application transaction
```gherkin
Given a *sql.Tx "raw" begun by the application on "db"
When the consumer calls ctx, _ := sqltx.BindTx(ctx, db, raw) and then SendTx with that context
Then the copies are written with "raw" and become visible only after "raw" commits
```

<!-- cases: N/A 3 zero values: a nil binding is "no transaction", covered by "SendTx without a usable transaction" -->
<!-- cases: N/A 4,6: no numeric or duplicate input -->
<!-- cases: N/A 8,9: owned by the sqltx module's spec -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Handler outcomes map to msgin settlement
A nil handler result SHALL acknowledge the message. An error SHALL be retried with the transport's backoff up to its attempt limit and then moved to the group's dead-letter table. An error satisfying `doger.IsPermanentError`, or a stored message that cannot be decoded, SHALL go to the dead-letter table without retry.

#### Scenario: Settlement outcomes
```gherkin
Given a *Transport with WithEnsureSchema(), WithRetry(msgin.RetryPolicy{MaxAttempts: 3, Backoff: resilience.ExponentialBackoff{Initial: 10*time.Millisecond}}) and a DeliverOnce subscription whose handler <behavior>
When one envelope is sent and the transport goes idle
Then the handler was called <calls> times, the queue table is empty, and the dead-letter table holds <dead> messages

Examples:
  | behavior                                         | calls | dead |
  | returns nil                                      | 1     | 0    |
  | fails once with ErrOutOfStock, then returns nil  | 2     | 0    |
  | returns ErrOutOfStock every time                 | 3     | 1    |
  | returns doger.PermanentError(ErrBadOrder)        | 1     | 1    |
  | panics with "kaboom"                             | 3     | 1    |
```

#### Scenario: Undecodable stored messages are dead-lettered
```gherkin
Given a *Transport with WithEnsureSchema() and a DeliverOnce subscription for group "mail"
When a row whose headers lack the doger envelope fields is inserted directly into the group's queue table
Then the handler is not called and the dead-letter table holds 1 message
```

#### Scenario: A custom dead-letter sink
```gherkin
Given a *Transport with WithDeadLetter(recordingAdapter) and WithRetry(msgin.RetryPolicy{MaxAttempts: 1})
When the handler returns ErrOutOfStock for one envelope
Then recordingAdapter received 1 message with the envelope's ID
```

<!-- cases: N/A 3 zero values: WithRetry with MaxAttempts 0 means retry forever, as in msgin; documented -->
<!-- cases: N/A 4 boundaries: MaxAttempts 1 is the custom-sink row -->
<!-- cases: N/A 8 concurrency: covered by "DeliverOnce members compete" -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Opt-in idempotent consumer
With `WithIdempotentConsumer(businessDB, deduper)`, each delivery SHALL run in a transaction on `businessDB` that records the envelope ID with msgin's `InboxDeduper` and is bound to the handler's context with `sqltx.BindTx`. An ID already recorded SHALL be acknowledged without calling the handler. A handler error SHALL roll the transaction back, so a retry is not seen as a duplicate.

#### Scenario: Duplicates and failures
```gherkin
Given a *Transport with WithEnsureSchema() and WithIdempotentConsumer(db, deduper) and a DeliverOnce subscription whose handler <behavior>
When the same envelope ID is delivered <times> times
Then the handler was called <calls> times and the inbox holds <rows> rows for that ID

Examples:
  | behavior                                         | times | calls | rows |
  | returns nil                                      | 1     | 1     | 1    |
  | returns nil                                      | 2     | 1     | 1    |
  | fails once with ErrOutOfStock, then returns nil  | 1     | 2     | 1    |
```

#### Scenario: Business writes join the dedup transaction
```gherkin
Given a *Transport with WithIdempotentConsumer(db, deduper) and a handler that inserts a row into "orders" using the *sql.Tx from sqltx.From(ctx), then returns ErrOutOfStock
When one envelope is delivered once and the retry limit is 1
Then "orders" holds 0 rows and the inbox holds 0 rows for that ID
```

<!-- cases: N/A 3 zero values: a nil businessDB or deduper fails New with ErrInvalidOption -->
<!-- cases: N/A 4 boundaries: 1 and 2 deliveries are the rows -->
<!-- cases: N/A 8 concurrency: two members claiming the same ID after a lease expiry are serialized by the inbox's unique index -->
<!-- cases: N/A 10 compatibility: off by default -->

### Requirement: Subscriptions stop cleanly
The stop function SHALL cancel the msgin consumer and return after handlers in flight finish or its context ends, leaving no goroutine. Calling it again SHALL return nil. Stopping SHALL NOT remove the group from the registry.

#### Scenario: Stop outcomes
```gherkin
Given a *Transport with WithEnsureSchema() and a DeliverOnce subscription whose handler <handler>
When the consumer calls stop with <ctx>
Then stop returns <result> within 1s, a second stop returns nil, and the registry still holds the group

Examples:
  | handler                                         | ctx                                  | result                                      |
  | is idle                                         | context.Background()                 | nil                                         |
  | is handling an event that finishes after 50ms   | context.Background()                 | nil, after the handler finished             |
  | blocks until its context is done                | a context that times out after 50ms  | an error matching context.DeadlineExceeded  |
```

#### Scenario: Removing a group
```gherkin
Given a *Transport with WithEnsureSchema() and group "mail" registered for "orders.placed" and stopped
When the consumer calls RemoveGroup(ctx, "orders.placed", "mail") and then sends an envelope to "orders.placed"
Then RemoveGroup returns nil and the group's queue table receives no new message
```

<!-- cases: N/A 3,4: no further inputs -->
<!-- cases: N/A 6 duplicate: the second stop is in the first scenario -->
<!-- cases: N/A 9 panics: handler panics are settlement outcomes -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Concurrent use
A `*Transport` SHALL be safe for concurrent `Send`, `SendTx` (each on its own transaction), `Subscribe` and stop calls.

#### Scenario: Concurrent sends and subscriptions
```gherkin
Given a *Transport with WithEnsureSchema() on Postgres
When 20 goroutines send 50 envelopes each to "orders.placed" while 3 goroutines subscribe groups "a", "b" and "c"
Then every call returns nil, every group registered before a send receives that send's event once, and the race detector reports nothing
```

<!-- cases: N/A 1-7,9,10: covered by the other requirements; this one is concurrency only -->

### Requirement: Conformance with doger's transport contract
The transport SHALL pass `eventtransporttest.Run` with the `DeliverAll` subtests declared unsupported and with `WithTx` enabled, on SQLite in every test run and on Postgres in CI.

#### Scenario: Conformance runs
```gherkin
Given the factory that builds a *Transport with WithEnsureSchema() on a fresh <database>
When eventtransporttest.Run(t, factory, eventtransporttest.WithoutDeliverAll(), eventtransporttest.WithTx(harness)) runs
Then every subtest passes with -race and no goroutine leaks

Examples:
  | database                                      |
  | SQLite (modernc.org/sqlite, shared memory)    |
  | Postgres 16 (CI service container)            |
```

<!-- cases: N/A 2-9: the suite owns those cases -->
<!-- cases: N/A 10 compatibility: the suite is the compatibility check against doger -->
