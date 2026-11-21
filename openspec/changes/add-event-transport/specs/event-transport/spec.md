# Spec Delta

## Purpose

Defines the seam between doger and any messaging library: how a transport is configured, how listeners receive events from other processes once per group or on every instance, how subscriptions follow the container lifecycle, how failures are reported back to the transport, and how a transport proves it honors the contract.

## ADDED Requirements

### Requirement: Transport resolution
The root SHALL resolve one transport: `InjectorOpts.EventTransport` when set, else a root service named `NameOf[EventTransport]()` (built once; an application service wins over an `AsDefault()` one), else none. A failing provider SHALL fail `Start` and each externalize with its error.

#### Scenario: Which transport is used
```gherkin
Given a root scope with InjectorOpts.EventTransport <opts> and <service>
And RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeImmediate)) returned nil
When the consumer publishes OrderPlaced{ID: "1"}
Then <outcome>

Examples:
  | opts  | service                                                                       | outcome                                                  |
  | tA    | no EventTransport service                                                     | tA received the send                                     |
  | unset | a service under NameOf[EventTransport]() = tB                                 | tB received the send                                     |
  | tA    | a service under NameOf[EventTransport]() = tB                                 | tA received the send and tB's provider was never called  |
  | unset | an AsDefault() service tC and an application service tB, both under NameOf[EventTransport]() | tB received the send               |
  | unset | a service under NameOf[EventTransport]() whose provider returns ErrNoBroker   | Publish returns an *ExternalizeError matching ErrNoBroker     |
  | unset | no EventTransport service                                                     | Publish returns nil and nothing is sent                  |
```

#### Scenario: A transport service is built once
```gherkin
Given a root scope with a lazy service under NameOf[EventTransport]() whose provider counts its calls
And RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeImmediate)) returned nil
When 20 goroutines each publish OrderPlaced once
Then the provider was called 1 time
```

<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 7 lifecycle: a transport is resolved on first use, before or after Start -->
<!-- cases: N/A 9 panics: a panicking provider follows the provider-panic rule and is returned as an error -->
<!-- cases: N/A 10 compatibility: containers with no externalized type and no remote listener never resolve a transport -->

### Requirement: ListenerDelivery declares a remote listener
`ListenerDelivery(m)` SHALL apply to `AsListener`, `ListenMethod` and `Listen`. `DeliverLocal` (the zero value) SHALL keep `add-eventing` delivery. A listener with `DeliverOnce` or `DeliverAll` SHALL be reached only through the transport, never by in-process dispatch.

#### Scenario: Local publishing does not reach remote listeners
```gherkin
Given a root scope with an in-memory transport and RegisterEventType[OrderPlaced](root, "orders.placed", <externalization>) returned nil
And a function listener "r" for OrderPlaced with ListenerID("r") and <delivery>
And the root was started
When the consumer publishes OrderPlaced{ID: "1"} and waits for the transport to go idle
Then "r" ran <ran> times

Examples:
  | externalization            | delivery                        | ran |
  | no option         | no ListenerDelivery             | 1   |
  | no option         | ListenerDelivery(DeliverLocal)  | 1   |
  | no option         | ListenerDelivery(DeliverOnce)   | 0   |
  | EventExternalized()   | ListenerDelivery(DeliverOnce)   | 1   |
  | EventExternalized()   | ListenerDelivery(DeliverAll)    | 1   |
  | EventExternalized()   | no ListenerDelivery             | 1   |
```
In the fourth and fifth rows the run comes from the transport, not from local dispatch; in the last row it comes from local dispatch only.

<!-- cases: N/A 3 zero values: DeliverLocal is the zero value (rows 1-2) -->
<!-- cases: N/A 4 boundaries: enumerated values; invalid ones are covered by "Remote listener registration rules" -->
<!-- cases: N/A 8 concurrency: covered by "Inbound deliveries may run concurrently" -->
<!-- cases: N/A 10 compatibility: rows 1, 2 and 6 pin add-eventing behavior -->

### Requirement: Remote listener registration rules
At registration a remote listener SHALL panic when: its scope is not the root or an extension private scope; it is a function listener without a non-empty `ListenerID`; it also has `ListenerAsync()` or a `ListenerTxPhase` other than `TxPhaseImmediate`; the root has started. `ListenerDelivery` with an invalid value SHALL panic.

#### Scenario: Invalid remote listeners
```gherkin
Given a root scope with a child scope "req"
When the consumer <registers>
Then it panics with "<message>"

Examples:
  | registers                                                                                         | message                                                                 |
  | Listen(req, fn, ListenerID("r"), ListenerDelivery(DeliverOnce))                                   | doger: remote listener "r" must be registered in the root or an extension scope |
  | Listen(root, fn, ListenerDelivery(DeliverOnce))                                                   | doger: remote function listener needs a ListenerID                         |
  | Listen(root, fn, ListenerID("r"), ListenerDelivery(DeliverAll), ListenerAsync())                  | doger: remote listener "r" cannot be async                                 |
  | Listen(root, fn, ListenerID("r"), ListenerDelivery(DeliverOnce), ListenerTxPhase(TxPhaseAfterCommit)) | doger: remote listener "r" cannot have a transaction phase             |
  | Listen(root, fn, ListenerID("r"), ListenerDelivery(DeliverOnce)) after root.Start() returned nil  | doger: remote listener "r" must be registered before Start                 |
  | Listen(root, fn, ListenerDelivery(EventDelivery(9)))                                              | doger: invalid EventDelivery 9                                             |
```

#### Scenario: Valid remote listeners
```gherkin
Given a root scope with an installed extension "billing"
When the consumer registers <listener>
Then it does not panic

Examples:
  | listener                                                                                              |
  | Listen(root, fn, ListenerID("r"), ListenerDelivery(DeliverOnce))                                      |
  | Provide(root, NewMailer, ListenMethod((*Mailer).OnPlaced, ListenerDelivery(DeliverAll)))              |
  | Provide(root, NewMailer, AsListener[OrderPlaced](ListenerDelivery(DeliverOnce), ListenerFilter(big))) |
  | a function listener with ListenerID("b") and ListenerDelivery(DeliverOnce) in the "billing" private scope |
  | Listen(root, fn, ListenerID("r"), ListenerDelivery(DeliverOnce), ListenerTxPhase(TxPhaseImmediate))   |
```

#### Scenario: Enum names
```gherkin
Given the value <value>
When the consumer calls String on it
Then it returns "<name>"

Examples:
  | value             | name              |
  | DeliverLocal      | DeliverLocal      |
  | DeliverOnce       | DeliverOnce       |
  | DeliverAll        | DeliverAll        |
  | EventDelivery(9)  | EventDelivery(9)  |
```

<!-- cases: N/A 4 boundaries: enumerated values -->
<!-- cases: N/A 8 concurrency: registration rules are checked synchronously -->
<!-- cases: N/A 10 compatibility: local listeners keep every add-eventing option combination -->

### Requirement: Start validates remote listeners
`Start` SHALL fail, before opening any subscription, with an error matching `ErrNoEventTransport` when remote listeners exist and no transport is resolved, `ErrEventTypeNotFound` when a remote listener's event type is not registered, and `ErrListenerGroupConflict` when two remote listeners share a group and destination.

#### Scenario: Start-time validation
```gherkin
Given a root scope with <transport> and <setup>
When the consumer calls Start
Then it returns <result>

Examples:
  | transport                 | setup                                                                                                                  | result                                        |
  | no transport              | RegisterEventType[OrderPlaced](root, "orders.placed") and a remote listener "r" for OrderPlaced                        | an error matching ErrNoEventTransport         |
  | an in-memory transport    | a remote listener "r" for OrderPlaced and no RegisterEventType call                                                    | an error matching ErrEventTypeNotFound        |
  | an in-memory transport    | RegisterEventType[OrderPlaced](root, "orders.placed") and two remote listeners with ListenerID("r") for OrderPlaced, both DeliverOnce | an error matching ErrListenerGroupConflict |
  | an in-memory transport    | RegisterEventType[OrderPlaced](root, "orders.placed") and remote listeners "r" for OrderPlaced and "r" for OrderCancelled registered as "orders.cancelled" | nil |
  | no transport              | no remote listener                                                                                                     | nil                                           |
```

<!-- cases: N/A 3,4: no zero or numeric input beyond the rows -->
<!-- cases: N/A 8 concurrency: validation runs once inside Start -->
<!-- cases: N/A 9 panics: validation returns errors -->
<!-- cases: N/A 10 compatibility: last row: containers without remote listeners start as before -->

### Requirement: Group names and instance identity
The transport SHALL receive a `Subscription` with the mapped destination, the mode, and the group `EventGroupPrefix + listenerID` for `DeliverOnce` or `EventGroupPrefix + listenerID + "/" + InstanceID` for `DeliverAll`. `InstanceID` SHALL default to a UUIDv7 generated per root, and then `Ephemeral` SHALL be true for `DeliverAll`.

#### Scenario: Subscriptions the transport receives
```gherkin
Given a root scope with InjectorOpts.EventGroupPrefix <prefix>, InjectorOpts.InstanceID <instance>, and a recording transport
And RegisterEventType[OrderPlaced](root, "orders.placed") returned nil
And a function listener for OrderPlaced with ListenerID("mail") and ListenerDelivery(<mode>)
When the consumer calls Start
Then the transport received one Subscription with Destination "orders.placed", Mode <mode>, Group <group> and Ephemeral <ephemeral>

Examples:
  | prefix   | instance  | mode         | group                               | ephemeral |
  | unset    | unset     | DeliverOnce  | "mail"                              | false     |
  | "shop."  | unset     | DeliverOnce  | "shop.mail"                         | false     |
  | unset    | "pod-0"   | DeliverAll   | "mail/pod-0"                        | false     |
  | "shop."  | "pod-0"   | DeliverAll   | "shop.mail/pod-0"                   | false     |
  | unset    | unset     | DeliverAll   | "mail/" followed by the generated ID | true     |
```

#### Scenario: Default listener IDs name service listener groups
```gherkin
Given a root scope with a recording transport and RegisterEventType[OrderPlaced](root, "orders.placed") returned nil
And Provide(root, NewMailer, ListenMethod((*Mailer).OnPlaced, ListenerDelivery(DeliverOnce)))
When the consumer calls Start
Then the transport received a Subscription whose Group is the listener's default ID as defined by add-eventing
```

<!-- cases: N/A 4 boundaries: no numeric input -->
<!-- cases: N/A 5 errors: group conflicts are covered by "Start validates remote listeners" -->
<!-- cases: N/A 8,9: names are computed once at Start -->
<!-- cases: N/A 10 compatibility: new options -->

### Requirement: Subscriptions follow the container lifecycle
`Start` SHALL open one subscription per remote listener after every Starter succeeded and before the Started hooks. A `Subscribe` error SHALL stop the subscriptions already opened and fail `Start` with that error. Shutdown SHALL stop every subscription before the Closing hooks, waiting for handlers in flight up to the shutdown context.

#### Scenario: Subscriptions open after Starters
```gherkin
Given a root scope with a recording transport, a Starter service "db" that records "start:db", and a Started hook that records "hook:started"
And RegisterEventType[OrderPlaced](root, "orders.placed") and a remote listener "r" for OrderPlaced
When the consumer calls Start
Then the recorded order is ["start:db", "subscribe:r", "hook:started"]
```

#### Scenario: A failing Subscribe fails Start
```gherkin
Given a root scope with a transport whose Subscribe returns ErrNoBroker for destination "orders.cancelled" and succeeds otherwise
And registered types "orders.placed" and "orders.cancelled", with remote listeners "a" for OrderPlaced and "b" for OrderCancelled registered in that order
When the consumer calls Start
Then Start returns an error matching ErrNoBroker
And the subscription for "a" was stopped
And the Failed hooks ran with that error
```

#### Scenario: Shutdown stops subscriptions and waits for handlers
```gherkin
Given a started root scope with an in-memory transport, a remote listener "r" that blocks until "release" is closed, and a Closing hook that records "hook:closing"
And "r" is handling one event
When the consumer calls Shutdown in another goroutine and closes "release" after 50ms
Then "r" finished before the subscription stop returned
And the recorded order is ["handler:r:done", "unsubscribe:r", "hook:closing"]
And no goroutine leaked
```

#### Scenario: The shutdown context bounds the wait
```gherkin
Given a started root scope with an in-memory transport and a remote listener "r" that blocks until its context is done
And "r" is handling one event
When the consumer calls ShutdownWithContext with a context that times out after 50ms
Then ShutdownWithContext returns within 1s
And the handler's context was cancelled
```

#### Scenario: Stop errors are reported
```gherkin
Given a started root scope whose transport's stop function for "r" returns ErrStopFailed, and a recording HookEventError
When the consumer calls Shutdown
Then the hook received an error matching ErrStopFailed
And Shutdown completed the remaining shutdown steps
```

<!-- cases: N/A 3,4: no zero or numeric input -->
<!-- cases: N/A 6 duplicate: double Shutdown follows container-shutdown; subscriptions are stopped once -->
<!-- cases: N/A 9 panics: a panicking Subscribe or stop function is recovered and treated as its error -->
<!-- cases: N/A 10 compatibility: containers without remote listeners open no subscription -->

### Requirement: Inbound dispatch reaches one listener
For each delivered envelope, the root SHALL decode the payload to the listener's registered type and call that listener only, building a service listener lazily, with a context holding the propagated headers and `EnvelopeFromContext`. A `ListenerFilter` returning false SHALL acknowledge without building. The handler SHALL return nil on success and the listener's error as a `*ListenerError` otherwise.

#### Scenario: Inbound outcomes
```gherkin
Given two roots "a" and "b" sharing an in-memory transport with RegisterEventType[OrderPlaced] for "orders.placed" on both, externalized on "a"
And on "b" a function listener "x" for OrderPlaced with ListenerDelivery(DeliverOnce) and <listener>, and a local function listener "y" for OrderPlaced
And both roots are started
When "a" publishes OrderPlaced{ID: "1", Total: <total>}
Then "x" ran <ran> times, "y" ran 0 times, and the handler returned <result>

Examples:
  | listener                                             | total | ran | result                                    |
  | that returns nil                                     | 10    | 1   | nil                                       |
  | with ListenerFilter(Total > 100)                     | 10    | 0   | nil                                       |
  | with ListenerFilter(Total > 100)                     | 500   | 1   | nil                                       |
  | that returns ErrOutOfStock                           | 10    | 1   | a *ListenerError matching ErrOutOfStock   |
  | that panics with "kaboom"                            | 10    | 1   | a *ListenerError whose message contains "kaboom" |
```

#### Scenario: Lazy service listeners are built on first delivery
```gherkin
Given a root scope "b" with Provide(b, NewMailer, ListenMethod((*Mailer).OnPlaced, ListenerDelivery(DeliverOnce))) whose provider counts its calls
And "b" was started
When 3 events for "orders.placed" are delivered to "b"
Then the provider was called 1 time
```

#### Scenario: Decoding failures are permanent
```gherkin
Given a started root "b" with a remote listener "x" for OrderPlaced registered as "orders.placed" with EventAlias("order.created")
When the transport delivers an envelope with <envelope>
Then "x" ran <ran> times and the handler returned <result>

Examples:
  | envelope                                                           | ran | result                                                       |
  | EventType "orders.placed", ContentType "application/json", Payload {"ID":"1"} | 1 | nil                                              |
  | EventType "order.created", ContentType "application/json", Payload {"ID":"1"} | 1 | nil                                              |
  | EventType "orders.placed", ContentType "text/csv", Payload "1"     | 0   | an error matching ErrCodecMismatch for which IsPermanentError is true |
  | EventType "orders.placed", ContentType "application/json", Payload "{" | 0 | an error for which IsPermanentError is true                  |
  | EventType "orders.unknown", ContentType "application/json", Payload {} | 0 | an error matching ErrEventTypeNotFound for which IsPermanentError is true |
```

<!-- cases: N/A 3 zero values: an empty payload is a decode failure (row 4 shape) -->
<!-- cases: N/A 6 duplicate: duplicate deliveries reach the listener again; deduplication by EnvelopeFromContext(ctx).ID is the listener's or transport's job -->
<!-- cases: N/A 7 lifecycle: covered by "Subscriptions follow the container lifecycle" -->
<!-- cases: N/A 10 compatibility: local listeners are never called by inbound dispatch -->

### Requirement: Permanent errors stop redelivery
`PermanentError(err)` SHALL return an error that `IsPermanentError` reports true for and that unwraps to `err`; `PermanentError(nil)` SHALL return nil. A listener returning a permanent error SHALL have it returned to the transport unchanged inside its `*ListenerError`, so `IsPermanentError` stays true.

#### Scenario: Marking and detecting permanent errors
```gherkin
Given the error <input>
When the consumer evaluates <expression>
Then the result is <result>

Examples:
  | input                                          | expression                                   | result |
  | ErrBadOrder                                    | IsPermanentError(PermanentError(input))      | true   |
  | ErrBadOrder                                    | errors.Is(PermanentError(input), ErrBadOrder) | true  |
  | ErrBadOrder                                    | IsPermanentError(input)                      | false  |
  | nil                                            | PermanentError(input) == nil                 | true   |
  | fmt.Errorf("wrap: %w", PermanentError(ErrBadOrder)) | IsPermanentError(input)                 | true   |
  | a *ListenerError wrapping PermanentError(ErrBadOrder) | IsPermanentError(input)               | true   |
```

<!-- cases: N/A 2,4,6,7,8: a pure function -->
<!-- cases: N/A 9 panics: never panics -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Inbound deliveries may run concurrently
The root SHALL accept concurrent handler calls from a transport for the same and for different listeners, building each service listener once.

#### Scenario: Concurrent inbound deliveries
```gherkin
Given a started root "b" with a remote service listener "*Mailer" whose provider counts its calls and a remote function listener "f"
When a transport calls the handlers of both listeners 100 times each from 20 goroutines
Then "*Mailer" received 100 events, "f" received 100 events, the provider was called 1 time, and the race detector reports nothing
```

<!-- cases: N/A 1-7,9,10: covered by the other requirements; this one is concurrency only -->

### Requirement: The in-memory transport
`NewMemoryEventTransport(opts...)` SHALL return an `EventTransport` that delivers within one process: one subscriber per group per event, round robin within a group, each new group starting at the latest event, and redelivery of a non-permanent error up to `MemoryMaxAttempts(n)` attempts (default 3), after which the event and any permanent failure are dropped and logged.

#### Scenario: Groups in memory
```gherkin
Given one in-memory transport shared by roots "a", "b1" and "b2"
And on "b1" and "b2" remote listeners with ListenerID("mail") and ListenerDelivery(DeliverOnce), and with ListenerID("cache") and ListenerDelivery(DeliverAll)
And all roots are started
When "a" publishes 10 OrderPlaced events
Then "mail" ran 10 times in total across "b1" and "b2", and "cache" ran 10 times on "b1" and 10 times on "b2"
```

#### Scenario: Redelivery limits in memory
```gherkin
Given an in-memory transport with <options>, a recording logger, and a started root with a remote listener "x" that <behavior>
When one event is delivered
Then "x" ran <ran> times and the logger recorded <logs> dropped events

Examples:
  | options                 | behavior                                  | ran | logs |
  | no option               | returns ErrOutOfStock every time          | 3   | 1    |
  | MemoryMaxAttempts(1)    | returns ErrOutOfStock every time          | 1   | 1    |
  | MemoryMaxAttempts(5)    | fails twice with ErrOutOfStock, then succeeds | 3 | 0   |
  | no option               | returns PermanentError(ErrBadOrder)       | 1   | 1    |
```

#### Scenario: A new group starts at the latest event
```gherkin
Given an in-memory transport where "a" already published 5 events to "orders.placed"
When a root with a remote listener "late" for "orders.placed" starts and "a" publishes 1 more event
Then "late" ran 1 time
```

#### Scenario: Invalid memory options
```gherkin
Given no transport
When the consumer calls NewMemoryEventTransport(MemoryMaxAttempts(0))
Then it panics with "doger: invalid MemoryMaxAttempts 0"
```

<!-- cases: N/A 7 lifecycle: covered by "Subscriptions follow the container lifecycle" -->
<!-- cases: N/A 8 concurrency: covered by the conformance suite run against it -->
<!-- cases: N/A 10 compatibility: new API; documented as not crash-safe -->

### Requirement: The conformance suite
Package `eventtransporttest` SHALL provide `Run(t, factory, opts...)` checking: send and receive, one delivery per `DeliverOnce` group, every `DeliverAll` group, new groups at the latest event, redelivery after an error, none after a permanent error, stop waiting for handlers, and cancellation. `WithTx` adds outbox checks; `WithoutDeliverAll` instead checks that `DeliverAll` is rejected with `ErrSubscriptionNotSupported`. It SHALL import only the standard library.

#### Scenario: The in-memory transport passes the suite
```gherkin
Given the factory func(t *testing.T) EventTransport { return NewMemoryEventTransport() }
When eventtransporttest.Run(t, factory) runs
Then every subtest passes with -race and no goroutine leaks
```

#### Scenario: The suite catches a broken transport
```gherkin
Given a test transport that <defect>
When eventtransporttest.Run(t, factory) runs against it with a recording testing.TB
Then the subtest "<subtest>" fails

Examples:
  | defect                                                    | subtest            |
  | delivers each event to every member of a DeliverOnce group | DeliverOnceGroup   |
  | starts a new group at the earliest event                  | NewGroupAtLatest   |
  | redelivers after a permanent error                        | PermanentError     |
  | returns from stop while a handler is still running        | StopWaitsHandlers  |
```

#### Scenario: Transaction checks for a TxEventSender
```gherkin
Given a transport implementing TxEventSender and eventtransporttest.WithTx(harness), where the harness begins, commits and rolls back transactions
When eventtransporttest.Run(t, factory, eventtransporttest.WithTx(harness)) runs
Then the subtests "OutboxCommitDelivers" and "OutboxRollbackDiscards" run and pass
```

#### Scenario: Transports without DeliverAll
```gherkin
Given a test transport whose Subscribe <behavior> for a Subscription with Mode DeliverAll
When eventtransporttest.Run(t, factory, eventtransporttest.WithoutDeliverAll()) runs against it with a recording testing.TB
Then the DeliverAll subtests are skipped and the subtest "DeliverAllRejected" <outcome>

Examples:
  | behavior                                                   | outcome |
  | returns an error matching doger.ErrSubscriptionNotSupported | passes  |
  | returns nil and delivers to every member                    | fails   |
```

<!-- cases: N/A 3,4,6: inputs are the factory and options only -->
<!-- cases: N/A 9 panics: a panicking factory fails the subtest that called it -->
<!-- cases: N/A 10 compatibility: new package -->

### Requirement: Clones and transports
`Clone()` and `CloneWithOpts` SHALL share a transport set in `InjectorOpts` and SHALL resolve a service transport on their own. A clone SHALL open its own subscriptions at its own `Start` and SHALL generate its own instance ID unless `InstanceID` is set.

#### Scenario: Clone subscriptions
```gherkin
Given a root scope with InjectorOpts.EventTransport set to a recording transport, a registered "orders.placed" and remote listeners "mail" (DeliverOnce) and "cache" (DeliverAll)
When the consumer clones it and starts the original and the clone
Then the transport received 4 subscriptions: two with Group "mail" and two "cache/..." groups with different instance IDs
```

<!-- cases: N/A 3,4,5,6,9: no further inputs -->
<!-- cases: N/A 8 concurrency: each root's subscriptions are independent -->
<!-- cases: N/A 10 compatibility: clones keep their add-eventing listener copy behavior -->
