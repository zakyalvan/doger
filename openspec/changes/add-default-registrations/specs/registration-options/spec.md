# Spec Delta

<!-- Based on the text of this requirement as modified by consolidate-pending-lifecycle-work, which must be archived before this change. -->

## MODIFIED Requirements

### Requirement: Options are opaque and constructed only by the library
`ProvideOption` SHALL be an opaque type whose values come only from constructors exported by this library: currently `AsStarter()`, `DependsOn[T]()`, `DependsOnNamed(name)`, `AsRunnable()`, `Kind[T, M].With(meta)`, `WithOrder(n)` and `AsDefault()`. Later changes MAY add constructors. Consumers SHALL NOT be able to create options that bypass the validation those constructors perform.

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
  | doger.AsDefault()                        | default                                |
```

<!-- cases: N/A 2 overrides: there is nothing to override; this requirement constrains the option type itself -->
<!-- cases: N/A 3 zero values: a zero ProvideOption is nil and is covered by the previous requirement (nil ignored); a zero Kind is covered in component-kinds -->
<!-- cases: N/A 4 boundaries, 6 duplicates: covered by "Invalid opt-ins panic at registration" and "Repeated options"; repeated WithOrder (last wins) is covered in collection-ordering; repeated AsDefault is covered in default-registrations -->
<!-- cases: N/A 7 lifecycle, 8 concurrency, 9 panics: constructing an option is a pure value creation -->
<!-- cases: N/A 10 compatibility: new API -->

## ADDED Requirements

### Requirement: AsDefault combines with every other option
`AsDefault()` SHALL be accepted on every registration form (lazy, eager, transient and the `Package` helpers) and with every other option. The other options SHALL apply to the default when it uses its own provider and SHALL have no effect when it yields. Options SHALL be validated before a default is skipped, so an invalid combination panics whether or not the default takes effect.

#### Scenario: Options on a default apply only when it uses its own provider
```gherkin
Given a root scope with child scope "ext"
And a lazy "cfg" in scope "ext" whose provider counts its calls
And a lazy "*app.Pool" registered in scope "ext" with doger.AsDefault() and <options>, implementing Start(ctx) error and Run(ctx) error, never invoked
And <explicit>
When the consumer calls Start on the root
Then the default pool's Start was called <starts> times, its Run <runs> times, and "cfg" was built <cfg_builds> times

Examples:
  | options                                          | explicit                                                   | starts | runs | cfg_builds |
  | doger.AsStarter(), doger.DependsOnNamed("cfg")   | nothing is registered in the root                          | 1      | 0    | 1          |
  | doger.AsStarter(), doger.DependsOnNamed("cfg")   | an explicit "*app.Pool" is in the root                     | 0      | 0    | 0          |
  | doger.AsRunnable()                               | nothing is registered in the root                          | 1      | 1    | 0          |
  | doger.AsRunnable()                               | an explicit "*app.Pool" is in the root                     | 0      | 0    | 0          |
```

#### Scenario: AsDefault adds no registration panic of its own
```gherkin
Given a root scope
When the consumer registers "*app.Dialer" as <form> with <options>
Then the registration <outcome>

Examples:
  | form                           | options                               | outcome                                                                  |
  | doger.Provide                  | doger.AsDefault()                     | does not panic                                                           |
  | doger.ProvideValue             | doger.AsDefault()                     | does not panic                                                           |
  | doger.ProvideTransient         | doger.AsDefault()                     | does not panic                                                           |
  | doger.ProvideNamed "dialer"    | doger.AsDefault()                     | does not panic                                                           |
  | doger.Package(doger.Lazy(...)) | doger.AsDefault()                     | does not panic                                                           |
  | doger.ProvideTransient         | doger.AsDefault(), doger.AsStarter()  | panics with "DI: transient service `*app.Dialer` cannot be started"      |
```

#### Scenario: An invalid option panics even when the default would be skipped
```gherkin
Given a root scope with an explicit lazy "*app.Dialer" in the root
When the consumer registers a transient "*app.Dialer" in the root with doger.AsDefault() and doger.AsStarter()
Then the registration panics with "DI: transient service `*app.Dialer` cannot be started"
```

<!-- cases: N/A 3 zero values: a nil option next to AsDefault is ignored as today -->
<!-- cases: 4 boundaries: one and several combined options are the rows -->
<!-- cases: 5 invalid input: the AsStarter-on-transient rows; the message is the existing one -->
<!-- cases: 6 duplicate: a second default of one name panics as specified in default-registrations -->
<!-- cases: 7 lifecycle: Start rows of the first scenario -->
<!-- cases: N/A 8 concurrency: registration-time only -->
<!-- cases: 9 panics: second and third scenarios -->
<!-- cases: 10 compatibility: other options behave as before when AsDefault is absent -->
