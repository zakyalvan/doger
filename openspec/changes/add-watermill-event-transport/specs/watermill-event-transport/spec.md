# Spec Delta

## Purpose

Lets a doger container send and receive externalized events over any watermill backend (SQL, Kafka, NATS JetStream, Redis Streams and others) through the `doger.EventTransport` contract, with predictable message mapping, acknowledgement, retry, poison handling and shutdown.

## ADDED Requirements

### Requirement: Constructing the transport
`dogerwatermill.New(pub, subs, opts...)` SHALL return a `*Transport` that implements `doger.EventTransport`, or an error matching `ErrInvalidConfig` when `pub` or `subs` is nil or an option is invalid. The transport SHALL NOT start goroutines or call the backend until `Send` or `Subscribe` is called.

#### Scenario: Constructor arguments
```gherkin
Given a fake watermill publisher "pub" and a fake subscriber factory "subs"
When the consumer calls dogerwatermill.New(<pub>, <subs>, <options>)
Then it returns <result>

Examples:
  | pub | subs | options                     | result                                      |
  | pub | subs | no options                  | a *Transport and a nil error                |
  | nil | subs | no options                  | a nil *Transport and an error matching ErrInvalidConfig |
  | pub | nil  | no options                  | a nil *Transport and an error matching ErrInvalidConfig |
  | pub | subs | WithRetryDelay(-1s)         | a nil *Transport and an error matching ErrInvalidConfig |
  | pub | subs | WithMaxAttempts(-1)         | a nil *Transport and an error matching ErrInvalidConfig |
  | pub | subs | WithMetadataPrefix("")      | a nil *Transport and an error matching ErrInvalidConfig |
  | pub | subs | WithRetryDelay(0), WithMaxAttempts(0) | a *Transport and a nil error      |
```

#### Scenario: Construction is passive
```gherkin
Given a fake publisher and a fake subscriber factory that count their calls
When the consumer calls dogerwatermill.New with them
Then the publisher and the factory were called 0 times and no goroutine was started
```

<!-- cases: N/A 6 duplicate: options may repeat; the last wins, as for doger options -->
<!-- cases: N/A 7,8: construction has no lifecycle or shared state -->
<!-- cases: N/A 9 panics: New returns errors and never panics -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Send maps an envelope to a watermill message
`Send(ctx, env)` SHALL publish one message to topic `env.Destination` with UUID `env.ID`, payload `env.Payload`, and metadata `<prefix>event_type`, `<prefix>version`, `<prefix>content_type`, `<prefix>published_at` (RFC 3339 with nanoseconds, UTC) and `<prefix>h_<name>` per header. A publish error SHALL be returned wrapped; a done context SHALL return its error without publishing.

#### Scenario: Message fields
```gherkin
Given a transport over a recording publisher with <options>
When the consumer sends Envelope{ID: "id-1", Destination: "orders.placed", EventType: "orders.placed", Version: 2, ContentType: "application/json", Payload: {"ID":"1"}, Headers: {"trace": "t-9"}, PublishedAt: 2026-01-01T00:00:00Z}
Then the publisher received one message on topic "orders.placed" with UUID "id-1", payload {"ID":"1"} and metadata <metadata>

Examples:
  | options                     | metadata                                                                                                                                  |
  | no options                  | doger_event_type=orders.placed, doger_version=2, doger_content_type=application/json, doger_published_at=2026-01-01T00:00:00Z, doger_h_trace=t-9 |
  | WithMetadataPrefix("x-")    | x-event_type=orders.placed, x-version=2, x-content_type=application/json, x-published_at=2026-01-01T00:00:00Z, x-h_trace=t-9              |
```

#### Scenario: Send failures
```gherkin
Given a transport over a publisher that <behavior>
When the consumer sends an envelope for "orders.placed" with <ctx>
Then Send returns <result>

Examples:
  | behavior               | ctx                                 | result                                                      |
  | returns ErrBrokerDown  | context.Background()                | an error matching ErrBrokerDown                             |
  | returns nil            | an already cancelled context        | an error matching context.Canceled, and nothing was published |
  | panics with "kaboom"   | context.Background()                | an error whose message contains "kaboom"                    |
```

<!-- cases: N/A 3 zero values: empty headers produce no h_ keys (covered by the round trip requirement) -->
<!-- cases: N/A 4 boundaries: payload size limits belong to the backend -->
<!-- cases: N/A 8 concurrency: covered by "The transport is safe for concurrent use" -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Subscribe builds one subscriber per subscription
`Subscribe(ctx, sub, h)` SHALL call the factory once with `sub`, subscribe the returned subscriber to `sub.Destination`, and return a stop function. A factory or subscribe error SHALL be returned, and a subscriber already built SHALL be closed.

#### Scenario: Subscribe outcomes
```gherkin
Given a transport whose factory <factory>
When the consumer subscribes Subscription{Destination: "orders.placed", Group: "mail", Mode: DeliverOnce}
Then <outcome>

Examples:
  | factory                                                         | outcome                                                                        |
  | returns a fake subscriber                                       | the factory received that Subscription, the fake was subscribed to "orders.placed", and a non-nil stop is returned |
  | returns ErrNoBroker                                             | Subscribe returns an error matching ErrNoBroker                                |
  | returns a fake subscriber whose Subscribe returns ErrNoTopic    | Subscribe returns an error matching ErrNoTopic and the fake subscriber was closed |
  | returns nil, nil                                                | Subscribe returns an error matching ErrInvalidConfig                           |
```

<!-- cases: N/A 4,6: one subscriber per call; repeated calls build repeated subscribers, owned by their stop functions -->
<!-- cases: N/A 9 panics: a panicking factory is recovered and returned as an error -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Received messages map back to envelopes
For each received message carrying `<prefix>event_type`, the handler SHALL receive the envelope `Send` would have produced it from, with the message's context. A message without that key SHALL NOT reach the handler and SHALL be treated as a permanent failure.

#### Scenario: Round trip through a fake broker
```gherkin
Given a transport over an in-process fake broker that hands published messages to the subscriber for the same topic
And a subscription to "orders.placed" whose handler records envelopes
When the consumer sends <envelope>
Then the handler received exactly <received>

Examples:
  | envelope                                                                               | received                     |
  | an envelope with ID "id-1", Version 2, Headers {"trace": "t-9", "tenant": "acme"}      | an envelope equal to the sent one |
  | an envelope with ID "id-2" and no headers                                             | an envelope equal to the sent one, with empty Headers |
```

#### Scenario: Foreign messages
```gherkin
Given a subscription to "orders.placed" whose handler counts calls, with <options>
When the backend delivers a message with UUID "m-1" and no doger metadata
Then the handler was called 0 times and <outcome>

Examples:
  | options                       | outcome                                                       |
  | no options                    | the message was acked and the logger recorded one dropped message |
  | WithPoisonTopic("poison")     | the message was published to "poison", then acked             |
```

<!-- cases: N/A 4,6: a duplicate message reaches the handler again; deduplication is the listener's job (EnvelopeFromContext(ctx).ID) -->
<!-- cases: N/A 8 concurrency: messages of one subscription are handled one at a time, as watermill delivers the next message after Ack or Nack -->
<!-- cases: N/A 9 panics: handler panics are recovered by doger core before they reach the adapter -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Acknowledgement, retries and poison handling
The adapter SHALL ack when the handler returns nil. On another error it SHALL wait the retry delay, then nack. After the maximum attempts for one message UUID, or on an error for which `doger.IsPermanentError` is true, it SHALL publish the message to the poison topic and ack, or ack and log when no poison topic is set. A failed poison publish SHALL nack.

#### Scenario: Handler outcomes
```gherkin
Given a subscription with <options> whose handler <behavior>
When the backend delivers message "m-1" until it is acked
Then the handler ran <runs> times, the message was nacked <nacks> times and acked once, and <where>

Examples:
  | options                                       | behavior                                    | runs | nacks | where                                   |
  | no options                                    | returns nil                                 | 1    | 0     | nothing was logged                      |
  | no options                                    | fails twice with ErrOutOfStock, then returns nil | 3 | 2  | nothing was logged                      |
  | WithMaxAttempts(3)                            | returns ErrOutOfStock every time            | 3    | 2     | the logger recorded one dropped message |
  | WithMaxAttempts(3), WithPoisonTopic("poison") | returns ErrOutOfStock every time            | 3    | 2     | "m-1" was published to "poison"         |
  | no options                                    | returns doger.PermanentError(ErrBadOrder)   | 1    | 0     | the logger recorded one dropped message |
  | WithPoisonTopic("poison")                     | returns doger.PermanentError(ErrBadOrder)   | 1    | 0     | "m-1" was published to "poison"         |
  | WithMaxAttempts(0)                            | fails 20 times with ErrOutOfStock, then returns nil | 21 | 20 | nothing was logged                   |
```

#### Scenario: Retry delay
```gherkin
Given a subscription with WithClock(a fake clock), WithRetryDelay(<delay>) and a handler that fails once with ErrOutOfStock
When the backend delivers message "m-1"
Then the nack happened after the fake clock advanced <delay>

Examples:
  | delay |
  | 1s    |
  | 0s    |
  | 30s   |
```

#### Scenario: Poison publish failure
```gherkin
Given a subscription with WithPoisonTopic("poison") whose publisher fails on "poison" with ErrBrokerDown and a handler returning doger.PermanentError(ErrBadOrder)
When the backend delivers message "m-1"
Then the message was nacked and the logger recorded ErrBrokerDown
```

<!-- cases: N/A 3 zero values: WithRetryDelay(0) and WithMaxAttempts(0) are rows above -->
<!-- cases: N/A 6 duplicate: attempts are counted per message UUID and reset when the message is acked -->
<!-- cases: N/A 9 panics: handler panics arrive as errors from doger core -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: Stop ends the subscription
The stop function SHALL cancel consumption, wait for the handler in flight to return or for its context to end, then close the subscriber the factory built, and return nil on success. A second call SHALL return nil. Cancelling the context given to `Subscribe` SHALL also end consumption.

#### Scenario: Stop waits for the handler
```gherkin
Given a subscription whose handler is running and blocks until "release" is closed
When the consumer calls stop with context.Background() and closes "release" after 50ms
Then stop returned after the handler returned, the subscriber was closed once, and no goroutine leaked
```

#### Scenario: Stop context bounds the wait
```gherkin
Given a subscription whose handler is running and blocks until its context is done
When the consumer calls stop with a context that times out after 50ms
Then stop returns an error matching context.DeadlineExceeded within 1s, the handler's context was cancelled, and the subscriber was closed
```

#### Scenario: Stop edge cases
```gherkin
Given a subscription <state>
When the consumer <action>
Then <outcome>

Examples:
  | state                                        | action                                    | outcome                                                     |
  | that is idle                                 | calls stop twice                          | both calls return nil and the subscriber was closed once    |
  | that is idle                                 | cancels the context given to Subscribe    | consumption ends within 1s and no goroutine leaked          |
  | whose subscriber's Close returns ErrClose    | calls stop                                | stop returns an error matching ErrClose                     |
```

<!-- cases: N/A 3,4: no values beyond the contexts -->
<!-- cases: N/A 9 panics: a panicking Close is recovered and returned as an error -->
<!-- cases: N/A 10 compatibility: new module -->

### Requirement: The transport is safe for concurrent use
`Send` and `Subscribe` SHALL be safe to call from many goroutines, and different subscriptions SHALL consume concurrently.

#### Scenario: Concurrent sends and subscriptions
```gherkin
Given a transport over an in-process fake broker with 10 subscriptions to distinct topics
When 50 goroutines each send 20 envelopes spread over those topics
Then the handlers received 1000 envelopes in total, each exactly once, and the race detector reports nothing
```

<!-- cases: N/A 1-7,9,10: covered by the other requirements; this one is concurrency only -->

### Requirement: The transport passes the conformance suite
The transport SHALL pass `eventtransporttest.Run` when built over watermill-sql with `dogerwmsql` subscriber factories on PostgreSQL and on MySQL.

#### Scenario: Conformance on SQL backends
```gherkin
Given a <database> reachable through <env>
And a factory that returns dogerwatermill.New(<publisher>, <subscriberFactory>) over fresh tables
When eventtransporttest.Run(t, factory) runs
Then every subtest passes with -race and no goroutine leaks

Examples:
  | database    | env                     | publisher                         | subscriberFactory                    |
  | PostgreSQL  | DOGER_TEST_POSTGRES_DSN | a watermill-sql Postgres publisher | dogerwmsql.PostgresSubscriberFactory |
  | MySQL       | DOGER_TEST_MYSQL_DSN    | a watermill-sql MySQL publisher    | dogerwmsql.MySQLSubscriberFactory    |
```

#### Scenario: Missing database configuration
```gherkin
Given DOGER_TEST_POSTGRES_DSN is unset and the CI environment variable is <ci>
When the PostgreSQL conformance test runs
Then the test <outcome>

Examples:
  | ci     | outcome                                                |
  | unset  | is skipped with the message "set DOGER_TEST_POSTGRES_DSN to run" |
  | "true" | fails with the message "DOGER_TEST_POSTGRES_DSN is required in CI" |
```

<!-- cases: N/A 2-9: the suite owns these cases -->
<!-- cases: N/A 10 compatibility: new module -->
