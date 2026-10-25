# Spec Delta

## MODIFIED Requirements

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
