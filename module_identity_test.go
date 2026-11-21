package doger

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const modulePath = "github.com/kartaladev/doger"

// oldLibPrefix is the message prefix inherited from samber/do, split so a
// source search for the old prefix finds no Go file.
const oldLibPrefix = "DI" + ": "

// readModulePath returns the module path declared by the go.mod file in dir.
func readModulePath(t *testing.T, dir string) string {
	t.Helper()

	f, err := os.Open(filepath.Clean(filepath.Join(dir, "go.mod")))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if after, ok := strings.CutPrefix(line, "module "); ok {
			return strings.Trim(strings.TrimSpace(after), `"`)
		}
	}
	require.NoError(t, scanner.Err())
	require.FailNowf(t, "no module directive", "%s/go.mod", dir)

	return ""
}

func TestModuleIdentity_RootModulePackages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dir        string
		importPath string
		pkgName    string
	}{
		{name: "root", dir: ".", importPath: modulePath, pkgName: "doger"},
		{name: "http", dir: "http", importPath: modulePath + "/http", pkgName: "dogerhttp"},
		{name: "stacktrace", dir: "stacktrace", importPath: modulePath + "/stacktrace", pkgName: "stacktrace"},
	}

	root := readModulePath(t, ".")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			importPath := root
			if tt.dir != "." {
				importPath += "/" + tt.dir
			}
			is.Equal(tt.importPath, importPath)
			if tt.dir == "." {
				is.Equal(tt.importPath, reflect.TypeFor[RootScope]().PkgPath())
			}

			pkg, err := build.ImportDir(tt.dir, 0)
			require.NoError(t, err)
			is.Equal(tt.pkgName, pkg.Name)
		})
	}
}

func TestModuleIdentity_HTTPAdapterModules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dir        string
		modulePath string
		pkgName    string
	}{
		{name: "chi", dir: "http/chi", modulePath: modulePath + "/http/chi", pkgName: "dogerchi"},
		{name: "echo", dir: "http/echo", modulePath: modulePath + "/http/echo", pkgName: "dogerecho"},
		{name: "fiber", dir: "http/fiber", modulePath: modulePath + "/http/fiber", pkgName: "dogerfiber"},
		{name: "gin", dir: "http/gin", modulePath: modulePath + "/http/gin", pkgName: "dogergin"},
		{name: "std", dir: "http/std", modulePath: modulePath + "/http/std", pkgName: "dogerhttpstd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			is.Equal(tt.modulePath, readModulePath(t, tt.dir))

			pkg, err := build.ImportDir(tt.dir, 0)
			require.NoError(t, err)
			is.Equal(tt.pkgName, pkg.Name)
		})
	}
}

// readWorkspaceDirs returns the directories listed by the use directives of
// the go.work file in dir, relative to dir and without a leading "./".
func readWorkspaceDirs(t *testing.T, dir string) []string {
	t.Helper()

	f, err := os.Open(filepath.Clean(filepath.Join(dir, "go.work")))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	var dirs []string
	inUseBlock := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "use (":
			inUseBlock = true
		case inUseBlock && line == ")":
			inUseBlock = false
		case inUseBlock && line != "":
			dirs = append(dirs, filepath.ToSlash(filepath.Clean(line)))
		case strings.HasPrefix(line, "use "):
			dirs = append(dirs, filepath.ToSlash(filepath.Clean(strings.TrimSpace(strings.TrimPrefix(line, "use ")))))
		}
	}
	require.NoError(t, scanner.Err())

	return dirs
}

func TestModuleIdentity_WorkspaceModules(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		".":                            modulePath,
		"clock/clockwork":              modulePath + "/clock/clockwork",
		"examples/dag":                 modulePath + "/examples/dag",
		"examples/eager-loading":       modulePath + "/examples/eager-loading",
		"examples/event-driven":        modulePath + "/examples/event-driven",
		"examples/healthcheckable":     modulePath + "/examples/healthcheckable",
		"examples/http/chi":            modulePath + "/examples/http/chi",
		"examples/http/echo":           modulePath + "/examples/http/echo",
		"examples/http/fiber":          modulePath + "/examples/http/fiber",
		"examples/http/gin":            modulePath + "/examples/http/gin",
		"examples/http/std":            modulePath + "/examples/http/std",
		"examples/interface":           modulePath + "/examples/interface",
		"examples/nested-scope":        modulePath + "/examples/nested-scope",
		"examples/package-system":      modulePath + "/examples/package-system",
		"examples/service-aliases":     modulePath + "/examples/service-aliases",
		"examples/service-explanation": modulePath + "/examples/service-explanation",
		"examples/shutdownable":        modulePath + "/examples/shutdownable",
		"examples/simple":              modulePath + "/examples/simple",
		"examples/struct":              modulePath + "/examples/struct",
		"examples/transient-services":  modulePath + "/examples/transient-services",
		"examples/web-application":     modulePath + "/examples/web-application",
		"http/chi":                     modulePath + "/http/chi",
		"http/echo":                    modulePath + "/http/echo",
		"http/fiber":                   modulePath + "/http/fiber",
		"http/gin":                     modulePath + "/http/gin",
		"http/std":                     modulePath + "/http/std",
		"scheduler/gocron":             modulePath + "/scheduler/gocron",
	}

	dirs := readWorkspaceDirs(t, ".")
	wantDirs := make([]string, 0, len(want))
	for dir := range want {
		wantDirs = append(wantDirs, dir)
	}
	assert.ElementsMatch(t, wantDirs, dirs, "go.work use entries")

	for _, dir := range dirs {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			expected, ok := want[dir]
			require.Truef(t, ok, "workspace directory %q has no expected module path", dir)

			got := readModulePath(t, dir)
			is.Equal(expected, got)
			is.False(strings.HasSuffix(got, "/v2"), "module path %q ends with /v2", got)
		})
	}
}

var oldQualifierRe = regexp.MustCompile(`\bdo\.[A-Z]`)

func TestModuleIdentity_NoOldPackageQualifierInStrings(t *testing.T) {
	t.Parallel()

	found := oldQualifierStrings(t, ".")

	assert.Empty(t, found, "string literals still use the old package qualifier do.")
}

func TestModuleIdentity_OldQualifierScanSkipsCheckouts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// split so that this file holds no literal with the old qualifier
	planted := "package p\n\nvar s = \"do" + ".Provide\"\n"
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(root, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
	}
	write("own.go", planted)                                 // part of the module: reported
	write(".claude/worktrees/x/.git", "gitdir: elsewhere\n") // an agent checkout
	write(".claude/worktrees/x/p.go", planted)
	write(".claude/notes/p.go", planted)       // anything under .claude
	write("vendored/.git/HEAD", "ref: main\n") // a nested repository
	write("vendored/p.go", planted)

	found := oldQualifierStrings(t, root)

	require.Len(t, found, 1, found)
	assert.Contains(t, found[0], "own.go")
}

// oldQualifierStrings returns the Go string literals under root that use
// the old package qualifier "do.". It skips .git, node_modules, testdata,
// .claude, and every nested directory holding a .git entry (another
// checkout).
func oldQualifierStrings(t *testing.T, root string) []string {
	t.Helper()

	var found []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", ".claude":
				return filepath.SkipDir
			}
			if path != root {
				if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err == nil && oldQualifierRe.MatchString(value) {
				found = append(found, fset.Position(lit.Pos()).String()+": "+value)
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)

	return found
}

type compatDoTagged struct {
	Value int `do:""`
}

type compatInjectTagged struct {
	Value int `inject:""`
}

func TestCompat_StructTagKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		opts   *InjectorOpts
		invoke func(Injector) (int, error)
		want   int
	}{
		{
			name: "default_do",
			opts: &InjectorOpts{},
			invoke: func(i Injector) (int, error) {
				s, err := InvokeStruct[compatDoTagged](i)
				return s.Value, err
			},
			want: 7,
		},
		{
			name: "override_inject",
			opts: &InjectorOpts{StructTagKey: "inject"},
			invoke: func(i Injector) (int, error) {
				s, err := InvokeStruct[compatInjectTagged](i)
				return s.Value, err
			},
			want: 7,
		},
		{
			name: "override_ignores_do",
			opts: &InjectorOpts{StructTagKey: "inject"},
			invoke: func(i Injector) (int, error) {
				s, err := InvokeStruct[compatDoTagged](i)
				return s.Value, err
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			i := NewWithOpts(tt.opts)
			ProvideValue(i, 7)

			got, err := tt.invoke(i)
			require.NoError(t, err)
			is.Equal(tt.want, got)
		})
	}
}

func TestCompat_ErrorSentinelMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sentinel error
		want     string
	}{
		{name: "ErrServiceNotFound", sentinel: ErrServiceNotFound, want: "doger: could not find service"},
		{name: "ErrServiceNotMatch", sentinel: ErrServiceNotMatch, want: "doger: could not find service satisfying interface"},
		{name: "ErrCircularDependency", sentinel: ErrCircularDependency, want: "doger: circular dependency detected"},
		{name: "ErrHealthCheckTimeout", sentinel: ErrHealthCheckTimeout, want: "doger: health check timeout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.sentinel.Error())
		})
	}
}

func TestModuleIdentity_PrefixOnce(t *testing.T) {
	t.Parallel()

	// panicMessage returns the message of the value fn panics with.
	panicMessage := func(t *testing.T, fn func()) (message string) {
		t.Helper()

		defer func() {
			recovered := recover()
			require.NotNil(t, recovered, "expected a panic")
			if err, ok := recovered.(error); ok {
				message = err.Error()
				return
			}
			message = fmt.Sprint(recovered)
		}()
		fn()

		return ""
	}

	tests := []struct {
		name    string
		message func(t *testing.T) string
		want    string
	}{
		{
			name: "start_after_shutdown",
			message: func(t *testing.T) string {
				root := New()
				root.Shutdown()
				err := root.StartWithContext(context.Background())
				require.ErrorIs(t, err, ErrShutdown)

				return err.Error()
			},
			want: "doger: cannot start: container is shut down",
		},
		{
			name: "duplicate_service_panic",
			message: func(t *testing.T) string {
				root := New()
				ProvideNamedValue(root, "db", 1)

				return panicMessage(t, func() { ProvideNamedValue(root, "db", 2) })
			},
			want: "doger: service `db` has already been declared",
		},
		{
			name: "duplicate_scope_panic",
			message: func(t *testing.T) string {
				root := New()
				root.Scope("tenant")

				return panicMessage(t, func() { root.Scope("tenant") })
			},
			want: "doger: scope `tenant` has already been declared",
		},
		{
			name: "circular_dependency_through_lazy_services",
			message: func(t *testing.T) string {
				root := New()
				ProvideNamed(root, "a", func(i Injector) (int, error) { return InvokeNamed[int](i, "b") })
				ProvideNamed(root, "b", func(i Injector) (int, error) { return InvokeNamed[int](i, "a") })
				_, err := InvokeNamed[int](root, "a")
				require.ErrorIs(t, err, ErrCircularDependency)

				return err.Error()
			},
			want: "doger: service \"a\" in scope \"[root]\": service \"b\" in scope \"[root]\": circular dependency detected: `a` -> `b` -> `a`",
		},
		{
			name: "lazy_provider_error",
			message: func(t *testing.T) string {
				root := New()
				ProvideNamed(root, "a", func(Injector) (int, error) { return 0, errors.New("boom") })
				_, err := InvokeNamed[int](root, "a")
				require.ErrorAs(t, err, new(*ServiceError))

				return err.Error()
			},
			want: "doger: service \"a\" in scope \"[root]\": boom",
		},
		{
			name: "start_rejects_dependency_cycle",
			message: func(t *testing.T) string {
				root := New()
				ProvideNamed(root, "a", func(Injector) (prefixOnceStarter, error) { return prefixOnceStarter{}, nil }, AsStarter())
				ProvideNamed(root, "b", func(i Injector) (prefixOnceStarter, error) { return InvokeNamed[prefixOnceStarter](i, "a") }, AsStarter())
				require.NoError(t, DeclareDependency(root, "a", "b"))
				err := root.Start()
				require.ErrorIs(t, err, ErrCircularDependency)

				return err.Error()
			},
			want: "doger: cannot start: circular dependency detected: `a` -> `b` -> `a`",
		},
		{
			name: "consumer_led_start_abort",
			message: func(t *testing.T) string {
				root := New()
				entered := make(chan struct{})
				ProvideNamed(root, "a", func(Injector) (*prefixOnceAbortStarter, error) {
					return &prefixOnceAbortStarter{entered: entered}, nil
				}, AsStarter())
				startErr := make(chan error, 1)
				go func() { startErr <- root.StartWithContext(context.Background()) }()
				<-entered
				root.ShutdownWithContext(context.Background())
				err := <-startErr
				require.ErrorIs(t, err, ErrShutdown)

				return err.Error()
			},
			// The Starter's failure is a StartError, so the library leads the
			// message; the consumer's "boom" follows unprefixed, and the
			// abort cause does not repeat the prefix.
			want: "doger: failed to start service `a` in scope `[root]`: boom (start aborted: container is shut down)",
		},
		{
			name: "refresh_after_shutdown",
			message: func(t *testing.T) string {
				root := New()
				root.Shutdown()
				err := root.Refresh()
				require.ErrorIs(t, err, ErrShutdown)

				return err.Error()
			},
			want: "doger: cannot start: container is shut down",
		},
		{
			name: "scope_start_after_shutdown",
			message: func(t *testing.T) string {
				root := New()
				tenant := root.Scope("tenant")
				root.Shutdown()
				err := tenant.Start()
				require.ErrorIs(t, err, ErrShutdown)

				return err.Error()
			},
			want: "doger: cannot start: container is shut down",
		},
		{
			name: "collection_member_missing_dependency",
			message: func(t *testing.T) string {
				root := New()
				ProvideNamed(root, "m", func(i Injector) (*prefixOnceMember, error) {
					_, err := InvokeNamed[int](i, "missing")
					return &prefixOnceMember{}, err
				})
				_, err := InvokeAll[*prefixOnceMember](root)
				require.ErrorIs(t, err, ErrServiceNotFound)

				return err.Error()
			},
			want: "doger: could not invoke collection member `m`: service \"m\" in scope \"[root]\": could not find service `missing`, available services: `m`, path: `m` -> `missing`",
		},
		{
			name: "extend_after_shutdown",
			message: func(t *testing.T) string {
				root := New()
				root.Shutdown()
				err := Extend(root, prefixOnceExtension{})
				require.ErrorIs(t, err, ErrShutdown)

				return err.Error()
			},
			want: "doger: extension `probe`: container is shut down: cannot install in phase closed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testWithTimeout(t, time.Second)
			is := assert.New(t)

			got := tt.message(t)
			is.Equal(tt.want, got)
			is.Equal(1, strings.Count(got, "doger: "), "doger: prefix must appear once")
			is.NotContains(got, oldLibPrefix)
		})
	}
}

// exportedErrorVars returns the names of the exported package-level
// variables named Err* declared in the non-test Go files of dir.
func exportedErrorVars(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	names := []string{}
	fset := token.NewFileSet()
	for _, entry := range entries {
		file := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
			continue
		}

		parsed, err := parser.ParseFile(fset, filepath.Join(dir, file), nil, parser.SkipObjectResolution)
		require.NoError(t, err)

		for _, decl := range parsed.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				for _, ident := range spec.(*ast.ValueSpec).Names {
					if ident.IsExported() && strings.HasPrefix(ident.Name, "Err") {
						names = append(names, ident.Name)
					}
				}
			}
		}
	}

	return names
}

// assertLibraryPrefixedOnce asserts that message carries the doger prefix
// first and exactly once, and no old DI prefix.
func assertLibraryPrefixedOnce(t *testing.T, message string) {
	t.Helper()
	is := assert.New(t)

	is.True(strings.HasPrefix(message, "doger: "), "%q must start with \"doger: \"", message)
	is.NotContains(message, oldLibPrefix)
	is.Equal(1, strings.Count(message, "doger: "), "%q must contain \"doger: \" once", message)
}

func TestModuleIdentity_AllSentinelsPrefixed(t *testing.T) {
	t.Parallel()

	// dogerclockwork (group 9) gets its own copy of this test when it is
	// created; dogergocron has one in scheduler/gocron.
	tests := []struct {
		name      string
		dir       string
		sentinels map[string]error
	}{
		{
			name: "doger",
			dir:  ".",
			sentinels: map[string]error{
				"ErrServiceNotFound":           ErrServiceNotFound,
				"ErrServiceNotMatch":           ErrServiceNotMatch,
				"ErrServiceTypeMismatch":       ErrServiceTypeMismatch,
				"ErrServiceAlreadyDeclared":    ErrServiceAlreadyDeclared,
				"ErrCircularDependency":        ErrCircularDependency,
				"ErrHealthCheckTimeout":        ErrHealthCheckTimeout,
				"ErrHealthCheckPanic":          ErrHealthCheckPanic,
				"ErrAlreadyStarted":            ErrAlreadyStarted,
				"ErrShutdown":                  ErrShutdown,
				"ErrNotStarted":                ErrNotStarted,
				"ErrNilInjector":               ErrNilInjector,
				"ErrStructInjection":           ErrStructInjection,
				"ErrScopeAlreadyDeclared":      ErrScopeAlreadyDeclared,
				"ErrProviderPanic":             ErrProviderPanic,
				"ErrShutdownPanic":             ErrShutdownPanic,
				"ErrInvalidExtension":          ErrInvalidExtension,
				"ErrExtensionDisabled":         ErrExtensionDisabled,
				"ErrRequiredExtensionDisabled": ErrRequiredExtensionDisabled,
				"ErrExtensionCycle":            ErrExtensionCycle,
				"ErrExtensionConflict":         ErrExtensionConflict,
				"ErrInvalidEvent":              ErrInvalidEvent,
				"ErrInvalidListener":           ErrInvalidListener,
				"ErrPublishAfterShutdown":      ErrPublishAfterShutdown,
			},
		},
		{
			// dogerhttp imports doger, so this package cannot import it: the
			// table stays empty and a new dogerhttp sentinel fails here until
			// dogerhttp gets its own copy of this test.
			name:      "dogerhttp",
			dir:       "http",
			sentinels: map[string]error{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			declared := exportedErrorVars(t, tt.dir)
			covered := make([]string, 0, len(tt.sentinels))
			for name := range tt.sentinels {
				covered = append(covered, name)
			}
			require.ElementsMatch(t, declared, covered, "the table must list every exported Err* variable")

			for name, sentinel := range tt.sentinels {
				t.Run(name, func(t *testing.T) {
					assertLibraryPrefixedOnce(t, sentinel.Error())
					if tt.dir == "." {
						assert.True(t, isLibMade(sentinel), "libSentinels must list %s", name)
					}
				})
			}
		})
	}
}

// prefixOnceMember is the collection member type of TestModuleIdentity_PrefixOnce.
type prefixOnceMember struct{}

// prefixOnceExtension is an extension named "probe" that registers nothing.
type prefixOnceExtension struct{}

func (prefixOnceExtension) Name() string                   { return "probe" }
func (prefixOnceExtension) Extend(*ExtensionContext) error { return nil }

// prefixOnceStarter is a Starter that does nothing.
type prefixOnceStarter struct{}

// prefixOnceAbortStarter tells when its Start runs, then returns "boom" once
// Shutdown cancelled its context.
type prefixOnceAbortStarter struct {
	entered chan struct{}
}

func (s *prefixOnceAbortStarter) Start(ctx context.Context) error {
	close(s.entered)
	<-ctx.Done()

	return errors.New("boom")
}

func (prefixOnceStarter) Start() {}
