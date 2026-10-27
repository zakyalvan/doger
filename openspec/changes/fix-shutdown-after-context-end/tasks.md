# Tasks

Read design.md (D1–D3, Test Strategy) before each group. `make test` is green before starting.

## 1. Services reached after the shutdown context ended are skipped (characterization)

- [ ] 1.1 RED: add `TestScopeShutdown_AfterContextEnded` (6 rows; scenario "A service reached after the context ended is skipped") in `scope_test.go` and `TestRootScope_ShutdownDeadlineDuringShutdown` (synctest; scenario "A deadline that ends during the shutdown skips the services after it") in `root_scope_test.go`, and add the two new assertions of the modified scenario "A child shutdown bounded by its context" to `TestScopeShutdown_WaitBounded` in `root_refresh_test.go`; verify `go test -race -run 'TestScopeShutdown_AfterContextEnded|TestRootScope_ShutdownDeadlineDuringShutdown|TestScopeShutdown_WaitBounded' .` passes on arrival (the behavior exists), then show it fails under each temporary mutation, reverted right after: the lazy wrapper's `ctx.Err()` guards removed (the lazy rows, `DeadlineDuringShutdown`, `WaitBounded`), then the eager wrapper's (the value row); record each failing output.
- [ ] 1.2 GREEN: none needed - the behavior is inherited and unchanged (design D1); verify `make test` is green.
- [ ] 1.3 REFACTOR: none needed - no production code changed; then document it (design D3): the godoc of `Scope.ShutdownWithContext` and `RootScope.ShutdownWithContext` (skipped, reported, dropped; what "proceeds" means after a start wait), and the "Shutdown with a deadline" section of `docs/docs/service-lifecycle/shutdowner.md` with its Defaults & control table and the fixed example; verify `go doc` shows both, `make lint` passes, and `npm run lint` and `npm run build` in `docs/` pass.

## 2. The signal helper's context only ends the wait for a signal

- [ ] 2.1 RED: replace the empty `TestRootScope_ShutdownOnSignalsWithContext` in `root_scope_test.go` with the table (rows cancelled_before, timeout_while_waiting; scenario "The signal helper shuts down when its context ends"), in `testWithTimeout`; verify `go test -race -run 'TestRootScope_ShutdownOnSignalsWithContext' .` fails with Shutdown called 0 times and a report error matching context.Canceled / context.DeadlineExceeded (the proposal's Defect).
- [ ] 2.2 GREEN: in the `ctx.Done()` branch of `RootScope.ShutdownOnSignalsWithContext`, shut down with `context.WithoutCancel(ctx)` (design D2); verify the 2.1 command passes, the container-lifecycle-hooks test for that helper (Closing and Closed once) passes unchanged, and `make test` is green.
- [ ] 2.3 REFACTOR: none needed - one argument changed; then document it (design D3): the godoc of `ShutdownOnSignalsWithContext`, `ExampleRootScope_ShutdownOnSignalsWithContext` made executable with `// Output:`, and the new upgrading page `docs/docs/upgrading/shutdown-context.md` flagging the behavior change; verify `go test -run 'ExampleRootScope_ShutdownOnSignalsWithContext' .`, `make lint`, and `npm run lint` and `npm run build` in `docs/` pass.

## 3. Integration and verification

- [ ] 3.1 Integration and cross-check: `make test`, `make lint`, build and tests of every `go.work` module under `http/*` and `examples/*`; every `#### Scenario:` header in this change's specs has a passing test from the design's Test Strategy table; `openspec validate fix-shutdown-after-context-end --strict` passes.

## Workflow follow-up

- Archive after review. It is independent of `add-scope-export`; if both land in one release, the upgrading pages can be merged then.
