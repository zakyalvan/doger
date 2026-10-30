# Spec Delta

## MODIFIED Requirements

### Requirement: Every module requires Go 1.27 or later
Every module of the repository, and its workspace file, SHALL declare the Go version `1.27`, so library code may use Go 1.27 language features such as generic methods.

#### Scenario: Module go directives declare 1.27
```gherkin
Given the repository file "<file>"
When its go directive is read
Then the directive is "go 1.27"
And the file has no toolchain directive

Examples:
  | file                                   |
  | go.work                                |
  | go.mod                                 |
  | config/go.mod                          |
  | http/chi/go.mod                        |
  | http/echo/go.mod                       |
  | http/fiber/go.mod                      |
  | http/gin/go.mod                        |
  | http/std/go.mod                        |
  | examples/dag/go.mod                    |
  | examples/eager-loading/go.mod          |
  | examples/event-driven/go.mod           |
  | examples/healthcheckable/go.mod        |
  | examples/http/chi/go.mod               |
  | examples/http/echo/go.mod              |
  | examples/http/fiber/go.mod             |
  | examples/http/gin/go.mod               |
  | examples/http/std/go.mod               |
  | examples/interface/go.mod              |
  | examples/nested-scope/go.mod           |
  | examples/package-system/go.mod         |
  | examples/service-aliases/go.mod        |
  | examples/service-explanation/go.mod    |
  | examples/shutdownable/go.mod           |
  | examples/simple/go.mod                 |
  | examples/struct/go.mod                 |
  | examples/transient-services/go.mod     |
  | examples/web-application/go.mod        |
  | scheduler/gocron/go.mod                |
```

<!-- cases: 1 default: the declared version is the only default -->
<!-- cases: N/A 2 overrides: a consumer cannot lower another module's go directive; with GOTOOLCHAIN=auto the go command fetches 1.27 automatically, with GOTOOLCHAIN=local an older toolchain refuses the build ("go.mod requires go >= 1.27"), which is go command behavior that cannot be exercised offline without a second toolchain -->
<!-- cases: N/A 3 zero values: no input -->
<!-- cases: 4 boundaries: 1.27 is the floor; the language version follows the go directive, so compiling Go 1.27 features is guaranteed by the directive rows (no separate compiler test) -->
<!-- cases: N/A 5 invalid input: no input -->
<!-- cases: 6 duplicate: the rows cover every go.mod in the workspace (27 modules, config/go.mod and scheduler/gocron/go.mod included) plus go.work; the existing test already holds the scheduler/gocron row, which the main spec lacked; the test fails if the workspace lists a module without a row -->
<!-- cases: N/A 7 lifecycle: static property of the build -->
<!-- cases: N/A 8 concurrency: static property of the build -->
<!-- cases: N/A 9 panics: static property of the build; nothing executes -->
<!-- cases: 10 backward compatibility: deliberately BREAKING for consumers on Go 1.26 or older; requiring this module raises the consumer's own go line to 1.27, which also turns on Go 1.27 GODEBUG defaults such as panicnil=0 (see provider-panic-handling) -->
