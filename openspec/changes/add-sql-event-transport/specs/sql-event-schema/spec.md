# Spec Delta

## Purpose

Lets a consumer construct the SQL event transport for PostgreSQL or MySQL with validated arguments and table names, and own the schema through reference DDL for their migration tool, with an optional idempotent schema setup for development and tests.

## ADDED Requirements

### Requirement: New validates its arguments
`eventsql.New(db, dialect, opts...)` SHALL return an error and a nil transport when `db` is nil (`ErrNilDB`), `dialect` is nil (`ErrNilDialect`), or an option is invalid (`ErrInvalidOption`, naming the option). It SHALL NOT contact the database.

#### Scenario: Constructor arguments
```gherkin
Given a *sql.DB "db" opened with a driver that records every connection attempt
When the consumer calls eventsql.New(<db>, <dialect>, <options>)
Then it returns <result>
And the driver recorded 0 connection attempts

Examples:
  | db  | dialect             | options                    | result                                                 |
  | db  | postgres.Dialect()  | no options                 | a transport and nil                                    |
  | db  | mysql.Dialect()     | no options                 | a transport and nil                                    |
  | nil | postgres.Dialect()  | no options                 | nil and an error matching ErrNilDB                     |
  | db  | nil                 | no options                 | nil and an error matching ErrNilDialect                |
  | db  | postgres.Dialect()  | BatchSize(0)               | nil and an error matching ErrInvalidOption naming "BatchSize" |
  | db  | postgres.Dialect()  | Workers(0)                 | nil and an error matching ErrInvalidOption naming "Workers" |
  | db  | postgres.Dialect()  | Lease(0)                   | nil and an error matching ErrInvalidOption naming "Lease" |
  | db  | postgres.Dialect()  | PollInterval(-1s)          | nil and an error matching ErrInvalidOption naming "PollInterval" |
  | db  | postgres.Dialect()  | MaxAttempts(0)             | nil and an error matching ErrInvalidOption naming "MaxAttempts" |
  | db  | postgres.Dialect()  | MaxPayloadBytes(0)         | nil and an error matching ErrInvalidOption naming "MaxPayloadBytes" |
  | db  | postgres.Dialect()  | EphemeralTTL(0)            | nil and an error matching ErrInvalidOption naming "EphemeralTTL" |
  | db  | postgres.Dialect()  | BatchSize(1), Workers(1)   | a transport and nil                                    |
  | db  | postgres.Dialect()  | Logger(nil)                | nil and an error matching ErrInvalidOption naming "Logger" |
```

<!-- cases: N/A 6 duplicate: a repeated option keeps the last value, like doger's options; covered by the BatchSize(1), Workers(1) row shape -->
<!-- cases: N/A 7 lifecycle: New has no lifecycle; it starts nothing -->
<!-- cases: N/A 8 concurrency: New shares no state -->
<!-- cases: N/A 9 panics: New returns errors and never panics -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Table names are validated
Table names SHALL be `TablePrefix(p)` (default "doger_") followed by "events", "event_deliveries" and "event_groups". The prefix SHALL match `^([A-Za-z_][A-Za-z0-9_]*\.)?[A-Za-z_][A-Za-z0-9_]*$`, be at most 40 bytes, and otherwise make `New` fail with `ErrInvalidTableName`.

#### Scenario: Prefix validation
```gherkin
Given a *sql.DB "db"
When the consumer calls eventsql.New(db, postgres.Dialect(), <option>)
Then it returns <result>

Examples:
  | option                                   | result                                                      |
  | no option                                | a transport using doger_events, doger_event_deliveries, doger_event_groups |
  | TablePrefix("shop_")                     | a transport using shop_events, shop_event_deliveries, shop_event_groups |
  | TablePrefix("app.doger_")                | a transport using app.doger_events, app.doger_event_deliveries, app.doger_event_groups |
  | TablePrefix("")                          | nil and an error matching ErrInvalidTableName               |
  | TablePrefix("bad-name")                  | nil and an error matching ErrInvalidTableName               |
  | TablePrefix("x; DROP TABLE users; --")   | nil and an error matching ErrInvalidTableName               |
  | TablePrefix("a.b.c_")                    | nil and an error matching ErrInvalidTableName               |
  | TablePrefix(40 times "a")                | a transport                                                 |
  | TablePrefix(41 times "a")                | nil and an error matching ErrInvalidTableName               |
```

<!-- cases: N/A 6 duplicate: last prefix wins -->
<!-- cases: N/A 7,8,9: validated once in New, no shared state, no panics -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Reference DDL for migrations
`postgres.DDL(prefix)` and `mysql.DDL(prefix)` SHALL return the statements that create the three tables and their indexes for that prefix, or an error matching `eventsql.ErrInvalidTableName` for an invalid prefix. Applying them to an empty database SHALL give a schema on which the transport passes the conformance suite.

#### Scenario: DDL output
```gherkin
Given a dialect package <pkg>
When the consumer calls <pkg>.DDL(<prefix>)
Then it returns <result>

Examples:
  | pkg      | prefix     | result                                                                                         |
  | postgres | "doger_"   | statements creating doger_events, doger_event_deliveries and doger_event_groups, and nil       |
  | mysql    | "doger_"   | statements creating doger_events, doger_event_deliveries and doger_event_groups, and nil       |
  | postgres | "bad-name" | nil and an error matching eventsql.ErrInvalidTableName                                         |
```

#### Scenario: DDL applied by a migration tool
```gherkin
Given an empty <engine> database
And every statement of <pkg>.DDL("doger_") was executed in order
When eventtransporttest.Run runs against eventsql.New(db, <pkg>.Dialect())
Then every subtest passes

Examples:
  | engine          | pkg      |
  | PostgreSQL 17   | postgres |
  | MySQL 8.4       | mysql    |
```

<!-- cases: N/A 3 zero values: an empty prefix is invalid (row 3 of DDL output shape) -->
<!-- cases: N/A 7,8,9: pure functions -->
<!-- cases: N/A 10 compatibility: new module; later schema changes will ship as new DDL versions with an upgrading note -->

### Requirement: EnsureSchema creates missing tables
`(*Transport).EnsureSchema(ctx)` SHALL create any missing table and index and leave existing ones and their rows unchanged, under a session lock it SHALL release afterwards, discarding the connection when the release fails. `(*Transport).CheckSchema(ctx)` SHALL return nil when the three tables exist and an error matching `ErrSchemaMissing` naming the missing tables otherwise.

#### Scenario: EnsureSchema and CheckSchema
```gherkin
Given a <engine> database in state <state>
When the consumer calls <call> on a transport with prefix "doger_"
Then it returns <result>

Examples:
  | engine        | state                                                | call           | result                                                        |
  | PostgreSQL 17 | empty                                                | CheckSchema    | an error matching ErrSchemaMissing naming doger_events, doger_event_deliveries, doger_event_groups |
  | PostgreSQL 17 | empty                                                | EnsureSchema   | nil, and CheckSchema then returns nil                         |
  | PostgreSQL 17 | after EnsureSchema, with 3 pending deliveries        | EnsureSchema   | nil, and the 3 deliveries are still pending                   |
  | MySQL 8.4     | empty                                                | EnsureSchema   | nil, and CheckSchema then returns nil                         |
  | MySQL 8.4     | only doger_events exists                             | CheckSchema    | an error matching ErrSchemaMissing naming doger_event_deliveries, doger_event_groups |
  | PostgreSQL 17 | empty, with a context cancelled before the call      | EnsureSchema   | an error matching context.Canceled                            |
```

#### Scenario: Concurrent EnsureSchema
```gherkin
Given an empty PostgreSQL 17 database
When 5 transports call EnsureSchema concurrently
Then every call returns nil and CheckSchema returns nil
```

#### Scenario: Releasing the schema lock
```gherkin
Given a transport on a fake driver whose schema lock statement returns 1
When the consumer calls EnsureSchema and the unlock statement <unlock>
Then EnsureSchema returns nil and the connection <connection>

Examples:
  | unlock                 | connection                                              |
  | returns 1              | goes back to the pool                                   |
  | fails with an error    | is closed instead of pooled, so its session lock ends   |
  | returns 0              | is closed instead of pooled, so its session lock ends   |
```

<!-- cases: N/A 4 boundaries: no numeric input -->
<!-- cases: N/A 9 panics: database errors are returned -->
<!-- cases: N/A 10 compatibility: new module -->
