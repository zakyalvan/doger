# Spec Delta

## Purpose

Gives consumers at-least-once, crash-tolerant delivery of in-process events to durable listeners: publications are recorded through a replaceable, lease-based store (in the business transaction when the store supports it), completed on success, retried with backoff, dead-lettered after too many attempts, and redelivered at start, with an in-memory default that works with zero configuration.

## ADDED Requirements

### Requirement: Durable publications are recorded before delivery
For each durable listener (`ListenerDurable()`) a publish reaches and whose filter passes, `Publish` SHALL encode the event once and `Append` one `Publication` (ID, ListenerID, EventType, ContentType, Version, Payload, Headers, PublishedAt from the root clock) in one call before any listener of that publish runs.

#### Scenario: A durable delivery is recorded and completed
```gherkin
Given a root scope with a recording store wrapping NewMemoryPublicationStore() and a fake clock at 2026-01-01T00:00:00Z
And durable function listeners "mail" and "audit" for OrderPlaced
When the consumer publishes OrderPlaced{ID: "42"}
Then the store received 1 Append call with 2 publications for listeners ["mail", "audit"]
And both have EventType "github.com/kartaladev/doger.OrderPlaced", ContentType "application/json", Payload `{"ID":"42"}` and PublishedAt 2026-01-01T00:00:00Z
And the Append call happened before either listener ran
And the store received a Complete call for each publication's token
```

#### Scenario: Only durable listeners that pass their filter are recorded
```gherkin
Given a root scope with a recording store and function listeners for OrderPlaced: <listeners>
When the consumer publishes OrderPlaced{Amount: 5}
Then the store received <appends> Append calls with <records> publications in total

Examples:
  | listeners                                                                 | appends | records |
  | 2 non-durable                                                             | 0       | 0       |
  | 1 durable                                                                 | 1       | 1       |
  | 3 durable and 2 non-durable                                               | 1       | 3       |
  | 1 durable with ListenerFilter(Amount > 100)                               | 0       | 0       |
  | 1 durable, and 1 durable with ListenerFilter(Amount > 100)                | 1       | 1       |
  | 1 durable with ListenerTxPhase(TxPhaseAfterCommit), ListenerTxFallback(TxFallbackSkip), no transaction | 0 | 0 |
```

#### Scenario: Interface listeners store the dynamic type
```gherkin
Given a root scope with a recording store and a durable function listener "audit" for DomainEvent
When the consumer publishes <event>
Then the recorded publication has EventType "<type>"

Examples:
  | event                  | type                                       |
  | OrderPlaced{ID: "1"}   | github.com/kartaladev/doger.OrderPlaced    |
  | &OrderShipped{ID: "1"} | *github.com/kartaladev/doger.OrderShipped  |
```

<!-- cases: N/A 3 zero values: invalid events are rejected before recording (add-eventing "Publish argument handling") -->
<!-- cases: N/A 5 errors: covered by "Recording failures stop the publish" -->
<!-- cases: N/A 6 duplicate: one publication per durable listener per publish by construction -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent durable publishing" -->
<!-- cases: N/A 9 panics: covered by "Recording failures stop the publish" -->
<!-- cases: N/A 10 compatibility: non-durable listeners record nothing (first row) -->

### Requirement: Delivery outcomes are recorded
A successful durable delivery SHALL call `Complete` with its claim token. An error or panic SHALL call `Fail` with the token, the cause, the attempt time and the `RetryPolicy`'s next attempt time, report a `*ListenerError` to `HookEventError`, and SHALL NOT be returned by `Publish`.

#### Scenario: Durable delivery failure
```gherkin
Given a root scope with a recording store, a fake clock at 2026-01-01T00:00:00Z, the retry policy ExponentialRetry{Base: 1s, Max: 1h, Jitter: 0} and a recording HookEventError
And a durable function listener "mail" for OrderPlaced that <behavior>
When the consumer publishes OrderPlaced{ID: "1"}
Then Publish returns nil
And the store received Fail with cause <cause>, attempt 1, at 2026-01-01T00:00:00Z and next attempt at 2026-01-01T00:00:01Z
And the hook received a *ListenerError for "mail"

Examples:
  | behavior                | cause                       |
  | returns ErrSMTPDown     | matching ErrSMTPDown        |
  | panics with "kaboom"    | containing "kaboom"         |
```

#### Scenario: Durable failure does not stop other listeners
```gherkin
Given a root scope with EventErrorMode EventFailFast and function listeners "d" (durable, order 0, returns ErrSMTPDown) and "s" (non-durable, order 1)
When the consumer publishes OrderPlaced{ID: "1"}
Then "s" ran and Publish returns nil
```

#### Scenario: A non-durable failure leaves durable deliveries recorded
```gherkin
Given a root scope with EventErrorMode EventFailFast, a recording store, and function listeners "s" (non-durable, order 0, returns ErrBoom) and "d" (durable, order 1)
When the consumer publishes OrderPlaced{ID: "1"}
Then Publish returns a *ListenerError for "s"
And "d" ran once and its publication was completed
```

<!-- cases: N/A 3,4: the listener result is the only input -->
<!-- cases: N/A 6 duplicate: one outcome per delivery -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent durable publishing" -->
<!-- cases: N/A 10 compatibility: Publish's result for non-durable listeners is unchanged -->

### Requirement: Recording failures stop the publish
If encoding the event, generating an ID or `Append` fails or panics, `Publish` SHALL return an error wrapping the cause with the message prefix "DI: failed to record event `<type>`" and SHALL call no listener of that publish, durable or not.

#### Scenario: Recording errors
```gherkin
Given a root scope where <failing>
And a durable function listener "mail" and a non-durable function listener "log" for OrderPlaced
When the consumer publishes OrderPlaced{ID: "1"}
Then Publish returns an error <match> whose message starts with "DI: failed to record event `doger.OrderPlaced`"
And neither "mail" nor "log" ran

Examples:
  | failing                                       | match                         |
  | the codec's Marshal returns ErrBroken         | matching ErrBroken            |
  | the store's Append returns ErrBroken          | matching ErrBroken            |
  | the store's Append panics with "kaboom"       | containing "kaboom"           |
  | PublicationIDGenerator returns ""             | matching ErrInvalidPublication |
  | the codec's Marshal panics with "kaboom"      | containing "kaboom"           |
```

<!-- cases: N/A 4,6: one failure path per row -->
<!-- cases: N/A 8 concurrency: recording is per publish -->
<!-- cases: N/A 10 compatibility: publishes without durable listeners never record -->

### Requirement: Durable listener registration rules
A durable listener SHALL be registered in the root or an extension private scope, SHALL have `TxPhaseAfterCommit` (the default) or `TxPhaseImmediate`, and a durable function listener SHALL have a non-empty `ListenerID`. Two live durable listeners SHALL NOT share an ID. Each violation SHALL panic at registration with a "DI: " message naming the listener.

#### Scenario: Registration rules
```gherkin
Given a root scope with a child scope "req" and a durable function listener "mailer" already registered in the root
When the consumer registers <listener>
Then <outcome>

Examples:
  | listener                                                                       | outcome                                                                        |
  | service "*Audit" in the root with AsListener[OrderPlaced](ListenerDurable())   | succeeds with ID "[root]/*Audit#github.com/kartaladev/doger.OrderPlaced"      |
  | a durable function listener with ListenerID("audit") in an extension scope     | succeeds                                                                       |
  | a durable function listener with ListenerID("x") in "req"                      | panics with "DI: durable listener `x` must be registered in the root or an extension scope" |
  | a durable function listener with no ListenerID in the root                     | panics with "DI: durable function listener requires ListenerID"               |
  | a durable function listener with ListenerID("mailer") in the root              | panics with "DI: duplicate durable listener id `mailer`"                       |
  | a durable function listener with ListenerID("mailer") after stopping the first | succeeds                                                                       |
  | ListenerDurable(), ListenerTxPhase(TxPhaseAfterRollback)                       | panics with "DI: durable listener `l` cannot use TxPhaseAfterRollback"        |
  | ListenerDurable(), ListenerTxPhase(TxPhaseAfterCompletion)                     | panics with "DI: durable listener `l` cannot use TxPhaseAfterCompletion"      |
```
Rows without an ID in the listener column use `ListenerID("l")` in the root.

#### Scenario: Durable immediate listeners refuse a bound transaction
```gherkin
Given a root scope with a TxAwareStore and a durable function listener "l" with ListenerTxPhase(TxPhaseImmediate)
When the consumer publishes OrderPlaced{ID: "1"} with a context marked with "tx1"
Then Publish returns an error matching ErrTxPhaseNotDurable and nothing was recorded
```

<!-- cases: N/A 4 boundaries: not numeric -->
<!-- cases: N/A 8 concurrency: the ID check runs under the registration lock (covered by "Concurrent durable publishing") -->
<!-- cases: N/A 10 compatibility: non-durable listeners keep add-eventing's rules (duplicate IDs allowed) -->

### Requirement: Recording inside a transaction
When a transaction is bound, durable publications SHALL be appended through the store with the publish context, so a `TxAwareStore` writes them in that transaction, and they SHALL be delivered after commit. A store that is not `TxAwareStore` SHALL make `Publish` fail with `ErrTxNotSupported` without recording or calling a listener.

#### Scenario: Durable delivery and the transaction outcome
```gherkin
Given a root scope with a fake TxAwareStore whose TxSource finds "tx1", and a durable function listener "mail" for OrderPlaced
And the consumer published OrderPlaced{ID: "1"} with a context marked with "tx1"
When the fake transaction "tx1" <outcome>
Then the Append call received a context in which the TxSource finds "tx1"
And "mail" ran <ran> times, with a context in which the TxSource finds no transaction
And Complete was called <completes> times with a detached context

Examples:
  | outcome    | ran | completes |
  | commits    | 1   | 1         |
  | rolls back | 0   | 0         |
```

#### Scenario: A store without transaction support refuses a bound transaction
```gherkin
Given a root scope with InjectorOpts.TxSource srcA, the default in-memory store, and a durable function listener "mail" for OrderPlaced
When the consumer publishes OrderPlaced{ID: "1"} with a context in which srcA finds "tx1"
Then Publish returns an error matching ErrTxNotSupported
And nothing was recorded and "mail" did not run
```

<!-- cases: N/A 3,4,6: no input values -->
<!-- cases: N/A 9 panics: callback panics follow transactional-events -->
<!-- cases: N/A 10 compatibility: new behavior -->

### Requirement: Live deliveries hold a lease
`Append` SHALL return each publication leased to the root's owner ID for `PublicationLease` with a claim token. `Complete` and `Fail` SHALL use that token; a store SHALL reject a stale token with `ErrClaimLost`, which the root SHALL report to `HookEventError` without retrying.

#### Scenario: A slow live delivery is not claimed by redelivery
```gherkin
Given a started root scope with NewMemoryPublicationStore(), PublicationLease 5m, a fake clock and PublicationRetryInterval 1m
And a durable function listener "mail" that waits for "release"
And the consumer published OrderPlaced{ID: "1"} from another goroutine and "mail" is waiting
When the clock advances by 2m and the retry loop ticks
Then "mail" was entered 1 time
And after "release" is closed the publication is completed once
```

#### Scenario: A lost lease is reported
```gherkin
Given a root scope with a store whose Complete returns ErrClaimLost and a recording HookEventError
And a durable function listener "mail"
When the consumer publishes OrderPlaced{ID: "1"}
Then Publish returns nil and the hook received an error matching ErrClaimLost naming "mail"
And Fail was not called
```

<!-- cases: N/A 3,4: lease length validation is covered by "Publication options are validated" -->
<!-- cases: N/A 6,9: one token per publication -->
<!-- cases: N/A 10 compatibility: new behavior -->

### Requirement: Redelivery at Start
After every Starter started and before the Started hooks, when durable listeners exist and the store implements `PublicationClaimer`, `Start` SHALL run one pass that claims, in batches of `PublicationBatchSize`, publications due at the pass start time, delivers each at most once in that pass, and ends when a claim returns no publication it has not attempted.

#### Scenario: Incomplete publications are redelivered once per pass
```gherkin
Given a store holding <incomplete> incomplete publications for listener "mail", of which <failing> fail on delivery
And a root scope using that store with PublicationBatchSize <batch> and a durable function listener "mail"
When the consumer calls Start
Then Start returns nil within 1s
And "mail" was attempted <attempts> times before the Started hooks ran
And <completed> publications are completed

Examples:
  | incomplete | failing | batch | attempts | completed |
  | 0          | 0       | 100   | 0        | 0         |
  | 1          | 0       | 100   | 1        | 1         |
  | 101        | 0       | 100   | 101      | 101       |
  | 1          | 1       | 100   | 1        | 0         |
  | 5          | 5       | 1     | 5        | 0         |
```

#### Scenario: Unresolvable publications are failed without blocking Start
```gherkin
Given a store holding 1 incomplete publication with <problem>
When the consumer calls Start on a root scope using that store with a durable listener "mail"
Then Start returns nil within 1s
And the publication was failed with an error matching <error>

Examples:
  | problem                                      | error                     |
  | ListenerID "gone"                            | ErrListenerNotFound       |
  | EventType "order.unknown"                    | ErrEventTypeNotFound      |
  | ContentType "application/x-protobuf"         | ErrCodecMismatch          |
  | a payload that is not valid JSON             | a *json.SyntaxError       |
```

#### Scenario: Claim error fails Start
```gherkin
Given a store whose Claim returns ErrBroken, and a durable listener "mail"
When the consumer calls Start
Then Start returns an error matching ErrBroken and the Failed hooks ran with it
```

#### Scenario: No work without durable listeners
```gherkin
Given a root scope whose InjectorOpts has no PublicationStore and no durable listener, and a service under NameOf[PublicationStore]() whose provider counts its calls
When the consumer calls Start
Then the store provider was called 0 times
```

<!-- cases: N/A 3 zero values: batch size 0 is covered by "Publication options are validated" -->
<!-- cases: N/A 6 duplicate: duplicates across instances are prevented by leases ("Live deliveries hold a lease") -->
<!-- cases: N/A 9 panics: a listener panic is a failed delivery; a Claim panic fails Start like an error (same code path as the Claim error row) -->
<!-- cases: N/A 10 compatibility: the last scenario pins that containers without durable listeners do nothing new at Start -->

### Requirement: Attempts and dead letters
`Claim` SHALL count an attempt for each publication it returns, so a crash during delivery still counts. When a failed attempt reaches `PublicationMaxAttempts`, `Fail` SHALL mark the publication dead, and dead publications SHALL NOT be claimed. A store implementing `PublicationDeadLetters` SHALL list and resubmit them.

#### Scenario: Attempts up to dead-lettering
```gherkin
Given a root scope with NewMemoryPublicationStore(), PublicationMaxAttempts <max>, ExponentialRetry{Base: 1s, Max: 1h, Jitter: 0}, a fake clock and PublicationRetryInterval 1s
And a durable function listener "mail" that always returns ErrSMTPDown
When the consumer starts the root, publishes OrderPlaced{ID: "1"}, and advances the clock by 1h in 1s steps
Then "mail" was attempted <attempts> times
And the store's dead letters list <dead> publications

Examples:
  | max | attempts | dead |
  | 1   | 1        | 1    |
  | 3   | 3        | 1    |
  | 10  | 10       | 1    |
```

#### Scenario: A crash during delivery counts as an attempt
```gherkin
Given NewMemoryPublicationStore() holding a publication claimed once by owner "old" whose lease has expired, never completed
When a new root scope using that store with PublicationMaxAttempts 2 claims it at Start
Then the claimed publication has Attempts 2
```

#### Scenario: Resubmitting a dead letter
```gherkin
Given NewMemoryPublicationStore() with one dead publication for listener "mail"
And a root scope using it with a durable function listener "mail" that succeeds
When the consumer calls Resubmit with that publication's ID and then Start
Then "mail" received the event once and the publication is completed
```

<!-- cases: N/A 3 zero values: PublicationMaxAttempts 0 means the default; covered by "Publication options are validated" -->
<!-- cases: N/A 8 concurrency: attempts are counted by the store under its claim -->
<!-- cases: N/A 9 panics: a panicking listener counts like an error -->
<!-- cases: N/A 10 compatibility: new behavior -->

### Requirement: Retry policy
After a failed attempt, the next attempt time SHALL be `RetryPolicy.Next(attempts, lastAttemptAt)`. The default SHALL be `ExponentialRetry{Base: 1s, Max: 1h, Jitter: 0.1}`: `min(Max, Base * 2^(attempts-1))` after the last attempt (0 for attempts below 1), varied by up to ±Jitter of that delay, never overflowing.

#### Scenario: Exponential schedule without jitter
```gherkin
Given ExponentialRetry{Base: 1s, Max: 1h, Jitter: 0}
When the consumer calls Next with attempts <attempts> and last attempt at 2026-01-01T00:00:00Z
Then it returns 2026-01-01T00:00:00Z plus <delay>

Examples:
  | attempts | delay   |
  | 1        | 1s      |
  | 2        | 2s      |
  | 3        | 4s      |
  | 12       | 34m8s   |
  | 13       | 1h      |
  | 100      | 1h      |
  | 0        | 0s      |
```

#### Scenario: Jitter stays within bounds
```gherkin
Given ExponentialRetry{Base: 1s, Max: 1h, Jitter: 0.1}
When the consumer calls Next 1000 times with attempts 4 and the same last attempt time
Then every result lies within 7.2s to 8.8s after the last attempt
```

<!-- cases: N/A 5,6,9: Next is pure arithmetic and has no error path -->
<!-- cases: N/A 7,8: the policy holds no state -->
<!-- cases: N/A 10 compatibility: new type -->

### Requirement: Retry loop
With `PublicationRetryInterval` greater than 0 and a store implementing `PublicationClaimer`, the root SHALL run one retry goroutine that, every interval, claims and delivers due publications as the Start pass does. It SHALL start when the root becomes ready and SHALL stop, waiting for its batch in progress, before the Closing hooks run.

#### Scenario: Retry loop lifecycle
```gherkin
Given a root scope with a fake clock, PublicationRetryInterval <interval>, and a store that counts Claim calls
And a durable function listener "mail"
When the consumer <action>
Then the store received <claims> Claim calls after Start returned

Examples:
  | interval | action                                                             | claims |
  | 0        | starts the root and advances the clock by 10m                      | 0      |
  | 1m       | starts the root and advances the clock by 3m, one tick at a time   | 3      |
  | 1m       | starts the root, shuts it down, and advances the clock by 10m      | 0      |
  | 1m       | calls Start that fails with a Starter error, and advances 10m      | 0      |
```

#### Scenario: The loop stops before Closing hooks
```gherkin
Given a started root scope with PublicationRetryInterval 1m and a durable listener whose delivery records "delivered" after a 20ms delay, with one tick in progress
When the consumer shuts the root down with a HookContainerClosing hook recording "closing"
Then the record is ["delivered", "closing"] and no goroutine leaked
```

<!-- cases: N/A 3 zero values: interval 0 is the first row -->
<!-- cases: N/A 5 errors: tick errors go to HookEventError like Start pass failures -->
<!-- cases: N/A 6 duplicate: one loop per root -->
<!-- cases: N/A 9 panics: a Claim panic in a tick is recovered and reported to HookEventError -->
<!-- cases: N/A 10 compatibility: off by default -->

### Requirement: Retention of completed publications
With `PublicationRetention` 0, `Complete` SHALL ask the store to delete the publication. With a positive retention, completed publications SHALL be kept and, when the store implements `PublicationPurger`, `Purge` SHALL be called with `now - retention` (inclusive) at Start and then every `min(retention, 1h)` until shutdown.

#### Scenario: Purge timing
```gherkin
Given a root scope with a fake clock at 2026-01-01T12:00:00Z, PublicationRetention <retention> and a store that records Complete and Purge calls
When the consumer starts the root, publishes 1 event to a durable listener that succeeds, and advances the clock by 2h
Then <observed>

Examples:
  | retention | observed                                                                                 |
  | 0         | Complete was called with Delete true and Purge was never called                          |
  | 24h       | Complete was called with Delete false and Purge was called at 12:00, 13:00 and 14:00 with bounds 24h earlier |
  | 30m       | Purge was called every 30m from 12:00 to 14:00 (5 calls) with bounds 30m earlier          |
```

#### Scenario: The purge bound is inclusive
```gherkin
Given NewMemoryPublicationStore() with one publication completed at 2026-01-01T12:00:00Z
When the consumer calls Purge with 2026-01-01T12:00:00Z
Then the store holds no publication
```

<!-- cases: N/A 3 zero values: retention 0 is the first row -->
<!-- cases: N/A 5 errors: Purge errors go to HookEventError and the timer continues -->
<!-- cases: N/A 6,9: a Purge panic is recovered and reported like an error -->
<!-- cases: N/A 10 compatibility: new behavior -->

### Requirement: Codec and event type registry
Payloads SHALL be encoded with `InjectorOpts.EventCodec` (default JSON, content type "application/json"); redelivery SHALL decode a publication only with a codec of the same content type. The dynamic type of each recorded event SHALL be registered automatically under its import-path name; `RegisterEventType[E](root, name, opts...)` SHALL add an explicit name and version.

#### Scenario: Registering event type names
```gherkin
Given a root scope where OrderPlaced is registered as "<existing>"
When the consumer calls RegisterEventType[<type>](root, "<name>", <opts>)
Then it returns <error>
And a new publication of OrderPlaced has EventType "<stored>" and Version <version>

Examples:
  | existing     | type           | name         | opts          | error                 | stored                                  | version |
  | (default)    | OrderPlaced    | order.placed | none          | nil                   | order.placed                            | 0       |
  | (default)    | OrderPlaced    | order.placed | EventVersion(2) | nil                 | order.placed                            | 2       |
  | order.placed | OrderPlaced    | order.placed | none          | nil                   | order.placed                            | 0       |
  | order.placed | OrderCancelled | order.placed | none          | ErrEventTypeConflict  | order.placed                            | 0       |
  | order.placed | OrderPlaced    | order.new    | none          | ErrEventTypeConflict  | order.placed                            | 0       |
  | (default)    | OrderPlaced    |              | none          | ErrInvalidEventType   | github.com/kartaladev/doger.OrderPlaced | 0       |
```

#### Scenario: Old names still decode after an explicit rename
```gherkin
Given a store holding an incomplete OrderPlaced publication with EventType "github.com/kartaladev/doger.OrderPlaced"
And a root scope using it that called RegisterEventType[OrderPlaced](root, "order.placed") and has a durable listener "mail"
When the consumer calls Start
Then "mail" received the decoded OrderPlaced once
```

#### Scenario: Codec choice
```gherkin
Given a root scope with EventCodec <codec> and a durable function listener "mail" for OrderPlaced
When the consumer publishes OrderPlaced{ID: "42"}
Then the stored ContentType is "<type>" and Payload is <payload>

Examples:
  | codec                                         | type          | payload       |
  | unset                                         | application/json | `{"ID":"42"}` |
  | a codec with content type "text/x-kv" encoding "id=<ID>" | text/x-kv | `id=42`  |
```

#### Scenario: JSON codec round trip
```gherkin
Given the default JSON codec
When fuzzed OrderPlaced values are marshaled and unmarshaled into a new OrderPlaced
Then each decoded value equals its original
```

<!-- cases: N/A 4 boundaries: versions are plain integers stored as given -->
<!-- cases: N/A 8 concurrency: registration is guarded like listener registration -->
<!-- cases: N/A 9 panics: codec panics are covered by "Recording failures stop the publish" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Headers propagate across redelivery
`InjectorOpts.PublicationPropagator`, when set, SHALL `Inject` the publish context into `Publication.Headers` at recording and `Extract` them into the delivery context, for live delivery and for redelivery. `PublicationFromContext(ctx)` SHALL return the publication being delivered and true, and false elsewhere.

#### Scenario: Context survives a restart
```gherkin
Given a store holding an incomplete publication recorded with a propagator that injected "tenant"="t1"
And a root scope using that store and the same propagator, with a durable listener "mail" that records the tenant from its context and PublicationFromContext
When the consumer calls Start
Then "mail" observed tenant "t1" and a publication whose ID equals the stored one
```

#### Scenario: PublicationFromContext outside delivery
```gherkin
Given <ctx>
When the consumer calls PublicationFromContext with it
Then it returns false

Examples:
  | ctx                                                    |
  | context.Background()                                   |
  | the context a non-durable listener received            |
  | nil                                                    |
```

<!-- cases: N/A 4,6: no numeric or duplicate input -->
<!-- cases: N/A 5 errors: a propagator panic is recovered; Inject panics fail the publish like a recording failure, Extract panics fail the delivery -->
<!-- cases: N/A 8 concurrency: per-delivery context -->
<!-- cases: N/A 10 compatibility: no headers without a propagator -->

### Requirement: Publication IDs
Publication IDs SHALL be UUIDv7 strings built from the root clock and `crypto/rand`, unless `InjectorOpts.PublicationIDGenerator` is set. An empty generated ID SHALL fail recording with `ErrInvalidPublication`.

#### Scenario: ID format
```gherkin
Given a root scope with PublicationIDGenerator <generator> and a fake clock
When the consumer publishes 2 events to a durable listener, advancing the clock by 1ms between them
Then the IDs <match>

Examples:
  | generator               | match                                                                         |
  | unset                   | match ^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ and the second sorts after the first |
  | func() string {"p-1"}   | are both "p-1"                                                                |
```

<!-- cases: N/A 4 boundaries: not numeric -->
<!-- cases: N/A 6 duplicate: duplicate IDs from a custom generator are documented as the generator's responsibility; stores may reject them on Append -->
<!-- cases: N/A 8,9: the default generator is safe for concurrent use and does not panic -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Store resolution
The root SHALL resolve its store once, at the first durable publish or at `Start` when durable listeners exist: `InjectorOpts.PublicationStore`, else a root service named `NameOf[PublicationStore]()` (including an exported `AsDefault()` one), else `NewMemoryPublicationStore()`. It SHALL NOT register a service. A failing store provider SHALL fail that publish or `Start`.

#### Scenario: Resolution order
```gherkin
Given a root scope with <opts> and <service>, and a durable listener
When the consumer publishes OrderPlaced{ID: "1"}
Then <outcome>

Examples:
  | opts                     | service                                                           | outcome                                         |
  | PublicationStore: storeA | a service under NameOf[PublicationStore]() = storeB               | storeA recorded the publication                 |
  | unset                    | a service under NameOf[PublicationStore]() = storeB               | storeB recorded the publication                 |
  | unset                    | an exported AsDefault() storeD, then an app service storeB        | storeB recorded the publication                 |
  | unset                    | a service named "store" = storeB                                  | the in-memory store recorded the publication    |
  | unset                    | none                                                              | the in-memory store recorded the publication, and ListProvidedServices is unchanged |
  | unset                    | a service under NameOf[PublicationStore]() whose provider fails with ErrBroken | Publish returns an error matching ErrBroken and nothing ran |
```

#### Scenario: The in-memory store is announced and can be forbidden
```gherkin
Given a root scope with no store configured, a recording Logf, RequireDurableStore <require>, and a durable listener
When the consumer calls Start and then publishes 3 events
Then <outcome>

Examples:
  | require | outcome                                                                                               |
  | false   | Start returns nil and Logf was called once with "DI: durable events use the in-memory publication store; publications do not survive a restart" |
  | true    | Start returns an error matching ErrDurableStoreRequired                                              |
```

<!-- cases: N/A 4 boundaries: not numeric -->
<!-- cases: N/A 6 duplicate: resolution happens once; a store service registered after resolution is not used (documented) -->
<!-- cases: N/A 8 concurrency: concurrent first publishes resolve once (covered by "Concurrent durable publishing") -->
<!-- cases: N/A 9 panics: a panicking provider follows the provider-panic rule and fails the publish -->
<!-- cases: N/A 10 compatibility: no service is registered, so ListProvidedServices is unchanged -->

### Requirement: Publication options are validated
`New` and `NewWithOpts` SHALL treat 0 as the default for `PublicationLease` (5m), `PublicationMaxAttempts` (10) and `PublicationBatchSize` (100), and SHALL panic for a negative `PublicationLease`, `PublicationRetryInterval` or `PublicationRetention` with "DI: invalid <field> <value>".

#### Scenario: Option validation
```gherkin
Given InjectorOpts with <field> set to <value>
When the consumer calls NewWithOpts
Then <outcome>

Examples:
  | field                    | value | outcome                                            |
  | PublicationLease         | 0     | leases last 5m                                     |
  | PublicationLease         | -1s   | it panics with "DI: invalid PublicationLease -1s"  |
  | PublicationMaxAttempts   | 0     | publications are dead-lettered after 10 attempts   |
  | PublicationBatchSize     | 0     | claims ask for 100 publications                    |
  | PublicationBatchSize     | 1     | claims ask for 1 publication                       |
  | PublicationRetryInterval | -1m   | it panics with "DI: invalid PublicationRetryInterval -1m0s" |
  | PublicationRetention     | -1h   | it panics with "DI: invalid PublicationRetention -1h0m0s"  |
```

<!-- cases: N/A 6,7,8,9: options are read once at creation -->
<!-- cases: N/A 10 compatibility: existing InjectorOpts fields are unchanged; counts are uint -->

### Requirement: Concurrent durable publishing
Concurrent durable publishes, a Start redelivery pass and retry ticks in one root SHALL deliver every publication at least once and none twice while their leases hold, with distinct IDs, and SHALL resolve the store once.

#### Scenario: Concurrent durable publishing
```gherkin
Given NewMemoryPublicationStore() holding 50 incomplete publications for listener "mail", and a root scope using it with a durable listener "mail" that records publication IDs
When the consumer starts the root while 100 goroutines publish OrderPlaced concurrently
Then the run passes under -race within 5s
And "mail" recorded 150 distinct IDs, each once
```

<!-- cases: N/A 3,4,5,6,9: covered by the single-goroutine requirements -->
<!-- cases: N/A 10 compatibility: new behavior -->

### Requirement: Clone resolves its own store
A clone SHALL copy durable listener bindings and the event type registry, SHALL resolve its own store, retry loop and owner ID, and SHALL share a store set in `InjectorOpts`, which the caller owns. Two roots sharing a store SHALL not deliver a leased publication twice.

#### Scenario: Clone store isolation and sharing
```gherkin
Given a root scope R1 with <store> and a durable listener "mail"
When the consumer clones R1 into R2 and publishes OrderPlaced{ID: "2"} on R2
Then <outcome>

Examples:
  | store                                         | outcome                                                                |
  | the default in-memory store, already resolved | R2 recorded the publication in a store other than R1's                 |
  | PublicationStore: shared                      | shared recorded the publication leased to R2's owner ID, not R1's       |
```

#### Scenario: Shared store across roots
```gherkin
Given two roots sharing NewMemoryPublicationStore() with 20 incomplete publications for "mail", each with a durable listener "mail"
When both roots call Start concurrently
Then the 20 publications were delivered 20 times in total
```

<!-- cases: N/A 3,4,5,9: clone takes no input -->
<!-- cases: N/A 10 compatibility: Clone keeps copying what it copied before -->

### Requirement: Store conformance suite
Package `publicationtest` SHALL provide `TestStore(t, newStore)` that checks a store against this contract: append returns leased tokens, claims skip leased, dead and not-yet-due publications, claims count attempts, stale tokens fail with `ErrClaimLost`, delete on complete, inclusive purge, and dead-letter resubmission.

#### Scenario: Conformance results
```gherkin
Given <store>
When the consumer runs publicationtest.TestStore with it
Then <outcome>

Examples:
  | store                                              | outcome                                       |
  | NewMemoryPublicationStore()                        | every subtest passes                           |
  | a store whose Claim also returns leased publications | the subtest "claim skips leased" fails       |
  | a store whose Complete accepts any token           | the subtest "stale token" fails                |
  | a store whose Purge bound is exclusive             | the subtest "purge is inclusive" fails         |
```

<!-- cases: N/A 3,4,6,7,8,9: the suite itself covers these per store -->
<!-- cases: N/A 10 compatibility: new package -->
