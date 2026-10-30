# Spec Delta

## Purpose

Lets consumers publish typed in-process events from any injector and react to them with lazily built listener services or functions, with predictable ordering, scope reach, error handling, async delivery and lifecycle integration, without writing their own event bus.

## ADDED Requirements

### Requirement: Publish delivers by assignability
`Publish[E](ctx, i, event)` SHALL deliver the event to every reachable listener declared for a type `L` to which the event's dynamic type is assignable, and SHALL return nil when no listener matches. A listener declared for an interface type SHALL receive every event that implements it.

#### Scenario: Publish matches listeners by the event's dynamic type
```gherkin
Given a root scope
And a function listener for "<listened>" that records the events it receives
When the consumer publishes <published> as a value of static type "<static>"
Then the listener received <received> events

Examples:
  | listened         | published                    | static       | received |
  | OrderPlaced      | OrderPlaced{ID: "42"}        | OrderPlaced  | 1        |
  | DomainEvent      | OrderPlaced{ID: "42"}        | OrderPlaced  | 1        |
  | OrderPlaced      | OrderPlaced{ID: "42"}        | DomainEvent  | 1        |
  | OrderPlaced      | OrderCancelled{ID: "42"}     | OrderCancelled | 0      |
  | *OrderPlaced     | OrderPlaced{ID: "42"}        | OrderPlaced  | 0        |
  | any              | OrderPlaced{ID: "42"}        | OrderPlaced  | 1        |
```

#### Scenario: Publish with no listeners succeeds
```gherkin
Given a root scope with no listener registered
When the consumer publishes OrderPlaced{ID: "42"}
Then Publish returns nil
And no service of the root scope was built
```

<!-- cases: N/A 6 duplicate: several listeners for one type are all delivered (see ordering requirement) -->
<!-- cases: N/A 9 panics: listener panics are covered by "Listener panics are recovered" -->

### Requirement: Publish argument handling
`Publish` SHALL return an error matching `ErrInvalidEvent` when the event is a nil interface value or a nil pointer, without calling any listener. A nil injector SHALL mean `DefaultRootScope`, and a nil context SHALL reach listeners as a non-nil context.

#### Scenario: Publish rejects or normalizes zero arguments
```gherkin
Given a function listener for "<listened>" registered on <scope>
When the consumer publishes <event> with context <ctx> through injector <injector>
Then Publish returns <error>
And the listener was called <calls> times with a non-nil context

Examples:
  | listened     | scope            | event                       | ctx                  | injector         | error           | calls |
  | DomainEvent  | a new root       | nil DomainEvent             | context.Background() | the root         | ErrInvalidEvent | 0     |
  | *OrderPlaced | a new root       | (*OrderPlaced)(nil)         | context.Background() | the root         | ErrInvalidEvent | 0     |
  | OrderPlaced  | DefaultRootScope | OrderPlaced{ID: "42"}       | context.Background() | nil              | nil             | 1     |
  | OrderPlaced  | a new root       | OrderPlaced{ID: "42"}       | nil                  | the root         | nil             | 1     |
  | OrderPlaced  | a new root       | OrderPlaced{}               | context.Background() | the root         | nil             | 1     |
```

<!-- cases: N/A 4 boundaries: no numeric input -->
<!-- cases: N/A 7 lifecycle: covered by the shutdown requirements -->

### Requirement: AsListener declares a listener service
`AsListener[E](opts...)` SHALL opt a registration into receiving events of type `E` through its `Listener[E]` method `OnEvent(context.Context, E) error`. A concrete registered type that does not implement `Listener[E]` SHALL panic at registration, before the service is stored, with "DI: service `<name>` does not implement Listener[<E>]".

#### Scenario: AsListener validates the registered type
```gherkin
Given a root scope
When the consumer registers a lazy service of type "<registered>" named "<name>" with AsListener[OrderPlaced]()
Then registration <outcome>

Examples:
  | registered    | name      | outcome                                                                                   |
  | *Mailer       | *Mailer   | succeeds                                                                                  |
  | *Auditor      | *Auditor  | panics with "DI: service `*Auditor` does not implement Listener[OrderPlaced]" and stores nothing |
  | MailerIface   | mailer    | succeeds (checked on the instance at first dispatch)                                       |
```

#### Scenario: Interface-typed listener is checked at first dispatch
```gherkin
Given a lazy service "mailer" registered as interface "Notifier" with AsListener[OrderPlaced]()
And its provider returns a value that does not implement Listener[OrderPlaced]
When the consumer publishes OrderPlaced{ID: "42"}
Then Publish returns a *ListenerError whose Listener is "[root]/mailer#github.com/kartaladev/doger.OrderPlaced"
And the error matches ErrInvalidListener
```

#### Scenario: Repeating AsListener for the same type registers one listener
```gherkin
Given a lazy service "*Mailer" registered with AsListener[OrderPlaced]() twice
When the consumer publishes OrderPlaced{ID: "42"}
Then Mailer.OnEvent was called 1 time
```

<!-- cases: N/A 4 boundaries: no numeric input -->
<!-- cases: N/A 8 concurrency: registration concurrency is covered by "Concurrent publish and registration" -->

### Requirement: Listener services are built lazily
A listener service SHALL NOT be built before the first event that reaches it. A lazy or eager listener SHALL then be reused for later events, and a transient listener SHALL be built once per delivery. A provider error SHALL fail that delivery with a `*ListenerError` wrapping the provider error.

#### Scenario: Listener build follows the service kind
```gherkin
Given a <kind> service "*Mailer" registered with AsListener[OrderPlaced]() whose provider counts its calls
When the consumer publishes OrderPlaced{ID: "1"} <times> times
Then the provider was called <builds> times

Examples:
  | kind      | times | builds |
  | lazy      | 0     | 0      |
  | lazy      | 1     | 1      |
  | lazy      | 3     | 1      |
  | transient | 3     | 3      |
```

#### Scenario: Listener provider error fails the delivery
```gherkin
Given a lazy service "*Mailer" with AsListener[OrderPlaced]() whose provider returns error "boom"
When the consumer publishes OrderPlaced{ID: "1"}
Then Publish returns a *ListenerError whose Listener is "[root]/*Mailer#github.com/kartaladev/doger.OrderPlaced"
And errors.Unwrap of it contains "boom"
```

<!-- cases: N/A 3 zero values: no input value -->
<!-- cases: N/A 6 duplicate: a service is one listener per event type -->

### Requirement: ListenMethod declares a method listener
`ListenMethod[S, E](method, opts...)` SHALL opt a registration of type `S` into receiving events of type `E` through `method(instance, ctx, event)`, so one service can listen to several event types. A registered type other than `S`, or a nil method, SHALL panic at registration with a "DI: " message naming the service.

#### Scenario: One service listens to several event types
```gherkin
Given a lazy service "*Mailer" registered with ListenMethod((*Mailer).OnPlaced) and ListenMethod((*Mailer).OnCancelled)
When the consumer publishes OrderPlaced{ID: "1"} and then OrderCancelled{ID: "1"}
Then Mailer recorded ["placed:1", "cancelled:1"]
And the provider of "*Mailer" was called 1 time
```

#### Scenario: ListenMethod validates its input
```gherkin
Given a root scope
When the consumer registers a service of type "<registered>" with ListenMethod(<method>)
Then registration <outcome>

Examples:
  | registered | method               | outcome                                                                         |
  | *Mailer    | (*Mailer).OnPlaced   | succeeds                                                                        |
  | *Auditor   | (*Mailer).OnPlaced   | panics with "DI: service `*Auditor` is not of type *Mailer required by ListenMethod" |
  | *Mailer    | nil                  | panics with "DI: ListenMethod requires a non-nil method for service `*Mailer`"   |
```

<!-- cases: N/A 4 boundaries: no numeric input -->

### Requirement: Listen registers a function listener
`Listen[E](i, fn, opts...)` SHALL register `fn` as a listener in the scope of `i` and return a cancel function. Calling cancel SHALL unregister it; later calls of cancel SHALL do nothing. A nil `fn` SHALL panic with "DI: Listen requires a non-nil function". A nil injector SHALL mean `DefaultRootScope`.

#### Scenario: Cancel unregisters a function listener
```gherkin
Given a function listener for OrderPlaced registered with Listen on a root scope
When the consumer calls cancel <cancels> times and then publishes OrderPlaced{ID: "1"}
Then the function was called <calls> times

Examples:
  | cancels | calls |
  | 0       | 1     |
  | 1       | 0     |
  | 2       | 0     |
```

#### Scenario: Listen rejects a nil function
```gherkin
Given a root scope
When the consumer calls Listen[OrderPlaced] with a nil function
Then it panics with "DI: Listen requires a non-nil function"
```

<!-- cases: N/A 6 duplicate: the same function registered twice is two listeners and is delivered twice -->

### Requirement: Listener order is deterministic
Synchronous listeners SHALL be called in ascending `ListenerOrder` (default 0), then by scope depth with the publishing scope first and its ancestors after it, then in registration order. Order SHALL be determined without building any listener; the `Ordered` interface SHALL NOT affect it.

#### Scenario: Listeners run in order, depth, then registration order
```gherkin
Given a root scope with function listeners "r1" (order 0) and "r2" (order -1) for OrderPlaced
And a child scope "req" with function listeners "c1" (order 0) and "c2" (order 0) for OrderPlaced
And a service "*Mailer" in "req" with AsListener[OrderPlaced]() that implements Ordered returning -10
When the consumer publishes OrderPlaced{ID: "1"} through "req"
Then the listeners ran in the order ["r2", "c1", "c2", "*Mailer", "r1"]
```

<!-- cases: N/A 3 zero values: zero order is the default in the scenario -->
<!-- cases: N/A 5 errors: ordering has no error path -->

### Requirement: Listener filter
`ListenerFilter(pred)` SHALL skip delivery of an event to that listener when `pred(event)` returns false, without building the listener. A nil predicate SHALL mean no filter. A predicate panic SHALL be recovered into a `*ListenerError` for that listener.

#### Scenario: Filter decides delivery
```gherkin
Given a lazy service "*Mailer" with AsListener[OrderPlaced](ListenerFilter(<pred>))
When the consumer publishes OrderPlaced{Amount: <amount>}
Then Mailer.OnEvent was called <calls> times
And the provider of "*Mailer" was called <builds> times
And Publish returns <error>

Examples:
  | pred                   | amount | calls | builds | error                                  |
  | e.Amount > 100         | 150    | 1     | 1      | nil                                    |
  | e.Amount > 100         | 100    | 0     | 0      | nil                                    |
  | nil                    | 5      | 1     | 1      | nil                                    |
  | panics with "bad"      | 5      | 0     | 0      | *ListenerError containing "bad"        |
```

<!-- cases: N/A 6 duplicate: one filter per listener; the last ListenerFilter option wins -->

### Requirement: Event reach across scopes
An event published through injector `i` SHALL reach listeners registered in `i`'s scope, its ancestors, and the root's extension private scopes, and SHALL NOT reach listeners in sibling or descendant scopes. `PublishLocal()` SHALL restrict delivery to `i`'s own scope.

#### Scenario: Reach depends on where the listener lives
```gherkin
Given a root scope with child scopes "tenant-a" and "tenant-b"
And a child scope "req" under "tenant-a"
And an extension private scope "ext:audit" installed with doger.Extend
And a function listener for OrderPlaced in "<listener-scope>"
When the consumer publishes OrderPlaced{ID: "1"} through "<publish-scope>" with options <opts>
Then the listener was called <calls> times

Examples:
  | listener-scope | publish-scope | opts           | calls |
  | req            | req           | none           | 1     |
  | tenant-a       | req           | none           | 1     |
  | [root]         | req           | none           | 1     |
  | tenant-b       | req           | none           | 0     |
  | req            | tenant-a      | none           | 0     |
  | ext:audit      | req           | none           | 1     |
  | ext:audit      | [root]        | none           | 1     |
  | [root]         | req           | PublishLocal() | 0     |
  | req            | req           | PublishLocal() | 1     |
```

<!-- cases: N/A 3 zero values: nil injector is covered by "Publish argument handling" -->
<!-- cases: N/A 5 errors: reach has no error path -->

### Requirement: Fail-fast is the default error mode
By default the first synchronous listener error SHALL stop dispatch, and `Publish` SHALL return a `*ListenerError` with the listener's ID, the event's type and the cause, matchable with `errors.As` and unwrapping to the cause. Its message SHALL be "DI: listener `<id>` failed on event `<type>`: <cause>".

#### Scenario: First error stops dispatch
```gherkin
Given function listeners "a", "b" and "c" for OrderPlaced registered in that order with ListenerID
And listener "b" returns ErrOutOfStock
When the consumer publishes OrderPlaced{ID: "1"}
Then listeners "a" and "b" ran and "c" did not
And errors.As finds a *ListenerError with Listener "b" and Event type OrderPlaced
And the error matches ErrOutOfStock
And its message is "DI: listener `b` failed on event `doger.OrderPlaced`: out of stock"
```

<!-- cases: N/A 3 zero values: a nil error from a listener is the happy path -->
<!-- cases: N/A 4 boundaries: covered by EventContinue scenario -->

### Requirement: EventContinue runs every listener
With `InjectorOpts.EventErrorMode` set to `EventContinue`, every synchronous listener SHALL run, and `Publish` SHALL return the `errors.Join` of every `*ListenerError` in listener order, or nil when none failed.

#### Scenario: Errors are joined in continue mode
```gherkin
Given a root scope with EventErrorMode <mode>
And function listeners "a", "b", "c" for OrderPlaced where <failing> return an error
When the consumer publishes OrderPlaced{ID: "1"}
Then the listeners that ran are <ran>
And Publish returns errors naming <named>

Examples:
  | mode          | failing  | ran           | named    |
  | EventContinue | none     | ["a","b","c"] | []       |
  | EventContinue | ["b"]    | ["a","b","c"] | ["b"]    |
  | EventContinue | ["a","c"]| ["a","b","c"] | ["a","c"]|
  | EventFailFast | ["a","c"]| ["a"]         | ["a"]    |
```

<!-- cases: N/A 6 duplicate: not applicable to error collection -->

### Requirement: Listener panics are recovered
A panic in a listener SHALL be recovered into a `*ListenerError` for that listener and treated as an error under the current error mode. An error panic value SHALL be wrapped with `%w`; any other value SHALL be formatted with `%v`.

#### Scenario: Panic becomes a listener error
```gherkin
Given a function listener "p" for OrderPlaced that panics with <value>
When the consumer publishes OrderPlaced{ID: "1"}
Then Publish returns a *ListenerError for "p"
And <match>

Examples:
  | value               | match                                  |
  | ErrOutOfStock       | the error matches ErrOutOfStock        |
  | "kaboom"            | the error message contains "kaboom"    |
  | 42                  | the error message contains "42"        |
  | nil                 | the error message contains "nil"       |
```

<!-- cases: N/A 4 boundaries: not applicable -->

### Requirement: Nested publish completes before the outer dispatch continues
A listener SHALL be able to call `Publish` during dispatch without deadlocking. The nested dispatch SHALL complete before the next listener of the outer dispatch runs, and an error it returns to the listener SHALL be handled by that listener's own return value.

#### Scenario: Nested publish runs inline
```gherkin
Given function listener "a" for OrderPlaced that publishes InvoiceRequested{ID: "1"} and then records "a"
And function listener "b" for OrderPlaced that records "b"
And function listener "i" for InvoiceRequested that records "i"
When the consumer publishes OrderPlaced{ID: "1"}
Then the recorded order is ["i", "a", "b"]
```

<!-- cases: N/A 3,4,5,6: re-entrancy concerns only ordering; errors follow the listener's own return -->

### Requirement: Async delivery
`Async()` SHALL deliver the event to that listener on the event executor instead of the publisher's goroutine. `Publish` SHALL return after submitting it, without waiting for the listener. The listener's context SHALL keep the publisher context's values and SHALL NOT be cancelled when the publisher's context is.

#### Scenario: Async listener runs after Publish returns
```gherkin
Given a function listener "x" for OrderPlaced with Async() that blocks until channel "release" is closed
When the consumer publishes OrderPlaced{ID: "1"} with a context carrying value "trace"="t1" and cancels that context after Publish returns
And then closes "release"
Then Publish returned nil before "x" finished
And "x" observed value "trace"="t1" and a context whose Err is nil
```

#### Scenario: Async errors go to the async error handler
```gherkin
Given a root scope with EventAsyncErrorHandler <handler>
And a function listener "x" for OrderPlaced with Async() that returns ErrOutOfStock
When the consumer publishes OrderPlaced{ID: "1"} and the delivery finishes
Then Publish returned nil
And <observed>

Examples:
  | handler            | observed                                                                         |
  | unset              | Logf was called with "DI: async listener `x` failed on event `doger.OrderPlaced`: out of stock" |
  | a recording func   | the handler received a *ListenerError for "x" matching ErrOutOfStock and Logf was not called |
```

<!-- cases: N/A 6 duplicate: not applicable -->
<!-- cases: N/A 9 panics: async panics are recovered like sync ones and go to the handler (covered by TestAsync_Panic row in design) -->

### Requirement: Default executor is bounded
The default event executor SHALL run at most `runtime.GOMAXPROCS(0) * 4` async deliveries at once. When it is full, `Publish` SHALL block until a slot frees or the publisher's context ends, and then SHALL return an error matching that context's error without delivering to that listener.

#### Scenario: Backpressure at the limit
```gherkin
Given a root scope with GOMAXPROCS 1, so the limit is 4
And a function listener for OrderPlaced with Async() that blocks until "release" is closed
And <inflight> deliveries are in flight
When the consumer publishes OrderPlaced{ID: "n"} with a context that times out after 50ms
Then Publish returns <error>

Examples:
  | inflight | error                    |
  | 0        | nil                      |
  | 3        | nil                      |
  | 4        | context.DeadlineExceeded |
```

<!-- cases: N/A 3 zero values: the limit has no zero input; a custom executor is the override -->
<!-- cases: N/A 6 duplicate: not applicable -->

### Requirement: Replaceable event executor
`InjectorOpts.EventExecutor` SHALL replace the default executor. Every async delivery SHALL be passed to its `Submit`, a `Submit` error SHALL be returned by `Publish` wrapped with the listener's ID, and its `Drain` SHALL be called during shutdown.

#### Scenario: Custom executor receives async deliveries
```gherkin
Given a root scope with an EventExecutor that runs tasks inline and counts Submit and Drain calls
And a function listener "x" for OrderPlaced with Async()
When the consumer publishes OrderPlaced{ID: "1"} twice and then shuts the root down
Then Submit was called 2 times and Drain 1 time
And "x" ran 2 times
```

#### Scenario: Submit error is returned
```gherkin
Given a root scope with an EventExecutor whose Submit returns ErrQueueFull
And a function listener "x" for OrderPlaced with Async()
When the consumer publishes OrderPlaced{ID: "1"}
Then Publish returns a *ListenerError for "x" matching ErrQueueFull
```

<!-- cases: N/A 4 boundaries: not applicable to an injected executor -->

### Requirement: Container lifecycle events
The root SHALL publish `ContainerStarting`, `ContainerStarted`, `ContainerReady`, `ContainerFailed{Err}`, `ContainerClosing` and `ContainerClosed{Report}`, each carrying `Root`, from the same goroutine right after the container hooks of the same point. `ContainerStarting` SHALL be published only after every Starting hook passed.

#### Scenario: Lifecycle events follow their hooks
```gherkin
Given a root scope with every container hook recording "hook:<point>"
And a function listener for each lifecycle event recording "event:<point>"
When the consumer calls <operation>
Then the record is <record>

Examples:
  | operation                                 | record                                                                                                  |
  | Start then Shutdown                       | [hook:Starting, event:Starting, hook:Started, event:Started, hook:Ready, event:Ready, hook:Closing, event:Closing, hook:Closed, event:Closed] |
  | Start with a Starter failing "boom"       | [hook:Starting, event:Starting, hook:Failed, event:Failed]                                               |
  | Start with a Starting hook vetoing "no"   | [hook:Starting, hook:Failed, event:Failed]                                                               |
```

#### Scenario: Lifecycle listeners never change Start or Shutdown results
```gherkin
Given a root scope with a function listener for <event> that <behavior>
When the consumer calls Start and then Shutdown
Then Start returns nil and the shutdown report has no error
And the async error handler received 1 *ListenerError

Examples:
  | event             | behavior                 |
  | ContainerStarted  | returns error "x"        |
  | ContainerReady    | panics with "x"          |
  | ContainerClosing  | returns error "x"        |
```

#### Scenario: Existing hooks behave as before
```gherkin
Given a root scope with a HookContainerStarting hook returning error "veto" and no listener
When the consumer calls Start
Then Start returns an error wrapping "veto" and no service was built
```

<!-- cases: N/A 3,4: lifecycle events carry no consumer input -->
<!-- cases: N/A 6 duplicate: one event per lifecycle point -->

### Requirement: Shutdown drains async deliveries
Root shutdown SHALL publish `ContainerClosing`, then stop accepting async submissions, then wait for in-flight async deliveries, and only then shut services down. If the shutdown context ends first, the in-flight deliveries' contexts SHALL be cancelled and the report SHALL contain an error matching that context's error.

#### Scenario: In-flight deliveries finish before services shut down
```gherkin
Given a lazy service "*DB" implementing Shutdowner that records "db-closed"
And a function listener for OrderPlaced with Async() that sleeps until "release" and records "delivered"
And one delivery is in flight
When the consumer calls ShutdownWithContext with <ctx> and closes "release" <release>
Then the record is <record>
And the report <report>
And no goroutine leaked

Examples:
  | ctx                     | release              | record                       | report                                     |
  | context.Background()    | after 10ms           | ["delivered", "db-closed"]   | has no error                               |
  | a context timing out at 20ms | never (the listener returns on ctx.Done) | ["db-closed"] | contains an error matching context.DeadlineExceeded |
```

<!-- cases: N/A 3 zero values: nil context is normalized as for every shutdown -->

### Requirement: Publish after shutdown
After the drain began, `Publish` SHALL return an error matching `ErrPublishAfterShutdown` when a matching listener is async, and SHALL still deliver to synchronous listeners. After the root is closed, every `Publish` SHALL return `ErrPublishAfterShutdown`; `ContainerClosed` SHALL reach function listeners only.

#### Scenario: Publishing during and after shutdown
```gherkin
Given a root scope with a synchronous function listener "s" and an async function listener "a" for OrderPlaced
And a lazy service "*DB" implementing Shutdowner
When the consumer publishes OrderPlaced{ID: "1"} <when>
Then Publish returns <error>
And "s" was called <s> times

Examples:
  | when                                   | error                    | s |
  | from a Shutdowner's Shutdown method    | ErrPublishAfterShutdown  | 1 |
  | after Shutdown returned                | ErrPublishAfterShutdown  | 0 |
```

#### Scenario: ContainerClosed reaches function listeners only
```gherkin
Given a root scope with a service listener "*Mailer" and a function listener "f" for ContainerClosed
When the consumer shuts the root down twice
Then "f" was called 1 time
And "*Mailer" OnEvent was never called for ContainerClosed
```

<!-- cases: N/A 4 boundaries: not applicable -->

### Requirement: Concurrent publish and registration
`Publish`, `Listen`, its cancel function and listener registrations SHALL be safe to call concurrently. A `Publish` SHALL see each listener either fully registered or not at all, and a lazy listener SHALL be built once under concurrent publishes.

#### Scenario: Concurrent publishers and registrations
```gherkin
Given a root scope with a lazy service "*Mailer" with AsListener[OrderPlaced]() whose provider counts its calls
When 100 goroutines publish OrderPlaced while 10 goroutines call Listen and cancel in a loop
Then the run passes under -race within 5s
And the provider of "*Mailer" was called 1 time
And Mailer received 100 events
```

<!-- cases: N/A 3,4,5,6,9: covered by the single-goroutine requirements -->

### Requirement: Clone copies listeners
`RootScope.Clone()` and `CloneWithOpts` SHALL copy the listener registry: service listeners rebound to the clone's scopes, and function listeners as the same function. Registering or cancelling a listener on one SHALL NOT affect the other. The clone SHALL use its own executor.

#### Scenario: Clone delivers to its own listeners
```gherkin
Given a root scope with a service listener "*Mailer" and a function listener "f" for OrderPlaced
When the consumer clones the root, registers function listener "g" on the clone, and publishes OrderPlaced{ID: "1"} on <target>
Then <outcome>

Examples:
  | target   | outcome                                                                   |
  | clone    | "f" and "g" ran, and the clone built its own *Mailer instance             |
  | original | "f" ran, "g" did not, and the original's *Mailer instance received the event |
```

<!-- cases: N/A 3,4,5,9: clone has no input; errors follow normal publish rules -->
