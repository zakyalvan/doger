# Spec Delta

## Purpose

Gives event types a stable wire identity (name, version, destination and aliases) and defines the envelope an event travels in between processes, with a replaceable codec, ID generator and header propagator.

## ADDED Requirements

### Requirement: RegisterEventType binds a wire name to a type
`RegisterEventType[E](root, name, opts...) error` SHALL bind `name` to the concrete type `E` in the root's registry. A nil root SHALL return an error matching `ErrNilInjector` and register nothing. Registering the same name for the same type again SHALL return nil and apply the newer options.

#### Scenario: Registering event types
```gherkin
Given a root scope
And <existing>
When the consumer calls RegisterEventType[<type>](root, "<name>")
Then it returns <error>
And the registry resolves "<name>" to <resolved>

Examples:
  | existing                                         | type          | name           | error                                 | resolved     |
  | no registration                                  | OrderPlaced   | orders.placed  | nil                                   | OrderPlaced  |
  | no registration                                  | *OrderPlaced  | orders.placed  | nil                                   | *OrderPlaced |
  | "orders.placed" registered for OrderPlaced       | OrderPlaced   | orders.placed  | nil                                   | OrderPlaced  |
  | "orders.placed" registered for OrderPlaced       | OrderCancelled | orders.placed | an error matching ErrEventTypeConflict | OrderPlaced  |
  | "orders.placed" registered for OrderPlaced       | OrderPlaced   | orders.v2      | an error matching ErrEventTypeConflict | OrderPlaced  |
  | no registration                                  | OrderPlaced   |                | an error matching ErrInvalidEventType | nothing      |
  | no registration                                  | DomainEvent   | domain.event   | an error matching ErrInvalidEventType | nothing      |
  | no registration                                  | any           | anything       | an error matching ErrInvalidEventType | nothing      |
```
`DomainEvent` is an interface type: the wire needs a concrete type to decode into.

#### Scenario: A nil root is rejected
```gherkin
Given no root scope
When the consumer calls RegisterEventType[OrderPlaced](nil, "orders.placed")
Then it returns an error matching ErrNilInjector
And no registry resolves "orders.placed"
```

<!-- cases: N/A 4 boundaries: names have no length limit in core; transports validate their own destination syntax -->
<!-- cases: N/A 7 lifecycle: registration is allowed before and after Start; see "Registration is safe while events flow" -->
<!-- cases: N/A 9 panics: RegisterEventType returns errors and never panics -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Event type options
`EventVersion(n)` SHALL set the type's version (default 1; 0 is invalid). `EventDestination(d)` SHALL set the destination (default: the name; "" is invalid). `EventAlias(names...)` SHALL add names that decode to the type. An invalid option SHALL make `RegisterEventType` return an error matching `ErrInvalidEventType` and register nothing.

#### Scenario: Version, destination and aliases
```gherkin
Given a root scope
When the consumer calls RegisterEventType[OrderPlaced](root, "orders.placed", <options>)
Then it returns <error>
And the type's version is <version>, its destination is <destination> and "<lookup>" decodes to <decoded>

Examples:
  | options                                      | error                                 | version | destination      | lookup           | decoded     |
  | no options                                   | nil                                   | 1       | "orders.placed"  | orders.placed    | OrderPlaced |
  | EventVersion(3)                              | nil                                   | 3       | "orders.placed"  | orders.placed    | OrderPlaced |
  | EventDestination("shop.orders")              | nil                                   | 1       | "shop.orders"    | orders.placed    | OrderPlaced |
  | EventAlias("order.created")                  | nil                                   | 1       | "orders.placed"  | order.created    | OrderPlaced |
  | EventVersion(0)                              | an error matching ErrInvalidEventType | none    | none             | orders.placed    | nothing     |
  | EventDestination("")                         | an error matching ErrInvalidEventType | none    | none             | orders.placed    | nothing     |
  | EventAlias("")                               | an error matching ErrInvalidEventType | none    | none             | orders.placed    | nothing     |
  | EventVersion(2), EventVersion(4)             | nil                                   | 4       | "orders.placed"  | orders.placed    | OrderPlaced |
```

#### Scenario: An alias bound to another type conflicts
```gherkin
Given a root scope with "order.created" registered for OrderCancelled
When the consumer calls RegisterEventType[OrderPlaced](root, "orders.placed", EventAlias("order.created"))
Then it returns an error matching ErrEventTypeConflict
And "orders.placed" is not registered
```

<!-- cases: N/A 7 lifecycle: options apply at registration only -->
<!-- cases: N/A 8 concurrency: covered by "Registration is safe while events flow" -->
<!-- cases: N/A 9 panics: options are validated by RegisterEventType, which returns errors -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Destination mapping
The destination an externalized event is sent to and a remote listener subscribes to SHALL be `InjectorOpts.EventDestinationMapper(destination)` when the mapper is set, and the type's destination otherwise. A mapper returning "" SHALL fail that send or subscription with an error matching `ErrInvalidEventType`.

#### Scenario: The mapper applies to both sides
```gherkin
Given a root scope with InjectorOpts.EventDestinationMapper <mapper> and a recording transport
And RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalized()) returned nil
And a function listener for OrderPlaced with ListenerID("mail") and ListenerDelivery(DeliverOnce)
When the consumer starts the root and publishes OrderPlaced{ID: "1"}
Then <outcome>

Examples:
  | mapper                          | outcome                                                                  |
  | unset                           | the transport received a send to "orders.placed" and a subscription to "orders.placed" |
  | func(d) { return "prod." + d }  | the transport received a send to "prod.orders.placed" and a subscription to "prod.orders.placed" |
  | func(d) { return "" }           | Start returns an error matching ErrInvalidEventType                      |
```

<!-- cases: N/A 3 zero values: an unset mapper is the first row -->
<!-- cases: N/A 4 boundaries: no numeric input -->
<!-- cases: N/A 9 panics: a panicking mapper is a programming error and follows the caller's goroutine; not recovered -->
<!-- cases: N/A 10 compatibility: new option -->

### Requirement: The default codec is JSON
The root SHALL encode payloads with `InjectorOpts.EventCodec`, defaulting to an `encoding/json` codec whose `ContentType()` is "application/json". Decoding SHALL produce a value of the registered type, a pointer when the registered type is a pointer.

#### Scenario: Round trip through the codec
```gherkin
Given a root scope with InjectorOpts.EventCodec <codec>
And RegisterEventType[<type>](root, "orders.placed") returned nil
When an envelope built from <event> is decoded by the same root
Then the decoded value is <decoded> and the envelope's content type is "<contentType>"

Examples:
  | codec                       | type          | event                    | decoded                  | contentType          |
  | unset                       | OrderPlaced   | OrderPlaced{ID: "1"}     | OrderPlaced{ID: "1"}     | application/json     |
  | unset                       | *OrderPlaced  | &OrderPlaced{ID: "1"}    | &OrderPlaced{ID: "1"}    | application/json     |
  | a fake codec "text/x-test"  | OrderPlaced   | OrderPlaced{ID: "1"}     | OrderPlaced{ID: "1"}     | text/x-test          |
```

#### Scenario: Encoding failures fail the externalization
```gherkin
Given a root scope with a codec whose Marshal returns ErrCodecBroken
And RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeImmediate)) returned nil and a recording transport
When the consumer publishes OrderPlaced{ID: "1"}
Then Publish returns an *ExternalizeError matching ErrCodecBroken
And the transport received no send
```

<!-- cases: N/A 4 boundaries: payload size limits belong to transports -->
<!-- cases: N/A 6 duplicate: one codec per root -->
<!-- cases: N/A 8 concurrency: codecs are called concurrently and must be safe for that; the default is -->
<!-- cases: N/A 10 compatibility: new option -->

### Requirement: Envelopes carry identity and metadata
An externalized event SHALL travel as an `Envelope` with a non-empty `ID`, the mapped `Destination`, the registered `EventType` name and `Version`, the codec's `ContentType`, the encoded `Payload`, `Headers` from the propagator, and `PublishedAt` from the root clock. IDs SHALL be UUIDv7 unless `InjectorOpts.EventIDGenerator` is set.

#### Scenario: Envelope fields
```gherkin
Given a root scope with a fake clock at 2026-01-01T00:00:00Z, a recording transport and InjectorOpts.EventIDGenerator <generator>
And RegisterEventType[OrderPlaced](root, "orders.placed", EventVersion(2), EventExternalize(ExternalizeImmediate)) returned nil
When the consumer publishes OrderPlaced{ID: "1"}
Then <outcome>

Examples:
  | generator                       | outcome                                                                                                                         |
  | unset                           | the transport received one envelope with a UUIDv7 ID whose timestamp is 2026-01-01T00:00:00Z, Destination "orders.placed", EventType "orders.placed", Version 2, ContentType "application/json", Payload {"ID":"1"} and PublishedAt 2026-01-01T00:00:00Z |
  | func() string { return "id-7" } | the transport received one envelope with ID "id-7"                                                                              |
  | func() string { return "" }     | Publish returns an *ExternalizeError matching ErrInvalidEnvelope and the transport received no send                                  |
```

#### Scenario: Default IDs are unique and time ordered
```gherkin
Given a root scope with a fake clock and a recording transport
And RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeImmediate)) returned nil
When the consumer publishes 1000 events from 10 goroutines while the fake clock advances 1ms between batches
Then the transport received 1000 distinct IDs
And IDs from a later batch sort after IDs from an earlier batch
```

<!-- cases: N/A 3 zero values: an empty generated ID is the third row -->
<!-- cases: N/A 6 duplicate: duplicates from a custom generator are the generator's responsibility; transports and listeners deduplicate by ID -->
<!-- cases: N/A 9 panics: a panicking generator fails the externalization like a codec error, as an *ExternalizeError -->
<!-- cases: N/A 10 compatibility: new behavior -->

### Requirement: Headers propagate through the envelope
When `InjectorOpts.EventPropagator` is set, its `Inject(ctx)` result SHALL become the envelope's headers on send, and its `Extract(ctx, headers)` SHALL derive the context a remote listener receives. Unset, headers SHALL be empty and listener contexts SHALL carry no extracted values.

#### Scenario: Trace context crosses the transport
```gherkin
Given two root scopes "a" and "b" sharing one in-memory transport, each with a propagator that copies context value "trace" to header "trace" and back
And RegisterEventType[OrderPlaced](..., "orders.placed", EventExternalize(ExternalizeImmediate)) on "a" and RegisterEventType[OrderPlaced](..., "orders.placed") on "b"
And on "b" a started function listener for OrderPlaced with ListenerID("mail") and ListenerDelivery(DeliverOnce) that records its context
When "a" publishes OrderPlaced{ID: "1"} with a context holding "trace"="t-9"
Then the listener on "b" ran once with context value "trace"="t-9"
And EnvelopeFromContext on that context returns the envelope with Headers {"trace": "t-9"}
```

#### Scenario: No propagator
```gherkin
Given the same two roots without a propagator
When "a" publishes OrderPlaced{ID: "1"} with a context holding "trace"="t-9"
Then the listener on "b" ran once with no "trace" value
And the envelope's Headers are empty
```

<!-- cases: N/A 4,6: no numeric or duplicate input -->
<!-- cases: N/A 8 concurrency: Inject and Extract are called per event and must be safe for concurrent use -->
<!-- cases: N/A 9 panics: an Inject panic fails the externalization as an *ExternalizeError; an Extract panic fails that delivery as a permanent error -->
<!-- cases: N/A 10 compatibility: new option -->

### Requirement: Registration is safe while events flow
`RegisterEventType` SHALL be safe to call concurrently with `Publish`, with other registrations and with inbound deliveries. A publish SHALL see either the registry before or after a concurrent registration, never a partial one.

#### Scenario: Concurrent registration and publishing
```gherkin
Given a root scope with an in-memory transport and RegisterEventType[OrderPlaced](root, "orders.placed", EventExternalize(ExternalizeImmediate)) returned nil
When 50 goroutines publish OrderPlaced while 50 goroutines register 50 distinct other event types
Then every Publish returns nil, every RegisterEventType returns nil, and the race detector reports nothing
```

<!-- cases: N/A 1-7,9,10: covered by the other requirements; this one is concurrency only -->

### Requirement: Clones copy the event type registry
`RootScope.Clone()` and `CloneWithOpts` SHALL copy the registry. A registration made on the clone or the original afterwards SHALL NOT appear in the other.

#### Scenario: Registry isolation after a clone
```gherkin
Given a root scope with "orders.placed" registered for OrderPlaced
And the consumer cloned it
When the consumer registers "orders.cancelled" for OrderCancelled on the clone
Then the clone resolves both names
And the original resolves "orders.placed" only
```

<!-- cases: N/A 3,4,5,9: no inputs beyond the clone call -->
<!-- cases: N/A 8 concurrency: covered by "Registration is safe while events flow" -->
<!-- cases: N/A 10 compatibility: clones keep their add-eventing behavior for listeners -->
