# Spec Delta

<!-- collection-invocation is introduced by consolidate-pending-lifecycle-work, which must be archived before this change. The MODIFIED block copies its requirement whole. -->

## MODIFIED Requirements

### Requirement: Aliases are excluded from collections
Services registered through `As`/`Bind` aliases SHALL NOT appear in a collection. The aliased original SHALL represent itself when its registered type is assignable to `T`. Exports are not `As`/`Bind` aliases: their membership is specified in scope-export ("Collections and type selection see exports in the parent").

#### Scenario: Alias does not duplicate its target
```gherkin
Given a root scope
And a lazy service "*AuditHandler" of type "*AuditHandler" implementing "EventHandler"
And an alias of "*AuditHandler" registered as type "EventHandler"
When the consumer calls InvokeAll for type "EventHandler"
Then the result has exactly 1 element
And that element is the instance of "*AuditHandler"
```

<!-- cases: N/A 2 overrides: alias exclusion is not configurable; register the instance directly to include it -->
<!-- cases: N/A 3 zero values: an alias-only match is impossible, because As requires its target to be assignable to the alias type -->
<!-- cases: N/A 4 boundaries: covered by the 1-element scenario above -->
<!-- cases: N/A 5 errors, 8 concurrency: alias filtering has no failure or shared state -->
<!-- cases: N/A 9 panics: no panicking path -->
<!-- cases: N/A 10 compatibility: aliases keep resolving through Invoke and InvokeAs unchanged; exports are a new kind of registration -->
