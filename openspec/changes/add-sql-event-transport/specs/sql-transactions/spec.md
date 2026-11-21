# Spec Delta

## Purpose

Lets an application bind a `database/sql` transaction to a context so that doger can find it (as a `doger.TxSource`), run transaction-phase listeners after commit or rollback, and let transports write externalized events in the same transaction, although `*sql.Tx` itself has no commit hooks. Shared by the SQL transport and the watermill and msgin adapters.

## ADDED Requirements

### Requirement: BeginTx binds a new transaction to the context
`sqltx.BeginTx(ctx, db, opts)` SHALL begin a transaction on `db` and return a context holding it and a `*sqltx.Tx` implementing `doger.Tx`, whose `SQL()` returns the `*sql.Tx` and `DB()` returns `db`. A nil `ctx` SHALL mean `context.Background()`; a nil `db` SHALL return an error matching `ErrNilDB`.

#### Scenario: Beginning a transaction
```gherkin
Given a *sql.DB "db" opened with the recording fake driver
When the consumer calls sqltx.BeginTx(<ctx>, <db>, <opts>)
Then it returns <result>

Examples:
  | ctx                          | db  | opts                                             | result                                                                   |
  | context.Background()         | db  | nil                                              | a context in which From finds the returned Tx, a Tx whose DB() is db, and nil |
  | context.Background()         | db  | &sql.TxOptions{Isolation: sql.LevelSerializable} | a Tx begun with LevelSerializable (recorded by the driver), and nil      |
  | an already cancelled context | db  | nil                                              | a nil Tx and an error matching context.Canceled                          |
  | nil                          | db  | nil                                              | a Tx bound to a context derived from context.Background(), and nil       |
  | context.Background()         | nil | nil                                              | a nil Tx and an error matching ErrNilDB                                  |
  | context.Background()         | db, whose driver fails Begin with ErrBeginFailed | nil       | a nil Tx and an error matching ErrBeginFailed                            |
```

<!-- cases: N/A 4,6: no numeric or duplicate input; nested BeginTx starts an independent transaction (covered by "From and Source find only active transactions") -->
<!-- cases: N/A 7 lifecycle: covered by "Commit and rollback run the registered callbacks" -->
<!-- cases: N/A 8 concurrency: each call opens its own transaction -->
<!-- cases: N/A 9 panics: errors are returned -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Commit and rollback run the registered callbacks
`Tx.Commit()` SHALL commit and then run `OnCommit` callbacks in registration order. `Tx.Rollback()`, and a `Commit` whose SQL commit fails, SHALL run `OnRollback` callbacks. Each callback SHALL run once; a panicking callback SHALL be recovered, logged, and SHALL NOT stop the others. A second `Commit` or `Rollback` SHALL return `sql.ErrTxDone` and run nothing.

#### Scenario: Callback outcomes
```gherkin
Given a Tx from sqltx.BeginTx on the recording fake driver, with OnCommit callbacks "c1", "c2" and OnRollback callback "r1", and a recording slog logger as slog.Default()
When the consumer calls <call>
Then it returns <result> and the callbacks ran <ran>

Examples:
  | call                                                   | result                                       | ran           |
  | Commit()                                               | nil                                          | ["c1", "c2"]  |
  | Rollback()                                             | nil                                          | ["r1"]        |
  | Commit() while the driver fails Commit with ErrCommitFailed | an error matching ErrCommitFailed       | ["r1"]        |
  | Commit(), then Commit() again                          | sql.ErrTxDone the second time                | ["c1", "c2"]  |
  | Commit(), then Rollback()                              | sql.ErrTxDone the second time                | ["c1", "c2"]  |
  | Commit() where "c1" panics with "kaboom"               | nil, and the logger recorded "kaboom"        | ["c1", "c2"]  |
```

#### Scenario: A callback registered after the end
```gherkin
Given a Tx that was committed and a recording slog logger as slog.Default()
When the consumer calls OnCommit with callback "late"
Then "late" does not run and the logger recorded that a callback was registered on a finished transaction
```

#### Scenario: Callbacks registered concurrently
```gherkin
Given a Tx from sqltx.BeginTx
When 50 goroutines each register one OnCommit callback and the consumer then calls Commit()
Then 50 callbacks ran once each and the race detector reports nothing
```

<!-- cases: N/A 3 zero values: a nil callback is ignored -->
<!-- cases: N/A 4: no numeric input -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: InTx commits or rolls back around a function
`sqltx.InTx(ctx, db, fn)` SHALL call `fn` with a context holding a new transaction, commit when `fn` returns nil, roll back when it returns an error (returning that error) or panics (re-panicking after the rollback). A commit error SHALL be returned. A nil `fn` SHALL panic with "doger: sqltx: nil InTx function".

#### Scenario: InTx outcomes
```gherkin
Given a *sql.DB on the recording fake driver
When the consumer calls sqltx.InTx(ctx, db, fn) where fn registers OnCommit "c" and OnRollback "r" on From(ctx) and then <fn>
Then InTx <result>, the driver recorded <recorded>, and the callbacks ran <ran>

Examples:
  | fn                                 | result                                        | recorded   | ran     |
  | returns nil                        | returns nil                                   | commit     | ["c"]   |
  | returns ErrOutOfStock              | returns ErrOutOfStock                         | rollback   | ["r"]   |
  | panics with "kaboom"               | panics with "kaboom" after rolling back       | rollback   | ["r"]   |
  | returns nil, with Commit failing with ErrCommitFailed | returns an error matching ErrCommitFailed | commit attempt | ["r"] |
```

#### Scenario: InTx argument errors
```gherkin
Given no transaction
When the consumer calls <call>
Then <outcome>

Examples:
  | call                              | outcome                                            |
  | sqltx.InTx(ctx, nil, fn)          | it returns an error matching ErrNilDB and fn never runs |
  | sqltx.InTx(ctx, db, nil)          | it panics with "doger: sqltx: nil InTx function"   |
```

<!-- cases: N/A 4,6: no numeric or duplicate input -->
<!-- cases: N/A 8 concurrency: as for BeginTx -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: BindTx adopts a transaction begun elsewhere
`sqltx.BindTx(ctx, db, tx)` SHALL return a context holding a `*sqltx.Tx` around `tx` without beginning a transaction; `DB()` SHALL return `db`. Callbacks SHALL run only when the transaction is ended through that `*sqltx.Tx`. A nil `tx` or `db` SHALL panic with "doger: sqltx: nil *sql.Tx" or "doger: sqltx: nil *sql.DB".

#### Scenario: Binding an existing transaction
```gherkin
Given a *sql.Tx "raw" begun on db with the recording fake driver
And ctx2, tx := sqltx.BindTx(ctx, db, raw), with OnCommit callback "c"
When the consumer calls <end>
Then the driver recorded <recorded>, "c" ran <ran> times, and From(ctx2) reports <active>

Examples:
  | end               | recorded  | ran | active |
  | tx.Commit()       | commit    | 1   | false  |
  | tx.Rollback()     | rollback  | 0   | false  |
  | raw.Commit()      | commit    | 0   | true   |
```
The last row is documented misuse: the transaction commits, but the wrapper never learns of it, so callbacks do not run and the context still reports an active transaction.

#### Scenario: BindTx argument panics
```gherkin
Given a *sql.DB "db" and a *sql.Tx "raw"
When the consumer calls <call>
Then it panics with "<message>"

Examples:
  | call                          | message                         |
  | sqltx.BindTx(ctx, db, nil)    | doger: sqltx: nil *sql.Tx       |
  | sqltx.BindTx(ctx, nil, raw)   | doger: sqltx: nil *sql.DB       |
```

<!-- cases: N/A 4,6,8: as for BeginTx -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: From and Source find only active transactions
`sqltx.From(ctx)` SHALL return the `*sqltx.Tx` held by `ctx` and true until it is committed or rolled back, and nil and false otherwise. `sqltx.Source()` SHALL return a comparable `doger.TxSource` whose `Lookup` answers as `From` and whose `Detach` returns a context with the same values, no transaction and no cancellation.

#### Scenario: Lookup and Detach
```gherkin
Given ctx1, tx1 := sqltx.BeginTx(context.WithValue(parent, "trace", "t1"), db, nil), where parent is cancellable
When the consumer evaluates <expression>
Then the result is <result>

Examples:
  | expression                                                     | result      |
  | From(ctx1) before tx1 ends                                     | tx1, true   |
  | From(ctx1) after tx1.Commit()                                  | nil, false  |
  | From(ctx1) after tx1.Rollback()                                | nil, false  |
  | From(context.Background())                                     | nil, false  |
  | From(nil)                                                      | nil, false  |
  | Source().Lookup(ctx1) before tx1 ends                          | tx1, true   |
  | Source().Lookup(Source().Detach(ctx1)) before tx1 ends         | nil, false  |
  | Source().Detach(ctx1).Value("trace")                           | "t1"        |
  | Source().Detach(ctx1).Err() after parent was cancelled         | nil         |
  | From(ctx2) where ctx2, tx2 := BeginTx(ctx1, db, nil)           | tx2, true   |
  | Source() == Source()                                           | true        |
```

<!-- cases: N/A 4,6: no numeric or duplicate input -->
<!-- cases: N/A 5 errors: From, Lookup and Detach cannot fail -->
<!-- cases: N/A 8 concurrency: lookups read immutable context values and an atomic ended flag; covered by the race run of "Callbacks registered concurrently" -->
<!-- cases: N/A 9 panics: a nil context is treated as context.Background() -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: FromDB checks the transaction's database
`sqltx.FromDB(ctx, db)` SHALL return the active `*sqltx.Tx` held by `ctx` and nil when its `DB()` is `db`; nil and an error matching `doger.ErrNoTransaction` when `ctx` holds no active transaction; and nil and an error matching `sqltx.ErrForeignTransaction` (message prefixed "doger: sqltx: ") when the transaction belongs to another `*sql.DB`.

#### Scenario: FromDB outcomes
```gherkin
Given *sql.DB values DB1 and DB2 on the recording fake driver
When the consumer calls sqltx.FromDB(<ctx>, DB1)
Then it returns <result>

Examples:
  | ctx                                                         | result                                                                 |
  | ctx1 from sqltx.BeginTx(ctx, DB1, nil), before it ends      | the Tx of ctx1 and nil                                                 |
  | ctx1 from sqltx.BindTx(ctx, DB1, raw), before it ends       | the Tx of ctx1 and nil                                                 |
  | context.Background()                                        | nil and an error matching doger.ErrNoTransaction                       |
  | nil                                                         | nil and an error matching doger.ErrNoTransaction                       |
  | ctx1 from sqltx.BeginTx(ctx, DB1, nil), after Commit()      | nil and an error matching doger.ErrNoTransaction                       |
  | ctx2 from sqltx.BeginTx(ctx, DB2, nil), before it ends      | nil and an error matching sqltx.ErrForeignTransaction, whose message starts with "doger: sqltx: " |
```

<!-- cases: N/A 2 overrides: no options -->
<!-- cases: N/A 4,6: no numeric or duplicate input -->
<!-- cases: N/A 7 lifecycle: the ended row covers it -->
<!-- cases: N/A 8 concurrency: reads immutable context values and an atomic ended flag, as From -->
<!-- cases: N/A 9 panics: never panics; a nil db only ever matches no transaction and reports ErrForeignTransaction for a bound one -->
<!-- cases: N/A 10 compatibility: new module; replaces the per-adapter errors the watermill and msgin plans had -->

### Requirement: Transaction-phase listeners work with sqltx
A doger root with `InjectorOpts.TxSource = sqltx.Source()` SHALL run `ListenerTxPhase(TxPhaseAfterCommit)` listeners after `Tx.Commit()` and `TxPhaseAfterRollback` listeners after `Tx.Rollback()` for events published with the bound context.

#### Scenario: Deferred listeners through sqltx
```gherkin
Given a doger root with InjectorOpts.TxSource = sqltx.Source(), and function listeners "a" with ListenerTxPhase(TxPhaseAfterCommit) and "r" with ListenerTxPhase(TxPhaseAfterRollback) for OrderPlaced
And ctx1, tx := sqltx.BeginTx(ctx, db, nil) on the recording fake driver, and the consumer published OrderPlaced{ID: "1"} with ctx1
When the consumer calls <end>
Then "a" ran <a> times and "r" ran <r> times

Examples:
  | end            | a | r |
  | tx.Commit()    | 1 | 0 |
  | tx.Rollback()  | 0 | 1 |
```

<!-- cases: N/A 2-9: the deferred-listener contract itself belongs to add-event-transport's transactional-events; this pins the integration only -->
<!-- cases: N/A 10 compatibility: new module -->
