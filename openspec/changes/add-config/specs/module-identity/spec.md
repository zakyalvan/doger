# Spec Delta

## MODIFIED Requirements

### Requirement: Every workspace module path mirrors its directory under the doger path
Every module listed in the workspace SHALL have the module path `github.com/kartaladev/doger` followed by its directory relative to the repository root, with no major-version suffix anywhere.

#### Scenario: Workspace module paths mirror their directories
```gherkin
Given the workspace file of the repository
When the module in workspace directory "<dir>" is loaded
Then its module path is "<module_path>"
And its module path does not end with "/v2"

Examples:
  | dir                           | module_path                                               |
  | .                             | github.com/kartaladev/doger                               |
  | config                        | github.com/kartaladev/doger/config                        |
  | examples/dag                  | github.com/kartaladev/doger/examples/dag                  |
  | examples/eager-loading        | github.com/kartaladev/doger/examples/eager-loading        |
  | examples/event-driven         | github.com/kartaladev/doger/examples/event-driven         |
  | examples/healthcheckable      | github.com/kartaladev/doger/examples/healthcheckable      |
  | examples/http/chi             | github.com/kartaladev/doger/examples/http/chi             |
  | examples/http/echo            | github.com/kartaladev/doger/examples/http/echo            |
  | examples/http/fiber           | github.com/kartaladev/doger/examples/http/fiber           |
  | examples/http/gin             | github.com/kartaladev/doger/examples/http/gin             |
  | examples/http/std             | github.com/kartaladev/doger/examples/http/std             |
  | examples/interface            | github.com/kartaladev/doger/examples/interface            |
  | examples/nested-scope         | github.com/kartaladev/doger/examples/nested-scope         |
  | examples/package-system       | github.com/kartaladev/doger/examples/package-system       |
  | examples/service-aliases      | github.com/kartaladev/doger/examples/service-aliases      |
  | examples/service-explanation  | github.com/kartaladev/doger/examples/service-explanation  |
  | examples/shutdownable         | github.com/kartaladev/doger/examples/shutdownable         |
  | examples/simple               | github.com/kartaladev/doger/examples/simple               |
  | examples/struct               | github.com/kartaladev/doger/examples/struct               |
  | examples/transient-services   | github.com/kartaladev/doger/examples/transient-services   |
  | examples/web-application      | github.com/kartaladev/doger/examples/web-application      |
  | http/chi                      | github.com/kartaladev/doger/http/chi                      |
  | http/echo                     | github.com/kartaladev/doger/http/echo                     |
  | http/fiber                    | github.com/kartaladev/doger/http/fiber                    |
  | http/gin                      | github.com/kartaladev/doger/http/gin                      |
  | http/std                      | github.com/kartaladev/doger/http/std                      |
```

<!-- cases: N/A 2 overrides: no option -->
<!-- cases: N/A 3 zero values: an empty workspace is not a supported layout -->
<!-- cases: 4 boundaries: covered by the 26 rows, which are every workspace entry; the test fails if the workspace lists a directory that has no row -->
<!-- cases: N/A 5 invalid input: no input -->
<!-- cases: N/A 6 duplicate: Go tooling rejects duplicate module paths in a workspace -->
<!-- cases: N/A 7 lifecycle: static property of the build -->
<!-- cases: N/A 8 concurrency: static property of the build -->
<!-- cases: N/A 9 panics: nothing executes -->
<!-- cases: N/A 10 backward compatibility: example modules are not consumed; the examples/http/gin outlier path is intentionally normalized -->
