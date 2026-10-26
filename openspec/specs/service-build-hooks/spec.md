# service-build-hooks Specification

## Purpose

Lets consumers observe each service instance right after the container builds it, for example to register metrics or validate configuration, without wrapping every provider.

## Requirements

### Requirement: HookAfterBuild receives each built instance
`InjectorOpts.HookAfterBuild []func(scope *Scope, serviceName string, instance any)` and `RootScope.AddAfterBuildHook` SHALL run, in registration order, after every successful provider call (each transient instance included) and after an eager value's first successful invocation. They SHALL NOT run for a failed build, a cached lazy instance, or an alias. Nil hooks SHALL be ignored.

#### Scenario: Which invocations fire the build hook
```gherkin
Given a root scope with a HookAfterBuild hook recording (scope name, service name, instance)
And "<service>" registered as <registration>
When the consumer invokes "<service>" <times>
Then the hook recorded <records>

Examples:
  | service | registration                         | times | records                                   |
  | db      | lazy, provider returns &DB{}          | twice | 1 record ([root], db, the same *DB)       |
  | req     | transient, provider returns &Req{}    | twice | 2 records, each with the instance returned |
  | cfg     | eager value &Cfg{}                     | twice | 1 record ([root], cfg, that *Cfg)         |
  | bad     | lazy, provider returns errBuild        | once  | nothing                                   |
  | boom    | lazy, provider panics with "kaboom"    | once  | nothing                                   |
  | iface   | alias As[*DB, DBIface] of lazy db      | once  | 1 record for db, none for the alias name  |
```

#### Scenario: Build hook order relative to invocation hooks
```gherkin
Given a root scope with HookBeforeInvocation, HookAfterBuild and HookAfterInvocation hooks recording their names
And lazy "db"
When the consumer invokes "db"
Then the recorded calls are before-invocation db, after-build db, after-invocation db
```

#### Scenario: Registration order and nil hooks
```gherkin
Given a root scope with HookAfterBuild hooks "h1", nil and "h2" in InjectorOpts
And root.AddAfterBuildHook("h3") and root.AddAfterBuildHook(nil)
When the consumer invokes lazy "db"
Then the recorded calls are h1 db, h2 db, h3 db
```

#### Scenario: Build hook in a child scope and clones
```gherkin
Given a root scope with a HookAfterBuild hook recording scope names
And a child scope "request" with lazy "handler"
When the consumer invokes "handler" from <container>
Then the hook recorded scope <scope>

Examples:
  | container                     | scope   |
  | the root's child "request"    | request |
  | root.Clone()'s child "request" | request |
```

#### Scenario: A panicking build hook propagates
```gherkin
Given a root scope with a HookAfterBuild hook that panics with "hook-boom"
And lazy "db"
When the consumer invokes "db"
Then the invocation panics with "hook-boom"
```

<!-- cases: 1 default: no hook registered keeps invocation unchanged (existing invoke suites) -->
<!-- cases: 2 overrides: field and Add method; clones copy the hook through InjectorOpts.copy like every other hook -->
<!-- cases: 3 zero values: nil hooks; a typed-nil instance returned by a provider is passed as is -->
<!-- cases: 4 boundaries: 0, 1 and many invocations per registration type -->
<!-- cases: 5 errors: failed and panicking builds fire nothing -->
<!-- cases: 6 duplicates: a cached lazy instance does not fire again; an alias does not fire for its own name -->
<!-- cases: 7 lifecycle: child scopes and clones -->
<!-- cases: N/A 8 concurrency: a lazy instance is built once under its wrapper's lock, so the hook fires once; re-entry has its own requirement -->
<!-- cases: 9 panics: a panicking hook propagates, consistent with the existing per-service hooks -->
<!-- cases: N/A 10 compatibility: new API -->

### Requirement: Build hooks run outside the wrapper lock
A `HookAfterBuild` hook SHALL run after the wrapper released its lock, so it may invoke, health-check or explain the service it receives. Another goroutine MAY obtain the instance before the hook returned.

#### Scenario: A build hook can use its own service
```gherkin
Given a root scope with a HookAfterBuild hook that calls <call> for the scope and service name it receives, and records the result
And "<service>" registered as <registration>
When the consumer invokes "<service>" within 1s
Then the invocation returns the instance and a nil error
And the hook recorded a nil error

Examples:
  | service | registration                                              | call             |
  | hc      | lazy, provider returns a Healthchecker                    | HealthCheckNamed |
  | db      | lazy, provider returns &DB{}                               | InvokeNamed      |
  | cfg     | eager value &Cfg{}, registered with DependsOnNamed("db")   | InvokeNamed      |
```

<!-- cases: 1 default: lazy rows -->
<!-- cases: N/A 2 overrides: there is no option -->
<!-- cases: N/A 3 zero values, 4 boundaries: one hook -->
<!-- cases: N/A 5 errors: the call results are asserted -->
<!-- cases: 6 duplicates: the hook still fires once per build -->
<!-- cases: 7 lifecycle: an eager value with DependsOn -->
<!-- cases: 8 concurrency: re-entry from the hook does not deadlock; tests run under testWithTimeout -->
<!-- cases: N/A 9 panics: covered by "A panicking build hook propagates" -->
<!-- cases: N/A 10 compatibility: new API -->
