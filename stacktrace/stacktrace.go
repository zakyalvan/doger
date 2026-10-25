package stacktrace

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
)

///
/// Inspired by palantir/stacktrace repo
/// -> https://github.com/palantir/stacktrace/blob/master/stacktrace.go
/// -> Apache 2.0 LICENSE
///

var (
	// goroot is read once at package init: runtime.GOROOT() re-reads an
	// env var on every call, and it cannot change during the process lifetime.
	//nolint:staticcheck
	goroot = runtime.GOROOT()

	// selfPkgPath is the import path of this package, derived at runtime so
	// that frame classification does not depend on the module path.
	selfPkgPath = reflect.TypeFor[pkgMarker]().PkgPath()
)

// pkgMarker is an empty type used to derive this package's import path.
type pkgMarker struct{}

// skipFrame reports whether a frame must be skipped when looking for the
// caller: frames in GOROOT, and frames of the package selfPkg except those
// declared in _test.go files. function is the fully qualified function name.
func skipFrame(selfPkg, goroot, function, file string) bool {
	if goroot != "" && strings.HasPrefix(file, goroot+"/") {
		return true
	}

	return strings.HasPrefix(function, selfPkg+".") && !strings.HasSuffix(file, "_test.go")
}

// NewFrameFromCaller creates a new Frame from the current call stack.
// This function walks up the call stack to find the first frame that is not
// in the stacktrace package or Go runtime, providing useful debugging
// information about where a service was invoked from.
//
// The function filters out:
//   - Frames in the Go runtime (GOROOT)
//   - Frames of the stacktrace package itself, except those declared in
//     _test.go files
//
// Frames of other library packages are reported: the caller of a service
// constructor is the library code that built it.
//
// Returns a Frame representing the caller and a boolean indicating success.
// The boolean is false if no suitable frame was found.
//
// This function is used internally by the DI container to track service
// invocation locations for debugging and explanation purposes.
func NewFrameFromCaller() (Frame, bool) {
	return callerFrame(runtime.Caller, funcNameForPC, 10)
}

// callerFrame returns the first of the first depth frames reported by caller
// that skipFrame does not skip. Index 0 of caller is callerFrame itself.
func callerFrame(
	caller func(skip int) (pc uintptr, file string, line int, ok bool),
	funcName func(pc uintptr) (string, bool),
	depth int,
) (Frame, bool) {
	// find the first frame that is not in this package
	for i := 0; i < depth; i++ {
		pc, file, line, ok := caller(i)
		if !ok {
			break
		}

		name, ok := funcName(pc)
		if !ok {
			break
		}
		if !skipFrame(selfPkgPath, goroot, name, file) {
			return newFrame(pc, file, name, line), true
		}
	}

	return Frame{}, false
}

// funcNameForPC returns the fully qualified name of the function containing
// pc, or false when pc does not resolve to a function.
func funcNameForPC(pc uintptr) (string, bool) {
	f := runtime.FuncForPC(pc)
	if f == nil {
		return "", false
	}

	return f.Name(), true
}

// NewFrameFromPC creates a new Frame from a program counter (PC) value.
// This function is used to create Frame objects from function pointers,
// typically for tracking where service providers were defined.
//
// Parameters:
//   - pc: The program counter value representing a function
//
// Returns a Frame representing the function and a boolean indicating success.
// The boolean is false if the PC value is invalid.
//
// This function is used internally to track service provider locations
// for debugging and explanation purposes.
func NewFrameFromPC(pc uintptr) (Frame, bool) {
	fun := runtime.FuncForPC(pc)
	if fun == nil {
		return Frame{}, false
	}

	file, line := fun.FileLine(pc)

	return newFrame(pc, file, fun.Name(), line), true
}

// newFrame builds the reported Frame from a raw file path and a fully
// qualified function name: the path is made GOPATH-relative and the name
// is shortened.
func newFrame(pc uintptr, file, function string, line int) Frame {
	return Frame{
		PC:       pc,
		File:     removeGoPath(file),
		Function: shortFuncName(function),
		Line:     line,
	}
}

// Frame represents a single stack frame with debugging information.
// This struct contains information about a function call location,
// including the file, line number, function name, and program counter.
type Frame struct {
	PC       uintptr
	File     string
	Function string
	Line     int
}

// String returns a formatted string representation of the frame.
// The format is "file:function:line" which is useful for debugging
// and logging purposes.
//
// Returns a string in the format "path/to/file.go:FunctionName:123".
func (f Frame) String() string {
	return fmt.Sprintf("%s:%s:%d", f.File, f.Function, f.Line)
}

// ptrReceiverReplacer strips the pointer-receiver syntax left over after
// splitting off the package path, e.g. "(*PtrReceiver).MethodName" ->
// "PtrReceiver.MethodName". The pattern is "(*" (not bare "(" and "*"
// separately) so a generic receiver's type arguments, which can themselves
// contain '*' (e.g. "(*ServiceLazy[*int]).getInstance"), are left untouched;
// NewReplacer tries the longest match at each position, so "(*" wins over
// the bare "(" fallback whenever both would apply.
var ptrReceiverReplacer = strings.NewReplacer("(*", "", "(", "", ")", "")

func shortFuncName(longName string) string {
	// f.Name() is like one of these:
	// - "github.com/palantir/shield/package.FuncName"
	// - "github.com/palantir/shield/package.Receiver.MethodName"
	// - "github.com/palantir/shield/package.(*PtrReceiver).MethodName"
	withoutPath := longName[strings.LastIndex(longName, "/")+1:]
	withoutPackage := withoutPath[strings.Index(withoutPath, ".")+1:]

	return ptrReceiverReplacer.Replace(withoutPackage)
}
