# Spec Delta

## Purpose

Lets listeners run at a chosen point of the publisher's transaction (immediately, after commit, after rollback or after completion) without core knowing the transaction type, with detached contexts and well-defined error routing once the transaction has ended.

## ADDED Requirements

### Requirement: Listeners run in their transaction phase
When the resolved `TxSource` finds a transaction in the publish context, a listener SHALL run per its `ListenerTxPhase`: `TxPhaseImmediate` during `Publish`; `TxPhaseAfterCommit` from `OnCommit`; `TxPhaseAfterRollback` from `OnRollback`; `TxPhaseAfterCompletion` from either. Without the option, a listener's phase SHALL be `TxPhaseImmediate`.

#### Scenario: Phases with a bound transaction
```gherkin
Given a root scope whose TxSource is a fake that finds transaction "tx1" in contexts marked with it
And a function listener "l" for OrderPlaced with <option>
And the consumer published OrderPlaced{ID: "1"} with a context marked with "tx1"
When the fake transaction "tx1" <outcome>
Then "l" ran <when>

Examples:
  | option                                      | outcome     | when                       |
  | no ListenerTxPhase                          | commits     | during Publish, once       |
  | ListenerTxPhase(TxPhaseImmediate)           | commits     | during Publish, once       |
  | ListenerTxPhase(TxPhaseAfterCommit)         | commits     | after commit, once         |
  | ListenerTxPhase(TxPhaseAfterCommit)         | rolls back  | never                      |
  | ListenerTxPhase(TxPhaseAfterRollback)       | rolls back  | after rollback, once       |
  | ListenerTxPhase(TxPhaseAfterRollback)       | commits     | never                      |
  | ListenerTxPhase(TxPhaseAfterCompletion)     | commits     | after commit, once         |
  | ListenerTxPhase(TxPhaseAfterCompletion)     | rolls back  | after rollback, once       |
  | ListenerTxPhase(TxPhaseAfterCommit), ListenerAsync() | commits | on the executor after commit, once |
```

#### Scenario: Deferred listeners keep listener order
```gherkin
Given a root scope with a fake TxSource and function listeners "a" (order 1) and "b" (order 0) for OrderPlaced, both with ListenerTxPhase(TxPhaseAfterCommit)
And the consumer published OrderPlaced{ID: "1"} with a context marked with "tx1"
When the fake transaction "tx1" commits
Then the listeners ran in the order ["b", "a"]
```

<!-- cases: N/A 3 zero values: the zero TxPhase is TxPhaseImmediate (first row) -->
<!-- cases: N/A 4 boundaries: no numeric input -->
<!-- cases: N/A 6 duplicate: the last ListenerTxPhase wins, as for other listener options; covered by "Transaction options are validated" -->
<!-- cases: N/A 8 concurrency: callbacks run where the Tx implementation calls them; covered by "Deferred listeners run detached" -->
<!-- cases: N/A 10 compatibility: listeners without the option behave as in add-eventing (first row) -->

### Requirement: Deferred listeners run detached
A deferred listener SHALL receive `TxSource.Detach(publishCtx)`: it SHALL keep the publisher context's values, SHALL NOT be cancelled with the publisher's context, and the `TxSource` SHALL NOT find a transaction in it. It SHALL run on the goroutine that calls the `Tx` callback, or on the executor when it is async.

#### Scenario: Context of a deferred listener
```gherkin
Given a root scope with a fake TxSource and a function listener "l" for OrderPlaced with ListenerTxPhase(TxPhaseAfterCommit) that records its context
And the consumer published OrderPlaced{ID: "1"} with a context marked with "tx1" and holding "trace"="t1", then cancelled that context
When the fake transaction "tx1" commits on goroutine "g2"
Then "l" ran on goroutine "g2" with value "trace"="t1", a context whose Err is nil, and TxSource.Lookup on it returning false
```

<!-- cases: N/A 3,4,6: no input values -->
<!-- cases: N/A 5 errors: covered by "Deferred listener errors go to HookEventError" -->
<!-- cases: N/A 9 panics: covered by "Deferred listener errors go to HookEventError" -->
<!-- cases: N/A 10 compatibility: new behavior -->

### Requirement: Deferred listener errors go to HookEventError
Errors and panics of deferred listeners SHALL be reported to `HookEventError` as `*ListenerError`, SHALL NOT propagate into the `Tx` callback, and SHALL NOT stop later deferred listeners of the same publish. `Publish` SHALL return nil for listeners it deferred.

#### Scenario: Deferred failures are reported
```gherkin
Given a root scope with a fake TxSource and a recording HookEventError
And function listeners "a" and "b" for OrderPlaced with ListenerTxPhase(TxPhaseAfterCommit), where "a" <behavior>
And the consumer published OrderPlaced{ID: "1"} with a context marked with "tx1", and Publish returned nil
When the fake transaction "tx1" commits
Then "b" ran, the commit callback returned normally, and the hook received a *ListenerError for "a" where <match>

Examples:
  | behavior              | match                             |
  | returns ErrOutOfStock | the error matches ErrOutOfStock   |
  | panics with "kaboom"  | the error message contains "kaboom" |
```

<!-- cases: N/A 3,4,6: no input values -->
<!-- cases: N/A 8 concurrency: each callback runs its listeners sequentially -->
<!-- cases: N/A 10 compatibility: HookEventError keeps its add-eventing contract -->

### Requirement: Fallback when no transaction is bound
When no transaction is found, a listener with a deferred phase SHALL run immediately during `Publish` under `TxFallbackRun` (the default) and SHALL be skipped under `ListenerTxFallback(TxFallbackSkip)`. A `TxSource` whose transaction has ended SHALL report no transaction, so the same rule applies.

#### Scenario: No transaction or an ended one
```gherkin
Given a root scope whose TxSource <source>
And a function listener "l" for OrderPlaced with ListenerTxPhase(<phase>) and <fallback>
When the consumer publishes OrderPlaced{ID: "1"}
Then "l" ran <ran> times during Publish

Examples:
  | source                                           | phase                  | fallback                         | ran |
  | is unset                                         | TxPhaseAfterCommit     | no ListenerTxFallback            | 1   |
  | is unset                                         | TxPhaseAfterCommit     | ListenerTxFallback(TxFallbackSkip) | 0 |
  | is unset                                         | TxPhaseAfterRollback   | no ListenerTxFallback            | 1   |
  | is unset                                         | TxPhaseAfterCompletion | ListenerTxFallback(TxFallbackSkip) | 0 |
  | reports false for the committed transaction "tx1" | TxPhaseAfterCommit    | no ListenerTxFallback            | 1   |
```

<!-- cases: N/A 3 zero values: the zero TxFallbackMode is TxFallbackRun -->
<!-- cases: N/A 4,6: no numeric or duplicate input -->
<!-- cases: N/A 5 errors: no error path beyond the listener's own -->
<!-- cases: N/A 8,9: as for immediate listeners -->
<!-- cases: N/A 10 compatibility: new option -->

### Requirement: TxSource resolution
The root SHALL resolve one `TxSource`: `InjectorOpts.TxSource`, else a root service named `NameOf[TxSource]()`, else the resolved publication store's own when it implements `TxAwareStore`, else none. When a `TxAwareStore` is in use and a different `TxSource` is configured, `Start` and the first durable `Publish` SHALL fail with `ErrTxSourceConflict`.

#### Scenario: Which TxSource is used
```gherkin
Given a root scope with InjectorOpts.TxSource <opts>, <service>, and <store>
And a <listener> function listener for OrderPlaced with ListenerTxPhase(TxPhaseAfterCommit) and ListenerID("l")
When the consumer publishes OrderPlaced{ID: "1"} with a context marked with "tx1"
Then <outcome>

Examples:
  | opts  | service                                   | store                                       | listener    | outcome                                   |
  | srcA  | no TxSource service                       | no publication store configured             | non-durable | srcA is consulted                         |
  | unset | a service under NameOf[TxSource]() = srcB | no publication store configured             | non-durable | srcB is consulted                         |
  | unset | no TxSource service                       | PublicationStore: a TxAwareStore with srcS  | durable     | srcS is consulted                         |
  | srcA  | no TxSource service                       | PublicationStore: a TxAwareStore with srcS  | durable     | Publish returns an error matching ErrTxSourceConflict |
  | srcS  | no TxSource service                       | PublicationStore: a TxAwareStore with srcS  | durable     | srcS is consulted                         |
  | unset | no TxSource service                       | no publication store configured             | non-durable | no transaction is found and "l" runs during Publish |
```

#### Scenario: A TxSource service is built once
```gherkin
Given a root scope with a lazy service under NameOf[TxSource]() whose provider counts its calls
And a function listener for OrderPlaced with ListenerTxPhase(TxPhaseAfterCommit)
When the consumer publishes OrderPlaced{ID: "1"} 3 times
Then the TxSource provider was called 1 time
```

<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: a failing TxSource provider fails the publish like any listener resolution; the conflict is the only TxSource-specific error -->
<!-- cases: N/A 9 panics: a panicking provider follows the provider-panic rule -->
<!-- cases: N/A 10 compatibility: containers without a TxSource never look one up -->

### Requirement: Transaction options are validated
`ListenerTxPhase` with a value outside the four `TxPhase` constants, and `ListenerTxFallback` outside `TxFallbackRun` and `TxFallbackSkip`, SHALL panic at registration with "DI: invalid TxPhase <n>" or "DI: invalid TxFallbackMode <n>". Both types SHALL implement `String()`. When an option is given twice, the last SHALL win.

#### Scenario: Option validation
```gherkin
Given a root scope
When the consumer registers a function listener for OrderPlaced with <options>
Then <outcome>

Examples:
  | options                                                                  | outcome                                        |
  | ListenerTxPhase(TxPhase(9))                                              | it panics with "DI: invalid TxPhase 9"         |
  | ListenerTxFallback(TxFallbackMode(5))                                    | it panics with "DI: invalid TxFallbackMode 5"  |
  | ListenerTxPhase(TxPhaseAfterRollback), ListenerTxPhase(TxPhaseAfterCommit) | it runs after commit                         |
```

#### Scenario: Enum names
```gherkin
Given the value <value>
When the consumer calls String on it
Then it returns "<name>"

Examples:
  | value                  | name                  |
  | TxPhaseImmediate       | TxPhaseImmediate      |
  | TxPhaseAfterCommit     | TxPhaseAfterCommit    |
  | TxPhaseAfterRollback   | TxPhaseAfterRollback  |
  | TxPhaseAfterCompletion | TxPhaseAfterCompletion |
  | TxFallbackRun          | TxFallbackRun         |
  | TxFallbackSkip         | TxFallbackSkip        |
  | TxPhase(9)             | TxPhase(9)            |
```

<!-- cases: N/A 7,8,9: options are validated once at registration -->
<!-- cases: N/A 10 compatibility: new types -->
