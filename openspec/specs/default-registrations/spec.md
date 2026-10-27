# default-registrations Specification

## Purpose

Lets a package or extension register a replaceable fallback service with `AsDefault()`, which an explicit registration of the application wins over regardless of registration order, without double lifecycle effects.

## Requirements

### Requirement: A default without a replacement is built and used
A registration given `AsDefault()` SHALL be resolved like a registration without it when no non-default service of the same name is visible from the default's scope: its provider is called on first resolution and the instance is returned, cached for lazy and eager services, as usual.

#### Scenario: Default resolves when nothing replaces it
```gherkin
Given a root scope
And <form> "*app.Dialer" registered in the root with doger.AsDefault(), producing "default-dialer"
When the consumer invokes "*app.Dialer" from the root <times> times
Then every result is "default-dialer"
And the provider was called <calls> times

Examples:
  | form                  | times | calls                |
  | a lazy service        | 1     | 1                    |
  | a lazy service        | 3     | 1                    |
  | an eager value        | 3     | - (no provider)      |
  | a transient service   | 3     | 3                    |
```

#### Scenario: A nil injector registers the default on DefaultRootScope
```gherkin
Given DefaultRootScope is reset to an empty root scope
And a lazy "*app.Dialer" registered with a nil injector and doger.AsDefault(), producing "default-dialer"
When the consumer invokes "*app.Dialer" from DefaultRootScope
Then the result is "default-dialer"
```

<!-- cases: 2 overrides: the explicit registration is the override; covered by "A default yields to a visible non-default service of the same name" and "Same-scope registrations of a name with a default" -->
<!-- cases: 3 zero values: nil injector scenario; a typed-nil eager value given AsDefault() is a value like any other and resolves as is -->
<!-- cases: 4 boundaries: the times/calls rows (1 and many) -->
<!-- cases: N/A 5 invalid input: AsDefault takes no argument and is valid on every registration form -->
<!-- cases: 6 duplicate: covered by "Same-scope registrations of a name with a default" -->
<!-- cases: 7 lifecycle: covered by the lifecycle and eager-shutdown requirements -->
<!-- cases: 8 concurrency: covered by "Concurrent first resolution and replacement" -->
<!-- cases: N/A 9 panics: a provider panic follows provider-panic-handling unchanged -->
<!-- cases: 10 compatibility: covered by "Registrations without AsDefault are unchanged" -->

### Requirement: A default yields to a visible non-default service of the same name
On its first resolution, a default SHALL check whether a non-default service with the same name is registered in an ancestor of the default's scope. If one is, the default SHALL resolve to that service's instance and its own provider SHALL NOT be called. Defaults, and services in descendant or sibling scopes, SHALL NOT count.

#### Scenario: Placement decides whether a default yields
```gherkin
Given a root scope with child scope "ext", grandchild scope "ext/sub" and child scope "other"
And a lazy "*app.Dialer" registered in scope "ext" with doger.AsDefault(), producing "default-dialer"
And an explicit lazy "*app.Dialer" registered in scope "<explicit_in>", producing "app-dialer"
When the consumer invokes "*app.Dialer" from scope "ext"
Then the result is "<result>"
And the default provider was called <calls> times

Examples:
  | explicit_in | result         | calls |
  | root        | app-dialer     | 0     |
  | other       | default-dialer | 1     |
  | ext/sub     | default-dialer | 1     |
  | (none)      | default-dialer | 1     |
```

#### Scenario: A grandchild default yields to the root
```gherkin
Given a root scope with child scope "a" and grandchild scope "a/b"
And a lazy "*app.Dialer" registered in scope "a/b" with doger.AsDefault()
And an explicit lazy "*app.Dialer" registered in the root, producing "app-dialer"
When the consumer invokes "*app.Dialer" from scope "a/b"
Then the result is "app-dialer"
```

#### Scenario: Registration order does not matter
```gherkin
Given a root scope with child scope "ext"
And a default lazy "*app.Dialer" in scope "ext" and an explicit lazy "*app.Dialer" in the root producing "app-dialer", registered in order <order>
When the consumer invokes "*app.Dialer" from scope "ext"
Then the result is "app-dialer"

Examples:
  | order                     |
  | default first, explicit second |
  | explicit first, default second |
```

#### Scenario: Repeating AsDefault is the same as giving it once
```gherkin
Given a root scope with child scope "ext"
And a lazy "*app.Dialer" registered in scope "ext" with doger.AsDefault(), doger.AsDefault()
And an explicit lazy "*app.Dialer" registered in the root, producing "app-dialer"
When the consumer invokes "*app.Dialer" from scope "ext"
Then the result is "app-dialer"
```

#### Scenario: A default does not yield to another default
```gherkin
Given a root scope with child scope "ext"
And a lazy "*app.Dialer" registered in the root with doger.AsDefault(), producing "root-default"
And a lazy "*app.Dialer" registered in scope "ext" with doger.AsDefault(), producing "ext-default"
When the consumer invokes "*app.Dialer" from scope "ext"
Then the result is "ext-default"
```

#### Scenario: An alias of a default resolves through the default's decision
```gherkin
Given a root scope with child scope "ext"
And a lazy "*app.Dialer" registered in scope "ext" with doger.AsDefault()
And doger.As[*app.Dialer, app.DialerIface] registered in scope "ext"
And an explicit lazy "*app.Dialer" registered in the root, producing "app-dialer"
When the consumer invokes "app.DialerIface" from scope "ext"
Then the result is "app-dialer"
```

<!-- cases: 2 overrides: the explicit registration is the override; Override* is covered in "Override ignores AsDefault" -->
<!-- cases: N/A 3 zero values: the empty name is an ordinary name; nil injector is covered in the first requirement -->
<!-- cases: 4 boundaries: depth 1 (parent) and 2 (grandparent), sibling and descendant rows -->
<!-- cases: N/A 5 invalid input: no input beyond the registrations -->
<!-- cases: 6 duplicate: same-scope cases are covered in "Same-scope registrations of a name with a default"; default-vs-default is the fifth scenario -->
<!-- cases: 7 lifecycle: the frozen decision is covered in "The decision is made once, at first resolution" -->
<!-- cases: 8 concurrency: covered in "Concurrent first resolution and replacement" -->
<!-- cases: N/A 9 panics: yielding calls no provider of the default; the target's provider panic follows provider-panic-handling -->
<!-- cases: N/A 10 compatibility: new behavior, only for registrations given AsDefault() -->

### Requirement: Same-scope registrations of a name with a default
In one scope, an explicit registration SHALL replace an unresolved default of the same name. A default registered where an explicit service of that name exists SHALL be skipped. An explicit registration over a resolved default, or a second default, SHALL panic with "DI: service `<name>` has already been declared".

#### Scenario: Explicit and default registrations of one name in one scope
```gherkin
Given a root scope
And <first> "*app.Dialer" registered in the root
And <step>
When the consumer registers <second> "*app.Dialer" in the root
Then the registration <outcome>
And invoking "*app.Dialer" returns "<result>"

Examples:
  | first                       | step                              | second              | outcome                                                          | result         |
  | a lazy default-dialer default | nothing else happened           | explicit app-dialer | succeeds                                                         | app-dialer     |
  | explicit app-dialer         | nothing else happened             | a default           | succeeds without effect                                          | app-dialer     |
  | a lazy default-dialer default | "*app.Dialer" was invoked       | explicit app-dialer | panics with "DI: service `*app.Dialer` has already been declared" | default-dialer |
  | a lazy default-dialer default | nothing else happened           | a second default    | panics with "DI: service `*app.Dialer` has already been declared" | default-dialer |
  | an eager default value      | nothing else happened             | explicit app-dialer | succeeds                                                         | app-dialer     |
  | a transient default         | nothing else happened             | explicit app-dialer | succeeds                                                         | app-dialer     |
```

#### Scenario: Registration hooks around defaults
```gherkin
Given a root scope with before- and after-registration hooks that record the service name
And <first> "*app.Dialer" registered in the root
When the consumer registers <second> "*app.Dialer" in the root
Then the before-registration hook saw "*app.Dialer" <before> times in total
And the after-registration hook saw "*app.Dialer" <after> times in total

Examples:
  | first                  | second              | before | after |
  | a lazy default         | an explicit lazy    | 2      | 2     |
  | an explicit lazy       | a lazy default      | 2      | 1     |
```

#### Scenario: A replaced default keeps its registration position
```gherkin
Given a root scope
And a lazy "first" registered, then a lazy "*app.Dialer" registered with doger.AsDefault(), then a lazy "last" registered
When the consumer registers an explicit "*app.Dialer" in the root
Then the registration order of the root is "first", "*app.Dialer", "last"
```

<!-- cases: 2 overrides: Override* is covered in "Override ignores AsDefault" -->
<!-- cases: N/A 3 zero values: the empty name behaves as any name -->
<!-- cases: 4 boundaries: zero, one and two prior registrations of the name are covered by the rows -->
<!-- cases: 5 errors: both panic rows; the message is the existing duplicate message -->
<!-- cases: 6 duplicate: the subject -->
<!-- cases: 7 lifecycle: "resolved" is pinned by the invoked row; a yielded default counts as resolved -->
<!-- cases: 8 concurrency: covered in "Concurrent first resolution and replacement" -->
<!-- cases: 9 panics: rows 3 and 4 -->
<!-- cases: 10 compatibility: an explicit duplicate without defaults still panics, see "Registrations without AsDefault are unchanged" -->

### Requirement: The decision is made once, at first resolution
Once a default was first resolved, whether its own provider is used or it yielded SHALL NOT change for the life of its registration. A non-default service registered later in an ancestor SHALL NOT replace a default that already chose its own provider, and later resolutions SHALL return what the first resolution decided.

#### Scenario: A late ancestor registration does not change a resolved default
```gherkin
Given a root scope with child scope "ext"
And a lazy "*app.Dialer" registered in scope "ext" with doger.AsDefault(), producing "default-dialer"
And "*app.Dialer" was invoked from scope "ext"
And an explicit lazy "*app.Dialer" producing "app-dialer" was registered in the root afterwards
When the consumer invokes "*app.Dialer" from scope "<from>"
Then the result is "<result>"

Examples:
  | from | result         |
  | ext  | default-dialer |
  | root | app-dialer     |
```

<!-- cases: N/A 2 overrides: Override* on the default's own scope replaces it as for any service -->
<!-- cases: N/A 3 zero values, 4 boundaries, 5 invalid input: no input -->
<!-- cases: 6 duplicate: same-scope cases are covered in "Same-scope registrations" -->
<!-- cases: 7 lifecycle: the subject; by-type and collection reads follow the same decision (type-resolution, collection-invocation) -->
<!-- cases: 8 concurrency: covered in "Concurrent first resolution and replacement" -->
<!-- cases: N/A 9 panics: nothing panics -->
<!-- cases: N/A 10 compatibility: new behavior -->

### Requirement: A default that yields has no lifecycle of its own
A default that yielded SHALL behave like an alias of the service it yielded to: it SHALL NOT be started, shut down or handled by a component kind on its own. A health check from the default's scope SHALL decide an unresolved default first and, if it yields, report the yielded-to service's result under the name, calling its health check once.

#### Scenario: The yielded-to service is started, shut down and checked once
```gherkin
Given a root scope with child scope "ext"
And an explicit lazy "*app.Pool" in the root implementing Start(ctx) error, Run(ctx) error, Shutdown() error and HealthCheck() error, whose HealthCheck returns errors.New("pool down"), each method counting calls
And a lazy "*app.Pool" registered in scope "ext" with doger.AsDefault() and doger.AsRunnable()
And "*app.Pool" was invoked from scope "ext"
And <before>
When the consumer calls <operation>
Then the root pool's <method> was called <count> times

Examples:
  | before                    | operation          | method      | count |
  | nothing else happened     | root.Start()       | Start       | 1     |
  | nothing else happened     | root.Start()       | Run         | 0     |
  | root.Start() was called   | root.Shutdown()    | Shutdown    | 1     |
  | nothing else happened     | root.HealthCheck() | HealthCheck | 1     |
  | nothing else happened     | ext.HealthCheck()  | HealthCheck | 1     |
```

#### Scenario: A health check from the default's scope reports the target's result
```gherkin
Given the setup of "The yielded-to service is started, shut down and checked once", where the default in "ext" <invoked>
When the consumer calls HealthCheck on scope "ext"
Then the result has key "*app.Pool" with an error containing "pool down"

Examples:
  | invoked                                  |
  | was invoked from scope "ext"             |
  | was never invoked                        |
```

<!-- cases: N/A 2 overrides: lifecycle belongs to the owner; to start the default instead, do not register the explicit service -->
<!-- cases: N/A 3 zero values, 4 boundaries, 5 invalid input: no input -->
<!-- cases: 6 duplicate: the count rows pin exactly one call -->
<!-- cases: 7 lifecycle: Start, Shutdown and HealthCheck from both scopes -->
<!-- cases: 8 concurrency: Start and Shutdown run their existing parallel paths; the test runs under -race -->
<!-- cases: N/A 9 panics: no extra code runs for a yielded default -->
<!-- cases: N/A 10 compatibility: new behavior -->

### Requirement: Lifecycle of a default that uses its own provider
A default that chose its own provider SHALL take part in start, kind handling, health check and shutdown exactly like a registration without `AsDefault()`.

#### Scenario: A default Starter without a replacement is started and shut down
```gherkin
Given a root scope
And a lazy "*app.Pool" registered with doger.AsDefault() that implements Start(ctx) error and Shutdown() error
And Start was called on the root
When the consumer calls Shutdown on the root
Then the pool's Start was called 1 time
And the pool's Shutdown was called 1 time
```

<!-- cases: N/A 2-6: identical to an ordinary registration, specified in container-start and component-kinds -->
<!-- cases: 7 lifecycle: the subject -->
<!-- cases: N/A 8 concurrency, 9 panics: identical to an ordinary registration -->
<!-- cases: N/A 10 compatibility: new option -->

### Requirement: Shutdown of eager default values
The container SHALL shut down an eager default value at scope shutdown only when the default uses its own value, deciding at shutdown if it was never resolved. A superseded or yielded eager default value SHALL NOT be shut down by the container; the code that created it owns it.

#### Scenario: Who shuts an eager default value down
```gherkin
Given a root scope with child scope "ext"
And an eager value "*app.Pool" implementing Shutdown() that counts calls, registered in scope "<in>" with doger.AsDefault()
And <other>
When the consumer calls Shutdown on the root
Then the eager value's Shutdown was called <calls> times

Examples:
  | in   | other                                                         | calls |
  | ext  | nothing else, never invoked                                   | 1     |
  | ext  | an explicit "*app.Pool" in the root, never invoked            | 0     |
  | ext  | an explicit "*app.Pool" in the root, invoked from "ext"       | 0     |
  | root | an explicit "*app.Pool" registered in the root afterwards     | 0     |
```

<!-- cases: N/A 2 overrides: ownership of a superseded value is not configurable; shut it down where it was created -->
<!-- cases: N/A 3 zero values: a typed-nil eager value has nothing to shut down -->
<!-- cases: 4 boundaries: never resolved, resolved, superseded -->
<!-- cases: N/A 5 invalid input: no input -->
<!-- cases: 6 duplicate: superseded row -->
<!-- cases: 7 lifecycle: the subject -->
<!-- cases: N/A 8 concurrency: shutdown decision runs under the default's lock -->
<!-- cases: N/A 9 panics: a panicking Shutdown follows the shutdown report unchanged -->
<!-- cases: 10 compatibility: eager values without AsDefault are shut down as before -->

### Requirement: Override ignores AsDefault
`Override*` SHALL register an explicit service even when given `AsDefault()`, replacing whatever is registered under the name, default or not.

#### Scenario: Override with AsDefault registers an explicit service
```gherkin
Given a root scope with child scope "ext"
And a lazy "*app.Dialer" registered in scope "ext" with doger.AsDefault(), producing "default-dialer"
And doger.Override registered "*app.Dialer" in the root with doger.AsDefault(), producing "override-dialer"
When the consumer invokes "*app.Dialer" from scope "ext"
Then the result is "override-dialer"
```

<!-- cases: 2 overrides: Override is the subject -->
<!-- cases: N/A 3 zero values, 4 boundaries, 5 invalid input: no input -->
<!-- cases: 6 duplicate: Override replacing a same-scope default is covered by task tests (design D5); it keeps the documented Override caveat -->
<!-- cases: N/A 7 lifecycle: Override keeps its documented caveat (a built instance is not shut down) -->
<!-- cases: N/A 8 concurrency, 9 panics: Override behavior is unchanged -->
<!-- cases: 10 compatibility: Override without AsDefault is unchanged -->

### Requirement: Concurrent first resolution and replacement
Concurrent first resolutions of a default SHALL agree on one decision and call its provider at most once. An explicit registration racing a first resolution SHALL either replace the default before it is resolved, or panic as a duplicate; an instance a default's provider built SHALL never be dropped from the container.

#### Scenario: Concurrent first resolutions agree
```gherkin
Given a root scope with child scope "ext"
And a lazy "*app.Dialer" registered in scope "ext" with doger.AsDefault(), whose provider counts its calls
When 100 goroutines invoke "*app.Dialer" from scope "ext" concurrently
Then every goroutine receives the same instance
And the provider was called exactly 1 time
```

#### Scenario: Replacement racing first resolution has one outcome
```gherkin
Given a root scope
And a lazy "*app.Dialer" registered with doger.AsDefault() that implements Shutdown() and counts calls, whose provider blocks until released
And a goroutine that will invoke "*app.Dialer", and another that will register an explicit "*app.Dialer", ordered by a channel handshake as <order>
When both goroutines finish
Then the registration <registration>
And every invoke returns <instance>
And after root Shutdown the default's Shutdown was called <shutdowns> times

Examples:
  | order                                             | registration                                                        | instance               | shutdowns |
  | registration completes before the invoke starts   | succeeded                                                           | the explicit instance  | 0         |
  | the invoke's decision completes before registering | panicked with "DI: service `*app.Dialer` has already been declared" | the default instance   | 1         |
```

<!-- cases: N/A 2 overrides, 3 zero values, 4 boundaries, 5 invalid input: no input -->
<!-- cases: 6 duplicate: the panic row is the duplicate path -->
<!-- cases: 7 lifecycle: the shutdown column pins that no built instance leaks -->
<!-- cases: 8 concurrency: the subject; a stress variant of both rows runs 1,000 times under -race -->
<!-- cases: 9 panics: the duplicate panic is the only panic -->
<!-- cases: N/A 10 compatibility: new behavior -->

### Requirement: Explain reports default status
`ExplainServiceOutput` SHALL report a service's default state - `not-default`, `unresolved`, `chosen` or `yielded`, serialized to JSON by name - and, when yielded, the scope name and service name it yielded to.

#### Scenario: Explain output of default registrations
```gherkin
Given a root scope with child scope "ext"
And <setup>
When the consumer calls doger.ExplainNamedService on scope "ext" for "*app.Dialer"
Then the output reports default state "<state>", yielded-to scope "<scope>" and yielded-to service "<service>"

Examples:
  | setup                                                                  | state       | scope  | service      |
  | a default in "ext", not invoked                                        | unresolved  |        |              |
  | a default in "ext", invoked, nothing in root                           | chosen      |        |              |
  | a default in "ext" and an explicit one in the root, invoked from "ext" | yielded     | [root] | *app.Dialer  |
  | an explicit service in "ext"                                           | not-default |        |              |
```

#### Scenario: Default state in JSON
```gherkin
Given a root scope with a default lazy "*app.Dialer" in child scope "ext", invoked, and an explicit one in the root
When the consumer marshals doger.ExplainNamedService(ext, "*app.Dialer") to JSON
Then the JSON has "default_state": "yielded", "default_yielded_to_scope_name": "[root]" and "default_yielded_to_service": "*app.Dialer"
```

#### Scenario: Explaining does not resolve a default
```gherkin
Given a root scope
And <form> "*app.Pool" registered in the root with doger.AsDefault(), never invoked, implementing Shutdown() error
When the consumer calls doger.ExplainInjector on the root
Then doger.ExplainNamedService(root, "*app.Pool") reports default state "unresolved"
And registering an explicit lazy "*app.Pool" in the root succeeds

Examples:
  | form           |
  | an eager value |
  | a lazy service |
```

<!-- cases: N/A 2 overrides: explain has no options -->
<!-- cases: 3 zero values: an unknown service returns ok=false as today; not-default omits the default fields from JSON -->
<!-- cases: N/A 4 boundaries, 5 invalid input: no input -->
<!-- cases: 6 duplicate: a superseded default is no longer registered, so explain reports the explicit service as not-default -->
<!-- cases: 7 lifecycle: covered by the state rows; explaining never fixes a decision, pinned by "Explaining does not resolve a default" for eager and lazy forms -->
<!-- cases: N/A 8 concurrency: explain reads under the existing locks -->
<!-- cases: N/A 9 panics: explain does not panic -->
<!-- cases: 10 compatibility: existing fields are unchanged; the default fields are added and omitted when empty -->

### Requirement: Registrations without AsDefault are unchanged
A registration given no `AsDefault()` SHALL behave exactly as before this change, including the panic on a duplicate name in one scope and shadowing of an ancestor service by a same-name service in a nearer scope.

#### Scenario: An explicit duplicate in one scope still panics
```gherkin
Given a root scope with an explicit lazy "*app.Dialer" registered in the root
When the consumer registers an explicit lazy "*app.Dialer" in the root
Then the registration panics with "DI: service `*app.Dialer` has already been declared"
```

#### Scenario: A nearer explicit service still shadows an ancestor's
```gherkin
Given a root scope with child scope "ext"
And an explicit lazy "*app.Dialer" producing "first" in the root and one producing "second" in scope "ext"
When the consumer invokes "*app.Dialer" from scope "ext"
Then the result is "second"
```

<!-- cases: N/A 2-9: this requirement only pins that existing behavior is unchanged; each behavior is specified where it is owned -->
<!-- cases: 10 compatibility: the subject -->
