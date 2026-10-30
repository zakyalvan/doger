# Tasks

**Phases:** groups 1–22 are P1 (config loads once), groups 23–28 are P2 (reload), and group 29 is integration only. No RED step depends on a later group. Test names, files and scenarios come from design.md (Test Strategy).

**Commands:**
- Root tests: `go test -race -run '<Tests>' .`
- Config-module tests: `cd config && go test -race -run '<Tests>' ./...`
- `make test-config` runs the config module's suite.

**Rules for every group:**
- Each RED states the failure it must show.
- A test that passes on arrival is recorded as such, and is shown to fail once by temporarily breaking the code (not committed).

## 1. Scaffolding: config module, workspace and CI (module-identity, toolchain-support)

- [ ] 1.1 RED: add the `config` rows to `TestModuleIdentity_WorkspaceModules` ("Workspace module paths mirror their directories") and `TestToolchain_GoDirective` ("Module go directives declare 1.27"); verify `go test -race -run 'TestModuleIdentity_WorkspaceModules|TestToolchain_GoDirective' .` fails because `./config` is not in go.work
- [ ] 1.2 GREEN: create `config/go.mod` (`github.com/kartaladev/doger/config`, go 1.27, requiring yaml/v3, go-toml/v2 and doger), a `TestMain` with goleak, and `use ./config` in go.work; verify the two tests pass and `make test` is green
- [ ] 1.3 REFACTOR: none needed, because the rows are data; verify `make test` and `make lint` pass
- [ ] 1.4 Add a `make test-config` target (`cd config && go test -race ./...`) and lint of `config/`; verify both run locally
- [ ] 1.5 Extend `test.yml` and `lint.yml` to `config/`, and add a `GOWORK=off` job plus a step asserting `cd config && go list -deps .` has only stdlib, yaml/v3 and go-toml/v2 ("Dependency footprint"); verify with `act` or a CI dry run that the jobs exist and pass on the empty module
- [ ] 1.6 Extend `release.yml`: pin `config/` to the root tag, drop non-example tests, `go mod tidy` in `config/`, tag `config/vX.Y.Z`; verify by running the script part on a scratch tag in a fork or locally
- [ ] 1.7 Re-enable govulncheck in `security.yml` and write the `.trivyignore` policy for validator's x/ modules; verify the workflow syntax with `actionlint`
- [ ] 1.8 Create `testdata/envmapping.txt` (the golden key→variable table from design decision 5); verify both modules can read it from a scratch test (not committed)

## 2. Key grammar adds index and map-key suffixes (extension-properties)

- [ ] 2.1 RED: write `TestProperties_KeySuffixes` ("Index and map-key suffixes") and keep `TestProperties_InvalidKeys` ("Invalid keys are not looked up"; its original rows pass on arrival, proven by temporarily breaking the pattern); verify `TestProperties_KeySuffixes` fails on the `[i]`/`[key]` rows
- [ ] 2.2 GREEN: extend `propertyKeyPattern` with the `[i]` (0–10000, no leading zero) and `[k]` (no `[`, `]`, whitespace; not starting with a digit or `-`) suffixes; verify both tests pass and `make test` is green
- [ ] 2.3 REFACTOR: Extract Variable for the segment and suffix sub-patterns; verify `make test` and `make lint` pass
- [ ] 2.4 Add `FuzzKeyGrammar` with a committed seed corpus (accepted keys map to an env name and back to the same uniform form); verify `go test -run FuzzKeyGrammar -fuzz FuzzKeyGrammar -fuzztime 30s .` finds nothing
- [ ] 2.5 Update the godoc of `PropertySource` for the grammar; verify `go doc doger.PropertySource`

## 3. Spring environment mapping and prefix normalization (extension-properties)

- [ ] 3.1 RED: rewrite `TestEnvPropertySource_Mapping` (reads the golden file) and `TestEnvPropertySource_Distinct`, and add `TestEnvPropertySource_SharedVariables`, `TestEnvPropertySource_UnaddressableMapKeys`, `TestEnvPropertySource_OldMappingIgnored` and `TestEnvPropertySource_InvalidPrefixPanics`. Rewrite `TestPropertySource_LateServiceIgnored` to `DOGER_REDIS__B_ADDR`, keep `TestPropertySource_LateSameNamePanics` and `TestEnvPropertySource_Unset` (both pass on arrival; prove each can fail). Verify `go test -race -run 'TestEnvPropertySource_|TestPropertySource_Late' .` fails on the new variable names
- [ ] 3.2 GREEN: replace `envName` with the Spring mapping (`.`→`_`, `-` removed, `/`→`__`, `[i]`→`_i`, `[k]`→`_K` for `[a-z0-9]+`), plus prefix normalization and the panic for invalid prefixes; verify the tests pass and `make test` is green
- [ ] 3.3 REFACTOR: Extract Function `normalizeEnvPrefix` and Split Phase (key → segments → variable); verify `make test` and `make lint` pass
- [ ] 3.4 Update `ExampleEnvPropertySource` in `extension_example_test.go` and `docs/docs/extensions/installing.md` to the new names, and add an upgrading-note entry (old → new variables); verify `go test -run ExampleEnvPropertySource .` passes

## 4. Property sources may list their keys (extension-properties)

- [ ] 4.1 RED: write `TestMapSource_Keys`, `TestMapSource_AllKeys`, `TestMapSource_NotCopied`, `TestEnvPropertySource_Keys` (prefix `TKEYS_`, non-parallel), `TestProperties_Keys` and `TestProperties_KeysUnavailable`; verify `go test -race -run 'TestMapSource_|TestEnvPropertySource_Keys|TestProperties_Keys' .` fails to compile only on `PropertyKeys`, `MapSource` and `Properties.Keys`
- [ ] 4.2 GREEN: add the optional `PropertyKeys` interface and `type MapSource map[string]string`. Implement `Keys` on the env source (the reverse mapping, skipping names that don't parse) and on `Properties` (view-relative, uniform-form prefix match, byte-wise sort). Verify the tests pass and `make test` is green
- [ ] 4.3 REFACTOR: Extract Function `keysUnder(sorted []string, prefix string)`, shared by `MapSource` and the env source; verify `make test` and `make lint` pass
- [ ] 4.4 Write godoc for `PropertyKeys` and `MapSource`, and `ExampleMapSource`; verify `go test -run ExampleMapSource .` passes

## 5. Option constructors return errors; ConfigSource installation (container-construction, config-sources, extension-properties)

- [ ] 5.1 RED: write `TestNewWithOpts_ConstructionOutcome`, `TestNew_Unchanged` (passes on arrival; prove it can fail), `TestNewWithOpts_LoadBeforePackages`, `TestNewWithOpts_FailureRunsNoPackage`, `TestMustNewWithOpts`, `TestDefaultRootScope_Usable`, `TestNew_PackagePanicPropagates`, `TestConfigSource_Contract`, `TestConfigSource_IsPropertySource`, `TestConfigSource_OptionChecks`, `TestPropertySource_Selection` (ConfigSource row) and `TestPropertySource_ConfigSourceAtConstruction`; verify they fail to compile only on the new `(root, err)` result, `MustNewWithOpts`, `ConfigSource`, `InjectorOpts.ConfigSource`, `ErrConfigConflict` and `ErrInvalidConfigSource`
- [ ] 5.2 GREEN: change `NewWithOpts` to `(*RootScope, error)`. Add `MustNewWithOpts`, `ConfigSource`, `ConfigLogger`, `InjectorOpts.ConfigSource`, the option checks (typed-nil first), the committed-view property source and `Load(context.Background())` before packages. Give `DefaultRootScope` an internal constructor. Then rewrite the ~193 `NewWithOpts` call sites with gopatch (`MustNewWithOpts` in tests, `root, err :=` elsewhere). Verify the tests pass and `make test` is green
- [ ] 5.3 REFACTOR: Extract Function `installConfig(root, opts, ctx) error` and Extract Function `checkConfigOptions(opts) error`; verify `make test` and `make lint` pass
- [ ] 5.4 Migrate `NewWithOpts` calls in `scheduler/gocron`, `http/*`, `examples/*`, `docs/` and README; verify `go build ./... && go test ./...` in each module and that the docs build
- [ ] 5.5 Write godoc for `NewWithOpts`, `MustNewWithOpts`, `ConfigSource` (one source per container, concurrency safety, the never-values duty) and `ConfigLogger`; add `ExampleNewWithOpts_config` (a fake source) and an upgrading-note entry; verify `go test -run ExampleNewWithOpts_config .` passes

## 6. Load context, timeout and logging (container-construction, config-sources)

- [ ] 6.1 RED: write `TestNewWithOptsContext_LoadContext`, `TestNewWithOptsContext_Cancel`, `TestConfigSource_LoadDeadline`, `TestConfigSource_FakeClockContext`, `TestConfigSource_LoadTimeout`, `TestConfigSource_LoadIgnoresDeadline`, `TestConfigSource_SetLogfOrder` and `TestConfigSource_LogfReachesSource` (non-parallel, slog default swapped); verify they fail to compile only on `NewWithOptsContext`, `InjectorOpts.ConfigTimeout` and `DefaultConfigTimeout`
- [ ] 6.2 GREEN: add `NewWithOptsContext`, plus the fake-clock-safe load context (deadline from `Clock.Now()`, `Done` via `Clock.AfterFunc`, an `Err` that never blocks, deferred cancel). Caller deadlines win over `ConfigTimeout`. Call `SetLogf` only when `Logf` was set. Verify the tests pass with `-race` and goleak, and `make test` is green
- [ ] 6.3 REFACTOR: Move Function: put the load-context helper next to `healthCheckContextWithin` in `di_lifecycle.go`; verify `make test` and `make lint` pass
- [ ] 6.4 Write godoc for `NewWithOptsContext` and `ConfigTimeout`, and add `ExampleNewWithOptsContext`; verify `go test -run ExampleNewWithOptsContext .` passes

## 7. Load failures and secrecy (config-sources)

- [ ] 7.1 RED: write `TestConfigSource_LoadFailure` and `TestConfigSource_NoValuesInErrorsOrLogs` (with a high-entropy marker, checking `Error()`, `%#v`, `errors.As` targets and log lines); verify they fail on the pinned error texts and on `ErrConfigLoad` being undefined
- [ ] 7.2 GREEN: add `ErrConfigLoad`, wrap the source's error with a text naming its type only, and recover `Load` panics naming the panic value's type; verify the tests pass and `make test` is green
- [ ] 7.3 REFACTOR: Extract type `configLoadError` (`Unwrap`, type-only `Error`); verify `make test` and `make lint` pass

## 8. Clones and config (container-construction, config-sources)

- [ ] 8.1 RED: write `TestCloneWithOpts_Outcome`, `TestClone_SharesConfig` and `TestConfigSource_CloneShares`; verify they fail to compile only on the `(clone, err)` result of `CloneWithOpts`
- [ ] 8.2 GREEN: `CloneWithOpts` returns `(*RootScope, error)`. It shares the config state for `Clone()` and for the parent's own source, loads a different source, and uses none for nil or empty opts. Rewrite the ~44 `CloneWithOpts` call sites. Verify the tests pass and `make test` is green
- [ ] 8.3 REFACTOR: Extract type `configState` (source, committed view, reload entries, membership) owned by the root and shared by clones; verify `make test` and `make lint` pass
- [ ] 8.4 Migrate `CloneWithOpts` in the other workspace modules, `docs/` and README; update its godoc and add an upgrading-note entry; verify every module builds and tests pass

## 9. Binder: scalars and conversions (config-binding)

- [ ] 9.1 RED: write `TestBind_Conversions` and `TestBind_ConversionErrors` against `ResolveConfig` over a `MapSource`; verify they fail to compile only on `ResolveConfig`, `ErrInvalidConfigValue` and `ConfigBindError`
- [ ] 9.2 GREEN: write a minimal `ResolveConfig` and a reflection binder for top-level scalar fields. Convert in order: exact types and `TextUnmarshaler` first, then strconv base 10 with `Bits()`. Return `*ConfigBindError{Key, Type}`, never wrapping parser errors. Verify the tests pass and `make test` is green
- [ ] 9.3 REFACTOR: Replace Conditional with Polymorphism: a converter table keyed by `reflect.Type`, then by `Kind`; verify `make test` and `make lint` pass
- [ ] 9.4 Add `FuzzBind_Scalars` (random strings into every supported kind, a marker value, no panic, no marker in errors); verify `-fuzztime 30s` finds nothing

## 10. Binder: names, tags and the property-source decision (config-binding)

- [ ] 10.1 RED: write `TestBind_CanonicalKeys`, `TestBind_TagOverride`, `TestBind_ConfigTagKey`, `TestBind_InvalidTag`, `TestBind_NameConflict`, `TestResolveConfig_EnvOnlyKey` (spike 4; env-only root) and `TestBind_DecidesPropertySource`; verify they fail to compile only on `InjectorOpts.ConfigTagKey`, `DefaultConfigTagKey` and `ErrConfigNameConflict`, then on the canonical-name assertions
- [ ] 10.2 GREEN: plan fields per type (`sync.Map` cache), with canonical kebab splitting at case and acronym boundaries, the tag key, `-`, invalid tags as `ErrInvalidConfigTarget` and uniform-name conflicts; binding decides the property source. Verify the tests pass and `make test` is green
- [ ] 10.3 REFACTOR: Split Phase into plan and bind, and Extract Function `canonicalName`; verify `make test` and `make lint` pass

## 11. Binder: presence, nesting, embedding, defaults and recursion (config-binding)

- [ ] 11.1 RED: write `TestBind_Presence`, `TestBind_NestedSiblingDefaults`, `TestBind_Embedded`, `TestBind_DefaultsNotMutated` and `TestBind_RecursiveType`; verify they fail on the assertions (nested fields unbound, defaults mutated, recursion not rejected)
- [ ] 11.2 GREEN: bind only present keys onto a deep copy that tracks visited pointers; recurse into nested and embedded structs (`CanSet` checked); reject recursive types before any lookup. Verify the tests pass and `make test` is green
- [ ] 11.3 REFACTOR: Extract Function `deepCopy(reflect.Value, visited)`; verify `make test` and `make lint` pass

## 12. Binder: lists, maps, pointers and uniform form (config-binding)

- [ ] 12.1 RED: write `TestBind_Lists`, `TestBind_ListOfStructs`, `TestBind_CommaSplit`, `TestBind_MapMerge`, `TestBind_MapOfStructs`, `TestBind_MapKeyKind`, `TestBind_Pointers`, `TestBind_WithoutPropertyKeys`, `TestBind_UniformKeysFromEnv` and `TestMapSource_Bind`; verify they fail on the assertions
- [ ] 12.2 GREEN: enumerate with `PropertyKeys` in uniform form. Check index density and the 10000 cap before allocating; fall back to a comma split. Maps take string keys only and merge struct values field by field, keeping `[k]` verbatim. Allocate pointers only when keys exist. A Lookup-only source with list, map or pointer fields gives `ErrInvalidConfigTarget`. Verify the tests pass and `make test` is green
- [ ] 12.3 REFACTOR: Replace Conditional with Polymorphism: one binder function per structural kind; verify `make test` and `make lint` pass

## 13. Binder: panics and the ResolveConfig API (config-binding)

- [ ] 13.1 RED: write `TestBind_PanicRecovered`, `TestResolveConfig_InvalidPrefix`, `TestResolveConfig_TypedNilInjector`, `TestResolveConfig_ConsumerDefaults`, `TestResolveConfig_Failure`, `TestResolveConfig_InProviderAndClone` and `TestResolveConfig_AfterShutdown`; verify they fail on the assertions (panic escapes, prefix not checked, closed container not rejected)
- [ ] 13.2 GREEN: recover panics from `UnmarshalText` and source calls as `ErrInvalidConfigValue` (type only). Validate the prefix and the typed-nil injector. Return the zero value on failure. Reject closed containers with `ErrStartAfterShutdown`. Verify the tests pass and `make test` is green
- [ ] 13.3 REFACTOR: Extract Function `resolveContainer(i Injector) (*RootScope, error)`; verify `make test` and `make lint` pass
- [ ] 13.4 Write godoc for `ResolveConfig` and the binder rules, and add `ExampleResolveConfig` (MapSource, unique prefix); verify `go test -run ExampleResolveConfig .` passes

## 14. BindConfig, BindConfigNamed and ConfigValue (config-binding)

- [ ] 14.1 RED: write `TestBindConfig_Registers`, `TestBindConfig_DefaultsOption`, `TestBindConfig_FailureRegistersNothing`, `TestBindConfig_NilInjector` (non-parallel, `DefaultRootScope` swapped and restored with `t.Cleanup`), `TestBindConfig_ChildScope`, `TestBindConfigNamed_TwoPrefixes`, `TestBindConfigNamed_EmptyName`, `TestBindConfig_Duplicate`, `TestBindConfig_ReplacesDefaultShadowsParent`, `TestBindConfig_Phases`, `TestBindConfig_InvalidType`, `TestConfigValue_Get`, `TestConfigValue_OnChangeStatic` and `TestBindConfig_Concurrent`; verify they fail to compile only on `BindConfig`, `BindConfigNamed`, `BindOption`, `ConfigDefaults`, `ConfigValue`, `ConfigValueName`, `StaticConfig` and `ErrConfigAlreadyBound`
- [ ] 14.2 GREEN: under the reload lock, bind against the committed view, then register `T` and `ConfigValue[T]` atomically through a non-panicking provide (`AsDefault` replaced, parent shadowed, `ErrConfigAlreadyBound` otherwise). Add the reload entry. The handle is a cell pointer that is nil-safe. Verify the tests pass with `-race` and `make test` is green
- [ ] 14.3 REFACTOR: Extract Function `bindAndValidate[T]`, shared by `BindConfig` and `ResolveConfig`; verify `make test` and `make lint` pass
- [ ] 14.4 Write godoc for the binding API (copies share the handle, `Get` results are read-only), and add `ExampleBindConfig` and `ExampleStaticConfig`; verify `go test -run 'ExampleBindConfig|ExampleStaticConfig' .` passes

## 15. BindProperties for extensions (config-binding)

- [ ] 15.1 RED: write `TestBindProperties_Extension`, `TestBindProperties_TargetUnchangedOnError` and `TestBindProperties_InvalidTarget` in `extension_test.go`; verify they fail to compile only on `BindProperties`
- [ ] 15.2 GREEN: `BindProperties(p, target)` binds through the view onto a copy and writes `*target` only on success; verify the tests pass and `make test` is green
- [ ] 15.3 REFACTOR: none needed: it delegates to the binder's bind-onto-copy path; verify `make test` and `make lint` pass
- [ ] 15.4 Write godoc and `ExampleBindProperties`, and update the extensions authoring guide in `docs/`; verify `go test -run ExampleBindProperties .` passes

## 16. Validation and ValidateConfig (config-validation)

- [ ] 16.1 RED: write `TestValidate_DefaultTags`, `TestValidate_RangeBoundaries`, `TestValidate_SkippedField`, `TestValidate_Method`, `TestValidate_MethodErrorText`, `TestValidate_TopLevelOnly`, `TestValidate_Replacement`, `TestValidate_CloneValidator`, `TestValidate_MergedValue`, `TestValidate_ErrorShape`, `TestValidate_NoValuesLeaked`, `TestValidateConfig_InvalidTarget`, `TestValidate_PanicRecovered`, `TestValidate_Concurrent` and `TestValidateConfig_Extension`; verify they fail to compile only on `ErrInvalidConfig`, `ConfigValidator`, `InjectorOpts.ConfigValidator`, `ConfigValidationError`, `ConfigFieldError` and `ValidateConfig`
- [ ] 16.2 GREEN: add `validator/v10` usage behind `sync.OnceValue`, with keys from the field-path map and the pinned text. Run `Validate()` (top level only) after tags pass. `InjectorOpts.ConfigValidator` replaces go-playground. Recover panics. Verify the tests pass with `-race` and `make test` is green
- [ ] 16.3 REFACTOR: Extract type `configValidation` (chooser plus formatter); verify `make test` and `make lint` pass
- [ ] 16.4 Write godoc for validation (the author's duty to keep values out of `Validate` errors) and a docs page section; add an upgrading-note entry for the new root dependency; verify docs build

## 17. config.Files: discovery, formats and profiles (config-files)

- [ ] 17.1 RED: write `TestFiles_Discovery`, `TestFiles_DiscoveryMergesDirs`, `TestFiles_DiscoveryOrder`, `TestFiles_AmbiguousFormat`, `TestFiles_Profile`, `TestFiles_ProfileWithoutBase`, `TestFiles_ProfileNames`, `TestFiles_Symlinks`, `TestFiles_ExplicitPaths` and `TestFiles_Codecs` (`t.Chdir`/`t.Setenv` tests non-parallel); verify `make test-config` fails to compile only on `config.Files` and its options
- [ ] 17.2 GREEN: implement `Files`, `Dir`, `File`, `Name`, `Format`/`FileFormat`, `Codec`/`Decoder`, `Profile` and `ProfileEnv`, with profile-name validation, `os.Root` per directory, `ErrAmbiguousFormat`, `ErrProfileNotFound` and missing paths matching `fs.ErrNotExist`; verify the tests pass
- [ ] 17.3 REFACTOR: Extract type `decoder` per format, and Extract Function `discover(dirs, name, profile)`; verify `make test-config` and lint pass

## 18. config.Files: keys, rendering, shapes and load errors (config-files)

- [ ] 18.1 RED: write `TestFiles_FlattenAndNormalize`, `TestFiles_InvalidShapes`, `TestFiles_ScalarRendering`, `TestFiles_MaxFileSize` and `TestFiles_LoadErrorHasNoValue` (marker); verify they fail on the assertions or compile only on `ParseError`, `ErrFileTooLarge` and `MaxFileSize`
- [ ] 18.2 GREEN: flatten to canonical keys (`/` instances, `[...]` verbatim, kebab), render scalars per format, decode YAML into `any` first, reject duplicate keys, multiple documents and non-mapping tops, cap file size with `io.LimitReader`, and build `*ParseError` with no parser text; verify the tests pass
- [ ] 18.3 REFACTOR: Extract Function `flatten(prefix, value)` and Extract type `ParseError` constructors; verify `make test-config` and lint pass
- [ ] 18.4 Add `FuzzFiles_YAML`, `FuzzFiles_TOML` and `FuzzFiles_JSON` with committed seeds (billion-laughs, self-referencing anchor, marker values); verify `-fuzztime 30s` each finds nothing

## 19. config.Files: layers and env (config-files)

- [ ] 19.1 RED: write `TestFiles_LayerPrecedence`, `TestFiles_UniformFormPrecedence`, `TestFiles_ListFromOneLayer`, `TestFiles_EnvPrefix`, `TestFiles_EnvMapping` (golden file), `TestFiles_EnvReverseMapping`, `TestFiles_EnvMapper`, `TestFiles_Keys` and `TestFiles_ProfileVariableNotAKey`; verify they fail to compile only on `EnvPrefix` and `EnvMapper`, then on the precedence assertions
- [ ] 19.2 GREEN: merge layers in uniform form into an immutable snapshot (`atomic.Pointer`). Add the env layer read once at `Load`, with the prefix rules and the Spring mapping matching the golden file. A higher scalar masks lower indices. `EnvMapper` contributes nothing to `Keys`. Exclude the profile variable. Verify the tests pass
- [ ] 19.3 REFACTOR: Extract type `snapshot` (sorted keys, map, `keysUnder`); verify `make test-config` and lint pass

## 20. config.Files: .env (config-files)

- [ ] 20.1 RED: write `TestFiles_DotEnv` (spike 5), `TestFiles_DotEnvSyntax`, `TestFiles_DotEnvLaterLineWins`, `TestFiles_DotEnvExport` and `TestFiles_DotEnvExportOnce` (`t.Setenv` cleanup registered before exporting); verify they fail to compile only on `DotEnv` and `DotEnvExport`
- [ ] 20.2 GREEN: write the `.env` parser (export, quotes, comments, escapes, no expansion, CRLF and BOM, invalid lines as `*ParseError`). It is a layer that never calls `os.Setenv`, except `DotEnvExport`, which sets unset variables once at the first `Load` and logs the names it skipped. Verify the tests pass
- [ ] 20.3 REFACTOR: Extract Function `parseDotEnv(r io.Reader) (map[string]string, error)`; verify `make test-config` and lint pass
- [ ] 20.4 Add `FuzzFiles_DotEnv` with committed seeds; verify `-fuzztime 30s` finds nothing

## 21. config.Files: added layers, options, context, concurrency and logging (config-files)

- [ ] 21.1 RED: write `TestFiles_AddedLayer`, `TestFiles_AddedLayerLoadError`, `TestFiles_InvalidOptions`, `TestFiles_LoadContextCancelled`, `TestFiles_LoadsSerialized`, `TestFiles_ConcurrentReadsDuringLoad` (spike 3, `-race`) and `TestFiles_LogSink` (non-parallel, slog default swapped); verify they fail to compile only on `Layer`, `LayerSource`, `LayerOrder`, `LayerName`, `Logger` and `ErrInvalidOption`, then on the assertions
- [ ] 21.2 GREEN: added layers above env (`LayerOrder` reorders; omitted layers are not read). Options are checked at `Load`. Cancellation keeps the previous snapshot. Loads are serialized by a mutex. Reads see one snapshot. `SetLogf` and `Logger` are used, else `slog.Default()` per call. Verify the tests pass with `-race`
- [ ] 21.3 REFACTOR: Introduce Parameter Object `loadPlan` (layers in order); verify `make test-config` and lint pass

## 22. Structs bind end to end through config.Files (config-files, config-binding)

- [ ] 22.1 RED: write `TestFiles_BindEnvOnlyKey` (spike 4 with files) and `TestFiles_BindFileSpellings` in `config/integration_test.go`; verify they fail on the assertions, or pass on arrival (record it and prove each can fail by breaking the uniform-form merge)
- [ ] 22.2 GREEN: fix whatever the two modules disagree on; verify the tests pass
- [ ] 22.3 REFACTOR: none needed: integration only; verify `make test-config` and lint pass
- [ ] 22.4 Add `ExampleFiles` and `ExampleFiles_overrides` (testdata directory, unique `EnvPrefix`), and write the config guide in `docs/` (zero config, overrides, binding, validation, secrecy); verify `cd config && go test -run Example ./...` passes and docs build

## 23. Reload: watcher lifecycle (config-reload)

- [ ] 23.1 RED: write `TestConfigWatch_Lifecycle` (spike 1; synctest, goleak) and `TestConfigWatch_Ends` with a fake `ConfigWatcher`; verify they fail to compile only on `ConfigWatcher`, `ConfigView` and `ErrConfigWatch`
- [ ] 23.2 GREEN: add internal watcher wiring in the root (not a service), started at the first member's `Start` with `context.WithCancel(context.WithoutCancel(startCtx))` and stopped at the last member's `Shutdown`. Track membership as a set. `Watch` returning nil early means "not watching". Log errors and panics by type as `ErrConfigWatch`. Verify the tests pass with goleak and `make test` is green
- [ ] 23.3 REFACTOR: Extract type `watcherRun` (start, stop, wait bounded by ctx); verify `make test` and `make lint` pass

## 24. Reload: the root commits, all or nothing (config-reload)

- [ ] 24.1 RED: write `TestConfigWatch_CommitDecision`, `TestConfigReload_ReadPathsAgree`, `TestConfigWatch_StagedInvisibleUntilCommit`, `TestConfigReload_AllOrNothing`, `TestConfigReload_NoMixedValues` (`-race`) and `TestConfigReload_LiveVersusSnapshot`; verify they fail on the assertions (staged values visible, sections mixed)
- [ ] 24.2 GREEN: `changed` takes the reload lock, rebinds every entry from `next`, stores the committed view, publishes all handles, then returns true; any failure returns false. Verify the tests pass with `-race` and `make test` is green
- [ ] 24.3 REFACTOR: Extract Function `rebindAll(next ConfigView) ([]published, error)`; verify `make test` and `make lint` pass

## 25. Reload: failures and hooks (config-reload)

- [ ] 25.1 RED: write `TestConfigReload_FailedWarnsAndHooks` and `TestConfigReload_HookPanic`; verify they fail to compile only on `HookConfigReloadFailed` and `AddConfigReloadFailedHook`
- [ ] 25.2 GREEN: a staging error or a failed rebind warns with keys only (to `Logf`, else `slog.Default().Warn`) and enqueues the hooks; hook panics are recovered and logged by type. Verify the tests pass and `make test` is green
- [ ] 25.3 REFACTOR: none needed: one warning path; verify `make test` and `make lint` pass
- [ ] 25.4 Write godoc for `HookConfigReloadFailed` and `AddConfigReloadFailedHook`; verify `go doc doger.InjectorOpts`

## 26. Reload: dispatcher, subscribers and bounded Shutdown (config-reload)

- [ ] 26.1 RED: write `TestConfigValueOnChange_Notified`, `TestConfigReload_PublishBeforeNotify`, `TestConfigValue_StaticAndZeroNeverNotify`, `TestConfigValueOnChange_Stop`, `TestConfigValueOnChange_NilFunc`, `TestConfigReload_DispatcherCoalesces`, `TestConfigValueOnChange_SubscriberPanic`, `TestConfigReload_ReentrantCallbacks` (spike 2), `TestConfigReload_ShutdownBoundedByContext` (goleak) and `TestConfigReload_ConcurrentReads` (spike 3, `-race`); verify they fail on the assertions (no notification, deadlock caught by synctest, unbounded Shutdown)
- [ ] 26.2 GREEN: one dispatcher goroutine per config state, coalescing to the latest value per section. Change detection compares the raw key/value set. An `atomic.Bool` stop is checked before each call. Panics are recovered. `Shutdown` waits only until its ctx ends. Verify the tests pass with `-race` and goleak, and `make test` is green
- [ ] 26.3 REFACTOR: Extract type `dispatcher` and Extract type `subscribers[T]`; verify `make test` and `make lint` pass
- [ ] 26.4 Write godoc for `ConfigValue.OnChange` (keep subscribers fast; they may call `Shutdown`), and add `ExampleConfigValue_OnChange`; verify `go test -run ExampleConfigValue_OnChange .` passes

## 27. Reload: clones, membership and bind vs reload (config-reload)

- [ ] 27.1 RED: write `TestConfigReload_CloneShares`, `TestConfigReload_ShutdownMemberSectionsLeave`, `TestConfigReload_HookOwnership` and `TestBindConfig_DuringReload` (`-race`, channel handshake); verify they fail on the assertions
- [ ] 27.2 GREEN: reload entries are owned by the scope that bound them and are removed at its `Shutdown`; hooks and `Logf` fire per owner; `BindConfig` waits on the reload lock. Verify the tests pass with `-race` and `make test` is green
- [ ] 27.3 REFACTOR: Extract Function `entriesOwnedBy(scope)`; verify `make test` and `make lint` pass

## 28. config.Files watching: polling, debounce, symlinks and hostile files (config-reload, config-files)

- [ ] 28.1 RED: write `TestFiles_PollInterval`, `TestFiles_PollIntervalNegative`, `TestFiles_PollDebouncesPartialWrite`, `TestFiles_PollReloads`, `TestFiles_PollSymlinkSwap` (skipped on Windows), `TestFiles_HostileYAML`, `TestFiles_LoadWhileWatching` and `TestSource_Contract` (compile checks for `doger.ConfigSource`, `ConfigLogger` and `ConfigWatcher`); verify `make test-config` fails to compile only on `(*Source).Watch`, `DisableWatch`, `PollInterval` and `DefaultPollInterval`
- [ ] 28.2 GREEN: `(*Source).Watch` polls a sha256 of the files `Load` read plus `./.env`, debounced over two equal ticks. A missing file, or one that became empty, gives `changed(ctx, nil, err)`. The env layer is kept from `Load`, and added layers are re-read on staging. `Load` after `Watch` returns `ErrInvalidOption`. Verify the tests pass with synctest, `-race` and goleak
- [ ] 28.3 REFACTOR: Extract Function `fingerprint(paths) ([32]byte, error)`; verify `make test-config` and lint pass
- [ ] 28.4 Extend the config guide in `docs/` with reload (debounce, ConfigMap swaps, new files not noticed, `DisableWatch`); verify docs build

## 29. Integration

- [ ] 29.1 Run `make test`, `make lint`, `make test-config`, the `GOWORK=off` job, and `go build ./... && go test ./...` in `scheduler/gocron`, every `http/*` and `examples/*` module; verify all pass
- [ ] 29.2 Cross-check by script that every `#### Scenario:` maps to exactly one row of design.md's Test Strategy and to a passing test, and run `openspec validate add-config --strict`; verify both are clean
- [ ] 29.3 Mark the ROADMAP item "Configuration validation at start" done, and review the upgrading note for completeness (NewWithOpts, CloneWithOpts, env names, prefix normalization, the validator dependency); verify the ROADMAP and docs diff

## Workflow follow-up

- Run `/opsx:verify add-config`, then `/opsx:archive add-config` once merged.
- Follow-up change: add the missing `scheduler/gocron` rows to the `module-identity` and `toolchain-support` main specs (a pre-existing gap).
- Next change: `unify-constructor` (Change 2 in the doc).
