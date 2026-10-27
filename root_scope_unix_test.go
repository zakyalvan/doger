//go:build unix

package doger

import (
	"context"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRootScope_ShutdownOnSignalsWithContext_Signal sends SIGUSR1 to the test
// process, so it does not run in parallel and no other test uses SIGUSR1.
func TestRootScope_ShutdownOnSignalsWithContext_Signal(t *testing.T) {
	testWithTimeout(t, 10*time.Second)
	is := assert.New(t)

	// The guard keeps SIGUSR1 handled, so a signal sent before the helper
	// registers cannot kill the process.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGUSR1)
	defer signal.Stop(guard)

	type result struct {
		sig    os.Signal
		report *ShutdownReport
	}

	signalled := 0
	for n := range 50 {
		root := New()
		probe := &ctxShutdownProbe{}
		ProvideNamed(root, "s", func(Injector) (*ctxShutdownProbe, error) { return probe, nil })
		MustInvokeNamed[*ctxShutdownProbe](root, "s")
		var closing, closed atomic.Int32
		var closingErr, closedErr error
		root.AddContainerClosingHook(func(ctx context.Context, _ *RootScope) {
			closing.Add(1)
			closingErr = ctx.Err()
		})
		root.AddContainerClosedHook(func(ctx context.Context, _ *RootScope, _ *ShutdownReport) {
			closed.Add(1)
			closedErr = ctx.Err()
		})
		// Cancelled by the same signal, right after the helper receives it.
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGUSR1)

		done := make(chan result, 1)
		go func() {
			sig, report := root.ShutdownOnSignalsWithContext(ctx, syscall.SIGUSR1)
			done <- result{sig: sig, report: report}
		}()

		// Give the helper time to register, then resend until it returns.
		time.Sleep(5 * time.Millisecond)
		var got result
	wait:
		for {
			require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGUSR1))
			select {
			case got = <-done:
				break wait
			case <-time.After(time.Millisecond):
			}
		}
		stop()

		if got.sig != nil {
			signalled++
			is.Equalf(syscall.SIGUSR1, got.sig, "repeat %d", n)
		}
		is.Truef(got.report.Succeed, "repeat %d: %s", n, got.report.Error())
		probe.mu.Lock()
		if is.Lenf(probe.ctxs, 1, "repeat %d: Shutdown calls", n) {
			is.NoErrorf(probe.ctxs[0].Err(), "repeat %d: Shutdown context", n)
		}
		probe.mu.Unlock()
		is.Equalf(int32(1), closing.Load(), "repeat %d: Closing hook runs", n)
		is.Equalf(int32(1), closed.Load(), "repeat %d: Closed hook runs", n)
		is.NoErrorf(closingErr, "repeat %d: Closing hook context", n)
		is.NoErrorf(closedErr, "repeat %d: Closed hook context", n)
	}
	is.Positive(signalled, "no repeat returned the signal, so the signal branch never ran")
}
