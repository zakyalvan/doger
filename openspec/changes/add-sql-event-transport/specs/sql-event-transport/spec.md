# Spec Delta

## Purpose

Delivers doger events between processes through a PostgreSQL or MySQL database: one copy per group, competing members within a group, crash recovery through leases, bounded retries with backoff, dead letters an operator can inspect and resubmit, and cleanup of ephemeral groups and delivered rows.

## ADDED Requirements

### Requirement: The transport honors the doger transport contract
`*eventsql.Transport` SHALL implement `doger.EventTransport` and `doger.TxEventSender`, and SHALL pass `eventtransporttest.Run` with `WithTx` on PostgreSQL and MySQL, with `-race` and no goroutine left after every subscription stopped.

#### Scenario: Conformance on each engine
```gherkin
Given an empty <engine> database prepared with EnsureSchema
And a factory returning eventsql.New(db, <dialect>, PollInterval(20ms), Backoff(eventsql.FixedBackoff(10ms)))
When eventtransporttest.Run(t, factory, eventtransporttest.WithTx(harness)) runs, where the harness uses sqltx.BeginTx on the same *sql.DB
Then every subtest passes and goleak reports no goroutine

Examples:
  | engine        | dialect             |
  | PostgreSQL 17 | postgres.Dialect()  |
  | PostgreSQL 13 | postgres.Dialect()  |
  | MySQL 8.4     | mysql.Dialect()     |
  | MySQL 8.0     | mysql.Dialect()     |
```

<!-- cases: N/A 2-9: each contract case is a subtest of the suite; the other requirements cover SQL-specific behavior -->
<!-- cases: N/A 10 compatibility: the oldest supported engine versions are rows -->

### Requirement: Send writes one delivery per registered group
`Send` SHALL store the envelope and one pending delivery for each group registered for its destination at the time of the write, in one transaction. With no group registered it SHALL store nothing and return nil. A payload over `MaxPayloadBytes` SHALL fail with an error matching `ErrPayloadTooLarge` for which `doger.IsPermanentError` is true.

#### Scenario: Deliveries per group
```gherkin
Given a PostgreSQL 17 database with groups <groups> registered for "orders.placed"
When the consumer sends an envelope with ID "e1" and a <size> payload to "orders.placed"
Then Send returns <result> and the database holds <deliveries>

Examples:
  | groups                     | size     | result                                                    | deliveries                                   |
  | "mail"                     | 100 B    | nil                                                       | 1 pending delivery of "e1" for "mail"        |
  | "mail", "cache/pod-0"      | 100 B    | nil                                                       | 2 pending deliveries of "e1"                 |
  | none                       | 100 B    | nil                                                       | no event and no delivery                     |
  | "mail"                     | 1 MiB    | nil                                                       | 1 pending delivery of "e1" for "mail"        |
  | "mail"                     | 1 MiB + 1 B | an error matching ErrPayloadTooLarge, permanent        | no event and no delivery                     |
```

#### Scenario: Send failures are returned
```gherkin
Given a transport whose database <condition>
When the consumer sends an envelope to "orders.placed"
Then Send returns <result>

Examples:
  | condition                                     | result                                      |
  | is unreachable                                | an error wrapping the driver's error        |
  | is reachable, with a context already cancelled | an error matching context.Canceled         |
  | lacks the doger_events table                  | an error wrapping the driver's error        |
```

<!-- cases: N/A 2 overrides: MaxPayloadBytes is the only option; rows 4-5 are its boundary -->
<!-- cases: N/A 6 duplicate: a repeated envelope ID is stored again; deduplication is the listener's job (add-event-transport) -->
<!-- cases: N/A 8 concurrency: covered by "Members of a group compete" -->
<!-- cases: N/A 9 panics: database errors are returned -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Groups start at the latest event and keep waiting events
`Subscribe` SHALL register its group for the destination, idempotently. A delivery SHALL exist for a group when its registration committed before the send's write ran. A non-ephemeral group SHALL stay registered after its subscription stops, so events sent while no member runs are delivered when one subscribes again.

#### Scenario: Registration and history
```gherkin
Given a PostgreSQL 17 database prepared with EnsureSchema
And <before>
When a subscription for group "mail" on "orders.placed" starts and the consumer sends "e3"
Then the subscription handles <handled>

Examples:
  | before                                                                                     | handled          |
  | "e1" and "e2" were sent and "mail" never subscribed                                         | "e3" only        |
  | "mail" subscribed and stopped, then "e1" and "e2" were sent                                 | "e1", "e2", "e3" |
  | "cache/x" (Ephemeral) subscribed and stopped, then "e1" was sent, now subscribing as "cache/x" | "e3" only     |
```

#### Scenario: Registration racing a send
```gherkin
Given a PostgreSQL 17 database with no group for "orders.placed"
And a transaction T1 that sent "e1" and has not committed
When group "mail" registers and commits, then T1 commits, then "e2" is sent
Then "mail" handles "e2" and does not handle "e1"
```

<!-- cases: N/A 3,4: no zero or numeric input -->
<!-- cases: N/A 5 errors: a failing registration fails Subscribe (covered by the conformance suite) -->
<!-- cases: N/A 8 concurrency: the race row is the concurrency case -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: N/A 10 compatibility: matches add-event-transport's "new group starts at the latest event" -->

### Requirement: Members of a group compete
Each delivery SHALL be handled by one member of its group at a time. Members of a `DeliverOnce` group in different processes SHALL share the deliveries, and each `DeliverAll` group SHALL receive every event.

#### Scenario: Two instances
```gherkin
Given two transports "i1" and "i2" on the same PostgreSQL 17 database, each subscribed with groups "mail" (DeliverOnce) and "cache/<instance>" (DeliverAll)
When 200 events are sent to "orders.placed"
Then "mail" handled 200 distinct events across "i1" and "i2", each exactly once
And "cache/i1" and "cache/i2" each handled 200 events
And no event was handled by two members at the same time
```

#### Scenario: Workers bound handler concurrency
```gherkin
Given a transport with Workers(<workers>) and BatchSize(16), subscribed with a handler that records how many calls run at once and takes 20ms
When 64 events are sent
Then at most <workers> calls ran at once and all 64 were handled

Examples:
  | workers |
  | 1       |
  | 4       |
  | 16      |
```

<!-- cases: N/A 3,4: workers bounds are rows; BatchSize bounds are validated by New -->
<!-- cases: N/A 5,6,9: covered by the retry and lease requirements -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Leases recover crashed and slow handlers
A claim SHALL lease a delivery for `Lease(d)` and count an attempt. The handler's context SHALL end when the lease ends. A delivery whose lease expired SHALL be claimable again. An acknowledgement after the lease was lost SHALL be ignored and logged as `ErrLeaseLost`.

#### Scenario: Lease outcomes
```gherkin
Given a transport "i1" with Lease(200ms) and a recording logger, and a transport "i2" with the same group "mail"
And one pending delivery of "e1" for "mail"
When <situation>
Then <outcome>

Examples:
  | situation                                                                  | outcome                                                                      |
  | "i1" claims "e1" and its process stops without acknowledging               | "i2" handles "e1" after the lease expires, with attempt 2                    |
  | "i1" claims "e1" and its handler runs for 1s                               | the handler's context ends after 200ms with context.DeadlineExceeded; "i2" handles "e1"; "i1"'s late acknowledgement is logged as ErrLeaseLost and does not remove "i2"'s claim |
  | "i1" claims "e1" and its handler returns nil after 50ms                    | "e1" is removed and "i2" never handles it                                    |
```

#### Scenario: Attempts are visible to the handler
```gherkin
Given a transport with MaxAttempts(5) and Backoff(FixedBackoff(10ms)), and a handler that fails twice with ErrOutOfStock and then succeeds while recording eventsql.AttemptFromContext(ctx)
When one event is sent
Then the handler recorded attempts [1, 2, 3]
```

<!-- cases: N/A 3,4: lease bounds are validated by New -->
<!-- cases: N/A 6 duplicate: duplicates after a lease loss are expected at-least-once behavior -->
<!-- cases: N/A 9 panics: handler panics are recovered by doger before they reach the transport -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Retries, backoff and dead letters
A handler error SHALL make the delivery visible again after `Backoff` for the attempt. After `MaxAttempts` attempts, or at once for an error where `doger.IsPermanentError` is true, the delivery SHALL be dead-lettered with the error text and not claimed again.

#### Scenario: Retry outcomes
```gherkin
Given a transport with MaxAttempts(<max>) and Backoff(FixedBackoff(10ms)), and a handler that <behavior>
When one event is sent and the transport goes idle
Then the handler ran <runs> times and the delivery is <state>

Examples:
  | max | behavior                                            | runs | state                                                   |
  | 10  | returns nil                                         | 1    | removed                                                 |
  | 3   | returns ErrOutOfStock every time                    | 3    | dead-lettered with last error "out of stock"           |
  | 1   | returns ErrOutOfStock every time                    | 1    | dead-lettered                                           |
  | 10  | returns doger.PermanentError(ErrBadOrder)           | 1    | dead-lettered with last error containing "bad order"   |
  | 10  | fails twice with ErrOutOfStock, then returns nil    | 3    | removed                                                 |
```

#### Scenario: The default backoff
```gherkin
Given the default Backoff policy
When the consumer asks for the delay of attempt <attempt>
Then it is within <range>

Examples:
  | attempt | range              |
  | 1       | 0.9s to 1.1s       |
  | 2       | 1.8s to 2.2s       |
  | 5       | 14.4s to 17.6s     |
  | 20      | 4m30s to 5m30s     |
```

<!-- cases: N/A 3 zero values: MaxAttempts(0) is rejected by New -->
<!-- cases: N/A 6,7: covered by "Dead letters can be inspected and resubmitted" -->
<!-- cases: N/A 8 concurrency: each delivery is retried by one member at a time (lease) -->
<!-- cases: N/A 9 panics: a panicking Backoff policy is a programming error; the delivery's claim expires and it is retried -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Dead letters can be inspected and resubmitted
`ListDeadLetters(ctx, destination, group, limit)` SHALL return up to `limit` dead letters, oldest first, with envelope, attempts and last error. `Resubmit(ctx, ids...)` SHALL make them pending again with their attempts reset. `DropGroup(ctx, destination, group)` SHALL unregister a group and delete its deliveries.

#### Scenario: Admin operations
```gherkin
Given a PostgreSQL 17 database where group "mail" on "orders.placed" has dead letters "d1" and "d2" and 1 pending delivery
When the consumer calls <call>
Then <outcome>

Examples:
  | call                                                   | outcome                                                                  |
  | ListDeadLetters(ctx, "orders.placed", "mail", 10)      | it returns "d1" then "d2" with their attempts and last errors            |
  | ListDeadLetters(ctx, "orders.placed", "mail", 1)       | it returns "d1" only                                                     |
  | ListDeadLetters(ctx, "orders.placed", "other", 10)     | it returns an empty list and nil                                         |
  | ListDeadLetters(ctx, "orders.placed", "mail", 0)       | it returns an error matching ErrInvalidOption                            |
  | Resubmit(ctx, "d1") with a subscribed "mail" member    | the member handles "d1" with attempt 1, and "d2" stays dead              |
  | Resubmit(ctx, "missing")                               | it returns nil and changes nothing                                       |
  | DropGroup(ctx, "orders.placed", "mail")                | the group, its 2 dead letters and its pending delivery are deleted, and a later send creates no delivery for "mail" |
```

<!-- cases: N/A 3 zero values: limit 0 is a row; an empty ID list is a no-op like "missing" -->
<!-- cases: N/A 8 concurrency: Resubmit of a delivery being dropped resolves by the database row lock; no extra behavior -->
<!-- cases: N/A 9 panics: database errors are returned -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Ephemeral groups are cleaned up
An `Ephemeral` subscription SHALL unregister its group and delete its deliveries when it stops. Every subscription SHALL heartbeat its group while running; an ephemeral group without a heartbeat for `EphemeralTTL` SHALL be removed by any running subscription. Non-ephemeral groups SHALL never be removed automatically.

#### Scenario: Ephemeral group lifecycle
```gherkin
Given a PostgreSQL 17 database and a transport with EphemeralTTL(1s) and PollInterval(50ms)
And <setup>
When 2s pass while a subscription for group "mail" on "orders.placed" runs
Then <outcome>

Examples:
  | setup                                                                                   | outcome                                             |
  | an Ephemeral subscription "cache/a" that was stopped normally                           | "cache/a" was removed at stop, with its deliveries  |
  | an Ephemeral group "cache/b" with 3 pending deliveries whose process crashed            | "cache/b" and its deliveries are removed            |
  | a non-ephemeral group "audit" with 3 pending deliveries and no running member           | "audit" and its deliveries are kept                 |
  | an Ephemeral subscription "cache/c" still running                                       | "cache/c" is kept                                   |
```

<!-- cases: N/A 3,4: EphemeralTTL bounds are validated by New -->
<!-- cases: N/A 5 errors: a failing cleanup is logged and retried on the next poll -->
<!-- cases: N/A 8 concurrency: several members may clean the same group; deletes are idempotent -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Stopping a subscription releases its claims
The stop function SHALL stop claiming, wait for running handlers until its context ends, and release the leases of deliveries it claimed but did not start, so other members can claim them at once. After every subscription of a transport stopped, the transport SHALL run no goroutine.

#### Scenario: Stop outcomes
```gherkin
Given two transports "i1" and "i2" with group "mail", Lease(30s), BatchSize(8), Workers(1), and a handler taking 100ms
And "i1" has claimed 8 deliveries and is running the first
When "i1"'s stop is called with <ctx>
Then <outcome>

Examples:
  | ctx                                 | outcome                                                                                       |
  | context.Background()                | stop returns after the running handler finished; "i2" handles the other 7 within 1s, not after 30s |
  | a context that times out after 10ms | stop returns within 1s; the running handler's context was cancelled; "i2" handles all 8 within 1s once the lease is released |
```

#### Scenario: No goroutine after stop
```gherkin
Given a transport with 3 subscriptions
When every stop function returned
Then goleak reports no goroutine started by the transport
```

<!-- cases: N/A 3,4,5: no further inputs; a failing release is logged and the lease expires instead -->
<!-- cases: N/A 6 duplicate: calling stop twice returns nil the second time (conformance suite) -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Delivered rows are removed
An acknowledged delivery SHALL be deleted at once unless `KeepDelivered(d)` is set, in which case it SHALL be marked delivered and deleted after `d`. An event with no remaining delivery SHALL be deleted.

#### Scenario: Retention
```gherkin
Given a transport with <option> and group "mail"
When one event is sent, handled successfully, and 2s pass with PollInterval(50ms)
Then the database holds <rows>

Examples:
  | option               | rows                                      |
  | no option            | no event and no delivery                  |
  | KeepDelivered(1h)    | the event and 1 delivered delivery        |
  | KeepDelivered(1s)    | no event and no delivery                  |
```

<!-- cases: N/A 3,4: KeepDelivered(0) means the default (row 1) -->
<!-- cases: N/A 5,8,9: cleanup errors are logged and retried on the next poll -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: SendTx writes in the bound transaction
`SendTx(ctx, env)` SHALL write the event and its deliveries with the transaction returned by `sqltx.FromDB(ctx, db)` for the transport's `*sql.DB`, so they commit or roll back with it, and SHALL return `FromDB`'s error unchanged: matching `doger.ErrNoTransaction` or `sqltx.ErrForeignTransaction`. `TxSource()` SHALL return `sqltx.Source()`.

#### Scenario: Outbox writes
```gherkin
Given transports "a" on database DB1 and "b" on database DB2, and group "mail" subscribed on "a"
When the consumer calls a.SendTx(<ctx>, envelope "e1") and then <end>
Then SendTx returns <result> and "mail" handles <handled>

Examples:
  | ctx                                                   | end                     | result                                       | handled |
  | a context from sqltx.BeginTx(ctx, DB1, nil)           | commits the Tx          | nil                                          | "e1"    |
  | a context from sqltx.BeginTx(ctx, DB1, nil)           | rolls back the Tx       | nil                                          | nothing |
  | context.Background()                                  | does nothing            | an error matching doger.ErrNoTransaction     | nothing |
  | a context whose Tx was already committed              | does nothing            | an error matching doger.ErrNoTransaction     | nothing |
  | a context from sqltx.BeginTx(ctx, DB2, nil)           | commits the Tx          | an error matching sqltx.ErrForeignTransaction | nothing |
  | a context from sqltx.BindTx(ctx, DB1, raw on DB1)     | commits the Tx          | nil                                          | "e1"    |
```

#### Scenario: The outbox through doger
```gherkin
Given a doger root with InjectorOpts.EventTransport = transport "a" (so its TxSource is sqltx.Source()), RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalized()) and a function listener "after" with ListenerTxPhase(TxPhaseAfterCommit)
And a second root on the same database with a remote listener "mail" (DeliverOnce), started
When the consumer calls sqltx.InTx(ctx, DB1, fn) where fn publishes OrderPlaced{ID: "1"} with its context and returns <fn>
Then "mail" handles OrderPlaced{ID: "1"} <mail> and "after" ran <after>

Examples:
  | fn                    | mail          | after        |
  | nil                   | once          | once, after the commit |
  | ErrOutOfStock         | never         | never        |
```

#### Scenario: The same TxSource configured twice is not a conflict
```gherkin
Given a doger root with InjectorOpts.TxSource = sqltx.Source() and InjectorOpts.EventTransport = transport "a"
When the consumer calls Start
Then it returns nil
```

<!-- cases: N/A 3,4,6: no zero, numeric or duplicate input beyond the rows -->
<!-- cases: N/A 8 concurrency: SendTx uses the caller's transaction on the caller's goroutine -->
<!-- cases: N/A 9 panics: database errors are returned -->
<!-- cases: N/A 10 compatibility: new module -->
