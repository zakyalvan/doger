package doger

import (
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
)

type ctxTestkey string

const (
	ctxTestKey ctxTestkey = "test-key"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// minTestTimeout is the least time testWithTimeout waits before it reports
// a test as hung.
const minTestTimeout = 5 * time.Second

// testWithTimeout panics, naming the test, once it has run for timeout
// without finishing. It guards against hangs, not timing: a timeout below
// minTestTimeout is raised to it, so a test starved of CPU under -race and
// parallel load is not reported as hung. Tests assert timing themselves, on
// a fake clock or in a synctest bubble.
//
// https://github.com/stretchr/testify/issues/1101
func testWithTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	timeout = max(timeout, minTestTimeout)

	testFinished := make(chan struct{})

	t.Cleanup(func() {
		close(testFinished)
	})

	line := ""
	funcName := ""

	var pc [1]uintptr
	n := runtime.Callers(2, pc[:])
	if n > 0 {
		frames := runtime.CallersFrames(pc[:])
		frame, _ := frames.Next()
		line = frame.File + ":" + strconv.Itoa(frame.Line)
		funcName = frame.Function
	}

	go func() {
		select {
		case <-testFinished:
		case <-time.After(timeout):
			if line == "" || funcName == "" {
				panic(fmt.Sprintf("Test timed out after: %v", timeout))
			}
			panic(fmt.Sprintf("%s: Test timed out after: %v\n%s", funcName, timeout, line))

			// t.Errorf("Test timed out after: %v", timeout)
			// os.Exit(1)
		}
	}()
}

// pkgName holds the current package name (eg. "do"). It won't break
// if the package name is changed, in case of a fork or anything.
var pkgName = func() string {
	t := reflect.TypeFor[__pkg_probe_type]()
	s := t.String() // eg. "do.__pkg_probe_type"
	if i := strings.IndexByte(s, '.'); i > 0 {
		return s[:i]
	}
	return "do"
}()

type __pkg_probe_type struct{}
