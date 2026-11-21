# Spec Delta

## Purpose

Decides which published events leave the process and when they are handed to the transport (immediately, after commit, or in the caller's transaction through an outbox), with predictable precedence between per-publish, per-type and global settings and a predictable error contract for each.

## ADDED Requirements

### Requirement: Only externalized event types leave the process
`Publish` SHALL hand an event to the transport only when its dynamic type is registered with `EventExternalized()` or `EventExternalize(s)`, exactly once per `Publish`, whatever the number of local listeners. In-process listeners SHALL run as in `add-eventing` whether or not the type is externalized.

#### Scenario: Externalized, registered and unregistered types
```gherkin
Given a root scope with a recording transport
And <registration>
And a function listener "local" for OrderPlaced
When the consumer publishes OrderPlaced{ID: "1"}
Then Publish returns nil, "local" ran once, and the transport received <sends> sends

Examples:
  | registration                                                              | sends |
  | no RegisterEventType call                                                 | 0     |
  | RegisterEventType[OrderPlaced](root, "orders.placed")                     | 0     |
  | RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalized())    | 1     |
  | RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeImmediate)) | 1 |
```

#### Scenario: Externalization does not depend on local listeners
```gherkin
Given a root scope with a recording transport and RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalized()) returned nil
And <listeners> function listeners for OrderPlaced
When the consumer publishes OrderPlaced{ID: "1"}
Then the transport received 1 send

Examples:
  | listeners |
  | 0         |
  | 1         |
  | 5         |
```

<!-- cases: N/A 3 zero values: an unregistered type is the first row -->
<!-- cases: N/A 6 duplicate: one registration per type; see event-types -->
<!-- cases: N/A 9 panics: covered by the strategy requirements -->
<!-- cases: N/A 10 compatibility: a type that is not externalized behaves exactly as in add-eventing (first two rows) -->

### Requirement: No transport keeps externalized events in-process
When no transport is resolved, `Publish` of an externalized type SHALL deliver to in-process listeners, return nil, and log once per root through `Logf` (or the standard `log` package) that externalized events stay in-process.

#### Scenario: Externalized type without a transport
```gherkin
Given a root scope with no EventTransport, a recording Logf, and RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalized()) returned nil
And a function listener "local" for OrderPlaced
When the consumer publishes OrderPlaced{ID: "1"} 3 times
Then every Publish returns nil and "local" ran 3 times
And Logf recorded 1 message containing "no EventTransport"
```

<!-- cases: N/A 2,4,5,6: no options or inputs beyond the absent transport -->
<!-- cases: N/A 8 concurrency: the one-time log is guarded like the other one-time logs -->
<!-- cases: N/A 9 panics: no transport is called -->
<!-- cases: N/A 10 compatibility: new behavior -->

### Requirement: Externalization strategy precedence
The strategy for one publish SHALL be the first set of: `PublishExternalize(s)`, the type's `EventExternalize(s)`, `InjectorOpts.EventExternalize`; otherwise `ExternalizeAuto`. `EventExternalized()` SHALL leave the type's strategy unset.

#### Scenario: Which strategy applies
```gherkin
Given a root scope with InjectorOpts.EventExternalize <global> and a transport implementing TxEventSender that records how it was called
And RegisterEventType[OrderPlaced](root, "orders.placed", <type>) returned nil
When the consumer publishes OrderPlaced{ID: "1"} <publish> with a context holding transaction "tx1" that later commits
Then the transport was called <how>

Examples:
  | global            | type                            | publish                          | how                                   |
  | unset             | EventExternalized()                 | with no option                   | with SendTx during Publish (Auto: outbox) |
  | ExternalizeImmediate   | EventExternalized()                 | with no option                   | with Send during Publish              |
  | ExternalizeImmediate   | EventExternalize(ExternalizeAfterCommit)  | with no option                   | with Send after commit                |
  | ExternalizeImmediate   | EventExternalize(ExternalizeAfterCommit)  | with PublishExternalize(ExternalizeOutbox) | with SendTx during Publish            |
  | unset             | EventExternalize(ExternalizeOutbox)       | with PublishExternalize(ExternalizeImmediate) | with Send during Publish           |
  | ExternalizeAfterCommit | EventExternalized()                 | with no option                   | with Send after commit                |
```

<!-- cases: N/A 4 boundaries: enumerated values only; invalid values are covered by "Externalization options are validated" -->
<!-- cases: N/A 5 errors: per-strategy errors are covered by their own requirements -->
<!-- cases: N/A 7,8,9: precedence is computed per publish with no shared state -->
<!-- cases: N/A 10 compatibility: new options -->

### Requirement: ExternalizeAuto chooses outbox or immediate
`ExternalizeAuto` SHALL behave as `ExternalizeOutbox` when the resolved `TxSource` finds a transaction and the transport implements `TxEventSender`, and as `ExternalizeImmediate` otherwise.

#### Scenario: Auto resolution
```gherkin
Given a root scope with <transport>, <txsource>, a recording transport wrapper, and RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalized()) returned nil
When the consumer publishes OrderPlaced{ID: "1"} with <ctx>
Then the transport was called <how>

Examples:
  | transport                         | txsource                             | ctx                                  | how                         |
  | a TxEventSender                   | the transport's own TxSource         | a context holding transaction "tx1"  | with SendTx during Publish  |
  | a TxEventSender                   | the transport's own TxSource         | context.Background()                 | with Send during Publish    |
  | a plain EventTransport            | InjectorOpts.TxSource finding "tx1"  | a context holding transaction "tx1"  | with Send during Publish    |
  | a plain EventTransport            | none                                 | context.Background()                 | with Send during Publish    |
```

<!-- cases: N/A 2 overrides: covered by "Externalization strategy precedence" -->
<!-- cases: N/A 4,6: no numeric or duplicate input -->
<!-- cases: N/A 5 errors: the chosen strategy's errors apply -->
<!-- cases: N/A 8,9: as for the chosen strategy -->
<!-- cases: N/A 10 compatibility: new behavior -->

### Requirement: Immediate externalization returns the send error
Under `ExternalizeImmediate`, `Publish` SHALL call `Send` on the publisher's goroutine after in-process synchronous listeners ran, and SHALL return a send error as an `*ExternalizeError` carrying the event type name, destination and strategy and wrapping the cause.

#### Scenario: Immediate send outcomes
```gherkin
Given a root scope whose transport's Send <behavior>
And RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeImmediate)) returned nil
And a function listener "local" for OrderPlaced
When the consumer publishes OrderPlaced{ID: "1"}
Then "local" ran once before Send was called, and Publish returns <result>

Examples:
  | behavior                         | result                                                                                         |
  | returns nil                      | nil                                                                                            |
  | returns ErrBrokerDown            | an *ExternalizeError with EventType "orders.placed", Destination "orders.placed", Strategy ExternalizeImmediate, matching ErrBrokerDown |
  | panics with "kaboom"             | an *ExternalizeError whose message contains "kaboom"                                                |
```

#### Scenario: Send honors the publish context
```gherkin
Given a root scope whose transport's Send blocks until its context is done
And RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeImmediate)) returned nil
When the consumer publishes OrderPlaced{ID: "1"} with a context that times out after 50ms
Then Publish returns an *ExternalizeError matching context.DeadlineExceeded within 1s
```

<!-- cases: N/A 3 zero values: a nil context reaches Send as context.Background(), per add-eventing's argument handling -->
<!-- cases: N/A 4,6: no numeric or duplicate input -->
<!-- cases: N/A 7 lifecycle: covered by "Externalization and the container lifecycle" -->
<!-- cases: N/A 10 compatibility: new behavior -->

### Requirement: After-commit externalization runs in the commit callback
Under `ExternalizeAfterCommit` with a transaction bound, `Publish` SHALL register the send with `OnCommit` and return nil for it; the send SHALL run with the detached publish context, and its error or panic SHALL go to `HookEventError` as an `*ExternalizeError`. A rollback SHALL send nothing. Without a transaction it SHALL behave as `ExternalizeImmediate`.

#### Scenario: After-commit outcomes
```gherkin
Given a root scope with a fake TxSource, a recording HookEventError, and a transport whose Send <behavior>
And RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeAfterCommit)) returned nil
And the consumer published OrderPlaced{ID: "1"} with <ctx>, and Publish returned <publishResult>
When <then>
Then the transport received <sends> sends and the hook received <hook>

Examples:
  | behavior              | ctx                                  | publishResult                              | then                             | sends | hook                                             |
  | returns nil           | a context holding transaction "tx1"  | nil                                        | the fake transaction "tx1" commits | 1   | nothing                                          |
  | returns nil           | a context holding transaction "tx1"  | nil                                        | the fake transaction "tx1" rolls back | 0 | nothing                                          |
  | returns ErrBrokerDown | a context holding transaction "tx1"  | nil                                        | the fake transaction "tx1" commits | 1   | an *ExternalizeError matching ErrBrokerDown           |
  | panics with "kaboom"  | a context holding transaction "tx1"  | nil                                        | the fake transaction "tx1" commits | 1   | an *ExternalizeError whose message contains "kaboom"  |
  | returns ErrBrokerDown | context.Background()                 | an *ExternalizeError matching ErrBrokerDown     | nothing more happens             | 1     | nothing                                          |
```

#### Scenario: The after-commit send is detached
```gherkin
Given a root scope with a fake TxSource and a transport that records the context Send receives
And RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeAfterCommit)) returned nil
And the consumer published OrderPlaced{ID: "1"} with a context holding transaction "tx1" and "trace"="t1", then cancelled that context
When the fake transaction "tx1" commits
Then Send received a context with "trace"="t1", Err nil, and no transaction found by the TxSource
```

<!-- cases: N/A 4,6: no numeric or duplicate input -->
<!-- cases: N/A 8 concurrency: the send runs where the Tx implementation calls OnCommit callbacks -->
<!-- cases: N/A 10 compatibility: new behavior; the at-most-once-on-crash property is documented, not observable in a test -->

### Requirement: Outbox externalization writes in the caller's transaction
Under `ExternalizeOutbox` with a transaction bound, `Publish` SHALL call the transport's `SendTx` during `Publish` with the publish context and return its error as an `*ExternalizeError`. Rolling back the transaction SHALL leave nothing to deliver; committing SHALL make the event deliverable.

#### Scenario: Outbox outcomes
```gherkin
Given two root scopes "a" and "b" sharing an in-memory transport that implements TxEventSender over a fake transaction
And RegisterEventType[OrderPlaced] for "orders.placed" on both, externalized with EventExternalize(ExternalizeOutbox) on "a"
And on "b" a started function listener for OrderPlaced with ListenerID("mail") and ListenerDelivery(DeliverOnce)
And "a" published OrderPlaced{ID: "1"} with a context holding transaction "tx1", and Publish returned nil
When the fake transaction "tx1" <outcome>
Then the listener on "b" ran <ran> times within 1s

Examples:
  | outcome     | ran |
  | commits     | 1   |
  | rolls back  | 0   |
```

#### Scenario: SendTx failure is returned
```gherkin
Given a root scope whose TxEventSender's SendTx returns ErrInsertFailed
And RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeOutbox)) returned nil
When the consumer publishes OrderPlaced{ID: "1"} with a context holding transaction "tx1"
Then Publish returns an *ExternalizeError with Strategy ExternalizeOutbox matching ErrInsertFailed
```

<!-- cases: N/A 3,4,6: no zero, numeric or duplicate input beyond the transaction -->
<!-- cases: N/A 8 concurrency: SendTx runs on the publisher's goroutine inside its transaction -->
<!-- cases: N/A 9 panics: a SendTx panic is returned as an *ExternalizeError, as for Send -->
<!-- cases: N/A 10 compatibility: new behavior -->

### Requirement: Outbox without a transaction
When `ExternalizeOutbox` applies and no transaction is found, `Publish` SHALL return an `*ExternalizeError` matching `ErrNoTransaction` and send nothing under `OutboxFallbackFail` (the default), and SHALL behave as `ExternalizeImmediate` under `InjectorOpts.EventOutboxFallback = OutboxFallbackImmediate`.

#### Scenario: Strict and fallback
```gherkin
Given a root scope with a TxEventSender transport, InjectorOpts.EventOutboxFallback <fallback>, and RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeOutbox)) returned nil
When the consumer publishes OrderPlaced{ID: "1"} with context.Background()
Then Publish returns <result> and the transport was called <how>

Examples:
  | fallback                  | result                                      | how                     |
  | unset                     | an *ExternalizeError matching ErrNoTransaction   | never                   |
  | OutboxFallbackFail        | an *ExternalizeError matching ErrNoTransaction   | never                   |
  | OutboxFallbackImmediate   | nil                                         | with Send during Publish |
```

<!-- cases: N/A 4,6: no numeric or duplicate input -->
<!-- cases: N/A 7,8,9: as for the strategy the fallback selects -->
<!-- cases: N/A 10 compatibility: new option -->

### Requirement: Outbox requires a capable transport
When `InjectorOpts.EventExternalize` or a registered type's strategy is `ExternalizeOutbox` and the resolved transport does not implement `TxEventSender`, `Start` SHALL fail with an error matching `ErrOutboxNotSupported`. A `PublishExternalize(ExternalizeOutbox)` with such a transport SHALL return an `*ExternalizeError` matching it.

#### Scenario: Outbox with a plain transport
```gherkin
Given a root scope with a plain EventTransport and <config>
When the consumer <action>
Then it returns an error matching ErrOutboxNotSupported

Examples:
  | config                                                                          | action                                                           |
  | InjectorOpts.EventExternalize = ExternalizeOutbox                                         | calls Start                                                      |
  | RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeOutbox)) | calls Start                                                     |
  | RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalized())           | publishes OrderPlaced{ID: "1"} with PublishExternalize(ExternalizeOutbox) |
```

<!-- cases: N/A 3,4,6: no zero, numeric or duplicate input -->
<!-- cases: N/A 8,9: a check, not a call into the transport -->
<!-- cases: N/A 10 compatibility: new behavior -->

### Requirement: Externalization follows in-process dispatch outcome
Externalization SHALL run after in-process synchronous listeners. Under `EventFailFast`, a listener error SHALL stop the externalization and `Publish` SHALL return that error. Under `EventContinue`, the externalization SHALL still run and `Publish` SHALL join its error with the listener errors.

#### Scenario: Listener failure and externalize
```gherkin
Given a root scope with InjectorOpts.EventErrorMode <mode>, a transport whose Send <send>, and RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeImmediate)) returned nil
And a function listener "local" for OrderPlaced that returns ErrOutOfStock
When the consumer publishes OrderPlaced{ID: "1"}
Then the transport received <sends> sends and Publish returns <result>

Examples:
  | mode           | send                   | sends | result                                                     |
  | EventFailFast  | returns nil            | 0     | a *ListenerError matching ErrOutOfStock                    |
  | EventContinue  | returns nil            | 1     | an error matching ErrOutOfStock                            |
  | EventContinue  | returns ErrBrokerDown  | 1     | an error matching both ErrOutOfStock and ErrBrokerDown     |
```

<!-- cases: N/A 3,4,6: no zero, numeric or duplicate input -->
<!-- cases: N/A 8 concurrency: async listeners do not delay or gate the externalization -->
<!-- cases: N/A 9 panics: a listener panic is a listener error per add-eventing -->
<!-- cases: N/A 10 compatibility: in-process behavior is unchanged; only the externalization step is added -->

### Requirement: PublishLocal and scopes
`PublishLocal()` SHALL never externalize. A publish from any scope of a root SHALL externalize through that root's transport.

#### Scenario: Local publishing and child scopes
```gherkin
Given a root scope with a child scope "req", a recording transport, and RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalized()) returned nil
When the consumer publishes OrderPlaced{ID: "1"} on <injector> <options>
Then the transport received <sends> sends

Examples:
  | injector | options              | sends |
  | the root | with no option       | 1     |
  | "req"    | with no option       | 1     |
  | the root | with PublishLocal()  | 0     |
  | "req"    | with PublishLocal()  | 0     |
```

<!-- cases: N/A 3,4,5,6,9: no further inputs -->
<!-- cases: N/A 8 concurrency: as for Publish -->
<!-- cases: N/A 10 compatibility: PublishLocal keeps its add-eventing meaning -->

### Requirement: Publisher handle for application ports
`PublisherFor(i)` SHALL return a `*Publisher` whose `Publish(ctx, events...)` publishes each event in order through `Publish(ctx, i, event)` and stops at the first error, returning it. A nil injector SHALL mean `DefaultRootScope`; no events SHALL return nil. It lets an application implement its own publishing port without importing doger in the domain.

#### Scenario: Publishing through the handle
```gherkin
Given a root scope with a recording transport, RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalized()) returned nil, and a function listener "local" for OrderPlaced that <behavior>
When the consumer calls PublisherFor(<injector>).Publish(ctx, <events>)
Then it returns <result>, "local" ran <ran> times and the transport received <sends> sends

Examples:
  | injector | behavior                                     | events                                         | result                                   | ran | sends |
  | root     | returns nil                                  | OrderPlaced{ID: "1"}, OrderPlaced{ID: "2"}     | nil                                      | 2   | 2     |
  | root     | returns nil                                  | nothing                                        | nil                                      | 0   | 0     |
  | root     | returns ErrOutOfStock for ID "1"             | OrderPlaced{ID: "1"}, OrderPlaced{ID: "2"}     | a *ListenerError matching ErrOutOfStock  | 1   | 0     |
  | root     | returns nil                                  | OrderPlaced{ID: "1"}, nil                      | an error matching ErrInvalidEvent        | 1   | 1     |
  | nil      | returns nil (registered on DefaultRootScope) | OrderPlaced{ID: "1"}                           | nil                                      | 1   | 1     |
```

#### Scenario: The handle satisfies a consumer-defined port
```gherkin
Given an application interface "EventPublisher" declaring Publish(ctx context.Context, events ...any) error
When the consumer assigns PublisherFor(root) to a variable of type EventPublisher
Then the program compiles and Publish on the variable reaches the root's listeners
```

<!-- cases: N/A 6 duplicate: publishing the same value twice publishes it twice -->
<!-- cases: N/A 7 lifecycle: as for Publish, per event -->
<!-- cases: N/A 8 concurrency: the handle holds only the injector and is safe for concurrent use, as Publish is -->
<!-- cases: N/A 9 panics: as for Publish -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Externalization and the container lifecycle
Externalization SHALL work before `Start`. When `Publish` fails with `ErrPublishAfterShutdown` it SHALL not externalize. A done publish context SHALL stop the publish before externalization, as for listeners.

#### Scenario: Externalization across the lifecycle
```gherkin
Given a root scope with a recording transport and RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeImmediate)) returned nil
When the consumer publishes OrderPlaced{ID: "1"} <when>
Then Publish returns <result> and the transport received <sends> sends

Examples:
  | when                                           | result                                      | sends |
  | before Start                                   | nil                                         | 1     |
  | after Start                                    | nil                                         | 1     |
  | with an already cancelled context              | an error matching context.Canceled          | 0     |
  | after the root was shut down                   | an error matching ErrPublishAfterShutdown   | 0     |
```

#### Scenario: Concurrent publishers each externalize once
```gherkin
Given a started root scope with a recording transport and RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeImmediate)) returned nil
When 100 goroutines each publish one OrderPlaced with a distinct ID
Then the transport received 100 sends with 100 distinct envelope IDs and the race detector reports nothing
```

<!-- cases: N/A 3,4,6: no further inputs -->
<!-- cases: N/A 9 panics: covered per strategy -->
<!-- cases: N/A 10 compatibility: ErrPublishAfterShutdown keeps its add-eventing meaning -->

### Requirement: Externalization options are validated
`New` and `NewWithOpts` SHALL panic with "doger: invalid ExternalizeStrategy <n>" or "doger: invalid OutboxFallback <n>" for out-of-range options. `PublishExternalize` with an invalid strategy SHALL panic with the same message. `EventExternalize` with an invalid strategy SHALL make `RegisterEventType` return `ErrInvalidEventType`. Both enums SHALL implement `String()`.

#### Scenario: Invalid values
```gherkin
Given <setup>
When the consumer <action>
Then <outcome>

Examples:
  | setup                                       | action                                                             | outcome                                           |
  | InjectorOpts.EventExternalize = ExternalizeStrategy(9) | calls NewWithOpts                                                 | it panics with "doger: invalid ExternalizeStrategy 9"     |
  | InjectorOpts.EventOutboxFallback = OutboxFallback(4) | calls NewWithOpts                                         | it panics with "doger: invalid OutboxFallback 4"     |
  | a root scope                                | calls PublishExternalize(ExternalizeStrategy(7))                             | it panics with "doger: invalid ExternalizeStrategy 7"     |
  | a root scope                                | calls RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeStrategy(7))) | it returns an error matching ErrInvalidEventType |
```

#### Scenario: Enum names
```gherkin
Given the value <value>
When the consumer calls String on it
Then it returns "<name>"

Examples:
  | value                    | name                    |
  | ExternalizeAuto               | ExternalizeAuto              |
  | ExternalizeImmediate          | ExternalizeImmediate         |
  | ExternalizeAfterCommit        | ExternalizeAfterCommit       |
  | ExternalizeOutbox             | ExternalizeOutbox            |
  | ExternalizeStrategy(9)        | ExternalizeStrategy(9)       |
  | OutboxFallbackFail       | OutboxFallbackFail      |
  | OutboxFallbackImmediate  | OutboxFallbackImmediate |
  | OutboxFallback(4)        | OutboxFallback(4)       |
```

<!-- cases: N/A 7,8: validated once, at construction or registration -->
<!-- cases: N/A 10 compatibility: new types; the zero InjectorOpts gives ExternalizeAuto and OutboxFallbackFail -->
