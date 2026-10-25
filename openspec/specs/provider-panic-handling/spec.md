# provider-panic-handling Specification

## Purpose
Defines what a consumer gets when a service provider panics during invocation: an error instead of a crash, with the panic value preserved so it can be matched, including a panic with a nil value.

## Requirements

### Requirement: A panicking provider yields an invocation error
When a lazy or transient provider panics during invocation, the invocation SHALL return the zero value and a non-nil error instead of propagating the panic. An error panic value SHALL be returned as is. A nil panic value SHALL yield an error that matches `*runtime.PanicNilError` with `errors.As`. Any other value SHALL yield an error whose message is `DI: ` followed by the value.

#### Scenario: Provider panic values become invocation errors
```gherkin
Given a root scope
And a <kind> provider for type int that panics with <value>
When the consumer invokes that service
Then the returned value is 0
And the error message is "<message>"
And the error matches <match>

Examples:
  | kind      | value               | message                                       | match                                      |
  | lazy      | nil                 | runtime error: panic called with nil argument | errors.As *runtime.PanicNilError           |
  | transient | nil                 | runtime error: panic called with nil argument | errors.As *runtime.PanicNilError           |
  | lazy      | errors.New("boom")  | boom                                          | errors.Is the panicked error               |
  | transient | errors.New("boom")  | boom                                          | errors.Is the panicked error               |
  | lazy      | "s"                 | DI: s                                         | neither *runtime.PanicNilError nor "boom"  |
  | lazy      | 42                  | DI: 42                                        | neither *runtime.PanicNilError nor "boom"  |
```

#### Scenario: Consumers can restore the legacy nil-panic behavior
```gherkin
Given the process runs with GODEBUG "panicnil=1"
And a root scope with a lazy provider for type int that panics with nil
When the consumer invokes that service
Then the returned value is 0
And the error is nil
```

<!-- cases: 1 default: the first scenario is the zero-configuration behavior -->
<!-- cases: 2 overrides: GODEBUG panicnil=1 (or a //go:debug panicnil=1 directive in the consumer's main package) is the Go-provided override; second scenario -->
<!-- cases: 3 zero values: the nil panic value rows -->
<!-- cases: N/A 4 boundaries: no numeric limits -->
<!-- cases: 5 errors: each row states how the error is matched (errors.As / errors.Is / message) -->
<!-- cases: N/A 6 not found/duplicate: unrelated to provider panics -->
<!-- cases: N/A 7 lifecycle: recovery happens per invocation; the retry behavior of a lazy service after a failed build is unchanged and covered by existing tests -->
<!-- cases: N/A 8 concurrency: recovery is local to the invoking goroutine; concurrent lazy builds are covered by the existing race suite -->
<!-- cases: 9 panics: this requirement is the panic-recovery contract -->
<!-- cases: 10 backward compatibility: error and non-nil panic values are unchanged; the nil rows are deliberately BREAKING (previously the invocation returned 0 and a nil error for consumers whose go line was below 1.21), with the GODEBUG override as migration -->
