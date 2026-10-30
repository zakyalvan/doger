# Spec Delta

## Purpose

Lets consumers publish typed in-process events from any injector and react to them with lazily built listener services or functions, with predictable ordering, scope reach, listener lifetime, error handling, async delivery and lifecycle integration, without writing their own event bus.

## ADDED Requirements

### Requirement: Publish delivers by assignability
`Publish(ctx, i, event, opts...)` SHALL deliver the event to every reachable listener declared for a type `L` to which the event's dynamic type is assignable, and SHALL return nil when none matches. A listener declared for an interface type SHALL receive every event whose dynamic type implements it.

#### Scenario: Publish matches listeners by the event's dynamic type
```gherkin
Given a root scope
And a function listener for "<listened>" that records the events it receives
When the consumer publishes <published>
Then the listener received <received> events

Examples:
  | listened       | published                          | received |
  | OrderPlaced    | OrderPlaced{ID: "42"}              | 1        |
  | DomainEvent    | OrderPlaced{ID: "42"}              | 1        |
  | OrderPlaced    | DomainEvent(OrderPlaced{ID: "42"}) | 1        |
  | OrderPlaced    | OrderCancelled{ID: "42"}           | 0        |
  | *OrderPlaced   | OrderPlaced{ID: "42"}              | 0        |
  | OrderPlaced    | &OrderPlaced{ID: "42"}             | 0        |
  | PtrEvent       | OrderPlaced{ID: "42"}              | 0        |
  | PtrEvent       | &OrderPlaced{ID: "42"}             | 1        |
  | any            | OrderPlaced{ID: "42"}              | 1        |
```
`DomainEvent` is implemented by `OrderPlaced` with value receivers; `PtrEvent` is implemented by `*OrderPlaced` only.

#### Scenario: Publish with no listeners changes nothing
```gherkin
Given a root scope with lazy services "a" and "b" and no listener
When the consumer publishes OrderPlaced{ID: "42"}
Then Publish returns nil
And ListInvokedServices of the root is empty
```

<!-- cases: N/A 4 boundaries: no numeric input -->
<!-- cases: N/A 6 duplicate: several matching listeners are all delivered; see "Listener order is deterministic" -->
<!-- cases: N/A 7 lifecycle: covered by "Publishing after shutdown" -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent publish and registration" -->
<!-- cases: N/A 9 panics: covered by "Listener panics are recovered" -->
<!-- cases: N/A 10 compatibility: new API; the no-listener scenario pins that existing containers are unaffected -->

### Requirement: Publish argument handling
`Publish` SHALL return an error matching `ErrInvalidEvent` without calling any listener when the event is nil or a nil value of a pointer, map, slice, channel or function type, including such a value held in an interface. A nil injector SHALL mean `DefaultRootScope`, and a nil context SHALL reach listeners as `context.Background()`.

#### Scenario: Publish rejects or normalizes zero arguments
```gherkin
Given a function listener for "<listened>" registered on <scope>
When the consumer calls Publish with context <ctx>, injector <injector> and event <event>
Then Publish returns <error>
And the listener was called <calls> times with a non-nil context

Examples:
  | listened     | scope            | ctx                  | injector  | event                             | error           | calls |
  | any          | a new root       | context.Background() | the root  | nil                               | ErrInvalidEvent | 0     |
  | *OrderPlaced | a new root       | context.Background() | the root  | (*OrderPlaced)(nil)               | ErrInvalidEvent | 0     |
  | DomainEvent  | a new root       | context.Background() | the root  | DomainEvent((*OrderPlaced)(nil))  | ErrInvalidEvent | 0     |
  | Tags         | a new root       | context.Background() | the root  | Tags(nil)                         | ErrInvalidEvent | 0     |
  | Tags         | a new root       | context.Background() | the root  | Tags{}                            | nil             | 1     |
  | OrderPlaced  | DefaultRootScope | context.Background() | nil       | OrderPlaced{ID: "42"}             | nil             | 1     |
  | OrderPlaced  | a new root       | nil                  | the root  | OrderPlaced{ID: "42"}             | nil             | 1     |
  | OrderPlaced  | a new root       | context.Background() | the root  | OrderPlaced{}                     | nil             | 1     |
```
`Tags` is `map[string]string`.

<!-- cases: N/A 4 boundaries: no numeric input -->
<!-- cases: N/A 6 duplicate: not applicable to arguments -->
<!-- cases: N/A 8 concurrency: argument checks are pure -->
<!-- cases: N/A 9 panics: argument checks never call user code -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: AsListener declares a listener service
`AsListener[E]()` SHALL opt a registration into receiving events of type `E` through `Listener[E]`'s `OnEvent(context.Context, E) error`. A concrete registered type that does not implement it SHALL panic at registration, before the service is stored, with "DI: service `<name>` does not implement Listener[<E>]". Repeating it for one `E` SHALL register one listener.

#### Scenario: AsListener validates the registered type
```gherkin
Given a root scope
When the consumer registers a lazy service of type "<registered>" named "<name>" with <options>
Then registration <outcome>

Examples:
  | registered  | name     | options                                              | outcome                                                                                              |
  | *Mailer     | *Mailer  | AsListener[OrderPlaced]()                            | succeeds and 1 event is delivered per publish                                                        |
  | *Mailer     | *Mailer  | AsListener[OrderPlaced](), AsListener[OrderPlaced]() | succeeds and 1 event is delivered per publish                                                        |
  | *Auditor    | *Auditor | AsListener[OrderPlaced]()                            | panics with "DI: service `*Auditor` does not implement Listener[doger.OrderPlaced]" and stores nothing |
  | Notifier    | mailer   | AsListener[OrderPlaced]()                            | succeeds; the instance is checked at first dispatch                                                  |
```

#### Scenario: Interface-typed listener is checked at first dispatch
```gherkin
Given a lazy service "mailer" registered as interface Notifier with AsListener[OrderPlaced]()
And its provider returns a value that does not implement Listener[OrderPlaced]
When the consumer publishes OrderPlaced{ID: "42"}
Then Publish returns a *ListenerError whose Listener is "[root]/mailer#github.com/kartaladev/doger.OrderPlaced"
And the error matches ErrInvalidListener
```

<!-- cases: N/A 3 zero values: AsListener takes no value; a nil ProvideOption is ignored by the existing rule -->
<!-- cases: N/A 4 boundaries: no numeric input -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent publish and registration" -->
<!-- cases: N/A 9 panics: the registration panic is the scenario; listener panics are covered separately -->
<!-- cases: N/A 10 compatibility: new option -->

### Requirement: Listener services are built lazily
A lazy listener service SHALL NOT be built before the first event that reaches it and SHALL be reused afterwards. An eager or value registration SHALL be used as already built. A transient listener SHALL be built once per delivery. A provider error SHALL fail that delivery with a `*ListenerError` wrapping it.

#### Scenario: Listener build follows the service kind
```gherkin
Given a <kind> service "*Mailer" registered with AsListener[OrderPlaced]() whose provider counts its calls
When the consumer publishes OrderPlaced{ID: "1"} <times> times
Then the provider was called <builds> times in total, counting registration

Examples:
  | kind      | times | builds |
  | lazy      | 0     | 0      |
  | lazy      | 1     | 1      |
  | lazy      | 3     | 1      |
  | transient | 3     | 3      |
  | eager     | 0     | 1      |
  | eager     | 3     | 1      |
```

#### Scenario: Listener provider error fails the delivery
```gherkin
Given a lazy service "*Mailer" with AsListener[OrderPlaced]() whose provider returns error ErrBoom
When the consumer publishes OrderPlaced{ID: "1"}
Then Publish returns a *ListenerError whose Listener is "[root]/*Mailer#github.com/kartaladev/doger.OrderPlaced"
And the error matches ErrBoom
```

<!-- cases: N/A 3 zero values: a value registration of a nil listener is rejected by ProvideValue's existing rules -->
<!-- cases: N/A 4 boundaries: 0, 1 and many publishes are in the table -->
<!-- cases: N/A 6 duplicate: one binding per registration and event type -->
<!-- cases: N/A 7 lifecycle: covered by "Listener lifetime follows its registration" -->
<!-- cases: N/A 8 concurrency: single build under concurrency is covered by "Concurrent publish and registration" -->
<!-- cases: N/A 9 panics: a provider panic follows the existing provider-panic rule and reaches the listener as an error -->
<!-- cases: N/A 10 compatibility: building rules are the existing ones -->

### Requirement: ListenMethod declares a method listener
`ListenMethod(method, opts...)` SHALL opt a registration of type `S` into events of type `E` through `method(instance, ctx, event)`, where `method` is a `func(S, context.Context, E) error`. A registered type other than `S`, or a nil method, SHALL panic at registration with a message naming the service.

#### Scenario: One service listens to several event types
```gherkin
Given a lazy service "*Mailer" registered with ListenMethod((*Mailer).OnPlaced) and ListenMethod((*Mailer).OnCancelled)
When the consumer publishes OrderPlaced{ID: "1"}, then OrderCancelled{ID: "1"}, in that order
Then Mailer recorded ["placed:1", "cancelled:1"]
And the provider of "*Mailer" was called 1 time
```

#### Scenario: ListenMethod validates its input
```gherkin
Given a root scope
When the consumer registers a service of type "<registered>" with ListenMethod(<method>)
Then registration <outcome>

Examples:
  | registered | method             | outcome                                                                              |
  | *Mailer    | (*Mailer).OnPlaced | succeeds                                                                             |
  | *Auditor   | (*Mailer).OnPlaced | panics with "DI: service `*Auditor` is not of type *doger.Mailer required by ListenMethod" |
  | *Mailer    | nil                | panics with "DI: ListenMethod requires a non-nil method for service `*Mailer`"       |
```

<!-- cases: N/A 4 boundaries: one or many methods are covered by the first scenario -->
<!-- cases: N/A 6 duplicate: two methods for the same event type are two listeners with distinct IDs (see "Listener IDs") -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent publish and registration" -->
<!-- cases: N/A 10 compatibility: new option -->

### Requirement: Listen registers a function listener
`Listen[E](i, fn, opts...)` SHALL register `fn` in the scope of `i` and return a stop function. The first call of stop SHALL unregister it and return true; later calls SHALL return false. A nil `fn` SHALL panic with "DI: Listen requires a non-nil function". A nil injector SHALL mean `DefaultRootScope`.

#### Scenario: Stop unregisters a function listener
```gherkin
Given a function listener for OrderPlaced registered with Listen on a root scope and stopped <stops> times
When the consumer publishes OrderPlaced{ID: "1"}
Then the function was called <calls> times
And the stop calls returned <results>

Examples:
  | stops | calls | results       |
  | 0     | 1     | []            |
  | 1     | 0     | [true]        |
  | 2     | 0     | [true, false] |
```

#### Scenario: Listen argument handling
```gherkin
Given <setup>
When the consumer calls Listen[OrderPlaced] with injector <injector> and function <fn>
Then <outcome>

Examples:
  | setup                       | injector | fn                  | outcome                                                       |
  | a root scope                | the root | nil                 | it panics with "DI: Listen requires a non-nil function"       |
  | DefaultRootScope            | nil      | a recording func    | a publish on DefaultRootScope calls the function once         |
```

<!-- cases: N/A 4 boundaries: 0, 1 and 2 stops are in the table -->
<!-- cases: N/A 6 duplicate: the same function registered twice is two listeners and is called twice -->
<!-- cases: N/A 7 lifecycle: covered by "Listener lifetime follows its registration" -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent publish and registration" -->
<!-- cases: N/A 9 panics: the nil-function panic is the scenario -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Listener IDs
Each listener SHALL have an ID used in errors and logs: `ListenerID(id)` when given and non-empty; else `<scope path>/<service name>#<event type>` for `AsListener`, with `/m<n>` appended for the n-th `ListenMethod` of that registration; else `<scope path>/func#<n>` for function listeners. Scope path segments SHALL escape `%` and `/`. IDs need not be unique.

#### Scenario: Default and explicit IDs
```gherkin
Given a root scope with a child scope "<scope>"
When the consumer registers <listener> in "<scope>"
Then the listener's ID, as reported in a *ListenerError, is "<id>"

Examples:
  | scope       | listener                                                             | id                                                                       |
  | billing     | service "*Mailer" with AsListener[OrderPlaced]()                     | [root]/billing/*Mailer#github.com/kartaladev/doger.OrderPlaced           |
  | billing     | the 2nd of ListenMethod((*Mailer).OnPlaced), ListenMethod((*Mailer).OnPlacedAudit) on service "*Mailer" | [root]/billing/*Mailer#github.com/kartaladev/doger.OrderPlaced/m2 |
  | billing     | service "*Mailer" with AsListener[OrderPlaced](ListenerID("mailer")) | mailer                                                                   |
  | billing     | service "*Mailer" with AsListener[OrderPlaced](ListenerID(""))       | [root]/billing/*Mailer#github.com/kartaladev/doger.OrderPlaced           |
  | billing     | a function listener, the 3rd registered in this container            | [root]/billing/func#3                                                    |
  | ext:a/b     | service "x" with AsListener[OrderPlaced]()                           | [root]/ext:a%2Fb/x#github.com/kartaladev/doger.OrderPlaced               |
```

<!-- cases: N/A 4 boundaries: function sequence numbers start at 1 and only grow -->
<!-- cases: N/A 5 errors: IDs never fail -->
<!-- cases: N/A 6 duplicate: duplicate IDs are allowed in this change; uniqueness is a durable-delivery concern (add-durable-events) -->
<!-- cases: N/A 8 concurrency: the sequence counter is atomic; covered by "Concurrent publish and registration" -->
<!-- cases: N/A 9 panics: no user code runs -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Listener lifetime follows its registration
A service listener SHALL be reached only while its registration is the one stored under its name: an override, an `AsDefault()` registration replaced or yielding, or the shutdown of its scope SHALL stop it being reached. Shutting a scope down SHALL also stop its function listeners. A recreated scope SHALL accept new listeners.

#### Scenario: Registration changes end a listener
```gherkin
Given a root scope where <setup>
When the consumer publishes OrderPlaced{ID: "1"} through the root
Then the listeners that ran are <ran>

Examples:
  | setup                                                                                                        | ran           |
  | "*Mailer" has AsListener[OrderPlaced]() and is overridden by a "*Mailer" without it                         | []            |
  | "*Mailer" without AsListener is overridden by a "*Mailer" with it                                           | ["*Mailer"]   |
  | an AsDefault() "*Mailer" with AsListener[OrderPlaced]() is replaced by an app "*Mailer" without it          | []            |
  | an AsDefault() "*Mailer" with AsListener[OrderPlaced]() yields to an app "*Mailer" with it, registered first | ["*Mailer"] once, the app instance |
  | child "req" has function listener "f" and service listener "*Mailer", then "req" is shut down               | []            |
  | child "req" is shut down and recreated, and the new "req" has function listener "g"                         | ["g"]         |
```

#### Scenario: Publishing through a shut-down scope
```gherkin
Given a child scope "req" of a root scope that has been shut down
When the consumer publishes OrderPlaced{ID: "1"} through "req"
Then Publish returns an error matching ErrPublishAfterShutdown
```

<!-- cases: N/A 3,4: no input value -->
<!-- cases: N/A 8 concurrency: a shutdown racing a publish is covered by "Publish racing shutdown" -->
<!-- cases: N/A 9 panics: no user code runs -->
<!-- cases: N/A 10 compatibility: override and AsDefault keep their existing rules; listeners follow them -->

### Requirement: Listener order is deterministic
Synchronous listeners SHALL be called in ascending `ListenerOrder` (default 0), then by reach rank (the publishing scope, then each ancestor up to the root, then extension private scopes in installation order), then in registration order. Order SHALL be decided without building any listener; `Ordered` and `WithOrder` SHALL NOT affect it.

#### Scenario: Listeners run in order, rank, then registration order
```gherkin
Given a root scope with function listeners "r1" (order 0) and "r2" (order -1) for OrderPlaced, registered in that order
And an installed extension whose private scope has function listener "x1" (order 0)
And a child scope "req" with function listeners "c1" and "c2" (order 0), registered in that order
And a service "*Mailer" in "req" with AsListener[OrderPlaced]() and WithOrder(-10), implementing Ordered with -20
When the consumer publishes OrderPlaced{ID: "1"} through "req"
Then the listeners ran in the order ["r2", "c1", "c2", "*Mailer", "r1", "x1"]
```

<!-- cases: N/A 3 zero values: order 0 is the default in the scenario -->
<!-- cases: N/A 4 boundaries: negative, zero and equal orders are in the scenario -->
<!-- cases: N/A 5 errors: ordering has no error path -->
<!-- cases: N/A 6 duplicate: equal orders fall back to rank and registration order -->
<!-- cases: N/A 8 concurrency: order is fixed per plan -->
<!-- cases: N/A 9 panics: no user code runs to order -->
<!-- cases: N/A 10 compatibility: Ordered and WithOrder keep their collection-only meaning -->

### Requirement: Listener filter
`ListenerFilter(pred)` SHALL skip a listener when `pred(event)` returns false, without building it. A nil predicate SHALL mean no filter; the last `ListenerFilter` SHALL win. A predicate whose parameter type the listener's event type is not assignable to SHALL panic at registration. A predicate panic SHALL become a `*ListenerError`.

#### Scenario: Filter decides delivery
```gherkin
Given a lazy service "*Mailer" registered with AsListener[OrderPlaced](<filters>)
When the consumer publishes OrderPlaced{Amount: <amount>}
Then Mailer.OnEvent was called <calls> times, the provider <builds> times, and Publish returned <error>

Examples:
  | filters                                                         | amount | calls | builds | error                            |
  | ListenerFilter(func(e OrderPlaced) bool { e.Amount > 100 })     | 150    | 1     | 1      | nil                              |
  | ListenerFilter(func(e OrderPlaced) bool { e.Amount > 100 })     | 100    | 0     | 0      | nil                              |
  | ListenerFilter(nil)                                             | 5      | 1     | 1      | nil                              |
  | ListenerFilter(always false), ListenerFilter(always true)       | 5      | 1     | 1      | nil                              |
  | ListenerFilter(func(OrderPlaced) bool { panic("bad") })         | 5      | 0     | 0      | *ListenerError containing "bad"  |
```

#### Scenario: Filter type mismatch panics at registration
```gherkin
Given a root scope
When the consumer registers "*Mailer" with AsListener[OrderPlaced](ListenerFilter(func(e OrderCancelled) bool { return true }))
Then registration panics with "DI: ListenerFilter for doger.OrderCancelled cannot apply to listener of doger.OrderPlaced"
```

<!-- cases: N/A 4 boundaries: 100 vs 150 covers the predicate boundary -->
<!-- cases: N/A 8 concurrency: predicates run on the dispatching goroutine -->
<!-- cases: N/A 10 compatibility: new option -->

### Requirement: Event reach across scopes
An event published through injector `i` SHALL reach listeners in `i`'s scope, its ancestors and the private scopes of installed extensions, and SHALL NOT reach sibling or descendant scopes or scopes of extensions still installing. `PublishLocal()` SHALL restrict delivery to `i`'s own scope.

#### Scenario: Reach depends on where the listener lives
```gherkin
Given a root scope with child scopes "tenant-a" and "tenant-b", and "req" under "tenant-a"
And an extension installed with doger.Extend whose private scope is "ext:audit"
And a plain child scope of the root named "ext:fake"
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
  | ext:fake       | req           | none           | 0     |
  | [root]         | req           | PublishLocal() | 0     |
  | req            | req           | PublishLocal() | 1     |
```

#### Scenario: Extensions still installing are not reached
```gherkin
Given an extension whose Extend registers a function listener "x" for OrderPlaced in its private scope and then publishes OrderPlaced{ID: "1"} through the root
When the consumer installs it with doger.Extend
Then "x" was called 0 times during Extend
And a publish through the root after Extend returned calls "x" once
```

<!-- cases: N/A 3 zero values: nil injector is covered by "Publish argument handling" -->
<!-- cases: N/A 5 errors: reach has no error path -->
<!-- cases: N/A 8 concurrency: reach is computed per plan; plan invalidation is covered by "Concurrent publish and registration" -->
<!-- cases: N/A 9 panics: no user code runs -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Fail-fast is the default error mode
By default the first synchronous listener error SHALL stop dispatch, and `Publish` SHALL return a `*ListenerError` holding the listener ID, the event type and the cause, matchable with `errors.As` and unwrapping to the cause, with the message "DI: listener `<id>` failed on event `<type>`: <cause>".

#### Scenario: First error stops dispatch
```gherkin
Given function listeners "a", "b" and "c" for OrderPlaced with ListenerID "a", "b" and "c", registered in that order
And listener "b" returns ErrOutOfStock, whose message is "out of stock"
When the consumer publishes OrderPlaced{ID: "1"}
Then listeners "a" and "b" ran and "c" did not
And errors.As finds a *ListenerError with Listener "b" and EventType doger.OrderPlaced
And the error matches ErrOutOfStock
And its message is "DI: listener `b` failed on event `doger.OrderPlaced`: out of stock"
```

#### Scenario: A typed-nil error is an error
```gherkin
Given a function listener "a" for OrderPlaced that returns a nil *MyErr as error
When the consumer publishes OrderPlaced{ID: "1"}
Then Publish returns a *ListenerError for "a"
```

<!-- cases: N/A 4 boundaries: first, middle and last failing listeners are covered by "EventContinue runs every listener" rows -->
<!-- cases: N/A 6 duplicate: not applicable -->
<!-- cases: N/A 8 concurrency: dispatch is sequential on one goroutine -->
<!-- cases: N/A 9 panics: covered by "Listener panics are recovered" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: EventContinue runs every listener
With `InjectorOpts.EventErrorMode` set to `EventContinue`, every synchronous listener SHALL run, and `Publish` SHALL return the `errors.Join` of each `*ListenerError` in listener order, or nil when none failed.

#### Scenario: Errors are joined in continue mode
```gherkin
Given a root scope with EventErrorMode <mode>
And function listeners "a", "b", "c" for OrderPlaced with those IDs, where <failing> return ErrOutOfStock and <panicking> panic with "x"
When the consumer publishes OrderPlaced{ID: "1"}
Then the listeners that ran are <ran>
And Publish returns *ListenerErrors for <named>, in that order

Examples:
  | mode          | failing   | panicking | ran           | named     |
  | EventContinue | none      | none      | ["a","b","c"] | []        |
  | EventContinue | ["b"]     | none      | ["a","b","c"] | ["b"]     |
  | EventContinue | ["a","c"] | none      | ["a","b","c"] | ["a","c"] |
  | EventContinue | none      | ["b"]     | ["a","b","c"] | ["b"]     |
  | EventFailFast | ["a","c"] | none      | ["a"]         | ["a"]     |
```

<!-- cases: N/A 3 zero values: the zero EventErrorMode is EventFailFast (see "Event options are validated") -->
<!-- cases: N/A 6 duplicate: not applicable -->
<!-- cases: N/A 8 concurrency: dispatch is sequential on one goroutine -->
<!-- cases: N/A 10 compatibility: new option -->

### Requirement: Listener panics are recovered
A panic in a listener SHALL be recovered into a `*ListenerError` for that listener and handled under the current error mode. An error panic value SHALL be wrapped with `%w`; any other value SHALL be formatted with `%v`.

#### Scenario: Panic becomes a listener error
```gherkin
Given a function listener "p" for OrderPlaced that panics with <value>
When the consumer publishes OrderPlaced{ID: "1"}
Then Publish returns a *ListenerError for "p" where <match>

Examples:
  | value         | match                                  |
  | ErrOutOfStock | the error matches ErrOutOfStock        |
  | "kaboom"      | the error message contains "kaboom"    |
  | 42            | the error message contains "42"        |
  | nil           | the error message contains "nil"       |
```

<!-- cases: N/A 3,4: the panic value is the only input -->
<!-- cases: N/A 6 duplicate: not applicable -->
<!-- cases: N/A 8 concurrency: async panics are covered by "Errors with no caller go to HookEventError" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Nested publish runs inline
A listener SHALL be able to call `Publish` during dispatch. The nested dispatch SHALL complete before the next listener of the outer dispatch runs. No lock SHALL be held while listener code runs.

#### Scenario: Nested publish runs inline
```gherkin
Given function listener "a" for OrderPlaced that publishes InvoiceRequested{ID: "1"} and then records "a"
And function listener "b" for OrderPlaced that records "b"
And function listener "i" for InvoiceRequested that records "i"
When the consumer publishes OrderPlaced{ID: "1"}
Then the record is ["i", "a", "b"]
```

<!-- cases: N/A 3,4,6: re-entrancy only concerns ordering -->
<!-- cases: N/A 5 errors: the nested error is returned to listener "a", which decides its own result -->
<!-- cases: N/A 8 concurrency: covered by "Concurrent publish and registration" -->
<!-- cases: N/A 9 panics: a nested panic is recovered by the nested dispatch -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Publish from a provider cannot deadlock
When `Publish` is called with a provider's injector and a reached listener is the service being built, or its build needs that service, `Publish` SHALL return a `*ListenerError` matching `ErrCircularDependency` for that listener instead of blocking, and SHALL record no dependency edge.

#### Scenario: Publishing during a build
```gherkin
Given a lazy service "*Billing" whose provider publishes OrderPlaced{ID: "1"} with its own injector and returns that error
And <listener>
When the consumer invokes "*Billing"
Then the invocation returns <result> within 1s
And ListServiceDependencies of "*Billing" is unchanged

Examples:
  | listener                                                                  | result                                         |
  | "*Billing" itself has AsListener[OrderPlaced]()                           | an error matching ErrCircularDependency        |
  | lazy "*Mailer" with AsListener[OrderPlaced]() whose provider invokes "*Billing" | an error matching ErrCircularDependency  |
  | lazy "*Mailer" with AsListener[OrderPlaced]() and no dependencies         | nil, and "*Mailer" received the event          |
```

<!-- cases: N/A 3,4,6: no input value -->
<!-- cases: N/A 8 concurrency: a different goroutine building the same service waits as for any lazy build -->
<!-- cases: N/A 9 panics: no extra user code runs -->
<!-- cases: N/A 10 compatibility: the existing circular-dependency error is reused -->

### Requirement: Publish honors its context
`Publish` with a context that is already done SHALL call no listener and return an error matching the context's error. When the context ends during dispatch, no further synchronous listener SHALL be called and `Publish` SHALL return an error matching the context's error.

#### Scenario: Cancellation stops dispatch
```gherkin
Given function listeners "a" and "b" for OrderPlaced, where "a" <a-behavior>
When the consumer publishes OrderPlaced{ID: "1"} with a context that <ctx>
Then the listeners that ran are <ran> and Publish returns <error>

Examples:
  | a-behavior          | ctx                                   | ran   | error            |
  | returns nil         | was cancelled before Publish          | []    | context.Canceled |
  | cancels the context | is cancelled by listener "a"          | ["a"] | context.Canceled |
  | returns nil         | is never cancelled                    | ["a","b"] | nil          |
```

<!-- cases: N/A 4 boundaries: before, during and never are the three cases -->
<!-- cases: N/A 6 duplicate: not applicable -->
<!-- cases: N/A 9 panics: covered by "Listener panics are recovered" -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Async delivery
`ListenerAsync()` SHALL deliver the event to that listener through the event executor and `Publish` SHALL return after submitting it. The listener's context SHALL keep the publisher context's values, SHALL NOT be cancelled by the publisher, and SHALL be cancelled with cause `ErrPublishAfterShutdown` when a shutdown's drain deadline passes.

#### Scenario: Async listener runs after Publish returns
```gherkin
Given a function listener "x" for OrderPlaced with ListenerAsync() that waits for channel "release" and then records its context
And the consumer published OrderPlaced{ID: "1"} with a context holding "trace"="t1" and cancelled that context after Publish returned nil
When the consumer closes "release"
Then "x" ran within 1s with value "trace"="t1" and a context whose Err is nil
```

<!-- cases: N/A 3 zero values: ListenerAsync takes no value -->
<!-- cases: N/A 4 boundaries: limits are covered by "Default executor is bounded" -->
<!-- cases: N/A 5 errors: covered by "Errors with no caller go to HookEventError" -->
<!-- cases: N/A 6 duplicate: repeating ListenerAsync is the same as once -->
<!-- cases: N/A 7 lifecycle: covered by the shutdown requirements -->
<!-- cases: N/A 10 compatibility: new option -->

### Requirement: Errors with no caller go to HookEventError
Errors and recovered panics of async and lifecycle listeners SHALL be passed to `InjectorOpts.HookEventError(ctx, root, err)` as a `*ListenerError`. When it is unset they SHALL be logged with `Logf`, or with the standard `log` package when `Logf` is unset. A panic in the hook SHALL be recovered and logged the same way.

#### Scenario: Where an async error goes
```gherkin
Given a root scope with <config>
And a function listener "x" for OrderPlaced with ListenerAsync() that <behavior>
When the consumer publishes OrderPlaced{ID: "1"} and waits for the executor to finish
Then Publish returned nil and <observed>

Examples:
  | config                                       | behavior                 | observed                                                                                          |
  | no Logf and no HookEventError                | returns ErrOutOfStock    | the standard log output contains "DI: listener `x` failed on event `doger.OrderPlaced`: out of stock" |
  | a recording Logf and no HookEventError       | returns ErrOutOfStock    | Logf received that message and the standard log output is empty                                   |
  | a recording HookEventError                   | returns ErrOutOfStock    | the hook received a *ListenerError for "x" matching ErrOutOfStock, and nothing was logged          |
  | a recording HookEventError                   | panics with "kaboom"     | the hook received a *ListenerError for "x" containing "kaboom"                                     |
  | a HookEventError that panics with "hookfail" | returns ErrOutOfStock    | the standard log output contains "hookfail" and the process keeps running                          |
```

<!-- cases: N/A 3,4: no input values beyond the configuration -->
<!-- cases: N/A 6 duplicate: one hook per root -->
<!-- cases: N/A 8 concurrency: the hook may be called from executor goroutines concurrently; documented -->
<!-- cases: N/A 10 compatibility: Logf keeps its meaning; only event errors fall back to the standard log -->

### Requirement: Default executor is bounded
The default executor SHALL run at most `InjectorOpts.EventAsyncParallelism` deliveries at once, defaulting to `runtime.GOMAXPROCS(0) * 4` read when the root is created. When it is full, `Publish` SHALL wait until a slot frees or its context ends; a done context SHALL fail even when a slot is free. That failure SHALL be a `*ListenerError` matching the context's error.

#### Scenario: Backpressure at the limit
```gherkin
Given a root scope with EventAsyncParallelism <limit>
And a function listener "x" for OrderPlaced with ListenerAsync() that waits for "release"
And <inflight> deliveries of "x" are in flight
When the consumer publishes OrderPlaced{ID: "n"} with a context that <ctx>
Then Publish returns <error>

Examples:
  | limit | inflight | ctx                        | error                                     |
  | 4     | 0        | times out after 50ms       | nil                                       |
  | 4     | 3        | times out after 50ms       | nil                                       |
  | 4     | 4        | times out after 50ms       | *ListenerError matching context.DeadlineExceeded |
  | 1     | 1        | times out after 50ms       | *ListenerError matching context.DeadlineExceeded |
  | 4     | 0        | was cancelled before Publish | an error matching context.Canceled      |
```

#### Scenario: Default parallelism
```gherkin
Given GOMAXPROCS is <procs> when the root is created with EventAsyncParallelism 0
When the consumer fills the executor with deliveries that wait for "release"
Then <limit> deliveries run at once

Examples:
  | procs | limit |
  | 1     | 4     |
  | 2     | 8     |
```

<!-- cases: N/A 5 errors: the only error is the context's -->
<!-- cases: N/A 6 duplicate: not applicable -->
<!-- cases: N/A 9 panics: covered by "Errors with no caller go to HookEventError" -->
<!-- cases: N/A 10 compatibility: new option -->

### Requirement: Nested async publishing never deadlocks
A `Publish` made from a task the executor is running SHALL NOT wait for an executor slot: async deliveries it submits SHALL run inline on that task's goroutine when no slot is free.

#### Scenario: Async listener publishing to an async listener
```gherkin
Given a root scope with EventAsyncParallelism 1
And a function listener "a" for OrderPlaced with ListenerAsync() that publishes InvoiceRequested{ID: "1"}
And a function listener "i" for InvoiceRequested with ListenerAsync() that records "i"
When the consumer publishes OrderPlaced{ID: "1"}
Then "i" was recorded within 1s
```

<!-- cases: N/A 3,4,5,6: only the nesting matters -->
<!-- cases: N/A 7 lifecycle: draining such tasks is covered by "Shutdown drains async deliveries" -->
<!-- cases: N/A 9 panics: as for other async deliveries -->
<!-- cases: N/A 10 compatibility: new behavior -->

### Requirement: Replaceable event executor
`InjectorOpts.EventExecutor` SHALL replace the default executor: every async delivery SHALL go through its `Submit`, a `Submit` error or panic SHALL become a `*ListenerError` returned by `Publish`, and its `Shutdown` SHALL be called during root shutdown. `NewBoundedEventExecutor(n)` SHALL return the default implementation.

#### Scenario: Custom executor receives async deliveries
```gherkin
Given a root scope with an EventExecutor that runs tasks inline and counts Submit and Shutdown calls
And a function listener "x" for OrderPlaced with ListenerAsync()
When the consumer publishes OrderPlaced{ID: "1"} twice and then shuts the root down
Then Submit was called 2 times, Shutdown 1 time, and "x" ran 2 times
```

#### Scenario: Submit failures are returned
```gherkin
Given a root scope with an EventExecutor whose Submit <behavior>
And a function listener "x" for OrderPlaced with ListenerAsync()
When the consumer publishes OrderPlaced{ID: "1"}
Then Publish returns a *ListenerError for "x" where <match>

Examples:
  | behavior                | match                               |
  | returns ErrQueueFull    | the error matches ErrQueueFull      |
  | panics with "boom"      | the error message contains "boom"   |
```

#### Scenario: NewBoundedEventExecutor bounds concurrency
```gherkin
Given the consumer calls NewBoundedEventExecutor(<n>)
When 5 tasks that wait for "release" are submitted to the result
Then <outcome>

Examples:
  | n | outcome                                                              |
  | 1 | 1 task runs at once                                                  |
  | 3 | 3 tasks run at once                                                  |
  | 0 | NewBoundedEventExecutor panics with "DI: NewBoundedEventExecutor requires n > 0" |
```

<!-- cases: N/A 6 duplicate: one executor per root -->
<!-- cases: N/A 8 concurrency: the executor's own concurrency is its contract -->
<!-- cases: N/A 10 compatibility: new option -->

### Requirement: Container lifecycle events
The root SHALL publish `ContainerStarting`, `ContainerStarted`, `ContainerReady`, `ContainerFailed{Err}`, `ContainerClosing` and `ContainerClosed{Report}`, each with `Root`, on the hook's goroutine right after the container hooks of the same point. `ContainerStarting` SHALL be published only after every Starting hook passed.

#### Scenario: Lifecycle events follow their hooks
```gherkin
Given a root scope with every container hook recording "hook:<point>" and a function listener for each lifecycle event recording "event:<point>"
When the consumer calls <operation>
Then the record is <record>

Examples:
  | operation                               | record                                                                                                                              |
  | Start, then Shutdown                    | [hook:Starting, event:Starting, hook:Started, event:Started, hook:Ready, event:Ready, hook:Closing, event:Closing, hook:Closed, event:Closed] |
  | Start with a Starter failing ErrBoom    | [hook:Starting, event:Starting, hook:Failed, event:Failed]                                                                         |
  | Start with a Starting hook vetoing ErrNo | [hook:Starting, hook:Failed, event:Failed]                                                                                         |
```

#### Scenario: Lifecycle events carry their data
```gherkin
Given a root scope R with a Starter failing ErrBoom and function listeners recording ContainerFailed and ContainerClosed
When the consumer calls Start and then Shutdown
Then the ContainerFailed event has Root R and an Err matching ErrBoom
And the ContainerClosed event has Root R and the *ShutdownReport that Shutdown returned
```

<!-- cases: N/A 3,4: lifecycle events carry no consumer input -->
<!-- cases: N/A 5 errors: listener errors are covered by "Lifecycle listeners only observe" -->
<!-- cases: N/A 6 duplicate: Closing and Closed fire once per root, as their hooks do -->
<!-- cases: N/A 8 concurrency: published on the lifecycle goroutine -->
<!-- cases: N/A 9 panics: covered by "Lifecycle listeners only observe" -->

### Requirement: Lifecycle listeners only observe
Every lifecycle listener SHALL run regardless of `EventErrorMode`. Their errors, panics and listener build failures SHALL go to `HookEventError` and SHALL NOT change the result of `Start` or `Shutdown`. Containers without lifecycle listeners SHALL behave exactly as before.

#### Scenario: Lifecycle listener failures are reported, not returned
```gherkin
Given a root scope with EventErrorMode EventFailFast and a recording HookEventError
And two listeners for <event>, where the first <behavior>
When the consumer calls Start and then Shutdown
Then Start returns nil and the shutdown report has no error
And both listeners for <event> were reached, the second one ran, and the hook received 1 *ListenerError

Examples:
  | event             | behavior                                         |
  | ContainerStarting | is a lazy service whose provider returns ErrBoom |
  | ContainerStarted  | returns ErrBoom                                  |
  | ContainerReady    | panics with "x"                                  |
  | ContainerClosing  | returns ErrBoom                                  |
  | ContainerClosed   | returns ErrBoom                                  |
```

#### Scenario: Existing hooks behave as before
```gherkin
Given a root scope with a HookContainerStarting hook returning ErrNo and no listener
When the consumer calls Start
Then Start returns an error wrapping ErrNo and no service was built
```

<!-- cases: N/A 3,4,6: no input values -->
<!-- cases: N/A 8 concurrency: published on the lifecycle goroutine -->

### Requirement: Shutdown drains async deliveries
Root shutdown SHALL, after publishing `ContainerClosing`, stop accepting async submissions, wake publishers waiting for a slot with `ErrPublishAfterShutdown`, wait for in-flight deliveries, and only then shut services down. If the shutdown context ends first, those deliveries' contexts SHALL be cancelled and the report SHALL hold an error matching the context's error.

#### Scenario: In-flight deliveries finish before services shut down
```gherkin
Given a lazy service "*DB" implementing Shutdowner that records "db-closed"
And a function listener for OrderPlaced with ListenerAsync() that waits for "release" or its context's end and records "delivered" or "cancelled"
And one delivery is in flight
When the consumer calls ShutdownWithContext with <ctx> while "release" is <release>
Then the record is <record>
And the report <report>

Examples:
  | ctx                          | release                  | record                       | report                                          |
  | context.Background()         | closed after 10ms        | ["delivered", "db-closed"]   | has no error                                    |
  | a context timing out at 20ms | never closed             | ["cancelled", "db-closed"]   | holds an error matching context.DeadlineExceeded |
```

#### Scenario: Waiting publishers are released at shutdown
```gherkin
Given a root scope with EventAsyncParallelism 1, one in-flight async delivery waiting for its context's end, and a second Publish waiting for a slot with context.Background()
When the consumer shuts the root down with a context timing out at 50ms
Then the waiting Publish returns an error matching ErrPublishAfterShutdown within 1s
```

<!-- cases: N/A 3 zero values: a nil shutdown context is normalized as for every shutdown -->
<!-- cases: N/A 4 boundaries: before and after the deadline are the two rows -->
<!-- cases: N/A 6 duplicate: a second Shutdown drains nothing -->
<!-- cases: N/A 9 panics: delivery panics are recovered as usual -->
<!-- cases: N/A 10 compatibility: containers without async listeners skip the drain -->

### Requirement: Scope shutdown drains its listeners
Shutting a child scope down SHALL, before its services shut down, wait for in-flight async deliveries to listeners of that scope and its descendants, bounded by the shutdown context, and SHALL then stop those listeners from being reached.

#### Scenario: Child scope drain
```gherkin
Given a child scope "req" with an async function listener for OrderPlaced that waits for "release" and records "delivered"
And a lazy service "*Conn" in "req" implementing Shutdowner that records "conn-closed"
And one delivery is in flight
When the consumer shuts "req" down while "release" is closed after 10ms
Then the record is ["delivered", "conn-closed"]
```

<!-- cases: N/A 3,4,5,6: as for root drain -->
<!-- cases: N/A 8 concurrency: deliveries to other scopes are not waited for -->
<!-- cases: N/A 9 panics: as for other async deliveries -->
<!-- cases: N/A 10 compatibility: scopes without async listeners shut down as before -->

### Requirement: Publishing after shutdown
After root shutdown stopped accepting async submissions, `Publish` SHALL return an error matching `ErrPublishAfterShutdown` for each reached async listener and still deliver to synchronous ones, skipping service listeners not built or already removed. After the root is closed every `Publish` SHALL fail that way, except that `ContainerClosed` SHALL reach function listeners.

#### Scenario: Publishing during and after shutdown
```gherkin
Given a root scope with a synchronous function listener "s", an async function listener "a", a built service listener "*Built" and a never-built lazy service listener "*Lazy", all for OrderPlaced
And a lazy service "*DB" implementing Shutdowner, built, that publishes OrderPlaced{ID: "1"} from its Shutdown and records the result
When the consumer shuts the root down and then publishes OrderPlaced{ID: "2"}
Then the result recorded by "*DB" matches ErrPublishAfterShutdown, "s" ran once and "*Lazy" was never built
And the second Publish returns an error matching ErrPublishAfterShutdown
```

#### Scenario: ContainerClosed reaches function listeners only
```gherkin
Given a root scope with a service listener "*Mailer" and a function listener "f" for ContainerClosed
When the consumer shuts the root down twice
Then "f" was called 1 time
And "*Mailer" never received ContainerClosed
```

<!-- cases: N/A 3,4: no input values -->
<!-- cases: N/A 8 concurrency: covered by "Publish racing shutdown" -->
<!-- cases: N/A 9 panics: as for other listeners -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Publish racing shutdown
`Publish` calls concurrent with root `Shutdown` SHALL each return nil or an error matching `ErrPublishAfterShutdown`, and every async delivery accepted before the drain began SHALL have finished or been cancelled when `Shutdown` returns.

#### Scenario: Concurrent publishers during shutdown
```gherkin
Given a root scope with an async function listener for OrderPlaced that counts deliveries
When 100 goroutines publish OrderPlaced in a loop while the root is shut down
Then every Publish returned nil or an error matching ErrPublishAfterShutdown
And the run passes under -race within 5s with no leaked goroutine
```

<!-- cases: N/A 3,4,5,6,9: covered by the single-goroutine requirements -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Concurrent publish and registration
`Publish`, `Listen`, its stop function, listener registrations and scope shutdowns SHALL be safe to call concurrently. A `Publish` SHALL see each listener either fully registered or not at all, and a lazy listener SHALL be built once under concurrent publishes.

#### Scenario: Concurrent publishers and registrations
```gherkin
Given a root scope with a lazy service "*Mailer" with AsListener[OrderPlaced]() whose provider counts its calls
When 100 goroutines publish OrderPlaced while 10 goroutines call Listen and stop in a loop and 5 goroutines create and shut down child scopes with listeners
Then the run passes under -race within 5s
And the provider of "*Mailer" was called 1 time and Mailer received 100 events
```

<!-- cases: N/A 3,4,5,6,9: covered by the single-goroutine requirements -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Clone copies listeners
`RootScope.Clone()` and `CloneWithOpts` SHALL copy service listener bindings with their registrations and function listeners as the same function. Stopping or adding a listener on one SHALL NOT affect the other. A clone SHALL get its own default executor; an executor set in `InjectorOpts` SHALL be shared, since the caller owns it.

#### Scenario: Clone delivers to its own listeners
```gherkin
Given a root scope with a service listener "*Mailer" and a function listener "f" for OrderPlaced
And the consumer cloned it, registered function listener "g" on the clone and stopped "f" on the original
When the consumer publishes OrderPlaced{ID: "1"} on <target>
Then <outcome>

Examples:
  | target   | outcome                                                                        |
  | clone    | "f" and "g" ran, and the clone built its own *Mailer instance                  |
  | original | "g" and "f" did not run, and the original's *Mailer instance received the event |
```

#### Scenario: Executor ownership in clones
```gherkin
Given a root scope with <executor> and an async listener
When the consumer clones it and shuts the original down
Then an async publish on the clone <outcome>

Examples:
  | executor                                   | outcome                                                        |
  | the default executor                       | is delivered                                                   |
  | InjectorOpts.EventExecutor set to E        | goes through E, which received one Shutdown call from the original |
```

<!-- cases: N/A 3,4,5,9: clone takes no input -->
<!-- cases: N/A 8 concurrency: clones share no mutable event state -->
<!-- cases: N/A 10 compatibility: clone keeps copying everything it copied before -->

### Requirement: Event options are validated
`New` and `NewWithOpts` SHALL panic with "DI: invalid EventErrorMode <n>" for an `EventErrorMode` other than `EventFailFast` and `EventContinue`. `EventAsyncParallelism` 0 SHALL mean the default. The zero `InjectorOpts` SHALL give every documented default.

#### Scenario: Option validation
```gherkin
Given InjectorOpts with <field> set to <value>
When the consumer calls NewWithOpts
Then <outcome>

Examples:
  | field                 | value         | outcome                                         |
  | EventErrorMode        | EventFailFast | the root fails fast                             |
  | EventErrorMode        | EventContinue | the root runs every listener                    |
  | EventErrorMode        | 99            | it panics with "DI: invalid EventErrorMode 99"  |
  | EventAsyncParallelism | 0             | the root uses GOMAXPROCS(0) * 4                 |
  | EventAsyncParallelism | 1             | the root runs 1 async delivery at once          |
  | (none)                | zero value    | fail fast, default executor, log fallback       |
```

<!-- cases: N/A 4 boundaries: EventAsyncParallelism is unsigned; 0 and 1 are the boundaries -->
<!-- cases: N/A 6,7,8,9: options are read once at creation -->
<!-- cases: N/A 10 compatibility: existing InjectorOpts fields are unchanged -->
