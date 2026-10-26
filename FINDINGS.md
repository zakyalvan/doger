# Handover: fifth verification round (`consolidate-pending-lifecycle-work`)

This brief is for the next session. Nothing from the fifth verification round has been implemented yet: the last code commit is still `c40c60b`. Delete this file in the commit that finishes the round.

## 1. Go skills plugin: cause found and fixed

The earlier blocker (no plugin installed, so `golang-how-to` could not load) is fixed in `dfa9e31`.

**Cause (reproduced).** A fresh container does not have the `claude-plugins-official` marketplace registered while the SessionStart hook runs. The hook only adds marketplaces declared in `extraKnownMarketplaces`, and that one was not declared. Installing `gopls-lsp@claude-plugins-official` failed with "Plugin not found in marketplace", `set -euo pipefail` aborted the hook with exit 1, and `cc-skills-golang@samber` was never attempted.

**Fix.** `.claude/settings.json` declares `claude-plugins-official` (`anthropics/claude-plugins-official`) in `extraKnownMarketplaces`. A simulated fresh state (no marketplaces registered, no plugins installed) now ends with exit 0 and both plugins installed.

**Next steps**
1. Run `claude plugin list` and load `golang-how-to`. If the plugins are missing, read the SessionStart hook output and report it rather than working around it.
2. Do not do Go work until `golang-how-to` loads. If it can't be fixed, ask the user.

## 2. Pending work: close the fifth `/opsx:verify` round

`/opsx:verify consolidate-pending-lifecycle-work` on `c40c60b` found 0 CRITICAL, 6 WARNING and 15 SUGGESTION. At that point:
- 348/348 tasks were done.
- `openspec validate --strict` passed.
- `make test`, `make lint` and every `go.work` module built clean.

The user's decisions:
- Close all 6 warnings.
- For Warning 2, use **Option A**: keep name-only exclusion and document it.
- Apply all 15 suggestions.
- Don't archive.
- Don't open a new PR. The open PR is #8.

**Conventions.** Follow "Part V4" in `design.md` and `notes.md`, and task groups 86–104 in `tasks.md`.
- Add **"Part V5 — Fifth verification"** to `design.md`, with decisions numbered from **V55**.
- Add task groups to `tasks.md` starting at **105**, using RED/GREEN/REFACTOR. A test-only group names the mutation that proves its test can fail.
- In `notes.md` "Part V5", record the RED output, or "passed on arrival" plus the mutation used. Always revert the mutation.
- Add a "Fixes from the fifth verification" line to `proposal.md` if anything is user-visible.
- Commit before each mutation, so a `git checkout --` revert can't wipe uncommitted work.
- Keep `openspec validate consolidate-pending-lifecycle-work --strict` passing.

### Warnings (all must be closed)
1. **A scope-hook rule has no test that would catch a regression.** The `closing` result of `claimCandidates` (`root_start.go:507`, used at `root_refresh.go:203`) has no test that fails if it regresses.
   - Add a row to `TestScopeStartedHook_SkippedWhileClosing` (`scope_hooks_test.go:154`) where the shutdown begins after the Starting hooks ran:
     - A scope Starting hook launches `tenant.Shutdown()` in a goroutine and waits until it is blocked in a `HookScopeClosing` hook.
     - Expect `tenant.Start()` to return nil, "w" to be started 0 times and the Started hook to run 0 times.
   - Add a matching Examples row to `specs/scope-lifecycle-hooks/spec.md` (about lines 66–80).
   - Mutation: `return candidates, false`.
2. **A live-collection read excludes the reader by name only (Option A).** `collectionCandidatesFor` (`invoke.go:362-366`) seeds `seen` with the reader's name.
   - Keep this behavior. Including the shadowed root "app" would fail with `ErrCircularDependency` (`virtual_scope.go:165`), and it matches how InvokeAs skips its invoker by name.
   - Document it:
     - an Examples row or scenario under "Reads follow the reader's injector" in `specs/live-collections/spec.md`;
     - a sentence in the `Collection.All` godoc (`collection.go:25-30`).
   - Add a test in `collection_test.go`:
     - Setup: holder "bus" in root, root EventHandlers "app" and "console", and a child provider "app" that calls `peers.Map(i)`.
     - Expect `{console}` for the child read and `{app, console}` for a nil read.
   - Mutation: drop the reader seed.
3. **Retrying a Starter whose build failed is untested.** `TestRefresh_RetriesFailedStarter` (`root_refresh_test.go:514`) covers only a failed Start.
   - Add rows (root.Refresh and tenant.Start) where the provider fails on its first call.
   - Add a failure column (Start / build) to the scenario "A Starter that failed is tried again" (`specs/container-start/spec.md:90-103`).
4. **One scenario can't be written as worded.** "Scope.Start on the root's own scope" (`specs/container-start/spec.md:145`) says providers receive a `*Scope`; they receive an `Injector`. `TestScopeStart_RootScope` takes the scope from a kind handler's `Component.Scope`. Reword the spec line and `design.md:214` (V19) to match.
5. **A test row registers the wrong kind of service.** The live-collections row "lazy l … registered lazy missing" uses `ProvideNamedValue` at `collection_test.go:237`. Use `ProvideNamed` with a provider returning 42.
6. **A migration note is missing the opt-out.** In `docs/docs/upgrading/start-lifecycle.md:90`, mention `HealthCheckSingleTimeout: -1` as the opt-out (design V52).

### Suggestions (apply all)
**Tests**
1. Make `TestStart_ParallelFailure` (`root_start_test.go:1790`) a table and add a Refresh row ("first failure stops scheduling").
2. Assert that a run starting during a scope shutdown does not *handle* (kind handler) that scope's services. Use `TestScopeShutdown_LaterStartSkips` (`root_refresh_test.go:250`) or `_DescendantSkipped` (:471).
3. Add a row to `TestCollection_ReadWithNilRecordsNoEdge` (`collection_test.go:482`): `c.All(root)` with a plain non-nil injector records no edge.
4. Pin that `WithOrder` is copied on Clone and dropped by an Override without options (design K D5), near `TestInvokeAll_DefaultOrderClone` (`invoke_all_order_test.go:97`).
5. In the `*sql.DB` row of `TestInvokeAll_Membership` (`invoke_all_test.go:122-131`), assert the instance with `assert.Same`, not only the count.
6. Make the nil-entry skip a table over all four scope hook lists (Starting/Started/Closing/Closed) (`scope_hooks_test.go:15-33`).
7. `TestPhase_RaceFree` (`phase_test.go:149-152`) drops observations once its buffer is full. Record a set of phases seen, or count the drops.
8. `TestPhase_LifecycleGiven` (`phase_test.go:92-103`): register the HookContainerStarted/Ready hooks its Given clause names (`specs/container-phase/spec.md:14`), or drop the clause.
9. Add a characterization test for the name-only `aliasResolvesTo` (`invoke.go:463-483`).
   - Case: a child decorator "svc" shadows a parent "svc", and a parent alias "g" targets the parent "svc".
   - Expected: `ErrServiceNotMatch`, not `ErrCircularDependency`.
   - Confirm this first; if the behavior differs, report it rather than asserting it.

**Godoc and docs**

10. The godoc of `ErrStartAfterShutdown` (`errors.go:21`) and `StartError` (`errors.go:42`) mentions only Start. Add Refresh and Scope.Start.
11. Say on the `StartError` godoc why the AsStarter-without-Start error (`unprefixed`, `errors.go:54`, `root_start.go:711`) has no `failed to start service` prefix (design Q2).
12. In `docs/docs/service-invocation/accept-interfaces-return-structs.md:62`, add that aliases resolving to the invoking service are skipped too (V40).

**Planning artifacts**

13. `design.md:677`: "Invalid collection fields (2 rows)" should say 3 rows.
14. `design.md:85` (Q3): `explainService` doesn't return sources. The source is looked up per edge with `dag.edgeSource` (`di_explain.go:270-286`).
15. `proposal.md` "Defaults & Control": merge the "Per-service `HealthCheck*`" row with the "Single-service health checks" row, so the two don't contradict.

### Finish
- Run all of the following:
  - `make test`
  - `make lint`
  - `go vet ./... && go test -race ./...` in every `go.work` module
  - `openspec validate consolidate-pending-lifecycle-work --strict`
  - docs lint and build
- Add an integration-checks task group as in group 104. No stress or benchmark rerun is needed unless production code changed beyond godoc.
- Make small conventional commits, then `git push -u origin feat/consolidate-pending-lifecycle-work`. Update the PR #8 description.
- Run `/opsx:verify consolidate-pending-lifecycle-work` and report the result. Fix any remaining warning, or ask the user if it needs a decision. **Do not archive.**
