package eventsql_test

import (
	"fmt"
	"runtime"
	"strconv"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// testWithTimeout panics, naming the test, once it has run for timeout
// without finishing: it guards the integration tests that wait on
// goroutines and databases against hangs. Call it in the test (or the
// parallel subtest) that does the work.
func testWithTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	finished := make(chan struct{})
	t.Cleanup(func() { close(finished) })

	where := t.Name()
	if _, file, line, ok := runtime.Caller(1); ok {
		where += " (" + file + ":" + strconv.Itoa(line) + ")"
	}
	go func() {
		select {
		case <-finished:
		case <-time.After(timeout):
			panic(fmt.Sprintf("%s: test timed out after %v", where, timeout))
		}
	}()
}
