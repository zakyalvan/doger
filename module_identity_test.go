package doger

import (
	"bufio"
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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const modulePath = "github.com/kartaladev/doger"

// readModulePath returns the module path declared by the go.mod file in dir.
func readModulePath(t *testing.T, dir string) string {
	t.Helper()

	f, err := os.Open(filepath.Join(dir, "go.mod"))
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

	f, err := os.Open(filepath.Join(dir, "go.work"))
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

	var found []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata":
				return filepath.SkipDir
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

	assert.Empty(t, found, "string literals still use the old package qualifier do.")
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
		{name: "ErrServiceNotFound", sentinel: ErrServiceNotFound, want: "DI: could not find service"},
		{name: "ErrServiceNotMatch", sentinel: ErrServiceNotMatch, want: "DI: could not find service satisfying interface"},
		{name: "ErrCircularDependency", sentinel: ErrCircularDependency, want: "DI: circular dependency detected"},
		{name: "ErrHealthCheckTimeout", sentinel: ErrHealthCheckTimeout, want: "DI: health check timeout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.sentinel.Error())
		})
	}
}
