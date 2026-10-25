package stacktrace

import (
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkipFrame_Layouts(t *testing.T) {
	t.Parallel()

	const goroot = "/usr/local/go"

	tests := []struct {
		name     string
		lib      string
		function string
		file     string
		skipped  bool
	}{
		{name: "own_checkout_do", lib: "github.com/kartaladev/doger", function: "github.com/kartaladev/doger/stacktrace.NewFrameFromCaller", file: "/home/u/src/do/stacktrace/stacktrace.go", skipped: true},
		{name: "own_checkout_doger", lib: "github.com/kartaladev/doger", function: "github.com/kartaladev/doger/stacktrace.NewFrameFromCaller", file: "/home/u/src/doger/stacktrace/stacktrace.go", skipped: true},
		{name: "own_checkout_my_fork", lib: "github.com/kartaladev/doger", function: "github.com/kartaladev/doger/stacktrace.NewFrameFromCaller", file: "/home/u/src/my-fork/stacktrace/stacktrace.go", skipped: true},
		{name: "own_module_cache", lib: "github.com/kartaladev/doger", function: "github.com/kartaladev/doger/stacktrace.NewFrameFromCaller", file: "/home/u/go/pkg/mod/github.com/kartaladev/doger@v0.1.0/stacktrace/stacktrace.go", skipped: true},
		{name: "own_module_cache_renamed", lib: "example.com/fork", function: "example.com/fork/stacktrace.NewFrameFromCaller", file: "/home/u/go/pkg/mod/example.com/fork@v0.1.0/stacktrace/stacktrace.go", skipped: true},
		{name: "own_vendored", lib: "github.com/kartaladev/doger", function: "github.com/kartaladev/doger/stacktrace.NewFrameFromCaller", file: "/home/u/app/vendor/github.com/kartaladev/doger/stacktrace/stacktrace.go", skipped: true},
		{name: "own_closure", lib: "github.com/kartaladev/doger", function: "github.com/kartaladev/doger/stacktrace.NewFrameFromCaller.func1", file: "/home/u/src/doger/stacktrace/stacktrace.go", skipped: true},
		{name: "own_test_helper_doger", lib: "github.com/kartaladev/doger", function: "github.com/kartaladev/doger/stacktrace.example1", file: "/home/u/src/doger/stacktrace/stacktrace_test.go", skipped: false},
		{name: "own_test_helper_my_fork", lib: "github.com/kartaladev/doger", function: "github.com/kartaladev/doger/stacktrace.example1", file: "/home/u/src/my-fork/stacktrace/stacktrace_test.go", skipped: false},
		{name: "lib_generic_func", lib: "github.com/kartaladev/doger", function: "github.com/kartaladev/doger.InvokeNamed[...]", file: "/home/u/src/doger/di.go", skipped: false},
		{name: "lib_generic_method", lib: "github.com/kartaladev/doger", function: "github.com/kartaladev/doger.(*serviceLazy[...]).getInstance", file: "/home/u/src/doger/service_lazy.go", skipped: false},
		{name: "external_test_package", lib: "github.com/kartaladev/doger", function: "github.com/kartaladev/doger/stacktrace_test.TestCaller", file: "/home/u/src/doger/stacktrace/caller_test.go", skipped: false},
		{name: "lookalike_palantir", lib: "github.com/kartaladev/doger", function: "github.com/palantir/stacktrace.Propagate", file: "/home/u/go/pkg/mod/github.com/palantir/stacktrace@v0.0.0/stacktrace.go", skipped: false},
		{name: "lookalike_acme_do", lib: "github.com/kartaladev/doger", function: "github.com/acme/do/stacktrace.Capture", file: "/home/u/src/acme/do/stacktrace/capture.go", skipped: false},
		{name: "sibling_stacktracex", lib: "github.com/kartaladev/doger", function: "github.com/kartaladev/doger/stacktracex.Helper", file: "/home/u/src/doger/stacktracex/helper.go", skipped: false},
		{name: "consumer_main", lib: "github.com/kartaladev/doger", function: "main.main", file: "/home/u/app/main.go", skipped: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			is.Equal(tt.skipped, skipFrame(tt.lib+"/stacktrace", goroot, tt.function, tt.file))
		})
	}
}

func TestSkipFrame_GOROOTPrefix(t *testing.T) {
	t.Parallel()

	const (
		selfPkg = "github.com/kartaladev/doger/stacktrace"
		goroot  = "/usr/local/go"
	)

	tests := []struct {
		name     string
		function string
		file     string
		skipped  bool
	}{
		{name: "goroot_runtime_goexit", function: "runtime.goexit", file: "/usr/local/go/src/runtime/asm_arm64.s", skipped: true},
		{name: "goroot_testing_trunner", function: "testing.tRunner", file: "/usr/local/go/src/testing/testing.go", skipped: true},
		{name: "goroot_sibling_gopath", function: "main.main", file: "/usr/local/gopath/src/app/main.go", skipped: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			is.Equal(tt.skipped, skipFrame(selfPkg, goroot, tt.function, tt.file))
		})
	}
}

func TestSkipFrame_EmptyGOROOT(t *testing.T) {
	t.Parallel()

	const selfPkg = "github.com/kartaladev/doger/stacktrace"

	tests := []struct {
		name     string
		function string
		file     string
		skipped  bool
	}{
		{name: "goroot_empty_consumer", function: "main.main", file: "example.com/app/main.go", skipped: false},
		{name: "goroot_empty_own_frame", function: "github.com/kartaladev/doger/stacktrace.NewFrameFromCaller", file: "github.com/kartaladev/doger@v0.1.0/stacktrace/stacktrace.go", skipped: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			is.Equal(tt.skipped, skipFrame(selfPkg, "", tt.function, tt.file))
		})
	}
}

func TestCallerFrame_SearchDepth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		skippable int
		found     bool
	}{
		{name: "9_skippable_then_reportable", skippable: 9, found: true},
		{name: "10_skippable_then_reportable", skippable: 10, found: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)

			// Index 0 stands for callerFrame itself; indexes below
			// tt.skippable are own-package frames, the next one is the
			// reportable consumer frame, and the stack ends after it.
			caller := func(skip int) (uintptr, string, int, bool) {
				switch {
				case skip < tt.skippable:
					return uintptr(skip + 1), "/home/u/src/doger/stacktrace/stacktrace.go", 1, true
				case skip == tt.skippable:
					return uintptr(skip + 1), "/home/u/app/main.go", 7, true
				default:
					return 0, "", 0, false
				}
			}
			funcName := func(pc uintptr) (string, bool) {
				if int(pc) <= tt.skippable {
					return selfPkgPath + ".NewFrameFromCaller", true
				}
				return "main.main", true
			}

			var (
				frame Frame
				found bool
			)
			is.NotPanics(func() {
				frame, found = callerFrame(caller, funcName, 10)
			})
			is.Equal(tt.found, found)
			if tt.found {
				is.Equal("/home/u/app/main.go", frame.File)
				is.Equal("main", frame.Function)
				is.Equal(7, frame.Line)
			} else {
				is.Equal(Frame{}, frame)
			}
		})
	}
}

// TestSelfPkgPath_MatchesSymbolPrefix guards the own-package rule of
// skipFrame: the linker escapes dots in the last import-path element of
// symbol names, so selfPkgPath only matches while that element has none.
func TestSelfPkgPath_MatchesSymbolPrefix(t *testing.T) {
	t.Parallel()
	must := require.New(t)

	f := runtime.FuncForPC(reflect.ValueOf(NewFrameFromCaller).Pointer())
	must.NotNil(f)
	must.True(strings.HasPrefix(f.Name(), selfPkgPath+"."), "symbol %q, package path %q", f.Name(), selfPkgPath)
}
