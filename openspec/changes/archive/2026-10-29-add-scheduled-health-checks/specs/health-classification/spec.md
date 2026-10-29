# Spec Delta

## Purpose

Lets a consumer classify each health check at registration, by tags such as readiness and liveness and by severity (critical or optional), and read a report filtered by tag, so one container can answer "is it ready?" and "is it alive?" separately and report a failing optional dependency as degraded rather than down.

## ADDED Requirements

### Requirement: An unclassified health checker counts toward readiness and is critical
A health checker registered without `WithHealthTags` SHALL carry the single tag `HealthTagReadiness` ("readiness"), and one registered without `AsOptionalHealth` SHALL be critical. Report entries SHALL expose `Tags`, sorted, and `Optional`.

#### Scenario: Default classification
```gherkin
Given an injector that is never started
And a Healthchecker value "db" registered with options <options>
When the consumer calls HealthReport() on the root scope
Then the entry for "db" has Tags ["readiness"] and Optional false

Examples:
  | options |
  | none    |
  | nil     |
```

#### Scenario: Predefined tag values
```gherkin
Given the constant <constant>
When the consumer reads its value
Then it is "<value>"

Examples:
  | constant           | value     |
  | HealthTagReadiness | readiness |
  | HealthTagLiveness  | liveness  |
```

<!-- cases: 1 default: this requirement -->
<!-- cases: N/A 2 overrides: the next two requirements -->
<!-- cases: 3 zero values: a nil option is ignored as in registration-options -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: invalid options are a later requirement -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: N/A 7 lifecycle: Clone and Override are a later requirement -->
<!-- cases: N/A 8 concurrency: options are values fixed at registration -->
<!-- cases: N/A 9 panics: a later requirement -->
<!-- cases: 10 backward compatibility: a registration without options behaves as before; every checker is in the unfiltered report -->

### Requirement: WithHealthTags replaces the default tags
`WithHealthTags(tags...)` SHALL replace the default tags with the given ones. Repeated `WithHealthTags` options SHALL accumulate, with duplicates collapsed, and `WithHealthTags()` with no tag SHALL leave the service with no tag.

#### Scenario: Tags of a registration
```gherkin
Given an injector that is never started
And a Healthchecker value "db" registered with options <options>
When the consumer calls HealthReport() on the root scope
Then the entry for "db" has Tags <tags>

Examples:
  | options                                                 | tags                      |
  | WithHealthTags(HealthTagLiveness)                       | ["liveness"]              |
  | WithHealthTags(HealthTagLiveness, HealthTagReadiness)   | ["liveness", "readiness"] |
  | WithHealthTags("db"), WithHealthTags("db", "external")  | ["db", "external"]        |
  | WithHealthTags()                                        | []                        |
```

<!-- cases: N/A 1 default: previous requirement -->
<!-- cases: 2 overrides: single, several and repeated options -->
<!-- cases: 3 zero values: no tag given -->
<!-- cases: 4 boundaries: 0, 1 and 2 tags; repeats -->
<!-- cases: N/A 5 errors: invalid options are a later requirement -->
<!-- cases: 6 not found/duplicate: duplicate tags collapse -->
<!-- cases: N/A 7 lifecycle: a later requirement -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: a later requirement -->
<!-- cases: N/A 10 backward compatibility: new option -->

### Requirement: AsOptionalHealth marks a service optional
`AsOptionalHealth()` SHALL mark the service optional, whatever its tags. Repeating it SHALL be equivalent to giving it once.

#### Scenario: Severity of a registration
```gherkin
Given an injector that is never started
And a Healthchecker value "db" registered with options <options>
When the consumer calls HealthReport() on the root scope
Then the entry for "db" has Tags <tags> and Optional <optional>

Examples:
  | options                                     | tags          | optional |
  | AsOptionalHealth()                          | ["readiness"] | true     |
  | AsOptionalHealth(), AsOptionalHealth()      | ["readiness"] | true     |
  | AsOptionalHealth(), WithHealthTags("cache") | ["cache"]     | true     |
```

<!-- cases: N/A 1 default: first requirement -->
<!-- cases: 2 overrides: with and without tags -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: 4 boundaries: repeated option -->
<!-- cases: N/A 5 errors: a later requirement -->
<!-- cases: 6 not found/duplicate: idempotent repeat -->
<!-- cases: N/A 7 lifecycle: a later requirement -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: a later requirement -->
<!-- cases: N/A 10 backward compatibility: new option -->

### Requirement: Invalid health classification panics at registration
Registration SHALL panic before the service is stored, naming the service, when `WithHealthTags` is given an empty tag, or when `WithHealthTags` or `AsOptionalHealth` is applied to a transient registration. Classification options on a service that turns out not to be a health checker SHALL have no effect.

#### Scenario: Invalid classification
```gherkin
Given an injector
When the consumer calls <registration> for "<name>" with <option>
Then the call <outcome>
And the service is registered: <registered>

Examples:
  | name  | registration          | option                       | outcome                                                     | registered |
  | db    | ProvideNamedValue     | WithHealthTags("")           | panics "DI: service `db` has an empty health tag"           | false      |
  | db    | ProvideNamedValue     | WithHealthTags("ok", "")     | panics "DI: service `db` has an empty health tag"           | false      |
  | tmp   | ProvideNamedTransient | WithHealthTags("readiness")  | panics "DI: transient service `tmp` cannot be health-checked" | false    |
  | tmp   | ProvideNamedTransient | AsOptionalHealth()           | panics "DI: transient service `tmp` cannot be health-checked" | false    |
  | cfg   | ProvideNamedValue (no health check) | AsOptionalHealth() | returns; HealthReport() does not list "cfg"              | true       |
```

<!-- cases: N/A 1 default: no option, no panic -->
<!-- cases: 2 overrides: each option -->
<!-- cases: 3 zero values: the empty tag -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: 5 errors: each panic message names the service -->
<!-- cases: N/A 6 not found/duplicate: duplicates collapse (previous requirement) -->
<!-- cases: 7 lifecycle: the panic happens before the service is stored -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: 9 panics: this requirement -->
<!-- cases: N/A 10 backward compatibility: new options -->

### Requirement: Classification follows clones, overrides, defaults and aliases
`Clone` and `CloneWithOpts` SHALL copy each registration's classification. `Override*` SHALL replace it with the new call's options, and no options SHALL mean the default. A default that yields SHALL take no part: the entry shows the classification of the service it yielded to. An alias or an export carries no classification and no entry of its own.

#### Scenario: Classification across clone, override and alias
```gherkin
Given an injector with a Healthchecker value "db" registered with WithHealthTags(HealthTagLiveness) and AsOptionalHealth()
When the consumer <action> and calls HealthReport() on <target>
Then the entry for "<entry>" has Tags <tags> and Optional <optional>

Examples:
  | action                                                          | target         | entry | tags          | optional |
  | clones the injector                                             | the clone      | db    | ["liveness"]  | true     |
  | overrides "db" with OverrideNamedValue and no options           | the injector   | db    | ["readiness"] | false    |
  | overrides "db" with OverrideNamedValue and WithHealthTags("x")  | the injector   | db    | ["x"]         | false    |
  | adds an alias "hc" to "db" with AsNamed                         | the injector   | db    | ["liveness"]  | true     |
  | registers, in child scope "child", a Healthchecker value "db" with AsDefault() and WithHealthTags("x") | scope "child" | db | ["liveness"] | true |
```

<!-- cases: N/A 1 default: covered by the first requirement -->
<!-- cases: 2 overrides: Override with and without options -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: not applicable -->
<!-- cases: 6 not found/duplicate: Override, alias (no entry of its own, health-reports) and a yielded default -->
<!-- cases: 7 lifecycle: clone -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: follows the existing option rules of registration-options -->

### Requirement: A report can be filtered by tag
`HealthReport(opts...)` and `HealthReportWithContext(ctx, opts...)` SHALL accept `HealthReportOption` values. `MatchHealthTags(tags...)` SHALL limit the report, its overall state and any on-demand checks to covered services carrying at least one of the tags; repeated options accumulate. With no tag given, or no option, the report SHALL cover every health checker.

#### Scenario: Filtered reports
```gherkin
Given an injector that is never started, with Healthchecker values "db" (no options, returns errors.New("down")), "proc" (WithHealthTags(HealthTagLiveness), returns nil) and "cache" (WithHealthTags("cache", HealthTagReadiness), returns nil)
When the consumer calls HealthReport(<options>) on the root scope
Then the report lists <listed>
And its State is <state>

Examples:
  | options                                                         | listed                   | state           |
  | none                                                            | "cache", "db", "proc"    | HealthUnhealthy |
  | MatchHealthTags(HealthTagLiveness)                              | "proc"                   | HealthHealthy   |
  | MatchHealthTags(HealthTagReadiness)                             | "cache", "db"            | HealthUnhealthy |
  | MatchHealthTags("cache")                                        | "cache"                  | HealthHealthy   |
  | MatchHealthTags("cache"), MatchHealthTags(HealthTagLiveness)    | "cache", "proc"          | HealthHealthy   |
  | MatchHealthTags()                                               | "cache", "db", "proc"    | HealthUnhealthy |
  | MatchHealthTags("unknown-tag")                                  | no service               | HealthHealthy   |
  | nil                                                             | "cache", "db", "proc"    | HealthUnhealthy |
```

#### Scenario: A filtered on-demand read checks only the covered services
```gherkin
Given an injector that is never started, with Healthchecker values "db" (no options) and "proc" (WithHealthTags(HealthTagLiveness)), both counting their calls
When the consumer calls HealthReport(MatchHealthTags(HealthTagLiveness)) on the root scope
Then the health check of "proc" was called 1 time
And the health check of "db" was not called
```

<!-- cases: 1 default: no option covers every checker -->
<!-- cases: 2 overrides: single, repeated and custom tags -->
<!-- cases: 3 zero values: MatchHealthTags() and a nil option -->
<!-- cases: 4 boundaries: a filter matching 0, 1 and several services -->
<!-- cases: N/A 5 errors: an unknown tag matches nothing, which is not an error -->
<!-- cases: N/A 6 not found/duplicate: a service matching two tags is listed once (the repeated-option row) -->
<!-- cases: 7 lifecycle: on-demand reads of an idle container -->
<!-- cases: N/A 8 concurrency: filtering is a pure selection over the shared checks of health-reports -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: HealthReport() without options is unchanged -->

### Requirement: A failing optional service is degraded, not unhealthy
A failing check of an optional service SHALL give its entry `State` `HealthDegraded`, with the check's error in `Err`. A passing check SHALL give `HealthHealthy` as for a critical service. Service and container health hooks SHALL report the `HealthDegraded` state like any other state change.

#### Scenario: Degraded entries
```gherkin
Given an injector that is never started, with HealthCheckTimeout 10ms and a Healthchecker value "cache" registered with <options>, whose check returns <result>
When the consumer calls HealthReport() on the root scope
Then the entry for "cache" has State <state> and Err <err>

Examples:
  | options              | result               | state           | err                    |
  | AsOptionalHealth()   | errors.New("slow")   | HealthDegraded  | the same "slow" error  |
  | AsOptionalHealth()   | nil                  | HealthHealthy   | nil                    |
  | AsOptionalHealth()   | ctx.Err() after blocking until ctx is done (past the 10ms timeout) | HealthDegraded  | an error matching ErrHealthCheckTimeout |
  | AsOptionalHealth()   | a panic "boom"       | HealthDegraded  | an error matching ErrHealthCheckPanic   |
  | none                 | errors.New("slow")   | HealthUnhealthy | the same "slow" error  |
```

#### Scenario: Degraded transitions reach the hooks
```gherkin
Given a started injector with a manual HealthScheduler and a Healthchecker value "cache" registered with AsOptionalHealth()
And "cache" returns nil in the first round and errors.New("slow") afterwards
And a HookServiceHealthChanged hook and a HookContainerHealthChanged hook that record their calls
When the test runs the round twice
Then the service hook recorded (Unknown, Healthy, nil) then (Healthy, Degraded, "slow")
And the container hook recorded (Unknown, Healthy) then (Healthy, Degraded)
```

<!-- cases: 1 default: a critical service is unhealthy on failure (the "none" row) -->
<!-- cases: 2 overrides: AsOptionalHealth -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: 5 errors: plain error, timeout and panic errors keep their matches -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: transitions across rounds -->
<!-- cases: N/A 8 concurrency: hooks are serialized as in health-check-scheduling -->
<!-- cases: 9 panics: a panicking optional check is degraded -->
<!-- cases: N/A 10 backward compatibility: new state -->

### Requirement: Liveness depends only on the phase with zero configuration
With no service tagged `HealthTagLiveness`, a report filtered by `MatchHealthTags(HealthTagLiveness)` SHALL cover no service, SHALL run no check, and SHALL report `HealthHealthy` whatever the readiness checks return. The exceptions are `PhaseFailed`, which is `HealthUnhealthy` by the phase gate of health-reports, and `PhaseClosed`, which is `HealthUnknown`.

#### Scenario: Zero-config liveness
```gherkin
Given an injector with a Healthchecker value "db" (no options) that returns errors.New("down")
And the container is in <phase>
When the consumer calls HealthReport(MatchHealthTags(HealthTagLiveness)) on the root scope
Then the report covers no service and its State is <state>
And the health check of "db" was not called by that read

Examples:
  | phase                                  | state          |
  | PhaseIdle                              | HealthHealthy   |
  | PhaseStarting (blocked in a Starter)   | HealthHealthy   |
  | PhaseReady                             | HealthHealthy   |
  | PhaseClosing (blocked in a Closing hook) | HealthHealthy |
  | PhaseFailed (a Starter returned an error) | HealthUnhealthy |
  | PhaseClosed                            | HealthUnknown   |
```

<!-- cases: 1 default: this requirement is the zero-config path -->
<!-- cases: N/A 2 overrides: tagging a service liveness is the filter requirement -->
<!-- cases: 3 zero values: no liveness service -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: N/A 5 errors: a failing readiness check does not leak into liveness -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: 7 lifecycle: every phase; liveness follows Spring Boot's CORRECT during graceful shutdown -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: N/A 10 backward compatibility: new API -->

### Requirement: Classification does not change forced results
`HealthCheck()`, `HealthCheckWithContext()` and the single-service functions SHALL return each service's check error unchanged, whatever its tags or severity.

#### Scenario: Forced results ignore classification
```gherkin
Given an injector that is never started, with a Healthchecker value "cache" registered with <options>, whose check returns errors.New("slow")
When the consumer calls <entry>
Then the result for "cache" is the same "slow" error

Examples:
  | options                                          | entry                                 |
  | AsOptionalHealth()                               | HealthCheck() on the root scope       |
  | WithHealthTags(HealthTagLiveness)                | HealthCheck() on the root scope       |
  | AsOptionalHealth()                               | HealthCheckNamed(injector, "cache")   |
```

<!-- cases: N/A 1 default: covered in health-check-execution -->
<!-- cases: 2 overrides: each option -->
<!-- cases: N/A 3 zero values: not applicable -->
<!-- cases: N/A 4 boundaries: not applicable -->
<!-- cases: 5 errors: the error is returned unchanged -->
<!-- cases: N/A 6 not found/duplicate: not applicable -->
<!-- cases: N/A 7 lifecycle: not applicable -->
<!-- cases: N/A 8 concurrency: not applicable -->
<!-- cases: N/A 9 panics: not applicable -->
<!-- cases: 10 backward compatibility: this requirement -->
