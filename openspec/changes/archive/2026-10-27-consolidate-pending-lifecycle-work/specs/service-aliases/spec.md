# Spec Delta

## Purpose

Defines how an alias registered with `As`, `AsNamed`, `Bind` or `BindNamed` relates to its target during shutdown, so a target is shut down exactly once, by its own registration.

## ADDED Requirements

### Requirement: An alias never shuts its target down
Shutting down an alias (through scope shutdown or `Shutdown`/`ShutdownNamed` on the alias name) SHALL remove the alias without calling the target's `Shutdown` method. The target SHALL be shut down only through its own registration, exactly once.

#### Scenario: Target is shut down once
```gherkin
Given a root scope with <registration> "*Svc" implementing Shutdown() error that counts its calls
And the alias As[*Svc, SvcIface] registered in <alias-scope>
And the alias was invoked from <alias-scope>
When the consumer calls <operation>
Then the target's Shutdown was called <calls> times
And the returned report or error is successful

Examples:
  | registration | alias-scope | operation                          | calls |
  | eager value  | the root    | root.Shutdown()                    | 1     |
  | lazy, built  | the root    | root.Shutdown()                    | 1     |
  | eager value  | child "c"   | c.Shutdown()                       | 0     |
  | eager value  | child "c"   | root.Shutdown()                    | 1     |
  | eager value  | the root    | ShutdownNamed(root, "SvcIface")    | 0     |
  | lazy, built  | the root    | ShutdownNamed(root, "*Svc")        | 1     |
```

#### Scenario: Repeated runs are stable
```gherkin
Given the first row of "Target is shut down once"
When it runs 200 times, each on a new root scope
Then the target's Shutdown was called exactly 1 time in every run
```

#### Scenario: Invoking the alias after its target is shut down
```gherkin
Given a root scope with lazy "*Svc" and the alias As[*Svc, SvcIface]
And the consumer called ShutdownNamed(root, "*Svc")
When the consumer invokes "SvcIface"
Then the error matches ErrServiceNotFound
```

<!-- cases: 1 default: rows 1 and 2 are the zero-configuration path -->
<!-- cases: N/A 2 overrides: shutting a target through an alias is never correct; a consumer shuts the target by its own name -->
<!-- cases: N/A 3 zero values: an alias without a target cannot be registered (As fails) -->
<!-- cases: 4 boundaries: alias in the same scope and in a child scope -->
<!-- cases: 5 errors: shutting the alias returns nil; invoking it after the target is gone is ErrServiceNotFound, unchanged -->
<!-- cases: 6 duplicates: the double-shutdown defect is the subject; 200 runs pin it -->
<!-- cases: 7 lifecycle: child-scope shutdown keeps a parent-scope target running until the parent shuts down -->
<!-- cases: N/A 8 concurrency: scope shutdown is already parallel; the repeated-runs scenario runs under -race -->
<!-- cases: N/A 9 panics: no new panic path -->
<!-- cases: 10 compatibility: BEHAVIOR CHANGE (flagged) - row 3: shutting down a child scope no longer shuts down a parent-scope target through an alias registered in the child; row 5: ShutdownNamed on an alias no longer shuts its target -->
