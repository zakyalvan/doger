# Tasks

Specs: `specs/toolchain-support/spec.md`, `specs/provider-panic-handling/spec.md`. Group 2 is RED → GREEN → REFACTOR: the directive bump is the GREEN step for both specs, and its REFACTOR is the lint configuration plus one modernizer per step (design D4). Group 3 holds behavior-preserving refactorings verified by the unchanged suite. No existing test is deleted. Commands run from the repository root unless a task names a module. Read design.md before each group: the checkbox lines carry only the summary.

## 1. Baseline

- [x] 1.1 Record the baseline: save the per-module test inventory (`go test -list '.*' ./...`, attributed by directory) outside the repo; verify `make test`, `make lint` (0 issues), and per-module `go vet ./... && go test -race ./...` all pass, and stop and report if not.

## 2. Go 1.27 floor (requirements "Every module requires Go 1.27 or later", "A panicking provider yields an invocation error")

- [x] 2.1 RED: add `invoke_test.go: TestInvoke_ProviderPanic` (6 rows) and `TestInvoke_ProviderPanicNil_GODEBUGOverride` (design Test Strategy); verify `GOWORK=off go test -race -count=1 -run 'TestInvoke_ProviderPanic' .` fails only in `lazy_nil` and `transient_nil` (error is nil), while the other rows and the override test pass on arrival (recorded: pre-bump behavior already matches them); in workspace mode the nil rows already pass, because `go.work` is `go 1.26.0` since #12 (design Context).
- [x] 2.2 RED: add `toolchain_test.go: TestToolchain_GoDirective` (26 rows) in the root package; verify `go test -count=1 -run TestToolchain_GoDirective .` fails on every row whose actual directive is not `go 1.27` (today `1.18`, `1.24`, `1.25` or `1.26.0`; design Context), and `make test` shows no other failures.
- [x] 2.3 GREEN: run `go mod edit -go=1.27` in all 25 modules and `go work edit -go=1.27` (design D1); verify the 2.1 and 2.2 tests pass both in workspace mode and with `GOWORK=off`, `make test` is green, `go test -race -count=5 -run HealthCheck .` passes, per-module `go vet ./... && go test -race ./...` passes, and `grep -rn '^toolchain' --include=go.mod . go.work` prints nothing.
- [x] 2.4 REFACTOR: run `go mod tidy` in each of the 25 modules; verify the diff is only the `go` lines and added `go.sum` entries (design D1), and `make test` passes.
- [x] 2.5 REFACTOR: in `.golangci.yml` add `run.go: "1.27"` and `linters.settings.modernize.disable: [atomictypes]`, with the design D4 reason as a comment; verify `golangci-lint config verify` passes and `golangci-lint run --enable-only modernize ./...` reports only `rangeint`, `reflecttypefor`, `mapsloop`, `slicescontains` and `waitgroupgo` findings.
- [x] 2.6 REFACTOR *Substitute Algorithm*: run `go fix -slicescontains ./...` after reviewing `-diff` (it rewrites the `contains` body; design D2); verify `make test` passes and lint shows no `slicescontains` finding.
- [x] 2.7 REFACTOR: run `go fix -mapsloop ./...` after reviewing `-diff` (`errors.go`, `scope.go`); verify `make test` passes and lint shows no `mapsloop` finding.
- [x] 2.8 REFACTOR: run `go fix -reflecttypefor ./...` after reviewing `-diff`; verify `make test` passes and lint shows no `reflecttypefor` finding.
- [ ] 2.9 REFACTOR: run `go fix -rangeint ./...` after reviewing `-diff`; verify `make test` passes and lint shows no `rangeint` finding.
- [ ] 2.10 REFACTOR: run `go fix -waitgroupgo ./...` after reviewing `-diff` (3 sites in `scope_test.go`); verify `go test -race -count=3 -run Shutdown .` and `make test` pass and lint shows no `waitgroupgo` finding.
- [ ] 2.11 REFACTOR: run `go fix -any ./...` in every workspace module and hand-edit the 3 `interface{}` comments (`invoke.go:14,21`, `invoke_test.go:69`); verify `grep -rn 'interface{}' --include='*.go' .` prints nothing, every module still passes `go vet ./...`, and `make lint` reports 0 issues.
- [ ] 2.12 Docs: update every Go-version statement listed in design Context and D6 (README, getting-started, about, faq, comparison tables, `index.tsx`, structured-data, `llms.txt`) to Go 1.27; verify `grep -rn '1\.18' README.md docs/docs docs/src docs/static/llms.txt docs/plugins` shows only the `samber/lo` and `samber/mo` lines, and `cd docs && npm ci && npm run lint && npm run build` passes.
- [ ] 2.13 Docs: add a "Go 1.27 and panic(nil)" section to `docs/docs/migrating/migrating-from-samber-do.md` (created by `rename-module-doger`) covering the Go 1.27 floor and the `panic(nil)` change with its `GODEBUG=panicnil=1` / `//go:debug panicnil=1` workaround; verify the page mentions `panicnil` and `cd docs && npm run build` passes.

## 3. Helper bodies and shutdown fan-outs (behavior-preserving refactorings)

- [ ] 3.1 *Substitute Algorithm*: make `reverseSlice` call `slices.Reverse(s)` and `coalesce` return `cmp.Or(v...)` (design D2), leaving call sites and `utils_test.go` unchanged; verify `go test -race -run 'TestUtils|Explain|InvokeStruct' .`, `make test` and `make lint` pass.
- [ ] 3.2 *Substitute Algorithm*: rewrite the fan-out in `(*Scope).shutdownChildrenInParallel` to `wg.Go`, capturing the per-iteration variables (design D3); verify `go test -race -count=3 -run Shutdown .`, `make test` (goleak clean) and `make lint` pass.
- [ ] 3.3 *Substitute Algorithm*: rewrite the fan-out in `(*Scope).shutdownServicesWithoutDependenciesInParallel` to `wg.Go` the same way; verify the same commands as 3.2 pass.

## 4. CI (design D5)

- [ ] 4.1 Confirm the CI Go versions set in #12: the `.github/workflows/test.yml` matrix is `["1.27", "stable"]`, and `lint.yml`, `doc.yml`, `security.yml` and `release.yml` use `go-version-file: go.work` (no edit expected; fix any drift); verify `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/*.yml` reports no errors.
- [ ] 4.2 In `.github/workflows/lint.yml`, give `golangci-lint-action` `version: v2.15.0` (keep `actions/setup-go` on `go-version-file: go.work`); verify actionlint reports no errors on `lint.yml`, and with a local golangci-lint whose `golangci-lint version` reports 2.15.0, `make lint` reports 0 issues.

## 5. Integration checks

- [ ] 5.1 Inventory diff against the 1.1 baseline; verify that the only differences are the four tests added in 2.1 and 2.2, with no deletions.
- [ ] 5.2 Full green run; verify `make test`, `make lint` (0 issues), per-module `go vet ./... && go test -race ./...`, and a clean `go mod tidy` in every module all pass, with no goleak reports.

## Workflow follow-up

- Check that CodeQL handles Go 1.27 on the first `security.yml` run after merge (design D5).
- Bump the pinned golangci-lint `version:` together with future Go releases (design Risks).
- Archive this change after review, then start `add-start-lifecycle-and-kinds`.
- Consider extending `make test` to cover every workspace module (separate tooling change, also noted in `rename-module-doger`).
