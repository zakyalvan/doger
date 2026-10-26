# registration-options Specification

## Purpose

Lets a consumer attach opt-ins and metadata (starter opt-in, declared dependencies, component kinds) to a service at registration time, with predictable behavior under clone, override and alias.

## Requirements

### Requirement: Registration functions accept options without breaking existing calls
Every `Provide*`, `Override*` and `Package` helper (`Lazy*`, `Eager*`, `Transient*`) SHALL accept a trailing variadic `...ProvideOption`. A call without options SHALL behave exactly as before this change. A nil option in the list SHALL be ignored.

#### Scenario: Registration with and without options
```gherkin
Given a root scope with lazy "db" of type *app.DB registered without options
When the consumer registers "<service>" of type <type> with <helper> and options <options>
Then invoking "<service>" returns the registered instance with a nil error
And the registration carries the opt-ins <optins>

Examples:
  | helper                    | service      | type                          | options                    | optins        |
  | doger.Provide             | *app.Server  | *app.Server (Start() error)   | none                       | none          |
  | doger.ProvideNamed        | srv          | *app.Server (Start() error)   | doger.AsStarter()          | starter       |
  | doger.ProvideValue        | *app.Config  | *app.Config (no Start)        | nil                        | none          |
  | doger.ProvideNamedValue   | srv          | *app.Server (Start() error)   | nil, doger.AsStarter()     | starter       |
  | doger.Lazy (via Package)  | *app.Job     | *app.Job (Run(ctx) error)     | doger.AsRunnable()         | runnable      |
  | doger.EagerNamed          | cfg          | *app.Config (no Start)        | doger.DependsOnNamed("db") | depends-on db |
```

#### Scenario: Registration functions keep their parameters and add a variadic option list
```gherkin
Given the exported registration function <function>
When the test inspects reflect.TypeOf(<function>)
Then IsVariadic() is true
And its last parameter type is []doger.ProvideOption
And its other parameters are unchanged

Examples:
  | function                         |
  | doger.Provide[int]               |
  | doger.ProvideNamed[int]          |
  | doger.ProvideValue[int]          |
  | doger.ProvideNamedValue[int]     |
  | doger.ProvideTransient[int]      |
  | doger.ProvideNamedTransient[int] |
  | doger.Override[int]              |
  | doger.OverrideNamed[int]         |
  | doger.OverrideValue[int]         |
  | doger.OverrideNamedValue[int]    |
  | doger.OverrideTransient[int]     |
  | doger.OverrideNamedTransient[int]|
  | doger.Lazy[int]                  |
  | doger.LazyNamed[int]             |
  | doger.Eager[int]                 |
  | doger.EagerNamed[int]            |
  | doger.Transient[int]             |
  | doger.TransientNamed[int]        |
```

<!-- cases: N/A 4 boundaries: options are an unbounded list with no limits; 0 options and 1 or 2 options are covered by the Examples rows -->
<!-- cases: N/A 7 lifecycle: options only exist at registration time; lifecycle effects are specified in container-start, component-kinds and dependency-declaration -->
<!-- cases: N/A 8 concurrency: registration concurrency is unchanged from the existing scope locking -->
<!-- cases: 9 panics: invalid option combinations are covered by "Invalid opt-ins panic at registration" -->
<!-- cases: 10 compatibility: call sites compile unchanged (first scenario); storing a function as a value of the old type no longer compiles, which is the documented break pinned by the second scenario -->

### Requirement: Options are opaque and constructed only by the library
`ProvideOption` SHALL be an opaque type whose values come only from constructors exported by this library: currently `AsStarter()`, `DependsOn[T]()`, `DependsOnNamed(name)`, `AsRunnable()`, `Kind[T, M].With(meta)` and `WithOrder(n)`. Later changes MAY add constructors. Consumers SHALL NOT be able to create options that bypass the validation those constructors perform.

#### Scenario: Each constructor yields an option recognized by registration
```gherkin
Given a root scope
And var scheduled = doger.NewKind[app.Job, Spec]("scheduled"), where app.Job is Run(ctx) error
When the consumer registers a lazy "*app.Server" (which implements Start(ctx) error and Run(ctx) error) with option <option>
Then the registration carries opt-in <optin>

Examples:
  | option                                   | optin                                  |
  | doger.AsStarter()                        | starter                                |
  | doger.DependsOn[*app.DB]()               | depends-on "*app.DB"                   |
  | doger.DependsOnNamed("primary-db")       | depends-on "primary-db"                |
  | doger.AsRunnable()                       | kind "runnable" with meta {}           |
  | scheduled.With(Spec{Every: time.Minute}) | kind "scheduled" with meta {Every: 1m} |
  | doger.WithOrder(-10)                     | collection order -10                   |
```

<!-- cases: N/A 2 overrides: there is nothing to override; this requirement constrains the option type itself -->
<!-- cases: N/A 3 zero values: a zero ProvideOption is nil and is covered by the previous requirement (nil ignored); a zero Kind is covered in component-kinds -->
<!-- cases: N/A 4 boundaries, 6 duplicates: covered by "Invalid opt-ins panic at registration" and "Repeated options"; repeated WithOrder (last wins) is covered in collection-ordering -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: constructing an option is a pure value creation -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Repeated options combine predictably
Repeating `DependsOn`/`DependsOnNamed` SHALL accumulate dependencies in the given order, with duplicates collapsed. Repeating `AsStarter()` SHALL be equivalent to giving it once. Applying the same `Kind` twice to one registration SHALL panic at registration, naming the kind and the service.

#### Scenario: Repeated options
```gherkin
Given a root scope
And var scheduled = doger.NewKind[app.Job, Spec]("scheduled")
When the consumer registers lazy "worker" of type *app.Worker (Start() error and Run(ctx) error) with options <options>
Then the outcome is <outcome>

Examples:
  | options                                                          | outcome                                                       |
  | DependsOnNamed("a"), DependsOnNamed("b")                         | depends-on ["a", "b"]                                         |
  | DependsOnNamed("a"), DependsOnNamed("a")                         | depends-on ["a"]                                              |
  | AsStarter(), AsStarter()                                         | starter                                                       |
  | AsRunnable(), AsRunnable()                                       | panic "DI: kind `runnable` applied twice to service `worker`" |
  | scheduled.With(Spec{Every: 1m}), scheduled.With(Spec{Every: 2m}) | panic "DI: kind `scheduled` applied twice to service `worker`" |
```

<!-- cases: N/A 2 overrides: no option changes this combination rule -->
<!-- cases: N/A 7 lifecycle, 8 concurrency: registration-time only -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Invalid opt-ins panic at registration
Registration SHALL panic before the service is stored, naming the service, when `AsStarter()` is applied to a concrete type with no `Starter*` variant, when `AsStarter()` or a kind option is applied to a transient registration, or when a kind option is applied to a concrete type not assignable to the kind's `T`. Interface-typed registrations are checked on the instance at Start instead. `DependsOn*` on a transient SHALL be allowed.

#### Scenario: Invalid opt-in combinations
```gherkin
Given a root scope
And var scheduled = doger.NewKind[doger.Job, Spec]("scheduled"), where doger.Job is a test interface Run(ctx) error
When the consumer registers "<service>" of type <type> with <helper> and option <option>
Then registration <result>
And "<service>" is registered afterwards: <registered>

Examples:
  | service | type                                | helper                | option                  | result                                                                                         | registered |
  | cfg     | *app.Config (no Start)              | ProvideNamed          | AsStarter()             | panics "DI: service `cfg` does not implement any Starter interface"                            | false      |
  | srv     | *app.Server (Start() error)         | ProvideNamedTransient | AsStarter()             | panics "DI: transient service `srv` cannot be started"                                         | false      |
  | tjob    | *app.Job (Run(ctx) error)           | ProvideNamedTransient | scheduled.With(Spec{})  | panics "DI: transient service `tjob` cannot be handled by kind `scheduled`"                    | false      |
  | plain   | *app.Config (no Run)                | ProvideNamed          | scheduled.With(Spec{})  | panics "DI: service `plain` is not assignable to `doger.Job` required by kind `scheduled`"     | false      |
  | job     | *app.Job (Run(ctx) error)           | ProvideNamedTransient | AsRunnable()            | panics "DI: transient service `job` cannot be handled by kind `runnable`"                      | false      |
  | cfg     | *app.Config (no Run)                | ProvideNamed          | AsRunnable()            | panics "DI: service `cfg` is not assignable to `doger.Runner` required by kind `runnable`"     | false      |
  | life    | app.Lifecycle (interface, no Start) | ProvideNamed          | AsStarter()             | succeeds (checked on the instance at Start)                                                    | true       |
  | life    | app.Lifecycle (interface, no Run)   | ProvideNamed          | scheduled.With(Spec{})  | succeeds (checked on the instance at Start)                                                    | true       |
  | req     | *app.Request                        | ProvideNamedTransient | DependsOnNamed("db")    | succeeds                                                                                       | true       |
  | srv     | *app.Server (Start() error)         | ProvideNamedValue     | AsStarter()             | succeeds                                                                                       | true       |
```

<!-- cases: N/A 2 overrides: validation cannot be disabled; opt-ins with an unusable target would silently do nothing -->
<!-- cases: N/A 3 zero values: an empty service name behaves exactly as before; a nil option is ignored (first requirement) -->
<!-- cases: 5 errors: the type in the "not assignable" message is rendered with reflect.Type.String(), so it reads `doger.Runner` -->
<!-- cases: N/A 7 lifecycle, 8 concurrency: registration-time only -->
<!-- cases: N/A 10 compatibility: new API; registrations without options never panic for these reasons -->

### Requirement: Metadata follows clone and override, and is not inherited by aliases
`RootScope.Clone`/`CloneWithOpts` SHALL copy each registration's options. `Override*` SHALL replace the previous registration's options with the new call's options; no options means none. An alias (`As`, `AsNamed`, `Bind`, `BindNamed`) SHALL carry no opt-ins of its own, and SHALL NOT inherit the target's opt-ins.

#### Scenario: Clone, override and alias
```gherkin
Given a root scope with lazy "*app.Server" (Start() error) registered with AsStarter() and DependsOnNamed("db")
When the consumer performs <operation>
Then the registration seen at "<name>" in <container> carries <optins>

Examples:
  | operation                                                    | container | name          | optins                   |
  | Clone()                                                      | clone     | *app.Server   | starter, depends-on "db" |
  | Override[*app.Server](i, newServer)                          | original  | *app.Server   | none                     |
  | Override[*app.Server](i, newServer, DependsOnNamed("cache")) | original  | *app.Server   | depends-on "cache"       |
  | As[*app.Server, app.Lifecycle](i)                            | original  | app.Lifecycle | none                     |
  | As[*app.Server, app.Lifecycle](i)                            | original  | *app.Server   | starter, depends-on "db" |
```

<!-- cases: N/A 2 overrides: this requirement is itself the override behavior -->
<!-- cases: N/A 3 zero values: Override without options is the "none" row -->
<!-- cases: N/A 4 boundaries: no numeric limits -->
<!-- cases: N/A 8 concurrency: Clone and Override keep the existing scope locking -->
<!-- cases: N/A 9 panics: no new panic paths -->
<!-- cases: 10 compatibility: Clone/Override/As behave as before for registrations without options -->

### Requirement: Registration order is kept by override and clone
Wherever this change orders services by registration (ties in discovery, start and kind handling), a service SHALL keep the position of its first registration in its own scope when it is overridden there. A clone SHALL keep its source's positions, and services registered later in the clone SHALL come after them. Positions SHALL stay distinct within a container, including in a clone made while services are being registered on its source.

#### Scenario: Registration order survives override and clone
```gherkin
Given a root scope and a child scope "request"
And lazy Starters registered in order: "a" in root, "b" in "request", "c" in root, with no dependencies
And the consumer then <operation>
When the consumer calls Start() on <container>
Then the start order is <order>

Examples:
  | operation                                                     | container | order      |
  | does nothing else                                             | the root  | a, c, b    |
  | overrides "a" in root with another lazy Starter               | the root  | a, c, b    |
  | overrides "a" in "request" with another lazy Starter          | the root  | a in root, c, b, a in "request" |
  | clones the root and registers lazy Starter "d" in the clone's root | the clone | a, c, d, b |
```

<!-- cases: N/A 2 overrides: Override is a covered row -->
<!-- cases: N/A 3 zero values: an empty container is covered by container-start ("Start on an empty container") -->
<!-- cases: N/A 5 errors: ordering cannot fail -->
<!-- cases: N/A 9 panics: none -->
<!-- cases: 6 duplicates: overriding in a child scope registers a new, later service in that scope (third row: root "a", then "request" "a") -->
<!-- cases: 8 concurrency: concurrent registrations from many goroutines get distinct positions, including registrations racing a Clone; checked by the internal race tests TestRegistrationIndex_ConcurrentUnique and TestRegistrationIndex_CloneDuringRegistration listed in design.md -->
<!-- cases: N/A 10 compatibility: existing APIs did not promise an order -->
