# Spec Delta

## Purpose

Gives consumers at-least-once, crash-tolerant delivery of in-process events to durable listeners: publications are recorded through a replaceable store, optionally in the business transaction, completed on success and redelivered at start, with an in-memory default that works with zero configuration.

## ADDED Requirements

### Requirement: Durable publications are recorded before delivery
For each durable listener (`Durable()`) an event reaches, `Publish` SHALL encode the event once and `Append` one `Publication` (ID, ListenerID, EventType, Payload, PublishedAt from the root clock, Attempts 0) before any listener of that publish runs, and SHALL call `Complete` once the listener returns nil.

#### Scenario: A durable delivery is recorded and completed
```gherkin
Given a root scope with the default store and a fake clock at 2026-01-01T00:00:00Z
And durable function listeners "mail" and "audit" for OrderPlaced
When the consumer publishes OrderPlaced{ID: "42"}
Then the store received 1 Append call with 2 publications for listeners ["mail", "audit"]
And both have EventType "github.com/kartaladev/doger.OrderPlaced", Payload `{"ID":"42"}` and PublishedAt 2026-01-01T00:00:00Z
And Append happened before either listener ran
And both publications were completed
```

#### Scenario: Only durable listeners are recorded
```gherkin
Given a root scope with <durable> durable and <plain> non-durable function listeners for OrderPlaced
When the consumer publishes OrderPlaced{ID: "1"}
Then the store received <appends> Append calls with <records> publications in total

Examples:
  | durable | plain | appends | records |
  | 0       | 2     | 0       | 0       |
  | 1       | 0     | 1       | 1       |
  | 3       | 2     | 1       | 3       |
```

<!-- cases: N/A 6 duplicate: one publication per listener per publish by construction -->
<!-- cases: N/A 9 panics: listener panics follow "Durable delivery failures are recorded" -->

### Requirement: Durable delivery failures are recorded
When a durable listener returns an error or panics, the root SHALL call `Fail` with the cause and the root clock's time, and SHALL report a `*ListenerError` to the async error handler. `Publish` SHALL NOT return delivery errors of durable listeners, because the publication remains recorded for redelivery.

#### Scenario: Failed durable delivery stays incomplete
```gherkin
Given a root scope with the default store
And a durable function listener "mail" for OrderPlaced that <behavior>
When the consumer publishes OrderPlaced{ID: "1"}
Then Publish returns nil
And the publication for "mail" has Attempts 1, LastError <last> and is not completed
And the async error handler received a *ListenerError for "mail"

Examples:
  | behavior                   | last            |
  | returns ErrSMTPDown        | "smtp down"     |
  | panics with "kaboom"       | contains "kaboom" |
```

<!-- cases: N/A 3,4,6: no input beyond the listener result -->

### Requirement: Recording failures stop the publish
If encoding the event or `Append` fails, `Publish` SHALL return an error wrapping the cause and naming the event type, and SHALL call no listener of that publish, durable or not.

#### Scenario: Encode or Append error
```gherkin
Given a root scope where <failing> returns ErrBroken
And a durable function listener "mail" and a non-durable function listener "log" for OrderPlaced
When the consumer publishes OrderPlaced{ID: "1"}
Then Publish returns an error matching ErrBroken that contains "doger.OrderPlaced"
And neither "mail" nor "log" ran

Examples:
  | failing                    |
  | the codec's Encode         |
  | the store's Append         |
```

<!-- cases: N/A 4,6,9: single failure path per call -->

### Requirement: Durable listeners need a stable ID
A durable service listener's default ID SHALL be `<scope path>/<service name>#<event type>`, where the scope path joins scope names from the root with "/". `Durable()` on a function listener without `ListenerID` SHALL panic with "DI: durable function listener requires ListenerID". A `ListenerID` already used in the same root SHALL panic with "DI: duplicate listener id `<id>`".

#### Scenario: Listener IDs
```gherkin
Given a root scope with a child scope "billing"
When the consumer registers <listener>
Then <outcome>

Examples:
  | listener                                                            | outcome                                                       |
  | service "*Mailer" in "billing" with AsListener[OrderPlaced](Durable()) | its ID is "[root]/billing/*Mailer#github.com/kartaladev/doger.OrderPlaced" |
  | the same with ListenerID("mailer")                                   | its ID is "mailer"                                            |
  | Listen[OrderPlaced] with Durable() and no ListenerID                 | panics with "DI: durable function listener requires ListenerID" |
  | Listen[OrderPlaced] with Durable() and ListenerID("")               | panics with "DI: durable function listener requires ListenerID" |
  | a second listener with ListenerID("mailer")                          | panics with "DI: duplicate listener id `mailer`"            |
```

<!-- cases: N/A 8 concurrency: ID uniqueness is checked under the registry lock (covered by events concurrency requirement) -->

### Requirement: Event type registry
A durable listener's event type SHALL be registered automatically under its default name, the type's import-path-qualified name. `RegisterEventType[E](root, name)` SHALL register an explicit name and return an error matching `ErrEventTypeConflict` when the name is taken by another type or the type has another explicit name. An empty name SHALL return `ErrInvalidEvent`.

#### Scenario: Registering event type names
```gherkin
Given a root scope where OrderPlaced is registered as "<existing>"
When the consumer calls RegisterEventType[<type>](root, "<name>")
Then it returns <error>
And a publication of OrderPlaced has EventType "<stored>"

Examples:
  | existing          | type          | name          | error                | stored           |
  | (none)            | OrderPlaced   | order.placed  | nil                  | order.placed     |
  | order.placed      | OrderPlaced   | order.placed  | nil                  | order.placed     |
  | order.placed      | OrderCancelled| order.placed  | ErrEventTypeConflict | order.placed     |
  | order.placed      | OrderPlaced   | order.new     | ErrEventTypeConflict | order.placed     |
  | (none)            | OrderPlaced   |               | ErrInvalidEvent      | github.com/kartaladev/doger.OrderPlaced |
```

<!-- cases: N/A 8,9: registration is guarded like listener registration; no panic path -->

### Requirement: Replaceable codec
Payloads SHALL be encoded with `encoding/json` by default. `InjectorOpts.EventCodec` SHALL replace the codec for both encoding at publish and decoding at redelivery.

#### Scenario: Codec choice
```gherkin
Given a root scope with EventCodec <codec>
And a durable function listener "mail" for OrderPlaced
When the consumer publishes OrderPlaced{ID: "42"}
Then the stored Payload is <payload>

Examples:
  | codec                              | payload        |
  | unset                              | `{"ID":"42"}`  |
  | a codec encoding as "id=<ID>"      | `id=42`        |
```

#### Scenario: JSON codec round trip
```gherkin
Given the default codec
When any OrderPlaced value is encoded and decoded to type OrderPlaced
Then the decoded value equals the original
```

<!-- cases: N/A 4,6: no boundary or duplicate concept -->

### Requirement: Transaction binding and phases
When the resolved `TxBinder` finds a transaction in the publish context, listeners SHALL run in their `Phase`: `PhaseImmediate` during `Publish`, `PhaseAfterCommit` on commit, `PhaseAfterRollback` on rollback, `PhaseAfterCompletion` on either. Durable listeners SHALL default to `PhaseAfterCommit`, others to `PhaseImmediate`.

#### Scenario: Phases with a bound transaction
```gherkin
Given a root scope with a fake TxBinder that binds transaction "tx1" from the context
And a function listener for OrderPlaced with <phase>
When the consumer publishes OrderPlaced{ID: "1"} within "tx1" and then <outcome> "tx1"
Then the listener ran <when>

Examples:
  | phase                       | outcome    | when                 |
  | Phase(PhaseImmediate)       | commits    | during Publish       |
  | Phase(PhaseAfterCommit)     | commits    | after commit         |
  | Phase(PhaseAfterCommit)     | rolls back | never                |
  | Phase(PhaseAfterRollback)   | rolls back | after rollback       |
  | Phase(PhaseAfterRollback)   | commits    | never                |
  | Phase(PhaseAfterCompletion) | commits    | after commit         |
  | Phase(PhaseAfterCompletion) | rolls back | after rollback       |
  | Durable() and no Phase      | commits    | after commit         |
  | no option                   | commits    | during Publish       |
```

#### Scenario: Durable records are written through the bound transaction
```gherkin
Given a root scope with a fake store and TxBinder that bind transaction "tx1"
And a durable function listener "mail" for OrderPlaced
When the consumer publishes OrderPlaced{ID: "1"} within "tx1"
Then the store's Append received a context from which the binder finds "tx1"
```

<!-- cases: N/A 4,6: no numeric or duplicate input -->

### Requirement: No bound transaction
When no transaction is bound, listeners with a non-immediate phase SHALL run during `Publish`, as if immediate. `TxFallback(TxSkip)` SHALL instead skip that listener, and for a durable listener SHALL record no publication. The default `TxBinder` SHALL never find a transaction.

#### Scenario: Fallback without a transaction
```gherkin
Given a root scope with the default TxBinder
And a <durable> function listener for OrderPlaced with Phase(PhaseAfterCommit) and <fallback>
When the consumer publishes OrderPlaced{ID: "1"}
Then the listener ran <ran> times and <records> publications were recorded

Examples:
  | durable     | fallback          | ran | records |
  | non-durable | no TxFallback     | 1   | 0       |
  | non-durable | TxFallback(TxSkip)| 0   | 0       |
  | durable     | no TxFallback     | 1   | 1       |
  | durable     | TxFallback(TxSkip)| 0   | 0       |
```

<!-- cases: N/A 3,4,6,9: no extra input -->

### Requirement: Redelivery at Start
After every Starter started and before the Started hooks, `Start` SHALL claim incomplete publications from the store in batches of `InjectorOpts.PublicationBatchSize` (default 100) until none remain, decode each and deliver it to the listener with its `ListenerID`, then call `Complete` or `Fail`.

#### Scenario: Incomplete publications are redelivered
```gherkin
Given a store holding <incomplete> incomplete publications for listener "mail" of type "github.com/kartaladev/doger.OrderPlaced"
And a root scope using that store with PublicationBatchSize <batch> and a durable function listener "mail"
When the consumer calls Start
Then "mail" received <incomplete> events before the Started hooks ran
And the store received <claims> Claim calls
And every publication is completed

Examples:
  | incomplete | batch | claims |
  | 0          | 100   | 1      |
  | 1          | 100   | 2      |
  | 100        | 100   | 2      |
  | 101        | 100   | 3      |
  | 5          | 1     | 6      |
```

#### Scenario: Unresolvable publications fail without blocking Start
```gherkin
Given a store holding an incomplete publication with <problem>
When the consumer calls Start on a root scope using that store
Then Start returns nil
And the publication was failed with an error matching <error>

Examples:
  | problem                                  | error                      |
  | ListenerID "gone" that is not registered | ErrListenerNotFound        |
  | EventType "order.unknown"                | ErrEventTypeNotRegistered  |
  | a payload the codec cannot decode         | the codec's decode error   |
```

#### Scenario: Redelivery errors are recorded, not returned
```gherkin
Given a store holding 1 incomplete publication for listener "mail"
And a durable function listener "mail" that returns ErrSMTPDown
When the consumer calls Start
Then Start returns nil
And the publication has Attempts 1 and is not completed
```

#### Scenario: Claim error fails Start
```gherkin
Given a store whose Claim returns ErrBroken
When the consumer calls Start
Then Start returns an error matching ErrBroken
And the Failed hooks ran with that error
```

<!-- cases: N/A 6 duplicate: duplicates across instances are the store's lease responsibility (design D9) -->

### Requirement: Periodic retry
With `InjectorOpts.EventRetryInterval` greater than 0, the root SHALL run one retry loop that claims and redelivers incomplete publications every interval, skipping a publication until `PublishedAt + interval * 2^(Attempts-1)` has passed. The loop SHALL start after `Start` and stop before services shut down. Zero SHALL disable it.

#### Scenario: Retry with exponential backoff
```gherkin
Given a root scope with a fake clock and EventRetryInterval <interval>
And a durable function listener "mail" that fails on its first <failures> attempts
When the consumer calls Start at t=0, publishes OrderPlaced{ID: "1"} at t=0, and advances the clock to <until>
Then "mail" was attempted <attempts> times

Examples:
  | interval | failures | until | attempts |
  | 0        | 1        | 10m   | 1        |
  | 1m       | 1        | 1m    | 2        |
  | 1m       | 3        | 1m    | 2        |
  | 1m       | 3        | 2m    | 3        |
  | 1m       | 3        | 3m    | 3        |
  | 1m       | 3        | 4m    | 4        |
  | 1m       | 3        | 10m   | 4        |
```

#### Scenario: Retry loop stops at shutdown
```gherkin
Given a started root scope with EventRetryInterval 1m
When the consumer shuts the root down
Then no goroutine leaked
And advancing the clock by 10m triggers no Claim call
```

<!-- cases: N/A 6,9: no duplicate or panic input -->

### Requirement: Retention of completed publications
By default `Complete` SHALL be followed by purging that publication. With `InjectorOpts.PublicationRetention` greater than 0, completed publications SHALL be kept, and `Purge` SHALL be called with `now - retention` on each retry tick, or at `Start` when retry is off.

#### Scenario: Purge timing
```gherkin
Given a root scope with a fake clock at 2026-01-01T12:00:00Z, PublicationRetention <retention> and EventRetryInterval <retry>
When the consumer calls Start and then publishes 1 event to a durable listener that succeeds
Then Purge was called <purges>

Examples:
  | retention | retry | purges                                                           |
  | 0         | 0     | once, right after the publication completed, with 2026-01-01T12:00:00Z |
  | 24h       | 0     | once at Start with 2025-12-31T12:00:00Z                          |
  | 24h       | 1m    | at each retry tick with now minus 24h, and not at Start          |
```

<!-- cases: N/A 5,6,9: Purge errors go to the async error handler like delivery errors -->

### Requirement: Store and binder resolution
The root SHALL resolve its `PublicationStore` once, at the first durable publish or at `Start`: `InjectorOpts.PublicationStore`, else a root service named `NameOf[PublicationStore]()`, else the in-memory store. `TxBinder` SHALL resolve the same way, defaulting to a binder that never binds.

#### Scenario: Resolution order
```gherkin
Given a root scope with <opts> and <service>
When the consumer publishes to a durable listener
Then the store used is <used>

Examples:
  | opts                        | service                                           | used        |
  | PublicationStore: storeA    | a service under NameOf[PublicationStore]() = storeB | storeA     |
  | unset                       | a service under NameOf[PublicationStore]() = storeB | storeB     |
  | unset                       | an AsDefault() service storeD, then an app service storeB | storeB |
  | unset                       | a service named "store" = storeB                  | in-memory   |
  | unset                       | none                                              | in-memory   |
```

#### Scenario: In-memory store warns once
```gherkin
Given a root scope with no store configured
When the consumer publishes to a durable listener 3 times
Then Logf was called once with "DI: durable events use the in-memory publication store; publications do not survive a restart"
```

<!-- cases: N/A 4,8: resolution happens once behind a guard (covered by concurrency requirement) -->

### Requirement: Publication identity during delivery
Each publication ID SHALL be 32 lowercase hex characters from `crypto/rand` unless `InjectorOpts.PublicationIDGenerator` is set. During a durable delivery, `PublicationFromContext(ctx)` SHALL return that publication and true; elsewhere it SHALL return false.

#### Scenario: Publication ID and context
```gherkin
Given a root scope with PublicationIDGenerator <generator>
And a durable function listener "mail" that records PublicationFromContext(ctx)
When the consumer publishes OrderPlaced{ID: "1"}
Then "mail" observed ok true and an ID that <id>
And PublicationFromContext(context.Background()) returns ok false

Examples:
  | generator              | id                                   |
  | unset                  | matches ^[0-9a-f]{32}$               |
  | func() string {"p-1"}  | equals "p-1"                         |
```

<!-- cases: N/A 4,6: duplicate IDs from a custom generator are the generator's responsibility; documented -->

### Requirement: Concurrent durable publishes
Concurrent `Publish` calls to durable listeners SHALL each record their own publications with distinct IDs, and each publication SHALL be completed exactly once, including when a redelivery at `Start` runs at the same time.

#### Scenario: Concurrent durable publishing
```gherkin
Given a root scope with the default store and a durable function listener "mail"
When 100 goroutines publish OrderPlaced concurrently while Start redelivers 50 stored publications
Then the run passes under -race within 5s
And 150 distinct publication IDs were completed exactly once each
```

<!-- cases: N/A 3,4,5,6,9: covered by the single-goroutine requirements -->

### Requirement: Clone resolves its own store
A clone SHALL copy durable listener registrations and the event type registry, and SHALL resolve its own store, binder and retry loop on first use rather than sharing the source's resolved ones.

#### Scenario: Clone store isolation
```gherkin
Given a root scope that already published to a durable listener with in-memory store S1
When the consumer clones it and publishes OrderPlaced{ID: "2"} on the clone
Then the clone recorded the publication in a store other than S1
And S1 holds no publication of OrderPlaced{ID: "2"}
```

<!-- cases: N/A 3,4,5,6,8,9: clone takes no input; other rules as for the source -->
